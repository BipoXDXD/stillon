# Vigilume — Plano 5: API do painel

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fechar o que o painel React (Plano 6) precisa e a API não tem — usuário inicial e recuperação de senha pelo CLI, `GET /me` com e-mail e 2FA, troca de senha, lista e encerramento de sessões, o dashboard numa chamada (`GET /targets/summary`), o filtro `?target_id=` de `/incidents` e o teto de 100 alvos —, e, antes de tudo, corrigir a corrida em que uma família de sessão revogada sobrevivia pelo sucessor de uma rotação concorrente.

**Architecture:** Toda mutação de `sessions` passa a tomar uma advisory lock de transação **por usuário** antes de qualquer trava de linha; é o que torna "revogar" definitivo. O access token ganha o claim `sid` (a família do refresh), que marca a sessão atual e deixa as rotas de credencial e de sessão recusarem um token de família já encerrada. O pacote novo `internal/account` é a conta do dono, chamado pelo CLI (`vigilume user …`) e pela API, com uma verificação de senha única (`auth.CheckPassword`) e uma política de senha única (15 a 128 code points). O resumo do dashboard lê os rollups horários até a marca d'água do dono e os checks brutos depois dela, e o estado atual sai de uma regra só (`internal/targetstate`), a mesma da página pública. Todo SQL novo é sqlc.

**Tech Stack:** Go 1.26, PostgreSQL 18 nos testes (17 em produção), `pgx/v5`, `sqlc` v1.31.1, `goose` v3, `golang.org/x/term` (novo), `testcontainers-go`.

**Spec:** `docs/superpowers/specs/2026-09-24-plano-5-api-do-painel-design.md` (aprovada), e o contrato comum `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`, **incluindo a seção "Reconciliação"**, que prevalece sobre a spec onde as duas diferem.

**Roteiro:** `docs/superpowers/plans/2026-09-22-roadmap.md` (a Task 10 o atualiza: o antigo Plano 5 virou 5, 6 e 7).

**Pendências absorvidas:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`. Entram: a `normalizarEmail` pendente (vira `auth.NormalizeEmail`) e o que o contrato pediu. O resto segue para o `PENDENCIAS-PARA-O-PLANO-6.md` que a Task 10 escreve.

**Validação prévia.** Todo o código deste plano rodou numa cópia do repositório (`git archive` de `main` em `3bc4c54`) antes de o plano ser entregue: PostgreSQL 18 real via testcontainers, `sqlc generate`, `go test -race -count=1 ./...` verde, `sqlc diff`, `gofmt`, `go vet`, `staticcheck`, `gosec` e `govulncheck` limpos. **O estado da árvore depois de cada tarefa** foi guardado e conferido (compila, `vet`, `gofmt`, `sqlc diff`, testes dos pacotes da tarefa com `-race`), e **os testes de cada tarefa foram rodados sobre o estado anterior** e falharam (Tasks 1–9; a 10 é trava de wiring, nasce verde e foi provada por sabotagem). Este texto foi aplicado sobre `main` por um script (`replay`) e reconstruiu cada estado byte a byte; o último é idêntico à cópia validada. Cada sabotagem pedida nos passos foi feita, e o teste ficou vermelho pela razão escrita. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

**Como ler os diffs.** Arquivo novo aparece inteiro, depois de "Crie `caminho`:". Arquivo modificado aparece como diff unificado contra o estado da tarefa anterior, depois de "Aplique em `caminho`:"; cada bloco tem o seu cabeçalho. O contexto basta para aplicar à mão, ou com `patch -p1` a partir da raiz. `internal/storage/db/*.go` é gerado: quando a tarefa muda SQL, o passo diz `sqlc generate`, e o diff não aparece.

---

## Global Constraints

Valores copiados da spec, do contrato e das decisões do usuário. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/vigilume`. **PostgreSQL 18 nos testes** (`TEST_POSTGRES_IMAGE` troca a imagem), 17 em produção. Sem framework HTTP e sem ORM (spec §3.3). **Código novo de banco em sqlc** (contrato, item 5); `internal/storage/db/*.go` é gerado e **nunca** editado à mão. A migração de `auth.PGStore`/`auth.TOTP` para sqlc (**R2**) é plano próprio e **não** entra aqui.
- **IDs UUIDv7**, gerados em Go (spec §4.2). Toda função de serviço recebe `userID` logo depois de `ctx`; recurso de outro usuário é **404, nunca 403** (spec §5.5).
- **`users` e `sessions` continuam sem RLS** (D7 do Plano 2); toda query nova que parte de um usuário filtra por `user_id`. As tabelas com RLS continuam atrás de `storage.InUserTx`.
- **Shapes JSON do contrato, sem mudança:** `GET /me` → `{id, email, totp_enabled, created_at}`; `POST /auth/password` `{current_password, new_password, totp_code}` → `204`; `GET /sessions` → `{items: [{id, created_at, last_used_at, user_agent, current}]}`, só famílias vivas, **sem IP nem hash**; `DELETE /sessions/{id}` → `204`, alheia ou inexistente `404`; `GET /targets/summary` → `{items: [{id, name, enabled, state, last_checked_at, last_latency_ms, uptime_24h, open_incident_id, spark: [{bucket, total, ok, p50_ms}]}]}`, ordem de `GET /targets`, sem paginação, `state` pela regra da página pública (D15 do Plano 4).
- **Reconciliação do contrato:** `GET /incidents?target_id=` é aditivo, mesma paginação, `target_id` de outro usuário devolve **lista vazia, não 404**; **`409 target_limit_reached`** no `POST /targets`, **teto de 100 alvos**; `vigilume user create` sai com **código 3** = "já existe usuário, nada mudou", e esse sentido não muda; o despacho de subcomandos nasce aqui e o Plano 7 acrescenta `healthcheck` e `version`; **sem blocklist de senhas** (decisão do usuário): só o mínimo de 15 code points.
- **Erros** no envelope da spec §9.1; os `fields` são vocabulário fechado (`new_password`: `too_short` | `too_long`), nunca eco da entrada. Senha atual errada, segundo fator errado ou ausente e conta bloqueada são **o mesmo `401 invalid_credentials`** (R34); **`401 unauthorized` fica só para token ausente, inválido ou de família morta** (D24), porque é nele que o painel renova a sessão.
- **Toda mutação exige `X-Requested-With: vigilume`** (spec §5.3), o que a cadeia global já faz.
- **Senha nunca em argumento nem em variável de ambiente.**
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa**, e `sqlc diff` limpo quando a tarefa mexe em SQL.
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não revoga", "não expõe", "não vê"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho **pela razão certa**, restaura, e **registra no relatório** o que quebrou e o que viu. Sabotagem que não compila não prova nada (duas das deste plano precisaram ser reescritas por causa de import sem uso). O teste vacuamente verde apareceu 14 vezes no Plano 1, 5 no Plano 2, 3 no Plano 4 e **uma vez na validação deste plano** (D22).

### Desvios registrados em relação à spec aprovada

| Spec | Aqui | Motivo |
|---|---|---|
| §6: `/totp/enroll` e `/totp/disable` atrás do limitador do login | Só `POST /auth/password` divide o balde do login (D5) | O teste existente `TestDisableRequiresPasswordAndPossession` faz sete requisições legítimas seguidas e levou `429`. O bloqueio por conta, que agora conta as falhas dessas rotas (D4), já limita as tentativas; o limitador por IP ali só atrapalharia o dono |
| §5.2 `ResetPasswordHash`, `ChangePasswordHash` | `ReplaceUserHash`, `SwapUserHash` | O `gosec` (G101) lê a constante gerada com "password" no nome como credencial embutida |
| §5.3 `config.LoadDatabaseURL` | O CLI lê `DATABASE_URL` direto | Uma linha; uma função em `config` só para isso não se paga |
| §8.6 `TestPublicPageAndSummaryShareTheRule` | Provado por sabotagem, sem teste próprio | Mudar `targetstate.StaleIntervals` para 2 deixa vermelhos o teste da regra **e** o da página pública: as duas projeções chamam a mesma função |
| §4.8 nulos do `LEFT JOIN LATERAL` e do percentil | `coalesce` no SQL e a regra `ok = 0 ⇔ sem p50` no Go | O sqlc não enxerga essa nulidade e geraria `string`/`int32` onde o Postgres manda `NULL` (D16) |

---

## Decisões tomadas neste plano

O usuário aprovou a spec; o que está aqui é como cada decisão dela virou código, com o custo de estar errada. As referências "Dn do Plano 5" nos comentários do código apontam para esta tabela.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | **Mutação de sessão serializada por usuário:** `pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' \|\| user_id, 0))` antes de qualquer trava de linha, em `Rotate`, `Revoke` (logout), troca de senha, `DELETE /sessions/{id}` e `set-password`. `Rotate` e `Revoke` leem o dono do refresh sem trava e só então travam | Trava da linha viva; tabela `session_families` | Sem ela, o sucessor de uma rotação concorrente escapa da revogação (Task 1, vermelho no código de hoje). Na ordem inversa (linha, depois usuário), *deadlock* (provado na sabotagem). Uma consulta a mais por refresh |
| D2 | **Claim `sid` obrigatório** no access token = família do refresh; token sem `sid` válido é inválido; `NewAccessToken` recusa família nula | Ler o cookie em `/sessions` | O cookie tem `Path=/api/v1/auth`. Nenhum token sem `sid` está em circulação: o painel ainda não foi publicado |
| D3 | **Política de senha nova: 15 a 128 code points**, qualquer caractere, sem composição, sem normalização, sem aparar | 8; NFC | O login continua aceitando senha antiga mais curta. Mudar o número é uma constante |
| D4 | **`auth.CheckPassword`:** bloqueio (com `VerifyDummy`) + `VerifyPassword` + `RecordFailure`, usado pelo login, pela troca de senha e por `/totp/enroll` e `/totp/disable` | Deixar `senhaConfere` só comparando | Antes, um access token roubado testava senhas no enroll sem nunca travar a conta |
| D5 | **Só `POST /auth/password` divide o balde do login** (mesma instância, 10/min, burst 5) | Uma instância por rota; o balde também nas rotas de TOTP | Instâncias separadas dobrariam o orçamento de quem adivinha. Ver o desvio sobre o TOTP |
| D6 | **CLI: senha só pelo stdin.** Terminal: duas vezes, sem eco. Pipe: uma linha, até 4 KiB, tira **um** `\n` ou `\r\n` final; sobrando quebra de linha, é erro | Flag; variável | Argumento aparece em `ps`; variável em `/proc/<pid>/environ` e `docker inspect` |
| D7 | **CLI lê só `DATABASE_URL`** e faz o que o boot faz: `CheckRole` → `Migrate` → operação | `config.Load` inteiro | Migrar como superusuário num banco virgem criaria as tabelas com o dono errado |
| D8 | **`user create` recusa se existe qualquer usuário** (spec §2.1), código 3; `set-password` com e-mail desconhecido, código 4; uso, 2; ambiente, 1 | Recusar só o mesmo e-mail | Um segundo usuário exige SQL |
| D9 | **Criação sob `pg_advisory_xact_lock('vigilume.user_create')`**, contando de novo dentro da trava | Contar fora | Dois CLIs simultâneos criariam dois donos (provado na sabotagem) |
| D10 | **`set-password`:** hash novo, bloqueio zerado, **todas** as famílias revogadas, auditado; o 2FA fica | Manter as sessões | Recuperação é "perdi" ou "vazou": manter as sessões deixaria o invasor dentro |
| D11 | **Família viva exigida** em `POST /auth/password` (no serviço, duas vezes: antes da senha e sob a trava) e em `GET`/`DELETE /sessions` (no handler); morta é `401 unauthorized` | Checar em toda rota | Nas rotas de recurso, um token de sessão encerrada vale até 15 min (spec §5.2) |
| D12 | **Troca de senha:** política → família → `CheckPassword` → TOTP (`Verify`, aceita recuperação; ausente = errado; conta falha) → hash → transação (trava, família de novo, **CAS** do hash, as outras famílias revogadas, auditoria) | Verificar a senha antes da política | O `204` sempre quer dizer "trocada e a atual viva". O timestep TOTP consumido antes de uma desistência obriga a esperar o código seguinte |
| D13 | **`GET /sessions`:** a linha viva de cada família; `created_at` = instante do **UUIDv7** da família; `last_used_at` = `created_at` da linha viva; `user_agent` cortado em 256 code points; ordem `created_at DESC, id DESC`; se houver duas linhas vivas da mesma família, fica a mais nova | Menor `created_at` das linhas | A retenção apaga linhas antigas de uma família viva, e o mínimo andaria para a frente |
| D14 | **`DELETE /sessions/{id}`:** `404` para tudo que não aparece em `GET /sessions` (inexistente, alheia, revogada, vencida, id malformado); a própria sessão também **apaga o cookie** de refresh | `204` idempotente | O painel trata `404` como "já encerrada" |
| D15 | **Regra única do estado atual** em `internal/targetstate` (3 intervalos, inclusivo); `statuspage` delega | Copiar a regra | Duas cópias divergiriam |
| D16 | **Summary híbrido:** rollups de `[H−24h, corte)`, brutos agregados com a fórmula dos rollups de `[corte, H)`, e a hora corrente só contada; `corte` = marca d'água **do dono** + 1h, limitada à janela. `uptime_24h` = spark + hora corrente; `last_latency_ms` nulo para `down` | Só rollups; só brutos | Medido na spec: ~33 ms com 100 alvos, contra ~390 ms só de brutos. Só rollups deixaria o uptime cego à queda em curso |
| D17 | **Teto de 100 alvos por usuário** (= `MaxPageSize`), contado e inserido sob `pg_advisory_xact_lock('vigilume.targets:' \|\| user_id)`; `409 target_limit_reached` | Sem teto | Reconciliado no contrato. Dois `POST` simultâneos passariam do teto sem a trava (provado) |
| D18 | **`GET /incidents?target_id=`:** aditivo, combina com `status` e cursor; alheio → lista vazia; malformado → `400` com `field: target_id` | 404 para o alheio | Reconciliado no contrato: o filtro não confirma a existência de alvo alheio |
| D19 | **Migration 00007:** um índice parcial, `sessions (user_id, family_id) WHERE used_at IS NULL AND revoked_at IS NULL` | Sem índice | `sessions_user_idx` leria as mais de mil linhas rotacionadas de uma família em uso |
| D20 | **Pacote `internal/account`** para CLI e API; todo SQL novo em sqlc, inclusive os dois métodos novos do `pgTx` (via `db.New(t.tx)`) | SQL à mão | Contrato, item 5 |
| D21 | **`golang.org/x/term` v0.46.0** | `syscall` à mão | Mesmo mantenedor do `x/crypto`; não trouxe dependência transitiva nova |
| D22 | **Achados da validação:** (a) um teste nasceu vacuamente verde — em `TestIncidentListFiltersByTarget`, a asserção "`target_id` + `status`" passava com o handler ignorando `target_id`, porque o outro alvo só tinha incidente aberto; o teste agora dá um resolvido aos dois alvos; (b) o `gosec` G101 na constante gerada (ver os desvios); (c) o limitador nas rotas de TOTP (ver os desvios) | — | — |
| D23 | **Guardas duplas, registradas para ninguém confundir teste com trava:** a família checada antes e sob a trava (D12); a RLS e o `WHERE user_id` no summary e na contagem do teto; a dedupe por família e o `used_at IS NULL` em `/sessions`; o `Authn` e o próprio handler nas rotas novas. Sabotar só uma de cada par deixa o teste verde; cada teste diz isso num comentário, e a sabotagem registrada quebra as duas | — | — |
| D24 | **`unauthorized` só para token recusado** (ausente, inválido, família morta); senha errada, código TOTP errado ou ausente e conta bloqueada são `401 invalid_credentials` em `/auth/password`, `/auth/totp/enroll` e `/auth/totp/disable`. No `disable`, código ausente ou errado também conta no bloqueio. **Corrige a ambiguidade apontada pelo Plano 6**: o painel renova a sessão em `401 unauthorized`, e o `disable` respondia `unauthorized` para um código digitado errado, o que viraria renovação e logout | Manter o `unauthorized` do Plano 1 | O painel trataria erro de digitação como sessão expirada |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa o Vigilume, com o que uma pessoa razoável esperaria. Cada uma tem teste na tarefa dona.

1. **O dono encerra a sessão de um invasor que está renovando em laço** (ou troca a senha enquanto isso). Espera-se que a sessão caia de vez, e não que sobreviva pelo refresh que estava sendo rotacionado naquele instante. → Task 1, `TestRevokingDuringRotationLeavesNothingAlive`; Task 6, `TestRevokeSessionTakesTheUserLock`; Task 5, `TestChangePasswordTakesTheUserLock`.
2. **O operador cria o usuário com `echo "senha" | vigilume user create`** (ou com um arquivo salvo no Windows, `\r\n`). Espera-se que a senha gravada seja a linha sem a quebra, e que um arquivo errado com duas linhas seja recusado em vez de virar uma senha que ninguém sabe. → Task 4, `TestUserPasswordLineHandling`.
3. **O dono perdeu o celular do 2FA e quer trocar a senha com um código de recuperação.** Espera-se que o código valha no mesmo campo e seja consumido. → Task 5, `TestChangePasswordAcceptsARecoveryCode`.
4. **O sondador ou a manutenção pararam há horas.** Espera-se "sem dados" no dashboard, e não "no ar", e um spark correto a partir dos checks brutos. → Task 7, `TestSummaryWithoutRollupsReadsRaw`, `TestSummaryNullables`; a borda de 3 intervalos em `TestCurrent`.
5. **O dono clica duas vezes em "encerrar", ou encerra a própria sessão.** Espera-se `404` no segundo clique, sem erro para ele, e, na própria sessão, o cookie de refresh apagado. → Task 6, `TestRevokeSessionTwiceIs404`, `TestRevokeCurrentSessionClearsCookie`.

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/storage/queries/sessions.sql` (novo) | Dono do refresh, trava do usuário, família viva, listagem e revogações. |
| `internal/storage/queries/users.sql` (novo) | Conta, criação, troca e redefinição do hash, trava de criação, auditoria com entidade. |
| `internal/storage/queries/targets.sql` | As cinco consultas do resumo; trava e contagem do teto. |
| `internal/storage/queries/incidents.sql` | `target_id` em `ListIncidents`. |
| `internal/storage/migrations/00007_sessions_live.sql` (novo) | O índice parcial das sessões vivas. |
| `internal/auth/{session,store}.go` | Trava do usuário em `Rotate`/`Revoke`; dois métodos novos no `Tx`. |
| `internal/auth/token.go`, `internal/httpapi/middleware/authn.go` | Claim `sid`, `AccessClaims`, `middleware.FamilyID`. |
| `internal/auth/{password,user}.go` | Política de senha, `NormalizeEmail`, `CheckPassword`. |
| `internal/account/account.go` (novo) | Conta do dono: criar, redefinir, `Me`, trocar senha, listar e revogar sessões. |
| `cmd/vigilume/{main,user}.go` | Despacho de subcomandos; `vigilume user create` e `set-password`. |
| `internal/httpapi/accounthandlers.go` (novo) | `GET /me`, `POST /auth/password`, `GET /sessions`, `DELETE /sessions/{id}`. |
| `internal/httpapi/{authhandlers,totphandlers,routes}.go` | `grantSession` com família; `CheckPassword`; cookie; rotas novas. |
| `internal/targetstate/targetstate.go` (novo) | A regra do estado atual. |
| `internal/targets/summary.go` (novo), `service.go`, `target.go` | `Summary`; teto de alvos. |
| `internal/httpapi/targethandlers.go`, `internal/apierr/errors.go` | `GET /targets/summary`; `409 target_limit_reached`. |
| `internal/incident/history/history.go`, `internal/httpapi/incidenthandlers.go` | Filtro `target_id`. |
| `internal/statuspage/public.go` | `estado` delega a `targetstate`. |
| `docs/superpowers/PENDENCIAS-PARA-O-PLANO-6.md` (novo), roteiro | O que atravessa para o Plano 6. |

---

## Ordem das tarefas e seu motivo

1. **A corrida de revogação** vem primeiro, com o teste que a reproduz no código de hoje: tudo o que o plano acrescenta (encerrar sessão, trocar senha, redefinir pelo CLI) revoga famílias, e nada disso vale sem ela.
2. **O `sid`** antes de qualquer rota que precise saber qual é a sessão atual.
3. **A política de senha e o `CheckPassword`**, que o CLI e a troca de senha usam.
4. **O CLI** e o pacote `account`: é o que cria o primeiro usuário; o Plano 7 depende dele.
5. **`/me` e a troca de senha**, e 6. **as sessões**, sobre o `account` e o `sid`.
7. **O resumo do dashboard**, independente das anteriores, com a regra de estado extraída.
8. **O teto de alvos**, que o resumo sem paginação pressupõe.
9. **O filtro de incidentes**, pequeno e isolado.
10. **Wiring das rotas novas e documentação.**

---

### Task 1: A corrida de revogação — mutações de sessão serializadas por usuário (D1)

**Files:**
- Create: `internal/storage/queries/sessions.sql` (`SessionUserByHash`, `LockUserSessions`)
- Modify: `internal/auth/store.go` (dois métodos no `Tx` e no `pgTx`, via sqlc)
- Modify: `internal/auth/session.go` (`travarSessao`, usado por `Rotate` e `Revoke`)
- Test: `internal/auth/session_race_test.go` (novo), `internal/auth/session_test.go` (o `txFalsa` ganha os dois métodos)

**Interfaces:**
- Consumes: nada de tarefa anterior.
- Produces:
  - `db.SessionUserByHash(ctx, tokenHash []byte) (uuid.UUID, error)` e `db.LockUserSessions(ctx, userID uuid.UUID) error` (gerados).
  - `auth.Tx` ganha `SessionUserByHash(ctx, hash []byte) (uuid.UUID, error)` e `LockUserSessions(ctx, userID uuid.UUID) error`. Toda implementação de `Tx`, inclusive fakes de teste, precisa deles.
  - A chave da trava, que as Tasks 4–6 repetem em SQL: `hashtextextended('vigilume.sessions:' || user_id::text, 0)`.

**Contexto para quem implementa.** Em READ COMMITTED, `UPDATE sessions SET revoked_at = … WHERE family_id = F AND revoked_at IS NULL` espera a linha viva que uma rotação travou (`SELECT … FOR UPDATE`). A rotação insere o sucessor, marca a antiga como usada e comita. O `UPDATE` então revoga a linha que esperou, mas não enxerga o sucessor: ele entrou depois do *snapshot* do comando. A família "revogada" continua viva pelo sucessor. Hoje isso afeta a detecção de reúso (`Rotate` com token usado revoga a família enquanto o dono rotaciona a linha viva) e o logout apresentado com um refresh antigo enquanto o ladrão rotaciona. O teste abaixo reproduz os dois no código de `main`.

A correção: toda transação que muda sessões de um usuário toma `pg_advisory_xact_lock` desse usuário **antes** de qualquer trava de linha. Com a trava, a rotação termina antes de a revogação começar, e o *snapshot* da revogação inclui o sucessor. Como `Rotate` e `Revoke` só conhecem o hash do token, eles leem o dono **sem trava** e relêem a linha **com trava** depois; é a segunda leitura que vale. A ordem (usuário, depois linha) em toda transação é o que impede o *deadlock*: a sabotagem 2 mostra o `40P01` que a ordem inversa produz.

O teste segura a rotação no meio (depois de `LockSessionByHash`) com um `Store` que embrulha o `Tx`, dispara a revogação e espera, por `pg_stat_activity`, que ela esteja esperando uma trava antes de soltar a rotação. Sem essa espera, o teste soltaria a rotação cedo demais e nunca exercitaria a corrida.

- [ ] **Step 1: Escrever o teste que reproduz a corrida**

Crie `internal/auth/session_race_test.go`:

```go
package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// storeQuePausa segura a transação logo depois de LockSessionByHash, com a
// linha viva travada: é o instante em que uma revogação concorrente precisa
// esperar a rotação terminar.
type storeQuePausa struct {
	inner  auth.Store
	travou chan struct{}
	solta  chan struct{}
}

func (s storeQuePausa) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		return fn(ctx, txQuePausa{Tx: tx, travou: s.travou, solta: s.solta})
	})
}

type txQuePausa struct {
	auth.Tx
	travou chan struct{}
	solta  chan struct{}
}

func (t txQuePausa) LockSessionByHash(ctx context.Context, hash []byte) (auth.SessionRow, error) {
	r, err := t.Tx.LockSessionByHash(ctx, hash)
	close(t.travou)
	<-t.solta
	return r, err
}

// esperarAlguemBloqueado espera um backend deste banco aparecer esperando uma
// trava (de linha ou advisory). É o que prova que a revogação chegou ao ponto
// de conflito antes de a rotação ser solta; sem isso, o teste poderia soltar a
// rotação cedo demais e nunca exercitar a corrida.
func esperarAlguemBloqueado(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a revogação nunca ficou esperando a rotação: o teste não exercitou a corrida")
}

// Revogar uma família enquanto ela é rotacionada não pode deixar o sucessor
// vivo (§4.7 da spec do Plano 5).
//
// Em READ COMMITTED, o UPDATE que revoga espera a linha viva travada pela
// rotação; a rotação insere o sucessor e comita; o UPDATE revoga a linha que
// esperou, mas não enxerga o sucessor, que entrou depois do snapshot dele. A
// família "revogada" continuava viva. Os dois caminhos que revogam família
// hoje são a detecção de reúso e o logout com um refresh antigo — o caso em
// que o ladrão está rotacionando a cadeia viva.
func TestRevokingDuringRotationLeavesNothingAlive(t *testing.T) {
	casos := []struct {
		nome    string
		revogar func(ctx context.Context, mgr *auth.Manager, antigo string) error
	}{
		{"detecção de reúso", func(ctx context.Context, mgr *auth.Manager, antigo string) error {
			_, _, err := mgr.Rotate(ctx, antigo, meta)
			if errors.Is(err, auth.ErrTokenReuse) {
				return nil
			}
			return err
		}},
		{"logout com refresh antigo", func(ctx context.Context, mgr *auth.Manager, antigo string) error {
			return mgr.Revoke(ctx, antigo)
		}},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			mgr, pool, clk, userID := setup(t)
			ctx := context.Background()

			antigo, _, err := mgr.Issue(ctx, userID, meta)
			if err != nil {
				t.Fatal(err)
			}
			vivo, s, err := mgr.Rotate(ctx, antigo, meta)
			if err != nil {
				t.Fatal(err)
			}

			travou, solta := make(chan struct{}), make(chan struct{})
			lento := auth.NewManager(
				storeQuePausa{inner: auth.NewPGStore(pool), travou: travou, solta: solta}, clk, ipKey)

			rotacao := make(chan error, 1)
			go func() {
				_, _, err := lento.Rotate(ctx, vivo, meta)
				rotacao <- err
			}()
			<-travou

			revogacao := make(chan error, 1)
			go func() { revogacao <- caso.revogar(ctx, mgr, antigo) }()
			esperarAlguemBloqueado(t, pool)
			close(solta)

			if err := <-rotacao; err != nil {
				t.Fatalf("a rotação concorrente falhou: %v", err)
			}
			if err := <-revogacao; err != nil {
				t.Fatalf("a revogação falhou: %v", err)
			}

			var vivas int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM sessions
				  WHERE family_id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
				s.FamilyID).Scan(&vivas); err != nil {
				t.Fatal(err)
			}
			if vivas != 0 {
				t.Errorf("%d linha(s) viva(s) na família revogada: o sucessor da rotação concorrente escapou", vivas)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar e ver falhar (no código de hoje)**

Run: `go test -count=1 -run TestRevokingDuringRotation ./internal/auth/`
Expected: FAIL nos dois subtestes, `1 linha(s) viva(s) na família revogada: o sucessor da rotação concorrente escapou`.

- [ ] **Step 3: Implementar**

Crie `internal/storage/queries/sessions.sql`:

```sql
-- Sessões (spec §5.2). sessions não tem RLS (D7 do Plano 2): login e refresh
-- a consultam por e-mail e por hash, antes de existir usuário na transação.
-- Toda query daqui que parte de um usuário filtra por user_id.

-- O dono de um refresh, lido SEM trava: serve só para saber qual trava de
-- usuário pegar antes de travar a linha (D1 do Plano 5).
-- name: SessionUserByHash :one
SELECT user_id FROM sessions WHERE token_hash = $1;

-- Serializa as mutações de sessão de um usuário (D1 do Plano 5). É de
-- TRANSAÇÃO: sai sozinha no commit ou no rollback. Toda transação que mexe em
-- linhas de sessions a pega ANTES de qualquer trava de linha, e é isso que
-- impede o ciclo (deadlock) entre rotação e revogação.
-- name: LockUserSessions :exec
SELECT pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' || sqlc.arg(user_id)::uuid::text, 0));
```

Rode `sqlc generate` (cria `internal/storage/db/sessions.sql.go`).

Aplique em `internal/auth/store.go`:

```diff
--- a/internal/auth/store.go
+++ b/internal/auth/store.go
@@ -10,6 +10,8 @@
 	"github.com/jackc/pgx/v5"
 	"github.com/jackc/pgx/v5/pgconn"
 	"github.com/jackc/pgx/v5/pgxpool"
+
+	"github.com/BipoXDXD/vigilume/internal/storage/db"
 )
 
 var ErrNotFound = errors.New("registro não encontrado")
@@ -32,6 +34,11 @@
 // poderiam ambas passar pela checagem de reúso antes de qualquer uma marcar
 // used_at.
 type Tx interface {
+	// SessionUserByHash e LockUserSessions vêm antes de LockSessionByHash em
+	// toda transação que muda sessões (D1 do Plano 5): a trava do usuário
+	// primeiro, a da linha depois.
+	SessionUserByHash(ctx context.Context, hash []byte) (uuid.UUID, error)
+	LockUserSessions(ctx context.Context, userID uuid.UUID) error
 	LockSessionByHash(ctx context.Context, hash []byte) (SessionRow, error)
 	InsertSession(ctx context.Context, r SessionRow) error
 	MarkSessionUsed(ctx context.Context, id uuid.UUID, at time.Time) error
@@ -86,6 +93,26 @@
 	return r, nil
 }
 
+// SessionUserByHash e LockUserSessions são sqlc (contrato dos Planos 5–7,
+// item 5); o resto deste arquivo migra no R2.
+func (t *pgTx) SessionUserByHash(ctx context.Context, hash []byte) (uuid.UUID, error) {
+	id, err := db.New(t.tx).SessionUserByHash(ctx, hash)
+	if errors.Is(err, pgx.ErrNoRows) {
+		return uuid.Nil, ErrNotFound
+	}
+	if err != nil {
+		return uuid.Nil, fmt.Errorf("buscar dono da sessão: %w", err)
+	}
+	return id, nil
+}
+
+func (t *pgTx) LockUserSessions(ctx context.Context, userID uuid.UUID) error {
+	if err := db.New(t.tx).LockUserSessions(ctx, userID); err != nil {
+		return fmt.Errorf("travar sessões do usuário: %w", err)
+	}
+	return nil
+}
+
 func (t *pgTx) InsertSession(ctx context.Context, r SessionRow) error {
 	_, err := t.tx.Exec(ctx, `
 		INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, ip_hash, user_agent)
```

Aplique em `internal/auth/session.go`:

```diff
--- a/internal/auth/session.go
+++ b/internal/auth/session.go
@@ -100,7 +100,7 @@
 	var reuso bool
 
 	err := m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
-		cur, err := tx.LockSessionByHash(ctx, hash)
+		cur, err := travarSessao(ctx, tx, hash)
 		if errors.Is(err, ErrNotFound) {
 			return ErrInvalidSession
 		}
@@ -167,10 +167,10 @@
 	hash := crypto.HashToken(token)
 	now := m.clk.Now()
 	return m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
-		// LockSessionByHash trava a linha com FOR UPDATE, o que fecha a
-		// corrida com uma rotação simultânea: o logout não pode ler a família
-		// e revogá-la enquanto uma rotação insere um sucessor nela.
-		cur, err := tx.LockSessionByHash(ctx, hash)
+		// A trava do usuário, e não a da linha, é o que fecha a corrida com
+		// uma rotação simultânea: o cookie apresentado pode ser um refresh
+		// antigo, e a linha viva que a rotação trava é outra (D1 do Plano 5).
+		cur, err := travarSessao(ctx, tx, hash)
 		if errors.Is(err, ErrNotFound) {
 			return nil // R28
 		}
@@ -181,6 +181,31 @@
 	})
 }
 
+// travarSessao pega a trava do usuário ANTES da trava da linha (D1 do Plano
+// 5), e toda transação que muda sessões segue essa ordem: é o que impede o
+// ciclo entre duas delas.
+//
+// A trava da linha sozinha não basta. Em READ COMMITTED, o UPDATE que revoga
+// uma família espera a linha viva que uma rotação travou; a rotação insere o
+// sucessor e comita; o UPDATE revoga a linha que esperou e não enxerga o
+// sucessor, que entrou depois do snapshot dele. A família "revogada" seguia
+// viva pelo sucessor (TestRevokingDuringRotationLeavesNothingAlive). Com a
+// trava do usuário, a rotação termina antes de a revogação começar, e o
+// snapshot da revogação já inclui o sucessor.
+//
+// A primeira leitura é sem trava e serve só para descobrir o usuário; a linha
+// é relida, travada, logo depois, e é essa segunda leitura que vale.
+func travarSessao(ctx context.Context, tx Tx, hash []byte) (SessionRow, error) {
+	userID, err := tx.SessionUserByHash(ctx, hash)
+	if err != nil {
+		return SessionRow{}, err
+	}
+	if err := tx.LockUserSessions(ctx, userID); err != nil {
+		return SessionRow{}, err
+	}
+	return tx.LockSessionByHash(ctx, hash)
+}
+
 func (m *Manager) insert(ctx context.Context, tx Tx, userID, familyID uuid.UUID, meta Meta) (string, Session, error) {
 	raw := make([]byte, refreshTokenBytes)
 	if _, err := rand.Read(raw); err != nil {
```

Aplique em `internal/auth/session_test.go` (o fake do `Tx` precisa dos dois métodos novos):

```diff
--- a/internal/auth/session_test.go
+++ b/internal/auth/session_test.go
@@ -357,6 +357,10 @@
 	}, nil
 }
 
+func (txFalsa) SessionUserByHash(context.Context, []byte) (uuid.UUID, error) {
+	return uuid.New(), nil
+}
+func (txFalsa) LockUserSessions(context.Context, uuid.UUID) error    { return nil }
 func (txFalsa) InsertSession(context.Context, auth.SessionRow) error { return nil }
 func (txFalsa) MarkSessionUsed(context.Context, uuid.UUID, time.Time) error {
 	return nil
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && sqlc diff && go test -race -count=20 -run 'TestRevokingDuringRotation|TestConcurrentRotation' ./internal/auth/ && go test -race -count=1 ./internal/auth/`
Expected: PASS. As vinte rodadas existem porque é teste de ordenação: um verde só não prova nada.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run TestRevokingDuringRotation ./internal/auth/`; restaure:
1. Em `travarSessao`, troque as três linhas do `LockUserSessions` por `_ = userID`. Expected: FAIL nos dois subtestes, `1 linha(s) viva(s)`.
2. Inverta a ordem: `LockSessionByHash` primeiro e `LockUserSessions` depois (`cur, err := tx.LockSessionByHash(ctx, hash); if err != nil { return SessionRow{}, err }; return cur, tx.LockUserSessions(ctx, userID)`). Expected: FAIL, `a revogação falhou: revogar família: ERROR: deadlock detected (SQLSTATE 40P01)`.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/queries/sessions.sql internal/storage/db/ internal/auth/
git commit -m "fix(auth): mutações de sessão serializadas por usuário (corrida de revogação)"
```

---

### Task 2: O access token leva a família da sessão (claim `sid`, D2)

**Files:**
- Modify: `internal/auth/token.go` (`AccessClaims`, `NewAccessToken` com família, `ParseAccessToken` exige `sid`)
- Modify: `internal/httpapi/middleware/authn.go` (`FamilyID(ctx)`)
- Modify: `internal/httpapi/authhandlers.go` (`grantSession` recebe a `auth.Session`)
- Test: `internal/auth/token_test.go`, `internal/httpapi/auth_integration_test.go` (e os helpers que cunham token em `targets_integration_test.go`, `totp_integration_test.go`, `cmd/vigilume/incident_e2e_test.go`)

**Interfaces:**
- Consumes: nada de tarefa anterior.
- Produces:
  - `type AccessClaims struct { UserID, FamilyID uuid.UUID }`.
  - `func NewAccessToken(secret []byte, userID, familyID uuid.UUID, now time.Time) (string, error)` — recusa `familyID == uuid.Nil`.
  - `func ParseAccessToken(secret []byte, raw string, now time.Time) (AccessClaims, error)` — `sid` ausente, não-UUID ou nulo é `ErrInvalidToken`.
  - `func middleware.FamilyID(ctx context.Context) (uuid.UUID, bool)`.
  - Helper de teste `accessDe(t, resp) string` em `auth_integration_test.go`, que as Tasks 5 e 6 usam.

**Contexto para quem implementa.** `GET /sessions` precisa marcar a sessão `current`, e as rotas de credencial (Tasks 5 e 6) precisam recusar um token cuja família já foi encerrada. O cookie de refresh, com `Path=/api/v1/auth`, não chega em `/api/v1/sessions`; o access token chega em toda rota. `sid` é o "Session ID" registrado na IANA pelo OpenID Connect. Ele é obrigatório porque não há token sem `sid` em circulação: o painel ainda não foi publicado, e um token antigo, se houvesse, seria recusado e o painel renovaria. Os testes que cunham token para rotas de recurso passam uma família qualquer (`uuid.New()`): nenhuma dessas rotas confere a família.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/auth/token_test.go`:

```diff
--- a/internal/auth/token_test.go
+++ b/internal/auth/token_test.go
@@ -14,9 +14,9 @@
 
 func TestAccessTokenRoundTrip(t *testing.T) {
 	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
-	userID := uuid.New()
+	userID, familia := uuid.New(), uuid.New()
 
-	raw, err := auth.NewAccessToken(jwtSecret, userID, now)
+	raw, err := auth.NewAccessToken(jwtSecret, userID, familia, now)
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -25,14 +25,55 @@
 	if err != nil {
 		t.Fatal(err)
 	}
-	if got != userID {
-		t.Errorf("userID = %s, queria %s", got, userID)
+	if got.UserID != userID {
+		t.Errorf("userID = %s, queria %s", got.UserID, userID)
+	}
+	if got.FamilyID != familia {
+		t.Errorf("família = %s, queria %s", got.FamilyID, familia)
+	}
+}
+
+// Um token sem sid não diz de qual sessão saiu, e as rotas de credencial não
+// teriam como recusá-lo depois de a sessão ser encerrada (D2 do Plano 5). É
+// assinado com o segredo CERTO: a recusa só pode vir da falta do claim.
+func TestAccessTokenWithoutSessionFamilyIsRejected(t *testing.T) {
+	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
+	registrados := jwt.RegisteredClaims{
+		Subject:   uuid.New().String(),
+		IssuedAt:  jwt.NewNumericDate(now),
+		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
+	}
+	casos := map[string]jwt.Claims{
+		"sem sid": registrados,
+		"sid que não é UUID": jwt.MapClaims{
+			"sub": registrados.Subject, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "sid": "abc",
+		},
+		"sid nulo": jwt.MapClaims{
+			"sub": registrados.Subject, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "sid": uuid.Nil.String(),
+		},
+	}
+	for nome, claims := range casos {
+		t.Run(nome, func(t *testing.T) {
+			raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
+			if err != nil {
+				t.Fatal(err)
+			}
+			if _, err := auth.ParseAccessToken(jwtSecret, raw, now); err == nil {
+				t.Error("token sem família de sessão válida foi aceito")
+			}
+		})
+	}
+}
+
+func TestAccessTokenRefusesNilFamily(t *testing.T) {
+	if _, err := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.Nil, time.Now()); err == nil {
+		t.Error("assinou um token sem família de sessão")
 	}
 }
 
 func TestAccessTokenExpiresIn15Minutes(t *testing.T) {
 	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
-	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)
+	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.New(), now)
 
 	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(14*time.Minute)); err != nil {
 		t.Errorf("aos 14 minutos ainda deveria valer: %v", err)
@@ -44,7 +85,7 @@
 
 func TestAccessTokenRejectsWrongSecret(t *testing.T) {
 	now := time.Now()
-	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)
+	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.New(), now)
 
 	if _, err := auth.ParseAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), raw, now); err == nil {
 		t.Error("token assinado com outro segredo foi aceito")
@@ -143,7 +184,7 @@
 	if err != nil {
 		t.Fatal(err)
 	}
-	acesso, err := auth.NewAccessToken(jwtSecret, userID, now)
+	acesso, err := auth.NewAccessToken(jwtSecret, userID, uuid.New(), now)
 	if err != nil {
 		t.Fatal(err)
 	}
```

Aplique em `internal/httpapi/auth_integration_test.go`:

```diff
--- a/internal/httpapi/auth_integration_test.go
+++ b/internal/httpapi/auth_integration_test.go
@@ -311,6 +311,62 @@
 	}
 }
 
+// accessDe lê o access token de uma resposta de login ou de refresh.
+func accessDe(t *testing.T, resp *http.Response) string {
+	t.Helper()
+	var body struct {
+		AccessToken string `json:"access_token"`
+	}
+	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
+		t.Fatal(err)
+	}
+	if body.AccessToken == "" {
+		t.Fatal("a resposta não trouxe access token")
+	}
+	return body.AccessToken
+}
+
+// O access token leva a família do refresh que saiu junto com ele (claim sid,
+// D2 do Plano 5), no login e em cada refresh. É o que GET /sessions usa para
+// marcar a sessão atual.
+func TestAccessTokenCarriesTheSessionFamily(t *testing.T) {
+	a := newAPI(t)
+	c := clientWithJar(t)
+	ctx := context.Background()
+
+	resp := login(t, a, c, testEmail, testPass)
+	defer resp.Body.Close()
+	if resp.StatusCode != http.StatusOK {
+		t.Fatalf("login devolveu %d", resp.StatusCode)
+	}
+	doLogin, err := auth.ParseAccessToken(testJWTSecret, accessDe(t, resp), a.clk.Now())
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	var familia uuid.UUID
+	if err := a.pool.QueryRow(ctx,
+		`SELECT family_id FROM sessions WHERE user_id = $1`, a.userID).Scan(&familia); err != nil {
+		t.Fatal(err)
+	}
+	if doLogin.FamilyID != familia {
+		t.Errorf("sid do login = %s, queria a família gravada %s", doLogin.FamilyID, familia)
+	}
+
+	r2 := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
+	defer r2.Body.Close()
+	if r2.StatusCode != http.StatusOK {
+		t.Fatalf("refresh devolveu %d", r2.StatusCode)
+	}
+	doRefresh, err := auth.ParseAccessToken(testJWTSecret, accessDe(t, r2), a.clk.Now())
+	if err != nil {
+		t.Fatal(err)
+	}
+	if doRefresh.FamilyID != familia {
+		t.Errorf("sid do refresh = %s, queria a mesma família %s", doRefresh.FamilyID, familia)
+	}
+}
+
 // E-mail inexistente e senha errada precisam ser indistinguíveis na resposta,
 // senão o endpoint vira oráculo de quais contas existem (spec §5.2).
 //
@@ -633,7 +689,7 @@
 	}
 
 	// Token com assinatura de outro segredo também não entra.
-	outro, err := auth.NewAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), a.userID, a.clk.Now())
+	outro, err := auth.NewAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), a.userID, uuid.New(), a.clk.Now())
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -643,7 +699,7 @@
 
 	// E o token legítimo passa, o que descarta que o 401 venha de qualquer
 	// outra camada da cadeia.
-	bom, err := auth.NewAccessToken(testJWTSecret, a.userID, a.clk.Now())
+	bom, err := auth.NewAccessToken(testJWTSecret, a.userID, uuid.New(), a.clk.Now())
 	if err != nil {
 		t.Fatal(err)
 	}
```

Aplique em `internal/httpapi/targets_integration_test.go`:

```diff
--- a/internal/httpapi/targets_integration_test.go
+++ b/internal/httpapi/targets_integration_test.go
@@ -21,7 +21,7 @@
 
 func tokenDe(t *testing.T, a *api, userID uuid.UUID) string {
 	t.Helper()
-	tok, err := auth.NewAccessToken(testJWTSecret, userID, a.clk.Now())
+	tok, err := auth.NewAccessToken(testJWTSecret, userID, uuid.New(), a.clk.Now())
 	if err != nil {
 		t.Fatal(err)
 	}
```

Aplique em `internal/httpapi/totp_integration_test.go`:

```diff
--- a/internal/httpapi/totp_integration_test.go
+++ b/internal/httpapi/totp_integration_test.go
@@ -7,6 +7,7 @@
 	"testing"
 	"time"
 
+	"github.com/google/uuid"
 	"github.com/pquerna/otp"
 	"github.com/pquerna/otp/totp"
 
@@ -17,7 +18,7 @@
 // o mesmo segredo que a API usa.
 func accessTokenValido(t *testing.T, a *api) string {
 	t.Helper()
-	tok, err := auth.NewAccessToken(testJWTSecret, a.userID, a.clk.Now())
+	tok, err := auth.NewAccessToken(testJWTSecret, a.userID, uuid.New(), a.clk.Now())
 	if err != nil {
 		t.Fatal(err)
 	}
```

Aplique em `cmd/vigilume/incident_e2e_test.go`:

```diff
--- a/cmd/vigilume/incident_e2e_test.go
+++ b/cmd/vigilume/incident_e2e_test.go
@@ -223,7 +223,7 @@
 	fim := make(chan error, 1)
 	go func() { fim <- run(runCtx, cfg) }()
 
-	tok, err := auth.NewAccessToken(chave, dono, time.Now())
+	tok, err := auth.NewAccessToken(chave, dono, uuid.New(), time.Now())
 	if err != nil {
 		t.Fatal(err)
 	}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/auth/ ./internal/httpapi/ ./cmd/vigilume/`
Expected: FAIL de compilação nos três pacotes (`too many arguments in call to auth.NewAccessToken`; `got.UserID undefined`).

- [ ] **Step 3: Implementar**

Aplique em `internal/auth/token.go`:

```diff
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -15,11 +15,35 @@
 
 var ErrInvalidToken = errors.New("token de acesso inválido")
 
-func NewAccessToken(secret []byte, userID uuid.UUID, now time.Time) (string, error) {
-	claims := jwt.RegisteredClaims{
-		Subject:   userID.String(),
-		IssuedAt:  jwt.NewNumericDate(now),
-		ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
+// AccessClaims é o que um access token válido diz: quem é o usuário e de qual
+// família de refresh (sessão) ele saiu.
+type AccessClaims struct {
+	UserID uuid.UUID
+	// FamilyID é o claim sid (o "Session ID" registrado na IANA pelo OpenID
+	// Connect). É o que permite a GET /sessions marcar a sessão atual e às
+	// rotas de credencial recusar um token cuja sessão já foi encerrada (D2 do
+	// Plano 5): o cookie de refresh, com Path=/api/v1/auth, não chega lá.
+	FamilyID uuid.UUID
+}
+
+type accessJWT struct {
+	jwt.RegisteredClaims
+	SID string `json:"sid"`
+}
+
+// NewAccessToken recusa família nula: todo access token sai de uma sessão, e um
+// token sem sid seria recusado por ParseAccessToken logo adiante.
+func NewAccessToken(secret []byte, userID, familyID uuid.UUID, now time.Time) (string, error) {
+	if familyID == uuid.Nil {
+		return "", errors.New("assinar token: família de sessão nula")
+	}
+	claims := accessJWT{
+		RegisteredClaims: jwt.RegisteredClaims{
+			Subject:   userID.String(),
+			IssuedAt:  jwt.NewNumericDate(now),
+			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
+		},
+		SID: familyID.String(),
 	}
 	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
 	if err != nil {
@@ -28,8 +52,8 @@
 	return tok, nil
 }
 
-func ParseAccessToken(secret []byte, raw string, now time.Time) (uuid.UUID, error) {
-	var claims jwt.RegisteredClaims
+func ParseAccessToken(secret []byte, raw string, now time.Time) (AccessClaims, error) {
+	var claims accessJWT
 
 	_, err := jwt.ParseWithClaims(raw, &claims,
 		func(t *jwt.Token) (any, error) { return secret, nil },
@@ -45,21 +69,28 @@
 		jwt.WithTimeFunc(func() time.Time { return now }),
 	)
 	if err != nil {
-		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
+		return AccessClaims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
 	}
 	// Access token não carrega audience; se veio uma, é um token de outro
 	// propósito — em especial o desafio de TOTP, que é entregue depois da
 	// senha e ANTES do segundo fator. Sem esta checagem ele valeria como
 	// credencial completa e o segundo fator viraria decoração.
 	if len(claims.Audience) > 0 {
-		return uuid.Nil, fmt.Errorf("%w: audience inesperada", ErrInvalidToken)
+		return AccessClaims{}, fmt.Errorf("%w: audience inesperada", ErrInvalidToken)
 	}
 
 	id, err := uuid.Parse(claims.Subject)
 	if err != nil {
-		return uuid.Nil, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
+		return AccessClaims{}, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
+	}
+	// Sem sid, o token não diz de qual sessão veio, e as rotas de credencial
+	// não teriam como recusá-lo depois de a sessão ser encerrada. Não há token
+	// assim em circulação: o painel ainda não foi publicado (D2 do Plano 5).
+	familia, err := uuid.Parse(claims.SID)
+	if err != nil || familia == uuid.Nil {
+		return AccessClaims{}, fmt.Errorf("%w: sid ausente ou inválido", ErrInvalidToken)
 	}
-	return id, nil
+	return AccessClaims{UserID: id, FamilyID: familia}, nil
 }
 
 // challengeAudience separa o token de desafio do access token. Sem essa
```

Aplique em `internal/httpapi/middleware/authn.go`:

```diff
--- a/internal/httpapi/middleware/authn.go
+++ b/internal/httpapi/middleware/authn.go
@@ -14,6 +14,8 @@
 
 type userKey struct{}
 
+type familyKey struct{}
+
 // UserID recupera o usuário autenticado. O segundo retorno é false quando a
 // rota não passou por Authn — um handler protegido deve tratar isso como 401,
 // nunca assumir presença.
@@ -22,6 +24,13 @@
 	return id, ok
 }
 
+// FamilyID recupera a família de sessão do access token (o claim sid). Mesmo
+// contrato de UserID: false quando a rota não passou por Authn.
+func FamilyID(ctx context.Context) (uuid.UUID, bool) {
+	id, ok := ctx.Value(familyKey{}).(uuid.UUID)
+	return id, ok
+}
+
 // Authn exige um access token válido no header Authorization.
 //
 // O envelope vem de apierr, e não de httpapi, porque httpapi importa este
@@ -35,13 +44,14 @@
 				return
 			}
 
-			userID, err := auth.ParseAccessToken(jwtSecret, raw, clk.Now())
+			claims, err := auth.ParseAccessToken(jwtSecret, raw, clk.Now())
 			if err != nil {
 				apierr.WriteError(w, r, apierr.CodeUnauthorized)
 				return
 			}
 
-			ctx := context.WithValue(r.Context(), userKey{}, userID)
+			ctx := context.WithValue(r.Context(), userKey{}, claims.UserID)
+			ctx = context.WithValue(ctx, familyKey{}, claims.FamilyID)
 			next.ServeHTTP(w, r.WithContext(ctx))
 		})
 	}
```

Aplique em `internal/httpapi/authhandlers.go`:

```diff
--- a/internal/httpapi/authhandlers.go
+++ b/internal/httpapi/authhandlers.go
@@ -175,7 +175,7 @@
 		return
 	}
 
-	h.grantSession(w, r, token, s.ExpiresAt, s.UserID)
+	h.grantSession(w, r, token, s)
 }
 
 func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
@@ -203,7 +203,7 @@
 		h.internal(w, r, "emitir sessão", err)
 		return
 	}
-	h.grantSession(w, r, token, s.ExpiresAt, userID)
+	h.grantSession(w, r, token, s)
 }
 
 // grantSession entrega a sessão ao cliente, e a ORDEM aqui é o ponto (item
@@ -218,13 +218,16 @@
 // a sessão no banco. O que ela tira é o cookie do lado do cliente, que é a
 // metade explorável: sessão órfã no banco expira sozinha e ninguém a
 // apresenta; cookie vivo no navegador é credencial em uso.
-func (h *AuthHandlers) grantSession(w http.ResponseWriter, r *http.Request, refresh string, expires time.Time, userID uuid.UUID) {
-	access, err := auth.NewAccessToken(h.jwtSecret, userID, h.clk.Now())
+//
+// O access token leva a família da sessão (claim sid): é a mesma família do
+// refresh que vai no cookie.
+func (h *AuthHandlers) grantSession(w http.ResponseWriter, r *http.Request, refresh string, s auth.Session) {
+	access, err := auth.NewAccessToken(h.jwtSecret, s.UserID, s.FamilyID, h.clk.Now())
 	if err != nil {
 		h.internal(w, r, "assinar access token", err)
 		return
 	}
-	h.setCookie(w, refresh, expires)
+	h.setCookie(w, refresh, s.ExpiresAt)
 	apierr.WriteJSON(w, http.StatusOK, tokenResponse{
 		AccessToken: access,
 		ExpiresIn:   int(auth.AccessTTL.Seconds()),
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ cmd/ && go vet ./... && go test -race -count=1 ./internal/auth/ ./internal/httpapi/... ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure:
1. Em `ParseAccessToken`, troque o bloco do `sid` por `familia, _ := uuid.Parse(claims.SID)`. Run: `go test -count=1 -run TestAccessToken ./internal/auth/`. Expected: FAIL em `TestAccessTokenWithoutSessionFamilyIsRejected`, nos três subtestes.
2. Em `grantSession`, troque `s.FamilyID` por `uuid.New()`. Run: `go test -count=1 -run TestAccessTokenCarries ./internal/httpapi/`. Expected: FAIL, `sid do login = …, queria a família gravada …` e o mesmo no refresh.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/ internal/httpapi/ cmd/vigilume/incident_e2e_test.go
git commit -m "feat(auth): access token leva a família da sessão (claim sid)"
```

---

### Task 3: Política de senha nova e uma verificação de senha só (D3, D4, D24)

**Files:**
- Modify: `internal/auth/password.go` (`MinPasswordLen`, `MaxPasswordLen`, `ReasonTooShort`, `ReasonTooLong`, `ValidateNewPassword`)
- Modify: `internal/auth/user.go` (`NormalizeEmail`, `CheckPassword`)
- Modify: `internal/httpapi/authhandlers.go` (o `Login` usa os dois; `normalizarEmail` sai)
- Modify: `internal/httpapi/totphandlers.go` (`senhaConfere` usa `CheckPassword`; o `disable` responde `invalid_credentials` e conta a falha do código)
- Test: `internal/auth/password_test.go`, `internal/auth/user_test.go`, `internal/httpapi/totp_integration_test.go`

**Interfaces:**
- Consumes: nada de tarefa anterior.
- Produces:
  - `const MinPasswordLen = 15`, `MaxPasswordLen = 128`; `const ReasonTooShort = "too_short"`, `ReasonTooLong = "too_long"`.
  - `func ValidateNewPassword(plain string) string` — `""` quando serve, senão o motivo.
  - `func NormalizeEmail(email string) string` — só `strings.TrimSpace`.
  - `func CheckPassword(ctx context.Context, users UserStore, u User, plain string, now time.Time) (ok, needsRehash bool, err error)` — bloqueada: `VerifyDummy` e `false`, sem contar; errada: `RecordFailure` e `false`; certa: `true`, sem zerar o contador.
  - Helper de teste `falhasDeLogin(t, a) int` em `totp_integration_test.go`, que a Task 5 usa.

**Contexto para quem implementa.** A política é a da NIST 800-63B-4 §3.1.1.2 para senha que pode ser fator único (o 2FA é opcional): 15 no mínimo, contando **code points** (`utf8.RuneCountInString`), sem regra de composição, sem aparar e sem normalizar. Vale para senha **nova**; o login continua aceitando uma senha antiga mais curta. Não há blocklist (decisão do usuário, reconciliação do contrato).

`CheckPassword` é o miolo do login, extraído para que toda rota que pede a senha de novo faça exatamente o mesmo: hoje `senhaConfere` (cadastro e desativação do 2FA) só comparava a senha, e um access token roubado testava senhas ali sem nunca travar a conta. O sucesso não zera o contador: com 2FA, o login só termina no `/totp/verify` (a regra que o `Login` já comenta).

O `TOTPDisable` passa a responder `401 invalid_credentials`, e não `unauthorized`, para código ausente ou errado (D24, apontado pelo Plano 6): o painel renova a sessão em `unauthorized`, e um erro de digitação viraria renovação e logout. O código errado conta no mesmo bloqueio do login, como no `/totp/verify` (R40). O teste novo tem a perna do token inválido, que continua `unauthorized`: é ela que prova que o código não é o mesmo para tudo.

O limitador do login **não** vai para as rotas de TOTP (desvio da spec §6, D5): o `TestDisableRequiresPasswordAndPossession` existente faz sete requisições legítimas seguidas e levaria `429`; o bloqueio por conta já limita as tentativas.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/auth/password_test.go`:

```diff
--- a/internal/auth/password_test.go
+++ b/internal/auth/password_test.go
@@ -144,3 +144,32 @@
 		})
 	}
 }
+
+// A política conta code points, não bytes (NIST 800-63B-4: cada code point é
+// um caractere). As bordas multibyte são as que uma contagem por len() erra:
+// 128 claves de sol têm 512 bytes e ainda cabem.
+func TestValidateNewPasswordCountsCodePoints(t *testing.T) {
+	casos := []struct {
+		nome   string
+		senha  string
+		motivo string
+	}{
+		{"vazia", "", auth.ReasonTooShort},
+		{"14 caracteres", strings.Repeat("a", 14), auth.ReasonTooShort},
+		{"15 caracteres", strings.Repeat("a", 15), ""},
+		{"15 espaços", strings.Repeat(" ", 15), ""},
+		{"128 caracteres", strings.Repeat("a", 128), ""},
+		{"129 caracteres", strings.Repeat("a", 129), auth.ReasonTooLong},
+		{"14 de dois bytes", strings.Repeat("é", 14), auth.ReasonTooShort},
+		{"15 de quatro bytes", strings.Repeat("𝄞", 15), ""},
+		{"128 de quatro bytes", strings.Repeat("𝄞", 128), ""},
+		{"129 de quatro bytes", strings.Repeat("𝄞", 129), auth.ReasonTooLong},
+	}
+	for _, c := range casos {
+		t.Run(c.nome, func(t *testing.T) {
+			if got := auth.ValidateNewPassword(c.senha); got != c.motivo {
+				t.Errorf("ValidateNewPassword = %q, queria %q", got, c.motivo)
+			}
+		})
+	}
+}
```

Aplique em `internal/auth/user_test.go`:

```diff
--- a/internal/auth/user_test.go
+++ b/internal/auth/user_test.go
@@ -215,3 +215,61 @@
 		t.Errorf("failed_attempts = %d, queria 3: a contagem parou de somar", tentativas)
 	}
 }
+
+// Só o espaço em volta sai; a caixa fica (o citext compara sem ela).
+func TestNormalizeEmail(t *testing.T) {
+	if got := auth.NormalizeEmail("  Pessoa@Exemplo.com \t"); got != "Pessoa@Exemplo.com" {
+		t.Errorf("NormalizeEmail = %q", got)
+	}
+}
+
+// CheckPassword é a verificação do login para toda rota que pede a senha. Os
+// três caminhos: senha certa não mexe no contador (quem chama decide quando
+// zerar); senha errada conta uma falha; conta bloqueada recusa até a senha
+// certa, sem contar falha nova.
+func TestCheckPassword(t *testing.T) {
+	_, pool, clk, userID := setup(t)
+	store := auth.NewPGStore(pool)
+	ctx := context.Background()
+
+	hash, err := auth.HashPassword(senha)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := store.UpdatePasswordHash(ctx, userID, hash); err != nil {
+		t.Fatal(err)
+	}
+	if err := store.RecordFailure(ctx, userID, clk.Now()); err != nil {
+		t.Fatal(err)
+	}
+	u, err := store.ByID(ctx, userID)
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	ok, _, err := auth.CheckPassword(ctx, store, u, senha, clk.Now())
+	if err != nil || !ok {
+		t.Fatalf("senha certa: ok = %v, erro = %v", ok, err)
+	}
+	if n, _ := lerTravas(t, pool, userID); n != 1 {
+		t.Errorf("senha certa mexeu no contador: %d falhas, queria 1", n)
+	}
+
+	ok, _, err = auth.CheckPassword(ctx, store, u, "senha-errada-com-mais-de-15", clk.Now())
+	if err != nil || ok {
+		t.Fatalf("senha errada: ok = %v, erro = %v", ok, err)
+	}
+	if n, _ := lerTravas(t, pool, userID); n != 2 {
+		t.Errorf("senha errada não contou: %d falhas, queria 2", n)
+	}
+
+	ate := clk.Now().Add(time.Minute)
+	u.LockedUntil = &ate
+	ok, _, err = auth.CheckPassword(ctx, store, u, senha, clk.Now())
+	if err != nil || ok {
+		t.Fatalf("conta bloqueada aceitou a senha certa: ok = %v, erro = %v", ok, err)
+	}
+	if n, _ := lerTravas(t, pool, userID); n != 2 {
+		t.Errorf("conta bloqueada contou falha: %d, queria 2", n)
+	}
+}
```

Aplique em `internal/httpapi/totp_integration_test.go`:

```diff
--- a/internal/httpapi/totp_integration_test.go
+++ b/internal/httpapi/totp_integration_test.go
@@ -4,6 +4,7 @@
 	"context"
 	"encoding/json"
 	"net/http"
+	"strconv"
 	"testing"
 	"time"
 
@@ -608,6 +609,113 @@
 	}
 }
 
+// falhasDeLogin lê o contador do bloqueio progressivo.
+func falhasDeLogin(t *testing.T, a *api) int {
+	t.Helper()
+	var n int
+	if err := a.pool.QueryRow(context.Background(),
+		`SELECT failed_attempts FROM users WHERE id = $1`, a.userID).Scan(&n); err != nil {
+		t.Fatal(err)
+	}
+	return n
+}
+
+// Um access token roubado não pode virar oráculo de senha pelo cadastro do
+// segundo fator: cada senha errada ali conta no mesmo bloqueio do login (D4 do
+// Plano 5), e a conta trava no limiar.
+func TestEnrollWrongPasswordCountsTowardTheLock(t *testing.T) {
+	a := newAPI(t)
+	roubado := accessTokenValido(t, a)
+
+	for i := range auth.LockThreshold {
+		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", roubado,
+			map[string]string{"password": "chute-numero-" + strconv.Itoa(i)})
+		resp.Body.Close()
+		if resp.StatusCode != http.StatusUnauthorized {
+			t.Fatalf("chute %d devolveu %d, queria 401", i, resp.StatusCode)
+		}
+	}
+	if n := falhasDeLogin(t, a); n != auth.LockThreshold {
+		t.Errorf("%d falhas contadas, queria %d", n, auth.LockThreshold)
+	}
+
+	// A prova que importa: travada, a conta recusa até a senha certa no login.
+	entrada := login(t, a, clientWithJar(t), testEmail, testPass)
+	defer entrada.Body.Close()
+	if entrada.StatusCode != http.StatusUnauthorized {
+		t.Errorf("login com a senha certa depois dos chutes devolveu %d, queria 401", entrada.StatusCode)
+	}
+}
+
+// Conta bloqueada não cadastra nem desliga o segundo fator, nem com a senha
+// certa: é o mesmo bloqueio do login.
+func TestSecondFactorRoutesRefuseALockedAccount(t *testing.T) {
+	a := newAPI(t)
+	c := clientWithJar(t)
+	segredo := cadastrarTOTP(t, a, c)
+	token := accessTokenValido(t, a)
+	bloquearConta(t, a)
+
+	code, err := totp.GenerateCode(segredo, a.clk.Now())
+	if err != nil {
+		t.Fatal(err)
+	}
+	if got := desativar(t, a, token, map[string]string{"password": testPass, "code": code}); got != http.StatusUnauthorized {
+		t.Errorf("desativar com a conta bloqueada devolveu %d, queria 401", got)
+	}
+	if confirmado, _, _, _ := estadoDoSegundoFator(t, a); !confirmado {
+		t.Error("a conta bloqueada desligou o segundo fator")
+	}
+}
+
+// Senha errada, código ausente e código errado nas rotas do segundo fator são
+// 401 invalid_credentials, e nunca unauthorized (D24 do Plano 5, apontado pelo
+// Plano 6): o painel renova a sessão em unauthorized, e um código digitado
+// errado viraria renovação e logout. O token recusado continua unauthorized:
+// é essa perna que prova que o código não é o mesmo para tudo.
+func TestSecondFactorRoutesAnswerInvalidCredentials(t *testing.T) {
+	a := newAPI(t)
+	c := clientWithJar(t)
+	segredo := cadastrarTOTP(t, a, c)
+	token := accessTokenValido(t, a)
+	code, err := totp.GenerateCode(segredo, a.clk.Now())
+	if err != nil {
+		t.Fatal(err)
+	}
+	errado := "000000"
+	if errado == code {
+		errado = "111111"
+	}
+
+	casos := []struct {
+		nome, rota, token string
+		corpo             map[string]string
+		codigo            string
+	}{
+		{"enroll com senha errada", "enroll", token, map[string]string{"password": "senha-que-nao-e-a-dele"}, "invalid_credentials"},
+		{"disable com senha errada", "disable", token, map[string]string{"password": "senha-que-nao-e-a-dele", "code": code}, "invalid_credentials"},
+		{"disable sem código", "disable", token, map[string]string{"password": testPass}, "invalid_credentials"},
+		{"disable com código errado", "disable", token, map[string]string{"password": testPass, "code": errado}, "invalid_credentials"},
+		{"disable com token inválido", "disable", "token-que-nao-vale", map[string]string{"password": testPass, "code": code}, "unauthorized"},
+	}
+	for _, caso := range casos {
+		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/"+caso.rota, caso.token, caso.corpo)
+		got := decodeEnvelope(t, resp).Error.Code
+		resp.Body.Close()
+		if resp.StatusCode != http.StatusUnauthorized || got != caso.codigo {
+			t.Errorf("%s: %d %s, queria 401 %s", caso.nome, resp.StatusCode, got, caso.codigo)
+		}
+	}
+	// Senha errada duas vezes, código ausente e código errado: quatro falhas no
+	// mesmo bloqueio do login.
+	if n := falhasDeLogin(t, a); n != 4 {
+		t.Errorf("%d falhas contadas, queria 4", n)
+	}
+	if confirmado, _, _, _ := estadoDoSegundoFator(t, a); !confirmado {
+		t.Error("uma recusa desligou o segundo fator")
+	}
+}
+
 // TestStolenAccessTokenCannotEnableTheSecondFactor percorre o sequestro
 // inteiro: tudo que o atacante tem é um access token válido da vítima.
 func TestStolenAccessTokenCannotEnableTheSecondFactor(t *testing.T) {
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/auth/ ./internal/httpapi/`
Expected: `internal/auth` FAIL de compilação (`auth.ValidateNewPassword` indefinido). `internal/httpapi` FAIL em `TestEnrollWrongPasswordCountsTowardTheLock` (`0 falhas contadas, queria 5`; login com a senha certa `200`), `TestSecondFactorRoutesRefuseALockedAccount` (`204`) e `TestSecondFactorRoutesAnswerInvalidCredentials` (`disable sem código: 401 unauthorized`).

- [ ] **Step 3: Implementar**

Aplique em `internal/auth/password.go`:

```diff
--- a/internal/auth/password.go
+++ b/internal/auth/password.go
@@ -9,6 +9,7 @@
 	"fmt"
 	"strings"
 	"sync"
+	"unicode/utf8"
 
 	"golang.org/x/crypto/argon2"
 )
@@ -24,6 +25,39 @@
 
 var ErrInvalidHash = errors.New("hash de senha malformado")
 
+// Política de senha NOVA (D3 do Plano 5), da NIST 800-63B-4 §3.1.1.2: 15
+// caracteres no mínimo, porque o segundo fator é opcional e a senha pode ser o
+// único fator; aceitar ao menos 64; nenhuma regra de composição. O máximo de
+// 128 dá folga a gerenciadores de senha e mantém o pior caso em 512 bytes.
+//
+// Vale para senha nova (vigilume user create, set-password, POST
+// /auth/password). O login continua aceitando uma senha antiga mais curta.
+const (
+	MinPasswordLen = 15
+	MaxPasswordLen = 128
+)
+
+// Motivos de recusa, no vocabulário fechado dos fields de erro da API.
+const (
+	ReasonTooShort = "too_short"
+	ReasonTooLong  = "too_long"
+)
+
+// ValidateNewPassword devolve "" quando a senha serve, ou o motivo da recusa.
+//
+// Conta code points, e não bytes (NIST: cada code point é um caractere): "é"
+// tem dois bytes e é um caractere. Não normaliza nem apara: espaço é caractere
+// como outro qualquer, e a senha é verificada inteira, como foi digitada.
+func ValidateNewPassword(plain string) string {
+	switch n := utf8.RuneCountInString(plain); {
+	case n < MinPasswordLen:
+		return ReasonTooShort
+	case n > MaxPasswordLen:
+		return ReasonTooLong
+	}
+	return ""
+}
+
 // HashPassword gera um hash no formato PHC, com salt aleatório.
 func HashPassword(plain string) (string, error) {
 	salt := make([]byte, argonSaltLen)
```

Aplique em `internal/auth/user.go`:

```diff
--- a/internal/auth/user.go
+++ b/internal/auth/user.go
@@ -4,6 +4,8 @@
 	"context"
 	"errors"
 	"fmt"
+	"log/slog"
+	"strings"
 	"time"
 
 	"github.com/google/uuid"
@@ -31,6 +33,51 @@
 	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
 }
 
+// NormalizeEmail tira apenas o espaço em volta. O caso real é o autocompletar
+// do celular, que costuma acrescentar um espaço à direita: sem isto,
+// " pessoa@exemplo.com " devolve 401 para a credencial certa.
+//
+// É a MESMA função no login e no cadastro pelo CLI, de propósito: duas
+// normalizações diferentes criam o e-mail que cadastra e nunca mais entra.
+//
+// NÃO aplica ToLower. A coluna é citext, então a caixa já é comparada sem
+// distinção no banco; baixar a caixa aqui mascararia a ausência do citext caso
+// uma migration futura trocasse o tipo da coluna.
+func NormalizeEmail(email string) string {
+	return strings.TrimSpace(email)
+}
+
+// CheckPassword é a verificação de senha do login, e de toda rota que pede a
+// senha de novo (troca de senha, cadastro e desativação do segundo fator; D4
+// do Plano 5). Custa um Argon2id em todo caminho.
+//
+// Conta bloqueada é recusada sem olhar a senha, pagando VerifyDummy: o
+// bloqueio não se anuncia (R34), nem pela resposta nem pelo tempo. Senha
+// errada conta no bloqueio progressivo: sem isto, uma rota que pede a senha
+// vira oráculo de senha limitado só por IP para quem tem um access token
+// roubado. O sucesso NÃO zera o contador; quem chama decide quando o
+// processo de autenticação terminou (com segundo fator, é depois dele).
+//
+// needsRehash só vale quando ok.
+func CheckPassword(ctx context.Context, users UserStore, u User, plain string, now time.Time) (ok, needsRehash bool, err error) {
+	if u.IsLocked(now) {
+		VerifyDummy()
+		return false, false, nil
+	}
+	ok, needsRehash, err = VerifyPassword(u.PasswordHash, plain)
+	if err != nil {
+		return false, false, err
+	}
+	if !ok {
+		// Falhar ao registrar não muda a resposta: a senha estava errada.
+		if err := users.RecordFailure(ctx, u.ID, now); err != nil {
+			slog.Error("registrar falha de senha", "erro", err)
+		}
+		return false, false, nil
+	}
+	return true, needsRehash, nil
+}
+
 type UserStore interface {
 	ByEmail(ctx context.Context, email string) (User, error)
 	ByID(ctx context.Context, userID uuid.UUID) (User, error)
```

Aplique em `internal/httpapi/authhandlers.go`:

```diff
--- a/internal/httpapi/authhandlers.go
+++ b/internal/httpapi/authhandlers.go
@@ -7,7 +7,6 @@
 	"log/slog"
 	"net/http"
 	"net/netip"
-	"strings"
 	"time"
 
 	"github.com/google/uuid"
@@ -62,7 +61,7 @@
 	}
 	now := h.clk.Now()
 
-	user, err := h.users.ByEmail(r.Context(), normalizarEmail(req.Email))
+	user, err := h.users.ByEmail(r.Context(), auth.NormalizeEmail(req.Email))
 	if errors.Is(err, auth.ErrNotFound) {
 		auth.VerifyDummy()
 		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
@@ -73,8 +72,9 @@
 		return
 	}
 
-	// O bloqueio não se anuncia (R34, spec §5.2): a resposta é o mesmo 401
-	// genérico de senha errada e de e-mail inexistente.
+	// CheckPassword recusa a conta bloqueada sem olhar a senha. O bloqueio
+	// não se anuncia (R34, spec §5.2): a resposta é o mesmo 401 genérico de
+	// senha errada e de e-mail inexistente.
 	//
 	// Uma mensagem distinta aqui reintroduziria exatamente a enumeração que o
 	// VerifyDummy existe para impedir, e por um canal muito mais barato que o
@@ -86,22 +86,12 @@
 	// A spec escolheu esse lado: o bloqueio dura 15 minutos e o evento fica no
 	// audit log, que é onde essa informação pertence. Não "melhore a UX" aqui
 	// sem reabrir o buraco.
-	if user.IsLocked(now) {
-		// O custo de CPU continua igualado: esta também é uma falha de login.
-		auth.VerifyDummy()
-		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
-		return
-	}
-
-	ok, needsRehash, err := auth.VerifyPassword(user.PasswordHash, req.Password)
+	ok, needsRehash, err := auth.CheckPassword(r.Context(), h.users, user, req.Password, now)
 	if err != nil {
 		h.internal(w, r, "verificar senha", err)
 		return
 	}
 	if !ok {
-		if err := h.users.RecordFailure(r.Context(), user.ID, now); err != nil {
-			slog.Error("registrar falha de login", "erro", err)
-		}
 		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
 		return
 	}
@@ -323,22 +313,3 @@
 	}
 	return true
 }
-
-// normalizarEmail tira apenas o espaço em volta. O caso real é o
-// autocompletar do celular, que costuma acrescentar um espaço à direita:
-// sem isto, " pessoa@exemplo.com " devolve 401 para a credencial certa.
-//
-// NÃO aplica ToLower de propósito. A coluna é citext, então a caixa já é
-// comparada sem distinção no banco; baixar a caixa aqui seria redundante e,
-// por rodar só no login, mascararia a ausência do citext caso uma migration
-// futura trocasse o tipo da coluna — o login continuaria funcionando e o
-// cadastro passaria a gravar caixa mista sem que nenhum teste reclamasse.
-//
-// PENDENTE: esta função precisa migrar para o pacote auth assim que a rota
-// de cadastro existir, e o cadastro tem de usar EXATAMENTE a mesma
-// normalização. Duas normalizações diferentes entre cadastro e login criam
-// uma discrepância pior que o problema original: o usuário cadastra um
-// e-mail que nunca mais consegue usar para entrar.
-func normalizarEmail(email string) string {
-	return strings.TrimSpace(email)
-}
```

Aplique em `internal/httpapi/totphandlers.go`:

```diff
--- a/internal/httpapi/totphandlers.go
+++ b/internal/httpapi/totphandlers.go
@@ -169,8 +169,13 @@
 // é o mesmo CodeInvalidCredentials do login, pelo motivo do R34: um código
 // distinto aqui contaria ao atacante que a sessão que ele roubou é válida e
 // que só falta a senha.
+//
+// É a verificação do login (auth.CheckPassword, D4 do Plano 5): conta
+// bloqueada é recusada e senha errada conta no bloqueio progressivo. Antes,
+// esta função só comparava a senha, e um access token roubado testava senhas
+// aqui sem nunca travar a conta.
 func (h *AuthHandlers) senhaConfere(w http.ResponseWriter, r *http.Request, user auth.User, senha string) bool {
-	ok, _, err := auth.VerifyPassword(user.PasswordHash, senha)
+	ok, _, err := auth.CheckPassword(r.Context(), h.users, user, senha, h.clk.Now())
 	if err != nil {
 		h.internal(w, r, "verificar senha", err)
 		return false
@@ -189,6 +194,12 @@
 // que uma sessão roubada bastava. O código de recuperação vale no lugar do
 // código do app, senão quem perde o telefone fica sem saída, que foi
 // justamente a lacuna que o R38 deixou.
+//
+// Prova errada ou ausente é 401 invalid_credentials, e NUNCA unauthorized
+// (D24 do Plano 5, apontado pelo Plano 6): unauthorized quer dizer "token
+// recusado", e o painel responde a ele renovando a sessão; um código digitado
+// errado viraria renovação, e depois logout. O código errado conta no mesmo
+// bloqueio do login, como no /totp/verify (R40).
 func (h *AuthHandlers) TOTPDisable(w http.ResponseWriter, r *http.Request) {
 	userID, ok := middleware.UserID(r.Context())
 	if !ok {
@@ -218,16 +229,23 @@
 	if prova == "" {
 		prova = req.RecoveryCode
 	}
+	now := h.clk.Now()
 	if prova == "" {
 		// Sem a segunda prova o pedido não chega ao serviço: a senha sozinha
-		// nunca desliga o fator.
-		apierr.WriteError(w, r, apierr.CodeUnauthorized)
+		// nunca desliga o fator. Ausente conta como errada.
+		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
+			slog.Error("registrar falha de segundo fator", "erro", err)
+		}
+		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
 		return
 	}
 
-	err = h.totp.Disable(r.Context(), userID, prova, h.clk.Now())
+	err = h.totp.Disable(r.Context(), userID, prova, now)
 	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
-		apierr.WriteError(w, r, apierr.CodeUnauthorized)
+		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
+			slog.Error("registrar falha de segundo fator", "erro", err)
+		}
+		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
 		return
 	}
 	if err != nil {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/auth/ ./internal/httpapi/`
Expected: PASS. A suíte do login (R34, R40, `TestLoginSpendsTheSameCPUWhetherOrNotTheAccountExists`) passa sem edição: é o árbitro de que a extração não mudou o login.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure:
1. Em `ValidateNewPassword`, troque `utf8.RuneCountInString(plain)` por `len(plain) + utf8.RuneCountInString("")` (o segundo termo só mantém o import usado; `len(plain)` puro não compila). Run: `go test -count=1 -run TestValidateNewPassword ./internal/auth/`. Expected: FAIL em `14 de dois bytes` e `128 de quatro bytes`.
2. Em `senhaConfere`, troque a chamada por `auth.VerifyPassword(user.PasswordHash, senha)`. Run: `go test -count=1 -run 'TestEnrollWrong|TestSecondFactorRoutes' ./internal/httpapi/`. Expected: FAIL (`0 falhas contadas`; login `200`; desativação `204`).
3. Em `CheckPassword`, troque `if u.IsLocked(now) {` por `if false && u.IsLocked(now) {`. Run: `go test -count=1 -run TestCheckPassword ./internal/auth/`. Expected: FAIL, `conta bloqueada aceitou a senha certa`.
4. Em `CheckPassword`, troque `users.RecordFailure(ctx, u.ID, now)` por `error(nil)` no `if`. Mesmo comando. Expected: FAIL, `senha errada não contou`.
5. No `TOTPDisable`, volte o código ausente para `apierr.CodeUnauthorized`; depois, o código errado. Run: `go test -count=1 -run TestSecondFactorRoutesAnswerInvalidCredentials ./internal/httpapi/`. Expected: FAIL, `disable sem código: 401 unauthorized` (e `disable com código errado: …` na segunda).
6. No `TOTPDisable`, troque o `RecordFailure` do código errado por `error(nil)`. Mesmo comando. Expected: FAIL, `3 falhas contadas, queria 4`.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/ internal/httpapi/
git commit -m "feat(auth): política de senha nova e verificação de senha única (CheckPassword)"
```

---

### Task 4: `vigilume user create` e `vigilume user set-password` (D6–D10, D20, D21)

**Files:**
- Create: `internal/storage/queries/users.sql`
- Modify: `internal/storage/queries/sessions.sql` (`RevokeAllFamilies`)
- Create: `internal/account/account.go` (`Service`, `ValidateEmail`, `HasUsers`, `UserIDByEmail`, `CreateUser`, `ResetPassword`)
- Create: `cmd/vigilume/user.go` (`runUser`, leitura da senha, códigos de saída)
- Modify: `cmd/vigilume/main.go` (despacho de subcomandos)
- Modify: `go.mod` (`golang.org/x/term`)
- Test: `internal/account/account_test.go`, `internal/account/export_test.go`, `cmd/vigilume/user_test.go`

**Interfaces:**
- Consumes: `auth.ValidateNewPassword`, `auth.NormalizeEmail`, `auth.MinPasswordLen`, `auth.MaxPasswordLen`, `auth.ReasonTooShort`, `auth.ReasonTooLong` (Task 3); `db.LockUserSessions` (Task 1).
- Produces:
  - `account.NewService(pool *pgxpool.Pool, clk clock.Clock) *Service` — **a Task 5 muda para** `NewService(pool, totp *auth.TOTP, clk, ipKey []byte)`.
  - `account.ValidateEmail(email string) (string, error)`; `ErrUserExists`, `ErrUserNotFound`, `ErrInvalidEmail`; `type PolicyError struct{ Reason string }`.
  - `(*Service) HasUsers(ctx) (bool, error)`, `UserIDByEmail(ctx, email) (uuid.UUID, error)`, `CreateUser(ctx, email, password) (uuid.UUID, error)`, `ResetPassword(ctx, email, password) (int64, error)` (linhas de sessão revogadas).
  - `account.audit(ctx, q, userID, action, entity *string, entityID *uuid.UUID, ipHash []byte, at)` (privada, usada pelas Tasks 5 e 6).
  - `cmd/vigilume`: `var subcomandos map[string]func(ctx context.Context, args []string) int` — o Plano 7 acrescenta `healthcheck` e `version` nele. `runUser(ctx, args, in entradaDaSenha, stdout, stderr io.Writer, getenv func(string) string) int`. Códigos: `saidaOK = 0`, `saidaAmbiente = 1`, `saidaUso = 2`, `saidaJaExiste = 3`, `saidaNaoCadastrado = 4`.

**Contexto para quem implementa.** O subcomando lê **só** `DATABASE_URL` e faz o que o boot faz, na mesma ordem: `CheckRole` → `Migrate` → operação. Num banco virgem, migrar como superusuário criaria as tabelas com o dono errado, e o papel comum depois não conseguiria usá-las (D8 do Plano 2); o teste afirma o efeito ausente (a tabela `users` não existe), e não só o código de saída, porque o código sai igual nas duas ordens. `users`, `sessions` e `audit_log` não têm RLS, então o papel comum faz tudo sem escopo.

A pré-condição (já existe usuário / e-mail não cadastrado) é conferida **antes** de pedir a senha, para ninguém digitar duas vezes à toa; `CreateUser` confere de novo **sob a trava** `vigilume.user_create` (D9). O teste de concorrência força a sobreposição com um gancho entre a contagem e a inserção: cada criação espera a outra chegar ali, ou meio segundo; com a trava, a segunda nem conta antes de a primeira comitar.

A senha vem só do stdin (D6): num terminal, duas vezes e sem eco (`term.ReadPassword`, com os *prompts* no stderr); num pipe, uma linha só, até 4 KiB, tirando **um** `\n` ou `\r\n` final. Sobrando quebra de linha, é erro: pegar só a primeira linha de um arquivo errado criaria um usuário com uma senha que ninguém sabe. A chamada real a `term.ReadPassword` não tem teste automatizado; confira à mão uma vez (`go run ./cmd/vigilume user create --email x@y.com` num terminal, com `DATABASE_URL` de um banco de teste) e registre no relatório.

A query que regrava o hash chama `ReplaceUserHash`, e não `ResetPasswordHash`: o `gosec` (G101) lê a constante gerada com "password" no nome como credencial embutida.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/account/export_test.go`:

```go
package account

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }
```

Crie `internal/account/account_test.go`:

```go
package account_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

const senha = "uma-senha-bem-comprida-e-boa"

func novo(t *testing.T) (*account.Service, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return account.NewService(pool, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))), pool
}

// Dois `vigilume user create` ao mesmo tempo não criam dois donos (D9 do Plano
// 5). O gancho segura cada criação entre a contagem e a inserção até as duas
// chegarem ali, ou por meio segundo: com a trava, a segunda não chega à
// contagem enquanto a primeira não comitar, e espera o meio segundo sozinha;
// sem a trava, as duas contam zero e as duas inserem.
func TestConcurrentCreateMakesOneUser(t *testing.T) {
	svc, pool := novo(t)
	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i, email := range []string{"a@exemplo.com", "b@exemplo.com"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = svc.CreateUser(context.Background(), email, senha)
		}()
	}
	wg.Wait()

	var ok, recusadas int
	for _, err := range erros {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, account.ErrUserExists):
			recusadas++
		default:
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || recusadas != 1 || n != 1 {
		t.Errorf("%d criações, %d recusas, %d usuários; queria 1, 1 e 1", ok, recusadas, n)
	}
}

func TestValidateEmail(t *testing.T) {
	bons := map[string]string{
		"dono@exemplo.com":     "dono@exemplo.com",
		"  Dono@Exemplo.com\t": "Dono@Exemplo.com",
	}
	for entrada, queria := range bons {
		if got, err := account.ValidateEmail(entrada); err != nil || got != queria {
			t.Errorf("ValidateEmail(%q) = %q, %v; queria %q", entrada, got, err, queria)
		}
	}
	for _, ruim := range []string{"", "dono", "Dono <dono@exemplo.com>", "a@b.com, c@d.com",
		"dono@" + string(make([]byte, 250))} {
		if _, err := account.ValidateEmail(ruim); !errors.Is(err, account.ErrInvalidEmail) {
			t.Errorf("ValidateEmail(%q) aceitou", ruim)
		}
	}
}
```

Crie `cmd/vigilume/user_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// senhaMarcada é distinta o bastante para ser procurada na saída: ela nunca
// pode aparecer no stdout nem no stderr.
const senhaMarcada = "marca-d-agua-7f3a9c-senha-do-teste"

type execucao struct {
	codigo         int
	stdout, stderr string
}

func (e execucao) saida() string { return e.stdout + e.stderr }

// rodarUser chama runUser com o DSN do papel da aplicação e a senha num pipe.
func rodarUser(t *testing.T, dsn string, stdin string, args ...string) execucao {
	t.Helper()
	return rodarUserCom(t, dsn, entradaDaSenha{pipe: strings.NewReader(stdin)}, nil, args...)
}

func rodarUserCom(t *testing.T, dsn string, in entradaDaSenha, env map[string]string, args ...string) execucao {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return dsn
		}
		return env[k]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := runUser(ctx, args, in, &out, &errb, getenv)
	return execucao{codigo: c, stdout: out.String(), stderr: errb.String()}
}

func usuarios(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// senhaConfere lê o hash gravado e o confere com a senha dada.
func senhaGravadaConfere(t *testing.T, pool *pgxpool.Pool, email, senha string) bool {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE email = $1`, email).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, _, err := auth.VerifyPassword(hash, senha)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func auditorias(t *testing.T, pool *pgxpool.Pool, acao string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1`, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUserCreateFromPipe(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	r := rodarUser(t, dsn, senhaMarcada+"\n", "create", "--email", "  Dono@Exemplo.com ")
	if r.codigo != saidaOK {
		t.Fatalf("código %d, queria 0; stderr: %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stdout, "Dono@Exemplo.com") {
		t.Errorf("stdout não diz quem foi criado: %q", r.stdout)
	}
	// O e-mail sai normalizado como no login (espaço em volta) e com a caixa
	// preservada; o citext compara sem ela.
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("o hash gravado não confere com a senha do stdin")
	}
	if n := auditorias(t, pool, "user.created"); n != 1 {
		t.Errorf("%d registros user.created, queria 1", n)
	}
}

// Um \n ou \r\n final sai; mais de uma linha é recusado. Provado pelo
// set-password, que troca a senha do mesmo usuário a cada rodada.
func TestUserPasswordLineHandling(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("create: código %d; %s", r.codigo, r.stderr)
	}

	for fim, nome := range map[string]string{"\n": "lf", "\r\n": "crlf", "": "nada"} {
		nova := "outra-senha-comprida-" + nome
		r := rodarUser(t, dsn, nova+fim, "set-password", "--email", "dono@exemplo.com")
		if r.codigo != saidaOK {
			t.Fatalf("fim %q: código %d; %s", fim, r.codigo, r.stderr)
		}
		if !senhaGravadaConfere(t, pool, "dono@exemplo.com", nova) {
			t.Errorf("fim %q: a senha gravada não é a linha sem a quebra", fim)
		}
	}

	for _, entrada := range []string{"primeira-linha-longa\nsegunda-linha-longa", "senha-comprida-demais\r", "a\n\n"} {
		r := rodarUser(t, dsn, entrada, "set-password", "--email", "dono@exemplo.com")
		if r.codigo != saidaUso {
			t.Errorf("entrada %q: código %d, queria 2", entrada, r.codigo)
		}
	}
	// A última senha boa continua valendo: as recusas não mexeram em nada.
	senhas := 0
	for _, nome := range []string{"lf", "crlf", "nada"} {
		if senhaGravadaConfere(t, pool, "dono@exemplo.com", "outra-senha-comprida-"+nome) {
			senhas++
		}
	}
	if senhas != 1 {
		t.Error("uma entrada recusada mudou a senha")
	}
}

// Usuário único (spec §2.1): com qualquer usuário no banco, create sai com 3
// e não muda nada — nem cria outro, nem mexe na senha do existente.
func TestUserCreateRefusesWhenAUserExists(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("primeiro create: código %d; %s", r.codigo, r.stderr)
	}

	for _, email := range []string{"dono@exemplo.com", "outra@exemplo.com"} {
		r := rodarUser(t, dsn, "uma-senha-nova-bem-longa", "create", "--email", email)
		if r.codigo != saidaJaExiste {
			t.Errorf("%s: código %d, queria 3", email, r.codigo)
		}
	}
	if n := usuarios(t, pool); n != 1 {
		t.Errorf("%d usuários depois das recusas, queria 1", n)
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("a recusa trocou a senha do usuário existente")
	}
}

// A senha nunca sai: nem no sucesso, nem num erro de política, nem na
// confirmação que não confere.
func TestUserPasswordNeverLeaves(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	longa := senhaMarcada + strings.Repeat("x", auth.MaxPasswordLen)
	execucoes := map[string]execucao{
		"muito longa": rodarUser(t, dsn, longa, "create", "--email", "dono@exemplo.com"),
		"confirmação diferente": rodarUserCom(t, dsn, entradaDaSenha{terminal: true, lerSemEco: sequencia(senhaMarcada, senhaMarcada+"!")},
			nil, "create", "--email", "dono@exemplo.com"),
		"sucesso": rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"),
	}
	for nome, r := range execucoes {
		if strings.Contains(r.saida(), "marca-d-agua") {
			t.Errorf("%s: a senha apareceu na saída: %q", nome, r.saida())
		}
	}
	if execucoes["muito longa"].codigo != saidaUso || execucoes["confirmação diferente"].codigo != saidaUso {
		t.Error("as recusas não saíram com 2")
	}
	if n := usuarios(t, pool); n != 1 {
		t.Errorf("%d usuários, queria 1 (só o do sucesso)", n)
	}
}

// sequencia devolve cada senha numa chamada, como o terminal faria.
func sequencia(senhas ...string) func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i >= len(senhas) {
			return nil, errors.New("leitura a mais")
		}
		i++
		return []byte(senhas[i-1]), nil
	}
}

func TestUserTerminalAsksTwice(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	r := rodarUserCom(t, dsn, entradaDaSenha{terminal: true, lerSemEco: sequencia(senhaMarcada, senhaMarcada)},
		nil, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("código %d; %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stderr, "Confirme a senha") {
		t.Error("o terminal não pediu a confirmação")
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("a senha digitada não foi a gravada")
	}
}

// Não existe flag nem variável de ambiente de senha (D6 do Plano 5). Com o
// stdin vazio e uma senha válida no ambiente, o resultado tem de ser "muito
// curta" — a variável não foi lida.
func TestUserHasNoPasswordFlagOrEnv(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	if r := rodarUser(t, dsn, "", "create", "--email", "dono@exemplo.com", "--password", senhaMarcada); r.codigo != saidaUso {
		t.Errorf("--password: código %d, queria 2", r.codigo)
	}
	if r := rodarUser(t, dsn, "", "create", "--email", "dono@exemplo.com", senhaMarcada); r.codigo != saidaUso {
		t.Errorf("senha posicional: código %d, queria 2", r.codigo)
	}
	env := map[string]string{"VIGILUME_PASSWORD": senhaMarcada, "PASSWORD": senhaMarcada, "SENHA": senhaMarcada}
	r := rodarUserCom(t, dsn, entradaDaSenha{pipe: strings.NewReader("")}, env, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaUso || !strings.Contains(r.stderr, "muito curta") {
		t.Errorf("senha no ambiente: código %d, stderr %q; queria 2 e \"muito curta\"", r.codigo, r.stderr)
	}
	if n := usuarios(t, pool); n != 0 {
		t.Errorf("%d usuários criados sem senha no stdin", n)
	}
}

func TestUserRejectsBadInvocations(t *testing.T) {
	testdb.New(t)
	dsn := testdb.DSN(t)
	casos := map[string][]string{
		"sem subcomando":       {},
		"subcomando estranho":  {"delete", "--email", "dono@exemplo.com"},
		"sem e-mail":           {"create"},
		"e-mail sem arroba":    {"create", "--email", "dono"},
		"com nome de exibição": {"create", "--email", "Dono <dono@exemplo.com>"},
	}
	for nome, args := range casos {
		if r := rodarUser(t, dsn, senhaMarcada, args...); r.codigo != saidaUso {
			t.Errorf("%s: código %d, queria 2", nome, r.codigo)
		}
	}
	if r := rodarUser(t, "", senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaUso {
		t.Errorf("sem DATABASE_URL: código %d, queria 2", r.codigo)
	}
}

// O CLI recusa o superusuário ANTES de migrar, como o boot. Só o código não
// basta, porque ele sai igual nas duas ordens: o que trava a ordem é o banco
// continuar vazio.
func TestUserRefusesSuperuserBeforeMigrating(t *testing.T) {
	vazio, err := url.Parse(testdb.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := url.Parse(testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	vazio.User = admin.User

	r := rodarUser(t, vazio.String(), senhaMarcada, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaAmbiente {
		t.Errorf("superusuário: código %d, queria 1; stderr %q", r.codigo, r.stderr)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, vazio.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var tabela *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('users')::text`).Scan(&tabela); err != nil {
		t.Fatal(err)
	}
	if tabela != nil {
		t.Error("o CLI migrou como superusuário antes de recusar: as tabelas nasceram com o dono errado")
	}
}

// set-password é o caminho de recuperação: senha nova, bloqueio desfeito, TODAS
// as sessões do usuário encerradas, e só as dele. O segundo fator fica.
func TestSetPasswordRevokesEverySessionAndClearsTheLock(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("create: código %d; %s", r.codigo, r.stderr)
	}
	var dono uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users`).Scan(&dono); err != nil {
		t.Fatal(err)
	}
	// Outro usuário, inserido por fora (o CLI só cria o primeiro).
	outro := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'outro@exemplo.com', 'x')`, outro); err != nil {
		t.Fatal(err)
	}

	mgr := auth.NewManager(auth.NewPGStore(pool), clock.Real{}, []byte("chave-de-teste-com-32-bytes-ok!!"))
	meta := auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}
	for range 2 {
		if _, _, err := mgr.Issue(ctx, dono, meta); err != nil {
			t.Fatal(err)
		}
	}
	doOutro, _, err := mgr.Issue(ctx, outro, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET failed_attempts = 5, locked_until = now() + interval '1 hour', totp_confirmed_at = now() WHERE id = $1`,
		dono); err != nil {
		t.Fatal(err)
	}

	r := rodarUser(t, dsn, "a-senha-nova-do-dono", "set-password", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("set-password: código %d; %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stdout, "2 sessões encerradas") {
		t.Errorf("stdout = %q, queria a contagem de sessões", r.stdout)
	}

	var vivas, falhas int
	var travada, totp bool
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, dono).Scan(&vivas); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT failed_attempts, locked_until IS NOT NULL, totp_confirmed_at IS NOT NULL FROM users WHERE id = $1`,
		dono).Scan(&falhas, &travada, &totp); err != nil {
		t.Fatal(err)
	}
	if vivas != 0 {
		t.Errorf("%d sessões do dono continuaram vivas", vivas)
	}
	if falhas != 0 || travada {
		t.Errorf("o bloqueio ficou: %d falhas, travada = %v", falhas, travada)
	}
	if !totp {
		t.Error("set-password desligou o segundo fator")
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", "a-senha-nova-do-dono") {
		t.Error("a senha nova não foi gravada")
	}
	if n := auditorias(t, pool, "user.password_reset"); n != 1 {
		t.Errorf("%d registros user.password_reset, queria 1", n)
	}
	// A sessão do outro usuário continua viva: prova direta, pelo refresh.
	if _, _, err := mgr.Rotate(ctx, doOutro, meta); err != nil {
		t.Errorf("a sessão de outro usuário caiu junto: %v", err)
	}
}

func TestSetPasswordUnknownEmail(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	r := rodarUser(t, dsn, senhaMarcada, "set-password", "--email", "ninguem@exemplo.com")
	if r.codigo != saidaNaoCadastrado {
		t.Errorf("código %d, queria 4", r.codigo)
	}
	if n := usuarios(t, pool); n != 0 {
		t.Errorf("%d usuários criados por um set-password", n)
	}
}

func TestUserPolicyBoundaries(t *testing.T) {
	testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, strings.Repeat("a", auth.MinPasswordLen-1), "create", "--email", "dono@exemplo.com"); r.codigo != saidaUso {
		t.Errorf("14 caracteres: código %d, queria 2", r.codigo)
	}
	if r := rodarUser(t, dsn, strings.Repeat("é", auth.MinPasswordLen), "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Errorf("15 caracteres de dois bytes: código %d, queria 0; %s", r.codigo, r.stderr)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/account/ ./cmd/vigilume/`
Expected: FAIL de compilação (`undefined: Service` em `export_test.go`; `undefined: entradaDaSenha`, `runUser`, `saidaOK` em `user_test.go`).

- [ ] **Step 3: Implementar**

Crie `internal/storage/queries/users.sql`:

```sql
-- Conta do usuário (Plano 5). users não tem RLS (D7 do Plano 2): o login a
-- consulta por e-mail antes de existir usuário na transação. Toda query daqui
-- que parte de um usuário recebe o id dele.

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: InsertUser :exec
INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4);

-- A senha redefinida pelo operador também tira o bloqueio: é o caminho de
-- recuperação de quem ficou trancado para fora. (O nome evita "password": o
-- gosec lê a constante gerada como credencial embutida, G101.)
-- name: ReplaceUserHash :execrows
UPDATE users SET password_hash = $2, failed_attempts = 0, locked_until = NULL WHERE id = $1;

-- Serializa dois `vigilume user create` simultâneos: sem ela, os dois contam
-- zero usuários e os dois criam (D9 do Plano 5).
-- name: LockUserCreation :exec
SELECT pg_advisory_xact_lock(hashtextextended('vigilume.user_create', 0));

-- O registro de auditoria com entidade. O insertAudit do auth (sem entidade)
-- continua à mão até o R2.
-- name: InsertAuditEntry :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, ip_hash, at)
VALUES ($1, $2, $3, $4, $5, $6, $7);
```

Aplique em `internal/storage/queries/sessions.sql`:

```diff
--- a/internal/storage/queries/sessions.sql
+++ b/internal/storage/queries/sessions.sql
@@ -13,3 +13,9 @@
 -- impede o ciclo (deadlock) entre rotação e revogação.
 -- name: LockUserSessions :exec
 SELECT pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' || sqlc.arg(user_id)::uuid::text, 0));
+
+-- Todas as famílias do usuário: é o set-password do CLI. Marca também as
+-- linhas já rotacionadas, como o RevokeFamily do auth.
+-- name: RevokeAllFamilies :execrows
+UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
+ WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;
```

Rode `sqlc generate` (cria `internal/storage/db/users.sql.go` e atualiza `sessions.sql.go`).

Crie `internal/account/account.go`:

```go
// Package account é o serviço da conta do dono: criar e redefinir a senha
// pelo CLI (Plano 5). O CLI e a API chamam o mesmo serviço, para que a
// política de senha e a normalização do e-mail sejam as mesmas nos dois (D20
// do Plano 5).
//
// users e sessions não têm RLS (D7 do Plano 2). A barreira aqui é outra: toda
// query que parte de um usuário recebe o id dele, e cada uma tem teste de
// isolamento com sabotagem.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var (
	// ErrUserExists: o sistema já tem usuário (spec §2.1: usuário único).
	ErrUserExists = errors.New("já existe um usuário")
	// ErrUserNotFound: nenhum usuário com este e-mail.
	ErrUserNotFound = errors.New("usuário não cadastrado")
	// ErrInvalidEmail: não é um endereço puro (sem nome de exibição).
	ErrInvalidEmail = errors.New("e-mail inválido")
)

// PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
// ou auth.ReasonTooLong.
type PolicyError struct{ Reason string }

func (e PolicyError) Error() string { return "senha fora da política: " + e.Reason }

// maxEmailLen é o limite prático de um endereço (RFC 5321 §4.5.3.1.3 e a
// errata 1690: 254 no caminho).
const maxEmailLen = 254

type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem de usuários e a inserção.
	depoisDeContar func()
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

// ValidateEmail normaliza como o login (auth.NormalizeEmail) e exige um
// endereço puro: "Nome <a@b.com>" é recusado, porque o login compara o texto
// inteiro e nunca casaria com ele.
func ValidateEmail(email string) (string, error) {
	e := auth.NormalizeEmail(email)
	if len(e) == 0 || len(e) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Name != "" || addr.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// HasUsers responde se o sistema já tem usuário. O CLI pergunta ANTES de
// pedir a senha, para ninguém digitar duas vezes à toa; CreateUser confere de
// novo sob a trava.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := db.New(s.pool).CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("contar usuários: %w", err)
	}
	return n > 0, nil
}

// UserIDByEmail devolve ErrUserNotFound quando o e-mail não está cadastrado.
func (s *Service) UserIDByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	id, err := db.New(s.pool).GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("buscar usuário: %w", err)
	}
	return id, nil
}

// CreateUser cria o dono. Recusa com ErrUserExists se já existe QUALQUER
// usuário (D8 do Plano 5), e a recusa não muda nada.
//
// A contagem e a inserção rodam sob uma advisory lock de transação (D9): sem
// ela, dois CLIs simultâneos contariam zero e criariam dois donos.
func (s *Service) CreateUser(ctx context.Context, email, password string) (uuid.UUID, error) {
	e, err := ValidateEmail(email)
	if err != nil {
		return uuid.Nil, err
	}
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return uuid.Nil, PolicyError{Reason: motivo}
	}
	// O Argon2id leva ~100 ms: fora da transação, para não segurar a trava.
	hash, err := auth.HashPassword(password)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("gerar id do usuário: %w", err)
	}
	now := s.clk.Now()

	err = s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockUserCreation(ctx); err != nil {
			return fmt.Errorf("travar criação de usuário: %w", err)
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("contar usuários: %w", err)
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n > 0 {
			return ErrUserExists
		}
		if err := q.InsertUser(ctx, db.InsertUserParams{
			ID: id, Email: e, PasswordHash: hash, CreatedAt: now,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrUserExists
			}
			return fmt.Errorf("inserir usuário: %w", err)
		}
		return audit(ctx, q, id, "user.created", nil, nil, nil, now)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ResetPassword é o caminho de recuperação do operador (vigilume user
// set-password, D10 do Plano 5): grava a senha nova, tira o bloqueio e encerra
// TODAS as sessões. Recuperação quase sempre é "perdi" ou "vazou", e manter as
// sessões deixaria o invasor dentro. Não mexe no segundo fator.
//
// Devolve quantas linhas de sessão foram revogadas.
func (s *Service) ResetPassword(ctx context.Context, email, password string) (int64, error) {
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return 0, PolicyError{Reason: motivo}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	now := s.clk.Now()

	var revogadas int64
	err = s.inTx(ctx, func(q *db.Queries) error {
		userID, err := q.GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar usuário: %w", err)
		}
		// A trava do usuário antes de tocar em sessions (D1): sem ela, uma
		// rotação em curso deixaria o sucessor fora da revogação.
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		if _, err := q.ReplaceUserHash(ctx, db.ReplaceUserHashParams{ID: userID, PasswordHash: hash}); err != nil {
			return fmt.Errorf("gravar senha: %w", err)
		}
		revogadas, err = q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("revogar sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.password_reset", nil, nil, nil, now)
	})
	return revogadas, err
}

func (s *Service) inTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// audit grava na mesma transação da mudança: não existe mudança sem rastro,
// nem rastro de mudança que não aconteceu (mesma razão do R39).
func audit(ctx context.Context, q *db.Queries, userID uuid.UUID, action string, entity *string, entityID *uuid.UUID, ipHash []byte, at time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	if err := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ID: id, UserID: &userID, Action: action, Entity: entity, EntityID: entityID, IpHash: ipHash, At: at,
	}); err != nil {
		return fmt.Errorf("auditar %s: %w", action, err)
	}
	return nil
}
```

Crie `cmd/vigilume/user.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
)

// Códigos de saída de `vigilume user` (spec do Plano 5, §4.1). O 3 não muda de
// sentido: um passo idempotente (o seed do compose) o trata como sucesso.
const (
	saidaOK            = 0
	saidaAmbiente      = 1 // banco, papel com rolsuper/rolbypassrls, migration
	saidaUso           = 2 // flag, e-mail, DATABASE_URL, senha fora da política
	saidaJaExiste      = 3 // create: já existe usuário; nada mudou
	saidaNaoCadastrado = 4 // set-password: e-mail não cadastrado; nada mudou
)

// maxLeitura limita o que se lê de um pipe. A política corta em 128 code
// points (512 bytes no pior caso); o teto só impede ler um arquivo enorme por
// engano.
const maxLeitura = 4096

// entradaDaSenha é de onde a senha vem. Senha NUNCA vem de argumento nem de
// variável de ambiente (D6 do Plano 5): argumento aparece em `ps` e no
// histórico do shell; variável, em /proc/<pid>/environ e no `docker inspect`.
type entradaDaSenha struct {
	// terminal: pede a senha duas vezes, sem eco, por lerSemEco.
	terminal  bool
	lerSemEco func() ([]byte, error)
	// pipe: uma linha só, lida daqui.
	pipe io.Reader
}

// stdinDoProcesso decide entre terminal e pipe olhando o stdin de verdade.
func stdinDoProcesso() entradaDaSenha {
	fd := int(os.Stdin.Fd()) // #nosec G115 -- descritor de arquivo do stdin (0); cabe em int em toda plataforma suportada.
	if term.IsTerminal(fd) {
		return entradaDaSenha{terminal: true, lerSemEco: func() ([]byte, error) { return term.ReadPassword(fd) }}
	}
	return entradaDaSenha{pipe: os.Stdin}
}

const usoUser = `uso:
  vigilume user create --email <e-mail>        cria o dono (só se não houver usuário)
  vigilume user set-password --email <e-mail>  redefine a senha e encerra todas as sessões

A senha vem do stdin: num terminal, é pedida duas vezes, sem eco; num pipe,
é a única linha da entrada. Só DATABASE_URL é lida do ambiente.`

// runUser é `vigilume user ...`. Devolve o código de saída; quem chama sai do
// processo com ele.
func runUser(ctx context.Context, args []string, in entradaDaSenha, stdout, stderr io.Writer, getenv func(string) string) int {
	falhar := func(codigo int, formato string, a ...any) int {
		fmt.Fprintf(stderr, "vigilume user: "+formato+"\n", a...)
		return codigo
	}
	if len(args) == 0 || (args[0] != "create" && args[0] != "set-password") {
		fmt.Fprintln(stderr, usoUser)
		return saidaUso
	}
	sub := args[0]
	fs := flag.NewFlagSet("vigilume user "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "e-mail do usuário")
	if err := fs.Parse(args[1:]); err != nil {
		return saidaUso
	}
	if fs.NArg() > 0 {
		return falhar(saidaUso, "argumento inesperado: %q (a senha vem do stdin)", fs.Arg(0))
	}
	e, err := account.ValidateEmail(*email)
	if err != nil {
		return falhar(saidaUso, "--email precisa ser um endereço, como dono@exemplo.com")
	}
	dsn := strings.TrimSpace(getenv("DATABASE_URL"))
	if dsn == "" {
		return falhar(saidaUso, "DATABASE_URL não definida")
	}

	// A mesma ordem do boot (D7 do Plano 5): o papel antes da migration. Num
	// banco virgem, migrar como superusuário criaria as tabelas com o dono
	// errado, e o papel comum depois não conseguiria usá-las.
	pool, err := storage.NewPool(ctx, dsn)
	if err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	defer pool.Close()
	if err := storage.CheckRole(ctx, pool); err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	if err := storage.Migrate(ctx, dsn); err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	svc := account.NewService(pool, clock.Real{})

	// A pré-condição vem ANTES da senha: ninguém digita duas vezes à toa.
	switch sub {
	case "create":
		existe, err := svc.HasUsers(ctx)
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		if existe {
			return falhar(saidaJaExiste, "já existe um usuário; nada foi alterado (use `vigilume user set-password` para trocar a senha)")
		}
	case "set-password":
		if _, err := svc.UserIDByEmail(ctx, e); errors.Is(err, account.ErrUserNotFound) {
			return falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", e)
		} else if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
	}

	senha, err := lerSenha(in, stderr)
	if err != nil {
		return falhar(saidaUso, "%v", err)
	}
	switch auth.ValidateNewPassword(senha) {
	case auth.ReasonTooShort:
		return falhar(saidaUso, "senha muito curta (mínimo de %d caracteres)", auth.MinPasswordLen)
	case auth.ReasonTooLong:
		return falhar(saidaUso, "senha muito longa (máximo de %d caracteres)", auth.MaxPasswordLen)
	}

	switch sub {
	case "create":
		id, err := svc.CreateUser(ctx, e, senha)
		if errors.Is(err, account.ErrUserExists) {
			return falhar(saidaJaExiste, "já existe um usuário; nada foi alterado")
		}
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		fmt.Fprintf(stdout, "usuário criado: %s (id %s)\n", e, id)
	case "set-password":
		n, err := svc.ResetPassword(ctx, e, senha)
		if errors.Is(err, account.ErrUserNotFound) {
			return falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", e)
		}
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		fmt.Fprintf(stdout, "senha redefinida; %d sessões encerradas. Acessos já abertos expiram em até 15 minutos.\n", n)
	}
	return saidaOK
}

// lerSenha nunca escreve a senha em lugar nenhum; o erro diz o que houve, não
// o que foi lido.
//
// A senha vive como string depois disto: Go não tem como zerar uma string, e
// este código não finge que tem.
func lerSenha(in entradaDaSenha, stderr io.Writer) (string, error) {
	if in.terminal {
		fmt.Fprint(stderr, "Senha: ")
		a, err := in.lerSemEco()
		fmt.Fprintln(stderr) // o terminal não ecoa o Enter
		if err != nil {
			return "", fmt.Errorf("ler a senha: %w", err)
		}
		fmt.Fprint(stderr, "Confirme a senha: ")
		b, err := in.lerSemEco()
		fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("ler a confirmação: %w", err)
		}
		if !bytes.Equal(a, b) {
			return "", errors.New("as senhas não conferem")
		}
		return validarTexto(a)
	}

	b, err := io.ReadAll(io.LimitReader(in.pipe, maxLeitura+1))
	if err != nil {
		return "", fmt.Errorf("ler a senha do stdin: %w", err)
	}
	if len(b) > maxLeitura {
		return "", fmt.Errorf("a entrada tem mais de %d bytes", maxLeitura)
	}
	// Um \n (ou \r\n) final é o que `echo` e um arquivo de texto põem; sai um,
	// e só um. Sobrando quebra de linha, a entrada tem mais de uma linha, e
	// pegar só a primeira criaria um usuário com uma senha que ninguém sabe.
	if bytes.HasSuffix(b, []byte("\r\n")) {
		b = b[:len(b)-2]
	} else if bytes.HasSuffix(b, []byte("\n")) {
		b = b[:len(b)-1]
	}
	if bytes.ContainsAny(b, "\r\n") {
		return "", errors.New("a senha tem mais de uma linha")
	}
	return validarTexto(b)
}

func validarTexto(b []byte) (string, error) {
	if !utf8.Valid(b) {
		return "", errors.New("a senha não é UTF-8 válido")
	}
	return string(b), nil
}
```

Aplique em `cmd/vigilume/main.go`:

```diff
--- a/cmd/vigilume/main.go
+++ b/cmd/vigilume/main.go
@@ -31,7 +31,25 @@
 // totpIssuer é o nome que aparece no app autenticador do usuário.
 const totpIssuer = "Vigilume"
 
+// subcomandos são os modos do binário que não sobem o servidor. O primeiro
+// argumento escolhe um deles; sem subcomando, o processo sobe o servidor
+// (--role). O Plano 7 acrescenta healthcheck e version neste mesmo mapa.
+var subcomandos = map[string]func(ctx context.Context, args []string) int{
+	"user": func(ctx context.Context, args []string) int {
+		return runUser(ctx, args, stdinDoProcesso(), os.Stdout, os.Stderr, os.Getenv)
+	},
+}
+
 func main() {
+	if len(os.Args) > 1 {
+		if sub, ok := subcomandos[os.Args[1]]; ok {
+			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
+			codigo := sub(ctx, os.Args[2:])
+			stop()
+			os.Exit(codigo)
+		}
+	}
+
 	role := flag.String("role", "", "all | api | prober (sobrepõe a env ROLE)")
 	flag.Parse()
 
```

Aplique em `go.mod` (o `go.sum` já traz o `golang.org/x/term v0.46.0`, dependência indireta de antes):

```diff
--- a/go.mod
+++ b/go.mod
@@ -13,6 +13,7 @@
 	github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0
 	golang.org/x/crypto v0.57.0
 	golang.org/x/sync v0.23.0
+	golang.org/x/term v0.46.0
 	golang.org/x/time v0.16.0
 )
 
```

Run: `go mod download && go mod verify`

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go mod tidy -diff && go test -race -count=3 ./internal/account/ && go test -race -count=1 ./cmd/vigilume/`
Expected: PASS; `go mod tidy -diff` sem saída. As três rodadas do `account` são pelo teste de concorrência.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, restaure com `sqlc generate`):
1. Em `CreateUser`, troque `q.LockUserCreation(ctx)` por `error(nil)` no `if`. Run: `go test -count=1 -run TestConcurrentCreate ./internal/account/`. Expected: FAIL, `2 criações, 0 recusas, 2 usuários`.
2. Em `runUser`, ponha o `storage.Migrate` antes do `storage.CheckRole`. Run: `go test -count=1 -run TestUserRefusesSuperuser ./cmd/vigilume/`. Expected: FAIL, `o CLI migrou como superusuário antes de recusar`.
3. Em `ResetPassword`, troque a chamada de `RevokeAllFamilies` por `revogadas, err = int64(0), error(nil)`. Run: `go test -count=1 -run TestSetPassword ./cmd/vigilume/`. Expected: FAIL, `0 sessões encerradas` e `2 sessões do dono continuaram vivas`.
4. Em `internal/storage/db/sessions.sql.go`, troque o `WHERE user_id = $2` de `revokeAllFamilies` por `WHERE $2::uuid IS NOT NULL`. Mesmo comando. Expected: FAIL, `3 sessões encerradas` e `a sessão de outro usuário caiu junto`.
5. Em `internal/storage/db/users.sql.go`, tire `, failed_attempts = 0, locked_until = NULL` de `replaceUserHash`. Mesmo comando. Expected: FAIL, `o bloqueio ficou: 5 falhas, travada = true`.
6. Em `runUser`, logo depois de `lerSenha`, acrescente `if senha == "" { senha = getenv("VIGILUME_PASSWORD") }`. Run: `go test -count=1 -run TestUserHasNoPassword ./cmd/vigilume/`. Expected: FAIL, `código 0` e `1 usuários criados sem senha no stdin`.
7. Acrescente `: %s` e `senha` à mensagem de "muito longa". Run: `go test -count=1 -run TestUserPasswordNeverLeaves ./cmd/vigilume/`. Expected: FAIL, `a senha apareceu na saída`.
8. Em `lerSenha`, antes do `bytes.ContainsAny`, corte `b` na primeira quebra de linha (`if i := bytes.IndexAny(b, "\r\n"); i >= 0 { b = b[:i] }`). Run: `go test -count=1 -run TestUserPasswordLine ./cmd/vigilume/`. Expected: FAIL, `entrada "primeira-linha-longa\nsegunda-linha-longa": código 0, queria 2`.

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/storage/queries/ internal/storage/db/ internal/account/ cmd/vigilume/
git commit -m "feat(cli): vigilume user create e set-password"
```

---

### Task 5: `GET /me` enriquecido e `POST /auth/password` (D5, D11, D12, D19, D24)

**Files:**
- Create: `internal/storage/migrations/00007_sessions_live.sql`
- Modify: `internal/storage/queries/sessions.sql` (`FamilyIsLive`, `RevokeOtherFamilies`), `internal/storage/queries/users.sql` (`GetAccount`, `SwapUserHash`)
- Modify: `internal/account/account.go` (construtor novo, `Me`, `FamilyIsLive`, `ChangePassword`)
- Create: `internal/httpapi/accounthandlers.go` (`AccountHandlers`: `Me`, `ChangePassword`)
- Modify: `internal/httpapi/authhandlers.go` (o `Me` antigo sai), `internal/httpapi/routes.go` (`Deps.Accounts`, balde do login compartilhado), `cmd/vigilume/main.go`, `cmd/vigilume/user.go` (construtor novo)
- Test: `internal/account/account_test.go`, `internal/httpapi/account_integration_test.go` (novo), `internal/httpapi/auth_integration_test.go` (`Deps.Accounts` no harness)

**Interfaces:**
- Consumes: `middleware.FamilyID`, `accessDe` (Task 2); `auth.CheckPassword`, `falhasDeLogin` (Task 3); `account.audit`, `inTx`, `PolicyError` (Task 4).
- Produces:
  - `account.NewService(pool *pgxpool.Pool, totp *auth.TOTP, clk clock.Clock, ipKey []byte) *Service` (o CLI passa `nil, …, nil`).
  - `type Account struct { ID uuid.UUID; Email string; TOTPEnabled bool; CreatedAt time.Time }`; `(*Service) Me(ctx, userID) (Account, error)` (`ErrUserNotFound`).
  - `(*Service) FamilyIsLive(ctx, userID, familyID uuid.UUID) (bool, error)`; a função privada `familyIsLive(ctx, q *db.Queries, userID, familyID, now)`, que a Task 6 reusa.
  - `type ChangePasswordInput struct { Current, New, TOTPCode string }`; `(*Service) ChangePassword(ctx, userID, familyID uuid.UUID, in ChangePasswordInput, meta auth.Meta) error` — `PolicyError`, `ErrSessionGone` ou `ErrInvalidCredentials`.
  - `httpapi.NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix) *AccountHandlers` — **a Task 6 acrescenta** `secureCookie bool`.
  - `httpapi.Deps.Accounts *account.Service`.
  - Helpers de teste em `account_integration_test.go`: `entrar(t, a) (*http.Client, string)`, `renova`, `codigoDoErro`, `getComToken`, `accessTokenDaSessao`; em `account_test.go`: `montar`, `segurarTrava`, `esperarAlguemNaTrava` (e, com eles, a prova que faltava à Task 4: `TestResetPasswordTakesTheUserLock`).

**Contexto para quem implementa.** A ordem de `ChangePassword` (D12) é a garantia: a política primeiro (é pública; validá-la antes não vaza nada e não conta falha por erro de digitação na senha nova); a família do token viva (leitura barata); `CheckPassword` (bloqueio com `VerifyDummy`, falha contada); o 2FA pelo `auth.TOTP.Verify`, que aceita o código de recuperação e consome o timestep; o hash novo **fora** da transação; e a transação: trava do usuário (D1), **a família conferida de novo** sob a trava, o **CAS** do hash (`WHERE password_hash = <o verificado>`), as **outras** famílias revogadas e a auditoria. Um `nil` sempre quer dizer "trocada, e a atual continua viva".

As duas checagens de família são duas guardas da mesma propriedade (D23): `TestChangePasswordDeadFamily` prova a propriedade; a checagem sob a trava tem prova própria em `TestChangePasswordRechecksTheFamilyUnderTheLock`, que segura a trava por fora, revoga a família enquanto a troca espera e solta. `TestChangePasswordLosesToAConcurrentChange` faz o mesmo com o hash, para o CAS.

A rota fica atrás da **mesma instância** do limitador do login (D5). Todo teste HTTP daqui roda do mesmo `127.0.0.1` e divide o balde de 10/min com burst 5: por isso os testes conferem a senha gravada pelo hash, e não fazendo login, e nenhum passa de cinco requisições no balde.

`GET /me` passa para `AccountHandlers` e ganha o limite por usuário das rotas de recurso. `totp_enabled` é o fator **confirmado**: o teste cadastra sem confirmar e exige `false` antes de confirmar.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/account/account_test.go`:

```diff
--- a/internal/account/account_test.go
+++ b/internal/account/account_test.go
@@ -7,10 +7,13 @@
 	"testing"
 	"time"
 
+	"github.com/google/uuid"
 	"github.com/jackc/pgx/v5/pgxpool"
 
 	"github.com/BipoXDXD/vigilume/internal/account"
+	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/clock"
+	"github.com/BipoXDXD/vigilume/internal/crypto"
 	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
 )
 
@@ -19,7 +22,7 @@
 func novo(t *testing.T) (*account.Service, *pgxpool.Pool) {
 	t.Helper()
 	pool := testdb.New(t)
-	return account.NewService(pool, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))), pool
+	return account.NewService(pool, nil, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)), nil), pool
 }
 
 // Dois `vigilume user create` ao mesmo tempo não criam dois donos (D9 do Plano
@@ -89,3 +92,191 @@
 		}
 	}
 }
+
+var ipKey = []byte("chave-de-teste-com-32-bytes-ok!!")
+
+// cenario é um usuário com senha e uma sessão viva (a "atual"), e o serviço
+// completo, com segundo fator e chave de IP.
+type cenario struct {
+	svc   *account.Service
+	pool  *pgxpool.Pool
+	user  uuid.UUID
+	atual uuid.UUID // família da sessão atual
+}
+
+func montar(t *testing.T) cenario {
+	t.Helper()
+	pool := testdb.New(t)
+	clk := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
+	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	hash, err := auth.HashPassword(senha)
+	if err != nil {
+		t.Fatal(err)
+	}
+	user := uuid.New()
+	if _, err := pool.Exec(context.Background(),
+		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'dono@exemplo.com', $2)`, user, hash); err != nil {
+		t.Fatal(err)
+	}
+	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)
+	_, s, err := mgr.Issue(context.Background(), user, auth.Meta{IP: "203.0.113.9"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	svc := account.NewService(pool, auth.NewTOTP(pool, box, "Vigilume"), clk, ipKey)
+	return cenario{svc: svc, pool: pool, user: user, atual: s.FamilyID}
+}
+
+func chaveDoUsuario(user uuid.UUID) string {
+	return "hashtextextended('vigilume.sessions:' || '" + user.String() + "', 0)"
+}
+
+// segurarTrava toma, numa conexão do teste, a mesma advisory lock que as
+// mutações de sessão do usuário tomam (D1). Devolve a função que a solta.
+func segurarTrava(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) func() {
+	t.Helper()
+	ctx := context.Background()
+	conn, err := pool.Acquire(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chaveDoUsuario(user)+`)`); err != nil {
+		t.Fatal(err)
+	}
+	return func() {
+		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chaveDoUsuario(user)+`)`); err != nil {
+			t.Error(err)
+		}
+		conn.Release()
+	}
+}
+
+// esperarAlguemNaTrava espera um backend aparecer esperando uma advisory lock.
+// Devolve false se ninguém aparecer: quem chama decide se isso é falha.
+func esperarAlguemNaTrava(t *testing.T, pool *pgxpool.Pool) bool {
+	t.Helper()
+	prazo := time.Now().Add(3 * time.Second)
+	for time.Now().Before(prazo) {
+		var n int
+		if err := pool.QueryRow(context.Background(),
+			`SELECT count(*) FROM pg_stat_activity
+			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
+			t.Fatal(err)
+		}
+		if n > 0 {
+			return true
+		}
+		time.Sleep(10 * time.Millisecond)
+	}
+	return false
+}
+
+func trocar(c cenario) error {
+	return c.svc.ChangePassword(context.Background(), c.user, c.atual,
+		account.ChangePasswordInput{Current: senha, New: "a-senha-nova-bem-comprida"}, auth.Meta{IP: "203.0.113.9"})
+}
+
+// A troca toma a trava do usuário antes de tocar em sessions (D1): com ela
+// segura por fora, a troca espera; solta, termina.
+func TestChangePasswordTakesTheUserLock(t *testing.T) {
+	c := montar(t)
+	soltar := segurarTrava(t, c.pool, c.user)
+	fim := make(chan error, 1)
+	go func() { fim <- trocar(c) }()
+
+	if !esperarAlguemNaTrava(t, c.pool) {
+		soltar()
+		t.Fatalf("a troca não esperou a trava do usuário (terminou com %v)", <-fim)
+	}
+	soltar()
+	if err := <-fim; err != nil {
+		t.Fatalf("a troca falhou depois de soltar a trava: %v", err)
+	}
+}
+
+// O set-password do CLI também toma a trava do usuário antes de revogar as
+// famílias (D1, D10): sem ela, o sucessor de uma rotação em curso escaparia.
+func TestResetPasswordTakesTheUserLock(t *testing.T) {
+	c := montar(t)
+	soltar := segurarTrava(t, c.pool, c.user)
+	fim := make(chan error, 1)
+	go func() {
+		_, err := c.svc.ResetPassword(context.Background(), "dono@exemplo.com", "a-senha-redefinida-pelo-cli")
+		fim <- err
+	}()
+
+	if !esperarAlguemNaTrava(t, c.pool) {
+		soltar()
+		t.Fatalf("a redefinição não esperou a trava do usuário (terminou com %v)", <-fim)
+	}
+	soltar()
+	if err := <-fim; err != nil {
+		t.Fatalf("a redefinição falhou depois de soltar a trava: %v", err)
+	}
+}
+
+// A família atual pode cair entre a primeira checagem e a trava (um DELETE
+// /sessions de outro aparelho). A troca confere de novo sob a trava, e aí nada
+// muda: o sucesso sempre quer dizer "trocada e a atual continua viva".
+func TestChangePasswordRechecksTheFamilyUnderTheLock(t *testing.T) {
+	c := montar(t)
+	soltar := segurarTrava(t, c.pool, c.user)
+	fim := make(chan error, 1)
+	go func() { fim <- trocar(c) }()
+	if !esperarAlguemNaTrava(t, c.pool) {
+		soltar()
+		t.Fatal("a troca não chegou à trava")
+	}
+	// Revogada por fora enquanto a troca espera a trava.
+	if _, err := c.pool.Exec(context.Background(),
+		`UPDATE sessions SET revoked_at = now() WHERE family_id = $1`, c.atual); err != nil {
+		t.Fatal(err)
+	}
+	soltar()
+
+	if err := <-fim; !errors.Is(err, account.ErrSessionGone) {
+		t.Fatalf("erro = %v, queria ErrSessionGone", err)
+	}
+	var hash string
+	if err := c.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
+		t.Fatal(err)
+	}
+	if ok, _, _ := auth.VerifyPassword(hash, senha); !ok {
+		t.Error("a senha mudou mesmo com a sessão atual encerrada")
+	}
+}
+
+// A senha mudou depois de verificada (outra troca concorrente): o CAS recusa,
+// e a troca que perdeu não grava por cima.
+func TestChangePasswordLosesToAConcurrentChange(t *testing.T) {
+	c := montar(t)
+	soltar := segurarTrava(t, c.pool, c.user)
+	fim := make(chan error, 1)
+	go func() { fim <- trocar(c) }()
+	if !esperarAlguemNaTrava(t, c.pool) {
+		soltar()
+		t.Fatal("a troca não chegou à trava")
+	}
+	outra, err := auth.HashPassword("a-outra-troca-que-ganhou")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := c.pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE id = $1`, c.user, outra); err != nil {
+		t.Fatal(err)
+	}
+	soltar()
+
+	if err := <-fim; !errors.Is(err, account.ErrInvalidCredentials) {
+		t.Fatalf("erro = %v, queria ErrInvalidCredentials", err)
+	}
+	var hash string
+	if err := c.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
+		t.Fatal(err)
+	}
+	if hash != outra {
+		t.Error("a troca que perdeu gravou por cima da que ganhou")
+	}
+}
```

Crie `internal/httpapi/account_integration_test.go`:

```go
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// A política pede 15 caracteres; testPass tem 22.
const senhaNova = "a-senha-nova-bem-comprida"

// entrar faz login num cliente próprio (um cookie jar = um aparelho) e devolve
// o cliente e o access token. Cada chamada abre uma família de sessão.
//
// Atenção ao orçamento: o login e POST /auth/password dividem o balde de 10/min
// com burst 5 por IP, e todo teste daqui roda do mesmo 127.0.0.1.
func entrar(t *testing.T, a *api) (*http.Client, string) {
	t.Helper()
	c := clientWithJar(t)
	resp := login(t, a, c, testEmail, testPass)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}
	return c, accessDe(t, resp)
}

func trocarSenha(t *testing.T, a *api, token string, corpo map[string]string) *http.Response {
	t.Helper()
	return postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/password", token, corpo)
}

// renova diz se o refresh do aparelho ainda vale.
func renova(t *testing.T, a *api, c *http.Client) bool {
	t.Helper()
	resp := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// senhaGravadaE confere o hash gravado sem passar pelo login, que gastaria o
// balde compartilhado.
func senhaGravadaE(t *testing.T, a *api, senha string) bool {
	t.Helper()
	var hash string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, a.userID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, _, err := auth.VerifyPassword(hash, senha)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func codigoDoErro(t *testing.T, resp *http.Response) string {
	t.Helper()
	return decodeEnvelope(t, resp).Error.Code
}

func getComToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type conta struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

func lerMe(t *testing.T, a *api, token string) conta {
	t.Helper()
	resp := getComToken(t, a.srv.URL+"/api/v1/me", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me devolveu %d", resp.StatusCode)
	}
	var c conta
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// totp_enabled é o fator CONFIRMADO: o cadastro iniciado e não confirmado não
// liga nada, e é essa a perna que distingue as duas colunas.
func TestMeReturnsTheAccount(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	c := lerMe(t, a, token)
	if c.ID != a.userID.String() || c.Email != testEmail || c.TOTPEnabled || c.CreatedAt.IsZero() {
		t.Fatalf("/me = %+v", c)
	}

	er := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token, map[string]string{"password": testPass})
	er.Body.Close()
	if er.StatusCode != http.StatusOK {
		t.Fatalf("enroll devolveu %d", er.StatusCode)
	}
	if lerMe(t, a, token).TOTPEnabled {
		t.Error("totp_enabled ligou com o cadastro só iniciado")
	}

	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET totp_confirmed_at = now() WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	if !lerMe(t, a, token).TOTPEnabled {
		t.Error("totp_enabled desligado com o fator confirmado")
	}
}

// O coração da troca: três aparelhos logados; troca-se a senha no primeiro. O
// primeiro continua renovando; os outros dois não; a sessão de outro usuário
// não é tocada.
func TestChangePasswordKeepsCurrentRevokesOthers(t *testing.T) {
	a := newAPI(t)
	atual, token := entrar(t, a)
	b, _ := entrar(t, a)
	c, _ := entrar(t, a)

	outro := outroUsuario(t, a)
	mgr := auth.NewManager(auth.NewPGStore(a.pool), a.clk, testIPHashKey)
	doOutro, _, err := mgr.Issue(context.Background(), outro, auth.Meta{IP: "203.0.113.7"})
	if err != nil {
		t.Fatal(err)
	}

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("troca devolveu %d, queria 204", resp.StatusCode)
	}

	if !renova(t, a, atual) {
		t.Error("a sessão atual caiu junto: a troca tinha de mantê-la")
	}
	if renova(t, a, b) || renova(t, a, c) {
		t.Error("uma sessão de outro aparelho sobreviveu à troca de senha")
	}
	if _, _, err := mgr.Rotate(context.Background(), doOutro, auth.Meta{IP: "203.0.113.7"}); err != nil {
		t.Errorf("a sessão de outro usuário caiu: %v", err)
	}
	if !senhaGravadaE(t, a, senhaNova) || senhaGravadaE(t, a, testPass) {
		t.Error("o hash gravado não é o da senha nova")
	}

	var auditadas int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.password_changed' AND ip_hash IS NOT NULL`,
		a.userID).Scan(&auditadas); err != nil {
		t.Fatal(err)
	}
	if auditadas != 1 {
		t.Errorf("%d registros user.password_changed com ip_hash, queria 1", auditadas)
	}
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	outro, _ := entrar(t, a)

	resp := trocarSenha(t, a, token, map[string]string{"current_password": "nao-e-a-senha-atual", "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigoDoErro(t, resp) != "invalid_credentials" {
		t.Fatalf("status %d, queria 401 invalid_credentials", resp.StatusCode)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a senha mudou com a senha atual errada")
	}
	if n := falhasDeLogin(t, a); n != 1 {
		t.Errorf("%d falhas contadas, queria 1: a troca virou oráculo de senha", n)
	}
	if !renova(t, a, outro) {
		t.Error("a troca recusada revogou as outras sessões")
	}
}

// Bloqueada, a conta recusa até a senha certa (R34): mesmo 401, nada muda.
func TestChangePasswordLockedAccount(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	bloquearConta(t, a)

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigoDoErro(t, resp) != "invalid_credentials" {
		t.Fatalf("status %d, queria 401 invalid_credentials", resp.StatusCode)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a conta bloqueada trocou a senha")
	}
}

// Conta bloqueada paga o mesmo Argon2id que a senha errada: sem VerifyDummy, a
// resposta rápida anunciaria o bloqueio (R34). Relativo à verificação real,
// menor de duas medidas, como no login.
func TestChangePasswordSpendsTheSameCPU(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	medir := func(atual string) time.Duration {
		t.Helper()
		inicio := time.Now()
		resp := trocarSenha(t, a, token, map[string]string{"current_password": atual, "new_password": senhaNova})
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, queria 401", resp.StatusCode)
		}
		return time.Since(inicio)
	}
	real := min(medir("errada-numero-um-longa"), medir("errada-numero-dois-longa"))

	bloquearConta(t, a)
	if bloqueada := medir(testPass); bloqueada < real/2 {
		t.Errorf("conta bloqueada custou %v contra %v da verificação real: falta VerifyDummy", bloqueada, real)
	}
}

// Com o segundo fator ligado, a senha sozinha não troca nada: sem código e com
// código errado é o 401 genérico, e o errado conta no bloqueio (R40).
func TestChangePasswordRequiresTOTP(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	token := accessTokenDaSessao(t, a, c)

	// invalid_credentials, e nunca unauthorized: o painel renova a sessão em
	// unauthorized, e um código digitado errado viraria logout (D24).
	for nome, corpo := range map[string]map[string]string{
		"sem código":    {"current_password": testPass, "new_password": senhaNova},
		"código errado": {"current_password": testPass, "new_password": senhaNova, "totp_code": "000000"},
	} {
		resp := trocarSenha(t, a, token, corpo)
		got := codigoDoErro(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || got != "invalid_credentials" {
			t.Fatalf("%s: %d %s; queria 401 invalid_credentials", nome, resp.StatusCode, got)
		}
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Fatal("a senha mudou sem o segundo fator")
	}
	if n := falhasDeLogin(t, a); n != 2 {
		t.Errorf("%d falhas, queria 2 (sem código e código errado)", n)
	}

	code, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	certo := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova, "totp_code": code})
	certo.Body.Close()
	if certo.StatusCode != http.StatusNoContent {
		t.Fatalf("com o código certo: %d, queria 204", certo.StatusCode)
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("o sucesso não zerou o contador: %d", n)
	}
}

// O código de recuperação vale no lugar do código do app, e é consumido.
func TestChangePasswordAcceptsARecoveryCode(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)
	token := accessTokenDaSessao(t, a, c)

	resp := trocarSenha(t, a, token, map[string]string{
		"current_password": testPass, "new_password": senhaNova, "totp_code": a.recuperacao[0],
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("com código de recuperação: %d, queria 204", resp.StatusCode)
	}
	var restantes, usos int
	ctx := context.Background()
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, a.userID).Scan(&restantes); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'totp.recovery_used'`, a.userID).Scan(&usos); err != nil {
		t.Fatal(err)
	}
	if restantes != 9 || usos != 1 {
		t.Errorf("%d códigos restantes e %d usos auditados; queria 9 e 1", restantes, usos)
	}
}

// A política vem antes de tudo: com a senha nova fora dela, a senha atual
// (aqui, errada) nem é verificada, e nenhuma falha é contada.
func TestChangePasswordPolicy(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	casos := map[string]string{
		"too_short": strings.Repeat("a", auth.MinPasswordLen-1),
		"too_long":  strings.Repeat("a", auth.MaxPasswordLen+1),
	}
	for motivo, nova := range casos {
		resp := trocarSenha(t, a, token, map[string]string{"current_password": "errada-mas-irrelevante", "new_password": nova})
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Fields []struct {
					Field  string `json:"field"`
					Reason string `json:"reason"`
				} `json:"fields"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" ||
			len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "new_password" || env.Error.Fields[0].Reason != motivo {
			t.Errorf("%s: status %d, corpo %+v", motivo, resp.StatusCode, env.Error)
		}
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas contadas: a política tem de vir antes da senha atual", n)
	}
}

// Com a família do token encerrada (aqui, pelo logout), a troca é 401
// unauthorized e nada muda, nem com a senha certa.
//
// Duas guardas dão esta propriedade: a leitura antes da senha e a checagem sob
// a trava. Sabotar só uma deixa o teste verde; a segunda tem prova própria em
// TestChangePasswordRechecksTheFamilyUnderTheLock (internal/account).
func TestChangePasswordDeadFamily(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigoDoErro(t, resp) != "unauthorized" {
		t.Fatalf("status %d, queria 401 unauthorized", resp.StatusCode)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a senha mudou com a sessão encerrada")
	}
}

// A troca divide o balde do login (contrato: "mesmo limite por IP"). Os cinco
// logins usam e-mail inexistente para não travar a conta: o que se mede é o
// limitador.
func TestChangePasswordSharesTheLoginLimiter(t *testing.T) {
	a := newAPI(t)
	for range 5 {
		resp := login(t, a, clientWithJar(t), "ninguem@exemplo.com", testPass)
		resp.Body.Close()
	}
	resp := trocarSenha(t, a, "token-qualquer", map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status %d, queria 429: a troca não divide o balde do login", resp.StatusCode)
	}
}

// accessTokenDaSessao renova no aparelho c e devolve o access token novo, com
// o sid da família viva daquele aparelho.
func accessTokenDaSessao(t *testing.T, a *api, c *http.Client) string {
	t.Helper()
	resp := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh devolveu %d", resp.StatusCode)
	}
	return accessDe(t, resp)
}
```

Aplique em `internal/httpapi/auth_integration_test.go`:

```diff
--- a/internal/httpapi/auth_integration_test.go
+++ b/internal/httpapi/auth_integration_test.go
@@ -16,6 +16,7 @@
 	"github.com/google/uuid"
 	"github.com/jackc/pgx/v5/pgxpool"
 
+	"github.com/BipoXDXD/vigilume/internal/account"
 	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/clock"
 	"github.com/BipoXDXD/vigilume/internal/config"
@@ -113,6 +114,7 @@
 	discord, email := &discordFalso{}, &carteiroFalso{}
 	deps := httpapi.Deps{
 		Store: store, Sessions: mgr, TOTP: segundoFator,
+		Accounts:    account.NewService(pool, segundoFator, clk, cfg.IPHashKey),
 		Targets:     targets.NewService(pool, box, clk),
 		Incidents:   history.NewService(pool, clk),
 		Channels:    notify.NewChannelService(pool, box, clk, discord, email, cfg.PublicBaseURL),
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/account/ ./internal/httpapi/`
Expected: FAIL de compilação nos dois pacotes (`too many arguments in call to account.NewService`; em `internal/account`, também `c.svc.ChangePassword undefined`).

- [ ] **Step 3: Implementar**

Crie `internal/storage/migrations/00007_sessions_live.sql`:

```sql
-- +goose Up
-- As sessões vivas de um usuário: GET /sessions e a checagem de família viva
-- das rotas de credencial (Plano 5). Sem ele, sessions_user_idx lê todas as
-- linhas do usuário, inclusive as já rotacionadas, que passam de mil por
-- família em uso (uma a cada ~15 min, por até 37 dias antes da retenção).
CREATE INDEX sessions_user_live_idx ON sessions (user_id, family_id)
    WHERE used_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP INDEX sessions_user_live_idx;
```

Aplique em `internal/storage/queries/sessions.sql`:

```diff
--- a/internal/storage/queries/sessions.sql
+++ b/internal/storage/queries/sessions.sql
@@ -19,3 +19,19 @@
 -- name: RevokeAllFamilies :execrows
 UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
  WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;
+
+-- A família está viva: tem a linha corrente (não usada, não revogada, não
+-- vencida). $now é o relógio injetado, o mesmo que Rotate usa, nunca now().
+-- name: FamilyIsLive :one
+SELECT EXISTS (
+    SELECT 1 FROM sessions
+     WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)
+       AND used_at IS NULL AND revoked_at IS NULL
+       AND expires_at > sqlc.arg(now)::timestamptz
+);
+
+-- A troca de senha derruba as OUTRAS famílias do usuário e mantém a atual.
+-- name: RevokeOtherFamilies :execrows
+UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
+ WHERE user_id = sqlc.arg(user_id) AND family_id <> sqlc.arg(keep_family_id)
+   AND revoked_at IS NULL;
```

Aplique em `internal/storage/queries/users.sql`:

```diff
--- a/internal/storage/queries/users.sql
+++ b/internal/storage/queries/users.sql
@@ -27,3 +27,17 @@
 -- name: InsertAuditEntry :exec
 INSERT INTO audit_log (id, user_id, action, entity, entity_id, ip_hash, at)
 VALUES ($1, $2, $3, $4, $5, $6, $7);
+
+-- GET /me. totp_enabled é o segundo fator CONFIRMADO: um cadastro iniciado e
+-- abandonado não conta (o mesmo critério de auth.TOTP.IsEnabled).
+-- name: GetAccount :one
+SELECT id, email, (totp_confirmed_at IS NOT NULL)::bool AS totp_enabled, created_at
+  FROM users WHERE id = $1;
+
+-- A troca de senha pela API é um compare-and-swap: só grava se o hash ainda
+-- é o que foi verificado. Duas trocas simultâneas com a mesma senha atual não
+-- se atropelam, e a verificação (Argon2id) não precisa segurar trava. Zera o
+-- bloqueio: a senha e o segundo fator acabaram de ser provados.
+-- name: SwapUserHash :execrows
+UPDATE users SET password_hash = sqlc.arg(new_hash), failed_attempts = 0, locked_until = NULL
+ WHERE id = sqlc.arg(id) AND password_hash = sqlc.arg(old_hash);
```

Rode `sqlc generate`.

Aplique em `internal/account/account.go`:

```diff
--- a/internal/account/account.go
+++ b/internal/account/account.go
@@ -1,5 +1,6 @@
-// Package account é o serviço da conta do dono: criar e redefinir a senha
-// pelo CLI (Plano 5). O CLI e a API chamam o mesmo serviço, para que a
+// Package account é o serviço da conta do dono (Plano 5): criar e redefinir a
+// senha pelo CLI; ler a conta e trocar a senha pela API. O CLI e a API chamam
+// o mesmo serviço, para que a
 // política de senha e a normalização do e-mail sejam as mesmas nos dois (D20
 // do Plano 5).
 //
@@ -22,6 +23,7 @@
 
 	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/clock"
+	"github.com/BipoXDXD/vigilume/internal/crypto"
 	"github.com/BipoXDXD/vigilume/internal/storage/db"
 )
 
@@ -32,6 +34,13 @@
 	ErrUserNotFound = errors.New("usuário não cadastrado")
 	// ErrInvalidEmail: não é um endereço puro (sem nome de exibição).
 	ErrInvalidEmail = errors.New("e-mail inválido")
+	// ErrInvalidCredentials: senha atual errada, segundo fator errado ou
+	// ausente, conta bloqueada, ou a senha mudou no meio. Um erro só, pelo
+	// motivo do R34: distinguir os casos contaria ao atacante o que acertou.
+	ErrInvalidCredentials = errors.New("credenciais inválidas")
+	// ErrSessionGone: a família de sessão do token já não está viva (D11 do
+	// Plano 5), ou o usuário não existe mais.
+	ErrSessionGone = errors.New("sessão encerrada")
 )
 
 // PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
@@ -45,15 +54,165 @@
 const maxEmailLen = 254
 
 type Service struct {
-	pool *pgxpool.Pool
-	clk  clock.Clock
+	pool  *pgxpool.Pool
+	users auth.UserStore
+	totp  *auth.TOTP
+	clk   clock.Clock
+	ipKey []byte
 	// depoisDeContar existe para o teste de criação concorrente: roda entre a
 	// contagem de usuários e a inserção.
 	depoisDeContar func()
 }
 
-func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
-	return &Service{pool: pool, clk: clk}
+// NewService: totp e ipKey só servem à API (troca de senha). O CLI passa nil
+// nos dois, e ChangePassword recusa rodar sem eles.
+func NewService(pool *pgxpool.Pool, totp *auth.TOTP, clk clock.Clock, ipKey []byte) *Service {
+	return &Service{pool: pool, users: auth.NewPGStore(pool), totp: totp, clk: clk, ipKey: ipKey}
+}
+
+// Account é o que GET /me devolve.
+type Account struct {
+	ID          uuid.UUID
+	Email       string
+	TOTPEnabled bool
+	CreatedAt   time.Time
+}
+
+// Me devolve ErrUserNotFound se o usuário do token não existe mais.
+func (s *Service) Me(ctx context.Context, userID uuid.UUID) (Account, error) {
+	r, err := db.New(s.pool).GetAccount(ctx, userID)
+	if errors.Is(err, pgx.ErrNoRows) {
+		return Account{}, ErrUserNotFound
+	}
+	if err != nil {
+		return Account{}, fmt.Errorf("ler conta: %w", err)
+	}
+	return Account{ID: r.ID, Email: r.Email, TOTPEnabled: r.TotpEnabled, CreatedAt: r.CreatedAt}, nil
+}
+
+// FamilyIsLive diz se a família de sessão do token ainda está viva: tem a
+// linha corrente, não usada, não revogada e não vencida.
+func (s *Service) FamilyIsLive(ctx context.Context, userID, familyID uuid.UUID) (bool, error) {
+	return familyIsLive(ctx, db.New(s.pool), userID, familyID, s.clk.Now())
+}
+
+func familyIsLive(ctx context.Context, q *db.Queries, userID, familyID uuid.UUID, now time.Time) (bool, error) {
+	vivo, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
+	if err != nil {
+		return false, fmt.Errorf("conferir família de sessão: %w", err)
+	}
+	return vivo, nil
+}
+
+// ChangePasswordInput é o corpo de POST /auth/password.
+type ChangePasswordInput struct {
+	Current  string
+	New      string
+	TOTPCode string
+}
+
+// ChangePassword troca a senha pela API (D12 do Plano 5), na ordem:
+//
+//  1. a política da senha nova (pública: validá-la primeiro não vaza nada e
+//     não gasta Argon2 nem conta falha por erro de digitação na nova);
+//  2. a família do token viva (leitura barata; a verdade vem no passo 6);
+//  3. a senha atual, pela verificação do login (auth.CheckPassword): conta
+//     bloqueada recusa, senha errada conta no bloqueio;
+//  4. o segundo fator, se ligado, por auth.TOTP.Verify (aceita o código de
+//     recuperação); errado ou ausente conta no bloqueio (R40);
+//  5. o hash novo, fora da transação;
+//  6. a transação: trava do usuário (D1), família viva de novo, CAS do hash,
+//     as OUTRAS famílias revogadas, auditoria.
+//
+// Devolve PolicyError, ErrSessionGone ou ErrInvalidCredentials; nil quer
+// dizer "trocada, e a família atual continua viva".
+func (s *Service) ChangePassword(ctx context.Context, userID, familyID uuid.UUID, in ChangePasswordInput, meta auth.Meta) error {
+	if s.totp == nil || len(s.ipKey) == 0 {
+		return errors.New("account: ChangePassword sem segundo fator ou sem chave de IP")
+	}
+	if motivo := auth.ValidateNewPassword(in.New); motivo != "" {
+		return PolicyError{Reason: motivo}
+	}
+	now := s.clk.Now()
+
+	vivo, err := s.FamilyIsLive(ctx, userID, familyID)
+	if err != nil {
+		return err
+	}
+	if !vivo {
+		return ErrSessionGone
+	}
+	u, err := s.users.ByID(ctx, userID)
+	if errors.Is(err, auth.ErrNotFound) {
+		return ErrSessionGone
+	}
+	if err != nil {
+		return err
+	}
+
+	ok, _, err := auth.CheckPassword(ctx, s.users, u, in.Current, now)
+	if err != nil {
+		return err
+	}
+	if !ok {
+		return ErrInvalidCredentials
+	}
+
+	ligado, err := s.totp.IsEnabled(ctx, userID)
+	if err != nil {
+		return err
+	}
+	if ligado {
+		// O Verify consome o timestep (ou o código de recuperação) fora da
+		// transação abaixo: se ela desistir, quem tentar de novo espera o
+		// próximo código de 30 s.
+		err := s.totp.Verify(ctx, userID, in.TOTPCode, now)
+		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
+			if err := s.users.RecordFailure(ctx, userID, now); err != nil {
+				return err
+			}
+			return ErrInvalidCredentials
+		}
+		if err != nil {
+			return err
+		}
+	}
+
+	hash, err := auth.HashPassword(in.New)
+	if err != nil {
+		return err
+	}
+	ipHash := crypto.HashIP(s.ipKey, meta.IP)
+
+	return s.inTx(ctx, func(q *db.Queries) error {
+		if err := q.LockUserSessions(ctx, userID); err != nil {
+			return fmt.Errorf("travar sessões: %w", err)
+		}
+		// A família pode ter caído entre o passo 2 e a trava (um DELETE
+		// /sessions de outro aparelho, a detecção de reúso). Então nada muda:
+		// o 204 sempre quer dizer "trocada e a atual continua viva".
+		vivo, err := familyIsLive(ctx, q, userID, familyID, now)
+		if err != nil {
+			return err
+		}
+		if !vivo {
+			return ErrSessionGone
+		}
+		n, err := q.SwapUserHash(ctx, db.SwapUserHashParams{ID: userID, NewHash: hash, OldHash: u.PasswordHash})
+		if err != nil {
+			return fmt.Errorf("gravar senha: %w", err)
+		}
+		if n == 0 {
+			// A senha mudou depois de verificada: a "atual" já não é.
+			return ErrInvalidCredentials
+		}
+		if _, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{
+			UserID: userID, KeepFamilyID: familyID, Now: now,
+		}); err != nil {
+			return fmt.Errorf("revogar as outras sessões: %w", err)
+		}
+		return audit(ctx, q, userID, "user.password_changed", nil, nil, ipHash, now)
+	})
 }
 
 // ValidateEmail normaliza como o login (auth.NormalizeEmail) e exige um
```

Crie `internal/httpapi/accounthandlers.go`:

```go
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// AccountHandlers são as rotas da conta do dono (Plano 5): GET /me e POST
// /auth/password.
type AccountHandlers struct {
	svc            *account.Service
	trustedProxies []netip.Prefix
}

func NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix) *AccountHandlers {
	return &AccountHandlers{svc: svc, trustedProxies: trustedProxies}
}

// meResponse é construído campo a campo: nada de hash, bloqueio ou segredo.
type meResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// Me diz ao painel quem está logado e se o segundo fator está ligado (é o que
// decide se o formulário de troca de senha pede o código). Usuário que sumiu
// é 401: o painel volta ao login em vez de mostrar erro.
func (h *AccountHandlers) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	a, err := h.svc.Me(r.Context(), userID)
	if errors.Is(err, account.ErrUserNotFound) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	if err != nil {
		h.internal(w, r, "ler conta", err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, meResponse{
		ID: a.ID.String(), Email: a.Email, TOTPEnabled: a.TOTPEnabled, CreatedAt: a.CreatedAt.UTC(),
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	TOTPCode        string `json:"totp_code"`
}

// ChangePassword é POST /auth/password: 204, e as OUTRAS sessões encerradas.
//
// Os erros seguem o contrato dos Planos 5–7: senha nova fora da política é 400
// com o campo; senha atual, segundo fator ou bloqueio é o 401 genérico do
// login (R34); a sessão do token já encerrada é 401 unauthorized, que o painel
// trata renovando (e a renovação falha, levando ao login).
func (h *AccountHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.svc.ChangePassword(r.Context(), userID, familyID, account.ChangePasswordInput{
		Current: req.CurrentPassword, New: req.NewPassword, TOTPCode: req.TOTPCode,
	}, auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()})
	var pe account.PolicyError
	switch {
	case errors.As(err, &pe):
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "new_password", Reason: pe.Reason}})
	case errors.Is(err, account.ErrSessionGone):
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case errors.Is(err, account.ErrInvalidCredentials):
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
	case err != nil:
		h.internal(w, r, "trocar senha", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// internal registra o erro real e devolve só o envelope genérico com o
// trace_id (spec §9.1).
func (h *AccountHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
```

Aplique em `internal/httpapi/authhandlers.go`:

```diff
--- a/internal/httpapi/authhandlers.go
+++ b/internal/httpapi/authhandlers.go
@@ -178,15 +178,6 @@
 	w.WriteHeader(http.StatusNoContent)
 }
 
-func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
-	userID, ok := middleware.UserID(r.Context())
-	if !ok {
-		apierr.WriteError(w, r, apierr.CodeUnauthorized)
-		return
-	}
-	apierr.WriteJSON(w, http.StatusOK, map[string]string{"id": userID.String()})
-}
-
 func (h *AuthHandlers) issue(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
 	token, s, err := h.sessions.Issue(r.Context(), userID, h.meta(r))
 	if err != nil {
```

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -7,6 +7,7 @@
 	"strings"
 	"time"
 
+	"github.com/BipoXDXD/vigilume/internal/account"
 	"github.com/BipoXDXD/vigilume/internal/apierr"
 	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/clock"
@@ -35,9 +36,11 @@
 // Deps reúne o que as rotas consomem. Virou struct quando o sétimo
 // parâmetro chegou: a lista posicional já não se lia.
 type Deps struct {
-	Store     *auth.PGStore
-	Sessions  *auth.Manager
-	TOTP      *auth.TOTP
+	Store    *auth.PGStore
+	Sessions *auth.Manager
+	TOTP     *auth.TOTP
+	// Accounts é a conta do dono: GET /me e POST /auth/password (Plano 5).
+	Accounts  *account.Service
 	Targets   *targets.Service
 	Incidents *history.Service
 	Channels  *notify.ChannelService
@@ -62,6 +65,7 @@
 	ih := NewIncidentHandlers(d.Incidents)
 	ch := NewChannelHandlers(d.Channels)
 	sp := NewStatusPageHandlers(d.StatusPages, cfg.PublicBaseURL)
+	ah := NewAccountHandlers(d.Accounts, cfg.TrustedProxies)
 	authn := middleware.Authn(cfg.JWTSecret, d.Clock)
 
 	mux := http.NewServeMux()
@@ -81,8 +85,14 @@
 	// Sem isso, o próprio lockout viraria o ataque — negação de serviço por
 	// bloqueio de conta alheia, que é um problema real em sistemas com
 	// lockout. Não afrouxe este limitador sem pensar nisso.
-	mux.Handle("POST /api/v1/auth/login",
-		middleware.RateLimit(ctx, 10, 5, cfg.TrustedProxies)(http.HandlerFunc(h.Login)))
+	//
+	// Uma instância só, e não uma por rota: a troca de senha divide o balde do
+	// login (D5 do Plano 5; contrato dos Planos 5–7, "mesmo limite por IP da
+	// rota de login"). Com instâncias separadas, quem adivinha senha dobraria
+	// o orçamento alternando as duas rotas.
+	limiteLogin := middleware.RateLimit(ctx, 10, 5, cfg.TrustedProxies)
+	mux.Handle("POST /api/v1/auth/login", limiteLogin(http.HandlerFunc(h.Login)))
+	mux.Handle("POST /api/v1/auth/password", limiteLogin(authn(http.HandlerFunc(ah.ChangePassword))))
 	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
 	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(h.Logout))
 
@@ -98,8 +108,6 @@
 	mux.Handle("POST /api/v1/auth/totp/verify",
 		middleware.RateLimit(ctx, 20, 5, cfg.TrustedProxies)(http.HandlerFunc(h.TOTPVerify)))
 
-	mux.Handle("GET /api/v1/me", authn(http.HandlerFunc(h.Me)))
-
 	// Rotas de recurso: Authn primeiro, depois o limite por USUÁRIO (spec
 	// §5.7), que precisa do usuário que Authn põe no contexto. Uma instância
 	// só, compartilhada pelas rotas: o orçamento é do usuário, não da rota.
@@ -110,6 +118,8 @@
 	porUsuario := middleware.RateLimitUser(ctx, 60, 20)
 	protegida := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(fn)) }
 
+	mux.Handle("GET /api/v1/me", protegida(ah.Me))
+
 	mux.Handle("GET /api/v1/targets", protegida(th.List))
 	mux.Handle("POST /api/v1/targets", protegida(th.Create))
 	mux.Handle("GET /api/v1/targets/{id}", protegida(th.Get))
```

Aplique em `cmd/vigilume/main.go`:

```diff
--- a/cmd/vigilume/main.go
+++ b/cmd/vigilume/main.go
@@ -14,6 +14,7 @@
 
 	"golang.org/x/sync/errgroup"
 
+	"github.com/BipoXDXD/vigilume/internal/account"
 	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/clock"
 	"github.com/BipoXDXD/vigilume/internal/config"
@@ -156,10 +157,12 @@
 
 	if cfg.Role.RunsAPI() {
 		store := auth.NewPGStore(pool)
+		segundoFator := auth.NewTOTP(pool, box, totpIssuer)
 		deps := httpapi.Deps{
 			Store:       store,
 			Sessions:    auth.NewManager(store, clk, cfg.IPHashKey),
-			TOTP:        auth.NewTOTP(pool, box, totpIssuer),
+			TOTP:        segundoFator,
+			Accounts:    account.NewService(pool, segundoFator, clk, cfg.IPHashKey),
 			Targets:     targets.NewService(pool, box, clk),
 			Incidents:   history.NewService(pool, clk),
 			Channels:    notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg), cfg.PublicBaseURL),
```

Aplique em `cmd/vigilume/user.go`:

```diff
--- a/cmd/vigilume/user.go
+++ b/cmd/vigilume/user.go
@@ -105,7 +105,8 @@
 	if err := storage.Migrate(ctx, dsn); err != nil {
 		return falhar(saidaAmbiente, "%v", err)
 	}
-	svc := account.NewService(pool, clock.Real{})
+	// Sem segundo fator e sem chave de IP: o CLI não troca senha pela API.
+	svc := account.NewService(pool, nil, clock.Real{}, nil)
 
 	// A pré-condição vem ANTES da senha: ninguém digita duas vezes à toa.
 	switch sub {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go test -race -count=3 ./internal/account/ && go test -race -count=1 ./internal/httpapi/ ./cmd/...`
Expected: PASS. Rode também `go test -count=5 -run TestChangePasswordSpendsTheSameCPU ./internal/httpapi/`: é teste de tempo, e cinco verdes seguidos são o mínimo.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, com `sqlc generate`):
1. Em `users.sql.go`, troque `totp_confirmed_at IS NOT NULL` por `totp_secret_enc IS NOT NULL` em `getAccount`. Run: `go test -count=1 -run TestMe ./internal/httpapi/`. Expected: FAIL, `totp_enabled ligou com o cadastro só iniciado`.
2. Em `sessions.sql.go`, troque `family_id <> $3` por `$3::uuid IS NOT NULL` em `revokeOtherFamilies`. Run: `go test -count=1 -run TestChangePasswordKeeps ./internal/httpapi/`. Expected: FAIL, `a sessão atual caiu junto`.
3. No mesmo SQL, troque `user_id = $2` por `$2::uuid IS NOT NULL`. Mesmo comando. Expected: FAIL, `a sessão de outro usuário caiu`.
4. Em `ChangePassword`, troque a chamada de `q.RevokeOtherFamilies` por uma função que devolve `0, nil`. Mesmo comando. Expected: FAIL, `uma sessão de outro aparelho sobreviveu`.
5. Em `ChangePassword`, tire o `q.LockUserSessions`. Run: `go test -count=1 -run 'TestChangePasswordTakes|TestChangePasswordRechecks' ./internal/account/`. Expected: FAIL, `a troca não esperou a trava do usuário (terminou com <nil>)`.
6. Troque a segunda checagem (`familyIsLive(ctx, q, …)`) por `true, error(nil)`. Run: `go test -count=1 -run TestChangePasswordRechecks ./internal/account/`. Expected: FAIL, `erro = <nil>, queria ErrSessionGone`.
7. Em `users.sql.go`, troque `AND password_hash = $3` por `AND $3::text IS NOT NULL` em `swapUserHash`. Run: `go test -count=1 -run TestChangePasswordLoses ./internal/account/`. Expected: FAIL, `erro = <nil>, queria ErrInvalidCredentials`.
8. Mova a validação da política para depois do `CheckPassword`. Run: `go test -count=1 -run TestChangePasswordPolicy ./internal/httpapi/`. Expected: FAIL (`401 invalid_credentials` no lugar do `400`; `2 falhas contadas`).
9. Troque `auth.CheckPassword(…)` por `auth.VerifyPassword(u.PasswordHash, in.Current)`. Run: `go test -count=1 -run 'TestChangePasswordWrong|TestChangePasswordLocked|TestChangePasswordSpends' ./internal/httpapi/`. Expected: FAIL (`0 falhas contadas`; `204` com a conta bloqueada).
10. Em `CheckPassword` (Task 3), tire o `VerifyDummy()` do caminho bloqueado. Run: `go test -count=1 -run TestChangePasswordSpends ./internal/httpapi/`. Expected: FAIL, `conta bloqueada custou ~0,7 ms contra ~65 ms da verificação real`.
11. Troque `if ligado {` por `if ligado && false {`. Run: `go test -count=1 -run TestChangePasswordRequiresTOTP ./internal/httpapi/`. Expected: FAIL, `sem código: 204 …`.
12. No ramo do TOTP errado, devolva `ErrSessionGone` em vez de `ErrInvalidCredentials`. Mesmo comando. Expected: FAIL, `sem código: 401 unauthorized; queria 401 invalid_credentials` (D24).
13. Em `routes.go`, dê ao `/auth/password` um `middleware.RateLimit(ctx, 10, 5, cfg.TrustedProxies)` próprio. Run: `go test -count=1 -run TestChangePasswordShares ./internal/httpapi/`. Expected: FAIL, `status 401, queria 429`.

14. Em `ResetPassword` (Task 4), tire o `q.LockUserSessions`. Run: `go test -count=1 -run TestResetPasswordTakes ./internal/account/`. Expected: FAIL, `a redefinição não esperou a trava do usuário`. (O teste mora aqui, e não na Task 4, porque os helpers de trava nascem nesta tarefa.)

Registre também a guarda dupla: tirar só a primeira checagem de família deixa `TestChangePasswordDeadFamily` verde (a do passo 6 cobre); é esperado e está comentado no teste.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/account/ internal/httpapi/ cmd/vigilume/
git commit -m "feat(api): GET /me enriquecido e POST /auth/password"
```

---

### Task 6: `GET /sessions` e `DELETE /sessions/{id}` (D11, D13, D14)

**Files:**
- Modify: `internal/storage/queries/sessions.sql` (`ListLiveFamilies`, `RevokeFamilyOfUser`)
- Modify: `internal/account/account.go` (`Session`, `ListSessions`, `RevokeSession`, `ErrSessionNotFound`)
- Modify: `internal/httpapi/accounthandlers.go` (`sessaoViva`, `ListSessions`, `RevokeSession`; o construtor ganha `secureCookie`)
- Modify: `internal/httpapi/authhandlers.go` (`apagarCookieDeRefresh`, que o logout e o `DELETE` da própria sessão dividem), `internal/httpapi/routes.go`
- Test: `internal/httpapi/sessions_integration_test.go` (novo), `internal/account/account_test.go`

**Interfaces:**
- Consumes: `familyIsLive`, `FamilyIsLive`, `audit`, `inTx` (Tasks 4–5); `middleware.FamilyID` (Task 2); `pathID` (já existe em `targethandlers.go`: id malformado é 404); `entrar`, `getComToken`, `renova`, `codigoDoErro`, `accessTokenDaSessao`, `segurarTrava`, `esperarAlguemNaTrava` (Task 5); `outroUsuario` (já existe).
- Produces:
  - `type account.Session struct { ID uuid.UUID; CreatedAt, LastUsedAt time.Time; UserAgent string; Current bool }`.
  - `(*Service) ListSessions(ctx, userID, current uuid.UUID) ([]Session, error)`; `(*Service) RevokeSession(ctx, userID, familyID uuid.UUID, meta auth.Meta) error` (`ErrSessionNotFound`).
  - `httpapi.NewAccountHandlers(svc, trustedProxies, secureCookie bool)`.
  - `apagarCookieDeRefresh(w http.ResponseWriter, secure bool)`.

**Contexto para quem implementa.** Uma família "existe" para a API exatamente quando aparece em `GET /sessions` (D14): viva, do usuário, não vencida. `DELETE` de qualquer outra coisa é `404`, inclusive o segundo clique; o painel trata `404` como "já encerrada". Encerrar a **própria** sessão (`id` = `sid` do token) é permitido e a resposta apaga o cookie de refresh: o navegador aceita apagar um cookie de outro `Path` a partir de `/api/v1/sessions/…` (RFC 6265 §5.2.4 não valida o `Path` contra a requisição). O access token em mãos ainda vale nas rotas de recurso até expirar; descartá-lo é trabalho do painel (`PENDENCIAS-PARA-O-PLANO-6.md`).

As duas rotas exigem a família do token viva (D11, `sessaoViva`): sem isso, um invasor cuja sessão o dono encerrou continuaria 15 minutos listando e derrubando as sessões do dono.

`created_at` da família vem do **UUIDv7** do `family_id` (`id.Time().UnixTime()`), e não do menor `created_at` das linhas: a retenção apaga linhas antigas de uma família que continua viva. `last_used_at` é o `created_at` da linha viva, isto é, o último refresh. Nos testes, `sessions.created_at` usa o `now()` do banco e o UUIDv7 usa o relógio real, e nenhum dos dois é o relógio falso: os testes de tempo comparam com `time.Now()` com folga de 1 s, e usam `time.Sleep(5 ms)` entre as famílias porque o UUIDv7 tem resolução de milissegundo. A ordem é do login (`created_at`), e não do último uso: uma ordem que muda a cada refresh faria a linha trocar de lugar embaixo do clique.

O filtro `used_at IS NULL` e a dedupe por família (fica a linha mais nova) são duas guardas (D23): tirar só o filtro deixa os testes verdes.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/httpapi/sessions_integration_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

type sessaoItem struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

func listarSessoes(t *testing.T, a *api, token string) []sessaoItem {
	t.Helper()
	resp := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions devolveu %d", resp.StatusCode)
	}
	var corpo struct {
		Items []sessaoItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatal(err)
	}
	return corpo.Items
}

func deleteComToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "vigilume")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// gerente abre famílias direto pelo Manager, sem passar pelo login e seu
// balde de 5 por IP.
func gerente(a *api) *auth.Manager {
	return auth.NewManager(auth.NewPGStore(a.pool), a.clk, testIPHashKey)
}

func familiaDe(t *testing.T, token string, a *api) uuid.UUID {
	t.Helper()
	c, err := auth.ParseAccessToken(testJWTSecret, token, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c.FamilyID
}

func ids(itens []sessaoItem) []string {
	out := make([]string, 0, len(itens))
	for _, i := range itens {
		out = append(out, i.ID)
	}
	return out
}

// Só famílias vivas: a revogada, a vencida e a de outro usuário não aparecem;
// só a do token é current.
func TestListSessionsShowsOnlyLiveFamilies(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	atual := familiaDe(t, token, a)
	mgr := gerente(a)
	ctx := context.Background()
	meta := auth.Meta{IP: "203.0.113.9", UserAgent: "outro aparelho"}

	_, viva, err := mgr.Issue(ctx, a.userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	revogada, _, err := mgr.Issue(ctx, a.userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Revoke(ctx, revogada); err != nil {
		t.Fatal(err)
	}
	_, vencida, err := mgr.Issue(ctx, a.userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE family_id = $1`,
		vencida.FamilyID, a.clk.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Issue(ctx, outroUsuario(t, a), meta); err != nil {
		t.Fatal(err)
	}

	itens := listarSessoes(t, a, token)
	if len(itens) != 2 {
		t.Fatalf("sessões = %v, queria só a atual e a viva", ids(itens))
	}
	vistas := map[string]bool{}
	for _, i := range itens {
		vistas[i.ID] = i.Current
	}
	if cur, ok := vistas[atual.String()]; !ok || !cur {
		t.Errorf("a sessão do token não veio como current: %+v", itens)
	}
	if cur, ok := vistas[viva.FamilyID.String()]; !ok || cur {
		t.Errorf("a outra sessão viva faltou ou veio como current: %+v", itens)
	}
}

// Nada de IP, hash, vencimento nem id de linha: as chaves são exatamente as do
// contrato, e os bytes gravados de ip_hash e token_hash não aparecem.
func TestListSessionsNeverExposesIPOrHash(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	resp := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var cru struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(corpo, &cru); err != nil {
		t.Fatal(err)
	}
	if len(cru.Items) != 1 {
		t.Fatalf("%d itens, queria 1", len(cru.Items))
	}
	permitidas := map[string]bool{"id": true, "created_at": true, "last_used_at": true, "user_agent": true, "current": true}
	for chave := range cru.Items[0] {
		if !permitidas[chave] {
			t.Errorf("chave %q fora do contrato na resposta", chave)
		}
	}

	var ipHash, tokenHash []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT ip_hash, token_hash FROM sessions WHERE user_id = $1`, a.userID).Scan(&ipHash, &tokenHash); err != nil {
		t.Fatal(err)
	}
	for _, segredo := range [][]byte{ipHash, tokenHash} {
		for _, forma := range []string{hex.EncodeToString(segredo), base64.StdEncoding.EncodeToString(segredo),
			base64.RawURLEncoding.EncodeToString(segredo)} {
			if bytes.Contains(corpo, []byte(forma)) {
				t.Errorf("um hash gravado apareceu na resposta: %s", forma)
			}
		}
	}
	if bytes.Contains(corpo, []byte("127.0.0.1")) {
		t.Error("o IP apareceu na resposta")
	}
}

// A ordem é a do login (created_at, o UUIDv7 da família), e não a do último
// uso: renovar a sessão mais antiga não a traz para o topo. last_used_at é o
// instante do último refresh.
func TestListSessionsOrderAndTimes(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	primeira := familiaDe(t, token, a)
	mgr := gerente(a)
	ctx := context.Background()

	var familias []uuid.UUID
	for range 2 {
		time.Sleep(5 * time.Millisecond) // o UUIDv7 tem resolução de milissegundo
		antes := time.Now()
		_, s, err := mgr.Issue(ctx, a.userID, auth.Meta{IP: "203.0.113.9"})
		if err != nil {
			t.Fatal(err)
		}
		depois := time.Now()
		seg, nseg := s.FamilyID.Time().UnixTime()
		if criada := time.Unix(seg, nseg); criada.Before(antes.Truncate(time.Millisecond)) || criada.After(depois) {
			t.Fatalf("o UUIDv7 da família (%v) está fora do login (%v a %v)", criada, antes, depois)
		}
		familias = append(familias, s.FamilyID)
	}

	time.Sleep(5 * time.Millisecond)
	antesDoRefresh := time.Now()
	novo := accessTokenDaSessao(t, a, c) // renova a PRIMEIRA

	itens := listarSessoes(t, a, novo)
	queria := []string{familias[1].String(), familias[0].String(), primeira.String()}
	if got := ids(itens); strings.Join(got, ",") != strings.Join(queria, ",") {
		t.Fatalf("ordem = %v, queria %v (login mais novo primeiro)", got, queria)
	}
	if !itens[0].CreatedAt.After(itens[1].CreatedAt) || !itens[1].CreatedAt.After(itens[2].CreatedAt) {
		t.Errorf("created_at fora de ordem: %+v", itens)
	}
	// O refresh foi agora: last_used_at da primeira passa o das outras.
	// Folga de 1 s entre o relógio do banco e o do processo.
	if itens[2].LastUsedAt.Before(antesDoRefresh.Add(-time.Second)) || !itens[2].LastUsedAt.After(itens[0].LastUsedAt) {
		t.Errorf("last_used_at da sessão renovada = %v; queria o instante do refresh (%v)", itens[2].LastUsedAt, antesDoRefresh)
	}
}

// O user agent é cortado em 256 code points, sem partir caractere.
func TestListSessionsTruncatesUserAgent(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	if _, _, err := gerente(a).Issue(context.Background(), a.userID,
		auth.Meta{IP: "203.0.113.9", UserAgent: strings.Repeat("é", 300)}); err != nil {
		t.Fatal(err)
	}
	for _, i := range listarSessoes(t, a, token) {
		if i.Current {
			continue
		}
		if n := utf8.RuneCountInString(i.UserAgent); n != 256 || !utf8.ValidString(i.UserAgent) {
			t.Errorf("user_agent com %d code points (UTF-8 válido: %v), queria 256", n, utf8.ValidString(i.UserAgent))
		}
	}
}

// Sessão de outro usuário é 404, e continua viva: a prova é o refresh dela.
func TestRevokeSessionOfAnotherUserIs404(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	mgr := gerente(a)
	meta := auth.Meta{IP: "203.0.113.9"}
	doOutro, s, err := mgr.Issue(context.Background(), outroUsuario(t, a), meta)
	if err != nil {
		t.Fatal(err)
	}

	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE da sessão alheia devolveu %d, queria 404", resp.StatusCode)
	}
	if _, _, err := mgr.Rotate(context.Background(), doOutro, meta); err != nil {
		t.Errorf("a sessão do outro usuário caiu: %v", err)
	}
}

func TestRevokeSessionTwiceIs404(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	doOutroAparelho, s, err := gerente(a).Issue(context.Background(), a.userID, auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	url := a.srv.URL + "/api/v1/sessions/" + s.FamilyID.String()

	primeiro := deleteComToken(t, url, token)
	primeiro.Body.Close()
	segundo := deleteComToken(t, url, token)
	segundo.Body.Close()
	if primeiro.StatusCode != http.StatusNoContent || segundo.StatusCode != http.StatusNotFound {
		t.Fatalf("primeiro %d, segundo %d; queria 204 e 404", primeiro.StatusCode, segundo.StatusCode)
	}
	if _, _, err := gerente(a).Rotate(context.Background(), doOutroAparelho, auth.Meta{IP: "203.0.113.9"}); err == nil {
		t.Error("a sessão encerrada ainda renova")
	}
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'session.revoked' AND entity = 'session_family' AND entity_id = $1 AND ip_hash IS NOT NULL`,
		s.FamilyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d registros session.revoked, queria 1", n)
	}

	lixo := deleteComToken(t, a.srv.URL+"/api/v1/sessions/nao-e-uuid", token)
	lixo.Body.Close()
	if lixo.StatusCode != http.StatusNotFound {
		t.Errorf("id malformado devolveu %d, queria 404", lixo.StatusCode)
	}
}

// Encerrar a própria sessão é o logout daquela família: 204 e o cookie de
// refresh apagado (Path=/api/v1/auth, vindo de /api/v1/sessions).
func TestRevokeCurrentSessionClearsCookie(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)

	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+familiaDe(t, token, a).String(), token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, queria 204", resp.StatusCode)
	}
	ck := refreshCookie(resp)
	if ck == nil || ck.Value != "" || ck.MaxAge >= 0 || ck.Path != "/api/v1/auth" {
		t.Fatalf("cookie = %+v; queria vigilume_refresh vazio, vencido, Path=/api/v1/auth", ck)
	}
	if renova(t, a, c) {
		t.Error("a sessão encerrada ainda renova")
	}
}

func TestRevokeOtherSessionKeepsCookie(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	_, s, err := gerente(a).Issue(context.Background(), a.userID, auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, queria 204", resp.StatusCode)
	}
	if len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("encerrar OUTRA sessão mexeu no cookie: %v", resp.Header.Values("Set-Cookie"))
	}
}

// Um token de sessão já encerrada não lista nem derruba sessões (D11): sem
// isso, o invasor encerrado continuaria 15 minutos derrubando o dono.
func TestSessionRoutesRejectDeadFamily(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	mgr := gerente(a)
	meta := auth.Meta{IP: "203.0.113.9"}
	doDono, s, err := mgr.Issue(context.Background(), a.userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()

	lista := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer lista.Body.Close()
	if lista.StatusCode != http.StatusUnauthorized || codigoDoErro(t, lista) != "unauthorized" {
		t.Errorf("GET /sessions com a família morta devolveu %d", lista.StatusCode)
	}
	del := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusUnauthorized || codigoDoErro(t, del) != "unauthorized" {
		t.Errorf("DELETE com a família morta devolveu %d", del.StatusCode)
	}
	if _, _, err := mgr.Rotate(context.Background(), doDono, meta); err != nil {
		t.Errorf("o token morto derrubou a sessão do dono: %v", err)
	}
}
```

Aplique em `internal/account/account_test.go`:

```diff
--- a/internal/account/account_test.go
+++ b/internal/account/account_test.go
@@ -280,3 +280,23 @@
 		t.Error("a troca que perdeu gravou por cima da que ganhou")
 	}
 }
+
+// Encerrar uma sessão também toma a trava do usuário (D1): é o que impede o
+// sucessor de uma rotação em curso de escapar da revogação.
+func TestRevokeSessionTakesTheUserLock(t *testing.T) {
+	c := montar(t)
+	soltar := segurarTrava(t, c.pool, c.user)
+	fim := make(chan error, 1)
+	go func() {
+		fim <- c.svc.RevokeSession(context.Background(), c.user, c.atual, auth.Meta{IP: "203.0.113.9"})
+	}()
+
+	if !esperarAlguemNaTrava(t, c.pool) {
+		soltar()
+		t.Fatalf("o encerramento não esperou a trava do usuário (terminou com %v)", <-fim)
+	}
+	soltar()
+	if err := <-fim; err != nil {
+		t.Fatalf("o encerramento falhou depois de soltar a trava: %v", err)
+	}
+}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/account/ ./internal/httpapi/`
Expected: `internal/account` FAIL de compilação (`c.svc.RevokeSession undefined`); `internal/httpapi` FAIL em todos os testes de sessão (`GET /sessions devolveu 404`).

- [ ] **Step 3: Implementar**

Aplique em `internal/storage/queries/sessions.sql`:

```diff
--- a/internal/storage/queries/sessions.sql
+++ b/internal/storage/queries/sessions.sql
@@ -35,3 +35,18 @@
 UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
  WHERE user_id = sqlc.arg(user_id) AND family_id <> sqlc.arg(keep_family_id)
    AND revoked_at IS NULL;
+
+-- GET /sessions: a linha corrente de cada família viva do usuário. O
+-- created_at dela é o do último refresh (cada rotação insere uma linha).
+-- name: ListLiveFamilies :many
+SELECT family_id, created_at, coalesce(user_agent, '')::text AS user_agent
+  FROM sessions
+ WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL AND revoked_at IS NULL
+   AND expires_at > sqlc.arg(now)::timestamptz;
+
+-- DELETE /sessions/{id}: todas as linhas não revogadas da família, como o
+-- RevokeFamily do auth, e só se ela for deste usuário.
+-- name: RevokeFamilyOfUser :execrows
+UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
+ WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)
+   AND revoked_at IS NULL;
```

Rode `sqlc generate`.

Aplique em `internal/account/account.go`:

```diff
--- a/internal/account/account.go
+++ b/internal/account/account.go
@@ -14,7 +14,9 @@
 	"errors"
 	"fmt"
 	"net/mail"
+	"slices"
 	"time"
+	"unicode/utf8"
 
 	"github.com/google/uuid"
 	"github.com/jackc/pgx/v5"
@@ -41,6 +43,9 @@
 	// ErrSessionGone: a família de sessão do token já não está viva (D11 do
 	// Plano 5), ou o usuário não existe mais.
 	ErrSessionGone = errors.New("sessão encerrada")
+	// ErrSessionNotFound: a família pedida não aparece em GET /sessions —
+	// inexistente, de outro usuário, já revogada ou vencida (D14).
+	ErrSessionNotFound = errors.New("sessão não encontrada")
 )
 
 // PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
@@ -350,6 +355,107 @@
 	return revogadas, err
 }
 
+// Session é uma família de refresh viva, como GET /sessions a mostra (D13 do
+// Plano 5). Não tem IP nem hash: não há campo onde eles caibam.
+type Session struct {
+	// ID é a família (family_id), o mesmo id do claim sid.
+	ID uuid.UUID
+	// CreatedAt é o login que abriu a família: o instante do UUIDv7 dela. O
+	// menor created_at das linhas não serve: a retenção apaga as linhas
+	// antigas de uma família que continua viva, e ele andaria para a frente.
+	CreatedAt time.Time
+	// LastUsedAt é o último refresh (granularidade de ~15 min num painel ativo).
+	LastUsedAt time.Time
+	// UserAgent é o do último refresh, cortado em maxUserAgent code points.
+	// É texto de quem fez o login, inclusive de um invasor.
+	UserAgent string
+	Current   bool
+}
+
+const maxUserAgent = 256
+
+// ListSessions devolve as famílias vivas do usuário, da mais nova para a mais
+// antiga pelo login (created_at), e não pelo último uso: uma ordem que muda a
+// cada refresh faria a linha trocar de lugar embaixo do clique de "encerrar".
+func (s *Service) ListSessions(ctx context.Context, userID, current uuid.UUID) ([]Session, error) {
+	rows, err := db.New(s.pool).ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: userID, Now: s.clk.Now()})
+	if err != nil {
+		return nil, fmt.Errorf("listar sessões: %w", err)
+	}
+	// Uma linha viva por família é invariante de Rotate (marca a antiga e
+	// insere a nova na mesma transação). Não depender dela custa pouco: fica a
+	// linha mais recente.
+	porFamilia := map[uuid.UUID]db.ListLiveFamiliesRow{}
+	for _, r := range rows {
+		if atual, ok := porFamilia[r.FamilyID]; !ok || r.CreatedAt.After(atual.CreatedAt) {
+			porFamilia[r.FamilyID] = r
+		}
+	}
+	out := make([]Session, 0, len(porFamilia))
+	for id, r := range porFamilia {
+		seg, nseg := id.Time().UnixTime()
+		out = append(out, Session{
+			ID: id, CreatedAt: time.Unix(seg, nseg).UTC(), LastUsedAt: r.CreatedAt.UTC(),
+			UserAgent: cortar(r.UserAgent, maxUserAgent), Current: id == current,
+		})
+	}
+	slices.SortFunc(out, func(a, b Session) int {
+		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
+			return c
+		}
+		return slices.Compare(b.ID[:], a.ID[:])
+	})
+	return out, nil
+}
+
+// cortar corta em n code points sem partir um caractere ao meio.
+func cortar(s string, n int) string {
+	if utf8.RuneCountInString(s) <= n {
+		return s
+	}
+	i := 0
+	for j := range s {
+		if i == n {
+			return s[:j]
+		}
+		i++
+	}
+	return s
+}
+
+// RevokeSession encerra a família pedida (DELETE /sessions/{id}). Só se ela
+// estiver viva E for do usuário; qualquer outra coisa é ErrSessionNotFound
+// (D14): a família "existe" para a API exatamente quando aparece em GET
+// /sessions.
+func (s *Service) RevokeSession(ctx context.Context, userID, familyID uuid.UUID, meta auth.Meta) error {
+	if len(s.ipKey) == 0 {
+		return errors.New("account: RevokeSession sem chave de IP")
+	}
+	now := s.clk.Now()
+	ipHash := crypto.HashIP(s.ipKey, meta.IP)
+	return s.inTx(ctx, func(q *db.Queries) error {
+		// A trava do usuário antes de tocar em sessions (D1): sem ela, uma
+		// rotação em curso da família deixaria o sucessor vivo.
+		if err := q.LockUserSessions(ctx, userID); err != nil {
+			return fmt.Errorf("travar sessões: %w", err)
+		}
+		vivo, err := familyIsLive(ctx, q, userID, familyID, now)
+		if err != nil {
+			return err
+		}
+		if !vivo {
+			return ErrSessionNotFound
+		}
+		if _, err := q.RevokeFamilyOfUser(ctx, db.RevokeFamilyOfUserParams{
+			UserID: userID, FamilyID: familyID, Now: now,
+		}); err != nil {
+			return fmt.Errorf("revogar sessão: %w", err)
+		}
+		entidade := "session_family"
+		return audit(ctx, q, userID, "session.revoked", &entidade, &familyID, ipHash, now)
+	})
+}
+
 func (s *Service) inTx(ctx context.Context, fn func(*db.Queries) error) error {
 	tx, err := s.pool.Begin(ctx)
 	if err != nil {
```

Aplique em `internal/httpapi/accounthandlers.go`:

```diff
--- a/internal/httpapi/accounthandlers.go
+++ b/internal/httpapi/accounthandlers.go
@@ -7,6 +7,8 @@
 	"net/netip"
 	"time"
 
+	"github.com/google/uuid"
+
 	"github.com/BipoXDXD/vigilume/internal/account"
 	"github.com/BipoXDXD/vigilume/internal/apierr"
 	"github.com/BipoXDXD/vigilume/internal/auth"
@@ -14,15 +16,18 @@
 	"github.com/BipoXDXD/vigilume/internal/observability"
 )
 
-// AccountHandlers são as rotas da conta do dono (Plano 5): GET /me e POST
-// /auth/password.
+// AccountHandlers são as rotas da conta do dono (Plano 5): GET /me, POST
+// /auth/password, GET /sessions e DELETE /sessions/{id}.
 type AccountHandlers struct {
 	svc            *account.Service
 	trustedProxies []netip.Prefix
+	// secureCookie: o mesmo critério do AuthHandlers, para apagar o cookie de
+	// refresh quando a sessão encerrada é a atual.
+	secureCookie bool
 }
 
-func NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix) *AccountHandlers {
-	return &AccountHandlers{svc: svc, trustedProxies: trustedProxies}
+func NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix, secureCookie bool) *AccountHandlers {
+	return &AccountHandlers{svc: svc, trustedProxies: trustedProxies, secureCookie: secureCookie}
 }
 
 // meResponse é construído campo a campo: nada de hash, bloqueio ou segredo.
@@ -98,6 +103,93 @@
 	}
 }
 
+type sessionResponse struct {
+	ID         string    `json:"id"`
+	CreatedAt  time.Time `json:"created_at"`
+	LastUsedAt time.Time `json:"last_used_at"`
+	UserAgent  string    `json:"user_agent"`
+	Current    bool      `json:"current"`
+}
+
+type sessionListResponse struct {
+	Items []sessionResponse `json:"items"`
+}
+
+// sessaoViva é a guarda das rotas de sessão (D11 do Plano 5): o token tem de
+// vir de uma família ainda viva. Sem ela, um access token de uma sessão já
+// encerrada — o do invasor, depois de o dono encerrá-la — continuaria por até
+// 15 minutos listando e derrubando as sessões do dono.
+func (h *AccountHandlers) sessaoViva(w http.ResponseWriter, r *http.Request) (userID, familyID uuid.UUID, ok bool) {
+	userID, okU := middleware.UserID(r.Context())
+	familyID, okF := middleware.FamilyID(r.Context())
+	if !okU || !okF {
+		apierr.WriteError(w, r, apierr.CodeUnauthorized)
+		return uuid.Nil, uuid.Nil, false
+	}
+	vivo, err := h.svc.FamilyIsLive(r.Context(), userID, familyID)
+	if err != nil {
+		h.internal(w, r, "conferir sessão", err)
+		return uuid.Nil, uuid.Nil, false
+	}
+	if !vivo {
+		apierr.WriteError(w, r, apierr.CodeUnauthorized)
+		return uuid.Nil, uuid.Nil, false
+	}
+	return userID, familyID, true
+}
+
+// ListSessions é GET /sessions. O DTO é construído campo a campo: nada de IP,
+// hash, vencimento nem id de linha.
+func (h *AccountHandlers) ListSessions(w http.ResponseWriter, r *http.Request) {
+	userID, familyID, ok := h.sessaoViva(w, r)
+	if !ok {
+		return
+	}
+	sessoes, err := h.svc.ListSessions(r.Context(), userID, familyID)
+	if err != nil {
+		h.internal(w, r, "listar sessões", err)
+		return
+	}
+	resp := sessionListResponse{Items: make([]sessionResponse, 0, len(sessoes))}
+	for _, s := range sessoes {
+		resp.Items = append(resp.Items, sessionResponse{
+			ID: s.ID.String(), CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt,
+			UserAgent: s.UserAgent, Current: s.Current,
+		})
+	}
+	apierr.WriteJSON(w, http.StatusOK, resp)
+}
+
+// RevokeSession é DELETE /sessions/{id}. Encerrar a própria sessão é
+// permitido, e a resposta apaga o cookie de refresh: o navegador aceita apagar
+// um cookie de outro Path (RFC 6265 §5.2.4 não valida o Path contra a
+// requisição). O access token em mãos continua valendo até expirar nas rotas
+// de recurso; descartá-lo é trabalho do painel.
+func (h *AccountHandlers) RevokeSession(w http.ResponseWriter, r *http.Request) {
+	userID, familyID, ok := h.sessaoViva(w, r)
+	if !ok {
+		return
+	}
+	alvo, ok := pathID(w, r)
+	if !ok {
+		return
+	}
+	err := h.svc.RevokeSession(r.Context(), userID, alvo,
+		auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()})
+	if errors.Is(err, account.ErrSessionNotFound) {
+		apierr.WriteError(w, r, apierr.CodeNotFound)
+		return
+	}
+	if err != nil {
+		h.internal(w, r, "revogar sessão", err)
+		return
+	}
+	if alvo == familyID {
+		apagarCookieDeRefresh(w, h.secureCookie)
+	}
+	w.WriteHeader(http.StatusNoContent)
+}
+
 // internal registra o erro real e devolve só o envelope genérico com o
 // trace_id (spec §9.1).
 func (h *AccountHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
```

Aplique em `internal/httpapi/authhandlers.go`:

```diff
--- a/internal/httpapi/authhandlers.go
+++ b/internal/httpapi/authhandlers.go
@@ -231,6 +231,13 @@
 }
 
 func (h *AuthHandlers) clearCookie(w http.ResponseWriter) {
+	apagarCookieDeRefresh(w, h.secureCookie)
+}
+
+// apagarCookieDeRefresh é o logout do lado do navegador. Mora fora do
+// AuthHandlers porque DELETE /sessions/{id} da própria sessão também apaga o
+// cookie, e os atributos têm de ser os mesmos do setCookie.
+func apagarCookieDeRefresh(w http.ResponseWriter, secure bool) {
 	// #nosec G124 -- falso positivo: mesmo caso do setCookie; Secure vem de PUBLIC_BASE_URL (routes.go) para não quebrar o desenvolvimento local sem TLS, com HttpOnly e SameSite=Strict fixos.
 	http.SetCookie(w, &http.Cookie{
 		Name:     refreshCookieName,
@@ -238,7 +245,7 @@
 		Path:     "/api/v1/auth",
 		MaxAge:   -1,
 		HttpOnly: true,
-		Secure:   h.secureCookie,
+		Secure:   secure,
 		SameSite: http.SameSiteStrictMode,
 	})
 }
```

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -65,7 +65,7 @@
 	ih := NewIncidentHandlers(d.Incidents)
 	ch := NewChannelHandlers(d.Channels)
 	sp := NewStatusPageHandlers(d.StatusPages, cfg.PublicBaseURL)
-	ah := NewAccountHandlers(d.Accounts, cfg.TrustedProxies)
+	ah := NewAccountHandlers(d.Accounts, cfg.TrustedProxies, secureCookie)
 	authn := middleware.Authn(cfg.JWTSecret, d.Clock)
 
 	mux := http.NewServeMux()
@@ -119,6 +119,8 @@
 	protegida := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(fn)) }
 
 	mux.Handle("GET /api/v1/me", protegida(ah.Me))
+	mux.Handle("GET /api/v1/sessions", protegida(ah.ListSessions))
+	mux.Handle("DELETE /api/v1/sessions/{id}", protegida(ah.RevokeSession))
 
 	mux.Handle("GET /api/v1/targets", protegida(th.List))
 	mux.Handle("POST /api/v1/targets", protegida(th.Create))
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go test -race -count=1 ./internal/account/ ./internal/httpapi/ && go test -count=5 -run TestListSessionsOrderAndTimes ./internal/httpapi/`
Expected: PASS (as cinco rodadas são pelo teste de tempo).

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, com `sqlc generate`). `L` = `go test -count=1 -run TestListSessionsShowsOnlyLive ./internal/httpapi/`:
1. Em `sessions.sql.go`, tire `AND revoked_at IS NULL` de `listLiveFamilies`. Run: `L`. Expected: FAIL, três sessões.
2. Troque `expires_at > $2::timestamptz` por `$2::timestamptz IS NOT NULL`. Run: `L`. Expected: FAIL, três sessões.
3. Troque `user_id = $1` por `$1::uuid IS NOT NULL`. Run: `L`. Expected: FAIL, três sessões.
4. Em `ListSessions`, troque `Current: id == current` por `Current: id != uuid.Nil`. Run: `L`. Expected: FAIL, `a outra sessão viva faltou ou veio como current`.
5. Acrescente ``IPHash string `json:"ip_hash"` `` a `sessionResponse`. Run: `go test -count=1 -run TestListSessionsNeverExposes ./internal/httpapi/`. Expected: FAIL, `chave "ip_hash" fora do contrato`.
6. No `slices.SortFunc`, compare `LastUsedAt` em vez de `CreatedAt`. Run: `go test -count=1 -run TestListSessionsOrder ./internal/httpapi/`. Expected: FAIL, `ordem = … (login mais novo primeiro)`.
7. No começo de `cortar`, acrescente `if len(s) > n { return s[:n] }`. Run: `go test -count=1 -run TestListSessionsTruncates ./internal/httpapi/`. Expected: FAIL, `user_agent com 128 code points`.
8. Em `sessions.sql.go`, troque `WHERE user_id = $1 AND family_id = $2` por `WHERE $1::uuid IS NOT NULL AND family_id = $2` em `familyIsLive`. Run: `go test -count=1 -run TestRevokeSessionOfAnother ./internal/httpapi/`. Expected: FAIL, `devolveu 204, queria 404`; com o mesmo feito em `revokeFamilyOfUser`, também `a sessão do outro usuário caiu`.
9. No `RevokeSession` do handler, troque o bloco do `apagarCookieDeRefresh` por `_ = familyID`. Run: `go test -count=1 -run TestRevokeCurrent ./internal/httpapi/`. Expected: FAIL, `cookie = ; queria vigilume_refresh vazio`. Depois, faça-o apagar sempre (`if alvo != uuid.Nil || familyID == uuid.Nil`). Run: `go test -count=1 -run TestRevokeOtherSessionKeeps ./internal/httpapi/`. Expected: FAIL, `encerrar OUTRA sessão mexeu no cookie`.
10. Em `sessaoViva`, troque o bloco do `!vivo` por `_ = vivo`. Run: `go test -count=1 -run TestSessionRoutesRejectDead ./internal/httpapi/`. Expected: FAIL (`200`, `204` e `o token morto derrubou a sessão do dono`).
11. Tire o `q.LockUserSessions` de `RevokeSession`. Run: `go test -count=1 -run TestRevokeSessionTakes ./internal/account/`. Expected: FAIL, `o encerramento não esperou a trava do usuário`.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/account/ internal/httpapi/
git commit -m "feat(api): GET /sessions e DELETE /sessions/{id}"
```

---

### Task 7: `GET /targets/summary` e a regra única do estado (D15, D16)

**Files:**
- Create: `internal/targetstate/targetstate.go`
- Modify: `internal/statuspage/public.go` (`estado` delega)
- Modify: `internal/storage/queries/targets.sql` (as cinco consultas do resumo)
- Create: `internal/targets/summary.go` (`Summary`, `SparkHour`, `SparkHours`, `(*Service) Summary`)
- Modify: `internal/httpapi/targethandlers.go` (`Summary`), `internal/httpapi/routes.go`
- Test: `internal/targetstate/targetstate_test.go`, `internal/targets/summary_test.go`, `internal/httpapi/summary_integration_test.go`

**Interfaces:**
- Consumes: `storage.InUserTx`, `storage.EnsureChecksPartitions`, `db.InsertCheck` (existentes); os helpers `novoAmbiente`, `usuario`, `entrada`, `caixa` de `internal/targets/service_test.go`; `tokenDe`, `chamar`, `lerCorpo`, `alvoValido` de `targets_integration_test.go`.
- Produces:
  - `targetstate.State` (`Up`, `Degraded`, `Down`, `Unknown`), `targetstate.StaleIntervals = 3`, `func targetstate.Current(enabled bool, interval time.Duration, lastStatus string, lastCheckedAt, now time.Time) State`.
  - `targets.SparkHours = 24`; `type targets.Summary struct { ID uuid.UUID; Name string; Enabled bool; State targetstate.State; LastCheckedAt *time.Time; LastLatencyMS *int32; Uptime24h *float64; OpenIncidentID *uuid.UUID; Spark []SparkHour }`; `type SparkHour struct { Bucket time.Time; Total, OK int32; P50 *int32 }`.
  - `func (s *Service) Summary(ctx, userID uuid.UUID) ([]Summary, error)`.
  - Helpers de teste em `summary_test.go`: `(ambiente).gravarCheck`, `gravarRollup`, `alvo`; `ptr[T]`.

**Contexto para quem implementa.** Com `agora` do relógio injetado, `H = agora.UTC().Truncate(time.Hour)` e `início = H − 24h`, `Summary` roda quatro consultas numa `InUserTx`: (1) os alvos na ordem de `GET /targets` (`id DESC`), com o último check (`LATERAL … LIMIT 1`) e o incidente aberto; (2) a **marca d'água do dono**, o maior `bucket` entre os rollups dos alvos dele (`max(bucket)` da tabela não serve: sob a RLS, um dono sem rollups recentes faria o Postgres varrer o índice da instância inteira testando a política linha a linha); com `corte = clamp(marca + 1h, início, H)`; (3) os rollups de `[início, corte)`; (4) os checks brutos de `[corte, H)` agregados por hora com **a fórmula dos rollups** (`floor(epoch/3600)*3600`, `ok` = não-`down`, `percentile_disc(0.5)` dos não-`down`) e os da hora corrente `[H, H+1h)`, só contados. O corte é fixo antes das consultas 3 e 4: uma consolidação que comite no meio não conta hora nenhuma duas vezes. O spark sai em ordem crescente, sem a hora corrente; o `uptime_24h` inclui a hora corrente, para ver a queda em curso.

O sqlc não enxerga a nulidade do `LEFT JOIN LATERAL` nem do percentil: a consulta 1 usa `coalesce` como a `ListPublicItems` (status `''` e `checked_at` na época Unix quando não há check), e a 4 devolve `p50 = 0` numa hora sem sucesso; o Go usa `ok = 0` para saber que não há mediana, a mesma regra do `CHECK` de `check_rollup_1h`.

O estado sai de `targetstate.Current`, e `statuspage.estado` passa a delegar a ela (D15). A prova de que as duas usam a mesma regra é a sabotagem 1: mudar o limite quebra o teste da regra **e** o da página pública.

O custo medido na spec (5.100 alvos na instância, RLS ativa): ~33 ms com 100 alvos.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/targetstate/targetstate_test.go`:

```go
package targetstate_test

import (
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/targetstate"
)

func TestCurrent(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	minuto := time.Minute
	casos := []struct {
		nome    string
		enabled bool
		status  string
		checkEm time.Time
		queria  targetstate.State
	}{
		{"no ar", true, "up", agora.Add(-minuto), targetstate.Up},
		{"lento", true, "degraded", agora.Add(-minuto), targetstate.Degraded},
		{"fora", true, "down", agora.Add(-minuto), targetstate.Down},
		{"desativado", false, "up", agora.Add(-minuto), targetstate.Unknown},
		{"nunca sondado", true, "", time.Unix(0, 0), targetstate.Unknown},
		{"exatamente 3 intervalos", true, "up", agora.Add(-3 * minuto), targetstate.Up},
		{"3 intervalos e 1 s", true, "up", agora.Add(-3*minuto - time.Second), targetstate.Unknown},
		{"status desconhecido", true, "talvez", agora, targetstate.Unknown},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := targetstate.Current(c.enabled, minuto, c.status, c.checkEm, agora); got != c.queria {
				t.Errorf("Current = %q, queria %q", got, c.queria)
			}
		})
	}
}
```

Crie `internal/targets/summary_test.go`:

```go
package targets_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
	"github.com/BipoXDXD/vigilume/internal/targets"
	"github.com/BipoXDXD/vigilume/internal/targetstate"
)

// gravarCheck grava um check como o sondador grava (InProberTx), criando a
// partição do mês se preciso.
func (a ambiente) gravarCheck(t *testing.T, alvo uuid.UUID, em time.Time, status string, latencia int32) {
	t.Helper()
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, a.pool, em); err != nil {
		t.Fatal(err)
	}
	p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: latencia}
	if status == "down" {
		kind := "timeout"
		p.ErrorKind = &kind
	}
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) gravarRollup(t *testing.T, alvo uuid.UUID, hora time.Time, total, ok int32, p50 *int32) {
	t.Helper()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, $3, $4, $5, $5, $5, $5)`, alvo, hora, total, ok, p50); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) alvo(t *testing.T, dono uuid.UUID) uuid.UUID {
	t.Helper()
	alvo, err := a.svc.Create(context.Background(), dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	return alvo.ID
}

func resumoDe(t *testing.T, a ambiente, dono uuid.UUID) map[uuid.UUID]targets.Summary {
	t.Helper()
	itens, err := a.svc.Summary(context.Background(), dono)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]targets.Summary{}
	for _, s := range itens {
		out[s.ID] = s
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// O spark lê os rollups até a marca d'água do dono e os checks brutos depois
// dela, sem hora duplicada nem faltando; a hora corrente fica fora do spark e
// dentro do uptime.
//
// Relógio em 12:20; H = 12:00; janela do spark = [ontem 12:00, hoje 12:00).
// Rollups até 10:00 (a manutenção ainda não consolidou 11:00). Um check bruto
// às 10:30 está numa hora já consolidada e não pode contar de novo.
func TestSummarySparkMixesRollupsAndRaw(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-25*time.Hour), 60, 0, nil) // antes da janela
	for i := 24; i >= 2; i-- {                                // 23 horas: ontem 12:00 até hoje 10:00
		a.gravarRollup(t, alvo, h.Add(-time.Duration(i)*time.Hour), 60, 60, ptr(int32(100)))
	}
	a.gravarCheck(t, alvo, h.Add(-90*time.Minute), "down", 5000) // 10:30, já consolidada
	a.gravarCheck(t, alvo, h.Add(-55*time.Minute), "up", 70)     // 11:05
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "down", 5000) // 11:10
	a.gravarCheck(t, alvo, h.Add(10*time.Minute), "down", 5000)  // 12:10, hora corrente
	a.gravarCheck(t, alvo, h.Add(19*time.Minute), "down", 5000)  // 12:19, a última

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 24 {
		t.Fatalf("spark com %d horas, queria 24", len(s.Spark))
	}
	if !s.Spark[0].Bucket.Equal(h.Add(-24*time.Hour)) || !s.Spark[23].Bucket.Equal(h.Add(-time.Hour)) {
		t.Errorf("spark de %v a %v, queria de %v a %v", s.Spark[0].Bucket, s.Spark[23].Bucket, h.Add(-24*time.Hour), h.Add(-time.Hour))
	}
	for i := 1; i < len(s.Spark); i++ {
		if !s.Spark[i].Bucket.Equal(s.Spark[i-1].Bucket.Add(time.Hour)) {
			t.Fatalf("hora duplicada ou faltando entre %v e %v", s.Spark[i-1].Bucket, s.Spark[i].Bucket)
		}
	}
	dezHoras, onzeHoras := s.Spark[22], s.Spark[23]
	if dezHoras.Total != 60 || dezHoras.OK != 60 {
		t.Errorf("10:00 = %+v; queria o rollup (60/60), sem o check bruto das 10:30", dezHoras)
	}
	if onzeHoras.Total != 2 || onzeHoras.OK != 1 || onzeHoras.P50 == nil || *onzeHoras.P50 != 70 || onzeHoras.Bucket.Location() != time.UTC {
		t.Errorf("11:00 = %+v; queria 2 checks, 1 ok, p50 70, em UTC", onzeHoras)
	}
	// 23 horas × 60 + 2 brutos das 11h + 2 da hora corrente.
	if s.Uptime24h == nil || *s.Uptime24h != float64(23*60+1)/float64(23*60+2+2) {
		t.Errorf("uptime_24h = %v, queria %v", s.Uptime24h, float64(23*60+1)/float64(23*60+2+2))
	}
	if s.State != targetstate.Down || s.LastLatencyMS != nil || s.LastCheckedAt == nil || !s.LastCheckedAt.Equal(h.Add(19*time.Minute)) {
		t.Errorf("estado %q, latência %v, último check %v; queria down, nil, 12:19", s.State, s.LastLatencyMS, s.LastCheckedAt)
	}
}

// Sem rollup nenhum (instalação nova, ou manutenção parada), o corte recua ao
// início da janela e tudo vem dos brutos.
func TestSummaryWithoutRollupsReadsRaw(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarCheck(t, alvo, h.Add(-24*time.Hour-time.Second), "up", 10) // antes da janela
	a.gravarCheck(t, alvo, h.Add(-24*time.Hour), "up", 20)             // primeira hora
	a.gravarCheck(t, alvo, h.Add(-time.Second), "degraded", 30)        // última hora
	a.gravarCheck(t, alvo, h, "up", 40)                                // hora corrente

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 2 || !s.Spark[0].Bucket.Equal(h.Add(-24*time.Hour)) || !s.Spark[1].Bucket.Equal(h.Add(-time.Hour)) {
		t.Fatalf("spark = %+v; queria as horas de ontem 12:00 e de hoje 11:00", s.Spark)
	}
	if *s.Spark[1].P50 != 30 {
		t.Errorf("degraded conta como sucesso no p50: p50 = %d, queria 30", *s.Spark[1].P50)
	}
	if s.Uptime24h == nil || *s.Uptime24h != 1 {
		t.Errorf("uptime_24h = %v, queria 1 (três checks, nenhum down)", s.Uptime24h)
	}
	if s.State != targetstate.Up || s.LastLatencyMS == nil || *s.LastLatencyMS != 40 {
		t.Errorf("estado %q, latência %v; queria up e 40", s.State, s.LastLatencyMS)
	}
}

// Uma queda na hora corrente derruba o uptime na hora: é por isso que a hora
// corrente entra nele.
func TestSummaryUptimeSeesAnOngoingOutage(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(20 * time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	for i := 24; i >= 1; i-- {
		a.gravarRollup(t, alvo, h.Add(-time.Duration(i)*time.Hour), 60, 60, ptr(int32(100)))
	}
	for m := range 20 {
		a.gravarCheck(t, alvo, h.Add(time.Duration(m)*time.Minute), "down", 5000)
	}

	s := resumoDe(t, a, dono)[alvo]
	if s.Uptime24h == nil || *s.Uptime24h >= 1 {
		t.Errorf("uptime_24h = %v com 20 minutos fora agora; a queda em curso não apareceu", s.Uptime24h)
	}
	if len(s.Spark) != 24 {
		t.Errorf("spark com %d horas, queria 24 (a hora corrente fica fora)", len(s.Spark))
	}
}

// Alvo sem check: tudo nulo e spark vazio, não nil. Alvo desativado com check
// recente: unknown, mas o último check continua visível.
func TestSummaryNullables(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	vazio := a.alvo(t, dono)
	desligado := a.alvo(t, dono)
	if _, err := a.svc.Update(context.Background(), dono, desligado, targets.Patch{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	a.gravarCheck(t, desligado, a.clk.Now().Add(-time.Second), "up", 55)

	r := resumoDe(t, a, dono)
	v := r[vazio]
	if v.State != targetstate.Unknown || v.LastCheckedAt != nil || v.LastLatencyMS != nil || v.Uptime24h != nil || v.OpenIncidentID != nil || len(v.Spark) != 0 {
		t.Errorf("alvo sem check = %+v", v)
	}
	d := r[desligado]
	if d.State != targetstate.Unknown || d.Enabled || d.LastLatencyMS == nil || *d.LastLatencyMS != 55 {
		t.Errorf("alvo desativado = %+v; queria unknown com a latência do último check", d)
	}
}

// O incidente aberto sai; o resolvido não.
func TestSummaryOpenIncident(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	aberto := a.alvo(t, dono)
	resolvido := a.alvo(t, dono)
	inc := uuid.Must(uuid.NewV7())
	agora := a.clk.Now()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		inc, aberto, agora.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause) VALUES ($1, $2, $3, $4, 'timeout')`,
		uuid.Must(uuid.NewV7()), resolvido, agora.Add(-time.Hour), agora.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := resumoDe(t, a, dono)
	if r[aberto].OpenIncidentID == nil || *r[aberto].OpenIncidentID != inc {
		t.Errorf("incidente aberto = %v, queria %v", r[aberto].OpenIncidentID, inc)
	}
	if r[resolvido].OpenIncidentID != nil {
		t.Errorf("o incidente resolvido saiu como aberto: %v", r[resolvido].OpenIncidentID)
	}
}

// A ordem é a de GET /targets: id decrescente (o mais novo primeiro).
func TestSummaryOrderIsTheListOrder(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	var criados []uuid.UUID
	for range 3 {
		criados = append(criados, a.alvo(t, dono))
	}
	itens, err := a.svc.Summary(context.Background(), dono)
	if err != nil {
		t.Fatal(err)
	}
	lista, err := a.svc.List(context.Background(), dono, nil, targets.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 3 || len(lista) != 3 {
		t.Fatalf("%d itens no resumo, %d na lista", len(itens), len(lista))
	}
	for i := range itens {
		if itens[i].ID != lista[i].ID {
			t.Errorf("posição %d: resumo %v, lista %v", i, itens[i].ID, lista[i].ID)
		}
	}
	if itens[0].ID != criados[2] {
		t.Errorf("o primeiro não é o mais novo")
	}
}

// Nada de outro usuário entra: nem alvo, nem check no uptime, nem rollup no
// spark, nem incidente.
//
// Duas barreiras dão esta propriedade: o WHERE user_id das consultas e a RLS
// da InUserTx. Sabotar só uma deixa o teste verde; o plano registra a
// sabotagem das duas juntas.
func TestSummaryIsolation(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono, intruso := a.usuario(t), a.usuario(t)
	meu := a.alvo(t, dono)
	alheio := a.alvo(t, intruso)
	a.gravarRollup(t, alheio, h.Add(-3*time.Hour), 60, 0, nil)
	a.gravarCheck(t, alheio, h.Add(-30*time.Minute), "down", 1)
	a.gravarCheck(t, alheio, h.Add(5*time.Minute), "down", 1)
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		uuid.Must(uuid.NewV7()), alheio, h); err != nil {
		t.Fatal(err)
	}
	a.gravarCheck(t, meu, h.Add(5*time.Minute), "up", 10)

	r := resumoDe(t, a, dono)
	if len(r) != 1 {
		t.Fatalf("%d alvos no resumo do dono, queria 1", len(r))
	}
	m := r[meu]
	if m.Uptime24h == nil || *m.Uptime24h != 1 || len(m.Spark) != 0 || m.OpenIncidentID != nil {
		t.Errorf("o resumo do dono carrega dados alheios: %+v", m)
	}
}

// Os buckets são horas cheias em UTC mesmo com a sessão do banco num fuso de
// meia hora: date_trunc em timestamptz usaria o fuso da sessão (D5 do Plano 4).
func TestSummaryBucketsAreUTCHoursInAnyTimeZone(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "up", 10)

	dsn, err := url.Parse(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("timezone", "Asia/Kolkata")
	dsn.RawQuery = q.Encode()
	kolkata, err := storage.NewPool(context.Background(), dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer kolkata.Close()

	svc := targets.NewService(kolkata, caixa(t), a.clk)
	itens, err := svc.Summary(context.Background(), dono)
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 1 || len(itens[0].Spark) != 1 || !itens[0].Spark[0].Bucket.Equal(h.Add(-time.Hour)) {
		t.Fatalf("spark = %+v; queria uma hora, 11:00 UTC", itens)
	}
}
```

Crie `internal/httpapi/summary_integration_test.go`:

```go
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// GET /targets/summary responde o resumo, e não o 404 de /targets/{id} com
// id "summary"; o item tem exatamente as chaves do contrato, os nulos saem
// null e o spark sai [] (nunca null), e nada de URL nem header.
func TestSummaryRouteReturnsTheContractShape(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	criado := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	if criado.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", criado.StatusCode, lerCorpo(t, criado, nil))
	}
	criado.Body.Close()

	resp := chamar(t, a, http.MethodGet, "/api/v1/targets/summary", tok, nil)
	var corpo struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	cru := lerCorpo(t, resp, &corpo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /targets/summary deu %d: %s", resp.StatusCode, cru)
	}
	if len(corpo.Items) != 1 {
		t.Fatalf("%d itens, queria 1: %s", len(corpo.Items), cru)
	}
	item := corpo.Items[0]
	chaves := []string{"id", "name", "enabled", "state", "last_checked_at", "last_latency_ms",
		"uptime_24h", "open_incident_id", "spark"}
	if len(item) != len(chaves) {
		t.Errorf("item com %d chaves, queria %d: %s", len(item), len(chaves), cru)
	}
	for _, k := range chaves {
		if _, ok := item[k]; !ok {
			t.Errorf("chave %q faltando", k)
		}
	}
	for _, k := range []string{"last_checked_at", "last_latency_ms", "uptime_24h", "open_incident_id"} {
		if string(item[k]) != "null" {
			t.Errorf("%s = %s, queria null num alvo sem check", k, item[k])
		}
	}
	if string(item["spark"]) != "[]" || string(item["state"]) != `"unknown"` {
		t.Errorf("spark = %s, state = %s; queria [] e \"unknown\"", item["spark"], item["state"])
	}
	if strings.Contains(string(cru), "segredo-do-cliente") || strings.Contains(string(cru), "exemplo.com") {
		t.Errorf("o resumo vazou URL ou header: %s", cru)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/targetstate/ ./internal/targets/ ./internal/httpapi/`
Expected: `internal/targetstate` e `internal/targets` FAIL de compilação (pacote e `Summary` indefinidos); `internal/httpapi` FAIL em `TestSummaryRouteReturnsTheContractShape`, `GET /targets/summary deu 404` (o `{id}` responde).

- [ ] **Step 3: Implementar**

Crie `internal/targetstate/targetstate.go`:

```go
// Package targetstate é a regra do estado atual de um alvo, a única do
// sistema (D15 do Plano 5): a página pública (/p/{slug}) e o dashboard
// (GET /targets/summary) a chamam, e por isso não divergem.
package targetstate

import "time"

type State string

const (
	Up       State = "up"
	Degraded State = "degraded"
	Down     State = "down"
	// Unknown: sem check recente. Alvo desativado, sondador parado ou alvo
	// que nunca foi sondado; nenhuma tela afirma o que não sabe.
	Unknown State = "unknown"
)

// StaleIntervals: o último check mais velho que isto, em intervalos do alvo,
// não diz mais nada sobre o presente. É o mesmo 3 da lacuna da histerese
// (Plano 3) e o D15 do Plano 4. O limite é inclusivo.
const StaleIntervals = 3

// Current é o estado do último check, se ele ainda é recente. lastStatus
// vazio quer dizer "nunca houve check".
func Current(enabled bool, interval time.Duration, lastStatus string, lastCheckedAt, now time.Time) State {
	recente := now.Sub(lastCheckedAt) <= StaleIntervals*interval
	if !enabled || lastStatus == "" || !recente {
		return Unknown
	}
	switch s := State(lastStatus); s {
	case Up, Degraded, Down:
		return s
	}
	return Unknown
}
```

Aplique em `internal/statuspage/public.go`:

```diff
--- a/internal/statuspage/public.go
+++ b/internal/statuspage/public.go
@@ -13,6 +13,7 @@
 
 	"github.com/BipoXDXD/vigilume/internal/storage"
 	"github.com/BipoXDXD/vigilume/internal/storage/db"
+	"github.com/BipoXDXD/vigilume/internal/targetstate"
 )
 
 // A projeção pública (spec §10.2). Os tipos daqui são o que a página
@@ -36,12 +37,8 @@
 	// Days é a largura da barra de uptime (spec §10.2).
 	Days = 90
 	// MaxIncidents limita o histórico da página: os mais recentes.
-	MaxIncidents = 20
-	// staleIntervals: o último check mais velho que isto, em intervalos do
-	// alvo, não diz mais nada sobre o presente. É o mesmo 3 da lacuna da
-	// histerese (Plano 3).
-	staleIntervals = 3
-	latencyWindow  = 24 * time.Hour
+	MaxIncidents  = 20
+	latencyWindow = 24 * time.Hour
 	// cacheTTL é o mesmo max-age da resposta: a página é lida quando algo
 	// quebrou, e mil visitantes de IPs diferentes não podem virar mil
 	// agregações de 90 dias no banco.
@@ -189,17 +186,11 @@
 	return v, err
 }
 
-// estado é o do último check, se ele ainda é recente.
+// estado é o do último check, se ele ainda é recente. A regra mora em
+// targetstate, a mesma do dashboard (D15 do Plano 5).
 func estado(it db.ListPublicItemsRow, agora time.Time) State {
-	recente := agora.Sub(it.LastCheckedAt) <= staleIntervals*time.Duration(it.IntervalSec)*time.Second
-	if !it.Enabled || it.LastStatus == "" || !recente {
-		return StateUnknown
-	}
-	switch State(it.LastStatus) {
-	case StateUp, StateDegraded, StateDown:
-		return State(it.LastStatus)
-	}
-	return StateUnknown
+	return State(targetstate.Current(it.Enabled, time.Duration(it.IntervalSec)*time.Second,
+		it.LastStatus, it.LastCheckedAt, agora))
 }
 
 func preencherDias(pi *PublicItem, desde time.Time, linhas []db.ListPublicDailyUptimeRow) {
```

Aplique em `internal/storage/queries/targets.sql`:

```diff
--- a/internal/storage/queries/targets.sql
+++ b/internal/storage/queries/targets.sql
@@ -42,3 +42,75 @@
    AND c.checked_at >= sqlc.arg(from_at)
    AND c.checked_at < sqlc.arg(to_at)
  ORDER BY c.checked_at;
+
+-- GET /targets/summary (Plano 5). Todas rodam sob InUserTx E filtram por
+-- user_id, como as de cima.
+
+-- Os alvos do dono na ordem de GET /targets, com o último check e o incidente
+-- aberto (o índice único parcial garante no máximo um). Alvo sem check volta
+-- com last_status '' e last_checked_at na época Unix, como ListPublicItems: o
+-- sqlc não enxerga a nulidade de um LEFT JOIN LATERAL.
+-- name: ListTargetSummaries :many
+SELECT t.id, t.name, t.enabled, t.interval_sec,
+       coalesce(lc.status, '')::text AS last_status,
+       coalesce(lc.checked_at, 'epoch'::timestamptz)::timestamptz AS last_checked_at,
+       coalesce(lc.latency_ms, 0)::int AS last_latency_ms,
+       x.id AS open_incident_id
+  FROM targets t
+  LEFT JOIN LATERAL (
+       SELECT c.status, c.checked_at, c.latency_ms FROM checks c
+        WHERE c.target_id = t.id
+        ORDER BY c.checked_at DESC LIMIT 1) lc ON true
+  LEFT JOIN incidents x ON x.target_id = t.id AND x.resolved_at IS NULL
+ WHERE t.user_id = sqlc.arg(user_id)
+ ORDER BY t.id DESC;
+
+-- A marca d'água DO DONO: a hora mais recente já consolidada entre os alvos
+-- dele. max(bucket) da tabela inteira não serve: sob a RLS, um dono sem
+-- rollups recentes faria o Postgres varrer o índice de bucket da instância
+-- inteira, testando a política linha a linha. Sem rollup, a época Unix.
+-- name: LatestUserRollupBucket :one
+SELECT coalesce(max(r.bucket), 'epoch'::timestamptz)::timestamptz
+  FROM targets t
+  CROSS JOIN LATERAL (
+       SELECT bucket FROM check_rollup_1h
+        WHERE target_id = t.id
+        ORDER BY bucket DESC LIMIT 1) r
+ WHERE t.user_id = sqlc.arg(user_id);
+
+-- name: ListSummaryRollups :many
+SELECT r.target_id, r.bucket, r.total, r.ok, r.p50
+  FROM check_rollup_1h r
+  JOIN targets t ON t.id = r.target_id
+ WHERE t.user_id = sqlc.arg(user_id)
+   AND r.bucket >= sqlc.arg(from_at)::timestamptz
+   AND r.bucket < sqlc.arg(to_at)::timestamptz;
+
+-- As horas cheias ainda não consolidadas, com a MESMA fórmula de
+-- ConsolidateRollups. p50 sai 0 numa hora sem sucesso (o sqlc não enxerga a
+-- nulidade do percentil); quem lê usa ok = 0 para saber que não há mediana,
+-- a mesma regra do CHECK de check_rollup_1h.
+-- name: ListSummaryRawHours :many
+SELECT c.target_id,
+       to_timestamp(floor(extract(epoch FROM c.checked_at) / 3600) * 3600)::timestamptz AS bucket,
+       count(*)::int AS total,
+       (count(*) FILTER (WHERE c.status <> 'down'))::int AS ok,
+       coalesce(percentile_disc(0.50) WITHIN GROUP (ORDER BY c.latency_ms)
+                FILTER (WHERE c.status <> 'down'), 0)::int AS p50
+  FROM checks c
+  JOIN targets t ON t.id = c.target_id
+ WHERE t.user_id = sqlc.arg(user_id)
+   AND c.checked_at >= sqlc.arg(from_at)::timestamptz
+   AND c.checked_at < sqlc.arg(to_at)::timestamptz
+ GROUP BY 1, 2;
+
+-- A hora corrente entra só no uptime, não no spark: só contagens, sem
+-- percentil.
+-- name: ListSummaryCurrentHour :many
+SELECT c.target_id, count(*)::int AS total, (count(*) FILTER (WHERE c.status <> 'down'))::int AS ok
+  FROM checks c
+  JOIN targets t ON t.id = c.target_id
+ WHERE t.user_id = sqlc.arg(user_id)
+   AND c.checked_at >= sqlc.arg(from_at)::timestamptz
+   AND c.checked_at < sqlc.arg(to_at)::timestamptz
+ GROUP BY 1;
```

Rode `sqlc generate`.

Crie `internal/targets/summary.go`:

```go
package targets

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/targetstate"
)

// SparkHours é a janela do spark: as 24 horas cheias anteriores à hora
// corrente (contrato dos Planos 5–7).
const SparkHours = 24

// Summary é um alvo como o dashboard o mostra (GET /targets/summary).
type Summary struct {
	ID      uuid.UUID
	Name    string
	Enabled bool
	State   targetstate.State
	// LastCheckedAt e LastLatencyMS são nil sem check nenhum; LastLatencyMS é
	// nil também quando o último check foi down (D16 do Plano 5): a latência
	// de um down é o tempo até o timeout, não a do serviço.
	LastCheckedAt *time.Time
	LastLatencyMS *int32
	// Uptime24h é ok/total sobre as horas do spark MAIS a hora corrente (D16):
	// uma queda em curso aparece nele. nil sem check nenhum na janela.
	Uptime24h      *float64
	OpenIncidentID *uuid.UUID
	Spark          []SparkHour
}

// SparkHour é uma hora cheia (UTC) do spark. Hora sem check não aparece. P50
// é nil numa hora sem sucesso.
type SparkHour struct {
	Bucket    time.Time
	Total, OK int32
	P50       *int32
}

type contagem struct{ total, ok int64 }

// Summary monta o dashboard numa chamada só (D16 do Plano 5), em quatro
// consultas na mesma transação do dono:
//
//  1. os alvos, com o último check e o incidente aberto;
//  2. a marca d'água dos rollups do dono, que dá o corte;
//  3. os rollups horários de [H−24h, corte);
//  4. os checks brutos de [corte, H), agregados por hora com a fórmula dos
//     rollups, e os da hora corrente [H, H+1h), só contados.
//
// Ler só os rollups deixaria a última hora fechada de fora por até 15 min e o
// uptime cego à queda em curso; ler só os brutos custa ~390 ms com 100 alvos
// (medido na spec). O corte é um valor fixo lido antes das consultas 3 e 4:
// uma consolidação que comite entre elas não conta hora nenhuma duas vezes.
func (s *Service) Summary(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	agora := s.clk.Now()
	h := agora.UTC().Truncate(time.Hour)
	inicio := h.Add(-SparkHours * time.Hour)

	var out []Summary
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		alvos, err := q.ListTargetSummaries(ctx, userID)
		if err != nil {
			return fmt.Errorf("listar alvos do resumo: %w", err)
		}
		marca, err := q.LatestUserRollupBucket(ctx, userID)
		if err != nil {
			return fmt.Errorf("marca d'água dos rollups: %w", err)
		}
		corte := marca.UTC().Add(time.Hour)
		if corte.Before(inicio) {
			corte = inicio
		}
		if corte.After(h) {
			corte = h
		}

		horas := map[uuid.UUID][]SparkHour{}
		rollups, err := q.ListSummaryRollups(ctx, db.ListSummaryRollupsParams{UserID: userID, FromAt: inicio, ToAt: corte})
		if err != nil {
			return fmt.Errorf("rollups do resumo: %w", err)
		}
		for _, r := range rollups {
			horas[r.TargetID] = append(horas[r.TargetID], SparkHour{Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok, P50: r.P50})
		}
		brutos, err := q.ListSummaryRawHours(ctx, db.ListSummaryRawHoursParams{UserID: userID, FromAt: corte, ToAt: h})
		if err != nil {
			return fmt.Errorf("checks do resumo: %w", err)
		}
		for _, r := range brutos {
			hora := SparkHour{Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok}
			if r.Ok > 0 {
				p50 := r.P50
				hora.P50 = &p50
			}
			horas[r.TargetID] = append(horas[r.TargetID], hora)
		}
		correntes, err := q.ListSummaryCurrentHour(ctx, db.ListSummaryCurrentHourParams{UserID: userID, FromAt: h, ToAt: h.Add(time.Hour)})
		if err != nil {
			return fmt.Errorf("hora corrente do resumo: %w", err)
		}
		corrente := map[uuid.UUID]contagem{}
		for _, r := range correntes {
			corrente[r.TargetID] = contagem{total: int64(r.Total), ok: int64(r.Ok)}
		}

		out = make([]Summary, 0, len(alvos))
		for _, a := range alvos {
			sm := Summary{
				ID: a.ID, Name: a.Name, Enabled: a.Enabled, OpenIncidentID: a.OpenIncidentID,
				State: targetstate.Current(a.Enabled, time.Duration(a.IntervalSec)*time.Second,
					a.LastStatus, a.LastCheckedAt, agora),
				Spark: horas[a.ID],
			}
			if a.LastStatus != "" {
				em := a.LastCheckedAt.UTC()
				sm.LastCheckedAt = &em
				if a.LastStatus != "down" {
					ms := a.LastLatencyMS
					sm.LastLatencyMS = &ms
				}
			}
			slices.SortFunc(sm.Spark, func(x, y SparkHour) int { return x.Bucket.Compare(y.Bucket) })
			c := corrente[a.ID]
			for _, hr := range sm.Spark {
				c.total += int64(hr.Total)
				c.ok += int64(hr.OK)
			}
			if c.total > 0 {
				u := float64(c.ok) / float64(c.total)
				sm.Uptime24h = &u
			}
			out = append(out, sm)
		}
		return nil
	})
	return out, err
}
```

Aplique em `internal/httpapi/targethandlers.go`:

```diff
--- a/internal/httpapi/targethandlers.go
+++ b/internal/httpapi/targethandlers.go
@@ -336,6 +336,58 @@
 	return id, ok
 }
 
+type sparkResponse struct {
+	Bucket time.Time `json:"bucket"`
+	Total  int32     `json:"total"`
+	OK     int32     `json:"ok"`
+	P50MS  *int32    `json:"p50_ms"`
+}
+
+// targetSummaryResponse é o item de GET /targets/summary, no shape do
+// contrato dos Planos 5–7. Campo a campo, como targetResponse: nada de URL
+// nem de headers.
+type targetSummaryResponse struct {
+	ID             string          `json:"id"`
+	Name           string          `json:"name"`
+	Enabled        bool            `json:"enabled"`
+	State          string          `json:"state"`
+	LastCheckedAt  *time.Time      `json:"last_checked_at"`
+	LastLatencyMS  *int32          `json:"last_latency_ms"`
+	Uptime24h      *float64        `json:"uptime_24h"`
+	OpenIncidentID *string         `json:"open_incident_id"`
+	Spark          []sparkResponse `json:"spark"`
+}
+
+// Summary é GET /targets/summary: o dashboard numa chamada só (spec §11.3),
+// sem paginação (o teto de alvos por usuário é o limite).
+func (h *TargetHandlers) Summary(w http.ResponseWriter, r *http.Request) {
+	userID, ok := usuario(w, r)
+	if !ok {
+		return
+	}
+	resumo, err := h.svc.Summary(r.Context(), userID)
+	if h.falhou(w, r, "resumir alvos", err) {
+		return
+	}
+	itens := make([]targetSummaryResponse, 0, len(resumo))
+	for _, s := range resumo {
+		item := targetSummaryResponse{
+			ID: s.ID.String(), Name: s.Name, Enabled: s.Enabled, State: string(s.State),
+			LastCheckedAt: s.LastCheckedAt, LastLatencyMS: s.LastLatencyMS, Uptime24h: s.Uptime24h,
+			Spark: make([]sparkResponse, 0, len(s.Spark)),
+		}
+		if s.OpenIncidentID != nil {
+			id := s.OpenIncidentID.String()
+			item.OpenIncidentID = &id
+		}
+		for _, hr := range s.Spark {
+			item.Spark = append(item.Spark, sparkResponse{Bucket: hr.Bucket, Total: hr.Total, OK: hr.OK, P50MS: hr.P50})
+		}
+		itens = append(itens, item)
+	}
+	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": itens})
+}
+
 // pathID lê o {id} da rota. Malformado é 404 (D16): o recurso não existe de
 // jeito nenhum, e um 400 ensinaria o formato do id a quem varre.
 func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
```

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -124,6 +124,8 @@
 
 	mux.Handle("GET /api/v1/targets", protegida(th.List))
 	mux.Handle("POST /api/v1/targets", protegida(th.Create))
+	// O literal é mais específico que {id} e ganha no ServeMux, sem conflito.
+	mux.Handle("GET /api/v1/targets/summary", protegida(th.Summary))
 	mux.Handle("GET /api/v1/targets/{id}", protegida(th.Get))
 	mux.Handle("PATCH /api/v1/targets/{id}", protegida(th.Update))
 	mux.Handle("DELETE /api/v1/targets/{id}", protegida(th.Delete))
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go test -race -count=1 ./internal/targetstate/ ./internal/targets/ ./internal/statuspage/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, com `sqlc generate`). `S` = `go test -count=1 -run TestSummary ./internal/targets/`:
1. Mude `StaleIntervals` para `2`. Run: `go test -count=1 ./internal/targetstate/ ./internal/statuspage/`. Expected: FAIL em `TestCurrent/exatamente_3_intervalos` **e** em `TestPublicProjection`: as duas projeções usam a mesma regra.
2. Em `targets.sql.go`, troque o bucket de `listSummaryRawHours` por `date_trunc('hour', c.checked_at)::timestamptz`. Run: `S`. Expected: FAIL em `TestSummaryBucketsAreUTCHoursInAnyTimeZone` (bucket 10:30).
3. Em `Summary`, leia os brutos desde `inicio` em vez de `corte`. Run: `S`. Expected: FAIL, `spark com 25 horas, queria 24`.
4. Troque `c := corrente[a.ID]` por `c := contagem{}` (e `_ = corrente`). Run: `S`. Expected: FAIL em `TestSummaryUptimeSeesAnOngoingOutage`.
5. Troque `if a.LastStatus != "down" {` por `if a.LastStatus != "" {`. Run: `S`. Expected: FAIL em `TestSummarySparkMixesRollupsAndRaw` (latência não nula num `down`).
6. Em `targets.sql.go`, troque `WHERE t.user_id = $1` por `WHERE $1::uuid IS NOT NULL` em `listTargetSummaries` **e** troque a `storage.InUserTx(ctx, s.pool, userID, …)` do `Summary` por `storage.InProberTx(ctx, s.pool, …)`. Run: `S`. Expected: FAIL, `2 alvos no resumo do dono`. Só uma das duas deixa o teste verde (D23), e o teste diz isso.
7. Troque `ORDER BY t.id DESC` por `ORDER BY t.id ASC`. Run: `S`. Expected: FAIL em `TestSummaryOrderIsTheListOrder`.
8. Tire `AND x.resolved_at IS NULL` do `LEFT JOIN incidents`. Run: `S`. Expected: FAIL, `o incidente resolvido saiu como aberto`.
9. Tire a rota `GET /api/v1/targets/summary`. Run: `go test -count=1 -run TestSummaryRoute ./internal/httpapi/`. Expected: FAIL, `deu 404`.
10. Tire o `Spark: make([]sparkResponse, 0, len(s.Spark)),` do handler. Mesmo comando. Expected: FAIL, `spark = null`.

- [ ] **Step 6: Commit**

```bash
git add internal/targetstate/ internal/statuspage/ internal/storage/ internal/targets/ internal/httpapi/
git commit -m "feat(api): GET /targets/summary e regra única do estado atual"
```

---

### Task 8: Teto de 100 alvos por usuário — `409 target_limit_reached` (D17)

**Files:**
- Modify: `internal/storage/queries/targets.sql` (`LockUserTargets`, `CountTargets`)
- Modify: `internal/targets/target.go` (`ErrLimitReached`, `MaxTargetsPerUser`), `internal/targets/service.go` (trava e contagem no `Create`; gancho de teste)
- Modify: `internal/apierr/errors.go` (`CodeTargetLimit`), `internal/httpapi/targethandlers.go`
- Test: `internal/targets/limit_test.go`, `internal/targets/export_test.go`, `internal/httpapi/targets_integration_test.go`

**Interfaces:**
- Consumes: `novoAmbiente`, `usuario`, `entrada` (existentes em `service_test.go`).
- Produces:
  - `targets.ErrLimitReached`; `targets.MaxTargetsPerUser = int64(MaxPageSize)` (100).
  - `apierr.CodeTargetLimit = "target_limit_reached"` → `409`, "Limite de alvos atingido."

**Contexto para quem implementa.** O teto está na reconciliação do contrato: o Plano 6 mapeia o código no formulário de alvo. O número é o de `MaxPageSize`, então uma página de `GET /targets` sempre cabe todos, e o resumo, que não pagina, tem custo e tamanho limitados. A contagem e a inserção rodam sob `pg_advisory_xact_lock('vigilume.targets:' || user_id)` dentro da `InUserTx`: sem a trava, dois `POST` simultâneos passariam os dois pela contagem. O `MaxChannels` do Plano 3 tem exatamente esse furo, e fica registrado nas pendências. O teste de concorrência força a sobreposição com o mesmo gancho da Task 4. A contagem roda sob a RLS **e** filtra por `user_id`: duas barreiras (D23).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/targets/export_test.go`:

```go
package targets

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }
```

Crie `internal/targets/limit_test.go`:

```go
package targets_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/targets"
)

// encherAte cria n alvos do dono direto no banco (o Create inteiro, n vezes,
// seria lento à toa).
func (a ambiente) encherAte(t *testing.T, dono uuid.UUID, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $2::int) i`, dono, n); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// O 100º entra; o 101º é ErrLimitReached; o teto é por usuário.
func TestCreateStopsAtTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	if _, err := a.svc.Create(ctx, dono, entrada()); err != nil {
		t.Fatalf("o 100º alvo foi recusado: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, entrada()); !errors.Is(err, targets.ErrLimitReached) {
		t.Fatalf("o 101º alvo deu %v, queria ErrLimitReached", err)
	}
	if _, err := a.svc.Create(ctx, outro, entrada()); err != nil {
		t.Errorf("o teto de um usuário barrou outro: %v", err)
	}
}

// Dois POST simultâneos com 99 alvos: um entra, o outro bate no teto. O gancho
// segura cada criação entre a contagem e a inserção até as duas chegarem ali,
// ou por meio segundo: com a trava, a segunda nem conta antes de a primeira
// comitar; sem ela, as duas contam 99 e as duas inserem.
func TestConcurrentCreatesRespectTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	a.svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = a.svc.Create(context.Background(), dono, entrada())
		}()
	}
	wg.Wait()

	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != int(targets.MaxTargetsPerUser) {
		t.Errorf("%d alvos depois de dois POST simultâneos (erros: %v), queria %d", n, erros, targets.MaxTargetsPerUser)
	}
}
```

Aplique em `internal/httpapi/targets_integration_test.go`:

```diff
--- a/internal/httpapi/targets_integration_test.go
+++ b/internal/httpapi/targets_integration_test.go
@@ -17,6 +17,7 @@
 	"github.com/BipoXDXD/vigilume/internal/auth"
 	"github.com/BipoXDXD/vigilume/internal/storage"
 	"github.com/BipoXDXD/vigilume/internal/storage/db"
+	"github.com/BipoXDXD/vigilume/internal/targets"
 )
 
 func tokenDe(t *testing.T, a *api, userID uuid.UUID) string {
@@ -563,3 +564,33 @@
 		t.Errorf("B, no mesmo IP, deu %d depois de A esgotar o balde: o limite não é por usuário", resp.StatusCode)
 	}
 }
+
+// O 101º alvo é 409 target_limit_reached (D17 do Plano 5, reconciliado no
+// contrato); o painel mapeia o código no formulário.
+func TestTargetLimitIs409(t *testing.T) {
+	a := newAPI(t)
+	tok := tokenDe(t, a, a.userID)
+	ctx := context.Background()
+	tx, err := a.pool.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer tx.Rollback(ctx)
+	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := tx.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
+		SELECT gen_random_uuid(), $1, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
+		  FROM generate_series(1, $2::int) i`, a.userID, targets.MaxTargetsPerUser); err != nil {
+		t.Fatal(err)
+	}
+	if err := tx.Commit(ctx); err != nil {
+		t.Fatal(err)
+	}
+
+	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
+	defer resp.Body.Close()
+	if resp.StatusCode != http.StatusConflict || decodeEnvelope(t, resp).Error.Code != "target_limit_reached" {
+		t.Errorf("status %d, queria 409 target_limit_reached", resp.StatusCode)
+	}
+}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/targets/ ./internal/httpapi/`
Expected: FAIL de compilação (`s.depoisDeContar undefined`; `undefined: targets.MaxTargetsPerUser`).

- [ ] **Step 3: Implementar**

Aplique em `internal/storage/queries/targets.sql`:

```diff
--- a/internal/storage/queries/targets.sql
+++ b/internal/storage/queries/targets.sql
@@ -114,3 +114,11 @@
    AND c.checked_at >= sqlc.arg(from_at)::timestamptz
    AND c.checked_at < sqlc.arg(to_at)::timestamptz
  GROUP BY 1;
+
+-- O teto de alvos por usuário (Plano 5): contar e inserir sob esta trava.
+-- Sem ela, dois POST simultâneos passariam os dois pela contagem.
+-- name: LockUserTargets :exec
+SELECT pg_advisory_xact_lock(hashtextextended('vigilume.targets:' || sqlc.arg(user_id)::uuid::text, 0));
+
+-- name: CountTargets :one
+SELECT count(*) FROM targets WHERE user_id = $1;
```

Rode `sqlc generate`.

Aplique em `internal/targets/target.go`:

```diff
--- a/internal/targets/target.go
+++ b/internal/targets/target.go
@@ -12,6 +12,9 @@
 
 var ErrNotFound = errors.New("alvo não encontrado")
 
+// ErrLimitReached: o usuário já tem MaxTargetsPerUser alvos (D17 do Plano 5).
+var ErrLimitReached = errors.New("limite de alvos atingido")
+
 // Target é o que sai do domínio. Os VALORES dos headers não saem nunca (D5):
 // o dono os escreve, o sondador os usa, e ninguém os lê de volta.
 type Target struct {
@@ -101,6 +104,12 @@
 	DefaultPageSize int32 = 50
 	MaxPageSize     int32 = 100
 
+	// MaxTargetsPerUser é o teto de alvos por usuário (D17 do Plano 5): o
+	// mesmo número de MaxPageSize, então uma página de GET /targets sempre
+	// cabe todos, e o custo de GET /targets/summary, que não pagina, tem teto
+	// (~33 ms e ~180 KB com 100 alvos, medidos na spec).
+	MaxTargetsPerUser = int64(MaxPageSize)
+
 	// MaxChecksWindow é a janela que a tabela bruta atende (spec §9.1).
 	// Acima disso, a série vem de check_rollup_1h (D6 do Plano 2).
 	MaxChecksWindow = 48 * time.Hour
```

Aplique em `internal/targets/service.go`:

```diff
--- a/internal/targets/service.go
+++ b/internal/targets/service.go
@@ -24,6 +24,9 @@
 	pool *pgxpool.Pool
 	box  *crypto.Box
 	clk  clock.Clock
+	// depoisDeContar existe para o teste de criação concorrente: roda entre a
+	// contagem de alvos e a inserção.
+	depoisDeContar func()
 }
 
 func NewService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock) *Service {
@@ -58,6 +61,21 @@
 	// cadastrou vê o estado em segundos, não depois de um intervalo inteiro.
 	now := s.clk.Now()
 	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
+		// O teto conta e insere sob a trava do usuário: sem ela, dois POST
+		// simultâneos passariam os dois pela contagem.
+		if err := q.LockUserTargets(ctx, userID); err != nil {
+			return fmt.Errorf("travar alvos do usuário: %w", err)
+		}
+		n, err := q.CountTargets(ctx, userID)
+		if err != nil {
+			return fmt.Errorf("contar alvos: %w", err)
+		}
+		if s.depoisDeContar != nil {
+			s.depoisDeContar()
+		}
+		if n >= MaxTargetsPerUser {
+			return ErrLimitReached
+		}
 		return q.InsertTarget(ctx, db.InsertTargetParams{
 			ID: id, UserID: userID, Name: f.Name, URL: f.URL, Method: f.Method,
 			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
@@ -65,6 +83,9 @@
 			Enabled: in.Enabled, NextRunAt: now, CreatedAt: now,
 		})
 	})
+	if errors.Is(err, ErrLimitReached) {
+		return Target{}, err
+	}
 	if err != nil {
 		return Target{}, fmt.Errorf("gravar alvo: %w", err)
 	}
```

Aplique em `internal/apierr/errors.go`:

```diff
--- a/internal/apierr/errors.go
+++ b/internal/apierr/errors.go
@@ -37,6 +37,7 @@
 	CodeDeliveryFailed     ErrorCode = "delivery_failed"
 	CodeStatusPageExists   ErrorCode = "status_page_exists"
 	CodeSlugTaken          ErrorCode = "slug_taken"
+	CodeTargetLimit        ErrorCode = "target_limit_reached"
 )
 
 // A mensagem vem sempre desta tabela, nunca do erro subjacente.
@@ -66,6 +67,9 @@
 	// Uma status page por usuário (spec §2.1); a segunda é conflito.
 	CodeStatusPageExists: {http.StatusConflict, "Você já tem uma status page."},
 	CodeSlugTaken:        {http.StatusConflict, "Este endereço já está em uso."},
+	// O teto de alvos por usuário (D17 do Plano 5); reconciliado no contrato
+	// dos Planos 5–7.
+	CodeTargetLimit: {http.StatusConflict, "Limite de alvos atingido."},
 }
 
 // FieldError aponta o campo culpado de um 400 (D14). Field e Reason vêm de
```

Aplique em `internal/httpapi/targethandlers.go`:

```diff
--- a/internal/httpapi/targethandlers.go
+++ b/internal/httpapi/targethandlers.go
@@ -315,6 +315,8 @@
 	switch {
 	case errors.Is(err, targets.ErrNotFound):
 		apierr.WriteError(w, r, apierr.CodeNotFound)
+	case errors.Is(err, targets.ErrLimitReached):
+		apierr.WriteError(w, r, apierr.CodeTargetLimit)
 	case errors.As(err, &verr):
 		campos := make([]apierr.FieldError, 0, len(verr.Fields))
 		for _, f := range verr.Fields {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go test -race -count=3 -run 'TestCreateStops|TestConcurrentCreates' ./internal/targets/ && go test -race -count=1 ./internal/targets/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, com `sqlc generate`):
1. Troque `q.LockUserTargets(ctx, userID)` por `error(nil)` no `if`. Run: `go test -count=1 -run TestConcurrentCreates ./internal/targets/`. Expected: FAIL, `101 alvos depois de dois POST simultâneos`.
2. Troque `n >= MaxTargetsPerUser` por `n > MaxTargetsPerUser`. Run: `go test -count=1 -run TestCreateStops ./internal/targets/`. Expected: FAIL, `o 101º alvo deu <nil>`.
3. Tire o `case errors.Is(err, targets.ErrLimitReached)` do `falhou`. Run: `go test -count=1 -run TestTargetLimit ./internal/httpapi/`. Expected: FAIL, `status 500, queria 409`.

Registre a barreira dupla: trocar só o `user_id` de `countTargets` por `$1::uuid IS NOT NULL` deixa `TestCreateStopsAtTheLimit` verde, porque a RLS da `InUserTx` restringe a contagem.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/targets/ internal/apierr/ internal/httpapi/
git commit -m "feat(targets): teto de 100 alvos por usuário (409 target_limit_reached)"
```

---

### Task 9: Filtro `GET /incidents?target_id=` (D18)

**Files:**
- Modify: `internal/storage/queries/incidents.sql` (`ListIncidents` ganha `target_id`)
- Modify: `internal/incident/history/history.go` (`Filter.TargetID`)
- Modify: `internal/httpapi/incidenthandlers.go`
- Test: `internal/incident/history/history_test.go`, `internal/httpapi/incidents_integration_test.go`

**Interfaces:**
- Consumes: `novaBancada`, `usuario`, `alvo`, `incidente`, `ids` (existentes em `history_test.go`); `incidentesDe`, `outroUsuario`, `tokenDe` (existentes nos testes HTTP).
- Produces: `history.Filter{…, TargetID *uuid.UUID}`; `db.ListIncidentsParams.TargetID *uuid.UUID`.

**Contexto para quem implementa.** Reconciliação do contrato, pedida pelo Plano 6 para a tela de detalhe do alvo: aditivo, combina com `status` e com o cursor, mesma paginação. `target_id` de outro usuário devolve **lista vazia, não 404**: o filtro não confirma a existência de alvo alheio. Malformado é `400` com `field: target_id`, como o `cursor`: é parâmetro de consulta, não o recurso do caminho.

O teste HTTP dá um incidente **resolvido** aos dois alvos do dono. Na primeira versão, o outro alvo só tinha incidente aberto, e a asserção "`target_id` + `status=resolved`" passava com o handler ignorando `target_id` (D22).

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/incident/history/history_test.go`:

```diff
--- a/internal/incident/history/history_test.go
+++ b/internal/incident/history/history_test.go
@@ -165,3 +165,31 @@
 		t.Errorf("ack de id inexistente deu %v", err)
 	}
 }
+
+// target_id restringe a um alvo; o de outro usuário dá lista vazia, e não os
+// incidentes dele.
+func TestListFiltersByTarget(t *testing.T) {
+	b := novaBancada(t)
+	ctx := context.Background()
+	dono, intruso := b.usuario(t), b.usuario(t)
+	api, site := b.alvo(t, dono, "API"), b.alvo(t, dono, "Site")
+	alheio := b.alvo(t, intruso, "Alheio")
+	daAPI := b.incidente(t, api, true)
+	b.incidente(t, site, true)
+	b.incidente(t, alheio, true)
+
+	got, err := b.svc.List(ctx, dono, history.Filter{TargetID: &api})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if g := ids(got); len(g) != 1 || g[0] != daAPI {
+		t.Errorf("filtro por alvo = %v, queria só %v", g, daAPI)
+	}
+	got, err = b.svc.List(ctx, dono, history.Filter{TargetID: &alheio})
+	if err != nil || len(got) != 0 {
+		t.Errorf("alvo alheio = %v, %v; queria lista vazia", ids(got), err)
+	}
+	if todos, _ := b.svc.List(ctx, dono, history.Filter{}); len(todos) != 2 {
+		t.Errorf("sem filtro: %d incidentes, queria 2", len(todos))
+	}
+}
```

Aplique em `internal/httpapi/incidents_integration_test.go`:

```diff
--- a/internal/httpapi/incidents_integration_test.go
+++ b/internal/httpapi/incidents_integration_test.go
@@ -112,6 +112,7 @@
 		"limit=101":         {Field: "limit", Reason: "out_of_range"},
 		"limit=x":           {Field: "limit", Reason: "invalid"},
 		"cursor=nao-e-uuid": {Field: "cursor", Reason: "invalid"},
+		"target_id=x":       {Field: "target_id", Reason: "invalid"},
 	} {
 		var env envelopeComCampos
 		resp := chamar(t, a, http.MethodGet, "/api/v1/incidents?"+consulta, tok, nil)
@@ -122,6 +123,32 @@
 	}
 }
 
+// ?target_id= é aditivo (Plano 5): combina com status, e o alvo de outro
+// usuário dá 200 com a lista vazia, não 404.
+func TestIncidentListFiltersByTarget(t *testing.T) {
+	a := newAPI(t)
+	tok := tokenDe(t, a, a.userID)
+	alvo, ids := incidentesDe(t, a, tok, 2)
+	// Outro alvo do mesmo dono, também com um incidente resolvido: sem o
+	// filtro, a consulta abaixo traria dois.
+	incidentesDe(t, a, tok, 2)
+
+	var l listaIncidentesJSON
+	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?target_id="+alvo+"&status=resolved", tok, nil), &l)
+	if len(l.Items) != 1 || l.Items[0].ID != ids[0] {
+		t.Errorf("target_id + status: %+v, queria só %s", l.Items, ids[0])
+	}
+
+	intruso := outroUsuario(t, a)
+	alheio, _ := incidentesDe(t, a, tokenDe(t, a, intruso), 1)
+	resp := chamar(t, a, http.MethodGet, "/api/v1/incidents?target_id="+alheio, tok, nil)
+	var vazia listaIncidentesJSON
+	lerCorpo(t, resp, &vazia)
+	if resp.StatusCode != http.StatusOK || len(vazia.Items) != 0 {
+		t.Errorf("alvo alheio deu %d com %d itens, queria 200 e lista vazia", resp.StatusCode, len(vazia.Items))
+	}
+}
+
 func TestIncidentListPaginates(t *testing.T) {
 	a := newAPI(t)
 	tok := tokenDe(t, a, a.userID)
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/incident/history/ ./internal/httpapi/`
Expected: `history` FAIL de compilação (`unknown field TargetID`); `httpapi` FAIL em `TestIncidentListRejectsBadQueries` (`target_id=x deu 200`) e `TestIncidentListFiltersByTarget` (dois itens; `alvo alheio deu 200 com 4 itens`).

- [ ] **Step 3: Implementar**

Aplique em `internal/storage/queries/incidents.sql`:

```diff
--- a/internal/storage/queries/incidents.sql
+++ b/internal/storage/queries/incidents.sql
@@ -56,6 +56,8 @@
 
 -- Histórico (API): rodam sob InUserTx e filtram por user_id, como as de
 -- targets.sql. open NULL lista tudo; true só abertos; false só resolvidos.
+-- target_id NULL lista todos os alvos; um id de alvo alheio não casa com
+-- nada e devolve a lista vazia (reconciliação dos Planos 5–7).
 -- O id é UUIDv7, gerado quando o incidente abre: ordenar por ele é ordenar
 -- pela abertura, e o cursor é o último id da página anterior.
 -- name: ListIncidents :many
@@ -65,6 +67,7 @@
  WHERE t.user_id = sqlc.arg(user_id)
    AND (sqlc.narg(open)::bool IS NULL OR (i.resolved_at IS NULL) = sqlc.narg(open)::bool)
    AND (sqlc.narg(before)::uuid IS NULL OR i.id < sqlc.narg(before)::uuid)
+   AND (sqlc.narg(target_id)::uuid IS NULL OR i.target_id = sqlc.narg(target_id)::uuid)
  ORDER BY i.id DESC
  LIMIT sqlc.arg(max_rows)::int;
 
```

Rode `sqlc generate`.

Aplique em `internal/incident/history/history.go`:

```diff
--- a/internal/incident/history/history.go
+++ b/internal/incident/history/history.go
@@ -31,11 +31,14 @@
 }
 
 // Filter: Open nil lista tudo; true só abertos; false só resolvidos. Before
-// é o cursor, o último id da página anterior.
+// é o cursor, o último id da página anterior. TargetID restringe a um alvo
+// (Plano 5, a tela de detalhe do alvo); o de outro usuário dá lista vazia,
+// não erro: o filtro não confirma a existência de alvo alheio.
 type Filter struct {
-	Open   *bool
-	Before *uuid.UUID
-	Limit  int32
+	Open     *bool
+	Before   *uuid.UUID
+	TargetID *uuid.UUID
+	Limit    int32
 }
 
 const (
@@ -59,7 +62,7 @@
 	var out []Incident
 	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
 		rows, err := q.ListIncidents(ctx, db.ListIncidentsParams{
-			UserID: userID, Open: f.Open, Before: f.Before, MaxRows: f.Limit,
+			UserID: userID, Open: f.Open, Before: f.Before, TargetID: f.TargetID, MaxRows: f.Limit,
 		})
 		if err != nil {
 			return fmt.Errorf("listar incidentes: %w", err)
```

Aplique em `internal/httpapi/incidenthandlers.go`:

```diff
--- a/internal/httpapi/incidenthandlers.go
+++ b/internal/httpapi/incidenthandlers.go
@@ -46,7 +46,8 @@
 	}
 }
 
-// List é GET /incidents?status=open|resolved&cursor=&limit= (spec §9).
+// List é GET /incidents?status=open|resolved&target_id=&cursor=&limit=
+// (spec §9; target_id é do Plano 5).
 func (h *IncidentHandlers) List(w http.ResponseWriter, r *http.Request) {
 	userID, ok := usuario(w, r)
 	if !ok {
@@ -86,6 +87,16 @@
 		}
 		f.Before = &c
 	}
+	// target_id malformado é 400, como o cursor: é parâmetro de consulta, e
+	// não o recurso do caminho (que seria 404).
+	if s := q.Get("target_id"); s != "" {
+		alvo, err := uuid.Parse(s)
+		if err != nil {
+			campoInvalido(w, r, "target_id", targets.ReasonInvalid)
+			return
+		}
+		f.TargetID = &alvo
+	}
 
 	items, err := h.svc.List(r.Context(), userID, f)
 	if h.falhou(w, r, "listar incidentes", err) {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && sqlc diff && go test -race -count=1 ./internal/incident/... ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure (no SQL gerado, com `sqlc generate`):
1. Em `incidents.sql.go`, troque `OR i.target_id = $4::uuid` por `OR $4::uuid IS NOT NULL`. Run: `go test -count=1 -run 'TestListFiltersByTarget|TestIncidentListFiltersByTarget' ./internal/incident/history/ ./internal/httpapi/`. Expected: FAIL nos dois pacotes.
2. No handler, troque `f.TargetID = &alvo` por `_ = alvo`. Run: `go test -count=1 -run TestIncidentListFiltersByTarget ./internal/httpapi/`. Expected: FAIL, `target_id + status: [dois itens]` e `alvo alheio deu 200 com 4 itens`.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/incident/ internal/httpapi/
git commit -m "feat(api): filtro target_id em GET /incidents"
```

---

### Task 10: Wiring das rotas novas e documentação

**Files:**
- Test: `internal/httpapi/plano5_routes_test.go` (novo)
- Create: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-6.md`
- Modify: `docs/superpowers/plans/2026-09-22-roadmap.md`

**Interfaces:**
- Consumes: `chamar`, `decodeEnvelope`, `entrar` (tarefas anteriores e existentes).
- Produces: nada que outra tarefa use.

**Contexto para quem implementa.** A lição do Plano 1: testes que montam a cadeia de middlewares à mão provam cada peça e nunca o *wiring*. Estes testes passam pelo `Routes` de verdade: sem token, toda rota nova responde `401 unauthorized`; as mutações sem `X-Requested-With` são `403`; o corpo acima de 1 MiB é `413`. Eles **nascem verdes** — são trava, não especificação —, e a prova de que travam é a sabotagem. O `401` sem token tem duas guardas (o `Authn` e o próprio handler, que recusa sem usuário no contexto; D23): registrar uma rota sem `Authn` deixa o teste verde, e o comentário diz isso.

As pendências substituem o `PENDENCIAS-PARA-O-PLANO-5.md` como estado corrente e carregam as notas para o Plano 6 (renovar só em `unauthorized`, tratar o `DELETE` da própria sessão como logout, coordenar o refresh entre abas).

- [ ] **Step 1: Escrever os testes de wiring**

Crie `internal/httpapi/plano5_routes_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A cadeia das rotas novas do Plano 5, pelo Routes de verdade (a lição do
// Plano 1: provar cada middleware à mão não prova o wiring).
//
// Sem token, toda rota nova responde o 401 unauthorized. Duas guardas dão
// isso: o Authn da cadeia e o próprio handler, que recusa sem usuário no
// contexto (como o D25 do Plano 4); registrar a rota sem Authn deixa este
// teste verde, e o comentário existe para ninguém achar que ele trava o
// Authn sozinho.
func TestPlano5RoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodPost, "/api/v1/auth/password"},
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodDelete, "/api/v1/sessions/" + uuid.NewString()},
		{http.MethodGet, "/api/v1/targets/summary"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		codigo := decodeEnvelope(t, resp).Error.Code
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || codigo != "unauthorized" {
			t.Errorf("%s %s sem token deu %d/%s, queria 401 unauthorized", r.metodo, r.caminho, resp.StatusCode, codigo)
		}
	}
}

// As mutações novas exigem X-Requested-With (spec §5.3) e o corpo passa pelo
// limite de 1 MiB, como todas as da API.
func TestPlano5MutationsPassThroughTheChain(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodPost, "/api/v1/auth/password"},
		{http.MethodDelete, "/api/v1/sessions/" + uuid.NewString()},
	} {
		req, err := http.NewRequest(r.metodo, a.srv.URL+r.caminho, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s sem X-Requested-With deu %d, queria 403", r.metodo, r.caminho, resp.StatusCode)
		}
	}

	grande := bytes.Repeat([]byte("a"), 2<<20)
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/password",
		bytes.NewReader(append(append([]byte(`{"current_password":"`), grande...), []byte(`"}`)...)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Requested-With", "vigilume")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo de 2 MiB deu %d, queria 413", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar**

Run: `go test -race -count=1 -run TestPlano5 ./internal/httpapi/`
Expected: PASS (nasce verde: é trava de wiring).

- [ ] **Step 3: Sabotagem**

1. Em `routes.go`, apague a linha `handler = middleware.RequireCustomHeader(handler)`. Run: `go test -count=1 -run TestPlano5Mutations ./internal/httpapi/`. Expected: FAIL, `POST /api/v1/auth/password sem X-Requested-With deu 400, queria 403` e o mesmo no `DELETE` (`404`). Restaure.
2. Registre `GET /api/v1/targets/summary` com `http.HandlerFunc(th.Summary)`, sem `protegida`. Run: `go test -count=1 -run TestPlano5RoutesRequire ./internal/httpapi/`. Expected: **PASS** — o handler também recusa sem usuário (D23). Registre no relatório; restaure.

- [ ] **Step 4: Documentação**

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-6.md`:

```markdown
# Pendências que atravessam o Plano 5

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-5.md` como estado corrente; aquele fica como histórico.

## Já valendo em produção — não é código

Continuam como o Plano 4 deixou (ver `PENDENCIAS-PARA-O-PLANO-5.md`, primeira seção):
`TRUSTED_PROXIES` no Fly.io, papel comum do Postgres, `METRICS_ADDR`, e-mail (`RESEND_API_KEY`
e `EMAIL_FROM`), `PUBLIC_BASE_URL` e a primeira manutenção depois do deploy. Duas acrescentadas:

1. **Usuário inicial:** `vigilume user create --email <e>` com a senha no stdin (terminal sem eco,
   ou pipe com uma linha só). Precisa só de `DATABASE_URL`, com o **papel comum**: o
   superusuário é recusado antes de qualquer migration (código 1). Código **3** = já existe
   usuário, nada mudou; um passo idempotente o trata como sucesso. `vigilume user set-password`
   é a recuperação: senha nova, bloqueio desfeito, **todas** as sessões encerradas.
2. **Política de senha nova:** 15 a 128 caracteres (code points). Senhas antigas mais curtas
   continuam entrando; a política vale na criação e na troca.

## Resolvidas no Plano 5

| Origem | Como fechou |
|---|---|
| Contrato dos Planos 5–7 | `GET /me` enriquecido, `POST /auth/password`, `GET /sessions`, `DELETE /sessions/{id}`, `GET /targets/summary`, `GET /incidents?target_id=`, `409 target_limit_reached` (teto de 100 alvos) |
| Corrida de revogação (achada na spec do Plano 5) | Toda mutação de sessão toma `pg_advisory_xact_lock` do usuário antes de travar linha; o sucessor de uma rotação concorrente não escapa mais da revogação (logout, detecção de reúso, troca de senha, `DELETE /sessions`, `set-password`) |
| `normalizarEmail` pendente no `authhandlers.go` | Virou `auth.NormalizeEmail`, a mesma no login e no CLI |
| `/totp/enroll` e `/totp/disable` sem bloqueio | Passaram a usar `auth.CheckPassword`: conta bloqueada recusa, senha errada conta |
| `/totp/disable` respondia `401 unauthorized` para código errado (apontado pelo Plano 6) | Senha ou código errado ou ausente é `401 invalid_credentials` em `/auth/password`, `/totp/enroll` e `/totp/disable`; `unauthorized` fica só para token recusado (D24) |

## Limites conhecidos do Plano 5

- **Janela de 15 minutos.** Depois de `DELETE /sessions/{id}`, da troca de senha ou do
  `set-password`, o access token já emitido de uma família encerrada continua valendo nas rotas
  de recurso (alvos, canais, status page, incidentes) até expirar. Não renova, não troca senha,
  não mexe no 2FA, não lista nem encerra sessões. É a escolha da spec §5.2 (JWT sem estado); a
  alternativa, checar a família em toda rota, custa uma consulta por requisição.
- **Login concorrente com a troca de senha.** Um login que verificou a senha **antiga** alguns
  milissegundos antes de a troca comitar pode abrir a família depois dela e sobreviver. Aparece
  em `GET /sessions` e se encerra dali.
- **`last_used_at` tem a granularidade do refresh** (~15 min num painel ativo).
- **O summary lê até 24h de `checks` se a manutenção parar**: o corte recua sozinho, e o custo
  sobe (~390 ms com 100 alvos) até ela voltar.
- **~180 KB de JSON por chamada do summary com 100 alvos**, sem compressão.
- **O limitador do login não cobre `/totp/enroll` nem `/totp/disable`** (só `/auth/password`,
  que o contrato manda): o bloqueio por conta limita as tentativas ali.

## Para o Plano 6 (painel)

- Renovar só em `401` com `code: unauthorized`, fora de `/auth/*`. `invalid_credentials` em
  `/auth/password` é erro de formulário: nunca renovar e repetir.
- `DELETE /sessions/{id}` da sessão `current`: o `204` já apagou o cookie; tratar como logout
  (descartar o token da memória, limpar o cache, ir para `/login`) sem tentar `/auth/refresh`.
  `404` no `DELETE`: a sessão já tinha acabado; recarregar a lista.
- Várias abas renovando o mesmo cookie ao mesmo tempo disparam a detecção de reúso e derrubam a
  família: coordenar o refresh entre abas (Web Locks).
- Campo de código da troca de senha: aparece com `totp_enabled` de `/me`; aceita o código de
  recuperação (16 caracteres base32). Política no cliente: 15 a 128 **code points**.
- `user_agent` de `/sessions` é texto de terceiros; `uptime_24h` é número cru (truncar na
  exibição, D16 do Plano 4).

## Herdadas, ainda abertas

- Do `PENDENCIAS-PARA-O-PLANO-5.md`: `jti` em denylist ou `challenge_id` consumido para o desafio
  de 2FA; troca de autenticador; `HashPasswordWith` com parâmetros fora de faixa; `LogValue` de
  `Spec.Headers` (M6); porta de métricas em desenvolvimento (M7); rota para desabilitar canal.
- **`MaxChannels` conta e insere sem trava:** dois `POST /channels` simultâneos passam do limite
  de 10. O teto de alvos do Plano 5 usa a trava que falta ali.
- **Recuperação do 2FA pelo CLI:** quem perde o autenticador e os códigos de recuperação só sai
  por SQL.
- **R2**: migrar `auth.PGStore` e `auth.TOTP` para sqlc. Os métodos novos do `pgTx` já são sqlc.
- As dívidas menores da seção homônima do `PENDENCIAS-PARA-O-PLANO-5.md` seguem como estavam.
```

Aplique em `docs/superpowers/plans/2026-09-22-roadmap.md`:

```diff
--- a/docs/superpowers/plans/2026-09-22-roadmap.md
+++ b/docs/superpowers/plans/2026-09-22-roadmap.md
@@ -14,7 +14,12 @@
 | 2 | Alvos e sondador | `2026-09-24-alvos-e-sondador.md` | Alvos sondados, checks persistidos | 6–7 | concluído |
 | 3 | Incidentes e notificações | `2026-09-24-incidentes-e-notificacoes.md` | Incidente vira Discord + e-mail | 8–9 | concluído |
 | 4 | Status page e retenção | `2026-09-24-statuspage-e-retencao.md` | Página pública, rollups, partições | 10 | concluído |
-| 5 | Painel React | `NN-painel-react.md` | Painel completo, deploy, README | 11–14 | a escrever |
+| 5 | API do painel | `2026-09-24-api-do-painel.md` | Conta, sessões, resumo do dashboard | 11 | concluído |
+| 6 | Painel React | `NN-painel-react.md` | Painel completo servido pelo binário | 12–13 | a executar |
+| 7 | Empacotamento e deploy | `NN-empacotamento-e-deploy.md` | Compose, CI, deploy, README | 14 | a executar |
+
+Em 2026-09-24 o antigo Plano 5 (painel, deploy e README) virou três, com um contrato comum:
+`docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`.
 
 ## Ordem e seus motivos
 
```

- [ ] **Step 5: Portão final**

Run: `gofmt -l . && go vet ./... && staticcheck ./... && sqlc diff && gosec -quiet ./... && govulncheck ./... && go test -race -count=1 ./...`
Expected: tudo limpo e verde. (`staticcheck`, `gosec` e `govulncheck` ficam em `$(go env GOPATH)/bin`.)

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/plano5_routes_test.go docs/superpowers/
git commit -m "test(api): wiring das rotas do Plano 5; docs: pendências para o Plano 6"
```

---

## Depois da última tarefa

Revisão do branch inteiro (`superpowers:requesting-code-review`), com atenção especial a três coisas: toda transação que muda `sessions` toma a trava do usuário **antes** de qualquer trava de linha (D1); nenhuma resposta de `/sessions` carrega IP, hash ou vencimento; e `unauthorized` só sai para token recusado (D24). Depois, `superpowers:finishing-a-development-branch`.
