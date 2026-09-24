package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

type incidenteJSON struct {
	ID             string     `json:"id"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	Cause          string     `json:"cause"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

// envelopeComCampos é o envelope com error.fields. Tipo à parte porque dois
// testes antigos comparam envelopes com !=, e um slice no struct o impediria.
type envelopeComCampos struct {
	Error struct {
		Code   string              `json:"code"`
		Fields []apierr.FieldError `json:"fields"`
	} `json:"error"`
}

type listaIncidentesJSON struct {
	Items      []incidenteJSON `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

// incidentesDe cria um alvo do usuário pela API e abre incidentes nele pelo
// escopo do sondador, que é quem os abre de verdade. Todos, menos o último,
// saem resolvidos (só pode haver um aberto por alvo).
func incidentesDe(t *testing.T, a *api, token string, n int) (alvo string, ids []string) {
	t.Helper()
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", token, alvoValido), &criado)
	ctx := context.Background()
	for i := range n {
		id := uuid.Must(uuid.NewV7())
		err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
			if err := q.OpenIncident(ctx, db.OpenIncidentParams{
				ID: id, TargetID: uuid.MustParse(criado.ID), StartedAt: a.clk.Now(), Cause: "timeout",
			}); err != nil {
				return err
			}
			if i < n-1 {
				_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: id, ResolvedAt: a.clk.Now().Add(time.Minute)})
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
	}
	return criado.ID, ids
}

func TestIncidentListFiltersAndAck(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	alvo, ids := incidentesDe(t, a, tok, 2)
	resolvido, aberto := ids[0], ids[1]

	var todos listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents", tok, nil), &todos)
	if len(todos.Items) != 2 || todos.Items[0].ID != aberto || todos.Items[0].TargetID != alvo ||
		todos.Items[0].TargetName != "API" || todos.Items[0].Cause != "timeout" || todos.Items[1].ResolvedAt == nil {
		t.Fatalf("lista = %+v", todos.Items)
	}
	for status, quer := range map[string]string{"open": aberto, "resolved": resolvido} {
		var l listaIncidentesJSON
		lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?status="+status, tok, nil), &l)
		if len(l.Items) != 1 || l.Items[0].ID != quer {
			t.Errorf("status=%s: %+v", status, l.Items)
		}
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/incidents/"+aberto+"/ack", tok, nil)
	var ack incidenteJSON
	lerCorpo(t, resp, &ack)
	if resp.StatusCode != http.StatusOK || ack.AcknowledgedAt == nil || !ack.AcknowledgedAt.Equal(a.clk.Now()) {
		t.Fatalf("ack deu %d: %+v", resp.StatusCode, ack)
	}
	primeiro := *ack.AcknowledgedAt
	a.clk.Advance(time.Hour)
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/incidents/"+aberto+"/ack", tok, nil), &ack)
	if !ack.AcknowledgedAt.Equal(primeiro) {
		t.Errorf("segundo ack mudou acknowledged_at para %v", ack.AcknowledgedAt)
	}
}

func TestIncidentListRejectsBadQueries(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for consulta, quer := range map[string]apierr.FieldError{
		"status=aberto":     {Field: "status", Reason: "invalid"},
		"limit=0":           {Field: "limit", Reason: "out_of_range"},
		"limit=101":         {Field: "limit", Reason: "out_of_range"},
		"limit=x":           {Field: "limit", Reason: "invalid"},
		"cursor=nao-e-uuid": {Field: "cursor", Reason: "invalid"},
		"target_id=x":       {Field: "target_id", Reason: "invalid"},
	} {
		var env envelopeComCampos
		resp := chamar(t, a, http.MethodGet, "/api/v1/incidents?"+consulta, tok, nil)
		lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0] != quer {
			t.Errorf("%s deu %d/%+v, queria 400 %+v", consulta, resp.StatusCode, env.Error.Fields, quer)
		}
	}
}

// ?target_id= é aditivo (Plano 5): combina com status, e o alvo de outro
// usuário dá 200 com a lista vazia, não 404.
func TestIncidentListFiltersByTarget(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	alvo, ids := incidentesDe(t, a, tok, 2)
	// Outro alvo do mesmo dono, também com um incidente resolvido: sem o
	// filtro, a consulta abaixo traria dois.
	incidentesDe(t, a, tok, 2)

	var l listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?target_id="+alvo+"&status=resolved", tok, nil), &l)
	if len(l.Items) != 1 || l.Items[0].ID != ids[0] {
		t.Errorf("target_id + status: %+v, queria só %s", l.Items, ids[0])
	}

	intruso := outroUsuario(t, a)
	alheio, _ := incidentesDe(t, a, tokenDe(t, a, intruso), 1)
	resp := chamar(t, a, http.MethodGet, "/api/v1/incidents?target_id="+alheio, tok, nil)
	var vazia listaIncidentesJSON
	lerCorpo(t, resp, &vazia)
	if resp.StatusCode != http.StatusOK || len(vazia.Items) != 0 {
		t.Errorf("alvo alheio deu %d com %d itens, queria 200 e lista vazia", resp.StatusCode, len(vazia.Items))
	}
}

func TestIncidentListPaginates(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	_, ids := incidentesDe(t, a, tok, 3)

	var p1 listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?limit=2", tok, nil), &p1)
	if len(p1.Items) != 2 || p1.NextCursor != ids[1] {
		t.Fatalf("primeira página: %d itens, cursor %q", len(p1.Items), p1.NextCursor)
	}
	var p2 listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents?limit=2&cursor="+p1.NextCursor, tok, nil), &p2)
	if len(p2.Items) != 1 || p2.Items[0].ID != ids[0] || p2.NextCursor != "" {
		t.Errorf("segunda página: %+v, cursor %q", p2.Items, p2.NextCursor)
	}
}

func TestAnotherUsersIncidentIs404(t *testing.T) {
	a := newAPI(t)
	_, ids := incidentesDe(t, a, tokenDe(t, a, a.userID), 1)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	resp := chamar(t, a, http.MethodPost, "/api/v1/incidents/"+ids[0]+"/ack", intruso, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ack pelo intruso deu %d, queria 404", resp.StatusCode)
	}
	var l listaIncidentesJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/incidents", intruso, nil), &l)
	if len(l.Items) != 0 {
		t.Errorf("o intruso listou %d incidentes", len(l.Items))
	}
	resp = chamar(t, a, http.MethodPost, "/api/v1/incidents/nao-e-uuid/ack", tokenDe(t, a, a.userID), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("id malformado deu %d, queria 404 (D16 do Plano 2)", resp.StatusCode)
	}
}

func TestIncidentRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/incidents"},
		{http.MethodPost, "/api/v1/incidents/" + uuid.NewString() + "/ack"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// Cada caso sobe a própria API num subteste: o cleanup do subteste restaura
// o banco, e o usuário fixo de newAPI pode ser criado de novo.
func TestReadyzChecksTheDatabase(t *testing.T) {
	t.Run("banco no ar", func(t *testing.T) {
		readyzDeve(t, newAPI(t), http.StatusOK)
	})
	t.Run("banco fora", func(t *testing.T) {
		readyzDeve(t, newAPICom(t, nil, func(d *httpapi.Deps) {
			d.Ready = func(context.Context) error { return errors.New("conexão recusada") }
		}), http.StatusServiceUnavailable)
	})
	t.Run("wiring esquecido", func(t *testing.T) {
		readyzDeve(t, newAPICom(t, nil, func(d *httpapi.Deps) { d.Ready = nil }), http.StatusServiceUnavailable)
	})
}

func readyzDeve(t *testing.T, a *api, status int) {
	t.Helper()
	resp, err := http.Get(a.srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("/readyz deu %d, queria %d", resp.StatusCode, status)
	}
	if status != http.StatusOK {
		if env := decodeEnvelope(t, resp); env.Error.Code != "unavailable" {
			t.Errorf("código = %q, queria unavailable", env.Error.Code)
		}
	}
}
