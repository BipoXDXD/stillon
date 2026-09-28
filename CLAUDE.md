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

## Onde parou (2026-09-28)

- `main` = PRs #1–#24 integrados (Planos 1–11 e a renomeação); imagem publicada em
  `ghcr.io/bipoxdxd/stillon`. PR #25 (spec sem o Fly, `.DS_Store`) aberto.
- **Runbook: blocos 0, 1 e 2 feitos.** Blocos 0 e 1 no Linux; o 2 no Mac (MacBook Air), depois da
  seção "Trocando de computador" do `mac.md`. Já existem: VPS Hostinger KVM 2 com Ubuntu 24.04
  (o IP está no `~/.stillon-vps` do Mac); domínio `stillon.tech` com o registro `A`; Resend com
  `avisos.stillon.tech` verificado (remetente `StillOn <alertas@avisos.stillon.tech>`); check
  "StillOn" no healthchecks.io; todos os segredos no Bitwarden.
- **A chave `stillon-admin` foi refeita no Mac** (a passphrase guardada não abria a antiga): chave,
  passphrase e a pública nova no Bitwarden e no painel. Usuário da VPS: `biposlayer`, com `sudo`.
  No Mac: `~/.stillon-vps` e `Host stillon` no `~/.ssh/config` (com `ServerAliveInterval 30`).
- Bloco 2 conferido: SSH só por chave e sem root (provado de fora), `fail2ban`, `ufw` e firewall
  do painel (só 22, 80 e 443; o `ping` não responde), atualizações automáticas com reinício às
  04:30. O terminal do navegador pede login depois do 2.7, e **root com a senha de root** entra.
- **Próximo: bloco 3** (Docker, `/opt/stillon`, segredos, chave do backup). Antes do 3.5, no Mac:
  `brew install age nmap`.
- Depois da VPS no ar: redesign do painel (o dono acha o visual ruim). Os dois PRs abertos do
  Dependabot (#17, #18) esperam revisão.
