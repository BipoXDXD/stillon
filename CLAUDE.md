# StillOn: instruções para o Claude

Monitor de uptime em Go (API, sondador, avisos) com painel React embutido. O README é a fonte de
verdade sobre o projeto; este arquivo diz como trabalhar nele e onde o trabalho parou.

## Como trabalhar

- **Tudo em português do Brasil**: respostas, perguntas, commits, PRs e documentação.
- Commits no formato do histórico: `tipo(escopo): descrição em português` (`fix(vps): …`,
  `docs: …`, `test: …`). Mudança na `main` só por PR.
- Antes de dizer que algo funciona, rode e mostre: `go build ./... && go vet ./... && make test`
  (o `make test` precisa do Docker, para o Postgres dos testes). Teste novo só vale depois de
  vê-lo falhar com o código sabotado: teste que passa sem testar nada já apareceu muitas vezes
  neste projeto.
- O CI completo gasta uns 30 min de Actions e o orçamento é curto: mudança só de documentação
  vai com `[skip ci]` no título do commit e do PR.
- O nome anterior do projeto não pode voltar ao repositório: `sh deploy/ci/checar-nome.sh`
  confere (o CI também).
- Nunca peça ao dono que cole senha, token ou chave privada no chat. Segredo vai para o
  gerenciador de senhas dele (Bitwarden, pasta "StillOn") e para a VPS, como o runbook manda.

## Hospedagem: guiada, um passo por vez

O dono nunca tinha usado VPS nem SSH. A hospedagem segue o runbook
[`docs/deploy/vps.md`](docs/deploy/vps.md) (num Mac, com [`docs/deploy/mac.md`](docs/deploy/mac.md)),
e o Claude **guia**: um passo numerado por vez, dizendo onde clicar ou o comando exato, o que deve
aparecer e o porquê em uma frase; espera ele confirmar e confere com o comando do **Confira**
antes do próximo. Antes de qualquer passo que gere um segredo, um passo próprio: "crie a entrada
no Bitwarden". Comandos com `sudo` ou passphrase vão num terminal separado (o `!` do chat do
Claude Code não tem TTY).

Lições da Hostinger: "invalid nameserver structure" = digitou na área **Nameservers** em vez de
**DNS records**; usuário Linux só em minúsculas.

## Onde parou (2026-09-25)

- `main` = PRs #1–#23 integrados (Planos 1–11 e a renomeação); imagem publicada em
  `ghcr.io/bipoxdxd/stillon`.
- **Runbook: blocos 0 e 1 feitos** (no computador Linux do dono). Já existem: VPS Hostinger KVM 2
  com Ubuntu 24.04 e a chave `stillon-admin` cadastrada; domínio `stillon.tech` com o registro `A`;
  Resend com `avisos.stillon.tech` verificado (remetente `StillOn <alertas@avisos.stillon.tech>`);
  check "StillOn" no healthchecks.io; todos os segredos no Bitwarden.
- **Próximo: bloco 2** (primeiro SSH, passos 2.1 a 2.7 numa sentada só). Num computador novo,
  primeiro a seção "Trocando de computador no meio do runbook" do `mac.md`.
- Depois da VPS no ar: redesign do painel (o dono acha o visual ruim). Os dois PRs abertos do
  Dependabot (#17, #18) esperam revisão.
