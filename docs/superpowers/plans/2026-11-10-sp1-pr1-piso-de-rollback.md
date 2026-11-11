# SP1, PR 1: piso de rollback — plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** o binário deste PR lê, lista, pinga e avalia sem quebrar as linhas que os PRs 2 a 12 da SP1 vão gravar
(período e tolerância acima das faixas atuais, linha sem período e sem cron, evento de tipo novo, notificação sem
`incident_id`), sem ele mesmo gravar nada novo.

**Arquitetura:** `ScheduleFromRow` deixa de aplicar faixas de cadastro e ganha uma forma explícita "desconhecida". O
sinal, o avaliador, a API e o painel tratam essa forma sem tocar no prazo. A reivindicação da outbox ignora linha com
`incident_id` nulo. Nenhuma migration.

**Stack:** Go 1.26.10, pgx v5, sqlc, Postgres 17/18 via testcontainers; React + Zod + Vitest no painel.

**Spec:** [`docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`](../specs/2026-11-09-sp1-heartbeats-tls-conteudo.md),
seções 2, 5.1 ("Leitura compatível" e "Agendamento desconhecido"), 8 (último parágrafo), 9 (linhas Rollback, Outbox no
piso, Agendamento desconhecido) e 12 ("Piso de rollback", PR 1). Decisões do dono por delegação, 09/11/2026: D6 = B,
D10 = B (`start + grace` opcional), D14 = B, estágios 14/7/3, SP1 antes da SP2. Nenhuma delas muda este PR.

## Restrições globais

- Nenhuma migration, nenhuma coluna nova, nenhum valor novo gravado. O binário deste PR só **tolera** o que virá.
- Cadastro e PATCH seguem com as faixas atuais: `MaxPeriod = 31 dias`, `MaxGrace = 7 dias`, descrição até 1000.
- Linha desconhecida: período e cron ambos nulos. Cron sem fuso continua erro (o CHECK atual impede).
- Ping em linha desconhecida: 200 fixo, grava evento e `last_*`, **nunca altera `deadline_at`**, em nenhum sinal.
- PATCH em linha desconhecida: 409 com o código novo `schedule_unsupported`, sem gravar. DELETE e rotação de token seguem.
- Saída da API: `schedule: {"kind": "unknown"}`, sem outros campos.
- Painel: "Agendamento não suportado nesta versão", sem edição.
- `go build ./... && go vet ./... && make test` antes de dizer que funciona; teste novo só vale visto falhar com a
  sabotagem indicada, registrada no corpo do PR. Valor esperado é literal.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%,
  nada local, só o CI.

## Foco de revisão

1. Linha desconhecida com incidente `heartbeat_late` já aberto (aberto antes do rollback): o fechamento não pode exigir
   prazo futuro, senão o incidente nunca fecha. Teste na Tarefa 4.
2. Linha desconhecida pausada e retomada pelo PATCH: o PATCH dá 409, então a retomada não é possível nessa versão;
   pausar também não. Aceito pela spec; o teste da Tarefa 5 fixa o 409 para `enabled` sozinho também.
3. Período de 365 dias na leitura com cron nulo: `Next` soma o período normalmente; o prazo calculado num sucesso
   cabe em `timestamptz`. Teste na Tarefa 1.
4. Página de `Service.List` com uma linha desconhecida no meio de linhas normais: todas aparecem. Teste na Tarefa 3.
5. Lote da outbox com 1 linha sem incidente e 1 com incidente no mesmo tique: a segunda sai, a primeira fica intacta.
   Teste na Tarefa 6.

---

### Tarefa 1: agendamento desconhecido e leitura sem faixa

**Arquivos:**
- Modificar: `internal/heartbeat/schedule.go`
- Teste: `internal/heartbeat/heartbeat_test.go` (testes puros já ficam aqui)

**Interfaces:**
- Produz: `func (s Schedule) IsKnown() bool`; `ScheduleFromRow(periodSec *int32, cronExpr, timezone *string) (Schedule, error)`
  com o comportamento novo. `Schedule{}` continua inválido: a forma desconhecida é um campo explícito
  (`unknown bool`), não o valor zero.

- [ ] **Passo 1: testes que falham**
  - `TestScheduleFromRowAcceptsPeriodAboveRegistrationRange`: `period = 31536000` (365 dias) → sem erro,
    `IsKnown() == true`, `Period() == 365*24*time.Hour`; `Next(t0)` com `t0 = 2026-11-10T00:00:00Z` é
    `2027-11-10T00:00:00Z`.
  - `TestScheduleFromRowRejectsNonPositivePeriod`: `0` e `-60` → `ValidationError` em `period_sec`.
  - `TestScheduleFromRowWithoutPeriodOrCronIsUnknown`: período nil, cron nil, fuso nil → sem erro, `IsKnown() == false`,
    `IsCron() == false`; também com fuso não nil e cron nil.
  - `TestScheduleFromRowCronWithoutTimezoneStillFails`: cron "0 3 * * *", fuso nil → erro, como hoje.
  - `TestNewPeriodStillEnforcesRegistrationRange`: `NewPeriod(31*24h + 1s)` → `out_of_range` (protege o cadastro).
- [ ] **Passo 2:** `go test -run 'ScheduleFromRow|NewPeriodStill' ./internal/heartbeat` → FAIL (365 dias dá erro; nil/nil dá `required`).
- [ ] **Passo 3:** implementar. `ScheduleFromRow`: período não nil → `period > 0` ou `ValidationError{period_sec, out_of_range}`,
  sem teto; período e cron nil → `Schedule{unknown: true}`; cron sem fuso → erro de hoje. `Next` numa forma desconhecida
  é bug de quem chama: `panic` com mensagem explícita (nenhum caminho a alcança depois das tarefas 2 a 5; o teste
  `TestNextOnUnknownSchedulePanics` fixa isso). `Equal` compara também `unknown`.
- [ ] **Passo 4:** mesmo comando → PASS. Sabotagem: reaplicar `NewPeriod` dentro de `ScheduleFromRow` → o teste de 365 dias quebra.
- [ ] **Passo 5:** commit `feat(heartbeat): agendamento desconhecido e leitura sem faixa de cadastro`.

### Tarefa 2: sinal não mexe no prazo da linha desconhecida

**Arquivos:** modificar `internal/heartbeat/heartbeat.go` (`State.Apply`); teste em `heartbeat_test.go`.

**Interfaces:** consome `IsKnown()`. `Apply` mantém a assinatura.

- [ ] **Passo 1:** `TestApplyOnUnknownScheduleKeepsDeadline`, tabela com `SignalStart`, `SignalSuccess`, `SignalFail` e
  sucesso depois de início: `DeadlineAt` sai igual ao literal de entrada (`2026-11-10T12:00:00Z`); `LastPingAt`,
  `LastSuccessAt` (no sucesso), `FailingSince` (na falha) e o evento mudam como numa linha conhecida; sucesso limpa
  `FailingSince`.
- [ ] **Passo 2:** `go test -run ApplyOnUnknown ./internal/heartbeat` → FAIL (panic do Next).
- [ ] **Passo 3:** no ramo `SignalSuccess`, só recalcular `DeadlineAt` quando `sch.IsKnown()`.
- [ ] **Passo 4:** PASS. Sabotagem: tirar a condição → panic/FAIL.
- [ ] **Passo 5:** commit `feat(heartbeat): sinal em agendamento desconhecido preserva o prazo`.

### Tarefa 3: leitura, estado e listagem

**Arquivos:** modificar `internal/heartbeat/service.go` (`toHeartbeat`, `statusOf`); teste novo
`internal/heartbeat/compat_rollback_test.go` (integração, `novoAmbiente`).

**Interfaces:** `statusOf` ganha o parâmetro `knownSchedule bool`: falso ignora o prazo (nunca `late` por prazo; incidente
aberto e falha seguem valendo).

- [ ] **Passo 1:** helper de teste `semChecksDeAgendamento(t, pool)` que roda, no banco de teste,
  `ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_one_schedule, DROP CONSTRAINT heartbeats_period_sec_check,
  DROP CONSTRAINT heartbeats_grace_sec_check` e o CHECK da descrição da 00022 (conferir os nomes com
  `SELECT conname FROM pg_constraint WHERE conrelid = 'heartbeats'::regclass` e fixá-los no helper). Testes:
  - `TestListToleratesRowsFromLaterVersions`: 3 linhas do dono — normal de 1 h; período 31536000, tolerância 31536000
    e descrição de 10000 caracteres; período e cron nulos. `Service.List` devolve as 3, sem erro, na ordem de id.
  - `TestUnknownScheduleStatusIgnoresDeadline`: linha desconhecida com prazo vencido e sem incidente → status `up`
    depois de um sucesso (ou `waiting` sem execução); com `failing_since` → `failing`; pausada → `paused`.
- [ ] **Passo 2:** `go test -run 'ListTolerates|UnknownScheduleStatus' ./internal/heartbeat` (Docker, sob flock) → FAIL.
- [ ] **Passo 3:** `toHeartbeat` passa `sch.IsKnown()` a `statusOf`.
- [ ] **Passo 4:** PASS. Sabotagem: devolver o erro antigo em `ScheduleFromRow` → `List` falha.
- [ ] **Passo 5:** commit `feat(heartbeat): lista e estado toleram linha de versão posterior`.

### Tarefa 4: avaliador ignora o prazo da linha desconhecida

**Arquivos:** modificar `internal/storage/queries/heartbeats.sql` (`ClaimHeartbeatsToOpen`, `ClaimHeartbeatsToClose`),
regenerar `internal/storage/db/heartbeats.sql.go` com `make generate`; teste em `compat_rollback_test.go`.

- [ ] **Passo 1:** testes, com o helper da Tarefa 3 e o avaliador real (o mesmo ponto de entrada que os testes atuais do
  avaliador usam):
  - `TestEvaluatorDoesNotOpenLateForUnknownSchedule`: linha desconhecida, prazo 2 h no passado, sem falha → nenhum incidente.
  - `TestEvaluatorStillOpensFailedForUnknownSchedule`: com `failing_since` → abre `heartbeat_failed`; um sucesso
    seguinte (ping) e um tique → incidente resolvido, mesmo com o prazo no passado.
  - `TestEvaluatorClosesLateIncidentOfUnknownScheduleAfterSuccess`: incidente `heartbeat_late` aberto antes, prazo no
    passado, sucesso depois do início do incidente → fecha.
- [ ] **Passo 2:** FAIL.
- [ ] **Passo 3:** `ToOpen`: trocar a condição do prazo por
  `(h.failing_since IS NOT NULL OR (h.deadline_at <= now AND (h.period_sec IS NOT NULL OR h.cron_expr IS NOT NULL)))`.
  `ToClose`: `AND (h.deadline_at > now OR (h.period_sec IS NULL AND h.cron_expr IS NULL))`. Atualizar os comentários.
- [ ] **Passo 4:** PASS; os testes atuais do avaliador continuam verdes. Sabotagem: tirar cada uma das duas condições → o
  teste correspondente quebra.
- [ ] **Passo 5:** commit `feat(heartbeat): avaliador ignora prazo de agendamento desconhecido`.

### Tarefa 5: API — saída `unknown`, PATCH 409, ping 200

**Arquivos:** `internal/heartbeat/service.go` (`Update`/`updateParams` devolvem `ErrScheduleUnsupported` antes de
qualquer escrita); `internal/heartbeat/heartbeat.go` (declarar o erro); `internal/apierr/errors.go`
(`CodeScheduleUnsupported ErrorCode = "schedule_unsupported"`, status 409, mensagem em português no padrão dos
vizinhos); `internal/httpapi/heartbeathandlers.go` (`toScheduleDTO` com `scheduleKindUnknown = "unknown"`; `falhou`
mapeia o erro). Teste: `internal/httpapi/heartbeat_compat_integration_test.go`.

- [ ] **Passo 1:** testes HTTP (helper que tira os CHECKs, como na Tarefa 3, e cria a linha pelo SQL):
  - `TestUnknownScheduleIsListedAsUnknown`: GET da lista e do detalhe → 200, `schedule` com chaves exatamente `{"kind"}`
    e valor `"unknown"`.
  - `TestPingOnUnknownScheduleKeepsDeadline`: POST no ping, `/start`, `/fail`, `/0` → 200; `deadline_at` relido do
    banco igual ao literal gravado; `last_ping_at` mudou; 4 eventos.
  - `TestPatchOnUnknownScheduleConflicts`: PATCH com `name`, com `enabled:false` e com `schedule` → 409, corpo com
    `"code":"schedule_unsupported"`, linha intacta (nome, `enabled`, prazo).
  - `TestDeleteAndRotateStillWorkOnUnknownSchedule`: DELETE → 204; rotação de token em outra linha desconhecida → 200.
  - `TestLongPeriodRowPingsAndPatchOfOtherFieldsWorks`: linha de 365 dias: ping de sucesso → prazo = agora + 365 dias +
    tolerância (literal); PATCH só de `name` → 200 e período intacto; PATCH reenviando `period_sec: 31536000` → 400
    `out_of_range`, sem gravar.
- [ ] **Passo 2:** FAIL.
- [ ] **Passo 3:** implementar. `ErrScheduleUnsupported` checado em `Update` logo depois de travar a linha.
- [ ] **Passo 4:** PASS. Sabotagens: tirar o 409 (PATCH grava e o prazo vira `agora + tolerância`); devolver `period`
  com `period_sec` 0 em `toScheduleDTO`.
- [ ] **Passo 5:** commit `feat(httpapi): heartbeat de versão posterior aparece como desconhecido`.

### Tarefa 6: outbox ignora notificação sem incidente

**Arquivos:** `internal/storage/queries/notifications.sql` (CTE `lote` de `ClaimDueNotifications`: `AND incident_id IS
NOT NULL`, com comentário apontando a SP1, PR 7, que remove o filtro); `make generate`. Teste: integração no pacote que
já testa `ClaimDueNotifications`/dispatcher (`internal/notify`).

- [ ] **Passo 1:** `TestClaimSkipsNotificationWithoutIncident`: no banco de teste,
  `ALTER TABLE notifications ALTER COLUMN incident_id DROP NOT NULL`; uma linha pendente sem incidente
  (`attempts = 0`, `next_attempt_at` literal no passado) e uma com incidente, no mesmo tique. Depois da reivindicação: só
  a segunda volta; a primeira continua `pending`, `attempts = 0` e o mesmo `next_attempt_at`.
- [ ] **Passo 2:** FAIL (a linha sem incidente é contada e some no `JOIN`).
- [ ] **Passo 3:** o filtro.
- [ ] **Passo 4:** PASS. Sabotagem: tirar o filtro → `attempts` vira 1.
- [ ] **Passo 5:** commit `fix(notify): reivindicação ignora aviso sem incidente`.

### Tarefa 7: painel tolera `unknown` e tipo de evento novo

**Arquivos:** `web/src/lib/api/schemas.ts` (`Agendamento` ganha `z.object({ kind: z.literal('unknown') })`;
`EventoDeHeartbeat.kind` passa a aceitar string desconhecida mapeada para `'unknown'`, sem quebrar a lista);
`FormularioHeartbeat.tsx`, `PaginaHeartbeat.tsx` e a lista de monitores (switch exaustivo sobre `kind`); testes em
`detalhe.test.tsx` e `monitores.test.tsx`.

- [ ] **Passo 1:** testes Vitest: o detalhe de um heartbeat com `schedule: {kind:'unknown'}` mostra "Agendamento não
  suportado nesta versão" e não mostra o botão Editar; a lista mostra a linha; um evento com `kind: 'log'` aparece como
  "Outro sinal" e não derruba o log; o parse de `{kind:'period'}` sem `period_sec` continua falhando.
- [ ] **Passo 2:** `npx vitest run src/features/heartbeats` (sob flock) → FAIL.
- [ ] **Passo 3:** implementar; `assertUnreachable` nos switches.
- [ ] **Passo 4:** PASS; `npx tsc --noEmit` e lint limpos. Sabotagem: tirar o ramo `unknown` → falha de parse.
- [ ] **Passo 5:** commit `feat(painel): heartbeat de versão posterior sem edição`.

### Tarefa 8: verificação final

- [ ] `go build ./... && go vet ./... && make test` (sob flock, com bateria acima de 50%; senão, só o CI).
- [ ] `sh deploy/ci/checar-nome.sh`.
- [ ] Revisão do branch inteiro com `code-review` e `security-review`; PR draft com as sabotagens no corpo. Merge só no
  calendário (a partir de 11:00 de terça, uma release por vez) e **sozinho**: PR 2 e PR 12 só depois deste publicado e
  conferido por SHA.
