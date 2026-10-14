# StillOn — Documento de Design

- **Data:** 2026-09-22
- **Status:** aprovado para planejamento de implementação
- **Autor:** BipoXDXD
- **Prazo alvo:** ~14 dias

---

## 1. Visão geral

StillOn é um monitor de disponibilidade com status page pública.

O usuário cadastra alvos HTTP (sites, APIs, webhooks). O sistema sonda cada alvo
em intervalo configurável, registra latência e resultado, detecta incidentes,
notifica por Discord e e-mail, e publica uma página de status compartilhável.

Equivalentes de mercado: UptimeRobot, Uptime Kuma, Better Stack.

### 1.1 Objetivo do projeto

Este é um projeto de portfólio dirigido a vagas de **backend Go** e **fullstack Go**.
As decisões de design otimizam para demonstrar, em ordem:

1. Concorrência correta em Go (goroutines com limite, `context`, shutdown gracioso)
2. Postura de segurança acima da média (SSRF, segredos, isolamento de dados)
3. Modelagem de dados que aguenta crescimento (particionamento, agregação)
4. Testabilidade (relógio injetado, banco real em testes, sem mock de SQL)

Recursos que não servem a esses objetivos foram cortados deliberadamente.

---

## 2. Escopo

### 2.1 Dentro do escopo

- Usuário único (login, 2FA opcional, sem multi-tenancy)
- Alvos HTTP/HTTPS com método, headers, timeout e intervalo configuráveis
- Sondagem agendada com limite global de concorrência
- Detecção de incidentes com histerese
- Notificação por Discord (webhook) e e-mail (Resend), com verificação de destino
- Histórico de 30 dias em resolução bruta, 13 meses agregado
- Uma status page pública por usuário, renderizada no servidor
- Painel administrativo SPA em React

### 2.2 Fora do escopo (decisão explícita)

| Item | Motivo |
|---|---|
| Multi-tenancy, times, planos | Dobraria o prazo sem somar sinal técnico novo |
| Sondas TCP/ICMP/DNS | HTTP já exercita todo o pipeline |
| Sondagem multi-região | Exige infra distribuída; fora do prazo |
| Slack, Telegram, webhook genérico | Dois canais já provam a abstração `Notifier` |
| SSE / WebSocket no painel | `refetchInterval` resolve; SSE é o primeiro corte se atrasar |
| Domínio customizado na status page | Verificação de DNS é escopo próprio |

---

## 3. Arquitetura

### 3.1 Topologia

Binário único com papel selecionado por flag:

```
stillon --role=all      # dev e deploy do portfólio: API + sondador no mesmo processo
stillon --role=api      # só serve HTTP
stillon --role=prober   # só executa sondas
```

O padrão segue o modo *single binary* do Grafana Loki/Mimir. A comunicação entre
API e sondador atravessa as interfaces `Store` e `Scheduler` desde o primeiro dia,
então separar os processos é mudar a flag — não reescrever.

A fila de trabalho vive no Postgres e usa `SELECT ... FOR UPDATE SKIP LOCKED`.
Isso garante exclusão mútua entre sondadores sem Redis, Kafka ou RabbitMQ, e é o
mesmo mecanismo em `--role=all` e em `--role=prober` replicado.

### 3.2 Componentes

| Componente | Responsabilidade | Depende de |
|---|---|---|
| `httpapi` | Rotas, middlewares, DTOs, envelope de erro | `auth`, `targets`, `incident`, `notify`, `statuspage` |
| `auth` | Senha, sessões, rotação de refresh, TOTP | `storage`, `crypto` |
| `targets` | CRUD e validação de alvos | `storage`, `prober/safedial` |
| `prober` | Agendamento e execução de sondas | `storage`, `safedial`, `incident` |
| `prober/safedial` | Guarda anti-SSRF | — (pacote puro, sem I/O de banco) |
| `incident` | Máquina de estado de incidentes | — (função pura sobre estado) |
| `notify` | Fan-out, retry, dedupe, canais | `storage`, `crypto` |
| `statuspage` | Projeção pública sanitizada + render HTML | `storage` |
| `storage` | Repositórios gerados por sqlc | `pgx` |
| `crypto` | AES-GCM, tipo `Secret`, hashing de tokens | — |
| `audit` | Trilha de auditoria | `storage` |

`incident` e `safedial` são pacotes puros, sem I/O. É onde mora a lógica que mais
importa e por isso são os mais fáceis e os mais testados.

### 3.3 Stack

| Camada | Escolha |
|---|---|
| Linguagem | Go 1.26 |
| HTTP | `net/http` stdlib (`ServeMux` com padrões de método, desde 1.22) |
| Banco | PostgreSQL 18 em desenvolvimento, testes e produção (imagem oficial fixada por digest, sem gerência, no compose de produção da VPS; ver §15). O CI também testa o 17 |
| Driver | `jackc/pgx` v5 + `pgxpool` |
| Queries | `sqlc` (SQL puro → Go tipado; sem ORM) |
| Migrations | `goose`, embutidas via `embed.FS`, aplicadas no boot |
| Logs | `log/slog` (JSON, com `trace_id` no `context`) |
| Métricas | `prometheus/client_golang` |
| Concorrência | `golang.org/x/sync` (`errgroup`, `semaphore`) |
| Rate limit | `golang.org/x/time/rate` |
| Senha | `golang.org/x/crypto/argon2` |
| Testes | `testing` + `testcontainers-go` |
| Frontend | Vite + React 19 + TypeScript strict |
| E-mail | Resend (trocável por SES atrás da interface `Notifier`) |

Decisão registrada: **sem framework HTTP**. Desde o Go 1.22 o `ServeMux` da stdlib
resolve `"GET /api/v1/targets/{id}"` nativamente, o que elimina a única razão real
para Chi ou Gin neste projeto.

Decisão registrada: **sem ORM**. `sqlc` gera código a partir do SQL escrito à mão,
o que mantém as queries revisáveis e transforma divergência de schema em erro de
compilação em vez de erro em runtime.

### 3.4 Estrutura de diretórios

```
cmd/stillon/main.go           flags, wiring, shutdown gracioso
internal/
  config/                      env → struct validada, falha rápido no boot
  httpapi/
    middleware/                authn, ratelimit, cors, secheaders, recover, reqid
  auth/
  targets/
  prober/
    safedial/
  incident/
  notify/
    discord/  email/
  statuspage/
  storage/
  crypto/
  audit/
  observability/
migrations/
web/                           SPA React (painel administrativo)
templates/                     HTML da status page pública (embed.FS)
docs/superpowers/specs/
```

Todo o código de aplicação vive sob `internal/`, que o compilador Go impede de ser
importado por qualquer outro módulo.

---

## 4. Modelo de dados

### 4.1 Tabelas

```
users
  id uuid pk            email citext unique     password_hash text
  totp_secret_enc bytea failed_attempts int     locked_until timestamptz
  created_at timestamptz

sessions
  id uuid pk            user_id uuid fk         family_id uuid
  token_hash bytea      expires_at timestamptz  revoked_at timestamptz
  used_at timestamptz   ip_hash bytea           user_agent text

targets
  id uuid pk            user_id uuid fk         name text
  url text              method text             interval_sec int
  timeout_ms int        expected_status int     headers_enc bytea
  degraded_threshold_ms int                     enabled bool
  next_run_at timestamptz                       created_at timestamptz

checks                  -- PARTICIONADA POR MÊS (RANGE em checked_at)
  id uuid               target_id uuid fk       checked_at timestamptz
  status text           latency_ms int          status_code int
  error_kind text
  pk (id, checked_at)   -- a chave de particao precisa entrar na PK

check_rollup_1h
  target_id uuid fk     bucket timestamptz      total int    ok int
  p50 int  p95 int  p99 int  max int
  pk (target_id, bucket)

incidents
  id uuid pk            target_id uuid fk       started_at timestamptz
  resolved_at timestamptz                       cause text
  ack_at timestamptz    open_notified_at timestamptz
  close_notified_at timestamptz

notif_channels
  id uuid pk            user_id uuid fk         kind text      -- discord | email
  config_enc bytea      verified_at timestamptz
  verify_token_hash bytea                       verify_expires_at timestamptz
  enabled bool

status_pages
  id uuid pk            user_id uuid fk         slug text unique
  title text            published bool

status_page_items
  status_page_id uuid fk   target_id uuid fk    display_name text   position int

audit_log
  id uuid pk            user_id uuid fk         action text
  entity text           entity_id uuid          ip_hash bytea    at timestamptz
```

### 4.2 Decisões de modelagem

**IDs são UUIDv7, não `serial`.** Um identificador sequencial vaza a contagem de
registros e convida enumeração (`/targets/1`, `/targets/2`). UUIDv7 é aleatório mas
ordenável por tempo, preservando a localidade do índice B-tree que o UUIDv4
destruiria.

**`checks` é particionada por mês.** A 30s de intervalo, um único alvo gera cerca de
1 milhão de linhas por ano. Retenção via `DELETE` gera bloat e exige `VACUUM`;
retenção via `DROP TABLE checks_2026_01` é instantânea. Partições do mês seguinte
são criadas pelo job de manutenção diário.

**IPs são armazenados como HMAC-SHA256, nunca em claro.** Continua sendo possível
correlacionar tentativas da mesma origem para rate limit e auditoria, mas um
vazamento do banco não entrega endereços. Reduz também a superfície LGPD.

**`error_kind` é enum, não texto livre.** Valores: `timeout`, `dns`, `tls`,
`conn_refused`, `http_status`, `body_limit`, `blocked`. A mensagem crua do erro
nunca é persistida nem exibida — ver seção 5.6.

---

## 5. Segurança

Esta seção é requisito de primeira classe, não apêndice.

### 5.1 SSRF — o risco crítico

O sistema aceita uma URL arbitrária do usuário e faz uma requisição a partir do
servidor. Isso é, por construção, um proxy. Sem defesa, um alvo cadastrado como
`http://169.254.169.254/latest/meta-data/` ou `http://10.0.0.5/admin` transforma o
monitor em ferramenta de varredura da rede interna.

Defesa em camadas, toda concentrada em `internal/prober/safedial`:

1. **Esquema:** apenas `http` e `https`. Rejeita `file`, `gopher`, `dict`, `ftp`.
2. **Blocklist de IP:** loopback (`127.0.0.0/8`, `::1`), link-local
   (`169.254.0.0/16`, `fe80::/10`), privados (`10/8`, `172.16/12`, `192.168/16`,
   `fc00::/7`), `0.0.0.0/8`, multicast, broadcast.
3. **Validação no momento da conexão.** Validar o DNS antes de discar é
   insuficiente: o atacante controla o DNS e faz *rebinding* — a resolução da
   validação devolve um IP público, a da conexão devolve um privado. A janela entre
   as duas é o bug. A correção é validar dentro do `ControlContext` do
   `net.Dialer`, que o Go chama após criar o socket e antes de discar, recebendo o
   endereço real pós-resolução:

   ```go
   dialer := &net.Dialer{
       Timeout: 5 * time.Second,
       ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
           return safedial.AllowAddr(address) // address = IP:porta real
       },
   }
   transport := &http.Transport{
       DialContext: dialer.DialContext,
       Proxy:       nil, // não herdar HTTP_PROXY do ambiente
   }
   ```

   Nota de implementação: `net.Dialer.Control` é ignorado quando `ControlContext`
   está definido. Usar apenas `ControlContext`.
4. **Redirects revalidados.** `CheckRedirect` aplica a mesma guarda a cada salto e
   limita a 3. Sem isso, uma URL pública que redireciona para IP privado contorna
   todas as camadas anteriores.
5. **Corpo limitado e descartado.** `io.LimitReader(resp.Body, 64<<10)`. O monitor
   não precisa do conteúdo, e expor conteúdo criaria canal de exfiltração.
6. **Timeout rígido** por sonda, propagado por `context`.

`safedial` é um pacote puro com testes table-driven cobrindo cada faixa de IP,
cada esquema e o cenário de rebinding.

### 5.2 Autenticação

- **Senha:** Argon2id com `m=64MiB, t=3, p=2` — acima do mínimo recomendado pela
  OWASP (19 MiB, t=2, p=1). Os parâmetros são gravados junto do hash no formato
  PHC, permitindo *rehash* transparente no próximo login quando o custo subir.
- **Access token:** JWT, validade de 15 minutos, assinado com HMAC-SHA256.
- **Refresh token:** opaco, 32 bytes de `crypto/rand`, validade de 30 dias.
- **Refresh é persistido hasheado** (SHA-256). Um dump do banco não permite
  autenticar como nenhum usuário.
- **Rotação com detecção de reúso:** cada refresh consumido é invalidado e emite um
  sucessor na mesma `family_id`. Se um token já consumido reaparece, a única
  explicação é roubo — toda a família é revogada e o evento vai para o audit log.
- **Login em tempo constante:** e-mail inexistente e senha incorreta consomem o
  mesmo tempo e retornam a mesma mensagem. Caso contrário o tempo de resposta vira
  oráculo de existência de conta.
- **Lockout progressivo** por conta após falhas consecutivas, somado a rate limit
  por IP.
- **TOTP** opcional (RFC 6238), segredo criptografado em repouso, com códigos de
  recuperação de uso único.

### 5.3 Tokens no navegador

| | Access token | Refresh token |
|---|---|---|
| Armazenamento | Memória do processo React | Cookie |
| Flags | — | `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth` |
| Legível por XSS | Apenas durante execução | Não |

**Nada é gravado em `localStorage` ou `sessionStorage`.** Qualquer XSS — inclusive
originado de dependência npm comprometida — lê `localStorage` e exfiltra a sessão
inteira. Cookie `HttpOnly` é invisível ao JavaScript.

O custo dessa escolha é CSRF, mitigado por `SameSite=Strict` somado à exigência do
header `X-Requested-With` em toda mutação (um formulário HTML cross-site não
consegue defini-lo).

### 5.4 Segredos em repouso

Headers customizados de alvos (podem conter `Authorization`) e configurações de
canais (URL de webhook do Discord, chave de API do Resend, endereço de e-mail) são
tratados como segredos.

- **AES-256-GCM.** Chave mestra via variável de ambiente, nonce aleatório por
  registro prefixado ao ciphertext, e o UUID da linha como *additional
  authenticated data* — impedindo que um ciphertext seja movido entre linhas.
- **Tipo que se recusa a vazar:**

  ```go
  type Secret []byte

  func (Secret) String() string               { return "[REDACTED]" }
  func (Secret) GoString() string             { return "[REDACTED]" }
  func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
  func (Secret) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
  ```

  Com isso, `slog.Info("target", "cfg", t)`, `fmt.Sprintf("%v", t)` e
  `json.Marshal(t)` tornam-se incapazes de imprimir o segredo. A proteção passa a
  ser do sistema de tipos, não da disciplina do programador.

### 5.5 Isolamento de dados

- Toda função de repositório recebe `userID` como primeiro parâmetro obrigatório.
  Não existe `GetTarget(ctx, id)` — apenas `GetTarget(ctx, userID, id)`. Omitir o
  filtro não compila.
- **Row-Level Security no Postgres** como segunda barreira independente, ativada em
  todas as tabelas com `user_id`.
- Recurso pertencente a outro usuário retorna **404, nunca 403**. Um 403 confirma
  que o identificador existe.

### 5.6 Status page pública — maior superfície de vazamento

É o único endpoint sem autenticação. Usa um DTO próprio, construído campo a campo;
**nunca serializa a struct de domínio**.

| Exposto | Nunca exposto |
|---|---|
| Nome de exibição | URL real do alvo |
| Status (no ar / degradado / fora) | Headers, IPs, portas |
| Uptime agregado e latência agregada | Mensagem crua de erro |
| Histórico de incidentes (início, fim, duração) | `user_id`, UUIDs internos, status code exato |

Uma mensagem como `dial tcp 10.0.3.42:5432: connect: connection refused` publicaria
topologia interna. Por isso todo erro é normalizado para o enum de `error_kind`
antes de qualquer persistência ou exibição.

### 5.7 Superfície HTTP

- **Headers de resposta:** CSP estrita sem `unsafe-inline` (com nonce por
  requisição), `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `frame-ancestors 'none'`.
- **CORS:** origem única, explícita, vinda da configuração. Nunca `*`.
- **Timeouts no `http.Server`:** `ReadHeaderTimeout` é obrigatório — sem ele o
  servidor aceita Slowloris. Também `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- **`http.MaxBytesReader`** em todo corpo de requisição.
- **Rate limit** em duas camadas: por IP para rotas públicas e de autenticação, por
  usuário para rotas autenticadas. Token bucket via `golang.org/x/time/rate`.
- **`/metrics` e `net/http/pprof`** ligados apenas em bind local ou atrás de
  autenticação. Métricas Prometheus públicas entregam o mapa operacional.

### 5.8 Abuso de e-mail

Sem controle, o serviço se torna um relay de spam: um usuário cadastra endereço
arbitrário de terceiro e o sistema passa a enviar e-mail em nome próprio para ele.

- **Verificação de posse obrigatória.** Cadastrar um canal de e-mail gera um token
  de uso único com validade de 24h, armazenado hasheado. O canal só entra no
  fan-out após `verified_at` estar preenchido — verificação feita no envio, não na
  UI.
- **Rate limit** de envios por usuário e de reenvios de verificação.
- Canal não verificado é exibido no painel com estado pendente e nunca recebe
  alerta.

### 5.9 Cadeia de suprimentos

`govulncheck`, `gosec` e `staticcheck` obrigatórios no CI. Dependabot ativo.
Imagem final `gcr.io/distroless/static:nonroot`, binário estático, sem shell.
Scan de container com Trivy.

---

## 6. Sondador

### 6.1 Fluxo

```
loop de despacho (tick de 1s)
  └─ SELECT alvos WHERE enabled AND next_run_at <= now()
       FOR UPDATE SKIP LOCKED LIMIT n
     └─ adquire slot no semáforo global (máx. 64 sondas simultâneas)
        └─ goroutine: probe(ctx com timeout do alvo)
           ├─ grava resultado em checks
           ├─ avalia incidente (seção 7)
           └─ next_run_at = now() + interval ± jitter
```

### 6.2 Decisões

- **Semáforo global** (`golang.org/x/sync/semaphore`) limita a concorrência. Sem
  ele, 500 alvos lentos produzem 500 goroutines bloqueadas em I/O de rede.
- **`FOR UPDATE SKIP LOCKED`** garante que nenhum alvo é sondado em duplicata,
  mesmo com múltiplos sondadores. É o mesmo mecanismo em `--role=all` e em
  `--role=prober` replicado, o que torna a separação uma troca de flag.
- **Jitter de ±10%** no reagendamento evita *thundering herd*: sem ele, todos os
  alvos de intervalo igual disparam no mesmo segundo indefinidamente.
- **Timeout sempre menor que o intervalo**, validado no cadastro do alvo. Caso
  contrário execuções se empilham.
- **Shutdown gracioso:** `SIGTERM` cancela o contexto raiz, `errgroup.Wait()`
  aguarda até 15s, sondas em voo terminam ou abortam, e então
  `server.Shutdown(ctx)`.
- **Relógio injetado:** `type Clock interface { Now() time.Time }`. Os testes usam
  relógio falso e avançam o tempo, eliminando `time.Sleep` da suíte.

---

## 7. Detecção de incidentes

### 7.1 O problema

Uma sonda falhou. Isso é um incidente?

- Abrir na **primeira falha** detecta rápido, mas um pacote perdido gera incidente
  fantasma. A status page fica ruidosa e perde credibilidade.
- Exigir **N falhas consecutivas** é confiável, mas atrasa a detecção em `N ×
  intervalo`.
- **Janela deslizante** tolera instabilidade intermitente ao custo de lógica e
  estado maiores.

A pergunta simétrica importa tanto quanto: **quando fechar?** Fechar no primeiro
sucesso faz um serviço instável abrir e fechar incidente a cada poucos minutos,
disparando notificação a cada ciclo.

### 7.2 Decisão adotada

Histerese assimétrica — **rápido para abrir, lento para fechar**:

| Transição | Condição |
|---|---|
| Nenhum incidente → aberto | 3 falhas consecutivas |
| Aberto → resolvido | 5 sucessos consecutivos |

Racional: com intervalo padrão de 60s, a detecção leva 3 minutos e a resolução
leva 5. Falso positivo custa credibilidade permanente da página; falso negativo
custa minutos de atraso. A assimetria reflete esse desequilíbrio de custo.

`degraded` **não** abre nem fecha incidente — é estado de exibição apenas
(seção 11.5). Apenas `down` conta como falha.

Esta é uma decisão de produto revisável na implementação sem impacto
arquitetural: `internal/incident` é uma função pura sobre estado, e os limiares são
campos da struct `Detector`.

### 7.3 Contrato

```go
// Evaluate recebe o resultado de uma sonda e decide a transição de estado.
// Função pura: não faz I/O, não consulta relógio global.
//
//   s.ConsecutiveFailures  / s.ConsecutiveSuccesses  já vêm atualizados
//   s.Window               últimos N resultados, mais recente por último
//   s.OpenIncident         nil quando não há incidente aberto
//
// Retorna ActionNone, ActionOpen ou ActionClose.
func (d *Detector) Evaluate(s State, r ProbeResult) Action
```

---

## 8. Notificações

Interface `Notifier` com duas implementações: Discord (webhook) e e-mail (Resend).

```go
type Notifier interface {
    Kind() string
    Send(ctx context.Context, ch Channel, ev Event) error
}
```

- **Idempotência no banco, não em mutex.** Cada incidente notifica no máximo uma
  vez na abertura e uma no fechamento, garantido por
  `UPDATE incidents SET open_notified_at = now() WHERE id = $1 AND open_notified_at IS NULL`
  — a corrida é resolvida pelo Postgres. **Revisado em 2026-10-14:** enquanto o
  incidente segue aberto e sem ack, cada canal recebe lembretes periódicos (Discord a
  cada 30 min nas primeiras 6 h e depois a cada 6 h; e-mail a cada 2 h e depois a cada
  24 h), cada um idempotente pelo `UNIQUE (incident_id, channel_id, event, seq)`. Ver
  [lembretes de incidente aberto](2026-10-14-lembretes-design.md).
- **Fan-out** para todos os canais habilitados **e verificados** do usuário.
- **Retry** com backoff exponencial e teto de tentativas. Falha definitiva vai para
  o audit log e nunca derruba o sondador.
- **Timeout** no envio: um webhook lento não pode travar o pipeline de sondagem.
- Envio roda fora do caminho crítico da sonda, em worker próprio.

---

## 9. API HTTP

REST sob `/api/v1`, servida por `net/http.ServeMux`.

```
POST   /auth/login          POST /auth/refresh     POST /auth/logout
GET    /me
GET    /targets             ?cursor=&limit=
POST   /targets
GET    /targets/{id}
PATCH  /targets/{id}
DELETE /targets/{id}
GET    /targets/{id}/checks ?from=&to=&resolution=
GET    /incidents           ?status=&cursor=
POST   /incidents/{id}/ack
GET    /channels            POST /channels         DELETE /channels/{id}
POST   /channels/{id}/verify/resend
GET    /status-pages        POST /status-pages     PATCH /status-pages/{id}

# públicos — sem autenticação, rate limit próprio
GET    /p/{slug}                    HTML renderizado pelo servidor
GET    /verify/{token}              verificação de canal de e-mail
GET    /healthz   GET /readyz
```

### 9.1 Convenções

**Paginação por cursor, nunca offset.** `OFFSET 10000` obriga o Postgres a varrer e
descartar 10 mil linhas, e o resultado muda se houver inserção concorrente.

**Envelope de erro estável:**

```json
{ "error": { "code": "target_not_found", "message": "Alvo não encontrado." },
  "trace_id": "01JB..." }
```

O `code` é contrato para o cliente; a `message` é texto para humano; o detalhe real
fica apenas no log, correlacionado pelo `trace_id`. Erro de banco jamais atravessa
a fronteira HTTP.

**`resolution` é decidida pelo servidor.** Janela ≤ 48h lê de `checks`; acima
disso, de `check_rollup_1h`. O cliente pede um intervalo de tempo, não uma tabela.

---

## 10. Status page pública

### 10.1 Decisão: renderizada em Go, não em React

O painel e a status page têm requisitos opostos:

| | Painel | Status page |
|---|---|---|
| Acesso | Autenticado, sessão longa | Anônimo, visita de segundos |
| Carregada quando | Dia normal | **Quando algo está quebrado** |
| Depende de JS | Sim | Não |
| Compartilhamento / SEO | Irrelevante | Importa |

A terceira linha decide. A status page é a página que as pessoas abrem justamente
quando algo falhou; fazê-la depender de baixar um bundle, hidratar e só então
consultar a API adiciona pontos de falha no pior momento possível.

Implementação: `html/template` da stdlib + `embed.FS`, CSS inline no `<style>` com
nonce da CSP, sem JavaScript. Resposta abaixo de 10 KB, com `Cache-Control` de 30s.

### 10.2 Conteúdo

Título, lista de alvos com nome de exibição e estado atual, barra de uptime dos
últimos 90 dias em resolução diária, e histórico de incidentes resolvidos com
início, fim e duração. Nada além do listado na seção 5.6.

---

## 11. Frontend (painel administrativo)

### 11.1 Stack

| Camada | Escolha | Racional |
|---|---|---|
| Build | Vite | Padrão atual |
| UI | React 19 + TypeScript strict | — |
| Rotas | React Router v7 | É o termo que aparece nas vagas |
| Estado de servidor | TanStack Query | Cache, revalidação, retry, estados de carga |
| Estado de UI | `useState` / Context | — |
| Formulários | React Hook Form + Zod | Schema compartilhado entre validação e tipos |
| Estilo | Tailwind + shadcn/ui | Primitivos acessíveis (Radix) sem virar projeto de design |
| Gráficos | Recharts | Suficiente; `uPlot` se a série pesar |
| Testes | Vitest + Testing Library + MSW | — |
| E2E | Playwright, só no fluxo crítico | login → cadastrar alvo → ver status |

**Sem Redux.** Quase todo o estado da aplicação é estado de servidor, que é
exatamente o problema que o TanStack Query resolve.

### 11.2 Autenticação no cliente

O access token vive apenas em memória. Três consequências exigem código explícito:

1. **Recarregar perde o token.** No boot, o app chama `POST /auth/refresh` — o
   cookie `HttpOnly` acompanha automaticamente — e re-hidrata a sessão. Durante a
   tentativa exibe splash, não a tela de login, para não piscar estado deslogado a
   cada F5.
2. **Expiração em 15 minutos.** Um wrapper de `fetch` intercepta `401`, renova e
   repete a requisição original de forma transparente.
3. **Renovação *single-flight*.** Cinco requisições recebendo `401` simultaneamente
   não podem disparar cinco refreshes: a rotação invalidaria quatro deles e
   derrubaria a sessão. Uma promise compartilhada resolve — a primeira renova, as
   demais aguardam nela.

4. **Todo request de mutação envia `X-Requested-With: stillon`.** O backend
   exige esse header como segunda camada anti-CSRF (5.3); um formulário HTML
   cross-site não consegue defini-lo. O wrapper de `fetch` o adiciona em
   `POST`/`PATCH`/`DELETE`, e o header está na lista de `Access-Control-Allow-Headers`
   do CORS.

O item 3 falha apenas sob concorrência e nunca em desenvolvimento. É risco
conhecido e endereçado.

### 11.3 Rotas

```
/login                    e-mail + senha, passo TOTP quando ativo
/                         Dashboard — grid de alvos, status, uptime 24h, sparkline
/targets/new              /targets/:id/edit
/targets/:id              Detalhe — latência, timeline de checks, incidentes
/incidents                Lista filtrável, ação de reconhecer
/channels                 Discord e e-mail, com estado de verificação
/status-pages             Configuração, preview e link público
/settings                 Senha, 2FA, sessões ativas com revogação
```

### 11.4 Atualização de dados

TanStack Query com `refetchInterval`: 15s no dashboard, 60s na tela de detalhe,
suspenso em aba de segundo plano (`refetchIntervalInBackground: false`).

SSE seria mais elegante e exibiria melhor o Go, mas está fora do escopo (2.2) e é
o primeiro item a ser cortado caso o prazo aperte.

### 11.5 Classificação de estado

| Estado | Regra |
|---|---|
| `up` | Sonda bem-sucedida e latência ≤ limiar |
| `degraded` | Sonda bem-sucedida e latência > limiar |
| `down` | Sonda falhou |

**Decisão adotada:** limiar configurável por alvo (`degraded_threshold_ms`), com
padrão de 2000 ms. Valor nulo desativa a classificação `degraded` para aquele alvo.

Racional: limiar fixo global é simples mas injusto — 800 ms é excelente para um
endpoint de relatório e péssimo para um health check. Limiar relativo à mediana
histórica seria mais justo, porém exige janela de aquecimento e torna o estado
difícil de explicar a quem lê a status page. Por alvo, com padrão razoável, é o
meio-termo defensável.

`degraded` é estado de exibição e não participa da máquina de incidentes (7.2).

### 11.6 Acessibilidade

Um painel de monitoramento comunica informação primariamente por cor, e verde/
vermelho é exatamente o par indistinguível para deuteranopia (~8% dos homens).

- Todo estado carrega **ícone e texto**, não apenas cor: `● No ar`, `▲ Degradado`,
  `✕ Fora`.
- Mudanças de estado anunciadas em região `aria-live`.
- Navegação completa por teclado com foco visível (shadcn/ui já fornece).
- Contraste mínimo AA verificado nos temas claro e escuro.
- Tema escuro via `prefers-color-scheme` com override manual persistido.

### 11.7 Risco conhecido: CSP × bibliotecas de gráfico

A CSP sem `unsafe-inline` (5.7) conflita com bibliotecas que injetam `<style>` em
runtime. Tailwind não é afetado (gera CSS estático em build). Caso o Recharts
esbarre, a saída é nonce por requisição propagado ao bundle. Verificar no dia 11,
não no dia 14.

### 11.8 Estrutura

```
web/src/
  app/            router, providers, boot de sessão
  lib/            wrapper de fetch, refresh single-flight, tipos da API, zod
  components/ui/  shadcn
  components/     StatusBadge, LatencyChart, IncidentTimeline, UptimeBar
  features/       auth/ targets/ incidents/ channels/ statuspages/
  hooks/
  test/           setup do MSW
```

Organização por feature, não por tipo de arquivo: mexer em alvos toca um diretório.

---

## 12. Retenção

| Dado | Retenção | Mecanismo |
|---|---|---|
| `checks` | 30 dias | `DROP` da partição mensal vencida |
| `check_rollup_1h` | 13 meses | `DELETE` (volume baixo) |
| `incidents` | Indefinida | — |
| `audit_log` | 12 meses | `DELETE` |
| `sessions` expiradas | 7 dias após expirar | `DELETE` |

Job de manutenção diário, executado nos papéis `all` e `prober`, também cria a
partição do mês seguinte e consolida os rollups horários.

---

## 13. Observabilidade

- **Logs:** `log/slog` em JSON, com `trace_id` propagado via `context` desde o
  middleware de entrada.
- **Métricas:** `probe_duration_seconds` (histograma, por alvo),
  `probe_total{status}`, `scheduler_inflight`, `scheduler_queue_depth`,
  `incidents_open`, `notify_total{kind,result}`, mais as métricas do `pgxpool`.
- **Health:** `/healthz` raso (processo vivo), `/readyz` verificando o banco.

---

## 14. Estratégia de testes

| Tipo | Alvo | Ferramenta |
|---|---|---|
| Unitário | `safedial`, `incident`, `crypto`, validação | table-driven |
| Integração | repositórios, migrations, RLS | `testcontainers-go` + Postgres real |
| Sondador | servidor simulando timeout, 500, redirect, lentidão, corpo gigante | `httptest` |
| Agendador | relógio falso, sem `sleep` | `Clock` injetado |
| API | fluxo de auth completo, tentativa de acesso cruzado entre usuários | `httptest.Server` |
| Frontend | componentes e hooks, API mockada | Vitest + MSW |
| E2E | login → cadastrar alvo → ver status | Playwright |
| Corrida | `go test -race ./...` | CI |

**Sem mock de banco.** `testcontainers-go` sobe Postgres real, o que torna os testes
de RLS e de `SKIP LOCKED` significativos — ambos são invisíveis contra um mock.

Meta de cobertura: alta e verificada em `safedial`, `incident`, `auth` e `crypto`.
Cobertura de handler CRUD não é perseguida.

---

## 15. CI/CD e deploy

**Pipeline (GitHub Actions):**
`go vet` → `staticcheck` → `gosec` → `govulncheck` → `go test -race -cover` →
`npm run typecheck` → `vitest run` → build multi-stage → Trivy → publicação no GHCR.
O deploy não é automático: é disparado à mão, com o SHA de um commit da `main`
que passou por tudo isso.

**Imagem:** multi-stage terminando em `gcr.io/distroless/static:nonroot`. Binário
estático, sem shell no container.

**Deploy:** uma VPS da Hostinger (Ubuntu 24.04) com um compose de produção
(`deploy/vps/compose.prod.yaml`): Caddy com HTTPS automático, o binário em
`--role=all` e o Postgres 18 sem gerência, com backup diário conferido e
cifrado. Migrations aplicadas no boot. O passo a passo e a operação estão no
runbook `docs/deploy/vps.md`.

Esta seção dizia "Fly.io, com Postgres gerenciado" até o Plano 11, que trocou o
Fly pela VPS em 2026-09-25, por decisão do dono (item 5 das decisões em
`docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`; a spec do Plano 11 está no
commit indicado no README, Histórico).

**Desenvolvimento local:** `docker compose up` sobe aplicação, banco e seed em um
comando. Requisito não negociável: se o avaliador não consegue rodar em 30
segundos, ele não roda.

---

## 16. Cronograma

| Dias | Entrega |
|---|---|
| 1–2 | Esqueleto, config, migrations, Postgres, CI verde |
| 3–4 | Autenticação completa (Argon2id, rotação, cookie, TOTP) |
| 5 | CRUD de alvos + `safedial` com bateria de testes |
| 6–7 | Agendador, sondador, persistência de checks |
| 8 | Detecção de incidentes + notificação Discord |
| 9 | E-mail (Resend) + fluxo de verificação de destino |
| 10 | Status page em Go, rollups, retenção, hardening, auditoria |
| 11 | Scaffold do front, Tailwind/shadcn, router, camada de auth (11.2) |
| 12 | Dashboard + CRUD de alvos + formulários |
| 13 | Detalhe do alvo com gráficos, incidentes, canais |
| 14 | Acessibilidade, testes de front, deploy, README |

---

## 17. Registro de decisões

| # | Decisão | Alternativa rejeitada | Motivo |
|---|---|---|---|
| 1 | Binário único com `--role` | Processos separados desde o início | Custo operacional não se paga em 2 semanas; o caminho de separação fica pronto |
| 2 | `net/http` stdlib | Chi, Gin, Echo | `ServeMux` ≥ 1.22 cobre o necessário |
| 3 | `sqlc` | GORM, Ent | SQL revisável; divergência de schema vira erro de compilação |
| 4 | Fila no Postgres (`SKIP LOCKED`) | Redis, NATS, Kafka | Uma dependência a menos; demonstra conhecimento de Postgres |
| 5 | Validação SSRF no `ControlContext` | Validação pré-DNS | Única defesa eficaz contra DNS rebinding |
| 6 | Token em memória + cookie `HttpOnly` | `localStorage` | `localStorage` é legível por XSS |
| 7 | Status page em Go | React | Deve carregar quando as coisas estão quebradas |
| 8 | Histerese 3 falhas / 5 sucessos | Falha única; janela deslizante | Falso positivo custa credibilidade permanente |
| 9 | Limiar de degradação por alvo | Global fixo; relativo à mediana | Justo sem exigir aquecimento nem ficar inexplicável |
| 10 | UUIDv7 | `serial` | Não vaza contagem nem permite enumeração; preserva índice |
| 11 | Discord + e-mail, e para aí | Slack, Telegram, webhook genérico | Dois canais já provam a abstração; mais vira coleção |

---

## 18. Riscos

| Risco | Mitigação |
|---|---|
| CSP quebra o Recharts | Verificar no dia 11; nonce por requisição se necessário |
| Deliverability de e-mail | Provedor (Resend) em vez de SMTP próprio |
| Refresh concorrente derruba sessão | *Single-flight* explícito (11.2, item 3) e teste dedicado |
| Particionamento atrasa o dia 1–2 | Partição criada por migration; job de manutenção só a partir do dia 10 |
| Escopo do front estourar os 4 dias | SSE já está fora; tema escuro e E2E são os próximos cortes |
