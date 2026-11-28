package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration dos membros e papéis, achada pelo nome.
const sufixoMembros = "membros_e_papeis"

// equipeDeTres cria A, titular do projeto PA; B, member de PA; e C, titular
// do projeto PC, de fora de PA.
func equipeDeTres(t *testing.T, pool *pgxpool.Pool) (a, b, c, pa, pc uuid.UUID) {
	t.Helper()
	a, b, c = novoUsuario(t, pool), novoUsuario(t, pool), novoUsuario(t, pool)
	pa = testdb.NovoProjeto(t, a, "Projeto A")
	pc = testdb.NovoProjeto(t, c, "Projeto C")
	testdb.Membro(t, pa, b, "member")
	return a, b, c, pa, pc
}

// No escopo do projeto, o membro vê os colegas; no escopo de usuário, só o
// próprio vínculo; e o escopo de outro projeto não vê nada de PA.
func TestMemberSeesColleaguesOnlyInProjectScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, b, c, pa, pc := equipeDeTres(t, pool)

	var n int
	if err := noEscopoDoProjeto(t, pool, b, pa, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM project_members`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("B no escopo de PA vê %d vínculos; queria 2 (A e B)", n)
	}
	if n := contarComEscopo(t, pool, b, "project_members"); n != 1 {
		t.Errorf("B no escopo de usuário vê %d vínculos; queria 1 (só o próprio)", n)
	}
	if err := noEscopoDoProjeto(t, pool, c, pc, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM project_members WHERE project_id = $1`, pa).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("C no escopo de PC vê %d vínculos de PA; queria 0", n)
	}
}

// O titular é sempre owner: rebaixá-lo, apagar o vínculo dele ou criar um
// projeto sem o vínculo de owner falha no commit (FK adiável), mesmo que o
// domínio fosse pulado.
func TestTitularAlwaysOwnerInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, _, _, pa, _ := equipeDeTres(t, pool)

	for _, caso := range []struct{ nome, sql string }{
		{"rebaixar o titular", `UPDATE project_members SET role = 'member' WHERE project_id = $1 AND user_id = $2`},
		{"apagar o vínculo do titular", `DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			var linhas int64
			err := noEscopoDoProjeto(t, pool, a, pa, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, caso.sql, pa, a)
				linhas = tag.RowsAffected()
				return err
			})
			if linhas != 1 {
				t.Errorf("o comando afetou %d linhas; queria 1 (a recusa tem de vir da FK, no commit)", linhas)
			}
			if codigoPG(err) != "23503" {
				t.Errorf("erro %v; queria 23503 no commit", err)
			}
		})
	}

	t.Run("projeto sem vínculo de owner", func(t *testing.T) {
		err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
			_, err := q.InsertProject(ctx, db.InsertProjectParams{ID: novoID(), Name: "Órfão", TitularID: a, Now: agora})
			return err
		})
		if codigoPG(err) != "23503" {
			t.Errorf("erro %v; queria 23503 no commit", err)
		}
	})
}

// O UPDATE de projects só muda o nome, e só no projeto do escopo.
func TestProjectsUpdateOnlyName(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b, c, pa, pc := equipeDeTres(t, pool)

	var linhas int64
	if err := noEscopoDoProjeto(t, pool, a, pa, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE projects SET name = 'Novo nome' WHERE id = $1`, pa)
		linhas = tag.RowsAffected()
		return err
	}); err != nil || linhas != 1 {
		t.Errorf("renomear PA no escopo de PA: %d linhas, erro %v; queria 1 e nil", linhas, err)
	}

	for _, caso := range []struct {
		nome string
		sql  string
		args []any
	}{
		{"titular_id", `UPDATE projects SET titular_id = $2 WHERE id = $1`, []any{pa, b}},
		{"is_default", `UPDATE projects SET is_default = NOT is_default WHERE id = $1`, []any{pa}},
		{"created_at", `UPDATE projects SET created_at = now() WHERE id = $1`, []any{pa}},
		{"id", `UPDATE projects SET id = $2 WHERE id = $1`, []any{pa, novoID()}},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			err := noEscopoDoProjeto(t, pool, a, pa, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, caso.sql, caso.args...)
				return err
			})
			if codigoPG(err) != "42501" {
				t.Errorf("mudar %s: erro %v; queria 42501", caso.nome, err)
			}
		})
	}

	if err := noEscopoDoProjeto(t, pool, c, pc, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE projects SET name = 'x' WHERE id = $1`, pa)
		linhas = tag.RowsAffected()
		return err
	}); err != nil || linhas != 0 {
		t.Errorf("C renomeando PA no escopo de PC: %d linhas, erro %v; queria 0 e nil", linhas, err)
	}
	if n := contarComEscopo(t, pool, a, "projects WHERE name = 'x'"); n != 0 {
		t.Errorf("PA ficou com o nome que C mandou")
	}
}

// UPDATE e DELETE de vínculos só alcançam o projeto do escopo.
func TestMemberUpdateDeleteStayInsideProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, b, c, pa, pc := equipeDeTres(t, pool)

	for _, sql := range []string{
		`UPDATE project_members SET role = 'viewer' WHERE project_id = $1 AND user_id = $2`,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`,
	} {
		var linhas int64
		if err := noEscopoDoProjeto(t, pool, c, pc, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, sql, pa, b)
			linhas = tag.RowsAffected()
			return err
		}); err != nil || linhas != 0 {
			t.Errorf("%s no escopo de PC: %d linhas, erro %v; queria 0 e nil", strings.Fields(sql)[0], linhas, err)
		}
	}
	var papel string
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, pa, b).Scan(&papel); err != nil || papel != "member" {
		t.Errorf("vínculo de B em PA: papel %q, erro %v; queria member intacto", papel, err)
	}
}

// O binário anterior grava o projeto antes do vínculo, na mesma transação:
// com a FK adiável, os dois caminhos dele seguem comitando.
func TestOlderBinaryProjectCreationStillCommits(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)

	if err := storage.InUserTx(ctx, pool, u, func(q *db.Queries) error {
		_, err := storage.CriarProjetoPadraoNaTx(ctx, q, u, agora)
		return err
	}); err != nil {
		t.Errorf("CriarProjetoPadraoNaTx: %v", err)
	}

	id := novoID()
	if err := storage.InUserTx(ctx, pool, u, func(q *db.Queries) error {
		if _, err := q.InsertProject(ctx, db.InsertProjectParams{ID: id, Name: "Outro", TitularID: u, Now: agora}); err != nil {
			return err
		}
		return q.InsertProjectOwner(ctx, db.InsertProjectOwnerParams{ProjectID: id, UserID: u, Now: agora})
	}); err != nil {
		t.Errorf("InsertProject + InsertProjectOwner: %v", err)
	}
}

// catalogoDeMembros resume políticas, constraints, colunas e gatilhos de
// projects e project_members, e as funções da 00048.
func catalogoDeMembros(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var s string
	err := pool.QueryRow(context.Background(), `
		SELECT concat_ws(E'\n',
		  (SELECT string_agg(l, E'\n' ORDER BY l) FROM (
		     SELECT c.relname || '.' || p.polname || ':' || p.polcmd::text || ':' ||
		            coalesce(pg_get_expr(p.polqual, p.polrelid), '') || ':' ||
		            coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '') AS l
		       FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		      WHERE c.relname IN ('projects', 'project_members')) x),
		  (SELECT string_agg(l, E'\n' ORDER BY l) FROM (
		     SELECT conrelid::regclass::text || '.' || conname || ':' || pg_get_constraintdef(oid) AS l
		       FROM pg_constraint WHERE conrelid IN ('projects'::regclass, 'project_members'::regclass)) x),
		  (SELECT string_agg(l, E'\n' ORDER BY l) FROM (
		     SELECT attrelid::regclass::text || '.' || attname || ':' || format_type(atttypid, atttypmod) AS l
		       FROM pg_attribute
		      WHERE attrelid IN ('projects'::regclass, 'project_members'::regclass) AND attnum > 0 AND NOT attisdropped) x),
		  (SELECT string_agg(l, E'\n' ORDER BY l) FROM (
		     SELECT tgrelid::regclass::text || '.' || tgname AS l
		       FROM pg_trigger WHERE tgrelid IN ('projects'::regclass, 'project_members'::regclass) AND NOT tgisinternal) x),
		  (SELECT count(*)::text FROM pg_proc WHERE proname = 'guard_projects_imutavel'))`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// O down da 00048 devolve o catálogo de antes e não toca nas linhas.
func TestMembersMigrationDownRestoresCatalog(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoMembros)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao-1); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	u := novoUsuario(t, pool)
	projeto := projetoPadraoDe(t, pool, u)
	antes := catalogoDeMembros(t, pool)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir a %d: %v", versao, err)
	}
	if depois := catalogoDeMembros(t, pool); depois == antes {
		t.Fatalf("a %d não mudou o catálogo de projects e project_members", versao)
	}
	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if depois := catalogoDeMembros(t, pool); depois != antes {
		t.Errorf("catálogo depois do down:\n%s\nqueria:\n%s", depois, antes)
	}
	var n int
	if err := emEscopo(t, pool, "app.user_id", u.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM projects p JOIN project_members m ON m.project_id = p.id
			WHERE p.id = $1 AND m.user_id = $2 AND m.role = 'owner'`, projeto, u).Scan(&n)
	}); err != nil || n != 1 {
		t.Errorf("projeto e vínculo depois do down: %d linhas, erro %v; queria 1", n, err)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
}
