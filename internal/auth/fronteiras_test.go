package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
)

var errBoom = errors.New("falha de teste")

// Com o semáforo cheio e o prazo vencido, ReserveArgon2 devolve ErrArgon2Busy
// e não um erro de cancelamento: quem chama responde como ao semáforo cheio. A
// função devolvida é segura de chamar e não solta a vaga que outra chamada
// segura.
func TestReserveArgon2PrazoVenceDevolveBusy(t *testing.T) {
	ocuparArgon2(t, 1, 20*time.Millisecond)

	release, err := auth.ReserveArgon2(context.Background())

	if !errors.Is(err, auth.ErrArgon2Busy) {
		t.Fatalf("erro = %v, queria ErrArgon2Busy", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("erro = %v: o prazo venceu, o ctx não foi cancelado", err)
	}
	release()
	if _, err := auth.ReserveArgon2(context.Background()); !errors.Is(err, auth.ErrArgon2Busy) {
		t.Errorf("depois de release() da chamada recusada, erro = %v: ele soltou a vaga alheia", err)
	}
}

// tentativasFalsas é um Attempts que só conta as reservas pedidas.
type tentativasFalsas struct {
	reservas int
	erro     error
}

func (a *tentativasFalsas) Reserve(context.Context) (bool, error) {
	a.reservas++
	return true, a.erro
}
func (a *tentativasFalsas) Refund(context.Context) error { return nil }
func (a *tentativasFalsas) Reset(context.Context) error  { return nil }

// Sem vaga do Argon2id, o login responde ErrArgon2Busy sem gastar tentativa
// do dono da conta.
func TestCheckPasswordSemVagaNaoReservaTentativa(t *testing.T) {
	ocuparArgon2(t, 1, 20*time.Millisecond)
	tentativas := &tentativasFalsas{}

	ok, needsRehash, err := auth.CheckPassword(context.Background(), tentativas, auth.User{}, "qualquer-senha-longa")

	if ok || needsRehash || !errors.Is(err, auth.ErrArgon2Busy) {
		t.Errorf("(ok, needsRehash, err) = (%v, %v, %v), queria (false, false, ErrArgon2Busy)", ok, needsRehash, err)
	}
	if tentativas.reservas != 0 {
		t.Errorf("Reserve foi chamado %d vezes, queria 0", tentativas.reservas)
	}
}

// Erro ao reservar a tentativa aborta o login com o erro original.
func TestCheckPasswordErroDaReservaPropaga(t *testing.T) {
	tentativas := &tentativasFalsas{erro: errBoom}

	ok, needsRehash, err := auth.CheckPassword(context.Background(), tentativas, auth.User{}, "qualquer-senha-longa")

	if ok || needsRehash || !errors.Is(err, errBoom) {
		t.Errorf("(ok, needsRehash, err) = (%v, %v, %v), queria (false, false, errBoom)", ok, needsRehash, err)
	}
}

// Fail só conta uma vez por reserva: chamá-lo de novo não aproxima a família
// do limite de 5 erros.
func TestFailDuplicadoNaoContaDuasVezes(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()

	r := provas.Reserve(familia)
	if r.Fail() {
		t.Fatal("o primeiro erro da família derrubou a sessão")
	}
	if r.Fail() {
		t.Fatal("o Fail repetido da mesma reserva derrubou a sessão")
	}
	for i := 2; i <= 4; i++ {
		if provas.Reserve(familia).Fail() {
			t.Fatalf("o erro %d derrubou a família antes do 5º erro real", i)
		}
	}
	if !provas.Reserve(familia).Fail() {
		t.Error("o 5º erro real não derrubou a família")
	}
}

// Succeed numa reserva já fechada por Fail não zera a conta: senão uma prova
// certa tardia apagaria os erros e burlaria o limite.
func TestSucceedDepoisDeFailNaoZera(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()

	r := provas.Reserve(familia)
	if r.Fail() {
		t.Fatal("o primeiro erro da família derrubou a sessão")
	}
	r.Succeed()
	for i := 2; i <= 4; i++ {
		if provas.Reserve(familia).Fail() {
			t.Fatalf("o erro %d derrubou a família antes do 5º erro real", i)
		}
	}
	if !provas.Reserve(familia).Fail() {
		t.Error("Succeed depois de Fail zerou a contagem: o 5º erro real não derrubou a família")
	}
}

// O teto de 256 é em code points, não em bytes, e nunca parte um caractere.
func TestTruncateUserAgent(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
		querido string
	}{
		{"vazio", "", ""},
		{"256 runas ASCII (on point)", strings.Repeat("a", 256), strings.Repeat("a", 256)},
		{"257 runas ASCII (off point)", strings.Repeat("a", 257), strings.Repeat("a", 256)},
		{"256 runas de 2 bytes cabem (512 bytes)", strings.Repeat("é", 256), strings.Repeat("é", 256)},
		{"257 runas, a última de 2 bytes", strings.Repeat("a", 255) + "éé", strings.Repeat("a", 255) + "é"},
		{"300 runas de 4 bytes", strings.Repeat("😀", 300), strings.Repeat("😀", 256)},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := auth.TruncateUserAgent(c.entrada)
			if got != c.querido {
				t.Errorf("%d runas e %d bytes, queria %d runas e %d bytes",
					utf8.RuneCountInString(got), len(got), utf8.RuneCountInString(c.querido), len(c.querido))
			}
			if !utf8.ValidString(got) {
				t.Error("o resultado não é UTF-8 válido")
			}
		})
	}
}

// Token com assinatura, exp e demais claims válidas mas sub que não é UUID é
// inválido: nunca vira o UUID zero.
func TestParseTokenSubjectNaoUUID(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	assinar := func(sub string, extra jwt.MapClaims) string {
		t.Helper()
		claims := jwt.MapClaims{"sub": sub, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()}
		for k, v := range extra {
			claims[k] = v
		}
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	acesso := jwt.MapClaims{"sid": uuid.NewString()}
	desafio := jwt.MapClaims{"aud": "totp-challenge", "pws": "carimbo", "cat": now.UnixMicro()}

	t.Run("controle: sub UUID é aceito", func(t *testing.T) {
		if _, err := auth.ParseAccessToken(jwtSecret, assinar(uuid.NewString(), acesso), now); err != nil {
			t.Errorf("access token com sub UUID: %v", err)
		}
		if _, err := auth.ParseChallengeToken(jwtSecret, assinar(uuid.NewString(), desafio), now); err != nil {
			t.Errorf("desafio com sub UUID: %v", err)
		}
	})
	t.Run("access token", func(t *testing.T) {
		_, err := auth.ParseAccessToken(jwtSecret, assinar("nao-e-uuid", acesso), now)
		if !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("erro = %v, queria ErrInvalidToken", err)
		}
	})
	t.Run("desafio", func(t *testing.T) {
		_, err := auth.ParseChallengeToken(jwtSecret, assinar("nao-e-uuid", desafio), now)
		if !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("erro = %v, queria ErrInvalidToken", err)
		}
	})
}

// sessoesReusoFalsas é um Store em memória: uma única sessão já usada, para
// exercitar a detecção de reúso do Rotate sem banco. Os métodos de Tx que a
// rotação com reúso não chama ficam na interface embutida (nil) e entrariam em
// pânico se chamados.
type sessoesReusoFalsas struct {
	linha       auth.SessionRow
	erroRevoga  error
	erroAuditar error
}

func (s *sessoesReusoFalsas) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return fn(ctx, &txReusoFalsa{s: s})
}

type txReusoFalsa struct {
	auth.Tx
	s *sessoesReusoFalsas
}

func (x *txReusoFalsa) SessionUserByHash(context.Context, []byte) (uuid.UUID, error) {
	return x.s.linha.UserID, nil
}
func (x *txReusoFalsa) LockUserSessions(context.Context, uuid.UUID) error { return nil }
func (x *txReusoFalsa) LockSessionByHash(context.Context, []byte) (auth.SessionRow, error) {
	return x.s.linha, nil
}
func (x *txReusoFalsa) RevokeFamily(context.Context, uuid.UUID, time.Time) error {
	return x.s.erroRevoga
}
func (x *txReusoFalsa) InsertAudit(context.Context, uuid.UUID, string, []byte, time.Time) error {
	return x.s.erroAuditar
}

// Se a defesa contra reúso não persistiu (a revogação da família ou a
// auditoria falhou), Rotate devolve o erro e não ErrTokenReuse, que afirmaria
// uma defesa que não houve.
func TestRotateReusoQueNaoPersisteDevolveOErro(t *testing.T) {
	agora := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	usadoEm := agora.Add(-time.Minute)
	casos := []struct {
		nome        string
		erroRevoga  error
		erroAuditar error
		querErro    error
	}{
		{"revogar a família falha", errBoom, nil, errBoom},
		{"auditar falha", nil, errBoom, errBoom},
		{"controle: tudo persiste", nil, nil, auth.ErrTokenReuse},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			store := &sessoesReusoFalsas{
				linha: auth.SessionRow{
					ID: uuid.New(), UserID: uuid.New(), FamilyID: uuid.New(),
					ExpiresAt: agora.Add(time.Hour), UsedAt: &usadoEm,
				},
				erroRevoga: c.erroRevoga, erroAuditar: c.erroAuditar,
			}
			m := auth.NewManager(store, clock.NewFake(agora), []byte(strings.Repeat("k", 32)))

			_, _, err := m.Rotate(context.Background(), "refresh-ja-usado", auth.Meta{IP: "203.0.113.7"})

			if !errors.Is(err, c.querErro) {
				t.Errorf("erro = %v, queria %v", err, c.querErro)
			}
			if c.querErro == errBoom && errors.Is(err, auth.ErrTokenReuse) {
				t.Errorf("erro = %v: afirmou ErrTokenReuse sem a defesa persistida", err)
			}
		})
	}
}
