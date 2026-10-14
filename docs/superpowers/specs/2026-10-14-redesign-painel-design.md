# Redesign do painel: direção C, "Editorial claro"

Data: 2026-10-14. Parte da [auditoria do painel](../reports/2026-10-14-auditoria-painel.md), que tem
o diagnóstico e as três direções. As decisões visuais foram do dono, tomadas no brainstorming com
mockups. Base: Refactoring UI (`~/.claude/knowledge/ui-design.md`, regras **DI n** do
`~/.claude/CLAUDE.md`).

O painel anterior está preservado na tag `painel-antes-do-redesign` (`b685891`) e nas capturas de
`docs/design/painel-antes/`: 10 telas em 1280 e 400 px, nos temas claro e escuro, mais a página
pública.

## Objetivo e critério de sucesso

O painel atual é correto (axe limpo, estados existem), mas tudo pesa igual: um alvo fora do ar é
um cartão idêntico a um no ar. O redesign tem de:

1. **Responder "tem algo quebrado?" de relance.** Os problemas aparecem primeiro, com cor, forma e
   texto, e o estado bom fica calmo.
2. **Escalar para 30 alvos ou mais** sem rolar várias telas.
3. **Dar ao produto uma identidade única.** O painel e a página pública passam a ser o mesmo
   produto.
4. **Não piorar a acessibilidade.** O axe continua limpo nos dois temas. O contraste dos tokens
   (4,5:1 para texto, 3:1 para componente e foco) é testado. Os alvos de toque têm 44 px, o foco
   continua visível e o `prefers-reduced-motion` continua respeitado.

## Decisões do dono

| Tema | Escolha |
|---|---|
| Direção | **C, "Editorial claro"**: frase de estado no topo, problemas em destaque, títulos serifados, a página "respira". |
| Fontes | **Fraunces** (títulos, frase de estado, marca, nome do alvo) + **Inter** (texto, dados, formulários). |
| Números grandes | **Inter 600** com cifras tabulares, não a Fraunces. |
| Primária | **Índigo**: `oklch(0.47 0.15 270)` no claro, `oklch(0.76 0.11 270)` no escuro. |
| Seções aprovadas | 1, painel (desktop, celular e "tudo no ar"); 2, detalhe do alvo e incidentes; 3, página pública e login. |

Os mockups aprovados ficam em `.superpowers/brainstorm/` (fora do Git) e estão descritos abaixo.

## Decisões técnicas (minhas, sem efeito visual)

- **Sem mudança de API.** O "próximo lembrete" do mockup de incidentes saiu de propósito: exigiria
  campo novo no backend. Tudo o que está aqui sai dos endpoints atuais.
- **Fontes auto-hospedadas** em `web/public/fonts/` (woff2 variável, subconjunto latino, com o
  arquivo da licença OFL ao lado). O Vite copia `public/` sem hash, então o painel e a página
  pública usam a mesma URL estável `/fonts/…`. O `font-src 'self'` do painel já permite. A CSP da
  página pública (`internal/httpapi/publicpage.go:92`) ganha `font-src 'self'`, com teste.
  - Por que não `@fontsource-variable`: o hash do Vite mudaria a URL a cada build, e o template Go
    da página pública não teria como apontar para ela.
- **Tokens em OKLCH** no `index.css`. O `tokens.test.ts` passa a converter OKLCH para sRGB antes de
  calcular o contraste, sem dependência nova, porque a fórmula é curta. Os nomes semânticos atuais
  (`--fundo`, `--superficie`, `--texto`, `--suave`, `--primaria`, `--estado-*`…) ficam, para as telas
  não mudarem de vocabulário.
- **Entrega em PRs pequenos**, cada um com CI verde e o painel funcionando entre eles. A
  alternativa, uma branch longa com tudo, foi descartada: integra tarde e o CI só roda no fim.

## Sistema

### Cor (OKLCH)

| Papel | Claro | Escuro |
|---|---|---|
| Fundo ("papel") | `0.98 0.008 85` | `0.20 0.01 60` |
| Superfície | branco | `0.25 0.01 60`; a elevação clareia em vez de sombrear |
| Texto | `0.22 0.015 60` | `0.93 0.01 80` |
| Texto suave | `0.45 0.015 60` | `0.75 0.012 70` |
| Texto apagado (metadados) | `0.58 0.012 60` | `0.65 0.012 70` |
| Divisória | `0.90 0.01 80` | `0.32 0.01 60` |
| Borda de controle (3:1) | `0.62 0.015 60` | `0.55 0.012 70` |
| Primária | `0.47 0.15 270`, texto branco | `0.76 0.11 270`, texto `0.20 0.03 270` |
| Primária 100 (item ativo, selo de reconhecido) | `0.95 0.03 270` | `0.30 0.05 270` |
| No ar | texto `0.48 0.11 150`, fundo 100 `0.96 0.03 150` | `0.78 0.13 150` |
| Degradado | texto `0.52 0.12 70`, fundo 100 `0.97 0.035 85` | `0.82 0.13 80` |
| Fora do ar | texto `0.50 0.17 30`, fundo 100 `0.96 0.025 30` | `0.72 0.17 30` |

Os valores do escuro e os tons 100 são ponto de partida. O teste de contraste manda: um par
reprovado ajusta a luminosidade, nunca o matiz.

### Tipografia

- Escala de tamanhos: 12, 13, 14, 16, 18, 20, 24, 28, 36 px, em `rem`. Pesos 400 e 600 (DI 3).
- **Fraunces 600**: `h1` (36 no desktop, 28 no celular), títulos de seção (18 a 20), nome do alvo
  em cartão de problema e no detalhe, marca.
- **Inter**: o resto. Números com `font-variant-numeric: tabular-nums` sempre. Os números grandes
  (KPIs) vão em Inter 600 de 28 px, com a unidade em Inter 400 de 13 px.
- Caixa alta só em rótulos de grupo pequenos, com `letter-spacing: 0.05em` (DI 6).

### Espaço, raio, sombra

- Espaçamento só na escala 4/8/12/16/24/32/48/64 (DI 2). Nada de `px-5`, `p-7` ou valores
  arbitrários.
- **Um raio: 8 px** (cartões, botões, campos, segmentos). Selos e badges são pílulas (`999px`).
- Sombras em dois níveis, ambos em duas camadas (DI 9): nível 1 para cartão e nível 2 para diálogo
  e popover. O escuro não usa sombra; a superfície mais clara faz o papel.
- **Borda de acento à esquerda** de 4 px na cor do estado, para cartão de problema e incidente
  aberto (índigo quando reconhecido).

### Componentes base

| Componente | Regras |
|---|---|
| Botão | Pirâmide (DI 4): primário índigo sólido; secundário com contorno; terciário em texto. Destrutivo é terciário até a confirmação, e só o botão do diálogo de confirmação é vermelho. Altura mínima de 44 px. |
| Controle segmentado | Novo. Usado na janela do gráfico e no filtro de incidentes. Trilho `painel`, item ativo em superfície com sombra nível 1, área de toque de 44 px. É um `radiogroup` acessível por teclado. |
| Selo de estado | Pílula com fundo 100, texto na cor do estado, ícone Lucide **e** texto (forma + cor + texto). |
| Campo | Altura de 44 px, borda de controle 3:1, foco índigo de 3 px. |
| Checkbox | `accent-color` da primária (DI 12). |
| Ícones | Só Lucide. Saem os glifos Unicode dos canais (`✓ … ⧖ ✕ ⏸`, auditoria S9). |

## Telas

### 1. Painel (lista de alvos)

- **Manchete** em Fraunces, gerada dos dados (reaproveita `resumoEmTexto`, hoje só `sr-only`):
  - com problemas: "Dois serviços pedem atenção.", com uma frase de detalhe;
  - tudo bem: "Tudo no ar.", com ícone de check em círculo verde 100 e a hora do último incidente
    resolvido;
  - meta: "Atualizado às HH:MM · N de M no ar".
- **Ordem por gravidade**: fora do ar, depois degradado, cada um num **cartão** com borda de acento,
  selo, causa ou latência e uptime de 24 h, e link "Ver incidente →" ou "Ver detalhes →". O cartão
  inteiro é clicável (auditoria P8).
- **"Funcionando"**: linhas compactas, com cabeçalho de colunas uma vez só (Alvo, Latência, 24 h,
  Últimas 24 h). A barra de 24 h usa forma e cor: a hora boa é cinza-papel, e a hora ruim é mais
  baixa e na cor do estado.
- **"Sem dados ou pausados"**: uma linha de texto por alvo, sem gráfico (auditoria P7).
- **Navegação**: no topo no desktop, com o item ativo em primária 100. No celular, barra inferior de
  5 abas de 44 px (Painel, Incidentes, Canais, Status, Conta), sem os 265 px de menu antes do
  conteúdo (auditoria L1). O tema vira um botão de ícone (L5).
- Carregando: o esqueleto imita a manchete, os cartões e as linhas (P12).

### 2. Detalhe do alvo

- Volta "← Painel", nome em Fraunces 32, método, URL e intervalo em texto apagado.
- Ações: Editar (secundário), Pausar e Apagar (terciários).
- **Faixa de estado** com fundo 100 da cor, ícone em círculo, título em Fraunces ("Fora do ar há
  2 h 31 min"), causa e início, e botão **Reconhecer** quando há incidente aberto sem ack.
- **Quatro KPIs** em Inter 600: uptime de 24 h, uptime de 30 dias, latência mediana e último check.
- **Gráfico de latência**:
  - linha da mediana em índigo, faixa p50–p95 em primária 100, "fora do ar" como faixa hachurada
    (forma + cor);
  - ticks do eixo X em horas redondas;
  - escala Y robusta: o teto vem do p95 da janela com folga, e o pico acima dele é anotado na
    legenda ("pico de 2.400 ms às 03:10, fora da escala") em vez de esmagar a série;
  - seletor de janela no controle segmentado.
- A tabela de dados do gráfico continua para leitor de tela e teclado, com números à direita.
- **Incidentes recentes** em linhas com ponto de estado e etiqueta (Em andamento, Reconhecido,
  Resolvido).

### 3. Incidentes

- Título, frase de resumo ("1 em andamento, 1 reconhecido"), o texto de ajuda do ack e o filtro no
  controle segmentado (Abertos, Resolvidos, Todos).
- Grupos "Em andamento" e "Resolvidos recentes".
- Cartão aberto com borda vermelha e botão Reconhecer. Reconhecido com borda índigo e "lembretes
  parados". Resolvido sem borda, com o nome apagado.
- Vazio, que é o estado bom: ícone de check e "Nenhum incidente aberto" com tratamento positivo
  (I4).

### 4. Canais, status page (configuração), configurações e novo alvo

A estrutura das telas fica. Mudam os tokens e os componentes: botões em pirâmide (Apagar
terciário, C1 e G2), ícones Lucide (C2), "Pausado" mais visível que "Verificado" num canal pausado
(C3), checkbox na cor da marca (S10), formulários com `max-w` no cartão (G3), "Subir / Descer /
Tirar" com Tirar terciário (SP2) e prévia da status page que parece a pública (SP3).

### 5. Login

Marca (ponto índigo + "StillOn" em Fraunces 28), frase de tom em Fraunces ("Seus serviços,
vigiados de perto.", trocável pelo dono), cartão de formulário com sombra nível 2, campos e botão
de 44 px.

### 6. Página pública (`internal/httpapi/templates/status.html`)

- Os mesmos tokens e fontes, no CSS inline do template (com nonce, como hoje). Ela não importa o
  CSS do painel. O tema escuro continua por `prefers-color-scheme`, como hoje, com os tokens do
  escuro.
- Nome da organização em Fraunces e **faixa de estado** com frase ("Todos os sistemas funcionando."
  ou "Instabilidade em 1 serviço" com detalhe).
- **Barras de 90 dias com forma e cor**: o dia normal tem altura cheia, o instável dois terços, o
  fora do ar menos da metade, e "sem dados" é só o contorno. Cada cor tem 3:1 sobre o fundo (hoje
  o verde e o vermelho ficam a 1,37:1 entre si, auditoria SP5).
- Legenda explícita, uptime de 90 dias por componente, histórico curto em frases e "Monitorado pelo
  StillOn" no rodapé.

## Testes

- `tokens.test.ts` convertendo OKLCH e cobrindo os pares novos: texto, suave e apagado sobre fundo
  e superfície; estados sobre seus fundos 100; primária com seu texto; borda de controle e foco a
  3:1; nos dois temas.
- O axe do Playwright (`web/e2e/a11y.spec.ts`) segue nos dois temas e passa a cobrir também o
  celular (400 px).
- Testes de tela:
  - os que procuram papel e texto sobrevivem;
  - os que dependem do `dl/dt` do cartão do painel são reescritos;
  - testes novos para a ordenação por gravidade, a manchete (problemas, tudo bem, sem alvos), o
    controle segmentado (teclado) e a escala robusta do gráfico (função pura, com o pico fora da
    escala).
- Go: teste da CSP da página pública com `font-src 'self'` e teste de que o template renderiza a
  faixa, a legenda e as barras com a classe do estado de cada dia.
- Cada teste novo é visto falhando antes (regra do projeto).

## Ordem de entrega (um PR cada)

1. **Fundação**: fontes em `web/public/fonts/` com licença, tokens OKLCH, `tokens.test.ts` novo,
   escala e raio. Componentes base: botão, campo, checkbox, selo e controle segmentado novo.
2. **Navegação**: topo no desktop e barra inferior no celular, com tema em botão de ícone.
3. **Painel**: manchete, ordenação, cartões de problema, linhas, grupos, esqueleto e "tudo no ar".
4. **Detalhe do alvo**: faixa, KPIs, gráfico novo e incidentes recentes.
5. **Incidentes**.
6. **Canais, status page, configurações, novo alvo e login.**
7. **Página pública**: template, CSP e testes Go.
8. **Fechamento**: capturas "depois" em `docs/design/painel-depois/` e README com as imagens novas.

O PR 1 muda o visual de todas as telas de uma vez, porque os tokens são globais. Isso é esperado:
entre os PRs 1 e 6 o painel funciona, com telas parcialmente no estilo novo.

## Fora do escopo

- "Próximo lembrete" e canais avisados no incidente (exigem API).
- Atalhos de teclado.
- Mudança de rotas, de dados ou de comportamento. O redesign é visual e de hierarquia.
- Seletor de tema na página pública (ela segue o sistema operacional, como hoje).
