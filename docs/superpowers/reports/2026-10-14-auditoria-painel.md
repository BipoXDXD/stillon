> Auditoria feita em 2026-10-14, antes dos PRs #74, #80 e #86, que já corrigiram o contraste das bordas, o sinal não cromático e os alvos de toque. As três direções visuais esperam a escolha do dono. As capturas de tela e os scripts citados ficaram fora do repositório.

# Auditoria visual do painel StillOn e três direções para o redesign

Data: 2026-10-14. Base: `main` em `8ca962d`. Referência: Refactoring UI (RUI), `~/.claude/knowledge/ui-design.md`
e regras "Design de interface" do `~/.claude/CLAUDE.md` (citadas como **DI n**).

## 0. Como o painel foi visto

- Rodei o painel de verdade: `npx vite` em `web/` (porta 5173) com a API respondida pelo próprio navegador
  (`page.route('**/api/v1/**')`), no mesmo formato das fixtures de `web/src/test/fixtures.ts` e do
  `web/e2e/a11y.spec.ts`. Nenhum segredo, nenhum backend. Dados: 7 alvos (no ar, degradado, fora, sem dados,
  pausado), 3 incidentes, 4 canais, uma status page, uma sessão.
- A status page **pública** não é React: é o template Go `internal/httpapi/templates/status.html`. Renderizei-o
  com `html/template` e dados de exemplo (programa em `scratchpad/statusgo/`).
- Screenshots (fullPage) em `scratchpad/painel-atual/`, nome `<tela>-<largura>-<tema>.png`, 400 e 1280 px,
  claro e escuro (56 arquivos): `painel`, `detalhe`, `incidentes`, `canais`, `status-page`, `configuracoes`,
  `novo-alvo`, `login`, estados `painel-vazio`, `painel-carregando`, `painel-erro`, `incidentes-vazio`,
  `canais-vazio`, e `publica` (página pública).
- Artefato do fullPage: a lateral é `position: fixed`, então nas capturas de 1280 ela "termina" em 900 px.
  Na tela real ela ocupa a altura toda. Não é defeito.
- Contraste: calculado pela fórmula WCAG sobre os tokens de `web/src/index.css` (script `scratchpad/contraste.mjs`).
  Alturas de controles medidas no DOM a 400 px (`scratchpad/medidas.json`).

## 1. Diagnóstico geral (por que "parece ruim")

O painel é **correto** (axe limpo nos dois temas, estados de carregar/erro/vazio existem, foco visível,
ícone + texto nos estados). O problema é de **hierarquia e sistema**, não de acessibilidade básica:

1. **Tudo tem o mesmo peso.** Um alvo fora do ar é um cartão idêntico a um no ar, só com o selo vermelho no
   canto; os cinco números do resumo têm o mesmo tamanho e cor, então "1 fora do ar" pesa igual a "3 no ar".
   Num monitor de uptime a pergunta é "tem algo quebrado?", e a tela não responde de relance (RUI cap. 2, DI 3).
2. **Densidade errada para o conteúdo.** Cada alvo vira um cartão de ~230 px de altura com três pares
   `label: valor`. Com 7 alvos, 1280 px já rola duas telas; no celular, cinco telas. Os labels "Latência /
   No ar em 24 h / Verificado" se repetem em todo cartão (RUI cap. 2 "Labels are a last resort").
3. **Identidade "vintage" sem convicção.** Creme + oliva + Helvetica + títulos 700 com tracking negativo:
   lê como template neutro levemente amarelado, não como escolha. O oliva também colide semanticamente com o
   verde de "No ar" (primária `#45583c` e `--estado-no-ar` `#3d633b` são quase o mesmo tom), então a marca
   "parece estado" e o estado "parece marca".
4. **Sem sistema fechado.** Espaçamentos fora da escala, quatro pesos de fonte, três raios, glifos Unicode
   misturados com ícones Lucide, checkbox azul do navegador.
5. **Duas identidades.** A página pública (o que terceiros veem) é branco puro, `system-ui`, verde/amarelo/
   vermelho saturados: não conversa com o painel.

## 2. Problemas por tela

Formato: problema — regra — local.

### 2.1 Sistema (tokens e componentes globais)

| # | Problema | Regra | Local |
|---|---|---|---|
| S1 | Borda de input/cartão `--borda` tem **1,42:1** sobre a superfície (claro) e 1,66:1 (escuro). O limite do campo de entrada fica abaixo de 3:1 (WCAG 1.4.11). Axe não pega isso. | DI 8 | `web/src/index.css:15`, `:36`; usado em `components/ui/campo.tsx:5` |
| S2 | Primária (`#45583c`) e "no ar" (`#3d633b`) quase idênticas; no escuro `#c5d2ac` x `#b4d49a`. Marca e estado se confundem. | DI 7 (primária separada dos acentos) | `index.css:17,22,38,43` |
| S3 | Superfície x fundo: **1,09:1** (claro) e 1,11:1 (escuro). Os cartões só se separam pela borda fraca (S1): tudo "achata". | RUI cap. 6 (profundidade), DI 9 | `index.css:11-12,32-33` |
| S4 | Tons escolhidos à mão um a um em hex, sem escala 100–900; não há tons de fundo para alertas/badges (o aviso de canais usa `bg-painel`, neutro, para uma mensagem de problema). | DI 7 | `index.css:10-50` |
| S5 | Quatro pesos: 400, 500 (`font-medium`, 13 usos), 600 (`font-semibold`, 16), 700 (`font-bold`, 8, mais `h1,h2,h3 {font-weight:700}`). | DI 3 (400 e 600) | `index.css:127-130`; ex. `PaginaPainel.tsx:38,43,109,156` |
| S6 | Espaçamento fora da escala 4/8/12/16/24/32/48/64: `px-5`, `lg:px-10`, `xl:px-14`, `lg:py-9`, `lg:gap-10`, `py-5`, `space-y-5`, `space-y-7`, `p-7`, `sm:p-10`, `pl-7`, `gap-1.5`, `py-2.5`, padding de seção `1.25rem` (20 px). | DI 2 | `Layout.tsx:72,127`; `PaginaPainel.tsx:36,90,105,107`; `PaginaLogin.tsx:133`; `index.css:110,123`; `campo.tsx:5,26`; `SeloDeEstado.tsx:30`; `dialogo.tsx:11` |
| S7 | Valores arbitrários: `text-[0.65rem]` (10,4 px, abaixo do mínimo de 12 da escala), `tracking-[0.16em]`, `max-w-[100rem]`. | DI 2, DI 6 | `Layout.tsx:75,127`; `PaginaLogin.tsx:134` |
| S8 | Três raios: `rounded-md` (12 usos), `rounded-lg` (8), `rounded-xl` (4, nas linhas de incidentes e canais). | DI 2 (um raio) | `PaginaIncidentes.tsx:41`, `PaginaCanais.tsx:222` x `PaginaPainel.tsx:36` |
| S9 | Iconografia dupla: estados de canal com glifos Unicode (`✓ … ⧖ ✕ ⏸`), estados de alvo com Lucide. | RUI cap. 8 (defaults caprichados) | `PaginaCanais.tsx:19-24,228` |
| S10 | Checkbox nativo azul do navegador, fora da paleta. | DI 12 | `PaginaStatusPage.tsx:147`, `FormularioAlvo.tsx:141`, `PaginaConfiguracoes.tsx:330` |
| S11 | Nenhuma sombra ou elevação; diálogo e cartão usam o mesmo tratamento de borda. | DI 9 | `index.css:106-124`, `dialogo.tsx` |
| S12 | Fonte: `'Helvetica Neue', Helvetica, Arial` sem fallback para Windows/Linux modernos (cai em Arial); sem cifras tabulares na família (o código pede `tabular-nums`, Arial obedece, mas Helvetica do macOS tem cifras proporcionais por padrão). | RUI cap. 4 (boas fontes) | `index.css:68` |
| S13 | Controles de "segmento" (janela do gráfico, filtro de incidentes) têm **30 px** de altura. Links de nome de alvo 25 px, "Configurar canais" 20 px, "Todos os incidentes" 16 px. Seletor de tema e Sair 40 px. | DI 11 (44 px) | `PaginaDetalhe.tsx:249`, `PaginaIncidentes.tsx:140`, `PaginaPainel.tsx:38,90`, `PaginaDetalhe.tsx:120`, `botao.tsx:16`, `Layout.tsx:30` |
| S14 | CSS estrutural por seletor de atributo (`main > section:not([aria-label='Resumo dos alvos'])…`): estilo depende do texto do aria-label; frágil para qualquer redesign. | RUI cap. 1 (sistema), qualidade | `index.css:106` |

### 2.2 Navegação / Layout (`web/src/app/Layout.tsx`)

| # | Problema | Regra | Local |
|---|---|---|---|
| L1 | No celular (400 px) a navegação, o seletor de tema, o e-mail e o Sair ocupam **~265 px** antes do título; não é fixa e não colapsa. Em toda tela o usuário rola antes de ver conteúdo. | DI 1 (mobile primeiro), RUI cap. 3 | `Layout.tsx:71-126` (screenshot `painel-400-claro.png`) |
| L2 | Subtítulo da marca a 10,4 px, "Monitor de disponibilidade", repetido em toda tela. | DI 6, S7 | `Layout.tsx:75` |
| L3 | Item ativo com fundo + anel + negrito: três sinais para uma coisa; o anel `ring-borda` some (1,4:1). Para destacar, bastaria rebaixar os inativos. | DI 3 (enfatize rebaixando) | `Layout.tsx:89` |
| L4 | Contador de incidentes: vermelho só no badge; o número de fora do ar não aparece na nav. Bom. Mas o contador conta incidentes abertos, incluindo reconhecidos: o badge nunca "acalma". (Observação de produto, não de visual.) | — | `Layout.tsx:43-49`, `consultas.ts:68` |
| L5 | Seletor de tema é um `<select>` com label "Tema" na lateral, ocupando o espaço nobre do rodapé; um botão de ícone (sol/lua) cabe em 44 px. | RUI cap. 2 (rebaixe o secundário) | `Layout.tsx:22-38` |

### 2.3 Painel / lista de alvos (`features/painel/PaginaPainel.tsx`)

| # | Problema | Regra | Local |
|---|---|---|---|
| P1 | **Sem ordenação por gravidade**: alvos na ordem da API. "Banco de relatórios" (fora) aparece em 4º. Num monitor, problemas primeiro. | RUI cap. 2 (hierarquia) | `PaginaPainel.tsx:160-164` |
| P2 | Cartão fora do ar idêntico ao no ar (mesma borda, mesmo fundo). O único sinal é o selo de 14 px no canto. | DI 3, RUI cap. 8 (accent border) | `PaginaPainel.tsx:36` |
| P3 | Resumo com 5 números de 36 px iguais, incluindo os zeros; "No ar 3" grita tanto quanto "Fora do ar 1". Zero deveria apagar; problema deveria acender. | DI 3 | `PaginaPainel.tsx:97-115` |
| P4 | Labels repetidos em cada cartão (`Latência`, `No ar em 24 h`, `Verificado`) em `dl` de duas colunas: o formato já diz ("142 ms", "99,99%", "há 1 min"). Em tabela, o label vira cabeçalho uma vez. | RUI cap. 2 "Labels are a last resort" | `PaginaPainel.tsx:43-50` |
| P5 | Minigráfico de 120x28 px com barras de 4 px: altura = mediana de latência e cor = uptime da hora. Duas variáveis num gráfico decorativo minúsculo; o verde e o vermelho das barras têm **1,09:1** entre si (claro) e são o par que a deuteranopia não separa. O componente é `aria-hidden`, mas para quem enxerga a cor é o único sinal da hora ruim. | DI 8 (cor nunca é o único sinal), RUI cap. 5 | `components/Minigrafico.tsx:17-20` |
| P6 | Rótulo "Últimas 24 h" + divisória para um gráfico pequeno: espaço ambíguo, o rodapé do cartão pesa mais que o dado. | RUI cap. 3 (espaço ambíguo) | `PaginaPainel.tsx:51-54` |
| P7 | Cartão de alvo sem dados mostra só travessões e uma área vazia de gráfico; o pausado igual. Estados que deveriam encolher ocupam o mesmo tamanho. | DI 12 | `PaginaPainel.tsx:34-62` |
| P8 | O cartão não é clicável inteiro: só o nome (alvo de 25 px de altura). | DI 11 | `PaginaPainel.tsx:38` |
| P9 | Aviso de canais usa fundo neutro `bg-painel` e ícone cinza para um problema real ("1 canal falhando"); não se distingue de uma dica. Faltam tons de aviso (amarelo 100/700). | DI 3, DI 7, DI 8 | `PaginaPainel.tsx:87-91` |
| P10 | Estado vazio: uma linha centralizada com link sublinhado; sem ilustração, a CTA é link (terciário) em vez de botão primário, e o botão "Cadastrar alvo" do cabeçalho compete com ela. | DI 12, DI 4 | `PaginaPainel.tsx:146-151` (`painel-vazio-*.png`) |
| P11 | Estado de erro: caixa com borda vermelha e texto preto, sem ícone; botão "Tentar de novo" secundário. Aceitável, mas o erro do painel inteiro aparece abaixo do aviso de canais, que fica com mais destaque. | DI 8, DI 3 | `components/EstadoDaConsulta.tsx:46-52` (`painel-erro-*.png`) |
| P12 | Carregando: três blocos de 64 px que não imitam o layout final (cartões em grade). Ao chegar os dados, a página pula. | DI 12 | `EstadoDaConsulta.tsx:11-24` (`painel-carregando-*.png`) |
| P13 | O carregamento com erro demora: o React Query tenta de novo (padrão 3 vezes com backoff) e o esqueleto fica vários segundos antes do erro aparecer (não cronometrei). Observação de produto. | — | `lib/api/consultas.ts` |

### 2.4 Detalhe do alvo (`features/alvos/PaginaDetalhe.tsx`, `GraficoDeLatencia.tsx`)

| # | Problema | Regra | Local |
|---|---|---|---|
| D1 | Não há números-chave no topo (uptime 24 h / 30 d, latência atual, último check, incidente aberto). A informação está numa frase corrida ("Mediana 124 ms, p95 160 ms, 98,33% no ar nesta janela."). | DI 3, RUI cap. 2 | `PaginaDetalhe.tsx:23-32,264` |
| D2 | Selo de estado embaixo da URL, pequeno; o estado atual do alvo deveria ser o elemento mais forte da tela depois do nome. | DI 3 | `PaginaDetalhe.tsx:221` |
| D3 | Três botões com o mesmo peso (Editar, Pausar, Apagar) e "Apagar" com borda vermelha permanente: a ação destrutiva é a mais chamativa da tela. | DI 4 | `PaginaDetalhe.tsx:223-233` |
| D4 | Seletor de janela: 6 botões soltos de 30 px, com `bg-painel` para o ativo (1,1:1 do fundo do cartão no claro). No celular quebra em duas linhas. | DI 11, DI 8 | `PaginaDetalhe.tsx:241-254` |
| D5 | Gráfico: linha de 1 px sem área, eixo Y que vai a 2400 ms por causa de um pico e esmaga a série normal; pontos "fora" em vermelho no eixo zero, pequenos. Ticks do eixo X a cada ~1h24 com horas quebradas (06:33, 07:57). | RUI cap. 5 (não só cor), legibilidade | `GraficoDeLatencia.tsx:42-97` |
| D6 | Tabela de dados escondida num `<details>` com `summary` de 20 px de alvo, sem estilo de tabela (sem zebra, sem alinhamento à direita dos números). | DI 6 (números à direita), DI 11 | `PaginaDetalhe.tsx:34-89,272-275` |
| D7 | "Incidentes recentes" em lista de texto corrido com travessão; sem estado visual (em andamento x resolvido). | DI 3, DI 8 | `PaginaDetalhe.tsx:106-115` |

### 2.5 Incidentes (`features/incidentes/PaginaIncidentes.tsx`)

| # | Problema | Regra | Local |
|---|---|---|---|
| I1 | Linha aberta e resolvida com o mesmo tratamento; nenhuma cor/ícone de "em andamento". Duração e início misturados numa linha com `·`. | DI 3, DI 8 | `PaginaIncidentes.tsx:41-57` |
| I2 | Filtro em botões de 30 px, colados (`gap-1`), sem aparência de controle segmentado. | DI 11 | `PaginaIncidentes.tsx:129-145` |
| I3 | Nome do alvo como link de 19 px de altura. | DI 11 | `PaginaIncidentes.tsx:43` |
| I4 | Vazio: só "Nenhum incidente aberto." em cinza. Esse é o estado **bom** e mais comum: merece um tratamento positivo (ícone de check, "Tudo no ar desde …"). | DI 12 | `PaginaIncidentes.tsx:153-157` |
| I5 | `rounded-xl` aqui e `rounded-lg` nos cartões do painel. | S8 | `PaginaIncidentes.tsx:41` |

### 2.6 Canais (`features/canais/PaginaCanais.tsx`)

| # | Problema | Regra | Local |
|---|---|---|---|
| C1 | "Apagar" com borda vermelha em **todas** as linhas: quatro chamarizes vermelhos na tela; a destrutiva deveria ser terciária/ícone até a confirmação. | DI 4 | `PaginaCanais.tsx:266-273` |
| C2 | Glifos Unicode como ícone (`✓`, `…`, `⧖`, `✕`, `⏸`), cada um com métrica diferente. | S9 | `PaginaCanais.tsx:19-24,228` |
| C3 | Canal pausado: o estado "Verificado" (verde) continua em destaque e "Pausado" vem abaixo em preto; a informação que importa (não recebe avisos) é a menos visível. | DI 3 | `PaginaCanais.tsx:227-228` |
| C4 | Botões pequenos de 40 px. | DI 11 | `PaginaCanais.tsx:238-273` |

### 2.7 Status page: configuração (`features/statuspage/PaginaStatusPage.tsx`) e página pública

| # | Problema | Regra | Local |
|---|---|---|---|
| SP1 | Checkbox azul nativo (S10). | DI 12 | `PaginaStatusPage.tsx:147` |
| SP2 | "Subir / Descer / Tirar" como botões fantasma em texto; o desabilitado é só cinza. "Tirar" (remoção) com o mesmo peso de reordenar. | DI 4 | `PaginaStatusPage.tsx:~170` (bloco de Subir/Descer/Tirar) |
| SP3 | "Prévia aproximada" com borda tracejada não parece a página pública (que tem barras de 90 dias). | RUI cap. 1 (seja pessimista), coerência | `PaginaStatusPage.tsx:221-240` |
| SP4 | **Página pública com identidade própria**: branco puro, `system-ui`, verdes/amarelos saturados. Não parece o mesmo produto. | RUI cap. 1 (personalidade) | `internal/httpapi/templates/status.html:2` |
| SP5 | Barras de 90 dias só por cor: amarelo `#e0a100` sobre branco **2,27:1**, cinza "sem dados" `#c9c9c9` **1,66:1**, verde x vermelho **1,37:1** entre si. Como objeto gráfico, abaixo de 3:1, e verde/vermelho para daltônicos. | DI 8 (WCAG 1.4.11, 1.4.1) | `status.html:2` (`ol li`, `.d li`, `.n li`) |
| SP6 | A CSP da página pública não tem `font-src` (`default-src 'none'`): fonte web exige mudar a CSP em `internal/httpapi/publicpage.go:92`. Restrição para qualquer direção que traga fonte própria. | — | `publicpage.go:92` |

### 2.8 Login e configurações

| # | Problema | Regra | Local |
|---|---|---|---|
| G1 | Login: sobrelinha em caixa alta com `tracking-[0.16em]` (o livro pede 0,05em), padding `p-7`/`sm:p-10`, `space-y-7`. Nada de marca (logo) na tela de entrada; só texto. | DI 2, DI 6 | `PaginaLogin.tsx:133-134` |
| G2 | Configurações: "Desativar" 2FA com borda vermelha ao lado de "Trocar autenticador"; "Encerrar" sessão também vermelho. | DI 4 | `PaginaConfiguracoes.tsx` (`configuracoes-1280-claro.png`) |
| G3 | Formulário de senha a 50% da largura dentro de um cartão de largura total: espaço vazio à direita sem propósito. Use `max-w` no cartão, não só no form. | DI 5 | `PaginaConfiguracoes.tsx` |

### 2.9 O que já está bom (manter em qualquer direção)

- Estado com ícone **e** texto e formas diferentes (`SeloDeEstado.tsx:9-15`); contraste de texto acima de 4,5:1
  em todos os pares de texto medidos (texto 13,6:1; suave 5,2–5,6:1 no claro, 7,6–8,5:1 no escuro; estados 5,7–9:1).
- Foco de 3 px com offset (`index.css:84-87`), `prefers-reduced-motion` (`index.css:89-96`), skip link, `h1` com foco
  na navegação.
- Estados de erro com "dados velhos" (`EstadoDaConsulta.tsx:54-68`) e esqueleto em vez de spinner.
- Tokens semânticos via CSS variables + `@theme inline`: um redesign troca valores sem tocar em toda tela.
- Gate de acessibilidade no CI (`web/e2e/a11y.spec.ts`, axe nos dois temas): o redesign herda a rede de proteção.

## 3. Três direções visuais

Todas compartilham o que a auditoria pede independentemente de gosto (entra em qualquer escolha):
**problemas primeiro** (ordenar por gravidade, resumo que só acende o que não está bem), **lista em linhas** em vez
de cartões de 230 px, **escala fechada** (espaço 4/8/12/16/24/32/48/64, fonte 12–36, pesos 400/600, um raio,
cinco sombras), tons 50–900 em OKLCH, tons de fundo para alertas, borda de input com 3:1, alvos de 44 px,
checkbox `accent-color` da marca, ícones só Lucide, e a página pública com os mesmos tokens.

Fontes: a CSP do painel é `font-src 'self'` (`internal/webui/webui.go:227`), então fonte web precisa ir no
bundle (pacote `@fontsource-variable/*`, ~30–60 KB por família woff2). A página pública precisaria de
`font-src 'self'` na CSP dela, ou fica com a system stack.

---

### Direção A: "Operacional sóbrio" (sala de controle neutra)

Inspiração de categoria: ferramentas de operação (Linear, Vercel, Better Stack). O dado é o protagonista, a cor
só aparece quando algo está errado.

- **Paleta (OKLCH)**
  - Cinzas frios, hue 255, croma sobe nos extremos: `gray-50 oklch(0.985 0.003 255)`, `100 0.967 0.005`,
    `200 0.92 0.008`, `300 0.86 0.011`, `400 0.70 0.015`, `500 0.56 0.018`, `600 0.46 0.020`, `700 0.37 0.022`,
    `800 0.27 0.022`, `900 0.21 0.024`, `950 0.16 0.022`.
  - Primária índigo, hue 265: `500 oklch(0.55 0.17 265)` (botão), `700 0.45 0.16 265`, `100 0.95 0.03 265`.
    Deliberadamente **fora** do eixo verde/amarelo/vermelho dos estados (resolve S2).
  - Estados: no ar `oklch(0.60 0.13 155)` (verde-esmeralda), degradado `oklch(0.75 0.15 75)` (âmbar),
    fora `oklch(0.58 0.20 27)` (vermelho), cada um com 100 (fundo) / 700 (texto) para badges invertidos.
  - Escuro: superfícies `gray-950 → 900 → 800` por elevação, borda interna `inset 0 0 0 1px` branco 8%.
- **Tipografia**: Inter Variable (bundle) com `font-feature-settings: 'tnum', 'cv11'`; 14 px corpo de tabela,
  16 px texto, 24/30 títulos, pesos 400/600. Números em `tabular-nums` sempre.
- **Densidade**: média-alta. Linha de alvo de 56 px no desktop, 72 px no celular (duas linhas). Raio 6 px.
  Sombras sutis só em diálogo/popover.
- **Mock da lista de alvos (1280)**

```
+----------------+-----------------------------------------------------------------------+
| (o) StillOn    |  Painel                                          [ + Cadastrar alvo ] |
|                |                                                                       |
| > Painel       |  +-----------------------------------------------------------------+ |
|   Incidentes 2 |  | (X) 1 fora do ar  (!) 1 degradado      5 no ar · 1 pausado      | |
|   Canais    !  |  +-----------------------------------------------------------------+ |
|   Status page  |   ^ faixa vermelha 100 só quando há problema; tudo ok = faixa verde  |
|   Config.      |                                                                       |
|                |  ALVO                  ESTADO        24 H              LAT.   UPTIME  |
|                |  ---------------------------------------------------------------------|
|                |  Banco de relatórios   (X) Fora 2h31 ||||||||||||||||:::  —     94,12% |
|                |  Painel do cliente     (!) Degradado ||||||||||||||||||:  2380ms 99,61% |
|                |  ---------------------------------------------------------------------|
|                |  API de pagamentos     (o) No ar     ||||||||||||||||||||  142ms 99,99% |
|                |  Site institucional    (o) No ar     ||||||||||||||||||||  318ms 100%   |
|                |  Webhook do Stripe     (o) No ar     ||||||||||||||||||||   88ms 99,99% |
|                |  Novo serviço          ( ) Sem dados  aguardando 1º check              |
|                |  Staging antigo        (=) Pausado    —                                |
| dono@…  (sol)  |                                         Atualizado há 12 s            |
+----------------+-----------------------------------------------------------------------+
  || = hora ok (cinza-400), : = hora ruim (vermelho, e barra mais BAIXA: forma + cor)
```

- **Prós**: responde "tem algo quebrado?" em um olhar; escala bem para 30–50 alvos; visual "ferramenta séria"
  que conversa com o resto do portfólio (Go, observabilidade); a primária índigo libera o verde para o estado.
- **Contras**: o mais "visto por aí" das três; perde a personalidade (o dono pode achar genérico); exige trocar
  cartões por tabela/linhas, que no celular pede um layout próprio.
- **Esforço**: médio. `index.css` (tokens inteiros), `components/ui/{botao,campo,dialogo,avisos}.tsx`,
  `components/{SeloDeEstado,Minigrafico,EstadoDaConsulta}.tsx`, `app/Layout.tsx` (lateral + barra inferior/menu no
  celular), `features/painel/PaginaPainel.tsx` (reescrita da lista e do resumo), `features/alvos/PaginaDetalhe.tsx`
  e `GraficoDeLatencia.tsx`, `features/incidentes`, `features/canais`, `features/statuspage`, `features/login`,
  `features/configuracoes` (ajustes de classe), `internal/httpapi/templates/status.html`, `package.json`
  (+ `@fontsource-variable/inter`). Testes de tela que procuram texto/papel devem sobreviver; os que dependem de
  `dl/dt` do cartão (`painel.test.tsx`) mudam. ~15 arquivos de código, 2–4 dias de trabalho em PRs pequenos.

---

### Direção B: "Terminal / mono" (console de plantão)

Inspiração: TUI, `htop`, painéis de NOC. Tudo é texto monoespaçado, alinhado em colunas; a personalidade vem da
tipografia e do rigor, não de cor.

- **Paleta (OKLCH)**
  - Escuro é o tema principal. Fundo `oklch(0.17 0.01 160)` (carvão esverdeado), superfícies `0.20`, `0.24`,
    linha `0.30`; texto `oklch(0.92 0.02 150)`, apoio `0.72 0.02 150`.
  - Primária "fósforo": `oklch(0.85 0.17 150)` (só cursor, foco, item ativo, botão primário com texto escuro).
  - Estados: ok `oklch(0.80 0.15 150)`, degradado `oklch(0.84 0.14 85)` (âmbar de CRT), fora `oklch(0.70 0.19 25)`.
    Forma por caractere: `●` ok, `▲` degradado, `✕` fora, `○` sem dados, `‖` pausado.
  - Claro: "papel de impressora", fundo `oklch(0.97 0.01 95)`, texto `0.22 0.02 150`, primária `0.45 0.12 150`.
- **Tipografia**: JetBrains Mono Variable ou IBM Plex Mono (bundle) em **tudo**; 14 px base, 12 para metadados,
  20/24 para títulos, pesos 400/600 (o mono já dá textura; nada de 700).
- **Densidade**: alta. Linha de 36–40 px no desktop (alvos de 44 px via área clicável da linha inteira),
  raio **0**, nenhuma sombra; separação por linhas de 1 px e fundos alternados.
- **Mock da lista de alvos (1280, escuro)**

```
 stillon ~ painel                                       [n] novo alvo   14/10 05:21 -03
 ──────────────────────────────────────────────────────────────────────────────────────
  STATUS  1 FORA  1 DEGR  3 OK  1 S/DADOS  1 PAUSA          próximo check em 18 s
 ──────────────────────────────────────────────────────────────────────────────────────
  EST  ALVO                    ÚLT. 24 H                       LAT     UP 24H   VISTO
  ✕    banco-de-relatorios     ▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂_ _ _         —    94.12%   40s
  ▲    painel-do-cliente       ▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▅▅▂▂     2380ms    99.61%   40s
  ●    api-de-pagamentos       ▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂      142ms    99.99%   40s
  ●    site-institucional      ▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂      318ms   100.00%   40s
  ●    webhook-do-stripe       ▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂       88ms    99.99%   40s
  ○    novo-servico            · · · aguardando primeiro check
  ‖    staging-antigo          pausado
 ──────────────────────────────────────────────────────────────────────────────────────
  [p]ainel  [i]ncidentes(2)  [c]anais(!)  [s]tatus  [g] config               ? atalhos
```
(Os nomes continuam os que o dono cadastrou; o kebab-case acima é só ilustração do tom.)

- **Prós**: o mais memorável para portfólio; casa com o produto (ferramenta de dev, Go, CLI `stillon`);
  densidade altíssima com alinhamento perfeito de números; zero sombra e zero raio simplificam o sistema;
  convida a atalhos de teclado (bom para a acessibilidade de teclado já existente).
- **Contras**: mono em formulários longos (configurações, status page) cansa; caixa de texto longa e mensagens de
  erro ficam largas; risco de virar "fantasia" se mal dosado; leitores com dislexia leem pior mono; a página
  pública para clientes finais talvez não deva ser mono (dá para usar só os tokens de cor nela). Precisa de cuidado
  com 44 px em linhas densas (linha inteira clicável).
- **Esforço**: médio-alto. Os mesmos arquivos da A, mais um componente de atalhos de teclado (opcional, pode ficar
  fora do primeiro PR), reescrita de navegação para barra inferior/topo, e revisão de todo formulário para a
  largura do mono. ~18 arquivos, 3–5 dias.

---

### Direção C: "Editorial claro" (relatório de status impresso)

Evolução assumida do vintage: em vez de "creme com oliva", um jornal/relatório. Tipografia serifada grande nos
títulos, muito branco, a tela abre com uma **frase** de estado.

- **Paleta (OKLCH)**
  - Papel quente hue 85: fundo `oklch(0.98 0.008 85)`, superfície `1.0 0 0` (branco) com sombra nível 1,
    cinzas quentes `ink-900 oklch(0.22 0.015 60)`, `ink-600 0.45 0.015 60`, `ink-400 0.65 0.012 60`,
    linha `0.90 0.01 80`.
  - Primária tinta azul-petróleo hue 220: `500 oklch(0.50 0.09 220)`, `700 0.40 0.08 220`, `100 0.95 0.02 220`.
  - Estados com tons de "tinta": ok `oklch(0.52 0.11 150)`, degradado `oklch(0.62 0.13 70)`, fora `oklch(0.53 0.17 30)`,
    com fundos 100 para marcações tipo marca-texto.
  - Escuro: "modo noturno de leitura", fundo `oklch(0.20 0.01 60)`, superfícies mais claras por elevação.
- **Tipografia**: títulos e números grandes em Source Serif 4 Variable (ou Fraunces, mais expressiva), texto e
  dados em Inter/Source Sans 3; 16 px corpo, 30/36/48 títulos, pesos 400/600.
- **Densidade**: baixa. Linha de alvo de 72 px, raio 8 px, sombras em duas partes (nível 1 e 2).
- **Mock da lista de alvos (1280)**

```
  StillOn                         Painel   Incidentes (2)   Canais   Status page   Conta
  ──────────────────────────────────────────────────────────────────────────────────────

  Dois serviços pedem atenção.                                       [ Cadastrar alvo ]
  Banco de relatórios está fora do ar há 2 h 31 min; Painel do cliente está lento.
  Atualizado às 05:21 · 5 de 7 no ar

  ┃ Banco de relatórios                                           Fora do ar  ✕
  ┃ 94,12% nas últimas 24 h · sem resposta (conexão recusada)     Ver incidente →
  ┃ ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁ ▁ ▁
  
  ┃ Painel do cliente                                             Degradado  ▲
  ┃ 99,61% · 2.380 ms (limite 2.000 ms)
  
  Funcionando ──────────────────────────────────────────────────────────────────────
    API de pagamentos          142 ms     99,99%   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁   ●
    Site institucional         318 ms    100,00%   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁   ●
    Webhook do Stripe           88 ms     99,99%   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁   ●
  
  Sem dados ou pausados ─────────────────────────────────────────────────────────────
    Novo serviço · aguardando o primeiro check      Staging antigo · pausado
```
  (`┃` = borda de acento à esquerda na cor do estado, RUI cap. 8.)

- **Prós**: o mais "humano" e o que melhor serve a página pública (clientes finais leem frases); a frase de
  estado é acessível por natureza; continuidade com o que já existe (menos ruptura de testes, pode reaproveitar
  a lógica `resumoEmTexto` de `PaginaPainel.tsx:15-32`, hoje só `sr-only`); navegação no topo resolve o L1.
- **Contras**: mais próximo do vintage que o dono não gostou (risco de "mudou pouco"); densidade baixa não escala
  para 30+ alvos; serifa em dashboard é aposta estética; duas famílias de fonte = mais bundle e mais decisões.
- **Esforço**: médio. Mesmos arquivos da A; a lista vira grupos (problemas / funcionando / sem dados), a nav vai
  para o topo (`Layout.tsx` muda de estrutura), +2 pacotes de fonte. ~15 arquivos, 2–4 dias.

---

## 4. Recomendação

**Direção A, "Operacional sóbrio"**, com uma pitada da B: números e a coluna de 24 h em fonte mono tabular
(`JetBrains Mono` só para dados), o que dá personalidade sem o custo de mono nos formulários.

Por quê: o defeito central do painel atual é de hierarquia (problema não se destaca) e de densidade (cartões
grandes repetindo labels). A A ataca os dois de frente, escala com o número de alvos, separa marca de estado
(primária índigo x verde/âmbar/vermelho) e é a mais fácil de manter dentro de um sistema fechado. A C corre o
risco de repetir o vintage; a B é a mais bonita para portfólio, mas cobra caro em formulários e em leitura.

Custo principal: 2–4 dias em PRs pequenos (tokens e componentes base primeiro, depois painel, detalhe, demais
telas, página pública) e reescrita dos testes de tela do cartão do painel. Decisões que ficam com o dono:
direção, matiz da primária, fonte (Inter x system stack para zero bundle) e se a página pública ganha
`font-src 'self'` na CSP.

Ordem sugerida, qualquer que seja a direção:
1. Tokens (`index.css`): escala OKLCH, borda 3:1, tons 100/700 de estado, sombras, um raio. Teste de contraste
   automatizado dos pares de tokens (vitest lendo o CSS), além do axe.
2. Componentes base: botão (pirâmide primário/secundário/terciário; destrutivo terciário fora da confirmação),
   controle segmentado de 44 px, selo, badge invertido, checkbox com `accent-color`.
3. Painel: ordenação por gravidade, resumo que só acende problemas, lista em linhas, minigráfico com forma (altura)
   além de cor.
4. Detalhe, incidentes, canais, status page, login, configurações.
5. Página pública com os mesmos tokens e barras de 90 dias com 3:1 e forma.
