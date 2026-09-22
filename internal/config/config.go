// Package config carrega e valida a configuração do processo a partir do
// ambiente. Falha no boot é preferível a falha em runtime: Load acumula todos
// os problemas e os reporta de uma vez, para que o operador corrija tudo numa
// passada só.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
)

// Role define quais subsistemas este processo executa (spec §3.1).
type Role string

const (
	RoleAll    Role = "all"
	RoleAPI    Role = "api"
	RoleProber Role = "prober"
)

func (r Role) RunsAPI() bool    { return r == RoleAll || r == RoleAPI }
func (r Role) RunsProber() bool { return r == RoleAll || r == RoleProber }

// keyLen é o tamanho exigido para toda chave simétrica: 32 bytes (AES-256,
// HMAC-SHA256).
const keyLen = 32

type Config struct {
	Role           Role
	HTTPAddr       string
	DatabaseURL    string
	PublicBaseURL  string
	CORSOrigin     string
	MasterKey      []byte
	JWTSecret      []byte
	IPHashKey      []byte
	TrustedProxies []netip.Prefix
	LogLevel       slog.Level
	ShutdownGrace  time.Duration
}

// Load recebe getenv como parâmetro em vez de chamar os.Getenv diretamente.
// Isso torna a função pura e testável sem mexer no ambiente do processo de
// teste, o que evita testes que interferem uns nos outros ao rodar em paralelo.
func Load(getenv func(string) string) (*Config, error) {
	var errs []error

	req := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			errs = append(errs, fmt.Errorf("variável obrigatória ausente: %s", name))
		}
		return v
	}

	opt := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}

	key := func(name string) []byte {
		raw := req(name)
		if raw == "" {
			return nil
		}
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s não é base64 válido", name))
			return nil
		}
		if len(b) != keyLen {
			errs = append(errs, fmt.Errorf("%s deve ter %d bytes depois de decodificar, tem %d", name, keyLen, len(b)))
			return nil
		}
		return b
	}

	// TRUSTED_PROXIES é a lista de origens cujo X-Forwarded-For vale. Vazia
	// por padrão, e isso é deliberado: sem lista, o cabeçalho é ignorado e a
	// falha é FECHADA (todos dividem um balde de rate limit). Acreditar no
	// XFF de qualquer origem seria falha ABERTA — o atacante escolheria o
	// próprio balde e a origem gravada na auditoria. Em produção atrás de um
	// proxy, preencher esta lista é obrigatório; o aviso no boot cobra isso.
	proxies := func(name string) []netip.Prefix {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return nil
		}
		var out []netip.Prefix
		for _, parte := range strings.Split(raw, ",") {
			parte = strings.TrimSpace(parte)
			if parte == "" {
				continue
			}
			p, err := netip.ParsePrefix(parte)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %q não é um CIDR válido", name, parte))
				continue
			}
			out = append(out, p)
		}
		return out
	}

	cfg := &Config{
		HTTPAddr:       opt("HTTP_ADDR", ":8080"),
		DatabaseURL:    req("DATABASE_URL"),
		PublicBaseURL:  req("PUBLIC_BASE_URL"),
		CORSOrigin:     req("CORS_ORIGIN"),
		MasterKey:      key("MASTER_KEY"),
		JWTSecret:      key("JWT_SECRET"),
		IPHashKey:      key("IP_HASH_KEY"),
		TrustedProxies: proxies("TRUSTED_PROXIES"),
		ShutdownGrace:  15 * time.Second,
	}

	// Https sem lista de proxies quase sempre significa "esqueci de
	// configurar": o deploy é atrás de um proxy, e sem a lista todo o rate
	// limit por IP e todo o ip_hash da auditoria colapsam num valor só. É
	// aviso e não erro porque um TLS terminado no próprio processo é
	// legítimo, só é raro.
	if strings.HasPrefix(cfg.PublicBaseURL, "https://") && len(cfg.TrustedProxies) == 0 {
		slog.Warn("TRUSTED_PROXIES vazio com PUBLIC_BASE_URL https: " +
			"se houver proxy na frente, o rate limit por IP e o ip_hash da auditoria " +
			"colapsam num valor único para todos os clientes")
	}

	switch role := Role(opt("ROLE", string(RoleAll))); role {
	case RoleAll, RoleAPI, RoleProber:
		cfg.Role = role
	default:
		errs = append(errs, fmt.Errorf("ROLE inválido: %q (use all, api ou prober)", role))
	}

	switch lvl := strings.ToLower(opt("LOG_LEVEL", "info")); lvl {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL inválido: %q (use debug, info, warn ou error)", lvl))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// LogValue garante que registrar a configuração nunca imprime material de
// chave nem a string de conexão (que embute a senha do banco). É a mesma
// estratégia do tipo crypto.Secret: a proteção é do tipo, não da disciplina.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("role", string(c.Role)),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("public_base_url", c.PublicBaseURL),
		slog.String("cors_origin", c.CORSOrigin),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("database_url", "[REDACTED]"),
		slog.String("master_key", "[REDACTED]"),
		slog.String("jwt_secret", "[REDACTED]"),
		slog.String("ip_hash_key", "[REDACTED]"),
	)
}
