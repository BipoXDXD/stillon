package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// enviarCru manda o corpo como está, sem passar por json.Marshal: os casos
// abaixo são justamente corpos que o Marshal não produziria.
func enviarCru(t *testing.T, a *api, metodo, caminho, token, corpo string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(metodo, a.srv.URL+caminho, strings.NewReader(corpo))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lido, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(lido)
}

func codigoDoEnvelope(t *testing.T, corpo string) string {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(corpo), &env); err != nil {
		t.Fatalf("corpo fora do envelope: %v: %s", err, corpo)
	}
	return env.Error.Code
}

// As rotas de autenticação também recusam campo desconhecido: o DTO é
// estrito em toda a API, e não só nas rotas de recurso.
func TestAuthRoutesRejectUnknownFields(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	for _, r := range []struct {
		caminho, token, corpo string
	}{
		{"/api/v1/auth/login", "", `{"email":"` + testEmail + `","password":"` + testPass + `","role":"admin"}`},
		{"/api/v1/auth/totp/verify", "", `{"challenge":"x","code":"123456","user_id":"x"}`},
		{"/api/v1/auth/password", token, `{"current_password":"a","new_password":"b","is_admin":true}`},
		{"/api/v1/auth/totp/enroll", token, `{"password":"a","secret":"AAAA"}`},
		{"/api/v1/auth/totp/confirm", token, `{"code":"123456","enabled":true}`},
		{"/api/v1/auth/totp/disable", token, `{"password":"a","code":"1","force":true}`},
		{"/api/v1/auth/totp/rotate", token, `{"password":"a","code":"1","secret":"AAAA"}`},
		{"/api/v1/auth/totp/rotate/confirm", token, `{"code":"1","rotation":"x","extra":1}`},
	} {
		status, corpo := enviarCru(t, a, http.MethodPost, r.caminho, r.token, r.corpo)
		if status != http.StatusBadRequest || codigoDoEnvelope(t, corpo) != "validation_failed" {
			t.Errorf("%s com campo extra deu %d %s, queria 400 validation_failed", r.caminho, status, corpo)
		}
	}
}

// Depois do objeto JSON não pode vir mais nada: um segundo objeto que o
// decoder ignorasse seria lido de outro jeito por um proxy ou WAF na frente.
func TestBodyWithTrailingDataIsRejected(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	valido := `{"name":"api","url":"https://exemplo.com/"}`
	for _, corpo := range []string{
		valido + `{"name":"outro"}`,
		valido + ` lixo`,
		valido + `[]`,
	} {
		status, resposta := enviarCru(t, a, http.MethodPost, "/api/v1/targets", tok, corpo)
		if status != http.StatusBadRequest || codigoDoEnvelope(t, resposta) != "validation_failed" {
			t.Errorf("corpo %q deu %d %s, queria 400 validation_failed", corpo, status, resposta)
		}
	}
	status, lista := enviarCru(t, a, http.MethodGet, "/api/v1/targets", tok, "")
	if status != http.StatusOK || !strings.Contains(lista, `"items":[]`) {
		t.Errorf("a lista depois das recusas deu %d %s, queria vazia", status, lista)
	}

	// Espaço em branco depois do objeto continua valendo (o \n do curl).
	if status, resposta := enviarCru(t, a, http.MethodPost, "/api/v1/targets", tok, valido+"\n"); status != http.StatusCreated {
		t.Errorf("corpo com \\n no fim deu %d %s, queria 201", status, resposta)
	}
}

// E-mail com NUL ou caractere de controle não chega ao banco (o Postgres
// recusa NUL em text, e isso virava 500): a resposta é a de conta
// inexistente, com o mesmo código.
func TestLoginWithControlCharInEmailLooksLikeUnknownAccount(t *testing.T) {
	a := newAPI(t)
	for _, email := range []string{`pessoa\u0000@exemplo.com`, `pessoa\u0007@exemplo.com`, strings.Repeat("a", 300) + "@exemplo.com"} {
		status, corpo := enviarCru(t, a, http.MethodPost, "/api/v1/auth/login", "",
			`{"email":"`+email+`","password":"qualquer-coisa"}`)
		if status != http.StatusUnauthorized || codigoDoEnvelope(t, corpo) != "invalid_credentials" {
			t.Errorf("e-mail %.40q deu %d %s, queria 401 invalid_credentials", email, status, corpo)
		}
	}
}
