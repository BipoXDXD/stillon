# SP2: fundação dos canais por integradores e webhook genérico, plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** tornar o canal de aviso extensível (modelo B: JSON selado por tipo), com `name`, `config_rev`, `send_down` e
`send_up`, troca de configuração com `If-Match`, teste sob demanda, histórico de envios, duas raias no dispatcher e
tolerância de rollback a tipo e evento desconhecidos; e entregar o primeiro integrador da ordem da spec, o **webhook genérico**
com envelope v1 e assinatura Standard Webhooks, ponta a ponta até o painel.

**Arquitetura:** uma tabela `map[Kind]Integration` de funções em `internal/notify` substitui os `switch` repetidos sobre o tipo
(Repeated Switches). `discord` e `email` guardam o valor cru em `config_enc` para sempre; tipos novos guardam
`{"v":1,...}` selado com o mesmo `Box` e o mesmo AAD. A reivindicação filtra tipos e eventos que o binário conhece (D-13=a),
num `Dispatcher` com duas raias e um semáforo de gravação. O webhook usa um cliente `safedial` novo que nunca segue redirect.

**Stack:** Go 1.26.10, pgx v5, sqlc, goose, Postgres 18 (17 na matriz do CI) via testcontainers; React + TypeScript + Zod +
Vitest.

**Spec:** `/Users/01bipo/Documents/Claude/stillon-pos-janela/specs/SP2-canais-integradores.md` (§§2 a 12 e 15), com a revisão
`/Users/01bipo/Documents/Claude/stillon-pos-janela/specs/revisao-SP1-SP2.md` (seção SP2 e "Segunda revisão"). O arco do aviso
TLS vem da SP1: `/Users/01bipo/Documents/Claude/stillon-continuidade/repo/.claude/worktrees/sp1-piso/docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`,
§5.2 "Entrega do aviso", §6 linha +4 e §12 (PR 1 e PR 7).

**Escopo deste plano:** os PRs 1 a 6 da §15 da spec (fundação e webhook) e a fatia do painel do PR 11 para o webhook, aqui
chamada PR 6b. Slack, Telegram, PagerDuty, ntfy, a rotação da `MASTER_KEY` e o resto do painel ficam em "PRs seguintes", sem
tarefas.

## Decisões D-1 a D-13

Coordenação, por delegação do dono: "SP1 e SP2: seguir as recomendações de `specs/revisao-SP1-SP2.md` e implementar: SP1
primeiro". Toda D da §14 da spec tem recomendação escrita; nenhuma precisou de Ruling.

| # | Decisão (recomendação da spec, delegada) | Onde pesa neste plano |
|---|---|---|
| D-1 | **B**: JSON selado em `config_enc`, tipo Go por integrador | Tasks 4, 6, 13, 19 |
| D-2 | **(b)** `MASTER_KEY_PREVIOUS` e comando de recifrar, em PR separado e não bloqueante | Fora deste plano ("PRs seguintes") |
| D-3 | **(a)** bot do usuário no Telegram | Fora (PR do Telegram) |
| D-4 | **(a)** JSON fixo + Standard Webhooks; placeholders em spec futura | Tasks 19 a 22 |
| D-5 | **Adiar** canal por monitor | Nada aqui: todo aviso vai a todos os canais elegíveis |
| D-6 | **Manter 10** canais | `MaxChannels` não muda |
| D-7 | Slack e webhook como Discord (30 min, depois 6 h); Telegram e ntfy como e-mail; PagerDuty sem lembrete | Task 21 (webhook); demais tipos nos PRs seguintes |
| D-8 | **Change event** no teste do PagerDuty, com aviso na tela e recusa de chave `R` só no teste | Fora (PR do PagerDuty) |
| D-9 | **Não pausar** automaticamente | Nenhuma escrita do sondador em `notif_channels` |
| D-10 | **SP1 primeiro** | Ordem e portões abaixo |
| D-11 | **(b)** aviso de certificado a todos os tipos menos PagerDuty; `send_down`/`send_up` não filtram | Task 12 (`tls_expiring` vai a todos); a exclusão do PagerDuty entra no PR dele |
| D-12 | **(a)** recusar `send_up=false` no PagerDuty (400, `send_up`, `invalid`) | Fora (PR do PagerDuty) |
| D-13 | **(a)** avisos de tipo ou evento desconhecido ficam `pending`, sem gastar tentativa | Task 2 (PR 1) e Task 8 (raias) |

**Ordem com a SP1 (D-10).** O PR 1 desta spec é só tolerância, não usa nada novo e pode entrar logo depois do PR 1 da SP1
(#245): é ele que protege o rollback de qualquer linha nova, e precisa estar publicado antes do PR 3 daqui. Os PRs 2 a 6b
só começam com o **PR 7 da SP1 publicado** (conferido pelo run do `Coolify release` do SHA), porque tocam a reivindicação,
`Event` e o envelope que aquele PR altera; a coordenação pode segurar mais, nunca menos.

## Rulings deste plano

- **Lista de eventos conhecidos e o PR 7 da SP1.** O PR 1 daqui cria `eventosConhecidos` com `opened`, `resolved` e
  `reminder`. O PR 1 da SP1 já excluiu `incident_id IS NULL` do `lote`; os dois filtros coexistem. O PR 7 da SP1 precisa
  acrescentar `tls_expiring` a `eventosConhecidos` ao remover o filtro de `incident_id`: sem isso, o aviso de certificado fica
  pendente para sempre. Custo se errado: avisos de certificado parados até o ajuste. **Avisar o plano do PR 7 da SP1.**
- **Teste de evento e tipo desconhecidos afrouxa o CHECK no container.** O schema não aceita `kind` nem `event` futuros. O
  teste do PR 1 faz `ALTER TABLE ... DROP CONSTRAINT` como superusuário no Postgres do teste e insere `kind='futuro'` e
  `event='futuro'`, simulando "schema novo, binário velho", no molde de `internal/storage/heartbeat_rollback_test.go`.
- **`NextReminder` devolve `(time.Time, bool)`.** `false` cobre "tipo sem cadência" (PagerDuty, futuro) e "tipo desconhecido";
  `enqueueDue` pula o par, e só o tipo desconhecido gera `slog.Warn`. Um erro abortando a página era o defeito da §2.
- **A regra de D-11 fica no SQL do PR do PagerDuty**, não na tabela `Integration`: hoje nenhum tipo implementado fica fora, e
  um campo "recebe TLS" seria Speculative Generality. A Task 12 já faz `tls_expiring` ir a todos os canais entregáveis.
- **Raias e tipos conhecidos saem da tabela `Integration`.** `kindsConhecidos()` (PR 2 em diante) é o conjunto de chaves de
  `integrations`; cada raia lista os tipos cuja `Lane` é a dela. Um fato, uma fonte. No PR 4 a raia "URL do usuário" nasce
  vazia (laço que não reivindica nada) e o webhook entra nela no PR 6.
- **Campos de erro sem prefixo.** Na forma nova (`config: {...}`), o 400 aponta `url`, `headers`, `email`, como na forma
  antiga; o vocabulário é fechado e não há ambiguidade dentro de um canal.
- **Forma antiga do `POST`.** `url` e `email` no topo continuam valendo para `discord` e `email` por uma versão. `config`
  junto com `url` ou `email` é 400 (`config`, `invalid`). Tipo novo só pela forma `config`.
- **`name` opcional.** Ausente ou `null` no `POST` grava `NULL`, e o painel mostra o `hint`. No `PATCH`, `null` limpa e ausente
  não altera; string vazia é 400 (`name`, `invalid`).
- **`PATCH` sem nenhum campo** continua 400 (`enabled`, `required`), o contrato atual. Resultado com `send_down` e `send_up`
  falsos é 400 (`send_down`, `invalid`), conferido em Go com a linha travada (`FOR UPDATE`), antes de o CHECK virar 500.
- **`If-Match`.** Valor aceito: `"<rev>"`, aspas incluídas, decimal positivo. Ausente → 428 (`precondition_required`, código
  novo). Malformado, fraco (`W/`), `*` ou diferente → 412 (`precondition_failed`, código novo). A resposta de sucesso do `PUT`
  e da rotação leva `ETag: "<rev novo>"` e `rev` no corpo.
- **Rotação em canal que não é webhook:** 404 se alheio ou inexistente; 400 (`kind`, `invalid`) se do dono.
- **`/test` de Discord** usa o `SendTest` atual; o do webhook manda o envelope com `event: "test"`; o do e-mail verificado
  usa `Resend.SendTestMessage`, novo, sem link nem `Idempotency-Key`.
- **Literal de IP no webhook.** IP literal que `safedial.AllowIP` recusa é 400 (`url`, `invalid`) no `Parse`: não é oráculo,
  o dono digitou o IP. Nome que resolve para endereço bloqueado só aparece no dial e sai como `delivery_failed`, igual a
  destino inalcançável (`TestDestinoRecusadoEInalcancavelRespondemIgual`).
- **Peças do `targets` que o webhook reusa vão para o `safedial`:** `terminaEmNumero` vira `safedial.EndsInNumber` e o mapa
  `forbiddenHeaders` vira `safedial.ForbiddenHeader(canon string) bool`. `targets` passa a chamá-los. Refatoração pura, em
  commit próprio (Task 13), testes do `targets` verdes antes e depois.
- **Segredo do webhook** vive no JSON selado: `secret`, `previous_secret` e `previous_until`. Gerado só pelo servidor; o DTO de
  entrada não tem esses campos, então mandá-los é 400 por campo desconhecido.
- **Medição da §13 do PR 4** é um passo de verificação local, registrado no corpo do PR, e não um gate automático: os números
  dependem da máquina.

## Restrições globais

- Migration: `internal/storage/migrations/000NN_notif_channels_integrations.sql`, `000NN` = **próximo número livre na hora do
  merge** (a SP1 em andamento ocupa 00023, 00024 e a de `target_tls`); a `00015` é vaga para sempre e não pode ser usada.
- `kind IN ('discord','email','webhook','slack','telegram','pagerduty','ntfy')`; `name` 1 a 80 caracteres ou `NULL`;
  `config_rev int NOT NULL DEFAULT 1 CHECK (config_rev >= 1)`; `send_down`/`send_up bool NOT NULL DEFAULT true` com
  `CHECK (send_down OR send_up)`; `CHECK (octet_length(config_enc) <= 16384)`; índice `(channel_id, id DESC)` em
  `notifications` no lugar de `notifications_channel_idx`. SQL exato na §8 da spec.
- AAD continua `notif_channels:config_enc:<id>`; `config_rev` fica fora dele. `discord` e `email`: valor cru. Tipos novos:
  JSON com `"v":1`.
- `config_rev` só muda em `PUT .../config` e na rotação; `PATCH` nunca o toca nem exige `If-Match`.
- Envelope v1 com todas as chaves sempre presentes; `incident_id` e `subject` anuláveis; `tls` só em `tls_expiring`.
- Assinatura: `webhook-id` = id da notificação (UUIDv7 gerado no `/test`), `webhook-timestamp` = segundos Unix do relógio
  injetado na tentativa, `webhook-signature` = `v1,<base64(HMAC-SHA256(chave, id + "." + timestamp + "." + corpo))>`, com a
  chave = bytes do base64 depois de `whsec_`; duas assinaturas separadas por espaço durante 24 h de rotação.
- Segredo de assinatura: 32 bytes de `crypto/rand`, `whsec_` + base64 padrão; mostrado só no 201 do webhook e na rotação.
- Raias: confiável (`discord`, `email`) com 10 s por envio; URL do usuário (`webhook`) com 5 s; lote de 8 em cada; um único
  `Dispatcher`, um único `notify_total`; semáforo de 4 vagas em `persistir`, com a espera dentro dos 5 s de `persistTimeout`.
- Jitter de 0 a 20% somado ao backoff (não ao `Retry-After`).
- Limite por canal no lote para tipos com `OnePerBatch` (webhook neste plano): depois do primeiro envio bem-sucedido, os
  seguintes voltam com `próxima = agora + 2 s × posição`, sem gastar tentativa.
- SSRF do webhook: só `https`, portas 443 e 8443, sem credencial nem fragmento, URL até 2048 bytes, host diferente do host de
  `PUBLIC_BASE_URL` (comparado depois de `DialedHost` e sem ponto final), cliente sem redirect (3xx = `rejected` permanente).
- Cabeçalhos do webhook: até 8; nome até 64; valor até 1024; total 4 KiB; recusa `safedial.ForbiddenHeader`, `webhook-*` e
  `Content-Type`.
- Corpo de requisição: 16 KiB (o `BodyLimit` atual). `limit` de `deliveries` até 100.
- Erros: envelope atual (`apierr.WriteError`, `field`/`reason` de vocabulário fechado); 404 igual para alheio e inexistente,
  com corpo sem dado do dono e recurso intacto.
- Segredo (URL, cabeçalho, segredo de assinatura) nunca em resposta fora dos dois pontos permitidos, em log, em erro ou em
  auditoria: canário `CANARY-<uuid>` conferido em corpo, cabeçalho e log capturado.
- Antes de dizer que algo funciona: `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`; no painel,
  `npm run lint && npm run typecheck && npm test` em `web/`. Teste novo só vale visto falhar com a sabotagem indicada,
  registrada no corpo do PR. Valor esperado é literal calculado à mão.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%, só o CI.
- Merge é deploy: conferir o run do `Coolify release` do SHA antes de anunciar. Um PR por linha da tabela de PRs; commits no
  formato `tipo(escopo): descrição`.

## Foco de revisão

1. **Rollback para a imagem do PR 1 com linhas `webhook` na fila:** a linha fica `pending`, `attempts` inalterado, canal sem
   `failing`, e nenhum lembrete de outro canal para de sair. Teste: `TestRollbackComWebhookPendenteNaoTravaNada` na Task 6,
   rodando as consultas do PR 1 contra o schema novo.
2. **`PUT .../config` e rotação simultâneos na mesma aba e em abas diferentes:** o segundo recebe 412 e o segredo de
   assinatura não se perde. Teste: `TestPutDepoisDaRotacaoComRevAntigaDa412EPreservaSegredo` na Task 22.
3. **Webhook que responde 2xx mas segura o corpo aberto:** o envio termina no prazo de 5 s da raia, sem goroutine presa, e
   conta como entregue. Teste: `TestWebhookCorpoPenduradoTerminaNoPrazo` na Task 20.
4. **Canal pausado com aviso já na fila e `/test` pedido pelo dono:** o teste sai e o canal continua pausado; o aviso da fila
   continua cancelado. Teste: `TestTesteEmCanalPausadoEnviaEMantemPausa` na Task 16 (asserta também o `cancelled`).
5. **Painel numa aba antiga recebendo `kind: "webhook"` e chaves novas (`name`, `rev`, `send_*`):** a lista não quebra e a linha
   aparece como tipo genérico. Teste: `lista com tipo desconhecido mostra linha genérica` na Task 3, com o JSON do PR 5.

## Mapa de arquivos

| Arquivo | Responsabilidade | Tasks |
|---|---|---|
| `internal/notify/policy.go` | Cadência; `NextReminder` com `bool` | 1, 4 |
| `internal/notify/reminders.go` | `enqueueDue` tolerante | 1 |
| `internal/notify/known.go` → `integration.go` | Tipos e eventos conhecidos; tabela `Integration` | 2, 4, 21 |
| `internal/storage/queries/notifications.sql` | `ClaimDueNotifications` com filtros; `ListChannelDeliveries` | 2, 5, 8, 17 |
| `internal/storage/queries/incidents.sql` | `ListDeliverableChannels(user, event)` | 12 |
| `internal/storage/queries/channels.sql` | Colunas novas, `ReplaceChannelConfig`, `UpdateChannel` | 14, 15, 22 |
| `internal/storage/migrations/000NN_notif_channels_integrations.sql` | Migration da §8 | 6 |
| `internal/notify/notify.go` | `Event` ampliado, `Kind`s novos | 5, 19 |
| `internal/notify/dispatcher.go` | Raias, jitter, semáforo, limite por canal | 7 a 10 |
| `internal/notify/service.go` | `Create`, `Update`, `ReplaceConfig`, `Test`, `Deliveries`, `RotateSigningSecret` | 4, 14 a 17, 21, 22 |
| `internal/notify/webhook.go` | `Parse`, `Hint`, envelope, assinatura, `Send`, `SendTest` | 19 a 21 |
| `internal/notify/resend.go` | `SendTestMessage` | 16 |
| `internal/prober/safedial/` | `NewPostClient`, `EndsInNumber`, `ForbiddenHeader` | 13 |
| `internal/prober/scheduler.go`, `heartbeat.go` | `enfileirar` com evento | 12 |
| `internal/httpapi/channelhandlers.go`, `routes.go`, `internal/apierr/errors.go` | Rotas, DTOs, códigos novos | 14 a 18, 22 |
| `cmd/stillon/main.go` | Composição: testers, webhook | 5, 21 |
| `web/src/lib/api/schemas.ts`, `cliente.ts`, `web/src/features/canais/*` | Painel | 3, 23, 24 |

---

## PR 1: tolerância de rollback (sem migration)

### Task 1: cadência opcional e lembretes que não param por tipo desconhecido

**Files:**
- Modify: `internal/notify/policy.go`, `internal/notify/reminders.go:102-125`
- Test: `internal/notify/policy_test.go`, `internal/notify/reminders_test.go`

**Interfaces:**
- Produces: `func (p Policy) NextReminder(kind Kind, startedAt, baseAt time.Time) (time.Time, bool)`; `cadences map[Kind]*cadence`
  onde `nil` = tipo conhecido sem lembrete.

- [ ] **Step 1: testes que falham**
  - `TestNextReminderTipoDesconhecidoNaoTemLembrete`: `DefaultPolicy().NextReminder("futuro", t0, t0)` → `ok == false`.
  - `TestNextReminderCadenciaNilNaoTemLembrete`: `Policy` com `cadences{"x": nil}` → `ok == false`.
  - Os testes atuais de cadência passam a ler `(vence, ok)` e assertam `ok == true` com os mesmos literais.
  - `TestKindDesconhecidoNaoParaOsLembretes` (integração, `reminders_test.go`): incidente aberto com abertura avisada a dois
    canais, um `discord` e um `kind='futuro'` (CHECK afrouxado como no Ruling), mesma página; `enqueueDue` devolve 1, há um
    `reminder` para o Discord e nenhum para o outro, sem erro.
- [ ] **Step 2:** `go test ./internal/notify -run 'NextReminder|KindDesconhecido'` → FAIL (assinatura e erro devolvido).
- [ ] **Step 3:** implementar a assinatura; `enqueueDue` faz `continue` em `!ok`, com `slog.Warn("lembrete: tipo de canal
  desconhecido", "channel_id", ...)` só quando o tipo não está em `cadences`.
- [ ] **Step 4:** mesmo comando → PASS. **Sabotagem:** devolver o erro em `enqueueDue` → `TestKindDesconhecido...` falha.
- [ ] **Step 5:** commit `fix(notify): lembretes não param com tipo de canal desconhecido`.

### Task 2: reivindicar só tipos e eventos conhecidos (D-13=a)

**Files:**
- Create: `internal/notify/known.go`
- Modify: `internal/storage/queries/notifications.sql` (`ClaimDueNotifications`), `internal/notify/dispatcher.go:reivindicar`
- Test: `internal/storage/claim_desconhecido_test.go` (pacote que já testa `ClaimDueNotifications`, como o PR 1 da SP1)

**Interfaces:**
- Produces: `var kindsConhecidos = []Kind{KindDiscord, KindEmail}`; `var eventosConhecidos = []string{incident.EventOpened,
  incident.EventResolved, incident.EventReminder}`; `ClaimDueNotificationsParams` ganha `Kinds []string` e `Events []string`.

- [ ] **Step 1: testes que falham**
  - `TestTipoDesconhecidoFicaPendenteSemGastarTentativa`: CHECK de `kind` afrouxado; uma notificação vencida num canal
    `futuro` e outra num `discord`, `MaxRows: 1`, a do tipo desconhecido mais antiga. A reivindicação devolve só a do Discord;
    a outra segue `pending`, `attempts = 0`, `next_attempt_at` inalterado.
  - `TestEventoDesconhecidoFicaPendenteSemGastarTentativa`: idem com CHECK de `event` afrouxado e `event='futuro'`.
- [ ] **Step 2:** rodar com `-run 'Desconhecid'` → FAIL (as duas linhas são reivindicadas).
- [ ] **Step 3:** no `lote`: `FROM notifications n JOIN notif_channels c ON c.id = n.channel_id`, `AND c.kind =
  ANY(sqlc.arg(kinds)::text[]) AND n.event = ANY(sqlc.arg(events)::text[])`, `FOR UPDATE OF n SKIP LOCKED`; manter o
  `AND incident_id IS NOT NULL` do PR 1 da SP1. `reivindicar` passa as duas listas; regenerar sqlc.
- [ ] **Step 4:** PASS; `make test` verde. **Sabotagem:** passar `Kinds` com `futuro` (ou remover o filtro) → falha.
- [ ] **Step 5:** commit `fix(notify): aviso de tipo ou evento desconhecido fica pendente no rollback`.

### Task 3: painel tolerante a tipo de canal desconhecido

**Files:**
- Modify: `web/src/lib/api/schemas.ts:183-193`, `web/src/features/canais/PaginaCanais.tsx:279`
- Test: `web/src/features/canais/canais.test.tsx`

**Interfaces:**
- Produces: `TipoDeCanal = z.enum(['discord','email']).or(z.string().transform(() => 'desconhecido' as const))`;
  `Canal.kind: 'discord' | 'email' | 'desconhecido'`. `Canal` passa a ignorar chaves extras (o `z.object` padrão já descarta).

- [ ] **Step 1: testes que falham:** `lista com tipo desconhecido mostra linha genérica`: a API falsa devolve um Discord e um
  `{kind:'webhook', name:'Ops', rev:1, send_down:true, send_up:true, ...}`; as duas linhas aparecem, a segunda com o texto
  "Outro tipo" e o `hint`, sem botão de reenviar verificação; nenhum erro de carregamento. E `parse de kind ausente continua
  falhando`.
- [ ] **Step 2:** `npm test -- canais` → FAIL (erro de parse derruba a lista).
- [ ] **Step 3:** schema e rótulo por `switch` exaustivo sobre `kind` (`'desconhecido'` → "Outro tipo").
- [ ] **Step 4:** PASS + lint + typecheck. **Sabotagem:** voltar ao `z.enum` puro → falha.
- [ ] **Step 5:** commit `fix(painel): canal de tipo desconhecido não derruba a lista`.

**Fecho do PR 1:** `go build ./... && go vet ./... && make test`, testes do `web/`, PR sozinho, merge e conferência do release
por SHA **antes** de abrir o PR 3.

---

## PR 2: tabela `Integration`, `ChannelTester` e `Event` ampliado (refatoração, sem mudança de comportamento)

Portão: PR 7 da SP1 publicado. Todos os testes existentes verdes antes e depois de cada Task; nenhum teste muda de valor
esperado.

### Task 4: tabela `Integration` no lugar dos `switch` sobre o tipo

**Files:**
- Create: `internal/notify/integration.go` (absorve `known.go`)
- Modify: `internal/notify/config.go` (`hint`), `service.go` (`validar`, `Create`), `policy.go` (`DefaultPolicy`)
- Test: `internal/notify/integration_test.go`

**Interfaces:**
- Produces:
  ```go
  type Lane int // LaneTrusted, LaneUserURL
  type VerifyMode int // VerifyMessage, VerifyLink
  type ParseEnv struct{ PublicHost string; EmailEnabled bool }
  type Integration struct {
      Parse       func(cfg json.RawMessage, env ParseEnv) (string, error) // texto a selar; erro é *ValidationError
      Hint        func(dest crypto.Secret) string
      Verify      VerifyMode
      Cadence     *cadence // nil = sem lembrete
      Lane        Lane
      OnePerBatch bool
  }
  var integrations map[Kind]Integration
  func kindsConhecidos() []string       // chaves, ordenadas
  func kindsDaRaia(l Lane) []string     // ordenadas
  ```
  `discord.Parse` lê `{"url": ...}` e devolve a URL crua; `email.Parse` lê `{"email": ...}` e devolve o endereço normalizado,
  com `kind unavailable` quando `!env.EmailEnabled`. Ambos com `DisallowUnknownFields`.

- [ ] **Step 1: testes que falham:** `TestIntegracoesTemTiposDaMigration` (chaves = `{"discord","email"}`);
  `TestParseDiscordEEmailComoAntes` em `[table]` com vazio (`url required`), URL fora de `discordHosts` (`url invalid`), campo
  desconhecido (`config invalid`), e-mail com nome de exibição (`email invalid`), Resend desligado (`kind unavailable`);
  `TestHintSemSegredo` com os literais de hoje (`webhook …6789`, `a***@exemplo.com`).
- [ ] **Step 2:** `go test ./internal/notify -run 'Integracoes|ParseDiscord|HintSem'` → FAIL (não compila).
- [ ] **Step 3:** criar a tabela; `validar` converte a entrada antiga em `config` e chama `Parse`; `hint` e `DefaultPolicy`
  leem da tabela; a `var kindsConhecidos` da Task 2 vira a função `kindsConhecidos()`, e `eventosConhecidos` muda de
  arquivo sem mudar de valor.
- [ ] **Step 4:** PASS e `make test` inteiro verde. **Sabotagem:** tirar `email` do mapa → `TestIntegracoes...` e os testes
  de cadastro de e-mail falham.
- [ ] **Step 5:** commit `refactor(notify): tabela de integrações no lugar dos switch por tipo`.

### Task 5: `Event` ampliado e `ChannelTester`

**Files:**
- Modify: `internal/notify/notify.go` (`Event`), `dispatcher.go:tentar`, `notifications.sql` (`ClaimDueNotifications`),
  `service.go`, `cmd/stillon/main.go:187`
- Test: `internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `Event.TLS` e a linha reivindicada como o PR 7 da SP1 os deixou (`IncidentID` anulável, `NotAfter`, `StageDays`).
- Produces: `Event` ganha `IncidentID *uuid.UUID` e `SubjectKind string` (`"target"` ou `"heartbeat"`; `""` no teste);
  a reivindicação devolve `subject_kind` = `CASE WHEN i.heartbeat_id IS NOT NULL THEN 'heartbeat' ELSE 'target' END`
  (aviso TLS: `target`). `type ChannelTester interface{ SendTest(ctx context.Context, dest crypto.Secret) error }`;
  `NewChannelService(pool, box, clk, testers map[Kind]ChannelTester, email VerificationMailer, publicBaseURL string)`.

- [ ] **Step 1:** `TestEventoLevaIncidenteEAssunto` com notifier falso que grava o `Event`: abertura de alvo → `IncidentID`
  = id do incidente, `SubjectKind = "target"`; abertura de heartbeat → `"heartbeat"`; aviso TLS (fixture do PR 7 da SP1) →
  `IncidentID == nil`, `SubjectKind = "target"`.
- [ ] **Step 2:** FAIL (campos inexistentes).
- [ ] **Step 3:** coluna na consulta, preenchimento em `tentar`; renomear `DiscordTester`; `main.go` passa
  `map[Kind]ChannelTester{KindDiscord: notify.NewDiscord()}`.
- [ ] **Step 4:** PASS, `make test` verde. **Sabotagem:** `IncidentID: &uuid.Nil` no TLS → falha.
- [ ] **Step 5:** commit `refactor(notify): Event com incidente e assunto, testador de canal por tipo`.

---

## PR 3: migration

### Task 6: migration da §8, rollback e descida

**Files:**
- Create: `internal/storage/migrations/000NN_notif_channels_integrations.sql` (SQL exato da §8 da spec)
- Modify: sqlc regenerado (`GetChannel` usa `SELECT *` e ganha os campos)
- Test: `internal/storage/notif_channels_integrations_test.go`

**Interfaces:**
- Produces: colunas `name`, `config_rev`, `send_down`, `send_up`; constraints `notif_channels_kind_check`,
  `notif_channels_some_event`, `notif_channels_config_size`; índice `notifications_channel_id_idx`.

- [ ] **Step 1: testes que falham**
  - `TestMigrationAceitaTiposNovosERecusaOutros`: INSERT `webhook` passa; `futuro` falha com `notif_channels_kind_check`.
  - `TestMigrationLimitesDeColunas`: `name` com 0 e 81 caracteres falha, 1 e 80 passam; `send_down=false, send_up=false`
    falha; `config_rev = 0` falha; `config_enc` com 16385 bytes falha, 16384 passa; linha antiga ganha `config_rev=1`,
    `send_down=send_up=true`, `name NULL`.
  - `TestRollbackComWebhookPendenteNaoTravaNada`: com o schema novo e as consultas do PR 1 (`ClaimDueNotifications` com
    `kindsConhecidos` = discord e email, `ListReminderCandidates`, `InsertChannel`, `ListChannels`), um canal `webhook` com
    abertura pendente e um Discord com incidente aberto: o Discord é reivindicado e recebe lembrete; o webhook fica `pending`
    com `attempts = 0`; `ListChannels` lista os dois; `channel_health` do webhook vazio.
  - `TestDownApagaTiposNovosERestauraCheck`: `Up`, um canal de cada tipo com notificações, `Down` com a CLI do goose no papel
    da aplicação: restam só `discord` e `email`, as notificações do webhook somem em cascata, `kind='webhook'` volta a falhar,
    `notifications_channel_idx` existe e o novo não; `Up` de novo passa.
  - `TestSondadorNaoAlteraCanal`: no escopo do sondador, `UPDATE notif_channels SET send_down = false` e `SET name = 'x'`
    afetam 0 linhas (só SELECT continua permitido).
  - `TestIndiceDeEntregasServeAPaginacao`: `EXPLAIN` de `ListChannelDeliveries` (Task 17 traz a consulta; aqui, a forma
    `WHERE channel_id = $1 AND id < $2 ORDER BY id DESC LIMIT 100`) usa `notifications_channel_id_idx`.
- [ ] **Step 2:** FAIL (migration inexistente).
- [ ] **Step 3:** criar a migration com o número livre; regenerar sqlc; `go build ./...`.
- [ ] **Step 4:** PASS em Postgres 17 e 18 (matriz do CI). **Sabotagem:** remover o `NO FORCE` do `Down` →
  `TestDownApaga...` falha (o `DELETE` não apaga nada sob RLS).
- [ ] **Step 5:** commit `feat(storage): canais com nome, versão de configuração e eventos`.

---

## PR 4: dispatcher com raias, jitter, semáforo e limite por canal

### Task 7: jitter no backoff

**Files:** Modify `internal/notify/dispatcher.go` (`tentar`, `adiar`); Test `internal/notify/backoff_test.go`

**Interfaces:**
- Produces: `func comJitter(d time.Duration, r float64) time.Duration` (`d + d×0,2×r`, `r ∈ [0,1)`); campo
  `Dispatcher.sortear func() float64` (`rand.Float64` de `math/rand/v2`; testes fixam).

- [ ] **Step 1:** `TestComJitter` em `[table]`: (30 s, 0) → 30 s; (30 s, 0,5) → 33 s; (1 h, 0,999) → menor que 72 min; (0, 0,5)
  → 0. `TestRetryAfterNaoGanhaJitter`: 429 com `Retry-After: 120` e `sortear = 0,999` na primeira tentativa → `próxima =
  agora + 120 s`.
- [ ] **Step 2:** FAIL. **Step 3:** `espera := max(comJitter(backoff(n.Attempts), d.sortear()), se.RetryAfter)`; mesmo em
  `adiar`. **Step 4:** PASS. **Sabotagem:** `comJitter` devolvendo `d` → falha. **Step 5:** commit
  `feat(notify): jitter de até 20% no backoff`.

### Task 8: duas raias num `Dispatcher`

**Files:** Modify `internal/notify/dispatcher.go`; Test `internal/notify/raias_test.go`

**Interfaces:**
- Consumes: `kindsDaRaia(l Lane) []string` (Task 4).
- Produces: `type raia struct{ nome string; kinds []string; sendTimeout time.Duration }`; `func raiasPadrao() []raia`
  (confiável 10 s, URL do usuário 5 s); `Run` sobe um laço por raia; `Dispatch(ctx)` despacha as raias em paralelo e espera,
  devolvendo o total (mantém a API dos testes); raia com `kinds` vazio não consulta o banco; construtor interno
  `novoDispatcher(..., raias []raia, notifiers ...Notifier)` para os testes.

- [ ] **Step 1:**
  - `TestWebhookLentoNaoAtrasaARaiaConfiavel`: raias de teste com `webhook` na raia lenta (notifier falso que dorme 5 s) e
    `discord` na confiável; 8 webhooks e 1 Discord vencidos: o Discord sai em menos de 1 s de relógio real; os webhooks
    terminam em `timeout` no prazo de 5 s.
  - `TestDuasRaiasUsamUmaMetricaSo`: um registro Prometheus; despacho nas duas raias; `Gather` tem uma família
    `notify_total` com rótulos dos dois tipos.
  - `TestRaiaVaziaNaoReivindica`: com a raia do usuário vazia, uma linha `webhook` pendente continua `pending`,
    `attempts = 0`.
- [ ] **Step 2:** FAIL. **Step 3:** `reivindicar(ctx, r raia)` passa `r.kinds`; `tentar` usa `r.sendTimeout`.
- [ ] **Step 4:** PASS. **Sabotagens:** reivindicar sem filtro de tipo → `TestWebhookLento...` falha; construir um
  `Dispatcher` por raia no `main` → `TestDuasRaias...` (que passa pelo construtor público) entra em pânico.
- [ ] **Step 5: medição (§13), antes do PR sair de rascunho:** `TestLoteDe16` (tag de build `medicao`, fora do `make test`)
  despacha 16 envios lentos de 4 s pelas duas raias contra o harness e registra `pgxpool` (`AcquireCount`, `EmptyAcquireCount`,
  `MaxConns`) e `runtime.MemStats.HeapInuse` antes e no pico. Rodar com `go test -tags medicao -run LoteDe16 ./internal/notify
  -v`, colar os números no corpo do PR. Critério para seguir: pico de heap abaixo de 10 MiB acima da base e nenhum
  `persistTimeout` estourado.
- [ ] **Step 6:** commit `feat(notify): duas raias de envio num dispatcher`.

### Task 9: semáforo de gravação

**Files:** Modify `internal/notify/dispatcher.go:persistir`; Test `internal/notify/raias_test.go`

**Interfaces:**
- Produces: `const gravacoesSimultaneas = 4`; `Dispatcher.vagas chan struct{}`; gancho de teste `antesDeGravar func()` chamado
  com a vaga tomada.

- [ ] **Step 1:** `TestGravacaoDeDesfechoRespeitaOSemaforo`: 16 desfechos nas duas raias; o gancho incrementa um contador
  atômico, guarda o máximo, espera 50 ms e decrementa; máximo observado = 4. `TestEsperaPorVagaContaNoPrazo`: com as 4 vagas
  presas pelo gancho por 6 s, a quinta gravação desiste em `persistTimeout` com erro, e a linha volta pelo lease.
- [ ] **Step 2:** FAIL (máximo 16). **Step 3:** tomar a vaga com `select` sobre `ctx.Done()` dentro do contexto de
  `persistTimeout`. **Step 4:** PASS. **Sabotagem:** remover o semáforo → falha. **Step 5:** commit
  `feat(notify): no máximo 4 gravações de desfecho ao mesmo tempo`.

### Task 10: limite por canal dentro do lote

**Files:** Modify `internal/notify/dispatcher.go:entregarEmSerie`; Test `internal/notify/limite_por_canal_test.go`

**Interfaces:**
- Consumes: `Integration.OnePerBatch`.
- Produces: `Dispatcher.umPorLote map[Kind]bool` montado da tabela (testes trocam); `const intervaloNoCanal = 2 * time.Second`.

- [ ] **Step 1:** `TestAdiamentoPorLimiteNaoGastaTentativa`: `umPorLote = {webhook: true}`, três notificações vencidas do mesmo
  canal, notifier falso que sempre aceita: um envio só; a primeira `sent`, a segunda e a terceira `pending` com `attempts`
  igual ao de antes da reivindicação e `next_attempt_at = agora + 2 s` e `agora + 4 s`. `TestLimiteNaoAdiaAberturaVelha`: a
  segunda é abertura de incidente já resolvido → `cancelled` (passa por `semEnvio` antes). `TestDiscordNaoTemLimite`: três
  do Discord → três envios.
- [ ] **Step 2:** FAIL. **Step 3:** em `entregarEmSerie`, depois do primeiro `sent` de um tipo limitado, cada seguinte vira
  `desfecho{status: "pending", proxima: agora + intervaloNoCanal×posição, devolver: true}` após `semEnvio`, contado como
  `deferred`. **Step 4:** PASS. **Sabotagem:** `devolver: false` → falha. **Step 5:** commit
  `feat(notify): um envio por lote nos canais limitados`.

---

## PR 5: fan-out por evento, API de canais e cliente sem redirect

### Task 11: códigos de erro novos

**Files:** Modify `internal/apierr/errors.go`; Test `internal/apierr/errors_test.go`

**Interfaces:**
- Produces: `CodePreconditionRequired` (`precondition_required`, 428, "Envie o cabeçalho If-Match com a versão do canal."),
  `CodePreconditionFailed` (`precondition_failed`, 412, "O canal foi alterado em outra aba. Recarregue."),
  `CodeChannelNotVerified` (`channel_not_verified`, 409, "Confirme o canal antes de testá-lo."). `CodeDeliveryFailed` passa a
  dizer "Não foi possível entregar a mensagem ao destino." (vale para cadastro, troca e teste).

- [ ] **Step 1:** casos novos na tabela do teste de status e mensagem. **Step 2:** FAIL. **Step 3:** entradas na tabela.
  **Step 4:** PASS. **Sabotagem:** status 409 no 412 → falha. **Step 5:** commit `feat(apierr): códigos de precondição e de
  canal não verificado`.

### Task 12: fan-out por evento

**Files:**
- Modify: `internal/storage/queries/incidents.sql` (`ListDeliverableChannels`), `internal/prober/scheduler.go:541`,
  `internal/prober/heartbeat.go:127,144`, o enfileiramento TLS do PR 7 da SP1
- Test: `internal/prober/fanout_test.go`

**Interfaces:**
- Produces: `ListDeliverableChannels(ctx, ListDeliverableChannelsParams{UserID, Event string})` com
  `AND ((sqlc.arg(event) = 'opened' AND send_down) OR (sqlc.arg(event) = 'resolved' AND send_up)
  OR sqlc.arg(event) NOT IN ('opened','resolved'))`.

- [ ] **Step 1:** `TestFanOutRespeitaSendDownESendUp`: canais A (down e up), B (só up), C (só down), D pausado: `opened` →
  {A, C}; `resolved` → {A, B}; `tls_expiring` → {A, B, C}. `TestMudarSendDownNaoMexeNaFila`: abertura enfileirada, depois
  `send_down=false` → a linha continua `pending` e sai.
- [ ] **Step 2:** FAIL. **Step 3:** consulta e os três chamadores. **Step 4:** PASS. **Sabotagem:** listar sem filtrar pelo
  evento → falha. **Step 5:** commit `feat(prober): aviso só aos canais que querem o evento`.

### Task 13: `safedial` sem redirect e peças de validação compartilhadas

**Files:**
- Modify: `internal/prober/safedial/transport.go`, `host.go`; `internal/targets/validate.go` (passa a chamar o `safedial`)
- Test: `internal/prober/safedial/post_client_test.go`, `host_test.go`

**Interfaces:**
- Produces: `func NewPostClient(timeout time.Duration) *http.Client` (`CheckRedirect` devolve `http.ErrUseLastResponse`);
  `func NewPostClientAllowing(timeout time.Duration, allowed ...netip.AddrPort) *http.Client` (pânico fora de loopback, como
  `NewClientAllowing`); `func EndsInNumber(host string) bool`; `func ForbiddenHeader(canon string) bool`.

- [ ] **Step 1:** `TestPostClientNaoSegueRedirect` em `[table]` com 301, 302, 307, 308: o cliente devolve a resposta 3xx e o
  segundo servidor (outra porta liberada) recebe 0 pedidos. `TestPostClientAllowingRecusaNaoLoopback` (pânico com 10.0.0.1).
  `TestEndsInNumber` com os casos de hoje de `terminaEmNumero` (mover os testes).
- [ ] **Step 2:** FAIL. **Step 3:** primeiro commit, só mover `terminaEmNumero` e `forbiddenHeaders` (testes do `targets`
  verdes antes e depois: `refactor(safedial): ...`); depois o cliente novo. **Step 4:** PASS. **Sabotagem:**
  `NewPostClient` = `newClient` → falha. **Step 5:** commit `feat(safedial): cliente de POST que não segue redirect`.

### Task 14: criar e alterar canal com `name`, `config`, `send_down` e `send_up`

**Files:**
- Modify: `internal/notify/service.go` (`ChannelInput`, `Create`, `Channel`, `toChannel`; `SetEnabled` vira `Update`),
  `internal/storage/queries/channels.sql` (`InsertChannel`, `ListChannels`, `GetChannelView` com as colunas novas;
  `GetChannelForUpdate`, `UpdateChannel`), `internal/httpapi/channelhandlers.go`
- Test: `internal/httpapi/channels_integration_test.go`, `internal/notify/service_test.go`

**Interfaces:**
- Produces:
  ```go
  type ChannelInput struct{ Kind Kind; Name *string; Config json.RawMessage; URL, Email string; SendDown, SendUp *bool }
  type ChannelPatch struct{ Enabled, SendDown, SendUp *bool; Name optional[string] } // optional distingue null de ausente
  func (s *ChannelService) Update(ctx context.Context, userID, id uuid.UUID, p ChannelPatch) (Channel, error)
  ```
  `Channel` ganha `Name *string`, `Rev int32`, `SendDown`, `SendUp bool`. `channelResponse` tem exatamente
  `id, kind, name, hint, status, enabled, send_down, send_up, rev, created_at, last_error, last_failure_at`.
  O `optional[T]` do `httpapi` é traduzido no handler para um tipo do domínio (`notify.OptionalString{Set bool; Value *string}`).

- [ ] **Step 1: testes que falham**
  - `TestRespostaDeCanalTemChavesExatas` (GET, POST, PATCH).
  - `TestCriarCanalComConfigEName`: `{"kind":"discord","name":"Ops","config":{"url":...}}` → 201 com `name:"Ops"`, `rev:1`,
    `send_down` e `send_up` `true`; forma antiga `{"kind":"discord","url":...}` → 201 igual, `name:null`.
  - `TestCriarCanalRecusaEntradaRuim` em `[table]`: campo desconhecido no topo e dentro de `config` → 400; `config` junto com
    `url` → 400 `config invalid`; `name` vazio, com 81 caracteres, com `\u0000` e com `\u001b` → 400 `name invalid`; `name`
    com 80 caracteres → 201; `send_down:false, send_up:false` → 400 `send_down invalid`; `id`, `rev`, `verified_at`,
    `user_id` no corpo → 400; nenhum desses grava linha (contagem antes e depois).
  - `TestPatchCanal`: `name` ausente não altera, `null` limpa, `"Novo"` troca; `send_up:false` grava; `enabled:null` → 400;
    corpo `{}` → 400 `enabled required`; deixar os dois eventos falsos → 400 e nada gravado.
  - `TestPatchNaoMudaConfigRev`: três `PATCH` seguidos → `rev` continua 1 e `config_enc` igual byte a byte.
- [ ] **Step 2:** FAIL. **Step 3:** implementar; `Update` lê com `GetChannelForUpdate` (`FOR UPDATE`), aplica em Go, grava com
  `UpdateChannel ... WHERE user_id = $u AND id = $id`; zero linhas → `ErrNotFound`.
- [ ] **Step 4:** PASS. **Sabotagens:** incrementar `config_rev` no `UpdateChannel` → `TestPatchNaoMudaConfigRev` falha;
  aceitar `null` como "não altera" em `name` → `TestPatchCanal` falha.
- [ ] **Step 5:** commit `feat(canais): nome, eventos por canal e corpo de configuração`.

### Task 15: `PUT /api/v1/channels/{id}/config` com `If-Match`

**Files:**
- Modify: `service.go` (`ReplaceConfig`), `channels.sql` (`ReplaceChannelConfig`), `channelhandlers.go`, `routes.go`
  (`comEnvio`)
- Test: `internal/httpapi/channels_config_integration_test.go`

**Interfaces:**
- Consumes: `Integration.Parse`, `ChannelTester` (Task 5), `CodePrecondition*` (Task 11).
- Produces: `func (s *ChannelService) ReplaceConfig(ctx context.Context, userID, id uuid.UUID, rev int32, cfg json.RawMessage)
  (Channel, error)`; `ErrPreconditionFailed`; `ErrKindNotReplaceable` (e-mail → 400 `kind invalid`);
  `ReplaceChannelConfig :execrows` = `UPDATE notif_channels SET config_enc = $novo, config_rev = config_rev + 1 WHERE id = $id
  AND user_id = $u AND config_rev = $rev`; `func ifMatch(r *http.Request) (rev int32, presente, valido bool)`. A transação de
  gravação insere a auditoria `channel.config_replaced` (entity `notif_channel`, sem detalhe).

- [ ] **Step 1: testes que falham**
  - `TestPutConfigTrocaEIncrementaRev`: Discord, `If-Match: "1"`, URL nova → 200, `rev:2`, `ETag: "2"`, hint novo; o teste de
    destino recebeu 1 mensagem; `last_success_at` gravado; auditoria com 1 linha.
  - `TestPutConfigComVersaoVelhaDa412`: dois `PUT` com `If-Match: "1"` → o segundo 412, nada enviado ao destino, valor do
    primeiro mantido.
  - `TestPutConfigSemIfMatchDa428` e `TestIfMatchMalformadoDa412` em `[table]` com `1`, `"0"`, `"-1"`, `W/"1"`, `*`, `"abc"`.
  - `TestPutConfigDestinoFalhaMantemAnterior`: destino responde 500 → 502 `delivery_failed`, `rev` e `config_enc` intactos.
  - `TestPutConfigEmEmailDa400` e `TestPutConfigEmCanalPausadoContinuaPausado`.
  - `TestPutConfigNaoEnviaAntesDoIfMatch`: rev errada e URL válida → 412 e o servidor de destino com 0 pedidos.
- [ ] **Step 2:** FAIL. **Step 3:** ordem da §5 da spec: ler e conferir `If-Match` (412 sem enviar), `Parse`, testar fora de
  transação, gravar com versão e auditoria na mesma transação; zero linhas → 412.
- [ ] **Step 4:** PASS. **Sabotagem:** tirar `config_rev = $rev` do `WHERE` → `TestPutConfigComVersaoVelhaDa412` falha.
- [ ] **Step 5:** commit `feat(canais): trocar configuração com If-Match`.

### Task 16: `POST /api/v1/channels/{id}/test`

**Files:**
- Modify: `service.go` (`Test`), `resend.go` (`SendTestMessage`), `channelhandlers.go`, `routes.go` (`comEnvio`),
  `notifications.sql` (`MarkChannelSuccess` passa a ser chamada também sob `InUserTx`)
- Test: `internal/httpapi/channels_test_integration_test.go`, `internal/notify/senders_test.go`

**Interfaces:**
- Produces: `func (s *ChannelService) Test(ctx context.Context, userID, id uuid.UUID) error`; `ErrNotVerified`;
  `func (r *Resend) SendTestMessage(ctx context.Context, dest crypto.Secret) error` (assunto "[StillOn] Teste de canal", corpo
  sem link, sem `Idempotency-Key`). O prazo do teste é o `sendTimeout` da raia do tipo.

- [ ] **Step 1: testes que falham** (comportamento por estado da §9)
  - `TestTesteDeCanalVerificadoEnviaELimpaFailing`: Discord com falha definitiva gravada → 204, `status` volta a `verified`.
  - `TestTesteEmCanalPausadoEnviaEMantemPausa`: canal pausado com uma abertura na fila → 204, `enabled:false`, e um
    `Dispatch` seguinte deixa a abertura `cancelled`.
  - `TestEmailNaoVerificadoNaoRecebeTeste`: `pending` e `expired` → 409 `channel_not_verified`, Resend falso com 0 pedidos.
  - `TestTesteDeEmailVerificadoSemLink`: corpo enviado sem `/verify/` e sem `Idempotency-Key`; `verified_at` inalterado.
  - `TestTesteDeEmailComResendDesligado` → 400 `kind unavailable`.
  - `TestTesteQueFalhaNaoMarcaFailing`: destino 500 → 502 `delivery_failed`, `last_failure_at` continua nulo.
  - `TestTesteDeCanalDivideOBaldeDeEnvio`: `envioBurst` testes seguidos → o seguinte 429 com `Retry-After`; um `POST
    /channels` depois também 429.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS. **Sabotagens:** remover a checagem de `verified_at` →
  `TestEmailNaoVerificado...` falha; recusar `enabled=false` → `TestTesteEmCanalPausado...` falha.
- [ ] **Step 5:** commit `feat(canais): testar canal sob demanda`.

### Task 17: `GET /api/v1/channels/{id}/deliveries`

**Files:**
- Modify: `notifications.sql` (`ListChannelDeliveries`), `service.go` (`Deliveries`), `channelhandlers.go`, `routes.go`
  (`protegida`)
- Test: `internal/httpapi/channels_deliveries_integration_test.go`

**Interfaces:**
- Produces: `type Delivery struct{ ID uuid.UUID; Event, Status string; Attempts int32; LastError *string; CreatedAt time.Time;
  SentAt *time.Time; SubjectName string }`; `func (s *ChannelService) Deliveries(ctx, userID, channelID uuid.UUID, cursor
  uuid.UUID, limit int) ([]Delivery, error)`; resposta `{"items":[...], "next_cursor"?}` com o `paginaDe` existente; chaves de
  item exatamente `id, event, status, attempts, last_error, created_at, sent_at, subject_name`.

- [ ] **Step 1:** `TestDeliveriesPaginaPorId`: 5 notificações, `limit=2` → ids 5,4 + cursor; depois 3,2; depois 1 sem cursor.
  `TestDeliveriesLimites` em `[table]`: `limit=0`, `101`, `-1`, `abc`, cursor que não é UUID → 400; `limit=100` → 200.
  `TestDeliveriesDeCanalSemEnviosEVazio` → `items: []`. `TestDeliveriesChavesExatas`.
- [ ] **Step 2:** FAIL. **Step 3:** canal conferido na mesma transação (ausente → `ErrNotFound`), `WHERE channel_id = $c AND
  ($cursor = uuid nulo OR id < $cursor) ORDER BY id DESC LIMIT $n`. **Step 4:** PASS. **Sabotagem:** `ORDER BY id` ascendente
  → falha. **Step 5:** commit `feat(canais): histórico de envios do canal`.

### Task 18: segurança transversal das rotas de canal

**Files:**
- Modify: `internal/httpapi/rotas_negar_por_padrao_integration_test.go` (nada a acrescentar à allowlist), novo
  `internal/httpapi/channels_seguranca_integration_test.go`, `canario_integration_test.go`

- [ ] **Step 1: testes que falham ou que travam o contrato**
  - `TestEveryRegisteredRouteDeniesByDefault` e `TestEveryResourceRouteRejectsRevokedFamily` passam a enumerar
    `PUT .../config`, `POST .../test` e `GET .../deliveries` (conferir na saída `-v` que as rotas aparecem; nenhuma entra em
    `rotasPublicas`).
  - `TestCanalAlheioDa404EmTodaRota`: usuário B em `PATCH`, `DELETE`, `PUT .../config` (com `If-Match` certo), `POST .../test`,
    `GET .../deliveries`, `POST .../verify/resend` do canal de A → 404 `not_found`, corpo sem id, nome, hint ou `rev` de A;
    o canal de A intacto (`config_enc`, `rev`, `enabled`, `name` relidos), destino com 0 pedidos. Inexistente → 404 igual,
    byte a byte fora o `trace_id`.
  - `TestSegredoDoCanalNuncaApareceEmLogNemResposta`: canário na URL do Discord falso e no e-mail; GET, POST, PATCH, PUT,
    `test`, `deliveries` e um erro forçado: canário ausente de corpo, cabeçalhos e log capturado (helper do
    `canario_integration_test.go`).
  - `TestEntradaHostilNuncaDa500` em `[table]` nas rotas novas: corpo de 16 KiB + 1 → 413; `\u0000` em `name` e em `config.url`;
    `'; DROP TABLE notif_channels; --` em `name` → gravado literal ou 400, nunca 500.
- [ ] **Step 2:** FAIL onde a regra faltar. **Step 3:** ajustes pontuais. **Step 4:** PASS. **Sabotagem:** em `Deliveries`,
  consultar as notificações sem conferir antes o canal → para B, a RLS devolve lista vazia com 200, e
  `TestCanalAlheioDa404EmTodaRota` falha.
- [ ] **Step 5:** commit `test(canais): rotas novas negam alheio e não vazam segredo`.

**Fecho do PR 5:** nenhum tipo novo ligado; `make test`, `checar-nome.sh`, release conferido.

---

## PR 6: webhook

### Task 19: `Parse` e `Hint` do webhook (SSRF sintático, cabeçalhos, próprio host)

**Files:** Create `internal/notify/webhook.go`; Modify `notify.go` (`KindWebhook`); Test `internal/notify/webhook_parse_test.go`

**Interfaces:**
- Consumes: `safedial.ValidateURL`, `DialedHost`, `IsSingleLabel`, `HostInstavelNoIDNA`, `EndsInNumber`, `AllowIP`,
  `ForbiddenHeader`.
- Produces:
  ```go
  type webhookInput struct{ URL string `json:"url"`; Headers map[string]string `json:"headers"` } // DisallowUnknownFields
  type webhookConfig struct {
      V int `json:"v"`; URL string `json:"url"`; Headers map[string]string `json:"headers,omitempty"`
      Secret string `json:"secret"`; PreviousSecret string `json:"previous_secret,omitempty"`
      PreviousUntil *time.Time `json:"previous_until,omitempty"`
  }
  func parseWebhook(cfg json.RawMessage, env ParseEnv) (string, error) // JSON v1 sem segredo
  func hintWebhook(dest crypto.Secret) string                          // host
  ```

- [ ] **Step 1:** `TestParseWebhook` em `[table]` (campo, motivo):
  - aceita `https://hooks.exemplo.com/x`, `https://hooks.exemplo.com:8443/x`, URL de 2048 bytes;
  - `url required`: vazio; `url invalid`: `http://`, porta 80, porta 9000, `https://u:p@h/`, `#frag`, 2049 bytes,
    `https://localhost/`, `https://2130706433/`, `https://0x7f000001/`, `https://127.0.0.1/`, `https://[::1]/`,
    `https://169.254.169.254/`, `https://10.0.0.5/`, host instável no IDNA, `\u0000` na URL;
  - próprio host com `PublicHost = "stillon.tech"`: `https://stillon.tech/ping/x`, `https://STILLON.TECH./x`,
    `https://stillon.tech:8443/` → `url invalid`;
  - `headers`: 9 cabeçalhos, nome de 65, valor de 1025, total acima de 4 KiB → `headers invalid`; `Host`, `Content-Type`,
    `webhook-signature`, `Webhook-Id` → `headers invalid` (`TestCabecalhosReservadosRecusados`); 8 válidos passam;
  - `secret` ou `previous_secret` na entrada, ou chave desconhecida → `config invalid`.
  `TestHintWebhookEOHost`: `https://hooks.exemplo.com/CANARY-x?token=CANARY-y` → `hooks.exemplo.com`.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS. **Sabotagem:** comparar o próprio host sem tirar o ponto
  final → `TestWebhookParaOProprioHostERecusado` (o caso com ponto) falha.
- [ ] **Step 5:** commit `feat(notify): validação do webhook genérico`.

### Task 20: envelope, assinatura e envio do webhook

**Files:** Modify `internal/notify/webhook.go`; Test `internal/notify/webhook_send_test.go`, `webhook_harness_test.go`

**Interfaces:**
- Consumes: `Event` (Task 5, com `TLS` do PR 7 da SP1), `safedial.NewPostClient`.
- Produces:
  ```go
  type Webhook struct{ client *http.Client; clk clock.Clock; validar func(string) error; publicHost string }
  func NewWebhook(clk clock.Clock, publicHost string) *Webhook
  func envelope(ev Event, testID uuid.UUID) ([]byte, error)      // JSON v1, chaves fixas
  func assinar(secret string, id string, ts int64, body []byte) (string, error) // "v1,<b64>"
  func (w *Webhook) Send(ctx context.Context, dest crypto.Secret, ev Event) error
  func (w *Webhook) SendTest(ctx context.Context, dest crypto.Secret) error
  ```

- [ ] **Step 1: testes que falham**
  - `TestEnvelopeDeAberturaLiteral`: `Event` fixo → bytes exatos (literal escrito à mão, chaves na ordem `schema, id, event,
    incident_id, subject, started_at, resolved_at, queued_at, cause, closed_by_resume, tls`).
  - `TestEnvelopeSemIncidenteTemIncidentIdNulo`: `tls_expiring` → `"incident_id":null`, `"subject":{"kind":"target",...}`,
    `"started_at":null`, `"cause":null`, `"tls":{"not_after":"…","stage_days":7}`; `test` → `incident_id`, `subject`,
    `started_at`, `cause`, `tls` nulos e `queued_at` = instante do relógio.
  - `TestAssinaturaCobreIdTempoECorpo`: vetor fixo (segredo `whsec_` + base64 de 32 bytes `0x01..0x20`, id
    `01890000-0000-7000-8000-000000000001`, ts `1700000000`, corpo `{"a":1}`) → assinatura literal calculada fora do código
    (anotar no teste o comando `openssl` usado). Trocar qualquer um dos três muda a assinatura.
  - `TestTimestampEDaTentativaEIdEFixo`: duas tentativas da mesma notificação com o relógio avançado 60 s → `webhook-id`
    igual, `webhook-timestamp` difere em 60, corpo idêntico byte a byte.
  - `TestRotacaoAssinaComAsDuasDuranteOPrazo`: com `previous_until` no futuro → duas assinaturas separadas por espaço,
    a nova primeiro; com `previous_until` no passado → só a nova.
  - Harness (servidor em loopback, cliente `NewPostClientAllowing`, `validar` relaxado só para porta e esquema), `[table]`
    da §11: lento acima de 5 s → `timeout` transitório em ≤ 5 s; 2xx com corpo pendurado → entregue em ≤ 5 s
    (`TestWebhookCorpoPenduradoTerminaNoPrazo`, sem goroutine sobrando segundo `goleak` ou contagem de `runtime.NumGoroutine`);
    500, 502, 503 → `server_error`; conexão cai antes do status → `network`; 2xx com corpo truncado → entregue; recusada →
    `network`; 429 com `Retry-After` `30`, `999999`, `NaN`, `-5` → 30 s, 1 h, 0, 0; 301, 307, 308 → `rejected` permanente e 0
    pedidos ao `Location` (`TestWebhookNaoSegueRedirect`); corpo de 10 MiB → lido até 64 KiB.
  - `TestWebhookParaEnderecoInternoERecusado` com o cliente **de produção** (`NewWebhook`, `validar` relaxado): destinos
    `http://127.0.0.1:<porta>`, `http://[::1]:<porta>` (pula sem IPv6), `http://169.254.169.254/`, `http://10.0.0.1/`,
    `https://2130706433/`, e redirect de um servidor liberado para `http://127.0.0.1:<outra>` → `invalid_channel` ou
    `rejected` permanente, e 0 pedidos em todo servidor interno.
  - `TestCabecalhosDoDonoVaoNoPedido`: `X-Token: CANARY-<uuid>` chega ao servidor; `SendError` e o log capturado não o têm.
- [ ] **Step 2:** FAIL. **Step 3:** implementar: revalidar a URL com `validar` a cada envio; `Content-Type:
  application/json`, `User-Agent` atual, cabeçalhos do dono, os três `webhook-*`; `classificarErro`/`classificarResposta`
  existentes (3xx cai no ramo `rejected`); `drenar`.
- [ ] **Step 4:** PASS. **Sabotagens:** omitir o timestamp do HMAC → `TestAssinaturaCobre...` falha; enviar só a nova →
  `TestRotacaoAssina...` falha; usar `safedial.NewClient` → `TestWebhookNaoSegueRedirect` falha; liberar loopback no
  cliente de produção → `TestWebhookParaEnderecoInterno...` falha.
- [ ] **Step 5:** commit `feat(notify): envio do webhook com envelope v1 e assinatura`.

### Task 21: ligar o webhook: integração, segredo no cadastro, troca que preserva o segredo

**Files:**
- Modify: `integration.go` (entrada `webhook`), `service.go` (`Create` gera o segredo; `ReplaceConfig` preserva),
  `channelhandlers.go` (`signing_secret` só no 201 do webhook), `cmd/stillon/main.go` (notifier e tester)
- Test: `internal/httpapi/webhook_integration_test.go`, `internal/notify/integration_test.go`

**Interfaces:**
- Consumes: Tasks 4, 14, 15, 19, 20.
- Produces: `Integration` ganha `Generate func(cfg string) (sealed string, shown string, err error)` e
  `Carry func(old, new string) (string, error)`, `nil` nos tipos sem segredo gerado. Entrada `webhook`: `Parse:
  parseWebhook`, `Hint: hintWebhook`, `Verify: VerifyMessage`, `Cadence` = a do Discord (D-7), `Lane: LaneUserURL`,
  `OnePerBatch: true`. `channelResponse` ganha `signing_secret` com `omitempty`, preenchido só aqui e na rotação.

- [ ] **Step 1: testes que falham**
  - `TestCriarWebhookDevolveSegredoUmaVez`: `POST` → 201 com `signing_secret` `whsec_` + 44 caracteres base64; o servidor de
    destino recebeu um `test` assinado que verifica com esse segredo; `GET /channels` sem `signing_secret`.
  - `TestPutConfigPreservaSegredoDeAssinatura`: depois de um `PUT` com URL nova, um envio verifica com o segredo do 201; com
    rotação em andamento, `previous_secret` e `previous_until` sobrevivem.
  - `TestIntegracaoWebhook`: cadência 30 min / 6 h; raia URL do usuário; `OnePerBatch`.
  - `TestDestinoRecusadoEInalcancavelRespondemIgual`: serviço com um `Webhook` de teste cujo cliente é
    `NewPostClientAllowing` liberando só uma porta de loopback **fechada**, e `validar` relaxado; cadastro em
    `http://127.0.0.1:<porta fechada>/` (conexão recusada) e em `http://10.0.0.1:8443/` (bloqueado no dial) → os dois 502
    `delivery_failed`, corpos iguais fora do `trace_id`, nenhuma linha gravada.
  - `TestConfigDeOutroCanalNaoAbre`: `config_enc` de um webhook copiado para outro canal do mesmo dono → a listagem mostra o
    segundo sem `hint`, e o despacho o fecha `failed/invalid_channel`, sem pedido ao destino.
  - `TestDispatcherEntregaWebhook`: abertura de incidente → o servidor recebe `event:"opened"` com `incident_id` do incidente.
  - `TestUmEnvioPorLoteParaTiposLimitados`: três aberturas do mesmo webhook no mesmo lote → um pedido.
- [ ] **Step 2:** FAIL. **Step 3:** implementar; `main.go` registra `notify.NewWebhook(clk, hostDe(cfg.PublicBaseURL))` no
  dispatcher e em `testers`.
- [ ] **Step 4:** PASS e `make test`. **Sabotagem:** selar só o que veio do cliente no `PUT` → `TestPutConfigPreserva...`
  falha.
- [ ] **Step 5:** commit `feat(canais): webhook genérico com segredo de assinatura`.

### Task 22: rotação do segredo de assinatura

**Files:** Modify `service.go` (`RotateSigningSecret`), `channelhandlers.go`, `routes.go` (`comEnvio`); Test
`internal/httpapi/webhook_rotacao_integration_test.go`

**Interfaces:**
- Produces: `func (s *ChannelService) RotateSigningSecret(ctx context.Context, userID, id uuid.UUID, rev int32) (Channel,
  string, error)`; `POST /api/v1/channels/{id}/signing-secret/rotate` com `If-Match`; `const janelaDeRotacao = 24 *
  time.Hour`; auditoria `channel.signing_secret_rotated`.

- [ ] **Step 1:**
  - `TestRotacaoDevolveSegredoNovoEAssinaComOsDois`: 200 com `signing_secret` novo, `rev` +1; um envio no instante seguinte
    leva duas assinaturas; com o relógio falso 24 h + 1 s depois, só uma.
  - `TestNovaRotacaoDescartaOMaisAntigo`: duas rotações → `previous_secret` = o segundo, prazo recomeça.
  - `TestRotacaoComVersaoVelhaDa412` e sem `If-Match` → 428; o segredo gerado nessa tentativa não aparece em resposta nem log.
  - `TestPutDepoisDaRotacaoComRevAntigaDa412EPreservaSegredo` (Foco de revisão 2).
  - `TestRotacaoEmCanalQueNaoEWebhookDa400` e alheio → 404.
  - `TestRotacaoNaoEnviaNadaAoDestino`: servidor com 0 pedidos.
  - `TestRotacaoGravaAuditoria`: 1 linha, sem segredo.
- [ ] **Step 2:** FAIL. **Step 3:** implementar com `ReplaceChannelConfig` (mesmo `WHERE config_rev = $rev`). **Step 4:** PASS.
  **Sabotagem:** ignorar `If-Match` → `TestRotacaoComVersaoVelhaDa412` falha.
- [ ] **Step 5:** commit `feat(canais): rotação do segredo do webhook`. Fecho do PR 6: README (seção de canais: envelope,
  cabeçalhos, como verificar a assinatura, portas aceitas), `make test`, release conferido.

---

## PR 6b: painel do webhook (fatia do PR 11)

### Task 23: catálogo, formulário do webhook e diálogo do segredo

**Files:**
- Modify: `web/src/lib/api/schemas.ts` (`Canal` com `name`, `rev`, `send_down`, `send_up`, `kind` incluindo `'webhook'`;
  `CanalCriado` com `signing_secret` opcional), `web/src/lib/api/cliente.ts` (`EntradaCanal` como união discriminada com
  `config`), `web/src/features/canais/PaginaCanais.tsx`
- Create: `web/src/features/canais/FormularioWebhook.tsx`, `DialogoSegredo.tsx`
- Test: `web/src/features/canais/webhook.test.tsx`

**Interfaces:**
- Produces: `EntradaCanal = {kind:'discord'; name?: string; config:{url}} | {kind:'email'; ...; config:{email}} |
  {kind:'webhook'; name?: string; config:{url; headers?: Record<string,string>}; send_down: boolean; send_up: boolean}`.

- [ ] **Step 1:** testes: catálogo com um cartão por tipo, e-mail indisponível com motivo quando a API responde
  `kind unavailable`; formulário do webhook com `Avançado` recolhido para cabeçalhos; recusa por campo (`url invalid` sob o
  campo); `delivery_failed` mantém o digitado; 429 mostra o tempo de espera; 201 abre o diálogo com o segredo, botão Copiar e o
  aviso "Este segredo não aparece de novo"; fechar o diálogo some com o segredo do estado (não está no DOM nem no cache da
  consulta). Estados vazios conforme decisões de design 7 e 9.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS, lint, typecheck. **Sabotagem:** guardar o segredo no
  cache da lista → o teste do DOM falha. **Step 5:** commit `feat(painel): cadastro de webhook`.

### Task 24: detalhe do canal: testar, trocar configuração, rotacionar, eventos e histórico

**Files:** Create `web/src/features/canais/DetalheDoCanal.tsx`, `HistoricoDeEnvios.tsx`; Modify `cliente.ts` (`If-Match`);
Test `web/src/features/canais/detalhe.test.tsx`

- [ ] **Step 1:** testes: "Avisar quedas" e "Avisar retornos" chamam `PATCH` e não deixam os dois desligados; Testar
  indisponível com motivo em e-mail `pending`/`expired`; Trocar configuração abre o formulário vazio com o aviso "O que ficar
  em branco será removido" e manda `If-Match: "<rev>"`; 412 mostra "Alterado em outra aba, recarregue" (também na rotação);
  Rotacionar abre o diálogo do segredo; histórico paginado com vazio, carregando, erro e "Guardado por 30 dias"; selos com
  ícone e texto, nunca só cor.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS. **Sabotagem:** omitir o `If-Match` → falha. **Step 5:**
  commit `feat(painel): detalhe e histórico do canal`.

---

## PRs seguintes (sem tarefas aqui)

| PR da spec | Conteúdo | Depende de |
|---|---|---|
| 7 | Slack: `hooks.slack.com`, `{"text","mrkdwn":false}` com escape de `&`, `<`, `>`, raia confiável, `OnePerBatch`, cadência do Discord | PR 6; `TestNomeDoAlvoNaoViraMencaoNoSlack` |
| 8 | Telegram com bot do usuário (D-3=a): `bot_token`, `chat_id`, `retry_after` no corpo até 4 KiB, cadência do e-mail | PR 6 |
| 9 | PagerDuty: `dedup_key = stillon:<incident_id>`, sem lembrete, teste por change event com recusa de chave `R` (D-8), `send_up=false` recusado (D-12), e `kind <> 'pagerduty'` para `tls_expiring` em `ListDeliverableChannels` (D-11) | PR 6 |
| 10 | ntfy: `base_url`, `topic`, `token`, raia URL do usuário, `NewPostClient`, cadência do e-mail | PR 6 |
| 11 (restante) | Formulários e cartões dos tipos acima, à medida que cada um for publicado | Cada tipo |
| D-2 | `MASTER_KEY_PREVIOUS` e comando de recifrar, com procedimento no runbook | Nenhuma; não bloqueia |

Cada tipo novo entra em `integrations`, na raia certa, e só então passa a ser reivindicado: o filtro de tipos conhecidos
(Task 2) e as raias (Task 8) protegem o rollback de cada um sem mudança extra.

## Lacunas da spec (registradas, sem bloquear)

- O aviso de certificado (`tls_expiring`) depende do PR 7 da SP1 acrescentar o evento à lista de conhecidos; a spec da SP1 não
  cita a lista porque ela nasce aqui (primeiro Ruling).
- A spec não diz como `If-Match` malformado, fraco ou `*` se comporta, nem se o `name` é obrigatório: resolvido nos Rulings.
- DNS rebinding (nome que resolve para endereço interno) não tem resolvedor falso no `safedial`; a Task 21 compara IP
  bloqueado com porta fechada, e o caso de DNS continua coberto pelos testes existentes do `safedial`.
- A spec manda o `Down` passar em Postgres 17 e 18 e "assume que o papel da aplicação é dono da tabela": a Task 6 roda na
  matriz do CI, sem confirmar o papel em produção.
- O `/test` do Discord e do webhook grava `last_success_at` sob `InUserTx`; a spec afirma que a política de `channel_health`
  aceita o dono, e a Task 16 o prova.
- As medidas da §13 seguem estimativas até o passo de medição da Task 8.
