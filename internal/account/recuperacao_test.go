package account_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// emailDeRecuperacao é uma chamada ao carteiro falso.
type emailDeRecuperacao struct{ para, assunto, corpo string }

// carteiro é o AccountMailer dos testes: guarda o que mandaram enviar.
type carteiro struct {
	mu       sync.Mutex
	enviados []emailDeRecuperacao
}

func (c *carteiro) SendAccountEmail(_ context.Context, to, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enviados = append(c.enviados, emailDeRecuperacao{to, subject, body})
	return nil
}

func (c *carteiro) todos() []emailDeRecuperacao {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]emailDeRecuperacao(nil), c.enviados...)
}


const senhaRedefinida = "a-senha-redefinida-pelo-link"

func novaRecuperacao(pool *pgxpool.Pool, clk clock.Clock) (*account.Recuperacao, *carteiro) {
	m := &carteiro{}
	r := account.NewRecuperacao(pool, clk, m, linksDeTeste)
	r.SetHash(hashBarato)
	return r, m
}

// tokenDoEmailDeRecuperacao tira o token do link de redefinição no corpo do e-mail.
func tokenDoEmailDeRecuperacao(t *testing.T, e emailDeRecuperacao) string {
	t.Helper()
	prefixo := linksDeTeste.RedefinirSenha("")
	for linha := range strings.SplitSeq(e.corpo, "\n") {
		if token, ok := strings.CutPrefix(linha, prefixo); ok && token != "" {
			return token
		}
	}
	t.Fatalf("o e-mail não traz o link de redefinição:\n%s", e.corpo)
	return ""
}

// pedirToken pede a redefinição e devolve o token do e-mail que saiu.
func pedirToken(t *testing.T, r *account.Recuperacao, m *carteiro, email string) string {
	t.Helper()
	antes := len(m.todos())
	if err := r.PedirRedefinicao(context.Background(), email); err != nil {
		t.Fatal(err)
	}
	enviados := m.todos()
	if len(enviados) != antes+1 {
		t.Fatalf("%d e-mails novos, queria 1", len(enviados)-antes)
	}
	return tokenDoEmailDeRecuperacao(t, enviados[len(enviados)-1])
}

// tokensDeRedefinicao lê, por fora da RLS, o vencimento dos tokens de
// redefinição do usuário (usados ou não).
func tokensDeRedefinicao(t *testing.T, user uuid.UUID) map[string]time.Time {
	t.Helper()
	rows, err := testdb.Admin(t).Query(context.Background(),
		`SELECT encode(token_hash, 'hex'), expires_at FROM account_tokens
		  WHERE user_id = $1 AND purpose = 'reset_password'`, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var h string
		var vence time.Time
		if err := rows.Scan(&h, &vence); err != nil {
			t.Fatal(err)
		}
		out[h] = vence
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func totalDeTokens(t *testing.T) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM account_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// senhaEntra é a conferência de senha do login: o hash gravado aceita a
// senha (pelo verificador barato, o par do hasher injetado).
func senhaEntra(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, plain string) bool {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, _, err := verificarBarato(context.Background(), hash, plain)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// familiaViva diz se a família ainda tem a linha corrente não revogada.
func familiaViva(t *testing.T, familia uuid.UUID) bool {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE family_id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
		familia).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// abrirSessao faz o que o login faz depois de conferir a senha: abre uma
// família de refresh com o carimbo do hash atual.
func abrirSessao(t *testing.T, pool *pgxpool.Pool, clk clock.Clock, user uuid.UUID) uuid.UUID {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)
	_, s, err := mgr.Issue(context.Background(), user, auth.PasswordStamp(hash), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	return s.FamilyID
}

func emailConfirmadoNoBanco(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(),
		`SELECT email_verified_at IS NOT NULL FROM users WHERE id = $1`, user).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// Com conta: um token reset_password de 1 h (só o hash gravado) e um e-mail
// para o endereço GRAVADO (não o digitado) com o link do token.
func TestPedirRedefinicaoComContaMandaLink(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)

	token := pedirToken(t, r, m, "  Dono@Exemplo.COM ")

	e := m.todos()[0]
	if e.para != "dono@exemplo.com" {
		t.Errorf("e-mail para %q, queria dono@exemplo.com", e.para)
	}
	if e.assunto == "" || !strings.Contains(e.corpo, linksDeTeste.RedefinirSenha(token)+"\n") {
		t.Errorf("e-mail sem assunto ou sem o link do token: %q\n%s", e.assunto, e.corpo)
	}
	tokens := tokensDeRedefinicao(t, c.user)
	vence, ok := tokens[fmt.Sprintf("%x", crypto.HashToken(token))]
	if len(tokens) != 1 || !ok {
		t.Fatalf("tokens gravados %v; queria só o hash do token do e-mail", tokens)
	}
	if queria := c.clk.Now().Add(time.Hour); !vence.Equal(queria) {
		t.Errorf("token vence em %v, queria %v (1 h)", vence, queria)
	}
}

// Sem conta: a mesma resposta (nil), e nada sai nem é gravado.
func TestPedirRedefinicaoSemContaNaoManda(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)

	if err := r.PedirRedefinicao(context.Background(), "ninguem@exemplo.com"); err != nil {
		t.Fatalf("sem conta devolveu %v; queria nil, como com conta", err)
	}
	if n := len(m.todos()); n != 0 {
		t.Errorf("%d e-mails enviados sem conta, queria 0", n)
	}
	if n := totalDeTokens(t); n != 0 {
		t.Errorf("%d tokens gravados sem conta, queria 0", n)
	}
}

// Só o último e-mail vale: pedir de novo invalida o link anterior.
func TestPedirDeNovoInvalidaOLinkAnterior(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)
	ctx := context.Background()

	primeiro := pedirToken(t, r, m, "dono@exemplo.com")
	segundo := pedirToken(t, r, m, "dono@exemplo.com")

	if err := r.Redefinir(ctx, primeiro, senhaRedefinida); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("o primeiro link devolveu %v; queria ErrTokenInvalido", err)
	}
	if err := r.Redefinir(ctx, segundo, senhaRedefinida); err != nil {
		t.Fatalf("o segundo link devolveu %v; queria nil", err)
	}
}

// A redefinição troca a senha, encerra TODAS as sessões (a "perda" pode ser
// um vazamento), apaga os tokens de redefinição e audita.
func TestRedefinirTrocaSenhaERevogaSessoes(t *testing.T) {
	c := montar(t)
	outra := abrirSessao(t, c.pool, c.clk, c.user)
	r, m := novaRecuperacao(c.pool, c.clk)

	token := pedirToken(t, r, m, "dono@exemplo.com")
	if err := r.Redefinir(context.Background(), token, senhaRedefinida); err != nil {
		t.Fatal(err)
	}

	if senhaEntra(t, c.pool, c.user, senha) {
		t.Error("a senha antiga ainda entra")
	}
	if !senhaEntra(t, c.pool, c.user, senhaRedefinida) {
		t.Error("a senha nova não entra")
	}
	for _, f := range []uuid.UUID{c.atual, outra} {
		if familiaViva(t, f) {
			t.Errorf("a família %s continuou viva", f)
		}
	}
	if n := eventos(t, c.pool, c.user, "user.password_reset_by_email"); n != 1 {
		t.Errorf("%d eventos user.password_reset_by_email, queria 1", n)
	}
	if tokens := tokensDeRedefinicao(t, c.user); len(tokens) != 0 {
		t.Errorf("sobraram %d tokens de redefinição", len(tokens))
	}
}

// Foco da revisão 1: o atacante cadastra o e-mail da vítima, não confirma e
// fica com uma sessão. A vítima, dona da caixa, pede o link e redefine: o
// e-mail fica confirmado, a sessão do atacante cai e a senha dele não entra.
func TestRedefinirTomaContaPreCriadaEDerrubaSessoes(t *testing.T) {
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	const senhaDoAtacante = "senha-do-atacante-bem-longa"
	hash, err := hashBarato(context.Background(), senhaDoAtacante)
	if err != nil {
		t.Fatal(err)
	}
	conta := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'vitima@exemplo.com', $2)`, conta, hash); err != nil {
		t.Fatal(err)
	}
	if emailConfirmadoNoBanco(t, pool, conta) {
		t.Fatal("o cenário precisa da conta não confirmada")
	}
	doAtacante := abrirSessao(t, pool, clk, conta)

	r, m := novaRecuperacao(pool, clk)
	token := pedirToken(t, r, m, "vitima@exemplo.com")
	if err := r.Redefinir(context.Background(), token, senhaRedefinida); err != nil {
		t.Fatal(err)
	}

	if !emailConfirmadoNoBanco(t, pool, conta) {
		t.Error("a redefinição não confirmou o e-mail, e ela prova a posse da caixa")
	}
	if familiaViva(t, doAtacante) {
		t.Error("a sessão do atacante continuou viva")
	}
	if senhaEntra(t, pool, conta, senhaDoAtacante) {
		t.Error("a senha do atacante ainda entra")
	}
	if !senhaEntra(t, pool, conta, senhaRedefinida) {
		t.Error("a senha da vítima não entra")
	}
}

// A redefinição prova a caixa de e-mail, não o segundo fator: o 2FA
// confirmado continua ligado.
func TestRedefinirMantemO2FA(t *testing.T) {
	c := montar(t)
	cadastrarTOTP(t, c, true)
	r, m := novaRecuperacao(c.pool, c.clk)

	token := pedirToken(t, r, m, "dono@exemplo.com")
	if err := r.Redefinir(context.Background(), token, senhaRedefinida); err != nil {
		t.Fatal(err)
	}
	if temSegredo, confirmado := segredoPendente(t, c); !temSegredo || !confirmado {
		t.Errorf("2FA depois da redefinição: segredo=%v confirmado=%v; queria os dois", temSegredo, confirmado)
	}
}

// Token usado e token vencido dão o mesmo ErrTokenInvalido, e a senha não
// muda.
func TestRedefinirTokenUsadoOuVencido(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)
	ctx := context.Background()

	usado := pedirToken(t, r, m, "dono@exemplo.com")
	if err := r.Redefinir(ctx, usado, senhaRedefinida); err != nil {
		t.Fatal(err)
	}
	if err := r.Redefinir(ctx, usado, "outra-senha-bem-comprida-1"); !errors.Is(err, account.ErrTokenInvalido) {
		t.Errorf("token usado devolveu %v; queria ErrTokenInvalido", err)
	}

	vencido := pedirToken(t, r, m, "dono@exemplo.com")
	c.clk.Advance(account.ValidadeDaRedefinicao + time.Second)
	if err := r.Redefinir(ctx, vencido, "outra-senha-bem-comprida-2"); !errors.Is(err, account.ErrTokenInvalido) {
		t.Errorf("token vencido devolveu %v; queria ErrTokenInvalido", err)
	}

	if !senhaEntra(t, c.pool, c.user, senhaRedefinida) {
		t.Error("a senha mudou com token usado ou vencido")
	}
}

// A política vem antes do token: a senha fraca não gasta o link.
func TestRedefinirSenhaFracaNaoGastaToken(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)
	ctx := context.Background()

	token := pedirToken(t, r, m, "dono@exemplo.com")
	var pe account.PolicyError
	if err := r.Redefinir(ctx, token, strings.Repeat("a", 14)); !errors.As(err, &pe) || pe.Reason != auth.ReasonTooShort {
		t.Fatalf("senha de 14 caracteres devolveu %v; queria PolicyError too_short", err)
	}
	if err := r.Redefinir(ctx, token, senhaRedefinida); err != nil {
		t.Fatalf("o token não serviu depois da senha fraca: %v", err)
	}
}

// Token lixo é recusado antes do Argon2id: chutar tokens não gasta o
// semáforo global de derivações.
func TestRedefinirTokenInvalidoNaoRodaArgon2(t *testing.T) {
	c := montar(t)
	r, _ := novaRecuperacao(c.pool, c.clk)
	var hashes atomic.Int32
	r.SetHash(func(ctx context.Context, plain string) (string, error) {
		hashes.Add(1)
		return hashBarato(ctx, plain)
	})

	if err := r.Redefinir(context.Background(), "lixo", senhaRedefinida); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("token lixo devolveu %v; queria ErrTokenInvalido", err)
	}
	if n := hashes.Load(); n != 0 {
		t.Errorf("%d derivações com token lixo, queria 0", n)
	}
}

// Foco da revisão 2: o mesmo link usado ao mesmo tempo (clique duplo,
// scanner mais humano) tem exatamente um vencedor, e a senha final é a dele.
func TestRedefinicaoSimultaneaSoUmaVence(t *testing.T) {
	c := montar(t)
	r, m := novaRecuperacao(c.pool, c.clk)
	token := pedirToken(t, r, m, "dono@exemplo.com")

	const n = 10
	erros := make([]error, n)
	largada := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-largada
			erros[i] = r.Redefinir(context.Background(), token, fmt.Sprintf("senha-simultanea-numero-%02d", i))
		})
	}
	close(largada)
	wg.Wait()

	vencedor := -1
	for i, err := range erros {
		switch {
		case err == nil:
			if vencedor >= 0 {
				t.Errorf("duas redefinições venceram: %d e %d", vencedor, i)
			}
			vencedor = i
		case !errors.Is(err, account.ErrTokenInvalido):
			t.Errorf("goroutine %d: %v; queria nil ou ErrTokenInvalido", i, err)
		}
	}
	if vencedor < 0 {
		t.Fatal("nenhuma redefinição venceu")
	}
	if !senhaEntra(t, c.pool, c.user, fmt.Sprintf("senha-simultanea-numero-%02d", vencedor)) {
		t.Error("a senha final não é a do vencedor")
	}
}
