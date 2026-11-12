package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As rotas autenticadas contam as provas erradas por família de sessão, no
// Postgres. A reserva vem ANTES da verificação: a quinta falha derruba a
// família, e dali em diante toda reserva nasce esgotada, sem verificar nada.

// bancadaDeProvas é um contador sobre um Postgres real, com o relógio fake.
type bancadaDeProvas struct {
	pool   *pgxpool.Pool
	clk    *clock.Fake
	provas *auth.FamilyProofs
}

func novaBancadaDeProvas(t *testing.T) *bancadaDeProvas {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	return &bancadaDeProvas{pool: pool, clk: clk, provas: auth.NewFamilyProofs(pool, clk)}
}

// familia cria um usuário com uma família de sessão viva e devolve os dois ids.
func (b *bancadaDeProvas) familia(t *testing.T) (usuario, familia uuid.UUID) {
	t.Helper()
	usuario, familia = uuid.New(), uuid.New()
	if _, err := b.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		usuario, usuario.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	b.sessaoDe(t, usuario, familia)
	return usuario, familia
}

func (b *bancadaDeProvas) sessaoDe(t *testing.T, usuario, familia uuid.UUID) {
	t.Helper()
	if _, err := b.pool.Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), usuario, familia, uuid.NewString(), b.clk.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaDeProvas) revogar(t *testing.T, familia uuid.UUID) {
	t.Helper()
	if _, err := b.pool.Exec(context.Background(),
		`UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`, familia, b.clk.Now()); err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaDeProvas) tamanho(t *testing.T) int64 {
	t.Helper()
	n, err := b.provas.Len(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (b *bancadaDeProvas) reservar(t *testing.T, usuario, familia uuid.UUID) *auth.ProofReservation {
	t.Helper()
	return reservarCom(t, b.provas, usuario, familia)
}

func reservarCom(t *testing.T, provas *auth.FamilyProofs, usuario, familia uuid.UUID) *auth.ProofReservation {
	t.Helper()
	r, err := provas.Reserve(context.Background(), usuario, familia)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	return r
}

// falhar é Fail com o erro de banco como falha do teste.
func falhar(t *testing.T, r *auth.ProofReservation) bool {
	t.Helper()
	derrubar, err := r.Fail()
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	return derrubar
}

func TestFamilyProofsDropTheFamilyAtTheLimit(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)

	for i := 1; i < auth.FamilyProofLimit; i++ {
		r := b.reservar(t, usuario, familia)
		if r.Exhausted() {
			t.Fatalf("reserva %d nasceu esgotada", i)
		}
		if falhar(t, r) {
			t.Fatalf("a falha %d derrubou a família (limite %d)", i, auth.FamilyProofLimit)
		}
		r.Release() // depois de Fail, Release não devolve nada
	}
	r := b.reservar(t, usuario, familia)
	if r.Exhausted() {
		t.Fatal("a quinta reserva nasceu esgotada")
	}
	if !falhar(t, r) {
		t.Fatalf("a falha %d não derrubou a família", auth.FamilyProofLimit)
	}
	r.Release()
	if !b.reservar(t, usuario, familia).Exhausted() {
		t.Error("depois da quinta falha, a reserva seguinte não nasceu esgotada")
	}
}

// O contador sobrevive à recriação do serviço: quatro falhas contadas por uma
// instância valem para a instância nova sobre o mesmo banco, e a quinta derruba.
// É o que a memória do processo não fazia (#324).
func TestFamilyProofsSurviveRecreatingTheService(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	for i := 1; i < auth.FamilyProofLimit; i++ {
		if falhar(t, b.reservar(t, usuario, familia)) {
			t.Fatalf("a falha %d derrubou a família", i)
		}
	}

	reiniciado := auth.NewFamilyProofs(b.pool, b.clk)

	r := reservarCom(t, reiniciado, usuario, familia)
	if r.Exhausted() {
		t.Fatal("a reserva depois do restart nasceu esgotada: a quinta ainda cabe")
	}
	if !falhar(t, r) {
		t.Error("a quinta falha, depois do restart, não derrubou a família: o contador não sobreviveu")
	}
	if !reservarCom(t, reiniciado, usuario, familia).Exhausted() {
		t.Error("depois da quinta falha, a reserva seguinte não nasceu esgotada")
	}
}

// Reservas simultâneas contam todas: com 12 em voo, só as 5 primeiras
// verificam; as outras nascem esgotadas. O incremento é atômico no banco, e
// instâncias diferentes do contador dividem o mesmo orçamento.
func TestFamilyProofsCountConcurrentReservations(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	instancias := []*auth.FamilyProofs{b.provas, auth.NewFamilyProofs(b.pool, b.clk)}

	var mu sync.Mutex
	validas := 0
	var wg sync.WaitGroup
	reservas := make(chan *auth.ProofReservation, 12)
	for i := range 12 {
		wg.Go(func() {
			r, err := instancias[i%len(instancias)].Reserve(context.Background(), usuario, familia)
			if err != nil {
				t.Error(err)
				return
			}
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
	b := novaBancadaDeProvas(t)
	ua, a := b.familia(t)
	ub, fb := b.familia(t)

	for range 10 {
		b.reservar(t, ua, a).Release()
	}
	for i := 1; i < auth.FamilyProofLimit; i++ {
		if falhar(t, b.reservar(t, ua, a)) {
			t.Fatalf("a falha %d derrubou a família: as reservas devolvidas continuaram contando", i)
		}
	}
	if falhar(t, b.reservar(t, ub, fb)) {
		t.Error("as falhas da família A derrubaram a família B")
	}
	r := b.reservar(t, ua, a)
	r.Succeed()
	r.Release()
	for i := 1; i < auth.FamilyProofLimit; i++ {
		if falhar(t, b.reservar(t, ua, a)) {
			t.Fatalf("depois do sucesso, a falha %d já derrubou a família: Succeed não zerou", i)
		}
	}
}

// A conta de uma família parada há mais de um dia é varrida: a tabela não
// cresce para sempre com famílias que ninguém usa mais.
func TestFamilyProofsForgetIdleFamilies(t *testing.T) {
	b := novaBancadaDeProvas(t)
	up, parada := b.familia(t)
	uo, outra := b.familia(t)
	for i := 1; i < auth.FamilyProofLimit; i++ {
		falhar(t, b.reservar(t, up, parada))
	}
	falhar(t, b.reservar(t, uo, outra))
	b.clk.Advance(25 * time.Hour)
	if falhar(t, b.reservar(t, up, parada)) {
		t.Error("a conta de uma família parada há 25 h continuou valendo")
	}
	if n := b.tamanho(t); n != 1 {
		t.Errorf("%d linhas no contador, queria 1: a outra, parada, não foi varrida", n)
	}
}

// Sabotagem: decidir pela posição da reserva (n), em vez dos erros reais.
func TestFamilyProofsDoNotCountReleasedReservationsAsFailures(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	reservas := make([]*auth.ProofReservation, auth.FamilyProofLimit)
	for i := range reservas {
		reservas[i] = b.reservar(t, usuario, familia)
	}
	for _, reserva := range reservas[:len(reservas)-1] {
		reserva.Release()
	}
	if falhar(t, reservas[len(reservas)-1]) {
		t.Error("a única prova errada derrubou a família depois de devolver as outras reservas")
	}
	for erros := 2; erros < auth.FamilyProofLimit; erros++ {
		if falhar(t, b.reservar(t, usuario, familia)) {
			t.Fatalf("a falha real %d derrubou a família antes do limite", erros)
		}
	}
	if !falhar(t, b.reservar(t, usuario, familia)) {
		t.Error("a quinta falha real não derrubou a família")
	}
}

// Um sucesso zera o orçamento. Reservas anteriores não podem devolver vagas
// da conta nova. Sabotagem: Release procurar somente pelo id da família.
func TestFamilyProofsOldReservationCannotRefundANewBudget(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	antiga := b.reservar(t, usuario, familia)
	b.reservar(t, usuario, familia).Succeed()
	for range auth.FamilyProofLimit {
		b.reservar(t, usuario, familia)
	}
	antiga.Release()
	if !b.reservar(t, usuario, familia).Exhausted() {
		t.Error("a reserva antiga devolveu uma vaga da conta nova ainda em voo")
	}
}

// On point da família parada: com exatamente 24 h sem reserva, a linha fica.
func TestFamilyProofsKeepAFamilyIdleForExactlyTheLimit(t *testing.T) {
	b := novaBancadaDeProvas(t)
	u1, f1 := b.familia(t)
	u2, f2 := b.familia(t)
	b.reservar(t, u1, f1)
	b.clk.Advance(24 * time.Hour)
	b.reservar(t, u2, f2)
	if got := b.tamanho(t); got != 2 {
		t.Errorf("%d linhas, queria 2", got)
	}
}

// Um sucesso zera a família: as provas que estavam em voo não contam mais.
func TestFamilyProofsInFlightFailuresAfterASuccessDoNotCount(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	emVoo := make([]*auth.ProofReservation, 0, auth.FamilyProofLimit)
	for range auth.FamilyProofLimit {
		emVoo = append(emVoo, b.reservar(t, usuario, familia))
	}
	b.reservar(t, usuario, familia).Succeed()
	for i, r := range emVoo {
		if falhar(t, r) {
			t.Errorf("a falha em voo %d derrubou a família depois do sucesso", i+1)
		}
	}
}

// O sucesso de uma reserva antiga não zera a conta que nasceu depois dela.
func TestFamilyProofsOldSuccessDoesNotResetANewCount(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	velha := b.reservar(t, usuario, familia)
	b.reservar(t, usuario, familia).Succeed()
	for range auth.FamilyProofLimit - 1 {
		falhar(t, b.reservar(t, usuario, familia))
	}
	velha.Succeed()
	if !falhar(t, b.reservar(t, usuario, familia)) {
		t.Error("a falha do limite não derrubou a família: o sucesso antigo zerou a conta nova")
	}
}

// Reserva devolvida não aproxima a família do esgotamento.
func TestFamilyProofsReleasedReservationsDoNotExhaust(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	for range auth.FamilyProofLimit + 1 {
		b.reservar(t, usuario, familia).Release()
	}
	if b.reservar(t, usuario, familia).Exhausted() {
		t.Error("reservas devolvidas esgotaram a família")
	}
}

// Quem manda a prova e larga a conexão não escapa da conta: a escrita do Fail
// não segue o cancelamento da requisição. Sabotagem: gravar com o contexto da
// requisição, e a falha desconectada deixa de contar.
func TestFamilyProofsCountAFailureWhoseRequestWasCancelled(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	for i := 1; i < auth.FamilyProofLimit; i++ {
		ctx, cancelar := context.WithCancel(context.Background())
		r, err := b.provas.Reserve(ctx, usuario, familia)
		if err != nil {
			t.Fatal(err)
		}
		cancelar() // o cliente largou a conexão enquanto o servidor julgava
		if falhar(t, r) {
			t.Fatalf("a falha %d derrubou a família", i)
		}
	}
	if !falhar(t, b.reservar(t, usuario, familia)) {
		t.Error("a quinta falha não derrubou a família: as falhas de requisições canceladas não contaram")
	}
}

// Revogar a família limpa o contador dela, mas só depois de uma folga: as
// provas em voo ainda precisam da linha para contar (a 5ª derruba a família
// enquanto as reservas esgotadas já a revogaram). Sabotagem: apagar a linha na
// própria revogação, e a conta das provas em voo se perde.
func TestFamilyProofsAreSweptAfterTheFamilyIsRevoked(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	outroUsuario, outraFamilia := b.familia(t)
	emVoo := b.reservar(t, usuario, familia)
	falhar(t, b.reservar(t, outroUsuario, outraFamilia))

	b.revogar(t, familia)
	b.reservar(t, outroUsuario, outraFamilia).Release() // a varredura roda aqui

	if n := b.tamanho(t); n != 2 {
		t.Fatalf("%d linhas logo depois da revogação, queria 2: a da família revogada fica pela folga", n)
	}
	if falhar(t, emVoo) {
		t.Error("a prova em voo derrubou a família no primeiro erro")
	}

	b.clk.Advance(2 * time.Minute)
	b.reservar(t, outroUsuario, outraFamilia).Release() // a varredura roda aqui

	if n := b.tamanho(t); n != 1 {
		t.Errorf("%d linhas depois da folga, queria 1: só a da família revogada sai", n)
	}
}

// Quem chega depois da revogação não recria o contador: a reserva nasce
// esgotada e nada é gravado. Sabotagem: INSERT sem a conferência de família viva.
func TestFamilyProofsReservationOnARevokedFamilyIsExhaustedAndLeavesNoRow(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	b.revogar(t, familia)

	r := b.reservar(t, usuario, familia)

	if !r.Exhausted() {
		t.Error("a reserva de uma família revogada nasceu válida")
	}
	if falhar(t, r) {
		t.Error("Fail numa reserva de família morta pediu para derrubar de novo")
	}
	r.Succeed()
	r.Release()
	if n := b.tamanho(t); n != 0 {
		t.Errorf("%d linhas depois de reservar numa família revogada, queria 0", n)
	}
}

// A família de outro usuário não vale: o contador só nasce para o par
// (usuário, família) vivo.
func TestFamilyProofsReservationWithAnotherUsersIDIsRefused(t *testing.T) {
	b := novaBancadaDeProvas(t)
	_, familia := b.familia(t)
	outro, _ := b.familia(t)

	if !b.reservar(t, outro, familia).Exhausted() {
		t.Error("a reserva com a família de outro usuário nasceu válida")
	}
	if n := b.tamanho(t); n != 0 {
		t.Errorf("%d linhas, queria 0", n)
	}
}

// Apagar o usuário leva o contador junto (ON DELETE CASCADE).
func TestFamilyProofsAreDeletedWithTheUser(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	falhar(t, b.reservar(t, usuario, familia))

	if _, err := b.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, usuario); err != nil {
		t.Fatal(err)
	}

	if n := b.tamanho(t); n != 0 {
		t.Errorf("%d linhas depois de apagar o usuário, queria 0", n)
	}
}

// Erro de banco não é reserva: o contador não finge que há orçamento.
func TestFamilyProofsReserveFailsClosedOnDatabaseError(t *testing.T) {
	b := novaBancadaDeProvas(t)
	usuario, familia := b.familia(t)
	b.pool.Close()

	r, err := b.provas.Reserve(context.Background(), usuario, familia)

	if err == nil {
		t.Fatalf("Reserve sem banco devolveu a reserva %+v e nenhum erro", r)
	}
}

// A métrica de tamanho é o total do banco, lido na coleta.
func TestFamilyProofsCollectorReportsTheTableSize(t *testing.T) {
	b := novaBancadaDeProvas(t)
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(b.provas.Collector())
	for range 3 {
		usuario, familia := b.familia(t)
		falhar(t, b.reservar(t, usuario, familia))
	}

	familias, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	if len(familias) != 1 || familias[0].GetName() != "session_family_proofs" {
		t.Fatalf("famílias de métricas = %v, queria só session_family_proofs", familias)
	}
	if got := familias[0].GetMetric()[0].GetGauge().GetValue(); got != 3 {
		t.Errorf("session_family_proofs = %v, queria 3", got)
	}
}
