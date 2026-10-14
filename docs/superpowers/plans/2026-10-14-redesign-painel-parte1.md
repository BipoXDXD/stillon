# Redesign do painel, parte 1: Fundação (PR 1) e Navegação (PR 2). Plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para implementar este plano tarefa a tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.

**Objetivo:** dar ao painel a fundação visual da direção C, "Editorial claro" (fontes, tokens OKLCH, escala, raio, sombras e componentes base), e trocar a lateral por navegação no topo (desktop) e barra inferior de 5 abas (celular), com o painel funcionando e o CI verde ao fim de cada PR.

**Arquitetura:** tudo é front-end em `web/`. Os tokens continuam como variáveis CSS no `index.css`, expostas ao Tailwind 4 por `@theme inline`; a escala de fonte, o raio e as famílias vão num `@theme static`; as sombras e o rótulo de grupo são `@utility`. Um teste de "sistema" (`src/sistema.test.ts`) reprova classe fora da escala nos arquivos já redesenhados, e a lista cresce a cada PR. Os componentes base ficam em `src/components/ui/` e `src/components/`; a navegação, em `src/app/Layout.tsx`.

**Tech stack:** Vite 8, React 19, Tailwind 4.3 (`@theme inline`, `@theme static`, `@utility`), `radix-ui` (Slot), `class-variance-authority`, `tailwind-merge` 3, lucide-react, Vitest 5 + Testing Library + user-event, Playwright 1.63 + axe, Go (só um teste do `internal/webui`). Ferramenta de fontes: fonttools 4.63.0 (Homebrew), com brotli.

**Spec:** [`docs/superpowers/specs/2026-10-14-redesign-painel-design.md`](../specs/2026-10-14-redesign-painel-design.md). Contexto: [auditoria](../reports/2026-10-14-auditoria-painel.md) e `~/.claude/knowledge/ui-design.md` (regras DI do `~/.claude/CLAUDE.md`).

**Partes seguintes:** os PRs 3 a 8 (painel, detalhe, incidentes, demais telas, página pública e fechamento) ficam nas partes 2 e 3 do plano. Elas partem da seção [Interfaces publicadas para os PRs seguintes](#interfaces-publicadas-para-os-prs-seguintes), no fim deste documento.

## Global Constraints

- Direção **C, "Editorial claro"**: frase de estado no topo, problemas em destaque, títulos serifados.
- Fontes: **Fraunces** (títulos, frase de estado, marca, nome do alvo) + **Inter** (texto, dados, formulários). Números grandes em **Inter 600** com cifras tabulares.
- Primária **índigo**: `oklch(0.47 0.15 270)` no claro, `oklch(0.76 0.11 270)` no escuro.
- Fontes auto-hospedadas em `web/public/fonts/` (woff2 variável, subconjunto latino, licença OFL ao lado), servidas na URL estável `/fonts/…`. Nada de `@fontsource-variable`.
- Tokens em OKLCH no `index.css`; os nomes semânticos atuais (`--fundo`, `--superficie`, `--texto`, `--suave`, `--primaria`, `--estado-*`…) ficam. O `tokens.test.ts` converte OKLCH para sRGB **sem dependência nova**.
- Contraste: **4,5:1** para texto e **3:1** para componente e foco, nos dois temas. Um par reprovado ajusta a luminosidade, nunca o matiz.
- Escala de fonte: **12, 13, 14, 16, 18, 20, 24, 28, 36 px**, em `rem`. Pesos **400 e 600**.
- Espaçamento só na escala **4/8/12/16/24/32/48/64** (DI 2). Nada de `px-5`, `p-7` ou valores arbitrários.
- **Um raio: 8 px** (cartões, botões, campos, segmentos). Selos e badges são pílulas (`999px`).
- Sombras em **dois níveis**, ambos em duas camadas: nível 1 para cartão, nível 2 para diálogo e popover. **O escuro não usa sombra**; a superfície mais clara faz o papel.
- Caixa alta só em rótulos de grupo pequenos, com `letter-spacing: 0.05em`.
- Ícones **só Lucide**.
- Alvos de toque de **44 px**, foco visível (anel índigo de 3 px), `prefers-reduced-motion` respeitado, axe limpo nos dois temas.
- **Sem mudança de API**, de rotas, de dados ou de comportamento: o redesign é visual e de hierarquia.
- Um PR por etapa, cada um com o CI verde e o painel funcionando entre eles.
- Projeto: tudo em português do Brasil; commits `tipo(escopo): descrição`; mudança na `main` só por PR; antes de dizer que algo funciona, rode `go build ./... && go vet ./... && make test` na raiz e `npm test && npm run typecheck && npm run lint` em `web/`; **teste novo só vale depois de vê-lo falhar com o código sabotado**; `sh deploy/ci/checar-nome.sh` passa.

## Review Focus

1. **Texto fora do subconjunto latino** (nome de alvo em grego, cirílico ou com emoji; as setas de "← Painel" e "Ver incidente →"): o navegador tem de cair na família genérica, sem tofu e sem texto invisível enquanto a fonte carrega. Teste na Task 1 (`fontes.test.ts`: `unicode-range` com as setas, `font-display: swap`, pilhas terminando em família genérica).
2. **Valor do controle segmentado fora das opções** (URL editada à mão, `?janela=abc`): o grupo continua alcançável pelo Tab e nenhum item mente que está marcado. Teste na Task 7.
3. **Um PR seguinte usa `tipo="perigo"` num "Apagar" fora da confirmação**: o vermelho sólido volta a aparecer em toda linha (auditoria C1, G2). O CI tem de reprovar. Teste na Task 4 (`sistema.test.ts` conta os usos por arquivo).
4. **Celular: a barra inferior fixa cobre o fim do conteúdo** (o último botão de um formulário fica embaixo dela). Teste na Task 12 (e2e de 400 px, rota a rota).
5. **Classe nova do sistema some no `cn`** (`text-meta` ao lado de `text-suave`: o tailwind-merge acha que as duas são cor e descarta a primeira). Teste na Task 3 (`utils.test.ts`).

## Decisões deste plano (pontos da spec que pediram interpretação)

- **Um valor da spec reprova no teste de contraste:** o texto apagado do claro, `oklch(0.58 0.012 60)`, fica a 4,05:1 sobre o fundo. Vai a `oklch(0.53 0.012 60)` (5,00:1 sobre o fundo, 4,57:1 sobre o painel), só a luminosidade. Todos os outros valores da spec passam como estão, inclusive a borda de controle do escuro (`0.55`), porque o painel escuro ficou mais escuro que o fundo (abaixo).
- **Tokens que a spec não fixa**, calculados para passar no teste: painel (trilho, hover, área rebaixada) claro `0.95 0.01 80` e escuro `0.16 0.01 60` (rebaixado: mais escuro que o fundo, e o item ativo do segmentado, em superfície, clareia); tons 100 do escuro (no ar `0.30 0.05 150`, degradado `0.31 0.05 80`, fora do ar `0.30 0.06 30`, sem dados `0.30 0.01 60`); texto sobre o perigo (branco no claro, `0.20 0.03 30` no escuro); sobreposição dos diálogos; cores do gráfico.
- **"Nome em Fraunces 32" no detalhe** não está na escala (12…28, 36). A escala vence: o nome do detalhe é o `h1` do `Titulo`, 28 px no celular e 36 px no desktop. Fica registrado para o PR 4.
- **Nomes das abas:** a spec lista as 5 abas do celular (Painel, Incidentes, Canais, Status, Conta). O desktop usa **os mesmos nomes**: um `<nav>` só, com nome acessível que não muda com a largura (WCAG 3.2.4) e sem texto duplicado escondido por CSS (que o jsdom leria duas vezes). "Status page" e "Configurações" continuam como título das telas.
- **Tema em botão de ícone:** um clique passa de Sistema a Claro, de Claro a Escuro e de Escuro a Sistema. O nome acessível diz o tema em vigor ("Tema: Claro") e o `title`, o próximo. Um menu com três opções foi descartado: mais peças (portal, trava de rolagem com nonce) para três valores.
- **Controle segmentado próprio**, no padrão "radio group" do WAI-ARIA APG, e não o `RadioGroup` do Radix: prototipado no jsdom, o Radix só marca o item quando a seta ainda está pressionada no momento do foco (o foco vem num `setTimeout`), o que deixaria o teste dependente do tempo.
- **Botão `perigo` muda de sentido no PR 1:** passa a ser vermelho sólido, só para a confirmação. Por isso as chamadas atuais de "Apagar", "Encerrar" e "Desativar" fora de diálogo migram para `terciario` já no PR 1; sem isso, entre os PRs 1 e 6 o painel teria quatro botões vermelhos sólidos. O `fantasma` vira `terciario` no mesmo passo.
- **Checkbox:** o `accent-color` da primária já é global no `index.css`; o PR 1 cria `CaixaDeMarcar` (linha inteira de 44 px como área de toque) e migra as três caixas existentes.
- **Axe no celular:** a spec pede o axe em 400 px. O teste de 360 px que já existe roda o axe em todas as rotas, nos dois temas, numa largura menor; ele fica, e o PR 2 acrescenta um teste de 400 px para a barra inferior. Não se duplica a passada do axe (orçamento de CI).
- **Espaço abaixo do conteúdo no celular:** a escala vai até 64, então o `main` usa `pb-16` (64 px) sobre a barra de 53 px.
- **Sombra no escuro:** `0 0 transparent`, não `none`: o Tailwind compõe sombras numa lista, onde `none` invalida a declaração.

## Mapa de arquivos

PR 1, Fundação:

| Arquivo | Responsabilidade |
|---|---|
| `web/scripts/gerar-fontes.sh` (novo) | Baixa as fontes do Google Fonts num commit fixo, confere o blob, gera os subconjuntos woff2 |
| `web/public/fonts/fraunces-latin.woff2`, `inter-latin.woff2`, `OFL-Fraunces.txt`, `OFL-Inter.txt` (gerados) | Fontes e licenças, servidas em `/fonts/…` |
| `web/src/fontes.test.ts` (novo) | `@font-face` × arquivos em disco × licenças |
| `web/src/index.css` | `@font-face`, tokens OKLCH, `@theme`, utilitários, base dos títulos |
| `web/src/tokens.test.ts` (reescrito) | OKLCH → sRGB e contraste dos pares nos dois temas |
| `web/src/sistema.test.ts` (novo) | Fitness function do sistema visual e dos usos do vermelho sólido |
| `web/src/lib/utils.ts` + `utils.test.ts` (novo) | `cn` com o tailwind-merge estendido |
| `web/src/components/Titulo.tsx` | `h1` em Fraunces 600, 28/36 px |
| `web/src/components/ui/botao.tsx` + `botao.test.tsx` (novo) | Pirâmide de botões |
| `web/src/components/ui/campo.tsx` + `campo.test.tsx` (novo) | Campo de 44 px e `CaixaDeMarcar` |
| `web/src/lib/estado.ts` + `estado.test.ts` (novos) | `EstadoExibido`, `estadoExibido` e `textoDoEstado` (R1.1 do backlog de refatoração) |
| `web/src/components/SeloDeEstado.tsx` + `selo.test.tsx` (novo) | `Selo` e `SeloDeEstado` em pílula com fundo 100 |
| `web/src/components/ui/controle-segmentado.tsx` + `controle-segmentado.test.tsx` (novos) | Radiogroup segmentado |
| `web/src/components/ui/dialogo.tsx`, `avisos.tsx` | Raio, sombra nível 2, sobreposição por token |
| `web/src/features/**` (6 arquivos) | Migração de `fantasma`/`perigo` e das caixas de marcar |
| `web/e2e/a11y.spec.ts`, `web/e2e/fluxo.spec.ts` | Cor computada pode vir em `oklch(…)`; comentário do alvo de toque |
| `internal/webui/webui_test.go` | `/fonts/x.woff2` sai como `font/woff2` e revalida |

PR 2, Navegação:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/components/Marca.tsx` + `marca.test.tsx` (novos) | Ponto índigo + "StillOn" em Fraunces |
| `web/src/app/BotaoDeTema.tsx` + `botao-de-tema.test.tsx` (novos) | Tema em botão de ícone |
| `web/src/app/Layout.tsx` + `layout.test.tsx` (novo) | Topo no desktop, barra inferior no celular |
| `web/src/components/ui/avisos.tsx` | Avisos acima da barra inferior no celular |
| `web/src/index.css` | Sai o `.marca-simbolo` |
| `web/src/main.test.tsx` | O tema deixa de ser `combobox` |
| `web/e2e/a11y.spec.ts`, `fluxo.spec.ts`, `troca.spec.ts` | Aviso de HTTPS acima do topo, abas no alvo de toque, barra sem cobrir o conteúdo, "Conta" |

---

# PR 1: Fundação

Branch: `feat/redesign-fundacao`, a partir da `main` com a spec integrada.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-fundacao
```

### Task 1: Fontes Fraunces e Inter auto-hospedadas

**Files:**
- Create: `web/scripts/gerar-fontes.sh`
- Create (gerados pelo script): `web/public/fonts/fraunces-latin.woff2`, `web/public/fonts/inter-latin.woff2`, `web/public/fonts/OFL-Fraunces.txt`, `web/public/fonts/OFL-Inter.txt`
- Create: `web/src/fontes.test.ts`
- Modify: `web/src/index.css` (topo do arquivo e bloco `@theme inline`; regra `body`)
- Modify: `internal/webui/webui_test.go:31-40` (mapa `distDeTeste`) e a tabela de `TestRoteamentoDoPainel`

**Interfaces:**
- Consumes: nada.
- Produces: famílias `'Fraunces'` e `'Inter'` com pesos 100 a 900; variáveis `--font-sans` e `--font-display` num bloco `@theme static`; classes `font-sans` (padrão do `<html>`) e `font-display`.

- [ ] **Step 1: Escrever o teste que falha**

Crie `web/src/fontes.test.ts`:

```ts
/// <reference types="node" />
import { readFileSync, statSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// As fontes do painel (spec do redesign, "Decisões técnicas"): woff2 variável,
// subconjunto latino, em public/fonts com a licença OFL ao lado. O Vite copia
// public/ sem hash, e a URL /fonts/… fica estável também para a página
// pública. scripts/gerar-fontes.sh gera os arquivos; este teste confere o
// disco contra o que o index.css promete.
const css = readFileSync('src/index.css', 'utf8')
const faces = Array.from(css.matchAll(/@font-face\s*\{([^}]*)\}/g), (m) => m[1] ?? '')

function propriedade(face: string, nome: string): string {
  const m = new RegExp(`${nome}:\\s*([^;]+);`).exec(face)
  if (!m) throw new Error(`@font-face sem ${nome}`)
  return (m[1] ?? '').trim()
}

function arquivoDa(face: string): string {
  const src = propriedade(face, 'src')
  const url = /^url\('\/fonts\/([\w-]+\.woff2)'\) format\('woff2'\)$/.exec(src)
  if (!url) throw new Error(`src fora do formato url('/fonts/x.woff2') format('woff2'): ${src}`)
  return `public/fonts/${url[1] ?? ''}`
}

// O subconjunto gerado tem 69.392 bytes (Fraunces) e 105.352 (Inter) com o
// fonttools 4.63.0. O teto pega um arquivo que perdeu o corte de caracteres.
const TETO_EM_BYTES = 128 * 1024
const POR_FAMILIA = faces.map((f) => [propriedade(f, 'font-family'), f] as const)

describe('@font-face do index.css', () => {
  it('declara a Fraunces e a Inter', () => {
    expect(POR_FAMILIA.map(([familia]) => familia)).toEqual(["'Fraunces'", "'Inter'"])
  })

  it.each(POR_FAMILIA)('%s aponta para um woff2 de verdade em public/fonts, dentro do teto', (_familia, face) => {
    const caminho = arquivoDa(face)
    expect(readFileSync(caminho).subarray(0, 4).toString('latin1')).toBe('wOF2')
    expect(statSync(caminho).size).toBeLessThanOrEqual(TETO_EM_BYTES)
  })

  it.each(POR_FAMILIA)('%s é variável (100 a 900) e não esconde o texto enquanto carrega', (_familia, face) => {
    expect(propriedade(face, 'font-weight')).toBe('100 900')
    expect(propriedade(face, 'font-display')).toBe('swap')
  })

  // Review Focus 1: as setas de "← Painel" e "Ver incidente →" entram no
  // subconjunto; o resto (grego, cirílico, emoji) cai na família genérica.
  it.each(POR_FAMILIA)('%s cobre o latim e as setas', (_familia, face) => {
    const faixa = propriedade(face, 'unicode-range')
    expect(faixa).toContain('U+0000-00FF')
    expect(faixa).toContain('U+2190-2193')
  })
})

describe('pilhas de fonte do tema', () => {
  // Review Focus 1: sem a família genérica no fim, um caractere fora do
  // subconjunto não teria para onde cair.
  it.each([
    ['--font-sans', "'Inter', ui-sans-serif, system-ui, sans-serif"],
    ['--font-display', "'Fraunces', ui-serif, Georgia, serif"],
  ])('%s termina numa família genérica', (token, pilha) => {
    expect(css).toContain(`${token}: ${pilha};`)
  })
})

describe('licenças', () => {
  it.each([
    ['OFL-Fraunces.txt', 'Copyright 2018 The Fraunces Project Authors'],
    ['OFL-Inter.txt', 'Copyright 2020 The Inter Project Authors'],
  ])('public/fonts/%s acompanha a fonte', (arquivo, copyright) => {
    const texto = readFileSync(`public/fonts/${arquivo}`, 'utf8')
    expect(texto).toContain(copyright)
    expect(texto).toContain('SIL Open Font License, Version 1.1')
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/fontes.test.ts`
Expected: FAIL. "declara a Fraunces e a Inter" recebe `[]`; as pilhas não estão no CSS; `ENOENT` nas licenças.

- [ ] **Step 3: Escrever o script de geração**

Crie `web/scripts/gerar-fontes.sh`:

```sh
#!/bin/sh
# Gera as fontes do painel em web/public/fonts a partir do repositório oficial
# do Google Fonts (github.com/google/fonts), num commit fixo, e confere cada
# arquivo baixado pelo hash do blob do Git antes de usá-lo.
# Precisa de curl, git e fonttools com brotli: no Mac, `brew install fonttools`;
# no Linux, `pip install 'fonttools[woff]'`.
# Uso, na raiz do repositório: sh web/scripts/gerar-fontes.sh
set -eu

COMMIT=e9678a5385322c63730122ba464765baad83cfd8
BASE="https://raw.githubusercontent.com/google/fonts/$COMMIT/ofl"
# Subconjunto latino do Google Fonts, mais as setas U+2190-2193 ("← Painel",
# "Ver incidente →"), que o latino deles não traz inteiras.
UNICODES='U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2190-2193,U+2212,U+2215,U+FEFF,U+FFFD'
DESTINO=web/public/fonts
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# baixar <caminho no repositório> <arquivo local> <blob esperado>
baixar() {
  curl -fsSL -o "$TMP/$2" "$BASE/$1"
  blob=$(git hash-object "$TMP/$2")
  if [ "$blob" != "$3" ]; then
    echo "gerar-fontes: $1 não confere (blob $blob, esperado $3)" >&2
    exit 1
  fi
}

baixar 'fraunces/Fraunces%5BSOFT%2CWONK%2Copsz%2Cwght%5D.ttf' Fraunces.ttf 8210f9488d3c732359a9292dd09aca3f2bae830e
baixar 'fraunces/OFL.txt' OFL-Fraunces.txt 22fa7ce5733434d0c634f6e55116f410858e938c
baixar 'inter/Inter%5Bopsz%2Cwght%5D.ttf' Inter.ttf 047c92f6e2212473dc436020afed689527076d44
baixar 'inter/OFL.txt' OFL-Inter.txt 21f6aff961064c2e429f570995e446bcdd555422

# A Fraunces sai sem os eixos SOFT e WONK (fixos em 0, o desenho padrão): o
# painel não os usa, e o arquivo cai a menos da metade. Ficam opsz e wght.
fonttools varLib.instancer "$TMP/Fraunces.ttf" SOFT=0 WONK=0 -o "$TMP/Fraunces-fixa.ttf"

mkdir -p "$DESTINO"
pyftsubset "$TMP/Fraunces-fixa.ttf" --unicodes="$UNICODES" --layout-features='*' \
  --flavor=woff2 --output-file="$DESTINO/fraunces-latin.woff2"
pyftsubset "$TMP/Inter.ttf" --unicodes="$UNICODES" --layout-features='*' \
  --flavor=woff2 --output-file="$DESTINO/inter-latin.woff2"
cp "$TMP/OFL-Fraunces.txt" "$TMP/OFL-Inter.txt" "$DESTINO/"
ls -l "$DESTINO"
```

- [ ] **Step 4: Gerar as fontes e conferir**

Run (na raiz): `sh web/scripts/gerar-fontes.sh`
Expected: termina sem erro e lista quatro arquivos. Com fonttools 4.63.0:

```text
fraunces-latin.woff2   69392 bytes
inter-latin.woff2     105352 bytes
OFL-Fraunces.txt        4391 bytes
OFL-Inter.txt           4377 bytes
```

Confira cada arquivo:

```bash
shasum -a 256 web/public/fonts/*.woff2
# 988605abd7c07766bd874b3920920269364de510207cdf2cc8f7f7fbfbb0c42f  fraunces-latin.woff2
# 363d146c20b8a0158f7c668eb52fed3f2870bcce38474cce66c1e53435047b7b  inter-latin.woff2
# (outra versão do fonttools pode gerar bytes diferentes; o tamanho fica perto)
head -c 4 web/public/fonts/inter-latin.woff2; echo        # wOF2
fonttools ttx -t fvar -o - web/public/fonts/fraunces-latin.woff2 | grep AxisTag
# <AxisTag>opsz</AxisTag> e <AxisTag>wght</AxisTag>, sem SOFT nem WONK
fonttools ttx -t cmap -o - web/public/fonts/inter-latin.woff2 | grep -c 'code="0x2192"'
# 2 (a Inter tem a seta →; a Fraunces não tem, e a seta cai na família genérica)
```

Se o blob de algum arquivo baixado não conferir, o script para: não troque o hash, investigue (o commit fixo não muda; um blob diferente é download corrompido ou interceptado).

- [ ] **Step 5: Declarar as fontes no `index.css`**

Em `web/src/index.css`, logo depois da linha `@custom-variant dark (&:where(.dark, .dark *));`, acrescente:

```css
/* Fontes auto-hospedadas (spec do redesign, "Decisões técnicas"): woff2
   variável, subconjunto latino, licença OFL ao lado. public/ não ganha hash,
   então a URL é estável e serve também à página pública. O swap mostra o
   texto na fonte do sistema até a fonte chegar. scripts/gerar-fontes.sh
   refaz os arquivos; src/fontes.test.ts confere. */
@font-face {
  font-family: 'Fraunces';
  src: url('/fonts/fraunces-latin.woff2') format('woff2');
  font-weight: 100 900;
  font-style: normal;
  font-display: swap;
  unicode-range: U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2190-2193, U+2212, U+2215, U+FEFF, U+FFFD;
}

@font-face {
  font-family: 'Inter';
  src: url('/fonts/inter-latin.woff2') format('woff2');
  font-weight: 100 900;
  font-style: normal;
  font-display: swap;
  unicode-range: U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2190-2193, U+2212, U+2215, U+FEFF, U+FFFD;
}
```

No bloco `@theme inline`, apague a linha `--font-sans: 'Helvetica Neue', Helvetica, Arial, sans-serif;`. Logo depois do fim do bloco `@theme inline { … }`, acrescente:

```css
/* static: as variáveis saem sempre no CSS, mesmo as que só o CSS deste
   arquivo usa (o base dos títulos, o main > section). */
@theme static {
  --font-sans: 'Inter', ui-sans-serif, system-ui, sans-serif;
  --font-display: 'Fraunces', ui-serif, Georgia, serif;
}
```

Na regra `body { … }`, apague a linha `font-family: var(--font-sans);`: o preflight do Tailwind já põe `--font-sans` no `<html>`.

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/fontes.test.ts`
Expected: PASS (11 testes).

Sabotagem (veja falhar e desfaça): troque `U+2190-2193` por `U+2191` no `@font-face` da Inter → "Inter cobre o latim e as setas" falha. Troque `swap` por `block` → o teste do `font-display` falha.

- [ ] **Step 7: Fixar no Go que `/fonts/…` sai com o tipo certo e revalida**

Em `internal/webui/webui_test.go`, no mapa de `distDeTeste()`, acrescente a entrada:

```go
		"fonts/inter-latin.woff2":         {Data: []byte("wOF2")},
```

e, na tabela de `TestRoteamentoDoPainel`, logo depois do caso `"script do tema revalida"`:

```go
		{"fonte fora de assets: woff2, revalida (URL estável)", "GET", "/fonts/inter-latin.woff2", 200, "font/woff2", "no-cache", false},
```

Run: `gofmt -w internal/webui/webui_test.go && go test ./internal/webui/ -run TestRoteamentoDoPainel -v`
Expected: PASS. É um teste de caracterização: o comportamento já existe. Sabotagem: em `internal/webui/webui.go`, no `case path.Ext(nome) != "":`, troque `cacheRevalida` por `cacheImutavel` → o caso novo falha (e o do `favicon.svg`). Desfaça.

- [ ] **Step 8: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 9: Commit**

```bash
git add web/scripts/gerar-fontes.sh web/public/fonts web/src/fontes.test.ts web/src/index.css internal/webui/webui_test.go
git commit -m "feat(painel): fontes Fraunces e Inter auto-hospedadas, com licença OFL"
```

### Task 2: Tokens em OKLCH e teste de contraste novo

**Files:**
- Modify: `web/src/index.css` (blocos `:root`, `.dark`, `@theme inline`; regras `html` e `html.dark` saem)
- Modify (reescrita): `web/src/tokens.test.ts`
- Modify: `web/e2e/a11y.spec.ts:136`, `web/e2e/fluxo.spec.ts:148`

**Interfaces:**
- Consumes: nada.
- Produces: as variáveis CSS e as cores do Tailwind da tabela [Tokens](#tokens-de-cor), inclusive as novas `apagado`, `primaria-100`, `perigo-texto`, `no-ar-100`, `degradado-100`, `fora-100`, `sem-dados-100`, `sobreposicao`; as variáveis `--sombra-nivel-1` e `--sombra-nivel-2`.

- [ ] **Step 1: Reescrever o teste**

Substitua todo o `web/src/tokens.test.ts` por:

```ts
/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Contraste dos tokens de cor, lidos do próprio index.css, nos dois temas
// (spec do redesign, seção Testes). Os tokens são OKLCH: o teste converte para
// sRGB e aplica a fórmula do WCAG 2, sem dependência nova. O axe confere texto
// renderizado, mas não vê o limite de um campo nem uma barra de gráfico
// (WCAG 1.4.11): este teste cobre os dois lados.
const css = readFileSync('src/index.css', 'utf8')

type Tema = 'claro' | 'escuro'
type Oklch = { l: number; c: number; h: number }

function bloco(seletor: string): string {
  const inicio = css.indexOf(`${seletor} {`)
  if (inicio === -1) throw new Error(`bloco "${seletor} {" ausente no index.css`)
  return css.slice(inicio, css.indexOf('}', inicio))
}

function declaracoes(seletor: string): Map<string, string> {
  return new Map(Array.from(bloco(seletor).matchAll(/--([\w-]+):\s*([^;]+);/g), (m) => [m[1] ?? '', (m[2] ?? '').trim()]))
}

// O .dark fica no mesmo <html> que o :root: o que ele não redefine vem do
// :root, e um var() resolve com os valores do tema em vigor.
const TOKENS: Record<Tema, Map<string, string>> = {
  claro: declaracoes(':root'),
  escuro: new Map([...declaracoes(':root'), ...declaracoes('.dark')]),
}

const OKLCH = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$/
const VAR = /^var\(--([\w-]+)\)$/

function cor(tema: Tema, nome: string, caminho: string[] = []): Oklch {
  if (caminho.includes(nome)) throw new Error(`ciclo de var(): --${[...caminho, nome].join(' → --')}`)
  const valor = TOKENS[tema].get(nome)
  if (valor === undefined) throw new Error(`token --${nome} ausente no tema ${tema}`)
  const referencia = VAR.exec(valor)
  if (referencia) return cor(tema, referencia[1] ?? '', [...caminho, nome])
  const m = OKLCH.exec(valor)
  if (!m) throw new Error(`--${nome} no tema ${tema} não é oklch(L C H) nem var(--x): ${valor}`)
  return { l: Number(m[1]), c: Number(m[2]), h: Number(m[3]) }
}

// OKLCH → OKLab → sRGB linear, com as matrizes de Björn Ottosson ("A
// perceptual color space for image processing", 2020), as mesmas do CSS
// Color 4. Um canal fora do gamut sRGB é cortado em [0, 1].
function srgbLinear({ l, c, h }: Oklch): [number, number, number] {
  const angulo = (h * Math.PI) / 180
  const a = c * Math.cos(angulo)
  const b = c * Math.sin(angulo)
  const longo = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const medio = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const curto = (l - 0.0894841775 * a - 1.291485548 * b) ** 3
  const cortar = (x: number) => Math.min(1, Math.max(0, x))
  return [
    cortar(4.0767416621 * longo - 3.3077115913 * medio + 0.2309699292 * curto),
    cortar(-1.2684380046 * longo + 2.6097574011 * medio - 0.3413193965 * curto),
    cortar(-0.0041960863 * longo - 0.7034186147 * medio + 1.707614701 * curto),
  ]
}

// Luminância relativa do WCAG 2; o sRGB aqui já está linear.
function luminancia(c: Oklch): number {
  const [r, g, b] = srgbLinear(c)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

// Fórmula do WCAG 2 (definição de contrast ratio).
function contraste(a: Oklch, b: Oklch): number {
  const [la, lb] = [luminancia(a), luminancia(b)]
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

const FUNDOS = ['fundo', 'superficie', 'painel']
// Texto e ícones: AA de texto (4,5:1) sobre todo fundo do painel.
const TEXTOS = ['texto', 'suave', 'apagado', 'primaria', 'realce', 'perigo', 'estado-no-ar', 'estado-degradado', 'estado-fora', 'estado-sem-dados']
// Texto sobre fundo tintado: o selo de estado sobre o tom 100, o item ativo
// e o selo de reconhecido sobre a primária 100, os botões sólidos, o erro do
// formulário.
const SOBRE_TINTA: [string, string][] = [
  ['estado-no-ar', 'estado-no-ar-100'],
  ['estado-degradado', 'estado-degradado-100'],
  ['estado-fora', 'estado-fora-100'],
  ['estado-sem-dados', 'estado-sem-dados-100'],
  ['perigo', 'estado-fora-100'],
  ['primaria', 'primaria-100'],
  ['texto', 'primaria-100'],
  ['primaria-texto', 'primaria'],
  ['perigo-texto', 'perigo'],
]
// Componentes de UI (1.4.11, 3:1): o limite dos controles, o anel de foco
// (também sobre o item ativo da navegação) e o fundo do botão primário.
const COMPONENTES: [string, string][] = [
  ...['borda-controle', 'foco', 'primaria'].flatMap((c) => FUNDOS.map((f): [string, string] => [c, f])),
  ['foco', 'primaria-100'],
]

describe.each(['claro', 'escuro'] as const)('contraste dos tokens, tema %s', (tema) => {
  const par = (a: string, b: string) => contraste(cor(tema, a), cor(tema, b))

  it.each(TEXTOS.flatMap((t) => FUNDOS.map((f) => [t, f])))('texto --%s sobre --%s chega a 4,5:1', (t, f) => {
    expect(par(t, f)).toBeGreaterThanOrEqual(4.5)
  })

  it.each(SOBRE_TINTA)('texto --%s sobre --%s chega a 4,5:1', (t, f) => {
    expect(par(t, f)).toBeGreaterThanOrEqual(4.5)
  })

  it.each(COMPONENTES)('componente --%s sobre --%s chega a 3:1', (c, f) => {
    expect(par(c, f)).toBeGreaterThanOrEqual(3)
  })
})

describe('tokens do index.css', () => {
  it('as cores dos dois temas são OKLCH, sem hex', () => {
    expect(bloco(':root')).not.toMatch(/#[0-9a-f]{3,8}\b/i)
    expect(bloco('.dark')).not.toMatch(/#[0-9a-f]{3,8}\b/i)
  })

  // Cada cor do Tailwind (bg-fora-100, text-apagado…) aponta para um token
  // que existe: um nome errado no @theme inline daria uma classe sem cor.
  it('todo var() do @theme inline aponta para um token do :root', () => {
    const referencias = Array.from(bloco('@theme inline').matchAll(/var\(--([\w-]+)\)/g), (m) => m[1] ?? '')
    expect(referencias.length).toBeGreaterThan(0)
    expect(referencias.filter((r) => !TOKENS.claro.has(r))).toEqual([])
  })
})

describe('a conversão e a fórmula do teste', () => {
  // Valores de referência: preto × branco = 21:1; #767676, o cinza mais
  // claro com 4,5:1 sobre branco (4,54:1), é oklch(0.5658 0 0); o vermelho
  // sRGB puro, oklch(0.62796 0.25768 29.2339), tem luminância 0,2126.
  it('confere com os valores de referência', () => {
    const branco = { l: 1, c: 0, h: 0 }
    const cinza = { l: 0.5658, c: 0, h: 0 }
    expect(contraste({ l: 0, c: 0, h: 0 }, branco)).toBeCloseTo(21, 5)
    expect(contraste(cinza, branco)).toBeCloseTo(4.54, 2)
    expect(contraste(branco, cinza)).toBeCloseTo(4.54, 2)
    expect(luminancia({ l: 0.62796, c: 0.25768, h: 29.2339 })).toBeCloseTo(0.2126, 3)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/tokens.test.ts`
Expected: FAIL. Os pares lançam `--texto no tema claro não é oklch(L C H) nem var(--x): #272c27`; "sem hex" falha; `--apagado` ausente. Só "a conversão e a fórmula" passa.

- [ ] **Step 3: Trocar os tokens no `index.css`**

Em `web/src/index.css`, substitua o comentário que começa em `/* Tokens do painel.` e os blocos `:root { … }` e `.dark { … }` inteiros por:

```css
/* Tokens do painel em OKLCH (spec do redesign, "Sistema > Cor"). Os nomes
   semânticos de antes ficam; entram o texto apagado, os tons 100 dos
   estados e da primária, o texto sobre o perigo, a sobreposição e as duas
   sombras. --borda é divisória decorativa; --borda-controle é o limite de
   campos e botões (WCAG 1.4.11). src/tokens.test.ts converte para sRGB e
   confere o contraste de cada par (4,5:1 texto, 3:1 componente e foco) nos
   dois temas: um par reprovado ajusta a luminosidade, nunca o matiz. O
   apagado do claro saiu de 0.58 (spec) para 0.53, que passa sobre o fundo. */
:root {
  color-scheme: light;
  --fundo: oklch(0.98 0.008 85);
  --superficie: oklch(1 0 0);
  --painel: oklch(0.95 0.01 80);
  --texto: oklch(0.22 0.015 60);
  --suave: oklch(0.45 0.015 60);
  --apagado: oklch(0.53 0.012 60);
  --borda: oklch(0.9 0.01 80);
  --borda-controle: oklch(0.62 0.015 60);
  --primaria: oklch(0.47 0.15 270);
  --primaria-texto: oklch(1 0 0);
  --primaria-100: oklch(0.95 0.03 270);
  --realce: var(--primaria);
  --foco: var(--primaria);
  --perigo: var(--estado-fora);
  --perigo-texto: oklch(1 0 0);
  --estado-no-ar: oklch(0.48 0.11 150);
  --estado-no-ar-100: oklch(0.96 0.03 150);
  --estado-degradado: oklch(0.52 0.12 70);
  --estado-degradado-100: oklch(0.97 0.035 85);
  --estado-fora: oklch(0.5 0.17 30);
  --estado-fora-100: oklch(0.96 0.025 30);
  --estado-sem-dados: var(--suave);
  --estado-sem-dados-100: var(--painel);
  --grafico-linha: var(--primaria);
  --grafico-faixa: var(--primaria-100);
  --grafico-uptime: oklch(0.88 0.05 150);
  --sobreposicao: oklch(0.22 0.015 60 / 0.6);
  /* Duas camadas (DI 9): uma grande e suave, outra pequena e escura. */
  --sombra-nivel-1: 0 1px 3px oklch(0.22 0.015 60 / 0.12), 0 1px 2px oklch(0.22 0.015 60 / 0.08);
  --sombra-nivel-2: 0 12px 24px oklch(0.22 0.015 60 / 0.14), 0 4px 8px oklch(0.22 0.015 60 / 0.08);
}

/* O escuro não usa sombra: a superfície mais clara que o fundo faz o papel,
   e o painel (trilho, área rebaixada) fica mais escuro que ele. A sombra é
   "0 0 transparent", não "none": o Tailwind compõe sombras numa lista, onde
   none invalida a declaração. */
.dark {
  color-scheme: dark;
  --fundo: oklch(0.2 0.01 60);
  --superficie: oklch(0.25 0.01 60);
  --painel: oklch(0.16 0.01 60);
  --texto: oklch(0.93 0.01 80);
  --suave: oklch(0.75 0.012 70);
  --apagado: oklch(0.65 0.012 70);
  --borda: oklch(0.32 0.01 60);
  --borda-controle: oklch(0.55 0.012 70);
  --primaria: oklch(0.76 0.11 270);
  --primaria-texto: oklch(0.2 0.03 270);
  --primaria-100: oklch(0.3 0.05 270);
  --perigo-texto: oklch(0.2 0.03 30);
  --estado-no-ar: oklch(0.78 0.13 150);
  --estado-no-ar-100: oklch(0.3 0.05 150);
  --estado-degradado: oklch(0.82 0.13 80);
  --estado-degradado-100: oklch(0.31 0.05 80);
  --estado-fora: oklch(0.72 0.17 30);
  --estado-fora-100: oklch(0.3 0.06 30);
  --estado-sem-dados-100: oklch(0.3 0.01 60);
  --grafico-uptime: oklch(0.4 0.06 150);
  --sobreposicao: oklch(0 0 0 / 0.6);
  --sombra-nivel-1: 0 0 transparent;
  --sombra-nivel-2: 0 0 transparent;
}
```

Substitua o bloco `@theme inline { … }` (que, depois da Task 1, já não tem o `--font-sans`) por:

```css
@theme inline {
  --color-fundo: var(--fundo);
  --color-superficie: var(--superficie);
  --color-painel: var(--painel);
  --color-texto: var(--texto);
  --color-suave: var(--suave);
  --color-apagado: var(--apagado);
  --color-borda: var(--borda);
  --color-borda-controle: var(--borda-controle);
  --color-primaria: var(--primaria);
  --color-primaria-texto: var(--primaria-texto);
  --color-primaria-100: var(--primaria-100);
  --color-realce: var(--realce);
  --color-foco: var(--foco);
  --color-perigo: var(--perigo);
  --color-perigo-texto: var(--perigo-texto);
  --color-no-ar: var(--estado-no-ar);
  --color-no-ar-100: var(--estado-no-ar-100);
  --color-degradado: var(--estado-degradado);
  --color-degradado-100: var(--estado-degradado-100);
  --color-fora: var(--estado-fora);
  --color-fora-100: var(--estado-fora-100);
  --color-sem-dados: var(--estado-sem-dados);
  --color-sem-dados-100: var(--estado-sem-dados-100);
  --color-sobreposicao: var(--sobreposicao);
}
```

Apague as regras `html { color-scheme: light; }` e `html.dark { color-scheme: dark; }`: o `color-scheme` agora está nos blocos de tokens.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/tokens.test.ts`
Expected: PASS (101 testes: 49 por tema, 2 do CSS, 1 da fórmula). Os valores foram conferidos antes de escrever o plano; o menor par é a borda de controle sobre o painel do claro (3,16:1).

Sabotagem 1 (o valor da spec): troque `--apagado: oklch(0.53 0.012 60);` por `oklch(0.58 0.012 60)` → falham "texto --apagado sobre --fundo/--superficie/--painel" do tema claro (4,05; 4,30; 3,71). Desfaça.
Sabotagem 2: troque `--color-fora-100: var(--estado-fora-100);` por `var(--estado-fora-10)` → falha "todo var() do @theme inline". Desfaça.

- [ ] **Step 5: A cor computada no e2e pode vir em `oklch(…)`**

O Chromium devolve `getComputedStyle(e).fill` no espaço de cor do valor, então um `var(--grafico-uptime)` em OKLCH sai `oklch(…)`, não `rgb(…)`.

Em `web/e2e/a11y.spec.ts`, linha 136, troque:

```ts
        expect(await barra.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)
```

por:

```ts
        expect(await barra.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^(rgb|oklch)\(/)
```

Em `web/e2e/fluxo.spec.ts`, linha 148, troque:

```ts
    expect(await marca.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)
```

por:

```ts
    expect(await marca.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^(rgb|oklch)\(/)
```

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde. Nenhum teste de tela depende de cor.

- [ ] **Step 7: Commit**

```bash
git add web/src/index.css web/src/tokens.test.ts web/e2e/a11y.spec.ts web/e2e/fluxo.spec.ts
git commit -m "feat(painel): tokens em OKLCH com índigo e teste de contraste convertendo para sRGB"
```

### Task 3: Escala, raio, sombras, títulos e o teste do sistema

**Files:**
- Modify: `web/src/index.css` (bloco `@theme static`, utilitários novos, base dos títulos, `body`)
- Modify: `web/src/lib/utils.ts`
- Create: `web/src/lib/utils.test.ts`
- Modify: `web/src/components/Titulo.tsx:37`
- Create: `web/src/sistema.test.ts`

**Interfaces:**
- Consumes: `--sombra-nivel-1`, `--sombra-nivel-2` (Task 2); `--font-display` (Task 1).
- Produces: `text-meta` (13 px) e `text-3xl` (28 px); `rounded-padrao` (8 px); utilitários `sombra-nivel-1`, `sombra-nivel-2`, `rotulo-de-grupo`; `h1`, `h2`, `h3` em Fraunces 600 por padrão (camada base); `tabular-nums` no `body`; `cn` que entende `text-meta` e `rounded-padrao`; a lista `ARQUIVOS_NO_SISTEMA` em `src/sistema.test.ts`.

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/lib/utils.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { cn } from './utils'

// O tailwind-merge só conhece a escala padrão do Tailwind. Review Focus 5:
// sem a extensão, "text-meta" passa por cor e some ao lado de "text-suave".
describe('cn com as classes do sistema', () => {
  it('text-meta é tamanho de fonte: convive com uma cor de texto', () => {
    expect(cn('text-meta', 'text-suave')).toBe('text-meta text-suave')
  })

  it('text-meta substitui outro tamanho', () => {
    expect(cn('text-sm', 'text-meta')).toBe('text-meta')
  })

  it('rounded-padrao e rounded-full são o mesmo grupo: vence o último', () => {
    expect(cn('rounded-padrao', 'rounded-full')).toBe('rounded-full')
  })
})
```

Crie `web/src/sistema.test.ts`:

```ts
/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Fitness function do sistema visual do redesign (DI 2 a 4): espaçamento só
// na escala 4/8/12/16/24/32/48/64 (classes 1, 2, 3, 4, 6, 8, 12, 16), um raio
// (rounded-padrao) e pílulas (rounded-full), pesos 400 e 600, sombras só de
// dois níveis (sombra-nivel-1 e -2) e cor só por token. Um arquivo entra na
// lista quando o PR que o redesenha chega; o PR 8 troca a lista por todo o src.
const ARQUIVOS_NO_SISTEMA = ['src/components/Titulo.tsx']

const ESCALA = new Set(['0', 'auto', '1', '2', '3', '4', '6', '8', '12', '16'])
const ESPACO = String.raw`(?:p[xytrblse]?|m[xytrblse]?|gap(?:-[xy])?|space-[xy])`
const CORES_DO_TAILWIND =
  'black|white|slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'

type Regra = { nome: string; padrao: RegExp; aceita?: (achado: RegExpExecArray) => boolean }

const REGRAS: Regra[] = [
  {
    nome: 'espaçamento fora da escala',
    // O valor começa por minúscula ou dígito: as classes do Tailwind nunca
    // começam por maiúscula, e assim o locale 'pt-BR' não vira "pt-BR" de espaço.
    padrao: new RegExp(String.raw`(?<![\w-])-?${ESPACO}-([a-z\d.][\w.]*)(?![\w./-])`, 'g'),
    aceita: (a) => ESCALA.has(a[1] ?? ''),
  },
  {
    nome: 'valor arbitrário de espaço, fonte, raio, sombra ou cor',
    padrao: new RegExp(
      String.raw`(?<![\w-])-?(?:${ESPACO}|text|rounded(?:-[a-z]{1,2})?|shadow|bg|border|ring|outline|fill|stroke|accent)-\[[^\]]*\]`,
      'g',
    ),
  },
  {
    nome: 'raio fora do sistema',
    padrao: /(?<![\w-])rounded(?:-\w+)*(?![\w-])/g,
    aceita: (a) => a[0] === 'rounded-padrao' || a[0] === 'rounded-full',
  },
  { nome: 'peso fora de 400 e 600', padrao: /(?<![\w-])font-(?:thin|extralight|light|medium|bold|extrabold|black)(?![\w-])/g },
  { nome: 'sombra do Tailwind', padrao: /(?<![\w-])shadow(?:-\w+)?(?![\w-])/g },
  {
    nome: 'cor fora dos tokens',
    padrao: new RegExp(String.raw`(?<![\w-])(?:bg|text|border|ring|outline|fill|stroke|accent)-(?:${CORES_DO_TAILWIND})(?![\w-])`, 'g'),
  },
  { nome: 'tamanho de fonte fora da escala', padrao: /(?<![\w-])text-[5-9]xl(?![\w-])/g },
]

function violacoes(codigo: string): string[] {
  return REGRAS.flatMap((r) =>
    Array.from(codigo.matchAll(r.padrao))
      .filter((achado) => !r.aceita?.(achado))
      .map((achado) => `${r.nome}: ${achado[0]}`),
  )
}

describe('o detector do sistema', () => {
  it.each(['p-5', 'space-y-1.5', 'py-0.5', 'text-[0.65rem]', 'px-[13px]', 'rounded-md', 'focus:rounded', 'font-bold', 'font-medium', 'shadow-lg', 'shadow', 'bg-black/60', 'text-6xl'])(
    'reprova %s',
    (classe) => {
      expect(violacoes(`className="${classe}"`)).not.toEqual([])
    },
  )

  it.each(['p-4', 'gap-6', 'mx-auto', '-mt-2', 'lg:py-12', 'rounded-padrao', 'rounded-full', 'font-semibold', 'sombra-nivel-1', 'bg-fora-100', 'text-3xl', 'min-h-11', 'size-11', 'max-w-[100rem]'])(
    'aceita %s',
    (classe) => {
      expect(violacoes(`className="${classe}"`)).toEqual([])
    },
  )

  it("aceita o locale 'pt-BR' fora de classe", () => {
    expect(violacoes(`new Intl.DateTimeFormat('pt-BR', { hour: '2-digit' })`)).toEqual([])
  })
})

describe.each(ARQUIVOS_NO_SISTEMA)('%s', (arquivo) => {
  it('segue o sistema visual (escala, raio, pesos, sombras e tokens)', () => {
    expect(violacoes(readFileSync(arquivo, 'utf8'))).toEqual([])
  })
})

// O contrato do index.css que os PRs seguintes usam.
describe('o index.css publica a escala e os utilitários', () => {
  const css = readFileSync('src/index.css', 'utf8')
  it.each([
    '--text-meta: 0.8125rem;',
    '--text-3xl: 1.75rem;',
    '--radius-padrao: 0.5rem;',
    '@utility sombra-nivel-1 {',
    '@utility sombra-nivel-2 {',
    '@utility rotulo-de-grupo {',
  ])('%s', (trecho) => {
    expect(css).toContain(trecho)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/lib/utils.test.ts src/sistema.test.ts`
Expected: FAIL. `cn('text-meta', 'text-suave')` devolve `"text-suave"`; `src/components/Titulo.tsx` reprova com `peso fora de 400 e 600: font-bold`; os seis trechos do `index.css` faltam. O detector passa (ele é o controle do teste).

- [ ] **Step 3: Estender o `cn`**

Substitua `web/src/lib/utils.ts` por:

```ts
import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// O tailwind-merge só conhece a escala padrão do Tailwind. Sem isto, o
// "text-meta" (13 px, do index.css) passa por cor e some ao lado de
// "text-suave", e o "rounded-padrao" não substitui outro raio.
const mesclar = extendTailwindMerge({ extend: { theme: { text: ['meta'], radius: ['padrao'] } } })

export function cn(...classes: ClassValue[]) {
  return mesclar(clsx(classes))
}
```

- [ ] **Step 4: Escala, raio, sombras e títulos no `index.css`**

Substitua o bloco `@theme static { … }` da Task 1 por:

```css
/* static: as variáveis saem sempre no CSS, mesmo as que só o CSS deste
   arquivo usa (o base dos títulos, o main > section). Escala de fonte da
   spec: 12, 13, 14, 16, 18, 20, 24, 28, 36 px. O padrão do Tailwind já tem
   12 (xs), 14 (sm), 16 (base), 18 (lg), 20 (xl), 24 (2xl) e 36 (4xl); entram
   o 13 (meta) e o 28 no lugar do 30 do text-3xl. Um raio só: 8 px. */
@theme static {
  --font-sans: 'Inter', ui-sans-serif, system-ui, sans-serif;
  --font-display: 'Fraunces', ui-serif, Georgia, serif;
  --text-meta: 0.8125rem;
  --text-meta--line-height: 1.125rem;
  --text-3xl: 1.75rem;
  --text-3xl--line-height: 2.25rem;
  --radius-padrao: 0.5rem;
}

/* Sombras de dois níveis (DI 9): 1 para cartão e item ativo, 2 para diálogo,
   popover e aviso. No escuro as duas são transparentes. */
@utility sombra-nivel-1 {
  box-shadow: var(--sombra-nivel-1);
}
@utility sombra-nivel-2 {
  box-shadow: var(--sombra-nivel-2);
}

/* Caixa alta só em rótulo de grupo pequeno, com 0,05em (DI 6). */
@utility rotulo-de-grupo {
  font-size: var(--text-xs);
  line-height: var(--text-xs--line-height);
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

/* Títulos em Fraunces 600. Na camada base, para uma classe da tela poder
   mudar tamanho, peso ou família (um KPI dentro de um título usa font-sans). */
@layer base {
  h1,
  h2,
  h3 {
    font-family: var(--font-display);
    font-weight: 600;
  }
}
```

Na regra `body { … }`, acrescente a linha:

```css
  font-variant-numeric: tabular-nums;
```

com um comentário logo acima da regra: `/* Números sempre em cifras tabulares (spec, Tipografia): colunas de latência e uptime alinham. */`.

Apague a regra antiga, sem camada, que dava peso 700 aos títulos:

```css
/* A identidade editorial usa a mesma família em toda a interface. */
h1, h2, h3 {
  font-weight: 700;
  letter-spacing: -0.025em;
}
```

- [ ] **Step 5: `h1` em Fraunces 600, 28/36 px**

Em `web/src/components/Titulo.tsx`, linha 37, troque o `className` do `<h1>`:

```tsx
    <h1 ref={ref} tabIndex={-1} aria-describedby={descritoPor} className="font-display text-3xl font-semibold outline-none sm:text-4xl">
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/lib/utils.test.ts src/sistema.test.ts src/components/titulo.test.tsx`
Expected: PASS.

Sabotagens: volte o `utils.ts` ao `twMerge` puro → os dois primeiros testes do `cn` falham; ponha `font-bold` de volta no `Titulo` → o teste do arquivo falha. Desfaça.

- [ ] **Step 7: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: tudo verde; o build gera `internal/webui/dist/fonts/*.woff2` (`ls ../internal/webui/dist/fonts`).

- [ ] **Step 8: Commit**

```bash
git add web/src/index.css web/src/lib/utils.ts web/src/lib/utils.test.ts web/src/components/Titulo.tsx web/src/sistema.test.ts
git commit -m "feat(painel): escala de fonte, raio único, sombras de dois níveis e teste do sistema visual"
```

### Task 4: Botão em pirâmide e o vermelho só na confirmação

**Files:**
- Modify: `web/src/components/ui/botao.tsx` (arquivo inteiro)
- Create: `web/src/components/ui/botao.test.tsx`
- Modify: `web/src/components/ui/dialogo.test.tsx` (teste novo do `Confirmar`)
- Modify: `web/src/sistema.test.ts` (lista e contagem do perigo)
- Modify (migração): `web/src/features/alvos/FormularioAlvo.tsx:183`, `web/src/features/alvos/PaginaDetalhe.tsx:231`, `web/src/features/canais/PaginaCanais.tsx:273`, `web/src/features/statuspage/PaginaStatusPage.tsx:177,184,190`, `web/src/features/login/PaginaLogin.tsx:210,215`, `web/src/features/configuracoes/PaginaConfiguracoes.tsx:702,876`
- Modify: `web/e2e/a11y.spec.ts` (comentário do teste de alvos de toque)

**Interfaces:**
- Consumes: `cn` (Task 3); cores `primaria`, `primaria-texto`, `primaria-100`, `perigo`, `perigo-texto`, `borda-controle`, `superficie`, `painel` (Task 2); `rounded-padrao`, `text-meta` (Task 3).
- Produces:

```ts
// web/src/components/ui/botao.tsx
export type TipoDeBotao = 'primario' | 'secundario' | 'terciario' | 'perigo'
export type TamanhoDeBotao = 'normal' | 'pequeno' | 'icone'
export type PropsDoBotao = ComponentProps<'button'> & { tipo?: TipoDeBotao; tamanho?: TamanhoDeBotao; asChild?: boolean }
export function Botao(props: PropsDoBotao)
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/components/ui/botao.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Botao } from './botao'

describe('Botao', () => {
  it('é type="button" por padrão, para não enviar o formulário em volta', () => {
    render(<Botao>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveAttribute('type', 'button')
  })

  // Pirâmide (DI 4): primário sólido, secundário com contorno, terciário só
  // em texto; o perigo é o vermelho sólido da confirmação.
  it.each([
    ['primario', 'bg-primaria'],
    ['secundario', 'border-borda-controle'],
    ['terciario', 'text-primaria'],
    ['perigo', 'bg-perigo'],
  ] as const)('o tipo %s pinta com %s', (tipo, classe) => {
    render(<Botao tipo={tipo}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveClass(classe)
  })

  it.each(['primario', 'secundario', 'terciario'] as const)('o tipo %s não usa o vermelho', (tipo) => {
    render(<Botao tipo={tipo}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' }).className).not.toMatch(/perigo/)
  })

  it.each(['normal', 'pequeno', 'icone'] as const)('o tamanho %s tem 44 px de altura mínima', (tamanho) => {
    render(<Botao tamanho={tamanho}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveClass('min-h-11')
  })

  it('o tamanho icone é um quadrado de 44 px', () => {
    render(<Botao tamanho="icone" aria-label="Tema: Sistema" />)
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveClass('size-11')
  })

  it('asChild passa o estilo ao filho e não põe type', () => {
    render(
      <Botao asChild tipo="secundario">
        <a href="/channels">Configurar canais</a>
      </Botao>,
    )
    const link = screen.getByRole('link', { name: 'Configurar canais' })
    expect(link).toHaveClass('border-borda-controle')
    expect(link).not.toHaveAttribute('type')
  })

  it('não aceita mais o tipo fantasma (o tsc -b confere)', () => {
    // @ts-expect-error o "fantasma" virou "terciario" no redesign
    const elemento = <Botao tipo="fantasma">Ação</Botao>
    expect(elemento).toBeTruthy()
  })
})
```

No fim de `web/src/components/ui/dialogo.test.tsx`, acrescente (e inclua `within` e `Confirmar` nos imports existentes):

```tsx
// DI 4: na confirmação, a ação destrutiva é a primária e só ali fica
// vermelha. Antes dela, Apagar e Encerrar são terciários.
describe('Confirmar', () => {
  it('o botão da ação é o vermelho sólido; o Cancelar, secundário', async () => {
    render(
      <Confirmar aberto aoMudar={() => {}} titulo="Apagar o alvo?" descricao="Ele some do painel." acao="Apagar" aoConfirmar={() => {}} />,
    )
    const dialogo = await screen.findByRole('alertdialog', { name: 'Apagar o alvo?' })
    expect(within(dialogo).getByRole('button', { name: 'Apagar' })).toHaveClass('bg-perigo')
    expect(within(dialogo).getByRole('button', { name: 'Cancelar' })).toHaveClass('border-borda-controle')
  })
})
```

Os imports do topo de `dialogo.test.tsx` ficam:

```tsx
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { Confirmar, Dialogo } from './dialogo'
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/ui/botao.tsx'` à lista:

```ts
const ARQUIVOS_NO_SISTEMA = ['src/components/Titulo.tsx', 'src/components/ui/botao.tsx']
```

e, no fim do arquivo, o teste do vermelho (Review Focus 3), com o import de `readdirSync`:

```ts
import { readdirSync, readFileSync } from 'node:fs'
```

```ts
// Review Focus 3 (auditoria C1, G2): o vermelho sólido é só do botão que
// confirma uma ação destrutiva. Cada uso de tipo="perigo" está contado por
// arquivo; um "Apagar" vermelho numa linha de lista muda a contagem. Os dois
// usos: o Confirmar do diálogo e o envio do Desativar 2FA, que já é o passo
// de confirmação, dentro do diálogo.
describe('vermelho sólido só na confirmação', () => {
  const PERIGO = /tipo="perigo"|tipo=\{[^}]*'perigo'/g
  const USOS_PERMITIDOS = {
    'src/components/ui/dialogo.tsx': 1,
    'src/features/configuracoes/PaginaConfiguracoes.tsx': 1,
  }

  it('tipo="perigo" aparece só nos diálogos de confirmação', () => {
    const usos: Record<string, number> = {}
    for (const nome of readdirSync('src', { recursive: true, encoding: 'utf8' })) {
      if (!nome.endsWith('.tsx') || nome.includes('.test.')) continue
      const arquivo = `src/${nome}`
      const total = (readFileSync(arquivo, 'utf8').match(PERIGO) ?? []).length
      if (total > 0) usos[arquivo] = total
    }
    expect(usos).toEqual(USOS_PERMITIDOS)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/ui/botao.test.tsx src/components/ui/dialogo.test.tsx src/sistema.test.ts`
Expected: FAIL. `terciario` não tem classe; o `perigo` de hoje é contorno, sem `bg-perigo`; o `pequeno` tem `min-h-10`; `botao.tsx` reprova (`rounded-md`, `font-bold`, `py-1.5`); a contagem acha também `PaginaDetalhe.tsx`, `PaginaCanais.tsx` e 3 usos em `PaginaConfiguracoes.tsx`.
Run: `cd web && npm run typecheck`
Expected: FAIL (`Unused '@ts-expect-error' directive` e `"terciario"` fora do tipo).

- [ ] **Step 3: Reescrever o `Botao`**

Substitua `web/src/components/ui/botao.tsx` por:

```tsx
import { cva } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

// Pirâmide de botões (DI 4, spec do redesign): uma ação primária sólida por
// tela, secundárias com contorno, terciárias só em texto. O vermelho sólido
// (perigo) é só do botão que confirma uma ação destrutiva num diálogo; antes
// da confirmação, Apagar, Encerrar e Desativar são terciários
// (src/sistema.test.ts conta os usos). Todo tamanho tem 44 px de altura
// (alvo de toque, WCAG 2.5.5).
export type TipoDeBotao = 'primario' | 'secundario' | 'terciario' | 'perigo'
export type TamanhoDeBotao = 'normal' | 'pequeno' | 'icone'

const variantes = cva(
  'inline-flex min-h-11 items-center justify-center gap-2 rounded-padrao font-semibold whitespace-nowrap disabled:pointer-events-none disabled:opacity-60 aria-disabled:opacity-60',
  {
    variants: {
      tipo: {
        primario: 'bg-primaria text-primaria-texto hover:bg-primaria/90',
        secundario: 'border border-borda-controle bg-superficie text-texto hover:bg-painel',
        terciario: 'text-primaria hover:bg-primaria-100',
        perigo: 'bg-perigo text-perigo-texto hover:bg-perigo/90',
      } satisfies Record<TipoDeBotao, string>,
      tamanho: {
        normal: 'px-4 text-sm',
        pequeno: 'px-3 text-meta',
        icone: 'size-11 p-0',
      } satisfies Record<TamanhoDeBotao, string>,
    },
    defaultVariants: { tipo: 'primario', tamanho: 'normal' },
  },
)

export type PropsDoBotao = ComponentProps<'button'> & { tipo?: TipoDeBotao; tamanho?: TamanhoDeBotao; asChild?: boolean }

export function Botao({ className, tipo, tamanho, asChild = false, type, ...props }: PropsDoBotao) {
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

- [ ] **Step 4: Migrar as chamadas**

Troque exatamente estes atributos (o resto da linha fica):

| Arquivo:linha | De | Para |
|---|---|---|
| `features/alvos/FormularioAlvo.tsx:183` ("Remover" o header) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/statuspage/PaginaStatusPage.tsx:177` (Subir) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/statuspage/PaginaStatusPage.tsx:184` (Descer) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/statuspage/PaginaStatusPage.tsx:190` (Tirar) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/login/PaginaLogin.tsx:210` (código de recuperação) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/login/PaginaLogin.tsx:215` (Voltar) | `tipo="fantasma"` | `tipo="terciario"` |
| `features/alvos/PaginaDetalhe.tsx:231` (Apagar) | `tipo="perigo"` | `tipo="terciario"` |
| `features/canais/PaginaCanais.tsx:273` (Apagar) | `tipo="perigo"` | `tipo="terciario"` |
| `features/configuracoes/PaginaConfiguracoes.tsx:702` (Encerrar) | `tipo="perigo"` | `tipo="terciario"` |
| `features/configuracoes/PaginaConfiguracoes.tsx:876` (Desativar/Ativar 2FA) | `tipo={eu.totp_enabled ? 'perigo' : 'primario'}` | `tipo={eu.totp_enabled ? 'terciario' : 'primario'}` |

Ficam como estão: o `Confirmar` de `components/ui/dialogo.tsx` e o `tipo="perigo"` de `PaginaConfiguracoes.tsx:499` (`DesativarTotp`, o envio do Desativar dentro do diálogo do 2FA).

Confira: `cd web && grep -rnE "fantasma|tipo=\"perigo\"|'perigo' :" src --include='*.tsx' | grep -v '\.test\.'`
Expected: só duas linhas, `src/components/ui/dialogo.tsx` (o `Confirmar`) e `src/features/configuracoes/PaginaConfiguracoes.tsx:499`.

- [ ] **Step 5: Atualizar o comentário do e2e de alvos de toque**

Em `web/e2e/a11y.spec.ts`, no teste `alvos de toque de 44 px`, troque o comentário:

```ts
    // Os botões do tamanho "pequeno" (min-h-10 sozinho dava 40 px): o
    // cabeçalho em toda tela, o formulário de alvo, os canais e a status
    // page. Sabotagem: tirar o alvo-de-toque do tamanho pequeno do Botao, ou
    // o min-h-11 do seletor de tema.
```

por:

```ts
    // Os botões do tamanho "pequeno": o cabeçalho em toda tela, o formulário
    // de alvo, os canais e a status page. Sabotagem: tirar o min-h-11 da base
    // do Botao (components/ui/botao.tsx), ou o do seletor de tema.
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/ui/botao.test.tsx src/components/ui/dialogo.test.tsx src/sistema.test.ts && npm run typecheck`
Expected: PASS e `tsc` sem erro.

Sabotagens: volte o "Apagar" de `PaginaCanais.tsx:273` para `tipo="perigo"` → "tipo=\"perigo\" aparece só nos diálogos" falha mostrando o arquivo. Tire `min-h-11` da base do `cva` → os três testes de tamanho falham. Desfaça.

- [ ] **Step 7: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde. Os testes de tela procuram botões por papel e nome, que não mudaram.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/ui/botao.tsx web/src/components/ui/botao.test.tsx web/src/components/ui/dialogo.test.tsx web/src/sistema.test.ts web/src/features web/e2e/a11y.spec.ts
git commit -m "feat(painel): botões em pirâmide e vermelho sólido só na confirmação"
```

### Task 5: Campo de 44 px e caixa de marcar

**Files:**
- Modify: `web/src/components/ui/campo.tsx` (arquivo inteiro)
- Create: `web/src/components/ui/campo.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)
- Modify (migração): `web/src/features/alvos/FormularioAlvo.tsx:140-143`, `web/src/features/statuspage/PaginaStatusPage.tsx:157-160`, `web/src/features/configuracoes/PaginaConfiguracoes.tsx:342-345`

**Interfaces:**
- Consumes: `cn`, `rounded-padrao`, `apagado`, `perigo`, `fora-100`, `borda-controle`.
- Produces:

```ts
// web/src/components/ui/campo.tsx
export const estiloDeEntrada: string
export type AcessibilidadeDoCampo = { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }
export function Campo(props: { rotulo: ReactNode; ajuda?: ReactNode; erro?: string; children: (a11y: AcessibilidadeDoCampo) => ReactNode })
export function Entrada(props: ComponentProps<'input'>)
export function Selecao(props: ComponentProps<'select'>)
export function CaixaDeMarcar(props: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode })
export function ErroDoFormulario(props: { children?: ReactNode })
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/components/ui/campo.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { describe, expect, it } from 'vitest'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Selecao } from './campo'

describe('Campo', () => {
  it('liga o rótulo, a ajuda e o erro ao controle', () => {
    render(
      <Campo rotulo="Nome" ajuda="Como aparece no painel." erro="Obrigatório.">
        {(a) => <Entrada {...a} />}
      </Campo>,
    )
    const entrada = screen.getByRole('textbox', { name: 'Nome' })
    expect(entrada).toHaveAccessibleDescription('Como aparece no painel. Obrigatório.')
    expect(entrada).toBeInvalid()
  })

  it('sem erro, o controle é válido e só a ajuda o descreve', () => {
    render(
      <Campo rotulo="Nome" ajuda="Como aparece no painel.">
        {(a) => <Entrada {...a} />}
      </Campo>,
    )
    const entrada = screen.getByRole('textbox', { name: 'Nome' })
    expect(entrada).toHaveAccessibleDescription('Como aparece no painel.')
    expect(entrada).not.toBeInvalid()
  })

  // 44 px (alvo de toque) e 16 px de fonte no celular: abaixo disso o
  // Safari do iPhone amplia a página ao focar o campo.
  it('a entrada e a seleção têm 44 px e 16 px de fonte no celular', () => {
    render(
      <>
        <Entrada aria-label="URL" />
        <Selecao aria-label="Método">
          <option>GET</option>
        </Selecao>
      </>,
    )
    for (const controle of [screen.getByRole('textbox', { name: 'URL' }), screen.getByRole('combobox', { name: 'Método' })]) {
      expect(controle).toHaveClass('min-h-11', 'text-base', 'border-borda-controle')
    }
  })
})

describe('CaixaDeMarcar', () => {
  it('é um checkbox com o próprio rótulo, que alterna ao clicar no texto', async () => {
    render(<CaixaDeMarcar>Ativo (sondar este alvo)</CaixaDeMarcar>)
    const caixa = screen.getByRole('checkbox', { name: 'Ativo (sondar este alvo)' })
    await userEvent.click(screen.getByText('Ativo (sondar este alvo)'))
    expect(caixa).toBeChecked()
  })

  it('repassa ref e atributos ao input, como o register do react-hook-form pede', () => {
    const ref = createRef<HTMLInputElement>()
    render(
      <CaixaDeMarcar ref={ref} name="enabled" defaultChecked>
        Ativo
      </CaixaDeMarcar>,
    )
    const caixa = screen.getByRole('checkbox', { name: 'Ativo' })
    expect(ref.current).toBe(caixa)
    expect(caixa).toHaveAttribute('name', 'enabled')
    expect(caixa).toBeChecked()
  })

  it('a linha inteira é a área de toque, com 44 px', () => {
    render(<CaixaDeMarcar>Guardei os códigos</CaixaDeMarcar>)
    expect(screen.getByRole('checkbox', { name: 'Guardei os códigos' }).closest('label')).toHaveClass('min-h-11')
  })
})

describe('ErroDoFormulario', () => {
  it('anuncia o erro do formulário e some sem texto', () => {
    const { rerender } = render(<ErroDoFormulario>Senha ou código incorretos.</ErroDoFormulario>)
    expect(screen.getByRole('alert')).toHaveTextContent('Senha ou código incorretos.')
    rerender(<ErroDoFormulario />)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/ui/campo.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/ui/campo.test.tsx src/sistema.test.ts`
Expected: FAIL. `CaixaDeMarcar` não existe (`is not a function` ou elemento inválido); falta `text-base`; `campo.tsx` reprova (`rounded-lg`, `rounded-md`, `py-2.5`, `space-y-1.5`, `font-medium`). Os testes de ligação do `Campo` passam: são de caracterização. Sabotagem para vê-los falhar: em `Campo`, troque `descrito` por `undefined` no `aria-describedby` → os dois primeiros falham. Desfaça.

- [ ] **Step 3: Reescrever o `campo.tsx`**

Substitua `web/src/components/ui/campo.tsx` por:

```tsx
import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// 44 px de altura (alvo de toque) e 16 px de fonte no celular: abaixo disso o
// Safari do iPhone amplia a página ao focar o campo. A borda é a de controle
// (3:1); o foco é o anel índigo de 3 px do :focus-visible global.
export const estiloDeEntrada =
  'block min-h-11 w-full min-w-0 rounded-padrao border border-borda-controle bg-superficie px-3 py-2 text-base text-texto placeholder:text-apagado aria-invalid:border-perigo sm:text-sm'

export type AcessibilidadeDoCampo = { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }

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
  children: (a11y: AcessibilidadeDoCampo) => ReactNode
}) {
  const id = useId()
  const idAjuda = `${id}-ajuda`
  const idErro = `${id}-erro`
  const descrito = [ajuda ? idAjuda : null, erro ? idErro : null].filter(Boolean).join(' ') || undefined
  return (
    <div className="min-w-0 space-y-2">
      <label htmlFor={id} className="block text-sm font-semibold">
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

// Checkbox com o rótulo na mesma linha, que inteira é a área de toque (44 px).
// A cor da marca vem do accent-color global do index.css (DI 12). className
// vai para a linha; o resto das props, ao input (o register do
// react-hook-form passa name, ref e eventos).
export function CaixaDeMarcar({
  children,
  className,
  ...props
}: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode }) {
  return (
    <label className={cn('flex min-h-11 items-center gap-3 text-sm', className)}>
      <input type="checkbox" className="size-5 shrink-0" {...props} />
      {children}
    </label>
  )
}

// Erro do formulário inteiro (não de um campo): anunciado assim que aparece.
export function ErroDoFormulario({ children }: { children?: ReactNode }) {
  if (!children) return null
  return (
    <p role="alert" className="rounded-padrao border border-perigo bg-fora-100 p-3 text-sm text-perigo">
      {children}
    </p>
  )
}
```

- [ ] **Step 4: Migrar as três caixas de marcar**

Acrescente `CaixaDeMarcar` ao import de `'@/components/ui/campo'` em cada arquivo e troque o bloco `<label className="flex items-center gap-2 text-sm"> … </label>`:

`web/src/features/alvos/FormularioAlvo.tsx:140-143` vira:

```tsx
      <CaixaDeMarcar {...f.register('enabled')}>Ativo (sondar este alvo)</CaixaDeMarcar>
```

`web/src/features/statuspage/PaginaStatusPage.tsx:157-160` vira:

```tsx
      <CaixaDeMarcar {...f.register('published')}>Publicada (visível para qualquer pessoa com o link)</CaixaDeMarcar>
```

`web/src/features/configuracoes/PaginaConfiguracoes.tsx:342-345` vira:

```tsx
      <CaixaDeMarcar checked={guardei} onChange={(e) => setGuardei(e.target.checked)}>
        Guardei os códigos
      </CaixaDeMarcar>
```

Confira: `cd web && grep -rn 'type="checkbox"' src --include='*.tsx' | grep -v '\.test\.'`
Expected: só `src/components/ui/campo.tsx`.

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/ui/campo.test.tsx src/sistema.test.ts src/features/alvos src/features/statuspage src/features/configuracoes`
Expected: PASS. Os testes das telas acham as caixas pelo rótulo, que não mudou.

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/ui/campo.tsx web/src/components/ui/campo.test.tsx web/src/sistema.test.ts web/src/features
git commit -m "feat(painel): campo de 44 px e caixa de marcar com a linha inteira de toque"
```

### Task 6: Selo de estado em pílula com fundo 100

> **Ajuste aprovado pelo dono (R1.1 do [backlog de refatoração](../reports/2026-10-14-backlog-refatoracao.md), Onda 1):**
> `EstadoExibido`, `estadoExibido` e `textoDoEstado` são regra de domínio, sem visual, e moram em
> `web/src/lib/estado.ts`. O `SeloDeEstado.tsx` só os importa (sem reexportar), e os consumidores
> (`painel/transicoes.ts`, `painel/PaginaPainel.tsx`, `alvos/PaginaDetalhe.tsx`,
> `statuspage/PaginaStatusPage.tsx`) importam de `@/lib/estado`.

**Files:**
- Create: `web/src/lib/estado.ts`, `web/src/lib/estado.test.ts`
- Modify: `web/src/components/SeloDeEstado.tsx` (arquivo inteiro)
- Create: `web/src/components/selo.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)
- Modify (imports): `web/src/features/painel/transicoes.ts`, `web/src/features/painel/PaginaPainel.tsx`, `web/src/features/alvos/PaginaDetalhe.tsx`, `web/src/features/statuspage/PaginaStatusPage.tsx`

**Interfaces:**
- Consumes: cores `no-ar`, `no-ar-100`, `degradado`, `degradado-100`, `fora`, `fora-100`, `sem-dados`, `sem-dados-100`, `primaria`, `primaria-100`; `text-meta`; `cn` estendido (Task 3: sem ele o `text-meta` some ao lado da cor).
- Produces:

```ts
// web/src/lib/estado.ts
export type EstadoExibido = EstadoAlvo | 'pausado'
export function textoDoEstado(e: EstadoExibido): string
export function estadoExibido(estado: EstadoAlvo, ativo: boolean): EstadoExibido

// web/src/components/SeloDeEstado.tsx
export type TomDoSelo = 'no-ar' | 'degradado' | 'fora' | 'sem-dados' | 'primaria'
export function SeloDeEstado(props: { estado: EstadoExibido; className?: string })
export type PropsDoSelo = { tom: TomDoSelo; Icone: LucideIcon; cheio?: boolean; children: ReactNode; className?: string }
export function Selo(props: PropsDoSelo)
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/components/selo.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { Check } from 'lucide-react'
import { describe, expect, it } from 'vitest'
import { Selo, SeloDeEstado } from './SeloDeEstado'

// Forma + cor + texto (spec, Componentes base): pílula com o fundo 100 do
// estado, texto na cor dele e um ícone Lucide de forma própria.
describe('SeloDeEstado', () => {
  it.each([
    ['up', 'No ar', 'bg-no-ar-100', 'text-no-ar'],
    ['degraded', 'Degradado', 'bg-degradado-100', 'text-degradado'],
    ['down', 'Fora do ar', 'bg-fora-100', 'text-fora'],
    ['unknown', 'Sem dados', 'bg-sem-dados-100', 'text-sem-dados'],
    ['pausado', 'Pausado', 'bg-sem-dados-100', 'text-sem-dados'],
  ] as const)('%s: pílula "%s" com %s e %s', (estado, texto, fundo, cor) => {
    const { container } = render(<SeloDeEstado estado={estado} />)
    expect(screen.getByText(texto)).toHaveClass('rounded-full', 'text-meta', fundo, cor)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('cada estado tem um ícone de forma diferente, não só outra cor', () => {
    const { container } = render(
      <>
        {(['up', 'degraded', 'down', 'unknown', 'pausado'] as const).map((e) => (
          <SeloDeEstado key={e} estado={e} />
        ))}
      </>,
    )
    const formas = Array.from(container.querySelectorAll('svg'), (svg) => svg.getAttribute('class'))
    expect(new Set(formas).size).toBe(5)
  })
})

describe('Selo', () => {
  it('o tom primaria serve ao selo de reconhecido', () => {
    render(
      <Selo tom="primaria" Icone={Check}>
        Reconhecido
      </Selo>,
    )
    expect(screen.getByText('Reconhecido')).toHaveClass('rounded-full', 'bg-primaria-100', 'text-primaria')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/SeloDeEstado.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/selo.test.tsx src/sistema.test.ts`
Expected: FAIL. `Selo` não é exportado; o selo não tem `rounded-full` nem fundo; `SeloDeEstado.tsx` reprova (`gap-1.5`, `font-medium`). O teste das formas passa: é de caracterização. Sabotagem para vê-lo falhar: troque o ícone do `degraded` por `Circle` → 4 formas. Desfaça.

- [ ] **Step 3: Criar `lib/estado.ts` e reescrever o `SeloDeEstado.tsx`**

Crie `web/src/lib/estado.ts` com `EstadoExibido`, a tabela de textos (`No ar`, `Degradado`, `Fora do ar`, `Sem dados`, `Pausado`), `textoDoEstado` e `estadoExibido` (código antes no selo), e `web/src/lib/estado.test.ts` com uma tabela por função (ativo × inativo para os quatro estados; o texto de cada estado). Troque os imports dos quatro consumidores para `@/lib/estado`.

Substitua `web/src/components/SeloDeEstado.tsx` por:

```tsx
import { Circle, CircleDashed, Pause, Triangle, X, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { cn } from '@/lib/utils'

// Estado com forma, cor E texto, nunca só cor (spec §11.6 e spec do
// redesign): verde e vermelho são o par que a deuteranopia não separa, então
// os ícones também diferem.
export type TomDoSelo = 'no-ar' | 'degradado' | 'fora' | 'sem-dados' | 'primaria'

const selos: Record<EstadoExibido, { Icone: LucideIcon; tom: TomDoSelo; cheio: boolean }> = {
  up: { Icone: Circle, tom: 'no-ar', cheio: true },
  degraded: { Icone: Triangle, tom: 'degradado', cheio: true },
  down: { Icone: X, tom: 'fora', cheio: false },
  unknown: { Icone: CircleDashed, tom: 'sem-dados', cheio: false },
  pausado: { Icone: Pause, tom: 'sem-dados', cheio: false },
}

export function SeloDeEstado({ estado, className }: { estado: EstadoExibido; className?: string }) {
  const { Icone, tom, cheio } = selos[estado]
  return (
    <Selo tom={tom} Icone={Icone} cheio={cheio} className={className}>
      {textoDoEstado(estado)}
    </Selo>
  )
}

// Pílula com o fundo 100 do tom e o texto na cor dele (todo par tem 4,5:1,
// src/tokens.test.ts). O tom primaria é o do selo de reconhecido.
const TONS: Record<TomDoSelo, string> = {
  'no-ar': 'bg-no-ar-100 text-no-ar',
  degradado: 'bg-degradado-100 text-degradado',
  fora: 'bg-fora-100 text-fora',
  'sem-dados': 'bg-sem-dados-100 text-sem-dados',
  primaria: 'bg-primaria-100 text-primaria',
}

export type PropsDoSelo = { tom: TomDoSelo; Icone: LucideIcon; cheio?: boolean; children: ReactNode; className?: string }

export function Selo({ tom, Icone, cheio = false, children, className }: PropsDoSelo) {
  return (
    <span className={cn('inline-flex items-center gap-1 rounded-full px-2 py-1 text-meta font-semibold whitespace-nowrap', TONS[tom], className)}>
      <Icone aria-hidden="true" className="size-3.5 shrink-0" fill={cheio ? 'currentColor' : 'none'} strokeWidth={2.5} />
      {children}
    </span>
  )
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/selo.test.tsx src/sistema.test.ts src/features/painel src/features/alvos src/features/statuspage`
Expected: PASS. As telas procuram "No ar", "Fora do ar" etc. por texto, que não mudou.

Sabotagem: em `utils.ts`, volte ao `twMerge` puro → o selo perde `text-meta` e o primeiro `it.each` falha (a classe da cor apaga a do tamanho). Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/estado.ts web/src/lib/estado.test.ts web/src/components/SeloDeEstado.tsx web/src/components/selo.test.tsx web/src/sistema.test.ts web/src/features
git commit -m "feat(painel): selo de estado em pílula com o fundo 100 da cor"
```

### Task 7: Controle segmentado acessível

**Files:**
- Create: `web/src/components/ui/controle-segmentado.tsx`
- Create: `web/src/components/ui/controle-segmentado.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `cn`, `rounded-padrao`, `painel`, `superficie`, `suave`, `texto`, `sombra-nivel-1`.
- Produces:

```ts
// web/src/components/ui/controle-segmentado.tsx
export type OpcaoSegmentada<V extends string> = { readonly valor: V; readonly rotulo: string }
export type PropsDoControleSegmentado<V extends string> = {
  rotulo: string
  opcoes: readonly OpcaoSegmentada<V>[]
  valor: V
  aoMudar: (valor: V) => void
  className?: string
}
export function ControleSegmentado<V extends string>(props: PropsDoControleSegmentado<V>)
```

Comportamento publicado: `role="radiogroup"` com `aria-label={rotulo}`; cada opção é `<button role="radio" aria-checked>` com 44 × 44 px no mínimo; só o item marcado fica na ordem do Tab (roving tabindex); setas → próximo/anterior em volta, Home/End → pontas, e mover o foco também escolhe; clique no item marcado não chama `aoMudar`. O componente é controlado: a tela guarda o valor.

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/components/ui/controle-segmentado.test.tsx`:

```tsx
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ControleSegmentado } from './controle-segmentado'

const JANELAS = [
  { valor: '1h', rotulo: '1 h' },
  { valor: '6h', rotulo: '6 h' },
  { valor: '24h', rotulo: '24 h' },
] as const
type Janela = (typeof JANELAS)[number]['valor']

// O controle é controlado: este invólucro faz o papel da tela (a janela do
// gráfico, o filtro de incidentes), que guarda o valor.
function Tela({ inicial, aoMudar }: { inicial: Janela; aoMudar: (v: Janela) => void }) {
  const [valor, setValor] = useState<Janela>(inicial)
  return (
    <>
      <button type="button">Antes</button>
      <ControleSegmentado
        rotulo="Janela do gráfico"
        opcoes={JANELAS}
        valor={valor}
        aoMudar={(v) => {
          setValor(v)
          aoMudar(v)
        }}
      />
      <button type="button">Depois</button>
    </>
  )
}

function montar(inicial: Janela) {
  const aoMudar = vi.fn<(v: Janela) => void>()
  render(<Tela inicial={inicial} aoMudar={aoMudar} />)
  return { aoMudar, usuario: userEvent.setup() }
}

const radio = (nome: string) => screen.getByRole('radio', { name: nome })

describe('ControleSegmentado', () => {
  it('é um radiogroup com o nome dado, um radio por opção, só o valor atual marcado', () => {
    montar('6h')
    const grupo = screen.getByRole('radiogroup', { name: 'Janela do gráfico' })
    expect(within(grupo).getAllByRole('radio').map((r) => r.textContent)).toEqual(['1 h', '6 h', '24 h'])
    expect(radio('6 h')).toBeChecked()
    expect(radio('1 h')).not.toBeChecked()
    expect(radio('24 h')).not.toBeChecked()
  })

  it('o Tab entra no item marcado, e o Tab seguinte sai do grupo', async () => {
    const { usuario } = montar('6h')
    await usuario.click(screen.getByRole('button', { name: 'Antes' }))
    await usuario.tab()
    expect(radio('6 h')).toHaveFocus()
    await usuario.tab()
    expect(screen.getByRole('button', { name: 'Depois' })).toHaveFocus()
  })

  it('seta para a direita e para baixo escolhem o próximo e, no último, voltam ao primeiro', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('{ArrowRight}')
    expect(radio('24 h')).toHaveFocus()
    expect(radio('24 h')).toBeChecked()
    await usuario.keyboard('{ArrowDown}')
    expect(radio('1 h')).toHaveFocus()
    expect(radio('1 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['1h']])
  })

  it('seta para a esquerda e para cima escolhem o anterior e, no primeiro, vão ao último', async () => {
    const { aoMudar, usuario } = montar('1h')
    await usuario.click(radio('1 h'))
    await usuario.keyboard('{ArrowLeft}')
    expect(radio('24 h')).toHaveFocus()
    await usuario.keyboard('{ArrowUp}')
    expect(radio('6 h')).toHaveFocus()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['6h']])
  })

  it('Home e End vão às pontas', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('{End}')
    expect(radio('24 h')).toBeChecked()
    await usuario.keyboard('{Home}')
    expect(radio('1 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['1h']])
  })

  it('clicar escolhe; clicar no já marcado não avisa de novo', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('24 h'))
    await usuario.click(radio('24 h'))
    expect(radio('24 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h']])
  })

  it('outra tecla não mexe no valor nem no foco', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('a{Enter}')
    expect(radio('6 h')).toHaveFocus()
    expect(aoMudar).not.toHaveBeenCalled()
  })

  // Review Focus 2: uma URL editada à mão (?janela=abc) chega como valor fora
  // das opções. Nenhum item mente que está marcado, e o grupo continua
  // alcançável pelo Tab.
  it('com valor fora das opções, nada fica marcado e o primeiro item entra na ordem do Tab', async () => {
    const aoMudar = vi.fn<(v: string) => void>()
    render(
      <>
        <button type="button">Antes</button>
        <ControleSegmentado<string> rotulo="Janela do gráfico" opcoes={JANELAS} valor="abc" aoMudar={aoMudar} />
      </>,
    )
    expect(screen.getAllByRole('radio').filter((r) => r.getAttribute('aria-checked') === 'true')).toEqual([])
    const usuario = userEvent.setup()
    await usuario.click(screen.getByRole('button', { name: 'Antes' }))
    await usuario.tab()
    expect(radio('1 h')).toHaveFocus()
  })

  it('cada item é um alvo de toque de 44 × 44 px', () => {
    montar('6h')
    for (const r of screen.getAllByRole('radio')) expect(r).toHaveClass('min-h-11', 'min-w-11')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/ui/controle-segmentado.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/ui/controle-segmentado.test.tsx`
Expected: FAIL com `Failed to resolve import "./controle-segmentado"`.

- [ ] **Step 3: Implementar**

Crie `web/src/components/ui/controle-segmentado.tsx`:

```tsx
import { useRef, type KeyboardEvent } from 'react'
import { cn } from '@/lib/utils'

// Controle segmentado (spec do redesign, Componentes base): a janela do
// gráfico e o filtro de incidentes. Segue o padrão "radio group" do WAI-ARIA
// APG: só o item marcado fica na ordem do Tab, as setas andam em volta, Home
// e End vão às pontas, e mover o foco também escolhe. Implementação própria,
// e não o RadioGroup do Radix: no Radix, a escolha depende de a seta ainda
// estar pressionada quando o foco chega (num setTimeout), o que no jsdom não
// acontece.
export type OpcaoSegmentada<V extends string> = { readonly valor: V; readonly rotulo: string }

export type PropsDoControleSegmentado<V extends string> = {
  rotulo: string
  opcoes: readonly OpcaoSegmentada<V>[]
  valor: V
  aoMudar: (valor: V) => void
  className?: string
}

const DESTINO: Partial<Record<string, (atual: number, total: number) => number>> = {
  ArrowRight: (atual, total) => (atual + 1) % total,
  ArrowDown: (atual, total) => (atual + 1) % total,
  ArrowLeft: (atual, total) => (atual - 1 + total) % total,
  ArrowUp: (atual, total) => (atual - 1 + total) % total,
  Home: () => 0,
  End: (_atual, total) => total - 1,
}

export function ControleSegmentado<V extends string>({ rotulo, opcoes, valor, aoMudar, className }: PropsDoControleSegmentado<V>) {
  const itens = useRef<(HTMLButtonElement | null)[]>([])
  const marcado = opcoes.findIndex((o) => o.valor === valor)
  // Valor fora das opções (uma URL editada à mão): nada marcado, e o primeiro
  // item fica na ordem do Tab para o grupo continuar alcançável.
  const naOrdemDoTab = marcado === -1 ? 0 : marcado

  function escolherPeloTeclado(e: KeyboardEvent<HTMLButtonElement>, atual: number) {
    const destino = DESTINO[e.key]?.(atual, opcoes.length)
    const opcao = destino === undefined ? undefined : opcoes[destino]
    if (destino === undefined || !opcao) return
    e.preventDefault()
    itens.current[destino]?.focus()
    if (opcao.valor !== valor) aoMudar(opcao.valor)
  }

  return (
    <div role="radiogroup" aria-label={rotulo} className={cn('inline-flex flex-wrap gap-1 rounded-padrao bg-painel p-1', className)}>
      {opcoes.map((o, i) => {
        const ativo = i === marcado
        return (
          <button
            key={o.valor}
            ref={(el) => {
              itens.current[i] = el
            }}
            type="button"
            role="radio"
            aria-checked={ativo}
            tabIndex={i === naOrdemDoTab ? 0 : -1}
            onClick={() => {
              if (!ativo) aoMudar(o.valor)
            }}
            onKeyDown={(e) => escolherPeloTeclado(e, i)}
            className={cn(
              'min-h-11 min-w-11 rounded-padrao px-3 text-sm text-suave hover:text-texto',
              ativo && 'bg-superficie font-semibold text-texto sombra-nivel-1',
            )}
          >
            {o.rotulo}
          </button>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/ui/controle-segmentado.test.tsx src/sistema.test.ts`
Expected: PASS (9 testes do controle).

Sabotagens: troque `(atual + 1) % total` por `Math.min(atual + 1, total - 1)` → "no último, voltam ao primeiro" falha; troque `tabIndex={i === naOrdemDoTab ? 0 : -1}` por `tabIndex={0}` → "o Tab seguinte sai do grupo" falha; troque `marcado === -1 ? 0 : marcado` por `marcado` → o teste do valor fora das opções falha. Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ui/controle-segmentado.tsx web/src/components/ui/controle-segmentado.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): controle segmentado acessível como radiogroup"
```

### Task 8: Diálogo e avisos no sistema (raio, sombra nível 2, sobreposição)

**Files:**
- Modify: `web/src/components/ui/dialogo.tsx:10-12` (constantes `sobreposicao` e `caixa`)
- Modify: `web/src/components/ui/avisos.tsx:31` (classe do `Toast.Root`)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `bg-sobreposicao`, `rounded-padrao`, `sombra-nivel-2`.
- Produces: `Dialogo`, `Confirmar`, `Reserva`, `ProvedorDeAvisos` e `useAvisar` com as mesmas assinaturas de hoje.

- [ ] **Step 1: Pôr os dois arquivos no teste do sistema**

Em `web/src/sistema.test.ts`, a lista fica:

```ts
const ARQUIVOS_NO_SISTEMA = [
  'src/components/Titulo.tsx',
  'src/components/SeloDeEstado.tsx',
  'src/components/ui/avisos.tsx',
  'src/components/ui/botao.tsx',
  'src/components/ui/campo.tsx',
  'src/components/ui/controle-segmentado.tsx',
  'src/components/ui/dialogo.tsx',
]
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/sistema.test.ts`
Expected: FAIL. `dialogo.tsx`: `p-5`, `rounded-xl`, `shadow-lg`, `bg-black`; `avisos.tsx`: `rounded-xl`, `shadow`.

- [ ] **Step 3: Corrigir o diálogo**

Em `web/src/components/ui/dialogo.tsx`, troque as duas constantes:

```ts
const sobreposicao = 'fixed inset-0 z-40 bg-sobreposicao'
// inset-x-4 + mx-auto + max-w-lg centra a caixa com 16 px de folga no
// celular, sem valor arbitrário de largura.
const caixa =
  'fixed inset-x-4 top-1/2 z-50 mx-auto max-h-[90vh] max-w-lg -translate-y-1/2 space-y-4 overflow-y-auto rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-2 sm:p-6'
```

- [ ] **Step 4: Corrigir o aviso**

Em `web/src/components/ui/avisos.tsx`, linha 31, o `className` do `Toast.Root` fica:

```tsx
          className={`rounded-padrao border bg-superficie p-3 text-sm sombra-nivel-2 ${a.tipo === 'erro' ? 'border-perigo' : 'border-borda'}`}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/sistema.test.ts src/components/ui/dialogo.test.tsx src/main.test.tsx`
Expected: PASS. O `main.test.tsx` abre um diálogo de verdade e confere o estilo da trava de rolagem com o nonce.

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/ui/dialogo.tsx web/src/components/ui/avisos.tsx web/src/sistema.test.ts
git commit -m "feat(painel): diálogo e avisos com raio único, sombra nível 2 e sobreposição por token"
```

### Task 9: Verificação do PR 1 e abertura

**Files:** nenhum novo.

- [ ] **Step 1: Portões do front-end**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: tudo verde; `ls ../internal/webui/dist/fonts` lista os dois woff2 e as duas licenças.

- [ ] **Step 2: Portões do Go**

Run (na raiz, com o Docker de pé): `go build ./... && go vet ./... && make test`
Expected: tudo verde.

- [ ] **Step 3: e2e de acessibilidade, local**

Run: `cd web && npx playwright test e2e/a11y.spec.ts`
Expected: PASS (precisa do build do Step 1 e do Docker; o `globalSetup` sobe Postgres e binário). Se o axe acusar contraste num par que o `tokens.test.ts` não cobre, acrescente o par ao teste antes de corrigir o token (o par vira regressão).

- [ ] **Step 4: Nome antigo**

Run: `sh deploy/ci/checar-nome.sh`
Expected: sai com 0.

- [ ] **Step 5: Abrir o PR**

```bash
git push -u origin feat/redesign-fundacao
gh pr create --title "feat(painel): fundação do redesign (fontes, tokens OKLCH, componentes base)" --body "$(cat <<'EOF'
PR 1 de 8 do redesign do painel (direção C, "Editorial claro"). Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md.

- Fraunces e Inter auto-hospedadas em web/public/fonts (woff2 variável, subconjunto latino, OFL), geradas por web/scripts/gerar-fontes.sh.
- Tokens em OKLCH com índigo; tokens.test.ts converte para sRGB e confere 4,5:1 e 3:1 nos dois temas.
- Escala de fonte (13 e 28 px), raio único, sombras de dois níveis, títulos em Fraunces 600.
- Botão em pirâmide (vermelho sólido só na confirmação), campo de 44 px, caixa de marcar, selo em pílula, controle segmentado novo.
- sistema.test.ts reprova classe fora da escala nos arquivos redesenhados.

O visual de todas as telas muda (tokens globais); as telas ganham a estrutura nova nos PRs 2 a 6.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Antes do merge, o dono revê o visual: rode `cd web && npm run dev` com a API local e passe pelas telas nos dois temas.

---

# PR 2: Navegação

Branch: `feat/redesign-navegacao`, a partir da `main` com o PR 1 integrado.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-navegacao
```

### Task 10: Marca e botão de tema

**Files:**
- Create: `web/src/components/Marca.tsx`, `web/src/components/marca.test.tsx`
- Create: `web/src/app/BotaoDeTema.tsx`, `web/src/app/botao-de-tema.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `Botao` (`tipo="terciario"`, `tamanho="icone"`), `useTema`, `type Tema` de `@/lib/tema`, `font-display`, `text-xl`, `text-3xl`.
- Produces:

```ts
// web/src/components/Marca.tsx
export function Marca(props: { tamanho?: 'normal' | 'grande' })  // normal: 20 px no cabeçalho; grande: 28 px no login
// web/src/app/BotaoDeTema.tsx
export function BotaoDeTema()  // nome acessível "Tema: Sistema" | "Tema: Claro" | "Tema: Escuro"
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/components/marca.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Marca } from './Marca'

describe('Marca', () => {
  it('o nome é texto em Fraunces e o ponto índigo é decorativo', () => {
    const { container } = render(<Marca />)
    expect(screen.getByText('StillOn')).toHaveClass('font-display', 'text-xl')
    expect(container.querySelector('[aria-hidden="true"]')).toHaveClass('bg-primaria', 'rounded-full')
  })

  it('a grande, do login, tem 28 px', () => {
    render(<Marca tamanho="grande" />)
    expect(screen.getByText('StillOn')).toHaveClass('text-3xl')
  })
})
```

Crie `web/src/app/botao-de-tema.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProvedorDeTema } from '@/lib/tema'
import { BotaoDeTema } from './BotaoDeTema'

afterEach(() => document.documentElement.classList.remove('dark'))

// O jsdom do setup diz que o sistema é claro (matchMedia sem match).
function montar() {
  render(
    <ProvedorDeTema>
      <BotaoDeTema />
    </ProvedorDeTema>,
  )
  return userEvent.setup()
}

describe('BotaoDeTema', () => {
  it('diz o tema em vigor e, a cada clique, passa a Claro, a Escuro e de volta a Sistema', async () => {
    const usuario = montar()
    const botao = screen.getByRole('button', { name: 'Tema: Sistema' })
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Claro')
    expect(document.documentElement).not.toHaveClass('dark')
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Escuro')
    expect(document.documentElement).toHaveClass('dark')
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Sistema')
    expect(document.documentElement).not.toHaveClass('dark')
  })

  it('guarda a escolha, como o seletor antigo', async () => {
    const usuario = montar()
    await usuario.click(screen.getByRole('button', { name: 'Tema: Sistema' }))
    expect(localStorage.getItem('stillon.tema')).toBe('claro')
  })

  it('o title diz para qual tema o clique leva', () => {
    montar()
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveAttribute('title', 'Trocar para Claro')
  })

  it('sem storage, a troca ainda vale nesta aba', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage indisponível')
    })
    const usuario = montar()
    await usuario.click(screen.getByRole('button', { name: 'Tema: Sistema' }))
    expect(screen.getByRole('button', { name: 'Tema: Claro' })).toBeInTheDocument()
  })

  it('é um botão de ícone de 44 px', () => {
    montar()
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveClass('size-11')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/app/BotaoDeTema.tsx'` e `'src/components/Marca.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/marca.test.tsx src/app/botao-de-tema.test.tsx`
Expected: FAIL com `Failed to resolve import "./Marca"` e `"./BotaoDeTema"`.

- [ ] **Step 3: Implementar a `Marca`**

Crie `web/src/components/Marca.tsx`:

```tsx
import { cn } from '@/lib/utils'

// A marca do StillOn (spec do redesign, seções 1 e 5): ponto índigo e o nome
// em Fraunces. O ponto é decorativo; o nome é texto. Normal no cabeçalho
// (20 px), grande no login (28 px).
export function Marca({ tamanho = 'normal' }: { tamanho?: 'normal' | 'grande' }) {
  const grande = tamanho === 'grande'
  return (
    <span className="inline-flex items-center gap-2">
      <span aria-hidden="true" className={cn('shrink-0 rounded-full bg-primaria', grande ? 'size-4' : 'size-3')} />
      <span className={cn('font-display font-semibold text-texto', grande ? 'text-3xl' : 'text-xl')}>StillOn</span>
    </span>
  )
}
```

- [ ] **Step 4: Implementar o `BotaoDeTema`**

Crie `web/src/app/BotaoDeTema.tsx`:

```tsx
import { Monitor, Moon, Sun, type LucideIcon } from 'lucide-react'
import { Botao } from '@/components/ui/botao'
import { useTema, type Tema } from '@/lib/tema'

// O tema num botão de ícone (auditoria L5): cada clique passa ao próximo, na
// ordem Sistema → Claro → Escuro → Sistema. O nome acessível diz o tema em
// vigor; o title, o próximo.
const CICLO: Record<Tema, { rotulo: string; Icone: LucideIcon; proximo: Tema }> = {
  sistema: { rotulo: 'Sistema', Icone: Monitor, proximo: 'claro' },
  claro: { rotulo: 'Claro', Icone: Sun, proximo: 'escuro' },
  escuro: { rotulo: 'Escuro', Icone: Moon, proximo: 'sistema' },
}

export function BotaoDeTema() {
  const { tema, mudar } = useTema()
  const { rotulo, Icone, proximo } = CICLO[tema]
  return (
    <Botao
      tipo="terciario"
      tamanho="icone"
      aria-label={`Tema: ${rotulo}`}
      title={`Trocar para ${CICLO[proximo].rotulo}`}
      onClick={() => mudar(proximo)}
    >
      <Icone aria-hidden="true" className="size-5" />
    </Botao>
  )
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/marca.test.tsx src/app/botao-de-tema.test.tsx src/sistema.test.ts`
Expected: PASS.

Sabotagem: no `CICLO`, troque o `proximo` do `escuro` para `'claro'` → o primeiro teste falha no terceiro clique. Desfaça.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/Marca.tsx web/src/components/marca.test.tsx web/src/app/BotaoDeTema.tsx web/src/app/botao-de-tema.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): marca em Fraunces e tema em botão de ícone"
```

### Task 11: Navegação no topo e barra inferior no celular

**Files:**
- Modify: `web/src/app/Layout.tsx` (arquivo inteiro)
- Create: `web/src/app/layout.test.tsx`
- Modify: `web/src/main.test.tsx:79-85`
- Modify: `web/src/components/ui/avisos.tsx` (`Toast.Viewport`)
- Modify: `web/src/index.css` (sai a regra `.marca-simbolo`)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `Marca`, `BotaoDeTema` (Task 10); `Botao`; `useAbertos`, `useMe`; `useSessao`; `useAvisar`; `mensagemDe`.
- Produces: o `Layout` com `<header>` (role `banner`), um `<nav aria-label="Principal">` com 5 links (Painel `/`, Incidentes `/incidents`, Canais `/channels`, Status `/status-pages`, Conta `/settings`) e `<main id="conteudo">` com `mx-auto max-w-6xl space-y-8 px-4 pt-8 pb-16 sm:px-6 lg:pt-12 lg:pb-12`. As telas continuam renderizando dentro do `<Outlet />`.

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/app/layout.test.tsx`:

```tsx
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EMAIL } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'

describe('Layout', () => {
  // As mesmas cinco abas no topo (desktop) e na barra inferior (celular):
  // um <nav> só, com nomes que não mudam com a largura da tela.
  it('a navegação principal tem as cinco abas, na ordem, e marca a atual', async () => {
    await renderizarPainel('/channels')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(within(nav).getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual([
      '/',
      '/incidents',
      '/channels',
      '/status-pages',
      '/settings',
    ])
    for (const nome of ['Painel', 'Canais', 'Status', 'Conta']) {
      expect(within(nav).getByRole('link', { name: nome })).toBeInTheDocument()
    }
    expect(within(nav).getByRole('link', { name: 'Canais' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Painel' })).not.toHaveAttribute('aria-current')
  })

  it('cada aba é um alvo de toque de 44 px', async () => {
    await renderizarPainel('/')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    for (const aba of within(nav).getAllByRole('link')) expect(aba).toHaveClass('min-h-11')
  })

  it('o tema é um botão de ícone, sem o select antigo', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('button', { name: 'Tema: Sistema' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Tema' })).not.toBeInTheDocument()
  })

  it('o cabeçalho tem a marca, o e-mail da conta, o tema e o Sair', async () => {
    await renderizarPainel('/')
    const cabecalho = screen.getByRole('banner')
    expect(within(cabecalho).getByText('StillOn')).toBeInTheDocument()
    expect(await within(cabecalho).findByText(EMAIL)).toBeInTheDocument()
    expect(within(cabecalho).getByRole('button', { name: /^Tema:/ })).toBeInTheDocument()
    expect(within(cabecalho).getByRole('button', { name: 'Sair' })).toBeInTheDocument()
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/app/Layout.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/app/layout.test.tsx src/sistema.test.ts`
Expected: FAIL. Não há link "Status" nem "Conta" (hoje são "Status page" e "Configurações"); o tema é `combobox`; a aba tem `min-h-10`; `Layout.tsx` reprova (`ml-60`, `px-5`, `py-9`, `gap-10`, `text-[0.65rem]`, `rounded-md`, `font-bold`…).

- [ ] **Step 3: Reescrever o `Layout`**

Substitua `web/src/app/Layout.tsx` por:

```tsx
import { Bell, LayoutDashboard, LogOut, Settings, ShieldCheck, TriangleAlert, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { ProvedorDeAnuncios } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Marca } from '@/components/Marca'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { cn } from '@/lib/utils'
import { BotaoDeTema } from './BotaoDeTema'
import { useSessao } from './contexto'

// As cinco abas, com os mesmos nomes no topo (desktop) e na barra inferior
// (celular): o nome acessível não muda com a largura da tela (WCAG 3.2.4).
const ABAS: { para: string; texto: string; Icone: LucideIcon; fim?: boolean; contador?: boolean }[] = [
  { para: '/', texto: 'Painel', fim: true, Icone: LayoutDashboard },
  { para: '/incidents', texto: 'Incidentes', contador: true, Icone: TriangleAlert },
  { para: '/channels', texto: 'Canais', Icone: Bell },
  { para: '/status-pages', texto: 'Status', Icone: ShieldCheck },
  { para: '/settings', texto: 'Conta', Icone: Settings },
]

export function Layout() {
  const me = useMe()
  return (
    <ProvedorDeAnuncios>
      <a
        href="#conteudo"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-padrao focus:bg-superficie focus:p-2"
      >
        Pular para o conteúdo
      </a>
      <AvisoDeContexto />
      <header className="border-b border-borda bg-superficie">
        <div className="mx-auto flex min-h-16 max-w-6xl items-center gap-4 px-4 sm:px-6 lg:gap-8">
          <Marca />
          <NavegacaoPrincipal />
          <div className="ml-auto flex min-w-0 items-center gap-2">
            {/* O e-mail some no celular (auditoria L1); a conta está na aba Conta. */}
            {me.data && <span className="hidden max-w-64 truncate text-meta text-apagado lg:inline">{me.data.email}</span>}
            <BotaoDeTema />
            <BotaoSair />
          </div>
        </div>
      </header>
      {/* pb-16 no celular: o fim do conteúdo fica acima da barra inferior
          fixa (53 px). */}
      <main id="conteudo" className="mx-auto min-w-0 max-w-6xl space-y-8 px-4 pt-8 pb-16 sm:px-6 lg:pt-12 lg:pb-12">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}

// No celular a navegação é a barra inferior fixa, de 5 abas de 44 px
// (auditoria L1: antes, ~265 px de menu vinham antes do conteúdo); a partir
// de lg (1024 px) ela volta ao fluxo do cabeçalho. Um <nav> só nos dois
// casos: a ordem do Tab e o nome da região não mudam com a largura. O item
// ativo usa a primária 100 e o peso 600, e o aria-current do NavLink diz o
// mesmo ao leitor de tela.
function NavegacaoPrincipal() {
  const abertos = useAbertos().data
  const contador = abertos && abertos !== '0' ? abertos : null
  return (
    <nav
      aria-label="Principal"
      className="fixed inset-x-0 bottom-0 z-30 border-t border-borda bg-superficie px-2 py-1 lg:static lg:z-auto lg:border-t-0 lg:bg-transparent lg:p-0"
    >
      <ul className="grid grid-cols-5 gap-1 lg:flex">
        {ABAS.map((aba) => (
          <li key={aba.para}>
            <NavLink
              to={aba.para}
              end={aba.fim}
              aria-label={
                aba.contador && contador ? `${aba.texto}, ${contador} ${contador === '1' ? 'aberto' : 'abertos'}` : undefined
              }
              className={({ isActive }) =>
                cn(
                  'flex min-h-11 flex-col items-center justify-center gap-1 rounded-padrao px-2 text-xs text-suave hover:text-texto',
                  'lg:flex-row lg:gap-2 lg:px-3 lg:text-sm',
                  isActive && 'bg-primaria-100 font-semibold text-primaria hover:text-primaria',
                )
              }
            >
              <span className="relative">
                <aba.Icone aria-hidden="true" className="size-5 lg:size-4" />
                {aba.contador && contador && <ContadorDeAbertos n={contador} />}
              </span>
              {aba.texto}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}

// O número de incidentes abertos sobre o ícone de "Incidentes" (spec §4.9).
// Zero não aparece. O número é decorativo para o leitor de tela: o nome do
// link já diz "Incidentes, 2 abertos".
function ContadorDeAbertos({ n }: { n: string }) {
  return (
    <span aria-hidden="true" className="absolute -top-2 -right-3 rounded-full bg-perigo px-1 text-xs font-semibold text-perigo-texto">
      {n}
    </span>
  )
}

// Ocupado enquanto o logout voa (até o prazo da sessão): um segundo clique
// não manda outro. aria-disabled, e não disabled, para o foco não cair no
// <body>.
function BotaoSair() {
  const { sessao } = useSessao()
  const avisar = useAvisar()
  const [saindo, setSaindo] = useState(false)
  return (
    <Botao
      tipo="terciario"
      tamanho="pequeno"
      aria-disabled={saindo || undefined}
      onClick={async () => {
        if (saindo) return
        setSaindo(true)
        try {
          await sessao.sair()
        } catch (e) {
          // O servidor não fez o logout (A9 do Plano 10): a sessão
          // continua, e o Sair pode ser tentado de novo.
          avisar(`Não foi possível sair. ${mensagemDe(e)}`, 'erro')
        } finally {
          setSaindo(false)
        }
      }}
    >
      <LogOut aria-hidden="true" className="size-4" />
      Sair
    </Botao>
  )
}
```

- [ ] **Step 4: Avisos acima da barra inferior**

Em `web/src/components/ui/avisos.tsx`, o `Toast.Viewport` fica:

```tsx
      {/* bottom-16 no celular: o aviso fica acima da barra inferior fixa. */}
      <Toast.Viewport className="fixed right-4 bottom-16 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2 lg:bottom-4" />
```

- [ ] **Step 5: Tirar o CSS da marca antiga**

Em `web/src/index.css`, apague a regra:

```css
.marca-simbolo {
  border: 1px solid var(--primaria);
  box-shadow: inset 0 0 0 3px var(--superficie);
}
```

- [ ] **Step 6: Atualizar o `main.test.tsx`**

Em `web/src/main.test.tsx`, troque as linhas 79 a 85:

```tsx
  expect(screen.getByRole('combobox', { name: 'Tema' })).toHaveValue('escuro')
  expect(document.documentElement).toHaveClass('dark')

  const usuario = userEvent.setup()
  await usuario.selectOptions(screen.getByRole('combobox', { name: 'Tema' }), 'claro')
  expect(document.documentElement).not.toHaveClass('dark')
  expect(localStorage.getItem('stillon.tema')).toBe('claro')
```

por:

```tsx
  expect(screen.getByRole('button', { name: 'Tema: Escuro' })).toBeInTheDocument()
  expect(document.documentElement).toHaveClass('dark')

  // Do Escuro, o clique leva a Sistema; o sistema do jsdom é claro.
  const usuario = userEvent.setup()
  await usuario.click(screen.getByRole('button', { name: 'Tema: Escuro' }))
  expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toBeInTheDocument()
  expect(document.documentElement).not.toHaveClass('dark')
  expect(localStorage.getItem('stillon.tema')).toBe('sistema')
```

- [ ] **Step 7: Rodar e ver passar**

Run: `cd web && npx vitest run src/app src/main.test.tsx src/sistema.test.ts src/features/painel/painel.test.tsx`
Expected: PASS. O `painel.test.tsx` ("mostra quantos incidentes estão abertos") continua achando o link por "Incidentes" e "Incidentes, 2 abertos".

Sabotagens: troque o texto da aba `Conta` por `Configurações` → o primeiro teste do Layout falha; tire o `aria-label` do `NavLink` → o `painel.test.tsx` do contador falha. Desfaça.

- [ ] **Step 8: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: tudo verde.

- [ ] **Step 9: Commit**

```bash
git add web/src/app/Layout.tsx web/src/app/layout.test.tsx web/src/main.test.tsx web/src/components/ui/avisos.tsx web/src/index.css web/src/sistema.test.ts
git commit -m "feat(painel): navegação no topo no desktop e barra inferior de 5 abas no celular"
```

### Task 12: e2e da navegação, verificação do PR 2 e abertura

**Files:**
- Modify: `web/e2e/a11y.spec.ts` (teste do aviso de HTTPS; teste de alvos de toque; teste novo da barra inferior)
- Modify: `web/e2e/fluxo.spec.ts:171`, `web/e2e/troca.spec.ts:65`

**Interfaces:**
- Consumes: o `Layout` da Task 11 (`header`, `nav[aria-label="Principal"]`, `#conteudo`).
- Produces: nada para os PRs seguintes.

- [ ] **Step 1: O aviso de HTTPS fica acima do cabeçalho em toda largura**

Em `web/e2e/a11y.spec.ts`, troque o comentário e o nome do teste:

```ts
// P2 do shell: visibilidade CSS não detecta texto encoberto por uma
// lateral fixa. O hit-test e a geometria precisam enxergar o aviso inteiro.
test('aviso de HTTPS fica visível fora da lateral no desktop e mantém login e celular', async ({ page }) => {
```

por:

```ts
// P2 do shell, com a navegação no topo (PR 2 do redesign): o aviso vem
// antes do cabeçalho em toda largura. O hit-test e a geometria precisam
// enxergar o aviso inteiro, sem nada por cima.
test('aviso de HTTPS fica acima do cabeçalho em toda largura e mantém o login', async ({ page }) => {
```

e, dentro do laço `for (const largura of [360, 1024, 1280, 1600])`, troque:

```ts
    const lateral = await page.locator('header').boundingBox()
    expect(caixaTexto).not.toBeNull()
    expect(lateral).not.toBeNull()
    if (largura >= 1024) expect(caixaTexto!.x, `${largura}: título fora da lateral`).toBeGreaterThanOrEqual(lateral!.x + lateral!.width)
    else expect(caixaTexto!.y, 'celular: aviso antes do cabeçalho').toBeLessThan(lateral!.y)
```

por:

```ts
    const cabecalho = await page.locator('header').boundingBox()
    expect(caixaTexto).not.toBeNull()
    expect(cabecalho).not.toBeNull()
    expect(caixaTexto!.y, `${largura}: aviso antes do cabeçalho`).toBeLessThan(cabecalho!.y)
```

- [ ] **Step 2: Alvos de toque: tema em botão e as cinco abas**

No teste `alvos de toque de 44 px`, troque:

```ts
      ['seletor de tema', page.getByLabel('Tema')],
```

por:

```ts
      ['tema', page.getByRole('button', { name: /^Tema:/ })],
```

e, na primeira chamada `await conferir([` (a do `/`), acrescente as abas depois de `['Incidente aberto', …]`:

```ts
      ...['Painel', 'Incidentes', 'Canais', 'Status', 'Conta'].map((aba): [string, Locator] => [
        `aba ${aba}`,
        page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: new RegExp(`^${aba}`) }),
      ]),
```

- [ ] **Step 3: Review Focus 4, a barra inferior não cobre o fim do conteúdo**

No fim de `web/e2e/a11y.spec.ts`, acrescente:

```ts
// Review Focus 4 do redesign: no celular, a navegação é uma barra fixa no pé
// da tela. Rolado até o fim, o último bloco do conteúdo tem de ficar acima
// dela, ou o último botão de um formulário fica inalcançável ao toque.
// Sabotagem: tirar o pb-16 do <main> no Layout.
test('celular de 400 px: a barra inferior não cobre o fim do conteúdo', async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 700 })
  await imitarApi(page)
  for (const [rota, titulo] of ROTAS) {
    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    await esperarOConteudo(page, rota)
    const sobra = await page.evaluate(() => {
      window.scrollTo(0, document.documentElement.scrollHeight)
      const barra = document.querySelector('nav[aria-label="Principal"]')?.getBoundingClientRect()
      const ultimo = document.querySelector('#conteudo')?.lastElementChild?.getBoundingClientRect()
      return barra && ultimo ? barra.top - ultimo.bottom : null
    })
    expect(sobra, `${rota}: barra ou conteúdo não encontrados`).not.toBeNull()
    expect(sobra ?? -1, `${rota}: o fim do conteúdo fica acima da barra`).toBeGreaterThanOrEqual(0)
  }
  await controlePositivo(page, 'barra inferior')
})
```

- [ ] **Step 4: "Configurações" virou a aba "Conta"**

Em `web/e2e/fluxo.spec.ts:171` e `web/e2e/troca.spec.ts:65`, troque:

```ts
  await page.getByRole('link', { name: 'Configurações' }).click()
```

por (mantendo a indentação de cada arquivo):

```ts
  await page.getByRole('link', { name: 'Conta', exact: true }).click()
```

Confira: `cd web && grep -rn "name: 'Configurações'" e2e`
Expected: nenhuma linha que clique em link (os títulos `heading` "Configurações" ficam).

- [ ] **Step 5: Rodar o e2e**

Run: `cd web && npm run build && npx playwright test`
Expected: PASS em todos os specs (precisa do Docker). Rode a sabotagem do Step 3 (tire o `pb-16` do `<main>`, `npm run build`, `npx playwright test e2e/a11y.spec.ts -g "barra inferior"`): falha numa rota longa (o painel ou o formulário de alvo). Desfaça e rode de novo.

- [ ] **Step 6: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint` e, na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 7: Commit e PR**

```bash
git add web/e2e
git commit -m "test(painel): e2e da navegação nova (aviso acima do topo, abas de 44 px, barra sem cobrir o conteúdo)"
git push -u origin feat/redesign-navegacao
gh pr create --title "feat(painel): navegação no topo e barra inferior no celular" --body "$(cat <<'EOF'
PR 2 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md.

- Desktop: marca, as cinco abas no topo (item ativo em primária 100), e-mail, tema e Sair.
- Celular: barra inferior fixa de 5 abas de 44 px (Painel, Incidentes, Canais, Status, Conta); o conteúdo começa logo abaixo de um cabeçalho de 64 px (auditoria L1).
- Tema em botão de ícone que alterna Sistema, Claro e Escuro (L5).
- e2e: aviso de HTTPS acima do cabeçalho, abas no teste de 44 px, barra que não cobre o fim do conteúdo.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Testes de tela que mudam nestes dois PRs

| PR | Teste | O que acontece | Como fica |
|---|---|---|---|
| 1 | `web/src/tokens.test.ts` | Lia hex | Reescrito (Task 2) |
| 1 | `web/e2e/a11y.spec.ts:136`, `web/e2e/fluxo.spec.ts:148` | `fill` computado vem `oklch(…)` | Regex `/^(rgb\|oklch)\(/` (Task 2) |
| 1 | Testes de tela com botões e caixas de marcar | Nada: procuram por papel e nome, que não mudam | Sem mudança |
| 1 | `web/e2e/a11y.spec.ts`, comentário de alvos de toque | Citava o `alvo-de-toque` do `pequeno` | Novo comentário (Task 4) |
| 2 | `web/src/main.test.tsx` | O tema era `combobox` | Botão "Tema: Escuro" → "Tema: Sistema" (Task 11) |
| 2 | `web/src/features/painel/painel.test.tsx` (contador) | Nada: o nome "Incidentes, N abertos" fica | Sem mudança |
| 2 | `web/e2e/a11y.spec.ts`, aviso de HTTPS | Supunha lateral no desktop | Aviso acima do cabeçalho em toda largura (Task 12) |
| 2 | `web/e2e/a11y.spec.ts`, alvos de toque | `getByLabel('Tema')` | `getByRole('button', { name: /^Tema:/ })` + abas (Task 12) |
| 2 | `web/e2e/fluxo.spec.ts:171`, `web/e2e/troca.spec.ts:65` | Link "Configurações" | Link "Conta" (Task 12) |

## Autorrevisão

- **Cobertura da spec (PRs 1 e 2):** fontes com licença (Task 1); tokens OKLCH com nomes mantidos e os novos (Task 2); `tokens.test.ts` com os pares da seção Testes, inclusive borda de controle e foco a 3:1 nos dois temas (Task 2); escala e raio (Task 3); botão em pirâmide com 44 px e destrutivo terciário até a confirmação (Task 4); campo de 44 px com borda 3:1 e foco índigo, checkbox com `accent-color` (Task 5); selo com forma, cor e texto (Task 6); controle segmentado em radiogroup com 44 px e teclado (Task 7); sombras de dois níveis (Tasks 2, 3 e 8); navegação no topo, barra de 5 abas de 44 px e tema em botão de ícone (Tasks 10 a 12). Fica para os PRs seguintes o que a spec põe neles: a CSP da página pública (PR 7), as telas (PRs 3 a 6).
- **Placeholders:** nenhum "TBD", "implementar depois" nem "parecido com a Task N"; todo passo de código traz o código.
- **Tipos:** `TipoDeBotao`, `TamanhoDeBotao`, `TomDoSelo`, `OpcaoSegmentada<V>` e `PropsDoControleSegmentado<V>` têm o mesmo nome nas tasks e nas interfaces publicadas.
- **Review Focus:** cada uma das cinco linhas tem teste na task dona (1, 7, 4, 12, 3).

## Interfaces publicadas para os PRs seguintes

Tudo abaixo vale a partir do merge do PR indicado. Os PRs 3 a 8 consomem estes nomes; não criem sinônimos.

### Tokens de cor

Cada token é uma variável CSS no `:root` (claro) e no `.dark` (escuro), exposta ao Tailwind por `@theme inline` como cor: vale com qualquer utilitário de cor (`bg-`, `text-`, `border-`, `border-l-`, `ring-`, `fill-`, `stroke-`, `accent-`, `outline-`, `from-`…). PR 1.

| Variável CSS | Nome no Tailwind | Uso |
|---|---|---|
| `--fundo` | `fundo` | "Papel" da página |
| `--superficie` | `superficie` | Cartão, cabeçalho, diálogo, item ativo do segmentado |
| `--painel` | `painel` | Área rebaixada: trilho do segmentado, hover, aviso de contexto |
| `--texto` | `texto` | Texto principal |
| `--suave` | `suave` | Texto secundário |
| `--apagado` | `apagado` | Metadados (método, URL, intervalo, "Atualizado às…") |
| `--borda` | `borda` | Divisória decorativa |
| `--borda-controle` | `borda-controle` | Limite de campo e botão secundário (3:1) |
| `--primaria` | `primaria` | Botão primário, terciário, link, item ativo, linha do gráfico |
| `--primaria-texto` | `primaria-texto` | Texto sobre a primária |
| `--primaria-100` | `primaria-100` | Item ativo da navegação, selo de reconhecido, faixa p50–p95 |
| `--realce` | `realce` | Igual à primária (legado do `.cartao-alvo:hover`) |
| `--foco` | `foco` | Anel de foco (igual à primária) |
| `--perigo` | `perigo` | Igual a `--estado-fora`: erro de campo, botão de confirmação |
| `--perigo-texto` | `perigo-texto` | Texto sobre o perigo (botão de confirmação, contador) |
| `--estado-no-ar` / `--estado-no-ar-100` | `no-ar` / `no-ar-100` | Texto/ícone e fundo do estado |
| `--estado-degradado` / `--estado-degradado-100` | `degradado` / `degradado-100` | Idem |
| `--estado-fora` / `--estado-fora-100` | `fora` / `fora-100` | Idem |
| `--estado-sem-dados` / `--estado-sem-dados-100` | `sem-dados` / `sem-dados-100` | Sem dados e pausado |
| `--sobreposicao` | `sobreposicao` | Fundo atrás de diálogo |
| `--grafico-linha`, `--grafico-faixa`, `--grafico-uptime` | (sem classe) | Para o Recharts via `var(--…)`; o PR 4 pode redefini-las |
| `--sombra-nivel-1`, `--sombra-nivel-2` | (sem classe) | Só pelos utilitários abaixo |

Todo par de texto sobre fundo que uma tela nova usar e que não esteja na tabela de pares do `src/tokens.test.ts` entra lá antes (TEXTOS × FUNDOS, SOBRE_TINTA ou COMPONENTES). Exemplo: texto em `degradado` sobre `primaria-100` não está coberto.

### Tipografia, raio, sombra e utilitários (PR 1)

| Classe ou recurso | Valor | Uso |
|---|---|---|
| `font-sans` (padrão do `<html>`) | Inter | Texto, dados, formulários |
| `font-display` | Fraunces | Marca, frase de estado, nome do alvo fora de título |
| `h1`, `h2`, `h3` | Fraunces 600 por padrão (`@layer base`) | Uma classe da tela sobrescreve; KPI dentro de título usa `font-sans` |
| `text-xs` 12 · `text-meta` 13 · `text-sm` 14 · `text-base` 16 · `text-lg` 18 · `text-xl` 20 · `text-2xl` 24 · `text-3xl` 28 · `text-4xl` 36 | px | Escala da spec; `text-3xl` passou de 30 para 28 |
| `font-normal`, `font-semibold` | 400, 600 | Únicos pesos |
| `tabular-nums` | global no `body` | Números alinham sem classe extra |
| KPI | `text-3xl font-semibold` (Inter 600, 28 px) + unidade `text-meta font-normal text-suave` | Detalhe do alvo |
| `rotulo-de-grupo` | 12 px, 600, caixa alta, `0.05em` | "FUNCIONANDO", "SEM DADOS OU PAUSADOS" (cor: `text-apagado`) |
| `rounded-padrao` | 8 px | Cartão, botão, campo, segmento |
| `rounded-full` | pílula | Selo, badge, ponto |
| `sombra-nivel-1` | duas camadas; transparente no escuro | Cartão, item ativo do segmentado |
| `sombra-nivel-2` | duas camadas; transparente no escuro | Diálogo, popover, aviso |
| Borda de acento | `border-l-4 border-l-fora` / `border-l-degradado` / `border-l-primaria` | Cartão de problema, incidente aberto, reconhecido |
| Espaçamento | `0`, `1`, `2`, `3`, `4`, `6`, `8`, `12`, `16` (= 0/4/8/12/16/24/32/48/64 px) e `auto` | Únicos valores de `p*`, `m*`, `gap*`, `space-*` |
| `.alvo-de-toque` | pseudo-elemento de 44 px | Link de texto solto (continua do index.css) |
| `cn(...)` | `@/lib/utils` | Já entende `text-meta` e `rounded-padrao` |

Regras de manutenção:

- **`src/sistema.test.ts`, `ARQUIVOS_NO_SISTEMA`:** cada PR acrescenta os arquivos que redesenha (ex.: o PR 3, `src/features/painel/PaginaPainel.tsx`); o PR 8 troca a lista por todo o `src`. Valores arbitrários de tamanho (`max-w-[…]`, `max-h-[…]`) são permitidos; de espaço, fonte, raio, sombra e cor, não.
- **`src/sistema.test.ts`, `USOS_PERMITIDOS`:** `tipo="perigo"` só em diálogo de confirmação. Um PR que crie outra confirmação acrescenta o arquivo com a contagem exata.
- **CSS legado** no `index.css`, a remover pelo PR que reescreve a tela: `.cartao-alvo` e `.resumo-operacional` (PR 3); `#conteudo > form` (PR 6); `main > section:not(…)` (o último PR de tela que o dispensar; no máximo o PR 8). As regras `main p, main h1…` (`overflow-wrap: anywhere`) e `main, main section…` (`min-width: 0`) ficam.

### Componentes

PR 1:

```ts
// web/src/components/ui/botao.tsx
export type TipoDeBotao = 'primario' | 'secundario' | 'terciario' | 'perigo'
export type TamanhoDeBotao = 'normal' | 'pequeno' | 'icone'
export type PropsDoBotao = ComponentProps<'button'> & { tipo?: TipoDeBotao; tamanho?: TamanhoDeBotao; asChild?: boolean }
export function Botao(props: PropsDoBotao)
// Padrões: tipo 'primario', tamanho 'normal'. Todos com min-h-11; 'icone' é size-11 (dê aria-label).
// 'perigo' só no botão que confirma, dentro de diálogo. Apagar, Pausar, Encerrar, Tirar: 'terciario'.
// asChild: estiliza um <Link>/<a> filho (ex.: "Ver detalhes →" como terciário).

// web/src/components/ui/campo.tsx
export const estiloDeEntrada: string
export type AcessibilidadeDoCampo = { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }
export function Campo(props: { rotulo: ReactNode; ajuda?: ReactNode; erro?: string; children: (a11y: AcessibilidadeDoCampo) => ReactNode })
export function Entrada(props: ComponentProps<'input'>)
export function Selecao(props: ComponentProps<'select'>)
export function CaixaDeMarcar(props: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode })
// className vai para a linha (<label>); o resto, ao <input type="checkbox">.
export function ErroDoFormulario(props: { children?: ReactNode })

// web/src/lib/estado.ts (R1.1: regra de domínio fora do componente visual)
export type EstadoExibido = EstadoAlvo | 'pausado'
export function textoDoEstado(e: EstadoExibido): string   // 'No ar' | 'Degradado' | 'Fora do ar' | 'Sem dados' | 'Pausado'
export function estadoExibido(estado: EstadoAlvo, ativo: boolean): EstadoExibido

// web/src/components/SeloDeEstado.tsx (importa EstadoExibido e textoDoEstado de @/lib/estado; não reexporta)
export type TomDoSelo = 'no-ar' | 'degradado' | 'fora' | 'sem-dados' | 'primaria'
export function SeloDeEstado(props: { estado: EstadoExibido; className?: string })
export type PropsDoSelo = { tom: TomDoSelo; Icone: LucideIcon; cheio?: boolean; children: ReactNode; className?: string }
export function Selo(props: PropsDoSelo)
// Selo serve aos rótulos de incidente: "Em andamento" (tom 'fora'), "Reconhecido" (tom 'primaria'),
// "Resolvido" (tom 'sem-dados'). O ícone é obrigatório (forma + cor + texto).

// web/src/components/ui/controle-segmentado.tsx
export type OpcaoSegmentada<V extends string> = { readonly valor: V; readonly rotulo: string }
export type PropsDoControleSegmentado<V extends string> = {
  rotulo: string                         // aria-label do radiogroup
  opcoes: readonly OpcaoSegmentada<V>[]  // aceita um array `as const`
  valor: V
  aoMudar: (valor: V) => void            // só chamado quando o valor muda
  className?: string                     // vai para o trilho
}
export function ControleSegmentado<V extends string>(props: PropsDoControleSegmentado<V>)

// web/src/components/ui/dialogo.tsx (assinaturas de antes)
export type Reserva = RefObject<HTMLElement | null>
export function Dialogo(props: { aberto: boolean; aoMudar: (v: boolean) => void; titulo: string; descricao?: string; travado?: boolean; reserva?: Reserva; children: ReactNode })
export function Confirmar(props: { aberto: boolean; aoMudar: (v: boolean) => void; titulo: string; descricao: ReactNode; acao: string; aoConfirmar: () => void; ocupado?: boolean; reserva?: Reserva })

// web/src/components/ui/avisos.tsx (assinaturas de antes)
export function ProvedorDeAvisos(props: { children: ReactNode })
export function useAvisar(): (texto: string, tipo?: 'ok' | 'erro') => void

// web/src/components/Titulo.tsx (assinatura de antes; agora Fraunces 600, 28/36 px)
export function Titulo(props: { children: ReactNode; aba: string; descritoPor?: string })
```

PR 2:

```ts
// web/src/components/Marca.tsx
export function Marca(props: { tamanho?: 'normal' | 'grande' })  // 'grande' (28 px) é a do login, PR 6

// web/src/app/BotaoDeTema.tsx (uso interno do Layout)
export function BotaoDeTema()

// web/src/app/Layout.tsx
export function Layout()
// Contrato com as telas: elas renderizam dentro de <main id="conteudo"> com
// max-w-6xl, px-4 sm:px-6, pt-8 lg:pt-12, pb-16 (celular) e space-y-8 entre os filhos.
// No celular, a barra inferior fixa tem 53 px: nada de position: fixed no pé da tela.
```

### O que os PRs seguintes vão encontrar nos testes

- **PR 3 (painel):** `src/features/painel/painel.test.tsx` usa o `dl/dt` do resumo e do cartão (a spec manda reescrever). No e2e de alvos de toque, a rota `/` confere os links "API", "Configurar canais" e "Incidente aberto" e as cinco abas; mantenha esses nomes ou atualize o teste.
- **PRs 4 e 5 (detalhe e incidentes):** ao adotar o `ControleSegmentado` na janela do gráfico e no filtro, os botões viram `radio`. Troque `getByRole('button', { name: '1 h' })` (e "6 h", "24 h", "7 dias", "30 dias", "90 dias", "Abertos", "Resolvidos", "Todos") por `getByRole('radio', { name })` em `web/e2e/a11y.spec.ts` (teste de alvos de toque) e nos testes de tela (`detalhe.test.tsx`, `grafico.test.tsx`, `incidentes.test.tsx`). A janela do gráfico no detalhe é o `h1` do `Titulo`: 28 px no celular e 36 px no desktop, não 32.
- **PR 6 (demais telas):** login com `<Marca tamanho="grande" />`; as três caixas de marcar já são `CaixaDeMarcar`; os botões destrutivos já são terciários.
- **Comandos:** `cd web && npm test && npm run typecheck && npm run lint && npm run build`; e2e com `cd web && npm run build && npx playwright test` (Docker); na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`.
