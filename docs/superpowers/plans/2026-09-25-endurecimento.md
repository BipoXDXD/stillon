# Vigilume — Plano 8: endurecimento

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fechar as pendências de segurança e correção do grupo A: o desafio do 2FA passa a valer uma vez, o `ClientIP` sai numa forma única, o `CLIENT_IP_HEADER` recusa nomes que nunca carregam o IP do cliente, o `HashPasswordWith` recusa parâmetros que levam o Argon2 ao panic, o `prober.Spec` não vaza headers no log, a trava do `MaxChannels` ganha a prova que faltava, e os registros (runbook e pendências) refletem o estado real.

**Architecture:** O desafio do 2FA continua um JWT sem estado próprio; o que o torna de uso único é uma marca d'água por usuário (`users.totp_challenge_mark`), avançada por um `UPDATE` condicional depois do código TOTP certo. O claim novo `cat` leva o instante de emissão em microssegundos, que é a chave da marca. As outras tarefas são locais: um helper `normalizar` por onde passam todas as saídas de `ClientIP`, uma denylist no `config.Load`, uma regra de parâmetros comum à escrita e à leitura do hash, um `LogValue` no `Spec` e um teste concorrente com gancho no `ChannelService`.

**Tech Stack:** Go 1.26, PostgreSQL 18 nos testes (17 e 18 na matriz do CI), `pgx/v5`, `sqlc` v1.31.1 (só `sqlc generate` para o modelo), `goose` v3, `golang-jwt/jwt/v5`, `testcontainers-go`.

**Spec:** `docs/superpowers/specs/2026-09-25-plano-8-endurecimento-design.md` (aprovada em 2026-09-25). Onde este plano difere dela, a diferença está em "Desvios registrados em relação à spec aprovada", abaixo, e prevalece.

**Pendências absorvidas:** o grupo A do `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md` e da seção "Herdadas, ainda abertas" do `PENDENCIAS-PARA-O-PLANO-6.md`, e os três itens não bloqueantes do PR #11. A Tarefa 6 escreve o `PENDENCIAS-PARA-O-PLANO-9.md` com o resto.

**Validação prévia.** Todo o código deste plano rodou numa cópia do repositório (worktree de `main` em `86ef779`) antes de o plano ser entregue: PostgreSQL 18 real via testcontainers, `sqlc generate`, `go test -race -count=1 ./...` verde, `sqlc diff`, `gofmt`, `go vet`, `staticcheck`, `gosec` e `govulncheck` limpos. O estado da árvore depois de cada tarefa foi guardado, e **os testes de cada tarefa foram aplicados sobre o estado anterior e falharam pela razão escrita no passo 2** (o teste do `https` em loopback é a exceção declarada: o código já recusava, e ele é provado por sabotagem). Cada sabotagem listada foi feita, e o teste ficou vermelho. **A validação achou três testes que nasceriam vacuamente verdes ou fracos** (D4, D10 e a sabotagem S4 da Tarefa 2), já corrigidos aqui. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

**Como ler os diffs.** Arquivo novo aparece inteiro, depois de "Crie `caminho`:". Arquivo modificado aparece como diff unificado contra o estado da tarefa anterior, depois de "Aplique em `caminho`:". O contexto basta para aplicar à mão, ou com `git apply` a partir da raiz. `internal/storage/db/*.go` é gerado: quando a tarefa muda o schema, o passo diz `sqlc generate`, e o diff não aparece.

---

## Global Constraints

Valores copiados da spec e das decisões do usuário. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/vigilume`. **PostgreSQL 18 nos testes** (`TEST_POSTGRES_IMAGE` troca a imagem); a matriz do CI roda 17 e 18.
- **Nenhum contrato da API muda:** os mesmos códigos HTTP, os mesmos shapes JSON e os mesmos `error.code`. O painel (`web/`) **não é tocado**.
- **`401 unauthorized` fica só para token recusado** (ausente, inválido, vencido, de família morta, e agora desafio já consumido); código errado, senha errada e conta bloqueada são `401 invalid_credentials` (D24 do Plano 5). É nisso que o painel decide entre "entre de novo" e "código incorreto".
- **`users` continua sem RLS** (D7 do Plano 2). O SQL novo do `auth.PGStore` é escrito à mão, como o resto do arquivo, até o R2; o R2 **não** entra aqui.
- **Uma migration nova:** `00008_totp_challenge_mark.sql`, coluna anulável sem `DEFAULT`.
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa**; `sqlc diff` limpo na Tarefa 1.
- **Prova por sabotagem é obrigatória** para toda asserção de ausência e para todo teste que nasce verde. O implementador quebra a propriedade de propósito, vê o teste ficar vermelho **pela razão certa**, restaura, e **registra no relatório** o que quebrou e o que viu. Sabotagem que não compila, ou que um curto-circuito deixa sem efeito, não prova nada (a S4 da Tarefa 2 foi reescrita por isso). O teste vacuamente verde apareceu 14 vezes no Plano 1, 5 no Plano 2, 3 no Plano 4, uma no Plano 5, e **uma na validação deste plano** (D4).
- **Integração só com todos os jobs verdes no GitHub** (lint, secrets, test 17 e 18, web, image, e2e).

### Desvios registrados em relação à spec aprovada

| Spec | Aqui | Motivo |
|---|---|---|
| §3.2 e R4: sem normalização, o mesmo cliente ganhava "dois baldes em cada limitador" | O limitador **já** normalizava: `chaveDe` (`ratelimit.go`) faz `Unmap` na chave do balde. O defeito atinge o `ip_hash` e o `ip` da auditoria e de `GET /sessions` (`auth.Meta.IP`) | Achado na validação. O teste do §6 "mesmo balde do limitador" virou "mesma saída de `ClientIP`", que é o valor que vai para o `ip_hash` |
| §3.1: `ParseChallengeToken` "passa a devolver também o `cat`" | Devolve uma struct `auth.Challenge{UserID, Stamp, IssuedAt}` (D1) | Quatro valores de retorno soltos |
| §3.6: gancho "exposto por `export_test.go`" | O teste é do pacote `notify` (interno) e atribui `b.svc.depoisDeContar` direto | `service_test.go` já é `package notify`; um `export_test.go` só para isso não se paga |
| §3.7.1 no grupo da Tarefa 5/6 | O teste do `https` em loopback está na Tarefa 2 | É o mesmo arquivo (`config_test.go`) da denylist; tarefas paralelas não disputam arquivo |
| §5: `internal/httpapi/authhandlers.go` "se o método novo precisar entrar na interface" | Não muda: a interface é `auth.UserStore`, em `internal/auth/user.go` | — |
| §6: "Dois `verify` concorrentes com o mesmo desafio: uma sessão" | A concorrência é provada no store (`TestConsumeChallengeIsAtomicUnderConcurrency`, 20 goroutines); pelo HTTP, o teste é sequencial | Pelo HTTP, cada `verify` precisa de um timestep TOTP distinto, e a ordem dos timesteps decide qual passa: o teste seria não determinístico e provaria a trava do timestep, e não a do desafio |
| §6: "Código errado não consome o desafio" como teste novo | Coberto pelo `TestLoginWithTOTPRequiresSecondFactor` existente (código errado e depois o certo, com o mesmo desafio) | A sabotagem "consumir antes do código" deixa esse teste e mais três vermelhos |

---

## Decisões tomadas neste plano

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | **`auth.Challenge`** como retorno de `ParseChallengeToken`; `IssuedAt` em UTC, reconstruído de `cat` por `time.UnixMicro` | Quatro retornos | Só forma; um chamador em produção (`TOTPVerify`) |
| D2 | **Ordem no `TOTPVerify`:** desafio → usuário/bloqueio → `totp.Verify` → `ResetFailures` → `ConsumeChallenge` → `issue` | Consumir antes do código | Antes do código, um código digitado errado queimaria o desafio. Depois do `ResetFailures`: um desafio reusado com o código certo zera o contador antes do 401; chegar lá exige senha e segundo fator (registrado nas pendências) |
| D3 | **`normalizar`** recusa zona e faz `Unmap`; o endereço da conexão que não normaliza (não é IP, ou tem zona) **fica como veio** | Recusar também | A conexão é o último recurso; não há outro valor. Zona só aparece com um par link-local ligado direto no app |
| D4 | **Denylist com teste de lista literal.** A primeira versão do teste percorria `cabecalhosProibidos` e ficou **verde com a lista vazia** (sabotagem S1) | Iterar a própria lista | Um teste que encolhe junto com o código não prova nada |
| D5 | **`HashPasswordWith` devolve `(string, error)`**, com `ErrInvalidHash`; a faixa é `parametrosValidos`, chamada também pelo `decodeHash` | Panic; faixa duplicada | Duas cópias da faixa divergiriam (sabotagem S3 da Tarefa 3) |
| D6 | **`Spec.LogValue`**: grupo com `url`, `method`, `timeout`, `expected_status`, `degraded_threshold` e os nomes dos headers **ordenados** | Omitir `headers` | Ordenado para o log ser estável entre sondas |
| D7 | **Gancho `depoisDeContar` só no caminho travado** (`travar == true`) | Nos dois | A primeira conferência, sem trava, é a do envio de teste; o gancho nela faria o teste medir a coisa errada |
| D8 | **Runbook:** os comentários com os nomes do Fly **entram na tabela** de renomeação, em vez de perderem os nomes | Apagar os nomes dos comentários | Os comentários são as referências cruzadas da lista; apagá-los esconderia onde cada nome está |
| D9 | **Desafio sem `cat` é `401 unauthorized`** no verify (teste próprio) | 400 | O painel volta ao passo da senha em `unauthorized`, que é o remédio certo para um desafio de antes do deploy |
| D10 | **`TestRecoveryCodeClosesLoginOnlyOnce` avança o relógio falso 1 s entre os logins.** Com o relógio parado, os três desafios saem no mesmo microssegundo, e o terceiro seria recusado pela marca, e não pelo código de recuperação | Tornar a marca `<=` | `<=` deixaria o mesmo desafio passar duas vezes. Com o relógio real, cada login paga um Argon2id antes do desafio, e dois desafios no mesmo microssegundo não acontecem |

---

## Review Focus

Entradas e condições que a spec implica e que um usuário real vai produzir. Cada linha tem o teste que a fixa, na tarefa dona do código.

1. **Desafio emitido antes do deploy** (sem `cat`), chegando ao `/totp/verify` depois do deploy: `401 unauthorized`, que o painel trata voltando à senha, nunca 500. → `TestTOTPChallengeFromBeforeTheMarkIsRefusedAsExpired` (Tarefa 1).
2. **Banco de produção com usuários existentes** recebendo a `00008`: a coluna existe e começa nula, e o primeiro desafio de cada usuário passa. → `TestMigrationsApplyAndSchemaIsUsable` (coluna nova na lista) e o primeiro passo de `TestConsumeChallengeAdvancesTheMarkOnlyForward` (Tarefa 1).
3. **Login em duas abas quase ao mesmo tempo:** a aba mais antiga recebe `401` e entra de novo; a mais nova fica logada. → `TestNewerTOTPChallengeRetiresTheOlderOne` (Tarefa 1). O texto que o painel mostra nesse caso ("o tempo acabou") não é exato; fica registrado nas pendências, porque o painel está fora do plano.
4. **Cliente IPv6 com grafia não canônica** no `X-Forwarded-For` ou na conexão (`2001:DB8:0:0::1`): o mesmo `ip_hash` que `2001:db8::1`. → `TestClientIPWritesIPv6InTheCanonicalForm` (Tarefa 2).
5. **`CLIENT_IP_HEADER` com espaços em volta** (`" Host "`, colado de um arquivo): recusado como sem os espaços. → caso `" " + nome + " "` em `TestLoadRejectsHeadersThatNeverCarryTheClientIP` (Tarefa 2).

---

## Execução e paralelismo

As Tarefas 1 a 5 mexem em arquivos disjuntos e podem rodar em paralelo, cada uma num worktree a partir do mesmo `main`; a integração no branch `plano-8-endurecimento` é em sequência (1, 2, 3, 4, 5), com a suíte inteira a cada passo. A 1 e a 3 compartilham o **pacote** `internal/auth` (arquivos diferentes) e o pacote de testes do `httpapi`: por isso a integração é em sequência, e não num merge só. A Tarefa 6 roda por último, no branch integrado, porque o arquivo de pendências descreve o que as outras fizeram.

---

### Task 1: desafio do 2FA de uso único

Spec §3.1. O desafio de `POST /auth/login` passa a abrir **uma** sessão. Hoje, com o mesmo desafio e um código TOTP novo a cada período, ele abre uma sessão por período durante os cinco minutos de validade.

**Files:**
- Create: `internal/storage/migrations/00008_totp_challenge_mark.sql`
- Modify: `internal/auth/token.go` (claim `cat`, struct `Challenge`, `ParseChallengeToken`)
- Modify: `internal/auth/user.go` (`UserStore.ConsumeChallenge` e `PGStore.ConsumeChallenge`)
- Modify: `internal/httpapi/totphandlers.go` (`TOTPVerify`)
- Generate: `internal/storage/db/models.go` (`sqlc generate`)
- Test: `internal/auth/token_test.go`, `internal/auth/challenge_mark_test.go` (novo), `internal/httpapi/totp_desafio_integration_test.go` (novo), `internal/httpapi/totp_integration_test.go`, `internal/storage/migrate_test.go`

**Interfaces:**
- Consumes: nada de outras tarefas.
- Produces:
  - `type auth.Challenge struct { UserID uuid.UUID; Stamp string; IssuedAt time.Time }`
  - `func auth.ParseChallengeToken(secret []byte, raw string, now time.Time) (auth.Challenge, error)` (antes devolvia `(uuid.UUID, string, error)`)
  - `auth.UserStore` ganha `ConsumeChallenge(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (consumed bool, err error)`, implementado por `*auth.PGStore`. Quem embute `auth.UserStore` (o `usuariosQueFalham` do `internal/account`) continua compilando.
  - Coluna `users.totp_challenge_mark timestamptz NULL`.

- [ ] **Step 1: Escreva os testes**

Crie `internal/auth/challenge_mark_test.go`:

````go
package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// A marca d'água do desafio de 2FA (Plano 8): o primeiro desafio consome, o
// mesmo não consome de novo, um mais antigo nunca, e um mais novo sim.
func TestConsumeChallengeAdvancesTheMarkOnlyForward(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	passos := []struct {
		nome   string
		em     time.Time
		queria bool
	}{
		{"primeiro desafio, marca vazia", t0, true},
		{"o mesmo desafio de novo", t0, false},
		{"um microssegundo mais antigo", t0.Add(-time.Microsecond), false},
		{"um microssegundo mais novo", t0.Add(time.Microsecond), true},
	}
	for _, p := range passos {
		got, err := store.ConsumeChallenge(ctx, userID, p.em)
		if err != nil {
			t.Fatal(err)
		}
		if got != p.queria {
			t.Errorf("%s: consumido = %v, queria %v", p.nome, got, p.queria)
		}
	}
}

// Vinte /totp/verify simultâneos com o MESMO desafio: um consome, os outros
// não. É a propriedade que o UPDATE condicional garante sem trava explícita.
func TestConsumeChallengeIsAtomicUnderConcurrency(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	em := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	const n = 20
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		consumos  int
		largada   = make(chan struct{})
		primeiroE error
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-largada
			ok, err := store.ConsumeChallenge(context.Background(), userID, em)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && primeiroE == nil {
				primeiroE = err
			}
			if ok {
				consumos++
			}
		}()
	}
	close(largada)
	wg.Wait()

	if primeiroE != nil {
		t.Fatal(primeiroE)
	}
	if consumos != 1 {
		t.Errorf("%d consumos do mesmo desafio, queria 1", consumos)
	}
}
````

Aplique em `internal/auth/token_test.go`:

````diff
diff --git a/internal/auth/token_test.go b/internal/auth/token_test.go
--- a/internal/auth/token_test.go
+++ b/internal/auth/token_test.go
@@ -144,15 +144,57 @@ func TestChallengeTokenRoundTrip(t *testing.T) {
 		t.Fatal(err)
 	}
 
-	got, gotCarimbo, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(time.Minute))
+	got, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(time.Minute))
 	if err != nil {
 		t.Fatal(err)
 	}
-	if got != userID {
-		t.Errorf("userID = %s, queria %s", got, userID)
+	if got.UserID != userID {
+		t.Errorf("userID = %s, queria %s", got.UserID, userID)
+	}
+	if got.Stamp != carimbo {
+		t.Errorf("carimbo = %q, queria %q", got.Stamp, carimbo)
+	}
+	if !got.IssuedAt.Equal(now) {
+		t.Errorf("emitido em %v, queria %v", got.IssuedAt, now)
+	}
+}
+
+// O instante do desafio tem resolução de microssegundo, e não de segundo como
+// o iat: é a chave da marca d'água do /totp/verify, e dois desafios do mesmo
+// usuário no mesmo segundo precisam de marcas diferentes (Plano 8).
+func TestChallengeTokenKeepsTheIssueInstantToTheMicrosecond(t *testing.T) {
+	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
+	instantes := []time.Time{now.Add(1 * time.Microsecond), now.Add(2 * time.Microsecond)}
+	for _, em := range instantes {
+		raw, err := auth.NewChallengeToken(jwtSecret, uuid.New(), auth.PasswordStamp("x"), em)
+		if err != nil {
+			t.Fatal(err)
+		}
+		got, err := auth.ParseChallengeToken(jwtSecret, raw, em)
+		if err != nil {
+			t.Fatal(err)
+		}
+		if !got.IssuedAt.Equal(em) {
+			t.Errorf("emitido em %v, voltou %v: o instante perdeu resolução", em, got.IssuedAt)
+		}
+	}
+}
+
+// Desafio sem instante de emissão (um emitido antes do Plano 8) é recusado:
+// sem ele, o /totp/verify não tem o que gravar como marca d'água.
+func TestChallengeTokenRequiresTheIssueInstant(t *testing.T) {
+	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
+	semInstante, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
+		"sub": uuid.NewString(),
+		"aud": []string{"totp-challenge"},
+		"exp": now.Add(time.Minute).Unix(),
+		"pws": auth.PasswordStamp("x"),
+	}).SignedString(jwtSecret)
+	if err != nil {
+		t.Fatal(err)
 	}
-	if gotCarimbo != carimbo {
-		t.Errorf("carimbo = %q, queria %q", gotCarimbo, carimbo)
+	if _, err := auth.ParseChallengeToken(jwtSecret, semInstante, now); err == nil {
+		t.Error("desafio sem instante de emissão foi aceito")
 	}
 }
 
@@ -172,7 +214,7 @@ func TestChallengeTokenRequiresThePasswordStamp(t *testing.T) {
 	if err != nil {
 		t.Fatal(err)
 	}
-	if _, _, err := auth.ParseChallengeToken(jwtSecret, semCarimbo, now); err == nil {
+	if _, err := auth.ParseChallengeToken(jwtSecret, semCarimbo, now); err == nil {
 		t.Error("desafio sem carimbo foi aceito")
 	}
 }
@@ -198,10 +240,10 @@ func TestChallengeTokenExpiresQuickly(t *testing.T) {
 		t.Fatal(err)
 	}
 
-	if _, _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL-time.Minute)); err != nil {
+	if _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL-time.Minute)); err != nil {
 		t.Errorf("dentro do prazo o desafio deveria valer: %v", err)
 	}
-	if _, _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL+time.Minute)); err == nil {
+	if _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL+time.Minute)); err == nil {
 		t.Error("o desafio deveria ter expirado")
 	}
 }
@@ -232,7 +274,7 @@ func TestChallengeAndAccessTokensAreNotInterchangeable(t *testing.T) {
 	if _, err := auth.ParseAccessToken(jwtSecret, desafio, now); err == nil {
 		t.Error("o token de desafio foi aceito como access token: o segundo fator seria decorativo")
 	}
-	if _, _, err := auth.ParseChallengeToken(jwtSecret, acesso, now); err == nil {
+	if _, err := auth.ParseChallengeToken(jwtSecret, acesso, now); err == nil {
 		t.Error("um access token comum foi aceito como desafio: o segundo fator seria dispensável")
 	}
 }
````

Crie `internal/httpapi/totp_desafio_integration_test.go`:

````go
package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// verificar manda o desafio com o código TOTP "de agora" e devolve a resposta.
func verificar(t *testing.T, a *api, c *http.Client, segredo, desafio string) *http.Response {
	t.Helper()
	code, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": code})
}

// O desafio abre UMA sessão (Plano 8). Antes, com o mesmo desafio e um código
// TOTP novo a cada período, abria uma sessão por período durante os cinco
// minutos de validade.
func TestTOTPChallengeOpensOnlyOneSession(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)

	primeiro := verificar(t, a, c, segredo, desafio)
	defer primeiro.Body.Close()
	if primeiro.StatusCode != http.StatusOK {
		t.Fatalf("primeiro uso do desafio devolveu %d, queria 200", primeiro.StatusCode)
	}

	// Um período depois: o código é NOVO (o timestep anterior já foi
	// consumido e não é o que está sendo testado aqui), o desafio é o mesmo
	// e ainda está no prazo.
	a.clk.Advance(30 * time.Second)
	segundo := verificar(t, a, c, segredo, desafio)
	defer segundo.Body.Close()
	if segundo.StatusCode != http.StatusUnauthorized {
		t.Fatalf("o mesmo desafio com um código novo devolveu %d, queria 401", segundo.StatusCode)
	}
	if code := decodeEnvelope(t, segundo).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized (o desafio, e não o código, foi recusado)", code)
	}
	if ck := refreshCookie(segundo); ck != nil && ck.Value != "" {
		t.Error("o desafio reusado ainda assim emitiu cookie de refresh")
	}

	// Perna positiva, no mesmo instante e com o mesmo tipo de código: um
	// desafio NOVO passa. Sem ela, o 401 acima poderia vir do código.
	a.clk.Advance(30 * time.Second)
	novo := verificar(t, a, c, segredo, desafioAtual(t, a, c))
	defer novo.Body.Close()
	if novo.StatusCode != http.StatusOK {
		t.Errorf("um desafio novo devolveu %d, queria 200", novo.StatusCode)
	}
}

// Consumir um desafio invalida os MAIS ANTIGOS ainda abertos; o inverso, o
// mais antigo primeiro e o mais novo depois, funciona (spec do Plano 8, §3.1).
func TestNewerTOTPChallengeRetiresTheOlderOne(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)

	antigo := desafioAtual(t, a, c)
	a.clk.Advance(time.Second)
	novo := desafioAtual(t, a, c)

	r1 := verificar(t, a, c, segredo, novo)
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("o desafio mais novo devolveu %d, queria 200", r1.StatusCode)
	}
	a.clk.Advance(30 * time.Second)
	r2 := verificar(t, a, c, segredo, antigo)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("o desafio mais antigo, depois do mais novo, devolveu %d, queria 401", r2.StatusCode)
	}

	// O inverso: dois desafios novos, o mais antigo verificado primeiro.
	a.clk.Advance(30 * time.Second)
	antigo = desafioAtual(t, a, c)
	a.clk.Advance(time.Second)
	novo = desafioAtual(t, a, c)
	r3 := verificar(t, a, c, segredo, antigo)
	defer r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("o desafio mais antigo, verificado primeiro, devolveu %d, queria 200", r3.StatusCode)
	}
	a.clk.Advance(30 * time.Second)
	r4 := verificar(t, a, c, segredo, novo)
	defer r4.Body.Close()
	if r4.StatusCode != http.StatusOK {
		t.Errorf("o desafio mais novo, depois do mais antigo, devolveu %d, queria 200", r4.StatusCode)
	}
}

// Um desafio emitido antes do deploy do Plano 8 não tem o claim cat. Ele
// chega ao verify como qualquer desafio inválido: 401 unauthorized, que o
// painel trata voltando ao passo da senha, e nunca um 500.
func TestTOTPChallengeFromBeforeTheMarkIsRefusedAsExpired(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)

	var u struct{ PasswordHash string }
	if err := a.pool.QueryRow(t.Context(), `SELECT password_hash FROM users WHERE id = $1`, a.userID).
		Scan(&u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	antigo, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": a.userID.String(),
		"aud": []string{"totp-challenge"},
		"iat": a.clk.Now().Unix(),
		"exp": a.clk.Now().Add(auth.ChallengeTTL).Unix(),
		"pws": auth.PasswordStamp(u.PasswordHash),
	}).SignedString(testJWTSecret)
	if err != nil {
		t.Fatal(err)
	}

	r := verificar(t, a, c, segredo, antigo)
	defer r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("desafio sem cat devolveu %d, queria 401", r.StatusCode)
	}
	if code := decodeEnvelope(t, r).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized", code)
	}
}
````

Aplique em `internal/httpapi/totp_integration_test.go`:

````diff
diff --git a/internal/httpapi/totp_integration_test.go b/internal/httpapi/totp_integration_test.go
--- a/internal/httpapi/totp_integration_test.go
+++ b/internal/httpapi/totp_integration_test.go
@@ -316,6 +316,11 @@ func TestRecoveryCodeClosesLoginOnlyOnce(t *testing.T) {
 
 	usar := func(codigo string) int {
 		t.Helper()
+		// Um segundo entre os logins: o desafio vale uma vez e só abre sessão
+		// se for mais novo que o último consumido (Plano 8). Com o relógio
+		// parado, os três desafios sairiam no mesmo instante, e o terceiro
+		// seria recusado pela marca d'água, e não pelo código de recuperação.
+		a.clk.Advance(time.Second)
 		resp := login(t, a, c, testEmail, testPass)
 		var d struct {
 			Challenge string `json:"challenge"`
````

Aplique em `internal/storage/migrate_test.go`:

````diff
diff --git a/internal/storage/migrate_test.go b/internal/storage/migrate_test.go
--- a/internal/storage/migrate_test.go
+++ b/internal/storage/migrate_test.go
@@ -30,11 +30,11 @@ func TestMigrationsApplyAndSchemaIsUsable(t *testing.T) {
 		}
 	}
 
-	// As migrations 00002 e 00003 só entregam valor pelas colunas que
+	// As migrations 00002, 00003 e 00008 só entregam valor pelas colunas que
 	// acrescentam em users. Provar que a tabela existe não prova que elas
 	// rodaram: sem esta checagem o teste ficaria verde com o schema da
 	// 00001 sozinho.
-	for _, column := range []string{"totp_confirmed_at", "totp_secret_enc", "totp_last_timestep"} {
+	for _, column := range []string{"totp_confirmed_at", "totp_secret_enc", "totp_last_timestep", "totp_challenge_mark"} {
 		var exists bool
 		err := pool.QueryRow(ctx,
 			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
````


- [ ] **Step 2: Rode e veja falhar**

Run: `go test -count=1 -run 'Challenge|TOTP|Recovery|Migrations' ./internal/auth/ ./internal/httpapi/ ./internal/storage/`

Expected: FAIL de compilação no `internal/auth`: `store.ConsumeChallenge undefined (type *auth.PGStore has no field or method ConsumeChallenge)` e `assignment mismatch: 2 variables but auth.ParseChallengeToken returns 3 values`. No `internal/httpapi`, que compila: `TestTOTPChallengeOpensOnlyOneSession` com "o mesmo desafio com um código novo devolveu 200, queria 401", `TestNewerTOTPChallengeRetiresTheOlderOne` com "o desafio mais antigo, depois do mais novo, devolveu 200, queria 401" e `TestTOTPChallengeFromBeforeTheMarkIsRefusedAsExpired` com "desafio sem cat devolveu 200, queria 401". No `internal/storage`: "coluna users.totp_challenge_mark não existe depois das migrations".

- [ ] **Step 3: Implemente**

Aplique em `internal/auth/token.go`:

````diff
diff --git a/internal/auth/token.go b/internal/auth/token.go
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -108,6 +108,23 @@ type challengeJWT struct {
 	// depois do desafio: sem isto, um desafio obtido com a senha antiga abria
 	// sessão até cinco minutos depois de um set-password (Task 11 do Plano 5).
 	PWS string `json:"pws"`
+	// CAT é o instante de emissão em microssegundos Unix (Plano 8). É a chave
+	// da marca d'água que torna o desafio de uso único: o iat não serve,
+	// porque jwt.NewNumericDate o arredonda para segundos, e dois desafios do
+	// mesmo usuário no mesmo segundo teriam a mesma marca. Microssegundo é a
+	// resolução do timestamptz, então o valor volta do banco sem perda.
+	CAT int64 `json:"cat"`
+}
+
+// Challenge é o que um desafio válido diz: de quem é, com qual senha foi
+// obtido e quando foi emitido.
+type Challenge struct {
+	UserID uuid.UUID
+	// Stamp é o carimbo da senha verificada no login (PasswordStamp).
+	Stamp string
+	// IssuedAt é o instante de emissão, com resolução de microssegundo: é o
+	// valor que o /totp/verify grava como marca d'água ao consumir o desafio.
+	IssuedAt time.Time
 }
 
 // NewChallengeToken recusa carimbo vazio: o desafio só sai depois de uma
@@ -125,6 +142,7 @@ func NewChallengeToken(secret []byte, userID uuid.UUID, stamp string, now time.T
 			ExpiresAt: jwt.NewNumericDate(now.Add(ChallengeTTL)),
 		},
 		PWS: stamp,
+		CAT: now.UnixMicro(),
 	}
 	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
 	if err != nil {
@@ -133,9 +151,11 @@ func NewChallengeToken(secret []byte, userID uuid.UUID, stamp string, now time.T
 	return tok, nil
 }
 
-// ParseChallengeToken devolve o usuário e o carimbo da senha verificada no
-// login. Desafio sem carimbo é inválido.
-func ParseChallengeToken(secret []byte, raw string, now time.Time) (uuid.UUID, string, error) {
+// ParseChallengeToken devolve o desafio: o usuário, o carimbo da senha
+// verificada no login e o instante de emissão. Desafio sem carimbo ou sem
+// instante é inválido; um desafio emitido antes do Plano 8 não tem instante e
+// morre com o deploy (no pior caso, um login refeito dentro de ChallengeTTL).
+func ParseChallengeToken(secret []byte, raw string, now time.Time) (Challenge, error) {
 	var claims challengeJWT
 
 	_, err := jwt.ParseWithClaims(raw, &claims,
@@ -147,15 +167,18 @@ func ParseChallengeToken(secret []byte, raw string, now time.Time) (uuid.UUID, s
 		jwt.WithAudience(challengeAudience),
 	)
 	if err != nil {
-		return uuid.Nil, "", fmt.Errorf("%w: %v", ErrInvalidToken, err)
+		return Challenge{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
 	}
 
 	id, err := uuid.Parse(claims.Subject)
 	if err != nil {
-		return uuid.Nil, "", fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
+		return Challenge{}, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
 	}
 	if claims.PWS == "" {
-		return uuid.Nil, "", fmt.Errorf("%w: desafio sem carimbo de senha", ErrInvalidToken)
+		return Challenge{}, fmt.Errorf("%w: desafio sem carimbo de senha", ErrInvalidToken)
+	}
+	if claims.CAT <= 0 {
+		return Challenge{}, fmt.Errorf("%w: desafio sem instante de emissão", ErrInvalidToken)
 	}
-	return id, claims.PWS, nil
+	return Challenge{UserID: id, Stamp: claims.PWS, IssuedAt: time.UnixMicro(claims.CAT).UTC()}, nil
 }
````

Aplique em `internal/auth/user.go`:

````diff
diff --git a/internal/auth/user.go b/internal/auth/user.go
--- a/internal/auth/user.go
+++ b/internal/auth/user.go
@@ -88,6 +88,11 @@ type UserStore interface {
 	// RehashPassword troca oldHash por newHash só se oldHash ainda for o hash
 	// gravado; swapped diz se trocou.
 	RehashPassword(ctx context.Context, userID uuid.UUID, oldHash, newHash string) (swapped bool, err error)
+	// ConsumeChallenge avança a marca d'água do desafio de 2FA até issuedAt,
+	// só se o desafio for mais novo que ela; consumed diz se avançou. Um
+	// desafio que não avança a marca já foi usado, ou é mais antigo que um
+	// usado (Plano 8).
+	ConsumeChallenge(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (consumed bool, err error)
 }
 
 func (s *PGStore) ByEmail(ctx context.Context, email string) (User, error) {
@@ -166,6 +171,24 @@ func (s *PGStore) RehashPassword(ctx context.Context, userID uuid.UUID, oldHash,
 	return n == 1, nil
 }
 
+// ConsumeChallenge é um compare-and-swap numa instrução: dois /totp/verify
+// concorrentes com o mesmo desafio disputam a mesma linha, o segundo espera o
+// commit do primeiro, relê a linha (READ COMMITTED) e não satisfaz mais o
+// WHERE. Não precisa de trava explícita.
+//
+// O "<" estrito é o que faz o desafio valer uma vez: com "<=", o mesmo
+// desafio passaria de novo, porque a marca ficou igual ao instante dele.
+func (s *PGStore) ConsumeChallenge(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (bool, error) {
+	tag, err := s.pool.Exec(ctx, `
+		UPDATE users SET totp_challenge_mark = $2
+		 WHERE id = $1 AND (totp_challenge_mark IS NULL OR totp_challenge_mark < $2)`,
+		userID, issuedAt)
+	if err != nil {
+		return false, fmt.Errorf("consumir desafio de 2FA: %w", err)
+	}
+	return tag.RowsAffected() == 1, nil
+}
+
 // ByID atende os dois caminhos de TOTP que só têm o id vindo do token: o
 // cadastro, que precisa do e-mail para o nome da conta na URI otpauth://, e a
 // verificação do segundo fator, que precisa do estado de bloqueio (R40).
````

Aplique em `internal/httpapi/totphandlers.go`:

````diff
diff --git a/internal/httpapi/totphandlers.go b/internal/httpapi/totphandlers.go
--- a/internal/httpapi/totphandlers.go
+++ b/internal/httpapi/totphandlers.go
@@ -95,15 +95,13 @@ func (h *AuthHandlers) TOTPConfirm(w http.ResponseWriter, r *http.Request) {
 
 // TOTPVerify fecha o login em dois passos: valida o desafio e emite a sessão.
 //
-// O desafio NÃO é consumido — é um JWT sem jti, e revogá-lo exigiria estado
-// que este plano não tem. O comentário anterior dizia que ele era consumido,
-// e isso era falso. O que sobra depois do item 11 (consumo do timestep,
-// RFC 6238 §5.2) é estreito: com o desafio de cinco minutos em mãos, cada
-// sessão adicional exige um código NOVO do autenticador, porque o já usado
-// não entra duas vezes. O R40 limita as tentativas erradas a LockThreshold.
-// O resíduo é sessão paralela para quem já provou senha e segundo fator —
-// higiene, não escalada. Fechar de vez pede jti em denylist ou um
-// challenge_id consumido no verify; fica nomeado para o Plano 5.
+// O desafio vale uma vez (Plano 8). É um JWT sem estado próprio; o que o torna
+// de uso único é a marca d'água em users.totp_challenge_mark: o desafio só
+// abre sessão se for mais novo que a marca, e abri-la a avança até o instante
+// dele. Antes, o mesmo desafio abria uma sessão por código TOTP novo durante
+// os cinco minutos de validade. Efeito colateral aceito: consumir um desafio
+// invalida os mais antigos do mesmo usuário ainda abertos (dois logins quase
+// simultâneos: se o mais novo terminar primeiro, o mais antigo refaz a senha).
 func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
 	var req struct {
 		Challenge string `json:"challenge"`
@@ -114,11 +112,12 @@ func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
 	}
 	now := h.clk.Now()
 
-	userID, stamp, err := auth.ParseChallengeToken(h.jwtSecret, req.Challenge, now)
+	desafio, err := auth.ParseChallengeToken(h.jwtSecret, req.Challenge, now)
 	if err != nil {
 		apierr.WriteError(w, r, apierr.CodeUnauthorized)
 		return
 	}
+	userID := desafio.UserID
 
 	// O segundo fator entra no MESMO bloqueio progressivo do login (R40).
 	// Sem isto, quem já tem a senha chuta seis dígitos indefinidamente: o
@@ -158,10 +157,25 @@ func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
 		slog.Error("zerar falhas de segundo fator", "erro", err)
 	}
 
+	// O consumo vem DEPOIS do código, de propósito: consumido antes, um
+	// código digitado errado queimaria o desafio e obrigaria a digitar a
+	// senha de novo. Desafio já usado é o mesmo 401 unauthorized de um
+	// desafio vencido, e não conta no bloqueio: quem chega aqui acertou o
+	// código.
+	consumido, err := h.users.ConsumeChallenge(r.Context(), userID, desafio.IssuedAt)
+	if err != nil {
+		h.internal(w, r, "consumir desafio", err)
+		return
+	}
+	if !consumido {
+		apierr.WriteError(w, r, apierr.CodeUnauthorized)
+		return
+	}
+
 	// O carimbo é o da senha verificada no login, antes do desafio: se ela
 	// mudou desde então (set-password, troca de senha), Issue recusa, e o
 	// desafio obtido com a senha antiga morre com ela.
-	h.issue(w, r, userID, stamp)
+	h.issue(w, r, userID, desafio.Stamp)
 }
 
 type passwordRequest struct {
````

Crie `internal/storage/migrations/00008_totp_challenge_mark.sql`:

````sql
-- +goose Up
-- A marca d'água do desafio de 2FA (Plano 8): o instante de emissão do último
-- desafio consumido pelo /auth/totp/verify. Um desafio só abre sessão se for
-- mais novo que a marca, e abri-la a avança; assim o mesmo desafio não abre
-- uma segunda sessão. NULL: nenhum desafio consumido ainda.
ALTER TABLE users ADD COLUMN totp_challenge_mark timestamptz;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_challenge_mark;
````


Depois da migration, regenere o modelo (o `db.User` ganha `TotpChallengeMark *time.Time`; nenhuma query sqlc muda):

```bash
sqlc generate
sqlc diff
```

- [ ] **Step 4: Rode e veja passar**

Run: `go test -race -count=1 ./internal/auth/ ./internal/httpapi/... ./internal/account/ ./internal/storage/ ./cmd/...`

Expected: PASS. Se `TestRecoveryCodeClosesLoginOnlyOnce` falhar com "outro código de recuperação devolveu 401", o ajuste de relógio do Step 1 não foi aplicado (D10).

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

| # | Quebre | Rode | Tem que ficar vermelho |
|---|---|---|---|
| S1 | Em `TOTPVerify`, troque a chamada por `consumido, err := true, error(nil)` | `-run 'TOTP\|Recovery' ./internal/httpapi/` | `TestTOTPChallengeOpensOnlyOneSession`, `TestNewerTOTPChallengeRetiresTheOlderOne` |
| S2 | No `UPDATE` de `ConsumeChallenge`, `<` vira `<=` | `-run ConsumeChallenge ./internal/auth/` | `TestConsumeChallengeAdvancesTheMarkOnlyForward`, `TestConsumeChallengeIsAtomicUnderConcurrency` |
| S3 | Tire a condição da marca do `WHERE` (fica `WHERE id = $1`) | `-run ConsumeChallengeIsAtomic ./internal/auth/` | `TestConsumeChallengeIsAtomicUnderConcurrency` |
| S4 | Mova o bloco do consumo para antes de `h.totp.Verify` | `-run 'TOTP\|Recovery\|SecondFactor' ./internal/httpapi/` | `TestLoginWithTOTPRequiresSecondFactor` e mais três (`FailedSecondFactorLocks…`, `SuccessfulSecondFactorClears…`, `LoggingInAgainDoesNotClear…`) |
| S5 | Em `ParseChallengeToken`, `IssuedAt: claims.IssuedAt.Time.UTC()` no lugar do `cat` | `-run Challenge ./internal/auth/` | `TestChallengeTokenKeepsTheIssueInstantToTheMicrosecond` |
| S6 | `if claims.CAT <= 0 {` vira `if false {` | `-run Challenge ./internal/auth/` e `-run FromBeforeTheMark ./internal/httpapi/` | `TestChallengeTokenRequiresTheIssueInstant`, `TestTOTPChallengeFromBeforeTheMarkIsRefusedAsExpired` |

Restaure depois de cada uma e rode o Step 4 de novo.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/
git add internal/storage/migrations/00008_totp_challenge_mark.sql internal/storage/db/models.go \
  internal/auth/token.go internal/auth/user.go internal/httpapi/totphandlers.go \
  internal/auth/token_test.go internal/auth/challenge_mark_test.go \
  internal/httpapi/totp_desafio_integration_test.go internal/httpapi/totp_integration_test.go \
  internal/storage/migrate_test.go
git commit -m "feat(auth): desafio do 2FA de uso único por marca d'água em users"
```

### Task 2: `ClientIP` numa forma única e denylist do `CLIENT_IP_HEADER`

Spec §3.2, §3.3 e §3.7.1. Três mudanças em dois pacotes que andam juntas no boot e no primeiro salto da requisição.

**Files:**
- Modify: `internal/httpapi/middleware/clientip.go` (`normalizar` em todas as saídas)
- Modify: `internal/config/config.go` (`cabecalhosProibidos`, `cabecalhoProibido`)
- Test: `internal/httpapi/middleware/clientip_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: nada de outras tarefas.
- Produces: `middleware.ClientIP` continua com a mesma assinatura; a saída passa a ser sempre a forma canônica de `netip.Addr.Unmap().String()` quando o valor é IP. `config.Load` recusa mais valores de `CLIENT_IP_HEADER`, sempre com a variável citada no erro.

- [ ] **Step 1: Escreva os testes**

Aplique em `internal/config/config_test.go`:

````diff
diff --git a/internal/config/config_test.go b/internal/config/config_test.go
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -282,12 +282,49 @@ func TestLoadRejectsABadClientIPHeader(t *testing.T) {
 	}
 }
 
+// Plano 8: nomes que nunca carregam o IP do cliente são recusados no boot, em
+// qualquer caixa. Sem isto, o erro só aparecia como um balde de rate limit
+// dividido por todos os clientes.
+func TestLoadRejectsHeadersThatNeverCarryTheClientIP(t *testing.T) {
+	// Lista literal, e não cabecalhosProibidos: um teste que percorresse a
+	// própria lista encolheria junto com ela, e ficaria verde com a lista
+	// vazia.
+	nomes := []string{
+		"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port",
+		"Host", "Cookie", "Authorization", "Proxy-Authorization",
+		"Connection", "Upgrade", "TE", "Trailer", "Transfer-Encoding", "Keep-Alive",
+		"Content-Length", "Content-Type",
+	}
+	for _, nome := range nomes {
+		for _, v := range []string{nome, strings.ToLower(nome), strings.ToUpper(nome), " " + nome + " "} {
+			env := validEnv()
+			env["CLIENT_IP_HEADER"] = v
+			_, err := Load(getenvFrom(env))
+			if err == nil || !strings.Contains(err.Error(), "CLIENT_IP_HEADER") {
+				t.Errorf("CLIENT_IP_HEADER=%q: erro = %v, queria recusa citando a variável", v, err)
+			}
+		}
+	}
+	// A perna positiva: os cabeçalhos de IP de cliente dos proxies comuns
+	// passam, inclusive um que começa parecido com a família proibida.
+	for _, v := range []string{"Fly-Client-IP", "CF-Connecting-IP", "True-Client-IP", "X-Real-IP", "X-Forwarded"} {
+		env := validEnv()
+		env["CLIENT_IP_HEADER"] = v
+		if _, err := Load(getenvFrom(env)); err != nil {
+			t.Errorf("CLIENT_IP_HEADER=%q foi recusado: %v", v, err)
+		}
+	}
+}
+
 // As chaves de demonstração do compose.yaml são públicas no repositório. Com
 // elas em produção, qualquer um forja JWT e decifra os segredos.
 func TestLoadRejectsDemoKeysOverHTTPS(t *testing.T) {
 	// O esquema não diferencia maiúsculas (RFC 3986): "HTTPS://" também é
 	// produção, e não pode passar pelo portão como se fosse http.
-	for _, base := range []string{"https://vigilume.example", "HTTPS://vigilume.example"} {
+	// https em loopback também recusa (Plano 8): o portão é "http E
+	// loopback", e não "loopback"; sem estes dois casos, uma regressão para
+	// só !hostLocal passaria no CI.
+	for _, base := range []string{"https://vigilume.example", "HTTPS://vigilume.example", "https://localhost", "https://127.0.0.1"} {
 		for i, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
 			env := validEnv()
 			env["PUBLIC_BASE_URL"] = base
````

Aplique em `internal/httpapi/middleware/clientip_test.go`:

````diff
diff --git a/internal/httpapi/middleware/clientip_test.go b/internal/httpapi/middleware/clientip_test.go
--- a/internal/httpapi/middleware/clientip_test.go
+++ b/internal/httpapi/middleware/clientip_test.go
@@ -4,6 +4,7 @@ import (
 	"net/http"
 	"net/http/httptest"
 	"net/netip"
+	"strings"
 	"testing"
 
 	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
@@ -191,3 +192,66 @@ func TestRateLimitSeparatesClientsBehindFly(t *testing.T) {
 		}
 	}
 }
+
+// Plano 8: todas as saídas de ClientIP têm a mesma forma. Antes, só o
+// Fly-Client-IP passava por Unmap; o endereço da conexão e o último salto do
+// X-Forwarded-For saíam crus, e o mesmo cliente IPv4 numa pilha dupla ganhava
+// dois ip_hash na auditoria e duas origens em GET /sessions.
+func TestClientIPIsTheSameForAMappedAndAPlainClient(t *testing.T) {
+	confiaveis := middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}
+	casos := []struct {
+		nome          string
+		mapeado, puro *http.Request
+		px            middleware.Proxies
+	}{
+		{"endereço da conexão, sem proxy",
+			requisicaoDe("[::ffff:203.0.113.9]:4444", ""), requisicaoDe("203.0.113.9:4444", ""),
+			middleware.Proxies{}},
+		{"endereço da conexão, fora da lista",
+			requisicaoDe("[::ffff:203.0.113.9]:4444", "1.2.3.4"), requisicaoDe("203.0.113.9:4444", "1.2.3.4"),
+			confiaveis},
+		{"último salto do X-Forwarded-For",
+			requisicaoDe("10.1.2.3:5555", "::ffff:198.51.100.7"), requisicaoDe("10.1.2.3:5555", "198.51.100.7"),
+			confiaveis},
+		{"proxy confiável mapeado, sem X-Forwarded-For",
+			requisicaoDe("[::ffff:10.1.2.3]:5555", ""), requisicaoDe("10.1.2.3:5555", ""),
+			confiaveis},
+	}
+	for _, c := range casos {
+		m, p := middleware.ClientIP(c.mapeado, c.px), middleware.ClientIP(c.puro, c.px)
+		if m != p || strings.Contains(m, ":") {
+			t.Errorf("%s: mapeado = %q, puro = %q; queria os dois iguais e em IPv4", c.nome, m, p)
+		}
+	}
+}
+
+// Uma zona no último salto é texto do cliente, e não um endereço de onde a
+// requisição possa ter vindo: cai no endereço da conexão, como o ilegível.
+func TestClientIPRejectsAZonedForwardedHop(t *testing.T) {
+	r := requisicaoDe("10.1.2.3:5555", "fe80::1%eth0")
+	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}); got != "10.1.2.3" {
+		t.Errorf("ClientIP = %q, queria o fallback 10.1.2.3", got)
+	}
+}
+
+// O que não é IP no RemoteAddr (listener de unix socket) continua como veio.
+func TestClientIPKeepsANonIPConnectionAddress(t *testing.T) {
+	r := requisicaoDe("@", "")
+	if got := middleware.ClientIP(r, middleware.Proxies{}); got != "@" {
+		t.Errorf("ClientIP = %q, queria @", got)
+	}
+}
+
+// Um IPv6 escrito de outro jeito (maiúsculas, zeros explícitos) é o mesmo
+// cliente: sai na forma canônica, e ganha um ip_hash só.
+func TestClientIPWritesIPv6InTheCanonicalForm(t *testing.T) {
+	confiaveis := middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}
+	for _, grafia := range []string{"2001:DB8:0:0::1", "2001:0db8::0001", "2001:db8::1"} {
+		if got := middleware.ClientIP(requisicaoDe("10.1.2.3:5555", grafia), confiaveis); got != "2001:db8::1" {
+			t.Errorf("último salto %q: ClientIP = %q, queria 2001:db8::1", grafia, got)
+		}
+	}
+	if got := middleware.ClientIP(requisicaoDe("[2001:DB8::1]:4444", ""), middleware.Proxies{}); got != "2001:db8::1" {
+		t.Errorf("endereço da conexão: ClientIP = %q, queria 2001:db8::1", got)
+	}
+}
````


- [ ] **Step 2: Rode e veja falhar**

Run: `go test -count=1 -run 'ClientIP|Header|DemoKeys' ./internal/httpapi/middleware/ ./internal/config/`

Expected: FAIL em `TestClientIPIsTheSameForAMappedAndAPlainClient`, `TestClientIPRejectsAZonedForwardedHop`, `TestClientIPWritesIPv6InTheCanonicalForm` e `TestLoadRejectsHeadersThatNeverCarryTheClientIP`. **`TestLoadRejectsDemoKeysOverHTTPS` passa**: o código já recusava `https` em loopback, e faltava só o teste; ele é provado pela sabotagem S6.

- [ ] **Step 3: Implemente**

Aplique em `internal/config/config.go`:

````diff
diff --git a/internal/config/config.go b/internal/config/config.go
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -209,6 +209,10 @@ func Load(getenv func(string) string) (*Config, error) {
 			errs = append(errs, fmt.Errorf("%s: deixe vazio para usar o X-Forwarded-For; a variável é para um cabeçalho de um valor só, como Fly-Client-IP", name))
 			return ""
 		}
+		if cabecalhoProibido(raw) {
+			errs = append(errs, fmt.Errorf("%s %q não carrega o IP do cliente: a variável é para o cabeçalho de um valor só que o proxy escreve, como Fly-Client-IP", name, raw))
+			return ""
+		}
 		return http.CanonicalHeaderKey(raw)
 	}
 
@@ -328,6 +332,37 @@ func chaveDeDemo(k []byte) bool {
 	return false
 }
 
+// cabecalhosProibidos são nomes que nunca carregam o IP do cliente, mas que
+// alguém copiaria por engano para o CLIENT_IP_HEADER (Plano 8). Com um deles,
+// o app leria um valor que não é IP, cairia no endereço do proxy para todo
+// cliente e só avisaria uma vez por minuto no log: o erro de configuração
+// apareceria como rate limit coletivo, e não no boot.
+//
+// É uma lista de proibidos, e não de permitidos, de propósito: uma lista de
+// permitidos recusaria o cabeçalho de um proxy legítimo que não estivesse
+// nela, e trocar de provedor exigiria mudar código.
+var cabecalhosProibidos = []string{
+	"Forwarded",
+	"Host", "Cookie", "Authorization", "Proxy-Authorization",
+	"Connection", "Upgrade", "TE", "Trailer", "Transfer-Encoding", "Keep-Alive",
+	"Content-Length", "Content-Type",
+}
+
+// cabecalhoProibido também recusa toda a família X-Forwarded-* (Host, Proto,
+// Port...): nenhum deles é o endereço do cliente. O X-Forwarded-For tem
+// mensagem própria antes desta checagem.
+func cabecalhoProibido(nome string) bool {
+	if len(nome) >= len("X-Forwarded-") && strings.EqualFold(nome[:len("X-Forwarded-")], "X-Forwarded-") {
+		return true
+	}
+	for _, p := range cabecalhosProibidos {
+		if strings.EqualFold(nome, p) {
+			return true
+		}
+	}
+	return false
+}
+
 // nomeDeCabecalho aceita só os caracteres de "token" da RFC 9110 que fazem
 // sentido num nome de cabeçalho: letras, dígitos e hífen.
 func nomeDeCabecalho(s string) bool {
````

Aplique em `internal/httpapi/middleware/clientip.go`:

````diff
diff --git a/internal/httpapi/middleware/clientip.go b/internal/httpapi/middleware/clientip.go
--- a/internal/httpapi/middleware/clientip.go
+++ b/internal/httpapi/middleware/clientip.go
@@ -38,6 +38,14 @@ func ClientIP(r *http.Request, p Proxies) string {
 	if err != nil {
 		direto = r.RemoteAddr
 	}
+	// O endereço da conexão sai na mesma forma que os dos cabeçalhos (Plano
+	// 8): sem isto, o mesmo cliente IPv4 numa pilha dupla chegava como
+	// "::ffff:1.2.3.4" e ganhava um ip_hash diferente do "1.2.3.4". O que não
+	// é IP (listener de unix socket, pipe de teste) continua como veio: não há
+	// outro valor para usar.
+	if n, ok := normalizar(direto); ok {
+		direto = n
+	}
 	if p.Header != "" {
 		return peloCabecalho(r, p, direto)
 	}
@@ -59,8 +67,8 @@ func ClientIP(r *http.Request, p Proxies) string {
 		return direto
 	}
 	partes := strings.Split(cadeia, ",")
-	ultimo := strings.TrimSpace(partes[len(partes)-1])
-	if _, err := netip.ParseAddr(ultimo); err != nil {
+	ultimo, ok := normalizar(strings.TrimSpace(partes[len(partes)-1]))
+	if !ok {
 		// Cabeçalho ilegível não pode virar chave de balde nem hash de
 		// origem: cai no endereço que a conexão realmente tem.
 		return direto
@@ -86,14 +94,25 @@ func peloCabecalho(r *http.Request, p Proxies, direto string) string {
 		avisoDeProxy.avisar(msgForaDaLista, direto, p.Header)
 		return direto
 	}
-	ip, err := netip.ParseAddr(valor)
-	if err != nil || ip.Zone() != "" {
+	ip, ok := normalizar(valor)
+	if !ok {
 		// Do proxy, mas ilegível: o valor não vai para o log (pode ser
 		// qualquer coisa que o cliente pôs num cabeçalho de mesmo nome).
 		avisoSemCabecalho.avisar(msgSemCabecalho, direto, p.Header)
 		return direto
 	}
-	return ip.Unmap().String()
+	return ip
+}
+
+// normalizar é a forma única de um IP que sai de ClientIP: IPv4 mapeado em
+// IPv6 vira o IPv4, e endereço com zona é recusado (uma zona num cabeçalho é
+// texto do cliente, não um endereço de onde a requisição possa ter vindo).
+func normalizar(s string) (string, bool) {
+	ip, err := netip.ParseAddr(s)
+	if err != nil || ip.Zone() != "" {
+		return "", false
+	}
+	return ip.Unmap().String(), true
 }
 
 const (
````


- [ ] **Step 4: Rode e veja passar**

Run: `go test -race -count=1 ./internal/httpapi/... ./internal/config/`

Expected: PASS.

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

| # | Quebre | Rode | Tem que ficar vermelho |
|---|---|---|---|
| S1 | Apague o `if n, ok := normalizar(direto)` do começo de `ClientIP` | `-run ClientIP ./internal/httpapi/middleware/` | `TestClientIPIsTheSameForAMappedAndAPlainClient`, `TestClientIPWritesIPv6InTheCanonicalForm` |
| S2 | Em `normalizar`, `ip.String()` no lugar de `ip.Unmap().String()` | idem | `TestClientIPUnmapsFlyClientIP`, `TestClientIPIsTheSameForAMappedAndAPlainClient` |
| S3 | Em `normalizar`, tire `\|\| ip.Zone() != ""` | idem | `TestClientIPFallsBackWhenFlyClientIPIsUnusable`, `TestClientIPRejectsAZonedForwardedHop` |
| S4 | No caminho do `X-Forwarded-For`, volte a só validar com `netip.ParseAddr` e devolver a string crua | idem | `TestClientIPIsTheSameForAMappedAndAPlainClient`, `TestClientIPRejectsAZonedForwardedHop`, `TestClientIPWritesIPv6InTheCanonicalForm` |
| S5 | Esvazie `cabecalhosProibidos`; depois, só tire `"Trailer"` | `-run Header ./internal/config/` | `TestLoadRejectsHeadersThatNeverCarryTheClientIP`, nas duas |
| S6 | Troque a checagem da família inteira por `if strings.HasPrefix(strings.ToLower(nome), "x-forwarded") {` | idem | `TestLoadRejectsHeadersThatNeverCarryTheClientIP` (a perna positiva `X-Forwarded`) |
| S7 | Em `cabecalhoProibido`, `nome == p` no lugar de `strings.EqualFold(nome, p)` | idem | `TestLoadRejectsHeadersThatNeverCarryTheClientIP` |
| S8 | No portão das chaves de demo, `(!hostLocal(cfg.PublicBaseURL))` no lugar de `(EhHTTPS(cfg.PublicBaseURL) \|\| !hostLocal(cfg.PublicBaseURL))` | `-run DemoKeys ./internal/config/` | `TestLoadRejectsDemoKeysOverHTTPS` (só nos casos `https://localhost` e `https://127.0.0.1`) |

A S6 tem que trocar a **condição inteira**: trocar só o lado direito do `&&` não tem efeito, porque a checagem de comprimento curto-circuita antes para `X-Forwarded` (11 caracteres). Foi o que aconteceu na validação.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/
git add internal/httpapi/middleware/clientip.go internal/httpapi/middleware/clientip_test.go \
  internal/config/config.go internal/config/config_test.go
git commit -m "fix(clientip): forma única em todas as saídas; CLIENT_IP_HEADER recusa nomes que não carregam IP"
```

### Task 3: `HashPasswordWith` com limites

Spec §3.4. A função que os testes usam para escrever hashes com parâmetros antigos passa a recusar o que levaria o Argon2 ao panic.

**Files:**
- Modify: `internal/auth/password.go` (`HashPasswordWith`, `parametrosValidos`, `decodeHash`, `HashPassword`)
- Test: `internal/auth/password_test.go`, `internal/httpapi/login_race_integration_test.go`

**Interfaces:**
- Consumes: nada de outras tarefas.
- Produces: `func auth.HashPasswordWith(plain string, salt []byte, memory, time uint32, threads uint8) (string, error)` (antes devolvia só `string`); erro é `auth.ErrInvalidHash`.

- [ ] **Step 1: Escreva os testes**

Aplique em `internal/auth/password_test.go`:

````diff
diff --git a/internal/auth/password_test.go b/internal/auth/password_test.go
--- a/internal/auth/password_test.go
+++ b/internal/auth/password_test.go
@@ -115,11 +115,49 @@ func TestVerifyDummyDoesNotPanic(t *testing.T) {
 
 func mustHashWith(t *testing.T, plain string, m, tt uint32, p uint8) string {
 	t.Helper()
-	h := auth.HashPasswordWith(plain, []byte("saltsaltsaltsa"), m, tt, p)
+	h, err := auth.HashPasswordWith(plain, []byte("saltsaltsaltsa"), m, tt, p)
+	if err != nil {
+		t.Fatal(err)
+	}
 	parts := strings.Split(h, "$")
 	return parts[len(parts)-1]
 }
 
+// HashPasswordWith recusa, com erro e sem panic, o que o decodeHash recusaria
+// ao ler (Plano 8). A perna positiva é o menor valor aceito em cada eixo.
+func TestHashPasswordWithRejectsOutOfRangeParams(t *testing.T) {
+	sal := []byte("saltsaltsaltsalt")
+	casos := []struct {
+		nome   string
+		salt   []byte
+		m, tt  uint32
+		p      uint8
+		aceita bool
+	}{
+		{"t=0", sal, 64, 0, 1, false},
+		{"p=0", sal, 64, 1, 0, false},
+		{"memória abaixo de 8p", sal, 15, 1, 2, false},
+		{"salt vazio", nil, 64, 1, 1, false},
+		{"o mínimo de cada eixo", sal, 16, 1, 2, true},
+	}
+	for _, c := range casos {
+		h, err := auth.HashPasswordWith("senha", c.salt, c.m, c.tt, c.p)
+		if c.aceita {
+			if err != nil {
+				t.Errorf("%s: erro = %v, queria hash", c.nome, err)
+				continue
+			}
+			if ok, _, err := auth.VerifyPassword(h, "senha"); !ok || err != nil {
+				t.Errorf("%s: o hash escrito não é lido de volta (ok=%v, err=%v)", c.nome, ok, err)
+			}
+			continue
+		}
+		if !errors.Is(err, auth.ErrInvalidHash) || h != "" {
+			t.Errorf("%s: hash = %q, erro = %v, queria ErrInvalidHash", c.nome, h, err)
+		}
+	}
+}
+
 // TestVerifyPasswordRejectsOutOfRangeParams cobre hashes cujos parâmetros são
 // sintaticamente válidos mas fora da faixa que o argon2 aceita. A biblioteca
 // entra em panic nesses casos; decodeHash tem de recusar antes.
````

Aplique em `internal/httpapi/login_race_integration_test.go`:

````diff
diff --git a/internal/httpapi/login_race_integration_test.go b/internal/httpapi/login_race_integration_test.go
--- a/internal/httpapi/login_race_integration_test.go
+++ b/internal/httpapi/login_race_integration_test.go
@@ -299,7 +299,10 @@ func TestLoginRacingAPasswordResetThatCommitsSecond(t *testing.T) {
 // e a senha antiga voltava a valer.
 func TestLoginRehashDoesNotUndoAPasswordReset(t *testing.T) {
 	a := newAPI(t)
-	fraco := auth.HashPasswordWith(testPass, []byte("sal-de-16-bytes!"), 8*1024, 1, 1)
+	fraco, err := auth.HashPasswordWith(testPass, []byte("sal-de-16-bytes!"), 8*1024, 1, 1)
+	if err != nil {
+		t.Fatal(err)
+	}
 	if _, err := a.pool.Exec(context.Background(),
 		`UPDATE users SET password_hash = $2 WHERE id = $1`, a.userID, fraco); err != nil {
 		t.Fatal(err)
````


- [ ] **Step 2: Rode e veja falhar**

Run: `go test -count=1 -run 'HashPasswordWith|OutOfRange|LoginRehash' ./internal/auth/ ./internal/httpapi/`

Expected: FAIL de compilação: `assignment mismatch: 2 variables but auth.HashPasswordWith returns 1 value` (em `password_test.go` e em `login_race_integration_test.go`).

- [ ] **Step 3: Implemente**

Aplique em `internal/auth/password.go`:

````diff
diff --git a/internal/auth/password.go b/internal/auth/password.go
--- a/internal/auth/password.go
+++ b/internal/auth/password.go
@@ -65,19 +65,33 @@ func HashPassword(plain string) (string, error) {
 	if _, err := rand.Read(salt); err != nil {
 		return "", fmt.Errorf("gerar salt: %w", err)
 	}
-	return HashPasswordWith(plain, salt, argonMemory, argonTime, argonThreads), nil
+	return HashPasswordWith(plain, salt, argonMemory, argonTime, argonThreads)
 }
 
 // HashPasswordWith é exportada para que os testes consigam construir hashes
 // com parâmetros antigos e exercitar o caminho de rehash. O código de
 // produção usa sempre HashPassword.
-func HashPasswordWith(plain string, salt []byte, memory, time uint32, threads uint8) string {
+//
+// Recusa os mesmos parâmetros que o decodeHash recusa ao ler (Plano 8), e
+// salt vazio: argon2.IDKey entra em panic com t=0, p=0 ou memória abaixo de
+// 8*p, e um hash assim nem seria lido de volta.
+func HashPasswordWith(plain string, salt []byte, memory, time uint32, threads uint8) (string, error) {
+	if !parametrosValidos(memory, time, threads) || len(salt) == 0 {
+		return "", ErrInvalidHash
+	}
 	sum := argon2.IDKey([]byte(plain), salt, time, memory, threads, argonKeyLen)
 	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
 		argon2.Version, memory, time, threads,
 		base64.RawStdEncoding.EncodeToString(salt),
 		base64.RawStdEncoding.EncodeToString(sum),
-	)
+	), nil
+}
+
+// parametrosValidos é a faixa que argon2.IDKey aceita sem panic. É uma regra
+// só para a escrita (HashPasswordWith) e a leitura (decodeHash), para que as
+// duas não divirjam.
+func parametrosValidos(memory, time uint32, threads uint8) bool {
+	return time >= 1 && threads >= 1 && memory >= 8*uint32(threads)
 }
 
 // verificacoes conta os Argon2id de verificação (VerifyPassword, e por ela
@@ -139,7 +153,7 @@ func decodeHash(encoded string) (memory, time uint32, threads uint8, salt, sum [
 	// quiser — o panic seria um downgrade do ataque, e o middleware.Recover
 	// já o converteria em 500. Recusar o hash é simplesmente a resposta
 	// certa para um valor que não é um hash válido.
-	if time < 1 || threads < 1 || memory < 8*uint32(threads) {
+	if !parametrosValidos(memory, time, threads) {
 		return 0, 0, 0, nil, nil, ErrInvalidHash
 	}
 	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) == 0 {
````


- [ ] **Step 4: Rode e veja passar**

Run: `go test -race -count=1 ./internal/auth/ ./internal/httpapi/`

Expected: PASS.

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

| # | Quebre | Rode | Tem que ficar vermelho |
|---|---|---|---|
| S1 | A guarda de `HashPasswordWith` vira `if false {` | `-run HashPasswordWith ./internal/auth/` | `TestHashPasswordWithRejectsOutOfRangeParams` (com panic do Argon2) |
| S2 | Tire `\|\| len(salt) == 0` | idem | `TestHashPasswordWithRejectsOutOfRangeParams` ("salt vazio") |
| S3 | Em `parametrosValidos`, `16*uint32(threads)` no lugar de `8*…` | `-run 'HashPasswordWith\|OutOfRange' ./internal/auth/` | `TestHashPasswordWithRejectsOutOfRangeParams` ("o mínimo de cada eixo") |

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/
git add internal/auth/password.go internal/auth/password_test.go internal/httpapi/login_race_integration_test.go
git commit -m "fix(auth): HashPasswordWith recusa parâmetros que levam o Argon2 ao panic"
```

### Task 4: `prober.Spec` com `LogValue`

Spec §3.5. Os headers de um alvo chegam ao `Spec` decifrados; o log passa a mostrar só os nomes.

**Files:**
- Modify: `internal/prober/probe.go`
- Test: `internal/prober/spec_log_test.go` (novo)

**Interfaces:**
- Consumes: nada de outras tarefas.
- Produces: `func (s prober.Spec) LogValue() slog.Value`.

- [ ] **Step 1: Escreva o teste**

Crie `internal/prober/spec_log_test.go`:

````go
package prober

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Um Spec no log mostra os nomes dos headers e nunca os valores (Plano 8),
// nos dois formatos que o projeto usa (JSON em produção, texto em
// desenvolvimento).
func TestSpecLogValueHidesHeaderValues(t *testing.T) {
	const canario = "Bearer canario-que-nao-pode-vazar"
	spec := Spec{
		URL: "https://alvo.example/saude", Method: "GET", Timeout: 5 * time.Second, ExpectedStatus: 200,
		Headers: map[string]string{"Authorization": canario, "X-Api-Key": "outro-" + canario},
	}
	for nome, novo := range map[string]func(*bytes.Buffer) slog.Handler{
		"json":  func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		"texto": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(novo(&buf)).Info("sonda", "spec", spec)
		saida := buf.String()
		if strings.Contains(saida, "canario") {
			t.Errorf("%s: o valor de um header foi para o log: %s", nome, saida)
		}
		// A perna positiva: o log continua útil, com a URL e os nomes.
		for _, quer := range []string{"https://alvo.example/saude", "Authorization", "X-Api-Key"} {
			if !strings.Contains(saida, quer) {
				t.Errorf("%s: o log não traz %q: %s", nome, quer, saida)
			}
		}
	}
}
````


- [ ] **Step 2: Rode e veja falhar**

Run: `go test -count=1 -run SpecLog ./internal/prober/`

Expected: FAIL em `TestSpecLogValueHidesHeaderValues`: "json: o valor de um header foi para o log" (e o mesmo para "texto").

- [ ] **Step 3: Implemente**

Aplique em `internal/prober/probe.go`:

````diff
diff --git a/internal/prober/probe.go b/internal/prober/probe.go
--- a/internal/prober/probe.go
+++ b/internal/prober/probe.go
@@ -4,7 +4,9 @@ package prober
 import (
 	"context"
 	"io"
+	"log/slog"
 	"net/http"
+	"slices"
 	"time"
 
 	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
@@ -30,6 +32,28 @@ type Spec struct {
 	Headers           map[string]string
 }
 
+// LogValue mostra os NOMES dos headers e nunca os valores (Plano 8). Os
+// headers chegam aqui decifrados e costumam ser credencial de terceiro
+// (Authorization, X-Api-Key); sem isto, um slog.Any("spec", spec) gravaria
+// essa credencial no log. A proteção é do tipo, não da disciplina, como no
+// crypto.Secret e no config.Config. A URL sai como está: o dono já a vê na
+// API, e credenciais na URL são recusadas no cadastro.
+func (s Spec) LogValue() slog.Value {
+	nomes := make([]string, 0, len(s.Headers))
+	for nome := range s.Headers {
+		nomes = append(nomes, nome)
+	}
+	slices.Sort(nomes)
+	return slog.GroupValue(
+		slog.String("url", s.URL),
+		slog.String("method", s.Method),
+		slog.Duration("timeout", s.Timeout),
+		slog.Int("expected_status", s.ExpectedStatus),
+		slog.Duration("degraded_threshold", s.DegradedThreshold),
+		slog.Any("headers", nomes),
+	)
+}
+
 // Result é o que sai de uma sonda. Não tem campo para mensagem de erro, e é
 // isso que impede que uma mensagem crua chegue ao banco (spec §4.2).
 type Result struct {
````


- [ ] **Step 4: Rode e veja passar**

Run: `go test -race -count=1 ./internal/prober/...`

Expected: PASS.

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

| # | Quebre | Rode | Tem que ficar vermelho |
|---|---|---|---|
| S1 | `slog.Any("headers", s.Headers)` no lugar de `nomes` | `-run SpecLog ./internal/prober/` | `TestSpecLogValueHidesHeaderValues` |
| S2 | Renomeie o método para `LogValueX` (o `slog` deixa de vê-lo) | idem | `TestSpecLogValueHidesHeaderValues` |

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/
git add internal/prober/probe.go internal/prober/spec_log_test.go
git commit -m "fix(prober): Spec no log mostra os nomes dos headers, nunca os valores"
```

### Task 5: prova da trava do `MaxChannels`

Spec §3.6. **Não muda o comportamento:** a trava existe desde o Plano 3 (`LockUser`, `FOR UPDATE` na linha de `users`, que não tem RLS). O R9 da spec do Plano 5 dizia o contrário, sem reprodução. Esta tarefa acrescenta o teste concorrente que faltava; o gancho é a única linha de produção.

**Files:**
- Modify: `internal/notify/service.go` (campo `depoisDeContar` e a chamada em `cabeNa`)
- Test: `internal/notify/service_test.go`

**Interfaces:**
- Consumes: nada de outras tarefas.
- Produces: campo não exportado `ChannelService.depoisDeContar func()`, só para teste.

- [ ] **Step 1: Escreva o teste**

Aplique em `internal/notify/service_test.go`:

````diff
diff --git a/internal/notify/service_test.go b/internal/notify/service_test.go
--- a/internal/notify/service_test.go
+++ b/internal/notify/service_test.go
@@ -318,6 +318,62 @@ func TestChannelLimitIsCheckedBeforeSending(t *testing.T) {
 	}
 }
 
+// A segunda conferência do limite, a da gravação, é serializada pela trava
+// na linha do usuário (LockUser). O R9 da spec do Plano 5 dizia que não; o
+// código tinha a trava desde o Plano 3, e faltava esta prova (Plano 8). Com
+// nove canais, duas criações simultâneas se encontram depois da contagem: sem
+// a trava, as duas contam nove e gravam, e o usuário fica com onze.
+func TestConcurrentCreatesDoNotPassTheChannelLimit(t *testing.T) {
+	b := novaBancadaCanais(t, true)
+	ctx := context.Background()
+	for range MaxChannels - 1 {
+		if _, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido}); err != nil {
+			t.Fatal(err)
+		}
+	}
+
+	// As duas criações esperam uma pela outra depois de contar, por no
+	// máximo 500 ms. Com a trava, a segunda nem chega a contar antes do
+	// commit da primeira, e a espera só encurta o teste.
+	var chegaram sync.WaitGroup
+	chegaram.Add(2)
+	todas := make(chan struct{})
+	go func() { chegaram.Wait(); close(todas) }()
+	b.svc.depoisDeContar = func() {
+		chegaram.Done()
+		select {
+		case <-todas:
+		case <-time.After(500 * time.Millisecond):
+		}
+	}
+
+	erros := make([]error, 2)
+	var wg sync.WaitGroup
+	for i := range erros {
+		wg.Add(1)
+		go func() {
+			defer wg.Done()
+			_, erros[i] = b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
+		}()
+	}
+	wg.Wait()
+
+	if n := len(b.listar(t, b.dono)); n != MaxChannels {
+		t.Errorf("%d canais depois de duas criações simultâneas (erros: %v), queria %d", n, erros, MaxChannels)
+	}
+	recusadas := 0
+	for _, err := range erros {
+		if errors.Is(err, ErrLimitReached) {
+			recusadas++
+		} else if err != nil {
+			t.Errorf("erro inesperado: %v", err)
+		}
+	}
+	if recusadas != 1 {
+		t.Errorf("%d criações recusadas pelo limite, queria 1", recusadas)
+	}
+}
+
 func TestAnotherUsersChannelIsInvisible(t *testing.T) {
 	b := novaBancadaCanais(t, true)
 	ctx := context.Background()
````


- [ ] **Step 2: Rode e veja falhar**

Run: `go test -count=1 -run ConcurrentCreates ./internal/notify/`

Expected: FAIL de compilação: `b.svc.depoisDeContar undefined (type *ChannelService has no field or method depoisDeContar)`.

- [ ] **Step 3: Implemente o gancho**

Aplique em `internal/notify/service.go`:

````diff
diff --git a/internal/notify/service.go b/internal/notify/service.go
--- a/internal/notify/service.go
+++ b/internal/notify/service.go
@@ -108,6 +108,10 @@ type ChannelService struct {
 	// e-mail responde erro de validação no campo kind.
 	email   VerificationMailer
 	baseURL string
+	// depoisDeContar é um gancho de teste, chamado entre a contagem travada e
+	// o INSERT: é onde duas criações simultâneas se encontrariam se a trava
+	// não existisse (Plano 8, o mesmo desenho do targets.Service).
+	depoisDeContar func()
 }
 
 // NewChannelService recebe publicBaseURL para montar o link de verificação.
@@ -230,6 +234,9 @@ func (s *ChannelService) cabeNa(ctx context.Context, q *db.Queries, userID uuid.
 	if err != nil {
 		return fmt.Errorf("contar canais: %w", err)
 	}
+	if travar && s.depoisDeContar != nil {
+		s.depoisDeContar()
+	}
 	if n >= MaxChannels {
 		return ErrLimitReached
 	}
````


- [ ] **Step 4: Rode e veja passar, e repita**

Run: `go test -race -count=10 -run ConcurrentCreates ./internal/notify/`

Expected: PASS nas dez (na validação, as dez levaram ~9 s juntas).

- [ ] **Step 5: Sabotagem (registre no relatório)**

| # | Quebre | Rode | Tem que ficar vermelho |
|---|---|---|---|
| S1 | Em `cabeNa`, `if travar {` (o da `LockUser`) vira `if false {` | `-run ConcurrentCreates ./internal/notify/` | `TestConcurrentCreatesDoNotPassTheChannelLimit` ("11 canais depois de duas criações simultâneas") |

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/
git add internal/notify/service.go internal/notify/service_test.go
git commit -m "test(notify): prova que duas criações simultâneas não passam do MaxChannels"
```

### Task 6: runbook, pendências e gates finais

Spec §3.7.2, §3.7.3 e §1 (critério 4). Roda depois de as Tarefas 1 a 5 estarem integradas no branch, porque o arquivo de pendências descreve o que elas fizeram.

**Files:**
- Modify: `docs/deploy/fly.md` (tabela de nomes)
- Create: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md`

**Interfaces:**
- Consumes: os nomes de teste e de função das Tarefas 1 a 5, citados no arquivo de pendências.
- Produces: nada de código.

- [ ] **Step 1: Confira o que o grep de renomeação acha hoje**

Run:

```bash
grep -rn 'vigilume\.fly\.dev\|vigilume-db\|registry\.fly\.io/vigilume\|app = "vigilume"\|-a vigilume' \
  .github fly.toml deploy docs/deploy | grep -v '^docs/deploy/fly.md'
```

Expected: além das linhas que a tabela já lista, comentários em `fly.toml` (o `fly deploy --image` do cabeçalho), `deploy/fly/db/fly.toml` (o host `vigilume-db.internal`), `.github/workflows/backup.yml`, `ci.yml` e `deploy.yml`. Se aparecer um arquivo fora dessa lista, acrescente-o à linha nova do Step 2.

- [ ] **Step 2: Atualize o runbook e escreva as pendências**

Aplique em `docs/deploy/fly.md`:

````diff
diff --git a/docs/deploy/fly.md b/docs/deploy/fly.md
--- a/docs/deploy/fly.md
+++ b/docs/deploy/fly.md
@@ -337,6 +337,7 @@ pelo workflow. Nenhum deles é lido de outro: cada um está escrito à mão.
 | App do banco (`vigilume-db`) | `deploy/fly/db/fly.toml`: `app`; `.github/workflows/backup.yml`: o `-a` do `flyctl proxy`; o host do secret `DATABASE_URL` do app (`<app do banco>.internal`); o secret `FLY_BACKUP_TOKEN` |
 | Pacote do GHCR (`ghcr.io/bipoxdxd/vigilume`) | `.github/workflows/ci.yml`: `IMAGEM`; `.github/workflows/deploy.yml`: `ORIGEM`; `compose.yaml`: o `image` do `seed` e do `app` |
 | Este runbook | Os comandos com `-a vigilume`, `-a vigilume-db`, `registry.fly.io/vigilume`, e as URLs `https://vigilume.fly.dev` (passos 9 e 11) |
+| Comentários que citam os nomes | `fly.toml` (o `fly deploy --image` do cabeçalho); `deploy/fly/db/fly.toml` (o host `vigilume-db.internal`); `.github/workflows/backup.yml`, `ci.yml` e `deploy.yml` (as listas de onde cada nome está). Não mudam o comportamento, mas o `grep` abaixo os acha, e um comentário com o nome velho ensina o nome errado a quem renomear de novo |
 
 Depois do PR, este `grep` não deve achar o nome antigo fora desta seção (a tabela acima e o
 próprio comando citam os nomes antigos de propósito):
````

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md`:

````markdown
# Pendências depois do Plano 8

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-8.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 8

O grupo A das pendências (segurança e correção), escolhido pelo usuário em 2026-09-25. Spec:
`docs/superpowers/specs/2026-09-25-plano-8-endurecimento-design.md`.

| Pendência | Como fechou |
|---|---|
| Desafio do 2FA reutilizável por cinco minutos (`jti` em denylist ou `challenge_id` consumido) | Marca d'água em `users.totp_challenge_mark` (migration `00008`) e claim `cat` em microssegundos no desafio; o `/totp/verify` consome o desafio depois do código certo, num `UPDATE` condicional. Desafio reusado: `401 unauthorized` |
| `Unmap` no caminho `X-Forwarded-For`/`RemoteAddr` do `ip_hash` | Toda saída de `middleware.ClientIP` passa por `normalizar` (`Unmap`, zona recusada). O limitador já normalizava a chave do balde (`chaveDe`); o defeito atingia o `ip_hash` e o `ip` da auditoria e de `GET /sessions` |
| Nomes de cabeçalho perigosos em `CLIENT_IP_HEADER` | Denylist no boot: `Forwarded`, a família `X-Forwarded-*`, `Host`, `Cookie`, `Authorization`, `Proxy-Authorization`, os hop-by-hop, `Content-Length` e `Content-Type` |
| `HashPasswordWith` com parâmetros fora de faixa | Devolve `ErrInvalidHash` com `t=0`, `p=0`, memória abaixo de `8*p` ou salt vazio; a regra é a mesma função do `decodeHash` |
| `LogValue` de `Spec.Headers` (M6 do Plano 2) | `prober.Spec.LogValue` mostra os nomes dos headers, nunca os valores |
| `MaxChannels` "conta e insere sem trava" | **O registro estava errado** (R9 da spec do Plano 5): a trava (`LockUser`, `FOR UPDATE` na linha de `users`) existe desde o Plano 3. Faltava a prova: `TestConcurrentCreatesDoNotPassTheChannelLimit`, que fica vermelho sem a trava |
| Teste de `https` com host de loopback (PR #11) | `TestLoadRejectsDemoKeysOverHTTPS` cobre `https://localhost` e `https://127.0.0.1` |
| Grep de renomeação casava comentários (PR #11) | A tabela de nomes do runbook ganhou a linha dos comentários que citam os nomes |

## Dependem do dono do projeto (nenhum agente faz)

- Criar os apps, o volume, os papéis e os segredos no Fly, e o primeiro deploy (runbook
  `docs/deploy/fly.md`). **Tem custo recorrente** (estimativa do Plano 7: ~US$ 9–11/mês); a
  decisão de criar é do dono.
- Gravar os secrets de repositório (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`,
  `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`) e as variáveis (`PG_VOLUME_GB`, `BACKUP_ENABLED=1`);
  rodar o `backup` à mão uma vez (runbook, passo 13).
- Medir o endereço do proxy do Fly, preencher `TRUSTED_PROXIES` e levá-lo ao `fly.toml` por PR;
  o teste de dois pontos de rede (que também prova se o proxy sobrescreve um `Fly-Client-IP`
  mandado pelo cliente).
- **Tráfego do próprio Fly sem `Fly-Client-IP`** (runbook, passo 9): se, no primeiro deploy, o
  aviso "cabeçalho de IP do cliente ausente ou ilegível" voltar a cada minuto com o
  `remote_addr` do proxy, é tráfego do Fly (health check, por exemplo) que chega sem o
  cabeçalho. Não foi ensaiado; a resposta é decidir se o aviso deve ignorar esse caso, e não
  mudar o `fly.toml`.
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado.
- Escolher a licença e tornar o repositório e o pacote do GHCR públicos, quando quiser
  (runbook, última seção).
- Hospedagem sem custo (pergunta do dono em 2026-09-25): uma VM grátis rodando o
  `compose.yaml` seria um alvo de deploy novo, com runbook, HTTPS e backup próprios. Não é
  trabalho do Plano 8; vira plano próprio se o dono quiser.

## Limites conhecidos do Plano 8

- **Consumir um desafio invalida os mais antigos ainda abertos** do mesmo usuário. Dois logins
  quase simultâneos em dois aparelhos: se o mais novo terminar o 2FA primeiro, o mais antigo
  recebe 401 e refaz a senha.
- **O painel diz "O tempo para digitar o código acabou"** também para o desafio recusado por
  já ter sido usado ou por um mais novo ter sido consumido (`PaginaLogin.tsx` trata todo
  `401 unauthorized` do verify como desafio vencido). O remédio é o mesmo, entrar de novo; só
  o texto não é exato. O duplo clique não chega a isso: o botão fica desabilitado enquanto
  envia.
- **Desafios emitidos antes do deploy do Plano 8 morrem** (não têm `cat`): no pior caso, um
  login refeito dentro de cinco minutos.
- **Relógio andando para trás** entre dois desafios do mesmo usuário deixa o mais novo com
  `cat` menor que a marca: 401 e um login refeito.
- **Um desafio reusado com o código certo zera o contador de falhas** antes de ser recusado
  (o `ResetFailures` vem antes do consumo). Chegar lá exige a senha e o segundo fator.
- **Endereço de conexão com zona** (par IPv6 link-local ligado direto no app) continua como
  veio, com a zona. Não acontece atrás de proxy.

Os limites do Plano 7 seguem como estão no `PENDENCIAS-PARA-O-PLANO-8.md`, seção "Limites
conhecidos do Plano 7".

## Herdadas, ainda abertas

**Operação** (grupo B, fora do Plano 8 por decisão do usuário):

- **Recuperação do 2FA pelo CLI:** quem perde o autenticador e os códigos de recuperação só sai
  por SQL.
- **Troca de autenticador** sem desativar e reativar o 2FA.
- **Rota para desabilitar canal** (a coluna `enabled` já é respeitada pelo despachante).
- **Porta de métricas em desenvolvimento** com `--role=api` e `--role=prober` no mesmo host
  (M7 do Plano 2).

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" do
`PENDENCIAS-PARA-O-PLANO-5.md`, inteira.

**R2** (grupo D): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O método novo do Plano 8
(`ConsumeChallenge`) também é SQL à mão, no mesmo arquivo.
````


- [ ] **Step 3: Gates finais no branch integrado**

Run:

```bash
make test
make lint
make sec
sqlc diff
gofmt -l .
```

Expected: tudo verde; `gofmt -l` sem saída. Na validação, `govulncheck` informou "0 vulnerabilities" no código (uma em módulo requerido, não alcançada).

- [ ] **Step 4: Commit**

```bash
git add docs/deploy/fly.md docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md
git commit -m "docs: runbook com os comentários na tabela de nomes; pendências depois do Plano 8"
```
