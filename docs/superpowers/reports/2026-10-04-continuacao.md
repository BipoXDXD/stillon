# Continuidade da execução — 2026-10-04

Branch: `melhoria/plano-12-seguranca-painel`. Base/HEAD:
`cd816221c2398126a5eb2786ad28042d16c97a1c`. Três ondas locais autorizadas, com agentes
GPT-6.1 High. Código congelado; gates e revisões locais concluídos. O Plano 12
inteiro continua parcial. [Primeira onda e capturas](2026-10-04-primeira-onda.md);
[segunda onda: backup e trava de partições](2026-10-04-segunda-onda.md);
[terceira onda: tema e contrato de mutação](2026-10-04-terceira-onda.md).

## Estado para retomada

Alterações e arquivos novos preparados no índice; `AGENTS.md` inicial permanece
não versionado, preservado e fora do índice. Nenhum commit criado. A assinatura
GPG continua habilitada; a tentativa única da primeira onda retornou `No pinentry`
em execução não interativa. Conferência posterior encontrou `pinentry-gnome3` e
`pinentry-curses` instalados; a causa exata não foi reproduzida e o erro não comprova
ausência do pacote. O dono pediu
orientação para desbloquear e integrar por PR. A assinatura deve ocorrer no seu
terminal separado, com `GPG_TTY` atualizado, sem senha no chat nem desativação da
assinatura. Nenhuma nova tentativa de assinatura foi executada pelo agente.

Antes de continuar:

```sh
git branch --show-current
git status --short
git diff --cached --stat
```

Checkpoint atual, incluindo a preferência Linux e a orientação de integração:
`.superpowers/checkpoint-pre-integracao-linux-2026-10-04.patch`. O JSON ao lado registra
base, tamanho/hash do patch e inventário SHA256/modos dos arquivos preparados.
O parecer de recuperação independente é registrado separadamente em
`.superpowers/recuperacao-checkpoint-pre-integracao-linux-2026-10-04.md`.
Não aplicar sobre este checkout, que já contém a mudança. Noutra cópia limpa,
partir da base acima, usar `git apply --check` e depois `git apply --binary`.

O checkpoint/JSON `checkpoint-terceira-onda-2026-10-04` permanece intacto como
entrega anterior à orientação Linux, inclusive arquivos novos e as quatro PNG.
O checkpoint/JSON `checkpoint-primeira-onda-2026-10-04` permanece intacto como
estado anterior recuperável. Seu patch tem SHA256
`3d51fe8f491a5de0495c6f7826d66fea0ed329f6b30ec2a6c7ac2cb39632fcad`;
ele não contém as ondas seguintes. O checkpoint/JSON da segunda onda também foi
preservado: patch de 859.004 bytes, SHA256
`02c44afe09479b6b763eea056f8ebf996f35a9db31305eb11657f84e28f03167`,
recuperado com 386 arquivos/88 alterados iguais em bytes/modos. Logs `/tmp` e
pareceres ignorados ajudam a auditoria; os três relatórios preservam resultados e limites.
Depois dos gates da terceira onda, só documentação de operação/integração foi alterada;
o código funcional e as imagens testadas permanecem os mesmos.

## Integração e validação

[Rascunho local do PR combinado](2026-10-04-entrega-combinada-pr.md), sem envio ao
GitHub. Main só muda por PR. A mudança contém código e não deve usar `[skip ci]`.
Nenhum push, merge, deploy ou Actions remoto foi executado.
O corpo pronto para envio está também em `/tmp/stillon-integracao-20261004/pr-body.md`.
Criar o commit com `git commit -S` e conferir `git verify-commit HEAD` antes do push;
PR em rascunho também dispara CI, portanto preparar tudo antes de abrir.

Pacote atual: `go build ./... && go vet ./... && make test` com PG18/race, lint/sec,
runtime/tools e Trivy verdes. Terceira onda: 269 Vitest e 14/14 E2E na imagem atual.
VPS local completa passou na segunda onda; seus scripts/helper não mudaram depois.
Make test atual repetiu integração de backup/config. Suíte Go completa PG17 pertence
à primeira onda; teste novo de trava PG17/18, à segunda. Não alegar suíte atual PG17.

O painel é proposta implementada, com capturas claro/escuro e desktop/celular;
a aceitação estética aguarda o dono. Capturas e parte dos E2E usam fixtures.
Antes da migration 00011 na VPS, conferir pares TOTP pendentes inconsistentes;
investigar sem limpar credenciais automaticamente. A 00010 não repara checks
ou histerese antigos. Atualizar imagem e scripts do backup juntos; o protocolo
novo recusa imagem antiga antes de acessar a origem.

## Próximos trabalhos

- Dependabot #17 apto localmente; #18 tem correção dos tipos Node preparada,
  preservando TS7. [Parecer e patch](2026-10-04-dependabot.md). Manifests desta
  branch não foram atualizados; PRs seguem separados.
- Assinatura/proveniência de imagens, rotação de MASTER_KEY, recuperação de
  refresh com resposta perdida, cookies de dispositivo, licença/publicação e
  integração continuam pendentes. Decisões críticas ficam com o dono.
- Auth para sqlc continua no Plano 13. Lacunas restantes constam no
  [registro do Plano 12](../PENDENCIAS-PARA-O-PLANO-12.md).

## Hospedagem real

O [PR #26](https://github.com/BipoXDXD/stillon/pull/26), consultado na primeira onda,
registra blocos 0–2 feitos em 28/09 e bloco 3 como próximo. É evidência documental,
sem conferência atual na VPS. O dono definiu esta máquina Linux para a operação,
dispensou o fluxo Mac e esclareceu que pretende apagar os dados que colocou na
VPS Hostinger. O servidor considerado continua na Hostinger, administrado por
este Linux. Nenhuma exclusão ou reinstalação foi feita nesta execução.
Depois da limpeza, reconferir os passos afetados; se houver reinstalação, o
registro anterior é histórico e todos os passos aplicáveis precisam de novos
Confira. Preservar chaves/segredos no Bitwarden e, se houver dados a recuperar,
backup e MASTER_KEY. PRs #25/#26 de documentação permanecem separados.

Retomar com o dono pelo [runbook VPS](../../deploy/vps.md), um passo numerado e
seu Confira antes do seguinte. O [runbook Mac](../../deploy/mac.md) permanece
somente como referência histórica/opcional. Segredos no Bitwarden/VPS;
sudo/passphrase no terminal separado.

## Recursos locais

Pilha existente `stillon-app-1`, `stillon-db-1`, `stillon-seed-1`, rede
`stillon_default` e volume `stillon_pgdata` preservados. Ensaios removeram somente
recursos próprios; nenhuma limpeza global. Imagens das ondas anteriores e atuais, além de
`stillon-pr18-web:local` ficam para revisão. Imagens atuais:
`stillon:plano12-terceira-local` e `stillon:plano12-terceira-tools`, versão
`cd81622-plano12-terceira`. As tags da segunda onda também foram preservadas. Inventários detalhados em `.superpowers/`.
