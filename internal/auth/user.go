package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
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
	// TOTPChallengeMark é a marca d'água do desafio de 2FA (Plano 8): o
	// instante do último desafio consumido, nil se nenhum foi. Só ByID a
	// preenche; é o TOTPVerify quem a lê.
	TOTPChallengeMark *time.Time
	// TOTPConfirmedAt é a data de ativação do 2FA, nil se desligado. ByEmail
	// e ByID a preenchem: entra no carimbo do cookie de dispositivo
	// (DeviceStamp).
	TOTPConfirmedAt *time.Time
}

// DeviceStamp é o carimbo das credenciais atuais do usuário para o cookie de
// dispositivo (ver NewDeviceStamp).
func (u User) DeviceStamp() DeviceStamp {
	return NewDeviceStamp(u.PasswordHash, u.TOTPConfirmedAt)
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

// maxEmailBytes é o teto de um endereço no RFC 5321 (caminho de 256 menos os
// dois sinais de maior e menor).
const maxEmailBytes = 254

func emailPossivel(email string) bool {
	return len(email) <= maxEmailBytes && !strings.ContainsFunc(email, unicode.IsControl)
}

// Attempts é o contador de tentativas de uma credencial de login (A2 e A3 do
// Plano 10): o bloqueio da conta (AccountAttempts) ou o do cookie de
// dispositivo (Devices.Attempts). A tentativa é RESERVADA antes de verificar
// — N tentativas simultâneas gastam N, e a que passa do limite nem chega a
// verificar —, e quem reservou decide o fim dela:
//
//   - errada: nada; a reserva fica contada;
//   - certa, com o login terminando aqui: Reset (zera);
//   - certa, com outro passo pela frente (a senha certa com 2FA ligado):
//     Refund, que devolve só esta reserva. Zerar ali daria ao atacante que já
//     tem a senha um botão de destravar entre um chute de seis dígitos e o
//     próximo (R40).
type Attempts interface {
	// Reserve devolve false quando o contador está bloqueado: nada foi
	// reservado, e quem chama recusa sem verificar a credencial.
	Reserve(ctx context.Context) (bool, error)
	Refund(ctx context.Context) error
	Reset(ctx context.Context) error
}

// AccountAttempts é o bloqueio da conta (spec §5.2) como Attempts: users,
// com o relógio do instante do login.
func AccountAttempts(users UserStore, userID uuid.UUID, now time.Time) Attempts {
	return contaAttempts{users: users, userID: userID, now: now}
}

type contaAttempts struct {
	users  UserStore
	userID uuid.UUID
	now    time.Time
}

func (c contaAttempts) Reserve(ctx context.Context) (bool, error) {
	return c.users.ReserveAttempt(ctx, c.userID, c.now)
}

func (c contaAttempts) Refund(ctx context.Context) error {
	return c.users.RefundAttempt(ctx, c.userID)
}

func (c contaAttempts) Reset(ctx context.Context) error {
	return c.users.ResetFailures(ctx, c.userID)
}

// CheckPassword é a verificação de senha do login. Custa um Argon2id em todo
// caminho. As rotas autenticadas que pedem a senha de novo (troca de senha e
// as do 2FA) não passam por aqui desde o A3 do Plano 10: elas usam
// VerifyPassword e o contador da família de sessão, e não o bloqueio da conta.
//
// A ordem é o ponto (A2 do Plano 10): a vaga do Argon2id primeiro, depois a
// reserva da tentativa, e só então a verificação. Antes, o bloqueio era lido
// antes e a falha gravada depois do Argon2id, e N requisições simultâneas
// passavam todas pela leitura: o limite de 5 virava o número de origens do
// atacante. A vaga vem antes da reserva para que um 429 do semáforo não gaste
// tentativa do dono.
//
// Contador bloqueado é recusado sem olhar a senha, pagando o mesmo Argon2id
// (o dummy): o bloqueio não se anuncia (R34), nem pela resposta nem pelo
// tempo. Senha errada deixa a reserva contada. Senha certa também deixa a
// reserva: quem chama devolve (Refund) ou zera (Reset), conforme o login
// termine ou não aqui (ver Attempts).
//
// needsRehash só vale quando ok. Com o teto do Argon2id cheio, devolve
// ErrArgon2Busy sem reservar nada (A1 do Plano 10).
func CheckPassword(ctx context.Context, t Attempts, u User, plain string) (ok, needsRehash bool, err error) {
	soltar, err := ReserveArgon2(ctx)
	if err != nil {
		return false, false, err
	}
	defer soltar()

	reservou, err := t.Reserve(ctx)
	if err != nil {
		return false, false, err
	}
	if !reservou {
		_, _, _ = verificarSenha(dummyHash(), plain)
		return false, false, nil
	}
	return verificarSenha(u.PasswordHash, plain)
}

type UserStore interface {
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, userID uuid.UUID) (User, error)
	// ReserveAttempt reserva uma tentativa de credencial no bloqueio da
	// conta; false quando a conta está bloqueada em at (nada reservado).
	ReserveAttempt(ctx context.Context, userID uuid.UUID, at time.Time) (bool, error)
	// RefundAttempt devolve uma reserva de uma prova certa num passo
	// intermediário do login (ver Attempts).
	RefundAttempt(ctx context.Context, userID uuid.UUID) error
	ResetFailures(ctx context.Context, userID uuid.UUID) error
	// RehashPassword troca oldHash por newHash só se oldHash ainda for o hash
	// gravado; swapped diz se trocou.
	RehashPassword(ctx context.Context, userID uuid.UUID, oldHash, newHash string) (swapped bool, err error)
	// ConsumeChallenge avança a marca d'água do desafio de 2FA até issuedAt,
	// só se o desafio for mais novo que ela; consumed diz se avançou. Um
	// desafio que não avança a marca já foi usado, ou é mais antigo que um
	// usado (Plano 8).
	ConsumeChallenge(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (consumed bool, err error)
}

func (s *PGStore) ByEmail(ctx context.Context, email string) (User, error) {
	// Um e-mail que nenhuma conta pode ter (controle, NUL, mais que o teto
	// do RFC 5321) é conta inexistente: o NUL nem chega ao Postgres, que o
	// recusa em text e viraria 500.
	if !emailPossivel(email) {
		return User{}, ErrNotFound
	}
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until, totp_confirmed_at
		  FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil, &u.TOTPConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}

// ReserveAttempt reserva a tentativa ANTES da verificação, numa instrução só
// (A2 do Plano 10): incrementa o contador e aplica o bloqueio no mesmo
// UPDATE, e só se a conta não estiver bloqueada em at. Sem linha de volta, a
// conta está bloqueada, e nada muda. Duas reservas simultâneas disputam a
// mesma linha: a segunda espera a primeira comitar e relê (READ COMMITTED),
// então N reservas simultâneas contam N, e as que chegam depois do bloqueio
// não passam.
//
// A reserva que atinge o limiar trava a conta e ainda assim é concedida: ela é
// a quinta tentativa, e é verificada. Se estiver certa, quem chama zera.
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
// A comparação usa $3, o instante recebido, e nunca now(): o relógio é
// injetado, e um now() do banco divergiria do clock.Fake nos testes.
//
// Ambos os CASE enxergam a linha ANTES do UPDATE, que é o que permite decidir
// as duas colunas a partir do mesmo estado. (Pressupõe LockThreshold >= 2: com
// limiar 1, a contagem reiniciada nunca voltaria a travar no mesmo UPDATE.)
func (s *PGStore) ReserveAttempt(ctx context.Context, userID uuid.UUID, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET failed_attempts = CASE
		           WHEN locked_until IS NOT NULL AND locked_until <= $3 THEN 1
		           ELSE failed_attempts + 1
		       END,
		       locked_until = CASE
		           WHEN locked_until IS NOT NULL AND locked_until <= $3 THEN NULL
		           WHEN failed_attempts + 1 >= $2 THEN $4
		           ELSE locked_until
		       END
		 WHERE id = $1 AND (locked_until IS NULL OR locked_until <= $3)`,
		userID, LockThreshold, at, at.Add(LockDuration))
	if err != nil {
		return false, fmt.Errorf("reservar tentativa de login: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RefundAttempt devolve UMA reserva: a de uma senha certa com o segundo fator
// pela frente. Se a reserva devolvida tinha travado a conta (era a quinta), a
// trava sai com ela; abaixo do limiar não há trava a manter.
func (s *PGStore) RefundAttempt(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET failed_attempts = GREATEST(failed_attempts - 1, 0),
		       locked_until = CASE WHEN failed_attempts - 1 < $2 THEN NULL ELSE locked_until END
		 WHERE id = $1`,
		userID, LockThreshold)
	if err != nil {
		return fmt.Errorf("devolver tentativa de login: %w", err)
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

// RehashPassword é o rehash do login, e é um compare-and-swap (Task 11 do
// Plano 5). A versão anterior gravava sem condição: um login que verificou a
// senha ANTIGA e regravou o hash dela depois de um set-password concorrente
// desfazia a redefinição, e a senha antiga voltava a valer.
func (s *PGStore) RehashPassword(ctx context.Context, userID uuid.UUID, oldHash, newHash string) (bool, error) {
	n, err := db.New(s.pool).RehashUser(ctx, db.RehashUserParams{ID: userID, OldHash: oldHash, NewHash: newHash})
	if err != nil {
		return false, fmt.Errorf("atualizar hash de senha: %w", err)
	}
	return n == 1, nil
}

// ConsumeChallenge é um compare-and-swap numa instrução: dois /totp/verify
// concorrentes com o mesmo desafio disputam a mesma linha, o segundo espera o
// commit do primeiro, relê a linha (READ COMMITTED) e não satisfaz mais o
// WHERE. Não precisa de trava explícita.
//
// O "<" estrito é o que faz o desafio valer uma vez: com "<=", o mesmo
// desafio passaria de novo, porque a marca ficou igual ao instante dele.
func (s *PGStore) ConsumeChallenge(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET totp_challenge_mark = $2
		 WHERE id = $1 AND (totp_challenge_mark IS NULL OR totp_challenge_mark < $2)`,
		userID, issuedAt)
	if err != nil {
		return false, fmt.Errorf("consumir desafio de 2FA: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ByID atende os dois caminhos de TOTP que só têm o id vindo do token: o
// cadastro, que precisa do e-mail para o nome da conta na URI otpauth://, e a
// verificação do segundo fator, que precisa do estado de bloqueio (R40) e da
// marca d'água do desafio (Plano 8).
//
// Uma consulta só, em vez de um EmailByID ao lado de um ByID lendo a mesma
// linha da mesma tabela.
func (s *PGStore) ByID(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until, totp_challenge_mark, totp_confirmed_at
		  FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil, &u.TOTPChallengeMark, &u.TOTPConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}
