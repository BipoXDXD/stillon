package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/crypto"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

var ipKey = []byte("chave-de-teste-com-32-bytes-ok!!")

func setup(t *testing.T) (*auth.Manager, *pgxpool.Pool, *clock.Fake, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)

	userID := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		userID, "pessoa@exemplo.com")
	if err != nil {
		t.Fatalf("criar usuário de teste: %v", err)
	}
	return mgr, pool, clk, userID
}

var meta = auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}

func TestIssueThenRotate(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()

	tok1, s1, err := mgr.Issue(ctx, userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == "" {
		t.Fatal("token vazio")
	}

	tok2, s2, err := mgr.Rotate(ctx, tok1, meta)
	if err != nil {
		t.Fatalf("rotação deveria funcionar: %v", err)
	}
	if tok2 == tok1 {
		t.Error("a rotação devolveu o mesmo token")
	}
	if s2.FamilyID != s1.FamilyID {
		t.Error("a rotação mudou de família; deveria manter a mesma")
	}
	if s2.UserID != userID {
		t.Error("a sessão nova aponta para outro usuário")
	}
}

// O coração da defesa. Um token já consumido só reaparece se alguém o copiou.
// Como não há como saber se quem chegou primeiro foi o ladrão ou o dono, a
// resposta é derrubar a família inteira e exigir login novo.
func TestReusedTokenRevokesEntireFamily(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	tok1, _, _ := mgr.Issue(ctx, userID, meta)
	tok2, _, err := mgr.Rotate(ctx, tok1, meta)
	if err != nil {
		t.Fatal(err)
	}

	// O atacante usa o token antigo, que ele copiou antes da rotação.
	if _, _, err := mgr.Rotate(ctx, tok1, meta); !errors.Is(err, auth.ErrTokenReuse) {
		t.Fatalf("erro = %v, queria ErrTokenReuse", err)
	}

	// O token do usuário legítimo também morre: não dá para distinguir.
	if _, _, err := mgr.Rotate(ctx, tok2, meta); err == nil {
		t.Error("o token legítimo continuou valendo depois da detecção de reúso")
	}

	var vivas int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&vivas)
	if err != nil {
		t.Fatal(err)
	}
	if vivas != 0 {
		t.Errorf("%d sessões continuaram vivas; queria 0", vivas)
	}
}

func TestReuseIsRecordedInAuditLog(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	tok1, _, _ := mgr.Issue(ctx, userID, meta)
	_, _, _ = mgr.Rotate(ctx, tok1, meta)
	_, _, _ = mgr.Rotate(ctx, tok1, meta)

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'session.reuse_detected'`,
		userID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d registros de auditoria, queria 1", n)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	mgr, _, clk, userID := setup(t)
	ctx := context.Background()

	tok, _, _ := mgr.Issue(ctx, userID, meta)

	clk.Advance(31 * 24 * time.Hour) // TTL de refresh é 30 dias (spec §5.2)

	if _, _, err := mgr.Rotate(ctx, tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	mgr, _, _, _ := setup(t)

	if _, _, err := mgr.Rotate(context.Background(), "token-que-nunca-existiu", meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

func TestRevokedTokenIsRejected(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()

	tok, _, _ := mgr.Issue(ctx, userID, meta)
	if err := mgr.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}

	if _, _, err := mgr.Rotate(ctx, tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

// O IP nunca pode chegar em claro ao banco (spec §4.2).
func TestIPIsStoredHashed(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	if _, _, err := mgr.Issue(ctx, userID, auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}); err != nil {
		t.Fatal(err)
	}

	var ipHash []byte
	err := pool.QueryRow(ctx, `SELECT ip_hash FROM sessions WHERE user_id = $1`, userID).Scan(&ipHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(ipHash) != 32 {
		t.Errorf("ip_hash tem %d bytes, queria 32 (HMAC-SHA256)", len(ipHash))
	}
	if string(ipHash) == "203.0.113.9" {
		t.Error("o IP foi gravado em claro")
	}
}

// Trava que o hash do IP é HMAC com a chave configurada, e não um SHA-256
// qualquer: o teste acima aceitaria qualquer digest de 32 bytes, inclusive um
// hash sem chave — justamente o hash enumerável que a §4.2 proíbe.
func TestIPHashIsKeyedHMAC(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	const ip = "203.0.113.9"
	if _, _, err := mgr.Issue(ctx, userID, auth.Meta{IP: ip, UserAgent: "teste"}); err != nil {
		t.Fatal(err)
	}

	var ipHash []byte
	if err := pool.QueryRow(ctx, `SELECT ip_hash FROM sessions WHERE user_id = $1`, userID).Scan(&ipHash); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ipHash, crypto.HashIP(ipKey, ip)) {
		t.Error("ip_hash não é o HMAC-SHA256 do IP com a chave do Manager")
	}
	semChave := sha256.Sum256([]byte(ip))
	if bytes.Equal(ipHash, semChave[:]) {
		t.Error("ip_hash é SHA-256 sem chave: enumerável em minutos (spec §4.2)")
	}
}

// A ordem das checagens em Rotate é parte da defesa. O reúso tem de ser
// avaliado ANTES de revogação e expiração: depois da primeira detecção a
// família inteira está revogada, e se a revogação fosse checada primeiro o
// atacante insistindo com o mesmo token receberia ErrInvalidSession — a
// evidência do incidente sumiria do erro devolvido a quem chama.
func TestReuseIsReportedEvenAfterFamilyRevocation(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()

	tok1, _, _ := mgr.Issue(ctx, userID, meta)
	if _, _, err := mgr.Rotate(ctx, tok1, meta); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Rotate(ctx, tok1, meta); !errors.Is(err, auth.ErrTokenReuse) {
		t.Fatalf("primeira detecção: erro = %v, queria ErrTokenReuse", err)
	}

	// Agora tok1 tem used_at E revoked_at preenchidos.
	if _, _, err := mgr.Rotate(ctx, tok1, meta); !errors.Is(err, auth.ErrTokenReuse) {
		t.Errorf("erro = %v, queria ErrTokenReuse; a checagem de reúso ficou atrás da de revogação", err)
	}
}

// Duas rotações simultâneas com o mesmo token. O SELECT ... FOR UPDATE faz a
// segunda esperar a primeira commitar e então enxergar used_at preenchido:
// exatamente uma ganha, a outra é reúso. Sem o lock, as duas leem
// used_at = NULL e as duas emitem token novo — a detecção deixa de existir.
//
// A barreira não é decoração: sem ela as duas goroutines se serializam
// sozinhas (a segunda só abre a transação depois que a primeira commitou) e o
// teste passa até com o FOR UPDATE removido. A barreira garante que as duas
// transações estão abertas antes de qualquer uma ler a linha, que é o único
// momento em que a ausência do lock aparece.
type storeComBarreira struct {
	inner   auth.Store
	entrada *sync.WaitGroup
}

func (s storeComBarreira) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		s.entrada.Done()
		s.entrada.Wait() // só segue quando a outra transação também abriu
		return fn(ctx, tx)
	})
}

func TestConcurrentRotationLetsOnlyOneWin(t *testing.T) {
	mgr, pool, clk, userID := setup(t)
	ctx := context.Background()

	// A disputa é repetida: mesmo com a barreira, de vez em quando uma
	// goroutine termina a transação inteira antes de a outra ler a linha, e aí
	// a rodada não exercita o lock. Dez rodadas independentes tornam a
	// detecção praticamente certa sem tornar o teste instável — com o lock,
	// toda rodada termina em 1 vitória e 1 reúso.
	const rodadas = 10
	for rodada := range rodadas {
		tok, _, err := mgr.Issue(ctx, userID, meta)
		if err != nil {
			t.Fatal(err)
		}

		var entrada sync.WaitGroup
		entrada.Add(2)
		concorrente := auth.NewManager(
			storeComBarreira{inner: auth.NewPGStore(pool), entrada: &entrada}, clk, ipKey)

		var wg sync.WaitGroup
		erros := make([]error, 2)
		for i := range erros {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, erros[i] = concorrente.Rotate(ctx, tok, meta)
			}()
		}
		wg.Wait()

		var ok, reuso int
		for _, err := range erros {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, auth.ErrTokenReuse):
				reuso++
			default:
				t.Fatalf("rodada %d: erro inesperado: %v", rodada, err)
			}
		}
		if ok != 1 || reuso != 1 {
			t.Fatalf("rodada %d: %d rotações bem-sucedidas e %d reúsos; queria 1 e 1", rodada, ok, reuso)
		}
	}

	// Detectado o reúso, a família cai inteira — inclusive o token que a
	// rotação vencedora acabou de emitir.
	var vivas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&vivas); err != nil {
		t.Fatal(err)
	}
	if vivas != 0 {
		t.Errorf("%d sessões continuaram vivas; queria 0", vivas)
	}
}

// A chave do HMAC de IP é validada na construção (ruling R27). Sem isso, a
// chave curta só apareceria como panic de crypto.HashIP dentro do handler,
// no primeiro login em produção, em vez de na partida do processo.
func TestNewManagerRejectsShortIPKey(t *testing.T) {
	casos := map[string][]byte{
		"nil":            nil,
		"vazia":          {},
		"31 bytes":       []byte("chave-de-teste-com-31-bytes-ok!"),
		"32 bytes serve": ipKey, // não pode entrar em panic
	}
	for nome, chave := range casos {
		t.Run(nome, func(t *testing.T) {
			deveEntrarEmPanic := len(chave) < 32

			defer func() {
				r := recover()
				if deveEntrarEmPanic && r == nil {
					t.Errorf("chave de %d bytes foi aceita; queria panic", len(chave))
				}
				if !deveEntrarEmPanic && r != nil {
					t.Errorf("chave de 32 bytes entrou em panic: %v", r)
				}
			}()

			_ = auth.NewManager(nil, clock.Real{}, chave)
		})
	}
}

// Fakes para exercitar a falha de commit sem banco: só o InTx precisa mentir.
type txFalsa struct{ usadaEm time.Time }

func (t txFalsa) LockSessionByHash(context.Context, []byte) (auth.SessionRow, error) {
	// Sessão já consumida: leva Rotate ao ramo de detecção de reúso.
	return auth.SessionRow{
		ID: uuid.New(), UserID: uuid.New(), FamilyID: uuid.New(),
		ExpiresAt: t.usadaEm.Add(time.Hour), UsedAt: &t.usadaEm,
	}, nil
}

func (txFalsa) InsertSession(context.Context, auth.SessionRow) error { return nil }
func (txFalsa) MarkSessionUsed(context.Context, uuid.UUID, time.Time) error {
	return nil
}
func (txFalsa) RevokeFamily(context.Context, uuid.UUID, time.Time) error { return nil }
func (txFalsa) RevokeByHash(context.Context, []byte, time.Time) error    { return nil }
func (txFalsa) InsertAudit(context.Context, uuid.UUID, string, []byte, time.Time) error {
	return nil
}

type storeQueFalhaNoCommit struct {
	agora time.Time
	err   error
}

func (s storeQueFalhaNoCommit) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	if err := fn(ctx, txFalsa{usadaEm: s.agora}); err != nil {
		return err
	}
	return s.err // o callback devolveu nil; é o commit que falha
}

// Detectado o reúso, a revogação da família e a auditoria só valem se a
// transação commitar. Se o commit falha, nada disso persistiu, e devolver
// ErrTokenReuse afirmaria uma defesa que não aconteceu — quem chama tem de
// receber o erro da transação.
func TestRotateReturnsCommitErrorInsteadOfReuse(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	falhaNoCommit := errors.New("commit falhou")

	mgr := auth.NewManager(
		storeQueFalhaNoCommit{agora: agora, err: falhaNoCommit},
		clock.NewFake(agora), ipKey)

	_, _, err := mgr.Rotate(context.Background(), "qualquer-token", meta)
	if !errors.Is(err, falhaNoCommit) {
		t.Errorf("erro = %v, queria o erro do commit", err)
	}
	if errors.Is(err, auth.ErrTokenReuse) {
		t.Error("devolveu ErrTokenReuse por cima de um commit falho: afirma uma revogação que não persistiu")
	}
}
