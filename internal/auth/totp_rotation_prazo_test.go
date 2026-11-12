package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// O prazo da troca pendente (#326): a confirmação vale até um instante antes
// de iniciada+PendingRotationTTL e é recusada no próprio prazo, com o mesmo erro
// de "sem troca pendente" e sem gravar nada. Os tempos vêm do `now` injetado.

// iniciadaGravada é o totp_pending_started_at, como está no banco.
func iniciadaGravada(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) *time.Time {
	t.Helper()
	var ini *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_pending_started_at FROM users WHERE id = $1`, userID).Scan(&ini); err != nil {
		t.Fatal(err)
	}
	return ini
}

func TestStartRotationRecordsWhenThePendingStarted(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	iniciarTroca(t, svc, pool, userID, familia)

	if ini := iniciadaGravada(t, pool, userID); ini == nil || !ini.Equal(inicioDaTroca) {
		t.Errorf("totp_pending_started_at = %v, queria %s (o now do StartRotation)", ini, inicioDaTroca)
	}
}

func TestConfirmRotationAcceptedJustBeforeTheDeadline(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)

	em := inicioDaTroca.Add(auth.PendingRotationTTL - time.Nanosecond)
	codigos, err := svc.ConfirmRotation(context.Background(), userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em)
	if err != nil {
		t.Fatalf("a confirmação um instante antes do prazo foi recusada: %v", err)
	}
	if len(codigos) != 10 {
		t.Errorf("%d códigos de recuperação, queria 10", len(codigos))
	}
}

func TestConfirmRotationRefusedAtAndAfterTheDeadline(t *testing.T) {
	casos := []struct {
		nome string
		apos time.Duration
	}{
		{"no próprio prazo", auth.PendingRotationTTL},
		{"um instante depois do prazo", auth.PendingRotationTTL + time.Nanosecond},
		{"muito depois do prazo", 24 * time.Hour},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			svc, pool, userID := setupTOTP(t)
			ctx := context.Background()
			familia := familiaViva(t, pool, userID, inicioDaTroca)
			novo := iniciarTroca(t, svc, pool, userID, familia)
			rotation := rotacaoAtual(t, pool, userID)
			antes := fotografar(t, pool, userID)

			// Código certo do app novo e rotation certo: só o prazo recusa.
			em := inicioDaTroca.Add(c.apos)
			if _, err := svc.ConfirmRotation(ctx, userID, familia, rotation, codigoEm(t, novo, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
				t.Fatalf("confirmação vencida devolveu %v, queria ErrTOTPNoRotation", err)
			}
			semEfeito(t, pool, userID, antes)

			// O fator ativo não mudou: o app novo não vale.
			if err := svc.Verify(ctx, userID, codigoEm(t, novo, em), em); !errors.Is(err, auth.ErrTOTPInvalid) {
				t.Errorf("o app novo valeu com a troca vencida: %v", err)
			}
		})
	}
}

// O prazo vem ANTES do código: com a troca vencida e o código ERRADO a resposta
// é a mesma de "sem troca pendente", e não ErrTOTPInvalid; a recusa também não
// mexe no bloqueio da conta (semEfeito compara tentativas e bloqueio).
func TestConfirmRotationExpiredWithAWrongCodeIsStillNoRotation(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	rotation := rotacaoAtual(t, pool, userID)
	bloquearConta(t, pool, userID)
	antes := fotografar(t, pool, userID)

	em := inicioDaTroca.Add(auth.PendingRotationTTL)
	errado := "000000"
	if errado == codigoEm(t, novo, em) {
		errado = "111111"
	}
	if _, err := svc.ConfirmRotation(context.Background(), userID, familia, rotation, errado, em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Fatalf("confirmação vencida com código errado devolveu %v, queria ErrTOTPNoRotation", err)
	}
	semEfeito(t, pool, userID, antes)
}

// Abandonar a troca não muda o fator ativo: depois do prazo o app antigo segue
// valendo.
func TestExpiredRotationKeepsTheOldFactor(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	iniciarTroca(t, svc, pool, userID, familia)

	em := inicioDaTroca.Add(auth.PendingRotationTTL + time.Minute)
	if err := svc.Verify(context.Background(), userID, codigoEm(t, segredoDeTroca, em), em); err != nil {
		t.Errorf("o app antigo deixou de valer com a troca abandonada: %v", err)
	}
}

// Linha pendente sem o instante (anterior à 00027, ou gravada pelo binário
// anterior com a coluna ainda vazia) é troca vencida.
func TestConfirmRotationRefusesAPendingWithoutStartedAt(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	novo := iniciarTroca(t, svc, pool, userID, familia)
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_pending_started_at = NULL WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	antes := fotografar(t, pool, userID)

	em := inicioDaTroca.Add(30 * time.Second)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
		t.Fatalf("pendente sem instante devolveu %v, queria ErrTOTPNoRotation", err)
	}
	semEfeito(t, pool, userID, antes)
}

// Um novo StartRotation da mesma família substitui o pendente e recomeça o
// prazo: o instante é o do último.
func TestRestartingARotationRestartsTheDeadline(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	iniciarTroca(t, svc, pool, userID, familia)

	segundo := inicioDaTroca.Add(9 * time.Minute)
	uri, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, segundo), segundo)
	if err != nil {
		t.Fatalf("recomeçar a troca: %v", err)
	}
	novo := secretFromURI(t, uri)

	// 15 min depois do primeiro, 6 min depois do segundo: dentro do prazo.
	em := inicioDaTroca.Add(15 * time.Minute)
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); err != nil {
		t.Errorf("o prazo não recomeçou no segundo StartRotation: %v", err)
	}
}
