package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// casosDeTetoDoUserAgent cobrem o teto de 256 code points na gravação. O corte
// é por caractere e vem depois da normalização: um corte por byte partiria o
// "日" (3 bytes, do byte 256 ao 258), e um corte antes da normalização contaria
// cada byte inválido como um caractere e não sobraria nenhum "b".
var casosDeTetoDoUserAgent = []struct {
	nome, enviado, gravado string
}{
	{"256 code points ficam como vieram", strings.Repeat("é", 256), strings.Repeat("é", 256)},
	{"257 code points perdem o último", strings.Repeat("é", 257), strings.Repeat("é", 256)},
	{
		"10 000 caracteres com multibyte na borda do corte",
		strings.Repeat("a", 255) + "日本" + strings.Repeat("x", 9_743),
		strings.Repeat("a", 255) + "日",
	},
	{
		"o corte conta depois da normalização, que junta bytes inválidos seguidos num U+FFFD",
		strings.Repeat("\xff", 300) + strings.Repeat("b", 300),
		"�" + strings.Repeat("b", 255),
	},
}

func TestLoginCapsTheStoredUserAgent(t *testing.T) {
	for _, caso := range casosDeTetoDoUserAgent {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)

			resp := postComUserAgent(t, clientWithJar(t), a.srv.URL+rotaDeLogin, caso.enviado, credenciaisDeTeste())
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK || refreshCookie(resp) == nil {
				t.Fatalf("login devolveu %d (refresh: %v), queria 200 com sessão", resp.StatusCode, refreshCookie(resp))
			}
			conferirUserAgentGravado(t, userAgentDaSessaoViva(t, a), caso.gravado)
		})
	}
}

func TestRefreshCapsTheStoredUserAgent(t *testing.T) {
	for _, caso := range casosDeTetoDoUserAgent {
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
			conferirUserAgentGravado(t, userAgentDaSessaoViva(t, a), caso.gravado)
		})
	}
}

// conferirUserAgentGravado resume a diferença em tamanho e fim do valor: o
// valor inteiro chega a 10 000 caracteres e afogaria a saída.
func conferirUserAgentGravado(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("user_agent gravado com %d code points (%d bytes, termina em %q); queria %d code points (termina em %q)",
			utf8.RuneCountInString(got), len(got), fim(got), utf8.RuneCountInString(want), fim(want))
	}
}

func fim(s string) string {
	r := []rune(s)
	return string(r[max(0, len(r)-3):])
}
