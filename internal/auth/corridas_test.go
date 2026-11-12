package auth_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Corridas provocadas sem goroutine, sem sleep e sem torcer pela ordem: o teste
// roda a escrita concorrente de forma síncrona, exatamente entre a leitura e a
// escrita que a produção separa. Na Manager, um Store que decora a Tx; no TOTP,
// que fala direto com o pool, um tracer do pgx que dispara a escrita logo antes
// da instrução alvo sair da conexão.

// --- A7: a sessão some entre a leitura sem trava e a leitura travada --------

type storeQueApagaNoMeio struct {
	inner  auth.Store
	apagar func()
}

func (s storeQueApagaNoMeio) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		return fn(ctx, txQueApagaNoMeio{Tx: tx, apagar: s.apagar})
	})
}

type txQueApagaNoMeio struct {
	auth.Tx
	apagar func()
}

// SessionUserByHash é a primeira leitura de travarSessao, a que não trava. A
// escrita concorrente entra logo depois dela e antes de LockUserSessions e de
// LockSessionByHash.
func (t txQueApagaNoMeio) SessionUserByHash(ctx context.Context, hash []byte) (uuid.UUID, error) {
	id, err := t.Tx.SessionUserByHash(ctx, hash)
	t.apagar()
	return id, err
}

// Se a sessão some entre as duas leituras, o resultado visível é o de uma
// sessão que não existe: ErrInvalidSession na rotação (401) e nil no logout,
// nunca um erro de banco (500). O espectador, outra família do mesmo usuário,
// não pode ser tocado, e nada de reúso é registrado.
func TestSessaoRemovidaEntreAsDuasLeiturasEhSessaoInexistente(t *testing.T) {
	casos := []struct {
		nome    string
		operar  func(ctx context.Context, mgr *auth.Manager, token string) error
		esperar func(t *testing.T, err error)
	}{
		{
			"Rotate",
			func(ctx context.Context, mgr *auth.Manager, token string) error {
				_, _, err := mgr.Rotate(ctx, token, meta)
				return err
			},
			func(t *testing.T, err error) {
				if !errors.Is(err, auth.ErrInvalidSession) {
					t.Errorf("Rotate devolveu %v, queria ErrInvalidSession", err)
				}
			},
		},
		{
			"Revoke",
			func(ctx context.Context, mgr *auth.Manager, token string) error {
				return mgr.Revoke(ctx, token)
			},
			func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("Revoke devolveu %v, queria nil (logout idempotente)", err)
				}
			},
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			mgr, pool, clk, userID := setup(t)
			ctx := context.Background()

			tokenAlvo, alvo, err := mgr.Issue(ctx, userID, carimbo, meta)
			if err != nil {
				t.Fatal(err)
			}
			_, espectador, err := mgr.Issue(ctx, userID, carimbo, meta)
			if err != nil {
				t.Fatal(err)
			}

			apagou := false
			lento := auth.NewManager(storeQueApagaNoMeio{
				inner: auth.NewPGStore(pool),
				apagar: func() {
					apagou = true
					if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE family_id = $1`, alvo.FamilyID); err != nil {
						t.Fatalf("apagar a sessão no meio: %v", err)
					}
				},
			}, clk, ipKey)

			caso.esperar(t, caso.operar(ctx, lento, tokenAlvo))

			if !apagou {
				t.Fatal("a sessão não foi apagada entre as leituras: o teste não exercitou a corrida")
			}
			var total, vivasDoEspectador, auditorias int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&total); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM sessions
				  WHERE family_id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
				espectador.FamilyID).Scan(&vivasDoEspectador); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE user_id = $1`, userID).Scan(&auditorias); err != nil {
				t.Fatal(err)
			}
			if total != 1 {
				t.Errorf("%d sessões no banco, queria 1 (só o espectador: nada foi criado)", total)
			}
			if vivasDoEspectador != 1 {
				t.Errorf("%d sessões vivas do espectador, queria 1", vivasDoEspectador)
			}
			if auditorias != 0 {
				t.Errorf("%d eventos de auditoria, queria 0", auditorias)
			}
		})
	}
}

// --- A8: compare-and-swap de Confirm e de ConfirmRotation -------------------

// tracerQueIntercala roda antes uma vez, logo antes da primeira instrução cujo
// SQL contém trecho sair para o banco. O antes roda na goroutine de quem chamou
// a instrução, então a ordem é a do código, não a do escalonador.
type tracerQueIntercala struct {
	trecho string
	antes  func()
	feito  bool
}

func (tr *tracerQueIntercala) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if !tr.feito && strings.Contains(d.SQL, tr.trecho) {
		tr.feito = true
		tr.antes()
	}
	return ctx
}

func (*tracerQueIntercala) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// totpComEscritaAntesDe devolve um TOTP que usa o MESMO banco de base, por um
// pool próprio com o tracer, e a função que exige que a escrita tenha entrado.
func totpComEscritaAntesDe(t *testing.T, trecho string, escrita func()) (*auth.TOTP, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	tr := &tracerQueIntercala{trecho: trecho, antes: escrita}
	cfg.ConnConfig.Tracer = tr
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Registrado depois do cleanup do pool de base: fecha antes dele.
	t.Cleanup(pool.Close)
	box, err := crypto.NewBox(totpMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewTOTP(pool, box, "StillOn"), func() {
		t.Helper()
		if !tr.feito {
			t.Fatalf("a instrução com %q não saiu: o teste não exercitou a corrida", trecho)
		}
	}
}

// ligadoPorOutraTransacao é o instante, literal, que a escrita concorrente
// grava: o estado final prova de quem é a escrita que ficou.
var ligadoPorOutraTransacao = time.Date(2026, 9, 22, 11, 30, 0, 0, time.UTC)

func contarCodigos(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func contarAuditoria(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, acao string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2`, userID, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func segredoAtivoGravado(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []byte {
	t.Helper()
	var sealed []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, userID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

// A guarda do UPDATE do Confirm é o que impede ligar o 2FA depois de uma
// escrita que mudou o chão debaixo dele: outro Confirm que ligou antes, ou uma
// troca de senha que apagou o segredo. Sem ela, o segundo Confirm reescreveria a
// data de ativação e trocaria os códigos de recuperação, e o segundo caso ligaria
// o 2FA sem segredo, o que trancaria o dono para fora.
func TestConfirmPerdeACorridaDaGuardaDoUpdate(t *testing.T) {
	casos := []struct {
		nome       string
		escrever   string
		argumento  []any
		esperado   error
		confirmado bool
	}{
		{
			nome:       "2FA ligado por outra transação",
			escrever:   `UPDATE users SET totp_confirmed_at = $2 WHERE id = $1`,
			argumento:  []any{ligadoPorOutraTransacao},
			esperado:   auth.ErrTOTPAlreadyEnabled,
			confirmado: true,
		},
		{
			nome:     "segredo apagado pela troca de senha",
			escrever: `UPDATE users SET totp_secret_enc = NULL WHERE id = $1`,
			esperado: auth.ErrTOTPNotEnrolled,
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			base, pool, userID := setupTOTP(t)
			ctx := context.Background()
			now := inicioDaTroca

			uri, err := base.Enroll(ctx, userID, "totp@exemplo.com")
			if err != nil {
				t.Fatal(err)
			}
			codigo := codigoEm(t, secretFromURI(t, uri), now)

			svc, exigirCorrida := totpComEscritaAntesDe(t, "SET totp_confirmed_at = $2, totp_last_timestep = $3", func() {
				args := append([]any{userID}, caso.argumento...)
				if _, err := pool.Exec(ctx, caso.escrever, args...); err != nil {
					t.Fatalf("escrita concorrente: %v", err)
				}
			})

			codes, err := svc.Confirm(ctx, userID, codigo, now)
			exigirCorrida()
			if !errors.Is(err, caso.esperado) {
				t.Errorf("Confirm devolveu %v, queria %v", err, caso.esperado)
			}
			if len(codes) != 0 {
				t.Errorf("Confirm devolveu %d códigos de recuperação, queria 0", len(codes))
			}

			var confirmadoEm *time.Time
			var ultimoPasso *int64
			if err := pool.QueryRow(ctx,
				`SELECT totp_confirmed_at, totp_last_timestep FROM users WHERE id = $1`, userID).
				Scan(&confirmadoEm, &ultimoPasso); err != nil {
				t.Fatal(err)
			}
			if caso.confirmado {
				if confirmadoEm == nil || !confirmadoEm.Equal(ligadoPorOutraTransacao) {
					t.Errorf("totp_confirmed_at = %v, queria %v (o da outra transação)", confirmadoEm, ligadoPorOutraTransacao)
				}
			} else if confirmadoEm != nil {
				t.Errorf("totp_confirmed_at = %v, queria NULL: o 2FA foi ligado sem segredo", confirmadoEm)
			}
			if ultimoPasso != nil {
				t.Errorf("totp_last_timestep = %d, queria NULL", *ultimoPasso)
			}
			if n := contarCodigos(t, pool, userID); n != 0 {
				t.Errorf("%d códigos de recuperação gravados, queria 0", n)
			}
			if n := contarAuditoria(t, pool, userID, "totp.enabled"); n != 0 {
				t.Errorf("%d eventos totp.enabled, queria 0", n)
			}
		})
	}
}

// O compare-and-swap da ConfirmRotation promove o segredo pendente que validou
// o código, e só ele, para a família que o iniciou e com o 2FA ainda ligado. Se
// qualquer uma das três coisas muda depois da leitura, a confirmação recusa, o
// segredo ativo fica como estava e os códigos de recuperação não são trocados.
func TestConfirmRotationPerdeACorridaDaGuardaDoUpdate(t *testing.T) {
	outraFamilia := uuid.MustParse("0198a000-0000-7000-8000-000000000001")
	casos := []struct {
		nome              string
		escrever          string
		argumento         []any
		pendenteFinal     []byte // nil = o pendente original
		familiaDoPendente *uuid.UUID
		confirmado        bool
	}{
		{
			nome:          "pendente trocado por outro iniciar-troca",
			escrever:      `UPDATE users SET totp_pending_secret_enc = '\x0102'::bytea WHERE id = $1`,
			pendenteFinal: []byte{1, 2},
			confirmado:    true,
		},
		{
			nome:              "pendente passa a ser de outra família",
			escrever:          `UPDATE users SET totp_pending_family_id = $2 WHERE id = $1`,
			argumento:         []any{outraFamilia},
			familiaDoPendente: &outraFamilia,
			confirmado:        true,
		},
		{
			nome:     "2FA desligado no meio",
			escrever: `UPDATE users SET totp_confirmed_at = NULL WHERE id = $1`,
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			base, pool, userID := setupTOTP(t)
			ctx := context.Background()
			familia := familiaViva(t, pool, userID, inicioDaTroca)
			novo := iniciarTroca(t, base, pool, userID, familia)
			if _, err := pool.Exec(ctx,
				`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, '\x0a0b'::bytea)`,
				uuid.New(), userID); err != nil {
				t.Fatal(err)
			}
			rotacao := rotacaoAtual(t, pool, userID)
			pendenteAntes := pendenteGravado(t, pool, userID)
			ativoAntes := segredoAtivoGravado(t, pool, userID)
			familiaAntes := familiaDoPendente(t, pool, userID)

			svc, exigirCorrida := totpComEscritaAntesDe(t, "SET totp_secret_enc = $2, totp_pending_secret_enc = NULL", func() {
				args := append([]any{userID}, caso.argumento...)
				if _, err := pool.Exec(ctx, caso.escrever, args...); err != nil {
					t.Fatalf("escrita concorrente: %v", err)
				}
			})

			em := inicioDaTroca.Add(30 * time.Second)
			codes, err := svc.ConfirmRotation(ctx, userID, familia, rotacao, codigoEm(t, novo, em), em)
			exigirCorrida()
			if !errors.Is(err, auth.ErrTOTPNoRotation) {
				t.Errorf("ConfirmRotation devolveu %v, queria ErrTOTPNoRotation", err)
			}
			if len(codes) != 0 {
				t.Errorf("ConfirmRotation devolveu %d códigos de recuperação, queria 0", len(codes))
			}

			if !bytes.Equal(segredoAtivoGravado(t, pool, userID), ativoAntes) {
				t.Error("totp_secret_enc mudou: a confirmação promoveu um pendente velho")
			}
			esperadoPendente := pendenteAntes
			if caso.pendenteFinal != nil {
				esperadoPendente = caso.pendenteFinal
			}
			if !bytes.Equal(pendenteGravado(t, pool, userID), esperadoPendente) {
				t.Error("totp_pending_secret_enc não é o da escrita concorrente")
			}
			esperadaFamilia := familiaAntes
			if caso.familiaDoPendente != nil {
				esperadaFamilia = caso.familiaDoPendente
			}
			if got := familiaDoPendente(t, pool, userID); got == nil || *got != *esperadaFamilia {
				t.Errorf("totp_pending_family_id = %v, queria %v", got, esperadaFamilia)
			}
			var confirmado bool
			if err := pool.QueryRow(ctx,
				`SELECT totp_confirmed_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&confirmado); err != nil {
				t.Fatal(err)
			}
			if confirmado != caso.confirmado {
				t.Errorf("totp_confirmed_at preenchido = %v, queria %v", confirmado, caso.confirmado)
			}
			if n := contarCodigos(t, pool, userID); n != 1 {
				t.Errorf("%d códigos de recuperação, queria 1 (o antigo, intacto)", n)
			}
			if n := contarAuditoria(t, pool, userID, "totp.rotated"); n != 0 {
				t.Errorf("%d eventos totp.rotated, queria 0", n)
			}
		})
	}
}

// --- A9: sem rastro de auditoria, a operação inteira é desfeita -------------

const mensagemDaAuditoriaRecusada = "auditoria recusada pelo teste"

// recusarAuditoria instala um trigger que rejeita o INSERT em audit_log da
// action dada, e o remove no Cleanup. As outras actions passam, para que a
// preparação do cenário grave o que precisar.
func recusarAuditoria(t *testing.T, pool *pgxpool.Pool, acao string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION recusar_auditoria_do_teste() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.action = TG_ARGV[0] THEN
				RAISE EXCEPTION '`+mensagemDaAuditoriaRecusada+`: %', NEW.action;
			END IF;
			RETURN NEW;
		END $$`); err != nil {
		t.Fatalf("criar a função do trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS recusar_auditoria_do_teste() CASCADE`); err != nil {
			t.Errorf("remover a função do trigger: %v", err)
		}
	})
	// A action vai em literal: TG_ARGV só aceita constantes, e acao vem do teste.
	if _, err := pool.Exec(ctx,
		`CREATE TRIGGER recusar_auditoria_do_teste BEFORE INSERT ON audit_log
		 FOR EACH ROW EXECUTE FUNCTION recusar_auditoria_do_teste('`+acao+`')`); err != nil {
		t.Fatalf("criar o trigger: %v", err)
	}
}

// estadoDoFator é tudo que as operações de 2FA podem mexer, lido do banco.
type estadoDoFator struct {
	Segredo          []byte
	Pendente         []byte
	Confirmado       bool
	FamiliaPendente  *uuid.UUID
	UltimoPasso      *int64
	Codigos          int
	CodigosHash      [][]byte
	SessoesRevogadas int
}

func lerEstadoDoFator(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) estadoDoFator {
	t.Helper()
	ctx := context.Background()
	var e estadoDoFator
	if err := pool.QueryRow(ctx,
		`SELECT totp_secret_enc, totp_pending_secret_enc, totp_confirmed_at IS NOT NULL,
		        totp_pending_family_id, totp_last_timestep
		   FROM users WHERE id = $1`, userID).
		Scan(&e.Segredo, &e.Pendente, &e.Confirmado, &e.FamiliaPendente, &e.UltimoPasso); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx,
		`SELECT code_hash FROM totp_recovery_codes WHERE user_id = $1 ORDER BY code_hash`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		e.CodigosHash = append(e.CodigosHash, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	e.Codigos = len(e.CodigosHash)
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, userID).
		Scan(&e.SessoesRevogadas); err != nil {
		t.Fatal(err)
	}
	return e
}

// codigoDeRecuperacaoDoTeste é um código no formato que o banco guarda
// (base32 maiúsculo, sem separador): VerifyWithProof e StartRotation o
// reconhecem pelo hash.
const codigoDeRecuperacaoDoTeste = "ABCDEFGHIJ234567"

func gravarCodigoDeRecuperacao(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
		uuid.New(), userID, crypto.HashToken(codigoDeRecuperacaoDoTeste)); err != nil {
		t.Fatal(err)
	}
}

// Ativar, desativar, trocar o autenticador e gastar um código de recuperação
// são eventos de segurança, e o rastro vai na mesma transação do efeito. Se a
// auditoria não grava, não pode sobrar efeito: nem 2FA ligado ou desligado, nem
// segredo trocado, nem código de recuperação gasto.
func TestOperacoesDe2FARevertemSeAAuditoriaFalha(t *testing.T) {
	now := inicioDaTroca
	casos := []struct {
		nome    string
		acao    string
		codigos int // códigos de recuperação que têm de sobrar
		// O fator como o cenário o deixa, e como o rollback tem de devolvê-lo:
		// o estado "antes" lido do banco não prova isso sozinho.
		confirmado bool // totp_confirmed_at preenchido
		pendente   bool // segredo pendente de troca gravado
		// preparar monta o cenário e devolve a operação que tem de falhar.
		preparar func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error
	}{
		{
			nome: "Confirm", acao: "totp.enabled", codigos: 0, confirmado: false, pendente: false,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				uri, err := svc.Enroll(context.Background(), userID, "totp@exemplo.com")
				if err != nil {
					t.Fatal(err)
				}
				codigo := codigoEm(t, secretFromURI(t, uri), now)
				return func() error {
					_, err := svc.Confirm(context.Background(), userID, codigo, now)
					return err
				}
			},
		},
		{
			nome: "VerifyWithProof com código de recuperação", acao: "totp.recovery_used", codigos: 1, confirmado: true, pendente: false,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarCodigoDeRecuperacao(t, pool, userID)
				return func() error {
					_, err := svc.VerifyWithProof(context.Background(), userID, codigoDeRecuperacaoDoTeste, now)
					return err
				}
			},
		},
		{
			nome: "Disable", acao: "totp.disabled", codigos: 1, confirmado: true, pendente: false,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarCodigoDeRecuperacao(t, pool, userID)
				codigo := codigoEm(t, segredoDeTroca, now)
				return func() error { return svc.Disable(context.Background(), userID, codigo, now) }
			},
		},
		{
			nome: "StartRotation com código de recuperação", acao: "totp.recovery_used", codigos: 1, confirmado: true, pendente: false,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				familia := familiaViva(t, pool, userID, now)
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarCodigoDeRecuperacao(t, pool, userID)
				return func() error {
					_, _, err := svc.StartRotation(context.Background(), userID, familia,
						"totp@exemplo.com", codigoDeRecuperacaoDoTeste, now)
					return err
				}
			},
		},
		{
			nome: "StartRotation", acao: "totp.rotation_started", codigos: 1, confirmado: true, pendente: false,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				familia := familiaViva(t, pool, userID, now)
				gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
				gravarCodigoDeRecuperacao(t, pool, userID)
				codigo := codigoEm(t, segredoDeTroca, now)
				return func() error {
					_, _, err := svc.StartRotation(context.Background(), userID, familia, "totp@exemplo.com", codigo, now)
					return err
				}
			},
		},
		{
			nome: "ConfirmRotation", acao: "totp.rotated", codigos: 1, confirmado: true, pendente: true,
			preparar: func(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID uuid.UUID) func() error {
				familia := familiaViva(t, pool, userID, now)
				// Outra família viva: a confirmação a revogaria, e o rollback tem de
				// devolvê-la.
				familiaViva(t, pool, userID, now)
				novo := iniciarTroca(t, svc, pool, userID, familia)
				gravarCodigoDeRecuperacao(t, pool, userID)
				rotacao := rotacaoAtual(t, pool, userID)
				em := now.Add(30 * time.Second)
				codigo := codigoEm(t, novo, em)
				return func() error {
					_, err := svc.ConfirmRotation(context.Background(), userID, familia, rotacao, codigo, em)
					return err
				}
			},
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			svc, pool, userID := setupTOTP(t)
			operar := caso.preparar(t, svc, pool, userID)
			antes := lerEstadoDoFator(t, pool, userID)
			recusarAuditoria(t, pool, caso.acao)

			err := operar()
			if err == nil || !strings.Contains(err.Error(), mensagemDaAuditoriaRecusada) {
				t.Fatalf("a operação devolveu %v, queria o erro do trigger de auditoria", err)
			}

			depois := lerEstadoDoFator(t, pool, userID)
			if !reflect.DeepEqual(antes, depois) {
				t.Errorf("o estado mudou apesar do erro:\nantes  %+v\ndepois %+v", antes, depois)
			}
			if depois.Codigos != caso.codigos {
				t.Errorf("%d códigos de recuperação, queria %d", depois.Codigos, caso.codigos)
			}
			if depois.Confirmado != caso.confirmado {
				t.Errorf("totp_confirmed_at preenchido = %v, queria %v", depois.Confirmado, caso.confirmado)
			}
			if temPendente := len(depois.Pendente) > 0 && depois.FamiliaPendente != nil; temPendente != caso.pendente {
				t.Errorf("segredo pendente com família = %v, queria %v", temPendente, caso.pendente)
			}
			if depois.SessoesRevogadas != 0 {
				t.Errorf("%d sessões revogadas, queria 0", depois.SessoesRevogadas)
			}
			if n := contarAuditoria(t, pool, userID, caso.acao); n != 0 {
				t.Errorf("%d eventos %s, queria 0", n, caso.acao)
			}
		})
	}
}
