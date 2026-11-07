# Plano de implementação — vinculação Discord

> **Para agentes:** usar `superpowers:subagent-driven-development` para executar este plano por tarefa. Etapas usam caixas de seleção para registrar conclusão comprovada.

**Estado:** plano revisado em 2026-11-07 contra a `main` (`42715ac`), com as decisões D1, D2 e D3 tomadas pelo dono, e de novo contra `f16edc2`, com os achados de uma revisão independente aplicados (RLS das tentativas, teste de arquitetura do cargo, contratos em passo 0, entre outros). Pronto para executar; nada foi implementado. Antes de começar, ver "O que precisa existir antes".

**Objetivo:** vincular uma conta Discord ao dono autenticado do StillOn e gerenciar o cargo Verificado como identificação, sem restringir canais.

**Arquitetura:** OAuth com PKCE no backend; o verifier fica no cookie de ponte e a tentativa guarda só hashes, vinculada à família original. Uma linha por DiscordID em `discord_links` é o vínculo e o estado desejado; um worker no papel API reconcilia somente o cargo configurado. Configurações consulta estado próprio, separado dos webhooks.

**Stack:** Go e net/http, pgx/sqlc/PostgreSQL, React/TypeScript/TanStack Query/Zod e as ferramentas de testes já presentes. Sem SDK Discord nem dependência nova prevista.

**Spec:** [especificação emendada](../specs/2026-10-05-vinculacao-discord-design.md).

## Decisões do dono

| Decisão | Escolha | Consequência no plano |
|---|---|---|
| D1 | PKCE S256, com o verifier no cookie de ponte | O verifier substitui o nonce de navegador: um valor só, que amarra o navegador e prova a troca. `browser_hash` é o SHA-256 dele; o banco nunca o guarda |
| D2 | Reconciliação por estado desejado, em linha por DiscordID, sem leases nem compensação | Duas tabelas, sem tabela de operações, geração ou tombstone. A linha nasce `present`, vira `absent` e é apagada. Rodadas serializadas por advisory lock de sessão |
| D3 | Build tag `e2e` para provar o OAuth de ida e volta | A origem do Discord é constante no binário comum; só o binário `-tags e2e` aceita loopback. O spec roda num passo próprio, porque o Playwright do CI usa a imagem de produção |

Dois detalhes foram fechados nesta revisão, dentro das decisões:

- Em D1, um único valor no cookie, em vez de nonce e verifier separados. Dois valores só somariam parsing; os dois ficariam no mesmo cookie, com a mesma exposição.
- Em D2, o início é recusado com qualquer linha existente. Assim `desired` nunca volta de `absent` para `present`, e religar não abre um segundo caminho no worker. Custo: quem desvincula espera a remoção terminar para religar.

## O que precisa existir antes

Do código, nada: a implementação e os gates usam só cliente controlado e provedor falso, sem credencial real. A aceitação real da Tarefa 6 depende de:

| O quê | Quem | Onde fica |
|---|---|---|
| Aplicativo no portal do Discord com dois redirects exatos: o de produção, `https://stillon.tech/api/v1/oauth/discord/callback`, e um de loopback, `http://127.0.0.1:<porta fixa>/callback`, que só serve à prova do PKCE da Tarefa 6 (o Discord compara o redirect por igualdade; confirmar ao cadastrar que o portal aceita `http` em loopback). O de loopback sai do portal depois da prova | dono, logado no Discord | portal |
| Servidor, cargo Verificado e URL de convite | dono | Discord |
| Bot convidado só com Gerenciar cargos, cargo do bot acima de Verificado | dono | Discord |
| Client secret e token do bot | dono cria a entrada antes de gerar | Bitwarden, pasta "StillOn", e variáveis privadas do recurso no Coolify. O script da prova do PKCE lê o client secret de uma variável de ambiente local do terminal do dono, preenchida a partir do Bitwarden; o segredo nunca passa pelo chat, por argumento de linha de comando nem por arquivo |
| Conta Discord de teste que aceite o consentimento | dono | Discord |

## Ordem de integração

1. Migration `internal/storage/migrations/00023_discord_links.sql`, se nada entrar antes. A `main` vai até a `00022`, e a `00015` fica vaga para sempre.
2. O número tem de ser maior que todos os da `main` no momento do merge: `storage.Migrate` recusa número menor que o aplicado, e a imagem nova não subiria ([spec de heartbeats](../specs/2026-11-05-heartbeats-design.md)). Antes do merge, conferir `ls internal/storage/migrations | tail -1` contra a `origin/main`; ao renumerar ou rebasear, rodar `make generate` (sqlc) e nunca mesclar à mão arquivo gerado.
3. A migration só cria tabelas e políticas: o binário anterior convive com ela, o que mantém o rollback por imagem.

Merge é deploy. O PR de código segue os congelamentos registrados em `docs/deploy/` e confere o run do `Coolify release` do SHA antes de dizer que algo está no ar.

A branch `feat/painel-vintage` não é mais o redesign nem colide com a Tarefa 5. Ela entrou na `main` pelo #54 e não tem commit fora dela; o visual dela foi substituído pela direção C ("Editorial claro", PRs 1 a 8, último #141). A Tarefa 5 parte do painel atual, e nenhuma rebase sobre essa branch é necessária.

## Restrições globais

- Cargo apenas identifica contas vinculadas: não bloqueia canais e não concede administração.
- Escopo OAuth mínimo: `identify`; tokens OAuth não são persistidos.
- State e verifier independentes, 32 bytes cada; validade de cinco minutos; consumo único. `code_challenge_method=S256` em toda autorização, sem fluxo alternativo.
- Refresh existente permanece HttpOnly/SameSite=Strict; cookie ponte usa Lax e caminho do callback.
- HTTP apenas com `DISCORD_ALLOW_INSECURE_LOOPBACK=true` e origem local validada; padrão false. Essa variável é só de desenvolvimento local: não vai para `deploy/coolify/app.json`.
- Worker consulta a cada dez segundos; dez tentativas; backoff máximo de quinze minutos, respeitando Retry-After maior, com teto de 24 horas; contador e prazo vivem na linha.
- Produção é `deploy/coolify/app.json`. `deploy/vps/compose.prod.yaml` e `deploy/vps/env.exemplo` não mudam: sem as variáveis, a função fica desligada no job `vps` do CI.
- Tudo em português do Brasil; commits assinados no formato `tipo(escopo): descrição em português`; main somente por PR; só mudança de documentação leva `[skip ci]` no título do commit e do PR.
- Em todo passo que termina em commit: rodar `sh deploy/ci/checar-nome.sh` antes de commitar.
- Teste novo requer sabotagem compilável detectada, restauração e GREEN; não usar segredos reais em gates.
- Gates finais: `go build ./...`, `go vet ./...`, `go vet -tags e2e ./internal/discordapi/...`, `make test` e web. O `make test` usa o PG18 por padrão (o de produção); para o PG17 rodar com `TEST_POSTGRES_IMAGE=postgres:17-alpine@sha256:<digest>`, usando o digest da matriz em `.github/workflows/ci.yml` (linha 108). Fechar com o CI do HEAD final.
- Não interferir em volumes/contêineres preexistentes.

## Modelo de ameaças (STRIDE)

Cada ameaça tem uma mitigação e um teste que falha sem ela. Os nomes dos testes são propostos e se confirmam na implementação. O que não se testa daqui está marcado como conferência manual.

| Ameaça | Caso | Mitigação | Teste |
|---|---|---|---|
| Spoofing | callback vindo de outro navegador, ou forçado na vítima com code e state do atacante | SHA-256 do cookie comparado ao `browser_hash` da tentativa; a família viva é conferida | `TestCallbackOutroNavegadorRejeitado` |
| Spoofing | code interceptado no retorno ou em log de proxy | PKCE: sem o verifier, o provedor recusa a troca; o cliente sempre envia o da tentativa | `TestTrocaEnviaOVerifierDaTentativa`; recusa real na Tarefa 6 |
| Spoofing | provedor falso alcançável em produção (D3) | origem fixa em arquivo `//go:build !e2e`; a tag não aparece no `Dockerfile`; o binário `e2e` só aceita loopback | `TestOrigemDoBinarioComumEFixa`; `deploy/ci/checar-imagem.sh` (job `image`) falha se o binário da imagem contém `STILLON_E2E_DISCORD_ORIGEM` |
| Tampering | `state` adulterado | `state` só vale pelo hash gravado; adulterado não reserva tentativa | `TestCallbackStateAdulterado` |
| Tampering | desvínculo durante um PUT em voo | gravação condicional ao `desired` lido; a rodada seguinte faz o DELETE | `TestDesvinculoDuranteConcessaoTerminaSemCargo` |
| Tampering | resposta hostil do provedor: Retry-After absurdo, nome com controle ou longo demais | teto de 24 h; o nome é normalizado antes do `CHECK` e não vira 500 | `TestRetryAfterAbsurdoFicaNoTeto`, `TestNomeDeExibicaoHostilENormalizado` |
| Tampering | nome de exibição vindo do Discord (controlado por terceiro) com HTML ou script, renderizado no painel | `CHECK` e normalização barram controles; o painel o renderiza como texto, nunca como HTML | `TestNomeDeExibicaoRenderizaComoTexto` (Tarefa 5: nome `<img src=x onerror=...>` aparece literal, sem elemento criado) |
| Repudiation | vínculo, desvínculo e efeito no cargo sem rastro | auditoria na mesma transação (`InsertAuditEntry`), inclusive a do worker, com IP nulo | `TestVinculoGravaAuditoriaNaMesmaTransacao`, `TestCargoConfirmadoGravaAuditoria` |
| Info disclosure | `code`, `state` ou verifier em log, resposta ou `next`; conflito que revela outra conta | retorno 303 fixo sem query; logs sem query nem cookie; `ErrIdentityInUse` vira `failed` sem dado alheio | `TestCallbackNaoVazaCanarioEmLogNemResposta` (canário também no cookie), `TestIdentidadeEmUsoNaoRevelaOutraConta` |
| Info disclosure | client secret ou token do bot em log, erro de boot ou banco | `Config.LogValue` redige; o erro de configuração parcial só nomeia variáveis; o banco guarda só hashes | `TestConfigLogValueRedigeSegredosDiscord`, `TestConfigParcialSemValoresNoErro` |
| Info disclosure | vínculo lido por outro usuário; dado pessoal retido depois de sair | FORCE RLS no dono; rotas agem só sobre o principal, sem id no caminho; a remoção confirmada apaga a linha | `TestRLSVinculoAlheioInvisivel`, `TestRemocaoConfirmadaApagaALinha` |
| DoS | tentativas em excesso; check usado para gastar o bot; 429 do Discord no worker | balde por usuário dividido entre início e check, limitador por IP no callback, backoff com teto e Retry-After, 429 encerra a rodada | `TestInicioECheckDividemOBalde`, `TestCallbackRateLimitPorIP`, `TestNextAttemptPreservesProviderWait` |
| DoS | configuração parcial impede o boot depois de um release | variáveis com padrão vazio em `app.json`; as seis definidas juntas, depois de publicar desligado | `TestCoolifyAppDiscordVaziaPorPadrao`, `TestConfigParcialSemValoresNoErro` |
| Elevation | cargo que autoriza algo no produto; bot com Administrador; vazamento do token do bot | o produto nunca lê o cargo para autorizar; bot só com Gerenciar cargos, um cargo acima de Verificado, rotação pelo portal | `TestSoODiscordlinkLeOEstadoDoCargo` (teste AST da Tarefa 4); conferência manual na ativação |
| Elevation | escopo do worker usado para ler ou criar dados | política própria: lê, atualiza e apaga `discord_links`; lê e apaga tentativas (o `DELETE ... WHERE` exige SELECT, ver Tarefa 1); sem INSERT em nenhuma; nada em outras tabelas | `TestEscopoDoWorkerSoAlcancaDiscordLinks` |

O limite conhecido da borda, descrito na tabela de segurança do README, vale também para este fluxo. O que reduz o efeito aqui é o escopo `identify`, o prazo de cinco minutos e o consumo único.

## Foco da revisão

1. Logout durante a troca OAuth não pode concluir vínculo: teste de barreira na tarefa 3.
2. Efeito em voo não pode sobreviver a um desvínculo: teste concorrente na tarefa 4.
3. Duplo clique ou callback repetido não pode trocar dois codes: testes nas tarefas 3 e 5.
4. Resposta externa ambígua não pode virar confirmação falsa: testes 429/timeout nas tarefas 2 e 4.
5. Retorno sem sessão não pode carregar code/state no `next`: prova HTTP e navegador na tarefa 5.
6. O binário de produção não pode conter a origem do E2E: teste e gate da tarefa 2.

## Equipe, isolamento e ordem

A execução usa os agentes do projeto, no máximo 3–4 por sessão: `complexo` para o que é delicado
(persistência e RLS, reserva da tentativa, worker e corridas, revisão de segurança) e
`simples` para o que é mecânico (configuração, documentação, componentes e testes de
renderização). O coordenador (a sessão principal) possui configuração, handlers, composição
(`cmd/stillon/main.go`) e documentação; leva ao dono só as decisões importantes. Cada agente
trabalha em worktree exclusivo da fase; nenhum agente reverte trabalho alheio. Arquivos
gerados por sqlc não se editam à mão: `make generate`.

O passo 0 precede tudo: o coordenador, dono único dos dois `types.go`, escreve os contratos
abaixo e os comita antes de abrir a Tarefa 1 ou a 2 (commit `feat(discord): declarar contratos
compartilhados`; só declarações, compila sem teste próprio). Tarefa 1 precede 3/4; tarefa 2
pode avançar em paralelo à 1, porque as duas só consomem os contratos. Mudança de interface
exige aviso ao coordenador e atualização dos consumidores. Cada tarefa termina em commit
assinado e revisão; um PR funcional reúne o fluxo completo, sem publicar uma função parcialmente
ativada.

## Contratos compartilhados

**Passo 0, antes da Tarefa 1 e da 2; dono único: o coordenador.** Criar
`internal/discordlink/types.go` e `internal/discordapi/types.go` com o que segue; nenhuma
tarefa cria nem edita esses dois arquivos. A interface do
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
    FailureNotFound FailureKind = "not_found" // código 10007 (membro desconhecido)
    FailureConfig FailureKind = "config"      // códigos 10004 (guild) e 10011 (cargo)
    FailureServer FailureKind = "server"
    FailureTimeout FailureKind = "timeout"
    FailureInvalidResponse FailureKind = "invalid_response"
)

type Failure struct { Kind FailureKind; RetryAfter time.Duration }
func (e *Failure) Error() string { return "discord: " + string(e.Kind) }
```

Um 404 do Discord não diz sozinho o que faltou: o cliente lê o código do corpo JSON (decodificação
limitada, como nas outras respostas) e distingue. `10007` (membro desconhecido) é o caso normal de
quem ainda não entrou: `Member` devolve `Member{Exists: false}` sem erro, e `AddRole`/`RemoveRole`
devolvem `FailureNotFound`, que o worker trata como membro ausente. `10004` (guild) e `10011` (cargo)
são `FailureConfig`, que pausa de imediato, como 401/403. Qualquer outro 404, ou 404 sem código
legível, é `FailureInvalidResponse`. A distinção vale nos três endpoints (membro, PUT e DELETE do
cargo) e tem teste em cada um.

```go
// package discordlink
// API é o que o serviço e o worker usam do Discord; discordapi.HTTPClient a satisfaz.
type API interface {
    IdentityFromCode(ctx context.Context, code, verifier, redirectURL string) (discordapi.Identity, error)
    Member(ctx context.Context, guildID, userID string) (discordapi.Member, error)
    AddRole(ctx context.Context, guildID, userID, roleID string) error
    RemoveRole(ctx context.Context, guildID, userID, roleID string) error
}

// Meta é o auth.Meta (IP e User-Agent) que o handler já monta em
// internal/httpapi/accounthandlers.go:100, com middleware.ClientIP e r.UserAgent().
// O serviço calcula crypto.HashIP(ipKey, meta.IP), como account.go:200, e grava o
// hash na auditoria (InsertAuditEntry, com IpHash). O worker audita com IP nulo.
type Principal struct { UserID, FamilyID uuid.UUID; Meta auth.Meta }

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
type Start struct { AuthorizationURL, Verifier string; ExpiresAt time.Time }
type Callback struct { State, Verifier, Code, ProviderError string; Meta auth.Meta }

// InviteURL é a URL de convite já validada: só nasce de ParseInviteURL (Tarefa 2).
type InviteURL struct { raw string }
func (u InviteURL) String() string { return u.raw }
```

Métodos de `*LinkService`: `Get(ctx, Principal) (Status, error)`, `Begin(ctx, Principal) (Start, error)`,
`Complete(ctx, Callback) error`, `Check(ctx, Principal) (Status, error)` e `Unlink(ctx, Principal) (Status, error)`.

`Status.State` admite somente `unavailable`, `unlinked`, `awaiting_member`,
`awaiting_screening`, `role_pending`, `confirmed`, `removal_pending`, `paused`. O estado é
calculado da linha, nesta ordem: sem linha, `unlinked`; `paused_at` preenchido, `paused`;
`desired=absent`, `removal_pending`; depois `observed`, com `unknown` como `role_pending`.
`LastResult` admite vazio, `cancelled`, `expired`, `failed`, `linked`; sem texto do
provedor. Erros de domínio exportados: `ErrSessionGone`, `ErrInvalidAttempt`,
`ErrIdentityInUse`, `ErrUnavailable`, `ErrAlreadyLinked`. `ErrIdentityInUse` não chega ao
cliente como erro próprio: vira `LastResult=failed` (ver Tarefa 3). `ErrAlreadyLinked` é o
409 do início. `Check` não falha por estar cedo: devolve o estado e o `RetryAt` atuais.
Construtores: `discordapi.NewClient(clientID string, clientSecret, botToken crypto.Secret) *HTTPClient`
e `NewService(pool *pgxpool.Pool, cfg Options, api API, clk clock.Clock, ipKey []byte) *LinkService`.
`ipKey` é o `cfg.IPHashKey`; como `auth.NewManager` (`session.go:77`), o construtor entra em panic
com chave de menos de 32 bytes, para o erro aparecer na partida e não no primeiro callback.
`Options` contém Enabled, ClientID, GuildID, RoleID, RedirectURL e `InviteURL InviteURL`; nenhum
segredo. `crypto.Secret` (`internal/crypto/secret.go`) já se redige sozinho em log.

**Função desligada.** `cmd/stillon/main.go` monta sempre o `LinkService` e registra as rotas (a
allowlist e `TestEveryRegisteredRouteDeniesByDefault` enumeram rotas pelos fontes), com
`Options.Enabled` vindo de `cfg.DiscordEnabled()`, no padrão de `cfg.EmailEnabled()`
(`config.go:114`). Desligada, `api` pode ser nil, porque nada o chama, e o `Worker` nem é criado.
`unavailable` vence qualquer linha: com `Enabled=false`, `Get` devolve `Status{Enabled: false,
State: unavailable}` antes de ler o banco, mesmo havendo vínculo gravado, e `Begin`, `Check` e
`Unlink` devolvem `ErrUnavailable` sem tocar em nada (o handler o traduz em
`apierr.CodeUnavailable`, 503, sem iniciar OAuth). As linhas ficam como estão e retomam quando a
função volta.

**Forma do JSON.** As chaves são `snake_case`, como o resto da API (`new_password`), e são
exatamente as da tabela: o DTO do handler (`statusDTO`, `startDTO`) e o schema Zod estrito do
painel (Tarefa 5) saem dela. As chaves do `statusDTO` estão sempre presentes; o que falta vem
como `null` (datas) ou `""` (texto). Datas em RFC 3339, UTC.

| DTO | Chave JSON | Tipo | Quando ausente |
|---|---|---|---|
| `statusDTO` | `enabled` | boolean | nunca |
| `statusDTO` | `state` | um dos oito estados | nunca |
| `statusDTO` | `display_name` | string | `""` sem vínculo |
| `statusDTO` | `last_result` | `""`, `cancelled`, `expired`, `failed` ou `linked` | `""` |
| `statusDTO` | `confirmed_at` | string de data | `null` |
| `statusDTO` | `retry_at` | string de data | `null` |
| `statusDTO` | `invite_url` | string | `""` com a função desligada |
| `startDTO` | `authorization_url` | string | nunca |
| `startDTO` | `expires_at` | string de data | nunca |

`Start.Verifier` não tem chave: sai só no cookie.

### Tarefa 1: persistência, isolamento e sessão

**Arquivos:** criar `internal/storage/migrations/00023_discord_links.sql` (ver "Ordem de integração"), `internal/storage/queries/discord_links.sql`,
`internal/discordlink/{store,store_test}.go` (o `types.go` é do passo 0); alterar `internal/storage/scope.go` e gerar
`internal/storage/db/*` com `make generate`. Toda query sobre `discord_links` leva o prefixo
`DiscordLink` no nome (`DiscordLinkByUser`, ...): o teste de arquitetura da Tarefa 4 depende disso.

**Produz:** tentativas com hashes e uma linha por DiscordID em `discord_links`. Dois escopos
novos, no padrão de `InPingTx` (`scope.go:70`): `InDiscordCallbackTx`, que lê e reserva apenas
`discord_oauth_attempts` pela prova dupla (os dois hashes, em GUCs próprias), e
`InDiscordWorkerTx`, com `app.scope = 'discord_worker'`. Sem ampliar prober/verify. A advisory
lock serializa rodadas, mas não substitui o escopo: sem ele a RLS esconde as linhas dos donos
do worker. Todo o resto do callback usa `storage.InUserTx` (`scope.go:34`).

- [ ] Escrever testes PostgreSQL de FORCE RLS, conexão reutilizada, tentativa usada/vencida, UNIQUE de DiscordID entre dois donos e UNIQUE de `user_id` com dois DiscordIDs. Entra também o `DELETE` do escopo do worker sobre tentativas vencidas, que confere as linhas afetadas (nunca só a ausência de erro): apaga as vencidas e preserva as vivas, e `TestEscopoDoWorkerSoAlcancaDiscordLinks` prova que ele não faz INSERT nem UPDATE de tentativa. Cada teste usa a fixture Docker existente e verifica dados reais após commit/rollback.
- [ ] Criar o DDL. Regras fixas:
  - `discord_oauth_attempts`: `id uuid` PK, `user_id uuid NOT NULL` FK com `ON DELETE CASCADE` (a tentativa não vale sem o dono), `family_id uuid NOT NULL` lógico, `state_hash bytea NOT NULL UNIQUE CHECK(octet_length(state_hash)=32)`, `browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32)`, `expires_at timestamptz NOT NULL`, `consumed_at` (nulo até o consumo), `result text CHECK (result IN ('cancelled','linked'))` (nulo até haver resultado) e `created_at timestamptz NOT NULL`. Consumida sem `result` lê-se `failed`, inclusive depois de queda; vencida sem consumo lê-se `expired`;
  - `discord_links`: `id uuid` PK (UUIDv7, é o `entity_id` da auditoria), `discord_id text NOT NULL UNIQUE`, `user_id uuid NOT NULL UNIQUE` com FK `ON DELETE RESTRICT`, `display_name text NOT NULL`, `guild_id text NOT NULL`, `role_id text NOT NULL`, `desired text NOT NULL CHECK (desired IN ('present','absent'))`, `observed text NOT NULL DEFAULT 'unknown' CHECK (observed IN ('unknown','awaiting_member','awaiting_screening','present'))`, `confirmed_at`, `next_attempt_at`, `attempts int NOT NULL DEFAULT 0 CHECK (attempts >= 0)`, `last_failure text`, `paused_at` (os quatro nulos de propósito: sem confirmação, sem trabalho agendado, sem falha e sem pausa), `created_at timestamptz NOT NULL` e `updated_at timestamptz NOT NULL`;
  - índice em toda coluna de FK (o `UNIQUE` já cobre `discord_links.user_id`; `discord_oauth_attempts.user_id` ganha o seu). Sem índice de fila: a tabela é pequena, e índice só com query medida;
  - snowflakes como `text` com `CHECK (discord_id ~ '^[1-9][0-9]{16,19}$')`, inclusive guild e cargo;
  - nome de exibição com `CHECK (char_length(display_name) <= 100 AND display_name !~ '[[:cntrl:]]')`;
  - políticas no padrão da 00016 (`00016_heartbeats.sql`: dono, escopo do sondador, escopo do ping, cada um com a sua política). Com FORCE RLS, `UPDATE` e `DELETE` com `WHERE` e o `RETURNING` leem as linhas, então exigem política de SELECT: só com `FOR DELETE`, um `DELETE ... WHERE expires_at < ...` apaga 0 linhas, em silêncio (reproduzido no PostgreSQL 18). Por isso cada escopo ganha o SELECT de que a sua escrita precisa:
    - `discord_links`: dono em `USING` e `WITH CHECK` (`FOR ALL`); worker com SELECT, UPDATE e DELETE;
    - `discord_oauth_attempts`, dono: `FOR ALL`, em `USING` e `WITH CHECK`;
    - `discord_oauth_attempts`, escopo do callback (`app.discord_state_hash` e `app.discord_browser_hash`, os dois hashes em hexadecimal, no padrão de `heartbeats_ping_select` e `_update`): política `FOR SELECT` e política `FOR UPDATE` (esta com `WITH CHECK`), as duas por `state_hash` e `browser_hash` iguais aos das GUCs, com `decode(NULLIF(current_setting(...), ''), 'hex')`. O SELECT cobre `DiscordAttemptOwnerByHashes` e o `UPDATE ... RETURNING` da reserva;
    - `discord_oauth_attempts`, worker: `FOR SELECT` e `FOR DELETE`, ambas por `app.scope = 'discord_worker'`; sem UPDATE e sem INSERT;
    - o worker não tem INSERT em tabela nenhuma.
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

- [ ] Criar o vínculo e a auditoria na mesma transação (`InUserTx`), com `INSERT … ON CONFLICT DO NOTHING` sem alvo, que cobre as duas chaves únicas. Zero linhas vira `ErrIdentityInUse`. Não usar `ON CONFLICT DO UPDATE`: sob RLS, a linha de outro dono é erro de política, e não conflito. Provar no teste de dois donos que o `DO NOTHING` se comporta assim com a linha alheia invisível.
- [ ] Executar testes focados; sabotar separadamente o filtro de dono, o consumo único, UNIQUE do DiscordID e a política `FOR SELECT` do worker sobre as tentativas (o `DELETE` passa a apagar 0 linhas). Esperar falha por isolamento/replay/duplicação/limpeza, restaurar e executar GREEN.
- [ ] Confirmar que tentativa errada não lê identidade e que rollback não deixa auditoria/vínculo órfão. Rodar `sh deploy/ci/checar-nome.sh`. Commit `feat(discord): persistir vínculos e tentativas isoladas`.

### Tarefa 2: cliente Discord e configuração

**Arquivos:** criar `internal/discordapi/{client,client_test,origem_producao,origem_e2e}.go` e
`internal/discordlink/{invite,invite_test}.go` (os dois `types.go` são do passo 0); alterar
`deploy/ci/checar-imagem.sh` e `internal/config/config.go` (campos, `Load` e `Config.LogValue`, `config.go:445-464`) e seus
testes, `internal/config/compose_test.go`, `.env.example`, `compose.yaml` e
`deploy/coolify/app.json` (bloco `environment` do serviço `app`, em ordem alfabética), pelo coordenador.

**Consome:** `crypto.Secret`; **produz:** `NewClient(clientID string, clientSecret, botToken crypto.Secret) *HTTPClient`.
Produção usa exclusivamente origem HTTPS fixa do Discord. Transporte/URL de teste são
injetados apenas por constructor privado no mesmo pacote dos testes.

- [ ] Origem (D3): `origem_producao.go` (`//go:build !e2e`) devolve a constante `https://discord.com`; `origem_e2e.go` (`//go:build e2e`) lê `STILLON_E2E_DISCORD_ORIGEM` e encerra o processo se não for `http://127.0.0.1:<porta>`. O teste comum `TestOrigemDoBinarioComumEFixa` prova a constante. O gate da imagem fica em `deploy/ci/checar-imagem.sh` (job `image`, `ci.yml:262`, passo "checagens da imagem", `ci.yml:313`), que já extrai o binário com `docker cp` no passo 4: logo depois do `grep` do painel, que prova que o arquivo foi extraído, entra `grep -aq STILLON_E2E_DISCORD_ORIGEM "$tmp/stillon" && falhar "..."`, que falha se a string estiver presente (ela só existe no binário `-tags e2e`). O `Dockerfile` continua sem a tag; sabotar compilando a imagem com `-tags e2e` e ver o gate falhar. Não há gate de `go version -m` no job `e2e`.
- [ ] Escrever `httptest.Server` que exige método, caminho, autenticação e corpo esperados para token, `/users/@me`, membro e PUT/DELETE de cargo. Servidor falha se receber bearer do usuário na operação de bot ou segredo em query.
- [ ] Implementar troca form-urlencoded em `/api/v10/oauth2/token`, com autenticação Basic, `grant_type`, `code`, `redirect_uri` e `code_verifier`; o servidor de teste confere que o S256 do verifier recebido bate com o challenge da tentativa (`TestTrocaEnviaOVerifierDaTentativa`). Decodificação limitada e rejeição de ID inválido. Descartar access/refresh depois de identificar. `AddRole` e `RemoveRole` alteram um único cargo (PUT/DELETE de `/guilds/{guild}/members/{user}/roles/{role}`), nunca substituem o array de cargos do membro; não há `SetRole(bool)`.
- [ ] Normalizar o nome de exibição antes de devolvê-lo: sem caracteres de controle, no máximo 100 caracteres, com o `username` como reserva. Nome hostil não pode virar violação de `CHECK` nem 500.
- [ ] Desabilitar redirects no cliente e limitar corpo de resposta a 64 KiB e chamada a cinco segundos. `Failure.Error` contém só categoria segura; não incluir corpo, token, code, verifier, URL de webhook ou header Authorization.
- [ ] Exercitar 401, 403, 429, 5xx, JSON inválido, corpo excessivo e timeout depois de registrar efeito, e o 404 distinguido pelo código do corpo (ver "Contratos compartilhados"), **nos três endpoints** (membro, PUT e DELETE do cargo): `10007` vira membro ausente (`Member{Exists: false}` na leitura, `FailureNotFound` no PUT/DELETE), `10004` e `10011` viram `FailureConfig`, e 404 com outro código ou sem JSON vira `FailureInvalidResponse`. Caso 429 deve produzir `Failure{Kind: FailureRateLimited, RetryAfter: ...}` a partir da indicação válida do provedor, limitada a 24 horas.
- [ ] Configuração, em `internal/config/config.go`:
  - os seis campos da spec (`DISCORD_OAUTH_CLIENT_ID`, `DISCORD_OAUTH_CLIENT_SECRET`, `DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `DISCORD_VERIFIED_ROLE_ID`, `DISCORD_INVITE_URL`), mais `DISCORD_ALLOW_INSECURE_LOOPBACK`; client secret e token do bot como `crypto.Secret`;
  - todos ausentes: função desligada; parcial: erro de inicialização sem valores, no padrão do par `RESEND_API_KEY`/`EMAIL_FROM` (`config.go:314`);
  - `DISCORD_ALLOW_INSECURE_LOOPBACK=true` exige `PUBLIC_BASE_URL` local (`hostLocal`, `config.go:380`); com https ou host remoto, o boot recusa;
  - função ligada (as seis definidas) com `PUBLIC_BASE_URL` que não seja https e **sem** a flag de loopback também recusa o boot: o cookie sairia sem `Secure` e o redirect deixaria de ser o https cadastrado no portal. Vale para http remoto, para http em localhost e para `PUBLIC_BASE_URL` vazia (sem ela o callback não tem como ser derivado). Teste de tabela `TestConfigDiscordLigadoExigeHTTPSOuLoopbackExplicito`, em `config_test.go`, com os três casos recusados e dois aceitos (https; http em localhost com a flag); sabotar removendo a checagem e ver o boot aceitar o http remoto;
  - `Config.DiscordEnabled()` (as seis definidas), no padrão de `EmailEnabled()`; `Config` guarda `DiscordInviteURL` como `string`, porque `internal/config` só importa a biblioteca padrão e não deve passar a importar `discordlink`;
  - `Config.LogValue` redige o client secret e o token do bot como `[REDACTED]`, com teste que registra a `Config` e procura um canário nos dois campos;
  - em `deploy/coolify/app.json`, as seis variáveis entram **vazias** (`${DISCORD_BOT_TOKEN:-}`), com a função desligada por padrão, no padrão do `RESEND_API_KEY`; `.env.example` e `compose.yaml` na raiz idem. `TestCoolifyAppDiscordVaziaPorPadrao`, em `compose_test.go`, exige o `:-` nas seis e a ausência da variável de loopback;
  - `deploy/vps/compose.prod.yaml` e `deploy/vps/env.exemplo` não mudam;
  - os segredos vão só ao papel API, nunca ao frontend ou seed.
- [ ] Validar o convite com `ParseInviteURL(raw string) (InviteURL, error)`: devolve o tipo já válido ou erro, nunca `bool` que joga o resultado fora (parse, don't validate), no estilo de validação de `notify.ValidateDiscordURL` (`internal/notify/discord.go:38`). O tipo `InviteURL` vive em `internal/discordlink` (declarado no passo 0, é o tipo de `Options.InviteURL`) e `ParseInviteURL` em `internal/discordlink/invite.go`, o único jeito de obter um valor dele. Quem a chama é `cmd/stillon/main.go`, uma vez, ao montar o `LinkService` com a função ligada, sobre `cfg.DiscordInviteURL`; convite inválido derruba o boot com um erro que nomeia a variável e não repete o valor. Aceita somente HTTPS, host exato `discord.gg` ou `discord.com` (este exige o caminho `/invite/<código>`), sem credencial, porta, query nem fragmento. Tabela de validação, incluindo os negativos abaixo, e provar RED removendo cada validação, depois GREEN:

```go
for _, raw := range []string{
    "http://discord.gg/convite",              // sem TLS
    "https://discord.gg.evil.test/convite",   // host parecido
    "https://user:secret@discord.gg/convite", // credencial
    "https://discord.gg/convite?token=x",     // query
    "https://discord.gg:8443/convite",        // porta
    "https://discord.gg/convite#frag",        // fragmento
    "https://discord.com/convite",            // discord.com sem /invite/
    "https://discord.gg/",                    // sem código de convite
    "https://discord.com/invite/",            // /invite/ sem código
    "https://discord.gg./convite",            // host com ponto final
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
`internal/httpapi/{discordhandlers,discordhandlers_integration_test}.go`; alterar `routes.go`,
`rotas_negar_por_padrao_integration_test.go`, `cmd/stillon/main.go` e o `Deps` de `httpapi`.

**Consome:** persistência e cliente; **produz:** os métodos de `*LinkService` e os handlers
`Get`, `Authorize`, `Callback`, `Check`, `Unlink` em `DiscordHandlers`, que recebe `*discordlink.LinkService`.
O DTO de resposta com as tags JSON é do handler (`statusDTO`).

- [ ] Escrever testes com verifier/state errados, ausentes, vencidos e repetidos, incluindo dois callbacks concorrentes. Cliente controlado conta trocas: exatamente uma quando ambos competem pela mesma tentativa.
- [ ] `Begin` recusa com `ErrAlreadyLinked` se houver linha do dono, em qualquer estado. Gera duas leituras independentes de crypto/rand, persiste os SHA-256 em transação com sessão viva e devolve URL construída por `net/url`, não concatenação de input, com `response_type=code`, `client_id`, `scope=identify`, `redirect_uri`, `state`, `code_challenge` (base64url do mesmo SHA-256 do verifier) e `code_challenge_method=S256`. Segunda tentativa invalida a anterior da família. O verifier sai só em `Start.Verifier`, para o cookie.
- [ ] No callback, reservar a tentativa como na Tarefa 1 (`InDiscordCallbackTx`), fechar a transação, chamar `IdentityFromCode` com o verifier do cookie e abrir a segunda transação com `storage.InUserTx` (`scope.go:34`), já com `LockUserSessions` e `FamilyIsLive`, antes de vínculo/auditoria. Não manter conexão transacional durante HTTP. `error` do provedor (cancelamento) com prova dupla válida consome a tentativa com `result='cancelled'`, sem troca. Os handlers preenchem `Principal.Meta` e `Callback.Meta` (`middleware.ClientIP(r, proxies)` e `r.UserAgent()`, como `accounthandlers.go:100`), e o serviço grava `crypto.HashIP(ipKey, meta.IP)` na auditoria do vínculo e do desvínculo (`account.go:200` e `:556`); o teste do vínculo confere que o `ip_hash` gravado é o do IP da requisição, e não nulo.
- [ ] `ErrIdentityInUse` vira `LastResult=failed`: teste em que o usuário B tenta vincular o DiscordID já vinculado ao usuário A e recebe `failed`, sem nenhum dado de A na resposta, e o vínculo de A segue intacto (conferido relendo o banco).
- [ ] Provar revogação concorrente com barreira no cliente controlado: iniciar callback, bloquear troca, revogar família no banco, liberar resposta; esperar `ErrSessionGone`, nenhum vínculo. Refresh legítimo da mesma família deve passar.
- [ ] Implementar cookie e retorno sem query. O `Secure` vem de `secureCookie` (`routes.go:76`, derivado de `PUBLIC_BASE_URL`); o gosec G124 exige o literal `true`, então a chamada leva `#nosec G124 -- <justificativa>`, como em `authhandlers.go:317`. Trecho contratual do handler:

```go
// #nosec G124 -- falso positivo: Secure é derivado de PUBLIC_BASE_URL (secureCookie, routes.go) para permitir desenvolvimento local sem TLS; em produção a URL é https e o cookie sai Secure, com HttpOnly e SameSite=Lax fixos.
http.SetCookie(w, &http.Cookie{
    Name: "stillon_discord_oauth", Value: started.Verifier,
    Path: "/api/v1/oauth/discord/callback", HttpOnly: true,
    Secure: secureCookie, SameSite: http.SameSiteLaxMode,
    MaxAge: 300, Expires: started.ExpiresAt,
})
// Depois de callback validado: limpar o mesmo cookie e retornar 303 para /settings.
```

- [ ] Registrar GET/POST/DELETE nas rotas da spec, em `routes.go`, no `mux` da cadeia `/api/`: o registro das rotas fica em `routes.go:162-203` (de `protegida` ao último `status-pages`), e `221-235` é a cadeia de middlewares que envolve esse `mux`. O callback cai nessa cadeia: o `RequireCustomHeader` deixa o GET passar (`csrf.go:22`), os cabeçalhos `Cache-Control: no-store` e `Referrer-Policy: no-referrer` já vêm do `SecurityHeaders` (`secheaders.go`) e o balde geral é de 120/30 por IP. Falta o que é próprio do callback: um limitador por IP dedicado, no padrão da `verificacao` (`routes.go:325`, `middleware.RateLimit(ctx, clk, 20, 10, proxiesDe(cfg))`), aplicado só a esse handler. Callback malformado não ecoa dados externos.
- [ ] Rate limit: `middleware.RateLimitUserEvery(ctx, d.Clock, 10*time.Second, 3)`, 1 a cada 10 s com burst 3, declarado com constantes nomeadas ao lado de `envioACada`/`envioBurst` (`routes.go:35`) e ligado como o `comEnvio` (`routes.go:191-192`). Uma instância só para `POST …/authorize` e `POST …/check`: cada check pode virar chamadas do bot, e o orçamento é do usuário. GET e DELETE usam `protegida` (`routes.go:162`); DELETE com família viva na transação.
- [ ] Log do callback: já foi conferido que nenhum código fora de teste loga `r.URL`, `RawQuery` ou `RequestURI` e que o Caddy do StillOn (`deploy/vps/Caddyfile`) não tem `log`. Não há logger a alterar. O proxy da frente fica fora deste repositório, e o desenho não depende dele: o code sozinho não completa a troca. O passo é um teste com canário, reusando o `logCapturado` de `canario_integration_test.go`: rodar dois cenários e afirmar, nos dois, que nada do que foi semeado aparece no log, na resposta, nos cabeçalhos nem no `Location`. (a) `CANARY-<uuid>` em `code`, em `state` e no cookie, sem tentativa correspondente: o callback é rejeitado cedo. (b) Tentativa **válida**, criada por `Begin`, com os hashes de verdade (o state e o verifier devolvidos por `Begin` seguem para o callback, e o teste afirma que esses dois valores também não vazam) e o canário no `code`, com o provedor falso devolvendo o canário no corpo do erro da troca (por exemplo `{"error":"invalid_grant","error_description":"CANARY-<uuid>"}`). Sem o (b), o canário nunca chega ao ponto em que o corpo do provedor seria tratado, e o teste passaria sem testar nada.
- [ ] Testes de segurança, cada um falhando sem a mitigação:
  - `TestEveryRegisteredRouteDeniesByDefault` (`rotas_negar_por_padrao_integration_test.go`) enumera as rotas pelos fontes e já cobre as 4 autenticadas novas; o callback entra em `rotasPublicas`, com o porquê (a credencial é a prova dupla de state e cookie). `TestEveryMutationRequiresTheAntiCSRFHeader` cobre as mutações sem mudança;
  - corpo com campo extra (`user_id`, `discord_id`, `role`) → 400 e banco inalterado, no padrão de `DisallowUnknownFields` (`authhandlers.go:420`);
  - o conjunto de chaves do JSON do GET é exatamente o esperado (`statusDTO`), e `Start.Verifier` não aparece no JSON do início;
  - `redirect_uri` enviado ao provedor é o fixo derivado de `PUBLIC_BASE_URL`, mesmo com o cabeçalho `Host` adulterado;
  - erro no callback (provedor recusa, code inválido) devolve resposta cujo **corpo** não tem as palavras `discord` nem `token` nem o corpo do provedor (o canário do cenário (b) acima é o corpo do provedor). A regra vale para o corpo: os cabeçalhos carregam o nome do cookie (`stillon_discord_oauth`) e o caminho do callback na limpeza do cookie, e o `Location` é só `/settings`.
- [ ] Testar status HTTP/JSON, integração desligada, CSRF, ausência de sessão, 409 com vínculo existente e redirecionamento fixo. Sabotar vínculo da família, comparação de cookie e limpeza de query; registrar RED/GREEN. Rodar `sh deploy/ci/checar-nome.sh`. Commit `feat(discord): vincular identidade à sessão autenticada`.

### Tarefa 4: reconciliação do cargo e desvinculação

**Arquivos:** criar `internal/discordlink/{worker,worker_test,arquitetura_test}.go` e
`internal/httpapi/discord_dono_integration_test.go`; ampliar queries em
`discord_links.sql`; o coordenador conecta o ciclo de vida em `cmd/stillon/main.go`.

**Produz:** `NewWorker(pool *pgxpool.Pool, api API, cfg Options, clk clock.Clock) *Worker`,
`Run(context.Context) error` e `RunOnce(context.Context) error`. Run usa ticker de dez
segundos e encerra no contexto da API. Em `cmd/stillon/main.go` o worker entra **dentro do
bloco `if cfg.Role.RunsAPI()`** (linha 177) com `g.Go(func() error { return worker.Run(gctx) })`,
e só quando a integração está ligada; o `RunMaintenance` (que roda no bloco do sondador, linha
158) não é tocado. A produção roda `--role=all`, e o filtro vale do mesmo jeito.

- [ ] Escrever testes com banco real e cliente controlado para ausência de membro, triagem pendente, concessão, cargo já presente, remoção, membro ausente na remoção, timeout ambíguo e `desired` alterado com PUT em voo.
- [ ] O worker age com a guild e o cargo **gravados na linha** (`guild_id`, `role_id`), nunca com os de `Options`: nem a remoção, que é o caso que a spec fixa, nem a concessão ou a leitura do membro. Teste com a configuração alterada depois do vínculo: vincular com guild e cargo A, montar o `Worker` com `Options` de guild e cargo B, desvincular e rodar; o cliente controlado tem de receber o DELETE com os ids de A e nenhuma chamada com os de B. Sabotar trocando a linha por `cfg.GuildID`/`cfg.RoleID` e ver o teste falhar.
- [ ] Rodada: tomar `pg_try_advisory_lock` numa conexão dedicada do pool, sem transação; se outra instância a tem, encerrar sem erro. A `TryLockRollups` da manutenção é de transação e não serve aqui, porque a rodada faz HTTP. Soltar a lock e devolver a conexão ao fim; se o unlock falhar, fechar a conexão. Testar duas rodadas simultâneas: só uma chama o cliente.
- [ ] Ler o lote em `InDiscordWorkerTx`, em transação curta: até 25 linhas com `next_attempt_at <= now` e `paused_at IS NULL`, por `next_attempt_at`. Para cada linha, fora de transação, aplicar a decisão da spec (ler membro, no máximo um efeito, reler) e gravar o resultado em nova transação com `WHERE id = $1 AND desired = $2`. Zero linhas afetadas não é erro: `Unlink` já agendou a linha, e a rodada seguinte reconcilia.
- [ ] Prazo total por rodada: `prazoDaRodada = 60 * time.Second`, um campo do `Worker` que o teste sobrescreve por um valor curto, aplicado com `context.WithTimeout` sobre o contexto de cada rodada. Sem ele, 25 linhas com duas chamadas de cinco segundos cada seguram a lock por mais de quatro minutos. Ao estourar, a rodada sai do lote, solta a lock e devolve a conexão; as linhas não processadas ficam como estão (`next_attempt_at` já vencido) e entram na rodada seguinte, e um efeito aplicado sem gravação se resolve na leitura dessa rodada, sem repetir. Teste: cliente controlado lento, `prazoDaRodada` curto, lote de várias linhas; só parte delas é tocada, a lock é solta e a segunda rodada termina o resto. Sabotar tirando o `WithTimeout` e ver o lote inteiro ser processado.
- [ ] Teste de corrida: PUT bloqueado em barreira, `Unlink` commita, PUT termina. A gravação não confirma, e a rodada seguinte faz DELETE e apaga a linha. Sabotar tirando `desired` do `WHERE` e ver o cargo ficar confirmado.
- [ ] Consultar membro antes de concessão; pending impede concessão. Após efeito incerto, manter pendente: a leitura da rodada seguinte confirma sem repetir o efeito. Nunca modificar outros cargos ou ingressar/expulsar usuário. `awaiting_*` zera `next_attempt_at`: só `Check` agenda de novo. A **confirmação** (`observed='present'` com `desired='present'`) também zera `next_attempt_at`, `attempts` e `last_failure` e grava `confirmed_at`: uma linha confirmada não é relida a cada rodada de dez segundos; só `Check` a agenda de novo. Teste "duas rodadas seguidas fazem um só GET de membro": linha que confirma na primeira rodada; na segunda, o cliente controlado conta zero chamadas novas. Sabotar deixando o `next_attempt_at` intacto na confirmação e ver o segundo GET.
- [ ] Implementar scheduler puro `nextAttempt(now time.Time, attempt int, providerWait time.Duration) time.Time`; atraso exponencial com teto local de quinze minutos, aumentado pelo prazo externo quando maior. A fórmula é `now + max(min(15 min, 10 s × 2^(attempt−1)), providerWait)`, com `attempt < 1` tratado como 1 e o deslocamento limitado para não estourar. `attempt` é o número da falha que acabou de acontecer (o contador já incrementado). Sem jitter: uma rodada só roda por vez, sob a lock. Teste de tabela, que cobre o crescimento e o teto:

| `attempt` | atraso local |
|---|---|
| 1 | 10 s |
| 2 | 20 s |
| 3 | 40 s |
| 4 | 1 min 20 s |
| 5 | 2 min 40 s |
| 6 | 5 min 20 s |
| 7 | 10 min 40 s |
| 8 | 15 min (seriam 21 min 20 s sem o teto) |
| 9 a 12 | 15 min |

Mais três casos: `providerWait` menor que o atraso local (5 s no `attempt` 8) não encurta o atraso; `providerWait` de 20 min no `attempt` 1 vale 20 min; `attempt` 100 não estoura e dá 15 min. O contador pausa em dez, então o 11 e o 12 só existem na função pura. Sabotar o teto e o `max` com o prazo do provedor. O caso do prazo do provedor mantém o formato abaixo:

```go
func TestNextAttemptPreservesProviderWait(t *testing.T) {
    now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
    got := nextAttempt(now, 1, 20*time.Minute)
    if got.Before(now.Add(20*time.Minute)) { t.Fatal("Retry-After antecipado") }
}
```

- [ ] 429 grava o prazo na linha e encerra a rodada. Ao atingir dez falhas ou configuração inválida, gravar `paused_at`, mantendo em `next_attempt_at` o prazo do provedor, e registrar no log só a categoria. O 401/403 e `FailureConfig` (guild ou cargo desconhecidos, códigos 10004 e 10011) pausam de imediato. `FailureNotFound` (10007) não é falha: na concessão vira `awaiting_member`, e na remoção conta como cargo já fora.
- [ ] `Check` é um `UPDATE` único e condicional. Sem trabalho pendente, agenda agora. Com trabalho ativo, não muda nada. Pausado, retoma só depois de `paused_at + 15 min` e de `next_attempt_at`, zerando o contador. Testar dez cliques concorrentes: nenhuma linha nova e nenhuma redução do prazo.
- [ ] `Unlink`, sob sessão viva: grava `desired='absent'`, cancela tentativas abertas do dono, limpa pausa e contador, agenda com `GREATEST(next_attempt_at, now)` e audita. Linha já `absent` não muda; sem linha, é sucesso. Testar que a morte do processo entre efeito e confirmação é recuperável.
- [ ] Remoção confirmada apaga a linha e grava a auditoria na mesma transação. A confirmação do cargo audita só na transição para `present` e zera o agendamento, o contador e a falha, como no passo da rodada.
- [ ] Limpeza: `DELETE FROM discord_oauth_attempts WHERE expires_at < now - 24 h` (consumidas ou não, porque toda tentativa expira em cinco minutos). Roda **no próprio worker do Discord**, no papel API, em intervalo mais longo que a rodada; não no `storage.RunMaintenance`, que pertence ao papel sondador. Teste: com o papel API sozinho, a limpeza roda e o `RunMaintenance` não é necessário; confere as linhas apagadas, e que uma tentativa vencida há menos de 24 h fica.
- [ ] Teste HTTP de dono, no padrão de `idor_integration_test.go` (`discord_dono_integration_test.go`, `TestDiscordDeOutroUsuarioNaoEhVisivelNemAlteravel`). As rotas não têm `{id}`, então não há 404 a comparar; o que se prova é o escopo: com A vinculado (nome de exibição canário) e B sem vínculo, o GET de B volta `unlinked`, sem nada de A no corpo; o `POST .../check` e o `DELETE` de B respondem sobre o estado de B e deixam a linha de A byte a byte como estava (releitura do banco: `desired`, `next_attempt_at`, `attempts`, `paused_at`, `updated_at`) e o GET de A inalterado. Sabotar fazendo `Check` e `Unlink` rodarem no escopo do worker, sem filtro de dono, e ver a linha de A mudar.
- [ ] Teste de arquitetura `TestSoODiscordlinkLeOEstadoDoCargo`, em `internal/discordlink/arquitetura_test.go`, no padrão de `rotasRegistradas` (`rotas_negar_por_padrao_integration_test.go`, que enumera as rotas com `go/parser`): percorre os fontes não-teste do módulo e falha se, **fora** de `internal/discordlink`, de `internal/storage/db` (código gerado, onde as queries nascem) e de `cmd/stillon` (a montagem), (a) algum identificador ou seletor começar com `DiscordLink` (as queries e o modelo de `discord_links`, daí o prefixo da Tarefa 1) ou (b) um arquivo que importe `internal/discordapi` usar o seletor `.Roles` (o campo de `Member`). Garante o que o STRIDE promete: o produto nunca lê o cargo para autorizar. Sabotar de duas formas: um arquivo descartável em `internal/httpapi` chamando `q.DiscordLinkByUser`, e outro em `internal/targets` lendo `member.Roles`; o teste tem de falhar nos dois, e passar com os arquivos removidos.
- [ ] Sabotar a condição pós-efeito, a lock da rodada, o respeito ao prazo, a limpeza e o filtro de papel API. Obter RED/GREEN, rodar `sh deploy/ci/checar-nome.sh` e commit `feat(discord): reconciliar cargo e desvinculação`.

### Tarefa 5: interface e prova de navegador

**Arquivos:** criar `web/src/features/configuracoes/{SecaoDiscord,SecaoDiscord.test}.tsx`, `web/e2e/discord.spec.ts` e `web/e2e/discord-falso.ts`; estender
`web/src/lib/api/schemas.ts`, `consultas.ts` e `cliente.ts` (não criar `discord.ts`: o padrão
atual concentra schemas, consultas e cliente nesses três arquivos); alterar `PaginaConfiguracoes.tsx`,
as fixtures existentes (`configuracoes.test.tsx`, `web/src/test/fixtures.ts`), as APIs falsas
(`web/src/test/api-falsa.ts`, `web/e2e/api-imitada.ts`), `web/e2e/{pilha,global-setup,global-teardown}.ts` e o job `e2e` do `ci.yml`.

**Consome:** DTO Status e rotas; **produz:** `SecaoDiscord`, uma **seção própria** em Configurações
(`<section aria-labelledby="t-discord">`, com `estiloDeCartao`, entre "Verificação em duas etapas" e "Sessões ativas",
`PaginaConfiguracoes.tsx:889`), irmã de `t-sessoes` e fora do `EstadoDaConsulta` de `/me`, porque
tem consulta própria. Segue a direção C e o sistema atual: `Botao` em pirâmide, com **Desvincular** rebaixado e
vermelho só na confirmação. Não altera a página de webhooks. A consulta entra em `chaves`
(`consultas.ts:9`) como `['account','discord']` e some com o `qc.clear()` do logout (`app/contexto.tsx:42`).

- [ ] Escrever testes de renderização dos oito estados e dos resultados cancelado/expirado. Nome é texto; CTA de alertas vai a Canais. Só estado `confirmed` mostra cargo confirmado. `TestNomeDeExibicaoRenderizaComoTexto` renderiza um nome como `<img src=x onerror=alert(1)>` e afirma que o texto aparece literal e que nenhum elemento `img` existe na seção; sabotar com `dangerouslySetInnerHTML` e ver o teste falhar.
- [ ] Adicionar schema Zod estrito para estados/resultados e datas em `schemas.ts`, com as chaves da tabela "Forma do JSON" (`snake_case`; `display_name`, `last_result`, `confirmed_at`, `retry_at`, `invite_url`, e `authorization_url` e `expires_at` no início); `useDiscordAccount` (em `consultas.ts`) faz GET dedicado. Não reutilizar cache infinito de `/me`, query de retorno ou localStorage como prova.
- [ ] Atualização enquanto pendente: `useDiscordAccount` usa `refetchInterval` em forma de função, de 4 s (constante nomeada, entre 3 e 5 s) enquanto o estado for `role_pending` ou `removal_pending`, e `false` nos demais; os estados `awaiting_*` e `paused` não mudam sozinhos, só por `Check`. `refetchIntervalInBackground` segue falso (`contexto.tsx:29`). Teste com relógio falso: pendente refaz o GET a cada tique, `confirmed` e `awaiting_member` não; sabotar com `refetchInterval: false` e com um intervalo fixo, nos moldes do teste de incidentes (`incidentes.test.tsx:189`).
- [ ] Implementar início por POST com trava síncrona de clique e URL de autorização validada antes de navegar na mesma aba. A validação compara as partes de `new URL(...)`, nunca prefixo de string: `protocol === 'https:'`, `host === 'discord.com'` (o `host` inclui a porta, então porta alheia falha), `pathname === '/oauth2/authorize'` e `username` e `password` vazios. Testes de tabela com `https://discord.com.evil.test/oauth2/authorize` (host parecido) e `https://discord.com@evil.test/oauth2/authorize` (o `discord.com` é a credencial, o host é `evil.test`), além de `http://`, porta e outro caminho: nenhum navega; sabotar com `startsWith('https://discord.com')` e ver os dois primeiros passarem a navegar. Check e unlink obedecem estado e retorno 202; GET não provoca mutação. O 409 do início vira orientação para concluir a remoção.
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

- [ ] Pilha do E2E (D3). O job `e2e` do CI roda contra a imagem de produção, que não tem o caminho falso; por isso `discord.spec.ts` usa a pilha própria (`pilha.ts`) e fica em `test.skip(!process.env.E2E_DISCORD, …)`, como o `capturas.spec.ts`. Com `E2E_DISCORD=1`, `pilha.ts` compila com `-tags e2e` e passa ao binário as seis variáveis com valores de mentira, `DISCORD_ALLOW_INSECURE_LOOPBACK=true` e `STILLON_E2E_DISCORD_ORIGEM`. O `discord-falso.ts` sobe no `globalSetup` (`global-setup.ts`), numa porta de loopback livre, **antes** de `subir()`, porque o binário precisa da origem ao iniciar; o `globalSetup` publica a URL em `process.env.E2E_DISCORD_FALSO_URL`, que chega aos workers do mesmo jeito que `E2E_BASE_URL` (`global-setup.ts:13`), e o `global-teardown.ts` o encerra. Como o fake roda no processo do `globalSetup` e o spec, no de um worker, o fake expõe um endpoint de controle para o spec registrar o `code_challenge` que viu na URL interceptada.
- [ ] O navegador continua indo a `https://discord.com/oauth2/authorize`: o spec intercepta essa URL com `page.route` e serve uma página de consentimento falsa com links de autorizar e cancelar, que abrem o callback como navegação de topo. A SPA não ganha exceção de origem, e a fixture de CSP do `fixtures.ts` segue valendo. `discord-falso.ts` guarda o `code_challenge` visto na URL e recusa o token sem o verifier correspondente.
- [ ] E2E: login real local → iniciar → consentimento falso controlado → callback backend
→ /settings limpo → pendente → cargo confirmado; acrescentar cancelamento, sessão
revogada, F5, logout/cache e desvinculação. Falhar se code/state aparecer em next,
storage, logs capturados ou URL final. O worker roda a cada 10 s e a tela refaz o GET a cada 4 s, então as esperas pelo estado confirmado e pela remoção usam `expect(...).toBeVisible({ timeout: 30_000 })`, e não o prazo padrão do Playwright.
- [ ] CI: um passo novo no job `e2e`, depois do Playwright do compose e com `setup-go`, `npm run build` e `E2E_DISCORD=1 npx playwright test discord.spec.ts --trace off`, sem `E2E_BASE_URL`. Passo, e não job: a lista de jobs obrigatórios está no `gate.py` e no ruleset, e o job já tem Node e Docker. Conferir que o `timeout-minutes` de 20 ainda basta. O gate contra `-tags e2e` na imagem não mora neste job: é o `grep` de `STILLON_E2E_DISCORD_ORIGEM` em `deploy/ci/checar-imagem.sh` (Tarefa 2, job `image`).
- [ ] Verificar teclado, foco, anúncios, CSP, claro/escuro e 360px. Sabotar confirmação
prematura, trava, isolamento do cache e query limpa; RED/GREEN. Rodar `sh deploy/ci/checar-nome.sh`. Commit
`feat(painel): apresentar vinculação de conta Discord`.

### Tarefa 6: integração, documentação e ativação

**Arquivos:** atualizar README (tabela de segurança e configuração), `.env.example`, `compose.yaml` e `deploy/coolify/app.json`;
registrar relatório sem segredos em `docs/superpowers/reports/`, com a data da execução.

- [ ] Conferir spec item a item, diff completo, queries geradas, ausência de credenciais,
licenças/dependências e comportamento desligado no ambiente atual (variáveis vazias em `app.json`).
- [ ] Executar `go build ./...`, `go vet ./...`, `go vet -tags e2e ./internal/discordapi/...` e `make test` (PG18 por padrão) e de novo com
`TEST_POSTGRES_IMAGE` do PG17 e o digest de `ci.yml:108`; web typecheck, lint, build, Vitest e E2E, inclusive `discord.spec.ts`, e
`sh deploy/ci/checar-nome.sh`. Preservar contêineres/volumes preexistentes; sem cleanup global.
Registrar hashes e comandos/resultados reais.
- [ ] Revisão independente de autorização, RLS, corridas de cargo e UI. Corrigir achados
e repetir somente gates afetados, depois gate final conforme exigências do projeto.
- [ ] Commit final assinado, verificar assinatura, push e PR em português, sem `[skip ci]`. Aguardar CI
do HEAD exato e conferir árvore/base antes do merge autorizado na main. Confirmar antes que o número da
migration ainda é o maior (ver "Ordem de integração") e que o sqlc foi regerado.
- [ ] Depois do merge, conferir o run do `Coolify release` do SHA. O release só troca a imagem, e a
função segue desligada: `app.json` só chega ao container quando o commit de infraestrutura fixado
no recurso é atualizado e o recurso é publicado de novo ([README do Coolify](../../../deploy/coolify/README.md)).
Fazer isso ainda sem as variáveis, confirmar `/readyz` 200 e o estado `unavailable` no painel.
- [ ] Preparar aplicativo/bot, cargo e URLs de retorno (a de produção e a de loopback da prova do PKCE) para revisão. Antes de gerar
segredos, preparar entrada Bitwarden. Confirmar no navegador o acesso Gerenciar cargos,
sem Administrador; credenciais pelas variáveis privadas do recurso, nunca chat. Etapas que exigem ação
pessoal do dono são entregues no ponto concreto, sem simular resultado.
- [ ] Definir as seis variáveis de uma vez e publicar o recurso. Validar consentimento e cargo com
conta do operador, depois unlink e remoção; capturar apenas telas sem segredos.
- [ ] Provar o PKCE real, num caminho fora do produto: uma tentativa legítima, com o code trocado por
verifier errado, tem de ser recusada. O script é descartável, não entra no repositório nem na imagem.
Ele monta a URL de autorização com `code_challenge` S256 e o redirect de loopback cadastrado no portal
(`http://127.0.0.1:<porta fixa>/callback`, o segundo redirect da tabela de pré-requisitos), recebe o
`code` num servidor local e tenta a troca primeiro com um verifier errado (esperado: recusa) e, em
outra tentativa com outro `code`, com o verifier certo (esperado: aceita, como controle). O client
secret vem de uma variável de ambiente local, preenchida pelo dono a partir do Bitwarden, e nunca do
chat, de argumento de linha de comando ou de arquivo; o script não o imprime. Depois da prova, o
dono remove o redirect de loopback do portal. Se o Discord recusar o fluxo ou aceitar o verifier errado,
limpar as seis variáveis, publicar de novo e levar a decisão ao dono (D1).
Se faltar credencial, entregar código/gates/PR e marcar ativação real pendente.

## Revisão do plano

Cobertos: escopo de identificação, cookie/state/PKCE/sessão, RLS e unicidade, reconciliação
por estado desejado, configuração parcial, retorno limpo, retry, desvinculação, retenção,
interface, gates e ativação real. Contratos de tipos estão definidos acima (passo 0, dono único); não há etapa de
login Discord, cadastro público, leitura de mensagens ou bloqueio de canais.

Revisado em 2026-10-14 contra `8ca962d` e em 2026-11-07 contra `42715ac`: caminhos e linhas
conferidos no código atual, migration 00023, produção em `deploy/coolify/app.json`, tabela
STRIDE ampliada e decisões D1–D3 aplicadas à spec e às tarefas. Revisado de novo em 2026-11-07
contra `f16edc2`, com os achados de uma revisão independente: políticas de SELECT que a RLS exige
para o `DELETE` e o `UPDATE ... RETURNING`, teste de arquitetura do cargo, worker com guild e cargo
da linha, `FailureNotFound` e `FailureConfig`, `Meta` e chave de hash de IP, confirmação que zera o
agendamento, gate de `-tags` em `checar-imagem.sh`, contratos em passo 0 e as demais correções
listadas nas tarefas.
