package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
)

// A 00046 tira o UNIQUE de status_pages.user_id: a página passa a ser uma por
// projeto (o índice status_pages_project_key da 00040). O Down devolve o
// UNIQUE por usuário, e por isso falha, sem apagar nada, se algum usuário já
// tiver duas páginas.
const (
	versaoPaginaPorProjeto      = 46
	versaoAntesPaginaPorProjeto = 45
)

// projetoDe grava, por fora da RLS, um projeto não padrão do titular com o
// vínculo de owner.
func projetoDe(t *testing.T, admin *pgxpool.Pool, titular uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	if _, err := admin.Exec(ctx, `
		INSERT INTO projects (id, name, titular_id, is_default, created_at)
		VALUES ($1, $2, $3, false, now())`, id, nome, titular); err != nil {
		t.Fatalf("criar o projeto %q: %v", nome, err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1, $2, 'owner', now())`, id, titular); err != nil {
		t.Fatalf("vincular o titular ao projeto %q: %v", nome, err)
	}
	return id
}

// paginaEm grava, por fora da RLS, a página do dono no projeto.
func paginaEm(t *testing.T, admin *pgxpool.Pool, dono, projeto uuid.UUID, slug string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO status_pages (id, user_id, project_id, slug, title, published, created_at)
		VALUES ($1, $2, $3, $4, 'Status', true, now())`, novoID(), dono, projeto, slug); err != nil {
		t.Fatalf("criar a página %q: %v", slug, err)
	}
}

func TestStatusPageUserUniqueDownRefusesWithTwoPages(t *testing.T) {
	ctx := context.Background()

	t.Run("uma página por usuário desce", func(t *testing.T) {
		pool, admin, dsn := bancoNaVersao(t, versaoPaginaPorProjeto)
		u, v := novoUsuario(t, pool), novoUsuario(t, pool)
		paginaEm(t, admin, u, projetoDe(t, admin, u, "A"), "de-u")
		paginaEm(t, admin, v, projetoDe(t, admin, v, "A"), "de-v")

		if err := storage.MigrateDownTo(ctx, dsn, versaoAntesPaginaPorProjeto); err != nil {
			t.Fatalf("descer a %d com uma página por usuário: %v", versaoAntesPaginaPorProjeto, err)
		}
		// O UNIQUE por usuário voltou: a segunda página de u é recusada.
		_, err := admin.Exec(ctx, `
			INSERT INTO status_pages (id, user_id, project_id, slug, title, published, created_at)
			VALUES ($1, $2, $3, 'segunda', 'Status', true, now())`, novoID(), u, projetoDe(t, admin, u, "B"))
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "status_pages_user_id_key" {
			t.Errorf("segunda página de u depois do down: erro %v, queria 23505 em status_pages_user_id_key", err)
		}
	})

	t.Run("duas páginas do mesmo usuário recusam o down", func(t *testing.T) {
		pool, admin, dsn := bancoNaVersao(t, versaoPaginaPorProjeto)
		u := novoUsuario(t, pool)
		paginaEm(t, admin, u, projetoDe(t, admin, u, "A"), "pagina-a")
		paginaEm(t, admin, u, projetoDe(t, admin, u, "B"), "pagina-b")

		err := storage.MigrateDownTo(ctx, dsn, versaoAntesPaginaPorProjeto)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "status_pages_user_id_key" {
			t.Fatalf("descer a %d com duas páginas do mesmo usuário: erro %v, queria 23505 em status_pages_user_id_key",
				versaoAntesPaginaPorProjeto, err)
		}
		// O down recusado não sai da 00046: a versão do goose continua nela.
		if v := versaoDoBanco(t, admin); v != versaoPaginaPorProjeto {
			t.Errorf("versão do goose depois do down recusado = %d, queria %d", v, versaoPaginaPorProjeto)
		}
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM status_pages WHERE user_id = $1`, u).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("páginas de u depois do down recusado = %d, queria 2 (o down não apaga nada)", n)
		}
	})
}
