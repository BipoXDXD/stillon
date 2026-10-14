# Plano de implementação — vinculação Discord

> **Para agentes:** usar `superpowers:subagent-driven-development` para executar este plano por tarefa. Etapas usam caixas de seleção para registrar conclusão comprovada.

**Estado:** plano revisado em 2026-10-14 contra o código da `main` (8ca962d). **Não executar antes de o dono decidir D1, D2 e D3** (seção abaixo). Os passos que dependem delas levam a marca "depende de D1", "depende de D2" ou "depende de D3".

**Objetivo:** vincular uma conta Discord ao dono autenticado do StillOn e gerenciar o cargo Verificado como identificação, sem restringir canais.

**Arquitetura:** OAuth no backend com state e cookie de ponte independentes, ambos vinculados à família original. PostgreSQL registra vínculo e intenção durável; worker no papel API reconcilia somente o cargo configurado. Configurações consulta estado próprio, separado dos webhooks.

**Stack:** Go e net/http, pgx/sqlc/PostgreSQL, React/TypeScript/TanStack Query/Zod e as ferramentas de testes já presentes. Sem SDK Discord nem dependência nova prevista.

**Spec:** [especificação aprovada](../specs/2026-10-05-vinculacao-discord-design.md). O escopo aprovado da spec não muda neste plano; D1 e D2 podem pedir emenda dela se o dono escolher a opção que a altera.

## Decisões pendentes do dono

Três pontos da revisão técnica de 2026-10-14 mexem na spec aprovada ou abrem superfície nova. Ninguém decide por ele: o plano abaixo marca cada passo afetado e continua válido na opção recomendada ou na outra, com o ajuste descrito.

### D1. PKCE

| Opção | Prós | Contras |
|---|---|---|
| a. Acrescentar PKCE S256 (`code_challenge` na autorização, `code_verifier` na troca do code) | cumpre a regra de segurança do CLAUDE.md global (OAuth só com code + PKCE + `state` e `redirect_uri` exata); o Discord aceita `code_verifier` | muda a spec aprovada (que só tem state e cookie) |
| b. Ficar só com `state` e cookie de ponte | sem mudança na spec | descumpre a regra escrita |

**Recomendação: a.** Custo principal: emendar a spec e decidir onde o `code_verifier` fica (coluna da tentativa cifrada ou o próprio cookie de ponte), detalhe a fechar junto com a aprovação.

### D2. Mecanismo de cargo

| Opção | Prós | Contras |
|---|---|---|
| a. Manter como aprovado (leases, gerações, compensação, orçamento de tentativas, tombstone) | já foi revisado e aprovado | cerca de metade do código da fase |
| b. Reconciliar pelo estado desejado | bem menor; elimina gerações e compensação | muda a spec |

Na opção b, cada DiscordID ganha uma linha com `desired` (`present` ou `absent`), backoff, contador de tentativas e erro. O worker roda com advisory lock, aplica PUT ou DELETE idempotente do cargo e confere relendo o membro.

**Recomendação: b.** Custo principal: emendar a spec nas seções "Persistência e efeitos externos" e "Contratos" e refazer a lista de testes de corrida.

### D3. Provedor falso no E2E

| Opção | Prós | Contras |
|---|---|---|
| a. Build tag `e2e` | o binário de produção não muda | um segundo binário para manter |
| b. Variável de ambiente que aponta o provedor para loopback | simples | abre em produção uma porta de redirecionamento de segredos |
| c. Sem E2E de ida e volta | nenhuma superfície nova | o navegador não prova a URL limpa |

**Recomendação: a.** Custo principal: um segundo binário para manter; o de produção continua com origem HTTPS fixa do Discord.

## Ordem de integração

1. Lembretes, com a migration `00012`.
2. CHECKs das latências do rollup (PR #90), com a migration `00013`, validados pela `00014`.
3. Discord, com a migration `00015` (`internal/storage/migrations/00015_discord_links.sql`), só depois de a `00012`, a `00013` e a `00014` estarem na `main`. Ao rebasear, rodar `make generate` (sqlc) para regerar `internal/storage/db/*`; nunca mesclar à mão arquivo gerado.

A branch `feat/painel-vintage` (redesign) colide com a Tarefa 5 em `web/src/features/configuracoes/PaginaConfiguracoes.tsx`. Ela entra antes ou depois do Discord, nunca em paralelo.

## Restrições globais

- Cargo apenas identifica contas vinculadas: não bloqueia canais e não concede administração.
- Escopo OAuth mínimo: `identify`; tokens OAuth não são persistidos.
- State e nonce independentes, 32 bytes cada; validade de cinco minutos; consumo único. Com D1a, o par PKCE S256 entra junto (depende de D1).
- Refresh existente permanece HttpOnly/SameSite=Strict; cookie ponte usa Lax e caminho do callback.
- HTTP apenas com `DISCORD_ALLOW_INSECURE_LOOPBACK=true` e origem local validada; padrão false. Essa variável é só de desenvolvimento local: não vai para `deploy/vps/compose.prod.yaml` nem para `env.exemplo`.
- Worker consulta a cada dez segundos; dez tentativas; backoff máximo de quinze minutos, respeitando Retry-After maior (depende de D2: na opção b o contador e o backoff vivem na linha por DiscordID).
- Tudo em português do Brasil; commits assinados no formato `tipo(escopo): descrição em português`; main somente por PR; só mudança de documentação leva `[skip ci]` no título do commit e do PR.
- Em todo passo que termina em commit: rodar `sh deploy/ci/checar-nome.sh` antes de commitar.
- Teste novo requer sabotagem compilável detectada, restauração e GREEN; não usar segredos reais em gates.
- Gates finais: `go build ./...`, `go vet ./...`, `make test` e web. O `make test` usa o PG18 por padrão (o de produção); para o PG17 rodar com `TEST_POSTGRES_IMAGE=postgres:17-alpine@sha256:<digest>`, usando o digest da matriz em `.github/workflows/ci.yml` (linha 109). Fechar com o CI do HEAD final.
- Não interferir em volumes/contêineres preexistentes.

## Modelo de ameaças (STRIDE)

Cada ameaça tem uma mitigação e um teste que falha sem ela. Os nomes dos testes são propostos e se confirmam na implementação.

| Ameaça | Caso | Mitigação | Teste |
|---|---|---|---|
| Spoofing | callback vindo de outro navegador | cookie de ponte comparado ao `browser_hash` da tentativa; a família viva é conferida | `TestCallbackOutroNavegadorRejeitado` |
| Tampering | `state` adulterado | `state` só vale pelo hash gravado; adulterado não reserva tentativa | `TestCallbackStateAdulterado` |
| Repudiation | vínculo e desvínculo sem rastro | auditoria na mesma transação do vínculo (`InsertAuditEntry`) | `TestVinculoGravaAuditoriaNaMesmaTransacao` |
| Info disclosure | `code` no log ou no `next`; conflito que revela outra conta | retorno 303 fixo sem query; logs sem a query; `ErrIdentityInUse` vira `LastResult=failed` sem dado alheio | `TestCallbackNaoVazaCanarioEmLogNemResposta`, `TestIdentidadeEmUsoNaoRevelaOutraConta` |
| DoS | tentativas em excesso; 429 do Discord no worker | `RateLimitUserEvery` no início, limitador por IP no callback, backoff com teto e Retry-After | `TestBeginRateLimitPorUsuario`, `TestCallbackRateLimitPorIP`, `TestNextAttemptPreservesProviderWait` |
| Elevation | cargo que autoriza algo no produto; bot com Administrador | o produto nunca lê o cargo para autorizar; a Tarefa 6 confere "Gerenciar cargos" sem Administrador | teste de arquitetura: nenhum pacote fora de `discordlink` lê o estado do cargo; conferência manual na ativação |

## Foco da revisão

1. Logout durante a troca OAuth não pode concluir vínculo: teste de barreira na tarefa 3.
2. Cargo concedido após desvinculação exige reconciliação do estado atual: teste concorrente na tarefa 4 (depende de D2: na opção b a corrida é entre dois `desired` consecutivos).
3. Duplo clique ou callback repetido não pode trocar dois codes: testes nas tarefas 3 e 5.
4. Resposta externa ambígua não pode virar confirmação falsa: testes 429/timeout nas tarefas 2 e 4.
5. Retorno sem sessão não pode carregar code/state no `next`: prova HTTP e navegador na tarefa 5 (depende de D3 para a prova de navegador).

## Equipe, isolamento e ordem

A execução usa os agentes do projeto, de 3 a 6 por vez: `complexo` para o que é delicado
(persistência e RLS, reserva da tentativa, worker e corridas, revisão de segurança) e
`simples` para o que é mecânico (configuração, documentação, componentes e testes de
renderização). O coordenador (a sessão principal) possui configuração, handlers, composição
(`cmd/stillon/main.go`) e documentação; leva ao dono só as decisões importantes. Cada agente
trabalha em worktree exclusivo da fase; nenhum agente reverte trabalho alheio. Arquivos
gerados por sqlc não se editam à mão: `make generate`.

Tarefa 1 precede 3/4; tarefa 2 pode avançar em paralelo à 1. Congelar os contratos abaixo
antes de implementar 3/4/5. Mudança de interface exige aviso ao coordenador e atualização
dos consumidores. Cada tarefa termina em commit assinado e revisão; um PR funcional
reúne o fluxo completo, sem publicar uma função parcialmente ativada.

## Contratos compartilhados

Criar `internal/discordlink/types.go` e `internal/discordapi/types.go`. A interface do
cliente é declarada em `discordlink`, que a consome; `discordapi` exporta só o tipo concreto
e os tipos de dados. Handlers recebem o tipo concreto `*discordlink.LinkService` (sem
interface `Service`: há uma única implementação) e nunca acessam o token do bot.

```go
// package discordapi
type Identity struct { ID, DisplayName string }
type Member struct { Exists, Pending bool; Roles []string }

type FailureKind string

const (
    FailureRateLimited FailureKind = "rate_limited"
    FailureUnauthorized FailureKind = "unauthorized"
    FailureForbidden FailureKind = "forbidden"
    FailureServer FailureKind = "server"
    FailureTimeout FailureKind = "timeout"
    FailureInvalidResponse FailureKind = "invalid_response"
)

type Failure struct { Kind FailureKind; RetryAfter time.Duration }
func (e *Failure) Error() string { return "discord: " + string(e.Kind) }
```

```go
// package discordlink
// API é o que o serviço e o worker usam do Discord; discordapi.HTTPClient a satisfaz.
type API interface {
    IdentityFromCode(ctx context.Context, code, redirectURL string) (discordapi.Identity, error)
    Member(ctx context.Context, guildID, userID string) (discordapi.Member, error)
    AddRole(ctx context.Context, guildID, userID, roleID string) error
    RemoveRole(ctx context.Context, guildID, userID, roleID string) error
}

type Principal struct { UserID, FamilyID uuid.UUID }

// Status não tem tags JSON: a forma do JSON é do DTO do handler, em
// internal/httpapi, para a regra de domínio não depender do contrato HTTP.
type Status struct {
    Enabled bool
    State State
    DisplayName string
    LastResult LastResult
    ConfirmedAt *time.Time
    RetryAt *time.Time
    InviteURL string
}
type Start struct { AuthorizationURL, BrowserNonce string; ExpiresAt time.Time }
type Callback struct { State, BrowserNonce, Code, ProviderError string }
```

Métodos de `*LinkService`: `Get(ctx, Principal) (Status, error)`, `Begin(ctx, Principal) (Start, error)`,
`Complete(ctx, Callback) error`, `Check(ctx, Principal) (Status, error)` e `Unlink(ctx, Principal) (Status, error)`.
Com D1a, `Begin` também gera o par PKCE e `Complete` repassa o `code_verifier` a `IdentityFromCode`
(depende de D1; a assinatura se fecha na decisão).

`Status.State` admite somente `unavailable`, `unlinked`, `awaiting_member`,
`awaiting_screening`, `role_pending`, `confirmed`, `removal_pending`, `paused`.
`LastResult` admite vazio, `cancelled`, `expired`, `failed`, `linked`; sem texto do
provedor. Erros de domínio exportados: `ErrSessionGone`, `ErrInvalidAttempt`,
`ErrIdentityInUse`, `ErrUnavailable`, `ErrTooEarly`. `ErrIdentityInUse` não chega ao
cliente como erro próprio: vira `LastResult=failed` (ver Tarefa 3).
Construtores: `discordapi.NewClient(clientID string, clientSecret, botToken crypto.Secret) *HTTPClient`
e `NewService(pool *pgxpool.Pool, cfg Options, api API, clk clock.Clock) *LinkService`.
`Options` contém ClientID, GuildID, RoleID, RedirectURL e InviteURL; nenhum segredo.
`crypto.Secret` (`internal/crypto/secret.go`) já se redige sozinho em log.

### Tarefa 1: persistência, isolamento e sessão

**Arquivos:** criar migration `internal/storage/migrations/00015_discord_links.sql` (a 00012
é dos lembretes, e a 00013 e a 00014 dos CHECKs do rollup; ver "Ordem de integração"), `internal/storage/queries/discord_links.sql`,
`internal/discordlink/{types,store,store_test}.go`; alterar `internal/storage/scope.go` e gerar
`internal/storage/db/*` com `make generate`.

**Produz:** tentativas com hashes, vínculo único e intenção durável de cargo por identidade
(depende de D2: operações com gerações na opção a; uma linha com `desired` por DiscordID na
opção b). Escopo mínimo `InDiscordCallbackTx`, que lê e reserva apenas `discord_oauth_attempts`
pela prova dupla (os dois hashes); sem ampliar prober/verify. Todo o resto do callback usa
`storage.InUserTx` (`scope.go:34`). O escopo do worker (`InDiscordWorkerTx`) depende de D2: na
opção b o advisory lock e a linha por DiscordID bastam e o escopo pode ser menor.

- [ ] Escrever testes PostgreSQL de FORCE RLS, conexão reutilizada, tentativa usada/vencida e UNIQUE de DiscordID entre dois donos. Cada teste usa a fixture Docker existente e verifica dados reais após commit/rollback.
- [ ] Criar o DDL. Regras fixas:
  - `ON DELETE` escolhido de propósito: as tentativas usam `CASCADE` (a tentativa não vale sem o dono); a identidade vinculada e as operações/intenção de cargo usam `RESTRICT`, para nunca apagar por cascata um cargo ainda não retirado;
  - índice em toda coluna de FK (o PG não cria sozinho);
  - snowflake como `text` com `CHECK (discord_id ~ '^[1-9][0-9]{16,19}$')`;
  - nome de exibição com `CHECK (char_length(display_name) <= 100 AND display_name !~ '[[:cntrl:]]')`;
  - `state_hash bytea UNIQUE CHECK(octet_length(state_hash)=32)` e `browser_hash bytea CHECK(octet_length(browser_hash)=32)`, FK de dono, FamilyID lógico e datas;
  - chave única de DiscordID em registro de identidade estável; a reserva durante a remoção e as gerações dependem de D2.
- [ ] Auditoria do vínculo e do desvínculo reusa `InsertAuditEntry` (`internal/storage/queries/users.sql:40`), na mesma transação do efeito local; não criar query de auditoria nova.
- [ ] Escrever a reserva atômica da tentativa, em três tempos, sem `RETURNING *`:
  1. ler o dono sem trava, pela prova dupla, com `DiscordAttemptOwnerByHashes`, no padrão de `SessionUserByHash` (`sessions.sql:7`), que existe para saber qual trava pegar;
  2. `LockUserSessions(owner)` (`sessions.sql:14`), antes de qualquer trava de linha;
  3. o `UPDATE` com a condição completa, e só depois `FamilyIsLive` (`sessions.sql:35`):

```sql
UPDATE discord_oauth_attempts
SET consumed_at = sqlc.arg(now)
WHERE state_hash = sqlc.arg(state_hash)
  AND browser_hash = sqlc.arg(browser_hash)
  AND user_id = sqlc.arg(owner)
  AND consumed_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING id, user_id, family_id, expires_at;
```

- [ ] Criar o vínculo/intenção e a auditoria na mesma transação (`InUserTx`); UNIQUE em identidade (e, na opção a de D2, em geração) impede trabalho duplicado.
- [ ] Executar testes focados; sabotar separadamente o filtro de dono, o consumo único e UNIQUE do DiscordID. Esperar falha por isolamento/replay/duplicação, restaurar e executar GREEN.
- [ ] Confirmar que tentativa errada não lê identidade e que rollback não deixa auditoria/trabalho órfão. Rodar `sh deploy/ci/checar-nome.sh`. Commit `feat(discord): persistir vínculos e tentativas isoladas`.

### Tarefa 2: cliente Discord e configuração

**Arquivos:** criar `internal/discordapi/{types,client,client_test}.go`; alterar
`internal/config/config.go` (campos, `Load` e `Config.LogValue`, `config.go:443-460`) e seus
testes, `.env.example`, `compose.yaml`, `deploy/vps/compose.prod.yaml` (bloco `environment`,
logo depois de `EMAIL_FROM`, linha 92) e `deploy/vps/env.exemplo` (depois de `EMAIL_FROM`,
linha 25), pelo coordenador.

**Consome:** `crypto.Secret`; **produz:** `NewClient(clientID string, clientSecret, botToken crypto.Secret) *HTTPClient`.
Produção usa exclusivamente origem HTTPS fixa do Discord. Transporte/URL de teste são
injetados apenas por constructor privado no mesmo pacote dos testes. O provedor falso do
E2E depende de D3 e não entra neste pacote.

- [ ] Escrever `httptest.Server` que exige método, caminho, autenticação e corpo esperados para token, `/users/@me`, membro e PUT/DELETE de cargo. Servidor falha se receber bearer do usuário na operação de bot ou segredo em query.
- [ ] Implementar troca form-urlencoded no servidor, decodificação limitada e rejeição de ID inválido. Descartar access/refresh depois de identificar. Com D1a, enviar `code_verifier` e conferir no servidor de teste (depende de D1). `AddRole` e `RemoveRole` alteram um único cargo (PUT/DELETE de `/guilds/{guild}/members/{user}/roles/{role}`), nunca substituem o array de cargos do membro; não há `SetRole(bool)`.
- [ ] Desabilitar redirects no cliente e limitar corpo de resposta a 64 KiB e chamada a cinco segundos. `Failure.Error` contém só categoria segura; não incluir corpo, token, code, URL de webhook ou header Authorization.
- [ ] Exercitar 401, 403, 404 de membro, 429, 5xx, JSON inválido, corpo excessivo e timeout depois de registrar efeito. Caso 429 deve produzir `Failure{Kind: FailureRateLimited, RetryAfter: ...}` a partir da indicação válida do provedor.
- [ ] Configuração, em `internal/config/config.go`:
  - os seis campos da spec (`DISCORD_OAUTH_CLIENT_ID`, `DISCORD_OAUTH_CLIENT_SECRET`, `DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `DISCORD_VERIFIED_ROLE_ID`, `DISCORD_INVITE_URL`), mais `DISCORD_ALLOW_INSECURE_LOOPBACK`; client secret e token do bot como `crypto.Secret`;
  - todos ausentes: função desligada; parcial: erro de inicialização sem valores;
  - `Config.LogValue` redige o client secret e o token do bot como `[REDACTED]`, com teste que registra a `Config` e procura um canário nos dois campos;
  - em `deploy/vps/compose.prod.yaml` e `deploy/vps/env.exemplo`, as seis variáveis entram **vazias** (`${DISCORD_BOT_TOKEN:-}`), com a função desligada por padrão, no padrão do `RESEND_API_KEY`; `.env.example` e `compose.yaml` na raiz idem;
  - os segredos vão só ao papel API, nunca ao frontend ou seed.
- [ ] Validar o convite com `ParseInviteURL(raw string) (InviteURL, error)`: devolve o tipo já válido ou erro, nunca `bool` que joga o resultado fora (parse, don't validate), no padrão de `notify.ValidateDiscordURL` (`internal/notify/discord.go:38`). Aceita somente HTTPS, host exato `discord.gg` ou `discord.com` (este exige o caminho `/invite/<código>`), sem credencial, porta, query nem fragmento. Tabela de validação, incluindo os negativos abaixo, e provar RED removendo cada validação, depois GREEN:

```go
for _, raw := range []string{
    "http://discord.gg/convite",              // sem TLS
    "https://discord.gg.evil.test/convite",   // host parecido
    "https://user:secret@discord.gg/convite", // credencial
    "https://discord.gg/convite?token=x",     // query
    "https://discord.gg:8443/convite",        // porta
    "https://discord.gg/convite#frag",        // fragmento
    "https://discord.com/convite",            // discord.com sem /invite/
    "https://dıscord.gg/convite",             // IDN com letra parecida
    "https://xn--dscord-n3a.gg/convite",      // punycode
} {
    if _, err := ParseInviteURL(raw); err == nil { t.Fatalf("convite indevido aceito: %s", raw) }
}
```

Maiúsculas no host (`HTTPS://DISCORD.GG/convite`) são aceitas e normalizadas para minúsculas;
o teste prova a forma normalizada devolvida. Commit `feat(discord): integrar cliente OAuth e cargos`.

### Tarefa 3: autorização, retorno e handlers

**Arquivos:** criar `internal/discordlink/{service,service_test}.go`,
`internal/httpapi/{discordhandlers,discordhandlers_test}.go`; alterar `routes.go`,
`cmd/stillon/main.go` e o `Deps` de `httpapi`.

**Consome:** persistência e cliente; **produz:** os métodos de `*LinkService` e os handlers
`Get`, `Authorize`, `Callback`, `Check`, `Unlink` em `DiscordHandlers`, que recebe `*discordlink.LinkService`.
O DTO de resposta com as tags JSON é do handler (`statusDTO`).

- [ ] Escrever testes com nonce/state errados, ausentes, vencidos e repetidos, incluindo dois callbacks concorrentes. Cliente controlado conta trocas: exatamente uma quando ambos competem pela mesma tentativa.
- [ ] `Begin` gera duas leituras independentes de crypto/rand, persiste SHA256 em transação com sessão viva e devolve URL construída por `net/url`, não concatenação de input. Segunda tentativa invalida a anterior da família. Com D1a, gera também o `code_verifier` e põe o `code_challenge` S256 na URL (depende de D1).
- [ ] No callback, reservar a tentativa como na Tarefa 1 (`InDiscordCallbackTx`), fechar a transação, chamar `IdentityFromCode` e abrir a segunda transação com `storage.InUserTx` (`scope.go:34`), já com `LockUserSessions` e `FamilyIsLive`, antes de vínculo/auditoria/intenção. Não manter conexão transacional durante HTTP.
- [ ] `ErrIdentityInUse` vira `LastResult=failed`: teste em que o usuário B tenta vincular o DiscordID já vinculado ao usuário A e recebe `failed`, sem nenhum dado de A na resposta, e o vínculo de A segue intacto (conferido relendo o banco).
- [ ] Provar revogação concorrente com barreira no cliente controlado: iniciar callback, bloquear troca, revogar família no banco, liberar resposta; esperar `ErrSessionGone`, nenhum vínculo ou grant. Refresh legítimo da mesma família deve passar.
- [ ] Implementar cookie e retorno sem query. O `Secure` vem de `secureCookie` (`routes.go:71`, derivado de `PUBLIC_BASE_URL`); o gosec G124 exige o literal `true`, então a chamada leva `#nosec G124 -- <justificativa>`, como em `authhandlers.go:317`. Trecho contratual do handler:

```go
// #nosec G124 -- falso positivo: Secure é derivado de PUBLIC_BASE_URL (secureCookie, routes.go) para permitir desenvolvimento local sem TLS; em produção a URL é https e o cookie sai Secure, com HttpOnly e SameSite=Lax fixos.
http.SetCookie(w, &http.Cookie{
    Name: "stillon_discord_oauth", Value: started.BrowserNonce,
    Path: "/api/v1/oauth/discord/callback", HttpOnly: true,
    Secure: secureCookie, SameSite: http.SameSiteLaxMode,
    MaxAge: 300, Expires: started.ExpiresAt,
})
// Depois de callback validado: limpar o mesmo cookie e retornar 303 para /settings.
```

- [ ] Registrar GET/POST/DELETE nas rotas da spec, em `routes.go`, dentro da cadeia `/api/` (`routes.go:201-215`). O callback cai nessa cadeia: o `RequireCustomHeader` deixa o GET passar (`csrf.go:22`), os cabeçalhos `Cache-Control: no-store` e `Referrer-Policy: no-referrer` já vêm do `SecurityHeaders` (`secheaders.go`) e o balde geral é de 120/30 por IP. Falta o que é próprio do callback: um limitador por IP dedicado, no padrão da `verificacao` (`routes.go:304`, `middleware.RateLimit(ctx, 20, 10, proxiesDe(cfg))`), aplicado só a esse handler. Callback malformado não ecoa dados externos.
- [ ] Rate limit do início (`POST …/authorize`): `middleware.RateLimitUserEvery(ctx, 10*time.Second, 3)`, 1 a cada 10 s com burst 3, declarado com constantes nomeadas ao lado de `envioACada`/`envioBurst` (`routes.go:35`) e ligado como o `porEnvio` (`routes.go:171`). Check e DELETE usam `protegida` com família viva na transação.
- [ ] Log do callback: já foi conferido que nenhum código fora de teste loga `r.URL`, `RawQuery` ou `RequestURI` e que o Caddy não tem `log`. Não há logger a alterar; o passo é um teste com canário: semear `CANARY-<uuid>` em `code` e em `state`, capturar o log da requisição (handler `slog` em buffer) e afirmar que o canário não aparece no log, na resposta, nos cabeçalhos nem no `Location`.
- [ ] Testes de segurança, cada um falhando sem a mitigação:
  - 401 sem credencial nas 4 rotas autenticadas novas (GET `/api/v1/account/discord`, POST `…/authorize`, POST `…/check`, DELETE `…/discord`), acrescentadas à lista de `TestPlano5RoutesRequireAuthentication` (`plano5_routes_test.go:23`); o callback é a exceção explícita, protegida por state, cookie e família viva;
  - corpo com campo extra (`user_id`, `discord_id`, `role`) → 400 e banco inalterado;
  - o conjunto de chaves do JSON do GET é exatamente o esperado (`statusDTO`);
  - `redirect_uri` enviado ao provedor é o fixo derivado de `PUBLIC_BASE_URL`, mesmo com o cabeçalho `Host` adulterado;
  - erro no callback (provedor recusa, code inválido) devolve resposta sem as palavras `discord` nem `token` e sem o corpo do provedor.
- [ ] Testar status HTTP/JSON, integração desligada, CSRF, ausência de sessão e redirecionamento fixo. Sabotar vínculo da família, comparação de cookie e limpeza de query; registrar RED/GREEN. Rodar `sh deploy/ci/checar-nome.sh`. Commit `feat(discord): vincular identidade à sessão autenticada`.

### Tarefa 4: concessão, reconciliação e desvinculação

**Depende de D2.** O texto abaixo é a opção a (como aprovado). Na opção b, a Tarefa 4 troca
claim com lease, gerações, compensação e orçamento por: um advisory lock por rodada, leitura das
linhas por DiscordID com `desired` diferente do estado conferido, PUT ou DELETE idempotente do
cargo e releitura do membro para confirmar; as marcas de backoff e contador ficam na linha. Os
testes de corrida (concessão contra desvinculação) continuam, trocando "geração" por "`desired` mudou durante o efeito".

**Arquivos:** criar `internal/discordlink/{worker,worker_test}.go`; ampliar queries de
trabalho sob ownership Go; o coordenador conecta o ciclo de vida em `cmd/stillon/main.go`.

**Produz:** `NewWorker(pool *pgxpool.Pool, api API, cfg Options, clk clock.Clock) *Worker`,
`Run(context.Context) error` e `RunOnce(context.Context) error`. Run usa ticker de dez
segundos e encerra no contexto da API. Em `cmd/stillon/main.go` o worker entra **dentro do
bloco `if cfg.Role.RunsAPI()`** (linha 167) com `g.Go(func() error { return worker.Run(gctx) })`,
e só quando a integração está ligada; o `RunMaintenance` (que roda no bloco do sondador, linha
156) não é tocado.

- [ ] Escrever testes com banco real e cliente controlado para ausência de membro, triagem pendente, concessão, remoção, timeout ambíguo e geração alterada com grant em voo.
- [ ] Claim usa lease e serialização por identidade. Antes do efeito, ler a geração desejada; depois, confirmar somente se ainda atual. Caso obsoleto agenda reconciliação do estado ATUAL, sem remoção cega por geração anterior.
- [ ] Consultar membro antes de grant; pending impede concessão. Após efeito incerto, manter estado pendente e reconferir. Nunca modificar outros cargos ou ingressar/expulsar usuário.
- [ ] Implementar scheduler puro `nextAttempt(now time.Time, attempt int, providerWait time.Duration) time.Time`; atraso exponencial com teto local de quinze minutos, aumentado pelo prazo externo quando maior:

```go
func TestNextAttemptPreservesProviderWait(t *testing.T) {
    now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
    got := nextAttempt(now, 1, 20*time.Minute)
    if got.Before(now.Add(20*time.Minute)) { t.Fatal("Retry-After antecipado") }
}
```

- [ ] Ao atingir dez falhas ou configuração inválida, pausar. Check preserva prazo/orçamento ativo; retomada elegível somente após quinze minutos e Retry-After. Testar dez cliques concorrentes sem dez jobs nem redução do prazo. O 401/403 e o cargo inválido pausam de imediato.
- [ ] Unlink sob sessão viva avança geração, cancela tentativas e reserva identidade até remoção/quiescência. Testar que compensação antiga não remove cargo de vínculo novo e que morte entre efeito/confirmação é recuperável.
- [ ] Limpeza (tombstone com mais de 24 h, tentativas consumidas ou expiradas há mais de 24 h) roda **no próprio worker do Discord**, no papel API, a cada rodada ou em intervalo mais longo próprio; não no `storage.RunMaintenance`, que pertence ao papel sondador. Respeita operações antigas e pausadas. Teste: com o papel API sozinho, a limpeza roda e o `RunMaintenance` não é necessário.
- [ ] Sabotar geração pós-efeito, reserva, respeito ao prazo, a limpeza e o filtro de papel API. Obter RED/GREEN, rodar `sh deploy/ci/checar-nome.sh` e commit `feat(discord): reconciliar cargo e desvinculação com segurança`.

### Tarefa 5: interface e prova de navegador

**Arquivos:** criar `web/src/features/configuracoes/{SecaoDiscord,SecaoDiscord.test}.tsx`; estender
`web/src/lib/api/schemas.ts`, `consultas.ts` e `cliente.ts` (não criar `discord.ts`: o padrão
atual concentra schemas, consultas e cliente nesses três arquivos); alterar `PaginaConfiguracoes.tsx`
e as fixtures existentes (`configuracoes.test.tsx`). O E2E é `web/e2e/discord.spec.ts` e **depende de D3**
(a); na opção c ele se reduz a testes de integração do backend e de componente, e a URL limpa é provada só por teste HTTP. Em nenhum caso usa credencial real.

**Consome:** DTO Status e rotas; **produz:** `SecaoDiscord`, uma **seção própria** em Configurações
(`<section aria-labelledby="t-discord">`, ao lado e abaixo da seção Conta, que começa em
`PaginaConfiguracoes.tsx:842`), sem alterar a página de webhooks. Consulta tem chave
`['account','discord']`, limpa com logout.

- [ ] Escrever testes de renderização dos oito estados e dos resultados cancelado/expirado. Nome é texto; CTA de alertas vai a Canais. Só estado `confirmed` mostra cargo confirmado.
- [ ] Adicionar schema Zod estrito para estados/resultados e datas em `schemas.ts`; `useDiscordAccount` (em `consultas.ts`) faz GET dedicado. Não reutilizar cache infinito de `/me`, query de retorno ou localStorage como prova.
- [ ] Implementar início por POST com trava síncrona de clique e URL de autorização validada antes de navegar na mesma aba. Check e unlink obedecem estado e retorno 202; GET não provoca mutação.
- [ ] Executar prova comportamental de duplo clique, com teste equivalente a:

```tsx
const button = screen.getByRole('button', { name: 'Vincular Discord' })
await user.dblClick(button)
expect(startCalls).toBe(1)
expect(screen.queryByText('Cargo confirmado')).not.toBeInTheDocument()
```

`startCalls` é incrementado pelo handler HTTP da fixture; não por mock do componente.
O teste usa renderização/cliente reais da suíte existente e resposta inicial bloqueada
até as duas interações terminarem, evitando uma prova que só conte um clique.

- [ ] E2E (depende de D3): login real local → iniciar → consentimento falso controlado → callback backend
→ /settings limpo → pendente → cargo confirmado; acrescentar cancelamento, sessão
revogada, F5, logout/cache e desvinculação. Falhar se code/state aparecer em next,
storage, logs capturados ou URL final.
- [ ] Verificar teclado, foco, anúncios, CSP, claro/escuro e 360px. Sabotar confirmação
prematura, trava, isolamento do cache e query limpa; RED/GREEN. Rodar `sh deploy/ci/checar-nome.sh`. Commit
`feat(painel): apresentar vinculação de conta Discord`.

### Tarefa 6: integração, documentação e ativação

**Arquivos:** atualizar README, `.env.example`, `compose.yaml`, `deploy/vps/compose.prod.yaml`,
`deploy/vps/env.exemplo` e o runbook local; registrar relatório sem segredos em
`docs/superpowers/reports/2026-10-05-vinculacao-discord.md`.

- [ ] Conferir spec item a item, diff completo, queries geradas, ausência de credenciais,
licenças/dependências e comportamento desligado no ambiente atual (variáveis vazias no compose de produção).
- [ ] Executar `go build ./...`, `go vet ./...` e `make test` (PG18 por padrão) e de novo com
`TEST_POSTGRES_IMAGE` do PG17 e o digest de `ci.yml:109`; web typecheck, lint, build, Vitest e E2E (conforme D3) e
`sh deploy/ci/checar-nome.sh`. Preservar contêineres/volumes preexistentes; sem cleanup global.
Registrar hashes e comandos/resultados reais.
- [ ] Revisão independente de autorização, RLS, corridas de cargo e UI. Corrigir achados
e repetir somente gates afetados, depois gate final conforme exigências do projeto.
- [ ] Commit final assinado, verificar assinatura, push e PR em português. Aguardar CI
do HEAD exato e conferir árvore/base antes do merge autorizado na main. Confirmar antes que a
migrations dos lembretes (00012) e dos CHECKs do rollup (00013) já estão na `main` e que o sqlc foi regerado (ver "Ordem de integração").
- [ ] Preparar aplicativo/bot, cargo e URLs de retorno para revisão. Antes de gerar
segredos, preparar entrada Bitwarden. Confirmar no navegador o acesso Gerenciar cargos,
sem Administrador; credenciais por ambiente local, nunca chat. Etapas que exigem ação
pessoal do dono são entregues no ponto concreto, sem simular resultado.
- [ ] Com configuração privada pronta, validar consentimento e cargo com conta do
operador, depois unlink e remoção; capturar apenas telas sem segredos. Não fazer
deploy VPS (a hospedagem segue o runbook guiado, um passo por vez). Se faltar credencial, entregar
código/gates/PR e marcar ativação real pendente.

## Revisão do plano

Cobertos: escopo de identificação, cookie/state/sessão, RLS e unicidade, trabalho
durável, configuração parcial, retorno limpo, retry, desvinculação, retenção, interface,
gates e ativação real. Contratos de tipos estão definidos acima; não há etapa de login
Discord, cadastro público, leitura de mensagens ou bloqueio de canais.

Revisado em 2026-10-14 contra a `main` (8ca962d): caminhos e linhas conferidos no código
atual, migration renumerada para 00014 (a 00013 ficou com o PR #90), tabela STRIDE com testes, decisões D1–D3 separadas
para o dono. O plano aguarda a escolha de D1, D2 e D3 antes de qualquer execução.
