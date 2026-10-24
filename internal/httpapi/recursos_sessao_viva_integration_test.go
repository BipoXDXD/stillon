package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// recursosPersistidos lê o estado real sob RLS, incluindo os hashes de
// verificação que a API oculta. O teste só compara o resultado; não o imprime.
func recursosPersistidos(t *testing.T, a *api) string {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
		t.Fatal(err)
	}
	var estado string
	err = tx.QueryRow(ctx, `SELECT jsonb_build_array(
		(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]') FROM targets t),
		(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]') FROM incidents t),
		(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]') FROM notif_channels t),
		(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]') FROM status_pages t),
		(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]') FROM status_page_items t)
	)::text`).Scan(&estado)
	if err != nil {
		t.Fatal(err)
	}
	return estado
}

func exigirRespostaDeSessao(t *testing.T, resp *http.Response, status int, codigo string) {
	t.Helper()
	defer resp.Body.Close()
	if got := codigoDoErro(t, resp); resp.StatusCode != status || got != codigo {
		t.Errorf("resposta %d/%s, queria %d/%s", resp.StatusCode, got, status, codigo)
	}
}

func exigirJWTNaoExpirado(t *testing.T, a *api, token string) {
	t.Helper()
	if _, err := auth.ParseAccessToken(testJWTSecret, token, a.clk.Now()); err != nil {
		t.Fatalf("o teste deixou o JWT expirar antes de testar a revogação: %v", err)
	}
}

// Tirar viva de protegida OU comEnvio precisa tornar este teste vermelho.
// Cada requisição começa depois do DELETE de sessão ter respondido 204; não
// há promessa de interromper uma mutação que já tenha passado pela guarda.
func TestEveryResourceRouteRejectsRevokedFamily(t *testing.T) {
	a := newAPI(t)
	token, _, familia := familiaComRefresh(t, a)
	dono := accessTokenValido(t, a)
	alvo, incidentes := incidentesDe(t, a, token, 1)
	var canal canalJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", token,
		map[string]string{"kind": "email", "email": "verificacao@exemplo.com"})
	lerCorpo(t, resp, &canal)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("preparar canal: %d", resp.StatusCode)
	}
	var pagina paginaJSON
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", token, map[string]any{
		"slug": "sessao-viva", "title": "Status", "published": false,
		"items": []map[string]string{{"target_id": alvo, "display_name": "API"}},
	})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("preparar página: %d", resp.StatusCode)
	}
	antes := recursosPersistidos(t, a)
	a.email.mu.Lock()
	enviosAntes := len(a.email.links)
	a.email.mu.Unlock()

	exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusOK, "")
	resp = chamar(t, a, http.MethodDelete, "/api/v1/sessions/"+familia.String(), dono, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revogar família pela API: %d", resp.StatusCode)
	}

	casos := []struct {
		padrao, caminho string
		corpo           any
	}{
		{"GET /api/v1/me", "/api/v1/me", nil},
		{"GET /api/v1/sessions", "/api/v1/sessions", nil},
		{"DELETE /api/v1/sessions/{id}", "/api/v1/sessions/" + familiaDe(t, dono, a).String(), nil},
		{"GET /api/v1/targets", "/api/v1/targets", nil},
		{"POST /api/v1/targets", "/api/v1/targets", alvoValido},
		{"GET /api/v1/targets/summary", "/api/v1/targets/summary", nil},
		{"GET /api/v1/targets/{id}", "/api/v1/targets/" + alvo, nil},
		{"PATCH /api/v1/targets/{id}", "/api/v1/targets/" + alvo, map[string]string{"name": "alterado sem sessão"}},
		{"DELETE /api/v1/targets/{id}", "/api/v1/targets/" + alvo, nil},
		{"GET /api/v1/targets/{id}/checks", "/api/v1/targets/" + alvo + "/checks", nil},
		{"GET /api/v1/incidents", "/api/v1/incidents", nil},
		{"POST /api/v1/incidents/{id}/ack", "/api/v1/incidents/" + incidentes[0] + "/ack", nil},
		{"GET /api/v1/channels", "/api/v1/channels", nil},
		{"POST /api/v1/channels", "/api/v1/channels", map[string]string{"kind": "email", "email": "outro@exemplo.com"}},
		{"PATCH /api/v1/channels/{id}", "/api/v1/channels/" + canal.ID, map[string]bool{"enabled": false}},
		{"POST /api/v1/channels/{id}/verify/resend", "/api/v1/channels/" + canal.ID + "/verify/resend", nil},
		{"DELETE /api/v1/channels/{id}", "/api/v1/channels/" + canal.ID, nil},
		{"GET /api/v1/status-pages", "/api/v1/status-pages", nil},
		{"POST /api/v1/status-pages", "/api/v1/status-pages", map[string]string{"slug": "nova-pagina", "title": "Nova"}},
		{"PATCH /api/v1/status-pages/{id}", "/api/v1/status-pages/" + pagina.ID, map[string]bool{"published": true}},
	}
	// A matriz não pode deixar uma rota de recurso nova sem a prova de
	// revogação. A enumeração existente também verifica registros novos.
	var cobertas, registradas []string
	for _, c := range casos {
		cobertas = append(cobertas, c.padrao)
	}
	for _, rota := range rotasRegistradas(t) {
		_, caminho, _ := strings.Cut(rota, " ")
		if strings.HasPrefix(caminho, "/api/v1/") && !strings.HasPrefix(caminho, "/api/v1/auth/") {
			registradas = append(registradas, rota)
		}
	}
	slices.Sort(cobertas)
	slices.Sort(registradas)
	if !slices.Equal(cobertas, registradas) {
		t.Fatalf("matriz de recursos %v diferente das rotas %v", cobertas, registradas)
	}
	for _, c := range casos {
		t.Run(c.padrao, func(t *testing.T) {
			// Avança só o relógio injetado: a guarda, e não um 429 ou a
			// expiração do JWT, deve produzir a recusa de cada rota.
			a.clk.Advance(time.Second)
			exigirJWTNaoExpirado(t, a, token)
			metodo, _, _ := strings.Cut(c.padrao, " ")
			exigirRespostaDeSessao(t, chamar(t, a, metodo, c.caminho, token, c.corpo), http.StatusUnauthorized, "unauthorized")
		})
	}
	if depois := recursosPersistidos(t, a); depois != antes {
		t.Error("requisição com família revogada modificou recursos persistidos")
	}
	a.email.mu.Lock()
	enviosDepois := len(a.email.links)
	a.email.mu.Unlock()
	if enviosDepois != enviosAntes {
		t.Error("requisição com família revogada enviou verificação externa")
	}
}

// Os emissores já revogam as famílias. A nova guarda deve observar essas
// revogações, em vez de aceitar o access token até o exp original.
func TestResourceAccessStopsAfterEachRevocationCause(t *testing.T) {
	for _, causa := range []string{"logout", "troca de senha", "set-password", "reset-2fa", "reúso de refresh", "quinta prova errada"} {
		t.Run(causa, func(t *testing.T) {
			a := newAPI(t)
			if causa == "reset-2fa" {
				cadastrarTOTP(t, a, clientWithJar(t))
			}
			token, refresh, _ := familiaComRefresh(t, a)
			var alvo alvoJSON
			resp := chamar(t, a, http.MethodPost, "/api/v1/targets", token, alvoValido)
			lerCorpo(t, resp, &alvo)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("preparar alvo: %d", resp.StatusCode)
			}
			antes := recursosPersistidos(t, a)
			switch causa {
			case "logout":
				req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/logout", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("X-Requested-With", "stillon")
				req.AddCookie(&http.Cookie{Name: "stillon_refresh", Value: refresh})
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				exigirRespostaDeSessao(t, resp, http.StatusNoContent, "")
			case "troca de senha":
				dono := accessTokenValido(t, a)
				exigirRespostaDeSessao(t, trocarSenha(t, a, dono, map[string]string{"current_password": testPass, "new_password": senhaNova}), http.StatusNoContent, "")
			case "set-password":
				if _, err := account.NewService(a.pool, nil, a.clk, nil).ResetPassword(context.Background(), testEmail, senhaNova); err != nil {
					t.Fatal(err)
				}
			case "reset-2fa":
				resetarPeloCLI(t, a)
			case "reúso de refresh":
				mgr := gerente(a)
				meta := auth.Meta{IP: "203.0.113.9"}
				if _, _, err := mgr.Rotate(context.Background(), refresh, meta); err != nil {
					t.Fatal(err)
				}
				if _, _, err := mgr.Rotate(context.Background(), refresh, meta); !errors.Is(err, auth.ErrTokenReuse) {
					t.Fatalf("reúso não revogou a família: %v", err)
				}
			case "quinta prova errada":
				for i := range 5 {
					codigo := "invalid_credentials"
					if i == 4 {
						codigo = "unauthorized"
					}
					exigirRespostaDeSessao(t, trocarSenha(t, a, token, map[string]string{"current_password": "senha-errada", "new_password": senhaNova}), http.StatusUnauthorized, codigo)
				}
			}
			exigirJWTNaoExpirado(t, a, token)
			exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusUnauthorized, "unauthorized")
			exigirRespostaDeSessao(t, chamar(t, a, http.MethodPatch, "/api/v1/targets/"+alvo.ID, token, map[string]string{"name": "alterado após revogação"}), http.StatusUnauthorized, "unauthorized")
			if depois := recursosPersistidos(t, a); depois != antes {
				t.Error("a revogação deixou o JWT modificar recursos")
			}
		})
	}
}

func TestResourceRoutesRejectAbsentExpiredOrAnotherUsersFamily(t *testing.T) {
	a := newAPI(t)
	_, _, vencida := familiaComRefresh(t, a)
	if _, err := a.pool.Exec(context.Background(), `UPDATE sessions SET expires_at=$2 WHERE family_id=$1`, vencida, a.clk.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	outro := outroUsuario(t, a)
	tokenAlheio := tokenDe(t, a, outro)
	for _, c := range []struct {
		nome    string
		familia uuid.UUID
	}{
		{"ausente", uuid.New()},
		{"vencida", vencida},
		{"de outro usuário", familiaDe(t, tokenAlheio, a)},
	} {
		t.Run(c.nome, func(t *testing.T) {
			// Assinatura, sub e exp são válidos; só a família é inválida.
			token, err := auth.NewAccessToken(testJWTSecret, a.userID, c.familia, a.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			exigirJWTNaoExpirado(t, a, token)
			exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusUnauthorized, "unauthorized")
		})
	}
}

// Rotação do refresh mantém a família: seu access token anterior ainda é
// legítimo. Rejeitar uma linha usada em vez da família quebraria o painel.
func TestResourceRoutesKeepRotatedFamilyAuthorized(t *testing.T) {
	a := newAPI(t)
	token, refresh, familia := familiaComRefresh(t, a)
	_, atual, err := gerente(a).Rotate(context.Background(), refresh, auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if atual.FamilyID != familia {
		t.Fatal("a preparação não rotacionou dentro da família original")
	}
	exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusOK, "")
	exigirRespostaDeSessao(t, chamar(t, a, http.MethodPost, "/api/v1/targets", token, alvoValido), http.StatusCreated, "")
}

// Só o pool usado pela consulta de sessão falha; os serviços de recurso
// continuam ligados a um banco real acessível. Um fallback permissivo daria
// 200/201 e modificaria recursos, em vez de 500 sem efeito.
func TestResourceRoutesFailClosedWhenSessionDatabaseFails(t *testing.T) {
	var sessoes *pgxpool.Pool
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		var err error
		sessoes, err = pgxpool.New(context.Background(), testdb.DSN(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sessoes.Close)
		d.Accounts = account.NewService(sessoes, d.TOTP, d.Clock, testIPHashKey)
	})
	token := tokenDe(t, a, a.userID)
	exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusOK, "")
	antes := recursosPersistidos(t, a)
	sessoes.Close()
	exigirRespostaDeSessao(t, chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil), http.StatusInternalServerError, "internal_error")
	exigirRespostaDeSessao(t, chamar(t, a, http.MethodPost, "/api/v1/channels", token,
		map[string]string{"kind": "email", "email": "nao-enviar@exemplo.com"}), http.StatusInternalServerError, "internal_error")
	if depois := recursosPersistidos(t, a); depois != antes {
		t.Error("falha da consulta de sessão deixou modificar recursos")
	}
	a.email.mu.Lock()
	envios := len(a.email.links)
	a.email.mu.Unlock()
	if envios != 0 {
		t.Error("falha da consulta de sessão deixou enviar verificação externa")
	}
}

func TestResourceSessionGuardKeepsExistingRateLimits(t *testing.T) {
	for _, modo := range []string{"usuário", "envio"} {
		t.Run(modo, func(t *testing.T) {
			a := newAPI(t)
			token := tokenDe(t, a, a.userID)
			limite, status := 20, http.StatusOK
			metodo, caminho := http.MethodGet, "/api/v1/targets"
			var corpo any
			if modo == "envio" {
				limite, status = 5, http.StatusBadRequest
				metodo, caminho = http.MethodPost, "/api/v1/channels"
				corpo = map[string]string{"kind": "inexistente"}
			}
			for range limite {
				resp := chamar(t, a, metodo, caminho, token, corpo)
				resp.Body.Close()
				if resp.StatusCode != status {
					t.Fatalf("antes do limite: %d, queria %d", resp.StatusCode, status)
				}
			}
			resp := chamar(t, a, metodo, caminho, token, corpo)
			resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("após o limite: %d, queria 429", resp.StatusCode)
			}
		})
	}
}
