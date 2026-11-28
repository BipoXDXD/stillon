package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// postsQueNaoCriam são os POST registrados que não criam recurso, e por isso
// ficam fora da trava de e-mail confirmado. O enroll do 2FA não cria recurso,
// mas fica de fora daqui de propósito: conta não confirmada não liga 2FA (ver
// TestContaNaoConfirmadaNaoCadastra2FA). Todo outro POST cria e tem de dar
// 403 email_not_verified a conta não confirmada: uma rota de criação nova,
// esquecida sem a trava, reprova TestContaNaoConfirmadaNaoCriaPorNenhumaRota.
// Rota nova entra aqui só de propósito, com o porquê.
var postsQueNaoCriam = map[string]string{
	"POST /api/v1/auth/login":                                                       "é onde a sessão nasce; quem não confirmou precisa entrar para confirmar",
	"POST /api/v1/auth/refresh":                                                     "renova a sessão que já existe",
	"POST /api/v1/auth/logout":                                                      "encerra a sessão",
	"POST /api/v1/auth/totp/verify":                                                 "segundo passo do login, antes de haver sessão",
	"POST /api/v1/auth/signup":                                                      "pública: cria a conta, que nasce não confirmada; nenhum recurso atrás da trava",
	"POST /api/v1/auth/email/confirm":                                               "pública: é a própria confirmação, pelo token do e-mail",
	"POST /api/v1/auth/email/resend":                                                "reenvia a confirmação à própria conta não confirmada (tem balde próprio)",
	"POST /api/v1/projects/{projectID}/titular-transfer":                            "propor a transferência não cria nada: quem recebe ganha o projeto só no aceite, que passa pela trava",
	"POST /api/v1/invitations/accept":                                               "o token do convite prova a caixa de entrada; não cria recurso, entra num projeto que já existe",
	"POST /api/v1/auth/password/forgot":                                             "pública: pede o link de redefinição, sem criar recurso",
	"POST /api/v1/auth/password/reset":                                              "pública: troca a senha pelo token do e-mail, sem criar recurso",
	"POST /api/v1/auth/password":                                                    "troca de senha: proteger a conta não espera a confirmação",
	"POST /api/v1/auth/totp/confirm":                                                "2FA: só confirma o cadastro que o enroll (com a trava) começou",
	"POST /api/v1/auth/totp/disable":                                                "2FA: mexe no fator da conta, não cria recurso",
	"POST /api/v1/auth/totp/rotate":                                                 "2FA: troca o autenticador, não cria recurso",
	"POST /api/v1/auth/totp/rotate/confirm":                                         "2FA: confirma a troca do autenticador",
	"POST /api/v1/incidents/{id}/ack":                                               "ack de incidente: mexe num incidente que já existe",
	"POST /api/v1/projects/{projectID}/incidents/{id}/ack":                          "ack de incidente: mexe num incidente que já existe",
	"POST /api/v1/heartbeats/{id}/incidents/{incident_id}/ack":                      "ack de incidente: mexe num incidente que já existe",
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/incidents/{incident_id}/ack": "ack de incidente: mexe num incidente que já existe",
	"POST /api/v1/heartbeats/{id}/token/rotate":                                     "edita um heartbeat que já existe (token novo, nenhum recurso novo)",
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/token/rotate":                "edita um heartbeat que já existe (token novo, nenhum recurso novo)",
	"POST /api/v1/heartbeats/{id}/ping-now":                                         "registra um sinal num heartbeat que já existe",
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/ping-now":                    "registra um sinal num heartbeat que já existe",
	"POST /api/v1/heartbeats/{id}/events/clear":                                     "apaga eventos de um heartbeat que já existe",
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/events/clear":                "apaga eventos de um heartbeat que já existe",
	"POST /api/v1/channels/{id}/verify/resend":                                      "reenvio de verificação de um canal que já existe (tem o balde de envio)",
	"POST /api/v1/projects/{projectID}/channels/{id}/verify/resend":                 "reenvio de verificação de um canal que já existe (tem o balde de envio)",
	"POST /verify/{token}":                                                          "pública: confirma um canal pelo token do e-mail, sem sessão",
	"POST /ping/{token}":                                                            "pública: sinal da tarefa agendada, sem sessão",
	"POST /ping/{token}/start":                                                      "pública: sinal da tarefa agendada, sem sessão",
	"POST /ping/{token}/fail":                                                       "pública: sinal da tarefa agendada, sem sessão",
	"POST /ping/{token}/log":                                                        "pública: sinal da tarefa agendada, sem sessão",
	"POST /ping/{token}/{exit_code}":                                                "pública: sinal da tarefa agendada, sem sessão",
}

// contaNaoConfirmada sobe a API com o usuário da fixture, cria (ainda
// confirmado) um heartbeat dele e então apaga a confirmação: é a conta do
// cadastro público antes do link. Devolve o access token, o Projeto padrão e o
// heartbeat.
func contaNaoConfirmada(t *testing.T) (a *api, token, projeto, hb string) {
	t.Helper()
	a = newAPI(t)
	_, token = entrar(t, a)
	hb, _ = criarHB(t, a, token, corpoDePeriodo)
	projeto = testdb.ProjetoPadrao(t, a.userID).String()
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET email_verified_at = NULL WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	return a, token, projeto, hb
}

// Review Focus 5: todo POST registrado que cria recurso, nas rotas antigas e
// nas de projeto (inclusive o clone de heartbeat), dá 403 email_not_verified a
// conta não confirmada. A lista vem de rotasRegistradas, não da memória: uma
// rota de criação nova sem a trava reprova aqui.
func TestContaNaoConfirmadaNaoCriaPorNenhumaRota(t *testing.T) {
	rotas := rotasRegistradas(t)
	for rota := range postsQueNaoCriam {
		if !slices.Contains(rotas, rota) {
			t.Errorf("postsQueNaoCriam tem %q, que não está mais registrada: tire-a", rota)
		}
	}

	a, token, projeto, hb := contaNaoConfirmada(t)
	preencher := strings.NewReplacer("{projectID}", projeto, "{id}", hb)
	var testadas int
	for _, rota := range rotas {
		metodo, caminho, _ := strings.Cut(rota, " ")
		if _, naoCria := postsQueNaoCriam[rota]; metodo != http.MethodPost || naoCria {
			continue
		}
		testadas++
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, metodo, preencherCuringas(preencher.Replace(caminho)), token, map[string]any{})
		if status != http.StatusForbidden || !strings.Contains(corpo, `"email_not_verified"`) {
			t.Errorf("%s de conta não confirmada deu %d %s, queria 403 email_not_verified", rota, status, corpo)
		}
	}
	// Projetos, alvos, heartbeats, clones, canais e páginas de status, nas duas
	// variantes (o projeto só tem uma), mais o enroll do 2FA: 12 rotas hoje.
	if testadas < 12 {
		t.Errorf("só %d POST travados testados, queria ao menos 12: a enumeração deixou rotas de fora", testadas)
	}
}

// A trava é só de criação: ler e editar o que já existe continua liberado, e o
// /me diz ao painel que falta confirmar.
func TestContaNaoConfirmadaLeEEdita(t *testing.T) {
	a, token, _, hb := contaNaoConfirmada(t)
	if status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/targets", token, nil); status != http.StatusOK {
		t.Errorf("GET /api/v1/targets deu %d %s, queria 200", status, corpo)
	}
	if status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+hb, token,
		map[string]any{"name": "renomeado"}); status != http.StatusOK {
		t.Errorf("PATCH do heartbeat deu %d %s, queria 200", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/me deu %d %s", status, corpo)
	}
	var me map[string]any
	if err := json.Unmarshal([]byte(corpo), &me); err != nil {
		t.Fatal(err)
	}
	if v, ok := me["email_verified"].(bool); !ok || v {
		t.Errorf("/me trouxe email_verified = %v (%T), queria false: %s", me["email_verified"], me["email_verified"], corpo)
	}
}

// Confirmado o e-mail, o mesmo usuário cria: a trava olha a coluna a cada
// pedido, sem precisar de sessão nova.
func TestContaConfirmadaCria(t *testing.T) {
	a, token, _, _ := contaNaoConfirmada(t)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/targets", token, alvoValido); status != http.StatusCreated {
		t.Errorf("POST /api/v1/targets de conta confirmada deu %d %s, queria 201", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/me", token, nil)
	if status != http.StatusOK || !strings.Contains(corpo, `"email_verified":true`) {
		t.Errorf("/me de conta confirmada deu %d %s, queria email_verified true", status, corpo)
	}
}

// Conta não confirmada não liga 2FA: quem cadastrasse o e-mail da vítima e
// ligasse o 2FA a trancaria para fora, porque a redefinição de senha mantém o
// fator. Confirmado o e-mail, o enroll volta a funcionar.
func TestContaNaoConfirmadaNaoCadastra2FA(t *testing.T) {
	a, token, _, _ := contaNaoConfirmada(t)
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/auth/totp/enroll", token, map[string]string{"password": testPass})
	if status != http.StatusForbidden || !strings.Contains(corpo, `"email_not_verified"`) {
		t.Fatalf("enroll de conta não confirmada deu %d %s, queria 403 email_not_verified", status, corpo)
	}
	var comSegredo bool
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc IS NOT NULL OR totp_pending_secret_enc IS NOT NULL FROM users WHERE id = $1`,
		a.userID).Scan(&comSegredo); err != nil {
		t.Fatal(err)
	}
	if comSegredo {
		t.Error("o enroll recusado gravou um segredo de 2FA")
	}
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	iniciarCadastroHTTP(t, a, token)
}
