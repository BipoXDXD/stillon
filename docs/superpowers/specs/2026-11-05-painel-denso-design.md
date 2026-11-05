# Painel denso: tabela de monitores, tags e filtros

Data: 2026-11-05. Continua o [redesign "Editorial claro"](2026-10-14-redesign-painel-design.md).
Regras **DI n** e **TS n** do `~/.claude/CLAUDE.md`. O painel anterior está na tag
`painel-antes-do-denso` (`cdcd0e2`).

## Objetivo

Quem acompanha dezenas de alvos precisa responder de relance "o que está quebrado, desde quando e
de que grupo". Os cartões do redesign ocupam 200 px por problema e separam os estados em três
blocos. Tudo vira **uma tabela só**, com mais informação por linha, tags e filtros.

A referência de densidade e organização é o healthchecks.io. Nada de marca, texto, ícone ou layout
vem de lá.

**Critérios de sucesso**

1. Cada linha de alvo tem 48 px no desktop; 30 alvos cabem em 1600 px, com a manchete.
2. Um problema continua saltando aos olhos, por cor, forma e texto, e fica no topo.
3. Em 400 px a tabela é usável, sem rolagem lateral da página.
4. Um filtro nunca esconde um incêndio: a manchete sempre fala do conjunto inteiro.

**Fora do escopo:** equipes, projetos, integrações novas, tela de heartbeats, ordenação por coluna,
edição em lote e URL ou intervalo na linha (exigem campo novo no resumo).

## O que fica do sistema

Nada muda em tokens, fontes, escala ou primária: Fraunces nos títulos e na manchete, Inter nos
dados, primária índigo, raio de 8 px, sombras de dois níveis, selo com ícone e texto, borda de
acento de 4 px e `Minigrafico` com forma e cor. Os pares de contraste continuam no
`tokens.test.ts`; um par novo entra lá, nos dois temas.

A manchete fica, mais enxuta: é ela que dá a identidade editorial à tela densa. O alvo de toque
fica em 44 px (`alvo-de-toque` e os testes de hit-test do `a11y.spec.ts`), e a linha tem 48 px: com
44, a área de um nome encosta na do vizinho e o teste acusa. A densidade vem de uma linha por alvo e
de mais colunas.

## O que muda

### Tabela de monitores

Uma `<table>` real, com `caption` para leitor de tela e cabeçalho visível uma vez só, ordenada por
gravidade e depois por nome (a ordem de hoje, `agruparPorGravidade`).

| Coluna | Conteúdo | ≥ 1024 px | 640 a 1023 px | < 640 px |
|---|---|---|---|---|
| Estado | `SeloDeEstado` | sim | só o ícone, junto do nome | só o ícone, junto do nome |
| Alvo | nome (`th scope=row`), link para o detalhe | sim | sim | sim |
| Tags | até 3 pílulas e "+N" (PR de tags) | sim | não | não |
| Situação | causa e duração, ou o último check; "Ver incidente" quando houver | sim | sim | em linha abaixo do nome, sem o link, só nos problemas, sem dados e pausados |
| Latência | último check, à direita | sim | sim | não |
| 24 h | uptime truncado, à direita | sim | sim | sim |
| Últimas 24 h | `Minigrafico` | sim | sim | sim |

- Linha de problema: borda de acento de 4 px na primeira célula, na cor do estado. O nome pesa 600
  em toda linha; o estado bom fica calmo, sem fundo colorido.
- Pausado e sem dados continuam na mesma tabela, no fim, com o texto em `suave`.
- Só o nome é link. Um link esticado sobre o `<tr>` depende de `position: relative` em linha de
  tabela; onde isso falhar, ele cobre a tabela inteira.
- O texto de cada estado vem das funções atuais (`detalheDoProblema`, `linhaSemDados`), para a
  frase não mudar de sentido.

### Pronta para um segundo tipo de monitor

A tabela não conhece `ItemDoResumo`. Ela recebe `LinhaDeMonitor`, uma união discriminada por
`tipo` que hoje só tem `'http'` (`linhas.ts`), montada por uma função pura. O `href`, a chave da
linha e o texto de medida ficam no modelo, e a medida passa por um `switch` exaustivo: um tipo
novo sem caso não compila (TS 5). A frente de heartbeats acrescenta o membro da união e o mapeador
dela, sem tocar nas colunas. Nada de heartbeat é implementado aqui.

### Tags

Modelo normalizado: `target_tags (target_id, tag)` com chave primária composta, `ON DELETE
CASCADE`, RLS `via target` igual à de `check_rollup_1h` e `CHECK` espelhando a validação.

| Regra | Valor | Motivo |
|---|---|---|
| Por alvo | até 8 | passa disso, tag vira descrição |
| Tamanho | 1 a 24 caracteres | cabe na pílula |
| Caracteres | letras, dígitos, `-` e `_` | allowlist; vírgula é o separador do campo |
| Forma | minúsculas, sem espaço nas pontas, duplicata some | `Prod` e `prod` são a mesma tag |

`unicode.IsLetter` recusa a forma decomposta do Unicode (o acento combinante é `Mn`), então
"produção" tem uma grafia só, sem dependência nova.

### API

- `tags: string[]` em `GET/POST/PATCH /targets` e em cada item de `GET /targets/summary`. No
  `PATCH`, ausente não mexe; `null` e `[]` limpam.
- `GET /targets/summary?tag=a&tag=b&state=down&state=degraded`:
  - `tag`: todas precisam estar no alvo (E). No máximo 8 valores, cada um pela mesma validação.
  - `state`: qualquer um serve (OU), por allowlist: `up`, `degraded`, `down`, `unknown`, `paused`.
  - Valor fora da allowlist ou acima do limite: 400 com o campo e o motivo.
  - Parâmetro desconhecido ou query malformada (`?tag=%zz`, `?tag=a;tag=b`): 400 no campo fixo `query`,
    sem ecoar a chave.
- Tudo roda dentro de `InUserTx` e filtra por `user_id`. Tag de outro usuário não aparece nem como
  filtro: o resultado é vazio, igual a uma tag inexistente, e não vaza existência. `PATCH` em alvo
  alheio segue 404.

### Filtros no painel

Barra acima da tabela, com o estado na URL (`?tag=`, `?estado=`, `?busca=`), para sobreviver ao
recarregar e ao botão Voltar:

- **Estado:** botões `aria-pressed`, com ícone, texto e contagem (`Fora do ar 2`). Contagens zero
  somem, exceto a do filtro ativo.
- **Tags:** botões `aria-pressed`, com a contagem.
- **Busca:** campo por nome ou tag, local e instantâneo, sem uma requisição por tecla.
- **Limpar filtros**, terciário, só com filtro ativo.

Tag e estado vão à API, a pedido do brief. A consulta completa continua em poll, porque a manchete,
as contagens e a lista de tags dependem do conjunto inteiro; com filtro ativo há uma segunda
consulta filtrada. O custo é dobrar o poll do resumo enquanto há filtro (de 4 para 8 chamadas por
minuto, dentro do limite de 60). Cortar isso exigiria filtrar só no cliente, o que deixaria a API de
filtro sem uso.

### Cadastro e detalhe

- O formulário do alvo ganha o campo **Tags**, separado por vírgula, com a mesma validação do
  servidor (TS 3).
- O detalhe mostra as tags abaixo da URL.

## Estados

| Estado | O que aparece |
|---|---|
| Carregando | esqueleto da manchete e de 8 linhas de 48 px; `aria-busy`; sem animação com `prefers-reduced-motion` |
| Sem alvos | a manchete de hoje, com "Cadastrar o primeiro alvo"; sem tabela nem barra de filtros |
| Erro do resumo | `EstadoDaConsulta` de hoje, com "Tentar de novo" |
| Erro só da consulta filtrada | a manchete e a barra ficam; no lugar da tabela, a mensagem com "Tentar de novo" |
| Sem resultado do filtro | "Nenhum alvo com esses filtros.", com a frase do que está ativo e "Limpar filtros"; a tabela some, a barra fica |
| Filtro trocando | a tabela anterior fica visível com `aria-busy` até a resposta (`keepPreviousData`) |
| Tudo no ar | o check verde e a hora do último incidente, como hoje |

## Mobile (400 px)

- Abaixo de 640 px: Alvo (ícone do estado + nome), 24 h e Últimas 24 h, com `px-2`. A Latência sai
  porque espremia o nome, e o degradado já a diz na situação.
- Linha `no ar` fica em uma altura; problema, sem dados e pausado ganham a situação abaixo do nome.
- "Ver incidente" fica fora em 400 px: o nome leva ao detalhe, que tem a faixa do incidente.
- Nome longo quebra dentro da célula (`overflow-wrap`), nunca alarga a página.
- A barra de filtros quebra linha; os botões têm 44 px. A busca ocupa a largura inteira.
- As tags não aparecem na linha em 400 px; o filtro por tag continua lá.

## Testes

- Vitest: `linhas.ts` (mapeamento por estado, incluindo o `switch` exaustivo), ordenação, colunas
  e células da tabela, filtros na URL, busca, "sem resultado", esquema de tags.
- `a11y.spec.ts`: axe nos dois temas, alvos de toque, sem rolagem lateral com 32 alvos e nome de
  100 caracteres, linha `no ar` até 48 px em 400 e 1280 px.
- Go: validação, migration, RLS e `target_tags`, filtro do resumo, allowlist, limites, usuário B
  sem ver tags de A, 404 em alvo alheio, canário de chaves do JSON.
- Todo teste novo é visto falhando com o código sabotado; a sabotagem vai no corpo do PR.

## Migrations e ordem de merge

A 00017 é a das tags (a 00016 é dos heartbeats, e a 00015 fica vaga; ver a [spec de heartbeats](2026-11-05-heartbeats-design.md)). O
goose do projeto **aceita lacuna** de numeração, mas **não aceita migration menor que a versão já
aplicada**. Provado em Postgres 18 com um teste descartável: 14 + 17 aplicam, e a 16 chegando
depois falha com `detected 1 missing (out-of-order) migration lower than database version (17)`.
Como `storage.Migrate` roda no boot, isso derruba o release.

**A 00016 tem de chegar à `main` antes da 00017.** O PR de tags só entra depois do de heartbeats.

## Entrega

1. Esta spec (`[skip ci]`).
2. `feat(web)`: tabela densa, sem tags, sem mudança de API.
3. `feat(api)`: migration 00017, `tags` nos alvos e filtro do resumo.
4. `feat(web)`: tags e filtros, cadastro, detalhe e capturas do README.

Os PRs 2 e 3 são independentes entre si. O 4 parte dos dois. As capturas do README só mudam no 4.
