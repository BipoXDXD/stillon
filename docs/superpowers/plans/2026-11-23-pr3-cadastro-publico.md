# PR 3: cadastro público. Plano de implementação

> **Para agentes:** sub-skill OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para executar este plano tarefa por tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.

**Objetivo:** abrir o StillOn para quem chega pela internet: cadastro com confirmação de e-mail, "esqueci a senha", Turnstile, teto global de 50 e-mails de conta por dia e um remetente só para esses e-mails. Tudo sem expor quais e-mails já têm conta.

**Arquitetura:** os tokens de conta (confirmação e redefinição) ficam numa tabela nova sob FORCE RLS, com escopo pelo hash do token, como o `verify_token_hash` dos canais. O teto diário fica numa tabela de contador com reserva atômica (`INSERT … ON CONFLICT … WHERE sent < teto`), compartilhada pelas réplicas. Os e-mails de conta saem por um enviador próprio (`internal/contaemail`): balde por destinatário, depois o teto, depois a Resend com o remetente de conta. Ele roda em segundo plano, para que a resposta HTTP não dependa de haver conta. Os avisos de incidente nunca passam por ele. A criação de recursos ganha um middleware `ExigeEmailConfirmado`. O cadastro sai desligado por configuração e só liga com o par de chaves do Turnstile e o e-mail configurados.

**Stack:** Go 1.26.11, PostgreSQL 17/18, pgx + sqlc, goose, testcontainers, Resend, Cloudflare Turnstile (siteverify por `net/http`, sem SDK), React 19 + react-router 8 + TanStack Query 5 + zod 4, Vitest + MSW, Playwright + axe.

**Spec:** [`docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`](../specs/2026-11-22-multiinquilino-cadastro-design.md) (seções Contas, Cotas e abuso, Testes). Roteiro: linha "PR 3" de [`2026-11-22-etapa1-multiinquilino.md`](2026-11-22-etapa1-multiinquilino.md). Portão herdado do [plano do 2b](2026-11-22-pr2b-pertenca-projetos.md) ("Portão PR 3").

## Restrições globais

- Go 1.26.11. **Nenhuma dependência nova** em `go.mod` nem em `web/package.json`: o Turnstile usa `net/http` no servidor e o `api.js` da Cloudflare no painel.
- Tudo em português do Brasil: código novo, nomes de teste, comentários, textos de tela, e-mails, commits (`tipo(escopo): descrição`) e PRs.
- Antes de dizer que algo funciona: `go build ./... && go vet ./... && go test -race -count=1 -p 1 <pacotes afetados>`. No fim de cada sub-PR, `make test`. No painel: `cd web && npm test && npm run typecheck && npm run lint`.
- Um teste novo só vale depois de vê-lo **falhar com o código sabotado**. Cada passo "rodar e ver falhar" diz o que sabotar quando o teste já passaria.
- Para buscar, use `git grep` / `git ls-files`, nunca `grep -r` da raiz: `.claude/` guarda outras worktrees.
- Migration nova: **só a 00047** (`00047_cadastro_publico.sql`), apenas expand. A 00015 fica vaga para sempre. Se outra PR publicar a 00047 antes, renumere para o próximo número livre antes do merge.
- Env nova é sempre opcional (`${X:-}`). Segredo real nunca vai para o repositório nem para o chat: fica no Bitwarden (pasta "StillOn") e no recurso do Coolify, pelo runbook. As chaves de teste públicas do Turnstile só aparecem em teste e em `compose.yaml` (dev).
- Contratos fechados do servidor:
  - código de erro novo só entra na tabela de `internal/apierr/errors.go` **e** no `ERROS_DO_SERVIDOR` de `web/src/test/api-falsa.ts`;
  - handler que decodifica JSON entra em `corposEstritos()` (`internal/httpapi/dto_estrito_internal_test.go`);
  - rota pública entra em `rotasPublicas` com o porquê;
  - arquivo novo que chama `ScopeToUser`/`InUserTx` entra na lista de `internal/storage/guarda_escopo_test.go` com o motivo.
- Valores do spec: token de **256 bits**, só o hash SHA-256 gravado. Confirmação vale **24 h**, redefinição **1 h**, os dois de uso único. O teto é de **50** e-mails de conta por dia (dia UTC). O token do Turnstile tem até **2048** caracteres e vale 300 s; o servidor não guarda token.
- Um comando pesado por vez (Docker, Playwright); remova os contêineres que criar. Merge fora de 00:00–01:00 BRT. Depois de cada merge, confira o run do `Coolify release` desse SHA e o `readyz`.
- Tarefas de tela (T11, T12): carregue `frontend-design:frontend-design` (direção C "Editorial claro", Fraunces + Inter, primária índigo), `design-skills:ux-writing`, `design-skills:interaction-design`, `design-skills:design-systems` (reusar `web/src/components/ui`) e `design-skills:accessibility-audit` (WCAG 2.2 AA).

## Foco da revisão

1. **Pré-conta do atacante.** Alguém cadastra o e-mail da vítima e não confirma. A vítima tenta se cadastrar, recebe "você já tem conta; esqueceu a senha?" e, pelo reset, toma a conta: o reset confirma o e-mail, troca a senha e derruba as sessões do atacante. Teste: `TestRedefinirTomaContaPreCriadaEDerrubaSessoes` (T6).
2. **Dois consumos do mesmo token ao mesmo tempo** (clique duplo, scanner de e-mail mais humano): exatamente um vence, os outros recebem `token_invalid`. Testes: `TestConfirmacaoSimultaneaSoUmaVence` (T5) e `TestRedefinicaoSimultaneaSoUmaVence` (T6).
3. **Teto sob concorrência e entre réplicas, com avisos nunca barrados.** 60 reservas simultâneas dão exatamente 50. Com o teto esgotado, um aviso de incidente por e-mail ainda sai. Testes: `TestTetoReservaExatamente50SobConcorrencia` (T1) e `TestAvisoSaiComTetoDeContaEsgotado` (T9).
4. **Enumeração pelos caminhos raros.** A resposta de signup e de forgot é idêntica para e-mail com conta e sem conta, também com o teto esgotado ou o balde do destinatário vazio (nada de 429 ou erro só num dos casos). Teste: `TestRespostasIdenticasComTetoEsgotado` (T8).
5. **Conta não confirmada criando recurso por uma rota esquecida** (clone de heartbeat, variante `/projects/{projectID}/…`, rota futura): todo `POST` de criação dá 403 `email_not_verified`, garantido por enumeração das rotas registradas. Teste: `TestContaNaoConfirmadaNaoCriaPorNenhumaRota` (T7).

## Decisões

- **Dois sub-PRs.** O 3a (backend) é publicável sozinho: só expande o schema, o cadastro sai desligado por configuração, e a trava de e-mail não muda nada enquanto o único usuário for o operador confirmado. O 3b (painel) chama as rotas novas e só sobe depois do 3a no ar.
- **O aceite dos termos é do PR 6, mas o cadastro público abre no PR 3: conflito do spec.** Decisão: o 3a publica o cadastro **desligado** (`SignupEnabled()` exige `TURNSTILE_SITE_KEY` + `TURNSTILE_SECRET_KEY` + e-mail). Ligar em produção (seção "Ativação em produção") espera o PR 6 publicar os termos, salvo decisão contrária do dono.
- **Código de erro da trava: 403 `email_not_verified`**, separado de `forbidden`. O painel precisa distinguir "confirme seu e-mail" de "sem permissão" para mostrar o aviso certo. Não vaza nada a terceiros, porque exige sessão.
- **Trava aplicada** a todo `POST` que cria targets, heartbeats (inclusive o clone), canais, páginas de status e projetos, nas variantes legadas e de projeto. As páginas de status entram por decisão minha: são conteúdo público e reservam slug, então uma conta não confirmada poderia publicar spam ou ocupar slugs. Fica liberado ler, editar e apagar, além de 2FA, senha e sessões.
- **Outros códigos novos:**
  - `turnstile_failed` (400): sai antes de qualquer consulta ao banco, portanto não diz nada sobre o e-mail;
  - `token_invalid` (400): o mesmo para token inexistente, usado ou vencido;
  - siteverify fora do ar dá 503 `unavailable` (falha fechada).
- **Validação do formato antes do Turnstile.** Ela só olha a entrada, sem enumeração, e não gasta o token de uso único quando o formulário está errado.
- **O token vai no fragmento do link** (`/confirm-email#token=…`, `/reset-password#token=…`). Assim não aparece em log de acesso nem no Referer. O painel lê o fragmento, limpa a barra com `history.replaceState` e faz POST: um GET de scanner não consome nada.
- **Signup em e-mail existente** (confirmado ou não): o e-mail "você já tem conta; esqueceu a senha?" com link para `/forgot-password`, e a mesma resposta 202. As duas ramificações fazem um Argon2 e enfileiram um e-mail. Esqueci a senha com e-mail sem conta: 202 e nenhum e-mail.
- **E-mails de conta em segundo plano** (fila em memória no `contaemail.Enviador`). Sem isso, a latência da Resend existiria só no ramo "tem conta" do forgot, e o tempo de resposta denunciaria a conta. O risco residual são alguns ms de banco (gravação do token), aceito com o limite por IP de 2/min.
- **Fila cheia descarta e registra no log**, nunca bloqueia. Um e-mail de conta perdido se resolve com "reenviar". No desligamento, a fila é drenada por até 10 s.
- **Balde por destinatário** (`conta_destino`: 1 a cada 20 min, rajada 3, em `ip_rate_limits` com a chave HMAC do e-mail) antes do teto global, para ninguém usar o StillOn para encher a caixa de uma vítima. Com o balde vazio, o e-mail some em silêncio e a resposta não muda.
- **O teto não devolve a vaga quando a Resend falha.** É mais simples, e o teto é proteção de cota, não contabilidade. Linhas com mais de 7 dias se apagam na própria reserva.
- **Verificação de canal por e-mail** passa pelo teto e pelo balde (decorador `VerificacaoComTeto`) e sai pelo remetente de conta, como o spec manda. Os avisos (`notificadores`) e o "2FA bloqueado" continuam no remetente atual e **nunca** passam pelo teto.
- **Remetente de conta: `ACCOUNT_EMAIL_FROM`**, opcional; sem ele, vale o `EMAIL_FROM`. Verificar o subdomínio na Resend é passo de runbook.
- **Helper comum `storage.CriarProjetoPadraoNaTx`** remove o Duplicated Code entre `account` e `pgstore`, como o portão do 2b pede. O signup cria o Projeto padrão na mesma transação do usuário.
- **`account.CreateUser` (CLI) continua criando operador**, sem a trava de usuário único. E-mail repetido vira `ErrEmailTaken`. `HasUsers` sai; o CLI confere antes da senha com `UserIDByEmail`.
- **Sem limpeza de contas não confirmadas** (YAGNI): o reset resolve a ocupação de e-mail.
- **O spec cita `user usage` para o operador acompanhar o teto, mas esse comando não existe.** Neste PR fica só o log estruturado (`slog.Warn` com o motivo `teto` ou `destino`). O comando fica para a PR que o criar.
- **A CSP do painel ganha `https://challenges.cloudflare.com` em `script-src` e `frame-src` em todas as rotas.** O painel é SPA: a navegação de `/login` para `/signup` não busca o HTML de novo, então uma CSP por rota não funcionaria.
- **Sem jornada e2e do cadastro com o binário real**, porque ela exigiria rede e e-mail. O caminho do backend fica nos testes de integração Go (T8); as telas e o axe usam `api-imitada.ts` com o script do Turnstile imitado.

## Roteiro

| Sub-PR | Conteúdo | Tarefas | Binário em sobreposição (release) | Publica sozinho? |
|---|---|---|---|---|
| **3a** backend | migration 00047, tokens, teto, Turnstile, enviador de conta, serviços, trava, rotas, fiação | T1–T9 | O binário anterior roda com a 00047 aplicada e ignora as tabelas novas. O painel antigo ignora `email_verified` no `/me` (zod não estrito) | Sim: cadastro desligado, trava sem efeito (só o operador confirmado existe) |
| **3b** painel | cliente, telas públicas, widget, CSP, aviso de e-mail não confirmado, links no login | T10–T12 | Nada de schema. O binário anterior serve o painel anterior | Sim, depois do 3a no ar; pode precisar de rebase sobre o 2b.4 (`Layout`, rotas) |

## Paralelismo

Cada tarefa roda numa worktree própria, criada a partir do ramo do sub-PR (`feat/pr3a-cadastro-backend`, `feat/pr3b-cadastro-painel`), e volta por merge na ordem da onda. Tarefas da mesma onda **não tocam o mesmo arquivo**: as fronteiras abaixo valem como contrato.

| Onda | Tarefas (paralelas) | Começa quando |
|---|---|---|
| 3a·1 | T1, T2, T3, T4 | já |
| 3a·2 | T5, T6, T7 | T1 e T2 no ramo do 3a |
| 3a·3 | T8 | T3, T5, T6 e T7 no ramo |
| 3a·4 | T9 | T4 e T8 no ramo |
| 3b·1 | T10 | já (consome só o contrato abaixo); merge na `main` depois do 3a no ar |
| 3b·2 | T11, T12 | T10 no ramo e a proposta do Step 1 da T11 aprovada pelo dono |

**Conflitos evitados de propósito:**
- `internal/storage/queries/users.sql` e `internal/storage/db/` só são tocados pela **T1**, que escreve de uma vez todas as queries novas e alteradas do PR (o `sqlc_orfao_test` confere por arquivo, então query ainda sem uso não reprova). A remoção das queries mortas (`CountUsers`, `LockUserCreation`) fica para a **T9**, na onda 4. Só T1 e T9 rodam `make generate`.
- `internal/storage/`: a T1 cria `teto_emails_conta.go` e altera `scope.go` e `guarda_escopo_test.go`; a T2 cria só `projeto_padrao.go` (+ teste), usando queries que já existem.
- `internal/storage/guarda_escopo_test.go`: a T1 já inclui `internal/account/cadastro.go` e `internal/account/recuperacao.go` em `chamadoresDeScopeToUser`, assim T5 e T6 não tocam o arquivo. Entrada de arquivo que ainda não existe não reprova: o teste só confere quem chama.
- `internal/account/`: a T2 altera `account.go` e cria `tokens.go` e `emails.go`. A T5 cria só `cadastro.go`, `cadastro_test.go` e `export_cadastro_test.go`; a T6 só `recuperacao.go`, `recuperacao_test.go` e `export_recuperacao_test.go`. A T7 altera `account.go` (onda 2, depois da T2) e `account_test.go`. **T5 e T6 não tocam `account.go` nem `export_test.go`.**
- `internal/apierr/errors.go` e o `ERROS_DO_SERVIDOR` de `web/src/test/api-falsa.ts`: os três códigos novos entram **todos na T7**. A T8 só os usa.
- `internal/httpapi/routes.go` e `Deps`: T7 (onda 2) e depois T8 (onda 3), nunca juntas. Os três testes sem banco que montam `Deps` (`erro_forcado_sem_banco_test.go`, `limites_por_ip_test.go`, `prazo_requisicao_test.go`) e o `newAPICom` de `auth_integration_test.go` são tocados por T7 e T8 em ondas diferentes.
- `internal/config/config.go`: só a T3. A T9 consome os métodos dela.
- Painel: a T10 é dona de `schemas.ts`, `cliente.ts`, `test/api-falsa.ts`, `test/fixtures.ts` e `e2e/api-imitada.ts`. A T11 é dona de `mensagens.ts`, `rotas.tsx`, `features/conta/*`, `webui.go`, `emails.go` (textos) e `e2e/a11y.spec.ts`. A T12 é dona de `Layout.tsx`, `PaginaLogin.tsx` e `app/AvisoDeConfirmacao.tsx`. A T12 não escreve em `mensagens.ts`: o texto do 403 `email_not_verified` nos formulários de criação é da T11.

**Quem produz cada interface consumida por outras tarefas:**

| Interface | Produz | Consome |
|---|---|---|
| tabelas `account_tokens` e `account_email_daily`; `storage.InAccountTokenTx`, `storage.InAccountEmailTx`; queries `InsertAccountToken`, `DeleteAccountTokens`, `PeekAccountToken`, `ConsumeAccountToken`, `InsertSignupUser`, `MarkEmailVerified`, `UserEmailVerified`, `GetAccount.email_verified_at` | T1 | T5, T6, T7 |
| `storage.TetoDeEmailsDeConta` (`Reservar`, `EnviadosHoje`) | T1 | T9 |
| `storage.CriarProjetoPadraoNaTx`; `account.emTx`, `ErrEmailTaken`, `AccountMailer`, `Links`, `novoTokenDeConta`, `ErrTokenInvalido`, os textos de `emails.go` | T2 | T5, T6, T9, T11 (textos) |
| `turnstile.Verificador`, `turnstile.ErrRecusado`, `turnstiletest`; `Config.SignupEnabled`, `TurnstileSiteKey`, `TurnstileSecretKey`, `RemetenteDeConta`, `HostDoTurnstile` | T3 | T8, T9 |
| `contaemail.Enviador`, `NovaVerificacaoComTeto`, `SemEnvio`; `notify.Resend.SendAccount` | T4 | T9 |
| `account.Cadastro` / `account.Recuperacao` | T5 / T6 | T8, T9 |
| `middleware.ExigeEmailConfirmado`, `Service.EmailConfirmado`, `Deps.EmailConfirmado`, `Account.EmailVerified`, os três `apierr.Code*` | T7 | T8, T9, T10 |
| contrato HTTP (abaixo) | T8 | T10 |
| `api.*` do cliente, `ConfigDoCadastro`, `Me.email_verified`, rotas da API falsa e da imitada | T10 | T11, T12 |
| textos aprovados (Step 1 da T11) | T11 | T12 |

**Contrato HTTP (fixo; a T10 pode começar só com ele):**

| Rota | Corpo | Sucesso | Erros |
|---|---|---|---|
| `GET /api/v1/auth/signup-config` | — | 200 `{"enabled": bool, "turnstile_site_key": string}` (chave vazia se desligado) | — |
| `POST /api/v1/auth/signup` | `{email, password, turnstile_token}` | 202 sem corpo | 400 `validation_failed` (`email`: `invalid`; `password`: `too_short`/`too_long`); 400 `turnstile_failed`; 404 `not_found` se desligado; 429; 503 `unavailable` |
| `POST /api/v1/auth/email/confirm` | `{token, password}` (a senha do cadastro; desde o PR 3a.2) | 204 | 400 `token_invalid` (antes de olhar a senha); 401 `invalid_credentials` (senha errada ou vazia; o link não é gasto); 429 `rate_limited` (balde por IP, Argon2 cheio ou conta travada pelo bloqueio do login, com `Retry-After: 900`) |
| `POST /api/v1/auth/email/resend` (com sessão) | — | 202 sem corpo | 401; 429 |
| `POST /api/v1/auth/password/forgot` | `{email, turnstile_token}` | 202 sem corpo | 400 `validation_failed` (`email`); 400 `turnstile_failed`; 404 se desligado; 429; 503 |
| `POST /api/v1/auth/password/reset` | `{token, new_password}` | 204 | 400 `validation_failed` (`new_password`); 400 `token_invalid`; 429 (Argon2 cheio) |
| `GET /api/v1/me` | — | 200 + `"email_verified": bool` | — |
| `POST` de criação (lista na T7) | — | — | 403 `email_not_verified` |

Todas as mutações exigem `X-Requested-With`, como as demais.

## Esforço por tarefa

| Tarefa | Modelo · esforço | Por quê |
|---|---|---|
| T1 | Opus · high | RLS, concorrência do teto |
| T2 | Opus · medium | refatoração com testes existentes |
| T3 | Opus · medium | cliente HTTP e config |
| T4 | Opus · medium | fila e decorador, com falsos |
| T5 | Opus · high | anti-enumeração, consumo atômico |
| T6 | Opus · high | tomada de conta, revogação de sessões |
| T7 | Opus · high | autorização em todas as rotas de criação |
| T8 | Opus · high | superfície pública, contrato, enumeração |
| T9 | Opus · medium | fiação, guarda dos avisos |
| T10 | Opus · medium | contrato do cliente |
| T11 | Opus · medium | telas, CSP, widget |
| T12 | Opus · medium | aviso e links |

Nunca Haiku.

## Estrutura de arquivos

**3a (criar):**
- `internal/storage/migrations/00047_cadastro_publico.sql`: tabelas `account_tokens` e `account_email_daily`, com RLS.
- `internal/storage/queries/account_tokens.sql` e `queries/account_email.sql`: queries novas.
- `internal/storage/teto_emails_conta.go`: `TetoDeEmailsDeConta`.
- `internal/storage/projeto_padrao.go`: helper comum do Projeto padrão.
- `internal/account/tokens.go`: geração de token, validades, `ErrTokenInvalido`.
- `internal/account/emails.go`: `AccountMailer`, `Links`, textos dos e-mails.
- `internal/account/cadastro.go`: `Cadastro` (signup, confirmação, reenvio).
- `internal/account/recuperacao.go`: `Recuperacao` (forgot, reset).
- `internal/turnstile/turnstile.go` e `internal/turnstile/turnstiletest/servidor.go`: verificador e siteverify falso.
- `internal/contaemail/contaemail.go`: enviador com balde, teto e fila; `VerificacaoComTeto`; `SemEnvio`.
- `internal/httpapi/middleware/email_confirmado.go`: a trava.
- `internal/httpapi/cadastrohandlers.go`: os handlers públicos.

**3a (alterar):**
- `internal/storage/scope.go`, `queries/users.sql`, `db/*` (gerado), `guarda_escopo_test.go`;
- `internal/account/account.go`, `internal/tenancy/pgstore/store.go`, `cmd/stillon/user.go`;
- `internal/notify/resend.go`;
- `internal/config/config.go`, `.env.example`, `compose.yaml`, `deploy/vps/env.exemplo`, `docs/deploy/vps.md`, `README.md`;
- `internal/apierr/errors.go`, `internal/httpapi/routes.go`, `accounthandlers.go`, testes de rotas e de corpos estritos;
- `cmd/stillon/main.go`;
- `web/src/test/api-falsa.ts` (só `ERROS_DO_SERVIDOR`).

**3b (criar):**
- `web/src/features/conta/PaginaCadastro.tsx`, `PaginaConfirmarEmail.tsx`, `PaginaEsqueciSenha.tsx`, `PaginaRedefinirSenha.tsx`, `Turnstile.tsx` (+ testes);
- `web/src/app/AvisoDeConfirmacao.tsx` (+ teste);
- `web/e2e/cadastro.spec.ts`, `web/e2e/conta-nao-confirmada.spec.ts`.

**3b (alterar):**
- `web/src/lib/api/{schemas,cliente,mensagens}.ts`, `web/src/test/{api-falsa,fixtures}.ts`, `web/e2e/{api-imitada,a11y.spec}.ts`;
- `web/src/app/{rotas,Layout}.tsx`, `web/src/features/login/PaginaLogin.tsx`;
- `internal/webui/webui.go`, `internal/account/emails.go` (textos aprovados).

---

## Sub-PR 3a: backend

### Task 1: migration 00047, escopos, queries e teto diário

**Files:**
- Create: `internal/storage/migrations/00047_cadastro_publico.sql`, `internal/storage/queries/account_tokens.sql`, `internal/storage/queries/account_email.sql`, `internal/storage/teto_emails_conta.go`
- Modify: `internal/storage/scope.go` (depois de `InVerifyTx`), `internal/storage/queries/users.sql`, `internal/storage/db/*` (gerado por `make generate`), `internal/storage/guarda_escopo_test.go:30-47`
- Test: `internal/storage/tokens_conta_test.go`, `internal/storage/teto_emails_conta_test.go`

**Interfaces:**
- Consumes: `storage.inTx`, `Beginner`, `ErrNoToken` (de `scope.go`); `clock.Clock`.
- Produces:
  - `func InAccountTokenTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error`: liga `app.account_token_hash` (hex). Hash com tamanho diferente de 32 → `ErrNoToken`.
  - `func InAccountEmailTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error`: liga `app.scope = 'account_email'`.
  - `type TetoDeEmailsDeConta struct{…}` com `func NewTetoDeEmailsDeConta(b Beginner, clk clock.Clock, teto int) *TetoDeEmailsDeConta`, `func (t *TetoDeEmailsDeConta) Reservar(ctx context.Context) (bool, error)` e `func (t *TetoDeEmailsDeConta) EnviadosHoje(ctx context.Context) (int, error)`.
  - Queries sqlc:
    - `InsertAccountToken :exec` (`token_hash, user_id, purpose, created_at, expires_at`);
    - `DeleteAccountTokens :exec` (`user_id, purpose`);
    - `PeekAccountToken :one` → `user_id` (válido: `used_at IS NULL AND expires_at > @now`);
    - `ConsumeAccountToken :one` → `user_id` (`UPDATE … SET used_at = @now WHERE token_hash = @token_hash AND purpose = @purpose AND used_at IS NULL AND expires_at > @now RETURNING user_id`);
    - `ReserveAccountEmail :one` → `sent`;
    - `AccountEmailsSentOn :one`;
    - `InsertSignupUser :one` (`INSERT INTO users (id, email, password_hash, created_at) … ON CONFLICT (email) DO NOTHING RETURNING id`);
    - `MarkEmailVerified :exec` (`SET email_verified_at = COALESCE(email_verified_at, @now)`);
    - `UserEmailVerified :one` → `bool`;
    - `GetAccount` passa a devolver também `email_verified_at`.
  - Constantes de propósito no SQL: `'confirm_email'` e `'reset_password'`.

- [ ] **Step 1: Escrever os testes que falham**

Em `tokens_conta_test.go` (com `testdb.New`):
- `TestTokenDeContaSoApareceComOHashCerto`: insere um token no escopo do dono (`InUserTx`) e o lê com `InAccountTokenTx(hash)` (acha). Com outro hash, `PeekAccountToken` dá `pgx.ErrNoRows`. Sem escopo nenhum, `SELECT count(*) FROM account_tokens` dá 0.
- `TestTokenDeContaConsumidoUmaVez`: o primeiro `ConsumeAccountToken` devolve o `user_id`; o segundo dá `pgx.ErrNoRows`.
- `TestConsumoExigeOHashDoEscopo`: dentro de `InAccountTokenTx(outroHash)`, `ConsumeAccountToken` com o `token_hash` verdadeiro no parâmetro dá `pgx.ErrNoRows`, e `used_at` continua nulo.
- `TestTokenDeContaVencidoNaoConsome`: com `@now` = `expires_at`, dá `pgx.ErrNoRows`.
- `TestDonoApagaSoOsProprios`: `DeleteAccountTokens` no escopo de A não apaga token de B (conta as linhas como admin via `testdb.Admin`).
- `TestInAccountTokenTxRecusaHashCurto`: `errors.Is(err, ErrNoToken)` com hash de 31 bytes.
- `TestInsertSignupUserNaoDuplica`: o segundo insert com `"Fulano@X.com"` depois de `"fulano@x.com"` dá `pgx.ErrNoRows` (citext).

Em `teto_emails_conta_test.go`:
- `TestTetoReservaAteOLimite`: com teto 3, `Reservar` dá `true, true, true, false`, e `EnviadosHoje` dá 3.
- `TestTetoReservaExatamente50SobConcorrencia`: 60 goroutines chamam `Reservar` em réplicas diferentes (dois `*TetoDeEmailsDeConta` sobre o mesmo pool, teto 50). Exatamente 50 `true`; `EnviadosHoje` = 50.
- `TestTetoViraODiaEmUTC`: relógio falso em 23:59:59Z (reserva), depois 00:00:00Z: `EnviadosHoje` = 0 e uma nova reserva passa.
- `TestTetoApagaDiasVelhos`: uma linha de 8 dias atrás (inserida como admin) some depois de uma `Reservar`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/storage/ -run 'TokenDeConta|DonoApaga|InAccountTokenTx|InsertSignupUser|Teto'`
Expected: FAIL de compilação (`InAccountTokenTx`, `NewTetoDeEmailsDeConta`, queries indefinidas).

- [ ] **Step 3: Escrever a migration**

`00047_cadastro_publico.sql`, com `SET LOCAL lock_timeout = '5s'` como as outras:
- `account_tokens`:
  - colunas:
    - `token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32)`;
    - `user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE`;
    - `purpose text NOT NULL CHECK (purpose IN ('confirm_email','reset_password'))`;
    - `created_at timestamptz NOT NULL`;
    - `expires_at timestamptz NOT NULL CHECK (expires_at > created_at)`;
    - `used_at timestamptz`;
  - índice `(user_id, purpose)`;
  - `ENABLE` + `FORCE ROW LEVEL SECURITY`;
  - política `SELECT`: `token_hash = decode(NULLIF(current_setting('app.account_token_hash', true), ''), 'hex') OR user_id = NULLIF(current_setting('app.user_id', true), '')::uuid`. O ramo do dono é necessário porque o `DELETE … WHERE user_id = …` também passa pela política de SELECT;
  - política `UPDATE`: só o ramo do hash, em `USING` e `WITH CHECK`;
  - políticas `INSERT` (`WITH CHECK`) e `DELETE` (`USING`): só o ramo do dono.
- `account_email_daily`:
  - colunas `day date PRIMARY KEY`, `sent integer NOT NULL CHECK (sent >= 0)`;
  - RLS `FORCE`, com uma política `FOR ALL` em `current_setting('app.scope', true) = 'account_email'`.
- Down: `DROP TABLE account_email_daily; DROP TABLE account_tokens;`.

- [ ] **Step 4: Escrever as queries e os escopos**

A reserva precisa ser um único comando, porque o `ON CONFLICT DO UPDATE` trava a linha e avalia o `WHERE` na versão mais nova. Nenhuma linha devolvida significa teto atingido:

```sql
-- name: ReserveAccountEmail :one
WITH velhos AS (DELETE FROM account_email_daily WHERE day < @day::date - 7)
INSERT INTO account_email_daily AS d (day, sent) VALUES (@day::date, 1)
ON CONFLICT (day) DO UPDATE SET sent = d.sent + 1 WHERE d.sent < @teto::int
RETURNING d.sent;
```

`Reservar` traduz `pgx.ErrNoRows` em `false, nil`. O dia vem de `clk.Now().UTC()`. Os escopos novos copiam `InVerifyTx`/`InIPRateLimitTx`. Em `guarda_escopo_test.go`, inclua em `chamadoresDeScopeToUser`:
- `internal/account/cadastro.go`: "signup: o usuário nasce na transação e o escopo é ligado para o Projeto padrão e o token";
- `internal/account/recuperacao.go`: "reset: o dono só é conhecido depois de consumir o token".

Rode `make generate`.

- [ ] **Step 5: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/storage/...`
Expected: PASS, inclusive `TestEveryTableForcesRLSWithAPolicy` e `sqlc_orfao`.

Sabotagens, uma por vez, cada uma derrubando o teste indicado:
- tirar o `WHERE d.sent < @teto` (cai o teste de concorrência);
- tirar `FORCE ROW LEVEL SECURITY` de `account_tokens` (cai `TestEveryTableForcesRLSWithAPolicy`);
- trocar a política de UPDATE para `USING (true)` (cai `TestConsumoExigeOHashDoEscopo`).

- [ ] **Step 6: Commit**

```bash
git add internal/storage
git commit -m "feat(storage): tokens de conta, teto diário de e-mails e queries do cadastro (00047)"
```

### Task 2: helper do Projeto padrão, CreateUser sem trava de usuário único, porta de e-mail de conta

**Files:**
- Create: `internal/storage/projeto_padrao.go`, `internal/storage/projeto_padrao_test.go`, `internal/account/tokens.go`, `internal/account/emails.go`, `internal/account/emails_test.go`
- Modify: `internal/account/account.go` (`CreateUser`, `criarProjetoPadrao`, `inTx`, remover `HasUsers` e `depoisDeContar`), `internal/account/export_test.go`, `internal/account/account_test.go:85`, `internal/account/projeto_padrao_test.go:141`, `internal/tenancy/pgstore/store.go:75-102`, `cmd/stillon/user.go:257-280`, `cmd/stillon/user_test.go`

**Interfaces:**
- Consumes: queries existentes `EnsureDefaultProject`, `GetDefaultProjectID`, `EnsureProjectOwner`; `tenancy.DefaultProjectName`.
- Produces:
  - `func storage.CriarProjetoPadraoNaTx(ctx context.Context, q *db.Queries, userID uuid.UUID, now time.Time) (uuid.UUID, error)`: supõe o escopo do usuário já ligado, gera o UUIDv7 e é idempotente;
  - `func emTx(ctx context.Context, pool *pgxpool.Pool, fn func(*db.Queries) error) error` (pacote `account`; `Service.inTx` delega a ela);
  - `var ErrEmailTaken = errors.New(...)`;
  - `type AccountMailer interface { SendAccountEmail(ctx context.Context, to, subject, body string) error }`, documentada como "enfileira e volta; não espera a rede";
  - `type Links struct{ Base string }` com `ConfirmarEmail(token string) string` (`Base+"/confirm-email#token="+token`), `RedefinirSenha(token string) string` (`/reset-password#token=`) e `EsqueciASenha() string` (`/forgot-password`);
  - `func emailDeConfirmacao(link string) (assunto, corpo string)`, `func emailJaTemConta(linkEsqueci string) (assunto, corpo string)` e `func emailDeRedefinicao(link string) (assunto, corpo string)`;
  - `func novoTokenDeConta() (token string, hash []byte, err error)`: 32 bytes de `crypto/rand`, `base64.RawURLEncoding`, hash = `crypto.HashToken(token)`;
  - `const ValidadeDaConfirmacao = 24 * time.Hour`, `const ValidadeDaRedefinicao = time.Hour`;
  - `var ErrTokenInvalido = errors.New(...)`;
  - `const propositoConfirmacao = "confirm_email"`, `const propositoRedefinicao = "reset_password"`.

- [ ] **Step 1: Escrever os testes que falham**

- `storage/projeto_padrao_test.go`:
  - `TestCriarProjetoPadraoNaTxEIdempotente`: duas chamadas no mesmo escopo dão o mesmo id, 1 linha em `projects` com `is_default` e 1 em `project_members` com `role = 'owner'`;
  - `TestCriarProjetoPadraoNaTxSemEscopoFalha`: sem `ScopeToUser`, devolve erro e não grava nada.
- `account/account_test.go`, no caso de `account_test.go:85`: `TestCreateUserAceitaSegundaConta` (e-mails diferentes → os dois criados, cada um com o seu Projeto padrão) e `TestCreateUserRecusaEmailRepetido` (`errors.Is(err, account.ErrEmailTaken)`, também com caixa diferente).
- `account/projeto_padrao_test.go:141`: a segunda criação com outro e-mail passa a ser sucesso.
- `account/emails_test.go`:
  - `TestLinksLevamOTokenNoFragmento`: `Links{Base: "https://stillon.tech"}.ConfirmarEmail("abc")` == `"https://stillon.tech/confirm-email#token=abc"`, e o mesmo para `RedefinirSenha`;
  - `TestTokenDeContaTem256Bits`: o token decodificado tem 32 bytes, dois tokens diferem, e `hash` == `crypto.HashToken(token)`;
  - `TestEmailsLevamOLink`: cada corpo contém o link recebido e nenhum contém `{{`.
- `cmd/stillon/user_test.go`: o teste que esperava "já existe um usuário" passa a criar com o **mesmo** e-mail e espera a saída `saidaJaExiste`. Um segundo e-mail passa a ser `saidaOK`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/storage/ ./internal/account/ ./cmd/stillon/ -run 'ProjetoPadrao|CreateUser|Links|TokenDeConta|Emails|User'`
Expected: FAIL (`CriarProjetoPadraoNaTx`, `ErrEmailTaken`, `Links` indefinidos; segunda conta recusada).

- [ ] **Step 3: Implementar**

- `CriarProjetoPadraoNaTx` encapsula a sequência hoje duplicada em `pgstore.criarPadrao` e `account.criarProjetoPadrao`. As duas passam a chamá-la: o pgstore dentro do seu `InUserTx`, o account depois do `ScopeToUser`.
- Em `CreateUser`, remova o bloco `LockUserCreation`/`CountUsers`/`ErrUserExists` e o gancho `depoisDeContar` (+ `SetDepoisDeContar`). Mapeie `23505` em `users_email_key` para `ErrEmailTaken`. `ErrUserExists` e `HasUsers` saem.
- `runCreate` confere antes da senha com `svc.UserIDByEmail`: achou → `saidaJaExiste`, "já existe uma conta com esse e-mail; nada foi alterado". `ErrEmailTaken` na corrida dá a mesma saída.
- Os textos de `emails.go` são rascunho sóbrio em pt-BR, com assunto curto e corpo de texto puro com o link e a validade. A T11 os submete ao dono.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/storage/ ./internal/account/... ./internal/tenancy/... ./cmd/stillon/`
Expected: PASS. Sabotagem: no helper, trocar `EnsureProjectOwner` por nada derruba `TestCriarProjetoPadraoNaTxEIdempotente` e os testes do pgstore.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/projeto_padrao*.go internal/account internal/tenancy/pgstore cmd/stillon/user.go cmd/stillon/user_test.go
git commit -m "refactor(account): helper comum do Projeto padrão, CreateUser sem usuário único e porta de e-mail de conta"
```

### Task 3: verificador do Turnstile, configuração e runbook

**Files:**
- Create: `internal/turnstile/turnstile.go`, `internal/turnstile/turnstile_test.go`, `internal/turnstile/rede_test.go`, `internal/turnstile/turnstiletest/servidor.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `.env.example`, `compose.yaml` (env do serviço app, perto da linha 109), `deploy/vps/env.exemplo`, `docs/deploy/vps.md`, `README.md` (tabela de env, perto da linha 76)
- **Não** tocar `deploy/coolify/app.json` nem `internal/config/infra_fixada_test.go`: isso é da seção "Ativação em produção".

**Interfaces:**
- Produces:
  - `var turnstile.ErrRecusado = errors.New("turnstile: desafio recusado")`;
  - `type Verificador struct{…}` com:
    - `func New(secret crypto.Secret, hostEsperado string) *Verificador` (cliente `safedial.NewClient(0)`, endpoint `https://challenges.cloudflare.com/turnstile/v0/siteverify`);
    - `func NewComEndpoint(secret crypto.Secret, hostEsperado, endpoint string, client *http.Client) *Verificador`;
    - `func (v *Verificador) Verificar(ctx context.Context, token, ip, acao string) error`.
  - `turnstiletest`:
    - constantes `SiteKeyPassa = "1x00000000000000000000AA"`, `SecretPassa = "1x0000000000000000000000000000000AA"`, `SecretFalha = "2x0000000000000000000000000000000AA"`, `SecretJaUsado = "3x0000000000000000000000000000000AA"`, `TokenFalso = "XXXX.DUMMY.TOKEN.XXXX"`;
    - `func Verificador(t testing.TB, secret string) *turnstile.Verificador`: siteverify falso em `httptest`, que imita a semântica documentada de cada secret de teste e grava os campos recebidos;
    - `func Recebidos(v *turnstile.Verificador) []url.Values` (ou equivalente) para os testes conferirem o formulário.
  - `Config`:
    - campos `TurnstileSiteKey string`, `TurnstileSecretKey string`, `AccountEmailFrom string`;
    - `func (c *Config) SignupEnabled() bool`: par do Turnstile && `EmailEnabled()`;
    - `func (c *Config) RemetenteDeConta() string`: `AccountEmailFrom` ou, vazio, `EmailFrom`;
    - `func (c *Config) HostDoTurnstile() string`: o host de `PublicBaseURL` quando `emProducao`, senão `""`.

- [ ] **Step 1: Escrever os testes que falham**

`turnstile_test.go`, com o servidor do `turnstiletest`:
- `TestVerificarAceitaOSecretQuePassa`: `SecretPassa` + `TokenFalso` → `nil`. O formulário recebido tem `secret`, `response`, `remoteip` e um `idempotency_key` não vazio.
- `TestVerificarRecusaOSecretQueFalha`: `SecretFalha` → `errors.Is(err, ErrRecusado)`.
- `TestVerificarRecusaTokenJaUsado`: `SecretJaUsado` → `ErrRecusado`.
- `TestVerificarRecusaSemRede`: token `""` e token de 2049 caracteres → `ErrRecusado`, e o servidor não recebe nada.
- `TestVerificarRecusaOutroHost`: servidor respondendo `hostname: "outro.test"` e `hostEsperado = "stillon.tech"` → `ErrRecusado`. Com `hostEsperado` vazio, passa.
- `TestVerificarRecusaOutraAcao`: resposta `action: "forgot"` com `acao = "signup"` → `ErrRecusado`. `action` vazia passa.
- `TestVerificarFalhaFechadoSemCloudflare`: servidor 500, corpo inválido, conexão recusada → erro que **não** é `ErrRecusado`.

`rede_test.go`: `TestSiteverifyDeVerdade` só roda com `TURNSTILE_REDE=1` (`t.Skip` sem ela). `SecretPassa` + `TokenFalso` → `nil`; `SecretFalha` → `ErrRecusado`.

`config_test.go`:
- `TestTurnstileExigeOPar`: só uma das duas → erro do `Load` citando `TURNSTILE_SITE_KEY e TURNSTILE_SECRET_KEY`;
- `TestSignupEnabled`: tabela [par+e-mail → true; par sem e-mail → false; e-mail sem par → false];
- `TestRemetenteDeContaCaiNoEmailFrom`;
- `TestAccountEmailFromSemResendFalha` (mesma regra de `exigirEmailPareado`);
- `TestHostDoTurnstileSoEmProducao`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 ./internal/turnstile/... ./internal/config/`
Expected: FAIL (pacote e campos inexistentes).

- [ ] **Step 3: Implementar**

`Verificar`:
1. Recusa sem rede token vazio ou com mais de 2048 caracteres.
2. Faz POST `application/x-www-form-urlencoded`, com o prazo do `ctx` limitado a 5 s e leitura do corpo limitada a 64 KiB.
3. Responde: `success=false` → `ErrRecusado` (registra os `error-codes` em `slog.Info`, sem o token); host diferente com `hostEsperado` não vazio → `ErrRecusado`; `action` não vazia e diferente → `ErrRecusado`; status diferente de 200 ou JSON inválido → `fmt.Errorf("turnstile: …: %w", err)`.

O secret nunca aparece em log nem em erro.

Docs:
- `.env.example` e `deploy/vps/env.exemplo`: as três variáveis vazias, com comentário. O cadastro só liga com o par do Turnstile e o e-mail configurados; as chaves de teste públicas servem só para dev.
- `compose.yaml` (dev): `${TURNSTILE_SITE_KEY:-1x00000000000000000000AA}` e `${TURNSTILE_SECRET_KEY:-1x0000000000000000000000000000000AA}`; `ACCOUNT_EMAIL_FROM: ${ACCOUNT_EMAIL_FROM:-}`.
- `README.md`: as três linhas na tabela de env.
- `docs/deploy/vps.md`: seção nova "Cadastro público: Turnstile e remetente de conta", no formato guiado do runbook (um passo numerado por vez, com **Confira**). Passos:
  1. criar a entrada "StillOn / Turnstile" no Bitwarden;
  2. no painel da Cloudflare, Turnstile → Add widget (nome StillOn, hostname `stillon.tech`, modo Managed) e colar site key e secret direto no Bitwarden;
  3. na Resend, adicionar o domínio do remetente de conta (sugestão: `conta.stillon.tech`) e criar os registros na área **DNS records** da Hostinger (não em Nameservers);
  4. verificar na Resend;
  5. cadastrar `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET_KEY` e `ACCOUNT_EMAIL_FROM` no recurso do app no Coolify, colando do Bitwarden;
  6. seguir a seção "Ativação em produção" deste plano (app.json + Passo 7).

  **Confira:** `curl -s https://stillon.tech/api/v1/auth/signup-config` mostra `"enabled":true`.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 ./internal/turnstile/... ./internal/config/`
Expected: PASS (o de rede pula). Sabotagem: ignorar `hostname` derruba `TestVerificarRecusaOutroHost`.

- [ ] **Step 5: Commit**

```bash
git add internal/turnstile internal/config .env.example compose.yaml deploy/vps/env.exemplo docs/deploy/vps.md README.md
git commit -m "feat(turnstile): verificador do siteverify, config do cadastro e passo do runbook"
```

### Task 4: enviador de e-mails de conta e remetente próprio

**Files:**
- Create: `internal/contaemail/contaemail.go`, `internal/contaemail/contaemail_test.go`, `internal/contaemail/fronteiras_test.go`
- Modify: `internal/notify/resend.go` (perto de `SendVerification`), `internal/notify/resend_test.go`

**Interfaces:**
- Consumes: `notify.VerificationMailer` (`service.go:66`); `crypto.HashIP`; `crypto.Secret`.
- Produces:
  - `func (r *Resend) SendAccount(ctx context.Context, to crypto.Secret, subject, body string) error`: usa `post` com chave de idempotência nova.
  - Interfaces locais de `contaemail`:
    - `type Transporte interface { SendAccount(ctx context.Context, to crypto.Secret, subject, body string) error }`;
    - `type Teto interface { Reservar(ctx context.Context) (bool, error) }`;
    - `type Orcamentos interface { Gastar(ctx context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error) }`.
  - `const TetoDiario = 50`; balde `"conta_destino"` com `aCada = 20 * time.Minute` e `rajada = 3`; fila com capacidade 100.
  - `type Enviador struct{…}` com:
    - `func New(t Transporte, teto Teto, o Orcamentos, chave []byte, log *slog.Logger) *Enviador`;
    - `func (e *Enviador) SendAccountEmail(ctx context.Context, to, subject, body string) error`: enfileira e volta `nil`; fila cheia → log e `nil`;
    - `func (e *Enviador) Rodar(ctx context.Context)`: o consumidor. Com `ctx` encerrado, drena o que restou por até 10 s e volta. Cada envio tem prazo próprio de 15 s.
  - `func NovaVerificacaoComTeto(e *Enviador, m notify.VerificationMailer) notify.VerificationMailer`: síncrono. Balde → teto → `m.SendVerification`. Suprimido → `nil`; erro do transporte → o erro.
  - `type SemEnvio struct{ Log *slog.Logger }`, que implementa `SendAccountEmail` registrando no log e devolvendo `nil` (e-mail desligado).
  - A ordem é sempre esta: balde do destinatário (`HashIP(chave, "conta_destino:"+auth.NormalizeEmail(to))`), depois o teto, depois o transporte.

- [ ] **Step 1: Escrever os testes que falham**

Com falsos de `Transporte`, `Teto` e `Orcamentos`, e `Rodar` numa goroutine:
- `TestEnviaQuandoHaBaldeETeto`: um e-mail chega ao transporte com destinatário, assunto e corpo intactos.
- `TestBaldeVazioNaoGastaOTeto`: com o balde recusando, o transporte e o `Reservar` recebem 0 chamadas.
- `TestTetoEsgotadoSuprime`: `Reservar` → `false`, o transporte recebe 0, e o log tem `motivo=teto` e nenhum endereço.
- `TestFilaCheiaNaoBloqueia`: com o consumidor parado, 101 chamadas a `SendAccountEmail` voltam todas em menos de 100 ms.
- `TestRodarDrenaAoEncerrar`: 3 enfileirados, `cancel()` → os 3 chegam ao transporte antes de `Rodar` voltar.
- `TestVerificacaoComTetoPassaPeloTeto`: teto esgotado → `SendVerification` devolve `nil` e o falso recebe 0; erro do transporte é devolvido.
- `TestChaveDoBaldeIgnoraCaixa`: `"A@X.com"` e `"a@x.com"` gastam a mesma chave.

`fronteiras_test.go` (lendo os imports com `go/parser`, como os outros `fronteiras_test.go`): `TestNotifyNaoImportaContaemail`, para que nenhum caminho dos avisos passe pelo teto.

`resend_test.go`: `TestSendAccountUsaORemetenteDaInstancia` (servidor falso confere `from`, `to`, `subject`, `text` e o cabeçalho `Idempotency-Key`).

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 ./internal/contaemail/ ./internal/notify/ -run 'Envia|Balde|Teto|Fila|Drena|Verificacao|Chave|NotifyNao|SendAccount'`
Expected: FAIL (pacote inexistente, `SendAccount` indefinido).

- [ ] **Step 3: Implementar** com as assinaturas acima.

Os logs de supressão usam `slog.Warn("e-mail de conta suprimido", "motivo", "teto"|"destino"|"fila")`. Erro do transporte vai em `slog.Error` sem o endereço.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 ./internal/contaemail/ ./internal/notify/`
Expected: PASS. Sabotagem: inverter a ordem balde/teto derruba `TestBaldeVazioNaoGastaOTeto`.

- [ ] **Step 5: Commit**

```bash
git add internal/contaemail internal/notify/resend.go internal/notify/resend_test.go
git commit -m "feat(contaemail): enviador de e-mails de conta com balde por destinatário e teto diário"
```

### Task 5: serviço de cadastro (signup, confirmação, reenvio)

**Files:**
- Create: `internal/account/cadastro.go`, `internal/account/cadastro_test.go`, `internal/account/export_cadastro_test.go`

**Interfaces:**
- Consumes:
  - da T1: `InsertSignupUser`, `InsertAccountToken`, `DeleteAccountTokens`, `ConsumeAccountToken`, `MarkEmailVerified`, `GetAccount`, `storage.InAccountTokenTx`;
  - da T2: `emTx`, `storage.CriarProjetoPadraoNaTx`, `AccountMailer`, `Links`, `novoTokenDeConta`, `ValidadeDaConfirmacao`, `ErrTokenInvalido`, os textos;
  - existentes: `audit`, `ValidateEmail`, `auth.ValidateNewPassword`, `auth.HashPassword`.
- Produces:
  - `type Cadastro struct{…}` com `func NewCadastro(pool *pgxpool.Pool, clk clock.Clock, mailer AccountMailer, links Links) *Cadastro`;
  - `func ValidarCadastro(email, senha string) (string, error)`: devolve o e-mail normalizado, ou `ErrInvalidEmail` / `PolicyError`;
  - `func (c *Cadastro) Cadastrar(ctx context.Context, email, senha string) error`: `nil` com conta nova ou existente; erro só de infraestrutura (inclusive `auth.ErrArgon2Busy`);
  - `func (c *Cadastro) ConfirmarEmail(ctx context.Context, token string) error`: `ErrTokenInvalido` ou infraestrutura;
  - `func (c *Cadastro) ReenviarConfirmacao(ctx context.Context, userID uuid.UUID) error`: já confirmado → `nil` sem e-mail;
  - `export_cadastro_test.go`: `func (c *Cadastro) SetHash(fn func(context.Context, string) (string, error))`.

- [ ] **Step 1: Escrever os testes que falham** (falso de `AccountMailer` que grava `to`, `subject` e `body`; relógio falso)

- `TestCadastrarContaNovaCriaUsuarioProjetoEToken`:
  - existe 1 usuário com `email_verified_at IS NULL` e `is_operator = false`;
  - existe o Projeto padrão com owner;
  - existe 1 token `confirm_email` com `expires_at = now + 24h`;
  - o mailer recebeu 1 e-mail cujo corpo contém `links.ConfirmarEmail(token)`;
  - o audit tem `user.signed_up`.
- `TestCadastrarContaExistenteMandaJaTemConta`: com a conta já criada, `Cadastrar` → `nil`, nenhum usuário novo, nenhum token novo, 1 e-mail cujo corpo contém `links.EsqueciASenha()`. Vale para conta confirmada **e** não confirmada (subtestes).
- `TestCadastrarComCaixaDiferenteContaComoExistente`: `"Fulano@X.com"` depois de `"fulano@x.com"` → ramo "já tem conta".
- `TestCadastrarFazOMesmoTrabalhoNosDoisRamos`: com o hasher falso contando, os dois ramos chamam o hash exatamente 1 vez e o mailer exatamente 1 vez.
- `TestCadastrarLevaTempoParecidoNosDoisRamos`: hasher falso de 50 ms, 5 rodadas por ramo; a diferença das medianas fica abaixo de 40 ms.
- `TestCadastrarValidaAntesDeTudo`: e-mail inválido → `ErrInvalidEmail`, senha de 14 → `PolicyError`, e o hash e o mailer recebem 0 chamadas.
- `TestConfirmarEmailMarcaEConsome`: o token do e-mail → `nil`, `email_verified_at` preenchido, audit `user.email_verified`. Repetir → `ErrTokenInvalido`.
- `TestConfirmarEmailVencido`: relógio + 24 h + 1 s → `ErrTokenInvalido`, e o e-mail continua não confirmado.
- `TestConfirmarEmailTokenLixo`: `"x"` e `""` → `ErrTokenInvalido`.
- `TestConfirmacaoSimultaneaSoUmaVence`: 20 goroutines com o mesmo token → exatamente 1 `nil` e 19 `ErrTokenInvalido`.
- `TestReenviarInvalidaOLinkAnterior`: `ReenviarConfirmacao` → e-mail novo; o token antigo → `ErrTokenInvalido`; o novo confirma.
- `TestReenviarJaConfirmadoNaoManda`: 0 e-mails, `nil`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/account/ -run 'Cadastrar|ConfirmarEmail|Confirmacao|Reenviar'`
Expected: FAIL (`NewCadastro` indefinido).

- [ ] **Step 3: Implementar**

`Cadastrar`:
1. `ValidarCadastro`.
2. `hash` **sempre**, antes de qualquer consulta.
3. `emTx`:
   - `InsertSignupUser` (UUIDv7). Sem linha → ramo existente.
   - Com linha: `ScopeToUser`, `CriarProjetoPadraoNaTx`, `DeleteAccountTokens(confirm)`, `InsertAccountToken` e `audit("user.signed_up")`.
4. Depois do commit, **um** `SendAccountEmail`: confirmação ou "já tem conta", para o e-mail normalizado.

`ConfirmarEmail`: `InAccountTokenTx(crypto.HashToken(token))` → `ConsumeAccountToken(confirm)`; `pgx.ErrNoRows` → `ErrTokenInvalido`. Depois `MarkEmailVerified` e `audit`, na mesma transação.

`ReenviarConfirmacao`: `GetAccount` → já confirmado sai. Senão `emTx`, com `ScopeToUser`, `DeleteAccountTokens` e `InsertAccountToken`, e depois o e-mail.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/account/... ./internal/storage/`
Expected: PASS, inclusive `guarda_escopo_test`. Sabotagens:
- pular o hash no ramo existente derruba `TestCadastrarFazOMesmoTrabalhoNosDoisRamos`;
- trocar o `ConsumeAccountToken` por `PeekAccountToken` derruba os testes de reuso e de concorrência.

- [ ] **Step 5: Commit**

```bash
git add internal/account/cadastro.go internal/account/cadastro_test.go internal/account/export_cadastro_test.go
git commit -m "feat(account): cadastro público com confirmação de e-mail sem enumeração"
```

### Task 6: serviço de recuperação (esqueci a senha, redefinição)

**Files:**
- Create: `internal/account/recuperacao.go`, `internal/account/recuperacao_test.go`, `internal/account/export_recuperacao_test.go`

**Interfaces:**
- Consumes:
  - da T1: `PeekAccountToken`, `ConsumeAccountToken`, `DeleteAccountTokens`, `InsertAccountToken`, `MarkEmailVerified`, `storage.InAccountTokenTx`;
  - da T2: `emTx`, `AccountMailer`, `Links`, `novoTokenDeConta`, `ValidadeDaRedefinicao`, `ErrTokenInvalido`, `emailDeRedefinicao`;
  - a sequência de revogação de `Service.ResetPassword`: `ScopeToUser`, `LockUserSessions`, `ReplaceUserHash`, `RevokeAllFamilies`.
- Produces:
  - `type Recuperacao struct{…}` com `func NewRecuperacao(pool *pgxpool.Pool, clk clock.Clock, mailer AccountMailer, links Links) *Recuperacao`;
  - `func (r *Recuperacao) PedirRedefinicao(ctx context.Context, email string) error`: `nil` com conta ou sem conta;
  - `func (r *Recuperacao) Redefinir(ctx context.Context, token, novaSenha string) error`: `ErrTokenInvalido`, `PolicyError`, `auth.ErrArgon2Busy` ou infraestrutura;
  - `export_recuperacao_test.go`: `SetHash`.

- [ ] **Step 1: Escrever os testes que falham**

- `TestPedirRedefinicaoComContaMandaLink`: 1 token `reset_password` com `expires_at = now + 1h`, e 1 e-mail com `links.RedefinirSenha(token)`.
- `TestPedirRedefinicaoSemContaNaoManda`: `nil`, 0 e-mails, 0 tokens.
- `TestPedirDeNovoInvalidaOLinkAnterior`: dois pedidos; o primeiro token → `ErrTokenInvalido` em `Redefinir`, o segundo passa.
- `TestRedefinirTrocaSenhaERevogaSessoes`:
  - preparação: criar duas sessões (famílias de refresh) com `Service.Login`/helper existente;
  - `Redefinir` → `nil`;
  - o login com a senha antiga falha e com a nova passa;
  - as duas famílias anteriores estão revogadas;
  - o audit tem `user.password_reset_by_email`.
- `TestRedefinirTomaContaPreCriadaEDerrubaSessoes`: o atacante cadastra o e-mail (sem confirmar) e tem sessão; a vítima pede a redefinição e redefine. Resultado: e-mail confirmado, a sessão do atacante revogada, a senha do atacante não entra mais.
- `TestRedefinirMantemO2FA`: com TOTP ligado, continua ligado depois da redefinição.
- `TestRedefinirTokenUsadoOuVencido`: repetir → `ErrTokenInvalido`; relógio + 1 h + 1 s → `ErrTokenInvalido`, com a senha intacta.
- `TestRedefinirSenhaFracaNaoGastaToken`: 14 caracteres → `PolicyError`, e o mesmo token ainda redefine depois.
- `TestRedefinirTokenInvalidoNaoRodaArgon2`: com o hasher falso contando, um token lixo → 0 hashes.
- `TestRedefinicaoSimultaneaSoUmaVence`: 10 goroutines com o mesmo token e senhas diferentes → exatamente 1 `nil`, e a senha final é a do vencedor.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/account/ -run 'PedirRedefinicao|PedirDeNovo|Redefini'`
Expected: FAIL (`NewRecuperacao` indefinido).

- [ ] **Step 3: Implementar**

`PedirRedefinicao`:
1. Normaliza e busca `GetUserIDByEmail`. Sem conta → `nil`.
2. `emTx`: `ScopeToUser`, `DeleteAccountTokens(reset)` e `InsertAccountToken(1h)`.
3. Envia o e-mail.

`Redefinir`:
1. `auth.ValidateNewPassword`.
2. `InAccountTokenTx` + `PeekAccountToken`. Inválido → `ErrTokenInvalido`.
3. Hash **fora** da transação.
4. `InAccountTokenTx`:
   - `ConsumeAccountToken` (`ErrNoRows` → `ErrTokenInvalido`, porque perdeu a corrida);
   - `ScopeToUser(uid)`, `LockUserSessions`, `ReplaceUserHash`, `RevokeAllFamilies(UserID, Now)`;
   - `MarkEmailVerified`, porque o reset prova a posse da caixa;
   - `DeleteAccountTokens(uid, reset)`;
   - `audit`.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/account/... ./internal/storage/`
Expected: PASS. Sabotagens:
- tirar `RevokeAllFamilies` derruba os dois testes de sessão;
- tirar `MarkEmailVerified` derruba `TestRedefinirTomaContaPreCriadaEDerrubaSessoes`.

- [ ] **Step 5: Commit**

```bash
git add internal/account/recuperacao.go internal/account/recuperacao_test.go internal/account/export_recuperacao_test.go
git commit -m "feat(account): esqueci a senha com link de uso único e revogação das sessões"
```

### Task 7: trava "sem e-mail confirmado não cria", `email_verified` no `/me` e códigos de erro

**Files:**
- Create: `internal/httpapi/middleware/email_confirmado.go`, `internal/httpapi/middleware/email_confirmado_test.go`, `internal/httpapi/email_confirmado_integration_test.go`
- Modify:
  - `internal/account/account.go:86-110` (`Account`, `Me`, `EmailConfirmado`) e `internal/account/account_test.go`;
  - `internal/httpapi/accounthandlers.go` (`meResponse`), `internal/apierr/errors.go`, `internal/httpapi/routes.go` (`Deps` e as rotas de criação);
  - `web/src/test/api-falsa.ts:62-84` (`ERROS_DO_SERVIDOR`);
  - fixtures que inserem usuário sem `email_verified_at`: `internal/httpapi/auth_integration_test.go:127` (`newAPICom`), `risco6a10_internal_test.go`, `targets_integration_test.go`, `totp_falha_interna_internal_test.go`, `totp_limite_conta_integration_test.go`, e os e2e de `cmd/stillon` que criarem recurso;
  - `Deps` sem banco: `erro_forcado_sem_banco_test.go`, `limites_por_ip_test.go`, `prazo_requisicao_test.go`.

**Interfaces:**
- Consumes: `UserEmailVerified` e `GetAccount.email_verified_at` (T1); `emTx` (T2).
- Produces:
  - `Account.EmailVerified bool`;
  - `func (s *Service) EmailConfirmado(ctx context.Context, userID uuid.UUID) (bool, error)`;
  - `type middleware.ConferidorDeEmail interface { EmailConfirmado(ctx context.Context, userID uuid.UUID) (bool, error) }`;
  - `func middleware.ExigeEmailConfirmado(c ConferidorDeEmail) func(http.Handler) http.Handler`: falso → 403 `email_not_verified`, erro → 500 `internal_error`; roda depois do authn;
  - `Deps.EmailConfirmado middleware.ConferidorDeEmail`, obrigatório (pânico se nil, como `Projects`);
  - `apierr.CodeEmailNotVerified = "email_not_verified"` (403), `CodeTurnstileFailed = "turnstile_failed"` (400), `CodeTokenInvalid = "token_invalid"` (400);
  - JSON do `/me`: `"email_verified": bool`.

- [ ] **Step 1: Escrever os testes que falham**

- `middleware/email_confirmado_test.go`, com um conferidor falso:
  - `TestExigeEmailConfirmadoDeixaPassarConfirmado`: o próximo handler roda;
  - `TestExigeEmailConfirmadoRecusaNaoConfirmado`: 403, o corpo tem `"email_not_verified"` e o próximo não roda;
  - `TestExigeEmailConfirmadoErroDa500`.
- `account_test.go`: `TestMeTrazEmailVerified` (false para usuário sem confirmação inserido como no signup; true para o do `CreateUser`).
- `email_confirmado_integration_test.go`:
  - `TestContaNaoConfirmadaNaoCriaPorNenhumaRota`:
    - usuário autenticado, sem confirmação, com Projeto padrão;
    - percorre `rotasRegistradas(t)`; para cada `POST` fora de `postsQueNaoCriam`, envia `{}` com o curinga trocado pelo id do Projeto padrão do usuário (rotas `{projectID}`) ou por um id dele (clone);
    - espera 403 `email_not_verified`;
    - `postsQueNaoCriam` é um mapa rota → motivo, com `auth/*`, troca de senha, 2FA, sessões, ack de incidente, teste de canal e reenvio de verificação, e cada entrada justificada;
    - uma entrada que não está registrada reprova, como em `rotasPublicas`.
  - `TestContaNaoConfirmadaLeEEdita`: `GET /api/v1/targets` → 200; `GET /api/v1/me` → `email_verified: false`.
  - `TestContaConfirmadaCria`: o mesmo usuário com `email_verified_at` preenchido → `POST /api/v1/targets` válido → 201.
- `web/src/test/api-falsa.test.ts` já compara a tabela com o Go: sem atualizar `ERROS_DO_SERVIDOR`, ele falha (é o teste que falha do lado do painel).

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/httpapi/... ./internal/account/ -run 'ExigeEmail|EmailVerified|NaoConfirmada|ContaConfirmada'`
Expected: FAIL (middleware e campo inexistentes).

- [ ] **Step 3: Implementar**

- Os códigos e o `ERROS_DO_SERVIDOR`.
- O middleware.
- `Me` e `meResponse` com o campo novo.
- Em `routes.go`, um wrapper `criacao := middleware.ExigeEmailConfirmado(d.EmailConfirmado)`, aplicado depois de `protegida` em cada rota de criação:
  - `POST /api/v1/targets` e `/api/v1/projects/{projectID}/targets`;
  - `POST` de heartbeats e de `heartbeats/{id}/clone`, nas duas variantes;
  - `POST` de canais, nas duas variantes;
  - `POST` de páginas de status, nas duas variantes;
  - `POST /api/v1/projects`.
- Fixtures: acrescente `email_verified_at = now()` nos INSERTs de usuário dos testes listados (e em qualquer outro que a suíte apontar com 403 `email_not_verified`).
- Os três testes sem banco passam um conferidor falso que devolve `true`.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/httpapi/... ./internal/account/... ./cmd/stillon/` e `cd web && npm test -- api-falsa`
Expected: PASS. Sabotagem: tirar a trava do clone de heartbeat da variante de projeto derruba `TestContaNaoConfirmadaNaoCriaPorNenhumaRota`, apontando a rota.

- [ ] **Step 5: Commit**

```bash
git add internal/account internal/apierr internal/httpapi cmd/stillon web/src/test/api-falsa.ts
git commit -m "feat(httpapi): sem e-mail confirmado não cria recursos; email_verified no /me"
```

### Task 8: rotas públicas de cadastro e recuperação

**Files:**
- Create: `internal/httpapi/cadastrohandlers.go`, `internal/httpapi/cadastro_integration_test.go`
- Modify:
  - `internal/httpapi/routes.go` (`Deps` e registro, perto do login, linha 160);
  - `internal/httpapi/rotas_negar_por_padrao_integration_test.go:22-37` (`rotasPublicas`);
  - `internal/httpapi/dto_estrito_internal_test.go` (`corposEstritos`);
  - `internal/httpapi/auth_integration_test.go` (`newAPICom` com os campos novos);
  - os três testes de `Deps` sem banco.

**Interfaces:**
- Consumes:
  - `account.Cadastro` e `account.Recuperacao` (T5/T6), via as interfaces abaixo;
  - `account.ValidarCadastro`, `account.ErrTokenInvalido`, `account.PolicyError`, `account.ErrInvalidEmail`, `auth.ValidateNewPassword`;
  - `turnstile.ErrRecusado` e `turnstiletest` (T3);
  - `apierr.CodeTurnstileFailed` e `CodeTokenInvalid` (T7);
  - `middleware.RateLimitIPCompartilhado`, `middleware.ClientIP`, `RateLimitUserEvery`.
- Produces:
  - `type Cadastrador interface { Cadastrar(ctx context.Context, email, senha string) error; ConfirmarEmail(ctx context.Context, token string) error; ReenviarConfirmacao(ctx context.Context, userID uuid.UUID) error }`;
  - `type Recuperador interface { PedirRedefinicao(ctx context.Context, email string) error; Redefinir(ctx context.Context, token, novaSenha string) error }`;
  - `type VerificadorTurnstile interface { Verificar(ctx context.Context, token, ip, acao string) error }`;
  - `type ConfigCadastro struct{ Habilitado bool; SiteKey string }`;
  - campos novos de `Deps`: `Cadastro Cadastrador`, `Recuperacao Recuperador`, `Turnstile VerificadorTurnstile` (os três obrigatórios) e `CadastroPublico ConfigCadastro`;
  - o contrato HTTP da seção Paralelismo.
  - Baldes por IP (`ip_rate_limits`):
    - `{Nome: "signup", PorMinuto: 2, Rajada: 5}`;
    - `{Nome: "password_forgot", PorMinuto: 2, Rajada: 5}`;
    - `{Nome: "email_confirm", PorMinuto: 10, Rajada: 10}`;
    - `{Nome: "password_reset", PorMinuto: 10, Rajada: 5}`.
  - Reenvio: `protegida` + `RateLimitUserEvery(ctx, clk, 10*time.Minute, 3)`.
  - Ações do Turnstile: `"signup"` e `"forgot"`. Token com mais de 128 caracteres em confirm/reset → `token_invalid` sem consultar.

- [ ] **Step 1: Escrever os testes que falham** (banco real; `account.NewCadastro`/`NewRecuperacao` com mailer falso que grava; `turnstiletest.Verificador(t, SecretPassa)`)

- `TestSignupContaNovaEExistenteRespondemIgual`: dois signups, um com e-mail novo e um com existente. Status 202, corpo vazio e cabeçalhos iguais, fora `Date`/`X-Request-Id`/`Traceparent`.
- `TestSignupFluxoCompleto`:
  1. signup;
  2. o token sai do link do mailer falso (fragmento `#token=`);
  3. login → `/me` com `email_verified: false`;
  4. `POST /api/v1/targets` → 403;
  5. `POST /auth/email/confirm` → 204;
  6. `/me` → true;
  7. `POST /api/v1/targets` → 201.
- `TestConfirmarTokenReusadoOuLixo`: o segundo confirm → 400 `token_invalid`; `{"token": "x"}` → 400 `token_invalid`.
- `TestSignupTurnstileRecusado`: com `SecretFalha` → 400 `turnstile_failed`, nenhum usuário criado, 0 e-mails.
- `TestSignupValidaAntesDoTurnstile`: senha de 14 → 400 `validation_failed` (`password`/`too_short`), e o siteverify falso recebeu 0 chamadas.
- `TestSignupTurnstileForaDoAr`: verificador apontado para servidor 500 → 503 `unavailable`, nenhum usuário.
- `TestSignupEForgotDesligados`: `Habilitado: false` → `POST signup` e `POST forgot` → 404 `not_found`; `GET signup-config` → `{"enabled": false, "turnstile_site_key": ""}`.
- `TestSignupConfigLigado`: → `{"enabled": true, "turnstile_site_key": "1x00000000000000000000AA"}`.
- `TestForgotComESemContaRespondemIgual`: comparação como a do signup.
- `TestResetDerrubaSessaoEExigeLoginNovo`:
  1. login (cookie de refresh);
  2. forgot e reset;
  3. o refresh com o cookie antigo → 401;
  4. o login com a senha nova → 200.
- `TestResetLinkAnteriorInvalido`: dois forgot; reset com o primeiro token → 400 `token_invalid`.
- `TestReenvioExigeSessao`: sem token → 401; com sessão → 202 e 1 e-mail novo.
- `TestRespostasIdenticasComTetoEsgotado`: como o mailer é uma porta, o "teto esgotado" é um `Enviador` real (T4) com teto falso que recusa. Signup e forgot, com e sem conta, devem dar respostas byte a byte iguais às do teto livre, e 0 envios ao transporte.
- `TestSignupLimitePorIP`: o 6º signup seguido do mesmo IP → 429.
- `rotasPublicas`: as 5 rotas públicas novas, com o motivo ("é onde a conta nasce", "a credencial é o token do e-mail", "pedido anônimo protegido por Turnstile e limite por IP", "configuração pública do formulário"). `corposEstritos`: os 4 handlers que decodificam JSON.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./internal/httpapi/ -run 'Signup|Confirmar|Forgot|Reset|Reenvio|RespostasIdenticas|EveryRegisteredRoute|Estrit'`
Expected: FAIL (rotas inexistentes → 404/401).

- [ ] **Step 3: Implementar** `CadastroHandlers` em `cadastrohandlers.go`.

Ordem em signup e forgot:
1. desligado → 404;
2. `decodeJSON`;
3. validação de formato (`ValidarCadastro` / `ValidateEmail`), com erros por campo;
4. `Turnstile.Verificar(token, middleware.ClientIP(r, px), ação)`: `ErrRecusado` → `turnstile_failed`; outro erro → `unavailable`;
5. serviço;
6. 202.

Os erros de serviço vão para `internal` (o Argon2 cheio já vira 429 lá). Registre as rotas e os baldes acima.

- [ ] **Step 4: Rodar e ver passar; sabotar**

Run: `go test -race -count=1 -p 1 ./internal/httpapi/...`
Expected: PASS. Sabotagens:
- trocar a ordem validação/Turnstile derruba `TestSignupValidaAntesDoTurnstile`;
- devolver 409 no ramo existente (via um Cadastrador que erra) derruba `TestSignupContaNovaEExistenteRespondemIgual`.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): rotas públicas de cadastro, confirmação e redefinição de senha"
```

### Task 9: fiação no binário, guarda dos avisos e limpeza

**Files:**
- Modify: `cmd/stillon/main.go` (`Deps` em :223, `notificadores`, `verificador`), `cmd/stillon/fiacao_email_test.go`, `internal/storage/queries/users.sql` (remover `CountUsers` e `LockUserCreation`), `internal/storage/db/*` (gerado)
- Create: `cmd/stillon/teto_avisos_e2e_test.go`

**Interfaces:**
- Consumes: tudo o que T1–T8 produzem (ver a tabela de interfaces).
- Produces: o binário que liga o cadastro quando `cfg.SignupEnabled()`.

- [ ] **Step 1: Escrever os testes que falham**

- `fiacao_email_test.go`:
  - `TestVerificadorDeCanalPassaPeloTeto`: com e-mail ligado, `verificador(cfg)` devolve o decorador de `contaemail`, não `*notify.Resend`;
  - `TestNotificadoresNaoPassamPeloTeto`: o notificador de e-mail dos avisos é `*notify.Resend` com `from == cfg.EmailFrom`;
  - `TestRemetenteDeContaNaFiacao`: com `ACCOUNT_EMAIL_FROM` definido, o transporte de conta usa esse remetente.
- `teto_avisos_e2e_test.go`, `TestAvisoSaiComTetoDeContaEsgotado`:
  1. binário com Resend falso (`httptest`, como os e2e de aviso existentes);
  2. `account_email_daily` do dia com `sent = 50` (como admin);
  3. um alvo cai;
  4. o Resend falso recebe o e-mail do aviso;
  5. em seguida, um `POST /api/v1/auth/password/forgot` de conta existente (Turnstile de teste) **não** chega ao Resend falso.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -race -count=1 -p 1 ./cmd/stillon/ -run 'VerificadorDeCanal|Notificadores|RemetenteDeConta|TetoDeContaEsgotado'`
Expected: FAIL (fiação ausente).

- [ ] **Step 3: Implementar a fiação**

1. Instâncias e enviador:
   - `contaResend := notify.NewResend(chave, cfg.RemetenteDeConta())`;
   - `teto := storage.NewTetoDeEmailsDeConta(pool, clk, contaemail.TetoDiario)`;
   - `enviador := contaemail.New(contaResend, teto, limitesIP, cfg.IPHashKey, logger)`, rodando com `go enviador.Rodar(ctx)` junto dos outros trabalhos de fundo, com espera no desligamento.
   - Sem e-mail: `contaemail.SemEnvio{}`.
2. `verificador(cfg)` passa a devolver `contaemail.NovaVerificacaoComTeto(enviador, contaResend)`.
3. Serviços: `links := account.Links{Base: cfg.PublicBaseURL}`, `account.NewCadastro` e `account.NewRecuperacao`.
4. Turnstile: `turnstile.New(crypto.NewSecret(cfg.TurnstileSecretKey), cfg.HostDoTurnstile())`, com o construtor de segredo usado para a Resend.
5. `Deps`: `EmailConfirmado: contas`, `Cadastro`, `Recuperacao`, `Turnstile` e `CadastroPublico{Habilitado: cfg.SignupEnabled(), SiteKey: cfg.TurnstileSiteKey}`.
6. Remover `CountUsers` e `LockUserCreation` de `users.sql` e rodar `make generate`.

- [ ] **Step 4: Rodar a suíte inteira**

Run: `go build ./... && go vet ./... && make test`
Expected: PASS. Sabotagem: passar `notificadores` pelo `Enviador` derruba `TestAvisoSaiComTetoDeContaEsgotado` e `TestNotificadoresNaoPassamPeloTeto`.

- [ ] **Step 5: Commit e PR do 3a**

```bash
git add cmd/stillon internal/storage/queries/users.sql internal/storage/db
git commit -m "feat(stillon): fiação do cadastro público, enviador de conta e guarda dos avisos"
```

PR `feat: cadastro público (backend, PR 3a)`, sem `[skip ci]`. No corpo:
- o cadastro sai desligado;
- a seção "Ativação em produção" vem depois.

Depois do merge, confira o `Coolify release` do SHA, o `readyz` e `GET /api/v1/auth/signup-config` → `"enabled":false`.

---

## Sub-PR 3b: painel

### Task 10: contrato no cliente, API falsa e API imitada

**Files:**
- Modify: `web/src/lib/api/schemas.ts:46`, `web/src/lib/api/cliente.ts:204`, `web/src/test/fixtures.ts:134`, `web/src/test/api-falsa.ts` (rotas de `/auth`, perto do GET `/me` em :449), `web/e2e/api-imitada.ts`
- Test: `web/src/lib/api/cliente.test.ts`, `web/src/test/api-falsa.test.ts`

**Interfaces:**
- Consumes: o contrato HTTP (T8) e `email_verified` (T7).
- Produces:
  - `Me` += `email_verified: z.boolean()`, e `fixtures.me()` com `email_verified: true` por padrão;
  - `ConfigDoCadastro = z.object({ enabled: z.boolean(), turnstile_site_key: z.string() })`;
  - no `api`:
    - `configDoCadastro(sinal?: AbortSignal): Promise<ConfigDoCadastro>`;
    - `cadastrar(e: { email: string; password: string; turnstile_token: string }): Promise<void>`;
    - `confirmarEmail(token: string): Promise<void>`;
    - `reenviarConfirmacao(): Promise<void>`;
    - `pedirRedefinicao(e: { email: string; turnstile_token: string }): Promise<void>`;
    - `redefinirSenha(e: { token: string; new_password: string }): Promise<void>`.
  - As chamadas públicas vão pelo mesmo caminho do `/auth/login`: sem access token e sem tentativa de renovar em 401.
  - API falsa:
    - estado `cadastro: { habilitado: boolean; contas: Set<string>; tokens: Map<string, 'confirm_email' | 'reset_password'> }`;
    - corpos `estrita` do contrato;
    - `forcar` aceita os códigos novos.
  - `api-imitada.ts`:
    - as mesmas rotas;
    - `page.route('https://challenges.cloudflare.com/turnstile/v0/api.js*')` serve um script que define `window.turnstile = { render(el, o) { …; o.callback('XXXX.DUMMY.TOKEN.XXXX'); return 'w1' }, reset() {}, remove() {} }`.

- [ ] **Step 1: Escrever os testes que falham**

- `cliente.test.ts`:
  - `cadastrar envia o corpo exato e não manda Authorization`;
  - `confirmarEmail com token_invalid rejeita com ApiError code token_invalid`;
  - `me aceita email_verified`;
  - `configDoCadastro lê enabled e a site key`.
- `api-falsa.test.ts`:
  - `signup com campo a mais dá 400` (estrita);
  - `signup responde 202 igual com e sem conta`;
  - `confirm consome o token uma vez`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npm test -- cliente api-falsa`
Expected: FAIL (`cadastrar` inexistente; o schema `Me` não tem o campo).

- [ ] **Step 3: Implementar** com as assinaturas acima. Nenhum texto de tela nesta tarefa.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api web/src/test web/e2e/api-imitada.ts
git commit -m "feat(web): contrato do cadastro público no cliente e nas APIs de teste"
```

### Task 11: telas públicas, widget do Turnstile e CSP

**Files:**
- Create:
  - `web/src/features/conta/PaginaCadastro.tsx`, `PaginaConfirmarEmail.tsx`, `PaginaEsqueciSenha.tsx`, `PaginaRedefinirSenha.tsx`, `Turnstile.tsx`;
  - `web/src/features/conta/conta.test.tsx`, `web/src/features/conta/turnstile.test.tsx`;
  - `web/e2e/cadastro.spec.ts`.
- Modify: `web/src/app/rotas.tsx` (4 rotas públicas, irmãs de `/login`), `web/src/lib/api/mensagens.ts`, `internal/webui/webui.go` (`Politica`), `internal/webui/webui_test.go`, `internal/account/emails.go` (textos aprovados), `web/e2e/a11y.spec.ts`

**Interfaces:**
- Consumes: o `api.*` e `ConfigDoCadastro` (T10); componentes de `PaginaLogin.tsx` (`Marca`, `Titulo`, `Campo`, `Entrada`, `Botao`, `ErroDoFormulario`, `AvisoDeContexto`) e de `web/src/components/ui`.
- Produces:
  - `function Turnstile(p: { siteKey: string; acao: 'signup' | 'forgot'; aoResolver: (token: string | null) => void; reiniciar: number }): JSX.Element`:
    - carrega uma vez `https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit` (promessa no módulo);
    - `render` com `sitekey`, `action`, `callback`, `'expired-callback'` e `'error-callback'` (os dois últimos chamam `aoResolver(null)`), `theme: 'auto'` e `language: 'pt-br'`;
    - `reset` quando `reiniciar` muda;
    - `remove` ao desmontar.
  - Rotas `/signup`, `/confirm-email`, `/forgot-password` e `/reset-password`.
  - `mensagens.ts`:
    - `porCodigo` com `email_not_verified`, `turnstile_failed` e `token_invalid`;
    - `porCampo.password` com `too_short` e `too_long`.
  - Os textos aprovados, que a T12 reutiliza.

- [ ] **Step 1: Proposta ao dono (parar e esperar)**

Carregue as skills de design das Restrições globais. Monte **uma** proposta com todos os textos e o desenho:
- as quatro telas, nos estados vazio, enviando, erro, sucesso e link inválido ou vencido;
- o estado "confira seu e-mail" depois do signup e do forgot, sempre com o mesmo texto, com ou sem conta;
- o aviso de e-mail não confirmado e o botão "reenviar" (T12);
- os links "Criar conta" e "Esqueci a senha" no login (T12);
- as mensagens dos três códigos novos e dos campos;
- assunto e corpo dos três e-mails de `emails.go`.

Disposição: reuse o cartão do login. Mostre como o widget fica nos temas claro e escuro.

Entregue ao dono numa mensagem com as opções onde houver escolha real, recomende uma e **espere a aprovação** antes do Step 2.

- [ ] **Step 2: Escrever os testes que falham**

`conta.test.tsx` (MSW + fake de `window.turnstile`):
- `cadastro só habilita o botão com o desafio resolvido`;
- `cadastro mostra o estado de confira seu e-mail depois do 202`;
- `cadastro com turnstile_failed mostra a mensagem e reinicia o widget`;
- `cadastro mostra o erro de senha curta no campo`;
- `confirmar lê o token do fragmento, limpa a barra e confirma` (espera `history.replaceState` sem `#token` e o POST com o token);
- `confirmar com token_invalid mostra link inválido ou vencido com caminho para reenviar`;
- `esqueci mostra o mesmo texto com ou sem conta`;
- `redefinir envia new_password e leva ao login com aviso de senha trocada`;
- `as telas públicas com cadastro desligado mostram que o cadastro está fechado` (`/signup` e `/forgot-password` com `enabled: false`).

`turnstile.test.tsx`:
- `carrega o script uma vez para dois widgets`;
- `expirado chama aoResolver com null`.

`webui_test.go`: a política esperada passa a ter `script-src 'self' https://challenges.cloudflare.com` e `frame-src https://challenges.cloudflare.com`; o resto fica idêntico.

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd web && npm test -- conta turnstile` e `go test -count=1 ./internal/webui/`
Expected: FAIL.

- [ ] **Step 4: Implementar** as telas, o widget, as rotas, a CSP, as mensagens e os textos de `emails.go`, como aprovados.

- [ ] **Step 5: e2e e acessibilidade**

`cadastro.spec.ts` (`api-imitada`), cobrindo:
- signup até "confira seu e-mail";
- `/confirm-email#token=…` com sucesso e inválido;
- forgot;
- reset até o login.

Em `a11y.spec.ts`, inclua as 4 rotas nos dois temas.

Run: `cd web && npm run e2e -- cadastro a11y`
Expected: PASS, sem violação do axe nem erro de CSP no console.

- [ ] **Step 6: Rodar tudo**

Run: `cd web && npm test && npm run typecheck && npm run lint` e `go test -count=1 ./internal/webui/ ./internal/account/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/conta web/src/app/rotas.tsx web/src/lib/api/mensagens.ts web/e2e/cadastro.spec.ts web/e2e/a11y.spec.ts internal/webui internal/account/emails.go
git commit -m "feat(web): telas de cadastro, confirmação e redefinição com Turnstile"
```

### Task 12: aviso de e-mail não confirmado e links no login

**Files:**
- Create: `web/src/app/AvisoDeConfirmacao.tsx`, `web/src/app/aviso-de-confirmacao.test.tsx`, `web/e2e/conta-nao-confirmada.spec.ts`
- Modify: `web/src/app/Layout.tsx`, `web/src/app/layout.test.tsx`, `web/src/features/login/PaginaLogin.tsx`, `web/src/features/login/login.test.tsx`

**Interfaces:**
- Consumes: `api.reenviarConfirmacao`, `api.configDoCadastro` e `Me.email_verified` (T10); os textos aprovados no Step 1 da T11.
- Produces: `function AvisoDeConfirmacao(p: { email: string }): JSX.Element`, montado pelo `Layout` quando `me.email_verified === false`.

- [ ] **Step 1: Proposta ao dono**

Use a proposta aprovada no Step 1 da T11 (aviso, botão, links). Se ela ainda não foi aprovada, espere. Se algo daqui não estava nela, mostre ao dono antes de seguir.

- [ ] **Step 2: Escrever os testes que falham**

- `aviso-de-confirmacao.test.tsx`:
  - `mostra o e-mail e o botão de reenviar`;
  - `reenviar chama a API, confirma o envio e desabilita o botão por 60 s` (relógio falso);
  - `erro 429 mostra a mensagem de limite`.
- `layout.test.tsx`: `mostra o aviso só com email_verified false`.
- `login.test.tsx`: `mostra criar conta e esqueci a senha com o cadastro ligado` e `esconde os links com o cadastro desligado ou com a config falhando`.

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd web && npm test -- aviso-de-confirmacao layout login`
Expected: FAIL.

- [ ] **Step 4: Implementar** o aviso no topo do conteúdo do `Layout`, com `role="status"` e sem roubar o foco, e os links do login condicionados a `configDoCadastro().enabled`.

- [ ] **Step 5: e2e**

Em `conta-nao-confirmada.spec.ts` (`api-imitada` com `me.email_verified: false`):
- o aviso aparece;
- reenviar funciona;
- criar monitor mostra a mensagem de `email_not_verified`;
- axe nos dois temas.

Run: `cd web && npm run e2e -- conta-nao-confirmada`
Expected: PASS.

- [ ] **Step 6: Rodar tudo e commit; PR do 3b**

Run: `cd web && npm test && npm run typecheck && npm run lint`

```bash
git add web/src/app web/src/features/login web/e2e/conta-nao-confirmada.spec.ts
git commit -m "feat(web): aviso de e-mail não confirmado e links de cadastro no login"
```

PR `feat: cadastro público (painel, PR 3b)`, só depois do 3a no ar. Rebase sobre o 2b.4 se ele tiver entrado.

---

## Ativação em produção (fora das sub-PRs; só com o aval do dono)

O cadastro só liga quando o dono decidir, de preferência depois do PR 6 (termos). O passo a passo guiado fica em `docs/deploy/vps.md`, na seção da T3.

1. Criar o widget do Turnstile na conta Cloudflare do dono e guardar site key e secret no Bitwarden. Ninguém cola segredo no chat.
2. Verificar o domínio do remetente de conta na Resend.
3. Cadastrar `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET_KEY` e `ACCOUNT_EMAIL_FROM` no recurso do app no Coolify.
4. PR pequeno com:
   - as três variáveis em `deploy/coolify/app.json` como `${X:-}`;
   - os nomes, com valores sintéticos, em `variaveisDoRecurso()` de `internal/config/infra_fixada_test.go` (só depois do passo 3, como o teste exige);
   - a nova fixação de `deploy/coolify/infra-fixada` pelo Passo 7 de `deploy/coolify/release/README.md`.
5. **Confira:** `signup-config` com `"enabled":true` e um cadastro real com um e-mail do dono, recebido do remetente de conta.

## Portões

- **Antes de abrir o PR do 3a:** `make test` verde; nenhum segredo real no diff (`git diff main --stat` + `git grep -n "0x4AAA"` vazio; as chaves reais do Turnstile começam com `0x`); `sh deploy/ci/checar-nome.sh`.
- **Antes de abrir o PR do 3b:** 3a no ar (`Coolify release` verde e `readyz` 200); `npm run e2e` verde.
- **Ativação:** só com o "sim" do dono à pergunta dos termos.

## Autorrevisão

1. **Cobertura do spec:**

   | Requisito do spec | Onde está |
   |---|---|
   | signup sempre 202 | T5/T8 |
   | token de 256 bits, hash, 24 h | T1/T2/T5 |
   | sem confirmação: login entra, criação não | T7 |
   | forgot de 1 h que invalida links anteriores e revoga sessões | T6/T8 |
   | Turnstile no signup e no forgot | T3/T8/T11 |
   | `ip_rate_limits` para signup, reenvio e forgot | T8 (o reenvio fica no balde por usuário e no balde do destinatário) |
   | verificação de canal | T4/T9 |
   | convites | PR 4 |
   | teto de 50/dia no banco, entre réplicas, sem barrar avisos | T1/T4/T9 |
   | remetente de conta | T3/T4/T9 |
   | testes de enumeração, uso único, vencimento, revogação e Turnstile obrigatório | T5/T6/T8 |
   | portão do 2b: projetos na trava, Projeto padrão na transação do signup, helper comum, contador fora dos avisos | T7/T5/T2/T9 |
   | aceite dos termos | PR 6 (conflito decidido acima) |
   | `user usage` | fora (só log) |

2. **Passos:** cada teste tem nome e asserções; cada código tem assinatura e arquivo. O único corpo escrito é o SQL da reserva, porque a concorrência depende da forma exata.
3. **Tipos:**
   - `AccountMailer.SendAccountEmail(ctx, to, subject, body string)` é a mesma assinatura na T2, na T4 (`Enviador`, `SemEnvio`) e na T9;
   - `ConferidorDeEmail.EmailConfirmado` = `Service.EmailConfirmado`;
   - `Verificar(ctx, token, ip, acao)` vale igual na T3 e na T8;
   - `ErrTokenInvalido` aparece em T2, T5, T6 e T8.
4. **Foco da revisão:** os cinco itens têm teste na tarefa dona (T6, T5/T6, T1/T9, T8, T7).
5. **Proporção:** o plano é maior que a seção do spec porque cobre 12 tarefas com interfaces cruzadas. Os blocos de código se limitam ao SQL da reserva e às assinaturas.
