package auth_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
)

// A3 (Plano 10, §3.1): as rotas autenticadas contam as provas erradas por
// família de sessão, em memória. A reserva vem ANTES da verificação (A2): a
// quinta falha derruba a família, e dali em diante toda reserva nasce
// esgotada, sem verificar nada.
func TestFamilyProofsDropTheFamilyAtTheLimit(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()

	for i := 1; i < auth.FamilyProofLimit; i++ {
		r := provas.Reserve(familia)
		if r.Exhausted() {
			t.Fatalf("reserva %d nasceu esgotada", i)
		}
		if r.Fail() {
			t.Fatalf("a falha %d derrubou a família (limite %d)", i, auth.FamilyProofLimit)
		}
		r.Release() // depois de Fail, Release não devolve nada
	}
	r := provas.Reserve(familia)
	if r.Exhausted() {
		t.Fatal("a quinta reserva nasceu esgotada")
	}
	if !r.Fail() {
		t.Fatalf("a falha %d não derrubou a família", auth.FamilyProofLimit)
	}
	r.Release()
	if !provas.Reserve(familia).Exhausted() {
		t.Error("depois da quinta falha, a reserva seguinte não nasceu esgotada")
	}
}

// Reservas simultâneas contam todas: com 12 em voo, só as 5 primeiras
// verificam; as outras nascem esgotadas.
func TestFamilyProofsCountConcurrentReservations(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()

	var mu sync.Mutex
	validas := 0
	var wg sync.WaitGroup
	reservas := make(chan *auth.ProofReservation, 12)
	for range 12 {
		wg.Go(func() {
			r := provas.Reserve(familia)
			if !r.Exhausted() {
				mu.Lock()
				validas++
				mu.Unlock()
			}
			reservas <- r
		})
	}
	wg.Wait()
	close(reservas)
	if validas != auth.FamilyProofLimit {
		t.Errorf("%d reservas válidas em 12 simultâneas, queria %d", validas, auth.FamilyProofLimit)
	}
	for r := range reservas {
		r.Release()
	}
}

// Release devolve a reserva de quem não chegou a errar prova (um 403, um 500,
// a sessão caída); Succeed zera a família; e cada família tem a sua conta.
func TestFamilyProofsReleaseSucceedAndIsolation(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	a, b := uuid.New(), uuid.New()

	for range 10 {
		provas.Reserve(a).Release()
	}
	for i := 1; i < auth.FamilyProofLimit; i++ {
		if provas.Reserve(a).Fail() {
			t.Fatalf("a falha %d derrubou a família: as reservas devolvidas continuaram contando", i)
		}
	}
	if provas.Reserve(b).Fail() {
		t.Error("as falhas da família A derrubaram a família B")
	}
	r := provas.Reserve(a)
	r.Succeed()
	r.Release()
	for i := 1; i < auth.FamilyProofLimit; i++ {
		if provas.Reserve(a).Fail() {
			t.Fatalf("depois do sucesso, a falha %d já derrubou a família: Succeed não zerou", i)
		}
	}
}

// A conta de uma família parada há mais de um dia é esquecida: o mapa não
// cresce para sempre com famílias que ninguém usa mais. A família esgotada
// também é esquecida — ela já foi derrubada no banco, e o token dela não passa
// da guarda da sessão viva.
func TestFamilyProofsForgetIdleFamilies(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	provas := auth.NewFamilyProofs(clk)
	parada, outra := uuid.New(), uuid.New()
	for i := 1; i < auth.FamilyProofLimit; i++ {
		provas.Reserve(parada).Fail()
	}
	provas.Reserve(outra).Fail()
	clk.Advance(25 * time.Hour)
	if provas.Reserve(parada).Fail() {
		t.Error("a conta de uma família parada há 25 h continuou valendo")
	}
	if n := provas.Len(); n != 1 {
		t.Errorf("%d famílias no mapa, queria 1: a outra, parada, não foi varrida", n)
	}
}
