# SP1, PR 2: faixas novas, duplicar e limpar eventos — plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** heartbeats aceitam período de 60 s a 365 dias, tolerância de 30 s a 365 dias e descrição de até 10.000
caracteres; o dono duplica uma tarefa e limpa o log dela; o diálogo da URL ganha exemplos de uso. O binário do PR 1,
publicado antes, continua lendo e pingando tudo o que este PR grava.

**Arquitetura:** a migration "+0 `heartbeat_limits`" só alarga três CHECKs de `heartbeats`, com descida que converte em
vez de apagar. As faixas do domínio sobem junto. `Service.Clone` reaproveita a inserção de `Create` (extraída antes, sem
mudar comportamento). `Service.ClearEvents` apaga os eventos e grava `audit_log` na mesma transação. Painel: limites,
duas ações no detalhe e exemplos no diálogo.

**Stack:** Go 1.26.10, pgx v5, sqlc, goose, Postgres 18 via testcontainers; React + Zod + Vitest no painel.

**Spec:** `/Users/01bipo/Documents/Claude/stillon-continuidade/repo/.claude/worktrees/sp1-piso/docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`,
seções 2, 5.1 ("Faixas"), 6 (linha +0 e regra do `set_config`), 7.2 (`clone`, `events/clear`), 8 (diálogo da URL e ações
no detalhe), 9 (Faixas, Rollback, Descida, Acesso, Entrada, Saída, Painel), 10 (R1) e 12 (PR 2, "Piso de rollback").
Plano anterior: `docs/superpowers/plans/2026-11-10-sp1-pr1-piso-de-rollback.md` (mesma worktree).

**Decisões do dono por delegação:** D6 = B (GET opcional por tarefa, só POST como padrão), D10 = B (`start + grace`
opcional, booleano `start_uses_grace`), D14 = B (corpo opt-in por tarefa, 10 KiB). **Nenhuma entra aqui:** são o PR 11.

## Rulings deste plano

- **Ping-now sai do PR 2 e vai para o PR 3.** A §12 põe "sinalizar" no PR 2, mas `source` só nasce na migration +1 (PR 3),
  com `DEFAULT 'ping'`. Um sinal manual gravado aqui ficaria para sempre marcado como `ping` ao aplicar a +1, e a mitigação
  R2 da §10 (`TestPingNowIsMarkedAsPanel`) seria falsa para esses eventos. O PR 3 já mexe no caminho de escrita do evento
  (`run_id`, `source`), então o ping-now custa menos lá, e este PR fica menor.
- **Duplicar e limpar ignoram o corpo**, como `token/rotate` hoje: não há `decodeJSON`, e nenhum campo do corpo
  (`status`, `user_id`, `name`) muda o resultado. Um teste fixa isso.
- **O que a cópia leva:** nome com o sufixo ` (cópia)`, cortando o original para caber em 100 caracteres; descrição; tags;
  agendamento; tolerância; `enabled`. Não leva token, eventos, `last_*`, falha, incidentes nem `resumed_at`. O prazo nasce
  de `FirstDeadline(agendamento, tolerância, agora)`, como na criação.
- **Auditoria do limpar:** `action = "heartbeat.events_cleared"`, `entity = "heartbeat"`, `entity_id` = id, sem `ip_hash`
  (dado que não é necessário não entra). Grava mesmo com zero eventos: registra o pedido, e limpar o que já está vazio é
  sucesso (204).
- **Descida da +0 também recoloca o prazo** das linhas cujo período foi reduzido:
  `deadline_at = LEAST(deadline_at, now() + make_interval(secs => 2678400 + LEAST(grace_sec, 604800)))`. Sem isso, o binário
  antigo ficaria até um ano sem avisar sobre uma tarefa de 365 dias rebaixada a 31. Linhas cron com tolerância reduzida
  mantêm o prazo (só atrasam o aviso, nunca o antecipam).
- **Sem `NOT VALID`:** alargar CHECK numa tabela de no máximo 100 linhas por conta; a revalidação é desprezível.
- **Exemplos do diálogo (B19):** acrescenta crontab, `wget`, Python (`urllib`) e PowerShell (`Invoke-RestMethod`), todos
  com POST e só as rotas que existem hoje. Exemplos de `rid` e `/log` entram no PR 3; aba de slug, no PR 8.
- **Duplicar no painel:** o diálogo da URL abre na página da tarefa de origem; "Já guardei" navega para a cópia.
- **Ensaio do piso:** teste de boot no CI (`goose` só com as migrations do piso sobre o schema novo aplica zero e não
  falha) mais o script `deploy/ci/ensaiar-piso.sh`, rodado à mão com a imagem publicada do PR 1, resultado no corpo do PR.

## Restrições globais

- Migration: `internal/storage/migrations/000NN_heartbeat_limits.sql`, onde `000NN` é o **próximo número livre na hora do
  merge** (ex.: `00023`; se o PR 3 entrar antes, este passa a `00024`). A `00015` é vaga para sempre. Testes acham a versão
  pelo nome do arquivo, nunca por número fixo.
- Faixas, nos dois lados (validação e CHECK): `period_sec` 60..31536000; `grace_sec` 30..31536000; `description` até 10000
  caracteres (`char_length`, não bytes). `MinGrace` fica em 30 s (`EarlySignalMargin` depende dele).
- Descida com dado: `SELECT set_config('app.scope', 'prober', true)` antes de qualquer `UPDATE`; o escopo do sondador não
  tem DELETE em `heartbeats`, então converte.
- Duplicar: `POST /api/v1/heartbeats/{id}/clone` → 201 com `ping_url` uma vez; conta no limite de 100
  (`heartbeat_limit_reached`, 409); agendamento desconhecido (PR 1) → 409 `schedule_unsupported`, sem gravar.
- Limpar: `POST /api/v1/heartbeats/{id}/events/clear` → 204 sem corpo; não mexe em `last_*`, prazo nem incidentes.
- Alheio ou inexistente: 404 igual, corpo sem dado do dono, recurso do dono intacto.
- Nada de D6, D10, D14, `source`, `rid`, `/log` ou ping-now neste PR.
- Merge só depois do PR 1 publicado e conferido por SHA (`gh run list --workflow coolify.yml`); merge é deploy.
- `go build ./... && go vet ./... && make test` antes de dizer que funciona; teste novo só vale visto falhar com a
  sabotagem indicada, registrada no corpo do PR. Valor esperado é literal.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%, só o CI.

## Foco de revisão

1. Descrição de 10.000 caracteres de 4 bytes (`"😀"`): 40 KB cabem no teto de 1 MiB da API e o CHECK conta caracteres;
   10.001 é recusado com `description`, nunca 500. Teste na Task 2.
2. Duplicar com 99 tarefas, dois pedidos simultâneos: um 201 e um 409, nunca 101 tarefas. Teste na Task 3.
3. Duplicar uma tarefa de nome com 100 caracteres (e com 99 caracteres multibyte): o nome da cópia tem exatamente 100
   caracteres e termina em ` (cópia)`. Teste na Task 3.
4. Limpar enquanto a tarefa pinga: um ping depois do limpar grava 1 evento, e `last_ping_at` e o prazo seguem os do ping.
   Teste na Task 4.
5. Descida da +0 com tarefa de 365 dias e prazo a 300 dias: depois da descida, o prazo cai para no máximo agora + 31 dias +
   tolerância rebaixada, e o CHECK antigo vale. Teste na Task 1.

---

### Task 1: migration +0 `heartbeat_limits`, com descida e boot do piso

**Files:**
- Create: `internal/storage/migrations/000NN_heartbeat_limits.sql`
- Create: `internal/storage/heartbeat_limits_test.go`
- Modify: `internal/storage/helpers_test.go` (helper de versão por nome)
- Modify: `internal/storage/heartbeat_rls_test.go:336-355` (casos "período longo" e "tolerância longa" passam às bordas novas)

**Interfaces:**
- Produz: CHECKs `heartbeats_period_sec_check` (60..31536000), `heartbeats_grace_sec_check` (30..31536000),
  `heartbeats_description_check` (`char_length(description) <= 10000`), com os mesmos nomes dos atuais.
- Produz (teste): `versaoDaMigration(t *testing.T, sufixo string) int64` — acha `migrations/*_<sufixo>.sql` e devolve o
  número; falha se houver zero ou mais de um. O PR 3 reutiliza.

- [ ] **Step 1: testes que falham** (em `heartbeat_limits_test.go`, com `testdb`):
  - `TestHeartbeatLimitsChecksAcceptTheNewEdges`: INSERT direto, no escopo do dono, com `period_sec = 31536000`,
    `grace_sec = 31536000`, `description = repeat('é', 10000)` → sem erro; cada um com +1 (`31536001`, `31536001`,
    10001 caracteres) → `23514`; `period_sec = 59` e `grace_sec = 29` continuam `23514`.
  - `TestHeartbeatLimitsDownConvertsAndRestoresTheOldChecks`: `EmptyDatabase` → `Migrate`; uma linha de período 31536000,
    tolerância 31536000, descrição de 10000 caracteres e `deadline_at = 2027-09-06T00:00:00Z`; uma linha cron com
    tolerância 1209600 e `deadline_at = 2026-11-20T00:00:00Z`; uma linha normal (3600/300). `MigrateDownTo(versão - 1)`:
    a primeira fica com 2678400 / 604800 / 1000 caracteres e `deadline_at <= now() + 3283200 s` (31 d + 7 d); a cron fica
    com 604800 e o prazo literal intacto; a normal, intacta. INSERT com 2678401 → `23514`. `MigrateUpTo(versão)` de novo
    → sem erro.
  - `TestFloorBinaryBootsOnHeartbeatLimits`: no molde de `TestOlderBinaryBootsOnNewerSchema`: `Migrate` e depois
    `goose.NewProvider` com `migrationsAte(t, "<nome do arquivo +0>")` → `Up` aplica 0 e não falha.
- [ ] **Step 2:** `go test -run 'HeartbeatLimits|FloorBinaryBoots' ./internal/storage` → FAIL (arquivo inexistente;
  `versaoDaMigration` falha).
- [ ] **Step 3: escrever a migration.** Up: `DROP CONSTRAINT` + `ADD CONSTRAINT` com o mesmo nome, nos três. Down, nesta
  ordem: `set_config('app.scope', 'prober', true)`; um `UPDATE` só das linhas fora da faixa antiga com `LEAST`, `left` e o
  prazo do Ruling (só onde `period_sec > 2678400`); depois os CHECKs antigos. Comentário no topo: por que a descida
  converte e não apaga (§6), e que o binário do PR 1 lê essas linhas.
- [ ] **Step 4:** mesmo comando → PASS. Ajustar `heartbeat_rls_test.go` (`longo := int32(31536001)`, tolerância
  `31536001`). Sabotagens: tirar o `set_config` do Down → o `UPDATE` não enxerga linhas e o `ADD CONSTRAINT` falha; tirar o
  `LEAST` do prazo → a asserção do prazo quebra; voltar o teto de `grace_sec` a 604800 no Up → o caso de 31536000 quebra.
- [ ] **Step 5:** commit `feat(storage): faixas de heartbeat até 365 dias e descrição até 10000`.

### Task 2: faixas no domínio, na API e no texto de quem lê a linha

**Files:**
- Modify: `internal/heartbeat/heartbeat.go` (constantes e comentários)
- Modify: `internal/heartbeat/service.go:522` (comentário de `seconds`)
- Modify: `internal/httpapi/heartbeathandlers.go:138` (comentário do `#nosec`)
- Test: `internal/heartbeat/heartbeat_test.go` (`TestPeriodRange`, `TestGraceRange`, `TestValidateDescription`),
  `internal/httpapi/heartbeats_integration_test.go` (`TestHeartbeatValidationBoundaries`)

**Interfaces:**
- Consome: CHECKs da Task 1.
- Produz: `MaxPeriod = 365 * 24 * time.Hour`, `MaxGrace = 365 * 24 * time.Hour`, `MaxDescriptionLength = 10000`.

- [ ] **Step 1: testes que falham.** Nas tabelas de `TestPeriodRange` e `TestGraceRange`, os casos literais
  `365*24*time.Hour` (aceito) e `365*24*time.Hour + time.Second` (recusado). Em `TestHeartbeatValidationBoundaries`, trocar
  as bordas para `31536000`/`31536001` (período e tolerância) e acrescentar: descrição `strings.Repeat("é", 10000)` → 201;
  10001 → 400 `description`; `strings.Repeat("😀", 10000)` → 201; descrição com `\u0000` → 400 `description[0]` (já existe);
  PATCH de `period_sec: 31536000` numa tarefa existente → 200 e `deadline_at` = agora + 31536000 s + tolerância (literal
  com o relógio do teste).
- [ ] **Step 2:** `go test -run 'PeriodRange|GraceRange|ValidateDescription' ./internal/heartbeat` e
  `go test -run 'HeartbeatValidationBoundaries' ./internal/httpapi` → FAIL.
- [ ] **Step 3:** trocar as constantes; atualizar os comentários que dizem "31 dias" e "migration 00022" (apontar a +0).
- [ ] **Step 4:** PASS. Sabotagem: deixar `MaxGrace` em 7 dias → "tolerância no teto" quebra.
- [ ] **Step 5:** commit `feat(heartbeat): período e tolerância até 365 dias, descrição até 10000`.

### Task 3: duplicar

**Files:**
- Modify: `internal/heartbeat/service.go` (extrair a inserção; `Clone`; `cloneName`)
- Modify: `internal/heartbeat/export_test.go` (`var CloneName = cloneName`)
- Modify: `internal/httpapi/heartbeathandlers.go` (`Clone`), `internal/httpapi/routes.go` (rota)
- Test: `internal/heartbeat/heartbeat_test.go` (puro), `internal/heartbeat/service_test.go` (concorrência),
  `internal/httpapi/heartbeat_acoes_integration_test.go` (novo)

**Interfaces:**
- Consome: `Schedule.IsKnown()` e `ErrScheduleUnsupported` (PR 1); `ScheduleFromRow`; `FirstDeadline`.
- Produz: `const CloneSuffix = " (cópia)"`; `func cloneName(name string) string`;
  `func (s *Service) Clone(ctx context.Context, userID, id uuid.UUID) (Heartbeat, string, error)` — erros `ErrNotFound`,
  `ErrLimitReached`, `ErrScheduleUnsupported`; handler `func (h *HeartbeatHandlers) Clone(w http.ResponseWriter, r *http.Request)`;
  rota `POST /api/v1/heartbeats/{id}/clone`.

- [ ] **Step 1: refatorar antes (sem mudar comportamento).** Extract Function de `Create`:
  `func (s *Service) inserir(ctx context.Context, q *db.Queries, userID uuid.UUID, params db.InsertHeartbeatParams, tags []string) (Heartbeat, error)`
  com trava, contagem, `depoisDeContar`, limite, `InsertHeartbeat`, `InsertHeartbeatTags` e `responseOf`. `Create` passa a
  chamá-la dentro do `InUserTx`. `make test` do pacote verde (inclui `TestHeartbeatLimitHoldsUnderConcurrency`). Commit
  `refactor(heartbeat): extrai a inserção com limite`.
- [ ] **Step 2: testes que falham.**
  - Puro, `TestCloneName`: `"backup"` → `"backup (cópia)"`; 100 × `"a"` → 92 × `"a"` + `" (cópia)"` (100 caracteres);
    99 × `"é"` → 92 × `"é"` + sufixo; nome que termina em espaço depois do corte (`strings.Repeat("a", 91) + " b..."`) →
    sem espaço duplo antes do sufixo.
  - Integração (`heartbeat_acoes_integration_test.go`):
    - `TestCloneCopiesTheConfigurationAndNotTheState`: original com nome "backup noturno", descrição, tags `prod, backup`,
      período 31536000, tolerância 600, pausado, com 3 eventos, falha e incidente aberto. POST clone → 201;
      `ping_url` presente; o token da URL pinga a cópia e não o original; a cópia tem nome "backup noturno (cópia)",
      mesma descrição, tags, agendamento, tolerância e `enabled:false`; `last_ping_at`, `last_success_at`,
      `last_start_at`, `open_incident_id` nulos; `deadline_at` = agora + 31536000 s + 600 s (literal); 0 eventos.
    - `TestCloneResponseHasExactKeys`: chaves da resposta = as de `TestHeartbeatResponseHasExactKeys` + `ping_url`; GET
      seguinte da cópia não tem `ping_url`.
    - `TestCloneCountsTowardsTheLimit`: com 100 tarefas → 409 `heartbeat_limit_reached`, contagem segue 100.
    - `TestCloneOfUnknownScheduleConflicts`: com o helper de CHECKs do PR 1, linha sem período e sem cron → 409
      `schedule_unsupported`, contagem inalterada.
    - `TestCloneIgnoresTheBody`: corpo `{"name":"x","status":"up","user_id":"<outro>","enabled":true}` → 201 com o nome
      derivado do original, `enabled` do original, dono = quem chamou.
  - `service_test.go`, `TestCloneLimitHoldsUnderConcurrency`: 99 tarefas, dois `Clone` encontrados no gancho
    `depoisDeContar` → um ok e um `ErrLimitReached`; `CountHeartbeats == 100`.
- [ ] **Step 3:** `go test -run 'CloneName|CloneLimit' ./internal/heartbeat` e `go test -run 'Clone' ./internal/httpapi`
  → FAIL.
- [ ] **Step 4: implementar.** `Clone`: num só `InUserTx`, `GetHeartbeat` (404 via `notFound`), `ScheduleFromRow`;
  `!IsKnown()` → `ErrScheduleUnsupported`; `ListHeartbeatTags`; token novo; `inserir`. Handler no molde de `Create`, sem
  decodificar corpo, `falhou` para os erros. `cloneName` corta por runa e faz `TrimRight` de espaço antes do sufixo.
- [ ] **Step 5:** PASS. Sabotagens: copiar `LastPingAt` → `...NotTheState` quebra; contar antes de travar (mover a trava
  para depois de `CountHeartbeats`) → o teste de concorrência quebra; cortar por byte → o caso `"é"` quebra.
- [ ] **Step 6:** commit `feat(heartbeat): duplicar tarefa`.

### Task 4: limpar eventos, com auditoria

**Files:**
- Modify: `internal/storage/queries/heartbeats.sql` (`DeleteHeartbeatEvents`); `make generate`
- Modify: `internal/heartbeat/service.go` (`ClearEvents`, `AuditEventsCleared`)
- Modify: `internal/httpapi/heartbeathandlers.go` (`ClearEvents`), `internal/httpapi/routes.go`
- Test: `internal/httpapi/heartbeat_acoes_integration_test.go`

**Interfaces:**
- Produz: query `-- name: DeleteHeartbeatEvents :execrows` (`DELETE FROM heartbeat_events e USING heartbeats h WHERE
  h.id = e.heartbeat_id AND h.user_id = sqlc.arg(user_id) AND e.heartbeat_id = sqlc.arg(heartbeat_id)`);
  `const AuditEventsCleared = "heartbeat.events_cleared"`;
  `func (s *Service) ClearEvents(ctx context.Context, userID, id uuid.UUID) error`; rota
  `POST /api/v1/heartbeats/{id}/events/clear`.

- [ ] **Step 1: testes que falham.**
  - `TestClearEventsWritesAudit` (R1 da §10): 3 eventos → 204, corpo vazio; `GET .../events` vazio; uma linha em
    `audit_log` com `action = 'heartbeat.events_cleared'`, `entity = 'heartbeat'`, `entity_id` = id, `user_id` = dono,
    `ip_hash IS NULL`.
  - `TestClearEventsKeepsTheSignalsAndTheDeadline`: `last_ping_at`, `last_success_at`, `last_runtime_ms`, `deadline_at` e
    incidente aberto iguais aos literais de antes.
  - `TestClearEventsOnAnEmptyLogIsStillNoContent`: 0 eventos → 204 e 1 linha de auditoria.
  - `TestPingAfterClearRecordsOneEvent`: limpar, depois `POST /ping/{token}` → 1 evento; `last_ping_at` = instante do ping.
- [ ] **Step 2:** `go test -run 'ClearEvents|PingAfterClear' ./internal/httpapi` → FAIL (405/404 da rota inexistente).
- [ ] **Step 3: implementar.** `ClearEvents` num `InUserTx`: `exists` (404), `DeleteHeartbeatEvents`, `InsertAuditEntry`
  com `At: s.clk.Now()`, na mesma transação.
- [ ] **Step 4:** PASS. Sabotagem: gravar a auditoria fora da transação, depois do commit, e forçar erro na auditoria →
  eventos apagados sem rastro (o teste com gancho de erro quebra); tirar `h.user_id` da query → o teste de acesso da Task 5
  quebra.
- [ ] **Step 5:** commit `feat(heartbeat): limpar o log de eventos com auditoria`.

### Task 5: acesso alheio e entrada nas rotas novas

**Files:**
- Modify: `internal/httpapi/heartbeats_integration_test.go` (`TestAnotherUsersHeartbeatIsIndistinguishableFromMissing`)
- Modify: `internal/httpapi/entrada_hostil_internal_test.go` (`TestIDMalformadoE404EmTodaRotaComID`)

**Interfaces:** consome as rotas das Tasks 3 e 4.

- [ ] **Step 1: testes que falham.** Acrescentar ao cenário IDOR as rotas `POST .../clone` e `POST .../events/clear`. Depois
  do cenário, afirmar: contagem de heartbeats do intruso = 0 e do dono = 1; os eventos do dono continuam (literal, 1 evento
  de `fail`); nenhuma linha em `audit_log` com `entity_id` do dono e `user_id` do intruso. Em
  `TestIDMalformadoE404EmTodaRotaComID`, as duas rotas com id `"nao-e-uuid"` e `"%00"` → 404, nunca 500.
- [ ] **Step 2:** `go test -run 'AnotherUsersHeartbeat|IDMalformado' ./internal/httpapi` → PASS, porque as Tasks 3 e 4
  já filtram pelo dono. O teste só vale depois de visto falhar: sabotar `DeleteHeartbeatEvents` sem `h.user_id` → os
  eventos do dono somem e o teste quebra; sabotar `Clone` lendo a linha sem `user_id` → 201 para o intruso.
- [ ] **Step 3:** desfazer as sabotagens.
- [ ] **Step 4:** PASS; `TestEveryRegisteredRouteDeniesByDefault` e `TestEveryMutationRequiresTheAntiCSRFHeader` cobrem as
  rotas novas sem mudança (conferir que listam as duas).
- [ ] **Step 5:** commit `test(httpapi): duplicar e limpar não alcançam tarefa alheia`.

### Task 6: painel — faixas, descrição longa, duplicar e limpar

**Files:**
- Modify: `web/src/features/heartbeats/esquema.ts` (`LIMITES`, `MAX_DESCRICAO`)
- Modify: `web/src/lib/api/cliente.ts` (`duplicarHeartbeat`, `limparEventosDoHeartbeat`)
- Modify: `web/src/features/heartbeats/PaginaHeartbeat.tsx` (botões, `Confirmacao` com `'limpar'`)
- Test: `web/src/features/heartbeats/esquema.test.ts`, `web/src/features/heartbeats/detalhe.test.tsx`

**Interfaces:**
- Produz: `duplicarHeartbeat(id: string): Promise<HeartbeatComPing>` (POST `.../clone`);
  `limparEventosDoHeartbeat(id: string): Promise<null>` (POST `.../events/clear`).
- Textos fixos: período `'Entre 1 minuto e 365 dias.'`; tolerância `'Entre 30 segundos e 365 dias.'`; descrição
  `'No máximo 10000 caracteres.'`; botões "Duplicar" e "Limpar eventos"; confirmação "Limpar o log de eventos?" com
  descrição "Os sinais registrados somem. O estado, o prazo e os incidentes continuam." e ação "Limpar eventos"; avisos
  "Eventos apagados." e, ao fechar o diálogo da cópia, "Cópia criada.".

- [ ] **Step 1: testes que falham.**
  - `esquema.test.ts`: 365 dias aceito, 366 dias recusado com o texto novo; tolerância 365 dias aceita, 365 dias + 1 s
    recusada; descrição de 10000 `"é"` aceita, 10001 recusada.
  - `detalhe.test.tsx`: "Duplicar" chama a rota, abre o diálogo com a URL; "Já guardei" navega para `/heartbeats/<id da
    cópia>`; erro 409 de limite mostra o aviso persistente e o botão volta a ficar ativo; "Limpar eventos" some com o log
    vazio; com eventos, pede confirmação, chama a rota e o log mostra "Nenhum sinal recebido."; botão ocupado não envia
    duas vezes; agendamento `unknown` (PR 1) não mostra "Duplicar".
- [ ] **Step 2:** `npx vitest run src/features/heartbeats` (sob flock) → FAIL.
- [ ] **Step 3: implementar.** "Limpar eventos" lê `useEventosDoHeartbeat(id)` (mesma chave do log) e some com a primeira
  página vazia; depois de limpar, invalida essa chave. "Duplicar" e "Limpar" usam `executar`, que já segura o clique duplo.
  Ação destrutiva fica terciária (vermelho só no `Confirmar`).
- [ ] **Step 4:** PASS; `npx tsc --noEmit` e lint limpos. Sabotagens: deixar o teto antigo em `LIMITES` → o caso de 365
  dias quebra; tirar a condição de log vazio → o teste "some" quebra.
- [ ] **Step 5:** commit `feat(painel): faixas novas, duplicar e limpar eventos`.

### Task 7: painel — exemplos de uso no diálogo da URL

**Files:**
- Modify: `web/src/features/heartbeats/DialogoDaUrlDePing.tsx`
- Test: `web/src/features/heartbeats/cadastro.test.tsx` (o diálogo aparece no cadastro)

**Interfaces:**
- Produz: abas "curl", "crontab", "wget", "Python", "PowerShell" (componente de abas já usado no painel; se não houver, botões
  com `aria-pressed`), cada uma com `Copiavel`. Textos fixos, com `URL` = a URL recebida:
  - crontab: `0 3 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 -o /dev/null -X POST URL`
  - wget: `wget -q -T 10 -t 3 -O /dev/null --method=POST URL`
  - Python: `urllib.request.urlopen(urllib.request.Request("URL", method="POST"), timeout=10)`
  - PowerShell: `Invoke-RestMethod -Method Post -TimeoutSec 10 -Uri URL`
- A frase "Só POST..." continua.

- [ ] **Step 1:** teste: cada aba mostra o texto literal com a URL do teste e o botão de copiar com rótulo próprio
  ("Copiar o exemplo de crontab" etc.); nenhum exemplo usa GET (`not.toMatch(/\bGET\b/)` e `wget` com `--method=POST`).
- [ ] **Step 2:** `npx vitest run src/features/heartbeats/cadastro.test.tsx` → FAIL.
- [ ] **Step 3:** implementar; alvo de toque de 44 px nas abas, foco visível.
- [ ] **Step 4:** PASS. Sabotagem: `wget` sem `--method=POST` → quebra.
- [ ] **Step 5:** commit `feat(painel): exemplos de uso da URL de ping`.

### Task 8: ensaio do binário publicado sobre o schema novo

**Files:**
- Create: `deploy/ci/ensaiar-piso.sh`
- Modify: `.github/workflows/ci.yml` (só o `shellcheck`, se a lista de scripts for explícita)

**Interfaces:**
- Produz: `sh deploy/ci/ensaiar-piso.sh <tag-do-piso> <tag-do-branch>`. Reutiliza o `compose.yaml` (que já lê
  `STILLON_TAG`), o usuário demo do seed e os helpers de `deploy/ci/checar-compose.sh` (`entrar`, `sql`), copiando o que for
  preciso sem mudar aquele script. O PR 3 e o PR 12 acrescentam cenários; este PR só tem o seu.

- [ ] **Step 1:** escrever o script, que sai com código diferente de zero no primeiro passo que falhar:
  1. `STILLON_TAG=<branch>` sobe a pilha (aplica a +0); login; cria pela API uma tarefa com `period_sec` 31536000,
     `grace_sec` 31536000 e descrição de 10000 caracteres; guarda a `ping_url`; duplica a tarefa; limpa os eventos da cópia.
  2. Troca só o app para `STILLON_TAG=<piso>` (`docker compose up -d --wait app`); login de novo.
  3. Com o piso: `GET /api/v1/heartbeats` → 200 com as duas tarefas e `period_sec` 31536000; `POST` na `ping_url` → 200;
     `PATCH` de `name` → 200; `PATCH` reenviando `period_sec: 31536000` → 400 `out_of_range`, e o `period_sec` relido pelo
     `sql` segue 31536000.
  4. Volta ao branch e derruba a pilha, removendo os volumes que criou.
- [ ] **Step 2:** `shellcheck deploy/ci/ensaiar-piso.sh` limpo. Ver o script falhar: rodar com `<piso>` = a tag publicada
  **anterior** ao PR 1 (a `main` de antes) → o passo 3 recebe 500 no `GET` da lista.
- [ ] **Step 3:** rodar com a tag publicada do PR 1 (SHA conferido no run do `Coolify release`) → sai 0. Uma vez por PR,
  sob flock, com bateria acima de 50%; não entra no CI (orçamento de Actions).
- [ ] **Step 4:** colar a saída resumida e as tags usadas no corpo do PR.
- [ ] **Step 5:** commit `test(deploy): ensaio do piso de rollback sobre as faixas novas`.

### Task 9: documentação e verificação final

**Files:** `README.md` (bullet "Heartbeats": faixas, duplicar, limpar com auditoria, exemplos).

- [ ] README atualizado, sem citar ping-now (PR 3).
- [ ] `go build ./... && go vet ./... && make test` (sob flock, bateria acima de 50%; senão, só o CI).
- [ ] `sh deploy/ci/checar-nome.sh`.
- [ ] `npx tsc --noEmit`, lint e `npx vitest run` no `web/`.
- [ ] Revisão do branch com `code-review` e `security-review`. PR draft com: número final da migration, as sabotagens de
  cada task, a saída do ensaio da Task 8, e a nota "ping-now foi para o PR 3 (Ruling)". Merge só com o PR 1 publicado e
  conferido por SHA; depois do merge, conferir o run do `Coolify release` deste SHA antes de anunciar.
