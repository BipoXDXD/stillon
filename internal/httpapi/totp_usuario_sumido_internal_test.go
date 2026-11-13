package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// contaSumida é a conta apagada depois da guarda de sessão viva e da reserva
// da prova: o token e a família ainda valem, mas ByID já não acha o usuário.
type contaSumida struct{ *contasDoDuble }

func (contaSumida) ByID(context.Context, uuid.UUID) (auth.User, error) {
	return auth.User{}, auth.ErrNotFound
}

// O usuário que some no meio de uma rota do 2FA é 401 unauthorized, como no
// /me e na troca de senha: o painel volta ao login. Nunca 500, nem detalhe
// interno no corpo, nem prova errada contada na família.
//
// Ficam de fora Confirm e RotateConfirm: não leem o usuário por ByID, e o
// serviço do TOTP é concreto, sem como devolver ErrNotFound num teste.
func TestTOTPRoutesAnswer401WhenTheUserVanishes(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)
	c.comContadorNoBanco(t)
	c.h.users = contaSumida{c.contas}

	rotas := []struct {
		nome    string
		caminho string
		handler http.HandlerFunc
		corpo   string
	}{
		{"enroll", "/api/v1/auth/totp/enroll", c.h.TOTPEnroll, fmt.Sprintf(`{"password":%q}`, senhaDoDono)},
		{"disable", "/api/v1/auth/totp/disable", c.h.TOTPDisable, fmt.Sprintf(`{"password":%q,"code":"123456"}`, senhaDoDono)},
		{"rotate", "/api/v1/auth/totp/rotate", c.h.TOTPRotate, fmt.Sprintf(`{"password":%q,"code":"123456"}`, senhaDoDono)},
	}
	for _, rota := range rotas {
		t.Run(rota.nome, func(t *testing.T) {
			acesso, err := auth.NewAccessToken(c.h.jwtSecret, c.usuario, c.familia, c.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, rota.caminho, strings.NewReader(rota.corpo))
			r.Header.Set("Authorization", "Bearer "+acesso)
			w := httptest.NewRecorder()
			middleware.Authn(c.h.jwtSecret, c.clk)(rota.handler).ServeHTTP(w, r)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, queria 401: %s", w.Code, w.Body.String())
			}
			if codigo := codigoDoEnvelope(t, w.Body.Bytes()); codigo != "unauthorized" {
				t.Errorf("code = %q, queria unauthorized", codigo)
			}
			if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 0 {
				t.Errorf("a recusa mexeu nos cookies: %v", cookies)
			}
			for _, proibido := range []string{
				"buscar usuário", "not found", "no rows", "dono@exemplo.com", c.usuario.String(),
				"otpauth", "rotation", "recovery_codes",
			} {
				if strings.Contains(w.Body.String(), proibido) {
					t.Errorf("o corpo trouxe %q: %s", proibido, w.Body.String())
				}
			}
		})
	}

	var erros int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT coalesce(sum(errors), 0) FROM session_family_proofs WHERE family_id = $1`, c.familia).Scan(&erros); err != nil {
		t.Fatal(err)
	}
	if erros != 0 {
		t.Errorf("%d provas erradas contadas para o usuário sumido, queria 0", erros)
	}
}
