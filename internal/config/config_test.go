package config

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	return map[string]string{
		"DATABASE_URL":    "postgres://localhost/vigilume",
		"PUBLIC_BASE_URL": "https://vigilume.example",
		"CORS_ORIGIN":     "https://app.vigilume.example",
		"MASTER_KEY":      k,
		"JWT_SECRET":      k,
		"IP_HASH_KEY":     k,
	}
}

func getenvFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatalf("esperava sucesso, veio: %v", err)
	}
	if cfg.Role != RoleAll {
		t.Errorf("Role padrão = %q, queria %q", cfg.Role, RoleAll)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr padrão = %q, queria \":8080\"", cfg.HTTPAddr)
	}
	if len(cfg.MasterKey) != 32 {
		t.Errorf("MasterKey tem %d bytes, queria 32", len(cfg.MasterKey))
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel padrão = %v, queria Info", cfg.LogLevel)
	}
}

func TestLoadReportsEveryMissingVarAtOnce(t *testing.T) {
	env := validEnv()
	delete(env, "DATABASE_URL")
	delete(env, "CORS_ORIGIN")

	_, err := Load(getenvFrom(env))
	if err == nil {
		t.Fatal("esperava erro quando faltam variáveis")
	}
	for _, want := range []string{"DATABASE_URL", "CORS_ORIGIN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erro não menciona %s: %v", want, err)
		}
	}
}

func TestLoadRejectsShortKey(t *testing.T) {
	env := validEnv()
	env["MASTER_KEY"] = base64.StdEncoding.EncodeToString([]byte("curta"))

	_, err := Load(getenvFrom(env))
	if err == nil || !strings.Contains(err.Error(), "MASTER_KEY") {
		t.Fatalf("esperava erro citando MASTER_KEY, veio: %v", err)
	}
}

func TestLoadRejectsUnknownRole(t *testing.T) {
	env := validEnv()
	env["ROLE"] = "banana"

	_, err := Load(getenvFrom(env))
	if err == nil || !strings.Contains(err.Error(), "ROLE") {
		t.Fatalf("esperava erro citando ROLE, veio: %v", err)
	}
}

func TestRolePredicates(t *testing.T) {
	cases := []struct {
		role              Role
		wantAPI, wantProb bool
	}{
		{RoleAll, true, true},
		{RoleAPI, true, false},
		{RoleProber, false, true},
	}
	for _, c := range cases {
		if got := c.role.RunsAPI(); got != c.wantAPI {
			t.Errorf("%s.RunsAPI() = %v, queria %v", c.role, got, c.wantAPI)
		}
		if got := c.role.RunsProber(); got != c.wantProb {
			t.Errorf("%s.RunsProber() = %v, queria %v", c.role, got, c.wantProb)
		}
	}
}

func TestLogValueRedactsSecrets(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("boot", "cfg", cfg)
	out := buf.String()

	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if strings.Contains(out, secret) {
		t.Error("o log vazou material de chave")
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("esperava marcador de redação no log, veio: %s", out)
	}
}

func TestLoadParsesTrustedProxies(t *testing.T) {
	env := validEnv()
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, 2001:db8::/32"

	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatalf("esperava sucesso, veio: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("%d prefixos confiáveis, queria 2", len(cfg.TrustedProxies))
	}
	if got := cfg.TrustedProxies[0].String(); got != "10.0.0.0/8" {
		t.Errorf("primeiro prefixo = %q, queria 10.0.0.0/8", got)
	}
}

// Vazio é o padrão e significa "não confie em X-Forwarded-For de ninguém" —
// a falha fechada. Ver middleware.ClientIP.
func TestTrustedProxiesDefaultsToEmpty(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("sem TRUSTED_PROXIES vieram %d prefixos, queria nenhum", len(cfg.TrustedProxies))
	}
}

// Um CIDR errado não pode virar silêncio: uma lista que o operador achou que
// tinha preenchido, e que na verdade ficou vazia, é a pior das duas falhas.
func TestLoadRejectsAMalformedTrustedProxy(t *testing.T) {
	env := validEnv()
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, isto-nao-e-um-cidr"

	if _, err := Load(getenvFrom(env)); err == nil {
		t.Fatal("esperava erro para CIDR inválido em TRUSTED_PROXIES")
	} else if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("o erro não menciona TRUSTED_PROXIES: %v", err)
	}
}

// Spec §5.7: /metrics entrega o mapa operacional, e só pode escutar em
// loopback (D10). "off" desliga.
func TestMetricsAddrMustBeLoopback(t *testing.T) {
	cases := []struct {
		valor string
		quer  string // "" com ok=true significa desligado
		ok    bool
	}{
		{"", "127.0.0.1:9090", true}, // padrão
		{"off", "", true},
		{"OFF", "", true},
		{"Off", "", true},
		{"127.0.0.1:9100", "127.0.0.1:9100", true},
		{"[::1]:9090", "[::1]:9090", true},
		{"localhost:9090", "localhost:9090", true},
		{":9090", "", false},
		{"0.0.0.0:9090", "", false},
		{"[::]:9090", "", false},
		{"10.0.0.5:9090", "", false},
		{"sem-porta", "", false},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			if c.valor != "" {
				env["METRICS_ADDR"] = c.valor
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "METRICS_ADDR") {
					t.Fatalf("METRICS_ADDR=%q aceito (err = %v)", c.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("METRICS_ADDR=%q recusado: %v", c.valor, err)
			}
			if cfg.MetricsAddr != c.quer {
				t.Errorf("MetricsAddr = %q, queria %q", cfg.MetricsAddr, c.quer)
			}
		})
	}
}
