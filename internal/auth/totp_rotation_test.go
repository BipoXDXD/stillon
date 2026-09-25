package auth_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
)

// segredoDeTroca é o segredo "antigo" dos testes da troca de autenticador
// (Plano 9, §3.2), gravado já confirmado por gravarSegredoTOTP.
const segredoDeTroca = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

// inicioDaTroca: 15 s dentro de um período, para o skew não pôr o código de
// um instante no período vizinho.
var inicioDaTroca = time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

func codigoEm(t *testing.T, segredo string, em time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(segredo, em)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// familiaViva abre uma família de sessão do usuário do setupTOTP, cujo hash é
// 'x'. A confirmação da troca encerra as OUTRAS famílias e mantém esta.
func familiaViva(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, em time.Time) uuid.UUID {
	t.Helper()
	mgr := auth.NewManager(auth.NewPGStore(pool), clock.NewFake(em), ipKey)
	_, s, err := mgr.Issue(context.Background(), userID, auth.PasswordStamp("x"), meta)
	if err != nil {
		t.Fatal(err)
	}
	return s.FamilyID
}

// rotacaoAtual é o rotation (A7 do Plano 10) da troca pendente do usuário,
// como o rotate o devolveu: o que o confirm exige de volta.
func rotacaoAtual(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()
	return auth.RotacaoDe(pendenteGravado(t, pool, userID))
}

// pendenteGravado devolve o segredo pendente cifrado, como está no banco.
func pendenteGravado(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []byte {
	t.Helper()
	var sealed []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_pending_secret_enc FROM users WHERE id = $1`, userID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

// familiaDoPendente devolve a família de sessão gravada com o pendente
// (totp_pending_family_id), ou nil.
func familiaDoPendente(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) *uuid.UUID {
	t.Helper()
	var f *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_pending_family_id FROM users WHERE id = $1`, userID).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

// revogarFamilia encerra a família direto no banco, como um DELETE
// /sessions/{id} de outro aparelho faria.
func revogarFamilia(t *testing.T, pool *pgxpool.Pool, familia uuid.UUID, em time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`, familia, em); err != nil {
		t.Fatal(err)
	}
}

// iniciarTroca grava o segredo antigo, inicia a troca com um código dele e
// devolve o segredo novo.
func iniciarTroca(t *testing.T, svc *auth.TOTP, pool *pgxpool.Pool, userID, familia uuid.UUID) string {
	t.Helper()
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	uri, _, err := svc.StartRotation(context.Background(), userID, familia, "totp@exemplo.com",
		codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca)
	if err != nil {
		t.Fatalf("iniciar a troca: %v", err)
	}
	return secretFromURI(t, uri)
}

// O segredo antigo vale até a confirmação, e para de valer nela; o novo só
// passa a valer ali. Trocar o segredo já no início deixaria sem 2FA quem
// desiste no meio, que é o que a troca com segredo pendente existe para evitar.
func TestRotationKeepsTheOldSecretUntilConfirmed(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	if novo == segredoDeTroca {
		t.Fatal("o segredo novo é igual ao antigo")
	}

	sealed := pendenteGravado(t, pool, userID)
	if len(sealed) == 0 {
		t.Fatal("o rotate não gravou o segredo pendente")
	}
	if bytes.Contains(sealed, []byte(novo)) {
		t.Error("o segredo pendente foi gravado em claro")
	}
	if f := familiaDoPendente(t, pool, userID); f == nil || *f != familia {
		t.Errorf("família do pendente = %v, queria %s (a que iniciou a troca)", f, familia)
	}

	t1 := inicioDaTroca.Add(30 * time.Second)
	if err := svc.Verify(ctx, userID, codigoEm(t, segredoDeTroca, t1), t1); err != nil {
		t.Errorf("o app antigo deixou de valer antes da confirmação: %v", err)
	}
	if err := svc.Verify(ctx, userID, codigoEm(t, novo, t1), t1); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o app novo valeu antes da confirmação: %v", err)
	}

	t2 := t1.Add(30 * time.Second)
	codigos, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, t2), t2)
	if err != nil {
		t.Fatalf("confirmar a troca: %v", err)
	}
	if len(codigos) != 10 {
		t.Errorf("%d códigos de recuperação, queria 10", len(codigos))
	}
	if p := pendenteGravado(t, pool, userID); len(p) != 0 {
		t.Error("o segredo pendente sobreviveu à confirmação")
	}
	if f := familiaDoPendente(t, pool, userID); f != nil {
		t.Errorf("a família do pendente (%s) sobreviveu à confirmação", f)
	}

	t3 := t2.Add(30 * time.Second)
	if err := svc.Verify(ctx, userID, codigoEm(t, segredoDeTroca, t3), t3); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o app antigo continuou valendo depois da confirmação: %v", err)
	}
	if err := svc.Verify(ctx, userID, codigoEm(t, novo, t3), t3); err != nil {
		t.Errorf("o app novo não vale depois da confirmação: %v", err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotated"); n != 1 {
		t.Errorf("%d eventos totp.rotated, queria 1", n)
	}
}

// O código da confirmação já entra consumido, como no Confirm do cadastro: o
// mesmo código não serve de novo para entrar.
func TestRotationConfirmConsumesTheTimestep(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)

	em := inicioDaTroca.Add(30 * time.Second)
	codigo := codigoEm(t, novo, em)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigo, em); err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, userID, codigo, em); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o código da confirmação valeu de novo: %v", err)
	}
	depois := em.Add(30 * time.Second)
	if err := svc.Verify(ctx, userID, codigoEm(t, novo, depois), depois); err != nil {
		t.Errorf("o código do período seguinte foi recusado: %v", err)
	}
}

// Iniciar exige a posse do fator ATUAL: código do app ou de recuperação.
// Errado ou vazio é ErrTOTPInvalid, e nada é gravado; com o 2FA desligado é
// ErrTOTPNotEnrolled (o handler responde 403).
func TestStartRotationRequiresTheCurrentFactor(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)

	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", "000000", inicioDaTroca); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("rotate com o 2FA desligado devolveu %v, queria ErrTOTPNotEnrolled", err)
	}
	if p := pendenteGravado(t, pool, userID); len(p) != 0 {
		t.Error("o rotate com o 2FA desligado gravou um pendente")
	}

	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	certo := codigoEm(t, segredoDeTroca, inicioDaTroca)
	errado := "000000"
	if errado == certo {
		errado = "111111"
	}
	for nome, codigo := range map[string]string{"errado": errado, "vazio": ""} {
		if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigo, inicioDaTroca); !errors.Is(err, auth.ErrTOTPInvalid) {
			t.Errorf("rotate com código %s devolveu %v, queria ErrTOTPInvalid", nome, err)
		}
	}
	if p := pendenteGravado(t, pool, userID); len(p) != 0 {
		t.Error("um rotate recusado gravou o pendente")
	}

	// A perna positiva, no mesmo cenário.
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", certo, inicioDaTroca); err != nil {
		t.Fatalf("rotate com o código certo devolveu %v", err)
	}
	if p := pendenteGravado(t, pool, userID); len(p) == 0 {
		t.Error("o rotate aceito não gravou o pendente")
	}
	// O código usado para iniciar fica consumido (RFC 6238 §5.2): o 2FA
	// continua ligado depois do rotate, e o código observado não entra.
	if err := svc.Verify(ctx, userID, certo, inicioDaTroca); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o código usado no rotate valeu de novo: %v", err)
	}
}

// Um código de recuperação prova a posse no lugar do app (o caso de quem
// perdeu o celular), e fica gasto mesmo que a troca não seja confirmada (R1
// da spec do Plano 9), com o mesmo rastro de auditoria do login.
func TestStartRotationAcceptsARecoveryCode(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := inicioDaTroca
	familia := familiaViva(t, pool, userID, now)

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	recuperacao, err := svc.Confirm(ctx, userID, codigoEm(t, secretFromURI(t, uri), now), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", recuperacao[0], now); err != nil {
		t.Fatalf("rotate com código de recuperação devolveu %v", err)
	}
	if err := svc.Verify(ctx, userID, recuperacao[0], now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Error("o código de recuperação usado no rotate continuou valendo")
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.recovery_used"); n != 1 {
		t.Errorf("%d eventos totp.recovery_used, queria 1", n)
	}
}

// Código errado na confirmação não mexe em nada: o pendente continua lá, e
// uma confirmação certa depois funciona.
func TestConfirmRotationWrongCodeKeepsThePending(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	antes := pendenteGravado(t, pool, userID)

	em := inicioDaTroca.Add(30 * time.Second)
	// O código do app ANTIGO é o erro mais provável de quem troca de celular.
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, segredoDeTroca, em), em); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("confirmar com o app antigo devolveu %v, queria ErrTOTPInvalid", err)
	}
	if !bytes.Equal(pendenteGravado(t, pool, userID), antes) {
		t.Error("a confirmação recusada mexeu no pendente")
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotated"); n != 0 {
		t.Errorf("%d eventos totp.rotated depois de uma recusa", n)
	}
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); err != nil {
		t.Errorf("a confirmação certa depois de uma errada devolveu %v", err)
	}
}

// Sem troca em andamento não há o que confirmar: ErrTOTPNoRotation (403 no
// handler), com o 2FA ligado ou desligado.
func TestConfirmRotationWithoutPending(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)

	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), "000000", inicioDaTroca); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("confirmar sem 2FA devolveu %v, queria ErrTOTPNoRotation", err)
	}
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	codigo := codigoEm(t, segredoDeTroca, inicioDaTroca)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigo, inicioDaTroca); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("confirmar sem pendente devolveu %v, queria ErrTOTPNoRotation", err)
	}
}

// Um rotate novo substitui o pendente anterior: o QR velho deixa de servir.
func TestRotateAgainReplacesThePending(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	primeiro := iniciarTroca(t, svc, pool, userID, familia)

	em := inicioDaTroca.Add(30 * time.Second)
	uri, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, em), em)
	if err != nil {
		t.Fatal(err)
	}
	segundo := secretFromURI(t, uri)

	depois := em.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, primeiro, depois), depois); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o pendente substituído ainda confirmou: %v", err)
	}
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, segundo, depois), depois); err != nil {
		t.Errorf("o pendente novo não confirmou: %v", err)
	}
}

// Depois da confirmação, os códigos de recuperação antigos morrem e os 10
// novos valem.
func TestConfirmRotationReplacesTheRecoveryCodes(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := inicioDaTroca
	familia := familiaViva(t, pool, userID, now)

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	atual := secretFromURI(t, uri)
	antigos, err := svc.Confirm(ctx, userID, codigoEm(t, atual, now), now)
	if err != nil {
		t.Fatal(err)
	}
	em := now.Add(30 * time.Second)
	uri, _, err = svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, atual, em), em)
	if err != nil {
		t.Fatal(err)
	}
	novo := secretFromURI(t, uri)
	em = em.Add(30 * time.Second)
	novos, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, antigos[1], em); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("um código de recuperação antigo continuou valendo: %v", err)
	}
	for i, c := range novos {
		if err := svc.Verify(ctx, userID, c, em); err != nil {
			t.Errorf("o código de recuperação novo %d não vale: %v", i, err)
		}
	}
}

// A confirmação encerra as OUTRAS famílias de sessão e mantém a atual: um
// celular trocado pode ter sido perdido ou roubado (spec do Plano 9, §3.2).
func TestConfirmRotationRevokesTheOtherFamilies(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	atual := familiaViva(t, pool, userID, inicioDaTroca)
	outra := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, atual)

	em := inicioDaTroca.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, atual, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); err != nil {
		t.Fatal(err)
	}
	viva := func(f uuid.UUID) bool {
		t.Helper()
		var v bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM sessions WHERE family_id = $1 AND revoked_at IS NULL AND used_at IS NULL)`,
			f).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !viva(atual) {
		t.Error("a confirmação encerrou a família atual")
	}
	if viva(outra) {
		t.Error("a confirmação deixou viva a outra família")
	}
}

// A família do token pode cair entre a guarda da rota e a transação (um
// DELETE /sessions de outro aparelho). Então nada muda, como na troca de
// senha: o 200 sempre quer dizer "trocado, e a sessão atual continua viva".
func TestConfirmRotationRechecksTheFamily(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`,
		familia, inicioDaTroca); err != nil {
		t.Fatal(err)
	}

	em := inicioDaTroca.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("confirmar com a família morta devolveu %v, queria ErrInvalidSession", err)
	}
	if p := pendenteGravado(t, pool, userID); len(p) == 0 {
		t.Error("a confirmação recusada apagou o pendente")
	}
	if err := svc.Verify(ctx, userID, codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Errorf("a confirmação recusada trocou o segredo: %v", err)
	}

	// A perna positiva: com a família de volta à vida, o mesmo pendente
	// confirma. (Outra família viva não serve: o pendente é só da família
	// que o iniciou, revisão final do Plano 9.)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET revoked_at = NULL WHERE family_id = $1`, familia); err != nil {
		t.Fatal(err)
	}
	depois := em.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, depois), depois); err != nil {
		t.Errorf("confirmar com a família viva devolveu %v", err)
	}
}

// A confirmação toma a trava de sessões do usuário (D1 do Plano 5) antes de
// revogar as outras famílias: sem ela, uma rotação em curso deixaria o
// sucessor fora da revogação.
func TestConfirmRotationTakesTheUserLock(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chave := "hashtextextended('vigilume.sessions:' || '" + userID.String() + "', 0)"
	var umaVez sync.Once
	soltar := func() {
		umaVez.Do(func() {
			if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chave+`)`); err != nil {
				t.Error(err)
			}
			conn.Release()
		})
	}
	t.Cleanup(soltar)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}

	em := inicioDaTroca.Add(30 * time.Second)
	fim := make(chan error, 1)
	go func() {
		_, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em)
		fim <- err
	}()

	esperou := false
	for prazo := time.Now().Add(3 * time.Second); time.Now().Before(prazo); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			esperou = true
			break
		}
	}
	soltar()
	err = <-fim
	if !esperou {
		t.Fatalf("a confirmação não esperou a trava do usuário (terminou com %v)", err)
	}
	if err != nil {
		t.Fatalf("a confirmação falhou depois de soltar a trava: %v", err)
	}
}

// O Disable apaga o pendente junto com o fator: um QR de uma troca que ficou
// para trás não pode ser confirmado depois de o 2FA ser ligado de novo.
func TestDisableDropsThePendingRotation(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	velho := iniciarTroca(t, svc, pool, userID, familia)

	em := inicioDaTroca.Add(30 * time.Second)
	if err := svc.Disable(ctx, userID, codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Fatal(err)
	}
	if p := pendenteGravado(t, pool, userID); len(p) != 0 {
		t.Error("o Disable deixou o segredo pendente")
	}
	if f := familiaDoPendente(t, pool, userID); f != nil {
		t.Errorf("o Disable deixou a família do pendente (%s)", f)
	}

	// Liga de novo, com outro app, e tenta confirmar a troca abandonada.
	em = em.Add(30 * time.Second)
	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, codigoEm(t, secretFromURI(t, uri), em), em); err != nil {
		t.Fatal(err)
	}
	em = em.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, velho, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("a troca abandonada antes do Disable confirmou depois: %v", err)
	}
}

// O pendente é selado com a AAD da coluna e do usuário (crypto.AAD("users",
// "totp_pending_secret_enc", id)). Copiado para outro usuário, ou para a
// coluna ativa, ele não abre; e a confirmação cifra de novo com a AAD de
// totp_secret_enc em vez de copiar os bytes, senão o segredo promovido não
// abriria no login.
func TestPendingSecretIsBoundToColumnAndUser(t *testing.T) {
	svc, pool, dono := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, dono, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, dono, familia)
	sealed := pendenteGravado(t, pool, dono)

	outro := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'outro@exemplo.com', 'x')`, outro); err != nil {
		t.Fatal(err)
	}
	gravarSegredoTOTP(t, pool, outro, segredoDeTroca)
	familiaDoOutro := familiaViva(t, pool, outro, inicioDaTroca)
	// A família vai junto, para a recusa ser a da AAD, e não a da família.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET totp_pending_secret_enc = $2, totp_pending_family_id = $3 WHERE id = $1`,
		outro, sealed, familiaDoOutro); err != nil {
		t.Fatal(err)
	}

	em := inicioDaTroca.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, outro, familiaDoOutro, rotacaoAtual(t, pool, outro), codigoEm(t, novo, em), em); err == nil {
		t.Error("o pendente copiado para outro usuário abriu e confirmou a troca")
	}
	if err := svc.Verify(ctx, outro, codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Errorf("o segredo do outro usuário mudou: %v", err)
	}

	// Na coluna ativa de um terceiro, o selo do pendente também não abre.
	terceiro := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, totp_secret_enc, totp_confirmed_at)
		 VALUES ($1, 'terceiro@exemplo.com', 'x', $2, $3)`, terceiro, sealed, inicioDaTroca); err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, terceiro, codigoEm(t, novo, em), em); err == nil {
		t.Error("o pendente copiado para a coluna ativa abriu")
	}
	box, err := crypto.NewBox(totpMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.Open(sealed, crypto.AAD("users", "totp_secret_enc", dono)); err == nil {
		t.Error("o pendente abre com a AAD da coluna ativa do próprio dono")
	}

	// A perna positiva: no dono, com a família dele, confirma, e o segredo
	// promovido abre no login (foi cifrado de novo, não copiado).
	if _, err := svc.ConfirmRotation(ctx, dono, familia, rotacaoAtual(t, pool, dono), codigoEm(t, novo, em), em); err != nil {
		t.Fatalf("o dono não confirmou a própria troca: %v", err)
	}
	depois := em.Add(30 * time.Second)
	if err := svc.Verify(ctx, dono, codigoEm(t, novo, depois), depois); err != nil {
		t.Errorf("o segredo promovido não abre no login: %v", err)
	}
}

// O pendente é da família de sessão que o criou (revisão final do Plano 9):
// outra família do mesmo dono, viva, não o confirma, nem com o código certo —
// é ErrTOTPNoRotation (403 no handler, o mesmo de "sem troca em andamento"), e
// nada muda. A perna positiva é a família que iniciou, no mesmo cenário.
func TestConfirmRotationOnlyFromTheFamilyThatStartedIt(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	dona := familiaViva(t, pool, userID, inicioDaTroca)
	outra := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, dona)
	antes := pendenteGravado(t, pool, userID)

	em := inicioDaTroca.Add(30 * time.Second)
	// Código errado da outra família: o mesmo ErrTOTPNoRotation, não
	// ErrTOTPInvalid (que contaria no bloqueio) — a família vem antes.
	if _, err := svc.ConfirmRotation(ctx, userID, outra, rotacaoAtual(t, pool, userID), codigoEm(t, segredoDeTroca, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("confirmar pela outra família com código errado devolveu %v, queria ErrTOTPNoRotation", err)
	}
	if _, err := svc.ConfirmRotation(ctx, userID, outra, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("confirmar pela outra família devolveu %v, queria ErrTOTPNoRotation", err)
	}
	if !bytes.Equal(pendenteGravado(t, pool, userID), antes) {
		t.Error("a confirmação recusada mexeu no pendente")
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotated"); n != 0 {
		t.Errorf("%d eventos totp.rotated depois da recusa", n)
	}
	if err := svc.Verify(ctx, userID, codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Errorf("a confirmação recusada trocou o segredo: %v", err)
	}

	depois := em.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, dona, rotacaoAtual(t, pool, userID), codigoEm(t, novo, depois), depois); err != nil {
		t.Errorf("confirmar pela família que iniciou devolveu %v", err)
	}
}

// Um rotate novo, de outra família, substitui o pendente E a família: a
// primeira deixa de confirmar, mesmo com o QR novo nas mãos.
func TestRotateAgainMovesThePendingToTheNewFamily(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	primeira := familiaViva(t, pool, userID, inicioDaTroca)
	segunda := familiaViva(t, pool, userID, inicioDaTroca)
	iniciarTroca(t, svc, pool, userID, primeira)

	em := inicioDaTroca.Add(30 * time.Second)
	uri, _, err := svc.StartRotation(ctx, userID, segunda, "totp@exemplo.com", codigoEm(t, segredoDeTroca, em), em)
	if err != nil {
		t.Fatal(err)
	}
	novo := secretFromURI(t, uri)
	if f := familiaDoPendente(t, pool, userID); f == nil || *f != segunda {
		t.Errorf("família do pendente = %v, queria %s (a do rotate que substituiu)", f, segunda)
	}

	depois := em.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, primeira, rotacaoAtual(t, pool, userID), codigoEm(t, novo, depois), depois); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Errorf("a família do rotate substituído confirmou: %v", err)
	}
	if _, err := svc.ConfirmRotation(ctx, userID, segunda, rotacaoAtual(t, pool, userID), codigoEm(t, novo, depois), depois); err != nil {
		t.Errorf("a família do rotate novo não confirmou: %v", err)
	}
}

// A família do token pode cair entre a guarda da rota e a gravação (um DELETE
// /sessions de outro aparelho, uma troca de senha): o StartRotation a
// reconfere sob a trava do usuário e, morta, devolve ErrInvalidSession (401
// unauthorized no handler) sem gravar pendente, família nem auditoria. A
// perna positiva é a mesma família, de volta à vida.
func TestStartRotationRechecksTheFamily(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	revogarFamilia(t, pool, familia, inicioDaTroca)

	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com",
		codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("rotate com a família morta devolveu %v, queria ErrInvalidSession", err)
	}
	if p := pendenteGravado(t, pool, userID); len(p) != 0 {
		t.Error("o rotate com a família morta gravou o pendente")
	}
	if f := familiaDoPendente(t, pool, userID); f != nil {
		t.Errorf("o rotate com a família morta gravou a família %s", f)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotation_started"); n != 0 {
		t.Errorf("%d eventos totp.rotation_started com a família morta", n)
	}

	if _, err := pool.Exec(ctx, `UPDATE sessions SET revoked_at = NULL WHERE family_id = $1`, familia); err != nil {
		t.Fatal(err)
	}
	em := inicioDaTroca.Add(30 * time.Second)
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Errorf("rotate com a família viva devolveu %v", err)
	}
	if f := familiaDoPendente(t, pool, userID); f == nil || *f != familia {
		t.Errorf("família do pendente = %v, queria %s", f, familia)
	}
}

// Todo rotate aceito deixa o rastro totp.rotation_started, para o dono ver
// que uma troca foi iniciada (antes, só havia rastro quando a prova era um
// código de recuperação). Um por rotate; o recusado não deixa.
func TestStartRotationIsAudited(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)

	certo := codigoEm(t, segredoDeTroca, inicioDaTroca)
	errado := "000000"
	if errado == certo {
		errado = "111111"
	}
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", errado, inicioDaTroca); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Fatalf("rotate com código errado devolveu %v", err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotation_started"); n != 0 {
		t.Errorf("%d eventos totp.rotation_started depois de um rotate recusado", n)
	}

	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", certo, inicioDaTroca); err != nil {
		t.Fatal(err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotation_started"); n != 1 {
		t.Errorf("%d eventos totp.rotation_started depois de um rotate, queria 1", n)
	}
	em := inicioDaTroca.Add(30 * time.Second)
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Fatal(err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.rotation_started"); n != 2 {
		t.Errorf("%d eventos totp.rotation_started depois de dois rotates, queria 2", n)
	}
}
