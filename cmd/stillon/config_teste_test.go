package main

import (
	"encoding/base64"
	"testing"

	"github.com/BipoXDXD/stillon/internal/config"
)

// configDeTeste monta o Config pelo config.Load, como o main faz, com um
// ambiente sintético. Assim um campo novo que o Load preenche (o
// RequestTimeout, por exemplo) chega aos testes com o valor de produção, e
// não com o zero de um literal esquecido. O teste ajusta depois o que for
// dele: endereços e o prazo de encerramento.
func configDeTeste(t *testing.T, papel config.Role, dsn string) *config.Config {
	t.Helper()
	k := base64.StdEncoding.EncodeToString(chave)
	env := map[string]string{
		"ROLE":            string(papel),
		"DATABASE_URL":    dsn,
		"PUBLIC_BASE_URL": "http://localhost",
		"CORS_ORIGIN":     "http://localhost:5173",
		"MASTER_KEY":      k,
		"JWT_SECRET":      k,
		"IP_HASH_KEY":     k,
		"METRICS_ADDR":    "off",
	}
	cfg, err := config.Load(func(nome string) string { return env[nome] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
