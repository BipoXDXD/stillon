package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// conviteGravado grava, por fora da RLS, um convite de projeto com o prazo e
// o aceite dados (aceitoPor uuid.Nil: pendente).
func conviteGravado(t *testing.T, projeto uuid.UUID, email string, expira time.Time, aceitoPor uuid.UUID) {
	t.Helper()
	var aceitoEm *time.Time
	var por *uuid.UUID
	if aceitoPor != uuid.Nil {
		agora := time.Now()
		aceitoEm, por = &agora, &aceitoPor
	}
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO project_invitations (id, project_id, email, role, token_hash, created_at, expires_at,
		                                 accepted_at, accepted_by)
		VALUES ($1, $2, $3, 'member', $4, $5::timestamptz - interval '30 days', $5, $6, $7)`,
		novoID(), projeto, email, hashDe(email), expira, aceitoEm, por); err != nil {
		t.Fatalf("gravar o convite de %s: %v", email, err)
	}
}

// usoDe lê TitularUsage em InUserTx(u) com o relógio agora.
func usoDe(t *testing.T, pool *pgxpool.Pool, u uuid.UUID) []db.TitularUsageRow {
	t.Helper()
	var linhas []db.TitularUsageRow
	err := storage.InUserTx(context.Background(), pool, u, func(q *db.Queries) error {
		var err error
		linhas, err = q.TitularUsage(context.Background(), time.Now())
		return err
	})
	if err != nil {
		t.Fatalf("TitularUsage de %s: %v", u, err)
	}
	return linhas
}

type contagemDeUso struct{ alvos, heartbeats, canais, membros, convites int64 }

func contagemDaLinha(l db.TitularUsageRow) contagemDeUso {
	return contagemDeUso{l.Alvos, l.Heartbeats, l.Canais, l.Membros, l.Convites}
}

// usoDeDoisProjetos: U é titular do padrão A (3 alvos, 1 heartbeat, 2 canais)
// e de B (2 alvos, 5 canais, M como member, um convite válido, um vencido e
// um aceito).
func usoDeDoisProjetos(t *testing.T, pool *pgxpool.Pool) (u, a, b uuid.UUID) {
	t.Helper()
	u = novoUsuario(t, pool)
	a = testdb.ProjetoPadrao(t, u)
	b = testdb.NovoProjeto(t, u, "Projeto B")
	testdb.Recursos(t, u, a, 3, 1, 2)
	testdb.Recursos(t, u, b, 2, 0, 5)
	m := novoUsuario(t, pool)
	testdb.Membro(t, b, m, "member")
	agora := time.Now()
	conviteGravado(t, b, "valido@exemplo.com", agora.Add(24*time.Hour), uuid.Nil)
	conviteGravado(t, b, "vencido@exemplo.com", agora.Add(-time.Hour), uuid.Nil)
	conviteGravado(t, b, "aceito@exemplo.com", agora.Add(24*time.Hour), m)
	return u, a, b
}

// Duas linhas, o padrão primeiro, cada uma com as contagens do próprio
// projeto. Na mesma transação, depois, o escopo de usuário continua sem ver
// recurso e os GUCs da cota voltam vazios.
func TestTitularUsageCountsEveryProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, b := usoDeDoisProjetos(t, pool)

	g := &guardaTx{Beginner: pool}
	err := storage.InUserTx(ctx, g, u, func(q *db.Queries) error {
		linhas, err := q.TitularUsage(ctx, time.Now())
		if err != nil {
			return err
		}
		if len(linhas) != 2 {
			t.Fatalf("TitularUsage devolveu %d linhas, queria 2: %+v", len(linhas), linhas)
		}
		if linhas[0].ID != a || !linhas[0].IsDefault || linhas[0].Name != "Projeto padrão" {
			t.Errorf("a primeira linha é %+v; queria o padrão A %s", linhas[0], a)
		}
		if linhas[1].ID != b || linhas[1].IsDefault || linhas[1].Name != "Projeto B" {
			t.Errorf("a segunda linha é %+v; queria B %s", linhas[1], b)
		}
		if c, quer := contagemDaLinha(linhas[0]), (contagemDeUso{3, 1, 2, 0, 0}); c != quer {
			t.Errorf("as contagens de A são %+v; queria %+v", c, quer)
		}
		if c, quer := contagemDaLinha(linhas[1]), (contagemDeUso{2, 0, 5, 1, 1}); c != quer {
			t.Errorf("as contagens de B são %+v; queria %+v", c, quer)
		}

		var vistos int64
		if err := g.tx.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&vistos); err != nil {
			return err
		}
		if vistos != 0 {
			t.Errorf("depois do uso, o escopo de usuário vê %d alvos; queria 0", vistos)
		}
		_, titular, projetos := escopoDepoisDaCota(t, g.tx)
		if titular != "" || projetos != "" {
			t.Errorf("depois do uso: app.cota_titular_id %q, app.cota_projetos %q; queria vazios", titular, projetos)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// U é só membro do projeto de O: o uso lista os projetos de que U é titular,
// não os que U enxerga.
func TestTitularUsageIgnoresProjectsWhereOnlyMember(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	pu := testdb.ProjetoPadrao(t, u)
	o := novoUsuario(t, pool)
	po := testdb.ProjetoPadrao(t, o)
	testdb.Recursos(t, o, po, 4, 0, 0)
	testdb.Membro(t, po, u, "manager")

	linhas := usoDe(t, pool, u)
	if len(linhas) != 1 || linhas[0].ID != pu {
		t.Errorf("TitularUsage de U devolveu %+v; queria só o padrão de U %s", linhas, pu)
	}
}

// A suspensão não esconde o uso: o operador lê justamente as contas suspensas.
func TestTitularUsageWorksWhileSuspended(t *testing.T) {
	pool := testdb.New(t)
	u, a, b := usoDeDoisProjetos(t, pool)
	testdb.Suspender(t, u)

	linhas := usoDe(t, pool, u)
	if len(linhas) != 2 || linhas[0].ID != a || linhas[1].ID != b {
		t.Fatalf("TitularUsage de U suspenso devolveu %+v; queria A e B", linhas)
	}
	if c, quer := contagemDaLinha(linhas[0]), (contagemDeUso{3, 1, 2, 0, 0}); c != quer {
		t.Errorf("as contagens de A com U suspenso são %+v; queria %+v", c, quer)
	}
	if c, quer := contagemDaLinha(linhas[1]), (contagemDeUso{2, 0, 5, 1, 1}); c != quer {
		t.Errorf("as contagens de B com U suspenso são %+v; queria %+v", c, quer)
	}
}

// Sem app.user_id (o escopo do sondador enxerga todos os projetos), a função
// recusa em vez de devolver vazio.
func TestTitularUsageRefusesWithoutUserScope(t *testing.T) {
	pool := testdb.New(t)
	usoDeDoisProjetos(t, pool)

	var linhas []db.TitularUsageRow
	err := storage.InProberTx(context.Background(), pool, func(q *db.Queries) error {
		var err error
		linhas, err = q.TitularUsage(context.Background(), time.Now())
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("TitularUsage no escopo do sondador deu %+v, %v; queria 42501", linhas, err)
	}
}
