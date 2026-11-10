package prober

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Validades literais: A vence antes de B, então A é o pior certificado.
var (
	validadeA = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	validadeB = time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
)

// linhaTLS é a linha de target_tls lida com o escopo do dono; xmin identifica
// a versão da linha, e muda a cada UPDATE.
type linhaTLS struct {
	NotAfter, ObservedAt time.Time
	Xmin                 int64
}

func (b *bancada) linhaTLS(t *testing.T, alvo uuid.UUID) (linhaTLS, bool) {
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
	var l linhaTLS
	err = tx.QueryRow(ctx, `SELECT not_after, observed_at, xmin::text::bigint FROM target_tls WHERE target_id = $1`, alvo).
		Scan(&l.NotAfter, &l.ObservedAt, &l.Xmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return linhaTLS{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return l, true
}

func (b *bancada) exigirLinhaTLS(t *testing.T, alvo uuid.UUID) linhaTLS {
	t.Helper()
	l, ok := b.linhaTLS(t, alvo)
	if !ok {
		t.Fatal("target_tls sem linha do alvo")
	}
	return l
}

func (b *bancada) linhaDoAlvo(alvo uuid.UUID) db.ClaimDueTargetsRow {
	return db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: "GET", IntervalSec: 60}
}

func comCertificado(validade time.Time) Result {
	return Result{Status: StatusUp, StatusCode: 200, CertNotAfter: validade}
}

func TestGravarKeepsTheWorstCertificate(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()

	b.s.gravar(a, t0, comCertificado(validadeB))
	b.s.gravar(a, t0.Add(time.Minute), comCertificado(validadeA))
	if l := b.exigirLinhaTLS(t, alvo); !l.NotAfter.Equal(validadeA) {
		t.Fatalf("not_after = %v depois de B e A; queria A (%v)", l.NotAfter, validadeA)
	}

	b.s.gravar(a, t0.Add(2*time.Minute), comCertificado(validadeB))
	l := b.exigirLinhaTLS(t, alvo)
	if !l.NotAfter.Equal(validadeA) {
		t.Errorf("not_after = %v depois de B de novo; queria A (%v)", l.NotAfter, validadeA)
	}
	if want := t0.Add(time.Minute); !l.ObservedAt.Equal(want) {
		t.Errorf("observed_at = %v; queria o da leitura de A (%v)", l.ObservedAt, want)
	}
}

// Num balanceador com réplicas de certificados diferentes, as sondas veem A e
// B alternados. A linha fica em A e não é reescrita a cada sonda.
func TestBalancerAlternationDoesNotRewriteTheRow(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()

	var xminDeA int64
	for i := range 10 {
		validade := validadeB
		if i%2 == 1 {
			validade = validadeA
		}
		b.s.gravar(a, t0.Add(time.Duration(i)*time.Minute), comCertificado(validade))
		if i == 1 {
			xminDeA = b.exigirLinhaTLS(t, alvo).Xmin
		}
	}

	l := b.exigirLinhaTLS(t, alvo)
	if !l.NotAfter.Equal(validadeA) {
		t.Errorf("not_after = %v; queria A (%v)", l.NotAfter, validadeA)
	}
	if l.Xmin != xminDeA {
		t.Errorf("xmin = %d; queria %d, o da primeira gravação de A: a linha foi reescrita", l.Xmin, xminDeA)
	}
}

// Foco 1: o certificado renovado substitui o guardado quando o antigo fica
// sem ser visto por mais que max(6 h, 3 x intervalo).
func TestRenewalReplacesAfterTheWindow(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()

	b.s.gravar(a, t0, comCertificado(validadeA))
	em := t0.Add(6*time.Hour + time.Second)
	b.s.gravar(a, em, comCertificado(validadeB))

	l := b.exigirLinhaTLS(t, alvo)
	if !l.NotAfter.Equal(validadeB) {
		t.Errorf("not_after = %v; queria a renovada B (%v)", l.NotAfter, validadeB)
	}
	if !l.ObservedAt.Equal(em) {
		t.Errorf("observed_at = %v; queria %v", l.ObservedAt, em)
	}
}

// Duas sondas do mesmo alvo gravam ao mesmo tempo: a primeira lê a validade
// guardada (nenhuma) e é segurada antes de decidir; a segunda tem de esperar
// a trava da histerese e só então ler, para ver o B da primeira e trocá-lo
// por A. Lendo antes da trava, ela veria "nenhuma", gravaria A, e a primeira
// sobrescreveria com B.
func TestConcurrentProbesKeepTheWorstCertificate(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()

	leu := make(chan struct{})
	liberar := make(chan struct{})
	var chamadas atomic.Int32
	b.s.depoisDeLerValidade = func() {
		if chamadas.Add(1) == 1 {
			close(leu)
			<-liberar
		}
	}

	primeira := make(chan struct{}, 1)
	go func() {
		defer func() { primeira <- struct{}{} }()
		b.s.gravar(a, t0, comCertificado(validadeB))
	}()
	select {
	case <-leu:
	case <-time.After(esperaMaxima):
		close(liberar)
		t.Fatal("a primeira gravação não leu a validade guardada")
	}

	segunda := make(chan struct{}, 1)
	go func() {
		defer func() { segunda <- struct{}{} }()
		b.s.gravar(a, t0.Add(time.Second), comCertificado(validadeA))
	}()
	if !testdb.EsperarBloqueados(t, b.pool, 1, segunda) {
		t.Error("a segunda gravação terminou com a primeira segurada; deveria esperar a trava da histerese")
	}

	close(liberar)
	esperar(t, primeira, "a primeira gravação terminar")
	esperar(t, segunda, "a segunda gravação terminar")

	if l := b.exigirLinhaTLS(t, alvo); !l.NotAfter.Equal(validadeA) {
		t.Errorf("not_after = %v; queria A (%v), o pior das duas sondas", l.NotAfter, validadeA)
	}
}

// A sonda descartada pela cerca (alvo pausado durante ela) não grava nada.
func TestDiscardedProbeDoesNotTouchTargetTLS(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, func(p *db.InsertTargetParams) { p.Enabled = false })

	b.s.gravar(b.linhaDoAlvo(alvo), b.clk.Now(), comCertificado(validadeA))

	if l, ok := b.linhaTLS(t, alvo); ok {
		t.Errorf("target_tls = %+v; a sonda descartada não deveria gravar", l)
	}
}
