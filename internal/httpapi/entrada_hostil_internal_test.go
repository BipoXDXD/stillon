package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/notify"
)

// Os handlers destes testes não têm banco: chegar a ele é panic. Assim, um 4xx
// aqui prova que a entrada hostil morreu na fronteira, e nunca virou 500.

type respostaHostil struct {
	status int
	codigo string
	campos []campoHostil
	chegou bool
}

type campoHostil struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func enviarHostil(t *testing.T, h http.HandlerFunc, ajustar func(*http.Request), corpo string) respostaHostil {
	t.Helper()
	agora := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	chave := []byte("segredo-publico-de-teste-32bytes!!")
	acesso, err := auth.NewAccessToken(chave, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(corpo))
	req.Header.Set("Authorization", "Bearer "+acesso)
	req.SetPathValue("id", uuid.NewString())
	req.SetPathValue("incident_id", uuid.NewString())
	ajustar(req)
	rec := httptest.NewRecorder()
	r := func() (r respostaHostil) {
		defer func() { r.chegou = recover() != nil }()
		middleware.Authn(chave, clock.NewFake(agora))(h).ServeHTTP(rec, req)
		return r
	}()
	if r.chegou {
		return r
	}
	var envelope struct {
		Error struct {
			Code   string        `json:"code"`
			Fields []campoHostil `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("corpo fora do envelope: %v: %s", err, rec.Body)
	}
	r.status, r.codigo, r.campos = rec.Code, envelope.Error.Code, envelope.Error.Fields
	return r
}

// Um id malformado é 404 em toda rota com {id} (D16), e não 400 nem 500: o
// recurso não existe de jeito nenhum.
func TestIDMalformadoE404EmTodaRotaComID(t *testing.T) {
	alvos, batidas, canais := &TargetHandlers{}, &HeartbeatHandlers{}, &ChannelHandlers{}
	paginas, incidentes := &StatusPageHandlers{}, &IncidentHandlers{}
	rotas := map[string]http.HandlerFunc{
		"GET /targets/{id}":                                 alvos.Get,
		"PATCH /targets/{id}":                               alvos.Update,
		"DELETE /targets/{id}":                              alvos.Delete,
		"GET /targets/{id}/checks":                          alvos.Checks,
		"POST /incidents/{id}/ack":                          incidentes.Ack,
		"GET /heartbeats/{id}":                              batidas.Get,
		"PATCH /heartbeats/{id}":                            batidas.Update,
		"DELETE /heartbeats/{id}":                           batidas.Delete,
		"POST /heartbeats/{id}/token/rotate":                batidas.RotateToken,
		"POST /heartbeats/{id}/clone":                       batidas.Clone,
		"POST /heartbeats/{id}/ping-now":                    batidas.PingNow,
		"GET /heartbeats/{id}/events":                       batidas.Events,
		"POST /heartbeats/{id}/events/clear":                batidas.ClearEvents,
		"GET /heartbeats/{id}/incidents":                    batidas.Incidents,
		"POST /heartbeats/{id}/incidents/{incident_id}/ack": batidas.AckIncident,
		"DELETE /channels/{id}":                             canais.Delete,
		"PATCH /channels/{id}":                              canais.SetEnabled,
		"POST /channels/{id}/verify/resend":                 canais.Resend,
		"PATCH /status-pages/{id}":                          paginas.Update,
	}
	// "\x00" é o %00 depois de o roteador decodificar o caminho.
	ids := []string{"nao-e-uuid", "", "\x00", "a\x00b", "' OR 1=1--", strings.Repeat("a", 4096)}
	for rota, h := range rotas {
		t.Run(rota, func(t *testing.T) {
			for _, id := range ids {
				r := enviarHostil(t, h, func(req *http.Request) { req.SetPathValue("id", id) }, `{}`)
				if r.chegou || r.status != http.StatusNotFound || r.codigo != "not_found" {
					t.Errorf("id %.20q: %+v, queria 404 not_found sem chegar ao serviço", id, r)
				}
			}
		})
	}
}

func TestIncidenteMalformadoNoAckE404(t *testing.T) {
	h := (&HeartbeatHandlers{}).AckIncident
	for _, id := range []string{"nao-e-uuid", "a\x00b", "' OR 1=1--"} {
		r := enviarHostil(t, h, func(req *http.Request) { req.SetPathValue("incident_id", id) }, `{}`)
		if r.chegou || r.status != http.StatusNotFound || r.codigo != "not_found" {
			t.Errorf("incident_id %q: %+v, queria 404 not_found sem chegar ao serviço", id, r)
		}
	}
}

// Parâmetro de consulta fora do formato é 400 com o campo certo, nos valores
// de borda e nos hostis, em toda listagem. O nome do campo é vocabulário
// fechado: nada do valor recebido volta.
func TestConsultaHostilE400NasListagens(t *testing.T) {
	alvos, batidas, incidentes := &TargetHandlers{clk: clock.NewFake(time.Now())}, &HeartbeatHandlers{}, &IncidentHandlers{}
	paginadas := map[string]http.HandlerFunc{
		"targets": alvos.List, "incidents": incidentes.List, "heartbeats": batidas.List,
		"heartbeats events": batidas.Events, "heartbeats incidents": batidas.Incidents,
	}
	const (
		foraDaFaixa = "out_of_range"
		invalido    = "invalid"
	)
	casos := []struct{ consulta, campo, motivo string }{
		{"limit=0", "limit", foraDaFaixa},
		{"limit=-1", "limit", foraDaFaixa},
		{"limit=101", "limit", foraDaFaixa},
		{"limit=99999999999", "limit", invalido},
		{"limit=1%00", "limit", invalido},
		{"limit=1%27%20OR%201%3D1--", "limit", invalido},
		{"cursor=%00", "cursor", invalido},
		{"cursor=%27%20OR%201%3D1--", "cursor", invalido},
	}
	for nome, h := range paginadas {
		for _, c := range casos {
			conferirConsultaHostil(t, nome, h, c.consulta, c.campo, c.motivo)
		}
	}

	especificos := []struct {
		nome                    string
		h                       http.HandlerFunc
		consulta, campo, motivo string
	}{
		{"incidents", incidentes.List, "status=%00", "status", invalido},
		{"incidents", incidentes.List, "status=OPEN", "status", invalido},
		{"incidents", incidentes.List, "target_id=%00", "target_id", invalido},
		{"heartbeats incidents", batidas.Incidents, "status=%00", "status", invalido},
		{"checks", alvos.Checks, "from=%00", "from", invalido},
		{"checks", alvos.Checks, "to=%00", "to", invalido},
		{"checks", alvos.Checks, "window=%00", "window", invalido},
		{"checks", alvos.Checks, "window=", "window", invalido},
		{"checks", alvos.Checks, "window=1h&from=2026-01-01T00:00:00Z", "window", invalido},
		{"checks", alvos.Checks, "from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z", "from", invalido},
		{"checks", alvos.Checks, "from=0001-01-01T00:00:00Z&to=9999-12-31T23:59:59Z", "from", foraDaFaixa},
		{"cron-preview", batidas.CronPreview, "expr=*%20*%20*%20*%20*&timezone=UTC%00", "timezone", invalido},
		{"cron-preview", batidas.CronPreview, "expr=*%20*%20*%20*%20*&timezone=" + strings.Repeat("a", 65), "timezone", invalido},
	}
	for _, c := range especificos {
		conferirConsultaHostil(t, c.nome, c.h, c.consulta, c.campo, c.motivo)
	}
}

func conferirConsultaHostil(t *testing.T, nome string, h http.HandlerFunc, consulta, campo, motivo string) {
	t.Helper()
	r := enviarHostil(t, h, func(req *http.Request) { req.URL.RawQuery = consulta }, ``)
	if r.chegou || r.status != http.StatusBadRequest || r.codigo != "validation_failed" {
		t.Errorf("%s ?%.50s: %+v, queria 400 sem chegar ao serviço", nome, consulta, r)
		return
	}
	if len(r.campos) != 1 || r.campos[0] != (campoHostil{Field: campo, Reason: motivo}) {
		t.Errorf("%s ?%.50s: campos %+v, queria %s/%s", nome, consulta, r.campos, campo, motivo)
	}
}

type carteiroQueNaoEnvia struct{}

func (carteiroQueNaoEnvia) SendVerification(context.Context, crypto.Secret, string) error {
	return nil
}

// O texto recebido com NUL ou acima do teto é 400 na fronteira. O Postgres
// recusa NUL em text: uma recusa que faltasse aqui seria 500.
func TestCorpoHostilE400NaFronteira(t *testing.T) {
	canais := NewChannelHandlers(notify.NewChannelService(nil, nil, nil, nil, carteiroQueNaoEnvia{}, ""))
	paginas, batidas, alvos := &StatusPageHandlers{}, &HeartbeatHandlers{}, &TargetHandlers{}
	webhook := "https://discord.com/api/webhooks/1/"
	cron := `{"name":"x","schedule":{"kind":"cron","expr":"* * * * *","timezone":`
	item := func(nome string) string {
		return `{"slug":"empresa","title":"T","items":[{"target_id":"` + uuid.NewString() + `","display_name":"` + nome + `"}]}`
	}
	var vinteEUm []string
	for range 21 {
		vinteEUm = append(vinteEUm, `{"target_id":"`+uuid.NewString()+`","display_name":"a"}`)
	}
	casos := []struct {
		nome          string
		h             http.HandlerFunc
		corpo         string
		campo, motivo string
	}{
		{"canal: tipo com NUL", canais.Create, `{"kind":"a\u0000"}`, "kind", "invalid"},
		{"canal: webhook com NUL", canais.Create, `{"kind":"discord","url":"` + webhook + `a\u0000"}`, "url", "invalid"},
		{"canal: webhook longo", canais.Create, `{"kind":"discord","url":"` + webhook + strings.Repeat("a", 600) + `"}`, "url", "invalid"},
		{"canal: e-mail com NUL", canais.Create, `{"kind":"email","email":"a\u0000@exemplo.com"}`, "email", "invalid"},
		{"canal: e-mail longo", canais.Create, `{"kind":"email","email":"` + strings.Repeat("a", 250) + `@exemplo.com"}`, "email", "invalid"},
		{"página: slug com NUL", paginas.Create, `{"slug":"em\u0000presa","title":"T"}`, "slug", "invalid"},
		{"página: título com NUL", paginas.Create, `{"slug":"empresa","title":"a\u0000b"}`, "title", "invalid"},
		{"página: título longo", paginas.Create, `{"slug":"empresa","title":"` + strings.Repeat("a", 101) + `"}`, "title", "too_long"},
		{"página: nome com NUL", paginas.Create, item(`a\u0000b`), "items[0].display_name", "invalid"},
		{"página: itens demais", paginas.Create, `{"slug":"empresa","title":"T","items":[` + strings.Join(vinteEUm, ",") + `]}`, "items", "out_of_range"},
		{"heartbeat: fuso com NUL", batidas.Create, cron + `"UTC\u0000"}}`, "timezone", "invalid"},
		{"heartbeat: fuso longo", batidas.Create, cron + `"` + strings.Repeat("a", 65) + `"}}`, "timezone", "invalid"},
		{"alvo: URL com NUL", alvos.Create, `{"name":"a","url":"https://exemplo.com/\u0000"}`, "url", "invalid"},
		{"alvo: método com NUL", alvos.Create, `{"name":"a","url":"https://exemplo.com/","method":"G\u0000"}`, "method", "invalid"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := enviarHostil(t, c.h, func(*http.Request) {}, c.corpo)
			if r.chegou || r.status != http.StatusBadRequest || r.codigo != "validation_failed" {
				t.Fatalf("%+v, queria 400 sem chegar ao serviço", r)
			}
			if len(r.campos) != 1 || r.campos[0] != (campoHostil{Field: c.campo, Reason: c.motivo}) {
				t.Errorf("campos %+v, queria %s/%s", r.campos, c.campo, c.motivo)
			}
		})
	}
}
