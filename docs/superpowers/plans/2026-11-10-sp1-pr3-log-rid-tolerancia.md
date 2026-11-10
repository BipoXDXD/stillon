# SP1, PR 3: `/log`, `rid`, `source`, em tolerância, retomada por ping e sinalizar agora — plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** a tarefa registra linhas de log sem mexer no prazo (`POST /ping/{token}/log`), identifica execuções com
`?rid=<uuid>`, e cada evento diz de onde veio (`ping` ou `panel`); o estado mostra "em tolerância"; a tarefa pausada pode
voltar sozinha com um sucesso; o dono sinaliza um sucesso pelo painel.

**Arquitetura:** a migration "+1 `heartbeat_events_log_run`" acrescenta `log` ao enum de eventos, `run_id` e `source` em
`heartbeat_events`, e `last_run_id` e `ping_resumes` em `heartbeats`, tudo inofensivo ao binário do PR 1. A decisão fica em
funções puras (`State.Apply` com `Run`, `ResumedByPing`, `statusOf` com objeto de parâmetros); o ping e o sinalizar agora
passam pelo mesmo `registrarSinal`. O `rid` é lido da query por uma função pura, antes do corpo e do banco.

**Stack:** Go 1.26.10, pgx v5, sqlc, goose, Postgres 18 via testcontainers; React + Zod + Vitest no painel.

**Spec:** `/Users/01bipo/Documents/Claude/stillon-continuidade/repo/.claude/worktrees/sp1-piso/docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`,
seções 2, 5.1 ("Em tolerância", "Retomada por ping", "`/log`", "Run id", "Sinalizar agora"), 6 (linha +1 e regra do
`set_config`), 7.1 (`/log`, `?rid`, regra da query nas rotas por token), 7.2 (`ping-now`, campos novos, eventos com
`run_id` e `source`), 8, 9 (Em tolerância, `/log`, `rid`, Rollback, Descida, Query, Acesso, Entrada, Saída, Painel), 10 (R2)
e 12 (PR 3). Planos irmãos: PR 1 (`docs/superpowers/plans/2026-11-10-sp1-pr1-piso-de-rollback.md`) e PR 2
(`/Users/01bipo/Documents/Claude/stillon-pos-janela/specs/planos/2026-11-10-sp1-pr2-faixas-e-acoes.md`).

**Decisões do dono por delegação:** D6 = B (GET opcional por tarefa, só POST como padrão), D10 = B (`start + grace`
opcional, booleano `start_uses_grace`), D14 = B (corpo opt-in por tarefa, 10 KiB). **Nenhuma entra aqui:** são o PR 11, que
depende deste (o limite de execução usa o `rid` daqui).

**Dependências:** PR 1 publicado (tolera `kind` de evento desconhecido e `schedule.kind = 'unknown'`; `IsKnown`,
`ErrScheduleUnsupported`, `statusOf` com `knownSchedule`). O PR 2 não é pré-requisito, mas este plano traz o ping-now que
saiu dele, e a Task 6 estende `Service.Clone` se o PR 2 já estiver na `main` (senão, o PR 2 copia `ping_resumes` ao entrar).

## Rulings deste plano

- **Ping-now mora aqui** (decidido no plano do PR 2): só aqui `source = 'panel'` existe. Passa pelo mesmo `registrarSinal`
  do ping, então vale a janela de repetição de 10 s (dois cliques seguidos registram um sucesso; a resposta é 200 com o
  heartbeat nos dois) e vale `ping_resumes`. Agendamento desconhecido: grava e não mexe no prazo, como o ping do PR 1.
- **Forma do `rid`:** só a canônica de 36 caracteres (`8-4-4-4-12`), hex sem distinção de caixa, guardada em minúsculas.
  Vazio, com chaves, `urn:uuid:`, 32 hex sem hífen, repetido (`?rid=a&rid=b`) ou com escape inválido → 400 `rid` `invalid`.
- **Ordem do 400:** o `rid` é lido antes de drenar o corpo e antes do banco, em toda rota de ping por token. O balde por
  token (middleware) vem antes, como hoje: ele não diz se o token existe.
- **"Allowlist de query" nas rotas por token** é só a leitura do `rid`: os demais parâmetros seguem ignorados, para não
  quebrar quem já chama (§7.1). A allowlist estrita (desconhecido → 400) é das rotas por slug, no PR 8. A leitura varre a
  query crua par a par, porque `url.ParseQuery` descarta o par com escape inválido e um `rid` malformado passaria como
  ausente.
- **`/log` não muda estado:** não mexe em `last_signal`, `last_ping_at`, `last_run_id`, prazo, falha nem execução aberta;
  nunca é repetição; aceita `rid`. Divide o teto de 1000 eventos.
- **Duração por `rid` sem coluna nova:** num sucesso ou falha com `rid`, o início vem do evento `start` mais recente de
  mesmo `rid` (índice da +1). A execução aberta da linha (`last_start_at`) só é consumida se esse início for ela; senão
  continua aberta (outra execução). Sem início no log (nunca veio ou foi aparado pelo teto), a execução fica sem duração e
  `last_start_at` intacto. Sem `rid`, tudo como hoje.
- **`last_run_id`** é o `rid` do último sinal que mudou o estado (start, success, fail); `isRepeat` compara `LastSignal`,
  `LastRunID` e a janela.
- **Ordem do estado:** `paused`, `failing`, `late`, `waiting`, `grace`, `up`. Tarefa que nunca terminou uma execução fica
  "Aguardando" até atrasar (como o "new" do serviço de referência). `grace` exige agendamento conhecido e nenhuma execução
  aberta. No painel, `grace` é exibido como `degraded` ("Em tolerância"), que não pede atenção.
- **`statusOf` ganha objeto de parâmetros** (`statusFacts`) antes de crescer: com o PR 1 ele já tem 7 parâmetros.
- **Retomada por ping:** só o sucesso retoma, e só com `ping_resumes` ligado e a tarefa pausada; grava `enabled = true` e
  `resumed_at = agora`; o prazo, a falha e a execução já são tratados pelo sucesso. Repetição não retoma (não há evento).
- **"Avançado" no formulário nasce aqui**, como `<details>` com só a retomada por ping; PRs 8, 11 e 12 acrescentam campos.
- **Rollback depois da +1:** o binário do PR 1 grava sinais sem `rid`, e `last_run_id` fica com o valor velho. Na volta, um
  sucesso sem `rid` em até 10 s de um sucesso do binário antigo não é repetição. Custo aceito: no máximo um evento a mais.

## Restrições globais

- Migration: `internal/storage/migrations/000NN_heartbeat_events_log_run.sql`, onde `000NN` é o **próximo número livre na
  hora do merge** (ex.: `00024` se o PR 2 entrar antes; `00023` se este entrar antes, e então o PR 2 renumera). Testes
  acham a versão por `versaoDaMigration(t, "heartbeat_events_log_run")`; se o PR 2 ainda não estiver na `main`, a Task 1
  cria esse helper.
- Colunas, literais da §6: `heartbeat_events.kind IN ('start','success','fail','log')`; `heartbeat_events.run_id uuid`;
  `heartbeat_events.source text NOT NULL DEFAULT 'ping' CHECK (source IN ('ping','panel'))`; índice
  `(heartbeat_id, run_id, id DESC) WHERE run_id IS NOT NULL`; `heartbeats.last_run_id uuid`;
  `heartbeats.ping_resumes bool NOT NULL DEFAULT false`. `heartbeats.last_signal` **não** ganha `log`.
- Descida: `set_config('app.scope', 'prober', true)`; `DELETE` dos eventos `log`; remove índice e colunas; CHECK antigo.
- Ping: `POST /ping/{token}/log` → 200 fixo ou 404, como os outros sufixos; `rid` malformado → 400 antes de olhar o token.
- API: `POST /api/v1/heartbeats/{id}/ping-now` → 200 com o heartbeat; POST/PATCH aceitam `ping_resumes` (bool; `null` no
  PATCH → 400); saída ganha `ping_resumes` e `state = "grace"`; eventos ganham `run_id` (string ou `null`) e `source`.
- Nada de D6, D10, D14, slug, chave, filtros ou linha do tempo unificada (PR 4) neste PR.
- `go build ./... && go vet ./... && make test` antes de dizer que funciona; teste novo só vale visto falhar com a
  sabotagem indicada, registrada no corpo do PR. Valor esperado é literal.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%, só o CI.

## Foco de revisão

1. `rid` em formas quase válidas: maiúsculas aceitas e guardadas em minúsculas; `{…}`, `urn:uuid:…`, 32 hex, vazio,
   repetido, `%00`, `'; DROP TABLE` → 400, nunca 500 nem 404. Teste na Task 5.
2. Sucesso com `rid` cujo início foi aparado pelo teto de 1000: 200, sem duração, `last_start_at` intacto, sem erro.
   Teste na Task 4.
3. `/log` em tarefa pausada, em falha e com agendamento desconhecido: 200, um evento, nada mais muda (inclusive com
   `ping_resumes` ligado). Teste na Task 4.
4. Duas execuções sobrepostas (start A, start B, success A, success B): durações literais de cada uma, e a execução B
   continua aberta depois do sucesso de A. Teste na Task 2.
5. Retomada por ping com o sucesso repetido em 10 s: o primeiro retoma e grava `resumed_at`; o segundo não grava evento nem
   muda `resumed_at`. Teste na Task 4.

---

### Task 1: migration +1 `heartbeat_events_log_run`, com descida e piso

**Files:**
- Create: `internal/storage/migrations/000NN_heartbeat_events_log_run.sql`
- Create: `internal/storage/heartbeat_log_run_test.go`
- Modify: `internal/storage/helpers_test.go` (só se `versaoDaMigration` ainda não existir; assinatura no plano do PR 2)

**Interfaces:**
- Produz: as colunas, o CHECK `heartbeat_events_kind_check` (mesmo nome do atual) e o índice
  `heartbeat_events_run_idx`.

- [ ] **Step 1: testes que falham.**
  - `TestLogRunMigrationDefaultsKeepTheFloorWorking`: com o schema novo, o `INSERT` do binário do PR 1, como literal
    (`INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, runtime_ms, exit_code) VALUES (...)`), grava com
    `source = 'ping'` e `run_id IS NULL`; o `UPDATE` literal de `SaveHeartbeatPing` do PR 1 deixa `last_run_id` e
    `ping_resumes` como estavam; um evento `kind = 'log'` com `exit_code` 3 → `23514` (o CHECK de `exit_code` segue);
    `source = 'cron'` → `23514`; `heartbeats.last_signal = 'log'` → `23514`.
  - `TestLogRunMigrationDownRemovesLogsAndColumns`: eventos `start`, `log`, `success` com `run_id` e um com
    `source = 'panel'`; `MigrateDownTo(versão - 1)` → só `start` e `success` ficam; colunas e índice somem
    (`information_schema.columns`, `pg_indexes`); INSERT com `kind = 'log'` → `23514`. `MigrateUpTo(versão)` → sem erro.
  - `TestFloorBinaryBootsOnLogRun`: `migrationsAte(t, "<nome do arquivo +1>")` sobre o schema novo → `Up` aplica 0.
- [ ] **Step 2:** `go test -run 'LogRunMigration|FloorBinaryBootsOnLogRun' ./internal/storage` → FAIL.
- [ ] **Step 3:** escrever a migration. Comentário no topo: o binário do PR 1 lista eventos `log` (o painel dele mostra
  "Outro sinal") e não cita as colunas novas; o sqlc expande `SELECT *` em colunas explícitas na geração.
- [ ] **Step 4:** PASS. Sabotagens: tirar o `set_config` do Down → o `DELETE` não enxerga linha e o CHECK antigo falha;
  `source` sem `DEFAULT` → o `INSERT` do piso falha com `23502`.
- [ ] **Step 5:** commit `feat(storage): eventos de log, run id e origem do sinal`.

### Task 2: domínio — `log`, `Run` e repetição por `rid`

**Files:**
- Modify: `internal/heartbeat/heartbeat.go`
- Test: `internal/heartbeat/heartbeat_test.go`

**Interfaces:**
- Produz:
  - `const SignalLog Signal = "log"`.
  - `type Source string`; `const SourcePing Source = "ping"`; `const SourcePanel Source = "panel"`.
  - `type Run struct { ID *uuid.UUID; StartAt *time.Time }` — `StartAt` só faz sentido com `ID`: o início de mesmo `rid`
    lido do log, ou `nil`.
  - `State` ganha `LastRunID *uuid.UUID`; `Event` ganha `RunID *uuid.UUID`.
  - `func (s State) Apply(sig Signal, now time.Time, run Run, sch Schedule, grace time.Duration) (State, *Event)` (troca a
    assinatura; os chamadores atuais passam `Run{}`).
  - `func ResumedByPing(sig Signal, enabled, pingResumes bool) bool`.

- [ ] **Step 1: testes que falham** (instantes literais a partir de `t0 = 2026-11-10T12:00:00Z`, `ridA`, `ridB` fixos):
  - `TestLogChangesNothingButTheEvent`: estado com prazo, `LastSignal = success`, `LastPingAt`, falha e início aberto →
    `Apply(SignalLog, …)` devolve o mesmo `State` e o evento `{At, Kind: log, RunID}`; dois `log` em 1 s geram dois eventos.
  - `TestRepeatComparesTheRunID`: dois sucessos em 5 s com `ridA` → o segundo é repetição; `ridA` e `ridB` → dois eventos;
    `nil` e `ridA` → dois eventos; `nil` e `nil` → repetição (como hoje).
  - `TestOverlappingRunsMeasureTheirOwnStart`: start A em t0, start B em t0+10 min, success A em t0+30 min com
    `StartAt = t0` → `RuntimeMS = 1800000`, `LastStartAt` segue t0+10 min (B aberta); success B em t0+40 min com
    `StartAt = t0+10min` → `1800000` e `LastStartAt` zerado.
  - `TestRunWithoutStartInTheLogHasNoRuntime`: success com `ridA` e `StartAt = nil`, com `LastStartAt` aberto → evento sem
    duração, `LastRuntimeMS = nil`, `LastStartAt` intacto, prazo recalculado.
  - `TestLastRunIDFollowsTheLastStateChangingSignal`: start A → `LastRunID = A`; log B → segue A; fail sem rid → `nil`.
  - `TestResumedByPing`: tabela — só `(SignalSuccess, enabled=false, pingResumes=true)` é `true`; start, fail e log nunca.
- [ ] **Step 2:** `go test -run 'LogChanges|RepeatCompares|OverlappingRuns|RunWithoutStart|LastRunID|ResumedByPing' ./internal/heartbeat`
  → FAIL (não compila).
- [ ] **Step 3: implementar.** `isRepeat` ganha o `run.ID`; `log` sai antes do `isRepeat` e não toca o estado;
  `closeRun` com `run.ID != nil` mede de `run.StartAt` e só consome `LastStartAt` quando `run.StartAt` é igual a ele.
  Atualizar os chamadores e os testes atuais para `Run{}` (comportamento sem `rid` inalterado: os testes antigos ficam
  verdes sem outra mudança).
- [ ] **Step 4:** PASS, e o pacote inteiro verde. Sabotagens: ignorar o `rid` em `isRepeat` → `RepeatCompares` quebra;
  consumir sempre `LastStartAt` → `OverlappingRuns` quebra; tratar `log` como sucesso → `LogChanges` quebra.
- [ ] **Step 5:** commit `feat(heartbeat): sinal de log e execução identificada por rid`.

### Task 3: domínio — estado "em tolerância"

**Files:**
- Modify: `internal/heartbeat/service.go` (`statusOf`, `toHeartbeat`, `StatusGrace`)
- Modify: `internal/heartbeat/export_test.go` (`var StatusOf = statusOf`; `type StatusFacts = statusFacts`)
- Test: `internal/heartbeat/heartbeat_test.go`

**Interfaces:**
- Consome: `knownSchedule` do PR 1.
- Produz: `const StatusGrace Status = "grace"`;
  `type statusFacts struct { Enabled, Failing, OpenIncident, KnownSchedule, FinishedARun, RunOpen bool; Deadline time.Time; Grace time.Duration }`;
  `func statusOf(f statusFacts, now time.Time) Status`.

- [ ] **Step 1: refatorar antes.** Introduce Parameter Object: `statusOf` passa a receber `statusFacts` (sem `Grace` nem
  `RunOpen` ainda); `toHeartbeat` monta o objeto. Testes do pacote verdes. Commit `refactor(heartbeat): estado recebe os
  fatos num objeto`.
- [ ] **Step 2: testes que falham**, `TestGraceStateBoundaries`, com `Deadline = 2026-11-10T12:00:00Z`, `Grace = 5 min`,
  agendamento conhecido, execução terminada, sem execução aberta:
  - `now = 11:55:00` → `grace`; `11:54:59.999999999` → `up`; `12:00:00` → `late`;
  - com `RunOpen` em `11:57` → `up`; sem `FinishedARun` em `11:57` → `waiting`; `KnownSchedule = false` em `11:57` → `up`;
    pausado → `paused`; `Failing` → `failing`; `OpenIncident` com prazo futuro → `late`.
- [ ] **Step 3:** `go test -run GraceState ./internal/heartbeat` → FAIL.
- [ ] **Step 4:** implementar na ordem do Ruling; `toHeartbeat` preenche `Grace` e `RunOpen = h.LastStartAt != nil`.
- [ ] **Step 5:** PASS. Sabotagem: trocar `!now.Before(deadline.Add(-grace))` por `now.After(...)` → o caso de 11:55:00
  quebra.
- [ ] **Step 6:** commit `feat(heartbeat): estado em tolerância derivado na leitura`.

### Task 4: o sinal no banco — `rid`, `source`, `/log` e retomada

**Files:**
- Modify: `internal/storage/queries/heartbeats.sql` (`InsertHeartbeatEvent`, `SaveHeartbeatPing`, `GetRunStartAt`,
  `ListHeartbeatEvents`); `make generate`
- Modify: `internal/heartbeat/ping.go`
- Test: `internal/heartbeat/ping_test.go`

**Interfaces:**
- Consome: Task 2.
- Produz:
  - `InsertHeartbeatEvent` com `run_id` e `source`; `SaveHeartbeatPing` com `last_run_id`, `enabled`, `resumed_at`;
    `-- name: GetRunStartAt :one` → `SELECT at FROM heartbeat_events WHERE heartbeat_id = $1 AND run_id = $2 AND kind =
    'start' ORDER BY id DESC LIMIT 1`; `ListHeartbeatEvents` devolve também `run_id` e `source`.
  - `type sinalRecebido struct { sig Signal; code *int16; runID *uuid.UUID; source Source }`.
  - `func registrarSinal(ctx context.Context, q *db.Queries, h db.Heartbeat, s sinalRecebido, now time.Time) error` —
    substitui `PingService.registrar`; serve ao ping e à Task 7.
  - `func (s *PingService) Ping(ctx context.Context, token string, sig Signal, runID *uuid.UUID) error`;
    `func (s *PingService) PingExit(ctx context.Context, token string, code ExitCode, runID *uuid.UUID) error`.

- [ ] **Step 1: testes que falham** (`novoAmbiente`, relógio falso):
  - `TestLogKeepsDeadlineAndLastSignal`: prazo, `last_signal`, `last_ping_at`, `last_run_id` relidos iguais aos literais
    de antes; 1 evento `log` com `source = 'ping'`.
  - `TestLogSharesTheEventCap`: 1000 eventos, um `log` → 1000 eventos, o mais antigo saiu.
  - `TestLogOnPausedFailingAndUnknownHeartbeats`: cada um → sem erro, 1 evento, `enabled`, `failing_since` e prazo
    intactos (o desconhecido com o helper de CHECKs do PR 1), também com `ping_resumes = true`.
  - `TestRunIDDurationsFromTheLog`: start A, start B, success A (+20 min), success B (+30 min desde B) pelo `Ping` →
    eventos com `run_id` gravado e `runtime_ms` 1200000 e 1800000.
  - `TestSuccessWhoseStartWasTrimmed`: start A, 1000 `log`s, success A → sem erro, `runtime_ms` nulo.
  - `TestPingResumesOnSuccess`: pausada, `ping_resumes = true`: start → segue pausada; success → `enabled`, `resumed_at`
    = agora, prazo recalculado (literal); o mesmo sucesso em 5 s → nenhum evento novo, `resumed_at` igual; com
    `ping_resumes = false` → segue pausada.
- [ ] **Step 2:** `go test -run 'Log|RunID|SuccessWhoseStart|PingResumes' ./internal/heartbeat` → FAIL.
- [ ] **Step 3: implementar.** `registrarSinal`: `ScheduleFromRow`; com `runID` e sinal de sucesso ou falha,
  `GetRunStartAt` (`pgx.ErrNoRows` → `StartAt nil`); `Apply`; sem evento, retorna; `ResumedByPing` decide `enabled` e
  `resumed_at`; `SaveHeartbeatPing` só quando o sinal não é `log`; `InsertHeartbeatEvent` com `run_id` e `source`; trim.
- [ ] **Step 4:** PASS. Sabotagens: chamar `SaveHeartbeatPing` também no `log` → `LogKeeps…` quebra; tirar
  `kind = 'start'` da consulta → a duração de A sai do próprio `log`/sucesso e quebra.
- [ ] **Step 5:** commit `feat(heartbeat): ping grava rid, origem, log e retoma por sucesso`.

### Task 5: HTTP do ping — rota `/log` e leitura do `rid`

**Files:**
- Modify: `internal/httpapi/pinghandlers.go`
- Create: `internal/httpapi/runid_internal_test.go`
- Test: `internal/httpapi/ping_integration_test.go`

**Interfaces:**
- Consome: Task 4.
- Produz: `func lerRunID(rawQuery string) (*uuid.UUID, bool)` (false = 400); rota `POST /ping/{token}/log`; `sinal` e
  `codigoDeSaida` leem o `rid` e repassam.

- [ ] **Step 1: testes que falham.**
  - `TestLerRunID` (tabela, puro): `""` → nil, ok; `"a=1"` → nil, ok; `"rid=0195f3c2-8a4b-7c1d-9e2f-3a4b5c6d7e8f"` → id,
    ok; o mesmo em maiúsculas → o mesmo id, ok; `"rid="`, `"rid=%7B0195…%7D"`, `"rid=urn:uuid:0195…"`,
    `"rid=0195f3c28a4b7c1d9e2f3a4b5c6d7e8f"`, `"rid=a&rid=b"`, rid válido repetido duas vezes, `"rid=%zz"`, `"rid=%00"`,
    `"rid=%27%3B%20DROP%20TABLE"` → false; `"x=%zz&rid=<válido>"` → id, ok (o par inválido alheio segue ignorado).
  - Integração:
    - `TestPingLogRecordsWithoutMovingTheDeadline`: `POST /ping/{token}/log` → 200 com o corpo fixo; prazo relido igual;
      `GET .../events` com `kind: "log"`.
    - `TestPingLogUnknownTokenIs404`: igual ao 404 de token desconhecido (corpo sem `trace_id` comparado).
    - `TestMalformedRunIDIs400BeforeTheToken`: `?rid=x` em `/ping/{token-válido}`, `/ping/{token-inexistente}`,
      `/ping/curto`, `/ping/{token}/7` e `/ping/{token}/log` → 400 com `field: "rid"`, corpos iguais entre si; nenhum evento
      gravado; corpo de 11 KiB com `rid` malformado → 400 (o `rid` vem antes do teto do corpo).
    - `TestPingIgnoresOtherQueryParameters`: `?foo=1&rid=<válido>` → 200 e o evento com o `run_id`.
    - `TestPingNeverLogsTheToken` e `TestPingChainServesOnlyPing` seguem verdes com a rota nova.
- [ ] **Step 2:** `go test -run 'LerRunID' ./internal/httpapi` e `go test -run 'PingLog|MalformedRunID|PingIgnoresOther' ./internal/httpapi`
  → FAIL.
- [ ] **Step 3: implementar.** `lerRunID` varre `strings.Split(rawQuery, "&")`, desescapa só a chave para achar `rid`,
  aceita no máximo um, exige `len == 36`, `uuid.Parse` e `strings.EqualFold(valor, id.String())`. `responder` recebe o
  `rid` já lido; o 400 sai com `campoInvalido(w, r, "rid", heartbeat.ReasonInvalid)` antes do `io.Copy`. A rota literal
  `/log` vence o curinga `{exit_code}` no `ServeMux`.
- [ ] **Step 4:** PASS. Sabotagens: trocar a varredura por `r.URL.Query().Get("rid")` → `"rid=%zz"` e o repetido passam
  como ausente; ler o `rid` depois do banco → o 404 aparece para o token inexistente.
- [ ] **Step 5:** commit `feat(httpapi): rota de log e run id no ping`.

### Task 6: API autenticada — `ping_resumes`, estado `grace`, eventos com `run_id` e `source`

**Files:**
- Modify: `internal/storage/queries/heartbeats.sql` (`InsertHeartbeat`, `UpdateHeartbeat` com `ping_resumes`); `make generate`
- Modify: `internal/heartbeat/service.go` (`Input.PingResumes bool`, `Patch.PingResumes *bool`,
  `Heartbeat.PingResumes bool`, `Clone` copia, se existir)
- Modify: `internal/httpapi/heartbeathandlers.go`
- Test: `internal/httpapi/heartbeats_integration_test.go`, `internal/httpapi/chaves_de_resposta_internal_test.go`,
  `internal/httpapi/patch_null_integration_test.go`

**Interfaces:**
- Produz: `heartbeatRequest.PingResumes *bool \`json:"ping_resumes"\``; `heartbeatPatchRequest.PingResumes optional[bool]`;
  `heartbeatResponse.PingResumes bool \`json:"ping_resumes"\``; `heartbeatEventResponse.RunID *string \`json:"run_id"\``
  e `Source string \`json:"source"\``.

- [ ] **Step 1: testes que falham.**
  - `TestHeartbeatResponseHasExactKeys`: a lista base ganha `ping_resumes`.
  - `TestHeartbeatEventHasExactKeys` (novo, integração ou na tabela de `chaves_de_resposta`): `at`, `exit_code`, `id`,
    `kind`, `run_id`, `runtime_ms`, `source`.
  - `TestPingResumesRoundTrip`: POST sem o campo → `false`; com `true` → `true`; PATCH `false` → `false` e prazo intacto;
    PATCH `null` → 400 `ping_resumes`; PATCH `"sim"` → 400; o heartbeat tem `true` depois de duplicar (se `Clone` existir).
  - `TestStateShowsGraceInsideTheWindow`: período 1 h, tolerância 10 min, sucesso em t0; relógio em t0+1h+1min → `state:
    "grace"`; em t0+1h+10min → `late`.
  - `TestEventListShowsRunIDAndSource`: ping com `rid` → `run_id` igual em minúsculas e `source: "ping"`; sem `rid` →
    `run_id: null`.
- [ ] **Step 2:** `go test -run 'ExactKeys|PingResumesRoundTrip|StateShowsGrace|EventListShowsRunID' ./internal/httpapi` → FAIL.
- [ ] **Step 3:** implementar; `recusouNulo` ganha `{"ping_resumes", req.PingResumes.Null}`; `Update` só grava
  `ping_resumes` quando veio, sem recalcular prazo.
- [ ] **Step 4:** PASS. Sabotagens: omitir `ping_resumes` quando `false` (`omitempty`) → o teste de chaves quebra; aceitar
  `null` no PATCH → o 400 quebra.
- [ ] **Step 5:** commit `feat(httpapi): retomada por ping, estado em tolerância e origem dos eventos`.

### Task 7: sinalizar agora

**Files:**
- Modify: `internal/heartbeat/service.go` (`PingNow`)
- Modify: `internal/httpapi/heartbeathandlers.go` (`PingNow`), `internal/httpapi/routes.go`
- Test: `internal/httpapi/heartbeat_acoes_integration_test.go` (do PR 2; criar se não existir),
  `internal/httpapi/heartbeats_integration_test.go` (IDOR)

**Interfaces:**
- Consome: `registrarSinal` (Task 4).
- Produz: `func (s *Service) PingNow(ctx context.Context, userID, id uuid.UUID) (Heartbeat, error)`; rota
  `POST /api/v1/heartbeats/{id}/ping-now` → 200 `heartbeatResponse` (sem `ping_url`).

- [ ] **Step 1: testes que falham.**
  - `TestPingNowIsMarkedAsPanel` (R2 da §10): → 200; último evento `kind: "success"`, `source: "panel"`, `run_id: null`;
    `last_success_at` = agora; prazo = agora + período + tolerância (literal); incidente de atraso aberto fecha no tique
    seguinte do avaliador.
  - `TestPingNowTwiceInTheWindowRecordsOnce`: dois POST em 5 s → dois 200, um evento.
  - `TestPingNowOnUnknownScheduleKeepsTheDeadline`: com o helper do PR 1 → 200, evento gravado, prazo literal intacto.
  - `TestPingNowIgnoresTheBody`: corpo `{"source":"ping","kind":"fail"}` → evento `success`/`panel`.
  - IDOR: `POST .../ping-now` entra no cenário de `TestAnotherUsersHeartbeatIsIndistinguishableFromMissing`; depois dele,
    `last_ping_at` do dono igual ao literal e nenhum evento novo.
  - `TestIDMalformadoE404EmTodaRotaComID` ganha a rota.
- [ ] **Step 2:** `go test -run 'PingNow|AnotherUsersHeartbeat|IDMalformado' ./internal/httpapi` → FAIL.
- [ ] **Step 3: implementar.** `PingNow` num `InUserTx`: `GetHeartbeatForUpdate` (404), `registrarSinal` com
  `sinalRecebido{sig: SignalSuccess, source: SourcePanel}` e `s.clk.Now()`, depois `view`.
- [ ] **Step 4:** PASS. Sabotagens: `source: SourcePing` → R2 quebra; trocar `GetHeartbeatForUpdate` por `GetHeartbeat` com
  outro `user_id` → o IDOR quebra. Comentário na trava: sem ela, um ping concorrente se perderia, como no PATCH.
- [ ] **Step 5:** commit `feat(heartbeat): sinalizar agora pelo painel`.

### Task 8: painel — esquemas, estado, log e "Avançado"

**Files:**
- Modify: `web/src/lib/api/schemas.ts` (`EstadoDoHeartbeat` com `'grace'`; `EventoDeHeartbeat` com `'log'`, `run_id`,
  `source`; `Heartbeat.ping_resumes`)
- Modify: `web/src/lib/api/cliente.ts` (`EntradaHeartbeat.ping_resumes?: boolean`)
- Modify: `web/src/features/heartbeats/estado.ts` (`grace: { exibido: 'degraded', texto: 'Em tolerância' }`)
- Modify: `web/src/features/heartbeats/PaginaHeartbeat.tsx` (`fraseDoEstado` para `grace`)
- Modify: `web/src/features/heartbeats/EventosDoHeartbeat.tsx` (tipo `log`, selo "pelo painel", execução)
- Modify: `web/src/features/heartbeats/esquema.ts`, `FormularioHeartbeat.tsx` (campo `pingResumes`, `<details>` "Avançado")
- Test: `estado.test.ts`, `detalhe.test.tsx`, `cadastro.test.tsx`, `esquema.test.ts`, `monitores.test.tsx`

**Interfaces:**
- Textos fixos: estado "Em tolerância"; frase `Sem sinal desde o prazo esperado; o alarme sai às ${dataHora(deadline_at)}.`;
  evento `log` → selo "Registro" (ícone `FileText`, tom neutro); `source = 'panel'` → "pelo painel"; `run_id` → `execução
  ${run_id.slice(0, 8)}`; checkbox "Retomar sozinho quando chegar um sucesso", com ajuda "Uma tarefa pausada volta a ser
  vigiada no primeiro sinal de sucesso."

- [ ] **Step 1: testes que falham.** `grace` aparece com o selo e a frase e não entra em "pedem atenção"; a lista de
  monitores filtra `grace` como degradado; o log mostra "Registro", "pelo painel" e a execução abreviada; o parse de um
  evento com `source: 'cron'` falha (enum estrito); o formulário envia `ping_resumes: true` só quando marcado e o PATCH só
  quando mudou; "Avançado" começa fechado em criação e aberto quando `ping_resumes` é `true` na edição.
- [ ] **Step 2:** `npx vitest run src/features/heartbeats src/features/painel` → FAIL.
- [ ] **Step 3:** implementar; `assertUnreachable` nos `switch` sobre estado e tipo.
- [ ] **Step 4:** PASS; `npx tsc --noEmit` e lint limpos. Sabotagem: tirar `grace` do `Record` → `tsc` falha (Record
  exaustivo); mapear `grace` para `down` → o teste de "pedem atenção" quebra.
- [ ] **Step 5:** commit `feat(painel): em tolerância, log e retomada por ping`.

### Task 9: painel — "Sinalizar agora" e exemplos de `rid` e `/log`

**Files:**
- Modify: `web/src/lib/api/cliente.ts` (`sinalizarAgora(id: string): Promise<Heartbeat>`)
- Modify: `web/src/features/heartbeats/PaginaHeartbeat.tsx`, `DialogoDaUrlDePing.tsx`
- Test: `detalhe.test.tsx`, `cadastro.test.tsx`

**Interfaces:**
- Textos fixos: botão "Sinalizar agora" (secundário); aviso "Sinal de sucesso registrado."; exemplos novos no diálogo:
  `RID=$(uuidgen); curl … "URL/start?rid=$RID"; tarefa.sh; curl … "URL/$??rid=$RID"` (aspas: `?` sem aspas é glob no
  zsh) com o título "Com run id (execuções sobrepostas)", e `curl … --data-binary @- "URL/log"` com o título "Registrar uma linha, sem mexer no prazo". (O corpo é
  descartado enquanto D14 não chega; o exemplo não promete guardá-lo: "O conteúdo não é guardado".)

- [ ] **Step 1: testes que falham.** O botão chama a rota, invalida o heartbeat e os eventos, avisa; fica ocupado sem
  duplo clique; erro mostra o aviso persistente; os exemplos novos aparecem com a URL literal do teste.
- [ ] **Step 2:** `npx vitest run src/features/heartbeats` → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** PASS. Sabotagem: não invalidar os eventos → o teste do log depois do sinal quebra.
- [ ] **Step 5:** commit `feat(painel): sinalizar agora e exemplos de run id e log`.

### Task 10: ensaio do piso, documentação e verificação final

**Files:**
- Modify: `deploy/ci/ensaiar-piso.sh` (do PR 2; criar com a mesma interface se este PR entrar antes)
- Modify: `README.md` (bullet "Heartbeats": `/log`, `rid`, em tolerância, retomada por ping, sinalizar agora)

- [ ] Cenário do PR 3 no script: com o branch, pings com `?rid=`, um `/log`, uma tarefa com `ping_resumes: true` pausada,
  e um sinalizar agora. Com o piso: `GET` da lista e dos eventos → 200, evento `log` presente; `POST` na URL → 200;
  `PATCH` de `name` → 200. Ver falhar com a tag anterior ao PR 1; passar com a tag publicada do PR 1. Saída no corpo do PR.
- [ ] `go build ./... && go vet ./... && make test` (sob flock, bateria acima de 50%; senão, só o CI).
- [ ] `sh deploy/ci/checar-nome.sh`; `npx tsc --noEmit`, lint e `npx vitest run` no `web/`.
- [ ] Revisão do branch com `code-review` e `security-review`. PR draft com: número final da migration, as sabotagens, a
  saída do ensaio, e a nota de que D6, D10 e D14 ficam para o PR 11. Merge com o PR 1 publicado; conferir o run do
  `Coolify release` deste SHA antes de anunciar.
