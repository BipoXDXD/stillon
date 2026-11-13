package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// rotaComDonoEId é um handler que começa com "usuário do token, depois {id}
// da rota". Os handlers de heartbeat entram como controle: já usavam donoEID
// antes da mudança de lugar.
type rotaComDonoEId struct {
	nome    string
	handler http.HandlerFunc
}

func rotasComDonoEId() []rotaComDonoEId {
	alvos, incidentes, canais := &TargetHandlers{}, &IncidentHandlers{}, &ChannelHandlers{}
	paginas, batidas := &StatusPageHandlers{}, &HeartbeatHandlers{}
	return []rotaComDonoEId{
		{"GET /targets/{id}", alvos.Get},
		{"PATCH /targets/{id}", alvos.Update},
		{"DELETE /targets/{id}", alvos.Delete},
		{"GET /targets/{id}/checks", alvos.Checks},
		{"POST /incidents/{id}/ack", incidentes.Ack},
		{"DELETE /channels/{id}", canais.Delete},
		{"PATCH /channels/{id}/enabled", canais.SetEnabled},
		{"POST /channels/{id}/resend", canais.Resend},
		{"PATCH /status-pages/{id}", paginas.Update},
		{"GET /heartbeats/{id}", batidas.Get},
		{"PATCH /heartbeats/{id}", batidas.Update},
	}
}

type erroDoEnvelope struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func lerCodigo(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e erroDoEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("corpo fora do envelope: %v: %s", err, rec.Body)
	}
	return e.Error.Code
}

// Sem usuário no contexto, a resposta é 401 mesmo com id malformado: a ordem
// (primeiro quem é, depois o que pediu) é parte do contrato.
func TestRotasComIdSemUsuarioDao401(t *testing.T) {
	for _, rota := range rotasComDonoEId() {
		for _, id := range []string{uuid.NewString(), "nao-e-uuid"} {
			t.Run(rota.nome+" id="+id, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
				req.SetPathValue("id", id)
				rec := httptest.NewRecorder()

				rota.handler(rec, req)

				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, queria 401: %s", rec.Code, rec.Body)
				}
				if got := lerCodigo(t, rec); got != string(apierr.CodeUnauthorized) {
					t.Errorf("code = %q, queria %q", got, apierr.CodeUnauthorized)
				}
			})
		}
	}
}

func comUsuario(t *testing.T, h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	agora := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	segredo := []byte("segredo-de-teste-com-32-bytes!!!")
	acesso, err := auth.NewAccessToken(segredo, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+acesso)
	rec := httptest.NewRecorder()
	middleware.Authn(segredo, clock.NewFake(agora))(h).ServeHTTP(rec, req)
	return rec
}

// Com usuário e id malformado, 404 not_found: o recurso não existe de jeito
// nenhum, e o corpo não devolve o id recebido. Serviço nil prova que o
// handler respondeu antes de tocar nele.
func TestRotasComIdMalformadoDao404(t *testing.T) {
	const malformado = "nao-e-uuid"
	for _, rota := range rotasComDonoEId() {
		t.Run(rota.nome, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.SetPathValue("id", malformado)

			rec := comUsuario(t, rota.handler, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, queria 404: %s", rec.Code, rec.Body)
			}
			if got := lerCodigo(t, rec); got != string(apierr.CodeNotFound) {
				t.Errorf("code = %q, queria %q", got, apierr.CodeNotFound)
			}
			if strings.Contains(rec.Body.String(), malformado) {
				t.Errorf("o corpo repete o id recebido: %s", rec.Body)
			}
		})
	}
}

// AckIncident tem um segundo id na rota: heartbeat válido com incident_id
// malformado continua 404.
func TestAckDeHeartbeatComIncidenteMalformadoDa404(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.SetPathValue("id", uuid.NewString())
	req.SetPathValue("incident_id", "x")

	rec := comUsuario(t, (&HeartbeatHandlers{}).AckIncident, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, queria 404: %s", rec.Code, rec.Body)
	}
	if got := lerCodigo(t, rec); got != string(apierr.CodeNotFound) {
		t.Errorf("code = %q, queria %q", got, apierr.CodeNotFound)
	}
}
