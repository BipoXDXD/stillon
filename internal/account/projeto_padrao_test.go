package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// O usuário criado pelo CLI nasce operador, com o e-mail verificado e com o
// Projeto padrão, de que é titular e owner, tudo na transação da criação.
func TestCreateUserCreatesDefaultProjectAndMarksOperator(t *testing.T) {
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC))
	svc := comHasherBarato(account.NewService(pool, nil, clk, nil))
	ctx := context.Background()

	id, err := svc.CreateUser(ctx, "dono@exemplo.com", senha)
	if err != nil {
		t.Fatal(err)
	}

	admin := testdb.Admin(t)
	var operador bool
	var verificado *time.Time
	if err := admin.QueryRow(ctx,
		`SELECT is_operator, email_verified_at FROM users WHERE id = $1`, id).Scan(&operador, &verificado); err != nil {
		t.Fatal(err)
	}
	if !operador {
		t.Errorf("is_operator = false, queria true")
	}
	if verificado == nil || !verificado.Equal(clk.Now()) {
		t.Errorf("email_verified_at = %v, queria %v", verificado, clk.Now())
	}

	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d projetos, queria 1", n)
	}
	var (
		projeto, titular uuid.UUID
		padrao           bool
		nome             string
	)
	if err := admin.QueryRow(ctx,
		`SELECT id, titular_id, is_default, name FROM projects`).Scan(&projeto, &titular, &padrao, &nome); err != nil {
		t.Fatal(err)
	}
	if titular != id || !padrao || nome != "Projeto padrão" {
		t.Errorf("projeto = (titular %v, padrão %v, nome %q), queria (%v, true, %q)",
			titular, padrao, nome, id, "Projeto padrão")
	}

	type vinculo struct {
		projeto, usuario uuid.UUID
		papel            string
	}
	rows, err := admin.Query(ctx, `SELECT project_id, user_id, role FROM project_members`)
	if err != nil {
		t.Fatal(err)
	}
	var vinculos []vinculo
	for rows.Next() {
		var v vinculo
		if err := rows.Scan(&v.projeto, &v.usuario, &v.papel); err != nil {
			t.Fatal(err)
		}
		vinculos = append(vinculos, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := vinculo{projeto: projeto, usuario: id, papel: "owner"}
	if len(vinculos) != 1 || vinculos[0] != want {
		t.Errorf("vínculos = %+v, queria só %+v", vinculos, want)
	}
}

// O usuário recém-criado grava no Projeto padrão pelo escopo de projeto: o
// vínculo de owner que a criação gravou basta para a conferência de pertença
// de InProjectTx, sem semente por fora da RLS.
func TestCreatedUserWritesTargetInDefaultProject(t *testing.T) {
	svc, pool := novo(t)
	ctx := context.Background()
	id, err := svc.CreateUser(ctx, "dono@exemplo.com", senha)
	if err != nil {
		t.Fatal(err)
	}

	var padrao uuid.UUID
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT id FROM projects WHERE titular_id = $1 AND is_default`, id).Scan(&padrao); err != nil {
		t.Fatalf("ler o Projeto padrão: %v", err)
	}

	alvo := uuid.New()
	agora := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
	err = storage.InProjectTx(ctx, pool, id, padrao, func(q *db.Queries, _ tenancy.ProjectScope) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: id, ProjectID: padrao, Name: "alvo", URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 300, TimeoutMS: 1000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		got, err := q.GetTarget(ctx, db.GetTargetParams{ProjectID: padrao, ID: alvo})
		if err != nil {
			return err
		}
		if got.ProjectID == nil || *got.ProjectID != padrao {
			t.Errorf("project_id do alvo = %v, queria o Projeto padrão %v", got.ProjectID, padrao)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("gravar e ler o alvo no Projeto padrão: %v", err)
	}
}

// A segunda conta, com outro e-mail, também nasce com o seu Projeto padrão.
func TestCreateUserSegundaContaGanhaProjetoProprio(t *testing.T) {
	svc, _ := novo(t)
	ctx := context.Background()
	primeiro, err := svc.CreateUser(ctx, "dono@exemplo.com", senha)
	if err != nil {
		t.Fatal(err)
	}

	segundo, err := svc.CreateUser(ctx, "outro@exemplo.com", senha)
	if err != nil {
		t.Fatalf("segunda criação: %v", err)
	}

	var n int
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d projetos depois da segunda conta, queria 2", n)
	}
	if projetosPadraoDe(t, primeiro) != 1 || projetosPadraoDe(t, segundo) != 1 {
		t.Error("cada conta deveria ter o seu Projeto padrão")
	}
}
