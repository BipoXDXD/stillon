package prober

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Duas sondas do mesmo alvo podem se sobrepor: o timeout chega a 90% do
// intervalo e o jitter encurta a espera para 0,9 dele. As duas gravam ao
// mesmo tempo, e a mais velha termina depois. A trava da histerese
// (LockTargetHealth) põe as duas em fila, e a guarda de ordem descarta a
// atrasada: abre um incidente só, e a sequência de falhas não conta a
// atrasada nem volta o last_checked_at.
//
// Uma transação de fora segura a linha da histerese enquanto as duas
// gravações entram na fila: a mais nova primeiro e a mais velha depois. A
// primeira espera a transação de fora e a segunda espera a primeira, que já
// segura a trava da tupla: ao soltar, a mais velha chega atrasada, como na
// sobreposição real.
func TestOverlappingProbesOfTheSameTargetOpenOneIncident(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	b.semearFalhasDa(t, a)
	nova := b.clk.Now()
	velha := nova.Add(-10 * time.Second)
	falha := Result{Status: StatusDown, ErrorKind: KindTimeout}

	ctx := context.Background()
	trava, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer trava.Rollback(ctx)
	if _, err := trava.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := trava.Exec(ctx, `SELECT 1 FROM target_health WHERE target_id = $1 FOR UPDATE`, alvo); err != nil {
		t.Fatal(err)
	}

	fim := make(chan struct{}, 2)
	gravarEmFila := func(em time.Time, naFila int) {
		go func() {
			b.s.gravar(a, em, falha)
			fim <- struct{}{}
		}()
		if !testdb.EsperarBloqueados(t, b.pool, naFila, fim) {
			t.Fatal("uma gravação terminou sem parar na trava da histerese")
		}
	}
	gravarEmFila(nova, 1)
	gravarEmFila(velha, 2)
	if err := trava.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	<-fim
	<-fim

	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Errorf("incidentes = %+v; queria um aberto", inc)
	}
	if s := b.saude(t, alvo); s.Falhas != 3 || !s.Checado.Equal(nova) {
		t.Errorf("histerese = %d falhas, checado em %v; queria 3 falhas em %v", s.Falhas, s.Checado, nova)
	}
	if rows := b.checks(t, alvo); len(rows) != 4 {
		t.Errorf("%d checks; queria 4 (as duas semeadas e as duas sobrepostas)", len(rows))
	}
}
