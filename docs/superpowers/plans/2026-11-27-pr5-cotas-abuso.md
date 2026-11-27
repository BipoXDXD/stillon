# PR 5 — cotas e abuso: plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** aplicar as cotas do plano grátis pelo titular, levar ao banco os limites de taxa por usuário que ainda vivem em memória, pôr um teto diário de e-mails por conta, tornar o sondador justo entre projetos e gentil com cada host, reservar slugs e dar ao operador `user suspend|unsuspend|usage`, com a suspensão respeitada por todo worker.

**Architecture:** os valores do plano moram no domínio (`tenancy.LimitesDe(isOperator)`), lidos na mesma transação e sob as mesmas travas do titular que o PR 2b já usa (`LockTitular*`, funções `cota_*_do_titular` da 00045). A suspensão é derivada: uma função SQL `projeto_suspenso(uuid)` entra nas reivindicações dos workers, no fan-out e na página pública, e nada é apagado nem pausado. Os limites por usuário reusam `ip_rate_limits` (00036) pela interface `middleware.Orcamentos`, que já existe. O teto por host fica na memória do sondador, que roda num processo só (`--role=all`).

**Tech Stack:** Go 1.26.11, PostgreSQL 17/18, pgx + sqlc, testcontainers, React + zod + vitest (painel em `web/`).

**Spec:** `docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`, seção **Cotas e abuso** (aprovada em 2026-11-22). Roteiro da etapa: `docs/superpowers/plans/2026-11-22-etapa1-multiinquilino.md` (linha 5). Base das cotas: `docs/superpowers/plans/2026-11-22-pr2b-pertenca-projetos.md` (Tasks 23 e 25).

## Global Constraints

- Cotas do plano grátis, do **titular** (`projects.titular_id`): projetos 3; membros por projeto 3 **além do titular**; alvos HTTP 20, com intervalo mínimo de 300 s; heartbeats 20; canais 5 **por projeto**. Alvos e heartbeats somam todos os projetos do titular.
- `users.is_operator`: sem as cotas do plano grátis. Valem os tetos atuais (projetos 100, alvos 100, heartbeats 100, intervalo mínimo de 30 s) e mais membros 100 e canais 10 por projeto (§ Decisões minhas, 2).
- A cota é conferida na mesma transação da criação, sob a trava do titular. Promover alguém a owner não mexe em cota.
- E-mails de conta: teto global de **50/dia** (já existe, `contaemail.TetoDiario`); os avisos de incidente nunca passam por ele.
- Acima de qualquer teto de e-mail, a resposta HTTP fica igual (sem enumeração) e o e-mail não sai.
- Suspensão derivada: nada é apagado nem pausado; sondador, avaliador de heartbeats, despachante, lembretes e página pública ignoram projetos de titular suspenso; ao reativar, tudo volta, inclusive as pausas que o usuário escolheu.
- Login de usuário suspenso: o mesmo 401 genérico da senha errada.
- **Migrations:** a última na `main` é a 00047, e o PR 4 (convites) sai antes e usa 00048 em diante. Aqui os números são relativos: **N = próximo número livre depois do PR 4**, e N, N+1… só fixam a ordem. Cada migration pega o próximo número livre no dia do seu sub-PR (`storage.Migrate` recusa número menor que o aplicado, e a 00015 fica vaga para sempre). Toda migration começa com `SET LOCAL lock_timeout = '5s'` e tem Down.
- Cada sub-PR publica sozinho, com o binário anterior ainda rodando durante o release (merge = deploy).
- Português do Brasil em código, comentários, commits (`tipo(escopo): descrição`) e PRs. TDD: teste novo só vale **depois de visto falhando com o código sabotado**; a sabotagem de cada teste-chave está escrita na tarefa.
- Antes de dizer que um sub-PR funciona: `go build ./... && go vet ./... && make test` (Docker para o Postgres); no painel, `cd web && npm run typecheck && npm run lint && npm test`.
- Comandos de teste focados: `go test -race -count=1 -p 1 <pacotes> -run '<regex>'`. Depois de mexer em `queries/*.sql`: `make generate` e `sqlc diff`.

## O que já existe e fica fora deste plano

| Item da spec | Onde já está | Este plano |
|---|---|---|
| Cota contada pelo titular, somando os projetos dele, sob trava com a chave do binário anterior | 00045, `targets.recusarAlvoAcimaDosTetos`, `heartbeat.inserir`, `notify.cabeNa` (PR 2b.2) | Só troca os **valores** (Tasks 4–6) |
| Cota de projetos 3 / operador 100, na transação do INSERT | `tenancy.ProjectLimit`, `pgstore.Create` (PR 2b.3) | Só move os números para `tenancy.LimitesDe` (Task 1) |
| Teto global de 50 e-mails de conta/dia, balde por destinatário, dreno | `contaemail`, `storage.TetoDeEmailsDeConta`, 00047 (PR 3) | Acrescenta o teto por conta (Tasks 9–10) |
| `ip_rate_limits` no cadastro, "esqueci a senha", confirmação, reset, login, `/totp/verify` | `routes.go:202-266`, `queries/ratelimits.sql` | Leva os baldes **por usuário** (Task 11) |
| Remetente de conta em subdomínio | `ACCOUNT_EMAIL_FROM` (`config.RemetenteDeConta`), passo do runbook em `docs/deploy/vps.md` (~linha 4529) | Nada |
| `InProjectTx` e `ListMyProjects` recusam/escondem projeto de titular suspenso; membro suspenso fora | `queries/scope.sql:ActiveMembershipRole`, `queries/projects.sql:ListMyProjects` | Nada (os workers e o login, sim: Tasks 14–15) |
| Trava de e-mail não confirmado na criação | middleware `criacao` | Nada |
| `user_id` nas reivindicações | `ClaimDueTargets`, `ClaimHeartbeatsTo*` devolvem `user_id` | A Task 12 troca por `project_id` (pedido do 2b, linha 125) |

**Fora (depende do PR 4):** a conferência da cota de membros no aceite do convite e o balde de envio de convites. A Task 7 registra a interface que o PR 4 deve expor e só troca o número se o PR 4 já tiver trazido o teto.

## Decisões que dependem do dono

**D1. Contas que já passam das cotas novas** (o operador é isento; o risco são as contas criadas pelo cadastro público desde o PR 3).
- (a) **Sem retroatividade:** nada é apagado nem pausado; a conta acima do teto só não cria recurso novo até ficar abaixo. `user usage` marca "acima da cota". Prós: zero risco de perder monitor (anti-meta da spec), código nenhum a mais. Contras: quem criou 100 alvos antes do PR fica com 100.
- (b) **Pausar o excedente** numa migration (os mais novos primeiro). Prós: teto real para todos já no release. Contras: muda o estado sem aviso; mistura pausa do sistema com pausa do usuário, e a spec quer as pausas do usuário preservadas; precisa de coluna nova para distinguir.
- (c) **Suspender** as contas acima do teto. Contras: desproporcional.
- **Recomendo (a).** O cadastro público está no ar há poucos dias; o operador confere com `user usage` (Task 17) e suspende à mão quem abusar. Antes do merge do 5.2, rodar na VPS a contagem por titular (consulta do Step 0 da Task 4) e anotar no PR quantas contas passam.

**D2. Alvos existentes com intervalo abaixo de 300 s em conta grátis.**
- (a) **Migration sobe para 300 s** os alvos de titular não operador (Task 8). Prós: o dado diz a verdade e o sondador não precisa saber de plano. Contras: muda configuração do usuário sem ele pedir; a janela do release (binário anterior aceita 30 s) exige repetir o `UPDATE`, idempotente, na migration seguinte (N+1).
- (b) **Deixar como está;** o mínimo vale só ao criar e ao mudar o intervalo. Prós: nada muda sozinho. Contras: o furo continua aberto para quem criou antes; e o PATCH de outro campo precisa conviver com o intervalo legado.
- (c) **Impor no sondador** (`GREATEST(interval_sec, mínimo do plano)` na reivindicação). Prós: dado intocado. Contras: o painel mostra 60 s e o alvo roda a 300 s, e a reivindicação passa a juntar `projects` e `users` em toda volta.
- **Recomendo (a)**, pelo mesmo motivo de D1: poucas contas afetadas, e o painel continua dizendo a verdade. Com (b), a Task 8 sai e o resto não muda (o PATCH já só confere o intervalo quando ele muda: Review Focus 3).

## Decisões minhas (uma linha de porquê cada)

1. **Limites em `tenancy.LimitesDe(isOperator bool) Limites`, função que devolve literal, sem `var` mutável.** Um lugar só para os números; os pacotes `targets`, `heartbeat` e `notify` já importam `tenancy`. Saem `targets.MaxTargetsPerUser`, `heartbeat.MaxPerUser`, `notify.MaxChannels`, `tenancy.MaxProjectsPerTitular/ForOperator` e `tenancy.ProjectLimit`.
2. **Operador: canais 10 por projeto, membros 100.** A spec diz "mantém os tetos atuais de 100", mas o teto atual de canais é 10; 10 por projeto já é mais do que hoje (10 somando tudo), e membros não tinham teto.
3. **Canais passam a contar por projeto** (`CountProjectChannels`), mantendo as duas travas do titular de hoje (`LockUser` + `LockTitularChannels`), para o binário anterior contar em fila. A função `cota_canais_do_titular` fica no banco até o PR 7 (o binário anterior a chama).
4. **O plano é o do titular, lido por `ProjectTitularPlan` (`projects JOIN users`)**: `users` não tem RLS, e `projects` já é visível a todo membro no `InProjectTx`.
5. **Intervalo mínimo:** o `Create` sem intervalo usa `max(DefaultIntervalSec, mínimo do plano)`; o `PATCH` só confere o mínimo quando `interval_sec` vem no corpo; abaixo do mínimo, `422` com `interval_sec: out_of_range` (vocabulário fechado de hoje, sem motivo novo).
6. **A API expõe os limites do titular em cada item de `GET /api/v1/projects` (`limits`).** O painel precisa do mínimo de intervalo do projeto atual, que é do titular e não de quem está logado.
7. **Mensagens de cota no painel sem número** ("Limite de alvos da conta atingido."): os tetos passam a variar por titular, e o código de erro não traz o valor.
8. **Teto diário por conta: 10 e-mails por dia UTC**, numa tabela própria `account_email_daily_by_user` (N+1), reservado na **mesma transação** do teto global e desfeito se o global recusar. Conta = quem causou o envio (reenvio, verificação de canal, convite); cadastro e "esqueci a senha" não têm conta autenticada e ficam só no balde por destinatário. Sem isenção de operador: é defesa da reputação do domínio, não cota de plano.
9. **Baldes por usuário levados ao banco:** reenvio de confirmação, envio de verificação de canal, criação de projeto (e o de convites, se o PR 4 o criar em memória). Ficam em memória `porUsuario` (60/min), `limiteConta` (senha e 2FA), o piso global por IP, ping, página pública e `/verify`: são de alto volume ou do caminho de autenticação, e um `INSERT … ON CONFLICT` por requisição custaria mais que o ganho com uma réplica só.
10. **Sondador justo sem teto rígido por projeto:** a reivindicação ordena por `row_number() OVER (PARTITION BY project_id ORDER BY next_run_at)` e depois por `next_run_at`, ou seja, um de cada projeto por rodada. Um teto fixo por projeto deixaria vagas ociosas quando só um projeto tem alvos vencidos.
11. **Teto por host: 60 requisições/min, rajada 20, por host canônico (`safedial.DialedHost`)**, em memória no sondador. Acima dele a sonda não sai e o alvo volta para `now + espera`, sem check, sem incidente e devolvendo a vaga do semáforo. Os saltos de redirect não contam (risco 4).
12. **Suspensão:** `projeto_suspenso(uuid)` (SQL, `SECURITY INVOKER`) e duas políticas de SELECT em `projects` (escopo `prober` e a página pública do slug), migration N+2. O ping continua aceito durante a suspensão: é barato, e manter `deadline_at` em dia evita incidente falso ao reativar. Suspender revoga todas as sessões; o operador não pode ser suspenso (código de uso).
13. **`user usage`** lê por uma função `uso_do_titular()` (N+3) no `InUserTx` do titular, no molde das funções de cota da 00045. `InProjectTx` recusaria o titular suspenso, que é justamente o caso que o operador quer olhar.
14. **Slugs reservados no Go** (`statuspage.slugReservado`), respondendo `ErrSlugTaken` (o mesmo 409 `slug_taken` de slug ocupado, que o painel já trata); prefixo `stillon` reservado; a página que já tem um slug reservado continua com ele.

## Review Focus

1. **Membro cria no projeto de outro titular.** O plano e o intervalo mínimo são os do titular, nunca os de quem cria: membro grátis em projeto de operador cria alvo de 30 s; operador membro de projeto grátis não fura 300 s. Teste: `TestMemberUsesTitularPlanNotOwn` (Task 4).
2. **Suspensão com incidente aberto e aviso pendente.** Durante, nada é sondado nem avisado e a página pública dá 404; ao reativar, o alvo pausado pelo usuário continua pausado e o resto volta. Teste: `TestUnsuspendRestoresStateAndKeepsPauses` (Task 14).
3. **PATCH de outro campo num alvo com intervalo legado abaixo de 300 s** não pode falhar. Teste: `TestPatchOtherFieldKeepsLegacyInterval` (Task 4).
4. **Criar alvo sem `interval_sec` numa conta grátis**: o padrão de hoje (60 s) seria recusado pela própria cota. Teste: `TestCreateWithoutIntervalUsesPlanDefault` (Task 4).
5. **Adiamento pelo teto por host** não vira check, incidente nem vaga perdida do semáforo. Teste: `TestHostThrottleDefersWithoutCheckOrIncident` (Task 13).

## Fatiamento (um sub-PR por linha, na ordem)

| Sub-PR | Tasks | Migration | Entrega | Por que publica sozinho |
|---|---|---|---|---|
| 5.1 | 1, 2, 3 | — | Limites no domínio, `limits` em `GET /projects`, painel usando o mínimo do projeto e mensagens sem número | Nada é recusado ainda; o painel cai em 30 s se `limits` faltar |
| 5.2 | 4, 5, 6, 7, 8 | N (só com D2 = a) | Cotas grátis aplicadas: alvos, intervalo, heartbeats, canais por projeto, membros (se o PR 4 trouxe) | As travas e as contagens do 2b continuam com a mesma chave; só o número muda |
| 5.3 | 9, 10 | N+1 | Teto diário de e-mail por conta | Tabela nova; o binário anterior a ignora |
| 5.4 | 11 | — | Baldes por usuário em `ip_rate_limits` | Só troca o armazenamento do balde |
| 5.5 | 12, 13 | — | Sondador intercalado por projeto e teto por host | Só muda a ordem e o ritmo da reivindicação |
| 5.6 | 14, 15, 16 | N+2 | Suspensão derivada nos workers e na página pública, login e refresh recusados, CLI `suspend`/`unsuspend` | Políticas só de SELECT, aditivas; usuário nenhum está suspenso antes do CLI |
| 5.7 | 17, 18 | N+3 | `user usage` e slugs reservados | Função nova e regra só no Go |

5.3, 5.4, 5.5 e 5.7 (Task 18) não dependem entre si e podem trocar de ordem; 5.6 vem depois de 5.5 porque as duas mexem em `ClaimDueTargets`.

---

# 5.1 — limites no domínio e na API

### Task 1: `tenancy.LimitesDe` (simples)

**Files:**
- Create: `internal/tenancy/limites.go`
- Modify: `internal/tenancy/project.go` (sai `MaxProjectsPerTitular`, `MaxProjectsForOperator`, `ProjectLimit`), `internal/tenancy/pgstore/store.go:125-133`, `internal/tenancy/project_test.go`
- Test: `internal/tenancy/limites_test.go`

**Interfaces (Produces):**
```go
package tenancy

// Limites são as cotas do titular de um projeto (spec §Cotas e abuso).
type Limites struct {
    Projetos           int
    MembrosPorProjeto  int   // além do titular
    Alvos              int64 // somando os projetos do titular
    Heartbeats         int64 // idem
    CanaisPorProjeto   int64
    IntervaloMinimoSec int32
}

func LimitesDe(isOperator bool) Limites
```

- [ ] **Step 1: testes que falham** (`limites_test.go`)
  - `TestFreePlanLimits`: `LimitesDe(false) == Limites{Projetos: 3, MembrosPorProjeto: 3, Alvos: 20, Heartbeats: 20, CanaisPorProjeto: 5, IntervaloMinimoSec: 300}`.
  - `TestOperatorLimits`: `LimitesDe(true) == Limites{Projetos: 100, MembrosPorProjeto: 100, Alvos: 100, Heartbeats: 100, CanaisPorProjeto: 10, IntervaloMinimoSec: 30}`.
  - `TestLimitesDeReturnsCopies`: mudar um campo do valor devolvido não muda a chamada seguinte.
  - `TestProjectLimit` sai; `pgstore` passa a `tenancy.LimitesDe(operador).Projetos` e o teste de corrida da criação de projeto (`pgstore/create_test.go`) continua verde sem mudança.
- [ ] **Step 2:** `go test -count=1 ./internal/tenancy/... -run 'TestFreePlanLimits|TestOperatorLimits|TestLimitesDeReturnsCopies'`. Esperado: `undefined: LimitesDe`.
- [ ] **Step 3: implementar** `LimitesDe` com `switch` devolvendo literal; trocar o chamador de `ProjectLimit`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/tenancy/...`.
- [ ] **Step 5:** `feat(tenancy): limites do plano grátis e do operador no domínio`

### Task 2: `limits` em `GET /api/v1/projects` (simples)

**Files:**
- Modify: `internal/storage/queries/projects.sql` (`ListMyProjects` ganha `t.is_operator AS titular_is_operator`), `internal/tenancy/ports.go` (`ProjectSummary.Limites Limites`), `internal/tenancy/pgstore/store.go`, `internal/httpapi/projecthandlers.go:26-30`, `internal/httpapi/projetos_contrato_internal_test.go`
- Modify (gerado): `internal/storage/db/projects.sql.go`
- Test: `internal/httpapi/projetos_integration_test.go`

**Interfaces:**
- Consumes: `tenancy.LimitesDe` (Task 1).
- Produces: cada item de `GET /api/v1/projects` ganha
  ```json
  "limits": {"projects": 3, "members_per_project": 3, "targets": 20, "heartbeats": 20,
             "channels_per_project": 5, "min_interval_sec": 300}
  ```
  com os valores do **titular** do projeto. `POST /api/v1/projects` devolve o mesmo formato.

- [ ] **Step 1: testes que falham**
  - `TestProjectListCarriesTitularLimits`: U grátis (usuário inserido sem `is_operator`) e O operador (`testdb.Operador`, helper novo desta task: `UPDATE users SET is_operator = true`); U membro (`testdb.Membro`, papel `member`) do padrão de O. `GET /api/v1/projects` como U traz o padrão de U com `min_interval_sec` 300 e `targets` 20, e o padrão de O com 30 e 100.
  - `TestProjectContractListsLimits` (contrato interno): o JSON de um item tem exatamente as chaves `id, name, is_default, role, created_at, limits`, e `limits` as seis acima.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/httpapi -run 'TestProjectListCarriesTitularLimits|TestProjectContractListsLimits'`. Esperado: chave `limits` ausente.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/tenancy/... ./internal/httpapi` e `sqlc diff`.
- [ ] **Step 5:** `feat(projetos): limites do titular na lista de projetos`

### Task 3: painel usa o mínimo de intervalo do projeto (simples)

**Files:**
- Modify: `web/src/lib/api/schemas.ts:58` (`Projeto.limits` opcional), `web/src/features/alvos/esquema.ts` (`esquemaDoAlvo(min)`, `valoresPadrao` por mínimo, `INTERVALOS` filtrado), `web/src/features/alvos/FormularioAlvo.tsx:164-188`, `web/src/lib/api/mensagens.ts:23-28`, `web/src/test/api-falsa.ts`
- Test: `web/src/features/alvos/esquema.test.ts`, `web/src/features/alvos/alvos.test.tsx`

**Interfaces:**
- Consumes: `limits.min_interval_sec` (Task 2), lido do projeto atual via `useProjeto()` (`web/src/app/projeto.tsx:52`). Sem `limits` (API anterior durante o release): 30.
- Produces: `intervaloMinimoDo(p: Projeto): number`; `esquemaDoAlvo(minIntervalo: number)`; `valoresPadraoPara(minIntervalo: number): ValoresDoAlvo` (intervalo `max(60, min)`).

- [ ] **Step 1: testes que falham**
  - `esquema.test.ts › recusa intervalo abaixo do mínimo do plano`: `esquemaDoAlvo(300)` recusa `interval_sec: 60` com erro no campo `interval_sec`; aceita 300; `esquemaDoAlvo(30)` aceita 30.
  - `esquema.test.ts › padrão respeita o mínimo`: `valoresPadraoPara(300).interval_sec === 300` e `intervaloEscolhido === '300'`; `valoresPadraoPara(30).interval_sec === 60`.
  - `alvos.test.tsx › seletor de intervalo esconde opções abaixo do mínimo`: projeto falso com `limits.min_interval_sec: 300`; o seletor não tem "30 segundos" nem "1 minuto"; a ajuda do campo em segundos diz "De 300 a 86400 (1 dia)."
  - `alvos.test.tsx › sem limits, mínimo de 30`: projeto falso sem `limits`; "30 segundos" aparece.
  - `mensagens`: `target_limit_reached` → "Limite de alvos da conta atingido.", `heartbeat_limit_reached` → "Limite de heartbeats da conta atingido.", `channel_limit_reached` → "Limite de canais deste projeto atingido." (os testes que citam o texto antigo mudam junto).
- [ ] **Step 2:** `cd web && npx vitest run src/features/alvos`. Esperado: falha por `esquemaDoAlvo` sem parâmetro e opções de 30 s visíveis.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** `cd web && npm run typecheck && npm run lint && npm test`.
- [ ] **Step 5:** `feat(painel): intervalo mínimo e mensagens de cota pelo plano do titular`

---

# 5.2 — cotas do plano grátis aplicadas

### Task 4: alvos — teto e intervalo mínimo do titular (complexa)

**Files:**
- Modify: `internal/storage/queries/targets.sql` (`ProjectTitularPlan`), `internal/targets/service.go:62-160,245-330` (`Create`, `recusarAlvoAcimaDosTetos`, `travarAlvosDoTitular`, `editarAlvo`, `lerParaEditar`), `internal/targets/target.go:16,129-136` (sai `MaxTargetsPerUser`; o comentário de `MaxPageSize` passa a citar `tenancy.LimitesDe(true).Alvos`)
- Modify (testes que enchem até o teto antigo ou usam intervalo < 300 com usuário grátis; `git grep -n 'MaxTargetsPerUser\|IntervalSec: *[0-9]\|"interval_sec": *[0-9]' -- '*_test.go'`): cada um passa a `tenancy.LimitesDe(<flag do titular>).Alvos` ou marca o titular com `testdb.Operador`
- Modify (gerado): `internal/storage/db/targets.sql.go`
- Test: `internal/targets/plano_test.go`, `internal/httpapi/cotas_plano_integration_test.go`

**Interfaces:**
- Consumes: `tenancy.LimitesDe`, `testdb.Operador` (Tasks 1–2); `LockTitularTargets`, `CountTitularTargets` (00045).
- Produces:
  ```sql
  -- name: ProjectTitularPlan :one
  SELECT p.titular_id, u.is_operator AS titular_is_operator
    FROM projects p JOIN users u ON u.id = p.titular_id
   WHERE p.id = sqlc.arg(project_id);
  ```
  `travarAlvosDoTitular(ctx, q, projectID) (tenancy.Limites, error)`: lê o plano, trava pela chave de hoje, devolve os limites. `recusarAlvoAcimaDosTetos` compara com `limites.Alvos`. `conferirIntervaloDoPlano(f fields, l tenancy.Limites) error` devolve `*ValidationError` com `interval_sec: out_of_range`.

- [ ] **Step 0 (antes do merge, para D1):** na VPS de produção (srv2070196, pelo caminho do runbook), como o papel da aplicação não enxerga tudo, rodar pelo `psql` de administração a contagem `SELECT p.titular_id, count(t.*) FROM projects p JOIN users u ON u.id = p.titular_id LEFT JOIN targets t ON t.project_id = p.id WHERE NOT u.is_operator GROUP BY 1 HAVING count(t.*) > 20` (e o mesmo para heartbeats e intervalos < 300) e anotar o resultado no PR.
- [ ] **Step 1: testes que falham**
  - `TestFreeTitularStopsAtTwentyTargets` (`targets`): U grátis com 19 alvos (admin); `Create` passa; o 21º dá `ErrLimitReached`. Sabotagem: comparar com `tenancy.LimitesDe(true).Alvos` deixa vermelho.
  - `TestOperatorTitularKeepsHundredTargets`: O operador com 99 alvos; `Create` passa; o 101º dá `ErrLimitReached`.
  - `TestFreePlanTargetQuotaRace`: U com 19 alvos; dois `Create` simultâneos (o gancho `depoisDeContar` segura o primeiro até o segundo esperar a trava, como nos testes de corrida de hoje); exatamente um passa e o outro dá `ErrLimitReached`; via admin, 20 alvos.
  - `TestFreePlanRefusesIntervalBelowMinimum`: U grátis, `Create` com `IntervalSec: 60` dá `*ValidationError` com `interval_sec: out_of_range`; com 300, passa.
  - `TestCreateWithoutIntervalUsesPlanDefault`: `Create` sem `IntervalSec` grava 300 para U grátis e 60 para O.
  - `TestPatchIntervalBelowMinimumRefused`: alvo de U com 300; `Update` com `IntervalSec: 120` dá `out_of_range`.
  - `TestPatchOtherFieldKeepsLegacyInterval`: alvo de U com `interval_sec = 60` gravado via admin; `Update` só do nome passa e o intervalo continua 60.
  - `TestMemberUsesTitularPlanNotOwn`: M grátis membro (`member`) do padrão de O: `Create` com 30 s passa. O membro do padrão de U: `Create` com 30 s dá `out_of_range`. Sabotagem: ler o plano de `scope.UserID` deixa os dois vermelhos.
  - `TestTargetQuotaErrorsOnBothRoutes` (`httpapi`): U grátis com 20 alvos; `POST /api/v1/targets` e `POST /api/v1/projects/{padrão}/targets` dão 409 `target_limit_reached`; com `interval_sec: 60`, 422 com `interval_sec`.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/targets ./internal/httpapi -run 'TestFreeTitularStopsAtTwentyTargets|TestOperatorTitularKeepsHundredTargets|TestFreePlanTargetQuotaRace|TestFreePlanRefusesIntervalBelowMinimum|TestCreateWithoutIntervalUsesPlanDefault|TestPatchInterval|TestPatchOtherFieldKeepsLegacyInterval|TestMemberUsesTitularPlanNotOwn|TestTargetQuotaErrorsOnBothRoutes'`. Esperado: o 21º alvo passa (queria `ErrLimitReached`); 60 s passa (queria `out_of_range`).
- [ ] **Step 3: implementar;** `make generate`. O padrão do intervalo é resolvido dentro da transação, depois de ler o plano: `cmp.Or(in.IntervalSec, max(DefaultIntervalSec, l.IntervaloMinimoSec))`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/targets ./internal/storage ./internal/httpapi ./internal/prober ./cmd/...` e `sqlc diff`.
- [ ] **Step 5:** `feat(targets): cota de 20 alvos e intervalo mínimo de 300 s para titular grátis`

### Task 5: heartbeats — teto do titular (simples)

**Files:**
- Modify: `internal/heartbeat/service.go:24-27,236-252` (`inserir`; sai `MaxPerUser`), testes que usam `heartbeat.MaxPerUser`
- Test: `internal/heartbeat/plano_test.go`

**Interfaces:**
- Consumes: `ProjectTitularPlan` (Task 4), `tenancy.LimitesDe`.
- Produces: `inserir` lê o plano antes de `LockTitularHeartbeats` e compara com `limites.Heartbeats`.

- [ ] **Step 1: testes que falham**
  - `TestFreeTitularStopsAtTwentyHeartbeats`: U grátis com 20 (admin); `Create` e `Clone` dão `ErrLimitReached`. Sabotagem: comparar com 100.
  - `TestOperatorTitularKeepsHundredHeartbeats`: O com 99; `Create` passa; o seguinte dá `ErrLimitReached`.
  - `TestFreePlanHeartbeatQuotaRace`: U com 19; dois `Create` simultâneos (gancho `depoisDeContar`); um passa.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/heartbeat -run 'TestFreeTitularStopsAtTwentyHeartbeats|TestOperatorTitularKeepsHundredHeartbeats|TestFreePlanHeartbeatQuotaRace'`. Esperado: o 21º passa.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/heartbeat ./internal/httpapi`.
- [ ] **Step 5:** `feat(heartbeat): cota de 20 heartbeats para titular grátis`

### Task 6: canais — 5 por projeto (simples)

**Files:**
- Modify: `internal/storage/queries/channels.sql` (`CountProjectChannels`; `CountTitularChannels` sai do sqlc), `internal/notify/service.go:25-29,255-295` (`cabe`, `cabeNa`; sai `MaxChannels`), `internal/httpapi/cotas_projetos_integration_test.go` (subteste `canais` de `TestQuotaCountsSeeEveryProjectOfTheUser` sai: a regra mudou)
- Modify (gerado): `internal/storage/db/channels.sql.go`
- Test: `internal/notify/plano_test.go`, `internal/httpapi/cotas_plano_integration_test.go`

**Interfaces:**
- Consumes: `ProjectTitularPlan`, `LockUser`, `LockTitularChannels`.
- Produces:
  ```sql
  -- name: CountProjectChannels :one
  SELECT count(*) FROM notif_channels WHERE project_id = sqlc.arg(project_id);
  ```
  `cabeNa(ctx, q, sc, travar)`: com `travar`, as mesmas duas travas de hoje; conta só o projeto do escopo e compara com `limites.CanaisPorProjeto`. A pré-conferência (`cabe`) conta igual.

- [ ] **Step 1: testes que falham**
  - `TestChannelQuotaIsPerProject` (`httpapi`): U grátis titular de A (padrão) e B (`testdb.NovoProjeto`); A com 5 canais (admin); `POST /projects/{A}/channels` dá 409 `channel_limit_reached` e o `carteiroFalso` não recebe link; `POST /projects/{B}/channels` dá 201. Sabotagem: voltar a `CountTitularChannels` deixa o B vermelho.
  - `TestOperatorChannelQuotaIsTenPerProject` (`notify`): O com 9 canais num projeto; `Create` passa; o 11º dá `ErrLimitReached`.
  - `TestFreePlanChannelQuotaRace` (`notify`): A com 4; dois `Create` simultâneos (gancho `depoisDeContar`); um passa.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/notify ./internal/httpapi -run 'TestChannelQuotaIsPerProject|TestOperatorChannelQuotaIsTenPerProject|TestFreePlanChannelQuotaRace'`. Esperado: o B dá 409 (queria 201).
- [ ] **Step 3: implementar;** `make generate`. `TestTitularLockSharesKeyWithPreviousBinary` (2b) continua verde sem mudança: as chaves das travas não mudaram.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/notify ./internal/storage ./internal/httpapi` e `sqlc diff`.
- [ ] **Step 5:** `feat(notify): cota de 5 canais por projeto para titular grátis`

### Task 7: membros — teto do plano no aceite do convite (condicional ao PR 4)

**Interface que o PR 4 deve expor** (registrada aqui; se o PR 4 já a trouxe, esta task só troca o número):
- O aceite do convite, na **mesma transação** do `INSERT INTO project_members`, toma `pg_advisory_xact_lock(hashtextextended('stillon.members:' || project_id::text, 0))`, conta `count(*) FROM project_members WHERE project_id = $1 AND user_id <> titular_id` e compara com `tenancy.LimitesDe(titular_is_operator).MembrosPorProjeto` (via `ProjectTitularPlan`).
- Acima do teto: `tenancy.ErrMemberLimit` → 409 `member_limit_reached` no aceite, sem consumir o convite (pode ser aceito depois que alguém sair).

**Files (se o PR 4 deixou um teto provisório):** o arquivo do aceite no PR 4 (procurar por `ErrMemberLimit` ou pela trava `stillon.members:`).

- [ ] **Step 1: testes que falham**
  - `TestInviteAcceptStopsAtThreeMembersBeyondTitular`: projeto de U grátis com 3 membros além de U; o aceite do 4º dá `ErrMemberLimit` e o convite continua válido. Sabotagem: contar o titular junto (`user_id <> titular_id` removido) deixa o 3º recusado.
  - `TestInviteAcceptRaceAtLastSeat`: 2 membros; dois aceites simultâneos; um passa.
- [ ] **Step 2:** rodar os dois. Esperado: o 4º aceito (teto provisório maior).
- [ ] **Step 3:** trocar o teto por `LimitesDe(...).MembrosPorProjeto`.
- [ ] **Step 4:** os dois passam, mais o pacote do aceite.
- [ ] **Step 5:** `feat(tenancy): cota de 3 membros além do titular no aceite do convite`

Se o PR 4 não trouxe teto nenhum, a task implementa a interface acima inteira, com os mesmos testes.

### Task 8: intervalos legados abaixo do mínimo (só com D2 = a)

**Files:**
- Create: `internal/storage/migrations/<N>_intervalo_minimo_do_plano.sql`
- Test: `internal/storage/intervalo_minimo_test.go`

**Interfaces (Produces):**
```sql
-- +goose Up
SET LOCAL lock_timeout = '5s';
UPDATE targets t SET interval_sec = 300
  FROM projects p JOIN users u ON u.id = p.titular_id
 WHERE p.id = t.project_id AND NOT u.is_operator AND t.interval_sec < 300;
-- +goose Down
-- Sem volta: o intervalo anterior não foi guardado (D2).
SELECT 1;
```
O mesmo `UPDATE`, idempotente, entra no começo da N+1 (Task 9), para pegar o que o binário anterior gravou durante o release.

- [ ] **Step 1: teste que falha** — `TestLegacyIntervalsRaisedForFreeTitulars` (`MigrateUpTo(N-1)`, alvos de U grátis com 30 e 600 e de O com 30; `MigrateUpTo(N)`): U fica com 300 e 600, O com 30; `next_run_at` intacto.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage -run TestLegacyIntervalsRaisedForFreeTitulars`. Esperado: U continua com 30.
- [ ] **Step 3:** escrever a migration.
- [ ] **Step 4:** o teste passa, mais `go test -race -count=1 -p 1 ./internal/storage`.
- [ ] **Step 5:** `fix(storage): intervalos abaixo de 300 s sobem ao mínimo do plano grátis`

---

# 5.3 — teto diário de e-mail por conta

### Task 9: tabela e reserva por conta (complexa)

**Files:**
- Create: `internal/storage/migrations/<N+1>_emails_de_conta_por_usuario.sql`
- Modify: `internal/storage/queries/account_email.sql`, `internal/storage/teto_emails_conta.go`, `cmd/stillon/main.go:230`
- Modify (gerado): `internal/storage/db/account_email.sql.go`, `models.go`
- Test: `internal/storage/teto_emails_conta_test.go`

**Interfaces:**
- Produces (N+1):
  ```sql
  -- (com D2 = a: o UPDATE da Task 8 repetido aqui, idempotente)
  CREATE TABLE account_email_daily_by_user (
      user_id uuid    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
      day     date    NOT NULL,
      sent    integer NOT NULL CHECK (sent >= 0),
      PRIMARY KEY (user_id, day)
  );
  -- FORCE RLS e uma política no molde de account_email_daily_scope (app.scope = 'account_email').
  ```
  Down: `DROP TABLE account_email_daily_by_user`.
- Produces (Go):
  ```go
  // Reservar conta um e-mail de conta para hoje. Com conta != uuid.Nil, reserva
  // antes uma vaga do teto da conta, na MESMA transação; se o teto global
  // recusar, a vaga da conta volta (rollback). recusa: "" liberado, "conta", "teto".
  func (t *TetoDeEmailsDeConta) Reservar(ctx context.Context, conta uuid.UUID) (recusa string, err error)
  func (t *TetoDeEmailsDeConta) EnviadosHojePelaConta(ctx context.Context, conta uuid.UUID) (int, error)
  func NewTetoDeEmailsDeConta(b Beginner, clk clock.Clock, teto, tetoPorConta int) *TetoDeEmailsDeConta
  ```
  Query `ReserveAccountEmailForUser :one` no molde de `ReserveAccountEmail` (INSERT … ON CONFLICT DO UPDATE … WHERE sent < teto RETURNING sent), apagando as linhas com mais de 7 dias como a global.

- [ ] **Step 1: testes que falham**
  - `TestAccountCapStopsAtTenPerUser`: `tetoPorConta = 10`; 10 reservas de U dão `""`; a 11ª dá `"conta"`; V ainda reserva.
  - `TestAccountCapRolledBackWhenGlobalRefuses`: teto global já em 50 hoje (admin); reserva de U dá `"teto"` e `EnviadosHojePelaConta(U)` continua 0. Sabotagem: reservar a conta numa transação separada deixa 1.
  - `TestZeroAccountSkipsAccountCap`: `Reservar(ctx, uuid.Nil)` só gasta o global.
  - `TestAccountCapConcurrentReservations`: 20 reservas simultâneas de U dão exatamente 10 `""`.
  - `TestAccountCapResetsNextUTCDay`: relógio falso avança para o dia seguinte UTC; U reserva de novo.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage -run 'TestAccountCap|TestZeroAccountSkipsAccountCap'`. Esperado: `too many arguments in call to t.Reservar`.
- [ ] **Step 3: implementar;** `make generate`; `TestEveryTableForcesRLSWithAPolicy` cobre a tabela nova sem mudança.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/storage ./internal/contaemail` e `sqlc diff`.
- [ ] **Step 5:** `feat(storage): teto diário de e-mails de conta por usuário`

### Task 10: o enviador recebe a conta (simples)

**Files:**
- Modify: `internal/account/emails.go` (`EntregaDeEmail.Conta uuid.UUID`), `internal/account/cadastro.go` (reenvio passa o usuário), `internal/contaemail/contaemail.go` (`Teto`, `liberar`, `TetoDiarioPorConta = 10`, `verificacaoComTeto`), `internal/notify/service.go:71-73` (`VerificationMailer.SendVerification(ctx, conta uuid.UUID, dest crypto.Secret, link string) error`) e quem chama (`ChannelService.Create`, `Resend`, com `scope.UserID`), `cmd/stillon/main.go`
- Test: `internal/contaemail/contaemail_test.go`, `internal/account/cadastro_test.go`, `internal/notify/service_test.go`

**Interfaces:**
- Consumes: `TetoDeEmailsDeConta.Reservar(ctx, conta)` (Task 9).
- Produces: `contaemail.Teto` passa a `Reservar(ctx context.Context, conta uuid.UUID) (string, error)`; a ordem continua destinatário → (conta + global numa transação) → transporte; o log do suprimido leva `motivo=conta`. O PR 4 passa o convidador em `EntregaDeEmail.Conta`.

- [ ] **Step 1: testes que falham**
  - `TestAccountCapSuppressesAfterTenResends` (`contaemail`, teto falso que conta por conta): 11 e-mails com a mesma `Conta` e destinatários diferentes; o transporte recebe 10 e o log tem `motivo=conta`.
  - `TestResendConfirmationCarriesAccount` (`account`): o `AccountMailer` falso recebe `entrega.Conta == U` no reenvio e `uuid.Nil` no cadastro e no "esqueci a senha".
  - `TestChannelVerificationCarriesRequester` (`notify`): o `VerificationMailer` falso recebe `conta == scope.UserID` no `Create` e no `Resend`.
  - `TestSuppressedAccountEmailKeepsSameResponse` (`httpapi`): com o teto da conta esgotado, `POST /api/v1/auth/email/resend` devolve o mesmo status e corpo de quando sai.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/contaemail ./internal/account ./internal/notify ./internal/httpapi -run 'TestAccountCapSuppressesAfterTenResends|TestResendConfirmationCarriesAccount|TestChannelVerificationCarriesRequester|TestSuppressedAccountEmailKeepsSameResponse'`. Esperado: compilação (`unknown field Conta`).
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test`.
- [ ] **Step 5:** `feat(contaemail): teto diário de 10 e-mails por conta`

---

# 5.4 — baldes por usuário no banco

### Task 11: `RateLimitUserCompartilhado` (simples)

**Files:**
- Modify: `internal/httpapi/middleware/ratelimit_compartilhado.go`, `internal/httpapi/routes.go:300,307,360` (e o balde de convites do PR 4, se for de memória)
- Test: `internal/httpapi/middleware/ratelimit_compartilhado_test.go`, `internal/httpapi/limites_usuario_integration_test.go`

**Interfaces (Produces):**
```go
// BaldeUsuario é um orçamento por usuário autenticado com nome próprio.
type BaldeUsuario struct {
    Nome   string
    ACada  time.Duration
    Rajada int
}

// RateLimitUserCompartilhado limita por usuário (o de authn no contexto) com o
// estado em o. A chave é o HMAC de "usuario:<uuid>" com chaveIP. Falha fechado.
func RateLimitUserCompartilhado(o Orcamentos, b BaldeUsuario, chaveIP []byte) func(http.Handler) http.Handler
```
Baldes: `conta_reenvio` (10 min, 3), `canal_envio` (`envioACada`, `envioBurst`), `projeto_novo` (`projetoNovoACada`, `projetoNovoBurst`); convites (se houver) `convite_envio` com os valores do PR 4.

- [ ] **Step 1: testes que falham**
  - `TestSharedUserBucketSpansInstances` (`middleware`): duas cadeias montadas com o mesmo `Orcamentos` em memória (duas "réplicas"), rajada 3; pedidos alternados do mesmo usuário: o 4º dá 429 com `Retry-After`. Sabotagem: `RateLimitUserEvery` deixa o 4º passar.
  - `TestSharedUserBucketKeysByUserNotIP`: dois usuários do mesmo IP não dividem fichas.
  - `TestSharedUserBucketFailsClosed`: `Orcamentos` que devolve erro → 500 `internal`, handler não roda.
  - `TestResendBucketIsShared` (`httpapi`, com `storage.OrcamentosPorIP` real): dois `Server` sobre o mesmo pool; 3 reenvios num e o 4º no outro dá 429.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/httpapi/... -run 'TestSharedUserBucket|TestResendBucketIsShared'`. Esperado: `undefined: RateLimitUserCompartilhado`.
- [ ] **Step 3: implementar** e trocar os três baldes; o comentário de `routes.go:198-201` passa a listar o que fica em memória (Decisão 9).
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/httpapi/...`.
- [ ] **Step 5:** `feat(httpapi): baldes por usuário de reenvio, verificação de canal e projeto no banco`

---

# 5.5 — sondador justo e gentil com cada host

### Task 12: reivindicação intercalada por projeto (complexa)

**Files:**
- Modify: `internal/storage/queries/prober.sql:21-35`, `internal/storage/queries/heartbeats.sql:101-131` (`RETURNING`/`SELECT` com `project_id` no lugar de `user_id`), `internal/prober/scheduler.go`, `internal/prober/heartbeat.go` (se lerem `UserID`), testes que leem `ClaimDueTargetsRow.UserID`
- Modify (gerado): `internal/storage/db/prober.sql.go`, `heartbeats.sql.go`
- Test: `internal/storage/reivindicacao_justa_test.go`

**Interfaces (Produces):** `ClaimDueTargets` com a mesma assinatura e linha (`user_id` → `project_id`):
```sql
WITH vencidos AS (
    SELECT id, next_run_at,
           row_number() OVER (PARTITION BY project_id ORDER BY next_run_at, id) AS vez
      FROM targets
     WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz
), escolhidos AS (
    SELECT id FROM vencidos ORDER BY vez, next_run_at, id LIMIT sqlc.arg(max_rows)::int
), lote AS MATERIALIZED (
    SELECT t.id FROM targets t
     WHERE t.id IN (SELECT id FROM escolhidos)
       AND t.enabled AND t.next_run_at <= sqlc.arg(now)::timestamptz
       FOR UPDATE SKIP LOCKED
)
UPDATE targets t SET next_run_at = … -- o mesmo de hoje
 WHERE t.id IN (SELECT id FROM lote)
RETURNING t.id, t.project_id, …;
```
O `FOR UPDATE` não aceita função de janela no mesmo `SELECT`; por isso a janela fica sem trava e o `lote` re-trava conferindo de novo o vencimento (o EvalPlanQual relê a linha que outra transação acabou de reivindicar).

- [ ] **Step 1: testes que falham**
  - `TestClaimInterleavesProjects`: A com 1000 alvos vencidos há 1 h (admin, `generate_series`), B com 1 alvo vencido agora; `ClaimDueTargets(max_rows = 10)` traz o alvo de B. Sabotagem: voltar a `ORDER BY next_run_at` deixa B fora.
  - `TestClaimKeepsOldestFirstWithinProject`: A com 3 alvos vencidos em instantes diferentes; `max_rows = 2` traz os dois mais antigos de A.
  - `TestClaimRoundRobinAcrossThreeProjects`: A 5, B 5, C 1, `max_rows = 6` → 2 de A, 2 de B, 1 de C e mais 1 de A ou B (o mais antigo na terceira vez).
  - `TestClaimDoesNotDoubleClaimConcurrently`: duas transações de reivindicação simultâneas sobre 20 alvos vencidos; a interseção é vazia e a soma é 20.
  - `TestClaimReturnsProjectID`: a linha devolvida traz o `project_id` do alvo.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage -run 'TestClaimInterleavesProjects|TestClaimKeepsOldestFirstWithinProject|TestClaimRoundRobinAcrossThreeProjects|TestClaimDoesNotDoubleClaimConcurrently|TestClaimReturnsProjectID'`. Esperado: B fora do lote.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/storage ./internal/prober` e `sqlc diff`.
- [ ] **Step 5:** `feat(prober): reivindicação intercalada por projeto`

### Task 13: teto de requisições por host (simples)

**Files:**
- Create: `internal/prober/teto_host.go`
- Modify: `internal/prober/scheduler.go:246-320` (`Tick`), `internal/prober/metrics.go`, `internal/storage/queries/prober.sql`
- Modify (gerado): `internal/storage/db/prober.sql.go`
- Test: `internal/prober/teto_host_test.go`

**Interfaces (Produces):**
```go
const (
    RequisicoesPorHostPorMinuto = 60
    RajadaPorHost               = 20
)
type tetoPorHost struct{ /* baldes por host, com poda dos parados */ }
func novoTetoPorHost(porMinuto, rajada int) *tetoPorHost
// Reservar gasta uma ficha do host em agora; sem ficha, devolve a espera.
func (t *tetoPorHost) Reservar(host string, agora time.Time) (espera time.Duration, ok bool)
```
```sql
-- name: PostponeTarget :exec
UPDATE targets SET next_run_at = LEAST(next_run_at, sqlc.arg(next_run_at)::timestamptz)
 WHERE id = sqlc.arg(id);
```
Métrica `stillon_prober_host_throttled_total`. O host é `safedial.DialedHost(url.Hostname())`; URL que não canonicaliza segue sem teto (a sonda recusa adiante, como hoje). O teto é consultado no `Tick`, depois da reivindicação e antes de disparar; o adiado devolve a vaga do semáforo na hora.

- [ ] **Step 1: testes que falham**
  - `TestHostThrottleAllowsBurstThenWaits` (puro): rajada 20; 20 `Reservar` no mesmo instante dão `ok`; o 21º dá `espera` de 1 s (60/min).
  - `TestHostThrottleIsPerCanonicalHost`: `Exemplo.COM` e `exemplo.com` dividem o balde; `outro.com` não.
  - `TestHostThrottleDefersWithoutCheckOrIncident` (`Tick` com relógio falso e sonda falsa): 25 alvos vencidos no mesmo host; a sonda roda 20 vezes; os 5 outros ficam com `next_run_at = agora + espera`, nenhum check novo, nenhum incidente, e o semáforo volta a ter todas as vagas depois das sondas. Sabotagem: não chamar `PostponeTarget` deixa os 5 com `next_run_at` de um intervalo inteiro.
  - `TestHostThrottleSumsAcrossProjects`: 15 alvos de A e 15 de B no mesmo host → 20 sondas no total.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/prober -run 'TestHostThrottle'`. Esperado: `undefined: novoTetoPorHost`.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/prober ./internal/storage` e `sqlc diff`.
- [ ] **Step 5:** `feat(prober): teto de 60 requisições por minuto por host`

---

# 5.6 — suspensão derivada

### Task 14: workers e página pública ignoram titular suspenso (complexa)

**Files:**
- Create: `internal/storage/migrations/<N+2>_suspensao_derivada.sql`
- Modify: `internal/storage/queries/prober.sql` (`ClaimDueTargets`, `CountDueTargets`), `heartbeats.sql` (`ClaimHeartbeatsToOpen`, `ClaimHeartbeatsToClose`), `incidents.sql` (`ListDeliverableChannelsForIncident`), `notifications.sql` (`ClaimDueNotifications`, `ListReminderCandidates`), `statuspages.sql` (`GetPublicPage`)
- Modify (gerado): os `*.sql.go` correspondentes
- Test: `internal/storage/suspensao_test.go`, `internal/prober/suspensao_test.go`, `internal/httpapi/statuspage_suspensao_integration_test.go`

**Interfaces (Produces, N+2):**
```sql
-- Suspensão derivada (spec §Cotas e abuso): o projeto some dos workers e da
-- página pública enquanto o titular estiver suspenso; nada é apagado.
CREATE POLICY projects_prober_select ON projects FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY projects_public_select ON projects FOR SELECT
    USING (EXISTS (SELECT 1 FROM status_pages s
                    WHERE s.project_id = projects.id AND s.published
                      AND s.slug = NULLIF(current_setting('app.public_slug', true), '')));
-- SECURITY INVOKER: enxerga só o que o escopo de quem chama enxerga.
-- Projeto nulo (linha anterior ao contract) nunca está suspenso.
CREATE FUNCTION projeto_suspenso(projeto uuid) RETURNS boolean
    LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
    SELECT EXISTS (SELECT 1 FROM projects p JOIN users u ON u.id = p.titular_id
                    WHERE p.id = projeto AND u.suspended_at IS NOT NULL) $$;
```
Down: `DROP FUNCTION`, `DROP POLICY` ×2. Cada query listada ganha `AND NOT projeto_suspenso(<project_id do recurso>)` (no fan-out, nas notificações e nos lembretes, o `project_id` do alvo ou do heartbeat do incidente). Notificação pendente de titular suspenso fica `pending` sem gastar tentativa.

- [ ] **Step 1: testes que falham**
  - `TestSuspendedTitularTargetsNotClaimed` (`storage`): alvos vencidos de U e de V; `testdb.Suspender(U)`; `ClaimDueTargets` só traz os de V; `CountDueTargets` só conta os de V. Sabotagem: tirar o filtro da reivindicação.
  - `TestMemberProjectOfSuspendedTitularNotClaimed`: alvo criado por M no projeto de U suspenso não é reivindicado (a suspensão é do titular, não de quem criou).
  - `TestSuspendedTitularHeartbeatsNotEvaluated`: heartbeat de U vencido; `ClaimHeartbeatsToOpen` vazio; com incidente aberto e sucesso, `ClaimHeartbeatsToClose` vazio.
  - `TestSuspendedTitularGetsNoFanOutNorReminders`: incidente aberto de U; `ListDeliverableChannelsForIncident` vazio; `ListReminderCandidates` sem ele; notificação já `pending` não sai em `ClaimDueNotifications` e `attempts` continua igual.
  - `TestSuspendedTitularPublicPageIsNotFound` (`httpapi`): página publicada de U; `GET` público do slug dá o mesmo 404 (status e corpo) de um slug inexistente.
  - `TestUnsuspendRestoresStateAndKeepsPauses` (`prober`): U com alvo A1 ativo vencido e A2 pausado pelo usuário, incidente aberto em A1 e notificação pendente; suspender; um `Tick` não sonda nada; `UPDATE users SET suspended_at = NULL`; o `Tick` seguinte sonda A1, A2 continua `enabled = false`, a notificação sai.
  - `TestProjectsVisibleToProberOnlyThroughPolicy` (catálogo): as políticas de `projects` cujo texto cita `app.scope` ou `app.public_slug` são exatamente as duas novas, ambas `polcmd = 'r'`.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage ./internal/prober ./internal/httpapi -run 'TestSuspendedTitular|TestMemberProjectOfSuspendedTitularNotClaimed|TestUnsuspendRestoresStateAndKeepsPauses|TestProjectsVisibleToProberOnlyThroughPolicy'`. Esperado: alvos de U reivindicados.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test` e `sqlc diff`.
- [ ] **Step 5:** `feat(storage): workers e página pública ignoram projeto de titular suspenso`

### Task 15: suspender, reativar, login e refresh (complexa)

**Files:**
- Modify: `internal/storage/queries/users.sql` (`SuspendUser`, `UnsuspendUser`, `UserSuspension`), `internal/account/account.go`, `internal/auth/user.go:246-260` (`User.SuspendedAt` no `ByEmail`), `internal/httpapi/authhandlers.go:74,201` (`Login`, `Refresh`), `internal/auth/session.go:211` (`Rotate`), o consumo do desafio do `/totp/verify`
- Modify (gerado): `internal/storage/db/users.sql.go`
- Test: `internal/account/suspensao_test.go`, `internal/httpapi/login_suspenso_integration_test.go`

**Interfaces (Produces):**
```sql
-- name: SuspendUser :execrows
UPDATE users SET suspended_at = COALESCE(suspended_at, sqlc.arg(now)::timestamptz)
 WHERE id = sqlc.arg(id) AND NOT is_operator;
-- name: UnsuspendUser :execrows
UPDATE users SET suspended_at = NULL WHERE id = sqlc.arg(id);
```
```go
var ErrOperatorNotSuspendable = errors.New("a conta do operador não pode ser suspensa")
// Suspend marca suspended_at (mantém o primeiro), revoga todas as famílias de
// sessão e audita user.suspended, numa transação. ErrNotFound sem usuário.
func (s *Service) Suspend(ctx context.Context, email string) (revogadas int64, err error)
// Unsuspend limpa suspended_at e audita user.unsuspended. Sessões não voltam.
func (s *Service) Unsuspend(ctx context.Context, email string) error
```
Login: a senha é conferida como hoje (mesmo custo de Argon2id); certa e suspenso → o mesmo 401 `invalid_credentials` da senha errada, sem zerar o contador de falhas e sem emitir desafio de 2FA. `Rotate` de usuário suspenso → `ErrInvalidRefresh` (401 de hoje).

- [ ] **Step 1: testes que falham**
  - `TestSuspendRevokesSessionsAndAudits`: U com duas famílias vivas; `Suspend` devolve 2; `audit_log` tem `user.suspended`; `suspended_at` preenchido.
  - `TestSuspendKeepsFirstTimestamp`: duas chamadas; `suspended_at` é o da primeira.
  - `TestOperatorCannotBeSuspended`: `Suspend(operador)` dá `ErrOperatorNotSuspendable` e nada muda.
  - `TestUnsuspendClearsAndAudits`.
  - `TestSuspendedLoginIsIndistinguishable` (`httpapi`): login de U suspenso com a senha certa e de U com a senha errada dão o mesmo status, código e corpo; com 2FA ligado, nenhum desafio sai. Sabotagem: devolver 403 para suspenso deixa vermelho.
  - `TestSuspendedRefreshRefused`: refresh válido emitido antes da suspensão (com a revogação desligada pelo teste, gravando `suspended_at` via admin) dá 401.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/account ./internal/auth ./internal/httpapi -run 'TestSuspend|TestOperatorCannotBeSuspended|TestUnsuspendClearsAndAudits|TestSuspendedLoginIsIndistinguishable|TestSuspendedRefreshRefused'`. Esperado: `undefined: Suspend`; login de suspenso dá 200.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/account ./internal/auth ./internal/httpapi` e `sqlc diff`.
- [ ] **Step 5:** `feat(account): suspender e reativar conta, com login e refresh recusados`

### Task 16: `stillon user suspend|unsuspend` (simples)

**Files:**
- Modify: `cmd/stillon/user.go:160-190` (validação do subcomando, `switch`, `usoUser`), `docs/deploy/vps.md` (seção de operação: os dois comandos, uma linha cada)
- Test: `cmd/stillon/user_test.go`

**Interfaces:**
- Consumes: `account.Service.Suspend`, `Unsuspend` (Task 15).
- Produces: `stillon user suspend --email X` → `saidaOK` e "conta suspensa; N sessões encerradas"; e-mail sem conta → `saidaNaoCadastrado`; operador → `saidaUso`. `stillon user unsuspend --email X` → `saidaOK` ou `saidaNaoCadastrado`. Nenhum dos dois lê senha.

- [ ] **Step 1: testes que falham**
  - `TestUserSuspendCLI`: saída e código para conta comum, sem conta e operador.
  - `TestUserUnsuspendCLI`: idem.
  - `TestUserSubcommandsListedInUsage`: `usoUser` cita `suspend` e `unsuspend`.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./cmd/stillon -run 'TestUserSuspendCLI|TestUserUnsuspendCLI|TestUserSubcommandsListedInUsage'`. Esperado: código 2 (subcomando desconhecido).
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./cmd/stillon`.
- [ ] **Step 5:** `feat(cli): stillon user suspend e unsuspend`

---

# 5.7 — uso da conta e slugs reservados

### Task 17: `stillon user usage` (complexa)

**Files:**
- Create: `internal/storage/migrations/<N+3>_uso_do_titular.sql`
- Modify: `internal/storage/queries/projects.sql` (`TitularUsage`), `internal/account/account.go` (`Usage`), `cmd/stillon/user.go`, `cmd/stillon/main.go` (o CLI monta o `TetoDeEmailsDeConta`), `internal/storage/guarda_escopo_test.go` (`TestOnlyQuotaFunctionsUseQuotaGUCs` aceita a N+3), `internal/storage/cota_titular_test.go` (`TestQuotaPoliciesAndFunctionsAreContained`: cinco políticas, cinco funções)
- Modify (gerado): `internal/storage/db/projects.sql.go`
- Test: `internal/storage/uso_do_titular_test.go`, `internal/account/uso_test.go`, `cmd/stillon/user_test.go`

**Interfaces (Produces, N+3):**
```sql
CREATE POLICY project_members_cota_select ON project_members FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));
-- uso_do_titular(): o titular é o app.user_id da transação (NULL → 42501);
-- mesmo preâmbulo e mesma cláusula SET das funções da 00045.
CREATE FUNCTION uso_do_titular()
    RETURNS TABLE (project_id uuid, name text, is_default boolean,
                   alvos bigint, heartbeats bigint, canais bigint, membros bigint) …
```
`membros` conta os vínculos sem o titular. Down: `DROP FUNCTION`, `DROP POLICY`.
```go
type UsoDoProjeto struct {
    ID uuid.UUID; Nome string; Padrao bool
    Alvos, Heartbeats, Canais, Membros int64
}
type Uso struct {
    Email       string
    Operador    bool
    SuspensaEm  *time.Time
    Limites     tenancy.Limites
    Projetos    []UsoDoProjeto
    EmailsHoje  int // da conta (Task 9)
    EmailsHojeGlobal int
}
func (s *Service) Usage(ctx context.Context, email string) (Uso, error)
```
Saída do CLI, uma linha por item, `ACIMA` ao lado do que passa do limite (D1):
```
conta: x@y (grátis, ativa)
projetos: 2/3
alvos: 21/20 ACIMA
heartbeats: 3/20
e-mails de conta hoje: 4/10 (global 17/50)
projeto "Projeto padrão": 15 alvos, 2 heartbeats, 5/5 canais, 1/3 membros
```

- [ ] **Step 1: testes que falham**
  - `TestTitularUsageCountsEveryProject` (`storage`): U titular de A e B com alvos, heartbeats, canais e um membro M em B; em `InUserTx(U)`, `TitularUsage` traz as duas linhas com as contagens certas, e depois, na mesma transação, `count(*)` em `targets` é 0 e os `app.cota_*` são `''`.
  - `TestTitularUsageWorksWhileSuspended`: `testdb.Suspender(U)`; a função ainda devolve as linhas.
  - `TestTitularUsageRefusesWithoutUserScope`: em `InProberTx`, 42501.
  - `TestUsageMarksOverQuota` (`account`): U grátis com 21 alvos (admin) → `Uso.Limites.Alvos == 20` e a soma dos projetos 21.
  - `TestUserUsageCLI` (`cmd`): a saída tem as linhas acima, com `ACIMA` só onde passa; sem conta → `saidaNaoCadastrado`.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage ./internal/account ./cmd/stillon -run 'TestTitularUsage|TestUsageMarksOverQuota|TestUserUsageCLI|TestOnlyQuotaFunctionsUseQuotaGUCs|TestQuotaPoliciesAndFunctionsAreContained'`. Esperado: `function uso_do_titular() does not exist`.
- [ ] **Step 3: implementar;** `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `make test` e `sqlc diff`.
- [ ] **Step 5:** `feat(cli): stillon user usage com projetos, contagens e e-mails do dia`

### Task 18: slugs reservados da status page (simples)

**Files:**
- Create: `internal/statuspage/slugs_reservados.go`
- Modify: `internal/statuspage/service.go` (`Create`; `Update` só quando o slug muda), `internal/statuspage/statuspage.go:27` (comentário)
- Test: `internal/statuspage/slugs_reservados_test.go`, `internal/httpapi/statuspages_projeto_integration_test.go`

**Interfaces (Produces):**
```go
// slugReservado: a lista abaixo e todo slug que começa com "stillon".
func slugReservado(slug string) bool
```
Lista inicial: `admin`, `administrador`, `api`, `app`, `assets`, `auth`, `blog`, `cdn`, `conta`, `contas`, `dashboard`, `docs`, `help`, `ajuda`, `login`, `entrar`, `cadastro`, `signup`, `logout`, `mail`, `email`, `painel`, `privacidade`, `termos`, `root`, `static`, `status`, `suporte`, `support`, `www`, `oficial`, `official`, `security`, `seguranca`, `billing`, `pagamento`, `healthchecks`, `google`, `microsoft`, `apple`, `amazon`, `aws`, `azure`, `cloudflare`, `github`, `gitlab`, `paypal`, `stripe`, `facebook`, `meta`, `instagram`, `whatsapp`, `netflix`, `nubank`, `itau`, `bradesco`, `santander`, `caixa`, `bancodobrasil`, `pix`, `mercadopago`, `mercadolivre`, `resend`, `hostinger`.

- [ ] **Step 1: testes que falham**
  - `TestReservedSlugs` (tabela): `admin`, `www`, `stillon`, `stillon-status`, `paypal` → `true`; `minha-loja`, `status-da-loja` → `false`.
  - `TestCreateWithReservedSlugIsTaken` (`httpapi`): `POST /projects/{id}/status-pages` com `admin` dá 409 `slug_taken`, igual a slug ocupado.
  - `TestRenameToReservedSlugIsTaken`: `PATCH` para `api` dá 409 `slug_taken`.
  - `TestExistingReservedSlugKeepsWorking`: página com slug `status` gravada via admin; `PATCH` só do título passa.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/statuspage ./internal/httpapi -run 'TestReservedSlugs|TestCreateWithReservedSlugIsTaken|TestRenameToReservedSlugIsTaken|TestExistingReservedSlugKeepsWorking'`. Esperado: `undefined: slugReservado`.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./internal/statuspage ./internal/httpapi`.
- [ ] **Step 5:** `feat(statuspage): slugs reservados respondem como ocupados`

---

## Riscos

1. **Churn de testes no 5.2.** Testes que inserem usuário cru (grátis por padrão) e enchem até 100 ou usam intervalo de 30/60 s vão quebrar; o `git grep` da Task 4 lista os arquivos. Não marcar todo usuário de teste como operador: o plano grátis tem de ser o caminho testado.
2. **Janela do release do 5.2.** O binário anterior ainda aceita 100 alvos e 30 s enquanto o novo sobe. As travas têm a mesma chave, então não há furo de corrida; o excedente da janela é o caso de D1 e, com D2 = a, o `UPDATE` repetido na N+1 fecha os intervalos.
3. **Custo da reivindicação justa.** A janela ordena todos os alvos vencidos em cada volta. Com as cotas (20 por conta grátis) isso é pequeno; depois de uma parada longa, o lote é o total de alvos. Medir com `EXPLAIN ANALYZE` sobre 5.000 alvos vencidos no Step 4 da Task 12 e anotar no PR.
4. **Teto por host não vê os redirects.** Um alvo que redireciona para outro host gasta a ficha do host da URL, não do destino. A `safedial` continua barrando IP interno em cada salto; o teto é proteção de cortesia, não de SSRF.
5. **Políticas novas em `projects` (N+2).** Abrem `projects` ao escopo `prober` e à página pública do slug. O `prober` já lê todos os alvos; a página pública só vê o projeto da própria página. `TestProjectsVisibleToProberOnlyThroughPolicy` impede que outra política cite esses GUCs.
6. **PR 4 ainda não existe.** Os nomes da Task 7 (`ErrMemberLimit`, `member_limit_reached`, trava `stillon.members:`) e o balde de convites da Task 11 são a proposta deste plano; se o PR 4 escolher outros, vale o do PR 4 e estas tarefas se ajustam.
7. **Contract (PR 7):** registrar lá a remoção de `cota_canais_do_titular` (sem uso depois da Task 6) e do `LockUser` da cota de canais.
