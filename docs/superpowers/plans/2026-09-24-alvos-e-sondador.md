# Vigilume — Plano 2: Alvos e Sondador

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** O usuário cadastra alvos HTTP pela API, e o sondador os sonda em intervalo, com concorrência limitada e a guarda anti-SSRF no caminho, gravando cada resultado em `checks`.

**Architecture:** Os alvos vivem em `targets`, protegida por Row-Level Security com `FORCE` (um papel só). O sondador reivindica alvos vencidos com `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED) RETURNING`, que já grava o próximo `next_run_at` com jitter. A reivindicação é um lease, com commit antes de discar. Um semáforo global de 64 limita as sondas em voo. As consultas novas são SQL escrito à mão e compilado pelo `sqlc`, atrás de repositórios que exigem `userID`. `checks` é particionada por mês em UTC.

**Tech Stack:** Go 1.26, PostgreSQL 16, `pgx/v5`, `sqlc` v1.31.1, `goose` v3 (Provider + session locker), `golang.org/x/sync` (`semaphore`, `errgroup`), `prometheus/client_golang`, `testcontainers-go`.

**Spec:** `docs/superpowers/specs/2026-09-22-vigilume-design.md`

**Roteiro:** `docs/superpowers/plans/2026-09-22-roadmap.md` (este é o plano 2 de 5)

**Pendências absorvidas:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-2.md` — R15, R22, R23 e 9b entram aqui; R2 foi adiada para o Plano 4 por decisão do usuário.

---

## Global Constraints

Valores copiados da spec. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/vigilume`.
- **PostgreSQL 16+.**
- **Sem framework HTTP** (`net/http` com padrões de método no `ServeMux`) e **sem ORM** (spec §3.3).
- **`sqlc` só no código novo** (`targets`, `checks`, agendador). `auth` continua com SQL à mão até o Plano 4 (decisão do usuário, 2026-09-23).
- **Todo código de aplicação sob `internal/`** (spec §3.4).
- **IDs UUIDv7**, gerados em Go com `uuid.NewV7()` (spec §4.2).
- **Toda função de repositório recebe `userID` como primeiro parâmetro depois de `ctx`**; recurso de outro usuário devolve **404, nunca 403** (spec §5.5).
- **RLS com `FORCE ROW LEVEL SECURITY`, um papel só.** A API faz `set_config('app.user_id', ..., true)` em toda transação; só a reivindicação do sondador liga `app.scope = 'prober'`. O boot **recusa** papel superusuário ou `BYPASSRLS` (spec §5.5, decisão do usuário).
- **SSRF (spec §5.1):** apenas `http`/`https`; validação do IP no `ControlContext`; redirecionamentos limitados a **3**, revalidados a cada salto; corpo lido por `io.LimitReader(resp.Body, 64<<10)` e descartado; timeout rígido por sonda, propagado por `context`.
- **Headers de alvo são segredo:** AES-256-GCM em `headers_enc`, AAD = `crypto.AAD("targets", "headers_enc", id)` (spec §5.4). **A API nunca devolve valor de header**, só os nomes.
- **`error_kind` é enum** e a mensagem crua de erro nunca é persistida nem exibida (spec §4.2, §5.6).
- **Sondador (spec §6):** tick de **1s**; semáforo global de **64**; `FOR UPDATE SKIP LOCKED`; jitter de **±10%**; timeout menor que o intervalo (aqui: **< 90%**, ver D3); shutdown gracioso de **15s**; relógio injetado, **sem `time.Sleep` nos testes do agendador**.
- **Estados:** `up` (sucesso e latência ≤ limiar), `degraded` (sucesso e latência > limiar), `down` (falha). `degraded_threshold_ms` tem padrão **2000**, e **nulo desativa** (spec §11.5).
- **`checks` particionada por mês**, `RANGE (checked_at)`, `PRIMARY KEY (id, checked_at)` (spec §4.1–4.2).
- **Paginação por cursor, nunca `OFFSET`.** Envelope de erro estável `{"error":{"code","message"},"trace_id"}` (spec §9.1).
- **Janela de `checks` ≤ 48h lê da tabela bruta** (spec §9.1). Acima disso, rollups, que chegam no Plano 4.
- **Rate limit por usuário nas rotas autenticadas**, além do limite por IP (spec §5.7).
- **Métricas:** `probe_duration_seconds` (histograma por alvo), `probe_total{status}`, `scheduler_inflight`, `scheduler_queue_depth` e as métricas do `pgxpool`. **`/metrics` só em bind local** (spec §13, §5.7).
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa** (spec §14).
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não vaza", "não alcança", "não grava"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho, restaura, e **registra no relatório** o que quebrou e o que viu. No Plano 1, catorze testes passaram sem a implementação. Conhecer o padrão não imunizou ninguém.

### Desvios registrados em relação à spec

| Spec | Aqui | Motivo |
|---|---|---|
| §3.4 `migrations/` na raiz | `internal/storage/migrations/` | Herdado do Plano 1: `//go:embed` só enxerga o diretório do pacote. |
| §4.2 enum `error_kind` com 7 valores | 8 valores: acrescenta `unknown` | Todo enum de erro de rede precisa de um valor residual. Sem ele, um `connection reset` ou um `EOF` no meio da resposta teria de ser classificado como algo que não é (D1). |
| §6.2 "timeout menor que o intervalo" | timeout **< 90%** do intervalo | O jitter de −10% pode antecipar a próxima execução para 90% do intervalo (D3). |
| §5.5 RLS "em todas as tabelas com `user_id`" | `targets`, e `checks` por delegação | `users`, `sessions`, `audit_log` e `totp_recovery_codes` são lidas pela própria autenticação, antes de existir usuário na transação. As tabelas dos Planos 3 e 4 entram com RLS desde a migration que as cria (D7). |

---

## Decisões tomadas neste plano

Cada uma tem o custo de estar errada, para quem revisar depois.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | `error_kind` ganha `unknown` | Forçar todo erro num dos 7 | Uma migration de `CHECK` e um valor a mais no front |
| D2 | Corpo acima de 64 KiB **não** é falha: lê 64 KiB, fecha e segue. `body_limit` fica no enum e o Plano 2 nunca o emite | Tratar corpo grande como `down` | Uma home page comum tem mais de 64 KiB; marcá-la como fora seria falso positivo permanente |
| D3 | `timeout_ms * 10 < interval_sec * 9000`, na validação **e** num `CHECK` do banco | Timeout < intervalo, como a spec diz | Com 100%, uma sonda de 29s num intervalo de 30s poderia começar a seguinte com a anterior em voo |
| D4 | Métodos aceitos: `GET`, `HEAD`, `POST` | Todos os métodos | Não há coluna de corpo; `PUT`/`DELETE` num monitor sem corpo seriam armadilha |
| D5 | Headers são *write-only* na API. Nomes proibidos: `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, `Keep-Alive`, `Upgrade`, `Te`, `Trailer`, `Expect`, `Proxy-Authorization`, `Proxy-Connection` | Devolver os valores ao dono | Devolver o valor faz de toda resposta de `GET` um canal de vazamento de `Authorization` |
| D6 | `GET /targets/{id}/checks` aceita janela ≤ 48h; acima disso responde 400 até o Plano 4 | Ler 13 meses da tabela bruta | O Plano 4 troca o 400 por leitura de rollup, sem mudar o contrato |
| D7 | RLS em `targets` e em `checks` (por `EXISTS` no alvo); nada nas tabelas de auth | RLS em tudo | Ver desvios. As tabelas de auth já têm a barreira de assinatura |
| D8 | O boot **recusa** papel superusuário ou `BYPASSRLS` | Só avisar | Recusar trava um deploy mal configurado; avisar desliga a RLS em silêncio, que é pior. **Vira pendência de deploy:** o Fly.io precisa de um papel comum, dono das tabelas |
| D9 | `probe_duration_seconds` com label `target_id`, apagada quando o sondador descobre que o alvo sumiu | Sem label por alvo | Cardinalidade cresce com alvos apagados até o restart, se a limpeza falhar |
| D10 | `METRICS_ADDR` padrão `127.0.0.1:9090`; `off` desliga; qualquer host que não seja loopback é **erro de config** | Aceitar qualquer bind | O Fly.io raspa métricas pela rede privada; o Plano 5 decide como expor |
| D11 | `checked_at` vem do relógio injetado; a latência vem do relógio monotônico real | Tudo pelo relógio injetado | O relógio falso não avança durante a sonda; a latência seria sempre zero nos testes |
| D12 | Sonda abortada pelo shutdown é **descartada**, não gravada | Gravar como falha | Gravar poria um `down` falso no histórico a cada deploy, e o Plano 3 abriria incidente com ele |
| D13 | `PATCH` que muda **o que** é sondado (URL, método, intervalo, timeout, status esperado, headers) reagenda para agora | Manter o agendamento | Sem isso, encurtar o intervalo de 1 dia para 60s só teria efeito no dia seguinte |
| D14 | O envelope de 400 ganha `error.fields: [{field, reason}]`, com vocabulário fechado | Só `validation_failed` | Extensão compatível da §9.1: quem ignora o campo continua funcionando |
| D15 | `safedial.NewClientAllowing` **entra em panic** com endereço que não seja loopback | Aceitar qualquer endereço | A porta existe para teste; o panic impede que vire liberação de rede interna em produção |
| D16 | `id` malformado no caminho responde **404** | 400 | O recurso não existe de nenhum jeito; 400 ensinaria o formato do id a quem varre |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa o Vigilume, com o que uma pessoa razoável esperaria. Cada uma tem teste na tarefa dona.

1. **Conexão reusada do pool depois de um `SET LOCAL`.** Espera-se que a próxima transação na mesma conexão funcione. No Postgres, `current_setting('app.user_id', true)` volta como `''`, e não `NULL`, depois do primeiro `SET LOCAL` da sessão; uma política com `::uuid` direto explode com `invalid input syntax for type uuid`. Isso nunca aparece num teste que abre conexão nova. → Task 3, `TestScopeSurvivesConnectionReuse`.
2. **Virada do mês, com o processo num fuso diferente de UTC.** Espera-se que a sonda das 23:59:59Z do dia 30 e a das 00:00:00Z do dia 1 sejam gravadas, cada uma na partição certa. → Task 3, `TestEnsureChecksPartitionsUsesUTCMonths` e `TestPartitionBoundsAreUTCRegardlessOfSessionTimeZone`.
3. **Alvo apagado ou editado enquanto a sonda dele está em voo.** Espera-se que nada quebre e que nenhum erro vá ao log. O `INSERT` do check falha pela RLS (`42501`) antes da FK (`23503`), e só tratar a FK seria o erro natural. → Task 7, `TestVanishedTargetIsDiscardedQuietly`.
4. **Deploy (SIGTERM) com sondas em voo.** Espera-se que nenhum `down` falso apareça no histórico. Com `WithCancelCause`, o erro da sonda cancelada é a própria causa, e `errors.Is(err, context.Canceled)` é `false`. Detectar o shutdown pelo erro falharia em silêncio. → Task 7, `TestShutdownDiscardsAbortedProbes`.
5. **Timeout perto do intervalo.** Espera-se que a mesma URL nunca seja sondada duas vezes ao mesmo tempo. → Task 4, caso `timeout em 90% do intervalo` de `TestValidate`, e o `CHECK` da migration na Task 3.

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/prober/safedial/safedial.go` | Faixas novas (R22, R23) e zona checada antes do `Unmap`. |
| `internal/prober/safedial/transport.go` | `NewClientAllowing`: exceção exata `IP:porta`, só loopback, para testes. |
| `internal/storage/migrate.go` | goose `Provider` com session locker (9b). |
| `internal/storage/role.go` | `CheckRole`: recusa papel que ignora RLS. |
| `internal/storage/testdb/testdb.go` | Papel não-super; `DSN`, `AdminDSN`, `EmptyDatabase`. |
| `internal/storage/migrations/00004_targets_checks.sql` | `targets`, `checks` particionada, RLS, função de partição. |
| `sqlc.yaml` | Configuração do gerador. |
| `internal/storage/queries/targets.sql` | Queries do CRUD e da leitura de checks. |
| `internal/storage/queries/prober.sql` | Reivindicação, contagem de fila, gravação de check, partição. |
| `internal/storage/db/*.go` | **Gerado pelo sqlc.** Nunca editar à mão. |
| `internal/storage/scope.go` | `InUserTx` e `InProberTx`: as duas únicas portas para a RLS. |
| `internal/storage/partitions.go` | Partições do mês corrente e do seguinte, em UTC. |
| `internal/storage/poolmetrics.go` | Coletor Prometheus do `pgxpool`. |
| `internal/targets/target.go` | Tipos, erros e constantes do domínio. |
| `internal/targets/validate.go` | Validação pura, campo a campo. |
| `internal/targets/headers.go` | Cifrar e decifrar headers com AAD da linha. |
| `internal/targets/service.go` | CRUD sobre `storage` + `sqlc`, exigindo `userID`. |
| `internal/apierr/errors.go` | `FieldError` e `WriteValidation`. |
| `internal/httpapi/middleware/ratelimit.go` | Chave por IP ou por usuário; `RateLimitUser`. |
| `internal/httpapi/optional.go` | `optional[T]`: ausente, `null` ou valor. |
| `internal/httpapi/targethandlers.go` | Handlers de `/targets`. |
| `internal/httpapi/routes.go` | `Deps` e montagem das rotas novas. |
| `internal/prober/probe.go` | Uma sonda: requisição, corpo limitado, classificação de estado. |
| `internal/prober/classify.go` | Erro de rede → `error_kind`. |
| `internal/prober/metrics.go` | Métricas do sondador. |
| `internal/prober/scheduler.go` | Tick, semáforo, reivindicação, persistência, shutdown. |
| `internal/observability/metrics.go` | Registry e servidor de `/metrics`. |
| `internal/config/config.go` | `METRICS_ADDR` restrito a loopback. |
| `cmd/vigilume/main.go` | Papéis com `errgroup`, partições, sondador, métricas. |

---

## Ordem das tarefas e seu motivo

1. **`safedial`**: pacote puro. A Task 6 precisa de `NewClientAllowing` para testar sondas contra `httptest`.
2. **Infra de storage**: o `testdb` não-super precisa existir **antes** da primeira política de RLS. Senão os testes de RLS nascem vacuamente verdes.
3. **Schema, sqlc e escopos**: a tabela e as duas portas da RLS, provadas no banco antes de qualquer consumidor.
4. **Domínio de alvos**: validação e CRUD sem HTTP.
5. **API de alvos**: expõe a Task 4.
6. **Uma sonda**: pura em relação ao banco; só precisa da Task 1.
7. **Agendador**: junta as Tasks 3, 4 e 6.
8. **Infra de métricas**: registry, pool e servidor em loopback.
9. **Wiring e fechamento**: `main.go`, teste ponta a ponta com o cliente de produção, e docs.

As Tasks 1 e 6 rodam sem Docker.

---
### Task 1: `safedial` — faixas de transição IPv6, zona antes do `Unmap`, e exceção de teste

Fecha R15, R22 e R23. É pacote puro e roda sem Docker.

**Files:**
- Modify: `internal/prober/safedial/safedial.go` (`blockedPrefixes` e `AllowIP`)
- Modify: `internal/prober/safedial/transport.go` (inteiro)
- Modify: `internal/prober/safedial/safedial_test.go` (casos novos em `TestAllowIP`)
- Modify: `internal/prober/safedial/transport_test.go` (apaga um teste, reescreve outro, acrescenta três)

**Interfaces:**
- Consumes: nada novo.
- Produces:
  - `safedial.NewClient(timeout time.Duration) *http.Client`, **assinatura inalterada**; `timeout` 0 = sem timeout de cliente (o sondador usa o do `context`).
  - `safedial.NewClientAllowing(timeout time.Duration, allowed ...netip.AddrPort) *http.Client`: igual a `NewClient`, mas cada `IP:porta` de `allowed` passa pelo dial mesmo estando na lista de bloqueio. **Entra em panic se algum endereço não for loopback.** Só testes chamam.
  - `safedial.NewTransport() *http.Transport`, inalterada.

**Contexto para quem implementa.** O `safedial` bloqueia loopback, e todo `httptest.Server` escuta em `127.0.0.1`. Por isso, até hoje, nenhum teste conseguiu seguir um redirecionamento: a requisição morria no salto zero, e dois testes do Plano 1 admitem isso nos comentários (R15). A exceção exata `IP:porta` resolve sem abrir faixa nenhuma. Liberar "loopback inteiro" seria errado: o teste de redirecionamento precisa que o **segundo** servidor, também em `127.0.0.1` mas em outra porta, continue bloqueado.

- [ ] **Step 1: Escrever os casos novos de `TestAllowIP` (falham)**

Em `internal/prober/safedial/safedial_test.go`, acrescente ao fim da tabela `cases` de `TestAllowIP`, antes do `}` que fecha o literal:

```go
		// R23 — formas de IPv4 dentro de IPv6 que Unmap() NÃO desfaz. Ele só
		// reconhece ::ffff:a.b.c.d; estas três atravessariam a lista.
		{"::127.0.0.1", true, "IPv4-compatível (obsoleto), ::/96"},
		{"::ffff:0:127.0.0.1", true, "IPv4-traduzido (SIIT), ::ffff:0:0:0/96"},

		// R22 — faixas de transição: um tradutor ou relay no caminho entrega
		// o IPv4 embutido, inclusive o privado.
		{"64:ff9b::127.0.0.1", true, "NAT64 bem conhecido embutindo loopback"},
		{"64:ff9b:1::a00:5", true, "NAT64 de uso local (RFC 8215)"},
		{"2002:7f00:1::1", true, "6to4 embutindo 127.0.0.1"},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", true, "Teredo, 2001::/32"},
		{"2001:db8::1", true, "documentação IPv6"},
		{"100::1", true, "descarte IPv6 (RFC 6666)"},
		{"192.88.99.1", true, "relay anycast 6to4 (obsoleto)"},

		// Travas contra bloqueio largo demais. Teredo é 2001:0000::/32, uma
		// /32 só. Quem "simplificar" para 2001::/16 derruba o Google.
		{"2001:4860:4860::8888", false, "DNS público do Google, fora do Teredo"},
		{"2003::1", false, "fora do 6to4"},

		// R23 — Unmap() DESCARTA a zona de um endereço mapeado: ::ffff:8.8.8.8%eth0
		// vira 8.8.8.8 limpo. Com a zona checada depois do Unmap, este passava,
		// contrariando a regra "alvo legítimo nunca carrega zona".
		{"::ffff:8.8.8.8%eth0", true, "IPv4 mapeado com zona"},
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/prober/safedial/ -run TestAllowIP -v`
Expected: FAIL em todos os casos novos marcados `true`. Os dois marcados `false` (`2001:4860:4860::8888` e `2003::1`) já passam: são travas contra bloqueio largo demais no Step 3.

- [ ] **Step 3: Implementar as faixas e mover a checagem de zona**

Em `internal/prober/safedial/safedial.go`, acrescente em `blockedPrefixes`, logo depois da linha `192.168.0.0/16`:

```go
	netip.MustParsePrefix("192.88.99.0/24"),  // relay anycast 6to4, obsoleto (RFC 7526)
```

e, logo depois da linha `ff00::/8` (fim da lista):

```go
	// IPv4-compatível (RFC 4291 §2.5.5.1, obsoleto): ::a.b.c.d. Unmap() NÃO
	// desfaz esta forma — só ::ffff:a.b.c.d —, então ::127.0.0.1 passaria.
	// Esta /96 também contém ::/128 e ::1/128 acima, que ficam pelo valor
	// documental, como o broadcast: cada um tem seu motivo explícito.
	netip.MustParsePrefix("::/96"),
	// IPv4-traduzido (RFC 2765, SIIT): ::ffff:0:a.b.c.d. Outra forma que
	// Unmap() não reconhece.
	netip.MustParsePrefix("::ffff:0:0:0/96"),
	// NAT64 (RFC 6052, RFC 8215): o tradutor da rede entrega o IPv4 embutido
	// nos 32 bits finais. 64:ff9b::7f00:1 é 127.0.0.1 visto através dele (R22).
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	// 6to4 (RFC 3056, obsoleto pela RFC 7526): 2002:AABB:CCDD::/48 embute o
	// IPv4 AA.BB.CC.DD, e um relay o alcança — inclusive se for privado (R22).
	// Bloquear a /16 inteira, em vez de decodificar o IPv4 embutido, é a
	// escolha fail-closed: nenhum alvo legítimo de hoje vive em 6to4.
	netip.MustParsePrefix("2002::/16"),
	// Teredo (RFC 4380): 2001:0000::/32 embute servidor e cliente IPv4 (R22).
	// É uma /32 só: 2001:4860::, do Google, é público e não pode cair aqui.
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"), // documentação IPv6
	netip.MustParsePrefix("100::/64"),      // descarte (RFC 6666)
```

Em `AllowIP`, troque o bloco que vai do comentário `// Unmap é indispensável` até o fim da checagem de zona por:

```go
	// A zona vem ANTES do Unmap, e a ordem importa (R23). netip.Prefix.Contains
	// devolve false para qualquer endereço com zona, então sem esta recusa
	// ::1%lo atravessaria a lista inteira. E Unmap() DESCARTA a zona de um
	// endereço mapeado: ::ffff:8.8.8.8%eth0 viraria 8.8.8.8 limpo e passaria
	// se a checagem viesse depois. Alvo legítimo de monitoramento nunca
	// carrega zona, então recusar é a resposta fail-closed.
	if ip.Zone() != "" {
		return fmt.Errorf("%w: zona IPv6 não é permitida (%s)", ErrBlocked, ip)
	}

	// Unmap é indispensável. ::ffff:127.0.0.1 é 127.0.0.1 vestido de IPv6:
	// sem desfazer o mapeamento, ele não casa com 127.0.0.0/8 e atravessa a
	// guarda inteira. É o bypass mais comum contra listas de bloqueio. As
	// outras formas de IPv4 dentro de IPv6, que Unmap não reconhece, estão
	// na lista como faixas próprias.
	ip = ip.Unmap()
```

No mesmo arquivo de teste, o comentário acima dos casos `::1%lo` e `fe80::1%eth0` diz que "Unmap() não a remove". Isso só vale para endereço não mapeado. Troque a frase por "e Unmap() a descarta quando o endereço é mapeado — por isso a zona é checada antes".

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/prober/safedial/ -run TestAllowIP -v`
Expected: PASS em todos os casos.

- [ ] **Step 5: Provar por sabotagem as faixas e a ordem**

Uma sabotagem de cada vez, com o teste rodado e a sabotagem desfeita antes da próxima:
1. Apague a linha `2001::/32`. O caso Teredo fica vermelho.
2. Troque `2001::/32` por `2001::/16`. O caso `2001:4860:4860::8888` fica vermelho.
3. Mova o bloco da zona para depois do `ip = ip.Unmap()`. O caso `::ffff:8.8.8.8%eth0` fica vermelho.
4. Apague `::/96`. O caso `::127.0.0.1` fica vermelho.

Registre as quatro no relatório.

- [ ] **Step 6: Escrever os testes de transporte (falham por não compilar)**

Em `internal/prober/safedial/transport_test.go`:

1. **Apague** `TestAlvoQueRedirecionaParaInternoMorreNoPrimeiroSalto` inteiro, com o comentário de cima. O teste que ele dizia ser impossível agora existe (`TestRedirectToAnotherInternalAddressIsBlockedAtTheHop`).
2. **Substitua** `TestTransportDoesNotReuseConnections` por `TestTransportIgnoresEnvironmentProxy` e acrescente os testes novos abaixo.

Acrescente aos imports: `"io"`, `"net"`, `"net/netip"`, `"sync/atomic"`.

```go
// enderecoDe devolve o IP:porta exato em que o servidor de teste escuta.
func enderecoDe(t *testing.T, srv *httptest.Server) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("endereço do servidor de teste: %v", err)
	}
	return ap
}

// Controle positivo: sem ele, todo teste abaixo que espera bloqueio passaria
// também se NewClientAllowing não liberasse nada. É o mesmo buraco do R15.
func TestAllowedAddressIsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, srv))
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("endereço liberado foi recusado: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, queria 204", resp.StatusCode)
	}
}

// R15, agora como comportamento. Reuso de conexão pula o dialer e, com ele,
// a revalidação do IP no ControlContext: se o keep-alive voltar, uma conexão
// aberta para um IP público serviria requisições depois de o DNS apontar
// para um interno. O servidor conta quantas conexões NOVAS viu.
func TestEachRequestOpensANewConnection(t *testing.T) {
	var novas atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			novas.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, srv))
	const pedidos = 3
	for range pedidos {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		// Ler o corpo até o fim é o que devolveria a conexão ao pool se o
		// keep-alive estivesse ligado. Sem isso, o teste passaria por acaso.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	if n := novas.Load(); n != pedidos {
		t.Errorf("%d conexões novas para %d requisições: houve reuso, e conexão reusada não passa pelo ControlContext", n, pedidos)
	}
}

// Um alvo liberado que redireciona para outro endereço interno. Os dois
// servidores escutam em 127.0.0.1, em portas diferentes; só o primeiro está
// liberado. O segundo salto tem de morrer no dial, e o segundo servidor não
// pode ter recebido nada.
func TestRedirectToAnotherInternalAddressIsBlockedAtTheHop(t *testing.T) {
	var atingido atomic.Int32
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atingido.Add(1)
		w.Write([]byte("segredo interno"))
	}))
	defer interno.Close()

	var redirecionou atomic.Int32
	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirecionou.Add(1)
		http.Redirect(w, r, interno.URL, http.StatusFound)
	}))
	defer redirecionador.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, redirecionador))

	resp, err := client.Get(redirecionador.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("o redirecionamento para outro endereço interno foi seguido")
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
	// Sem esta asserção o teste seria o do Plano 1 de novo: o bloqueio
	// poderia ter acontecido no salto zero, e o redirect nunca teria rodado.
	if redirecionou.Load() != 1 {
		t.Fatalf("o redirecionador recebeu %d requisições, queria 1: o bloqueio não aconteceu no salto", redirecionou.Load())
	}
	if n := atingido.Load(); n != 0 {
		t.Errorf("o servidor interno recebeu %d requisições", n)
	}
}

func TestAllowingPanicsForNonLoopback(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewClientAllowing aceitou 10.0.0.5: a porta de teste virou liberação de rede interna")
		}
	}()
	safedial.NewClientAllowing(time.Second, netip.MustParseAddrPort("10.0.0.5:80"))
}

// O Proxy continua verificado por configuração, e não por comportamento, e
// o motivo é concreto: http.ProxyFromEnvironment NUNCA manda ao proxy
// requisições para localhost ou 127.0.0.1. Um teste com HTTP_PROXY e
// httptest passaria com o Proxy ligado — seria o padrão vacuamente verde
// de novo. Fica a trava de configuração, com o motivo escrito.
func TestTransportIgnoresEnvironmentProxy(t *testing.T) {
	if safedial.NewTransport().Proxy != nil {
		t.Error("Proxy não é nil: o sondador herdaria HTTP_PROXY do ambiente, e o ControlContext validaria o IP do proxy em vez do IP do alvo")
	}
}
```

- [ ] **Step 7: Rodar e ver falhar**

Run: `go test ./internal/prober/safedial/ -v`
Expected: FAIL de compilação, `undefined: safedial.NewClientAllowing`.

- [ ] **Step 8: Implementar `NewClientAllowing`**

Substitua `internal/prober/safedial/transport.go` inteiro por:

```go
package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"syscall"
	"time"
)

// maxRedirects limita a cadeia de redirecionamentos. Três é suficiente para
// uso legítimo (http→https, com→com.br) e curto o bastante para não virar
// ferramenta de varredura.
const maxRedirects = 3

// MaxBodyBytes é quanto do corpo o sondador pode ler. O monitor não precisa
// do conteúdo; ler mais do que isso só criaria canal de exfiltração e pressão
// de memória.
const MaxBodyBytes = 64 << 10

// NewTransport devolve um http.Transport que se recusa a conectar em
// endereços internos.
//
// A validação acontece dentro de ControlContext, que o Go chama depois de
// criar o socket e antes de discar, recebendo o endereço já resolvido.
//
// Validar a URL antes de discar não bastaria: quem controla o DNS do domínio
// faz rebinding — a resolução da validação devolve um IP público e a da
// conexão devolve um privado. A janela entre as duas resoluções é o bug, e
// validar no ControlContext a fecha, porque ali o endereço é exatamente o que
// será conectado.
func NewTransport() *http.Transport { return newTransport(nil) }

func newTransport(allowed []netip.AddrPort) *http.Transport {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			if liberado(address, allowed) {
				return nil
			}
			return AllowAddr(address)
		},
	}

	return &http.Transport{
		DialContext: dialer.DialContext,

		// Sem proxy: HTTP_PROXY no ambiente faria toda requisição passar por
		// um intermediário, e o ControlContext validaria o IP do proxy em vez
		// do IP do alvo.
		Proxy: nil,

		// Sem keep-alive: conexão reusada não passa pelo dialer e, portanto,
		// não é revalidada. Cada sonda também deve medir uma conexão nova
		// para que a latência seja comparável entre execuções.
		// TestEachRequestOpensANewConnection trava isto por comportamento.
		DisableKeepAlives: true,

		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

// liberado diz se address está na lista de exceções exatas. O Unmap iguala
// 127.0.0.1 e ::ffff:127.0.0.1, que o sistema pode entregar em qualquer das
// duas formas.
func liberado(address string, allowed []netip.AddrPort) bool {
	if len(allowed) == 0 {
		return false
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return false
	}
	return slices.Contains(allowed, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
}

// NewClient monta o cliente completo, com limite de redirecionamentos e
// revalidação de esquema a cada salto. timeout 0 deixa o prazo por conta do
// context da requisição, que é como o sondador trabalha.
func NewClient(timeout time.Duration) *http.Client { return newClient(timeout, nil) }

// NewClientAllowing é NewClient com exceções EXATAS de IP:porta, que passam
// pelo dial mesmo estando na lista de bloqueio.
//
// Existe para teste: todo httptest.Server escuta em 127.0.0.1, que o
// safedial bloqueia, e sem esta porta nenhum teste do sondador conseguiria
// sondar coisa alguma. A exceção é por porta, e não por faixa, de propósito:
// um teste de redirecionamento precisa que OUTRA porta do mesmo 127.0.0.1
// continue bloqueada.
//
// Entra em panic com endereço que não seja loopback (D15). Nenhum código de
// produção chama esta função, e o panic garante que ela não vire, por
// engano, liberação de rede interna.
func NewClientAllowing(timeout time.Duration, allowed ...netip.AddrPort) *http.Client {
	norm := make([]netip.AddrPort, 0, len(allowed))
	for _, ap := range allowed {
		a := ap.Addr().Unmap()
		if !a.IsLoopback() {
			panic(fmt.Sprintf("safedial: NewClientAllowing só aceita loopback, recebeu %s", ap))
		}
		norm = append(norm, netip.AddrPortFrom(a, ap.Port()))
	}
	return newClient(timeout, norm)
}

func newClient(timeout time.Duration, allowed []netip.AddrPort) *http.Client {
	return &http.Client{
		Transport: newTransport(allowed),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// net/http já inclui a requisição original em via, então a
			// comparação é estrita: com maxRedirects = 3 o cliente segue
			// três saltos e para no quarto, que é o que a constante diz.
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: mais de %d redirecionamentos", ErrBlocked, maxRedirects)
			}
			// O IP do destino já será revalidado no dial, porque não há
			// reuso de conexão. O que falta checar aqui é o esquema: um
			// redirecionamento para file:// ou gopher:// nunca chega a discar.
			_, err := ValidateURL(req.URL.String())
			return err
		},
	}
}
```

- [ ] **Step 9: Rodar e ver passar**

Run: `go test -race ./internal/prober/safedial/ -v`
Expected: PASS, incluindo os três testes que já existiam (`TestClientCannotReachLoopbackServer`, `TestCheckRedirectRevalidaEsquemaELimitaSaltos` e os de `safedial_test.go`).

- [ ] **Step 10: Provar por sabotagem os testes de transporte**

1. `DisableKeepAlives: false` → `TestEachRequestOpensANewConnection` fica vermelho (1 conexão para 3 requisições).
2. Em `liberado`, devolva `ap.Addr().Unmap().IsLoopback()` (liberar loopback inteiro) → `TestRedirectToAnotherInternalAddressIsBlockedAtTheHop` fica vermelho, porque o servidor interno é atingido.
3. Em `liberado`, devolva sempre `false` → `TestAllowedAddressIsReachable` fica vermelho, e o teste de redirect também (o redirecionador recebe 0 requisições). Isso prova que a asserção `redirecionou == 1` morde.
4. Remova o `panic` → `TestAllowingPanicsForNonLoopback` fica vermelho.

Registre as quatro no relatório.

- [ ] **Step 11: Commit**

```bash
gofmt -l internal/ && go vet ./internal/prober/... && staticcheck ./internal/prober/...
git add internal/prober/safedial/
git commit -m "feat(safedial): faixas de transição IPv6, zona antes do Unmap e exceção exata para testes

Fecha R15, R22 e R23 do Plano 1."
```

---
### Task 2: Infra de storage — papel não-super nos testes, migrations com lock e `CheckRole`

Fecha o item 9b. Prepara o terreno para a RLS da Task 3.

**Files:**
- Modify: `internal/storage/migrate.go` (inteiro)
- Create: `internal/storage/role.go`
- Modify: `internal/storage/testdb/testdb.go` (inteiro)
- Create: `internal/storage/role_test.go`
- Modify: `internal/storage/migrate_test.go` (um teste novo)

**Interfaces:**
- Consumes: nada novo.
- Produces:
  - `storage.Migrate(ctx context.Context, dsn string) error`, **assinatura inalterada**.
  - `storage.ErrRoleBypassesRLS error` e `storage.CheckRole(ctx context.Context, pool *pgxpool.Pool) error`.
  - `testdb.New(t *testing.T) *pgxpool.Pool`, **inalterada por fora**. Por dentro, conecta como `vigilume_app`, não-super e dono das tabelas.
  - `testdb.DSN(t *testing.T) string`: DSN do papel da aplicação.
  - `testdb.AdminDSN(t *testing.T) string`: DSN do superusuário do contêiner.
  - `testdb.EmptyDatabase(t *testing.T) string`: DSN de um banco novo, **não migrado**, dono `vigilume_app`, apagado no fim do teste.

**Contexto para quem implementa.** Hoje o `testdb` conecta como `vigilume`, que é **superusuário** do contêiner. Superusuário ignora toda política de RLS, **mesmo com `FORCE`**. Se a Task 3 escrever as políticas com o `testdb` de hoje, todo teste de isolamento passa pelo `WHERE user_id` das queries e nunca pela política. Daria para apagar a política e a suíte ficaria verde. Por isso esta tarefa vem antes.

Três fatos foram provados num Postgres 16 real antes deste plano, e não precisam ser redescobertos:
- `citext` é extensão *trusted* desde o PG13: um papel comum com `CREATE` no banco a instala.
- O `Restore` do testcontainers recria o banco com `OWNER` = superusuário, mas as **tabelas** mantêm o dono que tinham no snapshot. Como as migrations rodam como `vigilume_app`, as tabelas continuam dele.
- O goose `Provider` lê a mesma tabela `goose_db_version` que o `goose.UpContext` antigo usava. Bancos já migrados continuam compatíveis.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/storage/role_test.go`:

```go
package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// Se este teste falhar, qualquer teste de RLS do repositório é suspeito: um
// pool de superusuário atravessa as políticas, e os testes de isolamento
// passariam pelo WHERE da query, nunca pela política.
func TestTestPoolDoesNotBypassRLS(t *testing.T) {
	pool := testdb.New(t)
	if err := storage.CheckRole(context.Background(), pool); err != nil {
		t.Fatalf("o pool de teste ignora RLS: %v", err)
	}
}

func TestCheckRoleRefusesSuperuser(t *testing.T) {
	ctx := context.Background()
	pool, err := storage.NewPool(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	err = storage.CheckRole(ctx, pool)
	if !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("CheckRole com superusuário = %v, queria ErrRoleBypassesRLS", err)
	}
}
```

Em `internal/storage/migrate_test.go`, acrescente `"sync"` aos imports e este teste:

```go
// Item 9b do Plano 1: com --role=api e --role=prober subindo ao mesmo tempo,
// os dois processos migram o mesmo banco. Sem o advisory lock, o segundo
// tenta aplicar a mesma migration e cai no boot. O banco é novo e vazio
// porque o de testdb.New já nasce migrado, e aí não haveria corrida.
func TestConcurrentMigrationsSerialize(t *testing.T) {
	dsn := testdb.EmptyDatabase(t)
	ctx := context.Background()

	const processos = 4
	var largada sync.WaitGroup
	largada.Add(1)
	erros := make(chan error, processos)
	for range processos {
		go func() {
			largada.Wait()
			erros <- storage.Migrate(ctx, dsn)
		}()
	}
	largada.Done()

	for range processos {
		if err := <-erros; err != nil {
			t.Errorf("migração concorrente falhou: %v", err)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/storage/ -run 'TestTestPoolDoesNotBypassRLS|TestCheckRoleRefusesSuperuser|TestConcurrentMigrationsSerialize' -v`
Expected: FAIL de compilação: `undefined: storage.CheckRole`, `testdb.AdminDSN`, `testdb.EmptyDatabase`.

- [ ] **Step 3: Implementar `CheckRole`**

Crie `internal/storage/role.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoleBypassesRLS = errors.New("o papel do banco ignora row-level security")

// CheckRole recusa subir com um papel que ignora RLS.
//
// Superusuário e BYPASSRLS atravessam as políticas mesmo com FORCE ROW LEVEL
// SECURITY. Rodar a aplicação com um deles não produz erro nenhum: só
// desliga, em silêncio, a segunda barreira de isolamento da spec §5.5. Por
// isso a checagem é no boot, e é erro, não aviso (D8).
func CheckRole(ctx context.Context, pool *pgxpool.Pool) error {
	var super, bypass bool
	err := pool.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		Scan(&super, &bypass)
	if err != nil {
		return fmt.Errorf("consultar papel do banco: %w", err)
	}
	if super || bypass {
		return fmt.Errorf("%w (rolsuper=%v, rolbypassrls=%v): conecte com um papel comum que seja dono das tabelas",
			ErrRoleBypassesRLS, super, bypass)
	}
	return nil
}
```

- [ ] **Step 4: Trocar o goose global pelo `Provider` com lock**

Substitua `internal/storage/migrate.go` inteiro por:

```go
// Package storage concentra o acesso ao Postgres: aplicação de migrations,
// construção do pool, escopos de RLS e os repositórios.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver "pgx" no database/sql
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// O //go:embed só enxerga arquivos dentro do diretório do pacote, por isso as
// migrations vivem aqui e não na raiz do repositório (desvio registrado nas
// Global Constraints).
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate aplica todas as migrations pendentes. É chamada no boot, o que
// elimina a classe de bug "esqueci de migrar em produção".
//
// O session locker (advisory lock do Postgres) serializa processos que
// migram ao mesmo tempo. Com instância única ele é inerte; com --role=api e
// --role=prober subindo juntos, sem ele os dois aplicam a mesma migration e
// o segundo cai no boot (item 9b do Plano 1, modo de falha medido: crash, não
// corrupção). O lock é de sessão, e o Postgres o solta sozinho se a conexão
// morrer: um processo derrubado no meio não tranca os outros.
//
// Usa database/sql em vez do pool pgx porque o goose exige *sql.DB.
func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir conexão para migrar: %w", err)
	}
	defer db.Close()

	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("abrir migrations embutidas: %w", err)
	}
	// Sonda o lock a cada segundo, por até dois minutos. O padrão do goose
	// (5s) faria cada réplica esperar cinco segundos a mais do que precisa.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 120))
	if err != nil {
		return fmt.Errorf("criar lock de migração: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("preparar migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("aplicar migrations: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Reescrever o `testdb` com o papel da aplicação**

Substitua `internal/storage/testdb/testdb.go` inteiro por:

```go
// Package testdb sobe um Postgres real para os testes de integração.
//
// A spec (§14) proíbe mock de banco: RLS, SKIP LOCKED e violação de
// constraint são invisíveis contra um mock, e são justamente o que precisa de
// teste. O contêiner sobe uma vez por execução de pacote; cada teste recebe o
// banco restaurado ao estado pós-migration via snapshot.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/BipoXDXD/vigilume/internal/storage"
)

const (
	dbName    = "vigilume_test"
	adminUser = "vigilume"
	adminPass = "vigilume"

	// appUser é o papel com que a aplicação roda nos testes, e existe por
	// causa da RLS. O usuário do contêiner é superusuário, e superusuário
	// atravessa toda política de RLS, mesmo com FORCE. Com ele, um teste de
	// isolamento passaria pelo WHERE da query e nunca pela política, e
	// apagar a política deixaria a suíte verde. As migrations rodam como
	// appUser para que ele seja o DONO das tabelas, exatamente como em
	// produção (D8).
	appUser = "vigilume_app"
	appPass = "vigilume_app"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	adminDSN  string
	appDSN    string
	startErr  error
	vazios    atomic.Int64
)

// New devolve um pool ligado a um Postgres limpo e migrado, como o papel
// da aplicação.
//
// Pula com -short: quem não tem Docker disponível ainda consegue rodar os
// testes dos pacotes puros (crypto, safedial, password) com `make test-short`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	iniciar(t)

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, appDSN)
	if err != nil {
		t.Fatalf("conectar ao Postgres de teste: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Restore precisa de zero conexões abertas no banco alvo, por isso
		// vem depois do Close.
		if err := container.Restore(ctx); err != nil {
			t.Fatalf("restaurar snapshot: %v", err)
		}
	})
	return pool
}

// DSN é a string de conexão do papel da aplicação, para quem precisa passar
// pelo caminho de boot (storage.Migrate, run) em vez de receber um pool
// pronto. Chame New antes no mesmo teste: é o cleanup de New que restaura o
// snapshot quando o teste termina.
func DSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	return appDSN
}

// AdminDSN é a conexão como superusuário. Serve para provar que o boot
// RECUSA esse papel e para ler o banco "por fora" da RLS, como faria quem
// tivesse um dump.
func AdminDSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	return adminDSN
}

// EmptyDatabase cria um banco novo, vazio e NÃO migrado, cujo dono é o
// papel da aplicação, e o apaga no fim do teste. O teste de migrations
// concorrentes precisa dele, porque o banco de New já nasce migrado.
func EmptyDatabase(t *testing.T) string {
	t.Helper()
	iniciar(t)
	ctx := context.Background()

	// O nome cru vai na URL; o sanitizado, entre aspas, no SQL. Os dois saem
	// da MESMA leitura do contador: relê-lo com testes em paralelo devolveria
	// o número de outro teste.
	cru := fmt.Sprintf("vigilume_vazio_%d", vazios.Add(1))
	nome := pgx.Identifier{cru}.Sanitize()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	defer admin.Close(ctx)

	// #nosec G201 -- nome vem de pgx.Identifier.Sanitize sobre um contador; nada vem de fora.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, nome, appUser)); err != nil {
		t.Fatalf("criar banco vazio: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			t.Errorf("conectar para apagar banco vazio: %v", err)
			return
		}
		defer c.Close(context.Background())
		// #nosec G201 -- mesmo identificador sanitizado acima.
		if _, err := c.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, nome)); err != nil {
			t.Errorf("apagar banco vazio: %v", err)
		}
	})

	u, err := url.Parse(appDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cru
	return u.String()
}

func iniciar(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("precisa de Docker; pulado em -short")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("subir Postgres de teste: %v", startErr)
	}
}

func start() {
	ctx := context.Background()

	container, startErr = tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(adminUser),
		tcpostgres.WithPassword(adminPass),
		tcpostgres.WithSQLDriver("pgx"), // exigido por Snapshot/Restore
		tcpostgres.BasicWaitStrategies(),
	)
	if startErr != nil {
		return
	}

	adminDSN, startErr = container.ConnectionString(ctx, "sslmode=disable")
	if startErr != nil {
		return
	}
	if startErr = criarPapelDaAplicacao(ctx); startErr != nil {
		return
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		startErr = err
		return
	}
	u.User = url.UserPassword(appUser, appPass)
	appDSN = u.String()

	// O snapshot é tirado com o schema já migrado e sem dados, então cada
	// teste começa exatamente daqui.
	if startErr = storage.Migrate(ctx, appDSN); startErr != nil {
		return
	}
	startErr = container.Snapshot(ctx)
}

func criarPapelDaAplicacao(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("conectar como admin: %w", err)
	}
	defer conn.Close(ctx)

	for _, q := range []string{
		`CREATE ROLE ` + appUser + ` LOGIN PASSWORD '` + appPass + `' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`,
		// CREATE no banco: a migration 00001 instala citext, que é trusted
		// desde o PG13 e por isso só exige este privilégio.
		`GRANT CREATE ON DATABASE ` + dbName + ` TO ` + appUser,
		// Desde o PG15 o schema public não dá CREATE a PUBLIC.
		`GRANT CREATE ON SCHEMA public TO ` + appUser,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("preparar papel da aplicação: %w", err)
		}
	}
	return nil
}

// Terminate derruba o contêiner. Chame de um TestMain quando quiser
// encerramento explícito; o Ryuk do testcontainers já limpa sozinho depois.
func Terminate() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `go test -race -count=1 ./internal/storage/... -v`
Expected: PASS nos três testes novos e nos três que já existiam.

Run: `go test -race -count=1 ./...`
Expected: PASS em todos os pacotes. Os testes de `auth` e `httpapi` inserem em `users` e `sessions` como o papel novo, que é dono dessas tabelas e não sofre RLS nelas.

- [ ] **Step 7: Provar por sabotagem**

1. Em `testdb.New`, conecte com `adminDSN` em vez de `appDSN` → `TestTestPoolDoesNotBypassRLS` fica vermelho.
2. Faça `CheckRole` devolver `nil` sempre → `TestCheckRoleRefusesSuperuser` fica vermelho.
3. Remova `goose.WithSessionLocker(locker)` e rode `go test -race -count=10 ./internal/storage/ -run TestConcurrentMigrationsSerialize`. Registre **quantas das 10** ficaram vermelhas e **qual erro** apareceu. Uma corrida pode ficar verde por sorte. Se as 10 ficarem verdes, suba `processos` para 8 e repita. Se nem assim a falha aparecer, **não finja**: registre no relatório que o teste não demonstrou a falha nesta máquina, com os números.

- [ ] **Step 8: Commit**

```bash
gofmt -l internal/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
git add internal/storage/
git commit -m "feat(storage): migrations com advisory lock e testes como papel não-super

Fecha o item 9b do Plano 1. O testdb deixa de conectar como superusuário,
que atravessaria a RLS da próxima tarefa e deixaria os testes de isolamento
vacuamente verdes. CheckRole recusa o boot com papel que ignora RLS."
```

---
### Task 3: Schema de alvos e checks, sqlc, escopos de RLS e partições

**Files:**
- Create: `internal/storage/migrations/00004_targets_checks.sql`
- Create: `sqlc.yaml`
- Create: `internal/storage/queries/targets.sql`
- Create: `internal/storage/queries/prober.sql`
- Create (gerado): `internal/storage/db/db.go`, `models.go`, `targets.sql.go`, `prober.sql.go`
- Create: `internal/storage/scope.go`
- Create: `internal/storage/partitions.go`
- Create: `internal/storage/helpers_test.go`, `internal/storage/rls_test.go`, `internal/storage/partitions_test.go`
- Modify: `internal/storage/migrate_test.go` (`TestMigrationsApplyAndSchemaIsUsable`)
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `testdb.New`, `testdb.AdminDSN` (Task 2).
- Produces:
  - Pacote gerado `github.com/BipoXDXD/vigilume/internal/storage/db`, com `db.New(db.DBTX) *db.Queries`, os modelos `db.Target` e `db.ClaimDueTargetsRow` e os métodos listados no Step 4. **Os nomes abaixo são exatamente os que o sqlc 1.31.1 gera com este `sqlc.yaml`**; foram gerados antes de o plano ser escrito.
  - `storage.Beginner` (interface com `Begin(context.Context) (pgx.Tx, error)`), satisfeita por `*pgxpool.Pool` e `*pgxpool.Conn`.
  - `storage.InUserTx(ctx, b Beginner, userID uuid.UUID, fn func(*db.Queries) error) error` e `storage.ErrNoUser`.
  - `storage.InProberTx(ctx, b Beginner, fn func(*db.Queries) error) error`.
  - `storage.EnsureChecksPartitions(ctx, pool *pgxpool.Pool, now time.Time) error`.
  - `storage.MaintainChecksPartitions(ctx, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error`.

**Contexto para quem implementa.** Tudo o que esta tarefa afirma sobre o Postgres foi executado num Postgres 16 antes do plano:
- **`SET LOCAL` deixa `''`.** Depois do primeiro `SET LOCAL` de uma sessão, `current_setting('app.user_id', true)` devolve `''`, e não `NULL`. Por isso as políticas usam `NULLIF(..., '')`.
- **Política única barra o sondador.** Com uma política só, o `WITH CHECK` de dono barra o `UPDATE` do sondador. Por isso são três políticas em `targets`.
- **RLS delegada funciona.** A de `checks` por `EXISTS` no alvo funciona, e a cascata de `DELETE` a atravessa.
- **Check de alvo inexistente falha na RLS.** O `INSERT` de check de alvo inexistente falha com `42501` (RLS), **antes** da FK (`23503`).
- **Bordas das partições em UTC.** A função grava as bordas em UTC mesmo com a sessão em `America/Sao_Paulo`.
- **Jitter conferido.** Com intervalo de 60s, a reivindicação deu 200 valores distintos em 200 alvos, entre 54,3s e 66,0s.

- [ ] **Step 1: Instalar o sqlc**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc version   # v1.31.1
```

O sqlc usa cgo (parser `pg_query`); `gcc` precisa estar no PATH.

- [ ] **Step 2: Escrever a migration**

Crie `internal/storage/migrations/00004_targets_checks.sql`:

```sql
-- +goose Up
-- Alvos de monitoramento (spec §4.1). Cada CHECK tem par na validação de
-- internal/targets: a validação existe para devolver o campo culpado, e o
-- CHECK para que nenhum caminho que esqueça de validar grave lixo.
CREATE TABLE targets (
    id                    uuid PRIMARY KEY,
    user_id               uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                  text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    url                   text NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2048),
    method                text NOT NULL CHECK (method IN ('GET', 'HEAD', 'POST')),
    interval_sec          int  NOT NULL CHECK (interval_sec BETWEEN 30 AND 86400),
    timeout_ms            int  NOT NULL CHECK (timeout_ms BETWEEN 1000 AND 30000),
    expected_status       int  NOT NULL CHECK (expected_status BETWEEN 100 AND 599),
    headers_enc           bytea,
    degraded_threshold_ms int  CHECK (degraded_threshold_ms BETWEEN 1 AND 60000),
    enabled               bool NOT NULL DEFAULT true,
    next_run_at           timestamptz NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    -- O jitter de -10% pode antecipar a próxima execução para 90% do
    -- intervalo; um timeout acima disso deixaria duas sondas do mesmo alvo
    -- em voo ao mesmo tempo (D3).
    CHECK (timeout_ms * 10 < interval_sec * 9000)
);

CREATE INDEX targets_user_idx ON targets (user_id, id DESC);
-- Índice parcial: a reivindicação só procura alvos ativos e vencidos.
CREATE INDEX targets_due_idx ON targets (next_run_at) WHERE enabled;

-- RLS (spec §5.5). FORCE porque a aplicação conecta como DONA da tabela, e o
-- dono ignora RLS sem ele. Superusuário ignora mesmo com FORCE — por isso o
-- boot recusa esse papel (storage.CheckRole).
--
-- NULLIF é obrigatório. Depois do primeiro SET LOCAL de uma sessão,
-- current_setting('app.user_id', true) passa a devolver '' — e não NULL —
-- naquela sessão. ''::uuid explode com "invalid input syntax for type uuid",
-- e num pool, que reusa conexões, isso derrubaria a primeira consulta sem
-- escopo depois de qualquer transação de usuário. Um teste com conexão nova
-- nunca vê isso; TestScopeSurvivesConnectionReuse vê.
ALTER TABLE targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE targets FORCE ROW LEVEL SECURITY;

CREATE POLICY targets_owner ON targets
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O sondador lê e reagenda alvos de todos os usuários, e nada mais: sem
-- política de INSERT nem de DELETE para ele. São duas políticas, e não uma
-- FOR ALL, porque o WITH CHECK de dono barraria o UPDATE do sondador.
CREATE POLICY targets_prober_select ON targets FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY targets_prober_update ON targets FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober');

-- Resultados de sonda, particionados por mês (spec §4.2): retenção por
-- DROP da partição, em vez de DELETE com bloat.
CREATE TABLE checks (
    id          uuid NOT NULL,
    target_id   uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    checked_at  timestamptz NOT NULL,
    status      text NOT NULL CHECK (status IN ('up', 'degraded', 'down')),
    latency_ms  int  NOT NULL CHECK (latency_ms >= 0),
    status_code int,
    -- 'unknown' é desvio registrado da spec §4.2 (D1).
    error_kind  text CHECK (error_kind IN ('timeout', 'dns', 'tls', 'conn_refused',
                                           'http_status', 'body_limit', 'blocked', 'unknown')),
    -- A chave de partição precisa entrar na PK.
    PRIMARY KEY (id, checked_at),
    -- Toda falha tem motivo, e só falha tem motivo.
    CHECK ((status = 'down') = (error_kind IS NOT NULL))
) PARTITION BY RANGE (checked_at);

CREATE INDEX checks_target_at_idx ON checks (target_id, checked_at DESC);

-- checks não tem user_id: a RLS delega ao alvo. O EXISTS roda sob as
-- políticas de targets, então quem não enxerga o alvo não enxerga os
-- checks dele. Vale para leitura E para escrita: um check de alvo
-- inexistente falha aqui (42501), antes da FK (23503).
--
-- A política vale para consultas pela tabela-mãe. Partições não herdam RLS;
-- a aplicação nunca consulta uma partição diretamente.
ALTER TABLE checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE checks FORCE ROW LEVEL SECURITY;

CREATE POLICY checks_via_target ON checks
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_id));

-- Cria a partição do mês que contém p_month. As bordas são instantes em UTC,
-- independentes do TimeZone da sessão: se dois boots discordassem sobre onde
-- o mês começa, o segundo tentaria criar uma faixa sobreposta e cairia.
-- +goose StatementBegin
CREATE FUNCTION ensure_checks_partition(p_month date) RETURNS void
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

-- +goose Down
DROP FUNCTION ensure_checks_partition(date);
DROP TABLE checks;
DROP TABLE targets;
```

- [ ] **Step 3: Escrever o `sqlc.yaml`**

Crie `sqlc.yaml` na raiz do repositório:

```yaml
version: "2"
sql:
  - engine: postgresql
    # O sqlc lê as migrations do goose e ignora as seções Down.
    schema: internal/storage/migrations
    queries: internal/storage/queries
    gen:
      go:
        package: db
        out: internal/storage/db
        sql_package: pgx/v5
        emit_pointers_for_null_types: true
        # Gera URL, TimeoutMS e LatencyMS em vez de Url, TimeoutMs, LatencyMs.
        initialisms: [id, url, ms]
        overrides:
          - db_type: uuid
            go_type: github.com/google/uuid.UUID
          - db_type: uuid
            nullable: true
            go_type:
              import: github.com/google/uuid
              type: UUID
              pointer: true
          - db_type: timestamptz
            go_type: time.Time
          - db_type: timestamptz
            nullable: true
            go_type:
              type: time.Time
              pointer: true
          - db_type: date
            go_type: time.Time
```

- [ ] **Step 4: Escrever as queries e gerar**

Crie `internal/storage/queries/targets.sql`:

```sql
-- Toda query daqui filtra por user_id E roda sob a RLS de InUserTx. As duas
-- barreiras são independentes de propósito (spec §5.5): um WHERE esquecido
-- é pego pela política, e uma política desligada é pega pelo WHERE.

-- name: InsertTarget :exec
INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
                     expected_status, headers_enc, degraded_threshold_ms, enabled,
                     next_run_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetTarget :one
SELECT * FROM targets WHERE user_id = $1 AND id = $2;

-- name: GetTargetForUpdate :one
SELECT * FROM targets WHERE user_id = $1 AND id = $2 FOR UPDATE;

-- Paginação por cursor (spec §9.1): o id é UUIDv7, ordenável por tempo, e o
-- cursor é o último id da página anterior.
-- name: ListTargets :many
SELECT * FROM targets
 WHERE user_id = sqlc.arg(user_id)
   AND (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before)::uuid)
 ORDER BY id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- name: UpdateTarget :exec
UPDATE targets
   SET name = $3, url = $4, method = $5, interval_sec = $6, timeout_ms = $7,
       expected_status = $8, headers_enc = $9, degraded_threshold_ms = $10,
       enabled = $11, next_run_at = $12
 WHERE user_id = $1 AND id = $2;

-- name: DeleteTarget :execrows
DELETE FROM targets WHERE user_id = $1 AND id = $2;

-- name: ListChecks :many
SELECT c.checked_at, c.status, c.latency_ms, c.status_code, c.error_kind
  FROM checks c
  JOIN targets t ON t.id = c.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND c.target_id = sqlc.arg(target_id)
   AND c.checked_at >= sqlc.arg(from_at)
   AND c.checked_at < sqlc.arg(to_at)
 ORDER BY c.checked_at;
```

Crie `internal/storage/queries/prober.sql`:

```sql
-- Estas queries rodam só sob InProberTx (app.scope = 'prober').

-- Reivindicação com lease (spec §6.1). O UPDATE grava o próximo next_run_at,
-- com jitter de ±10%, NA MESMA instrução que trava as linhas; o commit
-- acontece antes de discar. Segurar a transação durante a sonda não caberia:
-- o pool tem 10 conexões e o semáforo aceita 64 sondas.
--
-- SKIP LOCKED faz um segundo sondador pegar OUTROS alvos em vez de esperar
-- pelos que o primeiro travou (TestSkipLockedDoesNotWaitForAnotherProber).
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
RETURNING t.id, t.url, t.method, t.timeout_ms, t.expected_status,
          t.headers_enc, t.degraded_threshold_ms;

-- name: CountDueTargets :one
SELECT count(*) FROM targets
 WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz;

-- name: InsertCheck :exec
INSERT INTO checks (id, target_id, checked_at, status, latency_ms, status_code, error_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: EnsureChecksPartition :exec
SELECT ensure_checks_partition(sqlc.arg(month)::date);
```

Gere:

```bash
sqlc generate
ls internal/storage/db   # db.go models.go prober.sql.go targets.sql.go
go build ./...
```

Confira que os tipos gerados são estes. **Se algum nome divergir, pare e escale, não adapte o plano em silêncio:** as Tasks 4 e 7 dependem deles.

```go
type Target struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	HeadersEnc          []byte
	DegradedThresholdMS *int32
	Enabled             bool
	NextRunAt           time.Time
	CreatedAt           time.Time
}
type ClaimDueTargetsRow struct {
	ID                  uuid.UUID
	URL                 string
	Method              string
	TimeoutMS           int32
	ExpectedStatus      int32
	HeadersEnc          []byte
	DegradedThresholdMS *int32
}
type ListChecksRow struct {
	CheckedAt  time.Time
	Status     string
	LatencyMS  int32
	StatusCode *int32
	ErrorKind  *string
}
// InsertTargetParams: os 13 campos de Target, na ordem do INSERT.
// UpdateTargetParams: UserID, ID, e os 10 campos do SET, com NextRunAt.
// GetTargetParams, GetTargetForUpdateParams, DeleteTargetParams: {UserID, ID}
// ListTargetsParams: {UserID uuid.UUID; Before *uuid.UUID; MaxRows int32}
// ListChecksParams: {UserID, TargetID uuid.UUID; FromAt, ToAt time.Time}
// ClaimDueTargetsParams: {Now time.Time; MaxRows int32}
// InsertCheckParams: {ID, TargetID uuid.UUID; CheckedAt time.Time; Status string;
//                     LatencyMS int32; StatusCode *int32; ErrorKind *string}
// Métodos: InsertTarget, GetTarget, GetTargetForUpdate, ListTargets, UpdateTarget,
//          DeleteTarget (int64, error), ListChecks, ClaimDueTargets,
//          CountDueTargets(ctx, now time.Time) (int64, error),
//          InsertCheck, EnsureChecksPartition(ctx, month time.Time) error
```

- [ ] **Step 5: Escrever as portas de escopo e as partições**

Crie `internal/storage/scope.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Beginner é o mínimo que uma transação com escopo precisa. *pgxpool.Pool e
// *pgxpool.Conn satisfazem; aceitar a conexão avulsa é o que permite provar
// o comportamento numa conexão REUSADA (TestScopeSurvivesConnectionReuse).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var ErrNoUser = errors.New("transação de usuário sem usuário")

// InUserTx roda fn numa transação em que a RLS só enxerga as linhas de userID.
//
// set_config(..., true) é o SET LOCAL: o valor morre no fim da transação e
// não vaza para o próximo uso da conexão no pool. Ver a migration 00004 para
// o motivo do NULLIF nas políticas.
func InUserTx(ctx context.Context, b Beginner, userID uuid.UUID, fn func(*db.Queries) error) error {
	if userID == uuid.Nil {
		// uuid.Nil nunca é usuário. Aceitá-lo produziria uma transação que
		// não enxerga nada, e o erro apareceria longe daqui, como "não
		// encontrado".
		return ErrNoUser
	}
	return inTx(ctx, b, `SELECT set_config('app.user_id', $1, true)`, userID.String(), fn)
}

// InProberTx roda fn com o escopo do sondador: lê e reagenda alvos de todos
// os usuários e grava checks, mas não cria nem apaga alvos (políticas
// targets_prober_*). É o ÚNICO lugar do código que liga esse escopo.
func InProberTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "prober", fn)
}

func inTx(ctx context.Context, b Beginner, setSQL, valor string, fn func(*db.Queries) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, err := tx.Exec(ctx, setSQL, valor); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Crie `internal/storage/partitions.go`:

```go
package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// EnsureChecksPartitions garante as partições de checks do mês corrente e do
// seguinte. É idempotente.
//
// O mês é calculado em UTC, como as bordas que a função SQL grava. Às 23:30
// do dia 30 em São Paulo já é dia 1 em UTC, e a sonda gravada naquele
// instante pertence à partição do mês NOVO. Calcular no fuso local criaria
// a partição errada, e o INSERT falharia com "no partition of relation".
func EnsureChecksPartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	u := now.UTC()
	atual := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	q := db.New(pool)
	for _, mes := range []time.Time{atual, atual.AddDate(0, 1, 0)} {
		if err := q.EnsureChecksPartition(ctx, mes); err != nil {
			return fmt.Errorf("criar partição de checks %s: %w", mes.Format("2006-01"), err)
		}
	}
	return nil
}

// MaintainChecksPartitions repete EnsureChecksPartitions até ctx acabar. A
// partição do mês seguinte existe desde o boot, então um tick diário tem um
// mês inteiro de folga. O Plano 4 acrescenta aqui o DROP das partições
// vencidas (spec §12).
func MaintainChecksPartitions(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil && ctx.Err() == nil {
				slog.Error("manter partições de checks", "erro", err)
			}
		}
	}
}
```

- [ ] **Step 6: Escrever os testes de RLS e de partição (devem passar de primeira; a prova vem no Step 8)**

Estes testes afirmam o comportamento de um schema que o Step 2 já criou. Eles não têm fase vermelha natural. A prova de que mordem é a sabotagem do Step 8, que é **obrigatória**.

Crie `internal/storage/helpers_test.go`:

```go
package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var agora = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// users não tem RLS; a aplicação é dona da tabela.
func novoUsuario(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func novoAlvo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, proximo time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: "alvo", URL: "https://exemplo.com",
			Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: proximo, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatalf("criar alvo: %v", err)
	}
	return id
}

func novoCheck(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, em time.Time) {
	t.Helper()
	ctx := context.Background()
	code := int32(200)
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em,
			Status: "up", LatencyMS: 12, StatusCode: &code,
		})
	})
	if err != nil {
		t.Fatalf("gravar check: %v", err)
	}
}

// contarComEscopo roda um SELECT count(*) SEM WHERE sob o escopo de um
// usuário: é a consulta que "esqueceu o filtro", e só a política a contém.
func contarComEscopo(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, tabela string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	// tabela é sempre um literal do próprio teste.
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
```

Crie `internal/storage/rls_test.go`:

```go
package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// A consulta que esqueceu o filtro, e a que filtrou pelo usuário errado:
// só a política separa os dois donos.
func TestRLSIsolatesTargetsEvenWithTheWrongFilter(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora)
	alvoB := novoAlvo(t, pool, b, agora)

	if n := contarComEscopo(t, pool, a, "targets"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d alvos, queria 1", n)
	}

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		daB, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: b, MaxRows: 10})
		if err != nil {
			return err
		}
		if len(daB) != 0 {
			t.Errorf("sob o escopo de A, listar os alvos de B devolveu %d", len(daB))
		}
		_, err = q.GetTarget(ctx, db.GetTargetParams{UserID: b, ID: alvoB})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("sob o escopo de A, buscar o alvo de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithoutScopeNothingIsVisible(t *testing.T) {
	pool := testdb.New(t)
	novoAlvo(t, pool, novoUsuario(t, pool), agora)

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM targets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sem escopo, a aplicação viu %d alvos: a RLS não está forçada para o dono da tabela", n)
	}
}

// Review Focus 1. Depois do primeiro SET LOCAL da sessão, o GUC volta como
// '' e não NULL. Um pool reusa a conexão; a próxima consulta nela precisa
// funcionar.
func TestScopeSurvivesConnectionReuse(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	novoAlvo(t, pool, dono, agora.Add(-time.Minute))

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	err = storage.InUserTx(ctx, conn, dono, func(q *db.Queries) error {
		_, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: dono, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A premissa do teste. Se o Postgres um dia devolver NULL aqui, este
	// teste deixa de provar alguma coisa, e precisa dizer isso.
	var vazio bool
	if err := conn.QueryRow(ctx, `SELECT current_setting('app.user_id', true) = ''`).Scan(&vazio); err != nil {
		t.Fatal(err)
	}
	if !vazio {
		t.Fatal("premissa quebrada: o GUC não ficou '' depois do SET LOCAL; revise este teste")
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&n); err != nil {
		t.Fatalf("consulta sem escopo numa conexão reusada falhou: %v", err)
	}
	if n != 0 {
		t.Errorf("sem escopo viu %d alvos", n)
	}

	err = storage.InProberTx(ctx, conn, func(q *db.Queries) error {
		_, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatalf("o sondador falhou numa conexão reusada: %v", err)
	}
}

func TestProberCanRescheduleButNotCreate(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora.Add(-time.Minute))
	novoAlvo(t, pool, b, agora.Add(-time.Minute))

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		got, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Errorf("o sondador reivindicou %d alvos, queria os 2, de dois donos", len(got))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: uuid.Must(uuid.NewV7()), UserID: a, Name: "x", URL: "https://x",
			Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o sondador criou um alvo (erro = %v); queria violação de RLS 42501", err)
	}
}

func TestOwnerCannotHandTargetToAnotherUser(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE targets SET user_id = $1 WHERE id = $2`, b, alvo)
	if codigoPG(err) != "42501" {
		t.Errorf("A entregou o alvo a B (erro = %v); queria violação de RLS 42501", err)
	}
}

func TestChecksAreVisibleOnlyThroughTheirTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	novoCheck(t, pool, alvoA, agora)
	novoCheck(t, pool, alvoB, agora)

	if n := contarComEscopo(t, pool, a, "checks"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d checks, queria 1", n)
	}
	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		rows, err := q.ListChecks(ctx, db.ListChecksParams{
			UserID: b, TargetID: alvoB, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Errorf("sob o escopo de A, os checks de B vieram (%d)", len(rows))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// O sondador depende deste código para reconhecer "o alvo sumiu no meio da
// sonda" (Task 7). O natural seria esperar a FK (23503); a RLS chega antes.
func TestCheckForVanishedTargetFailsWithRLSCode(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: uuid.Must(uuid.NewV7()),
			CheckedAt: agora, Status: "up", LatencyMS: 1,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("check de alvo inexistente deu %v; queria 42501", err)
	}
}

// SKIP LOCKED, e não só FOR UPDATE: sem ele, o segundo sondador ESPERA o
// primeiro soltar as linhas — não duplica, mas trava. O prazo curto
// transforma a espera em falha.
func TestSkipLockedDoesNotWaitForAnotherProber(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	for range 10 {
		novoAlvo(t, pool, dono, agora.Add(-time.Minute))
	}

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	if _, err := tx1.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	primeiros, err := db.New(tx1).ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 5})
	if err != nil {
		t.Fatal(err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var segundos []db.ClaimDueTargetsRow
	err = storage.InProberTx(ctx2, pool, func(q *db.Queries) error {
		var err error
		segundos, err = q.ClaimDueTargets(ctx2, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatalf("o segundo sondador esperou o primeiro: %v", err)
	}

	vistos := map[uuid.UUID]bool{}
	for _, r := range primeiros {
		vistos[r.ID] = true
	}
	for _, r := range segundos {
		if vistos[r.ID] {
			t.Errorf("alvo %s reivindicado pelos dois sondadores", r.ID)
		}
	}
	if len(primeiros)+len(segundos) != 10 {
		t.Errorf("reivindicados %d+%d, queria 10 no total", len(primeiros), len(segundos))
	}
}

func codigoPG(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
```

Crie `internal/storage/partitions_test.go`:

```go
package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// Review Focus 2. 23:30 do dia 30 em São Paulo já é dia 1 em UTC.
func TestEnsureChecksPartitionsUsesUTCMonths(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	sp := time.FixedZone("BRT", -3*60*60)

	if err := storage.EnsureChecksPartitions(ctx, pool, time.Date(2026, 9, 30, 23, 30, 0, 0, sp)); err != nil {
		t.Fatal(err)
	}
	for nome, quer := range map[string]bool{
		"checks_2026_09": false,
		"checks_2026_10": true,
		"checks_2026_11": true,
	} {
		var existe bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1 AND relispartition)`, nome).
			Scan(&existe); err != nil {
			t.Fatal(err)
		}
		if existe != quer {
			t.Errorf("partição %s existe=%v, queria %v", nome, existe, quer)
		}
	}

	// Idempotente: o tick diário chama de novo sem erro.
	if err := storage.EnsureChecksPartitions(ctx, pool, time.Date(2026, 9, 30, 23, 30, 0, 0, sp)); err != nil {
		t.Errorf("segunda chamada falhou: %v", err)
	}
}

// Review Focus 2, pelo lado do banco. A função é chamada numa sessão com
// TimeZone de São Paulo; as bordas precisam ser meia-noite UTC.
func TestPartitionBoundsAreUTCRegardlessOfSessionTimeZone(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SET TimeZone = 'America/Sao_Paulo'`,
		`SELECT ensure_checks_partition('2026-09-15')`,
		`SELECT ensure_checks_partition('2026-10-15')`,
		`RESET TimeZone`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Release()

	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	novoCheck(t, pool, alvo, time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC))
	novoCheck(t, pool, alvo, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT tableoid::regclass::text FROM checks ORDER BY checked_at`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"checks_2026_09", "checks_2026_10"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("partições = %v, queria %v: as bordas seguiram o fuso da sessão", got, want)
	}
}
```

Em `internal/storage/migrate_test.go`, em `TestMigrationsApplyAndSchemaIsUsable`, troque a lista de tabelas por `[]string{"users", "sessions", "audit_log", "totp_recovery_codes", "targets", "checks"}` e acrescente ao fim da função:

```go
	// Sem FORCE, o dono da tabela — que é o papel da aplicação — ignora a
	// RLS inteira. As políticas continuariam lá, e não valeriam nada.
	for _, tabela := range []string{"targets", "checks"} {
		var ligada, forcada bool
		err := pool.QueryRow(ctx,
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1`, tabela).
			Scan(&ligada, &forcada)
		if err != nil {
			t.Fatalf("consultar RLS de %s: %v", tabela, err)
		}
		if !ligada || !forcada {
			t.Errorf("%s: row security ligada=%v forçada=%v, queria as duas", tabela, ligada, forcada)
		}
	}
```

- [ ] **Step 7: Rodar**

Run: `go test -race -count=1 ./internal/storage/... -v`
Expected: PASS em todos.

- [ ] **Step 8: Provar por sabotagem (obrigatório: estes testes nasceram verdes)**

Rode o pacote depois de cada sabotagem e desfaça antes da próxima. Para sabotar a migration, edite o `.sql` e rode de novo: o `testdb` migra do zero a cada execução de pacote.

| Sabotagem | Teste que precisa ficar vermelho |
|---|---|
| Remover `ALTER TABLE targets FORCE ROW LEVEL SECURITY` | `TestWithoutScopeNothingIsVisible`, `TestRLSIsolatesTargetsEvenWithTheWrongFilter`, `TestMigrationsApplyAndSchemaIsUsable` |
| Trocar `NULLIF(current_setting('app.user_id', true), '')::uuid` por `current_setting('app.user_id', true)::uuid` nas duas linhas de `targets_owner` | `TestScopeSurvivesConnectionReuse` |
| Fundir as três políticas de `targets` numa só, com `USING (... OR scope = 'prober')` e o `WITH CHECK` de dono | `TestScopeSurvivesConnectionReuse` e `TestProberCanRescheduleButNotCreate` (o `UPDATE` do sondador é barrado) |
| Acrescentar `CREATE POLICY p ON targets FOR INSERT WITH CHECK (current_setting('app.scope', true) = 'prober')` | `TestProberCanRescheduleButNotCreate` |
| Remover a política `checks_via_target` e trocar por `USING (true) WITH CHECK (true)` | `TestChecksAreVisibleOnlyThroughTheirTarget`, `TestCheckForVanishedTargetFailsWithRLSCode` |
| Remover `SKIP LOCKED` do `ClaimDueTargets` (e rodar `sqlc generate`) | `TestSkipLockedDoesNotWaitForAnotherProber` |
| Remover os dois `AT TIME ZONE 'UTC'` da função | `TestPartitionBoundsAreUTCRegardlessOfSessionTimeZone` |
| Em `EnsureChecksPartitions`, trocar `now.UTC()` por `now` | `TestEnsureChecksPartitionsUsesUTCMonths` |

Se alguma sabotagem **não** deixar seu teste vermelho, pare e escale. Não troque o teste até ele ficar vermelho sem entender por que ele passava.

- [ ] **Step 9: Makefile e CI**

No `Makefile`, acrescente `generate` à linha `.PHONY` e o alvo:

```makefile
generate:
	sqlc generate
```

Em `.github/workflows/ci.yml`, acrescente como **primeiro** passo depois de `actions/setup-go`:

```yaml
      - name: sqlc diff
        # Código gerado desatualizado em relação ao SQL é o erro que o sqlc
        # existe para impedir; o CI o transforma em falha de build.
        run: |
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
          sqlc diff
```

Run: `sqlc diff` → sem saída, exit 0.

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
go test -race -count=1 ./...
git add sqlc.yaml Makefile .github/workflows/ci.yml internal/storage/
git commit -m "feat(storage): targets e checks particionada com RLS forçada, via sqlc

Duas portas para a RLS (InUserTx, InProberTx). Partições mensais com bordas
em UTC. As políticas usam NULLIF porque o GUC vira '' depois do primeiro
SET LOCAL da sessão, e um pool reusa a conexão."
```

---
### Task 4: Domínio de alvos — validação, headers cifrados e CRUD

**Files:**
- Create: `internal/targets/target.go`
- Create: `internal/targets/validate.go`
- Create: `internal/targets/headers.go`
- Create: `internal/targets/service.go`
- Test: `internal/targets/validate_test.go` (pacote `targets`, puro)
- Test: `internal/targets/headers_test.go` (pacote `targets_test`, puro)
- Test: `internal/targets/service_test.go` (pacote `targets_test`, Postgres)

**Interfaces:**
- Consumes: `storage.InUserTx`, pacote `db` (Task 3); `crypto.Box`, `crypto.AAD`; `clock.Clock`; `safedial.ValidateURL`, `safedial.AllowIP`.
- Produces:
  - `targets.ErrNotFound error`.
  - `targets.Target` com os campos `ID uuid.UUID`, `Name`, `URL`, `Method string`, `IntervalSec`, `TimeoutMS`, `ExpectedStatus int32`, `DegradedThresholdMS *int32`, `Enabled bool`, `HeaderNames []string`, `CreatedAt time.Time`.
  - `targets.Input`, com os campos de `Target` menos `ID`, `HeaderNames` e `CreatedAt`, mais `Headers map[string]string`.
  - `targets.Patch` com `Name`, `URL`, `Method *string`, `IntervalSec`, `TimeoutMS`, `ExpectedStatus *int32`, `SetDegradedThreshold bool`, `DegradedThresholdMS *int32`, `Enabled *bool`, `SetHeaders bool` e `Headers map[string]string`.
  - `targets.Check` com os campos `CheckedAt time.Time`, `Status string`, `LatencyMS int32`, `StatusCode *int32`, `ErrorKind *string`.
  - `targets.FieldError{Field, Reason string}` e `*targets.ValidationError{Fields []FieldError}`, que implementa `error`.
  - As constantes `ReasonRequired`, `ReasonInvalid`, `ReasonOutOfRange`, `ReasonTooLong`, `ReasonForbidden`, e `DefaultDegradedThresholdMS int32 = 2000`, `DefaultPageSize int32 = 50`, `MaxPageSize int32 = 100`, `MaxChecksWindow = 48 * time.Hour`.
  - `targets.SealHeaders(box *crypto.Box, targetID uuid.UUID, h map[string]string) ([]byte, error)` e `targets.OpenHeaders(box *crypto.Box, targetID uuid.UUID, sealed []byte) (map[string]string, error)`. O sondador usa `OpenHeaders` na Task 7.
  - `targets.NewService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock) *Service`, com os métodos:
    - `Create(ctx, userID uuid.UUID, in Input) (Target, error)`
    - `Get(ctx, userID, id uuid.UUID) (Target, error)`
    - `List(ctx, userID uuid.UUID, before *uuid.UUID, limit int32) ([]Target, error)`
    - `Update(ctx, userID, id uuid.UUID, p Patch) (Target, error)`
    - `Delete(ctx, userID, id uuid.UUID) error`
    - `Checks(ctx, userID, id uuid.UUID, from, to time.Time) ([]Check, error)`

- [ ] **Step 1: Escrever o teste de validação (falha)**

Crie `internal/targets/validate_test.go`:

```go
package targets

import (
	"strings"
	"testing"
)

func valido() fields {
	return fields{
		Name: "API", URL: "https://api.exemplo.com/health", Method: "GET",
		IntervalSec: 60, TimeoutMS: 10000, ExpectedStatus: 200,
	}
}

func ptr[T any](v T) *T { return &v }

func TestValidate(t *testing.T) {
	cases := []struct {
		nome   string
		mexer  func(*fields)
		campo  string // "" = válido
		motivo string
	}{
		{"válido", func(f *fields) {}, "", ""},
		{"método em minúscula é normalizado", func(f *fields) { f.Method = "get" }, "", ""},

		{"nome vazio", func(f *fields) { f.Name = "   " }, "name", ReasonRequired},
		{"nome com 101 caracteres", func(f *fields) { f.Name = strings.Repeat("á", 101) }, "name", ReasonTooLong},
		{"nome com 100 caracteres", func(f *fields) { f.Name = strings.Repeat("á", 100) }, "", ""},
		{"nome com controle", func(f *fields) { f.Name = "a\x00b" }, "name", ReasonInvalid},

		{"url vazia", func(f *fields) { f.URL = "" }, "url", ReasonRequired},
		{"url ftp", func(f *fields) { f.URL = "ftp://exemplo.com" }, "url", ReasonInvalid},
		{"url com credencial", func(f *fields) { f.URL = "https://u:p@exemplo.com" }, "url", ReasonInvalid},
		{"url longa demais", func(f *fields) { f.URL = "https://exemplo.com/" + strings.Repeat("a", 2048) }, "url", ReasonTooLong},
		{"url loopback literal", func(f *fields) { f.URL = "http://127.0.0.1:8080/" }, "url", ReasonForbidden},
		{"url loopback IPv6", func(f *fields) { f.URL = "http://[::1]/" }, "url", ReasonForbidden},
		{"url privada", func(f *fields) { f.URL = "http://10.0.0.5/admin" }, "url", ReasonForbidden},
		{"url metadata", func(f *fields) { f.URL = "http://169.254.169.254/latest/" }, "url", ReasonForbidden},
		{"url localhost", func(f *fields) { f.URL = "http://localhost:9000/" }, "url", ReasonForbidden},
		{"url LOCALHOST. com ponto", func(f *fields) { f.URL = "http://LOCALHOST./" }, "url", ReasonForbidden},
		{"url sub.localhost", func(f *fields) { f.URL = "http://app.localhost/" }, "url", ReasonForbidden},
		{"url IP público literal", func(f *fields) { f.URL = "http://8.8.8.8/" }, "", ""},

		{"método DELETE", func(f *fields) { f.Method = "DELETE" }, "method", ReasonInvalid},

		{"intervalo 29", func(f *fields) { f.IntervalSec = 29 }, "interval_sec", ReasonOutOfRange},
		{"intervalo 86401", func(f *fields) { f.IntervalSec = 86401 }, "interval_sec", ReasonOutOfRange},
		{"timeout 999", func(f *fields) { f.TimeoutMS = 999 }, "timeout_ms", ReasonOutOfRange},
		{"timeout 30001", func(f *fields) { f.TimeoutMS = 30001 }, "timeout_ms", ReasonOutOfRange},
		// Review Focus 5 (D3): o jitter de −10% antecipa a próxima sonda
		// para 27s num intervalo de 30s.
		{"timeout em 90% do intervalo", func(f *fields) { f.IntervalSec = 30; f.TimeoutMS = 27000 }, "timeout_ms", ReasonOutOfRange},
		{"timeout logo abaixo de 90%", func(f *fields) { f.IntervalSec = 30; f.TimeoutMS = 26999 }, "", ""},

		{"status 99", func(f *fields) { f.ExpectedStatus = 99 }, "expected_status", ReasonOutOfRange},
		{"status 600", func(f *fields) { f.ExpectedStatus = 600 }, "expected_status", ReasonOutOfRange},

		{"limiar 0", func(f *fields) { f.DegradedThresholdMS = ptr[int32](0) }, "degraded_threshold_ms", ReasonOutOfRange},
		{"limiar 60001", func(f *fields) { f.DegradedThresholdMS = ptr[int32](60001) }, "degraded_threshold_ms", ReasonOutOfRange},
		{"limiar nulo desativa", func(f *fields) { f.DegradedThresholdMS = nil }, "", ""},

		{"header permitido", func(f *fields) { f.Headers = map[string]string{"Authorization": "Bearer x"} }, "", ""},
		{"header Host", func(f *fields) { f.Headers = map[string]string{"host": "interno"} }, "headers", ReasonForbidden},
		{"header com espaço no nome", func(f *fields) { f.Headers = map[string]string{"X Bad": "1"} }, "headers", ReasonInvalid},
		{"header com CRLF no valor", func(f *fields) { f.Headers = map[string]string{"X-A": "1\r\nX-Injetado: 2"} }, "headers", ReasonInvalid},
		{"header duplicado por caixa", func(f *fields) { f.Headers = map[string]string{"x-a": "1", "X-A": "2"} }, "headers", ReasonInvalid},
		{"header valor longo", func(f *fields) { f.Headers = map[string]string{"X-A": strings.Repeat("a", 1025)} }, "headers", ReasonTooLong},
		{"21 headers", func(f *fields) {
			f.Headers = map[string]string{}
			for i := range 21 {
				f.Headers["X-H"+strings.Repeat("a", i)] = "1"
			}
		}, "headers", ReasonTooLong},
		{"headers somando mais de 8 KiB", func(f *fields) {
			f.Headers = map[string]string{}
			for i := range 9 {
				f.Headers["X-H"+strings.Repeat("a", i)] = strings.Repeat("v", 1000)
			}
		}, "headers", ReasonTooLong},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			c.mexer(&f)
			err := validate(&f)
			if c.campo == "" {
				if err != nil {
					t.Fatalf("queria válido, deu %+v", err.Fields)
				}
				return
			}
			if err == nil {
				t.Fatalf("queria erro em %s (%s), passou", c.campo, c.motivo)
			}
			for _, fe := range err.Fields {
				if fe.Field == c.campo && fe.Reason == c.motivo {
					return
				}
			}
			t.Errorf("queria %s/%s, veio %+v", c.campo, c.motivo, err.Fields)
		})
	}
}

// Nomes de header chegam canonizados ao sondador: "authorization" e
// "Authorization" são o mesmo cabeçalho, e só um pode sobrar.
func TestValidateCanonicalizesHeaderNames(t *testing.T) {
	f := valido()
	f.Headers = map[string]string{"x-api-key": "k"}
	if err := validate(&f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Headers["X-Api-Key"]; !ok || len(f.Headers) != 1 {
		t.Errorf("headers = %v, queria só X-Api-Key", f.Headers)
	}
}
```

Run: `go test ./internal/targets/ -run TestValidate -v`
Expected: FAIL de compilação: `undefined: fields`, `validate`, `Reason*`.

- [ ] **Step 2: Implementar tipos e validação**

Crie `internal/targets/target.go`:

```go
// Package targets é o domínio dos alvos de monitoramento: validação, segredo
// dos headers e o CRUD. Toda operação exige userID (spec §5.5) e passa pela
// RLS de storage.InUserTx.
package targets

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("alvo não encontrado")

// Target é o que sai do domínio. Os VALORES dos headers não saem nunca (D5):
// o dono os escreve, o sondador os usa, e ninguém os lê de volta.
type Target struct {
	ID                  uuid.UUID
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32 // nil: sem classificação degraded (spec §11.5)
	Enabled             bool
	HeaderNames         []string
	CreatedAt           time.Time
}

// Input é o cadastro. Zero em Method, IntervalSec, TimeoutMS e ExpectedStatus
// significa "use o padrão"; zero nunca é válido em nenhum deles, então não há
// ambiguidade. DegradedThresholdMS NÃO segue essa regra: nil já significa
// "desativado", e quem traduz "campo ausente" em DefaultDegradedThresholdMS é
// a camada HTTP, a única que distingue ausente de null.
type Input struct {
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32
	Enabled             bool
	Headers             map[string]string
}

// Patch muda só o que vier preenchido. Nos dois campos com Set*, "limpar" é
// uma mudança legítima e diferente de "não mexer", e um ponteiro nil não
// separaria as duas coisas.
type Patch struct {
	Name, URL, Method                      *string
	IntervalSec, TimeoutMS, ExpectedStatus *int32
	SetDegradedThreshold                   bool
	DegradedThresholdMS                    *int32
	Enabled                                *bool
	SetHeaders                             bool
	Headers                                map[string]string
}

type Check struct {
	CheckedAt  time.Time
	Status     string
	LatencyMS  int32
	StatusCode *int32
	ErrorKind  *string
}

const (
	DefaultMethod                    = "GET"
	DefaultIntervalSec         int32 = 60
	DefaultTimeoutMS           int32 = 10000
	DefaultExpectedStatus      int32 = 200
	DefaultDegradedThresholdMS int32 = 2000

	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100

	// MaxChecksWindow é a janela que a tabela bruta atende (spec §9.1).
	// Acima disso, rollups, que chegam no Plano 4 (D6).
	MaxChecksWindow = 48 * time.Hour
)
```

Crie `internal/targets/validate.go`:

```go
package targets

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

// Motivos de validação. Vocabulário fechado: a camada HTTP os repassa ao
// cliente, e nada que venha do corpo recebido entra neles.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
	ReasonTooLong    = "too_long"
	ReasonForbidden  = "forbidden"
)

type FieldError struct {
	Field  string
	Reason string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return "dados do alvo inválidos" }

func (e *ValidationError) add(field, reason string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Reason: reason})
}

// Limites de cadastro. Cada um tem par num CHECK da migration 00004.
const (
	minIntervalSec         = 30
	maxIntervalSec         = 86400
	minTimeoutMS           = 1000
	maxTimeoutMS           = 30000
	maxNameRunes           = 100
	maxURLBytes            = 2048
	maxDegradedThresholdMS = 60000
	maxHeaders             = 20
	maxHeaderValueBytes    = 1024
	maxHeadersTotalBytes   = 8 << 10
)

// D4: não há coluna de corpo, e PUT ou DELETE sem corpo num monitor seriam
// armadilha.
var allowedMethods = []string{"GET", "HEAD", "POST"}

// forbiddenHeaders são os que o transporte controla. Deixar o usuário
// defini-los quebraria o enquadramento HTTP (Content-Length,
// Transfer-Encoding), a conexão (Connection, Upgrade) ou a noção de destino
// (Host), sem nenhum ganho para monitoramento.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true,
	"Connection": true, "Keep-Alive": true, "Upgrade": true, "Te": true,
	"Trailer": true, "Expect": true, "Proxy-Authorization": true,
	"Proxy-Connection": true,
}

// fields é o alvo em validação: a união do que Input e Patch conseguem mexer.
type fields struct {
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32
	Headers             map[string]string
}

// validate confere todos os campos e acumula todos os erros, para que o
// cliente corrija tudo numa passada. Normaliza no lugar: nome e URL sem
// espaço nas pontas, método em maiúscula, nomes de header canônicos.
func validate(f *fields) *ValidationError {
	var v ValidationError

	f.Name = strings.TrimSpace(f.Name)
	switch {
	case f.Name == "":
		v.add("name", ReasonRequired)
	case utf8.RuneCountInString(f.Name) > maxNameRunes:
		v.add("name", ReasonTooLong)
	case strings.ContainsFunc(f.Name, unicode.IsControl):
		v.add("name", ReasonInvalid)
	}

	f.URL = strings.TrimSpace(f.URL)
	if motivo := checkURL(f.URL); motivo != "" {
		v.add("url", motivo)
	}

	f.Method = strings.ToUpper(f.Method)
	if !slices.Contains(allowedMethods, f.Method) {
		v.add("method", ReasonInvalid)
	}

	intervaloOK := f.IntervalSec >= minIntervalSec && f.IntervalSec <= maxIntervalSec
	if !intervaloOK {
		v.add("interval_sec", ReasonOutOfRange)
	}
	switch {
	case f.TimeoutMS < minTimeoutMS || f.TimeoutMS > maxTimeoutMS:
		v.add("timeout_ms", ReasonOutOfRange)
	// D3: o jitter de −10% (spec §6.2) pode antecipar a próxima execução
	// para 90% do intervalo. Um timeout acima disso deixaria duas sondas do
	// mesmo alvo em voo ao mesmo tempo. Só se compara com intervalo válido,
	// para não acusar o timeout pelo erro do intervalo.
	case intervaloOK && int64(f.TimeoutMS)*10 >= int64(f.IntervalSec)*9000:
		v.add("timeout_ms", ReasonOutOfRange)
	}

	if f.ExpectedStatus < 100 || f.ExpectedStatus > 599 {
		v.add("expected_status", ReasonOutOfRange)
	}

	if d := f.DegradedThresholdMS; d != nil && (*d < 1 || *d > maxDegradedThresholdMS) {
		v.add("degraded_threshold_ms", ReasonOutOfRange)
	}

	if h, motivo := normalizeHeaders(f.Headers); motivo != "" {
		v.add("headers", motivo)
	} else {
		f.Headers = h
	}

	if len(v.Fields) == 0 {
		return nil
	}
	return &v
}

// checkURL é a recusa antecipada, só para dar resposta útil no cadastro.
// A guarda de verdade é o dial (safedial, spec §5.1): um NOME que resolve
// para IP interno passa daqui e morre lá, e é assim que tem de ser. Resolver
// DNS no cadastro seria exatamente a janela de rebinding que a spec descreve.
func checkURL(raw string) string {
	if raw == "" {
		return ReasonRequired
	}
	if len(raw) > maxURLBytes {
		return ReasonTooLong
	}
	u, err := safedial.ValidateURL(raw)
	if err != nil {
		return ReasonInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if safedial.AllowIP(ip) != nil {
			return ReasonForbidden
		}
		return ""
	}
	// RFC 6761: localhost e todo *.localhost resolvem para loopback.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ReasonForbidden
	}
	return ""
}

func normalizeHeaders(in map[string]string) (map[string]string, string) {
	if len(in) == 0 {
		return nil, ""
	}
	if len(in) > maxHeaders {
		return nil, ReasonTooLong
	}
	out := make(map[string]string, len(in))
	total := 0
	for nome, valor := range in {
		if !validHeaderName(nome) {
			return nil, ReasonInvalid
		}
		canon := http.CanonicalHeaderKey(nome)
		if forbiddenHeaders[canon] {
			return nil, ReasonForbidden
		}
		if _, dup := out[canon]; dup {
			return nil, ReasonInvalid
		}
		if len(valor) > maxHeaderValueBytes {
			return nil, ReasonTooLong
		}
		if !validHeaderValue(valor) {
			return nil, ReasonInvalid
		}
		total += len(canon) + len(valor)
		out[canon] = valor
	}
	if total > maxHeadersTotalBytes {
		return nil, ReasonTooLong
	}
	return out, ""
}

// validHeaderName aceita só token (RFC 9110 §5.6.2).
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
		if !ok {
			return false
		}
	}
	return true
}

// validHeaderValue recusa CR e LF, que injetariam um cabeçalho novo, e os
// demais caracteres de controle, exceto tab.
func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
```

Run: `go test ./internal/targets/ -run 'TestValidate' -v`
Expected: PASS.

- [ ] **Step 3: Provar por sabotagem a validação**

1. Troque `>= int64(f.IntervalSec)*9000` por `>= int64(f.IntervalSec)*10000` → o caso `timeout em 90% do intervalo` fica vermelho.
2. Remova o bloco `if ip, err := netip.ParseAddr(host)` → os quatro casos de IP literal bloqueado ficam vermelhos.
3. Remova `http.CanonicalHeaderKey` (use `nome` direto) → `header duplicado por caixa`, `header Host` e `TestValidateCanonicalizesHeaderNames` ficam vermelhos.

- [ ] **Step 4: Escrever os testes de headers (falham)**

Crie `internal/targets/headers_test.go`:

```go
package targets_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

var chaveDeTeste = []byte("chave-mestra-de-teste-32-bytes!!")

func caixa(t *testing.T) *crypto.Box {
	t.Helper()
	b, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHeadersRoundTripAndHidePlaintext(t *testing.T) {
	box := caixa(t)
	id := uuid.Must(uuid.NewV7())
	h := map[string]string{"Authorization": "Bearer segredo-do-cliente"}

	sealed, err := targets.SealHeaders(box, id, h)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("segredo-do-cliente")) {
		t.Fatal("o valor do header aparece em claro no ciphertext")
	}
	back, err := targets.OpenHeaders(box, id, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if back["Authorization"] != "Bearer segredo-do-cliente" {
		t.Errorf("volta = %v", back)
	}
}

// AAD amarra o ciphertext à linha (spec §5.4): copiar headers_enc de um alvo
// para outro não pode entregar os headers do primeiro ao sondador do segundo.
func TestHeadersAreBoundToTheirRow(t *testing.T) {
	box := caixa(t)
	sealed, err := targets.SealHeaders(box, uuid.Must(uuid.NewV7()), map[string]string{"X-A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := targets.OpenHeaders(box, uuid.Must(uuid.NewV7()), sealed); err == nil {
		t.Error("headers de um alvo abriram com o id de outro")
	}
}

func TestEmptyHeadersAreStoredAsNull(t *testing.T) {
	sealed, err := targets.SealHeaders(caixa(t), uuid.Must(uuid.NewV7()), nil)
	if err != nil || sealed != nil {
		t.Errorf("sealed = %v, err = %v; queria nil, nil", sealed, err)
	}
	h, err := targets.OpenHeaders(caixa(t), uuid.Must(uuid.NewV7()), nil)
	if err != nil || h != nil {
		t.Errorf("h = %v, err = %v; queria nil, nil", h, err)
	}
}
```

Run: `go test ./internal/targets/ -run TestHeaders -v` → FAIL de compilação: `undefined: targets.SealHeaders`.

- [ ] **Step 5: Implementar os headers**

Crie `internal/targets/headers.go`:

```go
package targets

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// SealHeaders cifra os headers do alvo para headers_enc (spec §5.4).
//
// Mapa vazio vira NULL: não há segredo a guardar, e um ciphertext de "{}"
// só daria a impressão de que há.
func SealHeaders(box *crypto.Box, targetID uuid.UUID, h map[string]string) ([]byte, error) {
	if len(h) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("serializar headers: %w", err)
	}
	sealed, err := box.Seal(plain, headersAAD(targetID))
	if err != nil {
		return nil, fmt.Errorf("cifrar headers: %w", err)
	}
	return sealed, nil
}

// OpenHeaders decifra headers_enc. O AAD é o id da linha: um ciphertext
// movido para outro alvo não abre.
func OpenHeaders(box *crypto.Box, targetID uuid.UUID, sealed []byte) (map[string]string, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	plain, err := box.Open(sealed, headersAAD(targetID))
	if err != nil {
		return nil, fmt.Errorf("decifrar headers do alvo: %w", err)
	}
	var h map[string]string
	if err := json.Unmarshal(plain.Reveal(), &h); err != nil {
		return nil, fmt.Errorf("ler headers do alvo: %w", err)
	}
	return h, nil
}

func headersAAD(id uuid.UUID) []byte { return crypto.AAD("targets", "headers_enc", id) }

// headerNames é o que a API pode mostrar: os nomes, ordenados, e nunca nil,
// para que o JSON saia [] e não null.
func headerNames(h map[string]string) []string {
	names := slices.Collect(maps.Keys(h))
	slices.Sort(names)
	if names == nil {
		return []string{}
	}
	return names
}
```

Run: `go test ./internal/targets/ -v` → PASS.

Sabotagem: troque `headersAAD(targetID)` por `crypto.AAD("targets", "headers_enc", uuid.Nil)` nas duas funções → `TestHeadersAreBoundToTheirRow` fica vermelho.

- [ ] **Step 6: Escrever os testes do serviço (falham)**

Crie `internal/targets/service_test.go`:

```go
package targets_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

type ambiente struct {
	svc   *targets.Service
	pool  *pgxpool.Pool
	admin *pgxpool.Pool // superusuário: enxerga o banco como um dump enxergaria
	clk   *clock.Fake
}

func novoAmbiente(t *testing.T) ambiente {
	t.Helper()
	pool := testdb.New(t)
	admin, err := storage.NewPool(context.Background(), testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	return ambiente{svc: targets.NewService(pool, caixa(t), clk), pool: pool, admin: admin, clk: clk}
}

func (a ambiente) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func entrada() targets.Input {
	limiar := targets.DefaultDegradedThresholdMS
	return targets.Input{
		Name: "API", URL: "https://api.exemplo.com/health", Enabled: true,
		DegradedThresholdMS: &limiar,
		Headers:             map[string]string{"Authorization": "Bearer segredo-do-cliente"},
	}
}

func TestCreateAppliesDefaultsAndHidesHeaderValues(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)

	alvo, err := a.svc.Create(context.Background(), dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	if alvo.Method != "GET" || alvo.IntervalSec != 60 || alvo.TimeoutMS != 10000 || alvo.ExpectedStatus != 200 {
		t.Errorf("padrões não aplicados: %+v", alvo)
	}
	if len(alvo.HeaderNames) != 1 || alvo.HeaderNames[0] != "Authorization" {
		t.Errorf("HeaderNames = %v", alvo.HeaderNames)
	}

	// Pelo lado de fora da RLS, como um dump do banco: o valor não pode estar
	// em claro, e o alvo nasce vencido, para ser sondado já.
	var enc []byte
	var proximo time.Time
	if err := a.admin.QueryRow(context.Background(),
		`SELECT headers_enc, next_run_at FROM targets WHERE id = $1`, alvo.ID).Scan(&enc, &proximo); err != nil {
		t.Fatal(err)
	}
	if len(enc) == 0 || bytes.Contains(enc, []byte("segredo-do-cliente")) {
		t.Errorf("headers_enc vazio ou em claro: %q", enc)
	}
	if !proximo.Equal(a.clk.Now()) {
		t.Errorf("next_run_at = %v, queria agora (%v)", proximo, a.clk.Now())
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	a := novoAmbiente(t)
	in := entrada()
	in.URL = "http://127.0.0.1/"
	_, err := a.svc.Create(context.Background(), a.usuario(t), in)
	var verr *targets.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("erro = %v, queria *ValidationError", err)
	}
}

func TestAnotherUsersTargetIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, intruso := a.usuario(t), a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	novoNome := "roubado"
	if _, err := a.svc.Get(ctx, intruso, alvo.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Get do intruso = %v, queria ErrNotFound", err)
	}
	if _, err := a.svc.Update(ctx, intruso, alvo.ID, targets.Patch{Name: &novoNome}); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Update do intruso = %v, queria ErrNotFound", err)
	}
	if err := a.svc.Delete(ctx, intruso, alvo.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Delete do intruso = %v, queria ErrNotFound", err)
	}
	if _, err := a.svc.Checks(ctx, intruso, alvo.ID, a.clk.Now().Add(-time.Hour), a.clk.Now()); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Checks do intruso = %v, queria ErrNotFound", err)
	}

	intacto, err := a.svc.Get(ctx, dono, alvo.ID)
	if err != nil || intacto.Name != "API" {
		t.Errorf("o alvo do dono mudou: %+v, %v", intacto, err)
	}
}

// D13: mudar o que é sondado reagenda para agora; mudar o nome, não.
func TestUpdateReschedulesOnlyWhenTheProbeChanges(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	criadoEm := a.clk.Now()

	proximo := func() time.Time {
		var p time.Time
		if err := a.admin.QueryRow(ctx, `SELECT next_run_at FROM targets WHERE id = $1`, alvo.ID).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	a.clk.Advance(time.Hour)
	nome := "Outro nome"
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{Name: &nome}); err != nil {
		t.Fatal(err)
	}
	if !proximo().Equal(criadoEm) {
		t.Errorf("mudar o nome reagendou a sonda")
	}

	url := "https://api.exemplo.com/v2/health"
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &url}); err != nil {
		t.Fatal(err)
	}
	if !proximo().Equal(a.clk.Now()) {
		t.Errorf("mudar a URL não reagendou para agora")
	}
}

// O Patch é validado sobre o alvo RESULTANTE: um timeout que cabia no
// intervalo antigo pode não caber no novo.
func TestUpdateValidatesTheResultingTarget(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.TimeoutMS = 29000
	in.IntervalSec = 60
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}

	trinta := int32(30)
	_, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{IntervalSec: &trinta})
	var verr *targets.ValidationError
	if !errors.As(err, &verr) || verr.Fields[0].Field != "timeout_ms" {
		t.Errorf("erro = %v, queria ValidationError em timeout_ms", err)
	}
}

func TestUpdateCanClearThresholdAndHeaders(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{
		SetDegradedThreshold: true, DegradedThresholdMS: nil,
		SetHeaders: true, Headers: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DegradedThresholdMS != nil || len(got.HeaderNames) != 0 {
		t.Errorf("limiar = %v, headers = %v; queria os dois limpos", got.DegradedThresholdMS, got.HeaderNames)
	}
}

func TestListPaginatesByCursorNewestFirst(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	var criados []uuid.UUID
	for range 5 {
		alvo, err := a.svc.Create(ctx, dono, entrada())
		if err != nil {
			t.Fatal(err)
		}
		criados = append(criados, alvo.ID)
	}

	var vistos []uuid.UUID
	var cursor *uuid.UUID
	for {
		pagina, err := a.svc.List(ctx, dono, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pagina {
			vistos = append(vistos, p.ID)
		}
		if len(pagina) < 2 {
			break
		}
		ultimo := pagina[len(pagina)-1].ID
		cursor = &ultimo
	}

	if len(vistos) != 5 {
		t.Fatalf("paginação devolveu %d alvos, queria 5: %v", len(vistos), vistos)
	}
	for i := range vistos {
		if vistos[i] != criados[len(criados)-1-i] {
			t.Errorf("posição %d = %s, queria %s (mais novo primeiro)", i, vistos[i], criados[len(criados)-1-i])
		}
	}
}

func TestChecksWindowIsLimited(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	fim := a.clk.Now()

	_, err = a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow-time.Second), fim)
	var verr *targets.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("janela de 48h+1s deu %v, queria ValidationError", err)
	}
	if _, err := a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow), fim); err != nil {
		t.Errorf("janela de 48h exatas deu %v", err)
	}
}
```

Run: `go test ./internal/targets/ -v` → FAIL de compilação: `undefined: targets.NewService`.

- [ ] **Step 7: Implementar o serviço**

Crie `internal/targets/service.go`:

```go
package targets

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Service é o CRUD de alvos. Todo método recebe userID logo depois de ctx
// (spec §5.5) e roda sob storage.InUserTx, que liga a RLS para esse
// usuário: as queries filtram por user_id E a política filtra de novo.
type Service struct {
	pool *pgxpool.Pool
	box  *crypto.Box
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock) *Service {
	return &Service{pool: pool, box: box, clk: clk}
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Target, error) {
	f := fields{
		Name:                in.Name,
		URL:                 in.URL,
		Method:              cmp.Or(in.Method, DefaultMethod),
		IntervalSec:         cmp.Or(in.IntervalSec, DefaultIntervalSec),
		TimeoutMS:           cmp.Or(in.TimeoutMS, DefaultTimeoutMS),
		ExpectedStatus:      cmp.Or(in.ExpectedStatus, DefaultExpectedStatus),
		DegradedThresholdMS: in.DegradedThresholdMS,
		Headers:             in.Headers,
	}
	if verr := validate(&f); verr != nil {
		return Target{}, verr
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Target{}, fmt.Errorf("gerar id do alvo: %w", err)
	}
	sealed, err := SealHeaders(s.box, id, f.Headers)
	if err != nil {
		return Target{}, err
	}

	// O alvo nasce vencido: a primeira sonda sai no próximo tick, e quem
	// cadastrou vê o estado em segundos, não depois de um intervalo inteiro.
	now := s.clk.Now()
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: userID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: in.Enabled, NextRunAt: now, CreatedAt: now,
		})
	})
	if err != nil {
		return Target{}, fmt.Errorf("gravar alvo: %w", err)
	}
	return Target{
		ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
		IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
		DegradedThresholdMS: f.DegradedThresholdMS, Enabled: in.Enabled,
		HeaderNames: headerNames(f.Headers), CreatedAt: now,
	}, nil
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		row, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id})
		if err != nil {
			return naoAchado(err)
		}
		out, err = s.toTarget(row)
		return err
	})
	return out, err
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, before *uuid.UUID, limit int32) ([]Target, error) {
	if limit < 1 || limit > MaxPageSize {
		limit = DefaultPageSize
	}
	var out []Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: userID, Before: before, MaxRows: limit})
		if err != nil {
			return fmt.Errorf("listar alvos: %w", err)
		}
		out = make([]Target, 0, len(rows))
		for _, r := range rows {
			t, err := s.toTarget(r)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return nil
	})
	return out, err
}

// Update lê com FOR UPDATE, aplica o patch, valida o alvo RESULTANTE e grava,
// tudo na mesma transação: dois PATCH simultâneos não se atropelam, e um
// timeout que cabia no intervalo antigo não passa despercebido no novo.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, p Patch) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		row, err := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: userID, ID: id})
		if err != nil {
			return naoAchado(err)
		}
		headers, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
		if err != nil {
			return err
		}

		f := fields{
			Name: row.Name, URL: row.URL, Method: row.Method,
			IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
			DegradedThresholdMS: row.DegradedThresholdMS, Headers: headers,
		}
		enabled := row.Enabled
		mudouASonda := p.apply(&f, &enabled)
		if verr := validate(&f); verr != nil {
			return verr
		}

		sealed, err := SealHeaders(s.box, row.ID, f.Headers)
		if err != nil {
			return err
		}
		proximo := row.NextRunAt
		if mudouASonda {
			proximo = s.clk.Now() // D13
		}
		err = q.UpdateTarget(ctx, db.UpdateTargetParams{
			UserID: userID, ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: enabled, NextRunAt: proximo,
		})
		if err != nil {
			return fmt.Errorf("atualizar alvo: %w", err)
		}
		out = Target{
			ID: row.ID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			DegradedThresholdMS: f.DegradedThresholdMS, Enabled: enabled,
			HeaderNames: headerNames(f.Headers), CreatedAt: row.CreatedAt,
		}
		return nil
	})
	return out, err
}

// apply devolve true quando a mudança altera O QUE é sondado (D13).
func (p Patch) apply(f *fields, enabled *bool) bool {
	antes := *f
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.URL != nil {
		f.URL = *p.URL
	}
	if p.Method != nil {
		f.Method = *p.Method
	}
	if p.IntervalSec != nil {
		f.IntervalSec = *p.IntervalSec
	}
	if p.TimeoutMS != nil {
		f.TimeoutMS = *p.TimeoutMS
	}
	if p.ExpectedStatus != nil {
		f.ExpectedStatus = *p.ExpectedStatus
	}
	if p.SetDegradedThreshold {
		f.DegradedThresholdMS = p.DegradedThresholdMS
	}
	if p.SetHeaders {
		f.Headers = p.Headers
	}
	if p.Enabled != nil {
		*enabled = *p.Enabled
	}
	return f.URL != antes.URL || f.Method != antes.Method ||
		f.IntervalSec != antes.IntervalSec || f.TimeoutMS != antes.TimeoutMS ||
		f.ExpectedStatus != antes.ExpectedStatus || p.SetHeaders
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: userID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar alvo: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Checks devolve os resultados de um alvo no intervalo [from, to). Busca o
// alvo antes, e não só os checks, porque uma lista vazia não distinguiria
// "alvo de outro usuário" de "alvo sem sondas": o primeiro precisa ser 404.
func (s *Service) Checks(ctx context.Context, userID, id uuid.UUID, from, to time.Time) ([]Check, error) {
	if !from.Before(to) {
		return nil, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonInvalid}}}
	}
	if to.Sub(from) > MaxChecksWindow {
		return nil, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonOutOfRange}}}
	}
	var out []Check
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if _, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id}); err != nil {
			return naoAchado(err)
		}
		rows, err := q.ListChecks(ctx, db.ListChecksParams{UserID: userID, TargetID: id, FromAt: from, ToAt: to})
		if err != nil {
			return fmt.Errorf("listar checks: %w", err)
		}
		out = make([]Check, 0, len(rows))
		for _, r := range rows {
			out = append(out, Check{
				CheckedAt: r.CheckedAt, Status: r.Status, LatencyMS: r.LatencyMS,
				StatusCode: r.StatusCode, ErrorKind: r.ErrorKind,
			})
		}
		return nil
	})
	return out, err
}

func (s *Service) toTarget(row db.Target) (Target, error) {
	h, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
	if err != nil {
		return Target{}, err
	}
	return Target{
		ID: row.ID, Name: row.Name, URL: row.URL, Method: row.Method,
		IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
		DegradedThresholdMS: row.DegradedThresholdMS, Enabled: row.Enabled,
		HeaderNames: headerNames(h), CreatedAt: row.CreatedAt,
	}, nil
}

// naoAchado traduz a ausência de linha. Sob a RLS, "não existe" e "é de
// outro usuário" são indistinguíveis de propósito: os dois viram 404.
func naoAchado(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("buscar alvo: %w", err)
}
```

- [ ] **Step 8: Rodar e ver passar**

Run: `go test -race -count=1 ./internal/targets/ -v`
Expected: PASS.

- [ ] **Step 9: Provar por sabotagem o serviço**

1. Em `Create`, grave `HeadersEnc: []byte(fmt.Sprint(f.Headers))` em vez de `sealed` → `TestCreateAppliesDefaultsAndHidesHeaderValues` fica vermelho.
2. **As duas barreiras de isolamento, uma de cada vez e depois juntas.** Esta sabotagem tem três execuções, e o resultado esperado da primeira e da segunda é **verde**. As barreiras são independentes de propósito (spec §5.5): derrubar só uma não pode abrir o isolamento. Um teste que ficasse vermelho aí estaria testando uma barreira só.
   - **2a.** Na query `GetTarget` de `targets.sql`, troque `WHERE user_id = $1 AND id = $2` por `WHERE sqlc.arg(user_id)::uuid IS NOT NULL AND id = sqlc.arg(id)`. Isso mantém `GetTargetParams{UserID, ID}` e tira o filtro. Rode `sqlc generate` e o teste: `TestAnotherUsersTargetIsNotFound` continua **verde**, porque a RLS segura.
   - **2b.** Desfaça a 2a. Na migration, troque a política `targets_owner` por `USING (true) WITH CHECK (true)`. O teste continua **verde**, porque o `WHERE` segura. Os testes da Task 3 é que ficam vermelhos.
   - **2c.** Aplique a 2a e a 2b juntas. `TestAnotherUsersTargetIsNotFound` fica **vermelho** no `Get`.

   Registre as três execuções no relatório.
3. Em `apply`, devolva sempre `false` → `TestUpdateReschedulesOnlyWhenTheProbeChanges` fica vermelho.
4. Em `Checks`, remova o `GetTarget` preliminar → `TestAnotherUsersTargetIsNotFound` fica vermelho (lista vazia em vez de 404).

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
git add internal/targets/
git commit -m "feat(targets): domínio de alvos com validação campo a campo e headers cifrados"
```

---
### Task 5: API de alvos — handlers, erros por campo e rate limit por usuário

**Files:**
- Modify: `internal/apierr/errors.go` (`FieldError`, `WriteValidation`, `errorBody`)
- Modify: `internal/httpapi/middleware/ratelimit.go` (chave injetável, `RateLimitUser`)
- Modify: `internal/httpapi/middleware/ratelimit_internal_test.go` (duas chamadas + um teste)
- Create: `internal/httpapi/optional.go`
- Create: `internal/httpapi/targethandlers.go`
- Modify: `internal/httpapi/authhandlers.go` (`decodeJSON` ganha a variante estrita)
- Modify: `internal/httpapi/routes.go` (`Deps` e rotas novas)
- Modify: `cmd/vigilume/main.go` (monta `targets.Service` e `Deps`)
- Modify: `internal/httpapi/auth_integration_test.go` (`newAPIWith` monta `Deps`)
- Create: `internal/httpapi/targets_integration_test.go`

**Interfaces:**
- Consumes: `targets.Service` e tipos (Task 4); `storage.EnsureChecksPartitions`, `storage.InProberTx`, `db.InsertCheckParams` (Task 3, só nos testes).
- Produces:
  - `apierr.FieldError{Field, Reason string}` (JSON `field`, `reason`) e `apierr.WriteValidation(w http.ResponseWriter, r *http.Request, fields []FieldError)`.
  - `middleware.RateLimitUser(ctx context.Context, perMinute, burst int) func(http.Handler) http.Handler`.
  - `httpapi.Deps{Store *auth.PGStore; Sessions *auth.Manager; TOTP *auth.TOTP; Targets *targets.Service; Clock clock.Clock}` e `httpapi.Routes(ctx context.Context, cfg *config.Config, d Deps) http.Handler`. **Muda a assinatura de `Routes`**: são 6 parâmetros hoje, e o sétimo pedia uma struct.
  - Rotas: `GET/POST /api/v1/targets`, `GET/PATCH/DELETE /api/v1/targets/{id}`, `GET /api/v1/targets/{id}/checks`.

**Contrato JSON** (a SPA do Plano 5 é escrita contra ele):

```
POST /api/v1/targets          201 → alvo
  { "name", "url", "method"?, "interval_sec"?, "timeout_ms"?, "expected_status"?,
    "degraded_threshold_ms"?: number|null, "enabled"?, "headers"?: {nome: valor} }
  degraded_threshold_ms: ausente → 2000; null → desativado; número → valor.

PATCH /api/v1/targets/{id}    200 → alvo
  Os mesmos campos, todos opcionais. "headers": null limpa; "headers": {...} substitui.

GET /api/v1/targets?limit=&cursor=   200 → { "items": [alvo], "next_cursor"?: "<uuid>" }
GET /api/v1/targets/{id}             200 → alvo
DELETE /api/v1/targets/{id}          204
GET /api/v1/targets/{id}/checks?from=&to=   200 → { "items": [check] }
  RFC 3339. Padrão: to = agora, from = to − 24h. Janela > 48h → 400 (D6).

alvo  = { "id", "name", "url", "method", "interval_sec", "timeout_ms", "expected_status",
          "degraded_threshold_ms": number|null, "enabled", "header_names": [..], "created_at" }
check = { "checked_at", "status", "latency_ms", "status_code": number|null,
          "error_kind": string|null }
400   = { "error": { "code": "validation_failed", "message", "fields"?: [{ "field", "reason" }] },
          "trace_id" }
```

**Nenhuma resposta contém valor de header** (D5). Campo desconhecido no corpo → 400. `id` malformado → 404 (D16).

- [ ] **Step 1: Envelope com campos**

Em `internal/apierr/errors.go`, troque `errorBody` e `WriteError` por:

```go
// FieldError aponta o campo culpado de um 400 (D14). Field e Reason vêm de
// vocabulário fechado, definido no código e nunca no corpo recebido: é o que
// mantém a garantia de WriteError de que nada do cliente nem do banco ecoa
// na resposta.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type errorBody struct {
	Error struct {
		Code    ErrorCode    `json:"code"`
		Message string       `json:"message"`
		Fields  []FieldError `json:"fields,omitempty"`
	} `json:"error"`
	TraceID string `json:"trace_id"`
}

func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode) {
	writeError(w, r, code, nil)
}

// WriteValidation é o 400 com os campos culpados. Extensão compatível da
// spec §9.1: quem ignora "fields" continua lendo code e message.
func WriteValidation(w http.ResponseWriter, r *http.Request, fields []FieldError) {
	writeError(w, r, CodeValidation, fields)
}

func writeError(w http.ResponseWriter, r *http.Request, code ErrorCode, fields []FieldError) {
	entry, known := errorTable[code]
	if !known {
		entry = errorTable[CodeInternal]
		code = CodeInternal
	}

	var body errorBody
	body.Error.Code = code
	body.Error.Message = entry.message
	body.Error.Fields = fields
	body.TraceID = observability.TraceID(r.Context())

	WriteJSON(w, entry.status, body)
}
```

- [ ] **Step 2: Rate limit com chave injetável (teste falha)**

Em `internal/httpapi/middleware/ratelimit_internal_test.go`, troque as duas chamadas `rateLimit(t.Context(), 60, 5, nil, teto)` e `rateLimit(t.Context(), 60, 1, nil, 1000)` por `rateLimit(t.Context(), 60, 5, porIP(nil), teto)` e `rateLimit(t.Context(), 60, 1, porIP(nil), 1000)`, e acrescente:

```go
// O limitador por usuário precisa falhar FECHADO. Montado por engano sem
// Authn na frente, ele não encontra usuário no contexto; cair num balde
// anônimo compartilhado seria silencioso. A requisição tem de ser recusada.
func TestRateLimitUserRefusesRequestsWithoutUser(t *testing.T) {
	h := rateLimit(t.Context(), 60, 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("sem usuário no contexto deu %d, queria 401", rec.Code)
	}
}
```

Run: `go test ./internal/httpapi/middleware/ -v` → FAIL de compilação: `undefined: porIP`, `porUsuario`.

- [ ] **Step 3: Implementar a chave injetável**

Em `internal/httpapi/middleware/ratelimit.go`:

1. Em `limiterState`, troque o campo `trusted []netip.Prefix` por `chave chaveFn`.
2. Substitua `RateLimit`, `rateLimit` e o início de `ServeHTTP` por:

```go
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
	return rateLimit(ctx, perMinute, burst, porIP(trusted), maxBuckets)
}

// RateLimitUser limita por usuário autenticado, a segunda camada da spec
// §5.7. Tem de ficar DEPOIS de Authn na cadeia: é Authn que põe o usuário no
// contexto. O limite por IP continua valendo por fora; este existe porque
// um usuário com token válido, atrás de muitos IPs, escaparia do outro.
func RateLimitUser(ctx context.Context, perMinute, burst int) func(http.Handler) http.Handler {
	return rateLimit(ctx, perMinute, burst, porUsuario, maxBuckets)
}

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

// rateLimit recebe o teto como parâmetro para que o teste possa exercitá-lo
// sem criar cem mil buckets.
func rateLimit(ctx context.Context, perMinute, burst int, chave chaveFn, max int) func(http.Handler) http.Handler {
	st := &limiterState{
		buckets: map[string]*bucket{},
		limit:   rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		chave:   chave,
		max:     max,
	}
	// (a goroutine de varredura e o return continuam como estão)
```

e, em `ServeHTTP`, troque a primeira linha (`chave := chaveDe(ClientIP(r, h.st.trusted))`) por:

```go
	chave, ok := h.st.chave(r)
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
```

Run: `go test -race ./internal/httpapi/middleware/ -v` → PASS.

Sabotagem: faça `porUsuario` devolver `"anon", true` sem usuário → `TestRateLimitUserRefusesRequestsWithoutUser` fica vermelho.

- [ ] **Step 4: `optional[T]` e a decodificação estrita**

Crie `internal/httpapi/optional.go`:

```go
package httpapi

import "encoding/json"

// optional distingue os três estados de um campo JSON: ausente, null e
// valor. *T colapsa os dois primeiros, e em degraded_threshold_ms eles
// significam coisas opostas: ausente é "use o padrão de 2000 ms", null é
// "desative a classificação degraded" (spec §11.5).
//
// Funciona porque encoding/json chama UnmarshalJSON também para null,
// quando o campo não é ponteiro; se a chave não aparece, não chama nada, e
// Set fica false.
type optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}
```

Em `internal/httpapi/authhandlers.go`, troque a assinatura de `decodeJSON` pelo trio abaixo, mantendo o corpo atual dentro de `decodeJSONWith`:

```go
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONWith(w, r, dst, false)
}

// decodeJSONStrict recusa campo desconhecido. Nas rotas de recurso, um campo
// com o nome errado ("intervalsec") seria ignorado em silêncio, e o cliente
// acharia que salvou.
func decodeJSONStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONWith(w, r, dst, true)
}

func decodeJSONWith(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		// ... o corpo atual de decodeJSON, sem mudança, a partir do
		//     `var tooLarge *http.MaxBytesError`
	}
	return true
}
```

O corpo do `if err != nil` é **exatamente** o de hoje, movido de lugar. Nada nele muda.

- [ ] **Step 5: Escrever os testes de integração (falham)**

Crie `internal/httpapi/targets_integration_test.go`:

```go
package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

func tokenDe(t *testing.T, a *api, userID uuid.UUID) string {
	t.Helper()
	tok, err := auth.NewAccessToken(testJWTSecret, userID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func outroUsuario(t *testing.T, a *api) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// chamar faz uma requisição com os headers que a cadeia exige: o anti-CSRF
// em mutação, o token quando houver.
func chamar(t *testing.T, a *api, metodo, caminho, token string, corpo any) *http.Response {
	t.Helper()
	var r io.Reader
	if corpo != nil {
		r = jsonBody(t, corpo)
	}
	req, err := http.NewRequest(metodo, a.srv.URL+caminho, r)
	if err != nil {
		t.Fatal(err)
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if metodo != http.MethodGet {
		req.Header.Set("X-Requested-With", "vigilume")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type alvoJSON struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	IntervalSec         int32     `json:"interval_sec"`
	DegradedThresholdMS *int32    `json:"degraded_threshold_ms"`
	HeaderNames         []string  `json:"header_names"`
	CreatedAt           time.Time `json:"created_at"`
}

// lerCorpo devolve o corpo cru (para procurar o que NÃO pode estar nele) e
// o decodifica em dst.
func lerCorpo(t *testing.T, resp *http.Response, dst any) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if dst != nil {
		if err := json.Unmarshal(b, dst); err != nil {
			t.Fatalf("corpo não é o JSON esperado: %v\n%s", err, b)
		}
	}
	return b
}

var alvoValido = map[string]any{
	"name":    "API",
	"url":     "https://api.exemplo.com/health",
	"headers": map[string]string{"Authorization": "Bearer segredo-do-cliente"},
}

func TestTargetCRUDFlow(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, lerCorpo(t, resp, nil))
	}
	var criado alvoJSON
	cru := lerCorpo(t, resp, &criado)
	if strings.Contains(string(cru), "segredo-do-cliente") {
		t.Fatal("a resposta do POST contém o valor do header (D5)")
	}
	if len(criado.HeaderNames) != 1 || criado.HeaderNames[0] != "Authorization" {
		t.Errorf("header_names = %v", criado.HeaderNames)
	}

	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil)
	cru = lerCorpo(t, resp, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(cru), "segredo-do-cliente") {
		t.Fatalf("GET deu %d ou vazou o header: %s", resp.StatusCode, cru)
	}

	resp = chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, map[string]any{"name": "Novo"})
	var editado alvoJSON
	lerCorpo(t, resp, &editado)
	if resp.StatusCode != http.StatusOK || editado.Name != "Novo" {
		t.Fatalf("PATCH deu %d, nome %q", resp.StatusCode, editado.Name)
	}

	resp = chamar(t, a, http.MethodDelete, "/api/v1/targets/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE deu %d", resp.StatusCode)
	}
	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET depois do DELETE deu %d, queria 404", resp.StatusCode)
	}
}

// Spec §5.5: recurso de outro usuário é 404, nunca 403, em toda rota.
func TestAnotherUsersTargetIs404OnEveryRoute(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", dono, alvoValido)
	var criado alvoJSON
	lerCorpo(t, resp, &criado)

	for _, c := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/targets/" + criado.ID},
		{http.MethodPatch, "/api/v1/targets/" + criado.ID},
		{http.MethodDelete, "/api/v1/targets/" + criado.ID},
		{http.MethodGet, "/api/v1/targets/" + criado.ID + "/checks"},
	} {
		var corpo any
		if c.metodo == http.MethodPatch {
			corpo = map[string]any{"name": "roubado"}
		}
		resp := chamar(t, a, c.metodo, c.caminho, intruso, corpo)
		env := decodeEnvelope(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || env.Error.Code != "not_found" {
			t.Errorf("%s %s pelo intruso deu %d/%s, queria 404/not_found", c.metodo, c.caminho, resp.StatusCode, env.Error.Code)
		}
	}

	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, dono, nil)
	var intacto alvoJSON
	lerCorpo(t, resp, &intacto)
	if intacto.Name != "API" {
		t.Errorf("o alvo do dono mudou para %q", intacto.Name)
	}
}

func TestTargetRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /targets sem token deu %d, queria 401", resp.StatusCode)
	}
}

func TestValidationErrorNamesTheFieldsWithoutEchoingInput(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tokenDe(t, a, a.userID), map[string]any{
		"name": "x", "url": "http://127.0.0.1:6379/", "interval_sec": 10,
	})
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"fields"`
		} `json:"error"`
	}
	cru := lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" {
		t.Fatalf("deu %d/%s", resp.StatusCode, env.Error.Code)
	}
	quer := map[string]string{"url": "forbidden", "interval_sec": "out_of_range"}
	for _, f := range env.Error.Fields {
		if quer[f.Field] == f.Reason {
			delete(quer, f.Field)
		}
	}
	if len(quer) != 0 {
		t.Errorf("faltaram %v em %s", quer, cru)
	}
	if strings.Contains(string(cru), "6379") {
		t.Errorf("a resposta ecoou a entrada: %s", cru)
	}
}

// degraded_threshold_ms: ausente → 2000, null → desativado, número → valor.
func TestDegradedThresholdAbsentNullAndValue(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	criar := func(extra map[string]any) alvoJSON {
		t.Helper()
		corpo := map[string]any{"name": "x", "url": "https://exemplo.com"}
		for k, v := range extra {
			corpo[k] = v
		}
		resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, corpo)
		var got alvoJSON
		lerCorpo(t, resp, &got)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST deu %d", resp.StatusCode)
		}
		return got
	}

	if got := criar(nil); got.DegradedThresholdMS == nil || *got.DegradedThresholdMS != 2000 {
		t.Errorf("ausente deu %v, queria 2000", got.DegradedThresholdMS)
	}
	if got := criar(map[string]any{"degraded_threshold_ms": nil}); got.DegradedThresholdMS != nil {
		t.Errorf("null deu %v, queria null", *got.DegradedThresholdMS)
	}
	comValor := criar(map[string]any{"degraded_threshold_ms": 500})
	if comValor.DegradedThresholdMS == nil || *comValor.DegradedThresholdMS != 500 {
		t.Errorf("500 deu %v", comValor.DegradedThresholdMS)
	}

	// PATCH com null desativa; PATCH sem o campo não mexe.
	resp := chamar(t, a, http.MethodPatch, "/api/v1/targets/"+comValor.ID, tok, map[string]any{"name": "y"})
	var semMexer alvoJSON
	lerCorpo(t, resp, &semMexer)
	if semMexer.DegradedThresholdMS == nil || *semMexer.DegradedThresholdMS != 500 {
		t.Errorf("PATCH sem o campo mexeu no limiar: %v", semMexer.DegradedThresholdMS)
	}
	resp = chamar(t, a, http.MethodPatch, "/api/v1/targets/"+comValor.ID, tok, map[string]any{"degraded_threshold_ms": nil})
	var desativado alvoJSON
	lerCorpo(t, resp, &desativado)
	if desativado.DegradedThresholdMS != nil {
		t.Errorf("PATCH null deu %v, queria null", *desativado.DegradedThresholdMS)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tokenDe(t, a, a.userID), map[string]any{
		"name": "x", "url": "https://exemplo.com", "intervalsec": 30,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("campo desconhecido deu %d, queria 400", resp.StatusCode)
	}
}

func TestMalformedIDIs404(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets/nao-e-uuid", tokenDe(t, a, a.userID), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("id malformado deu %d, queria 404 (D16)", resp.StatusCode)
	}
}

func TestListPaginatesWithNextCursor(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for range 3 {
		chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido).Body.Close()
	}

	var pagina struct {
		Items      []alvoJSON `json:"items"`
		NextCursor string     `json:"next_cursor"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets?limit=2", tok, nil), &pagina)
	if len(pagina.Items) != 2 || pagina.NextCursor == "" {
		t.Fatalf("primeira página: %d itens, cursor %q", len(pagina.Items), pagina.NextCursor)
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets?limit=2&cursor="+pagina.NextCursor, tok, nil), &pagina)
	if len(pagina.Items) != 1 || pagina.NextCursor != "" {
		t.Errorf("segunda página: %d itens, cursor %q; queria 1 e nenhum", len(pagina.Items), pagina.NextCursor)
	}

	resp := chamar(t, a, http.MethodGet, "/api/v1/targets?limit=101", tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("limit=101 deu %d, queria 400", resp.StatusCode)
	}
}

func TestChecksEndpoint(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	var criado alvoJSON
	lerCorpo(t, resp, &criado)
	alvoID := uuid.MustParse(criado.ID)

	kind := "timeout"
	for i, st := range []string{"up", "down"} {
		p := db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvoID,
			CheckedAt: a.clk.Now().Add(time.Duration(i-2) * time.Minute), Status: st, LatencyMS: 40,
		}
		if st == "down" {
			p.ErrorKind = &kind
		}
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	var got struct {
		Items []struct {
			Status    string  `json:"status"`
			ErrorKind *string `json:"error_kind"`
		} `json:"items"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID+"/checks", tok, nil), &got)
	if len(got.Items) != 2 || got.Items[0].Status != "up" || got.Items[1].ErrorKind == nil || *got.Items[1].ErrorKind != "timeout" {
		t.Errorf("checks = %+v", got.Items)
	}

	fim := a.clk.Now().UTC()
	caminho := fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
		fim.Add(-49*time.Hour).Format(time.RFC3339), fim.Format(time.RFC3339))
	resp = chamar(t, a, http.MethodGet, caminho, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("janela de 49h deu %d, queria 400 (D6)", resp.StatusCode)
	}
}

// Spec §5.7: um usuário esgota o SEU balde; outro usuário, no mesmo IP,
// continua passando. O limite por usuário (burst 20) é mais apertado que o
// por IP (burst 30) justamente para que este teste distinga os dois.
func TestPerUserRateLimit(t *testing.T) {
	a := newAPI(t)
	tokA := tokenDe(t, a, a.userID)
	tokB := tokenDe(t, a, outroUsuario(t, a))

	// Dispara até o 429, em vez de contar exatamente 21: o balde reabastece
	// 1 token por segundo, e sob -race num CI lento a 21ª poderia passar. O
	// teto de 25 fica abaixo do burst do IP (30), então quem barrou A foi o
	// limite por usuário.
	barrada := 0
	for i := 1; i <= 25; i++ {
		resp := chamar(t, a, http.MethodGet, "/api/v1/targets", tokA, nil)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			barrada = i
			break
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("requisição %d de A deu %d", i, resp.StatusCode)
		}
	}
	if barrada == 0 {
		t.Fatal("A fez 25 requisições sem receber 429: não há limite por usuário")
	}
	if barrada <= 20 {
		t.Errorf("A foi barrada na requisição %d, antes do burst de 20", barrada)
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets", tokB, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("B, no mesmo IP, deu %d depois de A esgotar o balde: o limite não é por usuário", resp.StatusCode)
	}
}
```

Em `internal/httpapi/auth_integration_test.go`, dentro de `newAPIWith`, troque a linha `srv := httptest.NewServer(httpapi.Routes(ctx, cfg, store, mgr, segundoFator, clk))` por:

```go
	srv := httptest.NewServer(httpapi.Routes(ctx, cfg, httpapi.Deps{
		Store: store, Sessions: mgr, TOTP: segundoFator,
		Targets: targets.NewService(pool, box, clk), Clock: clk,
	}))
```

e acrescente `"github.com/BipoXDXD/vigilume/internal/targets"` aos imports.

Run: `go test ./internal/httpapi/ -v` → FAIL de compilação: `undefined: httpapi.Deps`.

- [ ] **Step 6: Implementar os handlers**

Crie `internal/httpapi/targethandlers.go`:

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
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

type TargetHandlers struct {
	svc *targets.Service
	clk clock.Clock
}

func NewTargetHandlers(svc *targets.Service, clk clock.Clock) *TargetHandlers {
	return &TargetHandlers{svc: svc, clk: clk}
}

type targetRequest struct {
	Name                string            `json:"name"`
	URL                 string            `json:"url"`
	Method              string            `json:"method"`
	IntervalSec         int32             `json:"interval_sec"`
	TimeoutMS           int32             `json:"timeout_ms"`
	ExpectedStatus      int32             `json:"expected_status"`
	DegradedThresholdMS optional[int32]   `json:"degraded_threshold_ms"`
	Enabled             *bool             `json:"enabled"`
	Headers             map[string]string `json:"headers"`
}

type targetPatchRequest struct {
	Name                *string                     `json:"name"`
	URL                 *string                     `json:"url"`
	Method              *string                     `json:"method"`
	IntervalSec         *int32                      `json:"interval_sec"`
	TimeoutMS           *int32                      `json:"timeout_ms"`
	ExpectedStatus      *int32                      `json:"expected_status"`
	DegradedThresholdMS optional[int32]             `json:"degraded_threshold_ms"`
	Enabled             *bool                       `json:"enabled"`
	Headers             optional[map[string]string] `json:"headers"`
}

// targetResponse é o DTO de saída, construído campo a campo. Não existe
// campo para valor de header, então nenhuma mudança futura no domínio o faz
// aparecer aqui por acidente (D5).
type targetResponse struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	Method              string    `json:"method"`
	IntervalSec         int32     `json:"interval_sec"`
	TimeoutMS           int32     `json:"timeout_ms"`
	ExpectedStatus      int32     `json:"expected_status"`
	DegradedThresholdMS *int32    `json:"degraded_threshold_ms"`
	Enabled             bool      `json:"enabled"`
	HeaderNames         []string  `json:"header_names"`
	CreatedAt           time.Time `json:"created_at"`
}

type targetListResponse struct {
	Items      []targetResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type checkResponse struct {
	CheckedAt  time.Time `json:"checked_at"`
	Status     string    `json:"status"`
	LatencyMS  int32     `json:"latency_ms"`
	StatusCode *int32    `json:"status_code"`
	ErrorKind  *string   `json:"error_kind"`
}

func toTargetResponse(t targets.Target) targetResponse {
	return targetResponse{
		ID: t.ID.String(), Name: t.Name, URL: t.URL, Method: t.Method,
		IntervalSec: t.IntervalSec, TimeoutMS: t.TimeoutMS, ExpectedStatus: t.ExpectedStatus,
		DegradedThresholdMS: t.DegradedThresholdMS, Enabled: t.Enabled,
		HeaderNames: t.HeaderNames, CreatedAt: t.CreatedAt,
	}
}

func (h *TargetHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req targetRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}

	in := targets.Input{
		Name: req.Name, URL: req.URL, Method: req.Method,
		IntervalSec: req.IntervalSec, TimeoutMS: req.TimeoutMS, ExpectedStatus: req.ExpectedStatus,
		Enabled: true, Headers: req.Headers,
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	// Ausente → padrão; null → desativado; número → valor (spec §11.5).
	switch {
	case !req.DegradedThresholdMS.Set:
		v := targets.DefaultDegradedThresholdMS
		in.DegradedThresholdMS = &v
	case !req.DegradedThresholdMS.Null:
		v := req.DegradedThresholdMS.Value
		in.DegradedThresholdMS = &v
	}

	t, err := h.svc.Create(r.Context(), userID, in)
	if h.falhou(w, r, "criar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toTargetResponse(t))
}

func (h *TargetHandlers) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Get(r.Context(), userID, id)
	if h.falhou(w, r, "buscar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toTargetResponse(t))
}

func (h *TargetHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	limit := targets.DefaultPageSize
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 || n > int64(targets.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
		limit = int32(n)
	}
	var before *uuid.UUID
	if s := q.Get("cursor"); s != "" {
		c, err := uuid.Parse(s)
		if err != nil {
			campoInvalido(w, r, "cursor", targets.ReasonInvalid)
			return
		}
		before = &c
	}

	items, err := h.svc.List(r.Context(), userID, before, limit)
	if h.falhou(w, r, "listar alvos", err) {
		return
	}
	resp := targetListResponse{Items: make([]targetResponse, 0, len(items))}
	for _, t := range items {
		resp.Items = append(resp.Items, toTargetResponse(t))
	}
	// Página cheia pode ter continuação; página incompleta, certamente não.
	if len(items) == int(limit) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

func (h *TargetHandlers) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req targetPatchRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}

	p := targets.Patch{
		Name: req.Name, URL: req.URL, Method: req.Method,
		IntervalSec: req.IntervalSec, TimeoutMS: req.TimeoutMS, ExpectedStatus: req.ExpectedStatus,
		Enabled: req.Enabled,
	}
	if req.DegradedThresholdMS.Set {
		p.SetDegradedThreshold = true
		if !req.DegradedThresholdMS.Null {
			v := req.DegradedThresholdMS.Value
			p.DegradedThresholdMS = &v
		}
	}
	if req.Headers.Set {
		p.SetHeaders = true
		p.Headers = req.Headers.Value // null deixa Value nil: limpa
	}

	t, err := h.svc.Update(r.Context(), userID, id, p)
	if h.falhou(w, r, "atualizar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toTargetResponse(t))
}

func (h *TargetHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "apagar alvo", h.svc.Delete(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TargetHandlers) Checks(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	to := h.clk.Now()
	if s := q.Get("to"); s != "" {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			campoInvalido(w, r, "to", targets.ReasonInvalid)
			return
		}
		to = v
	}
	from := to.Add(-24 * time.Hour)
	if s := q.Get("from"); s != "" {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			campoInvalido(w, r, "from", targets.ReasonInvalid)
			return
		}
		from = v
	}

	checks, err := h.svc.Checks(r.Context(), userID, id, from, to)
	if h.falhou(w, r, "listar checks", err) {
		return
	}
	items := make([]checkResponse, 0, len(checks))
	for _, c := range checks {
		items = append(items, checkResponse{
			CheckedAt: c.CheckedAt, Status: c.Status, LatencyMS: c.LatencyMS,
			StatusCode: c.StatusCode, ErrorKind: c.ErrorKind,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// falhou traduz o erro do domínio para o envelope e devolve true se havia
// erro. O detalhe de um erro inesperado vai só para o log, correlacionado
// pelo trace_id (spec §9.1).
func (h *TargetHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *targets.ValidationError
	switch {
	case errors.Is(err, targets.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.As(err, &verr):
		campos := make([]apierr.FieldError, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			campos = append(campos, apierr.FieldError{Field: f.Field, Reason: f.Reason})
		}
		apierr.WriteValidation(w, r, campos)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}

func usuario(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	}
	return id, ok
}

// pathID lê o {id} da rota. Malformado é 404 (D16): o recurso não existe de
// jeito nenhum, e um 400 ensinaria o formato do id a quem varre.
func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func campoInvalido(w http.ResponseWriter, r *http.Request, campo, motivo string) {
	apierr.WriteValidation(w, r, []apierr.FieldError{{Field: campo, Reason: motivo}})
}
```

- [ ] **Step 7: Montar as rotas**

Em `internal/httpapi/routes.go`, acrescente o import de `targets` e troque a assinatura e o início de `Routes` por:

```go
// Deps reúne o que as rotas consomem. Virou struct quando o sétimo
// parâmetro chegou: a lista posicional já não se lia.
type Deps struct {
	Store    *auth.PGStore
	Sessions *auth.Manager
	TOTP     *auth.TOTP
	Targets  *targets.Service
	Clock    clock.Clock
}

// Routes monta a árvore de rotas e a cadeia de middlewares.
//
// ctx é o contexto de vida do servidor: ele encerra as goroutines de
// varredura dos rate limiters no shutdown, em vez de deixá-las vazar (R32).
func Routes(ctx context.Context, cfg *config.Config, d Deps) http.Handler {
	secureCookie := strings.HasPrefix(cfg.PublicBaseURL, "https://")
	h := NewAuthHandlers(d.Store, d.Sessions, d.TOTP, d.Clock, cfg.JWTSecret, secureCookie, cfg.TrustedProxies)
	th := NewTargetHandlers(d.Targets, d.Clock)
	authn := middleware.Authn(cfg.JWTSecret, d.Clock)
```

Mantenha o comentário que explicava `secureCookie`. Depois da linha `mux.Handle("GET /api/v1/me", ...)`, acrescente:

```go
	// Rotas de recurso: Authn primeiro, depois o limite por USUÁRIO (spec
	// §5.7), que precisa do usuário que Authn põe no contexto. Uma instância
	// só, compartilhada pelas rotas: o orçamento é do usuário, não da rota.
	//
	// 60/min com burst 20 é mais apertado que o por IP (120/min, burst 30)
	// de propósito: um usuário sozinho esgota o seu antes de esgotar o do
	// IP, e é isso que torna a camada observável (TestPerUserRateLimit).
	porUsuario := middleware.RateLimitUser(ctx, 60, 20)
	protegida := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(fn)) }

	mux.Handle("GET /api/v1/targets", protegida(th.List))
	mux.Handle("POST /api/v1/targets", protegida(th.Create))
	mux.Handle("GET /api/v1/targets/{id}", protegida(th.Get))
	mux.Handle("PATCH /api/v1/targets/{id}", protegida(th.Update))
	mux.Handle("DELETE /api/v1/targets/{id}", protegida(th.Delete))
	mux.Handle("GET /api/v1/targets/{id}/checks", protegida(th.Checks))
```

Em `cmd/vigilume/main.go`, dentro de `run`, troque a linha `srv := httpapi.NewServer(cfg, httpapi.Routes(ctx, cfg, store, sessions, totp, clk))` por:

```go
	deps := httpapi.Deps{
		Store: store, Sessions: sessions, TOTP: totp,
		Targets: targets.NewService(pool, box, clk), Clock: clk,
	}
	srv := httpapi.NewServer(cfg, httpapi.Routes(ctx, cfg, deps))
```

e acrescente o import de `targets`. A Task 9 reescreve `run` inteira; aqui é só o necessário para compilar.

- [ ] **Step 8: Rodar e ver passar**

Run: `go build ./... && go test -race -count=1 ./internal/httpapi/... ./internal/apierr/... -v`
Expected: PASS, inclusive toda a suíte de auth que já existia.

- [ ] **Step 9: Provar por sabotagem**

| Sabotagem | Teste que fica vermelho |
|---|---|
| Acrescentar `Headers map[string]string \`json:"headers"\`` em `targetResponse` e preenchê-lo | `TestTargetCRUDFlow` |
| Trocar `protegida(th.List)` por `th.List` (sem Authn) | `TestTargetRoutesRequireAuthentication` |
| Trocar `porUsuario` por `middleware.RateLimit(ctx, 60, 20, cfg.TrustedProxies)` (por IP) | `TestPerUserRateLimit` (B recebe 429) |
| Remover `porUsuario` de `protegida` | `TestPerUserRateLimit` (A faz 25 sem 429) |
| Trocar `optional[int32]` por `*int32` em `targetRequest` e ajustar o switch | `TestDegradedThresholdAbsentNullAndValue` |
| Usar `decodeJSON` em vez de `decodeJSONStrict` no `Create` | `TestUnknownFieldIsRejected` |
| Em `pathID`, responder `CodeValidation` | `TestMalformedIDIs404` |

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/ cmd/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
go test -race -count=1 ./...
git add internal/apierr/ internal/httpapi/ cmd/vigilume/
git commit -m "feat(httpapi): CRUD de alvos, erros por campo e rate limit por usuário"
```

---
### Task 6: Uma sonda — requisição, corpo limitado, classificação

Pura em relação ao banco e roda sem Docker. Os testes batem em servidores `httptest` reais, liberados pela `NewClientAllowing` da Task 1.

**Files:**
- Create: `internal/prober/probe.go`
- Create: `internal/prober/classify.go`
- Test: `internal/prober/classify_test.go` (pacote `prober`)
- Test: `internal/prober/probe_test.go` (pacote `prober`)

**Interfaces:**
- Consumes: `safedial.NewClient`, `safedial.NewClientAllowing`, `safedial.ErrBlocked`, `safedial.ErrInvalidURL`, `safedial.MaxBodyBytes` (Task 1).
- Produces:
  - `prober.Status` (`StatusUp`, `StatusDegraded`, `StatusDown`, com valores `"up"`, `"degraded"`, `"down"`).
  - `prober.ErrorKind`: `KindTimeout`, `KindDNS`, `KindTLS`, `KindConnRefused`, `KindHTTPStatus`, `KindBodyLimit`, `KindBlocked`, `KindUnknown`. Os valores são as strings do `CHECK` da migration 00004.
  - `prober.Spec{URL, Method string; Timeout time.Duration; ExpectedStatus int; DegradedThreshold time.Duration; Headers map[string]string}`. `DegradedThreshold` 0 desliga a classificação degraded.
  - `prober.Result{Status Status; Latency time.Duration; StatusCode int; ErrorKind ErrorKind}`. `StatusCode` é 0 sem resposta; `ErrorKind` é `""` quando não é `down`.
  - `prober.Probe(ctx context.Context, client *http.Client, s Spec) Result`.
  - `prober.Classify(err error) ErrorKind`.

**Como o Go embrulha cada falha.** Medido com servidores reais antes do plano; não redescobrir:

| Falha | O que chega | Como reconhecer |
|---|---|---|
| Cabeçalho não chega no prazo | `*url.Error` → `context.DeadlineExceeded` | `errors.Is(err, context.DeadlineExceeded)` |
| Corpo para no meio, prazo estoura | `context.deadlineExceededError` cru, sem `*url.Error` | o mesmo `errors.Is` |
| Conexão recusada | `*url.Error` → `*net.OpError` → `ECONNREFUSED` | `errors.Is(err, syscall.ECONNREFUSED)` |
| Certificado de autoridade desconhecida | `*tls.CertificateVerificationError` | `errors.As` |
| **Cancelado com causa (shutdown)** | **a própria causa**; `errors.Is(err, context.Canceled)` é **false** | **não reconhecer pelo erro.** A Task 7 olha `context.Cause` do contexto |
| Bloqueado pelo `safedial` | envolve `safedial.ErrBlocked` | `errors.Is` |

- [ ] **Step 1: Escrever o teste de classificação (falha)**

Crie `internal/prober/classify_test.go`:

```go
package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

func TestClassify(t *testing.T) {
	embrulhar := func(err error) error { return &url.Error{Op: "Get", URL: "https://x", Err: err} }

	cases := []struct {
		nome string
		err  error
		quer ErrorKind
	}{
		{"bloqueado no dial", embrulhar(&net.OpError{Op: "dial", Err: fmt.Errorf("%w: 10.0.0.5", safedial.ErrBlocked)}), KindBlocked},
		{"redirect para file://", embrulhar(fmt.Errorf("%w: esquema", safedial.ErrInvalidURL)), KindBlocked},
		{"prazo no cabeçalho", embrulhar(context.DeadlineExceeded), KindTimeout},
		{"prazo no corpo, sem url.Error", context.DeadlineExceeded, KindTimeout},
		{"DNS inexistente", embrulhar(&net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}), KindDNS},
		{"DNS que demorou", embrulhar(&net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}), KindTimeout},
		{"conexão recusada", embrulhar(&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), KindConnRefused},
		{"certificado desconhecido", embrulhar(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), KindTLS},
		{"cabeçalho TLS inválido", embrulhar(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), KindTLS},
		{"hostname errado no certificado", embrulhar(x509.HostnameError{Host: "x"}), KindTLS},
		{"conexão cortada no meio", embrulhar(io.ErrUnexpectedEOF), KindUnknown},
		{"reset", embrulhar(&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), KindUnknown},
		{"qualquer outro", errors.New("??"), KindUnknown},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if got := Classify(c.err); got != c.quer {
				t.Errorf("Classify = %q, queria %q", got, c.quer)
			}
		})
	}
}
```

Run: `go test ./internal/prober/ -run TestClassify -v` → FAIL de compilação: `undefined: Classify`.

- [ ] **Step 2: Implementar os tipos e a classificação**

Crie `internal/prober/classify.go`:

```go
package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

type ErrorKind string

// Os valores são os do CHECK de checks.error_kind (migration 00004). A
// mensagem crua do erro nunca sai do processo (spec §4.2, §5.6): uma string
// como "dial tcp 10.0.3.42:5432: connect: connection refused" publicaria
// topologia interna. Só o tipo sai.
const (
	KindTimeout     ErrorKind = "timeout"
	KindDNS         ErrorKind = "dns"
	KindTLS         ErrorKind = "tls"
	KindConnRefused ErrorKind = "conn_refused"
	KindHTTPStatus  ErrorKind = "http_status"
	// KindBodyLimit está no enum da spec, e o Plano 2 nunca o emite: corpo
	// grande é lido até 64 KiB e descartado, não é falha (D2).
	KindBodyLimit ErrorKind = "body_limit"
	KindBlocked   ErrorKind = "blocked"
	// KindUnknown é o residual (D1): reset, EOF no meio da resposta, erro de
	// protocolo. Sem ele, cada um teria de fingir ser outra coisa.
	KindUnknown ErrorKind = "unknown"
)

// Classify reduz um erro de rede ao enum. A ordem importa: o bloqueio vem
// primeiro porque o erro do safedial chega embrulhado num *net.OpError, e o
// DNS antes do timeout genérico porque *net.DNSError também responde
// Timeout().
//
// Cancelamento com causa (o shutdown da Task 7) NÃO é reconhecido aqui, e de
// propósito: nesse caso o erro devolvido é a própria causa, e não
// context.Canceled. Quem cancelou é que sabe, olhando context.Cause.
func Classify(err error) ErrorKind {
	if errors.Is(err, safedial.ErrBlocked) || errors.Is(err, safedial.ErrInvalidURL) {
		return KindBlocked
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return KindTimeout
		}
		return KindDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return KindConnRefused
	}
	if isTLS(err) {
		return KindTLS
	}
	return KindUnknown
}

func isTLS(err error) bool {
	var (
		verificacao *tls.CertificateVerificationError
		registro    tls.RecordHeaderError
		alerta      tls.AlertError
		autoridade  x509.UnknownAuthorityError
		hostname    x509.HostnameError
		invalido    x509.CertificateInvalidError
	)
	return errors.As(err, &verificacao) || errors.As(err, &registro) ||
		errors.As(err, &alerta) || errors.As(err, &autoridade) ||
		errors.As(err, &hostname) || errors.As(err, &invalido)
}
```

Run: `go test ./internal/prober/ -run TestClassify -v` → PASS.

Sabotagem: remova a checagem de `safedial.ErrBlocked`/`ErrInvalidURL` do topo da função. O caso `bloqueado no dial` fica vermelho (cai em `unknown`), e o `redirect para file://` também. Registre.

- [ ] **Step 3: Escrever os testes da sonda (falham)**

Crie `internal/prober/probe_test.go`:

```go
package prober

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

func enderecoDe(t *testing.T, srv *httptest.Server) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

// servidor sobe um httptest liberado no safedial e devolve o cliente que o
// alcança. Nenhum outro endereço fica liberado.
func servidor(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, safedial.NewClientAllowing(0, enderecoDe(t, srv))
}

func especificacao(url string) Spec {
	return Spec{URL: url, Method: http.MethodGet, Timeout: 2 * time.Second, ExpectedStatus: http.StatusOK}
}

func TestProbeUp(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	res := Probe(t.Context(), client, especificacao(srv.URL))
	if res.Status != StatusUp || res.StatusCode != 200 || res.ErrorKind != "" {
		t.Errorf("res = %+v, queria up/200/sem erro", res)
	}
}

func TestProbeUnexpectedStatusIsDown(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	res := Probe(t.Context(), client, especificacao(srv.URL))
	if res.Status != StatusDown || res.ErrorKind != KindHTTPStatus || res.StatusCode != 500 {
		t.Errorf("res = %+v, queria down/http_status/500", res)
	}
}

func TestProbeExpectedStatusOtherThan200(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s := especificacao(srv.URL)
	s.ExpectedStatus = http.StatusNoContent
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Errorf("res = %+v, queria up", res)
	}
}

// Spec §11.5: sucesso lento é degraded; limiar zero desliga a classificação.
func TestProbeDegradedAboveThreshold(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	s := especificacao(srv.URL)
	s.DegradedThreshold = 50 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDegraded || res.ErrorKind != "" {
		t.Errorf("com limiar de 50ms: %+v, queria degraded sem erro", res)
	}
	s.DegradedThreshold = 0
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Errorf("com limiar desligado: %+v, queria up", res)
	}
}

// Os handlers que "nunca respondem" esperam no contexto da requisição ou em
// parar. O close(parar) é registrado DEPOIS de servidor(), e cleanups rodam
// em ordem inversa: o canal fecha antes do srv.Close, que senão ficaria
// preso esperando o handler.
func TestProbeTimeoutWaitingForHeaders(t *testing.T) {
	parar := make(chan struct{})
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-parar:
		}
	})
	t.Cleanup(func() { close(parar) })

	s := especificacao(srv.URL)
	s.Timeout = 200 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout {
		t.Errorf("res = %+v, queria down/timeout", res)
	}
}

// O prazo vale também para o corpo: um servidor que manda o cabeçalho e
// para prenderia a sonda para sempre.
func TestProbeTimeoutWhileReadingBody(t *testing.T) {
	parar := make(chan struct{})
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-parar:
		}
	})
	t.Cleanup(func() { close(parar) })

	s := especificacao(srv.URL)
	s.Timeout = 200 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout {
		t.Errorf("res = %+v, queria down/timeout", res)
	}
}

// D2 e spec §5.1: um corpo sem fim é lido até 64 KiB e descartado, e a sonda
// termina bem. Sem o limite, ela leria até o prazo estourar e daria timeout.
func TestProbeCapsAnEndlessBodyWithoutFailing(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		bloco := []byte(strings.Repeat("x", 32<<10))
		for {
			if _, err := w.Write(bloco); err != nil {
				return
			}
		}
	})
	s := especificacao(srv.URL)
	s.Timeout = 3 * time.Second
	res := Probe(t.Context(), client, s)
	if res.Status != StatusUp {
		t.Fatalf("res = %+v, queria up: o corpo não foi limitado", res)
	}
	if res.Latency >= s.Timeout {
		t.Errorf("a sonda levou %v, o prazo inteiro", res.Latency)
	}
}

func TestProbeFollowsRedirectWithinLimit(t *testing.T) {
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(destino.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destino.URL, http.StatusFound)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, destino))

	if res := Probe(t.Context(), client, especificacao(origem.URL)); res.Status != StatusUp {
		t.Errorf("res = %+v, queria up depois de um redirect", res)
	}
}

// O handler redireciona para o próprio Host da requisição. Ler srv.URL de
// dentro dele seria corrida de dados: a variável é escrita pela goroutine do
// teste, e o detector não enxerga a rede como sincronização.
func TestProbeRedirectLoopIsBlocked(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/", http.StatusFound)
	})
	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria blocked depois de 3 saltos", res)
	}
}

func TestProbeSendsConfiguredHeadersAndUserAgent(t *testing.T) {
	// Canal, e não variável: o handler roda em outra goroutine, e o detector
	// de corrida não conta a resposta HTTP como sincronização.
	cabecalhos := make(chan http.Header, 1)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		cabecalhos <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})
	s := especificacao(srv.URL)
	s.Headers = map[string]string{"Authorization": "Bearer abc"}
	Probe(t.Context(), client, s)
	recebido := <-cabecalhos

	if recebido.Get("Authorization") != "Bearer abc" {
		t.Errorf("Authorization = %q", recebido.Get("Authorization"))
	}
	if !strings.HasPrefix(recebido.Get("User-Agent"), "Vigilume/") {
		t.Errorf("User-Agent = %q", recebido.Get("User-Agent"))
	}
}

func TestProbeConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(l.Addr().String())
	l.Close()

	res := Probe(t.Context(), safedial.NewClientAllowing(0, ap), especificacao("http://"+ap.String()))
	if res.Status != StatusDown || res.ErrorKind != KindConnRefused {
		t.Errorf("res = %+v, queria down/conn_refused", res)
	}
}

func TestProbeUntrustedCertificateIsTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, srv))

	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.ErrorKind != KindTLS {
		t.Errorf("res = %+v, queria tls", res)
	}
}

// O cliente de produção bloqueia o httptest, que escuta em loopback.
func TestProbeWithProductionClientIsBlocked(t *testing.T) {
	srv, _ := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	res := Probe(t.Context(), safedial.NewClient(0), especificacao(srv.URL))
	if res.Status != StatusDown || res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria down/blocked", res)
	}
}
```

Run: `go test ./internal/prober/ -v` → FAIL de compilação: `undefined: Probe`.

- [ ] **Step 4: Implementar a sonda**

Crie `internal/prober/probe.go`:

```go
// Package prober executa as sondas e as agenda (spec §6).
package prober

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

type Status string

const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

const userAgent = "Vigilume/1.0 (+monitor de disponibilidade)"

// Spec é o que uma sonda precisa saber sobre o alvo, já decifrado.
type Spec struct {
	URL               string
	Method            string
	Timeout           time.Duration
	ExpectedStatus    int
	DegradedThreshold time.Duration // 0: sem classificação degraded
	Headers           map[string]string
}

// Result é o que sai de uma sonda. Não tem campo para mensagem de erro, e é
// isso que impede que uma mensagem crua chegue ao banco (spec §4.2).
type Result struct {
	Status     Status
	Latency    time.Duration
	StatusCode int       // 0 quando não houve resposta
	ErrorKind  ErrorKind // "" quando não é down
}

// Probe faz uma requisição e a classifica. Nunca devolve erro: toda falha é
// um resultado, e o resultado é o produto.
//
// O prazo é do context, e vale para cabeçalho E corpo. A latência vai do
// envio ao fim da leitura do corpo, limitada a 64 KiB (spec §5.1). Ela é
// medida pelo relógio monotônico real, e não pelo relógio injetado, porque
// o relógio falso dos testes não anda durante a sonda (D11).
func Probe(ctx context.Context, client *http.Client, s Spec) Result {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, s.Method, s.URL, nil)
	if err != nil {
		// A URL já foi validada no cadastro; chegar aqui é defeito, e o
		// resultado honesto é "não deu para sondar".
		return Result{Status: StatusDown, ErrorKind: KindUnknown}
	}
	req.Header.Set("User-Agent", userAgent)
	for nome, valor := range s.Headers {
		req.Header.Set(nome, valor)
	}

	inicio := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Status: StatusDown, Latency: time.Since(inicio), ErrorKind: Classify(err)}
	}
	defer resp.Body.Close()

	// Corpo maior que o limite não é falha (D2): lê-se até 64 KiB e fecha.
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, safedial.MaxBodyBytes))
	latencia := time.Since(inicio)
	if err != nil {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: Classify(err)}
	}

	if resp.StatusCode != s.ExpectedStatus {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: KindHTTPStatus}
	}
	if s.DegradedThreshold > 0 && latencia > s.DegradedThreshold {
		return Result{Status: StatusDegraded, Latency: latencia, StatusCode: resp.StatusCode}
	}
	return Result{Status: StatusUp, Latency: latencia, StatusCode: resp.StatusCode}
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `go test -race -count=1 ./internal/prober/... -v`
Expected: PASS em tudo, inclusive `safedial`.

- [ ] **Step 6: Provar por sabotagem**

| Sabotagem | Teste que fica vermelho |
|---|---|
| Trocar `io.LimitReader(resp.Body, safedial.MaxBodyBytes)` por `resp.Body` | `TestProbeCapsAnEndlessBodyWithoutFailing` |
| Remover `s.DegradedThreshold > 0 &&` da condição de degraded | `TestProbeDegradedAboveThreshold`, no caso do limiar desligado |
| Remover o laço que copia `s.Headers` | `TestProbeSendsConfiguredHeadersAndUserAgent` |
| Trocar `context.WithTimeout(ctx, s.Timeout)` por `ctx` | `TestProbeTimeoutWaitingForHeaders` e `TestProbeTimeoutWhileReadingBody` (param no timeout de 10 min do `go test`; rode com `-timeout 30s`) |

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/ && go vet ./internal/prober/... && staticcheck ./internal/prober/...
git add internal/prober/
git commit -m "feat(prober): sonda com corpo limitado e erro reduzido ao enum error_kind"
```

---
### Task 7: Agendador — tick, semáforo, lease, persistência e shutdown

**Files:**
- Modify: `go.mod`, `go.sum` (`prometheus/client_golang`; `golang.org/x/sync` passa a direto)
- Create: `internal/prober/metrics.go`
- Create: `internal/prober/scheduler.go`
- Test: `internal/prober/scheduler_test.go` (pacote `prober`, Postgres)

**Interfaces:**
- Consumes: `storage.InProberTx`, `storage.InUserTx`, `storage.EnsureChecksPartitions`, pacote `db` (Task 3); `targets.OpenHeaders`, `targets.SealHeaders` (Task 4); `Probe`, `Spec`, `Result` (Task 6); `safedial.NewClient`, `safedial.NewClientAllowing` (Task 1); `clock.Clock`, `crypto.Box`.
- Produces:
  - `prober.NewMetrics(reg prometheus.Registerer) *Metrics`, que registra `probe_duration_seconds{target_id}`, `probe_total{status}`, `scheduler_inflight` e `scheduler_queue_depth`.
  - `prober.MaxInFlight int32 = 64`.
  - `prober.New(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, m *Metrics) *Scheduler`, **sempre com o cliente de produção** (`safedial.NewClient(0)`).
  - `(*Scheduler).Run(ctx context.Context, grace time.Duration) error`: tick de 1s até `ctx` acabar, depois `Stop(grace)`.
  - `(*Scheduler).Tick(ctx context.Context) (int, error)`: um despacho. Devolve quantos alvos disparou.
  - `(*Scheduler).Stop(grace time.Duration)`: espera as sondas em voo por até `grace`, então as aborta e as descarta.

**Contexto para quem implementa.**
- **A reivindicação é um lease.** O `UPDATE ... RETURNING` grava o próximo `next_run_at` e faz commit **antes** de discar. Se o processo morrer no meio da sonda, o alvo volta no próximo intervalo, sem trava pendurada.
- **As vagas são reservadas antes da reivindicação.** Reivindicar é empurrar `next_run_at` para frente; um alvo reivindicado sem vaga ficaria um intervalo inteiro sem ser sondado.
- **O shutdown é detectado por `context.Cause(s.sondas)`, nunca pelo erro da sonda.** Com `WithCancelCause`, o erro que o `http.Client` devolve é a própria causa, e `errors.Is(err, context.Canceled)` é `false` (medido; ver a tabela da Task 6).
- **O alvo apagado com a sonda em voo aparece como `42501` (RLS) no `INSERT`, antes da FK `23503`** (`TestCheckForVanishedTargetFailsWithRLSCode`, Task 3).
- **Sem `time.Sleep` nos testes** (spec §6.2). Os testes chamam `Tick` e `Stop` diretamente, avançam o relógio falso e coordenam os servidores por canais.

- [ ] **Step 1: Dependências**

```bash
go get github.com/prometheus/client_golang@v1.24.1
go get golang.org/x/sync@v0.23.0
```

`golang.org/x/sync` já está no `go.mod` como indireta; o `go get` a promove a direta.

- [ ] **Step 2: Escrever os testes (falham)**

Crie `internal/prober/scheduler_test.go`:

```go
package prober

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

const esperaMaxima = 5 * time.Second

type bancada struct {
	s    *Scheduler
	pool *pgxpool.Pool
	clk  *clock.Fake
	box  *crypto.Box
	m    *Metrics
	dono uuid.UUID
}

// novaBancada monta um agendador sobre o Postgres de teste. client nil usa
// o cliente de PRODUÇÃO (New); senão, newScheduler com o cliente dado.
func novaBancada(t *testing.T, client *http.Client, vagas int32) *bancada {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	if err := storage.EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}

	m := NewMetrics(prometheus.NewRegistry())
	var s *Scheduler
	if client == nil {
		s = New(pool, box, clk, m)
	} else {
		s = newScheduler(pool, box, clk, m, client, vagas)
	}
	// Registrado depois do testdb.New: cleanups rodam em ordem inversa, então
	// as sondas terminam antes de o banco ser restaurado.
	t.Cleanup(func() { s.Stop(esperaMaxima) })
	return &bancada{s: s, pool: pool, clk: clk, box: box, m: m, dono: dono}
}

func (b *bancada) alvo(t *testing.T, url string, mexer func(*db.InsertTargetParams)) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	p := db.InsertTargetParams{
		ID: id, UserID: b.dono, Name: "alvo", URL: url, Method: "GET",
		IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true,
		NextRunAt: b.clk.Now(), CreatedAt: b.clk.Now(),
	}
	if mexer != nil {
		mexer(&p)
	}
	if err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error { return q.InsertTarget(ctx, p) }); err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) checks(t *testing.T, alvo uuid.UUID) []db.ListChecksRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListChecksRow
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListChecks(ctx, db.ListChecksParams{
			UserID: b.dono, TargetID: alvo, FromAt: b.clk.Now().Add(-time.Hour), ToAt: b.clk.Now().Add(time.Hour),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (b *bancada) proximaExecucao(t *testing.T, alvo uuid.UUID) time.Time {
	t.Helper()
	ctx := context.Background()
	var next time.Time
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		row, err := q.GetTarget(ctx, db.GetTargetParams{UserID: b.dono, ID: alvo})
		next = row.NextRunAt
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// servidorLiberado sobe um httptest e devolve o cliente que o alcança.
func servidorLiberado(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, safedial.NewClientAllowing(0, enderecoDe(t, srv))
}

func esperar(t *testing.T, c <-chan struct{}, oque string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(esperaMaxima):
		t.Fatalf("esperando %s", oque)
	}
}

func TestTickProbesDueTargetAndRecordsCheck(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	n, err := b.s.Tick(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1 disparo", n, err)
	}
	b.s.Stop(esperaMaxima)

	rows := b.checks(t, alvo)
	if len(rows) != 1 {
		t.Fatalf("%d checks gravados, queria 1", len(rows))
	}
	c := rows[0]
	if c.Status != "up" || c.StatusCode == nil || *c.StatusCode != 200 || c.ErrorKind != nil {
		t.Errorf("check = %+v", c)
	}
	// D11: checked_at vem do relógio injetado.
	if !c.CheckedAt.Equal(b.clk.Now()) {
		t.Errorf("checked_at = %v, queria o relógio injetado %v", c.CheckedAt, b.clk.Now())
	}
	if v := testutil.ToFloat64(b.m.total.WithLabelValues("up")); v != 1 {
		t.Errorf("probe_total{status=up} = %v", v)
	}
	if v := testutil.ToFloat64(b.m.inflight); v != 0 {
		t.Errorf("scheduler_inflight = %v depois do Stop", v)
	}
}

func TestTickIgnoresTargetsNotDueOrDisabled(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) { p.NextRunAt = b.clk.Now().Add(time.Minute) })
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) { p.Enabled = false; p.NextRunAt = b.clk.Now().Add(-time.Hour) })

	if n, err := b.s.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("Tick = %d, %v; queria 0: nada vencido e ativo", n, err)
	}
	b.clk.Advance(2 * time.Minute)
	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1: só o ativo venceu", n, err)
	}
}

// Spec §6.2: sem jitter, alvos de intervalo igual disparam no mesmo segundo
// para sempre.
func TestNextRunIsJitteredWithinTenPercent(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	const total = 50
	alvos := make([]uuid.UUID, total)
	for i := range alvos {
		alvos[i] = b.alvo(t, srv.URL, nil)
	}
	agora := b.clk.Now()

	if n, err := b.s.Tick(context.Background()); err != nil || n != total {
		t.Fatalf("Tick = %d, %v", n, err)
	}
	b.s.Stop(esperaMaxima)

	distintos := map[time.Duration]bool{}
	for _, a := range alvos {
		d := b.proximaExecucao(t, a).Sub(agora)
		if d < 54*time.Second || d > 66*time.Second {
			t.Errorf("próxima execução em %v, fora de 60s ±10%%", d)
		}
		distintos[d] = true
	}
	if len(distintos) < total/2 {
		t.Errorf("só %d valores distintos em %d alvos: não há jitter", len(distintos), total)
	}
}

// Reservar as vagas antes de reivindicar: um alvo reivindicado sem vaga teria
// o next_run_at empurrado sem ter sido sondado.
func TestTickNeverClaimsMoreThanFreeSlots(t *testing.T) {
	liberar := make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-liberar:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, 2)
	var alvos []uuid.UUID
	for range 5 {
		alvos = append(alvos, b.alvo(t, srv.URL, nil))
	}
	agora := b.clk.Now()

	if n, err := b.s.Tick(context.Background()); err != nil || n != 2 {
		t.Fatalf("primeiro Tick = %d, %v; queria 2, o número de vagas", n, err)
	}
	if n, err := b.s.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("segundo Tick = %d, %v; queria 0, sem vaga livre", n, err)
	}

	vencidos := 0
	for _, a := range alvos {
		if !b.proximaExecucao(t, a).After(agora) {
			vencidos++
		}
	}
	if vencidos != 3 {
		t.Errorf("%d alvos continuam vencidos, queria 3: alvo reivindicado sem vaga", vencidos)
	}
	if v := testutil.ToFloat64(b.m.queueDepth); v != 3 {
		t.Errorf("scheduler_queue_depth = %v, queria 3", v)
	}

	close(liberar)
	b.s.Stop(esperaMaxima)
}

// Spec §6.2 e §14: dois sondadores sobre o mesmo banco nunca sondam o mesmo
// alvo duas vezes.
func TestTwoSchedulersNeverProbeTheSameTarget(t *testing.T) {
	var batidas sync.Map // caminho → *atomic.Int32
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		c, _ := batidas.LoadOrStore(r.URL.Path, new(atomic.Int32))
		c.(*atomic.Int32).Add(1)
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	outro := newScheduler(b.pool, b.box, b.clk, NewMetrics(prometheus.NewRegistry()), client, MaxInFlight)
	t.Cleanup(func() { outro.Stop(esperaMaxima) })

	const total = 40
	// Um caminho por alvo, para o servidor contar as batidas de cada um.
	for i := range total {
		b.alvo(t, fmt.Sprintf("%s/alvo-%d", srv.URL, i), nil)
	}

	var largada sync.WaitGroup
	largada.Add(1)
	var soma atomic.Int32
	var fim sync.WaitGroup
	for _, s := range []*Scheduler{b.s, outro} {
		fim.Add(1)
		go func() {
			defer fim.Done()
			largada.Wait()
			n, err := s.Tick(context.Background())
			if err != nil {
				t.Error(err)
			}
			soma.Add(int32(n))
		}()
	}
	largada.Done()
	fim.Wait()
	b.s.Stop(esperaMaxima)
	outro.Stop(esperaMaxima)

	if soma.Load() != total {
		t.Errorf("os dois dispararam %d, queria %d", soma.Load(), total)
	}
	batidas.Range(func(caminho, c any) bool {
		if n := c.(*atomic.Int32).Load(); n != 1 {
			t.Errorf("%s sondado %d vezes", caminho, n)
		}
		return true
	})
}

// logSeguro é um buffer que aceita escrita concorrente: o agendador loga de
// goroutines de sonda.
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

func capturarLog(t *testing.T) *logSeguro {
	t.Helper()
	l := &logSeguro{}
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(l, nil)))
	t.Cleanup(func() { slog.SetDefault(antigo) })
	return l
}

// Review Focus 3. O dono apaga o alvo com a sonda em voo. Nada quebra,
// nenhum erro vai ao log, e a série de latência do alvo some.
func TestVanishedTargetIsDiscardedQuietly(t *testing.T) {
	log := capturarLog(t)
	chegou, liberar := make(chan struct{}), make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-liberar
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")

	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: alvo})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	close(liberar)
	b.s.Stop(esperaMaxima)

	if strings.Contains(log.String(), "gravar check") {
		t.Errorf("alvo apagado virou erro no log:\n%s", log)
	}
	if n := testutil.CollectAndCount(b.m.duration); n != 0 {
		t.Errorf("%d séries de latência sobraram para um alvo apagado (D9)", n)
	}
}

// Review Focus 4. SIGTERM com sonda em voo: ela é abortada ao fim do prazo
// e NÃO vira um down no histórico (D12).
func TestShutdownDiscardsAbortedProbes(t *testing.T) {
	chegou := make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-r.Context().Done()
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")
	b.s.Stop(50 * time.Millisecond)

	if rows := b.checks(t, alvo); len(rows) != 0 {
		t.Errorf("a sonda abortada pelo shutdown foi gravada: %+v", rows)
	}
	if v := testutil.ToFloat64(b.m.total.WithLabelValues("down")); v != 0 {
		t.Errorf("probe_total{status=down} = %v para uma sonda abortada", v)
	}
}

// O outro lado de D12: a sonda que termina DENTRO do prazo é gravada.
func TestShutdownWaitsForProbesThatFinishInTime(t *testing.T) {
	chegou, liberar := make(chan struct{}), make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-liberar
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")

	parou := make(chan struct{})
	go func() {
		b.s.Stop(esperaMaxima)
		close(parou)
	}()
	close(liberar)
	esperar(t, parou, "o Stop terminar")

	if rows := b.checks(t, alvo); len(rows) != 1 || rows[0].Status != "up" {
		t.Errorf("checks = %+v, queria um up", rows)
	}
}

func TestProbeUsesDecryptedHeaders(t *testing.T) {
	recebido := make(chan string, 1)
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		recebido <- r.Header.Get("X-Token")
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) {
		enc, err := targets.SealHeaders(b.box, p.ID, map[string]string{"X-Token": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		p.HeadersEnc = enc
	})

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)
	if got := <-recebido; got != "abc" {
		t.Errorf("X-Token = %q", got)
	}
}

// New monta o agendador com o cliente de PRODUÇÃO. Um alvo em loopback tem
// de terminar bloqueado, e gravado como tal.
func TestNewUsesTheSSRFGuardedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	b := novaBancada(t, nil, 0)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	rows := b.checks(t, alvo)
	if len(rows) != 1 || rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
		t.Errorf("checks = %+v, queria um down/blocked", rows)
	}
}
```

Run: `go test ./internal/prober/ -run 'Tick|Next|Two|Vanished|Shutdown|Decrypted|SSRF' -v`
Expected: FAIL de compilação: `undefined: Scheduler`, `newScheduler`, `NewMetrics`.

- [ ] **Step 3: Implementar as métricas**

Crie `internal/prober/metrics.go`:

```go
package prober

import (
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics são as métricas do sondador (spec §13).
type Metrics struct {
	duration   *prometheus.HistogramVec
	total      *prometheus.CounterVec
	inflight   prometheus.Gauge
	queueDepth prometheus.Gauge
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
	}
	reg.MustRegister(m.duration, m.total, m.inflight, m.queueDepth)
	return m
}

func (m *Metrics) observe(alvo uuid.UUID, r Result) {
	m.duration.WithLabelValues(alvo.String()).Observe(r.Latency.Seconds())
	m.total.WithLabelValues(string(r.Status)).Inc()
}

// forget apaga a série de um alvo que deixou de existir (D9). Sem isso, cada
// alvo apagado deixaria uma série viva até o restart.
func (m *Metrics) forget(alvo uuid.UUID) {
	m.duration.DeleteLabelValues(alvo.String())
}
```

- [ ] **Step 4: Implementar o agendador**

Crie `internal/prober/scheduler.go`:

```go
package prober

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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

// MaxInFlight é o teto global de sondas simultâneas (spec §6.2). Sem ele,
// 500 alvos lentos seriam 500 goroutines presas em I/O de rede.
const MaxInFlight int32 = 64

const (
	tickEvery      = time.Second
	persistTimeout = 5 * time.Second
)

var errShutdown = errors.New("encerramento do sondador")

type Scheduler struct {
	pool    *pgxpool.Pool
	box     *crypto.Box
	clk     clock.Clock
	metrics *Metrics
	client  *http.Client
	sem     *semaphore.Weighted
	max     int32

	emVoo sync.WaitGroup
	// sondas é o contexto de TODAS as sondas, separado do ctx de Run. O
	// SIGTERM encerra o despacho na hora; as sondas em voo só são abortadas
	// quando o prazo de Stop acaba.
	sondas  context.Context
	abortar context.CancelCauseFunc
}

// New monta o agendador com o cliente de produção, guardado pelo safedial.
// Não há como passar outro cliente por aqui: TestNewUsesTheSSRFGuardedClient
// trava isso.
func New(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, m *Metrics) *Scheduler {
	return newScheduler(pool, box, clk, m, safedial.NewClient(0), MaxInFlight)
}

func newScheduler(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, m *Metrics, client *http.Client, max int32) *Scheduler {
	sondas, abortar := context.WithCancelCause(context.Background())
	return &Scheduler{
		pool: pool, box: box, clk: clk, metrics: m, client: client,
		sem: semaphore.NewWeighted(int64(max)), max: max,
		sondas: sondas, abortar: abortar,
	}
}

// Run despacha a cada segundo até ctx acabar, e então chama Stop(grace).
func (s *Scheduler) Run(ctx context.Context, grace time.Duration) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop(grace)
			return nil
		case <-t.C:
			if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar sondas", "erro", err)
			}
		}
	}
}

// Tick reivindica até "vagas livres" alvos vencidos e dispara uma goroutine
// por alvo. Devolve quantos disparou.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.clk.Now()

	// As vagas vêm ANTES da reivindicação. Reivindicar grava o próximo
	// next_run_at; um alvo reivindicado sem vaga ficaria um intervalo
	// inteiro sem ser sondado.
	var vagas int32
	for vagas < s.max && s.sem.TryAcquire(1) {
		vagas++
	}

	var alvos []db.ClaimDueTargetsRow
	if vagas > 0 {
		err := storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
			var err error
			alvos, err = q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: now, MaxRows: vagas})
			return err
		})
		// A conta é em int: converter len() para int32 seria o G115 do gosec.
		if sobra := int(vagas) - len(alvos); sobra > 0 {
			s.sem.Release(int64(sobra))
		}
		if err != nil {
			return 0, fmt.Errorf("reivindicar alvos: %w", err)
		}
	}

	for _, a := range alvos {
		s.emVoo.Add(1)
		s.metrics.inflight.Inc()
		go func() {
			defer s.emVoo.Done()
			defer s.sem.Release(1)
			defer s.metrics.inflight.Dec()
			s.sondar(a, now)
		}()
	}

	if err := s.medirFila(ctx, now); err != nil {
		return len(alvos), fmt.Errorf("medir fila: %w", err)
	}
	return len(alvos), nil
}

// Stop espera as sondas em voo por até grace. Depois disso, aborta as que
// sobraram e espera que terminem, o que é rápido, porque o http.Client
// respeita o cancelamento. Sonda abortada não é gravada (D12).
//
// Não chame Tick durante nem depois de Stop: Run para de despachar antes de
// chamá-lo.
func (s *Scheduler) Stop(grace time.Duration) {
	terminaram := make(chan struct{})
	go func() {
		s.emVoo.Wait()
		close(terminaram)
	}()
	select {
	case <-terminaram:
	case <-time.After(grace):
		s.abortar(errShutdown)
		<-terminaram
	}
}

func (s *Scheduler) sondar(a db.ClaimDueTargetsRow, checkedAt time.Time) {
	spec, err := s.especificar(a)
	if err != nil {
		// Headers que não abrem são defeito NOSSO (chave trocada, linha
		// adulterada), não queda do alvo. Gravar down acusaria o alvo.
		slog.Error("preparar sonda", "target_id", a.ID, "erro", err)
		return
	}

	res := Probe(s.sondas, s.client, spec)

	// Review Focus 4. O shutdown se reconhece pela CAUSA do contexto, nunca
	// pelo erro da sonda: com WithCancelCause, o erro devolvido é a própria
	// causa, e errors.Is(err, context.Canceled) é false.
	if errors.Is(context.Cause(s.sondas), errShutdown) {
		return
	}
	s.metrics.observe(a.ID, res)
	s.gravar(a.ID, checkedAt, res)
}

func (s *Scheduler) especificar(a db.ClaimDueTargetsRow) (Spec, error) {
	headers, err := targets.OpenHeaders(s.box, a.ID, a.HeadersEnc)
	if err != nil {
		return Spec{}, err
	}
	spec := Spec{
		URL: a.URL, Method: a.Method,
		Timeout:        time.Duration(a.TimeoutMS) * time.Millisecond,
		ExpectedStatus: int(a.ExpectedStatus),
		Headers:        headers,
	}
	if a.DegradedThresholdMS != nil {
		spec.DegradedThreshold = time.Duration(*a.DegradedThresholdMS) * time.Millisecond
	}
	return spec, nil
}

func (s *Scheduler) gravar(alvo uuid.UUID, em time.Time, res Result) {
	// Contexto próprio: a sonda terminou, e o resultado vale mesmo com o
	// processo encerrando.
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()

	id, err := uuid.NewV7()
	if err != nil {
		slog.Error("gerar id de check", "erro", err)
		return
	}
	p := db.InsertCheckParams{
		ID: id, TargetID: alvo, CheckedAt: em,
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

	err = storage.InProberTx(ctx, s.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) })
	switch {
	case err == nil:
	case alvoSumiu(err):
		// Review Focus 3: o dono apagou o alvo com a sonda em voo. Não é
		// erro, é o dono tendo mudado de ideia.
		s.metrics.forget(alvo)
	default:
		slog.Error("gravar check", "target_id", alvo, "erro", err)
	}
}

func (s *Scheduler) medirFila(ctx context.Context, now time.Time) error {
	return storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		n, err := q.CountDueTargets(ctx, now)
		if err != nil {
			return err
		}
		s.metrics.queueDepth.Set(float64(n))
		return nil
	})
}

// alvoSumiu reconhece o INSERT de check para alvo apagado. O 42501 é a RLS
// de checks (o EXISTS no alvo falha) e chega ANTES da FK; o 23503 é a FK,
// mantida para o caso de a política um dia mudar
// (TestCheckForVanishedTargetFailsWithRLSCode).
func alvoSumiu(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "42501" || pgErr.Code == "23503")
}

// int32Limitado converte com teto explícito, que é o que o gosec (G115)
// exige. Latência e status code cabem com folga; o teto é garantia.
func int32Limitado(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < 0 {
		return 0
	}
	return int32(v)
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `go test -race -count=3 ./internal/prober/... -v`
Expected: PASS nas três repetições. `-count=3` porque metade destes testes é sobre concorrência; um verde isolado não diz muito.

- [ ] **Step 6: Provar por sabotagem**

| Sabotagem | Teste que fica vermelho |
|---|---|
| Em `sondar`, remover o `if errors.Is(context.Cause(...))` | `TestShutdownDiscardsAbortedProbes` |
| Em `alvoSumiu`, aceitar só `23503` | `TestVanishedTargetIsDiscardedQuietly` (log de erro e série sobrando) |
| Remover `s.metrics.forget(alvo)` | `TestVanishedTargetIsDiscardedQuietly` |
| Em `Tick`, trocar `MaxRows: vagas` por `MaxRows: MaxInFlight` | `TestTickNeverClaimsMoreThanFreeSlots` (panic de semáforo ou 5 reivindicados) |
| Na query `ClaimDueTargets`, trocar `(0.9 + random() * 0.2)` por `1` e rodar `sqlc generate` | `TestNextRunIsJitteredWithinTenPercent` |
| Em `New`, trocar `safedial.NewClient(0)` por `http.DefaultClient` | `TestNewUsesTheSSRFGuardedClient` |
| Remover o `FOR UPDATE SKIP LOCKED` da query e rodar `sqlc generate` | `TestTwoSchedulersNeverProbeTheSameTarget` (alvo sondado 2 vezes). Rode com `-count=10` e registre quantas ficaram vermelhas |

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
go test -race -count=1 ./...
git add go.mod go.sum internal/prober/
git commit -m "feat(prober): agendador com lease SKIP LOCKED, semáforo global e shutdown que não grava sonda abortada"
```

---
### Task 8: Infra de métricas — registry, `/metrics` em loopback e o pool

**Files:**
- Modify: `internal/config/config.go` (`MetricsAddr`, validação e `LogValue`)
- Modify: `internal/config/config_test.go` (um teste de tabela)
- Create: `internal/observability/metrics.go`
- Create: `internal/observability/metrics_test.go`
- Create: `internal/storage/poolmetrics.go`
- Create: `internal/storage/poolmetrics_test.go`

**Interfaces:**
- Consumes: `prometheus/client_golang` (Task 7).
- Produces:
  - `config.Config.MetricsAddr string`: `""` quando desligado.
  - `observability.NewRegistry() *prometheus.Registry`, já com as métricas de runtime do Go e do processo.
  - `observability.NewMetricsServer(addr string, reg *prometheus.Registry) *http.Server`, que serve só `GET /metrics`.
  - `storage.NewPoolCollector(pool *pgxpool.Pool) prometheus.Collector`, que expõe `pgxpool_acquired_conns`, `pgxpool_idle_conns`, `pgxpool_total_conns`, `pgxpool_max_conns`, `pgxpool_acquire_total`, `pgxpool_empty_acquire_total` e `pgxpool_acquire_duration_seconds_total`.

**Por que o pool importa aqui.** O semáforo aceita 64 sondas, e o pool tem 10 conexões. Cada sonda faz um `INSERT` curto no fim; se o banco ficar lento, as sondas passam a esperar conexão. `pgxpool_empty_acquire_total` subindo é o sinal disso, antes de a latência medida começar a mentir.

- [ ] **Step 1: Teste de config (falha)**

Em `internal/config/config_test.go`, acrescente:

```go
// Spec §5.7: /metrics entrega o mapa operacional, e só pode escutar em
// loopback (D10). "off" desliga.
func TestMetricsAddrMustBeLoopback(t *testing.T) {
	cases := []struct {
		valor string
		quer  string // "" com ok=true significa desligado
		ok    bool
	}{
		{"", "127.0.0.1:9090", true}, // padrão
		{"off", "", true},
		{"127.0.0.1:9100", "127.0.0.1:9100", true},
		{"[::1]:9090", "[::1]:9090", true},
		{"localhost:9090", "localhost:9090", true},
		{":9090", "", false},
		{"0.0.0.0:9090", "", false},
		{"[::]:9090", "", false},
		{"10.0.0.5:9090", "", false},
		{"sem-porta", "", false},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			if c.valor != "" {
				env["METRICS_ADDR"] = c.valor
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "METRICS_ADDR") {
					t.Fatalf("METRICS_ADDR=%q aceito (err = %v)", c.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("METRICS_ADDR=%q recusado: %v", c.valor, err)
			}
			if cfg.MetricsAddr != c.quer {
				t.Errorf("MetricsAddr = %q, queria %q", cfg.MetricsAddr, c.quer)
			}
		})
	}
}
```

Run: `go test ./internal/config/ -run TestMetricsAddr -v` → FAIL de compilação: `cfg.MetricsAddr undefined`.

- [ ] **Step 2: Implementar em `config`**

Em `internal/config/config.go`:

1. Acrescente `"net"` aos imports.
2. Acrescente o campo `MetricsAddr string` em `Config`, depois de `HTTPAddr`.
3. Dentro de `Load`, junto das outras funções locais (`req`, `opt`, `key`, `proxies`), acrescente:

```go
	// /metrics entrega o mapa operacional (spec §5.7): quantos alvos, que
	// latências, quanta conexão. Só escuta em loopback; "off" desliga. Um
	// host vazio (":9090") escutaria em todas as interfaces, e por isso é
	// erro, não padrão (D10).
	metricsAddr := func(name string) string {
		raw := opt(name, "127.0.0.1:9090")
		if raw == "off" {
			return ""
		}
		host, _, err := net.SplitHostPort(raw)
		if err == nil && host == "localhost" {
			return raw
		}
		if err == nil {
			if ip, perr := netip.ParseAddr(host); perr == nil && ip.IsLoopback() {
				return raw
			}
		}
		errs = append(errs, fmt.Errorf("%s %q precisa escutar em loopback (127.0.0.1, [::1] ou localhost) ou ser \"off\"", name, raw))
		return ""
	}
```

4. No literal `cfg := &Config{...}`, acrescente `MetricsAddr: metricsAddr("METRICS_ADDR"),`.
5. Em `LogValue`, acrescente `slog.String("metrics_addr", c.MetricsAddr),` depois de `http_addr`.

Run: `go test ./internal/config/ -v` → PASS.

- [ ] **Step 3: Testes do registry e do pool (falham)**

Crie `internal/observability/metrics_test.go`:

```go
package observability_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/observability"
)

func TestMetricsServerExposesRuntimeMetrics(t *testing.T) {
	srv := httptest.NewServer(observability.NewMetricsServer("", observability.NewRegistry()).Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	corpo, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(corpo), "go_goroutines") {
		t.Errorf("status %d, corpo sem go_goroutines", resp.StatusCode)
	}

	outra, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	outra.Body.Close()
	if outra.StatusCode != http.StatusNotFound {
		t.Errorf("/ deu %d: o servidor de métricas só serve /metrics", outra.StatusCode)
	}
}
```

Crie `internal/storage/poolmetrics_test.go`:

```go
package storage_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

func valorDe(t *testing.T, reg *prometheus.Registry, nome string) float64 {
	t.Helper()
	familias, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range familias {
		if f.GetName() != nome {
			continue
		}
		m := f.GetMetric()[0]
		if g := m.GetGauge(); g != nil {
			return g.GetValue()
		}
		return m.GetCounter().GetValue()
	}
	t.Fatalf("métrica %s não exposta", nome)
	return 0
}

func TestPoolCollectorReflectsTheLivePool(t *testing.T) {
	pool := testdb.New(t)
	reg := prometheus.NewRegistry()
	reg.MustRegister(storage.NewPoolCollector(pool))

	antes := valorDe(t, reg, "pgxpool_acquire_total")
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := valorDe(t, reg, "pgxpool_acquired_conns"); v != 1 {
		t.Errorf("pgxpool_acquired_conns = %v com uma conexão na mão", v)
	}
	conn.Release()

	if v := valorDe(t, reg, "pgxpool_acquire_total"); v != antes+1 {
		t.Errorf("pgxpool_acquire_total foi de %v para %v, queria +1", antes, v)
	}
	if v := valorDe(t, reg, "pgxpool_max_conns"); v != 10 {
		t.Errorf("pgxpool_max_conns = %v, queria 10 (storage.NewPool)", v)
	}
	for _, nome := range []string{"pgxpool_idle_conns", "pgxpool_total_conns", "pgxpool_empty_acquire_total", "pgxpool_acquire_duration_seconds_total"} {
		valorDe(t, reg, nome)
	}
}
```

Run: `go test ./internal/observability/ ./internal/storage/ -run 'Metrics|Pool' -v` → FAIL de compilação.

- [ ] **Step 4: Implementar**

Crie `internal/observability/metrics.go`:

```go
package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry devolve o registry da aplicação, com as métricas de runtime do
// Go e do processo. É um registry próprio, e não o global, para que só
// entre o que o código registra de propósito: uma dependência que se
// registrasse sozinha no global não apareceria em /metrics sem ninguém ver.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// NewMetricsServer serve GET /metrics em addr, e nada mais. A config garante
// que addr é loopback (spec §5.7, D10); este servidor não tem autenticação
// porque não precisa, já que não é alcançável de fora.
func NewMetricsServer(addr string, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
```

Crie `internal/storage/poolmetrics.go`:

```go
package storage

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolCollector lê pool.Stat() a cada coleta, em vez de manter contadores
// próprios: o pgxpool já os mantém, e duas contagens divergiriam.
type poolCollector struct {
	pool                                  *pgxpool.Pool
	acquired, idle, total, max            *prometheus.Desc
	acquireCount, emptyAcquire, acquireDu *prometheus.Desc
}

// NewPoolCollector expõe o estado do pool (spec §13). O sinal que importa é
// pgxpool_empty_acquire_total subindo: sondas esperando conexão, com o
// semáforo de 64 contra um pool de 10.
func NewPoolCollector(pool *pgxpool.Pool) prometheus.Collector {
	d := func(nome, ajuda string) *prometheus.Desc {
		return prometheus.NewDesc("pgxpool_"+nome, ajuda, nil, nil)
	}
	return &poolCollector{
		pool:         pool,
		acquired:     d("acquired_conns", "Conexões em uso agora."),
		idle:         d("idle_conns", "Conexões ociosas agora."),
		total:        d("total_conns", "Conexões abertas agora."),
		max:          d("max_conns", "Teto de conexões do pool."),
		acquireCount: d("acquire_total", "Aquisições de conexão desde o boot."),
		emptyAcquire: d("empty_acquire_total", "Aquisições que encontraram o pool sem conexão ociosa."),
		acquireDu:    d("acquire_duration_seconds_total", "Tempo total gasto adquirindo conexões."),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.acquireCount, c.emptyAcquire, c.acquireDu} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))
	counter(c.acquireCount, float64(s.AcquireCount()))
	counter(c.emptyAcquire, float64(s.EmptyAcquireCount()))
	counter(c.acquireDu, s.AcquireDuration().Seconds())
}
```

Run: `go test -race -count=1 ./internal/config/ ./internal/observability/ ./internal/storage/ -v` → PASS.

- [ ] **Step 5: Provar por sabotagem**

1. Em `metricsAddr`, aceite qualquer host → `TestMetricsAddrMustBeLoopback` fica vermelho em `:9090`, `0.0.0.0:9090`, `[::]:9090` e `10.0.0.5:9090`.
2. Troque `mux.Handle("GET /metrics", ...)` por `mux.Handle("/", ...)` → `TestMetricsServerExposesRuntimeMetrics` fica vermelho em `/`.
3. Em `Collect`, emita `c.acquired` com valor `0` → `TestPoolCollectorReflectsTheLivePool` fica vermelho.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/ && go vet ./... && staticcheck ./... && gosec -quiet ./...
git add internal/config/ internal/observability/ internal/storage/poolmetrics*.go
git commit -m "feat(observability): /metrics só em loopback, com runtime e pgxpool"
```

---
### Task 9: Wiring, teste ponta a ponta e fechamento

**Files:**
- Modify: `cmd/vigilume/main.go` (`run` inteira; `servir` nova)
- Create: `cmd/vigilume/main_test.go`
- Rename + rewrite: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-2.md` → `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md`
- Modify: `docs/superpowers/plans/2026-09-22-roadmap.md` (tabela)

**Interfaces:**
- Consumes: tudo das Tasks 2 a 8.
- Produces: o binário com os três papéis funcionando (spec §3.1).
  - `--role=api`: API e métricas.
  - `--role=prober`: sondador, manutenção de partições e métricas.
  - `--role=all`: os dois.

**Contexto para quem implementa.** Hoje `run` só serve HTTP, e o papel `prober` fica parado em `<-ctx.Done()` ("ganha seu loop no Plano 2"). Cada subsistema passa a rodar num `errgroup`: se um cai (a porta HTTP ocupada, por exemplo), o contexto do grupo cancela e os outros encerram. Sem isso, o processo seguiria meio vivo, com o sondador rodando e a API morta, e o health check do Fly.io não perceberia.

- [ ] **Step 1: Escrever os testes de wiring (falham)**

Crie `cmd/vigilume/main_test.go`:

```go
package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

var chave = []byte("chave-mestra-de-teste-32-bytes!!")

// O teste que junta tudo, com o cliente de PRODUÇÃO: um alvo cadastrado
// apontando para 127.0.0.1 tem de ser sondado pelo processo real e gravado
// como bloqueado. Se o wiring trocasse o cliente, ou esquecesse o sondador,
// ou as partições, este teste pegaria.
//
// Usa relógio real, porque run monta clock.Real. A espera é por sondagem
// com prazo, não por sleep fixo.
func TestProberRoleProbesWithTheProductionClient(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'e2e@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	alvo := uuid.Must(uuid.NewV7())
	agora := time.Now()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora.Add(-time.Minute), CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t),
		MasterKey: chave, ShutdownGrace: 2 * time.Second,
		// MetricsAddr vazio: sem servidor de métricas neste teste.
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	var rows []db.ListChecksRow
	prazo := time.After(15 * time.Second)
	passo := time.NewTicker(100 * time.Millisecond)
	defer passo.Stop()
	for len(rows) == 0 {
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes de sondar: %v", err)
		case <-prazo:
			t.Fatal("nenhum check gravado em 15s: o sondador não está no wiring")
		case <-passo.C:
		}
		err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
			var err error
			rows, err = q.ListChecks(ctx, db.ListChecksParams{
				UserID: dono, TargetID: alvo, FromAt: agora.Add(-time.Hour), ToAt: time.Now().Add(time.Hour),
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
		t.Errorf("check = %+v, queria down/blocked: o cliente do wiring não é o do safedial", rows[0])
	}

	cancel()
	select {
	case err := <-fim:
		if err != nil {
			t.Errorf("run encerrou com erro: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run não encerrou depois do cancelamento")
	}
}

func TestRunRefusesARoleThatBypassesRLS(t *testing.T) {
	testdb.New(t) // o cleanup restaura o banco
	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.AdminDSN(t),
		MasterKey: chave, ShutdownGrace: time.Second,
	}
	// Com prazo: sem a checagem, run subiria normalmente e só voltaria no
	// cancelamento. O teste ficaria pendurado em vez de vermelho.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := run(ctx, cfg)
	if !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("run com superusuário = %v, queria ErrRoleBypassesRLS", err)
	}
}

// Um subsistema que cai derruba o processo. Com a porta da API ocupada, run
// tem de VOLTAR com erro, e não ficar vivo servindo só métricas e sondas.
func TestRunFailsWhenAServerCannotStart(t *testing.T) {
	testdb.New(t)
	ocupada, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ocupada.Close()

	cfg := &config.Config{
		Role: config.RoleAll, DatabaseURL: testdb.DSN(t), HTTPAddr: ocupada.Addr().String(),
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave,
		ShutdownGrace: time.Second,
	}
	fim := make(chan error, 1)
	go func() { fim <- run(context.Background(), cfg) }()

	select {
	case err := <-fim:
		if err == nil {
			t.Error("run voltou sem erro com a porta ocupada")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run seguiu vivo com a API morta")
	}
}
```

Run: `go test ./cmd/vigilume/ -v`
Expected:
- `TestProberRoleProbesWithTheProductionClient` **falha** ("nenhum check gravado em 15s"): o papel `prober` de hoje só espera o sinal.
- `TestRunRefusesARoleThatBypassesRLS` **falha** depois de 10s: `run` ainda não chama `CheckRole`, sobe normalmente e volta `nil` no prazo.
- `TestRunFailsWhenAServerCannotStart` **passa**: o `run` de hoje já devolve o erro da porta. Ele é trava contra regressão do `errgroup` novo, e a sabotagem 2 do Step 4 é o que prova que ele morde.

- [ ] **Step 2: Reescrever `run`**

Em `cmd/vigilume/main.go`, mantenha `main` como está e substitua `run` inteira por:

```go
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
	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations aplicadas")

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Antes de qualquer consulta de negócio. Com um papel que ignora RLS, a
	// segunda barreira de isolamento estaria desligada em silêncio (D8).
	if err := storage.CheckRole(ctx, pool); err != nil {
		return err
	}

	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	clk := clock.Real{}
	reg := observability.NewRegistry()
	reg.MustRegister(storage.NewPoolCollector(pool))

	g, gctx := errgroup.WithContext(ctx)

	if cfg.MetricsAddr != "" {
		servir(gctx, g, observability.NewMetricsServer(cfg.MetricsAddr, reg), "métricas", cfg.ShutdownGrace)
	}

	if cfg.Role.RunsProber() {
		// A partição do mês corrente tem de existir antes da primeira sonda.
		if err := storage.EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil {
			return err
		}
		sched := prober.New(pool, box, clk, prober.NewMetrics(reg))
		g.Go(func() error { return sched.Run(gctx, cfg.ShutdownGrace) })
		g.Go(func() error {
			return storage.MaintainChecksPartitions(gctx, pool, clk, manutencaoDeParticoes)
		})
		slog.Info("sondador ativo", "max_em_voo", prober.MaxInFlight)
	}

	if cfg.Role.RunsAPI() {
		store := auth.NewPGStore(pool)
		deps := httpapi.Deps{
			Store:    store,
			Sessions: auth.NewManager(store, clk, cfg.IPHashKey),
			TOTP:     auth.NewTOTP(pool, box, totpIssuer),
			Targets:  targets.NewService(pool, box, clk),
			Clock:    clk,
		}
		servir(gctx, g, httpapi.NewServer(cfg, httpapi.Routes(gctx, cfg, deps)), "api", cfg.ShutdownGrace)
	}

	return g.Wait()
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

Ajuste os imports de `main.go` para: `context`, `errors`, `flag`, `fmt`, `log/slog`, `net/http`, `os`, `os/signal`, `syscall`, `time`, `golang.org/x/sync/errgroup` e os pacotes internos `auth`, `clock`, `config`, `crypto`, `httpapi`, `observability`, `prober`, `storage`, `targets`.

- [ ] **Step 3: Rodar e ver passar**

Run: `go test -race -count=1 ./cmd/vigilume/ -v`
Expected: PASS nos três.

- [ ] **Step 4: Provar por sabotagem**

1. Remova o bloco `if cfg.Role.RunsProber()` → `TestProberRoleProbesWithTheProductionClient` fica vermelho.
2. Em `servir`, ignore o erro de `ListenAndServe` (não mande para `falhou`) → `TestRunFailsWhenAServerCannotStart` fica vermelho ("seguiu vivo").
3. Remova a chamada a `storage.CheckRole` → `TestRunRefusesARoleThatBypassesRLS` fica vermelho.
4. Remova `storage.EnsureChecksPartitions` do boot → `TestProberRoleProbesWithTheProductionClient` fica vermelho. As migrations não criam partição, então o `INSERT` do check falha com "no partition of relation" e vai para o log.

- [ ] **Step 5: Verificação manual do binário**

Suba um Postgres local com um papel comum:

```bash
docker run -d --rm --name vigilume-dev -p 55432:5432 -e POSTGRES_USER=adm -e POSTGRES_PASSWORD=adm -e POSTGRES_DB=vigilume postgres:16-alpine
sleep 5
docker exec vigilume-dev psql -U adm -d vigilume -c "CREATE ROLE app LOGIN PASSWORD 'app'; GRANT CREATE ON DATABASE vigilume TO app; GRANT CREATE ON SCHEMA public TO app;"

K=$(head -c 32 /dev/urandom | base64)
export DATABASE_URL="postgres://app:app@localhost:55432/vigilume?sslmode=disable" \
       PUBLIC_BASE_URL=http://localhost:8080 CORS_ORIGIN=http://localhost:5173 \
       MASTER_KEY=$K JWT_SECRET=$K IP_HASH_KEY=$K
make build && ./bin/vigilume --role=all &
sleep 3
curl -s localhost:8080/healthz -o /dev/null -w '%{http_code}\n'           # 200
curl -s 127.0.0.1:9090/metrics | grep -E '^(scheduler_inflight|pgxpool_max_conns) '
kill %1; wait
DATABASE_URL="postgres://adm:adm@localhost:55432/vigilume?sslmode=disable" ./bin/vigilume --role=prober
# esperado: sai com "o papel do banco ignora row-level security"
docker rm -f vigilume-dev
```

Registre as saídas no relatório.

- [ ] **Step 6: Atualizar as pendências**

```bash
git mv docs/superpowers/PENDENCIAS-PARA-O-PLANO-2.md docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md
```

Substitua o conteúdo inteiro de `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md` por:

```markdown
# Pendências que atravessam o Plano 2

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, que é **git-ignored**:
um `git clean -fdx` o apaga. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` na configuração do Fly.io.** Herdada do Plano 1, ainda aberta. Sem ela,
   o rate limit por IP e o `ip_hash` da auditoria colapsam num valor único, e linha já gravada
   com o hash do proxy não é recuperável.
2. **Papel comum do Postgres no Fly.io.** Desde o Plano 2, o boot **recusa** papel superusuário
   ou `BYPASSRLS` (D8). A `DATABASE_URL` de produção precisa apontar para um papel que seja dono
   das tabelas e não seja superusuário. Criar o papel e transferir a posse das tabelas já
   existentes (`ALTER TABLE ... OWNER TO`) **antes** do deploy do Plano 2; senão o processo não
   sobe.
3. **`METRICS_ADDR` no Fly.io.** O padrão (`127.0.0.1:9090`) funciona, mas não é raspável pela
   rede privada do Fly. Decidir no Plano 5 entre `off` e um bind na interface privada, que
   hoje a config recusa (D10).

## Resolvidas no Plano 2

| Origem | Como fechou |
|---|---|
| R15 | `TestEachRequestOpensANewConnection` conta conexões novas no servidor |
| R22 | 6to4, Teredo e NAT64 (e as formas de IPv4 que `Unmap` não desfaz) na lista de bloqueio |
| R23 | Zona checada antes do `Unmap`, que a descartava em endereço mapeado |
| Item 9b | goose `Provider` com session locker |
| §5.5 RLS | `targets` e `checks`, com `FORCE`, papel único e teste com papel não-super |
| §3.3 sqlc | Código novo gerado; CI roda `sqlc diff` |
| §13 métricas | Sondador, pool e runtime; `/metrics` só em loopback |

## Para o Plano 3 (incidentes e notificações)

- O sondador grava o check e **não** avalia incidente. O ponto de extensão é
  `(*Scheduler).gravar`, depois do `INSERT` bem-sucedido. A avaliação precisa saber o
  `user_id` do alvo; `ClaimDueTargets` não o devolve hoje.
- **Sonda abortada pelo shutdown não é gravada (D12)**, então não conta como falha para a
  histerese. Um deploy não abre incidente.
- Toda tabela nova com `user_id` nasce com RLS `FORCE` e política com `NULLIF`, como a
  migration 00004.

## Para o Plano 4

- **R2**, adiada por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.
- **D6:** `GET /targets/{id}/checks` com janela acima de 48h passa a ler `check_rollup_1h`.
- **Retenção:** `MaintainChecksPartitions` ganha o `DROP` das partições vencidas (spec §12).

## Para o Plano 5

- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA (item 12 residual).
- Fluxo dedicado de troca de autenticador.
- `HashPasswordWith` aceita parâmetros fora de faixa (porta exportada só para teste).
- O contrato JSON de `/targets` está na Task 5 do Plano 2, incluindo o `error.fields` (D14) e
  o tri-estado de `degraded_threshold_ms`.

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-alvos-e-sondador.md`: decisões D1–D16, com o custo de
  cada uma estar errada.
- Histórico de execução: `.superpowers/sdd/<data>-alvos-e-sondador/progress.md` (git-ignored).
```

Em `docs/superpowers/plans/2026-09-22-roadmap.md`, na tabela, troque a coluna **Estado** do Plano 1 para `concluído`. No Plano 2, troque o **Arquivo** para `2026-09-24-alvos-e-sondador.md` e o **Estado** para `concluído`.

- [ ] **Step 7: Verificação final e commit**

```bash
gofmt -l . && go vet ./... && staticcheck ./... && gosec -quiet ./... && govulncheck ./...
sqlc diff
go test -race -count=1 ./...
go test -race -short -count=1 ./...     # sem Docker: pacotes puros passam, integração pula
git add cmd/ docs/
git commit -m "feat: papéis api/prober/all no errgroup, com teste ponta a ponta do sondador

O papel prober ganha o loop que o Plano 1 deixou pendente. O teste de
wiring sonda um alvo em 127.0.0.1 com o cliente de produção e exige que
ele seja gravado como bloqueado."
```

---

## Verificação final do plano

- [ ] `make lint` e `make sec` sem apontamentos novos (o aviso GO-2026-5932 de `x/crypto/openpgp`, não alcançável, já existia)
- [ ] `sqlc diff` limpo
- [ ] `go test -race -count=1 ./...` verde, e `go test -short ./...` verde sem Docker
- [ ] CI verde no GitHub, inclusive o passo novo de `sqlc diff`
- [ ] **Toda sabotagem das nove tarefas registrada no relatório**, com o teste que ficou vermelho. As que ficaram verdes por desenho (Task 4, Step 9, 2a e 2b) estão explicadas
- [ ] `curl` em `/api/v1/targets` sem token devolve 401; com token de outro usuário, um id alheio devolve 404
- [ ] Nenhuma resposta de `/api/v1/targets*` contém valor de header
- [ ] `SELECT headers_enc FROM targets`, como superusuário, não contém o valor em claro
- [ ] O binário sobe como papel comum e **recusa** superusuário
- [ ] `curl 127.0.0.1:9090/metrics` mostra `probe_total`, `scheduler_inflight` e `pgxpool_max_conns`
- [ ] Um alvo apontando para `http://169.254.169.254/` é recusado no cadastro (`forbidden`). Um alvo cujo **nome** resolve para IP interno é aceito no cadastro e gravado como `blocked` na sonda

Só depois disso escrever o Plano 3.

---

## Revisão do plano contra a spec

### 1. Cobertura

| Requisito | Tarefa |
|---|---|
| §3.1 papéis `api`/`prober`/`all` | Task 9 |
| §3.3 `sqlc` | Task 3 (código novo; auth no Plano 4) |
| §4.1 `targets`, `checks` | Task 3 |
| §4.2 UUIDv7, `checks` particionada por mês, `error_kind` enum | Tasks 3, 4, 6, 7 |
| §5.1 SSRF: esquema, blocklist, `ControlContext`, redirect ≤3, corpo 64 KiB, timeout | Tasks 1, 4 (antecipada), 6 |
| §5.4 headers cifrados com AAD da linha | Task 4 |
| §5.5 `userID` obrigatório, RLS, 404 | Tasks 2, 3, 4, 5 |
| §5.6 erro normalizado antes de persistir | Task 6 (`Result` não tem campo de mensagem) |
| §5.7 rate limit por usuário, `/metrics` local | Tasks 5, 8 |
| §6.1 tick 1s, `SKIP LOCKED`, semáforo 64, jitter ±10% | Tasks 3, 7 |
| §6.2 timeout < intervalo, shutdown 15s, relógio injetado | Tasks 4, 7, 9 |
| §9 rotas de alvos e checks; §9.1 cursor, envelope, janela 48h | Tasks 4, 5 |
| §11.5 up/degraded/down, limiar por alvo, padrão 2000, nulo desativa | Tasks 4, 5, 6 |
| §13 métricas do sondador e do pool | Tasks 7, 8 |
| §14 `SKIP LOCKED` e RLS contra Postgres real; sondador com `httptest`; agendador sem sleep | Tasks 3, 6, 7 |

### 2. Lacunas conscientes, com destino

| Item da spec | Onde entra | Motivo |
|---|---|---|
| §7 avaliação de incidente no fluxo da sonda | Plano 3 | É o assunto do Plano 3; o ponto de extensão está marcado |
| §9 `GET /readyz` | Plano 5 | Junto do deploy, que define o que "pronto" significa no Fly.io |
| §12 retenção e rollups | Plano 4 | A partição já nasce mensal; o `DROP` vem com o job |
| R2 (auth em sqlc) | Plano 4 | Decisão do usuário, 2026-09-23 |
