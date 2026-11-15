package auth_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/notify"
)

type avisoLimpo struct {
	userID   uuid.UUID
	lockedAt time.Time
}

type avisosFalsos struct {
	pendentes []auth.TOTPLockAlert
	limpos    []avisoLimpo
}

func (a *avisosFalsos) PendingTOTPLockAlerts(_ context.Context, limite int) ([]auth.TOTPLockAlert, error) {
	return slices.Clone(a.pendentes[:min(limite, len(a.pendentes))]), nil
}

func (a *avisosFalsos) ClearTOTPLockAlert(_ context.Context, userID uuid.UUID, lockedAt time.Time) (bool, error) {
	a.limpos = append(a.limpos, avisoLimpo{userID, lockedAt})
	a.pendentes = slices.DeleteFunc(a.pendentes, func(alerta auth.TOTPLockAlert) bool {
		return alerta.UserID == userID && alerta.LockedAt.Equal(lockedAt)
	})
	return true, nil
}

func (a *avisosFalsos) RecordTOTPLockAlertFailure(_ context.Context, userID uuid.UUID, lockedAt time.Time) error {
	for i, alerta := range a.pendentes {
		if alerta.UserID == userID && alerta.LockedAt.Equal(lockedAt) {
			a.pendentes = append(slices.Delete(a.pendentes, i, i+1), alerta)
			break
		}
	}
	return nil
}

type envioDeAviso struct {
	email      string
	permanente bool
	chave      string
}

type correioFalso struct {
	enviados        []envioDeAviso
	falhaPara       string
	recusa          error
	destinoInvalido bool
}

func (c *correioFalso) SendTOTPLocked(_ context.Context, email string, permanente bool, chave string) (bool, error) {
	if email == c.falhaPara {
		if c.recusa != nil {
			return c.destinoInvalido, c.recusa
		}
		return false, errors.New("resend fora do ar")
	}
	c.enviados = append(c.enviados, envioDeAviso{email, permanente, chave})
	return false, nil
}

func TestRejectedTOTPLockAlertsDoNotHoldTheNextBatch(t *testing.T) {
	avisos := &avisosFalsos{}
	em := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for i := range 20 {
		avisos.pendentes = append(avisos.pendentes, auth.TOTPLockAlert{
			UserID: uuid.New(), Email: "recusa@exemplo.com", LockedAt: em.Add(time.Duration(i) * time.Second),
		})
	}
	const destino = "dono@exemplo.com"
	avisos.pendentes = append(avisos.pendentes, auth.TOTPLockAlert{UserID: uuid.New(), Email: destino, LockedAt: em.Add(time.Minute)})
	correio := &correioFalso{
		falhaPara:       "recusa@exemplo.com",
		recusa:          fmt.Errorf("provedor: %w", &notify.SendError{Reason: notify.ReasonRejected, Permanent: true}),
		destinoInvalido: true,
	}

	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	if len(correio.enviados) != 1 || correio.enviados[0].email != destino || len(avisos.pendentes) != 0 {
		t.Fatalf("entregues=%d, pendentes=%d, queria um envio e fila vazia", len(correio.enviados), len(avisos.pendentes))
	}
}

func TestTOTPLockAlertRetriesDoNotHoldTheNextBatch(t *testing.T) {
	avisos := &avisosFalsos{}
	em := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for i := range 20 {
		avisos.pendentes = append(avisos.pendentes, auth.TOTPLockAlert{
			UserID: uuid.New(), Email: "falha@exemplo.com", LockedAt: em.Add(time.Duration(i) * time.Second),
		})
	}
	const destino = "dono@exemplo.com"
	avisos.pendentes = append(avisos.pendentes, auth.TOTPLockAlert{UserID: uuid.New(), Email: destino, LockedAt: em.Add(time.Minute)})
	correio := &correioFalso{falhaPara: "falha@exemplo.com"}

	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	if len(correio.enviados) != 1 || correio.enviados[0].email != destino || len(avisos.pendentes) != 20 {
		t.Fatalf("entregues=%d, pendentes=%d, queria um envio e vinte retentativas", len(correio.enviados), len(avisos.pendentes))
	}
}

func TestTOTPLockAlertSurvivesAProviderReconfiguration(t *testing.T) {
	const destino = "dono@exemplo.com"
	aviso := auth.TOTPLockAlert{UserID: uuid.New(), Email: destino, LockedAt: time.Now(), Permanent: true}
	avisos := &avisosFalsos{pendentes: []auth.TOTPLockAlert{aviso}}
	correio := &correioFalso{
		falhaPara: destino,
		recusa:    &notify.SendError{Reason: notify.ReasonRejected, Permanent: true},
	}

	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	if len(avisos.pendentes) != 1 || avisos.pendentes[0] != aviso {
		t.Fatalf("a recusa do provedor alterou a fila: %d aviso(s) pendente(s)", len(avisos.pendentes))
	}
	correio.falhaPara = ""
	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)
	if len(correio.enviados) != 1 || !correio.enviados[0].permanente || len(avisos.pendentes) != 0 {
		t.Fatalf("após reparar o provedor: enviados=%d pendentes=%d", len(correio.enviados), len(avisos.pendentes))
	}
}

// Cada aviso pendente sai uma vez, com a chave de idempotência do bloqueio
// (usuário e instante), e só deixa a fila depois do envio bem-sucedido: o que
// falhou continua pendente para a próxima rodada.
func TestSendTOTPLockAlertsClearsOnlyWhatWasSent(t *testing.T) {
	quem, outro := uuid.MustParse("01938000-0000-7000-8000-000000000001"), uuid.MustParse("01938000-0000-7000-8000-000000000002")
	em := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	avisos := &avisosFalsos{pendentes: []auth.TOTPLockAlert{
		{UserID: quem, Email: "dono@exemplo.com", LockedAt: em, Permanent: true},
		{UserID: outro, Email: "falha@exemplo.com", LockedAt: em},
	}}
	correio := &correioFalso{falhaPara: "falha@exemplo.com"}

	auth.SendTOTPLockAlerts(context.Background(), avisos, correio)

	queria := envioDeAviso{"dono@exemplo.com", true, "totp-lock/01938000-0000-7000-8000-000000000001/1794657600000000"}
	if len(correio.enviados) != 1 || correio.enviados[0] != queria {
		t.Fatalf("enviados = %+v, queria só %+v", correio.enviados, queria)
	}
	if len(avisos.limpos) != 1 || avisos.limpos[0].userID != quem || !avisos.limpos[0].lockedAt.Equal(em) {
		t.Fatalf("limpos = %+v, queria só %v com o carimbo %v", avisos.limpos, quem, em)
	}
}
