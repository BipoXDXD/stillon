package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

var (
	ErrTOTPInvalid     = errors.New("código de segundo fator inválido")
	ErrTOTPNotEnrolled = errors.New("segundo fator não cadastrado")
)

const (
	totpPeriod = 30
	// Skew de 1 aceita o código do período anterior e do seguinte, cobrindo
	// relógio de celular levemente fora. Valores maiores alargam demais a
	// janela em que um código capturado continua servindo.
	totpSkew          = 1
	recoveryCodeCount = 10
	recoveryCodeBytes = 10
)

// TOTP implementa o segundo fator da spec §5.2.
//
// O segredo fica cifrado em repouso com a AAD amarrada à tabela, à coluna e ao
// id do usuário: o selo de uma linha não abre no lugar de outra.
type TOTP struct {
	pool   *pgxpool.Pool
	box    *crypto.Box
	issuer string
}

func NewTOTP(pool *pgxpool.Pool, box *crypto.Box, issuer string) *TOTP {
	return &TOTP{pool: pool, box: box, issuer: issuer}
}

// Enroll gera um segredo novo e o grava cifrado, ainda não confirmado.
//
// Gravar sem ativar é deliberado: se o usuário fechar a tela no meio do
// cadastro, a conta continua acessível com senha apenas.
func (s *TOTP) Enroll(ctx context.Context, userID uuid.UUID, email string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: email,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // exigido por Google Authenticator e afins
	})
	if err != nil {
		return "", fmt.Errorf("gerar chave TOTP: %w", err)
	}

	sealed, err := s.box.Seal([]byte(key.Secret()), crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", fmt.Errorf("cifrar segredo TOTP: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_confirmed_at = NULL WHERE id = $1`,
		userID, sealed)
	if err != nil {
		return "", fmt.Errorf("gravar segredo TOTP: %w", err)
	}

	return key.URL(), nil
}

// Confirm ativa o segundo fator e devolve os códigos de recuperação. Eles são
// mostrados uma única vez: o banco guarda apenas o hash.
func (s *TOTP) Confirm(ctx context.Context, userID uuid.UUID, code string, now time.Time) ([]string, error) {
	secret, _, err := s.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !validateTOTP(code, secret, now) {
		return nil, ErrTOTPInvalid
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		raw := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("gerar código de recuperação: %w", err)
		}
		codes[i] = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, err := tx.Exec(ctx,
		`UPDATE users SET totp_confirmed_at = $2 WHERE id = $1`, userID, now); err != nil {
		return nil, fmt.Errorf("ativar TOTP: %w", err)
	}
	// Recadastro invalida os códigos anteriores.
	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("limpar códigos antigos: %w", err)
	}
	for _, c := range codes {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("gerar id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
			id, userID, crypto.HashToken(c)); err != nil {
			return nil, fmt.Errorf("gravar código de recuperação: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirmar TOTP: %w", err)
	}
	return codes, nil
}

// Verify aceita tanto um código do app quanto um código de recuperação. O de
// recuperação é consumido no momento do uso.
func (s *TOTP) Verify(ctx context.Context, userID uuid.UUID, code string, now time.Time) error {
	secret, confirmed, err := s.load(ctx, userID)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrTOTPNotEnrolled
	}

	if validateTOTP(code, secret, now) {
		return nil
	}

	// DELETE ... RETURNING resolve o consumo do código em uma única
	// instrução: dois usos simultâneos do mesmo código não podem ambos
	// encontrar a linha.
	var id uuid.UUID
	err = s.pool.QueryRow(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1 AND code_hash = $2 RETURNING id`,
		userID, crypto.HashToken(code)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTOTPInvalid
	}
	if err != nil {
		return fmt.Errorf("consumir código de recuperação: %w", err)
	}
	return nil
}

func (s *TOTP) IsEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("consultar TOTP: %w", err)
	}
	return confirmed != nil, nil
}

func (s *TOTP) load(ctx context.Context, userID uuid.UUID) (secret string, confirmed bool, err error) {
	var sealed []byte
	var at *time.Time

	err = s.pool.QueryRow(ctx,
		`SELECT totp_secret_enc, totp_confirmed_at FROM users WHERE id = $1`, userID).
		Scan(&sealed, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("carregar TOTP: %w", err)
	}
	if len(sealed) == 0 {
		return "", false, ErrTOTPNotEnrolled
	}

	plain, err := s.box.Open(sealed, crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", false, fmt.Errorf("decifrar segredo TOTP: %w", err)
	}
	return string(plain.Reveal()), at != nil, nil
}

func validateTOTP(code, secret string, now time.Time) bool {
	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}
