package prober

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
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

// Só down conta como falha; degraded conta como sucesso (spec §7.2).
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
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET"}
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

// Uma lacuna maior que três intervalos (alvo pausado, sondador fora do ar)
// recomeça a histerese: as duas falhas de antes não se somam à primeira de
// depois, e o incidente, quando abre, começa na falha pós-lacuna, não uma
// semana atrás.
func TestLongGapRestartsTheHysteresis(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}
	falha := Result{Status: StatusDown, ErrorKind: KindTimeout}
	t0 := b.clk.Now()

	b.s.gravar(a, t0, falha)
	b.s.gravar(a, t0.Add(time.Minute), falha)
	depois := t0.Add(time.Hour)
	b.s.gravar(a, depois, falha)
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Fatalf("a falha depois de uma lacuna de 1h abriu %d incidentes: somou as falhas de antes", len(inc))
	}

	b.s.gravar(a, depois.Add(time.Minute), falha)
	b.s.gravar(a, depois.Add(2*time.Minute), falha)
	inc := b.incidentes(t)
	if len(inc) != 1 || !inc[0].StartedAt.Equal(depois) {
		t.Fatalf("incidentes = %+v; queria um, aberto em %v (a primeira falha depois da lacuna)", inc, depois)
	}
}

// A borda da lacuna (on/off point): três intervalos exatos ainda somam as
// sequências; um segundo a mais recomeça a histerese. Trocar <= por < na
// regra, ou o fator 3 por 2 ou 4, quebra um dos dois casos.
func TestGapBoundaryOfTheHysteresis(t *testing.T) {
	ultimo := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	antes := incident.State{ConsecutiveFailures: 2, StreakSince: ultimo, LastCheckedAt: ultimo}
	const intervaloSec = 60
	casos := []struct {
		nome   string
		lacuna time.Duration
		falhas int32
	}{
		{"três intervalos exatos somam", 3 * time.Minute, 2},
		{"três intervalos e um segundo recomeçam", 3*time.Minute + time.Second, 0},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			depois := recomecarAposLacuna(antes, ultimo.Add(c.lacuna), intervaloSec)
			if depois.ConsecutiveFailures != c.falhas || depois.ConsecutiveSuccesses != 0 ||
				!depois.LastCheckedAt.Equal(ultimo) || !depois.StreakSince.Equal(ultimo) {
				t.Errorf("lacuna de %v: estado = %+v; queria %d falhas e os instantes intactos", c.lacuna, depois, c.falhas)
			}
		})
	}
}

// degraded conta como SUCESSO para a histerese: zera a sequência de falhas
// e fecha o incidente. Só down é falha.
func TestDegradedChecksResolveTheIncident(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}
	t0 := b.clk.Now()

	for i := range 3 {
		b.s.gravar(a, t0.Add(time.Duration(i)*time.Minute), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Fatalf("três down: incidentes = %+v, queria um aberto", inc)
	}
	voltou := t0.Add(3 * time.Minute)
	for i := range 5 {
		b.s.gravar(a, voltou.Add(time.Duration(i)*time.Minute), Result{Status: StatusDegraded, StatusCode: 200})
	}
	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt == nil || !inc[0].ResolvedAt.Equal(voltou) {
		t.Fatalf("cinco degraded: incidentes = %+v; queria resolvido em %v", inc, voltou)
	}
}

// C6 da revisão: 42501 e 23503 viravam sempre "o alvo foi apagado", sem log.
// Mas a transação que abre o incidente também enfileira os avisos, e o
// INSERT em notifications gera os mesmos códigos (canal apagado em corrida,
// GRANT ou RLS que regrediu). O rollback levava check, histerese e
// incidente, a série de um alvo vivo era esquecida, e nada ia ao log. A
// trigger simula a falha vinda de outra tabela com o alvo vivo; o snapshot
// restaurado no fim do teste a leva embora.
func TestRLSOrFKErrorWithTheTargetAliveIsLogged(t *testing.T) {
	for _, codigo := range []string{"42501", "23503"} {
		t.Run(codigo, func(t *testing.T) {
			b := novaBancada(t, nil, MaxInFlight)
			alvo := b.alvo(t, "https://exemplo.com", nil)
			b.canal(t, true, true)
			a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}
			falha := Result{Status: StatusDown, ErrorKind: KindTimeout}
			t0 := b.clk.Now()
			b.s.gravar(a, t0, falha)
			b.s.gravar(a, t0.Add(time.Minute), falha)
			b.m.observe(alvo, falha)

			ctx := context.Background()
			// #nosec G201 -- codigo vem da tabela do próprio teste.
			if _, err := b.pool.Exec(ctx, fmt.Sprintf(`
				CREATE FUNCTION recusar_aviso() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					RAISE EXCEPTION 'aviso recusado pelo teste' USING ERRCODE = '%s';
				END $$;
				CREATE TRIGGER recusar_aviso BEFORE INSERT ON notifications
					FOR EACH ROW EXECUTE FUNCTION recusar_aviso();`, codigo)); err != nil {
				t.Fatal(err)
			}
			log := capturarLog(t)

			// A terceira falha abre o incidente e enfileira o aviso.
			b.s.gravar(a, t0.Add(2*time.Minute), falha)

			if s := log.String(); !strings.Contains(s, "level=ERROR") || !strings.Contains(s, "gravar check") ||
				!strings.Contains(s, alvo.String()) || !strings.Contains(s, codigo) {
				t.Errorf("log = %q; queria o erro de gravar check com o alvo e o código %s", s, codigo)
			}
			if vistos := b.m.alvosVistos(); !slices.Contains(vistos, alvo) {
				t.Errorf("alvos vistos = %v; a série do alvo vivo %s foi esquecida", vistos, alvo)
			}
		})
	}
}
