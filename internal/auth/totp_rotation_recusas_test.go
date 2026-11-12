package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// Os testes deste arquivo fixam cada caminho de RECUSA da troca de autenticador
// (StartRotation e ConfirmRotation): o erro devolvido, a ordem das checagens e
// que nada é gravado, contadores de bloqueio incluídos. Uma recusa não conta
// como tentativa errada de senha: failed_attempts e locked_until só zeram no
// sucesso (TestRotationClearsTheAccountLock).

// estadoDaConta é tudo o que uma troca pode tocar. As recusas têm de deixá-lo
// igual ao que era.
type estadoDaConta struct {
	ativo      []byte
	pendente   []byte
	familia    string
	tentativas int
	bloqueio   string
	passo      int64
	codigos    int
	iniciadas  int
	trocadas   int
	recuperada int
}

func fotografar(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) estadoDaConta {
	t.Helper()
	var e estadoDaConta
	var fam *uuid.UUID
	var ate *time.Time
	var passo *int64
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc, totp_pending_secret_enc, totp_pending_family_id, failed_attempts, locked_until, totp_last_timestep
		   FROM users WHERE id = $1`, userID).
		Scan(&e.ativo, &e.pendente, &fam, &e.tentativas, &ate, &passo); err != nil {
		t.Fatal(err)
	}
	if fam != nil {
		e.familia = fam.String()
	}
	if ate != nil {
		e.bloqueio = ate.UTC().Format(time.RFC3339Nano)
	}
	e.passo = -1
	if passo != nil {
		e.passo = *passo
	}
	e.codigos = quantosCodigos(t, pool, userID)
	e.iniciadas = eventosDeAuditoria(t, pool, userID, "totp.rotation_started")
	e.trocadas = eventosDeAuditoria(t, pool, userID, "totp.rotated")
	e.recuperada = eventosDeAuditoria(t, pool, userID, "totp.recovery_used")
	return e
}

// semEfeito afirma que a conta ficou exatamente como antes.
func semEfeito(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, antes estadoDaConta) {
	t.Helper()
	depois := fotografar(t, pool, userID)
	if string(depois.ativo) != string(antes.ativo) || string(depois.pendente) != string(antes.pendente) ||
		depois.familia != antes.familia || depois.tentativas != antes.tentativas || depois.bloqueio != antes.bloqueio ||
		depois.passo != antes.passo || depois.codigos != antes.codigos || depois.iniciadas != antes.iniciadas ||
		depois.trocadas != antes.trocadas || depois.recuperada != antes.recuperada {
		t.Errorf("a recusa mexeu na conta:\n antes  %+v\n depois %+v", antes, depois)
	}
}

// bloquearConta deixa o contador e o bloqueio num valor que uma recusa não pode
// mexer: 3 falhas e bloqueio ainda vigente.
func bloquearConta(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET failed_attempts = 3, locked_until = $2 WHERE id = $1`,
		userID, inicioDaTroca.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// novoUsuarioTOTP cria mais um usuário no banco do teste. Os testes de recusa
// rodam um caso por usuário num banco só, para não pagar uma restauração do
// snapshot por caso.
func novoUsuarioTOTP(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func passoDe(em time.Time) int64 { return em.Unix() / 30 }

func gravarUltimoPasso(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, passo int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET totp_last_timestep = $2 WHERE id = $1`, userID, passo); err != nil {
		t.Fatal(err)
	}
}

func TestStartRotationRefusalsChangeNothing(t *testing.T) {
	const email = "totp@exemplo.com"
	casos := []struct {
		nome    string
		preparo func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID, familia uuid.UUID) (codigo string, em time.Time)
		erro    error
	}{
		{
			nome: "código errado",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, _ uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				errado := "000000"
				if errado == codigoEm(t, segredoDeTroca, inicioDaTroca) {
					errado = "111111"
				}
				return errado, inicioDaTroca
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "código vazio",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, _ uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				return "", inicioDaTroca
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "replay do passo já usado",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, _ uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarUltimoPasso(t, pool, userID, passoDe(inicioDaTroca))
				return codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "código de um passo anterior ao último usado",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, _ uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarUltimoPasso(t, pool, userID, passoDe(inicioDaTroca))
				return codigoEm(t, segredoDeTroca, inicioDaTroca.Add(-30*time.Second)), inicioDaTroca
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "código de recuperação já gasto",
			preparo: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID, _ uuid.UUID) (string, time.Time) {
				_, codigos := ligarComCodigos(t, svc, userID, inicioDaTroca)
				if err := svc.Verify(context.Background(), userID, codigos[0], inicioDaTroca); err != nil {
					t.Fatal(err)
				}
				return codigos[0], inicioDaTroca
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "2FA desligado",
			preparo: func(*testing.T, *auth.TOTP, *pgxpool.Pool, uuid.UUID, uuid.UUID) (string, time.Time) {
				return "000000", inicioDaTroca
			},
			erro: auth.ErrTOTPNotEnrolled,
		},
		{
			// Com a família morta, nem o código certo é gasto. A transação
			// desfaz tudo nas duas ordens: a ORDEM família→prova é fixada pelo
			// caso com o código errado, logo abaixo.
			nome: "família morta, com o código certo",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, familia uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca
			},
			erro: auth.ErrInvalidSession,
		},
		{
			// A família é conferida ANTES da prova: com a ordem invertida, o
			// código errado devolveria ErrTOTPInvalid.
			nome: "família morta, com o código errado",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, familia uuid.UUID) (string, time.Time) {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return "000000", inicioDaTroca
			},
			erro: auth.ErrInvalidSession,
		},
		{
			// E a família antes do 2FA ligado.
			nome: "família morta, com o 2FA desligado",
			preparo: func(t *testing.T, _ *auth.TOTP, pool *pgxpool.Pool, userID, familia uuid.UUID) (string, time.Time) {
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return "000000", inicioDaTroca
			},
			erro: auth.ErrInvalidSession,
		},
	}
	svc, pool, _ := setupTOTP(t)
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			userID := novoUsuarioTOTP(t, pool)
			familia := familiaViva(t, pool, userID, inicioDaTroca)
			codigo, em := caso.preparo(t, svc, pool, userID, familia)
			bloquearConta(t, pool, userID)
			antes := fotografar(t, pool, userID)

			if _, _, err := svc.StartRotation(context.Background(), userID, familia, email, codigo, em); !errors.Is(err, caso.erro) {
				t.Fatalf("devolveu %v, queria %v", err, caso.erro)
			}
			semEfeito(t, pool, userID, antes)
		})
	}
}

// O mesmo código do app, apresentado ao mesmo tempo por várias chamadas, inicia
// uma troca só: as outras acham o passo já gasto sob a trava da linha.
func TestConcurrentStartRotationsAcceptTheCodeOnce(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	codigo := codigoEm(t, segredoDeTroca, inicioDaTroca)

	aceitas, recusadas := disputarOperacao(t, pool, travaDeSessoes, userID, auth.ErrTOTPInvalid, func() error {
		_, _, err := svc.StartRotation(context.Background(), userID, familia, "totp@exemplo.com", codigo, inicioDaTroca)
		return err
	})

	if aceitas != 1 || recusadas != verificacoesConcorrentes-1 {
		t.Errorf("%d aceitas e %d recusadas, queria 1 e %d", aceitas, recusadas, verificacoesConcorrentes-1)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotation_started"); n != 1 {
		t.Errorf("%d eventos totp.rotation_started, queria 1", n)
	}
	if e := fotografar(t, pool, userID); e.passo != passoDe(inicioDaTroca) || len(e.pendente) == 0 {
		t.Errorf("passo = %d, pendente com %d bytes", e.passo, len(e.pendente))
	}
}

// travaDeSessoes é a trava que a troca toma primeiro: segurá-la faz todas as
// chamadas pararem no mesmo ponto, antes de qualquer leitura sob trava de linha.
const travaDeSessoes = `SELECT pg_advisory_xact_lock(hashtextextended('stillon.sessions:' || $1::uuid::text, 0))`

func TestConfirmRotationRefusalsChangeNothing(t *testing.T) {
	casos := []struct {
		nome string
		// prepara roda depois da troca iniciada e devolve o rotation, o código e
		// a família com que a confirmação é chamada.
		prepara func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, novo string, em time.Time) (rotation, codigo string, fam uuid.UUID)
		erro    error
	}{
		{
			nome: "código errado",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				errado := "000000"
				if errado == codigoEm(t, novo, em) {
					errado = "111111"
				}
				return rotacaoAtual(t, pool, userID), errado, familia
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "código vazio",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, _ string, _ time.Time) (string, string, uuid.UUID) {
				return rotacaoAtual(t, pool, userID), "", familia
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			nome: "código do app antigo",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, _ string, em time.Time) (string, string, uuid.UUID) {
				return rotacaoAtual(t, pool, userID), codigoEm(t, segredoDeTroca, em), familia
			},
			erro: auth.ErrTOTPInvalid,
		},
		{
			// Rotation, família e 2FA vêm ANTES do código: com o código certo,
			// a resposta é a de "sem troca".
			nome: "rotation de outra troca, com o código certo",
			prepara: func(t *testing.T, _ *pgxpool.Pool, _, familia uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				return "00112233445566778899aabbccddeeff", codigoEm(t, novo, em), familia
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			nome: "rotation de outra troca, com o código errado",
			prepara: func(_ *testing.T, _ *pgxpool.Pool, _, familia uuid.UUID, _ string, _ time.Time) (string, string, uuid.UUID) {
				return "00112233445566778899aabbccddeeff", "000000", familia
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			nome: "pendente de outra família, com o código certo",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, _ uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				return rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), familiaViva(t, pool, userID, inicioDaTroca)
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			nome: "pendente de outra família, com o código errado",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, _ uuid.UUID, _ string, _ time.Time) (string, string, uuid.UUID) {
				return rotacaoAtual(t, pool, userID), "000000", familiaViva(t, pool, userID, inicioDaTroca)
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			nome: "2FA desligado com pendente, com o código certo",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				if _, err := pool.Exec(context.Background(), `UPDATE users SET totp_confirmed_at = NULL WHERE id = $1`, userID); err != nil {
					t.Fatal(err)
				}
				return rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), familia
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			// A guarda do 2FA é lida antes da família: sem ela, a família morta
			// responderia 401 a quem não tem troca nenhuma.
			nome: "2FA desligado com pendente e família morta",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				if _, err := pool.Exec(context.Background(), `UPDATE users SET totp_confirmed_at = NULL WHERE id = $1`, userID); err != nil {
					t.Fatal(err)
				}
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), familia
			},
			erro: auth.ErrTOTPNoRotation,
		},
		{
			nome: "família morta, com o código certo",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, novo string, em time.Time) (string, string, uuid.UUID) {
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), familia
			},
			erro: auth.ErrInvalidSession,
		},
		{
			// O código é conferido ANTES da família: o 401 de código errado não
			// revela se a família ainda vive.
			nome: "família morta, com o código errado",
			prepara: func(t *testing.T, pool *pgxpool.Pool, userID, familia uuid.UUID, _ string, _ time.Time) (string, string, uuid.UUID) {
				revogarFamilia(t, pool, familia, inicioDaTroca)
				return rotacaoAtual(t, pool, userID), "000000", familia
			},
			erro: auth.ErrTOTPInvalid,
		},
	}
	svc, pool, _ := setupTOTP(t)
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			userID := novoUsuarioTOTP(t, pool)
			familia := familiaViva(t, pool, userID, inicioDaTroca)
			novo := iniciarTroca(t, svc, pool, userID, familia)
			em := inicioDaTroca.Add(30 * time.Second)
			rotation, codigo, fam := caso.prepara(t, pool, userID, familia, novo, em)
			bloquearConta(t, pool, userID)
			antes := fotografar(t, pool, userID)

			if _, err := svc.ConfirmRotation(context.Background(), userID, fam, rotation, codigo, em); !errors.Is(err, caso.erro) {
				t.Fatalf("devolveu %v, queria %v", err, caso.erro)
			}
			semEfeito(t, pool, userID, antes)
		})
	}
}

// Um usuário que não existe: o Start confere a família viva antes de ler o
// fator, então responde como sessão inválida; o Confirm lê o usuário primeiro e
// responde ErrNotFound.
func TestRotationUnknownUser(t *testing.T) {
	svc, _, _ := setupTOTP(t)
	ctx := context.Background()
	if _, _, err := svc.StartRotation(ctx, uuid.New(), uuid.New(), "totp@exemplo.com", "000000", inicioDaTroca); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("StartRotation devolveu %v, queria ErrInvalidSession", err)
	}
	if _, err := svc.ConfirmRotation(ctx, uuid.New(), uuid.New(), "00112233445566778899aabbccddeeff", "000000", inicioDaTroca); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("ConfirmRotation devolveu %v, queria ErrNotFound", err)
	}
}

// A mesma confirmação repetida depois de aceita (o replay do código e do
// rotation) é "sem troca": o pendente já foi promovido e os códigos de
// recuperação gerados na primeira continuam os vigentes.
func TestConfirmRotationReplayIsRefused(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	em := inicioDaTroca.Add(30 * time.Second)
	rotation, codigo := rotacaoAtual(t, pool, userID), codigoEm(t, novo, em)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotation, codigo, em); err != nil {
		t.Fatal(err)
	}
	bloquearConta(t, pool, userID)
	antes := fotografar(t, pool, userID)

	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotation, codigo, em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("repetir a confirmação devolveu %v, queria ErrTOTPNoRotation", err)
	}
	semEfeito(t, pool, userID, antes)
}

// Duas confirmações simultâneas da mesma troca promovem o pendente uma vez: a
// que perde acha o pendente já promovido sob a guarda do UPDATE, e nada do
// segundo caminho (códigos de recuperação, revogação, auditoria) roda.
func TestConcurrentConfirmRotationsPromoteOnce(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	em := inicioDaTroca.Add(30 * time.Second)
	rotation, codigo := rotacaoAtual(t, pool, userID), codigoEm(t, novo, em)

	var mu sync.Mutex
	var vencedores [][]string
	aceitas, recusadas := disputarOperacao(t, pool, travaDeSessoes, userID, auth.ErrTOTPNoRotation, func() error {
		codigos, err := svc.ConfirmRotation(context.Background(), userID, familia, rotation, codigo, em)
		if err == nil {
			mu.Lock()
			vencedores = append(vencedores, codigos)
			mu.Unlock()
		}
		return err
	})

	if aceitas != 1 || recusadas != verificacoesConcorrentes-1 {
		t.Fatalf("%d aceitas e %d recusadas, queria 1 e %d", aceitas, recusadas, verificacoesConcorrentes-1)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotated"); n != 1 {
		t.Errorf("%d eventos totp.rotated, queria 1", n)
	}
	if n := quantosCodigos(t, pool, userID); n != len(vencedores[0]) {
		t.Errorf("%d códigos de recuperação gravados, queria os %d da confirmação vencedora", n, len(vencedores[0]))
	}
	if err := svc.Verify(context.Background(), userID, vencedores[0][0], em); err != nil {
		t.Errorf("o código de recuperação da confirmação vencedora não vale: %v", err)
	}
}
