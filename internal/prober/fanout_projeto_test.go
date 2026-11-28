package prober

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Fan-out pelo projeto do recurso (spec §8): o aviso de um incidente vai aos
// canais do projeto do alvo, e não a todos os canais de quem criou o alvo.
// Fan-out por pessoa vaza entre projetos do mesmo usuário e, com membros,
// entre inquilinos. Os dados são gravados e relidos por fora da RLS (Admin):
// o alvo e o canal de um membro não aparecem no escopo de outra pessoa.

// aviso é uma linha de notifications: o canal e o evento.
type aviso struct {
	canal  uuid.UUID
	evento string
}

func (a aviso) String() string { return a.evento + "@" + a.canal.String() }

// notificacoes lê todas as notificações, de todos os canais, por fora da RLS,
// em ordem de id (a de enfileiramento).
func notificacoes(t *testing.T) []aviso {
	t.Helper()
	rows, err := testdb.Admin(t).Query(context.Background(), `SELECT channel_id, event FROM notifications ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []aviso
	for rows.Next() {
		var a aviso
		if err := rows.Scan(&a.canal, &a.evento); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// projetoOuNulo traduz uuid.Nil em NULL: o recurso legado, sem projeto.
func projetoOuNulo(projeto uuid.UUID) *uuid.UUID {
	if projeto == uuid.Nil {
		return nil
	}
	return &projeto
}

// alvoNoProjeto grava, por fora da RLS, um alvo habilitado do criador no
// projeto (uuid.Nil grava NULL; o gatilho só o preenche se o criador tiver
// Projeto padrão) e devolve a linha como o sondador a reivindicaria.
func alvoNoProjeto(t *testing.T, projeto, criador uuid.UUID) db.ClaimDueTargetsRow {
	t.Helper()
	a := db.ClaimDueTargetsRow{
		ID: uuid.Must(uuid.NewV7()), ProjectID: projetoOuNulo(projeto), URL: "https://exemplo.com", Method: "GET", IntervalSec: 60,
	}
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		VALUES ($1, $2, $3, 'alvo', $4, $5, $6, 5000, 200, true, now(), now())`,
		a.ID, criador, projetoOuNulo(projeto), a.URL, a.Method, a.IntervalSec); err != nil {
		t.Fatalf("criar alvo no projeto %s: %v", projeto, err)
	}
	return a
}

// canalNoProjeto grava, por fora da RLS, um canal habilitado e verificado do
// criador no projeto (uuid.Nil grava NULL, como em alvoNoProjeto).
func canalNoProjeto(t *testing.T, projeto, criador uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, enabled, created_at)
		VALUES ($1, $2, $3, 'discord', '\x01', now(), true, now())`,
		id, criador, projetoOuNulo(projeto)); err != nil {
		t.Fatalf("criar canal no projeto %s: %v", projeto, err)
	}
	return id
}

// usuarioDeTeste cria um usuário sem projeto nenhum (users não tem RLS).
func usuarioDeTeste(t *testing.T, b *bancada) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := b.pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// abrirAlvoAlheio é o abrirPeloAgendador para um alvo que o dono da bancada
// não enxerga (de outro criador, ou sem projeto): três falhas pelo agendador,
// e o incidente conferido por fora da RLS.
func abrirAlvoAlheio(t *testing.T, b *bancada, a db.ClaimDueTargetsRow) {
	t.Helper()
	t0 := b.clk.Now()
	for i := range 3 {
		b.s.gravar(a, t0.Add(time.Duration(i)*time.Minute), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}
	var abertos int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM incidents WHERE target_id = $1 AND resolved_at IS NULL`, a.ID).Scan(&abertos); err != nil {
		t.Fatal(err)
	}
	if abertos != 1 {
		t.Fatalf("três down: %d incidentes abertos, queria 1", abertos)
	}
}

func TestIncidenteDeAlvoNoProjetoANaoAvisaCanalDoProjetoB(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	u := b.dono
	projetoA := testdb.ProjetoPadrao(t, u)
	projetoB := testdb.NovoProjeto(t, u, "B")
	canalA := canalNoProjeto(t, projetoA, u)
	canalNoProjeto(t, projetoB, u)
	a := alvoNoProjeto(t, projetoA, u)

	_, terceira := b.abrirPeloAgendador(t, a)
	if ns, quer := notificacoes(t), []aviso{{canalA, incident.EventOpened}}; !slices.Equal(ns, quer) {
		t.Fatalf("abertura: notificações = %v; queria %v (só o canal do projeto A)", ns, quer)
	}

	b.fecharPeloAgendador(t, a, terceira.Add(time.Minute))
	quer := []aviso{{canalA, incident.EventOpened}, {canalA, incident.EventResolved}}
	if ns := notificacoes(t); !slices.Equal(ns, quer) {
		t.Errorf("fechamento: notificações = %v; queria %v (só o canal do projeto A)", ns, quer)
	}
}

func TestCanalCriadoPorMembroNaoTitularRecebeAvisoDoProjeto(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	u := b.dono
	projetoA := testdb.ProjetoPadrao(t, u)
	m := usuarioDeTeste(t, b)
	testdb.Membro(t, projetoA, m, "manager")
	canalM := canalNoProjeto(t, projetoA, m)
	a := alvoNoProjeto(t, projetoA, u)

	b.abrirPeloAgendador(t, a)

	if ns, quer := notificacoes(t), []aviso{{canalM, incident.EventOpened}}; !slices.Equal(ns, quer) {
		t.Errorf("notificações = %v; queria %v (o canal do membro no projeto do alvo)", ns, quer)
	}
}

func TestAlvoCriadoPorMembroNaoAvisaOutrosProjetosDoMembro(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	u := b.dono
	projetoA := testdb.ProjetoPadrao(t, u)
	m := usuarioDeTeste(t, b)
	testdb.Membro(t, projetoA, m, "manager")
	canalA := canalNoProjeto(t, projetoA, u)
	canalNoProjeto(t, testdb.ProjetoPadrao(t, m), m) // cPM: projeto alheio a A
	a := alvoNoProjeto(t, projetoA, m)

	abrirAlvoAlheio(t, b, a)

	if ns, quer := notificacoes(t), []aviso{{canalA, incident.EventOpened}}; !slices.Equal(ns, quer) {
		t.Errorf("notificações = %v; queria %v (só o canal de A, nenhum do projeto próprio do membro)", ns, quer)
	}
}

// Fallback legado (sai no PR 7, com o NOT NULL): recurso ou canal sem
// project_id casa por user_id, a regra antiga.
func TestFanoutDeRecursoSemProjetoCaiNoCriador(t *testing.T) {
	t.Run("alvo e canal sem projeto", func(t *testing.T) {
		b := novaBancada(t, nil, MaxInFlight)
		l := usuarioDeTeste(t, b) // sem Projeto padrão: o gatilho deixa NULL
		canal := canalNoProjeto(t, uuid.Nil, l)
		a := alvoNoProjeto(t, uuid.Nil, l)
		// O canal legado de outra pessoa não casa: o fallback é por user_id.
		alheio := canalNoProjeto(t, uuid.Nil, usuarioDeTeste(t, b))
		conferirSemProjeto(t, "targets", a.ID)
		conferirSemProjeto(t, "notif_channels", canal)
		conferirSemProjeto(t, "notif_channels", alheio)

		abrirAlvoAlheio(t, b, a)

		if ns, quer := notificacoes(t), []aviso{{canal, incident.EventOpened}}; !slices.Equal(ns, quer) {
			t.Errorf("notificações = %v; queria %v (o canal do criador)", ns, quer)
		}
	})
	t.Run("canal em projeto e alvo sem projeto", func(t *testing.T) {
		b := novaBancada(t, nil, MaxInFlight)
		l := usuarioDeTeste(t, b)
		canal := canalNoProjeto(t, testdb.NovoProjeto(t, l, "X"), l)
		a := alvoNoProjeto(t, uuid.Nil, l)
		conferirSemProjeto(t, "targets", a.ID)

		abrirAlvoAlheio(t, b, a)

		if ns, quer := notificacoes(t), []aviso{{canal, incident.EventOpened}}; !slices.Equal(ns, quer) {
			t.Errorf("notificações = %v; queria %v (o canal do criador)", ns, quer)
		}
	})
}

// conferirSemProjeto falha se a linha da tabela tem project_id.
func conferirSemProjeto(t *testing.T, tabela string, id uuid.UUID) {
	t.Helper()
	var projeto *uuid.UUID
	// tabela é sempre um literal do próprio teste.
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id FROM `+tabela+` WHERE id = $1`, id).Scan(&projeto); err != nil {
		t.Fatal(err)
	}
	if projeto != nil {
		t.Fatalf("%s %s tem project_id %s; queria NULL", tabela, id, *projeto)
	}
}

// A guarda do banco (notifications_canal_do_projeto) descarta a notificação
// num canal fora do projeto do recurso sem abortar: o enfileiramento roda na
// transação do check, e abortar desfaria o check e o incidente e pararia o
// monitor. O enfileirador de teste grava direto em cB, o erro que o fan-out
// não comete mais.
func TestGuardaNaoDesfazOCheckNemOIncidente(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	u := b.dono
	projetoA := testdb.ProjetoPadrao(t, u)
	cB := canalNoProjeto(t, testdb.NovoProjeto(t, u, "B"), u)
	a := alvoNoProjeto(t, projetoA, u)
	// Contado: sem a troca, o fan-out real não acharia canal em A e o teste
	// passaria sem gravar nada em cB.
	var gravadas int
	b.s.avisos = func(ctx context.Context, q *db.Queries, incidente uuid.UUID, evento string, em time.Time) error {
		gravadas++
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: uuid.Must(uuid.NewV7()), IncidentID: incidente, ChannelID: cB, Event: evento, Now: em,
		})
	}

	abrirAlvoAlheio(t, b, a) // confere, por fora da RLS, o incidente aberto

	if gravadas != 1 {
		t.Fatalf("o enfileirador de teste rodou %d vezes, queria 1 (a abertura em cB)", gravadas)
	}

	var checks int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM checks WHERE target_id = $1`, a.ID).Scan(&checks); err != nil {
		t.Fatal(err)
	}
	if checks != 3 {
		t.Errorf("checks = %d, queria 3 (a guarda não desfaz a transação do check)", checks)
	}
	if ns := notificacoes(t); len(ns) != 0 {
		t.Errorf("notifications = %d, queria 0 (%v no canal de B)", len(ns), ns)
	}
}
