package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

const parametroCanario = "canario-da-consulta"

// consultasRecusadasEmToda vale nas cinco listagens: desconhecido sozinho e ao
// lado de um válido, nome com outra caixa, nome vazio e os três formatos que
// url.ParseQuery recusa e r.URL.Query() descartaria em silêncio.
var consultasRecusadasEmToda = []string{
	parametroCanario + "=1",
	"limit=1&" + parametroCanario + "=1",
	"LIMIT=1",
	"=1",
	"limit=%zz",
	"%zz=1",
	"limit=1;cursor=x",
}

// Os handlers não têm serviço: pedido que passe da validação entra em panic,
// e o teste o conta como consulta aceita, sem precisar de banco.
func TestListingsRejectQueryOutsideTheAllowlist(t *testing.T) {
	alvos, incidentes, heartbeats := &TargetHandlers{}, &IncidentHandlers{}, &HeartbeatHandlers{}
	for _, rota := range []struct {
		nome    string
		handler http.HandlerFunc
		// deOutraRota é aceito em outra listagem: a allowlist varia por rota.
		deOutraRota string
	}{
		{"GET /targets", alvos.List, "status=open"},
		{"GET /incidents", incidentes.List, "tag=web"},
		{"GET /heartbeats", heartbeats.List, "status=open"},
		{"GET /heartbeats/{id}/events", heartbeats.Events, "status=open"},
		{"GET /heartbeats/{id}/incidents", heartbeats.Incidents, "target_id=1"},
	} {
		for _, consulta := range append(slices.Clone(consultasRecusadasEmToda), rota.deOutraRota) {
			t.Run(rota.nome+"?"+consulta, func(t *testing.T) {
				resposta, chegouAoServico := listarSemServico(t, rota.handler, consulta)
				if chegouAoServico {
					t.Fatal("a consulta chegou ao serviço, queria 400 antes dele")
				}
				verificarRecusaDaConsulta(t, resposta)
			})
		}
	}
}

func listarSemServico(t *testing.T, handler http.HandlerFunc, consulta string) (resposta *httptest.ResponseRecorder, chegouAoServico bool) {
	t.Helper()
	agora := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	chave := []byte("segredo-publico-de-teste-32bytes!!")
	token, err := auth.NewAccessToken(chave, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	pedido := httptest.NewRequest(http.MethodGet, "/listagem?"+consulta, nil)
	pedido.Header.Set("Authorization", "Bearer "+token)
	pedido.SetPathValue("id", uuid.NewString())
	resposta = httptest.NewRecorder()
	defer func() { chegouAoServico = recover() != nil }()
	middleware.Authn(chave, clock.NewFake(agora))(handler).ServeHTTP(resposta, pedido)
	return resposta, false
}

func verificarRecusaDaConsulta(t *testing.T, resposta *httptest.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		Error struct {
			Fields []apierr.FieldError `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resposta.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("corpo inválido %q: %v", resposta.Body.String(), err)
	}
	quer := []apierr.FieldError{{Field: "query", Reason: "invalid"}}
	if resposta.Code != http.StatusBadRequest || !slices.Equal(envelope.Error.Fields, quer) {
		t.Errorf("deu %d/%+v, queria 400 %+v", resposta.Code, envelope.Error.Fields, quer)
	}
	if strings.Contains(resposta.Body.String(), parametroCanario) {
		t.Errorf("o corpo ecoou o nome do parâmetro: %s", resposta.Body.String())
	}
}
