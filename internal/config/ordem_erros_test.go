package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// linhasDoErro separa o errors.Join do Load em um erro por linha.
func linhasDoErro(t *testing.T, env map[string]string) []string {
	t.Helper()
	_, err := Load(getenvFrom(env))
	if err == nil {
		t.Fatal("esperava erro")
	}
	return strings.Split(err.Error(), "\n")
}

// confereOrdem exige que cada linha do erro comece pelo prefixo esperado, na
// mesma posição: quem lê o boot de um deploy quebrado corrige de cima para
// baixo, e a ordem faz parte do que o operador vê.
func confereOrdem(t *testing.T, linhas, prefixos []string) {
	t.Helper()
	if len(linhas) != len(prefixos) {
		t.Fatalf("%d erros, queria %d:\n%s", len(linhas), len(prefixos), strings.Join(linhas, "\n"))
	}
	for i, p := range prefixos {
		if !strings.HasPrefix(linhas[i], p) {
			t.Errorf("erro %d = %q, queria prefixo %q", i, linhas[i], p)
		}
	}
}

func TestLoadReportsFieldErrorsInDeclarationOrder(t *testing.T) {
	env := map[string]string{
		"ROLE":                   "xyz",
		"METRICS_ADDR":           "0.0.0.0:9090",
		"PUBLIC_BASE_URL":        "ftp://exemplo.com",
		"MASTER_KEY":             "@@@",
		"JWT_SECRET":             base64.StdEncoding.EncodeToString([]byte("curta")),
		"TRUSTED_PROXIES":        "lixo,0.0.0.0/0",
		"CLIENT_IP_HEADER":       "Host",
		"PROBE_BLOCKED_CIDRS":    "lixo",
		"ARGON2_MAX_CONCURRENT":  "0",
		"CONTENT_RULES_PER_HOST": "101",
		"RESEND_API_KEY":         "chave",
		"LOG_LEVEL":              "x",
	}
	confereOrdem(t, linhasDoErro(t, env), []string{
		"ROLE inválido",
		"METRICS_ADDR",
		"variável obrigatória ausente: DATABASE_URL",
		"PUBLIC_BASE_URL precisa",
		"variável obrigatória ausente: CORS_ORIGIN",
		"MASTER_KEY não é base64",
		"JWT_SECRET deve ter",
		"variável obrigatória ausente: IP_HASH_KEY",
		"TRUSTED_PROXIES: \"lixo\"",
		"TRUSTED_PROXIES: \"0.0.0.0/0\"",
		"CLIENT_IP_HEADER",
		"PROBE_BLOCKED_CIDRS: \"lixo\"",
		"ARGON2_MAX_CONCURRENT",
		"CONTENT_RULES_PER_HOST",
		"RESEND_API_KEY e EMAIL_FROM andam juntas",
		"LOG_LEVEL inválido",
	})
}

func TestLoadReportsProductionErrorsInOrder(t *testing.T) {
	chave := func(i int) string { return base64.StdEncoding.EncodeToString(chavesDeDemo[i]) }
	env := validEnv()
	delete(env, "PROBE_BLOCKED_CIDRS")
	env["MASTER_KEY"], env["JWT_SECRET"], env["IP_HASH_KEY"] = chave(0), chave(1), chave(2)
	env["RESEND_API_KEY"], env["EMAIL_FROM"] = "chave", "nao-e-endereco"
	env["LOG_LEVEL"] = "x"
	confereOrdem(t, linhasDoErro(t, env), []string{
		"EMAIL_FROM não é um endereço válido",
		"variável obrigatória ausente em produção: PROBE_BLOCKED_CIDRS",
		"MASTER_KEY é uma chave de demonstração",
		"JWT_SECRET é uma chave de demonstração",
		"IP_HASH_KEY é uma chave de demonstração",
		"LOG_LEVEL inválido",
	})
}
