package notify

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

type bancadaLembretes struct {
	pool      *pgxpool.Pool
	clk       *clock.Fake
	r         *Reminders
	dono      uuid.UUID
	incidente uuid.UUID
	inicio    time.Time // started_at do incidente
}

// novaBancadaLembretes monta usuário, alvo e um incidente que abriu agora,
// com a abertura já avisada. Os canais vêm de canal.
func novaBancadaLembretes(t *testing.T) *bancadaLembretes {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC))
	b := &bancadaLembretes{pool: pool, clk: clk, r: NewReminders(pool, clk, DefaultPolicy()), dono: uuid.Must(uuid.NewV7())}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		b.dono, b.dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	alvo := uuid.Must(uuid.NewV7())
	agora := clk.Now()
	b.inicio = agora
	err := storage.InUserTx(ctx, pool, b.dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: b.dono, Name: "API", URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	b.incidente = uuid.Must(uuid.NewV7())
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: b.incidente, TargetID: alvo, StartedAt: agora, Cause: "timeout"}); err != nil {
			return err
		}
		_, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: b.incidente, Now: agora})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// canal cria um canal habilitado e verificado do dono, que recebeu a
// abertura agora. mexer ajusta o canal antes de gravar.
func (b *bancadaLembretes) canal(t *testing.T, kind Kind, mexer func(*db.InsertChannelParams)) uuid.UUID {
	t.Helper()
	id := b.canalSemAbertura(t, kind, mexer)
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: uuid.Must(uuid.NewV7()), IncidentID: b.incidente, ChannelID: id, Event: incident.EventOpened, Now: b.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancadaLembretes) canalSemAbertura(t *testing.T, kind Kind, mexer func(*db.InsertChannelParams)) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	agora := b.clk.Now()
	p := db.InsertChannelParams{
		ID: uuid.Must(uuid.NewV7()), UserID: b.dono, Kind: string(kind), ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
	}
	if mexer != nil {
		mexer(&p)
	}
	if err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) }); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func (b *bancadaLembretes) pausar(t *testing.T, canal uuid.UUID, pausado bool) {
	t.Helper()
	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{UserID: b.dono, ID: canal, Enabled: !pausado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// avancarETicar avança o relógio e roda um tique, que deve enfileirar quer
// lembretes.
func (b *bancadaLembretes) avancarETicar(t *testing.T, d time.Duration, quer int) {
	t.Helper()
	b.clk.Advance(d)
	n, err := b.r.Tick(context.Background())
	if err != nil || n != quer {
		t.Fatalf("Tick em +%s = %d, %v; queria %d", b.clk.Now().Sub(b.inicio), n, err, quer)
	}
}

type lembrete struct {
	Seq    int32
	Criado time.Time
}

// lembretes lê, como o sondador, os lembretes do canal em ordem de seq.
func (b *bancadaLembretes) lembretes(t *testing.T, canal uuid.UUID) []lembrete {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT seq, created_at FROM notifications
	                             WHERE channel_id = $1 AND event = 'reminder' ORDER BY seq`, canal)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ls []lembrete
	for rows.Next() {
		var l lembrete
		if err := rows.Scan(&l.Seq, &l.Criado); err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ls
}

// esperarAlguemTravado espera até alguma sessão estar parada esperando uma
// trava, sem sleep fixo: é o sinal de que o segundo job chegou ao UNIQUE.
func esperarAlguemTravado(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&n); err != nil {
			t.Fatalf("nenhuma sessão travou: %v", err)
		}
		if n > 0 {
			return
		}
		runtime.Gosched()
	}
}

func TestRemindersNothingBeforeDue(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)

	b.avancarETicar(t, discordShortInterval-time.Second, 0)
	if ls := b.lembretes(t, discord); len(ls) != 0 {
		t.Errorf("lembretes antes de vencer: %+v", ls)
	}
}

func TestRemindersEnqueueTheDueReminder(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)

	b.avancarETicar(t, discordShortInterval, 1)
	ls := b.lembretes(t, discord)
	if len(ls) != 1 || ls[0].Seq != 1 || !ls[0].Criado.Equal(b.clk.Now()) {
		t.Errorf("lembretes = %+v; queria um, seq 1, enfileirado agora", ls)
	}
}

// O segundo tique no mesmo instante vê o lembrete novo como base, e não
// enfileira outro.
func TestRemindersTwoTicksAtTheSameInstantEnqueueOne(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)

	b.avancarETicar(t, discordShortInterval, 1)
	b.avancarETicar(t, 0, 0)
	if ls := b.lembretes(t, discord); len(ls) != 1 {
		t.Errorf("lembretes = %+v; queria um", ls)
	}
}

// Dois sondadores que leem a mesma base calculam o mesmo seq, e o UNIQUE
// deixa um só inserir; o outro segue sem erro. O primeiro é simulado por uma
// transação aberta que já inseriu o lembrete; o segundo é o job de verdade,
// que leu a base antiga e trava no UNIQUE até o primeiro confirmar.
func TestRemindersConcurrentJobsEnqueueOne(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	b.clk.Advance(discordShortInterval)

	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(tx).EnqueueReminder(ctx, db.EnqueueReminderParams{
		ID: uuid.Must(uuid.NewV7()), IncidentID: b.incidente, ChannelID: discord, Seq: 1, Now: b.clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	type resultado struct {
		n   int
		err error
	}
	feito := make(chan resultado, 1)
	go func() {
		n, err := b.r.Tick(ctx)
		feito <- resultado{n, err}
	}()
	esperarAlguemTravado(t, b.pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if r := <-feito; r.err != nil || r.n != 0 {
		t.Fatalf("Tick concorrente = %d, %v; queria 0, sem erro", r.n, r.err)
	}
	if ls := b.lembretes(t, discord); len(ls) != 1 {
		t.Errorf("lembretes = %+v; queria um", ls)
	}
}

func TestRemindersSkipAcknowledgedIncident(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.AckIncident(ctx, db.AckIncidentParams{UserID: b.dono, ID: b.incidente, Now: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	b.avancarETicar(t, discordShortInterval, 0)
	if ls := b.lembretes(t, discord); len(ls) != 0 {
		t.Errorf("lembretes com ack: %+v", ls)
	}
}

func TestRemindersSkipResolvedIncident(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: b.incidente, ResolvedAt: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	b.avancarETicar(t, discordShortInterval, 0)
	if ls := b.lembretes(t, discord); len(ls) != 0 {
		t.Errorf("lembretes de incidente resolvido: %+v", ls)
	}
}

// Sem a abertura avisada, não há o que lembrar.
func TestRemindersSkipIncidentWithoutOpenNotified(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if tag, err := tx.Exec(ctx, `UPDATE incidents SET open_notified_at = NULL WHERE id = $1`, b.incidente); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("limpar open_notified_at: %v, %d linhas", err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	b.avancarETicar(t, discordShortInterval, 0)
	if ls := b.lembretes(t, discord); len(ls) != 0 {
		t.Errorf("lembretes sem open_notified_at: %+v", ls)
	}
}

func TestRemindersSkipUnverifiedChannel(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, func(p *db.InsertChannelParams) { p.VerifiedAt = nil })

	b.avancarETicar(t, discordShortInterval, 0)
	if ls := b.lembretes(t, discord); len(ls) != 0 {
		t.Errorf("lembretes de canal não verificado: %+v", ls)
	}
}

// Pausado, o canal não recebe lembrete; retomado depois de vários
// intervalos, recebe um só, o vencido.
func TestRemindersPausedChannelGetsOneOnResume(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	b.pausar(t, discord, true)

	b.avancarETicar(t, discordShortInterval, 0)
	b.avancarETicar(t, 3*discordShortInterval, 0)
	b.pausar(t, discord, false)
	b.avancarETicar(t, 0, 1)
	if ls := b.lembretes(t, discord); len(ls) != 1 || ls[0].Seq != 1 {
		t.Errorf("lembretes = %+v; queria um, seq 1", ls)
	}
}

// Um canal criado no meio do incidente não recebeu a abertura e não recebe
// lembrete.
func TestRemindersSkipChannelWithoutOpened(t *testing.T) {
	b := novaBancadaLembretes(t)
	novo := b.canalSemAbertura(t, KindDiscord, nil)

	b.avancarETicar(t, discordShortInterval, 0)
	if ls := b.lembretes(t, novo); len(ls) != 0 {
		t.Errorf("lembretes de canal sem abertura: %+v", ls)
	}
}

// Discord: lembretes a cada 30 min de 0h30 a 6h00; o das 6h00 já tem idade
// de 6 h na base, e o seguinte sai às 12h00.
func TestRemindersSwitchToTheLongIntervalAfterSixHours(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)

	const curtos = 12 // 6 h / 30 min
	for range curtos {
		b.avancarETicar(t, 30*time.Minute, 1)
	}
	b.avancarETicar(t, 6*time.Hour-time.Second, 0)
	b.avancarETicar(t, time.Second, 1)

	ls := b.lembretes(t, discord)
	if len(ls) != curtos+1 {
		t.Fatalf("%d lembretes, queria %d", len(ls), curtos+1)
	}
	if ultimo := ls[len(ls)-1]; ultimo.Seq != curtos+1 || ultimo.Criado.Sub(b.inicio) != 12*time.Hour {
		t.Errorf("último lembrete = seq %d em +%s; queria seq %d em +12h", ultimo.Seq, ultimo.Criado.Sub(b.inicio), curtos+1)
	}
}

// Discord e e-mail no mesmo incidente seguem cadências próprias: em 2 h, o
// Discord recebe 4 lembretes e o e-mail, 1.
func TestRemindersCadencePerChannel(t *testing.T) {
	b := novaBancadaLembretes(t)
	discord := b.canal(t, KindDiscord, nil)
	email := b.canal(t, KindEmail, nil)

	for range 3 {
		b.avancarETicar(t, 30*time.Minute, 1)
	}
	b.avancarETicar(t, 30*time.Minute, 2)

	if ls := b.lembretes(t, discord); len(ls) != 4 {
		t.Errorf("%d lembretes no Discord em 2 h, queria 4", len(ls))
	}
	if ls := b.lembretes(t, email); len(ls) != 1 || ls[0].Criado.Sub(b.inicio) != 2*time.Hour {
		t.Errorf("lembretes do e-mail = %+v; queria um em +2h", ls)
	}
}

// A paginação por chave chega a todos os pares, mesmo com mais pares que uma
// página.
func TestRemindersPageThroughAllPairs(t *testing.T) {
	b := novaBancadaLembretes(t)
	b.r.pageSize = 2
	var canais []uuid.UUID
	for range 5 {
		canais = append(canais, b.canal(t, KindDiscord, nil))
	}

	b.avancarETicar(t, 30*time.Minute, len(canais))
	for _, c := range canais {
		if ls := b.lembretes(t, c); len(ls) != 1 {
			t.Errorf("canal %s com %d lembretes, queria 1", c, len(ls))
		}
	}
}
