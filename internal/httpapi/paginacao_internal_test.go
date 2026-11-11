package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// As cinco listagens paginadas leem limit e cursor do mesmo jeito: padrão 50,
// teto 100, cursor UUID, e 400 (nunca o padrão em silêncio) para o que sair
// disso. Os handlers não têm serviço: pedido aceito entra em panic, e
// enviarHostil o conta como chegou == true.
func TestPaginacaoDasListagens(t *testing.T) {
	alvos, batidas, incidentes := &TargetHandlers{clk: clock.NewFake(time.Now())}, &HeartbeatHandlers{}, &IncidentHandlers{}
	rotas := map[string]http.HandlerFunc{
		"targets": alvos.List, "incidents": incidentes.List, "heartbeats": batidas.List,
		"heartbeats events": batidas.Events, "heartbeats incidents": batidas.Incidents,
	}
	cursor := uuid.NewString()
	const (
		foraDaFaixa = "out_of_range"
		invalido    = "invalid"
	)

	aceitas := []string{
		"", "limit=", "limit=1", "limit=50", "limit=100", "limit=%2B5",
		"cursor=", "cursor=" + cursor, "limit=100&cursor=" + cursor,
	}
	recusadas := []struct{ consulta, campo, motivo string }{
		{"limit=0", "limit", foraDaFaixa},
		{"limit=-1", "limit", foraDaFaixa},
		{"limit=101", "limit", foraDaFaixa},
		{"limit=abc", "limit", invalido},
		{"limit=99999999999", "limit", invalido},
		{"limit=1.5", "limit", invalido},
		{"limit=%201", "limit", invalido},
		{"limit=0x10", "limit", invalido},
		{"cursor=nao-e-uuid", "cursor", invalido},
		{"cursor=00000000-0000-0000-0000-00000000000", "cursor", invalido},
		// Dois erros: o primeiro a ser lido, limit, é o único relatado.
		{"limit=0&cursor=x", "limit", foraDaFaixa},
	}

	for nome, h := range rotas {
		for _, consulta := range aceitas {
			t.Run(nome+"?"+consulta, func(t *testing.T) {
				r := enviarHostil(t, h, func(req *http.Request) { req.URL.RawQuery = consulta }, ``)
				if !r.chegou {
					t.Errorf("a consulta foi recusada (%+v), queria que chegasse ao serviço", r)
				}
			})
		}
		for _, c := range recusadas {
			t.Run(nome+"?"+c.consulta, func(t *testing.T) {
				r := enviarHostil(t, h, func(req *http.Request) { req.URL.RawQuery = c.consulta }, ``)
				quer := []campoHostil{{Field: c.campo, Reason: c.motivo}}
				if r.chegou || r.status != http.StatusBadRequest || r.codigo != "validation_failed" || !slices.Equal(r.campos, quer) {
					t.Errorf("deu %+v, queria 400 validation_failed %+v sem chegar ao serviço", r, quer)
				}
			})
		}
	}
}

// paginaDe devolve o limite e o cursor lidos, e em falha já escreveu o 400.
// Padrão e teto vêm de quem chama: cada pacote tem os seus.
func TestPaginaDe(t *testing.T) {
	cursor := uuid.New()
	casos := []struct {
		nome           string
		consulta       string
		padrao, maximo int32
		limite         int32
		cursor         *uuid.UUID
		ok             bool
		campo, motivo  string
	}{
		{"sem parametros usa o padrao", "", 50, 100, 50, nil, true, "", ""},
		{"limit vazio usa o padrao", "limit=", 50, 100, 50, nil, true, "", ""},
		{"limit no meio da faixa", "limit=7", 50, 100, 7, nil, true, "", ""},
		{"limit no teto", "limit=100", 50, 100, 100, nil, true, "", ""},
		{"limit acima do teto", "limit=101", 50, 100, 0, nil, false, "limit", "out_of_range"},
		{"limit zero", "limit=0", 50, 100, 0, nil, false, "limit", "out_of_range"},
		{"limit negativo", "limit=-1", 50, 100, 0, nil, false, "limit", "out_of_range"},
		{"limit que nao e numero", "limit=x", 50, 100, 0, nil, false, "limit", "invalid"},
		{"limit acima de int32", "limit=99999999999", 50, 100, 0, nil, false, "limit", "invalid"},
		{"cursor valido", "cursor=" + cursor.String(), 50, 100, 50, &cursor, true, "", ""},
		{"cursor invalido", "cursor=x", 50, 100, 0, nil, false, "cursor", "invalid"},
		{"padrao e teto de quem chama (padrao)", "", 10, 20, 10, nil, true, "", ""},
		{"padrao e teto de quem chama (no teto)", "limit=20", 10, 20, 20, nil, true, "", ""},
		{"padrao e teto de quem chama (acima)", "limit=21", 10, 20, 0, nil, false, "limit", "out_of_range"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			q, err := url.ParseQuery(c.consulta)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			limite, cur, ok := paginaDe(rec, req, q, c.padrao, c.maximo)

			if ok != c.ok || limite != c.limite {
				t.Fatalf("deu limite %d ok=%v, queria %d ok=%v", limite, ok, c.limite, c.ok)
			}
			if (cur == nil) != (c.cursor == nil) || (cur != nil && *cur != *c.cursor) {
				t.Errorf("cursor %v, queria %v", cur, c.cursor)
			}
			if c.ok {
				if rec.Body.Len() != 0 {
					t.Errorf("escreveu %q num pedido aceito", rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, queria 400", rec.Code)
			}
			if corpo := rec.Body.String(); !contemCampo(corpo, c.campo, c.motivo) {
				t.Errorf("corpo %s, queria o campo %s/%s", corpo, c.campo, c.motivo)
			}
		})
	}
}

func contemCampo(corpo, campo, motivo string) bool {
	return strings.Contains(corpo, `"field":"`+campo+`"`) && strings.Contains(corpo, `"reason":"`+motivo+`"`)
}
