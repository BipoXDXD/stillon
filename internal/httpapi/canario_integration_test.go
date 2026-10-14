package httpapi_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// logCapturado guarda tudo o que o slog padrão escreve durante o teste. O
// servidor loga de outras goroutines, daí o mutex.
type logCapturado struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapturado) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapturado) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// capturarLog troca o slog padrão por um que escreve em memória, no nível
// mais baixo, e devolve o original no fim do teste. Os testes de httpapi não
// rodam em paralelo, então a troca global é segura aqui.
func capturarLog(t *testing.T) *logCapturado {
	t.Helper()
	l := &logCapturado{}
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	return l
}

// Um canário semeado em todo campo sensível (senha, Authorization, cookies,
// token de verificação, valor de cabeçalho do alvo) nunca aparece na
// resposta, nos cabeçalhos dela nem no log.
func TestCanaryInSensitiveFieldsNeverLeaks(t *testing.T) {
	canario := "CANARY-" + uuid.NewString()
	log := capturarLog(t)
	a := newAPI(t)
	_, token := entrar(t, a)

	tentativas := []struct {
		nome    string
		status  int // 0: qualquer 2xx/4xx
		metodo  string
		caminho string
		corpo   string
		ajustar func(*http.Request)
	}{
		{nome: "senha errada", metodo: http.MethodPost, caminho: "/api/v1/auth/login",
			corpo: `{"email":"` + testEmail + `","password":"` + canario + `"}`},
		{nome: "senha de conta inexistente", metodo: http.MethodPost, caminho: "/api/v1/auth/login",
			corpo: `{"email":"ninguem@exemplo.com","password":"` + canario + `"}`},
		{nome: "senha com tipo errado", metodo: http.MethodPost, caminho: "/api/v1/auth/login",
			corpo: `{"email":1,"password":"` + canario + `"}`},
		{nome: "bearer inválido", metodo: http.MethodGet, caminho: "/api/v1/me",
			ajustar: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+canario) }},
		{nome: "cookie de refresh inválido", metodo: http.MethodPost, caminho: "/api/v1/auth/refresh",
			ajustar: func(r *http.Request) {
				r.Header.Set("Cookie", "stillon_refresh="+canario+"; stillon_dispositivo="+canario)
			}},
		{nome: "logout com cookie inválido", metodo: http.MethodPost, caminho: "/api/v1/auth/logout",
			ajustar: func(r *http.Request) { r.Header.Set("Cookie", "stillon_refresh="+canario) }},
		{nome: "troca de senha com a atual errada", metodo: http.MethodPost, caminho: "/api/v1/auth/password",
			corpo:   `{"current_password":"` + canario + `","new_password":"` + canario + `"}`,
			ajustar: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }},
		{nome: "desafio TOTP inválido", metodo: http.MethodPost, caminho: "/api/v1/auth/totp/verify",
			corpo: `{"challenge":"` + canario + `","code":"123456"}`},
		{nome: "token de verificação inválido (GET)", metodo: http.MethodGet, caminho: "/verify/" + canario},
		{nome: "token de verificação inválido (POST)", metodo: http.MethodPost, caminho: "/verify/" + canario},
		{nome: "valor de cabeçalho do alvo", metodo: http.MethodPost, caminho: "/api/v1/targets", status: http.StatusCreated,
			corpo:   `{"name":"api","url":"https://exemplo.com/","headers":{"Authorization":"Bearer ` + canario + `"}}`,
			ajustar: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }},
		{nome: "lista de alvos com o cabeçalho gravado", metodo: http.MethodGet, caminho: "/api/v1/targets",
			ajustar: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }},
	}
	for _, tt := range tentativas {
		req, err := http.NewRequest(tt.metodo, a.srv.URL+tt.caminho, strings.NewReader(tt.corpo))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "stillon")
		if tt.ajustar != nil {
			tt.ajustar(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		corpo, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if tt.status != 0 && resp.StatusCode != tt.status {
			t.Fatalf("%s: deu %d %s, queria %d", tt.nome, resp.StatusCode, corpo, tt.status)
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			t.Errorf("%s: deu %d, queria uma resposta 2xx/4xx", tt.nome, resp.StatusCode)
		}
		if strings.Contains(string(corpo), canario) {
			t.Errorf("%s: o canário voltou no corpo: %s", tt.nome, corpo)
		}
		for nome, valores := range resp.Header {
			if strings.Contains(strings.Join(valores, " "), canario) {
				t.Errorf("%s: o canário voltou no cabeçalho %s", tt.nome, nome)
			}
		}
	}
	if strings.Contains(log.String(), canario) {
		t.Errorf("o canário apareceu no log:\n%s", log.String())
	}
}
