package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As rotas antigas, sem projeto no caminho, agem no Projeto padrão do
// usuário. Quem ainda não tem um (o usuário de newAPI é inserido cru) ganha o
// projeto e o vínculo de owner na primeira chamada, e só um.
func TestLegacyRouteEnsuresDefaultProject(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	contar := func() (projetos, donos int) {
		t.Helper()
		admin := testdb.Admin(t)
		ctx := context.Background()
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM projects WHERE titular_id = $1 AND is_default`, a.userID).Scan(&projetos); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, `
			SELECT count(*) FROM project_members m JOIN projects p ON p.id = m.project_id
			WHERE m.user_id = $1 AND m.role = 'owner' AND p.is_default`, a.userID).Scan(&donos); err != nil {
			t.Fatal(err)
		}
		return projetos, donos
	}

	if p, d := contar(); p != 0 || d != 0 {
		t.Fatalf("antes da primeira chamada: %d projetos padrão e %d vínculos de owner, queria nenhum", p, d)
	}
	for i := 1; i <= 2; i++ {
		resp := chamar(t, a, http.MethodGet, "/api/v1/targets", token, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("chamada %d: GET /api/v1/targets deu %d, queria 200", i, resp.StatusCode)
		}
		if p, d := contar(); p != 1 || d != 1 {
			t.Fatalf("depois da chamada %d: %d projetos padrão e %d vínculos de owner, queria 1 e 1", i, p, d)
		}
	}
}
