# Redesign do painel, parte 2: Painel (PR 3), Detalhe do alvo (PR 4) e Incidentes (PR 5). Plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para implementar este plano tarefa a tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.

**Objetivo:** reescrever as três telas que respondem "tem algo quebrado?" (painel, detalhe do alvo e incidentes) na direção C, "Editorial claro": manchete gerada dos dados, problemas primeiro em cartões, o estado bom calmo em linhas, faixa de estado com Reconhecer, gráfico de latência com escala robusta e incidentes agrupados, sem API nova.

**Arquitetura:** tudo é front-end em `web/`. Cada tela ganha um módulo de funções puras testado sem React (`manchete.ts`, `grupos.ts`, `escala.ts`, `estado-do-alvo.ts`, `agrupamento.ts`) e componentes finos que só leem, chamam e aplicam. Os dados saem das consultas que já existem (`/targets/summary`, `/targets/:id/checks?window=`, `/incidents`); a consulta dos incidentes abertos, que já roda para o contador da navegação, passa a ser dividida pelo painel e pela tela de incidentes por `select`. O gráfico continua no Recharts (chunk lazy), com a hachura num `<pattern>` do próprio SVG.

**Tech stack:** Vite 8, React 19, Tailwind 4.3, `radix-ui`, `class-variance-authority`, `tailwind-merge` 3, lucide-react, TanStack Query 5, Recharts 3.10, Vitest 5 + Testing Library + user-event + MSW, Playwright 1.63 + axe.

**Spec:** [`docs/superpowers/specs/2026-10-14-redesign-painel-design.md`](../specs/2026-10-14-redesign-painel-design.md), seções "Telas" 1, 2 e 3. Parte anterior: [`2026-10-14-redesign-painel-parte1.md`](2026-10-14-redesign-painel-parte1.md) (PRs 1 e 2), cuja seção "Interfaces publicadas para os PRs seguintes" é a base desta parte. Contexto: [auditoria](../reports/2026-10-14-auditoria-painel.md) e `~/.claude/knowledge/ui-design.md`.

**Pré-requisito:** os PRs 1 e 2 integrados na `main`. Esta parte usa, sem redefinir, `Botao`/`TipoDeBotao`, `Selo`/`SeloDeEstado`/`TomDoSelo`/`estadoExibido`/`textoDoEstado`, `ControleSegmentado`/`OpcaoSegmentada`, `Titulo`, `cn`, os tokens e os utilitários `rounded-padrao`, `sombra-nivel-1`, `sombra-nivel-2`, `rotulo-de-grupo`, `text-meta`, e as listas `ARQUIVOS_NO_SISTEMA` e `USOS_PERMITIDOS` de `web/src/sistema.test.ts`.

**Parte seguinte:** os PRs 6 a 8 (demais telas, página pública e fechamento) ficam na parte 3, que parte da seção [Interfaces publicadas para a parte 3](#interfaces-publicadas-para-a-parte-3), no fim deste documento.

## Global Constraints

- Direção **C, "Editorial claro"**: frase de estado no topo, problemas em destaque, títulos serifados, a página "respira".
- Fontes: **Fraunces** (títulos, frase de estado, marca, nome do alvo) + **Inter** (texto, dados, formulários). Números grandes (KPIs) em **Inter 600 de 28 px** com cifras tabulares, unidade em **Inter 400 de 13 px**.
- Contraste: **4,5:1** para texto e **3:1** para componente e foco, nos dois temas. Todo par novo de texto sobre fundo entra no `web/src/tokens.test.ts` antes de ser usado.
- Escala de fonte **12, 13, 14, 16, 18, 20, 24, 28, 36 px**; pesos **400 e 600**; espaçamento só **4/8/12/16/24/32/48/64** (classes `1 2 3 4 6 8 12 16`); **um raio** (`rounded-padrao`) e pílulas (`rounded-full`); sombras só `sombra-nivel-1` e `sombra-nivel-2`; cor só por token.
- **Borda de acento à esquerda** de 4 px na cor do estado para cartão de problema e incidente aberto (índigo quando reconhecido).
- Estado sempre com **forma + cor + texto**; ícones **só Lucide**, decorativos com `aria-hidden="true"`.
- Alvos de toque de **44 px**, foco visível, `prefers-reduced-motion` respeitado, axe limpo nos dois temas e no celular.
- **Sem mudança de API**, de rotas, de dados ou de comportamento. O "próximo lembrete" está fora do escopo.
- Botões em pirâmide: uma ação primária por tela; Pausar, Apagar e Tirar são `terciario`; `tipo="perigo"` só no diálogo de confirmação (`USOS_PERMITIDOS` não muda nesta parte).
- **Armadilha do `sistema.test.ts`:** o detector lê o arquivo inteiro, comentários inclusive. Num arquivo da `ARQUIVOS_NO_SISTEMA`, o locale `'pt-BR'` passa (o valor de espaçamento começa por minúscula ou dígito), mas não escreva as palavras `shadow`, `rounded`, `font-bold`, `p-5` e afins, nem em comentário. Formatadores com `Intl` ficam em `src/lib/format.ts` (fora da lista).
- **Regra legada `main > section`** do `index.css` dá moldura de cartão a toda `<section>` filha direta do `<main>`. As telas desta parte embrulham o conteúdo num `<div className="space-y-8">`, para as seções não serem filhas diretas.
- Projeto: tudo em português do Brasil; commits `tipo(escopo): descrição`; mudança na `main` só por PR; **teste novo só vale depois de vê-lo falhar com o código sabotado**; o nome anterior do projeto não volta (`sh deploy/ci/checar-nome.sh`).
- Comandos de verificação (os mesmos da parte 1): `cd web && npm test && npm run typecheck && npm run lint && npm run build`; e2e com `cd web && npm run build && npx playwright test` (Docker); na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`.

## Review Focus

As entradas reais que podem quebrar estas telas, cada uma com o teste que a fixa, na task dona:

1. **0 alvos** (conta nova): a manchete diz "Nenhum serviço vigiado ainda." e oferece "Cadastrar o primeiro alvo"; sem meta "0 de 0". Teste na Task 16 (`painel.test.tsx`, "sem alvos").
2. **Todos os alvos pausados**: a manchete não diz "Tudo no ar.", diz "Todos os alvos estão pausados.", e a meta diz "nenhum alvo ativo". Testes nas Tasks 13 (`manchete.test.ts`) e 16.
3. **Nome muito longo** (100 caracteres sem espaço, o teto do servidor): nada rola de lado no painel, no detalhe nem nos incidentes, a 400 e a 1280 px. Teste nas Tasks 17, 24 e 27 (`a11y.spec.ts`, "muitos alvos e nome longo").
4. **Alvo sem nenhum check**: no painel, vai para "Sem dados ou pausados" como "Sem dados · nunca verificado", sem gráfico; no detalhe, faixa "Sem dados" com "Nenhum check ainda." e o KPI "Último check" com "— nunca". Testes nas Tasks 13, 16 e 22.
5. **Janela sem dados**: uma frase ("Nenhum check nesta janela.") no lugar do gráfico e da tabela, sem eixo vazio. Teste na Task 23 (`detalhe.test.tsx`).
6. **Série com um único ponto**: o domínio X ganha 1 h de abertura, as marcas do eixo existem e nenhuma coordenada vira `NaN`. Testes nas Tasks 19 (`escala.test.ts`) e 20 (`grafico.test.tsx`).
7. **Incidente sem causa** (`cause: ""`): "Causa não registrada", nunca um espaço entre dois "·". Testes nas Tasks 13, 16, 23 e 26.
8. **Muitos alvos (30+)**: ordem estável por gravidade e nome ("Serviço 2" antes de "Serviço 10"), nenhum alvo perdido, e cada linha de "Funcionando" com até 48 px. Testes nas Tasks 13 (`grupos.test.ts`, 32 alvos) e 17 (e2e).
9. **Celular a 400 px**: sem rolagem lateral, controles de 44 px, axe limpo. Testes nas Tasks 17, 24 e 27 (e2e de 400 px; o teste de alvos de toque de 400 px já existe e é atualizado).

## Decisões deste plano (pontos da spec que pediram interpretação)

- **Hora do último incidente resolvido** ("Tudo no ar."): a API não tem esse campo e a rota de incidentes ordena pela **abertura** (o id é UUIDv7). O painel pede `GET /incidents?status=resolved&limit=20` só quando está tudo no ar e mostra o maior `resolved_at` entre eles. Escapa só um incidente aberto antes dos 20 mais recentes e encerrado depois de todos eles. Sem resposta (carregando ou falha), a frase some; nunca vira "nenhum".
- **Causa no cartão de problema:** vem da consulta dos abertos (`status=open&limit=100`), a mesma do contador da navegação. Ela passa a ser dividida por `select` (`useAbertos` e `useIncidentesAbertos`), sem requisição nova.
- **Manchete em cinco casos:** sem alvos; problemas (fora do ar ou degradado); aguardando (ativo ainda sem dados, sem problema); todos pausados; tudo no ar. "Tudo no ar." só quando **todo alvo ativo** está no ar. Quantidades até dez por extenso ("Dois serviços pedem atenção."), depois em algarismos.
- **Meta "N de M no ar":** M conta só os ativos. Um pausado não deveria estar no ar.
- **Ordem dentro do grupo:** pelo nome, com `Intl.Collator('pt-BR', { numeric: true, sensitivity: 'base' })`. A ordem do servidor (id decrescente) não ajuda a achar um alvo entre 30.
- **Barra de 24 h:** a hora boa é cheia e cinza, mas o cinza é `--borda-controle` (3,16:1 no pior fundo), e não um cinza-papel claro, que ficaria abaixo de 3:1 e se confundiria com hora sem check. A hora degradada tem dois terços da altura e a fora do ar, um terço, nas cores do estado: as mesmas alturas da página pública (PR 7), onde o dia sem dados é contorno. A latência sai da barra e fica na coluna "Latência".
- **"Funcionando" é uma `<table>`**: a spec pede o cabeçalho de colunas uma vez só, e a tabela associa cada número à coluna para o leitor de tela.
- **Cartão inteiro clicável:** o `::before` do link do nome cobre o cartão. "Ver incidente" fica por cima (`z-10`) e leva a `/incidents?status=open` (não há página de um incidente). Sem incidente aberto, "Ver detalhes →" é só pista visual (`aria-hidden`), para não haver dois links ao mesmo destino. O link do incidente tem nome acessível "Ver incidente de <alvo>" (contém o texto visível, WCAG 2.5.3).
- **Volta do detalhe:** texto visível "Painel" com seta Lucide e nome acessível "Voltar ao painel", para não colidir com a aba "Painel" da navegação. O nome do alvo é o `h1` do `Titulo` (28/36 px), como a parte 1 registrou.
- **Reconhecer na faixa é primário:** o secundário tem borda de controle, que fica a 2,72–2,87:1 sobre os tons 100 do escuro. O texto da faixa usa `texto` e `suave`; o `apagado` fica a 4,08–4,31:1 sobre os tons 100 do escuro. Os pares entram no `tokens.test.ts` (Task 22).
- **KPIs:** "No ar em 24 h" e "Último check" vêm do resumo; "No ar em 30 dias" vem de `checks?window=30d` (horário, atrasa até ~1h15); "Latência mediana (24 h)" vem de `checks?window=24h`, a mesma consulta da janela padrão. O valor e a unidade ficam separados por um espaço ("99,99 %", "120 ms").
- **Escala Y robusta:** p95 dos pontos (posto mais próximo, como `resumir`) × 1,25 é o limite. Se o maior ponto cabe no limite, o teto é o maior × 1,1; senão, o próprio limite. O teto sobe ao próximo número redondo (1, 2, 2,5 ou 5 × 10ⁿ, mínimo 10 ms). O pico é o maior ponto acima do teto; com mais de um, a legenda diz quantos.
- **Marcas do eixo X:** o menor passo entre 15 min, 30 min, 1, 2, 3, 6 e 12 h, e 1, 2, 7 e 14 dias que dê até seis marcas, contado da meia-noite local pelo construtor de `Date` (sem deriva em troca de horário). Passo de dias formata "14/10"; o resto, "09:00".
- **Hachura "fora do ar":** na série bruta, cada check fora do ar vai até o check seguinte (o último, até um passo depois; sem passo, 1 min). Na horária, a hora inteira com menos de 99% no ar, o limiar "fora" da barra de 24 h; a legenda diz "Horas com menos de 99% no ar". As barras de uptime horário saem do gráfico: a tabela mantém a coluna "No ar" por hora.
- **Incidentes:** "Em andamento" agrupa os abertos, reconhecidos inclusive; "Resolvidos recentes", os resolvidos. O resumo ("1 em andamento, 1 reconhecido.") vem da consulta dos abertos e some quando não há aberto, porque o vazio positivo já diz isso.
- **Incidente sem causa:** `textoDaCausa('')` é "Causa não registrada".

## Mapa de arquivos

PR 3, Painel:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/lib/format.ts` + `format.test.ts` | `horaMinuto` |
| `web/src/lib/api/mensagens.ts` + `mensagens.test.ts` (novo) | `textoDaCausa` |
| `web/src/features/painel/manchete.ts` + `manchete.test.ts` (novos) | Manchete, resumo em texto, meta, último resolvido |
| `web/src/features/painel/grupos.ts` + `grupos.test.ts` (novos) | Ordem por gravidade, textos do cartão e da linha sem dados |
| `web/src/components/Minigrafico.tsx` + `Minigrafico.test.tsx` | Barra de 24 h com forma e cor; `estadoDaHora` exportado |
| `web/src/components/EstadoDaConsulta.tsx` + `estado-da-consulta.test.tsx` | Prop `esqueleto` |
| `web/src/features/painel/EsqueletoDoPainel.tsx` + `esqueleto.test.tsx` (novos) | Esqueleto que imita o layout |
| `web/src/lib/api/consultas.ts` | `useIncidentesAbertos`, `useResolvidosRecentes`; `useAbertos` por `select` |
| `web/src/features/painel/PaginaPainel.tsx` + `painel.test.tsx` | Tela nova |
| `web/src/index.css` | Saem `.cartao-alvo` e `.resumo-operacional` |
| `web/src/sistema.test.ts` | Lista |
| `web/e2e/a11y.spec.ts`, `web/e2e/fluxo.spec.ts` | Link do incidente, cartão clicável, 30 alvos, nome longo |

PR 4, Detalhe do alvo:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/features/alvos/serie.ts` + `serie.test.ts` | `faixasForaDoAr`, `latenciasDaSerie`, tipos `Intervalo` e `PontoDeLatencia` |
| `web/src/lib/format.ts` + `format.test.ts` | `diaMes` |
| `web/src/features/alvos/escala.ts` + `escala.test.ts` (novos) | Escala Y robusta, pico, domínio e marcas do eixo X |
| `web/src/features/alvos/GraficoDeLatencia.tsx` + `grafico.test.tsx` | Gráfico novo e legenda |
| `web/src/features/alvos/estado-do-alvo.ts` + `estado-do-alvo.test.ts` (novos) | Faixa de estado e valores dos KPIs |
| `web/src/components/SeloDoIncidente.tsx` + `selo-do-incidente.test.tsx` (novos) | Situação, período e selo do incidente |
| `web/src/features/alvos/FaixaDeEstado.tsx` (novo) | Faixa com Reconhecer |
| `web/src/features/alvos/Indicadores.tsx` (novo) | Quatro KPIs |
| `web/src/features/alvos/IncidentesRecentes.tsx` (novo) | Incidentes do alvo |
| `web/src/features/alvos/PaginaDetalhe.tsx` + `detalhe.test.tsx` | Tela nova |
| `web/src/tokens.test.ts` | Pares da faixa |
| `web/src/index.css` | Sai `--grafico-uptime` |
| `web/e2e/a11y.spec.ts`, `web/e2e/fluxo.spec.ts` | Janela em `radio`, hachura, volta, nome longo |

PR 5, Incidentes:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/features/incidentes/agrupamento.ts` + `agrupamento.test.ts` (novos) | Grupos e resumo dos abertos |
| `web/src/features/incidentes/PaginaIncidentes.tsx` + `incidentes.test.tsx` | Tela nova |
| `web/e2e/a11y.spec.ts` | Filtro em `radio`, nome longo e sem causa |

---

# PR 3: Painel

Branch: `feat/redesign-painel`, a partir da `main` com os PRs 1 e 2 integrados.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-painel
```

### Task 13: A lógica do painel em funções puras

**Files:**
- Modify: `web/src/lib/format.ts` (acrescenta `horaMinuto`)
- Modify: `web/src/lib/format.test.ts`
- Modify: `web/src/lib/api/mensagens.ts` (acrescenta `textoDaCausa`)
- Create: `web/src/lib/api/mensagens.test.ts`
- Create: `web/src/features/painel/grupos.ts`, `web/src/features/painel/grupos.test.ts`
- Create: `web/src/features/painel/manchete.ts`, `web/src/features/painel/manchete.test.ts`

**Interfaces:**
- Consumes: `estadoExibido`, `textoDoEstado`, `type EstadoExibido` (`@/lib/estado`, PR 1); `textoDoErroDeCheck` (`@/lib/api/mensagens`); `dataHora`, `duracao`, `haQuanto`, `milissegundos` (`@/lib/format`); tipos `Incidente`, `ItemDoResumo`.
- Produces:

```ts
// web/src/lib/format.ts
export function horaMinuto(instante: string | number): string            // "09:05", fuso do navegador

// web/src/lib/api/mensagens.ts
export function textoDaCausa(cause: string): string                      // "" → "Causa não registrada"

// web/src/features/painel/grupos.ts
export type GruposDoPainel = { problemas: ItemDoResumo[]; funcionando: ItemDoResumo[]; semDadosOuPausados: ItemDoResumo[] }
export function ehProblema(e: EstadoExibido): boolean
export function agruparPorGravidade(itens: readonly ItemDoResumo[]): GruposDoPainel
export function detalheDoProblema(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): string
export function linhaSemDados(it: ItemDoResumo, agora: Date): string

// web/src/features/painel/manchete.ts
export type Manchete =
  | { tipo: 'sem-alvos' }
  | { tipo: 'problemas'; quantos: number }
  | { tipo: 'aguardando'; quantos: number }
  | { tipo: 'todos-pausados' }
  | { tipo: 'tudo-no-ar' }
export function manchete(itens: readonly ItemDoResumo[]): Manchete
export function textoDaManchete(m: Manchete): string
export function resumoEmTexto(itens: readonly ItemDoResumo[]): string   // "1 fora do ar, 2 degradados, 5 no ar"
export function metaDoPainel(itens: readonly ItemDoResumo[], atualizadoEm: Date): string
export function ultimoResolvido(itens: readonly Incidente[]): (Incidente & { resolved_at: string }) | null
export function fraseDoUltimoResolvido(itens: readonly Incidente[] | undefined): string | null
```

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/lib/format.test.ts`, inclua `horaMinuto` no import do topo:

```ts
import { duracao, haQuanto, horaMinuto, milissegundos, navegadorDe, pontosDeCodigo, porcento } from './format'
```

e acrescente no fim:

```ts
// Datas pelo construtor local: o teste vale em qualquer fuso.
describe('horaMinuto', () => {
  it('hora e minuto com dois dígitos, no fuso do navegador', () => {
    expect(horaMinuto(new Date(2026, 9, 14, 9, 5).getTime())).toBe('09:05')
    expect(horaMinuto(new Date(2026, 9, 14, 23, 59).toISOString())).toBe('23:59')
  })
})
```

Crie `web/src/lib/api/mensagens.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { textoDaCausa } from './mensagens'

// Review Focus 7: um incidente sem causa não pode virar um espaço em branco
// entre dois "·" na tela.
describe('textoDaCausa', () => {
  it.each([
    ['timeout', 'Tempo esgotado'],
    ['conn_refused', 'Conexão recusada'],
    ['um_kind_novo', 'Falha'],
    ['', 'Causa não registrada'],
  ])('%j vira "%s"', (causa, texto) => {
    expect(textoDaCausa(causa)).toBe(texto)
  })
})
```

Crie `web/src/features/painel/grupos.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { agruparPorGravidade, detalheDoProblema, linhaSemDados } from './grupos'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const nomes = (itens: { name: string }[]) => itens.map((it) => it.name)
const agora = new Date('2026-09-24T12:00:00Z')

describe('agruparPorGravidade', () => {
  it('fora do ar antes de degradado; no ar em funcionando; sem dados e pausados no fim', () => {
    const g = agruparPorGravidade([
      alvo({ id: '1', name: 'Lento', state: 'degraded' }),
      alvo({ id: '2', name: 'Site' }),
      alvo({ id: '3', name: 'Parado', enabled: false }),
      alvo({ id: '4', name: 'Banco', state: 'down' }),
      alvo({ id: '5', name: 'Novo', state: 'unknown' }),
      alvo({ id: '6', name: 'Caiu', state: 'down' }),
    ])
    expect(nomes(g.problemas)).toEqual(['Banco', 'Caiu', 'Lento'])
    expect(nomes(g.funcionando)).toEqual(['Site'])
    expect(nomes(g.semDadosOuPausados)).toEqual(['Novo', 'Parado'])
  })

  it('pausado nunca é problema, mesmo com o último estado fora do ar', () => {
    const g = agruparPorGravidade([alvo({ name: 'Parado', state: 'down', enabled: false })])
    expect(g.problemas).toEqual([])
    expect(nomes(g.semDadosOuPausados)).toEqual(['Parado'])
  })

  // Review Focus 8: entre 30 alvos, "Serviço 10" depois de "Serviço 9", e
  // maiúscula não separa.
  it('dentro do grupo, pelo nome: números em ordem e sem diferenciar maiúscula', () => {
    const g = agruparPorGravidade([
      alvo({ id: 'a', name: 'Serviço 10' }),
      alvo({ id: 'b', name: 'serviço 2' }),
      alvo({ id: 'c', name: 'Serviço 1' }),
      alvo({ id: 'd', name: 'api' }),
    ])
    expect(nomes(g.funcionando)).toEqual(['api', 'Serviço 1', 'serviço 2', 'Serviço 10'])
  })

  it('com 32 alvos, nenhum se perde nem se repete', () => {
    const itens = Array.from({ length: 32 }, (_, i) =>
      alvo({ id: `a${i}`, name: `Serviço ${i}`, state: i < 2 ? 'down' : i < 4 ? 'unknown' : 'up' }),
    )
    const g = agruparPorGravidade(itens)
    const todos = [...g.problemas, ...g.funcionando, ...g.semDadosOuPausados].map((it) => it.id)
    expect(new Set(todos).size).toBe(32)
    expect([g.problemas.length, g.funcionando.length, g.semDadosOuPausados.length]).toEqual([2, 28, 2])
  })

  it('lista vazia dá três grupos vazios', () => {
    expect(agruparPorGravidade([])).toEqual({ problemas: [], funcionando: [], semDadosOuPausados: [] })
  })
})

describe('detalheDoProblema', () => {
  it('com incidente aberto: a causa e há quanto tempo', () => {
    const fora = alvo({ state: 'down' })
    expect(detalheDoProblema(fora, inc({ started_at: '2026-09-24T09:29:00Z', cause: 'conn_refused' }), agora)).toBe(
      'Conexão recusada · há 2 h 31 min',
    )
  })

  it('incidente sem causa diz que a causa não foi registrada', () => {
    expect(detalheDoProblema(alvo({ state: 'down' }), inc({ started_at: '2026-09-24T11:59:00Z', cause: '' }), agora)).toBe(
      'Causa não registrada · há 1 min',
    )
  })

  it('degradado sem incidente: a latência do último check', () => {
    expect(detalheDoProblema(alvo({ state: 'degraded', last_latency_ms: 2400 }), undefined, agora)).toBe(
      'Latência de 2.400 ms no último check',
    )
    expect(detalheDoProblema(alvo({ state: 'degraded', last_latency_ms: null }), undefined, agora)).toBe('Lento no último check')
  })

  it('fora do ar sem incidente (ainda não abriu): o último check', () => {
    expect(detalheDoProblema(alvo({ state: 'down', last_checked_at: '2026-09-24T11:57:00Z' }), undefined, agora)).toBe(
      'Último check há 3 minutos',
    )
  })
})

describe('linhaSemDados', () => {
  it.each([
    [{ enabled: false }, 'Pausado'],
    [{ state: 'unknown', last_checked_at: null }, 'Sem dados · nunca verificado'],
    [{ state: 'unknown', last_checked_at: '2026-09-24T11:55:00Z' }, 'Sem dados · último check há 5 minutos'],
  ] as const)('%j → "%s"', (p, texto) => {
    expect(linhaSemDados(alvo(p), agora)).toBe(texto)
  })
})
```

Crie `web/src/features/painel/manchete.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import {
  fraseDoUltimoResolvido,
  manchete,
  metaDoPainel,
  resumoEmTexto,
  textoDaManchete,
  ultimoResolvido,
  type Manchete,
} from './manchete'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))

describe('manchete', () => {
  it('sem alvos', () => {
    expect(manchete([])).toEqual({ tipo: 'sem-alvos' })
  })

  it('fora do ar e degradado contam como problema; pausado e sem dados, não', () => {
    const itens = [alvo({ state: 'down' }), alvo({ state: 'degraded' }), alvo({ state: 'down', enabled: false }), alvo({ state: 'unknown' }), alvo()]
    expect(manchete(itens)).toEqual({ tipo: 'problemas', quantos: 2 })
  })

  it('sem problema, um ativo ainda sem dados segura o "Tudo no ar."', () => {
    expect(manchete([alvo(), alvo({ state: 'unknown' })])).toEqual({ tipo: 'aguardando', quantos: 1 })
  })

  // Review Focus 2.
  it('todos pausados', () => {
    expect(manchete([alvo({ enabled: false }), alvo({ state: 'unknown', enabled: false })])).toEqual({ tipo: 'todos-pausados' })
  })

  it('tudo no ar: todo ativo no ar, com os pausados à parte', () => {
    expect(manchete([alvo(), alvo({ enabled: false })])).toEqual({ tipo: 'tudo-no-ar' })
  })
})

describe('textoDaManchete', () => {
  it.each([
    [{ tipo: 'sem-alvos' }, 'Nenhum serviço vigiado ainda.'],
    [{ tipo: 'problemas', quantos: 1 }, 'Um serviço pede atenção.'],
    [{ tipo: 'problemas', quantos: 2 }, 'Dois serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 10 }, 'Dez serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 11 }, '11 serviços pedem atenção.'],
    [{ tipo: 'aguardando', quantos: 1 }, 'Um serviço ainda sem dados.'],
    [{ tipo: 'aguardando', quantos: 3 }, 'Três serviços ainda sem dados.'],
    [{ tipo: 'todos-pausados' }, 'Todos os alvos estão pausados.'],
    [{ tipo: 'tudo-no-ar' }, 'Tudo no ar.'],
  ] satisfies [Manchete, string][])('%j → "%s"', (m, texto) => {
    expect(textoDaManchete(m)).toBe(texto)
  })
})

describe('resumoEmTexto', () => {
  it('conta por gravidade, com plural, e omite o estado que não aparece', () => {
    const itens = [alvo(), alvo({ state: 'degraded' }), alvo({ state: 'degraded' }), alvo({ state: 'down' }), alvo({ state: 'unknown' }), alvo({ enabled: false })]
    expect(resumoEmTexto(itens)).toBe('1 fora do ar, 2 degradados, 1 no ar, 1 sem dados, 1 pausado')
    expect(resumoEmTexto([alvo(), alvo()])).toBe('2 no ar')
  })
})

describe('metaDoPainel', () => {
  const as = new Date(2026, 9, 14, 14, 32)

  it('conta os ativos no ar sobre o total de ativos', () => {
    expect(metaDoPainel([alvo(), alvo({ state: 'down' }), alvo({ enabled: false })], as)).toBe('Atualizado às 14:32 · 1 de 2 no ar')
  })

  it('sem ativo, não inventa um total', () => {
    expect(metaDoPainel([alvo({ enabled: false })], as)).toBe('Atualizado às 14:32 · nenhum alvo ativo')
  })
})

describe('último incidente resolvido', () => {
  // A rota ordena pela abertura: o último a terminar pode vir depois na lista.
  it('é o de resolved_at maior, não o primeiro da lista', () => {
    const cedo = inc({ id: 'i2', started_at: '2026-09-24T09:00:00Z', resolved_at: '2026-09-24T09:05:00Z' })
    const tarde = inc({ id: 'i1', started_at: '2026-09-24T08:00:00Z', resolved_at: '2026-09-24T11:00:00Z' })
    expect(ultimoResolvido([cedo, tarde])?.id).toBe('i1')
  })

  it('ignora o aberto; sem resolvido, null', () => {
    expect(ultimoResolvido([inc({ resolved_at: null })])).toBeNull()
    expect(ultimoResolvido([])).toBeNull()
  })

  it('a frase: sem resposta, nada; lista vazia, "nenhum"; senão, a data do fim', () => {
    expect(fraseDoUltimoResolvido(undefined)).toBeNull()
    expect(fraseDoUltimoResolvido([])).toBe('Nenhum incidente registrado.')
    expect(fraseDoUltimoResolvido([inc({ resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() })])).toMatch(
      /^Último incidente resolvido em 24\/09\/2026,? 09:12\.$/,
    )
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/lib/format.test.ts src/lib/api/mensagens.test.ts src/features/painel/grupos.test.ts src/features/painel/manchete.test.ts`
Expected: FAIL. `horaMinuto` e `textoDaCausa` não são exportados; `Failed to resolve import "./grupos"` e `"./manchete"`.

- [ ] **Step 3: `horaMinuto` e `textoDaCausa`**

Em `web/src/lib/format.ts`, logo depois de `dataHora`, acrescente:

```ts
const horaMinutoFmt = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' })

// "09:05", no fuso do navegador: a meta do painel, o último check e as
// marcas do eixo do gráfico.
export function horaMinuto(instante: string | number): string {
  return horaMinutoFmt.format(new Date(instante))
}
```

Em `web/src/lib/api/mensagens.ts`, logo depois de `textoDoErroDeCheck`, acrescente:

```ts
// A causa de um incidente para a tela. A coluna cause guarda o error_kind
// do check que abriu o incidente; vazia (um dado antigo), vira uma frase, e
// não um espaço em branco entre dois "·".
export function textoDaCausa(cause: string): string {
  return textoDoErroDeCheck(cause, null) || 'Causa não registrada'
}
```

- [ ] **Step 4: `grupos.ts`**

Crie `web/src/features/painel/grupos.ts`:

```ts
import { textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, type EstadoExibido } from '@/lib/estado'
import { duracao, haQuanto, milissegundos } from '@/lib/format'

// A ordem do painel (spec do redesign, Telas > 1): os problemas primeiro, em
// cartões; o que funciona, em linhas; sem dados e pausados, em texto.
// Funções puras: a tela só as chama.
export type GruposDoPainel = {
  problemas: ItemDoResumo[]
  funcionando: ItemDoResumo[]
  semDadosOuPausados: ItemDoResumo[]
}

const GRAVIDADE: Record<EstadoExibido, number> = { down: 0, degraded: 1, up: 2, unknown: 3, pausado: 4 }

// numeric: "Serviço 2" antes de "Serviço 10"; base: maiúscula e acento não
// separam. A ordem do servidor (id decrescente) não ajuda a achar um alvo
// entre 30.
const PELO_NOME = new Intl.Collator('pt-BR', { sensitivity: 'base', numeric: true })

const exibido = (it: ItemDoResumo) => estadoExibido(it.state, it.enabled)

export function ehProblema(e: EstadoExibido): boolean {
  return e === 'down' || e === 'degraded'
}

export function agruparPorGravidade(itens: readonly ItemDoResumo[]): GruposDoPainel {
  const ordenados = [...itens].sort(
    (a, b) => GRAVIDADE[exibido(a)] - GRAVIDADE[exibido(b)] || PELO_NOME.compare(a.name, b.name),
  )
  return {
    problemas: ordenados.filter((it) => ehProblema(exibido(it))),
    funcionando: ordenados.filter((it) => exibido(it) === 'up'),
    semDadosOuPausados: ordenados.filter((it) => exibido(it) === 'unknown' || exibido(it) === 'pausado'),
  }
}

// A linha de baixo do cartão de problema: com incidente aberto, a causa e
// há quanto tempo; sem ele, a latência (degradado) ou o último check (fora
// do ar antes de o incidente abrir).
export function detalheDoProblema(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): string {
  if (incidente) {
    return `${textoDaCausa(incidente.cause)} · há ${duracao(agora.getTime() - Date.parse(incidente.started_at))}`
  }
  if (it.state === 'degraded') {
    return it.last_latency_ms === null ? 'Lento no último check' : `Latência de ${milissegundos(it.last_latency_ms)} no último check`
  }
  return it.last_checked_at === null ? 'Nenhum check ainda' : `Último check ${haQuanto(it.last_checked_at, agora)}`
}

// Uma linha de texto por alvo, sem gráfico (auditoria P7).
export function linhaSemDados(it: ItemDoResumo, agora: Date): string {
  if (!it.enabled) return 'Pausado'
  return it.last_checked_at === null
    ? 'Sem dados · nunca verificado'
    : `Sem dados · último check ${haQuanto(it.last_checked_at, agora)}`
}
```

- [ ] **Step 5: `manchete.ts`**

Crie `web/src/features/painel/manchete.ts`:

```ts
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, type EstadoExibido } from '@/lib/estado'
import { dataHora, horaMinuto } from '@/lib/format'
import { ehProblema } from './grupos'

// A manchete do painel (spec do redesign, Telas > 1): uma frase gerada dos
// dados que responde "tem algo quebrado?" de relance.
export type Manchete =
  | { tipo: 'sem-alvos' }
  | { tipo: 'problemas'; quantos: number }
  | { tipo: 'aguardando'; quantos: number }
  | { tipo: 'todos-pausados' }
  | { tipo: 'tudo-no-ar' }

// "Tudo no ar." só quando todo alvo ativo está no ar: um ativo ainda sem
// dados não está "no ar", e os pausados não contam.
export function manchete(itens: readonly ItemDoResumo[]): Manchete {
  if (itens.length === 0) return { tipo: 'sem-alvos' }
  const estados = itens.map((it) => estadoExibido(it.state, it.enabled))
  const problemas = estados.filter(ehProblema).length
  if (problemas > 0) return { tipo: 'problemas', quantos: problemas }
  const semDados = estados.filter((e) => e === 'unknown').length
  if (semDados > 0) return { tipo: 'aguardando', quantos: semDados }
  if (estados.every((e) => e === 'pausado')) return { tipo: 'todos-pausados' }
  return { tipo: 'tudo-no-ar' }
}

// Até dez por extenso, como numa manchete de jornal; depois, o número.
const POR_EXTENSO = ['Zero', 'Um', 'Dois', 'Três', 'Quatro', 'Cinco', 'Seis', 'Sete', 'Oito', 'Nove', 'Dez']

function servicos(n: number): string {
  return `${POR_EXTENSO[n] ?? n} ${n === 1 ? 'serviço' : 'serviços'}`
}

// Sem default: um tipo novo de Manchete sem caso aqui não compila.
export function textoDaManchete(m: Manchete): string {
  switch (m.tipo) {
    case 'sem-alvos':
      return 'Nenhum serviço vigiado ainda.'
    case 'problemas':
      return `${servicos(m.quantos)} ${m.quantos === 1 ? 'pede' : 'pedem'} atenção.`
    case 'aguardando':
      return `${servicos(m.quantos)} ainda sem dados.`
    case 'todos-pausados':
      return 'Todos os alvos estão pausados.'
    case 'tudo-no-ar':
      return 'Tudo no ar.'
  }
}

const ORDEM: readonly [EstadoExibido, string, string][] = [
  ['down', 'fora do ar', 'fora do ar'],
  ['degraded', 'degradado', 'degradados'],
  ['up', 'no ar', 'no ar'],
  ['unknown', 'sem dados', 'sem dados'],
  ['pausado', 'pausado', 'pausados'],
]

// A frase de detalhe da manchete, por gravidade: "1 fora do ar, 2 degradados, 5 no ar".
export function resumoEmTexto(itens: readonly ItemDoResumo[]): string {
  const conta = new Map<EstadoExibido, number>()
  for (const it of itens) {
    const e = estadoExibido(it.state, it.enabled)
    conta.set(e, (conta.get(e) ?? 0) + 1)
  }
  return ORDEM.flatMap(([e, um, varios]) => {
    const n = conta.get(e) ?? 0
    return n ? [`${n} ${n === 1 ? um : varios}`] : []
  }).join(', ')
}

// "Atualizado às 14:32 · 3 de 4 no ar". O total é o dos ativos: um pausado
// não deveria estar no ar.
export function metaDoPainel(itens: readonly ItemDoResumo[], atualizadoEm: Date): string {
  const ativos = itens.filter((it) => it.enabled)
  const noAr = ativos.filter((it) => it.state === 'up').length
  const quantos = ativos.length === 0 ? 'nenhum alvo ativo' : `${noAr} de ${ativos.length} no ar`
  return `Atualizado às ${horaMinuto(atualizadoEm.getTime())} · ${quantos}`
}

type Resolvido = Incidente & { resolved_at: string }

// O último incidente a terminar. A rota ordena pela abertura, então o
// primeiro da lista não serve.
export function ultimoResolvido(itens: readonly Incidente[]): Resolvido | null {
  const resolvidos = itens.filter((i): i is Resolvido => i.resolved_at !== null)
  return resolvidos.reduce<Resolvido | null>(
    (ultimo, i) => (ultimo === null || Date.parse(i.resolved_at) > Date.parse(ultimo.resolved_at) ? i : ultimo),
    null,
  )
}

// A frase do "Tudo no ar.". Sem resposta (carregando ou falha), nada: não
// saber não é "nenhum incidente".
export function fraseDoUltimoResolvido(itens: readonly Incidente[] | undefined): string | null {
  if (itens === undefined) return null
  const ultimo = ultimoResolvido(itens)
  return ultimo ? `Último incidente resolvido em ${dataHora(ultimo.resolved_at)}.` : 'Nenhum incidente registrado.'
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/lib/format.test.ts src/lib/api/mensagens.test.ts src/features/painel/grupos.test.ts src/features/painel/manchete.test.ts`
Expected: PASS.

Sabotagens (uma por vez, desfazendo cada uma): em `textoDaCausa`, devolva só `textoDoErroDeCheck(cause, null)` → o caso `''` falha; em `agruparPorGravidade`, troque `PELO_NOME.compare(a.name, b.name)` por `a.name < b.name ? -1 : 1` → os casos de nome falham; em `servicos`, troque `POR_EXTENSO[n] ?? n` por `n` → os casos por extenso falham; em `ultimoResolvido`, devolva `resolvidos[0] ?? null` → "é o de resolved_at maior" falha; em `metaDoPainel`, troque `${noAr} de ${ativos.length}` por `${noAr} de ${itens.length}` → "1 de 2" vira "1 de 3".

- [ ] **Step 7: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/format.ts web/src/lib/format.test.ts web/src/lib/api/mensagens.ts web/src/lib/api/mensagens.test.ts web/src/features/painel/grupos.ts web/src/features/painel/grupos.test.ts web/src/features/painel/manchete.ts web/src/features/painel/manchete.test.ts
git commit -m "feat(painel): manchete, ordem por gravidade e textos do painel em funções puras"
```

### Task 14: Barra de 24 h com forma e cor

**Files:**
- Modify: `web/src/components/Minigrafico.tsx` (arquivo inteiro)
- Modify: `web/src/components/Minigrafico.test.tsx` (arquivo inteiro)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `HoraDoSpark`; as variáveis CSS `--borda-controle`, `--estado-degradado`, `--estado-fora` (PR 1).
- Produces:

```ts
// web/src/components/Minigrafico.tsx
export type EstadoDaHora = 'no-ar' | 'degradado' | 'fora'
export function estadoDaHora(h: { total: number; ok: number }): EstadoDaHora   // ok/total ≥ 0,999 no-ar; ≥ 0,99 degradado; senão fora
export function Minigrafico(props: { horas: HoraDoSpark[]; agora: Date })     // 96 × 24 px, role="img"
```

- [ ] **Step 1: Reescrever o teste**

Substitua `web/src/components/Minigrafico.test.tsx` por:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Minigrafico } from './Minigrafico'

// UTC e expectativas literais: nenhum oracle usa a fórmula do componente.
const agora = new Date('2026-10-05T12:37:00Z')
const atributo = (c: HTMLElement, nome: string) => Array.from(c.querySelectorAll('rect'), (r) => r.getAttribute(nome))

describe('barra das últimas 24 horas', () => {
  it('série vazia não inventa medições e diz que não há checks', () => {
    const { container } = render(<Minigrafico horas={[]} agora={agora} />, { reactStrictMode: true })
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: sem checks')
    expect(container.querySelectorAll('rect')).toHaveLength(0)
  })

  // Sabotagem: incluir o bucket anterior/atual, ou deslocar a janela na fração de hora.
  it('inclui o primeiro e o último bucket completos e exclui as horas fora da janela', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-04T11:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-04T12:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-05T11:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-05T12:00:00Z', total: 1, ok: 1, p50_ms: 100 },
        ]}
      />,
    )
    expect(atributo(container, 'x')).toEqual(['0', '92'])
  })

  // Sabotagem: desenhar a hora sem checks como barra.
  it('hora sem checks é lacuna, não barra', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-04T13:00:00Z', total: 0, ok: 0, p50_ms: null },
          { bucket: '2026-10-04T14:00:00Z', total: 1, ok: 1, p50_ms: 10 },
        ]}
      />,
    )
    expect(atributo(container, 'x')).toEqual(['8'])
  })

  // Forma + cor (spec do redesign, Telas > 1): a hora boa é cheia e cinza;
  // a degradada tem dois terços da altura e a fora do ar, um terço, na cor
  // do estado. Sabotagem: excluir a igualdade nos limiares, trocar as
  // alturas ou pintar a hora boa de verde.
  it('forma e cor pelo estado da hora, nos limiares inclusivos de 99,9% e 99%', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T06:00:00Z', total: 1000, ok: 1000, p50_ms: 100 },
          { bucket: '2026-10-05T07:00:00Z', total: 1000, ok: 999, p50_ms: 100 },
          { bucket: '2026-10-05T08:00:00Z', total: 1000, ok: 998, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 1000, ok: 990, p50_ms: 100 },
          { bucket: '2026-10-05T10:00:00Z', total: 1000, ok: 989, p50_ms: 100 },
          { bucket: '2026-10-05T11:00:00Z', total: 1000, ok: 0, p50_ms: null },
        ]}
      />,
    )
    expect(atributo(container, 'height')).toEqual(['24', '24', '16', '16', '8', '8'])
    expect(atributo(container, 'y')).toEqual(['0', '0', '8', '8', '16', '16'])
    expect(atributo(container, 'fill')).toEqual([
      'var(--borda-controle)',
      'var(--borda-controle)',
      'var(--estado-degradado)',
      'var(--estado-degradado)',
      'var(--estado-fora)',
      'var(--estado-fora)',
    ])
  })

  // A latência foi para a coluna "Latência": a altura é só do estado.
  // Sabotagem: voltar a altura proporcional ao p50.
  it('a latência não muda a altura da barra', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T07:00:00Z', total: 10, ok: 10, p50_ms: 2000 },
          { bucket: '2026-10-05T08:00:00Z', total: 10, ok: 10, p50_ms: 10 },
        ]}
      />,
    )
    expect(atributo(container, 'height')).toEqual(['24', '24'])
  })

  // Sabotagem: contar a hora sem checks, errar o limiar ou o plural.
  it('o nome acessível resume as horas por estado', () => {
    render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T06:00:00Z', total: 1000, ok: 1000, p50_ms: 100 },
          { bucket: '2026-10-05T07:00:00Z', total: 1000, ok: 999, p50_ms: 100 },
          { bucket: '2026-10-05T08:00:00Z', total: 1000, ok: 990, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 0, ok: 0, p50_ms: null },
          { bucket: '2026-10-05T10:00:00Z', total: 1000, ok: 989, p50_ms: 100 },
        ]}
      />,
    )
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: 2 horas no ar, 1 hora degradada, 1 hora fora do ar')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/Minigrafico.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/Minigrafico.test.tsx`
Expected: FAIL. As posições são de passo 5 (`'0.5'`, `'115.5'`, `'10.5'`), as alturas seguem o p50 e a cor da hora boa é `var(--estado-no-ar)`. O primeiro e o último teste passam (caracterização: o nome acessível não muda).

- [ ] **Step 3: Reescrever o componente**

Substitua `web/src/components/Minigrafico.tsx` por:

```tsx
import type { HoraDoSpark } from '@/lib/api/schemas'

export type EstadoDaHora = 'no-ar' | 'degradado' | 'fora'

// Forma + cor (spec do redesign, Telas > 1): a hora boa é uma barra cheia e
// cinza; a degradada tem dois terços da altura, e a fora do ar, um terço,
// nas cores do estado. A altura carrega o sinal sem depender da cor (WCAG
// 1.4.1). O cinza é a borda de controle, com 3:1 sobre o fundo e o cartão
// (src/tokens.test.ts): um cinza mais claro se confundiria com a hora sem
// check, que é lacuna. As alturas são as da página pública (PR 7); lá, o dia sem dados é contorno.
const ESTILO: Record<EstadoDaHora, { cor: string; fracao: number }> = {
  'no-ar': { cor: 'var(--borda-controle)', fracao: 1 },
  degradado: { cor: 'var(--estado-degradado)', fracao: 2 / 3 },
  fora: { cor: 'var(--estado-fora)', fracao: 1 / 3 },
}

const HORAS = 24
const PASSO = 4
const LARGURA = HORAS * PASSO
const ALTURA = 24
const MS_POR_HORA = 3_600_000

// As 24 horas do painel em SVG próprio: o Recharts fica fora do painel
// (quase metade do bundle, spec do Plano 6, D4). Hora sem check é lacuna,
// não zero. O nome acessível resume as horas por estado.
export function Minigrafico({ horas, agora }: { horas: HoraDoSpark[]; agora: Date }) {
  const inicio = Math.floor(agora.getTime() / MS_POR_HORA) - HORAS
  const desenhadas = horas.flatMap((h) => {
    const i = Math.floor(new Date(h.bucket).getTime() / MS_POR_HORA) - inicio
    return i < 0 || i >= HORAS || h.total === 0 ? [] : [{ h, x: i * PASSO, estado: estadoDaHora(h) }]
  })
  return (
    // SVG desenhado aqui não vira <img>: role="img" com aria-label é o
    // padrão ARIA para dar nome a um SVG inline.
    // oxlint-disable-next-line jsx-a11y/prefer-tag-over-role
    <svg
      role="img"
      aria-label={`Últimas 24 h: ${resumir(desenhadas.map((d) => d.estado))}`}
      width={LARGURA}
      height={ALTURA}
      viewBox={`0 0 ${LARGURA} ${ALTURA}`}
    >
      {desenhadas.map(({ h, x, estado }) => {
        const { cor, fracao } = ESTILO[estado]
        const altura = ALTURA * fracao
        return <rect key={h.bucket} x={x} y={ALTURA - altura} width={PASSO - 1} height={altura} fill={cor} />
      })}
    </svg>
  )
}

// Os limiares de sempre (99,9% e 99%, inclusivos). O gráfico do detalhe usa
// o mesmo "fora" para hachurar a hora (src/features/alvos/serie.ts).
export function estadoDaHora(h: { total: number; ok: number }): EstadoDaHora {
  const fracao = h.ok / h.total
  if (fracao >= 0.999) return 'no-ar'
  return fracao >= 0.99 ? 'degradado' : 'fora'
}

const NOMES: Record<EstadoDaHora, [string, string]> = {
  'no-ar': ['hora no ar', 'horas no ar'],
  degradado: ['hora degradada', 'horas degradadas'],
  fora: ['hora fora do ar', 'horas fora do ar'],
}

function resumir(estados: EstadoDaHora[]): string {
  if (estados.length === 0) return 'sem checks'
  return (Object.keys(NOMES) as EstadoDaHora[])
    .map((e) => [e, estados.filter((x) => x === e).length] as const)
    .filter(([, n]) => n > 0)
    .map(([e, n]) => `${n} ${NOMES[e][n === 1 ? 0 : 1]}`)
    .join(', ')
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/Minigrafico.test.tsx src/sistema.test.ts`
Expected: PASS.

Sabotagens: troque `fracao: 2 / 3` por `fracao: 1` → o teste de forma falha; troque `'var(--borda-controle)'` por `'var(--estado-no-ar)'` → falha; volte `>= 0.99` para `> 0.99` → falha. Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: verde. O painel ainda usa o `Minigrafico` com as mesmas props.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/Minigrafico.tsx web/src/components/Minigrafico.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): barra de 24 h com forma e cor pelo estado da hora"
```

### Task 15: Esqueleto do painel

**Files:**
- Modify: `web/src/components/EstadoDaConsulta.tsx` (prop `esqueleto`)
- Modify: `web/src/components/estado-da-consulta.test.tsx`
- Create: `web/src/features/painel/EsqueletoDoPainel.tsx`
- Create: `web/src/features/painel/esqueleto.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `cn`, `rounded-padrao`, `painel`, `borda`.
- Produces:

```ts
// web/src/components/EstadoDaConsulta.tsx
export function EstadoDaConsulta<T>(props: {
  consulta: Pick<UseQueryResult<T>, 'data' | 'error' | 'isPending' | 'refetch' | 'dataUpdatedAt'>
  esqueleto?: ReactNode       // novo: no lugar do <Esqueleto /> padrão enquanto carrega
  children: (dados: T) => ReactNode
})

// web/src/features/painel/EsqueletoDoPainel.tsx
export function EsqueletoDoPainel()   // aria-busy, "Carregando…" (sr-only), 8 blocos aria-hidden
```

- [ ] **Step 1: Escrever os testes que falham**

No fim de `web/src/components/estado-da-consulta.test.tsx`, acrescente:

```tsx
// O esqueleto que imita a tela (P12 da auditoria): a tela passa o dela.
// Sabotagem: ignorar a prop e devolver sempre o <Esqueleto />.
it('carregando, mostra o esqueleto dado no lugar do padrão', () => {
  render(
    <EstadoDaConsulta
      consulta={{
        data: undefined,
        error: null,
        isPending: true,
        refetch: () => Promise.reject(new Error('não usado')),
        dataUpdatedAt: 0,
      }}
      esqueleto={<p>o esqueleto da tela</p>}
    >
      {() => <p>os dados</p>}
    </EstadoDaConsulta>,
  )
  expect(screen.getByText('o esqueleto da tela')).toBeInTheDocument()
  expect(screen.queryByText('Carregando…')).not.toBeInTheDocument()
})
```

Crie `web/src/features/painel/esqueleto.test.tsx`:

```tsx
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'

describe('EsqueletoDoPainel', () => {
  // P12 da auditoria: o esqueleto imita a manchete (2 blocos), dois cartões
  // e quatro linhas; o leitor de tela ouve só "Carregando…".
  it('imita a manchete, os cartões e as linhas, e avisa que está carregando', () => {
    const { container } = render(<EsqueletoDoPainel />)
    const ocupado = container.querySelector('[aria-busy="true"]')
    expect(ocupado).toHaveTextContent('Carregando…')
    expect(ocupado?.querySelectorAll('[aria-hidden="true"]')).toHaveLength(8)
  })

  it('nenhum bloco anima com prefers-reduced-motion', () => {
    const { container } = render(<EsqueletoDoPainel />)
    const animados = Array.from(container.querySelectorAll('.animate-pulse'))
    expect(animados).toHaveLength(8)
    for (const bloco of animados) expect(bloco).toHaveClass('motion-reduce:animate-none')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/painel/EsqueletoDoPainel.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/estado-da-consulta.test.tsx src/features/painel/esqueleto.test.tsx`
Expected: FAIL. O `EstadoDaConsulta` mostra "Carregando…" (ignora a prop; o `tsc` também a recusaria); `Failed to resolve import "./EsqueletoDoPainel"`.

- [ ] **Step 3: A prop `esqueleto`**

Em `web/src/components/EstadoDaConsulta.tsx`, troque a assinatura e a primeira linha do corpo:

```tsx
export function EstadoDaConsulta<T>({
  consulta,
  esqueleto,
  children,
}: {
  consulta: Pick<UseQueryResult<T>, 'data' | 'error' | 'isPending' | 'refetch' | 'dataUpdatedAt'>
  // O esqueleto que imita a tela; sem ele, blocos genéricos.
  esqueleto?: ReactNode
  children: (dados: T) => ReactNode
}) {
  if (consulta.isPending) return esqueleto ?? <Esqueleto />
```

- [ ] **Step 4: O esqueleto do painel**

Crie `web/src/features/painel/EsqueletoDoPainel.tsx`:

```tsx
import { cn } from '@/lib/utils'

// O esqueleto do painel (auditoria P12): a manchete, dois cartões de
// problema e quatro linhas, no lugar onde vão aparecer. A animação para com
// prefers-reduced-motion (motion-reduce, e a regra global do index.css); o
// leitor de tela ouve "Carregando…", e o e2e espera o aria-busy sumir.
const BLOCO = 'animate-pulse rounded-padrao bg-painel motion-reduce:animate-none'

export function EsqueletoDoPainel() {
  return (
    <div aria-busy="true" className="space-y-8">
      <span className="sr-only">Carregando…</span>
      <div className="space-y-3">
        <div aria-hidden="true" className={cn(BLOCO, 'h-8 w-2/3')} />
        <div aria-hidden="true" className={cn(BLOCO, 'h-4 w-1/3')} />
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {[0, 1].map((i) => (
          <div key={i} aria-hidden="true" className={cn(BLOCO, 'h-32 border-l-4 border-l-borda')} />
        ))}
      </div>
      <div className="space-y-1">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} aria-hidden="true" className={cn(BLOCO, 'h-11')} />
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/components/estado-da-consulta.test.tsx src/features/painel/esqueleto.test.tsx src/sistema.test.ts`
Expected: PASS.

Sabotagem: tire o `motion-reduce:animate-none` do `BLOCO` → o segundo teste do esqueleto falha. Desfaça.

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/EstadoDaConsulta.tsx web/src/components/estado-da-consulta.test.tsx web/src/features/painel/EsqueletoDoPainel.tsx web/src/features/painel/esqueleto.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): esqueleto que imita a manchete, os cartões e as linhas"
```

### Task 16: O painel por gravidade

**Files:**
- Modify: `web/src/lib/api/consultas.ts` (`useAbertos` por `select`, `useIncidentesAbertos`, `useResolvidosRecentes`)
- Modify: `web/src/features/painel/PaginaPainel.tsx` (arquivo inteiro)
- Modify: `web/src/features/painel/painel.test.tsx` (arquivo inteiro)
- Modify: `web/src/index.css` (saem `.cartao-alvo` e `.resumo-operacional`; a regra `main > section`)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: Tasks 13 a 15; `Botao`, `SeloDeEstado`, `estadoExibido`, `Titulo`, `cn`, `useAnunciar`, `transicoes`, `mapaDeEstados`, `useResumo`, `useCanais`.
- Produces:

```ts
// web/src/lib/api/consultas.ts
export type Abertos = { itens: Incidente[]; completa: boolean }   // completa: a página de 100 não tinha next_cursor
export function useAbertos(): UseQueryResult<string>             // como antes: "0" … "99", "99+"
export function useIncidentesAbertos(): UseQueryResult<Abertos>  // mesma consulta do contador, outro select
export function useResolvidosRecentes(ativa: boolean): UseQueryResult<ListaDeIncidentes>  // status=resolved, limit=20

// web/src/features/painel/PaginaPainel.tsx
export function PaginaPainel()
```

Contrato da tela (os testes e o e2e dependem dele): `h2` da manchete; regiões (`<section aria-labelledby>`) "Pedem atenção", "Funcionando" e "Sem dados ou pausados"; cartão de problema é `<li>` com `h3` do nome e link do nome para `/targets/:id`; link "Ver incidente de <nome>" para `/incidents?status=open`; "Funcionando" é uma tabela com `rowheader` por alvo e as colunas Alvo, Latência, 24 h, Últimas 24 h; linha de `tbody` com 44 px.

- [ ] **Step 1: Reescrever o teste da tela**

Substitua `web/src/features/painel/painel.test.tsx` por:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { canal, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
import { ItemDoResumo } from '@/lib/api/schemas'
import { mapaDeEstados, transicoes } from './transicoes'

const resumido = (p: Parameters<typeof itemDoResumo>[0]) => ItemDoResumo.parse(itemDoResumo(p))
const regiao = (nome: string) => screen.getByRole('region', { name: nome })
const cartaoDe = async (nome: string) =>
  (await screen.findByRole('heading', { level: 3, name: nome })).closest('li') as HTMLElement

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
  // Review Focus 1.
  it('sem alvos, a manchete diz que não há o que vigiar e oferece cadastrar o primeiro', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Cadastrar o primeiro alvo' })).toHaveAttribute('href', '/targets/new')
    expect(screen.queryByText(/^Atualizado às/)).not.toBeInTheDocument()
  })

  // Spec, Telas > 1: problemas primeiro, em cartões; o que funciona, em
  // linhas com o cabeçalho uma vez só; sem dados e pausados, em texto, sem
  // gráfico (auditoria P7). Review Focus 4 (o "Novo" nunca foi verificado).
  it('ordena por gravidade: problemas em cartões, no ar em linhas, sem dados e pausados em texto', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'z', name: 'Zeta' }),
      itemDoResumo({ id: 'a', name: 'alfa', uptime_24h: 0.99999 }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded', last_latency_ms: 2400 }),
      itemDoResumo({ id: 'c', name: 'Caiu', state: 'down', last_latency_ms: null }),
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down', last_latency_ms: null }),
      itemDoResumo({ id: 'p', name: 'Parado', state: 'unknown', enabled: false }),
      itemDoResumo({ id: 'n', name: 'Novo', state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] }),
    ]
    await r.cliente.refetchQueries()
    const problemas = await screen.findByRole('region', { name: 'Pedem atenção' })
    expect(within(problemas).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['Banco', 'Caiu', 'Lento'])
    expect(within(problemas).getAllByText('Fora do ar')).toHaveLength(2)
    expect(within(problemas).getByText('Degradado')).toBeInTheDocument()

    const funcionando = regiao('Funcionando')
    expect(within(funcionando).getAllByRole('columnheader').map((c) => c.textContent)).toEqual([
      'Alvo',
      'Latência',
      '24 h',
      'Últimas 24 h',
    ])
    expect(within(funcionando).getAllByRole('rowheader').map((c) => c.textContent)).toEqual(['alfa', 'Zeta'])
    // Truncado: 0,99999 é "99,99%", nunca "100,00%".
    expect(within(funcionando).getByRole('row', { name: /alfa/ })).toHaveTextContent('99,99%')

    const semDados = regiao('Sem dados ou pausados')
    expect(
      within(semDados)
        .getAllByRole('listitem')
        .map((li) => [within(li).getByRole('link').textContent, li.querySelector('span')?.textContent]),
    ).toEqual([
      ['Novo', 'Sem dados · nunca verificado'],
      ['Parado', 'Pausado'],
    ])
    expect(within(semDados).queryByRole('img')).not.toBeInTheDocument()
  })

  it('a manchete conta os problemas por extenso e detalha os estados por gravidade', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down' }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded' }),
      itemDoResumo({ id: 'a', name: 'API' }),
      itemDoResumo({ id: 's', name: 'Site' }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Dois serviços pedem atenção.' })).toBeInTheDocument()
    expect(screen.getByText('1 fora do ar, 1 degradado, 2 no ar.')).toBeInTheDocument()
    expect(screen.getByText(/^Atualizado às \d{2}:\d{2} · 2 de 4 no ar$/)).toBeInTheDocument()
  })

  // Review Focus 2.
  it('todos pausados: a manchete diz, e a meta não inventa um total', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ enabled: false }), itemDoResumo({ id: 'b', name: 'Banco', state: 'unknown', enabled: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Todos os alvos estão pausados.' })).toBeInTheDocument()
    expect(screen.getByText(/· nenhum alvo ativo$/)).toBeInTheDocument()
  })

  it('um ativo ainda sem dados: a manchete espera, e não diz "Tudo no ar."', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo(), itemDoResumo({ id: 'n', name: 'Novo', state: 'unknown', last_checked_at: null })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Um serviço ainda sem dados.' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Tudo no ar.' })).not.toBeInTheDocument()
  })

  describe('tudo no ar', () => {
    // A rota ordena pela abertura: o último a terminar vem depois na lista.
    // Sabotagem: em ultimoResolvido, devolver o primeiro → aparece 08:30.
    it('mostra o check em círculo verde e a hora do último incidente resolvido, pelo fim', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      r.falsa.estado.incidentes = [
        incidente({ id: 'i2', started_at: '2026-09-24T09:00:00Z', resolved_at: new Date(2026, 8, 24, 8, 30).toISOString() }),
        incidente({ id: 'i1', started_at: '2026-09-24T07:00:00Z', resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() }),
      ]
      await r.cliente.refetchQueries()
      const titulo = await screen.findByRole('heading', { level: 2, name: 'Tudo no ar.' })
      expect(titulo.previousElementSibling).toHaveClass('bg-no-ar-100')
      expect(await screen.findByText(/^Último incidente resolvido em 24\/09\/2026,? 09:12\.$/)).toBeInTheDocument()
    })

    it('sem incidente resolvido, diz que não houve nenhum', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      await r.cliente.refetchQueries()
      expect(await screen.findByText('Nenhum incidente registrado.')).toBeInTheDocument()
    })

    // Não saber não é "nenhum". Sabotagem: em fraseDoUltimoResolvido,
    // tratar undefined como lista vazia.
    it('se a consulta dos resolvidos falha, não diz "nenhum"', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      r.falsa.estado.forcar.set('GET /incidents', { status: 502, code: 'unavailable' })
      await r.cliente.refetchQueries()
      await screen.findByRole('heading', { level: 2, name: 'Tudo no ar.' })
      await waitFor(() =>
        expect(r.cliente.getQueryCache().find({ queryKey: ['incidents', 'resolved'] })?.state.status).toBe('error'),
      )
      expect(screen.queryByText('Nenhum incidente registrado.')).not.toBeInTheDocument()
      expect(screen.queryByText(/^Último incidente resolvido/)).not.toBeInTheDocument()
    })
  })

  it('cartão de problema: a causa e há quanto tempo do incidente aberto, e o link para ele', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down', last_latency_ms: null, open_incident_id: 'inc-9' }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded', last_latency_ms: 2400 }),
    ]
    r.falsa.estado.incidentes = [incidente({ id: 'inc-9', target_id: 'b', target_name: 'Banco', cause: 'conn_refused' })]
    await r.cliente.refetchQueries()
    const banco = await cartaoDe('Banco')
    expect(banco).toHaveClass('border-l-fora')
    expect(await within(banco).findByText(/^Conexão recusada · há /)).toBeInTheDocument()
    expect(within(banco).getByRole('link', { name: 'Banco' })).toHaveAttribute('href', '/targets/b')
    expect(within(banco).getByRole('link', { name: 'Ver incidente de Banco' })).toHaveAttribute('href', '/incidents?status=open')

    const lento = await cartaoDe('Lento')
    expect(lento).toHaveClass('border-l-degradado')
    expect(within(lento).getByText('Latência de 2.400 ms no último check')).toBeInTheDocument()
    // "Ver detalhes" é pista visual: o cartão já leva ao detalhe.
    expect(within(lento).getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual(['/targets/l'])
    expect(within(lento).getByText('Ver detalhes')).toHaveAttribute('aria-hidden', 'true')
  })

  // Review Focus 7.
  it('incidente sem causa: o cartão diz que a causa não foi registrada', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'b', name: 'Banco', state: 'down', open_incident_id: 'inc-9' })]
    r.falsa.estado.incidentes = [incidente({ id: 'inc-9', target_id: 'b', target_name: 'Banco', cause: '' })]
    await r.cliente.refetchQueries()
    expect(await within(await cartaoDe('Banco')).findByText(/^Causa não registrada · há /)).toBeInTheDocument()
  })

  // P12. Sabotagem: tirar o esqueleto={<EsqueletoDoPainel />} da tela → o
  // esqueleto padrão tem 3 blocos.
  it('enquanto o resumo não chega, o esqueleto do painel ocupa o lugar', async () => {
    const r = await renderizarPainel('/')
    let soltar!: () => void
    const segurado = new Promise<void>((ok) => (soltar = ok))
    servidor.use(
      http.get('*/api/v1/targets/summary', async () => {
        await segurado
        return HttpResponse.json({ items: [] })
      }),
    )
    void r.cliente.resetQueries({ queryKey: ['targets', 'summary'] })
    const ocupado = (await screen.findByText('Carregando…')).closest('[aria-busy="true"]') as HTMLElement
    expect(ocupado.querySelectorAll('[aria-hidden="true"]')).toHaveLength(8)
    soltar()
    expect(await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })).toBeInTheDocument()
  })

  it('anuncia na região aria-live quando um alvo cai, e não na primeira carga', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'up' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    const anuncios = screen.getByTestId('anuncios')
    expect(anuncios).toHaveTextContent('')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'down' })]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(anuncios).toHaveTextContent('API: fora do ar'))
  })

  // Spec §4 e M9 da revisão: um poll de fundo que falha não esconde o que já
  // se sabia. Sabotagem prevista: em EstadoDaConsulta, trocar
  // "if (consulta.data === undefined)" por
  // "if (consulta.data === undefined || consulta.error)".
  it('um poll que falha mantém os alvos e avisa que os dados estão velhos', async () => {
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

  // T5 da revisão de 2026-10-14: a fronteira do "99+" (uma página de até 100
  // abertos). 99 → "99"; 100 → "99+"; 101 (página cheia com next_cursor) →
  // "99+". Sabotagem: trocar "> 99" por "> 100" → o caso de 100 mostra "100".
  it.each([
    [99, 'Incidentes, 99 abertos'],
    [100, 'Incidentes, 99+ abertos'],
    [101, 'Incidentes, 99+ abertos'],
  ])('com %i incidentes abertos, o contador diz “%s”', async (quantos, nome) => {
    const r = await renderizarPainel('/')
    const link = await screen.findByRole('link', { name: 'Incidentes' })
    r.falsa.estado.incidentes = Array.from({ length: quantos }, (_, i) => incidente({ id: `i${i}` }))
    await r.cliente.refetchQueries()
    await waitFor(() => expect(link).toHaveAccessibleName(nome))
  })
})

// Plano 12: tirar os avisos de canais precisa derrubar estes casos. A API
// falsa só fornece respostas; a tela é real.
describe('avisos de canais', () => {
  it('sem canais avisa como configurar os alertas sem esconder os alvos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Configurar canais' })).toHaveAttribute('href', '/channels')
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })

  it('avisa de canais pausados e de ausência de canal verificado ativo', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.canais = [canal({ enabled: false }), canal({ id: 'c2', status: 'pending' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('1 canal pausado. Os avisos desse canal são descartados.')).toBeInTheDocument()
    expect(screen.getByText('Nenhum canal verificado está ativo para receber avisos.')).toBeInTheDocument()
    r.falsa.estado.canais = [canal(), canal({ id: 'c2', enabled: false })]
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    await waitFor(() => expect(screen.queryByText('Nenhum canal verificado está ativo para receber avisos.')).not.toBeInTheDocument())
    expect(screen.getByText('1 canal pausado. Os avisos desse canal são descartados.')).toBeInTheDocument()
  })

  it('falha na consulta de canais não é apresentada como ausência e mantém os alvos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('GET /channels', { status: 502, code: 'unavailable' })
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    expect(await screen.findByText('Não foi possível conferir os canais de notificação agora.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })

  // Um canal failing continua verificado e recebendo tentativas (notify).
  // Sabotagem: considerar apenas status verified no aviso de canal ativo.
  it('canal falhando é avisado e continua contado como verificado ativo', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.canais = [canal({ status: 'failing' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('1 canal está falhando na entrega.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum canal verificado está ativo para receber avisos.')).not.toBeInTheDocument()
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/painel/PaginaPainel.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/painel/painel.test.tsx src/sistema.test.ts`
Expected: FAIL. Não há `h2` de manchete, nem as regiões "Pedem atenção", "Funcionando" e "Sem dados ou pausados"; o esqueleto tem 3 blocos; `PaginaPainel.tsx` reprova no sistema (`rounded-lg`, `space-y-5`, `font-bold`, `font-medium`, `mt-0.5`, `pl-7`, `rounded-md`, `px-5`, `py-5`). Passam `transicoes`, o anúncio, o poll, a navegação e os avisos de canais.

- [ ] **Step 3: As consultas dos incidentes**

Em `web/src/lib/api/consultas.ts`, troque o import do topo por:

```ts
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useApi } from '@/app/contexto'
import type { Incidente, ListaDeIncidentes } from './schemas'
```

e substitua a função `useAbertos` inteira (com o comentário acima dela) por:

```ts
// Os incidentes abertos: uma página de até 100, o teto da rota. Uma consulta
// só, dividida pelo contador da navegação (spec do Plano 6 §4.9), pelos
// cartões do painel e pelo resumo da tela de incidentes: cada um lê o que
// precisa pelo select. A chave fica sob ['incidents'], e o reconhecer, que
// invalida ['incidents'], atualiza todos.
const LIMITE_DOS_ABERTOS = 100
// O mesmo intervalo de hoje (30 s). Se a Onda 1 do backlog (R1.5) já criou
// esta constante em consultas.ts, use a que existe e não declare outra.
const INTERVALO_DOS_INCIDENTES_MS = 30_000

export type Abertos = { itens: Incidente[]; completa: boolean }

function useConsultaDosAbertos<T>(select: (d: ListaDeIncidentes) => T) {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('open', undefined, LIMITE_DOS_ABERTOS), 'abertos'],
    queryFn: () => api.incidentes({ status: 'open', limit: LIMITE_DOS_ABERTOS }),
    refetchInterval: INTERVALO_DOS_INCIDENTES_MS,
    select,
  })
}

// Mais que uma página vira "99+".
function contarAbertos(d: ListaDeIncidentes): string {
  return d.next_cursor ? '99+' : d.items.length > 99 ? '99+' : String(d.items.length)
}

function listarAbertos(d: ListaDeIncidentes): Abertos {
  return { itens: d.items, completa: d.next_cursor === undefined }
}

export function useAbertos() {
  return useConsultaDosAbertos(contarAbertos)
}

export function useIncidentesAbertos() {
  return useConsultaDosAbertos(listarAbertos)
}

// Os resolvidos mais recentes, para o "Tudo no ar." do painel dizer quando
// terminou o último incidente. A rota ordena pela abertura (o id é UUIDv7),
// não pelo fim: entre os 20 abertos mais recentemente, o de resolved_at
// maior é o último a terminar, salvo um incidente aberto antes deles e
// encerrado depois de todos (spec do redesign, sem campo novo na API). Só
// roda com o painel todo no ar.
const LIMITE_DOS_RESOLVIDOS = 20

export function useResolvidosRecentes(ativa: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('resolved', undefined, LIMITE_DOS_RESOLVIDOS), 'ultimo'],
    queryFn: () => api.incidentes({ status: 'resolved', limit: LIMITE_DOS_RESOLVIDOS }),
    enabled: ativa,
    refetchInterval: 60_000,
  })
}
```

- [ ] **Step 4: Reescrever a tela**

Substitua `web/src/features/painel/PaginaPainel.tsx` por:

```tsx
import { ArrowRight, Bell, Check, Plus } from 'lucide-react'
import { useEffect, useId, useRef, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Minigrafico } from '@/components/Minigrafico'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { useCanais, useIncidentesAbertos, useResolvidosRecentes, useResumo } from '@/lib/api/consultas'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido } from '@/lib/estado'
import { milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'
import { agruparPorGravidade, detalheDoProblema, linhaSemDados } from './grupos'
import { fraseDoUltimoResolvido, manchete, metaDoPainel, resumoEmTexto, textoDaManchete } from './manchete'
import { mapaDeEstados, transicoes } from './transicoes'

export function PaginaPainel() {
  const consulta = useResumo()
  const anunciar = useAnunciar()
  const anterior = useRef<ReturnType<typeof mapaDeEstados> | null>(null)
  const itens = consulta.data?.items

  useEffect(() => {
    if (!itens) return
    const frases = transicoes(anterior.current, itens)
    if (frases.length) anunciar(frases.join('. '))
    anterior.current = mapaDeEstados(itens)
  }, [itens, anunciar])

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Titulo aba="Painel">Painel</Titulo>
        <Botao asChild>
          <Link to="/targets/new">
            <Plus aria-hidden="true" className="size-4" />
            Cadastrar alvo
          </Link>
        </Botao>
      </div>
      <AvisoDeCanais />
      <EstadoDaConsulta consulta={consulta} esqueleto={<EsqueletoDoPainel />}>
        {(d) => <Alvos itens={d.items} atualizadoEm={new Date(consulta.dataUpdatedAt)} />}
      </EstadoDaConsulta>
    </>
  )
}

// Um <div>, e não <section> filho direto do <main>: a regra legada
// "main > section" do index.css daria moldura de cartão a cada grupo.
function Alvos({ itens, atualizadoEm }: { itens: ItemDoResumo[]; atualizadoEm: Date }) {
  const grupos = agruparPorGravidade(itens)
  // A mesma consulta do contador da navegação: nenhuma requisição a mais.
  const abertos = useIncidentesAbertos()
  const porId = new Map((abertos.data?.itens ?? []).map((i) => [i.id, i]))
  return (
    <div className="space-y-8">
      <Manchete itens={itens} atualizadoEm={atualizadoEm} />
      {grupos.problemas.length > 0 && (
        <Grupo titulo="Pedem atenção">
          <ul className="grid gap-4 md:grid-cols-2">
            {grupos.problemas.map((it) => (
              <CartaoDeProblema
                key={it.id}
                it={it}
                incidente={it.open_incident_id ? porId.get(it.open_incident_id) : undefined}
                agora={atualizadoEm}
              />
            ))}
          </ul>
        </Grupo>
      )}
      {grupos.funcionando.length > 0 && (
        <Grupo titulo="Funcionando">
          <TabelaFuncionando itens={grupos.funcionando} agora={atualizadoEm} />
        </Grupo>
      )}
      {grupos.semDadosOuPausados.length > 0 && (
        <Grupo titulo="Sem dados ou pausados">
          <ul className="divide-y divide-borda">
            {grupos.semDadosOuPausados.map((it) => (
              <li key={it.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-3">
                <Link to={`/targets/${it.id}`} className="alvo-de-toque font-semibold underline-offset-4 hover:underline">
                  {it.name}
                </Link>
                <span className="text-sm text-suave">{linhaSemDados(it, atualizadoEm)}</span>
              </li>
            ))}
          </ul>
        </Grupo>
      )}
    </div>
  )
}

// A frase de estado no topo (spec do redesign, Telas > 1), em Fraunces.
function Manchete({ itens, atualizadoEm }: { itens: ItemDoResumo[]; atualizadoEm: Date }) {
  const m = manchete(itens)
  const resolvidos = useResolvidosRecentes(m.tipo === 'tudo-no-ar')
  if (m.tipo === 'sem-alvos') {
    return (
      <div className="space-y-2">
        <h2 className="text-2xl sm:text-3xl">{textoDaManchete(m)}</h2>
        <p className="text-suave">
          Cadastre um endereço para o StillOn vigiar.{' '}
          <Link to="/targets/new" className="alvo-de-toque font-semibold text-primaria underline underline-offset-4">
            Cadastrar o primeiro alvo
          </Link>
        </p>
      </div>
    )
  }
  const detalhe = m.tipo === 'tudo-no-ar' ? fraseDoUltimoResolvido(resolvidos.data?.items) : `${resumoEmTexto(itens)}.`
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        {m.tipo === 'tudo-no-ar' && (
          <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-full bg-no-ar-100 text-no-ar">
            <Check aria-hidden="true" className="size-6" strokeWidth={2.5} />
          </span>
        )}
        <h2 className="text-2xl sm:text-3xl">{textoDaManchete(m)}</h2>
      </div>
      {detalhe && <p className="text-suave">{detalhe}</p>}
      <p className="text-meta text-apagado">{metaDoPainel(itens, atualizadoEm)}</p>
    </div>
  )
}

function Grupo({ titulo, children }: { titulo: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2 id={id} className="rotulo-de-grupo font-sans text-apagado">
        {titulo}
      </h2>
      {children}
    </section>
  )
}

// O cartão inteiro é clicável (auditoria P8): o ::before do link do nome
// cobre o cartão. "Ver incidente" fica por cima dele (z-10) e leva a outro
// lugar; "Ver detalhes" é só a pista visual do clique no cartão, e não um
// segundo link para o mesmo destino.
function CartaoDeProblema({ it, incidente, agora }: { it: ItemDoResumo; incidente: Incidente | undefined; agora: Date }) {
  const estado = estadoExibido(it.state, it.enabled)
  return (
    <li
      className={cn(
        'relative min-w-0 space-y-3 rounded-padrao border border-l-4 border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6',
        estado === 'down' ? 'border-l-fora' : 'border-l-degradado',
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h3 className="min-w-0 text-xl">
          <Link
            to={`/targets/${it.id}`}
            className="underline-offset-4 before:absolute before:inset-0 before:rounded-padrao hover:underline"
          >
            {it.name}
          </Link>
        </h3>
        <SeloDeEstado estado={estado} />
      </div>
      <p className="text-sm text-suave">{detalheDoProblema(it, incidente, agora)}</p>
      <p className="text-sm">
        No ar em 24 h: <span className="font-semibold">{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</span>
      </p>
      {it.open_incident_id ? (
        <Link
          to="/incidents?status=open"
          aria-label={`Ver incidente de ${it.name}`}
          className="alvo-de-toque z-10 inline-flex items-center gap-1 text-sm font-semibold text-primaria underline underline-offset-4"
        >
          Ver incidente <ArrowRight aria-hidden="true" className="size-4" />
        </Link>
      ) : (
        <span aria-hidden="true" className="inline-flex items-center gap-1 text-sm font-semibold text-primaria">
          Ver detalhes <ArrowRight className="size-4" />
        </span>
      )}
    </li>
  )
}

// Linhas compactas (spec: 30 alvos sem rolar várias telas), com o cabeçalho
// de colunas uma vez só. Cada linha tem 44 px; números à direita.
function TabelaFuncionando({ itens, agora }: { itens: ItemDoResumo[]; agora: Date }) {
  return (
    <div className="rounded-padrao border border-borda bg-superficie sombra-nivel-1">
      <table className="w-full text-sm">
        <caption className="sr-only">Alvos no ar: latência do último check, tempo no ar em 24 h e as últimas 24 horas</caption>
        <thead>
          <tr className="text-meta text-apagado">
            <th scope="col" className="px-2 py-2 text-left font-normal sm:px-4">Alvo</th>
            <th scope="col" className="px-2 py-2 text-right font-normal sm:px-4">Latência</th>
            <th scope="col" className="px-2 py-2 text-right font-normal sm:px-4">24 h</th>
            <th scope="col" className="px-2 py-2 text-left font-normal sm:px-4">Últimas 24 h</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-borda border-t border-borda">
          {itens.map((it) => (
            <tr key={it.id} className="h-11">
              <th scope="row" className="px-2 text-left font-normal sm:px-4">
                <Link to={`/targets/${it.id}`} className="alvo-de-toque font-semibold underline-offset-4 hover:underline">
                  {it.name}
                </Link>
              </th>
              <td className="px-2 text-right sm:px-4">{milissegundos(it.last_latency_ms)}</td>
              <td className="px-2 text-right sm:px-4">{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</td>
              <td className="px-2 sm:px-4">
                <Minigrafico horas={it.spark} agora={agora} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// A consulta de canais é independente: falhar nunca esconde os alvos nem
// transforma "não se sabe" em "nenhum canal".
function AvisoDeCanais() {
  const consulta = useCanais()
  const canais = consulta.data?.items
  let frases: string[] = []
  if (consulta.error) frases = ['Não foi possível conferir os canais de notificação agora.']
  else if (canais) {
    if (!canais.length) frases.push('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')
    else {
      const pausados = canais.filter((c) => !c.enabled).length
      if (pausados) {
        frases.push(
          pausados === 1
            ? '1 canal pausado. Os avisos desse canal são descartados.'
            : `${pausados} canais pausados. Os avisos desses canais são descartados.`,
        )
      }
      if (!canais.some((c) => c.enabled && (c.status === 'verified' || c.status === 'failing'))) {
        frases.push('Nenhum canal verificado está ativo para receber avisos.')
      }
      const falhando = canais.filter((c) => c.enabled && c.status === 'failing').length
      if (falhando) frases.push(falhando === 1 ? '1 canal está falhando na entrega.' : `${falhando} canais estão falhando na entrega.`)
    }
  }
  if (!frases.length) return null
  return (
    <section aria-label="Avisos de notificação" className="flex items-start gap-3 rounded-padrao border border-borda bg-painel p-4 text-sm">
      <Bell aria-hidden="true" className="mt-1 size-4 shrink-0 text-suave" />
      <div className="min-w-0 flex-1 space-y-2">
        {frases.map((frase) => (
          <p key={frase}>{frase}</p>
        ))}
        <Link to="/channels" className="alvo-de-toque inline-block font-semibold text-primaria underline underline-offset-4">
          Configurar canais
        </Link>
      </div>
    </section>
  )
}
```

- [ ] **Step 5: Tirar o CSS legado do painel**

Em `web/src/index.css`, apague as regras `.cartao-alvo { … }`, `.cartao-alvo:hover { … }`, `.resumo-operacional { … }`, `.resumo-operacional > div { … }`, `.resumo-operacional > div:nth-child(2n + 1) { … }` e os dois blocos `@media (min-width: 40rem)` e `@media (min-width: 80rem)` que só têm regras de `.resumo-operacional`. Troque o seletor:

```css
main > section:not([aria-label='Resumo dos alvos']):not([aria-label='Avisos de notificação']) {
```

por:

```css
main > section:not([aria-label='Avisos de notificação']) {
```

Confira: `cd web && grep -rn "cartao-alvo\|resumo-operacional\|Resumo dos alvos" src`
Expected: nenhuma linha.

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/painel src/sistema.test.ts src/components`
Expected: PASS.

Sabotagens (uma por vez): tire `esqueleto={<EsqueletoDoPainel />}` → o teste do esqueleto conta 3; troque `porId.get(it.open_incident_id)` por `undefined` → "a causa e há quanto tempo" falha; em `Manchete`, troque `fraseDoUltimoResolvido(resolvidos.data?.items)` por `fraseDoUltimoResolvido(resolvidos.data?.items ?? [])` → "se a consulta dos resolvidos falha" falha. Desfaça.

- [ ] **Step 7: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: verde. O `main.test.tsx` e o `layout.test.tsx` (contador da navegação) passam com o `useAbertos` por `select`.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/api/consultas.ts web/src/features/painel/PaginaPainel.tsx web/src/features/painel/painel.test.tsx web/src/index.css web/src/sistema.test.ts
git commit -m "feat(painel): painel por gravidade com manchete, cartões de problema e linhas"
```

### Task 17: e2e do painel, verificação do PR 3 e abertura

**Files:**
- Modify: `web/e2e/a11y.spec.ts` (alvos de toque do `/`; cartão clicável; muitos alvos e nome longo)
- Modify: `web/e2e/fluxo.spec.ts` (passos 4 e 6)

**Interfaces:**
- Consumes: o contrato da tela da Task 16.
- Produces: o teste `muitos alvos e nome longo, ${largura} px` com a função local `semRolagemLateral(contexto)` e as constantes `NOME_LONGO` e `MUITOS_ALVOS`, que os PRs 4 e 5 estendem.

- [ ] **Step 1: O link do incidente no teste de alvos de toque**

Em `web/e2e/a11y.spec.ts`, no teste `alvos de toque de 44 px`, troque:

```ts
      ['Incidente aberto', page.getByRole('link', { name: 'Incidente aberto' })],
```

por:

```ts
      ['Ver incidente', page.getByRole('link', { name: 'Ver incidente de Banco' })],
```

- [ ] **Step 2: Cartão clicável, 30 alvos e nome longo**

No fim de `web/e2e/a11y.spec.ts`, acrescente:

```ts
// Auditoria P8: o cartão de problema é clicável inteiro, não só o nome. O
// clique no canto de baixo à direita (longe do nome e do "Ver incidente")
// abre o detalhe. Sabotagem: tirar o before:absolute do link do nome.
test('o cartão de problema é clicável inteiro', async ({ page }) => {
  await imitarApi(page)
  await page.goto('/')
  const cartao = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'Banco', level: 3 }) })
  await expect(cartao).toBeVisible()
  const caixa = await cartao.boundingBox()
  expect(caixa).not.toBeNull()
  await page.mouse.click(caixa!.x + caixa!.width - 8, caixa!.y + caixa!.height - 8)
  await expect(page).toHaveURL(/\/targets\/a3$/)
  await controlePositivo(page, 'cartão clicável')
})

// Review Focus 3, 8 e 9 da parte 2 do plano: 32 alvos (30 no ar, um fora do
// ar com nome de 100 caracteres sem espaço, o teto do servidor, e um
// degradado). A página não rola de lado, e cada linha de "Funcionando" fica
// compacta (até 48 px), para 30 alvos caberem em poucas telas. Sabotagens:
// trocar o h-11 da linha por h-16 (64 px); pôr whitespace-nowrap no link do
// nome do cartão (a página rola de lado no celular).
const NOME_LONGO = 'ServicoSemEspacos'.repeat(6).slice(0, 100)
const MUITOS_ALVOS = [
  item('a1', NOME_LONGO, 'down'),
  item('m-lento', 'Lento', 'degraded'),
  ...Array.from({ length: 30 }, (_, i) => item(`m${i}`, `Serviço ${i + 1}`, 'up')),
]

for (const largura of [400, 1280]) {
  test(`muitos alvos e nome longo, ${largura} px`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 900 })
    await imitarApi(page)
    // Registrada depois, esta rota vence a do imitarApi para o resumo.
    await page.route('**/api/v1/targets/summary', (route) => route.fulfill({ status: 200, json: { items: MUITOS_ALVOS } }))
    const semRolagemLateral = async (contexto: string) => {
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${contexto}: largura`).toBe(largura)
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${contexto}: ${v.id}`)).toEqual([])
      await controlePositivo(page, contexto)
    }

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Dois serviços pedem atenção.', level: 2 })).toBeVisible()
    await esperarOConteudo(page, '/')
    const funcionando = page.getByRole('region', { name: /^funcionando$/i })
    await expect(funcionando.getByRole('rowheader')).toHaveCount(30)
    const alturas = await funcionando
      .locator('tbody tr')
      .evaluateAll((linhas) => linhas.map((l) => l.getBoundingClientRect().height))
    expect(Math.max(...alturas), `${largura} px: a linha mais alta de "Funcionando"`).toBeLessThanOrEqual(48)
    await expect(page.getByRole('heading', { name: NOME_LONGO, level: 3 })).toBeVisible()
    await semRolagemLateral(`painel ${largura}`)
  })
}
```

- [ ] **Step 3: O fluxo encontra o alvo pela região, e o nome exato**

Em `web/e2e/fluxo.spec.ts`, no passo 4, troque:

```ts
    await page.getByRole('link', { name: 'Painel' }).click()
    const cartao = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: nomeDoAlvo }) })
    await expect(cartao).toBeVisible()
    await expect(cartao.getByText(/^(No ar|Degradado|Fora do ar)$/)).toBeVisible({ timeout: 30_000 })
```

por:

```ts
    // O alvo aparece primeiro em "Sem dados ou pausados" e, classificado,
    // passa a "Pedem atenção" (o .invalid fica fora do ar) ou a
    // "Funcionando" (um DNS que sequestra NXDOMAIN). exact: o cartão tem
    // também o "Ver incidente de <nome>".
    await page.getByRole('link', { name: 'Painel' }).click()
    await expect(page.getByRole('link', { name: nomeDoAlvo, exact: true })).toBeVisible()
    const classificado = page
      .getByRole('region', { name: /^(pedem atenção|funcionando)$/i })
      .getByRole('link', { name: nomeDoAlvo, exact: true })
    await expect(classificado).toBeVisible({ timeout: 30_000 })
```

No passo 6, troque:

```ts
    await page.getByRole('link', { name: nomeDoAlvo }).click()
```

por:

```ts
    await page.getByRole('link', { name: nomeDoAlvo, exact: true }).click()
```

- [ ] **Step 4: Rodar o e2e**

Run: `cd web && npm run build && npx playwright test`
Expected: PASS em todos os specs (precisa do Docker). Rode as sabotagens do Step 2, uma por vez, com `npm run build && npx playwright test e2e/a11y.spec.ts -g "cartão de problema|muitos alvos"`: tirar o `before:absolute` → o clique no canto não navega; `h-11` → `h-16` → a linha mais alta passa de 48. Desfaça e rode de novo.

- [ ] **Step 5: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint` e, na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 6: Commit e PR**

```bash
git add web/e2e
git commit -m "test(painel): e2e do painel novo (cartão clicável, 30 alvos, nome longo)"
git push -u origin feat/redesign-painel
gh pr create --title "feat(painel): painel por gravidade com manchete, cartões e linhas" --body "$(cat <<'EOF'
PR 3 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md (Telas > 1).

- Manchete gerada dos dados ("Dois serviços pedem atenção.", "Tudo no ar." com a hora do último incidente resolvido), com detalhe e meta "Atualizado às HH:MM · N de M no ar".
- Ordem por gravidade: cartões de problema clicáveis inteiros, com causa do incidente aberto; "Funcionando" em linhas de 44 px; "Sem dados ou pausados" em texto.
- Barra de 24 h com forma e cor; esqueleto que imita o layout.
- Sem API nova: a causa vem da consulta dos abertos que o contador já fazia; o último resolvido, de /incidents?status=resolved&limit=20, só com tudo no ar.
- e2e: cartão clicável, 32 alvos e nome de 100 caracteres a 400 e 1280 px.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Antes do merge, o dono revê o painel nos dois temas, a 400 e a 1280 px (`cd web && npm run dev` com a API local).

---

# PR 4: Detalhe do alvo

Branch: `feat/redesign-detalhe`, a partir da `main` com o PR 3 integrado.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-detalhe
```

### Task 18: Faixas "fora do ar" e pontos de latência a partir da série

**Files:**
- Modify: `web/src/features/alvos/serie.ts`
- Modify: `web/src/features/alvos/serie.test.ts`

**Interfaces:**
- Consumes: `estadoDaHora` (`@/components/Minigrafico`, Task 14); tipos `Check`, `Hora`, `Serie`.
- Produces:

```ts
// web/src/features/alvos/serie.ts
export type Intervalo = { de: number; ate: number }          // ms desde a época, [de, ate)
export type PontoDeLatencia = { t: number; ms: number }
export function faixasForaDoAr(s: Serie): Intervalo[]          // ordenadas, vizinhas fundidas
export function latenciasDaSerie(s: Serie): PontoDeLatencia[]  // bruta: checks que não caíram; horária: p95 (ou p50)
```

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/features/alvos/serie.test.ts`, troque o import por:

```ts
import { ehJanela, faixasForaDoAr, latenciasDaSerie, pontosBrutos, pontosHorarios, resumir } from './serie'
```

e acrescente no fim:

```ts
const t = (min: number) => Date.UTC(2026, 8, 24, 12, min)
const hora = (h: number, total: number, ok: number, p50: number | null = 100, p95: number | null = 300) => ({
  bucket: new Date(Date.UTC(2026, 8, 24, h)).toISOString(),
  total,
  ok,
  p50_ms: p50,
  p95_ms: p95,
  p99_ms: p95,
  max_ms: p95,
})

describe('faixasForaDoAr', () => {
  it('bruta: cada check fora do ar vai até o seguinte, e vizinhos se fundem', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'down', 10000), check(3, 'up', 100)] }
    expect(faixasForaDoAr(s)).toEqual([{ de: t(1), ate: t(3) }])
  })

  it('bruta: o último check fora do ar vai um passo adiante', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(2, 'up', 100), check(3, 'down', 10000)] }
    expect(faixasForaDoAr(s)).toEqual([{ de: t(3), ate: t(4) }])
  })

  // Review Focus 6: sem dois checks para medir o passo, um minuto.
  it('bruta: um check só, fora do ar, vale um minuto', () => {
    expect(faixasForaDoAr({ resolution: 'raw', items: [check(0, 'down', 10000)] })).toEqual([{ de: t(0), ate: t(1) }])
  })

  it('horária: a hora inteira abaixo de 99% no ar; degradada e vazia não entram', () => {
    const s: Serie = {
      resolution: '1h',
      items: [hora(8, 60, 60), hora(9, 60, 57), hora(10, 60, 0, null, null), hora(11, 1000, 995), hora(12, 0, 0, null, null)],
    }
    expect(faixasForaDoAr(s)).toEqual([{ de: Date.UTC(2026, 8, 24, 9), ate: Date.UTC(2026, 8, 24, 11) }])
  })

  it('série vazia não tem faixa', () => {
    expect(faixasForaDoAr({ resolution: 'raw', items: [] })).toEqual([])
  })
})

describe('latenciasDaSerie', () => {
  it('bruta: só os checks que não caíram', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'degraded', 2500)] }
    expect(latenciasDaSerie(s)).toEqual([
      { t: t(0), ms: 100 },
      { t: t(2), ms: 2500 },
    ])
  })

  it('horária: o topo da faixa (p95), o p50 sem p95, e nada sem os dois', () => {
    const s: Serie = { resolution: '1h', items: [hora(8, 60, 60, 100, 300), hora(9, 60, 60, 120, null), hora(10, 60, 0, null, null)] }
    expect(latenciasDaSerie(s)).toEqual([
      { t: Date.UTC(2026, 8, 24, 8), ms: 300 },
      { t: Date.UTC(2026, 8, 24, 9), ms: 120 },
    ])
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos/serie.test.ts`
Expected: FAIL com `faixasForaDoAr` e `latenciasDaSerie` não exportados.

- [ ] **Step 3: Implementar**

Em `web/src/features/alvos/serie.ts`, troque o import do topo por:

```ts
import { estadoDaHora } from '@/components/Minigrafico'
import type { Check, Hora, Serie } from '@/lib/api/schemas'
```

e acrescente no fim do arquivo:

```ts
export type Intervalo = { de: number; ate: number }
export type PontoDeLatencia = { t: number; ms: number }

const HORA_MS = 3_600_000
// Sem dois checks para medir o passo, um check fora do ar vale um minuto.
const PASSO_PADRAO_MS = 60_000

// As faixas hachuradas do gráfico (spec do redesign, Telas > 2). Bruta: cada
// check fora do ar vale até o check seguinte (o último, até um passo
// depois). Horária: a hora inteira abaixo de 99% no ar, o "fora" da barra
// de 24 h do painel. Faixas vizinhas se fundem.
export function faixasForaDoAr(s: Serie): Intervalo[] {
  return fundir(s.resolution === 'raw' ? faixasBrutas(s.items) : faixasHorarias(s.items))
}

function faixasBrutas(itens: Check[]): Intervalo[] {
  const marcas = itens.map((c) => ({ t: Date.parse(c.checked_at), fora: c.status === 'down' }))
  const passo = menorPasso(marcas.map((m) => m.t)) ?? PASSO_PADRAO_MS
  return marcas.flatMap((m, i) => (m.fora ? [{ de: m.t, ate: marcas[i + 1]?.t ?? m.t + passo }] : []))
}

function faixasHorarias(itens: Hora[]): Intervalo[] {
  return itens
    .filter((h) => h.total > 0 && estadoDaHora(h) === 'fora')
    .map((h) => {
      const de = Date.parse(h.bucket)
      return { de, ate: de + HORA_MS }
    })
}

function fundir(faixas: Intervalo[]): Intervalo[] {
  const fundidas: Intervalo[] = []
  for (const f of faixas) {
    const ultima = fundidas.at(-1)
    if (ultima !== undefined && ultima.ate >= f.de) ultima.ate = Math.max(ultima.ate, f.ate)
    else fundidas.push({ ...f })
  }
  return fundidas
}

function menorPasso(tempos: readonly number[]): number | null {
  const passos = tempos.slice(1).map((t, i) => t - (tempos[i] ?? t)).filter((d) => d > 0)
  return passos.length ? Math.min(...passos) : null
}

// Os pontos que a escala Y precisa cobrir: a latência dos checks que não
// caíram (bruta) ou o topo da faixa p50–p95 de cada hora (horária).
export function latenciasDaSerie(s: Serie): PontoDeLatencia[] {
  if (s.resolution === 'raw') {
    return s.items.filter((c) => c.status !== 'down').map((c) => ({ t: Date.parse(c.checked_at), ms: c.latency_ms }))
  }
  return s.items.flatMap((h) => {
    const ms = h.p95_ms ?? h.p50_ms
    return ms === null ? [] : [{ t: Date.parse(h.bucket), ms }]
  })
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos/serie.test.ts`
Expected: PASS.

Sabotagens: troque `marcas[i + 1]?.t ?? m.t + passo` por `m.t + passo` → a primeira faixa vira duas; troque `ultima.ate >= f.de` por `ultima.ate > f.de` → as horas 9 e 10 não se fundem; troque `estadoDaHora(h) === 'fora'` por `estadoDaHora(h) !== 'no-ar'` → a hora degradada entra. Desfaça.

- [ ] **Step 5: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/alvos/serie.ts web/src/features/alvos/serie.test.ts
git commit -m "feat(painel): faixas fora do ar e pontos de latência a partir da série"
```

### Task 19: Escala Y robusta, domínio e marcas do eixo X

**Files:**
- Modify: `web/src/lib/format.ts` (acrescenta `diaMes`), `web/src/lib/format.test.ts`
- Create: `web/src/features/alvos/escala.ts`, `web/src/features/alvos/escala.test.ts`

**Interfaces:**
- Consumes: `Intervalo`, `PontoDeLatencia` (Task 18); `horaMinuto` (Task 13); `milissegundos`.
- Produces:

```ts
// web/src/lib/format.ts
export function diaMes(instante: string | number): string    // "14/10", fuso do navegador

// web/src/features/alvos/escala.ts
export type EscalaY = { teto: number; pico: PontoDeLatencia | null; foraDaEscala: number }
export type EixoX = { marcas: number[]; porDia: boolean }
export function escalaRobusta(pontos: readonly PontoDeLatencia[]): EscalaY
export function textoDoPico(escala: EscalaY, comData: boolean): string | null
export function dominioX(tempos: readonly number[], faixas: readonly Intervalo[]): [number, number] | null
export function ticksDoEixoX(de: number, ate: number): EixoX
```

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/lib/format.test.ts`, inclua `diaMes` no import do topo e acrescente no fim:

```ts
describe('diaMes', () => {
  it('dia e mês com dois dígitos, no fuso do navegador', () => {
    expect(diaMes(new Date(2026, 9, 4, 23, 59).getTime())).toBe('04/10')
  })
})
```

Crie `web/src/features/alvos/escala.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { dominioX, escalaRobusta, textoDoPico, ticksDoEixoX } from './escala'

const ponto = (ms: number, t = 0) => ({ t, ms })
const vezes = (n: number, ms: number) => Array.from({ length: n }, (_, i) => ponto(ms, i))
// Datas pelo construtor local: os testes valem em qualquer fuso.
const local = (mes: number, dia: number, h: number, min = 0) => new Date(2026, mes - 1, dia, h, min).getTime()

describe('escalaRobusta', () => {
  it('sem pontos, o teto mínimo', () => {
    expect(escalaRobusta([])).toEqual({ teto: 10, pico: null, foraDaEscala: 0 })
  })

  // Review Focus 6.
  it('um ponto só: o próximo número redondo acima dele com 10% de ar', () => {
    expect(escalaRobusta([ponto(120)])).toEqual({ teto: 200, pico: null, foraDaEscala: 0 })
  })

  it('tudo perto do p95: a escala cobre o maior ponto', () => {
    const pontos = Array.from({ length: 20 }, (_, i) => ponto((i + 1) * 100, i))
    expect(escalaRobusta(pontos)).toEqual({ teto: 2500, pico: null, foraDaEscala: 0 })
  })

  // A spec: o teto vem do p95 com folga, e o pico acima dele é anotado em
  // vez de esmagar a série.
  it('um pico isolado fica fora da escala e é o pico anotado', () => {
    const pico = ponto(2400, 99)
    expect(escalaRobusta([...vezes(20, 100), pico])).toEqual({ teto: 200, pico, foraDaEscala: 1 })
  })

  it('vários pontos fora da escala: o pico é o maior, e a conta é de todos', () => {
    expect(escalaRobusta([...vezes(40, 100), ponto(1800, 98), ponto(2400, 99)])).toEqual({
      teto: 200,
      pico: ponto(2400, 99),
      foraDaEscala: 2,
    })
  })

  // Fronteira no teto: 200 cabe; 201 fica de fora.
  it('ponto igual ao teto cabe; um acima dele, não', () => {
    expect(escalaRobusta([...vezes(20, 100), ponto(200, 99)]).pico).toBeNull()
    expect(escalaRobusta([...vezes(20, 100), ponto(201, 99)]).pico).toEqual(ponto(201, 99))
  })

  it('latência zero não dá teto zero', () => {
    expect(escalaRobusta([ponto(0), ponto(0, 1)]).teto).toBe(10)
  })
})

describe('textoDoPico', () => {
  const pico = { t: local(10, 14, 3, 10), ms: 2400 }

  it('sem pico, nada', () => {
    expect(textoDoPico({ teto: 200, pico: null, foraDaEscala: 0 }, false)).toBeNull()
  })

  it('um ponto fora: o pico e a hora', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 1 }, false)).toBe('pico de 2.400 ms às 03:10, fora da escala')
  })

  it('janela de dias: o pico com a data', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 1 }, true)).toBe('pico de 2.400 ms em 14/10, 03:10, fora da escala')
  })

  it('vários pontos fora: quantos, e o pico', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 3 }, false)).toBe('3 pontos fora da escala; o pico, 2.400 ms às 03:10')
  })
})

describe('dominioX', () => {
  it('sem pontos, sem domínio (a tela mostra uma frase no lugar do gráfico)', () => {
    expect(dominioX([], [])).toBeNull()
  })

  it('do primeiro ao último ponto, esticado até o fim da última faixa', () => {
    expect(dominioX([0, 7_200_000], [{ de: 3_600_000, ate: 9_000_000 }])).toEqual([0, 9_000_000])
  })

  // Review Focus 6: um ponto só ganha 1 h de abertura, centrada nele.
  it('abertura mínima de 1 h, centrada', () => {
    expect(dominioX([10_000_000], [])).toEqual([10_000_000 - 1_800_000, 10_000_000 + 1_800_000])
  })
})

describe('ticksDoEixoX', () => {
  it('1 h: quartos de hora', () => {
    expect(ticksDoEixoX(local(10, 14, 9, 7), local(10, 14, 10, 7))).toEqual({
      marcas: [local(10, 14, 9, 15), local(10, 14, 9, 30), local(10, 14, 9, 45), local(10, 14, 10, 0)],
      porDia: false,
    })
  })

  it('a marca na ponta do domínio entra', () => {
    expect(ticksDoEixoX(local(10, 14, 9, 0), local(10, 14, 10, 0)).marcas).toEqual([
      local(10, 14, 9, 0),
      local(10, 14, 9, 15),
      local(10, 14, 9, 30),
      local(10, 14, 9, 45),
      local(10, 14, 10, 0),
    ])
  })

  it('6 h: horas cheias', () => {
    expect(ticksDoEixoX(local(10, 14, 3, 20), local(10, 14, 9, 20)).marcas).toEqual([4, 5, 6, 7, 8, 9].map((h) => local(10, 14, h)))
  })

  it('24 h: de seis em seis horas, contadas da meia-noite', () => {
    expect(ticksDoEixoX(local(10, 13, 9, 7), local(10, 14, 9, 7))).toEqual({
      marcas: [local(10, 13, 12), local(10, 13, 18), local(10, 14, 0), local(10, 14, 6)],
      porDia: false,
    })
  })

  it('7 dias: meias-noites, de dois em dois dias', () => {
    expect(ticksDoEixoX(local(10, 7, 9), local(10, 14, 9))).toEqual({
      marcas: [local(10, 9, 0), local(10, 11, 0), local(10, 13, 0)],
      porDia: true,
    })
  })

  it('90 dias: no máximo sete marcas', () => {
    const { marcas, porDia } = ticksDoEixoX(local(7, 16, 9), local(10, 14, 9))
    expect(porDia).toBe(true)
    expect(marcas.length).toBeGreaterThan(0)
    expect(marcas.length).toBeLessThanOrEqual(7)
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/lib/format.test.ts src/features/alvos/escala.test.ts`
Expected: FAIL. `diaMes` não exportado; `Failed to resolve import "./escala"`.

- [ ] **Step 3: `diaMes`**

Em `web/src/lib/format.ts`, logo depois de `horaMinuto`, acrescente:

```ts
const diaMesFmt = new Intl.DateTimeFormat('pt-BR', { day: '2-digit', month: '2-digit' })

// "14/10", no fuso do navegador: as marcas do eixo nas janelas de dias.
export function diaMes(instante: string | number): string {
  return diaMesFmt.format(new Date(instante))
}
```

- [ ] **Step 4: `escala.ts`**

Crie `web/src/features/alvos/escala.ts`:

```ts
import { diaMes, horaMinuto, milissegundos } from '@/lib/format'
import type { Intervalo, PontoDeLatencia } from './serie'

// A escala do gráfico de latência (spec do redesign, Telas > 2). Funções
// puras: o Recharts não desenha marcas de eixo no jsdom, e o que decide o
// desenho (o teto, o pico fora da escala, o domínio, as marcas) é testado
// aqui.

export type EscalaY = { teto: number; pico: PontoDeLatencia | null; foraDaEscala: number }
export type EixoX = { marcas: number[]; porDia: boolean }

// O teto vem do p95 dos pontos com 25% de folga: um pico isolado não esmaga
// a série. Se nada passa disso, a escala vai até o maior ponto, com 10% de
// ar acima dele.
const FOLGA_DO_P95 = 1.25
const AR_ACIMA_DO_MAIOR = 1.1
const TETO_MINIMO_MS = 10
const DEGRAUS = [1, 2, 2.5, 5, 10]

export function escalaRobusta(pontos: readonly PontoDeLatencia[]): EscalaY {
  if (pontos.length === 0) return { teto: TETO_MINIMO_MS, pico: null, foraDaEscala: 0 }
  const ordenados = pontos.map((p) => p.ms).sort((a, b) => a - b)
  // Posto mais próximo, como o resumir() da série.
  const p95 = ordenados[Math.max(0, Math.ceil(0.95 * ordenados.length) - 1)] ?? 0
  const maior = ordenados.at(-1) ?? 0
  const limite = p95 * FOLGA_DO_P95
  const teto = arredondarParaCima(maior <= limite ? maior * AR_ACIMA_DO_MAIOR : limite)
  const acima = pontos.filter((p) => p.ms > teto)
  const pico = acima.reduce<PontoDeLatencia | null>((m, p) => (m === null || p.ms > m.ms ? p : m), null)
  return { teto, pico, foraDaEscala: acima.length }
}

// O menor número redondo (1, 2, 2,5 ou 5 vezes uma potência de 10) que
// cobre x: as marcas do eixo Y saem limpas.
function arredondarParaCima(x: number): number {
  if (x <= TETO_MINIMO_MS) return TETO_MINIMO_MS
  const potencia = 10 ** Math.floor(Math.log10(x))
  return (DEGRAUS.find((d) => d * potencia >= x) ?? 10) * potencia
}

// A anotação da legenda: "pico de 2.400 ms às 03:10, fora da escala".
export function textoDoPico(escala: EscalaY, comData: boolean): string | null {
  const { pico, foraDaEscala } = escala
  if (pico === null) return null
  const quando = comData ? `em ${diaMes(pico.t)}, ${horaMinuto(pico.t)}` : `às ${horaMinuto(pico.t)}`
  const valor = `${milissegundos(pico.ms)} ${quando}`
  return foraDaEscala === 1 ? `pico de ${valor}, fora da escala` : `${foraDaEscala} pontos fora da escala; o pico, ${valor}`
}

// Abertura mínima de 1 h: uma série de um ponto só fica no meio de uma
// janela com marcas, e não num eixo de largura zero.
const ABERTURA_MINIMA_MS = 3_600_000

export function dominioX(tempos: readonly number[], faixas: readonly Intervalo[]): [number, number] | null {
  if (tempos.length === 0) return null
  const de = Math.min(...tempos, ...faixas.map((f) => f.de))
  const ate = Math.max(...tempos, ...faixas.map((f) => f.ate))
  if (ate - de >= ABERTURA_MINIMA_MS) return [de, ate]
  const meio = (de + ate) / 2
  return [meio - ABERTURA_MINIMA_MS / 2, meio + ABERTURA_MINIMA_MS / 2]
}

const MAXIMO_DE_MARCAS = 6
const PASSOS_EM_MINUTOS = [15, 30, 60, 120, 180, 360, 720]
const PASSOS_EM_DIAS = [1, 2, 7, 14]

// Marcas em horas redondas no fuso do navegador (quartos e meias horas na
// janela de 1 h; meias-noites nas de dias): o menor passo que dá até seis.
// As datas saem do construtor local, e não de soma de milissegundos, para
// uma troca de horário de verão não deslocar as marcas.
export function ticksDoEixoX(de: number, ate: number): EixoX {
  for (const minutos of PASSOS_EM_MINUTOS) {
    const marcas = marcasDesdeAMeiaNoite(de, ate, (d, k) => new Date(d.getFullYear(), d.getMonth(), d.getDate(), 0, k * minutos))
    if (marcas.length <= MAXIMO_DE_MARCAS) return { marcas, porDia: false }
  }
  let marcas: number[] = []
  for (const dias of PASSOS_EM_DIAS) {
    marcas = marcasDesdeAMeiaNoite(de, ate, (d, k) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + k * dias))
    if (marcas.length <= MAXIMO_DE_MARCAS) break
  }
  return { marcas, porDia: true }
}

// Para no primeiro passo que passa do teto: quem chama só quer saber se
// cabem até seis.
function marcasDesdeAMeiaNoite(de: number, ate: number, marca: (meiaNoite: Date, k: number) => Date): number[] {
  const inicio = new Date(de)
  const meiaNoite = new Date(inicio.getFullYear(), inicio.getMonth(), inicio.getDate())
  const marcas: number[] = []
  for (let k = 0; marcas.length <= MAXIMO_DE_MARCAS; k++) {
    const t = marca(meiaNoite, k).getTime()
    if (t > ate) break
    if (t >= de) marcas.push(t)
  }
  return marcas
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/lib/format.test.ts src/features/alvos/escala.test.ts`
Expected: PASS.

Sabotagens (uma por vez): troque `p.ms > teto` por `p.ms >= teto` → "ponto igual ao teto cabe" falha; troque `FOLGA_DO_P95 = 1.25` por `FOLGA_DO_P95 = 100` → o pico isolado volta a esmagar a série (teto 5000, sem pico); tire o bloco de abertura mínima de `dominioX` → "abertura mínima" falha; troque `t >= de` por `t > de` → "a marca na ponta do domínio entra" falha. Desfaça.

- [ ] **Step 6: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/format.ts web/src/lib/format.test.ts web/src/features/alvos/escala.ts web/src/features/alvos/escala.test.ts
git commit -m "feat(painel): escala Y robusta com pico anotado, domínio e marcas do eixo X"
```

### Task 20: O gráfico de latência novo

**Files:**
- Modify: `web/src/features/alvos/GraficoDeLatencia.tsx` (arquivo inteiro)
- Modify: `web/src/features/alvos/grafico.test.tsx`
- Modify: `web/src/index.css` (sai `--grafico-uptime` dos dois temas)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: Tasks 18 e 19; `pontosBrutos`, `pontosHorarios`, `type PontoBruto`, `type PontoHorario`; `dataHora`, `diaMes`, `horaMinuto`; `--grafico-linha`, `--grafico-faixa`, `--estado-fora`, `--borda`, `--suave`.
- Produces: `GraficoDeLatencia({ serie, descritoPor }: { serie: Serie; descritoPor?: string })`, com a mesma assinatura. Devolve `null` para série vazia (a tela mostra a frase). O `<figure data-testid="grafico-latencia">` continua; cada faixa fora do ar é um `.recharts-reference-area-rect` com `fill="url(#hachura-…)"`, e o padrão é um `<pattern id="hachura-…">` com um `<path stroke="var(--estado-fora)">`. A legenda é uma `<ul>`; o pico, um `<p>` cujo id entra no `aria-describedby` do `<svg>`.

- [ ] **Step 1: Escrever os testes que falham**

No fim de `web/src/features/alvos/grafico.test.tsx`, acrescente (e troque o import do vitest por `import { describe, expect, it, vi } from 'vitest'`):

```tsx
// Datas pelo construtor local: as horas da legenda valem em qualquer fuso.
const check = (minuto: number, status: 'up' | 'down', latency_ms: number) => ({
  checked_at: new Date(2026, 9, 14, 3, minuto).toISOString(),
  status,
  latency_ms,
  status_code: status === 'down' ? null : 200,
  error_kind: status === 'down' ? 'timeout' : null,
})

describe('o gráfico do redesign', () => {
  // A spec: o pico acima do teto é anotado na legenda em vez de esmagar a
  // série, e o leitor de tela o ouve junto do resumo. Sabotagem: tirar o id
  // do pico do aria-describedby.
  it('o pico fora da escala vai para a legenda e para a descrição do <svg>', () => {
    render(
      <>
        <p id="resumo">Mediana 100 ms.</p>
        <GraficoDeLatencia
          descritoPor="resumo"
          serie={{ resolution: 'raw', items: [...Array.from({ length: 20 }, (_, i) => check(i, 'up', 100)), check(20, 'up', 2400)] }}
        />
      </>,
    )
    expect(screen.getByText('pico de 2.400 ms às 03:20, fora da escala')).toBeInTheDocument()
    expect(screen.getByRole('application', { name: 'Gráfico de latência' })).toHaveAccessibleDescription(
      'Mediana 100 ms. pico de 2.400 ms às 03:20, fora da escala',
    )
  })

  it('sem nada fora da escala, a legenda não fala de pico', () => {
    render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 100), check(1, 'up', 120)] }} />)
    expect(screen.queryByText(/pico/)).not.toBeInTheDocument()
  })

  // Forma + cor: a queda é uma faixa hachurada com o padrão do próprio
  // gráfico. Sabotagem: pintar a faixa de var(--estado-fora) liso.
  it('a queda vira faixa hachurada, e a legenda diz "Fora do ar"', () => {
    const { container } = render(
      <GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'up', 100)] }} />,
    )
    const areas = container.querySelectorAll('.recharts-reference-area-rect')
    expect(areas).toHaveLength(1)
    const id = areas[0]?.getAttribute('fill')?.match(/^url\(#(.+)\)$/)?.[1]
    expect(id).toMatch(/^hachura-/)
    expect(container.querySelector(`pattern[id="${id}"] path`)).toHaveAttribute('stroke', 'var(--estado-fora)')
    expect(screen.getByText('Fora do ar')).toBeInTheDocument()
  })

  it('horária: legenda de mediana e faixa p50–p95, e a hora com falhas hachurada', () => {
    const { container } = render(
      <GraficoDeLatencia
        serie={{
          resolution: '1h',
          items: [
            { bucket: '2026-10-14T06:00:00Z', total: 60, ok: 60, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
            { bucket: '2026-10-14T07:00:00Z', total: 60, ok: 57, p50_ms: 120, p95_ms: 320, p99_ms: 400, max_ms: 500 },
          ],
        }}
      />,
    )
    expect(screen.getByText('Mediana')).toBeInTheDocument()
    expect(screen.getByText('p50–p95')).toBeInTheDocument()
    expect(screen.getByText('Horas com menos de 99% no ar')).toBeInTheDocument()
    expect(container.querySelectorAll('.recharts-reference-area-rect')).toHaveLength(1)
  })

  // Review Focus 6. Caracterização: passa de primeira; a fronteira (a
  // abertura mínima do domínio) é testada em escala.test.ts.
  it('uma série de um ponto só desenha sem coordenadas inválidas', () => {
    const { container } = render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 120)] }} />)
    expect(screen.getByRole('application', { name: 'Gráfico de latência' })).toBeInTheDocument()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('série vazia não desenha nada', () => {
    const { container } = render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [] }} />)
    expect(container).toBeEmptyDOMElement()
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/alvos/GraficoDeLatencia.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos/grafico.test.tsx src/sistema.test.ts`
Expected: FAIL. Não há legenda, nem pico, nem `ReferenceArea`; a série vazia desenha o `<figure>`; Passa o teste antigo do nome e o de um ponto só.

- [ ] **Step 3: Reescrever o gráfico**

Substitua `web/src/features/alvos/GraficoDeLatencia.tsx` por:

```tsx
import { useId } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, diaMes, horaMinuto } from '@/lib/format'
import { dominioX, escalaRobusta, textoDoPico, ticksDoEixoX } from './escala'
import { faixasForaDoAr, latenciasDaSerie, pontosBrutos, pontosHorarios, type PontoBruto, type PontoHorario } from './serie'

// Recharts, só aqui: este arquivo entra no chunk do detalhe, carregado sob
// demanda. As cores são variáveis CSS do index.css, e não um <style> gerado
// em runtime, que a CSP bloquearia sem nonce (spike do Plano 6, §8.1).
//
// O desenho da spec do redesign (Telas > 2): a linha da mediana em índigo, a
// faixa p50–p95 em primária 100, o "fora do ar" hachurado (forma + cor), as
// marcas do eixo X em horas redondas e a escala Y robusta, com o pico fora
// dela anotado na legenda. O que decide o desenho está em escala.ts e
// serie.ts, testado sem o Recharts.
//
// descritoPor: o id do resumo em texto, que descreve o gráfico.
export function GraficoDeLatencia({ serie, descritoPor }: { serie: Serie; descritoPor?: string }) {
  // O id entra num url(#…): só letras, dígitos, _ e -.
  const id = useId().replace(/[^\w-]/g, '')
  const idDaHachura = `hachura-${id}`
  const idDoPico = `pico-${id}`
  const horaria = serie.resolution === '1h'
  const dados: (PontoBruto | PontoHorario)[] = horaria ? pontosHorarios(serie.items) : pontosBrutos(serie.items)
  const faixas = faixasForaDoAr(serie)
  const dominio = dominioX(
    dados.map((p) => p.t),
    faixas,
  )
  if (dominio === null) return null
  const escala = escalaRobusta(latenciasDaSerie(serie))
  const eixo = ticksDoEixoX(dominio[0], dominio[1])
  const pico = textoDoPico(escala, eixo.porDia)
  const descricao = [descritoPor, pico ? idDoPico : undefined].filter(Boolean).join(' ') || undefined
  return (
    <div className="space-y-3">
      {/* O nome e a descrição ficam no <svg> do Recharts, que é o que recebe
          o foco (a accessibilityLayer: role="application", tabIndex 0). */}
      <figure className="h-64 w-full" data-testid="grafico-latencia">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart
            data={dados}
            margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
            aria-label="Gráfico de latência"
            aria-describedby={descricao}
          >
            <defs>
              <pattern id={idDaHachura} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <path d="M0 0V6" stroke="var(--estado-fora)" strokeWidth={2} />
              </pattern>
            </defs>
            <CartesianGrid stroke="var(--borda)" vertical={false} />
            <XAxis
              dataKey="t"
              type="number"
              scale="time"
              domain={dominio}
              ticks={eixo.marcas}
              tickFormatter={eixo.porDia ? diaMes : horaMinuto}
              stroke="var(--suave)"
            />
            {/* allowDataOverflow: o que passa do teto é cortado, e o pico vai
                para a legenda. */}
            <YAxis domain={[0, escala.teto]} allowDataOverflow stroke="var(--suave)" unit=" ms" width={72} />
            <Tooltip
              labelFormatter={(t) => dataHora(new Date(Number(t)).toISOString())}
              contentStyle={{ background: 'var(--superficie)', borderColor: 'var(--borda)', color: 'var(--texto)' }}
            />
            {faixas.map((f) => (
              <ReferenceArea
                key={f.de}
                x1={f.de}
                x2={f.ate}
                fill={`url(#${idDaHachura})`}
                fillOpacity={1}
                stroke="none"
                ifOverflow="hidden"
              />
            ))}
            {horaria ? (
              <>
                <Area dataKey="faixa" name="p50–p95" stroke="none" fill="var(--grafico-faixa)" isAnimationActive={false} />
                <Line dataKey="p50" name="Mediana" stroke="var(--grafico-linha)" strokeWidth={2} dot={false} isAnimationActive={false} />
              </>
            ) : (
              <Line
                dataKey="latencia"
                name="Latência"
                stroke="var(--grafico-linha)"
                strokeWidth={2}
                dot={false}
                connectNulls={false}
                isAnimationActive={false}
              />
            )}
          </ComposedChart>
        </ResponsiveContainer>
      </figure>
      <Legenda horaria={horaria} comFalhas={faixas.length > 0} idDaHachura={idDaHachura} />
      {pico && (
        <p id={idDoPico} className="text-meta text-suave">
          {pico}
        </p>
      )}
    </div>
  )
}

// A legenda explícita: o leitor de tela já tem a tabela e o resumo, e a
// legenda diz o que cada forma significa para quem olha.
function Legenda({ horaria, comFalhas, idDaHachura }: { horaria: boolean; comFalhas: boolean; idDaHachura: string }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-2 text-meta text-suave">
      <li className="flex items-center gap-2">
        <span aria-hidden="true" className="h-1 w-4 rounded-full bg-primaria" />
        {horaria ? 'Mediana' : 'Latência'}
      </li>
      {horaria && (
        <li className="flex items-center gap-2">
          <span aria-hidden="true" className="size-3 rounded-full bg-primaria-100" />
          p50–p95
        </li>
      )}
      {comFalhas && (
        <li className="flex items-center gap-2">
          <svg aria-hidden="true" width="16" height="12">
            <rect width="16" height="12" fill={`url(#${idDaHachura})`} />
          </svg>
          {horaria ? 'Horas com menos de 99% no ar' : 'Fora do ar'}
        </li>
      )}
    </ul>
  )
}
```

- [ ] **Step 4: O token sem uso**

Em `web/src/index.css`, apague a linha `--grafico-uptime: oklch(0.88 0.05 150);` do `:root` e a linha `--grafico-uptime: oklch(0.4 0.06 150);` do `.dark`: as barras de uptime horário saíram do gráfico.

Confira: `cd web && grep -rn "grafico-uptime" src e2e`
Expected: nenhuma linha (o e2e que a usava muda na Task 24).

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos src/sistema.test.ts src/tokens.test.ts`
Expected: PASS.

Sabotagens: em `descricao`, use só `descritoPor` → o primeiro teste novo falha; troque `fill={`url(#${idDaHachura})`}` da `ReferenceArea` por `fill="var(--estado-fora)"` → "a queda vira faixa hachurada" falha. Desfaça.

- [ ] **Step 6: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: verde. A página ainda passa uma série vazia ao gráfico (a frase vem na Task 23), e o gráfico devolve `null`; o `detalhe.test.tsx` não depende do gráfico vazio.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/alvos/GraficoDeLatencia.tsx web/src/features/alvos/grafico.test.tsx web/src/index.css web/src/sistema.test.ts
git commit -m "feat(painel): gráfico com mediana, faixa p50–p95, queda hachurada e pico anotado"
```

### Task 21: Estado do alvo, indicadores e selo do incidente

**Files:**
- Create: `web/src/features/alvos/estado-do-alvo.ts`, `web/src/features/alvos/estado-do-alvo.test.ts`
- Create: `web/src/components/SeloDoIncidente.tsx`, `web/src/components/selo-do-incidente.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `estadoExibido`, `textoDoEstado`, `Selo`, `type EstadoExibido`, `type TomDoSelo` (PR 1); `textoDaCausa`, `horaMinuto` (Task 13); `dataHora`, `duracao`, `haQuanto`, `milissegundos`, `porcento`.
- Produces:

```ts
// web/src/features/alvos/estado-do-alvo.ts
export type Faixa = { estado: EstadoExibido; titulo: string; detalhe: string }
export function faixaDoEstado(item: ItemDoResumo, aberto: Incidente | null, agora: Date): Faixa
export type ValorDoIndicador = { valor: string; unidade: string }
export function emPorcento(fracao: number | null): ValorDoIndicador        // 0,99999 → { '99,99', '%' }
export function emMilissegundos(ms: number | null): ValorDoIndicador      // 1234,4 → { '1.234', 'ms' }
export function ultimoCheck(iso: string | null, agora: Date): ValorDoIndicador  // { 'HH:MM', 'há N minutos' } ou { '—', 'nunca' }

// web/src/components/SeloDoIncidente.tsx
export type SituacaoDoIncidente = 'em-andamento' | 'reconhecido' | 'resolvido'
export function situacaoDoIncidente(i: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>): SituacaoDoIncidente
export function periodoDoIncidente(i: Pick<Incidente, 'started_at' | 'resolved_at'>, agora: Date): string  // "início 24/09/2026, 08:00 · há 1 h 0 min" | "… · durou 30 min"
export function SeloDoIncidente(props: { incidente: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>; className?: string })
// "Em andamento" (tom fora, CircleAlert), "Reconhecido" (tom primaria, Check), "Resolvido" (tom sem-dados, CircleCheck)
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/features/alvos/estado-do-alvo.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { emMilissegundos, emPorcento, faixaDoEstado, ultimoCheck } from './estado-do-alvo'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const agora = new Date('2026-09-24T12:00:00Z')

describe('faixaDoEstado', () => {
  it('fora do ar com incidente aberto: há quanto tempo, a causa e o início', () => {
    const f = faixaDoEstado(alvo({ state: 'down' }), inc({ started_at: '2026-09-24T09:29:00Z', cause: 'conn_refused' }), agora)
    expect(f.estado).toBe('down')
    expect(f.titulo).toBe('Fora do ar há 2 h 31 min')
    expect(f.detalhe).toMatch(/^Conexão recusada · desde 24\/09\/2026/)
  })

  // Review Focus 7.
  it('incidente sem causa', () => {
    expect(faixaDoEstado(alvo({ state: 'down' }), inc({ cause: '' }), agora).detalhe).toMatch(/^Causa não registrada · desde /)
  })

  it('sem incidente: o estado e o último check', () => {
    expect(faixaDoEstado(alvo({ last_checked_at: '2026-09-24T11:57:00Z' }), null, agora)).toEqual({
      estado: 'up',
      titulo: 'No ar',
      detalhe: 'Último check há 3 minutos.',
    })
  })

  it('degradado sem incidente: a latência do último check', () => {
    expect(faixaDoEstado(alvo({ state: 'degraded', last_latency_ms: 2400 }), null, agora).detalhe).toBe(
      'Latência de 2.400 ms no último check.',
    )
  })

  // Review Focus 4.
  it('alvo sem nenhum check', () => {
    expect(faixaDoEstado(alvo({ state: 'unknown', last_checked_at: null }), null, agora)).toEqual({
      estado: 'unknown',
      titulo: 'Sem dados',
      detalhe: 'Nenhum check ainda.',
    })
  })

  it('pausado, mesmo com incidente aberto, diz que os checks pararam', () => {
    expect(faixaDoEstado(alvo({ state: 'down', enabled: false }), inc(), agora)).toEqual({
      estado: 'pausado',
      titulo: 'Pausado',
      detalhe: 'Os checks estão parados. Retome para voltar a vigiar.',
    })
  })
})

describe('valores dos indicadores', () => {
  it('porcentagem truncada, com a unidade à parte', () => {
    expect(emPorcento(0.99999)).toEqual({ valor: '99,99', unidade: '%' })
    expect(emPorcento(1)).toEqual({ valor: '100,00', unidade: '%' })
    expect(emPorcento(null)).toEqual({ valor: '—', unidade: '' })
  })

  it('milissegundos inteiros, com separador de milhar', () => {
    expect(emMilissegundos(1234.4)).toEqual({ valor: '1.234', unidade: 'ms' })
    expect(emMilissegundos(0)).toEqual({ valor: '0', unidade: 'ms' })
    expect(emMilissegundos(null)).toEqual({ valor: '—', unidade: '' })
  })

  it('último check: a hora e há quanto tempo; nunca verificado, traço', () => {
    expect(ultimoCheck(new Date(2026, 8, 24, 11, 59).toISOString(), new Date(2026, 8, 24, 12, 0))).toEqual({
      valor: '11:59',
      unidade: 'há 1 minuto',
    })
    expect(ultimoCheck(null, agora)).toEqual({ valor: '—', unidade: 'nunca' })
  })
})
```

Crie `web/src/components/selo-do-incidente.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { periodoDoIncidente, SeloDoIncidente } from './SeloDoIncidente'

describe('SeloDoIncidente', () => {
  it.each([
    [{ resolved_at: null, acknowledged_at: null }, 'Em andamento', 'bg-fora-100'],
    [{ resolved_at: null, acknowledged_at: '2026-09-24T11:10:00Z' }, 'Reconhecido', 'bg-primaria-100'],
    [{ resolved_at: '2026-09-24T11:30:00Z', acknowledged_at: '2026-09-24T11:10:00Z' }, 'Resolvido', 'bg-sem-dados-100'],
  ])('%j → "%s"', (i, texto, fundo) => {
    const { container } = render(<SeloDoIncidente incidente={i} />)
    expect(screen.getByText(texto)).toHaveClass('rounded-full', fundo)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('periodoDoIncidente', () => {
  const agora = new Date('2026-09-24T12:00:00Z')

  it('aberto: o início e há quanto tempo', () => {
    expect(periodoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: null }, agora)).toMatch(/^início .+ · há 1 h 0 min$/)
  })

  it('resolvido: o início e quanto durou', () => {
    expect(periodoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: '2026-09-24T11:30:00Z' }, agora)).toMatch(
      /^início .+ · durou 30 min$/,
    )
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/SeloDoIncidente.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos/estado-do-alvo.test.ts src/components/selo-do-incidente.test.tsx`
Expected: FAIL com `Failed to resolve import "./estado-do-alvo"` e `"./SeloDoIncidente"`.

- [ ] **Step 3: `estado-do-alvo.ts`**

Crie `web/src/features/alvos/estado-do-alvo.ts`:

```ts
import { textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { dataHora, duracao, haQuanto, horaMinuto, milissegundos, porcento } from '@/lib/format'

// O topo do detalhe (spec do redesign, Telas > 2) em funções puras: a faixa
// de estado e os valores dos quatro indicadores.

export type Faixa = { estado: EstadoExibido; titulo: string; detalhe: string }

// O título diz o estado e, com incidente aberto, há quanto tempo; o
// detalhe, a causa e o início, ou o que se sabe do último check. Pausado
// vence: os checks pararam, e o incidente em aberto não muda isso.
export function faixaDoEstado(item: ItemDoResumo, aberto: Incidente | null, agora: Date): Faixa {
  const estado = estadoExibido(item.state, item.enabled)
  if (estado === 'pausado') {
    return { estado, titulo: 'Pausado', detalhe: 'Os checks estão parados. Retome para voltar a vigiar.' }
  }
  const nome = textoDoEstado(estado)
  if (aberto) {
    return {
      estado,
      titulo: `${nome} há ${duracao(agora.getTime() - Date.parse(aberto.started_at))}`,
      detalhe: `${textoDaCausa(aberto.cause)} · desde ${dataHora(aberto.started_at)}`,
    }
  }
  if (estado === 'degraded' && item.last_latency_ms !== null) {
    return { estado, titulo: nome, detalhe: `Latência de ${milissegundos(item.last_latency_ms)} no último check.` }
  }
  return {
    estado,
    titulo: nome,
    detalhe: item.last_checked_at === null ? 'Nenhum check ainda.' : `Último check ${haQuanto(item.last_checked_at, agora)}.`,
  }
}

// O número grande e a unidade à parte (spec: Inter 600 de 28 px, unidade
// em 13 px).
export type ValorDoIndicador = { valor: string; unidade: string }

const SEM_VALOR: ValorDoIndicador = { valor: '—', unidade: '' }

// Pelo porcento(), que trunca: "100,00" nunca esconde uma falha.
export function emPorcento(fracao: number | null): ValorDoIndicador {
  return fracao === null ? SEM_VALOR : { valor: porcento(fracao).slice(0, -1), unidade: '%' }
}

export function emMilissegundos(ms: number | null): ValorDoIndicador {
  return ms === null ? SEM_VALOR : { valor: Math.round(ms).toLocaleString('pt-BR'), unidade: 'ms' }
}

export function ultimoCheck(iso: string | null, agora: Date): ValorDoIndicador {
  return iso === null ? { valor: '—', unidade: 'nunca' } : { valor: horaMinuto(iso), unidade: haQuanto(iso, agora) }
}
```

- [ ] **Step 4: `SeloDoIncidente.tsx`**

Crie `web/src/components/SeloDoIncidente.tsx`:

```tsx
import { Check, CircleAlert, CircleCheck, type LucideIcon } from 'lucide-react'
import type { Incidente } from '@/lib/api/schemas'
import { dataHora, duracao } from '@/lib/format'
import { Selo, type TomDoSelo } from './SeloDeEstado'

// O incidente na tela: a situação (em andamento, reconhecido, resolvido), o
// período e o selo (spec do redesign, Telas > 2 e 3). O detalhe do alvo e a
// tela de incidentes usam os três.
export type SituacaoDoIncidente = 'em-andamento' | 'reconhecido' | 'resolvido'

export function situacaoDoIncidente(i: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>): SituacaoDoIncidente {
  if (i.resolved_at !== null) return 'resolvido'
  return i.acknowledged_at === null ? 'em-andamento' : 'reconhecido'
}

export function periodoDoIncidente(i: Pick<Incidente, 'started_at' | 'resolved_at'>, agora: Date): string {
  const inicio = Date.parse(i.started_at)
  const quanto = i.resolved_at ? `durou ${duracao(Date.parse(i.resolved_at) - inicio)}` : `há ${duracao(agora.getTime() - inicio)}`
  return `início ${dataHora(i.started_at)} · ${quanto}`
}

// Forma + cor + texto, como o selo de estado.
const SELOS: Record<SituacaoDoIncidente, { texto: string; tom: TomDoSelo; Icone: LucideIcon }> = {
  'em-andamento': { texto: 'Em andamento', tom: 'fora', Icone: CircleAlert },
  reconhecido: { texto: 'Reconhecido', tom: 'primaria', Icone: Check },
  resolvido: { texto: 'Resolvido', tom: 'sem-dados', Icone: CircleCheck },
}

export function SeloDoIncidente({
  incidente,
  className,
}: {
  incidente: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>
  className?: string
}) {
  const { texto, tom, Icone } = SELOS[situacaoDoIncidente(incidente)]
  return (
    <Selo tom={tom} Icone={Icone} className={className}>
      {texto}
    </Selo>
  )
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos/estado-do-alvo.test.ts src/components/selo-do-incidente.test.tsx src/sistema.test.ts`
Expected: PASS.

Sabotagens: em `faixaDoEstado`, mova o ramo do pausado para depois do `if (aberto)` → "pausado, mesmo com incidente aberto" falha; em `situacaoDoIncidente`, teste o `acknowledged_at` antes do `resolved_at` → o resolvido e reconhecido vira "Reconhecido". Desfaça.

- [ ] **Step 6: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/alvos/estado-do-alvo.ts web/src/features/alvos/estado-do-alvo.test.ts web/src/components/SeloDoIncidente.tsx web/src/components/selo-do-incidente.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): estado do alvo, valores dos indicadores e selo do incidente"
```

### Task 22: Faixa de estado com Reconhecer

**Files:**
- Create: `web/src/features/alvos/FaixaDeEstado.tsx`
- Modify: `web/src/features/alvos/PaginaDetalhe.tsx` (consulta de incidentes no topo; a faixa no lugar do selo)
- Modify: `web/src/features/alvos/detalhe.test.tsx` (testes novos)
- Modify: `web/src/tokens.test.ts` (pares da faixa)
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `faixaDoEstado` (Task 21); `Selo`, `type EstadoExibido`; `Botao`; `useApi`; `naoEncontrado`; `mensagemDe`; `dataHora`; `cn`.
- Produces:

```ts
// web/src/features/alvos/FaixaDeEstado.tsx
export function FaixaDeEstado(props: {
  item: ItemDoResumo                       // o alvo no resumo
  incidentes: readonly Incidente[]         // os recentes do alvo; o aberto é o de target_id igual e resolved_at null
  agora: Date
  aoSumir: () => void                      // 404 no ack: o alvo foi apagado
  aoErrar: (mensagem: string | null) => void
})
// <section aria-labelledby> cujo nome é o título ("Fora do ar há 2 h 31 min"); botão "Reconhecer"
// (primário) com a descrição "Reconhecer interrompe os lembretes; não resolve o incidente."
```

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/tokens.test.ts`, logo antes de `const SOBRE_TINTA`, acrescente:

```ts
const TONS_100 = ['estado-no-ar-100', 'estado-degradado-100', 'estado-fora-100', 'estado-sem-dados-100']
```

No fim da lista `SOBRE_TINTA` (antes do `]`), acrescente:

```ts
  // A faixa de estado do detalhe (PR 4): título em texto e detalhe em suave
  // sobre o tom 100. O apagado não entra: no escuro fica em 4,1:1.
  ...['texto', 'suave'].flatMap((t) => TONS_100.map((f): [string, string] => [t, f])),
```

No fim da lista `COMPONENTES` (antes do `]`), acrescente:

```ts
  // O Reconhecer da faixa é primário, com o foco por cima do tom 100. O
  // secundário não serviria: a borda de controle fica em 2,7:1 no escuro.
  ...['primaria', 'foco'].flatMap((c) => TONS_100.map((f): [string, string] => [c, f])),
```

No fim do `describe('detalhe', …)` de `web/src/features/alvos/detalhe.test.tsx` (antes do `})` que o fecha), acrescente:

```tsx
  describe('faixa de estado', () => {
    // Spec, Telas > 2: a faixa diz há quanto tempo o alvo está fora, a causa
    // e o início, com Reconhecer. O foco vai ao "Reconhecido" quando o botão
    // some (o cuidado I1 da tela de incidentes). Sabotagem: tirar o
    // "focarDepois.current = true" → o foco cai no <body>.
    it('fora do ar com incidente aberto: tempo, causa e Reconhecer, que manda o ack e leva o foco', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente({ cause: 'conn_refused' })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: /^Fora do ar há / })
      expect(within(faixa).getByText(/^Conexão recusada · desde /)).toBeInTheDocument()
      const botao = within(faixa).getByRole('button', { name: 'Reconhecer' })
      expect(botao).toHaveAccessibleDescription('Reconhecer interrompe os lembretes; não resolve o incidente.')
      await r.usuario.click(botao)
      const feito = await within(faixa).findByText(/lembretes parados desde/)
      await waitFor(() => expect(feito).toHaveFocus())
      expect(within(faixa).queryByRole('button', { name: 'Reconhecer' })).not.toBeInTheDocument()
      expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
    })

    it('sem incidente aberto, a faixa diz o estado e o último check, sem Reconhecer', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo()]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'No ar' })
      expect(within(faixa).getByText(/^Último check /)).toBeInTheDocument()
      expect(within(faixa).queryByRole('button')).not.toBeInTheDocument()
    })

    it('pausado: a faixa diz que os checks estão parados', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo({ enabled: false })]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', enabled: false })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'Pausado' })
      expect(within(faixa).getByText('Os checks estão parados. Retome para voltar a vigiar.')).toBeInTheDocument()
    })

    // Review Focus 4.
    it('alvo sem nenhum check: "Sem dados" e "Nenhum check ainda."', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'Sem dados' })
      expect(within(faixa).getByText('Nenhum check ainda.')).toBeInTheDocument()
    })

    // M3: o incidente sumiu com o alvo, apagado em outra aba. Sabotagem:
    // tirar o ramo do naoEncontrado em reconhecer() → o erro cru fica na tela.
    it('o ack com 404 explica e volta à lista', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente()]
      await r.cliente.refetchQueries()
      const botao = await screen.findByRole('button', { name: 'Reconhecer' })
      r.falsa.estado.incidentes = []
      await r.usuario.click(botao)
      expect((await screen.findAllByText('O alvo já tinha sido apagado, talvez em outra aba.'))[0]).toBeInTheDocument()
      await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    })

    it('um ack que falha mostra o erro e mantém o Reconhecer', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente()]
      await r.cliente.refetchQueries()
      r.falsa.estado.forcar.set('POST /incidents/:id/ack', { status: 502, code: 'unavailable' })
      await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer' }))
      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Reconhecer' })).toBeInTheDocument()
    })
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/alvos/FaixaDeEstado.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/tokens.test.ts src/features/alvos/detalhe.test.tsx`
Expected: os pares novos do `tokens.test.ts` passam (os valores foram calculados antes: `suave` sobre os tons 100 fica entre 5,93 e 6,83:1, `primaria` entre 6,08 e 6,47:1). Os testes da faixa FALHAM: não há região com esses nomes. Sabotagem dos pares, para vê-los morder: troque `['texto', 'suave']` por `['texto', 'suave', 'apagado']` → falham os quatro pares do `apagado` no escuro (4,08 a 4,31); troque `['primaria', 'foco']` por `['primaria', 'foco', 'borda-controle']` → falham os quatro da borda no escuro (2,72 a 2,87). Desfaça.

- [ ] **Step 3: O componente**

Crie `web/src/features/alvos/FaixaDeEstado.tsx`:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { Check, Circle, CircleDashed, Pause, Triangle, X, type LucideIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useApi } from '@/app/contexto'
import { Selo } from '@/components/SeloDeEstado'
import { Botao } from '@/components/ui/botao'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { faixaDoEstado } from './estado-do-alvo'

// Forma (ícone), cor (fundo 100 e ícone) e texto (título), como o selo. O
// texto da faixa é texto e suave, os pares do src/tokens.test.ts.
const VISUAL: Record<EstadoExibido, { Icone: LucideIcon; fundo: string; cor: string }> = {
  up: { Icone: Circle, fundo: 'bg-no-ar-100', cor: 'text-no-ar' },
  degraded: { Icone: Triangle, fundo: 'bg-degradado-100', cor: 'text-degradado' },
  down: { Icone: X, fundo: 'bg-fora-100', cor: 'text-fora' },
  unknown: { Icone: CircleDashed, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados' },
  pausado: { Icone: Pause, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados' },
}

type Props = {
  item: ItemDoResumo
  incidentes: readonly Incidente[]
  agora: Date
  aoSumir: () => void
  aoErrar: (mensagem: string | null) => void
}

// A faixa de estado do detalhe (spec do redesign, Telas > 2), com o
// Reconhecer quando há incidente aberto sem ack. O Reconhecer é primário: o
// secundário não tem 3:1 sobre os tons 100 do escuro.
export function FaixaDeEstado({ item, incidentes, agora, aoSumir, aoErrar }: Props) {
  const api = useApi()
  const qc = useQueryClient()
  const idDoTitulo = useId()
  const idDaAjuda = useId()
  const [reconhecendo, setReconhecendo] = useState(false)
  const reconhecido = useRef<HTMLParagraphElement>(null)
  // O Reconhecer some quando o ack vale, e o foco iria ao <body>: vai ao
  // "Reconhecido" (o mesmo cuidado da tela de incidentes, I1). Um ref e um
  // efeito a cada render: o render que o pede é o de sair do "reconhecendo",
  // já com a lista relida.
  const focarDepois = useRef(false)
  useEffect(() => {
    if (!focarDepois.current) return
    focarDepois.current = false
    if (document.activeElement && document.activeElement !== document.body) return
    reconhecido.current?.focus()
  })

  const aberto = incidentes.find((i) => i.target_id === item.id && i.resolved_at === null) ?? null
  const faixa = faixaDoEstado(item, aberto, agora)
  const { Icone, fundo, cor } = VISUAL[faixa.estado]

  async function reconhecer(id: string) {
    aoErrar(null)
    setReconhecendo(true)
    try {
      await api.reconhecer(id)
    } catch (e) {
      // 404: o incidente sumiu com o alvo, apagado em outra aba.
      if (naoEncontrado(e)) {
        aoSumir()
        return
      }
      aoErrar(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: ['incidents'] })
      focarDepois.current = true
      setReconhecendo(false)
    }
  }

  return (
    <section aria-labelledby={idDoTitulo} className={cn('flex flex-wrap items-start gap-4 rounded-padrao p-4 sm:p-6', fundo)}>
      <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full bg-superficie', cor)}>
        <Icone aria-hidden="true" className="size-6" strokeWidth={2.5} />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <h2 id={idDoTitulo} className="text-xl sm:text-2xl">
          {faixa.titulo}
        </h2>
        <p className="text-sm text-suave">{faixa.detalhe}</p>
        {aberto?.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some.
          <p ref={reconhecido} tabIndex={-1} className="flex flex-wrap items-center gap-2 pt-1 text-sm text-suave outline-none">
            <Selo tom="primaria" Icone={Check}>
              Reconhecido
            </Selo>
            lembretes parados desde {dataHora(aberto.acknowledged_at)}
          </p>
        )}
      </div>
      {aberto && !aberto.acknowledged_at && (
        <div className="max-w-xs space-y-2">
          <Botao
            // aria-disabled, e não disabled: o botão desabilitado perderia o
            // foco para o <body> enquanto o POST voa.
            aria-disabled={reconhecendo || undefined}
            aria-describedby={idDaAjuda}
            onClick={() => {
              if (!reconhecendo) void reconhecer(aberto.id)
            }}
          >
            Reconhecer
          </Botao>
          <p id={idDaAjuda} className="text-meta text-suave">
            Reconhecer interrompe os lembretes; não resolve o incidente.
          </p>
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 4: A faixa na tela**

Em `web/src/features/alvos/PaginaDetalhe.tsx`:

(a) Tire `import { estadoExibido } from '@/lib/estado'`, que fica sem uso depois do (d); o `import { SeloDeEstado } from '@/components/SeloDeEstado'` fica (o selo continua na tabela) e acrescente, depois do import do `GraficoDeLatencia`, `import { FaixaDeEstado } from './FaixaDeEstado'`.

(b) O componente `Incidentes` passa a receber a consulta. Troque:

```tsx
function Incidentes({ id }: { id: string }) {
  const consulta = useIncidentes('all', id, 10)
```

por:

```tsx
function Incidentes({ consulta }: { consulta: ReturnType<typeof useIncidentes> }) {
```

(c) Em `DetalheDoAlvo`, logo depois de `const serie = useSerie(id, janela)`, acrescente:

```tsx
  // Os incidentes recentes do alvo: a lista do fim da tela e o aberto da faixa.
  const incidentes = useIncidentes('all', id, 10)
```

(d) Apague a linha `{noResumo && <SeloDeEstado estado={estadoExibido(noResumo.state, noResumo.enabled)} />}` e, logo depois de `<ErroDoFormulario>{erro}</ErroDoFormulario>`, acrescente:

```tsx
          {noResumo && (
            <FaixaDeEstado
              item={noResumo}
              incidentes={incidentes.data?.pages.flatMap((p) => p.items) ?? []}
              agora={new Date(resumo.dataUpdatedAt)}
              aoSumir={() => void sumiu()}
              aoErrar={setErro}
            />
          )}
```

(e) Troque `<Incidentes id={id} />` por `<Incidentes consulta={incidentes} />`.

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos src/tokens.test.ts src/sistema.test.ts`
Expected: PASS, inclusive os testes antigos do detalhe.

Sabotagens: tire `focarDepois.current = true` → "leva o foco" falha; em `reconhecer`, troque o bloco do `naoEncontrado` por `aoErrar(mensagemDe(e))` → "o ack com 404" falha. Desfaça.

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/alvos/FaixaDeEstado.tsx web/src/features/alvos/PaginaDetalhe.tsx web/src/features/alvos/detalhe.test.tsx web/src/tokens.test.ts web/src/sistema.test.ts
git commit -m "feat(painel): faixa de estado do alvo com Reconhecer"
```

### Task 23: Indicadores, janela segmentada e incidentes recentes

**Files:**
- Create: `web/src/features/alvos/Indicadores.tsx`
- Create: `web/src/features/alvos/IncidentesRecentes.tsx`
- Modify: `web/src/features/alvos/PaginaDetalhe.tsx` (arquivo inteiro)
- Modify: `web/src/features/alvos/detalhe.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `emPorcento`, `emMilissegundos`, `ultimoCheck` (Task 21); `SeloDoIncidente`, `periodoDoIncidente` (Task 21); `FaixaDeEstado` (Task 22); `GraficoDeLatencia` (Task 20); `ControleSegmentado`, `type OpcaoSegmentada`; `useSerie`, `useIncidentes`, `resumir`, `textoDaCausa`.
- Produces:

```ts
// web/src/features/alvos/Indicadores.tsx
export function Indicadores(props: { alvoId: string; item: ItemDoResumo | undefined; agora: Date })
// <dl>: "No ar em 24 h", "No ar em 30 dias", "Latência mediana (24 h)", "Último check"; cada <dd> com o valor e, depois de um espaço, a unidade

// web/src/features/alvos/IncidentesRecentes.tsx
export function IncidentesRecentes(props: { consulta: ReturnType<typeof useIncidentes>; agora: Date })
// região "Incidentes recentes" com uma <li> por incidente e o link "Todos os incidentes"
```

Contrato da tela: link "Voltar ao painel" (texto visível "Painel"); `radiogroup` "Janela do gráfico"; Pausar e Apagar `terciario`, Editar `secundario`; região "Latência"; série vazia → "Nenhum check nesta janela." sem gráfico nem tabela.

- [ ] **Step 1: Escrever e ajustar os testes**

Em `web/src/features/alvos/detalhe.test.tsx`, troque o teste `a série pede a janela ao servidor pelo nome, sem from do navegador` inteiro por:

```tsx
  // B6 do Plano 10: a série pede a janela pelo nome (?window=1h), e o
  // servidor calcula o from com o relógio dele. Sabotagem: mandar também o
  // from no serie() do cliente → 400 da API falsa. Os indicadores pedem
  // também 24 h e 30 dias: a ordem não importa, o formato sim.
  it('a série pede a janela ao servidor pelo nome, sem from do navegador', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=1h')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [{ checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByText(/Mediana 100 ms/)).toBeInTheDocument()
    const consultas = () => r.falsa.corpos('GET /targets/:id/checks')
    expect(consultas()).toContainEqual({ window: '1h' })
    await r.usuario.click(screen.getByRole('radio', { name: '6 h' }))
    await waitFor(() => expect(consultas()).toContainEqual({ window: '6h' }))
    expect(consultas().filter((c) => 'from' in c)).toEqual([])
  })
```

No teste `a janela acima de 48h desenha pelo que o servidor decidiu (resolution 1h)`, troque:

```tsx
    expect(screen.getByRole('button', { name: '7 dias' })).toHaveAttribute('aria-pressed', 'true')
```

por:

```tsx
    expect(screen.getByRole('radio', { name: '7 dias' })).toBeChecked()
```

No teste `trocando de janela, o bloco fica ocupado até a janela nova chegar`, troque `screen.getByRole('button', { name: '7 dias' })` por `screen.getByRole('radio', { name: '7 dias' })`.

No teste `janela desconhecida na URL cai nas 24 h, sem quebrar`, troque:

```tsx
    expect(await screen.findByRole('button', { name: '24 h' })).toHaveAttribute('aria-pressed', 'true')
```

por:

```tsx
    expect(await screen.findByRole('radio', { name: '24 h' })).toBeChecked()
```

No fim do `describe('detalhe', …)`, acrescente:

```tsx
  it('volta ao painel pelo link "Painel"', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('link', { name: 'Voltar ao painel' })).toHaveAttribute('href', '/')
  })

  // Spec, Telas > 2: quatro KPIs. 30 dias vem da janela horária; a mediana,
  // da janela de 24 h. Sabotagem: trocar useSerie(alvoId, '30d') por '24h'
  // → "No ar em 30 dias" mostra 66,66 %.
  it('os quatro indicadores: no ar em 24 h e em 30 dias, mediana de 24 h e último check', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=1h')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ uptime_24h: 0.99999, last_checked_at: new Date(2026, 8, 24, 11, 59).toISOString() })]
    servidor.use(
      http.get('*/api/v1/targets/:id/checks', ({ request }) => {
        if (new URL(request.url).searchParams.get('window') === '30d') {
          return HttpResponse.json({
            resolution: '1h',
            items: [
              { bucket: '2026-09-24T10:00:00Z', total: 60, ok: 30, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 },
              { bucket: '2026-09-24T11:00:00Z', total: 60, ok: 60, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 },
            ],
          })
        }
        return HttpResponse.json({
          resolution: 'raw',
          items: [
            { checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null },
            { checked_at: '2026-09-24T11:01:00Z', status: 'up', latency_ms: 300, status_code: 200, error_kind: null },
            { checked_at: '2026-09-24T11:02:00Z', status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
          ],
        })
      }),
    )
    await r.cliente.refetchQueries()
    const valor = (rotulo: string) => screen.getByText(rotulo).nextElementSibling
    await waitFor(() => expect(valor('No ar em 30 dias')).toHaveTextContent('75,00 %'))
    expect(valor('No ar em 24 h')).toHaveTextContent('99,99 %')
    expect(valor('Latência mediana (24 h)')).toHaveTextContent('100 ms')
    expect(valor('Último check')).toHaveTextContent(/^11:59 há /)
  })

  // Review Focus 4: sem check nenhum, traço e "nunca".
  it('alvo sem nenhum check: os indicadores mostram traço, e o último check diz "nunca"', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] })]
    await r.cliente.refetchQueries()
    await screen.findByRole('region', { name: 'Sem dados' })
    expect(screen.getByText('No ar em 24 h').nextElementSibling).toHaveTextContent('—')
    expect(screen.getByText('Último check').nextElementSibling).toHaveTextContent('— nunca')
  })

  // Review Focus 5. Sabotagem: tirar o ramo "s.items.length === 0" → a
  // tabela vazia aparece.
  it('janela sem checks: uma frase no lugar do gráfico e da tabela', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = { resolution: 'raw', items: [] }
    await r.cliente.refetchQueries()
    const latencia = await screen.findByRole('region', { name: 'Latência' })
    expect(await within(latencia).findByText('Nenhum check nesta janela.')).toBeInTheDocument()
    expect(within(latencia).queryByTestId('grafico-latencia')).not.toBeInTheDocument()
    expect(within(latencia).queryByRole('table')).not.toBeInTheDocument()
    expect(within(latencia).getByText(/sem checks nesta janela/)).toBeInTheDocument()
  })

  it('a janela é um radiogroup: a seta escolhe a próxima e vai para a URL', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const grupo = await screen.findByRole('radiogroup', { name: 'Janela do gráfico' })
    expect(within(grupo).getByRole('radio', { name: '24 h' })).toBeChecked()
    await r.usuario.click(within(grupo).getByRole('radio', { name: '24 h' }))
    await r.usuario.keyboard('{ArrowRight}')
    expect(within(grupo).getByRole('radio', { name: '7 dias' })).toBeChecked()
    expect(r.router.state.location.search).toBe('?janela=7d')
  })

  // Spec, Telas > 2: linhas com selo (Em andamento, Reconhecido, Resolvido)
  // e a causa. Review Focus 7.
  it('incidentes recentes: selo, causa (ou "Causa não registrada") e período', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.incidentes = [incidente({ cause: '' }), incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z' })]
    await r.cliente.refetchQueries()
    const secao = await screen.findByRole('region', { name: 'Incidentes recentes' })
    await waitFor(() => expect(within(secao).getAllByRole('listitem')).toHaveLength(2))
    const [aberto, resolvido] = within(secao).getAllByRole('listitem') as [HTMLElement, HTMLElement]
    expect(within(aberto).getByText('Em andamento')).toBeInTheDocument()
    expect(within(aberto).getByText('Causa não registrada')).toBeInTheDocument()
    expect(within(resolvido).getByText('Resolvido')).toBeInTheDocument()
    expect(within(resolvido).getByText(/· durou 30 min$/)).toBeInTheDocument()
  })
```

Em `web/src/sistema.test.ts`, acrescente à `ARQUIVOS_NO_SISTEMA`: `'src/features/alvos/PaginaDetalhe.tsx'`, `'src/features/alvos/Indicadores.tsx'`, `'src/features/alvos/IncidentesRecentes.tsx'`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos/detalhe.test.tsx src/sistema.test.ts`
Expected: FAIL. A janela ainda é `button` com `aria-pressed`; não há "Voltar ao painel", indicadores, `radiogroup` nem a frase da série vazia; os incidentes recentes não têm selo; `PaginaDetalhe.tsx` reprova no sistema (`rounded-md` do seletor de janela). Os demais testes antigos passam.

- [ ] **Step 3: Os indicadores**

Crie `web/src/features/alvos/Indicadores.tsx`:

```tsx
import { useSerie } from '@/lib/api/consultas'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { emMilissegundos, emPorcento, ultimoCheck, type ValorDoIndicador } from './estado-do-alvo'
import { resumir } from './serie'

// Os quatro KPIs do detalhe (spec do redesign, Telas > 2), em Inter 600 de
// 28 px com a unidade em 13 px. "No ar em 24 h" e "Último check" vêm do
// resumo; "No ar em 30 dias", da janela horária de 30 dias (os dados por
// hora atrasam até ~1h15); a mediana, da janela de 24 h, a mesma consulta
// da janela padrão do gráfico.
export function Indicadores({ alvoId, item, agora }: { alvoId: string; item: ItemDoResumo | undefined; agora: Date }) {
  const dia = useSerie(alvoId, '24h')
  const mes = useSerie(alvoId, '30d')
  return (
    <dl className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <Indicador rotulo="No ar em 24 h" {...emPorcento(item?.uptime_24h ?? null)} />
      <Indicador rotulo="No ar em 30 dias" {...emPorcento(mes.data ? resumir(mes.data).uptime : null)} />
      <Indicador rotulo="Latência mediana (24 h)" {...emMilissegundos(dia.data ? resumir(dia.data).mediana : null)} />
      <Indicador rotulo="Último check" {...ultimoCheck(item?.last_checked_at ?? null, agora)} />
    </dl>
  )
}

function Indicador({ rotulo, valor, unidade }: ValorDoIndicador & { rotulo: string }) {
  return (
    <div className="min-w-0 space-y-1 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1">
      <dt className="text-meta text-suave">{rotulo}</dt>
      <dd>
        <span className="text-3xl font-semibold">{valor}</span>
        {unidade && (
          <>
            {' '}
            <span className="text-meta font-normal text-suave">{unidade}</span>
          </>
        )}
      </dd>
    </div>
  )
}
```

- [ ] **Step 4: Os incidentes recentes**

Crie `web/src/features/alvos/IncidentesRecentes.tsx`:

```tsx
import { Link } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { periodoDoIncidente, SeloDoIncidente } from '@/components/SeloDoIncidente'
import type { useIncidentes } from '@/lib/api/consultas'
import { textoDaCausa } from '@/lib/api/mensagens'

// Os incidentes recentes do alvo (spec do redesign, Telas > 2): uma linha
// por incidente, com o selo da situação, a causa e o período. Pela
// EstadoDaConsulta (D17): um poll de fundo que falha mantém a lista.
export function IncidentesRecentes({ consulta, agora }: { consulta: ReturnType<typeof useIncidentes>; agora: Date }) {
  return (
    <section className="space-y-3" aria-labelledby="titulo-incidentes">
      <h2 id="titulo-incidentes" className="text-xl">
        Incidentes recentes
      </h2>
      <EstadoDaConsulta consulta={consulta}>
        {(dados) => {
          const itens = dados.pages.flatMap((p) => p.items)
          return itens.length === 0 ? (
            <p className="text-suave">Nenhum incidente.</p>
          ) : (
            <ul className="divide-y divide-borda rounded-padrao border border-borda bg-superficie px-4 sombra-nivel-1">
              {itens.map((i) => (
                <li key={i.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-3">
                  <SeloDoIncidente incidente={i} />
                  <span className="text-sm">{textoDaCausa(i.cause)}</span>
                  <span className="text-meta text-suave">{periodoDoIncidente(i, agora)}</span>
                </li>
              ))}
            </ul>
          )
        }}
      </EstadoDaConsulta>
      <Link to="/incidents" className="alvo-de-toque text-sm font-semibold text-primaria underline underline-offset-4">
        Todos os incidentes
      </Link>
    </section>
  )
}
```

- [ ] **Step 5: A tela**

Substitua `web/src/features/alvos/PaginaDetalhe.tsx` por:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ControleSegmentado, type OpcaoSegmentada } from '@/components/ui/controle-segmentado'
import { Confirmar } from '@/components/ui/dialogo'
import { useAlvo, useIncidentes, useResumo, useSerie } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, fuso, milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { FaixaDeEstado } from './FaixaDeEstado'
import { GraficoDeLatencia } from './GraficoDeLatencia'
import { IncidentesRecentes } from './IncidentesRecentes'
import { Indicadores } from './Indicadores'
import { ehJanela, JANELAS, resumir, type Janela } from './serie'

const NOMES: Record<Janela, string> = { '1h': '1 h', '6h': '6 h', '24h': '24 h', '7d': '7 dias', '30d': '30 dias', '90d': '90 dias' }
const OPCOES_DE_JANELA: readonly OpcaoSegmentada<Janela>[] = (Object.keys(JANELAS) as Janela[]).map((j) => ({
  valor: j,
  rotulo: NOMES[j],
}))

function Resumo({ id, serie }: { id: string; serie: Serie }) {
  const r = resumir(serie)
  const horaria = serie.resolution === '1h'
  return (
    <p id={id}>
      {horaria ? 'Mediana das horas' : 'Mediana'} {milissegundos(r.mediana)}, {horaria ? 'pior p95 horário' : 'p95'}{' '}
      {milissegundos(r.p95)}, {r.uptime === null ? 'sem checks' : `${porcento(r.uptime)} no ar`} nesta janela.
    </p>
  )
}

// A tabela do gráfico, para leitor de tela e teclado; números à direita.
function Tabela({ serie }: { serie: Serie }) {
  if (serie.resolution === '1h') {
    const linhas = serie.items.slice(-200).reverse()
    return (
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Até as 200 horas mais recentes da janela, da mais recente</caption>
        <thead className="text-meta text-apagado">
          <tr>
            <th scope="col" className="py-2 font-normal">Hora</th>
            <th scope="col" className="py-2 text-right font-normal">Checks</th>
            <th scope="col" className="py-2 text-right font-normal">No ar</th>
            <th scope="col" className="py-2 text-right font-normal">Mediana</th>
            <th scope="col" className="py-2 text-right font-normal">p95</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-borda">
          {linhas.map((h) => (
            <tr key={h.bucket}>
              <td className="py-2">{dataHora(h.bucket)}</td>
              <td className="py-2 text-right">{h.total}</td>
              <td className="py-2 text-right">{h.total ? porcento(h.ok / h.total) : '—'}</td>
              <td className="py-2 text-right">{milissegundos(h.p50_ms)}</td>
              <td className="py-2 text-right">{milissegundos(h.p95_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  const linhas = serie.items.slice(-200).reverse()
  return (
    <table className="w-full text-left text-sm">
      <caption className="sr-only">Até os 200 checks mais recentes da janela, do mais recente</caption>
      <thead className="text-meta text-apagado">
        <tr>
          <th scope="col" className="py-2 font-normal">Quando</th>
          <th scope="col" className="py-2 font-normal">Estado</th>
          <th scope="col" className="py-2 text-right font-normal">Latência</th>
          <th scope="col" className="py-2 font-normal">Detalhe</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-borda">
        {linhas.map((c) => (
          <tr key={c.checked_at}>
            <td className="py-2">{dataHora(c.checked_at)}</td>
            <td className="py-2">
              <SeloDeEstado estado={c.status} />
            </td>
            <td className="py-2 text-right">{c.status === 'down' ? '—' : milissegundos(c.latency_ms)}</td>
            <td className="py-2">{textoDoErroDeCheck(c.error_kind, c.status_code) || (c.status_code ?? '')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// De um detalhe a outro a rota é a mesma, e o React reaproveitaria a tela:
// o erro, o diálogo de apagar e o "ocupado" de um alvo seguiriam no outro, e
// o título (com o alvo no cache) não receberia o foco. A key recomeça tudo.
export function PaginaDetalhe() {
  const { id = '' } = useParams()
  return <DetalheDoAlvo key={id} id={id} />
}

function DetalheDoAlvo({ id }: { id: string }) {
  const [params, setParams] = useSearchParams()
  const janelaDaUrl = params.get('janela')
  const janela: Janela = ehJanela(janelaDaUrl) ? janelaDaUrl : '24h'
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const alvo = useAlvo(id)
  const resumo = useResumo()
  const serie = useSerie(id, janela)
  // Os incidentes recentes do alvo: a lista do fim da tela e o aberto da faixa.
  const incidentes = useIncidentes('all', id, 10)
  const [apagando, setApagando] = useState(false)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const noResumo = resumo.data?.items.find((x) => x.id === id)
  const agora = new Date(resumo.dataUpdatedAt)
  const idDoResumo = useId()

  // Sai primeiro, e só então tira do cache o que era do alvo: com a tela
  // ainda montada, invalidar (ou remover) refaria o GET /targets/:id, e o 404
  // piscaria. O flushSync faz a saída valer já no fim do await (sem ele, o
  // Router a aplica numa transição, e a tela ainda estaria lá). Depois, o
  // resumo e os incidentes (o contador da navegação) se atualizam.
  async function sairDoAlvo(aviso: string) {
    avisar(aviso)
    await navegar('/', { flushSync: true })
    qc.removeQueries({ queryKey: ['targets', id] })
    void qc.invalidateQueries({ queryKey: ['targets'] })
    void qc.invalidateQueries({ queryKey: ['incidents'] })
  }

  // O alvo foi apagado em outra aba (M3, o padrão de Canais).
  const sumiu = () => sairDoAlvo('O alvo já tinha sido apagado, talvez em outra aba.')

  // O poll (60 s, foco da janela) que volta 404 depois de o alvo estar na
  // tela: ele sumiu. Um 404 já na primeira carga (um endereço que nunca
  // existiu) fica na tela como "Não encontrado.". A chave é o instante do
  // 404, e o ref faz cada 404 contar uma vez (o StrictMode roda o efeito de
  // montagem duas vezes).
  const sumiuNoPoll = naoEncontrado(alvo.error) && alvo.data !== undefined ? alvo.errorUpdatedAt : 0
  const tratado = useRef(0)
  const aoSumirNoPoll = useEffectEvent((quando: number) => {
    if (quando === tratado.current) return
    tratado.current = quando
    void sumiu()
  })
  useEffect(() => {
    if (sumiuNoPoll) aoSumirNoPoll(sumiuNoPoll)
  }, [sumiuNoPoll])

  async function alternar(ativo: boolean) {
    setErro(null)
    try {
      await api.alterarAlvo(id, { enabled: ativo })
      await qc.invalidateQueries({ queryKey: ['targets'] })
    } catch (e) {
      if (naoEncontrado(e)) await sumiu()
      else setErro(mensagemDe(e))
    }
  }

  async function apagar() {
    setOcupado(true)
    try {
      await api.apagarAlvo(id)
      await sairDoAlvo('Alvo apagado.')
    } catch (e) {
      if (naoEncontrado(e)) {
        await sumiu()
        return
      }
      setErro(mensagemDe(e))
      setApagando(false)
    } finally {
      setOcupado(false)
    }
  }

  return (
    <EstadoDaConsulta consulta={alvo}>
      {(a) => (
        // Um <div>, e não <section> filho direto do <main>: a regra legada
        // "main > section" do index.css daria moldura a cada bloco.
        <div className="space-y-8">
          <div className="space-y-4">
            {/* "Painel" visível, "Voltar ao painel" no nome acessível: não
                colide com a aba "Painel" da navegação. */}
            <Link
              to="/"
              aria-label="Voltar ao painel"
              className="alvo-de-toque inline-flex items-center gap-1 text-sm font-semibold text-primaria underline-offset-4 hover:underline"
            >
              <ArrowLeft aria-hidden="true" className="size-4" />
              Painel
            </Link>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0 space-y-1">
                <Titulo aba={a.name}>{a.name}</Titulo>
                <p className="text-meta break-all text-apagado">
                  {a.method} {a.url} · a cada {a.interval_sec} s
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Botao tipo="secundario" asChild>
                  <Link to={`/targets/${id}/edit`}>Editar</Link>
                </Botao>
                <Botao tipo="terciario" onClick={() => void alternar(!a.enabled)}>
                  {a.enabled ? 'Pausar' : 'Retomar'}
                </Botao>
                <Botao tipo="terciario" onClick={() => setApagando(true)}>
                  Apagar
                </Botao>
              </div>
            </div>
          </div>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          {noResumo && (
            <FaixaDeEstado
              item={noResumo}
              incidentes={incidentes.data?.pages.flatMap((p) => p.items) ?? []}
              agora={agora}
              aoSumir={() => void sumiu()}
              aoErrar={setErro}
            />
          )}
          <Indicadores alvoId={id} item={noResumo} agora={agora} />

          <section
            className="space-y-4 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6"
            aria-labelledby="titulo-latencia"
          >
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="titulo-latencia" className="text-xl">
                Latência
              </h2>
              <ControleSegmentado
                rotulo="Janela do gráfico"
                opcoes={OPCOES_DE_JANELA}
                valor={janela}
                aoMudar={(j) => setParams({ janela: j }, { replace: true })}
              />
            </div>
            <EstadoDaConsulta consulta={serie}>
              {(s) => (
                // Trocando de janela, os números da janela anterior ficam na
                // tela (keepPreviousData) até a nova chegar: marcados como velhos.
                <div className={cn('space-y-3', serie.isPlaceholderData && 'opacity-60')} aria-busy={serie.isPlaceholderData}>
                  {serie.isPlaceholderData && <p className="text-sm text-suave">Carregando…</p>}
                  <Resumo id={idDoResumo} serie={s} />
                  <p className="text-sm text-suave">
                    {s.resolution === '1h'
                      ? 'Resolução por hora — os dados horários podem atrasar até cerca de 1h15.'
                      : 'Resolução por check.'}{' '}
                    Horários em {fuso()}.
                  </p>
                  {s.items.length === 0 ? (
                    <p className="text-sm text-suave">Nenhum check nesta janela.</p>
                  ) : (
                    <>
                      <GraficoDeLatencia serie={s} descritoPor={idDoResumo} />
                      <details>
                        <summary className="alvo-de-toque cursor-pointer text-sm">Ver dados em tabela (até os 200 mais recentes)</summary>
                        <Tabela serie={s} />
                      </details>
                    </>
                  )}
                </div>
              )}
            </EstadoDaConsulta>
          </section>

          <IncidentesRecentes consulta={incidentes} agora={agora} />

          <Confirmar
            aberto={apagando}
            aoMudar={setApagando}
            titulo={`Apagar “${a.name}”?`}
            descricao="O histórico de checks e incidentes deste alvo também é apagado. Não há como desfazer."
            acao="Apagar alvo"
            ocupado={ocupado}
            aoConfirmar={() => void apagar()}
          />
        </div>
      )}
    </EstadoDaConsulta>
  )
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos src/sistema.test.ts`
Expected: PASS.

Sabotagens: troque `useSerie(alvoId, '30d')` por `useSerie(alvoId, '24h')` → o teste dos indicadores mostra 66,66 %; tire o ramo `s.items.length === 0` (deixe só o gráfico e a tabela) → "janela sem checks" acha a tabela. Desfaça.

- [ ] **Step 7: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: verde.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/alvos/Indicadores.tsx web/src/features/alvos/IncidentesRecentes.tsx web/src/features/alvos/PaginaDetalhe.tsx web/src/features/alvos/detalhe.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): detalhe com indicadores, janela segmentada e incidentes recentes"
```

### Task 24: e2e do detalhe, verificação do PR 4 e abertura

**Files:**
- Modify: `web/e2e/a11y.spec.ts` (janela em `radio` e volta no teste de alvos de toque; hachura na janela de 7 dias; detalhe no teste de nome longo)
- Modify: `web/e2e/fluxo.spec.ts` (passos 4 e 6)

**Interfaces:**
- Consumes: o contrato da tela da Task 23 e do gráfico da Task 20.
- Produces: nada para os PRs seguintes.

- [ ] **Step 1: A janela de 7 dias desenha a hachura**

Em `web/e2e/a11y.spec.ts`, no teste `acessibilidade e CSP em todas as rotas`, troque:

```ts
      if (rota.endsWith('janela=7d')) {
        // As barras de uptime da hora desenham, com cor (variável CSS).
        const barra = page.getByTestId('grafico-latencia').locator('.recharts-bar-rectangle path').first()
        await expect(barra).toBeAttached()
        expect(await barra.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^(rgb|oklch)\(/)
      }
```

por:

```ts
      if (rota.endsWith('janela=7d')) {
        // A hora com menos de 99% no ar vira faixa hachurada (forma + cor),
        // com o padrão do próprio gráfico e o traço em variável CSS.
        const faixa = page.getByTestId('grafico-latencia').locator('.recharts-reference-area-rect').first()
        await expect(faixa).toBeAttached()
        const padrao = (await faixa.getAttribute('fill'))?.match(/^url\(#(.+)\)$/)?.[1]
        expect(padrao, 'a faixa usa o padrão de hachura').toBeTruthy()
        const traco = page.locator(`pattern[id="${padrao}"] path`)
        expect(await traco.evaluate((e) => getComputedStyle(e).stroke)).toMatch(/^(rgb|oklch)\(/)
      }
```

- [ ] **Step 2: Alvos de toque do detalhe**

No teste `alvos de toque de 44 px`, no bloco do `/targets/a1`, troque:

```ts
      ...['1 h', '6 h', '24 h', '7 dias', '30 dias', '90 dias'].map(
        (j): [string, Locator] => [`janela ${j}`, page.getByRole('button', { name: j, exact: true })],
      ),
```

por:

```ts
      ['Voltar ao painel', page.getByRole('link', { name: 'Voltar ao painel' })],
      ...['1 h', '6 h', '24 h', '7 dias', '30 dias', '90 dias'].map(
        (j): [string, Locator] => [`janela ${j}`, page.getByRole('radio', { name: j, exact: true })],
      ),
```

- [ ] **Step 3: Nome longo no detalhe**

No teste `muitos alvos e nome longo, ${largura} px`, depois de `await semRolagemLateral(`painel ${largura}`)`, acrescente:

```ts
    // O detalhe do alvo de nome longo, fora do ar.
    await page.route('**/api/v1/targets/a1', (route) =>
      route.fulfill({ status: 200, json: alvo({ id: 'a1', name: NOME_LONGO, created_at: agora }) }),
    )
    await page.goto('/targets/a1')
    await expect(page.getByRole('heading', { name: NOME_LONGO, level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/targets/a1')
    await expect(page.getByRole('region', { name: 'Fora do ar' })).toBeVisible()
    await semRolagemLateral(`detalhe ${largura}`)
```

- [ ] **Step 4: O fluxo**

Em `web/e2e/fluxo.spec.ts`, no passo 4, troque:

```ts
    await page.getByRole('link', { name: 'Painel' }).click()
```

por:

```ts
    // A aba da navegação: o detalhe também tem o "Voltar ao painel".
    await page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: 'Painel' }).click()
```

No passo 6, troque o comentário e as linhas da marca:

```ts
    // 6. O detalhe carrega o chunk do Recharts. O alvo .invalid só tem checks
    //    "fora do ar", que viram marcas na base do gráfico; a marca tem cor
    //    computada (variável CSS do index.css). O dashboard já mostrou um
    //    check: a série tem pontos na primeira carga.
    await page.getByRole('link', { name: nomeDoAlvo, exact: true }).click()
    await expect(page.getByText(/no ar nesta janela/)).toBeVisible()
    const marca = page.getByTestId('grafico-latencia').locator('.recharts-symbols').first()
    await expect(marca).toBeAttached()
    expect(await marca.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^(rgb|oklch)\(/)
```

por:

```ts
    // 6. O detalhe carrega o chunk do Recharts. O alvo .invalid só tem checks
    //    "fora do ar", que viram faixas hachuradas; o traço do padrão tem
    //    cor computada (variável CSS do index.css). O dashboard já mostrou
    //    um check: a série tem pontos na primeira carga.
    await page.getByRole('link', { name: nomeDoAlvo, exact: true }).click()
    await expect(page.getByText(/no ar nesta janela/)).toBeVisible()
    const faixa = page.getByTestId('grafico-latencia').locator('.recharts-reference-area-rect').first()
    await expect(faixa).toBeAttached()
    const padrao = (await faixa.getAttribute('fill'))?.match(/^url\(#(.+)\)$/)?.[1]
    expect(padrao).toBeTruthy()
    expect(await page.locator(`pattern[id="${padrao}"] path`).evaluate((e) => getComputedStyle(e).stroke)).toMatch(
      /^(rgb|oklch)\(/,
    )
```

- [ ] **Step 5: Rodar o e2e**

Run: `cd web && npm run build && npx playwright test`
Expected: PASS em todos os specs (Docker). Sabotagem: em `GraficoDeLatencia`, troque o `fill` da `ReferenceArea` por `var(--estado-fora)`, `npm run build && npx playwright test e2e/a11y.spec.ts -g "acessibilidade"` → a rota de 7 dias falha em "a faixa usa o padrão de hachura". Desfaça e rode de novo.

- [ ] **Step 6: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint` e, na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 7: Commit e PR**

```bash
git add web/e2e
git commit -m "test(painel): e2e do detalhe novo (hachura, janela em radio, nome longo)"
git push -u origin feat/redesign-detalhe
gh pr create --title "feat(painel): detalhe do alvo com faixa de estado, KPIs e gráfico novo" --body "$(cat <<'EOF'
PR 4 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md (Telas > 2).

- Faixa de estado no tom 100 do estado ("Fora do ar há 2 h 31 min", causa e início), com Reconhecer primário e foco no "Reconhecido".
- Quatro KPIs em Inter 600: no ar em 24 h e em 30 dias, latência mediana de 24 h e último check.
- Gráfico: mediana em índigo, faixa p50–p95, queda hachurada, marcas em horas redondas e escala Y robusta com o pico fora dela anotado na legenda e na descrição do gráfico.
- Janela no controle segmentado; tabela com números à direita; incidentes recentes com selo.
- Ações em pirâmide: Editar secundário, Pausar e Apagar terciários.
- Sem API nova: 30 dias e a mediana vêm de /checks?window=30d e 24h.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Antes do merge, o dono revê o detalhe de um alvo no ar e de um fora do ar, nos dois temas, a 400 e a 1280 px.

---

# PR 5: Incidentes

Branch: `feat/redesign-incidentes`, a partir da `main` com o PR 4 integrado.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-incidentes
```

### Task 25: Grupos e resumo dos incidentes em funções puras

**Files:**
- Create: `web/src/features/incidentes/agrupamento.ts`, `web/src/features/incidentes/agrupamento.test.ts`

**Interfaces:**
- Consumes: `situacaoDoIncidente` (Task 21); tipo `Incidente`.
- Produces:

```ts
// web/src/features/incidentes/agrupamento.ts
export type GruposDeIncidentes = { emAndamento: Incidente[]; resolvidos: Incidente[] }
export function agruparIncidentes(itens: readonly Incidente[]): GruposDeIncidentes   // mantém a ordem da rota
export function resumoDosAbertos(abertos: readonly Incidente[], completa: boolean): string | null
// null sem abertos; "1 em andamento, 2 reconhecidos."; página cheia: "Mais de 100 incidentes abertos."
```

- [ ] **Step 1: Escrever os testes que falham**

Crie `web/src/features/incidentes/agrupamento.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { Incidente } from '@/lib/api/schemas'
import { incidente } from '@/test/fixtures'
import { agruparIncidentes, resumoDosAbertos } from './agrupamento'

const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const ACK = '2026-09-24T11:10:00Z'
const FIM = '2026-09-24T11:30:00Z'

describe('agruparIncidentes', () => {
  it('em andamento (reconhecidos inclusive) e resolvidos, na ordem da rota', () => {
    const g = agruparIncidentes([
      inc({ id: 'a' }),
      inc({ id: 'b', resolved_at: FIM }),
      inc({ id: 'c', acknowledged_at: ACK }),
      inc({ id: 'd', resolved_at: FIM, acknowledged_at: ACK }),
    ])
    expect(g.emAndamento.map((i) => i.id)).toEqual(['a', 'c'])
    expect(g.resolvidos.map((i) => i.id)).toEqual(['b', 'd'])
  })

  it('lista vazia dá grupos vazios', () => {
    expect(agruparIncidentes([])).toEqual({ emAndamento: [], resolvidos: [] })
  })
})

describe('resumoDosAbertos', () => {
  it('sem abertos, nada: o vazio positivo fala por ele', () => {
    expect(resumoDosAbertos([], true)).toBeNull()
  })

  it.each([
    [[{}], '1 em andamento.'],
    [[{}, { acknowledged_at: ACK }], '1 em andamento, 1 reconhecido.'],
    [[{ acknowledged_at: ACK }, { acknowledged_at: ACK }], '2 reconhecidos.'],
    [[{}, {}, {}], '3 em andamento.'],
  ] as const)('%j → "%s"', (ps, texto) => {
    expect(resumoDosAbertos(ps.map((p, i) => inc({ id: `i${i}`, ...p })), true)).toBe(texto)
  })

  // A consulta dos abertos tem uma página de 100: com mais, não se sabe a
  // conta exata.
  it('página cheia: não finge saber a conta', () => {
    const cem = Array.from({ length: 100 }, (_, i) => inc({ id: `i${i}` }))
    expect(resumoDosAbertos(cem, false)).toBe('Mais de 100 incidentes abertos.')
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/incidentes/agrupamento.test.ts`
Expected: FAIL com `Failed to resolve import "./agrupamento"`.

- [ ] **Step 3: Implementar**

Crie `web/src/features/incidentes/agrupamento.ts`:

```ts
import { situacaoDoIncidente } from '@/components/SeloDoIncidente'
import type { Incidente } from '@/lib/api/schemas'

// A tela de incidentes (spec do redesign, Telas > 3) em funções puras: os
// grupos "Em andamento" e "Resolvidos recentes" e a frase de resumo.

export type GruposDeIncidentes = { emAndamento: Incidente[]; resolvidos: Incidente[] }

// "Em andamento" é todo incidente aberto, reconhecido ou não: reconhecer
// para os lembretes, não resolve.
export function agruparIncidentes(itens: readonly Incidente[]): GruposDeIncidentes {
  return {
    emAndamento: itens.filter((i) => i.resolved_at === null),
    resolvidos: itens.filter((i) => i.resolved_at !== null),
  }
}

// "1 em andamento, 2 reconhecidos." sobre os abertos (a consulta do
// contador da navegação). Sem abertos, nada; página cheia, a conta exata
// não se sabe.
export function resumoDosAbertos(abertos: readonly Incidente[], completa: boolean): string | null {
  if (abertos.length === 0) return null
  if (!completa) return `Mais de ${abertos.length} incidentes abertos.`
  const reconhecidos = abertos.filter((i) => situacaoDoIncidente(i) === 'reconhecido').length
  const emAndamento = abertos.length - reconhecidos
  const partes = [
    emAndamento > 0 ? `${emAndamento} em andamento` : null,
    reconhecidos > 0 ? `${reconhecidos} ${reconhecidos === 1 ? 'reconhecido' : 'reconhecidos'}` : null,
  ].filter((p): p is string => p !== null)
  return `${partes.join(', ')}.`
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/incidentes/agrupamento.test.ts`
Expected: PASS.

Sabotagens: em `agruparIncidentes`, troque `i.resolved_at === null` por `situacaoDoIncidente(i) === 'em-andamento'` → o reconhecido sai do grupo; tire o `if (!completa)` → a página cheia vira "100 em andamento.". Desfaça.

- [ ] **Step 5: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/incidentes/agrupamento.ts web/src/features/incidentes/agrupamento.test.ts
git commit -m "feat(painel): grupos e resumo dos incidentes abertos em funções puras"
```

### Task 26: A tela de incidentes

**Files:**
- Modify: `web/src/features/incidentes/PaginaIncidentes.tsx` (arquivo inteiro)
- Modify: `web/src/features/incidentes/incidentes.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: Task 25; `SeloDoIncidente`, `situacaoDoIncidente`, `periodoDoIncidente`, `type SituacaoDoIncidente` (Task 21); `useIncidentesAbertos` (Task 16); `ControleSegmentado`, `type OpcaoSegmentada`; `textoDaCausa`; `Botao`, `Titulo`, `cn`.
- Produces: `PaginaIncidentes()`, com o contrato: `radiogroup` "Filtro de incidentes" (Abertos, Resolvidos, Todos); frase de resumo abaixo do título; regiões "Em andamento" e "Resolvidos recentes"; cartão `<li>` com `border-l-fora` (aberto), `border-l-primaria` (reconhecido) ou sem acento (resolvido, nome em `text-apagado`); "Nenhum incidente aberto." com ícone em círculo `bg-no-ar-100`.

- [ ] **Step 1: Ajustar e escrever os testes**

Em `web/src/features/incidentes/incidentes.test.tsx`:

No teste `o filtro vai para a URL e muda a consulta`, troque `screen.findByRole('button', { name: 'Resolvidos' })` por `screen.findByRole('radio', { name: 'Resolvidos' })`.

No teste `um ack que falha mostra o erro, e trocar o filtro o limpa`, troque `screen.getByRole('button', { name: 'Todos' })` por `screen.getByRole('radio', { name: 'Todos' })`.

No fim do `describe('incidentes', …)` (antes do `})` que o fecha), acrescente:

```tsx
  describe('redesign da lista', () => {
    it('o filtro é um radiogroup: a seta escolhe o próximo e vai para a URL', async () => {
      const r = await renderizarPainel('/incidents')
      const grupo = await screen.findByRole('radiogroup', { name: 'Filtro de incidentes' })
      expect(within(grupo).getByRole('radio', { name: 'Abertos' })).toBeChecked()
      await r.usuario.click(within(grupo).getByRole('radio', { name: 'Abertos' }))
      await r.usuario.keyboard('{ArrowRight}')
      expect(r.router.state.location.search).toBe('?status=resolved')
      expect(within(grupo).getByRole('radio', { name: 'Resolvidos' })).toBeChecked()
    })

    // Spec, Telas > 3: a frase de resumo vem dos abertos, em qualquer filtro.
    it('o resumo conta os abertos em andamento e os reconhecidos, mesmo no filtro de resolvidos', async () => {
      const r = await renderizarPainel('/incidents?status=resolved')
      r.falsa.estado.incidentes = [
        incidente(),
        incidente({ id: 'inc-2', target_name: 'Site', acknowledged_at: '2026-09-24T11:10:00Z' }),
        incidente({ id: 'inc-3', resolved_at: '2026-09-24T11:30:00Z' }),
      ]
      await r.cliente.refetchQueries()
      expect(await screen.findByText('1 em andamento, 1 reconhecido.')).toBeInTheDocument()
    })

    // I4 da auditoria: o vazio dos abertos é o estado bom. Sabotagem: voltar
    // ao parágrafo "Nenhum incidente aberto." sem o ícone.
    it('sem abertos, o vazio é positivo: check em círculo verde, sem frase de resumo', async () => {
      await renderizarPainel('/incidents')
      const frase = await screen.findByText('Nenhum incidente aberto.')
      expect(frase.previousElementSibling).toHaveClass('bg-no-ar-100')
      expect(screen.queryByText(/em andamento[,.]/)).not.toBeInTheDocument()
    })

    it('em Todos, os grupos separam em andamento e resolvidos, e cada cartão mostra a situação', async () => {
      const r = await renderizarPainel('/incidents?status=all')
      r.falsa.estado.incidentes = [
        incidente({ id: 'a', target_name: 'API' }),
        incidente({ id: 'b', target_name: 'Site', acknowledged_at: '2026-09-24T11:10:00Z' }),
        incidente({ id: 'c', target_name: 'Banco', resolved_at: '2026-09-24T11:30:00Z' }),
      ]
      await r.cliente.refetchQueries()
      const andamento = await screen.findByRole('region', { name: 'Em andamento' })
      const resolvidos = screen.getByRole('region', { name: 'Resolvidos recentes' })
      const cartao = (regiao: HTMLElement, nome: string) => within(regiao).getByRole('link', { name: nome }).closest('li') as HTMLElement
      expect(within(andamento).getAllByRole('listitem')).toHaveLength(2)

      expect(cartao(andamento, 'API')).toHaveClass('border-l-fora')
      expect(within(cartao(andamento, 'API')).getByText('Em andamento')).toBeInTheDocument()
      expect(within(cartao(andamento, 'API')).getByRole('button', { name: 'Reconhecer o incidente de API' })).toBeInTheDocument()

      expect(cartao(andamento, 'Site')).toHaveClass('border-l-primaria')
      expect(within(cartao(andamento, 'Site')).getByText(/· lembretes parados$/)).toBeInTheDocument()
      expect(within(cartao(andamento, 'Site')).queryByRole('button')).not.toBeInTheDocument()

      expect(cartao(resolvidos, 'Banco')).not.toHaveClass('border-l-4')
      expect(within(resolvidos).getByRole('link', { name: 'Banco' })).toHaveClass('text-apagado')
      expect(within(cartao(resolvidos, 'Banco')).getByText('Resolvido')).toBeInTheDocument()
    })

    // Review Focus 7.
    it('incidente sem causa diz que a causa não foi registrada', async () => {
      const r = await renderizarPainel('/incidents')
      r.falsa.estado.incidentes = [incidente({ cause: '' })]
      await r.cliente.refetchQueries()
      expect(await screen.findByText(/^Causa não registrada · início /)).toBeInTheDocument()
    })
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/incidentes/PaginaIncidentes.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/incidentes src/sistema.test.ts`
Expected: FAIL. O filtro é `button` com `aria-pressed`; não há resumo, grupos, acento nem vazio positivo; `PaginaIncidentes.tsx` reprova no sistema (`rounded-xl` do cartão e `rounded-md` do filtro). Os testes de "Carregar mais", poll, foco e 404 passam.

- [ ] **Step 3: Reescrever a tela**

Substitua `web/src/features/incidentes/PaginaIncidentes.tsx` por:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { CircleCheck } from 'lucide-react'
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import {
  periodoDoIncidente,
  SeloDoIncidente,
  situacaoDoIncidente,
  type SituacaoDoIncidente,
} from '@/components/SeloDoIncidente'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ControleSegmentado, type OpcaoSegmentada } from '@/components/ui/controle-segmentado'
import { useIncidentes, useIncidentesAbertos } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe, textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { agruparIncidentes, resumoDosAbertos } from './agrupamento'

type Filtro = 'open' | 'resolved' | 'all'
const FILTROS: readonly OpcaoSegmentada<Filtro>[] = [
  { valor: 'open', rotulo: 'Abertos' },
  { valor: 'resolved', rotulo: 'Resolvidos' },
  { valor: 'all', rotulo: 'Todos' },
]

// O texto visível que descreve o botão Reconhecer (aria-describedby): o ack
// é idempotente no servidor e não resolve nada, só marca que você viu.
const ID_AJUDA_ACK = 'ajuda-reconhecer'

// Borda de acento (spec do redesign, Telas > 3): vermelha no aberto, índigo
// no reconhecido; o resolvido não tem.
const ACENTO: Record<SituacaoDoIncidente, string> = {
  'em-andamento': 'border-l-4 border-l-fora',
  reconhecido: 'border-l-4 border-l-primaria',
  resolvido: '',
}

function Cartao({
  i,
  agora,
  reconhecendo,
  aoReconhecer,
}: {
  i: Incidente
  agora: Date
  reconhecendo: boolean
  aoReconhecer: (id: string) => void
}) {
  const situacao = situacaoDoIncidente(i)
  return (
    <li
      className={cn(
        'flex flex-wrap items-start justify-between gap-4 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6',
        ACENTO[situacao],
      )}
    >
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Link
            to={`/targets/${i.target_id}`}
            className={cn(
              'alvo-de-toque font-display text-lg font-semibold underline-offset-4 hover:underline',
              situacao === 'resolvido' && 'text-apagado',
            )}
          >
            {i.target_name}
          </Link>
          <SeloDoIncidente incidente={i} />
        </div>
        <p className="text-sm">
          {textoDaCausa(i.cause)} · {periodoDoIncidente(i, agora)}
        </p>
        {i.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some (I1).
          <p id={`ack-${i.id}`} tabIndex={-1} className="text-sm text-suave outline-none">
            Reconhecido em {dataHora(i.acknowledged_at)}
            {situacao === 'reconhecido' && ' · lembretes parados'}
          </p>
        )}
      </div>
      {situacao === 'em-andamento' && (
        <Botao
          tipo="secundario"
          // aria-disabled, e não disabled: o botão desabilitado perderia o
          // foco para o <body> enquanto o POST voa.
          aria-disabled={reconhecendo || undefined}
          onClick={() => {
            if (!reconhecendo) aoReconhecer(i.id)
          }}
          aria-label={`Reconhecer o incidente de ${i.target_name}`}
          aria-describedby={ID_AJUDA_ACK}
        >
          Reconhecer
        </Botao>
      )}
    </li>
  )
}

function Grupo({ titulo, children }: { titulo: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2 id={id} className="rotulo-de-grupo font-sans text-apagado">
        {titulo}
      </h2>
      {children}
    </section>
  )
}

// O vazio dos abertos é o estado bom (auditoria I4): check em círculo verde.
function Vazio({ filtro }: { filtro: Filtro }) {
  if (filtro !== 'open') {
    return <p className="text-suave">{filtro === 'resolved' ? 'Nenhum incidente resolvido.' : 'Nenhum incidente.'}</p>
  }
  return (
    <div className="flex flex-col items-center gap-3 rounded-padrao border border-borda bg-superficie p-8 text-center sombra-nivel-1">
      <span className="inline-flex size-12 items-center justify-center rounded-full bg-no-ar-100 text-no-ar">
        <CircleCheck aria-hidden="true" className="size-6" />
      </span>
      <p className="font-display text-xl font-semibold">Nenhum incidente aberto.</p>
      <p className="text-sm text-suave">Quando um alvo cair, o incidente aparece aqui.</p>
    </div>
  )
}

export function PaginaIncidentes() {
  const [params, setParams] = useSearchParams()
  const p = params.get('status')
  const filtro: Filtro = p === 'resolved' || p === 'all' ? p : 'open'
  const consulta = useIncidentes(filtro)
  // A mesma consulta do contador da navegação: a frase de resumo vale em
  // qualquer filtro, sem requisição a mais.
  const abertos = useIncidentesAbertos()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [erro, setErro] = useState<string | null>(null)
  const [reconhecendo, setReconhecendo] = useState<ReadonlySet<string>>(new Set())
  // O incidente cujo Reconhecer acabou de ser usado. Se o botão sumiu (o ack
  // valeu), o foco iria ao <body>: vai ao "Reconhecido em" da linha, ou ao
  // título da tela se a linha também saiu da lista.
  // Um ref, e um efeito a cada render: o render que o pede é o de tirar o
  // incidente de "reconhecendo", já com a lista relida.
  const focarDepois = useRef<string | null>(null)
  useEffect(() => {
    const id = focarDepois.current
    if (!id) return
    focarDepois.current = null
    if (document.activeElement && document.activeElement !== document.body) return
    const destino = document.getElementById(`ack-${id}`) ?? document.querySelector<HTMLElement>('main h1')
    destino?.focus()
  })

  async function reconhecer(id: string) {
    setErro(null)
    setReconhecendo((s) => new Set(s).add(id))
    try {
      await api.reconhecer(id)
    } catch (e) {
      // M3: o incidente sumiu com o alvo, apagado em outra aba. A lista é
      // relida logo abaixo.
      if (naoEncontrado(e)) avisar('O incidente não existe mais: o alvo foi apagado, talvez em outra aba.')
      else setErro(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: ['incidents'] })
      focarDepois.current = id
      setReconhecendo((s) => {
        const n = new Set(s)
        n.delete(id)
        return n
      })
    }
  }

  const frase = abertos.data ? resumoDosAbertos(abertos.data.itens, abertos.data.completa) : null
  const agora = new Date(consulta.dataUpdatedAt)
  const cartao = (i: Incidente) => (
    <Cartao
      key={i.id}
      i={i}
      agora={agora}
      reconhecendo={reconhecendo.has(i.id)}
      aoReconhecer={(id) => void reconhecer(id)}
    />
  )

  return (
    // Um <div>, e não <section> filho direto do <main>: a regra legada
    // "main > section" do index.css daria moldura a cada grupo.
    <div className="space-y-8">
      <div className="space-y-2">
        <Titulo aba="Incidentes">Incidentes</Titulo>
        {frase && <p className="text-lg text-suave">{frase}</p>}
      </div>
      <ControleSegmentado
        rotulo="Filtro de incidentes"
        opcoes={FILTROS}
        valor={filtro}
        aoMudar={(f) => {
          setErro(null)
          setParams({ status: f }, { replace: true })
        }}
      />
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      {/* D17: um poll ou um "Carregar mais" que falha mantém a lista, com o
          aviso de dados velhos; só sem dados nenhuns o erro toma a tela. */}
      <EstadoDaConsulta consulta={consulta}>
        {(d) => {
          const itens = d.pages.flatMap((pg) => pg.items)
          if (itens.length === 0) return <Vazio filtro={filtro} />
          const grupos = agruparIncidentes(itens)
          return (
            <div className="space-y-8">
              {grupos.emAndamento.some((i) => i.acknowledged_at === null) && (
                <p id={ID_AJUDA_ACK} className="text-sm text-suave">
                  Reconhecer interrompe os lembretes; não resolve o incidente.
                </p>
              )}
              {grupos.emAndamento.length > 0 && (
                <Grupo titulo="Em andamento">
                  <ul className="space-y-3">{grupos.emAndamento.map(cartao)}</ul>
                </Grupo>
              )}
              {grupos.resolvidos.length > 0 && (
                <Grupo titulo="Resolvidos recentes">
                  <ul className="space-y-3">{grupos.resolvidos.map(cartao)}</ul>
                </Grupo>
              )}
              {/* Botão, e não rolagem infinita: previsível no teclado e no leitor de tela. */}
              {consulta.hasNextPage && (
                <Botao tipo="secundario" disabled={consulta.isFetchingNextPage} onClick={() => void consulta.fetchNextPage()}>
                  Carregar mais
                </Botao>
              )}
            </div>
          )
        }}
      </EstadoDaConsulta>
    </div>
  )
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/incidentes src/sistema.test.ts`
Expected: PASS, inclusive os testes antigos ("Nenhum incidente aberto." e "Nenhum incidente." mantidos; o "Reconhecido em" segue com o id `ack-…`).

Sabotagens: troque `ACENTO` do reconhecido por `'border-l-4 border-l-fora'` → o teste dos grupos falha; troque `<Vazio filtro={filtro} />` pelo parágrafo antigo → o vazio positivo falha. Desfaça.

- [ ] **Step 5: Suíte, tipos, lint e build**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Expected: verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/incidentes/PaginaIncidentes.tsx web/src/features/incidentes/incidentes.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): incidentes com filtro segmentado, grupos, cartões e vazio positivo"
```

### Task 27: e2e dos incidentes, verificação do PR 5 e abertura

**Files:**
- Modify: `web/e2e/a11y.spec.ts` (filtro em `radio`; incidentes no teste de nome longo; import de `incidente` já existe)

**Interfaces:**
- Consumes: o contrato da tela da Task 26.
- Produces: nada.

- [ ] **Step 1: O filtro no teste de alvos de toque**

Em `web/e2e/a11y.spec.ts`, no teste `alvos de toque de 44 px`, troque:

```ts
      ...['Abertos', 'Resolvidos', 'Todos'].map(
        (f): [string, Locator] => [`filtro ${f}`, page.getByRole('button', { name: f, exact: true })],
      ),
```

por:

```ts
      ...['Abertos', 'Resolvidos', 'Todos'].map(
        (f): [string, Locator] => [`filtro ${f}`, page.getByRole('radio', { name: f, exact: true })],
      ),
```

e troque o comentário acima de `pontosForaDoAlvo`, na frase "ou o min-h-11 do seletor de janela ou do filtro de incidentes", por "ou o min-h-11 do controle segmentado (janela e filtro de incidentes)".

- [ ] **Step 2: Incidente de nome longo e sem causa**

No teste `muitos alvos e nome longo, ${largura} px`, depois de `await semRolagemLateral(`detalhe ${largura}`)`, acrescente:

```ts
    // Os incidentes, com o nome longo e sem causa (Review Focus 7).
    await page.route(
      (url) => url.pathname === '/api/v1/incidents',
      (route) =>
        route.fulfill({
          status: 200,
          json: { items: [incidente({ id: 'inc-longo', target_id: 'a1', target_name: NOME_LONGO, started_at: agora, cause: '' })] },
        }),
    )
    await page.goto('/incidents')
    await expect(page.getByRole('heading', { name: 'Incidentes', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/incidents')
    await expect(page.getByText(/^Causa não registrada · início /)).toBeVisible()
    await semRolagemLateral(`incidentes ${largura}`)
```

- [ ] **Step 3: Rodar o e2e**

Run: `cd web && npm run build && npx playwright test`
Expected: PASS em todos os specs (Docker). Sabotagem: no `ControleSegmentado`, troque `min-h-11 min-w-11` por `min-h-8 min-w-8`, `npm run build && npx playwright test e2e/a11y.spec.ts -g "alvos de toque"` → falham a janela e o filtro. Desfaça e rode de novo.

- [ ] **Step 4: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint` e, na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 5: Commit e PR**

```bash
git add web/e2e
git commit -m "test(painel): e2e dos incidentes (filtro em radio, nome longo, sem causa)"
git push -u origin feat/redesign-incidentes
gh pr create --title "feat(painel): incidentes agrupados com filtro segmentado e vazio positivo" --body "$(cat <<'EOF'
PR 5 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md (Telas > 3).

- Frase de resumo dos abertos ("1 em andamento, 1 reconhecido."), da consulta que o contador já fazia.
- Filtro no controle segmentado (Abertos, Resolvidos, Todos); grupos "Em andamento" e "Resolvidos recentes".
- Cartão aberto com borda vermelha e Reconhecer; reconhecido com borda índigo e "lembretes parados"; resolvido sem borda, com o nome apagado.
- Vazio positivo nos abertos; incidente sem causa diz "Causa não registrada".

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Antes do merge, o dono revê a tela nos três filtros, nos dois temas, a 400 e a 1280 px.

---

## Testes de tela que mudam nestes três PRs

| PR | Teste | O que acontece | Como fica |
|---|---|---|---|
| 3 | `painel.test.tsx`, "mostra os cinco estados com texto…" | Dependia do `dl/dt` do cartão, do "nunca" solto e do `sr-only` com o resumo | Reescrito como "ordena por gravidade…" e "a manchete conta os problemas…" (Task 16) |
| 3 | `painel.test.tsx`, "resumo operacional > conta os estados reais" | A região "Resumo dos alvos" sai | Substituído pelos testes da meta "N de M no ar" (Task 16) |
| 3 | `painel.test.tsx`, "sem alvos, oferece cadastrar o primeiro" | O parágrafo some | Manchete "Nenhum serviço vigiado ainda." com o mesmo link (Task 16) |
| 3 | `painel.test.tsx`, anúncio, poll, navegação, avisos de canais | Nada: procuram por papel e nome que ficam ("API", "Configurar canais", "Incidentes, N abertos") | Copiados sem mudança |
| 3 | `Minigrafico.test.tsx` | Altura pela latência e marca sob a barra | Reescrito: altura e cor pelo estado (Task 14) |
| 3 | `e2e/a11y.spec.ts`, alvos de toque do `/` | Link "Incidente aberto" | "Ver incidente de Banco" (Task 17) |
| 3 | `e2e/fluxo.spec.ts`, passos 4 e 6 | Supunha cartão `listitem` com o estado; nome casava também "Ver incidente de …" | Região classificada e `exact: true` (Task 17) |
| 4 | `detalhe.test.tsx`, "a série pede a janela…" | `button` e "a última consulta" (os KPIs pedem 24 h e 30 dias) | `radio` "6 h" e `toContainEqual` (Task 23) |
| 4 | `detalhe.test.tsx`, "janela acima de 48h", "trocando de janela", "janela desconhecida" | `button` com `aria-pressed` | `radio` com `toBeChecked` (Task 23) |
| 4 | `detalhe.test.tsx`, Pausar/Retomar, apagar, o alvo sumiu, incidentes do alvo | Nada: nomes iguais; a região "Incidentes recentes" e suas `<li>` ficam | Sem mudança |
| 4 | `grafico.test.tsx`, nome e descrição do `<svg>` | Nada: a descrição ainda contém o resumo | Sem mudança, mais os testes novos (Task 20) |
| 4 | `e2e/a11y.spec.ts`, janela de 7 dias | As barras de uptime saem | Hachura com o padrão (Task 24) |
| 4 | `e2e/a11y.spec.ts`, alvos de toque do detalhe | Janela em `button` | `radio`, mais "Voltar ao painel" (Task 24) |
| 4 | `e2e/fluxo.spec.ts`, passos 4 e 6 | "Painel" casava também "Voltar ao painel"; `.recharts-symbols` sai | Aba da navegação; hachura (Task 24) |
| 5 | `incidentes.test.tsx`, "o filtro vai para a URL", "um ack que falha…" | Filtro em `button` | `radio` (Task 26) |
| 5 | `incidentes.test.tsx`, lista, Carregar mais, poll, foco, 404, "Nenhum incidente." | Nada: textos e ids mantidos | Sem mudança |
| 5 | `e2e/a11y.spec.ts`, alvos de toque dos incidentes | Filtro em `button` | `radio` (Task 27) |

Testes novos da spec, por PR: ordenação por gravidade (Task 13 e 16), manchete com problemas, tudo bem e sem alvos (Tasks 13 e 16), controle segmentado por teclado nas telas (Tasks 23 e 26; o componente já foi testado no PR 1), escala robusta como função pura com o pico fora da escala (Task 19).

## Autorrevisão

- **Cobertura da spec, Telas 1 a 3:** manchete com problemas, tudo bem (check em círculo verde 100 e hora do último resolvido) e meta (Tasks 13 e 16); ordem por gravidade, cartão com borda, selo, causa ou latência, uptime de 24 h e "Ver incidente →"/"Ver detalhes →", cartão clicável inteiro (Task 16, e2e na 17); "Funcionando" com cabeçalho uma vez só e barra com forma e cor (Tasks 14 e 16); "Sem dados ou pausados" em texto (Task 16); esqueleto (Task 15). Detalhe: "← Painel", nome, método, URL e intervalo em apagado, Editar secundário e Pausar/Apagar terciários (Task 23); faixa com Reconhecer (Task 22); quatro KPIs em Inter 600 (Tasks 21 e 23); gráfico com mediana, p50–p95, hachura, horas redondas, escala robusta com pico na legenda e janela segmentada (Tasks 18 a 20 e 23); tabela com números à direita (Task 23); incidentes recentes com etiqueta (Tasks 21 e 23). Incidentes: título, resumo, ajuda do ack, filtro segmentado, grupos, cartões aberto/reconhecido/resolvido e vazio positivo (Tasks 25 e 26). A navegação (Telas 1, item 4) é do PR 2.
- **Placeholders:** nenhum "TBD", "implementar depois" nem "parecido com a Task N"; todo passo de código traz o código.
- **Tipos e nomes:** `Manchete`, `GruposDoPainel`, `Abertos`, `Intervalo`, `PontoDeLatencia`, `EscalaY`, `EixoX`, `Faixa`, `ValorDoIndicador`, `SituacaoDoIncidente` e `GruposDeIncidentes` têm o mesmo nome nas tasks e nas interfaces publicadas. Da parte 1 só se consome o que está publicado: `Botao` (`tipo` `secundario`/`terciario`), `Selo`, `SeloDeEstado`, `TomDoSelo`, `estadoExibido`, `textoDoEstado`, `ControleSegmentado<V>` com `OpcaoSegmentada<V>`, `rotulo`, `opcoes`, `valor`, `aoMudar`.
- **Review Focus:** as nove linhas têm teste na task dona (1: 16; 2: 13 e 16; 3: 17, 24 e 27; 4: 13, 16, 22 e 23; 5: 23; 6: 18, 19 e 20; 7: 13, 16, 21, 23 e 26; 8: 13 e 17; 9: 17, 24 e 27).

## Interfaces publicadas para a parte 3

Tudo abaixo vale a partir do merge do PR indicado.

```ts
// PR 3
// web/src/lib/format.ts
export function horaMinuto(instante: string | number): string   // "09:05"
// web/src/lib/api/mensagens.ts
export function textoDaCausa(cause: string): string             // "" → "Causa não registrada"
// web/src/components/Minigrafico.tsx
export type EstadoDaHora = 'no-ar' | 'degradado' | 'fora'
export function estadoDaHora(h: { total: number; ok: number }): EstadoDaHora
export function Minigrafico(props: { horas: HoraDoSpark[]; agora: Date })
// Gramática de forma da barra (alturas iguais às da página pública, PR 7, onde sem dados é contorno): no ar = altura cheia em
// var(--borda-controle); degradado = 2/3 em var(--estado-degradado); fora = 1/3 em var(--estado-fora); sem dados = lacuna.
// web/src/components/EstadoDaConsulta.tsx
export function EstadoDaConsulta<T>(props: { consulta; esqueleto?: ReactNode; children: (dados: T) => ReactNode })
// web/src/lib/api/consultas.ts
export type Abertos = { itens: Incidente[]; completa: boolean }
export function useIncidentesAbertos(): UseQueryResult<Abertos>
export function useResolvidosRecentes(ativa: boolean): UseQueryResult<ListaDeIncidentes>

// PR 4
// web/src/lib/format.ts
export function diaMes(instante: string | number): string       // "14/10"
// web/src/components/SeloDoIncidente.tsx
export type SituacaoDoIncidente = 'em-andamento' | 'reconhecido' | 'resolvido'
export function situacaoDoIncidente(i: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>): SituacaoDoIncidente
export function periodoDoIncidente(i: Pick<Incidente, 'started_at' | 'resolved_at'>, agora: Date): string
export function SeloDoIncidente(props: { incidente: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>; className?: string })
```

O que a parte 3 vai encontrar:

- **`src/sistema.test.ts`, `ARQUIVOS_NO_SISTEMA`,** acrescentada nesta parte: `src/components/Minigrafico.tsx`, `src/components/SeloDoIncidente.tsx`, `src/features/painel/PaginaPainel.tsx`, `src/features/painel/EsqueletoDoPainel.tsx`, `src/features/alvos/GraficoDeLatencia.tsx`, `src/features/alvos/FaixaDeEstado.tsx`, `src/features/alvos/Indicadores.tsx`, `src/features/alvos/IncidentesRecentes.tsx`, `src/features/alvos/PaginaDetalhe.tsx`, `src/features/incidentes/PaginaIncidentes.tsx`. `USOS_PERMITIDOS` não mudou.
- **`src/tokens.test.ts`:** a constante `TONS_100` e os pares `texto`/`suave` sobre os tons 100 e `primaria`/`foco` sobre eles. A página pública (PR 7) usa os mesmos tons e pode reaproveitar a lista.
- **CSS legado que sobra no `index.css`:** `main > section:not([aria-label='Avisos de notificação'])` (canais, status page e configurações ainda dependem dela; o último PR de tela a remove, no máximo o PR 8) e `#conteudo > form` (PR 6). Saíram `.cartao-alvo`, `.resumo-operacional` e `--grafico-uptime`.
- **`'pt-BR'`:** o detector da parte 1 aceita o locale (o valor de espaçamento começa por minúscula ou dígito). Mesmo assim, os formatadores ficam em `src/lib/format.ts`.
- **e2e:** o teste `muitos alvos e nome longo, ${largura} px` (a 400 e 1280 px) percorre painel, detalhe e incidentes com `semRolagemLateral`; o PR 6 pode acrescentar as telas dele ao mesmo teste. As capturas "depois" do PR 8 devem mostrar o painel com problemas, o "Tudo no ar.", o detalhe de um alvo fora do ar e os incidentes nos três filtros.
- **Comandos:** `cd web && npm test && npm run typecheck && npm run lint && npm run build`; e2e com `cd web && npm run build && npx playwright test` (Docker); na raiz, `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`.
