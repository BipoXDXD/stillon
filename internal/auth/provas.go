package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// As provas das rotas autenticadas: a troca de senha e o
// enroll, confirm, disable, rotate e rotate/confirm do 2FA. Antes elas
// consultavam e alimentavam o bloqueio da CONTA, e um anônimo que mandasse
// cinco senhas erradas ao login a cada 15 min desligava, junto com o login,
// tudo o que o dono logado podia fazer com a própria credencial.
//
// Agora cada família de sessão tem o seu contador, no Postgres
// (session_family_proofs). A quinta prova errada derruba a família (a rota
// responde 401 unauthorized): quem tem a sessão e chuta a senha é expulso, e o
// dono não é trancado por isso. A reserva vem ANTES da verificação: N
// requisições simultâneas gastam N provas, e as que passam do limite nem
// chegam a verificar. Cada passo é uma instrução SQL única (INSERT ... ON
// CONFLICT DO UPDATE, UPDATE ... RETURNING), então nenhum lê e depois grava e
// o orçamento vale entre processos. Cada instrução roda numa transação no
// escopo da RLS do usuário (storage.InUserTx, migration 00029); a métrica, que
// conta as famílias de todos, no escopo do sondador.
//
// No banco de propósito: o contador em memória zerava num restart ou deploy e
// devolvia as cinco tentativas a quem tem uma sessão roubada. Família viva nunca
// perde o contador, por mais parada que esteja: a vida dela já é limitada pelo
// prazo do refresh, e só o sucesso o zera. A linha de uma família que deixou de
// existir (revogada ou vencida) sai na retenção periódica
// (storage.ApplyRetention), uma folga depois, porque as provas em voo ainda a
// usam; a do usuário apagado, pela FK (migration 00028). A varredura fica fora
// do caminho da reserva: manutenção que falha não derruba as rotas de prova.
//
// O resíduo: uma reserva em voo quando o processo cai fica contada como prova
// gasta até a família acabar, o que só aproxima uma família da expulsão. Nunca
// a afasta.

// FamilyProofLimit é quantas provas erradas uma família aguenta; a última a
// derruba. É o mesmo limiar do bloqueio da conta.
const FamilyProofLimit = LockThreshold

// prazoDoBanco limita cada ida ao banco de uma prova. Fail, Succeed e Release
// rodam SEM o cancelamento da requisição (ver ProofReservation): sem prazo
// próprio, um banco parado seguraria a requisição para sempre.
const prazoDoBanco = 5 * time.Second

// FamilyProofs é o contador de provas por família de sessão, no banco. O valor
// zero não serve: use NewFamilyProofs.
type FamilyProofs struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewFamilyProofs(pool *pgxpool.Pool, clk clock.Clock) *FamilyProofs {
	return &FamilyProofs{pool: pool, clk: clk}
}

// ProofReservation é uma prova reservada. Quem reserva chama exatamente um
// entre Fail e Succeed quando julga a prova, e sempre Release no fim (com
// defer): depois de Fail ou Succeed, Release não faz nada.
//
// Fail, Succeed e Release escrevem com o contexto da reserva SEM o
// cancelamento: quem larga a conexão logo depois de mandar a prova não pode
// escapar da conta. Se a escrita do Fail se perdesse com o cliente, chutar e
// desconectar daria tentativas sem fim.
type ProofReservation struct {
	f       *FamilyProofs
	base    context.Context
	usuario uuid.UUID
	familia uuid.UUID
	geracao uuid.UUID
	n       int64
	morta   bool
	fechada atomic.Bool
}

// Reserve reserva uma prova para a família do usuário. A reserva pode nascer
// esgotada (Exhausted): a família já errou o limite, ou já não está viva, e
// quem chama não verifica nada. Erro de banco não é reserva: quem chama
// responde 500 sem verificar.
func (f *FamilyProofs) Reserve(ctx context.Context, userID, familyID uuid.UUID) (*ProofReservation, error) {
	agora := f.clk.Now()
	geracao, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("gerar geração do contador de provas: %w", err)
	}
	var linha db.ReserveFamilyProofRow
	err = storage.InUserTx(ctx, f.pool, userID, func(q *db.Queries) error {
		var err error
		linha, err = q.ReserveFamilyProof(ctx, db.ReserveFamilyProofParams{
			FamilyID: familyID, UserID: userID, Generation: geracao, Now: agora,
		})
		return err
	})
	r := &ProofReservation{f: f, base: context.WithoutCancel(ctx), usuario: userID, familia: familyID}
	switch {
	case errors.Is(err, pgx.ErrNoRows), violaChaveEstrangeira(err):
		// Sem linha: a família não está viva. Com a FK violada, o usuário foi
		// apagado entre a guarda da sessão e a reserva: o mesmo. Nada a fechar
		// depois.
		r.morta = true
		r.fechada.Store(true)
		return r, nil
	case err != nil:
		return nil, fmt.Errorf("reservar prova da família: %w", err)
	}
	r.geracao, r.n = linha.Generation, linha.Reserved
	return r, nil
}

func violaChaveEstrangeira(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" // foreign_key_violation
}

// FamilyID é a família da reserva.
func (r *ProofReservation) FamilyID() uuid.UUID { return r.familia }

// Exhausted diz que a família já tinha errado o limite quando esta reserva
// foi feita, ou já estava morta: não verifique nada, derrube a família (de
// novo, se preciso).
func (r *ProofReservation) Exhausted() bool { return r.morta || r.n > FamilyProofLimit }

// Fail registra a prova errada, que fica contada. Devolve true quando ela é
// a do limite: quem chama derruba a família. Erro de banco é erro: a prova
// errada não foi contada, e quem chama responde 500 em vez de seguir.
func (r *ProofReservation) Fail() (derrubar bool, err error) {
	if !r.fechada.CompareAndSwap(false, true) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(r.base, prazoDoBanco)
	defer cancel()
	// A posição da reserva conta chamadas em voo, não erros. Um Argon2
	// saturado devolve sua reserva e não pode aproximar a família da expulsão.
	var erros int64
	err = storage.InUserTx(ctx, r.f.pool, r.usuario, func(q *db.Queries) error {
		var err error
		erros, err = q.FailFamilyProof(ctx, db.FailFamilyProofParams{
			Now: r.f.clk.Now(), FamilyID: r.familia, Generation: r.geracao,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // um sucesso ou a revogação já encerrou a vida desta prova em voo
	}
	if err != nil {
		return false, fmt.Errorf("registrar prova errada da família: %w", err)
	}
	return erros >= FamilyProofLimit, nil
}

// Succeed zera a família: as provas pedidas conferiram. Se a escrita falhar, a
// conta fica como estava, o que só aproxima a família do limite; o erro vai
// para o log e a prova certa segue valendo.
func (r *ProofReservation) Succeed() {
	if !r.fechada.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(r.base, prazoDoBanco)
	defer cancel()
	if err := storage.InUserTx(ctx, r.f.pool, r.usuario, func(q *db.Queries) error {
		return q.ClearFamilyProofs(ctx, db.ClearFamilyProofsParams{FamilyID: r.familia, Generation: r.geracao})
	}); err != nil {
		slog.ErrorContext(r.base, "zerar contador de provas da família", "erro", err, "family_id", r.familia)
	}
}

// Release devolve a reserva de quem não chegou a errar prova (corpo inválido
// depois da reserva, 403, 500, sessão caída). Depois de Fail ou Succeed, não
// faz nada. Se a escrita falhar, a reserva fica contada como gasta, o que só
// aproxima a família do limite; o erro vai para o log.
func (r *ProofReservation) Release() {
	if !r.fechada.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(r.base, prazoDoBanco)
	defer cancel()
	if err := storage.InUserTx(ctx, r.f.pool, r.usuario, func(q *db.Queries) error {
		return q.ReleaseFamilyProof(ctx, db.ReleaseFamilyProofParams{FamilyID: r.familia, Generation: r.geracao})
	}); err != nil {
		slog.ErrorContext(r.base, "devolver reserva de prova da família", "erro", err, "family_id", r.familia)
	}
}

// Collector expõe o tamanho da tabela (famílias com contador): todo acúmulo
// tem limpeza e uma métrica, e é esta que mostra se a retenção das famílias
// mortas (revogadas ou vencidas) está segurando. É o total do banco, não o
// deste processo: agregue com max. A contagem roda na coleta; se falhar, a
// métrica some daquela coleta em vez de mentir com um zero.
func (f *FamilyProofs) Collector() prometheus.Collector {
	return &coletorDeProvas{
		f: f,
		tamanho: prometheus.NewDesc("session_family_proofs",
			"Famílias de sessão com contador de provas erradas no banco.", nil, nil),
	}
}

type coletorDeProvas struct {
	f       *FamilyProofs
	tamanho *prometheus.Desc
}

func (c *coletorDeProvas) Describe(ch chan<- *prometheus.Desc) { ch <- c.tamanho }

func (c *coletorDeProvas) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), prazoDoBanco)
	defer cancel()
	n, err := c.f.contar(ctx)
	if err != nil {
		slog.Error("contar contadores de provas das famílias", "erro", err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.tamanho, prometheus.GaugeValue, float64(n))
}

// contar é o total de famílias com contador, de todos os usuários: por isso
// roda no escopo do sondador, o único da RLS que enxerga a tabela inteira.
func (f *FamilyProofs) contar(ctx context.Context) (int64, error) {
	var n int64
	err := storage.InProberTx(ctx, f.pool, func(q *db.Queries) error {
		var err error
		n, err = q.CountFamilyProofs(ctx)
		return err
	})
	return n, err
}
