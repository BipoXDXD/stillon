package httpapi_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	rotaDeLogin   = "/api/v1/auth/login"
	rotaDeRefresh = "/api/v1/auth/refresh"
)

// casosDeUserAgent são os valores que chegam ao handler pela pilha HTTP real:
// o net/http aceita bytes acima de 0x7F em cabeçalho. O último caso é o valor
// legítimo, que tem de ser gravado sem nenhuma alteração.
var casosDeUserAgent = []struct {
	nome, enviado, gravado string
}{
	{"Latin-1", "caf\xe9", "caf�"},
	{"trechos inválidos e sequência cortada no fim", "\xff\xfea\xc3", "�a�"},
	{"UTF-8 válido fica como veio", "Navegador ação 日本", "Navegador ação 日本"},
}

func TestLoginStoresARecordableUserAgent(t *testing.T) {
	t.Parallel()
	for _, caso := range casosDeUserAgent {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)

			resp := postComUserAgent(t, clientWithJar(t), a.srv.URL+rotaDeLogin, caso.enviado, credenciaisDeTeste())
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK || refreshCookie(resp) == nil {
				t.Fatalf("login devolveu %d (refresh: %v), queria 200 com sessão", resp.StatusCode, refreshCookie(resp))
			}
			if got := userAgentDaSessaoViva(t, a); got != caso.gravado {
				t.Errorf("user_agent gravado = %q, queria %q", got, caso.gravado)
			}
		})
	}
}

func TestRefreshStoresARecordableUserAgent(t *testing.T) {
	t.Parallel()
	for _, caso := range casosDeUserAgent {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			c := clientWithJar(t)
			entrada := login(t, a, c, testEmail, testPass)
			defer entrada.Body.Close()
			if entrada.StatusCode != http.StatusOK {
				t.Fatalf("login devolveu %d", entrada.StatusCode)
			}

			resp := postComUserAgent(t, c, a.srv.URL+rotaDeRefresh, caso.enviado, nil)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK || refreshCookie(resp) == nil {
				t.Fatalf("refresh devolveu %d (refresh: %v), queria 200 com sessão", resp.StatusCode, refreshCookie(resp))
			}
			if got := userAgentDaSessaoViva(t, a); got != caso.gravado {
				t.Errorf("user_agent gravado = %q, queria %q", got, caso.gravado)
			}
		})
	}
}

// O NUL não passa pela pilha HTTP real (ver o teste seguinte): aqui ele é
// entregue direto às rotas, para provar que o handler não depende disso.
func TestLoginAndRefreshStoreARecordableUserAgentWithNUL(t *testing.T) {
	t.Parallel()
	const enviado, gravado = "a\x00b", "a�b"

	t.Run("login", func(t *testing.T) {
		a := newAPI(t)

		w := servirComUserAgent(t, a, rotaDeLogin, enviado, nil)

		if w.Code != http.StatusOK {
			t.Fatalf("login devolveu %d: %s", w.Code, w.Body.String())
		}
		if got := userAgentDaSessaoViva(t, a); got != gravado {
			t.Errorf("user_agent gravado = %q, queria %q", got, gravado)
		}
	})

	t.Run("refresh", func(t *testing.T) {
		a := newAPI(t)
		entrada := login(t, a, clientWithJar(t), testEmail, testPass)
		defer entrada.Body.Close()
		cookie := refreshCookie(entrada)
		if entrada.StatusCode != http.StatusOK || cookie == nil {
			t.Fatalf("login devolveu %d (refresh: %v)", entrada.StatusCode, cookie)
		}

		w := servirComUserAgent(t, a, rotaDeRefresh, enviado, cookie)

		if w.Code != http.StatusOK {
			t.Fatalf("refresh devolveu %d: %s", w.Code, w.Body.String())
		}
		if got := userAgentDaSessaoViva(t, a); got != gravado {
			t.Errorf("user_agent gravado = %q, queria %q", got, gravado)
		}
	})
}

// O corpo é um login válido de propósito: o único motivo para a recusa é o
// cabeçalho, que o http.Client se negaria a enviar e por isso vai pelo socket.
func TestNULInUserAgentIsRefusedByTheHTTPStack(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	corpo := fmt.Sprintf(`{"email":%q,"password":%q}`, testEmail, testPass)
	requisicao := "POST " + rotaDeLogin + " HTTP/1.1\r\n" +
		"Host: exemplo.test\r\n" +
		"Content-Type: application/json\r\n" +
		"X-Requested-With: stillon\r\n" +
		"User-Agent: a\x00b\r\n" +
		"Connection: close\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", len(corpo)) + corpo

	conn, err := net.Dial("tcp", a.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
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
	if got := userAgentsDasSessoesVivas(t, a); len(got) != 0 {
		t.Errorf("sessões criadas: %q, queria nenhuma", got)
	}
}

func credenciaisDeTeste() map[string]string {
	return map[string]string{"email": testEmail, "password": testPass}
}

func postComUserAgent(t *testing.T, c *http.Client, url, userAgent string, body any) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, jsonBody(t, body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// servirComUserAgent entrega a requisição às rotas sem passar pelo parser do
// net/http. Sem cookie, o corpo é o do login; com ele, a rota é a do refresh.
func servirComUserAgent(t *testing.T, a *api, rota, userAgent string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	corpo := "null"
	if cookie == nil {
		corpo = fmt.Sprintf(`{"email":%q,"password":%q}`, testEmail, testPass)
	}
	r := httptest.NewRequest(http.MethodPost, rota, strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "stillon")
	r.Header.Set("User-Agent", userAgent)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.srv.Config.Handler.ServeHTTP(w, r)
	return w
}

func userAgentDaSessaoViva(t *testing.T, a *api) string {
	t.Helper()
	vivas := userAgentsDasSessoesVivas(t, a)
	if len(vivas) != 1 {
		t.Fatalf("sessões vivas = %q, queria exatamente uma", vivas)
	}
	return vivas[0]
}

func userAgentsDasSessoesVivas(t *testing.T, a *api) []string {
	t.Helper()
	rows, err := a.pool.Query(context.Background(),
		`SELECT user_agent FROM sessions WHERE user_id = $1 AND used_at IS NULL`, a.userID)
	if err != nil {
		t.Fatal(err)
	}
	vivas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return vivas
}
