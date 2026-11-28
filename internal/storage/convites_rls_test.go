package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration dos convites, achada pelo nome.
const sufixoConvites = "convites"

// usuarioComEmail cria uma conta com o e-mail dado (users não tem RLS).
func usuarioComEmail(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := novoID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, email); err != nil {
		t.Fatal(err)
	}
	return id
}

// hashDoConvite é o SHA-256 do token do link, o que o banco guarda.
func hashDoConvite(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// equipeDoConvite cria A, titular de PA; B, com a conta b@x.com; C, com
// c@x.com e titular de PC; e um convite de PA para b@x.com com o papel dado.
func equipeDoConvite(t *testing.T, pool *pgxpool.Pool, papel string) (a, b, c, pa, pc uuid.UUID, hash []byte) {
	t.Helper()
	a = novoUsuario(t, pool)
	b = usuarioComEmail(t, pool, "b@x.com")
	c = usuarioComEmail(t, pool, "c@x.com")
	pa = testdb.NovoProjeto(t, a, "Projeto A")
	pc = testdb.NovoProjeto(t, c, "Projeto C")
	return a, b, c, pa, pc, hashDoConvite(testdb.Convite(t, pa, "b@x.com", papel))
}

// aceitar roda AcceptInvitation e depois, se ele achou a linha, o INSERT do
// vínculo com os valores dados, numa transação InInvitationAcceptTx(quem,
// escopo) só.
func aceitar(t *testing.T, pool *pgxpool.Pool, quem uuid.UUID, escopo []byte,
	vinculo db.InsertMemberFromInvitationParams) (aceitou bool, err error) {
	t.Helper()
	ctx := context.Background()
	err = storage.InInvitationAcceptTx(ctx, pool, quem, escopo, func(q *db.Queries) error {
		if _, err := q.AcceptInvitation(ctx, db.AcceptInvitationParams{TokenHash: escopo, Now: agora}); err != nil {
			return err
		}
		aceitou = true
		return q.InsertMemberFromInvitation(ctx, vinculo)
	})
	return aceitou, err
}

// pendente diz, por fora da RLS, se o convite do hash continua pendente.
func pendente(t *testing.T, hash []byte) bool {
	t.Helper()
	var p bool
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT accepted_at IS NULL FROM project_invitations WHERE token_hash = $1`, hash).Scan(&p); err != nil {
		t.Fatalf("ler o convite: %v", err)
	}
	return p
}

// No escopo do projeto, o convite aparece; no escopo de usuário e no de
// outro projeto do mesmo titular, não.
func TestInvitationsVisibleOnlyInOwnProjectScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, _, _, pa, _, _ := equipeDoConvite(t, pool, "member")
	outro := testdb.NovoProjeto(t, a, "Outro de A")

	contar := func(projeto uuid.UUID) int {
		var n int
		if err := emEscopoDeProjeto(t, pool, a, projeto, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM project_invitations`).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := contar(pa); n != 1 {
		t.Errorf("no escopo de PA: %d convites; queria 1", n)
	}
	if n := contar(outro); n != 0 {
		t.Errorf("no escopo do outro projeto de A: %d convites; queria 0", n)
	}
	if n := contarComEscopo(t, pool, a, "project_invitations"); n != 0 {
		t.Errorf("no escopo de usuário: %d convites; queria 0", n)
	}
}

// O escopo do token enxerga só a linha do próprio hash: nem o outro convite
// do mesmo projeto, nem vínculos, nem projetos.
func TestTokenScopeSeesOnlyItsRow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, _, _, pa, _, hash := equipeDoConvite(t, pool, "member")
	testdb.Convite(t, pa, "c@x.com", "viewer")

	g := &guardaTx{Beginner: pool}
	err := storage.InInvitationTokenTx(ctx, g, hash, func(*db.Queries) error {
		var visto []byte
		var n int
		if err := g.tx.QueryRow(ctx, `SELECT count(*), min(token_hash) FROM project_invitations`).Scan(&n, &visto); err != nil {
			return err
		}
		if n != 1 || !bytes.Equal(visto, hash) {
			t.Errorf("project_invitations no escopo do token: %d linha(s), hash %x; queria só a do hash %x", n, visto, hash)
		}
		for _, tabela := range []string{"project_members", "projects"} {
			if err := g.tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("%s no escopo do token: %d linha(s); queria 0", tabela, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Outra conta logada com o link não consome o convite nem entra no projeto,
// mesmo que o Go fosse pulado: a política compara o e-mail da conta.
func TestAcceptByOtherAccountRefusedInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, _, c, pa, _, hash := equipeDoConvite(t, pool, "member")

	aceitou, err := aceitar(t, pool, c, hash, db.InsertMemberFromInvitationParams{})
	if aceitou || !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("AcceptInvitation por C: aceitou=%v, erro %v; queria pgx.ErrNoRows", aceitou, err)
	}
	err = storage.InInvitationAcceptTx(ctx, pool, c, hash, func(q *db.Queries) error {
		return q.InsertMemberFromInvitation(ctx, db.InsertMemberFromInvitationParams{
			ProjectID: pa, UserID: c, Role: "member", Now: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("C inserindo-se em PA com o hash do convite de B: %v; queria 42501", err)
	}
	if !pendente(t, hash) {
		t.Error("o convite deixou de estar pendente")
	}
}

// Quem aceita entra só no projeto e com o papel do convite, e só a si mesmo.
func TestAcceptInsertsOnlyInvitedRoleAndProject(t *testing.T) {
	pool := testdb.New(t)
	_, b, c, pa, pc, hash := equipeDoConvite(t, pool, "member")

	for _, caso := range []struct {
		nome    string
		vinculo db.InsertMemberFromInvitationParams
	}{
		{"outro papel", db.InsertMemberFromInvitationParams{ProjectID: pa, UserID: b, Role: "owner", Now: agora}},
		{"outro projeto", db.InsertMemberFromInvitationParams{ProjectID: pc, UserID: b, Role: "member", Now: agora}},
		{"outra pessoa", db.InsertMemberFromInvitationParams{ProjectID: pa, UserID: c, Role: "member", Now: agora}},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			aceitou, err := aceitar(t, pool, b, hash, caso.vinculo)
			if !aceitou || codigoPG(err) != "42501" {
				t.Errorf("aceitou=%v, erro %v; queria aceitar e o INSERT dar 42501", aceitou, err)
			}
		})
	}
	if aceitou, err := aceitar(t, pool, b, hash, db.InsertMemberFromInvitationParams{
		ProjectID: pa, UserID: b, Role: "member", Now: agora,
	}); !aceitou || err != nil {
		t.Fatalf("com o projeto e o papel do convite: aceitou=%v, erro %v; queria sem erro", aceitou, err)
	}
	var papel string
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, pa, b).Scan(&papel); err != nil || papel != "member" {
		t.Errorf("vínculo de B em PA: papel %q, erro %v; queria member", papel, err)
	}
	if pendente(t, hash) {
		t.Error("o convite continua pendente depois do aceite")
	}
}

// Aceitar só preenche accepted_at e accepted_by. Um UPDATE cru no escopo do
// aceite que troque o papel ou o projeto do convite é recusado (gatilho), e
// a política de INSERT por convite nunca vê um owner forjado nem outro
// projeto.
func TestAcceptCannotRewriteInvitationRoleOrProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, b, _, _, pc, hash := equipeDoConvite(t, pool, "member")
	for _, caso := range []struct {
		nome, sql string
		args      []any
	}{
		{"papel", `UPDATE project_invitations SET role = 'owner', accepted_at = now(), accepted_by = $1
		            WHERE token_hash = $2`, []any{b, hash}},
		{"projeto", `UPDATE project_invitations SET project_id = $3, accepted_at = now(), accepted_by = $1
		              WHERE token_hash = $2`, []any{b, hash, pc}},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			g := &guardaTx{Beginner: pool}
			err := storage.InInvitationAcceptTx(ctx, g, b, hash, func(*db.Queries) error {
				_, err := g.tx.Exec(ctx, caso.sql, caso.args...)
				return err
			})
			if codigoPG(err) != "42501" {
				t.Errorf("aceite trocando o %s do convite: %v; queria 42501", caso.nome, err)
			}
			if !pendente(t, hash) {
				t.Error("o convite deixou de estar pendente")
			}
		})
	}
}

// O convite vencido não se aceita nem em SQL cru, sem o filtro de prazo da
// consulta: a política de aceite confere expires_at com o relógio do banco.
func TestExpiredInvitationNotAcceptableInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	b := usuarioComEmail(t, pool, "b@x.com")
	pa := testdb.NovoProjeto(t, a, "Projeto A")
	hash := hashDoConvite("convite-vencido")
	// O gatilho não deixa mudar o prazo: o vencido já nasce vencido.
	if _, err := testdb.Admin(t).Exec(ctx, `INSERT INTO project_invitations
		(id, project_id, email, role, token_hash, invited_by, created_at, expires_at)
		VALUES ($1, $2, 'b@x.com', 'member', $3, $4, now() - interval '8 days', now() - interval '1 day')`,
		novoID(), pa, hash, a); err != nil {
		t.Fatal(err)
	}
	g := &guardaTx{Beginner: pool}
	var linhas int64
	err := storage.InInvitationAcceptTx(ctx, g, b, hash, func(*db.Queries) error {
		tag, err := g.tx.Exec(ctx, `UPDATE project_invitations SET accepted_at = now(), accepted_by = $1
			WHERE token_hash = $2`, b, hash)
		linhas = tag.RowsAffected()
		return err
	})
	if err != nil || linhas != 0 {
		t.Errorf("aceite cru do convite vencido: %d linha(s), erro %v; queria 0 linhas", linhas, err)
	}
	if !pendente(t, hash) {
		t.Error("o convite vencido foi aceito")
	}
}

// Sem convite aceito por ela no escopo do hash, a pessoa não entra: nem no
// escopo de usuário (a política da 00039 é só do titular), nem com o hash de
// um convite que ainda não aceitou.
func TestMemberInsertWithoutAcceptedInvitationRefused(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, b, _, pa, _, hash := equipeDoConvite(t, pool, "viewer")

	err := storage.InUserTx(ctx, pool, b, func(q *db.Queries) error {
		return q.InsertMemberFromInvitation(ctx, db.InsertMemberFromInvitationParams{
			ProjectID: pa, UserID: b, Role: "viewer", Now: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("no escopo de usuário: %v; queria 42501", err)
	}
	err = storage.InInvitationAcceptTx(ctx, pool, b, hash, func(q *db.Queries) error {
		return q.InsertMemberFromInvitation(ctx, db.InsertMemberFromInvitationParams{
			ProjectID: pa, UserID: b, Role: "viewer", Now: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("no escopo do aceite sem AcceptInvitation: %v; queria 42501", err)
	}
}

// Os escopos do convite recusam hash de tamanho errado e usuário nulo antes
// de abrir a transação.
func TestInvitationScopesRefuseShortHashAndNilUser(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	nada := func(*db.Queries) error { return nil }
	if err := storage.InInvitationTokenTx(ctx, pool, []byte("curto"), nada); !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("InInvitationTokenTx com hash curto: %v; queria ErrNoToken", err)
	}
	if err := storage.InInvitationAcceptTx(ctx, pool, novoID(), []byte("curto"), nada); !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("InInvitationAcceptTx com hash curto: %v; queria ErrNoToken", err)
	}
	if err := storage.InInvitationAcceptTx(ctx, pool, uuid.Nil, hashDe("x"), nada); !errors.Is(err, storage.ErrNoUser) {
		t.Errorf("InInvitationAcceptTx com usuário nulo: %v; queria ErrNoUser", err)
	}
}

// catalogoDeConvites resume as políticas de projects, project_members e
// project_invitations e diz se a tabela existe.
func catalogoDeConvites(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var s string
	err := pool.QueryRow(context.Background(), `
		SELECT concat_ws(E'\n',
		  coalesce(to_regclass('public.project_invitations')::text, 'sem project_invitations'),
		  (SELECT string_agg(l, E'\n' ORDER BY l) FROM (
		     SELECT c.relname || '.' || p.polname || ':' || p.polcmd::text || ':' ||
		            coalesce(pg_get_expr(p.polqual, p.polrelid), '') || ':' ||
		            coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '') AS l
		       FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		      WHERE c.relname IN ('projects', 'project_members', 'project_invitations')) x))`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A 00049 sobe, desce devolvendo o catálogo de antes e sobe de novo.
func TestInvitationsMigrationDownRestoresCatalog(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoConvites)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao-1); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	antes := catalogoDeConvites(t, pool)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir a %d: %v", versao, err)
	}
	subiu := catalogoDeConvites(t, pool)
	if subiu == antes {
		t.Fatalf("a %d não mudou o catálogo", versao)
	}
	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if depois := catalogoDeConvites(t, pool); depois != antes {
		t.Errorf("catálogo depois do down:\n%s\nqueria:\n%s", depois, antes)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir a %d de novo: %v", versao, err)
	}
	if denovo := catalogoDeConvites(t, pool); denovo != subiu {
		t.Errorf("catálogo depois de subir de novo:\n%s\nqueria:\n%s", denovo, subiu)
	}
}
