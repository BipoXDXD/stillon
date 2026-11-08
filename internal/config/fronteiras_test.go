package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Cada campo sensível sai como o marcador, e nada além dele: procurar no log
// só o base64 das chaves deixaria passar os bytes crus ou a string de conexão
// inteira, com a senha do banco.
func TestLogValueRedigeCadaCampoSensivel(t *testing.T) {
	env := validEnv()
	env["DATABASE_URL"] = "postgres://stillon:senha-canaria@banco.exemplo/stillon"
	env["RESEND_API_KEY"], env["EMAIL_FROM"] = "re_chave_canaria", "a@b.dev"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("boot", "cfg", cfg)
	var registro struct {
		Cfg map[string]any `json:"cfg"`
	}
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("log não é JSON: %v", err)
	}
	for _, campo := range []string{"database_url", "master_key", "jwt_secret", "ip_hash_key", "resend_api_key"} {
		t.Run(campo, func(t *testing.T) {
			if got := registro.Cfg[campo]; got != "[REDACTED]" {
				t.Errorf("%s = %v, queria [REDACTED]", campo, got)
			}
		})
	}
}

// O prazo de encerramento da spec §6.2: o stop_grace_period do compose e o
// orçamento do dispatcher são contas feitas sobre este valor.
func TestShutdownGraceEhODaSpec(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("ShutdownGrace = %v, queria 15s", cfg.ShutdownGrace)
	}
}

// Só o /0 confia em qualquer origem. Um /1 é largo, mas é decisão do operador
// e não a falha aberta que a recusa existe para evitar.
func TestTrustedProxiesSoRecusaOPrefixoZero(t *testing.T) {
	cases := []struct {
		valor string
		ok    bool
	}{
		{"0.0.0.0/0", false},
		{"::/0", false},
		{"128.0.0.0/1", true},
		{"8000::/1", true},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			env["TRUSTED_PROXIES"] = c.valor
			_, err := Load(getenvFrom(env))
			if c.ok && err != nil {
				t.Fatalf("recusado: %v", err)
			}
			if !c.ok && (err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES")) {
				t.Fatalf("aceito (err = %v)", err)
			}
		})
	}
}

// Um espaço colado no segredo chegaria ao cabeçalho do Resend e ao remetente.
func TestEmailSemEspacoNasPontas(t *testing.T) {
	env := validEnv()
	env["RESEND_API_KEY"], env["EMAIL_FROM"] = "  re_chave\t", " a@b.dev\n"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResendAPIKey != "re_chave" {
		t.Errorf("ResendAPIKey = %q, queria %q", cfg.ResendAPIKey, "re_chave")
	}
	if cfg.EmailFrom != "a@b.dev" {
		t.Errorf("EmailFrom = %q, queria %q", cfg.EmailFrom, "a@b.dev")
	}
}

// O X-Forwarded-For tem mensagem própria, em qualquer caixa: ela diz o que
// fazer (deixar vazio), em vez de só dizer que o cabeçalho não serve.
func TestXForwardedForTemMensagemPropriaEmQualquerCaixa(t *testing.T) {
	for _, nome := range []string{"X-Forwarded-For", "x-forwarded-for", "X-FORWARDED-FOR"} {
		t.Run(nome, func(t *testing.T) {
			env := validEnv()
			env["CLIENT_IP_HEADER"] = nome
			_, err := Load(getenvFrom(env))
			if err == nil || !strings.Contains(err.Error(), "deixe vazio") {
				t.Fatalf("err = %v, queria a orientação de deixar vazio", err)
			}
		})
	}
}

// Dígito é caractere válido num nome de cabeçalho de proxy.
func TestClientIPHeaderAceitaDigito(t *testing.T) {
	env := validEnv()
	env["CLIENT_IP_HEADER"] = "x-client-ip6"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "X-Client-Ip6" {
		t.Errorf("ClientIPHeader = %q, queria %q", cfg.ClientIPHeader, "X-Client-Ip6")
	}
}

// hostLocal lê a URL como o operador a escreveu, com espaço nas pontas.
func TestHostLocalIgnoraEspacoNasPontas(t *testing.T) {
	cases := []struct {
		url  string
		quer bool
	}{
		{" http://localhost:8080 ", true},
		{"\thttp://127.0.0.1/\n", true},
		{" http://exemplo.com ", false},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			if got := hostLocal(c.url); got != c.quer {
				t.Errorf("hostLocal(%q) = %v, queria %v", c.url, got, c.quer)
			}
		})
	}
}
