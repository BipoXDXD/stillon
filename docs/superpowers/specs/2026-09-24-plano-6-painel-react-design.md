# Vigilume — Plano 6: painel React e o servir do painel pelo binário (design)

**Status:** proposta, aguardando revisão do usuário. Nada aqui é código de produto nem plano de
implementação; o plano vem depois de as três specs (5, 6 e 7) serem aprovadas.

**Contrato:** `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`. Endpoints, shapes e a
decisão de servir o painel pelo binário (`internal/webui`, `//go:embed all:dist`, mesma origem)
valem como estão. Onde esta spec discorda ou acrescenta, está na §11.

**Spec do projeto:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§5.3, §5.7, §9, §9.1,
§11 inteira, §14, §18). **Spec do Plano 5:** `…/2026-09-24-plano-5-api-do-painel-design.md`
(§3, §7 e §12 — notas para o Plano 6, incorporadas aqui).

**Base de código:** `main` em `a008b01` (Plano 4 integrado). Não existe `web/` nem `internal/webui`.

**Validação prévia.** As perguntas de maior risco foram respondidas com experimento, não com
opinião, em spikes **descartáveis fora do repositório** (`/tmp/claude-1000/plano6-spike/`), com as
versões atuais das bibliotecas (conferidas no npm e no Context7 em 2026-09-24). Resultados na §8.

1. **CSP estrita × Tailwind/shadcn/Radix/Recharts** (spec §11.7, "verificar no dia 11"): o que
   injeta `<style>` em runtime, o que quebra de forma visível, e qual saída funciona.
2. **Refresh concorrente**: o *single-flight* dentro da aba, e a corrida entre **abas** (nota do
   Plano 5, R7), reproduzida num navegador real, com e sem Web Locks.
3. **`//go:embed all:dist` só com `.gitkeep`**, tipos MIME da stdlib e o `emptyOutDir` do Vite.
4. **Vitest 5 + MSW 2 + jsdom**, e **axe-core sob a CSP estrita** (sem `bypassCSP`).

---

## 1. Objetivo

Entregar o painel administrativo da spec §11 — login com 2FA, dashboard, CRUD de alvos, detalhe
com gráfico, incidentes, canais, status page e configurações — servido pelo próprio binário Go,
com:

- **token só em memória**, sessão re-hidratada pelo cookie `HttpOnly` no boot, refresh
  *single-flight* dentro da aba e **serializado entre abas**;
- **CSP sem `unsafe-inline`** também no painel (spec §5.7), com nonce por requisição;
- estado comunicado por **ícone e texto**, anunciado em `aria-live`, navegável por teclado, com
  contraste AA verificado nos dois temas;
- testes que falham quando a propriedade some (Vitest + MSW com handlers que imitam as guardas do
  servidor; um E2E Playwright contra o binário real; prova por sabotagem das asserções de ausência).

O painel é a vitrine "fullstack" do portfólio (spec §1.1). O que ele precisa demonstrar, em ordem:
autenticação no cliente correta sob concorrência; a CSP estrita mantida sem atalho; acessibilidade
real; e testabilidade — a mesma disciplina de sabotagem do backend.

## 2. Escopo e fora de escopo

**Dentro:**

| Item | Onde |
|---|---|
| Projeto do painel (Vite 8 + React 19 + TS strict) | `web/` |
| Camada de API: fetch com refresh, envelope de erro, schemas Zod das respostas | `web/src/lib/api/` |
| As oito rotas da spec §11.3 | `web/src/features/*` |
| Handler Go que serve o painel (cache, fallback de SPA, CSP com nonce, MIME) | `internal/webui` |
| Montagem do painel na raiz e da API em `/api/` | `internal/httpapi/routes.go` (wiring) |
| Gerador de nonce compartilhado com a página pública | `internal/crypto` (movido de `publicpage.go`, D27) |
| Testes Vitest + Testing Library + MSW | `web/src/**/*.test.ts(x)`, `web/src/test/` |
| E2E Playwright do fluxo crítico, contra o binário real e Postgres em container | `web/e2e/` |
| Verificação de acessibilidade (axe no Playwright, dois temas) e de CSP em todas as rotas | `web/e2e/` |
| Textos de ajuda que as pendências pedem (headers × redirect, webhook de fórum, janela de 15 min, cache de 30 s) | telas |

**Fora (e para onde vai):**

- **SSE/WebSocket** (spec §2.2): `refetchInterval` resolve.
- **Imagem, compose, CI, deploy, README:** Plano 7. Esta spec diz o que o 7 precisa rodar (§11.3).
- **Endpoints novos:** Plano 5. O painel pede um acréscimo opcional (§11, Div. 3) e tem desenho
  que não depende dele.
- **Trusted Types** (`require-trusted-types-for 'script'`): o spike mostrou que Radix e o
  `ChartStyle` do shadcn usam `innerHTML` e quebram (§8.1). Endurecimento futuro, não deste plano.
- **Pré-visualização fiel da status page em rascunho:** inviável sem API nova (§5.7, D23).
- **Internacionalização:** só pt-BR (com `Intl` para números e datas).
- **Rota de desabilitar canal** (pendência): o painel não precisa dela.

**Essencial × cortável.** A spec §18 prevê corte no front. A ordem de construção (§3.8) entrega um
painel apresentável na fatia 3. O que pode sair, **nesta ordem**, se o prazo apertar:

| # | Corte | O que se perde | O que fica |
|---|---|---|---|
| 1 | gzip dos assets no handler (D17) | ~790 → ~240 KB na primeira carga | Tudo funciona, só mais pesado |
| 2 | Prévia aproximada da status page (D23) | A lista de "como vai aparecer" | Link público, estado de publicação |
| 3 | Goldens de contrato Go → MSW (D11) | Detecção automática de deriva DTO × fixture | Zod nas respostas + E2E pegam a deriva no caminho crítico |
| 4 | Aviso "sessão iniciada" entre abas | Aba parada no `/login` não entra sozinha | O aviso de "sessão encerrada" (esse não sai) |
| 5 | Downsampling da série bruta | Gráfico de 48 h a 30 s com 5.760 pontos | Janela padrão de 24 h |
| 6 | Tema escuro (spec §18 já o lista) | Override manual e `.dark` | `prefers-color-scheme` do sistema, se o tempo der; senão, só claro |
| 7 | Teste E2E de várias abas (§7.4) | A prova no navegador real | A prova determinística no Vitest (§7.2) |
| 8 | Firefox no projeto `a11y` do Playwright | CSP/axe num segundo motor | Chromium |

**Não se cortam:** Web Locks (sem ele a sessão cai com duas abas — §8.2), o nonce (sem ele o
gráfico some — §8.1), ícone+texto e `aria-live`, e o E2E do fluxo crítico (a peça que prova
navegador + binário + banco juntos; ficou barato com o harness da §7.4).

## 3. Arquitetura

### 3.1 Dependências (versões conferidas em 2026-09-24)

| Camada | Pacote | Versão | Nota |
|---|---|---|---|
| Build | `vite` / `@vitejs/plugin-react` | 8.3 / 6.1 | Rolldown; config com `import.meta.dirname` e extensões explícitas (o loader nativo avisa) |
| Linguagem | `typescript` | 6.0 | `strict` passa a ser padrão no 6.0; fica explícito no `tsconfig` |
| UI | `react` / `react-dom` | 19.3 | |
| Rotas | `react-router` | **8.4** | Spec §11.1 dizia v7; D5 |
| Servidor | `@tanstack/react-query` | 5.103 | |
| Formulários | `react-hook-form` / `@hookform/resolvers` / `zod` | 7.88 / 5.9 / 4.6 | O resolver detecta Zod 4 |
| Estilo | `tailwindcss` + `@tailwindcss/vite` | 4.3 | CSS estático no build |
| Componentes | `shadcn` (CLI, base `radix`) + `radix-ui` | 4.21 / 1.6.7 | Código copiado para `components/ui`, editado (D2) |
| Gráficos | `recharts` | 3.10 | Só no detalhe, carregado sob demanda (D4) |
| Ícones / QR | `lucide-react` / `qrcode.react` | — | QR como `<svg>` React, sem `data:` (a CSP não libera `img-src data:`) |
| Testes | `vitest` / `jsdom` / `@testing-library/*` / `msw` | 5.0 / 30 / 16 / 2.15 | |
| E2E | `@playwright/test` / `@axe-core/playwright` | 1.63 / 4.13 | |
| Lint | `oxlint` (regras `jsx-a11y` inclusas) | 1.x | Vem no template do Vite |

**Fora de propósito:** `sonner` (injeta `<style>` sem suporte a nonce, §8.1), `next-themes`
(o *preset* do shadcn o traz; injeta `<script>`/`<style>`; um `ThemeProvider` de 40 linhas basta),
`@fontsource-variable/geist` (fonte do sistema: 5 arquivos a menos e `font-src` sem uso real).
Node ≥ 22.22 (exigência do React Router 8); o `package.json` declara `engines`.

### 3.2 Diretórios

```
web/
  index.html            <html lang="pt-BR">; <script src="/tema.js"> no <head> (D20)
  public/tema.js        aplica .dark antes da primeira pintura (arquivo, não inline)
  vite.config.ts        html.cspNonce, outDir ../internal/webui/dist, proxy /api, plugin .gitkeep
  src/
    main.tsx            lê o nonce, dispara o boot da sessão, monta os providers
    app/                router.tsx, providers.tsx, layout (nav, região aria-live, skip link),
                        RequireAuth, Splash, ErrorBoundary de rota, NotFound
    lib/
      nonce.ts          lê <meta property="csp-nonce">, chama setNonce do get-nonce
      api/              http.ts (fetch + erros), auth.ts (token, refresh, boot, abas),
                        schemas.ts (Zod das respostas), endpoints/*.ts, keys.ts, errors.ts
      format.ts         porcento truncado, ms, durações, datas relativas (Intl pt-BR)
      theme.tsx         ThemeProvider próprio
    components/ui/      shadcn (com o nonce propagado onde há <style>, D2)
    components/         StatusBadge, Sparkline (SVG próprio), LatencyChart (Recharts),
                        IncidentTimeline, LiveAnnouncer, FieldError, ConfirmDialog, Toaster
    features/           auth/ dashboard/ targets/ incidents/ channels/ statuspages/ settings/
    test/               setup.ts (MSW, polyfills do jsdom), handlers.ts, fixtures/
  e2e/                  global-setup.ts, global-teardown.ts, init.sql, fluxo.spec.ts,
                        abas.spec.ts, a11y.spec.ts, fixtures (compartilhadas com o MSW)
internal/webui/
  webui.go              New(fs.FS) http.Handler, Dist() fs.FS
  webui_test.go
  dist/.gitkeep         único arquivo versionado de dist/
```

Organização por *feature* (spec §11.8). O `.gitignore` troca `/web/dist/` (que nunca existirá)
por `/internal/webui/dist/*` + `!/internal/webui/dist/.gitkeep`, e acrescenta
`/web/test-results/`, `/web/playwright-report/` e `/web/e2e/.tmp/`.

### 3.3 Providers e boot

```
main.tsx
  import './lib/nonce'                 // antes de qualquer componente (get-nonce)
  const boot = auth.boot()             // UMA promessa de módulo, fora do React (D9)
  createRoot(...).render(
    <StrictMode>
      <QueryClientProvider>            // defaults da §3.5
        <AuthProvider boot={boot}>     // estado: checking | authenticated | anonymous | offline
          <ThemeProvider>
            <Toast.Provider>           // Radix Toast (D3)
              <TooltipProvider>
                <RouterProvider router={router} />
```

`AuthProvider` expõe `status`, `login`, `verifyTotp`, `logout` e `sessionEnded(motivo)`. Enquanto
`status === 'checking'` o app mostra um *splash*, nunca o `/login` (spec §11.2, item 1): sem isso,
cada F5 pisca a tela de login. `offline` é "o servidor não respondeu" (rede ou 5xx no boot):
tela com "Tentar de novo", **não** o login, porque a sessão pode estar boa.

O boot fica fora da árvore porque o `StrictMode` monta, desmonta e remonta os efeitos em
desenvolvimento: um `useEffect(() => refresh())` dispara **dois** refreshes com o mesmo cookie, e
o segundo é reúso — a família cai (spec §5.2). O *single-flight* também absorveria, mas depender
dele para o boot é depender de a segunda chamada chegar enquanto a primeira voa. Teste na §7.2.

### 3.4 Camada de API e autenticação no cliente

**`http.ts`** — um único ponto por onde passa toda chamada:

- prefixo `/api/v1`, `credentials: 'same-origin'` (o cookie só vai para `/api/v1/auth`, pelo
  `Path`; mesma origem, D21);
- `Authorization: Bearer <token>` quando há token;
- **`X-Requested-With: vigilume` em todo método que não seja GET/HEAD**, inclusive
  `/auth/refresh`, `/auth/login` e `/auth/logout`: o `RequireCustomHeader` envolve a API inteira,
  rotas de autenticação incluídas (`routes.go`);
- corpo de erro lido como o envelope da §9.1 → `ApiError { status, code, message, fields,
  traceId }`. Resposta que não é o envelope (502 do proxy, HTML, rede caída) vira
  `ApiError { code: 'network' | 'unexpected' }`, nunca uma exceção crua na tela;
- sucesso validado pelo schema Zod da rota (D10). O `z.object` padrão **descarta** chave extra
  (acréscimo no servidor não quebra o painel) e **falha** com chave ausente ou tipo trocado
  (remoção quebra alto, na hora, com o nome do campo).

**A regra do 401** (nota 3 do coordenador; §12 do Plano 5):

| Resposta | O que o wrapper faz |
|---|---|
| `401 unauthorized` numa rota fora de `/auth/*` | Renova (uma vez) e repete a requisição original (uma vez) |
| `401 invalid_credentials` (senha/TOTP errados em `/auth/password`, `/totp/*`) | **Nada.** Erro de formulário; repetir reenviaria a senha errada e contaria outra falha no bloqueio |
| `401 invalid_session` de `/auth/refresh` | Sessão acabou: `sessionEnded('expirada')` |
| `401` de novo depois de renovar | Sessão acabou (não entra em laço) |
| Qualquer `401` em `/auth/*` | Nunca dispara refresh |

**Compare-and-refresh.** Se a requisição que tomou 401 saiu com o token `T1` e o token atual já é
`T2` (outra requisição renovou enquanto esta voava), ela só repete com `T2`, sem renovar. Menos
rotações, e nenhuma corrida nova.

**Refresh — três camadas** (D7):

```ts
function refresh(): Promise<string | null> {
  inflight ??= locks.request('vigilume:refresh', doRefresh)   // Web Locks: entre abas
                    .finally(() => { inflight = null })       // single-flight: dentro da aba
  return inflight
}
```

1. **Dentro da aba, *single-flight*** (spec §11.2, item 3): cinco 401 simultâneos esperam na mesma
   promessa.
2. **Entre abas, Web Locks** (`navigator.locks.request`). Abas do mesmo navegador dividem o cookie
   de refresh (a mesma família) mas não a memória. Sem a trava, duas abas renovando juntas
   apresentam o mesmo refresh; a segunda é reúso e a família cai **nas duas** — reproduzido no
   spike (§8.2), 3 de 3. Com a trava, a segunda aba só envia depois de a primeira receber a
   resposta, e o navegador já trocou o cookie: é uma rotação legítima, não reúso. `locks` é
   injetado (em jsdom não existe; em produção é `navigator.locks`).
3. **Aviso entre abas, `BroadcastChannel('vigilume:sessao')`**, só com eventos, **nunca com
   token**: `encerrada` (logout, sessão atual revogada, refresh recusado) faz as outras abas
   largarem o token e irem ao `/login` na hora — sem isso, a outra aba continua lendo dados por até
   15 min com o access token que tem (D4 do Plano 5). `iniciada` faz uma aba parada no `/login`
   rodar o boot (cortável, corte 4).

*BroadcastChannel para repassar o access token* foi avaliado e **recusado**: tira uma rotação por
aba, mas espalha o token por `postMessage`, cria a corrida "chegou o token da outra aba enquanto eu
renovava", e não resolve nada que a trava não resolva. O custo da trava é uma rotação a mais por
aba a cada 15 min.

`doRefresh` tem `AbortSignal.timeout(10 s)`: uma rede travada não pode segurar a trava de todas as
abas. **Resíduo aceito:** se a resposta de um refresh se perde depois de o servidor rotacionar, o
cookie não troca, o próximo refresh é reúso e a família cai — o usuário faz login de novo. É o
preço da detecção de reúso sem janela de graça (spec §5.2), raro, e seguro.

**Renovação reativa.** O painel renova quando toma 401, não por relógio: com o `refetchInterval`
de 15 s, a primeira consulta depois dos 15 min renova sozinha. Um timer proativo em `expires_in`
economiza um 401 a cada 15 min; não compensa a complexidade (visibilidade da aba, relógio
suspenso).

**O token é opaco** (D6): o painel nunca decodifica o JWT. O `sid` do Plano 5 é assunto do
servidor; `current` chega pronto em `GET /sessions`.

**Onde o token vive:** uma variável de módulo em `auth.ts`, fora do estado React (não vai para
DevTools de estado, não re-renderiza a árvore a cada rotação). Nada em `localStorage`,
`sessionStorage`, IndexedDB ou cookie legível (spec §5.3). O tema é a única coisa persistida
(D20), e não é segredo.

**Login e TOTP** (`POST /auth/login` → `{access_token}` **ou** `{totp_required, challenge}`):

- 401 `invalid_credentials` → "E-mail ou senha incorretos." — **sempre** o mesmo texto, também para
  conta bloqueada (R34 do Plano 1: o bloqueio não se anuncia; a tela não pode "melhorar a UX" e
  reabrir a enumeração).
- 429 → "Muitas tentativas. Aguarde um minuto e tente de novo."
- `totp_required` → passo 2 na mesma tela; o `challenge` fica só no estado do componente. O campo
  aceita **6 dígitos ou código de recuperação** (16 caracteres base32 — `TOTP.Verify` aceita os
  dois no mesmo campo), com um botão "Usar código de recuperação" que troca o rótulo e o
  `inputmode`. 401 `unauthorized` no verify = desafio vencido (5 min) → volta ao passo 1 com "O
  tempo para digitar o código acabou." 401 `invalid_credentials` → "Código incorreto."
- Depois do login: `?next=` só é seguido se for caminho do painel (`/` seguido de algo que não seja
  `/` nem `\`), senão vai para `/`. Sem isso é *open redirect* (teste com sabotagem, §7.5).

**Sair:** `POST /auth/logout` (com o header), `sessionEnded('saiu')`: token descartado,
`queryClient.clear()`, aviso `encerrada` às outras abas, `/login`.

**Encerrar a sessão atual** em `/settings` (`DELETE /sessions/{id}` com `current: true`): o `204`
**já apagou o cookie** (D15 do Plano 5). O painel faz `sessionEnded('encerrada-aqui')` **sem**
chamar `/auth/refresh` nem `/auth/logout`. O access token em memória ainda funcionaria por até
15 min nas rotas de recurso — é o painel que tem de largá-lo.

### 3.5 Estado de servidor (TanStack Query)

Defaults do `QueryClient`:

- `retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2` — 4xx não se repete
  (o 401 já foi tratado no wrapper; 404 e 429 não melhoram repetindo);
- `refetchIntervalInBackground: false` (spec §11.4), `refetchOnWindowFocus: true` (quem volta do
  e-mail de verificação vê o canal verificado);
- `staleTime: 10 s`.

| Chave | Rota | Intervalo |
|---|---|---|
| `['me']` | `GET /me` | — (uma vez por sessão) |
| `['targets','summary']` | `GET /targets/summary` | 15 s (spec §11.4) |
| `['targets','list']` | `GET /targets?limit=100` | — |
| `['targets',id]` | `GET /targets/{id}` | 60 s |
| `['targets',id,'checks',janela]` | `GET /targets/{id}/checks` | 60 s (spec §11.4) |
| `['incidents',filtro]` (infinita) | `GET /incidents?status=&cursor=` | 30 s |
| `['channels']` | `GET /channels` | 60 s |
| `['status-pages']` | `GET /status-pages` | — |
| `['sessions']` | `GET /sessions` | — |

**Orçamento do limite por usuário** (60/min, burst 20; §6 do Plano 5): o dashboard aberto gasta
4/min; detalhe aberto, +2/min. A navegação dispara no máximo 3–4 consultas por tela. O risco real
é o E2E, que navega rápido (R7).

`sessionEnded` faz `queryClient.clear()`: nada da sessão anterior sobrevive no cache para a próxima.

### 3.6 Erros e mensagens

`lib/api/errors.ts` traduz vocabulário fechado, sem ecoar nada do servidor além do `code`:

- **Motivos de campo** (`fields[].reason`): `required` "Obrigatório", `invalid` "Valor inválido",
  `out_of_range` "Fora do intervalo permitido" (a mensagem do campo diz o intervalo),
  `too_long`, `too_short`, `forbidden` (URL: "Endereços internos — localhost, redes privadas — não
  podem ser monitorados"; header: "Este header é controlado pelo monitor"), `unavailable` (canal de
  e-mail: "O envio de e-mail não está configurado neste servidor").
- **Códigos** (`slug_taken`, `status_page_exists`, `channel_limit_reached`, `channel_not_pending`,
  `delivery_failed`, `target_limit_reached` do Plano 5, `rate_limited`…) → mensagem por tela.
- `fields` do 400 vão para `setError` do React Hook Form, no campo certo; campo desconhecido cai
  no erro geral do formulário.
- `internal_error`/`unexpected` mostram o `trace_id` ("Código para suporte: …"), que é o que liga a
  tela ao log (spec §9.1).
- `error_kind` e `last_error` viram texto (timeout → "Tempo esgotado", dns → "Falha de DNS",
  conn_refused → "Conexão recusada", http_status → "Status HTTP inesperado (503)", blocked →
  "Destino bloqueado (endereço interno)", body_limit → "Resposta grande demais"; canais: rejected
  → "O destino recusou", invalid_channel → "Canal inválido (webhook apagado?)" etc.).

### 3.7 Rotas e code splitting

`createBrowserRouter` (modo *data*, sem *framework mode*, sem SSR). Os dados vêm do TanStack
Query, não de `loader`s: uma camada só de dados, com cache e *polling*. `lazy` separa o detalhe
do alvo (Recharts, D4) e as configurações (QR code). Um `ErrorBoundary` na raiz trata a falha de
`import()` dinâmico — o caso real é "houve deploy e o chunk antigo sumiu" (o handler devolve 404,
D12) — com "Há uma versão nova do painel. Recarregar." Sem esse tratamento, a tela fica branca.

Foco em navegação: ao trocar de rota, o foco vai para o `<h1>` da tela (o React Router não faz
isso sozinho) e o título da aba muda ("Alvos — Vigilume").

### 3.8 Ordem de construção

Cada fatia termina com testes verdes, sabotagens registradas e algo que se usa.

| Fatia | Entrega | Prova |
|---|---|---|
| 0 | `web/` + Tailwind + shadcn (nonce propagado) + `lib/api` (wrapper, refresh, locks, aviso entre abas) + `internal/webui` + wiring + harness E2E | Go: `webui` e wiring; Vitest: núcleo de auth; E2E: `/login` carrega sem violação de CSP |
| 1 | Login + TOTP, splash, layout, sair | E2E: login → dashboard vazio; F5 mantém a sessão |
| 2 | Dashboard (summary, StatusBadge, sparkline, `aria-live`) | Vitest: anúncios só em mudança |
| 3 | CRUD de alvos | **E2E do fluxo crítico completo** (§7.4) — daqui para frente o painel é apresentável |
| 4 | Detalhe com gráfico (lazy), janelas, pausar, apagar | Vitest: série raw/1h; E2E: detalhe sem violação de CSP |
| 5 | Incidentes | |
| 6 | Canais | |
| 7 | Status page | |
| 8 | Configurações: senha, 2FA, sessões, tema | Vitest: sessão atual encerrada não renova |
| 9 | Projeto `a11y` do Playwright (todas as rotas, estados mockados, dois temas) + E2E de abas + passada de teclado | axe sem violações; CSP zero em todas as rotas |

## 4. Telas

Todas com `<h1>`, carregando (esqueleto, não *spinner* solto), erro (com "Tentar de novo" e
`trace_id`) e vazio. Datas no fuso do navegador com o fuso indicado ("14:03, horário de
Brasília" via `Intl`), diferentemente da página pública, que é UTC por não ter JavaScript.

### 4.1 `/login`

Formulário e-mail + senha (Zod: e-mail com formato, senha não vazia — **sem** a política de
tamanho, que vale só para senha nova; o login aceita a senha antiga mais curta, R5 do Plano 5).
Passo TOTP como na §3.4. Autenticado → redireciona para `/`. `autocomplete="username"` /
`"current-password"` / `"one-time-code"`.

### 4.2 `/` — Dashboard

`GET /targets/summary`, 15 s. Grade de cartões na ordem da API. Cada cartão: nome (link para o
detalhe), `StatusBadge`, última latência ("123 ms", ou "—" quando `null`, inclusive quando o último
foi `down`, D23 do Plano 5), uptime 24 h **truncado** em duas casas ("99,99%", nunca "100,00%" com
falha — mesma regra de `porcento` do Plano 4), "verificado há 2 min", sparkline das 24 h e, se
houver, link "incidente aberto".

- **Pausado:** `enabled: false` mostra "⏸ Pausado", não "Sem dados" (o servidor diz `unknown` nos
  dois casos; o painel distingue pelo `enabled`).
- **Sparkline:** SVG próprio (~40 linhas), `p50_ms` por hora, com a fração `ok/total` como cor da
  barra; hora ausente é lacuna, **não zero** (nota do Plano 5). Decorativo (`aria-hidden`); a
  informação está no texto do cartão. Recharts não entra no dashboard (D4).
- **Resumo no topo:** "3 no ar, 1 fora, 1 pausado" — também é o texto que o leitor de tela ouve.
- **Anúncio:** a região `aria-live` do layout recebe "API: fora do ar" / "API: voltou ao ar" quando o
  estado de um alvo **muda** entre duas respostas. Nada na primeira carga, nada em poll sem mudança.
- **Vazio:** "Nenhum alvo ainda" + "Cadastrar o primeiro alvo".

### 4.3 `/targets/new` e `/targets/:id/edit`

React Hook Form + Zod espelhando `targets/validate.go` (o servidor é a fonte da verdade; o espelho
só antecipa o erro):

| Campo | Controle | Regra no cliente |
|---|---|---|
| Nome | texto | 1–100 *code points*, sem caractere de controle |
| URL | `type=url` | `http`/`https`, ≤ 2048 bytes; IP literal privado e `localhost` recusados já no cliente |
| Método | GET / HEAD / POST | |
| Intervalo | 30 s, 1, 5, 15 min, 1 h + "outro" (s) | 30–86400 |
| Timeout | ms | 1000–30000 **e** `timeout < 0,9 × intervalo` (D3 do Plano 2: o jitter de −10%) |
| Status esperado | número | 100–599, padrão 200 |
| Limiar de lentidão | Padrão (2000 ms) / Personalizado / Desativado | ausente / número 1–60000 / `null` (spec §11.5; `optional` do Go distingue os três) |
| Ativo | switch | |
| Headers | lista nome/valor | ≤ 20, nomes proibidos (Host, Content-Length…), valor ≤ 1 KiB, total ≤ 8 KiB |

Padrões do formulário = constantes do servidor (60 s, 10000 ms, 200, 2000 ms). O painel **sempre**
envia os números explicitamente (evita o "0 vira padrão em POST e 400 em PATCH" das dívidas).

**Headers na edição (D24):** o servidor nunca devolve valores, só `header_names`. A edição mostra
"Headers definidos: Authorization, X-Chave (valores ocultos)" e duas ações: **Substituir todos**
(abre a lista vazia; o PATCH leva o mapa inteiro) e **Remover todos** (`headers: null`). Sem uma das
duas, o PATCH **não leva a chave `headers`** — levar `{}` apagaria os headers em silêncio. O PATCH
só leva os campos alterados (`dirtyFields`), porque o servidor recusa campo desconhecido
(`decodeJSONStrict`) e não há por que reenviar o resto.

Texto de ajuda (pendência I3 do Plano 2): "Headers não acompanham redirecionamento para outro
host ou porta, nem de https para http."

Erros: `fields` → campo; `409 target_limit_reached` (Plano 5, D24) → "Limite de 100 alvos
atingido."; `url`/`forbidden` com a mensagem da §3.6. Sucesso → detalhe do alvo, invalida
`summary` e `list`.

### 4.4 `/targets/:id` — Detalhe

- **Cabeçalho:** nome, URL, método, intervalo; estado atual lido do cache do summary (sem chamada
  extra); ações Editar, Pausar/Retomar (`PATCH enabled`), Apagar (diálogo de confirmação com o nome
  do alvo; `204` → `/`).
- **Janela:** 1 h, 6 h, 24 h (padrão), 7 d, 30 d, 90 d, na URL (`?janela=24h`). O painel manda
  só `from` (o `to` fica o "agora" do servidor, sem depender do relógio do navegador) e renderiza
  pelo campo `resolution` — **nunca** pela janela que pediu: é o servidor que decide o corte de
  48 h (spec §9.1). O schema é uma união discriminada por `resolution`.
- **Gráfico (Recharts, lazy):** `raw` → latência dos checks não-`down` como linha, checks `down` como
  marcas na base; `1h` → `p50_ms` como linha e `p95_ms` como faixa, uptime da hora (`ok/total`) em
  barras. Rótulo "Resolução: por check" / "por hora — pode atrasar até ~1h15" (limite conhecido do
  rollup, Plano 4).
- **Acessível:** `accessibilityLayer` (padrão no Recharts 3) + um resumo em texto acima ("Mediana
  123 ms, p95 480 ms, 99,93% no ar nesta janela") + "Ver dados em tabela" (disclosure) com as
  mesmas linhas. O gráfico nunca é o único portador da informação.
- **Timeline:** os últimos checks (raw) ou horas (1h) em tabela: instante, estado com ícone,
  latência, status HTTP, `error_kind` em texto.
- **Incidentes do alvo:** a API não filtra `GET /incidents` por alvo. O detalhe mostra o incidente
  aberto (via `open_incident_id` do summary) e o link "Todos os incidentes". Filtrar no cliente uma
  lista paginada por cursor mentiria. O filtro `target_id` é o acréscimo pedido na §11, Div. 3.
- **Volume:** 48 h a 30 s são 5.760 checks numa resposta. O plano mede o Recharts com esse volume;
  se passar de ~100 ms por render, entra downsampling (LTTB) no cliente (corte 5) ou `uPlot`
  (spec §11.1).

### 4.5 `/incidents`

Filtro Abertos / Resolvidos / Todos (na URL), `useInfiniteQuery` com `next_cursor` e botão
"Carregar mais" (sem rolagem infinita: previsível no teclado e no leitor de tela). Linha: alvo
(link), causa em texto, início, duração ("em andamento há 12 min" ou "durou 1 h 4 min"), estado de
reconhecimento. **Reconhecer** (`POST /incidents/{id}/ack`, idempotente) em aberto não reconhecido;
o texto não promete o que o servidor não faz ("Marca que você está ciente").

### 4.6 `/channels`

Lista: tipo (ícone + "Discord"/"E-mail"), `hint`, estado — `verified` "✓ Verificado", `pending`
"… Aguardando verificação", `expired` "Verificação expirada", **`failing` "✕ Falhando"** com
`last_error` em texto e "desde 14:03" (`last_failure_at`). `last_error` também aparece, discreto,
num canal verificado que já falhou antes ("última falha: …"): o status diz o presente, esses
campos o passado (comentário do `channelResponse`).

- **Criar** (diálogo): Discord (URL; ajuda: "Webhook de canal de fórum não é aceito"; sucesso =
  mensagem de teste enviada, canal já verificado) ou e-mail (sucesso = "Enviamos um link para …,
  válido por 24 horas"). `502 delivery_failed` → "Não conseguimos entregar a mensagem de teste;
  confira o endereço." `409 channel_limit_reached`; `400 kind/unavailable` → e-mail não configurado;
  `429` → "Limite de envios: tente de novo em alguns minutos" (1 a cada 6 min, burst 5).
- **Reenviar verificação** em `pending`/`expired`; `409 channel_not_pending` → recarrega a lista.
- **Apagar** com confirmação.

### 4.7 `/status-pages`

`GET /status-pages` devolve zero ou um item. Sem página: formulário de criação. Com página:
edição.

- Título; slug com sugestão a partir do título e a regra `^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`
  explicada em texto; publicada (switch).
- Itens: escolher alvos de `GET /targets?limit=100` (o teto de 100 do Plano 5 cabe numa página),
  nome de exibição (padrão: nome do alvo), ordem com botões **Subir/Descer** (teclado e leitor de
  tela; arrastar-e-soltar fica de fora), máximo 20. O PATCH troca a lista inteira.
- `409 slug_taken` → erro no campo; `409 status_page_exists` (corrida entre abas) → recarrega.
- **Link público** (`public_url`) com Copiar e Abrir (`target=_blank rel="noopener noreferrer"`).
  Em rascunho: "A página só fica acessível depois de publicada" (o servidor responde 404 igual para
  rascunho e inexistente).
- Ajuda: "Mudanças aparecem na página pública em até 30 segundos."
- **Prévia (D23, cortável):** lista "como vai aparecer" — nomes de exibição e o estado atual de
  cada alvo, lidos do summary — rotulada "Prévia aproximada". A prévia fiel é inviável (§5.7).

### 4.8 `/settings`

- **Conta:** e-mail e "desde" (`GET /me`).
- **Senha:** atual, nova, confirmação e, **só com `totp_enabled`**, código (6 dígitos ou
  recuperação). Política no cliente: 15 a 128 **code points** — `[...senha].length`, não
  `senha.length` (que conta unidades UTF-16; nota do Plano 5). `204` → "Senha alterada. As outras
  sessões foram encerradas; um acesso já aberto em outro aparelho pode durar até 15 minutos." e
  invalida `['sessions']`. `401 invalid_credentials` → "Senha atual ou código incorretos" (não há
  como separar, e não se deve). `400 new_password` → no campo.
- **2FA desligado → Ativar:** senha (a rota passa pelo limitador do login, D12 do Plano 5) →
  `otpauth_uri` → QR (`QRCodeSVG`) + a chave em texto para digitar → código → `recovery_codes`
  exibidos **uma vez**, com Copiar e Baixar `.txt` (Blob) e a caixa "Guardei os códigos" antes de
  fechar. A URI e os códigos vivem só no estado do diálogo e somem ao fechar.
- **2FA ligado → Desativar:** senha + código ou código de recuperação.
- **Sessões:** `GET /sessions`: "Firefox em Linux" (parse simples do `user_agent`, que é texto de
  terceiros e é renderizado como texto, nunca HTML), criada em, "último acesso" (granularidade de
  ~15 min: o texto diz "por volta de"), selo "Esta sessão". **Encerrar** com o texto do Plano 5:
  "Encerrar impede a renovação. Um acesso já aberto no outro aparelho pode continuar por até 15
  minutos." A atual pede confirmação ("Você vai sair deste navegador") e segue a §3.4. `404` →
  "Esta sessão já tinha terminado", recarrega; não é erro.
- **Tema:** Sistema / Claro / Escuro.

### 4.9 Layout, 404 e erros

Navegação (Painel, Incidentes com contador de abertos, Canais, Status page, Configurações), e-mail
do usuário, tema, Sair. "Pular para o conteúdo" como primeiro foco. Rota desconhecida → "Página não
encontrada" dentro do layout.

## 5. Servir o painel pelo binário

### 5.1 Montagem

Hoje a raiz manda **tudo** que não é `/verify/` nem `/p/` para a cadeia da API. Passa a ser:

```go
raiz.Handle("/api/", api)        // a cadeia de hoje, inteira
raiz.Handle("/healthz", api)
raiz.Handle("/readyz", api)
raiz.Handle("/verify/", verificacao(ctx, cfg, d.Channels))
raiz.Handle("/p/", paginaPublica(ctx, cfg, d.StatusPages))
raiz.Handle("/", painel(d.Painel))   // RequestID → Recover → SecurityHeaders → webui
```

A separação entre painel e API é **estrutural** (o `ServeMux` escolhe o padrão mais específico): o
handler do painel nunca vê `/api/…`, `/p/…` nem `/verify/…`. `Deps` ganha `Painel fs.FS`
(nil → `webui.Dist()`), para os testes injetarem um `fstest.MapFS`.

A cadeia do painel não tem CORS, `RequireCustomHeader`, `BodyLimit` nem rate limit: só serve
GET/HEAD de arquivos em memória (D18). Os timeouts do `http.Server` continuam valendo.

### 5.2 O handler (`internal/webui`)

```go
//go:embed all:dist
var dist embed.FS

func Dist() fs.FS                 // fs.Sub(dist, "dist")
func New(fsys fs.FS) http.Handler // lê e valida index.html uma vez; pré-comprime (D17)
```

Decisão por requisição (`p := path.Clean(r.URL.Path)`):

| Caso | Resposta | `Cache-Control` |
|---|---|---|
| Método ≠ GET/HEAD | 405, `Allow: GET, HEAD` | `no-store` |
| Algum segmento começa com `.` (`/.gitkeep`) | 404 | `no-store` |
| `/index.html` | o mesmo que `/` (index com nonce) — nunca o arquivo cru com o marcador | `no-store` |
| `/assets/…` existente | o arquivo | `public, max-age=31536000, immutable` |
| `/assets/…` inexistente | **404**, nunca o index | `no-store` |
| Outro caminho **com extensão** existente na raiz do dist (`/favicon.svg`, `/tema.js`) | o arquivo | `no-cache` |
| Outro caminho com extensão inexistente | 404 | `no-store` |
| Caminho **sem extensão** (`/`, `/targets/abc`, `/qualquer`) | `index.html` com nonce novo (fallback de SPA) | `no-store` |

- **Por que 404 e não o index para asset ausente:** depois de um deploy, uma aba antiga pede
  `index-OLD.js`. Com fallback, receberia HTML com `Content-Type: text/html`, o `nosniff` barraria o
  módulo e a tela ficaria branca sem pista. Com 404, o `import()` falha e o `ErrorBoundary` oferece
  recarregar (§3.7).
- **Fallback por "sem extensão", não por lista de rotas** (D12): a lista duplicaria a tabela do
  React Router em Go e dessincronizaria. O custo — um caminho inexistente responde 200 com o
  index e o React Router mostra o 404 — é irrelevante num painel autenticado sem SEO.
- **`index.html` com `no-store`**, não `no-cache`: ele carrega o nonce desta resposta. Num cache
  compartilhado, todos receberiam o mesmo nonce, e ele deixaria de ser segredo por resposta.
- **MIME por tabela própria** (D14), não `mime.TypeByExtension`: `.woff2`, `.webmanifest` e `.map`
  não estão na tabela embutida do Go (§8.3); nesta máquina `.woff2` funciona porque
  `/etc/mime.types` existe, e na imagem distroless do Plano 7 não existe. Extensão fora da tabela →
  `application/octet-stream` (com `nosniff`, nunca executa).
- **HEAD** sem corpo; `Content-Length` sempre. Sem `ETag` (asset é imutável; index é `no-store`).
- **Sem source maps** no dist (`build.sourcemap: false`): tamanho do binário.

### 5.3 CSP do painel e o nonce

Por requisição ao index: `n := crypto.NewCSPNonce()` (128 bits, base64 **URL** sem padding — a
mesma função da página pública, movida para um pacote folha, D27), e:

```
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self' 'nonce-{n}';
  img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self';
  base-uri 'none'; form-action 'none'; frame-ancestors 'none'
```

e o corpo é o `index.html` com o marcador `VIGILUME_CSP_NONCE` trocado por `n` (`bytes.ReplaceAll`;
base64 URL não precisa de escape em atributo). O marcador é o valor de `html.cspNonce` no
`vite.config.ts`: o Vite põe `nonce="…"` nos `<script>`/`<link>` do index e acrescenta
`<meta property="csp-nonce" nonce="…">`, de onde o bundle lê o nonce (`meta.nonce` — o navegador
esconde o atributo de `getAttribute`, a propriedade não; comprovado no spike).

- **`script-src 'self'` sem nonce:** não há script inline (o tema é `/tema.js`, D20; o Vite não
  gera inline no build). Nonce em script só abriria uma porta a mais.
- **`style-src` com nonce, sem `style-src-attr`:** o spike não registrou **nenhuma** violação de
  atributo `style=` — React, Radix (floating-ui) e Recharts escrevem estilo pelo CSSOM
  (`element.style`), que a CSP não governa. Só `<style>` injetado quebra.
- **`img-src 'self'` sem `data:`:** o QR é `<svg>` React; ícones são SVG inline.
- **`form-action 'none'`:** os formulários são `onSubmit` + `preventDefault`; se o JS falhar, um
  submit nativo é barrado em vez de mandar a senha na query string.
- Os demais cabeçalhos vêm de `middleware.SecurityHeaders` (HSTS, `nosniff`, `no-referrer`,
  COOP, `X-Frame-Options`); o handler sobrescreve só CSP e `Cache-Control`, como a página pública.

**Index sem marcador** (build feito sem `html.cspNonce`): `New` registra `slog.Error` e o painel
responde **503** ("painel com build inválido"); a API segue. Servir o index sem nonce seria servir
um painel cujo gráfico some em silêncio (§8.1). O E2E falha na hora.

### 5.4 `dist/` antes do primeiro build

- `internal/webui/dist/.gitkeep` é o único arquivo versionado. `//go:embed all:dist` compila com
  ele sozinho; **sem** `all:` o compilador recusa ("contains no embeddable files", §8.3). O `all:`
  também é o que inclui chunks cujo nome começa com `_`, que o Rolldown pode gerar e que o `embed`
  sem `all:` descartaria **em silêncio**.
- `go test ./...` e `go build` funcionam sem Node. Sem `index.html`, o painel responde **503**
  "O painel não foi incluído neste binário (rode `npm run build` em web/)" e o boot registra um
  aviso; a API funciona normalmente.
- **`emptyOutDir: true`** no Vite (senão, fora da raiz do projeto, o Vite não limpa e os bundles
  antigos se acumulam e vão para o binário) **apaga o `.gitkeep`** (§8.3). Um plugin de 5 linhas
  (`closeBundle`) o recria. Verificação: depois de `npm run build`, `git status --porcelain
  internal/webui/dist` tem de sair vazio (passo de CI do Plano 7).

### 5.5 gzip (D17, cortável)

O bundle do spike tem 790 KB (240 KB em gzip). O handler comprime **no boot**, em memória, os
arquivos de texto acima de 1 KB, e serve a versão gzip quando `Accept-Encoding` a aceita
(`Content-Encoding: gzip`, `Vary: Accept-Encoding`). Uma vez por processo, alguns MB de RAM, zero
CPU por requisição. Alternativa: pré-comprimir no build (`.gz` no dist, plugin do Vite) — mais uma
dependência de build e o dobro de arquivos embutidos. O gzip das respostas da **API** (o summary de
~180 KB a cada 15 s, R8 do Plano 5) não é deste handler: fica com o Plano 7.

### 5.6 Desenvolvimento local

`vite` com `server.proxy['/api'] = 'http://localhost:8080'` (contrato, item 3). O navegador só vê
`localhost:5173`: o cookie `SameSite=Strict; Path=/api/v1/auth` funciona sem CORS e sem `Secure`
(o `PUBLIC_BASE_URL` local é http). O dev server não aplica a CSP; ela é provada no build, pelo E2E
(§7.4). Em dev o marcador fica literal e é usado como nonce sem efeito.

### 5.7 Por que não há prévia fiel da status page

Rascunho responde 404 em `/p/{slug}` (Plano 4), `/p/` tem `frame-ancestors 'none'` (não entra em
iframe) e a autenticação do painel é Bearer em memória (uma aba nova não a leva). Uma prévia real
pediria rota nova autenticada que devolvesse HTML — escopo de API, e não está no contrato. Daí a
prévia aproximada (D23) ou nenhuma.

## 6. Acessibilidade

| Requisito (spec §11.6) | Como | Como se verifica |
|---|---|---|
| Estado com ícone **e** texto | `StatusBadge`: ● No ar, ▲ Degradado, ✕ Fora do ar, ○ Sem dados, ⏸ Pausado; ícone `aria-hidden`, texto sempre visível; formas diferentes, não só cores | Vitest: cada estado tem texto e ícone; axe |
| Mudança anunciada | `LiveAnnouncer` (`role="status"`, `aria-live="polite"`) no layout; o dashboard anuncia só **transições** | Vitest com sabotagem (§7.5) |
| Teclado completo, foco visível | Primitivos Radix; ordenação por botões, não arrastar; foco no `<h1>` ao navegar; *skip link*; `motion-reduce` | E2E: login inteiro por teclado; passada manual registrada no relatório do plano |
| Contraste AA nos dois temas | Tokens de estado próprios para `:root` e `.dark` | Projeto `a11y` do Playwright: axe (`wcag2a`, `wcag2aa`) em **todas** as rotas, com a API mockada mostrando os cinco estados, claro e escuro |
| Tema escuro | `prefers-color-scheme` + override (Sistema/Claro/Escuro) persistido | Idem (os dois temas) |
| Gráfico acessível | Resumo em texto + tabela alternativa + `accessibilityLayer` | Vitest do resumo; axe |
| Formulários | `<label>` sempre, erro ligado por `aria-describedby`, `aria-invalid`, foco no primeiro inválido (`shouldFocusError`) | Testing Library consulta por papel/rótulo: sem rótulo, o teste nem acha o campo |

`<html lang="pt-BR">`. O oxlint roda as regras `jsx-a11y`. O axe roda **sob a CSP de produção**,
sem `bypassCSP` — o spike confirmou que funciona e já apontou dois problemas reais no markup de
teste (§8.4).

**"Degradado" no painel, "Lento" na página pública** (D22): o painel fala a língua do operador
(o campo é `degraded_threshold_ms`); a página pública, a do visitante.

## 7. Testes

### 7.1 Camadas

| Camada | Ferramenta | O que cobre |
|---|---|---|
| Handler do painel | `go test` com `fstest.MapFS` | Tabela da §5.2, CSP/nonce, MIME, 503 sem build |
| Wiring | `go test` pelo `Routes()` inteiro | `/api/`, `/p/`, `/verify/`, `/healthz` não caem no painel; o painel cai no painel |
| Núcleo do cliente | Vitest + MSW | Wrapper, regra do 401, single-flight, abas, boot, sessão encerrada |
| Componentes e telas | Vitest + Testing Library + MSW | Formulários (espelho do servidor, `fields` → campo), estados, anúncios, formatação |
| Fluxo crítico | Playwright, binário real + Postgres | Login → cadastrar alvo → ver status; F5; CSP; teclado |
| Várias abas | Playwright, binário real, latência via CDP | Web Locks no navegador de verdade |
| Acessibilidade + CSP por rota | Playwright com API mockada (`page.route`) | axe e zero violações de CSP em todas as rotas, dois temas |

**Handlers do MSW imitam as guardas do servidor**, senão os testes de cliente ficam vacuamente
verdes: sem `X-Requested-With` numa mutação → `403 forbidden`; sem `Bearer` válido → `401
unauthorized`; `/auth/refresh` com rotação e **detecção de reúso** (cookie consumido → família
revogada). Fixtures tipadas com `satisfies z.input<typeof Schema>`: uma fixture fora do contrato
não compila. Enquanto o corte 3 não for feito, as fixtures vêm dos **goldens** (D11): um teste de integração
Go chama cada rota do painel e grava a *forma* da resposta (chaves e tipos, com ids e datas
normalizados) em `internal/httpapi/testdata/painel/*.json`; o Vitest parseia cada golden com o
schema Zod correspondente. A deriva Go × TS vira teste vermelho dos dois lados.

`setupServer(...).listen({ onUnhandledRequest: 'error' })`: rota sem handler é falha, não aviso.
Polyfills do jsdom no `setup.ts`: `ResizeObserver`, `hasPointerCapture`, `scrollIntoView`
(Radix). O Recharts não desenha em jsdom (largura 0): a lógica do gráfico (série raw/1h, resumo,
marcas de down) é função pura testada à parte; o desenho é provado no E2E.

### 7.2 Autenticação no cliente (Vitest)

- **Single-flight:** cinco requisições com token vencido → **um** refresh; as cinco repetem com o
  token novo; família viva. (Protótipo verde no spike, §8.2.)
- **Várias abas, determinístico:** dois clientes (duas "abas") dividindo o mesmo servidor MSW (o
  mesmo "cookie") e uma trava falsa com a semântica de `navigator.locks` (FIFO); o handler de
  refresh tem `delay(20)`, o que garante a sobreposição. Espera: duas rotações em série, família
  viva.
- **Regra do 401:** `POST /auth/password` com `401 invalid_credentials` → zero chamadas a
  `/auth/refresh`, uma a `/auth/password`. `401 unauthorized` em `/sessions` → um refresh, uma
  repetição; refresh recusado → `sessionEnded`.
- **Boot sob `StrictMode`:** renderizar a árvore em `StrictMode` → exatamente um refresh.
- **Sessão atual encerrada:** `DELETE /sessions/{atual}` `204` → token descartado, cache limpo,
  `/login`, **zero** refreshes depois.
- **Aviso entre abas:** `sessionEnded` numa instância → a outra instância larga o token (canal
  injetado; o Node tem `BroadcastChannel` global).
- **`next`:** `//evil.example`, `/\evil`, `https://…` → `/`; `/targets/x` → `/targets/x`.

### 7.3 Go (`internal/webui`, wiring)

Tabela da §5.2 inteira, mais: nonce do cabeçalho = nonce do `<meta>` = nonce dos `<script>`/`<link>`;
nenhuma ocorrência do marcador no corpo; **duas requisições, dois nonces**; o nonce casa com a
gramática da CSP **em 1.000 sorteios** (lição do Plano 4: teste que depende de sorteio passou por
sorte uma vez); `/assets/x.js` → `text/javascript; charset=utf-8`; `.woff2` → `font/woff2`; extensão
desconhecida → `application/octet-stream`; sem `index.html` → 503 nas rotas de SPA **e** 404 (não
503) nos assets; index sem marcador → 503. Wiring pelo `Routes()` com `Deps.Painel` injetado:
`GET /api/v1/nao-existe` não é HTML; `GET /p/slug-inexistente` é a página 404 da status page (não o
SPA); `GET /verify/x` é a página de verificação; `GET /healthz` é `200` vazio; `GET /targets/abc` é
o SPA com a CSP do painel.

### 7.4 E2E (Playwright, binário real)

**Harness** (`web/e2e/global-setup.ts`; o `webServer` do Playwright não serve, porque sobe
**antes** do `globalSetup` e o binário precisa do banco antes):

1. `npm run build` já feito; `go build -o e2e/.tmp/vigilume ./cmd/vigilume` (dist embutido).
2. `docker run -d --rm -P postgres:18-alpine` (a mesma imagem do `testdb`; spec §3.3: 18 em desenvolvimento e testes) com `init.sql` em `docker-entrypoint-initdb.d`
   criando o **papel comum** (`NOSUPERUSER NOBYPASSRLS`, dono do banco) — o binário recusa
   superusuário (`CheckRole`, D8 do Plano 2). Espera `pg_isready`.
3. Sobe o binário com `DATABASE_URL` (papel comum), `HTTP_ADDR=127.0.0.1:<porta livre>`,
   `PUBLIC_BASE_URL` e `CORS_ORIGIN` = essa origem, chaves aleatórias em base64,
   `METRICS_ADDR=off`, `ROLE=all`; espera `/readyz` 200 (as migrations rodam no boot).
4. `vigilume user create --email e2e@vigilume.test` com a senha (≥ 15 caracteres) **por pipe no
   stdin** (contrato, item 1; D16 do Plano 5).
5. Exporta `E2E_BASE_URL` (variáveis do `globalSetup` chegam aos workers); o `globalTeardown`
   derruba processo e container.

**Listener de CSP do lado do teste** (`page.addInitScript` registra `securitypolicyviolation`
num array) — o app de produção não carrega nada disso. **Controle positivo:** cada arquivo de
teste injeta de propósito um `<style>` sem nonce e exige **exatamente uma** violação registrada;
sem o controle, "zero violações" seria verde também com o listener quebrado.

**`fluxo.spec.ts`** — o fluxo crítico da spec §14:

1. `/` sem sessão → splash → `/login` (e nunca o dashboard piscando).
2. Login **só com teclado** (Tab, digitação, Enter).
3. Dashboard vazio → "Cadastrar alvo" → nome "E2E", URL **`https://vigilume-e2e.invalid/`**
   (RFC 2606: nunca resolve; passa pela validação de cadastro, que não resolve DNS, e o sondador
   classifica sem depender de rede externa).
4. Dashboard mostra o alvo em "○ Sem dados" e, em até 30 s (primeira sonda em ~1 s pelo
   `next_run_at = agora` + poll de 15 s), um estado classificado. A asserção aceita qualquer um dos
   três (um DNS que sequestra NXDOMAIN poderia devolver página 200); no CI do GitHub é "✕ Fora do
   ar".
5. F5 → continua autenticado (boot com o cookie).
6. Detalhe do alvo abre (chunk lazy do Recharts carrega) e o gráfico tem a linha com `stroke`
   computado diferente de `none` — **presença** do efeito visível, não só ausência de violação.
7. Em todas as páginas: zero violações de CSP; `localStorage`/`sessionStorage` sem nada parecido
   com o token; `document.cookie` sem `vigilume_refresh`.
8. Sair → `/login`; F5 → continua no `/login`.

**`abas.spec.ts`** (corte 7): duas páginas do **mesmo contexto** (dividem cookies, não memória —
dois contextos não serviriam). Latência de 300 ms nas duas via CDP
(`Network.emulateNetworkConditions`), recarga simultânea (boot = refresh nas duas) → as duas
autenticadas; no banco, nenhuma auditoria `session.reuse_detected`. O spike mostrou o que **não**
funciona para forçar a corrida: segurar a requisição com `page.route` (o cookie só é anexado no
`continue`, então a corrida some — §8.2).

**`a11y.spec.ts`**: API mockada com `page.route` (mesmas fixtures do MSW), os cinco estados no
dashboard, cada rota do painel visitada em claro e escuro: axe `wcag2a`+`wcag2aa` sem violações e
zero violações de CSP. É o que dá cobertura de CSP a **todas** as telas, não só às do fluxo.

**Flakiness:** o limite por usuário (burst 20) pode morder um E2E rápido (R7); o harness usa um
usuário por arquivo de teste se precisar. Dependências externas: nenhuma além de Docker.

### 7.5 Asserções de ausência e as sabotagens previstas

Toda asserção cujo resultado esperado é "não acontece" tem a sabotagem que a deixa vermelha. O
implementador quebra, vê vermelho, restaura e registra no relatório da tarefa.

| Teste | Afirma | Sabotagem que tem de deixá-lo vermelho |
|---|---|---|
| Single-flight | 1 refresh, família viva | Tirar o `inflight ??=` → 5 refreshes, família revogada (**feito no spike**) |
| Abas (Vitest) | 2 rotações em série, família viva | Trava falsa vira no-op → família revogada |
| Abas (E2E) | As duas abas autenticadas | Trocar `locks.request(...)` por chamada direta → uma aba no `/login` (**feito no spike, 3/3**) |
| Regra do 401 | Zero refresh em `invalid_credentials` | Renovar em qualquer 401 |
| `X-Requested-With` | Mutações passam | Tirar o header do wrapper → 403 do handler MSW (e do servidor real, no E2E) |
| Boot sob StrictMode | 1 refresh | Boot dentro de `useEffect` sem guarda → 2 |
| Sessão atual encerrada | Zero refresh depois do 204 | Chamar `refresh()` depois do DELETE |
| Aviso entre abas | A outra aba larga o token | Não postar no canal |
| Anúncio `aria-live` | Nada em poll sem mudança | Anunciar a cada resposta |
| PATCH sem headers | Corpo **sem** a chave `headers` | Sempre mandar `headers: {}` |
| `next` seguro | `//evil` → `/` | Aceitar qualquer `next` |
| Token fora do storage (E2E) | Nada no `localStorage`/`sessionStorage` | Guardar o token em `sessionStorage` |
| Cookie ilegível (E2E) | `document.cookie` sem o refresh | — **Duas guardas:** isso é `HttpOnly` do servidor; o teste prova a propriedade, não o painel (comentar no teste) |
| Zero violações de CSP (E2E, `a11y`) | Nenhuma violação | Tirar o `nonce` do `ChartStyle` → violação e linha invisível (**feito no spike**); o controle positivo garante que o listener funciona |
| `/api/…` não cai no SPA | Não é HTML | Tirar `raiz.Handle("/api/", …)` |
| Asset ausente é 404 | Não é o index | Fallback para todo caminho |
| `/index.html` com nonce | Marcador ausente do corpo | Servir `/index.html` como arquivo |
| Nonce por resposta | Dois nonces diferentes | Sortear o nonce uma vez no `New` |
| `.gitkeep` não servido | 404 | Tirar a regra do segmento com `.` |
| MIME de `.woff2` | `font/woff2` | Voltar para `mime.TypeByExtension` — **fica verde nesta máquina** (`/etc/mime.types` existe). O teste prova a propriedade no ambiente, não a guarda; a guarda é provada por um teste que confere que **todo** arquivo de um dist real tem entrada na tabela (roda depois do build, no CI do Plano 7) |

## 8. Resultado dos spikes

Tudo **descartável**, fora do repositório, em `/tmp/claude-1000/plano6-spike/`: `painel/` (Vite +
shadcn + Recharts + testes), `srv/` (servidor Go com quatro CSPs), `multiaba/` (servidor que imita a
rotação com detecção de reúso), `embedspike/` e `fakerepo/`. Nada disso vira código do plano; o
plano reescreve do zero.

### 8.1 CSP × shadcn/Radix/Recharts

App com os componentes que o painel usa (Dialog, Select, Tooltip, DropdownMenu, Switch, ScrollArea,
Chart do shadcn com Recharts, um `LineChart` do Recharts com cor literal, toast), build de
produção, servido pelo Go com CSPs diferentes; o Playwright mede o **efeito visível** de cada
injeção e coleta `securitypolicyviolation`.

**O que injeta `<style>` em runtime** (leitura do código e medição):

| Origem | Mecanismo | Aceita nonce? |
|---|---|---|
| `ChartStyle` do shadcn (`chart.tsx`) | `<style dangerouslySetInnerHTML>` com as cores do `ChartConfig` | Não de fábrica; o wrapper é nosso, recebe `nonce` |
| Radix `Select.Viewport`, `ScrollArea.Viewport` | `<style>` para esconder a barra de rolagem | Sim, prop `nonce` |
| `react-remove-scroll` (Dialog, Sheet, Dropdown, Select) | `react-style-singleton` → `createElement('style')`, com a largura da barra | Sim, via `get-nonce` (`setNonce`) |
| `sonner` | `__insertCSS` na carga do módulo | **Não** (zero ocorrências de "nonce") |
| `next-themes` | `<script>` inline (inerte quando o React o cria) e `<style>` ao trocar tema | Parcial |
| Recharts 3 (tooltip, medição de texto) | só CSSOM (`Object.assign(span.style, …)`) | Não precisa |
| Tailwind 4 | CSS estático no build | Não precisa |

**Medições** (Chromium do Playwright 1.63):

| Medida | Controle `unsafe-inline` | Estrita, sem nonce | Nonce só no `get-nonce` | + `style-src-attr 'unsafe-inline'` | **Final:** nonce propagado, sem sonner | Final sob estrita (sabotagem) |
|---|---|---|---|---|---|---|
| Linha do Chart do shadcn (`stroke`) | cor | **`none` (some)** | `none` | `none` | cor | `none` |
| Linha do Recharts com cor literal | cor | cor | cor | cor | cor | cor |
| Dialog trava a rolagem do `body` | sim | **não** | sim | sim | sim | não |
| Barra do Select/ScrollArea escondida | sim | não | não | não | sim | não |
| Tooltip Radix posicionado | sim | sim | sim | sim | sim | sim |
| Tooltip do Recharts | visível | visível | visível | visível | visível | visível |
| Toast | sonner, fixo | **sonner sem estilo, largura total no rodapé** | idem | idem | Radix Toast, fixo | Radix Toast, fixo |
| Violações `style-src-elem` | 0 | 7 | 5 | 5 | **0** | 5 |
| Violações de atributo `style=` | 0 | **0** | 0 | 0 | 0 | 0 |

**Conclusão (D1–D3):** a CSP estrita quebra o painel de forma visível — o gráfico de latência
**some** — e a causa não é o Recharts, é o `ChartStyle` do shadcn. `style-src-attr 'unsafe-inline'`
não muda nada (não há violação de atributo). Hashes não servem: o CSS do `react-remove-scroll` traz
a largura da barra de rolagem da máquina, e o do `ChartStyle` muda com a configuração. Trocar de
biblioteca seria sair do Radix (Select, ScrollArea e Dialog injetam). **Nonce por requisição,
propagado a quatro pontos** (`setNonce`, `Select.Viewport`, `ScrollArea.Viewport`, `ChartStyle`), e
**Radix Toast no lugar do sonner**: zero violações, todos os efeitos visíveis de volta. A última
coluna é a sabotagem: o mesmo build sob a CSP sem nonce volta a quebrar, o que prova que a medição
enxerga o problema.

**Trusted Types** (`require-trusted-types-for 'script'`) no build final: a página não renderiza
(`innerHTML` bloqueado — Radix e `ChartStyle` usam `dangerouslySetInnerHTML`). Fora deste plano.

### 8.2 Refresh concorrente

- **Single-flight (Vitest 5 + MSW 2.15 + jsdom 30):** protótipo do wrapper com servidor MSW que
  rotaciona e detecta reúso. Cinco 401 simultâneos → 1 refresh, família viva. Sabotagem (sem
  *single-flight*) → 5 refreshes, família revogada, requisições falham. Os dois testes verdes; o
  stack de testes funciona com as versões atuais.
- **Várias abas, navegador real:** servidor Go que imita `Rotate` (cookie `HttpOnly`,
  `SameSite=Strict`, `Path=/api/v1/auth`, reúso revoga a família); duas páginas no mesmo contexto
  do Chromium chamando refresh quase juntas:

| Como a sobreposição foi forçada | Sem Web Locks | Com Web Locks |
|---|---|---|
| `page.route` segurando a requisição 300 ms | família viva (a corrida **não** acontece: o cookie é anexado no `continue`) | viva |
| Servidor lento (300 ms) | **revogada, 3/3** | viva, 3/3 (duas rotações em série) |
| Servidor rápido + latência de 300 ms via CDP | **revogada, 3/3** | viva, 3/3 |

A terceira linha é o método do E2E contra o binário real: não exige servidor lento.

### 8.3 `embed`, MIME e `emptyOutDir`

- `//go:embed all:dist` com só `.gitkeep`: compila, `fs.Sub` funciona, `index.html` ausente dá
  `fs.ErrNotExist` (tratável). `//go:embed dist`: **erro de compilação** ("contains no embeddable
  files").
- Tabela embutida do `mime` (Go 1.26): tem `.js`, `.mjs`, `.css`, `.svg`, `.png`, `.ico`, `.json`,
  `.txt`; **não tem** `.woff2`, `.webmanifest`, `.map`. Nesta máquina `.woff2` resolve por causa de
  `/etc/mime.types`.
- Vite 8 com `outDir` fora da raiz: sem `emptyOutDir`, não limpa (aviso) e acumula; com
  `--emptyOutDir`, **apaga o `.gitkeep`**.
- `html.cspNonce: 'VIGILUME_CSP_NONCE'` gera `<meta property="csp-nonce" nonce="…">` e `nonce` no
  `<script type="module">` e no `<link rel="stylesheet">`.
- Tamanho: 790 KB (239 KB gzip) com tudo num chunk; **sem o Recharts, 407 KB (128 KB)**. O
  Recharts é quase metade do bundle — por isso só no detalhe, lazy (D4).

### 8.4 axe sob a CSP

`@axe-core/playwright` 4.13 no build final, CSP de produção, sem `bypassCSP`: roda, não gera
violação de CSP, e apontou dois problemas reais no markup do spike (`button-name` no gatilho do
Select sem rótulo, `scrollable-region-focusable` no ScrollArea) nos dois temas.

## 9. Riscos

| # | Risco | Mitigação |
|---|---|---|
| R1 | Um componente shadcn novo (ou uma atualização do Radix) injeta `<style>` sem nonce e algo some em silêncio | Projeto `a11y` visita **todas** as rotas com CSP de produção e exige zero violações; regra de revisão: `grep` por `<style`, `dangerouslySetInnerHTML` e `createElement('style')` em `web/src`; lockfile fixa as versões |
| R2 | Refresh perde a resposta depois de o servidor rotacionar → reúso → login de novo | Aceito (§3.4); raro; seguro |
| R3 | Deriva entre DTO Go e schema TS | Zod nas respostas + goldens (D11) + E2E no caminho crítico |
| R4 | Recharts lento com 5.760 pontos | Medir no plano; LTTB (corte 5) ou `uPlot` |
| R5 | Bundle pesado | Lazy do detalhe e das configurações; gzip no handler; orçamento conferido no plano |
| R6 | Versões novas (TS 6, Vite 8, Vitest 5, React Router 8) com menos respostas prontas | Stack validado no spike (build, testes, E2E); versões fixadas |
| R7 | E2E esbarra no limite por usuário (burst 20) ou no do login (10/min) | Um usuário por arquivo se preciso; retries do Playwright desligados para não mascarar |
| R8 | Spike só em Chromium | Firefox no projeto `a11y` (corte 8); Web Locks, BroadcastChannel e nonce são Baseline |
| R9 | Escopo (spec §18) | Ordem da §3.8 com painel apresentável na fatia 3; lista de cortes da §2 |
| R10 | A janela de 15 min (D4 do Plano 5) lida como "encerrei e continua" | Textos da §4.8; aviso entre abas no mesmo navegador |
| R11 | `.gitkeep` apagado vira diff sujo | Plugin que o recria + verificação no CI (Plano 7) |
| R12 | Build sem `html.cspNonce` | 503 explícito no painel (§5.3) + E2E |

## 10. Decisões para o usuário revisar

Todas as decisões, com alternativa e custo se errada; as marcadas com ★ são as que mais merecem
segundo olhar.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 ★ | **Nonce por requisição** no `index.html` (marcador do `html.cspNonce` trocado pelo Go), só em `style-src` | Hashes; `style-src-attr`; trocar Radix/shadcn | Evidência na §8.1. Se errada, o custo é o index não ser cacheável — ele já não seria |
| D2 | Nonce propagado a quatro pontos, com os wrappers do shadcn editados | Deixar os `<style>` do Radix falharem (só estética) | Uma violação "tolerada" esconde a próxima que importa; o E2E exige zero |
| D3 | **Radix Toast em vez de sonner**; `ThemeProvider` próprio em vez de `next-themes` | sonner + `style-src 'unsafe-inline'` | Perde-se o visual pronto do sonner; ganha-se a CSP inteira |
| D4 | **Recharts só no detalhe, lazy**; sparkline em SVG próprio | Recharts no dashboard | +111 KB gzip na primeira carga |
| D5 ★ | **React Router 8** (modo *data*) em vez do v7 da spec §11.1 | Fixar 7.x | O 8 é o *major* atual (exige React ≥ 19.2.7, Node ≥ 22.22); a API usada é a mesma do v7 data mode. Voltar ao 7 é trocar a versão |
| D6 | Token opaco no cliente (nunca decodifica o JWT) | Ler `exp`/`sid` do token | Nenhum; o servidor já dá `expires_in` e `current` |
| D7 ★ | **Web Locks entre abas + BroadcastChannel só para eventos** (nunca token) | BroadcastChannel repassando o token; nada | Sem trava, a sessão cai com duas abas (§8.2). Repassar token espalha segredo e cria corrida nova |
| D8 | Refresh só em `401 unauthorized` fora de `/auth/*`, uma vez | Qualquer 401 | Repetir `invalid_credentials` conta falha no bloqueio (Plano 5) |
| D9 | Boot como promessa de módulo, fora do React | `useEffect` | `StrictMode` duplicaria o refresh |
| D10 | **Zod valida as respostas** (strip de chave extra, falha em chave ausente) | Só tipos TS | Alguns KB gzip e um parse por resposta; em troca, deriva vira erro com nome de campo em vez de `undefined` na tela |
| D11 | Goldens de contrato gerados pelo Go alimentam o MSW (cortável) | Fixtures à mão | Sem eles, uma fixture pode divergir do servidor e o teste de tela mente |
| D12 ★ | **Fallback de SPA para todo caminho sem extensão** fora de `/api/`, `/p/`, `/verify/` (separação estrutural no mux) | Lista de rotas do painel em Go | Caminho inexistente responde 200 (o React mostra o 404). A lista duplicaria a tabela de rotas |
| D13 | `/assets/*` imutável por 1 ano; index `no-store`; demais arquivos da raiz `no-cache` | `no-cache` no index | Nonce em cache compartilhado deixaria de ser por resposta |
| D14 | Tabela de MIME própria | `mime.TypeByExtension` | Fonte com tipo errado na distroless |
| D15 | `emptyOutDir` + plugin que recria o `.gitkeep` | Não limpar | Bundles velhos embutidos; binário crescendo a cada build |
| D16 | Sem build: painel 503, API segue | Falhar o boot | Um binário de API puro (testes, `--role=api` sem front) deixaria de subir |
| D17 | gzip em memória no boot (cortável) | Pré-comprimir no build; nada | Alguns MB de RAM |
| D18 | Sem rate limit no painel estático | O limitador por IP da API | Arquivos em memória custam quase nada; dividir o balde da API com o carregamento do painel geraria 429 falso |
| D19 | E2E com Postgres em container pelo `globalSetup` e alvo `.invalid` | Compose do Plano 7; alvo público | Depender do Plano 7 inverteria a ordem 5→6→7; alvo público depende de rede |
| D20 | Tema em `localStorage` (`vigilume.tema`) e `/tema.js` externo | Script inline com nonce | Nenhum segredo no storage (a §5.3 fala de tokens); um arquivo a mais |
| D21 | `credentials: 'same-origin'` | `'include'` | Quem servir o painel em outro host (o `CORS_ORIGIN`) precisa de `'include'` — e de mesmo *site*, por causa do `SameSite=Strict`; fica documentado |
| D22 | "Degradado" no painel, "Lento" na página pública | Um termo só | Inconsistência percebida; trocar é texto |
| D23 | Prévia aproximada da status page (cortável) | Nenhuma prévia; rota nova de prévia | Pode divergir do HTML real; o rótulo diz "aproximada" |
| D24 | Headers do alvo: substituir todos ou remover todos, nunca editar um | Reenviar valores | O servidor não devolve valores (D5 do Plano 2), e não deve |
| D25 | Detalhe mostra só o incidente aberto; filtro por alvo pedido ao Plano 5 | Filtrar no cliente | Filtrar lista paginada no cliente mostraria menos do que existe |
| D26 | Datas no fuso do navegador, com o fuso escrito | UTC como a página pública | O painel tem JavaScript; UTC seria atrito para o operador |
| D27 | `novoNonce` sai de `publicpage.go` para `crypto.NewCSPNonce` (pacote folha), usado pelos dois | Duplicar | `webui` não pode importar `httpapi` (ciclo); duas cópias de gerador de nonce divergem — o bug do "+" do Plano 4 foi exatamente num gerador |

## 11. Divergências com o contrato

Nenhum shape do contrato muda. O que segue são leituras, acréscimos pedidos e pontos de contato.

**Div. 1 (leitura) — "fallback de SPA para rotas do painel".** Implementado como "todo caminho sem
extensão que não seja da API, da página pública nem da verificação" (D12), não como lista de rotas.
A raiz passa a registrar `/api/`, `/healthz` e `/readyz` explicitamente em vez de mandar tudo para a
API; nenhum caminho da API muda de comportamento (o que antes era 404 fora de `/api/` vira o SPA).

**Div. 2 (detalhe) — `.gitkeep` × `emptyOutDir`.** O contrato diz que o `.gitkeep` mantém o diretório
antes do primeiro build; o build o apaga (§8.3). O plugin que o recria resolve sem mudar o
contrato. O `.gitignore` de hoje (`/web/dist/`) aponta para o lugar errado e é corrigido.

**Div. 3 (acréscimo pedido ao Plano 5, opcional) — `GET /incidents?target_id=`.** Filtro aditivo,
mesma paginação, `target_id` de outro usuário devolve lista vazia (não 404). Sem ele, o detalhe do
alvo mostra só o incidente aberto (D25). O painel não depende do acréscimo.

**Div. 4 (vindas do Plano 5, incorporadas):** `401 unauthorized` com família morta em `/sessions` e
`/auth/password` (tratado pela regra do 401); `409 target_limit_reached` (mapeado no formulário);
`sid` no token (o painel não o lê); política 15–128 *code points* (espelhada no formulário); `204`
do DELETE da sessão atual apaga o cookie (o painel não chama refresh nem logout).

**Div. 5 (spec do projeto, não contrato):** React Router 8 em vez de 7 (§11.1 → D5); prévia da
status page aproximada (§11.3 → D23); Trusted Types fora (§5.7 fala de CSP, não de TT).

### 11.1 Pontos de contato com o Plano 7

- **Ordem de build:** `npm ci && npm run build` (em `web/`) **antes** de `go build`; sem isso o
  binário sai com o painel 503 (§5.4). O Dockerfile precisa de um estágio Node.
- **CI:** `npm run typecheck`, `npm run lint`, `npm test` (Vitest), `npm run build`,
  `git status --porcelain internal/webui/dist` vazio, o teste Go de MIME sobre o dist real, e o E2E
  (precisa de Docker; o job do GitHub tem).
- **`init.sql`** do papel comum do E2E serve de base para o compose.
- **gzip da API** (summary de ~180 KB a cada 15 s): decisão de proxy/compose, não do painel.
- `CORS_ORIGIN` continua obrigatório no `config`, mesmo com o painel na mesma origem: o compose
  precisa preenchê-lo (com a própria origem).
