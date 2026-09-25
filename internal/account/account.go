// Package account é o serviço da conta do dono (Plano 5): criar e redefinir a
// senha pelo CLI, e desligar o 2FA pelo CLI (reset-2fa, Plano 9); ler a conta
// e trocar a senha pela API. O CLI e a API chamam o mesmo serviço, para que a
// política de senha e a normalização do e-mail sejam as mesmas nos dois (D20
// do Plano 5).
//
// users e sessions não têm RLS (D7 do Plano 2). A barreira aqui é outra: toda
// query que parte de um usuário recebe o id dele, e cada uma tem teste de
// isolamento com sabotagem.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

var (
	// ErrUserExists: o sistema já tem usuário (spec §2.1: usuário único).
	ErrUserExists = errors.New("já existe um usuário")
	// ErrUserNotFound: nenhum usuário com este e-mail.
	ErrUserNotFound = errors.New("usuário não cadastrado")
	// ErrInvalidEmail: não é um endereço puro (sem nome de exibição).
	ErrInvalidEmail = errors.New("e-mail inválido")
	// ErrInvalidCredentials: senha atual errada, segundo fator errado ou
	// ausente, conta bloqueada, ou a senha mudou no meio. Um erro só, pelo
	// motivo do R34: distinguir os casos contaria ao atacante o que acertou.
	ErrInvalidCredentials = errors.New("credenciais inválidas")
	// ErrSessionGone: a família de sessão do token já não está viva (D11 do
	// Plano 5), ou o usuário não existe mais.
	ErrSessionGone = errors.New("sessão encerrada")
	// ErrSessionNotFound: a família pedida não aparece em GET /sessions —
	// inexistente, de outro usuário, já revogada ou vencida (D14).
	ErrSessionNotFound = errors.New("sessão não encontrada")
)

// PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
// ou auth.ReasonTooLong.
type PolicyError struct{ Reason string }

func (e PolicyError) Error() string { return "senha fora da política: " + e.Reason }

// maxEmailLen é o limite prático de um endereço (RFC 5321 §4.5.3.1.3 e a
// errata 1690: 254 no caminho).
const maxEmailLen = 254

type Service struct {
	pool  *pgxpool.Pool
	users auth.UserStore
	totp  *auth.TOTP
	clk   clock.Clock
	ipKey []byte
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem de usuários e a inserção.
	depoisDeContar func()
}

// NewService: totp e ipKey só servem à API (troca de senha). O CLI passa nil
// nos dois, e ChangePassword recusa rodar sem eles.
func NewService(pool *pgxpool.Pool, totp *auth.TOTP, clk clock.Clock, ipKey []byte) *Service {
	return &Service{pool: pool, users: auth.NewPGStore(pool), totp: totp, clk: clk, ipKey: ipKey}
}

// Account é o que GET /me devolve.
type Account struct {
	ID          uuid.UUID
	Email       string
	TOTPEnabled bool
	CreatedAt   time.Time
}

// Me devolve ErrUserNotFound se o usuário do token não existe mais.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (Account, error) {
	r, err := db.New(s.pool).GetAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrUserNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("ler conta: %w", err)
	}
	return Account{ID: r.ID, Email: r.Email, TOTPEnabled: r.TotpEnabled, CreatedAt: r.CreatedAt}, nil
}

// FamilyIsLive diz se a família de sessão do token ainda está viva: tem a
// linha corrente, não usada, não revogada e não vencida.
func (s *Service) FamilyIsLive(ctx context.Context, userID, familyID uuid.UUID) (bool, error) {
	return familyIsLive(ctx, db.New(s.pool), userID, familyID, s.clk.Now())
}

func familyIsLive(ctx context.Context, q *db.Queries, userID, familyID uuid.UUID, now time.Time) (bool, error) {
	vivo, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
	if err != nil {
		return false, fmt.Errorf("conferir família de sessão: %w", err)
	}
	return vivo, nil
}

// ChangePasswordInput é o corpo de POST /auth/password.
type ChangePasswordInput struct {
	Current  string
	New      string
	TOTPCode string
}

// ChangePassword troca a senha pela API (D12 do Plano 5), na ordem:
//
//  1. a política da senha nova (pública: validá-la primeiro não vaza nada e
//     não gasta Argon2 nem conta falha por erro de digitação na nova);
//  2. a família do token viva (leitura barata; a verdade vem no passo 6);
//  3. a senha atual (auth.VerifyPassword);
//  4. o segundo fator, se ligado, por auth.TOTP.Verify (aceita o código de
//     recuperação);
//  5. o hash novo, fora da transação;
//  6. a transação: trava do usuário (D1), família viva de novo, CAS do hash,
//     as OUTRAS famílias revogadas, auditoria.
//
// O bloqueio da CONTA não entra aqui (A3 do Plano 10): nem recusa a conta
// bloqueada, nem conta a prova errada. Quem conta as provas erradas desta
// rota é o contador da família de sessão, no handler (auth.FamilyProofs).
//
// Devolve PolicyError, ErrSessionGone ou ErrInvalidCredentials; nil quer
// dizer "trocada, e a família atual continua viva". Com o teto do Argon2id
// cheio, auth.ErrArgon2Busy.
func (s *Service) ChangePassword(ctx context.Context, userID, familyID uuid.UUID, in ChangePasswordInput, meta auth.Meta) error {
	if s.totp == nil || len(s.ipKey) == 0 {
		return errors.New("account: ChangePassword sem segundo fator ou sem chave de IP")
	}
	if motivo := auth.ValidateNewPassword(in.New); motivo != "" {
		return PolicyError{Reason: motivo}
	}
	now := s.clk.Now()

	vivo, err := s.FamilyIsLive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if !vivo {
		return ErrSessionGone
	}
	u, err := s.users.ByID(ctx, userID)
	if errors.Is(err, auth.ErrNotFound) {
		return ErrSessionGone
	}
	if err != nil {
		return err
	}

	ok, _, err := auth.VerifyPassword(ctx, u.PasswordHash, in.Current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}

	ligado, err := s.totp.IsEnabled(ctx, userID)
	if err != nil {
		return err
	}
	if ligado {
		// O Verify consome o timestep (ou o código de recuperação) fora da
		// transação abaixo: se ela desistir, quem tentar de novo espera o
		// próximo código de 30 s.
		err := s.totp.Verify(ctx, userID, in.TOTPCode, now)
		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
	}

	hash, err := auth.HashPassword(ctx, in.New)
	if err != nil {
		return err
	}
	ipHash := crypto.HashIP(s.ipKey, meta.IP)

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		// A família pode ter caído entre o passo 2 e a trava (um DELETE
		// /sessions de outro aparelho, a detecção de reúso). Então nada muda:
		// o 204 sempre quer dizer "trocada e a atual continua viva".
		vivo, err := familyIsLive(ctx, q, userID, familyID, now)
		if err != nil {
			return err
		}
		if !vivo {
			return ErrSessionGone
		}
		n, err := q.SwapUserHash(ctx, db.SwapUserHashParams{ID: userID, NewHash: hash, OldHash: u.PasswordHash})
		if err != nil {
			return fmt.Errorf("gravar senha: %w", err)
		}
		if n == 0 {
			// A senha mudou depois de verificada: a "atual" já não é.
			return ErrInvalidCredentials
		}
		if _, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{
			UserID: userID, KeepFamilyID: familyID, Now: now,
		}); err != nil {
			return fmt.Errorf("revogar as outras sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.password_changed", nil, nil, ipHash, now)
	})
}

// ValidateEmail normaliza como o login (auth.NormalizeEmail) e exige um
// endereço puro: "Nome <a@b.com>" é recusado, porque o login compara o texto
// inteiro e nunca casaria com ele.
func ValidateEmail(email string) (string, error) {
	e := auth.NormalizeEmail(email)
	if len(e) == 0 || len(e) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Name != "" || addr.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// HasUsers responde se o sistema já tem usuário. O CLI pergunta ANTES de
// pedir a senha, para ninguém digitar duas vezes à toa; CreateUser confere de
// novo sob a trava.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := db.New(s.pool).CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("contar usuários: %w", err)
	}
	return n > 0, nil
}

// UserIDByEmail devolve ErrUserNotFound quando o e-mail não está cadastrado.
func (s *Service) UserIDByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	id, err := db.New(s.pool).GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("buscar usuário: %w", err)
	}
	return id, nil
}

// CreateUser cria o dono. Recusa com ErrUserExists se já existe QUALQUER
// usuário (D8 do Plano 5), e a recusa não muda nada.
//
// A contagem e a inserção rodam sob uma advisory lock de transação (D9): sem
// ela, dois CLIs simultâneos contariam zero e criariam dois donos.
func (s *Service) CreateUser(ctx context.Context, email, password string) (uuid.UUID, error) {
	e, err := ValidateEmail(email)
	if err != nil {
		return uuid.Nil, err
	}
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return uuid.Nil, PolicyError{Reason: motivo}
	}
	// O Argon2id leva ~100 ms: fora da transação, para não segurar a trava.
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("gerar id do usuário: %w", err)
	}
	now := s.clk.Now()

	err = s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockUserCreation(ctx); err != nil {
			return fmt.Errorf("travar criação de usuário: %w", err)
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("contar usuários: %w", err)
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n > 0 {
			return ErrUserExists
		}
		if err := q.InsertUser(ctx, db.InsertUserParams{
			ID: id, Email: e, PasswordHash: hash, CreatedAt: now,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrUserExists
			}
			return fmt.Errorf("inserir usuário: %w", err)
		}
		return audit(ctx, q, id, "user.created", nil, nil, nil, now)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ResetPassword é o caminho de recuperação do operador (vigilume user
// set-password, D10 do Plano 5): grava a senha nova, tira o bloqueio e encerra
// TODAS as sessões. Recuperação quase sempre é "perdi" ou "vazou", e manter as
// sessões deixaria o invasor dentro. Não mexe no segundo fator.
//
// Devolve quantas sessões (famílias vivas, as que GET /sessions mostraria)
// foram encerradas; as linhas já rotacionadas de uma família não contam.
func (s *Service) ResetPassword(ctx context.Context, email, password string) (int64, error) {
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return 0, PolicyError{Reason: motivo}
	}
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		return 0, err
	}
	now := s.clk.Now()

	var revogadas int64
	err = s.inTx(ctx, func(q *db.Queries) error {
		userID, err := q.GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar usuário: %w", err)
		}
		// A trava do usuário antes de tocar em sessions (D1): sem ela, uma
		// rotação em curso deixaria o sucessor fora da revogação.
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		if _, err := q.ReplaceUserHash(ctx, db.ReplaceUserHashParams{ID: userID, PasswordHash: hash}); err != nil {
			return fmt.Errorf("gravar senha: %w", err)
		}
		revogadas, err = q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("revogar sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.password_reset", nil, nil, nil, now)
	})
	return revogadas, err
}

// ResetTwoFactor é o `vigilume user reset-2fa` (Plano 9, §3.1): a saída de quem
// perdeu o autenticador E os códigos de recuperação, sem SQL à mão. Não pede
// senha: quem tem a DATABASE_URL já pode tudo no banco, e o comando existe
// para que o SQL certo não precise ser escrito, não como fronteira de
// segurança.
//
// Numa transação: o 2FA desligado (segredo, confirmação, timestep e a troca
// pendente), o bloqueio da conta zerado (A6 do Plano 10, como o
// set-password), os códigos de recuperação apagados, TODAS as sessões
// encerradas (quem estivesse dentro com uma sessão roubada sai, como no
// set-password) e a auditoria totp.reset_cli. Sem 2FA ligado, nada muda: ligado volta false, sem
// sessão encerrada e sem auditoria.
//
// Devolve ErrUserNotFound para e-mail não cadastrado; revogadas conta as
// famílias vivas, como o ResetPassword.
func (s *Service) ResetTwoFactor(ctx context.Context, email string) (ligado bool, revogadas int64, err error) {
	now := s.clk.Now()
	err = s.inTx(ctx, func(q *db.Queries) error {
		userID, err := q.GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar usuário: %w", err)
		}
		// A trava do usuário antes de tocar em users e sessions (D1), na
		// ordem do set-password: sem ela, uma rotação em curso deixaria o
		// sucessor fora da revogação.
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		n, err := q.ResetTOTP(ctx, userID)
		if err != nil {
			return fmt.Errorf("desligar 2FA: %w", err)
		}
		if n == 0 {
			return nil // já estava desligado: nada muda
		}
		ligado = true
		if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
			return fmt.Errorf("apagar códigos de recuperação: %w", err)
		}
		revogadas, err = q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("revogar sessões: %w", err)
		}
		return audit(ctx, q, userID, "totp.reset_cli", nil, nil, nil, now)
	})
	if err != nil {
		return false, 0, err
	}
	return ligado, revogadas, nil
}

// Session é uma família de refresh viva, como GET /sessions a mostra (D13 do
// Plano 5). Não tem IP nem hash: não há campo onde eles caibam.
type Session struct {
	// ID é a família (family_id), o mesmo id do claim sid.
	ID uuid.UUID
	// CreatedAt é o login que abriu a família: o instante do UUIDv7 dela. O
	// menor created_at das linhas não serve: a retenção apaga as linhas
	// antigas de uma família que continua viva, e ele andaria para a frente.
	CreatedAt time.Time
	// LastUsedAt é o último refresh (granularidade de ~15 min num painel ativo).
	LastUsedAt time.Time
	// UserAgent é o do último refresh, cortado em maxUserAgent code points.
	// É texto de quem fez o login, inclusive de um invasor.
	UserAgent string
	Current   bool
}

const maxUserAgent = 256

// ListSessions devolve as famílias vivas do usuário, da mais nova para a mais
// antiga pelo login (created_at), e não pelo último uso: uma ordem que muda a
// cada refresh faria a linha trocar de lugar embaixo do clique de "encerrar".
func (s *Service) ListSessions(ctx context.Context, userID, current uuid.UUID) ([]Session, error) {
	rows, err := db.New(s.pool).ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: userID, Now: s.clk.Now()})
	if err != nil {
		return nil, fmt.Errorf("listar sessões: %w", err)
	}
	// Uma linha viva por família é invariante de Rotate (marca a antiga e
	// insere a nova na mesma transação). Não depender dela custa pouco: fica a
	// linha mais recente.
	porFamilia := map[uuid.UUID]db.ListLiveFamiliesRow{}
	for _, r := range rows {
		if atual, ok := porFamilia[r.FamilyID]; !ok || r.CreatedAt.After(atual.CreatedAt) {
			porFamilia[r.FamilyID] = r
		}
	}
	out := make([]Session, 0, len(porFamilia))
	for id, r := range porFamilia {
		seg, nseg := id.Time().UnixTime()
		out = append(out, Session{
			ID: id, CreatedAt: time.Unix(seg, nseg).UTC(), LastUsedAt: r.CreatedAt.UTC(),
			UserAgent: cortar(r.UserAgent, maxUserAgent), Current: id == current,
		})
	}
	slices.SortFunc(out, func(a, b Session) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return slices.Compare(b.ID[:], a.ID[:])
	})
	return out, nil
}

// cortar corta em n code points sem partir um caractere ao meio.
func cortar(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// RevokeSession encerra a família pedida (DELETE /sessions/{id}). Só se ela
// estiver viva E for do usuário; qualquer outra coisa é ErrSessionNotFound
// (D14): a família "existe" para a API exatamente quando aparece em GET
// /sessions.
func (s *Service) RevokeSession(ctx context.Context, userID, familyID uuid.UUID, meta auth.Meta) error {
	return s.revogarFamilia(ctx, userID, familyID, meta, "session.revoked")
}

// RevokeFamilyForWrongProofs derruba a família que errou provas demais numa
// rota autenticada (A3 do Plano 10), com a auditoria session.proof_limit: é a
// pista do dono de que alguém com uma sessão dele chutou a senha ou o código.
// A família já morta é nil: duas requisições que estouram juntas derrubam uma
// vez só.
func (s *Service) RevokeFamilyForWrongProofs(ctx context.Context, userID, familyID uuid.UUID, meta auth.Meta) error {
	err := s.revogarFamilia(ctx, userID, familyID, meta, "session.proof_limit")
	if errors.Is(err, ErrSessionNotFound) {
		return nil
	}
	return err
}

func (s *Service) revogarFamilia(ctx context.Context, userID, familyID uuid.UUID, meta auth.Meta, acao string) error {
	if len(s.ipKey) == 0 {
		return errors.New("account: revogar sessão sem chave de IP")
	}
	now := s.clk.Now()
	ipHash := crypto.HashIP(s.ipKey, meta.IP)
	return s.inTx(ctx, func(q *db.Queries) error {
		// A trava do usuário antes de tocar em sessions (D1): sem ela, uma
		// rotação em curso da família deixaria o sucessor vivo.
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		vivo, err := familyIsLive(ctx, q, userID, familyID, now)
		if err != nil {
			return err
		}
		if !vivo {
			return ErrSessionNotFound
		}
		if _, err := q.RevokeFamilyOfUser(ctx, db.RevokeFamilyOfUserParams{
			UserID: userID, FamilyID: familyID, Now: now,
		}); err != nil {
			return fmt.Errorf("revogar sessão: %w", err)
		}
		entidade := "session_family"
		return audit(ctx, q, userID, acao, &entidade, &familyID, ipHash, now)
	})
}

func (s *Service) inTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// audit grava na mesma transação da mudança: não existe mudança sem rastro,
// nem rastro de mudança que não aconteceu (mesma razão do R39).
func audit(ctx context.Context, q *db.Queries, userID uuid.UUID, action string, entity *string, entityID *uuid.UUID, ipHash []byte, at time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	if err := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ID: id, UserID: &userID, Action: action, Entity: entity, EntityID: entityID, IpHash: ipHash, At: at,
	}); err != nil {
		return fmt.Errorf("auditar %s: %w", action, err)
	}
	return nil
}
