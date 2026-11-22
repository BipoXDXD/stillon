package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
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

// Um alvo gravado sem project_id pelo usuário recém-criado cai no Projeto
// padrão dele (o gatilho da 00040 precisa que o padrão exista).
func TestCreatedUserTargetLandsInDefaultProject(t *testing.T) {
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

	// O InsertTarget exige project_id; o INSERT cru, no escopo só do usuário
	// (o do InUserTx), é o caminho antigo que o gatilho ainda cobre.
	alvo := uuid.New()
	agora := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		VALUES ($1, $2, 'alvo', 'https://exemplo.com', 'GET', 300, 1000, 200, true, $3, $3)`,
		alvo, id, agora); err != nil {
		t.Fatalf("gravar o alvo sem project_id: %v", err)
	}
	got, err := db.New(tx).GetTarget(ctx, db.GetTargetParams{ProjectID: padrao, ID: alvo})
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID == nil || *got.ProjectID != padrao {
		t.Errorf("project_id do alvo = %v, queria o Projeto padrão %v", got.ProjectID, padrao)
	}
}

// A recusa do segundo usuário não deixa projeto para trás.
func TestCreateUserRefusesSecondUserWithoutCreatingProject(t *testing.T) {
	svc, _ := novo(t)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, "dono@exemplo.com", senha); err != nil {
		t.Fatal(err)
	}

	_, err := svc.CreateUser(ctx, "outro@exemplo.com", senha)
	if !errors.Is(err, account.ErrUserExists) {
		t.Fatalf("segunda criação: err = %v, queria ErrUserExists", err)
	}

	var n int
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d projetos depois da recusa, queria 1", n)
	}
}
