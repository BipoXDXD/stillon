package storage_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

func reservar(t *testing.T, teto *storage.TetoDeEmailsDeConta) bool {
	t.Helper()
	ok, err := teto.Reservar(context.Background())
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	return ok
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
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clock.NewFake(agora), 3)

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
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clock.NewFake(agora), 0)

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
		storage.NewTetoDeEmailsDeConta(pool, clk, 50),
		storage.NewTetoDeEmailsDeConta(pool, clk, 50),
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
			ok, err := replicas[i%2].Reservar(context.Background())
			if err != nil {
				erros <- err
				return
			}
			if ok {
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
	teto := storage.NewTetoDeEmailsDeConta(testdb.New(t), clk, 1)

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

	reservar(t, storage.NewTetoDeEmailsDeConta(pool, clock.NewFake(agora), 50))

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
