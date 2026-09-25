package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/config"
)

// prenderVagasDoArgon2 segura n vagas do semáforo do Argon2id (A1) até o fim
// do teste, ou até a função devolvida soltá-las.
func prenderVagasDoArgon2(t *testing.T, n int) func() {
	t.Helper()
	var vagas []func()
	soltar := func() {
		for _, s := range vagas {
			s()
		}
		vagas = nil
	}
	t.Cleanup(soltar)
	for range n {
		s, err := auth.ReserveArgon2(context.Background())
		if err != nil {
			t.Fatalf("prender vaga %d de %d: %v", len(vagas)+1, n, err)
		}
		vagas = append(vagas, s)
	}
	return soltar
}

// A1 (Plano 10, §3.1): com o teto do Argon2id cheio, as rotas que pagam um
// Argon2id respondem 429 rate_limited com Retry-After: 2 depois do prazo de
// aquisição, em vez de empilhar 64 MiB por requisição até o processo morrer.
// Cada caminho é um: senha errada no login, e-mail inexistente (VerifyDummy),
// a senha de uma rota autenticada (enroll), a troca de senha e a conta
// bloqueada. Nenhum conta no bloqueio: a senha não foi verificada.
//
// O teto vem de ARGON2_MAX_CONCURRENT pelas rotas: com 5 (acima do padrão, que
// é no máximo 4), 4 vagas presas ainda deixam o login passar, e é essa perna
// que prova que a config chegou ao semáforo.
func TestSaturatedArgon2AnswersRateLimited(t *testing.T) {
	a := newAPIWith(t, func(c *config.Config) { c.Argon2MaxConcurrent = 5 })
	token := accessTokenValido(t, a)

	soltar := prenderVagasDoArgon2(t, 4)
	resp := login(t, a, clientWithJar(t), testEmail, "senha-errada-com-mais-de-15")
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Fatalf("login com 4 de 5 vagas presas: %d %s, queria 401 invalid_credentials (o teto da config não chegou ao semáforo?)",
			resp.StatusCode, codigo)
	}
	soltar()
	if _, err := a.pool.Exec(t.Context(), `UPDATE users SET failed_attempts = 0 WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}

	soltar = prenderVagasDoArgon2(t, 5)
	for nome, fazer := range map[string]func() *http.Response{
		"login com senha errada": func() *http.Response {
			return login(t, a, clientWithJar(t), testEmail, "senha-errada-com-mais-de-15")
		},
		"login de e-mail inexistente": func() *http.Response {
			return login(t, a, clientWithJar(t), "ninguem@exemplo.com", testPass)
		},
		"enroll": func() *http.Response {
			return postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token,
				map[string]string{"password": testPass})
		},
		"troca de senha": func() *http.Response {
			return trocarSenha(t, a, token, map[string]string{
				"current_password": testPass, "new_password": "outra-senha-bem-comprida",
			})
		},
	} {
		resp := fazer()
		codigo := codigoDoErro(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests || codigo != "rate_limited" {
			t.Errorf("%s com o semáforo cheio: %d %s, queria 429 rate_limited", nome, resp.StatusCode, codigo)
		}
		if ra := resp.Header.Get("Retry-After"); ra != "2" {
			t.Errorf("%s: Retry-After = %q, queria \"2\"", nome, ra)
		}
	}
	soltar() // a conferência da senha gravada abaixo também paga um Argon2id
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas contadas sem senha verificada", n)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a troca de senha recusada pelo semáforo gravou a senha nova")
	}

	// A conta bloqueada paga o VerifyDummy, e ele passa pelo mesmo semáforo:
	// com o teto cheio, o mesmo 429. Um 401 rápido aqui anunciaria o bloqueio
	// (R34) justamente sob ataque.
	bloquearConta(t, a)
	prenderVagasDoArgon2(t, 5)
	resp = login(t, a, clientWithJar(t), testEmail, testPass)
	codigo = codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || codigo != "rate_limited" {
		t.Errorf("login da conta bloqueada com o semáforo cheio: %d %s, queria 429 rate_limited", resp.StatusCode, codigo)
	}
}
