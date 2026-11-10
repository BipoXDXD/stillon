package prober

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
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

// O certificado renovado substitui o guardado quando o antigo fica
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

// Minor 6 da revisão de segurança do #246. Com o certificado já vencido (ou
// trocado por um inválido), a sonda sai down/tls sem leitura: o check entra
// no histórico, e a linha guardada fica intacta, com o not_after anterior,
// que o painel mostra como "venceu em". A validade guardada (1º de setembro)
// já passou no relógio da bancada (22 de setembro, 12h). Sabotagens: tirar o
// retorno antecipado de gravar para CertNotAfter zero faz a gravação tentar
// guardar a validade zero; gravar o check com outro error_kind ou outro
// checked_at passa a ser pego; reescrever o observed_at da linha quando a
// validade guardada já passou ("renovar a observação do vencido") também.
func TestTLSFailureKeepsTheStoredCertificate(t *testing.T) {
	vencidaEm := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	falhaEm := time.Date(2026, 9, 22, 12, 1, 0, 0, time.UTC)
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()
	if !vencidaEm.Before(t0) {
		t.Fatalf("a validade guardada (%v) deveria ter passado no relógio da bancada (%v)", vencidaEm, t0)
	}

	b.s.gravar(a, t0, comCertificado(vencidaEm))
	antes := b.exigirLinhaTLS(t, alvo)
	if !antes.NotAfter.Equal(vencidaEm) {
		t.Fatalf("not_after = %v antes da falha; queria %v", antes.NotAfter, vencidaEm)
	}
	b.s.gravar(a, t0.Add(time.Minute), Result{Status: StatusDown, ErrorKind: KindTLS})

	c := b.checks(t, alvo)
	if len(c) != 2 {
		t.Fatalf("%d checks no histórico; queria 2, com o down/tls", len(c))
	}
	novo := c[1]
	kind := "<nulo>"
	if novo.ErrorKind != nil {
		kind = *novo.ErrorKind
	}
	if !novo.CheckedAt.Equal(falhaEm) || novo.Status != "down" || kind != "tls" || novo.StatusCode != nil {
		t.Errorf("check novo = {CheckedAt:%v Status:%q ErrorKind:%q StatusCode:%v}; queria %v, down, tls e sem código",
			novo.CheckedAt, novo.Status, kind, novo.StatusCode, falhaEm)
	}
	depois := b.exigirLinhaTLS(t, alvo)
	if !depois.NotAfter.Equal(antes.NotAfter) || !depois.ObservedAt.Equal(antes.ObservedAt) || depois.Xmin != antes.Xmin {
		t.Errorf("target_tls = %+v depois do down/tls; queria a linha intacta %+v", depois, antes)
	}
}

// Minor 4 da revisão de segurança do #246. O PATCH troca a URL para outro host
// com uma sonda da URL velha em voo: o PATCH apaga a leitura do host antigo, e
// a sonda velha, que termina depois, não pode recriá-la para o alvo.
// Sabotagens: tirar a comparação de URL de conferirSonda faz a sonda velha
// gravar a validade do host antigo; trocar o errSondaVelha por outro erro
// também deixa a tabela vazia, e só o log e o check pegam.
func TestOldHostProbeDoesNotRecreateTargetTLS(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t0 := b.clk.Now()
	b.s.gravar(a, t0, comCertificado(validadeB))
	b.exigirLinhaTLS(t, alvo)

	nova := urlNova
	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, targets.Patch{URL: &nova}); err != nil {
		t.Fatal(err)
	}
	if l, ok := b.linhaTLS(t, alvo); ok {
		t.Fatalf("target_tls = %+v depois do PATCH; o PATCH deveria apagar a leitura do host antigo", l)
	}

	log := capturarLog(t)
	b.s.gravar(a, t0.Add(time.Minute), comCertificado(validadeA))

	if l, ok := b.linhaTLS(t, alvo); ok {
		t.Errorf("target_tls = %+v; a sonda do host antigo gravou a validade dele para o alvo", l)
	}
	exigirDescartePelaCerca(t, b, log, alvo, 1)
}

// exigirDescartePelaCerca afirma que a gravação da sonda velha foi descartada
// pelo fencing (errSondaVelha), e não por outra falha da transação, que também
// deixaria target_tls vazia: o rollback levou o check dela, sobrando só os
// checks anteriores, e o log é o do descarte, sem erro.
func exigirDescartePelaCerca(t *testing.T, b *bancada, log *logSeguro, alvo uuid.UUID, checksAntes int) {
	t.Helper()
	if c := b.checks(t, alvo); len(c) != checksAntes {
		t.Errorf("%d checks no histórico; queria %d, sem o da sonda do host antigo", len(c), checksAntes)
	}
	s := log.String()
	if !strings.Contains(s, "sonda descartada: o alvo mudou de URL ou método durante a sonda") ||
		!strings.Contains(s, alvo.String()) || strings.Contains(s, "level=ERROR") {
		t.Errorf("log = %q; queria só o descarte da sonda velha do alvo %s, sem erro", s, alvo)
	}
}

// A gravação da sonda velha que começa com o PATCH ainda aberto espera a
// trava do alvo (FOR UPDATE do PATCH contra o FOR KEY SHARE da cerca) e, depois
// do commit, enxerga a URL nova e não grava a leitura do host antigo.
// Sabotagens: as mesmas do teste anterior.
//
// Sem prazo de parede na transação do PATCH: ela fica aberta enquanto o
// EsperarBloqueados consulta, e um prazo fixo vence à toa com a máquina
// carregada. O limite é o -timeout do go test, como no EsperarBloqueados.
func TestOldHostProbeWaitsForAnOpenPatchAndLeavesNoCertificate(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	ctx := t.Context()
	log := capturarLog(t)

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	q := db.New(tx)
	if _, err := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: b.dono, ID: alvo}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE targets SET url = $2 WHERE id = $1`, alvo, urlNova); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: b.dono, TargetID: alvo}); err != nil {
		t.Fatal(err)
	}

	gravou := make(chan struct{}, 1)
	go func() {
		defer func() { gravou <- struct{}{} }()
		b.s.gravar(a, b.clk.Now(), comCertificado(validadeA))
	}()
	if !testdb.EsperarBloqueados(t, b.pool, 1, gravou) {
		t.Fatal("a gravação terminou com o PATCH aberto; deveria esperar a linha do alvo")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	esperar(t, gravou, "a gravação terminar depois do commit do PATCH")

	if l, ok := b.linhaTLS(t, alvo); ok {
		t.Errorf("target_tls = %+v; a sonda do host antigo gravou depois do commit do PATCH", l)
	}
	exigirDescartePelaCerca(t, b, log, alvo, 0)
}
