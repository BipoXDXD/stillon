# Plano de implementação — vinculação Discord

> **Para agentes:** usar `superpowers:subagent-driven-development` para executar este plano por tarefa. Etapas usam caixas de seleção para registrar conclusão comprovada.

**Objetivo:** vincular uma conta Discord ao dono autenticado do StillOn e gerenciar o cargo Verificado como identificação, sem restringir canais.

**Arquitetura:** OAuth no backend com state e cookie de ponte independentes, ambos vinculados à família original. PostgreSQL registra vínculo e intenção durável; worker no papel API reconcilia somente o cargo configurado. Configurações consulta estado próprio, separado dos webhooks.

**Stack:** Go e net/http, pgx/sqlc/PostgreSQL, React/TypeScript/TanStack Query/Zod e as ferramentas de testes já presentes. Sem SDK Discord nem dependência nova prevista.

**Spec:** [especificação aprovada](../specs/2026-10-05-vinculacao-discord-design.md).

## Restrições globais

- Cargo apenas identifica contas vinculadas: não bloqueia canais e não concede administração.
- Escopo OAuth mínimo: `identify`; tokens OAuth não são persistidos.
- State e nonce independentes, 32 bytes cada; validade de cinco minutos; consumo único.
- Refresh existente permanece HttpOnly/SameSite=Strict; cookie ponte usa Lax e caminho do callback.
- HTTP apenas com `DISCORD_ALLOW_INSECURE_LOOPBACK=true` e origem local validada; padrão false.
- Worker consulta a cada dez segundos; dez tentativas; backoff máximo de quinze minutos, respeitando Retry-After maior.
- Tudo em português do Brasil; commits assinados; main somente por PR; documentação usa `[skip ci]`.
- Teste novo requer sabotagem compilável detectada, restauração e GREEN; não usar segredos reais em gates.
- Gates finais: `go build ./...`, `go vet ./...`, `make test` com PG17/18, web e CI do HEAD final.
- Não modificar o candidato congelado nem interferir em volumes/contêineres preexistentes.

## Foco da revisão

1. Logout durante a troca OAuth não pode concluir vínculo: teste de barreira na tarefa 3.
2. Cargo concedido após desvinculação exige reconciliação do estado atual: teste concorrente na tarefa 4.
3. Duplo clique ou callback repetido não pode trocar dois codes: testes nas tarefas 3 e 5.
4. Resposta externa ambígua não pode virar confirmação falsa: testes 429/timeout nas tarefas 2 e 4.
5. Retorno sem sessão não pode carregar code/state no `next`: prova HTTP e navegador na tarefa 5.

## Equipe, isolamento e ordem

Manter Astra Medium na coordenação e quatro Sol High auxiliares, conforme pedido.
Auxiliar Go possui persistência/serviço; auxiliar de integração possui cliente Discord e
worker; auxiliar web possui componentes/contratos web; auxiliar de auditoria revisa e
acompanha gates. Coordenador possui configuração, handlers, composição e documentação.
Todos trabalham em clone/worktree exclusivo da nova fase; nenhum agente reverte trabalho
alheio. Arquivos gerados por sqlc são propriedade do auxiliar Go, não editados à mão.

Tarefa 1 precede 3/4; tarefa 2 pode avançar em paralelo à 1. Congelar os contratos abaixo
antes de implementar 3/4/5. Mudança de interface exige aviso ao coordenador e atualização
dos consumidores. Cada tarefa termina em commit assinado e revisão; um PR funcional
reúne o fluxo completo, sem publicar uma função parcialmente ativada.

## Contratos compartilhados

Criar `internal/discordlink/types.go` e `internal/discordapi/types.go`. A camada de domínio
consome o cliente por interface; handlers nunca acessam token do bot.

```go
// package discordapi
type Identity struct { ID, DisplayName string }
type Member struct { Exists, Pending bool; Roles []string }
type Failure struct { Kind string; RetryAfter time.Duration }
func (e *Failure) Error() string { return "discord: " + e.Kind }
type Client interface {
    IdentityFromCode(context.Context, string, string) (Identity, error)
    Member(context.Context, string, string) (Member, error)
    SetRole(context.Context, string, string, string, bool) error
}
// args: code/redirect; guild/user; guild/user/role/present.
```

```go
// package discordlink
type Principal struct { UserID, FamilyID uuid.UUID }
type Status struct {
    Enabled bool `json:"enabled"`
    State string `json:"state"`
    DisplayName string `json:"display_name,omitempty"`
    LastResult string `json:"last_result,omitempty"`
    ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
    RetryAt *time.Time `json:"retry_at,omitempty"`
    InviteURL string `json:"invite_url,omitempty"`
}
type Start struct { AuthorizationURL, BrowserNonce string; ExpiresAt time.Time }
type Callback struct { State, BrowserNonce, Code, ProviderError string }
type Service interface {
    Get(context.Context, Principal) (Status, error)
    Begin(context.Context, Principal) (Start, error)
    Complete(context.Context, Callback) error
    Check(context.Context, Principal) (Status, error)
    Unlink(context.Context, Principal) (Status, error)
}
```

`Status.State` admite somente `unavailable`, `unlinked`, `awaiting_member`,
`awaiting_screening`, `role_pending`, `confirmed`, `removal_pending`, `paused`.
`LastResult` admite vazio, `cancelled`, `expired`, `failed`, `linked`; sem texto do
provedor. Erros de domínio exportados: `ErrSessionGone`, `ErrInvalidAttempt`,
`ErrIdentityInUse`, `ErrUnavailable`, `ErrTooEarly`. Constructor concreto:
`NewService(pool *pgxpool.Pool, cfg Options, api discordapi.Client, clk clock.Clock) *LinkService`.
`Options` contém ClientID, GuildID, RoleID, RedirectURL e InviteURL; nenhum segredo.

### Tarefa 1: persistência, isolamento e sessão

**Arquivos:** criar migration `internal/storage/migrations/00012_discord_links.sql`,
`internal/storage/queries/discord_links.sql`, `internal/discordlink/{types,store,store_test}.go`;
alterar `internal/storage/scope.go` e gerar `internal/storage/db/*` com `make generate`.

**Produz:** tentativas com hashes, vínculo único e operações duráveis por identidade;
escopos mínimos `InDiscordCallbackTx` e `InDiscordWorkerTx`, sem ampliar prober/verify.
Callback recebe os dois hashes; worker não pode consultar tokens/canais.

- [ ] Escrever testes PostgreSQL de FORCE RLS, conexão reutilizada, tentativa usada/vencida e UNIQUE de DiscordID entre dois donos. Cada teste usa a fixture Docker existente e verifica dados reais após commit/rollback.
- [ ] Criar DDL com `state_hash bytea UNIQUE CHECK(octet_length(state_hash)=32)`, `browser_hash bytea CHECK(octet_length(browser_hash)=32)`, FK de dono, FamilyID lógico e datas. Usar chave única de DiscordID em registro de identidade estável, com geração e reserva durante remoção; trabalho referencia essa identidade.
- [ ] Escrever a reserva atômica de tentativa com a condição completa, sem consulta otimista anterior:

```sql
UPDATE discord_oauth_attempts
SET consumed_at = sqlc.arg(now)
WHERE state_hash = sqlc.arg(state_hash)
  AND browser_hash = sqlc.arg(browser_hash)
  AND consumed_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;
```

- [ ] No mesmo escopo transacional, aplicar LockUserSessions antes da linha da tentativa e conferir FamilyIsLive. Criar operações/auditoria com o vínculo na mesma transação; UNIQUE em identidade/geração impede trabalho duplicado.
- [ ] Executar testes focados; sabotar separadamente o filtro de dono, o consumo único e UNIQUE do DiscordID. Esperar falha por isolamento/replay/duplicação, restaurar e executar GREEN.
- [ ] Confirmar que tentativa errada não lê identidade e que rollback não deixa auditoria/trabalho órfão. Commit `feat(discord): persistir vínculos e tentativas isoladas`.

### Tarefa 2: cliente Discord e configuração

**Arquivos:** criar `internal/discordapi/{types,client,client_test}.go`; alterar
`internal/config/config.go`, seus testes, `.env.example` e `compose.yaml` pelo coordenador.

**Consome:** `discordapi.Client`; **produz:** `NewClient(clientID, clientSecret, botToken string) *HTTPClient`.
Produção usa exclusivamente origem HTTPS fixa do Discord. Transporte/URL de teste são
injetados apenas por constructor privado no mesmo pacote dos testes.

- [ ] Escrever `httptest.Server` que exige método, caminho, autenticação e corpo esperados para token, `/users/@me`, membro e PUT/DELETE de cargo. Servidor falha se receber bearer do usuário na operação de bot ou segredo em query.
- [ ] Implementar troca form-urlencoded no servidor, decodificação limitada e rejeição de ID inválido. Descartar access/refresh depois de identificar. `SetRole` altera um cargo, nunca substitui o array de cargos do membro.
- [ ] Desabilitar redirects no cliente e limitar corpo de resposta a 64 KiB e chamada a cinco segundos. `Failure.Error` contém só categoria segura; não incluir corpo, token, code, URL de webhook ou header Authorization.
- [ ] Exercitar 401, 403, 404 de membro, 429, 5xx, JSON inválido, corpo excessivo e timeout depois de registrar efeito. Caso 429 deve produzir `Failure{Kind:"rate_limited", RetryAfter:...}` a partir da indicação válida do provedor.
- [ ] Configurar todos os campos ausentes como função desligada; parcial como erro sem valores. Restringir convite e loopback como na spec; enviar secrets apenas ao papel API, nunca ao frontend ou seed.
- [ ] Usar tabela de validação, incluindo os negativos abaixo, e provar RED removendo validação/limite/negação de redirect, depois GREEN:

```go
for _, raw := range []string{
    "http://discord.gg/convite", "https://discord.gg.evil.test/convite",
    "https://user:secret@discord.gg/convite", "https://discord.gg/convite?token=x",
} {
    if validInviteURL(raw) { t.Fatalf("convite indevido aceito: %s", raw) }
}
```

`validInviteURL(string) bool` é função pura no arquivo de configuração; aceita apenas
os hosts/caminhos definidos na spec. Commit `feat(discord): integrar cliente OAuth e cargos`.

### Tarefa 3: autorização, retorno e handlers

**Arquivos:** criar `internal/discordlink/{service,service_test}.go`,
`internal/httpapi/{discordhandlers,discordhandlers_test}.go`; alterar `routes.go`,
`cmd/stillon/main.go` e logger apenas se atualmente incluir query do callback.

**Consome:** persistência e cliente; **produz:** os cinco métodos de `Service` e handlers
`Get`, `Authorize`, `Callback`, `Check`, `Unlink` em `DiscordHandlers`.

- [ ] Escrever testes com nonce/state errados, ausentes, vencidos e repetidos, incluindo dois callbacks concorrentes. Cliente controlado conta trocas: exatamente uma quando ambos competem pela mesma tentativa.
- [ ] `Begin` gera duas leituras independentes de crypto/rand, persiste SHA256 em transação com sessão viva e devolve URL construída por `net/url`, não concatenação de input. Segunda tentativa invalida a anterior da família.
- [ ] No callback, reservar tentativa sob guarda de sessão, fechar transação, chamar `IdentityFromCode` e abrir nova transação com guarda de sessão antes de vínculo/auditoria/operação. Não manter conexão transacional durante HTTP.
- [ ] Provar revogação concorrente com barreira no cliente controlado: iniciar callback, bloquear troca, revogar família no banco, liberar resposta; esperar `ErrSessionGone`, nenhum vínculo ou grant. Refresh legítimo da mesma família deve passar.
- [ ] Implementar cookie e retorno sem query. Trecho contratual do handler:

```go
http.SetCookie(w, &http.Cookie{
    Name: "stillon_discord_oauth", Value: started.BrowserNonce,
    Path: "/api/v1/oauth/discord/callback", HttpOnly: true,
    Secure: secureCookie, SameSite: http.SameSiteLaxMode,
    MaxAge: 300, Expires: started.ExpiresAt,
})
// Depois de callback validado: limpar o mesmo cookie e retornar 303 para /settings.
```

- [ ] Registrar GET/POST/DELETE nas rotas da spec, com Bearer, balde compartilhado e família viva para mutações; GET callback tem limite por IP e headers no-store/no-referrer. Callback malformado não ecoa dados externos.
- [ ] Testar status HTTP/JSON, integração desligada, CSRF, ausência de sessão e redirecionamento fixo. Sabotar vínculo da família, comparação de cookie e limpeza de query; registrar RED/GREEN. Commit `feat(discord): vincular identidade à sessão autenticada`.

### Tarefa 4: concessão, reconciliação e desvinculação

**Arquivos:** criar `internal/discordlink/{worker,worker_test}.go`; ampliar queries de
trabalho sob ownership Go; coordenador conecta o ciclo de vida em `cmd/stillon/main.go`.

**Produz:** `NewWorker(pool *pgxpool.Pool, api discordapi.Client, cfg Options, clk clock.Clock) *Worker`,
`Run(context.Context) error` e `RunOnce(context.Context) error`. Run usa ticker de dez
segundos e encerra no contexto da API; somente all/api inicia o worker.

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

- [ ] Ao atingir dez falhas ou configuração inválida, pausar. Check preserva prazo/orçamento ativo; retomada elegível somente após quinze minutos e Retry-After. Testar dez cliques concorrentes sem dez jobs nem redução do prazo.
- [ ] Unlink sob sessão viva avança geração, cancela tentativas e reserva identidade até remoção/quiescência. Cleanup respeita 24h e operações antigas. Testar que compensação antiga não remove cargo de vínculo novo e que morte entre efeito/confirmação é recuperável.
- [ ] Sabotar geração pós-efeito, reserva, respeito ao prazo e filtro de papel API. Obter RED/GREEN e commit `feat(discord): reconciliar cargo e desvinculação com segurança`.

### Tarefa 5: interface e prova de navegador

**Arquivos:** criar `web/src/features/configuracoes/{ContaDiscord,ContaDiscord.test}.tsx`,
`web/src/lib/api/discord.ts`; alterar schemas/consultas, PaginaConfiguracoes e fixtures
existentes. Criar `web/e2e/discord.spec.ts` usando provedor falso local, sem credenciais reais.

**Consome:** DTO Status e rotas; **produz:** `ContaDiscord` dentro da seção Conta, sem
alterar a página de webhooks. Consulta tem chave `['account','discord']`, limpa com logout.

- [ ] Escrever testes de renderização dos oito estados e dos resultados cancelado/expirado. Nome é texto; CTA de alertas vai a Canais. Só estado `confirmed` mostra cargo confirmado.
- [ ] Adicionar schema Zod estrito para estados/resultados e datas; `useDiscordAccount` faz GET dedicado. Não reutilizar cache infinito de `/me`, query de retorno ou localStorage como prova.
- [ ] Implementar início por POST com trava síncrona de clique e URL de autorização validada antes de navegar na mesma aba. Check e unlink obedecem estado e retorno202; GET não provoca mutação.
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

- [ ] E2E: login real local → iniciar → consentimento falso controlado → callback backend
→ /settings limpo → pendente → cargo confirmado; acrescentar cancelamento, sessão
revogada, F5, logout/cache e desvinculação. Falhar se code/state aparecer em next,
storage, logs capturados ou URL final.
- [ ] Verificar teclado, foco, anúncios, CSP, claro/escuro e 360px. Sabotar confirmação
prematura, trava, isolamento do cache e query limpa; RED/GREEN. Commit
`feat(painel): apresentar vinculação de conta Discord`.

### Tarefa 6: integração, documentação e ativação

**Arquivos:** atualizar README, `.env.example`, compose e runbook local; registrar
relatório sem segredos em `docs/superpowers/reports/2026-10-05-vinculacao-discord.md`.

- [ ] Conferir spec item a item, diff completo, queries geradas, ausência de credenciais,
licenças/dependências e comportamento desligado no ambiente atual.
- [ ] Executar build/vet/make test PG17/18 com Docker e fontes congeladas; web typecheck,
lint/build/Vitest/E2E e `sh deploy/ci/checar-nome.sh`. Preservar contêineres/volumes
preexistentes; sem cleanup global. Registrar hashes e comandos/resultados reais.
- [ ] Revisão independente de autorização, RLS, corridas de cargo e UI. Corrigir achados
e repetir somente gates afetados, depois gate final conforme exigências do projeto.
- [ ] Commit final assinado, verificar assinatura, push e PR em português. Aguardar CI
do HEAD exato e conferir árvore/base antes do merge autorizado na main.
- [ ] Preparar aplicativo/bot, cargo e URLs de retorno para revisão. Antes de gerar
segredos, preparar entrada Bitwarden. Confirmar no navegador o acesso Gerenciar cargos,
sem Administrador; credenciais por ambiente local, nunca chat. Etapas que exigem ação
pessoal do dono são entregues no ponto concreto, sem simular resultado.
- [ ] Com configuração privada pronta, validar consentimento e cargo com conta do
operador, depois unlink e remoção; capturar apenas telas sem segredos. Não fazer
deploy VPS. Se faltar credencial, entregar código/gates/PR e marcar ativação real pendente.

## Revisão do plano

Cobertos: escopo de identificação, cookie/state/sessão, RLS e unicidade, trabalho
durável, configuração parcial, retorno limpo, retry, desvinculação, retenção, interface,
gates e ativação real. Contratos de tipos estão definidos acima; não há etapa de login
Discord, cadastro público, leitura de mensagens ou bloqueio de canais. Execução mantém
o método de quatro auxiliares já escolhido pelo dono e aguarda sua revisão deste plano.
