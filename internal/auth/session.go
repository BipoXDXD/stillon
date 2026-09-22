package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/crypto"
)

// RefreshTTL vem da spec §5.2.
const RefreshTTL = 30 * 24 * time.Hour

// refreshTokenBytes: 32 bytes de crypto/rand, ou seja 256 bits de entropia
// (spec §5.2). É o que torna busca exaustiva inviável e permite guardar
// apenas SHA-256 sem chave.
const refreshTokenBytes = 32

var (
	ErrInvalidSession = errors.New("sessão inválida")
	// ErrTokenReuse indica que um refresh já consumido reapareceu — evidência
	// de roubo. Quem chama deve tratar como incidente de segurança.
	ErrTokenReuse = errors.New("reúso de refresh token detectado")
)

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
}

// Meta descreve de onde a requisição veio. O IP é hasheado antes de tocar o
// banco (spec §4.2).
type Meta struct {
	IP        string
	UserAgent string
}

type Manager struct {
	store Store
	clk   clock.Clock
	ipKey []byte
}

// NewManager entra em panic com ipKey curta em vez de devolver erro (ruling
// R27, mesma decisão do R7 em crypto.HashIP). A validação fica aqui, na
// partida do processo, e não no primeiro Issue/Rotate: sem ela o panic de
// HashIP só estouraria dentro do handler HTTP, no primeiro login em produção.
// config.Load já garante 32 bytes para IPHashKey, então chave curta chegando
// aqui é erro de programação, não de configuração.
func NewManager(store Store, clk clock.Clock, ipKey []byte) *Manager {
	if len(ipKey) < crypto.KeySize {
		panic("auth: NewManager exige ipKey de ao menos 32 bytes")
	}
	return &Manager{store: store, clk: clk, ipKey: ipKey}
}

// Issue cria uma família nova. É o caminho do login.
func (m *Manager) Issue(ctx context.Context, userID uuid.UUID, meta Meta) (string, Session, error) {
	familyID, err := uuid.NewV7()
	if err != nil {
		return "", Session{}, fmt.Errorf("gerar família: %w", err)
	}

	var token string
	var s Session
	err = m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		token, s, err = m.insert(ctx, tx, userID, familyID, meta)
		return err
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, s, nil
}

// Rotate troca um refresh válido por um novo, dentro da mesma família.
//
// Toda a operação roda em uma transação com SELECT ... FOR UPDATE. Sem o
// lock, duas rotações simultâneas com o mesmo token poderiam ambas ler
// used_at = NULL e ambas seguir adiante, o que anularia a detecção de reúso.
func (m *Manager) Rotate(ctx context.Context, token string, meta Meta) (string, Session, error) {
	hash := crypto.HashToken(token)
	now := m.clk.Now()

	var newToken string
	var s Session
	// reuso é sinalizado por fora da transação de propósito: a revogação da
	// família e o registro de auditoria precisam ser COMMITADOS. Se o callback
	// devolvesse ErrTokenReuse, o InTx daria rollback e apagaria exatamente a
	// defesa que acabou de ser acionada — a detecção viraria uma mensagem de
	// erro sem efeito nenhum no banco.
	var reuso bool

	err := m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		cur, err := tx.LockSessionByHash(ctx, hash)
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidSession
		}
		if err != nil {
			return err
		}

		if cur.UsedAt != nil {
			// Um token consumido reapareceu. A única explicação é cópia.
			// Não há como saber se quem chegou agora é o ladrão ou o dono,
			// então a família inteira cai e ambos fazem login de novo.
			if err := tx.RevokeFamily(ctx, cur.FamilyID, now); err != nil {
				return err
			}
			if err := tx.InsertAudit(ctx, cur.UserID, "session.reuse_detected",
				crypto.HashIP(m.ipKey, meta.IP), now); err != nil {
				return err
			}
			reuso = true
			return nil
		}
		if cur.RevokedAt != nil || now.After(cur.ExpiresAt) {
			return ErrInvalidSession
		}

		if err := tx.MarkSessionUsed(ctx, cur.ID, now); err != nil {
			return err
		}

		newToken, s, err = m.insert(ctx, tx, cur.UserID, cur.FamilyID, meta)
		return err
	})
	// O erro da transação vem antes do reúso de propósito: se o commit falhar,
	// a revogação da família e a auditoria não persistiram, e devolver
	// ErrTokenReuse afirmaria uma defesa que não aconteceu.
	if err != nil {
		return "", Session{}, err
	}
	if reuso {
		return "", Session{}, ErrTokenReuse
	}
	return newToken, s, nil
}

// Revoke encerra uma sessão específica. É o caminho do logout.
//
// Revogar um token que não existe, ou já revogado, devolve nil (ruling R28):
// logout é idempotente. Um cliente que reenvia o logout, ou que manda um
// cookie já morto, não deve receber erro — e o retorno não diz nada sobre a
// existência do token, então quem chama não pode inferir nada dele.
func (m *Manager) Revoke(ctx context.Context, token string) error {
	hash := crypto.HashToken(token)
	now := m.clk.Now()
	return m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		return tx.RevokeByHash(ctx, hash, now)
	})
}

func (m *Manager) insert(ctx context.Context, tx Tx, userID, familyID uuid.UUID, meta Meta) (string, Session, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, fmt.Errorf("gerar refresh token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	id, err := uuid.NewV7()
	if err != nil {
		return "", Session{}, fmt.Errorf("gerar id de sessão: %w", err)
	}

	row := SessionRow{
		ID:        id,
		UserID:    userID,
		FamilyID:  familyID,
		TokenHash: crypto.HashToken(token),
		ExpiresAt: m.clk.Now().Add(RefreshTTL),
		IPHash:    crypto.HashIP(m.ipKey, meta.IP),
		UserAgent: meta.UserAgent,
	}
	if err := tx.InsertSession(ctx, row); err != nil {
		return "", Session{}, err
	}

	return token, Session{
		ID:        row.ID,
		UserID:    row.UserID,
		FamilyID:  row.FamilyID,
		ExpiresAt: row.ExpiresAt,
	}, nil
}
