package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// A troca de autenticador com segredo pendente (Plano 9, §3.2). Antes dela,
// trocar de celular era desativar e ativar de novo, e no meio a conta ficava
// sem 2FA — quem desistisse no meio ficava desprotegido sem perceber.
//
// StartRotation grava o segredo do app novo em totp_pending_secret_enc, e o
// antigo continua valendo; ConfirmRotation, com um código do app novo, promove
// o pendente numa transação só. O pendente não tem prazo, mas é da família de
// sessão que o criou (totp_pending_family_id, revisão final do Plano 9): só
// ela o confirma, e ele morre com ela — revogada a família (DELETE
// /sessions/{id}, logout, troca de senha), nenhuma outra sessão viva o
// confirma. Pendente e família caem juntos na confirmação, no Disable, no
// reset-2fa do CLI, na troca de senha e no set-password, e um novo
// StartRotation substitui os dois.

// StartRotation inicia a troca. O chamador já provou a senha
// (auth.CheckPassword); aqui se prova a posse do fator ATUAL, pelo código do
// app ou por um código de recuperação — as mesmas provas do Disable. Sem a
// segunda prova, uma sessão roubada somada à senha entregaria o segundo fator
// ao atacante, o bypass que o R38 fechou.
//
// A prova passa pelo Verify: o timestep do código do app fica consumido (o 2FA
// continua ligado depois deste passo, e o código observado não pode entrar
// depois), e o código de recuperação fica gasto com o rastro
// totp.recovery_used, mesmo que a troca nunca seja confirmada (R1 da spec).
//
// A gravação é sob a trava de sessões do usuário (D1 do Plano 5), com a
// família do token (familyID) reconferida ali: uma troca de senha que comita
// entre a guarda da rota e a gravação revoga a família, e o pendente de quem
// ela está expulsando não pode ser gravado depois dela. Pendente e família são
// gravados juntos, com a auditoria totp.rotation_started (a pista do dono de
// que uma troca foi iniciada).
//
// Devolve ErrTOTPNotEnrolled com o 2FA desligado, ErrTOTPInvalid com a prova
// errada ou vazia e ErrInvalidSession se a família caiu antes da trava.
func (s *TOTP) StartRotation(ctx context.Context, userID, familyID uuid.UUID, email, code string, now time.Time) (string, error) {
	ligado, err := s.IsEnabled(ctx, userID)
	if err != nil {
		return "", err
	}
	if !ligado {
		return "", ErrTOTPNotEnrolled
	}
	if code == "" {
		return "", ErrTOTPInvalid
	}
	if err := s.Verify(ctx, userID, code, now); err != nil {
		return "", err
	}

	key, err := s.novaChave(email)
	if err != nil {
		return "", err
	}
	sealed, err := s.box.Seal([]byte(key.Secret()), aadPendente(userID))
	if err != nil {
		return "", fmt.Errorf("cifrar segredo TOTP pendente: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit
	q := db.New(tx)

	// A trava antes da linha de users, na ordem da troca de senha e do
	// ConfirmRotation. Sob ela, a família não cai no meio: ou a troca de
	// senha já comitou (e a família está morta aqui), ou ela espera esta
	// transação e apaga o pendente gravado.
	if err := q.LockUserSessions(ctx, userID); err != nil {
		return "", fmt.Errorf("travar sessões: %w", err)
	}
	viva, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
	if err != nil {
		return "", fmt.Errorf("conferir família de sessão: %w", err)
	}
	if !viva {
		return "", ErrInvalidSession
	}

	// A guarda na WHERE, pelo motivo do R38: entre o Verify e o UPDATE cabe
	// uma desativação concorrente, e um pendente sem fator ativo seria lixo.
	//
	// O bloqueio da conta zera junto (A6 do Plano 10, revendo a D5 do Plano
	// 9): a senha e a posse do fator acabaram de ser provadas.
	tag, err := tx.Exec(ctx,
		`UPDATE users SET totp_pending_secret_enc = $2, totp_pending_family_id = $3,
		                  failed_attempts = 0, locked_until = NULL
		   WHERE id = $1 AND totp_confirmed_at IS NOT NULL`,
		userID, sealed, familyID)
	if err != nil {
		return "", fmt.Errorf("gravar segredo TOTP pendente: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrTOTPNotEnrolled
	}
	if err := insertAudit(ctx, tx, userID, "totp.rotation_started", nil, now); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("gravar segredo TOTP pendente: %w", err)
	}
	return key.URL(), nil
}

// ConfirmRotation conclui a troca com um código do app NOVO e devolve os 10
// códigos de recuperação novos. Numa transação só: o pendente vira o segredo
// atual (decifrado e cifrado de novo com a AAD de totp_secret_enc), o timestep
// usado fica consumido, os códigos antigos morrem, as OUTRAS famílias de
// sessão caem e a atual (familyID) fica, e a auditoria totp.rotated.
//
// Encerrar as outras sessões pelo mesmo motivo da troca de senha: um celular
// trocado pode ter sido perdido ou roubado.
//
// Só a família que iniciou a troca a confirma (totp_pending_family_id): o
// pendente de outra família é, para esta, "nenhuma troca em andamento".
//
// Devolve ErrTOTPNoRotation sem pendente, com o pendente de outra família (ou
// com o 2FA desligado), ErrTOTPInvalid com o código errado (o pendente fica) e ErrInvalidSession se
// a família caiu antes da trava.
func (s *TOTP) ConfirmRotation(ctx context.Context, userID, familyID uuid.UUID, code string, now time.Time) ([]string, error) {
	var pendente []byte
	var dona *uuid.UUID
	var confirmado *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT totp_pending_secret_enc, totp_pending_family_id, totp_confirmed_at FROM users WHERE id = $1`, userID).
		Scan(&pendente, &dona, &confirmado)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("carregar troca de TOTP: %w", err)
	}
	// O pendente de outra família sai aqui, antes do código: o mesmo 403 de
	// "sem troca", sem contar falha no bloqueio.
	if len(pendente) == 0 || confirmado == nil || dona == nil || *dona != familyID {
		return nil, ErrTOTPNoRotation
	}
	plain, err := s.box.Open(pendente, aadPendente(userID))
	if err != nil {
		return nil, fmt.Errorf("decifrar segredo TOTP pendente: %w", err)
	}
	segredo := string(plain.Reveal())
	step, ok := validateTOTP(code, segredo, now)
	if !ok {
		return nil, ErrTOTPInvalid
	}
	ativo, err := s.box.Seal([]byte(segredo), aadAtivo(userID))
	if err != nil {
		return nil, fmt.Errorf("cifrar segredo TOTP: %w", err)
	}
	codes, err := novosCodigosDeRecuperacao()
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit
	q := db.New(tx)

	// A trava de sessões do usuário ANTES de qualquer trava de linha (D1 do
	// Plano 5), na mesma ordem da troca de senha: trava, linha de users,
	// linhas de sessions. Sem ela, uma rotação de refresh em curso deixaria o
	// sucessor fora da revogação.
	if err := q.LockUserSessions(ctx, userID); err != nil {
		return nil, fmt.Errorf("travar sessões: %w", err)
	}
	// A família pode ter caído entre a guarda da rota e a trava. Então nada
	// muda: o 200 sempre quer dizer "trocado, e a sessão atual continua viva".
	viva, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("conferir família de sessão: %w", err)
	}
	if !viva {
		return nil, ErrInvalidSession
	}

	// Compare-and-swap no pendente lido e na família: um rotate que o
	// substituiu, uma confirmação concorrente ou um Disable no meio fazem a
	// WHERE não casar, e o código validado com o segredo velho não promove
	// nada. O bloqueio da conta zera junto (A6 do Plano 10).
	tag, err := tx.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_pending_secret_enc = NULL, totp_pending_family_id = NULL,
		                  totp_last_timestep = $3, failed_attempts = 0, locked_until = NULL
		   WHERE id = $1 AND totp_confirmed_at IS NOT NULL AND totp_pending_secret_enc = $4
		     AND totp_pending_family_id = $5`,
		userID, ativo, step, pendente, familyID)
	if err != nil {
		return nil, fmt.Errorf("promover segredo TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrTOTPNoRotation
	}
	if err := substituirCodigos(ctx, tx, userID, codes); err != nil {
		return nil, err
	}
	if _, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{
		UserID: userID, KeepFamilyID: familyID, Now: now,
	}); err != nil {
		return nil, fmt.Errorf("revogar as outras sessões: %w", err)
	}
	// Trocar o segundo fator é o que um sequestro faria; o evento é a pista
	// do dono (a razão do R39).
	if err := insertAudit(ctx, tx, userID, "totp.rotated", nil, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirmar troca de TOTP: %w", err)
	}
	return codes, nil
}
