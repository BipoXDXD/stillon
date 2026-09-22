# Sentinela — Plano 1: Fundação e Segurança de Base

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Entregar uma API Go que sobe, autentica usuários com segurança de nível de produção, e traz o núcleo criptográfico e a guarda anti-SSRF já implementados e testados.

**Architecture:** Binário único com papel selecionado por flag (`--role`). Postgres como única dependência externa. `net/http` da stdlib, sem framework. Pacotes puros (`crypto`, `safedial`, `auth/password`) isolados de I/O para serem testáveis sem banco; tudo que toca o banco é testado contra Postgres real via testcontainers com snapshot/restore.

**Tech Stack:** Go 1.26, PostgreSQL 16, `pgx/v5`, `goose`, `golang.org/x/crypto/argon2`, `golang-jwt/jwt/v5`, `golang.org/x/time/rate`, `google/uuid`, `testcontainers-go`.

**Spec:** `docs/superpowers/specs/2026-09-22-sentinela-design.md`

**Roteiro:** `docs/superpowers/plans/2026-09-22-roadmap.md` (este é o plano 1 de 5)

---

## Global Constraints

Valores copiados literalmente da spec. Valem para todas as tarefas.

- **Go 1.26.** Módulo: `github.com/BipoXDXD/sentinela`.
- **PostgreSQL 16+.**
- **Sem framework HTTP.** Apenas `net/http` da stdlib, com padrões de método no `ServeMux` (spec §3.3).
- **Sem ORM.** SQL escrito à mão (spec §3.3).
- **Todo código de aplicação sob `internal/`** (spec §3.4).
- **IDs são UUIDv7**, gerados em Go via `uuid.NewV7()`, nunca `serial` (spec §4.2).
- **Argon2id:** `m=64MiB (65536 KiB), t=3, p=2`, formato PHC (spec §5.2).
- **Access token:** JWT HS256, validade **15 minutos** (spec §5.2).
- **Refresh token:** opaco, **32 bytes** de `crypto/rand`, validade **30 dias**, persistido como SHA-256 (spec §5.2).
- **Cookie de refresh:** `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth` (spec §5.3).
- **Nada em `localStorage`** (spec §5.3).
- **IPs armazenados como HMAC-SHA256**, nunca em claro (spec §4.2).
- **Erro interno nunca atravessa a fronteira HTTP.** Envelope `{"error":{"code","message"},"trace_id"}` (spec §9.1).
- **Recurso de outro usuário retorna 404, nunca 403** (spec §5.5).
- **Toda função de repositório recebe `userID` como primeiro parâmetro** (spec §5.5).
- **`go test -race ./...` precisa passar** em toda tarefa (spec §14).
- **Mensagem crua de erro de rede jamais é persistida nem exibida** — normalizar para o enum `error_kind` (spec §4.2).

### Desvio registrado em relação à spec

A spec §3.4 posiciona `migrations/` na raiz. O `//go:embed` do Go só enxerga
arquivos dentro do diretório do pacote, então as migrations vivem em
`internal/storage/migrations/`. É o único desvio estrutural, e é forçado pela
linguagem.

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `cmd/sentinela/main.go` | Parse de flags, wiring, shutdown gracioso. Sem lógica. |
| `internal/config/config.go` | Env → struct validada. Falha rápido no boot, acumulando todos os erros. |
| `internal/storage/migrate.go` | Migrations embutidas, aplicadas no boot. |
| `internal/storage/migrations/*.sql` | Schema versionado. |
| `internal/storage/pool.go` | Construção do `pgxpool`. |
| `internal/storage/testdb/testdb.go` | Harness de teste: Postgres real, snapshot/restore. |
| `internal/crypto/secret.go` | Tipo `Secret` que se recusa a serializar. |
| `internal/crypto/box.go` | AES-256-GCM com AAD. |
| `internal/crypto/hash.go` | SHA-256 de token, HMAC-SHA256 de IP. |
| `internal/prober/safedial/safedial.go` | Guarda anti-SSRF: validação de URL e de endereço. |
| `internal/prober/safedial/transport.go` | `http.Transport` com `ControlContext` e `CheckRedirect`. |
| `internal/auth/password.go` | Argon2id: hash, verify, needs-rehash. |
| `internal/auth/token.go` | JWT de acesso; refresh opaco. |
| `internal/auth/session.go` | Emissão, rotação com detecção de reúso, revogação de família. |
| `internal/auth/store.go` | Transação e queries de `sessions` e `audit_log`. |
| `internal/httpapi/server.go` | `http.Server` com todos os timeouts; montagem de rotas. |
| `internal/httpapi/errors.go` | Envelope de erro e mapa código→status. |
| `internal/httpapi/middleware/*.go` | Um arquivo por middleware. |
| `internal/httpapi/authhandlers.go` | `/auth/login`, `/auth/refresh`, `/auth/logout`, `/me`. |
| `internal/observability/trace.go` | Propagação do `trace_id` pelo `context`. |
| `internal/clock/clock.go` | Relógio injetável, real e falso. |
| `internal/auth/user.go` | Usuário, bloqueio progressivo e `UserStore`. |
| `internal/httpapi/routes.go` | Montagem das rotas e da cadeia de middlewares. |

Cada arquivo tem uma responsabilidade. Os middlewares ficam separados um por
arquivo porque são testados isoladamente e revisados isoladamente.

---

## Ordem das tarefas e seu motivo

1. **Config e CI** — nada compila sem isso, e CI verde no dia 1 evita dívida.
2. **Banco e harness de teste** — se `testcontainers` não funcionar nesta máquina, toda a estratégia de testes cai. Descobrir no dia 2, não no dia 9.
3. **`crypto`** — puro, sem dependências, usado por quase tudo depois.
4. **`safedial`** — puro, e é o risco de segurança mais caro da spec (§5.1). Antecipado do Plano 2.
5. **Argon2id** — puro.
6. **Sessões** — primeira lógica que precisa de banco real.
7. **Servidor HTTP e middlewares** — infraestrutura de borda.
8. **Endpoints de autenticação** — junta tudo; fim do plano com fluxo completo verificável.

Tarefas 3, 4 e 5 são pacotes puros: rodam em milissegundos, sem Docker.

---

### Task 1: Esqueleto, configuração e CI

**Files:**
- Create: `go.mod`, `.gitignore`, `Makefile`, `.github/workflows/ci.yml`
- Create: `internal/config/config.go`
- Create: `cmd/sentinela/main.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: `config.Config` (struct com campos `Role`, `HTTPAddr`, `DatabaseURL`, `PublicBaseURL`, `CORSOrigin`, `MasterKey []byte`, `JWTSecret []byte`, `IPHashKey []byte`, `LogLevel slog.Level`, `ShutdownGrace time.Duration`); `config.Load(getenv func(string) string) (*Config, error)`; `config.Role` com `RunsAPI() bool` e `RunsProber() bool`.

- [ ] **Step 1: Inicializar o módulo e as dependências**

```bash
cd ~/GolandProjects/sentinela
go mod init github.com/BipoXDXD/sentinela
go get github.com/jackc/pgx/v5@latest
go get github.com/pressly/goose/v3@latest
go get github.com/google/uuid@latest
go get github.com/golang-jwt/jwt/v5@latest
go get golang.org/x/crypto@latest
go get golang.org/x/time@latest
go get golang.org/x/sync@latest
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
```

- [ ] **Step 2: Criar `.gitignore` e `Makefile`**

`.gitignore`:

```
/sentinela
/bin/
/web/node_modules/
/web/dist/
.env
*.test
coverage.out
```

`Makefile`:

```makefile
.PHONY: test test-short lint sec build run

test:
	go test -race -count=1 ./...

test-short:
	go test -race -short -count=1 ./...

lint:
	go vet ./...
	staticcheck ./...

sec:
	gosec -quiet ./...
	govulncheck ./...

build:
	go build -o bin/sentinela ./cmd/sentinela

run: build
	./bin/sentinela --role=all
```

`test-short` pula os testes que precisam de Docker — é o alvo para rodar a cada
salvamento durante o desenvolvimento.

- [ ] **Step 3: Escrever o teste que falha**

`internal/config/config_test.go`:

```go
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
```

- [ ] **Step 4: Rodar o teste e confirmar que falha**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Load`, `undefined: RoleAll`.

- [ ] **Step 5: Implementar a configuração**

`internal/config/config.go`:

```go
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
	Role          Role
	HTTPAddr      string
	DatabaseURL   string
	PublicBaseURL string
	CORSOrigin    string
	MasterKey     []byte
	JWTSecret     []byte
	IPHashKey     []byte
	LogLevel      slog.Level
	ShutdownGrace time.Duration
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

	cfg := &Config{
		HTTPAddr:      opt("HTTP_ADDR", ":8080"),
		DatabaseURL:   req("DATABASE_URL"),
		PublicBaseURL: req("PUBLIC_BASE_URL"),
		CORSOrigin:    req("CORS_ORIGIN"),
		MasterKey:     key("MASTER_KEY"),
		JWTSecret:     key("JWT_SECRET"),
		IPHashKey:     key("IP_HASH_KEY"),
		ShutdownGrace: 15 * time.Second,
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
```

- [ ] **Step 6: Rodar o teste e confirmar que passa**

Run: `go test -race ./internal/config/ -v`
Expected: PASS nos 6 testes.

- [ ] **Step 7: Escrever o `main.go` mínimo**

`cmd/sentinela/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/BipoXDXD/sentinela/internal/config"
)

func main() {
	role := flag.String("role", "", "all | api | prober (sobrepõe a env ROLE)")
	flag.Parse()

	getenv := os.Getenv
	if *role != "" {
		getenv = func(key string) string {
			if key == "ROLE" {
				return *role
			}
			return os.Getenv(key)
		}
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		// Antes de a config existir não há logger configurado; stderr basta.
		slog.Error("configuração inválida", "erro", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("iniciando", "cfg", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Error("encerrou com erro", "erro", err)
		os.Exit(1)
	}
	log.Info("encerrado")
}

// run é onde o wiring acontece. Mantido separado de main para que main tenha
// apenas responsabilidade de processo (flags, sinais, código de saída).
func run(ctx context.Context, cfg *config.Config) error {
	<-ctx.Done()
	return nil
}
```

- [ ] **Step 8: Criar o pipeline de CI**

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: vet
        run: go vet ./...

      - name: staticcheck
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          staticcheck ./...

      - name: gosec
        run: |
          go install github.com/securego/gosec/v2/cmd/gosec@latest
          gosec -quiet ./...

      - name: govulncheck
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...

      - name: test
        run: go test -race -count=1 -coverprofile=coverage.out ./...
```

O runner do GitHub Actions já traz Docker, então os testes com testcontainers
rodam no CI sem configuração extra.

- [ ] **Step 9: Verificar tudo e commitar**

Run: `make lint && make test && make build`
Expected: sem saída de erro; binário em `bin/sentinela`.

```bash
git add go.mod go.sum .gitignore Makefile .github cmd internal/config
git commit -m "feat(config): carregamento de configuração validado e esqueleto do binário

Load acumula todos os erros de ambiente e reporta de uma vez. getenv é
injetado para manter a função pura e os testes independentes. LogValue
redige chaves e a string de conexão."
```

---

### Task 2: Banco, migrations e harness de teste

**Files:**
- Create: `internal/storage/migrations/00001_init.sql`
- Create: `internal/storage/migrate.go`
- Create: `internal/storage/pool.go`
- Create: `internal/storage/testdb/testdb.go`
- Test: `internal/storage/migrate_test.go`

**Interfaces:**
- Consumes: `config.Config.DatabaseURL` (Task 1).
- Produces: `storage.Migrate(ctx context.Context, dsn string) error`; `storage.NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error)`; `testdb.New(t *testing.T) *pgxpool.Pool` — devolve pool com schema migrado e estado limpo por teste.

- [ ] **Step 1: Escrever a primeira migration**

`internal/storage/migrations/00001_init.sql`:

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

-- IDs são UUIDv7 gerados na aplicação (spec §4.2). O Postgres 16 não tem
-- uuidv7() nativo, e gerar em Go evita depender de extensão no servidor.
CREATE TABLE users (
    id              uuid PRIMARY KEY,
    email           citext NOT NULL UNIQUE,
    password_hash   text NOT NULL,
    totp_secret_enc bytea,
    failed_attempts int NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    used_at    timestamptz,
    ip_hash    bytea,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Índice parcial: a revogação de família só percorre sessões ainda vivas.
CREATE INDEX sessions_family_alive_idx ON sessions (family_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE audit_log (
    id        uuid PRIMARY KEY,
    user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    action    text NOT NULL,
    entity    text,
    entity_id uuid,
    ip_hash   bytea,
    at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_user_at_idx ON audit_log (user_id, at DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
```

- [ ] **Step 2: Escrever o teste que falha**

`internal/storage/migrate_test.go`:

```go
package storage_test

import (
	"context"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

// TestMigrationsApplyAndSchemaIsUsable prova três coisas de uma vez: o
// testcontainers funciona nesta máquina, as migrations aplicam, e o schema
// resultante aceita as operações que o resto do plano vai depender.
func TestMigrationsApplyAndSchemaIsUsable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	for _, table := range []string{"users", "sessions", "audit_log"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("consultar tabela %s: %v", table, err)
		}
		if !exists {
			t.Errorf("tabela %s não existe depois das migrations", table)
		}
	}
}

// TestEmailIsCaseInsensitive verifica que a coluna citext faz o que se espera
// dela: dois cadastros que diferem só em caixa colidem.
func TestEmailIsCaseInsensitive(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	insert := `INSERT INTO users (id, email, password_hash) VALUES (gen_random_uuid(), $1, 'x')`

	if _, err := pool.Exec(ctx, insert, "Pessoa@Exemplo.com"); err != nil {
		t.Fatalf("primeiro insert deveria passar: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "pessoa@exemplo.com"); err == nil {
		t.Error("esperava violação de unicidade para e-mail em caixa diferente")
	}
}

// TestSnapshotIsolatesTests confirma que cada teste recebe banco limpo. Se
// falhar, todos os testes de integração seguintes são não confiáveis.
func TestSnapshotIsolatesTests(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("banco começou com %d usuários, queria 0 — o isolamento quebrou", count)
	}
}
```

- [ ] **Step 3: Rodar o teste e confirmar que falha**

Run: `go test ./internal/storage/ -v`
Expected: FAIL — pacote `testdb` não existe.

- [ ] **Step 4: Implementar migrations e pool**

`internal/storage/migrate.go`:

```go
// Package storage concentra o acesso ao Postgres: aplicação de migrations,
// construção do pool e os repositórios.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver "pgx" no database/sql
	"github.com/pressly/goose/v3"
)

// O //go:embed só enxerga arquivos dentro do diretório do pacote, por isso as
// migrations vivem aqui e não na raiz do repositório (desvio registrado nas
// Global Constraints).
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate aplica todas as migrations pendentes. É chamada no boot, o que
// elimina a classe de bug "esqueci de migrar em produção".
//
// Usa database/sql em vez do pool pgx porque o goose exige *sql.DB. A conexão
// é aberta e fechada aqui; o pool de trabalho é construído por NewPool.
func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir conexão para migrar: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialeto do goose: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("aplicar migrations: %w", err)
	}
	return nil
}
```

`internal/storage/pool.go`:

```go
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool constrói o pool de conexões da aplicação.
//
// MaxConnLifetime existe para que conexões sejam recicladas periodicamente:
// sem isso, uma conexão pode ficar pendurada em um backend de Postgres que o
// provedor vai derrubar, e o erro aparece como falha intermitente.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN inválido: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("criar pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping no banco: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 5: Implementar o harness de teste**

`internal/storage/testdb/testdb.go`:

```go
// Package testdb sobe um Postgres real para os testes de integração.
//
// A spec (§14) proíbe mock de banco: RLS, SKIP LOCKED e violação de
// constraint são invisíveis contra um mock, e são justamente o que precisa de
// teste. O contêiner sobe uma vez por execução de pacote; cada teste recebe o
// banco restaurado ao estado pós-migration via snapshot.
package testdb

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/BipoXDXD/sentinela/internal/storage"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	dsn       string
	startErr  error
)

const (
	dbName = "sentinela_test"
	dbUser = "sentinela"
	dbPass = "sentinela"
)

// New devolve um pool ligado a um Postgres limpo e migrado.
//
// Pula com -short: quem não tem Docker disponível ainda consegue rodar os
// testes dos pacotes puros (crypto, safedial, password) com `make test-short`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("precisa de Docker; pulado em -short")
	}

	once.Do(start)
	if startErr != nil {
		t.Fatalf("subir Postgres de teste: %v", startErr)
	}

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("conectar ao Postgres de teste: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Restore precisa de zero conexões abertas no banco alvo, por isso
		// vem depois do Close.
		if err := container.Restore(ctx); err != nil {
			t.Fatalf("restaurar snapshot: %v", err)
		}
	})
	return pool
}

func start() {
	ctx := context.Background()

	container, startErr = tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(dbUser),
		tcpostgres.WithPassword(dbPass),
		tcpostgres.WithSQLDriver("pgx"), // exigido por Snapshot/Restore
		tcpostgres.BasicWaitStrategies(),
	)
	if startErr != nil {
		return
	}

	dsn, startErr = container.ConnectionString(ctx, "sslmode=disable")
	if startErr != nil {
		return
	}

	// O snapshot é tirado com o schema já migrado e sem dados, então cada
	// teste começa exatamente daqui.
	if startErr = storage.Migrate(ctx, dsn); startErr != nil {
		return
	}
	startErr = container.Snapshot(ctx)
}

// Terminate derruba o contêiner. Chame de um TestMain quando quiser
// encerramento explícito; o Ryuk do testcontainers já limpa sozinho depois.
func Terminate() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
```

- [ ] **Step 6: Rodar o teste e confirmar que passa**

Run: `go test -race ./internal/storage/... -v`
Expected: PASS. A primeira execução baixa a imagem `postgres:16-alpine` e leva
alguns minutos; as seguintes levam segundos.

Se falhar com erro de socket do Docker, resolver agora — todo o resto do plano
depende disso. Verificar com `docker info`.

- [ ] **Step 7: Ligar a migration ao boot**

Em `cmd/sentinela/main.go`, substituir o corpo de `run`:

```go
func run(ctx context.Context, cfg *config.Config) error {
	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations aplicadas")

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	<-ctx.Done()
	return nil
}
```

Adicionar `"github.com/BipoXDXD/sentinela/internal/storage"` aos imports.

- [ ] **Step 8: Commitar**

```bash
git add internal/storage cmd/sentinela/main.go
git commit -m "feat(storage): schema inicial, migrations embutidas e harness de teste

Migrations aplicadas no boot via embed.FS. O harness de teste sobe Postgres
real uma vez por pacote e usa snapshot/restore para dar banco limpo a cada
teste, porque RLS e SKIP LOCKED são invisíveis contra mock."
```

---

### Task 3: Núcleo criptográfico

**Files:**
- Create: `internal/crypto/secret.go`
- Create: `internal/crypto/box.go`
- Create: `internal/crypto/hash.go`
- Test: `internal/crypto/secret_test.go`
- Test: `internal/crypto/box_test.go`
- Test: `internal/crypto/hash_test.go`

**Interfaces:**
- Consumes: nada (pacote puro, sem I/O).
- Produces: `crypto.Secret` (tipo `[]byte` com `Reveal() []byte`); `crypto.NewBox(key []byte) (*Box, error)`; `(*Box).Seal(plaintext, aad []byte) ([]byte, error)`; `(*Box).Open(sealed, aad []byte) (Secret, error)`; `crypto.AAD(table, column string, id uuid.UUID) []byte`; `crypto.HashToken(token string) []byte`; `crypto.HashIP(key []byte, ip string) []byte`; erros `crypto.ErrMalformed` e `crypto.ErrAuthFailed`.

- [ ] **Step 1: Escrever o teste do tipo `Secret`**

`internal/crypto/secret_test.go`:

```go
package crypto_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

const plaintext = "senha-do-webhook-super-secreta"

// O objetivo do tipo Secret é tornar o vazamento impossível pelo sistema de
// tipos, não improvável pela disciplina do programador. Cada teste abaixo
// cobre um caminho pelo qual um segredo costuma escapar na prática.
func TestSecretNeverPrints(t *testing.T) {
	s := crypto.Secret(plaintext)

	cases := map[string]string{
		"%v direto":  fmt.Sprintf("%v", s),
		"%s direto":  fmt.Sprintf("%s", s),
		"%#v direto": fmt.Sprintf("%#v", s),
		"dentro de struct com %v": fmt.Sprintf("%v", struct {
			Nome  string
			Token crypto.Secret
		}{"alvo", s}),
	}

	for nome, got := range cases {
		if strings.Contains(got, plaintext) {
			t.Errorf("%s vazou o segredo: %s", nome, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s não trouxe o marcador de redação: %s", nome, got)
		}
	}
}

func TestSecretNeverSerializesToJSON(t *testing.T) {
	payload := struct {
		Nome  string        `json:"nome"`
		Token crypto.Secret `json:"token"`
	}{"alvo", crypto.Secret(plaintext)}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(plaintext)) {
		t.Errorf("json.Marshal vazou o segredo: %s", b)
	}
	// Sem esta segunda asserção o teste é vacuamente verde: encoding/json
	// serializa []byte como base64, então a ausência do plaintext não prova
	// que MarshalJSON foi chamado.
	if !bytes.Contains(b, []byte("[REDACTED]")) {
		t.Errorf("json.Marshal não trouxe o marcador de redação: %s", b)
	}
}

func TestSecretNeverReachesTheLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	log.Info("configuração do alvo", "token", crypto.Secret(plaintext))

	if strings.Contains(buf.String(), plaintext) {
		t.Errorf("slog vazou o segredo: %s", buf.String())
	}
	// Mesma armadilha do teste de JSON: o slog também codifica []byte em
	// base64, então só a ausência do plaintext não detectaria a remoção de
	// LogValue.
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Errorf("slog não trouxe o marcador de redação: %s", buf.String())
	}
}

// Reveal tem nome explícito de propósito: uma busca por "Reveal(" no
// repositório lista todos os pontos onde um segredo é exposto em claro.
func TestRevealReturnsTheBytes(t *testing.T) {
	s := crypto.Secret(plaintext)
	if got := string(s.Reveal()); got != plaintext {
		t.Errorf("Reveal() = %q, queria %q", got, plaintext)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./internal/crypto/ -run TestSecret -v`
Expected: FAIL — `undefined: crypto.Secret`.

- [ ] **Step 3: Implementar `Secret`**

`internal/crypto/secret.go`:

```go
// Package crypto reúne as primitivas criptográficas do Sentinela: o tipo
// Secret, o envelope AES-GCM e os hashes de token e de IP.
package crypto

import "log/slog"

const redacted = "[REDACTED]"

// Secret embrulha bytes sensíveis e se recusa a aparecer em qualquer saída
// textual. Cobre os quatro caminhos pelos quais um segredo vaza na prática:
// fmt, encoding/json, log/slog e interpolação em string.
//
// A proteção é do sistema de tipos. Um slog.Info descuidado às três da manhã
// não consegue imprimir o conteúdo — não porque o programador lembrou, mas
// porque o tipo não deixa.
type Secret []byte

func (Secret) String() string                 { return redacted }
func (Secret) GoString() string               { return redacted }
func (Secret) MarshalJSON() ([]byte, error)   { return []byte(`"` + redacted + `"`), nil }
func (Secret) LogValue() slog.Value           { return slog.StringValue(redacted) }

// Reveal devolve os bytes em claro. O nome é deliberadamente chamativo: uma
// busca por "Reveal(" enumera todos os pontos de exposição do repositório.
func (s Secret) Reveal() []byte { return []byte(s) }
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `go test -race ./internal/crypto/ -run TestSecret -v`
Expected: PASS nos 4 testes.

- [ ] **Step 5: Escrever o teste do envelope AES-GCM**

`internal/crypto/box_test.go`:

```go
package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{7}, 32)
}

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := crypto.NewBox(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := crypto.AAD("targets", "headers_enc", uuid.New())
	msg := []byte("Authorization: Bearer abc123")

	sealed, err := box.Seal(msg, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, msg) {
		t.Error("o ciphertext contém o texto em claro")
	}

	opened, err := box.Open(sealed, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened.Reveal(), msg) {
		t.Errorf("Open devolveu %q, queria %q", opened.Reveal(), msg)
	}
}

// O AAD amarra o ciphertext à linha que o guarda. Sem ele, quem tem acesso de
// escrita ao banco pode copiar o segredo de um alvo para outro e fazer o
// sistema usar credenciais de terceiros.
func TestOpenRejectsCiphertextMovedToAnotherRow(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	original := crypto.AAD("targets", "headers_enc", uuid.New())
	outra := crypto.AAD("targets", "headers_enc", uuid.New())

	sealed, err := box.Seal([]byte("segredo"), original)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := box.Open(sealed, outra); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", "headers_enc", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)
	sealed[len(sealed)-1] ^= 0xFF // vira um bit do tag de autenticação

	if _, err := box.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTruncatedInput(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))

	if _, err := box.Open([]byte{1, 2, 3}, nil); !errors.Is(err, crypto.ErrMalformed) {
		t.Errorf("erro = %v, queria ErrMalformed", err)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	outra, _ := crypto.NewBox(bytes.Repeat([]byte{9}, 32))
	aad := crypto.AAD("targets", "headers_enc", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)

	if _, err := outra.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

// Nonce repetido em GCM é catastrófico: dois ciphertexts com o mesmo nonce
// permitem recuperar o XOR dos textos em claro. Este teste é uma sentinela
// contra alguém "otimizar" o nonce para um valor fixo.
func TestSealUsesFreshNonceEveryTime(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", "headers_enc", uuid.New())
	msg := []byte("mesma mensagem")

	vistos := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sealed, err := box.Seal(msg, aad)
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(sealed[:12])
		if vistos[nonce] {
			t.Fatal("nonce repetido em 100 selagens")
		}
		vistos[nonce] = true
	}
}

func TestNewBoxRejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := crypto.NewBox(bytes.Repeat([]byte{1}, n)); err == nil {
			t.Errorf("NewBox aceitou chave de %d bytes", n)
		}
	}
}
```

- [ ] **Step 6: Rodar e confirmar que falha**

Run: `go test ./internal/crypto/ -run 'TestSeal|TestOpen|TestNewBox' -v`
Expected: FAIL — `undefined: crypto.NewBox`.

- [ ] **Step 7: Implementar o envelope**

`internal/crypto/box.go`:

```go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	// ErrMalformed indica entrada curta demais para conter nonce e tag.
	ErrMalformed = errors.New("ciphertext malformado")
	// ErrAuthFailed cobre chave errada, AAD errado e adulteração. Os três
	// casos devolvem o mesmo erro de propósito: distinguir entre eles daria
	// a um atacante um oráculo.
	ErrAuthFailed = errors.New("falha na autenticação do ciphertext")
)

// KeySize é o tamanho exigido da chave mestra: AES-256.
const KeySize = 32

// Box cifra e decifra segredos em repouso com AES-256-GCM (spec §5.4).
type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("chave deve ter %d bytes, tem %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("construir AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construir GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal devolve nonce || ciphertext || tag.
//
// O nonce vai prefixado porque GCM exige um nonce distinto por operação com a
// mesma chave, e guardá-lo junto do ciphertext é o que permite decifrar sem
// uma coluna extra.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("gerar nonce: %w", err)
	}
	// O primeiro argumento é o destino: passar nonce faz o resultado já sair
	// com o prefixo, sem cópia extra.
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Open(sealed, aad []byte) (Secret, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns+b.aead.Overhead() {
		return nil, ErrMalformed
	}
	nonce, ct := sealed[:ns], sealed[ns:]

	pt, err := b.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return Secret(pt), nil
}

// AAD amarra um ciphertext à coluna e à linha exatas que o armazenam. Com
// isso, mover o valor de um registro para outro — ou de uma coluna cifrada
// para outra da mesma linha — invalida a autenticação, mesmo que o atacante
// tenha acesso de escrita ao banco.
//
// A coluna entra no AAD desde já por assimetria de custo: hoje é um
// parâmetro; depois de existir ciphertext gravado, mudar o AAD exigiria
// re-cifrar tudo.
func AAD(table, column string, id uuid.UUID) []byte {
	b := make([]byte, 0, len(table)+len(column)+2+16)
	b = append(b, table...)
	b = append(b, ':')
	b = append(b, column...)
	b = append(b, ':')
	return append(b, id[:]...)
}
```

- [ ] **Step 8: Rodar e confirmar que passa**

Run: `go test -race ./internal/crypto/ -v`
Expected: PASS em todos.

- [ ] **Step 9: Escrever o teste dos hashes**

`internal/crypto/hash_test.go`:

```go
package crypto_test

import (
	"bytes"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

func TestHashTokenIsDeterministicAndFixedLength(t *testing.T) {
	a := crypto.HashToken("abc")
	b := crypto.HashToken("abc")
	c := crypto.HashToken("abd")

	if !bytes.Equal(a, b) {
		t.Error("HashToken não é determinístico")
	}
	if bytes.Equal(a, c) {
		t.Error("entradas diferentes produziram o mesmo hash")
	}
	if len(a) != 32 {
		t.Errorf("hash tem %d bytes, queria 32", len(a))
	}
}

// IP tem espaço de busca minúsculo (2^32 para IPv4): um SHA-256 puro seria
// revertido por enumeração em minutos. Por isso o IP usa hash com chave, e o
// token — que tem 256 bits de entropia — não precisa.
func TestHashIPDependsOnTheKey(t *testing.T) {
	k1 := bytes.Repeat([]byte{1}, 32)
	k2 := bytes.Repeat([]byte{2}, 32)

	if bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k2, "203.0.113.9")) {
		t.Error("chaves diferentes produziram o mesmo hash de IP")
	}
	if !bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k1, "203.0.113.9")) {
		t.Error("HashIP não é determinístico com a mesma chave")
	}
	if bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k1, "203.0.113.10")) {
		t.Error("IPs diferentes produziram o mesmo hash")
	}
}
```

- [ ] **Step 10: Implementar os hashes**

`internal/crypto/hash.go`:

```go
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

// HashToken é o que vai para a coluna sessions.token_hash (spec §5.2).
//
// SHA-256 puro basta aqui: o token tem 32 bytes de crypto/rand, ou seja 256
// bits de entropia, o que torna busca exaustiva inviável. Chave não
// acrescentaria segurança e acrescentaria uma chave para gerenciar.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// HashIP é o que vai para as colunas ip_hash (spec §4.2).
//
// Aqui a chave é obrigatória. O espaço de endereços IPv4 tem 2^32 elementos:
// um hash sem chave seria revertido por enumeração exaustiva em minutos, e a
// coluna deixaria de proteger qualquer coisa.
func HashIP(key []byte, ip string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ip))
	return mac.Sum(nil)
}
```

- [ ] **Step 11: Rodar tudo e commitar**

Run: `go test -race -short ./internal/crypto/ -v`
Expected: PASS em todos (o pacote é puro; roda sem Docker).

```bash
git add internal/crypto
git commit -m "feat(crypto): tipo Secret, envelope AES-GCM e hashes de token e IP

Secret cobre os quatro caminhos de vazamento (fmt, json, slog, interpolação).
O AAD do envelope amarra o ciphertext ao UUID da linha, impedindo que um
segredo seja movido entre registros. IP usa HMAC porque 2^32 é enumerável;
token usa SHA-256 puro porque tem 256 bits de entropia."
```


> **Correções aplicadas durante a execução** (ver rulings R6–R11 no ledger em
> `.superpowers/sdd/.../progress.md`): `HashIP` ganhou guarda de tamanho de
> chave com `panic`, porque `hmac.New` aceita chave vazia em silêncio e
> devolveria o hash enumerável que a §4.2 quer evitar; `Secret` implementa
> `fmt.Formatter`, porque `String`/`GoString` não cobrem `%d`, `%c` nem `%08b`;
> `AAD` passou a incluir a coluna; e quatro testes ganharam força de detecção
> que não tinham (fronteira de truncamento, propriedades do `AAD`, nonce entre
> instâncias distintas, e known-answer tests que travam o algoritmo dos hashes).

---

### Task 4: Guarda anti-SSRF (`safedial`)

Antecipada do Plano 2. É o risco de segurança mais caro da spec (§5.1) e é um
pacote puro, então descobrir um erro aqui custa minutos em vez de dias.

**Files:**
- Create: `internal/prober/safedial/safedial.go`
- Create: `internal/prober/safedial/transport.go`
- Test: `internal/prober/safedial/safedial_test.go`
- Test: `internal/prober/safedial/transport_test.go`

**Interfaces:**
- Consumes: nada (pacote puro).
- Produces: `safedial.ValidateURL(raw string) (*url.URL, error)`; `safedial.AllowIP(ip netip.Addr) error`; `safedial.AllowAddr(addr string) error`; `safedial.NewTransport() *http.Transport`; `safedial.NewClient(timeout time.Duration) *http.Client`; erros `safedial.ErrBlocked` e `safedial.ErrInvalidURL`.

- [ ] **Step 1: Escrever o teste da lista de bloqueio**

`internal/prober/safedial/safedial_test.go`:

```go
package safedial_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/prober/safedial"
)

func TestAllowIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
		porque  string
	}{
		// Públicos: liberados.
		{"8.8.8.8", false, "DNS público"},
		{"203.0.113.9", true, "TEST-NET-3, reservado para documentação"},
		{"1.1.1.1", false, "DNS público"},
		{"2606:4700:4700::1111", false, "IPv6 público"},

		// Loopback.
		{"127.0.0.1", true, "loopback"},
		{"127.255.255.254", true, "loopback, faixa inteira"},
		{"::1", true, "loopback IPv6"},

		// O bypass clássico: IPv4 embutido em IPv6. Sem Unmap, este endereço
		// não casa com 127.0.0.0/8 e atravessa a guarda inteira.
		{"::ffff:127.0.0.1", true, "loopback disfarçado de IPv6"},
		{"::ffff:169.254.169.254", true, "metadata da nuvem disfarçado de IPv6"},

		// Metadata de provedor de nuvem: o alvo número um de SSRF.
		{"169.254.169.254", true, "endpoint de metadata da AWS/GCP"},
		{"169.254.0.1", true, "link-local"},
		{"fe80::1", true, "link-local IPv6"},

		// Redes privadas.
		{"10.0.0.5", true, "RFC1918"},
		{"172.16.0.1", true, "RFC1918"},
		{"172.31.255.255", true, "RFC1918, borda da faixa"},
		{"192.168.1.1", true, "RFC1918"},
		{"fc00::1", true, "unique local IPv6"},
		{"fd00::1", true, "unique local IPv6"},

		// Bordas que costumam escapar.
		{"172.32.0.1", false, "fora da /12, é público"},
		{"0.0.0.0", true, "this host"},
		{"100.64.0.1", true, "CGNAT"},
		{"224.0.0.1", true, "multicast"},
		{"255.255.255.255", true, "broadcast"},
	}

	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			addr, err := netip.ParseAddr(c.ip)
			if err != nil {
				t.Fatalf("IP de teste inválido: %v", err)
			}

			err = safedial.AllowIP(addr)
			gotBlocked := err != nil

			if gotBlocked != c.blocked {
				t.Errorf("AllowIP(%s) bloqueou=%v, queria %v (%s)",
					c.ip, gotBlocked, c.blocked, c.porque)
			}
			if gotBlocked && !errors.Is(err, safedial.ErrBlocked) {
				t.Errorf("erro = %v, queria envolver ErrBlocked", err)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		raw    string
		ok     bool
		porque string
	}{
		{"https://exemplo.com/health", true, "caso normal"},
		{"http://exemplo.com:8080/x", true, "porta explícita"},
		{"file:///etc/passwd", false, "esquema file lê disco do servidor"},
		{"gopher://exemplo.com/", false, "gopher permite forjar protocolo arbitrário"},
		{"dict://exemplo.com/", false, "esquema não permitido"},
		{"ftp://exemplo.com/", false, "esquema não permitido"},
		{"", false, "vazio"},
		{"https://", false, "sem host"},
		{"exemplo.com", false, "sem esquema"},
		{"https://user:senha@exemplo.com/", false, "credencial na URL vaza em log"},
	}

	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			_, err := safedial.ValidateURL(c.raw)
			if (err == nil) != c.ok {
				t.Errorf("ValidateURL(%q) erro=%v, queria ok=%v (%s)", c.raw, err, c.ok, c.porque)
			}
		})
	}
}

func TestAllowAddrRejectsNonLiteralHost(t *testing.T) {
	// ControlContext sempre recebe IP literal. Qualquer outra coisa indica
	// que a função está sendo chamada fora do lugar previsto — negar é a
	// resposta segura.
	if err := safedial.AllowAddr("exemplo.com:443"); err == nil {
		t.Error("esperava bloqueio para host não literal")
	}
	if err := safedial.AllowAddr("sem-porta"); err == nil {
		t.Error("esperava bloqueio para endereço sem porta")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./internal/prober/safedial/ -v`
Expected: FAIL — pacote não existe.

- [ ] **Step 3: Implementar a guarda**

`internal/prober/safedial/safedial.go`:

```go
// Package safedial protege o sondador contra SSRF (spec §5.1).
//
// O Sentinela aceita uma URL arbitrária do usuário e faz uma requisição a
// partir do servidor — ou seja, é um proxy por construção. Sem esta guarda,
// um alvo cadastrado como http://169.254.169.254/ transforma o monitor em
// ferramenta de varredura da rede interna e de roubo de credenciais de nuvem.
//
// A defesa central não está em validar a URL, e sim em validar o endereço IP
// no instante da conexão — ver transport.go.
package safedial

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	ErrBlocked    = errors.New("endereço bloqueado")
	ErrInvalidURL = errors.New("URL inválida")
)

// blockedPrefixes cobre tudo que não deve ser alcançável a partir do
// sondador. Cada faixa tem um motivo concreto de estar aqui.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "este host"
	netip.MustParsePrefix("10.0.0.0/8"),         // RFC1918
	netip.MustParsePrefix("100.64.0.0/10"),      // CGNAT
	netip.MustParsePrefix("127.0.0.0/8"),        // loopback
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, inclui metadata de nuvem
	netip.MustParsePrefix("172.16.0.0/12"),      // RFC1918
	netip.MustParsePrefix("192.0.0.0/24"),       // atribuições IETF
	netip.MustParsePrefix("192.0.2.0/24"),       // TEST-NET-1
	netip.MustParsePrefix("192.168.0.0/16"),     // RFC1918
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),    // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),     // TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),        // multicast
	netip.MustParsePrefix("240.0.0.0/4"),        // reservado
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
	netip.MustParsePrefix("::/128"),             // não especificado
	netip.MustParsePrefix("::1/128"),            // loopback IPv6
	netip.MustParsePrefix("fc00::/7"),           // unique local
	netip.MustParsePrefix("fe80::/10"),          // link-local IPv6
	netip.MustParsePrefix("ff00::/8"),           // multicast IPv6
}

// AllowIP decide se um endereço pode ser alcançado.
func AllowIP(ip netip.Addr) error {
	if !ip.IsValid() {
		return fmt.Errorf("%w: endereço inválido", ErrBlocked)
	}

	// Unmap é indispensável. ::ffff:127.0.0.1 é 127.0.0.1 vestido de IPv6:
	// sem desfazer o mapeamento, ele não casa com 127.0.0.0/8 e atravessa a
	// guarda inteira. É o bypass mais comum contra listas de bloqueio.
	ip = ip.Unmap()

	// netip.Prefix.Contains devolve false para QUALQUER endereço com zona
	// IPv6 ("Prefixes strip zones"), e Unmap() não remove a zona. Sem esta
	// recusa, ::1%lo atravessa as vinte faixas sem casar nenhuma. Alvo
	// legítimo nunca carrega zona, então recusar é a resposta fail-closed.
	if ip.Zone() != "" {
		return fmt.Errorf("%w: zona IPv6 não é permitida (%s)", ErrBlocked, ip)
	}

	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s pertence a %s", ErrBlocked, ip, p)
		}
	}
	return nil
}

// AllowAddr valida um endereço no formato "IP:porta", que é o que o
// ControlContext do net.Dialer entrega.
//
// Notação alternativa de IP (octal como 0177.0.0.1, decimal como 2130706433)
// não é problema aqui: a validação acontece sobre o endereço já resolvido
// pelo sistema operacional, não sobre o texto digitado pelo usuário.
func AllowAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: endereço ilegível %q", ErrBlocked, addr)
	}
	if port == "" {
		return fmt.Errorf("%w: sem porta em %q", ErrBlocked, addr)
	}

	ip, err := netip.ParseAddr(host)
	if err != nil {
		// Chegar aqui significa que a função foi chamada fora do
		// ControlContext. Negar é a resposta segura.
		return fmt.Errorf("%w: %q não é IP literal", ErrBlocked, host)
	}
	return AllowIP(ip)
}

// ValidateURL é a primeira camada: rejeita o que nem deve virar requisição.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: esquema %q não é permitido", ErrInvalidURL, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: sem host", ErrInvalidURL)
	}
	if u.User != nil {
		// Credencial embutida na URL acaba em log de acesso e em tela.
		return nil, fmt.Errorf("%w: credenciais na URL não são permitidas", ErrInvalidURL)
	}
	return u, nil
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `go test -race -short ./internal/prober/safedial/ -v`
Expected: PASS em todos os casos da tabela.

- [ ] **Step 5: Escrever o teste do transporte — a prova de ponta a ponta**

`internal/prober/safedial/transport_test.go`:

```go
package safedial_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/sentinela/internal/prober/safedial"
)

// Este é o teste que importa. httptest.NewServer escuta em 127.0.0.1, então
// um cliente comum o alcança sem esforço. Se o cliente do safedial conseguir
// falar com ele, a guarda inteira é decorativa.
func TestClientCannotReachLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := safedial.NewClient(5 * time.Second)

	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("o cliente alcançou %s; a guarda não funcionou", srv.URL)
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
}

// Atenção ao que este teste NÃO cobre. Ele verifica que um alvo cujo primeiro
// salto já é interno morre imediatamente — e só isso. Não há redirecionamento
// exercido aqui: httptest.NewServer sempre escuta em loopback, então o próprio
// redirecionador é destino bloqueado e a requisição termina no salto zero.
//
// A cobertura real do CheckRedirect está em
// TestCheckRedirectRevalidaEsquemaELimitaSaltos, que o chama diretamente.
func TestAlvoQueRedirecionaParaInternoMorreNoPrimeiroSalto(t *testing.T) {
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("segredo interno"))
	}))
	defer interno.Close()

	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, interno.URL, http.StatusFound)
	}))
	defer redirecionador.Close()

	client := safedial.NewClient(5 * time.Second)

	resp, err := client.Get(redirecionador.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("o redirecionamento para loopback não foi bloqueado")
	}
}

// TestCheckRedirectRevalidaEsquemaELimitaSaltos exercita CheckRedirect
// diretamente, porque não há como exercitá-lo por dentro de um teste com
// httptest: todo httptest.Server escuta em loopback, então o próprio
// redirecionador já é destino bloqueado e a cadeia morre no salto zero.
func TestCheckRedirectRevalidaEsquemaELimitaSaltos(t *testing.T) {
	client := safedial.NewClient(5 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect é nil: nenhum salto seria revalidado")
	}

	req := func(raw string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("requisição de teste inválida: %v", err)
		}
		return r
	}

	// Um salto legítimo passa.
	if err := client.CheckRedirect(req("https://exemplo.com/x"), nil); err != nil {
		t.Errorf("salto legítimo recusado: %v", err)
	}

	// Um salto para esquema não permitido é recusado antes de discar.
	err := client.CheckRedirect(req("file:///etc/passwd"), nil)
	if !errors.Is(err, safedial.ErrInvalidURL) {
		t.Errorf("salto para file:// deu erro = %v, queria envolver ErrInvalidURL", err)
	}

	// A cadeia é limitada. net/http já inclui a requisição original em via,
	// então via com 3 elementos é o terceiro salto — ainda permitido — e via
	// com 4 é o quarto, que para. Isso faz maxRedirects significar o número
	// de saltos seguidos, como o nome promete.
	via := []*http.Request{
		req("https://exemplo.com/0"),
		req("https://exemplo.com/1"),
		req("https://exemplo.com/2"),
	}
	if err := client.CheckRedirect(req("https://exemplo.com/3"), via); err != nil {
		t.Errorf("terceiro salto recusado: %v", err)
	}

	via = append(via, req("https://exemplo.com/3"))
	err = client.CheckRedirect(req("https://exemplo.com/4"), via)
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("cadeia longa deu erro = %v, queria envolver ErrBlocked", err)
	}
}

func TestTransportDoesNotReuseConnections(t *testing.T) {
	// Reuso de conexão pula o dialer e, com ele, a revalidação do IP. Este
	// teste trava a configuração para que ninguém "otimize" o keep-alive de
	// volta sem perceber a consequência.
	tr := safedial.NewTransport()
	if !tr.DisableKeepAlives {
		t.Error("keep-alive ligado: uma conexão reusada não passa pelo ControlContext")
	}
	if tr.Proxy != nil {
		t.Error("Proxy não é nil: o sondador herdaria HTTP_PROXY do ambiente")
	}
}
```

- [ ] **Step 6: Rodar e confirmar que falha**

Run: `go test ./internal/prober/safedial/ -run 'TestClient|TestRedirect|TestTransport' -v`
Expected: FAIL — `undefined: safedial.NewClient`.

- [ ] **Step 7: Implementar o transporte**

`internal/prober/safedial/transport.go`:

```go
package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// maxRedirects limita a cadeia de redirecionamentos. Três é suficiente para
// uso legítimo (http→https, com→com.br) e curto o bastante para não virar
// ferramenta de varredura.
const maxRedirects = 3

// MaxBodyBytes é quanto do corpo o sondador pode ler. O monitor não precisa
// do conteúdo; ler mais do que isso só criaria canal de exfiltração e pressão
// de memória.
const MaxBodyBytes = 64 << 10

// NewTransport devolve um http.Transport que se recusa a conectar em
// endereços internos.
//
// A validação acontece dentro de ControlContext, que o Go chama depois de
// criar o socket e antes de discar, recebendo o endereço já resolvido.
//
// Validar a URL antes de discar não bastaria: quem controla o DNS do domínio
// faz rebinding — a resolução da validação devolve um IP público e a da
// conexão devolve um privado. A janela entre as duas resoluções é o bug, e
// validar no ControlContext a fecha, porque ali o endereço é exatamente o que
// será conectado.
func NewTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			return AllowAddr(address)
		},
	}

	return &http.Transport{
		DialContext: dialer.DialContext,

		// Sem proxy: HTTP_PROXY no ambiente faria toda requisição passar por
		// um intermediário, e o ControlContext validaria o IP do proxy em vez
		// do IP do alvo.
		Proxy: nil,

		// Sem keep-alive: conexão reusada não passa pelo dialer e, portanto,
		// não é revalidada. Cada sonda também deve medir uma conexão nova
		// para que a latência seja comparável entre execuções.
		DisableKeepAlives: true,

		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

// NewClient monta o cliente completo, com limite de redirecionamentos e
// revalidação de esquema a cada salto.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// net/http inclui a requisição original em via, então a
			// comparação é estrita: com maxRedirects = 3 o cliente segue
			// três saltos e recusa o quarto — a constante significa o que
			// o nome dela diz.
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: mais de %d redirecionamentos", ErrBlocked, maxRedirects)
			}
			// O IP do destino já será revalidado no dial, porque não há
			// reuso de conexão. O que falta checar aqui é o esquema: um
			// redirecionamento para file:// ou gopher:// nunca chega a discar.
			_, err := ValidateURL(req.URL.String())
			return err
		},
	}
}
```

- [ ] **Step 8: Rodar e confirmar que passa**

Run: `go test -race -short ./internal/prober/safedial/ -v`
Expected: PASS em todos. `TestClientCannotReachLoopbackServer` passando é a
prova de que a guarda funciona de ponta a ponta.

- [ ] **Step 9: Commitar**

```bash
git add internal/prober
git commit -m "feat(safedial): guarda anti-SSRF validada no momento da conexão

A validação roda no ControlContext do net.Dialer, que recebe o IP já
resolvido — é a única forma de fechar a janela de DNS rebinding. Unmap antes
de comparar prefixos fecha o bypass ::ffff:127.0.0.1. Keep-alive desligado
porque conexão reusada não passa pelo dialer.

O teste de ponta a ponta prova que o cliente não alcança um httptest.Server
em loopback."
```

---

### Task 5: Hashing de senha com Argon2id

**Files:**
- Create: `internal/auth/password.go`
- Test: `internal/auth/password_test.go`

**Interfaces:**
- Consumes: nada (pacote puro).
- Produces: `auth.HashPassword(plain string) (string, error)`; `auth.VerifyPassword(encoded, plain string) (ok bool, needsRehash bool, err error)`; `auth.VerifyDummy()`; erro `auth.ErrInvalidHash`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/auth/password_test.go`:

```go
package auth_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/auth"
)

const senha = "uma-senha-bem-comprida-e-boa"

func TestHashAndVerifyRoundTrip(t *testing.T) {
	h, err := auth.HashPassword(senha)
	if err != nil {
		t.Fatal(err)
	}

	ok, needsRehash, err := auth.VerifyPassword(h, senha)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a senha correta não foi aceita")
	}
	if needsRehash {
		t.Error("um hash recém-criado não deveria pedir rehash")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	h, _ := auth.HashPassword(senha)

	ok, _, err := auth.VerifyPassword(h, senha+"x")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("senha errada foi aceita")
	}
}

// Salt aleatório por hash impede que duas contas com a mesma senha tenham o
// mesmo registro — e com isso impede tabela arco-íris e comparação entre
// contas em caso de vazamento.
func TestSamePasswordProducesDifferentHashes(t *testing.T) {
	a, _ := auth.HashPassword(senha)
	b, _ := auth.HashPassword(senha)

	if a == b {
		t.Error("dois hashes da mesma senha saíram idênticos: o salt não é aleatório")
	}
}

func TestHashUsesSpecParameters(t *testing.T) {
	h, _ := auth.HashPassword(senha)

	// Formato PHC: $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$`)
	if !re.MatchString(h) {
		t.Errorf("hash fora do formato ou dos parâmetros da spec: %s", h)
	}
}

// Guardar os parâmetros dentro do hash é o que permite subir o custo depois
// sem invalidar as senhas existentes: o login recalcula com os parâmetros
// antigos, valida, e sinaliza rehash.
func TestVerifyFlagsWeakerParametersForRehash(t *testing.T) {
	fraco := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$" +
		mustHashWith(t, senha, 19456, 2, 1)

	ok, needsRehash, err := auth.VerifyPassword(fraco, senha)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("hash com parâmetros antigos deveria continuar validando")
	}
	if !needsRehash {
		t.Error("parâmetros abaixo do padrão atual deveriam pedir rehash")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := map[string]string{
		"vazio":                "",
		"sem cifrão":           "argon2id",
		"algoritmo errado":     "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"versão errada":        "$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"parâmetros ilegíveis": "$argon2id$v=19$m=abc,t=3,p=2$c2FsdA$aGFzaA",
		"salt não é base64":    "$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
		"campos a menos":       "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
	}

	for nome, h := range cases {
		t.Run(nome, func(t *testing.T) {
			if _, _, err := auth.VerifyPassword(h, senha); !errors.Is(err, auth.ErrInvalidHash) {
				t.Errorf("erro = %v, queria ErrInvalidHash", err)
			}
		})
	}
}

// VerifyDummy existe para o login gastar o mesmo tempo quando o e-mail não
// existe. Sem ela, o tempo de resposta vira oráculo de existência de conta
// (spec §5.2). O teste aqui só garante que a função é chamável e não entra em
// pânico; afirmar sobre tempo em teste é receita de flakiness.
func TestVerifyDummyDoesNotPanic(t *testing.T) {
	auth.VerifyDummy()
}

func mustHashWith(t *testing.T, plain string, m, tt uint32, p uint8) string {
	t.Helper()
	h := auth.HashPasswordWith(plain, []byte("saltsaltsaltsa"), m, tt, p)
	parts := strings.Split(h, "$")
	return parts[len(parts)-1]
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./internal/auth/ -v`
Expected: FAIL — `undefined: auth.HashPassword`.

- [ ] **Step 3: Implementar o hashing**

`internal/auth/password.go`:

```go
// Package auth cuida de senha, token de acesso e ciclo de vida de sessão.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Parâmetros da spec §5.2: acima do mínimo OWASP (19 MiB, t=2, p=1).
const (
	argonMemory  uint32 = 64 * 1024 // KiB, ou seja 64 MiB
	argonTime    uint32 = 3
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var ErrInvalidHash = errors.New("hash de senha malformado")

// HashPassword gera um hash no formato PHC, com salt aleatório.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gerar salt: %w", err)
	}
	return HashPasswordWith(plain, salt, argonMemory, argonTime, argonThreads), nil
}

// HashPasswordWith é exportada para que os testes consigam construir hashes
// com parâmetros antigos e exercitar o caminho de rehash. O código de
// produção usa sempre HashPassword.
func HashPasswordWith(plain string, salt []byte, memory, time uint32, threads uint8) string {
	sum := argon2.IDKey([]byte(plain), salt, time, memory, threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	)
}

// VerifyPassword confere a senha e informa se o hash precisa ser refeito.
//
// needsRehash permite subir o custo do Argon2 no futuro sem invalidar senha
// nenhuma: o login valida com os parâmetros gravados, e quem chama regrava o
// hash com os parâmetros atuais.
func VerifyPassword(encoded, plain string) (ok bool, needsRehash bool, err error) {
	memory, time, threads, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}

	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))

	// Comparação em tempo constante: bytes.Equal sai cedo na primeira
	// diferença e vazaria informação por tempo.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	needsRehash = memory < argonMemory || time < argonTime || threads < argonThreads
	return true, needsRehash, nil
}

func decodeHash(encoded string) (memory, time uint32, threads uint8, salt, sum []byte, err error) {
	// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}

	var version int
	if _, e := fmt.Sscanf(parts[2], "v=%d", &version); e != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); e != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if sum, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(sum) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	return memory, time, threads, salt, sum, nil
}

// dummyHash é calculado uma única vez, na primeira chamada. Calcular sob
// demanda evita pagar 64 MiB e três iterações no boot de todo processo,
// inclusive nos que rodam só como --role=prober.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("senha-inexistente-usada-apenas-para-tempo-constante")
	if err != nil {
		panic("auth: não foi possível preparar o hash dummy: " + err.Error())
	}
	return h
})

// VerifyDummy consome o mesmo trabalho de CPU de uma verificação real.
//
// O login chama esta função quando o e-mail não existe, para que a resposta
// custe o mesmo de uma senha errada. Sem isso, o tempo de resposta diz ao
// atacante quais e-mails têm conta (spec §5.2).
func VerifyDummy() {
	_, _, _ = VerifyPassword(dummyHash(), "qualquer-coisa")
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `go test -race -short ./internal/auth/ -v`
Expected: PASS. Cada teste leva ~100 ms porque o Argon2 gasta 64 MiB de
propósito; é o custo funcionando.

- [ ] **Step 5: Commitar**

```bash
git add internal/auth/password.go internal/auth/password_test.go
git commit -m "feat(auth): hashing de senha com Argon2id em formato PHC

Parâmetros gravados no hash permitem subir o custo depois sem invalidar
senhas: VerifyPassword sinaliza needsRehash. VerifyDummy dá ao login o mesmo
custo de CPU quando o e-mail não existe, para que o tempo de resposta não
revele quais contas existem."
```

---

### Task 6: Sessões com rotação e detecção de reúso

O núcleo de segurança do plano. Um refresh token roubado deve ser detectável,
e a detecção deve derrubar o acesso do atacante e do usuário legítimo — porque
não há como distinguir os dois, e pedir novo login é o custo aceitável.

**Files:**
- Create: `internal/clock/clock.go`
- Create: `internal/auth/session.go`
- Create: `internal/auth/store.go`
- Create: `internal/auth/token.go`
- Test: `internal/auth/session_test.go`
- Test: `internal/auth/token_test.go`

**Interfaces:**
- Consumes: `crypto.HashToken` e `crypto.HashIP` (Task 3); `testdb.New` (Task 2); `config.Config.JWTSecret` e `config.Config.IPHashKey` (Task 1).
- Produces:
  - `clock.Clock` (interface com `Now() time.Time`), `clock.Real{}`, `clock.NewFake(t time.Time) *clock.Fake` com `Advance(d time.Duration)`.
  - `auth.Store` (interface) e `auth.NewPGStore(pool *pgxpool.Pool) *auth.PGStore` — o tipo concreto satisfaz `Store`, e ganha `UserStore` na Task 8.
  - `auth.NewManager(store Store, clk clock.Clock, ipKey []byte) *auth.Manager`.
  - `(*Manager).Issue(ctx context.Context, userID uuid.UUID, meta Meta) (token string, s Session, err error)`.
  - `(*Manager).Rotate(ctx context.Context, token string, meta Meta) (string, Session, error)`.
  - `(*Manager).Revoke(ctx context.Context, token string) error`.
  - `auth.Meta{IP string; UserAgent string}`; `auth.Session{ID, UserID, FamilyID uuid.UUID; ExpiresAt time.Time}`.
  - Erros `auth.ErrInvalidSession`, `auth.ErrTokenReuse`, `auth.ErrNotFound`.
  - `auth.NewAccessToken(secret []byte, userID uuid.UUID, now time.Time) (string, error)` e `auth.ParseAccessToken(secret []byte, raw string, now time.Time) (uuid.UUID, error)`.

- [ ] **Step 1: Criar o relógio injetável**

`internal/clock/clock.go`:

```go
// Package clock isola a leitura do tempo para que a lógica dependente de
// relógio seja testável sem time.Sleep.
//
// A spec (§6.2) exige isso para o agendador do Plano 2; as sessões são o
// primeiro consumidor.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake é seguro para uso concorrente porque os testes do agendador vão
// avançar o tempo de uma goroutine enquanto outras o leem.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
```

- [ ] **Step 2: Escrever o teste de sessão que falha**

`internal/auth/session_test.go`:

```go
package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

var ipKey = []byte("chave-de-teste-com-32-bytes-ok!!")

func setup(t *testing.T) (*auth.Manager, *pgxpool.Pool, *clock.Fake, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)

	userID := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		userID, "pessoa@exemplo.com")
	if err != nil {
		t.Fatalf("criar usuário de teste: %v", err)
	}
	return mgr, pool, clk, userID
}

var meta = auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}

func TestIssueThenRotate(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()

	tok1, s1, err := mgr.Issue(ctx, userID, meta)
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == "" {
		t.Fatal("token vazio")
	}

	tok2, s2, err := mgr.Rotate(ctx, tok1, meta)
	if err != nil {
		t.Fatalf("rotação deveria funcionar: %v", err)
	}
	if tok2 == tok1 {
		t.Error("a rotação devolveu o mesmo token")
	}
	if s2.FamilyID != s1.FamilyID {
		t.Error("a rotação mudou de família; deveria manter a mesma")
	}
	if s2.UserID != userID {
		t.Error("a sessão nova aponta para outro usuário")
	}
}

// O coração da defesa. Um token já consumido só reaparece se alguém o copiou.
// Como não há como saber se quem chegou primeiro foi o ladrão ou o dono, a
// resposta é derrubar a família inteira e exigir login novo.
func TestReusedTokenRevokesEntireFamily(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	tok1, _, _ := mgr.Issue(ctx, userID, meta)
	tok2, _, err := mgr.Rotate(ctx, tok1, meta)
	if err != nil {
		t.Fatal(err)
	}

	// O atacante usa o token antigo, que ele copiou antes da rotação.
	if _, _, err := mgr.Rotate(ctx, tok1, meta); !errors.Is(err, auth.ErrTokenReuse) {
		t.Fatalf("erro = %v, queria ErrTokenReuse", err)
	}

	// O token do usuário legítimo também morre: não dá para distinguir.
	if _, _, err := mgr.Rotate(ctx, tok2, meta); err == nil {
		t.Error("o token legítimo continuou valendo depois da detecção de reúso")
	}

	var vivas int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&vivas)
	if err != nil {
		t.Fatal(err)
	}
	if vivas != 0 {
		t.Errorf("%d sessões continuaram vivas; queria 0", vivas)
	}
}

func TestReuseIsRecordedInAuditLog(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	tok1, _, _ := mgr.Issue(ctx, userID, meta)
	_, _, _ = mgr.Rotate(ctx, tok1, meta)
	_, _, _ = mgr.Rotate(ctx, tok1, meta)

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'session.reuse_detected'`,
		userID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d registros de auditoria, queria 1", n)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	mgr, _, clk, userID := setup(t)
	ctx := context.Background()

	tok, _, _ := mgr.Issue(ctx, userID, meta)

	clk.Advance(31 * 24 * time.Hour) // TTL de refresh é 30 dias (spec §5.2)

	if _, _, err := mgr.Rotate(ctx, tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	mgr, _, _, _ := setup(t)

	if _, _, err := mgr.Rotate(context.Background(), "token-que-nunca-existiu", meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

func TestRevokedTokenIsRejected(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()

	tok, _, _ := mgr.Issue(ctx, userID, meta)
	if err := mgr.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}

	if _, _, err := mgr.Rotate(ctx, tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("erro = %v, queria ErrInvalidSession", err)
	}
}

// O IP nunca pode chegar em claro ao banco (spec §4.2).
func TestIPIsStoredHashed(t *testing.T) {
	mgr, pool, _, userID := setup(t)
	ctx := context.Background()

	if _, _, err := mgr.Issue(ctx, userID, auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}); err != nil {
		t.Fatal(err)
	}

	var ipHash []byte
	err := pool.QueryRow(ctx, `SELECT ip_hash FROM sessions WHERE user_id = $1`, userID).Scan(&ipHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(ipHash) != 32 {
		t.Errorf("ip_hash tem %d bytes, queria 32 (HMAC-SHA256)", len(ipHash))
	}
	if string(ipHash) == "203.0.113.9" {
		t.Error("o IP foi gravado em claro")
	}
}
```

- [ ] **Step 3: Rodar e confirmar que falha**

Run: `go test ./internal/auth/ -run TestIssue -v`
Expected: FAIL — `undefined: auth.NewManager`.

- [ ] **Step 4: Definir o store**

`internal/auth/store.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("registro não encontrado")

// SessionRow é a linha crua de sessions.
type SessionRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
	UsedAt    *time.Time
	IPHash    []byte
	UserAgent string
}

// Tx expõe as operações que precisam rodar na mesma transação. A rotação
// inteira é atômica: sem isso, duas rotações simultâneas com o mesmo token
// poderiam ambas passar pela checagem de reúso antes de qualquer uma marcar
// used_at.
type Tx interface {
	LockSessionByHash(ctx context.Context, hash []byte) (SessionRow, error)
	InsertSession(ctx context.Context, r SessionRow) error
	MarkSessionUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error
	RevokeByHash(ctx context.Context, hash []byte, at time.Time) error
	InsertAudit(ctx context.Context, userID uuid.UUID, action string, ipHash []byte, at time.Time) error
}

type Store interface {
	InTx(ctx context.Context, fn func(context.Context, Tx) error) error
}

// PGStore é a implementação em Postgres. Devolver o tipo concreto, e não a
// interface, permite que o mesmo valor satisfaça Store agora e UserStore na
// Task 8 — um consumidor pede a interface de que precisa.
//
// O Plano 2 troca o corpo destes métodos por código gerado pelo sqlc (spec
// §3.3) sem mexer nas interfaces — ver o desvio registrado nas Global
// Constraints deste plano.
type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) InTx(ctx context.Context, fn func(context.Context, Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if err := fn(ctx, &pgTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type pgTx struct{ tx pgx.Tx }

func (t *pgTx) LockSessionByHash(ctx context.Context, hash []byte) (SessionRow, error) {
	var r SessionRow
	err := t.tx.QueryRow(ctx, `
		SELECT id, user_id, family_id, token_hash, expires_at, revoked_at, used_at
		  FROM sessions
		 WHERE token_hash = $1
		   FOR UPDATE`, hash).
		Scan(&r.ID, &r.UserID, &r.FamilyID, &r.TokenHash, &r.ExpiresAt, &r.RevokedAt, &r.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionRow{}, ErrNotFound
	}
	if err != nil {
		return SessionRow{}, fmt.Errorf("buscar sessão: %w", err)
	}
	return r, nil
}

func (t *pgTx) InsertSession(ctx context.Context, r SessionRow) error {
	_, err := t.tx.Exec(ctx, `
		INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, ip_hash, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.ID, r.UserID, r.FamilyID, r.TokenHash, r.ExpiresAt, r.IPHash, r.UserAgent)
	if err != nil {
		return fmt.Errorf("inserir sessão: %w", err)
	}
	return nil
}

func (t *pgTx) MarkSessionUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := t.tx.Exec(ctx, `UPDATE sessions SET used_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("marcar sessão usada: %w", err)
	}
	return nil
}

func (t *pgTx) RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error {
	_, err := t.tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID, at)
	if err != nil {
		return fmt.Errorf("revogar família: %w", err)
	}
	return nil
}

func (t *pgTx) RevokeByHash(ctx context.Context, hash []byte, at time.Time) error {
	_, err := t.tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`,
		hash, at)
	if err != nil {
		return fmt.Errorf("revogar sessão: %w", err)
	}
	return nil
}

func (t *pgTx) InsertAudit(ctx context.Context, userID uuid.UUID, action string, ipHash []byte, at time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	_, err = t.tx.Exec(ctx,
		`INSERT INTO audit_log (id, user_id, action, ip_hash, at) VALUES ($1, $2, $3, $4, $5)`,
		id, userID, action, ipHash, at)
	if err != nil {
		return fmt.Errorf("inserir auditoria: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Implementar o gerenciador de sessão**

`internal/auth/session.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/crypto"
)

// RefreshTTL vem da spec §5.2.
const RefreshTTL = 30 * 24 * time.Hour

// refreshTokenBytes: 32 bytes de crypto/rand, ou seja 256 bits de entropia
// (spec §5.2). É o que torna busca exaustiva inviável e permite guardar
// apenas SHA-256 sem chave.
const refreshTokenBytes = 32

var (
	ErrInvalidSession = errors.New("sessão inválida")
	// ErrTokenReuse indica que um refresh já consumido reapareceu — evidência
	// de roubo. Quem chama deve tratar como incidente de segurança.
	ErrTokenReuse = errors.New("reúso de refresh token detectado")
)

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
}

// Meta descreve de onde a requisição veio. O IP é hasheado antes de tocar o
// banco (spec §4.2).
type Meta struct {
	IP        string
	UserAgent string
}

type Manager struct {
	store Store
	clk   clock.Clock
	ipKey []byte
}

func NewManager(store Store, clk clock.Clock, ipKey []byte) *Manager {
	return &Manager{store: store, clk: clk, ipKey: ipKey}
}

// Issue cria uma família nova. É o caminho do login.
func (m *Manager) Issue(ctx context.Context, userID uuid.UUID, meta Meta) (string, Session, error) {
	familyID, err := uuid.NewV7()
	if err != nil {
		return "", Session{}, fmt.Errorf("gerar família: %w", err)
	}

	var token string
	var s Session
	err = m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		token, s, err = m.insert(ctx, tx, userID, familyID, meta)
		return err
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, s, nil
}

// Rotate troca um refresh válido por um novo, dentro da mesma família.
//
// Toda a operação roda em uma transação com SELECT ... FOR UPDATE. Sem o
// lock, duas rotações simultâneas com o mesmo token poderiam ambas ler
// used_at = NULL e ambas seguir adiante, o que anularia a detecção de reúso.
func (m *Manager) Rotate(ctx context.Context, token string, meta Meta) (string, Session, error) {
	hash := crypto.HashToken(token)
	now := m.clk.Now()

	var newToken string
	var s Session

	err := m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		cur, err := tx.LockSessionByHash(ctx, hash)
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidSession
		}
		if err != nil {
			return err
		}

		if cur.UsedAt != nil {
			// Um token consumido reapareceu. A única explicação é cópia.
			// Não há como saber se quem chegou agora é o ladrão ou o dono,
			// então a família inteira cai e ambos fazem login de novo.
			if err := tx.RevokeFamily(ctx, cur.FamilyID, now); err != nil {
				return err
			}
			if err := tx.InsertAudit(ctx, cur.UserID, "session.reuse_detected",
				crypto.HashIP(m.ipKey, meta.IP), now); err != nil {
				return err
			}
			return ErrTokenReuse
		}
		if cur.RevokedAt != nil || now.After(cur.ExpiresAt) {
			return ErrInvalidSession
		}

		if err := tx.MarkSessionUsed(ctx, cur.ID, now); err != nil {
			return err
		}

		newToken, s, err = m.insert(ctx, tx, cur.UserID, cur.FamilyID, meta)
		return err
	})
	if err != nil {
		return "", Session{}, err
	}
	return newToken, s, nil
}

// Revoke encerra uma sessão específica. É o caminho do logout.
func (m *Manager) Revoke(ctx context.Context, token string) error {
	hash := crypto.HashToken(token)
	now := m.clk.Now()
	return m.store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		return tx.RevokeByHash(ctx, hash, now)
	})
}

func (m *Manager) insert(ctx context.Context, tx Tx, userID, familyID uuid.UUID, meta Meta) (string, Session, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, fmt.Errorf("gerar refresh token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	id, err := uuid.NewV7()
	if err != nil {
		return "", Session{}, fmt.Errorf("gerar id de sessão: %w", err)
	}

	row := SessionRow{
		ID:        id,
		UserID:    userID,
		FamilyID:  familyID,
		TokenHash: crypto.HashToken(token),
		ExpiresAt: m.clk.Now().Add(RefreshTTL),
		IPHash:    crypto.HashIP(m.ipKey, meta.IP),
		UserAgent: meta.UserAgent,
	}
	if err := tx.InsertSession(ctx, row); err != nil {
		return "", Session{}, err
	}

	return token, Session{
		ID:        row.ID,
		UserID:    row.UserID,
		FamilyID:  row.FamilyID,
		ExpiresAt: row.ExpiresAt,
	}, nil
}
```

- [ ] **Step 6: Rodar e confirmar que passa**

Run: `go test -race ./internal/auth/ -v`
Expected: PASS em todos os 7 testes de sessão.

Se `TestReusedTokenRevokesEntireFamily` falhar, parar tudo: a detecção de
reúso é o que torna o roubo de refresh recuperável.

- [ ] **Step 7: Escrever o teste do token de acesso**

`internal/auth/token_test.go`:

```go
package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/auth"
)

var jwtSecret = []byte("segredo-de-teste-com-32-bytes!!!")

func TestAccessTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()

	raw, err := auth.NewAccessToken(jwtSecret, userID, now)
	if err != nil {
		t.Fatal(err)
	}

	got, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got != userID {
		t.Errorf("userID = %s, queria %s", got, userID)
	}
}

func TestAccessTokenExpiresIn15Minutes(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)

	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(14*time.Minute)); err != nil {
		t.Errorf("aos 14 minutos ainda deveria valer: %v", err)
	}
	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(16*time.Minute)); err == nil {
		t.Error("aos 16 minutos deveria ter expirado")
	}
}

func TestAccessTokenRejectsWrongSecret(t *testing.T) {
	now := time.Now()
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)

	if _, err := auth.ParseAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), raw, now); err == nil {
		t.Error("token assinado com outro segredo foi aceito")
	}
}

// "alg: none" é o ataque clássico contra JWT: o atacante troca o algoritmo e
// remove a assinatura. A biblioteca precisa recusar qualquer método que não
// seja o esperado.
func TestAccessTokenRejectsNoneAlgorithm(t *testing.T) {
	forjado := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiIwMTkyYzNkNC1hYmNkLTdlZmYtODAwMC0wMDAwMDAwMDAwMDAifQ."

	if _, err := auth.ParseAccessToken(jwtSecret, forjado, time.Now()); err == nil {
		t.Error("token com alg=none foi aceito")
	}
}
```

- [ ] **Step 8: Implementar o token de acesso**

`internal/auth/token.go`:

```go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AccessTTL vem da spec §5.2. Curto de propósito: o refresh rotacionado é
// quem carrega a sessão longa.
const AccessTTL = 15 * time.Minute

var ErrInvalidToken = errors.New("token de acesso inválido")

func NewAccessToken(secret []byte, userID uuid.UUID, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar token: %w", err)
	}
	return tok, nil
}

func ParseAccessToken(secret []byte, raw string, now time.Time) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims

	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		// Sem esta opção, um token forjado com alg=none seria aceito.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// Relógio injetado para que o teste de expiração não dependa do
		// tempo real da máquina.
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	return id, nil
}
```

- [ ] **Step 9: Rodar tudo e commitar**

Run: `go test -race ./... -v`
Expected: PASS em `config`, `storage`, `crypto`, `safedial` e `auth`.

```bash
git add internal/clock internal/auth
git commit -m "feat(auth): sessões com rotação de refresh e detecção de reúso

A rotação roda em transação com SELECT FOR UPDATE: sem o lock, duas rotações
simultâneas com o mesmo token passariam ambas pela checagem de reúso. Token
já consumido que reaparece derruba a família inteira e vira registro de
auditoria — não dá para distinguir ladrão de dono, e login novo é o custo
aceitável.

O token de acesso recusa explicitamente alg=none."
```

---

### Task 7: Servidor HTTP, middlewares e envelope de erro

**Files:**
- Create: `internal/observability/trace.go`
- Create: `internal/httpapi/errors.go`
- Create: `internal/httpapi/middleware/requestid.go`
- Create: `internal/httpapi/middleware/recover.go`
- Create: `internal/httpapi/middleware/secheaders.go`
- Create: `internal/httpapi/middleware/cors.go`
- Create: `internal/httpapi/middleware/ratelimit.go`
- Create: `internal/httpapi/middleware/bodylimit.go`
- Create: `internal/httpapi/middleware/csrf.go`
- Create: `internal/httpapi/server.go`
- Test: `internal/httpapi/middleware/middleware_test.go`

**Interfaces:**
- Consumes: `config.Config` (Task 1).
- Produces:
  - `observability.WithTraceID(ctx context.Context, id string) context.Context` e `observability.TraceID(ctx context.Context) string`.
  - `httpapi.ErrorCode` (string) com as constantes listadas abaixo; `httpapi.WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode)`; `httpapi.WriteJSON(w http.ResponseWriter, status int, v any)`.
  - `middleware.RequestID(next http.Handler) http.Handler`; `middleware.Recover(next http.Handler) http.Handler`; `middleware.SecurityHeaders(next http.Handler) http.Handler`; `middleware.CORS(origin string) func(http.Handler) http.Handler`; `middleware.RateLimit(perMinute int, burst int) func(http.Handler) http.Handler`; `middleware.BodyLimit(maxBytes int64) func(http.Handler) http.Handler`; `middleware.RequireCustomHeader(next http.Handler) http.Handler`.
  - `httpapi.NewServer(cfg *config.Config, h http.Handler) *http.Server`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/httpapi/middleware/middleware_test.go`:

```go
package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func TestSecurityHeadersArePresent(t *testing.T) {
	h := middleware.SecurityHeaders(http.HandlerFunc(ok))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, queria %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP sem frame-ancestors: %q", csp)
	}
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Error("a CSP não pode conter unsafe-inline (spec §5.7)")
	}
}

// Um panic não pode nunca entregar stack trace ao cliente: o stack revela
// caminhos de arquivo, nomes de pacote e versões.
func TestRecoverHidesTheStackTrace(t *testing.T) {
	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("segredo-no-panic")
	})
	h := middleware.RequestID(middleware.Recover(boom))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, queria 500", rec.Code)
	}
	body := rec.Body.String()
	for _, proibido := range []string{"segredo-no-panic", "goroutine", ".go:"} {
		if strings.Contains(body, proibido) {
			t.Errorf("a resposta vazou %q: %s", proibido, body)
		}
	}

	var env struct {
		Error   struct{ Code, Message string } `json:"error"`
		TraceID string                         `json:"trace_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("resposta não é o envelope esperado: %v", err)
	}
	if env.Error.Code != "internal_error" {
		t.Errorf("code = %q, queria internal_error", env.Error.Code)
	}
	if env.TraceID == "" {
		t.Error("sem trace_id: o erro fica impossível de correlacionar no log")
	}
}

func TestBodyLimitRejectsOversizedPayload(t *testing.T) {
	h := middleware.BodyLimit(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 500)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, queria 413", rec.Code)
	}
}

func TestRateLimitBlocksAfterBurst(t *testing.T) {
	h := middleware.RateLimit(60, 3)(http.HandlerFunc(ok))

	var last int
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("status depois de 10 requisições = %d, queria 429", last)
	}
}

func TestRateLimitIsPerClient(t *testing.T) {
	h := middleware.RateLimit(60, 3)(http.HandlerFunc(ok))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	outro := httptest.NewRequest(http.MethodGet, "/", nil)
	outro.RemoteAddr = "198.51.100.7:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, outro)

	if rec.Code != http.StatusOK {
		t.Errorf("outro cliente recebeu %d; o limite não é por cliente", rec.Code)
	}
}

func TestCORSOnlyEchoesTheConfiguredOrigin(t *testing.T) {
	h := middleware.CORS("https://app.exemplo.com")(http.HandlerFunc(ok))

	permitida := httptest.NewRequest(http.MethodGet, "/", nil)
	permitida.Header.Set("Origin", "https://app.exemplo.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, permitida)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.exemplo.com" {
		t.Errorf("origem permitida devolveu %q", got)
	}

	hostil := httptest.NewRequest(http.MethodGet, "/", nil)
	hostil.Header.Set("Origin", "https://atacante.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hostil)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("origem não permitida recebeu %q; deveria receber vazio", got)
	}
}

// Segunda camada anti-CSRF (spec §5.3): um formulário HTML cross-site não
// consegue definir header customizado.
func TestRequireCustomHeaderOnMutations(t *testing.T) {
	h := middleware.RequireCustomHeader(http.HandlerFunc(ok))

	sem := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sem)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST sem o header devolveu %d, queria 403", rec.Code)
	}

	com := httptest.NewRequest(http.MethodPost, "/", nil)
	com.Header.Set("X-Requested-With", "sentinela")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, com)
	if rec.Code != http.StatusOK {
		t.Errorf("POST com o header devolveu %d, queria 200", rec.Code)
	}

	leitura := httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, leitura)
	if rec.Code != http.StatusOK {
		t.Error("GET não deveria exigir o header")
	}
}
```

Adicionar `"io"` aos imports do arquivo de teste.

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./internal/httpapi/... -v`
Expected: FAIL — pacote `middleware` não existe.

- [ ] **Step 3: Implementar trace ID e envelope de erro**

`internal/observability/trace.go`:

```go
// Package observability concentra logging estruturado e correlação de
// requisições.
package observability

import "context"

type ctxKey struct{}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
```

`internal/httpapi/errors.go`:

```go
// Package httpapi expõe a API HTTP do Sentinela.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/sentinela/internal/observability"
)

type ErrorCode string

const (
	CodeInvalidCredentials ErrorCode = "invalid_credentials"
	CodeInvalidSession     ErrorCode = "invalid_session"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeAccountLocked      ErrorCode = "account_locked"
	CodeNotFound           ErrorCode = "not_found"
	CodeValidation         ErrorCode = "validation_failed"
	CodeRateLimited        ErrorCode = "rate_limited"
	CodePayloadTooLarge    ErrorCode = "payload_too_large"
	CodeForbidden          ErrorCode = "forbidden"
	CodeInternal           ErrorCode = "internal_error"
)

// A mensagem vem sempre desta tabela, nunca do erro subjacente.
//
// Essa é a garantia estrutural da spec §9.1: como WriteError não aceita um
// error como parâmetro, é impossível vazar "pq: duplicate key value violates
// unique constraint" para o cliente, mesmo por descuido.
var errorTable = map[ErrorCode]struct {
	status  int
	message string
}{
	CodeInvalidCredentials: {http.StatusUnauthorized, "E-mail ou senha incorretos."},
	CodeInvalidSession:     {http.StatusUnauthorized, "Sessão inválida ou expirada."},
	CodeUnauthorized:       {http.StatusUnauthorized, "Autenticação necessária."},
	CodeAccountLocked:      {http.StatusTooManyRequests, "Conta temporariamente bloqueada."},
	CodeNotFound:           {http.StatusNotFound, "Recurso não encontrado."},
	CodeValidation:         {http.StatusBadRequest, "Dados inválidos."},
	CodeRateLimited:        {http.StatusTooManyRequests, "Muitas requisições."},
	CodePayloadTooLarge:    {http.StatusRequestEntityTooLarge, "Corpo da requisição grande demais."},
	CodeForbidden:          {http.StatusForbidden, "Operação não permitida."},
	CodeInternal:           {http.StatusInternalServerError, "Erro interno."},
}

type errorBody struct {
	Error struct {
		Code    ErrorCode `json:"code"`
		Message string    `json:"message"`
	} `json:"error"`
	TraceID string `json:"trace_id"`
}

func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode) {
	entry, known := errorTable[code]
	if !known {
		entry = errorTable[CodeInternal]
		code = CodeInternal
	}

	var body errorBody
	body.Error.Code = code
	body.Error.Message = entry.message
	body.TraceID = observability.TraceID(r.Context())

	WriteJSON(w, entry.status, body)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("falha ao escrever resposta", "erro", err)
	}
}
```

- [ ] **Step 4: Implementar os middlewares**

`internal/httpapi/middleware/requestid.go`:

```go
// Package middleware reúne os middlewares HTTP do Sentinela, um por arquivo
// para que cada um seja testado e revisado isoladamente.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/observability"
)

const TraceHeader = "X-Trace-Id"

// RequestID atribui um identificador a cada requisição e o devolve no header.
// É o que liga a mensagem genérica vista pelo cliente ao log detalhado do
// servidor (spec §9.1).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.NewV7()
		trace := id.String()
		if err != nil {
			trace = "sem-trace"
		}
		w.Header().Set(TraceHeader, trace)
		next.ServeHTTP(w, r.WithContext(observability.WithTraceID(r.Context(), trace)))
	})
}
```

`internal/httpapi/middleware/recover.go`:

```go
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/BipoXDXD/sentinela/internal/httpapi"
	"github.com/BipoXDXD/sentinela/internal/observability"
)

// Recover impede que um panic derrube o processo e, principalmente, que o
// stack trace chegue ao cliente: ele revela caminhos de arquivo, nomes de
// pacote e versões de dependência.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic no handler",
					"trace_id", observability.TraceID(r.Context()),
					"rota", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				httpapi.WriteError(w, r, httpapi.CodeInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
```

`internal/httpapi/middleware/secheaders.go`:

```go
package middleware

import "net/http"

// SecurityHeaders aplica os cabeçalhos da spec §5.7.
//
// A CSP não tem unsafe-inline de propósito. O painel React é servido
// separadamente; esta API responde JSON, então a política pode ser a mais
// restritiva possível.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Content-Security-Policy",
			"default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
```

`internal/httpapi/middleware/cors.go`:

```go
package middleware

import "net/http"

// CORS libera exatamente uma origem. Curinga é proibido pela spec §5.7, e
// com credenciais o navegador nem aceitaria.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Origin") == origin {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				// Vary evita que um cache sirva a um site a resposta liberada
				// para outro.
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

`internal/httpapi/middleware/ratelimit.go`:

```go
package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/sentinela/internal/httpapi"
)

// RateLimit aplica token bucket por cliente (spec §5.7).
//
// Os buckets ficam em memória e são varridos periodicamente. Um mapa que só
// cresce seria vazamento de memória e vetor de exaustão: basta o atacante
// variar o IP de origem.
func RateLimit(perMinute, burst int) func(http.Handler) http.Handler {
	type bucket struct {
		lim  *rate.Limiter
		seen time.Time
	}

	var (
		mu      sync.Mutex
		buckets = map[string]*bucket{}
	)

	go func() {
		for range time.Tick(time.Minute) {
			mu.Lock()
			for k, b := range buckets {
				if time.Since(b.seen) > 10*time.Minute {
					delete(buckets, k)
				}
			}
			mu.Unlock()
		}
	}()

	limit := rate.Limit(float64(perMinute) / 60.0)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}

			mu.Lock()
			b, ok := buckets[ip]
			if !ok {
				b = &bucket{lim: rate.NewLimiter(limit, burst)}
				buckets[ip] = b
			}
			b.seen = time.Now()
			allow := b.lim.Allow()
			mu.Unlock()

			if !allow {
				httpapi.WriteError(w, r, httpapi.CodeRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

`internal/httpapi/middleware/bodylimit.go`:

```go
package middleware

import "net/http"

// BodyLimit envolve o corpo com http.MaxBytesReader (spec §5.7). Sem isso,
// uma requisição com corpo gigante consome memória até derrubar o processo.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
```

`internal/httpapi/middleware/csrf.go`:

```go
package middleware

import (
	"net/http"

	"github.com/BipoXDXD/sentinela/internal/httpapi"
)

// CustomHeaderName é o header que toda mutação precisa enviar.
const CustomHeaderName = "X-Requested-With"

// RequireCustomHeader é a segunda camada anti-CSRF da spec §5.3.
//
// O refresh vive em cookie SameSite=Strict, o que já cobre o caso principal.
// Este header cobre o resto: um <form> em site hostil consegue disparar POST
// com o cookie junto, mas não consegue definir header customizado sem passar
// pelo preflight de CORS — que a origem hostil não passa.
func RequireCustomHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(CustomHeaderName) == "" {
				httpapi.WriteError(w, r, httpapi.CodeForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `go test -race -short ./internal/httpapi/... -v`
Expected: PASS nos 7 testes.

- [ ] **Step 6: Montar o servidor**

`internal/httpapi/server.go`:

```go
package httpapi

import (
	"net/http"
	"time"

	"github.com/BipoXDXD/sentinela/internal/config"
)

// NewServer configura o http.Server com todos os timeouts.
//
// ReadHeaderTimeout é o mais importante: sem ele o servidor aceita Slowloris,
// em que o cliente envia headers um byte por minuto e ocupa uma conexão
// indefinidamente até esgotar o servidor (spec §5.7).
func NewServer(cfg *config.Config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}
```

- [ ] **Step 7: Commitar**

```bash
git add internal/observability internal/httpapi
git commit -m "feat(httpapi): servidor, middlewares e envelope de erro

WriteError não aceita um error como parâmetro: a mensagem vem sempre de uma
tabela fixa, o que torna estruturalmente impossível vazar erro de banco para
o cliente. O trace_id liga a mensagem genérica ao log detalhado.

ReadHeaderTimeout definido — sem ele o servidor aceita Slowloris. Os buckets
de rate limit são varridos periodicamente para não virarem vetor de exaustão
de memória."
```

---

### Task 8: Endpoints de autenticação

Fecha o plano: o fluxo completo de login, refresh, logout e rota protegida,
verificável de ponta a ponta contra Postgres real.

**Files:**
- Create: `internal/auth/user.go` (adiciona os métodos de `UserStore` ao `*auth.PGStore` da Task 6)
- Create: `internal/httpapi/authhandlers.go`
- Create: `internal/httpapi/middleware/authn.go`
- Create: `internal/httpapi/routes.go`
- Modify: `cmd/sentinela/main.go`
- Test: `internal/httpapi/auth_integration_test.go`

**Interfaces:**
- Consumes: `auth.Manager` e `auth.Store` (Task 6); `auth.VerifyPassword`, `auth.VerifyDummy`, `auth.HashPassword` (Task 5); todos os middlewares (Task 7); `testdb.New` (Task 2).
- Produces:
  - `auth.User{ID uuid.UUID; Email string; PasswordHash string; FailedAttempts int; LockedUntil *time.Time}`.
  - `auth.UserStore` com `ByEmail(ctx, email string) (User, error)`, `RecordFailure(ctx, userID uuid.UUID, at time.Time) error`, `ResetFailures(ctx, userID uuid.UUID) error`, `UpdatePasswordHash(ctx, userID uuid.UUID, hash string) error`.
  - `*auth.PGStore` (Task 6) passa a satisfazer também `auth.UserStore`.
  - `httpapi.NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, clk clock.Clock, jwtSecret []byte, secureCookie bool) *httpapi.AuthHandlers`.
  - `middleware.Authn(jwtSecret []byte, clk clock.Clock) func(http.Handler) http.Handler`; `middleware.UserID(ctx context.Context) (uuid.UUID, bool)`.
  - `httpapi.Routes(...) http.Handler`.

- [ ] **Step 1: Escrever o teste de integração que falha**

`internal/httpapi/auth_integration_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/config"
	"github.com/BipoXDXD/sentinela/internal/httpapi"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

const (
	testEmail = "pessoa@exemplo.com"
	testPass  = "uma-senha-bem-comprida"
)

func newAPI(t *testing.T) (*httptest.Server, *clock.Fake) {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))

	hash, err := auth.HashPassword(testPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		uuid.New(), testEmail, hash); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		CORSOrigin: "https://app.exemplo.com",
		JWTSecret:  []byte("segredo-de-teste-com-32-bytes!!!"),
		IPHashKey:  []byte("chave-de-teste-com-32-bytes-ok!!"),
	}
	store := auth.NewPGStore(pool)
	mgr := auth.NewManager(store, clk, cfg.IPHashKey)

	srv := httptest.NewServer(httpapi.Routes(cfg, store, mgr, clk))
	t.Cleanup(srv.Close)
	return srv, clk
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "sentinela")

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func clientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func TestFullAuthFlow(t *testing.T) {
	srv, _ := newAPI(t)
	c := clientWithJar(t)

	// Login.
	resp := post(t, c, srv.URL+"/api/v1/auth/login",
		map[string]string{"email": testEmail, "password": testPass})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}

	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	if login.AccessToken == "" {
		t.Fatal("login não devolveu access token")
	}

	// O cookie de refresh precisa estar blindado (spec §5.3).
	var refresh *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "sentinela_refresh" {
			refresh = ck
		}
	}
	if refresh == nil {
		t.Fatal("login não devolveu cookie de refresh")
	}
	if !refresh.HttpOnly {
		t.Error("cookie sem HttpOnly: XSS conseguiria lê-lo")
	}
	if refresh.SameSite != http.SameSiteStrictMode {
		t.Error("cookie sem SameSite=Strict")
	}
	if refresh.Path != "/api/v1/auth" {
		t.Errorf("Path do cookie = %q, queria /api/v1/auth", refresh.Path)
	}

	// Rota protegida com o access token.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+login.AccessToken)
	me, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Errorf("/me devolveu %d, queria 200", me.StatusCode)
	}

	// Refresh: o cookie vai sozinho pelo jar.
	r2 := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("refresh devolveu %d", r2.StatusCode)
	}

	// Logout.
	r3 := post(t, c, srv.URL+"/api/v1/auth/logout", nil)
	defer r3.Body.Close()
	if r3.StatusCode != http.StatusNoContent {
		t.Errorf("logout devolveu %d, queria 204", r3.StatusCode)
	}

	// Depois do logout o refresh não vale mais.
	r4 := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	defer r4.Body.Close()
	if r4.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh depois do logout devolveu %d, queria 401", r4.StatusCode)
	}
}

// E-mail inexistente e senha errada precisam ser indistinguíveis na resposta,
// senão o endpoint vira oráculo de quais contas existem (spec §5.2).
func TestLoginDoesNotRevealWhetherAccountExists(t *testing.T) {
	srv, _ := newAPI(t)
	c := clientWithJar(t)

	inexistente := post(t, c, srv.URL+"/api/v1/auth/login",
		map[string]string{"email": "ninguem@exemplo.com", "password": testPass})
	defer inexistente.Body.Close()

	senhaErrada := post(t, c, srv.URL+"/api/v1/auth/login",
		map[string]string{"email": testEmail, "password": "errada"})
	defer senhaErrada.Body.Close()

	if inexistente.StatusCode != senhaErrada.StatusCode {
		t.Errorf("status diferentes: %d vs %d", inexistente.StatusCode, senhaErrada.StatusCode)
	}

	var a, b map[string]any
	json.NewDecoder(inexistente.Body).Decode(&a)
	json.NewDecoder(senhaErrada.Body).Decode(&b)

	ea, _ := a["error"].(map[string]any)
	eb, _ := b["error"].(map[string]any)
	if ea["code"] != eb["code"] || ea["message"] != eb["message"] {
		t.Errorf("respostas distinguíveis: %v vs %v", ea, eb)
	}
}

func TestRefreshWithoutCookieIsRejected(t *testing.T) {
	srv, _ := newAPI(t)
	resp := post(t, clientWithJar(t), srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, queria 401", resp.StatusCode)
	}
}

func TestProtectedRouteRequiresToken(t *testing.T) {
	srv, _ := newAPI(t)

	resp, err := http.Get(srv.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, queria 401", resp.StatusCode)
	}
}

func TestMutationWithoutCustomHeaderIsRejected(t *testing.T) {
	srv, _ := newAPI(t)

	body, _ := json.Marshal(map[string]string{"email": testEmail, "password": testPass})
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, queria 403", resp.StatusCode)
	}
}
```

Adicionar `"net/http/cookiejar"` aos imports.

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./internal/httpapi/ -run TestFullAuthFlow -v`
Expected: FAIL — `undefined: httpapi.Routes`.

- [ ] **Step 3: Implementar o `UserStore`**

`internal/auth/user.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockThreshold e LockDuration implementam o bloqueio progressivo da
// spec §5.2.
const (
	LockThreshold = 5
	LockDuration  = 15 * time.Minute
)

type User struct {
	ID             uuid.UUID
	Email          string
	PasswordHash   string
	FailedAttempts int
	LockedUntil    *time.Time
}

func (u User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

type UserStore interface {
	ByEmail(ctx context.Context, email string) (User, error)
	RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error
	ResetFailures(ctx context.Context, userID uuid.UUID) error
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error
}

func (s *PGStore) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until
		  FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}

// RecordFailure incrementa o contador e aplica o bloqueio no mesmo UPDATE.
// Fazer as duas coisas em uma instrução evita corrida entre tentativas
// simultâneas de força bruta.
func (s *PGStore) RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET failed_attempts = failed_attempts + 1,
		       locked_until = CASE
		           WHEN failed_attempts + 1 >= $2 THEN $3
		           ELSE locked_until
		       END
		 WHERE id = $1`,
		userID, LockThreshold, at.Add(LockDuration))
	if err != nil {
		return fmt.Errorf("registrar falha de login: %w", err)
	}
	return nil
}

func (s *PGStore) ResetFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("zerar falhas de login: %w", err)
	}
	return nil
}

func (s *PGStore) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
	if err != nil {
		return fmt.Errorf("atualizar hash de senha: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Implementar os handlers**

`internal/httpapi/authhandlers.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
	"github.com/BipoXDXD/sentinela/internal/observability"
)

const refreshCookieName = "sentinela_refresh"

type AuthHandlers struct {
	users        auth.UserStore
	sessions     *auth.Manager
	clk          clock.Clock
	jwtSecret    []byte
	secureCookie bool
}

func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, clk clock.Clock, jwtSecret []byte, secureCookie bool) *AuthHandlers {
	return &AuthHandlers{users: users, sessions: sessions, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// Login faz o mesmo trabalho de CPU exista ou não a conta.
//
// Quando o e-mail não existe, VerifyDummy roda um Argon2id completo. Sem
// isso, a resposta para e-mail inexistente voltaria em microssegundos e a
// resposta para senha errada em ~100 ms, e a diferença enumeraria as contas
// do sistema (spec §5.2).
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, CodeValidation)
		return
	}
	now := h.clk.Now()

	user, err := h.users.ByEmail(r.Context(), req.Email)
	if errors.Is(err, auth.ErrNotFound) {
		auth.VerifyDummy()
		WriteError(w, r, CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}

	if user.IsLocked(now) {
		auth.VerifyDummy()
		WriteError(w, r, CodeAccountLocked)
		return
	}

	ok, needsRehash, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		h.internal(w, r, "verificar senha", err)
		return
	}
	if !ok {
		if err := h.users.RecordFailure(r.Context(), user.ID, now); err != nil {
			slog.Error("registrar falha de login", "erro", err)
		}
		WriteError(w, r, CodeInvalidCredentials)
		return
	}

	if needsRehash {
		if novo, err := auth.HashPassword(req.Password); err == nil {
			if err := h.users.UpdatePasswordHash(r.Context(), user.ID, novo); err != nil {
				slog.Error("regravar hash de senha", "erro", err)
			}
		}
	}
	if err := h.users.ResetFailures(r.Context(), user.ID); err != nil {
		slog.Error("zerar falhas de login", "erro", err)
	}

	h.issue(w, r, user.ID, now)
}

func (h *AuthHandlers) Refresh(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(refreshCookieName)
	if err != nil {
		WriteError(w, r, CodeInvalidSession)
		return
	}

	token, s, err := h.sessions.Rotate(r.Context(), ck.Value, h.meta(r))
	switch {
	case errors.Is(err, auth.ErrTokenReuse):
		// A família já foi revogada dentro de Rotate. Limpar o cookie evita
		// que o navegador continue reenviando um token morto.
		slog.Warn("reúso de refresh token detectado",
			"trace_id", observability.TraceID(r.Context()))
		h.clearCookie(w)
		WriteError(w, r, CodeInvalidSession)
		return
	case errors.Is(err, auth.ErrInvalidSession):
		h.clearCookie(w)
		WriteError(w, r, CodeInvalidSession)
		return
	case err != nil:
		h.internal(w, r, "rotacionar sessão", err)
		return
	}

	h.setCookie(w, token, s.ExpiresAt)
	h.writeAccess(w, r, s.UserID)
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.sessions.Revoke(r.Context(), ck.Value); err != nil {
			slog.Error("revogar sessão", "erro", err)
		}
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthorized)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"id": userID.String()})
}

func (h *AuthHandlers) issue(w http.ResponseWriter, r *http.Request, userID uuid.UUID, now time.Time) {
	token, s, err := h.sessions.Issue(r.Context(), userID, h.meta(r))
	if err != nil {
		h.internal(w, r, "emitir sessão", err)
		return
	}
	h.setCookie(w, token, s.ExpiresAt)
	h.writeAccess(w, r, userID)
}

func (h *AuthHandlers) writeAccess(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	access, err := auth.NewAccessToken(h.jwtSecret, userID, h.clk.Now())
	if err != nil {
		h.internal(w, r, "assinar access token", err)
		return
	}
	WriteJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access,
		ExpiresIn:   int(auth.AccessTTL.Seconds()),
	})
}

func (h *AuthHandlers) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:  refreshCookieName,
		Value: token,
		// Path restrito: o cookie só acompanha as rotas de autenticação,
		// então não é enviado em nenhuma outra requisição da aplicação.
		Path:     "/api/v1/auth",
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandlers) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandlers) meta(r *http.Request) auth.Meta {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return auth.Meta{IP: ip, UserAgent: r.UserAgent()}
}

// internal registra o erro real e devolve ao cliente apenas o envelope
// genérico com o trace_id (spec §9.1).
func (h *AuthHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	WriteError(w, r, CodeInternal)
}
```

Adicionar `"github.com/google/uuid"` aos imports.

- [ ] **Step 5: Implementar o middleware de autenticação**

`internal/httpapi/middleware/authn.go`:

```go
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/httpapi"
)

type userKey struct{}

// UserID recupera o usuário autenticado. O segundo retorno é false quando a
// rota não passou por Authn — um handler protegido deve tratar isso como 401,
// nunca assumir presença.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

func Authn(jwtSecret []byte, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				httpapi.WriteError(w, r, httpapi.CodeUnauthorized)
				return
			}

			userID, err := auth.ParseAccessToken(jwtSecret, raw, clk.Now())
			if err != nil {
				httpapi.WriteError(w, r, httpapi.CodeUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 6: Montar as rotas**

`internal/httpapi/routes.go`:

```go
package httpapi

import (
	"net/http"
	"strings"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/config"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

// maxBodyBytes cobre com folga qualquer payload legítimo desta API.
const maxBodyBytes = 1 << 20 // 1 MiB

func Routes(cfg *config.Config, store *auth.PGStore, sessions *auth.Manager, clk clock.Clock) http.Handler {
	secureCookie := strings.HasPrefix(cfg.PublicBaseURL, "https://")
	h := NewAuthHandlers(store, sessions, clk, cfg.JWTSecret, secureCookie)
	authn := middleware.Authn(cfg.JWTSecret, clk)

	mux := http.NewServeMux()

	// Rotas de autenticação: limite mais apertado, porque é onde a força
	// bruta bate.
	mux.Handle("POST /api/v1/auth/login",
		middleware.RateLimit(10, 5)(http.HandlerFunc(h.Login)))
	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(h.Logout))

	mux.Handle("GET /api/v1/me", authn(http.HandlerFunc(h.Me)))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// A ordem importa: RequestID primeiro, para que todo log e todo envelope
	// de erro abaixo dele tenham trace_id; Recover logo em seguida, para
	// capturar panic de qualquer middleware posterior.
	var handler http.Handler = mux
	handler = middleware.RequireCustomHeader(handler)
	handler = middleware.BodyLimit(maxBodyBytes)(handler)
	handler = middleware.RateLimit(120, 30)(handler)
	handler = middleware.CORS(cfg.CORSOrigin)(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(handler)
	handler = middleware.RequestID(handler)

	return handler
}
```

- [ ] **Step 7: Rodar o teste de integração e confirmar que passa**

Run: `go test -race ./internal/httpapi/ -v`
Expected: PASS nos 5 testes de integração.

- [ ] **Step 8: Ligar tudo no `main.go`**

Substituir o corpo de `run` em `cmd/sentinela/main.go`:

```go
func run(ctx context.Context, cfg *config.Config) error {
	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if !cfg.Role.RunsAPI() {
		// O papel prober ganha seu loop no Plano 2. Por ora só mantém o
		// processo vivo até o sinal de encerramento.
		<-ctx.Done()
		return nil
	}

	clk := clock.Real{}
	store := auth.NewPGStore(pool)
	sessions := auth.NewManager(store, clk, cfg.IPHashKey)

	srv := httpapi.NewServer(cfg, httpapi.Routes(cfg, store, sessions, clk))

	errCh := make(chan error, 1)
	go func() {
		slog.Info("servindo HTTP", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Shutdown gracioso com prazo (spec §6.2): conexões em voo terminam, e o
	// processo não fica preso caso alguma não termine.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
```

Adicionar aos imports: `"errors"`, `"net/http"`, e os pacotes `auth`, `clock`,
`httpapi`, `storage`.

- [ ] **Step 9: Verificar o binário de ponta a ponta**

```bash
docker run -d --name sentinela-pg -e POSTGRES_PASSWORD=dev -p 5432:5432 postgres:16-alpine

export DATABASE_URL="postgres://postgres:dev@localhost:5432/postgres?sslmode=disable"
export PUBLIC_BASE_URL="http://localhost:8080"
export CORS_ORIGIN="http://localhost:5173"
export MASTER_KEY=$(head -c 32 /dev/urandom | base64)
export JWT_SECRET=$(head -c 32 /dev/urandom | base64)
export IP_HASH_KEY=$(head -c 32 /dev/urandom | base64)

make run
```

Em outro terminal:

```bash
curl -i http://localhost:8080/healthz
```

Expected: `200 OK`, com `X-Trace-Id`, `Content-Security-Policy` e
`X-Content-Type-Options: nosniff` nos headers.

```bash
curl -i -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.c","password":"x"}'
```

Expected: `403` — a mutação não enviou `X-Requested-With`. Repetir com
`-H 'X-Requested-With: sentinela'` e esperar `401 invalid_credentials`.

- [ ] **Step 10: Rodar a suíte completa e commitar**

Run: `make lint && make sec && make test`
Expected: tudo verde.

```bash
git add internal cmd
git commit -m "feat(auth): endpoints de login, refresh, logout e rota protegida

Login gasta o mesmo tempo de CPU exista ou não a conta: VerifyDummy roda um
Argon2id completo quando o e-mail não é encontrado, para que o tempo de
resposta não enumere as contas do sistema.

Refresh vive em cookie HttpOnly com Path restrito às rotas de autenticação.
Reúso detectado limpa o cookie além de revogar a família."
```

---

### Task 9: Segundo fator (TOTP)

Exigido pela spec §5.2 e pelo cronograma dos dias 3–4. É o primeiro consumidor
real do `crypto.Box` da Task 3: o segredo TOTP é a credencial mais sensível do
sistema depois da senha, e precisa estar cifrado em repouso.

**Files:**
- Create: `internal/storage/migrations/00002_totp.sql`
- Create: `internal/auth/totp.go`
- Create: `internal/httpapi/totphandlers.go`
- Modify: `internal/httpapi/authhandlers.go` (desvio do login quando há TOTP ativo)
- Modify: `internal/httpapi/routes.go`
- Test: `internal/auth/totp_test.go`
- Test: `internal/httpapi/totp_integration_test.go`

**Interfaces:**
- Consumes: `crypto.Box`, `crypto.AAD`, `crypto.HashToken` (Task 3); `auth.Manager` (Task 6); `AuthHandlers` (Task 8).
- Produces:
  - `auth.TOTP` com `Enroll(ctx, userID uuid.UUID, email string) (uri string, err error)`, `Confirm(ctx, userID uuid.UUID, code string, now time.Time) ([]string, error)`, `Verify(ctx, userID uuid.UUID, code string, now time.Time) error`, `IsEnabled(ctx, userID uuid.UUID) (bool, error)`.
  - `auth.NewChallengeToken(secret []byte, userID uuid.UUID, now time.Time) (string, error)` e `auth.ParseChallengeToken(secret []byte, raw string, now time.Time) (uuid.UUID, error)`.
  - Erros `auth.ErrTOTPInvalid`, `auth.ErrTOTPNotEnrolled`.

- [ ] **Step 1: Adicionar a dependência**

```bash
go get github.com/pquerna/otp@latest
```

- [ ] **Step 2: Escrever a migration**

`internal/storage/migrations/00002_totp.sql`:

```sql
-- +goose Up
-- O segredo só vale depois de confirmado: cadastrar sem confirmar não pode
-- trancar ninguém para fora da própria conta.
ALTER TABLE users ADD COLUMN totp_confirmed_at timestamptz;

-- Códigos de recuperação são de uso único: a linha é apagada ao ser usada.
-- Guardar o hash, e não o código, mantém a coluna inútil em caso de vazamento.
CREATE TABLE totp_recovery_codes (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX totp_recovery_user_idx ON totp_recovery_codes (user_id);
CREATE UNIQUE INDEX totp_recovery_hash_idx ON totp_recovery_codes (code_hash);

-- +goose Down
DROP TABLE totp_recovery_codes;
ALTER TABLE users DROP COLUMN totp_confirmed_at;
```

- [ ] **Step 3: Escrever o teste que falha**

`internal/auth/totp_test.go`:

```go
package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/crypto"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

func setupTOTP(t *testing.T) (*auth.TOTP, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)

	box, err := crypto.NewBox(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		userID, "totp@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return auth.NewTOTP(pool, box, "Sentinela"), pool, userID
}

// secretFromURI extrai o segredo base32 da URI otpauth:// para que o teste
// consiga gerar um código válido, como um app autenticador faria.
func secretFromURI(t *testing.T, uri string) string {
	t.Helper()
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	return key.Secret()
}

func TestEnrollThenConfirm(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}

	// Antes de confirmar, o TOTP não pode estar ativo — senão um cadastro
	// interrompido trancaria o usuário para fora da própria conta.
	enabled, err := svc.IsEnabled(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("TOTP ficou ativo antes da confirmação")
	}

	code, err := totp.GenerateCode(secretFromURI(t, uri), now)
	if err != nil {
		t.Fatal(err)
	}

	recovery, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatalf("confirmar: %v", err)
	}
	if len(recovery) != 10 {
		t.Errorf("%d códigos de recuperação, queria 10", len(recovery))
	}

	if enabled, _ := svc.IsEnabled(ctx, userID); !enabled {
		t.Error("TOTP deveria estar ativo depois da confirmação")
	}

	// O segredo nunca pode estar em claro no banco.
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT totp_secret_enc FROM users WHERE id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(secretFromURI(t, uri))) {
		t.Error("o segredo TOTP foi gravado em claro")
	}
}

func TestVerifyRejectsWrongCode(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, _ := svc.Enroll(ctx, userID, "totp@exemplo.com")
	code, _ := totp.GenerateCode(secretFromURI(t, uri), now)
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("erro = %v, queria ErrTOTPInvalid", err)
	}
}

// Skew de 1 período aceita o código anterior e o seguinte. Sem tolerância,
// relógio de celular alguns segundos fora já reprova o usuário; com tolerância
// alta demais, a janela de reúso do código cresce.
func TestVerifyToleratesOnePeriodOfClockSkew(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, _ := svc.Enroll(ctx, userID, "totp@exemplo.com")
	code, _ := totp.GenerateCode(secretFromURI(t, uri), now)
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
		t.Fatal(err)
	}

	anterior, _ := totp.GenerateCode(secretFromURI(t, uri), now.Add(-30*time.Second))
	if err := svc.Verify(ctx, userID, anterior, now); err != nil {
		t.Errorf("código do período anterior deveria ser aceito: %v", err)
	}

	distante, _ := totp.GenerateCode(secretFromURI(t, uri), now.Add(-5*time.Minute))
	if err := svc.Verify(ctx, userID, distante, now); err == nil {
		t.Error("código de 5 minutos atrás não deveria ser aceito")
	}
}

func TestRecoveryCodeWorksExactlyOnce(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, _ := svc.Enroll(ctx, userID, "totp@exemplo.com")
	code, _ := totp.GenerateCode(secretFromURI(t, uri), now)
	recovery, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, recovery[0], now); err != nil {
		t.Fatalf("primeiro uso do código de recuperação deveria funcionar: %v", err)
	}
	if err := svc.Verify(ctx, userID, recovery[0], now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Error("o código de recuperação foi aceito duas vezes")
	}
	if err := svc.Verify(ctx, userID, recovery[1], now); err != nil {
		t.Errorf("os outros códigos deveriam continuar válidos: %v", err)
	}
}

func TestVerifyFailsWhenNotEnrolled(t *testing.T) {
	svc, _, userID := setupTOTP(t)

	if err := svc.Verify(context.Background(), userID, "123456", time.Now()); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("erro = %v, queria ErrTOTPNotEnrolled", err)
	}
}
```

Adicionar aos imports do arquivo de teste: `"bytes"` e `"github.com/jackc/pgx/v5/pgxpool"`.

- [ ] **Step 4: Rodar e confirmar que falha**

Run: `go test ./internal/auth/ -run TestEnroll -v`
Expected: FAIL — `undefined: auth.NewTOTP`.

- [ ] **Step 5: Implementar o serviço de TOTP**

`internal/auth/totp.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

var (
	ErrTOTPInvalid      = errors.New("código de segundo fator inválido")
	ErrTOTPNotEnrolled  = errors.New("segundo fator não cadastrado")
)

const (
	totpPeriod        = 30
	// Skew de 1 aceita o código do período anterior e do seguinte, cobrindo
	// relógio de celular levemente fora. Valores maiores alargam demais a
	// janela em que um código capturado continua servindo.
	totpSkew          = 1
	recoveryCodeCount = 10
	recoveryCodeBytes = 10
)

type TOTP struct {
	pool   *pgxpool.Pool
	box    *crypto.Box
	issuer string
}

func NewTOTP(pool *pgxpool.Pool, box *crypto.Box, issuer string) *TOTP {
	return &TOTP{pool: pool, box: box, issuer: issuer}
}

// Enroll gera um segredo novo e o grava cifrado, ainda não confirmado.
//
// Gravar sem ativar é deliberado: se o usuário fechar a tela no meio do
// cadastro, a conta continua acessível com senha apenas.
func (s *TOTP) Enroll(ctx context.Context, userID uuid.UUID, email string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: email,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // exigido por Google Authenticator e afins
	})
	if err != nil {
		return "", fmt.Errorf("gerar chave TOTP: %w", err)
	}

	sealed, err := s.box.Seal([]byte(key.Secret()), crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", fmt.Errorf("cifrar segredo TOTP: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_confirmed_at = NULL WHERE id = $1`,
		userID, sealed)
	if err != nil {
		return "", fmt.Errorf("gravar segredo TOTP: %w", err)
	}

	return key.URL(), nil
}

// Confirm ativa o segundo fator e devolve os códigos de recuperação. Eles são
// mostrados uma única vez: o banco guarda apenas o hash.
func (s *TOTP) Confirm(ctx context.Context, userID uuid.UUID, code string, now time.Time) ([]string, error) {
	secret, _, err := s.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !validate(code, secret, now) {
		return nil, ErrTOTPInvalid
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		raw := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("gerar código de recuperação: %w", err)
		}
		codes[i] = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET totp_confirmed_at = $2 WHERE id = $1`, userID, now); err != nil {
		return nil, fmt.Errorf("ativar TOTP: %w", err)
	}
	// Recadastro invalida os códigos anteriores.
	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("limpar códigos antigos: %w", err)
	}
	for _, c := range codes {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("gerar id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
			id, userID, crypto.HashToken(c)); err != nil {
			return nil, fmt.Errorf("gravar código de recuperação: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirmar TOTP: %w", err)
	}
	return codes, nil
}

// Verify aceita tanto um código do app quanto um código de recuperação. O de
// recuperação é consumido no momento do uso.
func (s *TOTP) Verify(ctx context.Context, userID uuid.UUID, code string, now time.Time) error {
	secret, confirmed, err := s.load(ctx, userID)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrTOTPNotEnrolled
	}

	if validate(code, secret, now) {
		return nil
	}

	// DELETE ... RETURNING resolve o consumo do código em uma única
	// instrução: dois usos simultâneos do mesmo código não podem ambos
	// encontrar a linha.
	var id uuid.UUID
	err = s.pool.QueryRow(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1 AND code_hash = $2 RETURNING id`,
		userID, crypto.HashToken(code)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTOTPInvalid
	}
	if err != nil {
		return fmt.Errorf("consumir código de recuperação: %w", err)
	}
	return nil
}

func (s *TOTP) IsEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("consultar TOTP: %w", err)
	}
	return confirmed != nil, nil
}

func (s *TOTP) load(ctx context.Context, userID uuid.UUID) (secret string, confirmed bool, err error) {
	var sealed []byte
	var at *time.Time

	err = s.pool.QueryRow(ctx,
		`SELECT totp_secret_enc, totp_confirmed_at FROM users WHERE id = $1`, userID).
		Scan(&sealed, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("carregar TOTP: %w", err)
	}
	if len(sealed) == 0 {
		return "", false, ErrTOTPNotEnrolled
	}

	plain, err := s.box.Open(sealed, crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", false, fmt.Errorf("decifrar segredo TOTP: %w", err)
	}
	return string(plain.Reveal()), at != nil, nil
}

func validate(code, secret string, now time.Time) bool {
	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}
```

- [ ] **Step 6: Implementar o token de desafio**

Acrescentar ao final de `internal/auth/token.go`:

```go
// challengeAudience separa o token de desafio do access token. Sem essa
// separação, o token entregue após a senha — antes do segundo fator — valeria
// como credencial completa, e o TOTP viraria decoração.
const challengeAudience = "totp-challenge"

// ChallengeTTL é curto: é só o tempo de digitar seis dígitos.
const ChallengeTTL = 5 * time.Minute

func NewChallengeToken(secret []byte, userID uuid.UUID, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		Audience:  jwt.ClaimStrings{challengeAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ChallengeTTL)),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar desafio: %w", err)
	}
	return tok, nil
}

func ParseChallengeToken(secret []byte, raw string, now time.Time) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims

	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
		// Sem esta checagem, um access token comum seria aceito como desafio
		// e vice-versa.
		jwt.WithAudience(challengeAudience),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	return id, nil
}
```

Também acrescentar a checagem simétrica no `ParseAccessToken`, para que um
desafio não passe como access token. Substituir a chamada `jwt.ParseWithClaims`
de `ParseAccessToken` por:

```go
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err == nil && len(claims.Audience) > 0 {
		// Access token não carrega audience; se veio uma, é um desafio.
		return uuid.Nil, fmt.Errorf("%w: audience inesperada", ErrInvalidToken)
	}
```

- [ ] **Step 7: Desviar o login quando o TOTP está ativo**

Em `internal/httpapi/authhandlers.go`, adicionar o campo `totp *auth.TOTP` a
`AuthHandlers` e ao construtor, e substituir a chamada final `h.issue(...)` de
`Login` por:

```go
	enabled, err := h.totp.IsEnabled(r.Context(), user.ID)
	if err != nil {
		h.internal(w, r, "consultar segundo fator", err)
		return
	}
	if enabled {
		challenge, err := auth.NewChallengeToken(h.jwtSecret, user.ID, now)
		if err != nil {
			h.internal(w, r, "assinar desafio", err)
			return
		}
		// Nenhum cookie de refresh é emitido aqui: a sessão só nasce depois
		// do segundo fator.
		WriteJSON(w, http.StatusOK, map[string]any{
			"totp_required": true,
			"challenge":     challenge,
		})
		return
	}

	h.issue(w, r, user.ID, now)
```

- [ ] **Step 8: Implementar os handlers de TOTP**

`internal/httpapi/totphandlers.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

type codeRequest struct {
	Code string `json:"code"`
}

// TOTPEnroll exige sessão ativa: cadastrar segundo fator é operação de conta
// já autenticada.
func (h *AuthHandlers) TOTPEnroll(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthorized)
		return
	}

	email, err := h.users.EmailByID(r.Context(), userID)
	if err != nil {
		h.internal(w, r, "buscar e-mail", err)
		return
	}

	uri, err := h.totp.Enroll(r.Context(), userID, email)
	if err != nil {
		h.internal(w, r, "cadastrar TOTP", err)
		return
	}
	// A URI otpauth:// vai para o cliente, que renderiza o QR code. Gerar a
	// imagem no servidor só acrescentaria payload.
	WriteJSON(w, http.StatusOK, map[string]string{"otpauth_uri": uri})
}

func (h *AuthHandlers) TOTPConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthorized)
		return
	}

	var req codeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, CodeValidation)
		return
	}

	codes, err := h.totp.Confirm(r.Context(), userID, req.Code, h.clk.Now())
	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		WriteError(w, r, CodeValidation)
		return
	}
	if err != nil {
		h.internal(w, r, "confirmar TOTP", err)
		return
	}

	// Única vez em que os códigos aparecem em claro.
	WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// TOTPVerify fecha o login em dois passos: consome o desafio e emite a sessão.
func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, CodeValidation)
		return
	}
	now := h.clk.Now()

	userID, err := auth.ParseChallengeToken(h.jwtSecret, req.Challenge, now)
	if err != nil {
		WriteError(w, r, CodeUnauthorized)
		return
	}

	if err := h.totp.Verify(r.Context(), userID, req.Code, now); err != nil {
		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
			WriteError(w, r, CodeInvalidCredentials)
			return
		}
		h.internal(w, r, "verificar TOTP", err)
		return
	}

	h.issue(w, r, userID, now)
}
```

Acrescentar a `auth.UserStore` e a `*auth.PGStore`:

```go
func (s *PGStore) EmailByID(ctx context.Context, userID uuid.UUID) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("buscar e-mail: %w", err)
	}
	return email, nil
}
```

- [ ] **Step 9: Registrar as rotas**

Em `internal/httpapi/routes.go`, acrescentar antes da montagem da cadeia:

```go
	mux.Handle("POST /api/v1/auth/totp/enroll", authn(http.HandlerFunc(h.TOTPEnroll)))
	mux.Handle("POST /api/v1/auth/totp/confirm", authn(http.HandlerFunc(h.TOTPConfirm)))
	// Verify não passa por authn: o usuário ainda não tem sessão, só o desafio.
	mux.Handle("POST /api/v1/auth/totp/verify",
		middleware.RateLimit(20, 5)(http.HandlerFunc(h.TOTPVerify)))
```

E passar o `*auth.TOTP` construído no `main.go` para `Routes` e daí para
`NewAuthHandlers`.

- [ ] **Step 10: Escrever o teste de integração do login em dois passos**

`internal/httpapi/totp_integration_test.go`:

```go
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Prova a propriedade que importa: com TOTP ativo, senha correta sozinha não
// entrega sessão nenhuma.
func TestLoginWithTOTPRequiresSecondFactor(t *testing.T) {
	srv, clk := newAPI(t)
	c := clientWithJar(t)

	// Login simples para obter access token e cadastrar o segundo fator.
	resp := post(t, c, srv.URL+"/api/v1/auth/login",
		map[string]string{"email": testEmail, "password": testPass})
	var first struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&first)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/totp/enroll", nil)
	req.Header.Set("Authorization", "Bearer "+first.AccessToken)
	req.Header.Set("X-Requested-With", "sentinela")
	er, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var enroll struct {
		URI string `json:"otpauth_uri"`
	}
	json.NewDecoder(er.Body).Decode(&enroll)
	er.Body.Close()

	key, err := otp.NewKeyFromURL(enroll.URI)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(key.Secret(), clk.Now())

	cr, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/totp/confirm",
		jsonBody(t, map[string]string{"code": code}))
	cr.Header.Set("Authorization", "Bearer "+first.AccessToken)
	cr.Header.Set("X-Requested-With", "sentinela")
	cr.Header.Set("Content-Type", "application/json")
	confirm, err := c.Do(cr)
	if err != nil {
		t.Fatal(err)
	}
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirmação devolveu %d", confirm.StatusCode)
	}
	confirm.Body.Close()

	// Agora o login com senha correta não pode mais entregar sessão.
	segundo := post(t, c, srv.URL+"/api/v1/auth/login",
		map[string]string{"email": testEmail, "password": testPass})
	defer segundo.Body.Close()

	var challenge struct {
		TOTPRequired bool   `json:"totp_required"`
		Challenge    string `json:"challenge"`
		AccessToken  string `json:"access_token"`
	}
	json.NewDecoder(segundo.Body).Decode(&challenge)

	if !challenge.TOTPRequired {
		t.Fatal("login não exigiu segundo fator")
	}
	if challenge.AccessToken != "" {
		t.Error("login entregou access token antes do segundo fator")
	}

	// O desafio não pode valer como credencial.
	me, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	me.Header.Set("Authorization", "Bearer "+challenge.Challenge)
	mr, err := c.Do(me)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Body.Close()
	if mr.StatusCode != http.StatusUnauthorized {
		t.Errorf("o token de desafio foi aceito em /me (status %d)", mr.StatusCode)
	}

	// Completar o segundo fator entrega a sessão.
	code2, _ := totp.GenerateCode(key.Secret(), clk.Now())
	final := post(t, c, srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": challenge.Challenge, "code": code2})
	defer final.Body.Close()
	if final.StatusCode != http.StatusOK {
		t.Fatalf("verificação do TOTP devolveu %d", final.StatusCode)
	}
}
```

Acrescentar ao arquivo de testes de integração o helper:

```go
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}
```

- [ ] **Step 11: Rodar tudo e commitar**

Run: `make lint && make sec && make test`
Expected: tudo verde.

```bash
git add internal cmd
git commit -m "feat(auth): segundo fator TOTP com códigos de recuperação

O segredo fica cifrado com AES-GCM e AAD ligada ao id do usuário. Enroll
grava sem ativar: cadastro interrompido não tranca ninguém para fora da
própria conta.

O token entregue depois da senha tem audience própria e é recusado como
access token — sem isso o segundo fator seria decorativo. Código de
recuperação é consumido com DELETE ... RETURNING, então dois usos simultâneos
não podem ambos encontrar a linha."
```

---

## Auto-revisão do plano

Executada conforme a skill de planejamento, comparando o plano com a spec.

### 1. Cobertura da spec

| Requisito da spec | Tarefa |
|---|---|
| §3.1 papéis por flag | Task 1, Task 8 |
| §3.3 stdlib sem framework, sem ORM | Task 7, Task 8 |
| §3.4 tudo sob `internal/` | Mapa de arquivos |
| §4.1 tabelas `users`, `sessions`, `audit_log` | Task 2 |
| §4.2 UUIDv7, `ip_hash`, `error_kind` | Task 2, Task 3, Task 6 |
| §5.1 SSRF | Task 4 |
| §5.2 Argon2id, rotação, tempo constante, lockout, TOTP | Tasks 5, 6, 8, 9 |
| §5.3 cookie `HttpOnly`, header anti-CSRF | Tasks 7, 8 |
| §5.4 AES-GCM, tipo que não vaza | Task 3 |
| §5.5 `userID` obrigatório, 404 em vez de 403 | Task 6 (assinaturas), Task 7 (`CodeNotFound`) |
| §5.7 headers, CORS, timeouts, rate limit, body limit | Task 7 |
| §9.1 envelope de erro, `trace_id` | Task 7 |
| §14 Postgres real, sem mock | Task 2 |
| §15 CI com vet, staticcheck, gosec, govulncheck | Task 1 |

### 2. Lacunas conscientes, com destino

| Item da spec | Onde entra | Motivo de não estar aqui |
|---|---|---|
| §5.5 Row-Level Security | Plano 2 | Só faz sentido com a primeira tabela de recurso por usuário (`targets`). Aplicar em `users`/`sessions`, que a própria autenticação lê, não acrescenta barreira. |
| §3.3 `sqlc` | Plano 2 | O schema ainda muda a cada tarefa aqui; introduzir geração de código antes de estabilizar significa regenerar a cada passo. As interfaces `Store` e `UserStore` já isolam a troca. |
| §5.8 verificação de e-mail | Plano 3 | Vem junto do canal de notificação por e-mail. |
| §13 métricas Prometheus | Plano 2 | As métricas que importam são as do sondador. |

### 3. Problemas encontrados e corrigidos nesta revisão

1. **Inconsistência de tipo:** a Task 6 definia `NewPGStore` devolvendo a interface `Store`, e a Task 8 precisava renomear o tipo. Corrigido na origem: a Task 6 já devolve `*PGStore`.
2. **Mapa de arquivos desatualizado:** listava `internal/audit/audit.go`, que não existe — a auditoria vive dentro da transação em `auth.Tx`. Removido, e acrescentados `clock.go`, `user.go`, `routes.go` e `trace.go`.
3. **Requisito sem tarefa:** o TOTP da §5.2 não tinha implementação. Acrescentada a Task 9.

---

## Verificação final do plano

- [ ] `make lint` sem apontamentos
- [ ] `make sec` sem apontamentos
- [ ] `go test -race -count=1 ./...` verde
- [ ] `go test -short ./...` verde sem Docker rodando
- [ ] CI verde no GitHub
- [ ] O binário sobe, migra e responde `/healthz`
- [ ] `curl` sem `X-Requested-With` em POST devolve 403
- [ ] Nenhuma resposta de erro contém texto vindo do Postgres ou stack trace
- [ ] Com TOTP ativo, login com senha correta devolve `totp_required` e nenhum access token
- [ ] O token de desafio é recusado em `/api/v1/me`
- [ ] `SELECT totp_secret_enc FROM users` não contém o segredo em claro

Só depois disso escrever o Plano 2.
