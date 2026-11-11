package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
)

const (
	refreshDaRotacao = "refresh-da-rotacao"
	detalheDaFalha   = "CANARY-detalhe-da-falha-interna"
)

// etapaQueFalha é onde o banco cai durante a rotação.
type etapaQueFalha string

const (
	semFalha              etapaQueFalha = ""
	falhaAoAbrirTransacao etapaQueFalha = "ao abrir a transação"
	falhaAoGravarSucessor etapaQueFalha = "ao gravar o sucessor"
	falhaNoCommit         etapaQueFalha = "no commit"
)

var falhasDaRotacao = []etapaQueFalha{falhaAoAbrirTransacao, falhaAoGravarSucessor, falhaNoCommit}

// bancoDeRotacao faz o papel do Postgres numa rotação: o que a transação
// escreve só vale depois do commit. É esse rollback que permite afirmar que o
// mesmo cookie ainda renova depois da falha.
type bancoDeRotacao struct {
	auth.Tx

	falha     etapaQueFalha
	linhas    map[string]auth.SessionRow
	pendentes []func()
}

func (b *bancoDeRotacao) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	if b.falha == falhaAoAbrirTransacao {
		return errors.New(detalheDaFalha)
	}
	b.pendentes = nil
	if err := fn(ctx, b); err != nil {
		return err
	}
	if b.falha == falhaNoCommit {
		return errors.New(detalheDaFalha)
	}
	for _, aplicar := range b.pendentes {
		aplicar()
	}
	return nil
}

func (b *bancoDeRotacao) LockSessionByHash(_ context.Context, hash []byte) (auth.SessionRow, error) {
	linha, ok := b.linhas[string(hash)]
	if !ok {
		return auth.SessionRow{}, auth.ErrNotFound
	}
	return linha, nil
}

func (b *bancoDeRotacao) SessionUserByHash(ctx context.Context, hash []byte) (uuid.UUID, error) {
	linha, err := b.LockSessionByHash(ctx, hash)
	return linha.UserID, err
}

func (b *bancoDeRotacao) LockUserSessions(context.Context, uuid.UUID) error { return nil }

func (b *bancoDeRotacao) MarkSessionUsed(_ context.Context, id uuid.UUID, at time.Time) error {
	b.pendentes = append(b.pendentes, func() {
		for hash, linha := range b.linhas {
			if linha.ID == id {
				linha.UsedAt = &at
				b.linhas[hash] = linha
			}
		}
	})
	return nil
}

func (b *bancoDeRotacao) InsertSession(_ context.Context, r auth.SessionRow) error {
	if b.falha == falhaAoGravarSucessor {
		return errors.New(detalheDaFalha)
	}
	b.pendentes = append(b.pendentes, func() { b.linhas[string(r.TokenHash)] = r })
	return nil
}

func (b *bancoDeRotacao) RevokeFamily(_ context.Context, familyID uuid.UUID, at time.Time) error {
	b.pendentes = append(b.pendentes, func() {
		for hash, linha := range b.linhas {
			if linha.FamilyID == familyID {
				linha.RevokedAt = &at
				b.linhas[hash] = linha
			}
		}
	})
	return nil
}

// novaRotacao monta uma sessão válida. A família é um UUIDv7 literal, com o
// nascimento uma hora antes do relógio do teste: a idade dela sai do próprio id.
func novaRotacao(t *testing.T, falha etapaQueFalha) (*bancoDeRotacao, *AuthHandlers) {
	t.Helper()
	const nascimentoDaFamiliaMs = 0x019a00000000
	agora := time.UnixMilli(nascimentoDaFamiliaMs).Add(time.Hour).UTC()
	familia := uuid.MustParse("019a0000-0000-7000-8000-000000000001")
	hash := crypto.HashToken(refreshDaRotacao)
	banco := &bancoDeRotacao{falha: falha, linhas: map[string]auth.SessionRow{
		string(hash): {
			ID: uuid.New(), UserID: uuid.New(), FamilyID: familia, TokenHash: hash, ExpiresAt: agora.Add(time.Hour),
		},
	}}
	clk := clock.NewFake(agora)
	return banco, &AuthHandlers{
		sessions:  auth.NewManager(banco, clk, []byte("chave-de-teste-com-32-bytes-ok!!")),
		clk:       clk,
		jwtSecret: []byte("segredo-de-teste-com-32-bytes!!!"),
	}
}

func renovarSessao(h *AuthHandlers) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refreshDaRotacao})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	return w
}

func codigoDoEnvelope(t *testing.T, corpo []byte) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(corpo, &envelope); err != nil {
		t.Fatalf("corpo fora do envelope: %v: %s", err, corpo)
	}
	return envelope.Error.Code
}

// Um instante de banco fora não pode tirar o refresh do navegador: o dono
// sairia de todas as abas por uma falha que nada diz sobre a sessão dele.
func TestRefreshInternalFailureAnswers500AndLeavesTheCookieAlone(t *testing.T) {
	for _, falha := range falhasDaRotacao {
		t.Run(string(falha), func(t *testing.T) {
			_, h := novaRotacao(t, falha)

			w := renovarSessao(h)

			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, queria 500: %s", w.Code, w.Body.String())
			}
			if codigo := codigoDoEnvelope(t, w.Body.Bytes()); codigo != "internal_error" {
				t.Errorf("code = %q, queria internal_error", codigo)
			}
			if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 0 {
				t.Errorf("a falha interna mexeu nos cookies: %v", cookies)
			}
			if strings.Contains(w.Body.String(), detalheDaFalha) {
				t.Errorf("o corpo trouxe o erro real: %s", w.Body.String())
			}
		})
	}
}

// A falha também não pode custar a família: com o banco de volta, o mesmo
// cookie renova, e o painel se recupera sozinho.
func TestRefreshInternalFailureLeavesTheSameCookieRenewable(t *testing.T) {
	for _, falha := range falhasDaRotacao {
		t.Run(string(falha), func(t *testing.T) {
			banco, h := novaRotacao(t, falha)
			renovarSessao(h)

			banco.falha = semFalha
			w := renovarSessao(h)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d com o banco de volta, queria 200: %s", w.Code, w.Body.String())
			}
			var novo string
			for _, ck := range w.Result().Cookies() {
				if ck.Name == refreshCookieName {
					novo = ck.Value
				}
			}
			if novo == "" || novo == refreshDaRotacao {
				t.Errorf("cookie de refresh = %q, queria um token novo", novo)
			}
		})
	}
}
