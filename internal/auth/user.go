package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockThreshold e LockDuration implementam o bloqueio progressivo da
// spec §5.2.
const (
	LockThreshold = 5
	LockDuration  = 15 * time.Minute
)

type User struct {
	ID             uuid.UUID
	Email          string
	PasswordHash   string
	FailedAttempts int
	LockedUntil    *time.Time
}

// IsLocked recebe o instante em vez de chamar time.Now para que o bloqueio
// seja testável com o relógio injetado.
func (u User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

// NormalizeEmail tira apenas o espaço em volta. O caso real é o autocompletar
// do celular, que costuma acrescentar um espaço à direita: sem isto,
// " pessoa@exemplo.com " devolve 401 para a credencial certa.
//
// É a MESMA função no login e no cadastro pelo CLI, de propósito: duas
// normalizações diferentes criam o e-mail que cadastra e nunca mais entra.
//
// NÃO aplica ToLower. A coluna é citext, então a caixa já é comparada sem
// distinção no banco; baixar a caixa aqui mascararia a ausência do citext caso
// uma migration futura trocasse o tipo da coluna.
func NormalizeEmail(email string) string {
	return strings.TrimSpace(email)
}

// CheckPassword é a verificação de senha do login, e de toda rota que pede a
// senha de novo (troca de senha, cadastro e desativação do segundo fator; D4
// do Plano 5). Custa um Argon2id em todo caminho.
//
// Conta bloqueada é recusada sem olhar a senha, pagando VerifyDummy: o
// bloqueio não se anuncia (R34), nem pela resposta nem pelo tempo. Senha
// errada conta no bloqueio progressivo: sem isto, uma rota que pede a senha
// vira oráculo de senha limitado só por IP para quem tem um access token
// roubado. O sucesso NÃO zera o contador; quem chama decide quando o
// processo de autenticação terminou (com segundo fator, é depois dele).
//
// needsRehash só vale quando ok.
func CheckPassword(ctx context.Context, users UserStore, u User, plain string, now time.Time) (ok, needsRehash bool, err error) {
	if u.IsLocked(now) {
		VerifyDummy()
		return false, false, nil
	}
	ok, needsRehash, err = VerifyPassword(u.PasswordHash, plain)
	if err != nil {
		return false, false, err
	}
	if !ok {
		// Falhar ao registrar não muda a resposta: a senha estava errada.
		if err := users.RecordFailure(ctx, u.ID, now); err != nil {
			slog.Error("registrar falha de senha", "erro", err)
		}
		return false, false, nil
	}
	return true, needsRehash, nil
}

type UserStore interface {
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, userID uuid.UUID) (User, error)
	RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error
	ResetFailures(ctx context.Context, userID uuid.UUID) error
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error
}

func (s *PGStore) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until
		  FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}

// RecordFailure incrementa o contador e aplica o bloqueio no mesmo UPDATE.
// Fazer as duas coisas em uma instrução evita corrida entre tentativas
// simultâneas de força bruta.
//
// Um bloqueio VENCIDO reinicia a contagem, e isso não é detalhe. O contador
// só zerava em ResetFailures, que roda apenas num login bem sucedido — e o
// login bem sucedido é recusado enquanto a conta está travada. O resultado
// era uma armadilha fechada: passado o bloqueio, failed_attempts continuava
// no limiar e UMA senha errada re-travava por mais quinze minutos. Uma
// requisição a cada quinze minutos, 96 por dia, muito abaixo do limitador,
// mantinha a vítima fora da conta para sempre. O atacante não ganhava acesso
// nenhum: era negação de serviço pura sobre conta alheia, e silenciosa,
// porque o bloqueio não se anuncia (R34).
//
// A comparação usa $4, o instante recebido, e nunca now(): o relógio é
// injetado, e um now() do banco divergiria do clock.Fake nos testes.
//
// Ambos os CASE enxergam a linha ANTES do UPDATE, que é o que permite decidir
// as duas colunas a partir do mesmo estado. (Pressupõe LockThreshold >= 2: com
// limiar 1, a contagem reiniciada nunca voltaria a travar no mesmo UPDATE.)
func (s *PGStore) RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET failed_attempts = CASE
		           WHEN locked_until IS NOT NULL AND locked_until <= $4 THEN 1
		           ELSE failed_attempts + 1
		       END,
		       locked_until = CASE
		           WHEN locked_until IS NOT NULL AND locked_until <= $4 THEN NULL
		           WHEN failed_attempts + 1 >= $2 THEN $3
		           ELSE locked_until
		       END
		 WHERE id = $1`,
		userID, LockThreshold, at.Add(LockDuration), at)
	if err != nil {
		return fmt.Errorf("registrar falha de login: %w", err)
	}
	return nil
}

func (s *PGStore) ResetFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("zerar falhas de login: %w", err)
	}
	return nil
}

func (s *PGStore) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
	if err != nil {
		return fmt.Errorf("atualizar hash de senha: %w", err)
	}
	return nil
}

// ByID atende os dois caminhos de TOTP que só têm o id vindo do token: o
// cadastro, que precisa do e-mail para o nome da conta na URI otpauth://, e a
// verificação do segundo fator, que precisa do estado de bloqueio (R40).
//
// Uma consulta só, em vez de um EmailByID ao lado de um ByID lendo a mesma
// linha da mesma tabela.
func (s *PGStore) ByID(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until
		  FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}
