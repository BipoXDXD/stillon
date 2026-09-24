package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

const (
	// MaxChannels é o limite de canais por usuário. Canal é destino de
	// envio: sem teto, cada canal novo seria mais um alvo de mensagem.
	MaxChannels = 10
	// VerifyTTL é a validade do link de verificação (spec §5.8).
	VerifyTTL = 24 * time.Hour
	// tokenBytes é o tamanho do token de verificação, como o do refresh
	// (spec §5.2): 256 bits de crypto/rand.
	tokenBytes = 32
)

var (
	ErrNotFound       = errors.New("canal não encontrado")
	ErrLimitReached   = errors.New("limite de canais atingido")
	ErrDeliveryFailed = errors.New("a mensagem de verificação não foi entregue")
	ErrNotPending     = errors.New("o canal não está pendente de verificação")
	ErrInvalidToken   = errors.New("token de verificação inválido ou vencido")
)

// Motivos de ValidationError, no vocabulário fechado da API (D14 do Plano 2).
const (
	ReasonRequired    = "required"
	ReasonInvalid     = "invalid"
	ReasonUnavailable = "unavailable"
)

// ValidationError aponta o campo culpado. Field e Reason vêm sempre de
// constantes do código, nunca da entrada.
type ValidationError struct {
	Field, Reason string
}

func (e *ValidationError) Error() string { return "canal inválido: " + e.Field + " " + e.Reason }

// DiscordTester manda a mensagem de teste do cadastro. *Discord satisfaz.
type DiscordTester interface {
	SendTest(ctx context.Context, dest crypto.Secret) error
}

// VerificationMailer manda o link de verificação. *Resend satisfaz.
type VerificationMailer interface {
	SendVerification(ctx context.Context, dest crypto.Secret, link string) error
}

// Channel é o que sai do domínio. Não tem campo para o destino: só o hint
// (D5 do Plano 2, aplicado aos canais).
type Channel struct {
	ID        uuid.UUID
	Kind      Kind
	Hint      string
	Status    ChannelStatus
	Enabled   bool
	CreatedAt time.Time
	// LastFailureAt e LastError vêm da última falha definitiva de envio
	// (channel_health). Zero e "" quando nunca falhou de vez.
	LastFailureAt time.Time
	LastError     Reason
}

type ChannelStatus string

const (
	StatusVerified ChannelStatus = "verified"
	StatusPending  ChannelStatus = "pending"
	StatusExpired  ChannelStatus = "expired"
	// StatusFailing é o canal verificado cuja última entrega falhou de vez,
	// sem sucesso depois dela: o webhook foi apagado, o domínio do e-mail
	// perdeu a verificação. O canal continua recebendo tentativas; o status
	// existe para que o dono saiba (pendência do Plano 3).
	StatusFailing ChannelStatus = "failing"
)

type ChannelInput struct {
	Kind  Kind
	URL   string // Discord
	Email string // e-mail
}

// ChannelService é o cadastro de canais (spec §8, §5.8).
type ChannelService struct {
	pool    *pgxpool.Pool
	box     *crypto.Box
	clk     clock.Clock
	discord DiscordTester
	// email é nil quando o operador não configurou o Resend: cadastrar
	// e-mail responde erro de validação no campo kind.
	email   VerificationMailer
	baseURL string
}

// NewChannelService recebe publicBaseURL para montar o link de verificação.
// Passe um email nil (a interface nil, e não um *Resend nil) para desligar
// o e-mail.
func NewChannelService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, discord DiscordTester, email VerificationMailer, publicBaseURL string) *ChannelService {
	return &ChannelService{
		pool: pool, box: box, clk: clk, discord: discord, email: email,
		baseURL: strings.TrimSuffix(publicBaseURL, "/"),
	}
}

// Create verifica o destino ANTES de gravar: a mensagem de teste do Discord
// ou o e-mail com o link. Se o envio falha, nada é gravado, e o cliente
// recebe ErrDeliveryFailed.
//
// O limite é conferido duas vezes. Antes do envio, para não mandar mensagem
// a um canal que não será gravado; na gravação, com o usuário travado, para
// que duas criações simultâneas não passem as duas.
func (s *ChannelService) Create(ctx context.Context, userID uuid.UUID, in ChannelInput) (Channel, error) {
	dest, err := s.validar(in)
	if err != nil {
		return Channel{}, err
	}
	if err := s.cabe(ctx, userID, false); err != nil {
		return Channel{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Channel{}, fmt.Errorf("gerar id do canal: %w", err)
	}
	sealed, err := sealConfig(s.box, id, dest)
	if err != nil {
		return Channel{}, err
	}
	now := s.clk.Now()
	p := db.InsertChannelParams{ID: id, UserID: userID, Kind: string(in.Kind), ConfigEnc: sealed, CreatedAt: now}
	out := Channel{ID: id, Kind: in.Kind, Hint: hint(in.Kind, crypto.Secret(dest)), Enabled: true, CreatedAt: now}

	envio, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	switch in.Kind {
	case KindDiscord:
		if err := s.discord.SendTest(envio, crypto.Secret(dest)); err != nil {
			slog.Warn("mensagem de teste do Discord falhou", "motivo", motivo(err))
			return Channel{}, ErrDeliveryFailed
		}
		p.VerifiedAt = &now
		out.Status = StatusVerified
	case KindEmail:
		token, hash, err := novoToken()
		if err != nil {
			return Channel{}, err
		}
		expira := now.Add(VerifyTTL)
		if err := s.email.SendVerification(envio, crypto.Secret(dest), s.link(token)); err != nil {
			slog.Warn("e-mail de verificação falhou", "motivo", motivo(err))
			return Channel{}, ErrDeliveryFailed
		}
		p.VerifyTokenHash, p.VerifyExpiresAt = hash, &expira
		out.Status = StatusPending
	}

	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := s.cabeNa(ctx, q, userID, true); err != nil {
			return err
		}
		return q.InsertChannel(ctx, p)
	})
	if err != nil {
		return Channel{}, err
	}
	return out, nil
}

func (s *ChannelService) validar(in ChannelInput) (string, error) {
	switch in.Kind {
	case KindDiscord:
		u := strings.TrimSpace(in.URL)
		if u == "" {
			return "", &ValidationError{"url", ReasonRequired}
		}
		if ValidateDiscordURL(u) != nil {
			return "", &ValidationError{"url", ReasonInvalid}
		}
		return u, nil
	case KindEmail:
		if s.email == nil {
			return "", &ValidationError{"kind", ReasonUnavailable}
		}
		if strings.TrimSpace(in.Email) == "" {
			return "", &ValidationError{"email", ReasonRequired}
		}
		addr, ok := NormalizeEmail(in.Email)
		if !ok {
			return "", &ValidationError{"email", ReasonInvalid}
		}
		return addr, nil
	case "":
		return "", &ValidationError{"kind", ReasonRequired}
	default:
		return "", &ValidationError{"kind", ReasonInvalid}
	}
}

func (s *ChannelService) cabe(ctx context.Context, userID uuid.UUID, travar bool) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		return s.cabeNa(ctx, q, userID, travar)
	})
}

func (s *ChannelService) cabeNa(ctx context.Context, q *db.Queries, userID uuid.UUID, travar bool) error {
	if travar {
		if err := q.LockUser(ctx, userID); err != nil {
			return fmt.Errorf("travar usuário: %w", err)
		}
	}
	n, err := q.CountChannels(ctx, userID)
	if err != nil {
		return fmt.Errorf("contar canais: %w", err)
	}
	if n >= MaxChannels {
		return ErrLimitReached
	}
	return nil
}

// List devolve os canais do usuário. Uma linha que não decifra (chave
// trocada, linha adulterada) sai sem hint, em vez de derrubar a lista
// inteira: é o defeito que o Plano 2 deixou em targets.List.
func (s *ChannelService) List(ctx context.Context, userID uuid.UUID) ([]Channel, error) {
	var out []Channel
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListChannels(ctx, userID)
		if err != nil {
			return fmt.Errorf("listar canais: %w", err)
		}
		out = make([]Channel, 0, len(rows))
		for _, r := range rows {
			out = append(out, s.toChannel(r))
		}
		return nil
	})
	return out, err
}

func (s *ChannelService) toChannel(r db.ListChannelsRow) Channel {
	c := Channel{ID: r.ID, Kind: Kind(r.Kind), Enabled: r.Enabled, CreatedAt: r.CreatedAt}
	if dest, err := openConfig(s.box, r.ID, r.ConfigEnc); err == nil {
		c.Hint = hint(c.Kind, dest)
	} else {
		slog.Error("canal com destino ilegível", "channel_id", r.ID)
	}
	if r.LastFailureAt != nil {
		c.LastFailureAt = *r.LastFailureAt
		if r.LastError != nil {
			c.LastError = Reason(*r.LastError)
		}
	}
	switch {
	case r.VerifiedAt != nil && falhandoDepoisDoSucesso(r.LastFailureAt, r.LastSuccessAt):
		c.Status = StatusFailing
	case r.VerifiedAt != nil:
		c.Status = StatusVerified
	case r.VerifyExpiresAt != nil && !s.clk.Now().Before(*r.VerifyExpiresAt):
		c.Status = StatusExpired
	default:
		c.Status = StatusPending
	}
	return c
}

// falhandoDepoisDoSucesso: houve falha definitiva, e nenhuma entrega deu
// certo depois dela. Uma entrega que volta a dar certo tira o canal de
// "failing" sem ação do dono.
func falhandoDepoisDoSucesso(falha, sucesso *time.Time) bool {
	return falha != nil && (sucesso == nil || falha.After(*sucesso))
}

func (s *ChannelService) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: userID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar canal: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ResendVerification manda um link novo a um e-mail pendente (vencido ou
// não) e invalida o anterior. Envia antes de trocar o token: se o envio
// falha, o link antigo continua valendo.
func (s *ChannelService) ResendVerification(ctx context.Context, userID, id uuid.UUID) error {
	var row db.NotifChannel
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		row, err = q.GetChannel(ctx, db.GetChannelParams{UserID: userID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	if Kind(row.Kind) != KindEmail || row.VerifiedAt != nil {
		return ErrNotPending
	}
	if s.email == nil {
		return &ValidationError{"kind", ReasonUnavailable}
	}
	dest, err := openConfig(s.box, row.ID, row.ConfigEnc)
	if err != nil {
		return err
	}
	token, hash, err := novoToken()
	if err != nil {
		return err
	}
	envio, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.email.SendVerification(envio, dest, s.link(token)); err != nil {
		slog.Warn("e-mail de verificação falhou", "motivo", motivo(err))
		return ErrDeliveryFailed
	}
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.RenewVerification(ctx, db.RenewVerificationParams{
			UserID: userID, ID: id, TokenHash: hash, ExpiresAt: s.clk.Now().Add(VerifyTTL),
		})
		if err != nil {
			return fmt.Errorf("renovar token: %w", err)
		}
		if n == 0 {
			// Verificado ou apagado entre a leitura e aqui.
			return ErrNotPending
		}
		return nil
	})
}

// VerifyState é o que a página GET /verify/{token} mostra.
type VerifyState int

const (
	VerifyInvalid VerifyState = iota
	VerifyPending
	VerifyDone
)

// CheckToken só lê: é o GET, que um leitor de links do provedor de e-mail
// pode disparar sozinho, sem o dono (D2 deste plano).
func (s *ChannelService) CheckToken(ctx context.Context, token string) (VerifyState, error) {
	hash, ok := hashDoToken(token)
	if !ok {
		return VerifyInvalid, nil
	}
	var estado VerifyState
	err := storage.InVerifyTx(ctx, s.pool, hash, func(q *db.Queries) error {
		row, err := q.FindChannelByVerification(ctx, hash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			estado = VerifyInvalid
		case err != nil:
			return fmt.Errorf("buscar token: %w", err)
		case row.VerifiedAt != nil:
			estado = VerifyDone
		case row.VerifyExpiresAt == nil || !s.clk.Now().Before(*row.VerifyExpiresAt):
			estado = VerifyInvalid
		default:
			estado = VerifyPending
		}
		return nil
	})
	return estado, err
}

// ConfirmToken é o POST: verifica o canal. Confirmar de novo um canal já
// verificado dá certo e não muda nada.
func (s *ChannelService) ConfirmToken(ctx context.Context, token string) error {
	hash, ok := hashDoToken(token)
	if !ok {
		return ErrInvalidToken
	}
	return storage.InVerifyTx(ctx, s.pool, hash, func(q *db.Queries) error {
		n, err := q.ConfirmChannel(ctx, db.ConfirmChannelParams{TokenHash: hash, Now: s.clk.Now()})
		if err != nil {
			return fmt.Errorf("confirmar canal: %w", err)
		}
		if n == 0 {
			return ErrInvalidToken
		}
		return nil
	})
}

func (s *ChannelService) link(token string) string { return s.baseURL + "/verify/" + token }

// novoToken devolve o token (vai no link) e o hash dele (vai para o banco).
// O banco nunca vê o token: um dump não verifica canal nenhum.
func novoToken() (string, []byte, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("gerar token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, crypto.HashToken(token), nil
}

// hashDoToken recusa, sem ir ao banco, o que não pode ser um token nosso.
func hashDoToken(token string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != tokenBytes {
		return nil, false
	}
	return crypto.HashToken(token), true
}

// motivo reduz a falha de envio ao Reason, que é o que pode ir ao log.
func motivo(err error) Reason {
	var se *SendError
	if errors.As(err, &se) {
		return se.Reason
	}
	return ReasonUnknown
}
