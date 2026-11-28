package storage_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

func reservar(t *testing.T, teto *storage.TetoDeEmailsDeConta) bool {
	t.Helper()
	recusa, err := teto.Reservar(context.Background(), uuid.Nil)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	return recusa == ""
}

func enviadosHoje(t *testing.T, teto *storage.TetoDeEmailsDeConta) int {
	t.Helper()
	n, err := teto.EnviadosHoje(context.Background())
	if err != nil {
		t.Fatalf("enviados hoje: %v", err)
	}
	return n
}

func TestTetoReservaAteOLimite(t *testing.T) {
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clock.NewFake(agora), 3, 10)

	var obtidos []bool
	for range 4 {
		obtidos = append(obtidos, reservar(t, teto))
	}
	if want := []bool{true, true, true, false}; !slices.Equal(obtidos, want) {
		t.Errorf("reservas = %v, queria %v", obtidos, want)
	}
	if n := enviadosHoje(t, teto); n != 3 {
		t.Errorf("enviados hoje = %d, queria 3", n)
	}
}

func TestTetoZeroNaoReserva(t *testing.T) {
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clock.NewFake(agora), 0, 10)

	if reservar(t, teto) {
		t.Error("teto 0 deixou reservar")
	}
	if n := enviadosHoje(t, teto); n != 0 {
		t.Errorf("enviados hoje = %d, queria 0", n)
	}
}

func TestTetoReservaExatamente50SobConcorrencia(t *testing.T) {
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	// Duas réplicas da API, cada uma com o seu teto, sobre o mesmo banco.
	replicas := []*storage.TetoDeEmailsDeConta{
		storage.NewTetoDeEmailsDeConta(pool, clk, 50, 10),
		storage.NewTetoDeEmailsDeConta(pool, clk, 50, 10),
	}

	var (
		concedidas atomic.Int32
		largada    = make(chan struct{})
		wg         sync.WaitGroup
		erros      = make(chan error, 60)
	)
	for i := range 60 {
		wg.Go(func() {
			<-largada
			recusa, err := replicas[i%2].Reservar(context.Background(), uuid.Nil)
			if err != nil {
				erros <- err
				return
			}
			if recusa == "" {
				concedidas.Add(1)
			}
		})
	}
	close(largada)
	wg.Wait()
	close(erros)
	for err := range erros {
		t.Errorf("reserva concorrente: %v", err)
	}

	if n := concedidas.Load(); n != 50 {
		t.Errorf("reservas concedidas = %d, queria 50", n)
	}
	if n := enviadosHoje(t, replicas[0]); n != 50 {
		t.Errorf("enviados hoje = %d, queria 50", n)
	}
}

func TestTetoViraODiaEmUTC(t *testing.T) {
	// O relógio fala em horário de Brasília: o dia do teto tem de ser o UTC,
	// e às 21h de Brasília o dia UTC já virou.
	brasilia := time.FixedZone("BRT", -3*60*60)
	clk := clock.NewFake(time.Date(2026, 11, 23, 23, 59, 59, 0, time.UTC).In(brasilia))
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clk, 1, 10)

	if !reservar(t, teto) {
		t.Fatal("primeira reserva do dia recusada")
	}
	clk.Advance(time.Second) // 00:00:00Z

	if n := enviadosHoje(t, teto); n != 0 {
		t.Errorf("enviados no dia UTC novo = %d, queria 0", n)
	}
	if !reservar(t, teto) {
		t.Error("reserva no dia UTC novo recusada")
	}
}

func TestTetoApagaDiasVelhos(t *testing.T) {
	pool := testdb.New(t)
	admin := testdb.Admin(t)
	ctx := context.Background()
	hoje := agora.UTC().Truncate(24 * time.Hour)
	oitoDias, seteDias := hoje.AddDate(0, 0, -8), hoje.AddDate(0, 0, -7)
	if _, err := admin.Exec(ctx, `INSERT INTO account_email_daily (day, sent) VALUES ($1, 5), ($2, 5)`,
		oitoDias, seteDias); err != nil {
		t.Fatal(err)
	}

	reservar(t, storage.NewTetoDeEmailsDeConta(pool, clock.NewFake(agora), 50, 10))

	existe := func(dia time.Time) bool {
		var ok bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_email_daily WHERE day = $1)`, dia).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if existe(oitoDias) {
		t.Error("a linha de 8 dias atrás sobreviveu à reserva")
	}
	if !existe(seteDias) {
		t.Error("a linha de 7 dias atrás sumiu; só as mais velhas que 7 dias saem")
	}
}

func reservarPela(t *testing.T, teto *storage.TetoDeEmailsDeConta, conta uuid.UUID) string {
	t.Helper()
	recusa, err := teto.Reservar(context.Background(), conta)
	if err != nil {
		t.Fatalf("reservar pela conta: %v", err)
	}
	return recusa
}

func enviadosHojePela(t *testing.T, teto *storage.TetoDeEmailsDeConta, conta uuid.UUID) int {
	t.Helper()
	n, err := teto.EnviadosHojePelaConta(context.Background(), conta)
	if err != nil {
		t.Fatalf("enviados hoje pela conta: %v", err)
	}
	return n
}

func TestAccountCapStopsAtTenPerUser(t *testing.T) {
	pool := testdb.New(t)
	u, v := novoUsuario(t, pool), novoUsuario(t, pool)
	teto := storage.NewTetoDeEmailsDeConta(pool, clock.NewFake(agora), 50, 10)

	for i := range 10 {
		if recusa := reservarPela(t, teto, u); recusa != "" {
			t.Fatalf("reserva %d de U recusada por %q", i+1, recusa)
		}
	}
	if recusa := reservarPela(t, teto, u); recusa != storage.RecusaConta {
		t.Errorf("11ª reserva de U = %q, queria %q", recusa, storage.RecusaConta)
	}
	if recusa := reservarPela(t, teto, v); recusa != "" {
		t.Errorf("reserva de V = %q, queria liberada", recusa)
	}
	if n := enviadosHojePela(t, teto, u); n != 10 {
		t.Errorf("enviados hoje por U = %d, queria 10", n)
	}
	// A recusa da conta não gasta o teto global: 10 de U e 1 de V.
	if n := enviadosHoje(t, teto); n != 11 {
		t.Errorf("enviados hoje = %d, queria 11", n)
	}
}

func TestAccountCapRolledBackWhenGlobalRefuses(t *testing.T) {
	pool := testdb.New(t)
	admin := testdb.Admin(t)
	u := novoUsuario(t, pool)
	hoje := agora.UTC().Truncate(24 * time.Hour)
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO account_email_daily (day, sent) VALUES ($1, 50)`, hoje); err != nil {
		t.Fatal(err)
	}
	teto := storage.NewTetoDeEmailsDeConta(pool, clock.NewFake(agora), 50, 10)

	if recusa := reservarPela(t, teto, u); recusa != storage.RecusaTeto {
		t.Errorf("reserva de U = %q, queria %q", recusa, storage.RecusaTeto)
	}
	if n := enviadosHojePela(t, teto, u); n != 0 {
		t.Errorf("enviados hoje por U = %d, queria 0: a vaga da conta não voltou", n)
	}
}

func TestZeroAccountSkipsAccountCap(t *testing.T) {
	pool := testdb.New(t)
	admin := testdb.Admin(t)
	teto := storage.NewTetoDeEmailsDeConta(pool, clock.NewFake(agora), 50, 1)

	for i := range 3 {
		if recusa := reservarPela(t, teto, uuid.Nil); recusa != "" {
			t.Fatalf("reserva %d sem conta recusada por %q", i+1, recusa)
		}
	}
	if n := enviadosHoje(t, teto); n != 3 {
		t.Errorf("enviados hoje = %d, queria 3", n)
	}
	var linhas int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM account_email_daily_by_user`).Scan(&linhas); err != nil {
		t.Fatal(err)
	}
	if linhas != 0 {
		t.Errorf("linhas por conta = %d, queria 0", linhas)
	}
}

func TestAccountCapConcurrentReservations(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	clk := clock.NewFake(agora)
	// Duas réplicas da API sobre o mesmo banco.
	replicas := []*storage.TetoDeEmailsDeConta{
		storage.NewTetoDeEmailsDeConta(pool, clk, 50, 10),
		storage.NewTetoDeEmailsDeConta(pool, clk, 50, 10),
	}

	var (
		liberadas atomic.Int32
		largada   = make(chan struct{})
		wg        sync.WaitGroup
		erros     = make(chan error, 20)
	)
	for i := range 20 {
		wg.Go(func() {
			<-largada
			recusa, err := replicas[i%2].Reservar(context.Background(), u)
			if err != nil {
				erros <- err
				return
			}
			if recusa == "" {
				liberadas.Add(1)
			}
		})
	}
	close(largada)
	wg.Wait()
	close(erros)
	for err := range erros {
		t.Errorf("reserva concorrente: %v", err)
	}

	if n := liberadas.Load(); n != 10 {
		t.Errorf("reservas liberadas = %d, queria 10", n)
	}
	if n := enviadosHojePela(t, replicas[0], u); n != 10 {
		t.Errorf("enviados hoje por U = %d, queria 10", n)
	}
	if n := enviadosHoje(t, replicas[0]); n != 10 {
		t.Errorf("enviados hoje = %d, queria 10", n)
	}
}

func TestAccountCapResetsNextUTCDay(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	// O relógio fala em horário de Brasília; o dia da conta é o UTC.
	brasilia := time.FixedZone("BRT", -3*60*60)
	clk := clock.NewFake(time.Date(2026, 11, 23, 23, 59, 59, 0, time.UTC).In(brasilia))
	teto := storage.NewTetoDeEmailsDeConta(pool, clk, 50, 1)

	if recusa := reservarPela(t, teto, u); recusa != "" {
		t.Fatalf("primeira reserva do dia = %q", recusa)
	}
	if recusa := reservarPela(t, teto, u); recusa != storage.RecusaConta {
		t.Fatalf("segunda reserva do dia = %q, queria %q", recusa, storage.RecusaConta)
	}
	clk.Advance(time.Second) // 00:00:00Z

	if n := enviadosHojePela(t, teto, u); n != 0 {
		t.Errorf("enviados por U no dia UTC novo = %d, queria 0", n)
	}
	if recusa := reservarPela(t, teto, u); recusa != "" {
		t.Errorf("reserva no dia UTC novo = %q, queria liberada", recusa)
	}
}
