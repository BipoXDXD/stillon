package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
)

const (
	hashDeSenhaDoDuble = "hash-de-senha-do-duble"
	refreshDoDuble     = "refresh-do-duble"
)

// sessoesDoDuble faz o papel do banco na gravação da sessão. Ele recusa o que a
// coluna text do Postgres em UTF-8 recusa: byte fora de UTF-8 e NUL.
type sessoesDoDuble struct {
	auth.Tx

	userID   uuid.UUID
	familyID uuid.UUID
	expira   time.Time

	mu       sync.Mutex
	gravados []string
}

func (s *sessoesDoDuble) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return fn(ctx, s)
}

func (s *sessoesDoDuble) SessionUserByHash(context.Context, []byte) (uuid.UUID, error) {
	return s.userID, nil
}

func (s *sessoesDoDuble) LockUserSessions(context.Context, uuid.UUID) error { return nil }

func (s *sessoesDoDuble) LockSessionByHash(context.Context, []byte) (auth.SessionRow, error) {
	return auth.SessionRow{
		ID: uuid.New(), UserID: s.userID, FamilyID: s.familyID,
		TokenHash: crypto.HashToken(refreshDoDuble), ExpiresAt: s.expira,
	}, nil
}

func (s *sessoesDoDuble) UserPasswordHash(context.Context, uuid.UUID) (string, error) {
	return hashDeSenhaDoDuble, nil
}

func (s *sessoesDoDuble) MarkSessionUsed(context.Context, uuid.UUID, time.Time) error { return nil }

func (s *sessoesDoDuble) InsertSession(_ context.Context, r auth.SessionRow) error {
	if !utf8.ValidString(r.UserAgent) || strings.ContainsRune(r.UserAgent, 0) {
		return errors.New("invalid byte sequence for encoding UTF8")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gravados = append(s.gravados, r.UserAgent)
	return nil
}

func (s *sessoesDoDuble) userAgentsGravados() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.gravados)
}

// novoCenarioDeSessao devolve os dois caminhos que gravam sessão. O do login é
// h.issue, o trecho final de Login: o começo dele precisa do banco para o TOTP.
func novoCenarioDeSessao(t *testing.T) (*sessoesDoDuble, http.HandlerFunc, http.HandlerFunc) {
	t.Helper()
	agora := time.Now().UTC()
	familia, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	banco := &sessoesDoDuble{userID: uuid.New(), familyID: familia, expira: agora.Add(time.Hour)}
	clk := clock.NewFake(agora)
	h := &AuthHandlers{
		sessions:     auth.NewManager(banco, clk, []byte("chave-de-teste-com-32-bytes-ok!!")),
		clk:          clk,
		jwtSecret:    []byte("segredo-de-teste-com-32-bytes!!!"),
		dispositivos: auth.NewDevices([]byte("chave-mestra-de-teste-32-bytes!!"), clk),
	}
	emitir := func(w http.ResponseWriter, r *http.Request) {
		h.issue(w, r, banco.userID, auth.PasswordStamp(hashDeSenhaDoDuble), nil,
			auth.NewDeviceStamp(hashDeSenhaDoDuble, nil))
	}
	return banco, emitir, h.Refresh
}

func requisicaoComUserAgent(userAgent string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("User-Agent", userAgent)
	r.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refreshDoDuble})
	return r
}

func TestSessionStoresARecordableUserAgent(t *testing.T) {
	casos := []struct {
		nome, enviado, gravado string
	}{
		{"Latin-1", "caf\xe9", "caf�"},
		{"trechos inválidos e sequência cortada no fim", "\xff\xfea\xc3", "�a�"},
		{"NUL", "a\x00b", "a�b"},
		{"NUL ao lado de byte inválido", "\x00\xe9", "��"},
		{"vazio", "", ""},
		{"UTF-8 válido fica como veio", "Navegador ação 日本", "Navegador ação 日本"},
	}
	for _, caso := range casos {
		t.Run("login/"+caso.nome, func(t *testing.T) {
			banco, emitir, _ := novoCenarioDeSessao(t)
			conferirGravacao(t, banco, emitir, caso.enviado, caso.gravado)
		})
		t.Run("refresh/"+caso.nome, func(t *testing.T) {
			banco, _, renovar := novoCenarioDeSessao(t)
			conferirGravacao(t, banco, renovar, caso.enviado, caso.gravado)
		})
	}
}

func conferirGravacao(t *testing.T, banco *sessoesDoDuble, rota http.HandlerFunc, enviado, gravado string) {
	t.Helper()
	w := httptest.NewRecorder()
	rota(w, requisicaoComUserAgent(enviado))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, queria 200: %s", w.Code, w.Body.String())
	}
	if got := banco.userAgentsGravados(); !slices.Equal(got, []string{gravado}) {
		t.Errorf("user_agent gravado = %q, queria [%q]", got, gravado)
	}
}

// Prova a premissa pela pilha HTTP real: o net/http entrega ao handler o byte
// acima de 0x7F.
func TestInvalidUTF8UserAgentCrossesTheHTTPStackAndIsRecorded(t *testing.T) {
	banco, _, renovar := novoCenarioDeSessao(t)
	srv := httptest.NewServer(renovar)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "caf\xe9")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refreshDoDuble})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, queria 200", resp.StatusCode)
	}
	if got := banco.userAgentsGravados(); !slices.Equal(got, []string{"caf�"}) {
		t.Errorf("user_agent gravado = %q, queria [%q]", got, "caf�")
	}
}

// O NUL vai pelo socket porque o http.Client se nega a enviá-lo; quem recusa é
// o net/http, antes do handler.
func TestNULInUserAgentIsRefusedByTheHTTPStack(t *testing.T) {
	banco, _, renovar := novoCenarioDeSessao(t)
	srv := httptest.NewServer(renovar)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	requisicao := fmt.Sprintf("POST / HTTP/1.1\r\nHost: exemplo.test\r\nUser-Agent: a\x00b\r\nCookie: %s=%s\r\nConnection: close\r\n\r\n",
		refreshCookieName, refreshDoDuble)
	if _, err := conn.Write([]byte(requisicao)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, queria 400", resp.StatusCode)
	}
	if got := banco.userAgentsGravados(); len(got) != 0 {
		t.Errorf("sessões gravadas: %q, queria nenhuma", got)
	}
}
