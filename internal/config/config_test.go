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
		"DATABASE_URL":    "postgres://localhost/sentinela",
		"PUBLIC_BASE_URL": "https://sentinela.example",
		"CORS_ORIGIN":     "https://app.sentinela.example",
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
