package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

// Sementes por fora da RLS: n linhas do recurso no projeto, de dono user.
const (
	encherAlvos = `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, $2, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $3::int) i`
	encherHeartbeats = `
		INSERT INTO heartbeats (id, user_id, project_id, name, ping_token_hash, period_sec, grace_sec,
		                        enabled, created_at, deadline_at, description, ping_resumes)
		SELECT gen_random_uuid(), $1, $2, 'hb ' || i, sha256(convert_to(gen_random_uuid()::text, 'UTF8')),
		       3600, 300, true, now(), now() + interval '1 hour', '', false
		  FROM generate_series(1, $3::int) i`
	encherCanais = `
		INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, created_at)
		SELECT gen_random_uuid(), $1, $2, 'discord', '\x01', now(), now()
		  FROM generate_series(1, $3::int) i`
)

func encher(t *testing.T, sql string, dono, projeto uuid.UUID, n int64) {
	t.Helper()
	if _, err := testdb.Admin(t).Exec(context.Background(), sql, dono, projeto, n); err != nil {
		t.Fatalf("semear: %v", err)
	}
}

// cessao é o projeto PA de A (titular) com B owner, C manager e D viewer, e o
// Projeto padrão de B.
type cessao struct {
	a, b, c, d  uuid.UUID
	pa, padraoB uuid.UUID
}

func novaCessao(t *testing.T, pool *pgxpool.Pool) cessao {
	t.Helper()
	c := cessao{a: novoUsuario(t, pool), b: novoUsuario(t, pool), c: novoUsuario(t, pool), d: novoUsuario(t, pool)}
	c.pa = testdb.NovoProjeto(t, c.a, "Projeto A")
	testdb.Membro(t, c.pa, c.b, "owner")
	testdb.Membro(t, c.pa, c.c, "manager")
	testdb.Membro(t, c.pa, c.d, "viewer")
	c.padraoB = testdb.ProjetoPadrao(t, c.b)
	return c
}

func titularDe(t *testing.T, projeto uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT titular_id FROM projects WHERE id = $1`, projeto).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func pendenteDe(t *testing.T, projeto uuid.UUID) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT pending_titular_id FROM projects WHERE id = $1`, projeto).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func propor(t *testing.T, st *pgstore.Store, ator, projeto, alvo uuid.UUID) {
	t.Helper()
	if err := st.ProposeTransfer(context.Background(), ator, projeto, alvo); err != nil {
		t.Fatalf("propor: %v", err)
	}
}

func TestProposeTransferAuthorizedInTx(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)
	fora := novoUsuario(t, pool)

	casos := []struct {
		nome          string
		ator, projeto uuid.UUID
		alvo          uuid.UUID
		quer          error
	}{
		{"owner que não é o titular propõe", c.b, c.pa, c.b, tenancy.ErrForbidden},
		{"manager propõe", c.c, c.pa, c.b, tenancy.ErrForbidden},
		{"titular propõe a um manager", c.a, c.pa, c.c, tenancy.ErrTransferTarget},
		{"titular propõe a um viewer", c.a, c.pa, c.d, tenancy.ErrTransferTarget},
		{"titular propõe a si mesmo", c.a, c.pa, c.a, tenancy.ErrTransferTarget},
		{"titular propõe a quem não é membro", c.a, c.pa, fora, tenancy.ErrTransferTarget},
		{"quem não é membro propõe", fora, c.pa, c.b, tenancy.ErrProjectNotFound},
	}
	for _, k := range casos {
		if err := st.ProposeTransfer(ctx, k.ator, k.projeto, k.alvo); !errors.Is(err, k.quer) {
			t.Errorf("%s: %v, queria %v", k.nome, err, k.quer)
		}
		if p := pendenteDe(t, c.pa); p != nil {
			t.Fatalf("%s gravou a proposta para %s", k.nome, p)
		}
	}

	propor(t, st, c.a, c.pa, c.b)
	if p := pendenteDe(t, c.pa); p == nil || *p != c.b {
		t.Errorf("pendente = %v, queria B", p)
	}
	// Uma proposta nova substitui a anterior.
	e := novoUsuario(t, pool)
	testdb.Membro(t, c.pa, e, "owner")
	propor(t, st, c.a, c.pa, e)
	if p := pendenteDe(t, c.pa); p == nil || *p != e {
		t.Errorf("pendente depois da segunda proposta = %v, queria E", p)
	}
}

// O Projeto padrão não é transferido. Esta é a barreira do domínio; a do
// banco está em TestDefaultProjectTransferRefusedInDB. Pode ser revisto pela
// D4 do PR 7.
func TestDefaultProjectTransferRefusedInDomain(t *testing.T) {
	pool := testdb.New(t)
	c := novaCessao(t, pool)
	padraoA := testdb.ProjetoPadrao(t, c.a)
	testdb.Membro(t, padraoA, c.b, "owner")

	err := pgstore.New(pool).ProposeTransfer(context.Background(), c.a, padraoA, c.b)
	if !errors.Is(err, tenancy.ErrTransferTarget) {
		t.Errorf("propor o Projeto padrão: %v, queria ErrTransferTarget", err)
	}
	if p := pendenteDe(t, padraoA); p != nil {
		t.Errorf("o Projeto padrão ficou com proposta para %s", p)
	}
}

// A segunda barreira: mesmo pulando o Go, o titular não grava proposta no
// Projeto padrão (CHECK projects_padrao_nao_transfere, 23514).
func TestDefaultProjectTransferRefusedInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	padraoA := testdb.ProjetoPadrao(t, c.a)
	testdb.Membro(t, padraoA, c.b, "owner")

	err := storage.InProjectPgxTx(ctx, pool, c.a, padraoA, func(tx pgx.Tx, _ tenancy.ProjectScope) error {
		_, err := tx.Exec(ctx, `UPDATE projects SET pending_titular_id = $1 WHERE id = $2`, c.b, padraoA)
		return err
	})
	if codigoPG(err) != "23514" {
		t.Errorf("proposta no Projeto padrão pelo SQL: %v, queria 23514", err)
	}
}

func TestAcceptMovesTitularAndQuota(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)
	padraoA := testdb.ProjetoPadrao(t, c.a)
	encher(t, encherAlvos, c.a, c.pa, 2)
	encher(t, encherAlvos, c.a, padraoA, 1)
	encher(t, encherAlvos, c.b, c.padraoB, 3)

	propor(t, st, c.a, c.pa, c.b)
	p, err := st.AcceptTransfer(ctx, c.b, c.pa)
	if err != nil {
		t.Fatalf("aceitar: %v", err)
	}
	if p.ID != c.pa || p.Role != tenancy.RoleOwner || p.PendingTitular || p.Name != "Projeto A" || p.Limites != tenancy.LimitesDe(false) {
		t.Errorf("resumo do aceite = %+v", p)
	}
	if got := titularDe(t, c.pa); got != c.b {
		t.Errorf("titular = %s, queria B", got)
	}
	if pd := pendenteDe(t, c.pa); pd != nil {
		t.Errorf("a proposta ficou depois do aceite: %s", pd)
	}

	alvosDoTitular := func(quem, projeto uuid.UUID) int64 {
		var n int64
		err := storage.InProjectPgxTx(ctx, pool, quem, projeto, func(tx pgx.Tx, _ tenancy.ProjectScope) error {
			return tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1)`, projeto).Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := alvosDoTitular(c.b, c.padraoB); n != 5 {
		t.Errorf("alvos do novo titular = %d, queria 5 (3 dele + 2 de PA)", n)
	}
	if n := alvosDoTitular(c.a, padraoA); n != 1 {
		t.Errorf("alvos do antigo titular = %d, queria 1 (PA saiu da conta dele)", n)
	}

	// O antigo continua owner e agora pode sair.
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'owner'`, c.pa, c.a); n != 1 {
		t.Errorf("o antigo titular deixou de ser owner")
	}
	if err := st.RemoveMember(ctx, c.a, c.pa, c.a); err != nil {
		t.Errorf("o antigo titular saindo: %v", err)
	}
	if err := st.RemoveMember(ctx, c.b, c.pa, c.b); !errors.Is(err, tenancy.ErrTitularProtected) {
		t.Errorf("o novo titular saindo: %v, queria ErrTitularProtected", err)
	}
}

func TestOnlyPendingAcceptsTransfer(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)
	e := novoUsuario(t, pool)
	testdb.Membro(t, c.pa, e, "owner")

	if _, err := st.AcceptTransfer(ctx, c.b, c.pa); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("aceitar sem proposta: %v, queria ErrForbidden", err)
	}
	propor(t, st, c.a, c.pa, c.b)
	for nome, quem := range map[string]uuid.UUID{"outro owner": e, "o titular": c.a, "o manager": c.c} {
		if _, err := st.AcceptTransfer(ctx, quem, c.pa); !errors.Is(err, tenancy.ErrForbidden) {
			t.Errorf("%s aceitando: %v, queria ErrForbidden", nome, err)
		}
	}
	if _, err := st.AcceptTransfer(ctx, novoUsuario(t, pool), c.pa); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("quem não é membro aceitando: %v, queria ErrProjectNotFound", err)
	}
	if got := titularDe(t, c.pa); got != c.a {
		t.Errorf("titular = %s depois das recusas, queria A", got)
	}
}

func TestCancelTransferByTitularOrPending(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)
	e := novoUsuario(t, pool)
	testdb.Membro(t, c.pa, e, "owner")

	propor(t, st, c.a, c.pa, c.b)
	for nome, quem := range map[string]uuid.UUID{"outro owner": e, "o manager": c.c, "o viewer": c.d} {
		if err := st.CancelTransfer(ctx, quem, c.pa); !errors.Is(err, tenancy.ErrForbidden) {
			t.Errorf("%s cancelando: %v, queria ErrForbidden", nome, err)
		}
	}
	if err := st.CancelTransfer(ctx, novoUsuario(t, pool), c.pa); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("quem não é membro cancelando: %v, queria ErrProjectNotFound", err)
	}
	if p := pendenteDe(t, c.pa); p == nil {
		t.Fatal("a proposta sumiu com as recusas")
	}

	// O titular cancela.
	if err := st.CancelTransfer(ctx, c.a, c.pa); err != nil {
		t.Fatalf("o titular cancelando: %v", err)
	}
	if p := pendenteDe(t, c.pa); p != nil {
		t.Errorf("a proposta ficou depois do cancelamento: %s", p)
	}
	if err := st.CancelTransfer(ctx, c.a, c.pa); err != nil {
		t.Errorf("o titular cancelando sem proposta: %v, queria nil", err)
	}
}

func TestPendingRefusesTransfer(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)

	propor(t, st, c.a, c.pa, c.b)
	if err := st.CancelTransfer(ctx, c.b, c.pa); err != nil {
		t.Fatalf("o pendente recusando: %v", err)
	}
	if p := pendenteDe(t, c.pa); p != nil {
		t.Errorf("a proposta ficou depois da recusa: %s", p)
	}
	if _, err := st.AcceptTransfer(ctx, c.b, c.pa); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("aceitar depois de recusar: %v, queria ErrForbidden", err)
	}
	if got := titularDe(t, c.pa); got != c.a {
		t.Errorf("titular = %s, queria A", got)
	}
}

func TestAcceptRefusedOverReceiverLimits(t *testing.T) {
	casos := []struct {
		nome string
		// montar enche a cota de B (ou o projeto) até o aceite estourar.
		montar func(t *testing.T, pool *pgxpool.Pool, c cessao)
		quer   error
	}{
		{"projetos", func(t *testing.T, _ *pgxpool.Pool, c cessao) {
			testdb.NovoProjeto(t, c.b, "B2")
			testdb.NovoProjeto(t, c.b, "B3")
		}, tenancy.ErrProjectLimit},
		{"alvos", func(t *testing.T, _ *pgxpool.Pool, c cessao) {
			encher(t, encherAlvos, c.b, c.padraoB, targets.MaxTargetsPerUser)
			encher(t, encherAlvos, c.a, c.pa, 1)
		}, targets.ErrLimitReached},
		{"heartbeats", func(t *testing.T, _ *pgxpool.Pool, c cessao) {
			encher(t, encherHeartbeats, c.b, c.padraoB, heartbeat.MaxPerUser-1)
			encher(t, encherHeartbeats, c.a, c.pa, 2)
		}, heartbeat.ErrLimitReached},
		{"canais", func(t *testing.T, _ *pgxpool.Pool, c cessao) {
			encher(t, encherCanais, c.b, c.padraoB, notify.MaxChannels)
			encher(t, encherCanais, c.a, c.pa, 1)
		}, notify.ErrLimitReached},
		// A, operador, tem folga de membros; B não tem: PA com A, C, D e E
		// além de B passaria do teto de B (3).
		{"membros", func(t *testing.T, pool *pgxpool.Pool, c cessao) {
			testdb.Operador(t, c.a)
			testdb.Membro(t, c.pa, novoUsuario(t, pool), "member")
		}, tenancy.ErrMemberLimit},
	}
	for _, k := range casos {
		t.Run(k.nome, func(t *testing.T) {
			pool := testdb.New(t)
			c := novaCessao(t, pool)
			st := pgstore.New(pool)
			k.montar(t, pool, c)
			propor(t, st, c.a, c.pa, c.b)

			if _, err := st.AcceptTransfer(context.Background(), c.b, c.pa); !errors.Is(err, k.quer) {
				t.Errorf("aceite acima do teto de %s: %v, queria %v", k.nome, err, k.quer)
			}
			if got := titularDe(t, c.pa); got != c.a {
				t.Errorf("titular = %s depois da recusa, queria A", got)
			}
			if p := pendenteDe(t, c.pa); p == nil || *p != c.b {
				t.Errorf("a recusa por cota mexeu na proposta: %v", p)
			}
		})
	}
}

// No limite exato o aceite passa: as contagens já somam o projeto.
func TestAcceptAtExactLimitPasses(t *testing.T) {
	pool := testdb.New(t)
	c := novaCessao(t, pool)
	st := pgstore.New(pool)
	testdb.NovoProjeto(t, c.b, "B2") // B fica com 3 contando PA
	encher(t, encherAlvos, c.b, c.padraoB, targets.MaxTargetsPerUser-1)
	encher(t, encherAlvos, c.a, c.pa, 1)
	propor(t, st, c.a, c.pa, c.b)

	if _, err := st.AcceptTransfer(context.Background(), c.b, c.pa); err != nil {
		t.Errorf("aceite no limite exato: %v", err)
	}
}

// Rebaixar ou remover o pendente apaga a proposta (a limpeza vai na mesma
// transação, sob a trava dos membros, como a dos convites).
func TestDemotingPendingClearsProposal(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	st := pgstore.New(pool)

	propor(t, st, c.a, c.pa, c.b)
	if _, err := st.ChangeRole(ctx, c.a, c.pa, c.b, tenancy.RoleManager); err != nil {
		t.Fatal(err)
	}
	if p := pendenteDe(t, c.pa); p != nil {
		t.Errorf("rebaixar o pendente deixou a proposta: %s", p)
	}

	if _, err := st.ChangeRole(ctx, c.a, c.pa, c.b, tenancy.RoleOwner); err != nil {
		t.Fatal(err)
	}
	propor(t, st, c.a, c.pa, c.b)
	// Mudar o papel de outro membro não mexe na proposta.
	if _, err := st.ChangeRole(ctx, c.a, c.pa, c.d, tenancy.RoleMember); err != nil {
		t.Fatal(err)
	}
	if p := pendenteDe(t, c.pa); p == nil {
		t.Fatal("mudar o papel de outro membro apagou a proposta")
	}
	if err := st.RemoveMember(ctx, c.a, c.pa, c.b); err != nil {
		t.Fatal(err)
	}
	if p := pendenteDe(t, c.pa); p != nil {
		t.Errorf("remover o pendente deixou a proposta: %s", p)
	}

	// Sair também.
	testdb.Membro(t, c.pa, c.b, "owner")
	propor(t, st, c.a, c.pa, c.b)
	if err := st.RemoveMember(ctx, c.b, c.pa, c.b); err != nil {
		t.Fatal(err)
	}
	if p := pendenteDe(t, c.pa); p != nil {
		t.Errorf("o pendente saindo deixou a proposta: %s", p)
	}
}

// O gatilho guard_projects_imutavel só aceita trocar o titular no aceite: o
// titular novo é o pendente, é quem age, e a proposta some na mesma UPDATE.
// Só o titular grava proposta.
func TestTitularIDChangeOutsideAcceptRefusedInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := novaCessao(t, pool)
	e := novoUsuario(t, pool)
	testdb.Membro(t, c.pa, e, "owner")
	admin := testdb.Admin(t)
	// A proposta é gravada por fora da RLS, mas o gatilho vale para o
	// superusuário: grava como o titular.
	pendente := func(id *uuid.UUID) {
		t.Helper()
		err := pgx.BeginFunc(ctx, admin, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1::uuid::text, true)`, c.a); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE projects SET pending_titular_id = $1 WHERE id = $2`, id, c.pa)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	como := func(quem uuid.UUID, sql string, args ...any) error {
		return storage.InProjectPgxTx(ctx, pool, quem, c.pa, func(tx pgx.Tx, _ tenancy.ProjectScope) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
	}

	casos := []struct {
		nome     string
		pendente *uuid.UUID
		quem     uuid.UUID
		sql      string
		args     []any
	}{
		{"o titular dá o projeto a um owner sem proposta", nil, c.a,
			`UPDATE projects SET titular_id = $1 WHERE id = $2`, []any{c.b, c.pa}},
		{"um owner toma o projeto sem proposta", nil, c.b,
			`UPDATE projects SET titular_id = $1 WHERE id = $2`, []any{c.b, c.pa}},
		{"o titular troca pelo pendente", &c.b, c.a,
			`UPDATE projects SET titular_id = $1, pending_titular_id = NULL WHERE id = $2`, []any{c.b, c.pa}},
		{"outro owner toma o projeto proposto a B", &c.b, e,
			`UPDATE projects SET titular_id = $1, pending_titular_id = NULL WHERE id = $2`, []any{e, c.pa}},
		{"o pendente aceita sem limpar a proposta", &c.b, c.b,
			`UPDATE projects SET titular_id = $1 WHERE id = $2`, []any{c.b, c.pa}},
		{"um owner propõe a si mesmo", nil, c.b,
			`UPDATE projects SET pending_titular_id = $1 WHERE id = $2`, []any{c.b, c.pa}},
		{"um owner troca a proposta", &c.b, e,
			`UPDATE projects SET pending_titular_id = $1 WHERE id = $2`, []any{e, c.pa}},
	}
	for _, k := range casos {
		pendente(k.pendente)
		if err := como(k.quem, k.sql, k.args...); codigoPG(err) != "42501" {
			t.Errorf("%s: %v, queria 42501", k.nome, err)
		}
		if got := titularDe(t, c.pa); got != c.a {
			t.Fatalf("%s trocou o titular para %s", k.nome, got)
		}
	}

	// O aceite em si passa pelo gatilho.
	pendente(&c.b)
	if err := como(c.b, `UPDATE projects SET titular_id = $1, pending_titular_id = NULL WHERE id = $2`, c.b, c.pa); err != nil {
		t.Errorf("o aceite pelo SQL: %v", err)
	}
	if got := titularDe(t, c.pa); got != c.b {
		t.Errorf("titular = %s depois do aceite, queria B", got)
	}
}

// esperarTrava espera o aceite parar numa trava (advisory ou de linha) e
// falha se ele terminar antes: a trava não foi tomada.
func esperarTrava(t *testing.T, pool *pgxpool.Pool, oQue string, fim chan error) {
	t.Helper()
	if !testdb.EsperarBloqueados(t, pool, 1, fim) {
		t.Fatalf("o aceite terminou sem esperar %s: %v", oQue, <-fim)
	}
}

func aceitarAoFundo(st *pgstore.Store, receptor, projeto uuid.UUID) chan error {
	fim := make(chan error, 1)
	go func() {
		_, err := st.AcceptTransfer(context.Background(), receptor, projeto)
		fim <- err
	}()
	return fim
}

func resultado(t *testing.T, fim chan error) error {
	t.Helper()
	select {
	case err := <-fim:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("o aceite não terminou")
		return nil
	}
}

// O aceite e uma criação simultânea do receptor não passam juntos do teto: o
// aceite toma as travas de cota do receptor (projetos, alvos, heartbeats,
// canais) antes de contar, e uma criação em voo no próprio projeto termina
// antes de o aceite contar (ProjectTitular FOR KEY SHARE contra o FOR UPDATE
// de ProjectTransferState).
func TestConcurrentAcceptAndCreateRespectQuota(t *testing.T) {
	travas := []struct{ nome, chave string }{
		{"projetos", "stillon.projects:"},
		{"alvos", "stillon.targets:"},
		{"heartbeats", "stillon.heartbeats:"},
		{"canais", "stillon.channels:"},
	}
	for _, tr := range travas {
		t.Run("trava de "+tr.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()
			c := novaCessao(t, pool)
			st := pgstore.New(pool)
			propor(t, st, c.a, c.pa, c.b)

			tx, err := testdb.Admin(t).Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1 || $2::uuid::text, 0))`, tr.chave, c.b); err != nil {
				t.Fatal(err)
			}
			fim := aceitarAoFundo(st, c.b, c.pa)
			esperarTrava(t, pool, "a trava "+tr.chave+"<receptor>", fim)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := resultado(t, fim); err != nil {
				t.Errorf("aceite depois de soltar a trava: %v", err)
			}
		})
	}

	// Corrida de verdade na última vaga de projeto: o aceite e a criação de
	// um projeto pelo receptor. Exatamente um passa.
	t.Run("última vaga de projeto", func(t *testing.T) {
		pool := testdb.New(t)
		ctx := context.Background()
		c := novaCessao(t, pool)
		st := pgstore.New(pool)
		testdb.NovoProjeto(t, c.b, "B2") // B com 2: sobra uma vaga
		propor(t, st, c.a, c.pa, c.b)

		tx, err := testdb.Admin(t).Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stillon.projects:' || $1::uuid::text, 0))`, c.b); err != nil {
			t.Fatal(err)
		}
		fimAceite := aceitarAoFundo(st, c.b, c.pa)
		fimCriacao := make(chan error, 1)
		go func() {
			_, err := st.Create(ctx, tenancy.NewProject{ID: uuid.Must(uuid.NewV7()), Name: "B3", TitularID: c.b, Now: time.Now()})
			fimCriacao <- err
		}()
		if !testdb.EsperarBloqueados(t, pool, 2, fimAceite) {
			t.Fatalf("o aceite não esperou a trava de projetos do receptor: %v", <-fimAceite)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		errAceite, errCriacao := resultado(t, fimAceite), resultado(t, fimCriacao)
		passaram := 0
		for _, err := range []error{errAceite, errCriacao} {
			switch {
			case err == nil:
				passaram++
			case !errors.Is(err, tenancy.ErrProjectLimit):
				t.Errorf("erro inesperado: %v", err)
			}
		}
		if passaram != 1 {
			t.Errorf("passaram %d (aceite: %v, criação: %v), queria 1", passaram, errAceite, errCriacao)
		}
		if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, c.b); n != 3 {
			t.Errorf("B ficou com %d projetos, queria 3", n)
		}
	})

	// Uma criação de alvo em voo no próprio projeto: ela leu o titular antigo
	// (FOR KEY SHARE) e grava 2 alvos antes do commit. O aceite espera e os
	// conta: com B a 1 do teto, recusa.
	t.Run("criação em voo no projeto", func(t *testing.T) {
		pool := testdb.New(t)
		ctx := context.Background()
		c := novaCessao(t, pool)
		st := pgstore.New(pool)
		encher(t, encherAlvos, c.b, c.padraoB, targets.MaxTargetsPerUser-1)
		propor(t, st, c.a, c.pa, c.b)

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		// O caminho de InProjectTx com a transação aberta: escopo de C (um
		// manager de PA) no projeto, e o ProjectTitular de quem cria.
		q := db.New(tx)
		if err := q.ScopeToUser(ctx, c.c); err != nil {
			t.Fatal(err)
		}
		if err := q.ScopeToProject(ctx, c.pa); err != nil {
			t.Fatal(err)
		}
		if _, err := q.ProjectTitular(ctx, c.pa); err != nil {
			t.Fatal(err)
		}

		// Só com o titular lido, ainda sem INSERT (que já travaria a linha
		// pela FK), o aceite espera: senão contaria antes dos alvos novos, e a
		// criação os gravaria contados para o titular antigo.
		fim := aceitarAoFundo(st, c.b, c.pa)
		esperarTrava(t, pool, "a criação em voo no projeto", fim)
		if _, err := tx.Exec(ctx, encherAlvos, c.c, c.pa, 2); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := resultado(t, fim); !errors.Is(err, targets.ErrLimitReached) {
			t.Errorf("aceite com a criação em voo: %v, queria targets.ErrLimitReached", err)
		}
	})
}
