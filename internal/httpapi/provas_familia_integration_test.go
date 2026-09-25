package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// O contador de provas por família das rotas autenticadas (A3 do Plano 10):
// a troca de senha e o enroll, confirm, disable, rotate e rotate/confirm do
// 2FA. As provas erradas não tocam o bloqueio da conta; a quinta, na mesma
// família, derruba a família e responde 401 unauthorized.

// provasPorFamilia é o limite do contrato (spec do Plano 10, §4): a quinta
// prova errada derruba a família. É auth.FamilyProofLimit, escrito aqui como
// número para o teste não seguir o código se o limite mudar.
const provasPorFamilia = 5

// provaErrada manda uma prova errada para a rota e devolve o status e o code.
type provaErrada func(t *testing.T, a *api, token string) (int, string)

func postEProva(rota string, corpo func(t *testing.T, a *api) map[string]string) provaErrada {
	return func(t *testing.T, a *api, token string) (int, string) {
		t.Helper()
		resp := postComToken(t, http.DefaultClient, a.srv.URL+rota, token, corpo(t, a))
		defer resp.Body.Close()
		return resp.StatusCode, codigoDoErro(t, resp)
	}
}

// codigoErradoPara é um código de seis dígitos que não é o do segredo agora.
func codigoErradoPara(t *testing.T, a *api, segredo string) string {
	t.Helper()
	if codigoDe(t, a, segredo) == "000000" {
		return "111111"
	}
	return "000000"
}

// eventosDaFamilia conta os registros de auditoria de uma ação sobre a
// família dada.
func eventosDaFamilia(t *testing.T, a *api, acao string, familia uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2 AND entity = 'session_family' AND entity_id = $3`,
		a.userID, acao, familia).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Em cada rota autenticada de credencial, as quatro primeiras provas erradas
// na mesma família têm a resposta de sempre da rota (401 invalid_credentials;
// 400 validation_failed no confirm do 2FA), e a quinta derruba a família: 401
// unauthorized, GET /sessions recusa o token, a auditoria session.proof_limit
// aponta a família, e o bloqueio da conta fica em zero.
func TestFifthWrongProofDropsTheFamily(t *testing.T) {
	casos := []struct {
		nome string
		// preparar põe a conta no estado da rota e devolve a prova errada.
		preparar func(t *testing.T, a *api, token string) provaErrada
		// antes é a resposta das quatro primeiras.
		status int
		codigo string
	}{
		{"troca de senha", func(t *testing.T, a *api, token string) provaErrada {
			return postEProva("/api/v1/auth/password", func(*testing.T, *api) map[string]string {
				return map[string]string{"current_password": "nao-e-a-senha-atual", "new_password": senhaNova}
			})
		}, http.StatusUnauthorized, "invalid_credentials"},
		{"enroll", func(t *testing.T, a *api, token string) provaErrada {
			return postEProva("/api/v1/auth/totp/enroll", func(*testing.T, *api) map[string]string {
				return map[string]string{"password": "senha-que-nao-e-a-dele"}
			})
		}, http.StatusUnauthorized, "invalid_credentials"},
		{"confirm", func(t *testing.T, a *api, token string) provaErrada {
			resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token,
				map[string]string{"password": testPass})
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("enroll devolveu %d", resp.StatusCode)
			}
			return postEProva("/api/v1/auth/totp/confirm", func(*testing.T, *api) map[string]string {
				return map[string]string{"code": "000000"}
			})
		}, http.StatusBadRequest, "validation_failed"},
		{"disable", func(t *testing.T, a *api, token string) provaErrada {
			segredo := cadastrarTOTP(t, a, clientWithJar(t))
			return postEProva("/api/v1/auth/totp/disable", func(t *testing.T, a *api) map[string]string {
				return map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)}
			})
		}, http.StatusUnauthorized, "invalid_credentials"},
		{"rotate", func(t *testing.T, a *api, token string) provaErrada {
			segredo := cadastrarTOTP(t, a, clientWithJar(t))
			return postEProva("/api/v1/auth/totp/rotate", func(t *testing.T, a *api) map[string]string {
				return map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)}
			})
		}, http.StatusUnauthorized, "invalid_credentials"},
		{"rotate/confirm", func(t *testing.T, a *api, token string) provaErrada {
			velho := cadastrarTOTP(t, a, clientWithJar(t))
			if st, _ := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)}); st != http.StatusOK {
				t.Fatalf("rotate devolveu %d", st)
			}
			return postEProva("/api/v1/auth/totp/rotate/confirm", func(t *testing.T, a *api) map[string]string {
				return map[string]string{"code": codigoErradoPara(t, a, velho), "rotation": a.rotacao}
			})
		}, http.StatusUnauthorized, "invalid_credentials"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			token, _, familia := familiaComRefresh(t, a)
			errar := caso.preparar(t, a, token)

			for i := 1; i < provasPorFamilia; i++ {
				if st, codigo := errar(t, a, token); st != caso.status || codigo != caso.codigo {
					t.Fatalf("prova errada %d: %d %s, queria %d %s", i, st, codigo, caso.status, caso.codigo)
				}
			}
			if st := statusDaListaDeSessoes(t, a, token); st != http.StatusOK {
				t.Fatalf("a família caiu antes da quinta prova errada (GET /sessions: %d)", st)
			}
			if st, codigo := errar(t, a, token); st != http.StatusUnauthorized || codigo != "unauthorized" {
				t.Errorf("quinta prova errada: %d %s, queria 401 unauthorized", st, codigo)
			}
			if st := statusDaListaDeSessoes(t, a, token); st != http.StatusUnauthorized {
				t.Errorf("GET /sessions com o token da família derrubada: %d, queria 401", st)
			}
			if n := eventosDaFamilia(t, a, "session.proof_limit", familia); n != 1 {
				t.Errorf("%d eventos session.proof_limit da família, queria 1", n)
			}
			if n := falhasDeLogin(t, a); n != 0 {
				t.Errorf("%d falhas no bloqueio da conta, queria 0", n)
			}
		})
	}
}

// O orçamento é da FAMÍLIA, e não da rota nem da conta: provas erradas em
// rotas diferentes somam na mesma família (inclusive o código ausente), e as
// de outra família do mesmo dono não entram na conta.
func TestWrongProofBudgetIsPerFamilyAcrossRoutes(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	tokenA, _, _ := familiaComRefresh(t, a)
	tokenB, _, _ := familiaComRefresh(t, a)
	disable := postEProva("/api/v1/auth/totp/disable", func(t *testing.T, a *api) map[string]string {
		return map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)}
	})
	rotate := postEProva("/api/v1/auth/totp/rotate", func(t *testing.T, a *api) map[string]string {
		return map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)}
	})
	senha := postEProva("/api/v1/auth/password", func(*testing.T, *api) map[string]string {
		return map[string]string{"current_password": "nao-e-a-senha-atual", "new_password": senhaNova}
	})
	// O código ausente conta como errado: a senha sozinha nunca desliga nem
	// troca o fator.
	disableSemCodigo := postEProva("/api/v1/auth/totp/disable", func(*testing.T, *api) map[string]string {
		return map[string]string{"password": testPass}
	})
	rotateSemCodigo := postEProva("/api/v1/auth/totp/rotate", func(*testing.T, *api) map[string]string {
		return map[string]string{"password": testPass}
	})

	for i, errar := range []provaErrada{disable, disableSemCodigo, rotate, rotateSemCodigo} {
		if st, codigo := errar(t, a, tokenA); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("família A, prova errada %d: %d %s, queria 401 invalid_credentials", i+1, st, codigo)
		}
	}
	for i := range provasPorFamilia - 1 {
		if st, codigo := disable(t, a, tokenB); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("família B, prova errada %d: %d %s, queria 401 invalid_credentials", i+1, st, codigo)
		}
	}
	if st, codigo := senha(t, a, tokenA); st != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("quinta prova errada da família A (na troca de senha): %d %s, queria 401 unauthorized", st, codigo)
	}
	if st := statusDaListaDeSessoes(t, a, tokenB); st != http.StatusOK {
		t.Errorf("a família B caiu junto com a A: GET /sessions %d", st)
	}
}

// A2 (Plano 10) nas rotas autenticadas: a reserva vem antes da verificação.
// Doze senhas erradas simultâneas na mesma família rodam exatamente cinco
// Argon2id; as outras nascem esgotadas (ou chegam com a família já derrubada)
// e são recusadas sem verificar. Quatro respostas são invalid_credentials e o
// resto unauthorized.
func TestParallelWrongProofsVerifyAtMostTheBudget(t *testing.T) {
	a := newAPI(t)
	token := accessTokenValido(t, a)

	antes := auth.Argon2Verifications()
	var mu sync.Mutex
	codigos := map[string]int{}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			resp, err := postSemT(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token,
				map[string]string{"password": "senha-que-nao-e-a-dele"})
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			var env envelope
			_ = json.NewDecoder(resp.Body).Decode(&env)
			mu.Lock()
			codigos[env.Error.Code]++
			mu.Unlock()
		})
	}
	wg.Wait()

	if n := auth.Argon2Verifications() - antes; n != provasPorFamilia {
		t.Errorf("%d Argon2id com 12 provas simultâneas, queria %d", n, provasPorFamilia)
	}
	if codigos["invalid_credentials"] != provasPorFamilia-1 || codigos["unauthorized"] != 12-(provasPorFamilia-1) {
		t.Errorf("respostas %v, queria %d invalid_credentials e %d unauthorized",
			codigos, provasPorFamilia-1, 12-(provasPorFamilia-1))
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas no bloqueio da conta, queria 0", n)
	}
}

// C1 da revisão do Plano 10: quem tem a sessão e a senha, mas não o fator,
// não pode zerar o orçamento da família com uma prova só de senha. Antes, o
// enroll com a senha certa zerava a família (Succeed antes do Enroll) e só
// depois respondia 403 por o 2FA já estar ligado: quatro códigos chutados no
// disable (ou no rotate), um enroll, e mais quatro, sem fim. Agora o enroll
// com o 2FA ligado devolve a reserva sem zerar nada, e a quinta prova errada
// derruba a família.
func TestPasswordAloneDoesNotResetTheFamilyBudget(t *testing.T) {
	for _, rota := range []string{"/api/v1/auth/totp/disable", "/api/v1/auth/totp/rotate"} {
		t.Run(rota, func(t *testing.T) {
			a := newAPI(t)
			segredo := cadastrarTOTP(t, a, clientWithJar(t))
			token, _, familia := familiaComRefresh(t, a)
			chutar := postEProva(rota, func(t *testing.T, a *api) map[string]string {
				return map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)}
			})
			enroll := postEProva("/api/v1/auth/totp/enroll", func(*testing.T, *api) map[string]string {
				return map[string]string{"password": testPass}
			})
			confirm := postEProva("/api/v1/auth/totp/confirm", func(t *testing.T, a *api) map[string]string {
				return map[string]string{"code": codigoDe(t, a, segredo)}
			})

			for i := 1; i < provasPorFamilia; i++ {
				if st, codigo := chutar(t, a, token); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
					t.Fatalf("chute %d: %d %s, queria 401 invalid_credentials", i, st, codigo)
				}
			}
			if st, codigo := enroll(t, a, token); st != http.StatusForbidden || codigo != "forbidden" {
				t.Fatalf("enroll com o 2FA ligado: %d %s, queria 403 forbidden", st, codigo)
			}
			// O confirm com o 2FA ligado também é 403 sem provar nada, e
			// também não pode zerar a família.
			if st, codigo := confirm(t, a, token); st != http.StatusForbidden || codigo != "forbidden" {
				t.Fatalf("confirm com o 2FA ligado: %d %s, queria 403 forbidden", st, codigo)
			}
			if st, codigo := chutar(t, a, token); st != http.StatusUnauthorized || codigo != "unauthorized" {
				t.Errorf("quinta prova errada depois do enroll e do confirm: %d %s, queria 401 unauthorized (algum deles zerou a família?)", st, codigo)
			}
			if st := statusDaListaDeSessoes(t, a, token); st != http.StatusUnauthorized {
				t.Errorf("GET /sessions com o token da família que chutou: %d, queria 401", st)
			}
			if n := eventosDaFamilia(t, a, "session.proof_limit", familia); n != 1 {
				t.Errorf("%d eventos session.proof_limit da família, queria 1", n)
			}
		})
	}
}

// C1(b) da revisão do Plano 10: com o 2FA ligado, o /totp/confirm responde 403
// ANTES de olhar o código. Antes ele conferia o código contra o segredo ativo
// e respondia 403 ao certo e 400 ao errado: um oráculo que, com o enroll
// zerando a família, dava para consultar sem limite. O ciclo é o da prova de
// conceito da revisão (quatro chutes no confirm e um enroll com a senha,
// repetido): nenhuma resposta distingue o código certo, e o confirm não gasta
// prova (não há o que provar), então a família continua viva.
func TestConfirmWithTheFactorOnIsNotAnOracle(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	token, _, _ := familiaComRefresh(t, a)
	confirm := func(codigo string) (int, string) {
		t.Helper()
		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", token,
			map[string]string{"code": codigo})
		defer resp.Body.Close()
		return resp.StatusCode, codigoDoErro(t, resp)
	}
	enroll := postEProva("/api/v1/auth/totp/enroll", func(*testing.T, *api) map[string]string {
		return map[string]string{"password": testPass}
	})

	for rodada := 1; rodada <= 3; rodada++ {
		for i := 1; i < provasPorFamilia; i++ {
			if st, codigo := confirm(codigoErradoPara(t, a, segredo)); st != http.StatusForbidden || codigo != "forbidden" {
				t.Fatalf("rodada %d, chute %d no confirm: %d %s, queria 403 forbidden", rodada, i, st, codigo)
			}
		}
		if st, codigo := enroll(t, a, token); st != http.StatusForbidden || codigo != "forbidden" {
			t.Fatalf("rodada %d, enroll: %d %s, queria 403 forbidden", rodada, st, codigo)
		}
	}
	if st, codigo := confirm(codigoDe(t, a, segredo)); st != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("confirm com o código certo e o 2FA ligado: %d %s, queria o mesmo 403 forbidden do errado", st, codigo)
	}
	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusOK {
		t.Errorf("a família caiu com confirms que não provam nada: GET /sessions %d", st)
	}
	// O confirm não consumiu o timestep: o código certo ainda desliga o fator.
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/disable", token,
		map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("disable com o código que o confirm recusou: %d, queria 204", resp.StatusCode)
	}
}
