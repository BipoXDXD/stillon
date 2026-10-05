# Dependabot: revisão local e correção preparada — 2026-10-04

Revisões em cópias isoladas, por agentes GPT-6.1 High. Nenhuma dependência do checkout
principal foi atualizada; nenhuma aprovação, comentário, push, merge ou CI remoto no GitHub.

## PR #17: x/net 0.58.0 → 0.59.0

**Apto no escopo local verificado.** O mínimo de Go 1.26.0 é atendido pela toolchain
1.26.8 do projeto. As versões transitivas novas já eram selecionadas. O único pacote
x/net no grafo do produto e de seus testes é IDNA; seu diretório é idêntico nas duas
versões. Alterações upstream de HTTP/2 e outros pacotes não entram nesses grafos.

Build/vet em modo readonly, testes com `-race` de prober, safedial, targets, auth e config,
`go mod verify` e govulncheck passaram. Govulncheck encontrou zero vulnerabilidades
alcançáveis/importadas; o aviso de módulo openpgp GO-2026-5932 já existia antes e esse
pacote não é importado. A cópia inicialmente sem Git falhou em metadados VCS/config;
foi corrigido apenas o laboratório e os checks afetados passaram. Isso não é RED de produto.

Origem do módulo conferida: `https://go.googlesource.com/net`, tag v0.59.0, commit
`540d04cfe5028e2655754591a4d3e08c586809f2`; hashes correspondem ao diff do PR.
A revisão local não substitui os checks remotos antes da integração.

## PR #18: TypeScript 7 e tipos Node

**O PR original precisa de ajuste; correção local pronta.** TypeScript 7.0.2 passou em
typecheck, lint, 251 testes, build e npm audit. Também passou o estágio web real no
Node 24 Alpine, com `npm ci --ignore-scripts` e opcionais preservados.

O agrupamento elevava `@types/node` 24.13.6 → 26.6.2, embora runtime e CI continuem
no Node 24. Prova temporária: importar `node:ffi` compila com tipos 26, é recusado com
tipos 24 (TS2307) e falha no Node 24 (ERR_UNKNOWN_BUILTIN_MODULE). O produto atual
não usa essa API; o achado é a perda da verificação de compatibilidade, não falha atual
do painel. A major dos tipos deve acompanhar o runtime descrito, como documenta
[DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped/blob/master/README.md).

A correção mantém TypeScript 7.0.2 e seus opcionais, retorna tipos Node a 24.13.6 e
undici-types a 7.18.2. Manifest e lockfile foram regenerados pelo npm, não à mão.
Uma segunda cópia instalada desse lock recusou a API incompatível e passou em
typecheck, lint, 251 testes, build e audit (zero vulnerabilidades reportadas).
Os oito assets gerados são idênticos aos do PR original. Integração de editor/tsserver
e ARM64 não foi testada; Alpine foi testado no PR original, sem repetir após alterar
apenas declarações de tipos.

## Patch preservado

[Correção do PR #18](2026-10-04-dependabot-18-correcao.patch), 2.162 bytes, aplica-se
**sobre o PR original**, não sobre esta branch. `git apply --check` passou na cópia
original. Somente `web/package.json` e `web/package-lock.json` são alterados.

SHA-256 do patch:

```text
f15577cc2c4f76cca8ec48ecc1d01cd2b1a0285825e24998fd66fb1ab15bc7ef
```

Na futura revisão do PR, aplicar o patch ao checkout correspondente e conferir os
checks de integração antes do merge. Os manifests principais desta onda continuam
nas versões anteriores; o patch é um artefato de revisão, não uma atualização implantada.

Relatórios detalhados locais: `.superpowers/revisao-dependabot-17-2026-10-04.md` e
`.superpowers/revisao-dependabot-18-2026-10-04.md`. As evidências necessárias à decisão
ficam resumidas aqui, sem depender da preservação dos diretórios temporários.
