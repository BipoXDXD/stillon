package statuspage_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// nomesPublicos são os nomes dos itens que /p/{slug} mostra.
func nomesPublicos(t *testing.T, a ambiente, slug string) []string {
	t.Helper()
	v, err := a.svc.Public(context.Background(), slug)
	if err != nil {
		t.Fatalf("/p/%s: %v", slug, err)
	}
	nomes := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		nomes = append(nomes, it.Name)
	}
	return nomes
}

// A status page é uma por projeto (00046): o titular de A e B tem uma página
// em cada, e cada uma mostra só os alvos do próprio projeto. A segunda página
// no MESMO projeto continua sendo ErrExists.
func TestSecondProjectGetsItsOwnStatusPage(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	projetoA := testdb.ProjetoPadrao(t, dono)
	projetoB := testdb.NovoProjeto(t, dono, "B")
	emA := escopoNo(projetoA, dono, tenancy.RoleOwner)
	emB := escopoNo(projetoB, dono, tenancy.RoleOwner)
	alvoA := a.alvoNoProjeto(t, dono, projetoA, "api-a")
	alvoB := a.alvoNoProjeto(t, dono, projetoB, "api-b")

	if _, err := a.svc.Create(ctx, emA, statuspage.Input{
		Slug: "pagina-a", Title: "Status A", Published: true,
		Items: []statuspage.Item{{TargetID: alvoA, DisplayName: "API de A"}},
	}); err != nil {
		t.Fatalf("Create em A: %v", err)
	}
	if _, err := a.svc.Create(ctx, emB, statuspage.Input{
		Slug: "pagina-b", Title: "Status B", Published: true,
		Items: []statuspage.Item{{TargetID: alvoB, DisplayName: "API de B"}},
	}); err != nil {
		t.Fatalf("Create em B (o titular já tem a página de A): %v", err)
	}

	if got := nomesPublicos(t, a, "pagina-a"); !slices.Equal(got, []string{"API de A"}) {
		t.Errorf("/p/pagina-a mostra %v, queria só [API de A]", got)
	}
	if got := nomesPublicos(t, a, "pagina-b"); !slices.Equal(got, []string{"API de B"}) {
		t.Errorf("/p/pagina-b mostra %v, queria só [API de B]", got)
	}

	if _, err := a.svc.Create(ctx, emB, statuspage.Input{Slug: "outra-b", Title: "Outra"}); !errors.Is(err, statuspage.ErrExists) {
		t.Errorf("segundo Create em B: erro %v, queria ErrExists", err)
	}
}

// O texto de ErrExists fala do projeto: a página deixou de ser uma por usuário.
func TestErrExistsFalaDoProjeto(t *testing.T) {
	if got, quer := statuspage.ErrExists.Error(), "Este projeto já tem uma status page."; got != quer {
		t.Errorf("ErrExists = %q, queria %q", got, quer)
	}
}
