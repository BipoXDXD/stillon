# Vigilume — Plano 6: Painel React

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** O painel administrativo da spec §11 — login com 2FA, dashboard, CRUD de alvos, detalhe com gráfico, incidentes, canais, status page e configurações — servido pelo próprio binário Go na mesma origem da API, com token só em memória, refresh que não derruba a sessão sob concorrência (nem entre abas), CSP sem `unsafe-inline` e acessibilidade verificada.

**Architecture:** O handler novo `internal/webui` embute `internal/webui/dist` (`//go:embed all:dist`) e serve o painel na raiz: assets com hash e cache imutável, `index.html` com um nonce de CSP novo a cada resposta, fallback de SPA para todo caminho sem extensão; a raiz passa a nomear `/api/`, `/healthz` e `/readyz`. O painel (`web/`, Vite 8 + React 19 + TypeScript strict) tem uma camada de API única — `sessao.ts` (token em memória, boot pelo cookie, refresh *single-flight* dentro da aba, Web Locks entre abas, `BroadcastChannel` só com eventos) e `cliente.ts` (a regra do 401, schemas Zod das respostas) — sob React Router 8 e TanStack Query. Os testes de cliente rodam contra uma API falsa com estado, no MSW, que imita as guardas do servidor; o E2E Playwright roda contra o binário real e um Postgres em container.

**Tech Stack:** Go 1.26, PostgreSQL 18 (testes), `embed`, `compress/gzip`; Node ≥ 22.22, Vite 8.3, React 19.3, TypeScript 6.0, React Router 8.4, TanStack Query 5.103, React Hook Form 7.88 + Zod 4.6, Tailwind 4.3, Radix (`radix-ui` 1.6.7), Recharts 3.10, Vitest 5.0 + Testing Library + MSW 2.15, Playwright 1.63 + axe-core.

**Spec:** `docs/superpowers/specs/2026-09-24-plano-6-painel-react-design.md` (a do painel), `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md` (o contrato, com a seção **Reconciliação**, que prevalece) e `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§5.3, §5.7, §9.1, §11, §14, §18).

**Base:** o estado final do Plano 5 no `main` (merge `e3aec43`, com a Task 11 de correções; `docs/superpowers/plans/2026-09-24-api-do-painel.md`): `vigilume user create`, `GET /me` com `email` e `totp_enabled`, `POST /auth/password`, `GET/DELETE /sessions`, `GET /targets/summary`, o filtro `GET /incidents?target_id=` e o `409 target_limit_reached`. Os diffs em arquivos Go aplicam sobre esse estado.

**Validação prévia.** Todo o código deste plano rodou numa cópia do repositório antes de o plano ser entregue: `go test -race -count=1 ./...` verde (PostgreSQL 18 real), `vet`, `staticcheck`, `gosec` e `govulncheck` limpos; `npm run typecheck`, `npm run lint`, `npm test` e `npm run build` verdes; o E2E Playwright verde contra o binário real, sem rede externa. Além disso, **o estado da árvore depois de cada tarefa** foi reconstruído a partir deste plano e conferido (compila, `vet`, os testes Go da tarefa com `-race`; do painel em diante, typecheck, lint, Vitest, build e o teste Go do dist real); o estado depois da Task 11 é idêntico, byte a byte, à cópia validada (fora `package-lock.json`, ver abaixo). Cada sabotagem pedida nos passos foi feita, e o teste ficou vermelho **pela razão certa**; as que ficaram verdes estão ditas no passo, com o motivo. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

**Como ler os diffs.** Arquivo novo aparece inteiro. Arquivo modificado aparece como diff unificado contra o estado da tarefa anterior; o contexto basta para aplicar à mão, ou com `git apply` depois de salvar o bloco num arquivo.

**O que é gerado, e não aparece no plano.**
- `web/package-lock.json` sai do `npm install` da Task 2. As dependências diretas estão com **versão exata** no `package.json`; as transitivas, o npm resolve na hora. O lock que a validação usou não vai no plano (são milhares de linhas); quem executa commita o que o `npm install` gerar. Se o lock resolver outra versão transitiva e algo quebrar, isso é informação: pare e reporte.
- `internal/webui/dist/*` sai do `npm run build` e é ignorado pelo git; só o `.gitkeep` é versionado (Task 1).
- `web/node_modules/`, `web/test-results/`, `web/playwright-report/`, `web/e2e/.tmp/`: ignorados (Task 1).

---

## Global Constraints

Valores copiados da spec do Plano 6, do contrato e da reconciliação. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26**, módulo `github.com/BipoXDXD/vigilume`, sem framework HTTP. **Node ≥ 22.22** (exigência do React Router 8). Dependências diretas do painel com **versão exata** no `package.json`.
- **O painel é servido pelo binário, na mesma origem da API** (contrato, item 2): build do Vite em `internal/webui/dist/`, `//go:embed all:dist`, `.gitkeep` versionado. `/api/`, `/p/`, `/verify/`, `/healthz`, `/readyz` continuam como estão.
- **Token só em memória** (spec §5.3). Nada em `localStorage`, `sessionStorage`, IndexedDB ou cookie legível. O refresh é o cookie `HttpOnly; SameSite=Strict; Path=/api/v1/auth` do servidor. A única coisa que o painel guarda no navegador é o tema (`vigilume.tema`).
- **Toda mutação envia `X-Requested-With: vigilume`** (contrato, item 4), inclusive `/auth/refresh`, `/auth/login` e `/auth/logout`.
- **A regra do 401** (reconciliação, item 3): só `401` com `code: unauthorized`, **fora de `/auth/*`**, renova e repete **uma** vez. `invalid_credentials` nunca renova. Refresh recusado, ou `401` de novo, é o fim da sessão.
- **Refresh single-flight dentro da aba e serializado entre abas** (spec §11.2, item 3; spec do Plano 6, D7): `navigator.locks.request('vigilume:refresh', …)`. O `BroadcastChannel('vigilume:sessao')` leva **eventos**, nunca o token.
- **`DELETE /sessions/{id}` da sessão atual** (nota do Plano 5 §12): o painel larga o token, limpa o cache e vai ao `/login`, **sem** chamar `/auth/refresh` nem `/auth/logout`.
- **Política de senha nova: 15 a 128 code points** (Plano 5, D1; sem blocklist — reconciliação, item 9). Conta-se com `[...s].length`, nunca `s.length`.
- **CSP do painel:** `default-src 'none'; script-src 'self'; style-src 'self' 'nonce-{n}'; img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`. **Sem `unsafe-inline`, sem `unsafe-eval`.** Nonce de 128 bits em base64 de URL, novo a cada resposta. **Zero violações de CSP** em todas as telas é requisito, verificado no E2E.
- **Cache:** `/assets/*` `public, max-age=31536000, immutable`; `index.html` `no-store`; demais arquivos da raiz `no-cache`. **Asset ausente é 404, nunca o `index.html`.**
- **Estado sempre com ícone e texto** (spec §11.6): No ar, Degradado, Fora do ar, Sem dados, Pausado. Mudança de estado anunciada numa região `aria-live` — **só a mudança**. Contraste **AA** nos temas claro e escuro, verificado com axe.
- **`refetchInterval`** (spec §11.4): 15 s no dashboard, 60 s no detalhe, suspenso em segundo plano.
- **Porcentagem truncada** em duas casas, nunca arredondada ("99,99%", nunca "100,00%" com falha).
- **E2E** (reconciliação, item 4): por padrão o Playwright sobe a própria pilha no `globalSetup`; com `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD`, roda contra uma pilha já de pé. O alvo cadastrado é `https://vigilume-e2e.invalid/`, e o teste confere que ele aparece, sem depender do estado.
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa**; do painel em diante, também `npm run typecheck`, `npm run lint`, `npm test` e `npm run build` (em `web/`).
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não renova", "não vaza", "não anuncia", "não manda"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho **pela razão certa** (uma sabotagem que não compila não prova nada: o build falha e o teste velho continua servindo), restaura, e registra no relatório. Teste com tempo ou sorteio roda várias vezes.

### Desvios registrados em relação à spec do Plano 6

| Spec | Aqui | Motivo |
|---|---|---|
| D2: nonce propagado a **quatro** pontos (`setNonce`, `Select.Viewport`, `ScrollArea.Viewport`, `ChartStyle`) | **Um** ponto: `setNonce` (react-remove-scroll do Dialog/AlertDialog) | O painel não usa o Select nem o ScrollArea do Radix (`<select>` nativo), nem o `ChartStyle` do shadcn (as cores do gráfico são variáveis CSS do `index.css`). Três injeções deixam de existir em vez de serem autorizadas. O E2E prova zero violações em todas as telas |
| §3.1: componentes pela CLI do shadcn | Componentes próprios no estilo shadcn (Radix + `class-variance-authority` + `tailwind-merge`), escritos inteiros no plano | O registro do shadcn muda com o tempo (o *preset* de 2026-09 trouxe `next-themes`, `@fontsource`, o pacote `cn` e um `<style>` injetado no gráfico); o plano precisa ser reprodutível |
| D11: goldens de contrato Go → MSW (cortável) | **Cortado** | A deriva DTO × painel é pega pelas fixtures tipadas com `z.input` dos schemas, pela API falsa, e pelo E2E contra o binário real no caminho crítico |
| §4.8: tema em `/settings` | Tema no cabeçalho, em todas as telas | Um lugar só, sempre ao alcance |
| §7.4: controle positivo com array na página | Violações registradas no processo do teste (`exposeFunction`) | O array da página zera a cada F5 e esconderia a violação da tela anterior (achado na validação) |
| §7.4 e §8.2: E2E de abas com latência de 300 ms via CDP (`Network.emulateNetworkConditions`), 3/3 no spike | A **resposta** de cada `/auth/refresh` é segurada no navegador (CDP, `Fetch` no estágio `Response`) até as duas abas terem a sua, ou 1,5 s (D11) | Contra o binário real (o do spike imitava o `Rotate` num servidor próprio), a latência emulada não reproduzia a corrida toda vez: a sabotagem da trava falhava só 2 de 3. Segurando a resposta, 10 de 10 |
| §4.4 e R4: downsampling (LTTB) se o Recharts passar de ~100 ms por render | **Não feito** (corte 5, que a spec permite) | Medido na validação (Chromium, Ryzen 7 5800X): desenhar 5.760 checks leva ~750 ms e um re-render com os dados em cache ~160 ms (a tela com 60 pontos: ~45 ms); 2.880 checks, ~430 ms e ~100 ms. A maior janela bruta que o painel pede é 24 h (2.880 pontos a 30 s), com poll de 60 s; 5.760 só com 48 h, que o painel não pede. O custo fica num render por minuto na tela de detalhe |
| §7.4, corte 8: Firefox no projeto `a11y` | **Cortado** (a spec permite) | Um segundo motor pede o Firefox do Playwright instalado e mais uma volta do projeto `a11y`; o Chromium já cobre a CSP de produção e o contraste. Firefox fica para o CI do Plano 7, se o tempo der |
| §7.2 e §7.5: teste "boot sob `StrictMode` → um refresh", com a sabotagem "boot dentro de `useEffect` sem guarda" | O boot fica **fora** da árvore (`main.tsx`, D9 da spec); a árvore dos testes roda sob `StrictMode` como a de produção, e o teste de F5 confere **um** refresh. A sabotagem prevista só fica vermelha somada à das duas guardas de `sessao.ts` (Task 3, sabotagem 8) | Com o boot fora da árvore, um efeito sem guarda ainda cai no boot único e no *single-flight*; são as guardas que a spec pedia, provadas em `chamado duas vezes, renova uma vez só` (Task 2) |

---

## Decisões tomadas neste plano

As decisões de desenho estão na spec do Plano 6 (D1–D27). Estas são as do plano, achadas ou tomadas na validação, com o custo de estarem erradas.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | **`z.config({ jitless: true })`** em `schemas.ts` | Deixar o JIT do Zod | O Zod 4 testa `Function('')` para decidir o JIT; o teste é pego num `try`, mas o navegador reporta **uma violação de `script-src`** a cada carga. Achado pelo E2E na validação. O custo é validação sem JIT: irrelevante nos tamanhos do painel |
| D2 | Rotas de credencial (`/auth/password`, `/auth/totp/enroll`, `/confirm`, `/disable`) **nunca renovam sozinhas**; um `unauthorized` delas guarda o formulário e oferece **"Renovar acesso e tentar de novo"**: o botão renova, e quem reenvia é o usuário | Renovar antes de enviar (`renovarAntes`); renovar e repetir sozinho, como as outras rotas; pedir para recarregar a página | A reconciliação proíbe repetir em `/auth/*`: repetir reenviaria senha e código. O D24 do Plano 5 tirou o motivo de um refresh preventivo (que uma versão anterior deste plano fazia): senha ou código errado é `invalid_credentials`, e `unauthorized` ali é token vencido ou família morta. Custo: um clique a mais para quem deixa a tela parada 15 minutos; com a família morta, o clique leva ao login (o refresh é recusado). Recarregar a página, a versão anterior, perdia o que estava digitado |
| D3 | Requisição **sem token** que volta 401 não renova | Renovar | Uma consulta que ainda voava quando a sessão acabou rodaria um refresh com o cookie já apagado e trocaria o motivo do fim ("Você encerrou esta sessão" viraria "Sua sessão terminou"). Achado pelo teste da Task 10 |
| D4 | **API falsa com estado** para o MSW (`test/api-falsa.ts`), imitando as guardas: 403 sem `X-Requested-With`, 401 sem Bearer válido, rotação com detecção de reúso, pote de cookies único por teste, e as da Task 11 do Plano 5 (família morta → 401 `unauthorized` em `/auth/password`, `/auth/totp/*` e `/sessions`; trocar a senha apaga o cadastro de 2FA pendente e invalida os desafios de login) | Handlers soltos por teste | Handlers que só respondem o caminho feliz deixam o teste de cliente vacuamente verde (lições do projeto) |
| D5 | Fixtures tipadas com `z.input` dos schemas das respostas | Objetos soltos | Fixture fora do contrato compila e mente |
| D6 | `key` nos formulários que trocam de campo (canal Discord/e-mail, passos do login) | Sem `key` | O React reaproveita o `<input>` e o texto fica registrado no campo anterior. Achado pelo teste da Task 8 |
| D7 | Token de cor **`--realce`** para links (claro `#1d4ed8`, escuro `#93c5fd`) | Usar a cor do botão | O azul do botão não passa AA como texto no fundo escuro. Achado pelo axe na validação |
| D8 | O `webui` lê e comprime (gzip, ≥ 1 KiB, tipos de texto) **tudo no boot**; arquivo oculto nem entra no mapa | Ler do `embed.FS` a cada requisição | Alguns MB de RAM. Havia também uma checagem de arquivo oculto na rota; a sabotagem mostrou que era redundante (a da carga já garante), e ela saiu |
| D9 | `Deps.Painel fs.FS` (nil = embutido) | Montar o painel fora de `Routes` | O teste de wiring precisa do `Routes()` inteiro com um dist falso |
| D10 | E2E: `globalSetup` sobe Postgres 18 + binário + `vigilume user create`; `workers: 1`, `retries: 0`; violações de CSP no processo do teste, com controle positivo | `webServer` do Playwright | O `webServer` sobe **antes** do `globalSetup`, e o binário precisa do banco antes. Retries esconderiam o teste que depende de sorte |
| D11 | Várias abas no E2E: o teste segura a **resposta** de cada `/auth/refresh` (CDP, `Fetch` no estágio `Response`) até as duas abas terem a sua, ou 1,5 s; o prazo é desarmado a cada liberação | Latência de 300 ms via `Network.emulateNetworkConditions`; `page.route` segurando a requisição | `page.route` não reproduz a corrida (o cookie é anexado no `continue`); a latência emulada, contra o binário real e rápido, só a reproduzia às vezes. Segurando a resposta, sem a Web Lock a sessão cai **10 de 10** na validação; com a trava, **0 de 5**. Um prazo que não era desarmado deixava a sabotagem verde 1 vez em 5 (um prazo velho soltava a resposta da rodada seguinte): achado ao validar, corrigido. Depois de as abas assentarem, o teste confere no banco da pilha própria que não houve auditoria `session.reuse_detected` (spec §7.4); a sabotagem fica vermelha nessa asserção, 5 de 5. Custo: ~3 s por rodada com a trava (a segunda aba espera o prazo) |
| D12 | Os testes de cada tarefa só dependem de telas que já existem: o painel provisório da Task 3 e o 404 servem de destino | Testes que visitam telas futuras | Um estado intermediário com teste vermelho por falta de tela futura |
| D13 | `chunkSizeWarningLimit: 700` | O aviso padrão de 500 KB | O chunk principal (React, Router, Query, Radix, Zod) tem ~620 KB (~190 KB gzip); o Recharts (~390 KB) já saiu para o chunk do detalhe |
| D14 | Nunca uma exceção crua: `lerCorpo` transforma JSON quebrado ou fora do schema em `ApiError` `unexpected` com o status; o `QueryClient` só repete rede caída e 5xx (`podeRepetir`) | `schema.parse` direto; repetir tudo que não é 4xx | Uma tela com `ZodError` cru, e três tentativas de uma resposta que o mesmo servidor daria igual (spec do Plano 6 §3.4; I4 e M9 da revisão) |
| D15 | A sessão tem uma **época**, que muda a cada login e fim de sessão; um refresh que volta em outra época não mexe no token, e o cliente só marca "expirou" se a sessão ainda estava aberta | Aplicar a resposta do refresh sempre | Sair (ou encerrar a sessão aqui) com um refresh em voo ressuscitava o token e trocava o motivo do fim (M8 da revisão) |
| D16 | O painel **exige contexto seguro** (HTTPS ou localhost): fora dele, um aviso visível; nenhuma trava substituta | Uma trava em `localStorage` | Sem `navigator.locks`, duas abas derrubam a sessão; `localStorage` não é exclusivo entre abas, e uma trava falsa esconderia o defeito (decisão do coordenador, I3). O Plano 7 documenta HTTPS como requisito |
| D17 | Poll de fundo que falha **mantém** os dados na tela, com "Não foi possível atualizar agora… Mostrando os dados de …" | Trocar a tela pelo erro | Um 502 de passagem apagaria o dashboard inteiro a cada 15 s (M9) |
| D18 | O painel embutido é lido e comprimido **uma vez por processo** (`sync.OnceValue`) | `webui.New` a cada `Routes()` | ~70 s a mais no pacote `httpapi` com `-race` e um dist real (169 s → 101 s, medido); em produção `Routes()` roda uma vez (M6) |
| D19 | `VIGILUME_EXIGIR_DIST=1` faz `TestTodoArquivoDoDistTemTipo` falhar com o dist vazio | O teste passar vazio sempre | Sem build, o teste não confere nada e fica verde; o CI do Plano 7, que roda depois do build, liga a variável (M7) |
| D20 | O `<h1>` recebe o foco só depois de uma navegação dentro do painel; na carga da página, não (`Titulo`, um valor do módulo) | Focar o título sempre (§3.7 ao pé da letra) | Focar na carga tirava o *skip link* do primeiro Tab (§4.9). Com a regra, as duas partes da spec valem juntas (decisão do coordenador) |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa o painel, com o que uma pessoa razoável esperaria. Cada uma tem teste na tarefa dona.

1. **Duas abas abertas (ou a restauração de sessão do navegador) renovam o token juntas.** Espera-se continuar dentro nas duas, e não ser derrubado pela detecção de reúso. → Task 2, `duas abas renovando juntas rotacionam em série, e a família sobrevive`; Task 11, `abas.spec.ts`.
2. **Houve deploy com a aba aberta, e o chunk antigo sumiu.** Espera-se "Há uma versão nova do painel. Recarregar", e não uma tela branca. → Task 1, `asset ausente é 404, nunca o index`; Task 3, `erro-de-rota.test.tsx`.
3. **O painel ficou 15 minutos parado e o usuário volta a clicar.** Espera-se que funcione sem pedir login; no formulário de senha ou de 2FA, que o que foi digitado fique e a tela ofereça renovar o acesso, sem acusar a senha. → Task 2, `cinco 401 simultâneos disparam um refresh só…` e `token vencido na troca de senha não renova: /auth/* nunca renova`; Task 10, `com o acesso vencido nesta tela, guarda o formulário e oferece renovar; o reenvio é do usuário`.
4. **O usuário cola a URL de um host interno, ou a do webhook de um canal de fórum.** Espera-se o erro no campo certo, com texto que explique. → Task 5, `o 400 com fields vai para o campo certo` e o espelho de `validate.go`; Task 8, `entrega que falha e e-mail não configurado viram texto claro`.
5. **A sessão acaba fora desta aba** (saiu em outra aba, encerrou a própria sessão em Configurações). Espera-se ir ao login com o motivo, sem requisição fantasma. → Task 3, `sessão encerrada em outra aba volta ao login com o aviso`; Task 10, `encerrar a própria sessão volta ao login, sem refresh nem logout depois`.

---

## Passada de teclado

Spec §6. É um teste do projeto `a11y` (Task 11, `passada de teclado em todas as rotas`), que quem executa roda de novo: em cada rota, numa carga nova, o primeiro Tab é o "Pular para o conteúdo" e ativá-lo leva o Tab seguinte para dentro do conteúdo; depois, Tab por todas as paradas até o foco sair da página (nenhuma armadilha), com o foco visível em cada uma. O teste anexa a lista de paradas ao relatório do Playwright. Na validação, com a API do projeto `a11y` (os cinco estados, um canal falhando):

| Rota | Paradas | Depois do cabeçalho (*skip link*, cinco links, tema, Sair) |
|---|---|---|
| `/` | 15 | Cadastrar alvo, os cinco cartões, "Incidente aberto" |
| `/targets/new`, `/targets/:id/edit` | 18 | Campos na ordem visual; o grupo de rádio do limiar é uma parada (setas trocam) |
| `/targets/:id` (24 h e 7 dias) | 20 | Editar, Pausar, Apagar, as seis janelas, o gráfico (a `accessibilityLayer` do Recharts), "Ver dados em tabela", "Todos os incidentes" |
| `/incidents` | 13 | Os três filtros, o alvo, Reconhecer |
| `/channels` | 13 | Novo canal e as ações de cada canal |
| `/status-pages` | 16 | Copiar, Abrir, os campos, Tirar, Salvar |
| `/settings` | 14 | Os campos da senha, Ativar, Encerrar |

Quem revisar o PR repete a passada à mão.

## Para o Plano 7

- **HTTPS é requisito do painel** (D16): fora de contexto seguro (HTTPS, ou http em localhost) não há `navigator.locks`, e duas abas derrubam a sessão; o painel mostra um aviso e não tem trava substituta. O README e o runbook dizem isso.
- **`VIGILUME_EXIGIR_DIST=1`** no passo do CI que roda `go test` depois do `npm run build` (D19).
- O E2E (`npm run e2e` em `web/`) sobe a própria pilha com Docker; com `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD`, roda contra o compose. Contra uma pilha externa, a asserção de auditoria do teste de abas é pulada (o teste não alcança o banco).

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/crypto/nonce.go` | `NewCSPNonce`, usado pela página pública e pelo painel. |
| `internal/webui/webui.go` | O handler do painel: embed, MIME, cache, fallback de SPA, CSP com nonce, gzip, 503 sem build. |
| `internal/httpapi/routes.go` | A raiz nomeia `/api/`, `/healthz`, `/readyz`; o painel em `/`; `Deps.Painel`. |
| `.gitignore` | `internal/webui/dist/*` com exceção do `.gitkeep`; artefatos do Playwright. |
| `web/package.json`, `tsconfig*.json`, `vite.config.ts`, `.oxlintrc.json` | O projeto do painel; o build sai em `internal/webui/dist`. |
| `web/index.html`, `web/public/tema.js`, `web/src/index.css` | A página, o tema antes da pintura, os tokens de cor (AA nos dois temas). |
| `web/src/lib/api/http.ts` | Transporte, `ApiError`, leitura do envelope, `X-Requested-With`. |
| `web/src/lib/api/sessao.ts` | Token em memória, boot, refresh (single-flight, Web Locks), login, TOTP, sair, aviso entre abas. |
| `web/src/lib/api/cliente.ts` | A regra do 401 e as funções de cada rota da API. |
| `web/src/lib/api/schemas.ts` | Zod das respostas (o contrato em tempo de execução). |
| `web/src/lib/api/consultas.ts` | Chaves e hooks do TanStack Query, com os intervalos. |
| `web/src/lib/api/mensagens.ts` | Vocabulário fechado → texto. |
| `web/src/app/*` | Contexto, rotas, layout, porta da sessão, erro de rota, 404. |
| `web/src/components/*` | Botão, campo, diálogos, avisos (Radix Toast), selo de estado, minigráfico, anúncios, título. |
| `web/src/features/*` | As telas: login, painel, alvos (formulário, detalhe, gráfico), incidentes, canais, status page, configurações. |
| `web/src/test/*` | Setup do MSW, API falsa, fixtures, render do painel inteiro. |
| `web/e2e/*`, `web/playwright.config.ts` | A pilha do E2E e os três specs: fluxo crítico, abas, acessibilidade/CSP. |

---

## Ordem das tarefas e seu motivo

1. **O handler do painel no binário:** Go puro, testável com um dist falso; é o que torna o resto servível e o que carrega a CSP.
2. **O projeto `web/` e o núcleo de sessão:** o risco maior do plano (refresh sob concorrência, a regra do 401) é provado antes de existir tela.
3. **A casca e o login:** o primeiro valor visível — entrar, F5, sair.
4. **Dashboard**, 5. **cadastro de alvos:** com eles, o painel é apresentável.
6. **Detalhe com gráfico:** o chunk lazy do Recharts e o diálogo de confirmação.
7. **Incidentes**, 8. **canais**, 9. **status page**, 10. **configurações:** uma tela por tarefa, cada uma com os seus erros de API mapeados.
11. **E2E:** o fluxo crítico, as abas e a acessibilidade/CSP de todas as telas, contra o binário real.

---

### Task 1: O handler do painel no binário

O pacote novo `internal/webui` serve o que o Vite vai gerar (Task 2) em `internal/webui/dist`, embutido no binário. A raiz de `Routes` deixa de mandar tudo para a API e passa a nomear `/api/`, `/healthz` e `/readyz`; o resto é do painel. O gerador de nonce da página pública sai para `internal/crypto`, porque o painel precisa do mesmo e `webui` não pode importar `httpapi` (ciclo).

**Files:**
- Create: `internal/crypto/nonce.go`, `internal/crypto/nonce_test.go`
- Create: `internal/webui/webui.go`, `internal/webui/webui_test.go`, `internal/webui/dist/.gitkeep`
- Create: `internal/httpapi/painel_integration_test.go`
- Modify: `internal/httpapi/publicpage.go`, `internal/httpapi/publicpage_test.go` (o nonce sai daqui)
- Modify: `internal/httpapi/routes.go`, `.gitignore`

**Interfaces:**
- Consumes: `middleware.SecurityHeaders`, `middleware.Recover`, `middleware.RequestID`; o helper de teste `newAPICom(t, ajustar, ajustarDeps)` de `auth_integration_test.go`.
- Produces:
  - `func crypto.NewCSPNonce() (string, error)` — 22 caracteres de base64 de URL.
  - `const webui.Marcador = "VIGILUME_CSP_NONCE"` — o valor de `html.cspNonce` que a Task 2 põe no `vite.config.ts`.
  - `func webui.Dist() fs.FS`, `func webui.New(fs.FS) *webui.Painel` (um `http.Handler`), `func webui.Politica(nonce string) string`, `func webui.TipoDe(nome string) string`.
  - `httpapi.Deps.Painel fs.FS` (nil = `webui.Dist()`).

**Contexto para quem implementa.** O `//go:embed all:dist` compila com o diretório só com o `.gitkeep` (sem o `all:`, o compilador recusa: "contains no embeddable files"), então `go test ./...` e `go build` funcionam sem Node; o painel responde 503 dizendo o que fazer. A tabela de MIME é própria porque a embutida do Go não tem `.woff2`, `.webmanifest` nem `.map`: numa máquina de desenvolvimento o `/etc/mime.types` completa a lista, e na imagem distroless do Plano 7 não. Por isso o teste de `.woff2` com um dist falso **fica verde** se a tabela for trocada por `mime.TypeByExtension` nesta máquina; a guarda de verdade é `TestTodoArquivoDoDistTemTipo`, que confere o dist real depois do build (a partir da Task 2). O teste de wiring (`TestCadaPrefixoCaiNaCadeiaCerta`) passa pelo `Routes()` inteiro: os testes de middleware do Plano 1 provaram as peças e deixaram o arquivo de rotas sem trava. Sem build, `TestTodoArquivoDoDistTemTipo` não tem o que conferir e passa vazio; com `VIGILUME_EXIGIR_DIST=1` (o CI do Plano 7 liga depois do `npm run build`), dist vazio é erro. O painel embutido é lido e comprimido **uma vez por processo** (`sync.OnceValue` em `routes.go`): a suíte de integração monta `Routes()` em quase todo teste, e refazer o `webui.New` a cada vez custava ~70 s a mais com `-race` e um dist real (medido na validação: 169 s → 101 s no pacote `httpapi`).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `internal/crypto/nonce_test.go`:

```go
package crypto_test

import (
	"regexp"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// O nonce vai para um atributo HTML e para o cabeçalho da CSP, e os dois
// têm de ser o mesmo texto. Um caractere que o html/template escapa ("+"
// vira "&#43;") quebraria isso só às vezes, conforme o sorteio: por isso o
// teste sorteia mil (D26 do Plano 4).
func TestCSPNonceNeedsNoEscaping(t *testing.T) {
	seguro := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	for range 1000 {
		n, err := crypto.NewCSPNonce()
		if err != nil || !seguro.MatchString(n) {
			t.Fatalf("nonce %q (%v): queria 22 caracteres de base64 de URL", n, err)
		}
	}
}

// Um nonce repetido deixa de ser segredo da resposta: quem viu uma página
// saberia o nonce da próxima.
func TestCSPNonceIsFreshEveryTime(t *testing.T) {
	vistos := map[string]bool{}
	for range 1000 {
		n, err := crypto.NewCSPNonce()
		if err != nil {
			t.Fatal(err)
		}
		if vistos[n] {
			t.Fatalf("nonce repetido: %q", n)
		}
		vistos[n] = true
	}
}
```

Crie `internal/webui/webui_test.go`:

```go
package webui_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BipoXDXD/vigilume/internal/webui"
)

// indexDeTeste imita o index.html que o Vite gera com html.cspNonce: o
// marcador no <meta> e nos dois elementos que carregam o bundle.
const indexDeTeste = `<!doctype html><html lang="pt-BR"><head>` +
	`<meta property="csp-nonce" nonce="VIGILUME_CSP_NONCE">` +
	`<script type="module" crossorigin src="/assets/index-abc123.js" nonce="VIGILUME_CSP_NONCE"></script>` +
	`<link rel="stylesheet" crossorigin href="/assets/index-def456.css" nonce="VIGILUME_CSP_NONCE">` +
	`</head><body><div id="root"></div></body></html>`

// bundleGrande passa do limite do gzip e comprime bem.
var bundleGrande = strings.Repeat("console.log('vigilume');\n", 200)

func distDeTeste() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                      {Data: []byte(indexDeTeste)},
		"assets/index-abc123.js":          {Data: []byte(bundleGrande)},
		"assets/index-def456.css":         {Data: []byte("body{margin:0}")},
		"assets/geist-latin-9f8e7d.woff2": {Data: []byte("wOF2")},
		"assets/estranho-1a2b3c.xyz":      {Data: []byte("?")},
		"favicon.svg":                     {Data: []byte("<svg/>")},
		"tema.js":                         {Data: []byte("document.documentElement.classList")},
		".gitkeep":                        {Data: nil},
	}
}

func pedir(t *testing.T, h http.Handler, metodo, caminho string, cab ...string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(metodo, caminho, nil)
	for i := 0; i+1 < len(cab); i += 2 {
		req.Header.Set(cab[i], cab[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func corpo(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var nonceDaCSP = regexp.MustCompile(`style-src 'self' 'nonce-([A-Za-z0-9_-]{22})'`)

// A tabela da spec do Plano 6 §5.2, caso a caso.
func TestRoteamentoDoPainel(t *testing.T) {
	h := webui.New(distDeTeste())
	casos := []struct {
		nome, metodo, caminho string
		status                int
		tipo, cache           string
		index                 bool
	}{
		{"raiz é o index", "GET", "/", 200, "text/html; charset=utf-8", "no-store", true},
		{"rota do painel é o index", "GET", "/targets/0190a1b2-c3d4", 200, "text/html; charset=utf-8", "no-store", true},
		{"rota desconhecida sem extensão também", "GET", "/qualquer/coisa", 200, "text/html; charset=utf-8", "no-store", true},
		{"index.html pelo nome sai com nonce", "GET", "/index.html", 200, "text/html; charset=utf-8", "no-store", true},
		{"asset com hash é imutável", "GET", "/assets/index-abc123.js", 200, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", false},
		{"css", "GET", "/assets/index-def456.css", 200, "text/css; charset=utf-8", "public, max-age=31536000, immutable", false},
		{"woff2 sem depender do /etc/mime.types", "GET", "/assets/geist-latin-9f8e7d.woff2", 200, "font/woff2", "public, max-age=31536000, immutable", false},
		{"extensão desconhecida não executa", "GET", "/assets/estranho-1a2b3c.xyz", 200, "application/octet-stream", "public, max-age=31536000, immutable", false},
		{"asset ausente é 404, nunca o index", "GET", "/assets/index-velho.js", 404, "text/plain; charset=utf-8", "no-store", false},
		{"arquivo da raiz revalida", "GET", "/favicon.svg", 200, "image/svg+xml", "no-cache", false},
		{"script do tema revalida", "GET", "/tema.js", 200, "text/javascript; charset=utf-8", "no-cache", false},
		{"arquivo da raiz ausente é 404", "GET", "/robots.txt", 404, "text/plain; charset=utf-8", "no-store", false},
		{"arquivo oculto não sai", "GET", "/.gitkeep", 404, "text/plain; charset=utf-8", "no-store", false},
		{"diretório de assets não lista", "GET", "/assets/", 200, "text/html; charset=utf-8", "no-store", true},
		{"POST é 405", "POST", "/", 405, "text/plain; charset=utf-8", "no-store", false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := pedir(t, h, c.metodo, c.caminho)
			if r.StatusCode != c.status {
				t.Fatalf("status = %d, queria %d", r.StatusCode, c.status)
			}
			if got := r.Header.Get("Content-Type"); got != c.tipo {
				t.Errorf("Content-Type = %q, queria %q", got, c.tipo)
			}
			if got := r.Header.Get("Cache-Control"); got != c.cache {
				t.Errorf("Cache-Control = %q, queria %q", got, c.cache)
			}
			b := corpo(t, r)
			if ehIndex := strings.Contains(b, `<div id="root">`); ehIndex != c.index {
				t.Errorf("corpo é o index = %v, queria %v", ehIndex, c.index)
			}
			if strings.Contains(b, webui.Marcador) {
				t.Errorf("o marcador saiu no corpo: %q", b)
			}
		})
	}
	if r := pedir(t, h, "POST", "/"); r.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("405 sem Allow: %q", r.Header.Get("Allow"))
	}
}

// O nonce do cabeçalho é o do <meta> e o dos dois elementos do bundle. Se um
// deles divergisse, o navegador bloquearia justamente os <style> que o
// painel injeta, e o gráfico sumiria (spec do Plano 6 §8.1).
func TestIndexLevaONonceDaCSP(t *testing.T) {
	h := webui.New(distDeTeste())
	r := pedir(t, h, "GET", "/")
	csp := r.Header.Get("Content-Security-Policy")
	m := nonceDaCSP.FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("CSP sem nonce de estilo: %q", csp)
	}
	b := corpo(t, r)
	if n := strings.Count(b, `nonce="`+m[1]+`"`); n != 3 {
		t.Errorf("o nonce da CSP aparece %d vezes no corpo, queria 3 (meta, script, link):\n%s", n, b)
	}
	for _, diretiva := range []string{"default-src 'none'", "script-src 'self';", "frame-ancestors 'none'",
		"base-uri 'none'", "form-action 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, diretiva) {
			t.Errorf("CSP sem %q: %s", diretiva, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP afrouxada: %s", csp)
	}
}

// Um nonce repetido deixa de ser segredo da resposta. Sabotagem prevista:
// sortear o nonce uma vez, em New.
func TestCadaRespostaTemNonceNovo(t *testing.T) {
	h := webui.New(distDeTeste())
	vistos := map[string]bool{}
	for range 50 {
		m := nonceDaCSP.FindStringSubmatch(pedir(t, h, "GET", "/").Header.Get("Content-Security-Policy"))
		if m == nil {
			t.Fatal("CSP sem nonce")
		}
		if vistos[m[1]] {
			t.Fatalf("nonce repetido entre respostas: %s", m[1])
		}
		vistos[m[1]] = true
	}
}

func TestHeadNaoTemCorpo(t *testing.T) {
	h := webui.New(distDeTeste())
	for _, c := range []string{"/", "/assets/index-abc123.js"} {
		r := pedir(t, h, "HEAD", c)
		if r.StatusCode != 200 || corpo(t, r) != "" || r.Header.Get("Content-Length") == "" {
			t.Errorf("HEAD %s: status %d, Content-Length %q", c, r.StatusCode, r.Header.Get("Content-Length"))
		}
	}
}

func TestGzipQuandoOClienteAceita(t *testing.T) {
	h := webui.New(distDeTeste())
	r := pedir(t, h, "GET", "/assets/index-abc123.js", "Accept-Encoding", "br, gzip")
	if r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("sem gzip: Content-Encoding %q, Vary %q", r.Header.Get("Content-Encoding"), r.Header.Get("Vary"))
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil || string(b) != bundleGrande {
		t.Fatalf("gzip não devolve o arquivo (%v)", err)
	}

	for _, ae := range []string{"", "br", "gzip;q=0", "identity"} {
		r := pedir(t, h, "GET", "/assets/index-abc123.js", "Accept-Encoding", ae)
		if r.Header.Get("Content-Encoding") != "" || corpo(t, r) != bundleGrande {
			t.Errorf("Accept-Encoding %q recebeu gzip", ae)
		}
	}
	// Arquivo pequeno não compensa: sai cru mesmo com gzip aceito.
	r = pedir(t, h, "GET", "/assets/index-def456.css", "Accept-Encoding", "gzip")
	if r.Header.Get("Content-Encoding") != "" {
		t.Error("arquivo de 14 bytes saiu em gzip")
	}
}

// go build sem npm run build: o dist só tem o .gitkeep. O painel responde
// 503 dizendo o que fazer, e os assets continuam 404 (e não 503: não há
// asset nenhum).
func TestSemBuildResponde503(t *testing.T) {
	h := webui.New(fstest.MapFS{".gitkeep": {}})
	r := pedir(t, h, "GET", "/targets")
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(corpo(t, r), "npm run build") {
		t.Fatalf("status %d", r.StatusCode)
	}
	if r := pedir(t, h, "GET", "/assets/index-abc123.js"); r.StatusCode != http.StatusNotFound {
		t.Errorf("asset sem build: status %d, queria 404", r.StatusCode)
	}
}

// Um build feito sem html.cspNonce produziria um painel cujo gráfico some.
// Melhor 503 explícito que isso.
func TestIndexSemMarcadorResponde503(t *testing.T) {
	d := distDeTeste()
	d["index.html"] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(indexDeTeste, webui.Marcador, "fixo"))}
	r := pedir(t, webui.New(d), "GET", "/")
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(corpo(t, r), "marcador") {
		t.Fatalf("status %d", r.StatusCode)
	}
}

// O dist embutido de verdade: com só o .gitkeep (go test sem build) ou com
// um build completo, todo arquivo dele tem de ter tipo na tabela. É a guarda
// que o teste de .woff2 acima não prova sozinho: numa máquina com
// /etc/mime.types, trocar a tabela por mime.TypeByExtension continuaria
// verde lá, e ficaria vermelho aqui assim que o build tivesse uma fonte.
func TestTodoArquivoDoDistTemTipo(t *testing.T) {
	dist := webui.Dist()
	n := 0
	err := fs.WalkDir(dist, ".", func(nome string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(nome, ".") {
			return err
		}
		n++
		if webui.TipoDe(nome) == "application/octet-stream" {
			t.Errorf("%s sem tipo na tabela do painel", nome)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		// Sem build, o teste não confere nada. O CI que roda depois do
		// "npm run build" liga VIGILUME_EXIGIR_DIST=1 (Plano 7): lá, um dist
		// vazio é erro, e não um verde vazio.
		if os.Getenv("VIGILUME_EXIGIR_DIST") == "1" {
			t.Fatal("VIGILUME_EXIGIR_DIST=1 e o dist embutido está vazio: rode npm run build em web/ antes")
		}
		t.Log("dist sem build (só o .gitkeep): nada a conferir")
		return
	}
	idx, err := fs.ReadFile(dist, "index.html")
	if err != nil || !bytes.Contains(idx, []byte(webui.Marcador)) {
		t.Errorf("o dist embutido não tem index.html com o marcador (%v)", err)
	}
}
```

Crie `internal/httpapi/painel_integration_test.go`:

```go
package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
)

// indexFalso é o bastante para reconhecer o painel numa resposta.
const indexFalso = `<!doctype html><meta property="csp-nonce" nonce="VIGILUME_CSP_NONCE">` +
	`<div id="root"></div>`

func comPainel(d *httpapi.Deps) {
	d.Painel = fstest.MapFS{
		"index.html":          {Data: []byte(indexFalso)},
		"assets/index-a1b.js": {Data: []byte("export {}")},
	}
}

// O wiring, e não a peça: cada prefixo cai na cadeia dele, pelo Routes()
// inteiro. Os testes de middleware do Plano 1 provaram as peças e deixaram
// o arquivo de rotas sem trava (lição de teste do projeto); este teste
// existe para que tirar uma linha da raiz fique vermelho.
func TestCadaPrefixoCaiNaCadeiaCerta(t *testing.T) {
	a := newAPICom(t, func(c *config.Config) { c.PublicBaseURL = "https://vigilume.exemplo.com" }, comPainel)

	casos := []struct {
		caminho string
		status  int
		painel  bool
		tipo    string
	}{
		// A API continua respondendo como API, inclusive onde não há rota:
		// um /api/ que caísse no painel devolveria o index com 200.
		{"/api/v1/nao-existe", http.StatusNotFound, false, "text/plain; charset=utf-8"},
		{"/api/v1/me", http.StatusUnauthorized, false, "application/json; charset=utf-8"},
		{"/healthz", http.StatusOK, false, ""},
		{"/readyz", http.StatusOK, false, ""},
		// A página pública e a verificação têm as páginas delas.
		{"/p/slug-que-nao-existe", http.StatusNotFound, false, "text/html; charset=utf-8"},
		{"/verify/token-que-nao-existe", http.StatusNotFound, false, "text/html; charset=utf-8"},
		// O resto é o painel.
		{"/", http.StatusOK, true, "text/html; charset=utf-8"},
		{"/targets/0190a1b2", http.StatusOK, true, "text/html; charset=utf-8"},
		{"/assets/index-a1b.js", http.StatusOK, false, "text/javascript; charset=utf-8"},
	}
	for _, c := range casos {
		t.Run(c.caminho, func(t *testing.T) {
			resp, err := http.Get(a.srv.URL + c.caminho)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, queria %d (%s)", resp.StatusCode, c.status, b)
			}
			if c.tipo != "" && resp.Header.Get("Content-Type") != c.tipo {
				t.Errorf("Content-Type = %q, queria %q", resp.Header.Get("Content-Type"), c.tipo)
			}
			if ehPainel := strings.Contains(string(b), `<div id="root">`); ehPainel != c.painel {
				t.Errorf("é o painel = %v, queria %v", ehPainel, c.painel)
			}
			if c.painel {
				csp := resp.Header.Get("Content-Security-Policy")
				if !strings.Contains(csp, "style-src 'self' 'nonce-") {
					t.Errorf("o painel saiu sem a CSP dele: %q", csp)
				}
				// SecurityHeaders também está na cadeia do painel.
				if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Strict-Transport-Security") == "" {
					t.Error("o painel saiu sem os cabeçalhos de segurança")
				}
			}
		})
	}
}

// A cadeia do painel não tem CORS nem exige X-Requested-With: é GET de
// arquivo. E a API continua exigindo o cabeçalho nas mutações, agora
// montada em /api/.
func TestAMutacaoDaAPIContinuaExigindoOCabecalho(t *testing.T) {
	a := newAPICom(t, nil, comPainel)
	resp, err := http.Post(a.srv.URL+"/api/v1/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST sem X-Requested-With: status %d, queria 403", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/crypto/ ./internal/webui/`
Expected: FAIL: `undefined: crypto.NewCSPNonce` e o pacote `webui` sem código Go.

- [ ] **Step 3: O nonce compartilhado**

Crie `internal/crypto/nonce.go`:

```go
package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// NewCSPNonce sorteia o nonce de uma resposta com CSP: 128 bits de
// crypto/rand, em base64 de URL sem padding.
//
// A página pública (/p/{slug}) e o painel usam esta mesma função. Duas
// cópias de gerador de nonce divergem, e o defeito que já aconteceu foi
// exatamente num gerador: com a base64 padrão, o "+" sai do html/template
// como "&#43;", e o atributo deixa de trazer o nonce literal da CSP (D26 do
// Plano 4). A gramática de nonce da CSP aceita "-" e "_", e nenhum dos dois
// precisa de escape em atributo HTML.
func NewCSPNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
```

Aplique em `internal/httpapi/publicpage.go`:

```diff
--- a/internal/httpapi/publicpage.go
+++ b/internal/httpapi/publicpage.go
@@ -3,9 +3,7 @@
 import (
 	"bytes"
 	"context"
-	"crypto/rand"
 	"embed"
-	"encoding/base64"
 	"errors"
 	"fmt"
 	"html/template"
@@ -17,6 +15,7 @@
 	"time"
 
 	"github.com/BipoXDXD/vigilume/internal/config"
+	"github.com/BipoXDXD/vigilume/internal/crypto"
 	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
 	"github.com/BipoXDXD/vigilume/internal/observability"
 	"github.com/BipoXDXD/vigilume/internal/statuspage"
@@ -75,7 +74,7 @@
 		return
 	}
 
-	nonce, err := novoNonce()
+	nonce, err := crypto.NewCSPNonce()
 	if err != nil {
 		slog.Error("nonce da status page", "erro", err)
 		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
@@ -108,19 +107,6 @@
 		`<h1>Status indisponível</h1><p>Tente de novo em instantes.</p></html>`
 )
 
-// novoNonce usa base64 de URL, sem padding: a gramática de nonce da CSP
-// aceita "-" e "_", e o html/template não escapa nenhum dos dois. Com a
-// base64 padrão, o "+" sairia como "&#43;" no atributo, e o HTML deixaria de
-// trazer o nonce literal da CSP (defeito achado na execução: o teste falhava
-// sempre que o nonce sorteado tinha um "+").
-func novoNonce() (string, error) {
-	b := make([]byte, 16)
-	if _, err := rand.Read(b); err != nil {
-		return "", err
-	}
-	return base64.RawURLEncoding.EncodeToString(b), nil
-}
-
 // O modelo de apresentação: só strings prontas. O template não formata,
 // não decide e não tem acesso a nada além disto.
 type paginaModelo struct {
```

O teste do nonce da página pública mudou de casa (agora é `TestCSPNonceNeedsNoEscaping`, em `internal/crypto`). Aplique em `internal/httpapi/publicpage_test.go`:

```diff
--- a/internal/httpapi/publicpage_test.go
+++ b/internal/httpapi/publicpage_test.go
@@ -3,7 +3,6 @@
 import (
 	"bytes"
 	"fmt"
-	"regexp"
 	"strings"
 	"testing"
 	"time"
@@ -130,17 +129,3 @@
 		}
 	}
 }
-
-// O nonce vai para o atributo do <style> e para o cabeçalho da CSP, e os
-// dois têm de ser o mesmo texto. Um caractere que o html/template escapa
-// ("+" vira "&#43;") quebraria isso só às vezes, conforme o sorteio: por
-// isso o teste sorteia mil.
-func TestNonceNeedsNoEscaping(t *testing.T) {
-	seguro := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
-	for range 1000 {
-		n, err := novoNonce()
-		if err != nil || !seguro.MatchString(n) {
-			t.Fatalf("nonce %q (%v): queria 22 caracteres de base64 de URL", n, err)
-		}
-	}
-}
```

- [ ] **Step 4: O handler**

Crie o arquivo vazio `internal/webui/dist/.gitkeep`:

```bash
mkdir -p internal/webui/dist && touch internal/webui/dist/.gitkeep
```

Crie `internal/webui/webui.go`:

```go
// Package webui serve o painel React (web/) embutido no binário, na mesma
// origem da API: o cookie de refresh é SameSite=Strict com Path=/api/v1/auth,
// e o painel precisa estar no mesmo site (contrato dos Planos 5, 6 e 7).
//
// Este pacote só sabe servir arquivos. A cadeia de middlewares (RequestID,
// Recover, SecurityHeaders) e a montagem na raiz ficam em httpapi/routes.go.
package webui

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// Marcador é o valor de html.cspNonce em web/vite.config.ts. O Vite o põe
// nos <script> e <link> do index.html e num <meta property="csp-nonce">; o
// handler troca cada ocorrência pelo nonce da resposta.
const Marcador = "VIGILUME_CSP_NONCE"

// O dist inteiro, com o prefixo all: por dois motivos. Antes do primeiro
// build, dist/ só tem o .gitkeep, e sem all: o compilador recusa o diretório
// ("contains no embeddable files"). E o Rolldown pode gerar chunks cujo nome
// começa com "_", que o embed sem all: descartaria em silêncio.
//
//go:embed all:dist
var dist embed.FS

// Dist é o painel embutido, com dist/ como raiz.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" é um literal válido; só falha com o embed quebrado
	}
	return sub
}

const (
	cacheImutavel = "public, max-age=31536000, immutable"
	cacheRevalida = "no-cache"
	cacheNenhum   = "no-store"
	// gzipMinimo: abaixo disso o cabeçalho do gzip come o ganho.
	gzipMinimo = 1 << 10
)

// tipos é a tabela de MIME do painel. Não é mime.TypeByExtension: a tabela
// embutida do Go não tem .woff2, .webmanifest nem .map, e o que completa a
// lista numa máquina de desenvolvimento é o /etc/mime.types, que a imagem
// distroless não tem. Extensão fora daqui sai como application/octet-stream,
// que com nosniff nunca executa.
var tipos = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".html":        "text/html; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/vnd.microsoft.icon",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

// comprimiveis são os tipos que o gzip reduz; imagens e fontes já vêm
// comprimidas.
var comprimiveis = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".html": true, ".json": true,
	".map": true, ".webmanifest": true, ".svg": true, ".txt": true,
}

// TipoDe devolve o Content-Type que o painel usa para um nome de arquivo.
func TipoDe(nome string) string {
	if t, ok := tipos[strings.ToLower(path.Ext(nome))]; ok {
		return t
	}
	return "application/octet-stream"
}

type arquivo struct {
	corpo []byte
	gz    []byte // nil quando não compensa
	tipo  string
}

// Painel é o handler. Tudo é lido e comprimido uma vez, em New: servir é só
// escrever bytes que já estão em memória.
type Painel struct {
	arquivos map[string]arquivo // chave sem a barra inicial: "assets/index-x.js"
	index    []byte             // o index.html com o marcador
	// indisponivel, quando não é vazio, é o motivo de o painel responder
	// 503 no lugar do index: build ausente ou build sem o marcador.
	indisponivel string
}

// New lê o painel de fsys. Nunca falha: um dist sem index.html (go build
// sem npm run build) ou sem o marcador vira um painel que responde 503, e a
// API continua de pé.
func New(fsys fs.FS) *Painel {
	p := &Painel{arquivos: map[string]arquivo{}}
	err := fs.WalkDir(fsys, ".", func(nome string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || escondido(nome) {
			return err
		}
		corpo, err := fs.ReadFile(fsys, nome)
		if err != nil {
			return err
		}
		a := arquivo{corpo: corpo, tipo: TipoDe(nome)}
		if comprimiveis[strings.ToLower(path.Ext(nome))] && len(corpo) >= gzipMinimo {
			a.gz = comprimir(corpo)
		}
		p.arquivos[nome] = a
		return nil
	})
	if err != nil {
		slog.Error("painel: ler os arquivos embutidos", "erro", err)
		p.indisponivel = "O painel não pôde ser lido."
		return p
	}
	idx, ok := p.arquivos["index.html"]
	switch {
	case !ok:
		slog.Warn("painel ausente: o binário foi compilado sem npm run build em web/")
		p.indisponivel = "O painel não foi incluído neste binário (rode npm run build em web/ antes do go build)."
	case !bytes.Contains(idx.corpo, []byte(Marcador)):
		// Servir este index seria servir um painel cujo gráfico some: sem
		// nonce, os <style> que o Radix e o shadcn injetam são bloqueados.
		slog.Error("painel inválido: index.html sem o marcador de nonce (html.cspNonce no vite.config.ts)")
		p.indisponivel = "O painel foi compilado sem o marcador de nonce; refaça o build."
	default:
		p.index = idx.corpo
	}
	return p
}

func comprimir(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression) // nível válido: não falha
	_, _ = zw.Write(b)                                       // bytes.Buffer não falha
	_ = zw.Close()
	if buf.Len() >= len(b) {
		return nil
	}
	return buf.Bytes()
}

// escondido: algum segmento começa com ".". New não carrega esses arquivos,
// e é isso que tira o .gitkeep (e qualquer arquivo oculto que um build venha
// a deixar) do que o painel serve: o que não está no mapa é 404.
func escondido(nome string) bool {
	for _, seg := range strings.Split(nome, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return true
		}
	}
	return false
}

func (p *Painel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		texto(w, http.StatusMethodNotAllowed, "Método não permitido.")
		return
	}
	caminho := path.Clean("/" + r.URL.Path)
	nome := strings.TrimPrefix(caminho, "/")
	switch {
	case nome == "index.html":
		// O arquivo cru traz o marcador no lugar do nonce: nunca sai assim.
		p.servirIndex(w, r)
	case strings.HasPrefix(nome, "assets/"):
		// Um asset que não existe é 404, e nunca o index: depois de um
		// deploy, uma aba antiga pede o chunk velho, e o HTML no lugar dele
		// seria barrado pelo nosniff sem deixar pista. Com 404, o import()
		// falha e o painel oferece recarregar.
		p.servirArquivo(w, r, nome, cacheImutavel)
	case path.Ext(nome) != "":
		p.servirArquivo(w, r, nome, cacheRevalida)
	default:
		// Fallback de SPA: todo caminho sem extensão é rota do painel. A
		// API, a página pública e a verificação nem chegam aqui: o ServeMux
		// da raiz as manda para as cadeias delas.
		p.servirIndex(w, r)
	}
}

func (p *Painel) servirIndex(w http.ResponseWriter, r *http.Request) {
	if p.indisponivel != "" {
		texto(w, http.StatusServiceUnavailable, p.indisponivel)
		return
	}
	nonce, err := crypto.NewCSPNonce()
	if err != nil {
		slog.Error("painel: sortear o nonce", "erro", err)
		texto(w, http.StatusServiceUnavailable, "Painel indisponível.")
		return
	}
	corpo := bytes.ReplaceAll(p.index, []byte(Marcador), []byte(nonce))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", Politica(nonce))
	// no-store, e não no-cache: o corpo traz o nonce desta resposta. Num
	// cache compartilhado, todos receberiam o mesmo nonce.
	h.Set("Cache-Control", cacheNenhum)
	h.Set("Content-Length", strconv.Itoa(len(corpo)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(corpo)
	}
}

// Politica é a CSP do painel (spec §5.7, spec do Plano 6 §5.3). O nonce vale
// só para estilo: não há script inline, e nonce em script-src seria uma porta
// a mais. Sem style-src-attr: React, Radix e Recharts escrevem estilo pelo
// CSSOM, que a CSP não governa; só <style> injetado precisa do nonce.
func Politica(nonce string) string {
	return "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}

func (p *Painel) servirArquivo(w http.ResponseWriter, r *http.Request, nome, cache string) {
	a, ok := p.arquivos[nome]
	if !ok {
		texto(w, http.StatusNotFound, "Arquivo não encontrado.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.tipo)
	h.Set("Cache-Control", cache)
	corpo := a.corpo
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if aceitaGzip(r.Header.Get("Accept-Encoding")) {
			h.Set("Content-Encoding", "gzip")
			corpo = a.gz
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(corpo)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(corpo)
	}
}

// aceitaGzip lê Accept-Encoding sem cerimônia: "gzip" presente e não
// recusado com q=0.
func aceitaGzip(v string) bool {
	for _, parte := range strings.Split(v, ",") {
		nome, params, _ := strings.Cut(strings.TrimSpace(parte), ";")
		if !strings.EqualFold(strings.TrimSpace(nome), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

func texto(w http.ResponseWriter, status int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", cacheNenhum)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}
```

- [ ] **Step 5: A raiz e o `.gitignore`**

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -2,9 +2,11 @@
 
 import (
 	"context"
+	"io/fs"
 	"log/slog"
 	"net/http"
 	"strings"
+	"sync"
 	"time"
 
 	"github.com/BipoXDXD/vigilume/internal/account"
@@ -17,6 +19,7 @@
 	"github.com/BipoXDXD/vigilume/internal/notify"
 	"github.com/BipoXDXD/vigilume/internal/statuspage"
 	"github.com/BipoXDXD/vigilume/internal/targets"
+	"github.com/BipoXDXD/vigilume/internal/webui"
 )
 
 // maxBodyBytes cobre com folga qualquer payload legítimo desta API.
@@ -49,6 +52,9 @@
 	Clock       clock.Clock
 	// Ready é o teste de prontidão de /readyz: o ping no banco em produção.
 	Ready func(context.Context) error
+	// Painel são os arquivos do painel React. Nil é o embutido no binário
+	// (webui.Dist); os testes injetam um fstest.MapFS.
+	Painel fs.FS
 }
 
 // Routes monta a árvore de rotas e a cadeia de middlewares.
@@ -178,13 +184,44 @@
 	handler = middleware.Recover(handler)
 	handler = middleware.RequestID(handler)
 
+	// A raiz separa as cadeias pelo prefixo, e o ServeMux escolhe sempre o
+	// padrão mais específico: o painel, montado em "/", nunca vê /api/, /p/
+	// nem /verify/. Antes do painel, "/" era a API; agora a API tem de ser
+	// nomeada, inclusive os dois health checks, que ficam fora de /api/.
 	raiz := http.NewServeMux()
+	raiz.Handle("/api/", handler)
+	raiz.Handle("/healthz", handler)
+	raiz.Handle("/readyz", handler)
 	raiz.Handle("/verify/", verificacao(ctx, cfg, d.Channels))
 	raiz.Handle("/p/", paginaPublica(ctx, cfg, d.StatusPages))
-	raiz.Handle("/", handler)
+	raiz.Handle("/", painel(d.Painel))
 	return raiz
 }
 
+// painel é a cadeia do painel React. Só serve GET e HEAD de arquivos que já
+// estão em memória: sem CORS, sem RequireCustomHeader, sem BodyLimit e sem
+// rate limit — dividir o balde da API com o carregamento do painel geraria
+// 429 falso. SecurityHeaders põe HSTS, nosniff e companhia; o handler troca a
+// CSP e o Cache-Control pelos dele, como a página pública.
+func painel(arquivos fs.FS) http.Handler {
+	var h http.Handler
+	if arquivos == nil {
+		h = painelEmbutido()
+	} else {
+		h = webui.New(arquivos)
+	}
+	h = middleware.SecurityHeaders(h)
+	h = middleware.Recover(h)
+	h = middleware.RequestID(h)
+	return h
+}
+
+// painelEmbutido lê e comprime o dist embutido uma vez por processo. O New
+// lê tudo e comprime no boot (D8 do plano); refazer isso a cada Routes()
+// custava ~70 s na suíte de integração com -race e um dist real, que monta
+// Routes() em quase todo teste.
+var painelEmbutido = sync.OnceValue(func() *webui.Painel { return webui.New(webui.Dist()) })
+
 // readyzTimeout limita o ping: um banco que não responde tem de virar 503
 // rápido, e não segurar o health check do orquestrador.
 const readyzTimeout = 2 * time.Second
```

Aplique em `.gitignore`:

```diff
--- a/.gitignore
+++ b/.gitignore
@@ -1,7 +1,13 @@
 /vigilume
 /bin/
 /web/node_modules/
-/web/dist/
+/web/test-results/
+/web/playwright-report/
+/web/e2e/.tmp/
+# O build do painel sai aqui (vite.config.ts) e é embutido pelo binário; só
+# o .gitkeep é versionado, para o //go:embed all:dist compilar sem build.
+/internal/webui/dist/*
+!/internal/webui/dist/.gitkeep
 .env
 *.test
 coverage.out
```

- [ ] **Step 6: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/crypto/ ./internal/webui/ ./internal/httpapi/`
Expected: PASS. `TestTodoArquivoDoDistTemTipo` registra "dist sem build (só o .gitkeep): nada a conferir".

Run: `VIGILUME_EXIGIR_DIST=1 go test -count=1 -run TestTodoArquivoDoDistTemTipo ./internal/webui/`
Expected: FAIL: "VIGILUME_EXIGIR_DIST=1 e o dist embutido está vazio" — é o verde vazio que a variável recusa (a Task 2 roda o mesmo comando depois do build, e aí passa).

Run: `git status --porcelain internal/webui/dist`
Expected: `?? internal/webui/dist/.gitkeep` (a exceção do `.gitignore` funciona).

- [ ] **Step 7: Sabotagem**

Uma de cada vez; rode o teste indicado; restaure:
1. Em `routes.go`, apague `raiz.Handle("/api/", handler)`. Run: `go test -count=1 -run TestCadaPrefixo ./internal/httpapi/`. Expected: FAIL — `/api/v1/nao-existe` e `/api/v1/me` respondem 200 com o index do painel.
2. Em `routes.go`, apague `raiz.Handle("/healthz", handler)`. Expected: FAIL em `TestCadaPrefixo` ("é o painel = true").
3. Em `painel()`, apague `h = middleware.SecurityHeaders(h)`. Expected: FAIL em `TestCadaPrefixo` ("o painel saiu sem os cabeçalhos de segurança").
4. Em `servirIndex`, troque a linha do nonce por `nonce, err := crypto.NewCSPNonce(); nonce = "AAAAAAAAAAAAAAAAAAAAAA"`. Run: `go test -count=1 ./internal/webui/`. Expected: FAIL em `TestCadaRespostaTemNonceNovo` ("nonce repetido entre respostas"). (Trocar a chamada inteira por um literal não compila — o import de `crypto` sobra — e não prova nada.)
5. Troque `bytes.ReplaceAll(p.index, …)` por `bytes.Replace(p.index, …, 1)`. Expected: FAIL em `TestIndexLevaONonceDaCSP` ("aparece 1 vezes no corpo, queria 3").
6. No `switch` de `ServeHTTP`, troque `case nome == "index.html":` por `case false:`. Expected: FAIL em `TestRoteamentoDoPainel` ("o marcador saiu no corpo").
7. No `case strings.HasPrefix(nome, "assets/"):`, troque `p.servirArquivo(w, r, nome, cacheImutavel)` por `if _, ok := p.arquivos[nome]; !ok { p.servirIndex(w, r); return }; p.servirArquivo(w, r, nome, cacheImutavel)`. Expected: FAIL ("asset ausente é 404, nunca o index": status 200).
8. Em `New`, tire `|| escondido(nome)`. Expected: FAIL (`/.gitkeep` responde 200).
9. Apague a linha `".woff2"` da tabela `tipos`. Expected: FAIL (`application/octet-stream`).
10. Em `New`, troque `case !bytes.Contains(idx.corpo, []byte(Marcador)):` por `case false:`. Expected: FAIL em `TestIndexSemMarcadorResponde503` (status 200).
11. Em `aceitaGzip`, troque `return q != "q=0" && …` por `return true`. Expected: FAIL em `TestGzipQuandoOClienteAceita` (`gzip;q=0` recebeu gzip).

- [ ] **Step 8: Commit**

```bash
git add internal/crypto/nonce.go internal/crypto/nonce_test.go internal/webui/ internal/httpapi/publicpage.go \
  internal/httpapi/publicpage_test.go internal/httpapi/routes.go internal/httpapi/painel_integration_test.go .gitignore
git commit -m "feat(webui): o binário serve o painel, com CSP de nonce, cache imutável e fallback de SPA"
```

---

### Task 2: O projeto `web/` e o núcleo da sessão

O projeto do painel e a camada que toda tela vai usar: o transporte (`http.ts`), a sessão (`sessao.ts`), a regra do 401 com as funções de cada rota (`cliente.ts`), os schemas das respostas (`schemas.ts`) e as mensagens. É o risco maior do plano, provado antes de existir tela: o refresh sob concorrência, dentro da aba e entre abas, e a regra do 401. O `main.tsx` desta tarefa é provisório (a árvore chega na Task 3); ele existe para o build gerar um dist de verdade, que o teste Go da Task 1 passa a conferir.

**Files:**
- Create: `web/package.json`, `web/tsconfig.json`, `web/tsconfig.app.json`, `web/tsconfig.node.json`, `web/vite.config.ts`, `web/.oxlintrc.json`
- Create: `web/index.html`, `web/public/tema.js`, `web/public/favicon.svg`, `web/src/index.css`, `web/src/main.tsx` (provisório)
- Create: `web/src/lib/nonce.ts`, `web/src/lib/utils.ts`, `web/src/lib/format.ts`
- Create: `web/src/lib/api/http.ts`, `schemas.ts`, `sessao.ts`, `cliente.ts`, `mensagens.ts`, `index.ts`
- Create: `web/src/test/setup.ts`, `web/src/test/ambiente.ts`, `web/src/test/api-falsa.ts`
- Test: `web/src/lib/format.test.ts`, `web/src/lib/api/sessao.test.ts`, `web/src/lib/api/cliente.test.ts`
- Generated: `web/package-lock.json` (pelo `npm install`)

**Interfaces:**
- Consumes: `webui.Marcador` (Task 1) — o mesmo texto em `html.cspNonce`.
- Produces:
  - `http.ts`: `class ApiError { status; code; fields: ErroDeCampo[]; traceId }`, `enviar(buscar, Pedido)`, `lerErro(Response)`, `lerCorpo(Response, esquema)` (JSON quebrado ou fora do schema vira `ApiError` `unexpected`), `type Buscar`.
  - `sessao.ts`: `criarSessao({ buscar, travar?, canal?, prazoMs? })` → `{ token(), estado(), motivo(), assinar(fn), iniciar(), tentarDeNovo(), renovar(usado), entrar(email, senha), verificarTotp(desafio, codigo), sair(), encerrar(motivo) }`; `type Estado = 'verificando' | 'autenticado' | 'anonimo' | 'offline'`; `type Motivo = 'saiu' | 'expirou' | 'encerrada-aqui' | 'outra-aba'`; `travaDoNavegador()`, `canalDoNavegador()`.
  - `cliente.ts`: `criarApi(sessao, buscar)` → `{ me, resumo, alvos, alvo, criarAlvo, alterarAlvo, apagarAlvo, serie, incidentes, reconhecer, canais, criarCanal, apagarCanal, reenviarVerificacao, paginas, criarPagina, alterarPagina, sessoes, encerrarSessao, trocarSenha, totpCadastrar, totpConfirmar, totpDesativar }`; os tipos `EntradaAlvo`, `AlteracaoAlvo`, `EntradaCanal`, `EntradaPagina`, `TrocaDeSenha`.
  - `schemas.ts`: os schemas Zod de cada resposta (`Alvo`, `ItemDoResumo`, `Serie`, `Incidente`, `Canal`, `PaginaDeStatus`, `Sessao`, `Me`…) e os tipos inferidos.
  - `mensagens.ts`: `mensagemDe(e)`, `mensagemDoCampo(campo, motivo)`, `textoDoErroDeCheck(kind, statusCode)`, `textoDoErroDeCanal(motivo)`.
  - `format.ts`: `porcento`, `milissegundos`, `duracao`, `haQuanto`, `dataHora`, `fuso`, `pontosDeCodigo`, `navegadorDe`.
  - `index.ts`: as instâncias `sessao` e `api` do navegador.
  - Testes: `usarApiFalsa()`, `novaSessao({ travar?, canal?, buscar? })`, `travaFalsa()`, `buscarDoTeste`; a API falsa exporta `EMAIL`, `SENHA`, `TOTP_OK`, `RECUPERACAO_OK`, e o estado dela tem `recuperacaoGasta` e `trocaFalhaDepoisDoCodigo` (a troca de senha que gasta o código de recuperação e depois falha, da Task 10).

**Contexto para quem implementa.** A API falsa (`test/api-falsa.ts`) é a peça que impede os testes de cliente de passarem vacuamente: ela recusa mutação sem `X-Requested-With` (403), rota protegida sem Bearer válido (401 `unauthorized`), imita as guardas da Task 11 do Plano 5 (um access token válido de **família morta** recebe 401 `unauthorized` em `/auth/password`, `/auth/totp/*` e `/sessions`; trocar a senha apaga o cadastro de 2FA pendente e invalida o desafio de login em aberto), e o refresh dela **rotaciona e detecta reúso** como `auth.Manager.Rotate`: o "pote de cookies" é lido no início da requisição e gravado no fim, depois de um atraso configurável — é o que deixa duas renovações se sobreporem como num navegador de verdade. O boot tem duas guardas para a mesma propriedade (a promessa única e o *single-flight*): tirar só uma deixa o teste verde, e o comentário no teste diz isso. `z.config({ jitless: true })` (D1) não tem teste de Vitest — o jsdom não aplica CSP —; quem o prova é o E2E da Task 11. Em `cliente.ts`, nada em `/auth/*` renova, nem com `unauthorized` (D2): senha ou código errado é `invalid_credentials` (D24 do Plano 5), e `unauthorized` ali é token vencido (ou família morta), que a tela de configurações trata guardando o formulário e oferecendo "Renovar acesso e tentar de novo" (Task 10). Nunca uma exceção crua: resposta de erro que não é o envelope, JSON quebrado ou resposta fora do schema viram `ApiError` `unexpected` com o status (spec do Plano 6 §3.4). A sessão tem uma **época** (`epoca`), que muda a cada login e a cada fim de sessão: um refresh que saiu numa época e voltou em outra não mexe no token — sem isso, sair com um refresh em voo ressuscitava o token. Os testes do canal entre abas provam ausência sem relógio: depois do que tem de estar ausente, uma **sentinela** vai pelo mesmo canal, e quando ela chega tudo o que foi postado antes já chegou.

- [ ] **Step 1: O projeto**

Crie `web/package.json`:

```json
{
  "name": "vigilume-painel",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "engines": {
    "node": ">=22.22"
  },
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "typecheck": "tsc -b",
    "lint": "oxlint --deny-warnings",
    "test": "vitest run",
    "e2e": "playwright test"
  },
  "dependencies": {
    "@hookform/resolvers": "5.9.1",
    "@tanstack/react-query": "5.103.2",
    "class-variance-authority": "0.7.1",
    "clsx": "2.1.1",
    "get-nonce": "1.0.1",
    "lucide-react": "1.48.0",
    "qrcode.react": "4.2.0",
    "radix-ui": "1.6.7",
    "react": "19.3.0",
    "react-dom": "19.3.0",
    "react-hook-form": "7.88.0",
    "react-router": "8.4.0",
    "recharts": "3.10.1",
    "tailwind-merge": "3.7.0",
    "zod": "4.6.5"
  },
  "devDependencies": {
    "@axe-core/playwright": "4.13.0",
    "@playwright/test": "1.63.0",
    "@tailwindcss/vite": "4.3.3",
    "@testing-library/dom": "10.4.2",
    "@testing-library/jest-dom": "7.0.1",
    "@testing-library/react": "16.3.3",
    "@testing-library/user-event": "14.6.7",
    "@types/node": "24.13.6",
    "@types/react": "19.3.0",
    "@types/react-dom": "19.3.0",
    "@vitejs/plugin-react": "6.1.1",
    "jsdom": "30.1.1",
    "msw": "2.15.0",
    "oxlint": "1.85.0",
    "tailwindcss": "4.3.3",
    "typescript": "6.0.3",
    "vite": "8.3.1",
    "vitest": "5.0.1"
  }
}
```

Crie `web/tsconfig.json`:

```json
{
  "files": [],
  "references": [{ "path": "./tsconfig.app.json" }, { "path": "./tsconfig.node.json" }]
}
```

Crie `web/tsconfig.app.json`:

```json
{
  "compilerOptions": {
    "tsBuildInfoFile": "./node_modules/.tmp/tsconfig.app.tsbuildinfo",
    "target": "es2023",
    "lib": ["ES2023", "DOM", "DOM.Iterable"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "types": ["vite/client", "@testing-library/jest-dom/vitest"],
    "jsx": "react-jsx",
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "verbatimModuleSyntax": true,
    "erasableSyntaxOnly": true,
    "allowImportingTsExtensions": true,
    "moduleDetection": "force",
    "skipLibCheck": true,
    "noEmit": true,
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["src"]
}
```

Crie `web/tsconfig.node.json`:

```json
{
  "compilerOptions": {
    "tsBuildInfoFile": "./node_modules/.tmp/tsconfig.node.tsbuildinfo",
    "target": "es2023",
    "lib": ["ES2023", "DOM"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "types": ["node"],
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "verbatimModuleSyntax": true,
    "allowImportingTsExtensions": true,
    "moduleDetection": "force",
    "skipLibCheck": true,
    "noEmit": true,
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["vite.config.ts", "playwright.config.ts", "e2e"]
}
```

Crie `web/vite.config.ts`:

```ts
/// <reference types="vitest/config" />
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

// O build sai dentro do pacote Go que o embute (contrato dos Planos 5–7).
const saida = path.resolve(import.meta.dirname, '../internal/webui/dist')

// emptyOutDir apaga o dist inteiro, inclusive o .gitkeep que mantém o
// diretório versionado para o //go:embed compilar sem build. Sem esvaziar, o
// Vite não limpa um outDir fora da raiz, e os bundles velhos iriam para o
// binário. Este plugin devolve o .gitkeep depois de cada build.
function manterGitkeep(): Plugin {
  return {
    name: 'vigilume:gitkeep',
    apply: 'build',
    closeBundle() {
      mkdirSync(saida, { recursive: true })
      writeFileSync(path.join(saida, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), manterGitkeep()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  // O Vite põe este marcador em <meta property="csp-nonce">, nos <script> e
  // nos <link> do index.html; o handler Go (internal/webui) troca cada
  // ocorrência pelo nonce da resposta. O valor é webui.Marcador.
  html: { cspNonce: 'VIGILUME_CSP_NONCE' },
  // O limite de aviso sobe para o chunk principal (React, Router, Query, Radix,
  // Zod): o Recharts, que era metade do bundle, já saiu dele (lazy).
  build: { outDir: saida, emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 700 },
  server: {
    // Em desenvolvimento, o navegador só vê localhost:5173: o cookie
    // SameSite=Strict de /api/v1/auth funciona sem CORS.
    proxy: { '/api': 'http://localhost:8080' },
  },
  test: {
    environment: 'jsdom',
    environmentOptions: { jsdom: { url: 'http://localhost:5173/' } },
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
})
```

Crie `web/.oxlintrc.json`:

```json
{
  "$schema": "./node_modules/oxlint/configuration_schema.json",
  "plugins": ["react", "typescript", "jsx-a11y", "vitest"],
  "categories": { "correctness": "error", "suspicious": "warn" },
  "rules": {
    "react/react-in-jsx-scope": "off",
    "jsx-a11y/no-autofocus": "off"
  },
  "ignorePatterns": ["node_modules", "playwright-report", "test-results"]
}
```

Instale (gera `web/package-lock.json`):

```bash
cd web && npm install --no-audit --no-fund
```

O npm 11 avisa que o `postinstall` do `msw` não rodou (`install-scripts`): ele só copia o *worker* do navegador, que este projeto não usa (os testes rodam o MSW no Node). Não aprove.

- [ ] **Step 2: A infraestrutura de teste e os testes (falham)**

Crie `web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll } from 'vitest'

// Um servidor MSW para a suíte; cada teste instala a API falsa dele com
// usarApiFalsa(). Requisição sem handler é FALHA, não aviso: uma rota que o
// teste esqueceu de imitar não pode passar em silêncio.
export const servidor = setupServer()

beforeAll(() => servidor.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  servidor.resetHandlers()
  localStorage.clear()
})
afterAll(() => servidor.close())

// O jsdom não tem o que o Radix e o Recharts pedem ao navegador.
class ResizeObserverFalso {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverFalso as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function () {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= function () {}
// O jsdom não implementa isSecureContext; o navegador diz true em
// http://localhost, que é onde os testes "rodam". Os testes do aviso de
// contexto inseguro (login.test.tsx) trocam o valor e o devolvem.
Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
window.matchMedia ??= (query: string) =>
  ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }) as MediaQueryList
```

Crie `web/src/test/api-falsa.ts`:

```ts
import { delay, http, HttpResponse, type HttpHandler } from 'msw'
import type { z } from 'zod'
import type {
  Alvo,
  Canal,
  Check,
  Hora,
  Incidente,
  ItemDoResumo,
  PaginaDeStatus,
  Sessao,
} from '@/lib/api/schemas'

// Uma API falsa, com estado, para o MSW. Ela imita as GUARDAS do servidor, e
// não só as respostas felizes; sem isso, os testes de cliente passariam
// vacuamente (lições de teste do projeto):
// - mutação sem X-Requested-With → 403 forbidden (RequireCustomHeader);
// - rota protegida sem Bearer válido → 401 unauthorized (Authn);
// - /auth/refresh rotaciona o cookie e detecta reúso: um cookie já consumido
//   revoga a família inteira (auth.Manager.Rotate).
//
// O "cookie" é o pote do navegador: um só por teste, como abas do mesmo
// navegador. O refresh lê o pote no INÍCIO da requisição e grava o cookie
// novo no FIM, depois do atraso — é o que deixa duas renovações se
// sobreporem como no navegador de verdade (spike do Plano 6, §8.2).

type Json = Record<string, unknown>

type Entrada<T extends z.ZodType> = z.input<T>

export const EMAIL = 'dono@exemplo.com'
export const SENHA = 'uma senha bem comprida'
export const TOTP_OK = '123456'
export const RECUPERACAO_OK = 'ABCDEFGHIJKLMNOP'

const agora = () => new Date('2026-09-24T12:00:00Z')

export type ApiFalsa = ReturnType<typeof criarApiFalsa>

export function criarApiFalsa() {
  const st = {
    pote: null as string | null,
    consumidos: new Set<string>(),
    familiaRevogada: false,
    familia: 'f-atual',
    seq: 0,
    acessos: new Set<string>(),
    atrasoRefresh: 0,
    totpLigado: false,
    totpPendente: false,
    // Códigos de recuperação já gastos. Com trocaFalhaDepoisDoCodigo, a
    // troca de senha gasta o código e DEPOIS falha com 401 unauthorized, como
    // o servidor quando a família morre no meio da troca (PENDENCIAS do
    // Plano 6: o código é consumido antes da transação).
    recuperacaoGasta: new Set<string>(),
    trocaFalhaDepoisDoCodigo: false,
    // O desafio de 2FA do login ainda vale (a senha não mudou desde ele).
    desafioValido: false,
    chamadas: new Map<string, number>(),
    corpos: new Map<string, Json[]>(),
    alvos: [] as Entrada<typeof Alvo>[],
    resumo: [] as Entrada<typeof ItemDoResumo>[],
    serie: { resolution: 'raw', items: [] } as { resolution: 'raw'; items: Entrada<typeof Check>[] } | {
      resolution: '1h'
      items: Entrada<typeof Hora>[]
    },
    incidentes: [] as Entrada<typeof Incidente>[],
    canais: [] as Entrada<typeof Canal>[],
    paginas: [] as Entrada<typeof PaginaDeStatus>[],
    sessoes: [] as Entrada<typeof Sessao>[],
    // Respostas forçadas por rota ("POST /targets" → 409 etc.).
    forcar: new Map<string, { status: number; code: string; fields?: { field: string; reason: string }[] }>(),
  }

  function contar(chave: string, corpo?: Json) {
    st.chamadas.set(chave, (st.chamadas.get(chave) ?? 0) + 1)
    if (corpo) st.corpos.set(chave, [...(st.corpos.get(chave) ?? []), corpo])
  }

  function erro(status: number, code: string, fields?: { field: string; reason: string }[]) {
    return HttpResponse.json(
      { error: { code, message: 'mensagem do servidor', ...(fields ? { fields } : {}) }, trace_id: 'trace-falso' },
      { status },
    )
  }

  function novoAcesso() {
    st.seq++
    const t = `acesso-${st.seq}`
    st.acessos.add(t)
    return t
  }

  function novoCookie() {
    st.seq++
    return `refresh-${st.seq}`
  }

  function abrirSessao() {
    st.pote = novoCookie()
    st.familiaRevogada = false
    return HttpResponse.json({ access_token: novoAcesso(), expires_in: 900 })
  }

  // As rotas que exigem a família do token viva (Plano 5: /auth/password e
  // /sessions; Task 11 do Plano 5: os três do 2FA). Um access token ainda
  // válido de uma família revogada passa pelo Authn e morre aqui, com 401
  // unauthorized — antes de a rota olhar senha ou código.
  const VIVAS = new Set([
    'POST /auth/password',
    'POST /auth/totp/enroll',
    'POST /auth/totp/confirm',
    'POST /auth/totp/disable',
    'GET /sessions',
    'DELETE /sessions/:id',
  ])

  // Envolve um handler com as guardas do servidor.
  function rota(
    metodo: 'get' | 'post' | 'patch' | 'delete',
    caminho: string,
    fn: (a: { request: Request; params: Record<string, string>; corpo: Json }) => Response | Promise<Response>,
    protegida = true,
  ): HttpHandler {
    return http[metodo](`*/api/v1${caminho}`, async ({ request, params }) => {
      const chave = `${metodo.toUpperCase()} ${caminho}`
      const corpo = request.headers.get('Content-Type')?.includes('json') ? ((await request.json()) as Json) : {}
      contar(chave, corpo)
      if (metodo !== 'get' && request.headers.get('X-Requested-With') !== 'vigilume') return erro(403, 'forbidden')
      if (protegida) {
        const t = request.headers.get('Authorization')?.replace(/^Bearer /, '')
        if (!t || !st.acessos.has(t)) return erro(401, 'unauthorized')
        if (VIVAS.has(chave) && st.familiaRevogada) return erro(401, 'unauthorized')
      }
      const f = st.forcar.get(chave)
      if (f) return erro(f.status, f.code, f.fields)
      return fn({ request, params: params as Record<string, string>, corpo })
    })
  }

  const handlers: HttpHandler[] = [
    rota(
      'post',
      '/auth/login',
      ({ corpo }) => {
        if (corpo.email !== EMAIL || corpo.password !== SENHA) return erro(401, 'invalid_credentials')
        if (st.totpLigado) {
          st.desafioValido = true
          return HttpResponse.json({ totp_required: true, challenge: 'desafio-1' })
        }
        return abrirSessao()
      },
      false,
    ),
    rota(
      'post',
      '/auth/totp/verify',
      ({ corpo }) => {
        if (corpo.challenge !== 'desafio-1') return erro(401, 'unauthorized')
        // Task 11 do Plano 5: a senha mudou depois do desafio → a senha
        // verificada é velha, e o verify responde invalid_credentials.
        if (!st.desafioValido) return erro(401, 'invalid_credentials')
        if (corpo.code !== TOTP_OK && corpo.code !== RECUPERACAO_OK) return erro(401, 'invalid_credentials')
        return abrirSessao()
      },
      false,
    ),
    rota(
      'post',
      '/auth/refresh',
      async () => {
        const apresentado = st.pote
        await delay(st.atrasoRefresh)
        if (!apresentado || st.familiaRevogada) return erro(401, 'invalid_session')
        if (st.consumidos.has(apresentado)) {
          st.familiaRevogada = true
          st.pote = null
          return erro(401, 'invalid_session')
        }
        st.consumidos.add(apresentado)
        st.pote = novoCookie()
        return HttpResponse.json({ access_token: novoAcesso(), expires_in: 900 })
      },
      false,
    ),
    rota(
      'post',
      '/auth/logout',
      () => {
        st.familiaRevogada = true
        st.pote = null
        return new HttpResponse(null, { status: 204 })
      },
      false,
    ),
    rota('get', '/me', () =>
      HttpResponse.json({ id: 'u1', email: EMAIL, totp_enabled: st.totpLigado, created_at: '2026-09-01T10:00:00Z' }),
    ),

    rota('get', '/targets/summary', () => HttpResponse.json({ items: st.resumo })),
    rota('get', '/targets', () => HttpResponse.json({ items: st.alvos })),
    rota('post', '/targets', ({ corpo }) => {
      const alvo = {
        id: `alvo-${st.alvos.length + 1}`,
        name: String(corpo.name),
        url: String(corpo.url),
        method: corpo.method as 'GET',
        interval_sec: Number(corpo.interval_sec),
        timeout_ms: Number(corpo.timeout_ms),
        expected_status: Number(corpo.expected_status),
        degraded_threshold_ms: corpo.degraded_threshold_ms === undefined ? 2000 : (corpo.degraded_threshold_ms as number | null),
        enabled: corpo.enabled !== false,
        header_names: Object.keys((corpo.headers as Json | undefined) ?? {}).sort(),
        created_at: agora().toISOString(),
      }
      st.alvos.push(alvo)
      return HttpResponse.json(alvo, { status: 201 })
    }),
    rota('get', '/targets/:id', ({ params }) => {
      const a = st.alvos.find((x) => x.id === params.id)
      return a ? HttpResponse.json(a) : erro(404, 'not_found')
    }),
    rota('patch', '/targets/:id', ({ params, corpo }) => {
      const a = st.alvos.find((x) => x.id === params.id)
      if (!a) return erro(404, 'not_found')
      const { headers, ...resto } = corpo
      Object.assign(a, resto)
      if (headers !== undefined) a.header_names = Object.keys((headers as Json | null) ?? {}).sort()
      return HttpResponse.json(a)
    }),
    rota('delete', '/targets/:id', ({ params }) => {
      st.alvos = st.alvos.filter((x) => x.id !== params.id)
      st.resumo = st.resumo.filter((x) => x.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('get', '/targets/:id/checks', () => HttpResponse.json(st.serie)),

    rota('get', '/incidents', ({ request }) => {
      const q = new URL(request.url).searchParams
      let itens = st.incidentes
      if (q.get('status') === 'open') itens = itens.filter((i) => i.resolved_at === null)
      if (q.get('status') === 'resolved') itens = itens.filter((i) => i.resolved_at !== null)
      if (q.get('target_id')) itens = itens.filter((i) => i.target_id === q.get('target_id'))
      const limite = Number(q.get('limit') ?? 50)
      const inicio = q.get('cursor') ? itens.findIndex((i) => i.id === q.get('cursor')) + 1 : 0
      const pagina = itens.slice(inicio, inicio + limite)
      const cheia = pagina.length === limite && inicio + limite < itens.length
      return HttpResponse.json({ items: pagina, ...(cheia ? { next_cursor: pagina[pagina.length - 1]!.id } : {}) })
    }),
    rota('post', '/incidents/:id/ack', ({ params }) => {
      const i = st.incidentes.find((x) => x.id === params.id)
      if (!i) return erro(404, 'not_found')
      i.acknowledged_at ??= agora().toISOString()
      return HttpResponse.json(i)
    }),

    rota('get', '/channels', () => HttpResponse.json({ items: st.canais })),
    rota('post', '/channels', ({ corpo }) => {
      const email = corpo.kind === 'email'
      const c = {
        id: `canal-${st.canais.length + 1}`,
        kind: email ? ('email' as const) : ('discord' as const),
        hint: email ? 'd***@exemplo.com' : 'discord.com/…/abcd',
        status: email ? ('pending' as const) : ('verified' as const),
        enabled: true,
        created_at: agora().toISOString(),
        last_error: null,
        last_failure_at: null,
      }
      st.canais.push(c)
      return HttpResponse.json(c, { status: 201 })
    }),
    rota('delete', '/channels/:id', ({ params }) => {
      st.canais = st.canais.filter((c) => c.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/channels/:id/verify/resend', () => new HttpResponse(null, { status: 204 })),

    rota('get', '/status-pages', () => HttpResponse.json({ items: st.paginas })),
    rota('post', '/status-pages', ({ corpo }) => {
      const p = {
        id: 'pagina-1',
        slug: String(corpo.slug),
        title: String(corpo.title),
        published: Boolean(corpo.published),
        items: (corpo.items as { target_id: string; display_name: string }[]) ?? [],
        public_url: `http://localhost:5173/p/${String(corpo.slug)}`,
        created_at: agora().toISOString(),
      }
      st.paginas = [p]
      return HttpResponse.json(p, { status: 201 })
    }),
    rota('patch', '/status-pages/:id', ({ params, corpo }) => {
      const p = st.paginas.find((x) => x.id === params.id)
      if (!p) return erro(404, 'not_found')
      Object.assign(p, corpo)
      p.public_url = `http://localhost:5173/p/${p.slug}`
      return HttpResponse.json(p)
    }),

    rota('get', '/sessions', () => HttpResponse.json({ items: st.sessoes })),
    rota('delete', '/sessions/:id', ({ params }) => {
      const s = st.sessoes.find((x) => x.id === params.id)
      if (!s) return erro(404, 'not_found')
      st.sessoes = st.sessoes.filter((x) => x.id !== params.id)
      // A própria família: o 204 já apaga o cookie (D15 do Plano 5).
      if (s.current) {
        st.familiaRevogada = true
        st.pote = null
      }
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/auth/password', ({ corpo }) => {
      if (corpo.current_password !== SENHA) return erro(401, 'invalid_credentials')
      if (st.totpLigado) {
        const codigo = String(corpo.totp_code ?? '')
        const recuperacao = codigo === RECUPERACAO_OK && !st.recuperacaoGasta.has(codigo)
        if (codigo !== TOTP_OK && !recuperacao) return erro(401, 'invalid_credentials')
        if (recuperacao) st.recuperacaoGasta.add(codigo)
        if (recuperacao && st.trocaFalhaDepoisDoCodigo) return erro(401, 'unauthorized')
      }
      const n = [...String(corpo.new_password)].length
      if (n < 15) return erro(400, 'validation_failed', [{ field: 'new_password', reason: 'too_short' }])
      st.sessoes = st.sessoes.filter((s) => s.current)
      // Task 11 do Plano 5: trocar a senha apaga o cadastro de 2FA pendente
      // e invalida os desafios de login em aberto.
      st.totpPendente = false
      st.desafioValido = false
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/auth/totp/enroll', ({ corpo }) => {
      if (corpo.password !== SENHA) return erro(401, 'invalid_credentials')
      if (st.totpLigado) return erro(403, 'forbidden')
      st.totpPendente = true
      return HttpResponse.json({ otpauth_uri: 'otpauth://totp/Vigilume:dono%40exemplo.com?secret=JBSWY3DPEHPK3PXP&issuer=Vigilume' })
    }),
    rota('post', '/auth/totp/confirm', ({ corpo }) => {
      if (!st.totpPendente || corpo.code !== TOTP_OK) return erro(400, 'validation_failed')
      st.totpLigado = true
      st.totpPendente = false
      return HttpResponse.json({ recovery_codes: ['AAAABBBBCCCCDDDD', 'EEEEFFFFGGGGHHHH'] })
    }),
    rota('post', '/auth/totp/disable', ({ corpo }) => {
      // D24 do Plano 5: senha ou código errado é invalid_credentials;
      // unauthorized fica só para o token recusado.
      if (corpo.password !== SENHA || (corpo.code !== TOTP_OK && corpo.code !== RECUPERACAO_OK)) {
        return erro(401, 'invalid_credentials')
      }
      st.totpLigado = false
      return new HttpResponse(null, { status: 204 })
    }),
  ]

  return {
    handlers,
    estado: st,
    chamadas: (rotaChave: string) => st.chamadas.get(rotaChave) ?? 0,
    corpos: (rotaChave: string) => st.corpos.get(rotaChave) ?? [],
    // O navegador tem um refresh válido no pote: o boot entra sozinho.
    logado() {
      st.pote = novoCookie()
      st.familiaRevogada = false
    },
    // Os access tokens venceram (15 min); o refresh continua valendo.
    expirarAcessos() {
      st.acessos.clear()
    },
  }
}
```

Crie `web/src/test/ambiente.ts`:

```ts
import { criarApi } from '@/lib/api/cliente'
import type { Buscar } from '@/lib/api/http'
import { criarSessao, type Canal, type Travar } from '@/lib/api/sessao'
import { criarApiFalsa } from './api-falsa'
import { servidor } from './setup'

// O fetch do Node não resolve caminho relativo; o do navegador resolve
// contra a origem da página. Este buscar faz o que o navegador faria.
export const buscarDoTeste = (entrada: string, init: RequestInit) => fetch(new URL(entrada, location.origin), init)

export function usarApiFalsa() {
  const falsa = criarApiFalsa()
  servidor.use(...falsa.handlers)
  return falsa
}

export function novaSessao(o: { travar?: Travar; canal?: Canal | null; buscar?: Buscar } = {}) {
  const buscar = o.buscar ?? buscarDoTeste
  const sessao = criarSessao({ buscar, travar: o.travar, canal: o.canal ?? null })
  const api = criarApi(sessao, buscar)
  return { sessao, api }
}

// Uma trava com a semântica de navigator.locks.request: exclusiva e em
// ordem de chegada. Várias "abas" dividem a mesma.
export function travaFalsa(): Travar {
  let fila: Promise<unknown> = Promise.resolve()
  return <T,>(_nome: string, fn: () => Promise<T>) => {
    const vez = fila.then(fn, fn)
    fila = vez.catch(() => undefined)
    return vez
  }
}
```

Crie `web/src/lib/format.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { duracao, haQuanto, milissegundos, navegadorDe, pontosDeCodigo, porcento } from './format'

describe('porcento', () => {
  it('trunca em duas casas, nunca arredonda para cima', () => {
    expect(porcento(0.99999)).toBe('99,99%')
    expect(porcento(0.9993)).toBe('99,93%')
    expect(porcento(1)).toBe('100,00%')
    expect(porcento(0)).toBe('0,00%')
  })
})

describe('milissegundos', () => {
  it('null é traço', () => {
    expect(milissegundos(null)).toBe('—')
    expect(milissegundos(1234)).toBe('1.234 ms')
  })
})

describe('duracao', () => {
  it('escolhe a unidade', () => {
    expect(duracao(20_000)).toBe('menos de 1 min')
    expect(duracao(12 * 60_000)).toBe('12 min')
    expect(duracao(64 * 60_000)).toBe('1 h 4 min')
    expect(duracao(51 * 3600_000)).toBe('2 d 3 h')
  })
})

describe('haQuanto', () => {
  it('fala em minutos e horas', () => {
    const agora = new Date('2026-09-24T12:00:00Z')
    expect(haQuanto('2026-09-24T11:58:00Z', agora)).toBe('há 2 minutos')
    expect(haQuanto('2026-09-24T09:00:00Z', agora)).toBe('há 3 horas')
  })
})

describe('pontosDeCodigo', () => {
  it('conta code points, não unidades UTF-16', () => {
    expect(pontosDeCodigo('😀'.repeat(15))).toBe(15)
    expect('😀'.repeat(15).length).toBe(30)
  })
})

describe('navegadorDe', () => {
  it('reconhece os comuns e não inventa', () => {
    expect(navegadorDe('Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0')).toBe('Firefox em Linux')
    expect(navegadorDe('Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/128.0 Safari/537.36')).toBe(
      'Chrome em Windows',
    )
    expect(navegadorDe('')).toBe('Navegador desconhecido')
    expect(navegadorDe('curl/8.0')).toBe('curl/8.0')
  })
})
```

Crie `web/src/lib/api/sessao.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'
import { EMAIL, RECUPERACAO_OK, SENHA, TOTP_OK } from '@/test/api-falsa'
import { buscarDoTeste, novaSessao, travaFalsa, usarApiFalsa } from '@/test/ambiente'
import { ApiError } from './http'
import type { Canal } from './sessao'

// Dois BroadcastChannel do mesmo nome, como duas abas.
function canais(): [Canal, Canal] {
  const a = new BroadcastChannel('teste-sessao')
  const b = new BroadcastChannel('teste-sessao')
  return [a, b]
}

// Prova de ausência sem relógio: a e b são o mesmo canal visto de duas
// abas, e mensagens do mesmo emissor chegam em ordem. Depois do que o teste
// quer ver ausente, a posta uma sentinela; quando ela chega em b, tudo o que
// a postou antes já chegou. Uma espera fixa passaria com a mensagem ainda a
// caminho.
async function ateASentinela(a: Canal, recebidos: unknown[]) {
  a.postMessage({ tipo: 'sentinela' })
  await vi.waitFor(() => expect(recebidos.at(-1)).toEqual({ tipo: 'sentinela' }))
  return recebidos.slice(0, -1)
}

describe('boot', () => {
  it('com o cookie de refresh, entra sem pedir a senha', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const { sessao } = novaSessao()
    await sessao.iniciar()
    expect(sessao.estado()).toBe('autenticado')
    expect(sessao.token()).not.toBeNull()
  })

  it('sem cookie, fica anônimo, sem avisar as outras abas', async () => {
    usarApiFalsa()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const { sessao } = novaSessao({ canal: a })
    await sessao.iniciar()
    expect(sessao.estado()).toBe('anonimo')
    // Sabotagem prevista: em iniciar(), avisar({ tipo: 'encerrada' }) também
    // no anônimo → a sentinela chega depois dele.
    expect(await ateASentinela(a, recebidos)).toEqual([])
  })

  it('servidor fora do ar é offline, não anônimo: a sessão pode estar boa', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.forcar.set('POST /auth/refresh', { status: 503, code: 'unavailable' })
    const { sessao } = novaSessao()
    await sessao.iniciar()
    expect(sessao.estado()).toBe('offline')
  })

  // O StrictMode monta os efeitos duas vezes; o boot é uma promessa só.
  // Duas guardas cobrem esta propriedade: o boot único e o single-flight de
  // renovar(). O teste prova a propriedade, não cada guarda: a sabotagem
  // que o deixa vermelho é tirar as duas ("boot ??=" → "boot =" e
  // "emVoo ??=" → "emVoo ="); tirar só uma fica verde.
  it('chamado duas vezes, renova uma vez só', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const { sessao } = novaSessao()
    await Promise.all([sessao.iniciar(), sessao.iniciar()])
    await sessao.iniciar()
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.estado.familiaRevogada).toBe(false)
  })
})

describe('renovar', () => {
  // Uma requisição que saiu com o token antigo e voltou 401 DEPOIS de outra
  // já ter renovado não renova de novo: só repete com o token atual.
  // Sabotagem prevista: em renovar(), trocar
  // "if (token !== null && token !== usado)" por
  // "if (token !== null && token !== usado && Math.random() > 2)".
  it('o 401 atrasado de um token antigo não dispara um segundo refresh', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    const antigo = sessao.token()
    const novo = await sessao.renovar(antigo)
    expect(novo).not.toBe(antigo)
    expect(await sessao.renovar(antigo)).toBe(novo)
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
  })
})

describe('login', () => {
  it('senha certa sem 2FA abre a sessão', async () => {
    usarApiFalsa()
    const { sessao } = novaSessao()
    expect(await sessao.entrar(EMAIL, SENHA)).toEqual({ tipo: 'ok' })
    expect(sessao.estado()).toBe('autenticado')
  })

  it('senha errada é invalid_credentials e não abre nada', async () => {
    usarApiFalsa()
    const { sessao } = novaSessao()
    await expect(sessao.entrar(EMAIL, 'errada')).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(sessao.token()).toBeNull()
  })

  it('com 2FA, a senha devolve o desafio; o código do app ou o de recuperação abrem a sessão', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.totpLigado = true
    for (const codigo of [TOTP_OK, RECUPERACAO_OK]) {
      const { sessao } = novaSessao()
      const r = await sessao.entrar(EMAIL, SENHA)
      expect(r).toEqual({ tipo: 'totp', desafio: 'desafio-1' })
      expect(sessao.token()).toBeNull()
      await sessao.verificarTotp('desafio-1', codigo)
      expect(sessao.estado()).toBe('autenticado')
    }
  })

  it('toda mutação de autenticação leva X-Requested-With (a API falsa recusa sem ele)', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    await sessao.sair()
    expect(falsa.chamadas('POST /auth/login')).toBe(1)
    expect(falsa.chamadas('POST /auth/logout')).toBe(1)
    expect(sessao.estado()).toBe('anonimo')
  })
})

describe('várias abas', () => {
  // Duas abas (duas sessões) dividem o pote de cookies e a trava do
  // navegador. O refresh demora 20 ms, o que garante a sobreposição.
  // Sabotagem prevista: travaFalsa() → (_n, fn) => fn() deixa este teste
  // vermelho (família revogada), como no spike com o navegador de verdade.
  it('duas abas renovando juntas rotacionam em série, e a família sobrevive', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    falsa.estado.atrasoRefresh = 20
    const trava = travaFalsa()
    const aba1 = novaSessao({ travar: trava })
    const aba2 = novaSessao({ travar: trava })
    await Promise.all([aba1.sessao.iniciar(), aba2.sessao.iniciar()])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
    expect(falsa.estado.familiaRevogada).toBe(false)
    expect(aba1.sessao.estado()).toBe('autenticado')
    expect(aba2.sessao.estado()).toBe('autenticado')
  })

  it('sem trava entre abas, a detecção de reúso derruba a família (o defeito que a trava fecha)', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    falsa.estado.atrasoRefresh = 20
    const semTrava = <T,>(_n: string, fn: () => Promise<T>) => fn()
    const aba1 = novaSessao({ travar: semTrava })
    const aba2 = novaSessao({ travar: semTrava })
    await Promise.all([aba1.sessao.iniciar(), aba2.sessao.iniciar()])
    expect(falsa.estado.familiaRevogada).toBe(true)
  })

  // Sabotagem prevista: em encerrar(), trocar "if (avisarOutras)" por
  // "if (avisarOutras && Math.random() > 2)" (não posta no canal).
  it('sair numa aba faz a outra largar o token na hora', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b })
    await aba1.sessao.iniciar()
    await aba2.sessao.iniciar()
    await aba1.sessao.sair()
    await vi.waitFor(() => expect(aba2.sessao.token()).toBeNull())
    expect(aba2.sessao.estado()).toBe('anonimo')
    expect(aba2.sessao.motivo()).toBe('outra-aba')
  })

  it('o canal nunca leva o token', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const { sessao } = novaSessao({ canal: a })
    await sessao.entrar(EMAIL, SENHA)
    await sessao.sair()
    const avisos = await ateASentinela(a, recebidos)
    expect(avisos).toEqual([{ tipo: 'iniciada' }, { tipo: 'encerrada' }])
    expect(JSON.stringify(avisos)).not.toContain('acesso-')
  })
})

describe('erros de rede', () => {
  // Sem rede de verdade: o fetch rejeita com TypeError (um 502 é resposta,
  // e nunca chegaria ao catch de sair()). Sabotagem prevista: em sair(),
  // trocar "if (!(e instanceof ApiError)) throw e" por
  // "if (!(e instanceof ApiError) || e.code === 'network') throw e".
  it('logout sem rede ainda larga a sessão local', async () => {
    usarApiFalsa()
    let semRede = false
    const buscar = (entrada: string, init: RequestInit) =>
      semRede ? Promise.reject(new TypeError('Failed to fetch')) : buscarDoTeste(entrada, init)
    const { sessao } = novaSessao({ buscar })
    await sessao.entrar(EMAIL, SENHA)
    semRede = true
    await sessao.sair()
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
  })

  it('refresh com erro 5xx sobe como ApiError, sem encerrar a sessão', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    falsa.estado.forcar.set('POST /auth/refresh', { status: 500, code: 'internal_error' })
    await expect(sessao.renovar(sessao.token())).rejects.toBeInstanceOf(ApiError)
    expect(sessao.estado()).toBe('autenticado')
  })
})
```

Crie `web/src/lib/api/cliente.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { EMAIL, SENHA } from '@/test/api-falsa'
import { novaSessao, usarApiFalsa } from '@/test/ambiente'
import { servidor } from '@/test/setup'
import { ApiError } from './http'

async function logado() {
  const falsa = usarApiFalsa()
  const s = novaSessao()
  await s.sessao.entrar(EMAIL, SENHA)
  return { falsa, ...s }
}

describe('a regra do 401', () => {
  // A spec §11.2, item 3. Sabotagem prevista: tirar o "emVoo ??=" de
  // sessao.ts (renovar a cada chamada) → 5 refreshes e família revogada.
  it('cinco 401 simultâneos disparam um refresh só, e os cinco repetem com o token novo', async () => {
    const { falsa, api } = await logado()
    falsa.estado.alvos = [1, 2, 3, 4, 5].map((i) => ({
      id: `a${i}`,
      name: `Alvo ${i}`,
      url: 'https://exemplo.com',
      method: 'GET' as const,
      interval_sec: 60,
      timeout_ms: 10000,
      expected_status: 200,
      degraded_threshold_ms: 2000,
      enabled: true,
      header_names: [],
      created_at: '2026-09-24T12:00:00Z',
    }))
    falsa.estado.atrasoRefresh = 20
    falsa.expirarAcessos()
    const r = await Promise.all([1, 2, 3, 4, 5].map((i) => api.alvo(`a${i}`)))
    expect(r.map((a) => a.id)).toEqual(['a1', 'a2', 'a3', 'a4', 'a5'])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.estado.familiaRevogada).toBe(false)
  })

  // Nota do Plano 5 §12. Sabotagem prevista: podeRenovar() devolvendo true
  // para todo 401 (code !== '' && caminho !== '') → 1 refresh e 2 chamadas
  // de /auth/password.
  it('invalid_credentials na troca de senha não renova nem repete', async () => {
    const { falsa, api } = await logado()
    await expect(
      api.trocarSenha({ current_password: 'errada', new_password: 'x'.repeat(20) }),
    ).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(falsa.chamadas('POST /auth/password')).toBe(1)
  })

  // Só "unauthorized" (o Authn recusando o token) renova. Sabotagem
  // prevista: trocar "code === 'unauthorized'" por "code !== ''" em
  // podeRenovar().
  it('outro 401 fora de /auth/* é erro, sem refresh', async () => {
    const { falsa, api } = await logado()
    falsa.estado.forcar.set('GET /me', { status: 401, code: 'invalid_session' })
    await expect(api.me()).rejects.toMatchObject({ code: 'invalid_session' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
  })

  // Reconciliação, item 3: nada em /auth/* renova, nem com "unauthorized".
  // A sessão continua (o refresh nem foi tentado); a tela pede para
  // recarregar. Sabotagem prevista: trocar o "!caminho.startsWith('/auth/')"
  // por "caminho !== ''".
  it('token vencido na troca de senha não renova: /auth/* nunca renova', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    await expect(
      api.trocarSenha({ current_password: SENHA, new_password: 'y'.repeat(20) }),
    ).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(falsa.chamadas('POST /auth/password')).toBe(1)
    expect(sessao.estado()).toBe('autenticado')
  })

  it('refresh recusado encerra a sessão com o motivo "expirou"', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.familiaRevogada = true
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('expirou')
  })

  it('401 de novo depois de renovar não entra em laço', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.estado.forcar.set('GET /me', { status: 401, code: 'unauthorized' })
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.chamadas('GET /me')).toBe(2)
    expect(sessao.estado()).toBe('anonimo')
  })

  // Uma consulta que ainda voava quando a sessão acabou volta 401 sem
  // token: não renova (o cookie já foi apagado) e não troca o motivo do fim.
  // Sabotagem prevista: tirar o "usado === null ||" do cliente.
  it('401 de requisição sem token não dispara refresh nem troca o motivo', async () => {
    const { falsa, sessao, api } = await logado()
    sessao.encerrar('encerrada-aqui')
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(sessao.motivo()).toBe('encerrada-aqui')
  })

  // Um refresh em voo quando a sessão acaba aqui (encerrar a própria sessão,
  // sair) não ressuscita o token, e o motivo do fim continua o primeiro.
  // Duas guardas, duas sabotagens: em renovarAgora(), trocar
  // "if (minha !== epoca)" por "if (minha !== epoca && Math.random() > 2)"
  // (o token volta); em pedir(), trocar "sessao.estado() === 'autenticado'"
  // por "sessao.estado() !== 'verificando'" (o motivo vira "expirou").
  it('sessão encerrada com um refresh em voo: o token não volta e o motivo fica', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.atrasoRefresh = 300
    const pedido = api.me().catch((x: unknown) => x)
    await vi.waitFor(() => expect(falsa.chamadas('POST /auth/refresh')).toBe(1), { interval: 5 })
    sessao.encerrar('encerrada-aqui')
    expect(await pedido).toMatchObject({ code: 'unauthorized' })
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('encerrada-aqui')
  })

  // Sabotagem prevista: tirar o X-Requested-With de enviar() → 403 da API
  // falsa, como o RequireCustomHeader do servidor.
  it('mutações levam X-Requested-With', async () => {
    const { falsa, api } = await logado()
    await api.reconhecer('nao-existe').catch(() => undefined)
    expect(falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
    await expect(api.reconhecer('nao-existe')).rejects.toMatchObject({ code: 'not_found' })
  })
})

describe('o envelope de erro', () => {
  it('traz code, fields e trace_id', async () => {
    const { falsa, api } = await logado()
    falsa.estado.forcar.set('POST /targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'url', reason: 'forbidden' }],
    })
    const e = await api
      .criarAlvo({
        name: 'x',
        url: 'http://10.0.0.1',
        method: 'GET',
        interval_sec: 60,
        timeout_ms: 1000,
        expected_status: 200,
        enabled: true,
      })
      .catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 400, code: 'validation_failed', traceId: 'trace-falso' })
    expect((e as ApiError).fields).toEqual([{ field: 'url', reason: 'forbidden' }])
  })

  // Um 502 do proxy com HTML não é o envelope: vira 'unexpected', com o
  // status. Sabotagem prevista: em lerErro, trocar 'unexpected' por
  // 'internal_error'.
  it('resposta de erro que não é o envelope vira "unexpected"', async () => {
    const { api } = await logado()
    servidor.use(
      http.get('*/api/v1/me', () => new HttpResponse('<html>502 Bad Gateway</html>', { status: 502, headers: { 'Content-Type': 'text/html' } })),
    )
    const e = await api.me().catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 502, code: 'unexpected' })
  })

  // Spec do Plano 6 §3.4: nunca uma exceção crua. Sucesso com JSON quebrado
  // ou fora do schema também é ApiError 'unexpected' (status 200: o
  // QueryClient não repete). Sabotagem prevista: em pedir(), trocar
  // "return lerCorpo(r, esquema)" por
  // "return esquema.parse(await r.json()) ?? lerCorpo(r, esquema)".
  it('sucesso sem JSON válido ou fora do schema vira ApiError "unexpected", não exceção crua', async () => {
    const { falsa, api } = await logado()
    falsa.estado.resumo = [{ bug: true } as never]
    await expect(api.resumo()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
    servidor.use(http.get('*/api/v1/me', () => new HttpResponse('{quebrado', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(api.me()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
  })

  // D10: uma chave que some do DTO quebra alto, com o nome do campo.
  it('resposta fora do schema falha com o nome do campo', async () => {
    const { falsa, api } = await logado()
    falsa.estado.resumo = [{ id: 'x', name: 'API' } as never]
    await expect(api.resumo()).rejects.toThrow(/enabled|state/)
  })
})
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd web && npx vitest run`
Expected: FAIL: os arquivos de teste não resolvem `./format`, `./sessao`, `./http` e `@/lib/api/cliente`.

- [ ] **Step 4: A página, os tokens de cor e o `main.tsx` provisório**

Crie `web/index.html`:

```html
<!doctype html>
<html lang="pt-BR">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="color-scheme" content="light dark" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <!-- Arquivo, e não script inline: a CSP do painel é script-src 'self'.
         Síncrono no <head> para aplicar o tema antes da primeira pintura. -->
    <script src="/tema.js"></script>
    <title>Vigilume</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

Crie `web/public/tema.js`:

```js
// Aplica o tema salvo antes da primeira pintura, para o painel não piscar
// claro antes de ficar escuro. Mesma chave e mesma regra de src/lib/theme.tsx.
;(function () {
  var tema = 'sistema'
  try {
    tema = localStorage.getItem('vigilume.tema') || 'sistema'
  } catch (e) {}
  var escuro =
    tema === 'escuro' || (tema === 'sistema' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', escuro)
})()
```

Crie `web/public/favicon.svg`:

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="13" fill="none" stroke="#1d4ed8" stroke-width="4"/><circle cx="16" cy="16" r="5" fill="#15803d"/></svg>
```

Crie `web/src/index.css`:

```css
@import 'tailwindcss';

/* O tema escuro é a classe .dark no <html>, posta por /tema.js e por
   src/lib/theme.tsx. */
@custom-variant dark (&:where(.dark, .dark *));

/* Tokens do painel. As cores de estado foram escolhidas para contraste AA
   (4,5:1) como texto sobre o fundo de cada tema; o projeto a11y do
   Playwright confere com o axe nos dois temas. */
:root {
  --fundo: #ffffff;
  --texto: #0f172a;
  --suave: #475569;
  --borda: #cbd5e1;
  --painel: #f8fafc;
  --primaria: #1d4ed8;
  --primaria-texto: #ffffff;
  --realce: #1d4ed8;
  --perigo: #b91c1c;
  --foco: #1d4ed8;
  --estado-no-ar: #15803d;
  --estado-degradado: #a16207;
  --estado-fora: #b91c1c;
  --estado-sem-dados: #475569;
  --grafico-linha: #1d4ed8;
  --grafico-faixa: #93c5fd;
  --grafico-uptime: #bbf7d0;
}

.dark {
  --fundo: #0b1120;
  --texto: #e2e8f0;
  --suave: #94a3b8;
  --borda: #334155;
  --painel: #111827;
  --primaria: #1d4ed8;
  --primaria-texto: #ffffff;
  /* Links: o azul do botão não tem contraste como texto no fundo escuro. */
  --realce: #93c5fd;
  --perigo: #f87171;
  --foco: #93c5fd;
  --estado-no-ar: #4ade80;
  --estado-degradado: #facc15;
  --estado-fora: #f87171;
  --estado-sem-dados: #94a3b8;
  --grafico-linha: #60a5fa;
  --grafico-faixa: #1e3a8a;
  --grafico-uptime: #14532d;
}

@theme inline {
  --color-fundo: var(--fundo);
  --color-texto: var(--texto);
  --color-suave: var(--suave);
  --color-borda: var(--borda);
  --color-painel: var(--painel);
  --color-primaria: var(--primaria);
  --color-primaria-texto: var(--primaria-texto);
  --color-realce: var(--realce);
  --color-perigo: var(--perigo);
  --color-foco: var(--foco);
  --color-no-ar: var(--estado-no-ar);
  --color-degradado: var(--estado-degradado);
  --color-fora: var(--estado-fora);
  --color-sem-dados: var(--estado-sem-dados);
  --font-sans: system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;
}

html {
  color-scheme: light;
}
html.dark {
  color-scheme: dark;
}

body {
  background: var(--fundo);
  color: var(--texto);
  font-family: var(--font-sans);
}

:focus-visible {
  outline: 3px solid var(--foco);
  outline-offset: 2px;
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    transition-duration: 0.01ms !important;
  }
}
```

Crie `web/src/main.tsx`:

```tsx
import './lib/nonce'
import './index.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// Provisório (Task 2): a árvore do painel (rotas, sessão, provedores) chega
// na Task 3. Este arquivo existe para o build produzir um dist de verdade.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <main>
      <h1>Vigilume</h1>
    </main>
  </StrictMode>,
)
```

- [ ] **Step 5: O núcleo**

Crie `web/src/lib/nonce.ts`:

```ts
import { setNonce } from 'get-nonce'

// O nonce desta resposta, que o handler Go (internal/webui) escreveu no
// <meta property="csp-nonce">. O navegador esconde o atributo de
// getAttribute (nonce hiding); a propriedade .nonce continua legível.
//
// Quem injeta <style> em runtime precisa dele, ou a CSP bloqueia o estilo:
// o react-remove-scroll (Dialog e AlertDialog do Radix) lê o nonce do
// get-nonce, e é para isso o setNonce abaixo. Sem ele, abrir um diálogo não
// trava a rolagem da página (spec do Plano 6, §8.1). Em desenvolvimento o
// valor é o marcador literal, sem efeito: o Vite não aplica CSP.
const meta = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')

export const nonce: string | undefined = meta?.nonce || undefined

if (nonce) setNonce(nonce)
```

Crie `web/src/lib/utils.ts`:

```ts
import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...classes: ClassValue[]) {
  return twMerge(clsx(classes))
}
```

Crie `web/src/lib/format.ts`:

```ts
// Formatação para a tela, em pt-BR. Funções puras: o "agora" é parâmetro.

// Porcentagem TRUNCADA em duas casas, nunca arredondada: "100,00%" num dia
// com falha é pior que "99,99%" (D16 do Plano 4; nota do Plano 5 §12).
export function porcento(fracao: number): string {
  const v = Math.floor(fracao * 10000) / 100
  return `${v.toFixed(2).replace('.', ',')}%`
}

export function milissegundos(ms: number | null | undefined): string {
  return ms === null || ms === undefined ? '—' : `${Math.round(ms).toLocaleString('pt-BR')} ms`
}

export function duracao(ms: number): string {
  const min = Math.round(ms / 60_000)
  if (min < 1) return 'menos de 1 min'
  if (min < 60) return `${min} min`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} h ${min % 60} min`
  return `${Math.floor(h / 24)} d ${h % 24} h`
}

const relativa = new Intl.RelativeTimeFormat('pt-BR', { numeric: 'auto' })

export function haQuanto(iso: string, agora: Date): string {
  const seg = Math.round((new Date(iso).getTime() - agora.getTime()) / 1000)
  const abs = Math.abs(seg)
  if (abs < 60) return relativa.format(Math.round(seg), 'second')
  if (abs < 3600) return relativa.format(Math.round(seg / 60), 'minute')
  if (abs < 86400) return relativa.format(Math.round(seg / 3600), 'hour')
  return relativa.format(Math.round(seg / 86400), 'day')
}

const dataHoraFmt = new Intl.DateTimeFormat('pt-BR', { dateStyle: 'short', timeStyle: 'short' })

// Datas no fuso do navegador (o painel tem JavaScript, ao contrário da página
// pública, que é UTC); o fuso aparece escrito onde a hora importa.
export function dataHora(iso: string): string {
  return dataHoraFmt.format(new Date(iso))
}

export function fuso(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

// Tamanho em code points, como o servidor conta (Plano 5, D1): "é" é 1,
// um emoji fora do plano básico também é 1; .length contaria unidades UTF-16.
export function pontosDeCodigo(s: string): number {
  return [...s].length
}

// "Firefox em Linux", a partir de um user agent. O texto é de terceiros e é
// só exibido como texto.
export function navegadorDe(ua: string): string {
  const nav = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : null
  const so = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPad/.test(ua)
      ? 'iOS'
      : /Windows/.test(ua)
        ? 'Windows'
        : /Mac OS X/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : null
  if (nav && so) return `${nav} em ${so}`
  return nav ?? so ?? (ua.trim() === '' ? 'Navegador desconhecido' : ua.slice(0, 60))
}
```

Crie `web/src/lib/api/http.ts`:

```ts
import type { z } from 'zod'

// O transporte cru da API: prefixo, cabeçalhos e leitura do envelope de erro
// (spec §9.1). Não sabe nada de sessão; quem renova o token é cliente.ts.

export const PREFIXO = '/api/v1'

export type Buscar = (entrada: string, init: RequestInit) => Promise<Response>

export type ErroDeCampo = { field: string; reason: string }

// ApiError é a única forma de erro que chega às telas. code é o contrato com
// o servidor; 'network' e 'unexpected' são do cliente: rede caída e resposta
// que não é o envelope (502 do proxy, HTML, JSON inválido).
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: ErroDeCampo[]
  readonly traceId: string | null

  constructor(status: number, code: string, message: string, fields: ErroDeCampo[] = [], traceId: string | null = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.traceId = traceId
  }
}

type Envelope = {
  error?: { code?: unknown; message?: unknown; fields?: unknown }
  trace_id?: unknown
}

function camposDe(v: unknown): ErroDeCampo[] {
  if (!Array.isArray(v)) return []
  return v.filter(
    (f): f is ErroDeCampo =>
      typeof f === 'object' && f !== null && typeof f.field === 'string' && typeof f.reason === 'string',
  )
}

export async function lerErro(r: Response): Promise<ApiError> {
  let corpo: Envelope | null = null
  try {
    corpo = (await r.json()) as Envelope
  } catch {
    corpo = null
  }
  const e = corpo?.error
  if (!e || typeof e.code !== 'string') {
    return new ApiError(r.status, 'unexpected', `Resposta inesperada do servidor (${r.status}).`)
  }
  const traceId = typeof corpo?.trace_id === 'string' && corpo.trace_id !== '' ? corpo.trace_id : null
  return new ApiError(r.status, e.code, typeof e.message === 'string' ? e.message : '', camposDe(e.fields), traceId)
}

// lerCorpo lê uma resposta de sucesso pelo schema da rota. JSON quebrado ou
// fora do contrato vira ApiError 'unexpected' com o status da resposta, e
// nunca uma exceção crua na tela (spec do Plano 6 §3.4): a tela mostra a
// mensagem de sempre, e o QueryClient não repete (status < 500). A mensagem
// leva os caminhos dos campos que falharam, para o log e para o suporte.
export async function lerCorpo<T>(r: Response, esquema: z.ZodType<T>): Promise<T> {
  let json: unknown
  try {
    json = await r.json()
  } catch {
    throw new ApiError(r.status, 'unexpected', `Resposta sem JSON válido (${r.status}).`)
  }
  const v = esquema.safeParse(json)
  if (!v.success) {
    const campos = v.error.issues.map((i) => i.path.join('.') || '(raiz)').join(', ')
    throw new ApiError(r.status, 'unexpected', `Resposta fora do contrato: ${campos}.`)
  }
  return v.data
}

export type Pedido = {
  metodo: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  caminho: string
  corpo?: unknown
  token?: string | null
  sinal?: AbortSignal
}

// enviar monta a requisição. Toda mutação leva X-Requested-With, inclusive
// /auth/refresh, /auth/login e /auth/logout: o RequireCustomHeader envolve a
// API inteira (routes.go), e sem o cabeçalho a resposta é 403 (spec §5.3).
export async function enviar(buscar: Buscar, p: Pedido): Promise<Response> {
  const cabecalhos: Record<string, string> = { Accept: 'application/json' }
  if (p.metodo !== 'GET') cabecalhos['X-Requested-With'] = 'vigilume'
  if (p.token) cabecalhos.Authorization = `Bearer ${p.token}`
  const init: RequestInit = { method: p.metodo, headers: cabecalhos, credentials: 'same-origin', signal: p.sinal }
  if (p.corpo !== undefined) {
    cabecalhos['Content-Type'] = 'application/json'
    init.body = JSON.stringify(p.corpo)
  }
  try {
    return await buscar(PREFIXO + p.caminho, init)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', 'Sem conexão com o servidor.')
  }
}
```

Crie `web/src/lib/api/schemas.ts`:

```ts
import { z } from 'zod'

// A CSP do painel não tem 'unsafe-eval'. O Zod 4 decide se compila
// validadores rápidos testando Function(''): o teste é pego num try, mas o
// navegador reporta a violação de script-src mesmo assim (achado no E2E da
// cópia). jitless desliga o teste e o JIT; a validação é a mesma.
z.config({ jitless: true })

// Os schemas das respostas da API, campo a campo com os DTOs Go
// (internal/httpapi/*handlers.go) e o contrato dos Planos 5–7.
//
// Toda resposta passa por aqui (D10 da spec do Plano 6). O z.object padrão
// descarta chave extra, então um campo novo no servidor não quebra o painel;
// e falha com chave ausente ou tipo trocado, então uma remoção quebra alto,
// com o nome do campo, em vez de virar undefined numa tela.

const instante = z.string().min(1)
const id = z.string().min(1)

export const Token = z.object({ access_token: z.string().min(1), expires_in: z.number() })
export type Token = z.infer<typeof Token>

export const RespostaLogin = z.union([
  Token,
  z.object({ totp_required: z.literal(true), challenge: z.string().min(1) }),
])
export type RespostaLogin = z.infer<typeof RespostaLogin>

export const Me = z.object({ id, email: z.string(), totp_enabled: z.boolean(), created_at: instante })
export type Me = z.infer<typeof Me>

export const Alvo = z.object({
  id,
  name: z.string(),
  url: z.string(),
  method: z.enum(['GET', 'HEAD', 'POST']),
  interval_sec: z.number(),
  timeout_ms: z.number(),
  expected_status: z.number(),
  degraded_threshold_ms: z.number().nullable(),
  enabled: z.boolean(),
  header_names: z.array(z.string()),
  created_at: instante,
})
export type Alvo = z.infer<typeof Alvo>

export const ListaDeAlvos = z.object({ items: z.array(Alvo), next_cursor: z.string().optional() })

export const EstadoAlvo = z.enum(['up', 'degraded', 'down', 'unknown'])
export type EstadoAlvo = z.infer<typeof EstadoAlvo>

export const HoraDoSpark = z.object({
  bucket: instante,
  total: z.number(),
  ok: z.number(),
  p50_ms: z.number().nullable(),
})
export type HoraDoSpark = z.infer<typeof HoraDoSpark>

export const ItemDoResumo = z.object({
  id,
  name: z.string(),
  enabled: z.boolean(),
  state: EstadoAlvo,
  last_checked_at: instante.nullable(),
  last_latency_ms: z.number().nullable(),
  uptime_24h: z.number().nullable(),
  open_incident_id: id.nullable(),
  spark: z.array(HoraDoSpark),
})
export type ItemDoResumo = z.infer<typeof ItemDoResumo>

export const Resumo = z.object({ items: z.array(ItemDoResumo) })

export const Check = z.object({
  checked_at: instante,
  status: z.enum(['up', 'degraded', 'down']),
  latency_ms: z.number(),
  status_code: z.number().nullable(),
  error_kind: z.string().nullable(),
})
export type Check = z.infer<typeof Check>

export const Hora = z.object({
  bucket: instante,
  total: z.number(),
  ok: z.number(),
  p50_ms: z.number().nullable(),
  p95_ms: z.number().nullable(),
  p99_ms: z.number().nullable(),
  max_ms: z.number().nullable(),
})
export type Hora = z.infer<typeof Hora>

// resolution discrimina o formato dos itens: o painel desenha pelo que o
// servidor decidiu, nunca pela janela que pediu (spec §9.1).
export const Serie = z.discriminatedUnion('resolution', [
  z.object({ resolution: z.literal('raw'), items: z.array(Check) }),
  z.object({ resolution: z.literal('1h'), items: z.array(Hora) }),
])
export type Serie = z.infer<typeof Serie>

export const Incidente = z.object({
  id,
  target_id: id,
  target_name: z.string(),
  started_at: instante,
  resolved_at: instante.nullable(),
  cause: z.string(),
  acknowledged_at: instante.nullable(),
})
export type Incidente = z.infer<typeof Incidente>

export const ListaDeIncidentes = z.object({ items: z.array(Incidente), next_cursor: z.string().optional() })
export type ListaDeIncidentes = z.infer<typeof ListaDeIncidentes>

export const Canal = z.object({
  id,
  kind: z.enum(['discord', 'email']),
  hint: z.string(),
  status: z.enum(['verified', 'pending', 'expired', 'failing']),
  enabled: z.boolean(),
  created_at: instante,
  last_error: z.string().nullable(),
  last_failure_at: instante.nullable(),
})
export type Canal = z.infer<typeof Canal>

export const ListaDeCanais = z.object({ items: z.array(Canal) })

export const ItemDaPagina = z.object({ target_id: id, display_name: z.string() })
export type ItemDaPagina = z.infer<typeof ItemDaPagina>

export const PaginaDeStatus = z.object({
  id,
  slug: z.string(),
  title: z.string(),
  published: z.boolean(),
  items: z.array(ItemDaPagina),
  public_url: z.string(),
  created_at: instante,
})
export type PaginaDeStatus = z.infer<typeof PaginaDeStatus>

export const ListaDePaginas = z.object({ items: z.array(PaginaDeStatus) })

export const Sessao = z.object({
  id,
  created_at: instante,
  last_used_at: instante,
  user_agent: z.string(),
  current: z.boolean(),
})
export type Sessao = z.infer<typeof Sessao>

export const ListaDeSessoes = z.object({ items: z.array(Sessao) })

export const CadastroTotp = z.object({ otpauth_uri: z.string().startsWith('otpauth://') })
export const ConfirmacaoTotp = z.object({ recovery_codes: z.array(z.string()) })
```

Crie `web/src/lib/api/sessao.ts`:

```ts
import { ApiError, enviar, lerCorpo, lerErro, type Buscar } from './http'
import { RespostaLogin, Token } from './schemas'

// A sessão do painel (spec §11.2; spec do Plano 6 §3.4).
//
// O access token vive numa variável deste módulo e em nenhum outro lugar:
// nada em localStorage, sessionStorage, IndexedDB ou estado React (spec
// §5.3). O refresh é um cookie HttpOnly que o JavaScript não lê; o boot o usa
// para re-hidratar a sessão depois de um F5.
//
// O refresh tem três camadas, e cada uma fecha um jeito de a sessão cair:
// 1. single-flight dentro da aba: cinco 401 juntos esperam UMA promessa;
// 2. Web Locks entre abas: abas do mesmo navegador dividem o cookie, e duas
//    renovando juntas apresentariam o mesmo refresh — a segunda é reúso, e o
//    servidor revoga a família inteira (spec §5.2). Com a trava, a segunda
//    só envia depois de o navegador guardar o cookie novo;
// 3. BroadcastChannel só com eventos, nunca com o token: "encerrada" faz as
//    outras abas largarem o token na hora, em vez de continuarem lendo dados
//    por até 15 minutos com o access token que têm.

export type Estado = 'verificando' | 'autenticado' | 'anonimo' | 'offline'
export type Motivo = 'saiu' | 'expirou' | 'encerrada-aqui' | 'outra-aba'

export type Travar = <T>(nome: string, fn: () => Promise<T>) => Promise<T>

export type Canal = {
  postMessage(msg: unknown): void
  addEventListener(tipo: 'message', fn: (e: MessageEvent) => void): void
}

export type Opcoes = {
  buscar: Buscar
  travar?: Travar
  canal?: Canal | null
  // Quanto um refresh espera antes de desistir. Uma rede travada não pode
  // segurar a trava de todas as abas.
  prazoMs?: number
}

export type ResultadoLogin = { tipo: 'ok' } | { tipo: 'totp'; desafio: string }

type Aviso = { tipo: 'encerrada' } | { tipo: 'iniciada' }

const TRAVA = 'vigilume:refresh'

export function travaDoNavegador(): Travar {
  if (typeof navigator !== 'undefined' && navigator.locks) {
    return (nome, fn) => navigator.locks.request(nome, fn)
  }
  return (_nome, fn) => fn()
}

export function canalDoNavegador(): Canal | null {
  return typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('vigilume:sessao')
}

export type Sessao = ReturnType<typeof criarSessao>

export function criarSessao(o: Opcoes) {
  const travar = o.travar ?? travaDoNavegador()
  const canal = o.canal ?? null
  const prazoMs = o.prazoMs ?? 10_000

  let token: string | null = null
  let estado: Estado = 'verificando'
  let motivo: Motivo | null = null
  let emVoo: Promise<string | null> | null = null
  let boot: Promise<void> | null = null
  // epoca muda a cada login e a cada fim de sessão. Um refresh que saiu
  // numa época e voltou em outra não mexe no token: sem isto, sair (ou
  // encerrar a sessão aqui) com um refresh em voo ressuscitava o token
  // quando a resposta chegasse.
  let epoca = 0
  const ouvintes = new Set<() => void>()

  function mudou() {
    for (const f of ouvintes) f()
  }

  function avisar(a: Aviso) {
    canal?.postMessage(a)
  }

  // Um refresh de verdade. null = o servidor recusou a sessão (401); erro de
  // rede ou 5xx sobe como ApiError, porque a sessão pode estar boa.
  async function renovarAgora(): Promise<string | null> {
    const minha = epoca
    const r = await enviar(o.buscar, {
      metodo: 'POST',
      caminho: '/auth/refresh',
      sinal: AbortSignal.timeout(prazoMs),
    })
    let novo: string | null = null
    if (r.status !== 401) {
      if (!r.ok) throw await lerErro(r)
      novo = (await lerCorpo(r, Token)).access_token
    }
    // Outra época: vale o que ela deixou (null depois de encerrar; o token
    // do login novo depois de entrar).
    if (minha !== epoca) return token
    if (novo !== null) token = novo
    return novo
  }

  // renovar é o que o cliente chama num 401. usado é o token com que a
  // requisição saiu: se outra requisição já renovou enquanto esta voava, não
  // há por que renovar de novo — basta repetir com o token atual.
  function renovar(usado: string | null): Promise<string | null> {
    if (token !== null && token !== usado) return Promise.resolve(token)
    emVoo ??= travar(TRAVA, renovarAgora).finally(() => {
      emVoo = null
    })
    return emVoo
  }

  function aceitar(novo: string) {
    epoca++
    token = novo
    estado = 'autenticado'
    motivo = null
    mudou()
    avisar({ tipo: 'iniciada' })
  }

  // encerrar larga a sessão deste lado: token, estado e, pelo canal, as
  // outras abas. Não chama o servidor: quem precisa (sair) chama antes.
  function encerrar(m: Motivo) {
    const avisarOutras = m !== 'outra-aba' && estado === 'autenticado'
    epoca++
    token = null
    estado = 'anonimo'
    motivo = m
    mudou()
    if (avisarOutras) avisar({ tipo: 'encerrada' })
  }

  // O boot é UMA promessa por carga de página, fora da árvore React: o
  // StrictMode monta os efeitos duas vezes, e dois refreshes com o mesmo
  // cookie seriam reúso (spec do Plano 6, D9).
  function iniciar(): Promise<void> {
    boot ??= (async () => {
      try {
        const t = await renovar(null)
        estado = t ? 'autenticado' : 'anonimo'
      } catch {
        estado = 'offline'
      }
      mudou()
    })()
    return boot
  }

  function tentarDeNovo(): Promise<void> {
    boot = null
    estado = 'verificando'
    mudou()
    return iniciar()
  }

  canal?.addEventListener('message', (e) => {
    const a = e.data as Aviso
    if (a?.tipo === 'encerrada' && estado === 'autenticado') encerrar('outra-aba')
    // Outra aba entrou: uma aba parada no /login entra junto, pelo cookie.
    if (a?.tipo === 'iniciada' && estado === 'anonimo') void tentarDeNovo()
  })

  async function entrar(email: string, senha: string): Promise<ResultadoLogin> {
    const r = await enviar(o.buscar, { metodo: 'POST', caminho: '/auth/login', corpo: { email, password: senha } })
    if (!r.ok) throw await lerErro(r)
    const d = await lerCorpo(r, RespostaLogin)
    if ('totp_required' in d) return { tipo: 'totp', desafio: d.challenge }
    aceitar(d.access_token)
    return { tipo: 'ok' }
  }

  async function verificarTotp(desafio: string, codigo: string): Promise<void> {
    const r = await enviar(o.buscar, {
      metodo: 'POST',
      caminho: '/auth/totp/verify',
      corpo: { challenge: desafio, code: codigo },
    })
    if (!r.ok) throw await lerErro(r)
    aceitar((await lerCorpo(r, Token)).access_token)
  }

  async function sair(): Promise<void> {
    try {
      await enviar(o.buscar, { metodo: 'POST', caminho: '/auth/logout' })
    } catch (e) {
      // Sem rede, o logout no servidor não acontece, mas a aba larga a
      // sessão do mesmo jeito: o refresh que ficou vence sozinho.
      if (!(e instanceof ApiError)) throw e
    }
    encerrar('saiu')
  }

  return {
    token: () => token,
    estado: () => estado,
    motivo: () => motivo,
    assinar(f: () => void) {
      ouvintes.add(f)
      return () => {
        ouvintes.delete(f)
      }
    },
    iniciar,
    tentarDeNovo,
    renovar,
    entrar,
    verificarTotp,
    sair,
    encerrar,
  }
}
```

Crie `web/src/lib/api/cliente.ts`:

```ts
import type { z } from 'zod'
import { enviar, lerCorpo, lerErro, type Buscar, type Pedido } from './http'
import {
  Alvo,
  CadastroTotp,
  Canal,
  ConfirmacaoTotp,
  ListaDeAlvos,
  ListaDeCanais,
  ListaDeIncidentes,
  ListaDePaginas,
  ListaDeSessoes,
  Incidente,
  Me,
  PaginaDeStatus,
  Resumo,
  Serie,
} from './schemas'
import type { Sessao } from './sessao'

// A regra do 401 (spec do Plano 6 §3.4; nota do Plano 5 §12; reconciliação,
// item 3): só "401 unauthorized" FORA de /auth/* renova, e a requisição
// repete UMA vez com o token novo. Refresh recusado, ou 401 de novo depois
// de renovar, é o fim da sessão.
//
// Em /auth/* um 401 nunca renova. "invalid_credentials" é senha ou código
// errado (em /auth/password, /auth/totp/enroll e /auth/totp/disable, D24 do
// Plano 5), e repetir reenviaria o erro e contaria outra falha no bloqueio
// progressivo.
function podeRenovar(caminho: string, code: string) {
  return code === 'unauthorized' && !caminho.startsWith('/auth/')
}

export type EntradaAlvo = {
  name: string
  url: string
  method: 'GET' | 'HEAD' | 'POST'
  interval_sec: number
  timeout_ms: number
  expected_status: number
  // Ausente = padrão do servidor (2000 ms); null = desativado (spec §11.5).
  degraded_threshold_ms?: number | null
  enabled: boolean
  headers?: Record<string, string> | null
}

export type AlteracaoAlvo = Partial<EntradaAlvo>

export type EntradaCanal = { kind: 'discord'; url: string } | { kind: 'email'; email: string }

export type EntradaPagina = {
  slug: string
  title: string
  published: boolean
  items: { target_id: string; display_name: string }[]
}

export type TrocaDeSenha = { current_password: string; new_password: string; totp_code?: string }

export type FiltroIncidentes = { status?: 'open' | 'resolved'; cursor?: string; targetId?: string; limit?: number }

function consulta(p: Record<string, string | number | undefined>) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

export type Api = ReturnType<typeof criarApi>

export function criarApi(sessao: Sessao, buscar: Buscar) {
  async function pedir<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T> | null): Promise<T> {
    const usado = sessao.token()
    let r = await enviar(buscar, { ...p, token: usado })
    if (r.status === 401) {
      const erro = await lerErro(r)
      // Sem token não há o que renovar: é uma requisição que ainda voava
      // quando a sessão acabou. Renovar aqui rodaria um refresh com o cookie
      // já apagado e trocaria o motivo do fim da sessão.
      if (usado === null || !podeRenovar(p.caminho, erro.code)) throw erro
      const novo = await sessao.renovar(usado)
      if (!novo) {
        // A sessão pode já ter acabado por outro motivo enquanto o refresh
        // voava (sair, encerrar aqui): o motivo que vale é o primeiro.
        if (sessao.estado() === 'autenticado') sessao.encerrar('expirou')
        throw erro
      }
      r = await enviar(buscar, { ...p, token: novo })
      if (r.status === 401) {
        const outro = await lerErro(r)
        if (podeRenovar(p.caminho, outro.code)) sessao.encerrar('expirou')
        throw outro
      }
    }
    if (!r.ok) throw await lerErro(r)
    if (esquema === null || r.status === 204) return undefined as T
    return lerCorpo(r, esquema)
  }

  const get = <T>(caminho: string, esquema: z.ZodType<T>) => pedir({ metodo: 'GET', caminho }, esquema)

  return {
    me: () => get('/me', Me),

    resumo: () => get('/targets/summary', Resumo),
    alvos: () => get('/targets?limit=100', ListaDeAlvos),
    alvo: (id: string) => get(`/targets/${encodeURIComponent(id)}`, Alvo),
    criarAlvo: (e: EntradaAlvo) => pedir({ metodo: 'POST', caminho: '/targets', corpo: e }, Alvo),
    alterarAlvo: (id: string, a: AlteracaoAlvo) =>
      pedir({ metodo: 'PATCH', caminho: `/targets/${encodeURIComponent(id)}`, corpo: a }, Alvo),
    apagarAlvo: (id: string) => pedir({ metodo: 'DELETE', caminho: `/targets/${encodeURIComponent(id)}` }, null),
    // Só from: o "agora" é do servidor, sem depender do relógio do navegador.
    serie: (id: string, desde: string) =>
      get(`/targets/${encodeURIComponent(id)}/checks${consulta({ from: desde })}`, Serie),

    incidentes: (f: FiltroIncidentes) =>
      get(
        `/incidents${consulta({ status: f.status, cursor: f.cursor, target_id: f.targetId, limit: f.limit })}`,
        ListaDeIncidentes,
      ),
    reconhecer: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/incidents/${encodeURIComponent(id)}/ack` }, Incidente),

    canais: () => get('/channels', ListaDeCanais),
    criarCanal: (e: EntradaCanal) => pedir({ metodo: 'POST', caminho: '/channels', corpo: e }, Canal),
    apagarCanal: (id: string) => pedir({ metodo: 'DELETE', caminho: `/channels/${encodeURIComponent(id)}` }, null),
    reenviarVerificacao: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/channels/${encodeURIComponent(id)}/verify/resend` }, null),

    paginas: () => get('/status-pages', ListaDePaginas),
    criarPagina: (e: EntradaPagina) => pedir({ metodo: 'POST', caminho: '/status-pages', corpo: e }, PaginaDeStatus),
    alterarPagina: (id: string, e: Partial<EntradaPagina>) =>
      pedir({ metodo: 'PATCH', caminho: `/status-pages/${encodeURIComponent(id)}`, corpo: e }, PaginaDeStatus),

    sessoes: () => get('/sessions', ListaDeSessoes),
    encerrarSessao: (id: string) => pedir({ metodo: 'DELETE', caminho: `/sessions/${encodeURIComponent(id)}` }, null),
    trocarSenha: (t: TrocaDeSenha) => pedir({ metodo: 'POST', caminho: '/auth/password', corpo: t }, null),

    totpCadastrar: (senha: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/enroll', corpo: { password: senha } }, CadastroTotp),
    totpConfirmar: (codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/confirm', corpo: { code: codigo } }, ConfirmacaoTotp),
    totpDesativar: (senha: string, codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/disable', corpo: { password: senha, code: codigo } }, null),
  }
}
```

Crie `web/src/lib/api/mensagens.ts`:

```ts
import { ApiError } from './http'

// Tradução de vocabulário fechado para texto. Nada do que o servidor manda
// além do code (e do trace_id) aparece na tela: a message do envelope é
// genérica de propósito (spec §9.1), e a tela sabe dizer melhor.

const porCodigo: Record<string, string> = {
  network: 'Sem conexão com o servidor. Confira a rede e tente de novo.',
  unexpected: 'O servidor respondeu algo inesperado.',
  internal_error: 'Erro interno do servidor.',
  unavailable: 'Serviço indisponível no momento.',
  rate_limited: 'Muitas requisições. Aguarde um minuto e tente de novo.',
  payload_too_large: 'Os dados enviados são grandes demais.',
  not_found: 'Não encontrado.',
  forbidden: 'Operação não permitida.',
  unauthorized: 'Sua sessão terminou. Entre de novo.',
  invalid_session: 'Sua sessão terminou. Entre de novo.',
  invalid_credentials: 'E-mail ou senha incorretos.',
  validation_failed: 'Confira os campos destacados.',
  target_limit_reached: 'Limite de 100 alvos atingido.',
  channel_limit_reached: 'Limite de canais atingido.',
  channel_not_pending: 'O canal não está mais aguardando verificação.',
  delivery_failed: 'Não conseguimos entregar a mensagem de teste. Confira o endereço.',
  status_page_exists: 'Você já tem uma status page.',
  slug_taken: 'Este endereço já está em uso.',
}

export function mensagemDe(e: unknown): string {
  if (e instanceof ApiError) {
    const base = porCodigo[e.code] ?? 'Não foi possível concluir.'
    // O trace_id liga a tela ao log do servidor (spec §9.1).
    if ((e.code === 'internal_error' || e.code === 'unexpected') && e.traceId) {
      return `${base} Código para suporte: ${e.traceId}.`
    }
    return base
  }
  return 'Algo deu errado no painel.'
}

const porMotivo: Record<string, string> = {
  required: 'Obrigatório.',
  invalid: 'Valor inválido.',
  out_of_range: 'Fora do intervalo permitido.',
  too_long: 'Longo demais.',
  too_short: 'Curto demais.',
  forbidden: 'Não permitido.',
  unavailable: 'Indisponível neste servidor.',
}

// Mensagens específicas por campo, quando o motivo sozinho diz pouco.
const porCampo: Record<string, Record<string, string>> = {
  url: {
    forbidden: 'Endereços internos (localhost, redes privadas) não podem ser monitorados.',
    invalid: 'Use uma URL http:// ou https:// completa.',
  },
  headers: { forbidden: 'Há um header controlado pelo monitor (Host, Content-Length…).' },
  timeout_ms: { out_of_range: 'Entre 1000 e 30000 ms, e menor que 90% do intervalo.' },
  interval_sec: { out_of_range: 'Entre 30 segundos e 1 dia.' },
  kind: { unavailable: 'O envio de e-mail não está configurado neste servidor.' },
  slug: { invalid: 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.' },
  new_password: { too_short: 'Mínimo de 15 caracteres.', too_long: 'Máximo de 128 caracteres.' },
}

export function mensagemDoCampo(campo: string, motivo: string): string {
  const raiz = campo.replace(/\[\d+\].*$/, '')
  return porCampo[raiz]?.[motivo] ?? porMotivo[motivo] ?? 'Valor inválido.'
}

// Os textos dos vocabulários do sondador e dos canais.
const tiposDeErro: Record<string, string> = {
  timeout: 'Tempo esgotado',
  dns: 'Falha de DNS',
  tls: 'Erro de TLS',
  conn_refused: 'Conexão recusada',
  http_status: 'Status HTTP inesperado',
  body_limit: 'Resposta grande demais',
  blocked: 'Destino bloqueado (endereço interno)',
}

export function textoDoErroDeCheck(kind: string | null, statusCode: number | null): string {
  if (!kind) return ''
  const base = tiposDeErro[kind] ?? 'Falha'
  return kind === 'http_status' && statusCode !== null ? `${base} (${statusCode})` : base
}

const errosDeCanal: Record<string, string> = {
  timeout: 'Tempo esgotado',
  network: 'Falha de rede',
  rate_limited: 'O destino limitou os envios (429)',
  rejected: 'O destino recusou',
  server_error: 'Erro no destino (5xx)',
  invalid_channel: 'Canal inválido (o webhook foi apagado?)',
  unknown: 'Erro desconhecido',
}

export function textoDoErroDeCanal(motivo: string): string {
  return errosDeCanal[motivo] ?? 'Erro desconhecido'
}
```

Crie `web/src/lib/api/index.ts`:

```ts
import { criarApi } from './cliente'
import { canalDoNavegador, criarSessao, travaDoNavegador } from './sessao'

// As instâncias do painel. Uma sessão por aba (o módulo é carregado uma vez),
// com a trava e o canal do navegador; os testes montam as deles.
const buscar = (entrada: string, init: RequestInit) => fetch(entrada, init)

export const sessao = criarSessao({ buscar, travar: travaDoNavegador(), canal: canalDoNavegador() })
export const api = criarApi(sessao, buscar)

export { ApiError } from './http'
export type { Api } from './cliente'
export type { Sessao } from './sessao'
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS. O build sai em `internal/webui/dist/` e deixa o `.gitkeep` lá (o plugin `vigilume:gitkeep`).

Run: `VIGILUME_EXIGIR_DIST=1 go test -count=1 -v -run TestTodoArquivoDoDistTemTipo ./internal/webui/`
Expected: PASS, agora conferindo os arquivos do build (sem a linha "nada a conferir"; na Task 1, o mesmo comando falhava).

Run: `git status --porcelain internal/webui/dist`
Expected: saída vazia — o build não deixou diff (o `.gitkeep` voltou) e o resto é ignorado.

Rode `npx vitest run src/lib/api` cinco vezes: os testes de abas dependem de um atraso de 20 ms, e o do refresh em voo, de um de 300 ms; têm de passar todas.

- [ ] **Step 7: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `sessao.ts`, troque `emVoo ??= travar(` por `emVoo = travar(`. Expected: FAIL em `cinco 401 simultâneos disparam um refresh só…` (5 refreshes, família revogada).
2. Em `sessao.ts`, troque `boot ??= (async` por `boot = (async` **e** faça a sabotagem 1. Expected: FAIL em `chamado duas vezes, renova uma vez só`. Só uma das duas fica verde: são duas guardas da mesma propriedade (o comentário do teste diz isso).
3. Em `sessao.ts`, troque `if (token !== null && token !== usado)` por `if (token !== null && token !== usado && Math.random() > 2)`. Expected: FAIL em `o 401 atrasado de um token antigo não dispara um segundo refresh`. (Apagar a linha não compila: `usado` sobra, e o `typecheck` recusa.)
4. Em `sessao.ts`, troque `if (avisarOutras) avisar` por `if (avisarOutras && Math.random() > 2) avisar`. Expected: FAIL em `sair numa aba faz a outra largar o token na hora` e em `o canal nunca leva o token` (a sentinela chega sem o aviso antes dela).
5. Em `cliente.ts`, troque `return code === 'unauthorized' && !caminho.startsWith('/auth/')` por `return code !== '' && caminho !== ''` (todo 401 renova). Expected: FAIL em `invalid_credentials na troca de senha não renova nem repete`, `outro 401 fora de /auth/* é erro, sem refresh` e `token vencido na troca de senha não renova: /auth/* nunca renova`.
6. Só a metade do código: troque `code === 'unauthorized'` por `code !== ''` na mesma linha. Expected: FAIL em `outro 401 fora de /auth/* é erro, sem refresh` (o `invalid_credentials` de `/auth/password` continua barrado pela outra metade).
7. Só a metade do caminho: troque `!caminho.startsWith('/auth/')` por `caminho !== ''`. Expected: FAIL em `token vencido na troca de senha não renova: /auth/* nunca renova`.
   As três sabotagens mantêm os dois parâmetros em uso: com um parâmetro sobrando, o `npm run typecheck` recusa (`noUnusedParameters`), e a sabotagem não seria o código que vai para o build.
8. Em `cliente.ts`, troque `if (usado === null || !podeRenovar(` por `if (!podeRenovar(`. Expected: FAIL em `401 de requisição sem token não dispara refresh nem troca o motivo`.
9. Em `http.ts`, apague a linha que põe `X-Requested-With`. Expected: FAIL em `mutações levam X-Requested-With` e em `toda mutação de autenticação leva X-Requested-With…` (403 da API falsa, como o `RequireCustomHeader`).
10. Em `http.ts`, dentro de `lerErro`, no `return` da resposta que não é o envelope, troque `'unexpected'` por `'internal_error'`. Expected: FAIL em `resposta de erro que não é o envelope vira "unexpected"` (o 502 com HTML).
11. Em `cliente.ts`, troque `return lerCorpo(r, esquema)` por `return esquema.parse(await r.json()) ?? lerCorpo(r, esquema)`. Expected: FAIL em `sucesso sem JSON válido ou fora do schema vira ApiError "unexpected", não exceção crua` (sai um `ZodError` cru).
12. Em `sessao.ts`, troque `if (minha !== epoca) return token` por `if (minha !== epoca && Math.random() > 2) return token`. Expected: FAIL em `sessão encerrada com um refresh em voo: o token não volta e o motivo fica` (o token volta).
13. Em `cliente.ts`, troque `if (sessao.estado() === 'autenticado') sessao.encerrar('expirou')` por `if (sessao.estado() !== 'verificando') sessao.encerrar('expirou')`. Expected: FAIL no mesmo teste (o motivo vira "expirou"). As sabotagens 12 e 13 são guardas diferentes do mesmo teste: cada uma, sozinha, o deixa vermelho.
14. Em `sessao.ts`, depois de `estado = t ? 'autenticado' : 'anonimo'`, acrescente `if (!t) avisar({ tipo: 'encerrada' })`. Expected: FAIL em `sem cookie, fica anônimo, sem avisar as outras abas` (a sentinela chega depois do aviso).
15. Em `sessao.ts`, troque `canal?.postMessage(a)` por `canal?.postMessage({ ...a, token })`. Expected: FAIL em `o canal nunca leva o token`.
16. Em `sessao.ts`, dentro de `sair()`, troque `if (!(e instanceof ApiError)) throw e` por `if (!(e instanceof ApiError) || e.code === 'network') throw e`. Expected: FAIL em `logout sem rede ainda larga a sessão local` (o fetch rejeita com `TypeError`, como sem rede; um 502 é resposta e nunca chegaria a este `catch`).
17. O teste `sem trava entre abas, a detecção de reúso derruba a família` é o controle: ele **tem** de passar, e prova que a API falsa reproduz a corrida que a trava fecha.

- [ ] **Step 8: Commit**

```bash
git add web/ internal/webui/dist/.gitkeep
git commit -m "feat(painel): projeto web e sessão com refresh single-flight, Web Locks entre abas e a regra do 401"
```

---

### Task 3: A casca do painel e o login

A árvore do painel: o contexto (sessão e API por contexto, para os testes montarem as deles; a regra de repetição das consultas), as rotas, a porta da sessão (splash no boot, nunca o `/login` piscando a cada F5), o layout (navegação com o contador de incidentes abertos, tema, sair, região `aria-live`, *skip link*, o aviso de contexto inseguro), o erro de rota (o chunk que sumiu depois de um deploy) e a tela de login com o passo do TOTP. O dashboard desta tarefa é provisório (só o título); ele chega na Task 4.

**Files:**
- Create: `web/src/app/contexto.tsx`, `web/src/app/rotas.tsx`, `web/src/app/Layout.tsx`, `web/src/app/ExigirSessao.tsx`, `web/src/app/ErroDeRota.tsx`, `web/src/app/NaoEncontrada.tsx`
- Create: `web/src/lib/tema.tsx`, `web/src/lib/api/consultas.ts`
- Create: `web/src/components/ui/botao.tsx`, `web/src/components/ui/campo.tsx`, `web/src/components/ui/avisos.tsx`, `web/src/components/Titulo.tsx`, `web/src/components/EstadoDaConsulta.tsx`, `web/src/components/Anunciador.tsx`, `web/src/components/AvisoDeContexto.tsx`
- Create: `web/src/features/login/PaginaLogin.tsx`, `web/src/features/painel/PaginaPainel.tsx` (provisório)
- Create: `web/src/test/renderizar.tsx`
- Modify: `web/src/main.tsx`
- Test: `web/src/features/login/login.test.tsx`, `web/src/app/erro-de-rota.test.tsx`, `web/src/app/contexto.test.ts`

**Interfaces:**
- Consumes: `criarSessao`, `criarApi`, `sessao`/`api` do navegador, `mensagemDe`, `ApiError` (Task 2).
- Produces:
  - `ProvedorDoPainel({ sessao, api, cliente?, children })`, `criarClienteDeConsultas()`, `podeRepetir(erro)`, `useApi()`, `useSessao()` → `{ sessao, estado, motivo }`.
  - `rotas: RouteObject[]` (cada tarefa seguinte acrescenta a sua).
  - `consultas.ts`: `chaves`, `useMe`, `useResumo`, `useAlvos`, `useAlvo`, `useSerie`, `useIncidentes(filtro, alvo?, limite?)`, `useAbertos()` (o contador da navegação), `useCanais`, `usePaginas`, `useSessoes`.
  - Componentes: `Botao({ tipo, tamanho, asChild })`, `Campo({ rotulo, ajuda, erro, children: (a11y) => … })`, `Entrada`, `Selecao`, `ErroDoFormulario`, `ProvedorDeAvisos`/`useAvisar()`, `Titulo({ aba })`, `esquecerAPrimeiraTela()` (para os testes), `EstadoDaConsulta({ consulta, children })`, `Esqueleto({ linhas? })`, `AvisoDeContexto()`, `ProvedorDeAnuncios`/`useAnunciar()`, `ProvedorDeTema`/`useTema()`.
  - `destinoSeguro(next)` (em `PaginaLogin.tsx`).
  - Teste: `renderizarPainel(rota, { logado?, atrasoDoBoot? })` → `{ falsa, sessao, api, cliente, router, usuario, tela }`.

**Contexto para quem implementa.** O boot (`sessao.iniciar()`) é disparado no `main.tsx`, **fora** da árvore React: o `StrictMode` monta os efeitos duas vezes (spec do Plano 6, D9). Quando a sessão acaba, o `ProvedorDoPainel` limpa o cache do TanStack Query: nada da sessão anterior sobra para a próxima. O texto de senha errada é sempre o mesmo, também para conta bloqueada (R34 do Plano 1). O `?next=` só é seguido se for caminho do painel (open redirect). Os dois formulários do login têm `key` (D6). O `Titulo` muda o título da aba e foca o `<h1>` **depois de uma navegação dentro do painel** (spec §3.7), mas não na carga da página: aí o primeiro Tab é o "Pular para o conteúdo" (§4.9; D20). A regra é um valor do módulo (uma carga, uma primeira tela), decidido uma vez por montagem num `ref` — o `StrictMode` roda o efeito duas vezes, e a segunda não pode achar que já não é a primeira tela. `renderizarPainel` chama `esquecerAPrimeiraTela()`: cada render de teste é uma carga nova. O teste da navegação vai a uma rota sem tela (`/sem-tela`, D12). **Contexto seguro é exigido** (I3 da revisão): fora de HTTPS ou localhost o navegador não tem `navigator.locks`, e duas abas derrubam a sessão; não há trava substituta, e o `AvisoDeContexto` diz isso na tela de entrada e no layout. **Carregando** é um esqueleto (spec §4), que para com `prefers-reduced-motion`; **dados velhos** ficam na tela quando um poll de fundo falha, com o aviso e "Tentar de novo" (em vez de trocar o painel inteiro pela mensagem de erro). O `QueryClient` só repete rede caída e 5xx (`podeRepetir`): 4xx, resposta fora do contrato (200 `unexpected`) e erro que não é `ApiError` não melhoram repetindo. O contador de incidentes abertos (spec §4.9) é uma página de até 100 abertos, a cada 30 s, sob a chave `['incidents', …]`: o reconhecer da Task 7 o atualiza. O teste de F5 confere **um** refresh com a árvore sob `StrictMode` (spec §7.2; o boot fora da árvore é o D9 da spec). `renderizarPainel(rota, { atrasoDoBoot })` não espera o boot quando há atraso: é como o teste vê o splash. No passo do código, `invalid_credentials` diz "Código incorreto. Se a senha mudou há pouco, volte e entre de novo.": com a Task 11 do Plano 5, uma troca de senha depois do desafio faz o verify recusar também o código certo (e um código de recuperação pode ter sido gasto ali); o teste troca a senha de verdade, por outra sessão, no meio do login. O teste de login leva a uma rota que não existe (`/sem-tela`) para provar o destino guardado sem depender de telas futuras (D12).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/test/renderizar.tsx`:

```tsx
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { criarClienteDeConsultas, ProvedorDoPainel } from '@/app/contexto'
import { rotas } from '@/app/rotas'
import { esquecerAPrimeiraTela } from '@/components/Titulo'
import { ProvedorDeAvisos } from '@/components/ui/avisos'
import { ProvedorDeTema } from '@/lib/tema'
import { EMAIL, SENHA } from './api-falsa'
import { novaSessao, usarApiFalsa } from './ambiente'

// Monta o painel inteiro (rotas, provedores, sessão) numa rota, contra a API
// falsa. logado: o navegador tem um cookie de refresh válido, e o boot entra.
// atrasoDoBoot: quanto o refresh do boot demora (para ver o splash).
export async function renderizarPainel(rota: string, o: { logado?: boolean; atrasoDoBoot?: number } = {}) {
  // Cada render é uma carga de página nova: a primeira tela não recebe foco.
  esquecerAPrimeiraTela()
  const falsa = usarApiFalsa()
  if (o.logado ?? true) falsa.logado()
  falsa.estado.atrasoRefresh = o.atrasoDoBoot ?? 0
  const { sessao, api } = novaSessao()
  const cliente = criarClienteDeConsultas()
  // Nos testes, erro é erro: sem as tentativas que escondem um 500.
  cliente.setDefaultOptions({
    ...cliente.getDefaultOptions(),
    queries: { ...cliente.getDefaultOptions().queries, retry: false },
  })
  const router = createMemoryRouter(rotas, { initialEntries: [rota] })
  const usuario = userEvent.setup()
  const tela = render(
    <StrictMode>
      <ProvedorDoPainel sessao={sessao} api={api} cliente={cliente}>
        <ProvedorDeTema>
          <ProvedorDeAvisos>
            <RouterProvider router={router} />
          </ProvedorDeAvisos>
        </ProvedorDeTema>
      </ProvedorDoPainel>
    </StrictMode>,
  )
  const boot = sessao.iniciar()
  if (!o.atrasoDoBoot) await boot
  return { falsa, sessao, api, cliente, router, usuario, tela }
}

export { EMAIL, SENHA }
```

Crie `web/src/features/login/login.test.tsx`:

```tsx
import { screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { RECUPERACAO_OK, TOTP_OK } from '@/test/api-falsa'
import { novaSessao } from '@/test/ambiente'
import { EMAIL, renderizarPainel, SENHA } from '@/test/renderizar'
import { destinoSeguro } from './PaginaLogin'

describe('destinoSeguro', () => {
  // Duas regras, duas sabotagens previstas: trocar a comparação de origem
  // por "if (Math.random() > 2)" → "//evil.example" passa; tirar a
  // faixa de controle da regex (deixar só "/[\\]/") → "/alvos\u0007" passa.
  it('só segue caminho do painel', () => {
    expect(destinoSeguro('/targets/x?janela=7d')).toBe('/targets/x?janela=7d')
    const ruins = [
      '//evil.example',
      '/\\evil.example',
      '/\t/evil.example',
      '/\n/evil.example',
      '/alvos\u0007',
      'https://evil.example',
      'javascript:alert(1)',
      '',
      null,
    ]
    for (const ruim of ruins) expect({ ruim, destino: destinoSeguro(ruim) }).toEqual({ ruim, destino: '/' })
  })
})

// I3 da revisão: fora de contexto seguro não há navigator.locks, e duas abas
// derrubam a sessão. O painel avisa, sem trava substituta. Sabotagem
// prevista: em AvisoDeContexto, trocar "|| window.isSecureContext" por
// "|| !window.isSecureContext".
describe('contexto seguro', () => {
  afterEach(() => {
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  })

  it('sem HTTPS (nem localhost), avisa na tela de entrada e no painel', async () => {
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    const r = await renderizarPainel('/login', { logado: false })
    expect(await screen.findByRole('alert')).toHaveTextContent('Este painel precisa de HTTPS')
    r.tela.unmount()
    await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(screen.getByRole('alert')).toHaveTextContent('com mais de uma aba aberta, a sessão cai')
  })

  it('com contexto seguro, nada de aviso', async () => {
    await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('entrar', () => {
  it('sem sessão, a rota protegida leva ao login guardando o destino', async () => {
    const { router } = await renderizarPainel('/incidents', { logado: false })
    expect(await screen.findByRole('heading', { name: 'Entrar no Vigilume' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.search).toBe('?next=%2Fincidents')
  })

  // Spec §11.2, item 1: enquanto o boot verifica o cookie, um splash — nunca
  // o login, que piscaria a cada F5. Sabotagem prevista: tirar o ramo
  // "verificando" de ExigirSessao.
  it('durante o boot, mostra o splash, e não o login nem o painel', async () => {
    await renderizarPainel('/', { atrasoDoBoot: 200 })
    expect(screen.getByText('Carregando o Vigilume…')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Entrar no Vigilume' })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
  })

  // A árvore dos testes roda sob StrictMode, como a de produção (spec §7.2).
  // D9: o boot fica fora da árvore (main.tsx), e a carga inteira renova UMA
  // vez. Sabotagem prevista, a do boot dentro de efeito sem guarda: em
  // ExigirSessao, "useEffect(() => void sessao.iniciar(), [sessao])" MAIS a
  // sabotagem 2 da Task 2 (as duas guardas de sessao.ts) → 3 refreshes com o
  // mesmo cookie. Só o efeito, com as guardas, fica verde: elas existem para isso.
  it('com o cookie, o F5 entra direto, sem passar pelo login, com um refresh só', async () => {
    const { router, falsa } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: /^(Painel|Entrar no Vigilume)$/ })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(screen.getByRole('heading', { name: 'Painel' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
  })

  it('senha certa leva ao destino guardado', async () => {
    const { usuario, router } = await renderizarPainel('/login?next=%2Fsem-tela%3Fx%3D1', { logado: false })
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    expect(await screen.findByRole('heading', { name: 'Página não encontrada' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/sem-tela')
    expect(router.state.location.search).toBe('?x=1')
  })

  // R34 do Plano 1: o bloqueio não se anuncia. A tela mostra sempre o mesmo
  // texto, e o campo de senha é limpo.
  it('senha errada mostra o texto genérico', async () => {
    const { usuario } = await renderizarPainel('/login', { logado: false })
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), 'errada')
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('E-mail ou senha incorretos.')
    expect(screen.getByLabelText('Senha')).toHaveValue('')
  })

  it('com 2FA, pede o código; aceita também o de recuperação', async () => {
    for (const codigo of [TOTP_OK, RECUPERACAO_OK]) {
      const { usuario, falsa, tela } = await renderizarPainel('/login', { logado: false })
      falsa.estado.totpLigado = true
      await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
      await usuario.type(screen.getByLabelText('Senha'), SENHA)
      await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
      if (codigo === RECUPERACAO_OK) {
        await usuario.click(await screen.findByRole('button', { name: 'Usar um código de recuperação' }))
        await usuario.type(screen.getByLabelText('Código de recuperação'), codigo)
      } else {
        await usuario.type(await screen.findByLabelText('Código do aplicativo autenticador'), codigo)
      }
      await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
      expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
      tela.unmount()
    }
  })

  it('código errado diz "Código incorreto" e continua no passo do código', async () => {
    const { usuario, falsa } = await renderizarPainel('/login', { logado: false })
    falsa.estado.totpLigado = true
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await usuario.type(await screen.findByLabelText('Código do aplicativo autenticador'), '000000')
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Código incorreto.')
    expect(screen.getByLabelText('Código do aplicativo autenticador')).toBeInTheDocument()
  })

  // Task 11 do Plano 5: a senha trocada (em outro aparelho) depois do
  // desafio faz o verify responder invalid_credentials, também com o código
  // certo. A tela sugere voltar e entrar de novo. Sabotagem prevista: tirar
  // a segunda frase da mensagem de invalid_credentials do verificar().
  it('código certo recusado porque a senha mudou depois do desafio: sugere entrar de novo', async () => {
    const { usuario, falsa } = await renderizarPainel('/login', { logado: false })
    const outra = novaSessao()
    await outra.sessao.entrar(EMAIL, SENHA)
    falsa.estado.totpLigado = true
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await screen.findByLabelText('Código do aplicativo autenticador')
    await outra.api.trocarSenha({
      current_password: SENHA,
      new_password: 'outra senha bem comprida',
      totp_code: TOTP_OK,
    })
    await usuario.type(screen.getByLabelText('Código do aplicativo autenticador'), TOTP_OK)
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Se a senha mudou há pouco, volte e entre de novo.')
    expect(falsa.chamadas('POST /auth/totp/verify')).toBe(1)
  })
})

describe('sair', () => {
  it('Sair volta ao login e o cache da sessão some', async () => {
    const { usuario, cliente, router } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(cliente.getQueryCache().getAll().length).toBeGreaterThan(0)
    await usuario.click(screen.getByRole('button', { name: 'Sair' }))
    await screen.findByRole('heading', { name: 'Entrar no Vigilume' })
    expect(router.state.location.pathname).toBe('/login')
    await waitFor(() => expect(cliente.getQueryCache().getAll()).toEqual([]))
  })

  it('sessão encerrada em outra aba volta ao login com o aviso', async () => {
    const { sessao } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    sessao.encerrar('outra-aba')
    expect(await screen.findByText('Você saiu em outra aba.')).toBeInTheDocument()
  })
})

// Spec §4.9 e §3.7 juntos: na carga da página, o primeiro Tab é o "Pular para
// o conteúdo"; depois de uma navegação dentro do painel, o foco vai ao <h1>
// da tela nova. Sabotagens previstas, uma por metade: em Titulo, trocar
// "focar.current = !primeiraTela" por "focar.current = !primeiraTela || true"
// (a carga foca o título, e o primeiro Tab sai dele); e trocar
// "if (focar.current)" por "if (focar.current && Math.random() > 2)" (a
// navegação não foca).
describe('foco', () => {
  it('na carga da página, o primeiro Tab é o "Pular para o conteúdo"', async () => {
    const r = await renderizarPainel('/')
    const titulo = await screen.findByRole('heading', { name: 'Painel' })
    expect(titulo).not.toHaveFocus()
    await r.usuario.tab()
    expect(screen.getByRole('link', { name: 'Pular para o conteúdo' })).toHaveFocus()
  })

  it('depois de navegar dentro do painel, o foco vai ao título da tela nova', async () => {
    const r = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    // Uma tela que existe desde esta tarefa (D12): a de página não encontrada.
    await r.router.navigate('/sem-tela')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Página não encontrada', level: 1 })).toHaveFocus())
  })
})
```

Crie `web/src/app/erro-de-rota.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { expect, it } from 'vitest'
import { ErroDeRota } from './ErroDeRota'

// Depois de um deploy, uma aba antiga pede o chunk velho; o handler Go
// responde 404 (e não o index), e o import() dinâmico falha com TypeError.
// A tela oferece recarregar em vez de ficar em branco.
it('chunk que não carrega vira "Há uma versão nova do painel"', async () => {
  const router = createMemoryRouter([
    {
      path: '/',
      ErrorBoundary: ErroDeRota,
      lazy: async () => {
        throw new TypeError('Failed to fetch dynamically imported module: /assets/PaginaDetalhe-velho.js')
      },
    },
  ])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Há uma versão nova do painel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Recarregar' })).toBeInTheDocument()
})

it('outro erro não finge ser deploy', async () => {
  const router = createMemoryRouter([
    {
      path: '/',
      ErrorBoundary: ErroDeRota,
      Component: () => {
        throw new Error('bug')
      },
    },
  ])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Algo deu errado' })).toBeInTheDocument()
})
```

Crie `web/src/app/contexto.test.ts`:

```ts
import { expect, it } from 'vitest'
import { ApiError } from '@/lib/api/http'
import { criarClienteDeConsultas, podeRepetir } from './contexto'

// Sabotagem prevista: voltar à regra antiga,
// "!(e instanceof ApiError && e.status >= 400 && e.status < 500)" → o erro
// cru e o 200 fora do contrato passam a ser repetidos.
it('só rede caída e 5xx são repetidos', () => {
  expect(podeRepetir(new ApiError(0, 'network', ''))).toBe(true)
  expect(podeRepetir(new ApiError(502, 'unexpected', ''))).toBe(true)
  expect(podeRepetir(new ApiError(500, 'internal_error', ''))).toBe(true)
  expect(podeRepetir(new ApiError(200, 'unexpected', ''))).toBe(false)
  expect(podeRepetir(new ApiError(404, 'not_found', ''))).toBe(false)
  expect(podeRepetir(new ApiError(429, 'rate_limited', ''))).toBe(false)
  expect(podeRepetir(new TypeError('x'))).toBe(false)
})

it('o QueryClient do painel usa a regra, e para na segunda repetição', () => {
  const retry = criarClienteDeConsultas().getDefaultOptions().queries?.retry
  if (typeof retry !== 'function') throw new Error('retry não é função')
  const e500 = new ApiError(500, 'internal_error', '')
  expect(retry(0, e500)).toBe(true)
  expect(retry(1, e500)).toBe(true)
  expect(retry(2, e500)).toBe(false)
  expect(retry(0, new ApiError(200, 'unexpected', ''))).toBe(false)
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/app src/features`
Expected: FAIL nos três arquivos: `./contexto` (e `@/app/contexto`), `@/app/rotas`, `./ErroDeRota` e `./PaginaLogin` não existem.

- [ ] **Step 3: Os componentes de base**

Crie `web/src/components/ui/botao.tsx`:

```tsx
import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

const variantes = cva(
  'inline-flex items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap disabled:pointer-events-none disabled:opacity-60',
  {
    variants: {
      tipo: {
        primario: 'bg-primaria text-primaria-texto hover:opacity-90',
        secundario: 'border border-borda bg-fundo text-texto hover:bg-painel',
        perigo: 'border border-perigo bg-fundo text-perigo hover:bg-painel',
        fantasma: 'text-texto hover:bg-painel',
      },
      tamanho: { normal: 'h-9 px-4', pequeno: 'h-8 px-3' },
    },
    defaultVariants: { tipo: 'primario', tamanho: 'normal' },
  },
)

type Props = ComponentProps<'button'> & VariantProps<typeof variantes> & { asChild?: boolean }

export function Botao({ className, tipo, tamanho, asChild = false, type, ...props }: Props) {
  const Comp = asChild ? Slot.Root : 'button'
  return (
    <Comp
      className={cn(variantes({ tipo, tamanho }), className)}
      type={asChild ? undefined : (type ?? 'button')}
      {...props}
    />
  )
}
```

Crie `web/src/components/ui/campo.tsx`:

```tsx
import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export const estiloDeEntrada =
  'block w-full rounded-md border border-borda bg-fundo px-3 py-2 text-sm text-texto aria-invalid:border-perigo'

// Campo liga rótulo, ajuda e erro ao controle: aria-describedby e
// aria-invalid, como o leitor de tela precisa. O controle vem por função,
// que recebe as props de acessibilidade.
export function Campo({
  rotulo,
  ajuda,
  erro,
  children,
}: {
  rotulo: ReactNode
  ajuda?: ReactNode
  erro?: string
  children: (a11y: { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }) => ReactNode
}) {
  const id = useId()
  const idAjuda = `${id}-ajuda`
  const idErro = `${id}-erro`
  const descrito = [ajuda ? idAjuda : null, erro ? idErro : null].filter(Boolean).join(' ') || undefined
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm font-medium">
        {rotulo}
      </label>
      {children({ id, 'aria-describedby': descrito, 'aria-invalid': erro ? true : undefined })}
      {ajuda && (
        <p id={idAjuda} className="text-sm text-suave">
          {ajuda}
        </p>
      )}
      {erro && (
        <p id={idErro} className="text-sm text-perigo">
          {erro}
        </p>
      )}
    </div>
  )
}

export function Entrada({ className, ...props }: ComponentProps<'input'>) {
  return <input className={cn(estiloDeEntrada, className)} {...props} />
}

export function Selecao({ className, ...props }: ComponentProps<'select'>) {
  return <select className={cn(estiloDeEntrada, className)} {...props} />
}

// Erro do formulário inteiro (não de um campo): anunciado assim que aparece.
export function ErroDoFormulario({ children }: { children?: ReactNode }) {
  if (!children) return null
  return (
    <p role="alert" className="rounded-md border border-perigo p-3 text-sm text-perigo">
      {children}
    </p>
  )
}
```

Crie `web/src/components/ui/avisos.tsx`:

```tsx
import { Toast } from 'radix-ui'
import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

// Avisos passageiros ("Alvo salvo"). Radix Toast, e não o sonner: o sonner
// injeta um <style> sem suporte a nonce, que a CSP do painel bloqueia (o
// toast saía sem estilo, largura total no rodapé — spike, §8.1).

type Aviso = { id: number; texto: string; tipo: 'ok' | 'erro' }

const Contexto = createContext<(texto: string, tipo?: Aviso['tipo']) => void>(() => {})

export function ProvedorDeAvisos({ children }: { children: ReactNode }) {
  const [avisos, setAvisos] = useState<Aviso[]>([])
  const avisar = useCallback((texto: string, tipo: Aviso['tipo'] = 'ok') => {
    setAvisos((a) => [...a, { id: Date.now() + Math.random(), texto, tipo }])
  }, [])
  return (
    <Toast.Provider swipeDirection="right" label="Aviso">
      <Contexto.Provider value={avisar}>{children}</Contexto.Provider>
      {avisos.map((a) => (
        <Toast.Root
          key={a.id}
          type={a.tipo === 'erro' ? 'foreground' : 'background'}
          duration={6000}
          onOpenChange={(aberto) => {
            if (!aberto) setAvisos((l) => l.filter((x) => x.id !== a.id))
          }}
          className={`rounded-md border bg-fundo p-3 text-sm shadow ${a.tipo === 'erro' ? 'border-perigo' : 'border-borda'}`}
        >
          <Toast.Title>{a.texto}</Toast.Title>
        </Toast.Root>
      ))}
      <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" />
    </Toast.Provider>
  )
}

export function useAvisar() {
  return useContext(Contexto)
}
```

Crie `web/src/components/Titulo.tsx`:

```tsx
import { useEffect, useRef, type ReactNode } from 'react'

// A primeira tela da carga da página não recebe o foco: o primeiro Tab é o
// "Pular para o conteúdo" (spec do Plano 6 §4.9). Toda tela seguinte — uma
// navegação dentro do painel — recebe (§3.7). Um valor do módulo: uma carga
// de página, uma primeira tela.
let primeiraTela = true

// Para os testes, que montam o painel várias vezes no mesmo módulo.
export function esquecerAPrimeiraTela() {
  primeiraTela = true
}

// O <h1> de cada tela. Ao montar, muda o título da aba e, depois de uma
// navegação, recebe o foco: o React Router troca a tela sem recarregar, e
// sem isso o leitor de tela não percebe a navegação (spec do Plano 6 §3.7).
export function Titulo({ children, aba }: { children: ReactNode; aba: string }) {
  const ref = useRef<HTMLHeadingElement>(null)
  // Decidido uma vez por montagem: o StrictMode roda o efeito duas vezes, e
  // a segunda não pode achar que já não é a primeira tela.
  const focar = useRef<boolean | null>(null)
  useEffect(() => {
    document.title = `${aba} — Vigilume`
    if (focar.current === null) {
      focar.current = !primeiraTela
      primeiraTela = false
    }
    if (focar.current) ref.current?.focus()
  }, [aba])
  return (
    <h1 ref={ref} tabIndex={-1} className="text-2xl font-semibold outline-none">
      {children}
    </h1>
  )
}
```

Crie `web/src/components/EstadoDaConsulta.tsx`:

```tsx
import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { mensagemDe } from '@/lib/api/mensagens'
import { dataHora } from '@/lib/format'
import { Botao } from './ui/botao'

// O esqueleto da spec §4: blocos no lugar do conteúdo, nunca um spinner
// solto. A animação para com prefers-reduced-motion (motion-reduce, e a regra
// global do index.css); o leitor de tela ouve "Carregando…".
export function Esqueleto({ linhas = 3 }: { linhas?: number }) {
  return (
    <div aria-busy="true" className="space-y-3">
      <span className="sr-only">Carregando…</span>
      {Array.from({ length: linhas }, (_, i) => (
        <div
          key={i}
          aria-hidden="true"
          className="h-16 animate-pulse rounded-lg bg-painel motion-reduce:animate-none"
        />
      ))}
    </div>
  )
}

// Carregando / erro / conteúdo, igual em toda tela. Uma consulta que já tem
// dados e falha num poll de fundo continua mostrando os dados, com o aviso
// de que estão velhos: esconder o painel inteiro por um 502 de passagem
// seria pior do que mostrar o que se sabia há 15 segundos.
export function EstadoDaConsulta<T>({
  consulta,
  children,
}: {
  consulta: Pick<UseQueryResult<T>, 'data' | 'error' | 'isPending' | 'refetch' | 'dataUpdatedAt'>
  children: (dados: T) => ReactNode
}) {
  if (consulta.isPending) return <Esqueleto />
  const tentar = (
    <Botao tipo="secundario" tamanho="pequeno" onClick={() => void consulta.refetch()}>
      Tentar de novo
    </Botao>
  )
  if (consulta.data === undefined) {
    return (
      <div role="alert" className="space-y-2 rounded-md border border-perigo p-4">
        <p>{mensagemDe(consulta.error)}</p>
        {tentar}
      </div>
    )
  }
  return (
    <>
      {consulta.error && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-borda p-3 text-sm">
          <p>
            Não foi possível atualizar agora ({mensagemDe(consulta.error)}). Mostrando os dados de{' '}
            {dataHora(new Date(consulta.dataUpdatedAt).toISOString())}.
          </p>
          {tentar}
        </div>
      )}
      {children(consulta.data)}
    </>
  )
}
```

Crie `web/src/components/Anunciador.tsx`:

```tsx
import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

// A região aria-live do painel (spec §11.6): uma só, no layout. Quem muda de
// estado anuncia aqui; o leitor de tela lê sem o foco sair do lugar.
const Contexto = createContext<(texto: string) => void>(() => {})

export function ProvedorDeAnuncios({ children }: { children: ReactNode }) {
  const [texto, setTexto] = useState('')
  const anunciar = useCallback((t: string) => {
    // Esvaziar antes garante que o mesmo texto, anunciado duas vezes, seja
    // lido duas vezes.
    setTexto('')
    requestAnimationFrame(() => setTexto(t))
  }, [])
  return (
    <Contexto.Provider value={anunciar}>
      {children}
      <output aria-live="polite" aria-atomic="true" className="sr-only" data-testid="anuncios">
        {texto}
      </output>
    </Contexto.Provider>
  )
}

export function useAnunciar() {
  return useContext(Contexto)
}
```

Crie `web/src/components/AvisoDeContexto.tsx`:

```tsx
// O painel EXIGE contexto seguro (https, ou http em localhost). Fora dele o
// navegador não oferece navigator.locks: duas abas renovando juntas
// apresentam o mesmo refresh, a detecção de reúso do servidor derruba a
// família, e a sessão cai. Não há trava substituta (localStorage não é
// exclusivo entre abas); o painel avisa, e quem opera põe HTTPS na frente.
export function AvisoDeContexto() {
  if (typeof window === 'undefined' || window.isSecureContext) return null
  return (
    <div role="alert" className="border-b border-perigo bg-painel px-4 py-3 text-sm">
      <p className="mx-auto max-w-6xl">
        <strong>Este painel precisa de HTTPS (ou de localhost).</strong> Sem conexão segura, o navegador não coordena a
        sessão entre abas: com mais de uma aba aberta, a sessão cai. Peça a quem opera o Vigilume para servir o painel
        por HTTPS.
      </p>
    </div>
  )
}
```

Crie `web/src/lib/tema.tsx`:

```tsx
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

// Tema: Sistema (prefers-color-scheme), Claro ou Escuro, persistido. É a
// única coisa que o painel guarda no navegador, e não é segredo (a spec §5.3
// proíbe o storage para tokens). /tema.js aplica o mesmo antes da primeira
// pintura, com a mesma chave.
export type Tema = 'sistema' | 'claro' | 'escuro'

const CHAVE = 'vigilume.tema'
const ESCURO = '(prefers-color-scheme: dark)'

function lerTema(): Tema {
  try {
    const v = localStorage.getItem(CHAVE)
    return v === 'claro' || v === 'escuro' ? v : 'sistema'
  } catch {
    return 'sistema'
  }
}

function aplicar(t: Tema) {
  const escuro = t === 'escuro' || (t === 'sistema' && window.matchMedia(ESCURO).matches)
  document.documentElement.classList.toggle('dark', escuro)
}

const Contexto = createContext<{ tema: Tema; mudar: (t: Tema) => void }>({ tema: 'sistema', mudar: () => {} })

export function ProvedorDeTema({ children }: { children: ReactNode }) {
  const [tema, setTema] = useState<Tema>(lerTema)

  useEffect(() => {
    aplicar(tema)
    if (tema !== 'sistema') return
    const mq = window.matchMedia(ESCURO)
    const ouvir = () => aplicar('sistema')
    mq.addEventListener('change', ouvir)
    return () => mq.removeEventListener('change', ouvir)
  }, [tema])

  const mudar = useCallback((t: Tema) => {
    try {
      localStorage.setItem(CHAVE, t)
    } catch {
      // Sem storage (modo privado restrito), o tema vale só nesta aba.
    }
    setTema(t)
  }, [])

  return <Contexto.Provider value={{ tema, mudar }}>{children}</Contexto.Provider>
}

export function useTema() {
  return useContext(Contexto)
}
```

- [ ] **Step 4: O contexto, as consultas e as rotas**

Crie `web/src/app/contexto.tsx`:

```tsx
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from 'react'
import type { Api } from '@/lib/api/cliente'
import { ApiError } from '@/lib/api/http'
import type { Sessao } from '@/lib/api/sessao'

type Painel = { sessao: Sessao; api: Api }

const Contexto = createContext<Painel | null>(null)

// Só se repete o que pode melhorar repetindo: rede caída e 5xx (inclusive o
// 502 do proxy, que chega como 'unexpected' com status 502). 4xx não melhora:
// o 401 já foi tratado no cliente, e 404 ou 429 repetidos só gastam o limite
// por usuário. Resposta fora do contrato (status 200, 'unexpected') também
// não: o mesmo servidor responderia o mesmo. E um erro que não é ApiError é
// defeito do painel, não da rede.
export function podeRepetir(e: unknown): boolean {
  return e instanceof ApiError && (e.code === 'network' || e.status >= 500)
}

export function criarClienteDeConsultas() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (n, e) => podeRepetir(e) && n < 2,
        staleTime: 10_000,
        refetchOnWindowFocus: true,
        // Aba em segundo plano não consulta (spec §11.4).
        refetchIntervalInBackground: false,
      },
      mutations: { retry: false },
    },
  })
}

// Quando a sessão acaba, nada da sessão anterior pode sobrar no cache para
// a próxima (outro usuário no mesmo navegador, ou o mesmo depois de sair).
function LimparAoEncerrar({ sessao }: { sessao: Sessao }) {
  const qc = useQueryClient()
  const estado = useSyncExternalStore(sessao.assinar, sessao.estado)
  useEffect(() => {
    if (estado === 'anonimo') qc.clear()
  }, [estado, qc])
  return null
}

export function ProvedorDoPainel({
  sessao,
  api,
  cliente,
  children,
}: Painel & { cliente?: QueryClient; children: ReactNode }) {
  const [qc] = useState(() => cliente ?? criarClienteDeConsultas())
  return (
    <Contexto.Provider value={{ sessao, api }}>
      <QueryClientProvider client={qc}>
        <LimparAoEncerrar sessao={sessao} />
        {children}
      </QueryClientProvider>
    </Contexto.Provider>
  )
}

function usePainel(): Painel {
  const p = useContext(Contexto)
  if (!p) throw new Error('fora do ProvedorDoPainel')
  return p
}

export function useApi(): Api {
  return usePainel().api
}

export function useSessao() {
  const { sessao } = usePainel()
  const estado = useSyncExternalStore(sessao.assinar, sessao.estado)
  return { sessao, estado, motivo: sessao.motivo() }
}
```

Crie `web/src/lib/api/consultas.ts`:

```ts
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useApi } from '@/app/contexto'

// As chaves e os intervalos de atualização (spec §11.4; spec do Plano 6 §3.5).
export const chaves = {
  me: ['me'] as const,
  resumo: ['targets', 'summary'] as const,
  alvos: ['targets', 'list'] as const,
  alvo: (id: string) => ['targets', id] as const,
  serie: (id: string, janela: string) => ['targets', id, 'checks', janela] as const,
  incidentes: (filtro: string, alvo?: string) => ['incidents', filtro, alvo ?? ''] as const,
  canais: ['channels'] as const,
  paginas: ['status-pages'] as const,
  sessoes: ['sessions'] as const,
}

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: Infinity })
}

export function useResumo() {
  const api = useApi()
  return useQuery({ queryKey: chaves.resumo, queryFn: api.resumo, refetchInterval: 15_000 })
}

export function useAlvos() {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvos, queryFn: api.alvos })
}

export function useAlvo(id: string) {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvo(id), queryFn: () => api.alvo(id), refetchInterval: 60_000 })
}

export function useSerie(id: string, janela: string, desde: () => string) {
  const api = useApi()
  return useQuery({
    queryKey: chaves.serie(id, janela),
    queryFn: () => api.serie(id, desde()),
    refetchInterval: 60_000,
    placeholderData: keepPreviousData,
  })
}

export function useIncidentes(filtro: 'open' | 'resolved' | 'all', alvo?: string, limite?: number) {
  const api = useApi()
  return useInfiniteQuery({
    queryKey: chaves.incidentes(filtro, alvo),
    queryFn: ({ pageParam }) =>
      api.incidentes({
        status: filtro === 'all' ? undefined : filtro,
        cursor: pageParam,
        targetId: alvo,
        limit: limite,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: 30_000,
  })
}

// O contador de incidentes abertos da navegação (spec do Plano 6 §4.9): uma
// página de até 100 (o teto da rota); mais que isso vira "99+". A chave fica
// sob ['incidents'], e o reconhecer, que invalida ['incidents'], o atualiza.
export function useAbertos() {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('open'), 'contagem'],
    queryFn: () => api.incidentes({ status: 'open', limit: 100 }),
    refetchInterval: 30_000,
    select: (d) => (d.next_cursor ? '99+' : d.items.length > 99 ? '99+' : String(d.items.length)),
  })
}

export function useCanais() {
  const api = useApi()
  return useQuery({ queryKey: chaves.canais, queryFn: api.canais, refetchInterval: 60_000 })
}

export function usePaginas() {
  const api = useApi()
  return useQuery({ queryKey: chaves.paginas, queryFn: api.paginas })
}

export function useSessoes() {
  const api = useApi()
  return useQuery({ queryKey: chaves.sessoes, queryFn: api.sessoes })
}
```

Crie `web/src/app/ExigirSessao.tsx`:

```tsx
import { Navigate, Outlet, useLocation } from 'react-router'
import { Botao } from '@/components/ui/botao'
import { useSessao } from './contexto'

// A porta das rotas autenticadas. Enquanto o boot verifica o cookie, um
// splash — nunca o /login, que piscaria a cada F5 (spec §11.2, item 1).
export function ExigirSessao() {
  const { sessao, estado, motivo } = useSessao()
  const local = useLocation()

  if (estado === 'verificando') {
    return (
      <div className="grid min-h-screen place-items-center" aria-busy="true">
        <p className="text-suave">Carregando o Vigilume…</p>
      </div>
    )
  }
  if (estado === 'offline') {
    return (
      <div role="alert" className="mx-auto mt-24 max-w-md space-y-4 p-4 text-center">
        <h1 className="text-xl font-semibold">Não foi possível falar com o servidor</h1>
        <p className="text-suave">Sua sessão pode estar ativa; tente de novo em instantes.</p>
        <Botao onClick={() => void sessao.tentarDeNovo()}>Tentar de novo</Botao>
      </div>
    )
  }
  if (estado === 'anonimo') {
    const next = local.pathname + local.search
    const q = next === '/' ? '' : `?next=${encodeURIComponent(next)}`
    return <Navigate to={`/login${q}`} replace state={{ motivo }} />
  }
  return <Outlet />
}
```

Crie `web/src/app/ErroDeRota.tsx`:

```tsx
import { isRouteErrorResponse, useRouteError } from 'react-router'
import { Botao } from '@/components/ui/botao'

// Uma falha de import() dinâmico quase sempre é "houve deploy e o chunk
// antigo sumiu": o handler Go responde 404 para asset ausente (e não o
// index), e aqui o painel oferece recarregar em vez de ficar em branco.
export function ErroDeRota() {
  const erro = useRouteError()
  const chunk =
    erro instanceof TypeError && /dynamically imported module|Importing a module script failed/i.test(erro.message)
  const naoAchou = isRouteErrorResponse(erro) && erro.status === 404
  return (
    <div role="alert" className="mx-auto mt-24 max-w-md space-y-4 p-4 text-center">
      <h1 className="text-xl font-semibold">
        {chunk ? 'Há uma versão nova do painel' : naoAchou ? 'Página não encontrada' : 'Algo deu errado'}
      </h1>
      <Botao onClick={() => window.location.reload()}>Recarregar</Botao>
    </div>
  )
}
```

Crie `web/src/app/NaoEncontrada.tsx`:

```tsx
import { Link } from 'react-router'
import { Titulo } from '@/components/Titulo'

export function NaoEncontrada() {
  return (
    <div className="space-y-4">
      <Titulo aba="Página não encontrada">Página não encontrada</Titulo>
      <Link to="/" className="text-realce underline">
        Voltar ao painel
      </Link>
    </div>
  )
}
```

Crie `web/src/app/Layout.tsx`:

```tsx
import { NavLink, Outlet } from 'react-router'
import { ProvedorDeAnuncios } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Botao } from '@/components/ui/botao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { useTema, type Tema } from '@/lib/tema'
import { cn } from '@/lib/utils'
import { useSessao } from './contexto'

const links = [
  { para: '/', texto: 'Painel', fim: true },
  { para: '/incidents', texto: 'Incidentes', contador: true },
  { para: '/channels', texto: 'Canais' },
  { para: '/status-pages', texto: 'Status page' },
  { para: '/settings', texto: 'Configurações' },
]

function SeletorDeTema() {
  const { tema, mudar } = useTema()
  return (
    <label className="flex items-center gap-2 text-sm">
      Tema
      <select
        value={tema}
        onChange={(e) => mudar(e.target.value as Tema)}
        className="rounded-md border border-borda bg-fundo px-2 py-1"
      >
        <option value="sistema">Sistema</option>
        <option value="claro">Claro</option>
        <option value="escuro">Escuro</option>
      </select>
    </label>
  )
}

// O número de incidentes abertos ao lado de "Incidentes" (spec §4.9). Zero
// não aparece. O número é decorativo para o leitor de tela: o nome do link
// já diz "Incidentes, 2 abertos".
function ContadorDeAbertos({ n }: { n: string }) {
  return (
    <span aria-hidden="true" className="ml-1.5 rounded-full bg-fora px-1.5 text-xs font-semibold text-fundo">
      {n}
    </span>
  )
}

export function Layout() {
  const { sessao } = useSessao()
  const me = useMe()
  const abertos = useAbertos().data
  const contador = abertos && abertos !== '0' ? abertos : null
  return (
    <ProvedorDeAnuncios>
      <a
        href="#conteudo"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-fundo focus:p-2"
      >
        Pular para o conteúdo
      </a>
      <AvisoDeContexto />
      <header className="border-b border-borda">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 py-3">
          <span className="font-semibold">Vigilume</span>
          <nav aria-label="Principal" className="flex flex-wrap gap-1">
            {links.map((l) => (
              <NavLink
                key={l.para}
                to={l.para}
                end={l.fim}
                aria-label={
                  l.contador && contador
                    ? `${l.texto}, ${contador} ${contador === '1' ? 'aberto' : 'abertos'}`
                    : undefined
                }
                className={({ isActive }) =>
                  cn('rounded-md px-3 py-1.5 text-sm hover:bg-painel', isActive && 'bg-painel font-semibold')
                }
              >
                {l.texto}
                {l.contador && contador && <ContadorDeAbertos n={contador} />}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <SeletorDeTema />
            {me.data && <span className="text-sm text-suave">{me.data.email}</span>}
            <Botao tipo="secundario" tamanho="pequeno" onClick={() => void sessao.sair()}>
              Sair
            </Botao>
          </div>
        </div>
      </header>
      <main id="conteudo" className="mx-auto max-w-6xl space-y-6 px-4 py-6">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}
```

Crie `web/src/features/painel/PaginaPainel.tsx` (provisório):

```tsx
import { Titulo } from '@/components/Titulo'

// Provisório (Task 3): o dashboard chega na Task 4.
export function PaginaPainel() {
  return <Titulo aba="Painel">Painel</Titulo>
}
```

Crie `web/src/app/rotas.tsx`:

```tsx
import type { RouteObject } from 'react-router'
import { PaginaLogin } from '@/features/login/PaginaLogin'
import { PaginaPainel } from '@/features/painel/PaginaPainel'
import { ErroDeRota } from './ErroDeRota'
import { ExigirSessao } from './ExigirSessao'
import { Layout } from './Layout'
import { NaoEncontrada } from './NaoEncontrada'

// As rotas da spec §11.3. O detalhe do alvo (Recharts, quase metade do
// bundle) e as configurações (QR code) chegam sob demanda.
export const rotas: RouteObject[] = [
  { path: '/login', Component: PaginaLogin, ErrorBoundary: ErroDeRota },
  {
    Component: ExigirSessao,
    ErrorBoundary: ErroDeRota,
    children: [
      {
        Component: Layout,
        children: [
          { index: true, Component: PaginaPainel },
          { path: '*', Component: NaoEncontrada },
        ],
      },
    ],
  },
]
```

- [ ] **Step 5: O login e o `main.tsx`**

Crie `web/src/features/login/PaginaLogin.tsx`:

```tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'
import { useSessao } from '@/app/contexto'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Motivo } from '@/lib/api/sessao'

// Só caminho do painel. "//evil.example" e "/\evil" são URLs de outro host
// para o navegador; sem esta regra, o ?next= seria um open redirect. Duas
// regras: caractere de controle ou "\" em qualquer lugar recusa (o parser de
// URL descarta tab e quebra de linha, e "/\t/evil" viraria "//evil"); e o
// destino, resolvido contra a origem do painel, tem de continuar nela.
export function destinoSeguro(next: string | null): string {
  // Casar caractere de controle é o propósito desta regex.
  // oxlint-disable-next-line no-control-regex
  if (!next || !next.startsWith('/') || /[\u0000-\u001f\u007f\\]/.test(next)) return '/'
  try {
    if (new URL(next, location.origin).origin !== location.origin) return '/'
  } catch {
    return '/'
  }
  return next
}

const avisosDeMotivo: Partial<Record<Motivo, string>> = {
  expirou: 'Sua sessão terminou. Entre de novo.',
  'outra-aba': 'Você saiu em outra aba.',
  'encerrada-aqui': 'Você encerrou esta sessão.',
}

const esquemaSenha = z.object({
  email: z.string().trim().pipe(z.email('Informe um e-mail válido.')),
  // Sem a política de tamanho: ela vale só para senha nova, e o login aceita
  // a senha antiga mais curta (R5 do Plano 5).
  senha: z.string().min(1, 'Informe a senha.'),
})

const esquemaCodigo = z.object({ codigo: z.string().trim().min(6, 'Informe o código.') })

export function PaginaLogin() {
  const { sessao, estado } = useSessao()
  const [params] = useSearchParams()
  const local = useLocation()
  const navegar = useNavigate()
  const [desafio, setDesafio] = useState<string | null>(null)
  const [recuperacao, setRecuperacao] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const destino = destinoSeguro(params.get('next'))
  const motivo = (local.state as { motivo?: Motivo } | null)?.motivo

  const senha = useForm({ resolver: zodResolver(esquemaSenha), defaultValues: { email: '', senha: '' } })
  const codigo = useForm({ resolver: zodResolver(esquemaCodigo), defaultValues: { codigo: '' } })

  if (estado === 'autenticado') return <Navigate to={destino} replace />

  async function entrar(v: z.infer<typeof esquemaSenha>) {
    setErro(null)
    try {
      const r = await sessao.entrar(v.email, v.senha)
      if (r.tipo === 'totp') {
        setDesafio(r.desafio)
        return
      }
      navegar(destino, { replace: true })
    } catch (e) {
      // O mesmo texto para senha errada, e-mail inexistente e conta
      // bloqueada: o bloqueio não se anuncia (R34 do Plano 1).
      setErro(mensagemDe(e))
      senha.setValue('senha', '')
    }
  }

  async function verificar(v: z.infer<typeof esquemaCodigo>) {
    if (!desafio) return
    setErro(null)
    try {
      await sessao.verificarTotp(desafio, v.codigo.replace(/\s+/g, ''))
      navegar(destino, { replace: true })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'unauthorized') {
        // O desafio vive 5 minutos (auth.ChallengeTTL).
        setDesafio(null)
        senha.setValue('senha', '')
        setErro('O tempo para digitar o código acabou. Entre de novo.')
        return
      }
      // Task 11 do Plano 5: se a senha mudou depois deste desafio, o verify
      // responde invalid_credentials como para um código errado — e um
      // código de recuperação pode já ter sido gasto nele.
      setErro(
        e instanceof ApiError && e.code === 'invalid_credentials'
          ? 'Código incorreto. Se a senha mudou há pouco, volte e entre de novo.'
          : mensagemDe(e),
      )
      codigo.setValue('codigo', '')
    }
  }

  return (
    <>
      <AvisoDeContexto />
      <main className="mx-auto mt-16 max-w-sm space-y-6 px-4">
        <Titulo aba="Entrar">Entrar no Vigilume</Titulo>
        {motivo && avisosDeMotivo[motivo] && (
          <p className="rounded-md border border-borda p-3 text-sm">{avisosDeMotivo[motivo]}</p>
        )}
        <ErroDoFormulario>{erro}</ErroDoFormulario>
        {desafio === null ? (
          // key nos dois formulários: trocar de passo monta campos novos, em vez
          // de o React reaproveitar o <input> do e-mail para o código.
          <form key="senha" noValidate className="space-y-4" onSubmit={senha.handleSubmit(entrar)}>
            <Campo rotulo="E-mail" erro={senha.formState.errors.email?.message}>
              {(a) => <Entrada {...a} type="email" autoComplete="username" {...senha.register('email')} />}
            </Campo>
            <Campo rotulo="Senha" erro={senha.formState.errors.senha?.message}>
              {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...senha.register('senha')} />}
            </Campo>
            <Botao type="submit" className="w-full" disabled={senha.formState.isSubmitting}>
              Entrar
            </Botao>
          </form>
        ) : (
          <form key="codigo" noValidate className="space-y-4" onSubmit={codigo.handleSubmit(verificar)}>
            <Campo
              rotulo={recuperacao ? 'Código de recuperação' : 'Código do aplicativo autenticador'}
              ajuda={
                recuperacao ? '16 caracteres, cada código vale uma vez.' : 'Os 6 dígitos que o aplicativo mostra agora.'
              }
              erro={codigo.formState.errors.codigo?.message}
            >
              {(a) => (
                <Entrada
                  {...a}
                  autoFocus
                  autoComplete="one-time-code"
                  inputMode={recuperacao ? 'text' : 'numeric'}
                  {...codigo.register('codigo')}
                />
              )}
            </Campo>
            <Botao type="submit" className="w-full" disabled={codigo.formState.isSubmitting}>
              Verificar
            </Botao>
            <Botao tipo="fantasma" className="w-full" onClick={() => setRecuperacao((r) => !r)}>
              {recuperacao ? 'Usar o código do aplicativo' : 'Usar um código de recuperação'}
            </Botao>
          </form>
        )}
      </main>
    </>
  )
}
```

Aplique em `web/src/main.tsx`:

```diff
--- a/web/src/main.tsx
+++ b/web/src/main.tsx
@@ -2,13 +2,28 @@
 import './index.css'
 import { StrictMode } from 'react'
 import { createRoot } from 'react-dom/client'
+import { createBrowserRouter } from 'react-router'
+import { RouterProvider } from 'react-router/dom'
+import { ProvedorDeAvisos } from '@/components/ui/avisos'
+import { api, sessao } from '@/lib/api'
+import { ProvedorDeTema } from '@/lib/tema'
+import { ProvedorDoPainel } from './app/contexto'
+import { rotas } from './app/rotas'
+
+// O boot é disparado aqui, fora da árvore React: uma promessa por carga de
+// página, que o StrictMode não duplica (spec do Plano 6, D9).
+void sessao.iniciar()
+
+const router = createBrowserRouter(rotas)
 
-// Provisório (Task 2): a árvore do painel (rotas, sessão, provedores) chega
-// na Task 3. Este arquivo existe para o build produzir um dist de verdade.
 createRoot(document.getElementById('root')!).render(
   <StrictMode>
-    <main>
-      <h1>Vigilume</h1>
-    </main>
+    <ProvedorDoPainel sessao={sessao} api={api}>
+      <ProvedorDeTema>
+        <ProvedorDeAvisos>
+          <RouterProvider router={router} />
+        </ProvedorDeAvisos>
+      </ProvedorDeTema>
+    </ProvedorDoPainel>
   </StrictMode>,
 )
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 7: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `destinoSeguro`, troque `if (new URL(next, location.origin).origin !== location.origin) return '/'` por `if (Math.random() > 2) return '/'`. Expected: FAIL em `só segue caminho do painel` (`//evil.example` passa).
2. Em `destinoSeguro`, troque a regex `/[\u0000-\u001f\u007f\\]/` por `/[\\]/`. Expected: FAIL no mesmo teste (`/alvos\u0007` passa). São duas regras: `/\t/evil.example` fica vermelho nas duas (o parser de URL descarta o tab e vê `//evil.example`).
3. Em `ErroDeRota.tsx`, troque a expressão regular por `/nunca-casa/i`. Expected: FAIL em `chunk que não carrega vira "Há uma versão nova do painel"`.
4. Em `contexto.tsx`, troque `if (estado === 'anonimo') qc.clear()` por `if ((estado as string) === 'nunca') qc.clear()` (o `as string` é o que deixa a sabotagem compilar: o TypeScript recusa comparar com um literal fora do tipo). Expected: FAIL em `Sair volta ao login e o cache da sessão some`.
5. Em `ExigirSessao.tsx`, troque `if (estado === 'verificando') {` por `if ((estado as string) === 'nunca') {`. Expected: FAIL em `durante o boot, mostra o splash, e não o login nem o painel` (o layout aparece antes de o boot terminar; os dois testes do aviso de contexto também caem, pelo mesmo motivo).
6. Em `contexto.tsx`, troque o corpo de `podeRepetir` por `return !(e instanceof ApiError && e.status >= 400 && e.status < 500)` (a regra antiga). Expected: FAIL nos dois testes de `contexto.test.ts` (o erro cru e o 200 fora do contrato passam a ser repetidos).
7. Em `AvisoDeContexto.tsx`, troque `|| window.isSecureContext` por `|| !window.isSecureContext`. Expected: FAIL em `sem HTTPS (nem localhost), avisa…` e em `com contexto seguro, nada de aviso` (e nos testes que procuram o `alert` do login, que passa a ter dois).
8. O boot dentro de efeito, sem guarda (spec §7.5): em `ExigirSessao.tsx`, importe `useEffect` e acrescente `useEffect(() => void sessao.iniciar(), [sessao])` depois de `const local = useLocation()`, **e** faça a sabotagem 2 da Task 2. Expected: FAIL em `com o cookie, o F5 entra direto, sem passar pelo login, com um refresh só` ("expected 3 to be 1": o `StrictMode` monta o efeito duas vezes). Só o efeito, com as guardas da Task 2 no lugar, fica verde: elas existem para isso.
9. Em `Titulo.tsx`, troque `focar.current = !primeiraTela` por `focar.current = !primeiraTela || true`. Expected: FAIL em `na carga da página, o primeiro Tab é o "Pular para o conteúdo"` (o título tem o foco, e o Tab sai dele).
10. Em `Titulo.tsx`, troque `if (focar.current)` por `if (focar.current && Math.random() > 2)`. Expected: FAIL em `depois de navegar dentro do painel, o foco vai ao título da tela nova`.
11. Em `PaginaLogin.tsx`, troque `'Código incorreto. Se a senha mudou há pouco, volte e entre de novo.'` por `'Código incorreto.'`. Expected: FAIL em `código certo recusado porque a senha mudou depois do desafio: sugere entrar de novo`.

- [ ] **Step 8: Commit**

```bash
git add web/src
git commit -m "feat(painel): casca do painel, porta da sessão e login com o passo do TOTP"
```

---

### Task 4: Dashboard

A tela `/` da spec §11.3 com o `GET /targets/summary` do Plano 5: um cartão por alvo, estado com ícone e texto, latência, uptime de 24 h truncado, "verificado há…", minigráfico das 24 horas e o incidente aberto; um resumo em texto no topo; e o anúncio na região `aria-live` quando um alvo **muda** de estado.

**Files:**
- Create: `web/src/components/SeloDeEstado.tsx`, `web/src/components/Minigrafico.tsx`
- Create: `web/src/features/painel/transicoes.ts`
- Create: `web/src/test/fixtures.ts`
- Modify: `web/src/features/painel/PaginaPainel.tsx` (o provisório da Task 3 vira o dashboard)
- Test: `web/src/features/painel/painel.test.tsx`

**Interfaces:**
- Consumes: `useResumo`, `useAnunciar`, `EstadoDaConsulta`, `Titulo`, `Botao`, `porcento`, `milissegundos`, `haQuanto` (Tasks 2–3).
- Produces:
  - `SeloDeEstado({ estado })`, `type EstadoExibido = EstadoAlvo | 'pausado'`, `estadoExibido(estado, ativo)`, `textoDoEstado(e)`.
  - `Minigrafico({ horas, agora })`.
  - `transicoes(anterior, itens): string[]`, `mapaDeEstados(itens)`.
  - Fixtures: `alvo(p)`, `itemDoResumo(p)`, `incidente(p)`, `canal(p)`, `sessao(p)`, `me(p)`, `pagina(p)` — cada uma tipada com `z.input` do schema da resposta. O arquivo só importa **tipos**: o projeto `a11y` do E2E (Task 11) o carrega também.

**Contexto para quem implementa.** O servidor diz `unknown` também para alvo desativado; o painel distingue pelo `enabled` e mostra "Pausado". O minigráfico é SVG próprio (o Recharts é quase metade do bundle e fica fora do dashboard) e decorativo (`aria-hidden`): a informação está no texto do cartão. Hora ausente no `spark` é hora sem check — lacuna, não zero. O `uptime_24h` é número cru: a tela trunca. As fixtures ficam completas já aqui (as das Tasks 6–11 também), para que cada tarefa seguinte só as use. Os testes desta tarefa cobrem também duas peças da Task 3 que só aparecem com um dashboard de verdade: o contador de incidentes abertos na navegação (o nome do link é "Incidentes, 2 abertos"; zero não aparece) e os dados velhos que ficam na tela quando um poll falha.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/test/fixtures.ts`:

```ts
import type { z } from 'zod'
import type { Alvo, Canal, Incidente, ItemDoResumo, Me, PaginaDeStatus, Sessao } from '@/lib/api/schemas'

// Fixtures no formato do contrato: z.input dos schemas das respostas. Uma
// fixture fora do contrato não compila. Os testes de tela (Vitest) e o
// projeto a11y do E2E (e2e/a11y.spec.ts) usam as mesmas: só tipos importados,
// para o Playwright carregar este arquivo sem o alias "@/" em tempo de execução.

export function alvo(p: Partial<z.input<typeof Alvo>> = {}): z.input<typeof Alvo> {
  return {
    id: 'alvo-1',
    name: 'API',
    url: 'https://api.exemplo.com/health',
    method: 'GET',
    interval_sec: 60,
    timeout_ms: 10000,
    expected_status: 200,
    degraded_threshold_ms: 2000,
    enabled: true,
    header_names: [],
    created_at: '2026-09-20T10:00:00Z',
    ...p,
  }
}

export function itemDoResumo(p: Partial<z.input<typeof ItemDoResumo>> = {}): z.input<typeof ItemDoResumo> {
  return {
    id: 'alvo-1',
    name: 'API',
    enabled: true,
    state: 'up',
    last_checked_at: '2026-09-24T11:59:00Z',
    last_latency_ms: 123,
    uptime_24h: 0.99999,
    open_incident_id: null,
    spark: [{ bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 110 }],
    ...p,
  }
}

export function incidente(p: Partial<z.input<typeof Incidente>> = {}): z.input<typeof Incidente> {
  return {
    id: 'inc-1',
    target_id: 'alvo-1',
    target_name: 'API',
    started_at: '2026-09-24T11:00:00Z',
    resolved_at: null,
    cause: 'timeout',
    acknowledged_at: null,
    ...p,
  }
}

export function canal(p: Partial<z.input<typeof Canal>> = {}): z.input<typeof Canal> {
  return {
    id: 'canal-1',
    kind: 'discord',
    hint: 'discord.com/…/abcd',
    status: 'verified',
    enabled: true,
    created_at: '2026-09-20T10:00:00Z',
    last_error: null,
    last_failure_at: null,
    ...p,
  }
}

export function sessao(p: Partial<z.input<typeof Sessao>> = {}): z.input<typeof Sessao> {
  return {
    id: 'fam-1',
    created_at: '2026-09-20T10:00:00Z',
    last_used_at: '2026-09-24T11:50:00Z',
    user_agent: 'Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0',
    current: true,
    ...p,
  }
}

export function me(p: Partial<z.input<typeof Me>> = {}): z.input<typeof Me> {
  return { id: 'u1', email: 'dono@exemplo.com', totp_enabled: false, created_at: '2026-09-01T10:00:00Z', ...p }
}

export function pagina(p: Partial<z.input<typeof PaginaDeStatus>> = {}): z.input<typeof PaginaDeStatus> {
  return {
    id: 'pagina-1',
    slug: 'minha',
    title: 'Minha',
    published: true,
    items: [{ target_id: 'alvo-1', display_name: 'API' }],
    public_url: 'http://localhost/p/minha',
    created_at: '2026-09-20T10:00:00Z',
    ...p,
  }
}
```

Crie `web/src/features/painel/painel.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { ItemDoResumo } from '@/lib/api/schemas'
import { mapaDeEstados, transicoes } from './transicoes'

const resumido = (p: Parameters<typeof itemDoResumo>[0]) => ItemDoResumo.parse(itemDoResumo(p))

describe('transicoes', () => {
  // Sabotagem prevista: anunciar todo item a cada resposta (ignorar o
  // "antes !== agora") → o segundo caso passa a devolver frases.
  it('só anuncia mudança: nada na primeira carga, nada sem mudança', () => {
    const a = [resumido({ id: '1', name: 'API', state: 'up' })]
    expect(transicoes(null, a)).toEqual([])
    expect(transicoes(mapaDeEstados(a), a)).toEqual([])
    const b = [resumido({ id: '1', name: 'API', state: 'down' })]
    expect(transicoes(mapaDeEstados(a), b)).toEqual(['API: fora do ar'])
    expect(transicoes(mapaDeEstados(b), a)).toEqual(['API: voltou ao ar'])
    const pausado = [resumido({ id: '1', name: 'API', state: 'unknown', enabled: false })]
    expect(transicoes(mapaDeEstados(a), pausado)).toEqual(['API: pausado'])
  })
})

describe('painel', () => {
  it('sem alvos, oferece cadastrar o primeiro', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('link', { name: 'Cadastrar o primeiro alvo' })).toHaveAttribute('href', '/targets/new')
  })

  it('mostra os cinco estados com texto, e pausado no lugar de "sem dados" para alvo desativado', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'a', name: 'No ar', state: 'up', uptime_24h: 0.99999 }),
      itemDoResumo({ id: 'b', name: 'Lento', state: 'degraded' }),
      itemDoResumo({ id: 'c', name: 'Caiu', state: 'down', last_latency_ms: null, open_incident_id: 'inc-1' }),
      itemDoResumo({ id: 'd', name: 'Novo', state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] }),
      itemDoResumo({ id: 'e', name: 'Parado', state: 'unknown', enabled: false }),
    ]
    await r.cliente.refetchQueries()
    const cartao = (nome: string) => screen.getByRole('link', { name: nome }).closest('li') as HTMLElement
    await screen.findByRole('link', { name: 'No ar' })
    expect(within(cartao('No ar')).getByText('No ar', { selector: 'span' })).toBeInTheDocument()
    // Truncado: 0,99999 é "99,99%", nunca "100,00%".
    expect(within(cartao('No ar')).getByText('99,99%')).toBeInTheDocument()
    expect(within(cartao('Lento')).getByText('Degradado')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByText('Fora do ar')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByText('—')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByRole('link', { name: 'Incidente aberto' })).toBeInTheDocument()
    expect(within(cartao('Novo')).getByText('Sem dados')).toBeInTheDocument()
    expect(within(cartao('Novo')).getByText('nunca')).toBeInTheDocument()
    expect(within(cartao('Parado')).getByText('Pausado')).toBeInTheDocument()
    expect(screen.getByText('1 no ar, 1 degradado, 1 fora do ar, 1 sem dados, 1 pausado')).toBeInTheDocument()
    // O ícone é decorativo: o leitor de tela ouve só o texto.
    for (const svg of within(cartao('Caiu')).getAllByText('Fora do ar')[0]!.querySelectorAll('svg')) {
      expect(svg).toHaveAttribute('aria-hidden', 'true')
    }
  })

  it('anuncia na região aria-live quando um alvo cai, e não na primeira carga', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'up' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    const regiao = screen.getByTestId('anuncios')
    expect(regiao).toHaveTextContent('')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'down' })]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(regiao).toHaveTextContent('API: fora do ar'))
  })

  // Spec §4 e M9 da revisão: um poll de fundo que falha não esconde o que já
  // se sabia. Sabotagem prevista: em EstadoDaConsulta, trocar
  // "if (consulta.data === undefined)" por
  // "if (consulta.data === undefined || consulta.error)".
  it('um poll que falha mantém os cartões e avisa que os dados estão velhos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    r.falsa.estado.forcar.set('GET /targets/summary', { status: 502, code: 'unavailable' })
    await r.cliente.refetchQueries({ queryKey: ['targets', 'summary'] })
    expect(await screen.findByText(/Não foi possível atualizar agora/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })
})

describe('navegação', () => {
  // Spec §4.9: "Incidentes" com o contador de abertos; zero não aparece.
  it('mostra quantos incidentes estão abertos, e some quando não há', async () => {
    const r = await renderizarPainel('/')
    const link = await screen.findByRole('link', { name: 'Incidentes' })
    r.falsa.estado.incidentes = [
      incidente({ id: 'i1' }),
      incidente({ id: 'i2' }),
      incidente({ id: 'i3', resolved_at: '2026-09-24T11:30:00Z' }),
    ]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(screen.getByRole('link', { name: 'Incidentes, 2 abertos' })).toBe(link))
    r.falsa.estado.incidentes = []
    await r.cliente.refetchQueries()
    await waitFor(() => expect(link).toHaveAccessibleName('Incidentes'))
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/painel`
Expected: FAIL: `./transicoes` não existe.

- [ ] **Step 3: Implementar**

Crie `web/src/components/SeloDeEstado.tsx`:

```tsx
import { Circle, CircleDashed, Pause, Triangle, X } from 'lucide-react'
import type { EstadoAlvo } from '@/lib/api/schemas'
import { cn } from '@/lib/utils'

// Estado com ícone E texto, nunca só cor (spec §11.6): verde e vermelho são
// o par que a deuteranopia não separa. As formas também diferem.
export type EstadoExibido = EstadoAlvo | 'pausado'

const selos: Record<EstadoExibido, { texto: string; Icone: typeof Circle; cor: string; cheio: boolean }> = {
  up: { texto: 'No ar', Icone: Circle, cor: 'text-no-ar', cheio: true },
  degraded: { texto: 'Degradado', Icone: Triangle, cor: 'text-degradado', cheio: true },
  down: { texto: 'Fora do ar', Icone: X, cor: 'text-fora', cheio: false },
  unknown: { texto: 'Sem dados', Icone: CircleDashed, cor: 'text-sem-dados', cheio: false },
  pausado: { texto: 'Pausado', Icone: Pause, cor: 'text-sem-dados', cheio: false },
}

export function textoDoEstado(e: EstadoExibido) {
  return selos[e].texto
}

// O servidor diz "unknown" também para alvo desativado; o painel distingue
// pelo enabled (spec do Plano 6 §4.2).
export function estadoExibido(estado: EstadoAlvo, ativo: boolean): EstadoExibido {
  return ativo ? estado : 'pausado'
}

export function SeloDeEstado({ estado, className }: { estado: EstadoExibido; className?: string }) {
  const { texto, Icone, cor, cheio } = selos[estado]
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-sm font-medium', cor, className)}>
      <Icone aria-hidden="true" className="size-4" fill={cheio ? 'currentColor' : 'none'} strokeWidth={2.5} />
      {texto}
    </span>
  )
}
```

Crie `web/src/components/Minigrafico.tsx`:

```tsx
import type { HoraDoSpark } from '@/lib/api/schemas'

// As 24 horas do dashboard em SVG próprio: o Recharts fica fora do
// dashboard (quase metade do bundle — spec do Plano 6, D4). Decorativo: a
// informação está no texto do cartão. Hora sem check é lacuna, não zero.
export function Minigrafico({ horas, agora }: { horas: HoraDoSpark[]; agora: Date }) {
  const largura = 120
  const altura = 28
  const passo = largura / 24
  const inicio = Math.floor(agora.getTime() / 3_600_000) - 24
  const maior = Math.max(1, ...horas.map((h) => h.p50_ms ?? 0))
  return (
    <svg aria-hidden="true" width={largura} height={altura} viewBox={`0 0 ${largura} ${altura}`} className="overflow-visible">
      {horas.map((h) => {
        const i = Math.floor(new Date(h.bucket).getTime() / 3_600_000) - inicio
        if (i < 0 || i >= 24 || h.total === 0) return null
        const fracao = h.ok / h.total
        const alt = Math.max(3, ((h.p50_ms ?? maior) / maior) * altura)
        const cor = fracao >= 0.999 ? 'var(--estado-no-ar)' : fracao >= 0.99 ? 'var(--estado-degradado)' : 'var(--estado-fora)'
        return <rect key={h.bucket} x={i * passo + 0.5} y={altura - alt} width={passo - 1} height={alt} fill={cor} />
      })}
    </svg>
  )
}
```

Crie `web/src/features/painel/transicoes.ts`:

```ts
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/components/SeloDeEstado'
import type { ItemDoResumo } from '@/lib/api/schemas'

// O que anunciar entre duas respostas do resumo: só as MUDANÇAS de estado.
// Nada na primeira carga (anterior null) e nada num poll sem mudança — um
// leitor de tela lendo "API: no ar" a cada 15 s tornaria a região inútil.
export function transicoes(anterior: Map<string, EstadoExibido> | null, itens: ItemDoResumo[]): string[] {
  if (anterior === null) return []
  const frases: string[] = []
  for (const it of itens) {
    const antes = anterior.get(it.id)
    const agora = estadoExibido(it.state, it.enabled)
    if (antes !== undefined && antes !== agora) {
      frases.push(agora === 'up' && antes === 'down' ? `${it.name}: voltou ao ar` : `${it.name}: ${textoDoEstado(agora).toLowerCase()}`)
    }
  }
  return frases
}

export function mapaDeEstados(itens: ItemDoResumo[]): Map<string, EstadoExibido> {
  return new Map(itens.map((it) => [it.id, estadoExibido(it.state, it.enabled)]))
}
```

Aplique em `web/src/features/painel/PaginaPainel.tsx`:

```diff
--- a/web/src/features/painel/PaginaPainel.tsx
+++ b/web/src/features/painel/PaginaPainel.tsx
@@ -1,6 +1,105 @@
+import { useEffect, useRef } from 'react'
+import { Link } from 'react-router'
+import { useAnunciar } from '@/components/Anunciador'
+import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
+import { Minigrafico } from '@/components/Minigrafico'
+import { estadoExibido, SeloDeEstado, type EstadoExibido } from '@/components/SeloDeEstado'
 import { Titulo } from '@/components/Titulo'
+import { Botao } from '@/components/ui/botao'
+import { useResumo } from '@/lib/api/consultas'
+import type { ItemDoResumo } from '@/lib/api/schemas'
+import { haQuanto, milissegundos, porcento } from '@/lib/format'
+import { mapaDeEstados, transicoes } from './transicoes'
+
+function resumoEmTexto(itens: ItemDoResumo[]): string {
+  const conta = new Map<EstadoExibido, number>()
+  for (const it of itens) {
+    const e = estadoExibido(it.state, it.enabled)
+    conta.set(e, (conta.get(e) ?? 0) + 1)
+  }
+  const partes: string[] = []
+  const add = (e: EstadoExibido, um: string, varios: string) => {
+    const n = conta.get(e) ?? 0
+    if (n) partes.push(`${n} ${n === 1 ? um : varios}`)
+  }
+  add('up', 'no ar', 'no ar')
+  add('degraded', 'degradado', 'degradados')
+  add('down', 'fora do ar', 'fora do ar')
+  add('unknown', 'sem dados', 'sem dados')
+  add('pausado', 'pausado', 'pausados')
+  return partes.join(', ')
+}
+
+function Cartao({ it, agora }: { it: ItemDoResumo; agora: Date }) {
+  return (
+    <li className="space-y-2 rounded-lg border border-borda p-4">
+      <div className="flex items-start justify-between gap-2">
+        <Link to={`/targets/${it.id}`} className="font-semibold underline-offset-2 hover:underline">
+          {it.name}
+        </Link>
+        <SeloDeEstado estado={estadoExibido(it.state, it.enabled)} />
+      </div>
+      <dl className="grid grid-cols-2 gap-x-2 text-sm">
+        <dt className="text-suave">Latência</dt>
+        <dd>{milissegundos(it.last_latency_ms)}</dd>
+        <dt className="text-suave">No ar em 24 h</dt>
+        <dd>{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</dd>
+        <dt className="text-suave">Verificado</dt>
+        <dd>{it.last_checked_at ? haQuanto(it.last_checked_at, agora) : 'nunca'}</dd>
+      </dl>
+      <Minigrafico horas={it.spark} agora={agora} />
+      {it.open_incident_id && (
+        <Link to="/incidents?status=open" className="block text-sm text-fora underline">
+          Incidente aberto
+        </Link>
+      )}
+    </li>
+  )
+}
 
-// Provisório (Task 3): o dashboard chega na Task 4.
 export function PaginaPainel() {
-  return <Titulo aba="Painel">Painel</Titulo>
+  const consulta = useResumo()
+  const anunciar = useAnunciar()
+  const anterior = useRef<ReturnType<typeof mapaDeEstados> | null>(null)
+  const itens = consulta.data?.items
+
+  useEffect(() => {
+    if (!itens) return
+    const frases = transicoes(anterior.current, itens)
+    if (frases.length) anunciar(frases.join('. '))
+    anterior.current = mapaDeEstados(itens)
+  }, [itens, anunciar])
+
+  const agora = new Date(consulta.dataUpdatedAt)
+  return (
+    <>
+      <div className="flex flex-wrap items-center justify-between gap-4">
+        <Titulo aba="Painel">Painel</Titulo>
+        <Botao asChild>
+          <Link to="/targets/new">Cadastrar alvo</Link>
+        </Botao>
+      </div>
+      <EstadoDaConsulta consulta={consulta}>
+        {(d) =>
+          d.items.length === 0 ? (
+            <p className="rounded-lg border border-borda p-6 text-center">
+              Nenhum alvo ainda.{' '}
+              <Link to="/targets/new" className="text-realce underline">
+                Cadastrar o primeiro alvo
+              </Link>
+            </p>
+          ) : (
+            <>
+              <p className="text-suave">{resumoEmTexto(d.items)}</p>
+              <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
+                {d.items.map((it) => (
+                  <Cartao key={it.id} it={it} agora={agora} />
+                ))}
+              </ul>
+            </>
+          )
+        }
+      </EstadoDaConsulta>
+    </>
+  )
 }
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `transicoes.ts`, troque `if (antes !== undefined && antes !== agora) {` por `if (true) {`. Expected: FAIL em `só anuncia mudança: nada na primeira carga, nada sem mudança`.
2. Em `format.ts`, troque `Math.floor(fracao * 10000)` por `Math.round(fracao * 10000)`. Expected: FAIL em `mostra os cinco estados…` ("99,99%" vira "100,00%") e em `trunca em duas casas…`.
3. Em `SeloDeEstado.tsx`, troque `aria-hidden="true"` por `aria-hidden="false"` no ícone. Expected: FAIL em `mostra os cinco estados…`. (Tirar o atributo fica **verde**: o `lucide-react` já põe `aria-hidden="true"` nos ícones por padrão. São duas guardas da mesma propriedade; o teste prova a propriedade, e o atributo explícito fica para não depender do padrão da biblioteca — achado na revisão.)
4. Em `estadoExibido`, troque `return ativo ? estado : 'pausado'` por `return ativo || !ativo ? estado : 'pausado'`. Expected: FAIL (o alvo desativado mostra "Sem dados", não "Pausado"). (`return estado` não compila: `ativo` sobra.)
5. Em `EstadoDaConsulta.tsx`, troque `if (consulta.data === undefined) {` por `if (consulta.data === undefined || consulta.error) {`. Expected: FAIL em `um poll que falha mantém os cartões e avisa que os dados estão velhos` (e no do contador, que usa a mesma tela).
6. Em `Layout.tsx`, troque `const contador = abertos && abertos !== '0' ? abertos : null` por `const contador = abertos && Math.random() > 2 ? abertos : null`. Expected: FAIL em `mostra quantos incidentes estão abertos, e some quando não há`.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): dashboard com estado em ícone e texto, uptime truncado e anúncio de mudança"
```

---

### Task 5: Cadastro e edição de alvos

`/targets/new` e `/targets/:id/edit`. O formulário espelha `internal/targets/validate.go` para responder cedo, e o 400 com `fields` do servidor cai no campo certo do mesmo jeito (um nome que resolve para IP interno, só o servidor recusa). O limiar de lentidão tem três estados (padrão/personalizado/desativado ↔ ausente/número/`null`), e os headers da edição são "manter", "substituir todos" ou "remover todos": o servidor nunca devolve os valores.

**Files:**
- Create: `web/src/features/alvos/esquema.ts`, `web/src/features/alvos/FormularioAlvo.tsx`, `web/src/features/alvos/PaginasDoFormulario.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/alvos/alvos.test.tsx`

**Interfaces:**
- Consumes: `useApi`, `useAlvo`, `api.criarAlvo`, `api.alterarAlvo`, `EntradaAlvo`, `AlteracaoAlvo`, `mensagemDoCampo`, `Campo`/`Entrada`/`Selecao`, `useAvisar` (Tasks 2–3).
- Produces: `esquemaAlvo`, `type ValoresDoAlvo`, `valoresPadrao`, `valoresDe(alvo)`, `paraCriacao(v)`, `paraAlteracao(original, v)`, `campoDoServidor`, `INTERVALOS`, `OUTRO`, `escolhaDoIntervalo(s)`; `PaginaNovoAlvo`, `PaginaEditarAlvo`.

**Contexto para quem implementa.** A checagem de URL no cliente é um **subconjunto** da lista do `safedial`: nunca pode ser mais restritiva que o servidor (recusar aqui o que o servidor aceita seria um bug sem saída) — por isso `[::ffff:8.8.8.8]` passa. O timeout tem de ser menor que 90% do intervalo (D3 do Plano 2: o jitter de −10%). Os números vão sempre explícitos no POST; o limiar padrão vai ausente, que o servidor lê como 2000 ms. O PATCH leva só o que mudou (o servidor recusa campo desconhecido), e **nunca** a chave `headers` sem "substituir" ou "remover": `{}` apagaria os headers em silêncio. O `409 target_limit_reached` é o teto de 100 alvos do Plano 5 (D24). O intervalo é uma lista (30 s, 1, 5, 15 min, 1 h) **mais "Outro…"**, que mostra o campo em segundos (30 a 86400, spec §4.3): o seletor é `intervaloEscolhido`, só do formulário, e o número que vai ao servidor é sempre `interval_sec`. Um alvo com intervalo fora da lista abre em "Outro…" com o valor (`escolhaDoIntervalo`); sem isso, o `<select>` mostraria o primeiro item, e salvar sem mexer mudaria o intervalo.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/alvos/alvos.test.tsx`:

```tsx
import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { Alvo } from '@/lib/api/schemas'
import { esquemaAlvo, paraAlteracao, paraCriacao, valoresDe, valoresPadrao } from './esquema'

function erroDe(v: Partial<typeof valoresPadrao>, campo: string) {
  const r = esquemaAlvo.safeParse({ ...valoresPadrao, name: 'API', url: 'https://exemplo.com', ...v })
  return r.success ? undefined : r.error.issues.find((i) => i.path.join('.') === campo)?.message
}

describe('o espelho de validate.go', () => {
  it('timeout tem de ser menor que 90% do intervalo (D3 do Plano 2)', () => {
    expect(erroDe({ interval_sec: 30, timeout_ms: 27000 }, 'timeout_ms')).toBe('Tem de ser menor que 90% do intervalo.')
    expect(erroDe({ interval_sec: 30, timeout_ms: 26999 }, 'timeout_ms')).toBeUndefined()
  })

  it('endereço interno óbvio é recusado já no cliente; um público passa', () => {
    for (const u of ['http://localhost/x', 'http://127.0.0.1', 'http://10.1.2.3', 'http://[::1]/', 'http://169.254.169.254/']) {
      expect(erroDe({ url: u }, 'url')).toMatch(/internos/)
    }
    expect(erroDe({ url: 'ftp://exemplo.com' }, 'url')).toBe('Só http:// e https://.')
    // O cliente nunca pode ser mais restritivo que o servidor: IPv4 mapeado
    // público passa pelo safedial (Unmap), então passa aqui também.
    expect(erroDe({ url: 'http://[::ffff:8.8.8.8]/' }, 'url')).toBeUndefined()
    expect(erroDe({ url: 'https://vigilume-e2e.invalid/' }, 'url')).toBeUndefined()
  })

  it('headers: proibidos, repetidos e limites', () => {
    const h = (nome: string, valor = 'x') => ({ nome, valor })
    expect(erroDe({ headers: [h('Host')] }, 'headers.0.nome')).toBe('Este header é controlado pelo monitor.')
    expect(erroDe({ headers: [h('X-A'), h('x-a')] }, 'headers.1.nome')).toBe('Header repetido.')
    expect(erroDe({ headers: [h('com espaço')] }, 'headers.0.nome')).toBe('Nome de header inválido.')
    expect(erroDe({ headers: [h('X-A', 'v'.repeat(1025))] }, 'headers.0.valor')).toBe('Até 1 KiB.')
  })

  it('nome com 100 code points passa; com 101, não', () => {
    expect(erroDe({ name: 'é'.repeat(100) }, 'name')).toBeUndefined()
    expect(erroDe({ name: 'é'.repeat(101) }, 'name')).toBe('Até 100 caracteres.')
  })
})

describe('os corpos enviados', () => {
  it('criação: limiar padrão vai ausente, desativado vai null', () => {
    const v = { ...valoresPadrao, name: 'API', url: 'https://exemplo.com' }
    expect(paraCriacao(v)).not.toHaveProperty('degraded_threshold_ms')
    expect(paraCriacao({ ...v, lentidao: 'desativado' }).degraded_threshold_ms).toBeNull()
    expect(paraCriacao({ ...v, lentidao: 'personalizado', degraded_threshold_ms: 800 }).degraded_threshold_ms).toBe(800)
  })

  // D24: sem "substituir" ou "remover", o PATCH NÃO leva a chave headers.
  // Sabotagem prevista: sempre pôr "a.headers = mapaDeHeaders(v)" → {} vai
  // no corpo e apagaria os headers em silêncio.
  it('edição só manda o que mudou, e nunca "headers" sem pedir', () => {
    const original = valoresDe(Alvo.parse(alvo({ header_names: ['Authorization'] })))
    expect(paraAlteracao(original, { ...original, name: 'Outro' })).toEqual({ name: 'Outro' })
    expect(paraAlteracao(original, original)).toEqual({})
    expect(paraAlteracao(original, { ...original, modoHeaders: 'remover' })).toEqual({ headers: null })
    expect(
      paraAlteracao(original, { ...original, modoHeaders: 'substituir', headers: [{ nome: 'X-A', valor: '1' }] }),
    ).toEqual({ headers: { 'X-A': '1' } })
  })
})

describe('cadastrar', () => {
  async function preencher(usuario: Awaited<ReturnType<typeof renderizarPainel>>['usuario']) {
    await usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
  }

  it('cria e leva ao detalhe', async () => {
    const r = await renderizarPainel('/targets/new')
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/targets/alvo-1'))
    expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({
      name: 'Site',
      url: 'https://exemplo.com',
      method: 'GET',
      interval_sec: 60,
      timeout_ms: 10000,
      expected_status: 200,
      enabled: true,
    })
  })

  it('o 400 com fields vai para o campo certo', async () => {
    const r = await renderizarPainel('/targets/new')
    r.falsa.estado.forcar.set('POST /targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'url', reason: 'forbidden' }],
    })
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const url = screen.getByLabelText('URL')
    await waitFor(() => expect(url).toHaveAttribute('aria-invalid', 'true'))
    expect(url).toHaveAccessibleDescription(/Endereços internos/)
  })

  it('o teto de 100 alvos (Plano 5, D24) aparece como erro do formulário', async () => {
    const r = await renderizarPainel('/targets/new')
    r.falsa.estado.forcar.set('POST /targets', { status: 409, code: 'target_limit_reached' })
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de 100 alvos atingido.')
  })
})

describe('editar', () => {
  it('mostra os headers definidos sem valores, e salvar sem mexer não manda headers', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ header_names: ['Authorization', 'X-Chave'] })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Headers definidos: Authorization, X-Chave (valores ocultos).')).toBeInTheDocument()
    const nome = screen.getByLabelText('Nome')
    await r.usuario.clear(nome)
    await r.usuario.type(nome, 'API nova')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ name: 'API nova' })
  })

  // Spec §4.3: intervalo fora da lista ("Outro…", 30 a 86400 s). Um alvo com
  // 120 s abre com "Outro…" e o campo em segundos, e não com o primeiro item
  // da lista (que, salvo sem mexer, mudaria o intervalo em silêncio).
  // Sabotagem prevista: em escolhaDoIntervalo, trocar ": OUTRO" por ": '60'".
  it('intervalo fora da lista abre em "Outro…", com o valor, e só ele vai no PATCH', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ interval_sec: 120 })]
    await r.cliente.refetchQueries()
    const segundos = await screen.findByLabelText('Intervalo em segundos')
    expect(screen.getByLabelText('Intervalo')).toHaveDisplayValue('Outro…')
    expect(segundos).toHaveValue(120)
    await r.usuario.clear(segundos)
    await r.usuario.type(segundos, '45')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ interval_sec: 45 })
  })
})

describe('intervalo "Outro…"', () => {
  it('fora de 30 a 86400 é recusado no campo; um da lista esconde o campo', async () => {
    const r = await renderizarPainel('/targets/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.selectOptions(screen.getByLabelText('Intervalo'), 'Outro…')
    const segundos = screen.getByLabelText('Intervalo em segundos')
    await r.usuario.clear(segundos)
    await r.usuario.type(segundos, '29')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(segundos).toHaveAccessibleDescription(/Mínimo de 30 s/))
    expect(r.falsa.chamadas('POST /targets')).toBe(0)
    await r.usuario.selectOptions(screen.getByLabelText('Intervalo'), '5 minutos')
    expect(screen.queryByLabelText('Intervalo em segundos')).not.toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({ interval_sec: 300 }))
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos`
Expected: FAIL: `./esquema` não existe.

- [ ] **Step 3: Implementar**

Crie `web/src/features/alvos/esquema.ts`:

```ts
import { z } from 'zod'
import type { AlteracaoAlvo, EntradaAlvo } from '@/lib/api/cliente'
import type { Alvo } from '@/lib/api/schemas'

// O formulário de alvo espelha internal/targets/validate.go. O servidor é a
// fonte da verdade: o espelho só antecipa o erro, e o 400 com fields cai no
// campo certo do mesmo jeito (um nome que resolve para IP interno, por
// exemplo, só o servidor recusa — e só no momento da conexão).

const PROIBIDOS = new Set([
  'host',
  'content-length',
  'transfer-encoding',
  'connection',
  'keep-alive',
  'upgrade',
  'te',
  'trailer',
  'expect',
  'proxy-authorization',
  'proxy-connection',
])

const NOME_DE_HEADER = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

const bytes = (s: string) => new TextEncoder().encode(s).length

// Um SUBCONJUNTO da lista do safedial, só para responder cedo aos casos
// óbvios. Nunca pode ser mais restritivo que o servidor: recusar aqui o que o
// servidor aceita seria um bug sem saída para o usuário.
function urlPermitida(raw: string): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return 'Use uma URL http:// ou https:// completa.'
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'Só http:// e https://.'
  const host = u.hostname.replace(/\.$/, '').toLowerCase()
  if (host === '') return 'Use uma URL http:// ou https:// completa.'
  if (host === 'localhost' || host.endsWith('.localhost'))
    return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
  const v4 = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    const interno =
      a === 0 ||
      a === 10 ||
      a === 127 ||
      a >= 224 ||
      (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 100 && b >= 64 && b <= 127)
    if (interno) return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
  }
  if (host.startsWith('[')) {
    const h = host.slice(1, -1)
    if (h === '::1' || h === '::' || /^f[cd]/.test(h) || /^fe[89ab]/.test(h)) {
      return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
    }
  }
  return null
}

export const esquemaAlvo = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Obrigatório.')
      .refine((s) => [...s].length <= 100, 'Até 100 caracteres.')
      .refine((s) => !/\p{Cc}/u.test(s), 'Sem caracteres de controle.'),
    url: z
      .string()
      .trim()
      .min(1, 'Obrigatório.')
      .refine((s) => bytes(s) <= 2048, 'Até 2048 caracteres.')
      .superRefine((s, ctx) => {
        const m = urlPermitida(s)
        if (m) ctx.addIssue({ code: 'custom', message: m })
      }),
    method: z.enum(['GET', 'HEAD', 'POST']),
    // Um dos intervalos da lista ou "outro" (spec §4.3): com "outro", o
    // número vem do campo em segundos; a regra é a mesma, 30 a 86400.
    intervaloEscolhido: z.string(),
    interval_sec: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(30, 'Mínimo de 30 s.')
      .max(86400, 'Máximo de 1 dia.'),
    timeout_ms: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(1000, 'Mínimo de 1000 ms.')
      .max(30000, 'Máximo de 30000 ms.'),
    expected_status: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(100, 'Entre 100 e 599.')
      .max(599, 'Entre 100 e 599.'),
    lentidao: z.enum(['padrao', 'personalizado', 'desativado']),
    degraded_threshold_ms: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(1, 'Entre 1 e 60000 ms.')
      .max(60000, 'Entre 1 e 60000 ms.'),
    enabled: z.boolean(),
    // manter: a edição não mexe nos headers (os valores nunca voltam do
    // servidor); substituir: a lista abaixo troca todos; remover: apaga.
    modoHeaders: z.enum(['manter', 'substituir', 'remover']),
    headers: z.array(z.object({ nome: z.string().trim(), valor: z.string() })),
  })
  .superRefine((v, ctx) => {
    // D3 do Plano 2: o jitter de −10% antecipa a próxima sonda para 90% do
    // intervalo; um timeout maior deixaria duas sondas do alvo em voo.
    if (v.timeout_ms * 10 >= v.interval_sec * 9000) {
      ctx.addIssue({ code: 'custom', path: ['timeout_ms'], message: 'Tem de ser menor que 90% do intervalo.' })
    }
    if (v.modoHeaders !== 'substituir') return
    const vistos = new Set<string>()
    let total = 0
    if (v.headers.length > 20) ctx.addIssue({ code: 'custom', path: ['headers'], message: 'Até 20 headers.' })
    v.headers.forEach((h, i) => {
      const nome = h.nome.toLowerCase()
      if (!NOME_DE_HEADER.test(h.nome)) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'nome'], message: 'Nome de header inválido.' })
      } else if (PROIBIDOS.has(nome)) {
        ctx.addIssue({
          code: 'custom',
          path: ['headers', i, 'nome'],
          message: 'Este header é controlado pelo monitor.',
        })
      } else if (vistos.has(nome)) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'nome'], message: 'Header repetido.' })
      }
      vistos.add(nome)
      if (bytes(h.valor) > 1024) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'valor'], message: 'Até 1 KiB.' })
      }
      total += bytes(h.nome) + bytes(h.valor)
    })
    if (total > 8192) ctx.addIssue({ code: 'custom', path: ['headers'], message: 'Os headers somam mais de 8 KiB.' })
  })

export type ValoresDoAlvo = z.infer<typeof esquemaAlvo>

export const INTERVALOS = [
  [30, '30 segundos'],
  [60, '1 minuto'],
  [300, '5 minutos'],
  [900, '15 minutos'],
  [3600, '1 hora'],
] as const

export const OUTRO = 'outro'

// O que o seletor mostra para um intervalo: ele mesmo, se está na lista;
// senão, "outro" (o campo em segundos aparece com o valor).
export function escolhaDoIntervalo(s: number): string {
  return INTERVALOS.some(([v]) => v === s) ? String(s) : OUTRO
}

export const valoresPadrao: ValoresDoAlvo = {
  name: '',
  url: '',
  method: 'GET',
  intervaloEscolhido: '60',
  interval_sec: 60,
  timeout_ms: 10000,
  expected_status: 200,
  lentidao: 'padrao',
  degraded_threshold_ms: 2000,
  enabled: true,
  modoHeaders: 'substituir',
  headers: [],
}

export function valoresDe(a: Alvo): ValoresDoAlvo {
  return {
    name: a.name,
    url: a.url,
    method: a.method,
    intervaloEscolhido: escolhaDoIntervalo(a.interval_sec),
    interval_sec: a.interval_sec,
    timeout_ms: a.timeout_ms,
    expected_status: a.expected_status,
    lentidao:
      a.degraded_threshold_ms === null ? 'desativado' : a.degraded_threshold_ms === 2000 ? 'padrao' : 'personalizado',
    degraded_threshold_ms: a.degraded_threshold_ms ?? 2000,
    enabled: a.enabled,
    modoHeaders: 'manter',
    headers: [],
  }
}

function limiar(v: ValoresDoAlvo): number | null | undefined {
  if (v.lentidao === 'desativado') return null
  if (v.lentidao === 'personalizado') return v.degraded_threshold_ms
  return 2000
}

function mapaDeHeaders(v: ValoresDoAlvo): Record<string, string> {
  return Object.fromEntries(v.headers.map((h) => [h.nome, h.valor]))
}

// O corpo do POST. Os números vão sempre explícitos (evita o "0 vira padrão
// em POST e 400 em PATCH" das dívidas do Plano 2); o limiar padrão vai
// ausente, que é o que o servidor lê como "use 2000 ms".
export function paraCriacao(v: ValoresDoAlvo): EntradaAlvo {
  const e: EntradaAlvo = {
    name: v.name,
    url: v.url,
    method: v.method,
    interval_sec: v.interval_sec,
    timeout_ms: v.timeout_ms,
    expected_status: v.expected_status,
    enabled: v.enabled,
  }
  if (v.lentidao !== 'padrao') e.degraded_threshold_ms = limiar(v)
  if (v.headers.length) e.headers = mapaDeHeaders(v)
  return e
}

// O corpo do PATCH: só o que mudou. O servidor recusa campo desconhecido, e
// "headers" só vai com "substituir" ou "remover" — mandar {} apagaria os
// headers em silêncio (spec do Plano 6, D24).
export function paraAlteracao(original: ValoresDoAlvo, v: ValoresDoAlvo): AlteracaoAlvo {
  const a: AlteracaoAlvo = {}
  if (v.name !== original.name) a.name = v.name
  if (v.url !== original.url) a.url = v.url
  if (v.method !== original.method) a.method = v.method
  if (v.interval_sec !== original.interval_sec) a.interval_sec = v.interval_sec
  if (v.timeout_ms !== original.timeout_ms) a.timeout_ms = v.timeout_ms
  if (v.expected_status !== original.expected_status) a.expected_status = v.expected_status
  if (v.enabled !== original.enabled) a.enabled = v.enabled
  if (limiar(v) !== limiar(original)) a.degraded_threshold_ms = limiar(v)
  if (v.modoHeaders === 'substituir') a.headers = mapaDeHeaders(v)
  if (v.modoHeaders === 'remover') a.headers = null
  return a
}

// Onde cada campo do servidor aparece no formulário.
export const campoDoServidor: Record<string, keyof ValoresDoAlvo> = {
  name: 'name',
  url: 'url',
  method: 'method',
  interval_sec: 'interval_sec',
  timeout_ms: 'timeout_ms',
  expected_status: 'expected_status',
  degraded_threshold_ms: 'degraded_threshold_ms',
  headers: 'headers',
}
```

Crie `web/src/features/alvos/FormularioAlvo.tsx`:

```tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario, Selecao } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import { campoDoServidor, esquemaAlvo, INTERVALOS, OUTRO, type ValoresDoAlvo } from './esquema'

export function FormularioAlvo({
  iniciais,
  nomesDeHeaders,
  aoEnviar,
  rotuloDoBotao,
}: {
  iniciais: ValoresDoAlvo
  // Na edição: os nomes dos headers já definidos (os valores nunca voltam).
  nomesDeHeaders?: string[]
  aoEnviar: (v: ValoresDoAlvo) => Promise<void>
  rotuloDoBotao: string
}) {
  const f = useForm<ValoresDoAlvo>({ resolver: zodResolver(esquemaAlvo), defaultValues: iniciais })
  const headers = useFieldArray({ control: f.control, name: 'headers' })
  const [erroGeral, setErroGeral] = useState<string | null>(null)
  const e = f.formState.errors
  const lentidao = useWatch({ control: f.control, name: 'lentidao' })
  const modoHeaders = useWatch({ control: f.control, name: 'modoHeaders' })
  const intervaloEscolhido = useWatch({ control: f.control, name: 'intervaloEscolhido' })
  const editando = nomesDeHeaders !== undefined

  async function enviar(v: ValoresDoAlvo) {
    setErroGeral(null)
    try {
      await aoEnviar(v)
    } catch (err) {
      // O 400 com fields vai para o campo certo; o resto, para o topo.
      if (err instanceof ApiError && err.fields.length) {
        let algum = false
        for (const fe of err.fields) {
          const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]
          if (campo) {
            f.setError(campo, { message: mensagemDoCampo(fe.field, fe.reason) }, { shouldFocus: !algum })
            algum = true
          }
        }
        if (algum) return
      }
      setErroGeral(mensagemDe(err))
    }
  }

  const numero = { valueAsNumber: true } as const
  return (
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erroGeral}</ErroDoFormulario>
      <Campo rotulo="Nome" erro={e.name?.message}>
        {(a) => <Entrada {...a} {...f.register('name')} />}
      </Campo>
      <Campo rotulo="URL" erro={e.url?.message} ajuda="http:// ou https://. Endereços internos não são aceitos.">
        {(a) => <Entrada {...a} type="url" inputMode="url" {...f.register('url')} />}
      </Campo>
      <div className="grid gap-4 sm:grid-cols-3">
        <Campo rotulo="Método" erro={e.method?.message}>
          {(a) => (
            <Selecao {...a} {...f.register('method')}>
              <option value="GET">GET</option>
              <option value="HEAD">HEAD</option>
              <option value="POST">POST</option>
            </Selecao>
          )}
        </Campo>
        <Campo rotulo="Intervalo" erro={intervaloEscolhido === OUTRO ? undefined : e.interval_sec?.message}>
          {(a) => (
            <Selecao
              {...a}
              {...f.register('intervaloEscolhido', {
                // Um intervalo da lista vira o número; "outro" mantém o que
                // estava, para o campo em segundos começar dele.
                onChange: (ev: { target: { value: string } }) => {
                  if (ev.target.value !== OUTRO)
                    f.setValue('interval_sec', Number(ev.target.value), { shouldDirty: true })
                },
              })}
            >
              {INTERVALOS.map(([s, t]) => (
                <option key={s} value={s}>
                  {t}
                </option>
              ))}
              <option value={OUTRO}>Outro…</option>
            </Selecao>
          )}
        </Campo>
        <Campo rotulo="Timeout (ms)" erro={e.timeout_ms?.message}>
          {(a) => <Entrada {...a} type="number" min={1000} max={30000} {...f.register('timeout_ms', numero)} />}
        </Campo>
      </div>
      {intervaloEscolhido === OUTRO && (
        <Campo rotulo="Intervalo em segundos" erro={e.interval_sec?.message} ajuda="De 30 a 86400 (1 dia).">
          {(a) => <Entrada {...a} type="number" min={30} max={86400} {...f.register('interval_sec', numero)} />}
        </Campo>
      )}
      <Campo rotulo="Status HTTP esperado" erro={e.expected_status?.message}>
        {(a) => <Entrada {...a} type="number" min={100} max={599} {...f.register('expected_status', numero)} />}
      </Campo>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Limiar de lentidão</legend>
        <p className="text-sm text-suave">Acima dele, uma resposta boa conta como “degradada”.</p>
        {(
          [
            ['padrao', 'Padrão (2000 ms)'],
            ['personalizado', 'Personalizado'],
            ['desativado', 'Desativado'],
          ] as const
        ).map(([v, t]) => (
          <label key={v} className="flex items-center gap-2 text-sm">
            <input type="radio" value={v} {...f.register('lentidao')} />
            {t}
          </label>
        ))}
        {lentidao === 'personalizado' && (
          <Campo rotulo="Limiar (ms)" erro={e.degraded_threshold_ms?.message}>
            {(a) => (
              <Entrada {...a} type="number" min={1} max={60000} {...f.register('degraded_threshold_ms', numero)} />
            )}
          </Campo>
        )}
      </fieldset>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" {...f.register('enabled')} />
        Ativo (sondar este alvo)
      </label>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">Headers</legend>
        <p className="text-sm text-suave">
          Os headers não acompanham redirecionamento para outro host ou porta, nem de https para http.
        </p>
        {editando && (
          <>
            <p className="text-sm">
              {nomesDeHeaders.length
                ? `Headers definidos: ${nomesDeHeaders.join(', ')} (valores ocultos).`
                : 'Nenhum header definido.'}
            </p>
            <div className="flex flex-wrap gap-4">
              {(
                [
                  ['manter', 'Manter como está'],
                  ['substituir', 'Substituir todos'],
                  ['remover', 'Remover todos'],
                ] as const
              ).map(([v, t]) => (
                <label key={v} className="flex items-center gap-2 text-sm">
                  <input type="radio" value={v} {...f.register('modoHeaders')} />
                  {t}
                </label>
              ))}
            </div>
          </>
        )}
        {modoHeaders === 'substituir' && (
          <div className="space-y-2">
            {headers.fields.map((h, i) => (
              <div key={h.id} className="grid grid-cols-[1fr_1fr_auto] items-end gap-2">
                <Campo rotulo={`Nome do header ${i + 1}`} erro={e.headers?.[i]?.nome?.message}>
                  {(a) => <Entrada {...a} {...f.register(`headers.${i}.nome`)} />}
                </Campo>
                <Campo rotulo={`Valor do header ${i + 1}`} erro={e.headers?.[i]?.valor?.message}>
                  {(a) => <Entrada {...a} {...f.register(`headers.${i}.valor`)} />}
                </Campo>
                <Botao tipo="fantasma" onClick={() => headers.remove(i)} aria-label={`Remover o header ${i + 1}`}>
                  Remover
                </Botao>
              </div>
            ))}
            {e.headers?.message && <p className="text-sm text-perigo">{e.headers.message}</p>}
            <Botao tipo="secundario" tamanho="pequeno" onClick={() => headers.append({ nome: '', valor: '' })}>
              Adicionar header
            </Botao>
          </div>
        )}
      </fieldset>

      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {rotuloDoBotao}
      </Botao>
    </form>
  )
}
```

Crie `web/src/features/alvos/PaginasDoFormulario.tsx`:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { useAlvo } from '@/lib/api/consultas'
import { FormularioAlvo } from './FormularioAlvo'
import { paraAlteracao, paraCriacao, valoresDe, valoresPadrao } from './esquema'

export function PaginaNovoAlvo() {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  return (
    <>
      <Titulo aba="Novo alvo">Novo alvo</Titulo>
      <FormularioAlvo
        iniciais={valoresPadrao}
        rotuloDoBotao="Cadastrar"
        aoEnviar={async (v) => {
          const a = await api.criarAlvo(paraCriacao(v))
          await qc.invalidateQueries({ queryKey: ['targets'] })
          avisar('Alvo cadastrado.')
          navegar(`/targets/${a.id}`)
        }}
      />
    </>
  )
}

export function PaginaEditarAlvo() {
  const { id = '' } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const consulta = useAlvo(id)
  return (
    <>
      <Titulo aba="Editar alvo">Editar alvo</Titulo>
      <EstadoDaConsulta consulta={consulta}>
        {(alvo) => {
          const iniciais = valoresDe(alvo)
          return (
            <FormularioAlvo
              iniciais={iniciais}
              nomesDeHeaders={alvo.header_names}
              rotuloDoBotao="Salvar"
              aoEnviar={async (v) => {
                const mudancas = paraAlteracao(iniciais, v)
                if (Object.keys(mudancas).length) await api.alterarAlvo(id, mudancas)
                await qc.invalidateQueries({ queryKey: ['targets'] })
                avisar('Alvo salvo.')
                navegar(`/targets/${id}`)
              }}
            />
          )
        }}
      </EstadoDaConsulta>
    </>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -1,6 +1,7 @@
 import type { RouteObject } from 'react-router'
 import { PaginaLogin } from '@/features/login/PaginaLogin'
 import { PaginaPainel } from '@/features/painel/PaginaPainel'
+import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
 import { ErroDeRota } from './ErroDeRota'
 import { ExigirSessao } from './ExigirSessao'
 import { Layout } from './Layout'
@@ -18,6 +19,8 @@
         Component: Layout,
         children: [
           { index: true, Component: PaginaPainel },
+          { path: 'targets/new', Component: PaginaNovoAlvo },
+          { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
           { path: '*', Component: NaoEncontrada },
         ],
       },
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `paraAlteracao`, troque `if (v.modoHeaders === 'substituir') a.headers = mapaDeHeaders(v)` por `a.headers = mapaDeHeaders(v)`. Expected: FAIL em `edição só manda o que mudou, e nunca "headers" sem pedir`.
2. Em `urlPermitida`, acrescente `|| h.startsWith('::ffff:')` à condição do IPv6. Expected: FAIL em `endereço interno óbvio é recusado já no cliente; um público passa` (o cliente ficou mais restritivo que o servidor).
3. No `superRefine` do esquema, troque `>= v.interval_sec * 9000` por `> v.interval_sec * 9000`. Expected: FAIL em `timeout tem de ser menor que 90% do intervalo`.
4. Em `FormularioAlvo.tsx`, troque `const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]` por `const campo = Math.random() > 2 ? campoDoServidor[fe.field.replace(/\[.*$/, '')] : undefined`. Expected: FAIL em `o 400 com fields vai para o campo certo`. (Trocar por um literal não compila: o import de `campoDoServidor` sobra.)
5. Em `escolhaDoIntervalo`, troque `? String(s) : OUTRO` por `? String(s) : '60'`. Expected: FAIL em `intervalo fora da lista abre em "Outro…", com o valor, e só ele vai no PATCH` (o alvo de 120 s abriria em "1 minuto", e salvar sem mexer mudaria o intervalo em silêncio).

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): cadastro e edição de alvos, com o espelho de validate.go e headers que nunca voltam"
```

---

### Task 6: Detalhe do alvo, com o gráfico

`/targets/:id`: configuração, estado atual (do cache do resumo), janelas de 1 h a 90 dias na URL, o gráfico de latência (Recharts, num chunk carregado sob demanda), o resumo em texto e a tabela alternativa, os incidentes do alvo (`GET /incidents?target_id=`, do Plano 5) e as ações Editar, Pausar/Retomar e Apagar (com confirmação). O diálogo de confirmação é o primeiro componente que injeta `<style>` em runtime (o `react-remove-scroll` do Radix); é o `setNonce` de `lib/nonce.ts`, da Task 2, que o deixa passar pela CSP — provado no E2E (Task 11).

**Files:**
- Create: `web/src/components/ui/dialogo.tsx`
- Create: `web/src/features/alvos/serie.ts`, `web/src/features/alvos/GraficoDeLatencia.tsx`, `web/src/features/alvos/PaginaDetalhe.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/alvos/serie.test.ts`, `web/src/features/alvos/detalhe.test.tsx`

**Interfaces:**
- Consumes: `useAlvo`, `useResumo`, `useSerie`, `useIncidentes('all', id, 10)`, `api.alterarAlvo`, `api.apagarAlvo`, `SeloDeEstado`, `Titulo`, fixtures (Tasks 2–5).
- Produces:
  - `Dialogo({ aberto, aoMudar, titulo, descricao?, travado?, children })` (`travado`: Esc e o clique fora não fecham; a Task 10 o usa no passo dos códigos de recuperação), `Confirmar({ aberto, aoMudar, titulo, descricao, acao, aoConfirmar, ocupado? })`.
  - `JANELAS`, `type Janela`, `ehJanela`, `desde(janela, agora)`, `pontosBrutos`, `pontosHorarios`, `resumir(serie)`.
  - `GraficoDeLatencia({ serie })`, `PaginaDetalhe`.

**Contexto para quem implementa.** O painel desenha pelo `resolution` da resposta, **nunca** pela janela que pediu: é o servidor que decide o corte de 48 h (spec §9.1). Só `from` vai na consulta; o "agora" é do servidor. A latência de um check `down` é o tempo até o timeout, não a do serviço (D23 do Plano 5): ela não entra na linha; o check vira uma marca na base. As cores do gráfico são variáveis CSS do `index.css` — o `ChartStyle` do shadcn gerava um `<style>` em runtime, que sem nonce fazia a linha sumir (spike, §8.1). O Recharts não desenha no jsdom (largura 0): o que importa é função pura em `serie.ts`, testada aqui; o desenho é provado no E2E. A tabela mostra os 200 pontos mais recentes. Na resolução por hora, o uptime de cada hora (`ok/total`) vai em **barras**, no eixo da direita (spec §4.4); o E2E da Task 11 confere que elas desenham, na janela de 7 dias. Volume (R4 da spec): medido na validação, o Recharts leva ~430 ms para desenhar 2.880 checks (24 h a 30 s, a maior janela bruta que o painel pede) e ~750 ms para 5.760 (48 h, o máximo bruto do servidor); um re-render com os dados em cache, ~100 ms e ~160 ms (a tela com 60 pontos: ~45 ms). O downsampling ficou de fora — corte 5 da spec (ver "Desvios").

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/alvos/serie.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { Serie } from '@/lib/api/schemas'
import { desde, pontosBrutos, pontosHorarios, resumir } from './serie'

const check = (min: number, status: 'up' | 'degraded' | 'down', latency_ms: number) => ({
  checked_at: new Date(Date.UTC(2026, 8, 24, 12, min)).toISOString(),
  status,
  latency_ms,
  status_code: status === 'down' ? null : 200,
  error_kind: status === 'down' ? 'timeout' : null,
})

describe('série bruta', () => {
  it('check fora do ar não entra na linha de latência: vira marca na base', () => {
    const p = pontosBrutos([check(0, 'up', 100), check(1, 'down', 10000), check(2, 'degraded', 2500)])
    expect(p.map((x) => x.latencia)).toEqual([100, null, 2500])
    expect(p.map((x) => x.fora)).toEqual([null, 0, null])
  })

  it('o resumo ignora a latência dos checks fora do ar', () => {
    const s: Serie = {
      resolution: 'raw',
      items: [check(0, 'up', 100), check(1, 'up', 200), check(2, 'down', 10000), check(3, 'up', 300)],
    }
    expect(resumir(s)).toEqual({ mediana: 200, p95: 300, uptime: 0.75, amostras: 4 })
  })

  it('série vazia não inventa número', () => {
    expect(resumir({ resolution: 'raw', items: [] })).toEqual({ mediana: null, p95: null, uptime: null, amostras: 0 })
  })
})

describe('série horária', () => {
  it('usa ok/total e a faixa p50–p95; hora sem sucesso fica sem faixa', () => {
    const itens = [
      { bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 100, p95_ms: 300, p99_ms: 400, max_ms: 500 },
      { bucket: '2026-09-24T11:00:00Z', total: 60, ok: 0, p50_ms: null, p95_ms: null, p99_ms: null, max_ms: null },
    ]
    const p = pontosHorarios(itens)
    expect(p[0]).toMatchObject({ p50: 100, faixa: [100, 300], uptime: 1 })
    expect(p[1]).toMatchObject({ p50: null, faixa: null, uptime: 0 })
    expect(resumir({ resolution: '1h', items: itens })).toEqual({ mediana: 100, p95: 300, uptime: 0.5, amostras: 120 })
  })
})

it('desde() sai em RFC 3339 sem milissegundos', () => {
  expect(desde('24h', Date.UTC(2026, 8, 24, 12, 0, 0, 123))).toBe('2026-09-23T12:00:00Z')
})
```

Crie `web/src/features/alvos/detalhe.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

describe('detalhe', () => {
  it('resumo em texto, tabela, incidentes do alvo pelo filtro target_id', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down' })]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [
        { checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null },
        { checked_at: '2026-09-24T11:01:00Z', status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
      ],
    }
    r.falsa.estado.incidentes = [incidente(), incidente({ id: 'inc-2', target_id: 'outro', target_name: 'Outro' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { name: 'API' })).toBeInTheDocument()
    expect(await screen.findByText(/Mediana 100 ms, p95 100 ms, 50,00% no ar nesta janela/)).toBeInTheDocument()
    expect(screen.getByText('Resolução por check.', { exact: false })).toBeInTheDocument()
    const tabela = screen.getByRole('table')
    expect(within(tabela).getByText('Tempo esgotado')).toBeInTheDocument()
    // Só o incidente deste alvo: a lista vem de /incidents?target_id=.
    const secao = screen.getByRole('region', { name: 'Incidentes recentes' })
    await waitFor(() => expect(within(secao).getAllByRole('listitem')).toHaveLength(1))
  })

  it('a janela acima de 48h desenha pelo que o servidor decidiu (resolution 1h)', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=7d')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: '1h',
      items: [{ bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByText(/Resolução por hora/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7 dias' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('apagar pede confirmação e volta ao painel', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    expect(within(dialogo).getByText(/Não há como desfazer/)).toBeInTheDocument()
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(r.falsa.chamadas('DELETE /targets/:id')).toBe(1)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos`
Expected: FAIL: `./serie` não existe, e os testes do detalhe caem no 404 do painel.

- [ ] **Step 3: Implementar**

Crie `web/src/components/ui/dialogo.tsx`:

```tsx
import { AlertDialog, Dialog } from 'radix-ui'
import type { ReactNode } from 'react'
import { Botao } from './botao'

// Dialog e AlertDialog do Radix. Os dois travam a rolagem da página com o
// react-remove-scroll, que injeta um <style>: é o nonce de src/lib/nonce.ts
// que o deixa passar pela CSP (spec do Plano 6, §8.1).

const sobreposicao = 'fixed inset-0 bg-black/50'
const caixa =
  'fixed top-1/2 left-1/2 w-[min(32rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 space-y-4 rounded-lg border border-borda bg-fundo p-6 shadow-lg max-h-[90vh] overflow-y-auto'

// travado: Esc e o clique fora não fecham. Para o passo que exige uma ação
// explícita antes de sair (os códigos de recuperação, que não aparecem de
// novo).
export function Dialogo({
  aberto,
  aoMudar,
  titulo,
  descricao,
  travado = false,
  children,
}: {
  aberto: boolean
  aoMudar: (v: boolean) => void
  titulo: string
  descricao?: string
  travado?: boolean
  children: ReactNode
}) {
  const segurar = (e: Event) => {
    if (travado) e.preventDefault()
  }
  return (
    <Dialog.Root open={aberto} onOpenChange={aoMudar}>
      <Dialog.Portal>
        <Dialog.Overlay className={sobreposicao} />
        <Dialog.Content className={caixa} onEscapeKeyDown={segurar} onPointerDownOutside={segurar}>
          <Dialog.Title className="text-lg font-semibold">{titulo}</Dialog.Title>
          {descricao ? (
            <Dialog.Description className="text-sm text-suave">{descricao}</Dialog.Description>
          ) : (
            <Dialog.Description className="sr-only">{titulo}</Dialog.Description>
          )}
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

// Confirmação de ação destrutiva. O foco começa em "Cancelar" (padrão do
// AlertDialog), e Esc cancela.
export function Confirmar({
  aberto,
  aoMudar,
  titulo,
  descricao,
  acao,
  aoConfirmar,
  ocupado,
}: {
  aberto: boolean
  aoMudar: (v: boolean) => void
  titulo: string
  descricao: ReactNode
  acao: string
  aoConfirmar: () => void
  ocupado?: boolean
}) {
  return (
    <AlertDialog.Root open={aberto} onOpenChange={aoMudar}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className={sobreposicao} />
        <AlertDialog.Content className={caixa}>
          <AlertDialog.Title className="text-lg font-semibold">{titulo}</AlertDialog.Title>
          <AlertDialog.Description className="text-sm text-suave">{descricao}</AlertDialog.Description>
          <div className="flex justify-end gap-2">
            <AlertDialog.Cancel asChild>
              <Botao tipo="secundario">Cancelar</Botao>
            </AlertDialog.Cancel>
            <Botao
              tipo="perigo"
              disabled={ocupado}
              onClick={(e) => {
                e.preventDefault()
                aoConfirmar()
              }}
            >
              {acao}
            </Botao>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}
```

Crie `web/src/features/alvos/serie.ts`:

```ts
import type { Check, Hora, Serie } from '@/lib/api/schemas'

// A série do detalhe do alvo em forma de desenho e de texto. Funções puras:
// o Recharts não desenha no jsdom, então o que importa (o que é "fora", o
// resumo em texto) é testado aqui, e o desenho, no E2E.

export const JANELAS = {
  '1h': 3_600_000,
  '6h': 6 * 3_600_000,
  '24h': 24 * 3_600_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
  '90d': 90 * 86_400_000,
} as const

export type Janela = keyof typeof JANELAS

export function ehJanela(v: string | null): v is Janela {
  return v !== null && v in JANELAS
}

// RFC 3339 sem milissegundos, no formato que o servidor lê.
export function desde(j: Janela, agora: number): string {
  return new Date(agora - JANELAS[j]).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

export type PontoBruto = { t: number; latencia: number | null; fora: number | null }
export type PontoHorario = { t: number; p50: number | null; faixa: [number, number] | null; uptime: number | null }

// A latência de um check "down" é o tempo até o timeout ou a recusa, não a
// do serviço (D23 do Plano 5): ela não entra na linha; o check vira uma
// marca na base do gráfico.
export function pontosBrutos(itens: Check[]): PontoBruto[] {
  return itens.map((c) => ({
    t: new Date(c.checked_at).getTime(),
    latencia: c.status === 'down' ? null : c.latency_ms,
    fora: c.status === 'down' ? 0 : null,
  }))
}

export function pontosHorarios(itens: Hora[]): PontoHorario[] {
  return itens.map((h) => ({
    t: new Date(h.bucket).getTime(),
    p50: h.p50_ms,
    faixa: h.p50_ms !== null && h.p95_ms !== null ? [h.p50_ms, h.p95_ms] : null,
    uptime: h.total ? h.ok / h.total : null,
  }))
}

function percentil(ordenados: number[], p: number): number | null {
  if (!ordenados.length) return null
  const i = Math.max(0, Math.ceil(p * ordenados.length) - 1)
  return ordenados[i] ?? null
}

export type ResumoDaSerie = { mediana: number | null; p95: number | null; uptime: number | null; amostras: number }

export function resumir(s: Serie): ResumoDaSerie {
  if (s.resolution === 'raw') {
    const bons = s.items.filter((c) => c.status !== 'down').map((c) => c.latency_ms).sort((a, b) => a - b)
    return {
      mediana: percentil(bons, 0.5),
      p95: percentil(bons, 0.95),
      uptime: s.items.length ? bons.length / s.items.length : null,
      amostras: s.items.length,
    }
  }
  const medianas = s.items.map((h) => h.p50_ms).filter((v): v is number => v !== null).sort((a, b) => a - b)
  const p95s = s.items.map((h) => h.p95_ms).filter((v): v is number => v !== null)
  const total = s.items.reduce((n, h) => n + h.total, 0)
  const ok = s.items.reduce((n, h) => n + h.ok, 0)
  return {
    mediana: percentil(medianas, 0.5),
    p95: p95s.length ? Math.max(...p95s) : null,
    uptime: total ? ok / total : null,
    amostras: total,
  }
}
```

Crie `web/src/features/alvos/GraficoDeLatencia.tsx`:

```tsx
import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Scatter,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { Serie } from '@/lib/api/schemas'
import { pontosBrutos, pontosHorarios, type PontoBruto, type PontoHorario } from './serie'

// Recharts, só aqui: este arquivo entra no chunk do detalhe, carregado sob
// demanda. As cores são variáveis CSS do index.css, e não um <style> gerado
// em runtime como o ChartStyle do shadcn — que a CSP bloquearia sem nonce e
// faria a linha sumir (spike do Plano 6, §8.1).
const hora = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' })
const dia = new Intl.DateTimeFormat('pt-BR', { day: '2-digit', month: '2-digit' })
const pct = (v: number) => `${Math.floor(v * 100)}%`

export function GraficoDeLatencia({ serie }: { serie: Serie }) {
  const horaria = serie.resolution === '1h'
  const dados: (PontoBruto | PontoHorario)[] =
    serie.resolution === '1h' ? pontosHorarios(serie.items) : pontosBrutos(serie.items)
  const fmt = (t: number) => (horaria ? dia.format(t) : hora.format(t))
  return (
    <div className="h-64 w-full" data-testid="grafico-latencia">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={dados} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="var(--borda)" vertical={false} />
          <XAxis
            dataKey="t"
            type="number"
            scale="time"
            domain={['dataMin', 'dataMax']}
            tickFormatter={fmt}
            stroke="var(--suave)"
          />
          <YAxis stroke="var(--suave)" unit=" ms" width={72} />
          {horaria && (
            // O uptime de cada hora (ok/total) em barras, no eixo da direita
            // (spec do Plano 6 §4.4). Atrás das linhas: vem antes delas.
            <YAxis
              yAxisId="uptime"
              orientation="right"
              domain={[0, 1]}
              tickFormatter={pct}
              stroke="var(--suave)"
              width={48}
            />
          )}
          <Tooltip
            labelFormatter={(t) => new Date(Number(t)).toLocaleString('pt-BR')}
            contentStyle={{ background: 'var(--fundo)', borderColor: 'var(--borda)', color: 'var(--texto)' }}
          />
          {horaria ? (
            <>
              <Bar
                yAxisId="uptime"
                dataKey="uptime"
                name="No ar na hora"
                fill="var(--grafico-uptime)"
                isAnimationActive={false}
              />
              <Area
                dataKey="faixa"
                name="p50–p95"
                stroke="none"
                fill="var(--grafico-faixa)"
                isAnimationActive={false}
              />
              <Line dataKey="p50" name="Mediana" stroke="var(--grafico-linha)" dot={false} isAnimationActive={false} />
            </>
          ) : (
            <>
              <Line
                dataKey="latencia"
                name="Latência"
                stroke="var(--grafico-linha)"
                dot={false}
                connectNulls={false}
                isAnimationActive={false}
              />
              <Scatter dataKey="fora" name="Fora do ar" fill="var(--estado-fora)" isAnimationActive={false} />
            </>
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}
```

Crie `web/src/features/alvos/PaginaDetalhe.tsx`:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta, Esqueleto } from '@/components/EstadoDaConsulta'
import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar } from '@/components/ui/dialogo'
import { useAlvo, useIncidentes, useResumo, useSerie } from '@/lib/api/consultas'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, duracao, fuso, milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { GraficoDeLatencia } from './GraficoDeLatencia'
import { desde, ehJanela, JANELAS, resumir, type Janela } from './serie'

const NOMES: Record<Janela, string> = { '1h': '1 h', '6h': '6 h', '24h': '24 h', '7d': '7 dias', '30d': '30 dias', '90d': '90 dias' }

function Resumo({ serie }: { serie: Serie }) {
  const r = resumir(serie)
  const horaria = serie.resolution === '1h'
  return (
    <p>
      {horaria ? 'Mediana das horas' : 'Mediana'} {milissegundos(r.mediana)}, {horaria ? 'pior p95 horário' : 'p95'}{' '}
      {milissegundos(r.p95)}, {r.uptime === null ? 'sem checks' : `${porcento(r.uptime)} no ar`} nesta janela.
    </p>
  )
}

function Tabela({ serie }: { serie: Serie }) {
  if (serie.resolution === '1h') {
    const linhas = serie.items.slice(-200).reverse()
    return (
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Horas da janela</caption>
        <thead>
          <tr>
            <th scope="col">Hora</th>
            <th scope="col">Checks</th>
            <th scope="col">No ar</th>
            <th scope="col">Mediana</th>
            <th scope="col">p95</th>
          </tr>
        </thead>
        <tbody>
          {linhas.map((h) => (
            <tr key={h.bucket}>
              <td>{dataHora(h.bucket)}</td>
              <td>{h.total}</td>
              <td>{h.total ? porcento(h.ok / h.total) : '—'}</td>
              <td>{milissegundos(h.p50_ms)}</td>
              <td>{milissegundos(h.p95_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  const linhas = serie.items.slice(-200).reverse()
  return (
    <table className="w-full text-left text-sm">
      <caption className="sr-only">Checks da janela, do mais recente</caption>
      <thead>
        <tr>
          <th scope="col">Quando</th>
          <th scope="col">Estado</th>
          <th scope="col">Latência</th>
          <th scope="col">Detalhe</th>
        </tr>
      </thead>
      <tbody>
        {linhas.map((c) => (
          <tr key={c.checked_at}>
            <td>{dataHora(c.checked_at)}</td>
            <td>
              <SeloDeEstado estado={c.status} />
            </td>
            <td>{c.status === 'down' ? '—' : milissegundos(c.latency_ms)}</td>
            <td>{textoDoErroDeCheck(c.error_kind, c.status_code) || (c.status_code ?? '')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Incidentes({ id }: { id: string }) {
  const consulta = useIncidentes('all', id, 10)
  const itens = consulta.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <section className="space-y-2" aria-labelledby="titulo-incidentes">
      <h2 id="titulo-incidentes" className="text-lg font-semibold">
        Incidentes recentes
      </h2>
      {consulta.isPending ? (
        <Esqueleto linhas={2} />
      ) : consulta.error ? (
        <p role="alert">{mensagemDe(consulta.error)}</p>
      ) : itens.length === 0 ? (
        <p className="text-suave">Nenhum incidente.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {itens.map((i) => (
            <li key={i.id}>
              {dataHora(i.started_at)} —{' '}
              {i.resolved_at
                ? `durou ${duracao(new Date(i.resolved_at).getTime() - new Date(i.started_at).getTime())}`
                : 'em andamento'}{' '}
              ({textoDoErroDeCheck(i.cause, null)})
            </li>
          ))}
        </ul>
      )}
      <Link to="/incidents" className="text-sm text-realce underline">
        Todos os incidentes
      </Link>
    </section>
  )
}

export function PaginaDetalhe() {
  const { id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const janela: Janela = ehJanela(params.get('janela')) ? (params.get('janela') as Janela) : '24h'
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const alvo = useAlvo(id)
  const resumo = useResumo()
  const serie = useSerie(id, janela, () => desde(janela, Date.now()))
  const [apagando, setApagando] = useState(false)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const noResumo = resumo.data?.items.find((x) => x.id === id)

  async function alternar(ativo: boolean) {
    setErro(null)
    try {
      await api.alterarAlvo(id, { enabled: ativo })
      await qc.invalidateQueries({ queryKey: ['targets'] })
    } catch (e) {
      setErro(mensagemDe(e))
    }
  }

  async function apagar() {
    setOcupado(true)
    try {
      await api.apagarAlvo(id)
      await qc.invalidateQueries({ queryKey: ['targets'] })
      avisar('Alvo apagado.')
      navegar('/')
    } catch (e) {
      setErro(mensagemDe(e))
      setApagando(false)
    } finally {
      setOcupado(false)
    }
  }

  return (
    <EstadoDaConsulta consulta={alvo}>
      {(a) => (
        <>
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-1">
              <Titulo aba={a.name}>{a.name}</Titulo>
              <p className="text-sm break-all text-suave">
                {a.method} {a.url} · a cada {a.interval_sec} s
              </p>
              {noResumo && <SeloDeEstado estado={estadoExibido(noResumo.state, noResumo.enabled)} />}
            </div>
            <div className="flex flex-wrap gap-2">
              <Botao tipo="secundario" asChild>
                <Link to={`/targets/${id}/edit`}>Editar</Link>
              </Botao>
              <Botao tipo="secundario" onClick={() => void alternar(!a.enabled)}>
                {a.enabled ? 'Pausar' : 'Retomar'}
              </Botao>
              <Botao tipo="perigo" onClick={() => setApagando(true)}>
                Apagar
              </Botao>
            </div>
          </div>
          <ErroDoFormulario>{erro}</ErroDoFormulario>

          <section className="space-y-3" aria-labelledby="titulo-latencia">
            <h2 id="titulo-latencia" className="text-lg font-semibold">
              Latência
            </h2>
            <fieldset className="flex flex-wrap gap-1">
              <legend className="sr-only">Janela</legend>
              {(Object.keys(JANELAS) as Janela[]).map((j) => (
                <button
                  key={j}
                  type="button"
                  aria-pressed={j === janela}
                  onClick={() => setParams({ janela: j }, { replace: true })}
                  className={cn('rounded-md border border-borda px-3 py-1 text-sm', j === janela && 'bg-painel font-semibold')}
                >
                  {NOMES[j]}
                </button>
              ))}
            </fieldset>
            <EstadoDaConsulta consulta={serie}>
              {(s) => (
                <div className="space-y-3">
                  <Resumo serie={s} />
                  <p className="text-sm text-suave">
                    {s.resolution === '1h'
                      ? 'Resolução por hora — os dados horários podem atrasar até cerca de 1h15.'
                      : 'Resolução por check.'}{' '}
                    Horários em {fuso()}.
                  </p>
                  <GraficoDeLatencia serie={s} />
                  <details>
                    <summary className="cursor-pointer text-sm">Ver dados em tabela</summary>
                    <Tabela serie={s} />
                  </details>
                </div>
              )}
            </EstadoDaConsulta>
          </section>

          <Incidentes id={id} />

          <Confirmar
            aberto={apagando}
            aoMudar={setApagando}
            titulo={`Apagar “${a.name}”?`}
            descricao="O histórico de checks e incidentes deste alvo também é apagado. Não há como desfazer."
            acao="Apagar alvo"
            ocupado={ocupado}
            aoConfirmar={() => void apagar()}
          />
        </>
      )}
    </EstadoDaConsulta>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -20,6 +20,10 @@
         children: [
           { index: true, Component: PaginaPainel },
           { path: 'targets/new', Component: PaginaNovoAlvo },
+          {
+            path: 'targets/:id',
+            lazy: async () => ({ Component: (await import('@/features/alvos/PaginaDetalhe')).PaginaDetalhe }),
+          },
           { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
           { path: '*', Component: NaoEncontrada },
         ],
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS. O build tem um chunk `PaginaDetalhe-*.js` de ~390 KB (o Recharts) separado do principal.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `pontosBrutos`, troque `latencia: c.status === 'down' ? null : c.latency_ms` por `latencia: c.latency_ms`. Expected: FAIL em `check fora do ar não entra na linha de latência…`.
2. Em `resumir`, tire o `.filter((c) => c.status !== 'down')`. Expected: FAIL em `o resumo ignora a latência dos checks fora do ar`.
3. Em `PaginaDetalhe.tsx`, troque `useIncidentes('all', id, 10)` por `useIncidentes('all', undefined, 10)`. Expected: FAIL em `resumo em texto, tabela, incidentes do alvo pelo filtro target_id` (aparecem os dois incidentes).
4. Em `desde`, tire o `.replace(/\.\d{3}Z$/, 'Z')`. Expected: FAIL em `desde() sai em RFC 3339 sem milissegundos`.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): detalhe do alvo com gráfico sob demanda, resumo em texto e incidentes do alvo"
```

---

### Task 7: Incidentes

`/incidents`: Abertos / Resolvidos / Todos (na URL), "Carregar mais" pelo `next_cursor` (botão, e não rolagem infinita: previsível no teclado e no leitor de tela), causa em texto, duração, e **Reconhecer** nos abertos não reconhecidos.

**Files:**
- Create: `web/src/features/incidentes/PaginaIncidentes.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/incidentes/incidentes.test.tsx`

**Interfaces:**
- Consumes: `useIncidentes(filtro)`, `api.reconhecer`, `textoDoErroDeCheck`, `duracao`, `dataHora`, fixtures (Tasks 2–4).
- Produces: `PaginaIncidentes`.

**Contexto para quem implementa.** O `ack` é idempotente no servidor; o texto do botão não promete o que o servidor não faz ("Marca que você está ciente"). A causa é o `error_kind` do sondador, traduzido por `textoDoErroDeCheck`. O filtro é um `<fieldset>` de botões com `aria-pressed`. Reconhecer invalida `['incidents']`, o que atualiza também o contador de abertos da navegação (Task 3).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/incidentes/incidentes.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { incidente } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

describe('incidentes', () => {
  it('lista os abertos por padrão, com causa em texto, e reconhece', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [
      incidente(),
      incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' }),
    ]
    await r.cliente.refetchQueries()
    const lista = (await screen.findByText(/Tempo esgotado/)).closest('ul') as HTMLElement
    expect(within(lista).getAllByRole('listitem')).toHaveLength(1)
    expect(within(lista).getByText(/Tempo esgotado/)).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await screen.findByText(/Reconhecido em/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
  })

  it('o filtro vai para a URL e muda a consulta', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente({ resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' })]
    await r.usuario.click(await screen.findByRole('button', { name: 'Resolvidos' }))
    expect(r.router.state.location.search).toBe('?status=resolved')
    expect(await screen.findByText(/durou 30 min/)).toBeInTheDocument()
    // Resolvido não se reconhece.
    expect(screen.queryByRole('button', { name: /Reconhecer/ })).not.toBeInTheDocument()
  })

  it('"Carregar mais" segue o next_cursor', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.incidentes = Array.from({ length: 60 }, (_, i) =>
      incidente({ id: `inc-${String(i).padStart(2, '0')}`, target_name: `Alvo ${i}` }),
    )
    await r.cliente.refetchQueries()
    const itens = () => screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('Tempo esgotado'))
    await waitFor(() => expect(itens()).toHaveLength(50))
    await r.usuario.click(screen.getByRole('button', { name: 'Carregar mais' }))
    await waitFor(() => expect(itens()).toHaveLength(60))
    expect(screen.queryByRole('button', { name: 'Carregar mais' })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/incidentes`
Expected: FAIL: `/incidents` cai no 404 do painel.

- [ ] **Step 3: Implementar**

Crie `web/src/features/incidentes/PaginaIncidentes.tsx`:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { Esqueleto } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { useIncidentes } from '@/lib/api/consultas'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Incidente } from '@/lib/api/schemas'
import { dataHora, duracao } from '@/lib/format'
import { cn } from '@/lib/utils'

type Filtro = 'open' | 'resolved' | 'all'
const FILTROS: [Filtro, string][] = [
  ['open', 'Abertos'],
  ['resolved', 'Resolvidos'],
  ['all', 'Todos'],
]

function Linha({ i, agora, aoReconhecer }: { i: Incidente; agora: number; aoReconhecer: (id: string) => void }) {
  const inicio = new Date(i.started_at).getTime()
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-4">
      <div className="space-y-1">
        <Link to={`/targets/${i.target_id}`} className="font-semibold underline-offset-2 hover:underline">
          {i.target_name}
        </Link>
        <p className="text-sm">
          {textoDoErroDeCheck(i.cause, null)} · início {dataHora(i.started_at)} ·{' '}
          {i.resolved_at
            ? `durou ${duracao(new Date(i.resolved_at).getTime() - inicio)}`
            : `em andamento há ${duracao(agora - inicio)}`}
        </p>
        {i.acknowledged_at && <p className="text-sm text-suave">Reconhecido em {dataHora(i.acknowledged_at)}</p>}
      </div>
      {!i.resolved_at && !i.acknowledged_at && (
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => aoReconhecer(i.id)}
          aria-label={`Reconhecer o incidente de ${i.target_name}`}
          title="Marca que você está ciente"
        >
          Reconhecer
        </Botao>
      )}
    </li>
  )
}

export function PaginaIncidentes() {
  const [params, setParams] = useSearchParams()
  const p = params.get('status')
  const filtro: Filtro = p === 'resolved' || p === 'all' ? p : 'open'
  const consulta = useIncidentes(filtro)
  const api = useApi()
  const qc = useQueryClient()
  const [erro, setErro] = useState<string | null>(null)
  const itens = consulta.data?.pages.flatMap((pg) => pg.items) ?? []

  async function reconhecer(id: string) {
    setErro(null)
    try {
      await api.reconhecer(id)
      await qc.invalidateQueries({ queryKey: ['incidents'] })
    } catch (e) {
      setErro(mensagemDe(e))
    }
  }

  return (
    <>
      <Titulo aba="Incidentes">Incidentes</Titulo>
      <fieldset className="flex gap-1">
        <legend className="sr-only">Filtro</legend>
        {FILTROS.map(([f, t]) => (
          <button
            key={f}
            type="button"
            aria-pressed={f === filtro}
            onClick={() => setParams({ status: f }, { replace: true })}
            className={cn('rounded-md border border-borda px-3 py-1 text-sm', f === filtro && 'bg-painel font-semibold')}
          >
            {t}
          </button>
        ))}
      </fieldset>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      {consulta.isPending ? (
        <Esqueleto linhas={2} />
      ) : consulta.error ? (
        <div role="alert" className="space-y-2">
          <p>{mensagemDe(consulta.error)}</p>
          <Botao tipo="secundario" tamanho="pequeno" onClick={() => void consulta.refetch()}>
            Tentar de novo
          </Botao>
        </div>
      ) : itens.length === 0 ? (
        <p className="text-suave">Nenhum incidente {filtro === 'open' ? 'aberto' : filtro === 'resolved' ? 'resolvido' : ''}.</p>
      ) : (
        <>
          <ul className="space-y-3">
            {itens.map((i) => (
              <Linha key={i.id} i={i} agora={consulta.dataUpdatedAt} aoReconhecer={(id) => void reconhecer(id)} />
            ))}
          </ul>
          {/* Botão, e não rolagem infinita: previsível no teclado e no leitor de tela. */}
          {consulta.hasNextPage && (
            <Botao tipo="secundario" disabled={consulta.isFetchingNextPage} onClick={() => void consulta.fetchNextPage()}>
              Carregar mais
            </Botao>
          )}
        </>
      )}
    </>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -1,4 +1,5 @@
 import type { RouteObject } from 'react-router'
+import { PaginaIncidentes } from '@/features/incidentes/PaginaIncidentes'
 import { PaginaLogin } from '@/features/login/PaginaLogin'
 import { PaginaPainel } from '@/features/painel/PaginaPainel'
 import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
@@ -25,6 +26,7 @@
             lazy: async () => ({ Component: (await import('@/features/alvos/PaginaDetalhe')).PaginaDetalhe }),
           },
           { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
+          { path: 'incidents', Component: PaginaIncidentes },
           { path: '*', Component: NaoEncontrada },
         ],
       },
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Em `PaginaIncidentes.tsx`, troque `{consulta.hasNextPage && (` por `{false && (`. Expected: FAIL em `"Carregar mais" segue o next_cursor`.
2. Troque `{!i.resolved_at && !i.acknowledged_at && (` por `{!i.acknowledged_at && (`. Expected: FAIL em `o filtro vai para a URL e muda a consulta` (um incidente resolvido ganha o botão Reconhecer).

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): lista de incidentes com filtro, paginação por cursor e reconhecimento"
```

---

### Task 8: Canais de notificação

`/channels`: cada canal com o estado em texto — verificado, aguardando verificação, verificação expirada e **falhando**, com o motivo (`last_error`) e a data (`last_failure_at`) do Plano 4 —; cadastrar Discord (a mensagem de teste já verifica) ou e-mail (link por 24 h); reenviar a verificação; apagar.

**Files:**
- Create: `web/src/features/canais/PaginaCanais.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/canais/canais.test.tsx`

**Interfaces:**
- Consumes: `useCanais`, `api.criarCanal`, `api.reenviarVerificacao`, `api.apagarCanal`, `Dialogo`, `Confirmar`, `textoDoErroDeCanal`, `mensagemDoCampo`, fixtures (Tasks 2–6).
- Produces: `PaginaCanais`.

**Contexto para quem implementa.** Textos de ajuda que as pendências pedem: "Webhook de canal de fórum não é aceito". Os erros: `502 delivery_failed` (a mensagem de teste não chegou), `400 kind/unavailable` (e-mail não configurado neste servidor), `409 channel_limit_reached`, `409 channel_not_pending` (recarrega) e `429` (o limite de 1 envio a cada 6 minutos). Os dois campos (URL e e-mail) têm `key`: sem ela, o React reaproveita o `<input>` ao trocar o tipo, e o e-mail digitado fica registrado no campo da URL — o teste desta tarefa achou isso (D6).

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/canais/canais.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { canal } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

describe('canais', () => {
  it('mostra cada estado em texto, e a falha com motivo e data', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal(),
      canal({ id: 'c2', kind: 'email', hint: 'd***@exemplo.com', status: 'pending' }),
      canal({
        id: 'c3',
        hint: 'discord.com/…/zzzz',
        status: 'failing',
        last_error: 'invalid_channel',
        last_failure_at: '2026-09-24T11:00:00Z',
      }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('✓ Verificado')).toBeInTheDocument()
    expect(screen.getByText('… Aguardando verificação')).toBeInTheDocument()
    expect(screen.getByText('✕ Falhando')).toBeInTheDocument()
    expect(screen.getByText(/Falha: Canal inválido \(o webhook foi apagado\?\)/)).toBeInTheDocument()
    // Reenviar só aparece no pendente.
    expect(screen.getAllByRole('button', { name: 'Reenviar verificação' })).toHaveLength(1)
  })

  it('cadastrar Discord verifica com a mensagem de teste', async () => {
    const r = await renderizarPainel('/channels')
    await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
    const dialogo = await screen.findByRole('dialog')
    expect(within(dialogo).getByText(/Webhook de canal de fórum não é aceito/)).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos('POST /channels')[0]).toEqual({ kind: 'discord', url: 'https://discord.com/api/webhooks/1/abc' })
    expect(await screen.findByText('Canal verificado com a mensagem de teste.')).toBeInTheDocument()
  })

  it('entrega que falha e e-mail não configurado viram texto claro', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.forcar.set('POST /channels', { status: 502, code: 'delivery_failed' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('Não conseguimos entregar a mensagem de teste')

    r.falsa.estado.forcar.set('POST /channels', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'kind', reason: 'unavailable' }],
    })
    await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
    await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), 'dono@exemplo.com')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() =>
      expect(within(dialogo).getByRole('alert')).toHaveTextContent('O envio de e-mail não está configurado'),
    )
    expect(r.falsa.corpos('POST /channels')[1]).toEqual({ kind: 'email', email: 'dono@exemplo.com' })
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/canais`
Expected: FAIL: `/channels` cai no 404 do painel.

- [ ] **Step 3: Implementar**

Crie `web/src/features/canais/PaginaCanais.tsx`:

```tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar, Dialogo } from '@/components/ui/dialogo'
import { useCanais } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo, textoDoErroDeCanal } from '@/lib/api/mensagens'
import type { Canal } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'

const estados: Record<Canal['status'], { texto: string; cor: string }> = {
  verified: { texto: '✓ Verificado', cor: 'text-no-ar' },
  pending: { texto: '… Aguardando verificação', cor: 'text-degradado' },
  expired: { texto: 'Verificação expirada', cor: 'text-sem-dados' },
  failing: { texto: '✕ Falhando', cor: 'text-fora' },
}

const esquema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('discord'), url: z.string().trim().pipe(z.url('Cole a URL do webhook.')) }),
  z.object({ kind: z.literal('email'), email: z.string().trim().pipe(z.email('Informe um e-mail válido.')) }),
])
type Valores = z.input<typeof esquema>

function NovoCanal({ aoCriar }: { aoCriar: (c: Canal) => void }) {
  const api = useApi()
  const f = useForm<Valores>({
    resolver: zodResolver(esquema),
    defaultValues: { kind: 'discord', url: '' } as Valores,
  })
  const [erro, setErro] = useState<string | null>(null)
  const tipo = useWatch({ control: f.control, name: 'kind' })
  const erros = f.formState.errors as Partial<Record<'url' | 'email', { message?: string }>>

  async function enviar(v: Valores) {
    setErro(null)
    try {
      const c = await api.criarCanal(
        v.kind === 'discord' ? { kind: 'discord', url: v.url } : { kind: 'email', email: v.email },
      )
      aoCriar(c)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'validation_failed' && e.fields[0]) {
        setErro(mensagemDoCampo(e.fields[0].field, e.fields[0].reason))
      } else if (e instanceof ApiError && e.code === 'rate_limited') {
        setErro('Limite de envios atingido: tente de novo em alguns minutos.')
      } else {
        setErro(mensagemDe(e))
      }
    }
  }

  return (
    <form noValidate className="space-y-4" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <fieldset className="flex gap-4">
        <legend className="mb-2 text-sm font-medium">Tipo</legend>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" value="discord" {...f.register('kind')} /> Discord
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" value="email" {...f.register('kind')} /> E-mail
        </label>
      </fieldset>
      {/* key: sem ela, o React reaproveita o mesmo <input> ao trocar o tipo,
          e o texto digitado fica registrado no campo anterior. */}
      {tipo === 'discord' ? (
        <Campo
          key="discord"
          rotulo="URL do webhook"
          erro={erros.url?.message}
          ajuda="Mandamos uma mensagem de teste agora. Webhook de canal de fórum não é aceito."
        >
          {(a) => <Entrada {...a} type="url" {...f.register('url')} />}
        </Campo>
      ) : (
        <Campo key="email" rotulo="E-mail" erro={erros.email?.message} ajuda="Mandamos um link de verificação, válido por 24 horas.">
          {(a) => <Entrada {...a} type="email" {...f.register('email')} />}
        </Campo>
      )}
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        Cadastrar canal
      </Botao>
    </form>
  )
}

export function PaginaCanais() {
  const consulta = useCanais()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [criando, setCriando] = useState(false)
  const [apagar, setApagar] = useState<Canal | null>(null)
  const [erro, setErro] = useState<string | null>(null)

  const recarregar = () => qc.invalidateQueries({ queryKey: ['channels'] })

  async function reenviar(c: Canal) {
    setErro(null)
    try {
      await api.reenviarVerificacao(c.id)
      avisar('Link de verificação reenviado.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'channel_not_pending') await recarregar()
      else setErro(mensagemDe(e))
    }
  }

  async function confirmarApagar() {
    if (!apagar) return
    try {
      await api.apagarCanal(apagar.id)
      await recarregar()
      avisar('Canal apagado.')
    } catch (e) {
      setErro(mensagemDe(e))
    } finally {
      setApagar(null)
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Titulo aba="Canais">Canais de notificação</Titulo>
        <Botao onClick={() => setCriando(true)}>Novo canal</Botao>
      </div>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) =>
          d.items.length === 0 ? (
            <p className="text-suave">Nenhum canal. Sem canal verificado, os incidentes não notificam ninguém.</p>
          ) : (
            <ul className="space-y-3">
              {d.items.map((c) => (
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-4">
                  <div className="space-y-1">
                    <p className="font-semibold">
                      {c.kind === 'discord' ? 'Discord' : 'E-mail'} <span className="font-normal text-suave">{c.hint}</span>
                    </p>
                    <p className={`text-sm font-medium ${estados[c.status].cor}`}>{estados[c.status].texto}</p>
                    {c.last_error && c.last_failure_at && (
                      <p className="text-sm text-suave">
                        {c.status === 'failing' ? 'Falha' : 'Última falha'}: {textoDoErroDeCanal(c.last_error)}, em{' '}
                        {dataHora(c.last_failure_at)}
                      </p>
                    )}
                  </div>
                  <div className="flex gap-2">
                    {(c.status === 'pending' || c.status === 'expired') && (
                      <Botao tipo="secundario" tamanho="pequeno" onClick={() => void reenviar(c)}>
                        Reenviar verificação
                      </Botao>
                    )}
                    <Botao
                      tipo="perigo"
                      tamanho="pequeno"
                      onClick={() => setApagar(c)}
                      aria-label={`Apagar o canal ${c.hint}`}
                    >
                      Apagar
                    </Botao>
                  </div>
                </li>
              ))}
            </ul>
          )
        }
      </EstadoDaConsulta>
      <Dialogo aberto={criando} aoMudar={setCriando} titulo="Novo canal">
        <NovoCanal
          aoCriar={(c) => {
            setCriando(false)
            void recarregar()
            avisar(c.kind === 'email' ? 'Enviamos o link de verificação.' : 'Canal verificado com a mensagem de teste.')
          }}
        />
      </Dialogo>
      <Confirmar
        aberto={apagar !== null}
        aoMudar={(v) => !v && setApagar(null)}
        titulo="Apagar o canal?"
        descricao={`O canal ${apagar?.hint ?? ''} deixa de receber os alertas.`}
        acao="Apagar canal"
        aoConfirmar={() => void confirmarApagar()}
      />
    </>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -1,4 +1,5 @@
 import type { RouteObject } from 'react-router'
+import { PaginaCanais } from '@/features/canais/PaginaCanais'
 import { PaginaIncidentes } from '@/features/incidentes/PaginaIncidentes'
 import { PaginaLogin } from '@/features/login/PaginaLogin'
 import { PaginaPainel } from '@/features/painel/PaginaPainel'
@@ -27,6 +28,7 @@
           },
           { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
           { path: 'incidents', Component: PaginaIncidentes },
+          { path: 'channels', Component: PaginaCanais },
           { path: '*', Component: NaoEncontrada },
         ],
       },
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Tire o `key="discord"` e o `key="email"` dos dois `Campo`. Expected: FAIL em `entrega que falha e e-mail não configurado viram texto claro` ("Informe um e-mail válido": o texto foi para o campo da URL). Tirar só um dos dois fica verde: chaves diferentes (`"discord"` e nenhuma) já bastam para o React remontar.
2. Em `NovoCanal`, troque `e.code === 'validation_failed' && e.fields[0]` por `e.code === 'nunca' && e.fields[0]`. (`if (false)` não compila: no `else if` seguinte, `e` volta a ser `unknown`.) Expected: FAIL no mesmo teste (a mensagem de e-mail não configurado some).
3. Troque `{c.last_error && c.last_failure_at && (` por `{false && (`. Expected: FAIL em `mostra cada estado em texto, e a falha com motivo e data`.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): canais com estado falhando, motivo da falha e verificação"
```

---

### Task 9: Status page

`/status-pages`: a única página do dono (`GET` devolve zero ou um item) — título, slug com sugestão a partir do título, publicada ou rascunho, os alvos na página com nome de exibição e ordem por botões Subir/Descer (teclado e leitor de tela; arrastar fica de fora), máximo 20; o link público com Copiar e Abrir; e uma prévia **aproximada** (nomes de exibição e o estado atual de cada alvo, do resumo).

**Files:**
- Create: `web/src/features/statuspage/PaginaStatusPage.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/statuspage/statuspage.test.tsx`

**Interfaces:**
- Consumes: `usePaginas`, `useAlvos`, `useResumo`, `api.criarPagina`, `api.alterarPagina`, `SeloDeEstado`, fixtures (Tasks 2–4).
- Produces: `PaginaStatusPage`, `sugerirSlug(titulo)`.

**Contexto para quem implementa.** A prévia fiel é inviável (spec do Plano 6 §5.7): rascunho responde 404 em `/p/{slug}`, `/p/` tem `frame-ancestors 'none'`, e a sessão do painel é Bearer em memória, que uma aba nova não leva. Por isso "Prévia aproximada", com esse rótulo. Os erros: `409 slug_taken` vai para o campo do slug; `409 status_page_exists` (corrida entre abas) recarrega. A página pública muda em até 30 segundos (cache do Plano 4), e o aviso de salvar diz isso. O PATCH troca a lista de itens inteira.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/statuspage/statuspage.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { sugerirSlug } from './PaginaStatusPage'

it('sugerirSlug tira acento e o que não cabe na regra', () => {
  expect(sugerirSlug('Situação da Ação — Produção!')).toBe('situacao-da-acao-producao')
  expect(sugerirSlug('  ')).toBe('')
})

describe('status page', () => {
  it('cria com alvos na ordem escolhida, e o slug ocupado cai no campo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' })]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha Empresa')
    await r.usuario.tab() // o blur sugere o slug
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('minha-empresa')

    for (const nome of ['API', 'Site']) {
      await r.usuario.selectOptions(screen.getByLabelText('Acrescentar alvo'), nome)
      await r.usuario.click(screen.getByRole('button', { name: 'Acrescentar' }))
    }
    await r.usuario.click(screen.getByRole('button', { name: 'Subir Site' }))

    r.falsa.estado.forcar.set('POST /status-pages', { status: 409, code: 'slug_taken' })
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(screen.getByLabelText('Endereço (slug)')).toHaveAccessibleDescription(/já está em uso/))

    r.falsa.estado.forcar.delete('POST /status-pages')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(r.falsa.chamadas('POST /status-pages')).toBe(2))
    expect(r.falsa.corpos('POST /status-pages')[1]).toEqual({
      title: 'Minha Empresa',
      slug: 'minha-empresa',
      published: false,
      items: [
        { target_id: 'alvo-2', display_name: 'Site' },
        { target_id: 'alvo-1', display_name: 'API' },
      ],
    })
    // Rascunho: o link existe, mas a página ainda não abre.
    expect(await screen.findByText('A página só fica acessível depois de publicada.')).toBeInTheDocument()
  })

  it('a prévia é rotulada como aproximada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [
      {
        id: 'pagina-1',
        slug: 'minha',
        title: 'Minha',
        published: true,
        items: [{ target_id: 'alvo-1', display_name: 'API pública' }],
        public_url: 'http://localhost:5173/p/minha',
        created_at: '2026-09-20T10:00:00Z',
      },
    ]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getByText('API pública')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Abrir' })).toHaveAttribute('rel', 'noopener noreferrer')
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/statuspage`
Expected: FAIL: `./PaginaStatusPage` não existe.

- [ ] **Step 3: Implementar**

Crie `web/src/features/statuspage/PaginaStatusPage.tsx`:

```tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario, Selecao } from '@/components/ui/campo'
import { useAlvos, usePaginas, useResumo } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Alvo, PaginaDeStatus } from '@/lib/api/schemas'

// O CHECK de status_pages.slug (Plano 4, D13).
const SLUG = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/
const MAX_ITENS = 20

const esquema = z.object({
  title: z.string().trim().min(1, 'Obrigatório.').refine((s) => [...s].length <= 100, 'Até 100 caracteres.'),
  slug: z.string().regex(SLUG, 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.'),
  published: z.boolean(),
  items: z
    .array(
      z.object({
        target_id: z.string().min(1),
        display_name: z.string().trim().min(1, 'Obrigatório.').refine((s) => [...s].length <= 100, 'Até 100 caracteres.'),
      }),
    )
    .max(MAX_ITENS, `Até ${MAX_ITENS} alvos.`),
})
type Valores = z.infer<typeof esquema>

export function sugerirSlug(titulo: string): string {
  return titulo
    .normalize('NFD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/, '')
}

function Formulario({ pagina, alvos }: { pagina: PaginaDeStatus | null; alvos: Alvo[] }) {
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const resumo = useResumo()
  const [erro, setErro] = useState<string | null>(null)
  const [escolhido, setEscolhido] = useState('')
  const f = useForm<Valores>({
    resolver: zodResolver(esquema),
    defaultValues: pagina
      ? { title: pagina.title, slug: pagina.slug, published: pagina.published, items: pagina.items }
      : { title: '', slug: '', published: false, items: [] },
  })
  const itens = useFieldArray({ control: f.control, name: 'items' })
  const e = f.formState.errors
  const nomeDoAlvo = new Map(alvos.map((a) => [a.id, a.name]))
  const escolhidos = useWatch({ control: f.control, name: 'items' })
  const usados = new Set(escolhidos.map((i) => i.target_id))
  const livres = alvos.filter((a) => !usados.has(a.id))

  async function enviar(v: Valores) {
    setErro(null)
    try {
      if (pagina) await api.alterarPagina(pagina.id, v)
      else await api.criarPagina(v)
      await qc.invalidateQueries({ queryKey: ['status-pages'] })
      avisar('Status page salva. A página pública muda em até 30 segundos.')
    } catch (err) {
      if (err instanceof ApiError && err.code === 'slug_taken') {
        f.setError('slug', { message: 'Este endereço já está em uso.' }, { shouldFocus: true })
      } else if (err instanceof ApiError && err.code === 'status_page_exists') {
        await qc.invalidateQueries({ queryKey: ['status-pages'] })
        setErro('Já existe uma status page; recarregamos a que está salva.')
      } else if (err instanceof ApiError && err.fields.length) {
        for (const fe of err.fields) {
          const raiz = fe.field.replace(/\[.*$/, '')
          if (raiz === 'title' || raiz === 'slug') f.setError(raiz, { message: mensagemDoCampo(fe.field, fe.reason) })
          else setErro(`${fe.field}: ${mensagemDoCampo(fe.field, fe.reason)}`)
        }
      } else {
        setErro(mensagemDe(err))
      }
    }
  }

  return (
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <Campo rotulo="Título" erro={e.title?.message}>
        {(a) => (
          <Entrada
            {...a}
            {...f.register('title', {
              onBlur: (ev: { target: { value: string } }) => {
                if (!f.getValues('slug')) f.setValue('slug', sugerirSlug(ev.target.value), { shouldValidate: true })
              },
            })}
          />
        )}
      </Campo>
      <Campo
        rotulo="Endereço (slug)"
        erro={e.slug?.message}
        ajuda="De 3 a 40 caracteres: letras minúsculas, números e hífens."
      >
        {(a) => <Entrada {...a} autoCapitalize="none" spellCheck={false} {...f.register('slug')} />}
      </Campo>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" {...f.register('published')} />
        Publicada (visível para qualquer pessoa com o link)
      </label>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">Alvos na página ({itens.fields.length} de {MAX_ITENS})</legend>
        {itens.fields.map((it, i) => (
          <div key={it.id} className="grid grid-cols-[1fr_auto] items-end gap-2 rounded-md border border-borda p-3">
            <Campo
              rotulo={`Nome de exibição de ${nomeDoAlvo.get(it.target_id) ?? 'alvo apagado'}`}
              erro={e.items?.[i]?.display_name?.message}
            >
              {(a) => <Entrada {...a} {...f.register(`items.${i}.display_name`)} />}
            </Campo>
            {/* Ordem por botões, não arrastar: teclado e leitor de tela. */}
            <div className="flex gap-1">
              <Botao tipo="fantasma" tamanho="pequeno" disabled={i === 0} onClick={() => itens.move(i, i - 1)}
                aria-label={`Subir ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}>
                Subir
              </Botao>
              <Botao tipo="fantasma" tamanho="pequeno" disabled={i === itens.fields.length - 1}
                onClick={() => itens.move(i, i + 1)} aria-label={`Descer ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}>
                Descer
              </Botao>
              <Botao tipo="fantasma" tamanho="pequeno" onClick={() => itens.remove(i)}
                aria-label={`Tirar ${nomeDoAlvo.get(it.target_id) ?? 'item'} da página`}>
                Tirar
              </Botao>
            </div>
          </div>
        ))}
        {e.items?.message && <p className="text-sm text-perigo">{e.items.message}</p>}
        {livres.length > 0 && itens.fields.length < MAX_ITENS && (
          <div className="flex items-end gap-2">
            <Campo rotulo="Acrescentar alvo">
              {(a) => (
                <Selecao {...a} value={escolhido} onChange={(ev) => setEscolhido(ev.target.value)}>
                  <option value="">Escolha…</option>
                  {livres.map((al) => (
                    <option key={al.id} value={al.id}>
                      {al.name}
                    </option>
                  ))}
                </Selecao>
              )}
            </Campo>
            <Botao
              tipo="secundario"
              disabled={!escolhido}
              onClick={() => {
                itens.append({ target_id: escolhido, display_name: nomeDoAlvo.get(escolhido) ?? '' })
                setEscolhido('')
              }}
            >
              Acrescentar
            </Botao>
          </div>
        )}
      </fieldset>

      {/* Prévia aproximada (spec do Plano 6, D23): a página real é HTML do
          servidor, só acessível depois de publicada. */}
      {escolhidos.length > 0 && (
        <section aria-labelledby="titulo-previa" className="space-y-2 rounded-md border border-dashed border-borda p-4">
          <h2 id="titulo-previa" className="font-semibold">
            Prévia aproximada
          </h2>
          <ul className="space-y-1">
            {escolhidos.map((it) => {
              const r = resumo.data?.items.find((x) => x.id === it.target_id)
              return (
                <li key={it.target_id} className="flex justify-between gap-4 text-sm">
                  <span>{it.display_name || '—'}</span>
                  {r ? <SeloDeEstado estado={estadoExibido(r.state, r.enabled)} /> : <span className="text-suave">—</span>}
                </li>
              )
            })}
          </ul>
        </section>
      )}

      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {pagina ? 'Salvar' : 'Criar status page'}
      </Botao>
    </form>
  )
}

export function PaginaStatusPage() {
  const paginas = usePaginas()
  const alvos = useAlvos()
  const [copiado, setCopiado] = useState(false)
  return (
    <>
      <Titulo aba="Status page">Status page</Titulo>
      <EstadoDaConsulta consulta={paginas}>
        {(d) => {
          const pagina = d.items[0] ?? null
          return (
            <>
              {pagina && (
                <div className="space-y-2 rounded-lg border border-borda p-4">
                  <p className="text-sm font-medium">Link público</p>
                  <p className="break-all">{pagina.public_url}</p>
                  {pagina.published ? (
                    <div className="flex gap-2">
                      <Botao
                        tipo="secundario"
                        tamanho="pequeno"
                        onClick={() => {
                          void navigator.clipboard?.writeText(pagina.public_url)
                          setCopiado(true)
                        }}
                      >
                        {copiado ? 'Copiado' : 'Copiar'}
                      </Botao>
                      <Botao tipo="secundario" tamanho="pequeno" asChild>
                        <a href={pagina.public_url} target="_blank" rel="noopener noreferrer">
                          Abrir
                        </a>
                      </Botao>
                    </div>
                  ) : (
                    <p className="text-sm text-suave">A página só fica acessível depois de publicada.</p>
                  )}
                </div>
              )}
              <EstadoDaConsulta consulta={alvos}>
                {(a) => <Formulario key={pagina?.id ?? 'nova'} pagina={pagina} alvos={a.items} />}
              </EstadoDaConsulta>
            </>
          )
        }}
      </EstadoDaConsulta>
    </>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -4,6 +4,7 @@
 import { PaginaLogin } from '@/features/login/PaginaLogin'
 import { PaginaPainel } from '@/features/painel/PaginaPainel'
 import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
+import { PaginaStatusPage } from '@/features/statuspage/PaginaStatusPage'
 import { ErroDeRota } from './ErroDeRota'
 import { ExigirSessao } from './ExigirSessao'
 import { Layout } from './Layout'
@@ -29,6 +30,7 @@
           { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
           { path: 'incidents', Component: PaginaIncidentes },
           { path: 'channels', Component: PaginaCanais },
+          { path: 'status-pages', Component: PaginaStatusPage },
           { path: '*', Component: NaoEncontrada },
         ],
       },
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Troque `if (err instanceof ApiError && err.code === 'slug_taken') {` por `if (false) {`. Expected: FAIL em `cria com alvos na ordem escolhida, e o slug ocupado cai no campo`.
2. No botão Subir, troque `itens.move(i, i - 1)` por `itens.move(i, i)`. Expected: FAIL no mesmo teste (a ordem enviada não muda).
3. Em `sugerirSlug`, tire o `.replace(/\p{M}/gu, '')`. Expected: FAIL em `sugerirSlug tira acento…`.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): status page do dono com ordem por botões, link público e prévia aproximada"
```

---

### Task 10: Configurações

`/settings`: a conta (`GET /me`), a troca de senha (com o código do 2FA só quando ele está ligado), ativar o 2FA (senha → QR e chave → código → códigos de recuperação exibidos uma vez, num diálogo que só fecha por "Concluir" depois de "Guardei os códigos"), desativar o 2FA, e as sessões ativas com Encerrar.

**Files:**
- Create: `web/src/features/configuracoes/PaginaConfiguracoes.tsx`
- Modify: `web/src/app/rotas.tsx`
- Test: `web/src/features/configuracoes/configuracoes.test.tsx`

**Interfaces:**
- Consumes: `useMe`, `useSessoes`, `useSessao`, `sessao.renovar`, `api.trocarSenha`, `api.encerrarSessao`, `api.totpCadastrar`, `api.totpConfirmar`, `api.totpDesativar`, `Dialogo` (com `travado`), `Confirmar`, `navegadorDe`, `pontosDeCodigo`, fixtures (Tasks 2–6).
- Produces: `PaginaConfiguracoes` (carregada sob demanda: o QR code fica fora do chunk principal).

**Contexto para quem implementa.** A política de senha é a do Plano 5 (D1): 15 a 128 **code points**, `[...s].length`; o servidor é a fonte da verdade e o `400 new_password` cai no campo. Senha atual e código errados dão **um** texto só (não há como separar, e não se deve). **Encerrar a sessão atual**: o `204` já apagou o cookie (D15 do Plano 5); o painel chama `sessao.encerrar('encerrada-aqui')` e **mais nada** — nem refresh, nem logout, nem recarregar a lista (a recarga, sem token, dispararia um refresh; o teste desta tarefa achou isso, e daí a guarda D3 no cliente). `404` ao encerrar é "a sessão já tinha terminado", não erro. O `user_agent` é texto de terceiros: renderizado como texto. O texto da janela de 15 minutos é o da nota do Plano 5 §12.

**As rotas de credencial** (`/auth/password`, `/auth/totp/enroll`, `/confirm`, `/disable`) nunca renovam sozinhas (D2). Um `401 unauthorized` delas é o acesso vencido nesta tela, ou a família da sessão morta (Task 11 do Plano 5: os três do 2FA exigem família viva, e a troca de senha também responde assim quando a família morre no meio). A tela guarda o formulário e mostra "O acesso desta tela venceu." com **"Renovar acesso e tentar de novo"** (`AvisoDeCredencial`): o botão chama `sessao.renovar`; renovado, diz "Acesso renovado. Envie de novo." (um aviso que some no envio seguinte, como os erros) e **o usuário** reenvia — nada em `/auth/*` se repete sozinho (reconciliação, item 3). Refresh recusado (família morta) é o fim da sessão: login, com "Sua sessão terminou". Os diálogos do 2FA usam o mesmo caminho.

**Código de recuperação na troca de senha** (PENDENCIAS do Plano 6): ele é gasto antes da transação da troca, e o `401` não diz se foi. Em qualquer `401` depois de enviar um código que não é de 6 dígitos, a tela acrescenta "Se você usou um código de recuperação, ele pode ter sido consumido; use outro." A API falsa tem a variante (`trocaFalhaDepoisDoCodigo`) que gasta o código e depois responde `401 unauthorized`.

**Os diálogos do 2FA.** No passo dos códigos de recuperação, o diálogo fica **travado** (`travado` do `Dialogo`): Esc e o clique fora não fecham, só "Concluir" depois de "Guardei os códigos". O `['me']` não vence sozinho (`staleTime: Infinity`), e outra aba pode ter ligado ou desligado o 2FA: abrir e fechar um diálogo do 2FA relê o `/me`. "Desativar" só abre com `totp_enabled` verdadeiro no `/me` relido: sem 2FA, o `POST /auth/totp/disable` conta tentativa falha na conta (R34). Simétrico, "Ativar" só abre com ele falso (outra aba pode tê-lo ligado: o enroll responderia 403) — **ou** com o diálogo travado: depois do confirm, o `/me` relido já diz "ligado", e os códigos de recuperação só existem no diálogo. Os testes da sessão morta e do cadastro apagado usam as guardas da API falsa (Task 2): a família revogada com o access token ainda válido, e uma troca de senha de verdade (`api.trocarSenha`) no meio da ativação. Trocar a senha apaga um cadastro de 2FA pendente, e o `confirm` seguinte responde o mesmo `400` de um código errado: o texto sugere recomeçar.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `web/src/features/configuracoes/configuracoes.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RECUPERACAO_OK, TOTP_OK } from '@/test/api-falsa'
import { sessao as familia } from '@/test/fixtures'
import { renderizarPainel, SENHA } from '@/test/renderizar'

describe('senha', () => {
  it('a política conta code points: 14 emojis não bastam, mesmo sendo 28 unidades UTF-16', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    const nova = screen.getByLabelText('Senha nova')
    await r.usuario.type(nova, '😀'.repeat(14))
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), '😀'.repeat(14))
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    await waitFor(() => expect(nova).toHaveAccessibleDescription(/Mínimo de 15 caracteres/))
    expect(r.falsa.chamadas('POST /auth/password')).toBe(0)
  })

  it('troca e avisa da janela de 15 minutos', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByText(/pode durar até 15 minutos/)).toBeInTheDocument()
    expect(r.falsa.corpos('POST /auth/password')[0]).toEqual({
      current_password: SENHA,
      new_password: 'outra senha bem comprida',
    })
  })

  it('com 2FA, pede o código; senha ou código errados dão um texto só', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Código do autenticador'), '000000')
    await r.usuario.type(screen.getByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    const alerta = await screen.findByRole('alert')
    expect(alerta).toHaveTextContent('Senha atual ou código incorretos.')
    // Um código de 6 dígitos não é de recuperação: nada de aviso de código gasto.
    expect(alerta).not.toHaveTextContent('código de recuperação')
  })

  // PENDENCIAS do Plano 6: o código de recuperação é gasto antes da
  // transação da troca; se ela falha depois, o usuário tem de saber que
  // aquele código não vale mais. Sabotagem prevista: em ehDeRecuperacao,
  // trocar "!/^\d{6}$/.test(codigo)" por "/^\d{6}$/.test(codigo)" (este fica
  // vermelho, e o teste acima também).
  it('troca que falha depois de gastar o código de recuperação avisa que ele pode ter sido consumido', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    r.falsa.estado.trocaFalhaDepoisDoCodigo = true
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Código do autenticador'), RECUPERACAO_OK)
    await r.usuario.type(screen.getByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Se você usou um código de recuperação, ele pode ter sido consumido; use outro.',
    )
    expect(r.falsa.estado.recuperacaoGasta.has(RECUPERACAO_OK)).toBe(true)
  })

  // D2 e I5 da revisão: /auth/* nunca renova sozinho. O formulário fica, e
  // "Renovar acesso e tentar de novo" renova; quem reenvia é o usuário.
  // Sabotagens previstas: a 7 da Task 2 (renovar em /auth/*: a troca passa
  // no primeiro envio, sem o aviso) e, no botão, trocar
  // "const novo = await sessao.renovar(sessao.token())" por
  // "const novo = sessao.token()" (não renova: o segundo envio volta 401).
  it('com o acesso vencido nesta tela, guarda o formulário e oferece renovar; o reenvio é do usuário', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    const refreshes = r.falsa.chamadas('POST /auth/refresh')
    r.falsa.expirarAcessos()
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('O acesso desta tela venceu.')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshes)
    expect(screen.getByLabelText('Senha atual')).toHaveValue(SENHA)
    await r.usuario.click(screen.getByRole('button', { name: 'Renovar acesso e tentar de novo' }))
    expect(await screen.findByText('Acesso renovado. Envie de novo.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshes + 1)
    expect(r.falsa.chamadas('POST /auth/password')).toBe(1)
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByText(/pode durar até 15 minutos/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/password')).toBe(2)
    // O aviso de renovado não sobrevive ao envio seguinte. Sabotagem
    // prevista: no início do envio, trocar "setErro(null)" por
    // "setErro((e) => (e?.renovado ? e : null))".
    expect(screen.queryByText('Acesso renovado. Envie de novo.')).not.toBeInTheDocument()
  })

  // Task 11 do Plano 5: os diálogos do 2FA respondem "unauthorized" quando
  // a família da sessão morreu. O mesmo caminho: renovar; refresh recusado
  // é o fim da sessão, com o motivo.
  it('no diálogo do 2FA, acesso vencido com a sessão morta volta ao login', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    // A família morre (encerrada em outro aparelho); o access token desta
    // aba continua válido, e é a guarda da rota que o recusa.
    r.falsa.estado.familiaRevogada = true
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('O acesso desta tela venceu.')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Renovar acesso e tentar de novo' }))
    expect(await screen.findByText('Sua sessão terminou. Entre de novo.')).toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/login')
  })
})

describe('sessões', () => {
  // Nota do Plano 5 §12: o 204 já apagou o cookie; o painel larga o token e
  // vai ao login SEM chamar refresh nem logout. Sabotagem prevista: trocar
  // sessao.encerrar('encerrada-aqui') por sessao.sair() → 1 logout.
  it('encerrar a própria sessão volta ao login, sem refresh nem logout depois', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia(), familia({ id: 'fam-2', current: false, user_agent: '' })]
    await r.cliente.refetchQueries()
    const itens = await screen.findAllByRole('button', { name: 'Encerrar' })
    expect(screen.getByText('Esta sessão')).toBeInTheDocument()
    expect(screen.getByText('Navegador desconhecido')).toBeInTheDocument()
    const refreshesAntes = r.falsa.chamadas('POST /auth/refresh')
    await r.usuario.click(itens[0]!)
    const dialogo = await screen.findByRole('alertdialog')
    expect(within(dialogo).getByText('Sair deste navegador?')).toBeInTheDocument()
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Encerrar' }))
    expect(await screen.findByText('Você encerrou esta sessão.')).toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/login')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshesAntes)
    expect(r.falsa.chamadas('POST /auth/logout')).toBe(0)
    expect(r.sessao.token()).toBeNull()
  })

  it('404 ao encerrar é "já tinha terminado", não erro', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia({ id: 'fam-2', current: false })]
    await r.cliente.refetchQueries()
    r.falsa.estado.sessoes = []
    await r.usuario.click(await screen.findByRole('button', { name: 'Encerrar' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Encerrar' }))
    expect(await screen.findByText('Esta sessão já tinha terminado.')).toBeInTheDocument()
  })

  it('o user agent é texto, nunca HTML', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia({ current: false, user_agent: '<img src=x onerror=alert(1)>' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(document.querySelector('img')).toBeNull()
  })
})

describe('2FA', () => {
  it('ativar: senha, QR e chave, código, e os códigos de recuperação só fecham depois de guardados', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect(await within(dialogo).findByText('JBSWY3DPEHPK3PXP')).toBeInTheDocument()
    expect(within(dialogo).getByTitle('QR code do autenticador')).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    expect(await within(dialogo).findByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
    const concluir = within(dialogo).getByRole('button', { name: 'Concluir' })
    expect(concluir).toBeDisabled()
    await r.usuario.click(within(dialogo).getByLabelText('Guardei os códigos'))
    await r.usuario.click(concluir)
    expect(await screen.findByText('Ativada.')).toBeInTheDocument()
  })

  // PENDENCIAS do Plano 6: trocar a senha apaga o cadastro pendente, e o
  // confirm responde o mesmo 400 de um código errado.
  it('código não aceito na ativação sugere recomeçar, se a senha foi trocada', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await within(dialogo).findByLabelText('Código de 6 dígitos')
    // A senha é trocada nesta mesma sessão (outra aba): a API falsa apaga o
    // cadastro pendente, como o servidor.
    await r.api.trocarSenha({ current_password: SENHA, new_password: 'outra senha bem comprida' })
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('feche e comece de novo')
  })

  // I2 da revisão: no passo dos códigos, Esc (e o clique fora) não fecham —
  // os códigos não aparecem de novo. Sabotagem prevista: no Dialogo de
  // ativar, trocar "travado={travado}" por "travado={travado && Math.random() > 2}".
  it('no passo dos códigos, Esc não fecha o diálogo', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    await within(dialogo).findByText('AAAABBBBCCCCDDDD')
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(within(dialogo).getByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
    // O /me relido (foco da janela, outro poll) já diz "ligado": o diálogo
    // continua, pelo travado. Sabotagem prevista: trocar
    // "(totp === false || travado)" por "totp === false && (travado || !travado)".
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBeGreaterThanOrEqual(3))
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(within(dialogo).getByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
  })

  // I2/M11: ['me'] não vence sozinho; abrir e fechar os diálogos do 2FA o
  // releem. Sabotagem prevista: trocar o reler por "const reler = () => void qc"
  // (nada invalida; o teste de ativar também fica vermelho: o "Ativada." vem do /me relido).
  it('abrir e fechar um diálogo do 2FA relê o /me', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    await screen.findByRole('dialog')
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBe(2))
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBe(3))
  })

  // Simétrico: com o 2FA ligado em outra aba, "Ativar" não abre (o enroll
  // responderia 403). Sabotagem prevista: trocar
  // "dialogo === 'ativar' && (totp === false || travado)" por
  // "dialogo === 'ativar' && (totp === false || travado || true)".
  it('com o 2FA ligado em outra aba, "Ativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    const ativar = await screen.findByRole('button', { name: 'Ativar' })
    r.falsa.estado.totpLigado = true
    await r.usuario.click(ativar)
    expect(await screen.findByRole('button', { name: 'Desativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/enroll')).toBe(0)
  })

  // PENDENCIAS do Plano 6: sem 2FA ligado, "desativar" não se oferece (o
  // POST /auth/totp/disable contaria tentativa falha, R34). Outra aba o
  // desligou: o /me relido ao abrir não deixa o diálogo aparecer.
  // Sabotagem prevista: trocar "dialogo === 'desativar' && totp === true"
  // por "dialogo === 'desativar' && (totp === true || true)".
  it('com o 2FA desligado em outra aba, "Desativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    const desativar = await screen.findByRole('button', { name: 'Desativar' })
    r.falsa.estado.totpLigado = false
    await r.usuario.click(desativar)
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/disable')).toBe(0)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/configuracoes`
Expected: FAIL: `/settings` cai no 404 do painel.

- [ ] **Step 3: Implementar**

Crie `web/src/features/configuracoes/PaginaConfiguracoes.tsx`:

```tsx
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { useApi, useSessao } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar, Dialogo } from '@/components/ui/dialogo'
import { chaves, useMe, useSessoes } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Sessao } from '@/lib/api/schemas'
import { dataHora, navegadorDe, pontosDeCodigo } from '@/lib/format'

// As rotas de credencial (/auth/password, /auth/totp/*) nunca renovam o
// token sozinhas (reconciliação, item 3): um "unauthorized" delas é o
// access token vencido nesta tela (ou, com a Task 11 do Plano 5, a família
// da sessão morta). A tela guarda o formulário e oferece renovar; renovado,
// quem envia de novo é o usuário — nada em /auth/* se repete sozinho.
// renovado: não é erro, é o "Acesso renovado. Envie de novo." — some no
// próximo envio, como qualquer aviso do formulário.
type ErroDeCredencial = { texto: string; renovavel: boolean; renovado?: boolean }

// Um código que não é de 6 dígitos é de recuperação (16 caracteres base32).
const ehDeRecuperacao = (codigo: string | undefined) => !!codigo && !/^\d{6}$/.test(codigo)

function erroDeCredencial(e: unknown, errado: string, codigoEnviado?: string): ErroDeCredencial {
  let r: ErroDeCredencial
  if (e instanceof ApiError && e.code === 'invalid_credentials') r = { texto: errado, renovavel: false }
  else if (e instanceof ApiError && e.code === 'unauthorized') {
    r = { texto: 'O acesso desta tela venceu.', renovavel: true }
  } else r = { texto: mensagemDe(e), renovavel: false }
  // PENDENCIAS do Plano 6: o código de recuperação é consumido ANTES da
  // transação da troca de senha. Se ela falha depois (família revogada,
  // outra troca concorrente), o código já foi gasto.
  if (e instanceof ApiError && e.status === 401 && ehDeRecuperacao(codigoEnviado)) {
    r = { ...r, texto: `${r.texto} Se você usou um código de recuperação, ele pode ter sido consumido; use outro.` }
  }
  return r
}

// O erro de um formulário de credencial, com "Renovar acesso e tentar de
// novo" quando o que venceu foi o acesso. Refresh recusado é o fim da
// sessão (volta ao login com o motivo), como em qualquer outra rota.
function AvisoDeCredencial({
  erro,
  aoMudar,
}: {
  erro: ErroDeCredencial | null
  aoMudar: (e: ErroDeCredencial | null) => void
}) {
  const { sessao } = useSessao()
  const [falha, setFalha] = useState<string | null>(null)
  if (!erro) return null
  if (erro.renovado) return <p className="text-sm">{erro.texto}</p>
  return (
    <div className="space-y-2">
      <ErroDoFormulario>{falha ?? erro.texto}</ErroDoFormulario>
      {erro.renovavel && (
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={async () => {
            setFalha(null)
            try {
              const novo = await sessao.renovar(sessao.token())
              if (!novo) {
                sessao.encerrar('expirou')
                return
              }
              aoMudar({ texto: 'Acesso renovado. Envie de novo.', renovavel: false, renovado: true })
            } catch (e) {
              setFalha(mensagemDe(e))
            }
          }}
        >
          Renovar acesso e tentar de novo
        </Botao>
      )}
    </div>
  )
}

// Política de senha nova do Plano 5 (D1): 15 a 128 code points, qualquer
// caractere, sem regra de composição. O servidor é a fonte da verdade.
const novaSenha = z
  .string()
  .refine((s) => pontosDeCodigo(s) >= 15, 'Mínimo de 15 caracteres.')
  .refine((s) => pontosDeCodigo(s) <= 128, 'Máximo de 128 caracteres.')

function TrocaDeSenha({ comTotp }: { comTotp: boolean }) {
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const esquema = z
    .object({ atual: z.string().min(1, 'Obrigatório.'), nova: novaSenha, confirmacao: z.string(), codigo: z.string() })
    .refine((v) => v.nova === v.confirmacao, { path: ['confirmacao'], message: 'As duas senhas não são iguais.' })
    .refine((v) => !comTotp || v.codigo.trim().length >= 6, { path: ['codigo'], message: 'Informe o código.' })
  const f = useForm({
    resolver: zodResolver(esquema),
    defaultValues: { atual: '', nova: '', confirmacao: '', codigo: '' },
  })
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const e = f.formState.errors

  return (
    <form
      noValidate
      className="max-w-md space-y-4"
      onSubmit={f.handleSubmit(async (v) => {
        setErro(null)
        const codigo = comTotp ? v.codigo.replace(/\s+/g, '') : undefined
        try {
          await api.trocarSenha({
            current_password: v.atual,
            new_password: v.nova,
            ...(codigo !== undefined ? { totp_code: codigo } : {}),
          })
          f.reset()
          await qc.invalidateQueries({ queryKey: chaves.sessoes })
          avisar(
            'Senha alterada. As outras sessões foram encerradas; um acesso já aberto em outro aparelho pode durar até 15 minutos.',
          )
        } catch (err) {
          if (err instanceof ApiError && err.fields[0]?.field === 'new_password') {
            f.setError(
              'nova',
              { message: mensagemDoCampo('new_password', err.fields[0].reason) },
              { shouldFocus: true },
            )
          } else {
            // Não há como separar senha errada de código errado, e não se deve.
            setErro(
              erroDeCredencial(err, comTotp ? 'Senha atual ou código incorretos.' : 'Senha atual incorreta.', codigo),
            )
          }
        }
      })}
    >
      <AvisoDeCredencial erro={erro} aoMudar={setErro} />
      <Campo rotulo="Senha atual" erro={e.atual?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('atual')} />}
      </Campo>
      <Campo rotulo="Senha nova" erro={e.nova?.message} ajuda="De 15 a 128 caracteres. Uma frase serve.">
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('nova')} />}
      </Campo>
      <Campo rotulo="Confirme a senha nova" erro={e.confirmacao?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('confirmacao')} />}
      </Campo>
      {comTotp && (
        <Campo rotulo="Código do autenticador" erro={e.codigo?.message} ajuda="Ou um código de recuperação.">
          {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
        </Campo>
      )}
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        Trocar a senha
      </Botao>
    </form>
  )
}

// O confirm responde o mesmo 400 para código errado e para cadastro que
// sumiu (trocar a senha apaga o cadastro pendente: PENDENCIAS do Plano 6).
const CODIGO_NAO_ACEITO =
  'Código incorreto. Se a senha foi trocada depois de o QR code aparecer, feche e comece de novo.'

function segredoDe(uri: string): string {
  try {
    return new URL(uri).searchParams.get('secret') ?? ''
  } catch {
    return ''
  }
}

function AtivarTotp({ aoTerminar, aoTravar }: { aoTerminar: () => void; aoTravar: () => void }) {
  const api = useApi()
  const [passo, setPasso] = useState<'senha' | 'codigo' | 'codigos'>('senha')
  const [uri, setUri] = useState('')
  const [codigos, setCodigos] = useState<string[]>([])
  const [guardei, setGuardei] = useState(false)
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const senha = useForm({ defaultValues: { senha: '' } })
  const codigo = useForm({ defaultValues: { codigo: '' } })

  if (passo === 'senha') {
    return (
      <form
        noValidate
        className="space-y-4"
        onSubmit={senha.handleSubmit(async (v) => {
          setErro(null)
          try {
            setUri((await api.totpCadastrar(v.senha)).otpauth_uri)
            setPasso('codigo')
          } catch (e) {
            setErro(erroDeCredencial(e, 'Senha incorreta.'))
          }
        })}
      >
        <AvisoDeCredencial erro={erro} aoMudar={setErro} />
        <Campo rotulo="Sua senha">
          {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...senha.register('senha')} />}
        </Campo>
        <Botao type="submit">Continuar</Botao>
      </form>
    )
  }
  if (passo === 'codigo') {
    return (
      <form
        noValidate
        className="space-y-4"
        onSubmit={codigo.handleSubmit(async (v) => {
          setErro(null)
          try {
            setCodigos((await api.totpConfirmar(v.codigo.replace(/\s+/g, ''))).recovery_codes)
            // Daqui em diante os códigos só existem nesta tela: Esc e o
            // clique fora não fecham o diálogo, só "Concluir".
            aoTravar()
            setPasso('codigos')
          } catch (e) {
            setErro(
              e instanceof ApiError && e.code === 'validation_failed'
                ? { texto: CODIGO_NAO_ACEITO, renovavel: false }
                : erroDeCredencial(e, 'Código incorreto.'),
            )
          }
        })}
      >
        <AvisoDeCredencial erro={erro} aoMudar={setErro} />
        <p className="text-sm">Leia o QR code com o aplicativo autenticador, ou digite a chave.</p>
        <div className="inline-block bg-white p-3">
          <QRCodeSVG value={uri} size={176} title="QR code do autenticador" />
        </div>
        <p className="text-sm">
          Chave: <code className="break-all">{segredoDe(uri)}</code>
        </p>
        <Campo rotulo="Código de 6 dígitos">
          {(a) => <Entrada {...a} inputMode="numeric" autoComplete="one-time-code" {...codigo.register('codigo')} />}
        </Campo>
        <Botao type="submit">Ativar</Botao>
      </form>
    )
  }
  return (
    <div className="space-y-4">
      <p className="text-sm">
        Guarde estes códigos de recuperação num lugar seguro. Cada um vale uma vez, e eles não aparecem de novo.
      </p>
      <ul className="grid grid-cols-2 gap-1 font-mono text-sm">
        {codigos.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => void navigator.clipboard?.writeText(codigos.join('\n'))}
        >
          Copiar
        </Botao>
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => {
            const url = URL.createObjectURL(new Blob([codigos.join('\n') + '\n'], { type: 'text/plain' }))
            const a = document.createElement('a')
            a.href = url
            a.download = 'vigilume-codigos-de-recuperacao.txt'
            a.click()
            URL.revokeObjectURL(url)
          }}
        >
          Baixar .txt
        </Botao>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={guardei} onChange={(e) => setGuardei(e.target.checked)} />
        Guardei os códigos
      </label>
      <Botao disabled={!guardei} onClick={aoTerminar}>
        Concluir
      </Botao>
    </div>
  )
}

function DesativarTotp({ aoTerminar }: { aoTerminar: () => void }) {
  const api = useApi()
  const f = useForm({ defaultValues: { senha: '', codigo: '' } })
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={f.handleSubmit(async (v) => {
        setErro(null)
        try {
          await api.totpDesativar(v.senha, v.codigo.replace(/\s+/g, ''))
          aoTerminar()
        } catch (e) {
          setErro(erroDeCredencial(e, 'Senha ou código incorretos.'))
        }
      })}
    >
      <AvisoDeCredencial erro={erro} aoMudar={setErro} />
      <Campo rotulo="Sua senha">
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('senha')} />}
      </Campo>
      <Campo rotulo="Código do autenticador" ajuda="Ou um código de recuperação.">
        {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
      </Campo>
      <Botao type="submit" tipo="perigo">
        Desativar
      </Botao>
    </form>
  )
}

function Sessoes() {
  const consulta = useSessoes()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const { sessao } = useSessao()
  const [alvo, setAlvo] = useState<Sessao | null>(null)
  const [erro, setErro] = useState<string | null>(null)

  async function encerrar() {
    if (!alvo) return
    const atual = alvo.current
    setErro(null)
    try {
      await api.encerrarSessao(alvo.id)
      if (atual) {
        // O 204 já apagou o cookie (D15 do Plano 5). O access token em
        // memória ainda valeria até 15 minutos: o painel é quem o larga,
        // sem chamar refresh nem logout, e sem recarregar nada.
        sessao.encerrar('encerrada-aqui')
        return
      }
      avisar('Sessão encerrada.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') avisar('Esta sessão já tinha terminado.')
      else setErro(mensagemDe(e))
    }
    setAlvo(null)
    await qc.invalidateQueries({ queryKey: chaves.sessoes })
  }

  return (
    <>
      <p className="text-sm text-suave">
        Encerrar impede a renovação. Um acesso já aberto no outro aparelho pode continuar por até 15 minutos.
      </p>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) => (
          <ul className="space-y-2">
            {d.items.map((s) => (
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-3"
              >
                <div className="text-sm">
                  <p className="font-medium">
                    {navegadorDe(s.user_agent)}
                    {s.current && <span className="ml-2 rounded bg-painel px-2 py-0.5 text-xs">Esta sessão</span>}
                  </p>
                  <p className="text-suave">
                    Desde {dataHora(s.created_at)} · último acesso por volta de {dataHora(s.last_used_at)}
                  </p>
                </div>
                <Botao tipo="perigo" tamanho="pequeno" onClick={() => setAlvo(s)}>
                  Encerrar
                </Botao>
              </li>
            ))}
          </ul>
        )}
      </EstadoDaConsulta>
      <Confirmar
        aberto={alvo !== null}
        aoMudar={(v) => !v && setAlvo(null)}
        titulo={alvo?.current ? 'Sair deste navegador?' : 'Encerrar a sessão?'}
        descricao={
          alvo?.current
            ? 'Esta é a sessão que você está usando: o painel volta para a tela de entrada.'
            : 'O outro aparelho não consegue mais renovar o acesso.'
        }
        acao="Encerrar"
        aoConfirmar={() => void encerrar()}
      />
    </>
  )
}

export function PaginaConfiguracoes() {
  const me = useMe()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [dialogo, setDialogo] = useState<'ativar' | 'desativar' | null>(null)
  const [travado, setTravado] = useState(false)
  // ['me'] não vence sozinho (staleTime Infinity): outra aba pode ter
  // ligado ou desligado o 2FA. Abrir e fechar um diálogo do 2FA relê /me.
  const reler = () => void qc.invalidateQueries({ queryKey: chaves.me })
  const abrir = (d: 'ativar' | 'desativar') => {
    reler()
    setDialogo(d)
  }
  const fechar = () => {
    setDialogo(null)
    setTravado(false)
    reler()
  }
  const terminar = (texto: string) => {
    fechar()
    avisar(texto)
  }
  const totp = me.data?.totp_enabled
  return (
    <>
      <Titulo aba="Configurações">Configurações</Titulo>
      <EstadoDaConsulta consulta={me}>
        {(eu) => (
          <>
            <section aria-labelledby="t-conta" className="space-y-1">
              <h2 id="t-conta" className="text-lg font-semibold">
                Conta
              </h2>
              <p>{eu.email}</p>
              <p className="text-sm text-suave">Desde {dataHora(eu.created_at)}</p>
            </section>
            <section aria-labelledby="t-senha" className="space-y-3">
              <h2 id="t-senha" className="text-lg font-semibold">
                Senha
              </h2>
              <TrocaDeSenha comTotp={eu.totp_enabled} />
            </section>
            <section aria-labelledby="t-2fa" className="space-y-3">
              <h2 id="t-2fa" className="text-lg font-semibold">
                Verificação em duas etapas
              </h2>
              <p className="text-sm">{eu.totp_enabled ? 'Ativada.' : 'Desativada.'}</p>
              <Botao
                tipo={eu.totp_enabled ? 'perigo' : 'primario'}
                onClick={() => abrir(eu.totp_enabled ? 'desativar' : 'ativar')}
              >
                {eu.totp_enabled ? 'Desativar' : 'Ativar'}
              </Botao>
            </section>
          </>
        )}
      </EstadoDaConsulta>
      <section aria-labelledby="t-sessoes" className="space-y-3">
        <h2 id="t-sessoes" className="text-lg font-semibold">
          Sessões ativas
        </h2>
        <Sessoes />
      </section>
      {/* Ativar só com o 2FA desligado segundo o /me relido (outra aba pode
          tê-lo ligado: o enroll responderia 403). Depois do confirm, o /me
          diz "ligado", e o diálogo continua aberto pelo travado: os códigos
          de recuperação só existem nele. */}
      <Dialogo
        aberto={dialogo === 'ativar' && (totp === false || travado)}
        aoMudar={(v) => !v && fechar()}
        travado={travado}
        titulo="Ativar a verificação em duas etapas"
      >
        <AtivarTotp
          aoTravar={() => setTravado(true)}
          aoTerminar={() => terminar('Verificação em duas etapas ativada.')}
        />
      </Dialogo>
      {/* Desativar só com o 2FA ligado segundo o /me relido: sem 2FA, o
          POST /auth/totp/disable conta tentativa falha na conta (R34). */}
      <Dialogo
        aberto={dialogo === 'desativar' && totp === true}
        aoMudar={(v) => !v && fechar()}
        titulo="Desativar a verificação em duas etapas"
      >
        <DesativarTotp aoTerminar={() => terminar('Verificação em duas etapas desativada.')} />
      </Dialogo>
    </>
  )
}
```

Aplique em `web/src/app/rotas.tsx`:

```diff
--- a/web/src/app/rotas.tsx
+++ b/web/src/app/rotas.tsx
@@ -31,6 +31,12 @@
           { path: 'incidents', Component: PaginaIncidentes },
           { path: 'channels', Component: PaginaCanais },
           { path: 'status-pages', Component: PaginaStatusPage },
+          {
+            path: 'settings',
+            lazy: async () => ({
+              Component: (await import('@/features/configuracoes/PaginaConfiguracoes')).PaginaConfiguracoes,
+            }),
+          },
           { path: '*', Component: NaoEncontrada },
         ],
       },
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run typecheck && npm run lint && npm test && npm run build`
Expected: PASS. O build tem um chunk `PaginaConfiguracoes-*.js` separado.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; rode `npx vitest run -t "<nome>"` em `web/`; restaure:
1. Troque `sessao.encerrar('encerrada-aqui')` por `await sessao.sair()`. Expected: FAIL em `encerrar a própria sessão volta ao login, sem refresh nem logout depois` (um logout).
2. Tire o `return` depois de `sessao.encerrar('encerrada-aqui')` **e** faça a sabotagem 8 da Task 2 (`if (!podeRenovar(` em `cliente.ts`). Expected: FAIL no mesmo teste (a recarga sem token dispara um refresh). Só uma das duas fica verde: são duas guardas da mesma propriedade.
3. Em `novaSenha`, troque `pontosDeCodigo(s) >= 15` por `s.length >= 15`. Expected: FAIL em `a política conta code points: 14 emojis não bastam, mesmo sendo 28 unidades UTF-16` (14 emojis são 28 unidades UTF-16).
4. Troque `disabled={!guardei}` por `disabled={false}`. Expected: FAIL em `ativar: senha, QR e chave, código…`.
5. Em `AvisoDeCredencial`, troque `const novo = await sessao.renovar(sessao.token())` por `const novo = sessao.token()`. Expected: FAIL em `com o acesso vencido nesta tela, guarda o formulário e oferece renovar; o reenvio é do usuário` (o segundo envio volta 401) e em `no diálogo do 2FA, acesso vencido com a sessão morta volta ao login`.
6. Faça a sabotagem 7 da Task 2 (`caminho !== ''` no lugar de `!caminho.startsWith('/auth/')`). Expected: FAIL nos mesmos dois testes: o painel renova e repete sozinho, e o primeiro envio passa sem o aviso — sem o usuário ter pedido.
7. Em `ehDeRecuperacao`, troque `!/^\d{6}$/.test(codigo)` por `/^\d{6}$/.test(codigo)`. Expected: FAIL em `troca que falha depois de gastar o código de recuperação…` e em `com 2FA, pede o código; senha ou código errados dão um texto só` (o aviso aparece para o código de 6 dígitos).
8. No `Dialogo` de ativar, troque `travado={travado}` por `travado={travado && Math.random() > 2}`. Expected: FAIL em `no passo dos códigos, Esc não fecha o diálogo`.
9. Troque o `reler` por `const reler = () => void qc`. Expected: FAIL em `abrir e fechar um diálogo do 2FA relê o /me`, em `com o 2FA desligado em outra aba…` e em `ativar: senha, QR e chave…` (o "Ativada." vem do `/me` relido).
10. Troque `aberto={dialogo === 'desativar' && totp === true}` por `aberto={dialogo === 'desativar' && (totp === true || true)}`. Expected: FAIL em `com o 2FA desligado em outra aba, "Desativar" não abre o formulário`.
11. No início do envio da troca de senha, troque `setErro(null)` por `setErro((e) => (e?.renovado ? e : null))`. Expected: FAIL em `com o acesso vencido nesta tela, guarda o formulário e oferece renovar…` ("Acesso renovado. Envie de novo." sobra depois da troca).
12. Troque `dialogo === 'ativar' && (totp === false || travado)` por `dialogo === 'ativar' && (totp === false || travado || true)`. Expected: FAIL em `com o 2FA ligado em outra aba, "Ativar" não abre o formulário`.
13. Troque a mesma expressão por `dialogo === 'ativar' && totp === false && (travado || !travado)`. Expected: FAIL em `no passo dos códigos, Esc não fecha o diálogo` (o `/me` relido fecha o diálogo com os códigos).
14. Na API falsa (`test/api-falsa.ts`), troque `if (VIVAS.has(chave) && st.familiaRevogada)` por `if (VIVAS.has(chave) && st.familiaRevogada && Math.random() > 2)`. Expected: FAIL em `no diálogo do 2FA, acesso vencido com a sessão morta volta ao login` — o teste depende da guarda imitada, e não de um atalho.
15. Na API falsa, apague `st.totpPendente = false` do `/auth/password`. Expected: FAIL em `código não aceito na ativação sugere recomeçar, se a senha foi trocada`.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(painel): configurações com troca de senha, 2FA e sessões, e a sessão atual encerrada sem refresh"
```

---

### Task 11: E2E contra o binário real

O fluxo crítico da spec §14 (login → cadastrar alvo → ver status), mais o que só o navegador de verdade prova: F5 com o cookie, a CSP de produção em todas as telas (zero violações, com controle positivo), o chunk do Recharts desenhando, o token fora do storage, duas abas renovando juntas, e axe (WCAG 2 A e AA) nos temas claro e escuro.

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/e2e/init.sql`, `web/e2e/pilha.ts`, `web/e2e/global-setup.ts`, `web/e2e/global-teardown.ts`, `web/e2e/fixtures.ts`
- Test: `web/e2e/fluxo.spec.ts`, `web/e2e/abas.spec.ts`, `web/e2e/a11y.spec.ts`

**Interfaces:**
- Consumes: o binário (`go build ./cmd/vigilume`) com o painel embutido; `vigilume user create --email` com a senha pelo stdin (Plano 5); `docker` com `postgres:18-alpine`.
- Produces: `npm run e2e` em `web/`. Variáveis: sem nada, sobe a própria pilha; com `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD`, usa uma pilha já de pé (o compose do Plano 7, reconciliação item 4).

**Contexto para quem implementa.** O `webServer` do Playwright sobe **antes** do `globalSetup`, e o binário precisa do banco antes: por isso o `globalSetup` sobe tudo (D10); se algo falha depois do `docker run` (banco que não sobe, `/readyz` que não chega, `user create` recusado), `subir()` derruba container e processo antes de propagar — o `globalTeardown` não roda quando o `globalSetup` falha. O papel do banco é comum (`init.sql`): o binário recusa superusuário (`CheckRole`). O alvo `https://vigilume-e2e.invalid/` (RFC 2606) passa pela validação de cadastro (que não resolve DNS) e nunca resolve: o sondador o classifica sem rede externa, e o teste só confere que ele aparece com um estado, sem depender de qual (reconciliação, item 4). As violações de CSP vão para o processo do teste (`exposeFunction`), e sobrevivem a F5 e navegação; o controle positivo injeta um `<style>` sem nonce e exige exatamente uma. O projeto `a11y` responde a API com `page.route` (os cinco estados, um canal falhando) e visita **todas** as rotas nos dois temas: é o que dá cobertura de CSP a todas as telas. O fluxo (spec §7.4) entra **só com o teclado a partir do topo** (Tab até o e-mail), espera o estado classificado no próprio poll do dashboard (15 s) em vez de recarregar em laço (o burst do limite por usuário), abre o detalhe uma vez, **apaga o alvo** (o teto de 100 alvos, contra uma pilha externa que roda o E2E de novo), **encerra a própria sessão** em Configurações e confere que o F5 não volta, entra de novo e sai. A **passada de teclado** (spec §6) é um teste repetível: em cada rota, numa carga nova, o primeiro Tab é o "Pular para o conteúdo" e ativá-lo leva ao conteúdo; depois, Tab até o foco sair da página, com o foco visível em cada parada; a lista de paradas vai anexada ao relatório. O projeto `a11y` responde com as **mesmas fixtures tipadas** dos testes de tela (`src/test/fixtures.ts`), e visita também a janela de 7 dias, que vem por hora: as barras de uptime têm de desenhar. Várias abas (D11): o teste segura a **resposta** de cada `/auth/refresh` no navegador (`Fetch.enable` do CDP no estágio `Response`: a requisição já saiu com o cookie daquele instante, e o `Set-Cookie` ainda não foi aplicado) até as duas abas terem a sua, ou até 1,5 s. Sem a Web Lock, as duas saem com o mesmo cookie e o servidor revoga a família; com a trava, a segunda aba só envia depois do prazo, com o cookie novo. `page.route` não serve (o cookie é anexado no `continue`), e latência emulada também não (a corrida só aparecia às vezes). O prazo é desarmado a cada liberação: um prazo velho de uma rodada, disparando na seguinte, soltava a primeira resposta cedo e deixava a sabotagem verde 1 vez em 5 (achado na validação). Depois de as duas abas assentarem, o teste confere **no banco** da pilha própria que não há auditoria `session.reuse_detected` (spec §7.4; contra uma pilha externa, o teste não alcança o banco e pula só essa asserção).

- [ ] **Step 1: A configuração e a pilha**

Crie `web/playwright.config.ts`:

```ts
import { defineConfig, devices } from '@playwright/test'

// O E2E roda contra o binário real (spec do Plano 6 §7.4; reconciliação,
// item 4). O globalSetup sobe a pilha (Postgres em container, o binário com
// o painel embutido, vigilume user create) — ou, com E2E_BASE_URL,
// E2E_EMAIL e E2E_PASSWORD, usa uma pilha já de pé (o compose do Plano 7).
// O webServer do Playwright não serve: ele sobe ANTES do globalSetup, e o
// binário precisa do banco antes.
export default defineConfig({
  testDir: './e2e',
  // Uma pilha, um usuário: os testes dividem o limite por usuário da API
  // (60/min, burst 20) e o do login (10/min por IP). Em série.
  workers: 1,
  fullyParallel: false,
  // Sem retries: uma nova tentativa esconderia exatamente o teste que
  // depende de sorte (lições de teste do projeto).
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  reporter: [['list']],
  use: { trace: 'retain-on-failure', locale: 'pt-BR', timezoneId: 'America/Sao_Paulo' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
```

Crie `web/e2e/init.sql`:

```sql
-- O papel comum do Vigilume: dono do banco, sem superusuário nem BYPASSRLS.
-- O binário recusa subir com um papel que ignore a RLS (storage.CheckRole,
-- D8 do Plano 2). Roda uma vez, quando o container cria o banco.
CREATE ROLE vigilume LOGIN PASSWORD 'vigilume' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE vigilume OWNER vigilume;
```

Crie `web/e2e/pilha.ts`:

```ts
import { execFileSync, spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:net'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'

// A pilha do E2E: Postgres num container e o binário real, compilado com o
// painel embutido. Tudo em e2e/.tmp, que o .gitignore ignora.

export const RAIZ = path.resolve(import.meta.dirname, '../..')
const TMP = path.join(import.meta.dirname, '.tmp')
const ESTADO = path.join(TMP, 'pilha.json')

export const EMAIL = 'e2e@vigilume.test'
// A política de senha nova do Plano 5: de 15 a 128 code points.
export const SENHA = 'uma senha de teste bem comprida'

type Estado = { container: string; pid: number }

const chave = () => randomBytes(32).toString('base64')

function portaLivre(): Promise<number> {
  return new Promise((ok, falha) => {
    const s = createServer()
    s.listen(0, '127.0.0.1', () => {
      const endereco = s.address()
      s.close(() => (typeof endereco === 'object' && endereco ? ok(endereco.port) : falha(new Error('sem porta'))))
    })
  })
}

async function esperar(o: { oque: string; ms: number; tentar: () => Promise<boolean> | boolean }) {
  const fim = Date.now() + o.ms
  while (Date.now() < fim) {
    if (await o.tentar()) return
    await new Promise((r) => setTimeout(r, 250))
  }
  throw new Error(`E2E: ${o.oque} não ficou pronto em ${o.ms} ms`)
}

export async function subir(): Promise<{ baseURL: string }> {
  mkdirSync(TMP, { recursive: true })
  const dist = path.join(RAIZ, 'internal/webui/dist/index.html')
  if (!existsSync(dist)) throw new Error('E2E: rode "npm run build" antes: o binário embute internal/webui/dist')

  const binario = path.join(TMP, 'vigilume')
  execFileSync('go', ['build', '-o', binario, './cmd/vigilume'], {
    cwd: RAIZ,
    stdio: 'inherit',
    env: { ...process.env, GOFLAGS: '-buildvcs=false' },
  })

  const container = execFileSync(
    'docker',
    [
      'run', '-d', '--rm', '-P',
      '-e', 'POSTGRES_PASSWORD=postgres',
      '-v', `${path.join(import.meta.dirname, 'init.sql')}:/docker-entrypoint-initdb.d/init.sql:ro`,
      'postgres:18-alpine',
    ],
    { encoding: 'utf8' },
  ).trim()
  const estado: Estado = { container, pid: 0 }
  writeFileSync(ESTADO, JSON.stringify(estado))
  // Daqui em diante há o que derrubar: qualquer falha antes de a pilha ficar
  // pronta (banco que não sobe, binário que não passa no /readyz, user
  // create recusado) derruba container e processo antes de propagar. O
  // globalTeardown não roda quando o globalSetup falha.
  try {
    return await terminarDeSubir(container, binario, estado)
  } catch (e) {
    derrubar()
    throw e
  }
}

async function terminarDeSubir(container: string, binario: string, estado: Estado): Promise<{ baseURL: string }> {
  // O entrypoint sobe o Postgres duas vezes (uma para o init.sql); a porta
  // só vale depois da segunda, e é o pg_isready pelo TCP que diz.
  await esperar({
    oque: 'o Postgres',
    ms: 60_000,
    tentar: () => {
      try {
        execFileSync('docker', ['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'vigilume', '-d', 'vigilume'], {
          stdio: 'ignore',
        })
        return true
      } catch {
        return false
      }
    },
  })
  const portaDoBanco = execFileSync('docker', ['port', container, '5432/tcp'], { encoding: 'utf8' })
    .split('\n')[0]!
    .split(':')
    .pop()!
    .trim()

  const porta = await portaLivre()
  const baseURL = `http://127.0.0.1:${porta}`
  const env = {
    ...process.env,
    DATABASE_URL: `postgres://vigilume:vigilume@127.0.0.1:${portaDoBanco}/vigilume?sslmode=disable`,
    HTTP_ADDR: `127.0.0.1:${porta}`,
    PUBLIC_BASE_URL: baseURL,
    CORS_ORIGIN: baseURL,
    MASTER_KEY: chave(),
    JWT_SECRET: chave(),
    IP_HASH_KEY: chave(),
    METRICS_ADDR: 'off',
    ROLE: 'all',
    LOG_LEVEL: 'warn',
  }
  const proc = spawn(binario, [], { env, stdio: ['ignore', 'inherit', 'inherit'], detached: true })
  estado.pid = proc.pid ?? 0
  writeFileSync(ESTADO, JSON.stringify(estado))
  proc.unref()

  await esperar({
    oque: 'o binário (/readyz)',
    ms: 60_000,
    tentar: async () => (await fetch(`${baseURL}/readyz`).catch(() => null))?.ok ?? false,
  })

  // O usuário pelo subcomando do contrato, com a senha pelo stdin (nunca em
  // argumento nem em variável de ambiente — D16 do Plano 5).
  execFileSync(binario, ['user', 'create', '--email', EMAIL], {
    env,
    input: `${SENHA}\n`,
    stdio: ['pipe', 'inherit', 'inherit'],
  })
  return { baseURL }
}

// Quantas linhas de auditoria com esta ação a pilha própria tem (o
// Postgres do container, pelo superusuário, que a RLS não filtra). null com
// pilha externa (E2E_BASE_URL): lá o teste não alcança o banco.
export function contarAuditoria(acao: string): number | null {
  if (!process.env.E2E_PILHA_PROPRIA || !existsSync(ESTADO)) return null
  if (!/^[a-z_.]+$/.test(acao)) throw new Error(`E2E: ação inválida: ${acao}`)
  const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
  const saida = execFileSync(
    'docker',
    ['exec', e.container, 'psql', '-U', 'postgres', '-d', 'vigilume', '-tAc',
      `SELECT count(*) FROM audit_log WHERE action = '${acao}'`],
    { encoding: 'utf8' },
  )
  return Number(saida.trim())
}

export function derrubar() {
  if (!existsSync(ESTADO)) return
  const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
  if (e.pid) {
    try {
      process.kill(e.pid, 'SIGTERM')
    } catch {
      // já tinha saído
    }
  }
  if (e.container) execFileSync('docker', ['rm', '-f', e.container], { stdio: 'ignore' })
}
```

Crie `web/e2e/global-setup.ts`:

```ts
import { EMAIL, SENHA, subir } from './pilha'

export default async function globalSetup() {
  if (process.env.E2E_BASE_URL) {
    // Pilha externa (o compose do Plano 7): o usuário já existe.
    if (!process.env.E2E_EMAIL || !process.env.E2E_PASSWORD) {
      throw new Error('E2E: com E2E_BASE_URL, informe também E2E_EMAIL e E2E_PASSWORD')
    }
    return
  }
  const { baseURL } = await subir()
  // As variáveis do globalSetup chegam aos workers.
  process.env.E2E_BASE_URL = baseURL
  process.env.E2E_EMAIL = EMAIL
  process.env.E2E_PASSWORD = SENHA
  process.env.E2E_PILHA_PROPRIA = '1'
}
```

Crie `web/e2e/global-teardown.ts`:

```ts
import { derrubar } from './pilha'

export default function globalTeardown() {
  if (process.env.E2E_PILHA_PROPRIA) derrubar()
}
```

Crie `web/e2e/fixtures.ts`:

```ts
import { test as base, expect, type Page } from '@playwright/test'

// O listener de CSP é do TESTE (addInitScript), não do painel: o app de
// produção não carrega nada disso. As violações vão para o processo do teste
// (exposeFunction), e por isso sobrevivem a F5 e navegação: um array na
// própria página zeraria a cada carga e esconderia a violação da tela
// anterior.
const registros = new WeakMap<Page, string[]>()

export function violacoes(page: Page): string[] {
  return registros.get(page) ?? []
}

async function registrarCsp(page: Page) {
  const lista: string[] = []
  registros.set(page, lista)
  await page.exposeFunction('vigilumeViolacao', (v: string) => {
    lista.push(v)
  })
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const w = window as unknown as { vigilumeViolacao: (v: string) => void }
      w.vigilumeViolacao(`${e.violatedDirective} ${e.blockedURI} ${e.sample}`)
    })
  })
}

// Controle positivo: um <style> sem nonce TEM de virar violação. Sem isto,
// "zero violações" seria verde também com o listener quebrado. Devolve a
// lista sem a violação provocada.
export async function controlePositivo(page: Page) {
  const antes = violacoes(page).length
  await page.evaluate(() => {
    const s = document.createElement('style')
    s.textContent = '.controle-positivo{color:red}'
    document.head.append(s)
  })
  await expect.poll(() => violacoes(page).length).toBe(antes + 1)
  violacoes(page).pop()
}

export const test = base.extend<{ credenciais: { email: string; senha: string } }>({
  // O Playwright exige desestruturação no primeiro parâmetro de um fixture.
  // oxlint-disable-next-line no-empty-pattern
  baseURL: async ({}, use) => {
    await use(process.env.E2E_BASE_URL)
  },
  // oxlint-disable-next-line no-empty-pattern
  credenciais: async ({}, use) => {
    await use({ email: process.env.E2E_EMAIL ?? '', senha: process.env.E2E_PASSWORD ?? '' })
  },
  page: async ({ page }, use) => {
    await registrarCsp(page)
    await use(page)
  },
})

export { expect }
```

- [ ] **Step 2: Os testes**

Crie `web/e2e/fluxo.spec.ts`:

```ts
import { controlePositivo, expect, test, violacoes } from './fixtures'

// O fluxo crítico da spec §14: login → cadastrar alvo → ver status. Mais o
// que só o navegador de verdade prova: F5 com o cookie, a CSP de produção, o
// chunk do Recharts desenhando a linha, e o token fora do storage.
test('login, cadastro de alvo, status, F5, detalhe, apagar, encerrar a sessão e sair', async ({ page, credenciais }) => {
  const nomeDoAlvo = `E2E ${Date.now()}`

  // 1. Sem sessão: o splash e depois o login; nunca o painel piscando.
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  await controlePositivo(page)

  // 2. Login só com o teclado, do topo da página: Tab até o e-mail, Tab até
  //    a senha, Enter.
  await page.keyboard.press('Tab')
  await expect(page.getByLabel('E-mail')).toBeFocused()
  await page.keyboard.type(credenciais.email)
  await page.keyboard.press('Tab')
  await expect(page.getByLabel('Senha')).toBeFocused()
  await page.keyboard.type(credenciais.senha)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

  // 3. Cadastrar. O alvo .invalid (RFC 2606) passa pela validação de
  //    cadastro, que não resolve DNS, e nunca resolve: sem rede externa.
  await page.getByRole('link', { name: 'Cadastrar alvo' }).click()
  await page.getByLabel('Nome').fill(nomeDoAlvo)
  await page.getByLabel('URL').fill('https://vigilume-e2e.invalid/')
  await page.getByRole('button', { name: 'Cadastrar' }).click()
  await expect(page.getByRole('heading', { name: nomeDoAlvo })).toBeVisible()

  // 4. O dashboard mostra o alvo (reconciliação, item 4: sem depender do
  //    estado, que um DNS que sequestra NXDOMAIN poderia mudar). A primeira
  //    sonda sai em ~1 s; o próprio poll do dashboard (15 s) traz o estado
  //    classificado, sem recarregar a página (recarregar em laço gastaria o
  //    burst do limite por usuário).
  await page.getByRole('link', { name: 'Painel' }).click()
  const cartao = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: nomeDoAlvo }) })
  await expect(cartao).toBeVisible()
  await expect(cartao.getByText(/^(No ar|Degradado|Fora do ar)$/)).toBeVisible({ timeout: 30_000 })

  // 5. F5: continua dentro, pelo cookie de refresh.
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

  // 6. O detalhe carrega o chunk do Recharts. O alvo .invalid só tem checks
  //    "fora do ar", que viram marcas na base do gráfico; a marca tem cor
  //    computada (variável CSS do index.css). O dashboard já mostrou um
  //    check: a série tem pontos na primeira carga.
  await page.getByRole('link', { name: nomeDoAlvo }).click()
  await expect(page.getByText(/no ar nesta janela/)).toBeVisible()
  const marca = page.getByTestId('grafico-latencia').locator('.recharts-symbols').first()
  await expect(marca).toBeAttached()
  expect(await marca.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)

  // 7. Um diálogo (Radix + react-remove-scroll, que injeta <style>) trava a
  //    rolagem: o nonce funcionou. E o alvo sai: o teto é de 100 alvos, e
  //    o E2E roda de novo contra a mesma pilha externa (reconciliação, item 4).
  await page.getByRole('button', { name: 'Apagar' }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).overflow)).toBe('hidden')
  await page.getByRole('button', { name: 'Apagar alvo' }).click()
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
  await expect(page.getByRole('link', { name: nomeDoAlvo })).toHaveCount(0)

  // 8. Nada do token no storage; o cookie de refresh é invisível ao JS. (Duas
  //    guardas: o HttpOnly é do servidor — este teste prova a propriedade.)
  const armazenado = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))
  expect(armazenado).not.toMatch(/eyJ/)
  expect(await page.evaluate(() => document.cookie)).not.toContain('vigilume_refresh')

  // 9. Zero violações de CSP no caminho todo, inclusive antes dos F5.
  expect(violacoes(page)).toEqual([])

  // 10. Encerrar a própria sessão em Configurações: o 204 apagou o cookie, e
  //     o F5 não volta (nota do Plano 5 §12).
  await page.getByRole('link', { name: 'Configurações' }).click()
  const atual = page.getByRole('listitem').filter({ hasText: 'Esta sessão' })
  await atual.getByRole('button', { name: 'Encerrar' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Encerrar' }).click()
  await expect(page.getByText('Você encerrou esta sessão.')).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()

  // 11. Entrar de novo (o ?next= volta a Configurações), sair; F5 continua fora.
  await page.getByLabel('E-mail').fill(credenciais.email)
  await page.getByLabel('Senha').fill(credenciais.senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
  await page.getByRole('button', { name: 'Sair' }).click()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  expect(violacoes(page)).toEqual([])
})
```

Crie `web/e2e/abas.spec.ts`:

```ts
import type { CDPSession, Page } from '@playwright/test'
import { expect, test } from './fixtures'
import { contarAuditoria } from './pilha'

// Duas abas do MESMO contexto (dividem cookies, não memória) recarregando
// juntas: o boot de cada uma é um refresh. Para a corrida não depender de
// sorte, o teste segura a RESPOSTA de cada /auth/refresh no navegador
// (CDP, Fetch no estágio de resposta: a requisição já saiu com o cookie
// daquele instante, e o Set-Cookie ainda não foi aplicado) até as duas
// abas terem a sua em mãos — ou até 1,5 s. Sem a Web Lock de sessao.ts, as
// duas saem com o mesmo cookie, a segunda é reúso, e o servidor revoga a
// família: uma aba cai no login. Com a trava, a segunda aba nem envia antes
// de a primeira terminar; a espera vence pelo prazo, e as duas continuam
// dentro. Latência via emulação de rede não bastou: com o servidor rápido,
// a corrida só aparecia às vezes (achado na validação do Plano 6).
async function segurarRefreshes(sessoes: CDPSession[]) {
  let pausadas: { cdp: CDPSession; requestId: string }[] = []
  let prazo: ReturnType<typeof setTimeout> | null = null
  // liberar desarma o prazo: um prazo velho de uma rodada, disparando no meio
  // da seguinte, soltaria a primeira resposta antes de a outra aba enviar a
  // sua, e a sabotagem passaria às vezes (achado na validação do Plano 6).
  const liberar = () => {
    if (prazo) clearTimeout(prazo)
    prazo = null
    const lote = pausadas
    pausadas = []
    for (const p of lote) void p.cdp.send('Fetch.continueResponse', { requestId: p.requestId })
  }
  for (const cdp of sessoes) {
    await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*/api/v1/auth/refresh', requestStage: 'Response' }] })
    cdp.on('Fetch.requestPaused', (e) => {
      pausadas.push({ cdp, requestId: e.requestId })
      if (pausadas.length >= sessoes.length) {
        liberar()
      } else if (!prazo) {
        prazo = setTimeout(liberar, 1_500)
      }
    })
  }
}

async function entrar(page: Page, email: string, senha: string) {
  await page.goto('/login')
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha').fill(senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
}

test('duas abas recarregando juntas continuam dentro', async ({ page, context, credenciais }) => {
  await entrar(page, credenciais.email, credenciais.senha)
  const outra = await context.newPage()
  await outra.goto('/')
  await expect(outra.getByRole('heading', { name: 'Painel' })).toBeVisible()

  await segurarRefreshes([await context.newCDPSession(page), await context.newCDPSession(outra)])
  const assentou = /^(Painel|Entrar no Vigilume)$/
  for (let rodada = 0; rodada < 2; rodada++) {
    await Promise.all([page.reload(), outra.reload()])
    // As duas abas terminam o boot (dentro ou no login) antes de olhar o
    // banco: o servidor não viu reúso nenhum (spec do Plano 6 §7.4). Só com a
    // pilha própria; contra uma pilha externa o teste não alcança o banco.
    await expect(page.getByRole('heading', { name: assentou })).toBeVisible()
    await expect(outra.getByRole('heading', { name: assentou })).toBeVisible()
    const reusos = contarAuditoria('session.reuse_detected')
    if (reusos !== null) expect(reusos, 'auditorias session.reuse_detected').toBe(0)
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
    await expect(outra.getByRole('heading', { name: 'Painel' })).toBeVisible()
  }
})
```

Crie `web/e2e/a11y.spec.ts`:

```ts
import AxeBuilder from '@axe-core/playwright'
import type { Page, Route } from '@playwright/test'
import type { z } from 'zod'
import type { Serie } from '../src/lib/api/schemas'
import { alvo, canal, incidente, itemDoResumo, me, pagina, sessao } from '../src/test/fixtures'
import { controlePositivo, expect, test, violacoes } from './fixtures'

// Todas as rotas do painel, com a API respondida pelo próprio teste
// (page.route): os cinco estados do dashboard, um canal falhando, sessões.
// Para cada rota, nos dois temas: axe (WCAG 2 A e AA, contraste incluído)
// sem violações, e zero violações de CSP. É o que dá cobertura de CSP a
// TODAS as telas, não só às do fluxo crítico. O axe roda sob a CSP de
// produção, sem bypassCSP (spike do Plano 6, §8.4).

// As respostas vêm das MESMAS fixtures tipadas dos testes de tela
// (src/test/fixtures.ts, z.input dos schemas): uma resposta fora do contrato
// não compila aqui também (spec do Plano 6 §7.4).
const agora = new Date().toISOString()
const hora = new Date(Math.floor(Date.now() / 3_600_000) * 3_600_000 - 3_600_000).toISOString()
const spark = [{ bucket: hora, total: 60, ok: 60, p50_ms: 110 }]
const item = (id: string, name: string, state: 'up' | 'degraded' | 'down' | 'unknown', enabled = true) =>
  itemDoResumo({
    id,
    name,
    enabled,
    state,
    last_checked_at: agora,
    last_latency_ms: state === 'down' ? null : 120,
    uptime_24h: 0.9993,
    open_incident_id: state === 'down' ? 'inc-1' : null,
    spark,
  })
const a1 = alvo({ id: 'a1', header_names: ['Authorization'], created_at: agora })
const bruta: z.input<typeof Serie> = {
  resolution: 'raw',
  items: [
    { checked_at: agora, status: 'up', latency_ms: 120, status_code: 200, error_kind: null },
    { checked_at: agora, status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
  ],
}
// A janela de 7 dias vem por hora (o corte de 48 h é do servidor): as barras
// de uptime da hora aparecem (spec do Plano 6 §4.4).
const horaria: z.input<typeof Serie> = {
  resolution: '1h',
  items: [
    { bucket: hora, total: 60, ok: 57, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
    { bucket: agora, total: 30, ok: 30, p50_ms: 100, p95_ms: 200, p99_ms: 250, max_ms: 300 },
  ],
}
const respostas: Record<string, unknown> = {
  'POST /auth/refresh': { access_token: 'token-de-teste', expires_in: 900 },
  'GET /me': me({ created_at: agora }),
  'GET /targets/summary': {
    items: [
      item('a1', 'API', 'up'), item('a2', 'Site', 'degraded'), item('a3', 'Banco', 'down'),
      item('a4', 'Novo', 'unknown'), item('a5', 'Parado', 'unknown', false),
    ],
  },
  'GET /targets': { items: [a1] },
  'GET /targets/a1': a1,
  'GET /incidents': {
    items: [incidente({ id: 'inc-1', target_id: 'a3', target_name: 'Banco', started_at: agora, cause: 'conn_refused' })],
  },
  'GET /channels': {
    items: [
      canal({ id: 'c1', created_at: agora }),
      canal({ id: 'c2', hint: 'discord.com/…/zzzz', status: 'failing', created_at: agora, last_error: 'invalid_channel', last_failure_at: agora }),
      canal({ id: 'c3', kind: 'email', hint: 'd***@exemplo.com', status: 'pending', created_at: agora }),
    ],
  },
  'GET /status-pages': { items: [pagina({ id: 'p1', items: [{ target_id: 'a1', display_name: 'API' }], created_at: agora })] },
  'GET /sessions': { items: [sessao({ id: 'f1', created_at: agora, last_used_at: agora })] },
}

function respostaDe(metodo: string, u: URL): unknown {
  const chave = `${metodo} ${u.pathname.replace('/api/v1', '')}`
  if (chave === 'GET /targets/a1/checks') {
    const desde = Date.parse(u.searchParams.get('from') ?? '')
    return Date.now() - desde > 48 * 3_600_000 ? horaria : bruta
  }
  return respostas[chave]
}

async function imitarApi(page: Page) {
  await page.route('**/api/v1/**', (route: Route) => {
    const corpo = respostaDe(route.request().method(), new URL(route.request().url()))
    return corpo === undefined
      ? route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '' }, trace_id: 't' } })
      : route.fulfill({ status: 200, json: corpo })
  })
}

const ROTAS: [string, string][] = [
  ['/', 'Painel'],
  ['/targets/new', 'Novo alvo'],
  ['/targets/a1', 'API'],
  ['/targets/a1?janela=7d', 'API'],
  ['/targets/a1/edit', 'Editar alvo'],
  ['/incidents', 'Incidentes'],
  ['/channels', 'Canais de notificação'],
  ['/status-pages', 'Status page'],
  ['/settings', 'Configurações'],
]

for (const tema of ['claro', 'escuro'] as const) {
  test(`acessibilidade e CSP em todas as rotas, tema ${tema}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    // A tela de login também: sem sessão, antes de imitar o refresh.
    for (const [rota, titulo] of ROTAS) {
      await page.goto(rota)
      await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
      await expect(page.locator('html')).toHaveClass(tema === 'escuro' ? /dark/ : /^(?!.*dark)/)
      if (rota.endsWith('janela=7d')) {
        // As barras de uptime da hora desenham, com cor (variável CSS).
        const barra = page.getByTestId('grafico-latencia').locator('.recharts-bar-rectangle path').first()
        await expect(barra).toBeAttached()
        expect(await barra.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)
      }
      const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(r.violations.map((v) => `${rota}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      expect(violacoes(page), rota).toEqual([])
    }
    // Um diálogo aberto também passa pelo axe e pela CSP.
    await page.goto('/channels')
    await page.getByRole('button', { name: 'Novo canal' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(r.violations.map((v) => v.id)).toEqual([])
    expect(violacoes(page)).toEqual([])
    await controlePositivo(page)
  })
}

// A passada de teclado da spec §6, repetível: em cada rota, numa carga
// nova, o primeiro Tab é o "Pular para o conteúdo" (§4.9), e ativá-lo leva o
// Tab seguinte para dentro do conteúdo; depois, Tab por todas as paradas até
// o foco sair da página — nenhuma armadilha — com o foco visível (outline do
// :focus-visible) em cada uma. As paradas vão para o relatório do teste.
test('passada de teclado em todas as rotas', async ({ page }, info) => {
  await imitarApi(page)
  const relatorio: string[] = []
  for (const [rota, titulo] of ROTAS) {
    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    await page.keyboard.press('Tab')
    await expect(page.getByRole('link', { name: 'Pular para o conteúdo' }), rota).toBeFocused()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('#conteudo')), `${rota}: depois do salto`).toBe(true)

    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    const paradas: string[] = []
    for (let i = 0; ; i++) {
      expect(i, `${rota}: o foco não sai da página em 150 Tabs (armadilha?)`).toBeLessThan(150)
      await page.keyboard.press('Tab')
      const p = await page.evaluate(() => {
        const e = document.activeElement as HTMLElement | null
        if (!e || e === document.body) return null
        const cs = getComputedStyle(e)
        const rotulo = (e as HTMLInputElement).labels?.[0]?.textContent
        const nome = (e.getAttribute('aria-label') ?? rotulo ?? e.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 30)
        return { nome: `${e.tagName.toLowerCase()} "${nome}"`, visivel: cs.outlineStyle !== 'none' && cs.outlineWidth !== '0px' }
      })
      if (!p) break
      expect(p.visivel, `${rota}: foco invisível em ${p.nome}`).toBe(true)
      paradas.push(p.nome)
    }
    relatorio.push(`${rota}: ${paradas.length} paradas — ${paradas.join(' | ')}`)
  }
  await info.attach('passada-de-teclado.txt', { body: relatorio.join('\n'), contentType: 'text/plain' })
})

test('a tela de login, sem sessão', async ({ page }) => {
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  expect(r.violations.map((v) => v.id)).toEqual([])
  expect(violacoes(page)).toEqual([])
})
```

- [ ] **Step 3: Rodar**

Run: `cd web && npx playwright install chromium && npm run build && npm run e2e`
Expected: PASS nos seis testes (fluxo, abas, a11y claro, a11y escuro, passada de teclado, login). Depois, `docker ps` não mostra container do `postgres:18-alpine` sobrando (o `globalTeardown` o derruba).

Run: `cd web && npm run typecheck && npm run lint`
Expected: PASS (o `tsconfig.node.json` cobre `e2e/` e `playwright.config.ts`).

Rode `npm run e2e` três vezes: o teste de abas depende de tempo. Com a pilha própria, o `vigilume user create` recebe a senha por pipe; o aviso que ele escreve no stderr quando o stdin não é terminal (Task 11 do Plano 5) aparece no log e não quebra nada.

- [ ] **Step 4: Sabotagem**

Cada uma pede `npm run build` antes de `npm run e2e` (o binário embute o build); restaure e reconstrua depois:
1. Em `lib/nonce.ts`, troque `if (nonce) setNonce(nonce)` por `if (nonce && Math.random() > 2) setNonce(nonce)`. Expected: FAIL em `acessibilidade e CSP em todas as rotas` (claro e escuro: `style-src-elem inline`, do diálogo) e no passo 7 do fluxo (a rolagem não trava). (Apagar a linha não compila — o import sobra — e o build velho continuaria no binário: verde falso.)
2. Em `lib/api/schemas.ts`, apague `z.config({ jitless: true })`. Expected: FAIL em todos: uma violação de `script-src` a cada carga (D1).
3. Em `lib/api/index.ts`, troque `travar: travaDoNavegador()` por `travar: (_nome, fn) => fn()` (e tire o import que sobra). Expected: FAIL em `duas abas recarregando juntas continuam dentro` com "auditorias session.reuse_detected — Expected: 0, Received: 1" (o servidor viu o reúso e revogou a família), **toda** vez — na validação, 10 de 10 antes da asserção do banco e 5 de 5 com ela; com a trava, 5 de 5 verdes, e a suíte inteira 3 de 3.
4. Em `index.css`, troque `--estado-degradado: #a16207;` (o do tema claro) por `#eab308`. Expected: FAIL em `acessibilidade e CSP em todas as rotas, tema claro` (`color-contrast`).
5. Em `fixtures.ts`, dentro de `controlePositivo`, troque `document.head.append(s)` por `void s`. Expected: FAIL em todos os testes que o chamam (o `expect.poll` não vê a violação): o listener é verificado, e não suposto.
6. Token no storage (spec §7.5): em `lib/api/sessao.ts`, na primeira linha de `aceitar`, acrescente `sessionStorage.setItem('vigilume.token', novo)`. Expected: FAIL no passo 8 do fluxo (`Expected pattern: not /eyJ/`, com o JWT no `sessionStorage`).
7. Em `features/alvos/GraficoDeLatencia.tsx`, troque `dataKey="uptime"` por `dataKey="semUptime"`. Expected: FAIL em `acessibilidade e CSP em todas as rotas` na rota `/targets/a1?janela=7d` (a barra não está no DOM).
8. Faça a sabotagem 9 da Task 3 (o título focado na carga). Expected: FAIL em `passada de teclado em todas as rotas` (o "Pular para o conteúdo" não é o primeiro Tab).
9. Em `index.css`, troque `outline: 3px solid var(--foco);` (em `:focus-visible`) por `outline: none;`. Expected: FAIL em `passada de teclado em todas as rotas` ("foco invisível em a \"Pular para o conteúdo\"").

- [ ] **Step 5: Commit**

```bash
git add web/playwright.config.ts web/e2e
git commit -m "test(painel): E2E do fluxo crítico, de várias abas e de acessibilidade e CSP contra o binário real"
```

---

## Depois da Task 11

- **Para o Plano 7:** `npm ci && npm run build` (em `web/`) **antes** do `go build` — sem isso o binário sai com o painel respondendo 503; o Dockerfile precisa de um estágio Node ≥ 22.22. No CI: `npm run typecheck`, `npm run lint`, `npm test`, `npm run build`, `git status --porcelain internal/webui/dist` vazio, `go test ./internal/webui/` depois do build (o teste de MIME confere o dist real), e `npm run e2e` (precisa de Docker e de `npx playwright install --with-deps chromium`). O `.dockerignore` espelha a regra do `internal/webui/dist` (reconciliação, item 8). Com o compose de pé, `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD` rodam o mesmo E2E contra ele.
- **Fora deste plano, registrado:** Trusted Types (`require-trusted-types-for 'script'`) quebra o Radix (`innerHTML`); gzip das respostas da **API** (o summary tem ~180 KB a cada 15 s com 100 alvos) é decisão de proxy do Plano 7; Firefox no projeto `a11y` (corte 8 da spec).
