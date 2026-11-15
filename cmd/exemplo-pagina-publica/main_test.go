package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/statuspage"
)

func TestGeneratorWritesThePublicPageWithTheExampleServices(t *testing.T) {
	destino := filepath.Join(t.TempDir(), "status-publica.html")
	if err := run([]string{destino}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, trecho := range []string{"<h1", "Status da Loja Exemplo", "API de pagamentos", "Webhook de pedidos", "Banco de relatórios"} {
		if !strings.Contains(html, trecho) {
			t.Errorf("a página gerada não tem %q", trecho)
		}
	}
}

func TestGeneratorRequiresExactlyOneDestination(t *testing.T) {
	for _, args := range [][]string{nil, {"a.html", "b.html"}, {""}} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) aceitou; devia pedir um destino", args)
		}
	}
}

func TestGeneratorFailsWhenTheDestinationCannotBeWritten(t *testing.T) {
	destino := filepath.Join(t.TempDir(), "nao-existe", "status-publica.html")
	if err := run([]string{destino}); err == nil {
		t.Fatal("gravou numa pasta que não existe")
	}
}

// O exemplo das capturas não se contradiz: cada incidente listado derruba o
// dia dele na barra, na medida da duração.
func TestExampleIncidentsMatchTheBars(t *testing.T) {
	const tolerancia = 0.001
	v := exemploDaPaginaPublica()
	if len(v.Incidents) == 0 {
		t.Fatal("o exemplo não tem incidentes")
	}
	for _, inc := range v.Incidents {
		i := slices.IndexFunc(v.Items, func(it statuspage.PublicItem) bool { return it.Name == inc.Name })
		if i < 0 {
			t.Fatalf("incidente de %q, que não está na página", inc.Name)
		}
		d := slices.IndexFunc(v.Items[i].Days[:], func(d statuspage.Day) bool {
			return d.Date.Truncate(24 * time.Hour).Equal(inc.StartedAt.Truncate(24 * time.Hour))
		})
		if d < 0 {
			t.Fatalf("%s: dia %s fora da barra", inc.Name, inc.StartedAt)
		}
		quer := 1 - inc.Duration().Hours()/24
		if u := v.Items[i].Days[d].Uptime; u == nil || *u < quer-tolerancia || *u > quer+tolerancia {
			t.Errorf("%s, dia %d: uptime %v, e %s fora pede %.4f", inc.Name, d, u, inc.Duration(), quer)
		}
	}
}
