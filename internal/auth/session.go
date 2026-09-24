package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
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
	// ErrStalePassword: a senha verificada para abrir a sessão já não é a do
	// usuário (um set-password ou uma troca de senha comitou no meio do
	// login). Quem chama responde como a senha errada: invalid_credentials.
	ErrStalePassword = errors.New("a senha mudou depois de verificada")
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

// PasswordStamp é o carimbo de um hash de senha: o SHA-256 dele, em base64url.
// É o que Issue recebe como prova de que a senha foi verificada, e o que o
// desafio do segundo fator carrega entre a senha e o código. O hash em si não
// sai do servidor; o carimbo só serve para comparar com o hash atual.
func PasswordStamp(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Issue cria uma família nova. É o caminho do login, e só depois de uma senha
// verificada: stamp é o PasswordStamp do hash que conferiu com ela.
//
// Sob a trava do usuário (D1 do Plano 5), Issue confere que o hash atual ainda
// é o verificado, e recusa com ErrStalePassword se não for. As duas metades
// são necessárias. Sem a reconferência, um login que verificou a senha ANTIGA
// e chegou aqui depois de um set-password (ou de uma troca de senha) abria uma
// sessão que sobrevivia à redefinição. Sem a trava, a reconferência passaria,
// o set-password comitaria enquanto a sessão nova ainda não existia para ele,
// e ela sobreviveria do mesmo jeito. Com as duas, ou a redefinição comita
// antes (e a reconferência recusa), ou a sessão comita antes (e a redefinição,
// que pega a mesma trava, a revoga). TestLoginRacingAPasswordReset
// (internal/httpapi) prova as duas ordens.
//
// A trava do usuário vem antes de qualquer outra coisa, como em toda
// transação que muda sessions; Issue não trava linha nenhuma de sessions.
func (m *Manager) Issue(ctx context.Context, userID uuid.UUID, stamp string, meta Meta) (string, Session, error) {
	if stamp == "" {
		return "", Session{}, errors.New("emitir sessão: sem o carimbo da senha verificada")
	}
	familyID, err := uuid.NewV7()
	if err != nil {
		return "", Session{}, fmt.Errorf("gerar família: %w", err)
	}

	var token string
	var s Session
	err = m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		if err := tx.LockUserSessions(ctx, userID); err != nil {
			return err
		}
		hash, err := tx.UserPasswordHash(ctx, userID)
		if errors.Is(err, ErrNotFound) {
			return ErrStalePassword
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(PasswordStamp(hash)), []byte(stamp)) != 1 {
			return ErrStalePassword
		}
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
		cur, err := travarSessao(ctx, tx, hash)
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

// Revoke encerra a FAMÍLIA da sessão apresentada. É o caminho do logout.
//
// Matar só a linha do token entregue não serve para o que o logout existe.
// Quando o refresh foi copiado e o ladrão já rotacionou, a cadeia viva é a
// dele; revogar apenas o cookie da vítima deixava o ladrão dentro. A detecção
// de reúso não cobre esse caso: ela exige uma ROTAÇÃO, e a vítima que faz
// logout está fazendo o oposto. Como não existe "encerrar todas as sessões"
// nem troca de senha que revogue, o logout era a única alavanca do usuário
// contra uma sessão suspeita — e não mexia nela.
//
// Cada login abre família nova, então isto não desloga os outros
// dispositivos: derruba a cadeia deste login, que é o que o usuário quer
// dizer com "sair".
//
// Revogar um token que não existe, ou já revogado, devolve nil (ruling R28):
// logout é idempotente. Um cliente que reenvia o logout, ou que manda um
// cookie já morto, não deve receber erro — e o retorno não diz nada sobre a
// existência do token, então quem chama não pode inferir nada dele.
func (m *Manager) Revoke(ctx context.Context, token string) error {
	hash := crypto.HashToken(token)
	now := m.clk.Now()
	return m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		// A trava do usuário, e não a da linha, é o que fecha a corrida com
		// uma rotação simultânea: o cookie apresentado pode ser um refresh
		// antigo, e a linha viva que a rotação trava é outra (D1 do Plano 5).
		cur, err := travarSessao(ctx, tx, hash)
		if errors.Is(err, ErrNotFound) {
			return nil // R28
		}
		if err != nil {
			return err
		}
		return tx.RevokeFamily(ctx, cur.FamilyID, now)
	})
}

// travarSessao pega a trava do usuário ANTES da trava da linha (D1 do Plano
// 5), e toda transação que muda sessões segue essa ordem: é o que impede o
// ciclo entre duas delas.
//
// A trava da linha sozinha não basta. Em READ COMMITTED, o UPDATE que revoga
// uma família espera a linha viva que uma rotação travou; a rotação insere o
// sucessor e comita; o UPDATE revoga a linha que esperou e não enxerga o
// sucessor, que entrou depois do snapshot dele. A família "revogada" seguia
// viva pelo sucessor (TestRevokingDuringRotationLeavesNothingAlive). Com a
// trava do usuário, a rotação termina antes de a revogação começar, e o
// snapshot da revogação já inclui o sucessor.
//
// A primeira leitura é sem trava e serve só para descobrir o usuário; a linha
// é relida, travada, logo depois, e é essa segunda leitura que vale.
func travarSessao(ctx context.Context, tx Tx, hash []byte) (SessionRow, error) {
	userID, err := tx.SessionUserByHash(ctx, hash)
	if err != nil {
		return SessionRow{}, err
	}
	if err := tx.LockUserSessions(ctx, userID); err != nil {
		return SessionRow{}, err
	}
	return tx.LockSessionByHash(ctx, hash)
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
