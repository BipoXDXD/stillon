# Vigilume — Plano 3: Incidentes e Notificações

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Três falhas seguidas de um alvo abrem um incidente, cinco sucessos o fecham, e cada transição chega ao dono por Discord e por e-mail verificado, com retentativa e auditoria, sem nunca travar o sondador.

**Architecture:** O detector é uma função pura (`internal/incident`) que o sondador chama na mesma transação em que grava o check. Uma linha de `target_health` por alvo guarda a histerese e é travada com `FOR UPDATE`. Abrir ou fechar incidente grava as notificações numa **outbox** (`notifications`), na mesma transação. Um dispatcher, no papel do sondador, a consome com `SKIP LOCKED` e lease, envia pelo `safedial` e faz backoff exponencial até 6 tentativas; a falha definitiva vai para `audit_log`. O cadastro de canais verifica o destino antes de gravar: mensagem de teste no Discord, link de 24h no e-mail. O link é confirmado por `POST`, numa cadeia HTTP pública separada. Toda tabela nova nasce com RLS `FORCE`. O escopo novo, `verify`, enxerga só a linha do token.

**Tech Stack:** Go 1.26, PostgreSQL 16, `pgx/v5`, `sqlc` v1.31.1, `goose` v3, `prometheus/client_golang`, `golang.org/x/time/rate`, `testcontainers-go`. APIs externas: webhook do Discord e `POST https://api.resend.com/emails`.

**Spec:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§4.1, §5.4, §5.5, §5.8, §7, §8, §9, §13).

**Roteiro:** `docs/superpowers/plans/2026-09-22-roadmap.md` (este é o plano 3 de 5).

**Pendências absorvidas:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md`. Entram aqui: a avaliação de incidente no `gravar`, a tolerância a checks fora de ordem (M4), a reconciliação das séries de métricas (D9/M1), o `/readyz` com banco, a advisory lock das partições e as travas de teste pequenas. O resto segue para o `PENDENCIAS-PARA-O-PLANO-4.md` que a Task 11 escreve.

**Validação prévia.** Todo o SQL e todo o código deste plano rodaram numa cópia do repositório antes de o plano ser entregue: PostgreSQL 16 real, `sqlc generate`, e `go test -race ./...` verde, com `sqlc diff`, `vet`, `staticcheck`, `gosec` e `govulncheck` limpos. Cada sabotagem pedida nos passos também foi feita, e o teste ficou vermelho. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

---

## Global Constraints

Valores copiados da spec e das decisões do usuário. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/vigilume`. **PostgreSQL 16+.**
- **Sem framework HTTP e sem ORM** (spec §3.3). Código novo de banco em **sqlc**; `internal/storage/db/*.go` é gerado e **nunca** editado à mão.
- **Todo código de aplicação sob `internal/`** (spec §3.4). **IDs UUIDv7**, gerados em Go com `uuid.NewV7()` (spec §4.2); o PG16 não tem `uuidv7()`.
- **Toda função de serviço recebe `userID` logo depois de `ctx`**; recurso de outro usuário devolve **404, nunca 403** (spec §5.5).
- **RLS com `FORCE`, um papel só**, e política com `NULLIF(current_setting(...), '')` (Plano 2). A API liga `app.user_id`, o sondador e o dispatcher ligam `app.scope = 'prober'`, a verificação de e-mail liga `app.verify_token_hash`. Os três escopos só são ligados em `internal/storage/scope.go`.
- **Histerese (spec §7.2):** abre com **3** falhas consecutivas, fecha com **5** sucessos consecutivos. Só `down` é falha; `degraded` não abre nem fecha.
- **Idempotência no banco (spec §8):** `UPDATE incidents SET open_notified_at = ... WHERE id = $1 AND open_notified_at IS NULL`, e `UNIQUE (incident_id, channel_id, event)` na outbox.
- **Outbox no Postgres** (decisão do usuário): as notificações são gravadas na mesma transação que abre ou fecha o incidente e consumidas com `FOR UPDATE SKIP LOCKED`.
- **Fan-out só para canais habilitados e verificados**, e a verificação é conferida **no envio** também (spec §5.8).
- **Retry com backoff exponencial e teto de tentativas; falha definitiva vai para `audit_log` e nunca derruba o sondador; envio com timeout, fora do caminho da sonda** (spec §8).
- **Chave do Resend é do operador:** `RESEND_API_KEY` + `EMAIL_FROM` na config, as duas ou nenhuma. O canal de e-mail guarda só o endereço, cifrado. Sem a chave, cadastrar e-mail dá erro claro (decisão do usuário).
- **Discord verificado por mensagem de teste no cadastro:** só um 2xx cria o canal. A URL só é aceita em `discord.com/api/webhooks` (e hosts irmãos) e sai pelo cliente `safedial` (decisão do usuário).
- **Segredos em repouso (spec §5.4):** destino do canal em `config_enc`, AES-256-GCM, AAD = `crypto.AAD("notif_channels", "config_enc", id)`. **A API nunca devolve o destino**, só um hint.
- **E-mail (spec §5.8):** token de uso único, 24h, guardado como SHA-256; rate limit de envios e de reenvios por usuário; canal não verificado nunca recebe alerta.
- **Envelope de erro estável** `{"error":{"code","message","fields"?},"trace_id"}` (spec §9.1, D14 do Plano 2). A mensagem vem da tabela de `apierr`, nunca do erro.
- **Métricas (spec §13):** `incidents_open` e `notify_total{kind,result}`, além das do Plano 2. **`/readyz` verifica o banco.**
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa**, e `sqlc diff` limpo quando a tarefa mexe em SQL.
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não envia", "não vê", "não verifica"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho, restaura, e **registra no relatório** o que quebrou e o que viu. Os passos marcados **Sabotagem** dizem qual quebra fazer. O padrão do teste vacuamente verde apareceu 14 vezes no Plano 1 e 5 no Plano 2.

### Desvios registrados em relação à spec

| Spec | Aqui | Motivo |
|---|---|---|
| §7.3 `Evaluate(s, r) Action`, com contadores "já atualizados" e `Window` | `Evaluate(s, r) (State, Action)`: atualiza os contadores e não tem janela | A regra inteira fica num lugar só, testável sem banco; a histerese por contagem não precisa de janela (D1). |
| §8 `Send(ctx, ch Channel, ev Event)` | `Send(ctx, dest crypto.Secret, ev Event)` | O que o envio precisa do canal é o destino já decifrado; o `Secret` impede que ele vaze num log. |
| §3.4 `notify/discord/` e `notify/email/` | Um pacote `internal/notify` | Dispatcher, canais e cadastro dividem a cifra da config e a classificação de erro; em pacotes separados, seria ciclo ou pacote de tipos sem comportamento. |
| §4.1 tabelas | `target_health` e `notifications` novas; `notif_channels.created_at` | A histerese precisa de estado por alvo; a outbox é decisão do usuário; `created_at` ordena a listagem. |
| §4.1 `open_notified_at`, `close_notified_at` | Instante do **enfileiramento** na outbox | Com outbox, a guarda de §8 protege o fan-out; a entrega tem estado próprio em `notifications` (D6). |
| §9 `GET /verify/{token}` | `GET` mostra um botão; `POST /verify/{token}` verifica | Leitores de link de provedores de e-mail abrem os links sozinhos; um GET que verifica confirmaria endereço de terceiro (D2). |
| §3.2 `incident` "sem I/O" | O detector continua puro; o histórico mora em `internal/incident/history` | O pacote puro não pode importar `storage`. |

---

## Decisões tomadas neste plano

Cada uma tem o custo de estar errada, para quem revisar depois. As que vieram do usuário estão nas Global Constraints e não se repetem aqui.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | `Evaluate` atualiza os contadores e devolve o estado novo | Quem chama atualiza (spec §7.3) | Nenhum para o banco; é só onde a regra mora |
| D2 | O link do e-mail abre uma página com botão; só o `POST` do formulário verifica. A cadeia `/verify/` é separada da API: sem `RequireCustomHeader` (um `<form>` não manda o header), sem CORS, CSP própria com `form-action 'self'` e limite por IP mais apertado | `GET` verifica | Com `GET`, o Safe Links do Outlook, e leitores parecidos, verificariam o endereço de um terceiro sem ele: o relay de spam que a §5.8 fecha. O custo do `POST` é um clique a mais |
| D3 | A confirmação **mantém** o hash na linha; o uso único é `verified_at`, e confirmar de novo é sucesso idempotente | Anular o hash | Anular não funciona: no escopo `verify`, a linha nova também precisa passar pela política de SELECT, e o Postgres recusa (provado em spike). Um hash de token já usado fica no banco, e só serve para confirmar o que já está confirmado |
| D4 | `started_at` é a **primeira** falha da sequência; `resolved_at` é o **primeiro** sucesso | O check que cruzou o limiar | A duração exibida mentiria por 2 intervalos na abertura e 4 no fechamento |
| D5 | Check com instante ≤ ao último avaliado é **ignorado** pela histerese, mas gravado em `checks` | Avaliar na ordem de chegada | Um sucesso atrasado (M4) zeraria uma sequência de falhas mais nova e atrasaria um incidente real |
| D6 | `open_notified_at`/`close_notified_at` marcam o **enfileiramento**; a entrega vive em `notifications.status` | Marcar só depois de entregar | Nenhum: a guarda da spec continua sendo o `UPDATE ... IS NULL`, e a entrega tem estado próprio |
| D7 | Entrega **at-least-once**: lease de 2 min; o Resend deduplica por `Idempotency-Key = notification/<id>` (24h); o Discord pode receber duplicado se o processo morrer entre enviar e gravar | Exactly-once | Uma mensagem repetida no Discord num crash raro; nenhuma perdida |
| D8 | Backoff de 30s dobrando, **6 tentativas** (~15 min). `Retry-After` vence se for maior (teto 1h). 4xx é permanente; 429, 5xx, rede e timeout são transitórios; destino bloqueado pelo `safedial` é permanente | Retentar tudo | Retentar um 404 do Discord (webhook apagado) só adia o registro da falha |
| D9 | Uma abertura ainda **não enviada** quando o incidente já fechou é **cancelada**; o fechamento diz quando caiu e quando voltou | Enviar as duas | Num incidente curto com o Discord em 429, o dono receberia "caiu" depois de "voltou" |
| D10 | Canal desabilitado ou não verificado **na hora do envio** vira `cancelled`, sem envio | Confiar no fan-out | Contraria a §5.8 ("verificação feita no envio") |
| D11 | O cadastro **envia antes de gravar**; falha de envio é 502 `delivery_failed` e nada fica gravado | Gravar pendente e enviar depois | Canal zumbi, pendente para sempre, que o dono teria de apagar |
| D12 | **10 canais** por usuário, conferidos antes do envio e de novo na gravação, com o usuário travado (`SELECT ... FOR UPDATE` em `users`) | Sem limite | Cada canal é mais um destino de mensagem |
| D13 | Criar canal e reenviar verificação dividem um balde por usuário: **1 a cada 6 min, burst 5** (10/h). A varredura do limitador passa a nunca apagar um balde antes de ele poder encher | Só o limite por minuto | Com a varredura antiga (10 min fixos), um balde de 6 min seria apagado vazio e voltaria cheio: 5 envios a cada 10 min, o triplo do limite |
| D14 | E-mail sem Resend configurado: `400` com `fields: [{kind, unavailable}]` | 501 ou 503 | Um código novo; o front mostra o motivo no campo |
| D15 | A API mostra um **hint**: "webhook …" + 4 últimos dígitos do id (nunca o token); primeira letra + domínio do e-mail | Mostrar o destino ao dono | Toda resposta de `GET /channels` seria um canal de vazamento do token do webhook |
| D16 | Discord: `https`, hosts `discord.com`, `discordapp.com`, `canary.discord.com`, `ptb.discord.com`; path `/api[/vN]/webhooks/{id}/{token}`; sem porta, credencial, query ou fragmento. **Revalidada a cada envio.** Envio com `allowed_mentions: {parse: []}`, `flags: SUPPRESS_EMBEDS` e `?wait=true` | Qualquer URL https | O servidor faria POST onde o usuário mandasse; um alvo chamado "@everyone" pingaria o servidor inteiro |
| D17 | A mensagem sai **só de campos do banco** (nome do alvo, instantes, causa): a retentativa monta o mesmo corpo | Montar com o relógio | O Resend recusa a mesma chave com outro corpo (409 permanente). Único caso restante: o dono renomeia o alvo entre duas tentativas |
| D18 | Alvo desativado **mantém** o incidente aberto até voltar e fechar pela regra normal; alvo apagado leva o histórico junto (`ON DELETE CASCADE`) | Fechar ao desativar | Um incidente aberto indefinidamente no painel de quem desativa um alvo quebrado |
| D19 | `incidents_open` é o total do **banco**, reconciliado ao subir, a cada 30 s e a cada transição. Com vários sondadores, todos reportam o mesmo número: agregue com `max` | Contar em memória | Com processos separados, a contagem em memória divergiria |
| D20 | O dispatcher roda no papel `prober`: lote de **8** a cada **2 s**, esperando o lote inteiro; envio com **10 s** de prazo, que não herda o cancelamento do shutdown | Worker contínuo com semáforo | Um lote lento atrasa o próximo em até 10 s |
| D21 | `/readyz`: ping com 2 s de prazo; `503 unavailable` se falhar **ou se não houver ping no wiring** | Só o ping | Um wiring esquecido pareceria pronto |
| D22 | `limit` não numérico passa a ser `invalid` (era `out_of_range`), em `/targets` e em `/incidents` | Manter | O cliente mostraria "fora da faixa" para "abc" |
| D23 | Queries sem "Token" no nome (`RenewVerification`, `FindChannelByVerification`) | `#nosec` | O `gosec` G101 dispara no código gerado, onde não dá para pôr `#nosec` |
| D24 | `ChannelService.List` não cai com uma linha que não decifra: ela sai sem hint, e o erro vai ao log | Derrubar a lista | É o defeito que o Plano 2 deixou em `targets.List` |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa o Vigilume, com o que uma pessoa razoável esperaria. Cada uma tem teste na tarefa dona.

1. **O link de verificação é aberto por um leitor de links do provedor de e-mail** (Safe Links do Outlook, antivírus corporativo), e não pela pessoa. Espera-se que o endereço **não** fique verificado até alguém clicar no botão. → Task 7, `TestEmailVerificationFlow`; Task 9, `TestEmailVerificationThroughTheBrowser`.
2. **Duas sondas do mesmo alvo gravam fora de ordem** (um `PATCH` reagendou com uma sonda em voo, M4 do Plano 2). Espera-se que um sucesso atrasado não apague três falhas mais novas, e que o incidente abra. → Task 1, `TestOutOfOrderCheckIsIgnored`; Task 3, `TestOutOfOrderCheckDoesNotResetTheFailureStreak`.
3. **Um alvo se chama "@everyone"** ou tem um link no nome. Espera-se que o alerta no Discord não pingue o servidor inteiro nem busque prévia do link. → Task 5, `TestDiscordSendsWithoutMentionsOrEmbeds`.
4. **O envio de e-mail dá timeout depois de o Resend já ter aceitado**, ou o processo morre no meio. Espera-se **um** e-mail, e não dois; e que nada se perca. → Task 5, `TestResendSendsTheAlertWithAnIdempotencyKey` (mesma chave, mesmo corpo); Task 2, `TestNotificationClaimIsALease`.
5. **Um incidente curto**, que fecha enquanto a abertura ainda está em retentativa (Discord em 429 por alguns minutos). Espera-se receber "voltou, depois de X fora", e não "caiu" chegando depois de "voltou". → Task 6, `TestStaleOpenedIsCancelledOnceTheIncidentResolved`.

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/incident/detector.go` | Histerese pura: `State`, `ProbeResult`, `Action`, `Detector.Evaluate`, eventos da outbox. |
| `internal/storage/migrations/00005_incidents_notifications.sql` | `target_health`, `incidents`, `notif_channels`, `notifications`, RLS, advisory lock das partições. |
| `internal/storage/queries/incidents.sql` | Detecção, fan-out, reconciliação e histórico. |
| `internal/storage/queries/channels.sql` | Cadastro de canais e verificação. |
| `internal/storage/queries/notifications.sql` | Reivindicação e desfecho da outbox, auditoria. |
| `internal/storage/queries/prober.sql` | `ClaimDueTargets` passa a devolver `user_id`. |
| `internal/storage/db/*.go` | **Gerado pelo sqlc.** |
| `internal/storage/scope.go` | `InVerifyTx`, o terceiro escopo de RLS. |
| `internal/prober/scheduler.go` | `gravar` avalia a histerese; `Reconcile`; laço de `Run`. |
| `internal/prober/metrics.go` | `incidents_open` e o conjunto de alvos vistos. |
| `internal/notify/notify.go` | `Kind`, `Event`, `Notifier`, `SendError`, classificação HTTP. |
| `internal/notify/message.go` | Texto do alerta, em pt-BR, só de campos do banco. |
| `internal/notify/discord.go` | Allowlist do webhook e envio ao Discord. |
| `internal/notify/resend.go` | Envio de alerta e de verificação pelo Resend. |
| `internal/notify/config.go` | Cifra do destino, e-mail normalizado, hint. |
| `internal/notify/dispatcher.go` | Consumo da outbox, backoff, auditoria, `notify_total`. |
| `internal/notify/service.go` | `ChannelService`: cadastro, lista, reenvio, verificação. |
| `internal/incident/history/history.go` | Lista e reconhecimento de incidentes do dono. |
| `internal/apierr/errors.go` | Códigos `unavailable`, `channel_limit_reached`, `channel_not_pending`, `delivery_failed`. |
| `internal/httpapi/incidenthandlers.go` | `GET /incidents`, `POST /incidents/{id}/ack`. |
| `internal/httpapi/channelhandlers.go` | `GET/POST /channels`, `DELETE /channels/{id}`, `POST /channels/{id}/verify/resend`. |
| `internal/httpapi/verifyhandlers.go` | Páginas de `GET/POST /verify/{token}`. |
| `internal/httpapi/routes.go` | Rotas novas, `/readyz`, cadeia pública de `/verify/`. |
| `internal/httpapi/middleware/ratelimit.go` | `RateLimitUserEvery`; varredura que respeita o tempo de encher. |
| `internal/httpapi/targethandlers.go` | `limit` não numérico vira `invalid`. |
| `internal/config/config.go` | `RESEND_API_KEY`, `EMAIL_FROM`; `METRICS_ADDR=OFF`. |
| `cmd/vigilume/main.go` | Dispatcher no papel prober; canais, histórico e `/readyz` na API. |
| `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md` | O que atravessa para o Plano 4. |

---

## Ordem das tarefas e seu motivo

1. **Detector:** pacote puro, sem Docker. É a regra que mais importa, e a mais fácil de provar.
2. **Schema, sqlc e escopos:** as tabelas e as políticas, provadas no banco antes de qualquer consumidor. Leva a advisory lock das partições, que mora na mesma migration.
3. **Avaliação no `gravar`:** junta as Tasks 1 e 2 dentro do sondador.
4. **Reconciliação e métricas:** `incidents_open` e a limpeza das séries (D9/M1 do Plano 2).
5. **Canais:** Discord, Resend e a mensagem, testados contra `httptest`, sem Docker.
6. **Dispatcher:** consome a outbox que a Task 3 enche, com os canais da Task 5.
7. **Cadastro de canais:** verificação antes de gravar, limite e escopo `verify`.
8. **API de incidentes e `/readyz`.**
9. **API de canais e verificação:** handlers, cadeia pública e limite de envio.
10. **Dívidas pequenas do Plano 2:** travas de teste que a revisão final pediu.
11. **Config, wiring, teste ponta a ponta e documentação.**

As Tasks 1 e 5 rodam sem Docker (`go test -short` pula o resto).

---
### Task 1: Detector de incidentes — histerese pura

Pacote novo e puro, que roda sem Docker. É a regra da spec §7: rápido para abrir e lento para fechar, tolerante a checks fora de ordem.

**Files:**
- Create: `internal/incident/detector.go`
- Test: `internal/incident/detector_test.go`

**Interfaces:**
- Consumes: nada.
- Produces:
  - `incident.State{ConsecutiveFailures, ConsecutiveSuccesses int32; StreakSince, LastCheckedAt time.Time; Open bool}`: o zero dos instantes significa "nunca".
  - `incident.ProbeResult{Down bool; At time.Time}`.
  - `incident.Action` com `ActionNone`, `ActionOpen`, `ActionClose` (e `String()`).
  - `incident.Detector{OpenAfter, CloseAfter int32}` e `incident.Default` (3/5).
  - `func (d Detector) Evaluate(s State, r ProbeResult) (State, Action)`.
  - Constantes `incident.EventOpened = "opened"` e `incident.EventResolved = "resolved"`, os valores de `notifications.event`.

**Contexto para quem implementa.** Quem chama (Task 3) traz o estado de `target_health` e se há incidente aberto, e grava o que voltar. `StreakSince` é o instante do **primeiro** check da sequência atual, de falhas ou de sucessos, e vira `started_at` ao abrir e `resolved_at` ao fechar (D4). Um check com instante igual ou anterior ao último avaliado volta com o estado **intacto** (D5).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/incident/detector_test.go`:

```go
package incident

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// rodar aplica uma sequência de checks, um por minuto a partir de t0, e
// devolve o estado final e a ação de cada passo. "d" é down, "u" é up.
func rodar(d Detector, s State, seq string) (State, []Action) {
	acoes := make([]Action, 0, len(seq))
	for i, c := range seq {
		var a Action
		s, a = d.Evaluate(s, ProbeResult{Down: c == 'd', At: t0.Add(time.Duration(i) * time.Minute)})
		acoes = append(acoes, a)
	}
	return s, acoes
}

// minuto devolve o instante do i-ésimo check de rodar.
func minuto(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }

func TestEvaluateHysteresis(t *testing.T) {
	cases := []struct {
		nome     string
		seq      string
		abreEm   int // índice do check que abre; -1 se nenhum
		fechaEm  int // índice do check que fecha; -1 se nenhum
		aberto   bool
		desdeIdx int // índice cujo instante deve estar em StreakSince
	}{
		{"duas falhas não abrem", "dd", -1, -1, false, 0},
		{"a terceira falha abre", "ddd", 2, -1, true, 0},
		{"um sucesso no meio recomeça a contagem", "ddudd", -1, -1, false, 3},
		{"falhas além da terceira não reabrem", "dddddd", 2, -1, true, 0},
		{"quatro sucessos não fecham", "ddduuuu", 2, -1, true, 3},
		{"o quinto sucesso fecha", "ddduuuuu", 2, 7, false, 3},
		{"uma falha no meio recomeça os sucessos", "ddduuuuduuuuu", 2, 12, false, 8},
		{"sucessos sem incidente não fecham nada", "uuuuuu", -1, -1, false, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s, acoes := rodar(Default, State{}, c.seq)
			for i, a := range acoes {
				quer := ActionNone
				switch i {
				case c.abreEm:
					quer = ActionOpen
				case c.fechaEm:
					quer = ActionClose
				}
				if a != quer {
					t.Errorf("check %d (%c): ação %v, queria %v", i, c.seq[i], a, quer)
				}
			}
			if s.Open != c.aberto {
				t.Errorf("Open = %v, queria %v", s.Open, c.aberto)
			}
			if !s.StreakSince.Equal(minuto(c.desdeIdx)) {
				t.Errorf("StreakSince = %v, queria o check %d (%v)", s.StreakSince, c.desdeIdx, minuto(c.desdeIdx))
			}
			if !s.LastCheckedAt.Equal(minuto(len(c.seq) - 1)) {
				t.Errorf("LastCheckedAt = %v, queria o último check", s.LastCheckedAt)
			}
		})
	}
}

// started_at e resolved_at saem de StreakSince no instante da transição: o
// alvo caiu na PRIMEIRA falha e voltou no PRIMEIRO sucesso.
func TestTransitionCarriesTheStartOfTheStreak(t *testing.T) {
	s, _ := rodar(Default, State{}, "uddd")
	if !s.Open || !s.StreakSince.Equal(minuto(1)) {
		t.Errorf("ao abrir, StreakSince = %v, queria a primeira falha %v", s.StreakSince, minuto(1))
	}
	s, _ = rodar(Default, State{}, "ddduuduuuuu")
	if s.Open || !s.StreakSince.Equal(minuto(6)) {
		t.Errorf("ao fechar, StreakSince = %v, queria o primeiro sucesso da sequência final %v", s.StreakSince, minuto(6))
	}
}

// M4 do Plano 2: duas sondas do mesmo alvo em voo, gravando fora de ordem.
// Um sucesso VELHO não pode zerar uma sequência de falhas mais nova.
func TestOutOfOrderCheckIsIgnored(t *testing.T) {
	s, _ := rodar(Default, State{}, "dd") // falhas em t0 e t0+1min

	velho := ProbeResult{Down: false, At: minuto(1).Add(-30 * time.Second)}
	depois, a := Default.Evaluate(s, velho)
	if a != ActionNone || depois != s {
		t.Fatalf("check velho mudou o estado: %+v -> %+v (ação %v)", s, depois, a)
	}
	igual := ProbeResult{Down: false, At: minuto(1)}
	if depois, a = Default.Evaluate(s, igual); a != ActionNone || depois != s {
		t.Fatalf("check com o MESMO instante do último mudou o estado: %+v", depois)
	}

	// A terceira falha, mais nova, abre: a sequência sobreviveu ao atraso.
	if _, a = Default.Evaluate(s, ProbeResult{Down: true, At: minuto(2)}); a != ActionOpen {
		t.Errorf("a terceira falha depois do check velho deu %v, queria open", a)
	}
}

// Os limiares são campos, não constantes (spec §7.2).
func TestThresholdsAreConfigurable(t *testing.T) {
	d := Detector{OpenAfter: 1, CloseAfter: 2}
	_, acoes := rodar(d, State{}, "duu")
	if acoes[0] != ActionOpen || acoes[1] != ActionNone || acoes[2] != ActionClose {
		t.Errorf("com 1/2, ações = %v", acoes)
	}
}

// Quem chama traz Open do banco. Um estado que já chega aberto não reabre.
func TestOpenComesFromTheCaller(t *testing.T) {
	s := State{Open: true}
	_, acoes := rodar(Default, s, "ddd")
	for i, a := range acoes {
		if a != ActionNone {
			t.Errorf("check %d reabriu um incidente já aberto: %v", i, a)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/incident/`
Expected: FAIL de compilação, `undefined: Detector` (e os outros tipos).

- [ ] **Step 3: Implementar**

Crie `internal/incident/detector.go`:

```go
// Package incident decide quando uma sequência de sondas vira incidente, e
// quando o incidente acaba (spec §7). É função pura: não faz I/O e não lê
// relógio. Quem chama traz o estado do banco e grava o que voltar.
package incident

import "time"

// State é a histerese de um alvo. O zero de StreakSince e LastCheckedAt
// significa "nunca": o alvo ainda não teve check avaliado.
type State struct {
	ConsecutiveFailures  int32
	ConsecutiveSuccesses int32
	// StreakSince é o instante do PRIMEIRO check da sequência atual, de
	// falhas ou de sucessos. É ele que vira started_at ao abrir e
	// resolved_at ao fechar: o alvo caiu quando a primeira falha aconteceu,
	// não quando a terceira confirmou.
	StreakSince   time.Time
	LastCheckedAt time.Time
	// Open diz se o alvo tem incidente aberto agora. Vem do banco, não da
	// contagem: é a linha em incidents que manda.
	Open bool
}

// ProbeResult é o que o detector precisa saber de um check. Só down conta
// como falha: degraded é estado de exibição (spec §7.2, §11.5).
type ProbeResult struct {
	Down bool
	At   time.Time
}

type Action int

const (
	ActionNone Action = iota
	ActionOpen
	ActionClose
)

func (a Action) String() string {
	switch a {
	case ActionOpen:
		return "open"
	case ActionClose:
		return "close"
	default:
		return "none"
	}
}

// Detector guarda os limiares, que são decisão de produto revisável
// (spec §7.2): mudar um número aqui não mexe em mais nada.
type Detector struct {
	OpenAfter  int32 // falhas consecutivas para abrir
	CloseAfter int32 // sucessos consecutivos para fechar
}

// Default é a histerese da spec: rápido para abrir, lento para fechar.
var Default = Detector{OpenAfter: 3, CloseAfter: 5}

// Evaluate aplica um check ao estado e devolve o estado novo e a transição.
//
// Diverge do contrato da spec §7.3 em dois pontos, registrados no plano: os
// contadores são atualizados AQUI, e não por quem chama (assim a regra
// inteira fica num lugar só e testável sem banco), e não há Window, porque
// a histerese por contagem não precisa de janela.
//
// Um check com At igual ou anterior ao último avaliado é ignorado: o estado
// volta intacto e a ação é ActionNone. Isso acontece quando duas sondas do
// mesmo alvo estão em voo e a mais nova grava primeiro (M4 do Plano 2). Sem
// a guarda, um sucesso velho chegando atrasado zeraria uma sequência de
// falhas mais recente.
func (d Detector) Evaluate(s State, r ProbeResult) (State, Action) {
	if !s.LastCheckedAt.IsZero() && !r.At.After(s.LastCheckedAt) {
		return s, ActionNone
	}
	s.LastCheckedAt = r.At

	if r.Down {
		if s.ConsecutiveFailures == 0 {
			s.StreakSince = r.At
		}
		s.ConsecutiveFailures++
		s.ConsecutiveSuccesses = 0
		if !s.Open && s.ConsecutiveFailures >= d.OpenAfter {
			s.Open = true
			return s, ActionOpen
		}
		return s, ActionNone
	}

	if s.ConsecutiveSuccesses == 0 {
		s.StreakSince = r.At
	}
	s.ConsecutiveSuccesses++
	s.ConsecutiveFailures = 0
	if s.Open && s.ConsecutiveSuccesses >= d.CloseAfter {
		s.Open = false
		return s, ActionClose
	}
	return s, ActionNone
}

// Eventos da outbox de notificações (notifications.event).
const (
	EventOpened   = "opened"
	EventResolved = "resolved"
)
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race -count=1 -cover ./internal/incident/`
Expected: PASS. Cobertura ≥ 80% (só `Action.String` fica de fora).

- [ ] **Step 5: Sabotagem — a guarda de fora de ordem**

Troque a primeira linha de `Evaluate` por `if false && !s.LastCheckedAt.IsZero() && !r.At.After(s.LastCheckedAt) {`. Rode `go test ./internal/incident/`.
Expected: FAIL em `TestOutOfOrderCheckIsIgnored`, com "check velho mudou o estado". Restaure e confirme PASS. Registre no relatório.

- [ ] **Step 6: Commit**

```bash
git add internal/incident/
git commit -m "feat(incident): detector com histerese 3/5 e checks fora de ordem ignorados"
```

---
### Task 2: Schema de incidentes e notificações, sqlc e o escopo `verify`

**Files:**
- Create: `internal/storage/migrations/00005_incidents_notifications.sql`
- Create: `internal/storage/queries/incidents.sql`, `internal/storage/queries/channels.sql`, `internal/storage/queries/notifications.sql`
- Modify: `internal/storage/queries/prober.sql` (`ClaimDueTargets` devolve `user_id`)
- Modify: `internal/storage/scope.go` (`InVerifyTx`, `ErrNoToken`)
- Generated: `internal/storage/db/*.go` (`sqlc generate`)
- Modify: `internal/storage/helpers_test.go` (quatro helpers novos)
- Test: `internal/storage/notify_rls_test.go`

**Interfaces:**
- Consumes: `storage.InUserTx`, `storage.InProberTx` e os helpers de teste do Plano 2 (`novoUsuario`, `novoAlvo`, `contarComEscopo`, `codigoPG`).
- Produces (gerados pelo sqlc, em `db`):
  - Modelos `db.TargetHealth`, `db.Incident`, `db.NotifChannel`, `db.Notification`. `db.ClaimDueTargetsRow` ganha `UserID uuid.UUID`.
  - Detecção: `EnsureTargetHealth(ctx, targetID)`, `LockTargetHealth(ctx, targetID) (TargetHealth, error)`, `SaveTargetHealth(ctx, SaveTargetHealthParams{TargetID, ConsecutiveFailures, ConsecutiveSuccesses int32, StreakSince *time.Time, LastCheckedAt time.Time})`, `GetOpenIncident(ctx, targetID) (Incident, error)`, `OpenIncident(ctx, OpenIncidentParams{ID, TargetID, StartedAt, Cause})`, `ResolveIncident(ctx, ResolveIncidentParams{ResolvedAt time.Time, ID}) (int64, error)`, `MarkOpenNotified`/`MarkCloseNotified(ctx, {Now time.Time, ID}) (int64, error)`, `ListDeliverableChannels(ctx, userID) ([]uuid.UUID, error)`, `EnqueueNotification(ctx, EnqueueNotificationParams{ID, IncidentID, ChannelID uuid.UUID; Event string; Now time.Time})`.
  - Reconciliação: `CountOpenIncidents(ctx) (int64, error)`, `ExistingTargetIDs(ctx, ids []uuid.UUID) ([]uuid.UUID, error)`.
  - Histórico: `ListIncidents(ctx, ListIncidentsParams{UserID; Open *bool; Before *uuid.UUID; MaxRows int32}) ([]ListIncidentsRow, error)`, `AckIncident(ctx, AckIncidentParams{Now time.Time; UserID, ID uuid.UUID}) (AckIncidentRow, error)`. As duas linhas têm `ID, TargetID, TargetName, StartedAt, ResolvedAt *time.Time, Cause, AckAt *time.Time`.
  - Canais: `LockUser(ctx, id)`, `CountChannels(ctx, userID) (int64, error)`, `InsertChannel(ctx, InsertChannelParams{ID, UserID, Kind, ConfigEnc, VerifiedAt *time.Time, VerifyTokenHash []byte, VerifyExpiresAt *time.Time, CreatedAt})`, `ListChannels(ctx, userID) ([]NotifChannel, error)`, `GetChannel(ctx, GetChannelParams{UserID, ID}) (NotifChannel, error)`, `DeleteChannel(ctx, DeleteChannelParams{UserID, ID}) (int64, error)`, `RenewVerification(ctx, RenewVerificationParams{TokenHash []byte; ExpiresAt time.Time; UserID, ID uuid.UUID}) (int64, error)`, `FindChannelByVerification(ctx, tokenHash []byte) (FindChannelByVerificationRow{ID, VerifiedAt, VerifyExpiresAt *time.Time}, error)`, `ConfirmChannel(ctx, ConfirmChannelParams{Now time.Time; TokenHash []byte}) (int64, error)`.
  - Outbox: `ClaimDueNotifications(ctx, ClaimDueNotificationsParams{LeaseUntil, Now time.Time; MaxRows int32}) ([]ClaimDueNotificationsRow, error)`, com `ID, Event, Attempts int32, ChannelID, UserID, Kind, ConfigEnc, Enabled, VerifiedAt *time.Time, IncidentID, StartedAt, ResolvedAt *time.Time, Cause, TargetName`. Também `MarkNotificationSent(ctx, {Now, ID})`, `RetryNotification(ctx, {ID, NextAttemptAt time.Time, LastError *string})`, `CloseNotification(ctx, {ID, Status string, LastError *string})` e `InsertAudit(ctx, InsertAuditParams{ID; UserID *uuid.UUID; Action string; Entity *string; EntityID *uuid.UUID; At time.Time})`.
  - `storage.InVerifyTx(ctx, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error` e `storage.ErrNoToken`.

**Contexto para quem implementa.** Três armadilhas provadas em spike, que não é preciso redescobrir:

1. **No escopo `verify`, anular `verify_token_hash` falha.** Num `UPDATE`, a linha **nova** também passa pela política de SELECT, e sem o hash ela deixa de casar: "new row violates row-level security policy". Por isso `ConfirmChannel` mantém o hash e é idempotente (D3).
2. **A política de verify diz QUAL linha, não QUAIS colunas.** Nesse escopo, um `UPDATE` arbitrário mudaria o `user_id` da linha do token. A trava é haver uma query só que roda nele. Não acrescente outra.
3. **O sqlc gera ponteiro para parâmetro de coluna anulável.** Onde a query recebe um instante que nunca é nulo, o SQL usa `sqlc.arg(x)::timestamptz`, e o campo sai `time.Time`.

Mais uma: o `gosec` G101 dispara em constante gerada cujo nome tenha "Token". Por isso as queries se chamam `RenewVerification` e `FindChannelByVerification` (D23).

- [ ] **Step 1: Escrever os helpers e os testes (falham)**

Acrescente ao fim de `internal/storage/helpers_test.go`:

```go
// novoCanal cria um canal do dono. tokenHash nil cria um Discord já
// verificado; com hash, um e-mail pendente que vence em expira.
func novoCanal(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, tokenHash []byte, expira time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	p := db.InsertChannelParams{ID: id, UserID: dono, Kind: "discord", ConfigEnc: []byte{1}, CreatedAt: agora}
	if tokenHash == nil {
		p.VerifiedAt = &agora
	} else {
		p.Kind, p.VerifyTokenHash, p.VerifyExpiresAt = "email", tokenHash, &expira
	}
	if err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) }); err != nil {
		t.Fatalf("criar canal: %v", err)
	}
	return id
}

func novoIncidente(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "timeout"})
	})
	if err != nil {
		t.Fatalf("abrir incidente: %v", err)
	}
	return id
}

func novaNotificacao(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: "opened", Now: agora,
		})
	})
	if err != nil {
		t.Fatalf("enfileirar notificação: %v", err)
	}
	return id
}

func novoID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
```

Crie `internal/storage/notify_rls_test.go`:

```go
package storage_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// target_health e incidents não têm user_id: a RLS delega ao alvo, como em
// checks. Quem não enxerga o alvo não enxerga nem cria nada dele.
func TestIncidentsAndHealthFollowTheTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	novoIncidente(t, pool, alvoA)
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.EnsureTargetHealth(ctx, alvoA) }); err != nil {
		t.Fatal(err)
	}

	for _, tabela := range []string{"incidents", "target_health"} {
		if n := contarComEscopo(t, pool, b, tabela); n != 0 {
			t.Errorf("B viu %d linhas de %s do alvo de A", n, tabela)
		}
		if n := contarComEscopo(t, pool, a, tabela); n != 1 {
			t.Errorf("A viu %d linhas de %s, queria 1", n, tabela)
		}
	}

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: novoID(), TargetID: alvoB, StartedAt: agora, Cause: "dns"})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A abrindo incidente no alvo de B deu %v, queria 42501 (RLS)", err)
	}
}

// O detector não abre dois, e o índice parcial é a garantia que não depende
// do código estar certo.
func TestOnlyOneOpenIncidentPerTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alvo := novoAlvo(t, pool, novoUsuario(t, pool), agora)
	primeiro := novoIncidente(t, pool, alvo)

	abrir := func() error {
		return storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.OpenIncident(ctx, db.OpenIncidentParams{ID: novoID(), TargetID: alvo, StartedAt: agora, Cause: "dns"})
		})
	}
	if err := abrir(); codigoPG(err) != "23505" {
		t.Fatalf("segundo incidente aberto no mesmo alvo deu %v, queria 23505", err)
	}

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: primeiro, ResolvedAt: agora.Add(time.Minute)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := abrir(); err != nil {
		t.Errorf("depois de resolver o primeiro, abrir outro deu %v", err)
	}
}

// O dono vê só os seus canais. O sondador lê os de todos para o fan-out e
// não escreve nenhum.
func TestChannelsAreIsolatedAndProberOnlyReads(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, time.Time{})
	novoCanal(t, pool, b, nil, time.Time{})

	if n := contarComEscopo(t, pool, a, "notif_channels"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d canais, queria 1", n)
	}

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		ids, err := q.ListDeliverableChannels(ctx, a)
		if err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] != canalA {
			t.Errorf("fan-out de A = %v, queria [%s]", ids, canalA)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: novoID(), UserID: a, Kind: "discord", ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o sondador criando canal deu %v, queria 42501", err)
	}
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: a, ID: canalA})
		if n != 0 {
			t.Errorf("o sondador apagou %d canais", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hashDe(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// O escopo de verificação só existe com o token: enxerga a linha daquele
// hash e mais nada, nem a linha de outro token pedida pelo nome.
func TestVerifyScopeSeesOnlyTheTokenRow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hA, hB, hVencido := hashDe("token-a"), hashDe("token-b"), hashDe("token-vencido")
	canalA := novoCanal(t, pool, a, hA, agora.Add(24*time.Hour))
	novoCanal(t, pool, b, hB, agora.Add(24*time.Hour))
	novoCanal(t, pool, b, hVencido, agora.Add(-time.Minute))
	novoAlvo(t, pool, a, agora)

	err := storage.InVerifyTx(ctx, pool, hA, func(q *db.Queries) error {
		row, err := q.FindChannelByVerification(ctx, hA)
		if err != nil || row.ID != canalA {
			t.Errorf("buscar pelo próprio token deu %v, %v", row.ID, err)
		}
		// A linha de B existe e o WHERE casa com ela: só a política a esconde.
		if _, err := q.FindChannelByVerification(ctx, hB); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("com o token de A, buscar o de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.verify_token_hash', encode($1::bytea, 'hex'), true)`, hA); err != nil {
		t.Fatal(err)
	}
	for tabela, quer := range map[string]int{"notif_channels": 1, "targets": 0} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != quer {
			t.Errorf("no escopo de verificação, %s mostrou %d linhas, queria %d", tabela, n, quer)
		}
	}
}

// Confirmar é idempotente, e token vencido não confirma nada.
func TestConfirmChannelIsIdempotentAndRespectsExpiry(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	h, hVencido := hashDe("token"), hashDe("vencido")
	novoCanal(t, pool, dono, h, agora.Add(24*time.Hour))
	novoCanal(t, pool, dono, hVencido, agora.Add(-time.Minute))

	confirmar := func(hash []byte, em time.Time) int64 {
		t.Helper()
		var n int64
		err := storage.InVerifyTx(ctx, pool, hash, func(q *db.Queries) error {
			var err error
			n, err = q.ConfirmChannel(ctx, db.ConfirmChannelParams{TokenHash: hash, Now: em})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := confirmar(h, agora); n != 1 {
		t.Fatalf("primeira confirmação afetou %d linhas", n)
	}
	// Depois do vencimento, o canal JÁ verificado continua confirmando.
	if n := confirmar(h, agora.Add(48*time.Hour)); n != 1 {
		t.Errorf("segunda confirmação afetou %d linhas, queria 1 (idempotente)", n)
	}
	if n := confirmar(hVencido, agora); n != 0 {
		t.Errorf("token vencido confirmou %d linhas", n)
	}

	var verificadoEm time.Time
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		cs, err := q.ListChannels(ctx, dono)
		for _, c := range cs {
			if string(c.VerifyTokenHash) == string(h) && c.VerifiedAt != nil {
				verificadoEm = *c.VerifiedAt
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verificadoEm.Equal(agora) {
		t.Errorf("verified_at = %v, queria o instante da PRIMEIRA confirmação %v", verificadoEm, agora)
	}
}

func TestInVerifyTxRefusesAMalformedHash(t *testing.T) {
	pool := testdb.New(t)
	err := storage.InVerifyTx(context.Background(), pool, nil, func(*db.Queries) error { return nil })
	if !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("hash vazio deu %v, queria ErrNoToken", err)
	}
}

// A outbox delega ao canal. O sondador reivindica, e a reivindicação é um
// lease: a mesma notificação não volta antes de ele vencer.
func TestNotificationClaimIsALease(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	n := novaNotificacao(t, pool, novoIncidente(t, pool, alvo), novoCanal(t, pool, a, nil, time.Time{}))

	if v := contarComEscopo(t, pool, b, "notifications"); v != 0 {
		t.Errorf("B viu %d notificações de A", v)
	}

	reivindicar := func(em time.Time) []db.ClaimDueNotificationsRow {
		t.Helper()
		var rows []db.ClaimDueNotificationsRow
		err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			var err error
			rows, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
				Now: em, LeaseUntil: em.Add(2 * time.Minute), MaxRows: 8,
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}

	rows := reivindicar(agora)
	if len(rows) != 1 {
		t.Fatalf("reivindicou %d, queria 1", len(rows))
	}
	r := rows[0]
	if r.ID != n || r.Attempts != 1 || r.UserID != a || r.TargetName != "alvo" || r.Cause != "timeout" || r.Kind != "discord" {
		t.Errorf("linha reivindicada = %+v", r)
	}
	if again := reivindicar(agora.Add(time.Minute)); len(again) != 0 {
		t.Errorf("antes do lease vencer, reivindicou de novo %d", len(again))
	}
	if again := reivindicar(agora.Add(3 * time.Minute)); len(again) != 1 || again[0].Attempts != 2 {
		t.Errorf("depois do lease, reivindicou %+v; queria a mesma, na tentativa 2", again)
	}
}

// Um segundo dispatcher pega OUTRAS notificações em vez de esperar pelas que
// o primeiro travou. A trava é segurada de propósito numa transação aberta:
// sem sobreposição de verdade, o teste passaria mesmo sem SKIP LOCKED.
func TestNotificationClaimSkipsLockedRows(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	inc := novoIncidente(t, pool, novoAlvo(t, pool, a, agora))
	travada := novaNotificacao(t, pool, inc, novoCanal(t, pool, a, nil, time.Time{}))
	livre := novaNotificacao(t, pool, inc, novoCanal(t, pool, a, nil, time.Time{}))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM notifications WHERE id = $1 FOR UPDATE`, travada); err != nil {
		t.Fatal(err)
	}

	prazo, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var rows []db.ClaimDueNotificationsRow
	err = storage.InProberTx(prazo, pool, func(q *db.Queries) error {
		var err error
		rows, err = q.ClaimDueNotifications(prazo, db.ClaimDueNotificationsParams{
			Now: agora, LeaseUntil: agora.Add(2 * time.Minute), MaxRows: 8,
		})
		return err
	})
	if err != nil {
		t.Fatalf("reivindicação esperou a trava em vez de pular: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != livre {
		t.Errorf("reivindicou %+v, queria só a livre %s", rows, livre)
	}
}

// Dívida do Plano 2: dois boots concorrentes criando a mesma partição. Com
// a advisory lock, a segunda chamada ESPERA a primeira. O teste segura a
// trava numa sessão, confirma no pg_stat_activity que a chamada está
// parada nela, e só então solta.
func TestPartitionCreationWaitsForTheAdvisoryLock(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	trava, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer trava.Release()
	const chave = `hashtextextended('vigilume.ensure_checks_partition', 0)`
	if _, err := trava.Exec(ctx, `SELECT pg_advisory_lock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}

	fim := make(chan error, 1)
	go func() {
		fim <- storage.EnsureChecksPartitions(ctx, pool, time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC))
	}()

	esperando := false
	for range 50 {
		var n int
		err := trava.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND wait_event = 'advisory'
			  AND query LIKE '%ensure_checks_partition%'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			esperando = true
			break
		}
		select {
		case err := <-fim:
			t.Fatalf("EnsureChecksPartitions terminou (%v) com a trava segurada: a função não usa a advisory lock", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !esperando {
		t.Fatal("a chamada nunca apareceu esperando a advisory lock")
	}

	if _, err := trava.Exec(ctx, `SELECT pg_advisory_unlock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fim:
		if err != nil {
			t.Fatalf("depois de soltar a trava: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a chamada não terminou depois de a trava ser solta")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/storage/`
Expected: FAIL de compilação: `undefined: db.InsertChannelParams`, `undefined: storage.InVerifyTx` e afins.

- [ ] **Step 3: Escrever a migration**

Crie `internal/storage/migrations/00005_incidents_notifications.sql`:

```sql
-- +goose Up
-- Histerese por alvo (spec §7): uma linha por alvo, criada na primeira
-- sonda gravada. Vive fora de targets para que a gravação do sondador não
-- dispute a linha do alvo com o PATCH do dono.
CREATE TABLE target_health (
    target_id             uuid PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,
    consecutive_failures  int NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    consecutive_successes int NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0),
    streak_since          timestamptz,
    last_checked_at       timestamptz
);

-- Sem user_id: a RLS delega ao alvo, como em checks (migration 00004).
ALTER TABLE target_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE target_health FORCE ROW LEVEL SECURITY;
CREATE POLICY target_health_via_target ON target_health
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));

-- Incidentes (spec §4.1). cause usa o mesmo enum de checks.error_kind.
CREATE TABLE incidents (
    id                uuid PRIMARY KEY,
    target_id         uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    started_at        timestamptz NOT NULL,
    resolved_at       timestamptz,
    cause             text NOT NULL CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused',
                                                     'http_status', 'body_limit', 'blocked', 'unknown')),
    ack_at            timestamptz,
    -- Instante em que o fan-out da abertura e o do fechamento foram
    -- enfileirados em notifications (spec §8, D6 do Plano 3).
    open_notified_at  timestamptz,
    close_notified_at timestamptz,
    CHECK (resolved_at IS NULL OR resolved_at >= started_at)
);

-- No máximo um incidente aberto por alvo. O detector já não abre dois, e
-- o índice é a garantia que não depende do código estar certo.
CREATE UNIQUE INDEX incidents_one_open_per_target ON incidents (target_id) WHERE resolved_at IS NULL;
CREATE INDEX incidents_target_idx ON incidents (target_id, id DESC);

ALTER TABLE incidents ENABLE ROW LEVEL SECURITY;
ALTER TABLE incidents FORCE ROW LEVEL SECURITY;
CREATE POLICY incidents_via_target ON incidents
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id));

-- Canais de notificação (spec §4.1, §5.8). config_enc guarda a URL do
-- webhook ou o endereço de e-mail, cifrados com AAD da linha (spec §5.4).
CREATE TABLE notif_channels (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind              text NOT NULL CHECK (kind IN ('discord', 'email')),
    config_enc        bytea NOT NULL,
    verified_at       timestamptz,
    verify_token_hash bytea UNIQUE,
    verify_expires_at timestamptz,
    enabled           bool NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL,
    CHECK ((verify_token_hash IS NULL) = (verify_expires_at IS NULL)),
    -- Só e-mail tem token: o Discord é verificado pela mensagem de teste.
    CHECK (kind = 'email' OR verify_token_hash IS NULL)
);

CREATE INDEX notif_channels_user_idx ON notif_channels (user_id, id DESC);

ALTER TABLE notif_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE notif_channels FORCE ROW LEVEL SECURITY;

CREATE POLICY notif_channels_owner ON notif_channels
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O sondador (e o dispatcher, que roda no mesmo papel) lê canais para o
-- fan-out e para o envio, e nada mais: sem INSERT, UPDATE nem DELETE.
CREATE POLICY notif_channels_prober_select ON notif_channels FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');

-- Escopo de verificação: GET/POST /verify/{token} não tem usuário, só o
-- token. A transação liga app.verify_token_hash (storage.InVerifyTx) e
-- enxerga exatamente a linha daquele hash. decode(NULL) é NULL, então sem o
-- GUC, ou com ele vazio numa conexão reusada, a política não casa nada.
--
-- A confirmação MANTÉM o hash na linha (query ConfirmChannel). Anulá-lo
-- aqui não funciona: num UPDATE, a linha NOVA também precisa passar pela
-- política de SELECT, e uma linha sem hash deixa de casar. O Postgres
-- responde "new row violates row-level security policy" (provado em spike).
-- O uso único fica por conta de verified_at.
--
-- Limite conhecido: a política diz QUAL linha, não QUAIS colunas. Neste
-- escopo, um UPDATE arbitrário poderia mudar o user_id da linha do token.
-- A trava é existir uma query só que roda nele.
CREATE POLICY notif_channels_verify_select ON notif_channels FOR SELECT
    USING (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'));
CREATE POLICY notif_channels_verify_update ON notif_channels FOR UPDATE
    USING (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'))
    WITH CHECK (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'));

-- Outbox das notificações (decisão do usuário, 2026-09-24): o sondador
-- grava aqui, na mesma transação que abre ou fecha o incidente, e o
-- dispatcher consome com SKIP LOCKED. Sobrevive a restart e funciona com
-- --role=api e --role=prober separados.
CREATE TABLE notifications (
    id              uuid PRIMARY KEY,
    incident_id     uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES notif_channels(id) ON DELETE CASCADE,
    event           text NOT NULL CHECK (event IN ('opened', 'resolved')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'cancelled')),
    attempts        int  NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- Próxima tentativa. Enquanto um dispatcher envia, vale como lease: a
    -- reivindicação o empurra para o futuro, e um processo que morre no
    -- meio devolve a notificação à fila quando o lease vence.
    next_attempt_at timestamptz NOT NULL,
    last_error      text CHECK (last_error IN ('timeout', 'network', 'rate_limited', 'rejected',
                                               'server_error', 'invalid_channel', 'unknown')),
    created_at      timestamptz NOT NULL,
    sent_at         timestamptz,
    -- Spec §8: no máximo uma notificação por incidente, canal e evento.
    UNIQUE (incident_id, channel_id, event)
);

CREATE INDEX notifications_due_idx ON notifications (next_attempt_at) WHERE status = 'pending';

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
CREATE POLICY notifications_via_channel ON notifications
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id));

-- Dívida do Plano 2: CREATE TABLE IF NOT EXISTS ... PARTITION OF não é
-- seguro com dois boots concorrentes (os dois veem "não existe" e o segundo
-- cai com 42P07). A advisory lock de transação serializa as chamadas; ela
-- é solta no fim do comando que chamou a função.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('vigilume.ensure_checks_partition', 0));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
END
$$;
-- +goose StatementEnd
DROP TABLE notifications;
DROP TABLE notif_channels;
DROP TABLE incidents;
DROP TABLE target_health;
```

- [ ] **Step 4: Escrever as queries**

Crie `internal/storage/queries/incidents.sql`:

```sql
-- Detecção (spec §7): estas rodam só sob InProberTx, na MESMA transação do
-- INSERT do check (prober.Scheduler.avaliar).

-- name: EnsureTargetHealth :exec
INSERT INTO target_health (target_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- A trava da linha serializa duas sondas do mesmo alvo gravando ao mesmo
-- tempo (M4 do Plano 2): a segunda lê o estado que a primeira gravou.
-- name: LockTargetHealth :one
SELECT * FROM target_health WHERE target_id = $1 FOR UPDATE;

-- name: SaveTargetHealth :exec
UPDATE target_health
   SET consecutive_failures = $2, consecutive_successes = $3,
       streak_since = $4, last_checked_at = sqlc.arg(last_checked_at)::timestamptz
 WHERE target_id = $1;

-- name: GetOpenIncident :one
SELECT * FROM incidents WHERE target_id = $1 AND resolved_at IS NULL;

-- name: OpenIncident :exec
INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, $4);

-- name: ResolveIncident :execrows
UPDATE incidents SET resolved_at = sqlc.arg(resolved_at)::timestamptz
 WHERE id = sqlc.arg(id) AND resolved_at IS NULL;

-- Idempotência no banco (spec §8): a corrida é resolvida pelo Postgres.
-- Quem vê 0 linhas não enfileira.
-- name: MarkOpenNotified :execrows
UPDATE incidents SET open_notified_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id) AND open_notified_at IS NULL;

-- name: MarkCloseNotified :execrows
UPDATE incidents SET close_notified_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id) AND close_notified_at IS NULL;

-- Fan-out (spec §8, §5.8): só canais habilitados E verificados.
-- name: ListDeliverableChannels :many
SELECT id FROM notif_channels
 WHERE user_id = $1 AND enabled AND verified_at IS NOT NULL
 ORDER BY id;

-- name: EnqueueNotification :exec
INSERT INTO notifications (id, incident_id, channel_id, event, next_attempt_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(incident_id), sqlc.arg(channel_id), sqlc.arg(event),
        sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (incident_id, channel_id, event) DO NOTHING;

-- Reconciliação das métricas (D9/M1 do Plano 2).
-- name: CountOpenIncidents :one
SELECT count(*) FROM incidents WHERE resolved_at IS NULL;

-- name: ExistingTargetIDs :many
SELECT id FROM targets WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- Histórico (API): rodam sob InUserTx e filtram por user_id, como as de
-- targets.sql. open NULL lista tudo; true só abertos; false só resolvidos.
-- O id é UUIDv7, gerado quando o incidente abre: ordenar por ele é ordenar
-- pela abertura, e o cursor é o último id da página anterior.
-- name: ListIncidents :many
SELECT i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at
  FROM incidents i
  JOIN targets t ON t.id = i.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND (sqlc.narg(open)::bool IS NULL OR (i.resolved_at IS NULL) = sqlc.narg(open)::bool)
   AND (sqlc.narg(before)::uuid IS NULL OR i.id < sqlc.narg(before)::uuid)
 ORDER BY i.id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- Reconhecer é idempotente: o COALESCE preserva o primeiro ack.
-- name: AckIncident :one
UPDATE incidents i
   SET ack_at = COALESCE(i.ack_at, sqlc.arg(now)::timestamptz)
  FROM targets t
 WHERE t.id = i.target_id AND t.user_id = sqlc.arg(user_id) AND i.id = sqlc.arg(id)
RETURNING i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at;
```

Crie `internal/storage/queries/channels.sql`:

```sql
-- Canais (spec §8, §5.8). As desta seção rodam sob InUserTx e filtram por
-- user_id; as duas do fim rodam sob InVerifyTx.

-- Serializa as criações de canal do mesmo usuário, para que o limite de
-- canais valha também contra duas requisições simultâneas. users não tem
-- RLS (D7 do Plano 2).
-- name: LockUser :exec
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: CountChannels :one
SELECT count(*) FROM notif_channels WHERE user_id = $1;

-- name: InsertChannel :exec
INSERT INTO notif_channels (id, user_id, kind, config_enc, verified_at,
                            verify_token_hash, verify_expires_at, enabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8);

-- Sem paginação: o limite por usuário é pequeno (notify.MaxChannels).
-- name: ListChannels :many
SELECT * FROM notif_channels WHERE user_id = $1 ORDER BY id DESC;

-- name: GetChannel :one
SELECT * FROM notif_channels WHERE user_id = $1 AND id = $2;

-- name: DeleteChannel :execrows
DELETE FROM notif_channels WHERE user_id = $1 AND id = $2;

-- Reenvio: troca o token. O antigo deixa de casar com qualquer linha.
-- name: RenewVerification :execrows
UPDATE notif_channels
   SET verify_token_hash = sqlc.arg(token_hash), verify_expires_at = sqlc.arg(expires_at)::timestamptz
 WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id)
   AND kind = 'email' AND verified_at IS NULL;

-- name: FindChannelByVerification :one
SELECT id, verified_at, verify_expires_at FROM notif_channels
 WHERE verify_token_hash = sqlc.arg(token_hash);

-- Idempotente: confirmar de novo um canal já verificado não muda nada e
-- afeta a linha, então o segundo clique também vê sucesso. Token vencido e
-- ainda não usado não afeta nada.
-- name: ConfirmChannel :execrows
UPDATE notif_channels
   SET verified_at = COALESCE(verified_at, sqlc.arg(now)::timestamptz)
 WHERE verify_token_hash = sqlc.arg(token_hash)
   AND (verified_at IS NOT NULL OR verify_expires_at > sqlc.arg(now)::timestamptz);
```

Crie `internal/storage/queries/notifications.sql`:

```sql
-- Dispatcher (spec §8). Rodam só sob InProberTx.

-- Reivindicação com lease, no mesmo molde de ClaimDueTargets: o UPDATE
-- empurra next_attempt_at para lease_until e conta a tentativa na mesma
-- instrução que trava as linhas, e o commit acontece antes do envio. A
-- CTE devolve, na mesma ida ao banco, tudo o que o envio precisa.
-- name: ClaimDueNotifications :many
WITH claimed AS (
    UPDATE notifications n
       SET next_attempt_at = sqlc.arg(lease_until)::timestamptz, attempts = n.attempts + 1
     WHERE n.id IN (
           SELECT id FROM notifications
            WHERE status = 'pending' AND next_attempt_at <= sqlc.arg(now)::timestamptz
            ORDER BY next_attempt_at
            LIMIT sqlc.arg(max_rows)::int
              FOR UPDATE SKIP LOCKED)
    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts
)
SELECT cl.id, cl.event, cl.attempts,
       c.id AS channel_id, c.user_id, c.kind, c.config_enc, c.enabled, c.verified_at,
       i.id AS incident_id, i.started_at, i.resolved_at, i.cause,
       t.name AS target_name
  FROM claimed cl
  JOIN notif_channels c ON c.id = cl.channel_id
  JOIN incidents i ON i.id = cl.incident_id
  JOIN targets t ON t.id = i.target_id;

-- name: MarkNotificationSent :exec
UPDATE notifications SET status = 'sent', sent_at = sqlc.arg(now)::timestamptz, last_error = NULL
 WHERE id = sqlc.arg(id);

-- name: RetryNotification :exec
UPDATE notifications SET next_attempt_at = $2, last_error = $3 WHERE id = $1;

-- status é 'failed' ou 'cancelled'.
-- name: CloseNotification :exec
UPDATE notifications SET status = $2, last_error = $3 WHERE id = $1;

-- Falha definitiva vai para a trilha de auditoria (spec §8). audit_log não
-- tem RLS (D7 do Plano 2).
-- name: InsertAudit :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, at) VALUES ($1, $2, $3, $4, $5, $6);
```

Em `internal/storage/queries/prober.sql`, troque o bloco de `ClaimDueTargets` (do comentário "SKIP LOCKED faz" até o `RETURNING`) por:

```sql
-- SKIP LOCKED faz um segundo sondador pegar OUTROS alvos em vez de esperar
-- pelos que o primeiro travou (TestSkipLockedDoesNotWaitForAnotherProber).
--
-- user_id volta porque a avaliação de incidente enfileira as notificações
-- nos canais do dono (Plano 3).
-- name: ClaimDueTargets :many
UPDATE targets t
   SET next_run_at = sqlc.arg(now)::timestamptz
       + make_interval(secs => t.interval_sec * (0.9 + random() * 0.2))
 WHERE t.id IN (
       SELECT id FROM targets
        WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz
        ORDER BY next_run_at
        LIMIT sqlc.arg(max_rows)::int
          FOR UPDATE SKIP LOCKED)
RETURNING t.id, t.user_id, t.url, t.method, t.timeout_ms, t.expected_status,
          t.headers_enc, t.degraded_threshold_ms;
```

- [ ] **Step 5: Gerar o código**

Run: `sqlc generate && go build ./...`
Expected: sem saída de erro. Aparecem `internal/storage/db/incidents.sql.go`, `channels.sql.go` e `notifications.sql.go`, e `models.go` ganha os quatro modelos. O sondador continua compilando: `ClaimDueTargetsRow` só ganhou um campo.

- [ ] **Step 6: Escrever `InVerifyTx`**

Em `internal/storage/scope.go`, acrescente `"crypto/sha256"` e `"encoding/hex"` aos imports e troque a linha `var ErrNoUser = ...` por:

```go
var (
	ErrNoUser  = errors.New("transação de usuário sem usuário")
	ErrNoToken = errors.New("transação de verificação sem hash de token")
)
```

Acrescente, logo antes de `func inTx(`:

```go
// InVerifyTx roda fn no escopo de verificação de canal (GET/POST
// /verify/{token}): a RLS enxerga só a linha de notif_channels cujo
// verify_token_hash é tokenHash, e nada de outra tabela. É o único lugar do
// código que liga esse escopo.
//
// O hash vai em hexadecimal porque GUC é texto; a política o decodifica.
func InVerifyTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error {
	if len(tokenHash) != sha256.Size {
		// Um hash vazio casaria com nada, e o erro apareceria longe daqui
		// como "link inválido". Um tamanho errado é defeito de quem chama.
		return ErrNoToken
	}
	return inTx(ctx, b, `SELECT set_config('app.verify_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
}
```

- [ ] **Step 7: Rodar e ver passar**

Run: `go test -race -count=1 ./internal/storage/ ./internal/prober/ ./cmd/...`
Expected: PASS. Os testes do Plano 2 continuam verdes com a migration nova.

- [ ] **Step 8: Sabotagem — três guardas, uma de cada vez**

Restaure depois de cada uma, com `sqlc generate` quando mexer em SQL:

1. Na migration, comente a linha `PERFORM pg_advisory_xact_lock(...)` da função **Up**. Rode `go test -count=1 -run TestPartitionCreationWaits ./internal/storage/`. Expected: FAIL com "terminou (<nil>) com a trava segurada".
2. Em `notifications.sql`, troque `FOR UPDATE SKIP LOCKED` por `FOR UPDATE`; `sqlc generate`; rode `-run TestNotificationClaim`. Expected: FAIL com "reivindicação esperou a trava em vez de pular".
3. Na migration, troque o `USING` de `notif_channels_verify_select` por `USING (NULLIF(current_setting('app.verify_token_hash', true), '') IS NOT NULL)`; rode `-run TestVerifyScope`. Expected: FAIL com "buscar o de B deu <nil>" e "mostrou 3 linhas".

Registre as três no relatório.

- [ ] **Step 9: Conferir o gerado e commitar**

Run: `sqlc diff && go vet ./... && gosec -quiet ./...`
Expected: sem saída.

```bash
git add internal/storage/
git commit -m "feat(storage): incidentes, canais e outbox com RLS, escopo verify e advisory lock de partição"
```

---
### Task 3: Avaliação de incidente no `gravar` do sondador

O ponto de extensão que o Plano 2 deixou: depois do `INSERT` do check, na mesma transação, o sondador aplica a histerese, abre ou fecha o incidente e enfileira as notificações.

**Files:**
- Modify: `internal/prober/scheduler.go` (imports, `Scheduler.detector`, `newScheduler`, `sondar`, `gravar`; funções novas `avaliar`, `paraDetector`, `enfileirar`, `valorOuZero`)
- Test: `internal/prober/incident_test.go`

**Interfaces:**
- Consumes: `incident.Detector`, `incident.Default`, `incident.State`, `incident.ProbeResult`, `incident.Action*`, `incident.EventOpened`/`EventResolved` (Task 1); as queries de detecção e `db.ClaimDueTargetsRow.UserID` (Task 2).
- Produces:
  - `func (s *Scheduler) gravar(a db.ClaimDueTargetsRow, em time.Time, res Result)`: a assinatura muda, e recebe a linha inteira em vez do id.
  - `func (s *Scheduler) avaliar(ctx, q *db.Queries, a db.ClaimDueTargetsRow, em time.Time, res Result) (incident.Action, error)`: a Task 4 usa a ação devolvida.
  - `func paraDetector(em time.Time, r Result) incident.ProbeResult`.
  - Helpers de teste, no pacote `prober`: `(*bancada).rodada`, `canal`, `comoDono`, `incidentes`, `notificacoes` e `servidorAlternavel`. A Task 4 usa `rodada`.

**Contexto para quem implementa.** A transação é uma só: se a avaliação falhar, o check também não é gravado, e o erro vai ao log como antes. A trava em `target_health` (`LockTargetHealth`, `FOR UPDATE`) serializa duas sondas do mesmo alvo gravando juntas. O enfileiramento só acontece se `MarkOpenNotified`/`MarkCloseNotified` afetarem uma linha (spec §8). O `UNIQUE` da outbox é uma segunda guarda da mesma propriedade, e por isso nenhum teste isola a primeira: sabotar uma deixa a outra segurando.

Um teste de sondador **não** pode chamar `Tick` depois de `Stop`. O helper `rodada` espera `emVoo` diretamente, e é isso que deixa rodar várias rodadas no mesmo agendador.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/prober/incident_test.go`:

```go
package prober

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// rodada sonda os alvos vencidos, espera as sondas gravarem e avança o
// relógio além do intervalo (60s ± 10%), para que a próxima rodada os
// encontre vencidos de novo. Esperar emVoo, e não chamar Stop, mantém o
// agendador utilizável entre as rodadas.
func (b *bancada) rodada(t *testing.T) time.Time {
	t.Helper()
	em := b.clk.Now()
	if n, err := b.s.Tick(context.Background()); err != nil || n == 0 {
		t.Fatalf("Tick = %d, %v; queria ao menos um disparo", n, err)
	}
	b.s.emVoo.Wait()
	b.clk.Advance(2 * time.Minute)
	return em
}

// canal cria um canal do dono da bancada. O conteúdo cifrado não importa
// aqui: o sondador só enfileira, quem decifra é o dispatcher.
func (b *bancada) canal(t *testing.T, verificado, habilitado bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	agora := b.clk.Now()
	p := db.InsertChannelParams{ID: id, UserID: b.dono, Kind: "discord", ConfigEnc: []byte{1}, CreatedAt: agora}
	if verificado {
		p.VerifiedAt = &agora
	}
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) })
	if err != nil {
		t.Fatal(err)
	}
	if !habilitado {
		// Não há query de aplicação para desabilitar canal ainda; o teste
		// usa SQL direto, sob o escopo do dono.
		b.comoDono(t, func(tx pgx.Tx) {
			if _, err := tx.Exec(ctx, `UPDATE notif_channels SET enabled = false WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		})
	}
	return id
}

func (b *bancada) comoDono(t *testing.T, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func (b *bancada) incidentes(t *testing.T) []db.ListIncidentsRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListIncidentsRow
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListIncidents(ctx, db.ListIncidentsParams{UserID: b.dono, MaxRows: 100})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

type notificacao struct {
	Canal  uuid.UUID
	Evento string
}

func (b *bancada) notificacoes(t *testing.T) []notificacao {
	t.Helper()
	var out []notificacao
	b.comoDono(t, func(tx pgx.Tx) {
		rows, err := tx.Query(context.Background(), `SELECT channel_id, event FROM notifications ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[notificacao])
		if err != nil {
			t.Fatal(err)
		}
	})
	return out
}

// servidorAlternavel responde 500 enquanto fora for true, e 200 depois.
func servidorAlternavel(t *testing.T) (url string, client *http.Client, fora *atomic.Bool) {
	t.Helper()
	fora = &atomic.Bool{}
	fora.Store(true)
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		if fora.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return srv.URL, client, fora
}

// O caminho inteiro, pelo Tick: três falhas abrem o incidente, com início
// na PRIMEIRA falha, e enfileiram a abertura só no canal verificado e
// habilitado (spec §5.8, §8).
func TestThreeFailuresOpenAnIncidentAndEnqueueTheFanOut(t *testing.T) {
	url, client, _ := servidorAlternavel(t)
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, url, nil)
	entregavel := b.canal(t, true, true)
	b.canal(t, false, true) // pendente de verificação
	b.canal(t, true, false) // desabilitado

	primeira := b.rodada(t)
	b.rodada(t)
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Fatalf("duas falhas abriram %d incidentes", len(inc))
	}
	b.rodada(t)

	inc := b.incidentes(t)
	if len(inc) != 1 {
		t.Fatalf("três falhas abriram %d incidentes, queria 1", len(inc))
	}
	if !inc[0].StartedAt.Equal(primeira) || inc[0].ResolvedAt != nil || inc[0].Cause != "http_status" {
		t.Errorf("incidente = %+v; queria início %v (a primeira falha), aberto, causa http_status", inc[0], primeira)
	}
	ns := b.notificacoes(t)
	if len(ns) != 1 || ns[0].Canal != entregavel || ns[0].Evento != incident.EventOpened {
		t.Errorf("notificações = %+v; queria só a abertura no canal entregável %s", ns, entregavel)
	}

	// Falhas além da terceira não reabrem nem notificam de novo.
	b.rodada(t)
	if inc := b.incidentes(t); len(inc) != 1 {
		t.Errorf("a quarta falha deixou %d incidentes", len(inc))
	}
	if ns := b.notificacoes(t); len(ns) != 1 {
		t.Errorf("a quarta falha deixou %d notificações", len(ns))
	}
}

// Cinco sucessos fecham, com resolved_at no PRIMEIRO deles, e enfileiram o
// fechamento. Quatro não bastam.
func TestFiveSuccessesResolveTheIncident(t *testing.T) {
	url, client, fora := servidorAlternavel(t)
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, url, nil)
	b.canal(t, true, true)
	for range 3 {
		b.rodada(t)
	}

	fora.Store(false)
	voltou := b.rodada(t)
	for range 3 {
		b.rodada(t)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Fatalf("quatro sucessos: incidentes = %+v, queria o mesmo, ainda aberto", inc)
	}
	b.rodada(t)

	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt == nil || !inc[0].ResolvedAt.Equal(voltou) {
		t.Fatalf("cinco sucessos: incidentes = %+v; queria resolvido em %v (o primeiro sucesso)", inc, voltou)
	}
	ns := b.notificacoes(t)
	if len(ns) != 2 || ns[1].Evento != incident.EventResolved {
		t.Errorf("notificações = %+v; queria abertura e fechamento", ns)
	}
}

// Só down conta como falha: degraded é estado de exibição (spec §7.2).
func TestOnlyDownCountsAsFailure(t *testing.T) {
	em := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for status, down := range map[Status]bool{StatusUp: false, StatusDegraded: false, StatusDown: true} {
		if got := paraDetector(em, Result{Status: status}); got.Down != down || !got.At.Equal(em) {
			t.Errorf("%s virou %+v, queria Down=%v", status, got, down)
		}
	}
}

// M4 do Plano 2, pelo caminho de gravação: duas sondas do mesmo alvo em voo
// gravam fora de ordem. O sucesso velho fica no histórico de checks, mas não
// zera a sequência de falhas mais nova.
func TestOutOfOrderCheckDoesNotResetTheFailureStreak(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono}
	falha := Result{Status: StatusDown, ErrorKind: KindTimeout}
	t0 := b.clk.Now()

	b.s.gravar(a, t0, falha)
	b.s.gravar(a, t0.Add(2*time.Minute), falha)
	b.s.gravar(a, t0.Add(time.Minute), Result{Status: StatusUp, StatusCode: 200}) // chegou atrasado
	b.s.gravar(a, t0.Add(3*time.Minute), falha)

	if n := len(b.checks(t, alvo)); n != 4 {
		t.Errorf("%d checks gravados, queria os 4: o atrasado também vai para o histórico", n)
	}
	if inc := b.incidentes(t); len(inc) != 1 || !inc[0].StartedAt.Equal(t0) {
		t.Errorf("incidentes = %+v; queria um, aberto em %v", inc, t0)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/prober/`
Expected: FAIL de compilação. `paraDetector` não existe, e `b.s.gravar(a, ...)` recebe `db.ClaimDueTargetsRow` onde a assinatura atual pede `uuid.UUID`.

- [ ] **Step 3: Implementar**

Em `internal/prober/scheduler.go`:

1. Troque o bloco de imports por:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/targets"
)
```

2. Na struct `Scheduler`, acrescente depois de `max int32`:

```go
	detector incident.Detector
```

3. Em `newScheduler`, acrescente `detector: incident.Default,` ao literal, logo depois de `sem: semaphore.NewWeighted(int64(max)), max: max,`.

4. Em `sondar`, troque `s.gravar(a.ID, checkedAt, res)` por `s.gravar(a, checkedAt, res)`.

5. Troque a função `gravar` inteira pelo bloco abaixo, que também traz as funções novas:

```go
// gravar persiste o check e avalia a histerese na MESMA transação: ou o
// check e o estado do alvo ficam gravados juntos, ou nenhum dos dois.
func (s *Scheduler) gravar(a db.ClaimDueTargetsRow, em time.Time, res Result) {
	// Contexto próprio: a sonda terminou, e o resultado vale mesmo com o
	// processo encerrando.
	ctx, cancel := context.WithTimeout(context.Background(), PersistTimeout)
	defer cancel()

	id, err := uuid.NewV7()
	if err != nil {
		slog.Error("gerar id de check", "erro", err)
		return
	}
	p := db.InsertCheckParams{
		ID: id, TargetID: a.ID, CheckedAt: em,
		Status: string(res.Status), LatencyMS: int32Limitado(res.Latency.Milliseconds()),
	}
	if res.StatusCode != 0 {
		code := int32Limitado(int64(res.StatusCode))
		p.StatusCode = &code
	}
	if res.ErrorKind != "" {
		kind := string(res.ErrorKind)
		p.ErrorKind = &kind
	}

	err = storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		if err := q.InsertCheck(ctx, p); err != nil {
			return err
		}
		_, err := s.avaliar(ctx, q, a, em, res)
		return err
	})
	switch {
	case err == nil:
	case alvoSumiu(err):
		// Review Focus 3 do Plano 2: o dono apagou o alvo com a sonda em
		// voo. Não é erro, é o dono tendo mudado de ideia.
		s.metrics.forget(a.ID)
	default:
		slog.Error("gravar check", "target_id", a.ID, "erro", err)
	}
}

// avaliar aplica o check à histerese do alvo (spec §7) e, numa transição,
// abre ou fecha o incidente e enfileira as notificações (spec §8).
//
// A linha de target_health é travada antes de ler o incidente aberto: duas
// sondas do mesmo alvo gravando ao mesmo tempo (M4 do Plano 2) passam aqui
// uma de cada vez, e a segunda vê o estado que a primeira gravou.
func (s *Scheduler) avaliar(ctx context.Context, q *db.Queries, a db.ClaimDueTargetsRow, em time.Time, res Result) (incident.Action, error) {
	if err := q.EnsureTargetHealth(ctx, a.ID); err != nil {
		return incident.ActionNone, fmt.Errorf("criar histerese do alvo: %w", err)
	}
	h, err := q.LockTargetHealth(ctx, a.ID)
	if err != nil {
		return incident.ActionNone, fmt.Errorf("travar histerese do alvo: %w", err)
	}
	aberto, err := q.GetOpenIncident(ctx, a.ID)
	temAberto := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return incident.ActionNone, fmt.Errorf("buscar incidente aberto: %w", err)
	}

	estado, acao := s.detector.Evaluate(incident.State{
		ConsecutiveFailures:  h.ConsecutiveFailures,
		ConsecutiveSuccesses: h.ConsecutiveSuccesses,
		StreakSince:          valorOuZero(h.StreakSince),
		LastCheckedAt:        valorOuZero(h.LastCheckedAt),
		Open:                 temAberto,
	}, paraDetector(em, res))

	// Depois de qualquer Evaluate os dois instantes estão preenchidos: um
	// check fora de ordem só é ignorado quando já houve um antes dele.
	desde := estado.StreakSince
	if err := q.SaveTargetHealth(ctx, db.SaveTargetHealthParams{
		TargetID:             a.ID,
		ConsecutiveFailures:  estado.ConsecutiveFailures,
		ConsecutiveSuccesses: estado.ConsecutiveSuccesses,
		StreakSince:          &desde,
		LastCheckedAt:        estado.LastCheckedAt,
	}); err != nil {
		return incident.ActionNone, fmt.Errorf("gravar histerese do alvo: %w", err)
	}

	switch acao {
	case incident.ActionOpen:
		id, err := uuid.NewV7()
		if err != nil {
			return acao, fmt.Errorf("gerar id de incidente: %w", err)
		}
		// Abrir só acontece num check down, que sempre tem error_kind.
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{
			ID: id, TargetID: a.ID, StartedAt: estado.StreakSince, Cause: string(res.ErrorKind),
		}); err != nil {
			return acao, fmt.Errorf("abrir incidente: %w", err)
		}
		n, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: id, Now: em})
		if err != nil {
			return acao, fmt.Errorf("marcar abertura notificada: %w", err)
		}
		if n == 1 {
			return acao, enfileirar(ctx, q, a.UserID, id, incident.EventOpened, em)
		}
	case incident.ActionClose:
		if _, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: aberto.ID, ResolvedAt: estado.StreakSince}); err != nil {
			return acao, fmt.Errorf("resolver incidente: %w", err)
		}
		n, err := q.MarkCloseNotified(ctx, db.MarkCloseNotifiedParams{ID: aberto.ID, Now: em})
		if err != nil {
			return acao, fmt.Errorf("marcar fechamento notificado: %w", err)
		}
		if n == 1 {
			return acao, enfileirar(ctx, q, a.UserID, aberto.ID, incident.EventResolved, em)
		}
	}
	return acao, nil
}

// paraDetector traduz o resultado da sonda para o detector. Só down conta
// como falha: degraded é estado de exibição e não mexe em incidente
// (spec §7.2).
func paraDetector(em time.Time, r Result) incident.ProbeResult {
	return incident.ProbeResult{Down: r.Status == StatusDown, At: em}
}

// enfileirar grava uma notificação por canal entregável do dono (spec §8).
// Os ids são UUIDv7 gerados aqui, porque o Postgres 16 não tem uuidv7(); com
// no máximo notify.MaxChannels canais, um INSERT por canal é barato. O
// UNIQUE (incident, channel, event) torna a repetição inofensiva.
func enfileirar(ctx context.Context, q *db.Queries, dono, incidente uuid.UUID, evento string, em time.Time) error {
	canais, err := q.ListDeliverableChannels(ctx, dono)
	if err != nil {
		return fmt.Errorf("listar canais do dono: %w", err)
	}
	for _, canal := range canais {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("gerar id de notificação: %w", err)
		}
		if err := q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: evento, Now: em,
		}); err != nil {
			return fmt.Errorf("enfileirar notificação: %w", err)
		}
	}
	return nil
}

func valorOuZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./internal/prober/ && go test -race -count=1 ./internal/prober/`
Expected: PASS. `TestVanishedTargetIsDiscardedQuietly`, do Plano 2, continua verde: o `INSERT` do check falha antes da avaliação.

- [ ] **Step 5: Sabotagem — duas guardas**

1. Em `incidents.sql`, tire `AND verified_at IS NOT NULL` de `ListDeliverableChannels`; `sqlc generate`; rode `go test -count=1 -run TestThreeFailures ./internal/prober/`. Expected: FAIL com duas notificações, e não uma só no canal entregável.
2. Em `avaliar`, troque `StartedAt: estado.StreakSince` por `StartedAt: em`. Rode `-run 'TestThreeFailures|TestOutOfOrder'`. Expected: FAIL nos dois, com o início no check que abriu.

Restaure (e `sqlc generate`) e registre no relatório.

- [ ] **Step 6: Commit**

```bash
git add internal/prober/
git commit -m "feat(prober): avalia a histerese ao gravar o check e enfileira as notificações"
```

---
### Task 4: Reconciliação das métricas e `incidents_open`

Fecha a dívida D9/M1 do Plano 2: apagar um alvo parado não passa pelo sondador, e a série de latência dele vivia até o restart. Acrescenta `incidents_open` (spec §13).

**Files:**
- Modify: `internal/prober/metrics.go` (inteiro)
- Modify: `internal/prober/scheduler.go` (`reconcileEvery`, `Scheduler.transicao`, `newScheduler`, `Run`, `reconciliar`, `Reconcile`, o aviso de transição em `gravar`)
- Test: `internal/prober/reconcile_test.go`

**Interfaces:**
- Consumes: `CountOpenIncidents`, `ExistingTargetIDs` (Task 2); `avaliar` devolvendo a ação (Task 3); `(*bancada).rodada` (Task 3).
- Produces:
  - `func (s *Scheduler) Reconcile(ctx context.Context) error`.
  - Métrica `incidents_open` (gauge). `Metrics.alvosVistos() []uuid.UUID`, só para teste.
  - `Run` reconcilia ao subir, a cada `reconcileEvery` (30 s) e a cada transição de incidente. A Task 11 depende disso para ler `incidents_open` em segundos.

**Contexto para quem implementa.** O gauge vem do **banco**, e não de contagem em memória: com `--role=prober` replicado, cada processo reporta o total global (D19). O aviso de transição vai por um canal com buffer 1, enviado sem bloquear. Um aviso que chega com outro já pendente se funde nele.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/prober/reconcile_test.go`:

```go
package prober

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// D9/M1 do Plano 2: apagar um alvo PARADO não passa pelo sondador. A série
// dele só some na reconciliação.
func TestReconcileForgetsSeriesOfDeletedTargets(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	vivo := b.alvo(t, srv.URL, nil)
	apagado := b.alvo(t, srv.URL, nil)
	b.rodada(t)
	if n := testutil.CollectAndCount(b.m.duration); n != 2 {
		t.Fatalf("%d séries depois de sondar dois alvos", n)
	}

	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: apagado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(b.m.duration); n != 1 {
		t.Errorf("%d séries depois da reconciliação, queria 1", n)
	}
	if vistos := b.m.alvosVistos(); len(vistos) != 1 || vistos[0] != vivo {
		t.Errorf("alvos vistos = %v, queria só o vivo %s", vistos, vivo)
	}
}

func TestReconcileReportsOpenIncidents(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	for range 2 {
		alvo := b.alvo(t, "https://exemplo.com", nil)
		err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
			return q.OpenIncident(ctx, db.OpenIncidentParams{
				ID: uuid.Must(uuid.NewV7()), TargetID: alvo, StartedAt: b.clk.Now(), Cause: "dns",
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.incidentsOpen); v != 2 {
		t.Errorf("incidents_open = %v, queria 2", v)
	}
}

// Uma transição avisa o laço de Run, para que incidents_open não espere a
// reconciliação periódica de 30s.
func TestTransitionSignalsTheReconcileLoop(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono}
	falha := Result{Status: StatusDown, ErrorKind: KindDNS}

	b.s.gravar(a, b.clk.Now(), falha)
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 0 {
		t.Fatal("duas falhas, sem transição, já avisaram o laço")
	}
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 1 {
		t.Error("a abertura do incidente não avisou o laço de reconciliação")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/prober/`
Expected: FAIL de compilação: `b.s.Reconcile undefined`, `b.m.alvosVistos undefined`, `b.m.incidentsOpen undefined`, `b.s.transicao undefined`.

- [ ] **Step 3: Implementar as métricas**

Troque `internal/prober/metrics.go` inteiro por:

```go
package prober

import (
	"sync"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics são as métricas do sondador (spec §13).
type Metrics struct {
	duration      *prometheus.HistogramVec
	total         *prometheus.CounterVec
	inflight      prometheus.Gauge
	queueDepth    prometheus.Gauge
	incidentsOpen prometheus.Gauge

	// vistos é o conjunto de alvos com série em duration. A reconciliação o
	// confronta com o banco para apagar as séries de alvos que sumiram.
	mu     sync.Mutex
	vistos map[uuid.UUID]struct{}
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "probe_duration_seconds",
			Help: "Latência das sondas, por alvo.",
			// Até 30s, o timeout máximo de um alvo.
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"target_id"}),
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "probe_total",
			Help: "Sondas concluídas, por estado.",
		}, []string{"status"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scheduler_inflight",
			Help: "Sondas em voo agora.",
		}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scheduler_queue_depth",
			Help: "Alvos vencidos que não couberam no último despacho.",
		}),
		// É o total do banco, não o deste processo: com vários sondadores,
		// todos reportam o mesmo número. Agregue com max, nunca com sum.
		incidentsOpen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "incidents_open",
			Help: "Incidentes abertos agora, em todos os alvos.",
		}),
		vistos: map[uuid.UUID]struct{}{},
	}
	reg.MustRegister(m.duration, m.total, m.inflight, m.queueDepth, m.incidentsOpen)
	return m
}

func (m *Metrics) observe(alvo uuid.UUID, r Result) {
	m.duration.WithLabelValues(alvo.String()).Observe(r.Latency.Seconds())
	m.total.WithLabelValues(string(r.Status)).Inc()
	m.mu.Lock()
	m.vistos[alvo] = struct{}{}
	m.mu.Unlock()
}

// forget apaga a série de um alvo que deixou de existir (D9 do Plano 2).
// Sem isso, cada alvo apagado deixaria uma série viva até o restart.
func (m *Metrics) forget(alvo uuid.UUID) {
	m.duration.DeleteLabelValues(alvo.String())
	m.mu.Lock()
	delete(m.vistos, alvo)
	m.mu.Unlock()
}

func (m *Metrics) alvosVistos() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]uuid.UUID, 0, len(m.vistos))
	for id := range m.vistos {
		ids = append(ids, id)
	}
	return ids
}
```

- [ ] **Step 4: Implementar a reconciliação no agendador**

Em `internal/prober/scheduler.go`:

1. Logo depois de `const tickEvery = time.Second`, acrescente:

```go

// reconcileEvery é o intervalo da reconciliação das métricas. Uma
// transição de incidente também dispara uma, sem esperar o intervalo.
const reconcileEvery = 30 * time.Second
```

2. Na struct, depois de `detector incident.Detector`, acrescente:

```go
	// transicao avisa o laço de Run que um incidente abriu ou fechou, para
	// que incidents_open não espere a próxima reconciliação periódica.
	// Buffer de 1: avisos que chegam com um já pendente se fundem nele.
	transicao chan struct{}
```

3. Em `newScheduler`, troque `detector: incident.Default,` por `detector: incident.Default, transicao: make(chan struct{}, 1),`.

4. Troque a função `Run` inteira (com o comentário acima dela) por:

```go
// Run despacha a cada segundo até ctx acabar, e então chama Stop(grace).
// Reconcilia as métricas ao subir, a cada reconcileEvery e a cada
// transição de incidente.
func (s *Scheduler) Run(ctx context.Context, grace time.Duration) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	r := time.NewTicker(reconcileEvery)
	defer r.Stop()
	s.reconciliar(ctx)
	for {
		select {
		case <-ctx.Done():
			s.Stop(grace)
			return nil
		case <-t.C:
			if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar sondas", "erro", err)
			}
		case <-r.C:
			s.reconciliar(ctx)
		case <-s.transicao:
			s.reconciliar(ctx)
		}
	}
}

func (s *Scheduler) reconciliar(ctx context.Context) {
	if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
		slog.Error("reconciliar métricas", "erro", err)
	}
}

// Reconcile apaga as séries de latência de alvos que não existem mais e
// atualiza incidents_open (D9/M1 do Plano 2).
//
// forget só roda sozinho quando o alvo some com uma sonda em voo. Apagar um
// alvo parado não passa pelo sondador, e com --role=api e --role=prober
// separados nem poderia: sem esta reconciliação, a série do alvo apagado
// vivia até o restart.
func (s *Scheduler) Reconcile(ctx context.Context) error {
	vistos := s.metrics.alvosVistos()
	var existentes []uuid.UUID
	var abertos int64
	err := storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		var err error
		if len(vistos) > 0 {
			if existentes, err = q.ExistingTargetIDs(ctx, vistos); err != nil {
				return err
			}
		}
		abertos, err = q.CountOpenIncidents(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("reconciliar métricas: %w", err)
	}

	vivos := make(map[uuid.UUID]struct{}, len(existentes))
	for _, id := range existentes {
		vivos[id] = struct{}{}
	}
	for _, id := range vistos {
		if _, ok := vivos[id]; !ok {
			s.metrics.forget(id)
		}
	}
	s.metrics.incidentsOpen.Set(float64(abertos))
	return nil
}
```

5. Em `gravar`, troque o trecho que vai de `err = storage.InProberTx(` até a linha `case err == nil:` (inclusive) por:

```go
	var acao incident.Action
	err = storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		if err := q.InsertCheck(ctx, p); err != nil {
			return err
		}
		var err error
		acao, err = s.avaliar(ctx, q, a, em, res)
		return err
	})
	switch {
	case err == nil:
		if acao != incident.ActionNone {
			select {
			case s.transicao <- struct{}{}:
			default:
			}
		}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `gofmt -w internal/ && go test -race -count=1 ./internal/prober/`
Expected: PASS.

- [ ] **Step 6: Sabotagem**

Em `Reconcile`, troque `s.metrics.forget(id)` por `_ = id`. Em `gravar`, troque `case s.transicao <- struct{}{}:` por `case make(chan struct{}) <- struct{}{}:`. Rode `go test -count=1 -run 'TestReconcile|TestTransition' ./internal/prober/`.
Expected: FAIL em `TestReconcileForgetsSeriesOfDeletedTargets` ("2 séries depois da reconciliação") e em `TestTransitionSignalsTheReconcileLoop`. Restaure e registre.

- [ ] **Step 7: Commit**

```bash
git add internal/prober/
git commit -m "feat(prober): reconcilia séries de alvos apagados e expõe incidents_open"
```

---
### Task 5: Canais — Discord, Resend e a mensagem

Os dois `Notifier` da spec §8, testados contra `httptest` pelo `safedial`, e sem Docker.

**Files:**
- Create: `internal/notify/notify.go`, `internal/notify/message.go`, `internal/notify/discord.go`, `internal/notify/resend.go`, `internal/notify/config.go`
- Test: `internal/notify/senders_test.go`, `internal/notify/message_test.go`

**Interfaces:**
- Consumes: `crypto.Secret`, `crypto.Box`, `crypto.AAD`; `safedial.NewClient`, `safedial.NewClientAllowing`, `safedial.ErrBlocked`, `safedial.ErrInvalidURL`; `incident.EventOpened`/`EventResolved`.
- Produces:
  - `notify.Kind` (`KindDiscord`, `KindEmail`); `notify.Event{NotificationID uuid.UUID; Kind, TargetName string; StartedAt, ResolvedAt time.Time; Cause string}`.
  - `notify.Notifier` com `Kind() Kind` e `Send(ctx, dest crypto.Secret, ev Event) error`.
  - `notify.Reason` com os sete valores do CHECK de `last_error`; `notify.SendError{Reason; Permanent bool; RetryAfter time.Duration}`.
  - `notify.ValidateDiscordURL(string) error`, `notify.ErrDiscordURL`; `*notify.Discord` via `NewDiscord()`, com `Send` e `SendTest(ctx, dest) error`.
  - `*notify.Resend` via `NewResend(apiKey crypto.Secret, from string)`, com `Send` e `SendVerification(ctx, dest, link string) error`.
  - `notify.NormalizeEmail(string) (string, bool)`.
  - Do pacote, usadas pelas Tasks 6 e 7: `sealConfig(box, id, dest string) ([]byte, error)`, `openConfig(box, id, sealed) (crypto.Secret, error)`, `hint(kind, dest) string`, `mensagem(ev) (assunto, corpo string)`, `classificarErro`, `classificarResposta`, `drenar`, `userAgent`.

**Contexto para quem implementa.** Fatos das APIs externas, conferidos na documentação (Context7) em 2026-09-24:

- **Discord, Execute Webhook:** `POST /api/webhooks/{id}/{token}`, JSON com `content` (até 2000 caracteres), `allowed_mentions` e `flags`. `allowed_mentions: {"parse": []}` desliga toda menção; **omitir o campo** deixa o padrão de webhook, que resolve menções de usuário. `?wait=true` faz o Discord confirmar a gravação; sem ele, uma mensagem descartada não devolve erro. Um 429 traz `Retry-After` em segundos, com fração.
- **Resend:** `POST https://api.resend.com/emails` com `from`, `to[]`, `subject`, `text` e `Authorization: Bearer <chave>`. `Idempotency-Key` tem até 256 caracteres e vale 24h. Um 409 `concurrent_idempotent_requests` é a mesma chave ainda em processamento (repetir depois); um 409 `invalid_idempotent_request` é a mesma chave com **outro corpo** (permanente). 400, 401, 403 e 422 são erros do pedido; 429 e 5xx são transitórios. O limite padrão é de 10 pedidos por segundo por conta.

`SendError` **não** carrega o erro de origem. O `*url.Error` do `net/http` traz a URL do webhook, e o token com ela.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/notify/senders_test.go`:

```go
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

// pedido é o que o servidor de teste viu. A rede não conta como
// sincronização para o -race (Plano 2), então o handler entrega o pedido
// por canal, e não numa variável que o teste lê.
type pedido struct {
	Metodo, Caminho, Query string
	Cabecalho              http.Header
	Corpo                  []byte
}

// provedor sobe um httptest que responde com responder e entrega cada
// pedido em pedidos. Devolve o cliente do safedial liberado só para ele.
func provedor(t *testing.T, responder func(w http.ResponseWriter)) (*httptest.Server, *http.Client, <-chan pedido) {
	t.Helper()
	pedidos := make(chan pedido, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(r.Body)
		pedidos <- pedido{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), corpo}
		responder(w)
	}))
	t.Cleanup(srv.Close)
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return srv, safedial.NewClientAllowing(0, ap), pedidos
}

func receber(t *testing.T, c <-chan pedido) pedido {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("o provedor não recebeu o pedido")
		return pedido{}
	}
}

func status(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code) }
}

var evAberto = Event{
	NotificationID: uuid.MustParse("01920000-0000-7000-8000-000000000001"),
	Kind:           incident.EventOpened,
	TargetName:     "API de pagamentos",
	StartedAt:      time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
	Cause:          "timeout",
}

func aceitarQualquer(string) error { return nil }

func comoSendError(t *testing.T, err error) *SendError {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("erro = %v, queria *SendError", err)
	}
	return se
}

func TestValidateDiscordURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://discord.com/api/webhooks/123456789012345678/abcDEF_ghi-JKL", true},
		{"https://discordapp.com/api/webhooks/1/t", true},
		{"https://canary.discord.com/api/v10/webhooks/1/t", true},
		{"https://Discord.com/api/webhooks/1/t", true},
		{"http://discord.com/api/webhooks/1/t", false},              // sem TLS
		{"https://discord.com.evil.io/api/webhooks/1/t", false},     // sufixo
		{"https://evil.io/api/webhooks/1/t", false},                 // outro host
		{"https://discord.com:8443/api/webhooks/1/t", false},        // porta
		{"https://user:pw@discord.com/api/webhooks/1/t", false},     // credencial
		{"https://discord.com/api/webhooks/1/t?thread_id=2", false}, // query
		{"https://discord.com/api/webhooks/1/t#x", false},           // fragmento
		{"https://discord.com/api/webhooks/abc/t", false},           // id não numérico
		{"https://discord.com/api/webhooks/1/t/slack", false},       // outro endpoint
		{"https://discord.com/api/webhooks/1/../../users/@me", false},
		{"https://discord.com/api/webhooks/1/" + strings.Repeat("a", 600), false},
		{"https://127.0.0.1/api/webhooks/1/t", false},
		{"", false},
	}
	for _, c := range cases {
		if err := ValidateDiscordURL(c.url); (err == nil) != c.ok {
			t.Errorf("ValidateDiscordURL(%q) = %v, queria ok=%v", c.url, err, c.ok)
		}
	}
}

func TestDiscordSendsWithoutMentionsOrEmbeds(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := &Discord{client: client, validate: aceitarQualquer}

	if err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/tok"), evAberto); err != nil {
		t.Fatal(err)
	}
	p := receber(t, pedidos)
	if p.Metodo != http.MethodPost || p.Caminho != "/api/webhooks/1/tok" || p.Query != "wait=true" {
		t.Errorf("pedido = %s %s?%s", p.Metodo, p.Caminho, p.Query)
	}
	if p.Cabecalho.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", p.Cabecalho.Get("Content-Type"))
	}
	// O JSON cru, e não o decodificado: "parse": null e a ausência da chave
	// decodificam para o mesmo nil, e os dois ligam as menções no Discord.
	if !strings.Contains(string(p.Corpo), `"allowed_mentions":{"parse":[]}`) {
		t.Errorf("allowed_mentions não desliga as menções: %s", p.Corpo)
	}
	var corpo discordPayload
	if err := json.Unmarshal(p.Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.Flags != suppressEmbeds || !strings.Contains(corpo.Content, "API de pagamentos") || !strings.Contains(corpo.Content, "tempo esgotado") {
		t.Errorf("payload = %+v", corpo)
	}
}

func TestDiscordClassifiesResponses(t *testing.T) {
	cases := []struct {
		nome      string
		responder func(http.ResponseWriter)
		reason    Reason
		permanent bool
		retry     time.Duration
	}{
		{"204 é entregue", status(http.StatusNoContent), "", false, 0},
		{"429 pede espera", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "2.5")
			w.WriteHeader(http.StatusTooManyRequests)
		}, ReasonRateLimited, false, 2500 * time.Millisecond},
		{"500 é transitório", status(http.StatusInternalServerError), ReasonServerError, false, 0},
		{"404 é webhook apagado", status(http.StatusNotFound), ReasonRejected, true, 0},
		{"401 é token inválido", status(http.StatusUnauthorized), ReasonRejected, true, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			srv, client, _ := provedor(t, c.responder)
			d := &Discord{client: client, validate: aceitarQualquer}
			err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
			if c.reason == "" {
				if err != nil {
					t.Fatalf("erro = %v, queria entregue", err)
				}
				return
			}
			se := comoSendError(t, err)
			if se.Reason != c.reason || se.Permanent != c.permanent || se.RetryAfter != c.retry {
				t.Errorf("SendError = %+v, queria %s permanente=%v retry=%v", se, c.reason, c.permanent, c.retry)
			}
		})
	}
}

// A revalidação no envio é a guarda contra uma URL que entrou no banco por
// fora da API. O cliente deste teste ALCANÇA o servidor: se a validação
// sumisse, o pedido chegaria, e o contador mostraria.
func TestDiscordRevalidatesTheURLBeforeSending(t *testing.T) {
	var chegou atomic.Int32
	srv, client, _ := provedor(t, func(w http.ResponseWriter) { chegou.Add(1); w.WriteHeader(http.StatusOK) })
	d := &Discord{client: client, validate: ValidateDiscordURL}

	err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
	se := comoSendError(t, err)
	if se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("SendError = %+v, queria invalid_channel permanente", se)
	}
	if chegou.Load() != 0 {
		t.Error("o pedido chegou ao servidor: a URL não foi revalidada antes do envio")
	}
}

// O SendError não carrega o erro de origem, que traz a URL com o token.
func TestSendErrorNeverCarriesTheWebhookToken(t *testing.T) {
	d := &Discord{client: safedial.NewClient(0), validate: aceitarQualquer}
	err := d.Send(context.Background(), crypto.Secret("http://127.0.0.1:1/api/webhooks/1/token-secreto"), evAberto)
	if err == nil || strings.Contains(err.Error(), "token-secreto") {
		t.Errorf("erro = %v; não pode ser nil nem conter o token", err)
	}
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("destino bloqueado pelo safedial deu %+v, queria invalid_channel permanente", se)
	}
}

func TestResendSendsTheAlertWithAnIdempotencyKey(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL + "/emails", apiKey: crypto.Secret("re_chave"), from: "Vigilume <alertas@vigilume.dev>"}

	for range 2 {
		if err := r.Send(context.Background(), crypto.Secret("pessoa@exemplo.com"), evAberto); err != nil {
			t.Fatal(err)
		}
	}
	p1, p2 := receber(t, pedidos), receber(t, pedidos)

	if p1.Cabecalho.Get("Authorization") != "Bearer re_chave" {
		t.Errorf("Authorization = %q", p1.Cabecalho.Get("Authorization"))
	}
	if k := p1.Cabecalho.Get("Idempotency-Key"); k != "notification/"+evAberto.NotificationID.String() {
		t.Errorf("Idempotency-Key = %q", k)
	}
	// A mesma chave com outro corpo é recusada pelo Resend: a retentativa
	// tem de montar o mesmo corpo, byte a byte.
	if string(p1.Corpo) != string(p2.Corpo) {
		t.Errorf("duas montagens do mesmo evento diferem:\n%s\n%s", p1.Corpo, p2.Corpo)
	}
	var corpo resendPayload
	if err := json.Unmarshal(p1.Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.From != r.from || len(corpo.To) != 1 || corpo.To[0] != "pessoa@exemplo.com" ||
		!strings.Contains(corpo.Subject, "API de pagamentos") || !strings.Contains(corpo.Text, "24/09/2026 12:00 UTC") {
		t.Errorf("payload = %+v", corpo)
	}
}

func TestResendVerificationCarriesTheLinkAndNoIdempotencyKey(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}

	if err := r.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/verify/abc"); err != nil {
		t.Fatal(err)
	}
	p := receber(t, pedidos)
	if p.Cabecalho.Get("Idempotency-Key") != "" {
		t.Error("verificação levou Idempotency-Key; cada envio tem token novo")
	}
	if !strings.Contains(string(p.Corpo), "https://v.dev/verify/abc") {
		t.Errorf("corpo sem o link: %s", p.Corpo)
	}
}

func TestResendClassifiesResponses(t *testing.T) {
	conflito := func(nome string) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"statusCode":409,"name":"`+nome+`","message":"x"}`)
		}
	}
	cases := []struct {
		nome      string
		responder func(http.ResponseWriter)
		reason    Reason
		permanent bool
	}{
		{"mesma chave em processamento", conflito("concurrent_idempotent_requests"), ReasonRateLimited, false},
		{"mesma chave com outro corpo", conflito("invalid_idempotent_request"), ReasonRejected, true},
		{"422 é pedido inválido", status(http.StatusUnprocessableEntity), ReasonRejected, true},
		{"403 é domínio não verificado", status(http.StatusForbidden), ReasonRejected, true},
		{"503 é transitório", status(http.StatusServiceUnavailable), ReasonServerError, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			srv, client, _ := provedor(t, c.responder)
			r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}
			se := comoSendError(t, r.Send(context.Background(), crypto.Secret("p@x.com"), evAberto))
			if se.Reason != c.reason || se.Permanent != c.permanent {
				t.Errorf("SendError = %+v, queria %s permanente=%v", se, c.reason, c.permanent)
			}
		})
	}
}

func TestSendTimesOutWithTheContext(t *testing.T) {
	liberar := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(liberar) }) })
	srv, client, _ := provedor(t, func(w http.ResponseWriter) { <-liberar })
	d := &Discord{client: client, validate: aceitarQualquer}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	se := comoSendError(t, d.Send(ctx, crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto))
	once.Do(func() { close(liberar) })
	if se.Reason != ReasonTimeout || se.Permanent {
		t.Errorf("SendError = %+v, queria timeout transitório", se)
	}
}

func TestRetryAfter(t *testing.T) {
	for v, quer := range map[string]time.Duration{
		"3":                             3 * time.Second,
		"0.25":                          250 * time.Millisecond,
		"":                              0,
		"-1":                            0,
		"NaN":                           0,
		"Inf":                           0,
		"Wed, 21 Oct 2026 07:28:00 GMT": 0,
		"999999":                        maxRetryAfter,
	} {
		if got := retryAfter(v); got != quer {
			t.Errorf("retryAfter(%q) = %v, queria %v", v, got, quer)
		}
	}
}
```

Crie `internal/notify/message_test.go`:

```go
package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
)

func TestMessageForOpenedAndResolved(t *testing.T) {
	assunto, corpo := mensagem(evAberto)
	if assunto != "[Vigilume] API de pagamentos está fora do ar" ||
		corpo != "API de pagamentos está fora do ar desde 24/09/2026 12:00 UTC.\nCausa: tempo esgotado.\n" {
		t.Errorf("abertura: %q / %q", assunto, corpo)
	}

	fechou := evAberto
	fechou.Kind = incident.EventResolved
	fechou.ResolvedAt = fechou.StartedAt.Add(2*time.Hour + 5*time.Minute + 40*time.Second)
	assunto, corpo = mensagem(fechou)
	if assunto != "[Vigilume] API de pagamentos voltou" ||
		corpo != "API de pagamentos voltou em 24/09/2026 14:05 UTC, depois de 2h 05min fora do ar.\n" {
		t.Errorf("fechamento: %q / %q", assunto, corpo)
	}
}

// Causa fora do enum não vaza para a mensagem: vira o residual.
func TestMessageNeverEchoesAnUnknownCause(t *testing.T) {
	ev := evAberto
	ev.Cause = "dial tcp 10.0.3.42:5432: connect: connection refused"
	if _, corpo := mensagem(ev); strings.Contains(corpo, "10.0.3.42") || !strings.Contains(corpo, "erro de rede") {
		t.Errorf("corpo = %q", corpo)
	}
}

func TestDuration(t *testing.T) {
	for d, quer := range map[time.Duration]string{
		30 * time.Second:               "menos de um minuto",
		7*time.Minute + 59*time.Second: "7 min",
		time.Hour:                      "1h 00min",
		26*time.Hour + 30*time.Minute:  "1d 2h",
	} {
		if got := duracao(d); got != quer {
			t.Errorf("duracao(%v) = %q, queria %q", d, got, quer)
		}
	}
}

func TestConfigIsBoundToTheRow(t *testing.T) {
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	id, outro := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(box, id, "https://discord.com/api/webhooks/1/tok")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "tok") {
		t.Error("config_enc contém o destino em claro")
	}
	if dest, err := openConfig(box, id, sealed); err != nil || string(dest.Reveal()) != "https://discord.com/api/webhooks/1/tok" {
		t.Errorf("abrir na própria linha: %v", err)
	}
	if _, err := openConfig(box, outro, sealed); err == nil {
		t.Error("o ciphertext abriu em outra linha: o AAD não amarra o id")
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, quer := range map[string]string{
		"pessoa@Exemplo.COM":                "pessoa@exemplo.com",
		"  pessoa@exemplo.com ":             "pessoa@exemplo.com",
		"Pessoa@exemplo.com":                "Pessoa@exemplo.com", // a parte local pode diferenciar caixa
		"Fulano <p@x.com>":                  "",
		"p@x.com (comentário)":              "",
		"p@localhost":                       "",
		"sem-arroba":                        "",
		"a@b.com, c@d.com":                  "",
		"":                                  "",
		strings.Repeat("a", 250) + "@x.com": "",
	} {
		got, ok := NormalizeEmail(in)
		if (quer == "") == ok || got != quer {
			t.Errorf("NormalizeEmail(%q) = %q, %v; queria %q", in, got, ok, quer)
		}
	}
}

func TestHintNeverShowsTheSecret(t *testing.T) {
	if h := hint(KindDiscord, crypto.Secret("https://discord.com/api/webhooks/123456789012/token-secreto")); h != "webhook …9012" {
		t.Errorf("hint do Discord = %q", h)
	}
	if h := hint(KindEmail, crypto.Secret("pessoa@exemplo.com")); h != "p***@exemplo.com" {
		t.Errorf("hint do e-mail = %q", h)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/notify/`
Expected: FAIL de compilação; o pacote ainda não tem código.

- [ ] **Step 3: Implementar**

Crie `internal/notify/notify.go`:

```go
// Package notify entrega as notificações de incidente (spec §8): os dois
// canais (Discord e e-mail pelo Resend), o dispatcher que consome a outbox
// e o cadastro de canais com verificação de destino (spec §5.8).
//
// Canais, dispatcher e cadastro moram num pacote só de propósito: o
// dispatcher precisa decifrar a config que o cadastro cifra, e o cadastro
// precisa dos canais para a mensagem de teste. Em pacotes separados, isso
// seria um ciclo de importação ou um pacote de tipos sem comportamento.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

type Kind string

const (
	KindDiscord Kind = "discord"
	KindEmail   Kind = "email"
)

// Event é o que uma notificação diz. Tudo nele sai do banco, e nada do
// relógio: uma retentativa monta exatamente a mesma mensagem. O Resend
// exige isso, porque recusa a mesma Idempotency-Key com outro corpo.
type Event struct {
	NotificationID uuid.UUID
	Kind           string // incident.EventOpened ou incident.EventResolved
	TargetName     string
	StartedAt      time.Time
	ResolvedAt     time.Time // zero enquanto o incidente está aberto
	Cause          string
}

// Notifier é a interface da spec §8. dest é a URL do webhook ou o endereço
// de e-mail, já decifrados.
type Notifier interface {
	Kind() Kind
	Send(ctx context.Context, dest crypto.Secret, ev Event) error
}

// Reason é o motivo de uma falha de envio. Os valores são os do CHECK de
// notifications.last_error (migration 00005).
type Reason string

const (
	ReasonTimeout        Reason = "timeout"
	ReasonNetwork        Reason = "network"
	ReasonRateLimited    Reason = "rate_limited"
	ReasonRejected       Reason = "rejected"
	ReasonServerError    Reason = "server_error"
	ReasonInvalidChannel Reason = "invalid_channel"
	ReasonUnknown        Reason = "unknown"
)

// SendError é a falha de um envio, já classificada. Não carrega o erro de
// origem de propósito: o *url.Error do net/http traz a URL do webhook, com
// o token dentro, e um %v descuidado num log a publicaria.
type SendError struct {
	Reason    Reason
	Permanent bool
	// RetryAfter é o prazo que o provedor pediu (cabeçalho Retry-After num
	// 429). Zero quando ele não pediu nada.
	RetryAfter time.Duration
}

func (e *SendError) Error() string {
	return fmt.Sprintf("envio falhou: %s (permanente: %v)", e.Reason, e.Permanent)
}

// maxRetryAfter limita o Retry-After que o provedor pode impor.
const maxRetryAfter = time.Hour

// classificarResposta traduz o status HTTP. 2xx é entregue; 429 e 5xx são
// transitórios; o resto dos 4xx é recusa do provedor, e repetir o mesmo
// pedido daria a mesma recusa.
func classificarResposta(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return &SendError{Reason: ReasonRateLimited, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode >= 500:
		return &SendError{Reason: ReasonServerError}
	default:
		return &SendError{Reason: ReasonRejected, Permanent: true}
	}
}

// classificarErro traduz a falha de rede. Destino bloqueado pelo safedial é
// permanente: o mesmo endereço será bloqueado de novo.
func classificarErro(err error) error {
	switch {
	case errors.Is(err, safedial.ErrBlocked), errors.Is(err, safedial.ErrInvalidURL):
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	case errors.Is(err, context.DeadlineExceeded):
		return &SendError{Reason: ReasonTimeout}
	default:
		return &SendError{Reason: ReasonNetwork}
	}
}

// retryAfter lê o Retry-After em segundos, que é como o Discord e o Resend
// o mandam (o Discord, com fração). A forma de data HTTP vira zero, e o
// backoff do dispatcher decide.
func retryAfter(v string) time.Duration {
	s, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	// ParseFloat aceita "NaN" e "Inf"; converter qualquer um deles para
	// Duration é indefinido.
	if err != nil || math.IsNaN(s) || math.IsInf(s, 0) || s <= 0 {
		return 0
	}
	if s >= maxRetryAfter.Seconds() {
		return maxRetryAfter
	}
	return time.Duration(s * float64(time.Second))
}

// drenar lê um pedaço do corpo e fecha. Ninguém usa o conteúdo; ler até um
// limite é o que deixa a conexão fechar limpa.
func drenar(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

const userAgent = "Vigilume/1.0 (+monitor de disponibilidade)"
```

Crie `internal/notify/message.go`:

```go
package notify

import (
	"fmt"
	"time"

	"github.com/BipoXDXD/vigilume/internal/incident"
)

// causas traduz o error_kind para quem lê o alerta. É o enum, e nunca a
// mensagem crua do erro, que não é persistida (spec §4.2).
var causas = map[string]string{
	"timeout":      "tempo esgotado",
	"dns":          "falha de DNS",
	"tls":          "erro de TLS",
	"conn_refused": "conexão recusada",
	"http_status":  "status HTTP diferente do esperado",
	"body_limit":   "corpo da resposta acima do limite",
	"blocked":      "destino bloqueado pela proteção de rede",
	"unknown":      "erro de rede",
}

const formatoData = "02/01/2006 15:04 UTC"

// mensagem monta assunto e corpo do alerta. Só usa campos de ev, que vêm do
// banco: a mesma notificação produz sempre o mesmo texto.
func mensagem(ev Event) (assunto, corpo string) {
	if ev.Kind == incident.EventResolved {
		return fmt.Sprintf("[Vigilume] %s voltou", ev.TargetName),
			fmt.Sprintf("%s voltou em %s, depois de %s fora do ar.\n",
				ev.TargetName, ev.ResolvedAt.UTC().Format(formatoData), duracao(ev.ResolvedAt.Sub(ev.StartedAt)))
	}
	causa, ok := causas[ev.Cause]
	if !ok {
		causa = causas["unknown"]
	}
	return fmt.Sprintf("[Vigilume] %s está fora do ar", ev.TargetName),
		fmt.Sprintf("%s está fora do ar desde %s.\nCausa: %s.\n",
			ev.TargetName, ev.StartedAt.UTC().Format(formatoData), causa)
}

// duracao escreve d em minutos, horas ou dias, arredondando para baixo.
func duracao(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de um minuto"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %02dmin", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}
```

Crie `internal/notify/discord.go`:

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

var ErrDiscordURL = errors.New("URL de webhook do Discord inválida")

// discordHosts são os hosts de webhook do Discord. A lista é fechada: o
// servidor faz um POST para a URL que o usuário deu, e aceitar qualquer
// host transformaria o canal num proxy de POST arbitrário.
var discordHosts = map[string]bool{
	"discord.com":        true,
	"discordapp.com":     true,
	"canary.discord.com": true,
	"ptb.discord.com":    true,
}

// /api/webhooks/{id}/{token}, com versão opcional (/api/v10/webhooks/...).
var discordPath = regexp.MustCompile(`^/api(/v[0-9]{1,2})?/webhooks/[0-9]{1,20}/[A-Za-z0-9_-]{1,100}$`)

const maxDiscordURL = 512

// ValidateDiscordURL aceita só URLs de webhook do Discord: https, host da
// lista, sem porta, sem credencial, sem query nem fragmento. A query fica
// de fora porque o envio acrescenta a sua (?wait=true).
func ValidateDiscordURL(raw string) error {
	if len(raw) > maxDiscordURL || strings.ContainsAny(raw, "?#") {
		return ErrDiscordURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
		return ErrDiscordURL
	}
	if !discordHosts[strings.ToLower(u.Host)] || !discordPath.MatchString(u.EscapedPath()) {
		return ErrDiscordURL
	}
	return nil
}

// suppressEmbeds (1 << 2) impede o Discord de buscar prévia de um link que
// apareça no nome do alvo.
const suppressEmbeds = 1 << 2

type discordPayload struct {
	Content         string          `json:"content"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
	Flags           int             `json:"flags"`
}

// Parse vazio desliga TODA menção: um alvo chamado "@everyone" não pinga o
// servidor inteiro. O padrão do Discord para webhook, sem este campo,
// resolve menções de usuário.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

type Discord struct {
	client *http.Client
	// validate é ValidateDiscordURL em produção. Os testes do pacote trocam
	// por uma que aceita o httptest.
	validate func(string) error
}

// NewDiscord usa o cliente do safedial: a URL veio do usuário.
func NewDiscord() *Discord {
	return &Discord{client: safedial.NewClient(0), validate: ValidateDiscordURL}
}

func (*Discord) Kind() Kind { return KindDiscord }

func (d *Discord) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	_, corpo := mensagem(ev)
	emoji := "🔴 "
	if ev.Kind == incident.EventResolved {
		emoji = "🟢 "
	}
	return d.post(ctx, dest, emoji+corpo)
}

// SendTest é a verificação do canal no cadastro: só um 2xx cria o canal.
func (d *Discord) SendTest(ctx context.Context, dest crypto.Secret) error {
	return d.post(ctx, dest, "✅ Vigilume conectado. Os alertas de incidente vão chegar neste canal.")
}

func (d *Discord) post(ctx context.Context, dest crypto.Secret, conteudo string) error {
	alvo := string(dest.Reveal())
	// Revalida a cada envio: o que está no banco pode ter entrado antes de
	// uma regra ficar mais rígida, ou por fora da API.
	if err := d.validate(alvo); err != nil {
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	}
	corpo, err := json.Marshal(discordPayload{
		Content:         conteudo,
		AllowedMentions: allowedMentions{Parse: []string{}},
		Flags:           suppressEmbeds,
	})
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	// wait=true faz o Discord confirmar a gravação da mensagem antes de
	// responder; sem ele, uma mensagem descartada não devolve erro.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, alvo+"?wait=true", bytes.NewReader(corpo))
	if err != nil {
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return classificarErro(err)
	}
	defer drenar(resp)
	return classificarResposta(resp)
}
```

Crie `internal/notify/resend.go`:

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

const resendEndpoint = "https://api.resend.com/emails"

// Resend envia e-mail pela API do Resend (spec §3.3). A chave é do operador
// (RESEND_API_KEY), não do usuário: o canal de e-mail guarda só o endereço.
type Resend struct {
	client   *http.Client
	endpoint string
	apiKey   crypto.Secret
	from     string
}

// NewResend usa o cliente do safedial. O host é fixo e confiável; o
// cliente guardado não bloqueia nada legítimo e evita um segundo cliente
// sem guarda no processo.
func NewResend(apiKey crypto.Secret, from string) *Resend {
	return &Resend{client: safedial.NewClient(0), endpoint: resendEndpoint, apiKey: apiKey, from: from}
}

func (*Resend) Kind() Kind { return KindEmail }

// Send manda o alerta com Idempotency-Key = id da notificação. O Resend
// guarda a chave por 24h: uma retentativa depois de um timeout em que o
// e-mail JÁ saiu não gera um segundo e-mail.
func (r *Resend) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	assunto, corpo := mensagem(ev)
	return r.post(ctx, dest, assunto, corpo, "notification/"+ev.NotificationID.String())
}

// SendVerification manda o link de verificação (spec §5.8). Não leva
// Idempotency-Key: cada envio tem um token novo, então o corpo muda.
func (r *Resend) SendVerification(ctx context.Context, dest crypto.Secret, link string) error {
	corpo := "Alguém cadastrou este endereço para receber alertas do Vigilume.\n\n" +
		"Para confirmar, abra o link abaixo em até 24 horas:\n" + link + "\n\n" +
		"Se não foi você, ignore esta mensagem: sem a confirmação, nenhum alerta é enviado.\n"
	return r.post(ctx, dest, "[Vigilume] Confirme este endereço", corpo, "")
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (r *Resend) post(ctx context.Context, dest crypto.Secret, assunto, corpo, chave string) error {
	payload, err := json.Marshal(resendPayload{
		From: r.from, To: []string{string(dest.Reveal())}, Subject: assunto, Text: corpo,
	})
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+string(r.apiKey.Reveal()))
	if chave != "" {
		req.Header.Set("Idempotency-Key", chave)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return classificarErro(err)
	}
	defer drenar(resp)
	if resp.StatusCode == http.StatusConflict {
		return conflitoResend(resp)
	}
	return classificarResposta(resp)
}

// conflitoResend separa os dois 409 do Resend. concurrent_idempotent_requests
// é a mesma chave ainda em processamento, e vale repetir mais tarde;
// invalid_idempotent_request é a mesma chave com outro corpo, e repetir dá
// o mesmo 409.
func conflitoResend(resp *http.Response) error {
	var corpo struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&corpo)
	if corpo.Name == "concurrent_idempotent_requests" {
		return &SendError{Reason: ReasonRateLimited}
	}
	return &SendError{Reason: ReasonRejected, Permanent: true}
}
```

Crie `internal/notify/config.go`:

```go
package notify

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// sealConfig cifra o destino do canal para config_enc (spec §5.4). O AAD
// amarra o ciphertext à linha: movido para outro canal, ele não abre.
func sealConfig(box *crypto.Box, id uuid.UUID, dest string) ([]byte, error) {
	sealed, err := box.Seal([]byte(dest), configAAD(id))
	if err != nil {
		return nil, fmt.Errorf("cifrar destino do canal: %w", err)
	}
	return sealed, nil
}

func openConfig(box *crypto.Box, id uuid.UUID, sealed []byte) (crypto.Secret, error) {
	dest, err := box.Open(sealed, configAAD(id))
	if err != nil {
		return nil, fmt.Errorf("decifrar destino do canal: %w", err)
	}
	return dest, nil
}

func configAAD(id uuid.UUID) []byte { return crypto.AAD("notif_channels", "config_enc", id) }

const maxEmail = 254

// NormalizeEmail aceita só um endereço nu ("a@b.com"), sem nome de exibição
// nem comentário, e devolve o domínio em minúsculas. O segundo retorno é
// false para qualquer outra coisa.
func NormalizeEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmail {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", false
	}
	i := strings.LastIndexByte(a.Address, '@')
	local, dominio := a.Address[:i], a.Address[i+1:]
	if !strings.Contains(dominio, ".") {
		return "", false
	}
	return local + "@" + strings.ToLower(dominio), true
}

// hint é o que a API mostra do destino: o bastante para o dono reconhecer
// o canal, e nada que sirva a quem lê a resposta sem ser o dono. Do
// webhook, os quatro últimos dígitos do id (nunca o token); do e-mail, a
// primeira letra e o domínio.
func hint(kind Kind, dest crypto.Secret) string {
	s := string(dest.Reveal())
	switch kind {
	case KindDiscord:
		partes := strings.Split(strings.TrimSuffix(s, "/"), "/")
		if len(partes) < 2 {
			return "webhook"
		}
		id := partes[len(partes)-2]
		if len(id) > 4 {
			id = id[len(id)-4:]
		}
		return "webhook …" + id
	case KindEmail:
		i := strings.LastIndexByte(s, '@')
		if i < 1 {
			return "e-mail"
		}
		primeira, _ := utf8.DecodeRuneInString(s)
		return string(primeira) + "***" + s[i:]
	}
	return ""
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./internal/notify/ && go test -race -count=1 -cover ./internal/notify/`
Expected: PASS, sem Docker.

- [ ] **Step 5: Sabotagem — duas guardas**

1. Em `discord.go`, troque `AllowedMentions: allowedMentions{Parse: []string{}},` por `AllowedMentions: allowedMentions{},`. Rode `-run TestDiscordSends`. Expected: FAIL, com o JSON mostrando `"parse":null`.
2. Em `post`, troque `if err := d.validate(alvo); err != nil {` por `if err := d.validate(alvo); false && err != nil {`. Rode `-run TestDiscordRevalidates`. Expected: FAIL ("erro = <nil>"): o pedido chegou ao servidor.

Restaure e registre.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): canais Discord e Resend, mensagem determinística e classificação de erro"
```

---
### Task 6: Dispatcher — consumo da outbox, backoff e auditoria

**Files:**
- Create: `internal/notify/dispatcher.go`
- Test: `internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `ClaimDueNotifications`, `MarkNotificationSent`, `RetryNotification`, `CloseNotification`, `InsertAudit` (Task 2); `Notifier`, `SendError`, `openConfig` (Task 5); `incident.EventOpened` (Task 1).
- Produces:
  - `notify.NewDispatcher(pool, box, clk, reg prometheus.Registerer, notifiers ...Notifier) *Dispatcher`.
  - `(*Dispatcher).Run(ctx) error`, que roda até ctx acabar e sempre devolve nil.
  - `(*Dispatcher).Dispatch(ctx) (int, error)`: um lote, síncrono, que os testes chamam direto.
  - `notify.MaxAttempts = 6`; a métrica `notify_total{kind,result}`, com `result` sendo `sent`, `retry`, `failed` ou `cancelled`.
  - Do pacote, usados pela Task 7: `sendTimeout` (10 s) e `valorOuZero`. Os testes da Task 7 usam `chaveDeTeste`, definido neste teste.

**Contexto para quem implementa.** A ordem das decisões em `tentar` é a da spec §8 e das decisões D8 a D10: primeiro o canal ainda entregável, depois a abertura velha, depois o `Notifier` configurado, e só então o envio. `persistir` grava o desfecho com prazo próprio. A falha definitiva e a auditoria entram na **mesma** transação.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/notify/dispatcher_test.go`:

```go
package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

const webhookDeTeste = "https://discord.com/api/webhooks/1/tok"

// canalFalso registra os envios e devolve, em ordem, as respostas dadas.
// Sem resposta programada, entrega.
type canalFalso struct {
	mu        sync.Mutex
	respostas []error
	envios    []Event
	destinos  []string
	bloquear  bool
	panico    bool
}

func (*canalFalso) Kind() Kind { return KindDiscord }

func (c *canalFalso) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	c.mu.Lock()
	c.envios = append(c.envios, ev)
	c.destinos = append(c.destinos, string(dest.Reveal()))
	var resp error
	if len(c.respostas) > 0 {
		resp, c.respostas = c.respostas[0], c.respostas[1:]
	}
	bloquear, panico := c.bloquear, c.panico
	c.mu.Unlock()
	if panico {
		panic("canal quebrado")
	}
	if bloquear {
		<-ctx.Done()
		return ctx.Err()
	}
	return resp
}

func (c *canalFalso) quantos() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.envios)
}

type bancada struct {
	d         *Dispatcher
	pool      *pgxpool.Pool
	clk       *clock.Fake
	box       *crypto.Box
	canal     *canalFalso
	dono      uuid.UUID
	canalID   uuid.UUID
	incidente uuid.UUID
}

var chaveDeTeste = []byte("chave-mestra-de-teste-32-bytes!!")

// novaBancada monta usuário, alvo, incidente aberto e um canal Discord
// verificado, com o destino cifrado de verdade. mexer ajusta o canal antes
// de gravar.
func novaBancada(t *testing.T, mexer func(*db.InsertChannelParams)) *bancada {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	box, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	b := &bancada{pool: pool, clk: clk, box: box, canal: &canalFalso{}, dono: uuid.Must(uuid.NewV7())}
	b.d = NewDispatcher(pool, box, clk, prometheus.NewRegistry(), b.canal)

	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		b.dono, b.dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	alvo := uuid.Must(uuid.NewV7())
	agora := clk.Now()
	b.canalID = uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(box, b.canalID, webhookDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	canal := db.InsertChannelParams{
		ID: b.canalID, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
	}
	if mexer != nil {
		mexer(&canal)
	}
	err = storage.InUserTx(ctx, pool, b.dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: b.dono, Name: "API", URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, canal)
	})
	if err != nil {
		t.Fatal(err)
	}
	b.incidente = uuid.Must(uuid.NewV7())
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: b.incidente, TargetID: alvo, StartedAt: agora.Add(-3 * time.Minute), Cause: "timeout"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *bancada) enfileirar(t *testing.T, evento string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: b.incidente, ChannelID: b.canalID, Event: evento, Now: b.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type linha struct {
	Status   string
	Attempts int32
	Proxima  time.Time
	Erro     *string
	Enviada  *time.Time
}

// estado lê a notificação sob o escopo do dono (a RLS delega ao canal).
func (b *bancada) estado(t *testing.T, id uuid.UUID) linha {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	var l linha
	err = tx.QueryRow(ctx, `SELECT status, attempts, next_attempt_at, last_error, sent_at FROM notifications WHERE id = $1`, id).
		Scan(&l.Status, &l.Attempts, &l.Proxima, &l.Erro, &l.Enviada)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (b *bancada) auditorias(t *testing.T) int {
	t.Helper()
	var n int
	err := b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'notification.failed' AND entity = 'notif_channel' AND entity_id = $1 AND user_id = $2`,
		b.canalID, b.dono).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (b *bancada) despachar(t *testing.T, quer int) {
	t.Helper()
	n, err := b.d.Dispatch(context.Background())
	if err != nil || n != quer {
		t.Fatalf("Dispatch = %d, %v; queria %d", n, err, quer)
	}
}

func TestDispatchSendsAndMarksSent(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)

	if b.canal.quantos() != 1 {
		t.Fatalf("%d envios, queria 1", b.canal.quantos())
	}
	ev, dest := b.canal.envios[0], b.canal.destinos[0]
	if ev.NotificationID != id || ev.Kind != incident.EventOpened || ev.TargetName != "API" || ev.Cause != "timeout" ||
		!ev.StartedAt.Equal(b.clk.Now().Add(-3*time.Minute)) || !ev.ResolvedAt.IsZero() {
		t.Errorf("evento = %+v", ev)
	}
	if dest != webhookDeTeste {
		t.Errorf("destino decifrado = %q", dest)
	}
	if l := b.estado(t, id); l.Status != "sent" || l.Attempts != 1 || l.Enviada == nil || l.Erro != nil {
		t.Errorf("linha = %+v", l)
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "sent")); v != 1 {
		t.Errorf("notify_total{discord,sent} = %v", v)
	}
	b.despachar(t, 0) // entregue não volta
}

func TestRetryableFailureBacksOffExponentially(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonServerError}, &SendError{Reason: ReasonServerError}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	l := b.estado(t, id)
	if l.Status != "pending" || l.Attempts != 1 || !l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) ||
		l.Erro == nil || *l.Erro != "server_error" {
		t.Fatalf("depois da 1ª falha: %+v; queria pendente, próxima em 30s, server_error", l)
	}

	b.clk.Advance(29 * time.Second)
	b.despachar(t, 0)
	b.clk.Advance(time.Second)
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Attempts != 2 || !l.Proxima.Equal(b.clk.Now().Add(time.Minute)) {
		t.Errorf("depois da 2ª falha: %+v; queria próxima em 1min", l)
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "retry")); v != 2 {
		t.Errorf("notify_total{discord,retry} = %v", v)
	}
}

// O provedor pode pedir mais tempo que o backoff (429 com Retry-After).
func TestRetryAfterWinsWhenLonger(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited, RetryAfter: 5 * time.Minute}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); !l.Proxima.Equal(b.clk.Now().Add(5 * time.Minute)) {
		t.Errorf("próxima = %v, queria em 5min (Retry-After > 30s de backoff)", l.Proxima)
	}
}

// A sexta falha transitória é definitiva, e vai para a auditoria.
func TestLastAttemptFailsForGoodAndIsAudited(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	falha := &SendError{Reason: ReasonNetwork}
	for i := range MaxAttempts {
		b.canal.respostas = []error{falha}
		b.despachar(t, 1)
		if i < MaxAttempts-1 {
			if b.auditorias(t) != 0 {
				t.Fatalf("tentativa %d já auditou", i+1)
			}
			b.clk.Advance(maxBackoff)
		}
	}
	if l := b.estado(t, id); l.Status != "failed" || l.Attempts != MaxAttempts || *l.Erro != "network" {
		t.Errorf("linha = %+v; queria failed na tentativa %d", l, MaxAttempts)
	}
	if n := b.auditorias(t); n != 1 {
		t.Errorf("%d entradas de auditoria, queria 1", n)
	}
	b.clk.Advance(maxBackoff)
	b.despachar(t, 0)
}

func TestPermanentFailureIsFinalAtOnce(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonRejected, Permanent: true}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "failed" || l.Attempts != 1 || *l.Erro != "rejected" {
		t.Errorf("linha = %+v", l)
	}
	if b.auditorias(t) != 1 {
		t.Error("falha definitiva não foi auditada")
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "failed")); v != 1 {
		t.Errorf("notify_total{discord,failed} = %v", v)
	}
}

// Spec §5.8: a verificação vale no envio, e não só no fan-out. Um canal que
// não está verificado na hora do envio não recebe nada.
func TestUnverifiedChannelIsCancelledWithoutSending(t *testing.T) {
	b := novaBancada(t, func(p *db.InsertChannelParams) { p.VerifiedAt = nil })
	id := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if b.canal.quantos() != 0 {
		t.Error("enviou para um canal não verificado")
	}
	if l := b.estado(t, id); l.Status != "cancelled" {
		t.Errorf("linha = %+v, queria cancelled", l)
	}
}

func TestDisabledChannelIsCancelledWithoutSending(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notif_channels SET enabled = false WHERE id = $1`, b.canalID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	b.despachar(t, 1)
	if b.canal.quantos() != 0 || b.estado(t, id).Status != "cancelled" {
		t.Errorf("canal desabilitado: %d envios, status %s", b.canal.quantos(), b.estado(t, id).Status)
	}
}

// A abertura que não saiu antes de o incidente fechar fica velha. O
// fechamento, que diz quando caiu e quando voltou, sai normalmente.
func TestStaleOpenedIsCancelledOnceTheIncidentResolved(t *testing.T) {
	b := novaBancada(t, nil)
	aberta := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: b.incidente, ResolvedAt: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fechada := b.enfileirar(t, incident.EventResolved)

	b.despachar(t, 2)
	if l := b.estado(t, aberta); l.Status != "cancelled" {
		t.Errorf("abertura velha: %+v, queria cancelled", l)
	}
	if l := b.estado(t, fechada); l.Status != "sent" {
		t.Errorf("fechamento: %+v, queria sent", l)
	}
	if b.canal.quantos() != 1 || b.canal.envios[0].Kind != incident.EventResolved || b.canal.envios[0].ResolvedAt.IsZero() {
		t.Errorf("envios = %+v; queria só o fechamento, com ResolvedAt", b.canal.envios)
	}
}

// Canal cujo Notifier o operador não configurou (e-mail sem Resend) falha
// de vez, em vez de ficar pendente para sempre.
func TestChannelWithoutNotifierFailsForGood(t *testing.T) {
	b := novaBancada(t, nil)
	b.d = NewDispatcher(b.pool, b.box, b.clk, prometheus.NewRegistry()) // nenhum canal
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "failed" || *l.Erro != "invalid_channel" {
		t.Errorf("linha = %+v", l)
	}
	if b.auditorias(t) != 1 {
		t.Error("falha definitiva não foi auditada")
	}
}

// Um webhook que não responde não segura o lote além do prazo de envio.
func TestSendIsBoundedByTheTimeout(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.bloquear = true
	b.d.sendTimeout = 50 * time.Millisecond
	id := b.enfileirar(t, incident.EventOpened)

	inicio := time.Now()
	b.despachar(t, 1)
	if d := time.Since(inicio); d > 5*time.Second {
		t.Errorf("o lote levou %v com um envio travado", d)
	}
	if l := b.estado(t, id); l.Status != "pending" || l.Erro == nil || *l.Erro != "timeout" {
		t.Errorf("linha = %+v; queria retentativa por timeout", l)
	}
}

type logSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSeguro) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSeguro) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestPanickingNotifierDoesNotKillTheDispatcher(t *testing.T) {
	log := &logSeguro{}
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
	t.Cleanup(func() { slog.SetDefault(antigo) })

	b := novaBancada(t, nil)
	b.canal.panico = true
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if !strings.Contains(log.String(), "panic no envio de notificação") {
		t.Errorf("o panic não foi para o log:\n%s", log)
	}
	// Fica como a reivindicação deixou: volta quando o lease vencer.
	if l := b.estado(t, id); l.Status != "pending" || l.Attempts != 1 {
		t.Errorf("linha = %+v", l)
	}
	if strings.Contains(log.String(), webhookDeTeste) {
		t.Error("o destino apareceu no log")
	}
}

func TestBackoff(t *testing.T) {
	for tentativa, quer := range map[int32]time.Duration{
		1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 5: 8 * time.Minute, 20: maxBackoff,
	} {
		if got := backoff(tentativa); got != quer {
			t.Errorf("backoff(%d) = %v, queria %v", tentativa, got, quer)
		}
	}
}

// Um erro que não é *SendError (um Notifier novo, mal escrito) é tratado
// como transitório, e não derruba a classificação.
func TestPlainErrorIsRetried(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{errors.New("qualquer coisa")}
	id := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "pending" || *l.Erro != "unknown" {
		t.Errorf("linha = %+v", l)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/notify/`
Expected: FAIL de compilação: `undefined: NewDispatcher`, `undefined: MaxAttempts`, `undefined: maxBackoff`, `undefined: backoff`.

- [ ] **Step 3: Implementar**

Crie `internal/notify/dispatcher.go`:

```go
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

const (
	dispatchEvery = 2 * time.Second
	// maxConcurrentSends é o tamanho do lote e o teto de envios simultâneos.
	// O Resend aceita 10 pedidos por segundo por conta; 8 fica abaixo.
	maxConcurrentSends = 8
	// sendTimeout é o prazo de um envio (spec §8): um webhook lento não
	// segura o lote além disso.
	sendTimeout = 10 * time.Second
	// lease é quanto uma notificação reivindicada fica invisível para os
	// outros dispatchers. Tem de passar com folga de sendTimeout +
	// persistTimeout; um processo que morre no meio devolve a notificação à
	// fila quando o lease vence.
	lease          = 2 * time.Minute
	persistTimeout = 5 * time.Second

	// MaxAttempts é o teto de tentativas (spec §8). Com o backoff abaixo, a
	// sexta tentativa acontece cerca de 15 minutos depois da primeira.
	MaxAttempts  = 6
	firstBackoff = 30 * time.Second
	maxBackoff   = time.Hour
)

// Dispatcher consome a outbox de notificações (spec §8). Roda no papel do
// sondador, fora do caminho crítico da sonda.
type Dispatcher struct {
	pool        *pgxpool.Pool
	box         *crypto.Box
	clk         clock.Clock
	notifiers   map[Kind]Notifier
	total       *prometheus.CounterVec
	sendTimeout time.Duration
}

// NewDispatcher recebe os canais que o operador configurou. Um canal de
// e-mail com o Resend desligado falha de forma definitiva no envio, em vez
// de ficar pendente para sempre.
func NewDispatcher(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, reg prometheus.Registerer, notifiers ...Notifier) *Dispatcher {
	d := &Dispatcher{
		pool: pool, box: box, clk: clk, notifiers: map[Kind]Notifier{},
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_total",
			Help: "Desfechos de envio de notificação, por canal e resultado.",
		}, []string{"kind", "result"}),
		sendTimeout: sendTimeout,
	}
	for _, n := range notifiers {
		d.notifiers[n.Kind()] = n
	}
	reg.MustRegister(d.total)
	return d
}

// Run despacha a cada dispatchEvery até ctx acabar. Um lote em andamento
// termina antes de Run voltar: os envios têm prazo próprio, e não herdam o
// cancelamento do shutdown.
func (d *Dispatcher) Run(ctx context.Context) error {
	t := time.NewTicker(dispatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := d.Dispatch(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar notificações", "erro", err)
			}
		}
	}
}

// Dispatch reivindica um lote, envia em paralelo e espera todos terminarem.
// Devolve quantas notificações reivindicou.
func (d *Dispatcher) Dispatch(ctx context.Context) (int, error) {
	now := d.clk.Now()
	var lote []db.ClaimDueNotificationsRow
	err := storage.InProberTx(ctx, d.pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: now, LeaseUntil: now.Add(lease), MaxRows: maxConcurrentSends,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("reivindicar notificações: %w", err)
	}

	var wg sync.WaitGroup
	for _, n := range lote {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Um panic num canal não derruba o processo. A notificação fica
			// como está e volta quando o lease vencer.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic no envio de notificação", "notification_id", n.ID, "panic", fmt.Sprint(r))
				}
			}()
			d.entregar(n)
		}()
	}
	wg.Wait()
	return len(lote), nil
}

// desfecho é o que fazer com a notificação depois da tentativa.
type desfecho struct {
	status  string // sent, pending (retentativa), failed ou cancelled
	reason  Reason
	proxima time.Time
}

func (d *Dispatcher) entregar(n db.ClaimDueNotificationsRow) {
	f := d.tentar(n)
	d.total.WithLabelValues(n.Kind, resultado(f.status)).Inc()
	if f.status == "failed" {
		// Só o motivo vai ao log, nunca o destino.
		slog.Warn("notificação falhou de vez", "notification_id", n.ID, "channel_id", n.ChannelID, "motivo", f.reason)
	}
	if err := d.persistir(n, f); err != nil {
		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
	}
}

func (d *Dispatcher) tentar(n db.ClaimDueNotificationsRow) desfecho {
	// Spec §5.8: a verificação vale no ENVIO, não só no fan-out. Um canal
	// desabilitado depois de enfileirado não recebe mais nada.
	if !n.Enabled || n.VerifiedAt == nil {
		return desfecho{status: "cancelled"}
	}
	// Uma abertura que ainda não saiu quando o incidente já fechou fica
	// velha: o fechamento diz quando caiu e quando voltou.
	if n.Event == incident.EventOpened && n.ResolvedAt != nil {
		return desfecho{status: "cancelled"}
	}
	canal, ok := d.notifiers[Kind(n.Kind)]
	if !ok {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}
	dest, err := openConfig(d.box, n.ChannelID, n.ConfigEnc)
	if err != nil {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.sendTimeout)
	defer cancel()
	err = canal.Send(ctx, dest, Event{
		NotificationID: n.ID, Kind: n.Event, TargetName: n.TargetName,
		StartedAt: n.StartedAt, ResolvedAt: valorOuZero(n.ResolvedAt), Cause: n.Cause,
	})
	if err == nil {
		return desfecho{status: "sent"}
	}

	var se *SendError
	if !errors.As(err, &se) {
		se = &SendError{Reason: ReasonUnknown}
		if errors.Is(err, context.DeadlineExceeded) {
			se.Reason = ReasonTimeout
		}
	}
	if se.Permanent || n.Attempts >= MaxAttempts {
		return desfecho{status: "failed", reason: se.Reason}
	}
	espera := max(backoff(n.Attempts), se.RetryAfter)
	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera)}
}

// backoff dobra a cada tentativa a partir de firstBackoff: 30s, 1min,
// 2min, 4min, 8min.
func backoff(tentativa int32) time.Duration {
	d := firstBackoff
	for i := int32(1); i < tentativa && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

func (d *Dispatcher) persistir(n db.ClaimDueNotificationsRow, f desfecho) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	var motivo *string
	if f.reason != "" {
		r := string(f.reason)
		motivo = &r
	}
	return storage.InProberTx(ctx, d.pool, func(q *db.Queries) error {
		switch f.status {
		case "sent":
			return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: n.ID, Now: d.clk.Now()})
		case "pending":
			return q.RetryNotification(ctx, db.RetryNotificationParams{ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo})
		case "cancelled":
			return q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "cancelled", LastError: motivo})
		}
		if err := q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "failed", LastError: motivo}); err != nil {
			return err
		}
		// Falha definitiva vai para a trilha de auditoria (spec §8).
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		entidade := "notif_channel"
		return q.InsertAudit(ctx, db.InsertAuditParams{
			ID: id, UserID: &n.UserID, Action: "notification.failed",
			Entity: &entidade, EntityID: &n.ChannelID, At: d.clk.Now(),
		})
	})
}

// resultado é o rótulo de notify_total: "pending" vira "retry".
func resultado(status string) string {
	if status == "pending" {
		return "retry"
	}
	return status
}

func valorOuZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./internal/notify/ && go test -race -count=1 -cover ./internal/notify/`
Expected: PASS.

- [ ] **Step 5: Sabotagem — três guardas de uma vez**

Em `tentar`, troque `if !n.Enabled || n.VerifiedAt == nil {` por `if false && (!n.Enabled || n.VerifiedAt == nil) {` e `if n.Event == incident.EventOpened && n.ResolvedAt != nil {` por `if false && n.Event == incident.EventOpened && n.ResolvedAt != nil {`. Em `persistir`, faça a auditoria virar no-op: troque `return q.InsertAudit(ctx, db.InsertAuditParams{` por `_ = q.InsertAudit` + `return nil` + a linha original (código morto). Rode `go test -count=1 ./internal/notify/`.
Expected: FAIL em `TestUnverifiedChannelIsCancelledWithoutSending`, `TestDisabledChannelIsCancelledWithoutSending`, `TestStaleOpenedIsCancelledOnceTheIncidentResolved`, `TestLastAttemptFailsForGoodAndIsAudited`, `TestPermanentFailureIsFinalAtOnce` e `TestChannelWithoutNotifierFailsForGood`. Restaure e registre.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): dispatcher da outbox com lease, backoff exponencial e auditoria"
```

---
### Task 7: Cadastro de canais — verificar antes de gravar

**Files:**
- Create: `internal/notify/service.go`
- Test: `internal/notify/service_test.go`

**Interfaces:**
- Consumes: as queries de canais e `storage.InVerifyTx` (Task 2); `ValidateDiscordURL`, `NormalizeEmail`, `sealConfig`, `openConfig`, `hint`, `SendError` (Task 5); `sendTimeout`, `chaveDeTeste` (Task 6); `crypto.HashToken`.
- Produces:
  - `notify.NewChannelService(pool, box, clk, discord DiscordTester, email VerificationMailer, publicBaseURL string) *ChannelService`. `email` nil (a **interface** nil) desliga o e-mail.
  - `notify.DiscordTester` (`SendTest`) e `notify.VerificationMailer` (`SendVerification`), satisfeitas por `*Discord` e `*Resend`.
  - `Create(ctx, userID, ChannelInput{Kind; URL, Email string}) (Channel, error)`, `List(ctx, userID) ([]Channel, error)`, `Delete(ctx, userID, id) error`, `ResendVerification(ctx, userID, id) error`.
  - `CheckToken(ctx, token) (VerifyState, error)`, com `VerifyInvalid`, `VerifyPending` e `VerifyDone`, e `ConfirmToken(ctx, token) error`.
  - `notify.Channel{ID; Kind; Hint; Status ChannelStatus; Enabled bool; CreatedAt}`, com `StatusVerified`, `StatusPending` e `StatusExpired`.
  - Erros: `ErrNotFound`, `ErrLimitReached`, `ErrDeliveryFailed`, `ErrNotPending`, `ErrInvalidToken`; `*ValidationError{Field, Reason}` com `ReasonRequired`, `ReasonInvalid` e `ReasonUnavailable`.
  - `notify.MaxChannels = 10`, `notify.VerifyTTL = 24h`.

**Contexto para quem implementa.** `Create` e `ResendVerification` **enviam antes de gravar** (D11). O link é `publicBaseURL + "/verify/" + token`, com o token em base64url de 32 bytes de `crypto/rand`; o banco só vê `crypto.HashToken(token)`. `CheckToken` só **lê**: é o GET, que um leitor de links pode abrir sozinho (D2). `ConfirmToken` é o POST.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/notify/service_test.go`:

```go
package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

type testadorFalso struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (f *testadorFalso) SendTest(_ context.Context, dest crypto.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, string(dest.Reveal()))
	return f.err
}

func (f *testadorFalso) quantos() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

type carteiroFalso struct {
	mu        sync.Mutex
	enderecos []string
	links     []string
	err       error
}

func (f *carteiroFalso) SendVerification(_ context.Context, dest crypto.Secret, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enderecos = append(f.enderecos, string(dest.Reveal()))
	f.links = append(f.links, link)
	return f.err
}

// ultimoToken tira o token do último link enviado.
func (f *carteiroFalso) ultimoToken(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.links) == 0 {
		t.Fatal("nenhum link enviado")
	}
	link := f.links[len(f.links)-1]
	token, ok := strings.CutPrefix(link, "https://vigilume.dev/verify/")
	if !ok {
		t.Fatalf("link fora do formato: %s", link)
	}
	return token
}

type bancadaCanais struct {
	svc     *ChannelService
	clk     *clock.Fake
	discord *testadorFalso
	email   *carteiroFalso
	dono    uuid.UUID
	outro   uuid.UUID
}

const webhookValido = "https://discord.com/api/webhooks/123456789/token-do-webhook"

func novaBancadaCanais(t *testing.T, comEmail bool) *bancadaCanais {
	t.Helper()
	pool := testdb.New(t)
	box, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	b := &bancadaCanais{
		clk:     clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)),
		discord: &testadorFalso{}, email: &carteiroFalso{},
		dono: uuid.Must(uuid.NewV7()), outro: uuid.Must(uuid.NewV7()),
	}
	for _, id := range []uuid.UUID{b.dono, b.outro} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
			id, id.String()+"@exemplo.com"); err != nil {
			t.Fatal(err)
		}
	}
	var mailer VerificationMailer
	if comEmail {
		mailer = b.email
	}
	b.svc = NewChannelService(pool, box, b.clk, b.discord, mailer, "https://vigilume.dev/")
	return b
}

func (b *bancadaCanais) listar(t *testing.T, userID uuid.UUID) []Channel {
	t.Helper()
	cs, err := b.svc.List(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestCreateDiscordSendsTheTestAndSavesVerified(t *testing.T) {
	b := novaBancadaCanais(t, true)
	c, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: " " + webhookValido + " "})
	if err != nil {
		t.Fatal(err)
	}
	if b.discord.quantos() != 1 || b.discord.urls[0] != webhookValido {
		t.Errorf("mensagens de teste = %v", b.discord.urls)
	}
	if c.Status != StatusVerified || c.Hint != "webhook …6789" {
		t.Errorf("canal = %+v", c)
	}
	cs := b.listar(t, b.dono)
	if len(cs) != 1 || cs[0].ID != c.ID || cs[0].Status != StatusVerified || cs[0].Hint != "webhook …6789" {
		t.Errorf("listagem = %+v", cs)
	}
}

// Envia ANTES de gravar: falhou o envio, nada fica no banco.
func TestFailedDeliverySavesNothing(t *testing.T) {
	b := novaBancadaCanais(t, true)
	b.discord.err = &SendError{Reason: ReasonRejected, Permanent: true}
	b.email.err = &SendError{Reason: ReasonServerError}

	for _, in := range []ChannelInput{
		{Kind: KindDiscord, URL: webhookValido},
		{Kind: KindEmail, Email: "pessoa@exemplo.com"},
	} {
		if _, err := b.svc.Create(context.Background(), b.dono, in); !errors.Is(err, ErrDeliveryFailed) {
			t.Errorf("%s com envio falho deu %v, queria ErrDeliveryFailed", in.Kind, err)
		}
	}
	if cs := b.listar(t, b.dono); len(cs) != 0 {
		t.Errorf("%d canais gravados depois de envios que falharam", len(cs))
	}
}

func TestInvalidInputIsRejectedBeforeAnySend(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for _, c := range []struct {
		in            ChannelInput
		campo, motivo string
	}{
		{ChannelInput{}, "kind", ReasonRequired},
		{ChannelInput{Kind: "slack"}, "kind", ReasonInvalid},
		{ChannelInput{Kind: KindDiscord}, "url", ReasonRequired},
		{ChannelInput{Kind: KindDiscord, URL: "https://evil.io/api/webhooks/1/t"}, "url", ReasonInvalid},
		{ChannelInput{Kind: KindEmail}, "email", ReasonRequired},
		{ChannelInput{Kind: KindEmail, Email: "Fulano <p@x.com>"}, "email", ReasonInvalid},
	} {
		_, err := b.svc.Create(context.Background(), b.dono, c.in)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Field != c.campo || verr.Reason != c.motivo {
			t.Errorf("Create(%+v) = %v, queria %s/%s", c.in, err, c.campo, c.motivo)
		}
	}
	if b.discord.quantos() != 0 || len(b.email.links) != 0 {
		t.Error("entrada inválida chegou a disparar envio")
	}
}

func TestEmailIsUnavailableWithoutResend(t *testing.T) {
	b := novaBancadaCanais(t, false)
	_, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Field != "kind" || verr.Reason != ReasonUnavailable {
		t.Errorf("e-mail sem Resend deu %v, queria kind/unavailable", err)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "pessoa@Exemplo.com"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusPending || c.Hint != "p***@exemplo.com" || b.email.enderecos[0] != "pessoa@exemplo.com" {
		t.Errorf("canal = %+v, endereço = %v", c, b.email.enderecos)
	}
	token := b.email.ultimoToken(t)

	if st, err := b.svc.CheckToken(ctx, token); err != nil || st != VerifyPending {
		t.Fatalf("CheckToken = %v, %v; queria pendente", st, err)
	}
	// Olhar não verifica: é o que protege do leitor de links (D2).
	if cs := b.listar(t, b.dono); cs[0].Status != StatusPending {
		t.Fatalf("o GET verificou o canal: %+v", cs[0])
	}

	if err := b.svc.ConfirmToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if cs := b.listar(t, b.dono); cs[0].Status != StatusVerified {
		t.Errorf("depois de confirmar: %+v", cs[0])
	}
	if st, _ := b.svc.CheckToken(ctx, token); st != VerifyDone {
		t.Errorf("CheckToken depois de confirmar = %v, queria VerifyDone", st)
	}
	if err := b.svc.ConfirmToken(ctx, token); err != nil {
		t.Errorf("segunda confirmação deu %v, queria sucesso idempotente", err)
	}
}

func TestExpiredTokenNeitherChecksNorConfirms(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	if _, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"}); err != nil {
		t.Fatal(err)
	}
	token := b.email.ultimoToken(t)
	b.clk.Advance(VerifyTTL)

	if st, _ := b.svc.CheckToken(ctx, token); st != VerifyInvalid {
		t.Errorf("CheckToken vencido = %v", st)
	}
	if err := b.svc.ConfirmToken(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ConfirmToken vencido = %v", err)
	}
	if cs := b.listar(t, b.dono); cs[0].Status != StatusExpired {
		t.Errorf("status = %s, queria expired", cs[0].Status)
	}
}

func TestMalformedTokenIsInvalid(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for _, token := range []string{"", "curto", strings.Repeat("A", 43) + "=", "../../etc"} {
		if st, err := b.svc.CheckToken(context.Background(), token); err != nil || st != VerifyInvalid {
			t.Errorf("CheckToken(%q) = %v, %v", token, st, err)
		}
		if err := b.svc.ConfirmToken(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("ConfirmToken(%q) = %v", token, err)
		}
	}
}

// Reenviar troca o token: o link antigo para de valer. Vale também para
// um pendente que já venceu.
func TestResendReplacesTheToken(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	antigo := b.email.ultimoToken(t)
	b.clk.Advance(VerifyTTL + time.Hour)

	if err := b.svc.ResendVerification(ctx, b.dono, c.ID); err != nil {
		t.Fatal(err)
	}
	novo := b.email.ultimoToken(t)
	if novo == antigo || len(b.email.enderecos) != 2 || b.email.enderecos[1] != "p@x.com" {
		t.Fatalf("reenvio: token novo=%v, endereços=%v", novo != antigo, b.email.enderecos)
	}
	if st, _ := b.svc.CheckToken(ctx, antigo); st != VerifyInvalid {
		t.Errorf("token antigo depois do reenvio = %v", st)
	}
	if st, _ := b.svc.CheckToken(ctx, novo); st != VerifyPending {
		t.Errorf("token novo = %v", st)
	}
}

func TestResendRefusesWhatIsNotPending(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	discord, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	email, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmToken(ctx, b.email.ultimoToken(t)); err != nil {
		t.Fatal(err)
	}

	for nome, id := range map[string]uuid.UUID{"discord": discord.ID, "e-mail verificado": email.ID} {
		if err := b.svc.ResendVerification(ctx, b.dono, id); !errors.Is(err, ErrNotPending) {
			t.Errorf("reenviar %s deu %v, queria ErrNotPending", nome, err)
		}
	}
	if err := b.svc.ResendVerification(ctx, b.outro, email.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reenviar canal de outro usuário deu %v, queria ErrNotFound", err)
	}
}

// O limite é conferido ANTES do envio: o 11º canal não chega a mandar a
// mensagem de teste. (A segunda conferência, na gravação, é a que vale sob
// concorrência; este teste trava a primeira.)
func TestChannelLimitIsCheckedBeforeSending(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for range MaxChannels {
		if _, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("canal %d deu %v, queria ErrLimitReached", MaxChannels+1, err)
	}
	if n := b.discord.quantos(); n != MaxChannels {
		t.Errorf("%d mensagens de teste, queria %d: o canal recusado também recebeu", n, MaxChannels)
	}
}

func TestAnotherUsersChannelIsInvisible(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	if cs := b.listar(t, b.outro); len(cs) != 0 {
		t.Errorf("o outro usuário listou %d canais", len(cs))
	}
	if err := b.svc.Delete(ctx, b.outro, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("apagar canal alheio deu %v", err)
	}
	if err := b.svc.Delete(ctx, b.dono, c.ID); err != nil {
		t.Errorf("o dono apagando o próprio canal deu %v", err)
	}
	if cs := b.listar(t, b.dono); len(cs) != 0 {
		t.Errorf("depois do DELETE, %d canais", len(cs))
	}
}

func TestUnreadableChannelDoesNotBreakTheList(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := b.svc.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notif_channels SET config_enc = '\x00ff' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	cs := b.listar(t, b.dono)
	if len(cs) != 1 || cs[0].Hint != "" || cs[0].Status != StatusVerified {
		t.Errorf("listagem com linha ilegível = %+v", cs)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/notify/`
Expected: FAIL de compilação: `undefined: ChannelService`, `undefined: NewChannelService` e os demais.

- [ ] **Step 3: Implementar**

Crie `internal/notify/service.go`:

```go
package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

const (
	// MaxChannels é o limite de canais por usuário. Canal é destino de
	// envio: sem teto, cada canal novo seria mais um alvo de mensagem.
	MaxChannels = 10
	// VerifyTTL é a validade do link de verificação (spec §5.8).
	VerifyTTL = 24 * time.Hour
	// tokenBytes é o tamanho do token de verificação, como o do refresh
	// (spec §5.2): 256 bits de crypto/rand.
	tokenBytes = 32
)

var (
	ErrNotFound       = errors.New("canal não encontrado")
	ErrLimitReached   = errors.New("limite de canais atingido")
	ErrDeliveryFailed = errors.New("a mensagem de verificação não foi entregue")
	ErrNotPending     = errors.New("o canal não está pendente de verificação")
	ErrInvalidToken   = errors.New("token de verificação inválido ou vencido")
)

// Motivos de ValidationError, no vocabulário fechado da API (D14 do Plano 2).
const (
	ReasonRequired    = "required"
	ReasonInvalid     = "invalid"
	ReasonUnavailable = "unavailable"
)

// ValidationError aponta o campo culpado. Field e Reason vêm sempre de
// constantes do código, nunca da entrada.
type ValidationError struct {
	Field, Reason string
}

func (e *ValidationError) Error() string { return "canal inválido: " + e.Field + " " + e.Reason }

// DiscordTester manda a mensagem de teste do cadastro. *Discord satisfaz.
type DiscordTester interface {
	SendTest(ctx context.Context, dest crypto.Secret) error
}

// VerificationMailer manda o link de verificação. *Resend satisfaz.
type VerificationMailer interface {
	SendVerification(ctx context.Context, dest crypto.Secret, link string) error
}

// Channel é o que sai do domínio. Não tem campo para o destino: só o hint
// (D5 do Plano 2, aplicado aos canais).
type Channel struct {
	ID        uuid.UUID
	Kind      Kind
	Hint      string
	Status    ChannelStatus
	Enabled   bool
	CreatedAt time.Time
}

type ChannelStatus string

const (
	StatusVerified ChannelStatus = "verified"
	StatusPending  ChannelStatus = "pending"
	StatusExpired  ChannelStatus = "expired"
)

type ChannelInput struct {
	Kind  Kind
	URL   string // Discord
	Email string // e-mail
}

// ChannelService é o cadastro de canais (spec §8, §5.8).
type ChannelService struct {
	pool    *pgxpool.Pool
	box     *crypto.Box
	clk     clock.Clock
	discord DiscordTester
	// email é nil quando o operador não configurou o Resend: cadastrar
	// e-mail responde erro de validação no campo kind.
	email   VerificationMailer
	baseURL string
}

// NewChannelService recebe publicBaseURL para montar o link de verificação.
// Passe um email nil (a interface nil, e não um *Resend nil) para desligar
// o e-mail.
func NewChannelService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, discord DiscordTester, email VerificationMailer, publicBaseURL string) *ChannelService {
	return &ChannelService{
		pool: pool, box: box, clk: clk, discord: discord, email: email,
		baseURL: strings.TrimSuffix(publicBaseURL, "/"),
	}
}

// Create verifica o destino ANTES de gravar: a mensagem de teste do Discord
// ou o e-mail com o link. Se o envio falha, nada é gravado, e o cliente
// recebe ErrDeliveryFailed.
//
// O limite é conferido duas vezes. Antes do envio, para não mandar mensagem
// a um canal que não será gravado; na gravação, com o usuário travado, para
// que duas criações simultâneas não passem as duas.
func (s *ChannelService) Create(ctx context.Context, userID uuid.UUID, in ChannelInput) (Channel, error) {
	dest, err := s.validar(in)
	if err != nil {
		return Channel{}, err
	}
	if err := s.cabe(ctx, userID, false); err != nil {
		return Channel{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Channel{}, fmt.Errorf("gerar id do canal: %w", err)
	}
	sealed, err := sealConfig(s.box, id, dest)
	if err != nil {
		return Channel{}, err
	}
	now := s.clk.Now()
	p := db.InsertChannelParams{ID: id, UserID: userID, Kind: string(in.Kind), ConfigEnc: sealed, CreatedAt: now}
	out := Channel{ID: id, Kind: in.Kind, Hint: hint(in.Kind, crypto.Secret(dest)), Enabled: true, CreatedAt: now}

	envio, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	switch in.Kind {
	case KindDiscord:
		if err := s.discord.SendTest(envio, crypto.Secret(dest)); err != nil {
			slog.Warn("mensagem de teste do Discord falhou", "motivo", motivo(err))
			return Channel{}, ErrDeliveryFailed
		}
		p.VerifiedAt = &now
		out.Status = StatusVerified
	case KindEmail:
		token, hash, err := novoToken()
		if err != nil {
			return Channel{}, err
		}
		expira := now.Add(VerifyTTL)
		if err := s.email.SendVerification(envio, crypto.Secret(dest), s.link(token)); err != nil {
			slog.Warn("e-mail de verificação falhou", "motivo", motivo(err))
			return Channel{}, ErrDeliveryFailed
		}
		p.VerifyTokenHash, p.VerifyExpiresAt = hash, &expira
		out.Status = StatusPending
	}

	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := s.cabeNa(ctx, q, userID, true); err != nil {
			return err
		}
		return q.InsertChannel(ctx, p)
	})
	if err != nil {
		return Channel{}, err
	}
	return out, nil
}

func (s *ChannelService) validar(in ChannelInput) (string, error) {
	switch in.Kind {
	case KindDiscord:
		u := strings.TrimSpace(in.URL)
		if u == "" {
			return "", &ValidationError{"url", ReasonRequired}
		}
		if ValidateDiscordURL(u) != nil {
			return "", &ValidationError{"url", ReasonInvalid}
		}
		return u, nil
	case KindEmail:
		if s.email == nil {
			return "", &ValidationError{"kind", ReasonUnavailable}
		}
		if strings.TrimSpace(in.Email) == "" {
			return "", &ValidationError{"email", ReasonRequired}
		}
		addr, ok := NormalizeEmail(in.Email)
		if !ok {
			return "", &ValidationError{"email", ReasonInvalid}
		}
		return addr, nil
	case "":
		return "", &ValidationError{"kind", ReasonRequired}
	default:
		return "", &ValidationError{"kind", ReasonInvalid}
	}
}

func (s *ChannelService) cabe(ctx context.Context, userID uuid.UUID, travar bool) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		return s.cabeNa(ctx, q, userID, travar)
	})
}

func (s *ChannelService) cabeNa(ctx context.Context, q *db.Queries, userID uuid.UUID, travar bool) error {
	if travar {
		if err := q.LockUser(ctx, userID); err != nil {
			return fmt.Errorf("travar usuário: %w", err)
		}
	}
	n, err := q.CountChannels(ctx, userID)
	if err != nil {
		return fmt.Errorf("contar canais: %w", err)
	}
	if n >= MaxChannels {
		return ErrLimitReached
	}
	return nil
}

// List devolve os canais do usuário. Uma linha que não decifra (chave
// trocada, linha adulterada) sai sem hint, em vez de derrubar a lista
// inteira: é o defeito que o Plano 2 deixou em targets.List.
func (s *ChannelService) List(ctx context.Context, userID uuid.UUID) ([]Channel, error) {
	var out []Channel
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListChannels(ctx, userID)
		if err != nil {
			return fmt.Errorf("listar canais: %w", err)
		}
		out = make([]Channel, 0, len(rows))
		for _, r := range rows {
			out = append(out, s.toChannel(r))
		}
		return nil
	})
	return out, err
}

func (s *ChannelService) toChannel(r db.NotifChannel) Channel {
	c := Channel{ID: r.ID, Kind: Kind(r.Kind), Enabled: r.Enabled, CreatedAt: r.CreatedAt}
	if dest, err := openConfig(s.box, r.ID, r.ConfigEnc); err == nil {
		c.Hint = hint(c.Kind, dest)
	} else {
		slog.Error("canal com destino ilegível", "channel_id", r.ID)
	}
	switch {
	case r.VerifiedAt != nil:
		c.Status = StatusVerified
	case r.VerifyExpiresAt != nil && !s.clk.Now().Before(*r.VerifyExpiresAt):
		c.Status = StatusExpired
	default:
		c.Status = StatusPending
	}
	return c
}

func (s *ChannelService) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: userID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar canal: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ResendVerification manda um link novo a um e-mail pendente (vencido ou
// não) e invalida o anterior. Envia antes de trocar o token: se o envio
// falha, o link antigo continua valendo.
func (s *ChannelService) ResendVerification(ctx context.Context, userID, id uuid.UUID) error {
	var row db.NotifChannel
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		row, err = q.GetChannel(ctx, db.GetChannelParams{UserID: userID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	if Kind(row.Kind) != KindEmail || row.VerifiedAt != nil {
		return ErrNotPending
	}
	if s.email == nil {
		return &ValidationError{"kind", ReasonUnavailable}
	}
	dest, err := openConfig(s.box, row.ID, row.ConfigEnc)
	if err != nil {
		return err
	}
	token, hash, err := novoToken()
	if err != nil {
		return err
	}
	envio, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.email.SendVerification(envio, dest, s.link(token)); err != nil {
		slog.Warn("e-mail de verificação falhou", "motivo", motivo(err))
		return ErrDeliveryFailed
	}
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.RenewVerification(ctx, db.RenewVerificationParams{
			UserID: userID, ID: id, TokenHash: hash, ExpiresAt: s.clk.Now().Add(VerifyTTL),
		})
		if err != nil {
			return fmt.Errorf("renovar token: %w", err)
		}
		if n == 0 {
			// Verificado ou apagado entre a leitura e aqui.
			return ErrNotPending
		}
		return nil
	})
}

// VerifyState é o que a página GET /verify/{token} mostra.
type VerifyState int

const (
	VerifyInvalid VerifyState = iota
	VerifyPending
	VerifyDone
)

// CheckToken só lê: é o GET, que um leitor de links do provedor de e-mail
// pode disparar sozinho, sem o dono (D2 deste plano).
func (s *ChannelService) CheckToken(ctx context.Context, token string) (VerifyState, error) {
	hash, ok := hashDoToken(token)
	if !ok {
		return VerifyInvalid, nil
	}
	var estado VerifyState
	err := storage.InVerifyTx(ctx, s.pool, hash, func(q *db.Queries) error {
		row, err := q.FindChannelByVerification(ctx, hash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			estado = VerifyInvalid
		case err != nil:
			return fmt.Errorf("buscar token: %w", err)
		case row.VerifiedAt != nil:
			estado = VerifyDone
		case row.VerifyExpiresAt == nil || !s.clk.Now().Before(*row.VerifyExpiresAt):
			estado = VerifyInvalid
		default:
			estado = VerifyPending
		}
		return nil
	})
	return estado, err
}

// ConfirmToken é o POST: verifica o canal. Confirmar de novo um canal já
// verificado dá certo e não muda nada.
func (s *ChannelService) ConfirmToken(ctx context.Context, token string) error {
	hash, ok := hashDoToken(token)
	if !ok {
		return ErrInvalidToken
	}
	return storage.InVerifyTx(ctx, s.pool, hash, func(q *db.Queries) error {
		n, err := q.ConfirmChannel(ctx, db.ConfirmChannelParams{TokenHash: hash, Now: s.clk.Now()})
		if err != nil {
			return fmt.Errorf("confirmar canal: %w", err)
		}
		if n == 0 {
			return ErrInvalidToken
		}
		return nil
	})
}

func (s *ChannelService) link(token string) string { return s.baseURL + "/verify/" + token }

// novoToken devolve o token (vai no link) e o hash dele (vai para o banco).
// O banco nunca vê o token: um dump não verifica canal nenhum.
func novoToken() (string, []byte, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("gerar token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, crypto.HashToken(token), nil
}

// hashDoToken recusa, sem ir ao banco, o que não pode ser um token nosso.
func hashDoToken(token string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != tokenBytes {
		return nil, false
	}
	return crypto.HashToken(token), true
}

// motivo reduz a falha de envio ao Reason, que é o que pode ir ao log.
func motivo(err error) Reason {
	var se *SendError
	if errors.As(err, &se) {
		return se.Reason
	}
	return ReasonUnknown
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./internal/notify/ && go test -race -count=1 -cover ./internal/notify/`
Expected: PASS.

- [ ] **Step 5: Sabotagem — duas guardas**

1. Em `Create`, apague a pré-checagem `if err := s.cabe(ctx, userID, false); err != nil { return Channel{}, err }`. Rode `-run TestChannelLimit`. Expected: FAIL com "11 mensagens de teste, queria 10".
2. Em `CheckToken`, logo depois do `if !ok { ... }`, acrescente `_ = s.ConfirmToken(ctx, token)`. É o GET que verifica. Rode `-run 'TestEmailVerificationFlow|TestResendReplaces'`. Expected: FAIL nos dois: o próprio GET deixou o token confirmado (estado 2, `VerifyDone`).

Restaure e registre.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): cadastro de canais com verificação antes de gravar e limite por usuário"
```

---
### Task 8: API de incidentes e `/readyz`

**Files:**
- Create: `internal/incident/history/history.go`
- Test: `internal/incident/history/history_test.go`
- Create: `internal/httpapi/incidenthandlers.go`
- Modify: `internal/apierr/errors.go` (`CodeUnavailable`)
- Modify: `internal/httpapi/routes.go` (`Deps.Incidents`, `Deps.Ready`, rotas de incidentes, `/readyz`)
- Modify: `internal/httpapi/auth_integration_test.go` (`newAPICom`, `Incidents` e `Ready` no `Deps` de teste)
- Test: `internal/httpapi/incidents_integration_test.go`

**Interfaces:**
- Consumes: `ListIncidents`, `AckIncident` (Task 2); os helpers de handler do Plano 2 (`usuario`, `pathID`, `campoInvalido`, `targets.ReasonInvalid`, `targets.ReasonOutOfRange`); os helpers de teste do Plano 2 (`newAPIWith`, `chamar`, `lerCorpo`, `tokenDe`, `outroUsuario`, `decodeEnvelope`, `alvoValido`, `alvoJSON`).
- Produces:
  - `history.NewService(pool, clk) *Service`; `List(ctx, userID, Filter{Open *bool; Before *uuid.UUID; Limit int32}) ([]Incident, error)`; `Ack(ctx, userID, id) (Incident, error)`; `history.ErrNotFound`; `DefaultPageSize = 50`, `MaxPageSize = 100`.
  - `GET /api/v1/incidents?status=open|resolved&cursor=&limit=` → `{"items":[...],"next_cursor"?}`; `POST /api/v1/incidents/{id}/ack` → o incidente. Cada item tem `id`, `target_id`, `target_name`, `started_at`, `resolved_at`, `cause` e `acknowledged_at`.
  - `GET /readyz` → 200, ou 503 `unavailable`.
  - `httpapi.Deps` ganha `Incidents *history.Service` e `Ready func(context.Context) error`.
  - Teste: `newAPICom(t, ajustarCfg func(*config.Config), ajustarDeps func(*httpapi.Deps)) *api` e `envelopeComCampos`, usados pelas Tasks 9 e 10.

**Contexto para quem implementa.** `limit` não numérico é `invalid`, e fora da faixa é `out_of_range` (D22; a Task 10 alinha `/targets`). O `/readyz` responde 503 também quando `Ready` é nil (D21). Dois testes antigos (`TestLoginDoesNotRevealWhetherAccountExists` e `TestLockedAccountIsIndistinguishableFromUnknownAccount`) comparam envelopes com `!=`; por isso o envelope com `fields` é um **tipo à parte**, e não um campo novo em `envelope`.

Cada caso de `TestReadyzChecksTheDatabase` sobe a própria API num **subteste**. O `newAPI` cria sempre o mesmo e-mail, e só o cleanup do subteste restaura o banco antes da próxima.

- [ ] **Step 1: Escrever os testes do serviço (falham)**

Crie `internal/incident/history/history_test.go`:

```go
package history_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/incident/history"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

var agora = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

type bancada struct {
	pool *pgxpool.Pool
	clk  *clock.Fake
	svc  *history.Service
}

func novaBancada(t *testing.T) *bancada {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	return &bancada{pool: pool, clk: clk, svc: history.NewService(pool, clk)}
}

func (b *bancada) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := b.pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) alvo(t *testing.T, dono uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, b.pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: nome, URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// incidente abre um incidente e, se resolvido, o fecha 10 minutos depois.
func (b *bancada) incidente(t *testing.T, alvo uuid.UUID, resolvido bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "dns"}); err != nil {
			return err
		}
		if resolvido {
			_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: id, ResolvedAt: agora.Add(10 * time.Minute)})
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ids(is []history.Incident) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(is))
	for _, i := range is {
		out = append(out, i.ID)
	}
	return out
}

func TestListFiltersAndOrdersNewestFirst(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	velho := b.incidente(t, alvo, true)
	aberto := b.incidente(t, alvo, false)

	abertos, fechados := true, false
	for nome, c := range map[string]struct {
		f    history.Filter
		quer []uuid.UUID
	}{
		"todos":      {history.Filter{}, []uuid.UUID{aberto, velho}},
		"abertos":    {history.Filter{Open: &abertos}, []uuid.UUID{aberto}},
		"resolvidos": {history.Filter{Open: &fechados}, []uuid.UUID{velho}},
	} {
		got, err := b.svc.List(ctx, dono, c.f)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != len(c.quer) || (len(g) > 0 && g[0] != c.quer[0]) {
			t.Errorf("%s: %v, queria %v", nome, g, c.quer)
		}
	}

	got, _ := b.svc.List(ctx, dono, history.Filter{})
	if got[0].TargetName != "API" || got[0].ResolvedAt != nil || got[1].ResolvedAt == nil || got[1].Cause != "dns" {
		t.Errorf("itens = %+v", got)
	}
}

func TestListPaginatesByCursor(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	var todos []uuid.UUID
	for range 3 {
		todos = append(todos, b.incidente(t, alvo, true))
	}

	pagina, err := b.svc.List(ctx, dono, history.Filter{Limit: 2})
	if err != nil || len(pagina) != 2 || pagina[0].ID != todos[2] {
		t.Fatalf("primeira página = %v, %v", ids(pagina), err)
	}
	cursor := pagina[1].ID
	resto, err := b.svc.List(ctx, dono, history.Filter{Limit: 2, Before: &cursor})
	if err != nil || len(resto) != 1 || resto[0].ID != todos[0] {
		t.Errorf("segunda página = %v, %v", ids(resto), err)
	}
}

func TestAckIsIdempotentAndScopedToTheOwner(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono, intruso := b.usuario(t), b.usuario(t)
	inc := b.incidente(t, b.alvo(t, dono, "API"), false)

	if _, err := b.svc.Ack(ctx, intruso, inc); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("ack pelo intruso deu %v, queria ErrNotFound", err)
	}
	if got, _ := b.svc.List(ctx, intruso, history.Filter{}); len(got) != 0 {
		t.Errorf("o intruso listou %d incidentes", len(got))
	}

	primeiro, err := b.svc.Ack(ctx, dono, inc)
	if err != nil || primeiro.AckAt == nil || !primeiro.AckAt.Equal(agora) {
		t.Fatalf("primeiro ack = %+v, %v", primeiro, err)
	}
	b.clk.Advance(time.Hour)
	segundo, err := b.svc.Ack(ctx, dono, inc)
	if err != nil || !segundo.AckAt.Equal(agora) {
		t.Errorf("segundo ack = %v, %v; queria o instante do primeiro", segundo.AckAt, err)
	}
	if _, err := b.svc.Ack(ctx, dono, uuid.Must(uuid.NewV7())); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("ack de id inexistente deu %v", err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/incident/history/`
Expected: FAIL de compilação; o pacote não existe.

- [ ] **Step 3: Implementar o serviço**

Crie `internal/incident/history/history.go`:

```go
// Package history é o histórico de incidentes do dono (GET /incidents e o
// reconhecimento). Mora fora de internal/incident para que o detector
// continue puro, sem banco (spec §3.2).
package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var ErrNotFound = errors.New("incidente não encontrado")

type Incident struct {
	ID         uuid.UUID
	TargetID   uuid.UUID
	TargetName string
	StartedAt  time.Time
	ResolvedAt *time.Time // nil enquanto aberto
	Cause      string
	AckAt      *time.Time
}

// Filter: Open nil lista tudo; true só abertos; false só resolvidos. Before
// é o cursor, o último id da página anterior.
type Filter struct {
	Open   *bool
	Before *uuid.UUID
	Limit  int32
}

const (
	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100
)

type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, f Filter) ([]Incident, error) {
	if f.Limit < 1 || f.Limit > MaxPageSize {
		f.Limit = DefaultPageSize
	}
	var out []Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListIncidents(ctx, db.ListIncidentsParams{
			UserID: userID, Open: f.Open, Before: f.Before, MaxRows: f.Limit,
		})
		if err != nil {
			return fmt.Errorf("listar incidentes: %w", err)
		}
		out = make([]Incident, 0, len(rows))
		for _, r := range rows {
			out = append(out, Incident{
				ID: r.ID, TargetID: r.TargetID, TargetName: r.TargetName, StartedAt: r.StartedAt,
				ResolvedAt: r.ResolvedAt, Cause: r.Cause, AckAt: r.AckAt,
			})
		}
		return nil
	})
	return out, err
}

// Ack reconhece o incidente. É idempotente: o segundo ack devolve o
// instante do primeiro. Incidente de outro usuário é ErrNotFound (spec §5.5).
func (s *Service) Ack(ctx context.Context, userID, id uuid.UUID) (Incident, error) {
	var out Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		r, err := q.AckIncident(ctx, db.AckIncidentParams{UserID: userID, ID: id, Now: s.clk.Now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reconhecer incidente: %w", err)
		}
		out = Incident{
			ID: r.ID, TargetID: r.TargetID, TargetName: r.TargetName, StartedAt: r.StartedAt,
			ResolvedAt: r.ResolvedAt, Cause: r.Cause, AckAt: r.AckAt,
		}
		return nil
	})
	return out, err
}
```

Run: `go test -race -count=1 ./internal/incident/history/`
Expected: PASS.

- [ ] **Step 4: Escrever os testes da API (falham)**

Em `internal/httpapi/auth_integration_test.go`:

1. Acrescente `"github.com/BipoXDXD/vigilume/internal/incident/history"` aos imports.
2. Troque o cabeçalho de `newAPIWith` (do comentário até `t.Helper()`) por:

```go
// newAPIWith sobe a API real (rotas, middlewares e Postgres) com um usuário
// já cadastrado. O ajuste opcional permite que um teste mexa na configuração
// antes de as rotas serem montadas.
func newAPIWith(t *testing.T, ajustar func(*config.Config)) *api {
	t.Helper()
	return newAPICom(t, ajustar, nil)
}

// newAPICom é newAPIWith com um segundo ajuste, nas dependências, para o
// teste que precisa trocar uma delas (o ping de /readyz, por exemplo).
func newAPICom(t *testing.T, ajustar func(*config.Config), ajustarDeps func(*httpapi.Deps)) *api {
	t.Helper()
```

3. Troque a montagem do servidor (`srv := httptest.NewServer(httpapi.Routes(ctx, cfg, httpapi.Deps{...}))`) por:

```go
	deps := httpapi.Deps{
		Store: store, Sessions: mgr, TOTP: segundoFator,
		Targets:   targets.NewService(pool, box, clk),
		Incidents: history.NewService(pool, clk),
		Clock:     clk, Ready: pool.Ping,
	}
	if ajustarDeps != nil {
		ajustarDeps(&deps)
	}
	srv := httptest.NewServer(httpapi.Routes(ctx, cfg, deps))
```

Crie `internal/httpapi/incidents_integration_test.go`:

```go
package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

type incidenteJSON struct {
	ID             string     `json:"id"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	Cause          string     `json:"cause"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

// envelopeComCampos é o envelope com error.fields. Tipo à parte porque dois
// testes antigos comparam envelopes com !=, e um slice no struct o impediria.
type envelopeComCampos struct {
	Error struct {
		Code   string              `json:"code"`
		Fields []apierr.FieldError `json:"fields"`
	} `json:"error"`
}

type listaIncidentesJSON struct {
	Items      []incidenteJSON `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

// incidentesDe cria um alvo do usuário pela API e abre incidentes nele pelo
// escopo do sondador, que é quem os abre de verdade. Todos, menos o último,
// saem resolvidos (só pode haver um aberto por alvo).
func incidentesDe(t *testing.T, a *api, token string, n int) (alvo string, ids []string) {
	t.Helper()
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", token, alvoValido), &criado)
	ctx := context.Background()
	for i := range n {
		id := uuid.Must(uuid.NewV7())
		err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
			if err := q.OpenIncident(ctx, db.OpenIncidentParams{
				ID: id, TargetID: uuid.MustParse(criado.ID), StartedAt: a.clk.Now(), Cause: "timeout",
			}); err != nil {
				return err
			}
			if i < n-1 {
				_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: id, ResolvedAt: a.clk.Now().Add(time.Minute)})
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
	}
	return criado.ID, ids
}

func TestIncidentListFiltersAndAck(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	alvo, ids := incidentesDe(t, a, tok, 2)
	resolvido, aberto := ids[0], ids[1]

	var todos listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents", tok, nil), &todos)
	if len(todos.Items) != 2 || todos.Items[0].ID != aberto || todos.Items[0].TargetID != alvo ||
		todos.Items[0].TargetName != "API" || todos.Items[0].Cause != "timeout" || todos.Items[1].ResolvedAt == nil {
		t.Fatalf("lista = %+v", todos.Items)
	}
	for status, quer := range map[string]string{"open": aberto, "resolved": resolvido} {
		var l listaIncidentesJSON
		lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?status="+status, tok, nil), &l)
		if len(l.Items) != 1 || l.Items[0].ID != quer {
			t.Errorf("status=%s: %+v", status, l.Items)
		}
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/incidents/"+aberto+"/ack", tok, nil)
	var ack incidenteJSON
	lerCorpo(t, resp, &ack)
	if resp.StatusCode != http.StatusOK || ack.AcknowledgedAt == nil || !ack.AcknowledgedAt.Equal(a.clk.Now()) {
		t.Fatalf("ack deu %d: %+v", resp.StatusCode, ack)
	}
	primeiro := *ack.AcknowledgedAt
	a.clk.Advance(time.Hour)
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/incidents/"+aberto+"/ack", tok, nil), &ack)
	if !ack.AcknowledgedAt.Equal(primeiro) {
		t.Errorf("segundo ack mudou acknowledged_at para %v", ack.AcknowledgedAt)
	}
}

func TestIncidentListRejectsBadQueries(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for consulta, quer := range map[string]apierr.FieldError{
		"status=aberto":     {Field: "status", Reason: "invalid"},
		"limit=0":           {Field: "limit", Reason: "out_of_range"},
		"limit=101":         {Field: "limit", Reason: "out_of_range"},
		"limit=x":           {Field: "limit", Reason: "invalid"},
		"cursor=nao-e-uuid": {Field: "cursor", Reason: "invalid"},
	} {
		var env envelopeComCampos
		resp := chamar(t, a, http.MethodGet, "/api/v1/incidents?"+consulta, tok, nil)
		lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0] != quer {
			t.Errorf("%s deu %d/%+v, queria 400 %+v", consulta, resp.StatusCode, env.Error.Fields, quer)
		}
	}
}

func TestIncidentListPaginates(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	_, ids := incidentesDe(t, a, tok, 3)

	var p1 listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?limit=2", tok, nil), &p1)
	if len(p1.Items) != 2 || p1.NextCursor != ids[1] {
		t.Fatalf("primeira página: %d itens, cursor %q", len(p1.Items), p1.NextCursor)
	}
	var p2 listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?limit=2&cursor="+p1.NextCursor, tok, nil), &p2)
	if len(p2.Items) != 1 || p2.Items[0].ID != ids[0] || p2.NextCursor != "" {
		t.Errorf("segunda página: %+v, cursor %q", p2.Items, p2.NextCursor)
	}
}

func TestAnotherUsersIncidentIs404(t *testing.T) {
	a := newAPI(t)
	_, ids := incidentesDe(t, a, tokenDe(t, a, a.userID), 1)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	resp := chamar(t, a, http.MethodPost, "/api/v1/incidents/"+ids[0]+"/ack", intruso, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ack pelo intruso deu %d, queria 404", resp.StatusCode)
	}
	var l listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents", intruso, nil), &l)
	if len(l.Items) != 0 {
		t.Errorf("o intruso listou %d incidentes", len(l.Items))
	}
	resp = chamar(t, a, http.MethodPost, "/api/v1/incidents/nao-e-uuid/ack", tokenDe(t, a, a.userID), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("id malformado deu %d, queria 404 (D16 do Plano 2)", resp.StatusCode)
	}
}

func TestIncidentRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/incidents"},
		{http.MethodPost, "/api/v1/incidents/" + uuid.NewString() + "/ack"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// Cada caso sobe a própria API num subteste: o cleanup do subteste restaura
// o banco, e o usuário fixo de newAPI pode ser criado de novo.
func TestReadyzChecksTheDatabase(t *testing.T) {
	t.Run("banco no ar", func(t *testing.T) {
		readyzDeve(t, newAPI(t), http.StatusOK)
	})
	t.Run("banco fora", func(t *testing.T) {
		readyzDeve(t, newAPICom(t, nil, func(d *httpapi.Deps) {
			d.Ready = func(context.Context) error { return errors.New("conexão recusada") }
		}), http.StatusServiceUnavailable)
	})
	t.Run("wiring esquecido", func(t *testing.T) {
		readyzDeve(t, newAPICom(t, nil, func(d *httpapi.Deps) { d.Ready = nil }), http.StatusServiceUnavailable)
	})
}

func readyzDeve(t *testing.T, a *api, status int) {
	t.Helper()
	resp, err := http.Get(a.srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("/readyz deu %d, queria %d", resp.StatusCode, status)
	}
	if status != http.StatusOK {
		if env := decodeEnvelope(t, resp); env.Error.Code != "unavailable" {
			t.Errorf("código = %q, queria unavailable", env.Error.Code)
		}
	}
}
```

- [ ] **Step 5: Rodar e ver falhar**

Run: `go test ./internal/httpapi/`
Expected: FAIL de compilação: `unknown field Incidents in struct literal of type httpapi.Deps` (e `Ready`).

- [ ] **Step 6: Implementar a API**

Em `internal/apierr/errors.go`, acrescente à lista de constantes:

```go
	CodeUnavailable        ErrorCode = "unavailable"
```

e à `errorTable`:

```go
	CodeUnavailable:        {http.StatusServiceUnavailable, "Serviço indisponível."},
```

Crie `internal/httpapi/incidenthandlers.go`:

```go
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/incident/history"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

type IncidentHandlers struct {
	svc *history.Service
}

func NewIncidentHandlers(svc *history.Service) *IncidentHandlers {
	return &IncidentHandlers{svc: svc}
}

// incidentResponse é construído campo a campo, como targetResponse.
type incidentResponse struct {
	ID             string     `json:"id"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	Cause          string     `json:"cause"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

type incidentListResponse struct {
	Items      []incidentResponse `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func toIncidentResponse(i history.Incident) incidentResponse {
	return incidentResponse{
		ID: i.ID.String(), TargetID: i.TargetID.String(), TargetName: i.TargetName,
		StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, Cause: i.Cause, AcknowledgedAt: i.AckAt,
	}
}

// List é GET /incidents?status=open|resolved&cursor=&limit= (spec §9).
func (h *IncidentHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := history.Filter{Limit: history.DefaultPageSize}

	switch q.Get("status") {
	case "":
	case "open":
		f.Open = new(bool)
		*f.Open = true
	case "resolved":
		f.Open = new(bool)
	default:
		campoInvalido(w, r, "status", targets.ReasonInvalid)
		return
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", targets.ReasonInvalid)
			return
		}
		if n < 1 || n > int64(history.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
		f.Limit = int32(n)
	}
	if s := q.Get("cursor"); s != "" {
		c, err := uuid.Parse(s)
		if err != nil {
			campoInvalido(w, r, "cursor", targets.ReasonInvalid)
			return
		}
		f.Before = &c
	}

	items, err := h.svc.List(r.Context(), userID, f)
	if h.falhou(w, r, "listar incidentes", err) {
		return
	}
	resp := incidentListResponse{Items: make([]incidentResponse, 0, len(items))}
	for _, i := range items {
		resp.Items = append(resp.Items, toIncidentResponse(i))
	}
	if len(items) == int(f.Limit) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

// Ack é POST /incidents/{id}/ack. Idempotente: o segundo ack devolve o
// instante do primeiro.
func (h *IncidentHandlers) Ack(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	i, err := h.svc.Ack(r.Context(), userID, id)
	if h.falhou(w, r, "reconhecer incidente", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toIncidentResponse(i))
}

func (h *IncidentHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, history.ErrNotFound) {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return true
	}
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
	return true
}
```

Em `internal/httpapi/routes.go`:

1. Acrescente aos imports `"log/slog"`, `"time"`, `"github.com/BipoXDXD/vigilume/internal/apierr"` e `"github.com/BipoXDXD/vigilume/internal/incident/history"`.
2. Troque a struct `Deps` por:

```go
type Deps struct {
	Store     *auth.PGStore
	Sessions  *auth.Manager
	TOTP      *auth.TOTP
	Targets   *targets.Service
	Incidents *history.Service
	Clock     clock.Clock
	// Ready é o teste de prontidão de /readyz: o ping no banco em produção.
	Ready func(context.Context) error
}
```

3. Depois de `th := NewTargetHandlers(d.Targets, d.Clock)`, acrescente `ih := NewIncidentHandlers(d.Incidents)`.
4. Troque o bloco que vai da rota `GET /api/v1/targets/{id}/checks` até o fim do `GET /healthz` por:

```go
	mux.Handle("GET /api/v1/targets/{id}/checks", protegida(th.Checks))

	mux.Handle("GET /api/v1/incidents", protegida(ih.List))
	mux.Handle("POST /api/v1/incidents/{id}/ack", protegida(ih.Ack))

	// Spec §13: /healthz é raso (o processo responde); /readyz verifica o
	// banco.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", readyz(d.Ready))
```

5. Acrescente ao fim do arquivo:

```go
// readyzTimeout limita o ping: um banco que não responde tem de virar 503
// rápido, e não segurar o health check do orquestrador.
const readyzTimeout = 2 * time.Second

// readyz responde 200 só com o banco alcançável. Sem ping no Deps também é
// 503: um wiring esquecido não pode parecer pronto.
func readyz(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
		defer cancel()
		if ping == nil {
			apierr.WriteError(w, r, apierr.CodeUnavailable)
			return
		}
		if err := ping(ctx); err != nil {
			slog.Warn("readyz: banco indisponível", "erro", err)
			apierr.WriteError(w, r, apierr.CodeUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
```

- [ ] **Step 7: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./... && go test -race -count=1 ./internal/httpapi/ ./internal/incident/...`
Expected: PASS. O `go vet ./...` também compila `cmd/vigilume`: o `Deps` do `main.go` ainda não tem `Incidents` nem `Ready`, e isso compila; o `/readyz` de produção fica 503 até a Task 11.

- [ ] **Step 8: Sabotagem**

Em `readyz`, apague o bloco `if ping == nil { ... }`. Rode `go test -count=1 -run TestReadyz ./internal/httpapi/`. Expected: FAIL no subteste "wiring esquecido", com 500 no lugar de 503: a chamada à função nil vira panic, e o `Recover` o transforma em 500. Restaure e registre.

Não há sabotagem útil para a tabela de rotas autenticadas. Sem `protegida(...)`, o handler ainda recusa, porque `usuario()` responde 401 quando não há usuário no contexto. São duas guardas para a mesma propriedade, e o teste prova a propriedade, não o wiring.

- [ ] **Step 9: Commit**

```bash
git add internal/incident/history/ internal/httpapi/ internal/apierr/
git commit -m "feat(api): histórico e reconhecimento de incidentes, /readyz com banco"
```

---
### Task 9: API de canais, páginas de verificação e limite de envio

**Files:**
- Modify: `internal/httpapi/middleware/ratelimit.go` (inteiro: `RateLimitUserEvery`, `porMinuto`, varredura que respeita o tempo de encher)
- Modify: `internal/httpapi/middleware/ratelimit_internal_test.go` (inteiro)
- Create: `internal/httpapi/channelhandlers.go`, `internal/httpapi/verifyhandlers.go`
- Modify: `internal/apierr/errors.go` (três códigos)
- Modify: `internal/httpapi/routes.go` (constantes, `Deps.Channels`, rotas de canais, cadeia pública de `/verify/`)
- Modify: `internal/httpapi/auth_integration_test.go` (fakes de envio no `api`, `Channels` no `Deps`)
- Test: `internal/httpapi/channels_integration_test.go`

**Interfaces:**
- Consumes: `ChannelService` e seus erros (Task 7); `newAPICom`, `envelopeComCampos` (Task 8).
- Produces:
  - `middleware.RateLimitUserEvery(ctx, every time.Duration, burst int)`.
  - `GET /api/v1/channels` → `{"items":[{id,kind,hint,status,enabled,created_at}]}`; `POST /api/v1/channels` com `{"kind":"discord","url":...}` ou `{"kind":"email","email":...}` → 201; `DELETE /api/v1/channels/{id}` → 204; `POST /api/v1/channels/{id}/verify/resend` → 204.
  - `GET /verify/{token}` (HTML, 200 ou 404) e `POST /verify/{token}` (HTML, 200 ou 404).
  - Códigos novos: `channel_limit_reached` (409), `channel_not_pending` (409), `delivery_failed` (502).
  - `httpapi.Deps.Channels *notify.ChannelService`.

**Contexto para quem implementa.** Três pontos que não são óbvios:

1. **A varredura do limitador.** Apagar um balde é devolvê-lo cheio. A varredura antiga apagava qualquer balde parado há 10 minutos; com um token a cada 6 minutos e burst 5, o balde leva 30 minutos para encher. O limite de 10 por hora viraria 5 envios a cada 10 minutos (D13). Agora cada limitador guarda `ocioso = max(10 min, tempo para encher)`. O parâmetro de teto de `rateLimit` passou a se chamar `teto`: com o nome `max`, ele esconderia a função embutida `max`.
2. **A cadeia de `/verify/` é separada** (D2). O `POST` vem de um `<form>` HTML, que não manda `X-Requested-With`; por isso fica fora do `RequireCustomHeader`. Sem cookie e sem sessão, um CSRF não teria o que roubar: a credencial é o token no caminho. As páginas trocam a CSP por uma com `form-action 'self'`. `SecurityHeaders` continua na cadeia, e daí vêm `Referrer-Policy: no-referrer` (o token não vaza no Referer) e `Cache-Control: no-store`.
3. **As páginas são texto fixo.** O formulário não tem `action`, e por isso envia para a própria URL; o token nunca é escrito no HTML.

- [ ] **Step 1: Escrever o teste do limitador (falha)**

Troque `internal/httpapi/middleware/ratelimit_internal_test.go` inteiro por:

```go
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// TestRateLimitCapsTheNumberOfBuckets trava o item 30, que hoje é inerte
// APENAS por causa do item 21: com RemoteAddr cru atrás do proxy o mapa tem
// uma entrada só, e o atacante não consegue variar a chave. No instante em
// que o ClientIP passa a ler o X-Forwarded-For de um proxy confiável, ele
// recupera esse controle — e um mapa sem teto vira exaustão de memória.
// Por isso os dois saem juntos.
func TestRateLimitCapsTheNumberOfBuckets(t *testing.T) {
	const teto = 8
	h := rateLimit(t.Context(), porMinuto(60), 5, porIP(nil), teto)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	for i := range 500 {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i%256)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	if n := quantosBuckets(h); n > teto {
		t.Errorf("%d buckets no mapa com teto de %d: o mapa cresce sem limite", n, teto)
	}
}

// TestIPv6ClientsShareABucketPerSlash64 corta o vetor na raiz: um /64 é o
// bloco que um único assinante recebe, então dar um balde por endereço
// entrega ao atacante bilhões de baldes de graça.
func TestIPv6ClientsShareABucketPerSlash64(t *testing.T) {
	h := rateLimit(t.Context(), porMinuto(60), 1, porIP(nil), 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	var negados int
	for i := range 20 {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:1:1::%x]:1234", i+1)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			negados++
		}
	}
	if negados == 0 {
		t.Error("20 endereços do mesmo /64 passaram todos: cada endereço ganhou o próprio balde")
	}
	if n := quantosBuckets(h); n != 1 {
		t.Errorf("%d buckets para um único /64, queria 1", n)
	}
}

// O limitador por usuário precisa falhar FECHADO. Montado por engano sem
// Authn na frente, ele não encontra usuário no contexto; cair num balde
// anônimo compartilhado seria silencioso. A requisição tem de ser recusada.
func TestRateLimitUserRefusesRequestsWithoutUser(t *testing.T) {
	h := rateLimit(t.Context(), porMinuto(60), 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("sem usuário no contexto deu %d, queria 401", rec.Code)
	}
}

// Apagar um balde é devolvê-lo cheio. Um limite de um token a cada 6
// minutos com burst 5 leva 30 minutos para encher; se a varredura o apagasse
// aos 10 minutos parado, o usuário ganharia 5 envios novos a cada 10
// minutos, o triplo do limite.
func TestSweepKeepsABucketUntilItCouldHaveRefilled(t *testing.T) {
	h := rateLimit(t.Context(), rate.Every(6*time.Minute), 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	lh := h.(*limiterHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/channels", nil)
	r = r.WithContext(context.WithValue(r.Context(), userKey{}, uuid.New()))
	for range 5 {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	lh.st.mu.Lock()
	for _, b := range lh.st.buckets {
		b.seen = time.Now().Add(-11 * time.Minute)
	}
	lh.st.varrer()
	n := len(lh.st.buckets)
	lh.st.mu.Unlock()
	if n != 1 {
		t.Fatal("o balde vazio foi apagado aos 11 minutos, antes de poder encher: o limite por hora vazaria")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("depois da varredura, a sexta requisição deu %d, queria 429", rec.Code)
	}
}
```

Run: `go test ./internal/httpapi/middleware/`
Expected: FAIL de compilação: `undefined: porMinuto`, e `rateLimit` recebe `int` onde agora vai `rate.Limit`.

- [ ] **Step 2: Implementar o limitador**

Troque `internal/httpapi/middleware/ratelimit.go` inteiro por:

```go
package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/vigilume/internal/apierr"
)

// maxBuckets limita quantas origens distintas o limitador acompanha ao mesmo
// tempo. Sem teto, o mapa é um vetor de exaustão de memória: cada origem
// inédita cria uma entrada que vive no mínimo 10 minutos, e um /64 de IPv6
// oferece endereços de graça. Ver o comentário de chaveDe sobre por que o
// agrupamento por /64 é a metade barata dessa defesa.
const maxBuckets = 100_000

// chaveFn devolve a chave do balde. O segundo retorno é false quando a
// requisição não traz a identidade que o limitador espera.
type chaveFn func(*http.Request) (string, bool)

// RateLimit aplica token bucket por cliente (spec §5.7).
//
// A varredura recebe ctx para poder parar. time.Tick nunca é coletado pelo
// GC — não há como cancelá-lo — então a goroutine de limpeza precisaria de
// um jeito explícito de terminar; amarrar isso ao contexto do servidor faz
// a goroutine morrer no shutdown gracioso junto com o resto, em vez de
// vazar para sempre (R32).
func RateLimit(ctx context.Context, perMinute, burst int, trusted []netip.Prefix) func(http.Handler) http.Handler {
	return rateLimit(ctx, porMinuto(perMinute), burst, porIP(trusted), maxBuckets)
}

// RateLimitUser limita por usuário autenticado, a segunda camada da spec
// §5.7. Tem de ficar DEPOIS de Authn na cadeia: é Authn que põe o usuário no
// contexto. O limite por IP continua valendo por fora; este existe porque
// um usuário com token válido, atrás de muitos IPs, escaparia do outro.
func RateLimitUser(ctx context.Context, perMinute, burst int) func(http.Handler) http.Handler {
	return rateLimit(ctx, porMinuto(perMinute), burst, porUsuario, maxBuckets)
}

// RateLimitUserEvery limita por usuário com um token a cada every. É a
// camada das rotas que mandam mensagem para fora (spec §5.8): criar canal e
// reenviar verificação custam um e-mail ou um POST de webhook, e o limite
// por minuto das rotas comuns faria do serviço um relay.
func RateLimitUserEvery(ctx context.Context, every time.Duration, burst int) func(http.Handler) http.Handler {
	return rateLimit(ctx, rate.Every(every), burst, porUsuario, maxBuckets)
}

func porMinuto(n int) rate.Limit { return rate.Limit(float64(n) / 60.0) }

func porIP(trusted []netip.Prefix) chaveFn {
	return func(r *http.Request) (string, bool) {
		return chaveDe(ClientIP(r, trusted)), true
	}
}

// porUsuario falha FECHADO: sem usuário no contexto, a requisição é
// recusada, em vez de cair num balde anônimo que todos dividiriam. O
// prefixo "u:" impede que uma chave de usuário colida com uma de IP.
func porUsuario(r *http.Request) (string, bool) {
	id, ok := UserID(r.Context())
	if !ok {
		return "", false
	}
	return "u:" + id.String(), true
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

type limiterState struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   rate.Limit
	burst   int
	chave   chaveFn
	max     int
	// ocioso é quanto um balde parado sobrevive à varredura.
	ocioso time.Duration
}

type limiterHandler struct {
	st   *limiterState
	next http.Handler
}

// ociosoMinimo é o tempo mínimo que um balde parado sobrevive à varredura.
const ociosoMinimo = 10 * time.Minute

// rateLimit recebe o teto como parâmetro para que o teste possa exercitá-lo
// sem criar cem mil buckets.
func rateLimit(ctx context.Context, limite rate.Limit, burst int, chave chaveFn, teto int) func(http.Handler) http.Handler {
	st := &limiterState{
		buckets: map[string]*bucket{},
		limit:   limite,
		burst:   burst,
		chave:   chave,
		max:     teto,
		// Apagar um balde é devolvê-lo cheio. Isso só é inofensivo depois
		// de ele ter tido tempo de encher sozinho: com um token a cada 6
		// minutos e burst 5, são 30 minutos. Varrer aos 10 daria 5 envios
		// novos a cada 10 minutos, o triplo do limite.
		ocioso: max(ociosoMinimo, tempoParaEncher(limite, burst)),
	}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				st.mu.Lock()
				st.varrer()
				st.mu.Unlock()
			}
		}
	}()

	return func(next http.Handler) http.Handler {
		return &limiterHandler{st: st, next: next}
	}
}

func (h *limiterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	chave, ok := h.st.chave(r)
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	h.st.mu.Lock()
	b, ok := h.st.buckets[chave]
	if !ok {
		h.st.abrirEspaco()
		b = &bucket{lim: rate.NewLimiter(h.st.limit, h.st.burst)}
		h.st.buckets[chave] = b
	}
	b.seen = time.Now()
	allow := b.lim.Allow()
	h.st.mu.Unlock()

	if !allow {
		apierr.WriteError(w, r, apierr.CodeRateLimited)
		return
	}
	h.next.ServeHTTP(w, r)
}

// varrer remove o que está parado há tempo demais. Chamar com mu travado.
func (s *limiterState) varrer() {
	for k, b := range s.buckets {
		if time.Since(b.seen) > s.ocioso {
			delete(s.buckets, k)
		}
	}
}

// tempoParaEncher é quanto um balde vazio leva para voltar ao burst.
func tempoParaEncher(limite rate.Limit, burst int) time.Duration {
	if limite <= 0 || limite == rate.Inf {
		return 0
	}
	return time.Duration(float64(burst) / float64(limite) * float64(time.Second))
}

// abrirEspaco garante que cabe mais uma entrada. Chamar com mu travado.
//
// Primeiro tenta a varredura normal; se o mapa continuar cheio — o caso do
// ataque, em que todas as entradas são recentes — despeja a menos vista
// recentemente. O custo da varredura é O(max), e não O(origens observadas),
// que é justamente a diferença entre ter e não ter teto.
func (s *limiterState) abrirEspaco() {
	if len(s.buckets) < s.max {
		return
	}
	s.varrer()
	for len(s.buckets) >= s.max {
		var maisVelha string
		var quando time.Time
		for k, b := range s.buckets {
			if quando.IsZero() || b.seen.Before(quando) {
				maisVelha, quando = k, b.seen
			}
		}
		delete(s.buckets, maisVelha)
	}
}

// chaveDe agrupa IPv6 por /64, que é o bloco entregue a um único assinante.
// Um balde por ENDEREÇO daria ao atacante bilhões de baldes dentro da própria
// alocação dele; por /64, variar o endereço não compra nada.
func chaveDe(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// quantosBuckets existe para o teste do teto: é a única forma de observar o
// tamanho do mapa sem exportá-lo.
func quantosBuckets(h http.Handler) int {
	lh, ok := h.(*limiterHandler)
	if !ok {
		return -1
	}
	lh.st.mu.Lock()
	defer lh.st.mu.Unlock()
	return len(lh.st.buckets)
}
```

Run: `go test -race -count=1 ./internal/httpapi/middleware/`
Expected: PASS.

- [ ] **Step 3: Sabotagem do limitador**

Em `rateLimit`, troque `ocioso: max(ociosoMinimo, tempoParaEncher(limite, burst)),` por `ocioso: ociosoMinimo,`. Rode `-run TestSweep`. Expected: FAIL com "o balde vazio foi apagado aos 11 minutos". Restaure e registre.

- [ ] **Step 4: Escrever os testes da API (falham)**

Em `internal/httpapi/auth_integration_test.go`:

1. Acrescente `"github.com/BipoXDXD/vigilume/internal/notify"` aos imports.
2. Na struct `api`, depois de `recuperacao []string`, acrescente:

```go
	// discord e email substituem os envios de verificação dos canais.
	discord *discordFalso
	email   *carteiroFalso
```

3. Em `newAPICom`, troque a montagem de `deps` e o `return` por:

```go
	discord, email := &discordFalso{}, &carteiroFalso{}
	deps := httpapi.Deps{
		Store: store, Sessions: mgr, TOTP: segundoFator,
		Targets:   targets.NewService(pool, box, clk),
		Incidents: history.NewService(pool, clk),
		Channels:  notify.NewChannelService(pool, box, clk, discord, email, cfg.PublicBaseURL),
		Clock:     clk, Ready: pool.Ping,
	}
	if ajustarDeps != nil {
		ajustarDeps(&deps)
	}
	srv := httptest.NewServer(httpapi.Routes(ctx, cfg, deps))
	t.Cleanup(srv.Close)
	return &api{srv: srv, pool: pool, clk: clk, userID: userID, discord: discord, email: email}
```

Crie `internal/httpapi/channels_integration_test.go`:

```go
package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

type discordFalso struct {
	mu  sync.Mutex
	err error
}

func (f *discordFalso) SendTest(context.Context, crypto.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

type carteiroFalso struct {
	mu    sync.Mutex
	links []string
}

func (f *carteiroFalso) SendVerification(_ context.Context, _ crypto.Secret, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, link)
	return nil
}

// token tira o token do último link enviado.
func (f *carteiroFalso) token(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.links) == 0 {
		t.Fatal("nenhum e-mail de verificação enviado")
	}
	_, token, ok := strings.Cut(f.links[len(f.links)-1], "/verify/")
	if !ok {
		t.Fatalf("link fora do formato: %s", f.links[len(f.links)-1])
	}
	return token
}

const webhookDeTeste = "https://discord.com/api/webhooks/123456789/token-do-webhook"

type canalJSON struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Hint   string `json:"hint"`
	Status string `json:"status"`
}

func canaisDe(t *testing.T, a *api, token string) []canalJSON {
	t.Helper()
	var l struct {
		Items []canalJSON `json:"items"`
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/channels", token, nil)
	lerCorpo(t, resp, &l)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /channels deu %d", resp.StatusCode)
	}
	return l.Items
}

func TestDiscordChannelFlow(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	var criado canalJSON
	cru := lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated || criado.Status != "verified" || criado.Hint != "webhook …6789" {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	if strings.Contains(string(cru), "token-do-webhook") {
		t.Fatal("a resposta contém o token do webhook")
	}
	if cs := canaisDe(t, a, tok); len(cs) != 1 || cs[0].ID != criado.ID {
		t.Fatalf("lista = %+v", cs)
	}

	resp = chamar(t, a, http.MethodDelete, "/api/v1/channels/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE deu %d", resp.StatusCode)
	}
	if cs := canaisDe(t, a, tok); len(cs) != 0 {
		t.Errorf("depois do DELETE, %d canais", len(cs))
	}
}

// O caminho do navegador: o GET mostra o botão e NÃO verifica; o POST do
// formulário, sem X-Requested-With, verifica.
func TestEmailVerificationThroughTheBrowser(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	var criado canalJSON
	lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated || criado.Status != "pending" {
		t.Fatalf("POST deu %d: %+v", resp.StatusCode, criado)
	}
	link := a.srv.URL + "/verify/" + a.email.token(t)

	get, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if get.StatusCode != http.StatusOK || !strings.Contains(string(html), `<form method="post">`) {
		t.Fatalf("GET deu %d: %s", get.StatusCode, html)
	}
	for cabecalho, quer := range map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Content-Security-Policy": "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	} {
		if got := get.Header.Get(cabecalho); got != quer {
			t.Errorf("%s = %q, queria %q", cabecalho, got, quer)
		}
	}
	if cs := canaisDe(t, a, tok); cs[0].Status != "pending" {
		t.Fatal("o GET verificou o canal: um leitor de links confirmaria endereços de terceiros")
	}

	post, err := http.Post(link, "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST do formulário deu %d", post.StatusCode)
	}
	if cs := canaisDe(t, a, tok); cs[0].Status != "verified" {
		t.Errorf("depois do POST, status = %s", cs[0].Status)
	}
}

func TestUnknownVerifyTokenIs404(t *testing.T) {
	a := newAPI(t)
	for _, token := range []string{"nao-existe", strings.Repeat("A", 43)} {
		get, err := http.Get(a.srv.URL + "/verify/" + token)
		if err != nil {
			t.Fatal(err)
		}
		get.Body.Close()
		post, err := http.Post(a.srv.URL+"/verify/"+token, "application/x-www-form-urlencoded", nil)
		if err != nil {
			t.Fatal(err)
		}
		post.Body.Close()
		if get.StatusCode != http.StatusNotFound || post.StatusCode != http.StatusNotFound {
			t.Errorf("token %q: GET %d, POST %d; queria 404 nos dois", token, get.StatusCode, post.StatusCode)
		}
	}
}

func TestChannelErrorsMapToCodes(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	var env envelopeComCampos
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "slack"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "kind" {
		t.Errorf("kind inválido deu %d/%+v", resp.StatusCode, env.Error)
	}

	a.discord.mu.Lock()
	a.discord.err = &notify.SendError{Reason: notify.ReasonRejected, Permanent: true}
	a.discord.mu.Unlock()
	resp = chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusBadGateway || e.Error.Code != "delivery_failed" {
		t.Errorf("envio falho deu %d/%s, queria 502/delivery_failed", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()

	a.discord.mu.Lock()
	a.discord.err = nil
	a.discord.mu.Unlock()
	var criado canalJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste}), &criado)
	resp = chamar(t, a, http.MethodPost, "/api/v1/channels/"+criado.ID+"/verify/resend", tok, nil)
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusConflict || e.Error.Code != "channel_not_pending" {
		t.Errorf("reenviar Discord deu %d/%s, queria 409/channel_not_pending", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()
}

func TestChannelLimitIs409(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	agora := a.clk.Now()
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		for range notify.MaxChannels {
			if err := q.InsertChannel(ctx, db.InsertChannelParams{
				ID: uuid.Must(uuid.NewV7()), UserID: a.userID, Kind: "discord", ConfigEnc: []byte{1},
				VerifiedAt: &agora, CreatedAt: agora,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID), map[string]string{"kind": "discord", "url": webhookDeTeste})
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusConflict || e.Error.Code != "channel_limit_reached" {
		t.Errorf("canal além do limite deu %d/%s", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()
}

// O serviço sem Resend recusa o e-mail na validação, antes de tocar no
// banco: por isso o pool nil. Se a ordem mudar, o teste cai com panic, alto.
func TestEmailChannelIsUnavailableWithoutResend(t *testing.T) {
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Channels = notify.NewChannelService(nil, nil, d.Clock, &discordFalso{}, nil, "")
	})
	var env envelopeComCampos
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID), map[string]string{"kind": "email", "email": "p@x.com"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
		env.Error.Fields[0].Field != "kind" || env.Error.Fields[0].Reason != "unavailable" {
		t.Errorf("e-mail sem Resend deu %d/%+v, queria 400 kind/unavailable", resp.StatusCode, env.Error)
	}
}

func TestAnotherUsersChannelIs404(t *testing.T) {
	a := newAPI(t)
	var criado canalJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID),
		map[string]string{"kind": "email", "email": "p@x.com"}), &criado)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodDelete, "/api/v1/channels/" + criado.ID},
		{http.MethodPost, "/api/v1/channels/" + criado.ID + "/verify/resend"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, intruso, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s pelo intruso deu %d, queria 404", r.metodo, r.caminho, resp.StatusCode)
		}
	}
	if cs := canaisDe(t, a, intruso); len(cs) != 0 {
		t.Errorf("o intruso listou %d canais", len(cs))
	}
}

// Criar canal e reenviar verificação mandam mensagem para fora: o balde é
// de 5 envios, e a sexta tentativa leva 429 mesmo com o limite por minuto
// (burst 20) folgado. Listar não gasta desse balde.
func TestChannelSendsHaveTheirOwnRateLimit(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for i := 1; i <= 5; i++ {
		resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "slack"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("tentativa %d deu %d, queria 400", i, resp.StatusCode)
		}
	}
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels/"+uuid.NewString()+"/verify/resend", tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("sexto envio (reenvio) deu %d, queria 429: o balde é compartilhado pelas rotas de envio", resp.StatusCode)
	}
	canaisDe(t, a, tok) // falha se o GET também estiver no balde de envio
}

func TestChannelRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	id := uuid.NewString()
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/channels"},
		{http.MethodPost, "/api/v1/channels"},
		{http.MethodDelete, "/api/v1/channels/" + id},
		{http.MethodPost, "/api/v1/channels/" + id + "/verify/resend"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// O relógio da API é falso; o link vence pelo relógio dela.
func TestExpiredVerifyLinkIs404(t *testing.T) {
	a := newAPI(t)
	chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID),
		map[string]string{"kind": "email", "email": "p@x.com"}).Body.Close()
	link := a.srv.URL + "/verify/" + a.email.token(t)
	a.clk.Advance(notify.VerifyTTL + time.Second)

	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("link vencido deu %d, queria 404", resp.StatusCode)
	}
}
```

- [ ] **Step 5: Rodar e ver falhar**

Run: `go test ./internal/httpapi/`
Expected: FAIL de compilação: `unknown field Channels in struct literal of type httpapi.Deps`.

- [ ] **Step 6: Implementar**

Em `internal/apierr/errors.go`, acrescente às constantes:

```go
	CodeChannelLimit       ErrorCode = "channel_limit_reached"
	CodeChannelNotPending  ErrorCode = "channel_not_pending"
	CodeDeliveryFailed     ErrorCode = "delivery_failed"
```

e à `errorTable`:

```go
	CodeChannelLimit:       {http.StatusConflict, "Limite de canais atingido."},
	CodeChannelNotPending:  {http.StatusConflict, "O canal não está pendente de verificação."},
	// 502: a falha foi do destino (Discord ou provedor de e-mail), não do
	// pedido nem deste servidor.
	CodeDeliveryFailed: {http.StatusBadGateway, "Não foi possível entregar a mensagem de verificação."},
```

Crie `internal/httpapi/channelhandlers.go`:

```go
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

type ChannelHandlers struct {
	svc *notify.ChannelService
}

func NewChannelHandlers(svc *notify.ChannelService) *ChannelHandlers {
	return &ChannelHandlers{svc: svc}
}

type channelRequest struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Email string `json:"email"`
}

// channelResponse não tem campo para o destino: só o hint. Nenhuma mudança
// no domínio faz a URL do webhook ou o endereço aparecerem aqui.
type channelResponse struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Hint      string    `json:"hint"`
	Status    string    `json:"status"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func toChannelResponse(c notify.Channel) channelResponse {
	return channelResponse{
		ID: c.ID.String(), Kind: string(c.Kind), Hint: c.Hint, Status: string(c.Status),
		Enabled: c.Enabled, CreatedAt: c.CreatedAt,
	}
}

func (h *ChannelHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	cs, err := h.svc.List(r.Context(), userID)
	if h.falhou(w, r, "listar canais", err) {
		return
	}
	items := make([]channelResponse, 0, len(cs))
	for _, c := range cs {
		items = append(items, toChannelResponse(c))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ChannelHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req channelRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	c, err := h.svc.Create(r.Context(), userID, notify.ChannelInput{
		Kind: notify.Kind(req.Kind), URL: req.URL, Email: req.Email,
	})
	if h.falhou(w, r, "criar canal", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toChannelResponse(c))
}

func (h *ChannelHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "apagar canal", h.svc.Delete(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChannelHandlers) Resend(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "reenviar verificação", h.svc.ResendVerification(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChannelHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *notify.ValidationError
	switch {
	case errors.As(err, &verr):
		campoInvalido(w, r, verr.Field, verr.Reason)
	case errors.Is(err, notify.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, notify.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeChannelLimit)
	case errors.Is(err, notify.ErrNotPending):
		apierr.WriteError(w, r, apierr.CodeChannelNotPending)
	case errors.Is(err, notify.ErrDeliveryFailed):
		apierr.WriteError(w, r, apierr.CodeDeliveryFailed)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}
```

Crie `internal/httpapi/verifyhandlers.go`:

```go
package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// VerifyHandlers servem o link do e-mail de verificação (spec §5.8, §9).
//
// O GET só mostra um botão; quem verifica é o POST do formulário (D2 deste
// plano). Leitores de link de provedores de e-mail abrem os links das
// mensagens para checar malware. Se o GET verificasse, o endereço de um
// terceiro seria confirmado sem ele: o relay de spam que a §5.8 fecha.
type VerifyHandlers struct {
	svc *notify.ChannelService
}

func NewVerifyHandlers(svc *notify.ChannelService) *VerifyHandlers {
	return &VerifyHandlers{svc: svc}
}

// cspVerificacao troca a CSP da API só nestas páginas: form-action 'self'
// deixa o botão enviar o formulário para a própria URL; o resto continua
// fechado, sem script e sem estilo.
const cspVerificacao = "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// As páginas são texto fixo: nada da requisição nem do banco entra nelas.
// O formulário não tem action, e por isso envia para a URL atual; o token
// não é escrito no HTML.
const (
	paginaConfirmar = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: confirmar e-mail</title>` +
		`<h1>Confirmar este e-mail</h1>` +
		`<p>Este endereço vai receber os alertas de incidente do Vigilume.</p>` +
		`<form method="post"><button type="submit">Confirmar</button></form></html>`
	paginaConfirmado = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: e-mail confirmado</title>` +
		`<h1>E-mail confirmado</h1><p>Os alertas de incidente vão chegar neste endereço.</p></html>`
	paginaInvalida = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: link inválido</title>` +
		`<h1>Link inválido ou vencido</h1><p>Peça um novo link no painel do Vigilume.</p></html>`
	paginaErro = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<title>Vigilume: erro</title><h1>Algo deu errado</h1><p>Tente de novo em instantes.</p></html>`
)

func (h *VerifyHandlers) Page(w http.ResponseWriter, r *http.Request) {
	estado, err := h.svc.CheckToken(r.Context(), r.PathValue("token"))
	if err != nil {
		slog.Error("verificar token", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusInternalServerError, paginaErro)
		return
	}
	switch estado {
	case notify.VerifyPending:
		pagina(w, http.StatusOK, paginaConfirmar)
	case notify.VerifyDone:
		pagina(w, http.StatusOK, paginaConfirmado)
	default:
		pagina(w, http.StatusNotFound, paginaInvalida)
	}
}

func (h *VerifyHandlers) Confirm(w http.ResponseWriter, r *http.Request) {
	err := h.svc.ConfirmToken(r.Context(), r.PathValue("token"))
	switch {
	case err == nil:
		pagina(w, http.StatusOK, paginaConfirmado)
	case errors.Is(err, notify.ErrInvalidToken):
		pagina(w, http.StatusNotFound, paginaInvalida)
	default:
		slog.Error("confirmar token", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusInternalServerError, paginaErro)
	}
}

func pagina(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", cspVerificacao)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}
```

Em `internal/httpapi/routes.go`:

1. Acrescente `"github.com/BipoXDXD/vigilume/internal/notify"` aos imports.
2. Logo depois de `const maxBodyBytes = 1 << 20 // 1 MiB`, acrescente:

```go

// As rotas que mandam mensagem para fora (criar canal, reenviar
// verificação) ganham um balde próprio por usuário: um envio a cada 6
// minutos, 5 de uma vez. Dá 10 por hora depois do burst (spec §5.8).
const (
	envioACada = 6 * time.Minute
	envioBurst = 5
)

// verifyBodyBytes: o POST de /verify é um formulário sem campos.
const verifyBodyBytes = 1 << 10
```

3. Em `Deps`, depois de `Incidents *history.Service`, acrescente `Channels  *notify.ChannelService`.
4. Depois de `ih := NewIncidentHandlers(d.Incidents)`, acrescente `ch := NewChannelHandlers(d.Channels)`.
5. Depois da rota `POST /api/v1/incidents/{id}/ack`, acrescente:

```go

	porEnvio := middleware.RateLimitUserEvery(ctx, envioACada, envioBurst)
	comEnvio := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(porEnvio(fn))) }
	mux.Handle("GET /api/v1/channels", protegida(ch.List))
	mux.Handle("POST /api/v1/channels", comEnvio(ch.Create))
	mux.Handle("DELETE /api/v1/channels/{id}", protegida(ch.Delete))
	mux.Handle("POST /api/v1/channels/{id}/verify/resend", comEnvio(ch.Resend))
```

6. No fim de `Routes`, troque `return handler` por:

```go
	raiz := http.NewServeMux()
	raiz.Handle("/verify/", verificacao(ctx, cfg, d.Channels))
	raiz.Handle("/", handler)
	return raiz
```

7. Acrescente ao fim do arquivo:

```go
// verificacao é a cadeia das páginas de /verify/{token}, separada da API.
//
// Ela é aberta ao navegador, e não ao SPA: o POST vem de um <form> HTML,
// que não manda X-Requested-With. Por isso fica fora do RequireCustomHeader.
// Sem cookie e sem sessão, não há o que um CSRF roubaria: a credencial é o
// token no caminho, que o atacante não tem. Também fica fora do CORS, que é
// da API. O limite por IP é mais apertado que o da API.
func verificacao(ctx context.Context, cfg *config.Config, svc *notify.ChannelService) http.Handler {
	vh := NewVerifyHandlers(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /verify/{token}", vh.Page)
	mux.HandleFunc("POST /verify/{token}", vh.Confirm)

	var h http.Handler = mux
	h = middleware.BodyLimit(verifyBodyBytes)(h)
	h = middleware.RateLimit(ctx, 20, 10, cfg.TrustedProxies)(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.RequestID(h)
	return h
}
```

- [ ] **Step 7: Rodar e ver passar**

Run: `gofmt -w internal/ && go vet ./... && go test -race -count=1 ./internal/httpapi/...`
Expected: PASS. `TestTheMiddlewareChainIsActuallyWired` continua verde: a cadeia da API não mudou, só ficou atrás de um mux raiz.

- [ ] **Step 8: Sabotagem — duas guardas**

Em `Routes`, troque `raiz.Handle("/verify/", verificacao(ctx, cfg, d.Channels))` por `raiz.Handle("/verify/", middleware.RequireCustomHeader(verificacao(ctx, cfg, d.Channels)))`, e `comEnvio(ch.Resend)` por `protegida(ch.Resend)`. Rode `-run 'TestEmailVerificationThroughTheBrowser|TestChannelSendsHaveTheirOwnRateLimit'`.
Expected: FAIL nos dois. O `POST` do formulário leva 403, e o reenvio escapa do balde de envio. Restaure e registre.

- [ ] **Step 9: Commit**

```bash
git add internal/httpapi/ internal/apierr/
git commit -m "feat(api): canais de notificação, verificação de e-mail por formulário e limite de envio por hora"
```

---
### Task 10: Dívidas pequenas do Plano 2 — travas de teste

As travas que a revisão final do Plano 2 pediu e que cabem aqui (decisão do usuário): `POST /metrics` com 405, `METRICS_ADDR=OFF`, a tabela das 6 rotas de alvos e as bordas das consultas via HTTP. A advisory lock das partições entrou na Task 2, e o `/readyz` com banco na Task 8.

**Files:**
- Modify: `internal/config/config.go` (`off` em qualquer caixa)
- Modify: `internal/config/config_test.go` (dois casos)
- Modify: `internal/observability/metrics_test.go` (um teste)
- Modify: `internal/httpapi/targethandlers.go` (`limit` não numérico vira `invalid`)
- Modify: `internal/httpapi/targets_integration_test.go` (a tabela das 6 rotas e dois testes)

**Interfaces:**
- Consumes: `envelopeComCampos` (Task 8).
- Produces: nada novo; só travas.

- [ ] **Step 1: Escrever os testes (falham)**

Em `internal/config/config_test.go`, na tabela de `TestMetricsAddrMustBeLoopback`, acrescente depois de `{"off", "", true},`:

```go
		{"OFF", "", true},
		{"Off", "", true},
```

Acrescente ao fim de `internal/observability/metrics_test.go`:

```go
// O padrão com método ("GET /metrics") é o que recusa as outras. Trocado
// por "/metrics", o servidor passaria a aceitar POST sem ninguém ver.
func TestMetricsServerOnlyAnswersGET(t *testing.T) {
	srv := httptest.NewServer(observability.NewMetricsServer("", observability.NewRegistry()).Handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics deu %d, queria 405", resp.StatusCode)
	}
}
```

Em `internal/httpapi/targets_integration_test.go`, acrescente `"github.com/BipoXDXD/vigilume/internal/apierr"` aos imports e troque `TestTargetRoutesRequireAuthentication` inteiro por:

```go
// Todas as seis rotas, e não só a lista: uma rota nova montada com
// mux.Handle direto, sem protegida(), ficaria aberta sem teste nenhum ver.
func TestTargetRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	id := uuid.NewString()
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/targets"},
		{http.MethodPost, "/api/v1/targets"},
		{http.MethodGet, "/api/v1/targets/" + id},
		{http.MethodPatch, "/api/v1/targets/" + id},
		{http.MethodDelete, "/api/v1/targets/" + id},
		{http.MethodGet, "/api/v1/targets/" + id + "/checks"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// Dívida do Plano 2: as bordas das consultas via HTTP. Cada valor
// malformado aponta o campo e o motivo certos.
func TestTargetQueriesRejectMalformedValues(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)
	checks := "/api/v1/targets/" + criado.ID + "/checks?"
	agora := a.clk.Now().UTC().Format(time.RFC3339)

	for caminho, quer := range map[string]apierr.FieldError{
		"/api/v1/targets?limit=0":                 {Field: "limit", Reason: "out_of_range"},
		"/api/v1/targets?limit=abc":               {Field: "limit", Reason: "invalid"},
		"/api/v1/targets?cursor=nao-e-uuid":       {Field: "cursor", Reason: "invalid"},
		checks + "from=ontem":                     {Field: "from", Reason: "invalid"},
		checks + "to=2026-13-01T00:00:00Z":        {Field: "to", Reason: "invalid"},
		checks + "from=" + agora + "&to=" + agora: {Field: "from", Reason: "invalid"}, // janela vazia
	} {
		var env envelopeComCampos
		resp := chamar(t, a, http.MethodGet, caminho, tok, nil)
		lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0] != quer {
			t.Errorf("%s deu %d/%+v, queria 400 %+v", caminho, resp.StatusCode, env.Error.Fields, quer)
		}
	}
}

// A janela é [from, to): o check em from entra, o check em to não.
func TestChecksWindowIsHalfOpen(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)
	fim := a.clk.Now().UTC().Truncate(time.Minute)
	for _, em := range []time.Time{fim.Add(-2 * time.Minute), fim.Add(-time.Minute), fim} {
		p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: uuid.MustParse(criado.ID), CheckedAt: em, Status: "up", LatencyMS: 5}
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	var got struct {
		Items []struct {
			CheckedAt time.Time `json:"checked_at"`
		} `json:"items"`
	}
	caminho := fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
		fim.Add(-2*time.Minute).Format(time.RFC3339), fim.Format(time.RFC3339))
	lerCorpo(t, chamar(t, a, http.MethodGet, caminho, tok, nil), &got)
	if len(got.Items) != 2 || !got.Items[0].CheckedAt.Equal(fim.Add(-2*time.Minute)) || !got.Items[1].CheckedAt.Equal(fim.Add(-time.Minute)) {
		t.Errorf("janela [from, to) devolveu %+v", got.Items)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar (e ver o que já passa)**

Run: `go test -count=1 ./internal/config/ ./internal/observability/ ./internal/httpapi/`
Expected: FAIL em `TestMetricsAddrMustBeLoopback/OFF` e `/Off`, e em `TestTargetQueriesRejectMalformedValues` no caso `limit=abc`, que hoje dá `out_of_range`. **Passam já**, e são travas sem implementação nova: `TestMetricsServerOnlyAnswersGET`, a tabela das 6 rotas, `TestChecksWindowIsHalfOpen` e os outros casos de `TestTargetQueriesRejectMalformedValues`. As sabotagens do Step 5 provam que elas mordem.

- [ ] **Step 3: Implementar**

Em `internal/config/config.go`, troque `if raw == "off" {` por `if strings.EqualFold(raw, "off") {`, e o comentário acima de `metricsAddr := func(...)` por:

```go
	// /metrics entrega o mapa operacional (spec §5.7): quantos alvos, que
	// latências, quanta conexão. Só escuta em loopback; "off", em qualquer
	// caixa, desliga. Um host vazio (":9090") escutaria em todas as
	// interfaces, e por isso é erro, não padrão (D10).
```

Em `internal/httpapi/targethandlers.go`, em `List`, troque:

```go
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 || n > int64(targets.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
```

por:

```go
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", targets.ReasonInvalid)
			return
		}
		if n < 1 || n > int64(targets.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/ && go test -race -count=1 ./internal/config/ ./internal/observability/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem — as travas que já passavam**

1. Em `internal/observability/metrics.go`, troque `mux.Handle("GET /metrics", ...` por `mux.Handle("/metrics", ...`. Rode `-run TestMetricsServerOnlyAnswersGET ./internal/observability/`. Expected: FAIL com "POST /metrics deu 200".
2. Em `internal/targets/service.go`, em `Checks`, troque `if !from.Before(to) {` por `if from.After(to) {`. Rode `-run TestTargetQueriesRejectMalformedValues ./internal/httpapi/`. Expected: FAIL no caso da janela vazia.
3. Em `internal/storage/queries/targets.sql`, em `ListChecks`, troque `c.checked_at < sqlc.arg(to_at)` por `c.checked_at <= sqlc.arg(to_at)`; `sqlc generate`; rode `-run TestChecksWindowIsHalfOpen ./internal/httpapi/`. Expected: FAIL com três itens na janela.

Restaure (e `sqlc generate`) e registre as três.

- [ ] **Step 6: Commit**

```bash
git add internal/config/ internal/observability/ internal/httpapi/
git commit -m "test: travas pendentes do Plano 2 (POST /metrics, METRICS_ADDR=OFF, rotas e bordas de /targets)"
```

---
### Task 11: Config do Resend, wiring, teste ponta a ponta e documentação

**Files:**
- Modify: `internal/config/config.go` (`ResendAPIKey`, `EmailFrom`, `EmailEnabled`, validação, `LogValue`)
- Modify: `internal/config/config_test.go` (dois testes)
- Modify: `cmd/vigilume/main.go` (inteiro)
- Test: `cmd/vigilume/incident_e2e_test.go`
- Create: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`
- Modify: `docs/superpowers/plans/2026-09-22-roadmap.md` (linha do Plano 3)
- Modify: `.gitignore` (`/.superpowers/`)

**Interfaces:**
- Consumes: tudo das Tasks 1 a 9.
- Produces:
  - `config.Config.ResendAPIKey`, `config.Config.EmailFrom`, `(*Config).EmailEnabled() bool`.
  - Wiring: papel `prober` roda o `Dispatcher`; papel `api` tem `Incidents`, `Channels` e `Ready = pool.Ping`.

**Contexto para quem implementa.** `verificador(cfg)` devolve a **interface** nil quando o e-mail está desligado. Um `*Resend` nil dentro de uma interface não é nil, e o cadastro acharia que o e-mail está disponível.

O teste ponta a ponta usa relógio real, como o do Plano 2. Para não esperar três intervalos de 60 s, ele devolve o alvo à fila a cada check gravado. O canal Discord do teste tem uma URL interna, que só entra por SQL; o dispatcher de produção a recusa, falha de vez e audita.

- [ ] **Step 1: Escrever os testes de config (falham)**

Acrescente ao fim de `internal/config/config_test.go`:

```go
func TestEmailSettingsGoTogether(t *testing.T) {
	cases := []struct {
		nome        string
		chave, from string
		ligado, ok  bool
	}{
		{"nenhuma", "", "", false, true},
		{"as duas", "re_123", "Vigilume <alertas@vigilume.dev>", true, true},
		{"só a chave", "re_123", "", false, false},
		{"só o remetente", "", "alertas@vigilume.dev", false, false},
		{"remetente inválido", "re_123", "não é e-mail", false, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			env := validEnv()
			env["RESEND_API_KEY"], env["EMAIL_FROM"] = c.chave, c.from
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "EMAIL_FROM") {
					t.Fatalf("aceito (err = %v)", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.EmailEnabled() != c.ligado {
				t.Errorf("EmailEnabled = %v, queria %v", cfg.EmailEnabled(), c.ligado)
			}
		})
	}
}

func TestLogValueRedactsTheResendKey(t *testing.T) {
	env := validEnv()
	env["RESEND_API_KEY"], env["EMAIL_FROM"] = "re_chave_secreta", "a@b.dev"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "cfg", cfg)
	if strings.Contains(buf.String(), "re_chave_secreta") || !strings.Contains(buf.String(), `"email_enabled":true`) {
		t.Errorf("log = %s", buf.String())
	}
}
```

Run: `go test ./internal/config/`
Expected: FAIL de compilação: `cfg.EmailEnabled undefined`.

- [ ] **Step 2: Implementar a config**

Em `internal/config/config.go`:

1. Acrescente `"net/mail"` aos imports.
2. Na struct `Config`, depois de `ShutdownGrace  time.Duration`, acrescente:

```go
	// E-mail pelo Resend (spec §8). A chave é do operador; vazia, o canal
	// de e-mail fica indisponível.
	ResendAPIKey string
	EmailFrom    string
```

e, logo depois da struct:

```go

// EmailEnabled diz se o operador configurou o envio de e-mail.
func (c *Config) EmailEnabled() bool { return c.ResendAPIKey != "" }
```

3. No literal `cfg := &Config{...}`, depois de `ShutdownGrace:  15 * time.Second,`, acrescente:

```go
		ResendAPIKey:   strings.TrimSpace(getenv("RESEND_API_KEY")),
		EmailFrom:      strings.TrimSpace(getenv("EMAIL_FROM")),
```

4. Logo depois do fechamento desse literal, acrescente:

```go
	// As duas variáveis de e-mail andam juntas. Sem nenhuma, o e-mail fica
	// desligado, e cadastrar um canal de e-mail responde que ele está
	// indisponível. Com só uma, é quase sempre esquecimento, e o boot avisa.
	switch {
	case (cfg.ResendAPIKey == "") != (cfg.EmailFrom == ""):
		errs = append(errs, errors.New("RESEND_API_KEY e EMAIL_FROM andam juntas: defina as duas, ou nenhuma"))
	case cfg.EmailFrom != "":
		if _, err := mail.ParseAddress(cfg.EmailFrom); err != nil {
			errs = append(errs, errors.New("EMAIL_FROM não é um endereço válido (use \"Nome <a@b.com>\" ou a@b.com)"))
		}
	}
```

5. Em `LogValue`, depois de `slog.String("ip_hash_key", "[REDACTED]"),`, acrescente:

```go
		slog.Bool("email_enabled", c.EmailEnabled()),
		slog.String("email_from", c.EmailFrom),
		slog.String("resend_api_key", "[REDACTED]"),
```

Run: `go test -race -count=1 ./internal/config/`
Expected: PASS.

- [ ] **Step 3: Escrever o teste ponta a ponta (falha)**

Crie `cmd/vigilume/incident_e2e_test.go`:

```go
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// O caminho do Plano 3 no processo real, com o cliente e o validador de
// PRODUÇÃO: um alvo que falha abre incidente depois de três sondas, a
// abertura vai para a outbox, e o dispatcher tenta o canal Discord. O canal
// tem uma URL interna, que só entra por fora da API; o envio a recusa, falha
// de vez e audita. incidents_open aparece em /metrics.
//
// Duas guardas recusam essa URL: a revalidação da allowlist no envio e,
// atrás dela, o safedial. O teste prova o fluxo, não qual das duas agiu; a
// revalidação sozinha é travada por TestDiscordRevalidatesTheURLBeforeSending.
//
// Relógio real, como TestProberRoleProbesWithTheProductionClient. Para não
// esperar três intervalos de 60s, o teste devolve o alvo à fila (next_run_at
// no passado) a cada check gravado.
func TestIncidentFlowsFromProbeToAuditedNotification(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'e2e@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(chave)
	if err != nil {
		t.Fatal(err)
	}

	alvo, canal := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agora := time.Now()
	// O AAD é contrato do dado gravado (notify.configAAD); o teste o repete
	// porque a cifra é interna ao pacote.
	sealed, err := box.Seal([]byte("http://127.0.0.1:1/api/webhooks/1/tok"), crypto.AAD("notif_channels", "config_enc", canal))
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora.Add(-time.Minute), CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: canal, UserID: dono, Kind: "discord", ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	metricas := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t), MetricsAddr: metricas,
		MasterKey: chave, ShutdownGrace: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	prazo := time.After(30 * time.Second)
	passo := time.NewTicker(100 * time.Millisecond)
	defer passo.Stop()
	vistos := 0
	for {
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes do fim do fluxo: %v", err)
		case <-prazo:
			t.Fatalf("o fluxo não terminou em 30s: %d checks, estado %+v", vistos, estadoDoFluxo(t, pool, dono, canal))
		case <-passo.C:
		}
		if n := quantosChecks(t, pool, dono, alvo); n > vistos {
			vistos = n
			devolverAFila(t, pool, dono, alvo)
		}
		e := estadoDoFluxo(t, pool, dono, canal)
		if e.incidentes == 1 && e.notificacao == "failed" && e.auditorias == 1 && strings.Contains(lerMetricas(t, metricas), "\nincidents_open 1\n") {
			break
		}
	}

	e := estadoDoFluxo(t, pool, dono, canal)
	if e.erro != "invalid_channel" {
		t.Errorf("last_error = %q, queria invalid_channel", e.erro)
	}

	cancel()
	select {
	case err := <-fim:
		if err != nil {
			t.Errorf("run encerrou com erro: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run não encerrou depois do cancelamento")
	}
}

func portaLivre(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func lerMetricas(t *testing.T, addr string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		return "" // o servidor pode ainda não ter subido
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func quantosChecks(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) int {
	t.Helper()
	var n int
	escopoDoDono(t, pool, dono, `SELECT count(*) FROM checks WHERE target_id = $1`, []any{alvo}, &n)
	return n
}

// devolverAFila põe o alvo vencido de novo, para a próxima sonda sair no
// próximo tick em vez de daqui a um intervalo.
func devolverAFila(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE targets SET next_run_at = now() - interval '1 minute' WHERE id = $1`, alvo); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

type fluxo struct {
	incidentes  int
	notificacao string
	erro        string
	auditorias  int
}

func estadoDoFluxo(t *testing.T, pool *pgxpool.Pool, dono, canal uuid.UUID) fluxo {
	t.Helper()
	var f fluxo
	escopoDoDono(t, pool, dono, `SELECT
		(SELECT count(*) FROM incidents WHERE resolved_at IS NULL),
		coalesce((SELECT status FROM notifications WHERE channel_id = $1 AND event = 'opened'), ''),
		coalesce((SELECT last_error FROM notifications WHERE channel_id = $1 AND event = 'opened'), ''),
		(SELECT count(*) FROM audit_log WHERE action = 'notification.failed' AND entity_id = $1)`,
		[]any{canal}, &f.incidentes, &f.notificacao, &f.erro, &f.auditorias)
	return f
}

func escopoDoDono(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, sql string, args []any, dst ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, sql, args...).Scan(dst...); err != nil {
		t.Fatal(err)
	}
}

// O papel api com as dependências do Plano 3 ligadas: sem Channels ou
// Incidents no Deps, as rotas cairiam em panic (nil) na primeira chamada
// autenticada; sem Ready, /readyz responderia 503.
func TestAPIRoleWiresIncidentsChannelsAndReadyz(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'api@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	addr := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleAPI, DatabaseURL: testdb.DSN(t), HTTPAddr: addr,
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave, ShutdownGrace: time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	tok, err := auth.NewAccessToken(chave, dono, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pedir := func(caminho string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+caminho, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	prazo := time.Now().Add(10 * time.Second)
	for pedir("/readyz") != http.StatusOK {
		if time.Now().After(prazo) {
			t.Fatal("/readyz não ficou 200 em 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, caminho := range []string{"/api/v1/channels", "/api/v1/incidents"} {
		if s := pedir(caminho); s != http.StatusOK {
			t.Errorf("GET %s deu %d, queria 200", caminho, s)
		}
	}
	cancel()
	if err := <-fim; err != nil {
		t.Errorf("run encerrou com erro: %v", err)
	}
}
```

- [ ] **Step 4: Rodar e ver falhar**

Run: `go test -count=1 -run 'TestIncidentFlows|TestAPIRole' ./cmd/vigilume/`
Expected: FAIL. `TestIncidentFlowsFromProbeToAuditedNotification` estoura os 30 s com a notificação em `pending`, porque não há dispatcher no wiring. `TestAPIRoleWiresIncidentsChannelsAndReadyz` falha no `/readyz`, porque o `Deps` não tem `Ready`.

- [ ] **Step 5: Implementar o wiring**

Troque `cmd/vigilume/main.go` inteiro por:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
	"github.com/BipoXDXD/vigilume/internal/incident/history"
	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/prober"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

// totpIssuer é o nome que aparece no app autenticador do usuário.
const totpIssuer = "Vigilume"

func main() {
	role := flag.String("role", "", "all | api | prober (sobrepõe a env ROLE)")
	flag.Parse()

	getenv := os.Getenv
	if *role != "" {
		getenv = func(key string) string {
			if key == "ROLE" {
				return *role
			}
			return os.Getenv(key)
		}
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		// Antes de a config existir não há logger configurado; stderr basta.
		slog.Error("configuração inválida", "erro", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("iniciando", "cfg", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Error("encerrou com erro", "erro", err)
		os.Exit(1)
	}
	log.Info("encerrado")
}

// manutencaoDeParticoes é o intervalo do job que garante a partição do mês
// seguinte. A do mês seguinte já existe desde o boot, então um dia de
// intervalo deixa um mês inteiro de folga.
const manutencaoDeParticoes = 24 * time.Hour

// run é onde o wiring acontece. Mantido separado de main para que main tenha
// apenas responsabilidade de processo (flags, sinais, código de saída).
//
// Cada subsistema roda no mesmo errgroup. Se um falha — a porta ocupada, por
// exemplo —, o contexto do grupo cancela, os outros encerram, e o processo
// sai com erro, em vez de seguir meio vivo.
func run(ctx context.Context, cfg *config.Config) error {
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Com um papel que ignora RLS, a segunda barreira de isolamento estaria
	// desligada em silêncio (D8). A checagem vem ANTES da migration: CheckRole
	// só lê pg_roles, e migrar primeiro faria o boot recusado criar todas as
	// tabelas com o dono errado, que o papel comum depois não conseguiria usar.
	if err := storage.CheckRole(ctx, pool); err != nil {
		return err
	}

	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations aplicadas")

	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	clk := clock.Real{}

	// A partição do mês corrente tem de existir antes da primeira sonda.
	// Fica antes do errgroup: um retorno daqui não deixa servidor nenhum
	// rodando sem ninguém para desligá-lo.
	if cfg.Role.RunsProber() {
		if err := storage.EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil {
			return err
		}
	}

	reg := observability.NewRegistry()
	reg.MustRegister(storage.NewPoolCollector(pool))

	g, gctx := errgroup.WithContext(ctx)

	if cfg.MetricsAddr != "" {
		servir(gctx, g, observability.NewMetricsServer(cfg.MetricsAddr, reg), "métricas", cfg.ShutdownGrace)
	}

	if cfg.Role.RunsProber() {
		sched := prober.New(pool, box, clk, prober.NewMetrics(reg))
		grace := graceDoSondador(cfg.ShutdownGrace)
		g.Go(func() error { return sched.Run(gctx, grace) })
		g.Go(func() error {
			return storage.MaintainChecksPartitions(gctx, pool, clk, manutencaoDeParticoes)
		})
		// O dispatcher roda com o sondador (spec §8: fora do caminho
		// crítico da sonda, em worker próprio). A outbox no banco é o que
		// deixa --role=api e --role=prober separados sem perder nada.
		disp := notify.NewDispatcher(pool, box, clk, reg, notificadores(cfg)...)
		g.Go(func() error { return disp.Run(gctx) })
		slog.Info("sondador ativo", "max_em_voo", prober.MaxInFlight, "email", cfg.EmailEnabled())
	}

	if cfg.Role.RunsAPI() {
		store := auth.NewPGStore(pool)
		deps := httpapi.Deps{
			Store:     store,
			Sessions:  auth.NewManager(store, clk, cfg.IPHashKey),
			TOTP:      auth.NewTOTP(pool, box, totpIssuer),
			Targets:   targets.NewService(pool, box, clk),
			Incidents: history.NewService(pool, clk),
			Channels:  notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg), cfg.PublicBaseURL),
			Clock:     clk,
			Ready:     pool.Ping,
		}
		servir(gctx, g, httpapi.NewServer(cfg, httpapi.Routes(gctx, cfg, deps)), "api", cfg.ShutdownGrace)
	}

	return g.Wait()
}

// notificadores são os canais que o dispatcher sabe usar. O e-mail só
// entra com o Resend configurado; sem ele, uma notificação de e-mail falha
// de vez e vai para a auditoria.
func notificadores(cfg *config.Config) []notify.Notifier {
	ns := []notify.Notifier{notify.NewDiscord()}
	if cfg.EmailEnabled() {
		ns = append(ns, notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom))
	}
	return ns
}

// verificador devolve a interface nil, e não um *Resend nil, quando o
// e-mail está desligado: um *Resend nil numa interface não é nil, e o
// cadastro acharia que o e-mail está disponível.
func verificador(cfg *config.Config) notify.VerificationMailer {
	if !cfg.EmailEnabled() {
		return nil
	}
	return notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)
}

// graceDoSondador é o prazo que o sondador dá às sondas em voo no shutdown.
//
// Stop pode levar até grace + PersistTimeout: uma sonda que termina no limite
// ainda grava o check com prazo próprio. Descontar PersistTimeout faz o
// sondador inteiro caber no ShutdownGrace (15s, spec §6.2). Quando não cabe
// (grace ≤ PersistTimeout, só em teste), vai o grace inteiro: um prazo zero
// ou negativo abortaria as sondas na hora.
func graceDoSondador(total time.Duration) time.Duration {
	if g := total - prober.PersistTimeout; g > 0 {
		return g
	}
	return total
}

// servir roda srv até ctx acabar e então o desliga com prazo (spec §6.2).
// Uma falha ao subir volta como erro do grupo e derruba o resto.
func servir(ctx context.Context, g *errgroup.Group, srv *http.Server, nome string, grace time.Duration) {
	g.Go(func() error {
		falhou := make(chan error, 1)
		go func() {
			slog.Info("servindo", "servidor", nome, "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				falhou <- fmt.Errorf("servidor %s: %w", nome, err)
			}
		}()
		select {
		case err := <-falhou:
			return err
		case <-ctx.Done():
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `gofmt -w cmd internal && go vet ./... && go test -race -count=1 ./cmd/vigilume/`
Expected: PASS; o ponta a ponta leva uns 6 s.

- [ ] **Step 7: Sabotagem**

Em `run`, troque `g.Go(func() error { return disp.Run(gctx) })` por `_ = disp`. Rode `-run TestIncidentFlows`. Expected: FAIL depois de 30 s, com "estado {incidentes:1 notificacao:pending ...}". Restaure e registre.

- [ ] **Step 8: Documentação**

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`:

```markdown
# Pendências que atravessam o Plano 3

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` no Fly.io.** Herdada do Plano 1, ainda aberta. Sem ela, o rate limit por
   IP (inclusive o de `/verify/`) e o `ip_hash` da auditoria colapsam num valor único.
2. **Papel comum do Postgres no Fly.io**, dono das tabelas, sem superusuário nem `BYPASSRLS`
   (D8 do Plano 2). Os três detalhes de deploy continuam valendo (ver
   `PENDENCIAS-PARA-O-PLANO-3.md`). A migration 00005 cria quatro tabelas novas e troca uma
   função: o papel precisa de `CREATE` no schema `public`.
3. **`METRICS_ADDR` no Fly.io.** Decidir no Plano 5 entre `off` e um bind na interface privada.
4. **E-mail.** `RESEND_API_KEY` e `EMAIL_FROM` no Fly.io, as duas ou nenhuma. O domínio do
   remetente precisa estar verificado no Resend, ou todo envio volta 403 e vira falha definitiva.
   Sem elas, o cadastro de e-mail responde `400 kind/unavailable`, e o Discord segue funcionando.
5. **`PUBLIC_BASE_URL` precisa ser o host da API.** O link de verificação é
   `PUBLIC_BASE_URL/verify/{token}`, servido pela API, não pelo painel.

## Resolvidas no Plano 3

| Origem | Como fechou |
|---|---|
| Plano 2, ponto de extensão | `gravar` avalia a histerese na mesma transação do check |
| M4 | Check fora de ordem é ignorado pela histerese e gravado em `checks` |
| D9/M1 | `Scheduler.Reconcile` ao subir, a cada 30 s e a cada transição |
| `Scheduler.Run` silencioso com banco fora | `/readyz` pinga o banco (papel `api`) |
| Advisory lock de partição | `ensure_checks_partition` com `pg_advisory_xact_lock` (migration 00005) |
| `POST /metrics` sem trava | `TestMetricsServerOnlyAnswersGET` |
| `METRICS_ADDR=OFF` | `strings.EqualFold` |
| Tabela das 6 rotas; bordas de `/targets` e `/checks` via HTTP | `TestTargetRoutesRequireAuthentication`, `TestTargetQueriesRejectMalformedValues`, `TestChecksWindowIsHalfOpen` |
| `limit` não numérico reportava `out_of_range` | Agora `invalid` (D22) |

## Limites conhecidos do Plano 3

- **Entrega at-least-once (D7).** Um processo que morre entre o envio e a gravação do desfecho
  reenvia depois do lease (2 min). O Resend deduplica por `Idempotency-Key`; o Discord recebe
  a mensagem duas vezes.
- **Escopo `verify` limita linha, não coluna.** Nele, um `UPDATE` arbitrário poderia mudar o
  `user_id` da linha do token. A trava é haver uma única query nesse escopo (`ConfirmChannel`).
- **Renomear o alvo entre duas tentativas de e-mail** muda o corpo. O Resend responde 409
  `invalid_idempotent_request`, e a notificação falha de vez (D17).
- **`incidents_open` é global**: com vários sondadores, agregue com `max` (D19).
- **Alvo desativado mantém o incidente aberto; alvo apagado leva o histórico** (D18). A status
  page do Plano 4 precisa decidir se mostra incidentes de alvos que saíram da página.
- **Não há rota para desabilitar canal.** A coluna `enabled` existe e é respeitada no fan-out e
  no envio, mas só dá para mudar por SQL. O Plano 5 decide se o painel precisa disso.
- **O dispatcher só roda no papel `prober`.** Com `--role=api` sozinho, as notificações ficam
  na outbox até um sondador subir.
- **`TestIncidentFlowsFromProbeToAuditedNotification` passa por duas guardas.** A revalidação
  da allowlist e o `safedial` recusam a URL interna; o teste prova o fluxo, não qual das duas
  agiu. A revalidação sozinha é travada por `TestDiscordRevalidatesTheURLBeforeSending`.

## Para o Plano 4

- **R2**, adiada por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.
- **D6 do Plano 2:** `GET /targets/{id}/checks` com janela acima de 48h passa a ler
  `check_rollup_1h`.
- **Retenção (spec §12):** `DROP` das partições vencidas de `checks`; `audit_log` com 12 meses;
  e `notifications` já entregues, falhas e canceladas, que hoje crescem para sempre (sugestão:
  30 dias, no mesmo job de manutenção).
- **Status page:** o histórico de incidentes resolvidos (spec §10.2) lê `incidents` por alvo;
  `incidents_target_idx (target_id, id DESC)` já atende.

## Para o Plano 5

- Telas de incidentes (lista, reconhecer) e de canais (cadastro, estado de verificação,
  reenvio), consumindo os contratos das Tasks 8 e 9 do Plano 3.
- Documentar no cadastro de alvos que headers **não** seguem redirect para outro host:porta
  nem rebaixamento https→http (I3 do Plano 2).
- `Spec.Headers` decifrado com `LogValue` que redija os valores (M6 do Plano 2).
- Porta de métricas em desenvolvimento com `--role=api` e `--role=prober` no mesmo host (M7).
- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA; troca de autenticador;
  `HashPasswordWith` com parâmetros fora de faixa (herdados).

## Dívidas menores herdadas do Plano 2, ainda abertas

- Partições não herdam a RLS (a aplicação nunca consulta partição; está comentado).
- `MaintainChecksPartitions` sem teste; usa ticker real e dá panic com `every <= 0`.
- O comentário de `targets.sql` diz que toda query filtra por `user_id`; o `INSERT` não filtra.
- `initialisms` sem `ip` gera `IpHash` nos modelos de auth.
- `targets.Service.Get`/`List` decifram os headers só para devolver os nomes; uma linha
  corrompida derruba o `List` (o `ChannelService.List` do Plano 3 já não tem esse defeito).
- `apply` compara antes de normalizar; `Update` revalida a URL guardada; `Service.List` com
  `limit` fora de faixa cai para 50; `0` explícito em `POST` vira padrão e em `PATCH` dá 400.
- `Probe` com `Timeout == 0` gera contexto vencido (a validação garante ≥ 1000 ms).
- `TestConcurrentMigrationsSerialize` não confere a versão final do schema.
- `GRANT CREATE ON DATABASE` do testdb não sobrevive ao `Restore` (falta comentário).
- Logs do testcontainers poluem a saída de teste; nomes de teste em inglês ao lado de helpers
  em português.

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-incidentes-e-notificacoes.md`: decisões D1–D24 deste plano,
  com o custo de cada uma estar errada.
- `docs/superpowers/plans/2026-09-24-alvos-e-sondador.md`: decisões D1–D16 do Plano 2.
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md`: o estado anterior a este plano.
```

Em `docs/superpowers/plans/2026-09-22-roadmap.md`, troque a linha do Plano 3 por:

```markdown
| 3 | Incidentes e notificações | `2026-09-24-incidentes-e-notificacoes.md` | Incidente vira Discord + e-mail | 8–9 | concluído |
```

Em `.gitignore`, acrescente a linha `/.superpowers/`. Os documentos de pendências sempre disseram que o ledger era ignorado, mas o diretório só estava fora do controle de versão por nunca ter sido adicionado.

- [ ] **Step 9: Verificação completa e commit**

Run: `sqlc diff && go vet ./... && staticcheck ./... && gosec -quiet ./... && govulncheck ./... && go test -race -count=1 ./...`
Expected: tudo verde; é a mesma sequência do CI.

```bash
git add cmd/ internal/config/ docs/ .gitignore
git commit -m "feat: dispatcher e canais no wiring, e-mail configurável, ponta a ponta do incidente e pendências do Plano 4"
```

---
## Verificação final do plano

Ao fim da Task 11, e antes da revisão final do branch:

- [ ] `sqlc diff`, `go vet ./...`, `staticcheck ./...`, `gosec -quiet ./...` e `govulncheck ./...` sem saída de erro.
- [ ] `go test -race -count=1 ./...` verde, duas vezes seguidas: os testes de concorrência (SKIP LOCKED, advisory lock, M4) não podem ser intermitentes.
- [ ] `go test -short ./...` verde sem Docker (`incident`, `notify` senders e mensagem, `safedial`, `crypto`).
- [ ] O relatório de cada tarefa lista as sabotagens pedidas, o que foi quebrado e o que o teste disse.
- [ ] `git status` limpo; `.superpowers/` ignorado.

---

## Revisão do plano contra a spec

### 1. Cobertura

| Spec | Onde |
|---|---|
| §4.1 `incidents`, `notif_channels` | Task 2 (migration 00005) |
| §5.4 segredos em repouso (destino do canal) | Task 5 (`sealConfig`, AAD da linha), Task 7 |
| §5.5 RLS e 404 | Task 2 (políticas e testes), Tasks 7, 8 e 9 (404 do intruso) |
| §5.8 verificação obrigatória, 24h, hash, rate limit de envio e reenvio, pendente nunca recebe | Tasks 2, 3 (fan-out), 6 (envio), 7 e 9 |
| §7.2 histerese 3/5; `degraded` não conta | Task 1; Task 3 (`TestOnlyDownCountsAsFailure`) |
| §7.3 detector puro | Task 1 (desvio D1) |
| §8 `Notifier`; idempotência no banco; fan-out só verificados; retry com backoff e teto; auditoria; timeout; worker próprio | Tasks 3, 5, 6 e 11 |
| §9 `GET /incidents`, `POST /incidents/{id}/ack`, `/channels`, `/verify/{token}`, `/readyz` | Tasks 8 e 9 |
| §13 `incidents_open`, `notify_total{kind,result}`, `/readyz` com banco | Tasks 4, 6 e 8 |
| §14 banco real, sem mock; `-race` | Todas |
| Decisões do usuário (outbox, Resend do operador, Discord por teste, dívidas do Plano 2) | Tasks 2, 3, 6, 7, 10 e 11 |

### 2. Lacunas conscientes, com destino

- Retenção de `notifications` e de `audit_log` → Plano 4 (`PENDENCIAS-PARA-O-PLANO-4.md`).
- Status page lendo incidentes resolvidos → Plano 4.
- Telas de incidentes e canais; rota para desabilitar canal → Plano 5.
- `audit_log` tem `user_id` e continua sem RLS (D7 do Plano 2). O dispatcher grava nela no escopo do sondador.

### 3. Placeholders

Nenhum passo diz "implementar depois" ou "tratar os erros"; todo passo de código traz o código. Os blocos de código deste plano foram gerados a partir da cópia em que tudo rodou.

### 4. Consistência de tipos

Os nomes usados entre tarefas saem de uma mesma árvore compilada: `ClaimDueTargetsRow.UserID` (Tasks 2 e 3), `avaliar` devolvendo `incident.Action` (Tasks 3 e 4), `sendTimeout` e `chaveDeTeste` (Tasks 6 e 7), `newAPICom` e `envelopeComCampos` (Tasks 8, 9 e 10), `DiscordTester` e `VerificationMailer` (Tasks 7, 9 e 11).
