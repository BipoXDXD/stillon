package prober

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// rodada sonda os alvos vencidos, espera as sondas gravarem e avança o
// relógio além do intervalo (60s ± 10%), para que a próxima rodada os
// encontre vencidos de novo. Esperar emVoo, e não chamar Stop, mantém o
// agendador utilizável entre as rodadas.
func (b *bancada) rodada(t *testing.T) time.Time {
	t.Helper()
	em := b.clk.Now()
	if n, err := b.s.Tick(context.Background()); err != nil || n == 0 {
		t.Fatalf("Tick = %d, %v; queria ao menos um disparo", n, err)
	}
	b.s.emVoo.Wait()
	b.clk.Advance(2 * time.Minute)
	return em
}

// canal cria um canal do dono da bancada. O conteúdo cifrado não importa
// aqui: o sondador só enfileira, quem decifra é o dispatcher.
func (b *bancada) canal(t *testing.T, verificado, habilitado bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	agora := b.clk.Now()
	p := db.InsertChannelParams{ID: id, UserID: b.dono, Kind: "discord", ConfigEnc: []byte{1}, CreatedAt: agora}
	if verificado {
		p.VerifiedAt = &agora
	}
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) })
	if err != nil {
		t.Fatal(err)
	}
	if !habilitado {
		// Não há query de aplicação para desabilitar canal ainda; o teste
		// usa SQL direto, sob o escopo do dono.
		b.comoDono(t, func(tx pgx.Tx) {
			if _, err := tx.Exec(ctx, `UPDATE notif_channels SET enabled = false WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		})
	}
	return id
}

func (b *bancada) comoDono(t *testing.T, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func (b *bancada) incidentes(t *testing.T) []db.ListIncidentsRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListIncidentsRow
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListIncidents(ctx, db.ListIncidentsParams{UserID: b.dono, MaxRows: 100})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

type notificacao struct {
	Canal  uuid.UUID
	Evento string
}

func (b *bancada) notificacoes(t *testing.T) []notificacao {
	t.Helper()
	var out []notificacao
	b.comoDono(t, func(tx pgx.Tx) {
		rows, err := tx.Query(context.Background(), `SELECT channel_id, event FROM notifications ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[notificacao])
		if err != nil {
			t.Fatal(err)
		}
	})
	return out
}

// servidorAlternavel responde 500 enquanto fora for true, e 200 depois.
func servidorAlternavel(t *testing.T) (url string, client *http.Client, fora *atomic.Bool) {
	t.Helper()
	fora = &atomic.Bool{}
	fora.Store(true)
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		if fora.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return srv.URL, client, fora
}

// O caminho inteiro, pelo Tick: três falhas abrem o incidente, com início
// na PRIMEIRA falha, e enfileiram a abertura só no canal verificado e
// habilitado (spec §5.8, §8).
func TestThreeFailuresOpenAnIncidentAndEnqueueTheFanOut(t *testing.T) {
	url, client, _ := servidorAlternavel(t)
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, url, nil)
	entregavel := b.canal(t, true, true)
	b.canal(t, false, true) // pendente de verificação
	b.canal(t, true, false) // desabilitado

	primeira := b.rodada(t)
	b.rodada(t)
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Fatalf("duas falhas abriram %d incidentes", len(inc))
	}
	b.rodada(t)

	inc := b.incidentes(t)
	if len(inc) != 1 {
		t.Fatalf("três falhas abriram %d incidentes, queria 1", len(inc))
	}
	if !inc[0].StartedAt.Equal(primeira) || inc[0].ResolvedAt != nil || inc[0].Cause != "http_status" {
		t.Errorf("incidente = %+v; queria início %v (a primeira falha), aberto, causa http_status", inc[0], primeira)
	}
	ns := b.notificacoes(t)
	if len(ns) != 1 || ns[0].Canal != entregavel || ns[0].Evento != incident.EventOpened {
		t.Errorf("notificações = %+v; queria só a abertura no canal entregável %s", ns, entregavel)
	}

	// Falhas além da terceira não reabrem nem notificam de novo.
	b.rodada(t)
	if inc := b.incidentes(t); len(inc) != 1 {
		t.Errorf("a quarta falha deixou %d incidentes", len(inc))
	}
	if ns := b.notificacoes(t); len(ns) != 1 {
		t.Errorf("a quarta falha deixou %d notificações", len(ns))
	}
}

// Cinco sucessos fecham, com resolved_at no PRIMEIRO deles, e enfileiram o
// fechamento. Quatro não bastam.
func TestFiveSuccessesResolveTheIncident(t *testing.T) {
	url, client, fora := servidorAlternavel(t)
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, url, nil)
	b.canal(t, true, true)
	for range 3 {
		b.rodada(t)
	}

	fora.Store(false)
	voltou := b.rodada(t)
	for range 3 {
		b.rodada(t)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Fatalf("quatro sucessos: incidentes = %+v, queria o mesmo, ainda aberto", inc)
	}
	b.rodada(t)

	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt == nil || !inc[0].ResolvedAt.Equal(voltou) {
		t.Fatalf("cinco sucessos: incidentes = %+v; queria resolvido em %v (o primeiro sucesso)", inc, voltou)
	}
	ns := b.notificacoes(t)
	if len(ns) != 2 || ns[1].Evento != incident.EventResolved {
		t.Errorf("notificações = %+v; queria abertura e fechamento", ns)
	}
}

// Só down conta como falha: degraded é estado de exibição (spec §7.2).
func TestOnlyDownCountsAsFailure(t *testing.T) {
	em := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for status, down := range map[Status]bool{StatusUp: false, StatusDegraded: false, StatusDown: true} {
		if got := paraDetector(em, Result{Status: status}); got.Down != down || !got.At.Equal(em) {
			t.Errorf("%s virou %+v, queria Down=%v", status, got, down)
		}
	}
}

// M4 do Plano 2, pelo caminho de gravação: duas sondas do mesmo alvo em voo
// gravam fora de ordem. O sucesso velho fica no histórico de checks, mas não
// zera a sequência de falhas mais nova.
func TestOutOfOrderCheckDoesNotResetTheFailureStreak(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono}
	falha := Result{Status: StatusDown, ErrorKind: KindTimeout}
	t0 := b.clk.Now()

	b.s.gravar(a, t0, falha)
	b.s.gravar(a, t0.Add(2*time.Minute), falha)
	b.s.gravar(a, t0.Add(time.Minute), Result{Status: StatusUp, StatusCode: 200}) // chegou atrasado
	b.s.gravar(a, t0.Add(3*time.Minute), falha)

	if n := len(b.checks(t, alvo)); n != 4 {
		t.Errorf("%d checks gravados, queria os 4: o atrasado também vai para o histórico", n)
	}
	if inc := b.incidentes(t); len(inc) != 1 || !inc[0].StartedAt.Equal(t0) {
		t.Errorf("incidentes = %+v; queria um, aberto em %v", inc, t0)
	}
}
