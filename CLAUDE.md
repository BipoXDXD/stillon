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

Lições da execução guiada (2026-09-28):

- **Linhas curtas, um comando por bloco.** Heredoc, linha longa e um `.` solto no fim se
  perderam ou se partiram na colagem. No lugar do heredoc do runbook: `cd <pasta>` e vários
  `echo '…' | sudo tee -a <arquivo>`; destino no começo (`install -t .`).
- **Diga onde cada comando roda e o prompt esperado.** Uma sessão SSH caiu em silêncio e o
  comando seguinte rodou no Mac (que também tem `/etc/ssh/sshd_config.d`).
- **Senha e passphrase: colar do Bitwarden, nunca digitar.** A primeira chave SSH se perdeu com uma
  passphrase que não batia. Nada aparece ao colar, e as perguntas de confirmação pedem a colagem
  duas vezes. Copie a passphrase **antes** do `ssh`: o prazo de login do servidor é de 2 min.
- Configuração do Mac sem segredo (ex.: `~/.ssh/config`), o Claude escreve; o dono roda o que
  pede passphrase, `sudo` ou painel.

## Orientação atual (2026-11-05)

O dono trabalha no **Mac** (MacBook Air): a hospedagem e o desenvolvimento partem dele. A suíte Go
passa no macOS (PR #64).

**O StillOn está no ar** em <https://stillon.tech>, publicado em `a552515` (PR #144; run do
`Coolify release` verde e `readyz` 200 em 2026-11-05). **Todo merge na `main` publica sozinho** pelo workflow `Coolify release`
([`.github/workflows/coolify.yml`](.github/workflows/coolify.yml), descrição em
[`deploy/coolify/release/README.md`](deploy/coolify/release/README.md)): ele roda quando o `ci` de um
push na `main` termina com sucesso, confere o SHA exato e envia as imagens à VPS, que é
**compartilhada com outros projetos do dono** (GoblinShift, Duora). Por isso:

- **Merge é deploy.** Antes de dizer que algo está no ar, confira o run do `Coolify release` desse
  SHA (`gh run list --workflow coolify.yml`); nunca anuncie deploy sem conferência.
- **`[skip ci]` não publica.** Commit sem o `ci` da `main` não é publicado. Serve para
  documentação (entra com `gh pr merge --admin`); um PR com código nunca leva `[skip ci]`.
- O environment `production` do GitHub guarda as chaves do release (`COOLIFY_RELEASE_*`); o
  `deploy.yml` manual antigo ainda existe e usa segredos do repositório.
- A `main` tem ruleset (PR obrigatório e checks). Migrations vão até a 00022. A 00015 fica vaga para
  sempre, porque `storage.Migrate` recusa número menor que o aplicado; a vinculação Discord pega o
  próximo número livre ([spec de heartbeats](docs/superpowers/specs/2026-11-05-heartbeats-design.md)).

**VPS (conferido na API da Hostinger em 2026-11-05).** A produção é a `srv2070196`, com Coolify, paga
até 2027-10-17 e sem renovação automática. A anterior, `srv2008471`, foi cancelada de propósito e
venceu em 2026-10-25: não perguntar sobre a renovação dela.

**Redesign** (direção C, "Editorial claro", Fraunces + Inter, primária índigo,
[spec](docs/superpowers/specs/2026-10-14-redesign-painel-design.md)): concluído (PRs 1 a 8 na `main`, o
último é o #141). Painel antigo na tag `painel-antes-do-redesign` e em
`docs/design/painel-antes/`.

**Esperam decisão do dono** (lista completa, com o que decidir em cada uma, na
[continuidade de 2026-11-05](docs/superpowers/reports/2026-11-05-continuacao.md)): detalhes de
design do redesign, ligar o 2FA e mover o login para o Bitwarden, D1–D3 da
[vinculação Discord](docs/superpowers/plans/2026-10-05-vinculacao-discord.md), os segredos da VPS
no nível do repositório, o acesso SSH a partir deste Mac, a migração para Go 1.27.3 ou mais novo (a `main` fica no 1.26.10) e o `stash@{0}` local
(volume externo do compose). Não confundir planejamento aprovado com código pronto.

O `checar-vps.sh` não roda nativo no Mac (o `stillon-entrada` fixa um `PATH` de Ubuntu e o tar do
deploy é `amd64`): localmente, ele roda num Ubuntu descartável com o socket do Docker e
`DOCKER_DEFAULT_PLATFORM=linux/amd64`; o bloco 9 (backup) só fecha no job `vps` do CI.

O histórico (execução guiada na VPS, Task 6, deploys de 2026-10-14, revisões e redesign até o PR 3)
fica em [2026-10-14](docs/superpowers/reports/2026-10-14-continuacao.md) e
[2026-11-05](docs/superpowers/reports/2026-11-05-continuacao.md).

## Registro histórico (2026-09-28)

- `main` = PRs #1–#24 integrados (Planos 1–11 e a renomeação); imagem publicada em
  `ghcr.io/bipoxdxd/stillon`. PR #25 (spec sem o Fly, `.DS_Store`) aberto.
- **Runbook: blocos 0, 1 e 2 feitos.** Blocos 0 e 1 no Linux; o 2 no Mac (MacBook Air), depois da
  seção "Trocando de computador" do `mac.md`. Já existem: VPS Hostinger KVM 2 com Ubuntu 24.04
  (o IP está no `~/.stillon-vps` do Mac); domínio `stillon.tech` com o registro `A`; Resend com
  `avisos.stillon.tech` verificado (remetente `StillOn <alertas@avisos.stillon.tech>`); check
  "StillOn" no healthchecks.io; todos os segredos no Bitwarden.
- **A chave `stillon-admin` foi refeita no Mac** (a passphrase guardada não abria a antiga): chave,
  passphrase e a pública nova no Bitwarden e no painel. Usuário da VPS: o `EU` do `~/.stillon-vps`, com `sudo`.
  No Mac: `~/.stillon-vps` e `Host stillon` no `~/.ssh/config` (com `ServerAliveInterval 30`).
- Bloco 2 conferido: SSH só por chave e sem root (provado de fora), `fail2ban`, `ufw` e firewall
  do painel (só 22, 80 e 443; o `ping` não responde), atualizações automáticas com reinício às
  04:30. O terminal do navegador pede login depois do 2.7, e **root com a senha de root** entra.
- **Próximo: bloco 3** (Docker, `/opt/stillon`, segredos, chave do backup). Antes do 3.5, no Mac:
  `brew install age nmap`.
