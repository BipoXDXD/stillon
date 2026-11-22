# PR 2b: pertença ao projeto, avisos por projeto e criação de projetos — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** fazer do projeto o dono de fato dos recursos no banco (RLS por `app.project_id` no lugar de `user_id = app.user_id`), mandar avisos só aos canais do projeto do recurso, contar cotas pelo titular em todos os projetos dele e abrir a criação de projetos (`POST /api/v1/projects`) com seletor no painel, em quatro PRs publicáveis um a um.

**Architecture:** continua o plano da etapa (Tasks 5–15, PR #450). A pertença segue conferida uma vez, em `storage.InProjectTx`; a partir do 2b `app.project_id` **concede** acesso e `app.user_id` deixa de conceder acesso a recurso. As contagens de cota que precisam enxergar outros projetos do titular rodam em funções SQL que ligam GUCs próprios (`app.cota_*`) só dentro delas, com políticas só de SELECT. O fan-out deriva o projeto do incidente no SQL. O painel passa a `/projects/:projectId/…`, com cliente e chaves de cache por projeto.

**Tech Stack:** Go 1.26.11, PostgreSQL 17/18, pgx + sqlc, goose, testcontainers; React 19, react-router 8, TanStack Query 5, zod 4, radix-ui, Vitest + MSW, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`. Plano da etapa (restrições, roteiro e interfaces do PR 2): `docs/superpowers/plans/2026-11-22-etapa1-multiinquilino.md`. Ledger: `.superpowers/sdd/2026-11-22-etapa1-multiinquilino/progress.md`. Design do painel: `docs/superpowers/specs/2026-10-14-redesign-painel-design.md`.

**Pré-requisito:** PR 2 (#450, branch `feat/projetos-escopo`, HEAD `4b5fae39`) na `main` e publicado. Este plano foi reconciliado com esse HEAD em 2026-11-22; números de linha citados são dele.

## Global Constraints

Do plano da etapa (valem sem mudança):

- Migrations a partir da **00039**; a 00015 fica vaga; nunca número menor que o aplicado.
- Um único papel de banco com FORCE RLS; boot recusa superusuário e BYPASSRLS.
- Cada PR publica sozinho com o binário anterior ainda rodando durante o release (expand → migrate → contract).
- Cotas do plano grátis, por conta (do **titular**): projetos 3; membros por projeto 3 além do titular; alvos HTTP 20 com intervalo mínimo 300 s; heartbeats 20; canais por projeto 5. Conta `users.is_operator` sem essas cotas (tetos atuais de 100).
- E-mails de conta: teto global de **50/dia** no banco; avisos de incidente nunca bloqueados por eles.
- Respostas de cadastro, reenvio e "esqueci a senha" sempre iguais (sem enumeração).
- Operador em espaço reservado nos textos legais; nunca publicar nome nem CNPJ.
- Português do Brasil em código, commits e docs; TDD com o teste visto falhando; revisão independente no SHA final; merge fora de 00:00–01:00 e 03:10–03:45 BRT; conferir `Coolify release` e `readyz` depois de cada merge.

Do 2b:

- Migrations do 2b: **00043** (2b.1), **00044** e **00045** (2b.2), **00046** (2b.3), nesta ordem. No HEAD do PR 2 a última é a 00042. Se outra frente publicar migration antes, renumerar em sequência, sem buraco.
- `app.project_id` com valor só é ligado por `storage.InProjectTx` (via `ScopeToProject`, só em `internal/storage/scope.go`), depois de conferir a pertença. A única outra escrita nele é a cláusula `SET app.project_id = ''` das funções de cota da 00045 (só esvazia).
- `app.cota_titular_id` e `app.cota_projetos` só aparecem na 00045: ligados dentro das funções de cota, que os declaram na cláusula `SET` (o Postgres restaura na saída, mesmo com erro); as políticas que os citam são só `FOR SELECT`.
- `storage.InUserTx` em código de produção só em `internal/account/account.go`, `internal/auth/provas.go`, `internal/tenancy/pgstore/store.go` (e a definição em `internal/storage/scope.go`); `ScopeToUser` só na lista fechada que já existe em `internal/storage/guarda_escopo_test.go` (`account/account.go`, `auth/store.go`, `auth/totp.go`, `storage/scope.go`).
- Depois da 00044, nenhuma política de tabela de projeto cita `app.user_id`.
- Fan-out: canais habilitados e verificados do **projeto do recurso** do incidente. Fallback por `user_id` só quando o recurso ou o canal tem `project_id` NULL (sai no PR 7).
- Tetos de recurso do 2b são os de hoje (`targets.MaxTargetsPerUser` = 100, `heartbeat.MaxPerUser` = 100, `notify.MaxChannels` = 10, teto de regras por host), contados pelo **titular** do projeto, somando todos os projetos dele, sob trava com a **mesma chave** de hoje (`stillon.targets:<uuid>`, `stillon.heartbeats:<uuid>`, linha de `users` `FOR UPDATE`); canais ganham também a chave `stillon.channels:<titular>`. Os valores do plano grátis são do PR 5.
- Cota de projetos: 3 por titular, 100 para `users.is_operator`; trava `stillon.projects:<titular>`; contagem e INSERT do projeto e do vínculo owner na mesma transação.
- Detalhes visuais do seletor e textos novos do painel são do dono: o implementador propõe opções com as skills de design e **para** na Task 31 até o dono decidir.
- Toda tarefa de UI carrega as skills de design no passo indicado: `frontend-design:frontend-design` (direção C "Editorial claro", Fraunces + Inter, primária índigo, dentro da spec do redesign; nada de estética nova), `design-skills:ux-writing`, `design-skills:interaction-design`, `design-skills:design-systems` (reaproveitar tokens e componentes de `web/src/components/ui`), `design-skills:accessibility-audit` (WCAG 2.2 AA).
- Testes Go: `go test -race -count=1 -p 1 <pacotes>` (integração precisa do Docker, um comando pesado por vez, contêineres removidos ao fim). Painel: `cd web && npm test && npm run typecheck && npm run lint`.
- Busca no repositório com `git grep` / `git ls-files`, nunca `grep -r` a partir da raiz (`.claude/` tem worktrees de outras sessões).

## Roteiro dos PRs do 2b

| PR | Tasks (ordem) | Migration | Entrega | Binário em sobreposição no release, e por que funciona |
|---|---|---|---|---|
| 2b.1 | 16, 17, 18, 19, 20, 33, 34 | 00043 | Fan-out pelo projeto do incidente; guarda de notificação no banco; sementes e testes de RLS no escopo de projeto (verdes antes e depois da troca); guardas estáticas de escopo; pré-conferência de canais no projeto; Projeto padrão sem escrita no caminho quente; faxina estacionada do PR 2 | PR 2. Políticas não mudam. A guarda da 00043 aceita o fan-out por `user_id` do PR 2 porque todo usuário tem um projeto só (o `POST` ainda não existe): canal e recurso estão no mesmo padrão |
| 2b.2 | 22, 23 | 00044, 00045 | Troca das políticas para pertença ao projeto; cotas contadas pelo titular em todos os projetos dele | 2b.1. Todo acesso a recurso dele já é `InProjectTx` e o último `InUserTx` em recurso (`ChannelService.cabe`) saiu na Task 20. As travas têm a mesma chave dos dois lados (titular = único membro até o PR 4). Rollback até a imagem do PR 2 também funciona (§ Decisões, B) |
| 2b.3 | 24, 25, 26, 27 | 00046 | `POST /api/v1/projects` com cota do titular; segundo projeto pode ter status page própria | 2b.2. Fan-out por projeto e cotas por titular já estão no ar, então o segundo projeto não fura teto nem recebe aviso de outro. O 2b.2 responde 405 ao `POST` e (desde o PR 2) já mapeia `status_pages_project_key` para "já existe" |
| 2b.4 | 28, 29, 30, 31, 32 | — | Painel em `/projects/:projectId/…`, seletor, criar projeto, links antigos no Projeto padrão | 2b.3. O painel novo só usa rotas que o 2b.3 já tem. Bloqueado na Task 31 pela decisão de design do dono |

Ordem obrigatória: 2b.1 → 2b.2 → 2b.3 → 2b.4, cada um **publicado** (run do `Coolify release` verde do SHA e `readyz` 200) antes do merge do seguinte. A Task 21 foi absorvida pelo PR 2 e não existe mais (o número fica para manter as referências).

**Esforço na execução** (Opus em todas; nunca Haiku):

| PR | Complexas (Opus high) | Simples (Opus medium) |
|---|---|---|
| 2b.1 | 16 (fan-out entre projetos), 17 (gatilho no caminho do sondador), 19 (provas de RLS com sabotagem) | 18 (sementes, mecânica e longa), 20, 33, 34 |
| 2b.2 | 22 (troca das políticas), 23 (funções de cota e travas) | — |
| 2b.3 | 25 (transação e corrida da cota) | 24, 26, 27 |
| 2b.4 | 30 (cache e respostas em voo entre projetos), 31 (decisão de design do dono) | 28, 29, 32 |

## Review Focus

1. **`app.project_id` passou a conceder acesso e `app.user_id` deixou de conceder.** Um caminho que liga um GUC de acesso sem conferir a pertença (`set_config` cru, `ScopeToProject` fora do `InProjectTx`, função nova citando `app.cota_*`) expõe um projeto inteiro; uma política filha que sobrou com `app.user_id` deixa o membro removido alcançar o que criou. Testes: `TestOnlyScopeGoSetsProjectScope` (PR 2) e `TestOnlyScopeSQLSetsProjectGUC` (Task 20), `TestOnlyQuotaFunctionsUseQuotaGUCs` e `TestQuotaPoliciesAndFunctionsAreContained` (Task 23), `TestNoProjectTablePolicyNamesAppUserID` e `TestRemovedMemberLosesRowsTheyCreated` (Task 22).
2. **Cada projeto novo ganha teto próprio de recursos.** Contagem que roda no escopo de projeto só vê o projeto atual; com o `POST /projects` um usuário multiplica alvos, heartbeats, canais e regras por host. Testes: `TestQuotaCountsSeeEveryProjectOfTheUser` e `TestMemberQuotaCountsTitularProjects` (Task 23).
3. **Aviso de A sai no canal de B ou num projeto alheio do criador.** Fan-out por pessoa vaza entre projetos do mesmo usuário e, com membros, entre inquilinos. Testes: `TestIncidenteDeAlvoNoProjetoANaoAvisaCanalDoProjetoB` e `TestAlvoCriadoPorMembroNaoAvisaOutrosProjetosDoMembro` (Task 16).
4. **Dois binários no release.** Trava com chave diferente entre o binário anterior e o novo deixa duas criações passarem do teto; `InUserTx` em recurso devolve vazio em silêncio depois da troca. Testes: `TestTitularLockSharesKeyWithPreviousBinary` (Task 23), `TestInUserTxOnlyInPersonalContexts` (Task 20).
5. **O painel mostra dados de um projeto na tela de outro.** Chave de cache sem projeto ou resposta em voo de A chegando depois da troca para B. Testes: `chaves-do-projeto.test.ts` (Task 28) e "as listas mudam com o projeto" (Task 30).

## Decisões

### Do dono (vinculantes, 2026-11-22)

1. A permissiva `user_id = app.user_id` dos recursos **sai no 2b** (Task 22); recurso passa a ser visto por `project_id = NULLIF(current_setting('app.project_id', true), '')::uuid`.
2. Cota de projetos final já no 2b: 3 por titular, operador 100, sob trava, na mesma transação do INSERT do projeto e do owner (Task 25).
3. `user_id` dos recursos: `ON DELETE SET NULL` no PR 6, com portão (abaixo).
4. Papel no banco: não no 2b; reavaliar no PR 4.
5. PR 4: membro que sai do projeto tem só os canais de e-mail dele desabilitados (portão abaixo).
6. Detalhes visuais do seletor ficam com o dono (Task 31). As tarefas de UI usam as skills de design (acréscimo de 2026-11-22).

### Minhas (uma linha de porquê cada)

**A. Cotas fora do escopo de projeto: funções de cota com GUCs dedicados e políticas só de SELECT (Task 23).**
- **Mecanismo:** funções `cota_*_do_titular(projeto uuid)` em plpgsql com `SET app.project_id = '' SET app.cota_titular_id = '' SET app.cota_projetos = ''`. Dentro: (1) lê `projects.titular_id` do projeto pela RLS normal (só quem enxerga o projeto conta; senão 42501); (2) liga `app.cota_titular_id`, que abre `projects_cota_select`; (3) junta os ids dos projetos do titular em `app.cota_projetos`, que abre `<t>_cota_select` (`project_id = ANY(...)`) em `targets`, `heartbeats` e `notif_channels`; (4) conta. Na saída o Postgres restaura os três GUCs. Por quê: (i) enxerga todos os projetos do titular qualquer que seja o `app.project_id` de quem chama; (ii) não reabre nada por `user_id`: fora das funções os GUCs são vazios e as políticas novas valem falso, então `InUserTx` continua sem ver recurso; (iii) cabe no papel único com FORCE RLS, sem SECURITY DEFINER nem BYPASSRLS.
- **Por que dois GUCs e não um:** `app.cota_projetos` deixa a política dos recursos numa comparação de array por linha, sem subconsulta a `projects` em toda leitura (a spec proíbe JOIN por linha); `app.cota_titular_id` é o que permite ao membro (PR 4) achar os outros projetos do titular, que `projects_member_select` não mostra.
- **Recusadas:** contar por `user_id` com `SET app.project_id = ''` (depende da permissiva que a decisão 1 remove); ligar `app.scope = 'prober'` dentro da função (abriria tudo o que o sondador lê); contador desnormalizado por gatilho (deriva com cascatas e não resolve o teto por host).
- **Chave = titular, para contagem e trava.** Até o PR 4 o único membro de qualquer projeto é o titular, então `titular_id` = `user_id` em toda gravação dos dois binários e as travas caem na mesma chave; o binário anterior conta só o projeto atual, mas no release do 2b.2 ninguém tem segundo projeto (o `POST` vem no 2b.3).
- **Trava de canais com duas chaves:** `SELECT … FROM users WHERE id = titular FOR UPDATE` (a do binário anterior) e `pg_advisory_xact_lock('stillon.channels:<titular>')`. A linha de `users` do titular é invisível ao membro (PR 4): o `FOR UPDATE` devolveria zero linhas e não travaria nada, em silêncio. A chave advisory não depende de RLS; a de `users` fica só pela compatibilidade e sai no PR 7.

**B. Divisão em quatro PRs (roteiro acima).** Sementes, provas de RLS e guardas (Tasks 18–20) e o fan-out vêm antes da troca, num PR sem mudança de política, para que o binário em sobreposição da troca já não tenha `InUserTx` em recurso; a troca e as cotas vão juntas porque as funções de cota são escritas contra as políticas novas; o `POST` só depois de fan-out e cotas publicados; o painel separado porque é o único trecho que espera decisão do dono.
- **Binário do PR 2 sobre as políticas novas (rollback de duas versões):** funciona. Todas as rotas de recurso dele estão em `InProjectTx` (as antigas via `ProjetoPadrao`), `pgstore`/`account`/`auth` só tocam `projects`, `project_members` e tabelas pessoais, e o sondador não muda. Único desvio: `ChannelService.cabe` (pré-conferência em `InUserTx`) passa a contar 0, e a contagem dentro do `Create` (em `InProjectTx`) continua valendo; no pior caso sai um e-mail de verificação de um canal recusado. Sobre a 00046, o binário do PR 2 já traduz `status_pages_project_key` em `ErrExists` (72e3b6fb): a segunda página no mesmo projeto segue 409.

**C. `status_pages.user_id UNIQUE` sai no 2b.3 (00046).** O binário do PR 2 não depende dele estruturalmente (nenhum `ON CONFLICT (user_id)`, nenhuma leitura `:one` por `user_id`); `traduzir` (`internal/statuspage/service.go:22-24,279`) já mapeia `constraintUser` e `constraintProjeto` para `ErrExists`. Sem isso, o segundo projeto ficaria sem status page até o PR 7, que deixa de ter esse item.

**D. Numeração:** 00043 guarda de notificação (2b.1), 00044 troca das políticas e 00045 cotas (2b.2), 00046 status page por projeto (2b.3).

**E. Itens estacionados do PR 2 (ledger) e onde ficam:**
- **CR2 e Ruling "cota de canais com dois escopos":** Task 20 põe a pré-conferência em `InProjectTx` (mesmo escopo da definitiva; com um projeto por usuário, mesma contagem); Task 23 faz as duas contarem o titular em todos os projetos (`cota_canais_do_titular`), provado pelo subteste `canais` de `TestQuotaCountsSeeEveryProjectOfTheUser` (nenhum e-mail sai).
- **T1 (banco não garante ≥ 1 owner):** Task 25 grava projeto e owner na mesma transação (`TestCreateProjectRollsBackWithoutOwner`); a FK adiável vira portão do PR 4. Até lá todo caminho que grava projeto grava o owner junto (`account.criarProjetoPadrao`, `pgstore.EnsureDefault`, `Store.Create`), e só remoção ou rebaixamento de membro (PR 4) quebraria a invariante.
- **Ruling T2 (trava NULL → projeto deve conferir pertença):** atendido de forma mais forte na Task 22: `guard_project_id_imutavel` passa a recusar **qualquer** mudança de `project_id`, inclusive NULL → valor, porque o único caminho legítimo (o backfill) é derrubado na mesma migration. Uma conferência de pertença no gatilho ficaria sem caminho que a use.
- **CR4 (EnsureDefault adotar linhas `project_id` NULL): não.** Depois da 00044 o `InUserTx` não vê recurso e o guard recusa NULL → valor; antes dela nenhum caminho produz a linha (backfills da 00041, 00042 e o último passe da 00044). Adotar exigiria abrir uma escrita NULL → valor, que é o buraco que a T2 fecha.
- **Desempenho do `ProjetoPadrao` (T9/CR5):** Task 33, antes do painel mudar de rota.
- **Ruling "ErrNoProject → 404 como defesa no tenancy.Service":** Task 33.
- **Renomes `…TLSOfOwner` → `…TLSOfProject` e `InsertTarget` `narg` → `arg`:** Task 34. **`…FiltersOwnerWithoutRLS` → `…FiltersProjectWithoutRLS`:** Task 19 (já reescreve esses arquivos).
- **Comentários de cota (`CodeTargetLimit` "por usuário", linha longa de `MaxTargetsPerUser`, linha longa de `ListContentRuleTargetURLs`, cabeçalhos de `targets.sql`/`heartbeats.sql`/`channels.sql`, `notify/service.go`):** Task 23, que reescreve essas cotas (a query `ListContentRuleTargetURLs` sai).
- **Teste do pânico de `Routes` com `Deps.Projects` nil; balde `porEnvio` nas gêmeas `POST channels` e `verify/resend`; mutação invisível em `verify/resend` (T15):** Task 34.
- **Helper comum `EnsureDefaultProject` (Duplicated Code entre `account` e `pgstore`): fora do 2b → PR 3**, que reescreve a criação do Projeto padrão no signup.
- **`ListMyProjects` e usuário suspenso (CR6/T8): fora do 2b → PR 5** (suspensão).

**F. Outras decisões:**
- **Guarda de `status_page_items` descartada:** o `WITH CHECK` de `status_page_items_via_page` já exige o alvo visível (`00006_statuspage_rollups.sql:72-73`), e no escopo de projeto só os alvos do projeto são visíveis. Fica um teste que prende isso (`TestStatusPageItemRefusesTargetOfOtherProjectInDB`, Task 22).
- **Terceiro passe de backfill na troca (Task 22):** depois dela uma linha órfã ficaria invisível para sempre; a função é derrubada na mesma migration.
- **Código de erro do teto de projetos:** `project_limit_reached`, no molde de `target_limit_reached`, `heartbeat_limit_reached` e `channel_limit_reached` (`internal/apierr/errors.go:37-43`).
- **Lembretes:** o teste de caracterização entra na Task 16, dona do fan-out.
- **"Projeto não encontrado" no painel:** reusa a `NaoEncontrada` existente, sem texto novo.
- **PATCH (renomear) de projeto fica para o PR 4** (precisa de política de UPDATE em `projects`); **DELETE fica no PR 6**.
- **Restritivas da 00042 ficam:** impedem que uma permissiva de serviço (ping, verify, sondador, pública) some ao escopo de projeto.
- **Nome de projeto:** validado no domínio (`tenancy.ParseProjectName`, com `internal/rotulo` na lista permitida da guarda de fronteira, por ser stdlib pura); não é único por titular.

## Portões para PRs futuros

- **PR 3 (cadastro):** "sem e-mail confirmado não cria monitores nem canais" inclui `POST /api/v1/projects`; o signup cria o Projeto padrão na mesma transação do usuário, com o helper comum de `account` e `pgstore`; o contador de 50/dia envolve só os e-mails de conta, **nunca** `Resend.Send`/`post` dos avisos.
- **PR 4 (convites e papéis):** depende do 2b.2 publicado. Antes de rebaixar ou remover membro, a invariante "titular é sempre owner" no banco (FK adiável `projects (id, titular_id, titular_role) → project_members (project_id, user_id, role)`). Membro que sai: desabilitar só os canais de **e-mail** criados por ele (decisão 5). Reavaliar papel no banco (decisão 4). PATCH de projeto e política de UPDATE em `projects`. `cota_urls_com_regra_do_titular` devolve ao Go do membro URLs com regra dos outros projetos do titular (só para contar hosts, nunca para a API): conferir que continua assim.
- **PR 5 (cotas e abuso):** valores do plano grátis sobre as funções de cota da 00045; suspensão derivada em `ListDeliverableChannelsForIncident` e nas reivindicações; reivindicações trocam `user_id` por `project_id`; `porProjetoNovo` e os outros baldes em memória vão para `ip_rate_limits`; `ListMyProjects` filtra o próprio usuário suspenso.
- **PR 6 (exportar/excluir):** **a exclusão de conta não sai sem `user_id` dos recursos em `ON DELETE SET NULL`** (decisão 3), com `guard_user_id_imutavel` aceitando valor → NULL; DELETE de projeto.
- **PR 7 (contract):** `project_id NOT NULL`; remove `preencher_project_id_padrao`, o fallback por `user_id` de `ListDeliverableChannelsForIncident` e de `guardar_notificacao_no_projeto`, o `LockUser` da cota de canais (a chave advisory basta) e as rotas antigas; exige o painel do 2b.4 no ar por um release.

---

# PR 2b.1 — avisos por projeto e preparação da troca

### Task 16: fan-out pelo projeto do incidente (complexa)

**Files:**
- Modify: `internal/storage/queries/incidents.sql:43-47` (troca `ListDeliverableChannels` por `ListDeliverableChannelsForIncident`), `internal/storage/queries/prober.sql:11-12` (comentário: `user_id` volta só por compatibilidade, vira `project_id` no PR 5), `internal/prober/avisos.go:19,43,55`, `internal/prober/scheduler.go:620,625`, `internal/prober/heartbeat.go:134,150`, `internal/prober/avisar_test.go:48,69,91,112` (perdem `b.dono`)
- Modify (gerado): `internal/storage/db/incidents.sql.go`
- Modify: `internal/storage/notify_rls_test.go:88-97` (passa à query nova e espera `[canalA]`)
- Test: `internal/prober/fanout_projeto_test.go`, `internal/notify/reminders_projeto_test.go`

**Interfaces:**
- Consumes: `testdb.ProjetoPadrao`, `testdb.NovoProjeto`, `testdb.Membro`, `testdb.Admin`; os métodos de teste do sondador em `bancada` (`abrirPeloAgendador`, `fecharPeloAgendador`, `comoSondador`; `internal/prober/avisar_caracterizacao_test.go:23-81`) e em `bancadaHB` (`atrasar`, `recuperar`; `avisar_heartbeat_caracterizacao_test.go:20-59`). As notificações são lidas por um helper local `notificacoes(t) []struct{canal uuid.UUID; evento string}` (SQL via `testdb.Admin`, ordenado por `id`).
- Produces:
  ```sql
  -- Fan-out (spec §8): canais habilitados E verificados do PROJETO do recurso
  -- do incidente. Fallback legado (sai no PR 7, com o NOT NULL): recurso ou
  -- canal sem project_id casa por user_id, a regra antiga.
  -- name: ListDeliverableChannelsForIncident :many
  SELECT c.id
    FROM incidents i
    LEFT JOIN targets t    ON t.id = i.target_id
    LEFT JOIN heartbeats h ON h.id = i.heartbeat_id
    JOIN notif_channels c
      ON c.project_id = COALESCE(t.project_id, h.project_id)
      OR ((c.project_id IS NULL OR COALESCE(t.project_id, h.project_id) IS NULL)
          AND c.user_id = COALESCE(t.user_id, h.user_id))
   WHERE i.id = sqlc.arg(incident_id) AND c.enabled AND c.verified_at IS NOT NULL
   ORDER BY c.id;
  ```
  ```go
  func (q *Queries) ListDeliverableChannelsForIncident(ctx context.Context, incidentID uuid.UUID) ([]uuid.UUID, error)
  // internal/prober (não exportadas; perdem o parâmetro dono)
  func enfileirar(ctx context.Context, q *db.Queries, incidente uuid.UUID, evento string, em time.Time) error
  func avisarAbertura(ctx context.Context, q *db.Queries, incidente uuid.UUID, em time.Time) error
  func avisarFechamento(ctx context.Context, q *db.Queries, incidente uuid.UUID, em time.Time) error
  ```
  `ListDeliverableChannels` sai do sqlc (o binário anterior tem a cópia dele). As reivindicações (`ClaimDueTargets`, `ClaimHeartbeatsTo*`) não mudam.
- [ ] **Step 1: testes que falham** (`fanout_projeto_test.go`, com helpers locais `alvoNoProjeto(t, projeto, criador)` e `canalNoProjeto(t, projeto, criador)` gravados via `testdb.Admin`, canal verificado e habilitado):
  - `TestIncidenteDeAlvoNoProjetoANaoAvisaCanalDoProjetoB`: U titular de A (padrão) e B; canais cA e cB; alvo em A; `abrirPeloAgendador` → `notificacoes` = `[{cA, opened}]`; `fecharPeloAgendador` → `[{cA, opened}, {cA, resolved}]`.
  - `TestCanalCriadoPorMembroNaoTitularRecebeAvisoDoProjeto`: `testdb.Membro(t, A, M, "manager")`; canal cM com `user_id = M`, `project_id = A`; alvo de U em A; a abertura enfileira em cM.
  - `TestAlvoCriadoPorMembroNaoAvisaOutrosProjetosDoMembro`: M membro de A e titular do próprio padrão PM com canal cPM; alvo com `user_id = M` em A; a abertura não enfileira em cPM.
  - `TestHeartbeatAtrasadoDeAAvisaSoCanaisDeA` (`bancadaHB`): canais em A e B; `atrasar` → só o de A ganha `opened`; `recuperar` → só ele ganha `resolved`.
  - `TestFanoutDeRecursoSemProjetoCaiNoCriador`: usuário L sem Projeto padrão; alvo e canal com `project_id` NULL (conferido via admin); a abertura enfileira no canal de L. Variante: canal em projeto e alvo NULL também recebe. (Passa já no RED: provar com a sabotagem de tirar o `OR` legado da query nova no GREEN.)
  - `TestLembreteDeIncidenteDeANaoVaiParaCanalDeB` (`reminders_projeto_test.go`, molde de `reminder_flow_test.go`): U com canais em A e B; incidente de alvo de A com `OpenIncident` + `MarkOpenNotified` + `EnqueueNotification` só em cA; relógio além do 1º lembrete; as linhas `event='reminder'` só têm cA. Caracterização: provar com a sabotagem de trocar o `JOIN notif_channels c ON c.id = n.channel_id` de `ListReminderCandidates` (`notifications.sql:103`) por `c.user_id = COALESCE(t.user_id, h.user_id)` (o lembrete vai também a cB); desfazer.
- [ ] **Step 2:** `go test -race -count=1 ./internal/prober ./internal/notify -run 'TestIncidenteDeAlvoNoProjetoANaoAvisaCanalDoProjetoB|TestCanalCriadoPorMembroNaoTitularRecebeAvisoDoProjeto|TestAlvoCriadoPorMembroNaoAvisaOutrosProjetosDoMembro|TestHeartbeatAtrasadoDeAAvisaSoCanaisDeA|TestFanoutDeRecursoSemProjetoCaiNoCriador|TestLembreteDeIncidenteDeANaoVaiParaCanalDeB'`. Esperado: o primeiro dá `[{cA opened} {cB opened}]`; o do membro não tem linha em cM.
- [ ] **Step 3: implementar** a query, as três assinaturas e os chamadores; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/prober ./internal/storage ./internal/notify` e `sqlc diff`. `TestSchedulerMarcaEEnfileira*` e `TestHeartbeatMarcaEEnfileira*` seguem verdes.
- [ ] **Step 5:** `fix(prober): avisos de incidente nos canais do projeto do recurso`

### Task 17: guarda no banco: notificação só em canal do projeto do recurso (complexa)

**Files:**
- Create: `internal/storage/migrations/00043_guarda_notificacao_projeto.sql`
- Test: `internal/storage/guarda_notificacao_projeto_test.go`, `internal/prober/fanout_projeto_test.go` (+1 caso)

**Interfaces:**
- Consumes: `ListDeliverableChannelsForIncident` e `enfileirar` (Task 16).
- Produces: função `guardar_notificacao_no_projeto() RETURNS trigger` (plpgsql, SECURITY INVOKER, `SET search_path = public, pg_temp`) e `CREATE TRIGGER notifications_canal_do_projeto BEFORE INSERT ON notifications FOR EACH ROW`. Regra: lê `project_id` e `user_id` do recurso do incidente (alvo ou heartbeat) e do canal. Descarta (`RAISE WARNING 'notificação descartada: canal % fora do projeto do incidente %'` e `RETURN NULL`) quando: a leitura do recurso ou do canal não acha linha (fail closed); os dois `project_id` são não nulos e `IS DISTINCT FROM`; ou um deles é NULL e os `user_id` são `IS DISTINCT FROM`. Senão `RETURN NEW`. Descarta e **não aborta**: o enfileiramento roda na transação do `InsertCheck`, e abortar pararia o monitor. Todos os INSERTs em `notifications` de produção rodam em `InProberTx` (`prober/avisos.go`, `notify/reminders.go`), que enxerga as três tabelas. Down: `DROP TRIGGER`, `DROP FUNCTION`.
- [ ] **Step 1: testes que falham**
  - `TestNotificacaoEmCanalDeOutroProjetoEDescartada`: em `InProberTx`, `EnqueueNotification(incidente de A, cB)` não dá erro e `count(*) FROM notifications` (admin) = 0; o mesmo com `EnqueueReminder`.
  - `TestNotificacaoNoProjetoCertoPassa`: `(incidente de A, cA)` grava 1.
  - `TestNotificacaoLegadaSemProjetoPassaPeloCriador`: recurso e canal com `project_id` NULL e mesmo `user_id` gravam 1; com `user_id` diferente, 0.
  - `TestGuardaNaoDesfazOCheckNemOIncidente` (prober): com um `enfileirar` de teste que grava direto em cB, o check e o incidente ficam gravados (admin) e a notificação não.
- [ ] **Step 2:** `go test -race -count=1 ./internal/storage ./internal/prober -run 'TestNotificacao|TestGuardaNaoDesfazOCheckNemOIncidente'`. Esperado: `notifications = 1, queria 0`.
- [ ] **Step 3: implementar** a migration (`SET LOCAL lock_timeout = '5s'`).
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/storage ./internal/prober ./internal/notify` (`TestEveryTableForcesRLSWithAPolicy` intacto: não há tabela nova).
- [ ] **Step 5:** `feat(storage): notificação só em canal do projeto do recurso`

### Task 18: sementes de recurso no escopo de projeto (fora de `internal/storage`) (simples)

**Files:**
- Modify: `internal/storage/testdb/projeto.go` (helper novo)
- Modify: os 34 arquivos de teste fora de `internal/storage` que `git grep -l "storage.InUserTx" -- '*_test.go' ':!internal/storage/*'` lista (69 chamadas no HEAD do PR 2: `cmd/stillon`, `internal/heartbeat`, `internal/httpapi`, `internal/incident/history`, `internal/notify`, `internal/prober`, `internal/statuspage`; `internal/account/projeto_padrao_test.go` fica, é contexto pessoal)
- Test: `internal/storage/testdb/projeto_test.go`

**Interfaces:**
- Consumes: `storage.InProjectTx`, `testdb.ProjetoPadrao`.
- Produces:
  ```go
  // NoProjetoPadrao garante o Projeto padrão de userID e roda fn em
  // storage.InProjectTx nele; projeto vai para os Insert* que pedem project_id.
  func NoProjetoPadrao(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID,
      fn func(q *db.Queries, projeto uuid.UUID) error) error
  ```
- [ ] **Step 1: teste que falha:** `TestNoProjetoPadraoUsesProjectScope`: dentro de `fn`, `SELECT current_setting('app.project_id', true)` = `projeto.String()` e `projeto == testdb.ProjetoPadrao(t, u)`.
- [ ] **Step 2:** `go test -count=1 ./internal/storage/testdb -run TestNoProjetoPadraoUsesProjectScope`. Esperado: `undefined: testdb.NoProjetoPadrao`.
- [ ] **Step 3: implementar** o helper. Nos arquivos listados, cada `storage.InUserTx(ctx, pool, dono, func(q) …)` que toca tabela de `tabelasDoProjeto` (`internal/storage/rls_projeto_test.go:23`) vira `testdb.NoProjetoPadrao(t, pool, dono, func(q, projeto) …)`, com `ProjectID: &projeto` nos `Insert*` que pedem (o `narg` de `InsertTarget` vira `arg` na Task 34). Sessões e conta ficam em `InUserTx`. Os comentários de "sabotagem dupla" que citam a RLS do `InUserTx` (`internal/targets/limit_test.go:43`, `summary_test.go:320`, `incidents_integration_test.go:131`) passam a citar a do projeto. Um commit por pacote, todos com a mensagem do Step 5 e o pacote no escopo.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/prober ./internal/notify ./internal/heartbeat ./internal/httpapi ./internal/statuspage ./internal/incident/... ./internal/targets ./cmd/stillon` verde; `git grep -n 'storage.InUserTx' -- '*_test.go' ':!internal/storage/*'` só lista sessão, conta, 2FA e projetos. Verde também com a **Up provisória** aplicada: crie `internal/storage/migrations/00044_rls_por_pertenca_ao_projeto.sql` sem commit, só com os itens 3 e 4 do Step 3 da Task 22 (troca das 4 pais e das 9 filhas), rode, e apague o arquivo (`git status` limpo) antes do commit. As sementes não podem depender da permissiva por `user_id`.
- [ ] **Step 5:** `test(<pacote>): sementes de recurso no escopo de projeto em vez do escopo de usuário`

### Task 19: testes de RLS de `internal/storage` na semântica de projeto (complexa)

**Files:**
- Modify: os `internal/storage/*_test.go` que `git grep -l InUserTx -- 'internal/storage/*_test.go'` lista (no HEAD: `claim_desconhecido`, `endurecimento_rls`, `escopo_erros`, `escopo_projeto`, `escrita_por_comando`, `filtro_dono`, `heartbeat_events_clear`, `heartbeat_for_update`, `heartbeat_paginacao`, `heartbeat_rls`, `heartbeat_rollback`, `helpers`, `maintenance`, `notify_rls`, `project_id_recursos`, `rls_projeto`, `rls`, `statuspage_items_constraints`, `statuspage_rls`, `tags_rls`, `target_tls`), mais `filtro_dono_*_test.go` (renomes)

**Interfaces:** Consumes `testdb.NoProjetoPadrao` (Task 18), `testdb.NovoProjeto`, `testdb.Membro`, `emEscopoDeProjeto` (`rls_projeto_test.go:46`). Não produz API.

Regras de reescrita:
- "V não vê a linha de U em `InUserTx(V)`" vira "`InProjectTx(V, padrão de V)` não vê a linha do projeto de U"; o "filtro errado" de cada teste continua.
- `TestScopeSurvivesConnectionReuse` e `TestInUserTxRefusesNilUser` passam a provar o escopo de usuário em `sessions`.
- `TestOwnerCannotHandTargetToAnotherUser` fica (guard de `user_id`).
- Em `TestRestrictivePolicyLeavesServiceScopesUntouched` sai a asserção "`InUserTx(U)` vê A e B" (volta invertida na Task 22).
- Os testes de contagem (`CountTargets`, `CountHeartbeats`, `CountChannels`, `ListContentRuleTargetURLs`) ficam como estão: saem na Task 23.
- Renomes: todo `Test…FiltersOwnerWithoutRLS` vira `Test…FiltersProjectWithoutRLS` (o filtro testado já é `project_id` desde o PR 2); `TestDeleteTargetTLSOfOwnerFiltersOwnerWithoutRLS` vira `TestDeleteTargetTLSOfProjectFiltersProjectWithoutRLS` junto com o renome da query na Task 34 (se a 34 ainda não rodou, só o sufixo muda aqui).
- [ ] **Step 1:** reescrever pelas regras; cada teste reescrito ganha no comentário a política cuja remoção o deixa vermelho.
- [ ] **Step 2:** com a Up provisória da Task 18 aplicada (sem commit), `go test -race -count=1 ./internal/storage`. Esperado **antes** da reescrita: falhas nos arquivos listados (sementes com 42501, contagens 0). Provar cada teste reescrito vermelho pela sabotagem anotada.
- [ ] **Step 3:** ajustar até verde.
- [ ] **Step 4:** `go test -race -count=1 ./internal/storage` verde **com e sem** a Up provisória; arquivo provisório apagado.
- [ ] **Step 5:** `test(storage): provas de RLS no escopo de projeto em vez do escopo de usuário`

### Task 20: guardas de escopo e pré-conferência de canais no projeto (simples)

**Files:**
- Modify: `internal/storage/guarda_escopo_test.go` (estende o teste-guarda do PR 2, 000e880a; não cria outro arquivo de guarda de Go)
- Create: `internal/storage/queries_projeto_test.go`
- Modify: `internal/notify/service.go:101-111,146-156,249-276` (`cabe`, `Create`, comentários)

**Interfaces:**
- Consumes: `storage.InProjectTx`, `tenancy.ProjectScope`; de `guarda_escopo_test.go`: `usosDeEscopo`, `donoDoEscopoDeProjeto`, `chamadoresDeScopeToUser`, `diretoriosForaDaVarredura`.
- Produces:
  ```go
  // notify/service.go — hoje cabe(ctx, userID, travar) em InUserTx (linha 253),
  // chamada só com travar=false (linha 154).
  func (s *ChannelService) cabe(ctx context.Context, scope tenancy.ProjectScope) error // storage.InProjectTx + cabeNa(ctx, q, atual.UserID, false)
  ```
  ```go
  // guarda_escopo_test.go
  var chamadoresDeInUserTx = map[string]string{ // arquivo -> motivo
      "internal/account/account.go":       "conta e perfil da pessoa",
      "internal/auth/provas.go":           "provas de família de sessão (00029)",
      "internal/tenancy/pgstore/store.go": "projects e project_members: a lista e o Projeto padrão da pessoa",
      "internal/storage/scope.go":         "definição",
  }
  func achadosDeGUCDeProjeto(arquivo, sql string) []string // linhas com set_config('app.project_id' ou SET [LOCAL] app.project_id
  ```
  `usosDeEscopo` passa a colher também o identificador `InUserTx` (mesma varredura AST). `chamadoresDeScopeToUser` **não muda**: nem a troca das políticas (Task 22) nem a criação de projetos (Task 25, em `pgstore/store.go`, que já está em `chamadoresDeInUserTx`) chamam `ScopeToUser` em arquivo novo. Os textos da guarda que falam da troca no futuro ("quando as permissivas forem por projeto (PR 2b)") passam ao presente na Task 22.
- [ ] **Step 1: testes que falham**
  - `TestInUserTxOnlyInPersonalContexts` (em `guarda_escopo_test.go`): todo uso de `InUserTx` em `.go` de produção está em `chamadoresDeInUserTx`. Hoje falha em `internal/notify/service.go:254`.
  - `TestOnlyScopeSQLSetsProjectGUC` (em `guarda_escopo_test.go`): em `internal/storage/queries/*.sql` e `internal/storage/migrations/*.sql`, `achadosDeGUCDeProjeto` só acha o bloco `ScopeToProject` de `queries/scope.sql`. Caso positivo embutido: `achadosDeGUCDeProjeto("x.sql", "SELECT set_config('app.project_id', $1, true);")` acha 1 linha. (Passa no HEAD: provar com a sabotagem de acrescentar `SET LOCAL app.project_id = '…'` numa query de teste em memória.)
  - `TestResourceQueriesFilterByProject` (`queries_projeto_test.go`): nos blocos `-- name:` de `queries/{targets,heartbeats,channels,statuspages,incidents,notifications}.sql`, a regex `\buser_id\s*=\s*(\$\d+|sqlc\.n?arg\()` só casa nos permitidos, cada um com motivo: `CountTargets`, `ListContentRuleTargetURLs`, `CountHeartbeats`, `CountChannels` ("cota por usuário até a Task 23"). Caso positivo embutido com um bloco de exemplo. (No HEAD só esses quatro filtram por `user_id`, depois que a Task 16 tirou `ListDeliverableChannels`: provar com a sabotagem de tirar um nome da lista.)
- [ ] **Step 2:** `go test -count=1 ./internal/storage -run 'TestInUserTxOnlyInPersonalContexts|TestOnlyScopeSQLSetsProjectGUC|TestResourceQueriesFilterByProject|TestOnlyScopeGoSetsProjectScope'`. Esperado: `internal/notify/service.go:254 usa InUserTx fora dos contextos pessoais`.
- [ ] **Step 3: implementar:** `cabe(ctx, scope)` em `storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, …)`, chamando `cabeNa(ctx, q, atual.UserID, false)`; `Create` passa `scope`. Com um projeto por usuário (o `POST` vem no 2b.3), pré-conferência e conferência definitiva contam o mesmo; a Task 23 faz as duas contarem o titular. Comentários de `ChannelService` e de `cabe` dizem isso.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/notify ./internal/httpapi -run 'Channel'`.
- [ ] **Step 5:** `test(storage): guardas do escopo de projeto e pré-conferência de canais no projeto`

### Task 21: absorvida pelo PR 2 (72e3b6fb)

`traduzir` já mapeia `status_pages_project_key` (`constraintProjeto`) para `ErrExists`, provado por `TestSecondPageInSameProjectIsExists` (`internal/statuspage/projeto_test.go:156`). Nada a fazer no 2b.

### Task 33: Projeto padrão sem escrita no caminho quente e UUID nulo no domínio (simples)

**Files:**
- Modify: `internal/tenancy/pgstore/store.go:44-73` (`EnsureDefault`), `internal/tenancy/service.go:21-24` (`Scope`)
- Test: `internal/tenancy/pgstore/store_test.go`, `internal/tenancy/service_test.go`

**Interfaces:**
- Consumes: `db.GetDefaultProjectID` (já existe), `Store.Membership`.
- Produces: mesmas assinaturas. `EnsureDefault`: caminho rápido numa `InUserTx(userID)` só de leitura com `GetDefaultProjectID`; achou → `s.Membership(ctx, userID, projeto)`; `pgx.ErrNoRows` → o caminho de hoje (INSERT `ON CONFLICT` + owner + `Membership`). `Service.Scope(ctx, userID, uuid.Nil)` devolve `tenancy.ErrProjectNotFound` sem chamar o repositório (defesa do Ruling "ErrNoProject"; o middleware já barra o nulo).
- [ ] **Step 1: testes que falham**
  - `TestEnsureDefaultSkipsWritesWhenProjectExists`: U com Projeto padrão; via admin, gatilho de teste `BEFORE INSERT ON projects` e outro `ON project_members` que dão `RAISE EXCEPTION 'escrita no caminho quente'` (removidos no `t.Cleanup`); `EnsureDefault(U)` devolve o escopo do padrão sem erro. Usuário sem projeto com os gatilhos ligados dá erro (prova de que o caminho lento ainda escreve).
  - `TestEnsureDefaultStillCreatesOnFirstCall`: usuário sem projeto, sem gatilhos: cria um padrão com owner; segunda chamada devolve o mesmo id.
  - `TestScopeRefusesNilProjectWithoutRepository` (repositório falso que falha o teste se chamado): `Scope(ctx, u, uuid.Nil)` dá `errors.Is(err, tenancy.ErrProjectNotFound)`.
- [ ] **Step 2:** `go test -race -count=1 ./internal/tenancy/... -run 'TestEnsureDefault|TestScopeRefusesNilProjectWithoutRepository'`. Esperado: `escrita no caminho quente`; o falso é chamado.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/tenancy/... ./internal/httpapi -run 'Projeto|Project|Legacy'`.
- [ ] **Step 5:** `perf(tenancy): rotas antigas leem o Projeto padrão sem escrever a cada requisição`

### Task 34: faxina estacionada do PR 2 (simples)

**Files:**
- Modify: `internal/storage/queries/targets.sql:14-23,80-98` (`InsertTarget` `narg` → `arg`; `GetTargetTLSOfOwner`, `ListTargetTLSOfOwner`, `DeleteTargetTLSOfOwner` → `…TLSOfProject`), `internal/targets/service.go` e os chamadores que o compilador apontar (`internal/prober/certificado_scheduler_test.go:331`, `internal/storage/filtro_dono_contagens_test.go:94-121`, sementes com `InsertTargetParams{`)
- Modify (gerado): `internal/storage/db/targets.sql.go`
- Test: `internal/httpapi/prazo_requisicao_test.go`, `internal/httpapi/channels_integration_test.go`, `internal/httpapi/projetos_integration_test.go`

**Interfaces:**
- Consumes: `rotasSemBanco`/`configSemBanco` (`prazo_requisicao_test.go:48`), `carteiroFalso` (`channels_integration_test.go:35`), `prepararDono`, `testdb.Membro`.
- Produces: `db.InsertTargetParams.ProjectID uuid.UUID` (era `*uuid.UUID`); o comentário de `InsertTarget` perde "aceita nulo só para o caminho antigo" (depois da Task 18 nenhum chamador manda nulo; depois da 00044 o `InUserTx` não grava recurso). Queries `GetTargetTLSOfProject`, `ListTargetTLSOfProject`, `DeleteTargetTLSOfProject`, mesmo SQL.
- [ ] **Step 1: testes que falham**
  - `TestRoutesRecusaDepsSemProjects` (molde de `TestRoutesRecusaConfigSemPrazo`): `httpapi.Routes` com os `Deps` de `rotasSemBanco` e `Projects: nil` dá panic cujo texto contém `"Projects"` e `"tenancy.NewService"`. (Passa no HEAD: provar com a sabotagem de tirar o `if d.Projects == nil` de `routes.go:98`.)
  - `TestProjectChannelSendsShareTheSendBucket` (molde de `TestChannelSendsHaveTheirOwnRateLimit`): relógio parado; 3 `POST /api/v1/projects/{A}/channels` (Discord) e 2 `POST …/channels/{id}/verify/resend` passam do limitador; o sexto envio (`…/verify/resend`) dá 429 `rate_limited`; em seguida `POST /api/v1/channels` (legado) também dá 429 (o balde é o mesmo); `GET /api/v1/projects/{A}/channels` dá 200. (Provar com a sabotagem de trocar `comEnvioNoProjeto` por `noProjeto` na rota de reenvio.)
  - `TestViewerResendLeavesPendingTokenUntouched` (T15): o dono cria um canal de e-mail pendente pela rota gêmea (o `carteiroFalso` recebe 1 link); via admin, guardar `verify_token_hash, verify_expires_at`; viewer do projeto faz `POST /api/v1/projects/{A}/channels/{id}/verify/resend` → 403 `forbidden`; via admin os dois valores são iguais e o carteiro continua com 1 link.
  - O renome e o `arg` são provados pelo compilador e por `sqlc diff`.
- [ ] **Step 2:** `go test -race -count=1 ./internal/httpapi -run 'TestRoutesRecusaDepsSemProjects|TestProjectChannelSendsShareTheSendBucket|TestViewerResendLeavesPendingTokenUntouched'`, com as sabotagens anotadas. Esperado: cada um vermelho sob a sua sabotagem.
- [ ] **Step 3: implementar** os renomes e o `arg`; `make generate`.
- [ ] **Step 4:** `go build ./... && go vet ./... && sqlc diff`, o comando do Step 2, e `go test -race -count=1 -p 1 ./internal/targets ./internal/prober ./internal/storage ./cmd/stillon`.
- [ ] **Step 5:** `refactor(storage): nomes por projeto, project_id obrigatório no InsertTarget e testes das rotas de envio`

---

# PR 2b.2 — troca das políticas e cotas do titular

### Task 22: migration 00044, políticas por pertença ao projeto (complexa)

**Files:**
- Create: `internal/storage/migrations/00044_rls_por_pertenca_ao_projeto.sql`
- Test: `internal/storage/rls_pertenca_test.go`, `internal/httpapi/membro_projeto_integration_test.go`
- Modify: `internal/storage/endurecimento_rls_test.go` (`TestPoliticasDeUpdateDasTabelasComDono`), `internal/storage/project_id_recursos_test.go` (`TestProjectIDIsImmutableOnceSet`), `internal/storage/rls_projeto_test.go:65-73` (`recursosDeProjeto` ganha `incAlvo, incHeartbeat uuid.UUID`; `TestRestrictivePolicyLeavesServiceScopesUntouched`), `internal/storage/guarda_escopo_test.go` (textos no presente), os três `TODO(PR 2b)` e os gêmeos de leitura abaixo
- Modify (pendências do 2b.1): `internal/storage/project_id_recursos_test.go` (`TestOlderBinaryInsertsLandInDefaultProject`, `TestInsertWithoutDefaultProjectKeepsNullProjectID`, `TestFillTriggerSkipsProjectScope`), `internal/account/projeto_padrao_test.go` (semente crua com `app.user_id`), `internal/httpapi/projetos_integration_test.go` (`TestViewerResendLeavesPendingTokenUntouched`, só re-sabotagem), `internal/storage/rls_projeto_test.go` (isolamento de `status_pages`), `internal/storage/escopo_projeto_test.go` (`TestProjectScopeSurvivesConnectionReuse`, só re-sabotagem)

**Interfaces:**
- Consumes: `storage.InProjectTx`, `storage.InUserTx`, `storage.MigrateUpTo`/`MigrateDownTo` (`export_test.go:17,29`), `testdb.{EmptyDatabase, NovoProjeto, Membro, Suspender, NoProjetoPadrao, Admin}`, `semearProjeto(t, dono, projeto uuid.UUID, nome, slug string) recursosDeProjeto` (já existe, `rls_projeto_test.go:73`).
- Produces (catálogo):
  - pais: `DROP POLICY <t>_owner` e `CREATE POLICY <t>_do_projeto ON <t> FOR ALL USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid) WITH CHECK (<mesma expressão>)` para `targets`, `heartbeats`, `notif_channels`, `status_pages`;
  - filhas que citam o dono: `ALTER POLICY … RENAME` e `ALTER POLICY … USING/WITH CHECK`, trocando `X.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid` por `X.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid` e mantendo `OR … 'prober'` e as condições de linha:

    | Hoje (origem) | Depois |
    |---|---|
    | `notifications_via_channel` (00005:131) | mesmo nome |
    | `channel_health_owner_or_prober` (00006:124) | `channel_health_projeto_ou_sondador` |
    | `heartbeat_tags_owner` (00021:17) | `heartbeat_tags_do_projeto` |
    | `target_tls_owner_or_prober_select` / `_delete` (00025:31,35) | `target_tls_projeto_ou_sondador_select` / `_delete` |
    | `incidents_select_via_heartbeat` (00034:40) | mesmo nome |
    | `incidents_owner_update` (00034:55) | `incidents_projeto_update` |
    | `target_tags_owner` (00034:66) | `target_tags_do_projeto` |
    | `target_health_owner_reset` (00035:62) | `target_health_projeto_reset` |
  - `guard_project_id_imutavel()` estrito: `IF NEW.project_id IS DISTINCT FROM OLD.project_id THEN RAISE … USING ERRCODE = '42501'` (atende o Ruling T2: depois da troca o único NULL → valor era o backfill);
  - `backfill_projetos_padrao()` derrubada.
- [ ] **Step 0: conferir o catálogo.** No banco de teste, a consulta de `TestNoProjectTablePolicyNamesAppUserID` (abaixo) lista exatamente as 13 políticas (4 pais `*_owner` + as 9 filhas da tabela). Política a mais entra na tabela com o mesmo molde antes do Step 1.
- [ ] **Step 1: testes que falham**
  - `TestMemberSeesAndEditsRowsCreatedByTitular`: `semearProjeto(t, U, A, "a", "a")`; `testdb.Membro(t, A, M, "member")`. Em `InProjectTx(M, A)`: para cada tabela de `tabelasDoProjeto`, `count(*)` igual ao de `InProjectTx(U, A)` e > 0; `UPDATE targets SET name='m'` 1 linha; `UPDATE incidents SET ack_at=now()` 1 linha para `incAlvo` e para `incHeartbeat`; zerar `target_health` 1 linha; `DELETE FROM target_tags` 1 linha; `INSERT INTO heartbeat_tags` passa; `DELETE FROM target_tls` 1 linha.
  - `TestUserScopeSeesNoProjectRows`: U com linhas em A e B; em `InUserTx(U)`, `count(*) = 0` em toda tabela de `tabelasDoProjeto`; `INSERT INTO targets` com `project_id` NULL dá 42501 e com `project_id = A` também.
  - `TestRemovedMemberLosesRowsTheyCreated`: M (member) cria alvo por `InProjectTx(M, A)`; admin apaga o vínculo; `InProjectTx(M, A)` dá `tenancy.ErrProjectNotFound`; `InUserTx(M)` vê 0 alvos; `InProjectTx(U, A)` vê o alvo.
  - `TestSuspendedTitularHidesProjectFromMemberEvenInUserScope`: `testdb.Suspender(t, U)` → `InProjectTx(M, A)` dá `ErrProjectNotFound` e `InUserTx(M)` vê 0 alvos.
  - `TestNoProjectTablePolicyNamesAppUserID`: para as tabelas de `tabelasDoProjeto` e as partições de `checks`, nenhuma `pg_get_expr(polqual|polwithcheck, polrelid)` contém `app.user_id`. Sabotagem: deixar uma filha sem trocar deixa vermelho.
  - `TestStatusPageItemRefusesTargetOfOtherProjectInDB`: página em A, alvo de B do mesmo U; INSERT em `status_page_items` em `InProjectTx(U, A)` com o alvo de B dá 42501; com o alvo de A passa.
  - `TestPolicySwapBackfillsRowsWrittenBefore`: banco vazio até a 00043; usuário X sem projeto com alvo gravado em `InUserTx` pelo SQL antigo (`project_id` NULL); `MigrateUpTo(00044)`; via admin o `project_id` é o padrão de X e `InProjectTx(X, padrão)` vê o alvo.
  - `TestProjectIDIsImmutableOnceSet`, caso novo: linha com `project_id` NULL inserida pelo admin; em `InProberTx`, `UPDATE targets SET project_id = $A` dá 42501 (hoje passa).
  - `TestPolicySwapDownRestoresCatalogAndKeepsRows`: `EmptyDatabase`, `MigrateUpTo(00043)`; snapshot S0 = `relname, polname, polcmd, polpermissive, pg_get_expr(polqual), pg_get_expr(polwithcheck)` ordenado + `pg_get_functiondef('guard_project_id_imutavel'::regproc)` + existência de `backfill_projetos_padrao`; `MigrateUpTo(00044)` e semeadura por `InProjectTx`; `MigrateDownTo(00043)`; snapshot igual a S0, contagens via admin iguais e `InUserTx(U)` volta a ver os alvos.
  - `TestPoliticasDeUpdateDasTabelasComDono`: as esperadas trocam `*_owner:*` por `heartbeats.heartbeats_do_projeto:*`, `notif_channels.notif_channels_do_projeto:*`, `targets.targets_do_projeto:*`.
  - `TestRestrictivePolicyLeavesServiceScopesUntouched`: volta a asserção invertida, `InUserTx(U)` vê 0 alvos.
  - Pendências do 2b.1 (Parked destinados a esta task; hoje só no ledger fora do git):
    - Ruling A2, `internal/storage/project_id_recursos_test.go`: `TestOlderBinaryInsertsLandInDefaultProject` e `TestInsertWithoutDefaultProjectKeepsNullProjectID` passam a afirmar 42501 na inserção pelo caminho antigo (ou saem); `TestFillTriggerSkipsProjectScope` aceita 42501 sem exigir o nome da política. `TestPoliticasDeUpdateDasTabelasComDono` já está na lista acima.
    - `internal/account/projeto_padrao_test.go`: a semente crua com `app.user_id` (INSERT em `targets` sem `project_id`, no teste do projeto padrão) sai ou muda junto com o gatilho do caminho antigo.
    - `TestViewerResendLeavesPendingTokenUntouched` (`internal/httpapi/projetos_integration_test.go`): depois da troca, re-sabotar (tirar o `Require` de `internal/notify/service.go`) e ver caírem as asserções de hash/expiração e do carteiro, não só o status.
    - Isolamento de `status_pages` entre projetos: teste que cai com `status_pages_do_projeto` sabotada (hoje só `TestProjectScopeSeesNothingOfOtherProjectOfSameUser`, sem prova por sabotagem).
    - `TestProjectScopeSurvivesConnectionReuse` (`internal/storage/escopo_projeto_test.go`): confirmar que, depois da 00044, ele prova vazamento de `app.project_id` (sabotagem: não limpar o GUC).
  - Os três `TODO(PR 2b)` (apagar o comentário `TODO` e o texto "a permissiva ainda é por dono"):
    - `internal/targets/projeto_test.go:95`, `TestViewerReadsButCannotChangeTargets`: `len(vistos) == 1 && vistos[0].ID == alvo.ID`.
    - `internal/statuspage/projeto_test.go:134`, `TestMemberCannotEditStatusPage`: `got, err := a.svc.Get(ctx, comoMember)` com `err == nil && got.ID == p.ID`.
    - `internal/httpapi/projetos_integration_test.go:298`, `TestViewerGetsForbiddenOnEveryMutatingProjectRoute`: toda leitura (inclusive por `{id}`) dá `http.StatusOK` e `corpo == antes[r.caminho(t, rota, r.projeto)]` (o viewer lê o mesmo que o dono); o comentário do teste deixa de citar o portão.
  - Gêmeos de leitura (asserções novas em testes que já existem): `internal/heartbeat/projeto_test.go` `TestViewerCannotRotateOrPingHeartbeat`: o viewer faz `Get` do heartbeat do titular sem erro; `internal/incident/history/projeto_test.go` `TestViewerCannotAckIncident`: o `List` do viewer contém o incidente; `internal/notify/projeto_test.go` `TestMemberCannotManageChannels`: o `List` do member tem `len == 1` com o canal do titular. Novo `TestMemberUpdatesTitularTarget` (`internal/targets/projeto_test.go`): member faz `Update` do alvo do titular e o nome lido via admin muda.
  - `TestMemberReadsTitularResourcesOnProjectRoutes` (HTTP, `membro_projeto_integration_test.go`): U cria alvo, heartbeat, canal e página pelas rotas antigas; M vira member do padrão de U; com o token de M, `GET /api/v1/projects/{A}/targets`, `…/heartbeats`, `…/channels`, `…/status-pages` devolvem os mesmos `items` que U vê; `PATCH …/targets/{id}` dá 200; `POST …/incidents/{id}/ack` dá 200.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage ./internal/targets ./internal/heartbeat ./internal/notify ./internal/statuspage ./internal/incident/... ./internal/httpapi -run 'TestMemberSeesAndEditsRowsCreatedByTitular|TestUserScopeSeesNoProjectRows|TestRemovedMemberLosesRowsTheyCreated|TestSuspendedTitularHidesProjectFromMemberEvenInUserScope|TestNoProjectTablePolicyNamesAppUserID|TestStatusPageItemRefusesTargetOfOtherProjectInDB|TestPolicySwapBackfillsRowsWrittenBefore|TestProjectIDIsImmutableOnceSet|TestPolicySwapDownRestoresCatalogAndKeepsRows|TestPoliticasDeUpdateDasTabelasComDono|TestRestrictivePolicyLeavesServiceScopesUntouched|TestViewerReadsButCannotChangeTargets|TestMemberCannotEditStatusPage|TestViewerGetsForbiddenOnEveryMutatingProjectRoute|TestViewerCannotRotateOrPingHeartbeat|TestViewerCannotAckIncident|TestMemberCannotManageChannels|TestMemberUpdatesTitularTarget|TestMemberReadsTitularResourcesOnProjectRoutes'`. Esperado: no escopo de M `targets` conta 0, queria 1; `InUserTx(U)` vê 2 alvos, queria 0; o catálogo cita `app.user_id` em 13 políticas.
- [ ] **Step 3: implementar a 00044.** Comentário no topo: "`app.project_id` passa a conceder acesso: só `storage.InProjectTx` o liga, depois de conferir a pertença". Up, numa transação goose:
  1. `SET LOCAL lock_timeout = '5s'`;
  2. `SELECT backfill_projetos_padrao();` (último passe, ainda com as `*_owner`);
  3. os 4 pares `DROP POLICY <t>_owner` + `CREATE POLICY <t>_do_projeto`;
  4. os `RENAME` e `ALTER POLICY` da tabela acima;
  5. `CREATE OR REPLACE FUNCTION guard_project_id_imutavel()` estrito, `SET search_path = pg_catalog, pg_temp`;
  6. `DROP FUNCTION backfill_projetos_padrao();`.

  Down: o inverso exato, sem DML, com as expressões de S0 (as vigentes no HEAD, não necessariamente as da migration de origem, se alguma posterior as alterou), o corpo original do guard (00040:37-46) e `backfill_projetos_padrao()` recriada com o `REVOKE` (00041:27-53).
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test` e `sqlc diff`. Na descrição do PR: a saída de `git grep -n "storage.InUserTx" <SHA do 2b.1 na main> -- 'internal/**/*.go' ':!*_test.go'`, provando que o binário em sobreposição só usa `InUserTx` em `account`, `auth/provas.go` e `pgstore`.
- [ ] **Step 5:** `feat(storage): políticas de RLS por pertença ao projeto no lugar das de dono`

### Task 23: cotas de recurso contadas pelo titular em todos os projetos dele (complexa)

**Files:**
- Create: `internal/storage/migrations/00045_contagem_de_cota_do_titular.sql`
- Modify: `internal/storage/queries/targets.sql:1-7,226-239`, `heartbeats.sql:1-3,141-152`, `channels.sql:1-13,28`, `projects.sql`; `internal/targets/service.go:118-130,278-282,460-470` (`recusarAlvoAcimaDosTetos`, `lerParaEditar`, `contarRegrasNoHost`), `internal/targets/target.go:129-134`, `internal/heartbeat/service.go:24-27,124,232-250` (`inserir`, usado por `Create` e `Clone`), `internal/notify/service.go:25-28,101-111,249-276` (`cabe`, `cabeNa`), `internal/apierr/errors.go` (comentário de `CodeTargetLimit` "por titular")
- Modify (testes que usam as queries que saem; `git grep -n 'CountTargets\|CountHeartbeats\|CountChannels\|ListContentRuleTargetURLs\|LockUserTargets\|LockUserHeartbeats' -- '*_test.go'`): `internal/heartbeat/{create_clone_list,service,service_erros_banco}_test.go`, `internal/httpapi/{heartbeat_acoes,targets}_integration_test.go`, `internal/storage/{escopo_projeto,rls_projeto,targets_owner_filter,filtro_dono_contagens}_test.go`, `internal/targets/limit_test.go` (comentário) — contagem por SQL cru via admin; os três `Test…Count…FiltersProjectWithoutRLS` e a parte de `ListContentRuleTargetURLs` de `targets_owner_filter_test.go` saem (as funções de cota têm os próprios testes)
- Modify: `internal/storage/guarda_escopo_test.go` (`TestOnlyScopeSQLSetsProjectGUC` aceita `SET app.project_id = ''` só na 00045), `internal/storage/queries_projeto_test.go` (lista permitida fica vazia)
- Modify (gerado): `internal/storage/db/*.sql.go`
- Test: `internal/storage/cota_titular_test.go`, `internal/targets/cota_titular_test.go`, `internal/httpapi/cotas_projetos_integration_test.go`

**Interfaces:**
- Consumes: `storage.InProjectTx`, `emEscopoDeProjeto`, `testdb.NovoProjeto`, `testdb.Membro`, `testdb.NoProjetoPadrao` (Task 18).
- Produces (00045):
  ```sql
  -- Cota do titular (spec §Cotas): enxergar os outros projetos do titular sem
  -- reabrir nada por user_id. Os GUCs app.cota_* só existem aqui, ligados
  -- dentro das funções abaixo; a cláusula SET da função os restaura na saída,
  -- mesmo com erro. Políticas só de SELECT.
  CREATE POLICY projects_cota_select ON projects FOR SELECT
      USING (titular_id = NULLIF(current_setting('app.cota_titular_id', true), '')::uuid);
  CREATE POLICY targets_cota_select ON targets FOR SELECT
      USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));
  -- heartbeats_cota_select e notif_channels_cota_select: a mesma expressão.
  ```
  Quatro funções plpgsql, todas com `SET search_path = public, pg_temp SET app.project_id = '' SET app.cota_titular_id = '' SET app.cota_projetos = ''`, `REVOKE EXECUTE … FROM PUBLIC` e `GRANT EXECUTE` ao papel da aplicação, e o mesmo preâmbulo:
  1. `SELECT p.titular_id INTO titular FROM projects p WHERE p.id = projeto` (RLS normal, por `app.user_id`); `NULL` → `RAISE EXCEPTION 'projeto fora do escopo da contagem de cota' USING ERRCODE = '42501'`;
  2. `PERFORM set_config('app.cota_titular_id', titular::text, true)`;
  3. `SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular`;
  4. `PERFORM set_config('app.cota_projetos', projetos::text, true)`.

  | Função | Retorno | Corpo depois do preâmbulo |
  |---|---|---|
  | `cota_alvos_do_titular(projeto uuid)` | `bigint` | `count(*) FROM targets WHERE project_id = ANY(projetos)` |
  | `cota_urls_com_regra_do_titular(projeto uuid)` | `TABLE (id uuid, url text)` | `id, url FROM targets WHERE project_id = ANY(projetos) AND content_mode IS NOT NULL` |
  | `cota_heartbeats_do_titular(projeto uuid)` | `bigint` | `count(*) FROM heartbeats WHERE …` |
  | `cota_canais_do_titular(projeto uuid)` | `bigint` | `count(*) FROM notif_channels WHERE …` |

  Down: `DROP FUNCTION` ×4, `DROP POLICY` ×4.

  Queries (`LockUserTargets`, `CountTargets`, `ListContentRuleTargetURLs`, `LockUserHeartbeats`, `CountHeartbeats`, `CountChannels` saem do sqlc; `LockUser` fica; o binário anterior tem as cópias dele):
  ```sql
  -- name: ProjectTitular :one
  SELECT titular_id FROM projects WHERE id = sqlc.arg(project_id);
  -- Mesma chave textual de LockUserTargets: o binário anterior (titular = único membro) trava igual.
  -- name: LockTitularTargets :exec
  SELECT pg_advisory_xact_lock(hashtextextended('stillon.targets:' || sqlc.arg(titular_id)::uuid::text, 0));
  -- name: CountTitularTargets :one
  SELECT cota_alvos_do_titular(sqlc.arg(project_id)::uuid)::bigint;
  -- name: ListTitularContentRuleTargetURLs :many
  SELECT r.id, r.url FROM cota_urls_com_regra_do_titular(sqlc.arg(project_id)::uuid) AS r;
  -- name: LockTitularHeartbeats :exec   ('stillon.heartbeats:' || titular_id, como hoje)
  -- name: CountTitularHeartbeats :one   (cota_heartbeats_do_titular)
  -- Chave advisory: não depende da RLS de users (invisível ao membro no PR 4).
  -- name: LockTitularChannels :exec
  SELECT pg_advisory_xact_lock(hashtextextended('stillon.channels:' || sqlc.arg(titular_id)::uuid::text, 0));
  -- name: CountTitularChannels :one     (cota_canais_do_titular)
  ```
  Se o sqlc não inferir os tipos de `cota_urls_com_regra_do_titular`, casts explícitos (`r.id::uuid`, `r.url::text`). Nos serviços, a ordem é sempre: `ProjectTitular(scope.ProjectID)` → `LockTitular…(titular)` → `CountTitular…(scope.ProjectID)` → comparar com o teto de hoje. Canais: `cabeNa(ctx, q, sc tenancy.ProjectScope, travar bool)`; com `travar`, `LockUser(titular)` (compatibilidade com o binário anterior) **e** `LockTitularChannels(titular)`; a pré-conferência (`cabe`, `travar = false`) também conta `CountTitularChannels`, encerrando o Ruling dos dois escopos. `contarRegrasNoHost(ctx, q, projectID uuid.UUID, host string, exceto uuid.UUID)`. Os nomes dos tetos (`MaxTargetsPerUser`, `MaxPerUser`, `MaxChannels`) ficam até o PR 5, com o comentário "por titular, somando os projetos dele" em linhas de até 100 colunas.
- [ ] **Step 1: testes que falham**
  - `TestQuotaCountsSeeEveryProjectOfTheUser` (`httpapi`, subtestes `alvos`, `heartbeats`, `canais`, `regras_por_host`): U titular de A (padrão) e B (`testdb.NovoProjeto`); A cheio via admin (`targets.MaxTargetsPerUser` alvos; `heartbeat.MaxPerUser` heartbeats; `notify.MaxChannels` canais; o teto de regras de um host); `POST /api/v1/projects/{B}/targets` (`…/heartbeats`, `…/channels`, alvo com regra no mesmo host) dá 409 com `target_limit_reached` (`heartbeat_limit_reached`, `channel_limit_reached`, `content_rule_host_limit_reached`); via admin, B continua com 0; no de canais (e-mail), o `carteiroFalso` não recebe link (a pré-conferência já conta A). Sabotagem: voltar `CountTitularTargets` a `SELECT count(*) FROM targets WHERE user_id = $1` deixa o subteste `alvos` vermelho.
  - `TestMemberQuotaCountsTitularProjects` (`targets`): U com `MaxTargetsPerUser` alvos em B (via admin); M member de A (titular U), sem vínculo em B; `Create` no escopo de M em A dá `ErrLimitReached`.
  - `TestQuotaFunctionsCountEveryProjectOfTitular` (`storage`): alvos, heartbeats e canais em A e B; em `InProjectTx(U, A)`, as três funções de contagem dão A+B e `cota_urls_com_regra_do_titular(A)` traz as linhas com regra dos dois; na mesma transação, em seguida, `SELECT count(*) FROM targets` dá só A, `current_setting('app.project_id', true)` = A e os dois `app.cota_*` = `''`.
  - `TestQuotaFunctionDoesNotCountOtherTitulars`: V com alvos no padrão dele; `cota_alvos_do_titular(A)` não os soma.
  - `TestQuotaFunctionRefusesProjectOutsideScope`: em `InProjectTx(V, padrão de V)`, `cota_alvos_do_titular(A)` dá 42501; em `InProberTx`, também.
  - `TestQuotaFunctionRestoresScopeAfterError`: em `InProjectTx(U, A)`, `SAVEPOINT s`; `cota_alvos_do_titular(<projeto de V>)` dá 42501; `ROLLBACK TO s`; `current_setting('app.project_id', true)` = A e os `app.cota_*` = `''`.
  - `TestInUserTxSeesNoResourcesEvenAfterQuotaCount`: em `InUserTx(U)`, `cota_alvos_do_titular(A)` devolve A+B; em seguida, na mesma transação, `count(*)` em `targets`, `heartbeats`, `notif_channels` = 0 e os `app.cota_*` = `''`.
  - `TestTitularLockSharesKeyWithPreviousBinary` (`targets`, subtestes `alvo`, `heartbeat`, `canal_linha_de_users`, `canal_chave_nova`): uma transação admin segura o SQL literal do binário anterior (`SELECT pg_advisory_xact_lock(hashtextextended('stillon.targets:' || $U::text, 0))`; heartbeat com `'stillon.heartbeats:'`; `SELECT id FROM users WHERE id = $U FOR UPDATE`; e, no último, `'stillon.channels:'`); `Create` no escopo de U com `context.WithTimeout(500 ms)` não termina antes do prazo; depois do commit da admin, `Create` passa.
  - `TestOnlyQuotaFunctionsUseQuotaGUCs` (estático, em `guarda_escopo_test.go`): `app.cota_titular_id` e `app.cota_projetos` só em `migrations/00045_contagem_de_cota_do_titular.sql`; nenhum `.go` nem `queries/*.sql` os cita.
  - `TestQuotaPoliciesAndFunctionsAreContained` (catálogo): as políticas cujo `polqual`/`polwithcheck` cita `app.cota_` são exatamente `projects_cota_select`, `targets_cota_select`, `heartbeats_cota_select`, `notif_channels_cota_select`, todas `polcmd = 'r'`; as funções cujo `prosrc` cita `app.cota_` são exatamente as quatro, e o `proconfig` de cada uma contém `app.project_id=`, `app.cota_titular_id=` e `app.cota_projetos=`.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage ./internal/targets ./internal/httpapi -run 'TestQuotaCountsSeeEveryProjectOfTheUser|TestMemberQuotaCountsTitularProjects|TestQuotaFunction|TestInUserTxSeesNoResourcesEvenAfterQuotaCount|TestTitularLockSharesKeyWithPreviousBinary|TestOnlyQuotaFunctionsUseQuotaGUCs|TestQuotaPoliciesAndFunctionsAreContained|TestOnlyScopeSQLSetsProjectGUC'`. Esperado: o `POST` em B dá 201 (queria 409); `function cota_alvos_do_titular(uuid) does not exist`.
- [ ] **Step 3: implementar** a migration (`SET LOCAL lock_timeout = '5s'`), as queries, os três serviços e os comentários listados; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test` e `sqlc diff`. `TestResourceQueriesFilterByProject` passa com a lista permitida vazia; `git grep -n "PR 2b" -- '*.go' '*.sql' ':!internal/storage/db/*'` não lista mais comentário de cota.
- [ ] **Step 5:** `fix(storage): cotas de recurso contadas pelo titular em todos os projetos dele`

---

# PR 2b.3 — criar projetos

### Task 24: nome e teto de projeto no domínio (simples)

**Files:**
- Create: `internal/tenancy/project.go`
- Modify: `internal/tenancy/fronteiras_test.go:16-18,51` (permitir `internal/rotulo`)
- Test: `internal/tenancy/project_test.go`

**Interfaces (Produces):**
```go
package tenancy

const MaxProjectNameRunes = 100 // = CHECK de projects.name (00039:13)
const (
    MaxProjectsPerTitular  = 3
    MaxProjectsForOperator = 100
)
var (
    ErrProjectNameRequired = errors.New("nome do projeto obrigatório")
    ErrProjectNameTooLong  = errors.New("nome do projeto longo demais")
    ErrProjectNameInvalid  = errors.New("nome do projeto com caractere invisível ou de controle")
    ErrProjectLimit        = errors.New("limite de projetos atingido")
)
func ParseProjectName(s string) (string, error) // trim; devolve o nome normalizado
func ProjectLimit(isOperator bool) int
```
- [ ] **Step 1: testes que falham**
  - `TestParseProjectName` (tabela): `"  Produção  "` → `"Produção"`, nil; `""` e `"   "` → `ErrProjectNameRequired`; `strings.Repeat("ç", 100)` passa, 101 → `ErrProjectNameTooLong`; `"a​b"`, `"a\x00b"`, `"a‮b"` → `ErrProjectNameInvalid`.
  - `TestProjectLimit`: `false` → 3; `true` → 100.
  - `TestTenancyImportsNoInfrastructure` ajustado: aceita `internal/rotulo`, continua recusando `internal/storage` (caso positivo embutido).
- [ ] **Step 2:** `go test -count=1 ./internal/tenancy -run 'TestParseProjectName|TestProjectLimit|TestTenancyImportsNoInfrastructure'`. Esperado: `undefined: ParseProjectName`.
- [ ] **Step 3: implementar:** `ParseProjectName` usa `rotulo.TemInvisivel` e `utf8.RuneCountInString`, na ordem dos motivos de `targets.motivoDoNome` (`internal/targets/validate.go:142-152`).
- [ ] **Step 4:** o comando do Step 2 passa.
- [ ] **Step 5:** `feat(tenancy): nome e teto de projeto no domínio`

### Task 25: criar projeto com owner e cota do titular na mesma transação (complexa)

**Files:**
- Modify: `internal/storage/queries/projects.sql`, `internal/storage/queries/users.sql`, `internal/tenancy/ports.go`, `internal/tenancy/service.go`, `internal/tenancy/pgstore/store.go`, os repositórios falsos `repoFalso` de `internal/tenancy/service_test.go:15` e `internal/httpapi/prazo_requisicao_test.go:30` (ganham `Create`)
- Modify (gerado): `internal/storage/db/projects.sql.go`, `internal/storage/db/users.sql.go`
- Test: `internal/tenancy/pgstore/create_test.go`, `internal/tenancy/service_test.go`

**Interfaces:**
- Consumes: `storage.InUserTx`, `tenancy.ParseProjectName`, `tenancy.ProjectLimit`, `tenancy.ErrProjectLimit` (Task 24); `tenancy.ProjectSummary{ID, Name, IsDefault, Role, CreatedAt}` (`ports.go:11`).
- Produces:
  ```go
  // tenancy (porta)
  type NewProject struct {
      ID        uuid.UUID
      Name      string // já normalizado por ParseProjectName
      TitularID uuid.UUID
      Now       time.Time
  }
  // ProjectRepository ganha:
  // Create grava o projeto (is_default = false) e o vínculo owner do titular
  // na MESMA transação, sob a trava do titular; ErrProjectLimit quando
  // ProjectLimit(is_operator) já foi atingido.
  Create(ctx context.Context, p NewProject) (ProjectSummary, error)

  func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (ProjectSummary, error)
  // ParseProjectName -> uuid.NewV7 -> repo.Create com s.clk.Now()
  ```
  ```sql
  -- name: LockTitularProjects :exec
  SELECT pg_advisory_xact_lock(hashtextextended('stillon.projects:' || sqlc.arg(titular_id)::uuid::text, 0));
  -- name: CountTitularProjects :one
  SELECT count(*) FROM projects WHERE titular_id = sqlc.arg(titular_id);
  -- Sem ON CONFLICT: projeto novo que colide falha alto (ao contrário do padrão).
  -- name: InsertProject :one
  INSERT INTO projects (id, name, titular_id, is_default, created_at)
  VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(titular_id), false, sqlc.arg(now))
  RETURNING id, name, is_default, created_at;
  -- name: InsertProjectOwner :exec
  INSERT INTO project_members (project_id, user_id, role, created_at)
  VALUES (sqlc.arg(project_id), sqlc.arg(user_id), 'owner', sqlc.arg(now));
  -- users.sql
  -- name: UserIsOperator :one
  SELECT is_operator FROM users WHERE id = sqlc.arg(id);
  ```
  `CountTitularProjects` roda em `InUserTx(titular)`: `projects_member_select` (00039:50) mostra ao titular todos os projetos dele por `titular_id`, sem as funções de cota; `projects_titular_insert` e `project_members_titular_owner_insert` aceitam os dois INSERTs.
- [ ] **Step 1: testes que falham**
  - `TestCreateProjectRecordsOwnerInSameTransaction`: via admin, `projects` tem `titular_id = U`, `is_default = false`, e `project_members` tem `(P, U, 'owner')`; `ListForUser(U)` traz o padrão e depois P com `Role = RoleOwner`.
  - `TestCreateProjectRollsBackWithoutOwner`: gatilho de teste (admin) que dá `RAISE` no INSERT de `project_members` para P; `Create` devolve erro e via admin `count(*) FROM projects WHERE id = P` = 0. Sabotagem: comitar o projeto antes do vínculo deixa vermelho.
  - `TestCreateProjectInvisibleToOtherUser`: V não vê P em `ListForUser(V)` e `Membership(V, P)` dá `ErrProjectNotFound`.
  - `TestCreateProjectRespectsTitularLimit`: U comum com o padrão e mais 2; o 4º dá `errors.Is(err, tenancy.ErrProjectLimit)` e o `count` fica 3.
  - `TestCreateProjectLimitUnderRace`: U com 2 projetos; 5 goroutines `Create`; passa exatamente 1, as outras dão `ErrProjectLimit`, `count` = 3. Sabotagem: sem `LockTitularProjects`, vermelho com `-count=20`.
  - `TestOperatorProjectLimitIsHundred`: `is_operator = true` (admin) com 3 projetos; o 4º passa.
  - `TestServiceCreateValidatesNameBeforeRepository` (repositório falso): `Create(U, "  ")` dá `ErrProjectNameRequired` sem chamar o falso; `Create(U, " X ")` repassa `Name == "X"` e `Now == clk.Now()`.
- [ ] **Step 2:** `go test -race -count=1 ./internal/tenancy/... -run 'TestCreateProject|TestOperatorProjectLimitIsHundred|TestServiceCreateValidatesNameBeforeRepository'`. Esperado: `undefined: tenancy.NewProject`.
- [ ] **Step 3: implementar.** `Store.Create`, numa só `InUserTx(titular)`: `LockTitularProjects` → `UserIsOperator` → `CountTitularProjects >= ProjectLimit(op)` dá `ErrProjectLimit` → `InsertProject` → `InsertProjectOwner`. `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/tenancy/... ./internal/account ./internal/storage` e `sqlc diff`.
- [ ] **Step 5:** `feat(tenancy): criar projeto com owner e cota do titular na mesma transação`

### Task 26: `POST /api/v1/projects` (simples)

**Files:**
- Modify: `internal/httpapi/projecthandlers.go`, `internal/httpapi/routes.go:223-225`, `internal/apierr/errors.go`, `internal/httpapi/rotas_protegidas_test.go`, `internal/httpapi/rotas_projeto_paridade_test.go` (se precisar excluir a rota de conta)
- Create: `internal/httpapi/projetos_contrato_internal_test.go`, `internal/httpapi/testdata/contrato-projetos.json`
- Test: `internal/httpapi/projetos_criar_integration_test.go`

**Interfaces:**
- Consumes: `tenancy.Service.Create`, os erros de nome e `tenancy.ErrProjectLimit` (Tasks 24–25); `middleware.RateLimitUserEvery(ctx context.Context, clk clock.Clock, every time.Duration, burst int)`; `projectResponse` (`projecthandlers.go:24`), `decodeJSON`, `usuario`, `erroInterno`, `apierr.WriteValidation`.
- Produces:
  ```go
  const (
      projetoNovoACada = time.Minute
      projetoNovoBurst = 5
  )
  type projectRequest struct{ Name string `json:"name"` }
  func (h *ProjectHandlers) Create(w http.ResponseWriter, r *http.Request)
  // 201 com projectResponse (o mesmo item de GET /api/v1/projects: {"id","name","is_default","role","created_at"})
  // 400 validation_failed fields[{name: required|too_long|invalid}] | 409 project_limit_reached | 401 | 429
  const CodeProjectLimit ErrorCode = "project_limit_reached" // 409, "Limite de projetos atingido."
  ```
  Registro: `porProjetoNovo := middleware.RateLimitUserEvery(ctx, d.Clock, projetoNovoACada, projetoNovoBurst)` (em memória até o PR 5); `criacaoDeProjeto := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(porProjetoNovo(viva(fn)))) }`; `mux.Handle("POST /api/v1/projects", criacaoDeProjeto(ph.Create))`. O titular vem do token, nunca do corpo (`decodeJSON` estrito).
- [ ] **Step 1: testes que falham**
  - `TestCreateProjectReturns201AndListShowsIt`: `POST {"name":"Clientes"}` dá 201 com `role:"owner"`, `is_default:false`; `GET /api/v1/projects` traz 2 itens em `items`, o padrão primeiro; `GET /api/v1/projects/{novo}/targets` dá 200 com `items: []`.
  - `TestCreatedProjectIsInvisibleToAnotherUser`: o intruso não vê o projeto no `GET /projects`; `GET /projects/{novo}/targets` com o token dele dá 404 com `semTrace(corpo) == semTrace(corpo de uuid.NewString())`.
  - `TestCreateProjectRejectsInvalidName` (tabela): `""`, `"   "`, 101 runas, `"a​b"` → 400 `validation_failed` com `fields == [{name, required|too_long|invalid}]`; via admin, o `count` não muda.
  - `TestCreateProjectRejectsUnknownField`: `{"name":"x","titular_id":"…"}` dá 400.
  - `TestCreateProjectLimitIs409`: o 4º dá 409 `project_limit_reached`.
  - `TestCreateProjectHasOwnBucket`: relógio falso; 5 POSTs passam do limitador (os que estouram a cota dão 409, não 429), o 6º dá 429 `rate_limited`; `GET /api/v1/projects` em seguida dá 200.
  - `TestProjectResponseKeepsThePanelContract`: grava ou confere `testdata/contrato-projetos.json` (casos padrão e criado), molde de `channels_contrato_internal_test.go`.
  - `TestProtectedRoutesRequireAuthentication` ganha `{POST, /api/v1/projects}`; `TestEveryRegisteredRouteDeniesByDefault` e `TestEveryMutationRequiresTheAntiCSRFHeader` cobrem pela enumeração; `TestEveryLegacyResourceRouteHasAProjectTwin` não trata `/api/v1/projects` como recurso.
- [ ] **Step 2:** `go test -race -count=1 ./internal/httpapi -run 'TestCreateProject|TestCreatedProjectIsInvisibleToAnotherUser|TestProjectResponseKeepsThePanelContract|TestProtectedRoutesRequireAuthentication|TestEveryRegisteredRouteDeniesByDefault|TestEveryMutationRequiresTheAntiCSRFHeader|TestEveryLegacyResourceRouteHasAProjectTwin'`. Esperado: o POST dá 405 `method_not_allowed`.
- [ ] **Step 3: implementar.** Mapeamento em `ProjectHandlers`: erros de nome → `apierr.WriteValidation`; `ErrProjectLimit` → 409; o resto → `erroInterno`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test`.
- [ ] **Step 5:** `feat(httpapi): criar projeto com POST /api/v1/projects`

### Task 27: status page no segundo projeto (migration 00046) (simples)

**Files:**
- Create: `internal/storage/migrations/00046_status_page_por_projeto.sql`
- Modify: `internal/apierr/errors.go:73-75` (comentário e texto de `CodeStatusPageExists`: "Este projeto já tem uma status page.")
- Test: `internal/statuspage/segundo_projeto_test.go`, `internal/storage/status_page_unique_test.go`

**Interfaces:**
- Consumes: `traduzir` com `constraintUser` e `constraintProjeto` (PR 2, `internal/statuspage/service.go:22-24,279`).
- Produces: Up `ALTER TABLE status_pages DROP CONSTRAINT status_pages_user_id_key;` (com `SET LOCAL lock_timeout = '5s'`). Down `ALTER TABLE status_pages ADD CONSTRAINT status_pages_user_id_key UNIQUE (user_id);`, que falha com 23505 (sem apagar nada) se algum usuário já tiver duas páginas; comentário no Down diz isso. `constraintUser` continua em `traduzir` até o PR 7 (rollback).
- [ ] **Step 1: testes que falham**
  - `TestSecondProjectGetsItsOwnStatusPage` (`statuspage`): U titular de A e B; `Create` em A e em B passam com slugs diferentes; `GET /p/{slug}` de cada um mostra só os itens do próprio projeto; um segundo `Create` em B dá `ErrExists`.
  - `TestStatusPageUserUniqueDownRefusesWithTwoPages` (`storage`): `MigrateUpTo(00046)`; uma página por usuário → `MigrateDownTo(00045)` passa; com duas páginas do mesmo usuário → o down falha com 23505 e via admin as duas páginas continuam.
- [ ] **Step 2:** `go test -race -count=1 ./internal/statuspage ./internal/storage -run 'TestSecondProjectGetsItsOwnStatusPage|TestStatusPageUserUniqueDownRefusesWithTwoPages'`. Esperado: o `Create` em B dá `ErrExists` (UNIQUE por usuário).
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 ./internal/statuspage ./internal/storage ./internal/httpapi`.
- [ ] **Step 5:** `feat(storage): uma status page por projeto, não mais por usuário`

---

# PR 2b.4 — painel por projeto

Contrato do servidor que o painel segue (Task 15 do PR 2 e Task 26): `GET /api/v1/projects` → `{"items":[{"id","name","is_default","role","created_at"}]}`, padrão primeiro e depois por `id` (UUIDv7, ordem de criação), `role` ∈ `owner|manager|member|viewer`; `POST /api/v1/projects` devolve um item igual, 201.

### Task 28: cliente, schemas e chaves de cache por projeto (simples)

**Files:**
- Modify: `web/src/lib/api/schemas.ts`, `web/src/lib/api/cliente.ts`, `web/src/lib/api/consultas.ts:10-31`, `web/src/test/api-falsa.ts` (projetos com estado próprio e rotas `/projects/:projectId/...`), `web/src/test/fixtures.ts`
- Test: `web/src/lib/api/schemas-contrato.test.ts`, `web/src/lib/api/schemas-versao-posterior.test.ts`, `web/src/lib/api/cliente.test.ts`, `web/src/lib/api/chaves-do-projeto.test.ts`

**Interfaces (Produces):**
```ts
export const Papel // z.enum(['owner','manager','member','viewer']) com fallback 'desconhecido' (molde de schemas-versao-posterior)
export const Projeto = z.object({ id, name: z.string(), is_default: z.boolean(), role: Papel, created_at: instante })
export const ListaDeProjetos = z.object({ items: z.array(Projeto) })
// criarApi(...) ganha:
projetos: (sinal?: AbortSignal) => Promise<ListaDeProjetos>
criarProjeto: (e: { name: string }) => Promise<Projeto>
doProjeto: (id: string) => ApiDoProjeto // métodos de recurso de hoje, prefixo caminho`/projects/${id}`
export type ApiDoProjeto = ReturnType<typeof criarApiDoProjeto>
// consultas.ts
chaves.projetos: readonly ['projects']
chaves.doProjeto: (id: string) => { /* as chaves de recurso de hoje, todas com prefixo ['projeto', id] */ }
export function useProjetos(): UseQueryResult<ListaDeProjetos>
```
`me`, `sessoes`, `encerrarSessao`, `trocarSenha` e `totp*` ficam na raiz (são da conta). Os métodos de recurso saem da raiz na Task 30. Sem tela nesta tarefa: nenhuma skill de design.
- [ ] **Step 1: testes que falham**
  - `schemas-contrato.test.ts`, "projeto": todo caso de `internal/httpapi/testdata/contrato-projetos.json` (Task 26) passa em `Projeto`.
  - `schemas-versao-posterior.test.ts`: `role: 'auditor'` vira `'desconhecido'`; `id` ausente continua falhando.
  - `cliente.test.ts`, "recursos vão para o projeto": `api.doProjeto('p/1').alvo('a')` pede `/api/v1/projects/p%2F1/targets/a`; `criarProjeto` manda `X-Requested-With` e o corpo `{name}`.
  - `chaves-do-projeto.test.ts`: toda chave de `chaves.doProjeto('A')` (valores e funções chamadas com argumentos de exemplo) começa por `['projeto','A']`; nenhuma chave de A é prefixo de uma de B.
- [ ] **Step 2:** `cd web && npx vitest run src/lib/api`. Esperado: `doProjeto is not a function`.
- [ ] **Step 3: implementar**, mais `st.projetos` e o estado de recursos por projeto na API falsa.
- [ ] **Step 4:** `cd web && npm test && npm run typecheck && npm run lint`.
- [ ] **Step 5:** `feat(painel): cliente e chaves de cache por projeto`

### Task 29: projeto na URL, contexto do projeto atual e links antigos (simples)

**Files:**
- Create: `web/src/app/projeto.tsx` (provedor e hooks), `web/src/app/RedirecionarAoProjeto.tsx`, `web/src/app/escolher-projeto.ts`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/app/escolher-projeto.test.ts`, `web/src/app/projeto.test.tsx`, `web/src/app/rotas.test.tsx`

**Interfaces (Produces):**
```ts
export type ProjetoAtual = { id: string; nome: string; papel: Papel; padrao: boolean }
export function useProjeto(): ProjetoAtual          // fora do provedor: throw
export function useApiDoProjeto(): ApiDoProjeto
export function useRotaDoProjeto(): (rota: `/${string}`) => string // '/targets/1' -> '/projects/{id}/targets/1'
export function escolherProjeto(lista: readonly Projeto[], lembrado: string | null): Projeto | null
export const CHAVE_DO_ULTIMO_PROJETO = 'stillon.projeto-atual'
```
Rotas: `/` → redireciona ao projeto escolhido (lembrado na lista → `is_default` → primeiro → `null`), preservando a search; `projects/:projectId` → `<ProvedorDoProjeto>` com os filhos de hoje (index, `targets/...`, `heartbeats/...`, `incidents`, `channels`, `status-pages`); `settings` fica fora; `targets/*`, `heartbeats/*`, `incidents`, `channels`, `status-pages` → `<RedirecionarAoProjeto>` (Projeto padrão, mesmo resto e search, `replace`). `/p/` é da página pública e não pode ser usado. Projeto da URL fora de `GET /projects`: renderiza a `NaoEncontrada` existente, sem consultas de recurso. `localStorage` sempre em try/catch.
- [ ] **Step 1: carregar `design-skills:design-systems`** e conferir que a carga e o "não encontrado" usam só `EstadoDaConsulta` (`web/src/components/EstadoDaConsulta.tsx`) e `NaoEncontrada` (`web/src/app/NaoEncontrada.tsx`) como estão, sem componente, token ou texto novo.
- [ ] **Step 2: testes que falham**
  - `escolher-projeto.test.ts`: o lembrado na lista vence; lembrado fora da lista cai no `is_default`; sem padrão, o primeiro; lista vazia → `null`.
  - `projeto.test.tsx`: `renderizarPainel('/?estado=down')` termina em `/projects/{padrão}/?estado=down`; depois de abrir `/projects/B/`, um novo render de `/` vai para B; com o `localStorage` lançando exceção, vai para o padrão; `/projects/{uuid aleatório}/` mostra a `NaoEncontrada` e a API falsa registra 0 pedidos a `/projects/{uuid}/…`.
  - `rotas.test.tsx`: `/targets/a1` vira `/projects/{padrão}/targets/a1` e mostra o detalhe; `/incidents?status=open` preserva a search; o histórico não ganha entrada.
- [ ] **Step 3:** `cd web && npx vitest run src/app`. Esperado: o módulo `./escolher-projeto` não existe.
- [ ] **Step 4: implementar.** O provedor lê `useProjetos()` (em `EstadoDaConsulta` enquanto carrega) e grava o lembrado.
- [ ] **Step 5:** `cd web && npm test && npm run typecheck`.
- [ ] **Step 6:** `feat(painel): projeto atual na URL e links antigos no Projeto padrão`

### Task 30: telas nas rotas do projeto (complexa)

**Files:**
- Modify: as 17 telas e hooks de produção que usam `useApi()` para recurso (`git grep -l "useApi()" -- 'web/src/**/*.ts' 'web/src/**/*.tsx' | grep -v test`), `web/src/lib/api/consultas.ts` (hooks com `useApiDoProjeto()` + `chaves.doProjeto`), `web/src/features/painel/linhas.ts`, `web/src/app/Layout.tsx` (abas com o projeto), `web/src/app/NaoEncontrada.tsx` (link de volta relativo), `web/src/lib/api/cliente.ts` (tira os métodos de recurso da raiz)
- Create: `web/src/app/links-do-projeto.test.ts` (guarda)
- Test: `web/src/features/invalidacoes.test.tsx`, `web/src/app/troca-de-projeto.test.tsx`

**Interfaces:**
- Consumes: `useProjeto`, `useApiDoProjeto`, `useRotaDoProjeto` (Task 29), `chaves.doProjeto` (Task 28).
- Produces: `linhasDoResumo(itens, prefixo: string)` e `linhasDosHeartbeats(hbs, prefixo: string)` com os `href` prefixados; `Aba.para` relativo ao projeto (`'/'`, `'/incidents'`, …), exceto `'/settings'`. Trocar de projeto mantém a seção; um detalhe (`targets/:id`) volta à raiz da seção.
- [ ] **Step 1: carregar `design-skills:design-systems` e `frontend-design:frontend-design`.** Decidem aqui só que as abas e listas mudam de destino e não de aparência: mesmos componentes de `web/src/components/ui`, mesmos tokens, nenhuma classe ou estilo novo; qualquer mudança visual que pareça necessária vira pergunta ao dono na Task 31, não código aqui.
- [ ] **Step 2: testes que falham**
  - `links-do-projeto.test.ts`: glob de produção (molde de `chaves-literais.test.ts`) acusa `to="/targets`, `` to={`/heartbeats ``, `navegar('/…')` e `href: '/…'` para `targets|heartbeats|incidents|channels|status-pages`, exceto em `RedirecionarAoProjeto.tsx`; caso positivo embutido.
  - `troca-de-projeto.test.tsx`, "as listas mudam com o projeto": A com "api-a", B com "api-b"; em `/projects/A/` aparece "api-a"; navegar para `/projects/B/` mostra "api-b" e "api-a" nunca aparece, mesmo com a resposta de A atrasada 500 ms na API falsa.
  - `invalidacoes.test.tsx`: criar alvo em A não refaz o `GET` de B; incidente aberto só em B não põe contador na aba Incidentes de A; criar em `/projects/B/targets/new` navega para `/projects/B/targets/{id}`.
- [ ] **Step 3:** `cd web && npx vitest run src/app src/features`. Esperado: a guarda lista os links absolutos; "api-a" aparece em B.
- [ ] **Step 4: implementar**, arquivo por arquivo.
- [ ] **Step 5:** `cd web && npm test && npm run typecheck && npm run lint && npm run build`.
- [ ] **Step 6:** `refactor(painel): telas nas rotas do projeto atual`

### Task 31: seletor de projeto, criar projeto e estado vazio (complexa, decisão do dono)

**Perguntas abertas para o dono (o implementador propõe opções no Step 1 e para; não decide):**
1. Posição do seletor no desktop: à direita da Marca, antes das abas, ou no grupo da direita, junto do e-mail? No celular (navegação na barra inferior): no cabeçalho ao lado da Marca, ou linha própria abaixo dele?
2. Tipografia do nome do projeto atual: Inter (controle) ou Fraunces (título)? Mostrar o papel ao lado de cada projeto na lista?
3. Selo "Padrão" no Projeto padrão? (O nome "Projeto padrão" continua até existir o renomear, no PR 4.)
4. Textos do estado vazio "sem projetos" e do diálogo "Novo projeto" (título, rótulo do campo, botão), e o aviso de `project_limit_reached`.
5. O `<title>` da aba inclui o projeto (ex.: "Incidentes · Meu projeto · StillOn")?

**Files:**
- Create: `web/src/app/SeletorDeProjeto.tsx`, `web/src/features/projetos/DialogoNovoProjeto.tsx`, `web/src/features/projetos/esquema.ts`, `web/src/features/projetos/SemProjetos.tsx`
- Modify: `web/src/app/Layout.tsx`, `web/src/lib/api/mensagens.ts` (`project_limit_reached`)
- Test: `web/src/app/seletor-de-projeto.test.tsx`, `web/src/features/projetos/novo-projeto.test.tsx`, `web/src/features/projetos/esquema.test.ts`

**Interfaces (Produces):**
```ts
export const EsquemaDoProjeto // z.object({ name }) com trim, mínimo 1 e máximo 100 runas (contar por code point, como o Go)
export function SeletorDeProjeto(): JSX.Element // DropdownMenu do radix-ui (já dependência, radix-ui 1.8.1)
export function DialogoNovoProjeto(p: { aberto: boolean; aoFechar: () => void }): JSX.Element // sobre Dialogo de components/ui/dialogo.tsx
export function SemProjetos(): JSX.Element
```
Ao criar: invalida `chaves.projetos` e navega para `/projects/{novo}/`. `useProjeto().papel` já existe, mas esconder ações por papel é do PR 4.
- [ ] **Step 1: propor as opções de design e PARAR.** Carregar, nesta ordem, e usar cada skill para o que decide aqui:
  - `frontend-design:frontend-design`: 2 opções de posição e tipografia do seletor (perguntas 1–3) dentro da direção C da spec `docs/superpowers/specs/2026-10-14-redesign-painel-design.md` (Fraunces + Inter, primária índigo, "Editorial claro"); nada de estética nova.
  - `design-skills:design-systems`: cada opção montada só com tokens e componentes existentes (`Botao`, `Campo`, `Dialogo`, `avisos` de `web/src/components/ui`); listar o que faltaria e por quê, se algo faltar.
  - `design-skills:ux-writing`: 2 propostas de texto para o botão do seletor (nome acessível), o item "Novo projeto", o diálogo (título, rótulo, botão, erro de campo), o estado vazio, o aviso de `project_limit_reached` (sem citar número, que muda no PR 5) e o `<title>` (pergunta 5).
  - `design-skills:interaction-design`: os estados de cada opção — seletor carregando, lista com 1 e com vários projetos, foco e teclado (Enter/Espaço abre, setas navegam, Esc fecha e devolve o foco ao botão), diálogo enviando, erro de campo, 409 de limite (diálogo aberto, valor mantido), 405/erro genérico.

  Entregar ao dono (via controlador) as opções com prós e contras e uma recomendação por pergunta, em texto ou num protótipo descartável fora de `web/src`. **Não escrever a tela antes da resposta.** As respostas entram aqui, por escrito, antes do Step 2.
- [ ] **Step 2: testes que falham**
  - `seletor-de-projeto.test.tsx`: o botão tem nome acessível com o projeto atual; abre com teclado (Enter, setas) e lista os projetos na ordem do servidor; o atual tem `aria-checked` (ou `aria-current`); Esc fecha e o foco volta ao botão; escolher B em `/projects/A/incidents` leva a `/projects/B/incidents`; em `/projects/A/targets/a1`, leva a `/projects/B/`.
  - `novo-projeto.test.tsx`: "cria e entra" (POST com `{name:"Clientes"}`, URL `/projects/{novo}/`, seletor mostra "Clientes", painel no estado vazio de hoje); nome vazio não envia (0 POSTs, mensagem no campo); erro de campo do servidor (`fields:[{name,'invalid'}]`) aparece no campo; 409 `project_limit_reached` mostra o aviso de `mensagens.ts` e mantém o diálogo aberto com o valor; 405 `method_not_allowed` (binário anterior no release) mostra o aviso genérico sem perder o texto; `GET /projects` → `items: []` faz `/` mostrar `SemProjetos` sem consultas de recurso. Os textos esperados são os decididos no Step 1.
  - `esquema.test.ts`: espelha `TestParseProjectName` nos casos que o zod alcança (trim, vazio, 100/101 runas com `"ç"`); o invisível fica com o servidor.
- [ ] **Step 3:** `cd web && npx vitest run src/app/seletor-de-projeto.test.tsx src/features/projetos`. Esperado: os módulos não existem.
- [ ] **Step 4: implementar** conforme as respostas do Step 1.
- [ ] **Step 5:** `cd web && npm test && npm run typecheck && npm run lint && npm run build`.
- [ ] **Step 6:** `feat(painel): seletor de projeto e criação de projeto`

### Task 32: E2E e acessibilidade com projetos (simples)

**Files:**
- Modify: `web/e2e/api-imitada.ts` (`GET`/`POST /projects` e prefixo `/projects/:id`), `web/e2e/a11y.spec.ts` (`ROTAS` com o prefixo; seletor aberto e diálogo na passada de axe e de teclado), `web/e2e/fluxo.spec.ts`
- Test: `web/e2e/projetos.spec.ts`

**Interfaces:** Consumes o seletor e o diálogo (Task 31). Não produz API.
- [ ] **Step 1: carregar `design-skills:accessibility-audit`** e fazer a auditoria WCAG 2.2 AA do seletor, do diálogo e do estado vazio, conferindo: 1.4.3 contraste de texto ≥ 4,5:1 e 1.4.11 de borda/foco ≥ 3:1, nos temas claro e escuro; 1.4.10 sem rolagem horizontal a 320 px; 2.1.1/2.1.2 tudo por teclado e sem armadilha (o diálogo prende e devolve o foco ao fechar); 2.4.3 ordem de foco; 2.4.7 e 2.4.11 foco visível e não encoberto; 2.5.8 alvos de pelo menos 24×24 px; 2.4.2 `<title>` conforme a decisão da Task 31; 3.2.3 seletor no mesmo lugar em toda tela; 3.3.1/3.3.2 rótulo e erro do campo ligados (`aria-describedby`); 3.3.7 o 409 não apaga o nome digitado; 4.1.2 nome, papel e estado do botão e dos itens (`aria-checked`); 4.1.3 erro e aviso de limite anunciados (`role="status"` ou `aria-live`). Cada item vira asserção no `a11y.spec.ts` ou nota no PR com o motivo. Falha que exija mudança visual volta ao dono, não é decidida aqui.
- [ ] **Step 2: testes que falham**
  - `projetos.spec.ts` (binário real), "criar, trocar e isolar": login; criar "E2E B <sufixo único>" pelo seletor; cadastrar alvo em B; trocar para o padrão e o alvo de B não aparece; voltar a B e aparece; `page.goto('/projects/' + randomUUID() + '/')` mostra a `NaoEncontrada`; `GET /api/v1/projects/{uuid}/targets` com o token dá 404. `afterEach` apaga o alvo (o projeto fica: não há DELETE até o PR 6).
  - `fluxo.spec.ts`: a jornada que abre `/targets/...` segue verde pelo redirecionamento, com a asserção nova da URL `/projects/{padrão}/targets/{id}`.
  - `a11y.spec.ts`: axe sem violações com o seletor aberto e com o diálogo "Novo projeto"; teclado: Tab chega ao seletor, Enter abre, setas e Enter escolhem, Esc fecha com o foco de volta ao botão.
- [ ] **Step 3:** `cd web && npx playwright test e2e/projetos.spec.ts e2e/a11y.spec.ts`. Esperado: a rota da API imitada não existe.
- [ ] **Step 4: implementar** a imitação e os ajustes.
- [ ] **Step 5:** `cd web && npm run e2e` (Docker: um pesado por vez; remover o contêiner ao fim).
- [ ] **Step 6:** `test(e2e): projetos no painel, troca e projeto inexistente`

## Self-Review (rascunho em 2026-11-22; reconciliado com o HEAD `4b5fae39` em 2026-11-22)

- **Cobertura da spec (fatia do 2b no roteiro):** políticas por pertença → Task 22; isolamento no banco para workers → Tasks 16–17; `POST /api/v1/projects` → Tasks 24–26; seletor e projeto na URL → Tasks 28–32; cota de projetos (decisão 2) → Task 25; contagem pelo titular (spec §Cotas, CR2) → Task 23; status page por projeto → PR 2 (ex-Task 21) e Task 27; "cotas sob concorrência" → `TestCreateProjectLimitUnderRace` e `TestTitularLockSharesKeyWithPreviousBinary`; "design segue a direção C" → passos de skill nas Tasks 29–32. Itens estacionados do ledger: todos com dono em § Decisões, E. Fora do 2b, de propósito: PATCH/DELETE de projeto, convites, suspensão no sondador, valores do plano grátis, helper comum do Projeto padrão (PR 3).
- **O que mudou na reconciliação:** Task 21 absorvida (72e3b6fb); Task 20 estende `guarda_escopo_test.go` (000e880a) em vez de criar guarda nova, com `TestOnlyScopeSQLSetsProjectGUC` e `TestInUserTxOnlyInPersonalContexts` no mesmo arquivo e `chamadoresDeScopeToUser` sem mudança; lista de `InUserTx` corrigida (`auth/provas.go`, não `auth` inteiro); heartbeats já filtravam por projeto (a lista permitida da Task 20 tem só as 4 cotas); `cabe` perde o `travar` (só era chamada com `false`); trava de canais ganha a chave advisory `stillon.channels:` (a linha de `users` do titular é invisível ao membro); `semearProjeto` reusado com a assinatura real; os três `TODO(PR 2b)` e os gêmeos de leitura ganharam asserção exata e nome real; Task 23 lista os testes que usam as queries removidas; `RateLimitUserEvery` com `ctx` e relógio; `constraintProjeto` (não `constraintProject`); `heartbeat.inserir` em vez de `Create`/`Clone`; tarefas novas 33 (Projeto padrão sem escrita, UUID nulo no domínio) e 34 (renomes, `narg`→`arg`, pânico de `Routes`, balde de envio nas gêmeas, T15); Up provisória das Tasks 18–19 descrita; passos de skill de design nas tarefas de UI e a parada do dono na Task 31; esforço por tarefa.
- **Tipos e nomes conferidos entre tarefas e com o HEAD:** `testdb.NoProjetoPadrao` (18 → 19, 22, 23); `ListDeliverableChannelsForIncident` (16 → 17); `constraintProjeto` (PR 2 → 27); `semearProjeto`/`recursosDeProjeto` (HEAD → 22); `cabe(ctx, scope)` (20) e `cabeNa(ctx, q, sc, travar)` (23); `cota_*_do_titular`, `CountTitular*`/`LockTitular*`/`ProjectTitular` (23); `tenancy.ParseProjectName`, `ProjectLimit`, `ErrProjectLimit` (24 → 25 → 26); `NewProject`, `Service.Create`, `ProjectSummary` (HEAD → 25 → 26); `projectResponse` e o envelope `items` (HEAD → 26 → 28); `project_limit_reached` (26 → 31); `contrato-projetos.json` (26 → 28); `useProjeto`/`useApiDoProjeto`/`useRotaDoProjeto` (29 → 30, 31); `InsertTargetParams.ProjectID` vira `uuid.UUID` só na 34, depois da 18.
- **Review Focus:** cada linha tem teste na tarefa dona (20, 22, 23, 16, 28, 30); o RF1 usa o teste do PR 2 e o novo da Task 20.
- **Proporção:** SQL só onde fixa valor (políticas, chaves de trava, preâmbulo das funções de cota, fan-out); o resto em assinaturas e asserções. O plano cresceu com as Tasks 33–34 e os passos de design, sem corpo de função.
