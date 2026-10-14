# Redesign do painel, parte 3: demais telas (PR 6), página pública (PR 7) e fechamento (PR 8). Plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para implementar este plano tarefa a tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.

**Objetivo:** levar a direção C, "Editorial claro", às telas que faltam (Canais, configuração da status page, Configurações, Novo e editar alvo, Login), reescrever a página pública com os mesmos tokens e fontes, e fechar o redesign com as capturas "depois" e o README.

**Arquitetura:** o PR 6 é só front-end em `web/`, sobre os componentes da parte 1 (`Botao`, `Campo`, `CaixaDeMarcar`, `Selo`, `Marca`) e dois nomes novos, `estiloDeCartao` e `Opcao`. O PR 7 é Go: o template `internal/httpapi/templates/status.html` ganha os tokens OKLCH do painel no CSS inline (com nonce), as fontes de `/fonts/` e a faixa de estado; a CSP ganha `font-src 'self'`; a resposta sai em gzip para caber nos 10 KB da spec original. A frase da faixa é uma regra só, com os casos num JSON que o teste Go e o teste da prévia do painel leem. O PR 8 põe todo o `src` no teste do sistema, gera as capturas com um roteiro no repositório e atualiza o README.

**Tech stack:** Vite 8, React 19, Tailwind 4.3, lucide-react, react-hook-form, Vitest 5 + Testing Library + user-event, Playwright 1.63 + axe; Go 1.26 (`html/template`, `compress/gzip`, `encoding/json`).

**Spec:** [`docs/superpowers/specs/2026-10-14-redesign-painel-design.md`](../specs/2026-10-14-redesign-painel-design.md) (seções "Telas" 4, 5 e 6, "Testes" e "Ordem de entrega" 6 a 8). Parte 1: [`2026-10-14-redesign-painel-parte1.md`](2026-10-14-redesign-painel-parte1.md), seção "Interfaces publicadas para os PRs seguintes". Contexto: [auditoria](../reports/2026-10-14-auditoria-painel.md) (códigos C1–C3, G1–G3, S9, S10, SP2–SP6).

**Ponto de partida:** a `main` com os PRs 1 a 5 integrados (partes 1 e 2). Este plano não depende de nada da parte 2 além de o painel funcionar e o `sistema.test.ts` continuar com as regras da parte 1.

## Global Constraints

- Direção **C, "Editorial claro"**; fontes **Fraunces** (títulos, frase de estado, marca, nome) + **Inter** (texto, dados, formulários); primária **índigo**: `oklch(0.47 0.15 270)` no claro, `oklch(0.76 0.11 270)` no escuro.
- Fontes auto-hospedadas na URL estável `/fonts/…` (`fraunces-latin.woff2`, `inter-latin.woff2`, da parte 1). Nada de `@fontsource-variable`.
- Contraste: **4,5:1** para texto e **3:1** para componente, foco e objeto gráfico, nos dois temas. Um par reprovado ajusta a luminosidade, nunca o matiz.
- Escala de fonte **12, 13, 14, 16, 18, 20, 24, 28, 36 px**; pesos **400 e 600**; espaçamento só **4/8/12/16/24/32/48/64**; **um raio de 8 px** (`rounded-padrao`), pílula (`rounded-full`) para selos; sombras `sombra-nivel-1` e `sombra-nivel-2`; cor só por token.
- Botões em pirâmide: primário sólido, secundário com contorno, terciário em texto. **Destrutivo é terciário até a confirmação; só o botão do diálogo de confirmação é vermelho** (`tipo="perigo"`, contado em `USOS_PERMITIDOS`).
- Ícones **só Lucide**. Checkbox e radio com `accent-color` da primária. Alvos de toque de **44 px**, foco visível, `prefers-reduced-motion` respeitado, axe limpo nos dois temas.
- Página pública: **sem JavaScript**, CSS inline com o **nonce** da resposta, sem importar o CSS do painel, tema escuro por **`prefers-color-scheme`** (sem seletor de tema), resposta **abaixo de 10 KB** (spec original §10.1) e **nada além do listado na §5.6** (nome de exibição, estado, uptime, latência agregada, incidentes com início e fim).
- **Sem mudança de API**, de rotas, de dados ou de comportamento. O que a página pública não tem nos dados atuais fica de fora (seção "Decisões").
- Use os nomes publicados da parte 1 (`Botao`/`TipoDeBotao`, `Campo`, `Entrada`, `Selecao`, `CaixaDeMarcar`, `ErroDoFormulario`, `estiloDeEntrada`, `Selo`/`TomDoSelo`, `SeloDeEstado`, `estadoExibido`, `textoDoEstado`, `Marca`, `Titulo`, `Confirmar`, `Dialogo`, `cn`, os tokens e utilitários). Não crie sinônimos.
- Projeto: tudo em português do Brasil; commits `tipo(escopo): descrição`; mudança na `main` só por PR; **teste novo só vale depois de vê-lo falhar com o código sabotado**; `sh deploy/ci/checar-nome.sh` passa.

## Review Focus

1. **Status page sem nenhum item.** A página pública mostra o título e a faixa "Nenhum serviço nesta página.", sem lista, sem legenda e sem a nota das barras; o histórico continua. No painel, a prévia some. Testes: Task 7.4 (`TestPublicPageWithoutItems`) e o teste que já existe em `statuspage.cobertura.test.tsx` ("tirar o último alvo… a prévia some"), mantido na Task 6.4.
2. **Componente sem dados nos 90 dias** (alvo novo ou pausado o tempo todo). A barra inteira é contorno ("sem dados"), o uptime diz "Sem dados nos últimos 90 dias", o selo diz "Sem dados" e a faixa diz "Sem dados recentes de 1 serviço.". Teste: Task 7.4 (`TestComponentWithoutDataIn90Days`).
3. **Nome de organização longo** (100 caracteres sem espaço, o teto do servidor). O título quebra em qualquer ponto e a página não ganha rolagem horizontal no celular. Testes: Task 7.4 (`TestLongOrganizationNameWraps`) e Task 7.5 (e2e de 400 px, `scrollWidth` igual à largura).
4. **A CSP bloqueando a fonte.** Sem `font-src 'self'`, o navegador recusa `/fonts/…` e a página cai na fonte do sistema em silêncio. Testes: Task 7.1 (a CSP inteira, string exata: libera só as fontes do próprio host e mais nada), Task 7.4 (o template só aponta `url(/fonts/…)`) e Task 7.5 (e2e: as duas `FontFace` em `loaded` e zero violação de CSP).
5. **Tema escuro da página pública.** Cada token do escuro tem de ser o do painel, e cada texto, barra e faixa tem de passar no contraste com o fundo escuro. Testes: Task 7.4 (`TestPublicPageUsesThePanelTokens` e `TestPublicPageColorsAreAccessible`, nos dois temas) e Task 7.5 (e2e com `prefers-color-scheme: dark`: fundo escuro computado e axe limpo).
6. **Formulários com erro de validação no celular de 400 px.** O erro aumenta o formulário; ele não pode ganhar overflow, o texto do erro tem de aparecer, e nem o campo que recebe o foco nem o botão de envio podem ficar embaixo da barra inferior fixa (WCAG 2.4.11). Teste: Task 6.8 (e2e de 400 px em Novo alvo, Status page, Configurações e Login, nos dois temas).

## Decisões deste plano (pontos da spec que pediram interpretação)

- **10 KB da página pública (spec original §10.1):** medido hoje, o pior caso (5 serviços, os 90 dias espalhados nas quatro classes) já tem 9.628 bytes; o template novo, com os tokens dos dois temas, as `@font-face`, a faixa e a legenda, vai a ~12,3 KB. Não cabe em texto puro. A resposta passa a sair em **gzip** quando o cliente aceita (todo navegador): o pior caso cai a ~2 KB, e o caso de 20 serviços, que antes passava dos 10 KB (limite registrado no plano 4), também cabe. O teste mede o corpo comprimido contra os 10 KB e o texto puro contra um teto de 16 KB. A alternativa, apertar o CSS, não fecha a conta: o corpo sem o CSS já ocupa ~8,6 KB. **O dono confirma esta leitura da §10.1 na revisão do PR 7.**
- **O que a página pública não tem nos dados atuais** (`statuspage.View`), e por isso fica de fora, sem API nova: o início do incidente **em andamento** (a faixa não diz "fora do ar há 2 h"; a projeção só traz incidentes resolvidos); o **tipo** do incidente resolvido (fora do ar ou lento), então o histórico continua neutro; a **causa** (fora por decisão da §5.6); a hora do último check de cada serviço; resolução horária do dia de hoje; e horário local (tudo segue em UTC, como hoje). A faixa, a legenda, as barras de 90 dias, o uptime de 90 dias e o histórico saem do que já existe.
- **Frase da faixa:** o pior estado presente dá a frase: "N serviço(s) fora do ar.", "Instabilidade em N serviço(s)." (o exemplo da spec), "Sem dados recentes de N serviço(s)." ou "Todos os sistemas funcionando."; página vazia, "Nenhum serviço nesta página.". O detalhe lista os nomes por estado, na ordem da página, com os rótulos que a página já usa ("Fora do ar: A, B. Lento: C. Sem dados: D."). A regra mora no Go (`faixa`) e numa cópia em TypeScript para a prévia do painel; as duas leem os casos de `internal/httpapi/testdata/faixa.json` nos testes, então não divergem.
- **Rótulos:** o estado atual do serviço continua "No ar", "Lento", "Fora do ar", "Sem dados" (vocabulário que a página já usa). A legenda dos dias usa "Instável" para 99% a 99,9%, porque um dia instável não é necessariamente lento.
- **Formas dos dias:** no ar com altura cheia, instável com 66%, fora do ar com um terço (33%), sem dados só o contorno (borda de controle, 3:1). As cores são as de estado do painel, todas acima de 3:1 sobre o cartão e sobre a página nos dois temas (conferido: o menor par é a borda de controle sobre a superfície escura, 3,30:1).
- **Histórico curto:** cada incidente resolvido já sai numa frase ("Nome: 24/09/2026 10:00 a 24/09 11:35 (1 h 35 min)"), com o teto de 20 da projeção. Só muda o estilo; o texto fica, para não crescer a página nem mexer no que o e2e Go confere.
- **`@font-face` da página pública sem `unicode-range`:** o arquivo já é o subconjunto latino; um caractere fora dele cai na família genérica glifo a glifo do mesmo jeito. Economiza ~400 bytes. As pilhas terminam em família genérica.
- **`<main>` na página pública:** custa 13 bytes e dá o landmark que ela não tinha.
- **Canal pausado (C3):** o selo é "Pausado" no tom `degradado` (âmbar), porque canal pausado descarta avisos; a verificação vira texto apagado ao lado. O alvo pausado do painel continua cinza (`sem-dados`): pausar um alvo não perde nada.
- **Subir, Descer e Tirar (SP2):** reordenar é secundário, em botão de ícone (`ArrowUp`, `ArrowDown`) com o nome acessível de hoje; Tirar é terciário em texto.
- **Prévia (SP3):** a prévia ganha o "papel", o título em Fraunces, a mesma faixa da página pública e um cartão por alvo. O painel não tem os 90 dias de cada alvo (só o resumo de 24 h), então a barra vira um contorno tracejado e uma nota diz que as barras e o histórico aparecem na página publicada. O rótulo "Prévia aproximada" fica.
- **Nomes novos (não são sinônimos de nada da parte 1):** `estiloDeCartao` (string de classes, como o `estiloDeEntrada`), porque o cartão se repete em quatro telas; `Opcao` (radio com a linha de 44 px, irmão da `CaixaDeMarcar`), porque há três grupos de rádio. A Task 6.1 confere antes se a parte 2 já criou um utilitário de cartão.
- **CSS legado:** o `#conteudo > form` sai na Task 6.1 e o `main > section:not(…)` na Task 6.6. Os dois têm especificidade maior que uma classe do Tailwind e venceriam o `p-4`/`p-6` do `estiloDeCartao` com o padding de 20 px fora da escala. O PR 6 é o último PR de tela, então é ele que os dispensa (regra de manutenção da parte 1).
- **Foco fora da barra inferior:** o `pb-16` da parte 1 resolve o fim da página rolada, mas não o foco: o teclado e o erro do formulário levam o foco a um campo que o navegador pode parar embaixo da barra fixa. A Task 6.8 põe `scroll-padding-bottom: 4rem` no `html` abaixo de `lg`.
- **`/fonts/` na página pública:** o `internal/webui` já serve `/fonts/*.woff2` como `font/woff2`, `no-cache`, pela cadeia do painel montada em `/` (teste da parte 1, Task 1). A página `/p/{slug}` está no mesmo host, então `font-src 'self'` basta. Um binário compilado sem `npm run build` responde 404 nas fontes; a página continua legível na fonte do sistema (`font-display: swap`).
- **Capturas (PR 8):** o roteiro fica no repositório (`web/e2e/capturas.spec.ts`, só com `CAPTURAR=1`), com a API imitada do `a11y.spec.ts` extraída para `web/e2e/api-imitada.ts`. A página pública vem do template de verdade, renderizado com dados de exemplo por um teste Go que só roda com `STILLON_EXEMPLO_PUBLICA`. Ela ganha as quatro variações (400 e 1280, claro e escuro); o "antes" só tinha `status-publica-1280-claro.png`, porque a página não tinha tema escuro desenhado.
- **Numeração:** as tasks levam o número do PR (6.1, 7.1, 8.1) para não colidir com a numeração da parte 2.

## Mapa de arquivos

PR 6, demais telas:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/components/ui/cartao.ts` (novo) | `estiloDeCartao` |
| `web/src/components/ui/campo.tsx` + `campo.test.tsx` | `Opcao` (radio de 44 px) |
| `web/src/features/canais/PaginaCanais.tsx` + `canais.test.tsx` | Selos Lucide, "Pausado" em destaque, cartões, `Opcao` |
| `web/src/features/statuspage/faixa.ts` + `faixa.test.ts` (novos) | Frase da faixa (cópia da regra do Go) |
| `internal/httpapi/testdata/faixa.json` (novo) | Casos da faixa, lidos pelo TS e pelo Go |
| `web/src/features/statuspage/PreviaDaPagina.tsx` (novo) | Prévia que parece a página pública |
| `web/src/features/statuspage/PaginaStatusPage.tsx` + `statuspage.test.tsx` | Cartões, Subir/Descer/Tirar, prévia |
| `web/src/features/alvos/FormularioAlvo.tsx` + `alvos.test.tsx` | Cartão, `Opcao`, legendas |
| `web/src/features/configuracoes/PaginaConfiguracoes.tsx` + `configuracoes.test.tsx` | Seções em cartão, selo "Esta sessão", QR sem `bg-white` |
| `web/src/features/login/PaginaLogin.tsx` + `login.test.tsx` | Marca, frase de tom, cartão nível 2 |
| `web/src/index.css` | Saem `#conteudo > form` e `main > section:not(…)`; entra `scroll-padding-bottom` |
| `web/src/sistema.test.ts` | Arquivos novos na lista; CSS legado ausente |
| `web/e2e/a11y.spec.ts` | Texto do canal pausado; formulários com erro a 400 px |

PR 7, página pública:

| Arquivo | Responsabilidade |
|---|---|
| `internal/httpapi/publicpage.go` | `politicaDaPagina`, `faixa`, `bandModelo`, `comprimida`, handler com gzip |
| `internal/httpapi/templates/status.html` | Template novo |
| `internal/httpapi/publicpage_test.go` | Faixa (casos do JSON), tamanho comprimido, renderização |
| `internal/httpapi/publicpage_a11y_test.go` (reescrito) | OKLCH → sRGB, contraste, formas dos dias, paridade com o painel |
| `internal/httpapi/publicpage_integration_test.go` | Frase nova, `font-src`, gzip |
| `internal/httpapi/painel_integration_test.go` | `/fonts/…` no mesmo host |
| `internal/webui/webui.go` + `webui_test.go` | `AceitaGzip` exportado e testado |
| `web/e2e/pagina-publica.spec.ts` (novo) | e2e contra o binário |

PR 8, fechamento:

| Arquivo | Responsabilidade |
|---|---|
| `web/src/sistema.test.ts` | Todo o `src` no teste do sistema |
| `web/e2e/api-imitada.ts` (novo, extraído) | A API imitada do `a11y.spec.ts` |
| `web/e2e/capturas.spec.ts` (novo) | Roteiro das capturas |
| `internal/httpapi/publicpage_test.go` | `TestGerarExemploDaPaginaPublica` |
| `docs/design/painel-depois/*.png` (gerados) | 44 capturas |
| `README.md` | Seção "Telas" com as imagens novas |

## Comandos de verificação

Os da parte 1, mais o portão Go:

```bash
cd web && npm test && npm run typecheck && npm run lint && npm run build
cd web && npm run build && npx playwright test          # e2e: precisa do Docker
go build ./... && go vet ./... && make test             # na raiz: precisa do Docker
sh deploy/ci/checar-nome.sh
```

---

# PR 6: Demais telas

Branch: `feat/redesign-demais-telas`, a partir da `main` com os PRs 1 a 5 integrados.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-demais-telas
```

### Task 6.1: Cartão do sistema, opção de rádio e o fim do `#conteudo > form`

**Files:**
- Create: `web/src/components/ui/cartao.ts`
- Modify: `web/src/components/ui/campo.tsx` (nova função `Opcao`, depois de `CaixaDeMarcar`)
- Modify: `web/src/components/ui/campo.test.tsx`
- Modify: `web/src/index.css` (sai a regra `#conteudo > form`)
- Modify: `web/src/features/alvos/FormularioAlvo.tsx` (classe do `<form>`, imports)
- Modify: `web/src/features/statuspage/PaginaStatusPage.tsx` (classe do `<form>` do `Formulario`, imports)
- Modify: `web/src/features/alvos/alvos.test.tsx`, `web/src/features/statuspage/statuspage.test.tsx`
- Modify: `web/src/sistema.test.ts`

**Interfaces:**
- Consumes: `cn` (`@/lib/utils`); classes `rounded-padrao`, `sombra-nivel-1`, `bg-superficie`, `border-borda` (parte 1).
- Produces:

```ts
// web/src/components/ui/cartao.ts
export const estiloDeCartao: string
// 'rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6'
// Uso: cn(estiloDeCartao, 'max-w-2xl space-y-6'). Quem usa põe largura e espaço entre filhos.

// web/src/components/ui/campo.tsx
export function Opcao(props: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode })
// <label> de 44 px com <input type="radio">; className vai para a linha, o resto ao input (register do RHF).
```

- [ ] **Step 1: Conferir se a parte 2 já criou um cartão**

Run: `cd web && grep -rn "estiloDeCartao\|export const .*[Cc]artao\|function Cartao" src`
Expected: nada. Se aparecer um `estiloDeCartao` com o mesmo valor, pule a criação do arquivo no Step 4 e use o import que já existe; se aparecer outro utilitário de cartão, use-o no lugar de `estiloDeCartao` em todas as tasks deste PR e registre a troca na descrição do PR.

- [ ] **Step 2: Escrever os testes que falham**

Em `web/src/components/ui/campo.test.tsx`, acrescente `Opcao` ao import de `./campo` e, no fim do arquivo:

```tsx
describe('Opcao', () => {
  it('é um radio com o próprio rótulo, e a linha inteira tem 44 px', async () => {
    render(
      <>
        <Opcao name="tipo" value="discord">
          Discord
        </Opcao>
        <Opcao name="tipo" value="email">
          E-mail
        </Opcao>
      </>,
    )
    const email = screen.getByRole('radio', { name: 'E-mail' })
    await userEvent.click(screen.getByText('E-mail'))
    expect(email).toBeChecked()
    expect(email.closest('label')).toHaveClass('min-h-11')
  })

  it('repassa ref e atributos ao input, como o register do react-hook-form pede', () => {
    const ref = createRef<HTMLInputElement>()
    render(
      <Opcao ref={ref} name="kind" value="discord" defaultChecked>
        Discord
      </Opcao>,
    )
    const opcao = screen.getByRole('radio', { name: 'Discord' })
    expect(ref.current).toBe(opcao)
    expect(opcao).toHaveAttribute('value', 'discord')
    expect(opcao).toBeChecked()
  })
})
```

No fim de `web/src/features/alvos/alvos.test.tsx`:

```tsx
// Auditoria G3: o cartão tem a largura do formulário, e não a da página.
// Antes, o cartão vinha do "#conteudo > form" do index.css, que saiu.
describe('formulário de alvo no sistema visual', () => {
  it('o formulário é um cartão de largura limitada', async () => {
    await renderizarPainel('/targets/new')
    const formulario = (await screen.findByLabelText('Nome')).closest('form')
    expect(formulario).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie', 'sombra-nivel-1')
  })
})
```

Em `web/src/features/statuspage/statuspage.test.tsx`, troque o import das fixtures por `import { alvo, pagina } from '@/test/fixtures'` e acrescente, dentro do `describe` principal (depois do primeiro `it`):

```tsx
  it('o formulário é um cartão de largura limitada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [pagina()]
    await r.cliente.refetchQueries()
    const formulario = (await screen.findByLabelText('Título')).closest('form')
    expect(formulario).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie', 'sombra-nivel-1')
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/components/ui/cartao.ts'` à `ARQUIVOS_NO_SISTEMA` e, no fim do arquivo:

```ts
// Regras de manutenção da parte 1: o CSS legado de tela sai com o PR que a
// redesenha. Estas regras têm especificidade maior que uma classe e venceriam
// o padding do estiloDeCartao com 20 px, fora da escala.
describe('CSS legado removido pelo PR 6', () => {
  const css = readFileSync('src/index.css', 'utf8')
  it.each(['#conteudo > form'])('o index.css não tem mais %s', (seletor) => {
    expect(css).not.toContain(seletor)
  })
})
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd web && npx vitest run src/components/ui/campo.test.tsx src/sistema.test.ts src/features/alvos/alvos.test.tsx src/features/statuspage/statuspage.test.tsx`
Expected: FAIL. `Opcao` não é exportado (elemento inválido); `ENOENT` em `src/components/ui/cartao.ts`; `#conteudo > form` ainda está no CSS; os dois formulários não têm `max-w-2xl` com `rounded-padrao` (o de status page tem `max-w-2xl`, mas não o resto; o de alvo também).

- [ ] **Step 4: Criar o `estiloDeCartao` e a `Opcao`**

Crie `web/src/components/ui/cartao.ts`:

```ts
// O cartão do sistema (spec do redesign, "Sistema"): superfície sobre o
// papel, raio único, borda decorativa e sombra nível 1 (no escuro a sombra é
// transparente e a superfície mais clara faz o papel). O padding cresce no
// desktop (DI 5). Quem usa acrescenta a largura (max-w-2xl nos formulários,
// auditoria G3) e o espaço entre os filhos.
export const estiloDeCartao = 'rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6'
```

Em `web/src/components/ui/campo.tsx`, logo depois da função `CaixaDeMarcar`:

```tsx
// Botão de opção (radio) com o rótulo na mesma linha, que inteira é a área de
// toque (44 px), como a CaixaDeMarcar. A cor da marca vem do accent-color
// global do index.css. className vai para a linha; o resto das props, ao
// input (o register do react-hook-form passa name, ref e eventos).
export function Opcao({
  children,
  className,
  ...props
}: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode }) {
  return (
    <label className={cn('flex min-h-11 items-center gap-3 text-sm', className)}>
      <input type="radio" className="size-5 shrink-0" {...props} />
      {children}
    </label>
  )
}
```

- [ ] **Step 5: Tirar o `#conteudo > form` e dar o cartão aos dois formulários**

Em `web/src/index.css`, apague o bloco:

```css
#conteudo > form {
  border: 1px solid var(--borda);
  border-radius: 0.5rem;
  background: var(--superficie);
  padding: 1.25rem;
}
```

Em `web/src/features/alvos/FormularioAlvo.tsx`, acrescente os imports:

```tsx
import { estiloDeCartao } from '@/components/ui/cartao'
import { cn } from '@/lib/utils'
```

e troque a abertura do formulário:

```tsx
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
```

por:

```tsx
    <form noValidate className={cn(estiloDeCartao, 'max-w-2xl space-y-6')} onSubmit={f.handleSubmit(enviar)}>
```

Em `web/src/features/statuspage/PaginaStatusPage.tsx`, os mesmos dois imports, e no `Formulario`:

```tsx
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
```

vira:

```tsx
    <form noValidate className={cn(estiloDeCartao, 'max-w-2xl space-y-6')} onSubmit={f.handleSubmit(enviar)}>
```

- [ ] **Step 6: Rodar e ver passar**

Run: o mesmo comando do Step 3.
Expected: PASS.

Sabotagens (veja falhar e desfaça): na `Opcao`, troque `min-h-11` por `min-h-10` → "a linha inteira tem 44 px" falha; no `estiloDeCartao`, troque `rounded-padrao` por `rounded-lg` → o teste do sistema reprova `cartao.ts` e os dois testes de formulário falham.

- [ ] **Step 7: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/ui/cartao.ts web/src/components/ui/campo.tsx web/src/components/ui/campo.test.tsx web/src/index.css web/src/features/alvos/FormularioAlvo.tsx web/src/features/alvos/alvos.test.tsx web/src/features/statuspage/PaginaStatusPage.tsx web/src/features/statuspage/statuspage.test.tsx web/src/sistema.test.ts
git commit -m "feat(painel): cartão do sistema, opção de rádio de 44 px e fim do CSS legado de formulário"
```

### Task 6.2: Canais com selos Lucide e "Pausado" em destaque

**Files:**
- Modify: `web/src/features/canais/PaginaCanais.tsx`
- Modify: `web/src/features/canais/canais.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)
- Modify: `web/e2e/a11y.spec.ts` (texto do canal pausado)

**Interfaces:**
- Consumes: `Selo`, `TomDoSelo` (`@/components/SeloDeEstado`); `estiloDeCartao`, `Opcao` (Task 6.1); `cn`; ícones `CircleCheck`, `Clock`, `TimerOff`, `CircleX`, `Pause` de `lucide-react`.
- Produces: nada para fora. Textos que os testes procuram: "Verificado", "Aguardando verificação", "Verificação expirada", "Falhando", "Pausado", "Os avisos são descartados.".

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/features/canais/canais.test.tsx`, troque os textos com glifo (todas as ocorrências, inclusive as de `findAllByText` e `queryByText`):

| De | Para |
|---|---|
| `'✓ Verificado'` | `'Verificado'` |
| `'… Aguardando verificação'` | `'Aguardando verificação'` |
| `'✕ Falhando'` | `'Falhando'` |
| `'⧖ Verificação expirada'` | `'Verificação expirada'` |
| `'⏸ Pausado: os avisos são descartados'` | `'Pausado'` |

Confira: `cd web && grep -n "✓\|⧖\|✕\|⏸\|… Aguardando" src/features/canais/canais.test.tsx`
Expected: nenhuma linha.

Acrescente, no fim do `describe('canais', …)`:

```tsx
  // Auditoria C2 e S9: os estados usam ícones Lucide, com forma, cor e texto,
  // e não glifos Unicode de métrica desigual.
  it('cada estado é um selo com ícone Lucide e texto, sem glifo Unicode', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal(),
      canal({ id: 'c2', kind: 'email', hint: 'd***@exemplo.com', status: 'pending' }),
      canal({ id: 'c3', kind: 'email', hint: 'e***@exemplo.com', status: 'expired' }),
      canal({
        id: 'c4',
        hint: 'discord.com/…/zzzz',
        status: 'failing',
        last_error: 'invalid_channel',
        last_failure_at: '2026-09-24T11:00:00Z',
      }),
    ]
    await r.cliente.refetchQueries()
    const falhando = await screen.findByText('Falhando')
    for (const [texto, fundo] of [
      ['Verificado', 'bg-no-ar-100'],
      ['Aguardando verificação', 'bg-degradado-100'],
      ['Verificação expirada', 'bg-sem-dados-100'],
      ['Falhando', 'bg-fora-100'],
    ] as const) {
      const selo = screen.getByText(texto)
      expect(selo).toHaveClass('rounded-full', fundo)
      expect(selo.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    expect(falhando.closest('ul')?.textContent).not.toMatch(/[✓⧖✕⏸]/)
  })

  // Auditoria C3: num canal pausado, o que importa é que os avisos são
  // descartados. "Pausado" é o selo; a verificação vira texto apagado.
  it('canal pausado mostra "Pausado" em destaque e a verificação rebaixada', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ enabled: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Pausado')).toHaveClass('rounded-full', 'bg-degradado-100')
    expect(screen.getByText('Os avisos são descartados.')).toBeInTheDocument()
    const verificacao = screen.getByText('Verificado')
    expect(verificacao).not.toHaveClass('rounded-full')
    expect(verificacao).toHaveClass('text-suave')
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/canais/PaginaCanais.tsx'` à `ARQUIVOS_NO_SISTEMA`.

Em `web/e2e/a11y.spec.ts`, no teste `acessibilidade e CSP em todas as rotas`, troque:

```ts
    await expect(page.getByText('⏸ Pausado: os avisos são descartados')).toBeVisible()
```

por:

```ts
    await expect(page.getByText('Os avisos são descartados.')).toBeVisible()
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/canais src/sistema.test.ts`
Expected: FAIL. Os textos sem glifo não existem ("Unable to find an element with the text: Verificado"); `PaginaCanais.tsx` reprova no sistema (`rounded-xl`, `font-medium`).

- [ ] **Step 3: Reescrever os estados, o cartão e o tipo**

Em `web/src/features/canais/PaginaCanais.tsx`, acrescente aos imports:

```tsx
import { CircleCheck, CircleX, Clock, Pause, TimerOff, type LucideIcon } from 'lucide-react'
import { Selo, type TomDoSelo } from '@/components/SeloDeEstado'
import { estiloDeCartao } from '@/components/ui/cartao'
import { cn } from '@/lib/utils'
```

troque o import do campo por:

```tsx
import { Campo, Entrada, ErroDoFormulario, Opcao } from '@/components/ui/campo'
```

e substitua o mapa `estados` (o de glifos) por:

```tsx
// Estado da verificação com forma, cor e texto (auditoria C2, S9).
const estados: Record<Canal['status'], { texto: string; Icone: LucideIcon; tom: TomDoSelo }> = {
  verified: { texto: 'Verificado', Icone: CircleCheck, tom: 'no-ar' },
  pending: { texto: 'Aguardando verificação', Icone: Clock, tom: 'degradado' },
  expired: { texto: 'Verificação expirada', Icone: TimerOff, tom: 'sem-dados' },
  failing: { texto: 'Falhando', Icone: CircleX, tom: 'fora' },
}

// Canal pausado (auditoria C3): o que importa é que os avisos são
// descartados, então "Pausado" é o selo, em âmbar (o alvo pausado do painel
// é cinza: pausar um alvo não perde nada). A verificação continua, rebaixada
// a texto apagado.
function EstadoDoCanal({ canal }: { canal: Canal }) {
  const { texto, Icone, tom } = estados[canal.status]
  if (canal.enabled) {
    return (
      <Selo tom={tom} Icone={Icone}>
        {texto}
      </Selo>
    )
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Selo tom="degradado" Icone={Pause}>
        Pausado
      </Selo>
      <span className="text-sm">Os avisos são descartados.</span>
      <span className="inline-flex items-center gap-1 text-meta text-suave">
        <Icone aria-hidden="true" className="size-3.5 shrink-0" />
        {texto}
      </span>
    </div>
  )
}
```

No `NovoCanal`, troque o `<fieldset>` do tipo inteiro por:

```tsx
      <fieldset>
        <legend className="mb-2 text-sm font-semibold">Tipo</legend>
        <div className="flex flex-wrap gap-x-6">
          <Opcao value="discord" {...f.register('kind')}>
            Discord
          </Opcao>
          <Opcao value="email" {...f.register('kind')}>
            E-mail
          </Opcao>
        </div>
      </fieldset>
```

Na lista, troque a abertura do `<li>`:

```tsx
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-borda bg-superficie p-4">
                  <div className="min-w-0 space-y-1 break-words">
```

por:

```tsx
                <li key={c.id} className={cn(estiloDeCartao, 'flex flex-wrap items-center justify-between gap-4')}>
                  <div className="min-w-0 space-y-2 break-words">
```

e troque as duas linhas de estado:

```tsx
                    <p className={`text-sm font-medium ${estados[c.status].cor}`}>{estados[c.status].texto}</p>
                    {!c.enabled && <p className="text-sm font-medium">⏸ Pausado: os avisos são descartados</p>}
```

por:

```tsx
                    <EstadoDoCanal canal={c} />
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/canais src/sistema.test.ts`
Expected: PASS.

Sabotagens: em `EstadoDoCanal`, devolva o `Selo` da verificação também para o canal pausado (tire o `if (canal.enabled)`) → o teste do pausado falha ("Verificado" com `rounded-full`); troque o ícone do `pending` por `CircleCheck` e o `texto` por `'… Aguardando verificação'` → o teste dos selos falha. Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/canais web/src/sistema.test.ts web/e2e/a11y.spec.ts
git commit -m "feat(painel): canais com selos Lucide e \"Pausado\" em destaque"
```

### Task 6.3: A frase da faixa, com os casos compartilhados com o Go

**Files:**
- Create: `internal/httpapi/testdata/faixa.json`
- Create: `web/src/features/statuspage/faixa.ts`
- Create: `web/src/features/statuspage/faixa.test.ts`

**Interfaces:**
- Consumes: `EstadoAlvo` (`@/lib/api/schemas`, enum zod e tipo).
- Produces:

```ts
// web/src/features/statuspage/faixa.ts
export type ItemDaFaixa = { readonly nome: string; readonly estado: EstadoAlvo }
export type Faixa = { readonly estado: EstadoAlvo; readonly frase: string; readonly detalhe: string }
export function faixaDaPagina(itens: readonly ItemDaFaixa[]): Faixa
```

```json
// internal/httpapi/testdata/faixa.json: [{ "caso", "itens": [{ "nome", "estado" }], "estado", "frase", "detalhe" }]
// O Go (Task 7.2) lê o mesmo arquivo.
```

- [ ] **Step 1: Escrever os casos e o teste que falha**

Crie `internal/httpapi/testdata/faixa.json`:

```json
[
  { "caso": "página vazia", "itens": [], "estado": "unknown", "frase": "Nenhum serviço nesta página.", "detalhe": "" },
  {
    "caso": "todos no ar",
    "itens": [{ "nome": "API", "estado": "up" }, { "nome": "Site", "estado": "up" }],
    "estado": "up",
    "frase": "Todos os sistemas funcionando.",
    "detalhe": ""
  },
  {
    "caso": "um lento",
    "itens": [{ "nome": "API", "estado": "degraded" }, { "nome": "Site", "estado": "up" }],
    "estado": "degraded",
    "frase": "Instabilidade em 1 serviço.",
    "detalhe": "Lento: API."
  },
  {
    "caso": "dois lentos",
    "itens": [{ "nome": "API", "estado": "degraded" }, { "nome": "Banco", "estado": "degraded" }],
    "estado": "degraded",
    "frase": "Instabilidade em 2 serviços.",
    "detalhe": "Lento: API, Banco."
  },
  {
    "caso": "o pior estado dá a frase, e o detalhe vai do pior ao melhor",
    "itens": [
      { "nome": "Fila", "estado": "unknown" },
      { "nome": "Banco", "estado": "degraded" },
      { "nome": "API", "estado": "down" },
      { "nome": "Site", "estado": "up" }
    ],
    "estado": "down",
    "frase": "1 serviço fora do ar.",
    "detalhe": "Fora do ar: API. Lento: Banco. Sem dados: Fila."
  },
  {
    "caso": "dois fora do ar, na ordem da página",
    "itens": [{ "nome": "Site", "estado": "down" }, { "nome": "API", "estado": "down" }],
    "estado": "down",
    "frase": "2 serviços fora do ar.",
    "detalhe": "Fora do ar: Site, API."
  },
  {
    "caso": "sem dados ao lado de um no ar",
    "itens": [{ "nome": "Fila", "estado": "unknown" }, { "nome": "Site", "estado": "up" }],
    "estado": "unknown",
    "frase": "Sem dados recentes de 1 serviço.",
    "detalhe": "Sem dados: Fila."
  },
  {
    "caso": "todos sem dados",
    "itens": [{ "nome": "Fila", "estado": "unknown" }, { "nome": "Novo", "estado": "unknown" }],
    "estado": "unknown",
    "frase": "Sem dados recentes de 2 serviços.",
    "detalhe": "Sem dados: Fila, Novo."
  }
]
```

Crie `web/src/features/statuspage/faixa.test.ts`:

```ts
/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { EstadoAlvo } from '@/lib/api/schemas'
import { faixaDaPagina } from './faixa'

// A frase da faixa é a da página pública (internal/httpapi/publicpage.go). Os
// casos ficam num JSON só, que este teste e o do Go leem: a prévia do painel
// e a página publicada não podem dizer coisas diferentes.
const Caso = z.object({
  caso: z.string(),
  itens: z.array(z.object({ nome: z.string(), estado: EstadoAlvo })),
  estado: EstadoAlvo,
  frase: z.string(),
  detalhe: z.string(),
})
const CASOS = z.array(Caso).parse(JSON.parse(readFileSync('../internal/httpapi/testdata/faixa.json', 'utf8')))

describe('faixaDaPagina segue internal/httpapi/testdata/faixa.json', () => {
  it('o arquivo tem os oito casos', () => {
    expect(CASOS).toHaveLength(8)
  })

  it.each(CASOS.map((c) => [c.caso, c] as const))('%s', (_caso, c) => {
    expect(faixaDaPagina(c.itens)).toEqual({ estado: c.estado, frase: c.frase, detalhe: c.detalhe })
  })
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/statuspage/faixa.test.ts`
Expected: FAIL com `Failed to resolve import "./faixa"`.

- [ ] **Step 3: Implementar**

Crie `web/src/features/statuspage/faixa.ts`:

```ts
import type { EstadoAlvo } from '@/lib/api/schemas'

// A faixa de estado da página pública (spec do redesign, Telas 6), para a
// prévia do painel. A página publicada diz a mesma frase
// (internal/httpapi/publicpage.go); os dois testes leem os casos de
// internal/httpapi/testdata/faixa.json.
export type ItemDaFaixa = { readonly nome: string; readonly estado: EstadoAlvo }
export type Faixa = { readonly estado: EstadoAlvo; readonly frase: string; readonly detalhe: string }

// Os rótulos que a página pública já usa para o estado de cada serviço.
const ROTULO_PUBLICO: Record<EstadoAlvo, string> = {
  up: 'No ar',
  degraded: 'Lento',
  down: 'Fora do ar',
  unknown: 'Sem dados',
}

// Do pior ao melhor: o primeiro presente dá a frase.
const GRAVIDADE = ['down', 'degraded', 'unknown'] as const

const FRASES: Record<(typeof GRAVIDADE)[number], (servicos: string) => string> = {
  down: (servicos) => `${servicos} fora do ar.`,
  degraded: (servicos) => `Instabilidade em ${servicos}.`,
  unknown: (servicos) => `Sem dados recentes de ${servicos}.`,
}

function servicos(n: number): string {
  return n === 1 ? '1 serviço' : `${n} serviços`
}

export function faixaDaPagina(itens: readonly ItemDaFaixa[]): Faixa {
  if (itens.length === 0) return { estado: 'unknown', frase: 'Nenhum serviço nesta página.', detalhe: '' }
  const grupos = GRAVIDADE.map((estado) => ({
    estado,
    nomes: itens.filter((i) => i.estado === estado).map((i) => i.nome),
  })).filter((g) => g.nomes.length > 0)
  const pior = grupos[0]
  if (!pior) return { estado: 'up', frase: 'Todos os sistemas funcionando.', detalhe: '' }
  return {
    estado: pior.estado,
    frase: FRASES[pior.estado](servicos(pior.nomes.length)),
    detalhe: grupos.map((g) => `${ROTULO_PUBLICO[g.estado]}: ${g.nomes.join(', ')}.`).join(' '),
  }
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/statuspage/faixa.test.ts`
Expected: PASS (9 testes).

Sabotagens: troque a ordem de `GRAVIDADE` para `['degraded', 'down', 'unknown']` → falha "o pior estado dá a frase…"; em `servicos`, troque `'1 serviço'` por `'um serviço'` → falham os casos com um serviço. Desfaça.

- [ ] **Step 5: Tipos e lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/testdata/faixa.json web/src/features/statuspage/faixa.ts web/src/features/statuspage/faixa.test.ts
git commit -m "feat(painel): frase da faixa da página pública, com casos compartilhados com o Go"
```

### Task 6.4: Status page de configuração: prévia que parece a pública, Tirar terciário

**Files:**
- Create: `web/src/features/statuspage/PreviaDaPagina.tsx`
- Modify: `web/src/features/statuspage/PaginaStatusPage.tsx`
- Modify: `web/src/features/statuspage/statuspage.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `faixaDaPagina` (Task 6.3); `estiloDeCartao` (Task 6.1); `SeloDeEstado`, `estadoExibido`; `Botao` (`tipo="secundario" tamanho="icone"`, `tipo="terciario" tamanho="pequeno"`); `ArrowUp`, `ArrowDown`.
- Produces:

```ts
// web/src/features/statuspage/PreviaDaPagina.tsx
export type ItemDaPrevia = { readonly id: string; readonly nome: string; readonly estado: EstadoAlvo | null; readonly ativo: boolean }
// estado null: o alvo não está no resumo (apagado ou ainda carregando).
export function PreviaDaPagina(props: { titulo: string; itens: readonly ItemDaPrevia[] })
// <section aria-labelledby> com o nome acessível "Prévia aproximada".
```

- [ ] **Step 1: Escrever os testes que falham**

Em `web/src/features/statuspage/statuspage.test.tsx`, o import das fixtures vira `import { alvo, itemDoResumo, pagina } from '@/test/fixtures'` e, dentro do `describe` principal, acrescente:

```tsx
  // Auditoria SP3: a prévia parece a página pública: o título, a mesma faixa
  // de estado (faixa.ts) e um cartão por alvo. Sabotagem: tirar a faixa da
  // PreviaDaPagina → o teste não acha a frase.
  it('a prévia parece a página pública: título, faixa de estado e um cartão por alvo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down' })]
    r.falsa.estado.paginas = [pagina({ items: [{ target_id: 'alvo-1', display_name: 'API pública' }] })]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getByText('Minha')).toHaveClass('font-display')
    expect(await within(previa).findByText('1 serviço fora do ar.')).toBeInTheDocument()
    expect(within(previa).getByText('Fora do ar: API pública.')).toBeInTheDocument()
    expect(within(previa).getByText('Fora do ar')).toHaveClass('rounded-full')
    await r.usuario.clear(screen.getByLabelText('Título'))
    await r.usuario.type(screen.getByLabelText('Título'), 'Loja')
    expect(within(previa).getByText('Loja')).toBeInTheDocument()
  })

  // A página pública não sabe de pausa: o alvo pausado sai "sem dados" na
  // faixa, como sairá na página publicada.
  it('na faixa da prévia, o alvo pausado conta como sem dados', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'up', enabled: false })]
    r.falsa.estado.paginas = [pagina({ items: [{ target_id: 'alvo-1', display_name: 'API pública' }] })]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(await within(previa).findByText('Sem dados recentes de 1 serviço.')).toBeInTheDocument()
  })

  // Auditoria SP2: reordenar é secundário, em ícone; Tirar, que remove, é
  // terciário. Os nomes acessíveis de antes ficam.
  it('Subir e Descer são botões de ícone secundários, e Tirar é terciário', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Banco' })]
    r.falsa.estado.paginas = [
      pagina({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Banco' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    for (const nome of ['Subir Banco', 'Descer API']) {
      const botao = await screen.findByRole('button', { name: nome })
      expect(botao).toHaveClass('border-borda-controle', 'size-11')
      expect(botao.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    const tirar = screen.getByRole('button', { name: 'Tirar API da página' })
    expect(tirar).toHaveClass('text-primaria')
    expect(tirar).not.toHaveClass('border-borda-controle')
  })

  it('o link público é um cartão de largura limitada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [pagina()]
    await r.cliente.refetchQueries()
    const rotulo = await screen.findByText('Link público')
    expect(rotulo.parentElement).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie')
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/statuspage/PaginaStatusPage.tsx'` e `'src/features/statuspage/PreviaDaPagina.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/statuspage src/sistema.test.ts`
Expected: FAIL. A prévia não tem o título nem a frase; Subir é `text-primaria` (terciário, da parte 1) e não `size-11`; o link público tem `rounded-lg`; o sistema reprova `PaginaStatusPage.tsx` (`rounded-md`, `rounded-lg`, `font-medium`) e não acha `PreviaDaPagina.tsx` (`ENOENT`).

- [ ] **Step 3: Criar a `PreviaDaPagina`**

Crie `web/src/features/statuspage/PreviaDaPagina.tsx`:

```tsx
import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'
import type { EstadoAlvo } from '@/lib/api/schemas'
import { cn } from '@/lib/utils'
import { faixaDaPagina } from './faixa'

// estado null: o alvo não está no resumo (apagado, ou o resumo ainda não chegou).
export type ItemDaPrevia = { readonly id: string; readonly nome: string; readonly estado: EstadoAlvo | null; readonly ativo: boolean }

// A faixa com o fundo 100 do estado, o texto na cor dele (pares do
// tokens.test.ts) e a borda de acento, como na página pública.
const TONS_DA_FAIXA: Record<EstadoAlvo, string> = {
  up: 'border-l-no-ar bg-no-ar-100 text-no-ar',
  degraded: 'border-l-degradado bg-degradado-100 text-degradado',
  down: 'border-l-fora bg-fora-100 text-fora',
  unknown: 'border-l-sem-dados bg-sem-dados-100 text-sem-dados',
}

// Prévia da página pública (auditoria SP3; spec do Plano 6, D23): o papel, o
// título em Fraunces, a mesma faixa de estado e um cartão por alvo. A página
// real é HTML do servidor, só acessível depois de publicada, e o painel não
// tem os 90 dias de cada alvo: a barra vira um contorno, e a nota diz o que
// falta.
export function PreviaDaPagina({ titulo, itens }: { titulo: string; itens: readonly ItemDaPrevia[] }) {
  // A página pública não sabe de pausa: alvo pausado ou fora do resumo sai
  // "sem dados" (targetstate.Current), e a faixa segue a mesma regra.
  const faixa = faixaDaPagina(
    itens.map((it) => ({ nome: it.nome || '—', estado: it.ativo && it.estado ? it.estado : 'unknown' })),
  )
  return (
    <section aria-labelledby="titulo-previa" className="space-y-4 rounded-padrao border border-borda bg-fundo p-4 sm:p-6">
      <h2 id="titulo-previa" className="rotulo-de-grupo font-sans text-apagado">
        Prévia aproximada
      </h2>
      <p className="font-display text-2xl font-semibold">{titulo.trim() || 'Título da página'}</p>
      <div className={cn('space-y-1 rounded-padrao border-l-4 p-4', TONS_DA_FAIXA[faixa.estado])}>
        <p className="font-display text-xl font-semibold">{faixa.frase}</p>
        {faixa.detalhe && <p className="text-sm">{faixa.detalhe}</p>}
      </div>
      <ul className="space-y-3">
        {itens.map((it) => (
          <li key={it.id} className="space-y-3 rounded-padrao border border-borda bg-superficie p-4">
            <div className="flex items-start justify-between gap-4">
              <span className="min-w-0 font-semibold">{it.nome || '—'}</span>
              {it.estado ? (
                <SeloDeEstado estado={estadoExibido(it.estado, it.ativo)} />
              ) : (
                <span className="text-suave">—</span>
              )}
            </div>
            <div aria-hidden="true" className="h-8 rounded-padrao border border-dashed border-borda-controle" />
          </li>
        ))}
      </ul>
      <p className="text-sm text-suave">As barras dos últimos 90 dias e o histórico de incidentes aparecem na página publicada.</p>
    </section>
  )
}
```

- [ ] **Step 4: Usar a prévia e redesenhar a tela**

Em `web/src/features/statuspage/PaginaStatusPage.tsx`:

1. Imports: tire `import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'` e acrescente:

```tsx
import { ArrowDown, ArrowUp } from 'lucide-react'
import { PreviaDaPagina } from './PreviaDaPagina'
```

2. No `Formulario`, logo depois de `const escolhidos = useWatch({ control: f.control, name: 'items' })`:

```tsx
  const tituloAtual = useWatch({ control: f.control, name: 'title' })
```

3. A `<legend>` da lista:

```tsx
        <legend className="text-sm font-medium">Alvos na página ({itens.fields.length} de {MAX_ITENS})</legend>
```

vira:

```tsx
        <legend className="text-sm font-semibold">Alvos na página ({itens.fields.length} de {MAX_ITENS})</legend>
```

4. A linha de cada item: troque `rounded-md border border-borda p-3` por `rounded-padrao border border-borda p-3` no `className` do `<div key={it.id} …>`.

5. Troque o bloco dos três botões (do `<div className="flex flex-wrap gap-1">` até o `</div>` que fecha depois do "Tirar") por:

```tsx
            {/* Ordem por botões, não arrastar: teclado e leitor de tela.
                Reordenar é secundário; Tirar, que remove, é terciário
                (auditoria SP2). aria-disabled, e não disabled, nas pontas:
                o botão que leva o item à ponta está com o foco, e
                desabilitado o perderia para o <body> (como o Reconhecer).
                A guarda fica no clique. */}
            <div className="flex flex-wrap items-center gap-2">
              <Botao
                tipo="secundario"
                tamanho="icone"
                title="Subir"
                aria-disabled={i === 0 || undefined}
                onClick={() => {
                  if (i > 0) itens.move(i, i - 1)
                }}
                aria-label={`Subir ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}
              >
                <ArrowUp aria-hidden="true" className="size-5" />
              </Botao>
              <Botao
                tipo="secundario"
                tamanho="icone"
                title="Descer"
                aria-disabled={i === itens.fields.length - 1 || undefined}
                onClick={() => {
                  if (i < itens.fields.length - 1) itens.move(i, i + 1)
                }}
                aria-label={`Descer ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}
              >
                <ArrowDown aria-hidden="true" className="size-5" />
              </Botao>
              <Botao
                tipo="terciario"
                tamanho="pequeno"
                data-tirar=""
                onClick={() => {
                  focarDepoisDeTirar.current = i
                  itens.remove(i)
                }}
                aria-label={`Tirar ${nomeDoAlvo.get(it.target_id) ?? 'item'} da página`}
              >
                Tirar
              </Botao>
            </div>
```

O comentário antigo, `{/* Ordem por botões, não arrastar: teclado e leitor de tela. */}`, e o de dentro do `<div>` saem: o novo os junta.

6. Troque o bloco da prévia inteiro (do comentário `{/* Prévia aproximada (spec do Plano 6, D23)…` até o `)}` que fecha o `<section>`) por:

```tsx
      {escolhidos.length > 0 && (
        <PreviaDaPagina
          titulo={tituloAtual}
          itens={escolhidos.map((it) => {
            const r = resumo.data?.items.find((x) => x.id === it.target_id)
            return { id: it.target_id, nome: it.display_name, estado: r?.state ?? null, ativo: r?.enabled ?? true }
          })}
        />
      )}
```

7. No `PaginaStatusPage`, o cartão do link público:

```tsx
                <div className="space-y-2 rounded-lg border border-borda p-4">
                  <p className="text-sm font-medium">Link público</p>
```

vira:

```tsx
                <div className={cn(estiloDeCartao, 'max-w-2xl space-y-2')}>
                  <p className="text-sm font-semibold">Link público</p>
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/statuspage src/sistema.test.ts`
Expected: PASS, inclusive os testes que já existiam ("a prévia é rotulada como aproximada", "prévia usa resumo real e mostra o estado pausado", "alvo que desaparece…" com dois "—", "tirar o último alvo… a prévia some", o foco do "Tirar" e o `aria-disabled` do "Descer").

Sabotagens: na `PreviaDaPagina`, troque `it.ativo && it.estado ? it.estado : 'unknown'` por `it.estado ?? 'unknown'` → falha "o alvo pausado conta como sem dados"; volte o Subir a `tipo="terciario"` → falha o teste do SP2. Desfaça.

- [ ] **Step 6: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/statuspage web/src/sistema.test.ts
git commit -m "feat(painel): status page com prévia que parece a pública e Tirar terciário"
```

### Task 6.5: Formulário de alvo com opções de 44 px

**Files:**
- Modify: `web/src/features/alvos/FormularioAlvo.tsx`
- Modify: `web/src/features/alvos/alvos.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `Opcao` (Task 6.1); `CaixaDeMarcar` (já usada desde a parte 1); `Botao` `terciario` no "Remover" (parte 1).
- Produces: nada para fora.

- [ ] **Step 1: Escrever os testes que falham**

No `describe('formulário de alvo no sistema visual', …)` de `web/src/features/alvos/alvos.test.tsx` (Task 6.1), acrescente:

```tsx
  // DI 11: cada opção de rádio é uma linha de 44 px, com o rótulo dentro.
  it('as opções do limiar de lentidão são linhas de 44 px', async () => {
    await renderizarPainel('/targets/new')
    for (const rotulo of ['Padrão (2000 ms)', 'Personalizado', 'Desativado']) {
      const opcao = await screen.findByRole('radio', { name: rotulo })
      expect(opcao.closest('label')).toHaveClass('min-h-11')
    }
  })
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/alvos/FormularioAlvo.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/alvos/alvos.test.tsx src/sistema.test.ts`
Expected: FAIL. A linha do rádio tem `flex items-center gap-2 text-sm`, sem `min-h-11`; o sistema reprova `FormularioAlvo.tsx` (`font-medium` duas vezes, `rounded-lg`).

- [ ] **Step 3: Trocar rádios, legendas e linhas de header**

Em `web/src/features/alvos/FormularioAlvo.tsx`:

1. O import do campo vira:

```tsx
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Opcao, Selecao } from '@/components/ui/campo'
```

(Se a parte 1 deixou o import em outra ordem, mantenha os nomes que já estão e acrescente `Opcao`.)

2. As duas `<legend className="text-sm font-medium">` viram `<legend className="text-sm font-semibold">`.

3. Os rádios do limiar:

```tsx
        ).map(([v, t]) => (
          <label key={v} className="flex items-center gap-2 text-sm">
            <input type="radio" value={v} {...f.register('lentidao')} />
            {t}
          </label>
        ))}
```

viram:

```tsx
        ).map(([v, t]) => (
          <Opcao key={v} value={v} {...f.register('lentidao')}>
            {t}
          </Opcao>
        ))}
```

4. Os rádios do modo dos headers:

```tsx
            <div className="flex flex-wrap gap-4">
              {(
```

…

```tsx
              ).map(([v, t]) => (
                <label key={v} className="flex items-center gap-2 text-sm">
                  <input type="radio" value={v} {...f.register('modoHeaders')} />
                  {t}
                </label>
              ))}
            </div>
```

viram:

```tsx
            <div className="flex flex-wrap gap-x-6">
              {(
```

…

```tsx
              ).map(([v, t]) => (
                <Opcao key={v} value={v} {...f.register('modoHeaders')}>
                  {t}
                </Opcao>
              ))}
            </div>
```

5. A linha de cada header: troque `rounded-lg border border-borda p-3` por `rounded-padrao border border-borda p-3`.

Confira: `cd web && grep -rn 'type="radio"' src --include='*.tsx' | grep -v '\.test\.'`
Expected: só `src/components/ui/campo.tsx` (a `Opcao`). Os rádios do canal saíram na Task 6.2.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/alvos src/sistema.test.ts`
Expected: PASS. Os testes que já existiam acham os rádios pelo rótulo (`getByLabelText('Remover todos')`), que não mudou.

Sabotagem: volte um dos três rádios do limiar ao `<label className="flex items-center gap-2 text-sm">` → o teste de 44 px falha nele. Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/alvos web/src/sistema.test.ts
git commit -m "feat(painel): formulário de alvo em cartão, com opções de 44 px"
```

### Task 6.6: Configurações em cartões de largura limitada

**Files:**
- Modify: `web/src/features/configuracoes/PaginaConfiguracoes.tsx`
- Modify: `web/src/features/configuracoes/configuracoes.test.tsx`
- Modify: `web/src/index.css` (sai `main > section:not(…)`)
- Modify: `web/src/sistema.test.ts` (lista e CSS legado)

**Interfaces:**
- Consumes: `estiloDeCartao`; `Selo` com `Check`; `QRCodeSVG` com `marginSize`.
- Produces: nada para fora. As seções continuam `role="region"` com os nomes "Conta", "Senha", "Verificação em duas etapas" e "Sessões ativas".

- [ ] **Step 1: Escrever os testes que falham**

No fim de `web/src/features/configuracoes/configuracoes.test.tsx`:

```tsx
// Auditoria G3: o cartão tem a largura do conteúdo (max-w-2xl), e o
// formulário de senha ocupa o cartão, sem o max-w-md que deixava metade dele
// vazia. Antes, o cartão vinha do "main > section:not(…)" do index.css.
describe('configurações no sistema visual', () => {
  it('cada seção é um cartão de largura limitada, e a senha não fica espremida nele', async () => {
    await renderizarPainel('/settings')
    for (const nome of ['Conta', 'Senha', 'Verificação em duas etapas', 'Sessões ativas']) {
      expect(await screen.findByRole('region', { name: nome })).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie')
    }
    expect(screen.getByRole('region', { name: 'Senha' }).querySelector('form')).not.toHaveClass('max-w-md')
  })

  it('"Esta sessão" é um selo', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia()]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Esta sessão')).toHaveClass('rounded-full', 'bg-primaria-100')
  })
})
```

Em `web/src/sistema.test.ts`, acrescente `'src/features/configuracoes/PaginaConfiguracoes.tsx'` à `ARQUIVOS_NO_SISTEMA` e `'main > section:not('` à lista do `describe('CSS legado removido pelo PR 6')`:

```ts
  it.each(['#conteudo > form', 'main > section:not('])('o index.css não tem mais %s', (seletor) => {
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/configuracoes src/sistema.test.ts`
Expected: FAIL. As seções têm só `space-y-*`; o formulário de senha tem `max-w-md`; "Esta sessão" é `rounded bg-painel`; o sistema reprova o arquivo (`font-medium`, `rounded-lg`, `rounded`, `py-0.5`, `bg-white`) e acha `main > section:not(` no CSS.

- [ ] **Step 3: Conferir quem ainda dependia do `main > section:not(…)`**

Run: `cd web && grep -rn "<section" src/features src/app --include='*.tsx' | grep -v '\.test\.'`
Expected: a lista das `<section>` do painel. Para cada uma que é filha direta do `<main>` (a tela a renderiza no topo do fragmento, fora de outro elemento) e cujo `className` não dá superfície própria (só `space-y-*`, sem `bg-` nem `estiloDeCartao`), acrescente `estiloDeCartao` com `cn(estiloDeCartao, '<classes que ela já tinha>')`. As de Configurações são feitas no Step 4. As do painel e do detalhe são da parte 2, que já as redesenhou; em geral não haverá outra. Anote na descrição do PR as que precisaram do ajuste.

- [ ] **Step 4: Redesenhar as seções, as sessões e o QR**

Em `web/src/features/configuracoes/PaginaConfiguracoes.tsx`:

1. Imports, acrescente:

```tsx
import { Check } from 'lucide-react'
import { Selo } from '@/components/SeloDeEstado'
import { estiloDeCartao } from '@/components/ui/cartao'
import { cn } from '@/lib/utils'
```

2. `TrocaDeSenha`: o `<form>` com `className="max-w-md space-y-4"` vira `className="space-y-4"`.

3. `PassoDoQr`: troque

```tsx
      <div className="inline-block bg-white p-3">
        <QRCodeSVG value={uri} size={176} title="QR code do autenticador" />
      </div>
```

por:

```tsx
      {/* A zona de silêncio branca é do próprio SVG (marginSize, em
          módulos): o leitor de QR precisa dela também no tema escuro. */}
      <QRCodeSVG value={uri} size={176} marginSize={4} title="QR code do autenticador" className="rounded-padrao" />
```

4. `Sessoes`, a linha de cada sessão:

```tsx
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-3"
              >
                <div className="text-sm">
                  <p className="font-medium">
                    {navegadorDe(s.user_agent)}
                    {s.current && <span className="ml-2 rounded bg-painel px-2 py-0.5 text-xs">Esta sessão</span>}
                  </p>
```

vira:

```tsx
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-padrao border border-borda p-4"
              >
                <div className="text-sm">
                  <p className="font-semibold">
                    {navegadorDe(s.user_agent)}
                    {s.current && (
                      <Selo tom="primaria" Icone={Check} className="ml-2">
                        Esta sessão
                      </Selo>
                    )}
                  </p>
```

5. As quatro seções de `PaginaConfiguracoes`. Cada `<section aria-labelledby="t-…" className="space-y-N">` ganha o cartão, e cada `<h2 id="t-…" className="text-lg font-semibold">` vira `<h2 id="t-…" className="text-xl">` (Fraunces 600 vem da camada base, parte 1):

```tsx
            <section aria-labelledby="t-conta" className={cn(estiloDeCartao, 'max-w-2xl space-y-1')}>
              <h2 id="t-conta" className="text-xl">
                Conta
              </h2>
```

```tsx
            <section aria-labelledby="t-senha" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
              <h2 id="t-senha" className="text-xl">
                Senha
              </h2>
```

```tsx
            <section aria-labelledby="t-2fa" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
              <h2 id="t-2fa" className="text-xl">
                Verificação em duas etapas
              </h2>
```

```tsx
      <section aria-labelledby="t-sessoes" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
        <h2 id="t-sessoes" className="text-xl">
          Sessões ativas
        </h2>
```

O resto das seções (o conteúdo, os botões do 2FA, os diálogos) fica.

6. Em `web/src/index.css`, apague o bloco:

```css
main > section:not([aria-label='Resumo dos alvos']):not([aria-label='Avisos de notificação']) {
  border: 1px solid var(--borda);
  border-radius: 0.5rem;
  background: var(--superficie);
  padding: 1.25rem;
}
```

(Se a parte 2 já mudou os `:not(…)` dele, apague a regra `main > section:not(` inteira do mesmo jeito.)

- [ ] **Step 5: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/configuracoes src/sistema.test.ts src/main.test.tsx`
Expected: PASS. Os testes que já existiam procuram "Ativada.", "Esta sessão", os botões e os diálogos por papel e texto, que não mudaram.

Sabotagem: devolva o `max-w-md` ao formulário de senha → falha "a senha não fica espremida". Desfaça.

- [ ] **Step 6: Suíte, tipos, lint e o e2e da troca de autenticador**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.
Run: `cd web && npm run build && npx playwright test e2e/a11y.spec.ts -g "troca de autenticador"`
Expected: PASS nos dois temas (o QR do passo 2 passa pelo axe, agora sem o `div` branco).

- [ ] **Step 7: Commit**

```bash
git add web/src/features/configuracoes web/src/index.css web/src/sistema.test.ts
git commit -m "feat(painel): configurações em cartões de largura limitada"
```

Se o Step 3 mexeu em outra tela, inclua o arquivo no `git add`.

### Task 6.7: Login com a marca, a frase de tom e o cartão nível 2

**Files:**
- Modify: `web/src/features/login/PaginaLogin.tsx`
- Modify: `web/src/features/login/login.test.tsx`
- Modify: `web/src/sistema.test.ts` (lista)

**Interfaces:**
- Consumes: `Marca` com `tamanho="grande"` (parte 1, PR 2); `Titulo`; `sombra-nivel-2`; `font-display`.
- Produces: nada para fora. O `h1` continua "Entrar no StillOn" (os testes e o e2e o procuram).

- [ ] **Step 1: Escrever o teste que falha**

No fim de `web/src/features/login/login.test.tsx` (dentro de um `describe` novo, porque o arquivo agrupa por assunto):

```tsx
// Spec do redesign, Telas 5: a marca (ponto índigo + "StillOn" em Fraunces
// 28), a frase de tom em Fraunces e o cartão com sombra nível 2; campos e
// botão de 44 px. Sabotagem: tirar a <Marca> → o primeiro expect falha.
describe('a tela de entrada no sistema visual', () => {
  it('tem a marca, a frase de tom, o cartão nível 2 e campos e botão de 44 px', async () => {
    await renderizarPainel('/login', { logado: false })
    const titulo = await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    expect(screen.getByText('StillOn', { exact: true })).toHaveClass('font-display', 'text-3xl')
    expect(screen.getByText('Seus serviços, vigiados de perto.')).toHaveClass('font-display')
    expect(titulo.parentElement).toHaveClass('sombra-nivel-2', 'rounded-padrao')
    for (const campo of [screen.getByLabelText('E-mail'), screen.getByLabelText('Senha')]) {
      expect(campo).toHaveClass('min-h-11')
    }
    expect(screen.getByRole('button', { name: 'Entrar' })).toHaveClass('min-h-11')
    expect(screen.queryByText('Monitor de disponibilidade')).not.toBeInTheDocument()
  })
})
```

Acrescente `describe` ao import do `vitest` se ele ainda não estiver lá (já está: `import { afterEach, describe, expect, it } from 'vitest'`).

Em `web/src/sistema.test.ts`, acrescente `'src/features/login/PaginaLogin.tsx'` à `ARQUIVOS_NO_SISTEMA`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npx vitest run src/features/login src/sistema.test.ts`
Expected: FAIL. Não há a marca nem a frase; o pai do título é o `<main>` com `rounded-lg`; o sistema reprova `PaginaLogin.tsx` (`my-10`, `space-y-7`, `p-7`, `sm:my-24`, `sm:p-10`, `font-bold`, `rounded-lg`, `rounded-md`).

- [ ] **Step 3: Reescrever a moldura da tela**

Em `web/src/features/login/PaginaLogin.tsx`, acrescente o import:

```tsx
import { Marca } from '@/components/Marca'
```

e, logo depois de `const ID_MOTIVO = 'motivo-do-fim-da-sessao'`:

```tsx
// A frase de tom do login (spec do redesign, Telas 5). O dono troca aqui.
const FRASE_DO_LOGIN = 'Seus serviços, vigiados de perto.'
```

Troque o `return (…)` da função `PaginaLogin` inteiro por:

```tsx
  return (
    <>
      <AvisoDeContexto />
      <main className="mx-auto max-w-md space-y-8 px-4 py-12 sm:py-16">
        <div className="space-y-2">
          <Marca tamanho="grande" />
          <p className="font-display text-xl text-suave">{FRASE_DO_LOGIN}</p>
        </div>
        <div className="space-y-6 rounded-padrao border border-borda bg-superficie p-6 sombra-nivel-2 sm:p-8">
          {/* O foco vai ao <h1> depois de a sessão acabar (é uma navegação), e
              o leitor de tela lê o motivo junto, pela descrição. */}
          <Titulo aba="Entrar" descritoPor={textoDoMotivo ? ID_MOTIVO : undefined}>
            Entrar no StillOn
          </Titulo>
          {textoDoMotivo && (
            <p id={ID_MOTIVO} className="rounded-padrao bg-painel p-3 text-sm">
              {textoDoMotivo}
            </p>
          )}
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          {desafio === null ? (
            // key nos dois formulários: trocar de passo monta campos novos, em vez
            // de o React reaproveitar o <input> do e-mail para o código.
            <form key="senha" noValidate className="space-y-4" onSubmit={senha.handleSubmit(entrar)}>
              {/* type="text" com inputMode="email": o <input type="email"> do
                  Chromium entrega o domínio internacional em punycode no value,
                  e o texto que chegaria ao servidor não seria o cadastrado. */}
              <Campo rotulo="E-mail" erro={senha.formState.errors.email?.message}>
                {(a) => (
                  <Entrada
                    {...a}
                    type="text"
                    inputMode="email"
                    autoCapitalize="none"
                    spellCheck={false}
                    autoComplete="username"
                    {...senha.register('email')}
                  />
                )}
              </Campo>
              <Campo rotulo="Senha" erro={senha.formState.errors.senha?.message}>
                {(a) => (
                  <Entrada
                    {...a}
                    type="password"
                    autoComplete="current-password"
                    autoFocus={voltou}
                    {...senha.register('senha')}
                  />
                )}
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
              <Botao tipo="terciario" className="w-full" onClick={() => setRecuperacao((r) => !r)}>
                {recuperacao ? 'Usar o código do aplicativo' : 'Usar um código de recuperação'}
              </Botao>
              {/* A mensagem de invalid_credentials manda voltar; o Voltar do
                  navegador sairia do /login, porque o passo é estado da tela. */}
              <Botao tipo="terciario" className="w-full" onClick={() => voltarASenha(null)}>
                Voltar
              </Botao>
            </form>
          )}
        </div>
      </main>
    </>
  )
```

O sobretítulo "Monitor de disponibilidade" sai (auditoria G1): a marca e a frase fazem o papel.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/features/login src/sistema.test.ts`
Expected: PASS, inclusive os testes que já existiam (o foco no `h1` com o motivo, o "Pular para o conteúdo", o 2FA, o código de recuperação).

Sabotagem: troque `sombra-nivel-2` por `sombra-nivel-1` no cartão → o teste falha. Desfaça.

- [ ] **Step 5: Suíte, tipos e lint**

Run: `cd web && npm test && npm run typecheck && npm run lint`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/login web/src/sistema.test.ts
git commit -m "feat(painel): login com a marca, a frase de tom e cartão de nível 2"
```

### Task 6.8: Formulários com erro no celular, foco fora da barra, verificação e PR

**Files:**
- Modify: `web/src/index.css` (`scroll-padding-bottom` abaixo de `lg`)
- Modify: `web/e2e/a11y.spec.ts` (teste novo)

**Interfaces:**
- Consumes: o `Layout` da parte 1 (`nav[aria-label="Principal"]` fixa no pé abaixo de `lg`, 64rem); `imitarApi`, `esperarOConteudo`, `controlePositivo` do `a11y.spec.ts`.
- Produces: nada para os PRs seguintes.

- [ ] **Step 1: Escrever o e2e que falha**

No fim de `web/e2e/a11y.spec.ts`:

```ts
// Review Focus 6 da parte 3 do redesign: com erro de validação o formulário
// cresce (uma linha de erro por campo). No celular de 400 px ele continua sem
// overflow, o texto de cada erro aparece, e nem o campo que recebe o foco nem
// o botão de envio param embaixo da barra inferior fixa (WCAG 2.4.11).
// Sabotagens: tirar o scroll-padding-bottom do index.css → o botão de envio
// em foco fica sob a barra; pôr whitespace-nowrap no <p> de erro do Campo →
// overflow.
async function folgaAcimaDaBarra(page: Page): Promise<number | null> {
  return page.evaluate(() => {
    const foco = document.activeElement?.getBoundingClientRect()
    const barra = document.querySelector('nav[aria-label="Principal"]')?.getBoundingClientRect()
    return foco && barra ? barra.top - foco.bottom : null
  })
}

for (const tema of ['claro', 'escuro'] as const) {
  test(`celular de 400 px: formulários com erro de validação, tema ${tema}`, async ({ page }) => {
    await page.setViewportSize({ width: 400, height: 700 })
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    const casos: { rota: string; titulo: string; enviar: string; antes?: () => Promise<void> }[] = [
      { rota: '/targets/new', titulo: 'Novo alvo', enviar: 'Cadastrar' },
      {
        rota: '/status-pages',
        titulo: 'Status page',
        enviar: 'Salvar',
        antes: async () => {
          await page.getByLabel('Título', { exact: true }).fill('')
          await page.getByLabel('Nome de exibição de API').fill('')
        },
      },
      { rota: '/settings', titulo: 'Configurações', enviar: 'Trocar a senha' },
    ]
    for (const c of casos) {
      await page.goto(c.rota)
      await expect(page.getByRole('heading', { name: c.titulo, level: 1 })).toBeVisible()
      await esperarOConteudo(page, c.rota)
      await c.antes?.()
      const enviar = page.getByRole('button', { name: c.enviar, exact: true })
      await enviar.click()
      const invalidos = page.locator('#conteudo [aria-invalid="true"]')
      await expect(invalidos.first(), `${c.rota}: nenhum campo inválido`).toBeVisible()
      expect(await folgaAcimaDaBarra(page), `${c.rota}: o campo com erro, em foco, fica sob a barra`).toBeGreaterThanOrEqual(0)
      for (const campo of await invalidos.all()) {
        const ids = ((await campo.getAttribute('aria-describedby')) ?? '').split(' ')
        const erro = page.locator(`[id="${ids.at(-1) ?? ''}"]`)
        await erro.scrollIntoViewIfNeeded()
        await expect(erro, `${c.rota}: o texto do erro`).toBeVisible()
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${c.rota}: overflow`).toBe(400)
      await enviar.focus()
      expect(await folgaAcimaDaBarra(page), `${c.rota}: o botão de envio, em foco, fica sob a barra`).toBeGreaterThanOrEqual(0)
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${c.rota}: ${v.id}`)).toEqual([])
      await controlePositivo(page, `${c.rota} com erro, ${tema}`)
    }
    // O login não tem a barra: só o overflow e o erro.
    await page.route('**/api/v1/auth/refresh', (route) =>
      route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
    )
    await page.goto('/login')
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await page.getByRole('button', { name: 'Entrar', exact: true }).click()
    await expect(page.getByText('Informe o e-mail.')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth), 'login: overflow').toBe(400)
    await controlePositivo(page, `login com erro, ${tema}`)
  })
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd web && npm run build && npx playwright test e2e/a11y.spec.ts -g "formulários com erro"`
Expected: FAIL em "o botão de envio, em foco, fica sob a barra" de ao menos uma rota (o formulário de alvo, o mais longo): depois de o erro levar o foco ao primeiro campo, o botão fica abaixo da janela, e o `focus()` o rola só até o pé dela, embaixo da barra de 53 px. Se nenhuma rota falhar, baixe a altura da janela do teste de 700 para 600 e rode de novo: o teste só vale depois de falhar sem a correção.

- [ ] **Step 3: O foco não para embaixo da barra**

Em `web/src/index.css`, logo depois da regra `:focus-visible { … }`:

```css
/* Abaixo de lg, a navegação é uma barra fixa no pé da tela (app/Layout.tsx).
   O foco levado pelo teclado ou pelo erro de um formulário não pode parar
   embaixo dela (WCAG 2.4.11): a rolagem deixa 4rem livres no pé. */
@media (width < 64rem) {
  html {
    scroll-padding-bottom: 4rem;
  }
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npm run build && npx playwright test e2e/a11y.spec.ts -g "formulários com erro"`
Expected: PASS nos dois temas.

Sabotagens: troque `4rem` por `0` e refaça o build → falha "o botão de envio, em foco, fica sob a barra"; em `components/ui/campo.tsx`, ponha `whitespace-nowrap` no `<p id={idErro} …>` e refaça o build → falha o overflow numa rota (a mensagem de erro do slug é longa). Desfaça os dois.

- [ ] **Step 5: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Run: `cd web && npx playwright test`
Run (na raiz, com o Docker de pé): `go build ./... && go vet ./... && make test`
Run: `sh deploy/ci/checar-nome.sh`
Expected: tudo verde. O e2e de alvos de toque continua achando Subir, Descer e Tirar pelos nomes de sempre.

- [ ] **Step 6: Commit e PR**

```bash
git add web/src/index.css web/e2e/a11y.spec.ts
git commit -m "test(painel): formulários com erro no celular de 400 px e foco fora da barra inferior"
git push -u origin feat/redesign-demais-telas
gh pr create --title "feat(painel): redesign de canais, status page, configurações, alvo e login" --body "$(cat <<'EOF'
PR 6 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md (Telas 4 e 5). Plano: docs/superpowers/plans/2026-10-14-redesign-painel-parte3.md.

- Canais: estados em selos Lucide (sem glifos Unicode), "Pausado" em destaque com a verificação rebaixada, cartões, rádios de 44 px (auditoria C1–C3, S9).
- Status page: prévia que parece a pública (título, a mesma faixa de estado, um cartão por alvo), Subir/Descer em ícone secundário e Tirar terciário (SP2, SP3). A frase da faixa lê os casos de internal/httpapi/testdata/faixa.json, os mesmos que o Go vai ler no PR 7.
- Formulário de alvo e configurações em cartões de largura limitada (G3), rádios de 44 px, QR com a zona de silêncio do próprio SVG.
- Login com a marca, a frase de tom ("Seus serviços, vigiados de perto.", trocável em PaginaLogin.tsx) e o cartão nível 2 (G1).
- index.css sem o CSS legado de formulário e de seção; scroll-padding-bottom abaixo de lg para o foco não parar embaixo da barra inferior.
- e2e: formulários com erro de validação a 400 px, nos dois temas.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Antes do merge, o dono revê o visual: `cd web && npm run dev` com a API local, passando por Canais, Status page, Configurações, Novo alvo e Login nos dois temas e a 400 px.

---

# PR 7: Página pública

Branch: `feat/redesign-pagina-publica`, a partir da `main` com o PR 6 integrado.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-pagina-publica
```

### Task 7.1: CSP que libera só as fontes do próprio host

**Files:**
- Modify: `internal/httpapi/publicpage.go` (função nova `politicaDaPagina`; o `Page` a usa)
- Modify: `internal/httpapi/publicpage_test.go`
- Modify: `internal/httpapi/publicpage_integration_test.go`

**Interfaces:**
- Consumes: nada.
- Produces:

```go
// internal/httpapi/publicpage.go
func politicaDaPagina(nonce string) string
// "default-src 'none'; style-src 'nonce-<n>'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
```

- [ ] **Step 1: Escrever o teste que falha**

No fim de `internal/httpapi/publicpage_test.go`:

```go
// Review Focus 4 da parte 3 do redesign: a página carrega as fontes de
// /fonts/, no próprio host. A CSP abre font-src 'self' e nada além: a string
// inteira é conferida, para uma diretiva a mais (script, imagem, outro host)
// também quebrar o teste.
func TestPublicPagePolicyOpensOnlyTheOwnFonts(t *testing.T) {
	const quer = "default-src 'none'; style-src 'nonce-abc'; font-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	if got := politicaDaPagina("abc"); got != quer {
		t.Errorf("CSP da página pública:\n got %q\nwant %q", got, quer)
	}
}
```

Em `internal/httpapi/publicpage_integration_test.go`, em `TestPublicPageRendersWithoutLeaking`, a condição da CSP:

```go
	if m == nil || !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") || strings.Contains(csp, "unsafe-inline") ||
		!strings.Contains(csp, "default-src 'none'") || strings.Contains(corpo, "<script") {
```

vira:

```go
	if m == nil || !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") || strings.Contains(csp, "unsafe-inline") ||
		!strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "font-src 'self'") ||
		strings.Contains(corpo, "<script") {
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/httpapi/ -run TestPublicPagePolicyOpensOnlyTheOwnFonts`
Expected: FAIL de compilação, `undefined: politicaDaPagina`.

- [ ] **Step 3: Implementar**

Em `internal/httpapi/publicpage.go`, no `Page`, troque:

```go
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; "+
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
```

por:

```go
	hd.Set("Content-Security-Policy", politicaDaPagina(nonce))
```

e, logo depois da função `Page`:

```go
// politicaDaPagina é a CSP da página pública: o <style> com o nonce desta
// resposta e as fontes do próprio host (/fonts/, servidas pelo painel na
// mesma origem). Nada de script, imagem nem conexão.
func politicaDaPagina(nonce string) string {
	return "default-src 'none'; style-src 'nonce-" + nonce + "'; font-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
}
```

Atualize o comentário do topo do arquivo, que diz "com o CSS inline autorizado por nonce", para: "com o CSS inline autorizado por nonce e as fontes do próprio host (/fonts/)".

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/httpapi && go test ./internal/httpapi/ -run 'TestPublicPagePolicyOpensOnlyTheOwnFonts|TestPublicPageRendersWithoutLeaking' -v`
Expected: PASS (o de integração precisa do Docker: `make test` sobe o Postgres; rodando isolado, use o mesmo ambiente do `make test`).

Sabotagens: tire `font-src 'self'; ` da função → os dois falham; troque por `font-src *; ` → o unitário falha. Desfaça.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/publicpage.go internal/httpapi/publicpage_test.go internal/httpapi/publicpage_integration_test.go
git commit -m "feat(statuspage): CSP da página pública libera só as fontes do próprio host"
```

### Task 7.2: Faixa de estado em frase, com os casos do JSON

**Files:**
- Modify: `internal/httpapi/publicpage.go` (`paginaModelo.Summary` vira `Band`; `resumo` vira `faixa`)
- Modify: `internal/httpapi/templates/status.html` (só `{{.Summary}}` → `{{.Band.Text}}`; o template novo é da Task 7.4)
- Modify: `internal/httpapi/publicpage_test.go`
- Modify: `internal/httpapi/publicpage_integration_test.go`

**Interfaces:**
- Consumes: `internal/httpapi/testdata/faixa.json` (Task 6.3); `rotulo(statuspage.State) string` (já existe).
- Produces:

```go
// internal/httpapi/publicpage.go
type bandModelo struct{ State, Text, Detail string } // State: "up" | "degraded" | "down" | "unknown"
func faixa(items []statuspage.PublicItem) bandModelo
// paginaModelo: Nonce, Title, Generated string; Band bandModelo; Items []itemModelo; Incidents []string
```

- [ ] **Step 1: Escrever o teste que falha**

Em `internal/httpapi/publicpage_test.go`, acrescente `"encoding/json"`, `"os"` e `"path/filepath"` aos imports e, no fim:

```go
// A frase da faixa segue os casos de testdata/faixa.json, que a prévia do
// painel (web/src/features/statuspage/faixa.test.ts) também lê: a página
// publicada e a prévia dizem a mesma coisa.
func TestBandPhraseFollowsTheSharedCases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "faixa.json"))
	if err != nil {
		t.Fatal(err)
	}
	var casos []struct {
		Caso  string `json:"caso"`
		Itens []struct {
			Nome   string           `json:"nome"`
			Estado statuspage.State `json:"estado"`
		} `json:"itens"`
		Estado  string `json:"estado"`
		Frase   string `json:"frase"`
		Detalhe string `json:"detalhe"`
	}
	if err := json.Unmarshal(b, &casos); err != nil {
		t.Fatal(err)
	}
	if len(casos) != 8 {
		t.Fatalf("%d casos em testdata/faixa.json, queria 8", len(casos))
	}
	for _, c := range casos {
		t.Run(c.Caso, func(t *testing.T) {
			items := make([]statuspage.PublicItem, 0, len(c.Itens))
			for _, it := range c.Itens {
				items = append(items, statuspage.PublicItem{Name: it.Nome, State: it.Estado})
			}
			quer := bandModelo{State: c.Estado, Text: c.Frase, Detail: c.Detalhe}
			if got := faixa(items); got != quer {
				t.Errorf("faixa = %+v, queria %+v", got, quer)
			}
		})
	}
}
```

Em `TestPresentationRules`, apague o último laço (o de `resumo`, com os casos "Nenhum serviço nesta página." a "Há serviços fora do ar.") e a variável `items` dele: os casos da frase agora são os do JSON.

Em `internal/httpapi/publicpage_integration_test.go`, troque `"Há serviços fora do ar."` por `"1 serviço fora do ar."` na lista de `quer`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/httpapi/ -run TestBandPhraseFollowsTheSharedCases`
Expected: FAIL de compilação, `undefined: faixa` e `undefined: bandModelo`.

- [ ] **Step 3: Implementar**

Em `internal/httpapi/publicpage.go`:

1. O modelo:

```go
type paginaModelo struct {
	Nonce, Title, Summary, Generated string
	Items                            []itemModelo
	Incidents                        []string
}
```

vira:

```go
type paginaModelo struct {
	Nonce, Title, Generated string
	Band                    bandModelo
	Items                   []itemModelo
	Incidents               []string
}

// bandModelo é a faixa de estado do topo (spec do redesign, Telas 6): a
// frase, o detalhe com os nomes por estado e a classe da cor (o estado).
type bandModelo struct{ State, Text, Detail string }
```

2. Em `apresentar`, `Nonce: nonce, Title: v.Title, Summary: resumo(v.Items),` vira `Nonce: nonce, Title: v.Title, Band: faixa(v.Items),`.

3. Troque a função `resumo` inteira por:

```go
// gravidade é a ordem da faixa: o primeiro estado presente dá a frase.
var gravidade = []statuspage.State{statuspage.StateDown, statuspage.StateDegraded, statuspage.StateUnknown}

// faixa diz o estado da página numa frase, pelo pior estado presente, e
// lista os nomes por estado, do pior ao melhor, na ordem da página. Os casos
// ficam em testdata/faixa.json, que a prévia do painel também lê.
func faixa(items []statuspage.PublicItem) bandModelo {
	if len(items) == 0 {
		return bandModelo{State: string(statuspage.StateUnknown), Text: "Nenhum serviço nesta página."}
	}
	nomes := map[statuspage.State][]string{}
	for _, it := range items {
		nomes[it.State] = append(nomes[it.State], it.Name)
	}
	b := bandModelo{State: string(statuspage.StateUp), Text: "Todos os sistemas funcionando."}
	var partes []string
	for _, s := range gravidade {
		n := nomes[s]
		if len(n) == 0 {
			continue
		}
		if len(partes) == 0 {
			b.State, b.Text = string(s), fraseDaFaixa(s, len(n))
		}
		partes = append(partes, rotulo(s)+": "+strings.Join(n, ", ")+".")
	}
	b.Detail = strings.Join(partes, " ")
	return b
}

func fraseDaFaixa(s statuspage.State, n int) string {
	servicos := "1 serviço"
	if n != 1 {
		servicos = fmt.Sprintf("%d serviços", n)
	}
	switch s {
	case statuspage.StateDown:
		return servicos + " fora do ar."
	case statuspage.StateDegraded:
		return "Instabilidade em " + servicos + "."
	}
	return "Sem dados recentes de " + servicos + "."
}
```

4. Em `internal/httpapi/templates/status.html`, troque `<p>{{.Summary}}</p>` por `<p>{{.Band.Text}}</p>`.

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -w internal/httpapi && go vet ./internal/httpapi/ && go test ./internal/httpapi/ -run 'TestBandPhraseFollowsTheSharedCases|TestPresentationRules|TestPublicPageFitsInTenKilobytes' -v`
Expected: PASS (9 subtestes da faixa).

Sabotagem: em `gravidade`, troque a ordem para `StateDegraded, StateDown, StateUnknown` → o caso "o pior estado dá a frase…" falha. Desfaça. Rode também `cd web && npx vitest run src/features/statuspage/faixa.test.ts`: o mesmo JSON, verde dos dois lados.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/publicpage.go internal/httpapi/templates/status.html internal/httpapi/publicpage_test.go internal/httpapi/publicpage_integration_test.go
git commit -m "feat(statuspage): faixa de estado em frase na página pública"
```

### Task 7.3: A página pública comprimida (os 10 KB da §10.1)

**Files:**
- Modify: `internal/webui/webui.go` (`aceitaGzip` vira `AceitaGzip`)
- Modify: `internal/webui/webui_test.go`
- Modify: `internal/httpapi/publicpage.go` (`comprimida`; o `Page` comprime)
- Modify: `internal/httpapi/publicpage_test.go` (`paginaDeTeste` com o número de serviços; o teste de tamanho)
- Modify: `internal/httpapi/publicpage_integration_test.go`

**Interfaces:**
- Consumes: nada novo.
- Produces:

```go
// internal/webui/webui.go
func AceitaGzip(acceptEncoding string) bool // "gzip" presente e não recusado com q=0

// internal/httpapi/publicpage.go
func comprimida(b []byte) []byte // gzip, nível rápido

// internal/httpapi/publicpage_test.go
func paginaDeTeste(servicos int, dia func(i int) *float64) statuspage.View
```

- [ ] **Step 1: Escrever os testes que falham**

No fim de `internal/webui/webui_test.go`:

```go
// AceitaGzip é usada também pela página pública (internal/httpapi).
func TestAceitaGzip(t *testing.T) {
	for v, quer := range map[string]bool{
		"gzip": true, "gzip, deflate, br": true, "br, GZIP": true, "gzip;q=0.5": true,
		"gzip;q=0": false, "gzip; q=0.000": false, "br": false, "identity": false, "": false,
	} {
		if got := webui.AceitaGzip(v); got != quer {
			t.Errorf("AceitaGzip(%q) = %v, queria %v", v, got, quer)
		}
	}
}
```

Em `internal/httpapi/publicpage_test.go`, troque `paginaDeTeste` e `TestPublicPageFitsInTenKilobytes` por:

```go
// paginaDeTeste monta a projeção de n serviços com nomes longos e o
// histórico cheio; dia(i) decide o uptime do dia i de cada barra.
func paginaDeTeste(servicos int, dia func(i int) *float64) statuspage.View {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	u := 0.99912
	lat := int32(143)
	v := statuspage.View{Title: "Status da Empresa Exemplo de Serviços Online", GeneratedAt: agora}
	for i := range servicos {
		it := statuspage.PublicItem{Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i+1),
			State: statuspage.StateDegraded, Uptime90: &u, LatencyMS: &lat}
		for d := range it.Days {
			it.Days[d] = statuspage.Day{Date: agora.AddDate(0, 0, d-statuspage.Days+1), Uptime: dia(d)}
		}
		v.Items = append(v.Items, it)
	}
	for i := range statuspage.MaxIncidents {
		inicio := agora.Add(-time.Duration(i+1) * 26 * time.Hour)
		v.Incidents = append(v.Incidents, statuspage.PublicIncident{
			Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i%5+1), StartedAt: inicio, ResolvedAt: inicio.Add(95 * time.Minute),
		})
	}
	return v
}

// Spec §10.1: resposta abaixo de 10 KB. A página sai em gzip para quem o
// aceita (todo navegador), e o teto vale para o corpo comprimido: no pior
// caso de cinco serviços (os 90 dias espalhados por igual nas quatro
// classes, onde a codificação pela maioria quase não ajuda) e com os 20
// serviços que a página aceita, que em texto puro passavam dos 10 KB. Sem
// compressão, o teto de 16 KB pega uma regressão grande para o cliente que
// não aceita gzip.
func TestPublicPageFitsInTenKilobytes(t *testing.T) {
	valores := []*float64{nil, ptr(1), ptr(0.995), ptr(0.5)} // n, verde, d, f
	espalhado := func(i int) *float64 { return valores[i%4] }
	pior := renderizar(t, paginaDeTeste(5, espalhado))
	if n := len(comprimida([]byte(pior))); n >= 10*1024 {
		t.Errorf("pior caso comprimido: %d bytes, queria menos de 10 KB", n)
	}
	if n := len(pior); n >= 16*1024 {
		t.Errorf("pior caso sem compressão: %d bytes, queria menos de 16 KB", n)
	}
	if c, quer := strings.Count(pior, "<li"), 5*statuspage.Days+5+statuspage.MaxIncidents; c != quer {
		t.Errorf("%d <li>, queria %d (dias, serviços e incidentes)", c, quer)
	}
	cheia := renderizar(t, paginaDeTeste(statuspage.MaxItems, espalhado))
	if n := len(comprimida([]byte(cheia))); n >= 10*1024 {
		t.Errorf("%d serviços comprimidos: %d bytes, queria menos de 10 KB", statuspage.MaxItems, n)
	}
	t.Logf("pior caso %d bytes (%d comprimido); %d serviços %d bytes (%d comprimido)",
		len(pior), len(comprimida([]byte(pior))), statuspage.MaxItems, len(cheia), len(comprimida([]byte(cheia))))
}
```

Em `internal/httpapi/publicpage_integration_test.go`, acrescente `"compress/gzip"` aos imports e, no fim:

```go
// A página sai em gzip para quem aceita (spec §10.1, os 10 KB), e em texto
// puro para quem não aceita. O Vary separa as duas no cache público.
func TestPublicPageIsCompressedWhenTheClientAccepts(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "comprimida", "title": "T", "published": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}
	pedir := func(aceita string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, a.srv.URL+"/p/comprimida", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Com o cabeçalho posto à mão, o Transport não descomprime sozinho.
		req.Header.Set("Accept-Encoding", aceita)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return r, b
	}

	r, b := pedir("gzip, br")
	if r.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(r.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Content-Encoding %q e Vary %q, queria gzip e Accept-Encoding", r.Header.Get("Content-Encoding"), r.Header.Get("Vary"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	html, err := io.ReadAll(zr)
	if err != nil || !strings.Contains(string(html), "Nenhum serviço nesta página.") {
		t.Errorf("corpo comprimido ilegível (%v):\n%s", err, html)
	}

	r, b = pedir("identity")
	if r.Header.Get("Content-Encoding") != "" || !strings.Contains(string(b), "Nenhum serviço nesta página.") {
		t.Errorf("sem gzip, Content-Encoding %q e corpo:\n%s", r.Header.Get("Content-Encoding"), b)
	}
}
```

(Acrescente `"bytes"` aos imports do arquivo de integração se ainda não estiver lá.)

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/webui/ ./internal/httpapi/ -run 'TestAceitaGzip|TestPublicPageFitsInTenKilobytes'`
Expected: FAIL de compilação, `undefined: webui.AceitaGzip` e `undefined: comprimida`.

- [ ] **Step 3: Exportar o `AceitaGzip`**

Em `internal/webui/webui.go`, renomeie a função `aceitaGzip` para `AceitaGzip` (a declaração e a chamada em `servirArquivo`) e troque o comentário dela por:

```go
// AceitaGzip lê Accept-Encoding sem cerimônia: "gzip" presente e não
// recusado com q=0. O painel e a página pública (internal/httpapi) usam.
```

- [ ] **Step 4: Comprimir a resposta**

Em `internal/httpapi/publicpage.go`, acrescente `"compress/gzip"` e `"github.com/BipoXDXD/stillon/internal/webui"` aos imports e tire `"io"` (deixa de ser usado). No `Page`, troque o fim:

```go
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", politicaDaPagina(nonce))
	hd.Set("Cache-Control", cachePublico)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, &buf)
}
```

por:

```go
	corpo := buf.Bytes()
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", politicaDaPagina(nonce))
	hd.Set("Cache-Control", cachePublico)
	// O pior caso passa de 10 KB em texto puro; comprimido, fica perto de
	// 2 KB (spec §10.1). Vary: o cache público guarda as duas versões.
	hd.Add("Vary", "Accept-Encoding")
	if webui.AceitaGzip(r.Header.Get("Accept-Encoding")) {
		hd.Set("Content-Encoding", "gzip")
		corpo = comprimida(corpo)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(corpo)
}

// comprimida devolve o corpo em gzip. O nonce muda a cada resposta, então
// não há o que guardar: comprime a cada vez, no nível rápido.
func comprimida(b []byte) []byte {
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, gzip.BestSpeed) // nível válido: não falha
	_, _ = zw.Write(b)                                  // bytes.Buffer não falha
	_ = zw.Close()
	return out.Bytes()
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `gofmt -w internal && go vet ./internal/... && go test ./internal/webui/ ./internal/httpapi/ -run 'TestAceitaGzip|TestPublicPage|TestRoteamentoDoPainel' -v`
Expected: PASS; o log do tamanho mostra o pior caso perto de 9,6 KB em texto e ~2 KB comprimido (o template ainda é o antigo). O de integração precisa do Docker.

Sabotagens: troque a condição do `Page` por `if false` → o teste de integração falha (sem `Content-Encoding`); em `AceitaGzip`, troque `return q != "q=0" && …` por `return true` → `TestAceitaGzip` falha em `gzip;q=0`. Desfaça.

- [ ] **Step 6: Commit**

```bash
git add internal/webui internal/httpapi/publicpage.go internal/httpapi/publicpage_test.go internal/httpapi/publicpage_integration_test.go
git commit -m "feat(statuspage): página pública comprimida com gzip"
```

### Task 7.4: O template novo (tokens, fontes, faixa, barras com forma, legenda)

**Files:**
- Modify (reescrita): `internal/httpapi/templates/status.html`
- Modify (reescrita): `internal/httpapi/publicpage_a11y_test.go`
- Modify: `internal/httpapi/publicpage_test.go` (testes de renderização; contagem de `<li>`)

**Interfaces:**
- Consumes: `paginaModelo` com `Band` (Task 7.2); `politicaDaPagina` (Task 7.1); os tokens de `web/src/index.css` (parte 1).
- Produces: o template; para os testes Go, `lerCSSDaPagina`, `cor`, `contraste`, `luminancia` (em `publicpage_a11y_test.go`) e `diasEfetivos` (em `publicpage_test.go`).

- [ ] **Step 1: Reescrever o teste de acessibilidade e forma**

Substitua todo o `internal/httpapi/publicpage_a11y_test.go` por:

```go
package httpapi

import (
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Acessibilidade e forma da página pública, lidas do próprio template nos
// dois esquemas (claro e prefers-color-scheme: dark). As cores são os
// tokens do painel (web/src/index.css), em OKLCH: o teste converte para
// sRGB, como o src/tokens.test.ts do painel, e aplica a fórmula do WCAG 2.
// A barra de 90 dias é objeto gráfico: cada dia precisa de 3:1 contra o
// cartão e contra a página (WCAG 1.4.11), e a cor não é o único sinal
// (1.4.1): cada classe de dia tem forma própria, a altura ou só o contorno.

type tema string

const (
	claro  tema = "claro"
	escuro tema = "escuro"
)

var temas = []tema{claro, escuro}

type oklch struct{ l, c, h float64 }

// cssDaPagina é o <style> do template: o texto, os tokens de cada tema (o
// escuro já com o que herda do claro) e as regras de fora de @media e
// @font-face, por seletor.
type cssDaPagina struct {
	texto  string
	tokens map[tema]map[string]string
	regras map[string]map[string]string
}

var (
	estiloRE         = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
	tokenRE          = regexp.MustCompile(`--([\w-]+):\s*([^;}]+)`)
	tokensClarosRE   = regexp.MustCompile(`(?m)^:root\{([^}]*)\}`)
	tokensEscurosRE  = regexp.MustCompile(`@media\(prefers-color-scheme:dark\)\{:root\{([^}]*)\}\}`)
	blocoComChavesRE = regexp.MustCompile(`@(?:media|font-face)[^{]*\{(?:[^{}]*\{[^}]*\})*[^{}]*\}`)
	regraCSS         = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	varRE            = regexp.MustCompile(`^var\(--([\w-]+)\)$`)
	oklchRE          = regexp.MustCompile(`^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$`)
)

func lerCSSDaPagina(t *testing.T) cssDaPagina {
	t.Helper()
	fonte, err := templatesFS.ReadFile("templates/status.html")
	if err != nil {
		t.Fatal(err)
	}
	estilo := estiloRE.FindSubmatch(fonte)
	if estilo == nil {
		t.Fatal("template sem <style>")
	}
	texto := string(estilo[1])
	claros, escuros := tokensClarosRE.FindStringSubmatch(texto), tokensEscurosRE.FindStringSubmatch(texto)
	if claros == nil || escuros == nil {
		t.Fatal("template sem os tokens do claro (:root{…} no começo da linha) ou do escuro (@media(prefers-color-scheme:dark){:root{…}})")
	}
	c := cssDaPagina{
		texto:  texto,
		tokens: comHeranca(declaracoesDeTokens(claros[1]), declaracoesDeTokens(escuros[1])),
		regras: map[string]map[string]string{},
	}
	juntarRegras(c.regras, blocoComChavesRE.ReplaceAllString(texto, ""))
	return c
}

func declaracoesDeTokens(bloco string) map[string]string {
	out := map[string]string{}
	for _, m := range tokenRE.FindAllStringSubmatch(bloco, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// comHeranca monta os dois temas: o escuro vive no mesmo documento que o
// claro, e o que ele não redefine vem do claro.
func comHeranca(claros, escuros map[string]string) map[tema]map[string]string {
	todos := maps.Clone(claros)
	maps.Copy(todos, escuros)
	return map[tema]map[string]string{claro: claros, escuro: todos}
}

func juntarRegras(regras map[string]map[string]string, css string) {
	for _, r := range regraCSS.FindAllStringSubmatch(css, -1) {
		for _, sel := range strings.Split(r[1], ",") {
			sel = strings.TrimSpace(sel)
			if regras[sel] == nil {
				regras[sel] = map[string]string{}
			}
			for _, decl := range strings.Split(r[2], ";") {
				if prop, valor, ok := strings.Cut(decl, ":"); ok {
					regras[sel][strings.TrimSpace(prop)] = strings.TrimSpace(valor)
				}
			}
		}
	}
}

// valor lê uma propriedade de uma regra do template; falta é erro.
func (c cssDaPagina) valor(t *testing.T, seletor, prop string) string {
	t.Helper()
	v := c.regras[seletor][prop]
	if v == "" {
		t.Fatalf("regra %q sem %s", seletor, prop)
	}
	return v
}

// cor segue var() até um oklch(L C H), com os tokens de um tema.
func cor(t *testing.T, tokens map[string]string, valor string) oklch {
	t.Helper()
	for range 10 {
		m := varRE.FindStringSubmatch(strings.TrimSpace(valor))
		if m == nil {
			break
		}
		v, ok := tokens[m[1]]
		if !ok {
			t.Fatalf("token --%s ausente", m[1])
		}
		valor = v
	}
	m := oklchRE.FindStringSubmatch(strings.TrimSpace(valor))
	if m == nil {
		t.Fatalf("%q não é oklch(L C H) nem var(--x)", valor)
	}
	numero := func(s string) float64 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%q: %v", valor, err)
		}
		return f
	}
	return oklch{numero(m[1]), numero(m[2]), numero(m[3])}
}

// srgbLinear: OKLCH → OKLab → sRGB linear, com as matrizes de Björn
// Ottosson, as mesmas do CSS Color 4 e do src/tokens.test.ts do painel. Um
// canal fora do gamut é cortado em [0, 1].
func srgbLinear(c oklch) [3]float64 {
	angulo := c.h * math.Pi / 180
	a, b := c.c*math.Cos(angulo), c.c*math.Sin(angulo)
	longo := math.Pow(c.l+0.3963377774*a+0.2158037573*b, 3)
	medio := math.Pow(c.l-0.1055613458*a-0.0638541728*b, 3)
	curto := math.Pow(c.l-0.0894841775*a-1.291485548*b, 3)
	cortar := func(x float64) float64 { return math.Min(1, math.Max(0, x)) }
	return [3]float64{
		cortar(4.0767416621*longo - 3.3077115913*medio + 0.2309699292*curto),
		cortar(-1.2684380046*longo + 2.6097574011*medio - 0.3413193965*curto),
		cortar(-0.0041960863*longo - 0.7034186147*medio + 1.707614701*curto),
	}
}

// luminancia é a luminância relativa do WCAG 2; o sRGB já está linear.
func luminancia(c oklch) float64 {
	r := srgbLinear(c)
	return 0.2126*r[0] + 0.7152*r[1] + 0.0722*r[2]
}

// contraste segue a definição de contrast ratio do WCAG 2.
func contraste(a, b oklch) float64 {
	la, lb := luminancia(a), luminancia(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// Cada classe de dia em duas regras: a da maioria, na <ol> ("" é a <ol> sem
// classe, só com a regra base "ol li"), e a explícita, no <li> (ver
// itemModelo.Bar).
var classesDeDia = []struct{ nome, maioria, explicita string }{
	{"no ar", "", "li.g"},
	{"instável", ".d li", "li.d"},
	{"fora do ar", ".f li", "li.f"},
	{"sem dados", ".n li", "li.n"},
}

type forma struct{ altura, fundo, borda string }

func (f forma) contornada() bool { return f.fundo == "none" && f.borda != "" }

// corDaForma é o preenchimento, ou a cor do contorno do dia sem dados.
func (f forma) corDaForma() string {
	if f.contornada() {
		campos := strings.Fields(f.borda)
		return campos[len(campos)-1]
	}
	return f.fundo
}

// formaDoDia aplica, em ordem, a regra base, a da maioria da <ol> e, se o dia
// não é da maioria, a explícita. Todas têm a mesma especificidade, e vence a
// última no texto (TestPublicPageDaysHaveTheirOwnShape confere a ordem).
func (c cssDaPagina) formaDoDia(dia, barra int) forma {
	d := map[string]string{}
	maps.Copy(d, c.regras["ol li"])
	maps.Copy(d, c.regras[classesDeDia[barra].maioria])
	if dia != barra {
		maps.Copy(d, c.regras[classesDeDia[dia].explicita])
	}
	borda := d["border"]
	if borda == "0" {
		borda = ""
	}
	return forma{altura: d["height"], fundo: d["background"], borda: borda}
}

func TestPublicPageDaysHaveTheirOwnShape(t *testing.T) {
	c := lerCSSDaPagina(t)
	ultimaDaMaioria, primeiraExplicita := -1, len(c.texto)
	for _, d := range classesDeDia {
		if d.maioria != "" {
			ultimaDaMaioria = max(ultimaDaMaioria, strings.Index(c.texto, "\n"+d.maioria+"{"))
		}
		primeiraExplicita = min(primeiraExplicita, strings.Index(c.texto, "\n"+d.explicita+"{"))
	}
	if ultimaDaMaioria < 0 || primeiraExplicita < 0 || ultimaDaMaioria > primeiraExplicita {
		t.Fatalf("as regras explícitas (li.g, li.d…) vêm depois das da maioria (.d li…), cada uma no começo da linha: %d, %d", ultimaDaMaioria, primeiraExplicita)
	}
	semCor := map[string]string{}
	for i, d := range classesDeDia {
		f := c.formaDoDia(i, i)
		for j, barra := range classesDeDia {
			if g := c.formaDoDia(i, j); g != f {
				t.Errorf("dia %s numa barra de maioria %s desenha %+v, e não %+v", d.nome, barra.nome, g, f)
			}
		}
		if f.altura == "" || f.fundo == "" {
			t.Errorf("dia %s sem altura ou sem fundo: %+v", d.nome, f)
		}
		chave := f.altura + " " + strconv.FormatBool(f.contornada())
		if outro, repetida := semCor[chave]; repetida {
			t.Errorf("dias %s e %s têm a mesma forma (%s): a cor seria o único sinal", d.nome, outro, chave)
		}
		semCor[chave] = d.nome
	}
	for i, d := range classesDeDia {
		if contornada := c.formaDoDia(i, i).contornada(); contornada != (d.nome == "sem dados") {
			t.Errorf("dia %s: contorno = %v; só o sem dados é só o contorno (spec do redesign, Telas 6)", d.nome, contornada)
		}
	}
}

func TestPublicPageColorsAreAccessible(t *testing.T) {
	c := lerCSSDaPagina(t)
	for _, tm := range temas {
		t.Run(string(tm), func(t *testing.T) {
			tokens := c.tokens[tm]
			corDe := func(sel, prop string) oklch { return cor(t, tokens, c.valor(t, sel, prop)) }
			fundos := map[string]oklch{"página": corDe("body", "background"), "cartão": corDe(".s>li", "background")}
			for nomeDoFundo, fundo := range fundos {
				for _, sel := range []string{"body", ".up", ".degraded", ".down", ".unknown", ".m", ".l", "footer"} {
					if r := contraste(corDe(sel, "color"), fundo); r < 4.5 {
						t.Errorf("texto %s sobre o fundo da %s: %.2f:1, queria 4,5:1", sel, nomeDoFundo, r)
					}
				}
				for i, d := range classesDeDia {
					if r := contraste(cor(t, tokens, c.formaDoDia(i, i).corDaForma()), fundo); r < 3 {
						t.Errorf("dia %s sobre o fundo da %s: %.2f:1, queria 3:1", d.nome, nomeDoFundo, r)
					}
				}
			}
			for _, e := range []string{"up", "degraded", "down", "unknown"} {
				if r := contraste(corDe("."+e, "color"), corDe(".b."+e, "background")); r < 4.5 {
					t.Errorf("faixa %s: %.2f:1, queria 4,5:1", e, r)
				}
			}
		})
	}
}

// Review Focus 5 da parte 3: a página usa os tokens do painel, nos dois
// temas. Um token do template que não existe no index.css, ou com outro
// valor, quebra aqui.
func TestPublicPageUsesThePanelTokens(t *testing.T) {
	c := lerCSSDaPagina(t)
	painel := lerTokensDoPainel(t)
	for _, tm := range temas {
		for nome := range c.tokens[tm] {
			if _, ok := painel[tm][nome]; !ok {
				t.Errorf("%s: --%s não existe no web/src/index.css", tm, nome)
				continue
			}
			daPagina, doPainel := cor(t, c.tokens[tm], "var(--"+nome+")"), cor(t, painel[tm], "var(--"+nome+")")
			if math.Abs(daPagina.l-doPainel.l) > 1e-9 || math.Abs(daPagina.c-doPainel.c) > 1e-9 || math.Abs(daPagina.h-doPainel.h) > 1e-9 {
				t.Errorf("%s: --%s é %+v na página e %+v no painel", tm, nome, daPagina, doPainel)
			}
		}
	}
}

// lerTokensDoPainel lê os blocos :root e .dark do index.css do painel.
func lerTokensDoPainel(t *testing.T) map[tema]map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "index.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	bloco := func(seletor string) string {
		i := strings.Index(css, "\n"+seletor+" {")
		if i < 0 {
			t.Fatalf("web/src/index.css sem o bloco %q", seletor)
		}
		return css[i : i+strings.Index(css[i:], "}")]
	}
	return comHeranca(declaracoesDeTokens(bloco(":root")), declaracoesDeTokens(bloco(".dark")))
}

// Review Focus 4: o template só aponta fontes do próprio host, que é o que
// o font-src 'self' da politicaDaPagina libera.
func TestTemplateFontsComeFromTheOwnHost(t *testing.T) {
	c := lerCSSDaPagina(t)
	urls := regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(c.texto, -1)
	if len(urls) != 2 {
		t.Fatalf("%d url() no template, queria as 2 fontes", len(urls))
	}
	for _, u := range urls {
		if !strings.HasPrefix(u[1], "/fonts/") || !strings.HasSuffix(u[1], ".woff2") {
			t.Errorf("url(%s) fora de /fonts/*.woff2", u[1])
		}
	}
}

// A conversão e a fórmula conferem com os valores de referência: preto ×
// branco = 21:1; #767676, o cinza mais claro com 4,5:1 sobre branco, é
// oklch(0.5658 0 0); o vermelho sRGB puro tem luminância 0,2126.
func TestContrastFormulaMatchesReference(t *testing.T) {
	branco := oklch{1, 0, 0}
	if r := contraste(oklch{0, 0, 0}, branco); math.Abs(r-21) > 1e-6 {
		t.Errorf("preto x branco = %.4f, queria 21", r)
	}
	if r := contraste(oklch{0.5658, 0, 0}, branco); math.Abs(r-4.54) > 0.005 {
		t.Errorf("#767676 x branco = %.4f, queria 4,54", r)
	}
	if l := luminancia(oklch{0.62796, 0.25768, 29.2339}); math.Abs(l-0.2126) > 0.001 {
		t.Errorf("vermelho puro: luminância %.4f, queria 0,2126", l)
	}
}
```

- [ ] **Step 2: Escrever os testes de renderização**

Em `internal/httpapi/publicpage_test.go`, acrescente `"regexp"`, `"slices"` e `"unicode/utf8"` aos imports. No `TestPublicPageFitsInTenKilobytes`, troque a contagem de `<li>`:

```go
	if c, quer := strings.Count(pior, "<li"), 5*statuspage.Days+5+statuspage.MaxIncidents; c != quer {
		t.Errorf("%d <li>, queria %d (dias, serviços e incidentes)", c, quer)
	}
```

por:

```go
	if c, quer := strings.Count(pior, "<li"), 5*statuspage.Days+5+statuspage.MaxIncidents+itensDaLegenda; c != quer {
		t.Errorf("%d <li>, queria %d (dias, serviços, incidentes e legenda)", c, quer)
	}
```

e, no fim do arquivo:

```go
// A legenda tem quatro <li> de texto, cada um com uma barra de um <li>.
const itensDaLegenda = 8

var (
	listaDeServicosRE = regexp.MustCompile(`(?s)<ul class=s>(.*?)</ul>`)
	barraRE           = regexp.MustCompile(`<ol(?: class="(\w)")? aria-hidden=true>(.*?)</ol>`)
	diaRE             = regexp.MustCompile(`<li(?: class="(\w)")?>`)
)

// diasEfetivos lê de volta a classe de cada dia de cada barra da lista de
// serviços: a do <li>, ou a da <ol> (a maioria), ou "g" (no ar) sem as duas.
func diasEfetivos(t *testing.T, html string) [][]string {
	t.Helper()
	lista := listaDeServicosRE.FindStringSubmatch(html)
	if lista == nil {
		t.Fatal("a página não tem a lista de serviços")
	}
	var barras [][]string
	for _, b := range barraRE.FindAllStringSubmatch(lista[1], -1) {
		var dias []string
		for _, d := range diaRE.FindAllStringSubmatch(b[2], -1) {
			switch {
			case d[1] != "":
				dias = append(dias, d[1])
			case b[1] != "":
				dias = append(dias, b[1])
			default:
				dias = append(dias, "g")
			}
		}
		barras = append(barras, dias)
	}
	return barras
}

func contem(t *testing.T, html string, trechos ...string) {
	t.Helper()
	for _, s := range trechos {
		if !strings.Contains(html, s) {
			t.Errorf("a página não tem %q", s)
		}
	}
}

// Spec do redesign, Telas 6: a faixa de estado em frase, a legenda e a classe
// do estado de cada dia. Sabotagem: em classeDia, troque o "d" pelo "f" →
// o segundo dia sai "f", e a lista de dias não confere.
func TestTemplateRendersBandLegendAndDayClasses(t *testing.T) {
	it := statuspage.PublicItem{Name: "API", State: statuspage.StateDegraded, Uptime90: ptr(0.9991)}
	for d := range it.Days {
		it.Days[d].Uptime = ptr(1)
	}
	it.Days[0].Uptime, it.Days[1].Uptime, it.Days[2].Uptime = nil, ptr(0.995), ptr(0.5)
	html := renderizar(t, statuspage.View{Title: "Empresa", Items: []statuspage.PublicItem{it}})
	contem(t, html,
		`<div class="b degraded"><strong>Instabilidade em 1 serviço.</strong><p>Lento: API.</div>`,
		`<span class=degraded>Lento</span>`,
		"<ul class=l>", "No ar (99,9% ou mais)", "Instável (99% a 99,9%)", "Fora do ar (menos de 99%)", "Sem dados</ul>",
		"Cada barra é um dia, de 90 dias atrás até hoje.",
		"99,91% no ar em 90 dias",
		"Monitorado pelo StillOn",
	)
	quer := append([]string{"n", "d", "f"}, slices.Repeat([]string{"g"}, statuspage.Days-3)...)
	if got := diasEfetivos(t, html); len(got) != 1 || !slices.Equal(got[0], quer) {
		t.Errorf("dias = %v, queria %v", got, quer)
	}
}

// Review Focus 1: sem itens, a faixa diz isso, e não há lista, legenda nem
// a nota das barras; o histórico continua.
func TestPublicPageWithoutItems(t *testing.T) {
	html := renderizar(t, statuspage.View{Title: "Empresa"})
	contem(t, html,
		`<div class="b unknown"><strong>Nenhum serviço nesta página.</strong></div>`,
		"Nenhum incidente nos últimos 90 dias.",
	)
	for _, nao := range []string{"<ul class=s>", "<ul class=l>", "Cada barra é um dia"} {
		if strings.Contains(html, nao) {
			t.Errorf("a página sem itens tem %q", nao)
		}
	}
}

// Review Focus 2: um serviço sem nenhum check em 90 dias é a barra inteira
// em contorno, com o uptime e o selo dizendo "sem dados".
func TestComponentWithoutDataIn90Days(t *testing.T) {
	novo := statuspage.PublicItem{Name: "Novo", State: statuspage.StateUnknown}
	html := renderizar(t, statuspage.View{Title: "Empresa", Items: []statuspage.PublicItem{novo}})
	contem(t, html,
		`<ol class="n" aria-hidden=true>`+strings.Repeat("<li>", statuspage.Days)+`</ol>`,
		"Sem dados nos últimos 90 dias",
		`<span class=unknown>Sem dados</span>`,
		`<div class="b unknown"><strong>Sem dados recentes de 1 serviço.</strong><p>Sem dados: Novo.</div>`,
	)
}

// Review Focus 3: o maior título que o servidor aceita, sem espaço, quebra em
// qualquer ponto, e o nome do serviço e o detalhe da faixa também.
func TestLongOrganizationNameWraps(t *testing.T) {
	titulo := strings.Repeat("Organizacao", 9) + "X"
	if n := utf8.RuneCountInString(titulo); n != 100 {
		t.Fatalf("título de %d runas, queria 100 (o teto do servidor)", n)
	}
	html := renderizar(t, statuspage.View{Title: titulo})
	contem(t, html, "<title>"+titulo+"</title>", "<h1>"+titulo+"</h1>")
	c := lerCSSDaPagina(t)
	for _, sel := range []string{"h1", ".h strong", ".b p", ".i li"} {
		if v := c.regras[sel]["overflow-wrap"]; v != "anywhere" {
			t.Errorf("%s com overflow-wrap %q, queria anywhere", sel, v)
		}
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `go test ./internal/httpapi/ -run 'TestPublicPage|TestTemplate|TestComponent|TestLong|TestContrast'`
Expected: FAIL. `lerCSSDaPagina` não acha `:root{` (o template antigo usa hex no `body`); a página não tem a faixa `<div class="b …">` nem a legenda; `TestTemplateFontsComeFromTheOwnHost` acha 0 `url()`. `TestContrastFormulaMatchesReference` passa (é o controle da fórmula).

- [ ] **Step 4: Escrever o template novo**

Substitua todo o `internal/httpapi/templates/status.html` por (uma regra por linha no `<style>`: os testes leem `:root{` e as regras dos dias no começo da linha):

```html
<!doctype html><html lang=pt-BR><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style nonce="{{.Nonce}}">
@font-face{font-family:Fraunces;src:url(/fonts/fraunces-latin.woff2)format("woff2");font-weight:100 900;font-display:swap}
@font-face{font-family:Inter;src:url(/fonts/inter-latin.woff2)format("woff2");font-weight:100 900;font-display:swap}
:root{color-scheme:light dark;--fundo:oklch(.98 .008 85);--superficie:oklch(1 0 0);--texto:oklch(.22 .015 60);--suave:oklch(.45 .015 60);--apagado:oklch(.53 .012 60);--borda:oklch(.9 .01 80);--borda-controle:oklch(.62 .015 60);--estado-no-ar:oklch(.48 .11 150);--estado-no-ar-100:oklch(.96 .03 150);--estado-degradado:oklch(.52 .12 70);--estado-degradado-100:oklch(.97 .035 85);--estado-fora:oklch(.5 .17 30);--estado-fora-100:oklch(.96 .025 30);--estado-sem-dados:var(--suave);--estado-sem-dados-100:oklch(.95 .01 80)}
@media(prefers-color-scheme:dark){:root{--fundo:oklch(.2 .01 60);--superficie:oklch(.25 .01 60);--texto:oklch(.93 .01 80);--suave:oklch(.75 .012 70);--apagado:oklch(.65 .012 70);--borda:oklch(.32 .01 60);--borda-controle:oklch(.55 .012 70);--estado-no-ar:oklch(.78 .13 150);--estado-no-ar-100:oklch(.3 .05 150);--estado-degradado:oklch(.82 .13 80);--estado-degradado-100:oklch(.31 .05 80);--estado-fora:oklch(.72 .17 30);--estado-fora-100:oklch(.3 .06 30);--estado-sem-dados-100:oklch(.3 .01 60)}}
body{margin:0 auto;max-width:46rem;padding:32px 16px 48px;font:16px/1.5 Inter,ui-sans-serif,system-ui,sans-serif;font-variant-numeric:tabular-nums;color:var(--texto);background:var(--fundo)}
h1,h2,.b strong{font-family:Fraunces,ui-serif,Georgia,serif;font-weight:600}
h1{margin:0;font-size:28px;line-height:1.25;overflow-wrap:anywhere}
@media(min-width:40rem){h1{font-size:36px}}
h2{margin:48px 0 12px;font-size:20px}
.b{margin:24px 0 32px;padding:16px;border-left:4px solid;border-radius:8px}
.b strong{display:block;font-size:20px;line-height:1.4}
.b p{margin:4px 0 0;overflow-wrap:anywhere}
.b.up{background:var(--estado-no-ar-100)}
.b.degraded{background:var(--estado-degradado-100)}
.b.down{background:var(--estado-fora-100)}
.b.unknown{background:var(--estado-sem-dados-100)}
.up{color:var(--estado-no-ar)}
.degraded{color:var(--estado-degradado)}
.down{color:var(--estado-fora)}
.unknown{color:var(--estado-sem-dados)}
ul{margin:0;padding:0;list-style:none}
.s>li{margin:12px 0;padding:16px;border:1px solid var(--borda);border-radius:8px;background:var(--superficie)}
.h{display:flex;justify-content:space-between;gap:16px}
.h strong{min-width:0;overflow-wrap:anywhere}
.h span{font-size:14px;font-weight:600;white-space:nowrap}
ol{display:flex;align-items:flex-end;gap:1px;height:32px;margin:12px 0 8px;padding:0;list-style:none}
ol li{flex:1;box-sizing:border-box;height:100%;border-radius:1px;background:var(--estado-no-ar)}
.d li{height:66%;background:var(--estado-degradado)}
.f li{height:33%;background:var(--estado-fora)}
.n li{background:none;border:1px solid var(--borda-controle)}
li.g{height:100%;background:var(--estado-no-ar);border:0}
li.d{height:66%;background:var(--estado-degradado);border:0}
li.f{height:33%;background:var(--estado-fora);border:0}
li.n{height:100%;background:none;border:1px solid var(--borda-controle)}
.l{display:flex;flex-wrap:wrap;gap:8px 16px;margin:16px 0 8px}
.l>li{display:flex;align-items:center;gap:8px}
.l ol{width:8px;height:16px;margin:0}
.m,.l{font-size:14px;color:var(--suave)}
.m{margin:0}
.i li{margin:8px 0;overflow-wrap:anywhere}
footer{margin-top:48px;font-size:13px;color:var(--apagado)}
</style><main><h1>{{.Title}}</h1><div class="b {{.Band.State}}"><strong>{{.Band.Text}}</strong>{{with .Band.Detail}}<p>{{.}}{{end}}</div>{{if .Items}}<ul class=s>{{range .Items}}<li><div class=h><strong>{{.Name}}</strong><span class={{.State}}>{{.Label}}</span></div><ol{{with .Bar}} class="{{.}}"{{end}} aria-hidden=true>{{range .Days}}<li{{with .}} class="{{.}}"{{end}}>{{end}}</ol><p class=m>{{.Uptime}}{{with .Latency}} · {{.}}{{end}}{{end}}</ul><ul class=l><li><ol aria-hidden=true><li></ol>No ar (99,9% ou mais)<li><ol class=d aria-hidden=true><li></ol>Instável (99% a 99,9%)<li><ol class=f aria-hidden=true><li></ol>Fora do ar (menos de 99%)<li><ol class=n aria-hidden=true><li></ol>Sem dados</ul><p class=m>Cada barra é um dia, de 90 dias atrás até hoje.{{end}}<h2>Incidentes resolvidos (90 dias)</h2>{{if .Incidents}}<ul class=i>{{range .Incidents}}<li>{{.}}{{end}}</ul>{{else}}<p class=m>Nenhum incidente nos últimos 90 dias.{{end}}</main><footer>Atualizado em {{.Generated}} (UTC) · Monitorado pelo StillOn</footer></html>
```

Atualize o comentário de `itemModelo.Bar` em `publicpage.go`: a frase sobre os 10 KB passa a dizer "codificar pela maioria mantém o texto puro abaixo do teto de 16 KB e o comprimido bem abaixo dos 10 KB (spec §10.1)"; e o comentário de `classeDia`: a classe `n` é contorno, `d` dois terços e `f` um terço da altura.

- [ ] **Step 5: Rodar e ver passar**

Run: `gofmt -w internal/httpapi && go vet ./internal/httpapi/ && go test ./internal/httpapi/ -run 'TestPublicPage|TestTemplate|TestComponent|TestLong|TestContrast|TestBar|TestPresentation|TestBand' -v`
Expected: PASS; o log do tamanho mostra o pior caso perto de 12,3 KB em texto e ~2 KB comprimido.

Sabotagens (veja falhar e desfaça):
- em `.f li` e `li.f`, troque `33%` por `66%` → "dias instável e fora do ar têm a mesma forma";
- no `:root`, troque `--estado-degradado:oklch(.52 .12 70)` por `oklch(.7 .12 70)` → falham a paridade com o painel e o contraste do dia instável no claro;
- no bloco escuro, troque `--borda-controle:oklch(.55 .012 70)` por `oklch(.35 .012 70)` → falha o contraste do dia sem dados no escuro;
- tire o `overflow-wrap:anywhere` do `h1` → falha `TestLongOrganizationNameWraps`;
- mova a linha `li.g{…}` para antes de `.d li{…}` → falha a ordem das regras.

- [ ] **Step 6: Suíte Go inteira**

Run (na raiz, com o Docker de pé): `go build ./... && go vet ./... && make test`
Expected: tudo verde, inclusive `cmd/stillon` (o e2e Go da status page procura "100,00% no ar em 90 dias", "Sem dados" e "latência típica 80 ms", que continuam) e a integração (`1 serviço fora do ar.`, `font-src 'self'`, gzip).

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi/templates/status.html internal/httpapi/publicpage.go internal/httpapi/publicpage_test.go internal/httpapi/publicpage_a11y_test.go
git commit -m "feat(statuspage): página pública com os tokens, as fontes e as barras com forma"
```

### Task 7.5: As fontes no mesmo host, o e2e da página pública, verificação e PR

**Files:**
- Modify: `internal/httpapi/painel_integration_test.go` (`comPainel` e a tabela de `TestCadaPrefixoCaiNaCadeiaCerta`)
- Create: `web/e2e/pagina-publica.spec.ts`

**Interfaces:**
- Consumes: `controlePositivo`, `test`, `expect` de `web/e2e/fixtures.ts`; as rotas `POST /api/v1/auth/login`, `POST /api/v1/targets`, `DELETE /api/v1/targets/{id}`, `GET|POST /api/v1/status-pages`, `PATCH /api/v1/status-pages/{id}`, `POST /api/v1/auth/logout`.
- Produces: nada para os PRs seguintes.

- [ ] **Step 1: Fixar que `/fonts/…` sai da cadeia do painel no mesmo host**

Em `internal/httpapi/painel_integration_test.go`, no `fstest.MapFS` de `comPainel`, acrescente:

```go
		"fonts/inter-latin.woff2": {Data: []byte("wOF2")},
```

e, na tabela de `TestCadaPrefixoCaiNaCadeiaCerta`, depois do caso de `/assets/index-a1b.js`:

```go
		// A página pública (/p/…) carrega as fontes daqui, no mesmo host:
		// é o que o font-src 'self' dela libera (redesign, PR 7).
		{"/fonts/inter-latin.woff2", http.StatusOK, false, "font/woff2"},
```

Run: `go test ./internal/httpapi/ -run TestCadaPrefixoCaiNaCadeiaCerta -v` (Docker)
Expected: PASS. É caracterização: o comportamento já existe. Sabotagem: em `internal/httpapi/routes.go`, acrescente `raiz.Handle("/fonts/", http.NotFoundHandler())` antes do `raiz.Handle("/", …)` → o caso falha com 404. Desfaça.

- [ ] **Step 2: Escrever o e2e**

Crie `web/e2e/pagina-publica.spec.ts`:

```ts
import AxeBuilder from '@axe-core/playwright'
import type { APIRequestContext } from '@playwright/test'
import { controlePositivo, expect, test } from './fixtures'

// A página pública redesenhada (PR 7 do redesign) contra o binário de
// verdade: o template Go, a CSP com font-src 'self' e as fontes de /fonts/,
// que o painel serve no mesmo host. A API monta um alvo e a status page do
// usuário (há uma por usuário: a que existir é alterada e, no fim, volta a
// ser o que era). O alvo é apagado no fim.
// Sabotagem: tirar o font-src 'self' de politicaDaPagina (publicpage.go) e
// refazer o binário → as fontes ficam em "error" e a CSP acusa a violação.

const MUTACAO = { 'X-Requested-With': 'stillon' }
// 100 caracteres sem espaço: o maior título que o servidor aceita e o pior
// caso de quebra de linha no celular (Review Focus 3 da parte 3).
const TITULO = `${'Organizacao'.repeat(9)}X`
const NOME = 'API de pagamentos'
// O estado do alvo muda enquanto o sondador trabalha: qualquer frase de um
// serviço vale.
const FRASES = /^(Todos os sistemas funcionando|1 serviço fora do ar|Instabilidade em 1 serviço|Sem dados recentes de 1 serviço)\.$/
// O login divide o balde do IP (5 de burst) com o fluxo.spec.ts, que roda
// antes: um 429 espera e tenta de novo, com teto.
const RECUOS_DO_429_MS = [2_000, 4_000, 8_000, 16_000]

type Pagina = {
  id: string
  slug: string
  title: string
  published: boolean
  items: { target_id: string; display_name: string }[]
}

async function entrar(request: APIRequestContext, email: string, senha: string): Promise<Record<string, string>> {
  for (let i = 0; ; i++) {
    const r = await request.post('/api/v1/auth/login', { headers: MUTACAO, data: { email, password: senha } })
    const espera = RECUOS_DO_429_MS[i]
    if (r.status() !== 429 || espera === undefined) {
      expect(r.status(), 'login pela API').toBe(200)
      const { access_token: token } = (await r.json()) as { access_token: string }
      return { Authorization: `Bearer ${token}`, ...MUTACAO }
    }
    await new Promise((ok) => setTimeout(ok, espera))
  }
}

test('página pública: faixa, legenda, fontes sob a CSP, título longo a 400 px e os dois temas', async ({
  page,
  request,
  credenciais,
}) => {
  test.setTimeout(120_000)
  const cabecalhos = await entrar(request, credenciais.email, credenciais.senha)
  const criado = await request.post('/api/v1/targets', {
    headers: cabecalhos,
    data: {
      name: `E2E pública ${Date.now()}`,
      url: 'https://stillon-e2e.invalid/',
      method: 'GET',
      interval_sec: 60,
      timeout_ms: 5000,
      expected_status: 200,
    },
  })
  expect(criado.status(), 'criar o alvo').toBe(201)
  const { id: alvo } = (await criado.json()) as { id: string }
  const lista = await request.get('/api/v1/status-pages', { headers: cabecalhos })
  expect(lista.status(), 'listar as status pages').toBe(200)
  const original = ((await lista.json()) as { items: Pagina[] }).items[0]
  const corpo = { title: TITULO, published: true, items: [{ target_id: alvo, display_name: NOME }] }
  let nossa: Pagina | undefined
  try {
    const salva = original
      ? await request.patch(`/api/v1/status-pages/${original.id}`, { headers: cabecalhos, data: corpo })
      : await request.post('/api/v1/status-pages', { headers: cabecalhos, data: { ...corpo, slug: 'e2e-pagina-publica' } })
    expect(salva.status(), 'salvar a status page').toBe(original ? 200 : 201)
    nossa = (await salva.json()) as Pagina

    await page.setViewportSize({ width: 400, height: 800 })
    for (const [tema, fundo] of [
      ['light', 'oklch(0.98 0.008 85)'],
      ['dark', 'oklch(0.2 0.01 60)'],
    ] as const) {
      await page.emulateMedia({ colorScheme: tema })
      await page.goto(`/p/${nossa.slug}`)
      await expect(page.getByRole('heading', { level: 1, name: TITULO })).toBeVisible()
      await expect(page.getByText(FRASES)).toBeVisible()
      await expect(page.getByText(NOME, { exact: true })).toBeVisible()
      await expect(page.getByText('Fora do ar (menos de 99%)')).toBeVisible()
      await expect(page.getByText('Monitorado pelo StillOn')).toBeVisible()
      expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor), `${tema}: o fundo`).toBe(fundo)
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${tema}: overflow`).toBe(400)
      await expect
        .poll(
          () =>
            page.evaluate(() => {
              const fontes: string[] = []
              document.fonts.forEach((f) => fontes.push(`${f.family.replaceAll('"', '')} ${f.status}`))
              return fontes.sort()
            }),
          `${tema}: as fontes de /fonts/ carregam sob a CSP`,
        )
        .toEqual(['Fraunces loaded', 'Inter loaded'])
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${tema}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await controlePositivo(page, `página pública, ${tema}`)
    }
  } finally {
    if (original) {
      await request.patch(`/api/v1/status-pages/${original.id}`, {
        headers: cabecalhos,
        data: { title: original.title, published: original.published, items: original.items },
      })
    } else if (nossa) {
      await request.patch(`/api/v1/status-pages/${nossa.id}`, { headers: cabecalhos, data: { published: false, items: [] } })
    }
    await request.delete(`/api/v1/targets/${alvo}`, { headers: cabecalhos })
    await request.post('/api/v1/auth/logout', { headers: MUTACAO })
  }
})
```

- [ ] **Step 3: Rodar e ver falhar com a sabotagem, depois passar**

O comportamento já está no lugar desde a Task 7.4, então o "falhar" é pela sabotagem:

Run: em `internal/httpapi/publicpage.go`, tire `font-src 'self'; ` de `politicaDaPagina`; depois `cd web && npm run build && npx playwright test e2e/pagina-publica.spec.ts`
Expected: FAIL em "as fontes de /fonts/ carregam sob a CSP" (`Fraunces error`, `Inter error`) ou em "violações de CSP" (`font-src` na lista).

Desfaça a sabotagem e rode de novo.
Expected: PASS.

Se o fundo computado vier noutra forma (por exemplo, `oklch(0.98 0.008 85 / 1)`), confira no relatório do Playwright o valor real e ajuste só a string esperada: o que o teste prova é que o fundo do escuro é outro token, não a sintaxe.

- [ ] **Step 4: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build`
Run: `cd web && npx playwright test`
Run (na raiz, com o Docker de pé): `go build ./... && go vet ./... && make test`
Run: `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 5: Ver a página no navegador**

Na raiz: `docker compose up --wait` (ele constrói do código), entre em <http://localhost:8080> com a conta de demonstração, publique uma status page com o alvo de exemplo e abra o link, nos dois temas do sistema operacional e a 400 px no modo de dispositivo do navegador. Confira a olho a faixa, as barras, a legenda, o histórico e o rodapé, e que os títulos estão em Fraunces. Depois, `docker compose down`.

- [ ] **Step 6: Commit e PR**

```bash
git add internal/httpapi/painel_integration_test.go web/e2e/pagina-publica.spec.ts
git commit -m "test(statuspage): e2e da página pública com fontes, tema escuro e título longo"
git push -u origin feat/redesign-pagina-publica
gh pr create --title "feat(statuspage): página pública no visual novo, com fontes e tema escuro" --body "$(cat <<'EOF'
PR 7 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md (Telas 6). Plano: docs/superpowers/plans/2026-10-14-redesign-painel-parte3.md.

- Template com os tokens OKLCH do painel no CSS inline (com nonce), Fraunces e Inter de /fonts/ e o tema escuro por prefers-color-scheme. Um teste Go confere que cada token é o do web/src/index.css.
- Faixa de estado em frase ("Todos os sistemas funcionando.", "Instabilidade em 1 serviço." com os nomes), com os casos em internal/httpapi/testdata/faixa.json, os mesmos da prévia do painel.
- Barras de 90 dias com forma e cor (cheia, dois terços, um terço, contorno), cada cor a 3:1 sobre o cartão e a página nos dois temas; legenda; uptime de 90 dias; histórico; "Monitorado pelo StillOn".
- CSP com font-src 'self', e nada além (teste da string inteira).
- Resposta em gzip para quem aceita: o template novo passa de 10 KB em texto puro (~12,3 KB no pior caso de 5 serviços) e fica perto de 2 KB comprimido. **Para o dono confirmar:** a spec original (§10.1) pede "resposta abaixo de 10 KB"; o teste mede o corpo comprimido contra os 10 KB e o texto puro contra um teto de 16 KB. Os 20 serviços que a página aceita, que antes passavam dos 10 KB, agora cabem.
- O que os dados atuais não trazem, e por isso ficou de fora: início do incidente em andamento ("fora do ar há 2 h"), tipo do incidente resolvido, causa (fora pela §5.6), hora do último check e horário local.
- e2e contra o binário: fontes carregadas sob a CSP, título de 100 caracteres a 400 px, os dois temas, axe.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

# PR 8: Fechamento

Branch: `feat/redesign-fechamento`, a partir da `main` com o PR 7 integrado.

```bash
git switch main && git pull --ff-only && git switch -c feat/redesign-fechamento
```

### Task 8.1: Todo o `src` no teste do sistema

**Files:**
- Modify: `web/src/sistema.test.ts` (`ARQUIVOS_NO_SISTEMA`)
- Modify: os arquivos que o teste reprovar

**Interfaces:**
- Consumes: as regras do sistema da parte 1.
- Produces: `ARQUIVOS_NO_SISTEMA` com todo `.ts`/`.tsx` de `src`, menos testes e `src/test/`.

- [ ] **Step 1: Escrever o teste que falha**

Em `web/src/sistema.test.ts`, troque a constante `ARQUIVOS_NO_SISTEMA` (a lista literal) por:

```ts
// PR 8 do redesign: todo o src entra no sistema, menos os testes e os
// ajudantes de teste (src/test/). Um arquivo novo entra sozinho.
const ARQUIVOS_NO_SISTEMA = readdirSync('src', { recursive: true, encoding: 'utf8' })
  .filter((nome) => /\.tsx?$/.test(nome) && !nome.includes('.test.') && !nome.startsWith('test/'))
  .map((nome) => `src/${nome}`)
  .sort()
```

e, logo depois do `describe('o detector do sistema', …)`:

```ts
describe('a lista do sistema', () => {
  it('cobre as telas e os componentes', () => {
    expect(ARQUIVOS_NO_SISTEMA).toEqual(
      expect.arrayContaining([
        'src/app/Layout.tsx',
        'src/components/ui/botao.tsx',
        'src/features/painel/PaginaPainel.tsx',
        'src/features/incidentes/PaginaIncidentes.tsx',
        'src/features/alvos/PaginaDetalhe.tsx',
        'src/features/canais/PaginaCanais.tsx',
        'src/features/login/PaginaLogin.tsx',
      ]),
    )
    expect(ARQUIVOS_NO_SISTEMA.some((a) => a.includes('.test.') || a.startsWith('src/test/'))).toBe(false)
  })
})
```

- [ ] **Step 2: Rodar e ver o que reprova**

Run: `cd web && npx vitest run src/sistema.test.ts`
Expected: o teste "a lista do sistema" passa; os arquivos que nenhum PR de tela pôs na lista (por exemplo `src/components/EstadoDaConsulta.tsx`, `src/components/AvisoDeContexto.tsx`, `src/app/rotas.tsx`, as telas de erro) podem reprovar, cada falha com a regra e a classe. Se nada reprovar, o teste ainda vale: sabotagem no Step 4.

- [ ] **Step 3: Corrigir cada classe reprovada pela vizinha da escala**

Para cada linha da falha, troque a classe no arquivo apontado:

| Reprovada | Troque por |
|---|---|
| `p-5`, `px-5`, `py-5`, `space-y-5`, `gap-5` | o `-4` ou o `-6` do mesmo prefixo, o que ficar mais perto do desenho (escolha por eliminação, DI 2) |
| `p-7`, `space-y-7`, `p-10`, `gap-10` | `-6`/`-8`, `-8`/`-12` |
| `py-0.5`, `gap-1.5`, `space-y-1.5`, `py-2.5`, `mt-0.5` | `py-1`, `gap-2`, `space-y-2`, `py-2`, `mt-1` |
| `rounded`, `rounded-md`, `rounded-lg`, `rounded-xl` | `rounded-padrao` (selo e badge: `rounded-full`) |
| `font-medium`, `font-bold` | `font-semibold` |
| `shadow`, `shadow-sm`, `shadow-lg` | `sombra-nivel-1` (cartão) ou `sombra-nivel-2` (diálogo, popover) |
| `bg-white`, `bg-black/60`, `text-gray-…` | o token do papel (`bg-superficie`, `bg-sobreposicao`, `text-suave`…) |
| `text-[…]`, `px-[…]`, `rounded-[…]` | a classe da escala mais próxima (`text-xs` a `text-4xl`, `text-meta`) |

Se uma classe não tem vizinha sem mudar o desenho de propósito (um valor que a spec fixou), pare e registre na descrição do PR em vez de forçar.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd web && npx vitest run src/sistema.test.ts`
Expected: PASS.

Sabotagem: ponha `className="p-5"` num `<div>` de `src/components/EstadoDaConsulta.tsx` → falha com `espaçamento fora da escala: p-5`. Desfaça.

- [ ] **Step 5: Suíte, tipos, lint e e2e**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build && npx playwright test`
Expected: tudo verde.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "test(painel): todo o src no teste do sistema visual"
```

### Task 8.2: O roteiro de captura

**Files:**
- Create: `web/e2e/api-imitada.ts` (extraído do `a11y.spec.ts`)
- Modify: `web/e2e/a11y.spec.ts` (importa a API imitada)
- Create: `web/e2e/capturas.spec.ts`
- Modify: `internal/httpapi/publicpage_test.go` (`TestGerarExemploDaPaginaPublica`, `exemploDaPaginaPublica`)

**Interfaces:**
- Consumes: as fixtures de `web/src/test/fixtures.ts`; `renderizar` e `ptr` de `publicpage_test.go`.
- Produces:

```ts
// web/e2e/api-imitada.ts
export const agora: string
export const SEGREDO_NOVO: string
export const codigosNovos: z.input<typeof ConfirmacaoTotp>
export async function imitarApi(page: Page): Promise<void>
```

```go
// internal/httpapi/publicpage_test.go
func TestGerarExemploDaPaginaPublica(t *testing.T) // só com STILLON_EXEMPLO_PUBLICA=<arquivo absoluto>
```

- [ ] **Step 1: Extrair a API imitada sem mudar comportamento**

Crie `web/e2e/api-imitada.ts` e **mova** para ele, sem reescrever, o bloco do `web/e2e/a11y.spec.ts` que vai de `const agora = new Date().toISOString()` até o fim da função `imitarApi` (inclui `hora`, `spark`, `item`, `a1`, `bruta`, `horaria`, `respostas`, `SEGREDO_NOVO`, `troca`, `codigosNovos`, `respostaDe` e o que a parte 2 tenha acrescentado ali). Exporte `agora`, `SEGREDO_NOVO`, `codigosNovos` e `imitarApi` (`export const`, `export async function`). O topo do arquivo novo:

```ts
import type { Page, Route } from '@playwright/test'
import type { z } from 'zod'
import type { ConfirmacaoTotp, InicioDaTroca, Serie } from '../src/lib/api/schemas'
import { alvo, canal, incidente, itemDoResumo, me, pagina, sessao } from '../src/test/fixtures'

// A API do painel respondida pelo próprio teste (page.route), com as mesmas
// fixtures tipadas dos testes de tela: o a11y.spec.ts e o roteiro de
// captura (capturas.spec.ts) veem os mesmos dados.
```

(Se a parte 2 importou outras fixtures para esse bloco, mova o import junto.)

No `web/e2e/a11y.spec.ts`, acrescente:

```ts
import { agora, codigosNovos, imitarApi, SEGREDO_NOVO } from './api-imitada'
```

e tire os imports que ficarem sem uso.

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde (o lint aponta import sem uso; tire-o).
Run: `cd web && npm run build && npx playwright test e2e/a11y.spec.ts`
Expected: PASS, igual a antes da extração.

- [ ] **Step 2: O exemplo da página pública**

No fim de `internal/httpapi/publicpage_test.go` (acrescente `"os"` aos imports se a Task 7.2 não o deixou):

```go
// TestGerarExemploDaPaginaPublica grava a página pública com dados de
// exemplo no arquivo de STILLON_EXEMPLO_PUBLICA (caminho absoluto), para as
// capturas do PR 8 do redesign (web/e2e/capturas.spec.ts). Sem a variável,
// não faz nada.
func TestGerarExemploDaPaginaPublica(t *testing.T) {
	destino := os.Getenv("STILLON_EXEMPLO_PUBLICA")
	if destino == "" {
		t.Skip("só para as capturas: STILLON_EXEMPLO_PUBLICA=<arquivo absoluto>")
	}
	if err := os.WriteFile(destino, []byte(renderizar(t, exemploDaPaginaPublica())), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exemploDaPaginaPublica: dois serviços no ar (um com um dia instável), um
// lento hoje, um fora do ar, um novo com só 10 dias de dados, e três
// incidentes resolvidos.
func exemploDaPaginaPublica() statuspage.View {
	agora := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	servicos := []struct {
		nome    string
		estado  statuspage.State
		uptime  float64
		latencia int32
		ruins   map[int]float64
	}{
		{"API de pagamentos", statuspage.StateUp, 0.99991, 142, map[int]float64{41: 0.995}},
		{"Site institucional", statuspage.StateUp, 1, 318, nil},
		{"Painel do cliente", statuspage.StateDegraded, 0.9993, 2380, map[int]float64{88: 0.996, 89: 0.993}},
		{"Banco de relatórios", statuspage.StateDown, 0.9921, 88, map[int]float64{17: 0.97, 89: 0.61}},
	}
	v := statuspage.View{Title: "Status da Loja Exemplo", GeneratedAt: agora}
	for _, s := range servicos {
		lat := s.latencia
		it := statuspage.PublicItem{Name: s.nome, State: s.estado, Uptime90: ptr(s.uptime), LatencyMS: &lat}
		for d := range it.Days {
			it.Days[d] = statuspage.Day{Date: agora.AddDate(0, 0, d-statuspage.Days+1), Uptime: ptr(1)}
			if u, ruim := s.ruins[d]; ruim {
				it.Days[d].Uptime = ptr(u)
			}
		}
		v.Items = append(v.Items, it)
	}
	novo := statuspage.PublicItem{Name: "Webhook de pedidos", State: statuspage.StateUp, Uptime90: ptr(1)}
	for d := range novo.Days {
		novo.Days[d].Date = agora.AddDate(0, 0, d-statuspage.Days+1)
		if d >= statuspage.Days-10 {
			novo.Days[d].Uptime = ptr(1)
		}
	}
	v.Items = append(v.Items, novo)
	for i, nome := range []string{"Banco de relatórios", "Painel do cliente", "API de pagamentos"} {
		inicio := agora.Add(-time.Duration(i*9+2) * 24 * time.Hour)
		v.Incidents = append(v.Incidents, statuspage.PublicIncident{
			Name: nome, StartedAt: inicio, ResolvedAt: inicio.Add(time.Duration(40+i*25) * time.Minute),
		})
	}
	return v
}
```

Run: `gofmt -w internal/httpapi && go test ./internal/httpapi/ -run TestGerarExemploDaPaginaPublica -v`
Expected: SKIP com a mensagem da variável.
Run (na raiz): `mkdir -p web/e2e/.tmp && STILLON_EXEMPLO_PUBLICA="$PWD/web/e2e/.tmp/status-publica.html" go test ./internal/httpapi/ -run TestGerarExemploDaPaginaPublica -v`
Expected: PASS, e `grep -c '<li' web/e2e/.tmp/status-publica.html` mostra `466` (5 × 90 dias + 5 serviços + 3 incidentes + 8 da legenda).

- [ ] **Step 3: O roteiro**

Crie `web/e2e/capturas.spec.ts`:

```ts
import { mkdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'
import { imitarApi } from './api-imitada'
import { expect, test } from './fixtures'

// As capturas "depois" do redesign (PR 8), no formato das de
// docs/design/painel-antes: <tela>-<largura>-<tema>.png, página inteira, a
// 400 e 1280 px, nos temas claro e escuro. A API é a imitada do
// a11y.spec.ts (sem segredo, sem backend de dados); a página pública é o
// template Go renderizado com dados de exemplo (STILLON_EXEMPLO_PUBLICA,
// gerado por TestGerarExemploDaPaginaPublica), e as fontes vêm do binário.
// Só roda com CAPTURAR=1: as imagens vão para o repositório, não para o CI.
test.skip(!process.env.CAPTURAR, 'capturas só com CAPTURAR=1 (PR 8 do redesign)')

const DESTINO = path.resolve(import.meta.dirname, '../../docs/design/painel-depois')
const LARGURAS = [400, 1280] as const
const TEMAS = [
  ['claro', 'light'],
  ['escuro', 'dark'],
] as const

type Tela = { nome: string; rota: string; preparar?: (page: Page) => Promise<void> }

const erroDaApi = { error: { code: 'internal', message: '' }, trace_id: 't' }

const TELAS: Tela[] = [
  { nome: 'painel', rota: '/' },
  {
    nome: 'painel-vazio',
    rota: '/',
    preparar: (page) => page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 200, json: { items: [] } })),
  },
  {
    nome: 'painel-erro',
    rota: '/',
    preparar: (page) => page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 500, json: erroDaApi })),
  },
  { nome: 'detalhe-alvo', rota: '/targets/a1' },
  { nome: 'incidentes', rota: '/incidents' },
  { nome: 'canais', rota: '/channels' },
  { nome: 'status-page', rota: '/status-pages' },
  { nome: 'configuracoes', rota: '/settings' },
  { nome: 'novo-alvo', rota: '/targets/new' },
  {
    nome: 'login',
    rota: '/login',
    preparar: (page) =>
      page.route('**/api/v1/auth/refresh', (r) =>
        r.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
      ),
  },
]

// A tela pronta: título visível, nada carregando (o erro do painel espera as
// tentativas da consulta) e as fontes carregadas.
async function esperarATela(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible()
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 20_000 })
  await expect(page.getByText('Carregando…', { exact: true })).toHaveCount(0, { timeout: 20_000 })
  await page.evaluate(async () => {
    await document.fonts.ready
  })
}

test('capturas das telas do painel', async ({ page }) => {
  test.setTimeout(10 * 60_000)
  mkdirSync(DESTINO, { recursive: true })
  for (const tela of TELAS) {
    for (const largura of LARGURAS) {
      for (const [tema, esquema] of TEMAS) {
        await page.unrouteAll({ behavior: 'ignoreErrors' })
        await imitarApi(page)
        // Registrada depois, a rota da tela vence a do imitarApi.
        await tela.preparar?.(page)
        await page.setViewportSize({ width: largura, height: 900 })
        await page.emulateMedia({ colorScheme: esquema })
        await page.goto(tela.rota)
        await esperarATela(page)
        await page.screenshot({
          path: path.join(DESTINO, `${tela.nome}-${largura}-${tema}.png`),
          fullPage: true,
          animations: 'disabled',
        })
      }
    }
  }
})

test('capturas da página pública', async ({ page }) => {
  const exemplo = process.env.STILLON_EXEMPLO_PUBLICA ?? ''
  test.skip(
    exemplo === '',
    'gere antes o exemplo: STILLON_EXEMPLO_PUBLICA=<arquivo absoluto> go test ./internal/httpapi -run TestGerarExemploDaPaginaPublica',
  )
  const html = readFileSync(exemplo, 'utf8')
  await page.route('**/p/exemplo', (r) => r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: html }))
  mkdirSync(DESTINO, { recursive: true })
  for (const largura of LARGURAS) {
    for (const [tema, esquema] of TEMAS) {
      await page.setViewportSize({ width: largura, height: 900 })
      await page.emulateMedia({ colorScheme: esquema })
      await page.goto('/p/exemplo')
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
      await page.evaluate(async () => {
        await document.fonts.ready
      })
      await page.screenshot({
        path: path.join(DESTINO, `status-publica-${largura}-${tema}.png`),
        fullPage: true,
        animations: 'disabled',
      })
    }
  }
})
```

Run: `cd web && npm run typecheck && npm run lint`
Expected: verde.
Run: `cd web && npm run build && npx playwright test e2e/capturas.spec.ts`
Expected: os dois testes aparecem como `skipped` (sem `CAPTURAR`): o CI não gera imagem.

- [ ] **Step 4: Commit**

```bash
git add web/e2e/api-imitada.ts web/e2e/a11y.spec.ts web/e2e/capturas.spec.ts internal/httpapi/publicpage_test.go
git commit -m "test(painel): roteiro de captura das telas e exemplo da página pública"
```

### Task 8.3: As capturas, o README e o PR

**Files:**
- Create (geradas): `docs/design/painel-depois/*.png` (44 arquivos)
- Modify: `README.md`

**Interfaces:**
- Consumes: o roteiro da Task 8.2.
- Produces: nada.

- [ ] **Step 1: Gerar as capturas**

Run (na raiz, com o Docker de pé):

```bash
mkdir -p web/e2e/.tmp
STILLON_EXEMPLO_PUBLICA="$PWD/web/e2e/.tmp/status-publica.html" go test ./internal/httpapi/ -run TestGerarExemploDaPaginaPublica
cd web && npm run build && CAPTURAR=1 STILLON_EXEMPLO_PUBLICA="$PWD/e2e/.tmp/status-publica.html" npx playwright test e2e/capturas.spec.ts
```

Expected: os dois testes passam.

Confira:

```bash
ls docs/design/painel-depois | wc -l          # 44: 10 telas x 4 + status-publica x 4
file docs/design/painel-depois/painel-400-claro.png docs/design/painel-depois/painel-1280-escuro.png
# PNG image data, 400 x <altura> ... e 1280 x <altura> ...
```

Abra cada PNG e compare com o par de `docs/design/painel-antes/`: nenhuma tela em branco, carregando ou com a fonte do sistema (Fraunces nos títulos). Uma tela errada é defeito do roteiro ou da tela: corrija e gere de novo, não comite a imagem ruim.

- [ ] **Step 2: O README com as imagens novas**

Em `README.md`, na lista de "O que faz", troque as duas últimas linhas do item **Painel**:

```markdown
  [Capturas da proposta visual](docs/superpowers/reports/2026-10-04-primeira-onda.md#painel):
  temas claro/escuro e largura de 360 px, com dados de teste.
```

por:

```markdown
  Capturas na seção [Telas](#telas).
```

e, logo antes de `## Arquitetura`, acrescente:

```markdown
## Telas

O painel abre dizendo, numa frase, se algo está quebrado; os problemas vêm primeiro, em cartões
com a cor, a forma e o texto do estado. Os mesmos tokens e fontes desenham a página pública.

![Painel com dois serviços pedindo atenção, no tema claro](docs/design/painel-depois/painel-1280-claro.png)

| Celular, tema escuro | Página pública |
|---|---|
| ![Painel no celular, tema escuro](docs/design/painel-depois/painel-400-escuro.png) | ![Página de status pública com a faixa de estado e as barras de 90 dias](docs/design/painel-depois/status-publica-1280-claro.png) |

Todas as telas, a 400 e 1280 px e nos dois temas, ficam em
[`docs/design/painel-depois/`](docs/design/painel-depois/); o visual anterior, em
[`docs/design/painel-antes/`](docs/design/painel-antes/) e na tag `painel-antes-do-redesign`.
```

Confira: `sh deploy/ci/checar-nome.sh` (o nome antigo não pode voltar ao repositório, nem no README).

- [ ] **Step 3: Portões completos**

Run: `cd web && npm test && npm run typecheck && npm run lint && npm run build && npx playwright test`
Run (na raiz): `go build ./... && go vet ./... && make test`
Run: `sh deploy/ci/checar-nome.sh`
Expected: tudo verde.

- [ ] **Step 4: Commit e PR**

O PR tem código (o teste do sistema e o roteiro), então roda o CI completo: sem `[skip ci]`.

```bash
git add docs/design/painel-depois README.md
git commit -m "docs: capturas do painel depois do redesign e README com as telas novas"
git push -u origin feat/redesign-fechamento
gh pr create --title "feat(painel): fechamento do redesign (sistema em todo o src, capturas e README)" --body "$(cat <<'EOF'
PR 8 de 8 do redesign do painel. Spec: docs/superpowers/specs/2026-10-14-redesign-painel-design.md. Plano: docs/superpowers/plans/2026-10-14-redesign-painel-parte3.md.

- sistema.test.ts passa a cobrir todo o src (menos testes e src/test/): um arquivo novo entra sozinho na escala, no raio, nos pesos, nas sombras e nos tokens.
- Roteiro de captura no repositório: web/e2e/capturas.spec.ts (só com CAPTURAR=1), com a API imitada extraída do a11y.spec.ts para web/e2e/api-imitada.ts, e a página pública renderizada do template de verdade por TestGerarExemploDaPaginaPublica.
- 44 capturas em docs/design/painel-depois/ (10 telas a 400 e 1280 px, claro e escuro, mais a página pública nas quatro variações), no formato de docs/design/painel-antes/.
- README com a seção Telas.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Testes de tela que mudam nestes três PRs

| PR | Teste | O que acontece | Como fica |
|---|---|---|---|
| 6 | `web/src/features/canais/canais.test.tsx` | Procurava os textos com glifo (`'✓ Verificado'`, `'⏸ Pausado: os avisos são descartados'`…) | Textos sem glifo; "Pausado" e "Os avisos são descartados." (Task 6.2) |
| 6 | `web/e2e/a11y.spec.ts`, rotas | `getByText('⏸ Pausado: os avisos são descartados')` | `getByText('Os avisos são descartados.')` (Task 6.2) |
| 6 | `statuspage*.test.tsx` | Nada: a prévia continua `region` "Prévia aproximada", os "—" e o "Pausado" ficam; Subir/Descer/Tirar mantêm o nome acessível | Sem mudança |
| 6 | `login.test.tsx`, `main.test.tsx`, e2e | Nada: o `h1` continua "Entrar no StillOn" | Sem mudança |
| 7 | `publicpage_test.go`, `TestPresentationRules` | Testava `resumo` | O laço sai; os casos da frase vêm do JSON (Task 7.2) |
| 7 | `publicpage_test.go`, `TestPublicPageFitsInTenKilobytes` | 10 KB e 7 KB em texto puro | 10 KB comprimido (5 e 20 serviços) e 16 KB em texto (Tasks 7.3 e 7.4) |
| 7 | `publicpage_a11y_test.go` | Lia hex | Reescrito para OKLCH, formas e paridade (Task 7.4) |
| 7 | `publicpage_integration_test.go` | `"Há serviços fora do ar."` | `"1 serviço fora do ar."`; `font-src 'self'`; gzip (Tasks 7.1 a 7.3) |
| 8 | `web/e2e/a11y.spec.ts` | A API imitada morava nele | Importada de `web/e2e/api-imitada.ts` (Task 8.2) |

## Autorrevisão

- **Cobertura da spec:** Telas 4: botões em pirâmide com Apagar terciário (parte 1, e os testes de canais e status page desta), ícones Lucide no lugar dos glifos (6.2), "Pausado" mais visível que "Verificado" (6.2), checkbox da marca (`CaixaDeMarcar`, parte 1) e rádios de 44 px (6.1, 6.2, 6.5), `max-w` no cartão de formulário (6.1, 6.6), Tirar terciário (6.4), prévia que parece a pública (6.3, 6.4). Telas 5: marca, frase de tom, cartão nível 2, campos e botão de 44 px (6.7). Telas 6: tokens e fontes no CSS inline com nonce, tema escuro por `prefers-color-scheme`, nome em Fraunces, faixa em frase, barras com forma e cor a 3:1, legenda, uptime de 90 dias, histórico, "Monitorado pelo StillOn" (7.2, 7.4); CSP com `font-src 'self'` e teste (7.1). Testes da spec: CSP e template com faixa, legenda e classe de cada dia (7.1, 7.4). Ordem de entrega 8: capturas e README (8.2, 8.3).
- **Placeholders:** nenhum "TBD" nem "parecido com a Task N"; todo passo de código traz o código. Os dois passos condicionais (6.1 Step 1 e 6.6 Step 3) dependem do que a parte 2 entregou e trazem o comando que decide e a ação de cada resultado; a tabela da Task 8.1 dá a troca de cada classe que o teste pode reprovar.
- **Tipos e nomes:** `estiloDeCartao`, `Opcao`, `faixaDaPagina`/`ItemDaFaixa`/`Faixa`, `PreviaDaPagina`/`ItemDaPrevia`, `politicaDaPagina`, `faixa`/`bandModelo`/`fraseDaFaixa`, `comprimida`, `webui.AceitaGzip`, `paginaDeTeste(servicos, dia)`, `diasEfetivos`, `lerCSSDaPagina`, `imitarApi` têm o mesmo nome em todas as tasks. Os da parte 1 são usados com as assinaturas publicadas.
- **Review Focus:** cada uma das seis linhas tem teste na task dona (1 e 2 na 7.4; 3 na 7.4 e na 7.5; 4 na 7.1, 7.4 e 7.5; 5 na 7.4 e na 7.5; 6 na 6.8).
