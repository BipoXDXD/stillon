# PR 7 — Contract do multi-inquilino: plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: superpowers:subagent-driven-development (recomendada) ou superpowers:executing-plans, tarefa a tarefa. Os passos usam checkbox (`- [ ]`).

**Objetivo:** fechar o expand → migrate → contract da Etapa 1:
- `project_id NOT NULL` nas quatro tabelas de recurso;
- o fim do preenchimento automático e dos fallbacks por `user_id`;
- o fim das rotas sem `/projects/{projectID}`;
- o fim do Projeto padrão protegido.

**Arquitetura:**
- **Banco:** duas migrations (C e C+1), separadas para que o `VALIDATE` não rode sob o lock exclusivo do `ADD CONSTRAINT`.
  - A C confere que não há linha sem projeto, tira o gatilho de preenchimento e põe um `CHECK … NOT VALID`.
  - A C+1 valida, faz o `SET NOT NULL` (sem varrer a tabela, por causa do CHECK validado), reescreve a guarda das notificações sem `user_id` e apaga a função de cota de canais que ficou sem uso.
- **Go:** os tipos do sqlc perdem o ponteiro, os fallbacks somem, e as rotas antigas saem com o middleware `ProjetoPadrao` e o `DefaultScope`.
- **Ordem:** os chamadores (testes, e2e, scripts) migram antes, num sub-PR só de testes. A remoção das rotas vira só remoção.

**Stack:** Go 1.26.11, PostgreSQL 17/18, pgx v5 + sqlc, goose, testcontainers; React 19, Vitest + MSW, Playwright.

**Spec:** [`docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`](../specs/2026-11-22-multiinquilino-cadastro-design.md), seções "Dados e migração" (item 3, Contract), "Isolamento (RLS)", "API e painel" e "Fica com o builder". Roteiro: linha "7" de [`2026-11-22-etapa1-multiinquilino.md`](2026-11-22-etapa1-multiinquilino.md). Portão herdado: "PR 7 (contract)" em § "Portões para PRs futuros" de [`2026-11-22-pr2b-pertenca-projetos.md`](2026-11-22-pr2b-pertenca-projetos.md). Pendências deixadas para cá: risco 7 de [`2026-11-27-pr5-cotas-abuso.md`](2026-11-27-pr5-cotas-abuso.md) e B8 e a última linha do plano do PR 6 (`2026-11-28-pr6-exportar-excluir-legais.md`, PR #466).

## O que já está feito (não refazer)

O contract da spec foi adiantado em parte pelo 2b. Conferido na `main` em `e49adf30`:

| Item do contract na spec | Onde já está |
|---|---|
| Índices `(project_id, id DESC)` em `targets`, `heartbeats`, `notif_channels` | 00040 |
| `status_pages (project_id)` único (`status_pages_project_key`) | 00040 |
| Remoção de `status_pages_user_id_key` | 00046 |
| Políticas de recurso por `app.project_id` (`<t>_do_projeto`) e as de posse indireta pelo `project_id` do pai | 00044 |
| Gatilho de imutabilidade estrito (`guard_project_id_imutavel` sem o ramo `OLD.project_id IS NOT NULL`) | 00044 |
| Backfill encerrado (`backfill_projetos_padrao` apagada) | 00044 |
| Painel chamando só `/projects/{id}/…` (`criarApiDoProjeto`, `web/src/lib/api/cliente.ts:139`) | 2b.4 |

Desde a 00044, o escopo de usuário não grava recurso (`TestOlderBinaryInsertsAreRefusedInUserScope`). Então nenhuma linha nova sem projeto pode ter nascido depois dela; a pré-checagem da C confirma isso.

## O que falta (o escopo deste plano)

1. `project_id NOT NULL` em `targets`, `heartbeats`, `notif_channels`, `status_pages`.
2. Gatilho e função `preencher_project_id_padrao` (00040).
3. Fallback por `user_id`:
   - em `ListDeliverableChannelsForIncident` (`queries/incidents.sql:43-56`);
   - em `guardar_notificacao_no_projeto` (00043).
4. `LockUser` da cota de canais (`internal/notify/service.go:277`); a chave advisory `LockTitularChannels` basta.
5. `cota_canais_do_titular` e a política `notif_channels_cota_select` (00045), sem uso depois do 5.2.
6. `constraintUser` em `internal/statuspage/service.go:22,278`.
7. Rotas antigas, `middleware.ProjetoPadrao`, `ResolvedorDeProjeto.DefaultScope`, `tenancy.Service.DefaultScope`, `pgstore.Store.EnsureDefault`.
8. Projeto padrão protegido: `ErrDefaultProjectProtected` / `default_project_protected` do PR 6 (B8).
9. Destino de `user_id`: decidido em B1.

## Restrições globais

**Processo**
- Tudo em pt-BR: código novo, nomes de teste, comentários, commits (`tipo(escopo): descrição`) e PRs.
- Busca com `git grep` / `git ls-files`, nunca `grep -r` da raiz: `.claude/` guarda outras worktrees.
- Nada é declarado pronto sem `go build ./... && go vet ./... && make test`. Durante a tarefa, `go test -race -count=1 -p 1 <pacotes afetados>`. Painel: `cd web && npm test && npm run typecheck && npm run lint`.
- Um teste só conta depois de visto falhando com o código sabotado. Cada teste deste plano traz a sua sabotagem. Quando o teste só prova remoção de código morto, o plano diz isso e não inventa sabotagem.
- Um comando pesado por vez (Docker, Playwright); remova os contêineres que criar.

**Migrations**
- Números relativos: **C** e **C+1**, os próximos livres no dia do merge. Hoje a `main` está na 00048; 4.2 = 00049, 4.3 = 00050, PR 5 = N…N+3, PR 6 = M e M+1. Se todos saírem antes: C ≈ 00057.
- `storage.Migrate` recusa número menor que o aplicado: se outra frente publicar migration antes, renumere em sequência antes do merge, sem buraco. A 00015 fica vaga.
- Toda migration começa com `SET LOCAL lock_timeout = '5s'`, em Up e em Down, e tem Down que devolve o catálogo exato da anterior.
- Este plano **não cria índice**: os de projeto já existem (00040). Por isso nenhuma migration é `NO TRANSACTION`.
  - Se um dia precisar de índice, siga a 00037: `-- +goose NO TRANSACTION`, `SET lock_timeout` (sem `LOCAL`, que não vale fora de transação), `DROP INDEX CONCURRENTLY IF EXISTS` antes do `CREATE INDEX CONCURRENTLY` e `RESET lock_timeout`.
  - Índice em `checks` (particionada) nem assim: `CONCURRENTLY` não vale na mãe.
- Este plano não toca `checks`, `check_rollup_1h` nem `internal/storage/maintenance.go`: as tabelas grandes são de posse indireta e já filtram pelo pai.

**Release**
- Merge é deploy. Todo sub-PR sobe com o binário anterior servindo enquanto as migrations rodam.
- Depois do merge, confira o run do `Coolify release` do SHA (`gh run list --workflow coolify.yml`) e o `readyz`.
- Merge fora de 00:00–01:00 e 03:10–03:45 BRT.

**Contratos fechados do servidor**
- Código de erro removido sai de `internal/apierr/errors.go` **e** de `ERROS_DO_SERVIDOR` em `web/src/test/api-falsa.ts`.
- `set_config` só em `internal/storage/scope.go`.

**Modelo**
- Implementadores e revisores: Opus, esforço medium. Nunca Haiku.

## Foco da revisão

1. **Release recusado por lock.** O sondador segura linhas de `targets` o tempo todo. Se a C+1 não pega o lock em 5 s:
   - esperado: a migration desiste sem efeito parcial, o boot do binário novo cai, o anterior segue servindo, e rodar de novo resolve;
   - teste: `TestContractDesistePorLockSemEfeitoParcial` (T4).
2. **Aviso perdido quando o criador do recurso excluiu a conta.** Depois do PR 6, `user_id` pode ser NULL. A guarda da 00043 trata `recurso_dono IS NULL` como "recurso escondido" e descarta o aviso.
   - esperado: o aviso entra no canal do mesmo projeto;
   - teste: `TestNotificacaoComCriadorApagadoPassa` (T4). Ver o risco R1.
3. **Linha sem projeto na produção.**
   - esperado: a C aborta com mensagem que nomeia tabela e contagem, e não apaga nada (D3);
   - teste: `TestContractRecusaLinhaSemProjeto` (T3).
4. **Cliente antigo nas rotas sem projeto.**
   - esperado: 404 com o envelope comum, sem criar Projeto padrão, mesmo para quem não tem nenhum;
   - testes: `TestLegacyResourceRoutesAreGone` e `TestLegacyRouteDoesNotCreateDefaultProject` (T6).
5. **Quem exclui o Projeto padrão fica sem rumo.**
   - esperado: o painel abre outro projeto, ou o estado vazio que cria um; `GET /projects` não recria o padrão; a cota de projetos libera;
   - testes: `TestExcluirProjetoPadraoPermitido`, `TestSemProjetoPadraoNadaORecria` (T7) e o caso do painel em T7.

---

## Decisões do dono

Não decido estas. O plano segue a recomendação de cada uma e diz o que muda se a escolha for outra.

**D1 — Quando desligar as rotas antigas**

Fatos:
- O painel embutido no binário só chama `/projects/{id}/…` desde o 2b.4.
- Não existe chave de API (é da etapa 4).
- Logo, só tem credencial quem usa o painel ou guarda o cookie de refresh num script.

| Opção | O que é | Prós | Contras |
|---|---|---|---|
| A (recomendada) | Desligar no 7.3 assim que o 7.1 estiver na `main`, sem janela | Nada novo para operar | Um script pessoal com cookie de refresh quebraria (só o dono teria um) |
| B | Janela de 14 dias com a telemetria do 7.0 (log `rota_legada` por chamada) antes do 7.3 | Prova de uso real | Os logs do contêiner somem a cada deploy (cada merge): o Claude teria de conferir antes de cada merge da janela; mais um sub-PR |
| C | Manter as rotas antigas como apelido do Projeto padrão para sempre | Zero quebra | Contraria a spec ("saem no PR de contract"); prende `EnsureDefault`, o padrão protegido (D4) e as duas famílias de rota |

Com B, o sub-PR 7.0 (T0) entra antes e o 7.3 espera a janela. Com C, o 7.3 sai do plano e D4 fica em B.

**D2 — O que a rota antiga responde depois de desligada**

| Opção | O que é | Prós | Contras |
|---|---|---|---|
| A (recomendada) | 404 `not_found`, o mesmo de qualquer rota inexistente | Nenhum código novo; não revela nada | Quem chamar não sabe para onde foi |
| B | 410 `route_moved`, com o caminho novo na mensagem, por 30 dias, e depois 404 | Explica a mudança | Código de erro novo, um handler-coringa e mais um PR para tirá-lo, para clientes que não existem (D1) |

**D3 — Linhas sem projeto achadas na pré-checagem** (perda de dados)

Esperado: zero (ver "O que já está feito"). A contagem na produção é o passo 0 da T3. Esta decisão só vale se der mais que zero.

| Opção | O que é | Prós | Contras |
|---|---|---|---|
| A (recomendada) | A C aborta; o release falha com o binário anterior servindo; dono e Claude olham caso a caso | Nada se perde sem alguém ver | O PR 7 para até resolver |
| B | A C apaga as linhas | Segue sozinho | Perda silenciosa de monitor, que é anti-meta da spec |
| C | A C anexa cada linha ao Projeto padrão do `user_id`, criando o projeto se faltar | Nada se perde | Ressuscita a lógica do backfill, que saiu na 00044; linha com `user_id` NULL não tem para onde ir |

**D4 — Projeto padrão depois do contract** (o PR 6, B8, deixou para cá)

| Opção | O que é | Prós | Contras |
|---|---|---|---|
| A (recomendada) | Pode ser excluído como qualquer outro (owner + nome digitado). `is_default` fica como marcador do projeto inicial: o painel ainda o prefere ao abrir a raiz e nos links antigos | Uniforme; a cota de 3 projetos não fica presa num projeto que não sai | A pessoa pode apagar os monitores de antes com dois cliques (mas digitando o nome) |
| B | Continua protegido para sempre | Ninguém perde o projeto inicial | Ocupa 1 das 3 vagas para sempre; regra especial sem motivo depois das rotas antigas |
| C | Pode ser excluído e a coluna `is_default` sai | Modelo mais simples | Mais um contract (a coluna é lida pelo binário anterior), e o painel perde o critério de abertura |

**D5 — Telas** — nenhuma tela nova. A única mudança visível (o botão "Excluir projeto" aparece também no padrão, com D4 = A) segue o diálogo que o dono escolher no D1 do PR 6.

## Decisões que eu tomei

- **B1. `user_id` fica nas tabelas de recurso, com o mesmo nome, como "quem criou".** Ganha `COMMENT ON COLUMN` dizendo isso.
  - Por que fica: o PR 6 já o tornou `ON DELETE SET NULL`; `DisableEmailChannelsCreatedBy` (PR 4) e o D4 do PR 6 dependem dele; é a trilha de quem criou o recurso.
  - Por que não renomear para `created_by`: custaria um expand/contract próprio, porque o binário anterior lê `user_id`.
- **B2. Os índices `*_user_idx (user_id, id DESC)` ficam.** Servem à ação `SET NULL` da FK quando uma conta é excluída e ao `DisableEmailChannelsCreatedBy`. Trocar por `(user_id)` não vale uma migration.
- **B3. `NOT NULL` em duas migrations: CHECK `NOT VALID` na C; `VALIDATE` + `SET NOT NULL` + `DROP CONSTRAINT` na C+1.**
  - O goose roda cada arquivo na sua transação. Assim a varredura do `VALIDATE` roda sob `SHARE UPDATE EXCLUSIVE`, sem bloquear o sondador.
  - O `SET NOT NULL` só pega o `ACCESS EXCLUSIVE` por um instante: o Postgres 12+ usa o CHECK validado e não varre.
  - As tabelas são pequenas, mas o molde custa pouco e é o mesmo das grandes.
- **B4. A pré-checagem é um bloco `DO` no começo da C,** que dá `RAISE EXCEPTION` com tabela e contagem (D3 = A). O `VALIDATE` falharia de qualquer jeito, mas com uma mensagem pior e só na C+1.
- **B5. A guarda das notificações passa a comparar só projetos.** "Recurso escondido pela RLS" passa a ser `recurso_projeto IS NULL`, que com o `NOT NULL` só acontece quando a linha não é visível. Com isso ela deixa de ler `user_id`, que pode ser NULL depois do PR 6 (R1).
- **B6. As restritivas `<t>_so_projeto_do_escopo` (00042) ficam.** Com as permissivas `<t>_do_projeto` elas são redundantes hoje. Continuam como segunda barreira caso um escopo de serviço (sondador, ping) um dia ligue `app.project_id`. Custam um `OR` por linha.
- **B7. Os fallbacks por `user_id` saem sem teste novo.** Com o `NOT NULL`, o ramo `(… IS NULL) AND c.user_id = …` é inalcançável, então nenhum teste distingue a versão com e sem ele. Os testes de fan-out por projeto que já existem continuam passando.
- **B8. `LockUser` e a query `CountTitularChannels` saem do Go no 7.2; a função SQL e a política saem na C+1 do mesmo sub-PR.** Condição: o 5.2 publicado, pois o binário que roda durante o release do 7.2 já conta por projeto. As duas versões usam a mesma chave advisory, então contam em fila.
- **B9. Rotas antigas saem por inteiro**, com `ProjetoPadrao`, `DefaultScope` (interface e serviço) e `pgstore.Store.EnsureDefault`/`criarPadrao`. Ficam:
  - `EnsureDefaultProject` e `GetDefaultProjectID`, que o cadastro e o CLI usam por `storage.CriarProjetoPadraoNaTx`: conta nova continua nascendo com o "Projeto padrão";
  - o redirecionamento de links antigos do painel (`RedirecionarAoProjeto`): é só front e cobre favoritos.
- **B10. Os chamadores migram num sub-PR próprio, antes da remoção.** Uma guarda estática (`TestNenhumChamadorUsaRotaLegada`) impede que um PR em paralelo (4.x, 5.x, 6.x) traga chamada nova a rota antiga.
- **B11. O ensaio do piso (`deploy/ci/ensaiar-piso.sh`) passa a usar as rotas de projeto.** O piso tem de ser uma tag com o 2b.2 ou mais nova. O cabeçalho do script diz isso. O piso do SP1 PR 1 deixa de servir: ele não tem rotas de projeto.
- **B12. `constraintUser` sai de `statuspage.traduzir`.** O UNIQUE por usuário não existe desde a 00046, e o Down da 00046 está fora de alcance depois da C+1.

## Sub-PRs

| Sub-PR | Tarefas | Migration | Depende de | Binário anterior durante o release |
|---|---|---|---|---|
| 7.0 Telemetria (só com D1 = B) | T0 | — | nada | só ganha um log |
| 7.1 Chamadores nas rotas de projeto | T1, T2 | — | nada | código de produção intacto |
| 7.2 Contract do schema | T3, T4, T5 | C, C+1 | 5.2 e 6.2 **publicados**; publicar **antes do 6.3** (R1) | Ver abaixo |
| 7.3 Fim das rotas antigas e do padrão protegido | T6, T7 | — | 7.1 na `main`; D1, D2, D4; 6.2 (DELETE de projeto) e 6.5 (painel) na `main` | Serve as rotas antigas que o painel novo não chama |

**Por que o 7.2 não quebra o binário anterior (o do PR 6):**
- Ele sempre grava `project_id` dentro de `InProjectTx`, porque a política `WITH CHECK` exige igualdade. O gatilho que sai nunca agia para ele, e o CHECK nunca o recusa.
- Os fallbacks dele por `user_id` ficam inalcançáveis, sem erro.
- Ele não chama `cota_canais_do_titular` (5.2).
- Ele toma `LockUser` + a chave advisory; o novo toma só a advisory. A chave é a mesma, então contam em fila.

**Por que o 7.3 não quebra:** painel e servidor saem juntos no binário, e o painel não chama rota antiga desde o 2b.4. Uma aba aberta com um bundle anterior ao 2b.4 receberia 404 e voltaria com um recarregamento.

**Ordem:** 7.1 e 7.2 em paralelo; o 7.3 depois do 7.1. O 7.2 e o 7.3 não dependem um do outro.

## Estrutura de arquivos

**Criar**
- `internal/storage/migrations/<C>_contract_preparar_projeto_obrigatorio.sql`
- `internal/storage/migrations/<C+1>_contract_projeto_obrigatorio.sql`
- `internal/storage/contract_projeto_test.go`: testes de T3 e T4.
- `internal/httpapi/rota_padrao_test.go`: helper `rotaNoPadrao` (T1).
- `internal/httpapi/sem_rota_legada_test.go`: guarda estática (T1).
- `internal/httpapi/rotas_legadas_integration_test.go`: T6.

**Alterar**
- `internal/storage/queries/{incidents,channels}.sql` e `internal/storage/db/*` (regerados).
- `internal/notify/service.go`, `internal/statuspage/service.go`.
- `internal/httpapi/routes.go`, `internal/httpapi/middleware/projeto.go`.
- `internal/tenancy/{service,ports,role}.go`, `internal/tenancy/pgstore/store.go`.
- `internal/apierr/errors.go`, `web/src/test/api-falsa.ts`.
- Painel: o componente de configurações do projeto que o 6.5 criar (botão "Excluir projeto").
- Testes de integração em `internal/httpapi/*_integration_test.go` (lista em T1), `cmd/stillon/{incident,teto_regras}_e2e_test.go`, `web/e2e/{fluxo,jornadas,pagina-publica}.spec.ts`.
- `deploy/compose/seed.sh`, `deploy/ci/memoria/carga.py`, `deploy/ci/test_carga_memoria.py`, `deploy/ci/ensaiar-piso.sh`.
- `README.md`, `CLAUDE.md` (linha das migrations e Orientação).

**Apagar**
- `internal/httpapi/projeto_padrao_integration_test.go` (`TestLegacyRouteEnsuresDefaultProject`).
- `internal/httpapi/rotas_projeto_paridade_test.go`: vira `TestNoResourceRouteOutsideAProject`, no mesmo arquivo renomeado para `rotas_so_por_projeto_test.go`.
- Testes do gatilho e do fallback (lista em T5).

---

## Tarefas

### Sub-PR 7.0 — Telemetria das rotas antigas (só com D1 = B)

#### T0: log `rota_legada`

**Arquivos:** criar `internal/httpapi/middleware/rota_legada.go` (+ teste); alterar `routes.go` (`recursoLegado`, `comEnvioLegado`).

**Interfaces:**
- Produz `middleware.RotaLegada(next http.Handler) http.Handler`.
  - Vai depois de `viva`, para contar só sessão viva e não varredura anônima.
  - Faz `slog.Warn("rota_legada", "padrao", r.Pattern, "user_agent", <até 120 runas>, "trace_id", observability.TraceID(ctx))`.
  - Sem `user_id` nem IP.

- [ ] **Passo 1 — testes que falham** (`internal/httpapi/rotas_legadas_integration_test.go`, com um `slog.Handler` de captura):

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestRotaLegadaRegistraUso` | `GET /api/v1/targets` com sessão gera um registro `rota_legada` com `padrao = "GET /api/v1/targets"` | não chamar o log |
| `TestRotaDeProjetoNaoRegistraUsoLegado` | `GET /api/v1/projects/{id}/targets` não gera registro | pôr o middleware também em `noProjeto` |
| `TestRotaLegadaSemSessaoNaoRegistra` | sem token: 401 e nenhum registro | pôr o middleware antes de `authn` |

- [ ] **Passo 2:** `go test -race -count=1 ./internal/httpapi -run 'RotaLegada|UsoLegado'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar e ligar nos dois wrappers.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(httpapi): registrar uso das rotas sem projeto`. Depois do deploy, a janela de D1 conta a partir do run verde do `Coolify release`.

### Sub-PR 7.1 — Chamadores nas rotas de projeto

#### T1: testes Go nas rotas de projeto, mais a guarda

**Arquivos:**
- Criar `internal/httpapi/rota_padrao_test.go` e `internal/httpapi/sem_rota_legada_test.go`.
- Alterar os arquivos com chamada a `"/api/v1/(targets|incidents|heartbeats|channels|status-pages)…"`. Gere a lista com `git grep -c -E '"/api/v1/(targets|incidents|heartbeats|channels|status-pages)' -- 'internal/**/*_test.go' 'cmd/**/*_test.go'`. Hoje são 37 arquivos e ~430 chamadas; os maiores são `heartbeats_integration_test.go`, `targets_integration_test.go` e `recursos_sessao_viva_integration_test.go`.

**Interfaces:**
- Produz `rotaNoPadrao(t *testing.T, userID uuid.UUID, sufixo string) string`.
  - Devolve `"/api/v1/projects/" + testdb.ProjetoPadrao(t, userID).String() + sufixo`.
  - `sufixo` começa com `/targets`, `/heartbeats` etc.
- Produz a guarda `TestNenhumChamadorUsaRotaLegada`.
  - Percorre `internal/`, `cmd/`, `web/e2e/`, `deploy/` e `README.md` com `filepath.WalkDir`, pulando diretórios por `diretorioIgnorado` (o molde de `internal/storage/migrate_order_test.go:134`: ponto, `node_modules`, `vendor`, `testdata`).
  - Procura a regex `/api/v1/(targets|incidents|heartbeats|channels|status-pages)\b` em `.go`, `.ts`, `.sh`, `.py` e `.md`.
  - Exceções nomeadas, com motivo: `routes.go` (até o 7.3), `rotas_legadas_integration_test.go`, `rotas_so_por_projeto_test.go`, `rotas_projeto_paridade_test.go` (até o 7.3).
- Os testes que provam o Projeto padrão das rotas antigas (`projeto_padrao_integration_test.go`, `targets_projeto_integration_test.go` onde compara antiga × gêmea) ficam como estão até o 7.3, e entram nas exceções com o motivo "prova da rota antiga, sai no 7.3".

- [ ] **Passo 1:** escrever `TestNenhumChamadorUsaRotaLegada` e o helper.
- [ ] **Passo 2:** `go test ./internal/httpapi -run TestNenhumChamadorUsaRotaLegada`. Esperado: FAIL, listando os arquivos. (A sabotagem é o próprio estado atual.)
- [ ] **Passo 3:** trocar cada chamada pela gêmea com `rotaNoPadrao`. Teste com dois usuários usa o `userID` de cada um. Nenhuma asserção muda: a gêmea usa os mesmos handlers.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`. Esperado: PASS, com a mesma contagem de testes de antes (`go test -json … | jq` ou `-v | grep -c '^--- PASS'` antes e depois).
- [ ] **Passo 5:** commit `test: chamadas de teste pelas rotas de projeto`.

#### T2: e2e, scripts e README

**Arquivos:**
- `web/e2e/fluxo.spec.ts`: as três chamadas pós-logout passam a `/api/v1/projects`, que também exige sessão.
- `web/e2e/jornadas.spec.ts` e `web/e2e/pagina-publica.spec.ts`: resolvem o projeto com `GET /api/v1/projects` (o `is_default`) e usam o prefixo.
- `deploy/compose/seed.sh`, `deploy/ci/memoria/carga.py` e `deploy/ci/test_carga_memoria.py` (o servidor falso passa a responder `/api/v1/projects` e `/api/v1/projects/<id>/targets`).
- `deploy/ci/ensaiar-piso.sh` (B11).
- `README.md`.

**Interfaces:** nos scripts, a função `projeto_padrao` faz `GET /api/v1/projects` e escolhe com `jq -r '.items[] | select(.is_default) | .id'`. Sem padrão, `falhar` com mensagem.

- [ ] **Passo 1:** rodar `TestNenhumChamadorUsaRotaLegada`. Esperado: FAIL só nestes arquivos (T1 já limpou os `.go`).
- [ ] **Passo 2:** migrar cada arquivo.
- [ ] **Passo 3:** a guarda passa.
  - `python3 -m unittest discover -s deploy/ci -p 'test_*.py'` passa.
  - `sh deploy/ci/ensaiar-piso.sh <tag ≥ 2b.2> <tag do branch>` passa (local, com Docker, um pesado por vez).
  - `cd web && npx playwright test e2e/fluxo.spec.ts e2e/jornadas.spec.ts e2e/pagina-publica.spec.ts` passa.
- [ ] **Passo 4:** commit `test(e2e): scripts e jornadas pelas rotas de projeto`. O título do PR **não** leva `[skip ci]`: tem script do CI.

### Sub-PR 7.2 — Contract do schema

#### T3: migration C (pré-checagem, fim do preenchimento, CHECK `NOT VALID`)

- [ ] **Passo 0 — inventário, antes de escrever código.** Anote os resultados no PR.
  - `ls internal/storage/migrations | tail -3`: define C.
  - `gh run list --workflow coolify.yml --limit 20`: 5.2 e 6.2 publicados.
  - `git grep -n "CountTitularChannels" -- internal ':!internal/storage/db'`: só a definição em `queries/channels.sql`. Se houver chamador, B8 espera.
  - `git grep -n "recurso_dono" -- internal/storage/migrations`: se o PR 6 já corrigiu a guarda (R1), a T4 só a simplifica.
  - Contagem na produção, só leitura, pelo caminho do runbook (`docs/deploy/vps.md`; nunca `ssh stillon`): `SELECT 'targets', count(*) FROM targets WHERE project_id IS NULL UNION ALL …` nas quatro tabelas. Mais que zero → D3.

**Arquivos:** criar `<C>_contract_preparar_projeto_obrigatorio.sql`; criar `internal/storage/contract_projeto_test.go`.

**Esboço da C** (nomes fixos; os corpos das funções do Down são cópia literal da 00040):
```sql
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- D3 = A: aborta com tabela e contagem, sem apagar.
DO $$ … RAISE EXCEPTION 'contract: % linha(s) de % sem project_id', n, tabela; … $$;
DROP TRIGGER targets_project_id_padrao ON targets;          -- e as outras três
DROP FUNCTION preencher_project_id_padrao();
ALTER TABLE targets ADD CONSTRAINT targets_project_id_presente
    CHECK (project_id IS NOT NULL) NOT VALID;                -- e as outras três
-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE … DROP CONSTRAINT <t>_project_id_presente;      -- ×4
CREATE FUNCTION preencher_project_id_padrao() …;            -- corpo da 00040
CREATE TRIGGER <t>_project_id_padrao BEFORE INSERT …;       -- ×4, como na 00040
```

- [ ] **Passo 1 — testes que falham** (`versaoDaMigration(t, "contract_preparar_projeto_obrigatorio")`, `bancoNaVersao`, `storage.MigrateUpTo/DownTo`, como em `projeto_padrao_migration_test.go`):

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestContractRecusaLinhaSemProjeto` | Na versão C−1, pela conexão de admin, um usuário sem Projeto padrão e um alvo dele com `project_id` NULL. `MigrateUpTo(C)` falha com mensagem que contém `targets` e `1`; a versão do banco continua C−1 e o alvo continua lá | trocar o bloco `DO` por `DELETE … WHERE project_id IS NULL` |
| `TestPreenchimentoDoProjetoSumiu` | Depois da C: nenhum gatilho `%_project_id_padrao` em `pg_trigger`, nenhuma função `preencher_project_id_padrao`; INSERT de alvo sem `project_id` pelo admin dá 23514 (`targets_project_id_presente`) | manter o gatilho de `targets` |
| `TestContractPreparoDesceESobe` | Com dados (`semearLegado` de dois usuários): C → C−1 → C mantém as contagens de recursos e o gatilho volta no meio | Down sem recriar os gatilhos |

- [ ] **Passo 2:** `go test -race -count=1 -p 1 ./internal/storage -run 'TestContractRecusa|TestPreenchimento|TestContractPreparo'`. Esperado: FAIL (a versão não existe).
- [ ] **Passo 3:** escrever a C.
- [ ] **Passo 4:** os testes passam. Rode também `-run 'ProjectID|Backfill|ProjectMigrations'` (os testes antigos de migration seguem verdes).
- [ ] **Passo 5:** commit `feat(storage): contract prepara project_id obrigatório`.

#### T4: migration C+1 (`NOT NULL`, guarda sem `user_id`, fim da cota de canais por titular)

**Arquivos:** criar `<C+1>_contract_projeto_obrigatorio.sql`; testes em `contract_projeto_test.go` e em `internal/storage/guarda_notificacao_projeto_test.go`.

**Esboço da C+1:**
```sql
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE targets VALIDATE CONSTRAINT targets_project_id_presente;   -- ×4
ALTER TABLE targets ALTER COLUMN project_id SET NOT NULL;              -- ×4 (sem varredura: CHECK validado)
ALTER TABLE targets DROP CONSTRAINT targets_project_id_presente;       -- ×4
-- B5: só projetos. recurso_projeto NULL = recurso que a RLS escondeu.
CREATE OR REPLACE FUNCTION guardar_notificacao_no_projeto() … ;
DROP POLICY notif_channels_cota_select ON notif_channels;
DROP FUNCTION cota_canais_do_titular(uuid);
COMMENT ON COLUMN targets.user_id IS 'Quem criou. NULL depois da exclusão da conta. Não concede acesso.';  -- ×4
-- +goose Down
SET LOCAL lock_timeout = '5s';
-- O inverso exato: o catálogo volta ao da C.
COMMENT ON COLUMN … IS NULL;                                            -- ×4
CREATE FUNCTION cota_canais_do_titular(projeto uuid) …;                 -- corpo da 00045 (com o REVOKE, se a 00045 tiver)
CREATE POLICY notif_channels_cota_select …;                             -- como na 00045
CREATE OR REPLACE FUNCTION guardar_notificacao_no_projeto() …;          -- corpo vigente antes da C+1 (o da 00043, ou o do PR 6 se ele mudou)
ALTER TABLE … ADD CONSTRAINT <t>_project_id_presente CHECK (project_id IS NOT NULL) NOT VALID;  -- ×4
ALTER TABLE … ALTER COLUMN project_id DROP NOT NULL;                    -- ×4
```

Corpo novo da guarda (fixado aqui porque o teste não determina a ordem dos casos):
```sql
SELECT COALESCE(t.project_id, h.project_id) INTO recurso_projeto
  FROM incidents i LEFT JOIN targets t ON t.id = i.target_id
                   LEFT JOIN heartbeats h ON h.id = i.heartbeat_id
 WHERE i.id = NEW.incident_id;
SELECT c.project_id INTO canal_projeto FROM notif_channels c WHERE c.id = NEW.channel_id;
IF recurso_projeto IS NULL OR canal_projeto IS NULL
   OR recurso_projeto IS DISTINCT FROM canal_projeto THEN
    RAISE WARNING …; RETURN NULL;   -- mesma mensagem e mesmo "descarta, não aborta" da 00043
END IF;
RETURN NEW;
```

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestContractCatalogo` (subtestes por item) | `attnotnull` de `project_id` nas quatro tabelas; nenhum CHECK `%_project_id_presente`; `status_pages_project_key` único; os quatro `%_project_id_imutavel` presentes; `cota_canais_do_titular` e `notif_channels_cota_select` ausentes; `col_description` de `user_id` não vazio nas quatro | omitir o `SET NOT NULL` de `status_pages` |
| `TestNotificacaoComCriadorApagadoPassa` | Alvo e canal no projeto de A; o alvo foi criado por B, que excluiu a conta (`testdb.ApagarUsuario`, do PR 6), então `user_id` é NULL. Um INSERT em `notifications` sob `InProberTx` entra | manter o `IF recurso_dono IS NULL` da 00043 |
| `TestNotificacaoEmCanalDeOutroProjetoEDescartada` (existe) | continua passando com o corpo novo | trocar o `IS DISTINCT FROM` por `false` |
| `TestNotificacaoDeRecursoInvisivelEDescartada` | Num escopo que não vê o alvo (`InProjectTx` de outro projeto, se a política de INSERT de `notifications` deixar; senão, `InPublicTx`), a notificação é descartada sem erro | tirar o `recurso_projeto IS NULL` da condição |
| `TestContractDesistePorLockSemEfeitoParcial` | Uma transação de admin segura `LOCK targets IN ROW EXCLUSIVE MODE` aberta; `MigrateUpTo(C+1)` falha com 55P03 em menos de 15 s; depois de soltar, `project_id` continua anulável e a versão é C. Molde: `content_rule_migration_test.go:229-280` | tirar o `SET LOCAL lock_timeout` (o teste estoura o prazo do contexto) |
| `TestContractDesceESobe` | Com dados legados semeados antes da 00039 e migrados até C+1: C+1 → C−1 → C+1 mantém contagens, hashes de token de ping, `verify_token_hash` dos canais, slugs e `project_id` de cada linha | na C+1, `UPDATE heartbeats SET ping_token_hash = sha256(ping_token_hash)` |

- [ ] **Passo 2:** `go test -race -count=1 -p 1 ./internal/storage -run 'TestContract|TestNotificacao'`. Esperado: FAIL.
- [ ] **Passo 3:** escrever a C+1. Os corpos do Down saem de `pg_get_functiondef` na versão C de um banco de teste, não de memória.
- [ ] **Passo 4:** os testes passam.
- [ ] **Passo 5:** commit `feat(storage): project_id obrigatório e guarda de avisos só por projeto`.

#### T5: o Go do contract

**Arquivos:**
- `internal/storage/queries/incidents.sql`: em `ListDeliverableChannelsForIncident`, só `JOIN notif_channels c ON c.project_id = COALESCE(t.project_id, h.project_id)`; o comentário perde o "Fallback legado".
- `internal/storage/queries/channels.sql`: saem `LockUser` e `CountTitularChannels`; o comentário do topo perde a exceção do `LockUser`.
- `internal/storage/db/*`, por `make generate`: `ProjectID` vira `uuid.UUID` em `Target`, `Heartbeat`, `NotifChannel`, `StatusPage` e nas linhas de `prober.sql.go`, `heartbeats.sql.go`, `channels.sql.go`.
- `internal/notify/service.go` (`cabeNa`: só `LockTitularChannels`; o comentário perde "a que o binário anterior tomava").
- `internal/statuspage/service.go` (B12).
- Testes que comparam `ProjectID` como ponteiro (`git grep -n "ProjectID == nil\|\*[a-zA-Z.]*ProjectID" -- '*_test.go'`: `account/projeto_padrao_test.go`, `storage/filtro_dono_paginas_test.go`, `storage/project_id_recursos_test.go`, `storage/reivindicacao_justa_test.go`).
- Apagar `TestFillTriggerSkipsProjectScope` e `TestNotificacaoLegadaSemProjetoPassaPeloCriador`.
- Ajustar `TestOlderBinaryInsertsAreRefusedInUserScope`: o código esperado passa de 42501 para "23502 ou 42501", porque o `NOT NULL` agora recusa antes da RLS. Confira qual sai e fixe esse.
- Comentários "sai no PR 7" / "até o PR 7" no código: `git grep -n "PR 7" -- internal web/src` e atualize cada um.

- [ ] **Passo 1:** `make generate && go build ./...`. Esperado: falha de compilação nos pontos acima. É a prova de que a troca de tipo chegou.
- [ ] **Passo 2:** corrigir os pontos; apagar e ajustar os testes listados.
- [ ] **Passo 3:** `go test -race -count=1 -p 1 ./internal/notify ./internal/statuspage ./internal/storage ./internal/prober ./internal/heartbeat ./internal/targets`. Precisam continuar verdes, em especial:
  - `TestConcurrentCreatesDoNotPassTheChannelLimit` (a advisory sozinha segura a corrida). Sabotagem para vê-lo falhar uma vez: tirar também `LockTitularChannels`;
  - os testes de fan-out de `notify_rls_test.go` (B7: sem teste novo; remoção de ramo morto).
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `refactor(storage): tipos sem ponteiro e fim dos fallbacks por user_id`.
- [ ] **Passo 6 — depois do merge:** run do `Coolify release` verde e `readyz` 200. Se o release falhar com 55P03 (lock), rode o workflow de novo fora de pico: a migration não deixa efeito parcial (Foco 1).

### Sub-PR 7.3 — Fim das rotas antigas e do padrão protegido

#### T6: remover as rotas antigas

**Arquivos:**
- `internal/httpapi/routes.go`: saem as 34 linhas `recursoLegado(…)` / `comEnvioLegado(…)`, as variáveis `padrao`, `recursoLegado`, `comEnvioLegado` e o comentário "As antigas, sem {projectID}…".
- `internal/httpapi/middleware/projeto.go`: saem `ProjetoPadrao` e `DefaultScope` da interface `ResolvedorDeProjeto`.
- `internal/tenancy/service.go`: sai `DefaultScope`.
- `internal/tenancy/pgstore/store.go`: saem `EnsureDefault` e `criarPadrao`.
- Os falsos que implementam `DefaultScope` nos testes de middleware.
- Apagar `projeto_padrao_integration_test.go` e os casos antiga × gêmea de `targets_projeto_integration_test.go`.
- `rotas_projeto_paridade_test.go` → `rotas_so_por_projeto_test.go`.
- `sem_rota_legada_test.go`: saem as exceções "até o 7.3".
- `README.md` e `CLAUDE.md` ("Orientação atual": rotas só por projeto; a linha das migrations com a C+1).

**Interfaces:** `ResolvedorDeProjeto` fica só com `Scope(ctx, userID, projectID uuid.UUID) (tenancy.ProjectScope, error)`.

- [ ] **Passo 1 — testes que falham** (`rotas_legadas_integration_test.go`, `rotas_so_por_projeto_test.go`):

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestLegacyResourceRoutesAreGone` | Para cada uma das 34 rotas antigas (tabela com método e caminho, com `{id}` trocado por um UUID qualquer), com sessão viva: 404 `not_found` no envelope comum (com D2 = B: 410 `route_moved`) | deixar `GET /api/v1/heartbeats` registrada |
| `TestLegacyRouteDoesNotCreateDefaultProject` | Usuário sem projeto nenhum (criado pelo admin sem padrão) chama `GET /api/v1/targets`: 404, e `projects` continua sem linha dele | manter o `ProjetoPadrao` só nessa rota |
| `TestNoResourceRouteOutsideAProject` | Em `rotasRegistradas(t)`, nenhum `METODO /api/v1/<recurso>…` com `<recurso>` em `recursosDeProjeto`, fora de `/api/v1/projects/{projectID}/` | registrar de volta uma rota antiga |

- [ ] **Passo 2:** `go test -race -count=1 ./internal/httpapi -run 'TestLegacyResourceRoutesAreGone|TestLegacyRouteDoesNotCreate|TestNoResourceRouteOutsideAProject'`. Esperado: FAIL.
- [ ] **Passo 3:** remover as rotas e o resto da lista. `go build ./...` aponta os falsos e chamadores de `DefaultScope` que faltarem.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`. Mais `TestProtectedRoutesRequireAuthentication`, `TestEveryRegisteredRouteDeniesByDefault`, `TestEveryMutatingProjectRouteRefusesViewer` e `TestNenhumChamadorUsaRotaLegada` verdes.
- [ ] **Passo 5:** commit `feat(httpapi): fim das rotas de recurso sem projeto`.

#### T7: fim do Projeto padrão protegido (D4 = A)

**Arquivos:**
- `internal/tenancy/{role,ports,service}.go` e `pgstore/` (onde o 6.2 pôs o `Delete`): sai o passo 5 da ordem do `Delete` (`is_default` → `ErrDefaultProjectProtected`) e o erro.
- `internal/apierr/errors.go` e `web/src/test/api-falsa.ts`: sai `default_project_protected`.
- O handler de `DELETE /api/v1/projects/{projectID}`: sai o caso do 409.
- Painel: o botão "Excluir projeto" do 6.5 deixa de esconder o padrão; o teste `projeto padrão não oferece excluir` vira `projeto padrão oferece excluir para owner`.

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExcluirProjetoPadraoPermitido` (substitui `TestExcluirProjetoPadraoRecusa409` do 6.2) | owner exclui o padrão com o nome certo: 204, os recursos somem, o outro projeto do titular fica | manter a checagem de `is_default` |
| `TestSemProjetoPadraoNadaORecria` | depois de excluir o padrão: `GET /api/v1/projects` lista os outros e não cria padrão; `POST /api/v1/projects` funciona e conta na cota (excluir liberou uma vaga) | chamar `CriarProjetoPadraoNaTx` no `List` |
| painel: `projeto padrão oferece excluir para owner` (Vitest) | o botão aparece no padrão para owner e não aparece para manager | manter o `!is_default` na condição |
| painel: `escolher-projeto.test.ts` › `sem lembrado e sem padrão, o primeiro da lista` (existe) | continua verde | — |

- [ ] **Passo 2:** `go test -race -count=1 ./internal/httpapi ./internal/tenancy/... -run 'ProjetoPadrao|SemProjetoPadrao'` e `cd web && npx vitest run <arquivo do 6.5>`. Esperado: FAIL.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`; `cd web && npm test && npm run typecheck && npm run lint`.
- [ ] **Passo 5:** commit `feat(projetos): projeto padrão pode ser excluído como os outros`.
- [ ] **Passo 6 — depois do merge:** run do `Coolify release` verde e `readyz` 200. No PR, a linha "Etapa 1: contract concluído".

---

## Riscos

1. **R1 — A guarda da 00043 descarta avisos de recurso sem criador.** Com o 6.2 (`user_id` anulável) e o 6.3 (exclusão de conta) no ar, `recurso_dono IS NULL` passa a significar "o criador excluiu a conta", e o aviso some com só um `WARNING` no log do Postgres. O plano do PR 6 não toca a 00043.
   - Saída A: publicar o 7.2 entre o 6.2 e o 6.3.
   - Saída B: o 6.2 troca a condição por `IF NOT FOUND` na M+1.
   - O coordenador escolhe uma das duas antes do 6.3.
2. **Lock na C+1.** O sondador segura `targets` continuamente. Se a migration desistir, o release falha e o binário anterior segue servindo (Foco 1). Rodar de novo fora de pico resolve. Nenhuma migration toca `checks`.
3. **Churn de rebase no 7.1.** São ~430 trocas mecânicas em 37 arquivos, que outras frentes (4.x, 5.x, 6.x) também editam. A guarda torna o conflito visível: quem rebasear depois vê a falha e usa `rotaNoPadrao`. Prefira fazer o merge do 7.1 numa janela sem outro PR grande de `internal/httpapi` aberto.
4. **Down de C+1 → C devolve a guarda com o defeito do R1.** O rollback só serve de emergência e é curto. O Down copia o corpo vigente antes da C+1; se o PR 6 já tiver corrigido a guarda, volta a versão corrigida.
5. **Ensaio do piso.** Com B11, o piso do SP1 PR 1 deixa de ser ensaiável. O rollback real deste PR é para o binário do PR 6, que o ensaio passa a cobrir.
6. **Planos irmãos ainda abertos.** Nomes do 6.2 (`Delete`, `ErrDefaultProjectProtected`, `TestExcluirProjetoPadraoRecusa409`), do 6.5 (botão) e do 5.2 (`CountProjectChannels`) são os do plano deles. Se o código final escolher outros, valem os do código, e T5 e T7 se ajustam. O passo 0 da T3 confere.
