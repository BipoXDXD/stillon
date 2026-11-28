package storage_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Suspensão derivada (spec §Cotas e abuso): enquanto o titular estiver
// suspenso, o projeto some dos workers; nada é apagado. Cada teste tem um
// titular ativo (V) ao lado do suspenso (U), para que o vazio de U não passe
// por um worker que não reivindica nada.

func idsDoLote(lote []db.ClaimDueTargetsRow) []uuid.UUID {
	var ids []uuid.UUID
	for _, a := range lote {
		ids = append(ids, a.ID)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return ids
}

func ordenados(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return out
}

func contarVencidos(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		n, err = q.CountDueTargets(ctx, agora)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSuspendedTitularTargetsNotClaimed(t *testing.T) {
	pool := testdb.New(t)
	u, v := novoUsuario(t, pool), novoUsuario(t, pool)
	alvosVencidosNoProjeto(t, testdb.ProjetoPadrao(t, u), u, 2, agora.Add(-time.Hour), time.Minute)
	deV := alvosVencidosNoProjeto(t, testdb.ProjetoPadrao(t, v), v, 2, agora.Add(-time.Minute), time.Second)
	testdb.Suspender(t, u)

	if n := contarVencidos(t, pool); n != 2 {
		t.Errorf("CountDueTargets = %d; queria 2, só os do titular ativo", n)
	}
	if got, quer := idsDoLote(reivindicar(t, pool, 10)), ordenados(deV...); !slices.Equal(got, quer) {
		t.Errorf("reivindicou %v; queria só os do titular ativo %v", got, quer)
	}
}

// A suspensão é do titular do projeto, não de quem criou o alvo: o membro
// suspenso não para o projeto de U, e U suspenso para o alvo que o membro
// criou.
func TestMemberProjectOfSuspendedTitularNotClaimed(t *testing.T) {
	pool := testdb.New(t)
	u, m := novoUsuario(t, pool), novoUsuario(t, pool)
	projetoU := testdb.ProjetoPadrao(t, u)
	testdb.Membro(t, projetoU, m, "member")

	primeiro := alvosVencidosNoProjeto(t, projetoU, m, 1, agora.Add(-time.Minute), 0)
	testdb.Suspender(t, m)
	if got := idsDoLote(reivindicar(t, pool, 10)); !slices.Equal(got, primeiro) {
		t.Fatalf("com só o membro suspenso, reivindicou %v; queria o alvo dele %v", got, primeiro)
	}

	alvosVencidosNoProjeto(t, projetoU, m, 1, agora.Add(-time.Minute), 0)
	testdb.Suspender(t, u)
	if got := reivindicar(t, pool, 10); len(got) != 0 {
		t.Errorf("com o titular suspenso, reivindicou %v; queria nada", idsDoLote(got))
	}
}

// heartbeatVencido grava, por fora da RLS, um heartbeat do titular com o
// prazo vencido há um minuto.
func heartbeatVencido(t *testing.T, titular uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	id := novoID()
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO heartbeats (id, user_id, project_id, name, ping_token_hash, period_sec, grace_sec,
		                        enabled, created_at, deadline_at)
		VALUES ($1, $2, $3, $4, $5, 3600, 300, true, $6, $7)`,
		id, titular, testdb.ProjetoPadrao(t, titular), nome, hashDe(nome+id.String()), agora.Add(-2*time.Hour),
		agora.Add(-time.Minute)); err != nil {
		t.Fatalf("criar heartbeat vencido: %v", err)
	}
	return id
}

// heartbeatRecuperado grava um heartbeat com incidente aberto (de agora) e um
// sucesso depois dele, com o prazo novo no futuro: o avaliador o fecharia.
func heartbeatRecuperado(t *testing.T, pool *pgxpool.Pool, titular uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	id := heartbeatVencido(t, titular, nome)
	novoIncidenteDeHeartbeat(t, pool, id)
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		UPDATE heartbeats SET last_success_at = $2, deadline_at = $3 WHERE id = $1`,
		id, agora.Add(time.Second), agora.Add(time.Hour)); err != nil {
		t.Fatalf("registrar o sucesso do heartbeat: %v", err)
	}
	return id
}

func TestSuspendedTitularHeartbeatsNotEvaluated(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, v := novoUsuario(t, pool), novoUsuario(t, pool)
	heartbeatVencido(t, u, "u-atrasado")
	vAtrasado := heartbeatVencido(t, v, "v-atrasado")
	heartbeatRecuperado(t, pool, u, "u-recuperado")
	vRecuperado := heartbeatRecuperado(t, pool, v, "v-recuperado")
	testdb.Suspender(t, u)

	var abrir []db.ClaimHeartbeatsToOpenRow
	var fechar []db.ClaimHeartbeatsToCloseRow
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		abrir, err = q.ClaimHeartbeatsToOpen(ctx, db.ClaimHeartbeatsToOpenParams{
			Now: agora.Add(2 * time.Second), CooldownCut: agora.Add(2 * time.Second), MaxRows: 10,
		})
		if err != nil {
			return err
		}
		fechar, err = q.ClaimHeartbeatsToClose(ctx, db.ClaimHeartbeatsToCloseParams{Now: agora.Add(2 * time.Second), MaxRows: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if len(abrir) != 1 || abrir[0].ID != vAtrasado {
		t.Errorf("ClaimHeartbeatsToOpen trouxe %+v; queria só o do titular ativo %v", abrir, vAtrasado)
	}
	if len(fechar) != 1 || fechar[0].ID != vRecuperado {
		t.Errorf("ClaimHeartbeatsToClose trouxe %+v; queria só o do titular ativo %v", fechar, vRecuperado)
	}
}

// incidenteAvisado monta, para o titular, um alvo com incidente aberto cuja
// abertura já foi avisada, um canal verificado e a notificação de abertura
// pendente nele.
func incidenteAvisado(t *testing.T, pool *pgxpool.Pool, titular uuid.UUID) (incidente, canal, notificacao uuid.UUID) {
	t.Helper()
	incidente = novoIncidente(t, pool, novoAlvo(t, pool, titular, agora.Add(time.Hour)))
	canal = novoCanal(t, pool, titular, nil, time.Time{})
	notificacao = novaNotificacao(t, pool, incidente, canal)
	marcarAberturaAvisada(t, pool, incidente)
	return incidente, canal, notificacao
}

// lembretesDe devolve os incidentes candidatos a lembrete.
func lembretesDe(t *testing.T, pool *pgxpool.Pool) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var linhas []db.ListReminderCandidatesRow
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		linhas, err = q.ListReminderCandidates(ctx, db.ListReminderCandidatesParams{MaxRows: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, l := range linhas {
		ids = append(ids, l.IncidentID)
	}
	return ordenados(ids...)
}

// reivindicarAvisos roda ClaimDueNotifications em agora e devolve os ids do lote.
func reivindicarAvisos(t *testing.T, pool *pgxpool.Pool) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var lote []db.ClaimDueNotificationsRow
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: agora, LeaseUntil: agora.Add(time.Minute), MaxRows: 10,
			Kinds: kindsConhecidos, Events: eventosConhecidos,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, n := range lote {
		ids = append(ids, n.ID)
	}
	return ordenados(ids...)
}

// O fan-out não olha a suspensão: a notificação do titular suspenso é
// gravada e fica pending, sem gastar tentativa, até a suspensão sair (a
// barreira é só ClaimDueNotifications). Os lembretes param durante a
// suspensão e voltam com a reativação.
func TestSuspendedTitularNotificationWaitsUntilUnsuspend(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, v := novoUsuario(t, pool), novoUsuario(t, pool)
	incidenteU := novoIncidente(t, pool, novoAlvo(t, pool, u, agora.Add(time.Hour)))
	canalU := novoCanal(t, pool, u, nil, time.Time{})
	marcarAberturaAvisada(t, pool, incidenteU)
	incidenteV, _, notificacaoV := incidenteAvisado(t, pool, v)
	testdb.Suspender(t, u)

	notificacaoU := uuid.Must(uuid.NewV7())
	var canaisU []uuid.UUID
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		if canaisU, err = q.ListDeliverableChannelsForIncident(ctx, incidenteU); err != nil {
			return err
		}
		for _, c := range canaisU {
			if err := q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
				ID: notificacaoU, IncidentID: incidenteU, ChannelID: c, Event: "opened", Now: agora,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(canaisU, []uuid.UUID{canalU}) {
		t.Fatalf("o fan-out do titular suspenso achou os canais %v; queria %v", canaisU, []uuid.UUID{canalU})
	}

	if l := lembretesDe(t, pool); !slices.Equal(l, []uuid.UUID{incidenteV}) {
		t.Errorf("candidatos a lembrete com U suspenso: %v; queria só o incidente do titular ativo %v", l, incidenteV)
	}
	if lote := reivindicarAvisos(t, pool); !slices.Equal(lote, []uuid.UUID{notificacaoV}) {
		t.Errorf("ClaimDueNotifications com U suspenso trouxe %v; queria só a do titular ativo %v", lote, notificacaoV)
	}
	var status string
	var tentativas int
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT status, attempts FROM notifications WHERE id = $1`, notificacaoU).Scan(&status, &tentativas); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || tentativas != 0 {
		t.Errorf("a notificação do titular suspenso ficou status=%s attempts=%d; queria pending sem gastar tentativa",
			status, tentativas)
	}

	testdb.Reativar(t, u)
	if lote := reivindicarAvisos(t, pool); !slices.Equal(lote, []uuid.UUID{notificacaoU}) {
		t.Errorf("ClaimDueNotifications depois de reativar trouxe %v; queria a do titular reativado %v", lote, notificacaoU)
	}
	if l, queria := lembretesDe(t, pool), ordenados(incidenteU, incidenteV); !slices.Equal(l, queria) {
		t.Errorf("candidatos a lembrete depois de reativar: %v; queria %v", l, queria)
	}
}

// O sondador e a página pública enxergam projects só pelas duas políticas da
// suspensão derivada, ambas de SELECT: nenhuma outra política de projects
// abre a tabela a esses escopos, e nenhum deles escreve nela.
func TestProjectsVisibleToProberOnlyThroughPolicy(t *testing.T) {
	pool := testdb.New(t)
	linhas, err := pool.Query(context.Background(), `
		SELECT pol.polname, pol.polcmd::text
		  FROM pg_policy pol
		 WHERE pol.polrelid = 'projects'::regclass
		   AND (strpos(coalesce(pg_get_expr(pol.polqual, pol.polrelid), '') ||
		               coalesce(pg_get_expr(pol.polwithcheck, pol.polrelid), ''), 'app.scope') > 0
		     OR strpos(coalesce(pg_get_expr(pol.polqual, pol.polrelid), '') ||
		               coalesce(pg_get_expr(pol.polwithcheck, pol.polrelid), ''), 'app.public_slug') > 0)
		 ORDER BY pol.polname`)
	if err != nil {
		t.Fatal(err)
	}
	type politica struct{ nome, cmd string }
	politicas, err := pgx.CollectRows(linhas, func(r pgx.CollectableRow) (politica, error) {
		var p politica
		return p, r.Scan(&p.nome, &p.cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	var nomes []string
	for _, p := range politicas {
		nomes = append(nomes, p.nome)
		if p.cmd != "r" {
			t.Errorf("a política %s de projects com polcmd %q; queria só SELECT ('r')", p.nome, p.cmd)
		}
	}
	if quer := []string{"projects_prober_select", "projects_public_select"}; !slices.Equal(nomes, quer) {
		t.Errorf("políticas de projects que citam app.scope ou app.public_slug: %v; queria exatamente %v", nomes, quer)
	}
}

// O Down da suspensão derivada leva a função e as duas políticas, e o Up sobe
// de novo.
func TestSuspensaoDerivadaMigrationDownReverte(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, "suspensao_derivada")
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	estado := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM pg_policy
			         WHERE polrelid = 'projects'::regclass
			           AND polname IN ('projects_prober_select', 'projects_public_select'))::text
			       || ' / ' ||
			       (SELECT count(*) FROM pg_proc WHERE proname = 'projeto_suspenso')::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s, queria := estado(), "2 / 1"; s != queria {
		t.Fatalf("depois do up: %q; queria %q", s, queria)
	}
	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if s, queria := estado(), "0 / 0"; s != queria {
		t.Errorf("depois do down: %q; queria %q", s, queria)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
	if s, queria := estado(), "2 / 1"; s != queria {
		t.Errorf("depois de subir de novo: %q; queria %q", s, queria)
	}
}
