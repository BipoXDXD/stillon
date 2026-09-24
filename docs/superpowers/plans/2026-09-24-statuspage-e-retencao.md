# Vigilume — Plano 4: Status Page, Rollups e Retenção

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** O dono publica uma status page em `/p/{slug}` — HTML sem JavaScript, abaixo de 10 KB, com estado atual, barra de uptime de 90 dias e histórico de incidentes — enquanto um job de manutenção consolida os checks em rollups horários e aplica a retenção da spec §12; de quebra, o canal que quebra depois de verificado deixa de falhar em silêncio, e um apagão em massa deixa de esgotar as tentativas no 429 do Discord.

**Architecture:** A migration 00006 cria `check_rollup_1h`, `status_pages`, `status_page_items` e `channel_health`, e um **quarto escopo de RLS**, o público: `app.public_slug`, sempre numa transação `READ ONLY`, enxerga só a página publicada daquele slug e, por delegação, os alvos dela. `storage.RunMaintenance` roda a cada 15 minutos no papel do sondador: partições, consolidação idempotente dos rollups e retenção. `GET /targets/{id}/checks` acima de 48h lê os rollups. O pacote novo `internal/statuspage` tem o cadastro do dono e a projeção pública, com tipos próprios que não têm onde guardar id, URL ou causa; a projeção fica 30 s em cache por slug. A página é renderizada com `html/template` embutido numa cadeia HTTP separada, com CSP de nonce. No `notify`, o dispatcher grava a saúde do canal e passa a entregar em série por canal, devolvendo a tentativa num 429.

**Tech Stack:** Go 1.26, PostgreSQL 16, `pgx/v5`, `sqlc` v1.31.1, `goose` v3, `html/template` + `embed`, `golang.org/x/sync/singleflight`, `testcontainers-go`.

**Spec:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§2.1, §4.1, §5.5, §5.6, §5.7, §9, §9.1, §10, §12).

**Roteiro:** `docs/superpowers/plans/2026-09-22-roadmap.md` (este é o plano 4 de 5).

**Pendências absorvidas:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`. Entram: a D6 do Plano 2 (rollups em `/checks`), a retenção, a status page, o canal `failing` e o apagão em massa. A **R2** (auth → sqlc) sai por decisão do usuário e vira plano próprio. O resto segue para o `PENDENCIAS-PARA-O-PLANO-5.md` que a Task 10 escreve.

**Validação prévia.** Todo o código deste plano rodou numa cópia do repositório antes de o plano ser entregue: PostgreSQL 16 real, `sqlc generate`, `go test -race ./...` verde, `sqlc diff`, `vet`, `staticcheck`, `gosec` e `govulncheck` limpos. Além disso, **o estado da árvore depois de cada tarefa** foi reconstruído a partir deste plano e conferido (compila, `vet`, `sqlc diff`, testes da tarefa com `-race`); o estado depois da Task 11 é idêntico, byte a byte, à cópia validada. Os diffs abaixo são exatos: aplicam sobre o estado da tarefa anterior. Cada sabotagem pedida nos passos foi feita, e o teste ficou vermelho. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

**Como ler os diffs.** Arquivo novo aparece inteiro. Arquivo modificado aparece como diff unificado contra o estado da tarefa anterior; o contexto é suficiente para aplicar à mão, ou com `git apply` depois de salvar o bloco num arquivo. `internal/storage/db/*.go` é gerado: quando a tarefa muda SQL, o passo diz `sqlc generate`, e o diff não aparece.

---

## Global Constraints

Valores copiados da spec e das decisões do usuário. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/vigilume`. **PostgreSQL 16+.** Sem framework HTTP e sem ORM (spec §3.3). Código novo de banco em **sqlc**; `internal/storage/db/*.go` é gerado e **nunca** editado à mão.
- **IDs UUIDv7**, gerados em Go (spec §4.2). **Toda função de serviço do dono recebe `userID` logo depois de `ctx`**; recurso de outro usuário é **404, nunca 403** (spec §5.5).
- **RLS com `FORCE`, um papel só.** Os escopos só são ligados em `internal/storage/scope.go`: `app.user_id` (API), `app.scope = 'prober'` (sondador e dispatcher), `app.verify_token_hash` (verificação de e-mail) e, novo, **`app.public_slug` (status page), sempre com `SET TRANSACTION READ ONLY`**.
- **Status page (spec §5.6):** DTO próprio, construído campo a campo; **nunca** a struct de domínio. Exposto: nome de exibição, estado (no ar / lento / fora / sem dados), uptime e latência agregados, incidentes com início, fim e duração. **Nunca exposto:** URL, headers, IPs, portas, mensagem de erro, causa, `user_id`, UUIDs, status code.
- **Status page (spec §10):** `html/template` + `embed.FS`, **sem JavaScript**, CSS inline no `<style>` com **nonce** da CSP, **CSP sem `unsafe-inline`**, resposta **abaixo de 10 KB**, **`Cache-Control` de 30 s**. Conteúdo: título, alvos com nome de exibição e estado, **barra de 90 dias em resolução diária**, incidentes resolvidos com início, fim e duração.
- **Uma status page por usuário** (spec §2.1), API no plural da §9: `GET /status-pages`, `POST /status-pages`, `PATCH /status-pages/{id}`. O histórico de incidentes mostra **só os alvos que estão na página** (decisão do usuário).
- **`resolution` é decidida pelo servidor** (spec §9.1): janela **≤ 48h** lê `checks`; acima, `check_rollup_1h`.
- **Retenção (spec §12):** `checks` 30 dias por `DROP` da partição mensal; `check_rollup_1h` **13 meses**; `incidents` indefinida; `audit_log` **12 meses**; `sessions` **7 dias após expirar**. O job roda nos papéis `all` e `prober`, cria a partição do mês seguinte e consolida os rollups.
- **Envelope de erro estável** (spec §9.1); os `fields` são vocabulário fechado, nunca eco da entrada.
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa**, e `sqlc diff` limpo quando a tarefa mexe em SQL.
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não vaza", "não vê", "não conta"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho, restaura, e **registra no relatório** o que quebrou e o que viu. O teste vacuamente verde apareceu 14 vezes no Plano 1, 5 no Plano 2, e **três vezes na validação deste plano** (ver D25); um quarto defeito, que dependia de sorteio, só apareceu na execução (D26).

### Desvios registrados em relação à spec

| Spec | Aqui | Motivo |
|---|---|---|
| §9 `GET /targets/{id}/checks ?from=&to=&resolution=` | `resolution` sai na **resposta**; o parâmetro é ignorado | §9.1 diz que o servidor decide; a lista de rotas é o rascunho |
| §4.1 tabelas | `channel_health` nova | O dispatcher (escopo prober) grava a saúde sem ganhar `UPDATE` em `notif_channels` |
| §10.1 "resposta abaixo de 10 KB" | Vale para **5 serviços**; a página aceita 20 | 20 barras de 90 dias não cabem em 10 KB com nenhuma codificação (D20) |
| §12 "job diário" | A cada **15 minutos** | O rollup da última hora cheia fica disponível logo depois de ela fechar; cada rodada é idempotente e barata |

---

## Decisões tomadas neste plano

O usuário delegou todas as decisões de desenho deste plano. Cada uma tem o custo de estar errada, para quem revisar depois.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | Uma página por usuário: `UNIQUE (user_id)`; a segunda é **409 `status_page_exists`**; slug de outro é **409 `slug_taken`**. Os dois vêm da violação de UNIQUE, não de consulta prévia | Várias páginas | Migrar para várias é tirar o UNIQUE e mudar o `GET`; a API já é plural |
| D2 | Escopo público via `app.public_slug`, **sempre `READ ONLY`**. As políticas de `incidents`, `checks` e `target_health` são `FOR ALL` e delegam a `targets`: sem `READ ONLY`, o escopo público seria porta de escrita nelas | Políticas `FOR SELECT` novas em cada tabela | Nenhum enquanto só `InPublicTx` ligar o escopo |
| D3 | `targets_public_select` consulta os itens por uma **função plpgsql** (`public_page_target_ids`, desde a Task 11) | `EXISTS` direto na política | O `EXISTS` direto fecha um ciclo com o `WITH CHECK` dos itens: `42P17 infinite recursion` (provado em spike). A função roda com o papel de quem chama, e a RLS dos itens vale dentro dela |
| D4 | O `WITH CHECK` dos itens exige o alvo **visível ao dono**; o serviço também confere (`OwnTargetIDs`) para dizer **qual** item está errado | Só a FK | A FK ignora RLS: sem o `WITH CHECK`, o dono poria o alvo de outro usuário na página e publicaria o estado dele |
| D5 | Rollup: bucket = hora cheia **UTC** (`epoch % 3600 = 0`); `ok` conta os **não-down** (degraded é sucesso, como no Plano 3); latências só dos sucessos, `NULL` numa hora sem sucesso | `date_trunc` | `date_trunc` em `timestamptz` usa o fuso da sessão; num fuso de meia hora, a "hora cheia" seria outra |
| D6 | Consolidação idempotente (`ON CONFLICT DO UPDATE`) de `[marca d'água − 2h, hora cheia atual)`; sem rollup nenhum, volta **62 dias** (31 até a Task 11, D30). `RunMaintenance` a cada **15 min** | Diária | Um check que chega mais de 2h atrasado não entra no rollup |
| D7 | Retenção de `checks` por **partição inteira**: sai a partição que termina antes do corte (os checks duram 30–61 dias), com a **mesma advisory lock** de `ensure_checks_partition` | `DELETE` por linha | Um `DROP` concorrente com o `CREATE` da partição seria corrida no catálogo |
| D8 | Notificações `sent`/`failed`/`cancelled` saem com **30 dias**; `pending` **nunca** sai por idade | Guardar para sempre | A outbox cresceria sem teto |
| D9 | `/checks` acima de 48h: `items` horários (`bucket`, `total`, `ok`, `p50_ms`, `p95_ms`, `p99_ms`, `max_ms`), `from` arredondado para baixo até a hora, buckets em **UTC**, teto de **400 dias** (cobre os 13 meses dos rollups). A resposta ganha `resolution: "raw" \| "1h"` | Teto de 90 dias | Guardar 13 meses sem poder consultar seria só custo; 400 dias são até 9.600 itens |
| D10 | Canal `failing`: `channel_health` guarda `last_success_at`, `last_failure_at` e `last_error`; `failing` = verificado **e** falha definitiva depois do último sucesso. Retentativa e cancelamento **não** mexem. A última falha continua visível depois que o canal volta | Coluna em `notif_channels` | O sondador precisaria de `UPDATE` em `notif_channels` |
| D11 | Apagão em massa: um **429 devolve a tentativa** (`refund`) enquanto a notificação tem menos de **1h**; depois, conta | Não contar 429 nunca | Um webhook que responde 429 para sempre seguraria a fila para sempre |
| D12 | O mesmo canal recebe **em série**, na ordem de enfileiramento (id v7); canais diferentes, em paralelo. Depois de um 429, o resto do canal no lote é **adiado sem envio** para quando o provedor pediu (`deferred` em `notify_total`) | Agrupar alertas numa mensagem | Mais pedidos a quem pediu espera renovam o bloqueio; em série, abertura sai antes do fechamento (fecha a pendência do Plano 3) |
| D13 | Slug: `^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`, **não normalizado** (maiúscula é `invalid`), regra também no `CHECK` | Baixar a caixa | O slug vira URL pública: o dono tem de ver exatamente o que publica |
| D14 | Itens trocados **inteiros** a cada escrita; máx. **20**; erro aponta a posição (`items[2].target_id`); alvo repetido, nulo ou alheio é `invalid` | `PATCH` por item | A posição é a ordem; um `PATCH` por item exigiria reordenar os outros |
| D15 | Estado público = o do **último check**; `unknown` ("Sem dados") se o check tem mais de **3 intervalos**, se o alvo está desativado ou se nunca houve check | Estado do incidente aberto | A página afirmaria "no ar" sobre um alvo que ninguém sonda há horas |
| D16 | Barra: 90 dias **UTC**; verde ≥ **99,9%**, amarelo ≥ **99%**, vermelho abaixo, cinza sem check. Porcentagem **truncada** em duas casas | Arredondar | "100,00%" num dia com falha; com checks de 1 min, 99,9% de um dia é 1,4 check |
| D17 | Latência pública = média das medianas horárias das últimas 24h, ponderada pelos sucessos | Mediana exata | Aproximação; a exata exigiria ler `checks` no escopo público a cada visita |
| D18 | Incidentes públicos: **resolvidos**, dos últimos 90 dias, até **20**, só dos alvos na página, com nome de exibição, início e fim (**sem causa**) | Mostrar o aberto | O estado atual já diz que está fora; a causa é `error_kind`, que a §5.6 não lista |
| D19 | Cache da projeção **30 s por slug** em processo, com `singleflight`; só sucesso é guardado; teto de 1.000 slugs; a consulta roda **desligada do contexto do visitante** com prazo de 5 s; mudança do dono invalida o slug (D31) | Só o `Cache-Control` | A página é aberta quando algo quebrou: mil visitantes de IPs diferentes seriam mil agregações de 90 dias |
| D20 | **10 KB para 5 serviços.** A barra é codificada pela **maioria**: a `<ol>` leva a classe mais comum e só os outros dias levam atributo. Pior caso com 5 serviços ~9,3 KB; caso comum < 7 KB | Cortar o máximo para 5 | Tirar capacidade legítima para cumprir um número; com 20 serviços a página passa de 10 KB |
| D21 | 404 **idêntico** para inexistente, rascunho e slug malformado (este nem vai ao banco), com `no-store`; banco fora é **503 em HTML** | Cachear o 404 | Página recém-publicada presa num 404 em cache |
| D22 | Cadeia `/p/` separada (como `/verify/`): sem CORS e sem `RequireCustomHeader`, CSP própria, limite por IP **120/min, burst 60** | Dentro da cadeia da API | Com `TRUSTED_PROXIES` vazio no Fly, todos os visitantes dividem um balde (ver pendências) |
| D23 | `public_url` no DTO do dono (`PUBLIC_BASE_URL/p/{slug}`), também em rascunho | O painel monta | Duas fontes da mesma URL |
| D24 | `RunMaintenance` **substitui** `MaintainChecksPartitions` (sem teste, panic com `every <= 0`) e roda também ao subir | Manter os dois | Dois jobs disputando a mesma advisory lock |
| D25 | Três testes que nasceram vacuamente verdes na validação foram corrigidos e ficam registrados: `count(*)` de `channel_health` sem escopo (a RLS escondia tudo); rota sem `authn` que ainda dava 401 (o handler também recusa, comentado no teste); nome proibido que casava "id" dentro de "Incidents" | — | — |
| D26 | Nonce da CSP em **base64 de URL sem padding** | base64 padrão | Com a padrão, o `+` sai como `&#43;` no atributo do `<style>` e o HTML deixa de trazer o nonce literal da CSP. Achado na **execução** deste plano: o teste de vazamento passou na validação porque o nonce sorteado não tinha `+`, e falhou no branch. `TestNonceNeedsNoEscaping` sorteia mil |
| D27 | A política pública pergunta `id IN (SELECT public_page_target_ids())`: um conjunto por consulta (revisão final) | Função booleana por alvo (o desenho até a Task 10) | A função por alvo rodava uma vez para cada alvo da instância: ~0,5 s com 5 mil alvos, e 503 no pior momento |
| D28 | Qualquer falha **transitória** (429, timeout, rede, 5xx), inclusive a que esgota as tentativas, para o canal no lote; o resto é adiado sem envio; só o 429 dentro de 1h devolve a tentativa, e quem está no teto falha de vez com o mesmo motivo. Lease = 8 × (10 s + 5 s) + 1 min | Parar só no 429 (Task 5) | Um webhook que trava seguraria o lote por k × 10 s, e os outros canais esperariam junto |
| D29 | Retenção em duas transações: DELETEs primeiro; `DROP` sozinho, com `lock_timeout` de **2 s** | Uma transação | O `DROP` pega `ACCESS EXCLUSIVE` em `checks`: segurá-la durante os DELETEs, ou esperar por ela sem limite, enfileira o sondador e a página pública |
| D30 | Backfill da primeira consolidação: **62 dias** | 31 | Os checks de 31 a 61 dias, que ainda existem no primeiro deploy, sumiriam no `DROP` sem nunca virar rollup |
| D31 | `Create` e `Update` tiram do cache da instância o slug antigo e o novo | Esperar os 30 s | Um slug abandonado mostraria a página velha, e um slug tomado por outro dono mostraria a página do dono anterior |
| D32 | `Update` trava a página (`FOR UPDATE`) antes de ler | Sem trava | Dois PATCH simultâneos: o segundo regravaria o campo que o primeiro mudou |
| D33 | `public_page_target_ids` fica `SECURITY INVOKER` **sem** `search_path` fixo (achado recusado) | `SET search_path` | Sombrear `status_page_items` com tabela temporária exige executar SQL na própria sessão, com os próprios privilégios: não há fronteira de privilégio. Só importaria numa `SECURITY DEFINER` |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa o Vigilume, com o que uma pessoa razoável esperaria. Cada uma tem teste na tarefa dona.

1. **O dono põe na página um nome de exibição com HTML** (`<script>`), ou um alvo cujo nome interno ou URL é segredo. Espera-se texto escapado, e nenhum nome interno, URL, header, id ou código de status no HTML. → Task 9, `TestPublicPageRendersWithoutLeaking`; Task 8, `TestPublicViewHasNoInternalFields`.
2. **Um visitante tenta adivinhar slugs** (rascunho, inexistente, `../etc`, maiúsculas). Espera-se a mesma resposta, byte a byte, sem revelar que o rascunho existe. → Task 9, `TestPublicPage404IsTheSameForDraftAndMissing`; Task 8, `TestPublicFindsOnlyPublishedPages`.
3. **Muitos alvos do mesmo dono caem juntos** e o Discord responde 429. Espera-se que os alertas cheguem, na ordem, sem esgotar as tentativas. → Task 5, `TestRateLimitDoesNotSpendAttempts`, `TestSameChannelSendsInOrderAndHoldsAfterA429`.
4. **O dono apaga um alvo que está na página publicada.** Espera-se que ele suma da página (e o histórico dele junto), sem derrubar a página. → Task 6, `TestDeletedTargetLeavesThePage`; Task 8, `TestDeletedTargetLeavesThePublicPage`.
5. **O sondador parou há horas**, e o último check de um alvo era "no ar". Espera-se "Sem dados", e não uma página afirmando que está tudo bem. → Task 8, `TestPublicProjection` (item "Velho", borda de exatamente 3 intervalos).

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/storage/migrations/00006_statuspage_rollups.sql` | Rollups, status page, itens, escopo público, `channel_health`, índices e função de retenção. |
| `internal/storage/scope.go` | `InPublicTx`, o quarto escopo de RLS (somente leitura). |
| `internal/storage/queries/statuspages.sql` | Cadastro do dono e as cinco consultas públicas. |
| `internal/storage/queries/maintenance.sql` | Consolidação, marca d'água, retenção, `ListRollups`. |
| `internal/storage/queries/notifications.sql` | `created_at` na reivindicação, `refund`, saúde do canal. |
| `internal/storage/queries/channels.sql` | `ListChannels` com a saúde do canal. |
| `internal/storage/maintenance.go` | `Maintain`, `ConsolidateRollups`, `ApplyRetention`, `RunMaintenance`. |
| `internal/storage/partitions.go` | Perde `MaintainChecksPartitions`. |
| `internal/targets/{target,service}.go` | `Series`, `Rollup`, `Resolution`, `MaxRollupWindow`; `Checks` escolhe a tabela. |
| `internal/notify/service.go` | Status `failing`, `LastFailureAt`, `LastError`. |
| `internal/notify/dispatcher.go` | Saúde do canal; série por canal; 429 com `refund`; adiamento. |
| `internal/statuspage/statuspage.go` | Tipos e validação do cadastro. |
| `internal/statuspage/service.go` | `Service`: `Get`, `Create`, `Update`. |
| `internal/statuspage/public.go` | Projeção pública, estado, barra, cache. |
| `internal/apierr/errors.go` | `status_page_exists`, `slug_taken`. |
| `internal/httpapi/statuspagehandlers.go` | `GET/POST /status-pages`, `PATCH /status-pages/{id}`. |
| `internal/httpapi/publicpage.go` + `templates/status.html` | `GET /p/{slug}`: cadeia, CSP, cache, apresentação. |
| `internal/httpapi/{targethandlers,channelhandlers,routes}.go` | `resolution`; `last_error`; rotas novas. |
| `cmd/vigilume/main.go` | `RunMaintenance`; `StatusPages` no `Deps`. |
| `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md` | O que atravessa para o Plano 5. |

---

## Ordem das tarefas e seu motivo

1. **Schema e escopo público:** tudo depende das tabelas, e a RLS pública é a maior superfície de vazamento; é provada antes de existir código que a use.
2. **Manutenção:** os rollups têm de existir antes de alguém lê-los (Tasks 3 e 8).
3. **D6:** o primeiro leitor dos rollups, pequeno e isolado.
4. **Canal `failing`** e 5. **apagão em massa:** as duas pendências do Plano 3, no mesmo arquivo, em sequência para que cada diff seja pequeno.
6. **Cadastro da página** e 7. **API do dono:** o domínio antes da borda HTTP.
8. **Projeção pública** e 9. **HTML:** a projeção é testável sem HTTP; o HTML só apresenta.
10. **Ponta a ponta e documentação.**
11. **Correções da revisão final:** uma revisão independente do branch inteiro, depois da Task 10, achou sete defeitos e dois testes fracos (D27–D32).

**Ambiente de teste.** Os testes com banco sobem PostgreSQL 16 por `testcontainers-go` (Docker precisa estar rodando). O `sqlc` é o v1.31.1 (`sqlc version`).

---

### Task 1: Schema, escopo público e consultas

A migration 00006 e o quarto escopo de RLS. Nenhum código de aplicação usa as tabelas ainda; os testes desta tarefa provam a RLS direto no banco.

**Files:**
- Create: `internal/storage/migrations/00006_statuspage_rollups.sql`
- Create: `internal/storage/queries/statuspages.sql`
- Modify: `internal/storage/queries/notifications.sql` (`created_at` na reivindicação, `refund`, saúde do canal)
- Modify: `internal/storage/scope.go` (`ErrNoSlug`, `InPublicTx`)
- Generated: `internal/storage/db/*.go` (`sqlc generate`)
- Test: `internal/storage/statuspage_rls_test.go`

**Interfaces:**
- Consumes: `storage.Beginner`, `inTx` e os helpers de teste de `notify_rls_test.go` (`novoUsuario`, `novoAlvo`, `novoCheck`, `novoIncidente`, `novoCanal`, `novoID`, `agora`), do Plano 3.
- Produces:
  - `func InPublicTx(ctx context.Context, b Beginner, slug string, fn func(*db.Queries) error) error`; `var ErrNoSlug`.
  - Queries do dono: `GetStatusPage`, `InsertStatusPage`, `UpdateStatusPage`, `DeleteStatusPageItems`, `InsertStatusPageItem`, `ListStatusPageItems`, `OwnTargetIDs` (Task 6).
  - Queries públicas: `GetPublicPage`, `ListPublicItems`, `ListPublicDailyUptime`, `ListPublicLatency`, `ListPublicIncidents` (Task 8).
  - `ClaimDueNotificationsRow.CreatedAt`; `RetryNotificationParams.Refund` (Task 5); `MarkChannelSuccess`, `MarkChannelFailure` (Task 4).

**Contexto para quem implementa.** Leia os comentários da migration antes de mexer nela: cada política tem um motivo provado em spike. Os dois que mais importam: o `WITH CHECK` dos itens olha `targets` porque a FK ignora RLS (D4); e a política pública de `targets` passa por uma função plpgsql porque um `EXISTS` direto fecha um ciclo com esse `WITH CHECK` e o Postgres recusa com `42P17` (D3). As mudanças em `notifications.sql` são aditivas: o dispatcher atual continua compilando (o `Refund` novo tem valor zero `false`).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/storage/statuspage_rls_test.go`:

```go
package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// novaPagina cria a página do dono com os alvos dados, na ordem.
func novaPagina(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, slug string, publicada bool, alvos ...uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: id, UserID: dono, Slug: slug, Title: "Status", Published: publicada, CreatedAt: agora,
		}); err != nil {
			return err
		}
		for i, alvo := range alvos {
			if err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
				StatusPageID: id, TargetID: alvo, DisplayName: "Serviço", Position: int32(i),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("criar página: %v", err)
	}
	return id
}

// contarPublico conta as linhas de uma tabela no escopo público do slug,
// com um SELECT sem WHERE: só a política separa o que ele vê.
func contarPublico(t *testing.T, pool *pgxpool.Pool, slug, tabela string) int {
	t.Helper()
	var n int
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', $1, true)`, slug); err != nil {
		t.Fatal(err)
	}
	// tabela é sempre um literal do próprio teste.
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// O escopo público vê a página publicada do slug e, por delegação, só os
// alvos dela: um alvo do mesmo dono fora da página, a página de outro dono e
// os canais ficam de fora.
func TestPublicScopeSeesOnlyThePublishedPageTargets(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	naPagina, foraDaPagina := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, a, agora)
	alvoB := novoAlvo(t, pool, b, agora)
	novaPagina(t, pool, a, "pagina-a", true, naPagina)
	novaPagina(t, pool, b, "pagina-b", true, alvoB)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	for _, alvo := range []uuid.UUID{naPagina, foraDaPagina, alvoB} {
		novoCheck(t, pool, alvo, agora)
		novoIncidente(t, pool, alvo)
	}
	novoCanal(t, pool, a, nil, time.Time{})

	for tabela, quer := range map[string]int{
		"status_pages": 1, "status_page_items": 1, "targets": 1, "checks": 1, "incidents": 1,
		"notif_channels": 0, "notifications": 0, "channel_health": 0,
	} {
		if n := contarPublico(t, pool, "pagina-a", tabela); n != quer {
			t.Errorf("no escopo público de pagina-a, %s mostrou %d linhas, queria %d", tabela, n, quer)
		}
	}

	err := storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
		if _, err := q.GetPublicPage(ctx, "pagina-b"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("com o slug de A, a página de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDraftPageIsInvisibleToThePublic(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	novaPagina(t, pool, a, "rascunho", false, novoAlvo(t, pool, a, agora))

	for _, tabela := range []string{"status_pages", "status_page_items", "targets"} {
		if n := contarPublico(t, pool, "rascunho", tabela); n != 0 {
			t.Errorf("página não publicada: %s mostrou %d linhas ao público", tabela, n)
		}
	}
}

// As políticas que delegam a targets são FOR ALL: sem a transação READ ONLY,
// o escopo público poderia fechar o incidente de um alvo da página.
func TestPublicScopeIsReadOnly(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	inc := novoIncidente(t, pool, alvo)
	novaPagina(t, pool, a, "pagina-a", true, alvo)

	err := storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: inc, ResolvedAt: agora.Add(time.Minute)})
		return err
	})
	if codigoPG(err) != "25006" {
		t.Errorf("escrita no escopo público deu %v, queria 25006 (read-only transaction)", err)
	}
	if err := storage.InPublicTx(ctx, pool, "", func(*db.Queries) error { return nil }); !errors.Is(err, storage.ErrNoSlug) {
		t.Errorf("slug vazio deu %v, queria ErrNoSlug", err)
	}
}

// A FK ignora RLS: sem o WITH CHECK nos itens, A poria na própria página
// o alvo de B, e a página pública de A mostraria o estado do alvo de B.
func TestOwnerCannotPutAnotherUsersTargetOnThePage(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	pagina := novaPagina(t, pool, a, "pagina-a", true)
	alvoB := novoAlvo(t, pool, b, agora)

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		return q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: alvoB, DisplayName: "roubado", Position: 0,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A pondo o alvo de B na página deu %v, queria 42501", err)
	}
}

// Uma página por usuário (spec §2.1), slug único entre todos, e o formato
// do slug também no banco.
func TestStatusPageConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novaPagina(t, pool, a, "pagina-a", false)

	inserir := func(dono uuid.UUID, slug string) error {
		return storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
			return q.InsertStatusPage(ctx, db.InsertStatusPageParams{
				ID: novoID(), UserID: dono, Slug: slug, Title: "x", CreatedAt: agora,
			})
		})
	}
	if err := inserir(a, "outra-de-a"); codigoPG(err) != "23505" {
		t.Errorf("segunda página de A deu %v, queria 23505", err)
	}
	if err := inserir(b, "pagina-a"); codigoPG(err) != "23505" {
		t.Errorf("slug repetido por B deu %v, queria 23505", err)
	}
	for _, slug := range []string{"ab", "-abc", "abc-", "ABC", "a_b_c", "com espaço"} {
		if err := inserir(b, slug); codigoPG(err) != "23514" {
			t.Errorf("slug %q deu %v, queria 23514 (CHECK)", slug, err)
		}
	}
}

// A política pública não muda o que o dono vê: fora do escopo público, ela
// não casa com nada.
func TestPublicPolicyDoesNotWidenOwnerScope(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora)
	novaPagina(t, pool, b, "pagina-b", true, novoAlvo(t, pool, b, agora))

	if n := contarComEscopo(t, pool, a, "targets"); n != 1 {
		t.Errorf("A viu %d alvos com a página de B publicada, queria 1", n)
	}
}

// channel_health segue notifications: o dono do canal e o sondador; o
// escopo verify, que enxerga o canal, não.
func TestChannelHealthFollowsOwnerOrProber(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	h := hashDe("token-a")
	canal := novoCanal(t, pool, a, h, agora.Add(24*time.Hour))
	motivo := "rejected"
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: canal, Now: agora, LastError: &motivo})
	})
	if err != nil {
		t.Fatal(err)
	}

	if n := contarComEscopo(t, pool, a, "channel_health"); n != 1 {
		t.Errorf("o dono viu %d linhas de channel_health, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "channel_health"); n != 0 {
		t.Errorf("outro usuário viu %d linhas de channel_health", n)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.verify_token_hash', encode($1::bytea, 'hex'), true)`, h); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_health`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("o escopo verify viu %d linhas de channel_health", n)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/storage/`
Expected: FAIL de compilação: `q.InsertStatusPage undefined`, `undefined: db.InsertStatusPageParams`.

- [ ] **Step 3: Escrever a migration**

Crie `internal/storage/migrations/00006_statuspage_rollups.sql`:

```sql
-- +goose Up
-- Rollup horário dos checks (spec §4.1, §9.1, §12): o que sobra de um alvo
-- depois que a partição bruta vence. ok conta os checks que não foram down;
-- as latências são só desses, e ficam NULL numa hora sem nenhum sucesso.
CREATE TABLE check_rollup_1h (
    target_id uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    bucket    timestamptz NOT NULL,
    total     int NOT NULL CHECK (total > 0),
    ok        int NOT NULL CHECK (ok BETWEEN 0 AND total),
    p50       int,
    p95       int,
    p99       int,
    max       int,
    PRIMARY KEY (target_id, bucket),
    -- A hora cheia em UTC. date_trunc em timestamptz usa o TimeZone da
    -- sessão, e num fuso de meia hora (Índia) a "hora cheia" seria outra.
    CHECK (extract(epoch FROM bucket)::bigint % 3600 = 0),
    CHECK ((ok = 0) = (p50 IS NULL))
);

-- Retenção (DELETE por bucket) e marca d'água da consolidação (max(bucket)).
CREATE INDEX check_rollup_bucket_idx ON check_rollup_1h (bucket);

ALTER TABLE check_rollup_1h ENABLE ROW LEVEL SECURITY;
ALTER TABLE check_rollup_1h FORCE ROW LEVEL SECURITY;
CREATE POLICY check_rollup_via_target ON check_rollup_1h
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));

-- Status page (spec §4.1, §10). Uma por usuário (spec §2.1): o UNIQUE em
-- user_id é a garantia, e a API responde 409 na segunda.
CREATE TABLE status_pages (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    -- 3 a 40 caracteres, minúsculas, dígitos e hífen, sem hífen nas pontas.
    -- Vira caminho de URL (/p/{slug}), e por isso a regra fica também no banco.
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    published  bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);

ALTER TABLE status_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_pages FORCE ROW LEVEL SECURITY;
CREATE POLICY status_pages_owner ON status_pages
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
-- Escopo público (storage.InPublicTx): enxerga só a página PUBLICADA daquele
-- slug. Rascunho e página de outro slug não existem para ele.
CREATE POLICY status_pages_public_select ON status_pages FOR SELECT
    USING (published AND slug = NULLIF(current_setting('app.public_slug', true), ''));

CREATE TABLE status_page_items (
    status_page_id uuid NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    target_id      uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    display_name   text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 100),
    position       int  NOT NULL CHECK (position >= 0),
    PRIMARY KEY (status_page_id, target_id),
    UNIQUE (status_page_id, position)
);

CREATE INDEX status_page_items_target_idx ON status_page_items (target_id);

-- O USING delega à página e NÃO olha targets: a política de targets abaixo
-- consulta esta tabela, e as duas se referindo uma à outra seria recursão
-- infinita. O WITH CHECK olha o alvo: a FK ignora RLS, e sem esta checagem o
-- dono poderia pôr na própria página o alvo de outro usuário.
ALTER TABLE status_page_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_page_items FORCE ROW LEVEL SECURITY;
CREATE POLICY status_page_items_via_page ON status_page_items
    USING (EXISTS (SELECT 1 FROM status_pages p WHERE p.id = status_page_items.status_page_id))
    WITH CHECK (EXISTS (SELECT 1 FROM status_pages p WHERE p.id = status_page_items.status_page_id)
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = status_page_items.target_id));

-- O escopo público enxerga os alvos da página publicada daquele slug, e por
-- delegação os checks, rollups e incidentes deles. Só SELECT, e o escopo
-- público só existe em transação READ ONLY: as políticas FOR ALL que delegam
-- a targets (incidents, checks) não viram porta de escrita.
--
-- A consulta aos itens fica numa função plpgsql, e não num EXISTS direto na
-- política. O WITH CHECK dos itens olha targets, e targets olhando os itens
-- fecharia um ciclo: o Postgres recusa com "infinite recursion detected in
-- policy" (42P17, provado em spike) ao expandir as políticas. Uma função
-- plpgsql não é expandida na reescrita; ela roda com o papel de quem chama,
-- e a RLS dos itens vale dentro dela.
-- +goose StatementBegin
CREATE FUNCTION target_on_public_page(p_target uuid) RETURNS bool
LANGUAGE plpgsql STABLE AS $$
BEGIN
    RETURN EXISTS (SELECT 1 FROM status_page_items i WHERE i.target_id = p_target);
END
$$;
-- +goose StatementEnd

CREATE POLICY targets_public_select ON targets FOR SELECT
    USING (NULLIF(current_setting('app.public_slug', true), '') IS NOT NULL
           AND target_on_public_page(targets.id));

-- Saúde do canal (pendência do Plano 3): o dispatcher grava o último sucesso
-- e a última falha definitiva, e a API mostra "failing". Tabela à parte, como
-- target_health: o sondador não ganha UPDATE em notif_channels.
CREATE TABLE channel_health (
    channel_id      uuid PRIMARY KEY REFERENCES notif_channels(id) ON DELETE CASCADE,
    last_success_at timestamptz,
    last_failure_at timestamptz,
    last_error      text CHECK (last_error IN ('timeout', 'network', 'rate_limited', 'rejected',
                                               'server_error', 'invalid_channel', 'unknown'))
);

ALTER TABLE channel_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_health FORCE ROW LEVEL SECURITY;
-- Como notifications: o canal visível NÃO basta (o escopo verify o vê).
CREATE POLICY channel_health_owner_or_prober ON channel_health
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                   AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                        OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                        AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                             OR current_setting('app.scope', true) = 'prober')));

-- Retenção das notificações entregues, falhas e canceladas (created_at < corte).
CREATE INDEX notifications_created_idx ON notifications (created_at) WHERE status <> 'pending';
-- Retenção de audit_log (12 meses) e de sessões vencidas.
CREATE INDEX audit_log_at_idx ON audit_log (at);

-- Retenção de checks (spec §12): apaga as partições mensais que terminam
-- antes de p_cutoff. A partição que contém o corte fica, então os checks
-- duram entre 30 e 61 dias. Mesma trava de ensure_checks_partition: criar e
-- apagar partição nunca correm juntos.
-- +goose StatementBegin
CREATE FUNCTION drop_checks_partitions_before(p_cutoff timestamptz) RETURNS int
LANGUAGE plpgsql AS $$
DECLARE
    parte  record;
    inicio date;
    apagadas int := 0;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('vigilume.ensure_checks_partition', 0));
    FOR parte IN
        SELECT c.relname FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'checks'::regclass
           AND c.relname ~ '^checks_[0-9]{4}_[0-9]{2}$'
    LOOP
        inicio := to_date(substr(parte.relname, 8), 'YYYY_MM');
        IF ((inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC') <= p_cutoff THEN
            EXECUTE format('DROP TABLE %I', parte.relname);
            apagadas := apagadas + 1;
        END IF;
    END LOOP;
    RETURN apagadas;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION drop_checks_partitions_before(timestamptz);
DROP INDEX audit_log_at_idx;
DROP INDEX notifications_created_idx;
DROP TABLE channel_health;
DROP POLICY targets_public_select ON targets;
DROP FUNCTION target_on_public_page(uuid);
DROP TABLE status_page_items;
DROP TABLE status_pages;
DROP TABLE check_rollup_1h;
```

- [ ] **Step 4: Escrever as consultas e gerar**

Crie `internal/storage/queries/statuspages.sql`:

```sql
-- Status page (spec §10): as do dono rodam sob InUserTx e filtram por
-- user_id; as públicas, sob InPublicTx (somente leitura, escopo do slug).

-- name: GetStatusPage :one
SELECT * FROM status_pages WHERE user_id = $1;

-- name: InsertStatusPage :exec
INSERT INTO status_pages (id, user_id, slug, title, published, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateStatusPage :execrows
UPDATE status_pages SET slug = $3, title = $4, published = $5
 WHERE user_id = $1 AND id = $2;

-- Os itens são trocados inteiros a cada escrita: apagar e reinserir, na
-- mesma transação.
-- name: DeleteStatusPageItems :exec
DELETE FROM status_page_items i
 USING status_pages p
 WHERE p.id = i.status_page_id AND p.user_id = sqlc.arg(user_id) AND p.id = sqlc.arg(page_id);

-- name: InsertStatusPageItem :exec
INSERT INTO status_page_items (status_page_id, target_id, display_name, position)
VALUES ($1, $2, $3, $4);

-- name: ListStatusPageItems :many
SELECT i.target_id, i.display_name, i.position
  FROM status_page_items i
  JOIN status_pages p ON p.id = i.status_page_id
 WHERE p.user_id = sqlc.arg(user_id) AND p.id = sqlc.arg(page_id)
 ORDER BY i.position;

-- Os alvos do dono entre os ids pedidos: a validação dos itens diz qual id
-- não é dele.
-- name: OwnTargetIDs :many
SELECT id FROM targets WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::uuid[]);

-- Públicas. Nenhuma devolve URL, headers, user_id nem status code: a
-- projeção só tem o que a spec §5.6 permite expor.

-- name: GetPublicPage :one
SELECT id, title FROM status_pages WHERE slug = sqlc.arg(slug) AND published;

-- O estado atual é o do check mais recente. interval_sec e enabled servem só
-- para decidir se esse check ainda é recente; não saem na página. Alvo sem
-- check nenhum volta com last_status '' e last_checked_at na época Unix.
-- name: ListPublicItems :many
SELECT i.target_id, i.display_name, t.enabled, t.interval_sec,
       coalesce(lc.status, '')::text AS last_status,
       coalesce(lc.checked_at, 'epoch'::timestamptz)::timestamptz AS last_checked_at
  FROM status_page_items i
  JOIN targets t ON t.id = i.target_id
  LEFT JOIN LATERAL (
       SELECT c.status, c.checked_at FROM checks c
        WHERE c.target_id = i.target_id
        ORDER BY c.checked_at DESC LIMIT 1) lc ON true
 WHERE i.status_page_id = sqlc.arg(page_id)
 ORDER BY i.position;

-- Uptime diário (UTC) dos alvos da página, a partir dos rollups.
-- name: ListPublicDailyUptime :many
SELECT r.target_id,
       to_timestamp(floor(extract(epoch FROM r.bucket) / 86400) * 86400)::timestamptz AS day,
       sum(r.total)::bigint AS total, sum(r.ok)::bigint AS ok
  FROM check_rollup_1h r
  JOIN status_page_items i ON i.target_id = r.target_id AND i.status_page_id = sqlc.arg(page_id)
 WHERE r.bucket >= sqlc.arg(since)::timestamptz
 GROUP BY 1, 2
 ORDER BY 1, 2;

-- Latência mediana aproximada: média das medianas horárias, ponderada pelos
-- sucessos de cada hora.
-- name: ListPublicLatency :many
SELECT r.target_id, (sum(r.p50::bigint * r.ok) / nullif(sum(r.ok), 0))::int AS p50
  FROM check_rollup_1h r
  JOIN status_page_items i ON i.target_id = r.target_id AND i.status_page_id = sqlc.arg(page_id)
 WHERE r.bucket >= sqlc.arg(since)::timestamptz AND r.ok > 0
 GROUP BY 1;

-- Incidentes resolvidos dos alvos da página: só o nome de exibição, o início
-- e o fim. A causa não sai (spec §5.6).
-- name: ListPublicIncidents :many
SELECT i.display_name, x.started_at, x.resolved_at::timestamptz AS resolved_at
  FROM incidents x
  JOIN status_page_items i ON i.target_id = x.target_id AND i.status_page_id = sqlc.arg(page_id)
 WHERE x.resolved_at IS NOT NULL AND x.started_at >= sqlc.arg(since)::timestamptz
 ORDER BY x.started_at DESC
 LIMIT sqlc.arg(max_rows)::int;
```

Aplique em `internal/storage/queries/notifications.sql`:

```diff
--- a/internal/storage/queries/notifications.sql
+++ b/internal/storage/queries/notifications.sql
@@ -14,9 +14,9 @@
             ORDER BY next_attempt_at
             LIMIT sqlc.arg(max_rows)::int
               FOR UPDATE SKIP LOCKED)
-    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts
+    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts, n.created_at
 )
-SELECT cl.id, cl.event, cl.attempts,
+SELECT cl.id, cl.event, cl.attempts, cl.created_at,
        c.id AS channel_id, c.user_id, c.kind, c.config_enc, c.enabled, c.verified_at,
        i.id AS incident_id, i.started_at, i.resolved_at, i.cause,
        t.name AS target_name
@@ -29,8 +29,13 @@
 UPDATE notifications SET status = 'sent', sent_at = sqlc.arg(now)::timestamptz, last_error = NULL
  WHERE id = sqlc.arg(id);
 
+-- refund devolve a tentativa que a reivindicação contou: um 429 é o
+-- provedor pedindo espera, não uma falha do envio (Plano 4, apagão em massa).
 -- name: RetryNotification :exec
-UPDATE notifications SET next_attempt_at = $2, last_error = $3 WHERE id = $1;
+UPDATE notifications
+   SET next_attempt_at = sqlc.arg(next_attempt_at), last_error = sqlc.narg(last_error),
+       attempts = attempts - CASE WHEN sqlc.arg(refund)::bool THEN 1 ELSE 0 END
+ WHERE id = sqlc.arg(id);
 
 -- status é 'failed' ou 'cancelled'.
 -- name: CloseNotification :exec
@@ -40,3 +45,14 @@
 -- tem RLS (D7 do Plano 2).
 -- name: InsertAudit :exec
 INSERT INTO audit_log (id, user_id, action, entity, entity_id, at) VALUES ($1, $2, $3, $4, $5, $6);
+
+-- Saúde do canal (Plano 4): o último sucesso e a última falha definitiva.
+-- name: MarkChannelSuccess :exec
+INSERT INTO channel_health (channel_id, last_success_at) VALUES (sqlc.arg(channel_id), sqlc.arg(now)::timestamptz)
+ON CONFLICT (channel_id) DO UPDATE SET last_success_at = EXCLUDED.last_success_at;
+
+-- name: MarkChannelFailure :exec
+INSERT INTO channel_health (channel_id, last_failure_at, last_error)
+VALUES (sqlc.arg(channel_id), sqlc.arg(now)::timestamptz, sqlc.arg(last_error))
+ON CONFLICT (channel_id) DO UPDATE
+   SET last_failure_at = EXCLUDED.last_failure_at, last_error = EXCLUDED.last_error;
```

Run: `sqlc generate && sqlc diff`
Expected: sem saída.

- [ ] **Step 5: `InPublicTx`**

Aplique em `internal/storage/scope.go`:

```diff
--- a/internal/storage/scope.go
+++ b/internal/storage/scope.go
@@ -23,6 +23,7 @@
 var (
 	ErrNoUser  = errors.New("transação de usuário sem usuário")
 	ErrNoToken = errors.New("transação de verificação sem hash de token")
+	ErrNoSlug  = errors.New("transação pública sem slug")
 )
 
 // InUserTx roda fn numa transação em que a RLS só enxerga as linhas de userID.
@@ -62,6 +63,37 @@
 	return inTx(ctx, b, `SELECT set_config('app.verify_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
 }
 
+// InPublicTx roda fn no escopo público da status page (GET /p/{slug}): a
+// RLS enxerga a página publicada daquele slug, os itens dela e, por
+// delegação, os alvos, checks, rollups e incidentes desses itens. Nada mais.
+//
+// A transação é READ ONLY. As políticas que delegam a targets são FOR ALL
+// (incidents, checks, target_health), e sem isto o escopo público seria
+// também porta de escrita nelas. O Postgres recusa qualquer escrita com
+// 25006, seja qual for a política.
+func InPublicTx(ctx context.Context, b Beginner, slug string, fn func(*db.Queries) error) error {
+	if slug == "" {
+		return ErrNoSlug
+	}
+	tx, err := b.Begin(ctx)
+	if err != nil {
+		return fmt.Errorf("abrir transação: %w", err)
+	}
+	defer tx.Rollback(ctx) // no-op depois de Commit
+
+	// Tem de ser o primeiro comando da transação.
+	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
+		return fmt.Errorf("transação somente leitura: %w", err)
+	}
+	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', $1, true)`, slug); err != nil {
+		return fmt.Errorf("definir escopo da RLS: %w", err)
+	}
+	if err := fn(db.New(tx)); err != nil {
+		return err
+	}
+	return tx.Commit(ctx)
+}
+
 func inTx(ctx context.Context, b Beginner, setSQL, valor string, fn func(*db.Queries) error) error {
 	tx, err := b.Begin(ctx)
 	if err != nil {
```

- [ ] **Step 6: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/storage/`
Expected: PASS, e o `gofmt -l` sem saída. As suítes do Plano 3 continuam verdes (a migration só acrescenta).

- [ ] **Step 7: Sabotagem**

Uma de cada vez; depois de cada uma, `sqlc generate` quando mexer em SQL, rode `go test -count=1 -run 'TestPublicScope|TestDraft|TestOwner|TestStatusPage|TestChannelHealth' ./internal/storage/`, e restaure:
1. Em `InPublicTx`, troque `SET TRANSACTION READ ONLY` por `SELECT 1`. Expected: FAIL em `TestPublicScopeIsReadOnly`.
2. Na migration, tire `AND EXISTS (SELECT 1 FROM targets t WHERE t.id = status_page_items.target_id)` do `WITH CHECK` dos itens. Expected: FAIL em `TestOwnerCannotPutAnotherUsersTargetOnThePage`.
3. Na política `status_pages_public_select`, tire `published AND`. Expected: FAIL em `TestDraftPageIsInvisibleToThePublic`.
4. No `USING` de `channel_health_owner_or_prober`, tire a condição de dono/prober (deixe só o `EXISTS` do canal). Expected: FAIL em `TestChannelHealthFollowsOwnerOrProber`.

Registre no relatório cada quebra e a falha vista.

- [ ] **Step 8: Commit**

```bash
git add internal/storage/
git commit -m "feat(storage): schema da status page, rollups e saúde do canal, com o escopo público de RLS"
```

---

### Task 2: Manutenção — rollups e retenção

`RunMaintenance` substitui `MaintainChecksPartitions`: a cada 15 minutos (e ao subir), garante as partições, consolida os rollups e aplica a retenção da spec §12.

**Files:**
- Create: `internal/storage/queries/maintenance.sql`
- Create: `internal/storage/maintenance.go`
- Modify: `internal/storage/partitions.go` (remove `MaintainChecksPartitions`)
- Modify: `cmd/vigilume/main.go` (`manutencaoACada`, `RunMaintenance`)
- Generated: `internal/storage/db/*.go`
- Test: `internal/storage/maintenance_test.go`

**Interfaces:**
- Consumes: `check_rollup_1h` e `drop_checks_partitions_before` (Task 1); `EnsureChecksPartitions`, `InProberTx`, `InUserTx`.
- Produces:
  - `func Maintain(ctx, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error)`
  - `func ConsolidateRollups(ctx, pool *pgxpool.Pool, now time.Time) (int64, error)` — a Task 3 e a Task 10 usam.
  - `func ApplyRetention(ctx, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error)`
  - `func RunMaintenance(ctx, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error`; `var ErrMaintenanceInterval`.
  - Constantes `ChecksRetention`, `RollupRetentionMonths`, `AuditRetentionMonths`, `SessionGrace`, `NotificationsRetention`.
  - Query `ListRollups` (Task 3).

**Contexto para quem implementa.** A consolidação é idempotente: rodar de novo sobre a mesma janela reescreve os mesmos números, e é isso que permite voltar 2 horas a cada rodada para pegar check atrasado (D6). `percentile_disc ... FILTER (WHERE status <> 'down')` é o que deixa as latências só dos sucessos. `audit_log` e `sessions` não têm RLS (D7 do Plano 2), por isso as queries delas rodam no escopo prober sem filtro.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/storage/maintenance_test.go`:

```go
package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// hora devolve 2026-09-22 às h:m UTC.
func hora(h, m int) time.Time { return time.Date(2026, 9, 22, h, m, 0, 0, time.UTC) }

func checkEm(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, em time.Time, status string, latencia int32) {
	t.Helper()
	ctx := context.Background()
	p := db.InsertCheckParams{ID: novoID(), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: latencia}
	if status == "down" {
		k := "timeout"
		p.ErrorKind = &k
	}
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
		t.Fatalf("gravar check: %v", err)
	}
}

func rollupsDe(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) []db.ListRollupsRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListRollupsRow
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListRollups(ctx, db.ListRollupsParams{
			UserID: dono, TargetID: alvo, FromAt: hora(0, 0).AddDate(0, 0, -40), ToAt: hora(0, 0).AddDate(0, 0, 40),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func consolidar(t *testing.T, pool *pgxpool.Pool, em time.Time) {
	t.Helper()
	if _, err := storage.ConsolidateRollups(context.Background(), pool, em); err != nil {
		t.Fatal(err)
	}
}

func valor(p *int32) int32 {
	if p == nil {
		return -1
	}
	return *p
}

// Só horas cheias entram; a hora corrente fica para a próxima rodada. ok
// conta os que não foram down, e as latências são só deles.
func TestConsolidateRollupsAggregatesClosedHours(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	checkEm(t, pool, alvo, hora(10, 20), "degraded", 300)
	checkEm(t, pool, alvo, hora(10, 40), "down", 5000)
	checkEm(t, pool, alvo, hora(11, 10), "up", 50) // hora corrente

	consolidar(t, pool, hora(11, 30))

	rs := rollupsDe(t, pool, dono, alvo)
	if len(rs) != 1 {
		t.Fatalf("%d rollups, queria 1 (só a hora das 10)", len(rs))
	}
	r := rs[0]
	if !r.Bucket.Equal(hora(10, 0)) || r.Total != 3 || r.Ok != 2 ||
		valor(r.P50) != 100 || valor(r.P95) != 300 || valor(r.Max) != 300 {
		t.Errorf("rollup = %+v p50=%d p95=%d max=%d; queria 10:00, 3 checks, 2 ok, p50 100, p95 e max 300 (o down fora das latências)",
			r, valor(r.P50), valor(r.P95), valor(r.Max))
	}
}

// Rodar de novo reescreve os mesmos números, e um check que chegou atrasado
// na hora já consolidada entra na rodada seguinte.
func TestConsolidationIsIdempotentAndPicksUpLateChecks(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	consolidar(t, pool, hora(11, 0))
	consolidar(t, pool, hora(11, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Total != 1 {
		t.Fatalf("depois de duas rodadas: %+v", rs)
	}

	checkEm(t, pool, alvo, hora(10, 55), "up", 200) // atrasado
	consolidar(t, pool, hora(11, 15))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Total != 2 || rs[0].Ok != 2 {
		t.Errorf("o check atrasado não entrou: %+v", rs)
	}
}

func TestHourWithoutSuccessHasNoLatency(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "down", 5000)
	consolidar(t, pool, hora(11, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Ok != 0 || rs[0].P50 != nil || rs[0].Max != nil {
		t.Errorf("hora só com falhas: %+v", rs)
	}
}

// Depois de um sondador parado por dias, a rodada seguinte consolida o
// buraco inteiro, e não só as últimas horas.
func TestConsolidationCatchesUpAfterAGap(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(1, 5), "up", 100)
	consolidar(t, pool, hora(2, 0))
	checkEm(t, pool, alvo, hora(15, 5), "up", 100)
	checkEm(t, pool, alvo, hora(20, 5), "up", 100)
	consolidar(t, pool, hora(21, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 3 {
		t.Errorf("%d rollups depois do buraco, queria 3 (01h, 15h e 20h)", len(rs))
	}
}

// Rollups não atravessam donos: a RLS delega ao alvo.
func TestRollupsFollowTheTarget(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	consolidar(t, pool, hora(11, 0))
	if n := contarComEscopo(t, pool, a, "check_rollup_1h"); n != 1 {
		t.Errorf("o dono viu %d rollups, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "check_rollup_1h"); n != 0 {
		t.Errorf("outro usuário viu %d rollups", n)
	}
}

func existeTabela(t *testing.T, pool *pgxpool.Pool, nome string) bool {
	t.Helper()
	var existe bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)`, nome).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	return existe
}

// Retenção (spec §12), cada item com o que sai E o que fica: sem o lado do
// que fica, um DELETE sem WHERE passaria.
func TestRetentionRemovesOnlyWhatExpired(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hoje := hora(12, 0) // 2026-09-22
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	// Partições de julho a outubro. Corte dos checks: 2026-08-23. Julho
	// termina antes (sai); agosto termina depois (fica).
	for _, mes := range []time.Time{hora(0, 0).AddDate(0, -2, 0), hora(0, 0).AddDate(0, -1, 0), hoje} {
		if err := storage.EnsureChecksPartitions(ctx, pool, mes); err != nil {
			t.Fatal(err)
		}
	}

	// Rollups: um de 14 meses atrás (sai), um de 12 (fica).
	for _, meses := range []int{-14, -12} {
		b := hoje.AddDate(0, meses, 0)
		if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, b); err != nil {
			t.Fatal(err)
		}
		checkEm(t, pool, alvo, b.Add(5*time.Minute), "up", 10)
		consolidarJanela(t, pool, b, b.Add(time.Hour))
	}

	// Notificações: entregue velha (sai), pendente velha (fica), entregue nova (fica).
	inc := novoIncidente(t, pool, alvo)
	velhaEntregue := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -40))
	velhaPendente := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -40))
	novaEntregue := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -10))
	for _, id := range []uuid.UUID{velhaEntregue, novaEntregue} {
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: id, Now: hoje})
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Auditoria: 13 meses (sai), 11 (fica). Sessões: vencida há 8 dias
	// (sai), há 6 (fica).
	for _, meses := range []int{-13, -11} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, user_id, action, at) VALUES ($1, $2, 'x', $3)`,
			novoID(), dono, hoje.AddDate(0, meses, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for _, dias := range []int{-8, -6} {
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, novoID(), novoID().String(), hoje.AddDate(0, 0, dias)); err != nil {
			t.Fatal(err)
		}
	}

	r, err := storage.ApplyRetention(ctx, pool, hoje)
	if err != nil {
		t.Fatal(err)
	}
	if r.DroppedPartitions < 1 || r.DeletedRollups != 1 || r.DeletedNotifications != 1 || r.DeletedAudit != 1 || r.DeletedSessions != 1 {
		t.Errorf("relatório = %+v; queria 1 de cada (e ao menos a partição de julho)", r)
	}
	if existeTabela(t, pool, "checks_2026_07") {
		t.Error("a partição de julho sobreviveu ao corte de 30 dias")
	}
	if !existeTabela(t, pool, "checks_2026_08") {
		t.Error("a partição de agosto, que ainda tem checks dentro dos 30 dias, foi apagada")
	}
	rollups := contarComoSondador(t, pool, `SELECT count(*) FROM check_rollup_1h`)
	sessoes := contarComoSondador(t, pool, `SELECT count(*) FROM sessions`)
	auditoria := contarComoSondador(t, pool, `SELECT count(*) FROM audit_log`)
	notifs := contarComoSondador(t, pool, `SELECT count(*) FROM notifications`)
	pendentes := contarComoSondador(t, pool, `SELECT count(*) FROM notifications WHERE status = 'pending'`)
	if rollups != 1 || sessoes != 1 || auditoria != 1 || notifs != 2 || pendentes != 1 {
		t.Errorf("sobraram rollups=%d sessões=%d auditoria=%d notificações=%d (pendentes %d); queria 1, 1, 1, 2 (1 pendente)",
			rollups, sessoes, auditoria, notifs, pendentes)
	}
	_ = velhaPendente
}

func consolidarJanela(t *testing.T, pool *pgxpool.Pool, de, ate time.Time) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.ConsolidateRollups(ctx, db.ConsolidateRollupsParams{FromAt: de, ToAt: ate})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func notificacaoEm(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID, em time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: "opened", Now: em,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// contarComoSondador conta no escopo do sondador, que enxerga as linhas de
// todos os donos (audit_log e sessions nem têm RLS).
func contarComoSondador(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunMaintenanceRejectsANonPositiveInterval(t *testing.T) {
	err := storage.RunMaintenance(context.Background(), nil, clock.NewFake(agora), 0)
	if !errors.Is(err, storage.ErrMaintenanceInterval) {
		t.Errorf("intervalo 0 deu %v, queria ErrMaintenanceInterval", err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/storage/`
Expected: FAIL de compilação: `undefined: db.ListRollupsRow`, `q.ListRollups undefined`.

- [ ] **Step 3: Consultas**

Crie `internal/storage/queries/maintenance.sql`:

```sql
-- Manutenção (spec §12): rodam sob InProberTx, no job do papel prober.

-- Consolida as horas cheias de [from_at, to_at) em check_rollup_1h. É
-- idempotente: rodar de novo sobre a mesma janela reescreve os mesmos números,
-- e um check que chegou atrasado entra na próxima rodada.
-- name: ConsolidateRollups :execrows
INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
SELECT c.target_id,
       to_timestamp(floor(extract(epoch FROM c.checked_at) / 3600) * 3600),
       count(*),
       count(*) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.50) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.95) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.99) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       max(c.latency_ms) FILTER (WHERE c.status <> 'down')
  FROM checks c
 WHERE c.checked_at >= sqlc.arg(from_at)::timestamptz
   AND c.checked_at < sqlc.arg(to_at)::timestamptz
 GROUP BY 1, 2
ON CONFLICT (target_id, bucket) DO UPDATE
   SET total = EXCLUDED.total, ok = EXCLUDED.ok, p50 = EXCLUDED.p50,
       p95 = EXCLUDED.p95, p99 = EXCLUDED.p99, max = EXCLUDED.max;

-- A marca d'água: a hora mais recente já consolidada. Sem rollup nenhum,
-- devolve a época Unix.
-- name: LatestRollupBucket :one
SELECT coalesce(max(bucket), 'epoch'::timestamptz)::timestamptz FROM check_rollup_1h;

-- name: DropChecksPartitionsBefore :one
SELECT drop_checks_partitions_before(sqlc.arg(cutoff)::timestamptz)::int;

-- name: DeleteOldRollups :execrows
DELETE FROM check_rollup_1h WHERE bucket < sqlc.arg(cutoff)::timestamptz;

-- Entregues, falhas e canceladas; pendente nunca sai por idade.
-- name: DeleteOldNotifications :execrows
DELETE FROM notifications WHERE status <> 'pending' AND created_at < sqlc.arg(cutoff)::timestamptz;

-- audit_log e sessions não têm RLS (D7 do Plano 2).
-- name: DeleteOldAudit :execrows
DELETE FROM audit_log WHERE at < sqlc.arg(cutoff)::timestamptz;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < sqlc.arg(cutoff)::timestamptz;

-- D6 do Plano 2: GET /targets/{id}/checks acima de 48h lê daqui. Roda sob
-- InUserTx e filtra por user_id, como ListChecks.
-- name: ListRollups :many
SELECT r.bucket, r.total, r.ok, r.p50, r.p95, r.p99, r.max
  FROM check_rollup_1h r
  JOIN targets t ON t.id = r.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND r.target_id = sqlc.arg(target_id)
   AND r.bucket >= sqlc.arg(from_at)
   AND r.bucket < sqlc.arg(to_at)
 ORDER BY r.bucket;
```

Run: `sqlc generate && sqlc diff`

- [ ] **Step 4: Implementar**

Crie `internal/storage/maintenance.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Retenção (spec §12).
const (
	// ChecksRetention é a idade a partir da qual uma partição mensal de
	// checks pode sair. A partição sai inteira: os checks duram entre 30 e
	// 61 dias.
	ChecksRetention        = 30 * 24 * time.Hour
	RollupRetentionMonths  = 13
	AuditRetentionMonths   = 12
	SessionGrace           = 7 * 24 * time.Hour
	NotificationsRetention = 30 * 24 * time.Hour
)

const (
	// rollupReconsolidate é quanto antes da marca d'água cada rodada volta:
	// um check que chegou atrasado ainda entra na hora dele.
	rollupReconsolidate = 2 * time.Hour
	// rollupBackfill é até onde a primeira rodada volta, sem rollup nenhum:
	// o que ainda existe de checks brutos.
	rollupBackfill = 31 * 24 * time.Hour
)

var ErrMaintenanceInterval = errors.New("intervalo de manutenção precisa ser positivo")

// MaintenanceReport diz o que uma rodada fez, para o log.
type MaintenanceReport struct {
	RollupHours          int64
	DroppedPartitions    int32
	DeletedRollups       int64
	DeletedNotifications int64
	DeletedAudit         int64
	DeletedSessions      int64
}

// Maintain é uma rodada da manutenção (spec §12): garante as partições,
// consolida os rollups e aplica a retenção. Cada passo é idempotente, e
// rodar de novo no mesmo instante não muda nada.
func Maintain(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	var r MaintenanceReport
	if err := EnsureChecksPartitions(ctx, pool, now); err != nil {
		return r, err
	}
	n, err := ConsolidateRollups(ctx, pool, now)
	if err != nil {
		return r, err
	}
	r.RollupHours = n
	ret, err := ApplyRetention(ctx, pool, now)
	if err != nil {
		return r, err
	}
	ret.RollupHours = r.RollupHours
	return ret, nil
}

// ConsolidateRollups consolida as horas cheias ainda não consolidadas, até a
// última hora completa antes de now (em UTC). Volta rollupReconsolidate antes
// da marca d'água; sem rollup nenhum, volta rollupBackfill. Devolve quantas
// linhas de rollup gravou ou reescreveu.
func ConsolidateRollups(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	ate := now.UTC().Truncate(time.Hour)
	var n int64
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		ultima, err := q.LatestRollupBucket(ctx)
		if err != nil {
			return fmt.Errorf("ler marca d'água dos rollups: %w", err)
		}
		de := ate.Add(-rollupBackfill)
		if ultima.Unix() > 0 {
			if volta := ultima.Add(-rollupReconsolidate); volta.After(de) {
				de = volta
			}
		}
		if !de.Before(ate) {
			return nil
		}
		n, err = q.ConsolidateRollups(ctx, db.ConsolidateRollupsParams{FromAt: de, ToAt: ate})
		if err != nil {
			return fmt.Errorf("consolidar rollups: %w", err)
		}
		return nil
	})
	return n, err
}

// ApplyRetention apaga o que venceu (spec §12). Pendência nunca sai por idade:
// uma notificação pendente ainda vai ser tentada.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	var r MaintenanceReport
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		if r.DroppedPartitions, err = q.DropChecksPartitionsBefore(ctx, now.Add(-ChecksRetention)); err != nil {
			return fmt.Errorf("apagar partições vencidas: %w", err)
		}
		if r.DeletedRollups, err = q.DeleteOldRollups(ctx, now.AddDate(0, -RollupRetentionMonths, 0)); err != nil {
			return fmt.Errorf("apagar rollups vencidos: %w", err)
		}
		if r.DeletedNotifications, err = q.DeleteOldNotifications(ctx, now.Add(-NotificationsRetention)); err != nil {
			return fmt.Errorf("apagar notificações vencidas: %w", err)
		}
		if r.DeletedAudit, err = q.DeleteOldAudit(ctx, now.AddDate(0, -AuditRetentionMonths, 0)); err != nil {
			return fmt.Errorf("apagar auditoria vencida: %w", err)
		}
		if r.DeletedSessions, err = q.DeleteExpiredSessions(ctx, now.Add(-SessionGrace)); err != nil {
			return fmt.Errorf("apagar sessões vencidas: %w", err)
		}
		return nil
	})
	return r, err
}

// RunMaintenance roda Maintain ao subir e depois a cada every, até ctx acabar.
// Substitui o MaintainChecksPartitions do Plano 2, que não tinha teste e dava
// panic com every <= 0. Erro de uma rodada vai ao log; a próxima tenta de novo.
func RunMaintenance(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
	if every <= 0 {
		return ErrMaintenanceInterval
	}
	rodar := func() {
		r, err := Maintain(ctx, pool, clk.Now())
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("manutenção", "erro", err)
			}
			return
		}
		slog.Info("manutenção", "rollups", r.RollupHours, "particoes_apagadas", r.DroppedPartitions,
			"rollups_apagados", r.DeletedRollups, "notificacoes_apagadas", r.DeletedNotifications,
			"auditoria_apagada", r.DeletedAudit, "sessoes_apagadas", r.DeletedSessions)
	}
	rodar()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			rodar()
		}
	}
}
```

Aplique em `internal/storage/partitions.go`:

```diff
--- a/internal/storage/partitions.go
+++ b/internal/storage/partitions.go
@@ -3,12 +3,10 @@
 import (
 	"context"
 	"fmt"
-	"log/slog"
 	"time"
 
 	"github.com/jackc/pgx/v5/pgxpool"
 
-	"github.com/BipoXDXD/vigilume/internal/clock"
 	"github.com/BipoXDXD/vigilume/internal/storage/db"
 )
 
@@ -30,22 +28,3 @@
 	}
 	return nil
 }
-
-// MaintainChecksPartitions repete EnsureChecksPartitions até ctx acabar. A
-// partição do mês seguinte existe desde o boot, então um tick diário tem um
-// mês inteiro de folga. O Plano 4 acrescenta aqui o DROP das partições
-// vencidas (spec §12).
-func MaintainChecksPartitions(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
-	t := time.NewTicker(every)
-	defer t.Stop()
-	for {
-		select {
-		case <-ctx.Done():
-			return nil
-		case <-t.C:
-			if err := EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil && ctx.Err() == nil {
-				slog.Error("manter partições de checks", "erro", err)
-			}
-		}
-	}
-}
```

Aplique em `cmd/vigilume/main.go`:

```diff
--- a/cmd/vigilume/main.go
+++ b/cmd/vigilume/main.go
@@ -65,10 +65,10 @@
 	log.Info("encerrado")
 }
 
-// manutencaoDeParticoes é o intervalo do job que garante a partição do mês
-// seguinte. A do mês seguinte já existe desde o boot, então um dia de
-// intervalo deixa um mês inteiro de folga.
-const manutencaoDeParticoes = 24 * time.Hour
+// manutencaoACada é o intervalo do job de manutenção (spec §12): partições,
+// rollups horários e retenção. Cada rodada é idempotente, e 15 minutos deixam
+// o rollup da última hora cheia disponível logo depois de ela fechar.
+const manutencaoACada = 15 * time.Minute
 
 // run é onde o wiring acontece. Mantido separado de main para que main tenha
 // apenas responsabilidade de processo (flags, sinais, código de saída).
@@ -125,7 +125,7 @@
 		grace := graceDoSondador(cfg.ShutdownGrace)
 		g.Go(func() error { return sched.Run(gctx, grace) })
 		g.Go(func() error {
-			return storage.MaintainChecksPartitions(gctx, pool, clk, manutencaoDeParticoes)
+			return storage.RunMaintenance(gctx, pool, clk, manutencaoACada)
 		})
 		// O dispatcher roda com o sondador (spec §8: fora do caminho
 		// crítico da sonda, em worker próprio). A outbox no banco é o que
```

- [ ] **Step 5: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && go test -race -count=1 ./internal/storage/ ./cmd/...`
Expected: PASS.

- [ ] **Step 6: Sabotagem**

Uma de cada vez, com `sqlc generate` quando mexer em SQL; rode `go test -count=1 -run 'TestConsolidat|TestHour|TestRollups|TestRetention|TestRunMaint' ./internal/storage/`; restaure:
1. Em `ConsolidateRollups` (SQL), troque `count(*) FILTER (WHERE c.status <> 'down')` por `count(*)`. Expected: FAIL em `TestConsolidateRollupsAggregatesClosedHours` e `TestHourWithoutSuccessHasNoLatency`.
2. Em `DeleteOldNotifications`, tire `status <> 'pending' AND`. Expected: FAIL em `TestRetentionRemovesOnlyWhatExpired`.
3. Em `ConsolidateRollups` (Go), troque `ultima.Add(-rollupReconsolidate)` por `ultima.Add(time.Hour)`. Expected: FAIL em `TestConsolidationIsIdempotentAndPicksUpLateChecks`.
4. Na função `drop_checks_partitions_before` da migration, troque `(inicio + interval '1 month')` por `inicio`. Expected: FAIL em `TestRetentionRemovesOnlyWhatExpired` (a partição que contém o corte seria apagada).

- [ ] **Step 7: Commit**

```bash
git add internal/storage/ cmd/vigilume/main.go
git commit -m "feat(storage): manutenção com rollups horários e retenção da spec §12"
```

---

### Task 3: `/checks` acima de 48h lê os rollups (D6 do Plano 2)

**Files:**
- Modify: `internal/targets/target.go` (`Rollup`, `Resolution`, `Series`, `MaxRollupWindow`)
- Modify: `internal/targets/service.go` (`Checks` devolve `Series`)
- Modify: `internal/httpapi/targethandlers.go` (`rollupResponse`, `resolution` na resposta)
- Test: `internal/targets/service_test.go`, `internal/httpapi/targets_integration_test.go`

**Interfaces:**
- Consumes: `db.ListRollups` e `storage.ConsolidateRollups` (Task 2).
- Produces:
  - `func (s *Service) Checks(ctx, userID, id uuid.UUID, from, to time.Time) (Series, error)` — a assinatura muda.
  - `type Series struct { Resolution Resolution; Checks []Check; Rollups []Rollup }`; `ResolutionRaw = "raw"`, `Resolution1h = "1h"`; `MaxRollupWindow = 400 * 24 * time.Hour`.
  - JSON: `{"resolution": "raw"|"1h", "items": [...]}`; item horário `{bucket, total, ok, p50_ms, p95_ms, p99_ms, max_ms}`.

**Contexto para quem implementa.** O pgx devolve `timestamptz` no fuso local do processo; os buckets saem em `.UTC()` porque são horas cheias em UTC por definição (D9). O teste HTTP grava um check bruto e chama `storage.ConsolidateRollups` de verdade, em vez de inserir rollup à mão: prova o caminho inteiro.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/targets/service_test.go` (troca `TestChecksWindowIsLimited` por dois testes):

```diff
--- a/internal/targets/service_test.go
+++ b/internal/targets/service_test.go
@@ -291,7 +291,9 @@
 	}
 }
 
-func TestChecksWindowIsLimited(t *testing.T) {
+// D6: até 48h a série é bruta; acima, horária (rollups); acima de
+// MaxRollupWindow, 400. As bordas são exatas dos dois lados.
+func TestChecksWindowPicksTheResolution(t *testing.T) {
 	a := novoAmbiente(t)
 	ctx := context.Background()
 	dono := a.usuario(t)
@@ -301,12 +303,77 @@
 	}
 	fim := a.clk.Now()
 
-	_, err = a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow-time.Second), fim)
+	for _, c := range []struct {
+		janela time.Duration
+		quer   targets.Resolution
+	}{
+		{targets.MaxChecksWindow, targets.ResolutionRaw},
+		{targets.MaxChecksWindow + time.Second, targets.Resolution1h},
+		{targets.MaxRollupWindow, targets.Resolution1h},
+	} {
+		s, err := a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-c.janela), fim)
+		if err != nil || s.Resolution != c.quer {
+			t.Errorf("janela %v deu %q/%v, queria %q", c.janela, s.Resolution, err, c.quer)
+		}
+	}
+	_, err = a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxRollupWindow-time.Second), fim)
 	var verr *targets.ValidationError
-	if !errors.As(err, &verr) {
-		t.Errorf("janela de 48h+1s deu %v, queria ValidationError", err)
+	if !errors.As(err, &verr) || verr.Fields[0] != (targets.FieldError{Field: "from", Reason: targets.ReasonOutOfRange}) {
+		t.Errorf("janela acima do teto deu %v, queria from/out_of_range", err)
+	}
+}
+
+// D6: a série horária vem de check_rollup_1h, só do alvo pedido, e inclui a
+// hora que contém from (arredondado para baixo) e exclui a que começa em to.
+func TestChecksReadsHourlyRollupsAboveTheRawWindow(t *testing.T) {
+	a := novoAmbiente(t)
+	ctx := context.Background()
+	dono := a.usuario(t)
+	alvo, err := a.svc.Create(ctx, dono, entrada())
+	if err != nil {
+		t.Fatal(err)
+	}
+	outro, err := a.svc.Create(ctx, dono, entrada())
+	if err != nil {
+		t.Fatal(err)
+	}
+	fim := a.clk.Now().Truncate(time.Hour) // 12:00 UTC
+	inicio := fim.Add(-72 * time.Hour)
+	p50 := int32(80)
+	for _, r := range []struct {
+		alvo   uuid.UUID
+		bucket time.Time
+	}{
+		{alvo.ID, inicio.Add(-time.Hour)}, // antes da janela
+		{alvo.ID, inicio},                 // contém from (from = inicio+30min)
+		{alvo.ID, fim.Add(-time.Hour)},    // última hora dentro
+		{alvo.ID, fim},                    // começa em to: fora
+		{outro.ID, inicio},                // outro alvo: fora
+	} {
+		if _, err := a.admin.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
+			VALUES ($1, $2, 60, 59, $3, 120, 200, 300)`, r.alvo, r.bucket, p50); err != nil {
+			t.Fatal(err)
+		}
 	}
-	if _, err := a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow), fim); err != nil {
-		t.Errorf("janela de 48h exatas deu %v", err)
+
+	s, err := a.svc.Checks(ctx, dono, alvo.ID, inicio.Add(30*time.Minute), fim)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if s.Resolution != targets.Resolution1h || len(s.Checks) != 0 || len(s.Rollups) != 2 {
+		t.Fatalf("série = %q, %d checks, %d rollups; queria 1h com 2 rollups", s.Resolution, len(s.Checks), len(s.Rollups))
+	}
+	primeiro, ultimo := s.Rollups[0], s.Rollups[1]
+	if !primeiro.Bucket.Equal(inicio) || !ultimo.Bucket.Equal(fim.Add(-time.Hour)) || primeiro.Bucket.Location() != time.UTC {
+		t.Errorf("buckets = %v, %v; queria %v, %v", primeiro.Bucket, ultimo.Bucket, inicio, fim.Add(-time.Hour))
+	}
+	if primeiro.Total != 60 || primeiro.OK != 59 || primeiro.P50 == nil || *primeiro.P50 != 80 || *primeiro.Max != 300 {
+		t.Errorf("rollup = %+v", primeiro)
+	}
+
+	// Outro usuário não enxerga a série, nem vazia: é 404.
+	intruso := a.usuario(t)
+	if _, err := a.svc.Checks(ctx, intruso, alvo.ID, inicio, fim); !errors.Is(err, targets.ErrNotFound) {
+		t.Errorf("série horária do intruso = %v, queria ErrNotFound", err)
 	}
 }
```

Aplique em `internal/httpapi/targets_integration_test.go`:

```diff
--- a/internal/httpapi/targets_integration_test.go
+++ b/internal/httpapi/targets_integration_test.go
@@ -467,42 +467,63 @@
 	}
 
 	var got struct {
-		Items []struct {
+		Resolution string `json:"resolution"`
+		Items      []struct {
 			Status    string  `json:"status"`
 			ErrorKind *string `json:"error_kind"`
 		} `json:"items"`
 	}
 	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID+"/checks", tok, nil), &got)
-	if len(got.Items) != 2 || got.Items[0].Status != "up" || got.Items[1].ErrorKind == nil || *got.Items[1].ErrorKind != "timeout" {
-		t.Errorf("checks = %+v", got.Items)
+	if got.Resolution != "raw" || len(got.Items) != 2 || got.Items[0].Status != "up" || got.Items[1].ErrorKind == nil || *got.Items[1].ErrorKind != "timeout" {
+		t.Errorf("checks = %q %+v", got.Resolution, got.Items)
 	}
 
-	fim := a.clk.Now().UTC()
+	// D6: acima de 48h a série é horária, lida de check_rollup_1h.
+	fim := a.clk.Now().UTC().Truncate(time.Hour)
+	velho := db.InsertCheckParams{
+		ID: uuid.Must(uuid.NewV7()), TargetID: alvoID, CheckedAt: fim.Add(-30*time.Hour + time.Minute),
+		Status: "down", LatencyMS: 10000, ErrorKind: &kind,
+	}
+	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, velho) }); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := storage.ConsolidateRollups(ctx, a.pool, a.clk.Now()); err != nil {
+		t.Fatal(err)
+	}
+	var horaria struct {
+		Resolution string `json:"resolution"`
+		Items      []struct {
+			Bucket time.Time `json:"bucket"`
+			Total  int32     `json:"total"`
+			OK     *int32    `json:"ok"`
+			P50    *int32    `json:"p50_ms"`
+		} `json:"items"`
+	}
 	caminho := fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
 		fim.Add(-49*time.Hour).Format(time.RFC3339), fim.Format(time.RFC3339))
+	cru := lerCorpo(t, chamar(t, a, http.MethodGet, caminho, tok, nil), &horaria)
+	// Duas horas: a do check velho e a dos dois checks recentes (11:00 UTC).
+	if horaria.Resolution != "1h" || len(horaria.Items) != 2 || !horaria.Items[0].Bucket.Equal(fim.Add(-30*time.Hour)) ||
+		horaria.Items[0].Bucket.Location() != time.UTC ||
+		horaria.Items[0].Total != 1 || horaria.Items[0].OK == nil || *horaria.Items[0].OK != 0 || horaria.Items[0].P50 != nil {
+		t.Errorf("janela de 49h = %s, queria 1h com a hora sem sucesso (ok 0, p50_ms null)", cru)
+	}
+
+	caminho = fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
+		fim.Add(-401*24*time.Hour).Format(time.RFC3339), fim.Format(time.RFC3339))
 	resp = chamar(t, a, http.MethodGet, caminho, tok, nil)
 	var env struct {
 		Error struct {
-			Code   string `json:"code"`
 			Fields []struct {
 				Field  string `json:"field"`
 				Reason string `json:"reason"`
 			} `json:"fields"`
 		} `json:"error"`
 	}
-	cru := lerCorpo(t, resp, &env)
-	if resp.StatusCode != http.StatusBadRequest {
-		t.Errorf("janela de 49h deu %d, queria 400 (D6)", resp.StatusCode)
-	}
-	// O 400 tem de apontar o culpado: é "from" que está longe demais de "to".
-	culpado := false
-	for _, f := range env.Error.Fields {
-		if f.Field == "from" && f.Reason == "out_of_range" {
-			culpado = true
-		}
-	}
-	if !culpado {
-		t.Errorf("janela de 49h não apontou from/out_of_range: %s", cru)
+	cru = lerCorpo(t, resp, &env)
+	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
+		env.Error.Fields[0].Field != "from" || env.Error.Fields[0].Reason != "out_of_range" {
+		t.Errorf("janela de 401 dias deu %d %s, queria 400 from/out_of_range", resp.StatusCode, cru)
 	}
 }
 
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/targets/ ./internal/httpapi/`
Expected: `internal/targets` FAIL de compilação (`targets.Resolution` indefinido); `internal/httpapi` FAIL em `TestChecksEndpoint`: `resolution` vazio e a janela de 49h ainda dá 400.

- [ ] **Step 3: Implementar**

Aplique em `internal/targets/target.go`:

```diff
--- a/internal/targets/target.go
+++ b/internal/targets/target.go
@@ -66,6 +66,31 @@
 	ErrorKind  *string
 }
 
+// Rollup é uma hora cheia (UTC) de checks de um alvo. OK conta os que não
+// foram down; as latências são só deles, e ficam nil numa hora sem sucesso.
+type Rollup struct {
+	Bucket             time.Time
+	Total, OK          int32
+	P50, P95, P99, Max *int32
+}
+
+// Resolution diz de onde veio a série. Quem escolhe é o servidor, pela
+// largura da janela (spec §9.1): o cliente pede tempo, não tabela.
+type Resolution string
+
+const (
+	ResolutionRaw Resolution = "raw"
+	Resolution1h  Resolution = "1h"
+)
+
+// Series é a resposta de Service.Checks: Checks quando a resolução é raw,
+// Rollups quando é 1h. A outra fica vazia.
+type Series struct {
+	Resolution Resolution
+	Checks     []Check
+	Rollups    []Rollup
+}
+
 const (
 	DefaultMethod                    = "GET"
 	DefaultIntervalSec         int32 = 60
@@ -77,6 +102,10 @@
 	MaxPageSize     int32 = 100
 
 	// MaxChecksWindow é a janela que a tabela bruta atende (spec §9.1).
-	// Acima disso, rollups, que chegam no Plano 4 (D6).
+	// Acima disso, a série vem de check_rollup_1h (D6 do Plano 2).
 	MaxChecksWindow = 48 * time.Hour
+	// MaxRollupWindow cobre a retenção dos rollups (13 meses, spec §12):
+	// guardar o que não se pode consultar seria só custo. São até 9.600
+	// horas por resposta.
+	MaxRollupWindow = 400 * 24 * time.Hour
 )
```

Aplique em `internal/targets/service.go`:

```diff
--- a/internal/targets/service.go
+++ b/internal/targets/service.go
@@ -214,28 +214,51 @@
 	})
 }
 
-// Checks devolve os resultados de um alvo no intervalo [from, to). Busca o
-// alvo antes, e não só os checks, porque uma lista vazia não distinguiria
-// "alvo de outro usuário" de "alvo sem sondas": o primeiro precisa ser 404.
-func (s *Service) Checks(ctx context.Context, userID, id uuid.UUID, from, to time.Time) ([]Check, error) {
+// Checks devolve a série de um alvo no intervalo [from, to). Até
+// MaxChecksWindow, os checks brutos; acima, as horas de check_rollup_1h, com
+// from arredondado para baixo até a hora cheia, para que a hora que o contém
+// entre. Os buckets saem em UTC: são horas cheias em UTC por definição, e o
+// pgx devolveria o fuso local do processo. Busca o alvo antes, e não só a série, porque uma lista vazia não
+// distinguiria "alvo de outro usuário" de "alvo sem sondas": o primeiro
+// precisa ser 404.
+func (s *Service) Checks(ctx context.Context, userID, id uuid.UUID, from, to time.Time) (Series, error) {
 	if !from.Before(to) {
-		return nil, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonInvalid}}}
+		return Series{}, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonInvalid}}}
 	}
+	if to.Sub(from) > MaxRollupWindow {
+		return Series{}, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonOutOfRange}}}
+	}
+	out := Series{Resolution: ResolutionRaw}
 	if to.Sub(from) > MaxChecksWindow {
-		return nil, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonOutOfRange}}}
+		out.Resolution = Resolution1h
 	}
-	var out []Check
 	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
 		if _, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id}); err != nil {
 			return naoAchado(err)
 		}
+		if out.Resolution == Resolution1h {
+			rows, err := q.ListRollups(ctx, db.ListRollupsParams{
+				UserID: userID, TargetID: id, FromAt: from.Truncate(time.Hour), ToAt: to,
+			})
+			if err != nil {
+				return fmt.Errorf("listar rollups: %w", err)
+			}
+			out.Rollups = make([]Rollup, 0, len(rows))
+			for _, r := range rows {
+				out.Rollups = append(out.Rollups, Rollup{
+					Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok,
+					P50: r.P50, P95: r.P95, P99: r.P99, Max: r.Max,
+				})
+			}
+			return nil
+		}
 		rows, err := q.ListChecks(ctx, db.ListChecksParams{UserID: userID, TargetID: id, FromAt: from, ToAt: to})
 		if err != nil {
 			return fmt.Errorf("listar checks: %w", err)
 		}
-		out = make([]Check, 0, len(rows))
+		out.Checks = make([]Check, 0, len(rows))
 		for _, r := range rows {
-			out = append(out, Check{
+			out.Checks = append(out.Checks, Check{
 				CheckedAt: r.CheckedAt, Status: r.Status, LatencyMS: r.LatencyMS,
 				StatusCode: r.StatusCode, ErrorKind: r.ErrorKind,
 			})
```

Aplique em `internal/httpapi/targethandlers.go`:

```diff
--- a/internal/httpapi/targethandlers.go
+++ b/internal/httpapi/targethandlers.go
@@ -79,6 +79,17 @@
 	ErrorKind  *string   `json:"error_kind"`
 }
 
+// rollupResponse é uma hora da série quando resolution é "1h" (D6).
+type rollupResponse struct {
+	Bucket time.Time `json:"bucket"`
+	Total  int32     `json:"total"`
+	OK     int32     `json:"ok"`
+	P50    *int32    `json:"p50_ms"`
+	P95    *int32    `json:"p95_ms"`
+	P99    *int32    `json:"p99_ms"`
+	Max    *int32    `json:"max_ms"`
+}
+
 func toTargetResponse(t targets.Target) targetResponse {
 	return targetResponse{
 		ID: t.ID.String(), Name: t.Name, URL: t.URL, Method: t.Method,
@@ -267,18 +278,30 @@
 		from = v
 	}
 
-	checks, err := h.svc.Checks(r.Context(), userID, id, from, to)
+	serie, err := h.svc.Checks(r.Context(), userID, id, from, to)
 	if h.falhou(w, r, "listar checks", err) {
 		return
 	}
-	items := make([]checkResponse, 0, len(checks))
-	for _, c := range checks {
+	// resolution discrimina o formato dos itens: o cliente não adivinha pela
+	// forma do JSON.
+	if serie.Resolution == targets.Resolution1h {
+		items := make([]rollupResponse, 0, len(serie.Rollups))
+		for _, b := range serie.Rollups {
+			items = append(items, rollupResponse{
+				Bucket: b.Bucket, Total: b.Total, OK: b.OK, P50: b.P50, P95: b.P95, P99: b.P99, Max: b.Max,
+			})
+		}
+		apierr.WriteJSON(w, http.StatusOK, map[string]any{"resolution": serie.Resolution, "items": items})
+		return
+	}
+	items := make([]checkResponse, 0, len(serie.Checks))
+	for _, c := range serie.Checks {
 		items = append(items, checkResponse{
 			CheckedAt: c.CheckedAt, Status: c.Status, LatencyMS: c.LatencyMS,
 			StatusCode: c.StatusCode, ErrorKind: c.ErrorKind,
 		})
 	}
-	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
+	apierr.WriteJSON(w, http.StatusOK, map[string]any{"resolution": serie.Resolution, "items": items})
 }
 
 // falhou traduz o erro do domínio para o envelope e devolve true se havia
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/targets/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run TestChecks ./internal/targets/ ./internal/httpapi/`; restaure:
1. Troque `FromAt: from.Truncate(time.Hour)` por `FromAt: from`. Expected: FAIL (a hora que contém `from` some).
2. Troque `Bucket: r.Bucket.UTC()` por `Bucket: r.Bucket`. Expected: FAIL nos dois pacotes (fuso).
3. Troque `> MaxChecksWindow` por `>= MaxChecksWindow`, e depois `> MaxRollupWindow` por `>= MaxRollupWindow`. Expected: FAIL nas bordas de `TestChecksWindowPicksTheResolution`.
4. No SQL gerado de `ListRollups` (só para a sabotagem; restaure com `sqlc generate`), troque `AND r.bucket < $4` por `AND $4::timestamptz IS NOT NULL`. Expected: FAIL (a hora que começa em `to` entra).

- [ ] **Step 6: Commit**

```bash
git add internal/targets/ internal/httpapi/
git commit -m "feat(targets): janelas acima de 48h leem os rollups horários (D6 do Plano 2)"
```

---

### Task 4: Canal `failing`

Pendência do Plano 3: o canal que quebra depois de verificado (webhook apagado → 404; domínio do Resend desverificado → 403) só ia para `audit_log`, e `GET /channels` seguia `verified`.

**Files:**
- Modify: `internal/storage/queries/channels.sql` (`ListChannels` com `LEFT JOIN channel_health`)
- Modify: `internal/notify/service.go` (`StatusFailing`, `LastFailureAt`, `LastError`, `toChannel`)
- Modify: `internal/notify/dispatcher.go` (`persistir` grava a saúde)
- Modify: `internal/httpapi/channelhandlers.go` (`last_error`, `last_failure_at`)
- Modify: `internal/storage/notify_rls_test.go` (deixa de usar `ListChannels`)
- Generated: `internal/storage/db/*.go`
- Test: `internal/notify/dispatcher_test.go`, `internal/httpapi/channels_integration_test.go`

**Interfaces:**
- Consumes: `MarkChannelSuccess`, `MarkChannelFailure` (Task 1).
- Produces: `notify.StatusFailing = "failing"`; `Channel.LastFailureAt time.Time`, `Channel.LastError Reason`; JSON `last_error` e `last_failure_at`, `null` quando nunca houve falha definitiva.

**Contexto para quem implementa.** `ListChannels` passa a devolver `db.ListChannelsRow` e não mais `db.NotifChannel`. Isso quebra `toChannel` e o teste `TestConfirmChannelIsIdempotentAndRespectsExpiry` (que lia a lista); as três mudanças vão juntas nesta tarefa. `failing` é derivado (D10): falha definitiva **depois** do último sucesso. Cancelamento não conta (não houve tentativa); retentativa também não (ainda pode dar certo).

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/notify/dispatcher_test.go` (o helper `canalVisto` e dois testes no fim do arquivo):

```diff
--- a/internal/notify/dispatcher_test.go
+++ b/internal/notify/dispatcher_test.go
@@ -492,3 +492,68 @@
 		t.Errorf("linha = %+v", l)
 	}
 }
+
+// canalVisto é o canal como o dono o vê em GET /channels.
+func (b *bancada) canalVisto(t *testing.T) Channel {
+	t.Helper()
+	svc := NewChannelService(b.pool, b.box, b.clk, nil, nil, "https://vigilume.dev")
+	cs, err := svc.List(context.Background(), b.dono)
+	if err != nil || len(cs) != 1 {
+		t.Fatalf("List = %v, %v", cs, err)
+	}
+	return cs[0]
+}
+
+// Pendência do Plano 3: o canal que quebra depois de verificado não falha
+// mais em silêncio. A falha definitiva o põe em failing, com o motivo; a
+// próxima entrega que dá certo o tira, sem ação do dono.
+func TestDefinitiveFailureMarksTheChannelFailingUntilASuccess(t *testing.T) {
+	b := novaBancada(t, nil)
+	if c := b.canalVisto(t); c.Status != StatusVerified || !c.LastFailureAt.IsZero() {
+		t.Fatalf("antes de qualquer envio: %+v", c)
+	}
+
+	b.canal.respostas = []error{&SendError{Reason: ReasonRejected, Permanent: true}}
+	b.enfileirar(t, incident.EventOpened)
+	b.despachar(t, 1)
+	falhou := b.clk.Now()
+	c := b.canalVisto(t)
+	if c.Status != StatusFailing || c.LastError != ReasonRejected || !c.LastFailureAt.Equal(falhou) {
+		t.Fatalf("depois da falha definitiva: %+v; queria failing, rejected, %v", c, falhou)
+	}
+
+	b.clk.Advance(time.Minute)
+	b.enfileirar(t, incident.EventResolved)
+	b.despachar(t, 1)
+	c = b.canalVisto(t)
+	if c.Status != StatusVerified || c.LastError != ReasonRejected || !c.LastFailureAt.Equal(falhou) {
+		t.Errorf("depois de uma entrega: %+v; queria verified, com a última falha ainda visível", c)
+	}
+}
+
+// Retentativa e cancelamento não mexem na saúde do canal: a primeira ainda
+// pode dar certo, e o segundo nem tentou enviar.
+func TestRetryAndCancelLeaveTheChannelHealthAlone(t *testing.T) {
+	b := novaBancada(t, nil)
+	b.canal.respostas = []error{&SendError{Reason: ReasonServerError}}
+	b.enfileirar(t, incident.EventOpened)
+	b.despachar(t, 1)
+	if c := b.canalVisto(t); c.Status != StatusVerified || !c.LastFailureAt.IsZero() {
+		t.Errorf("retentativa mexeu no canal: %+v", c)
+	}
+	// Conta sob o escopo prober: sem escopo, a RLS esconderia qualquer
+	// linha e o 0 seria verdade por acidente.
+	ctx := context.Background()
+	tx, err := b.pool.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer tx.Rollback(ctx)
+	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
+		t.Fatal(err)
+	}
+	var n int
+	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_health`).Scan(&n); err != nil || n != 0 {
+		t.Errorf("channel_health tem %d linhas depois de uma retentativa (%v), queria 0", n, err)
+	}
+}
```

Aplique em `internal/httpapi/channels_integration_test.go`:

```diff
--- a/internal/httpapi/channels_integration_test.go
+++ b/internal/httpapi/channels_integration_test.go
@@ -105,6 +105,40 @@
 	}
 }
 
+// Pendência do Plano 3: o canal que falhou de vez aparece como failing, com
+// o motivo e a hora; antes da falha, os dois campos são null.
+func TestFailingChannelShowsTheLastError(t *testing.T) {
+	a := newAPI(t)
+	ctx := context.Background()
+	tok := tokenDe(t, a, a.userID)
+	var criado canalJSON
+	cru := lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste}), &criado)
+	if !strings.Contains(string(cru), `"last_error":null`) || !strings.Contains(string(cru), `"last_failure_at":null`) {
+		t.Errorf("canal novo = %s; queria last_error e last_failure_at null", cru)
+	}
+
+	em := a.clk.Now().UTC()
+	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
+		motivo := "rejected"
+		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: uuid.MustParse(criado.ID), Now: em, LastError: &motivo})
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	var l struct {
+		Items []struct {
+			Status        string     `json:"status"`
+			LastError     *string    `json:"last_error"`
+			LastFailureAt *time.Time `json:"last_failure_at"`
+		} `json:"items"`
+	}
+	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/channels", tok, nil), &l)
+	if len(l.Items) != 1 || l.Items[0].Status != "failing" || l.Items[0].LastError == nil || *l.Items[0].LastError != "rejected" ||
+		l.Items[0].LastFailureAt == nil || !l.Items[0].LastFailureAt.Equal(em) {
+		t.Errorf("GET /channels = %+v; queria failing, rejected, %v", l.Items, em)
+	}
+}
+
 // O caminho do navegador: o GET mostra o botão e NÃO verifica; o POST do
 // formulário, sem X-Requested-With, verifica.
 func TestEmailVerificationThroughTheBrowser(t *testing.T) {
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/notify/ ./internal/httpapi/`
Expected: `internal/notify` FAIL de compilação (`StatusFailing`, `c.LastFailureAt` indefinidos); `internal/httpapi` FAIL em `TestFailingChannelShowsTheLastError` (o canal segue `verified`, sem `last_error`).

**Atenção:** o segundo teste do dispatcher conta `channel_health` sob o escopo prober. Sem escopo, a RLS esconderia qualquer linha, e o "0 linhas" seria verdade por acidente (D25).

- [ ] **Step 3: Implementar**

Aplique em `internal/storage/queries/channels.sql` e rode `sqlc generate`:

```diff
--- a/internal/storage/queries/channels.sql
+++ b/internal/storage/queries/channels.sql
@@ -17,7 +17,12 @@
 
 -- Sem paginação: o limite por usuário é pequeno (notify.MaxChannels).
 -- name: ListChannels :many
-SELECT * FROM notif_channels WHERE user_id = $1 ORDER BY id DESC;
+SELECT c.id, c.kind, c.config_enc, c.verified_at, c.verify_expires_at, c.enabled, c.created_at,
+       h.last_success_at, h.last_failure_at, h.last_error
+  FROM notif_channels c
+  LEFT JOIN channel_health h ON h.channel_id = c.id
+ WHERE c.user_id = $1
+ ORDER BY c.id DESC;
 
 -- name: GetChannel :one
 SELECT * FROM notif_channels WHERE user_id = $1 AND id = $2;
```

Aplique em `internal/notify/service.go`:

```diff
--- a/internal/notify/service.go
+++ b/internal/notify/service.go
@@ -73,6 +73,10 @@
 	Status    ChannelStatus
 	Enabled   bool
 	CreatedAt time.Time
+	// LastFailureAt e LastError vêm da última falha definitiva de envio
+	// (channel_health). Zero e "" quando nunca falhou de vez.
+	LastFailureAt time.Time
+	LastError     Reason
 }
 
 type ChannelStatus string
@@ -81,6 +85,11 @@
 	StatusVerified ChannelStatus = "verified"
 	StatusPending  ChannelStatus = "pending"
 	StatusExpired  ChannelStatus = "expired"
+	// StatusFailing é o canal verificado cuja última entrega falhou de vez,
+	// sem sucesso depois dela: o webhook foi apagado, o domínio do e-mail
+	// perdeu a verificação. O canal continua recebendo tentativas; o status
+	// existe para que o dono saiba (pendência do Plano 3).
+	StatusFailing ChannelStatus = "failing"
 )
 
 type ChannelInput struct {
@@ -246,14 +255,22 @@
 	return out, err
 }
 
-func (s *ChannelService) toChannel(r db.NotifChannel) Channel {
+func (s *ChannelService) toChannel(r db.ListChannelsRow) Channel {
 	c := Channel{ID: r.ID, Kind: Kind(r.Kind), Enabled: r.Enabled, CreatedAt: r.CreatedAt}
 	if dest, err := openConfig(s.box, r.ID, r.ConfigEnc); err == nil {
 		c.Hint = hint(c.Kind, dest)
 	} else {
 		slog.Error("canal com destino ilegível", "channel_id", r.ID)
 	}
+	if r.LastFailureAt != nil {
+		c.LastFailureAt = *r.LastFailureAt
+		if r.LastError != nil {
+			c.LastError = Reason(*r.LastError)
+		}
+	}
 	switch {
+	case r.VerifiedAt != nil && falhandoDepoisDoSucesso(r.LastFailureAt, r.LastSuccessAt):
+		c.Status = StatusFailing
 	case r.VerifiedAt != nil:
 		c.Status = StatusVerified
 	case r.VerifyExpiresAt != nil && !s.clk.Now().Before(*r.VerifyExpiresAt):
@@ -264,6 +281,13 @@
 	return c
 }
 
+// falhandoDepoisDoSucesso: houve falha definitiva, e nenhuma entrega deu
+// certo depois dela. Uma entrega que volta a dar certo tira o canal de
+// "failing" sem ação do dono.
+func falhandoDepoisDoSucesso(falha, sucesso *time.Time) bool {
+	return falha != nil && (sucesso == nil || falha.After(*sucesso))
+}
+
 func (s *ChannelService) Delete(ctx context.Context, userID, id uuid.UUID) error {
 	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
 		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: userID, ID: id})
```

Aplique em `internal/notify/dispatcher.go`:

```diff
--- a/internal/notify/dispatcher.go
+++ b/internal/notify/dispatcher.go
@@ -215,7 +215,10 @@
 	return storage.InProberTx(ctx, d.pool, func(q *db.Queries) error {
 		switch f.status {
 		case "sent":
-			return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: n.ID, Now: d.clk.Now()})
+			if err := q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: n.ID, Now: d.clk.Now()}); err != nil {
+				return err
+			}
+			return q.MarkChannelSuccess(ctx, db.MarkChannelSuccessParams{ChannelID: n.ChannelID, Now: d.clk.Now()})
 		case "pending":
 			return q.RetryNotification(ctx, db.RetryNotificationParams{ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo})
 		case "cancelled":
@@ -224,6 +227,11 @@
 		if err := q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "failed", LastError: motivo}); err != nil {
 			return err
 		}
+		// O canal passa a "failing" até a próxima entrega que der certo
+		// (pendência do Plano 3: antes, a falha só ia para a auditoria).
+		if err := q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: n.ChannelID, Now: d.clk.Now(), LastError: motivo}); err != nil {
+			return err
+		}
 		// Falha definitiva vai para a trilha de auditoria (spec §8).
 		id, err := uuid.NewV7()
 		if err != nil {
```

Aplique em `internal/httpapi/channelhandlers.go`:

```diff
--- a/internal/httpapi/channelhandlers.go
+++ b/internal/httpapi/channelhandlers.go
@@ -34,13 +34,23 @@
 	Status    string    `json:"status"`
 	Enabled   bool      `json:"enabled"`
 	CreatedAt time.Time `json:"created_at"`
+	// A última falha definitiva de envio, null se nunca houve. Continua
+	// aqui depois que o canal volta a entregar: o status diz o presente, e
+	// estes dois, o que aconteceu.
+	LastError     *string    `json:"last_error"`
+	LastFailureAt *time.Time `json:"last_failure_at"`
 }
 
 func toChannelResponse(c notify.Channel) channelResponse {
-	return channelResponse{
+	r := channelResponse{
 		ID: c.ID.String(), Kind: string(c.Kind), Hint: c.Hint, Status: string(c.Status),
 		Enabled: c.Enabled, CreatedAt: c.CreatedAt,
 	}
+	if !c.LastFailureAt.IsZero() {
+		motivo, em := string(c.LastError), c.LastFailureAt
+		r.LastError, r.LastFailureAt = &motivo, &em
+	}
+	return r
 }
 
 func (h *ChannelHandlers) List(w http.ResponseWriter, r *http.Request) {
```

Aplique em `internal/storage/notify_rls_test.go`:

```diff
--- a/internal/storage/notify_rls_test.go
+++ b/internal/storage/notify_rls_test.go
@@ -188,7 +188,7 @@
 	ctx := context.Background()
 	dono := novoUsuario(t, pool)
 	h, hVencido := hashDe("token"), hashDe("vencido")
-	novoCanal(t, pool, dono, h, agora.Add(24*time.Hour))
+	canal := novoCanal(t, pool, dono, h, agora.Add(24*time.Hour))
 	novoCanal(t, pool, dono, hVencido, agora.Add(-time.Minute))
 
 	confirmar := func(hash []byte, em time.Time) int64 {
@@ -217,11 +217,9 @@
 
 	var verificadoEm time.Time
 	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
-		cs, err := q.ListChannels(ctx, dono)
-		for _, c := range cs {
-			if string(c.VerifyTokenHash) == string(h) && c.VerifiedAt != nil {
-				verificadoEm = *c.VerifiedAt
-			}
+		c, err := q.GetChannel(ctx, db.GetChannelParams{UserID: dono, ID: canal})
+		if err == nil && c.VerifiedAt != nil {
+			verificadoEm = *c.VerifiedAt
 		}
 		return err
 	})
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && sqlc diff && go test -race -count=1 ./internal/notify/ ./internal/storage/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure depois de cada:
1. Em `persistir`, troque o `return q.MarkChannelSuccess(...)` por `return nil`. Rode `-run 'TestDefinitiveFailure' ./internal/notify/`. Expected: FAIL (o canal não volta a `verified`).
2. Troque a condição do `if err := q.MarkChannelFailure(...)` por `if err := error(nil); err != nil {`. Expected: FAIL (o canal não vira `failing`).
3. Grave a falha também no `case "pending"`. Rode `-run TestRetryAndCancel`. Expected: FAIL (1 linha em `channel_health`).
4. Em `falhandoDepoisDoSucesso`, devolva só `falha != nil`. Expected: FAIL em `TestDefinitiveFailure` (o sucesso não limpa).
5. Em `toChannelResponse`, não preencha `LastError`/`LastFailureAt`. Rode `-run TestFailingChannel ./internal/httpapi/`. Expected: FAIL.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/notify/ internal/httpapi/
git commit -m "feat(notify): canal que falha de vez depois de verificado aparece como failing"
```

---

### Task 5: Apagão em massa

Pendência do Plano 3: muitos alvos de um dono caindo juntos disparam dezenas de envios ao mesmo webhook, e os 429 do Discord esgotavam as 6 tentativas.

**Files:**
- Modify: `internal/notify/dispatcher.go` (`rateLimitGrace`, `porCanal`, `entregarEmSerie`, `entregarProtegido`, `perdoa429`, `adiar`, `desfecho.devolver`)
- Test: `internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `ClaimDueNotificationsRow.CreatedAt` e `RetryNotificationParams.Refund` (Task 1).
- Produces: `rateLimitGrace = time.Hour`; `notify_total{result="deferred"}`; `entregar` passa a devolver o `desfecho`.

**Contexto para quem implementa.** Três mudanças (D11, D12):
1. **429 devolve a tentativa** (`Refund: true`) enquanto a notificação tem menos de `rateLimitGrace`. A espera continua sendo `max(backoff, Retry-After)`.
2. **O lote é separado por canal**; cada canal é uma goroutine que entrega em série, na ordem do id (v7 = ordem de enfileiramento).
3. **Depois de um 429**, o resto da fila daquele canal é adiado para o mesmo instante, sem envio e com `Refund`. O adiamento também respeita `rateLimitGrace`: uma notificação velha é enviada na vez dela e gasta a tentativa, o que mantém tudo limitado no tempo.

O `recover` sai de `Dispatch` e vai para `entregarProtegido`: um panic num envio não pode derrubar o resto da fila do canal.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/notify/dispatcher_test.go`:

```diff
--- a/internal/notify/dispatcher_test.go
+++ b/internal/notify/dispatcher_test.go
@@ -4,6 +4,7 @@
 	"bytes"
 	"context"
 	"errors"
+	"fmt"
 	"log/slog"
 	"strings"
 	"sync"
@@ -557,3 +558,156 @@
 		t.Errorf("channel_health tem %d linhas depois de uma retentativa (%v), queria 0", n, err)
 	}
 }
+
+// apagao simula muitos alvos do mesmo dono caindo juntos: n alvos, cada um
+// com um incidente aberto e a abertura enfileirada para o mesmo canal.
+// Devolve os ids na ordem de enfileiramento.
+func (b *bancada) apagao(t *testing.T, n int) []uuid.UUID {
+	t.Helper()
+	ctx := context.Background()
+	ids := make([]uuid.UUID, 0, n)
+	for i := range n {
+		alvo, incidente, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
+		agora := b.clk.Now()
+		err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
+			return q.InsertTarget(ctx, db.InsertTargetParams{
+				ID: alvo, UserID: b.dono, Name: fmt.Sprintf("alvo-%d", i), URL: "https://exemplo.com", Method: "GET",
+				IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
+			})
+		})
+		if err != nil {
+			t.Fatal(err)
+		}
+		err = storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
+			if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: incidente, TargetID: alvo, StartedAt: agora, Cause: "timeout"}); err != nil {
+				return err
+			}
+			return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
+				ID: id, IncidentID: incidente, ChannelID: b.canalID, Event: incident.EventOpened, Now: agora,
+			})
+		})
+		if err != nil {
+			t.Fatal(err)
+		}
+		ids = append(ids, id)
+	}
+	return ids
+}
+
+// Apagão em massa (pendência do Plano 3): um 429 é o provedor pedindo
+// espera, e não gasta tentativa. Dez 429 seguidos, mais que MaxAttempts,
+// e a notificação ainda sai.
+func TestRateLimitDoesNotSpendAttempts(t *testing.T) {
+	b := novaBancada(t, nil)
+	id := b.enfileirar(t, incident.EventOpened)
+	for i := range 10 {
+		b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited, RetryAfter: 2 * time.Second}}
+		b.despachar(t, 1)
+		l := b.estado(t, id)
+		if l.Status != "pending" || l.Attempts != 0 || !l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) {
+			t.Fatalf("depois do 429 nº %d: %+v; queria pendente, 0 tentativas, próxima pelo backoff", i+1, l)
+		}
+		b.clk.Advance(30 * time.Second)
+	}
+	b.despachar(t, 1)
+	if l := b.estado(t, id); l.Status != "sent" || l.Attempts != 1 {
+		t.Errorf("linha = %+v; queria sent na 11ª tentativa, com 1 tentativa contada", l)
+	}
+}
+
+// O perdão tem teto: uma notificação com mais de rateLimitGrace de idade já
+// perdeu a maior parte do valor, e um webhook que responde 429 para sempre
+// não pode segurá-la na fila para sempre.
+func TestRateLimitCountsOnceTheNotificationIsOld(t *testing.T) {
+	b := novaBancada(t, nil)
+	id := b.enfileirar(t, incident.EventOpened)
+	b.clk.Advance(rateLimitGrace)
+	b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited}}
+	b.despachar(t, 1)
+	if l := b.estado(t, id); l.Status != "pending" || l.Attempts != 1 {
+		t.Errorf("429 depois do teto: %+v; queria a tentativa contada", l)
+	}
+}
+
+// Dentro do lote, o mesmo canal recebe em série, na ordem em que as
+// notificações foram enfileiradas. Um 429 adia o resto do canal no lote,
+// sem enviar, para quando o provedor pediu; nada gasta tentativa.
+func TestSameChannelSendsInOrderAndHoldsAfterA429(t *testing.T) {
+	b := novaBancada(t, nil)
+	ids := b.apagao(t, 5)
+	b.canal.respostas = []error{nil, nil, &SendError{Reason: ReasonRateLimited, RetryAfter: 3 * time.Minute}}
+
+	b.despachar(t, 5)
+	if n := b.canal.quantos(); n != 3 {
+		t.Fatalf("%d envios, queria 3 (dois entregues e o do 429; o resto espera)", n)
+	}
+	for i, ev := range b.canal.envios {
+		if ev.NotificationID != ids[i] {
+			t.Errorf("envio %d foi %s, queria %s (ordem de enfileiramento)", i, ev.NotificationID, ids[i])
+		}
+	}
+	for i, id := range ids {
+		l := b.estado(t, id)
+		switch {
+		case i < 2 && l.Status != "sent":
+			t.Errorf("notificação %d: %+v, queria sent", i, l)
+		case i >= 2 && (l.Status != "pending" || l.Attempts != 0 || !l.Proxima.Equal(b.clk.Now().Add(3*time.Minute)) ||
+			l.Erro == nil || *l.Erro != "rate_limited"):
+			t.Errorf("notificação %d: %+v; queria pendente para daqui a 3min, sem tentativa gasta", i, l)
+		}
+	}
+	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "deferred")); v != 2 {
+		t.Errorf("notify_total{discord,deferred} = %v, queria 2", v)
+	}
+
+	b.clk.Advance(3 * time.Minute)
+	b.despachar(t, 3)
+	for i, id := range ids {
+		if l := b.estado(t, id); l.Status != "sent" {
+			t.Errorf("depois da espera, notificação %d: %+v", i, l)
+		}
+	}
+}
+
+// Canais diferentes não esperam um pelo outro: um webhook travado não
+// atrasa o canal vizinho além do prazo de envio.
+func TestDifferentChannelsSendInParallel(t *testing.T) {
+	b := novaBancada(t, nil)
+	// 1s por envio: a folga de 1s entre "em paralelo" (~1s) e "em série"
+	// (>= 2s) cobre o banco numa CI lenta com -race.
+	b.d.sendTimeout = time.Second
+	b.canal.bloquear = true
+	b.apagao(t, 1)
+	// Um segundo canal, verificado, do mesmo dono, com a abertura do
+	// incidente da bancada.
+	ctx := context.Background()
+	outro := uuid.Must(uuid.NewV7())
+	sealed, err := sealConfig(b.box, outro, webhookDeTeste)
+	if err != nil {
+		t.Fatal(err)
+	}
+	agora := b.clk.Now()
+	err = storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
+		return q.InsertChannel(ctx, db.InsertChannelParams{
+			ID: outro, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
+		})
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	err = storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
+		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
+			ID: uuid.Must(uuid.NewV7()), IncidentID: b.incidente, ChannelID: outro, Event: incident.EventOpened, Now: agora,
+		})
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	inicio := time.Now()
+	b.despachar(t, 2)
+	// Em série, dois envios travados levariam pelo menos 2 × 1s.
+	if d := time.Since(inicio); d >= 2*time.Second {
+		t.Errorf("dois canais travados levaram %v; em paralelo, ~1s", d)
+	}
+}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/notify/`
Expected: FAIL de compilação: `undefined: rateLimitGrace`.

- [ ] **Step 3: Implementar**

Aplique em `internal/notify/dispatcher.go`:

```diff
--- a/internal/notify/dispatcher.go
+++ b/internal/notify/dispatcher.go
@@ -1,10 +1,12 @@
 package notify
 
 import (
+	"bytes"
 	"context"
 	"errors"
 	"fmt"
 	"log/slog"
+	"slices"
 	"sync"
 	"time"
 
@@ -39,6 +41,14 @@
 	MaxAttempts  = 6
 	firstBackoff = 30 * time.Second
 	maxBackoff   = time.Hour
+
+	// rateLimitGrace é por quanto tempo, contado do enfileiramento, um 429
+	// não gasta tentativa. Num apagão em massa, dezenas de alertas vão ao
+	// mesmo webhook, e os 429 esgotariam as 6 tentativas justamente quando
+	// o alerta mais importa. Depois disso, o 429 conta: um alerta com uma
+	// hora de atraso já perdeu quase todo o valor, e um webhook que responde
+	// 429 para sempre não segura a fila para sempre.
+	rateLimitGrace = time.Hour
 )
 
 // Dispatcher consome a outbox de notificações (spec §8). Roda no papel do
@@ -106,32 +116,94 @@
 	}
 
 	var wg sync.WaitGroup
-	for _, n := range lote {
+	for _, fila := range porCanal(lote) {
 		wg.Add(1)
 		go func() {
 			defer wg.Done()
-			// Um panic num canal não derruba o processo. A notificação fica
-			// como está e volta quando o lease vencer.
-			defer func() {
-				if r := recover(); r != nil {
-					slog.Error("panic no envio de notificação", "notification_id", n.ID, "panic", fmt.Sprint(r))
-				}
-			}()
-			d.entregar(n)
+			d.entregarEmSerie(fila)
 		}()
 	}
 	wg.Wait()
 	return len(lote), nil
 }
 
+// porCanal separa o lote por canal, cada fila na ordem de enfileiramento
+// (os ids são UUID v7). Canais diferentes saem em paralelo; o mesmo canal,
+// em série: dezenas de pedidos simultâneos ao mesmo webhook só compram 429,
+// e em série a abertura sai antes do fechamento.
+func porCanal(lote []db.ClaimDueNotificationsRow) [][]db.ClaimDueNotificationsRow {
+	indice := map[uuid.UUID]int{}
+	var filas [][]db.ClaimDueNotificationsRow
+	for _, n := range lote {
+		i, ok := indice[n.ChannelID]
+		if !ok {
+			i = len(filas)
+			indice[n.ChannelID] = i
+			filas = append(filas, nil)
+		}
+		filas[i] = append(filas[i], n)
+	}
+	for _, f := range filas {
+		slices.SortFunc(f, func(a, b db.ClaimDueNotificationsRow) int { return bytes.Compare(a.ID[:], b.ID[:]) })
+	}
+	return filas
+}
+
+// entregarEmSerie entrega a fila de um canal. Depois de um 429, o resto da
+// fila não é enviado: fica para quando o provedor pediu, sem gastar
+// tentativa, porque não houve envio.
+func (d *Dispatcher) entregarEmSerie(fila []db.ClaimDueNotificationsRow) {
+	var espera time.Time
+	for _, n := range fila {
+		if !espera.IsZero() && d.perdoa429(n) {
+			d.adiar(n, espera)
+			continue
+		}
+		if f := d.entregarProtegido(n); f.reason == ReasonRateLimited && f.status == "pending" {
+			espera = f.proxima
+		}
+	}
+}
+
+// entregarProtegido isola o panic de um envio: a notificação fica como a
+// reivindicação a deixou e volta quando o lease vencer, e o resto da fila
+// do canal segue.
+func (d *Dispatcher) entregarProtegido(n db.ClaimDueNotificationsRow) (f desfecho) {
+	defer func() {
+		if r := recover(); r != nil {
+			slog.Error("panic no envio de notificação", "notification_id", n.ID, "panic", fmt.Sprint(r))
+			f = desfecho{}
+		}
+	}()
+	return d.entregar(n)
+}
+
+// perdoa429 diz se um 429 para esta notificação ainda não gasta tentativa.
+func (d *Dispatcher) perdoa429(n db.ClaimDueNotificationsRow) bool {
+	return d.clk.Now().Sub(n.CreatedAt) < rateLimitGrace
+}
+
+// adiar devolve à fila, sem enviar, uma notificação cujo canal acabou de
+// pedir espera.
+func (d *Dispatcher) adiar(n db.ClaimDueNotificationsRow, ate time.Time) {
+	d.total.WithLabelValues(n.Kind, "deferred").Inc()
+	f := desfecho{status: "pending", reason: ReasonRateLimited, proxima: ate, devolver: true}
+	if err := d.persistir(n, f); err != nil {
+		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
+	}
+}
+
 // desfecho é o que fazer com a notificação depois da tentativa.
 type desfecho struct {
 	status  string // sent, pending (retentativa), failed ou cancelled
 	reason  Reason
 	proxima time.Time
+	// devolver desconta a tentativa que a reivindicação contou (429 dentro
+	// de rateLimitGrace, ou adiamento sem envio).
+	devolver bool
 }
 
-func (d *Dispatcher) entregar(n db.ClaimDueNotificationsRow) {
+func (d *Dispatcher) entregar(n db.ClaimDueNotificationsRow) desfecho {
 	f := d.tentar(n)
 	d.total.WithLabelValues(n.Kind, resultado(f.status)).Inc()
 	if f.status == "failed" {
@@ -141,6 +213,7 @@
 	if err := d.persistir(n, f); err != nil {
 		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
 	}
+	return f
 }
 
 func (d *Dispatcher) tentar(n db.ClaimDueNotificationsRow) desfecho {
@@ -187,10 +260,13 @@
 			se.Reason = ReasonTimeout
 		}
 	}
+	espera := max(backoff(n.Attempts), se.RetryAfter)
+	if se.Reason == ReasonRateLimited && !se.Permanent && d.perdoa429(n) {
+		return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), devolver: true}
+	}
 	if se.Permanent || n.Attempts >= MaxAttempts {
 		return desfecho{status: "failed", reason: se.Reason}
 	}
-	espera := max(backoff(n.Attempts), se.RetryAfter)
 	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera)}
 }
 
@@ -220,7 +296,9 @@
 			}
 			return q.MarkChannelSuccess(ctx, db.MarkChannelSuccessParams{ChannelID: n.ChannelID, Now: d.clk.Now()})
 		case "pending":
-			return q.RetryNotification(ctx, db.RetryNotificationParams{ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo})
+			return q.RetryNotification(ctx, db.RetryNotificationParams{
+				ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo, Refund: f.devolver,
+			})
 		case "cancelled":
 			return q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "cancelled", LastError: motivo})
 		}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/notify/`
Expected: PASS, inclusive `TestPanickingNotifierDoesNotKillTheDispatcher` e `TestRetryAfterWinsWhenLonger`, do Plano 3.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run 'TestRateLimit|TestSameChannel|TestDifferentChannels' ./internal/notify/`; restaure:
1. `Refund: f.devolver` → `Refund: false`. Expected: FAIL em `TestRateLimitDoesNotSpendAttempts` e `TestSameChannelSendsInOrderAndHoldsAfterA429`.
2. `perdoa429` devolvendo sempre `true`. Expected: FAIL em `TestRateLimitCountsOnceTheNotificationIsOld`.
3. Em `entregarEmSerie`, troque `if !espera.IsZero() && d.perdoa429(n)` por `if false`. Expected: FAIL em `TestSameChannelSendsInOrderAndHoldsAfterA429`.
4. Inverta a ordenação de `porCanal` (`bytes.Compare(b.ID[:], a.ID[:])`). Expected: FAIL na ordem.
5. Em `Dispatch`, chame `d.entregarEmSerie(fila)` direto, sem goroutine. Expected: FAIL em `TestDifferentChannelsSendInParallel`.
6. Em `tentar`, troque a condição do perdão por `if false`. Expected: FAIL em `TestRateLimitDoesNotSpendAttempts`.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): 429 não gasta tentativa por 1h, e o mesmo canal recebe em série"
```

---

### Task 6: Cadastro da status page

O pacote novo `internal/statuspage`, com o domínio e o serviço do dono. A projeção pública chega na Task 8, no mesmo pacote.

**Files:**
- Create: `internal/statuspage/statuspage.go`
- Create: `internal/statuspage/service.go`
- Test: `internal/statuspage/service_test.go`

**Interfaces:**
- Consumes: as queries do dono e `OwnTargetIDs` (Task 1).
- Produces:
  - `type Item struct { TargetID uuid.UUID; DisplayName string }`, `Page`, `Input`, `Patch` (com `Items *[]Item`), `ValidationError{Fields []FieldError}`, `FieldError{Field, Reason string}`.
  - `var ErrNotFound, ErrExists, ErrSlugTaken`; `const MaxItems = 20`.
  - `func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service`; `Get(ctx, userID) (Page, error)`; `Create(ctx, userID, Input) (Page, error)`; `Update(ctx, userID, id uuid.UUID, Patch) (Page, error)`.
  - Helpers de teste no pacote `statuspage_test`: `novoAmbiente`, `(ambiente).usuario`, `(ambiente).alvo`, `campos`, `agora` (a Task 8 usa).

**Contexto para quem implementa.** As duas violações de UNIQUE de `status_pages` se separam pelo nome da constraint (`status_pages_user_id_key`, `status_pages_slug_key`), que é o nome padrão que o Postgres dá. Uma validação que falha desfaz a transação inteira: a página não fica gravada pela metade. O `WITH CHECK` dos itens também recusaria um alvo alheio; a checagem em Go existe para apontar **qual** item (D4, D14). Por isso sabotar só uma das duas guardas deixa o teste de alvo alheio vermelho **pela mensagem**, e não pela propriedade.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/statuspage/service_test.go`:

```go
package statuspage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/statuspage"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

var agora = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

type ambiente struct {
	svc  *statuspage.Service
	pool *pgxpool.Pool
	clk  *clock.Fake
}

func novoAmbiente(t *testing.T) ambiente {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	return ambiente{svc: statuspage.NewService(pool, clk), pool: pool, clk: clk}
}

func (a ambiente) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// alvo cria um alvo do dono, com URL que não pode vazar para a página.
func (a ambiente) alvo(t *testing.T, dono uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: nome, URL: "https://interno.exemplo.com/health", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: a.clk.Now(), CreatedAt: a.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func campos(err error) []statuspage.FieldError {
	var verr *statuspage.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	return verr.Fields
}

func TestCreateKeepsTheOrderAndGetReadsItBack(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")

	criada, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "minha-empresa", Title: "  Status da Empresa  ", Published: true,
		Items: []statuspage.Item{{TargetID: site, DisplayName: " Site "}, {TargetID: api, DisplayName: "API"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lida, err := a.svc.Get(ctx, dono)
	if err != nil {
		t.Fatal(err)
	}
	quer := []statuspage.Item{{TargetID: site, DisplayName: "Site"}, {TargetID: api, DisplayName: "API"}}
	for _, p := range []statuspage.Page{criada, lida} {
		if p.ID != criada.ID || p.Slug != "minha-empresa" || p.Title != "Status da Empresa" || !p.Published ||
			!slices.Equal(p.Items, quer) || !p.CreatedAt.Equal(agora) {
			t.Errorf("página = %+v; queria a ordem dada, com espaços aparados", p)
		}
	}
}

func TestGetWithoutPageIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	if _, err := a.svc.Get(context.Background(), a.usuario(t)); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("Get sem página = %v, queria ErrNotFound", err)
	}
}

// Spec §2.1: uma página por usuário. O slug é único entre todos.
func TestOnePagePerUserAndSlugsAreUnique(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "Status"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa-2", Title: "Outra"}); !errors.Is(err, statuspage.ErrExists) {
		t.Errorf("segunda página do mesmo dono = %v, queria ErrExists", err)
	}
	if _, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "empresa", Title: "Minha"}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Errorf("slug de outro dono = %v, queria ErrSlugTaken", err)
	}
	pOutro, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "outra", Title: "Minha"})
	if err != nil {
		t.Fatal(err)
	}
	tomado := "empresa"
	if _, err := a.svc.Update(ctx, outro, pOutro.ID, statuspage.Patch{Slug: &tomado}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Errorf("PATCH para o slug de outro dono = %v, queria ErrSlugTaken", err)
	}
}

func TestValidationNamesEveryField(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo := a.alvo(t, dono, "api")
	vinteEUm := make([]statuspage.Item, statuspage.MaxItems+1)
	for i := range vinteEUm {
		vinteEUm[i] = statuspage.Item{TargetID: uuid.Must(uuid.NewV7()), DisplayName: "x"}
	}

	for nome, c := range map[string]struct {
		in   statuspage.Input
		quer statuspage.FieldError
	}{
		"slug vazio":          {statuspage.Input{Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "required"}},
		"slug maiúsculo":      {statuspage.Input{Slug: "Empresa", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug curto":          {statuspage.Input{Slug: "ab", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug com hífen":      {statuspage.Input{Slug: "-empresa", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug longo":          {statuspage.Input{Slug: strings.Repeat("a", 41), Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug com barra":      {statuspage.Input{Slug: "a/../b", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"título vazio":        {statuspage.Input{Slug: "empresa", Title: "   "}, statuspage.FieldError{Field: "title", Reason: "required"}},
		"título longo":        {statuspage.Input{Slug: "empresa", Title: strings.Repeat("é", 101)}, statuspage.FieldError{Field: "title", Reason: "too_long"}},
		"título com controle": {statuspage.Input{Slug: "empresa", Title: "a\nb"}, statuspage.FieldError{Field: "title", Reason: "invalid"}},
		"itens demais":        {statuspage.Input{Slug: "empresa", Title: "T", Items: vinteEUm}, statuspage.FieldError{Field: "items", Reason: "out_of_range"}},
		"nome vazio": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo}}}, statuspage.FieldError{Field: "items[0].display_name", Reason: "required"}},
		"alvo repetido": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo, DisplayName: "a"}, {TargetID: alvo, DisplayName: "b"}}},
			statuspage.FieldError{Field: "items[1].target_id", Reason: "invalid"}},
		"alvo nulo": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{DisplayName: "a"}}}, statuspage.FieldError{Field: "items[0].target_id", Reason: "invalid"}},
		"alvo inexistente": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo, DisplayName: "a"}, {TargetID: uuid.Must(uuid.NewV7()), DisplayName: "b"}}},
			statuspage.FieldError{Field: "items[1].target_id", Reason: "invalid"}},
	} {
		_, err := a.svc.Create(ctx, dono, c.in)
		if f := campos(err); len(f) != 1 || f[0] != c.quer {
			t.Errorf("%s: %v (%+v), queria %+v", nome, err, f, c.quer)
		}
	}
	if _, err := a.svc.Get(ctx, dono); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("uma entrada inválida deixou página gravada: %v", err)
	}
}

// Spec §5.5: o alvo de outro usuário não entra na página, e a recusa
// desfaz tudo, inclusive a página.
func TestAnotherUsersTargetIsRefused(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	alheio := a.alvo(t, outro, "do outro")

	_, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "T", Items: []statuspage.Item{{TargetID: alheio, DisplayName: "roubado"}},
	})
	if f := campos(err); len(f) != 1 || f[0] != (statuspage.FieldError{Field: "items[0].target_id", Reason: "invalid"}) {
		t.Fatalf("alvo alheio = %v, queria items[0].target_id/invalid", err)
	}
	if _, err := a.svc.Get(ctx, dono); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("a recusa deixou a página gravada: %v", err)
	}
}

func TestUpdateChangesOnlyWhatCameAndReplacesItemsWhole(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site, db1 := a.alvo(t, dono, "api"), a.alvo(t, dono, "site"), a.alvo(t, dono, "db")
	p, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "Status", Items: []statuspage.Item{{TargetID: api, DisplayName: "API"}, {TargetID: site, DisplayName: "Site"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	titulo, publicada := "Novo título", true
	p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Title: &titulo, Published: &publicada})
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Novo título" || !p.Published || p.Slug != "empresa" || len(p.Items) != 2 {
		t.Errorf("PATCH de título e publicação mexeu no resto: %+v", p)
	}

	novos := []statuspage.Item{{TargetID: db1, DisplayName: "Banco"}, {TargetID: api, DisplayName: "API"}}
	p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Items: &novos})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Items, novos) {
		t.Errorf("itens = %+v, queria a lista nova, na ordem nova", p.Items)
	}

	vazia := []statuspage.Item{}
	if p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Items: &vazia}); err != nil || len(p.Items) != 0 {
		t.Errorf("PATCH com items vazio = %+v, %v; queria sem itens", p.Items, err)
	}

	ruim := "Empresa"
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Slug: &ruim}); campos(err) == nil {
		t.Errorf("PATCH com slug inválido = %v, queria ValidationError", err)
	}
}

func TestUpdateOfAnotherIDIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	pOutro, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "outra", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	titulo := "invadido"
	for _, id := range []uuid.UUID{pOutro.ID, uuid.Must(uuid.NewV7())} {
		if _, err := a.svc.Update(ctx, dono, id, statuspage.Patch{Title: &titulo}); !errors.Is(err, statuspage.ErrNotFound) {
			t.Errorf("PATCH na página %s = %v, queria ErrNotFound", id, err)
		}
	}
	if p, _ := a.svc.Get(ctx, outro); p.Title != "T" {
		t.Errorf("a página do outro mudou: %+v", p)
	}
	semPagina := a.usuario(t)
	if _, err := a.svc.Update(ctx, semPagina, pOutro.ID, statuspage.Patch{Title: &titulo}); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("PATCH de quem não tem página = %v, queria ErrNotFound", err)
	}
}

// Alvo apagado sai da página junto (ON DELETE CASCADE nos itens): a página
// continua de pé, com os outros itens na ordem de antes.
func TestDeletedTargetLeavesThePage(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "T", Items: []statuspage.Item{{TargetID: api, DisplayName: "API"}, {TargetID: site, DisplayName: "Site"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: dono, ID: api})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.svc.Get(ctx, dono)
	if err != nil || len(p.Items) != 1 || p.Items[0].TargetID != site {
		t.Errorf("depois de apagar o alvo: %+v, %v; queria só o Site", p.Items, err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/statuspage/`
Expected: FAIL: o pacote `statuspage` não existe.

- [ ] **Step 3: Implementar**

Crie `internal/statuspage/statuspage.go`:

```go
// Package statuspage é a status page pública (spec §10): o cadastro do dono
// (GET/POST/PATCH /status-pages) e a projeção pública de /p/{slug}.
//
// Uma página por usuário (spec §2.1). O dono escolhe quais alvos entram,
// com que nome de exibição e em que ordem. O que sai para o público é só o
// que a spec §5.6 permite; a projeção pública tem tipos próprios, e nenhum
// deles carrega URL, id ou mensagem de erro.
package statuspage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("status page não encontrada")
	// ErrExists: o usuário já tem a página dele (uma por usuário).
	ErrExists = errors.New("o usuário já tem uma status page")
	// ErrSlugTaken: outro usuário já publicou (ou reservou) este slug.
	ErrSlugTaken = errors.New("slug em uso")
)

// Motivos de validação: o mesmo vocabulário fechado de targets.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
	ReasonTooLong    = "too_long"
)

// Limites. Os de texto têm par num CHECK da migration 00006.
const (
	MaxItems        = 20
	maxTitleRunes   = 100
	maxDisplayRunes = 100
)

// slugRE é o CHECK de status_pages.slug: 3 a 40 caracteres, minúsculas,
// dígitos e hífen, sem hífen nas pontas.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

type Item struct {
	TargetID    uuid.UUID
	DisplayName string
}

type Page struct {
	ID        uuid.UUID
	Slug      string
	Title     string
	Published bool
	Items     []Item // na ordem de exibição
	CreatedAt time.Time
}

type Input struct {
	Slug      string
	Title     string
	Published bool
	Items     []Item
}

// Patch muda só o que vier preenchido. Items, quando vem, troca a lista
// inteira: a ordem é a posição na lista, e um PATCH por item exigiria
// reordenar os outros.
type Patch struct {
	Slug, Title *string
	Published   *bool
	Items       *[]Item
}

type FieldError struct {
	Field  string
	Reason string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return "dados da status page inválidos" }

func (e *ValidationError) add(field, reason string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Reason: reason})
}

// validate confere tudo e acumula os erros, como targets. Normaliza no
// lugar: título e nomes de exibição sem espaço nas pontas. O slug NÃO é
// normalizado: ele vira URL pública, e o dono tem de ver exatamente o que
// publica.
func validate(in *Input) *ValidationError {
	var v ValidationError
	switch {
	case in.Slug == "":
		v.add("slug", ReasonRequired)
	case !slugRE.MatchString(in.Slug):
		v.add("slug", ReasonInvalid)
	}
	in.Title = strings.TrimSpace(in.Title)
	if motivo := texto(in.Title, maxTitleRunes); motivo != "" {
		v.add("title", motivo)
	}
	if len(in.Items) > MaxItems {
		v.add("items", ReasonOutOfRange)
		return &v
	}
	vistos := map[uuid.UUID]bool{}
	for i := range in.Items {
		it := &in.Items[i]
		it.DisplayName = strings.TrimSpace(it.DisplayName)
		if motivo := texto(it.DisplayName, maxDisplayRunes); motivo != "" {
			v.add(fmt.Sprintf("items[%d].display_name", i), motivo)
		}
		if it.TargetID == uuid.Nil || vistos[it.TargetID] {
			v.add(fmt.Sprintf("items[%d].target_id", i), ReasonInvalid)
		}
		vistos[it.TargetID] = true
	}
	if len(v.Fields) > 0 {
		return &v
	}
	return nil
}

func texto(s string, max int) string {
	switch {
	case s == "":
		return ReasonRequired
	case utf8.RuneCountInString(s) > max:
		return ReasonTooLong
	case strings.ContainsFunc(s, unicode.IsControl):
		return ReasonInvalid
	}
	return ""
}
```

Crie `internal/statuspage/service.go`:

```go
package statuspage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Nomes que o Postgres dá aos UNIQUE da migration 00006.
const (
	constraintUser = "status_pages_user_id_key"
	constraintSlug = "status_pages_slug_key"
)

// Service é o cadastro do dono. Toda operação exige userID e roda sob
// storage.InUserTx (spec §5.5).
type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

// Get devolve a página do usuário, ou ErrNotFound se ele ainda não criou.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Page, error) {
	var p Page
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// Create cria a página. A segunda do mesmo usuário é ErrExists, e o slug de
// outro é ErrSlugTaken: os dois vêm do UNIQUE do banco, e não de uma
// consulta antes, então valem também sob concorrência.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Page, error) {
	if verr := validate(&in); verr != nil {
		return Page{}, verr
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Page{}, err
	}
	var p Page
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: id, UserID: userID, Slug: in.Slug, Title: in.Title, Published: in.Published, CreatedAt: s.clk.Now(),
		}); err != nil {
			return traduzir(err)
		}
		if err := gravarItens(ctx, q, userID, id, in.Items); err != nil {
			return err
		}
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// Update aplica o patch à página id do usuário. A validação roda sobre o
// resultado, como se fosse um cadastro novo.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, patch Patch) (Page, error) {
	var p Page
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		atual, err := ler(ctx, q, userID)
		if err != nil {
			return err
		}
		if atual.ID != id {
			return ErrNotFound
		}
		in := Input{Slug: atual.Slug, Title: atual.Title, Published: atual.Published, Items: atual.Items}
		if patch.Slug != nil {
			in.Slug = *patch.Slug
		}
		if patch.Title != nil {
			in.Title = *patch.Title
		}
		if patch.Published != nil {
			in.Published = *patch.Published
		}
		if patch.Items != nil {
			in.Items = *patch.Items
		}
		if verr := validate(&in); verr != nil {
			return verr
		}
		if _, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{
			UserID: userID, ID: id, Slug: in.Slug, Title: in.Title, Published: in.Published,
		}); err != nil {
			return traduzir(err)
		}
		if patch.Items != nil {
			if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{UserID: userID, PageID: id}); err != nil {
				return fmt.Errorf("apagar itens: %w", err)
			}
			if err := gravarItens(ctx, q, userID, id, in.Items); err != nil {
				return err
			}
		}
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// gravarItens confere que cada alvo é do dono e grava na ordem dada. A RLS
// dos itens também recusa alvo alheio (WITH CHECK da migration 00006); esta
// checagem existe para dizer QUAL item está errado, em vez de um erro de
// política.
func gravarItens(ctx context.Context, q *db.Queries, userID, pageID uuid.UUID, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.TargetID
	}
	meus, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{UserID: userID, Ids: ids})
	if err != nil {
		return fmt.Errorf("conferir alvos: %w", err)
	}
	dono := make(map[uuid.UUID]bool, len(meus))
	for _, id := range meus {
		dono[id] = true
	}
	var verr ValidationError
	for i, it := range items {
		if !dono[it.TargetID] {
			verr.add(fmt.Sprintf("items[%d].target_id", i), ReasonInvalid)
		}
	}
	if len(verr.Fields) > 0 {
		return &verr
	}
	for i, it := range items {
		if err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pageID, TargetID: it.TargetID, DisplayName: it.DisplayName, Position: int32(i),
		}); err != nil {
			return fmt.Errorf("gravar item: %w", err)
		}
	}
	return nil
}

func ler(ctx context.Context, q *db.Queries, userID uuid.UUID) (Page, error) {
	row, err := q.GetStatusPage(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, fmt.Errorf("buscar status page: %w", err)
	}
	itens, err := q.ListStatusPageItems(ctx, db.ListStatusPageItemsParams{UserID: userID, PageID: row.ID})
	if err != nil {
		return Page{}, fmt.Errorf("listar itens: %w", err)
	}
	p := Page{
		ID: row.ID, Slug: row.Slug, Title: row.Title, Published: row.Published,
		Items: make([]Item, 0, len(itens)), CreatedAt: row.CreatedAt,
	}
	for _, it := range itens {
		p.Items = append(p.Items, Item{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return p, nil
}

// traduzir separa as duas violações de UNIQUE de status_pages. O resto sai
// como erro interno.
func traduzir(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case constraintUser:
			return ErrExists
		case constraintSlug:
			return ErrSlugTaken
		}
	}
	return fmt.Errorf("gravar status page: %w", err)
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/statuspage/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 ./internal/statuspage/`; restaure:
1. Em `traduzir`, devolva `ErrSlugTaken` para `constraintUser`. Expected: FAIL em `TestOnePagePerUserAndSlugsAreUnique`.
2. Em `gravarItens`, troque `if !dono[it.TargetID]` por `if false && !dono[it.TargetID]`. Expected: FAIL em `TestAnotherUsersTargetIsRefused` e `TestValidationNamesEveryField` (o `WITH CHECK` recusa, mas como erro interno, sem o campo).
3. Em `Update`, troque `if atual.ID != id` por `if false`. Expected: FAIL em `TestUpdateOfAnotherIDIsNotFound`.
4. Em `Update`, tire a chamada a `validate`. Expected: FAIL em `TestUpdateChangesOnlyWhatCameAndReplacesItemsWhole`.
5. Em `validate`, troque `vistos[it.TargetID]` por nada (aceite repetido), e depois `> MaxItems` por `> MaxItems+5`. Expected: FAIL em `TestValidationNamesEveryField`.

- [ ] **Step 6: Commit**

```bash
git add internal/statuspage/
git commit -m "feat(statuspage): cadastro da status page do dono, uma por usuário"
```

---

### Task 7: API da status page do dono

**Files:**
- Modify: `internal/apierr/errors.go` (`status_page_exists`, `slug_taken`)
- Create: `internal/httpapi/statuspagehandlers.go`
- Modify: `internal/httpapi/routes.go` (`Deps.StatusPages`, três rotas)
- Modify: `internal/httpapi/auth_integration_test.go` (`StatusPages` no `Deps` de teste)
- Modify: `cmd/vigilume/main.go` (`StatusPages` no wiring)
- Modify: `cmd/vigilume/incident_e2e_test.go` (`/api/v1/status-pages` na lista do wiring)
- Test: `internal/httpapi/statuspages_integration_test.go`

**Interfaces:**
- Consumes: `statuspage.Service` (Task 6).
- Produces:
  - `Deps.StatusPages *statuspage.Service` (a Task 9 usa o mesmo serviço para `/p/`).
  - `func NewStatusPageHandlers(svc *statuspage.Service, publicBaseURL string) *StatusPageHandlers`.
  - JSON da página: `{id, slug, title, published, items: [{target_id, display_name}], public_url, created_at}`. `GET` devolve `{"items": [...]}` com zero ou uma página.
  - Helper de teste `paginaJSON` (a Task 9 usa).

**Contexto para quem implementa.** O `decodeJSONStrict` recusa campo desconhecido: um `"publised": true` ignorado em silêncio faria o dono achar que publicou. O teste de 401 prova a **propriedade**, e não o middleware: o handler também responde 401 sem usuário no contexto (D25). Quem pega a rota sem `authn` é `TestStatusPageFlow`.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/httpapi/statuspages_integration_test.go`:

```go
package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/apierr"
)

type paginaJSON struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Published bool   `json:"published"`
	Items     []struct {
		TargetID    string `json:"target_id"`
		DisplayName string `json:"display_name"`
	} `json:"items"`
	PublicURL string `json:"public_url"`
}

func paginasDe(t *testing.T, a *api, tok string) []paginaJSON {
	t.Helper()
	var l struct {
		Items []paginaJSON `json:"items"`
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/status-pages", tok, nil)
	cru := lerCorpo(t, resp, &l)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(cru), `"items":[`) {
		t.Fatalf("GET /status-pages deu %d: %s", resp.StatusCode, cru)
	}
	return l.Items
}

func TestStatusPageFlow(t *testing.T) {
	a := newAPIWith(t, nil)
	tok := tokenDe(t, a, a.userID)
	if ps := paginasDe(t, a, tok); len(ps) != 0 {
		t.Fatalf("antes de criar: %+v", ps)
	}
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &alvo)

	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "Status da Empresa",
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "API pública"}},
	})
	var criada paginaJSON
	cru := lerCorpo(t, resp, &criada)
	if resp.StatusCode != http.StatusCreated || criada.Slug != "empresa" || criada.Published ||
		len(criada.Items) != 1 || criada.Items[0].TargetID != alvo.ID || criada.PublicURL != "/p/empresa" {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	if ps := paginasDe(t, a, tok); len(ps) != 1 || ps[0].ID != criada.ID {
		t.Fatalf("lista = %+v", ps)
	}

	resp = chamar(t, a, http.MethodPatch, "/api/v1/status-pages/"+criada.ID, tok, map[string]any{"published": true})
	var mudada paginaJSON
	cru = lerCorpo(t, resp, &mudada)
	if resp.StatusCode != http.StatusOK || !mudada.Published || mudada.Title != "Status da Empresa" || len(mudada.Items) != 1 {
		t.Errorf("PATCH deu %d: %s", resp.StatusCode, cru)
	}

	// Segunda página: 409 com o código próprio.
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "outra", "title": "T"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusConflict || env.Error.Code != "status_page_exists" {
		t.Errorf("segunda página deu %d/%s, queria 409 status_page_exists", resp.StatusCode, env.Error.Code)
	}

	// Slug de outro usuário: 409 slug_taken.
	tokB := tokenDe(t, a, outroUsuario(t, a))
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tokB, map[string]any{"slug": "empresa", "title": "T"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusConflict || env.Error.Code != "slug_taken" {
		t.Errorf("slug tomado deu %d/%s, queria 409 slug_taken", resp.StatusCode, env.Error.Code)
	}

	// Outro usuário não vê nem muda a página de A.
	if ps := paginasDe(t, a, tokB); len(ps) != 0 {
		t.Errorf("B vê a página de A: %+v", ps)
	}
	resp = chamar(t, a, http.MethodPatch, "/api/v1/status-pages/"+criada.ID, tokB, map[string]any{"title": "invadido"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("PATCH de B na página de A deu %d, queria 404", resp.StatusCode)
	}
}

func TestStatusPageValidationPointsAtTheItem(t *testing.T) {
	a := newAPIWith(t, nil)
	tok := tokenDe(t, a, a.userID)
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &alvo)

	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "Empresa", "title": "",
		"items": []map[string]any{
			{"target_id": alvo.ID, "display_name": "ok"},
			{"target_id": "01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3", "display_name": "<script>"},
		},
	})
	var env envelopeComCampos
	cru := lerCorpo(t, resp, &env)
	quer := []apierr.FieldError{
		{Field: "slug", Reason: "invalid"}, {Field: "title", Reason: "required"},
	}
	if resp.StatusCode != http.StatusBadRequest || !slices.Equal(env.Error.Fields, quer) {
		t.Errorf("POST inválido deu %d: %s; queria %+v", resp.StatusCode, cru, quer)
	}
	if strings.Contains(string(cru), "script") || strings.Contains(string(cru), "Empresa") {
		t.Errorf("o 400 ecoou a entrada: %s", cru)
	}

	// Com os campos da página certos, o alvo alheio aparece pela posição.
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "T",
		"items": []map[string]any{
			{"target_id": alvo.ID, "display_name": "ok"},
			{"target_id": "01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3", "display_name": "x"},
		},
	})
	cru = lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
		env.Error.Fields[0] != (apierr.FieldError{Field: "items[1].target_id", Reason: "invalid"}) {
		t.Errorf("alvo inexistente deu %d: %s", resp.StatusCode, cru)
	}

	// Campo desconhecido é 400: um "publised" ignorado em silêncio faria o
	// dono achar que publicou.
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "empresa", "title": "T", "publised": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("campo desconhecido deu %d, queria 400", resp.StatusCode)
	}
}

// Prova a propriedade (401 sem token), não o middleware: o handler também
// responde 401 quando não acha o usuário no contexto. Quem pega a rota sem
// authn é TestStatusPageFlow, em que o dono passa a receber 401.
func TestStatusPageRoutesRequireAuthentication(t *testing.T) {
	a := newAPIWith(t, nil)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/status-pages"},
		{http.MethodPost, "/api/v1/status-pages"},
		{http.MethodPatch, "/api/v1/status-pages/01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", map[string]any{})
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}
```

Aplique em `cmd/vigilume/incident_e2e_test.go`:

```diff
--- a/cmd/vigilume/incident_e2e_test.go
+++ b/cmd/vigilume/incident_e2e_test.go
@@ -202,8 +202,8 @@
 	}
 }
 
-// O papel api com as dependências do Plano 3 ligadas: sem Channels ou
-// Incidents no Deps, as rotas cairiam em panic (nil) na primeira chamada
+// O papel api com as dependências dos Planos 3 e 4 ligadas: sem Channels,
+// Incidents ou StatusPages no Deps, as rotas cairiam em panic (nil) na primeira chamada
 // autenticada; sem Ready, /readyz responderia 503.
 func TestAPIRoleWiresIncidentsChannelsAndReadyz(t *testing.T) {
 	pool := testdb.New(t)
@@ -248,7 +248,7 @@
 		}
 		time.Sleep(50 * time.Millisecond)
 	}
-	for _, caminho := range []string{"/api/v1/channels", "/api/v1/incidents"} {
+	for _, caminho := range []string{"/api/v1/channels", "/api/v1/incidents", "/api/v1/status-pages"} {
 		if s := pedir(caminho); s != http.StatusOK {
 			t.Errorf("GET %s deu %d, queria 200", caminho, s)
 		}
```

Aplique em `internal/httpapi/auth_integration_test.go`:

```diff
--- a/internal/httpapi/auth_integration_test.go
+++ b/internal/httpapi/auth_integration_test.go
@@ -23,6 +23,7 @@
 	"github.com/BipoXDXD/vigilume/internal/httpapi"
 	"github.com/BipoXDXD/vigilume/internal/incident/history"
 	"github.com/BipoXDXD/vigilume/internal/notify"
+	"github.com/BipoXDXD/vigilume/internal/statuspage"
 	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
 	"github.com/BipoXDXD/vigilume/internal/targets"
 )
@@ -112,10 +113,11 @@
 	discord, email := &discordFalso{}, &carteiroFalso{}
 	deps := httpapi.Deps{
 		Store: store, Sessions: mgr, TOTP: segundoFator,
-		Targets:   targets.NewService(pool, box, clk),
-		Incidents: history.NewService(pool, clk),
-		Channels:  notify.NewChannelService(pool, box, clk, discord, email, cfg.PublicBaseURL),
-		Clock:     clk, Ready: pool.Ping,
+		Targets:     targets.NewService(pool, box, clk),
+		Incidents:   history.NewService(pool, clk),
+		Channels:    notify.NewChannelService(pool, box, clk, discord, email, cfg.PublicBaseURL),
+		StatusPages: statuspage.NewService(pool, clk),
+		Clock:       clk, Ready: pool.Ping,
 	}
 	if ajustarDeps != nil {
 		ajustarDeps(&deps)
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/httpapi/ ./cmd/...`
Expected: `internal/httpapi` FAIL de compilação (`StatusPages` não é campo de `Deps`); `cmd/vigilume` FAIL em `TestAPIRoleWiresIncidentsChannelsAndReadyz`: `GET /api/v1/status-pages deu 404`.

- [ ] **Step 3: Implementar**

Aplique em `internal/apierr/errors.go`:

```diff
--- a/internal/apierr/errors.go
+++ b/internal/apierr/errors.go
@@ -35,6 +35,8 @@
 	CodeChannelLimit       ErrorCode = "channel_limit_reached"
 	CodeChannelNotPending  ErrorCode = "channel_not_pending"
 	CodeDeliveryFailed     ErrorCode = "delivery_failed"
+	CodeStatusPageExists   ErrorCode = "status_page_exists"
+	CodeSlugTaken          ErrorCode = "slug_taken"
 )
 
 // A mensagem vem sempre desta tabela, nunca do erro subjacente.
@@ -61,6 +63,9 @@
 	// 502: a falha foi do destino (Discord ou provedor de e-mail), não do
 	// pedido nem deste servidor.
 	CodeDeliveryFailed: {http.StatusBadGateway, "Não foi possível entregar a mensagem de verificação."},
+	// Uma status page por usuário (spec §2.1); a segunda é conflito.
+	CodeStatusPageExists: {http.StatusConflict, "Você já tem uma status page."},
+	CodeSlugTaken:        {http.StatusConflict, "Este endereço já está em uso."},
 }
 
 // FieldError aponta o campo culpado de um 400 (D14). Field e Reason vêm de
```

Crie `internal/httpapi/statuspagehandlers.go`:

```go
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/statuspage"
)

// StatusPageHandlers servem o cadastro do dono (spec §9). A API é no
// plural, como a §9, mas cada usuário tem uma página só (spec §2.1): GET
// devolve uma lista de zero ou um item.
type StatusPageHandlers struct {
	svc     *statuspage.Service
	baseURL string
}

func NewStatusPageHandlers(svc *statuspage.Service, publicBaseURL string) *StatusPageHandlers {
	return &StatusPageHandlers{svc: svc, baseURL: strings.TrimSuffix(publicBaseURL, "/")}
}

type statusPageItemJSON struct {
	TargetID    uuid.UUID `json:"target_id"`
	DisplayName string    `json:"display_name"`
}

type statusPageRequest struct {
	Slug      string               `json:"slug"`
	Title     string               `json:"title"`
	Published bool                 `json:"published"`
	Items     []statusPageItemJSON `json:"items"`
}

type statusPagePatchRequest struct {
	Slug      *string               `json:"slug"`
	Title     *string               `json:"title"`
	Published *bool                 `json:"published"`
	Items     *[]statusPageItemJSON `json:"items"`
}

type statusPageResponse struct {
	ID        string               `json:"id"`
	Slug      string               `json:"slug"`
	Title     string               `json:"title"`
	Published bool                 `json:"published"`
	Items     []statusPageItemJSON `json:"items"`
	// PublicURL é o link que o painel mostra (spec §11.3). Existe mesmo em
	// rascunho: o dono vê o endereço antes de publicar, e ele dá 404 até lá.
	PublicURL string    `json:"public_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *StatusPageHandlers) toResponse(p statuspage.Page) statusPageResponse {
	items := make([]statusPageItemJSON, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, statusPageItemJSON{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return statusPageResponse{
		ID: p.ID.String(), Slug: p.Slug, Title: p.Title, Published: p.Published, Items: items,
		PublicURL: h.baseURL + "/p/" + p.Slug, CreatedAt: p.CreatedAt,
	}
}

func paraItens(in []statusPageItemJSON) []statuspage.Item {
	out := make([]statuspage.Item, 0, len(in))
	for _, it := range in {
		out = append(out, statuspage.Item{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return out
}

func (h *StatusPageHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	items := []statusPageResponse{}
	p, err := h.svc.Get(r.Context(), userID)
	switch {
	case errors.Is(err, statuspage.ErrNotFound):
	case h.falhou(w, r, "buscar status page", err):
		return
	default:
		items = append(items, h.toResponse(p))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *StatusPageHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req statusPageRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	p, err := h.svc.Create(r.Context(), userID, statuspage.Input{
		Slug: req.Slug, Title: req.Title, Published: req.Published, Items: paraItens(req.Items),
	})
	if h.falhou(w, r, "criar status page", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, h.toResponse(p))
}

func (h *StatusPageHandlers) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req statusPagePatchRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	patch := statuspage.Patch{Slug: req.Slug, Title: req.Title, Published: req.Published}
	if req.Items != nil {
		itens := paraItens(*req.Items)
		patch.Items = &itens
	}
	p, err := h.svc.Update(r.Context(), userID, id, patch)
	if h.falhou(w, r, "atualizar status page", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, h.toResponse(p))
}

// falhou traduz o erro do domínio. Os campos de validação são vocabulário
// fechado; "items[3].display_name" leva só a posição, nunca o conteúdo.
func (h *StatusPageHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *statuspage.ValidationError
	switch {
	case errors.As(err, &verr):
		campos := make([]apierr.FieldError, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			campos = append(campos, apierr.FieldError{Field: f.Field, Reason: f.Reason})
		}
		apierr.WriteValidation(w, r, campos)
	case errors.Is(err, statuspage.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, statuspage.ErrExists):
		apierr.WriteError(w, r, apierr.CodeStatusPageExists)
	case errors.Is(err, statuspage.ErrSlugTaken):
		apierr.WriteError(w, r, apierr.CodeSlugTaken)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}
```

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -14,6 +14,7 @@
 	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
 	"github.com/BipoXDXD/vigilume/internal/incident/history"
 	"github.com/BipoXDXD/vigilume/internal/notify"
+	"github.com/BipoXDXD/vigilume/internal/statuspage"
 	"github.com/BipoXDXD/vigilume/internal/targets"
 )
 
@@ -40,7 +41,9 @@
 	Targets   *targets.Service
 	Incidents *history.Service
 	Channels  *notify.ChannelService
-	Clock     clock.Clock
+	// StatusPages serve o cadastro do dono e a página pública /p/{slug}.
+	StatusPages *statuspage.Service
+	Clock       clock.Clock
 	// Ready é o teste de prontidão de /readyz: o ping no banco em produção.
 	Ready func(context.Context) error
 }
@@ -58,6 +61,7 @@
 	th := NewTargetHandlers(d.Targets, d.Clock)
 	ih := NewIncidentHandlers(d.Incidents)
 	ch := NewChannelHandlers(d.Channels)
+	sp := NewStatusPageHandlers(d.StatusPages, cfg.PublicBaseURL)
 	authn := middleware.Authn(cfg.JWTSecret, d.Clock)
 
 	mux := http.NewServeMux()
@@ -123,6 +127,10 @@
 	mux.Handle("DELETE /api/v1/channels/{id}", protegida(ch.Delete))
 	mux.Handle("POST /api/v1/channels/{id}/verify/resend", comEnvio(ch.Resend))
 
+	mux.Handle("GET /api/v1/status-pages", protegida(sp.List))
+	mux.Handle("POST /api/v1/status-pages", protegida(sp.Create))
+	mux.Handle("PATCH /api/v1/status-pages/{id}", protegida(sp.Update))
+
 	// Spec §13: /healthz é raso (o processo responde); /readyz verifica o
 	// banco.
 	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
```

Aplique em `cmd/vigilume/main.go`:

```diff
--- a/cmd/vigilume/main.go
+++ b/cmd/vigilume/main.go
@@ -23,6 +23,7 @@
 	"github.com/BipoXDXD/vigilume/internal/notify"
 	"github.com/BipoXDXD/vigilume/internal/observability"
 	"github.com/BipoXDXD/vigilume/internal/prober"
+	"github.com/BipoXDXD/vigilume/internal/statuspage"
 	"github.com/BipoXDXD/vigilume/internal/storage"
 	"github.com/BipoXDXD/vigilume/internal/targets"
 )
@@ -138,14 +139,15 @@
 	if cfg.Role.RunsAPI() {
 		store := auth.NewPGStore(pool)
 		deps := httpapi.Deps{
-			Store:     store,
-			Sessions:  auth.NewManager(store, clk, cfg.IPHashKey),
-			TOTP:      auth.NewTOTP(pool, box, totpIssuer),
-			Targets:   targets.NewService(pool, box, clk),
-			Incidents: history.NewService(pool, clk),
-			Channels:  notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg), cfg.PublicBaseURL),
-			Clock:     clk,
-			Ready:     pool.Ping,
+			Store:       store,
+			Sessions:    auth.NewManager(store, clk, cfg.IPHashKey),
+			TOTP:        auth.NewTOTP(pool, box, totpIssuer),
+			Targets:     targets.NewService(pool, box, clk),
+			Incidents:   history.NewService(pool, clk),
+			Channels:    notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg), cfg.PublicBaseURL),
+			StatusPages: statuspage.NewService(pool, clk),
+			Clock:       clk,
+			Ready:       pool.Ping,
 		}
 		servir(gctx, g, httpapi.NewServer(cfg, httpapi.Routes(gctx, cfg, deps)), "api", cfg.ShutdownGrace)
 	}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . && go vet ./... && go test -race -count=1 ./internal/httpapi/ ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run 'TestStatusPage|TestAPIRoleWires' ./internal/httpapi/ ./cmd/...`; restaure:
1. Rota `PATCH` com `http.HandlerFunc(sp.Update)` em vez de `protegida(sp.Update)`. Expected: FAIL em `TestStatusPageFlow`.
2. Apague a rota `GET /api/v1/status-pages`. Expected: FAIL em `TestStatusPageFlow`.
3. Em `falhou`, mapeie `ErrSlugTaken` para `CodeStatusPageExists`. Expected: FAIL.
4. Em `List`, tire o `case errors.Is(err, statuspage.ErrNotFound):`. Expected: FAIL (o `GET` sem página vira 500).
5. Em `main.go`, troque `statuspage.NewService(pool, clk)` por `(*statuspage.Service)(nil)`. Expected: FAIL em `TestAPIRoleWiresIncidentsChannelsAndReadyz`.

- [ ] **Step 6: Commit**

```bash
git add internal/apierr/ internal/httpapi/ cmd/vigilume/
git commit -m "feat(httpapi): GET/POST /status-pages e PATCH /status-pages/{id}"
```

---

### Task 8: Projeção pública

O que `/p/{slug}` mostra, sem HTTP ainda: estado atual, barra de 90 dias, uptime, latência e incidentes, sob `InPublicTx`, com cache de 30 s.

**Files:**
- Create: `internal/statuspage/public.go`
- Modify: `internal/statuspage/service.go` (campos `cache` e `voo`)
- Test: `internal/statuspage/public_test.go`

**Interfaces:**
- Consumes: `InPublicTx` e as consultas públicas (Task 1); helpers de teste da Task 6.
- Produces:
  - `func (s *Service) Public(ctx context.Context, slug string) (View, error)`: `ErrNotFound` para inexistente, rascunho e slug malformado.
  - `type View struct { Title string; Items []PublicItem; Incidents []PublicIncident; GeneratedAt time.Time }`.
  - `type PublicItem struct { Name string; State State; Days [Days]Day; Uptime90 *float64; LatencyMS *int32 }`; `type Day struct { Date time.Time; Uptime *float64 }`.
  - `type PublicIncident struct { Name string; StartedAt, ResolvedAt time.Time }` com `Duration()`.
  - `State`: `StateUp`, `StateDegraded`, `StateDown`, `StateUnknown`; `const Days = 90`, `MaxIncidents = 20`.

**Contexto para quem implementa.** Os tipos públicos não têm onde guardar id, URL, causa ou código: `TestPublicViewHasNoInternalFields` percorre os tipos por reflexão e quebra se alguém acrescentar um campo com esses nomes, ou um `uuid.UUID`, antes que ele chegue ao template. A latência de 24h é `sum(p50 × ok) / sum(ok)`: `sum` de `bigint` é `numeric`, então a divisão é exata e o `::int` arredonda (166,67 → 167). No `singleflight`, a consulta usa `context.WithoutCancel` com prazo próprio: ela é de todos que esperam, e o visitante que fecha a aba não pode derrubar os outros (D19).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/statuspage/public_test.go`:

```go
package statuspage_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/statuspage"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// admin enxerga o banco como superusuário: grava rollups e incidentes em
// qualquer data, sem depender do job de manutenção.
func admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := storage.NewPool(context.Background(), testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func (a ambiente) check(t *testing.T, alvo uuid.UUID, em time.Time, status string) {
	t.Helper()
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, a.pool, em); err != nil {
		t.Fatal(err)
	}
	p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: 50}
	if status == "down" {
		kind := "timeout"
		p.ErrorKind = &kind
	}
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) })
	if err != nil {
		t.Fatal(err)
	}
}

func rollup(t *testing.T, adm *pgxpool.Pool, alvo uuid.UUID, hora time.Time, total, ok int, p50 *int) {
	t.Helper()
	if _, err := adm.Exec(context.Background(), `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, $3, $4, $5, $5, $5, $5)`, alvo, hora, total, ok, p50); err != nil {
		t.Fatal(err)
	}
}

func incidenteResolvido(t *testing.T, adm *pgxpool.Pool, alvo uuid.UUID, inicio, fim time.Time) {
	t.Helper()
	if _, err := adm.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause)
		VALUES ($1, $2, $3, $4, 'timeout')`, uuid.Must(uuid.NewV7()), alvo, inicio, fim); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) publicar(t *testing.T, dono uuid.UUID, slug string, publicada bool, itens ...statuspage.Item) statuspage.Page {
	t.Helper()
	p, err := a.svc.Create(context.Background(), dono, statuspage.Input{Slug: slug, Title: "Status", Published: publicada, Items: itens})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Spec §10: rascunho, slug inexistente e slug malformado são o mesmo 404.
func TestPublicFindsOnlyPublishedPages(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	a.publicar(t, a.usuario(t), "rascunho", false)
	for _, slug := range []string{"rascunho", "nao-existe", "Maiuscula", "../etc", ""} {
		if _, err := a.svc.Public(ctx, slug); !errors.Is(err, statuspage.ErrNotFound) {
			t.Errorf("Public(%q) = %v, queria ErrNotFound", slug, err)
		}
	}
}

func TestPublicProjection(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site, velho, desligado, fora := a.alvo(t, dono, "api"), a.alvo(t, dono, "site"),
		a.alvo(t, dono, "velho"), a.alvo(t, dono, "desligado"), a.alvo(t, dono, "fora da página")
	nunca := a.alvo(t, dono, "nunca sondado")
	if _, err := adm.Exec(ctx, `UPDATE targets SET enabled = false WHERE id = $1`, desligado); err != nil {
		t.Fatal(err)
	}
	a.publicar(t, dono, "empresa", true,
		statuspage.Item{TargetID: site, DisplayName: "Site"}, statuspage.Item{TargetID: api, DisplayName: "API"},
		statuspage.Item{TargetID: velho, DisplayName: "Velho"}, statuspage.Item{TargetID: desligado, DisplayName: "Desligado"},
		statuspage.Item{TargetID: nunca, DisplayName: "Nunca"})

	agora := a.clk.Now() // 2026-09-24 12:00 UTC; intervalo dos alvos: 60s
	a.check(t, api, agora.Add(-time.Minute), "down")
	a.check(t, site, agora.Add(-3*time.Minute), "degraded") // exatamente 3 intervalos: ainda recente
	a.check(t, velho, agora.Add(-3*time.Minute-time.Second), "up")
	a.check(t, desligado, agora.Add(-time.Minute), "up")
	a.check(t, fora, agora.Add(-time.Minute), "up")

	hoje := agora.Truncate(24 * time.Hour)
	primeiro := hoje.AddDate(0, 0, -(statuspage.Days - 1))
	p50a, p50b := 100, 300
	rollup(t, adm, api, hoje.Add(8*time.Hour), 60, 60, &p50a)    // hoje: 60 de 60
	rollup(t, adm, api, hoje.Add(9*time.Hour), 60, 30, &p50b)    // hoje: +30 de 60
	rollup(t, adm, api, primeiro.Add(time.Hour), 10, 0, nil)     // 1º dia: 0 de 10
	rollup(t, adm, api, primeiro.Add(-time.Hour), 10, 10, &p50a) // antes da barra: fora
	rollup(t, adm, fora, hoje.Add(8*time.Hour), 60, 0, nil)      // fora da página

	incidenteResolvido(t, adm, api, agora.Add(-48*time.Hour), agora.Add(-47*time.Hour))
	incidenteResolvido(t, adm, api, agora.Add(-2*time.Hour), agora.Add(-time.Hour-50*time.Minute))
	incidenteResolvido(t, adm, fora, agora.Add(-time.Hour), agora.Add(-30*time.Minute))
	incidenteResolvido(t, adm, api, primeiro.Add(-time.Hour), primeiro.Add(-30*time.Minute)) // antes dos 90 dias
	if _, err := adm.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		uuid.Must(uuid.NewV7()), site, agora.Add(-time.Minute)); err != nil { // aberto: não entra
		t.Fatal(err)
	}

	v, err := a.svc.Public(ctx, "empresa")
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "Status" || len(v.Items) != 5 {
		t.Fatalf("página = %q com %d itens, queria os 5 da página", v.Title, len(v.Items))
	}
	nomes := []string{}
	estados := map[string]statuspage.State{}
	for _, it := range v.Items {
		nomes = append(nomes, it.Name)
		estados[it.Name] = it.State
	}
	if strings.Join(nomes, ",") != "Site,API,Velho,Desligado,Nunca" {
		t.Errorf("ordem = %v, queria a da página", nomes)
	}
	for nome, quer := range map[string]statuspage.State{
		"Site": statuspage.StateDegraded, "API": statuspage.StateDown, "Velho": statuspage.StateUnknown,
		"Desligado": statuspage.StateUnknown, "Nunca": statuspage.StateUnknown,
	} {
		if estados[nome] != quer {
			t.Errorf("estado de %s = %q, queria %q", nome, estados[nome], quer)
		}
	}

	apiItem := v.Items[1]
	if !apiItem.Days[0].Date.Equal(primeiro) || !apiItem.Days[statuspage.Days-1].Date.Equal(hoje) {
		t.Errorf("barra de %v a %v, queria de %v a %v", apiItem.Days[0].Date, apiItem.Days[statuspage.Days-1].Date, primeiro, hoje)
	}
	if u := apiItem.Days[statuspage.Days-1].Uptime; u == nil || *u != 0.75 {
		t.Errorf("uptime de hoje = %v, queria 0.75 (90 de 120)", u)
	}
	if u := apiItem.Days[0].Uptime; u == nil || *u != 0 {
		t.Errorf("uptime do 1º dia = %v, queria 0", u)
	}
	if apiItem.Days[1].Uptime != nil {
		t.Errorf("dia sem check tem uptime %v, queria nil", *apiItem.Days[1].Uptime)
	}
	if u := apiItem.Uptime90; u == nil || *u != 90.0/130.0 {
		t.Errorf("uptime de 90 dias = %v, queria 90/130", u)
	}
	// Mediana ponderada pelos sucessos: (100×60 + 300×30) / 90 = 166,67,
	// arredondada (sum de bigint é numeric, e o ::int arredonda).
	if l := apiItem.LatencyMS; l == nil || *l != 167 {
		t.Errorf("latência = %v, queria 167", l)
	}
	if v.Items[0].Uptime90 != nil || v.Items[0].LatencyMS != nil {
		t.Errorf("item sem rollup tem uptime/latência: %+v", v.Items[0])
	}

	if len(v.Incidents) != 2 || v.Incidents[0].Name != "API" ||
		!v.Incidents[0].StartedAt.Equal(agora.Add(-2*time.Hour)) || v.Incidents[0].Duration() != 10*time.Minute ||
		!v.Incidents[1].StartedAt.Equal(agora.Add(-48*time.Hour)) {
		t.Errorf("incidentes = %+v; queria os 2 resolvidos da API nos 90 dias, o mais recente primeiro", v.Incidents)
	}
}

func TestPublicIncidentsAreCapped(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono, "api")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: alvo, DisplayName: "API"})
	for i := range statuspage.MaxIncidents + 5 {
		inicio := a.clk.Now().Add(-time.Duration(i+1) * time.Hour)
		incidenteResolvido(t, adm, alvo, inicio, inicio.Add(time.Minute))
	}
	v, err := a.svc.Public(context.Background(), "empresa")
	if err != nil || len(v.Incidents) != statuspage.MaxIncidents {
		t.Errorf("%d incidentes (%v), queria %d", len(v.Incidents), err, statuspage.MaxIncidents)
	}
}

// O alvo apagado some também da página pública, com o histórico de
// incidentes dele (D18 do Plano 3: apagar o alvo leva o histórico).
func TestDeletedTargetLeavesThePublicPage(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: api, DisplayName: "API"}, statuspage.Item{TargetID: site, DisplayName: "Site"})
	incidenteResolvido(t, adm, api, a.clk.Now().Add(-2*time.Hour), a.clk.Now().Add(-time.Hour))
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: dono, ID: api})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.svc.Public(ctx, "empresa")
	if err != nil || len(v.Items) != 1 || v.Items[0].Name != "Site" || len(v.Incidents) != 0 {
		t.Errorf("depois de apagar o alvo: %+v, %v; queria só o Site, sem incidentes", v, err)
	}
}

// A projeção fica 30s em cache: mil visitantes não viram mil agregações. A
// página que acabou de ser publicada aparece na hora (o 404 não é guardado).
func TestPublicViewIsCachedForThirtySeconds(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	p := a.publicar(t, dono, "empresa", false)
	if _, err := a.svc.Public(ctx, "empresa"); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("rascunho = %v", err)
	}
	sim := true
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Published: &sim}); err != nil {
		t.Fatal(err)
	}
	v, err := a.svc.Public(ctx, "empresa")
	if err != nil || v.Title != "Status" {
		t.Fatalf("recém-publicada = %+v, %v; queria visível na hora", v, err)
	}

	novo := "Título novo"
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Title: &novo}); err != nil {
		t.Fatal(err)
	}
	a.clk.Advance(29 * time.Second)
	if v, _ := a.svc.Public(ctx, "empresa"); v.Title != "Status" {
		t.Errorf("antes de 30s veio %q, queria a versão em cache", v.Title)
	}
	a.clk.Advance(time.Second)
	if v, _ := a.svc.Public(ctx, "empresa"); v.Title != "Título novo" {
		t.Errorf("depois de 30s veio %q, queria a versão nova", v.Title)
	}
}

// Spec §5.6: a projeção pública não tem onde guardar id, URL, código de
// status ou causa. O teste percorre os tipos; um campo novo com um desses
// tipos ou nomes quebra aqui antes de chegar ao template.
func TestPublicViewHasNoInternalFields(t *testing.T) {
	// Pedaços de nome em CamelCase: "ID" pega TargetID, e não Incidents.
	proibidos := []string{"ID", "URL", "Cause", "Code", "Header", "User", "Error", "Target", "Slug"}
	vistos := map[reflect.Type]bool{}
	var andar func(tp reflect.Type, caminho string)
	andar = func(tp reflect.Type, caminho string) {
		for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice || tp.Kind() == reflect.Array {
			tp = tp.Elem()
		}
		if tp.Kind() != reflect.Struct || vistos[tp] || tp == reflect.TypeOf(time.Time{}) {
			return
		}
		vistos[tp] = true
		if tp == reflect.TypeOf(uuid.UUID{}) {
			t.Errorf("%s é um uuid", caminho)
		}
		for i := range tp.NumField() {
			f := tp.Field(i)
			for _, p := range proibidos {
				if strings.Contains(f.Name, p) {
					t.Errorf("%s.%s: nome proibido na projeção pública", caminho, f.Name)
				}
			}
			if f.Type == reflect.TypeOf(uuid.UUID{}) {
				t.Errorf("%s.%s é um uuid", caminho, f.Name)
			}
			andar(f.Type, caminho+"."+f.Name)
		}
	}
	andar(reflect.TypeOf(statuspage.View{}), "View")
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/statuspage/`
Expected: FAIL de compilação: `a.svc.Public undefined`, `undefined: statuspage.Days`.

- [ ] **Step 3: Implementar**

Crie `internal/statuspage/public.go`:

```go
package statuspage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// A projeção pública (spec §10.2). Os tipos daqui são o que a página
// mostra, e nada além: nome de exibição, estado, uptime, latência agregada
// e incidentes com início e fim (spec §5.6). Nenhum carrega id, URL, status
// code ou causa, então nenhuma mudança no template os faz vazar.

// State é o estado atual de um item na página.
type State string

const (
	StateUp       State = "up"
	StateDegraded State = "degraded"
	StateDown     State = "down"
	// StateUnknown: sem check recente. Alvo desativado, sondador parado
	// ou alvo que nunca foi sondado; a página não afirma o que não sabe.
	StateUnknown State = "unknown"
)

const (
	// Days é a largura da barra de uptime (spec §10.2).
	Days = 90
	// MaxIncidents limita o histórico da página: os mais recentes.
	MaxIncidents = 20
	// staleIntervals: o último check mais velho que isto, em intervalos do
	// alvo, não diz mais nada sobre o presente. É o mesmo 3 da lacuna da
	// histerese (Plano 3).
	staleIntervals = 3
	latencyWindow  = 24 * time.Hour
	// cacheTTL é o mesmo max-age da resposta: a página é lida quando algo
	// quebrou, e mil visitantes de IPs diferentes não podem virar mil
	// agregações de 90 dias no banco.
	cacheTTL = 30 * time.Second
	// maxCached limita a memória: slugs são publicados por usuários, e o
	// cache não pode crescer sem teto.
	maxCached = 1000
	// publicQueryTimeout limita a projeção: com o banco lento, a página
	// responde 503 em vez de segurar a conexão do visitante.
	publicQueryTimeout = 5 * time.Second
)

// Day é um dia (UTC) da barra. Uptime é nil sem nenhum check no dia.
type Day struct {
	Date   time.Time
	Uptime *float64 // 0 a 1
}

type PublicItem struct {
	Name  string
	State State
	Days  [Days]Day // do mais antigo ao de hoje
	// Uptime90 é o agregado dos 90 dias; nil sem check nenhum.
	Uptime90 *float64
	// LatencyMS é a mediana aproximada das últimas 24h; nil sem sucesso.
	LatencyMS *int32
}

type PublicIncident struct {
	Name       string
	StartedAt  time.Time
	ResolvedAt time.Time
}

func (i PublicIncident) Duration() time.Duration { return i.ResolvedAt.Sub(i.StartedAt) }

type View struct {
	Title       string
	Items       []PublicItem
	Incidents   []PublicIncident // resolvidos, do mais recente ao mais antigo
	GeneratedAt time.Time
}

// Public devolve a projeção da página publicada do slug, ou ErrNotFound. A
// página inexistente e o rascunho são o mesmo ErrNotFound: o público não
// distingue os dois. Os dados ficam em cache por cacheTTL.
func (s *Service) Public(ctx context.Context, slug string) (View, error) {
	if !slugRE.MatchString(slug) {
		return View{}, ErrNotFound // nem vai ao banco
	}
	agora := s.clk.Now()
	if v, ok := s.cache.get(slug, agora); ok {
		return v, nil
	}
	v, err, _ := s.voo.Do(slug, func() (any, error) {
		// A consulta é de todos os que esperam neste voo, e não só de quem
		// chegou primeiro: o visitante que fecha a aba não derruba os outros.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicQueryTimeout)
		defer cancel()
		v, err := s.projetar(ctx, slug, agora)
		if err == nil {
			s.cache.put(slug, v, agora)
		}
		return v, err
	})
	if err != nil {
		return View{}, err
	}
	return v.(View), nil
}

func (s *Service) projetar(ctx context.Context, slug string, agora time.Time) (View, error) {
	hoje := agora.UTC().Truncate(24 * time.Hour)
	desde := hoje.AddDate(0, 0, -(Days - 1))
	v := View{GeneratedAt: agora}
	err := storage.InPublicTx(ctx, s.pool, slug, func(q *db.Queries) error {
		pagina, err := q.GetPublicPage(ctx, slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar página pública: %w", err)
		}
		v.Title = pagina.Title

		itens, err := q.ListPublicItems(ctx, pagina.ID)
		if err != nil {
			return fmt.Errorf("listar itens públicos: %w", err)
		}
		dias, err := q.ListPublicDailyUptime(ctx, db.ListPublicDailyUptimeParams{PageID: pagina.ID, Since: desde})
		if err != nil {
			return fmt.Errorf("listar uptime diário: %w", err)
		}
		latencias, err := q.ListPublicLatency(ctx, db.ListPublicLatencyParams{PageID: pagina.ID, Since: agora.Add(-latencyWindow)})
		if err != nil {
			return fmt.Errorf("listar latência: %w", err)
		}
		incidentes, err := q.ListPublicIncidents(ctx, db.ListPublicIncidentsParams{
			PageID: pagina.ID, Since: desde, MaxRows: MaxIncidents,
		})
		if err != nil {
			return fmt.Errorf("listar incidentes públicos: %w", err)
		}

		porAlvo := map[uuid.UUID][]db.ListPublicDailyUptimeRow{}
		for _, d := range dias {
			porAlvo[d.TargetID] = append(porAlvo[d.TargetID], d)
		}
		latencia := map[uuid.UUID]int32{}
		for _, l := range latencias {
			latencia[l.TargetID] = l.P50
		}
		v.Items = make([]PublicItem, 0, len(itens))
		for _, it := range itens {
			pi := PublicItem{Name: it.DisplayName, State: estado(it, agora)}
			preencherDias(&pi, desde, porAlvo[it.TargetID])
			if ms, ok := latencia[it.TargetID]; ok {
				pi.LatencyMS = &ms
			}
			v.Items = append(v.Items, pi)
		}
		v.Incidents = make([]PublicIncident, 0, len(incidentes))
		for _, x := range incidentes {
			v.Incidents = append(v.Incidents, PublicIncident{Name: x.DisplayName, StartedAt: x.StartedAt.UTC(), ResolvedAt: x.ResolvedAt.UTC()})
		}
		return nil
	})
	return v, err
}

// estado é o do último check, se ele ainda é recente.
func estado(it db.ListPublicItemsRow, agora time.Time) State {
	recente := agora.Sub(it.LastCheckedAt) <= staleIntervals*time.Duration(it.IntervalSec)*time.Second
	if !it.Enabled || it.LastStatus == "" || !recente {
		return StateUnknown
	}
	switch State(it.LastStatus) {
	case StateUp, StateDegraded, StateDown:
		return State(it.LastStatus)
	}
	return StateUnknown
}

func preencherDias(pi *PublicItem, desde time.Time, linhas []db.ListPublicDailyUptimeRow) {
	for i := range pi.Days {
		pi.Days[i].Date = desde.AddDate(0, 0, i)
	}
	var total, ok int64
	for _, l := range linhas {
		i := int(l.Day.Sub(desde) / (24 * time.Hour))
		if i < 0 || i >= Days || l.Total == 0 {
			continue
		}
		u := float64(l.Ok) / float64(l.Total)
		pi.Days[i].Uptime = &u
		total += l.Total
		ok += l.Ok
	}
	if total > 0 {
		u := float64(ok) / float64(total)
		pi.Uptime90 = &u
	}
}

// cachePublico guarda a projeção por slug. Só guarda sucesso: a página que
// acabou de ser publicada aparece na hora, e um erro de banco não fica
// preso por 30 segundos.
type cachePublico struct {
	mu       sync.Mutex
	entradas map[string]entradaCache
}

type entradaCache struct {
	v    View
	vale time.Time
}

func (c *cachePublico) get(slug string, agora time.Time) (View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entradas[slug]
	if !ok || !agora.Before(e.vale) {
		return View{}, false
	}
	return e.v, true
}

func (c *cachePublico) put(slug string, v View, agora time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entradas == nil {
		c.entradas = map[string]entradaCache{}
	}
	if len(c.entradas) >= maxCached {
		for k, e := range c.entradas {
			if !agora.Before(e.vale) {
				delete(c.entradas, k)
			}
		}
		if len(c.entradas) >= maxCached {
			return // cheio de entradas válidas: esta fica sem cache
		}
	}
	c.entradas[slug] = entradaCache{v: v, vale: agora.Add(cacheTTL)}
}

type singleflightGroup = singleflight.Group
```

Aplique em `internal/statuspage/service.go`:

```diff
--- a/internal/statuspage/service.go
+++ b/internal/statuspage/service.go
@@ -26,6 +26,10 @@
 type Service struct {
 	pool *pgxpool.Pool
 	clk  clock.Clock
+	// A página pública (public.go): cache da projeção por slug, e voo para
+	// que N visitantes com o cache vencido façam uma consulta só.
+	cache cachePublico
+	voo   singleflightGroup
 }
 
 func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/statuspage/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run TestPublic ./internal/statuspage/` (com `sqlc generate` para restaurar quando mexer no gerado); restaure:
1. Em `estado`, tire `!it.Enabled ||`. Expected: FAIL em `TestPublicProjection` (item "Desligado").
2. Em `estado`, troque `!recente` por `!(recente || true)`. Expected: FAIL (item "Velho").
3. Troque `<= staleIntervals` por `< staleIntervals`. Expected: FAIL (o item "Site", com exatamente 3 intervalos).
4. Em `Public`, ignore o cache (`&& false` no `get`). Expected: FAIL em `TestPublicViewIsCachedForThirtySeconds`.
5. Em `cachePublico.get`, tire a validade (`if !ok {`). Expected: FAIL no mesmo teste.
6. Guarde também o erro no cache (`if true {` no lugar de `if err == nil {`). Expected: FAIL (a página recém-publicada fica presa no 404).
7. Troque `-(Days - 1)` por `-Days`. Expected: FAIL (a barra desalinha).
8. No SQL gerado de `ListPublicIncidents`, tire `x.resolved_at IS NOT NULL AND`. Expected: FAIL (o incidente aberto entra).
9. No SQL gerado de `ListPublicIncidents`, tire `AND i.status_page_id = $1` do `JOIN`. Expected: FAIL.

- [ ] **Step 6: Commit**

```bash
git add internal/statuspage/
git commit -m "feat(statuspage): projeção pública com estado, 90 dias, latência e incidentes, em cache de 30s"
```

---

### Task 9: A página `/p/{slug}`

HTML renderizado no servidor, sem JavaScript, numa cadeia HTTP separada.

**Files:**
- Create: `internal/httpapi/templates/status.html`
- Create: `internal/httpapi/publicpage.go`
- Modify: `internal/httpapi/routes.go` (monta `/p/`)
- Test: `internal/httpapi/publicpage_test.go` (pacote interno), `internal/httpapi/publicpage_integration_test.go`

**Interfaces:**
- Consumes: `statuspage.Service.Public` (Task 8); `pagina()` e as constantes de `verifyhandlers.go` (Plano 3); `paginaJSON` (Task 7).
- Produces: `GET /p/{slug}` → `200 text/html` com `Cache-Control: public, max-age=30` e CSP `default-src 'none'; style-src 'nonce-…'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`; `404` idêntico e `no-store`; `503` em HTML.

**Contexto para quem implementa.** O template recebe um modelo **só de strings prontas** (`paginaModelo`): ele não formata, não decide, e não tem acesso a nada da `View` que não tenha sido copiado de propósito. O `html/template` escapa tudo; o atributo `class` fica entre aspas porque, sem elas, os dois ramos de `{{if}}` terminam em contextos diferentes e o template não compila. A barra é codificada pela maioria (D20): cada dia sem atributo custa 4 bytes, com atributo, 14. Os dias saem em `<li>` sem fechamento, que é opcional em HTML. O nonce é base64 **de URL** (D26).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/httpapi/publicpage_test.go`:

```go
package httpapi

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/statuspage"
)

// paginaDeTeste monta a projeção de cinco serviços com nomes longos e o
// histórico cheio; dia(i) decide o uptime do dia i de cada barra.
func paginaDeTeste(dia func(i int) *float64) statuspage.View {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	u := 0.99912
	lat := int32(143)
	v := statuspage.View{Title: "Status da Empresa Exemplo de Serviços Online", GeneratedAt: agora}
	for i := range 5 {
		it := statuspage.PublicItem{Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i+1),
			State: statuspage.StateDegraded, Uptime90: &u, LatencyMS: &lat}
		for d := range it.Days {
			it.Days[d] = statuspage.Day{Date: agora.AddDate(0, 0, d-statuspage.Days+1), Uptime: dia(d)}
		}
		v.Items = append(v.Items, it)
	}
	for i := range statuspage.MaxIncidents {
		inicio := agora.Add(-time.Duration(i+1) * 26 * time.Hour)
		v.Incidents = append(v.Incidents, statuspage.PublicIncident{
			Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i%5+1), StartedAt: inicio, ResolvedAt: inicio.Add(95 * time.Minute),
		})
	}
	return v
}

func renderizar(t *testing.T, v statuspage.View) string {
	t.Helper()
	var buf bytes.Buffer
	if err := statusTemplate.Execute(&buf, apresentar(v, "AAAAAAAAAAAAAAAAAAAAAA==")); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Spec §10.1: resposta abaixo de 10 KB, com cinco serviços. O pior caso
// tem os 90 dias espalhados por igual nas quatro classes, e a codificação
// pela maioria quase não ajuda; o caso comum (barra de uma cor só) fica bem
// abaixo. Com os 20 serviços que a página aceita, passa dos 10 KB: limite
// conhecido, registrado no plano.
func TestPublicPageFitsInTenKilobytes(t *testing.T) {
	valores := []*float64{nil, ptr(1), ptr(0.995), ptr(0.5)} // n, verde, d, f
	pior := renderizar(t, paginaDeTeste(func(i int) *float64 { return valores[i%4] }))
	if n := len(pior); n >= 10*1024 {
		t.Errorf("pior caso: %d bytes, queria menos de 10 KB", n)
	}
	if c := strings.Count(pior, "<li"); c != 5*statuspage.Days+5+statuspage.MaxIncidents {
		t.Errorf("%d <li>, queria %d (dias, serviços e incidentes)", c, 5*statuspage.Days+5+statuspage.MaxIncidents)
	}
	comum := renderizar(t, paginaDeTeste(func(int) *float64 { return ptr(1) }))
	if n := len(comum); n >= 7*1024 {
		t.Errorf("caso comum: %d bytes, queria menos de 7 KB", n)
	}
	t.Logf("pior caso %d bytes; caso comum %d bytes", len(pior), len(comum))
}

func ptr(v float64) *float64 { return &v }

// A barra sai pela maioria e reconstrói cada dia: a classe efetiva de um
// dia é a dele, ou a da <ol> quando ele não tem.
func TestBarEncodesByMajority(t *testing.T) {
	var dias [statuspage.Days]statuspage.Day
	for i := range dias {
		dias[i].Uptime = ptr(0.995) // amarelo
	}
	dias[0].Uptime, dias[1].Uptime, dias[2].Uptime = nil, ptr(1), ptr(0.1)
	maioria, classes := barra(dias)
	if maioria != "d" || classes[0] != "n" || classes[1] != "g" || classes[2] != "f" || classes[3] != "" {
		t.Errorf("barra = %q, %q", maioria, classes[:4])
	}
	var verdes [statuspage.Days]statuspage.Day
	for i := range verdes {
		verdes[i].Uptime = ptr(1)
	}
	if maioria, classes := barra(verdes); maioria != "" || classes != ([statuspage.Days]string{}) {
		t.Errorf("barra toda verde = %q, %v; queria sem classe nenhuma", maioria, classes)
	}
}

func TestPresentationRules(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		u    *float64
		quer string
	}{{nil, "n"}, {f(1), ""}, {f(0.999), ""}, {f(0.9989), "d"}, {f(0.99), "d"}, {f(0.9899), "f"}, {f(0), "f"}} {
		if got := classeDia(c.u); got != c.quer {
			t.Errorf("classeDia(%v) = %q, queria %q", c.u, got, c.quer)
		}
	}
	for u, quer := range map[float64]string{1: "100,00%", 0.99999: "99,99%", 0.5: "50,00%", 0: "0,00%"} {
		if got := porcento(u); got != quer {
			t.Errorf("porcento(%v) = %q, queria %q", u, got, quer)
		}
	}
	for d, quer := range map[time.Duration]string{
		20 * time.Second: "menos de 1 min", 7 * time.Minute: "7 min",
		65 * time.Minute: "1 h 5 min", 50 * time.Hour: "2 d 2 h",
	} {
		if got := duracao(d); got != quer {
			t.Errorf("duracao(%v) = %q, queria %q", d, got, quer)
		}
	}
	for _, c := range []struct {
		estados []statuspage.State
		quer    string
	}{
		{nil, "Nenhum serviço nesta página."},
		{[]statuspage.State{"up", "up"}, "Todos os serviços estão no ar."},
		{[]statuspage.State{"up", "unknown"}, "Há serviços sem dados recentes."},
		{[]statuspage.State{"degraded", "unknown"}, "Há serviços lentos."},
		{[]statuspage.State{"degraded", "down"}, "Há serviços fora do ar."},
	} {
		var items []statuspage.PublicItem
		for _, e := range c.estados {
			items = append(items, statuspage.PublicItem{State: e})
		}
		if got := resumo(items); got != c.quer {
			t.Errorf("resumo(%v) = %q, queria %q", c.estados, got, c.quer)
		}
	}
}

// O nonce vai para o atributo do <style> e para o cabeçalho da CSP, e os
// dois têm de ser o mesmo texto. Um caractere que o html/template escapa
// ("+" vira "&#43;") quebraria isso só às vezes, conforme o sorteio: por
// isso o teste sorteia mil.
func TestNonceNeedsNoEscaping(t *testing.T) {
	seguro := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	for range 1000 {
		n, err := novoNonce()
		if err != nil || !seguro.MatchString(n) {
			t.Fatalf("nonce %q (%v): queria 22 caracteres de base64 de URL", n, err)
		}
	}
}
```

Crie `internal/httpapi/publicpage_integration_test.go`:

```go
package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

func getPublico(t *testing.T, a *api, caminho string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(a.srv.URL + caminho)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

var nonceNoStyle = regexp.MustCompile(`<style nonce="([^"]+)">`)

// Spec §5.6 e §10: a página pública sai sem autenticação, só com o que a
// tabela da §5.6 permite, e com a CSP estrita autorizando o CSS pelo nonce.
func TestPublicPageRendersWithoutLeaking(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	alvoSecreto := map[string]any{
		"name": "db-primario-interno", "url": "https://interno.exemplo.com/saude-secreta", "interval_sec": 60,
		"headers": map[string]string{"Authorization": "Bearer token-do-cliente"},
	}
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoSecreto), &alvo)
	kind := "conn_refused"
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		code := int32(503)
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: uuid.MustParse(alvo.ID), CheckedAt: a.clk.Now().Add(-time.Minute),
			Status: "down", LatencyMS: 12, StatusCode: &code, ErrorKind: &kind,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var pagina paginaJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "Status <da> Empresa", "published": true,
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "Banco <script>alert(1)</script>"}},
	})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}

	resp, corpo := getPublico(t, a, "/p/empresa")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /p/empresa deu %d %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), corpo)
	}
	for _, quer := range []string{"Fora do ar", "Há serviços fora do ar.", "Banco &lt;script&gt;", "Status &lt;da&gt; Empresa"} {
		if !strings.Contains(corpo, quer) {
			t.Errorf("a página não tem %q:\n%s", quer, corpo)
		}
	}
	for _, vazou := range []string{
		"interno.exemplo.com", "saude-secreta", "db-primario-interno", "token-do-cliente", "Authorization",
		alvo.ID, pagina.ID, a.userID.String(), "503", "conn_refused", "<script>",
	} {
		if strings.Contains(corpo, vazou) {
			t.Errorf("a página pública contém %q", vazou)
		}
	}

	csp := resp.Header.Get("Content-Security-Policy")
	m := nonceNoStyle.FindStringSubmatch(corpo)
	if m == nil || !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") || strings.Contains(csp, "unsafe-inline") ||
		!strings.Contains(csp, "default-src 'none'") || strings.Contains(corpo, "<script") {
		t.Errorf("CSP %q não autoriza só o <style> da página (nonce %v)", csp, m)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=30" {
		t.Errorf("Cache-Control = %q, queria public, max-age=30 (spec §10.1)", cc)
	}

	// O nonce é por resposta.
	_, outro := getPublico(t, a, "/p/empresa")
	if m2 := nonceNoStyle.FindStringSubmatch(outro); m2 == nil || m2[1] == m[1] {
		t.Errorf("o nonce se repetiu entre respostas: %v", m2)
	}
}

// Rascunho e inexistente dão a mesma resposta, byte a byte: o público não
// descobre quais slugs existem. E o 404 não fica em cache.
func TestPublicPage404IsTheSameForDraftAndMissing(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "rascunho", "title": "T"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}

	r1, rascunho := getPublico(t, a, "/p/rascunho")
	r2, inexistente := getPublico(t, a, "/p/nao-existe")
	r3, malformado := getPublico(t, a, "/p/NAO_EXISTE")
	for _, r := range []*http.Response{r1, r2, r3} {
		if r.StatusCode != http.StatusNotFound || r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("404 deu %d com Cache-Control %q", r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	if rascunho != inexistente || inexistente != malformado {
		t.Errorf("os 404 diferem:\n%s\n%s\n%s", rascunho, inexistente, malformado)
	}
}

// A página é só GET. E ela é pública de verdade: não pede token nem o
// cabeçalho anti-CSRF da API.
func TestPublicPageIsGetOnly(t *testing.T) {
	a := newAPI(t)
	resp, err := http.Post(a.srv.URL+"/p/empresa", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /p/empresa deu %d, queria 405", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/httpapi/`
Expected: FAIL de compilação: `undefined: statusTemplate`, `undefined: apresentar`, `undefined: barra`.

- [ ] **Step 3: Implementar**

Crie `internal/httpapi/templates/status.html` (uma linha de HTML; o CSS é minificado de propósito, porque cada byte conta nos 10 KB):

```html
<!doctype html><html lang=pt-BR><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style nonce="{{.Nonce}}">
body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:0 auto;padding:1rem;color:#1b1b1b;background:#fff}h1{font-size:1.5rem;margin:.5rem 0}h2{font-size:1.1rem;margin-top:2rem}ul{list-style:none;padding:0}.s>li{border:1px solid #d6d6d6;border-radius:.5rem;padding:.75rem;margin:.5rem 0}.h{display:flex;justify-content:space-between;gap:1rem}.h span{white-space:nowrap}ol{display:flex;gap:1px;list-style:none;padding:0;margin:.5rem 0;height:1.75rem}ol li{flex:1;background:#2e9d5b;border-radius:1px}.d li{background:#e0a100}.f li{background:#d23c3c}.n li{background:#c9c9c9}li.g{background:#2e9d5b}li.d{background:#e0a100}li.f{background:#d23c3c}li.n{background:#c9c9c9}.m,.i,footer{color:#555;font-size:.875rem}.up{color:#1f7a45}.degraded{color:#8a5f00}.down{color:#b3261e}.unknown{color:#555}@media(prefers-color-scheme:dark){body{background:#121212;color:#eee}.s>li{border-color:#3a3a3a}.m,.i,footer,.unknown{color:#aaa}.up{color:#5cc98a}.degraded{color:#f0c050}.down{color:#ff8a80}.n li,li.n{background:#444}}
</style><h1>{{.Title}}</h1><p>{{.Summary}}</p><ul class=s>{{range .Items}}<li><div class=h><strong>{{.Name}}</strong><span class={{.State}}>{{.Label}}</span></div><ol{{if .Bar}} class="{{.Bar}}"{{end}} aria-hidden=true>{{range .Days}}<li{{if .}} class="{{.}}"{{end}}>{{end}}</ol><p class=m>{{.Uptime}}{{with .Latency}} · {{.}}{{end}}</p>{{else}}<li>Nenhum serviço nesta página.{{end}}</ul><h2>Incidentes resolvidos (90 dias)</h2>{{if .Incidents}}<ul class=i>{{range .Incidents}}<li>{{.}}{{end}}</ul>{{else}}<p class=i>Nenhum incidente nos últimos 90 dias.{{end}}<footer>Atualizado em {{.Generated}} (UTC) · Vigilume</footer></html>
```

Crie `internal/httpapi/publicpage.go`:

```go
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/statuspage"
)

// A status page pública (spec §10): HTML renderizado aqui, sem JavaScript,
// com o CSS inline autorizado por nonce. É a página que se abre quando algo
// quebrou; ela não pode depender de bundle, hidratação nem API.

//go:embed templates/status.html
var templatesFS embed.FS

var statusTemplate = template.Must(template.ParseFS(templatesFS, "templates/status.html"))

const (
	// A resposta é pública e a mesma para todos: 30s no cache do navegador
	// e de qualquer proxy (spec §10.1), igual ao cache da projeção.
	cachePublico = "public, max-age=30"
	// O limite por IP é folgado: o visitante de uma página em incidente
	// recarrega. Com TRUSTED_PROXIES vazio atrás do proxy do Fly, todos os
	// visitantes dividem um balde, e a página cai no pior momento (ver
	// PENDENCIAS-PARA-O-PLANO-5).
	publicPorMinuto = 120
	publicBurst     = 60
)

// paginaPublica é a cadeia de /p/{slug}, separada da API como a de
// /verify/: sem CORS, sem RequireCustomHeader, e com a CSP e o cache
// próprios desta página.
func paginaPublica(ctx context.Context, cfg *config.Config, svc *statuspage.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{slug}", (&PublicPageHandlers{svc: svc}).Page)

	var h http.Handler = mux
	h = middleware.RateLimit(ctx, publicPorMinuto, publicBurst, cfg.TrustedProxies)(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.RequestID(h)
	return h
}

type PublicPageHandlers struct {
	svc *statuspage.Service
}

func (h *PublicPageHandlers) Page(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Public(r.Context(), r.PathValue("slug"))
	switch {
	case errors.Is(err, statuspage.ErrNotFound):
		// Inexistente e rascunho são a mesma resposta, byte a byte.
		pagina(w, http.StatusNotFound, paginaNaoAchada)
		return
	case err != nil:
		slog.Error("status page pública", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}

	nonce, err := novoNonce()
	if err != nil {
		slog.Error("nonce da status page", "erro", err)
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}
	var buf bytes.Buffer
	if err := statusTemplate.Execute(&buf, apresentar(v, nonce)); err != nil {
		slog.Error("renderizar status page", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; "+
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	hd.Set("Cache-Control", cachePublico)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, &buf)
}

// As páginas de erro são texto fixo e saem com a CSP de /verify/ (sem
// estilo) e o no-store de SecurityHeaders: uma página recém-publicada não
// pode ficar presa num 404 em cache.
const (
	paginaNaoAchada = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Página não encontrada</title>` +
		`<h1>Página não encontrada</h1><p>Confira o endereço com quem o enviou.</p></html>`
	paginaIndisponivel = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Indisponível</title>` +
		`<h1>Status indisponível</h1><p>Tente de novo em instantes.</p></html>`
)

// novoNonce usa base64 de URL, sem padding: a gramática de nonce da CSP
// aceita "-" e "_", e o html/template não escapa nenhum dos dois. Com a
// base64 padrão, o "+" sairia como "&#43;" no atributo, e o HTML deixaria de
// trazer o nonce literal da CSP (defeito achado na execução: o teste falhava
// sempre que o nonce sorteado tinha um "+").
func novoNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// O modelo de apresentação: só strings prontas. O template não formata,
// não decide e não tem acesso a nada além disto.
type paginaModelo struct {
	Nonce, Title, Summary, Generated string
	Items                            []itemModelo
	Incidents                        []string
}

type itemModelo struct {
	Name, State, Label, Uptime, Latency string
	// Bar é a classe da maioria dos dias, na <ol>; Days tem a classe só dos
	// dias diferentes dela, e "" nos outros. Um dia sem atributo custa 4
	// bytes, e com atributo, 14: codificar pela maioria é o que faz cinco
	// barras de 90 dias caberem nos 10 KB (spec §10.1). "" em Bar é verde.
	Bar  string
	Days [statuspage.Days]string
}

func apresentar(v statuspage.View, nonce string) paginaModelo {
	m := paginaModelo{
		Nonce: nonce, Title: v.Title, Summary: resumo(v.Items),
		Generated: v.GeneratedAt.UTC().Format("02/01/2006 15:04"),
		Items:     make([]itemModelo, 0, len(v.Items)),
		Incidents: make([]string, 0, len(v.Incidents)),
	}
	for _, it := range v.Items {
		im := itemModelo{Name: it.Name, State: string(it.State), Label: rotulo(it.State), Uptime: "Sem dados nos últimos 90 dias"}
		if it.Uptime90 != nil {
			im.Uptime = porcento(*it.Uptime90) + " no ar em 90 dias"
		}
		if it.LatencyMS != nil {
			im.Latency = fmt.Sprintf("latência típica %d ms", *it.LatencyMS)
		}
		im.Bar, im.Days = barra(it.Days)
		m.Items = append(m.Items, im)
	}
	for _, x := range v.Incidents {
		m.Incidents = append(m.Incidents, fmt.Sprintf("%s: %s a %s (%s)", x.Name,
			x.StartedAt.Format("02/01/2006 15:04"), x.ResolvedAt.Format("02/01 15:04"), duracao(x.Duration())))
	}
	return m
}

func resumo(items []statuspage.PublicItem) string {
	if len(items) == 0 {
		return "Nenhum serviço nesta página."
	}
	conta := map[statuspage.State]int{}
	for _, it := range items {
		conta[it.State]++
	}
	switch {
	case conta[statuspage.StateDown] > 0:
		return "Há serviços fora do ar."
	case conta[statuspage.StateDegraded] > 0:
		return "Há serviços lentos."
	case conta[statuspage.StateUnknown] > 0:
		return "Há serviços sem dados recentes."
	}
	return "Todos os serviços estão no ar."
}

func rotulo(s statuspage.State) string {
	switch s {
	case statuspage.StateUp:
		return "No ar"
	case statuspage.StateDegraded:
		return "Lento"
	case statuspage.StateDown:
		return "Fora do ar"
	}
	return "Sem dados"
}

// classeDia: verde (sem classe) a partir de 99,9%, amarelo a partir de 99%,
// vermelho abaixo, cinza sem check. Com checks de minuto em minuto, 99,9%
// de um dia é 1,4 check: uma sonda perdida não pinta o dia.
func classeDia(u *float64) string {
	switch {
	case u == nil:
		return "n"
	case *u >= 0.999:
		return ""
	case *u >= 0.99:
		return "d"
	}
	return "f"
}

// barra codifica os dias pela classe mais comum (ver itemModelo). Com a
// maioria verde, a <ol> fica sem classe e os verdes, sem atributo; com
// outra maioria, um dia verde precisa de "g" explícito.
func barra(dias [statuspage.Days]statuspage.Day) (string, [statuspage.Days]string) {
	var classes [statuspage.Days]string
	conta := map[string]int{}
	for i, d := range dias {
		classes[i] = classeDia(d.Uptime)
		conta[classes[i]]++
	}
	maioria := ""
	for _, c := range []string{"n", "d", "f"} { // empate: verde fica
		if conta[c] > conta[maioria] {
			maioria = c
		}
	}
	var out [statuspage.Days]string
	for i, c := range classes {
		switch {
		case c == maioria:
		case c == "":
			out[i] = "g"
		default:
			out[i] = c
		}
	}
	return maioria, out
}

// porcento trunca em duas casas: 99,999% não pode virar "100,00%" num dia
// com falha.
func porcento(u float64) string {
	v := math.Floor(u*10000) / 100
	return strings.Replace(fmt.Sprintf("%.2f%%", v), ".", ",", 1)
}

func duracao(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "menos de 1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d d %d h", int(d.Hours())/24, int(d.Hours())%24)
}
```

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -163,6 +163,7 @@
 
 	raiz := http.NewServeMux()
 	raiz.Handle("/verify/", verificacao(ctx, cfg, d.Channels))
+	raiz.Handle("/p/", paginaPublica(ctx, cfg, d.StatusPages))
 	raiz.Handle("/", handler)
 	return raiz
 }
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 -v -run 'TestPublicPage|TestPresentation|TestBar' ./internal/httpapi/ | grep -E '^(---|ok|FAIL)|bytes' && go test -race -count=1 ./internal/httpapi/`
Expected: PASS, com o log `pior caso ~9300 bytes; caso comum ~6000 bytes`.

- [ ] **Step 5: Olhar a página**

Grave o HTML de `paginaDeTeste` num arquivo (um teste temporário que chama `renderizar` e escreve o resultado) e abra num navegador, claro e escuro, e numa largura de 390 px. Confira: o rótulo de estado não quebra linha no celular (`.h span{white-space:nowrap}`), as quatro cores da barra se distinguem nos dois temas e o texto ao lado da barra diz o uptime (a barra tem `aria-hidden`). Apague o teste temporário.

- [ ] **Step 6: Sabotagem**

Uma de cada vez; rode `go test -count=1 -run 'TestPublicPage|TestBar|TestPresentation' ./internal/httpapi/`; restaure:
1. Tire `hd.Set("Cache-Control", cachePublico)`. Expected: FAIL em `TestPublicPageRendersWithoutLeaking` (`no-store`).
2. Troque o `style-src 'nonce-…'` por `style-src 'unsafe-inline'`. Expected: FAIL no mesmo teste.
3. Passe um nonce fixo para `apresentar`. Expected: FAIL (o nonce se repete entre respostas).
4. Em `barra`, não marque o dia verde com `"g"` quando a maioria é outra. Expected: FAIL em `TestBarEncodesByMajority`.
5. Em `novoNonce`, troque `base64.RawURLEncoding` por `base64.StdEncoding`. Rode `-run TestNonce`. Expected: FAIL (o `=` do padding, e em ~29% dos sorteios um `+`, que o template escaparia como `&#43;`).

A cadeia `/p/` separada da API não tem teste próprio: dentro da cadeia da API, um `GET` se comportaria igual (o handler sobrescreve CSP e cache, e `RequireCustomHeader` só olha mutações). A separação dá o balde próprio de rate limit e deixa a CSP da API intocada; é organização, não uma propriedade observável.

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): status page pública em /p/{slug}, HTML sem JavaScript abaixo de 10 KB"
```

---

### Task 10: Ponta a ponta e documentação

**Files:**
- Create: `cmd/vigilume/statuspage_e2e_test.go`
- Create: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`
- Modify: `docs/superpowers/plans/2026-09-22-roadmap.md`

**Interfaces:**
- Consumes: tudo o que veio antes, pelo processo real (`run`).

**Contexto para quem implementa.** O teste ponta a ponta sobe o processo no papel `all`, com checks gravados numa hora já fechada, e espera `/p/{slug}` mostrar "100,00% no ar em 90 dias". Ele **passa de primeira**: o wiring já veio nas Tasks 2, 7 e 9. O papel dele é trava de regressão do wiring, e a prova é a sabotagem do Step 3, não um vermelho antes da implementação.

- [ ] **Step 1: Escrever o teste**

Crie `cmd/vigilume/statuspage_e2e_test.go`:

```go
package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// O caminho do Plano 4 no processo real, papel all: os checks de uma hora
// já fechada viram rollup na manutenção que roda ao subir (sem esperar os
// 15 minutos do ticker), e /p/{slug} mostra o uptime deles sem
// autenticação. Prova o wiring de RunMaintenance, de StatusPages no Deps e
// da cadeia /p/.
//
// O alvo está desativado: o sondador não o toca, e o estado atual sai como
// "Sem dados", mas o uptime dos 90 dias vem dos rollups.
func TestAllRoleConsolidatesAndServesThePublicPage(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono, alvo, pagina := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'p4@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	agora := time.Now()
	hora := agora.UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "nome-interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: false, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: pagina, UserID: dono, Slug: "empresa-e2e", Title: "Status E2E", Published: true, CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: alvo, DisplayName: "API pública", Position: 0,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureChecksPartitions(ctx, pool, hora); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: hora.Add(time.Duration(i) * time.Minute), Status: "up", LatencyMS: 80}
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	addr := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleAll, DatabaseURL: testdb.DSN(t), HTTPAddr: addr, MetricsAddr: portaLivre(t),
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave, ShutdownGrace: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	var corpo string
	prazo := time.Now().Add(15 * time.Second)
	for !strings.Contains(corpo, "100,00% no ar em 90 dias") {
		if time.Now().After(prazo) {
			t.Fatalf("a página não mostrou o uptime consolidado em 15s:\n%s", corpo)
		}
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get("http://" + addr + "/p/empresa-e2e")
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		corpo = string(b)
	}
	for _, quer := range []string{"Status E2E", "API pública", "Sem dados", "latência típica 80 ms"} {
		if !strings.Contains(corpo, quer) {
			t.Errorf("a página não tem %q", quer)
		}
	}
	if strings.Contains(corpo, "nome-interno") || strings.Contains(corpo, "127.0.0.1") {
		t.Error("a página pública vazou o nome interno ou a URL do alvo")
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
```

- [ ] **Step 2: Rodar**

Run: `go test -race -count=1 -run TestAllRole ./cmd/...`
Expected: PASS.

- [ ] **Step 3: Sabotagem**

Em `main.go`, troque `return storage.RunMaintenance(gctx, pool, clk, manutencaoACada)` por `<-gctx.Done(); return nil`. Rode o teste. Expected: FAIL em 15 s: "a página não mostrou o uptime consolidado". Restaure.

- [ ] **Step 4: Documentação**

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`:

```markdown
# Pendências que atravessam o Plano 4

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` no Fly.io.** Herdada do Plano 1, ainda aberta, e agora mais cara: a
   status page pública tem limite por IP (120/min, burst 60). Sem `TRUSTED_PROXIES`, todos os
   visitantes dividem **um** balde, e a página começa a responder 429 justamente durante um
   incidente, quando mais gente a abre.
2. **Papel comum do Postgres no Fly.io**, dono das tabelas, sem superusuário nem `BYPASSRLS`
   (D8 do Plano 2). A migration 00006 cria quatro tabelas, três funções e uma política em
   `targets`: o papel precisa de `CREATE` no schema `public`.
3. **`METRICS_ADDR` no Fly.io.** Decidir no Plano 5 entre `off` e um bind na interface privada.
4. **E-mail.** `RESEND_API_KEY` e `EMAIL_FROM`, as duas ou nenhuma (ver o Plano 3).
5. **`PUBLIC_BASE_URL` é o host da API.** Agora vale também para a status page: o
   `public_url` de `GET /status-pages` é `PUBLIC_BASE_URL/p/{slug}`, servido pela API.
6. **Primeira manutenção depois do deploy.** Sem rollup nenhum, a primeira rodada consolida
   os últimos 31 dias de checks de uma vez (uma consulta só). Com muitos alvos, é a rodada
   mais pesada que o job faz; as seguintes voltam só 2 horas.

## Resolvidas no Plano 4

| Origem | Como fechou |
|---|---|
| D6 do Plano 2 | `GET /targets/{id}/checks` acima de 48h lê `check_rollup_1h`; a resposta diz `resolution` |
| Retenção (spec §12) | `RunMaintenance` a cada 15 min: partições, rollups, `DROP` de partições vencidas, `DELETE` de rollups (13 meses), auditoria (12 meses), sessões (7 dias após vencer) e notificações encerradas (30 dias) |
| `MaintainChecksPartitions` sem teste, panic com `every <= 0` | Substituído por `RunMaintenance`, que recusa `every <= 0` |
| Canal que quebra depois de verificado | `channel_health`; `GET /channels` mostra `failing`, `last_error` e `last_failure_at` |
| Apagão em massa (429 esgotando as tentativas) | 429 devolve a tentativa por até 1h; o mesmo canal recebe em série e espera depois de um 429 |
| Abertura e fechamento no mesmo lote chegando fora de ordem | Mesmo canal em série, na ordem de enfileiramento |
| Status page (spec §10) | Cadastro do dono e `/p/{slug}` em HTML, sem JavaScript |
| Incidentes de alvos que saíram da página | Decisão: só os alvos que estão na página agora |

## Limites conhecidos do Plano 4

- **10 KB vale para cinco serviços.** O pior caso com cinco (dias espalhados pelas quatro
  cores) tem ~9,3 KB. A página aceita até 20 serviços, e aí passa dos 10 KB. É HTML sem
  compressão; o limite da spec é de transferência, e gzip resolveria se virar problema.
- **O rollup atrasa até ~1h15.** A hora em curso não entra, e a última hora fechada entra na
  rodada seguinte (a cada 15 min). A barra de hoje e a latência de 24h da página pública, e
  as janelas acima de 48h de `/checks`, ficam esse tanto atrás. O estado atual da página não
  sofre com isso: vem do último check.
- **A latência da página é aproximada:** média das medianas horárias, ponderada pelos
  sucessos de cada hora, arredondada. Não é a mediana exata das 24h.
- **Dias da barra em UTC.** Sem JavaScript, não há fuso do visitante; os horários dos
  incidentes também saem em UTC, e a página diz isso.
- **Cache de 30s em dois lugares.** O navegador (e qualquer proxy) guarda a resposta 30s, e o
  processo guarda a projeção 30s por slug. Mudar a página no painel leva até ~60s para
  aparecer para quem já a abriu. A página recém-publicada aparece na hora: o 404 não é
  guardado.
- **Cache por processo.** Com várias instâncias da API, cada uma tem o seu; o teto é de 1.000
  slugs por instância.
- **Um 429 dentro do perdão ainda conta como `retry` em `notify_total`.** Os adiados sem envio
  contam como `deferred`.
- **Um webhook que responde 429 para sempre** segura cada notificação por até 1h antes de ela
  começar a gastar tentativas. Depois disso, as 6 tentativas seguem o backoff normal.
- **Canal `failing` é derivado de "última falha depois do último sucesso".** Um canal que
  alterna falha e sucesso fica `failing` só até a próxima entrega que der certo.
- **Retenção de checks por partição inteira:** os checks duram entre 30 e 61 dias.
- **429 e 413 da cadeia `/p/` saem como envelope JSON**, como os de `/verify/`.

## Para o Plano 5

- Telas de incidentes, canais (com o estado `failing`, `last_error` e `last_failure_at`) e
  status page (cadastro, pré-visualização, link público), consumindo os contratos deste plano.
- Gráficos do detalhe do alvo: `resolution` decide entre a série bruta (`items` com
  `checked_at`) e a horária (`items` com `bucket`, `total`, `ok`, `p50_ms`…). Janela máxima:
  400 dias.
- Documentar no cadastro de alvos que headers **não** seguem redirect para outro host:porta
  nem rebaixamento https→http (I3 do Plano 2).
- Documentar no cadastro de canais que webhook de canal de **fórum** do Discord é recusado.
- `Spec.Headers` decifrado com `LogValue` que redija os valores (M6 do Plano 2).
- Porta de métricas em desenvolvimento com `--role=api` e `--role=prober` no mesmo host (M7).
- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA; troca de autenticador;
  `HashPasswordWith` com parâmetros fora de faixa (herdados).
- Rota para desabilitar canal, se o painel precisar (a coluna `enabled` já é respeitada).

## Plano próprio, fora da sequência

- **R2**, por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.

## Dívidas menores herdadas, ainda abertas

- Partições não herdam a RLS (a aplicação nunca consulta partição; está comentado).
- O comentário de `targets.sql` diz que toda query filtra por `user_id`; o `INSERT` não filtra.
- `initialisms` sem `ip` gera `IpHash` nos modelos de auth.
- `targets.Service.Get`/`List` decifram os headers só para devolver os nomes; uma linha
  corrompida derruba o `List`.
- `apply` compara antes de normalizar; `Update` revalida a URL guardada; `Service.List` com
  `limit` fora de faixa cai para 50; `0` explícito em `POST` vira padrão e em `PATCH` dá 400.
- `Probe` com `Timeout == 0` gera contexto vencido (a validação garante ≥ 1000 ms).
- `TestConcurrentMigrationsSerialize` não confere a versão final do schema.
- `GRANT CREATE ON DATABASE` do testdb não sobrevive ao `Restore` (falta comentário).
- Logs do testcontainers poluem a saída de teste; nomes de teste em inglês ao lado de helpers
  em português.

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-statuspage-e-retencao.md`: decisões deste plano, com o
  custo de cada uma estar errada.
- `docs/superpowers/plans/2026-09-24-incidentes-e-notificacoes.md`: decisões D1–D24 do Plano 3.
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`: o estado anterior a este plano.
```

Aplique em `docs/superpowers/plans/2026-09-22-roadmap.md`:

```diff
--- a/docs/superpowers/plans/2026-09-22-roadmap.md
+++ b/docs/superpowers/plans/2026-09-22-roadmap.md
@@ -13,7 +13,7 @@
 | 1 | Fundação e segurança de base | `2026-09-22-fundacao-e-autenticacao.md` | API sobe, autentica, núcleo de segurança testado | 1–5 | concluído |
 | 2 | Alvos e sondador | `2026-09-24-alvos-e-sondador.md` | Alvos sondados, checks persistidos | 6–7 | concluído |
 | 3 | Incidentes e notificações | `2026-09-24-incidentes-e-notificacoes.md` | Incidente vira Discord + e-mail | 8–9 | concluído |
-| 4 | Status page e retenção | `NN-statuspage-e-retencao.md` | Página pública, rollups, partições | 10 | a escrever |
+| 4 | Status page e retenção | `2026-09-24-statuspage-e-retencao.md` | Página pública, rollups, partições | 10 | concluído |
 | 5 | Painel React | `NN-painel-react.md` | Painel completo, deploy, README | 11–14 | a escrever |
 
 ## Ordem e seus motivos
```

- [ ] **Step 5: Verificação final**

Run: `sqlc diff && gofmt -l . && go vet ./... && staticcheck ./... && gosec -quiet ./... && govulncheck ./... && go test -race -count=1 ./...`
Expected: tudo limpo e verde. O `govulncheck` pode citar uma vulnerabilidade num módulo requerido que o código não chama; ela já existia antes deste plano.

- [ ] **Step 6: Commit**

```bash
git add cmd/vigilume/statuspage_e2e_test.go docs/superpowers/
git commit -m "test: ponta a ponta da status page e da manutenção; pendências do Plano 5"
```

---

### Task 11: Correções da revisão final

Uma revisão independente do branch inteiro, feita depois da Task 10, não achou vazamento nem acesso indevido no escopo público. Ela achou sete defeitos e dois testes fracos, e cada um tem aqui o teste que o prova. O oitavo achado (search_path da função plpgsql) foi recusado, com o motivo registrado na migration e na D33.

**Files:**
- Modify: `internal/storage/migrations/00006_statuspage_rollups.sql` (função `public_page_target_ids`, D27)
- Modify: `internal/storage/queries/maintenance.sql` (`SetLocalLockTimeout`), `internal/storage/queries/statuspages.sql` (`LockStatusPage`)
- Modify: `internal/storage/maintenance.go` (backfill de 62 dias; retenção em duas transações, D29 e D30)
- Modify: `internal/notify/dispatcher.go` (falha transitória para o canal no lote; `doSistema`; lease calculado, D28)
- Modify: `internal/statuspage/service.go` (`FOR UPDATE`; invalidação do cache, D31 e D32), `internal/statuspage/public.go` (`forget`)
- Modify: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`
- Generated: `internal/storage/db/*.go`
- Test: `internal/storage/{statuspage_rls,maintenance}_test.go`, `internal/notify/dispatcher_test.go`, `internal/statuspage/{service,public}_test.go`

**Interfaces:**
- Produces: `public_page_target_ids() RETURNS SETOF uuid` no lugar de `target_on_public_page(uuid)`; queries `SetLocalLockTimeout`, `LockStatusPage`; `(*cachePublico).forget(slug)`; `desfecho.transitoria`, `desfecho.doSistema`.

**Contexto para quem implementa.** A migration 00006 muda **no próprio arquivo**, e não num 00007, porque ela nunca foi aplicada em ambiente nenhum: só existe neste branch. Os achados, na ordem da revisão:

1. **A política pública chamava a função uma vez por alvo da instância**, de qualquer dono. Medido na revisão: ~0,5 s com 5 mil alvos. A política passa a perguntar `id IN (SELECT public_page_target_ids())`, e o Postgres roda a função uma vez por consulta. O teste conta as chamadas com `track_functions` e `pg_stat_get_xact_function_calls`, sem cronômetro.
2. **Um canal lento segurava o lote por k × `sendTimeout`**, e os outros canais esperavam junto. Regressão da Task 5. Agora qualquer falha **transitória** para o canal no lote (D28). O lease passa a cobrir o pior lote.
3. **O `DROP` da partição segurava `ACCESS EXCLUSIVE` em `checks`** durante a retenção inteira. Passa a rodar numa transação própria, com `lock_timeout` de 2 s (D29).
4. **O backfill de 31 dias perdia os checks de 31 a 61 dias** no primeiro deploy. Passa a 62 (D30).
5. **Slug renomeado continuava no cache**, inclusive quando outro dono o tomava. Invalidação no `Create` e no `Update` (D31).
6. **Dois PATCH simultâneos perdiam campo.** `FOR UPDATE` (D32).
7. **O canal virava `failing` por uma falha do próprio Vigilume** (lease vencido). Deixa de marcar.
8. **Dois testes fracos.** O comentário de `TestPublicPolicyDoesNotWidenOwnerScope` prometia provar a guarda, e a guarda é redundante na semântica: agora diz que prova a propriedade. `TestPublicScopeSeesOnlyThePublishedPageTargets` contava zero em tabelas vazias; agora grava as linhas antes, confere que gravou, e cobre também `check_rollup_1h` e `target_health`.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/storage/statuspage_rls_test.go`:

```diff
--- a/internal/storage/statuspage_rls_test.go
+++ b/internal/storage/statuspage_rls_test.go
@@ -79,14 +79,35 @@
 	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
 		t.Fatal(err)
 	}
+	// Uma linha de cada tabela por alvo, dentro e fora da página: sem elas, o
+	// 0 das tabelas que o público não pode ver seria verdade por acidente.
+	canal := novoCanal(t, pool, a, nil, time.Time{})
 	for _, alvo := range []uuid.UUID{naPagina, foraDaPagina, alvoB} {
 		novoCheck(t, pool, alvo, agora)
-		novoIncidente(t, pool, alvo)
+		novaNotificacao(t, pool, novoIncidente(t, pool, alvo), canal)
+		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.EnsureTargetHealth(ctx, alvo) }); err != nil {
+			t.Fatal(err)
+		}
+	}
+	motivo := "rejected"
+	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
+		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: canal, Now: agora, LastError: &motivo})
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := storage.ConsolidateRollups(ctx, pool, agora.Add(2*time.Hour)); err != nil {
+		t.Fatal(err)
+	}
+	for tabela, quer := range map[string]int{"notifications": 3, "channel_health": 1, "check_rollup_1h": 3, "target_health": 3} {
+		if n := contarComoSondador(t, pool, `SELECT count(*) FROM `+tabela); n != quer {
+			t.Fatalf("a preparação gravou %d linhas em %s, queria %d", n, tabela, quer)
+		}
 	}
-	novoCanal(t, pool, a, nil, time.Time{})
 
 	for tabela, quer := range map[string]int{
 		"status_pages": 1, "status_page_items": 1, "targets": 1, "checks": 1, "incidents": 1,
+		"check_rollup_1h": 1, "target_health": 1,
 		"notif_channels": 0, "notifications": 0, "channel_health": 0,
 	} {
 		if n := contarPublico(t, pool, "pagina-a", tabela); n != quer {
@@ -94,7 +115,7 @@
 		}
 	}
 
-	err := storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
+	err = storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
 		if _, err := q.GetPublicPage(ctx, "pagina-b"); !errors.Is(err, pgx.ErrNoRows) {
 			t.Errorf("com o slug de A, a página de B deu %v, queria ErrNoRows", err)
 		}
@@ -186,8 +207,11 @@
 	}
 }
 
-// A política pública não muda o que o dono vê: fora do escopo público, ela
-// não casa com nada.
+// A política pública não muda o que o dono vê. Prova a propriedade, e não a
+// guarda "app.public_slug IS NOT NULL" da política: sem ela, a função
+// devolveria os itens que o dono enxerga, que são só os das páginas dele, e
+// o resultado seria o mesmo (achado da revisão, provado por sabotagem). A
+// guarda é atalho: fora do escopo público, a função nem precisa rodar.
 func TestPublicPolicyDoesNotWidenOwnerScope(t *testing.T) {
 	pool := testdb.New(t)
 	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
@@ -237,3 +261,63 @@
 		t.Errorf("o escopo verify viu %d linhas de channel_health", n)
 	}
 }
+
+// A política pública de targets resolve os alvos da página UMA vez por
+// consulta, e não uma vez por alvo da instância (achado da revisão: com uma
+// função por linha, a página pública custava ~0,5 s com 5 mil alvos de
+// outros donos). A contagem de chamadas vem do próprio Postgres
+// (track_functions), sem cronômetro.
+func TestPublicPolicyResolvesThePageTargetsOnce(t *testing.T) {
+	pool := testdb.New(t)
+	ctx := context.Background()
+	a, outro := novoUsuario(t, pool), novoUsuario(t, pool)
+	novaPagina(t, pool, a, "pagina-a", true, novoAlvo(t, pool, a, agora))
+	for range 50 {
+		novoAlvo(t, pool, outro, agora)
+	}
+
+	var papel string
+	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&papel); err != nil {
+		t.Fatal(err)
+	}
+	admin, err := storage.NewPool(ctx, testdb.AdminDSN(t))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer admin.Close()
+	conn, err := admin.Acquire(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer conn.Release()
+	// track_functions só o superusuário liga; SET ROLE devolve a conexão ao
+	// papel da aplicação, e a RLS volta a valer.
+	for _, sql := range []string{`SET track_functions = 'pl'`, `SET ROLE ` + pgx.Identifier{papel}.Sanitize()} {
+		if _, err := conn.Exec(ctx, sql); err != nil {
+			t.Fatal(err)
+		}
+	}
+	defer conn.Exec(ctx, `RESET ROLE`)
+	tx, err := conn.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer tx.Rollback(ctx)
+	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', 'pagina-a', true)`); err != nil {
+		t.Fatal(err)
+	}
+	var vistos int
+	if err := tx.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&vistos); err != nil {
+		t.Fatal(err)
+	}
+	var chamadas int64
+	if err := tx.QueryRow(ctx, `SELECT coalesce(pg_stat_get_xact_function_calls('public_page_target_ids'::regproc), 0)`).Scan(&chamadas); err != nil {
+		t.Fatal(err)
+	}
+	if vistos != 1 || chamadas < 1 || chamadas > 2 {
+		t.Errorf("o escopo público viu %d alvos com %d chamadas da função; queria 1 alvo e 1 ou 2 chamadas, e não uma por alvo (51)", vistos, chamadas)
+	}
+}
```

Aplique em `internal/storage/maintenance_test.go`:

```diff
--- a/internal/storage/maintenance_test.go
+++ b/internal/storage/maintenance_test.go
@@ -38,7 +38,7 @@
 	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
 		var err error
 		rows, err = q.ListRollups(ctx, db.ListRollupsParams{
-			UserID: dono, TargetID: alvo, FromAt: hora(0, 0).AddDate(0, 0, -40), ToAt: hora(0, 0).AddDate(0, 0, 40),
+			UserID: dono, TargetID: alvo, FromAt: hora(0, 0).AddDate(0, 0, -90), ToAt: hora(0, 0).AddDate(0, 0, 90),
 		})
 		return err
 	})
@@ -307,3 +307,67 @@
 		t.Errorf("intervalo 0 deu %v, queria ErrMaintenanceInterval", err)
 	}
 }
+
+// Sem rollup nenhum (o primeiro deploy), a consolidação volta até o check
+// bruto mais velho que as partições ainda podem guardar (61 dias), e não só
+// 31: senão a metade mais velha some no DROP sem nunca virar rollup.
+func TestFirstConsolidationReachesTheOldestRawChecks(t *testing.T) {
+	pool := testdb.New(t)
+	ctx := context.Background()
+	dono := novoUsuario(t, pool)
+	alvo := novoAlvo(t, pool, dono, agora)
+	velho := hora(10, 0).AddDate(0, 0, -60)
+	if err := storage.EnsureChecksPartitions(ctx, pool, velho); err != nil {
+		t.Fatal(err)
+	}
+	checkEm(t, pool, alvo, velho.Add(5*time.Minute), "up", 100)
+	consolidar(t, pool, hora(12, 0))
+	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || !rs[0].Bucket.Equal(velho) {
+		t.Errorf("rollups = %+v, queria a hora de 60 dias atrás", rs)
+	}
+}
+
+// O DROP da partição vencida pega ACCESS EXCLUSIVE em checks. Com checks
+// ocupada, ele desiste em dropLockTimeout (55P03), em vez de enfileirar o
+// sondador atrás dele; os DELETEs, numa transação à parte, já valeram. A
+// rodada seguinte, com checks livre, apaga a partição.
+func TestPartitionDropGivesUpWhenChecksIsBusy(t *testing.T) {
+	pool := testdb.New(t)
+	ctx := context.Background()
+	hoje := hora(12, 0)
+	for _, mes := range []time.Time{hoje.AddDate(0, -2, 0), hoje} {
+		if err := storage.EnsureChecksPartitions(ctx, pool, mes); err != nil {
+			t.Fatal(err)
+		}
+	}
+	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, action, at) VALUES ($1, 'teste', $2)`,
+		novoID(), hoje.AddDate(-1, -1, 0)); err != nil {
+		t.Fatal(err)
+	}
+
+	ocupada, err := pool.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := ocupada.Exec(ctx, `SELECT count(*) FROM checks`); err != nil {
+		t.Fatal(err)
+	}
+	prazo, cancel := context.WithTimeout(ctx, 20*time.Second)
+	defer cancel()
+	inicio := time.Now()
+	r, err := storage.ApplyRetention(prazo, pool, hoje)
+	if codigoPG(err) != "55P03" || time.Since(inicio) > 10*time.Second {
+		t.Errorf("com checks ocupada, ApplyRetention deu %v em %v; queria 55P03 (lock_timeout) em segundos", err, time.Since(inicio))
+	}
+	if r.DeletedAudit != 1 || contarComoSondador(t, pool, `SELECT count(*) FROM audit_log`) != 0 {
+		t.Errorf("os DELETEs não valeram com o DROP travado: %+v", r)
+	}
+	if err := ocupada.Rollback(ctx); err != nil {
+		t.Fatal(err)
+	}
+
+	r, err = storage.ApplyRetention(ctx, pool, hoje)
+	if err != nil || r.DroppedPartitions != 1 {
+		t.Errorf("com checks livre: %+v, %v; queria 1 partição apagada", r, err)
+	}
+}
```

Aplique em `internal/notify/dispatcher_test.go`:

```diff
--- a/internal/notify/dispatcher_test.go
+++ b/internal/notify/dispatcher_test.go
@@ -315,6 +315,11 @@
 	if n := b.auditorias(t); n != 1 {
 		t.Errorf("%d entradas de auditoria, queria 1", n)
 	}
+	// A falha foi do Vigilume (o desfecho não chegou a ser gravado), não do
+	// canal: ele não vira failing (achado da revisão).
+	if c := b.canalVisto(t); c.Status != StatusVerified {
+		t.Errorf("canal = %+v; queria verified, sem culpa pela falha do próprio Vigilume", c)
+	}
 }
 
 func TestPermanentFailureIsFinalAtOnce(t *testing.T) {
@@ -711,3 +716,69 @@
 		t.Errorf("dois canais travados levaram %v; em paralelo, ~1s", d)
 	}
 }
+
+// tentativas põe attempts em todas as notificações do dono.
+func (b *bancada) tentativas(t *testing.T, n int32) {
+	t.Helper()
+	ctx := context.Background()
+	tx, err := b.pool.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := tx.Exec(ctx, `UPDATE notifications SET attempts = $1`, n); err != nil {
+		t.Fatal(err)
+	}
+	if err := tx.Commit(ctx); err != nil {
+		t.Fatal(err)
+	}
+}
+
+// Um webhook que trava não segura o lote por k × sendTimeout (achado da
+// revisão): depois de uma falha transitória, o resto da fila do canal é
+// adiado sem envio. Fora do 429, a tentativa conta, como contaria se cada
+// uma tivesse sido enviada e falhado igual.
+func TestTransientFailureHoldsTheRestOfTheChannel(t *testing.T) {
+	b := novaBancada(t, nil)
+	ids := b.apagao(t, 4)
+	b.canal.respostas = []error{&SendError{Reason: ReasonTimeout}}
+
+	b.despachar(t, 4)
+	if n := b.canal.quantos(); n != 1 {
+		t.Fatalf("%d envios, queria 1 (o resto do canal espera)", n)
+	}
+	for i, id := range ids {
+		l := b.estado(t, id)
+		if l.Status != "pending" || l.Attempts != 1 || l.Erro == nil || *l.Erro != "timeout" ||
+			!l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) {
+			t.Errorf("notificação %d: %+v; queria pendente, 1 tentativa, timeout, próxima em 30s", i, l)
+		}
+	}
+	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "deferred")); v != 3 {
+		t.Errorf("notify_total{discord,deferred} = %v, queria 3", v)
+	}
+}
+
+// As adiadas que já estão no teto falham de vez, com o motivo de quem parou
+// o canal, sem esperar outro sendTimeout cada uma.
+func TestDeferredAtTheLastAttemptFailWithoutSending(t *testing.T) {
+	b := novaBancada(t, nil)
+	ids := b.apagao(t, 3)
+	b.tentativas(t, MaxAttempts-1)
+	b.canal.respostas = []error{&SendError{Reason: ReasonNetwork}}
+
+	b.despachar(t, 3)
+	if n := b.canal.quantos(); n != 1 {
+		t.Fatalf("%d envios, queria 1", n)
+	}
+	for i, id := range ids {
+		if l := b.estado(t, id); l.Status != "failed" || l.Erro == nil || *l.Erro != "network" {
+			t.Errorf("notificação %d: %+v; queria failed por network", i, l)
+		}
+	}
+	if c := b.canalVisto(t); c.Status != StatusFailing {
+		t.Errorf("canal = %+v; queria failing", c)
+	}
+}
```

Aplique em `internal/statuspage/service_test.go`:

```diff
--- a/internal/statuspage/service_test.go
+++ b/internal/statuspage/service_test.go
@@ -280,3 +280,64 @@
 		t.Errorf("depois de apagar o alvo: %+v, %v; queria só o Site", p.Items, err)
 	}
 }
+
+// Dois PATCH ao mesmo tempo não perdem campo (achado da revisão): o Update
+// trava a linha antes de ler. Aqui, outra transação trava a página e muda o
+// título; o PATCH de published espera por ela e lê o título novo. Sem a
+// trava, ele leria o título velho e o regravaria por cima.
+func TestConcurrentPatchesDoNotLoseFields(t *testing.T) {
+	a := novoAmbiente(t)
+	ctx := context.Background()
+	dono := a.usuario(t)
+	p, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "Velho"})
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	outra, err := a.pool.Begin(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer outra.Rollback(ctx)
+	for _, sql := range []string{
+		`SELECT set_config('app.user_id', '` + dono.String() + `', true)`,
+		`SELECT id FROM status_pages FOR UPDATE`,
+		`UPDATE status_pages SET title = 'Novo'`,
+	} {
+		if _, err := outra.Exec(ctx, sql); err != nil {
+			t.Fatal(err)
+		}
+	}
+
+	fim := make(chan error, 1)
+	sim := true
+	go func() {
+		_, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Published: &sim})
+		fim <- err
+	}()
+	// O PATCH está esperando pela trava da outra transação.
+	prazo := time.Now().Add(5 * time.Second)
+	for {
+		var esperando int
+		if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
+			WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&esperando); err != nil {
+			t.Fatal(err)
+		}
+		if esperando > 0 {
+			break
+		}
+		if time.Now().After(prazo) {
+			t.Fatal("o PATCH não esperou pela trava da outra transação")
+		}
+		time.Sleep(20 * time.Millisecond)
+	}
+	if err := outra.Commit(ctx); err != nil {
+		t.Fatal(err)
+	}
+	if err := <-fim; err != nil {
+		t.Fatal(err)
+	}
+	if got, err := a.svc.Get(ctx, dono); err != nil || got.Title != "Novo" || !got.Published {
+		t.Errorf("página = %+v, %v; queria o título da outra transação E published", got, err)
+	}
+}
```

Aplique em `internal/statuspage/public_test.go`:

```diff
--- a/internal/statuspage/public_test.go
+++ b/internal/statuspage/public_test.go
@@ -221,7 +221,9 @@
 }
 
 // A projeção fica 30s em cache: mil visitantes não viram mil agregações. A
-// página que acabou de ser publicada aparece na hora (o 404 não é guardado).
+// mudança aqui não passa pelo serviço (é outro processo, ou o sondador), e
+// por isso só aparece quando o cache vence. A página recém-publicada aparece
+// na hora: o 404 não é guardado.
 func TestPublicViewIsCachedForThirtySeconds(t *testing.T) {
 	a := novoAmbiente(t)
 	ctx := context.Background()
@@ -239,8 +241,12 @@
 		t.Fatalf("recém-publicada = %+v, %v; queria visível na hora", v, err)
 	}
 
-	novo := "Título novo"
-	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Title: &novo}); err != nil {
+	// Outro processo muda o título, sem passar por este serviço.
+	err = storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
+		_, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{UserID: dono, ID: p.ID, Slug: "empresa", Title: "Título novo", Published: true})
+		return err
+	})
+	if err != nil {
 		t.Fatal(err)
 	}
 	a.clk.Advance(29 * time.Second)
@@ -253,6 +259,103 @@
 	}
 }
 
+// Mudança feita por este serviço tira o slug do cache na hora (achado da
+// revisão): A renomeia "acme" e B toma o slug logo em seguida; /p/acme
+// mostra B, e não a página de A em cache. Despublicar também vale na hora.
+func TestOwnerChangesInvalidateTheCache(t *testing.T) {
+	a := novoAmbiente(t)
+	ctx := context.Background()
+	donoA, donoB := a.usuario(t), a.usuario(t)
+	pa, err := a.svc.Create(ctx, donoA, statuspage.Input{Slug: "acme", Title: "Página de A", Published: true})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if v, err := a.svc.Public(ctx, "acme"); err != nil || v.Title != "Página de A" {
+		t.Fatalf("antes: %+v, %v", v, err)
+	}
+
+	novo := "acme2"
+	if _, err := a.svc.Update(ctx, donoA, pa.ID, statuspage.Patch{Slug: &novo}); err != nil {
+		t.Fatal(err)
+	}
+	// O endereço abandonado deixa de mostrar A na hora.
+	if _, err := a.svc.Public(ctx, "acme"); !errors.Is(err, statuspage.ErrNotFound) {
+		t.Errorf("/p/acme depois do rename = %v; queria ErrNotFound na hora", err)
+	}
+	if _, err := a.svc.Create(ctx, donoB, statuspage.Input{Slug: "acme", Title: "Página de B", Published: true}); err != nil {
+		t.Fatal(err)
+	}
+	if v, err := a.svc.Public(ctx, "acme"); err != nil || v.Title != "Página de B" {
+		t.Errorf("/p/acme depois da troca = %q, %v; queria a página de B na hora", v.Title, err)
+	}
+
+	// Com acme2 em cache, despublicar tem de tirá-la na hora.
+	if v, err := a.svc.Public(ctx, "acme2"); err != nil || v.Title != "Página de A" {
+		t.Fatalf("/p/acme2 = %q, %v", v.Title, err)
+	}
+	nao := false
+	if _, err := a.svc.Update(ctx, donoA, pa.ID, statuspage.Patch{Published: &nao}); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := a.svc.Public(ctx, "acme2"); !errors.Is(err, statuspage.ErrNotFound) {
+		t.Errorf("despublicada = %v; queria ErrNotFound na hora", err)
+	}
+}
+
+// Com várias instâncias da API, o slug pode ser liberado em OUTRA instância,
+// e esta ainda guarda a página velha nele. Quem toma o slug por esta
+// instância, por Create ou por Update, tira do cache daqui a versão velha.
+func TestTakingASlugFreedElsewhereInvalidatesTheCache(t *testing.T) {
+	a := novoAmbiente(t)
+	ctx := context.Background()
+	donoA, donoB, donoC := a.usuario(t), a.usuario(t), a.usuario(t)
+	liberarEmOutraInstancia := func(dono uuid.UUID, p statuspage.Page, novoSlug string) {
+		t.Helper()
+		err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
+			_, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{UserID: dono, ID: p.ID, Slug: novoSlug, Title: p.Title, Published: true})
+			return err
+		})
+		if err != nil {
+			t.Fatal(err)
+		}
+	}
+
+	pa, err := a.svc.Create(ctx, donoA, statuspage.Input{Slug: "acme", Title: "Página de A", Published: true})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := a.svc.Public(ctx, "acme"); err != nil {
+		t.Fatal(err)
+	}
+	liberarEmOutraInstancia(donoA, pa, "acme-velha")
+	if _, err := a.svc.Create(ctx, donoB, statuspage.Input{Slug: "acme", Title: "Página de B", Published: true}); err != nil {
+		t.Fatal(err)
+	}
+	if v, err := a.svc.Public(ctx, "acme"); err != nil || v.Title != "Página de B" {
+		t.Errorf("Create no slug liberado: /p/acme = %q, %v; queria B na hora", v.Title, err)
+	}
+
+	pb, err := a.svc.Get(ctx, donoB)
+	if err != nil {
+		t.Fatal(err)
+	}
+	liberarEmOutraInstancia(donoB, pb, "acme-b")
+	pc, err := a.svc.Create(ctx, donoC, statuspage.Input{Slug: "zeta", Title: "Página de C", Published: true})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := a.svc.Public(ctx, "acme"); err != nil {
+		t.Fatal(err) // ainda B, no cache desta instância
+	}
+	tomado := "acme"
+	if _, err := a.svc.Update(ctx, donoC, pc.ID, statuspage.Patch{Slug: &tomado}); err != nil {
+		t.Fatal(err)
+	}
+	if v, err := a.svc.Public(ctx, "acme"); err != nil || v.Title != "Página de C" {
+		t.Errorf("Update para o slug liberado: /p/acme = %q, %v; queria C na hora", v.Title, err)
+	}
+}
+
 // Spec §5.6: a projeção pública não tem onde guardar id, URL, código de
 // status ou causa. O teste percorre os tipos; um campo novo com um desses
 // tipos ou nomes quebra aqui antes de chegar ao template.
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -count=1 ./internal/storage/ ./internal/notify/ ./internal/statuspage/`
Expected: FAIL em
- `TestPublicPolicyResolvesThePageTargetsOnce`: a função `public_page_target_ids` não existe;
- `TestFirstConsolidationReachesTheOldestRawChecks`: nenhum rollup;
- `TestPartitionDropGivesUpWhenChecksIsBusy`: estoura o prazo de 20 s, e os DELETEs não valeram;
- `TestNoSendPastTheLastAttempt`: o canal fica `failing`;
- `TestTransientFailureHoldsTheRestOfTheChannel`: 4 envios, e não 1;
- `TestDeferredAtTheLastAttemptFailWithoutSending`: 3 envios;
- `TestOwnerChangesInvalidateTheCache`, `TestTakingASlugFreedElsewhereInvalidatesTheCache` e `TestConcurrentPatchesDoNotLoseFields`.

`TestPublicScopeSeesOnlyThePublishedPageTargets` **passa**: ele ficou mais forte, e a prova dele é a sabotagem do Step 5.

- [ ] **Step 3: Implementar**

Aplique em `internal/storage/migrations/00006_statuspage_rollups.sql`:

```diff
--- a/internal/storage/migrations/00006_statuspage_rollups.sql
+++ b/internal/storage/migrations/00006_statuspage_rollups.sql
@@ -83,18 +83,29 @@
 -- policy" (42P17, provado em spike) ao expandir as políticas. Uma função
 -- plpgsql não é expandida na reescrita; ela roda com o papel de quem chama,
 -- e a RLS dos itens vale dentro dela.
+--
+-- A função devolve o CONJUNTO de alvos da página, e a política pergunta
+-- "id IN (conjunto)": o Postgres a roda uma vez por consulta (hashed
+-- SubPlan). Uma função booleana por alvo rodaria uma vez para cada alvo da
+-- instância, de qualquer usuário, e o custo da página pública cresceria com
+-- o total de alvos (achado da revisão: ~0,5 s com 5 mil alvos).
+--
+-- SECURITY INVOKER sem search_path fixo, de propósito: a função roda com os
+-- privilégios de quem chama, e sombrear status_page_items com uma tabela
+-- temporária exige executar SQL na própria sessão. Não há fronteira de
+-- privilégio a cruzar; isso só importaria numa SECURITY DEFINER.
 -- +goose StatementBegin
-CREATE FUNCTION target_on_public_page(p_target uuid) RETURNS bool
+CREATE FUNCTION public_page_target_ids() RETURNS SETOF uuid
 LANGUAGE plpgsql STABLE AS $$
 BEGIN
-    RETURN EXISTS (SELECT 1 FROM status_page_items i WHERE i.target_id = p_target);
+    RETURN QUERY SELECT i.target_id FROM status_page_items i;
 END
 $$;
 -- +goose StatementEnd
 
 CREATE POLICY targets_public_select ON targets FOR SELECT
     USING (NULLIF(current_setting('app.public_slug', true), '') IS NOT NULL
-           AND target_on_public_page(targets.id));
+           AND targets.id IN (SELECT public_page_target_ids()));
 
 -- Saúde do canal (pendência do Plano 3): o dispatcher grava o último sucesso
 -- e a última falha definitiva, e a API mostra "failing". Tabela à parte, como
@@ -159,7 +170,7 @@
 DROP INDEX notifications_created_idx;
 DROP TABLE channel_health;
 DROP POLICY targets_public_select ON targets;
-DROP FUNCTION target_on_public_page(uuid);
+DROP FUNCTION public_page_target_ids();
 DROP TABLE status_page_items;
 DROP TABLE status_pages;
 DROP TABLE check_rollup_1h;
```

Aplique em `internal/storage/queries/maintenance.sql`:

```diff
--- a/internal/storage/queries/maintenance.sql
+++ b/internal/storage/queries/maintenance.sql
@@ -26,6 +26,10 @@
 -- name: LatestRollupBucket :one
 SELECT coalesce(max(bucket), 'epoch'::timestamptz)::timestamptz FROM check_rollup_1h;
 
+-- Vale só até o fim da transação (o terceiro argumento de set_config).
+-- name: SetLocalLockTimeout :exec
+SELECT set_config('lock_timeout', sqlc.arg(timeout)::text, true);
+
 -- name: DropChecksPartitionsBefore :one
 SELECT drop_checks_partitions_before(sqlc.arg(cutoff)::timestamptz)::int;
 
```

Aplique em `internal/storage/queries/statuspages.sql`:

```diff
--- a/internal/storage/queries/statuspages.sql
+++ b/internal/storage/queries/statuspages.sql
@@ -4,6 +4,11 @@
 -- name: GetStatusPage :one
 SELECT * FROM status_pages WHERE user_id = $1;
 
+-- A trava do PATCH: sem ela, dois PATCH simultâneos leem a mesma versão, e
+-- o segundo regrava os campos que o primeiro mudou (achado da revisão).
+-- name: LockStatusPage :one
+SELECT id FROM status_pages WHERE user_id = $1 FOR UPDATE;
+
 -- name: InsertStatusPage :exec
 INSERT INTO status_pages (id, user_id, slug, title, published, created_at)
 VALUES ($1, $2, $3, $4, $5, $6);
```

Run: `sqlc generate && sqlc diff`

Aplique em `internal/storage/maintenance.go`:

```diff
--- a/internal/storage/maintenance.go
+++ b/internal/storage/maintenance.go
@@ -30,8 +30,15 @@
 	// um check que chegou atrasado ainda entra na hora dele.
 	rollupReconsolidate = 2 * time.Hour
 	// rollupBackfill é até onde a primeira rodada volta, sem rollup nenhum:
-	// o que ainda existe de checks brutos.
-	rollupBackfill = 31 * 24 * time.Hour
+	// o que ainda pode existir de checks brutos. A partição sai inteira, então
+	// um check vive até 61 dias; 31 perderia a metade mais velha no primeiro
+	// deploy (achado da revisão).
+	rollupBackfill = 62 * 24 * time.Hour
+	// dropLockTimeout limita a espera do DROP da partição vencida. O DROP
+	// pega ACCESS EXCLUSIVE em checks, e enquanto espera, enfileira atrás
+	// dele o INSERT do sondador e as leituras da página pública. Melhor
+	// desistir e tentar na próxima rodada.
+	dropLockTimeout = 2 * time.Second
 )
 
 var ErrMaintenanceInterval = errors.New("intervalo de manutenção precisa ser positivo")
@@ -99,13 +106,16 @@
 
 // ApplyRetention apaga o que venceu (spec §12). Pendência nunca sai por idade:
 // uma notificação pendente ainda vai ser tentada.
+//
+// São duas transações. Os DELETEs vêm primeiro; o DROP das partições vem
+// sozinho, com lock_timeout, porque pega ACCESS EXCLUSIVE em checks e não
+// pode segurá-la durante o resto da retenção (achado da revisão). Se o DROP
+// não conseguir a trava, o erro sobe, os DELETEs já valeram, e a próxima
+// rodada tenta de novo.
 func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
 	var r MaintenanceReport
 	err := InProberTx(ctx, pool, func(q *db.Queries) error {
 		var err error
-		if r.DroppedPartitions, err = q.DropChecksPartitionsBefore(ctx, now.Add(-ChecksRetention)); err != nil {
-			return fmt.Errorf("apagar partições vencidas: %w", err)
-		}
 		if r.DeletedRollups, err = q.DeleteOldRollups(ctx, now.AddDate(0, -RollupRetentionMonths, 0)); err != nil {
 			return fmt.Errorf("apagar rollups vencidos: %w", err)
 		}
@@ -120,6 +130,19 @@
 		}
 		return nil
 	})
+	if err != nil {
+		return r, err
+	}
+	err = InProberTx(ctx, pool, func(q *db.Queries) error {
+		if err := q.SetLocalLockTimeout(ctx, fmt.Sprintf("%dms", dropLockTimeout.Milliseconds())); err != nil {
+			return fmt.Errorf("limitar a espera do DROP: %w", err)
+		}
+		var err error
+		if r.DroppedPartitions, err = q.DropChecksPartitionsBefore(ctx, now.Add(-ChecksRetention)); err != nil {
+			return fmt.Errorf("apagar partições vencidas: %w", err)
+		}
+		return nil
+	})
 	return r, err
 }
 
```

Aplique em `internal/notify/dispatcher.go`:

```diff
--- a/internal/notify/dispatcher.go
+++ b/internal/notify/dispatcher.go
@@ -28,13 +28,15 @@
 	maxConcurrentSends = 8
 	// sendTimeout é o prazo de um envio (spec §8): um webhook lento não
 	// segura o lote além disso.
-	sendTimeout = 10 * time.Second
-	// lease é quanto uma notificação reivindicada fica invisível para os
-	// outros dispatchers. Tem de passar com folga de sendTimeout +
-	// persistTimeout; um processo que morre no meio devolve a notificação à
-	// fila quando o lease vence.
-	lease          = 2 * time.Minute
+	sendTimeout    = 10 * time.Second
 	persistTimeout = 5 * time.Second
+	// lease é quanto uma notificação reivindicada fica invisível para os
+	// outros dispatchers; um processo que morre no meio devolve a notificação
+	// à fila quando ele vence. O mesmo canal recebe em série, então o pior
+	// lote é o lote inteiro num canal só, com cada envio lento mas bem
+	// sucedido: maxConcurrentSends × (sendTimeout + persistTimeout), mais
+	// folga. Menos que isso, um segundo dispatcher reenviaria.
+	lease = maxConcurrentSends*(sendTimeout+persistTimeout) + time.Minute
 
 	// MaxAttempts é o teto de tentativas (spec §8). Com o backoff abaixo, a
 	// sexta tentativa acontece cerca de 15 minutos depois da primeira.
@@ -149,18 +151,21 @@
 	return filas
 }
 
-// entregarEmSerie entrega a fila de um canal. Depois de um 429, o resto da
-// fila não é enviado: fica para quando o provedor pediu, sem gastar
-// tentativa, porque não houve envio.
+// entregarEmSerie entrega a fila de um canal. Depois de uma falha
+// transitória (429, timeout, rede, 5xx), o resto da fila não é enviado: o
+// canal está pedindo espera ou está fora, e mandar mais só gastaria até
+// sendTimeout por notificação. Sem isso, um webhook que trava seguraria o
+// lote inteiro por k × sendTimeout, e os outros canais esperariam junto
+// (achado da revisão).
 func (d *Dispatcher) entregarEmSerie(fila []db.ClaimDueNotificationsRow) {
-	var espera time.Time
+	var parou *desfecho
 	for _, n := range fila {
-		if !espera.IsZero() && d.perdoa429(n) {
-			d.adiar(n, espera)
+		if parou != nil {
+			d.adiar(n, *parou)
 			continue
 		}
-		if f := d.entregarProtegido(n); f.reason == ReasonRateLimited && f.status == "pending" {
-			espera = f.proxima
+		if f := d.entregarProtegido(n); f.transitoria {
+			parou = &f
 		}
 	}
 }
@@ -184,10 +189,23 @@
 }
 
 // adiar devolve à fila, sem enviar, uma notificação cujo canal acabou de
-// pedir espera.
-func (d *Dispatcher) adiar(n db.ClaimDueNotificationsRow, ate time.Time) {
+// falhar de forma transitória. Só o 429 dentro de rateLimitGrace devolve a
+// tentativa: é o provedor pedindo espera. Nos outros casos a tentativa conta,
+// como contaria se ela tivesse sido enviada e falhado igual, e quem já está
+// no teto falha de vez, sem esperar outro sendTimeout.
+func (d *Dispatcher) adiar(n db.ClaimDueNotificationsRow, parou desfecho) {
 	d.total.WithLabelValues(n.Kind, "deferred").Inc()
-	f := desfecho{status: "pending", reason: ReasonRateLimited, proxima: ate, devolver: true}
+	proxima := parou.proxima
+	if proxima.IsZero() { // quem parou o canal já tinha falhado de vez
+		proxima = d.clk.Now().Add(backoff(n.Attempts))
+	}
+	f := desfecho{status: "pending", reason: parou.reason, proxima: proxima}
+	switch {
+	case parou.reason == ReasonRateLimited && d.perdoa429(n):
+		f.devolver = true
+	case n.Attempts >= MaxAttempts:
+		f = desfecho{status: "failed", reason: parou.reason}
+	}
 	if err := d.persistir(n, f); err != nil {
 		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
 	}
@@ -199,8 +217,15 @@
 	reason  Reason
 	proxima time.Time
 	// devolver desconta a tentativa que a reivindicação contou (429 dentro
-	// de rateLimitGrace, ou adiamento sem envio).
+	// de rateLimitGrace).
 	devolver bool
+	// transitoria: o envio falhou por um motivo que passa (429, timeout,
+	// rede, 5xx), mesmo que a notificação tenha esgotado as tentativas. O
+	// resto da fila do canal no lote é adiado.
+	transitoria bool
+	// doSistema: a falha foi do Vigilume, e não do canal (o desfecho da
+	// última tentativa não chegou a ser gravado). Não marca o canal failing.
+	doSistema bool
 }
 
 func (d *Dispatcher) entregar(n db.ClaimDueNotificationsRow) desfecho {
@@ -232,7 +257,7 @@
 	// commit que falhou) e a linha voltou quando o lease venceu: ela falha
 	// de vez sem enviar, porque o teto é de envios.
 	if n.Attempts > MaxAttempts {
-		return desfecho{status: "failed", reason: ReasonUnknown}
+		return desfecho{status: "failed", reason: ReasonUnknown, doSistema: true}
 	}
 	canal, ok := d.notifiers[Kind(n.Kind)]
 	if !ok {
@@ -260,14 +285,17 @@
 			se.Reason = ReasonTimeout
 		}
 	}
+	if se.Permanent {
+		return desfecho{status: "failed", reason: se.Reason}
+	}
 	espera := max(backoff(n.Attempts), se.RetryAfter)
-	if se.Reason == ReasonRateLimited && !se.Permanent && d.perdoa429(n) {
-		return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), devolver: true}
+	if se.Reason == ReasonRateLimited && d.perdoa429(n) {
+		return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), devolver: true, transitoria: true}
 	}
-	if se.Permanent || n.Attempts >= MaxAttempts {
-		return desfecho{status: "failed", reason: se.Reason}
+	if n.Attempts >= MaxAttempts {
+		return desfecho{status: "failed", reason: se.Reason, transitoria: true}
 	}
-	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera)}
+	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), transitoria: true}
 }
 
 // backoff dobra a cada tentativa a partir de firstBackoff: 30s, 1min,
@@ -306,9 +334,12 @@
 			return err
 		}
 		// O canal passa a "failing" até a próxima entrega que der certo
-		// (pendência do Plano 3: antes, a falha só ia para a auditoria).
-		if err := q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: n.ChannelID, Now: d.clk.Now(), LastError: motivo}); err != nil {
-			return err
+		// (pendência do Plano 3: antes, a falha só ia para a auditoria). A
+		// falha do próprio Vigilume não é culpa do canal.
+		if !f.doSistema {
+			if err := q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: n.ChannelID, Now: d.clk.Now(), LastError: motivo}); err != nil {
+				return err
+			}
 		}
 		// Falha definitiva vai para a trilha de auditoria (spec §8).
 		id, err := uuid.NewV7()
```

Aplique em `internal/statuspage/service.go`:

```diff
--- a/internal/statuspage/service.go
+++ b/internal/statuspage/service.go
@@ -71,6 +71,9 @@
 		p, err = ler(ctx, q, userID)
 		return err
 	})
+	if err == nil {
+		s.cache.forget(in.Slug)
+	}
 	return p, err
 }
 
@@ -78,11 +81,19 @@
 // resultado, como se fosse um cadastro novo.
 func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, patch Patch) (Page, error) {
 	var p Page
+	var slugAntigo string
 	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
+		if _, err := q.LockStatusPage(ctx, userID); err != nil {
+			if errors.Is(err, pgx.ErrNoRows) {
+				return ErrNotFound
+			}
+			return fmt.Errorf("travar status page: %w", err)
+		}
 		atual, err := ler(ctx, q, userID)
 		if err != nil {
 			return err
 		}
+		slugAntigo = atual.Slug
 		if atual.ID != id {
 			return ErrNotFound
 		}
@@ -118,6 +129,13 @@
 		p, err = ler(ctx, q, userID)
 		return err
 	})
+	if err == nil {
+		// O slug antigo pode ser tomado por outro dono logo em seguida, e a
+		// página despublicada tem de sair na hora: o cache deste processo
+		// não pode servir a versão velha por mais 30s (achado da revisão).
+		s.cache.forget(slugAntigo)
+		s.cache.forget(p.Slug)
+	}
 	return p, err
 }
 
```

Aplique em `internal/statuspage/public.go`:

```diff
--- a/internal/statuspage/public.go
+++ b/internal/statuspage/public.go
@@ -229,6 +229,13 @@
 	return e.v, true
 }
 
+// forget tira o slug do cache: a página mudou neste processo.
+func (c *cachePublico) forget(slug string) {
+	c.mu.Lock()
+	defer c.mu.Unlock()
+	delete(c.entradas, slug)
+}
+
 func (c *cachePublico) put(slug string, v View, agora time.Time) {
 	c.mu.Lock()
 	defer c.mu.Unlock()
```

Aplique em `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`:

```diff
--- a/docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md
+++ b/docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md
@@ -17,8 +17,9 @@
 5. **`PUBLIC_BASE_URL` é o host da API.** Agora vale também para a status page: o
    `public_url` de `GET /status-pages` é `PUBLIC_BASE_URL/p/{slug}`, servido pela API.
 6. **Primeira manutenção depois do deploy.** Sem rollup nenhum, a primeira rodada consolida
-   os últimos 31 dias de checks de uma vez (uma consulta só). Com muitos alvos, é a rodada
-   mais pesada que o job faz; as seguintes voltam só 2 horas.
+   os últimos 62 dias de checks de uma vez (uma consulta só): tudo o que as partições ainda
+   guardam. Com muitos alvos, é a rodada mais pesada que o job faz; as seguintes voltam só
+   2 horas.
 
 ## Resolvidas no Plano 4
 
@@ -47,19 +48,28 @@
 - **Dias da barra em UTC.** Sem JavaScript, não há fuso do visitante; os horários dos
   incidentes também saem em UTC, e a página diz isso.
 - **Cache de 30s em dois lugares.** O navegador (e qualquer proxy) guarda a resposta 30s, e o
-  processo guarda a projeção 30s por slug. Mudar a página no painel leva até ~60s para
-  aparecer para quem já a abriu. A página recém-publicada aparece na hora: o 404 não é
-  guardado.
-- **Cache por processo.** Com várias instâncias da API, cada uma tem o seu; o teto é de 1.000
-  slugs por instância.
+  processo guarda a projeção 30s por slug. Uma mudança feita pelo painel tira o slug do cache
+  da instância que a recebeu; quem já abriu a página vê a mudança em até 30s (cache HTTP).
+  Checks, rollups e incidentes novos aparecem quando o cache vence. A página recém-publicada
+  aparece na hora: o 404 não é guardado.
+- **Cache por processo.** Com várias instâncias da API, cada uma tem o seu, e a mudança feita
+  numa leva até 30s para valer nas outras; o teto é de 1.000 slugs por instância.
 - **Um 429 dentro do perdão ainda conta como `retry` em `notify_total`.** Os adiados sem envio
   contam como `deferred`.
 - **Um webhook que responde 429 para sempre** segura cada notificação por até 1h antes de ela
   começar a gastar tentativas. Depois disso, as 6 tentativas seguem o backoff normal.
+- **Uma falha transitória para o canal no lote.** Depois de um 429, timeout, erro de rede ou
+  5xx, o resto das notificações daquele canal no lote é adiado sem envio. Fora do 429 dentro
+  de 1h, a tentativa conta, e quem está no teto falha de vez com o mesmo motivo. Um canal
+  fora do ar esgota as 6 tentativas das notificações dele em ~15 min, como antes.
+- **Lease de 3 min** (8 × (10 s + 5 s) + 1 min): um processo que morre no meio de um lote
+  devolve as notificações à fila 3 min depois.
 - **Canal `failing` é derivado de "última falha depois do último sucesso".** Um canal que
   alterna falha e sucesso fica `failing` só até a próxima entrega que der certo.
 - **Retenção de checks por partição inteira:** os checks duram entre 30 e 61 dias.
 - **429 e 413 da cadeia `/p/` saem como envelope JSON**, como os de `/verify/`.
+- **O DROP da partição vencida desiste em 2 s** se `checks` estiver ocupada (`lock_timeout`),
+  e tenta de novo na rodada seguinte. Os DELETEs da retenção rodam antes, noutra transação.
 
 ## Para o Plano 5
 
```

- [ ] **Step 4: Rodar e ver passar**

Run: `sqlc diff && gofmt -l . && go vet ./... && go test -race -count=1 ./...`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure depois de cada uma (e `sqlc generate` quando mexer em SQL):
1. Na migration, volte ao desenho antigo: uma função `target_on_public_page(p_target uuid) RETURNS bool`, que consulta `public_page_target_ids()`, e a política com `AND target_on_public_page(targets.id)`. Expected: FAIL em `TestPublicPolicyResolvesThePageTargetsOnce`, com 51 chamadas. **Atenção:** trocar a política por uma subconsulta correlacionada (`SELECT bool_or(x = targets.id) FROM public_page_target_ids() x`) **não** é sabotagem válida. O `Function Scan` sem parâmetro externo materializa o resultado, e a função continua rodando uma vez só (visto na validação).
2. Acrescente à migration `CREATE POLICY vaza ON notifications FOR SELECT USING (current_setting('app.public_slug', true) <> '')`. Expected: FAIL em `TestPublicScopeSeesOnlyThePublishedPageTargets` ("notifications mostrou 3 linhas").
3. `rollupBackfill` de volta a 31 dias. Expected: FAIL em `TestFirstConsolidationReachesTheOldestRawChecks`.
4. `SetLocalLockTimeout(ctx, "0")` (sem limite). Expected: FAIL em `TestPartitionDropGivesUpWhenChecksIsBusy` (prazo de 20 s estourado).
5. Em `entregarEmSerie`, pare o canal só em 429 (`f.transitoria && f.reason == ReasonRateLimited`). Expected: FAIL em `TestTransientFailureHoldsTheRestOfTheChannel` e `TestDeferredAtTheLastAttemptFailWithoutSending`.
6. Em `adiar`, tire o caso `n.Attempts >= MaxAttempts`. Expected: FAIL em `TestDeferredAtTheLastAttemptFailWithoutSending`.
7. `if !f.doSistema` → `if true`. Expected: FAIL em `TestNoSendPastTheLastAttempt`.
8. Tire a chamada a `LockStatusPage` do `Update`. Expected: FAIL em `TestConcurrentPatchesDoNotLoseFields`.
9. Tire, um de cada vez, os três `forget`: o do slug antigo no `Update`, o do slug novo no `Update` e o do `Create`. Expected: FAIL em `TestOwnerChangesInvalidateTheCache` (slug abandonado) ou em `TestTakingASlugFreedElsewhereInvalidatesTheCache` (slug liberado em outra instância).

- [ ] **Step 6: Commit**

```bash
git add internal/storage/ internal/notify/ internal/statuspage/ docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md
git commit -m "fix: correções da revisão final do Plano 4"
```

---

## Depois da última tarefa

Revisão do branch inteiro (`superpowers:requesting-code-review`), com atenção especial à tabela da §5.6 contra tudo o que `/p/{slug}` renderiza, e às políticas de RLS da migration 00006 contra os quatro escopos. Depois, `superpowers:finishing-a-development-branch`.
