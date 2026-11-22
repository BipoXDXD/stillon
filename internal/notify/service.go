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

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

const (
	// MaxChannels é o limite de canais do usuário (ver cabe e Create para
	// onde ele é contado; o teto por titular vem no PR 2b). Canal é destino
	// de envio: sem teto, cada canal novo seria mais um alvo de mensagem.
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

// Motivos de ValidationError, no vocabulário fechado da API.
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

// Channel é o que sai do domínio. Não tem campo para o destino: só o hint.
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
	// existe para que o dono saiba.
	StatusFailing ChannelStatus = "failing"
)

type ChannelInput struct {
	Kind  Kind
	URL   string // Discord
	Email string // e-mail
}

// ChannelService é o cadastro de canais (spec §8, §5.8). Todo método do dono
// recebe o escopo de projeto logo depois de ctx e roda sob storage.InProjectTx,
// que confere a pertença e liga a RLS para aquele projeto: as queries filtram
// por project_id E a política filtra de novo. A permissão vale pelo papel lido
// NA transação (o escopo que InProjectTx entrega), nunca pelo que quem chamou
// trouxe. A cota (trava e contagem) continua por user_id, e as duas
// conferências rodam sob InProjectTx: a pré-conferência (cabe), antes do
// envio, e a definitiva, com a trava, na gravação. Com um projeto por usuário
// (a criação de projetos vem no PR 2b.3), as duas contam o mesmo; a Task 23
// faz as duas contarem os canais do titular em todos os projetos dele. A
// verificação por link
// (CheckToken, ConfirmToken) não sabe nada de projeto.
type ChannelService struct {
	pool    *pgxpool.Pool
	box     *crypto.Box
	clk     clock.Clock
	discord DiscordTester
	// email é nil quando o operador não configurou o Resend: cadastrar
	// e-mail responde erro de validação no campo kind.
	email   VerificationMailer
	baseURL string
	// depoisDeContar é um gancho de teste, chamado entre a contagem travada e
	// o INSERT: é onde duas criações simultâneas se encontrariam se a trava
	// não existisse (o mesmo desenho do targets.Service).
	depoisDeContar func()
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

// Create (PermManageChannels) verifica o destino ANTES de gravar: a mensagem
// de teste do Discord ou o e-mail com o link. Se o envio falha, nada é
// gravado, e o cliente recebe ErrDeliveryFailed. O canal nasce no projeto do
// escopo, com user_id = quem criou.
//
// A permissão e o limite são conferidos antes do envio, para não mandar
// mensagem a um canal que não será gravado, e de novo na gravação: a
// permissão pelo papel daquela transação, o limite com o usuário travado,
// para que duas criações simultâneas não passem as duas.
func (s *ChannelService) Create(ctx context.Context, scope tenancy.ProjectScope, in ChannelInput) (Channel, error) {
	dest, err := s.validar(in)
	if err != nil {
		return Channel{}, err
	}
	if err := s.exigir(ctx, scope, tenancy.PermManageChannels); err != nil {
		return Channel{}, err
	}
	if err := s.cabe(ctx, scope); err != nil {
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
	p := db.InsertChannelParams{ID: id, Kind: string(in.Kind), ConfigEnc: sealed, CreatedAt: now}
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

	err = storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageChannels); err != nil {
			return err
		}
		if err := s.cabeNa(ctx, q, atual.UserID, true); err != nil {
			return err
		}
		p.UserID, p.ProjectID = atual.UserID, atual.ProjectID
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

// exigir confere, numa transação que não grava nada, a pertença e a
// permissão do papel lido no banco. Serve aos caminhos que mandam mensagem
// para fora antes de gravar: a recusa tem de vir antes do envio.
func (s *ChannelService) exigir(ctx context.Context, scope tenancy.ProjectScope, perm tenancy.Permission) error {
	return storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(_ *db.Queries, atual tenancy.ProjectScope) error {
		return atual.Require(perm)
	})
}

// cabe é a pré-conferência do limite, antes do envio, sem trava. Roda sob
// InProjectTx, como a conferência definitiva (cabeNa com a trava, em Create):
// InUserTx num recurso de projeto devolveria vazio em silêncio quando as
// permissivas forem por projeto. Com um projeto por usuário as duas contam o
// mesmo; a Task 23 faz as duas contarem o titular.
func (s *ChannelService) cabe(ctx context.Context, scope tenancy.ProjectScope) error {
	return storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		return s.cabeNa(ctx, q, atual.UserID, false)
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
	if travar && s.depoisDeContar != nil {
		s.depoisDeContar()
	}
	if n >= MaxChannels {
		return ErrLimitReached
	}
	return nil
}

// List (PermRead) devolve os canais do projeto. Uma linha que não decifra
// (chave trocada, linha adulterada) sai sem hint, em vez de derrubar a lista
// inteira.
func (s *ChannelService) List(ctx context.Context, scope tenancy.ProjectScope) ([]Channel, error) {
	var out []Channel
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermRead); err != nil {
			return err
		}
		rows, err := q.ListChannels(ctx, atual.ProjectID)
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

// Delete (PermManageChannels) apaga o canal do projeto.
func (s *ChannelService) Delete(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) error {
	return storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageChannels); err != nil {
			return err
		}
		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{ProjectID: atual.ProjectID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar canal: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetEnabled (PermManageChannels) pausa (false) ou retoma (true) o canal e o
// devolve no formato da listagem. Canal de outro projeto ou inexistente é
// ErrNotFound: a RLS e o project_id na WHERE o escondem. Mudar para o valor que
// já está não é erro.
//
// Pausado, o canal não recebe nada: o despacho cancela o aviso na hora do
// envio (semEnvio), e retomar não reenvia o que passou. Guardar os avisos
// exigiria outro estado na fila e renderia uma enxurrada de alertas velhos ao
// retomar.
func (s *ChannelService) SetEnabled(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID, enabled bool) (Channel, error) {
	var out Channel
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageChannels); err != nil {
			return err
		}
		n, err := q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{ProjectID: atual.ProjectID, ID: id, Enabled: enabled})
		if err != nil {
			return fmt.Errorf("pausar canal: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		r, err := q.GetChannelView(ctx, db.GetChannelViewParams{ProjectID: atual.ProjectID, ID: id})
		if err != nil {
			return fmt.Errorf("ler canal: %w", err)
		}
		out = s.toChannel(db.ListChannelsRow(r))
		return nil
	})
	return out, err
}

// ResendVerification (PermManageChannels) manda um link novo a um e-mail
// pendente (vencido ou não) e invalida o anterior. Envia antes de trocar o
// token: se o envio falha, o link antigo continua valendo. A permissão é
// conferida na leitura, antes do envio, e de novo na troca do token.
func (s *ChannelService) ResendVerification(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) error {
	var row db.NotifChannel
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageChannels); err != nil {
			return err
		}
		var err error
		row, err = q.GetChannel(ctx, db.GetChannelParams{ProjectID: atual.ProjectID, ID: id})
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
	return storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageChannels); err != nil {
			return err
		}
		n, err := q.RenewVerification(ctx, db.RenewVerificationParams{
			ProjectID: atual.ProjectID, ID: id, TokenHash: hash, ExpiresAt: s.clk.Now().Add(VerifyTTL),
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
// pode disparar sozinho, sem o dono.
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
