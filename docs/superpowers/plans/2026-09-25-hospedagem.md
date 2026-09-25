# Vigilume — Plano 11: produção numa VPS (Hostinger)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pôr no repositório tudo o que leva o Vigilume ao ar numa VPS da Hostinger (compose de produção com Caddy, script de entrada da VPS com deploy/rollback/backup, vigia externo, workflows de deploy e backup por SSH, runbook para iniciante), testado no CI, e tirar o Fly.io do repositório. A execução na VPS de verdade é guiada com o dono, depois do merge.

**Architecture:** Três grupos com arquivos quase disjuntos. **vps** (`deploy/vps/`, `deploy/ci/checar-vps.sh`, `deploy/ci/checar-vigia.sh`, job `vps` do CI): a pilha caddy/app/db na rede fixa 172.30.0.0/24 e o `vigilume-entrada`, a única coisa que a chave de deploy alcança. **workflows** (`deploy/ci/vps.sh`, `deploy.yml`, `backup.yml`, `deploy/ci/checar-workflows.sh`, job `workflows`): o lado do runner, com host key fixada, e a remoção do Fly. **runbook** (`docs/deploy/vps.md`, README, pendências, roadmap): o passo a passo do zero ao ar. Os contatos entre grupos são os protocolos `deploy`/`backup` por SSH e os nomes fixos listados em "Interfaces" de cada parte.

**Tech Stack:** Go 1.26, PostgreSQL 18 (`postgres:18-alpine` fixado por digest), Caddy 2 (digest), Docker 29 com image store containerd e Compose ≥ 2.20, `age`, `crane` v0.22.1, `actionlint` v1.7.12, `shellcheck`, systemd (timer do vigia), healthchecks.io.

**Spec:** `docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`. Critério de pronto: a seção "Obrigações do Plano 11 (hospedagem)" de `docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`. Onde uma parte difere da spec, a tabela "Desvios" daquela parte prevalece.

**Validação prévia.** Cada parte foi implementada tarefa por tarefa numa cópia destacada de `aebc4d0`; os testes do passo 1 falharam sobre o estado anterior pela razão escrita; as sabotagens foram feitas e restauradas de backup (vps 57 vermelhas + 2 verdes declaradas; workflows 49 + 1; runbook 8 + 0); os gates passaram no estado final de cada cópia. Commits validados: vps `f7cec0e` `d857b66` `f3b81ac` `3d89558`; workflows `a2cba94` `697571b` `c22a868`; runbook `3138e89` `45cd5cb`.

## Global Constraints

- Go 1.26; módulo `github.com/BipoXDXD/vigilume`; `go test -race -count=1 ./...` verde; `sqlc diff`, `gofmt -l .`, `go vet` limpos; `make lint` e `make sec` verdes; `web/` inalterado e verde.
- Scripts: `shellcheck` limpo em `deploy/vps/*`, `deploy/ci/*.sh`, `deploy/backup/backup.sh`; `actionlint` limpo em todos os workflows; `bash deploy/ci/checar-vps.sh <imagem>`, `bash deploy/ci/checar-vigia.sh`, `sh deploy/ci/testar-vps.sh`, `sh deploy/ci/checar-workflows.sh` verdes.
- Nomes fixos entre as partes (exatos): `deploy/vps/{compose.prod.yaml, Caddyfile, initdb.sh, env.exemplo, vigilume-entrada, sudoers-vigilume-entrada, vigia.sh, vigilume-vigia.service, vigilume-vigia.timer, db/Dockerfile}`, `docs/deploy/vps.md`; rede `vigilume` 172.30.0.0/24 (caddy .2, app .3, db .4, sem IPv6); arquivos da VPS em `/opt/vigilume`.
- Protocolo SSH: `ssh deploy@VPS "deploy <sha40> <image-id sha256:…>"` com o tar (layout OCI sem nome) na entrada padrão → 0 saudável, 2 recusado, 3 imagem divergente, 4 não saudável com rollback, 1 outro erro; `ssh deploy@VPS backup` → só o cifrado do `age` na saída padrão, 0/3/4 do `backup.sh`, 1 outro erro. `<image-id>` = digest do manifesto linux/amd64; a VPS exige o image store containerd.
- Secrets: `VPS_HOST`, `VPS_SSH_KEY`, `VPS_HOST_KEY` (a linha inteira do `ssh_host_ed25519_key.pub`); variável `BACKUP_ENABLED`; artifact `vigilume-AAAAMMDD.dump.age`. Nenhum workflow aceita host key desconhecida (`StrictHostKeyChecking` `no`/`off`/`accept-new`, `ssh-keyscan`).
- Nada do Fly fora de `docs/superpowers/**` (perna 6 do `checar-workflows.sh`); o cabeçalho `Fly-Client-IP` e os testes `…BehindFly…` ficam (formato que o Caddy apaga).
- Textos em português; os exatos estão nas tarefas. Prova por sabotagem para toda asserção de ausência e todo teste que nasce verde.
- Nenhum agente acessa a VPS nem serviço externo. Integração só com todos os jobs verdes no GitHub.

## Execução e integração

Ordem de integração (as partes foram escritas em paralelo sobre `aebc4d0`): **vps V1..V4 → workflows W1..W3 → runbook R-1, R-2**. W3 (remoção do Fly) só fica verde junto com a remoção de `docs/deploy/fly.md` (R-1): o branch fica vermelho na perna 6 do `checar-workflows.sh` entre W3 e R-1, e é isso que se espera; os gates completos rodam depois da R-2.

Conflitos esperados e como resolver:
- `deploy/vps/db/Dockerfile` (vps × workflows, add/add): a linha `FROM` é a mesma; ficar com o comentário do vps (cita os cinco lugares do digest, inclusive o `compose.prod.yaml`).
- `.github/workflows/ci.yml`: o job `vps` e o `AGE_VERSION` (vps), o job `workflows` e a remoção do Fly (workflows); o `needs` do `publish` fica com os dois (`[lint, test, secrets, web, image, e2e, vps, workflows]`).
- `.github/dependabot.yml`: diretórios do vps (`/deploy/vps/db`, `directories: [/, /deploy/vps]`) e a limpeza do flyctl e o `actionlint` do workflows.
- `internal/config/compose_test.go`: o `TestPostgres18DigestIsTheSameEverywhere` do vps (lê `deploy/vps/db/Dockerfile` e `deploy/vps/compose.prod.yaml`); o `TestFlyctlVersionIsTheSameInDeployAndBackup` sai (workflows).
- `compose.yaml`, `deploy/compose/initdb.sql`, `deploy/backup/backup.sh` (mensagem do código 4, workflows), `README.md`, `.gitignore`, `internal/config/repositorio_test.go`: trechos distintos, manter os dois lados.
- Os marcadores `⟦…⟧` da R-1 são resolvidos contra os nomes finais do vps e do workflows (tabela "Marcadores" da parte runbook e "Interfaces" das partes vps e workflows); o `conferir-docs.sh` do Step 3 da R-1 falha enquanto sobrar um.

## Review Focus

As entradas de "Review Focus" de cada parte valem para a revisão final; as mais prováveis de morder o primeiro deploy:
- O passo do job `vps` que liga o image store containerd no runner nunca rodou fora do GitHub.
- O `crane pull --format oci` não pode pôr `org.opencontainers.image.ref.name` no `index.json` (a entrada recusaria com 3).
- SSH de verdade (`restrict,command=` + `sudo` com `env_keep` do `SSH_ORIGINAL_COMMAND` + tar binário) só é provado no primeiro deploy guiado.
- O terminal web da Hostinger depois do SSH endurecido, e se o firewall do painel é stateful.
- Achados já conhecidos das revisões antecipadas (workflows e runbook), para a onda de correção: opções `-o` do ssh exatamente uma vez com o valor esperado; prova da obrigação 1 no mesmo laço; `--force-recreate` do caddy; `docker logout`; "a VPS morreu" reusa a chave age do gerenciador e refaz o vigia.

---



---

# Parte: vps

# Plano 11 — parte "vps"

Redator: grupo **vps**. Spec: `docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`, §2 (topologia),
§3 (deploy: o `vigilume-entrada` e o `sudoers`), §4 (backup: o verbo `backup`), §6 (vigia externo) e §8 (testes no
CI). Obrigações do Plano 10 cobertas aqui (`docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`): **1** (Caddy tira
`Fly-Client-IP`, `X-Real-IP`, `CF-Connecting-IP`; sem `trusted_proxies`), **2** (`TRUSTED_PROXIES` = IP fixo do Caddy,
`CLIENT_IP_HEADER` vazio), **3** (só 80/443 do Caddy; sem IPv6), **4** (`GOMEMLIMIT` e `mem_limit`), **5** (compose de
produção sem seed, sem chave do repositório, segredos fora do git, `restart: unless-stopped`), a parte VPS da **6**
(backup e deploy na VPS) e a **7** (vigia externo). A verificação de cada uma **na VPS** é do runbook.

**Base:** `aebc4d0` (o `main` em `a61fc0f` mais o commit da spec). **Validação:** todo o código desta parte rodou
numa cópia destacada (`.superpowers/plano-11-spike/vps`), uma tarefa por commit, com Docker 29.8.1 (image store
containerd), Compose v5.5.1 e a imagem real do app construída do `Dockerfile` da base:

| Tarefa | Commit validado |
|---|---|
| V1 — compose de produção, Caddy, papéis do Postgres, digests e o job `vps` do CI | `f7cec0e` |
| V2 — `vigilume-entrada` com o verbo `deploy` (rollback, as 3 últimas) e o `sudoers` | `d857b66` |
| V3 — verbo `backup` (o `backup.sh` sem mudança; só o cifrado na saída padrão) | `f3b81ac` |
| V4 — vigia externo (`vigia.sh`, `vigilume-vigia.service`/`.timer`) | `3d89558` |

Os blocos abaixo são o conteúdo dos arquivos novos e os `git diff <anterior> <tarefa>` dos modificados, tirados
desses commits sem retoque. O teste de cada tarefa rodou sobre o estado anterior e falhou pela razão escrita no
passo 2. Cada sabotagem foi feita numa cópia de backup (pasta própria no scratchpad), rodada, e o arquivo restaurado
do backup; o estado final foi conferido sem marca de sabotagem (`git status` limpo e o teste verde de novo).
Sabotagens: **V1 23 vermelhas e 1 verde declarada; V2 17 vermelhas e 1 verde declarada (e 1 que levou código morto
embora); V3 8 vermelhas; V4 9 vermelhas** — 57 vermelhas, 2 verdes declaradas.

**Gates no estado final (`3d89558`):** `bash deploy/ci/checar-vps.sh <imagem do app>` verde (15 blocos "ok", ~50 s
com o cache de build quente); `bash deploy/ci/checar-vigia.sh` verde (com `systemd-analyze verify`); `shellcheck`
(0.11, pela imagem `koalaman/shellcheck:stable`) limpo em todos os scripts da parte; `actionlint` limpo no
`ci.yml`; `docker compose -f deploy/vps/compose.prod.yaml config` resolvido pelo próprio teste com um `.env`
completo (e recusando, com a mensagem de cada variável, o `env.exemplo` vazio); `gofmt -l`, `go vet ./...` e
`go test -race -count=1 ./...` verdes (Postgres 18 via testcontainers). Tudo o que o teste sobe (projeto
`vigilume-teste-vps`, imagens `vigilume-teste-vps*`, o Postgres descartável do backup) é derrubado no fim.

**A validação achou e fechou quatro furos:**
1. **Tar com nome passava pela checagem.** O tar do deploy é um diretório OCI empacotado com `tar -C dir .`, e os
   membros saem como `./index.json`; o `tar -xOf arquivo index.json` da primeira versão não os achava, e um tar com
   o nome numa anotação (`io.containerd.image.name`) era carregado e **etiquetava** a imagem antes da recusa. A
   leitura agora aceita `index.json` e `./index.json` (`ler_do_tar`), e o teste ganhou os dois tars anotados
   (sabotagens V2-S4 e V2-S4b).
2. **As recusas de argumento eram vacuamente verdes.** A primeira versão mandava `/dev/null` na entrada padrão; sem
   a checagem do SHA, o pedido caía em "nada chegou na entrada padrão" e saía 2 do mesmo jeito (V2-S1 e V2-S2
   ficaram verdes). Agora toda recusa manda o tar válido de `a`: só o pedido está errado.
3. **"As 3 últimas" pela data de build não eram as 3 últimas no ar.** O `docker image ls` do store containerd não
   lista por data de criação, e a imagem que nunca ficou saudável sobrevivia no lugar de uma que ficou. A poda
   passou a seguir o `historico` (um SHA por deploy saudável).
4. **Uma linha morta no rollback** (reetiquetar `atual` na anterior): a etiqueta só anda no sucesso, então ela já
   estava lá (V2-S14 ficou verde). Saiu.

---

## (a) Tarefas desta parte

1. **Tarefa V1** (`f7cec0e`) — `deploy/vps/compose.prod.yaml`, `Caddyfile`, `initdb.sh`, `env.exemplo`,
   `db/Dockerfile`; o teste `deploy/ci/checar-vps.sh` (config resolvida, pilha de pé com Caddy em modo local: portas,
   `Host`, IP do cliente, cabeçalhos removidos, banco fechado); o job `vps` no `ci.yml`; o digest do Postgres
   sincronizado (teste Go, `dependabot.yml`, comentários).
2. **Tarefa V2** (`d857b66`) — `deploy/vps/vigilume-entrada` (verbo `deploy`) e `deploy/vps/sudoers-vigilume-entrada`;
   o `checar-vps.sh` passa a subir a pilha pela entrada e testa recusas, imagem divergente, rollback, as 3 últimas,
   o store containerd e o `sudoers` num Ubuntu 24.04 descartável; o job `vps` troca o runner para o store
   containerd.
3. **Tarefa V3** (`f3b81ac`) — verbo `backup` da entrada; o teste confere o cifrado na saída padrão (e que decifra
   num dump que o `pg_restore` lê), os códigos 1 e 3 sem nada na saída, e que nada sobra; o job `vps` instala o
   `age`.
4. **Tarefa V4** (`3d89558`) — `deploy/vps/vigia.sh`, `vigilume-vigia.service`, `vigilume-vigia.timer` e o teste
   `deploy/ci/checar-vigia.sh` (curl falso; `systemd-analyze verify`); passo no job `vps`.

Ordem: V1 → V2 → V3 → V4, no mesmo worktree (V2 reescreve a subida do teste de V1; V3 e V4 acrescentam).

## (b) Interfaces entre grupos

**Produz (nomes e comportamentos exatos):**

| O quê | Detalhe | Quem consome |
|---|---|---|
| Protocolo `deploy` | `ssh deploy@VPS "deploy <sha40> <image-id>"` com o tar na entrada padrão. `<sha40>` = `^[0-9a-f]{40}$`; `<image-id>` = `^sha256:[0-9a-f]{64}$`, o digest do **manifesto linux/amd64**. Pedido só com minúsculas, dígitos, `:` e espaço. Códigos: **0** saudável; **2** pedido recusado (verbo, contagem, formato, argumento na linha de comando, entrada padrão vazia ou maior que 2 GiB); **3** imagem divergente (o `Loaded image ID` ≠ `<image-id>`, mais de uma imagem, ou o tar traz nome); **4** não ficou saudável em 90 s e o rollback foi feito (no primeiro deploy: o app fica parado); **1** outro erro (Docker sem store containerd, `load` falhou, trava ocupada por 10 min, rollback também falhou). Tudo o que a entrada escreve vai para a saída de **erro**; a saída padrão do `deploy` é vazia | workflows (`deploy.yml`), runbook |
| O tar do deploy | Layout OCI **sem nome**: sem `manifest.json` com `RepoTags` e sem as anotações `io.containerd.image.name` / `org.opencontainers.image.ref.name` no `index.json` (membros `index.json` ou `./index.json`). É o que sai de `crane pull --format oci <ref@digest> dir` empacotado (`tar -cf - -C dir .`). **O `crane pull` padrão (formato tarball, com `manifest.json` e `RepoTags`) é recusado com 3.** | workflows |
| Protocolo `backup` | `ssh deploy@VPS backup > vigilume-<data>.dump.age`: só o cifrado (age, chave pública em `/opt/vigilume/age-recipient`) na saída padrão; o log na de erro. Códigos: **0** conferido; **3** a cópia não passou nas checagens (nada na saída); **4** conferido e enviado, mas o disco dos volumes do Docker passou de 85% (guardar o artifact e falhar depois, como hoje); **1** outro erro (saída vazia ou cifrado incompleto: descartar); **2** `backup` com argumento | workflows (`backup.yml`), runbook |
| Store do Docker na VPS | O image store **containerd** (padrão de instalação nova do Docker 29): `docker info -f '{{.DriverStatus}}'` contém `io.containerd.snapshotter.v1`. Sem ele, todo `deploy` sai 1 com "o Docker não usa o image store containerd; veja docs/deploy/vps.md (Docker)" | runbook (verificação no passo do Docker, com essa seção) |
| Arquivos na VPS | `/opt/vigilume/`: `compose.prod.yaml`, `Caddyfile`, `initdb.sh`, `.env` (root 600), `age-recipient` e, **a mais que a lista comum**, `deploy/backup/backup.sh`, `deploy/backup/verificar.sql`, `deploy/compose/initdb.sql` (o layout do repositório: o `backup.sh` acha o `initdb.sql` por `../compose/`). A entrada cria `versao`, `versao-anterior`, **`historico`** e **`.entrada.trava`**. Fora dali: `/usr/local/sbin/vigilume-entrada` (de `deploy/vps/vigilume-entrada`, root 755), `/etc/sudoers.d/vigilume-entrada` (de `deploy/vps/sudoers-vigilume-entrada`, root 0440, conferir com `visudo -cf`), **`/usr/local/sbin/vigilume-vigia`** (de `deploy/vps/vigia.sh`, root 755), `/etc/systemd/system/vigilume-vigia.service` e `.timer` (`systemctl enable --now vigilume-vigia.timer`) | runbook |
| Formato de `versao`/`versao-anterior` | Uma linha com o SHA de 40 hex. `historico`: um SHA por deploy saudável, o mais novo no fim | runbook |
| Usuário `deploy` | O `sshd` roda o comando forçado pelo **shell de login** do usuário: ele precisa de um shell de verdade (`/bin/sh` ou `/bin/bash`), **não** `/usr/sbin/nologin`. O "sem shell interativo" vem do `restrict,command="sudo /usr/local/sbin/vigilume-entrada"`. O `sudoers` guarda só o `SSH_ORIGINAL_COMMAND`, só para a entrada, e não aceita argumento (`""`); `!use_pty` para o tar binário passar | runbook |
| Pacotes na VPS | `age` (o do apt: a entrada só usa `age -R`, e o `age-recipient` é uma linha `age1…` de qualquer versão), `curl`, `flock` (util-linux), plugin `docker compose` ≥ 2.20 (`up --wait --wait-timeout`), `openssl` para gerar os segredos | runbook |
| `/opt/vigilume/.env` | As variáveis do `deploy/vps/env.exemplo` (o teste confere que ele tem toda variável que o `compose.prod.yaml` lê, e nenhum valor): `VIGILUME_DOMINIO`, `POSTGRES_PASSWORD`, `VIGILUME_APP_PASSWORD`, `VIGILUME_BACKUP_PASSWORD` (`openssl rand -hex 32`: vão numa URL sem escape), `MASTER_KEY`, `JWT_SECRET`, `IP_HASH_KEY` (`openssl rand -base64 32`), `RESEND_API_KEY`, `EMAIL_FROM`, `HC_URL`. As senhas do Postgres só valem na primeira subida (depois, `ALTER ROLE` no banco e no `.env` juntos) | runbook |
| Operação à mão na VPS | `cd /opt/vigilume && sudo docker compose -f compose.prod.yaml <ps\|logs\|exec\|restart>` funciona **sem** `VIGILUME_SHA`: a imagem padrão é `vigilume:atual`, que a entrada move para a versão saudável. Projeto `vigilume`; rede `vigilume_vigilume`. Primeiro usuário: `sudo docker compose -f compose.prod.yaml exec app /vigilume user create --email <e-mail>` (a senha pelo terminal). Mudou o `Caddyfile`: `sudo docker compose -f compose.prod.yaml restart caddy` | runbook |
| Arquivos de `deploy/vps/` não chegam sozinhos | O deploy só manda a imagem. Um PR que mude `compose.prod.yaml`, `Caddyfile`, `initdb.sh`, a entrada, o `sudoers`, o vigia ou os scripts do backup (inclusive os PRs do Dependabot com o digest do Caddy e do Postgres) só vale na VPS depois de copiado (`scp`) e aplicado (`up -d`, `install`) à mão | runbook |
| DNS | Só registro **A**. As portas são publicadas só em IPv4 (`0.0.0.0:80`, `0.0.0.0:443`); sem **AAAA** | runbook |
| Linhas no `.github/workflows/ci.yml` | (1) no `env:` do topo, depois de `GITLEAKS_VERSION: v8.30.1`: o comentário e `AGE_VERSION: v1.3.2`; (2) o job **`vps`** inteiro, entre o `e2e` e o `publish` (bloco na Tarefa V4, estado final); (3) `needs: [lint, test, secrets, web, image, e2e]` → `needs: [lint, test, secrets, web, image, e2e, vps]` no `publish`; (4) o comentário da matriz do job `test` troca `deploy/fly/db/Dockerfile` por `deploy/vps/db/Dockerfile` e cita o `deploy/vps/compose.prod.yaml` | workflows (conflito de texto provável: é o mesmo arquivo) |
| `.github/dependabot.yml` | Ecossistema `docker`: `/deploy/fly/db` → `/deploy/vps/db` (comentário junto). `docker-compose`: `directory: /` → `directories: [/, /deploy/vps]` (o Dependabot casa `compose.prod.yaml` pela regex `(docker-)?compose(-[\w]+)?(?>\.[\w-]+)?\.ya?ml`, e passa a atualizar o digest do Caddy e do Postgres de produção). **O comentário do topo (flyctl) fica para o grupo workflows** | workflows |
| Sincronia do digest do Postgres | `TestPostgres18DigestIsTheSameEverywhere` (`internal/config/compose_test.go`) passa a ler `deploy/vps/db/Dockerfile` e `deploy/vps/compose.prod.yaml` e **não lê mais** `deploy/fly/db/Dockerfile`: o grupo workflows pode apagá-lo sem tocar no teste. O comentário do `imagemPadrao` (`internal/storage/testdb/testdb.go`) aponta para `deploy/vps/db/Dockerfile` | workflows |
| `deploy/vps/db/Dockerfile` | Uma linha `FROM postgres:18-alpine@sha256:…` (o `backup.yml` pode continuar lendo a imagem com `sed -n 's/^FROM //p'`, se ainda precisar dela) | workflows |
| Texto do `backup.sh` | O `backup.sh` (sem mudança, por regra) ainda diz "aumente o volume (fly volumes extend)" no código 4; a entrada imprime, depois, a linha certa para a VPS ("o disco da VPS passou de 85%: libere espaço (docs/deploy/vps.md, disco)") | runbook (seção "disco") |

**Consome de outros grupos:** o formato do tar e do `<image-id>` definidos com o grupo workflows (acima); os nomes
fixos das instruções comuns.

**Arquivos que outros grupos também tocam:** `.github/workflows/ci.yml` (workflows: as partes Fly),
`.github/dependabot.yml` (workflows: o comentário do flyctl e a remoção do Fly), `internal/config/compose_test.go`
(workflows: o `TestFlyctlVersionIsTheSameInDeployAndBackup`, que some com o Fly). Trechos desta parte: os listados
na tabela.

## (c) Desvios

| Spec / instruções comuns | Aqui | Motivo |
|---|---|---|
| "env_file /opt/vigilume/.env" | O `.env` é lido pela **interpolação** do compose (o `.env` do diretório do projeto), e cada serviço recebe só as suas variáveis em `environment:` | Com `env_file`, o Caddy receberia a `MASTER_KEY` e o app a senha do superusuário e a do backup. Os valores fixos (`TRUSTED_PROXIES`, `CLIENT_IP_HEADER`, `METRICS_ADDR`) ficam no compose: uma linha a mais no `.env` não os muda. O arquivo e o lugar continuam os mesmos |
| §3: "`docker compose up -d app`" | `docker compose up -d --wait --wait-timeout 90` da pilha inteira | No primeiro deploy o Caddy precisa subir junto (o app não depende dele); nos seguintes, só o app muda e o compose não recria os outros. O `--wait` é a espera do healthcheck (até 90 s) |
| Imagem `vigilume:${VIGILUME_SHA}` | `vigilume:${VIGILUME_SHA:-atual}`, `pull_policy: never`, e a etiqueta `vigilume:atual` movida pela entrada no sucesso | Sem o padrão, todo `docker compose logs/exec` à mão do dono exigiria a variável; sem o `pull_policy`, uma imagem ausente viraria um pull de `docker.io/library/vigilume` |
| Arquivos em `/opt/vigilume` | Mais `historico`, `.entrada.trava` e `deploy/{backup,compose}/` | A poda das 3 últimas precisa da ordem dos deploys; a trava serializa deploy e backup; o `backup.sh` sem mudança acha o `initdb.sql` por `../compose/` |
| "mantém as 3 últimas imagens" | As 3 últimas versões que ficaram **no ar** (o `historico`); a de um deploy que não ficou saudável sai na poda seguinte | Ver furo 3 acima |
| §2: "**só** 80 e 443" | `0.0.0.0:80:80` e `0.0.0.0:443:443` (só IPv4), e HTTP/3 desligado (`protocols h1 h2`) | Numa porta publicada em `[::]`, o Docker entrega o cliente IPv6 pelo `docker-proxy` com o IP do gateway: todos os clientes IPv6 no mesmo balde de rate limit. A 443/udp não é publicada, e o Caddy deixaria de anunciar um HTTP/3 que não chega |
| §4: "sobe um `postgres:18-alpine` descartável… roda o `backup.sh`" | O `backup.sh` roda **dentro** do Postgres descartável (`docker exec`), com os dados em `tmpfs`; a imagem é a do serviço `db` que está no ar | Um contêiner a menos, e o cliente é garantidamente da mesma versão do servidor |
| §6: timer "faz `curl`" | O serviço usa `DynamicUser=yes` e `LoadCredential=vigilume-env:/opt/vigilume/.env` | O vigia não roda como root nem lê o `.env` direto |
| `vigia.sh` | Instalado como `/usr/local/sbin/vigilume-vigia` | O mesmo padrão do `vigilume-entrada` |
| §8: "o rate limit e o `ip_hash` usam o IP real" | O observável é o `ip_hash` da sessão do login (HMAC-SHA256 recalculado no teste); o rate limit não é exercitado | O `ip_hash` distingue "IP real", "IP do Caddy" e "IP forjado" num login cada; o rate limit só mostraria colapso de balde com dezenas de requisições |
| §8: "o banco não aceita conexão de fora da rede" | De um contêiner em **outra rede** do Docker o `pg_isready` não alcança `172.30.0.4`, com controle positivo de dentro da rede; e o `db` sem `PortBindings` | O próprio host alcança os IPs das pontes do Docker (não é "de fora"); na VPS, o que vale é não haver porta publicada |
| Teste "o compose.prod.yaml sobe" | Com `deploy/ci/vps/compose.teste.yaml` por cima: outro nome de projeto, portas do Caddy em `127.0.0.1:18580/18543` e o nome de imagem `vigilume-teste-vps` | O runner (e a máquina de quem testa) pode ter a 80 ocupada, e o teste não pode mexer em imagens `vigilume:*` de verdade. IPs fixos, ambiente, limites e healthchecks são os de produção |
| Um script de teste | Dois: `deploy/ci/checar-vps.sh` (pilha, entrada, sudoers, backup) e `deploy/ci/checar-vigia.sh` (sem Docker) | O vigia não precisa da pilha, e roda em 1 s |

## (d) Decisões

| Decisão | Custo se errada |
|---|---|
| **D1.** O `<image-id>` é conferido pelo `Loaded image ID` do `docker load` (acordado com o grupo workflows), e a entrada **exige** o store containerd | Numa VPS com o store clássico, todo deploy sai 1 com a mensagem; o runbook confere o store antes do primeiro deploy |
| **D2.** Tar com nome é recusado **antes** do `load` | Se o `deploy.yml` mandar um tar com nome (o `crane pull` sem `--format oci`, ou um `docker save`), todo deploy sai 3 — visível e sem efeito colateral. O contrário (carregar e recusar depois) deixaria a etiqueta de outra imagem da máquina trocada |
| **D3.** A conferência do ID protege contra **engano de transporte** (imagem trocada, tar truncado), não contra quem tem a chave de deploy: essa escolhe o tar **e** o ID | Nenhum a mais: a garantia contra a chave vazada é a chave restrita a este script e o que ele aceita (uma imagem, com o healthcheck e o rollback) |
| **D4.** Primeiro deploy que não fica saudável: `stop app` e código 4 | O Caddy responde 502 até um deploy bom; nada de app doente reiniciando em loop |
| **D5.** Aviso de disco: a ocupação do sistema de arquivos de `DockerRootDir` (`df`), ≥ 85% → código 4 (o `backup.sh` mede só o banco contra o tamanho do disco, e segue igual) | Um alarme por disco cheio de imagem ou log, e não só de banco — é o que derruba a VPS |
| **D6.** Postgres descartável com `--memory 3g` e `--tmpfs … size=2g` | Um banco restaurado acima de ~2 GB faz o backup sair 1; hoje o banco é de MB. Revisar quando o `backup.sh` mostrar mais de 1 GB |
| **D7.** Limites: Caddy 256 MiB; app 1 GiB com `GOMEMLIMIT=768MiB`; db 1 GiB | Folga grande numa VPS de 8 GB (2,3 GB somados). O Argon2id (64 MiB × 2 simultâneos no KVM 2) cabe; um OOM do db apareceria no `restart` e no vigia |
| **D8.** Uma trava (`flock -w 600`) para deploy e backup | Um deploy espera até 10 min atrás de um backup (e vice-versa) em vez de disputar o banco durante o rollback |
| **D9.** Teto de 2 GiB para o tar do deploy | A imagem de hoje tem ~30 MB; o teto só impede encher o disco por engano |
| **D10.** `initdb.sh` sem `set -u` (o entrypoint faz `source` de `.sh` sem bit de execução) e com as senhas por `psql -v` | Nenhum: as guardas `: "${VAR:?}"` param o init com a mensagem |
| **D11.** O teste do sudoers roda num `ubuntu:24.04` com `apt-get install sudo` (rede no CI) | Se o apt do runner falhar, o job fica vermelho por fora do código; o erro mostra o fim do log |

## (e) Review Focus

- **O passo que troca o runner para o store containerd** (`/etc/docker/daemon.json` + `systemctl restart docker`,
  no job `vps`) não pôde ser rodado fora do GitHub: a máquina da validação já usa o containerd. Se o `jq` achar um
  `daemon.json` inesperado, o job fica vermelho no primeiro passo.
- **O formato real do tar do `deploy.yml`.** A entrada foi testada com um tar OCI montado a partir do `docker save`
  (membros `./…`, só o manifesto linux/amd64 no `index.json`), não com a saída do `crane`. Conferir no primeiro
  deploy de verdade (ou no teste do grupo workflows) que o `crane pull --format oci` não põe
  `org.opencontainers.image.ref.name` no `index.json`; se puser, o deploy sai 3 com "o tar traz nome de imagem".
- **SSH de verdade.** O `sudoers` foi provado num contêiner (`su deploy -c 'SSH_ORIGINAL_COMMAND=… sudo -n …'`), e
  a entrada com o ambiente limpo (`env -i`), mas não houve `sshd` com `restrict,command=` e um tar binário passando
  por `ssh | sudo | script`. O primeiro deploy guiado é a prova.
- **`docker compose up --wait` com o app em loop de restart**: medido (o deploy doente sai em poucos segundos com
  "unhealthy"/"exited"), mas é comportamento do Compose v5.5.1; um Compose mais velho na VPS pode esperar os 90 s.
- **O `backup.sh` roda como o usuário de quem chama** (`docker exec --user "$(id -u):$(id -g)"`): root na VPS. Sem
  isso, no teste (não-root), o `shred` não apagaria o dump em claro.
- **O IP que o Caddy vê é o do gateway da rede quando o cliente é o próprio host** (o `docker-proxy`); para
  clientes de fora, em IPv4, o Docker preserva o IP de origem. O teste só cobre o primeiro caso; o runbook confere
  o segundo (o `ip_hash` de um login de fora diferente do de um login da VPS).

---

## (f) Tarefas

### Tarefa V1: compose de produção, Caddy, papéis do Postgres, digests e o job `vps` do CI

Spec §2 e §8 (compose de produção no CI); obrigações 1–5. O `compose.prod.yaml` sobe `caddy`, `app` e `db` numa
rede `vigilume` com IPs fixos e sem IPv6; só o Caddy publica porta. O Caddy tem um site só para o domínio e tira os
três cabeçalhos de IP; o app confia no `X-Forwarded-For` só do IP do Caddy. O teste sobe a pilha com o Caddy em
modo local (`local_certs`) e confere cada obrigação por fora.

**Files:**
- Create: `deploy/vps/compose.prod.yaml`, `deploy/vps/Caddyfile`, `deploy/vps/initdb.sh`, `deploy/vps/env.exemplo`, `deploy/vps/db/Dockerfile`
- Create: `deploy/ci/checar-vps.sh`, `deploy/ci/vps/compose.teste.yaml`
- Modify: `internal/config/compose_test.go` (`TestPostgres18DigestIsTheSameEverywhere`), `internal/storage/testdb/testdb.go` (comentário), `.github/dependabot.yml`, `.github/workflows/ci.yml` (job `vps`, `needs` do `publish`, comentário da matriz)

**Interfaces:**
- Consumes: a imagem do app (`runtime`), `vigilume healthcheck`, `vigilume user create` (senha pelo stdin), `crypto.HashIP` (HMAC-SHA256 com `IP_HASH_KEY`), o cabeçalho `X-Trace-Id` (prova de que a resposta veio do app), o aviso "cabeçalho de IP do cliente ignorado: a conexão não vem de TRUSTED_PROXIES" (B3).
- Produces: os arquivos de `deploy/vps/` acima; `bash deploy/ci/checar-vps.sh <imagem>` (sai 0 com "vps conferida"); o job `vps`.

- [ ] **Step 1: Escreva os testes**

Crie `deploy/ci/vps/compose.teste.yaml`:

````yaml
# SÓ PARA O TESTE (deploy/ci/checar-vps.sh). NUNCA vai para a VPS.
#
# Por cima do deploy/vps/compose.prod.yaml: outro nome de projeto, as portas
# 80 e 443 do Caddy em portas altas do loopback (o runner ou a máquina de quem
# testa pode ter a 80 ocupada), e outro nome de imagem para o app, para o
# teste nunca mexer numa imagem vigilume:* de verdade. O resto (IPs fixos,
# ambiente do app, healthchecks, limites) é o de produção.
name: vigilume-teste-vps

services:
  caddy:
    ports: !override
      - "127.0.0.1:${TESTE_VPS_HTTP:-18580}:80"
      - "127.0.0.1:${TESTE_VPS_HTTPS:-18543}:443"
  app:
    image: vigilume-teste-vps:${VIGILUME_SHA:-atual}
````

Crie `deploy/ci/checar-vps.sh`:

````bash
#!/usr/bin/env bash
# Checagens da produção na VPS (Plano 11, spec §8), sem VPS: sobe o
# deploy/vps/compose.prod.yaml com o Caddy em modo local (certificado da CA
# interna, domínio vigilume.teste) e confere o que a revisão exigiu da
# hospedagem. O job "vps" do CI roda; localmente, da raiz do repositório:
#
#   bash deploy/ci/checar-vps.sh <imagem do app>     (ex.: vigilume:dev)
#
# O projeto do compose é "vigilume-teste-vps" e as portas do Caddy vão para o
# loopback (TESTE_VPS_HTTP, padrão 18580; TESTE_VPS_HTTPS, padrão 18543):
# deploy/ci/vps/compose.teste.yaml. A rede é a de produção (172.30.0.0/24):
# não rode numa máquina que já tenha o Vigilume de produção de pé.
# Derruba tudo no fim (TESTE_VPS_MANTER=1 deixa de pé, para investigar).
set -euo pipefail

IMAGEM_APP=${1:?uso: checar-vps.sh <imagem do app>}
RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
HTTP=${TESTE_VPS_HTTP:-18580}
HTTPS=${TESTE_VPS_HTTPS:-18543}
DOMINIO=vigilume.teste
PROJETO=vigilume-teste-vps
REPO_TESTE=vigilume-teste-vps
EMAIL=dono@vigilume.teste
SENHA=senha-do-teste-da-vps-2026

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
ok() { echo "ok: $*"; }

# O diretório que faz o papel de /opt/vigilume.
DIR=$(mktemp -d)
limpar() {
    if [ "${TESTE_VPS_MANTER:-}" = 1 ]; then
        echo "de pé em $DIR (projeto $PROJETO)" >&2
        return
    fi
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    docker image ls --format '{{.Repository}}:{{.Tag}}' "$REPO_TESTE" | xargs -r docker rmi >/dev/null 2>&1 || true
    rm -rf "$DIR"
}
trap limpar EXIT

cp "$RAIZ/deploy/vps/compose.prod.yaml" "$RAIZ/deploy/vps/Caddyfile" "$RAIZ/deploy/vps/initdb.sh" "$DIR/"
cp "$RAIZ/deploy/ci/vps/compose.teste.yaml" "$DIR/"
b64() { openssl rand -base64 32; }
IP_HASH_KEY=$(b64)
cat > "$DIR/.env" <<EOF
VIGILUME_DOMINIO=$DOMINIO
POSTGRES_PASSWORD=$(openssl rand -hex 32)
VIGILUME_APP_PASSWORD=$(openssl rand -hex 32)
VIGILUME_BACKUP_PASSWORD=$(openssl rand -hex 32)
MASTER_KEY=$(b64)
JWT_SECRET=$(b64)
IP_HASH_KEY=$IP_HASH_KEY
RESEND_API_KEY=
EMAIL_FROM=
HC_URL=
VIGILUME_CADDY_TESTE=local_certs
TESTE_VPS_HTTP=$HTTP
TESTE_VPS_HTTPS=$HTTPS
EOF
chmod 600 "$DIR/.env"

# O compose como a entrada o chama: o .env do diretório do projeto para a
# interpolação, mais o arquivo do teste por cima.
dc() { COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml" docker compose --project-directory "$DIR" "$@"; }
sql() { dc exec -T db psql -U postgres -d vigilume -Atq -c "$1"; }

# ---------------------------------------------------------------------------
# 1. O compose.prod.yaml sozinho (o que vai para a VPS), pela config resolvida.
# ---------------------------------------------------------------------------
cfg=$(cd "$DIR" && docker compose -f compose.prod.yaml config --format json)
q() { jq -r "$1" <<<"$cfg"; }

[ "$(q '[.services | to_entries[] | select(.value.ports != null and (.value.ports | length) > 0) | .key] | join(",")')" = caddy ] ||
    falhar "algum serviço além do caddy publica porta"
[ "$(q '[.services.caddy.ports[] | "\(.host_ip)|\(.published)|\(.target)|\(.protocol)"] | sort | join(" ")')" = "0.0.0.0|443|443|tcp 0.0.0.0|80|80|tcp" ] ||
    falhar "o caddy não publica exatamente 80 e 443 (tcp, IPv4): $(q '.services.caddy.ports')"
[ "$(q '.services | keys | sort | join(",")')" = app,caddy,db ] ||
    falhar "serviços do compose.prod: $(q '.services | keys | join(",")'), queria app, caddy e db"
for s in caddy app db; do
    [ "$(q ".services.$s.restart")" = unless-stopped ] || falhar "$s sem restart: unless-stopped"
    [ "$(q ".services.$s.mem_limit // empty")" != "" ] || falhar "$s sem mem_limit"
done
# GOMEMLIMIT abaixo do mem_limit do app (obrigação 4).
gomem=$(q '.services.app.environment.GOMEMLIMIT // empty')
limite=$(q '.services.app.mem_limit')
case "$gomem" in
    *MiB) gomem_b=$((${gomem%MiB} * 1024 * 1024)) ;;
    *GiB) gomem_b=$((${gomem%GiB} * 1024 * 1024 * 1024)) ;;
    *) gomem_b=0 ;;
esac
[ "$gomem_b" -gt 0 ] && [ "$gomem_b" -lt "$limite" ] ||
    falhar "GOMEMLIMIT '$gomem' não está abaixo do mem_limit do app ($limite bytes)"
# O IP do cliente (obrigação 2): TRUSTED_PROXIES é o IP fixo do Caddy.
caddy_ip=$(q '.services.caddy.networks.vigilume.ipv4_address')
[ "$caddy_ip" = 172.30.0.2 ] || falhar "IP do caddy é '$caddy_ip', queria 172.30.0.2"
[ "$(q '.services.app.environment.TRUSTED_PROXIES')" = "$caddy_ip/32" ] ||
    falhar "TRUSTED_PROXIES do app não é $caddy_ip/32"
[ "$(q '.services.app.environment.CLIENT_IP_HEADER')" = "" ] || falhar "CLIENT_IP_HEADER do app não é vazio"
[ "$(q '.services.app.environment.PUBLIC_BASE_URL')" = "https://$DOMINIO" ] || falhar "PUBLIC_BASE_URL não é https://<domínio>"
[ "$(q '.services.app.environment.METRICS_ADDR')" = off ] || falhar "METRICS_ADDR do app não é off"
[ "$(q '.services.app.networks.vigilume.ipv4_address')/$(q '.services.db.networks.vigilume.ipv4_address')" = 172.30.0.3/172.30.0.4 ] ||
    falhar "IPs fixos do app e do db não são 172.30.0.3 e 172.30.0.4"
[ "$(q '.networks.vigilume.enable_ipv6')" = false ] || falhar "a rede vigilume não desliga o IPv6"
[ "$(q '.networks.vigilume.ipam.config[0].subnet')" = 172.30.0.0/24 ] || falhar "sub-rede da rede vigilume não é 172.30.0.0/24"
# O Caddy não recebe segredo nenhum; o app não recebe a senha do superusuário.
[ "$(q '.services.caddy.environment | keys | sort | join(",")')" = VIGILUME_CADDY_TESTE,VIGILUME_DOMINIO ] ||
    falhar "o caddy recebe variáveis além do domínio e do modo de teste: $(q '.services.caddy.environment | keys | join(",")')"
[ "$(q '.services.app.environment | has("POSTGRES_PASSWORD") or has("VIGILUME_BACKUP_PASSWORD")')" = false ] ||
    falhar "o app recebe a senha do superusuário ou a do backup"
grep -q -- '-demo-\|ZGVtby12aWdpbHVtZS' "$RAIZ/deploy/vps/compose.prod.yaml" && falhar "o compose.prod.yaml tem chave de demonstração"
# O env.exemplo é o modelo do .env: sem valor nenhum, e com toda variável que
# o compose.prod.yaml lê (menos VIGILUME_SHA, da entrada, e o modo de teste).
grep -Ev '^(#|$)' "$RAIZ/deploy/vps/env.exemplo" | grep -v '=$' && falhar "o env.exemplo tem valor preenchido"
# shellcheck disable=SC2016 # o ${ é literal: é o que se procura
while read -r v; do
    case "$v" in VIGILUME_SHA | VIGILUME_CADDY_TESTE) continue ;; esac
    grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
ok "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis"

# ---------------------------------------------------------------------------
# 2. A pilha de pé, com o Caddy em modo local.
# ---------------------------------------------------------------------------
docker tag "$IMAGEM_APP" "$REPO_TESTE:t1"
if ! VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 >&2; then
    dc logs >&2
    echo "FALHOU: a pilha não subiu" >&2
    exit 1
fi

# Só o caddy publica, e só 80 e 443 (de dentro do contêiner).
publicadas=$(dc ps --format json | jq -rs '[.[] | .Service as $s | (.Publishers // [])[] | select(.PublishedPort > 0) | "\($s):\(.TargetPort)"] | sort | unique | join(" ")')
[ "$publicadas" = "caddy:443 caddy:80" ] || falhar "portas publicadas na pilha: '$publicadas'"
for s in app db; do
    [ "$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$(dc ps -q "$s")")" = "{}" ] ||
        falhar "$s tem porta publicada no host"
done
rede=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$(dc ps -q db)")
[ "$(docker network inspect -f '{{.EnableIPv6}}' "$rede")" = false ] || falhar "a rede $rede tem IPv6"

# A CA interna do Caddy, para o curl conferir o certificado de verdade.
dc exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > "$DIR/ca.crt"
cu() { curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "$DOMINIO:$HTTPS:127.0.0.1" "$@"; }
BASE=https://$DOMINIO:$HTTPS

# 3. /healthz pelo Caddy, com TLS conferido, chegando ao app (X-Trace-Id é do app).
resp=$(cu -D - -o /dev/null "$BASE/healthz" || true)
echo "$resp" | head -1 | grep -q ' 200' || falhar "/healthz pelo Caddy não deu 200: $(echo "$resp" | head -1)"
echo "$resp" | grep -qi '^x-trace-id:' || falhar "/healthz pelo Caddy não veio do app (sem X-Trace-Id)"
ok "/healthz pelo Caddy, com o certificado da CA interna"

# 4. Host estranho não chega ao app.
#    a) outro nome no SNI: sem certificado para ele, nada responde.
if curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "estranho.teste:$HTTPS:127.0.0.1" \
    -o /dev/null "https://estranho.teste:$HTTPS/healthz" 2>/dev/null; then
    falhar "https com SNI estranho.teste respondeu"
fi
#    b) o SNI do domínio com outro Host.
resp=$(cu -D - -o /dev/null -H 'Host: estranho.teste' "$BASE/healthz" || true)
echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host estranho pelo https chegou ao app"
#    c) http puro, com outro Host e com Host nenhum reconhecível (o IP).
for h in estranho.teste 127.0.0.1; do
    resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $h" "http://127.0.0.1:$HTTP/healthz" || true)
    echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host $h pelo http chegou ao app"
done
#    d) o domínio pelo http vira redirecionamento para o https, sem passar no app.
resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $DOMINIO" "http://127.0.0.1:$HTTP/healthz" || true)
echo "$resp" | head -1 | grep -q ' 308' || falhar "http no domínio não redirecionou (308): $(echo "$resp" | head -1)"
ok "Host estranho não chega ao app"

# 5. O IP que o app vê é o da conexão com o Caddy, com qualquer cabeçalho que
#    o cliente mande. O observável é o ip_hash da sessão criada pelo login:
#    HMAC-SHA256(IP_HASH_KEY, ip), o mesmo cálculo de crypto.HashIP.
hmac() { printf %s "$1" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$(printf %s "$IP_HASH_KEY" | base64 -d | od -An -tx1 | tr -d ' \n')" | sed 's/^.*= //'; }
printf '%s\n' "$SENHA" | dc exec -T app /vigilume user create --email "$EMAIL" >&2 ||
    { falhar "vigilume user create falhou"; exit 1; }
papeis=$(sql "SELECT string_agg(rolname || ':' || rolsuper::int || rolbypassrls::int || pg_has_role(oid, 'pg_read_all_data', 'MEMBER')::int, ' ' ORDER BY rolname) FROM pg_roles WHERE rolname IN ('vigilume_app', 'vigilume_backup')")
[ "$papeis" = "vigilume_app:000 vigilume_backup:011" ] ||
    falhar "papéis do initdb.sh: '$papeis', queria vigilume_app:000 vigilume_backup:011 (super, bypassrls, pg_read_all_data)"
entrar() {
    cu -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' "$@" \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}"
}
ultimo_hash() { sql "SELECT encode(ip_hash, 'hex') FROM sessions ORDER BY created_at DESC, id DESC LIMIT 1"; }

[ "$(entrar)" = 200 ] || falhar "login pelo Caddy não deu 200"
real=$(ultimo_hash)
[ -n "$real" ] || falhar "a sessão do login não tem ip_hash"
[ "$real" = "$(hmac "$caddy_ip")" ] && falhar "o app viu o IP do Caddy: TRUSTED_PROXIES não casa, e todo cliente divide um balde"
[ "$(entrar -H 'X-Real-IP: 1.2.3.4' -H 'Fly-Client-IP: 1.2.3.5' -H 'CF-Connecting-IP: 1.2.3.6' -H 'X-Forwarded-For: 1.2.3.7')" = 200 ] ||
    falhar "login com cabeçalhos forjados não deu 200"
forjado=$(ultimo_hash)
[ "$forjado" = "$real" ] || falhar "cabeçalhos do cliente mudaram o IP visto pelo app"
for ip in 1.2.3.4 1.2.3.5 1.2.3.6 1.2.3.7; do
    [ "$forjado" = "$(hmac "$ip")" ] && falhar "o app acreditou no IP forjado $ip"
done
# O aviso da B3 (X-Forwarded-For de fora de TRUSTED_PROXIES) não aparece.
dc logs app 2>&1 | grep -q 'não vem de TRUSTED_PROXIES' && falhar "o app avisou que o X-Forwarded-For não vem de TRUSTED_PROXIES"
ok "X-Real-IP, Fly-Client-IP, CF-Connecting-IP e X-Forwarded-For do cliente não mudam o IP visto"

# 6. O Caddy tira os três cabeçalhos: com um CLIENT_IP_HEADER posto por
#    engano, o app cai no IP do Caddy (e avisa), e não no valor do cliente.
for cab in X-Real-IP Fly-Client-IP CF-Connecting-IP; do
    cat > "$DIR/compose.cabecalho.yaml" <<EOF
services:
  app:
    environment:
      CLIENT_IP_HEADER: $cab
EOF
    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" VIGILUME_SHA=t1 \
        docker compose --project-directory "$DIR" up -d --wait --wait-timeout 120 app >&2; then
        falhar "o app não subiu com CLIENT_IP_HEADER=$cab"
        continue
    fi
    [ "$(entrar -H "$cab: 9.8.7.6")" = 200 ] || falhar "login com $cab não deu 200"
    h=$(ultimo_hash)
    [ "$h" = "$(hmac 9.8.7.6)" ] && falhar "o Caddy repassou o $cab do cliente"
    [ "$h" = "$(hmac "$caddy_ip")" ] || falhar "com CLIENT_IP_HEADER=$cab o app não caiu no IP do Caddy"
done
rm -f "$DIR/compose.cabecalho.yaml"
VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
ok "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP"

# 7. O banco não aceita conexão de fora da rede do compose. O controle
#    positivo (de dentro da rede) prova que a sonda funciona.
pg=$(docker inspect -f '{{.Config.Image}}' "$(dc ps -q db)")
docker run --rm --network "$rede" "$pg" pg_isready -h 172.30.0.4 -t 5 >/dev/null ||
    falhar "controle: de dentro da rede, o pg_isready não alcançou o banco"
if docker run --rm "$pg" pg_isready -h 172.30.0.4 -t 5 >/dev/null 2>&1; then
    falhar "de outra rede do Docker, o banco aceitou conexão"
fi
ok "o banco só aceita conexão de dentro da rede"

if [ "$falhas" -gt 0 ]; then
    dc logs >&2
    exit 1
fi
echo "vps conferida"
````

Aplique em `internal/config/compose_test.go` (o digest do Postgres passa a ser conferido no `db/Dockerfile` e no `compose.prod.yaml` da VPS; o do Fly sai da lista):

````diff
diff --git a/internal/config/compose_test.go b/internal/config/compose_test.go
index 2407152..6f4d5bf 100644
--- a/internal/config/compose_test.go
+++ b/internal/config/compose_test.go
@@ -30,9 +30,9 @@ func TestComposeUsesTheDemoKeys(t *testing.T) {
 }
 
 // O Postgres de produção, do compose, da perna 18 do CI e dos testes tem de
-// ser o MESMO digest (D29 do Plano 7). O Dependabot só atualiza o
-// deploy/fly/db/Dockerfile e o compose.yaml; as outras duas strings são a mão,
-// e este teste pega a que ficou para trás.
+// ser o MESMO digest (D29 do Plano 7). O Dependabot atualiza o
+// deploy/vps/db/Dockerfile, o deploy/vps/compose.prod.yaml e o compose.yaml;
+// as outras duas strings são a mão, e este teste pega a que ficou para trás.
 func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 	digest18 := regexp.MustCompile(`postgres:18-alpine@sha256:[0-9a-f]{64}`)
 	fontes := []struct {
@@ -40,7 +40,8 @@ func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 		// regex com um grupo que isola a linha onde o digest 18 deve estar.
 		linha *regexp.Regexp
 	}{
-		{"../../deploy/fly/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
+		{"../../deploy/vps/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
+		{"../../deploy/vps/compose.prod.yaml", regexp.MustCompile(`(?m)^\s+image: (postgres:\S+)$`)},
 		{"../../compose.yaml", regexp.MustCompile(`(?m)^\s+image: (postgres:\S+)$`)},
 		{"../../.github/workflows/ci.yml", regexp.MustCompile(`(?m)^\s+image: (postgres:18\S*)$`)},
 		{"../storage/testdb/testdb.go", regexp.MustCompile(`(?m)^const imagemPadrao = "([^"]+)"$`)},
@@ -64,7 +65,7 @@ func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 		if referencia == "" {
 			referencia, origem = img, f.arquivo
 		} else if img != referencia {
-			t.Errorf("%s usa %s, mas %s usa %s: atualize os quatro juntos", f.arquivo, img, origem, referencia)
+			t.Errorf("%s usa %s, mas %s usa %s: atualize os cinco juntos", f.arquivo, img, origem, referencia)
 		}
 	}
 }
````

- [ ] **Step 2: Rode e veja falhar**

```bash
bash deploy/ci/checar-vps.sh <imagem do app>      # ex.: docker build -t vigilume:dev . antes
go test -count=1 -run TestPostgres18DigestIsTheSameEverywhere ./internal/config/
```

Expected: o `checar-vps.sh` sai 1 logo no começo com `cp: não foi possível obter estado de '…/deploy/vps/compose.prod.yaml'` (e do `Caddyfile` e `initdb.sh`); o teste Go falha com `open ../../deploy/vps/db/Dockerfile: no such file or directory`.

- [ ] **Step 3: Implemente**

Crie `deploy/vps/compose.prod.yaml`:

````yaml
# Vigilume em produção numa VPS (Plano 11, spec §2). Fica em
# /opt/vigilume/compose.prod.yaml, ao lado do Caddyfile, do initdb.sh e do
# .env (segredos, root 600, fora do git; modelo em deploy/vps/env.exemplo).
#
# O compose.yaml da raiz do repositório NÃO vai para o servidor: ele tem o
# seed do usuário de demonstração e chaves públicas (obrigação 5).
#
# Quem sobe e troca a versão é o /usr/local/sbin/vigilume-entrada (verbo
# deploy), com VIGILUME_SHA no ambiente. Sem ela, a imagem é vigilume:atual,
# que a entrada move para a versão saudável depois de cada deploy: assim um
# `docker compose logs` ou `exec` à mão, em /opt/vigilume, funciona sem
# variável nenhuma.
#
# Os valores vêm do /opt/vigilume/.env pela interpolação do compose (o .env
# do diretório do projeto), e cada serviço recebe SÓ as variáveis de que
# precisa: o Caddy não vê segredo nenhum, e o app não vê a senha do
# superusuário nem a do backup. Os valores fixos (TRUSTED_PROXIES e cia.)
# ficam aqui, e não no .env: uma linha errada lá não os muda.
name: vigilume

# Rotação de log por serviço, além do daemon.json do runbook: um log sem
# teto enche o disco da VPS.
x-log: &log
  driver: json-file
  options:
    max-size: 10m
    max-file: "3"

services:
  caddy:
    # caddy:2 por digest (a tag fica no nome). O Dependabot atualiza este
    # digest e o do Postgres (ecossistema docker-compose, /deploy/vps).
    image: caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52
    # SÓ o Caddy publica porta, e só 80 e 443 em IPv4 (obrigação 3). O Docker
    # ignora o ufw para porta publicada: esta lista é o firewall que vale.
    # Em IPv4 de propósito: numa porta publicada em [::], o Docker entrega a
    # conexão IPv6 pelo docker-proxy, e todo cliente IPv6 chegaria ao Caddy
    # com o IP do gateway da rede — um balde de rate limit só para todos.
    # (Sem registro AAAA no DNS: runbook.)
    ports:
      - "0.0.0.0:80:80"
      - "0.0.0.0:443:443"
    environment:
      VIGILUME_DOMINIO: ${VIGILUME_DOMINIO:?defina VIGILUME_DOMINIO no /opt/vigilume/.env}
      # Vazio em produção. Só o teste do CI o preenche (local_certs).
      VIGILUME_CADDY_TESTE: ${VIGILUME_CADDY_TESTE:-}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      # Os certificados. Perder o volume é pedir tudo de novo ao Let's
      # Encrypt, que tem limite semanal por domínio.
      - caddy_data:/data
      - caddy_config:/config
    networks:
      vigilume:
        ipv4_address: 172.30.0.2
    mem_limit: 256m
    security_opt: ["no-new-privileges:true"]
    logging: *log
    restart: unless-stopped

  app:
    # A imagem que o vigilume-entrada carregou do docker save do deploy.yml
    # (a mesma que passou no Trivy), etiquetada localmente. pull_policy never:
    # sem ela, uma imagem ausente viraria um pull de docker.io/library/vigilume.
    image: vigilume:${VIGILUME_SHA:-atual}
    pull_policy: never
    command: ["--role=all"]
    environment:
      DATABASE_URL: postgres://vigilume_app:${VIGILUME_APP_PASSWORD:?defina VIGILUME_APP_PASSWORD no /opt/vigilume/.env}@172.30.0.4:5432/vigilume?sslmode=disable
      # Com https, o boot recusa as chaves de demonstração e liga o cookie
      # Secure (obrigação 5).
      PUBLIC_BASE_URL: https://${VIGILUME_DOMINIO:?defina VIGILUME_DOMINIO no /opt/vigilume/.env}
      CORS_ORIGIN: https://${VIGILUME_DOMINIO}
      # O IP fixo do Caddy, e o CLIENT_IP_HEADER vazio: o app lê o último
      # salto do X-Forwarded-For, que o Caddy escreve (obrigação 2).
      TRUSTED_PROXIES: 172.30.0.2/32
      CLIENT_IP_HEADER: ""
      METRICS_ADDR: "off"
      LOG_LEVEL: info
      # Abaixo do mem_limit (obrigação 4): o GC aperta antes do OOM killer. A
      # folga (256 MiB) é a memória que o GOMEMLIMIT não conta (pilhas das
      # goroutines, binário, buffers do kernel contados no cgroup).
      GOMEMLIMIT: 768MiB
      MASTER_KEY: ${MASTER_KEY:?defina MASTER_KEY no /opt/vigilume/.env}
      JWT_SECRET: ${JWT_SECRET:?defina JWT_SECRET no /opt/vigilume/.env}
      IP_HASH_KEY: ${IP_HASH_KEY:?defina IP_HASH_KEY no /opt/vigilume/.env}
      # Vazios, o canal de e-mail fica indisponível (o app sobe).
      RESEND_API_KEY: ${RESEND_API_KEY:-}
      EMAIL_FROM: ${EMAIL_FROM:-}
    healthcheck:
      # A imagem não tem shell: o próprio binário checa o /readyz (D5 do
      # Plano 7). O vigilume-entrada espera este healthcheck no deploy.
      test: ["CMD", "/vigilume", "healthcheck"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 30s
      start_interval: 500ms
    networks:
      vigilume:
        ipv4_address: 172.30.0.3
    mem_limit: 1g
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    logging: *log
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy

  db:
    # O MESMO digest de deploy/vps/db/Dockerfile, do compose.yaml, da perna 18
    # do ci.yml e do testdb (TestPostgres18DigestIsTheSameEverywhere).
    image: postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
    environment:
      # Superusuário só para o init e para a operação à mão; o app usa
      # vigilume_app, e o backup, vigilume_backup (initdb.sh).
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?defina POSTGRES_PASSWORD no /opt/vigilume/.env}
      VIGILUME_APP_PASSWORD: ${VIGILUME_APP_PASSWORD:?defina VIGILUME_APP_PASSWORD no /opt/vigilume/.env}
      VIGILUME_BACKUP_PASSWORD: ${VIGILUME_BACKUP_PASSWORD:?defina VIGILUME_BACKUP_PASSWORD no /opt/vigilume/.env}
    volumes:
      # Layout do 18: o volume é /var/lib/postgresql (D11 do Plano 7).
      - pgdata:/var/lib/postgresql
      # Só roda com o volume vazio (primeira subida). Trocar a senha no .env
      # depois disso NÃO muda a do banco: runbook.
      - ./initdb.sh:/docker-entrypoint-initdb.d/10-vigilume.sh:ro
    healthcheck:
      # Pelo TCP: durante o init o servidor temporário só escuta no socket.
      test: ["CMD", "pg_isready", "-h", "127.0.0.1", "-U", "vigilume_app", "-d", "vigilume"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 30s
      start_interval: 500ms
    # Nenhum `ports:` (obrigação 3): nem para o backup, que roda dentro da
    # rede (vigilume-entrada backup).
    networks:
      vigilume:
        ipv4_address: 172.30.0.4
    mem_limit: 1g
    logging: *log
    restart: unless-stopped

networks:
  vigilume:
    # Sem IPv6 (obrigação 3): o sondador não ganha um caminho por um prefixo
    # que o safedial não bloqueia.
    enable_ipv6: false
    ipam:
      config:
        - subnet: 172.30.0.0/24
          gateway: 172.30.0.1
          # Os IPs dinâmicos (o Postgres descartável do backup) saem da
          # metade de cima; os fixos (.2, .3, .4) ficam fora do sorteio.
          ip_range: 172.30.0.128/25

volumes:
  pgdata:
  caddy_data:
  caddy_config:
````

Crie `deploy/vps/Caddyfile`:

````caddyfile
# Caddy de produção do Vigilume (Plano 11, spec §2). Montado só-leitura em
# /etc/caddy/Caddyfile pelo deploy/vps/compose.prod.yaml; na VPS, fica em
# /opt/vigilume/Caddyfile.
#
# {$VIGILUME_DOMINIO} vem do /opt/vigilume/.env (pelo environment do serviço
# caddy no compose). {$VIGILUME_CADDY_TESTE} é vazio em produção; o teste do
# CI (deploy/ci/checar-vps.sh) põe ali `local_certs`, e o certificado sai da
# CA interna do Caddy em vez do Let's Encrypt.
{
	{$VIGILUME_CADDY_TESTE}

	# SEM trusted_proxies, de propósito (obrigação 1 do Plano 11): não há CDN
	# na frente, e sem a lista o Caddy SUBSTITUI o X-Forwarded-For que o
	# cliente mandou pelo IP de quem abriu a conexão. O app confia no
	# X-Forwarded-For só quando vem do IP fixo do Caddy (TRUSTED_PROXIES) e
	# lê o último salto; com trusted_proxies aqui, um cliente escolheria o
	# próprio IP em todo limitador e no ip_hash.
	servers {
		# Só HTTP/1.1 e HTTP/2: a 443/udp (HTTP/3) não é publicada no compose.
		protocols h1 h2
	}
}

# Um bloco de site só para o domínio. Uma requisição com outro Host (ou outro
# SNI) não casa com nada e não chega ao app: é a checagem de Host da revisão.
{$VIGILUME_DOMINIO} {
	reverse_proxy app:8080 {
		# O Caddy repassa todo cabeçalho do cliente e só reescreve os
		# X-Forwarded-*. Estes três carregam "o IP do cliente" em outras
		# plataformas; o app não os lê com CLIENT_IP_HEADER vazio, mas um
		# CLIENT_IP_HEADER posto por engano passaria a acreditar no valor do
		# cliente. Removidos aqui, o engano cai no IP do Caddy (um aviso no
		# log), e não no IP que o atacante escolheu (obrigação 1; B4 do
		# Plano 10).
		header_up -Fly-Client-IP
		header_up -X-Real-IP
		header_up -CF-Connecting-IP
	}
}
````

Crie `deploy/vps/initdb.sh`:

````sh
#!/bin/sh
# Papéis e banco do Vigilume em produção (Plano 11, spec §2). O entrypoint do
# postgres:18-alpine roda este arquivo UMA vez, na primeira subida (volume
# vazio), como o superusuário, com as senhas do /opt/vigilume/.env que o
# compose.prod.yaml passa ao serviço db. Nada de senha no repositório.
#
# - vigilume_app: comum (NOSUPERUSER NOBYPASSRLS), dono do banco. O boot
#   recusa um papel que atravesse a row-level security (storage.CheckRole).
# - vigilume_backup: BYPASSRLS + pg_read_all_data, só para o pg_dump (o do
#   dono falha com FORCE ROW LEVEL SECURITY). O DATABASE_URL do app nunca
#   aponta para ele.
#
# O entrypoint faz `source` de um .sh sem bit de execução: por isso nada de
# `set -u` aqui (ele valeria para o resto do entrypoint). As guardas abaixo
# fazem o papel dele.
: "${VIGILUME_APP_PASSWORD:?defina VIGILUME_APP_PASSWORD no /opt/vigilume/.env}"
: "${VIGILUME_BACKUP_PASSWORD:?defina VIGILUME_BACKUP_PASSWORD no /opt/vigilume/.env}"

# As senhas entram como variáveis do psql (:'app'), citadas por ele: nem
# aspas na senha quebram o SQL, nem a senha aparece na linha de comando.
psql -v ON_ERROR_STOP=1 --no-psqlrc --username "${POSTGRES_USER:-postgres}" --dbname postgres \
    -v app="$VIGILUME_APP_PASSWORD" -v backup="$VIGILUME_BACKUP_PASSWORD" <<'SQL'
CREATE ROLE vigilume_app LOGIN PASSWORD :'app'
    NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE vigilume OWNER vigilume_app;
CREATE ROLE vigilume_backup LOGIN PASSWORD :'backup'
    NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT pg_read_all_data TO vigilume_backup;
SQL
````

Crie `deploy/vps/env.exemplo`:

````sh
# Modelo do /opt/vigilume/.env (Plano 11). SEM VALORES, de propósito: cada
# segredo é gerado NA VPS (docs/deploy/vps.md) e copiado para o gerenciador de
# senhas do dono. O arquivo de verdade é do root, modo 600, e nunca entra no
# git. Perder a MASTER_KEY é perder os segredos cifrados no banco.
#
# O compose.prod.yaml lê este arquivo pela interpolação (${...}): cada
# serviço recebe só o que usa. Sem aspas e sem espaço em volta do "=".

# O domínio, sem https:// e sem barra (ex.: vigilume.exemplo.com.br).
VIGILUME_DOMINIO=

# Postgres: `openssl rand -hex 32` para cada um (hex: nada a escapar na URL).
# Só valem na PRIMEIRA subida do banco (initdb.sh); trocar depois é um
# ALTER ROLE no banco e aqui, juntos.
POSTGRES_PASSWORD=
VIGILUME_APP_PASSWORD=
VIGILUME_BACKUP_PASSWORD=

# Chaves do app: `openssl rand -base64 32` para cada uma (32 bytes).
MASTER_KEY=
JWT_SECRET=
IP_HASH_KEY=

# E-mail pelo Resend, com o domínio verificado. Vazio: sem canal de e-mail.
RESEND_API_KEY=
EMAIL_FROM=

# A URL de ping do healthchecks.io (vigia externo, vigilume-vigia.timer).
HC_URL=
````

Crie `deploy/vps/db/Dockerfile`:

````dockerfile
# O Postgres de produção da VPS: a imagem OFICIAL, a mesma (tag e digest) do
# deploy/vps/compose.prod.yaml. Este arquivo não se constrói: é o ponto fixo
# do digest para o Dependabot (ecossistema docker) e para quem precisa da
# imagem num script (sed -n 's/^FROM //p'). O MESMO digest está no
# compose.prod.yaml, no compose.yaml, na perna 18 do .github/workflows/ci.yml e
# no internal/storage/testdb; o TestPostgres18DigestIsTheSameEverywhere fica
# vermelho até todos andarem juntos.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
````

Aplique em `internal/storage/testdb/testdb.go`:

````diff
diff --git a/internal/storage/testdb/testdb.go b/internal/storage/testdb/testdb.go
index fc1d42a..cef8773 100644
--- a/internal/storage/testdb/testdb.go
+++ b/internal/storage/testdb/testdb.go
@@ -151,7 +151,7 @@ func iniciar(t *testing.T) {
 // imagemPadrao é a imagem de desenvolvimento e testes: a mesma de produção,
 // por digest (D29 do Plano 7); a CI escolhe a versão por TEST_POSTGRES_IMAGE.
 // O Dependabot não atualiza esta string: mantenha-a igual ao digest de
-// deploy/fly/db/Dockerfile e atualize as duas juntas (e a matriz do ci.yml).
+// deploy/vps/db/Dockerfile e atualize as duas juntas (e a matriz do ci.yml).
 const imagemPadrao = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"
 
 func imagem() string {
````

Aplique em `.github/dependabot.yml`:

````diff
diff --git a/.github/dependabot.yml b/.github/dependabot.yml
index 2209bd7..0bb5f2d 100644
--- a/.github/dependabot.yml
+++ b/.github/dependabot.yml
@@ -46,12 +46,11 @@ updates:
       interval: weekly
 
   # O Dockerfile do app (node, golang, distroless); o do Postgres de
-  # produção (que o fly.toml e o backup.yml usam: o Dependabot não lê
-  # nenhum dos dois) e o do Trivy do CI.
+  # produção (deploy/vps/db, o ponto fixo do digest) e o do Trivy do CI.
   - package-ecosystem: docker
     directories:
       - /
-      - /deploy/fly/db
+      - /deploy/vps/db
       - /deploy/ci/trivy
     schedule:
       interval: weekly
@@ -64,9 +63,12 @@ updates:
       - dependency-name: node
         update-types: ['version-update:semver-major']
 
-  # O Postgres do compose.yaml, o mesmo digest do de produção.
+  # O Postgres do compose.yaml, o mesmo digest do de produção; e o
+  # compose.prod.yaml da VPS (Caddy e Postgres).
   - package-ecosystem: docker-compose
-    directory: /
+    directories:
+      - /
+      - /deploy/vps
     schedule:
       interval: weekly
     # Versão MAIOR de Postgres e Node não vem por PR: o Postgres maior é dump
````

Aplique em `.github/workflows/ci.yml` (o job `vps` desta tarefa; V2–V4 acrescentam passos a ele):

````diff
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index 88ce9a1..f056cf1 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -99,9 +99,10 @@ jobs:
       fail-fast: false
       matrix:
         # Por digest, com a tag na string. O Dependabot não atualiza estas
-        # strings: a mão, junto com o digest de deploy/fly/db/Dockerfile (o
-        # 18 é o de produção, igual ao do Dockerfile, do compose.yaml e do
-        # testdb) e com o imagemPadrao de internal/storage/testdb.
+        # strings: a mão, junto com o digest de deploy/vps/db/Dockerfile (o
+        # 18 é o de produção, igual ao do Dockerfile, do compose.yaml, do
+        # deploy/vps/compose.prod.yaml e do testdb) e com o imagemPadrao de
+        # internal/storage/testdb.
         include:
           - label: postgres:17-alpine
             image: postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
@@ -327,12 +328,32 @@ jobs:
           path: web/test-results
           retention-days: 7
 
+  vps:
+    # A produção da VPS (Plano 11, spec §8) com a imagem que acabou de passar
+    # no Trivy: o deploy/vps/compose.prod.yaml com o Caddy em modo local, e o
+    # vigilume-entrada (deploy, rollback, backup) contra ele. Portas, Host,
+    # IP do cliente, banco fechado, recusas e backup só cifrado.
+    needs: image
+    runs-on: ubuntu-latest
+    timeout-minutes: 20
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          persist-credentials: false
+      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
+        with:
+          name: imagens
+          path: ${{ runner.temp }}
+      - run: gunzip -c "$RUNNER_TEMP/imagens.tar.gz" | docker load
+      - name: checagens da VPS
+        run: bash deploy/ci/checar-vps.sh "$IMAGEM:ci"
+
   publish:
     # Só depois de tudo verde, e só em push para a main ou tag v* deste
     # repositório (nunca em PR, nem num fork). O pacote do GHCR segue a
     # visibilidade do repositório: privado, por enquanto.
     if: github.event_name == 'push' && github.repository == 'BipoXDXD/vigilume'
-    needs: [lint, test, secrets, web, image, e2e]
+    needs: [lint, test, secrets, web, image, e2e, vps]
     runs-on: ubuntu-latest
     timeout-minutes: 30
     permissions:
````

- [ ] **Step 4: Rode e veja passar**

```bash
bash deploy/ci/checar-vps.sh <imagem do app>
go test -race -count=1 ./internal/config/
actionlint .github/workflows/ci.yml
shellcheck deploy/ci/checar-vps.sh deploy/vps/initdb.sh
```

Expected: `ok:` em "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis", "/healthz pelo Caddy, com o certificado da CA interna", "Host estranho não chega ao app", "X-Real-IP, Fly-Client-IP, CF-Connecting-IP e X-Forwarded-For do cliente não mudam o IP visto", "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP", "o banco só aceita conexão de dentro da rede", e `vps conferida` (~20 s); o resto limpo.

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

Todas com `bash deploy/ci/checar-vps.sh <imagem>`, salvo S20–S21 (`go test -run TestPostgres18DigestIsTheSameEverywhere ./internal/config/`).

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| S1 | `ports: ["127.0.0.1:15432:5432"]` no `db` | `algum serviço além do caddy publica porta`, `portas publicadas na pilha: 'caddy:443 caddy:80 db:5432'`, `db tem porta publicada no host` |
| S2 | `"0.0.0.0:80:80"` → `"80:80"` | `o caddy não publica exatamente 80 e 443 (tcp, IPv4)` |
| S3 | Tire o `restart: unless-stopped` do `app` | `app sem restart: unless-stopped` |
| S4 | Tire o `mem_limit` do `db` | `db sem mem_limit` |
| S5 | `GOMEMLIMIT: 2GiB` | `GOMEMLIMIT '2GiB' não está abaixo do mem_limit do app` |
| S6 | `TRUSTED_PROXIES: 10.9.9.9/32` | `TRUSTED_PROXIES do app não é 172.30.0.2/32`, `o app viu o IP do Caddy…`, `o app avisou que o X-Forwarded-For não vem de TRUSTED_PROXIES` |
| S7 | `CLIENT_IP_HEADER: X-Real-IP` | `CLIENT_IP_HEADER do app não é vazio`, `o app viu o IP do Caddy…` |
| S8 | Tire `header_up -X-Real-IP` do `Caddyfile` | `o Caddy repassou o X-Real-IP do cliente`, `com CLIENT_IP_HEADER=X-Real-IP o app não caiu no IP do Caddy` |
| S9 | `trusted_proxies static private_ranges` no bloco `servers` | **fica verde** (declarada): o cliente do teste (o gateway `172.30.0.1`) passa a ser confiável, o Caddy **acrescenta** o IP dele ao `X-Forwarded-For` do cliente, e o app lê o último salto — o mesmo IP. O comentário do `Caddyfile` e a checagem por inspeção seguram a regra; com um CDN de verdade na frente a história seria outra |
| S10 | Um site `:80 { reverse_proxy app:8080 }` a mais | `Host estranho.teste pelo http chegou ao app`, `Host 127.0.0.1 pelo http chegou ao app` |
| S11 | `enable_ipv6: true` | `a rede vigilume não desliga o IPv6`, `a rede vigilume-teste-vps_vigilume tem IPv6` |
| S12 | `MASTER_KEY: ${MASTER_KEY}` no `environment` do `caddy` | `o caddy recebe variáveis além do domínio e do modo de teste` |
| S13 | `vigilume_app` com `BYPASSRLS` no `initdb.sh` | `a pilha não subiu` (o boot recusa: "o papel do banco ignora row-level security") |
| S14 | `PUBLIC_BASE_URL: http://…` | `PUBLIC_BASE_URL não é https://<domínio>` |
| S15 | Tire o `GOMEMLIMIT` | `GOMEMLIMIT '' não está abaixo do mem_limit do app` |
| S16 | Tire `header_up -Fly-Client-IP` | `o Caddy repassou o Fly-Client-IP do cliente`, `…CLIENT_IP_HEADER=Fly-Client-IP…` |
| S17 | Tire `header_up -CF-Connecting-IP` | `o Caddy repassou o CF-Connecting-IP do cliente`, `…CLIENT_IP_HEADER=CF-Connecting-IP…` |
| S18 | Publique também `0.0.0.0:443:443/udp` | `o caddy não publica exatamente 80 e 443 (tcp, IPv4)` |
| S19 | `POSTGRES_PASSWORD` no `environment` do `app` | `o app recebe a senha do superusuário ou a do backup` |
| S20 | Mude um caractere do digest no `compose.prod.yaml` | `…compose.prod.yaml usa …, mas ../../deploy/vps/db/Dockerfile usa …: atualize os cinco juntos` |
| S21 | Mude um caractere do digest no `db/Dockerfile` | as outras quatro fontes acusam a diferença |
| S22 | Tire `JWT_SECRET=` do `env.exemplo` | `o env.exemplo não tem JWT_SECRET` |
| S23 | `EMAIL_FROM=alertas@exemplo.com` no `env.exemplo` | `o env.exemplo tem valor preenchido` |
| S24 | Tire o `GRANT pg_read_all_data TO vigilume_backup` | `papéis do initdb.sh: 'vigilume_app:000 vigilume_backup:010'…` |

Restaure de backup depois de cada uma e rode o Step 4 de novo.

- [ ] **Step 6: Commit**

```bash
git add deploy/vps deploy/ci/checar-vps.sh deploy/ci/vps internal/config/compose_test.go internal/storage/testdb/testdb.go .github/dependabot.yml .github/workflows/ci.yml
git commit -m "feat(vps): compose de produção com Caddy, papéis do Postgres e checagens no CI"
```

---

### Tarefa V2: `vigilume-entrada` com o verbo `deploy`, rollback e o `sudoers`

Spec §3. A entrada lê o pedido do `SSH_ORIGINAL_COMMAND` (nunca da linha de comando), recusa o que não for
exatamente `deploy <sha40> sha256:<64 hex>`, guarda o tar da entrada padrão num diretório só dela, recusa tar com
nome, carrega, confere o `Loaded image ID`, etiqueta `vigilume:<sha>`, sobe com `--wait` e, se não ficar saudável,
volta à versão anterior. O teste passa a subir a pilha **pela entrada** (uma cópia com as constantes trocadas).

**Files:**
- Create: `deploy/vps/vigilume-entrada` (modo 755), `deploy/vps/sudoers-vigilume-entrada`
- Modify: `deploy/ci/checar-vps.sh` (subida pela entrada; blocos 2 e 8), `.github/workflows/ci.yml` (store containerd no job `vps`)

**Interfaces:**
- Consumes: V1 (compose, Caddy, teste); o protocolo acordado com o grupo workflows.
- Produces: o protocolo `deploy` (códigos 0/2/3/4/1); `versao`, `versao-anterior`, `historico`, `.entrada.trava` em `/opt/vigilume`; a etiqueta `vigilume:atual`.

- [ ] **Step 1: Escreva os testes**

Aplique em `deploy/ci/checar-vps.sh`:

````diff
diff --git a/deploy/ci/checar-vps.sh b/deploy/ci/checar-vps.sh
index 6080bf3..ea25151 100644
--- a/deploy/ci/checar-vps.sh
+++ b/deploy/ci/checar-vps.sh
@@ -11,6 +11,9 @@
 # deploy/ci/vps/compose.teste.yaml. A rede é a de produção (172.30.0.0/24):
 # não rode numa máquina que já tenha o Vigilume de produção de pé.
 # Derruba tudo no fim (TESTE_VPS_MANTER=1 deixa de pé, para investigar).
+# O script escreve outros scripts e procura ${...} em arquivos: muita string
+# com $ literal entre aspas simples, de propósito.
+# shellcheck disable=SC2016
 set -euo pipefail
 
 IMAGEM_APP=${1:?uso: checar-vps.sh <imagem do app>}
@@ -35,8 +38,10 @@ limpar() {
         return
     fi
     dc down -v --remove-orphans >/dev/null 2>&1 || true
-    docker image ls --format '{{.Repository}}:{{.Tag}}' "$REPO_TESTE" | xargs -r docker rmi >/dev/null 2>&1 || true
-    rm -rf "$DIR"
+    for r in "$REPO_TESTE" "$REPO_TESTE-fonte"; do
+        docker image ls --format '{{.Repository}}:{{.Tag}}' "$r" | xargs -r docker rmi >/dev/null 2>&1 || true
+    done
+    rm -rf "$DIR" "$TARS"
 }
 trap limpar EXIT
 
@@ -66,6 +71,71 @@ chmod 600 "$DIR/.env"
 dc() { COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml" docker compose --project-directory "$DIR" "$@"; }
 sql() { dc exec -T db psql -U postgres -d vigilume -Atq -c "$1"; }
 
+# A entrada do teste: a de produção com as constantes trocadas (o diretório,
+# o nome da imagem, o compose do teste por cima, uma espera menor). Cada troca
+# tem de casar exatamente uma linha, ou o teste rodaria outra coisa.
+TARS=$(mktemp -d)
+ENTRADA=$TARS/vigilume-entrada
+trocar() {
+    [ "$(grep -cxF -- "$1" "$ENTRADA")" = 1 ] || { echo "FALHOU: a linha '$1' não está (uma vez) no vigilume-entrada" >&2; exit 1; }
+    awk -v de="$1" -v para="$2" '$0 == de { print para; next } { print }' "$ENTRADA" > "$ENTRADA.novo" && mv "$ENTRADA.novo" "$ENTRADA"
+}
+cp "$RAIZ/deploy/vps/vigilume-entrada" "$ENTRADA"
+trocar 'DIR=/opt/vigilume' "DIR=$DIR"
+trocar 'IMAGEM=vigilume' "IMAGEM=$REPO_TESTE"
+trocar 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"' 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml"'
+trocar 'ESPERA_S=90' 'ESPERA_S=45'
+trocar 'TMP_BASE=/var/tmp' "TMP_BASE=$TARS"
+# Como o sudo a roda: ambiente limpo, só o pedido do SSH. A saída padrão e a
+# de erro ficam em $TARS/saida e $TARS/erro; devolve o código.
+entrada() {
+    local pedido=$1 codigo=0
+    shift
+    env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="$pedido" bash "$ENTRADA" "$@" \
+        > "$TARS/saida" 2> "$TARS/erro" || codigo=$?
+    echo "$codigo"
+}
+# O tar como o deploy.yml o manda (crane pull --format oci): layout OCI com o
+# manifesto linux/amd64 da imagem, SEM nome. Imprime o digest do manifesto,
+# que é o image-id do protocolo.
+oci_tar() {
+    local img=$1 saida=$2 x y d f m
+    x=$(mktemp -d "$TARS/x.XXXX")
+    y=$(mktemp -d "$TARS/y.XXXX")
+    docker save "$img" | tar -xf - -C "$x"
+    d=$(jq -r '.manifests[0].digest' "$x/index.json")
+    while :; do
+        f="$x/blobs/${d/://}"
+        case "$(jq -r '.mediaType // empty' "$f")" in
+            *index* | *manifest.list*)
+                d=$(jq -r '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64")][0].digest' "$f") ;;
+            *) break ;;
+        esac
+    done
+    m=$d
+    mkdir -p "$y/blobs/sha256"
+    cp "$x/oci-layout" "$y/"
+    for b in "$m" $(jq -r '.config.digest, .layers[].digest' "$x/blobs/${m/://}"); do
+        cp "$x/blobs/${b/://}" "$y/blobs/sha256/"
+    done
+    printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"%s","digest":"%s","size":%s}]}' \
+        "$(jq -r .mediaType "$x/blobs/${m/://}")" "$m" "$(stat -c %s "$x/blobs/${m/://}")" > "$y/index.json"
+    tar -cf "$saida" -C "$y" .
+    rm -rf "$x" "$y"
+    echo "$m"
+}
+# Imagens diferentes do mesmo app (um LABEL a mais), e uma que nunca fica
+# saudável (o boot recusa ARGON2_MAX_CONCURRENT fora de 1–64; o compose não
+# define essa variável, e o ENV da imagem vale).
+fonte() {
+    local nome=$1 extra=$2
+    printf 'FROM %s\n%s\n' "$IMAGEM_APP" "$extra" | docker build -q -t "$REPO_TESTE-fonte:$nome" - >/dev/null
+    oci_tar "$REPO_TESTE-fonte:$nome" "$TARS/$nome.tar"
+}
+sha_de() { printf %s "$1" | sha1sum | cut -c1-40; }
+versao() { cat "$DIR/versao" 2>/dev/null || echo -; }
+imagem_do_app() { docker inspect -f '{{.Config.Image}}' "$(dc ps -q app)"; }
+
 # ---------------------------------------------------------------------------
 # 1. O compose.prod.yaml sozinho (o que vai para a VPS), pela config resolvida.
 # ---------------------------------------------------------------------------
@@ -113,22 +183,46 @@ grep -q -- '-demo-\|ZGVtby12aWdpbHVtZS' "$RAIZ/deploy/vps/compose.prod.yaml" &&
 # O env.exemplo é o modelo do .env: sem valor nenhum, e com toda variável que
 # o compose.prod.yaml lê (menos VIGILUME_SHA, da entrada, e o modo de teste).
 grep -Ev '^(#|$)' "$RAIZ/deploy/vps/env.exemplo" | grep -v '=$' && falhar "o env.exemplo tem valor preenchido"
-# shellcheck disable=SC2016 # o ${ é literal: é o que se procura
 while read -r v; do
     case "$v" in VIGILUME_SHA | VIGILUME_CADDY_TESTE) continue ;; esac
     grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
 done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
 ok "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis"
 
+# A entrada e o compose falam da mesma imagem.
+grep -qx 'IMAGEM=vigilume' "$RAIZ/deploy/vps/vigilume-entrada" &&
+    [ "$(q '.services.app.image')" = "vigilume:atual" ] ||
+    falhar "o IMAGEM do vigilume-entrada e o image do app no compose.prod.yaml não casam"
+
 # ---------------------------------------------------------------------------
-# 2. A pilha de pé, com o Caddy em modo local.
+# 2. A pilha de pé pelo vigilume-entrada, com o Caddy em modo local.
 # ---------------------------------------------------------------------------
-docker tag "$IMAGEM_APP" "$REPO_TESTE:t1"
-if ! VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 >&2; then
+ID_A=$(fonte a 'LABEL vigilume.teste=a')
+ID_DOENTE=$(fonte doente 'ENV ARGON2_MAX_CONCURRENT=999')
+SHA_A=$(sha_de a)
+SHA_DOENTE=$(sha_de doente)
+
+# 2a. Primeiro deploy que não fica saudável: sem versão anterior, o app para
+#     e o código é 4.
+c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
+[ "$c" = 4 ] || falhar "primeiro deploy doente saiu $c, queria 4: $(tail -3 "$TARS/erro")"
+grep -q 'sem versão anterior' "$TARS/erro" || falhar "primeiro deploy doente não disse que não havia versão anterior"
+[ "$(versao)" = - ] || falhar "primeiro deploy doente gravou a versão $(versao)"
+[ -z "$(dc ps -q app)" ] || falhar "primeiro deploy doente deixou o app de pé"
+
+# 2b. O deploy saudável.
+c=$(entrada "deploy $SHA_A $ID_A" < "$TARS/a.tar")
+if [ "$c" != 0 ]; then
+    cat "$TARS/erro" >&2
     dc logs >&2
-    echo "FALHOU: a pilha não subiu" >&2
+    echo "FALHOU: o deploy de $SHA_A saiu $c" >&2
     exit 1
 fi
+[ "$(versao)" = "$SHA_A" ] || falhar "depois do deploy, versao = $(versao)"
+[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "o app roda $(imagem_do_app), queria $REPO_TESTE:$SHA_A"
+[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "$REPO_TESTE:atual não aponta para a versão no ar"
+[ ! -s "$TARS/saida" ] || falhar "o deploy escreveu na saída padrão"
+ok "vigilume-entrada: primeiro deploy doente para o app (4); deploy saudável (0)"
 
 # Só o caddy publica, e só 80 e 443 (de dentro do contêiner).
 publicadas=$(dc ps --format json | jq -rs '[.[] | .Service as $s | (.Publishers // [])[] | select(.PublishedPort > 0) | "\($s):\(.TargetPort)"] | sort | unique | join(" ")')
@@ -210,7 +304,7 @@ services:
     environment:
       CLIENT_IP_HEADER: $cab
 EOF
-    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" VIGILUME_SHA=t1 \
+    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" VIGILUME_SHA=$SHA_A \
         docker compose --project-directory "$DIR" up -d --wait --wait-timeout 120 app >&2; then
         falhar "o app não subiu com CLIENT_IP_HEADER=$cab"
         continue
@@ -221,7 +315,7 @@ EOF
     [ "$h" = "$(hmac "$caddy_ip")" ] || falhar "com CLIENT_IP_HEADER=$cab o app não caiu no IP do Caddy"
 done
 rm -f "$DIR/compose.cabecalho.yaml"
-VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
+VIGILUME_SHA=$SHA_A dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
 ok "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP"
 
 # 7. O banco não aceita conexão de fora da rede do compose. O controle
@@ -234,6 +328,151 @@ if docker run --rm "$pg" pg_isready -h 172.30.0.4 -t 5 >/dev/null 2>&1; then
 fi
 ok "o banco só aceita conexão de dentro da rede"
 
+# ---------------------------------------------------------------------------
+# 8. vigilume-entrada: recusas, imagem divergente, rollback, as 3 últimas.
+# ---------------------------------------------------------------------------
+# 8a. Pedidos recusados com 2, antes de ler a entrada padrão ou mexer em
+#     qualquer coisa. O tar da entrada padrão é o de a, válido: o que está
+#     errado é só o pedido.
+recusa() {
+    local pedido=$1 motivo=$2 c
+    shift 2
+    c=$(entrada "$pedido" "$@" < "$TARS/a.tar")
+    [ "$c" = 2 ] || falhar "pedido '$pedido' ($motivo) saiu $c, queria 2"
+}
+recusa "" "vazio"
+recusa "reiniciar" "verbo desconhecido"
+recusa "DEPLOY $SHA_A $ID_A" "verbo em maiúsculas"
+recusa "deploy" "sem argumentos"
+recusa "deploy $SHA_A" "sem image-id"
+recusa "deploy $SHA_A $ID_A extra" "palavra a mais"
+recusa "deploy ${SHA_A:0:39} $ID_A" "SHA com 39 caracteres"
+recusa "deploy ${SHA_A}0 $ID_A" "SHA com 41 caracteres"
+recusa "deploy $(tr a-f A-F <<<"$SHA_A") $ID_A" "SHA em maiúsculas"
+recusa "deploy g${SHA_A:1} $ID_A" "SHA com letra fora do hex"
+recusa "deploy $SHA_A ${ID_A#sha256:}" "image-id sem sha256:"
+recusa "deploy $SHA_A ${ID_A:0:70}" "image-id curto"
+recusa "deploy $SHA_A;id $ID_A" "caractere de shell"
+recusa 'deploy $(id) '"$ID_A" "substituição de comando"
+# Com um pedido e um tar válidos: só o argumento é recusado.
+c=$(entrada "deploy $SHA_A $ID_A" backup < "$TARS/a.tar")
+[ "$c" = 2 ] || falhar "argumento na linha de comando saiu $c, queria 2"
+[ "$(versao)" = "$SHA_A" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "um pedido recusado mexeu na versão"
+c=$(entrada "deploy $SHA_A $ID_A" < /dev/null)
+[ "$c" = 2 ] || falhar "deploy sem nada na entrada padrão saiu $c, queria 2"
+ok "vigilume-entrada: pedidos malformados recusados com 2"
+
+# 8b. Imagem divergente: o tar de uma imagem com o ID de outra. Nada é
+#     etiquetado e a versão no ar não muda.
+ID_B=$(fonte b 'LABEL vigilume.teste=b')
+SHA_B=$(sha_de b)
+c=$(entrada "deploy $SHA_B $ID_A" < "$TARS/b.tar")
+[ "$c" = 3 ] || falhar "tar de b com o ID de a saiu $c, queria 3"
+docker image inspect "$REPO_TESTE:$SHA_B" >/dev/null 2>&1 && falhar "a imagem divergente foi etiquetada"
+c=$(entrada "deploy $SHA_B sha256:$(printf '0%.0s' {1..64})" < "$TARS/b.tar")
+[ "$c" = 3 ] || falhar "ID inexistente saiu $c, queria 3"
+# Um tar COM nome (o docker save comum) é recusado antes do load: o nome
+# etiquetaria outra imagem da máquina.
+docker tag "$REPO_TESTE-fonte:b" "$REPO_TESTE-fonte:b-com-nome"
+docker save "$REPO_TESTE-fonte:b-com-nome" -o "$TARS/b-com-nome.tar"
+docker rmi "$REPO_TESTE-fonte:b-com-nome" >/dev/null
+c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-com-nome.tar")
+[ "$c" = 3 ] || falhar "tar com nome saiu $c, queria 3"
+docker image inspect "$REPO_TESTE-fonte:b-com-nome" >/dev/null 2>&1 && falhar "o tar com nome foi carregado (a etiqueta voltou)"
+# O mesmo vale para um tar OCI sem manifest.json com o nome numa anotação do
+# index.json (as duas que o containerd lê).
+for anot in io.containerd.image.name org.opencontainers.image.ref.name; do
+    rm -rf "$TARS/anotado" && mkdir "$TARS/anotado"
+    tar -xf "$TARS/b.tar" -C "$TARS/anotado"
+    jq --arg a "$anot" --arg n "docker.io/library/$REPO_TESTE-fonte:anotado" \
+        '.manifests[0].annotations = {($a): $n}' "$TARS/anotado/index.json" > "$TARS/index.json"
+    mv "$TARS/index.json" "$TARS/anotado/index.json"
+    tar -cf "$TARS/b-anotado.tar" -C "$TARS/anotado" .
+    c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-anotado.tar")
+    [ "$c" = 3 ] || falhar "tar com $anot saiu $c, queria 3"
+    docker image inspect "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 && falhar "o tar com $anot foi carregado com o nome"
+    docker rmi "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 || true
+done
+[ "$(versao)" = "$SHA_A" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "uma imagem divergente mexeu na versão"
+ok "vigilume-entrada: imagem divergente e tar com nome recusados com 3"
+
+# 8c. Rollback: um deploy que não fica saudável volta à versão anterior.
+c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
+[ "$c" = 4 ] || falhar "deploy doente saiu $c, queria 4: $(tail -3 "$TARS/erro")"
+grep -q 'rollback feito' "$TARS/erro" || falhar "o deploy doente não disse que fez o rollback"
+grep -q 'ARGON2_MAX_CONCURRENT' "$TARS/erro" || falhar "o deploy doente não mostrou o log do app"
+[ "$(versao)" = "$SHA_A" ] || falhar "depois do rollback, versao = $(versao)"
+[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "depois do rollback o app roda $(imagem_do_app)"
+[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "depois do rollback, atual não é a versão no ar"
+[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do rollback o app não está saudável"
+[ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz")" = 200 ] || falhar "depois do rollback, /healthz pelo Caddy não deu 200"
+ok "vigilume-entrada: deploy doente volta à versão anterior (4)"
+
+# 8d. Deploys seguidos: versao-anterior anda, e ficam as 3 últimas imagens
+#     (as 3 últimas que ficaram no ar; a doente sai), fora a etiqueta atual.
+ID_C=$(fonte c 'LABEL vigilume.teste=c')
+SHA_C=$(sha_de c)
+ID_D=$(fonte d 'LABEL vigilume.teste=d')
+SHA_D=$(sha_de d)
+for par in "b $SHA_B $ID_B" "c $SHA_C $ID_C" "d $SHA_D $ID_D"; do
+    read -r nome sha id <<<"$par"
+    c=$(entrada "deploy $sha $id" < "$TARS/$nome.tar")
+    [ "$c" = 0 ] || falhar "deploy de $nome saiu $c: $(tail -3 "$TARS/erro")"
+done
+[ "$(versao)" = "$SHA_D" ] || falhar "depois de b, c e d, versao = $(versao)"
+[ "$(cat "$DIR/versao-anterior" 2>/dev/null)" = "$SHA_C" ] || falhar "versao-anterior não é a de c"
+restantes=$(docker image ls "$REPO_TESTE" --format '{{.Tag}}' | grep -vx atual | sort | tr '\n' ' ')
+esperadas=$(printf '%s\n' "$SHA_B" "$SHA_C" "$SHA_D" | sort | tr '\n' ' ')
+[ "$restantes" = "$esperadas" ] || falhar "imagens que sobraram: '$restantes', queria as de b, c e d"
+# O mesmo SHA de novo (redeploy) é aceito e não mexe na anterior.
+c=$(entrada "deploy $SHA_D $ID_D" < "$TARS/d.tar")
+[ "$c" = 0 ] || falhar "redeploy de d saiu $c: $(tail -3 "$TARS/erro")"
+[ "$(cat "$DIR/versao-anterior")" = "$SHA_C" ] || falhar "o redeploy de d mudou a versao-anterior"
+ok "vigilume-entrada: versao-anterior e as 3 últimas imagens"
+
+# 8e. Docker sem o image store containerd: recusa com 1. Um docker falso na
+#     frente do PATH responde ao `docker info` como o store clássico.
+mkdir -p "$TARS/bin"
+printf '#!/bin/sh\nif [ "$1" = info ]; then echo "[[Backing Filesystem extfs] [Supports d_type true]]"; exit 0; fi\nexec %s "$@"\n' \
+    "$(command -v docker)" > "$TARS/bin/docker"
+chmod +x "$TARS/bin/docker"
+sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-path"
+c=0
+env -i PATH="$TARS/bin:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_A $ID_A" \
+    bash "$TARS/entrada-path" < "$TARS/a.tar" > /dev/null 2> "$TARS/erro" || c=$?
+[ "$c" = 1 ] || falhar "sem o store containerd a entrada saiu $c, queria 1"
+grep -q 'containerd' "$TARS/erro" || falhar "sem o store containerd a entrada não disse por quê"
+ok "vigilume-entrada: recusa o Docker sem o image store containerd"
+
+# 8f. O sudoers: sintaxe (visudo) e efeito, num Ubuntu 24.04 descartável com
+#     o usuário deploy e uma entrada falsa que mostra o que recebeu.
+printf '#!/bin/sh\necho "args=$# pedido=${SSH_ORIGINAL_COMMAND:-} outra=${OUTRA:-} usuario=$(id -un)"\n' > "$TARS/entrada-falsa"
+cat > "$TARS/sudo.sh" <<'FIM'
+set -eu
+apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo >/dev/null
+useradd -m -s /bin/bash deploy
+install -m 0755 /t/entrada-falsa /usr/local/sbin/vigilume-entrada
+install -m 0440 /t/sudoers /etc/sudoers.d/vigilume-entrada
+visudo -cf /etc/sudoers.d/vigilume-entrada
+visudo -c >/dev/null
+como_deploy() { su deploy -c "$1" 2>&1 || echo "negado"; }
+echo "A $(como_deploy 'SSH_ORIGINAL_COMMAND="deploy abc" OUTRA=x sudo -n /usr/local/sbin/vigilume-entrada')"
+echo "B $(como_deploy 'sudo -n /usr/local/sbin/vigilume-entrada backup' | tail -1)"
+echo "C $(como_deploy 'sudo -n /bin/sh -c id' | tail -1)"
+echo "D $(como_deploy 'sudo -n /usr/bin/id' | tail -1)"
+FIM
+cp "$RAIZ/deploy/vps/sudoers-vigilume-entrada" "$TARS/sudoers"
+chmod 644 "$TARS/entrada-falsa" "$TARS/sudo.sh" "$TARS/sudoers"
+chmod 755 "$TARS"
+docker run --rm -v "$TARS:/t:ro" ubuntu:24.04 bash /t/sudo.sh > "$TARS/sudo.log" 2>&1 ||
+    falhar "o teste do sudoers não rodou: $(tail -5 "$TARS/sudo.log")"
+grep -qx 'A args=0 pedido=deploy abc outra= usuario=root' "$TARS/sudo.log" ||
+    falhar "sudoers: o deploy não roda a entrada como root com o SSH_ORIGINAL_COMMAND (e só ele): $(grep '^A ' "$TARS/sudo.log")"
+grep -qx 'B negado' "$TARS/sudo.log" || falhar "sudoers: a entrada aceitou argumento: $(grep '^B ' "$TARS/sudo.log")"
+grep -qx 'C negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou um shell como root"
+grep -qx 'D negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou outro comando como root"
+ok "sudoers: só a entrada, sem argumento, com o SSH_ORIGINAL_COMMAND"
+
 if [ "$falhas" -gt 0 ]; then
     dc logs >&2
     exit 1
````

Aplique em `.github/workflows/ci.yml`:

````diff
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index f056cf1..f62e445 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -340,6 +340,16 @@ jobs:
       - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
         with:
           persist-credentials: false
+      - name: Docker com o image store containerd, como na VPS
+        # O vigilume-entrada recusa o store clássico: o image-id do protocolo
+        # é o digest do manifesto, que só o containerd devolve. Antes do load:
+        # trocar de store esconde as imagens do outro.
+        run: |
+          arq=/etc/docker/daemon.json
+          atual=$(sudo cat "$arq" 2>/dev/null || echo '{}')
+          jq '.features["containerd-snapshotter"] = true' <<<"$atual" | sudo tee "$arq" >/dev/null
+          sudo systemctl restart docker
+          docker info -f '{{.DriverStatus}}' | grep -q io.containerd.snapshotter.v1
       - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
         with:
           name: imagens
````

- [ ] **Step 2: Rode e veja falhar**

```bash
bash deploy/ci/checar-vps.sh <imagem do app>
```

Expected: sai 1 com `cp: não foi possível obter estado de '…/deploy/vps/vigilume-entrada'` (a entrada não existe).

- [ ] **Step 3: Implemente**

Crie `deploy/vps/vigilume-entrada` (e `chmod 755`):

````bash
#!/usr/bin/env bash
# vigilume-entrada: a ÚNICA coisa que a chave de deploy alcança na VPS (Plano
# 11, spec §3). Instalado em /usr/local/sbin/vigilume-entrada (root, 755). O
# authorized_keys do usuário deploy força:
#
#   restrict,command="sudo /usr/local/sbin/vigilume-entrada" ssh-ed25519 ...
#
# e o sudoers (deploy/vps/sudoers-vigilume-entrada) deixa o deploy rodar só
# este arquivo, sem argumento, guardando o SSH_ORIGINAL_COMMAND. O pedido vem
# do SSH_ORIGINAL_COMMAND, e nunca da linha de comando:
#
#   ssh deploy@VPS "deploy <sha 40 hex> <image-id sha256:64 hex>" < imagem.tar
#       O tar (layout OCI, sem nome) chega pela entrada padrão. Códigos: 0
#       deploy saudável; 2 pedido recusado (uso, argumento); 3 imagem
#       divergente (o ID carregado não é o pedido, ou o tar traz nome); 4 não
#       ficou saudável e o rollback foi feito; 1 qualquer outro erro.
#
# Tudo o que o script escreve vai para a saída de ERRO: a saída padrão fica
# reservada (o verbo backup manda por ela só o dump cifrado).
#
# Sem variável de ambiente que mude o comportamento: o sudo limpa o ambiente,
# e os caminhos abaixo são constantes. O teste do CI (deploy/ci/checar-vps.sh)
# roda uma cópia com estas constantes trocadas por sed.
set -euo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

DIR=/opt/vigilume
IMAGEM=vigilume
ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"
ESPERA_S=90
TMP_BASE=/var/tmp
TETO_BYTES=$((2 * 1024 * 1024 * 1024))

# fd 3 = a saída padrão de verdade; daqui em diante, o que não for o dump
# cifrado vai para a de erro.
exec 3>&1 1>&2

sair() {
    local codigo=$1
    shift
    echo "vigilume-entrada: $*"
    exit "$codigo"
}
uso() { sair 2 "$* (uso: \"deploy <sha de 40 hex> sha256:<64 hex>\")"; }

dc() { COMPOSE_FILE="$ARQUIVOS_COMPOSE" docker compose --project-directory "$DIR" "$@"; }

# O ID que o deploy.yml manda é o digest do manifesto linux/amd64, e só o
# image store containerd devolve esse valor no `docker load` e no `.Id` (no
# store clássico, o ID é o da config). Com o outro store, a conferência
# recusaria todo deploy: melhor dizer por quê.
exigir_containerd() {
    docker info -f '{{.DriverStatus}}' 2>/dev/null | grep -q 'io.containerd.snapshotter.v1' ||
        sair 1 "o Docker não usa o image store containerd; veja docs/deploy/vps.md (Docker)"
}

# Mantém as imagens das 3 últimas versões que ficaram no ar (o historico,
# uma linha por deploy saudável); as outras etiquetas $IMAGEM:<sha> saem,
# inclusive as de deploys que não ficaram saudáveis. Uma em uso não sai (o rmi
# recusa) e não é erro.
podar() {
    local manter tag
    manter=$(tac "$DIR/historico" | awk '!visto[$0]++' | head -n 3)
    while read -r tag; do
        [ "$tag" = atual ] && continue
        grep -qxF -- "$tag" <<<"$manter" && continue
        docker rmi "$IMAGEM:$tag" >/dev/null 2>&1 || true
    done < <(docker image ls "$IMAGEM" --format '{{.Tag}}')
}

# Conteúdo de um arquivo da raiz do tar, com ou sem o "./" na frente; nada se
# não houver.
ler_do_tar() {
    local membro
    membro=$(tar -tf "$1" | grep -xE "(\./)?$2" | head -n 1 || true)
    [ -z "$membro" ] || tar -xOf "$1" "$membro"
}

deploy() {
    local sha=$1 id=$2 tmp tam saida carregados anterior
    exigir_containerd

    tmp=$(mktemp -d "$TMP_BASE/vigilume-entrada.XXXXXX")
    # shellcheck disable=SC2064 # o caminho é fixado agora, de propósito
    trap "rm -rf '$tmp'" EXIT
    head -c $((TETO_BYTES + 1)) > "$tmp/imagem.tar"
    tam=$(stat -c %s "$tmp/imagem.tar")
    [ "$tam" -gt 0 ] || uso "nada chegou na entrada padrão (o docker save da imagem)"
    [ "$tam" -le "$TETO_BYTES" ] || uso "a imagem passa de $TETO_BYTES bytes"

    # O tar do deploy.yml não tem nome (layout OCI do crane). Um tar com nome
    # etiquetaria, no load, uma imagem qualquer desta máquina (caddy:2, por
    # exemplo) antes de qualquer conferência: recusado sem carregar.
    # O membro pode vir como "index.json" ou "./index.json" (tar -C dir .).
    if ler_do_tar "$tmp/imagem.tar" index.json |
        grep -Eq '"(org\.opencontainers\.image\.ref\.name|io\.containerd\.image\.name)"'; then
        sair 3 "o tar traz nome de imagem (index.json); o deploy manda o tar sem nome"
    fi
    if ler_do_tar "$tmp/imagem.tar" manifest.json | grep -q '"RepoTags":\["'; then
        sair 3 "o tar traz nome de imagem (manifest.json); o deploy manda o tar sem nome"
    fi

    saida=$(docker load -i "$tmp/imagem.tar") || sair 1 "docker load falhou"
    echo "$saida"
    carregados=$(sed -n 's/^Loaded image ID: //p' <<<"$saida")
    if [ -z "$carregados" ] || [ "$(wc -l <<<"$carregados")" -ne 1 ] || grep -q '^Loaded image: ' <<<"$saida"; then
        sair 3 "o tar não trouxe exatamente uma imagem sem nome"
    fi
    if [ "$carregados" != "$id" ]; then
        # Sem etiqueta, e fora de uso: sai. Em uso ou etiquetada, o rmi recusa.
        docker rmi "$carregados" >/dev/null 2>&1 || true
        sair 3 "a imagem carregada é $carregados, e o deploy pediu $id"
    fi
    docker tag "$id" "$IMAGEM:$sha"

    anterior=$(cat "$DIR/versao" 2>/dev/null || true)
    echo "deploy de $sha (antes: ${anterior:-nenhuma})"
    if VIGILUME_SHA=$sha dc up -d --wait --wait-timeout "$ESPERA_S"; then
        echo "$sha" > "$DIR/versao.novo" && mv "$DIR/versao.novo" "$DIR/versao"
        if [ -n "$anterior" ] && [ "$anterior" != "$sha" ]; then
            echo "$anterior" > "$DIR/versao-anterior.novo" && mv "$DIR/versao-anterior.novo" "$DIR/versao-anterior"
        fi
        echo "$sha" >> "$DIR/historico"
        docker tag "$IMAGEM:$sha" "$IMAGEM:atual"
        podar
        echo "deploy de $sha saudável"
        exit 0
    fi

    echo "o app não ficou saudável em ${ESPERA_S} s; o log dele:"
    dc logs --no-color --tail 100 app || true
    if [ -z "$anterior" ]; then
        dc stop app || true
        sair 4 "primeiro deploy e sem versão anterior: o app ficou parado"
    fi
    # A etiqueta atual só anda no sucesso: continua na anterior.
    if VIGILUME_SHA=$anterior dc up -d --wait --wait-timeout "$ESPERA_S"; then
        sair 4 "rollback feito: $anterior de volta no ar; $sha não subiu"
    fi
    dc logs --no-color --tail 100 app || true
    sair 1 "o rollback para $anterior também não ficou saudável: veja docs/deploy/vps.md"
}

[ "$#" -eq 0 ] || uso "argumento na linha de comando não é aceito; o pedido vem do SSH"
pedido=${SSH_ORIGINAL_COMMAND:-}
# Só minúsculas, dígitos, ":" e espaço: nada que um shell ou o docker
# interpretem (as palavras nunca passam por eval).
case "$pedido" in
    "") uso "pedido vazio" ;;
    *[!a-z0-9:\ ]*) uso "caractere não permitido no pedido" ;;
esac
read -r -a palavras <<<"$pedido"

case "${palavras[0]}" in
    deploy)
        [ "${#palavras[@]}" -eq 3 ] || uso "deploy leva o SHA e o image-id"
        [[ "${palavras[1]}" =~ ^[0-9a-f]{40}$ ]] || uso "SHA malformado: '${palavras[1]}'"
        [[ "${palavras[2]}" =~ ^sha256:[0-9a-f]{64}$ ]] || uso "image-id malformado: '${palavras[2]}'"
        exec 9>"$DIR/.entrada.trava"
        flock -w 600 9 || sair 1 "outra operação da entrada não terminou em 10 min"
        deploy "${palavras[1]}" "${palavras[2]}"
        ;;
    *) uso "verbo desconhecido: '${palavras[0]}'" ;;
esac
````

Crie `deploy/vps/sudoers-vigilume-entrada`:

````text
# /etc/sudoers.d/vigilume-entrada (root, 0440; confira com `visudo -cf`).
#
# O usuário deploy (sem sudo geral, fora do grupo docker) roda como root SÓ o
# vigilume-entrada, e SEM argumento nenhum (o "" no fim da regra): o pedido
# vem do SSH_ORIGINAL_COMMAND, que o sshd põe no ambiente do comando forçado
# do authorized_keys. O sudo limpa o ambiente; esta linha guarda só essa
# variável, e só para este comando. Nenhuma outra variável passa (o script
# não lê nenhuma outra, e as constantes dele não se mudam por ambiente).
Defaults!/usr/local/sbin/vigilume-entrada env_keep += "SSH_ORIGINAL_COMMAND"
# Sem pseudo-terminal: o docker save chega pela entrada padrão, em binário.
Defaults!/usr/local/sbin/vigilume-entrada !use_pty
deploy ALL=(root) NOPASSWD: /usr/local/sbin/vigilume-entrada ""
````

- [ ] **Step 4: Rode e veja passar**

```bash
bash deploy/ci/checar-vps.sh <imagem do app>
shellcheck deploy/vps/vigilume-entrada deploy/ci/checar-vps.sh
actionlint .github/workflows/ci.yml
```

Expected: os seis `ok:` de V1 e mais "vigilume-entrada: primeiro deploy doente para o app (4); deploy saudável (0)", "…pedidos malformados recusados com 2", "…imagem divergente e tar com nome recusados com 3", "…deploy doente volta à versão anterior (4)", "…versao-anterior e as 3 últimas imagens", "…recusa o Docker sem o image store containerd", "sudoers: só a entrada, sem argumento, com o SSH_ORIGINAL_COMMAND", e `vps conferida` (~50 s).

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

Todas com `bash deploy/ci/checar-vps.sh <imagem>`.

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| S1 | `[[ "${palavras[1]}" =~ ^[0-9a-f]{40}$ ]] \|\|` → `true \|\|` | `pedido '…' (SHA com 39 caracteres) saiu 0, queria 2` (e 41, e fora do hex), `um pedido recusado mexeu na versão` |
| S2 | O regex do image-id vira `^sha256:` | `pedido '…' (image-id curto) saiu 3, queria 2` |
| S3 | `if [ "$carregados" != "$id" ]; then` → `if false; then` | `tar de b com o ID de a saiu 0, queria 3`, `a imagem divergente foi etiquetada`, `ID inexistente saiu 1, queria 3` |
| S4 | Desligue a checagem das anotações do `index.json` | `o tar com io.containerd.image.name foi carregado com o nome` (e `org.opencontainers.image.ref.name`) |
| S4b | `ler_do_tar` só com o nome exato (`grep -xE "$2"`, sem o `./`) | as mesmas duas: é o furo 1 |
| S5 | Desligue as duas checagens de nome (`index.json` e `manifest.json`) | `o tar com nome foi carregado (a etiqueta voltou)` e os dois anotados |
| S6 | `if VIGILUME_SHA=$anterior dc up` → `if false && …` (sem rollback) | `deploy doente saiu 1, queria 4`, `o deploy doente não disse que fez o rollback`, `depois do rollback o app não está saudável`, `/healthz pelo Caddy não deu 200` |
| S7 | Sem o `1>&2` no `exec 3>&1 1>&2` | `o deploy escreveu na saída padrão` (e as mensagens que o teste procura na de erro) |
| S8 | Tire o `exigir_containerd` do `deploy` | `sem o store containerd a entrada saiu 0, queria 1` |
| S9 | `[ "$#" -eq 0 ] \|\|` → `true \|\|` | `argumento na linha de comando saiu 0, queria 2` |
| S10 | Aceite `;`, `$`, `(` e `)` no filtro de caracteres | **fica verde** (declarada): os pedidos `deploy <sha>;id …` e `deploy $(id) …` caem no regex do SHA (2), e as palavras nunca passam por `eval`. O filtro é defesa em profundidade |
| S11 | Tire o `podar` | `imagens que sobraram: '…' , queria as de b, c e d` |
| S12 | Não grave a `versao-anterior` | `versao-anterior não é a de c`, `o redeploy de d mudou a versao-anterior` |
| S13 | Tire o `dc stop app` do primeiro deploy doente | `primeiro deploy doente deixou o app de pé` |
| S14 | (primeira versão) Tire o `docker tag "$IMAGEM:$anterior" "$IMAGEM:atual"` do rollback | ficou **verde**: a etiqueta só anda no sucesso. A linha saiu do código (furo 4) |
| S15 | Tire o `Defaults!… env_keep += "SSH_ORIGINAL_COMMAND"` | `sudoers: o deploy não roda a entrada como root com o SSH_ORIGINAL_COMMAND (e só ele): A args=0 pedido= …` |
| S16 | Tire o `""` do fim da regra | `sudoers: a entrada aceitou argumento: B args=1 …` |
| S17 | `NOPASSWD: ALL` | `…aceitou argumento`, `o deploy rodou um shell como root`, `o deploy rodou outro comando como root` |
| S18 | `env_keep += "SSH_ORIGINAL_COMMAND OUTRA"` | `…(e só ele): A args=0 pedido=deploy abc outra=x usuario=root` |

S3–S8 e S12 foram refeitas no estado final (depois dos furos 1–4), todas vermelhas.

- [ ] **Step 6: Commit**

```bash
chmod 755 deploy/vps/vigilume-entrada
git add deploy/vps/vigilume-entrada deploy/vps/sudoers-vigilume-entrada deploy/ci/checar-vps.sh .github/workflows/ci.yml
git commit -m "feat(vps): vigilume-entrada com o verbo deploy, rollback e sudoers restrito"
```

---

### Tarefa V3: verbo `backup` (o `backup.sh` sem mudança, só o cifrado na saída padrão)

Spec §4. `backup` sobe um Postgres descartável com a imagem do `db` no ar, ligado à rede do compose, com os dados
em `tmpfs`; roda nele o `deploy/backup/backup.sh` **sem mudança** (dump como `vigilume_backup`, restauração,
`verificar.sql`); cifra com `age -R /opt/vigilume/age-recipient` direto para a saída padrão. O dump em claro fica em
`/dev/shm` e é apagado com `shred`. Códigos do `backup.sh` preservados (0/3/4), 1 para o resto.

**Files:**
- Modify: `deploy/vps/vigilume-entrada` (constantes `SHM_BASE` e `SCRIPTS`, função `backup`, verbo), `deploy/ci/checar-vps.sh` (bloco 9), `.github/workflows/ci.yml` (`AGE_VERSION`, `setup-go` e o `age` no job `vps`)

**Interfaces:**
- Consumes: `deploy/backup/backup.sh`, `deploy/backup/verificar.sql`, `deploy/compose/initdb.sql` (sem mudança); o papel `vigilume_backup` do `initdb.sh` (V1).
- Produces: o protocolo `backup` (0/3/4/1, e 2 com argumento); o layout `/opt/vigilume/deploy/…`.

- [ ] **Step 1: Escreva os testes**

Aplique em `deploy/ci/checar-vps.sh`:

````diff
diff --git a/deploy/ci/checar-vps.sh b/deploy/ci/checar-vps.sh
index ea25151..af9bb6b 100644
--- a/deploy/ci/checar-vps.sh
+++ b/deploy/ci/checar-vps.sh
@@ -6,6 +6,8 @@
 #
 #   bash deploy/ci/checar-vps.sh <imagem do app>     (ex.: vigilume:dev)
 #
+# Precisa de docker (com o image store containerd, como a VPS), jq, curl,
+# openssl e do age/age-keygen no PATH.
 # O projeto do compose é "vigilume-teste-vps" e as portas do Caddy vão para o
 # loopback (TESTE_VPS_HTTP, padrão 18580; TESTE_VPS_HTTPS, padrão 18543):
 # deploy/ci/vps/compose.teste.yaml. A rede é a de produção (172.30.0.0/24):
@@ -28,7 +30,12 @@ SENHA=senha-do-teste-da-vps-2026
 
 falhas=0
 falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
-ok() { echo "ok: $*"; }
+# "ok" só quando o bloco que ele fecha não falhou.
+falhas_antes=0
+ok() {
+    [ "$falhas" = "$falhas_antes" ] && echo "ok: $*"
+    falhas_antes=$falhas
+}
 
 # O diretório que faz o papel de /opt/vigilume.
 DIR=$(mktemp -d)
@@ -37,6 +44,8 @@ limpar() {
         echo "de pé em $DIR (projeto $PROJETO)" >&2
         return
     fi
+    # Um postgres descartável esquecido pela entrada seguraria a rede.
+    docker ps -aq --filter name=vigilume-restauro- | xargs -r docker rm -f >/dev/null 2>&1 || true
     dc down -v --remove-orphans >/dev/null 2>&1 || true
     for r in "$REPO_TESTE" "$REPO_TESTE-fonte"; do
         docker image ls --format '{{.Repository}}:{{.Tag}}' "$r" | xargs -r docker rmi >/dev/null 2>&1 || true
@@ -86,6 +95,11 @@ trocar 'IMAGEM=vigilume' "IMAGEM=$REPO_TESTE"
 trocar 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"' 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml"'
 trocar 'ESPERA_S=90' 'ESPERA_S=45'
 trocar 'TMP_BASE=/var/tmp' "TMP_BASE=$TARS"
+trocar 'SHM_BASE=/dev/shm' "SHM_BASE=$TARS"
+# O age do teste pode não estar num diretório do PATH fixo da entrada (na
+# VPS ele vem do apt, em /usr/bin).
+trocar 'export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
+    "export PATH=$(dirname "$(command -v age)"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
 # Como o sudo a roda: ambiente limpo, só o pedido do SSH. A saída padrão e a
 # de erro ficam em $TARS/saida e $TARS/erro; devolve o código.
 entrada() {
@@ -473,6 +487,64 @@ grep -qx 'C negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou um shell
 grep -qx 'D negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou outro comando como root"
 ok "sudoers: só a entrada, sem argumento, com o SSH_ORIGINAL_COMMAND"
 
+# ---------------------------------------------------------------------------
+# 9. vigilume-entrada backup: só texto cifrado na saída padrão.
+# ---------------------------------------------------------------------------
+# Os scripts do backup no layout que a entrada espera ($DIR/deploy), tais e
+# quais os do repositório; a chave do age gerada aqui (a privada fica em
+# $TARS, só para conferir).
+mkdir -p "$DIR/deploy/backup" "$DIR/deploy/compose"
+cp "$RAIZ/deploy/backup/backup.sh" "$RAIZ/deploy/backup/verificar.sql" "$DIR/deploy/backup/"
+cp "$RAIZ/deploy/compose/initdb.sql" "$DIR/deploy/compose/"
+age-keygen -o "$TARS/chave-age" 2>/dev/null
+age-keygen -y "$TARS/chave-age" > "$DIR/age-recipient"
+
+c=$(entrada "backup extra" < /dev/null)
+[ "$c" = 2 ] || falhar "backup com argumento saiu $c, queria 2"
+
+c=$(entrada backup < /dev/null)
+if [ "$c" != 0 ]; then
+    cat "$TARS/erro" >&2
+    falhar "backup saiu $c, queria 0"
+fi
+grep -q 'backup conferido' "$TARS/erro" || falhar "o log do backup não diz 'backup conferido'"
+[ "$(head -c 21 "$TARS/saida")" = "age-encryption.org/v1" ] || falhar "a saída padrão do backup não começa com o cabeçalho do age"
+grep -aq 'PGDMP' "$TARS/saida" && falhar "a saída padrão do backup tem o dump em claro (PGDMP)"
+grep -aqE 'vigilume_app|goose_db_version|CREATE TABLE|backup conferido' "$TARS/saida" && falhar "a saída padrão do backup tem texto em claro"
+# Decifrado com a privada, é um dump do pg_dump que o pg_restore lê.
+if age -d -i "$TARS/chave-age" -o "$TARS/decifrado.dump" "$TARS/saida"; then
+    [ "$(head -c 5 "$TARS/decifrado.dump")" = PGDMP ] || falhar "o backup decifrado não é um dump do pg_dump"
+    chmod 644 "$TARS/decifrado.dump"
+    docker run --rm -v "$TARS/decifrado.dump:/d.dump:ro" "$pg" pg_restore -l /d.dump 2>/dev/null |
+        grep -q 'TABLE public users' || falhar "o dump decifrado não tem a tabela users"
+    rm -f "$TARS/decifrado.dump"
+else
+    falhar "o backup não decifra com a chave privada"
+fi
+# Nada sobra: nem o postgres descartável, nem o dump em claro.
+[ -z "$(docker ps -aq --filter name=vigilume-restauro-)" ] || falhar "o postgres descartável do backup ficou para trás"
+ls "$TARS"/vigilume-backup.* >/dev/null 2>&1 && falhar "o diretório do dump em claro ficou para trás"
+ok "vigilume-entrada backup: conferido, só o cifrado na saída padrão"
+
+# Sem a chave pública: 1, e nada na saída padrão.
+mv "$DIR/age-recipient" "$DIR/age-recipient.fora"
+c=$(entrada backup < /dev/null)
+[ "$c" = 1 ] || falhar "backup sem age-recipient saiu $c, queria 1"
+[ ! -s "$TARS/saida" ] || falhar "backup sem age-recipient escreveu na saída padrão"
+grep -q 'postgres descartável' "$TARS/erro" && falhar "backup sem age-recipient fez o dump antes de recusar"
+mv "$DIR/age-recipient.fora" "$DIR/age-recipient"
+# A cópia que não passa nas checagens: 3, e nada na saída padrão. O
+# verificar.sql do diretório da entrada é trocado por um que sempre falha
+# (o do repositório não muda).
+cp "$DIR/deploy/backup/verificar.sql" "$TARS/verificar.sql"
+printf '\\set ON_ERROR_STOP on\nDO $$ BEGIN RAISE EXCEPTION %s; END $$;\n' "'checagem do teste'" > "$DIR/deploy/backup/verificar.sql"
+c=$(entrada backup < /dev/null)
+[ "$c" = 3 ] || falhar "backup com a checagem falhando saiu $c, queria 3"
+[ ! -s "$TARS/saida" ] || falhar "backup com a checagem falhando escreveu na saída padrão"
+cp "$TARS/verificar.sql" "$DIR/deploy/backup/verificar.sql"
+[ -z "$(docker ps -aq --filter name=vigilume-restauro-)" ] || falhar "o postgres descartável ficou para trás depois da falha"
+ok "vigilume-entrada backup: sem chave (1) e cópia reprovada (3) não mandam nada"
+
 if [ "$falhas" -gt 0 ]; then
     dc logs >&2
     exit 1
````

Aplique em `.github/workflows/ci.yml`:

````diff
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index f62e445..9832cac 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -37,6 +37,8 @@ env:
   GOSEC_VERSION: v2.29.0
   GOVULNCHECK_VERSION: v1.8.0
   GITLEAKS_VERSION: v8.30.1
+  # O mesmo do backup.yml (o job vps cifra e decifra um backup de teste).
+  AGE_VERSION: v1.3.2
   # O nome com que o job image carrega a imagem e o e2e a usa: o mesmo do
   # compose.yaml, com VIGILUME_TAG=ci.
   #
@@ -355,6 +357,13 @@ jobs:
           name: imagens
           path: ${{ runner.temp }}
       - run: gunzip -c "$RUNNER_TEMP/imagens.tar.gz" | docker load
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+          cache: false
+      - name: age
+        # A mesma versão do backup.yml: o teste cifra e decifra o backup.
+        run: go install "filippo.io/age/cmd/age@${AGE_VERSION}" "filippo.io/age/cmd/age-keygen@${AGE_VERSION}"
       - name: checagens da VPS
         run: bash deploy/ci/checar-vps.sh "$IMAGEM:ci"
 
````

- [ ] **Step 2: Rode e veja falhar**

```bash
PATH=<diretório do age>:$PATH bash deploy/ci/checar-vps.sh <imagem do app>
```

Expected: sai 1 com `FALHOU: a linha 'SHM_BASE=/dev/shm' não está (uma vez) no vigilume-entrada`. Com só essa constante acrescentada à entrada da V2 (conferido), o motivo de fundo aparece: `vigilume-entrada: verbo desconhecido: 'backup'`, `backup saiu 2, queria 0`, `backup sem age-recipient saiu 2, queria 1`, `backup com a checagem falhando saiu 2, queria 3`.

- [ ] **Step 3: Implemente**

Aplique em `deploy/vps/vigilume-entrada`:

````diff
diff --git a/deploy/vps/vigilume-entrada b/deploy/vps/vigilume-entrada
index 94ef4b1..f4d0e5b 100755
--- a/deploy/vps/vigilume-entrada
+++ b/deploy/vps/vigilume-entrada
@@ -15,6 +15,18 @@
 #       divergente (o ID carregado não é o pedido, ou o tar traz nome); 4 não
 #       ficou saudável e o rollback foi feito; 1 qualquer outro erro.
 #
+#   ssh deploy@VPS backup > vigilume-<data>.dump.age
+#       O dump do banco, restaurado e conferido pelo deploy/backup/backup.sh
+#       (sem mudança), cifrado com a chave PÚBLICA do age em
+#       /opt/vigilume/age-recipient, na saída padrão; o log na de erro.
+#       Códigos: 0 conferido; 3 a cópia não passou nas checagens (nada na
+#       saída); 4 conferido e enviado, mas o disco passou de 85%; 1 outro
+#       erro (nada, ou um cifrado incompleto, na saída: descarte).
+#       Os scripts ficam em /opt/vigilume/deploy/, com o layout do
+#       repositório (backup/backup.sh, backup/verificar.sql,
+#       compose/initdb.sql), porque o backup.sh acha o initdb.sql por
+#       ../compose/.
+#
 # Tudo o que o script escreve vai para a saída de ERRO: a saída padrão fica
 # reservada (o verbo backup manda por ela só o dump cifrado).
 #
@@ -30,6 +42,8 @@ IMAGEM=vigilume
 ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"
 ESPERA_S=90
 TMP_BASE=/var/tmp
+SHM_BASE=/dev/shm
+SCRIPTS="$DIR/deploy"
 TETO_BYTES=$((2 * 1024 * 1024 * 1024))
 
 # fd 3 = a saída padrão de verdade; daqui em diante, o que não for o dump
@@ -42,7 +56,7 @@ sair() {
     echo "vigilume-entrada: $*"
     exit "$codigo"
 }
-uso() { sair 2 "$* (uso: \"deploy <sha de 40 hex> sha256:<64 hex>\")"; }
+uso() { sair 2 "$* (uso: \"deploy <sha de 40 hex> sha256:<64 hex>\" ou \"backup\")"; }
 
 dc() { COMPOSE_FILE="$ARQUIVOS_COMPOSE" docker compose --project-directory "$DIR" "$@"; }
 
@@ -142,6 +156,76 @@ deploy() {
     sair 1 "o rollback para $anterior também não ficou saudável: veja docs/deploy/vps.md"
 }
 
+# backup: o deploy/backup/backup.sh SEM MUDANÇA (Plano 11, spec §4), dentro de
+# um postgres descartável (a mesma imagem do db) ligado à rede do compose,
+# com os dados em tmpfs. O dump em claro fica num diretório em memória
+# ($SHM_BASE) e sai daqui só cifrado com a chave PÚBLICA do age.
+backup() {
+    local db ip rede img senha codigo=0 raiz_docker pct nome tmp
+    [ -s "$DIR/age-recipient" ] || sair 1 "falta $DIR/age-recipient (a chave PÚBLICA do age)"
+    command -v age >/dev/null || sair 1 "o age não está instalado"
+    [ -f "$SCRIPTS/backup/backup.sh" ] && [ -f "$SCRIPTS/backup/verificar.sql" ] && [ -f "$SCRIPTS/compose/initdb.sql" ] ||
+        sair 1 "faltam os scripts do backup em $SCRIPTS (backup/backup.sh, backup/verificar.sql, compose/initdb.sql)"
+    senha=$(sed -n 's/^VIGILUME_BACKUP_PASSWORD=//p' "$DIR/.env" | tail -n 1)
+    [ -n "$senha" ] || sair 1 "VIGILUME_BACKUP_PASSWORD vazio no $DIR/.env"
+    db=$(dc ps -q db)
+    [ -n "$db" ] || sair 1 "o serviço db não está de pé"
+    rede=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$db")
+    ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$db")
+    img=$(docker inspect -f '{{.Config.Image}}' "$db")
+
+    nome=vigilume-restauro-$$
+    tmp=$(mktemp -d "$SHM_BASE/vigilume-backup.XXXXXX")
+    # shellcheck disable=SC2064 # nome e caminho fixados agora, de propósito
+    trap "docker rm -f '$nome' >/dev/null 2>&1 || true; shred -u '$tmp'/* 2>/dev/null || true; rm -rf '$tmp'" EXIT
+
+    # A senha do descartável é de uso único; a do vigilume_backup vai ao
+    # contêiner pelo ambiente (-e NOME, sem valor), fora da linha de comando.
+    export POSTGRES_PASSWORD PGPASSWORD ORIGEM_URL VOLUME_BYTES
+    POSTGRES_PASSWORD=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
+    PGPASSWORD=$POSTGRES_PASSWORD
+    ORIGEM_URL="postgres://vigilume_backup:$senha@$ip:5432/vigilume?sslmode=disable"
+    raiz_docker=$(docker info -f '{{.DockerRootDir}}')
+    VOLUME_BYTES=$(df -B1 --output=size "$raiz_docker" | tail -n 1 | tr -d ' ')
+
+    echo "== postgres descartável ($img) na rede $rede"
+    docker run -d --name "$nome" --network "$rede" --memory 3g \
+        --tmpfs /var/lib/postgresql:rw,size=2g -e POSTGRES_PASSWORD \
+        -v "$SCRIPTS:/deploy:ro" -v "$tmp:/saida" "$img" >/dev/null ||
+        sair 1 "o postgres descartável não subiu"
+    for _ in $(seq 1 60); do
+        docker exec "$nome" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1 && break
+        sleep 1
+    done
+    docker exec "$nome" pg_isready -h 127.0.0.1 -U postgres >/dev/null || sair 1 "o postgres descartável não ficou pronto"
+
+    # O mesmo usuário de quem roda a entrada (root, na VPS): o dump sai com
+    # esse dono, e o shred consegue apagá-lo.
+    docker exec --user "$(id -u):$(id -g)" -e ORIGEM_URL -e PGPASSWORD -e VOLUME_BYTES \
+        -e SAIDA=/saida/vigilume.dump -e PGHOST=127.0.0.1 -e PGPORT=5432 -e PGUSER=postgres \
+        "$nome" sh /deploy/backup/backup.sh || codigo=$?
+    case "$codigo" in
+        0 | 4) ;;
+        3) sair 3 "a cópia restaurada não passou nas checagens: não há backup que sirva" ;;
+        *) sair 1 "o backup.sh saiu com $codigo: não houve backup" ;;
+    esac
+    [ -s "$tmp/vigilume.dump" ] || sair 1 "o backup.sh saiu $codigo sem deixar o dump"
+
+    # O disco de verdade: o sistema de arquivos dos volumes do Docker. O
+    # backup.sh mede o banco contra ele; aqui, a ocupação total (imagens,
+    # logs, volumes). Acima de 85%, o backup vai do mesmo jeito, com o 4.
+    pct=$(df --output=pcent "$raiz_docker" | tail -n 1 | tr -dc 0-9)
+    echo "disco de $raiz_docker: ${pct}% ocupado"
+    if [ "$pct" -ge 85 ]; then
+        echo "ERRO: o disco da VPS passou de 85%: libere espaço (docs/deploy/vps.md, disco)"
+        codigo=4
+    fi
+
+    age -R "$DIR/age-recipient" < "$tmp/vigilume.dump" >&3 || sair 1 "o age falhou: o backup cifrado está incompleto"
+    echo "backup cifrado enviado (código $codigo)"
+    exit "$codigo"
+}
+
 [ "$#" -eq 0 ] || uso "argumento na linha de comando não é aceito; o pedido vem do SSH"
 pedido=${SSH_ORIGINAL_COMMAND:-}
 # Só minúsculas, dígitos, ":" e espaço: nada que um shell ou o docker
@@ -161,5 +245,11 @@ case "${palavras[0]}" in
         flock -w 600 9 || sair 1 "outra operação da entrada não terminou em 10 min"
         deploy "${palavras[1]}" "${palavras[2]}"
         ;;
+    backup)
+        [ "${#palavras[@]}" -eq 1 ] || uso "backup não leva argumento"
+        exec 9>"$DIR/.entrada.trava"
+        flock -w 600 9 || sair 1 "outra operação da entrada não terminou em 10 min"
+        backup
+        ;;
     *) uso "verbo desconhecido: '${palavras[0]}'" ;;
 esac
````

- [ ] **Step 4: Rode e veja passar**

```bash
PATH=<diretório do age>:$PATH bash deploy/ci/checar-vps.sh <imagem do app>
shellcheck deploy/vps/vigilume-entrada deploy/ci/checar-vps.sh
actionlint .github/workflows/ci.yml
```

Expected: os `ok:` de V2 e mais "vigilume-entrada backup: conferido, só o cifrado na saída padrão", "vigilume-entrada backup: sem chave (1) e cópia reprovada (3) não mandam nada", e `vps conferida`.

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

Todas com `bash deploy/ci/checar-vps.sh <imagem>`.

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| S1 | `age -R … >&3` → `cat "$tmp/vigilume.dump" >&3` | `a saída padrão do backup não começa com o cabeçalho do age`, `…tem o dump em claro (PGDMP)`, `…tem texto em claro`, `o backup não decifra com a chave privada` |
| S2 | Sem o `1>&2` no `exec 3>&1 1>&2` | `o log do backup não diz 'backup conferido'`, `…tem texto em claro` (e as de V2) |
| S3 | `3) sair 3 …` → `3) ;;` | `backup com a checagem falhando escreveu na saída padrão` |
| S4 | Sem o `trap` de limpeza | `o postgres descartável do backup ficou para trás`, `o diretório do dump em claro ficou para trás` |
| S5 | Sem o `-e ORIGEM_URL` no `docker exec` | `backup saiu 1, queria 0`, `backup com a checagem falhando saiu 1, queria 3` |
| S6 | Tire a checagem do `age-recipient` do começo | `backup sem age-recipient fez o dump antes de recusar` |
| S7 | Aceite `backup` com argumento | `backup com argumento saiu 0, queria 2` |
| S8 | Sem o `--network "$rede"` no Postgres descartável | `psql: … "172.30.0.4", port 5432 failed: Operation timed out`, `backup saiu 1, queria 0` |

O aviso de disco (código 4) não tem teste: encher 85% do disco do runner não é razoável. A leitura é um `df` de uma linha, e o código 4 segue o mesmo caminho do 0 até o `exit`.

- [ ] **Step 6: Commit**

```bash
git add deploy/vps/vigilume-entrada deploy/ci/checar-vps.sh .github/workflows/ci.yml
git commit -m "feat(vps): verbo backup na entrada, com o backup.sh sem mudança e só o cifrado na saída"
```

---

### Tarefa V4: vigia externo (`vigia.sh`, timer do systemd, healthchecks.io)

Spec §6; obrigação 7. A cada minuto, o timer roda o vigia, que pede `https://<domínio>/healthz` (DNS, TLS
conferido, Caddy, app) e pinga a `HC_URL` do healthchecks.io, ou `HC_URL/fail`. O serviço roda com usuário
dinâmico e recebe o `.env` pelo `LoadCredential`.

**Files:**
- Create: `deploy/vps/vigia.sh` (modo 755), `deploy/vps/vigilume-vigia.service`, `deploy/vps/vigilume-vigia.timer`, `deploy/ci/checar-vigia.sh`
- Modify: `.github/workflows/ci.yml` (passo no job `vps`)

**Interfaces:**
- Consumes: `VIGILUME_DOMINIO` e `HC_URL` do `/opt/vigilume/.env` (V1).
- Produces: `/usr/local/sbin/vigilume-vigia`; as unidades `vigilume-vigia.service`/`.timer`.

- [ ] **Step 1: Escreva o teste**

Crie `deploy/ci/checar-vigia.sh`:

````bash
#!/usr/bin/env bash
# Checagens do vigia externo (Plano 11, spec §6): deploy/vps/vigia.sh e as
# unidades vigilume-vigia.service/.timer. O job "vps" do CI roda; localmente,
# da raiz do repositório:
#
#   bash deploy/ci/checar-vigia.sh
#
# O curl é trocado por um falso (primeiro no PATH) que anota cada URL pedida e
# responde conforme o cenário: nada sai para a rede.
set -euo pipefail

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
VIGIA=$RAIZ/deploy/vps/vigia.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

mkdir -p "$T/bin" "$T/cred"
cat > "$T/bin/curl" <<'EOF'
#!/bin/sh
# O último argumento é a URL; os outros, as opções.
for url; do :; done
echo "$*" >> "$REGISTRO"
case "$url" in
    */healthz) [ "${SAUDE:-ok}" = ok ] ;;
    *) exit 0 ;;
esac
EOF
chmod +x "$T/bin/curl"

HC=https://hc-ping.com/0000-teste
# roda <cenário de saúde> <conteúdo do .env>; devolve o código do vigia.
roda() {
    local c=0
    printf '%s\n' "$2" > "$T/cred/vigilume-env"
    : > "$T/registro"
    env -i PATH="$T/bin:/usr/bin:/bin" CREDENTIALS_DIRECTORY="$T/cred" REGISTRO="$T/registro" SAUDE="$1" \
        sh "$VIGIA" 2> "$T/erro" || c=$?
    echo "$c"
}
urls() { awk '{ print $NF }' "$T/registro" | tr '\n' ' '; }
ENV_OK="VIGILUME_DOMINIO=vigilume.exemplo.com
MASTER_KEY=nao-importa
HC_URL=$HC"

# 1. Saudável: pede o /healthz público e pinga a HC_URL.
c=$(roda ok "$ENV_OK")
[ "$c" = 0 ] || falhar "saudável: o vigia saiu $c"
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC " ] || falhar "saudável: pediu '$(urls)'"

# 2. Fora do ar: pinga HC_URL/fail e sai com erro (o systemd registra).
c=$(roda falha "$ENV_OK")
[ "$c" != 0 ] || falhar "fora do ar: o vigia saiu 0"
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC/fail " ] || falhar "fora do ar: pediu '$(urls)'"

# 3. HC_URL com barra no fim: sem barra dupla no /fail.
c=$(roda falha "VIGILUME_DOMINIO=vigilume.exemplo.com
HC_URL=$HC/")
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC/fail " ] || falhar "HC_URL com barra: pediu '$(urls)'"

# 4. Sem HC_URL: erro, e nenhum pedido.
c=$(roda ok "VIGILUME_DOMINIO=vigilume.exemplo.com
HC_URL=")
[ "$c" != 0 ] || falhar "sem HC_URL: o vigia saiu 0"
[ -z "$(urls)" ] || falhar "sem HC_URL: pediu '$(urls)'"
grep -q HC_URL "$T/erro" || falhar "sem HC_URL: não disse o que falta"

# 5. Sem domínio: é falha (pinga /fail), e não um pedido a https:///healthz.
c=$(roda ok "HC_URL=$HC")
[ "$c" != 0 ] || falhar "sem domínio: o vigia saiu 0"
[ "$(urls)" = "$HC/fail " ] || falhar "sem domínio: pediu '$(urls)'"

# 6. O /healthz com o certificado conferido (nada de -k/--insecure) e com
#    prazo; os pings com prazo.
roda ok "$ENV_OK" >/dev/null
grep -Eq -- '(^| )(-k|--insecure)( |$)' "$T/registro" && falhar "o vigia desliga a conferência do certificado"
[ "$(grep -c -- '--max-time' "$T/registro")" = 2 ] || falhar "algum pedido do vigia sem --max-time"

# 7. As unidades: o timer dispara o serviço a cada minuto; o serviço lê o
#    .env pelo LoadCredential, com usuário dinâmico, e roda o vigia instalado.
S=$RAIZ/deploy/vps/vigilume-vigia.service
TM=$RAIZ/deploy/vps/vigilume-vigia.timer
grep -qx 'ExecStart=/usr/local/sbin/vigilume-vigia' "$S" || falhar "o serviço não roda /usr/local/sbin/vigilume-vigia"
grep -qx 'LoadCredential=vigilume-env:/opt/vigilume/.env' "$S" || falhar "o serviço não entrega o .env como vigilume-env"
grep -qx 'DynamicUser=yes' "$S" || falhar "o serviço não usa usuário dinâmico"
grep -qx 'Type=oneshot' "$S" || falhar "o serviço não é oneshot"
grep -qx 'OnCalendar=\*-\*-\* \*:\*:00' "$TM" || falhar "o timer não dispara a cada minuto"
grep -qx 'Unit=vigilume-vigia.service' "$TM" || falhar "o timer não aponta para o serviço"
grep -qx 'WantedBy=timers.target' "$TM" || falhar "o timer não volta depois do reboot"
if command -v systemd-analyze >/dev/null; then
    # O verify confere a sintaxe e que o ExecStart existe: aponta o serviço
    # para o vigia do repositório, com os mesmos nomes de arquivo.
    mkdir -p "$T/unidades"
    sed "s|^ExecStart=.*|ExecStart=$VIGIA|" "$S" > "$T/unidades/vigilume-vigia.service"
    cp "$TM" "$T/unidades/"
    systemd-analyze verify "$T/unidades/vigilume-vigia.timer" "$T/unidades/vigilume-vigia.service" 2> "$T/verify" ||
        falhar "systemd-analyze verify: $(cat "$T/verify")"
    [ ! -s "$T/verify" ] || falhar "systemd-analyze verify avisou: $(cat "$T/verify")"
else
    echo "aviso: sem systemd-analyze, as unidades não foram verificadas" >&2
fi

[ "$falhas" = 0 ] || exit 1
echo "vigia conferido"
````

Aplique em `.github/workflows/ci.yml`:

````diff
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index 9832cac..7e738a6 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -364,6 +364,8 @@ jobs:
       - name: age
         # A mesma versão do backup.yml: o teste cifra e decifra o backup.
         run: go install "filippo.io/age/cmd/age@${AGE_VERSION}" "filippo.io/age/cmd/age-keygen@${AGE_VERSION}"
+      - name: vigia externo (vigia.sh e as unidades do systemd)
+        run: bash deploy/ci/checar-vigia.sh
       - name: checagens da VPS
         run: bash deploy/ci/checar-vps.sh "$IMAGEM:ci"
 
````

- [ ] **Step 2: Rode e veja falhar**

```bash
bash deploy/ci/checar-vigia.sh
```

Expected: sai 1 com `FALHOU: saudável: o vigia saiu 2`, `…pediu ''` (os cinco cenários) e `grep: …/deploy/vps/vigilume-vigia.service: Arquivo ou diretório inexistente`.

- [ ] **Step 3: Implemente**

Crie `deploy/vps/vigia.sh` (e `chmod 755`):

````sh
#!/bin/sh
# Vigia externo do Vigilume (Plano 11, spec §6; obrigação 7): o Vigilume não
# consegue avisar da própria queda. Instalado em /usr/local/sbin/vigilume-vigia
# e rodado a cada minuto pelo vigilume-vigia.timer.
#
# Pede https://<domínio>/healthz como um cliente de fora (DNS, TLS com o
# certificado conferido, Caddy e app) e pinga o healthchecks.io: a HC_URL se
# deu certo, a HC_URL/fail se não. Se a VPS morrer ou perder a rede, os pings
# param, e o healthchecks.io manda o e-mail ao dono do mesmo jeito.
#
# Lê VIGILUME_DOMINIO e HC_URL do /opt/vigilume/.env. O serviço roda com um
# usuário dinâmico, sem acesso ao /opt/vigilume: o systemd entrega uma cópia
# do .env em $CREDENTIALS_DIRECTORY/vigilume-env (LoadCredential).
set -eu

if [ -n "${CREDENTIALS_DIRECTORY:-}" ]; then
    ARQ_ENV=$CREDENTIALS_DIRECTORY/vigilume-env
else
    ARQ_ENV=/opt/vigilume/.env
fi
valor() { sed -n "s/^$1=//p" "$ARQ_ENV" | tail -n 1; }

hc=$(valor HC_URL)
dominio=$(valor VIGILUME_DOMINIO)
if [ -z "$hc" ]; then
    echo "vigia: HC_URL vazio em $ARQ_ENV: não há a quem avisar" >&2
    exit 1
fi
hc=${hc%/}

pingar() { curl -fsS -o /dev/null --max-time 10 --retry 3 "$1"; }

if [ -n "$dominio" ] && curl -fsS -o /dev/null --max-time 15 "https://$dominio/healthz"; then
    pingar "$hc"
    exit 0
fi
echo "vigia: https://${dominio:-<VIGILUME_DOMINIO vazio>}/healthz falhou" >&2
pingar "$hc/fail" || true
exit 1
````

Crie `deploy/vps/vigilume-vigia.service`:

````ini
# /etc/systemd/system/vigilume-vigia.service (Plano 11, spec §6). Disparado
# a cada minuto pelo vigilume-vigia.timer; roda o /usr/local/sbin/vigilume-vigia
# (deploy/vps/vigia.sh) uma vez.
[Unit]
Description=Vigilume: vigia externo do /healthz público (healthchecks.io)
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/vigilume-vigia
# Um usuário criado só para esta execução, sem acesso ao /opt/vigilume: o
# systemd (como root) lê o .env e entrega a cópia em $CREDENTIALS_DIRECTORY.
DynamicUser=yes
LoadCredential=vigilume-env:/opt/vigilume/.env
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
CapabilityBoundingSet=
# Menos que o intervalo do timer: uma execução presa não encavala na seguinte.
TimeoutStartSec=50
````

Crie `deploy/vps/vigilume-vigia.timer`:

````ini
# /etc/systemd/system/vigilume-vigia.timer (Plano 11, spec §6). Ligar com
# `systemctl enable --now vigilume-vigia.timer`. A cada minuto; volta sozinho
# depois de um reboot.
[Unit]
Description=Vigilume: vigia externo a cada minuto

[Timer]
OnCalendar=*-*-* *:*:00
AccuracySec=1s
Unit=vigilume-vigia.service

[Install]
WantedBy=timers.target
````

- [ ] **Step 4: Rode e veja passar**

```bash
bash deploy/ci/checar-vigia.sh
shellcheck deploy/ci/checar-vigia.sh deploy/vps/vigia.sh
actionlint .github/workflows/ci.yml
```

Expected: `vigia conferido` (com o `systemd-analyze verify` sem aviso).

- [ ] **Step 5: Sabotagens (registre cada uma no relatório)**

Todas com `bash deploy/ci/checar-vigia.sh`.

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| S1 | Na falha, pingue `"$hc"` em vez de `"$hc/fail"` | `fora do ar: pediu '… https://hc-ping.com/0000-teste '` (e HC_URL com barra, sem domínio) |
| S2 | Na falha, `exit 0` | `fora do ar: o vigia saiu 0`, `sem domínio: o vigia saiu 0` |
| S3 | `curl -k` no `/healthz` | `o vigia desliga a conferência do certificado` |
| S4 | Tire o `hc=${hc%/}` | `HC_URL com barra: pediu '… https://hc-ping.com/0000-teste//fail '` |
| S5 | Tire o `[ -n "$dominio" ] &&` | `sem domínio: o vigia saiu 0`, `sem domínio: pediu 'https:///healthz …'` |
| S6 | Uma chave inválida no serviço (`ProtectSistema=yes`) | `systemd-analyze verify avisou: …Unknown key name 'ProtectSistema'…` |
| S7 | `OnCalendar=hourly` | `o timer não dispara a cada minuto` |
| S8 | `EnvironmentFile=/opt/vigilume/.env` no lugar do `LoadCredential` | `o serviço não entrega o .env como vigilume-env` |
| S9 | Tire o `--max-time 10` do ping | `algum pedido do vigia sem --max-time` |

- [ ] **Step 6: Commit**

```bash
chmod 755 deploy/vps/vigia.sh
git add deploy/vps/vigia.sh deploy/vps/vigilume-vigia.service deploy/vps/vigilume-vigia.timer deploy/ci/checar-vigia.sh .github/workflows/ci.yml
git commit -m "feat(vps): vigia externo com timer do systemd e healthchecks.io"
```

**O job `vps` no estado final** (para o grupo workflows conferir o encaixe no `ci.yml`):

````yaml
  vps:
    # A produção da VPS (Plano 11, spec §8) com a imagem que acabou de passar
    # no Trivy: o deploy/vps/compose.prod.yaml com o Caddy em modo local, e o
    # vigilume-entrada (deploy, rollback, backup) contra ele. Portas, Host,
    # IP do cliente, banco fechado, recusas e backup só cifrado.
    needs: image
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - name: Docker com o image store containerd, como na VPS
        # O vigilume-entrada recusa o store clássico: o image-id do protocolo
        # é o digest do manifesto, que só o containerd devolve. Antes do load:
        # trocar de store esconde as imagens do outro.
        run: |
          arq=/etc/docker/daemon.json
          atual=$(sudo cat "$arq" 2>/dev/null || echo '{}')
          jq '.features["containerd-snapshotter"] = true' <<<"$atual" | sudo tee "$arq" >/dev/null
          sudo systemctl restart docker
          docker info -f '{{.DriverStatus}}' | grep -q io.containerd.snapshotter.v1
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          name: imagens
          path: ${{ runner.temp }}
      - run: gunzip -c "$RUNNER_TEMP/imagens.tar.gz" | docker load
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: false
      - name: age
        # A mesma versão do backup.yml: o teste cifra e decifra o backup.
        run: go install "filippo.io/age/cmd/age@${AGE_VERSION}" "filippo.io/age/cmd/age-keygen@${AGE_VERSION}"
      - name: vigia externo (vigia.sh e as unidades do systemd)
        run: bash deploy/ci/checar-vigia.sh
      - name: checagens da VPS
        run: bash deploy/ci/checar-vps.sh "$IMAGEM:ci"
````


---

# Parte: workflows

# Plano 11 — parte "workflows"

Redator: grupo **workflows**. Spec: `docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`, §1
(critério 3), §3 (deploy), §4 (backup), §7 (Fly removido) e §8 (workflows). Obrigação 6 de
`docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md` (backup e deploy da VPS).

**Base:** `aebc4d0` (spec do Plano 11 sobre `a61fc0f`). **Validação:** tudo rodou numa cópia destacada
(`.superpowers/plano-11-spike/workflows`), uma tarefa por commit:

| Tarefa | Commit validado |
|---|---|
| W1 — `deploy/ci/vps.sh` (SSH com host key fixada, códigos, cabeçalho do age) e o teste com ssh falso | `a2cba94` |
| W2 — `deploy.yml` e `backup.yml` reescritos para a VPS; `checar-workflows.sh`; job `workflows` no CI | `697571b` |
| W3 — o Fly sai do repositório; `deploy/vps/db/Dockerfile`; perna "sem Fly" do checador | `c22a868` |

Os blocos "Crie"/"Aplique" abaixo são `git show`/`git diff` desses commits, sem retoque. O teste do passo 1
de cada tarefa foi rodado sobre o estado anterior e falhou pela razão escrita no passo 2. Cada sabotagem foi
feita por um script que copia o arquivo para `scratchpad/p11-workflows/`, troca o trecho, roda o teste e
restaura da cópia; depois de cada rodada, a suíte inteira voltou verde e `git status` limpo.
**Sabotagens: W1 18 vermelhas; W2 18 vermelhas nas checagens + 2 no actionlint; W3 10 vermelhas + 1 no
teste Go; 1 verde declarada** (49 vermelhas, 1 verde declarada).

Gates no estado final (`c22a868`): `actionlint` v1.7.12 com `shellcheck` 0.11.0 no PATH (o actionlint roda o
shellcheck em cada `run:`) limpo; `shellcheck deploy/ci/*.sh deploy/backup/backup.sh` limpo;
`sh deploy/ci/testar-vps.sh` (46 casos, com `sh` = dash e com `bash`); `sh deploy/ci/checar-workflows.sh`
verde **com `docs/deploy/fly.md` removido** (ver Interfaces: o arquivo é do grupo runbook; com ele presente, a
perna 6 fica vermelha só por causa dele, como deve); `gofmt -l` vazio, `go vet ./...`, `go build ./...`,
`go test -count=1 ./internal/config/ ./cmd/...` verdes. Nenhuma VPS foi tocada. Por fim, **o estado final foi
reconstruído a partir do texto desta parte** (blocos "Crie"/"Substitua" e diffs, tarefa por tarefa sobre
`aebc4d0`, com o `git rm` da W3) e comparado com `c22a868`: iguais.

**Validação do formato da imagem (medida, não suposta).** Num Docker 29.8.1 com o image store containerd (o
padrão de uma instalação nova do 29, que é o que a VPS terá), com um registry local (`registry:3` em
127.0.0.1:18501, derrubado e com o volume apagado no fim):
- o `.Id` de uma imagem **muda** num `docker save --platform linux/amd64 | docker load` (antes: o digest do
  índice multi-arch; depois: o do manifesto da plataforma). Num runner com o store clássico, o `docker save`
  ainda regenera o manifesto, e nenhum digest bate. Por isso o deploy **não** usa `docker save`;
- `crane pull --platform linux/amd64 --format oci <repo>@<índice de 2 plataformas>` grava o manifesto
  linux/amd64 byte a byte (o `index.json` do layout aponta para o digest que o índice do registry lista), e
  `tar -C layout -cf - . | docker load` responde `Loaded image ID: sha256:<esse digest>`, que é também o
  `docker image inspect -f '{{.Id}}'` da imagem carregada.

---

## (a) Tarefas desta parte

1. **W1** (`a2cba94`) — `deploy/ci/vps.sh`: o lado do Actions na conversa com a VPS (host key do secret, chave
   privada 600 apagada no fim, códigos 0/2/3/4/1/255 com mensagem própria, backup só aceito com o cabeçalho
   do age), testado por `deploy/ci/testar-vps.sh` com um ssh falso.
2. **W2** (`697571b`) — `deploy.yml` (manual por SHA, tag `:<sha>` → digest → manifesto linux/amd64 → layout
   OCI → `vps.sh deploy`) e `backup.yml` (06:17 UTC + manual, `vps.sh backup`, artifact 30 dias, 3 e 4 como
   hoje, desligado até `BACKUP_ENABLED=1`, sem `FLY_BACKUP_TOKEN`/`BACKUP_DATABASE_PASSWORD`/`AGE_RECIPIENT`);
   `deploy/ci/checar-workflows.sh`; job `workflows` no `ci.yml` (actionlint, shellcheck, checador, teste do
   vps.sh), exigido pelo `publish`. Sai o `TestFlyctlVersionIsTheSameInDeployAndBackup`.
3. **W3** (`c22a868`) — remove `fly.toml` e `deploy/fly/`; cria `deploy/vps/db/Dockerfile`; Dependabot,
   `ci.yml`, README, Dockerfile, compose, `initdb.sql`, `backup.sh` (só a mensagem), `user.go`, `.gitignore`,
   testes Go e `testdb.go` sem o Fly; perna 6 do checador ("nada do Fly.io").

Ordem: W1 → W2 → W3, no mesmo worktree.

## (b) Interfaces entre grupos

**Produz:**

| O quê | Onde | Quem consome |
|---|---|---|
| Protocolo de deploy, lado do runner: `ssh deploy@$VPS_HOST "deploy <sha40> <image-id>"` com o tar na entrada padrão. **`<image-id>` = digest do manifesto linux/amd64 do índice que a tag `:<sha>` aponta** (acordado com o controlador). **O tar é um layout OCI** (`index.json`, `oci-layout`, `blobs/`) com os blobs originais, **sem nome de imagem**: o `docker load` imprime `Loaded image ID: sha256:…`, e não `Loaded image: nome:tag` | W2, `deploy.yml` | **vps**: o `vigilume-entrada` confere `docker image inspect -f '{{.Id}}'` depois do load contra o argumento, sem depender do nome impresso, e etiqueta `vigilume:<sha>` pelo ID |
| Exigência: **Docker da VPS com o image store containerd** (padrão do Docker 29 numa instalação nova). Com o store clássico, o `.Id` carregado é o digest do config, e todo deploy sai 3 | W2 | **runbook**: verificação `docker info -f '{{.DriverStatus}}'` mostra `io.containerd.snapshotter.v1` antes do primeiro deploy |
| Protocolo de backup, lado do runner: `ssh deploy@$VPS_HOST backup`, saída padrão → `vigilume-AAAAMMDD.dump.age`, erro → log do job; a linha `Postgres: …` do log vai para o resumo do job; 0 e 4 só valem com o arquivo começando por `age-encryption.org/v1`; 3, 255 e outros apagam o arquivo | W1/W2 | **vps** (o verbo `backup` tem de mandar o cifrado **binário**, não `--armor`, e só ele, na saída padrão) |
| Secrets: `VPS_HOST` (nome ou IPv4, sem usuário nem porta; porta 22), `VPS_SSH_KEY` (privada ed25519 OpenSSH inteira), **`VPS_HOST_KEY` = a linha de `/etc/ssh/ssh_host_ed25519_key.pub` da VPS** (`ssh-ed25519 AAAA… [comentário]`), não a impressão digital. Usuário SSH fixo: `deploy` | W1 | **runbook** (passo dos secrets: como copiar a linha e conferir a impressão digital com `ssh-keygen -lf` antes de colar) |
| Variável `BACKUP_ENABLED=1`; a variável `PG_VOLUME_GB` deixa de existir (o disco é medido na VPS) | W2 | runbook |
| `deploy/vps/db/Dockerfile` (só o FROM com o digest, comentário ajustado); Dependabot olha `/deploy/vps/db` | W3 | **vps** cria o mesmo arquivo: **conflito esperado** (add/add). Resolver ficando com uma das versões, desde que a linha FROM seja idêntica; o `TestPostgres18DigestIsTheSameEverywhere` já lê `deploy/vps/db/Dockerfile` |
| `deploy/ci/checar-workflows.sh` perna 6: fica vermelha enquanto existir `docs/deploy/fly.md`, `fly.toml` ou `deploy/fly/`, ou qualquer `flyctl`, `fly.io`/`fly.dev`/`fly.toml`, `deploy/fly`, `superfly`, `FLY_*` ou `fly <subcomando>` fora de `docs/superpowers/**` | W3 | **runbook** (o `docs/deploy/vps.md` novo não pode citar esses termos; o `Fly-Client-IP` pode) e **vps** (idem nos arquivos de `deploy/vps/`) |
| Links para `docs/deploy/vps.md` (README, `compose.yaml`, `.gitignore`, `repositorio_test.go`), com as seções "Tornar o repositório público", "Secrets do GitHub" e "Disco" citadas | W2/W3 | **runbook** (os nomes de seção) |

**Consome:** `deploy/vps/vigilume-entrada` (grupo vps) com os códigos do protocolo das instruções comuns;
`docs/deploy/vps.md` (runbook).

**Conflitos de merge prováveis (sem conflito de lógica):** `deploy/vps/db/Dockerfile` (vps, add/add);
`compose.yaml` (linha 36, comentário), `deploy/compose/initdb.sql` (cabeçalho), `deploy/backup/backup.sh`
(mensagem do código 4) e `internal/config/compose_test.go` (se o vps puser o `compose.prod.yaml` na lista dos
quatro lugares do digest) com o grupo vps; `README.md` (seção "Deploy e operação") e
`internal/config/repositorio_test.go` com o grupo runbook. **Ordem de integração:** W3 junto com ou depois
da remoção de `docs/deploy/fly.md` pelo runbook — sozinha, a perna 6 do job `workflows` fica vermelha por
esse arquivo.

## (c) Desvios

| Pedido | Feito | Por quê |
|---|---|---|
| "baixa a imagem do GHCR, `docker save \| ssh`" | `crane pull --platform linux/amd64 --format oci` + `tar \| ssh` | Medido: o `docker save` muda o ID (store containerd) ou regenera o manifesto (store clássico), e a conferência da VPS daria 3. O crane (v0.22.1, `go install`, módulo conferido no checksum database do Go) grava os blobs como estão no registry; o job ainda confere que o `index.json` do layout aponta para o `IMAGE_ID`. Aceito pelo controlador |
| spec §3: `VPS_HOST_KEY` = "impressão digital fixada" | a chave pública inteira (`ssh-ed25519 AAAA…`) | O OpenSSH só fixa host key por `known_hosts`, que precisa da chave; a impressão digital serve ao dono para conferir antes de colar (runbook) |
| spec §4: `vigilume-<data>.sql.age` | `vigilume-AAAAMMDD.dump.age` | O conteúdo é `pg_dump --format=custom` (restaura com `pg_restore`, não com `psql`); o nome é o de antes, que o `.gitignore` e o teste dele já cobrem |
| spec §4: `backup.sh` "sem mudança" | só a mensagem do código 4 ("aumente o volume (fly volumes extend)" → "libere espaço ou aumente o disco") | Critério 3 da spec (nada do Fly); comportamento e códigos iguais |
| "sem SHA" só em `deploy.yml`/`backup.yml` | a perna 2 do checador cobre **todos** os workflows | Mais forte e já verdade no `ci.yml` |
| "`StrictHostKeyChecking=no`" | recusa `no`, `off` e `accept-new`, com `=` ou espaço, também em comentário, e `ssh-keyscan` | `accept-new` e `ssh-keyscan` são o mesmo "confiar no primeiro contato" |
| O `deploy.yml` antigo conferia `TRUSTED_PROXIES` no `fly.toml` | saiu | O valor passa a ser o IP fixo do Caddy no compose de produção (grupo vps, Obrigação 2); a checagem equivalente, se houver, é do vps |
| "sobrar referência a fly/flyctl" | busca pelo que só existe na plataforma (lista na perna 6), não pela palavra "Fly" | `Fly-Client-IP` é um cabeçalho que o `CLIENT_IP_HEADER` aceita e o Caddy **apaga** (Obrigação 1); os testes `…BehindFly…` e a prosa "no formato do Fly" do `clientip.go` descrevem esse formato. Ficam (verde declarada V1) |

## (d) Decisões (custo se errada)

1. **`<image-id>` = manifesto linux/amd64; tar em layout OCI.** Custo: se a VPS usar o store clássico, todo
   deploy sai 3 até trocar o store (a mensagem diz "image-id divergente", e o runbook confere o store antes).
2. **Só `ssh-ed25519` como host key** (`HostKeyAlgorithms=ssh-ed25519`). O Ubuntu 24.04 gera essa chave.
   Custo: uma VPS sem ela recusa com mensagem clara; trocar é uma linha.
3. **Recusas locais saem 2** (SHA, image-id, `VPS_HOST`, `VPS_HOST_KEY`, `VPS_SSH_KEY` inválidos), o mesmo
   código de "uso recusado" da VPS, sempre sem chamar o ssh. Custo: nenhum além de ler a mensagem.
4. **ssh 255 vira "falha de SSH"** com mensagem própria (conexão, chave ou host key). Um 255 que viesse da
   entrada da VPS (não vem: ela só usa 0–4) cairia nessa mensagem.
5. **Job `workflows` separado no CI**, com `actionlint` v1.7.12 por `go install` (versão à mão, listada no
   `dependabot.yml`), `shellcheck` da imagem do runner chamado direto (se faltar, o job fica vermelho em vez de
   o actionlint pular os `run:` em silêncio) e exigido pelo `publish`. Custo: ~1 min de Actions por execução.
6. **`crane` por `go install` com `setup-go` no deploy** (em vez de um binário baixado com checksum
   escrito à mão): o `go install` com versão fixa confere o módulo no sum.golang.org. Custo: ~20 s por deploy.
7. **Backup inválido é apagado** antes do upload (código ≠ 0/4, arquivo vazio ou sem o cabeçalho do age):
   texto claro nunca vira artifact. Custo: num 3, o cifrado que talvez servisse não é guardado (igual a hoje).

## (e) Review Focus

- **O ID da imagem entre runner e VPS** (decisão 1): o único ponto que o teste daqui não fecha de ponta a
  ponta, porque exige o `vigilume-entrada` do grupo vps e um Docker com store containerd. Conferir no
  primeiro deploy guiado: o log da VPS deve mostrar `Loaded image ID: sha256:<IMAGE_ID do job>`.
- **O ssh de verdade não foi exercitado** (não há `sshd` nesta máquina, e nenhuma VPS pode ser tocada): as
  opções foram conferidas com `ssh -G` (OpenSSH 9.6) e o resto com o ssh falso. O primeiro deploy guiado é a
  prova do `known_hosts` escrito como `<host> ssh-ed25519 AAAA…`.
- **`shellcheck` na imagem do runner** (decisão 5): se não estiver, o job `workflows` fica vermelho em
  "shellcheck dos scripts" no primeiro PR; aí, instalar por `apt-get` no passo.
- **Tamanho do tar pelo SSH**: a imagem tem ~7,6 MB comprimida; o layout OCI vai comprimido (camadas gzip do
  registry), sem problema de tempo no `timeout-minutes: 20`.
- A perna 6 do checador é por regex: um termo novo da plataforma (fora da lista) passaria. É a rede de
  segurança da remoção, não a remoção.

---

## (f) Tarefas

### Task W1: `deploy/ci/vps.sh` — SSH com host key fixada, códigos e cabeçalho do age, com ssh falso

Spec §3 (secrets `VPS_HOST`, `VPS_SSH_KEY`, `VPS_HOST_KEY`; sem `StrictHostKeyChecking=no`), §4 (backup só
cifrado; códigos 0/3/4) e §8 (lógica testável). Os workflows só chamam este script.

**Files:**
- Create: `deploy/ci/vps.sh`
- Create: `deploy/ci/testar-vps.sh`

**Interfaces:**
- Consumes: o protocolo das instruções comuns (`deploy <sha40> <image-id>` → 0/2/3/4/1; `backup` → 0/3/4/1),
  ssh 255 em falha de conexão.
- Produces: `sh deploy/ci/vps.sh deploy <sha> <image-id> < imagem.tar` e `sh deploy/ci/vps.sh backup
  <arquivo>`, com o ambiente `VPS_HOST`, `VPS_SSH_KEY`, `VPS_HOST_KEY`; sai com o código da VPS (ou 2 numa
  recusa local, antes do ssh). Mensagens `::error::…` por código.

- [ ] **Step 1: Escreva o teste**

Crie `deploy/ci/testar-vps.sh`:

````sh
#!/bin/sh
# Testes de deploy/ci/vps.sh com um ssh FALSO (nenhuma VPS é tocada). O job
# lint do CI os roda; localmente, da raiz do repositório:
#
#   sh deploy/ci/testar-vps.sh
#
# O ssh falso grava os argumentos, a entrada padrão e a chave que recebeu,
# escreve SSH_FALSO_SAIDA na saída padrão e SSH_FALSO_ERRO na de erro, e sai
# com SSH_FALSO_CODIGO.
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
VPS=$RAIZ/deploy/ci/vps.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
falhas=0
casos=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
# ler <arquivo>: o conteúdo, ou nada se não existe (um caso que falha não
# derruba os seguintes pelo set -e).
ler() { cat "$1" 2>/dev/null || true; }

mkdir "$tmp/bin"
cat >"$tmp/bin/ssh" <<'FALSO'
#!/bin/sh
: >"$SSH_FALSO_DIR/chamado"
printf '%s\n' "$@" >"$SSH_FALSO_DIR/args"
# A chave e o known_hosts, copiados enquanto existem (o script os apaga).
anterior=
for a in "$@"; do
    case $anterior in
        -i)
            cp "$a" "$SSH_FALSO_DIR/chave"
            stat -c %a "$a" >"$SSH_FALSO_DIR/chave.modo"
            echo "$a" >"$SSH_FALSO_DIR/chave.caminho"
            ;;
    esac
    case $a in
        UserKnownHostsFile=*) cp "${a#UserKnownHostsFile=}" "$SSH_FALSO_DIR/known_hosts" ;;
    esac
    anterior=$a
done
cat >"$SSH_FALSO_DIR/stdin"
printf '%s' "${SSH_FALSO_SAIDA:-}"
printf '%s' "${SSH_FALSO_ERRO:-}" >&2
exit "${SSH_FALSO_CODIGO:-0}"
FALSO
chmod +x "$tmp/bin/ssh"

CHAVE='-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
-----END OPENSSH PRIVATE KEY-----'
HOSTKEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl root@vps'
SHA=0123456789abcdef0123456789abcdef01234567
ID=sha256:1111111111111111111111111111111111111111111111111111111111111111
AGE='age-encryption.org/v1
-> X25519 abc
--- def
binario'

# rodar <nome do caso> <args do vps.sh...>: roda com o ambiente de um secret
# válido (sobrescrevível por variáveis exportadas antes) e deixa em $saida a
# saída e o erro juntos, e em $codigo o código.
rodar() {
    casos=$((casos + 1))
    caso=$1
    shift
    SSH_FALSO_DIR=$tmp/caso-$casos
    mkdir -p "$SSH_FALSO_DIR"
    export SSH_FALSO_DIR
    codigo=0
    saida=$(PATH="$tmp/bin:$PATH" RUNNER_TEMP="$tmp" \
        VPS_HOST="${T_HOST-vps.exemplo.com}" VPS_SSH_KEY="${T_CHAVE-$CHAVE}" VPS_HOST_KEY="${T_HOSTKEY-$HOSTKEY}" \
        sh "$VPS" "$@" <"${T_ENTRADA:-/dev/null}" 2>&1) || codigo=$?
}
esperar_codigo() {
    [ "$codigo" = "$1" ] || falhar "$caso: código $codigo, queria $1 (saída: $saida)"
}
esperar_texto() {
    case $saida in
        *"$1"*) ;;
        *) falhar "$caso: a saída não diz \"$1\" (saída: $saida)" ;;
    esac
}
esperar_ssh_nao_chamado() {
    [ ! -e "$SSH_FALSO_DIR/chamado" ] || falhar "$caso: chamou o ssh, e devia recusar antes"
}

# ---- deploy ----------------------------------------------------------------

printf 'tar-da-imagem\001\002fim' >"$tmp/imagem.tar"
T_ENTRADA=$tmp/imagem.tar SSH_FALSO_CODIGO=0 SSH_FALSO_ERRO='log do deploy
' rodar "deploy saudável" deploy "$SHA" "$ID"
esperar_codigo 0
esperar_texto "deploy de $SHA saudável na VPS"
esperar_texto "log do deploy"
cmp -s "$tmp/imagem.tar" "$SSH_FALSO_DIR/stdin" || falhar "$caso: a imagem não chegou byte a byte à entrada do ssh"
args=$(ler "$SSH_FALSO_DIR/args")
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 1)" = "deploy $SHA $ID" ] || falhar "$caso: comando remoto $(ler "$SSH_FALSO_DIR/args" | tail -n 1)"
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 2 | head -n 1)" = "deploy@vps.exemplo.com" ] || falhar "$caso: destino errado: $args"
for opcao in StrictHostKeyChecking=yes GlobalKnownHostsFile=/dev/null IdentitiesOnly=yes BatchMode=yes HostKeyAlgorithms=ssh-ed25519; do
    printf '%s\n' "$args" | grep -qx "$opcao" || falhar "$caso: sem -o $opcao"
done
printf '%s\n' "$args" | grep -qi 'StrictHostKeyChecking=no' && falhar "$caso: StrictHostKeyChecking=no"
printf '%s\n' "$args" | grep -qx -- '-F' || falhar "$caso: sem -F /dev/null"
[ "$(ler "$SSH_FALSO_DIR/known_hosts")" = "vps.exemplo.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl" ] ||
    falhar "$caso: known_hosts = $(ler "$SSH_FALSO_DIR/known_hosts")"
# O secret vem sem a quebra de linha final; o ssh recusa a chave sem ela.
printf '%s\n' "$CHAVE" | cmp -s - "$SSH_FALSO_DIR/chave" ||
    falhar "$caso: a chave entregue ao ssh não é a do secret terminada em quebra de linha"
[ "$(ler "$SSH_FALSO_DIR/chave.modo")" = 600 ] || falhar "$caso: a chave com modo $(ler "$SSH_FALSO_DIR/chave.modo"), queria 600"
[ -s "$SSH_FALSO_DIR/chave.caminho" ] && [ ! -e "$(ler "$SSH_FALSO_DIR/chave.caminho")" ] || falhar "$caso: a chave privada ficou no disco do runner"

for par in \
    "2|código 2: uso ou argumento inválido" \
    "3|código 3: image-id divergente" \
    "4|rollback feito" \
    "255|falha de SSH (código 255)" \
    "1|erro na VPS (código 1)" \
    "7|erro na VPS (código 7)"; do
    c=${par%%|*}
    T_ENTRADA=$tmp/imagem.tar SSH_FALSO_CODIGO=$c rodar "deploy, a VPS sai $c" deploy "$SHA" "$ID"
    esperar_codigo "$c"
    esperar_texto "${par#*|}"
    esperar_texto "::error::"
done

NL='
'
for ruim in "${SHA}0" "$SHA${NL}x" "x${NL}$SHA" "0123456789ABCDEF0123456789abcdef01234567" "$SHA-tools" "" "$SHA; reboot"; do
    rodar "deploy, SHA recusado ($ruim)" deploy "$ruim" "$ID"
    esperar_codigo 2
    esperar_texto "SHA inválido"
    esperar_ssh_nao_chamado
done
for ruim in "sha256:abc" "1111111111111111111111111111111111111111111111111111111111111111" "sha256:1111111111111111111111111111111111111111111111111111111111111111 x" "$ID${NL}x"; do
    rodar "deploy, image-id recusado ($ruim)" deploy "$SHA" "$ruim"
    esperar_codigo 2
    esperar_texto "image-id inválido"
    esperar_ssh_nao_chamado
done
rodar "deploy sem o image-id" deploy "$SHA"
esperar_codigo 2
esperar_ssh_nao_chamado
rodar "verbo desconhecido" reboot
esperar_codigo 2
esperar_ssh_nao_chamado

for ruim in "" "-oProxyCommand=x" "vps.exemplo.com${NL}x" "vps.exemplo.com -p 2222" "deploy@vps"; do
    T_HOST=$ruim rodar "VPS_HOST recusado ($ruim)" deploy "$SHA" "$ID"
    esperar_codigo 2
    esperar_texto "VPS_HOST"
    esperar_ssh_nao_chamado
done
for ruim in "" "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG" "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ" "vps.exemplo.com ssh-ed25519 AAAAC3Nza" "x${NL}$HOSTKEY"; do
    T_HOSTKEY=$ruim rodar "VPS_HOST_KEY recusado ($ruim)" deploy "$SHA" "$ID"
    esperar_codigo 2
    esperar_texto "VPS_HOST_KEY"
    esperar_ssh_nao_chamado
done
T_CHAVE="" rodar "VPS_SSH_KEY vazio" deploy "$SHA" "$ID"
esperar_codigo 2
esperar_texto "VPS_SSH_KEY"
esperar_ssh_nao_chamado

# ---- backup ----------------------------------------------------------------

arq=$tmp/vigilume-20260925.sql.age
SSH_FALSO_SAIDA=$AGE SSH_FALSO_ERRO='== dump
Postgres: 40% do disco
' rodar "backup conferido" backup "$arq"
esperar_codigo 0
esperar_texto "backup cifrado e conferido"
esperar_texto "== dump"
[ "$(ler "$arq")" = "$AGE" ] || falhar "$caso: o arquivo não é a saída padrão do ssh"
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 1)" = backup ] || falhar "$caso: comando remoto $(ler "$SSH_FALSO_DIR/args" | tail -n 1)"
case $(ler "$arq") in *"== dump"*) falhar "$caso: o log foi parar no arquivo do backup" ;; esac
: >"$tmp/resumo"
GITHUB_STEP_SUMMARY=$tmp/resumo SSH_FALSO_SAIDA=$AGE SSH_FALSO_ERRO='Postgres: 40% do disco
' rodar "backup, linha do disco no resumo" backup "$arq"
esperar_codigo 0
grep -qx 'Postgres: 40% do disco' "$tmp/resumo" || falhar "$caso: a linha do disco não foi para o resumo do job"

for claro in 'PGDMP binario do pg_dump' '-- PostgreSQL database dump' 'age-encryption.org/v2
x' ' age-encryption.org/v1
x' 'xage-encryption.org/v1'; do
    SSH_FALSO_SAIDA=$claro rodar "backup em claro recusado ($claro)" backup "$arq"
    esperar_codigo 1
    esperar_texto "não começa com o cabeçalho do age"
    [ ! -e "$arq" ] || falhar "$caso: o arquivo em claro ficou no disco"
done

SSH_FALSO_SAIDA='' rodar "backup vazio" backup "$arq"
esperar_codigo 1
esperar_texto "saída veio vazia"
[ ! -e "$arq" ] || falhar "$caso: o arquivo vazio ficou"

SSH_FALSO_SAIDA=$AGE SSH_FALSO_CODIGO=4 rodar "backup com disco acima de 85%" backup "$arq"
esperar_codigo 4
esperar_texto "acima de 85%"
[ "$(ler "$arq")" = "$AGE" ] || falhar "$caso: o backup conferido do código 4 não ficou para ser guardado"

SSH_FALSO_SAIDA='PGDMP' SSH_FALSO_CODIGO=4 rodar "backup código 4 em claro" backup "$arq"
esperar_codigo 1
[ ! -e "$arq" ] || falhar "$caso: o arquivo em claro ficou"

for par in \
    "3|restauração conferida (código 3)" \
    "255|falha de SSH (código 255)" \
    "1|falhou na VPS (código 1)"; do
    c=${par%%|*}
    SSH_FALSO_SAIDA=$AGE SSH_FALSO_CODIGO=$c rodar "backup, a VPS sai $c" backup "$arq"
    esperar_codigo "$c"
    esperar_texto "${par#*|}"
    [ ! -e "$arq" ] || falhar "$caso: sobrou o arquivo de um backup que não serve"
done

T_HOSTKEY="" rodar "backup sem VPS_HOST_KEY" backup "$arq"
esperar_codigo 2
esperar_ssh_nao_chamado
rodar "backup sem arquivo" backup
esperar_codigo 2
esperar_ssh_nao_chamado

if [ "$falhas" -gt 0 ]; then
    echo "$falhas falha(s) em $casos casos" >&2
    exit 1
fi
echo "vps.sh: $casos casos, tudo certo"
````

- [ ] **Step 2: Rode e veja falhar**

Run: `sh deploy/ci/testar-vps.sh`

Expected: FAIL, `84 falha(s) em 46 casos`, todas com `sh: 0: cannot open …/deploy/ci/vps.sh: No such file`
(o script ainda não existe).

- [ ] **Step 3: Implemente**

Crie `deploy/ci/vps.sh`:

````sh
#!/bin/sh
# O lado do GitHub Actions na conversa com a VPS (Plano 11, spec §3 e §4). Os
# workflows deploy.yml e backup.yml só chamam este script; a lógica (SSH com a
# host key fixada, códigos de saída, cabeçalho do age) fica aqui, onde
# deploy/ci/testar-vps.sh a testa com um ssh falso.
#
#   sh deploy/ci/vps.sh deploy <sha 40 hex> <image-id sha256:…> < imagem.tar
#   sh deploy/ci/vps.sh backup <arquivo de saída>
#
# Ambiente (secrets de repositório):
#   VPS_HOST      nome ou IPv4 da VPS
#   VPS_SSH_KEY   a chave PRIVADA ed25519 do usuário deploy (só para isto)
#   VPS_HOST_KEY  a chave pública do servidor, como está no
#                 /etc/ssh/ssh_host_ed25519_key.pub da VPS ("ssh-ed25519 AAAA…")
#
# O outro lado é o /usr/local/sbin/vigilume-entrada (deploy/vps/), que o
# authorized_keys do deploy força: "deploy …" sai 0 (saudável), 2 (uso ou
# argumento recusado), 3 (imagem divergente), 4 (não ficou saudável; rollback
# feito) ou 1 (outro erro); "backup" manda o dump cifrado pela saída padrão e
# o log pela de erro, e sai 0, 3, 4 (os do backup.sh) ou 1. O ssh sai 255
# quando a conexão falha. Este script sai com o MESMO código, depois de dizer
# o que ele significa.
set -eu

erro() { echo "::error::$*"; }

# casa <valor> <ERE>: o valor INTEIRO casa, e numa linha só (o grep -x casaria
# uma das linhas de um valor com quebra de linha).
casa() {
    case $1 in *'
'*) return 1 ;; esac
    printf '%s\n' "$1" | grep -Eqx "$2"
}

uso() {
    erro "uso: vps.sh deploy <sha> <image-id> < imagem.tar | vps.sh backup <arquivo>"
    exit 2
}

# Recusas locais saem 2, como as da VPS: nada foi enviado.
recusar() {
    erro "$*"
    exit 2
}

conferir_ambiente() {
    host=${VPS_HOST:-}
    # Só letras, dígitos, ponto e hífen, sem hífen na ponta: um valor que
    # começasse com "-" viraria opção do ssh.
    if ! casa "$host" '[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?'; then
        recusar "VPS_HOST ausente ou inválido: use o nome ou o IPv4 da VPS"
    fi
    # A host key vem do secret, nunca do primeiro contato (nada de
    # StrictHostKeyChecking=no nem de ssh-keyscan): quem se puser no meio do
    # caminho não recebe a imagem nem entrega um backup falso.
    if ! casa "${VPS_HOST_KEY:-}" 'ssh-ed25519 AAAA[A-Za-z0-9+/]+={0,3}( .*)?'; then
        recusar "VPS_HOST_KEY ausente ou inválido: cole a linha de /etc/ssh/ssh_host_ed25519_key.pub da VPS (ssh-ed25519 AAAA…)"
    fi
    case ${VPS_SSH_KEY:-} in
        *'BEGIN OPENSSH PRIVATE KEY'*) ;;
        *) recusar "VPS_SSH_KEY ausente ou inválido: cole a chave privada inteira (-----BEGIN OPENSSH PRIVATE KEY-----)" ;;
    esac

    dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/vps.XXXXXX")
    # A chave privada sai do disco do runner em qualquer saída do script.
    trap 'rm -rf "$dir"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    chave=$dir/chave
    conhecidos=$dir/known_hosts
    (
        umask 077
        # O secret colado sem a quebra de linha final vira "invalid format".
        printf '%s\n' "$VPS_SSH_KEY" >"$chave"
        tipo_e_chave=$(printf '%s' "$VPS_HOST_KEY" | cut -d' ' -f1-2)
        printf '%s %s\n' "$host" "$tipo_e_chave" >"$conhecidos"
    )
}

# -F /dev/null: nenhuma configuração do runner muda o que está escrito aqui.
conectar() {
    ssh -F /dev/null \
        -i "$chave" -o IdentitiesOnly=yes \
        -o UserKnownHostsFile="$conhecidos" -o GlobalKnownHostsFile=/dev/null \
        -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 \
        -o BatchMode=yes -o ConnectTimeout=20 \
        -o ServerAliveInterval=15 -o ServerAliveCountMax=4 \
        -o LogLevel=ERROR \
        "deploy@$host" "$1"
}

resumo() {
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then echo "$*" >>"$GITHUB_STEP_SUMMARY"; fi
}

fazer_deploy() {
    [ $# -eq 2 ] || uso
    sha=$1
    id=$2
    casa "$sha" '[0-9a-f]{40}' ||
        recusar "SHA inválido: use o SHA completo do commit (40 hex minúsculos)"
    casa "$id" 'sha256:[0-9a-f]{64}' ||
        recusar "image-id inválido: queria sha256: e 64 hex"
    conferir_ambiente

    # A imagem (tar) vai pela entrada padrão deste script até a VPS.
    if conectar "deploy $sha $id"; then codigo=0; else codigo=$?; fi
    case $codigo in
        0)
            echo "deploy de $sha saudável na VPS"
            resumo "Deploy de \`$sha\` (\`$id\`): saudável."
            ;;
        2) erro "a VPS recusou o pedido (código 2: uso ou argumento inválido); nada mudou em produção" ;;
        3) erro "a imagem que chegou à VPS não é a esperada (código 3: image-id divergente de $id); nada mudou em produção" ;;
        4) erro "a versão nova não ficou saudável (código 4): a VPS voltou para a versão anterior (rollback feito); o log do app está acima" ;;
        255) erro "falha de SSH (código 255): conexão, VPS_SSH_KEY ou VPS_HOST_KEY; confira a mensagem do ssh acima antes de tentar de novo" ;;
        *) erro "erro na VPS (código $codigo); o log está acima" ;;
    esac
    exit "$codigo"
}

# O age escreve esta linha no começo de todo arquivo cifrado (formato
# binário). O dump em claro do pg_dump começa com "PGDMP"; um SQL, com
# comentário ou comando. Só o cabeçalho prova que o que chegou está cifrado.
CABECALHO_AGE=age-encryption.org/v1

cifrado() {
    [ -s "$1" ] || return 1
    primeira=$(head -n 1 "$1" | head -c 64)
    [ "$primeira" = "$CABECALHO_AGE" ]
}

fazer_backup() {
    [ $# -eq 1 ] || uso
    saida=$1
    conferir_ambiente

    # A saída padrão do ssh (o cifrado) vai para o arquivo; a de erro (o log
    # do backup) aparece no job e fica copiada para achar a linha do disco.
    {
        {
            if conectar backup 2>&1 >&3 3>&-; then c=0; else c=$?; fi
            echo "$c" >"$dir/codigo"
        } | tee "$dir/log" >&2
    } 3>"$saida"
    codigo=$(cat "$dir/codigo")
    grep '^Postgres: ' "$dir/log" | while IFS= read -r linha; do resumo "$linha"; done || true

    case $codigo in
        0 | 4)
            if [ ! -s "$saida" ]; then
                rm -f "$saida"
                erro "o backup saiu com código $codigo mas a saída veio vazia: nada foi guardado"
                exit 1
            fi
            if ! cifrado "$saida"; then
                rm -f "$saida"
                erro "a saída do backup não começa com o cabeçalho do age ($CABECALHO_AGE): não é um arquivo cifrado, e foi apagada sem ser guardada"
                exit 1
            fi
            if [ "$codigo" -eq 4 ]; then
                erro "disco da VPS acima de 85% (código 4): o backup de hoje foi conferido e será guardado, mas aumente o disco ou libere espaço"
                exit 4
            fi
            echo "backup cifrado e conferido: $saida ($(wc -c <"$saida") bytes)"
            resumo "Backup conferido: \`$(basename "$saida")\`."
            exit 0
            ;;
        3) erro "o backup não passou na restauração conferida (código 3): o dump não serve e nada foi guardado; o log está acima" ;;
        255) erro "falha de SSH (código 255): conexão, VPS_SSH_KEY ou VPS_HOST_KEY; nenhum backup foi feito" ;;
        *) erro "o backup falhou na VPS (código $codigo): nada foi guardado; o log está acima" ;;
    esac
    rm -f "$saida"
    exit "$codigo"
}

[ $# -ge 1 ] || uso
verbo=$1
shift
case $verbo in
    deploy) fazer_deploy "$@" ;;
    backup) fazer_backup "$@" ;;
    *) uso ;;
esac
````

- [ ] **Step 4: Rode e veja passar**

Run:

```bash
sh deploy/ci/testar-vps.sh
bash deploy/ci/testar-vps.sh
shellcheck deploy/ci/vps.sh deploy/ci/testar-vps.sh
```

Expected: `vps.sh: 46 casos, tudo certo` nas duas shells; shellcheck sem saída.

- [ ] **Step 5: Sabotagens (em `deploy/ci/vps.sh`; restaure de backup depois de cada uma)**

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| S1 | `StrictHostKeyChecking=yes` → `=no` | `sem -o StrictHostKeyChecking=yes` e `StrictHostKeyChecking=no` |
| S2 | Tire `-o GlobalKnownHostsFile=/dev/null` | `sem -o GlobalKnownHostsFile=/dev/null` |
| S3 | known_hosts sem o host (`printf '%s\n' "$tipo_e_chave"`) | `known_hosts = ssh-ed25519 AAAA…` |
| S4 | `umask 077` → `umask 022` | `a chave com modo 644, queria 600` |
| S5 | Tire o `trap 'rm -rf "$dir"' EXIT` | `a chave privada ficou no disco do runner` |
| S6 | Tire a recusa de quebra de linha do `casa` | `SHA recusado (<sha>\nx)` e as outras pernas com quebra de linha |
| S7 | Mensagem do 4 igual à genérica | `deploy, a VPS sai 4: a saída não diz "rollback feito"` |
| S8 | `exit "$codigo"` do deploy → `exit 0` | `deploy, a VPS sai 2/3/4/255/1/7: código 0` |
| S9 | `cifrado` sempre verdadeiro | `backup em claro recusado (PGDMP…)`: código 0, e `o arquivo em claro ficou no disco` |
| S10 | Tire o `rm -f "$saida"` do fim do backup | `backup, a VPS sai 3/255/1: sobrou o arquivo de um backup que não serve` |
| S11 | Código 4 do backup sai 0 | `backup com disco acima de 85%: código 0, queria 4` |
| S12 | `2>&1 >&3` → `>&3 2>&1` (log no arquivo) | `o log foi parar no arquivo do backup`, `a saída não diz "== dump"` |
| S13 | Tire `-o HostKeyAlgorithms=ssh-ed25519` | `sem -o HostKeyAlgorithms=ssh-ed25519` |
| S14 | Não mande a linha `Postgres:` ao resumo | `a linha do disco não foi para o resumo do job` |
| S15 | Pule a validação do `VPS_HOST` | `VPS_HOST recusado (-oProxyCommand=x)`: código 0 e `chamou o ssh` |
| S16 | Tire `-F /dev/null` | `sem -F /dev/null` |
| S17 | Grave a chave sem a quebra de linha final | `a chave entregue ao ssh não é a do secret terminada em quebra de linha` |
| S18 | `conectar "deploy …" </dev/null` | `a imagem não chegou byte a byte à entrada do ssh` |

A validação achou dois testes fracos, já fortalecidos aqui: a S7 original (só encurtar a mensagem) ficava
verde porque o texto esperado continuava lá — a sabotagem certa troca a mensagem do 4 pela genérica; e a S17
ficava verde porque o `$(…)` do teste comia a quebra de linha — o teste agora compara com `cmp`.

- [ ] **Step 6: Commit**

```bash
git add deploy/ci/vps.sh deploy/ci/testar-vps.sh
git commit -m "feat(deploy): vps.sh fala com a VPS por SSH com host key fixada, com códigos e cabeçalho do age testados"
```

---

### Task W2: `deploy.yml` e `backup.yml` para a VPS; `checar-workflows.sh`; job `workflows` no CI

Spec §3, §4 e §8. O `deploy.yml` reaproveita os passos "só da main", "conferir o SHA", "o commit está na
main" e "resolver o digest da tag :<sha>" do atual; saem os passos do Fly e o do `TRUSTED_PROXIES` do
`fly.toml`. O `backup.yml` perde o Go, o age, o flyctl, o túnel e o Postgres descartável do runner: tudo isso
passa para a VPS.

**Files:**
- Create: `deploy/ci/checar-workflows.sh`
- Modify (reescrito): `.github/workflows/deploy.yml`, `.github/workflows/backup.yml`
- Modify: `.github/workflows/ci.yml` (env `ACTIONLINT_VERSION`, job `workflows`, `needs` do `publish`)
- Modify: `.github/dependabot.yml` (comentário das versões à mão)
- Modify: `internal/config/compose_test.go` (sai `TestFlyctlVersionIsTheSameInDeployAndBackup`)

**Interfaces:**
- Consumes: `deploy/ci/vps.sh` (W1).
- Produces: secrets `VPS_HOST`, `VPS_SSH_KEY`, `VPS_HOST_KEY`; variável `BACKUP_ENABLED`; artifact
  `vigilume-backup-<run_id>` com `vigilume-AAAAMMDD.dump.age`, 30 dias; job `workflows`.

- [ ] **Step 1: Escreva o teste**

A perna 6 (Fly) entra na W3. Crie `deploy/ci/checar-workflows.sh`:

````sh
#!/bin/sh
# Checagens dos workflows do GitHub Actions que o actionlint não faz (Plano 11,
# spec §8). Workflows não rodam fora do GitHub: o que dá para provar aqui é o
# texto deles. O job "workflows" do CI roda este script; localmente, da raiz:
#
#   sh deploy/ci/checar-workflows.sh
#
# A lógica de shell do deploy e do backup não está nos workflows: está em
# deploy/ci/vps.sh, testada por deploy/ci/testar-vps.sh.
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
cd "$RAIZ"
WF=.github/workflows
DEPLOY=$WF/deploy.yml
BACKUP=$WF/backup.yml
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
# Os laços com pipe rodam num subshell: as falhas de dentro deles vão como
# linhas para este arquivo, contadas depois.
marcas=$(mktemp)
trap 'rm -f "$marcas"' EXIT
contar_marcas() {
    if [ -s "$marcas" ]; then falhas=$((falhas + $(wc -l <"$marcas"))); fi
    : >"$marcas"
}

# 1. Nenhum workflow aceita a host key do primeiro servidor que responder.
#    O ssh aceita "no", "off" e "accept-new" (este último também confia no
#    primeiro contato); ssh-keyscan no job é o mesmo erro por outro caminho.
if grep -EniH 'StrictHostKeyChecking[[:space:]=]+"?(no|off|accept-new)' "$WF"/*.yml; then
    falhar "workflow com StrictHostKeyChecking desligado (acima): a host key vem do secret VPS_HOST_KEY"
fi
if grep -EniH 'ssh-keyscan' "$WF"/*.yml; then
    falhar "workflow com ssh-keyscan (acima): a host key vem do secret VPS_HOST_KEY, nunca da rede"
fi

# 2. Toda action fixada por SHA completo (o comentário diz a tag), em TODOS
#    os workflows: uma tag móvel roda código alheio com o GITHUB_TOKEN e com
#    os secrets da VPS (D20 do Plano 7).
for f in "$WF"/*.yml; do
    grep -En '^[[:space:]-]*uses:' "$f" | while IFS= read -r linha; do
        ref=$(printf '%s\n' "$linha" | sed -E 's/^[0-9]+:[[:space:]-]*uses:[[:space:]]*//; s/[[:space:]]+#.*$//; s/^["'\'']//; s/["'\'']$//')
        case $ref in
            ./*) continue ;; # action local, do próprio commit
        esac
        if ! printf '%s\n' "$ref" | grep -Eqx '[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}'; then
            echo "FALHOU: $f:${linha%%:*}: action sem SHA completo: $ref" >&2
            echo x >>"$marcas"
        fi
    done
done
contar_marcas

# 3. Deploy: só à mão, por SHA, sem cancelar um deploy no meio, com o mínimo
#    de permissão, e com a conversa com a VPS inteira no vps.sh.
tem() { grep -Eq "$2" "$1" || falhar "$1: $3"; }
# nao_tem olha só o código: um comentário pode citar o que é proibido.
nao_tem() {
    if grep -En "$2" "$1" | grep -Ev '^[0-9]+:[[:space:]]*#' | sed "s|^|$1:|" | grep .; then
        falhar "$1: $3"
    fi
}

gatilhos=$(sed -n '/^on:/,/^[a-z]/p' "$DEPLOY" | grep -E '^  [a-z_]+:' | tr -d ' :' | tr '\n' ' ')
[ "$gatilhos" = "workflow_dispatch " ] || falhar "$DEPLOY: gatilhos \"$gatilhos\", queria só workflow_dispatch"
tem "$DEPLOY" '^      sha:$' "sem o input sha"
for f in "$DEPLOY" "$BACKUP"; do
    tem "$f" '^concurrency:$' "sem concurrency"
    tem "$f" '^  cancel-in-progress: false$' "concurrency tem de ser cancel-in-progress: false"
    nao_tem "$f" 'cancel-in-progress: true' "um deploy ou backup não é cancelado no meio"
    nao_tem "$f" ':[[:space:]]*write' "permissão de escrita (acima): o job só lê"
    nao_tem "$f" '(^|[^.])\bssh[[:space:]]+(-|deploy@|"\$)' "ssh direto no workflow (acima): use deploy/ci/vps.sh"
    nao_tem "$f" 'VPS_SSH_KEY\}\}.*>' "a chave privada gravada pelo workflow (acima): o vps.sh a grava e apaga"
done
perms=$(sed -n '/^permissions:/,/^[a-z]/p' "$DEPLOY" | grep -E '^  ' | tr -d ' ' | sort | tr '\n' ' ')
[ "$perms" = "contents:read packages:read " ] || falhar "$DEPLOY: permissions \"$perms\", queria contents: read e packages: read"
perms=$(sed -n '/^permissions:/,/^[a-z]/p' "$BACKUP" | grep -E '^  ' | tr -d ' ' | sort | tr '\n' ' ')
[ "$perms" = "contents:read " ] || falhar "$BACKUP: permissions \"$perms\", queria só contents: read"
# shellcheck disable=SC2016 # o $SHA e o $IMAGE_ID são texto do workflow
tem "$DEPLOY" 'sh deploy/ci/vps\.sh deploy "\$SHA" "\$IMAGE_ID"$' "a entrega não passa por deploy/ci/vps.sh deploy"
tem "$BACKUP" 'sh deploy/ci/vps\.sh backup ' "o backup não passa por deploy/ci/vps.sh backup"

# 4. Backup: diário às 06:17 UTC e à mão, desligado até BACKUP_ENABLED=1,
#    artifact por 30 dias.
tem "$BACKUP" "^    - cron: '17 6 \\* \\* \\*'" "sem o agendamento diário das 06:17 UTC"
n=$(grep -Ec '^[[:space:]]*- cron:' "$BACKUP" || true)
[ "$n" = 1 ] || falhar "$BACKUP: $n agendamentos, queria só o das 06:17 UTC"
tem "$BACKUP" '^  workflow_dispatch:' "sem o disparo manual"
tem "$BACKUP" "^    if: vars\\.BACKUP_ENABLED == '1'$" "o job não está desligado até BACKUP_ENABLED=1"
tem "$BACKUP" '^          retention-days: 30$' "o artifact não fica 30 dias"
tem "$BACKUP" '^          if-no-files-found: error$' "o upload sem arquivo tem de falhar"

# 5. Secrets: só os três da VPS (e o GITHUB_TOKEN do GHCR, no deploy). A
#    chave pública do age mora na VPS; a senha do banco e tokens de outra
#    plataforma não passam pelo GitHub.
for f in "$DEPLOY" "$BACKUP"; do
    grep -Eo 'secrets\.[A-Za-z_]+' "$f" | sort -u | while IFS= read -r s; do
        case $s in
            secrets.VPS_HOST | secrets.VPS_SSH_KEY | secrets.VPS_HOST_KEY) ;;
            secrets.GITHUB_TOKEN) [ "$f" = "$DEPLOY" ] || echo "FALHOU: $f: $s" >&2 ;;
            *) echo "FALHOU: $f: secret fora da lista: $s" >&2 ;;
        esac
    done
done 2>&1 | tee "$marcas" >&2
contar_marcas

if [ "$falhas" -gt 0 ]; then
    echo "$falhas falha(s) nos workflows" >&2
    exit 1
fi
echo "workflows: tudo certo"
````

- [ ] **Step 2: Rode e veja falhar**

Run: `sh deploy/ci/checar-workflows.sh`

Expected: FAIL, `6 falha(s) nos workflows`:
- `deploy.yml: a entrega não passa por deploy/ci/vps.sh deploy`;
- `backup.yml: o backup não passa por deploy/ci/vps.sh backup`;
- `deploy.yml: secret fora da lista: secrets.FLY_DEPLOY_TOKEN`;
- `backup.yml: secret fora da lista:` `AGE_RECIPIENT`, `BACKUP_DATABASE_PASSWORD`, `FLY_BACKUP_TOKEN`.

As outras pernas (host key, SHA, gatilhos, concurrency, permissões, cron, `BACKUP_ENABLED`, retenção) já
valem nos workflows antigos: regressão, provada pelas sabotagens C1–C18.

- [ ] **Step 3: Implemente**

Substitua `.github/workflows/deploy.yml` por:

````yaml
name: deploy
# Deploy na VPS, SÓ à mão (Plano 11, spec §3): Actions → deploy → Run
# workflow, com o SHA completo (40 hex) do commit cuja imagem o job publish
# do ci publicou, a partir da main. Runbook: docs/deploy/vps.md.
#
# Quem pode disparar: só quem tem acesso de escrita ao repositório (hoje, só
# o dono). No GitHub Free com repositório privado não há environment com
# revisor obrigatório; essa é a trava que existe. Quando o repositório abrir,
# um environment "production" com revisor pode ser acrescentado (runbook,
# "Tornar o repositório público").
#
# O input é o SHA, não um digest: o publish só dá a tag :<sha> DEPOIS do
# Trivy. Um digest qualquer do pacote podia ser um que subiu e reprovou no
# Trivy, o de um publish cancelado no meio, ou o da variante tools; a tag
# :<sha> da imagem runtime só existe para o que passou. O job resolve a tag
# no digest uma vez e daí em diante só usa o digest.
#
# A VPS não fala com o GHCR (o pacote é privado, e ela guardaria um token do
# GitHub): o job baixa a imagem linux/amd64 e a entrega por SSH, na entrada
# padrão de `deploy <sha> <image-id>` (deploy/ci/vps.sh). O <image-id> é o
# digest do manifesto linux/amd64 do índice publicado; o tar vai em layout
# OCI com os blobs originais (crane), e o `docker load` da VPS (image store
# containerd, o padrão do Docker 29) dá à imagem exatamente esse ID. Um
# `docker save` do runner não serve: ele regenera o manifesto, e o ID muda.
on:
  workflow_dispatch:
    inputs:
      sha:
        description: 'SHA completo (40 hex) do commit publicado, do resumo do job publish'
        required: true
        type: string

permissions:
  contents: read
  packages: read

# Um deploy nunca é cancelado no meio por outro: o segundo espera.
concurrency:
  group: deploy
  cancel-in-progress: false

# shell: bash dá `bash -eo pipefail` a todo run (o padrão é só `bash -e`).
defaults:
  run:
    shell: bash

env:
  # Versão fixada: o `go install` confere o módulo no checksum database do Go
  # (sum.golang.org). O Dependabot não a atualiza (fica à mão, dependabot.yml).
  CRANE_VERSION: v0.22.1

jobs:
  deploy:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    env:
      SHA: ${{ inputs.sha }}
      # Nome repetido à mão: IMAGEM do ci.yml e image do compose.yaml.
      ORIGEM: ghcr.io/bipoxdxd/vigilume
    steps:
      - name: só da main
        # Só da main: o vps.sh que conversa com a VPS é o que passou por PR.
        # Um passo que falha, e não um `if:` no job: disparado de outro
        # branch, o deploy fica vermelho (recusado), e não "skipped".
        run: |
          if [ "$GITHUB_REF" != refs/heads/main ]; then
            echo "deploy só da main (disparado de $GITHUB_REF)" >&2
            exit 1
          fi
      - name: conferir o SHA
        # O input vai para o shell só por variável de ambiente, e só no
        # formato de um SHA completo: nada de texto livre na linha de comando.
        # Exatamente 40 hex também exclui as tags da variante tools
        # (<sha>-tools), que nunca vai para produção.
        run: |
          if ! printf '%s' "$SHA" | grep -Eqx '[0-9a-f]{40}'; then
            echo "SHA inválido: use o SHA completo do commit (40 hex minúsculos)" >&2
            exit 1
          fi
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          # O histórico inteiro, com origin/main: o passo seguinte precisa dele.
          fetch-depth: 0
          persist-credentials: false
      - name: o commit está na main
        # Uma tag v* num commit fora da main também publica :<sha> (ci.yml),
        # sem que ele tenha passado por PR. Só vai para produção o que a main
        # contém.
        run: |
          if ! git cat-file -e "$SHA^{commit}" 2>/dev/null; then
            echo "o commit $SHA não existe neste repositório" >&2
            exit 1
          fi
          if ! git merge-base --is-ancestor "$SHA" origin/main; then
            echo "o commit $SHA não está na main: só se faz deploy do que passou por ela" >&2
            exit 1
          fi

      - uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4.4.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: '1.26'
          cache: false
      - name: crane
        run: go install "github.com/google/go-containerregistry/cmd/crane@${CRANE_VERSION}"
      - uses: docker/login-action@dbcb813823bdd20940b903addbd779551569679f # v4.6.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: resolver o digest da tag :<sha>
        # Sem a tag (commit não publicado, publish reprovado no Trivy ou
        # cancelado antes das tags), o inspect falha e o job para aqui.
        run: |
          if ! digest=$(docker buildx imagetools inspect "$ORIGEM:$SHA" --format '{{json .Manifest}}' | jq -r .digest) \
            || ! printf '%s' "$digest" | grep -Eqx 'sha256:[0-9a-f]{64}'; then
            echo "não achei a imagem $ORIGEM:$SHA (o publish desse commit terminou?)" >&2
            exit 1
          fi
          echo "imagem: $ORIGEM:$SHA = $digest"
          echo "DIGEST=$digest" >> "$GITHUB_ENV"

      - name: manifesto linux/amd64
        # A VPS é amd64. Exatamente uma entrada linux/amd64 no índice, ou
        # nada: um índice estranho para o deploy aqui, e não na VPS.
        run: |
          id=$(docker buildx imagetools inspect --raw "$ORIGEM@$DIGEST" \
            | jq -r '[.manifests[]? | select(.platform.os == "linux" and .platform.architecture == "amd64")] | if length == 1 then .[0].digest else empty end')
          if ! printf '%s' "$id" | grep -Eqx 'sha256:[0-9a-f]{64}'; then
            echo "o índice $ORIGEM@$DIGEST não tem exatamente um manifesto linux/amd64" >&2
            exit 1
          fi
          echo "manifesto linux/amd64: $id"
          echo "IMAGE_ID=$id" >> "$GITHUB_ENV"

      - name: baixar a imagem (layout OCI)
        # O crane grava o manifesto e as camadas como estão no registry; a
        # conferência abaixo prova que o tar leva o manifesto do IMAGE_ID.
        run: |
          crane pull --platform linux/amd64 --format oci "$ORIGEM@$DIGEST" "$RUNNER_TEMP/imagem"
          no_tar=$(jq -r '[.manifests[].digest] | if length == 1 then .[0] else "vários" end' "$RUNNER_TEMP/imagem/index.json")
          if [ "$no_tar" != "$IMAGE_ID" ]; then
            echo "o layout baixado leva $no_tar, e não o manifesto $IMAGE_ID" >&2
            exit 1
          fi
          du -sh "$RUNNER_TEMP/imagem"

      # Secrets de repositório (runbook, "Secrets do GitHub"): VPS_HOST (nome
      # ou IP), VPS_SSH_KEY (a chave privada ed25519 do usuário deploy, só
      # para isto), VPS_HOST_KEY (a linha de /etc/ssh/ssh_host_ed25519_key.pub
      # da VPS). O vps.sh fixa a host key (nunca a aceita às cegas), sai
      # com o código da VPS e diz o que ele significa: 2 recusado, 3 imagem
      # divergente, 4 não ficou saudável (rollback feito), 255 SSH.
      - name: entregar na VPS
        env:
          VPS_HOST: ${{ secrets.VPS_HOST }}
          VPS_SSH_KEY: ${{ secrets.VPS_SSH_KEY }}
          VPS_HOST_KEY: ${{ secrets.VPS_HOST_KEY }}
        run: tar -C "$RUNNER_TEMP/imagem" -cf - . | sh deploy/ci/vps.sh deploy "$SHA" "$IMAGE_ID"
````

Substitua `.github/workflows/backup.yml` por:

````yaml
name: backup
# Backup lógico diário do Postgres de produção, com restauração conferida
# (D32 do Plano 7) e aviso de disco (D35), feito NA VPS (Plano 11, spec §4):
# o verbo `backup` do vigilume-entrada roda o deploy/backup/backup.sh num
# Postgres descartável, cifra com a chave PÚBLICA do age que mora na VPS
# (/opt/vigilume/age-recipient) e manda só o cifrado pela saída padrão. A
# chave privada fica com o dono, fora do GitHub e da VPS.
#
# Desligado até o dono ligar: o job só roda com a variável de repositório
# BACKUP_ENABLED=1 (runbook docs/deploy/vps.md). Antes disso, o agendamento
# diário termina em "skipped", sem gastar minutos nem falhar.
on:
  schedule:
    - cron: '17 6 * * *' # 06:17 UTC, fora da hora cheia em que o GitHub enfileira tudo
  workflow_dispatch:

permissions:
  contents: read

# Dois backups ao mesmo tempo disputariam o Postgres descartável da VPS;
# nenhum é cancelado no meio.
concurrency:
  group: backup
  cancel-in-progress: false

# shell: bash dá `bash -eo pipefail` a todo run (o padrão é só `bash -e`).
defaults:
  run:
    shell: bash

jobs:
  backup:
    if: vars.BACKUP_ENABLED == '1'
    runs-on: ubuntu-latest
    # Secrets de REPOSITÓRIO (no GitHub Free com repositório privado não há
    # environment), os mesmos do deploy: VPS_HOST, VPS_SSH_KEY, VPS_HOST_KEY.
    # Nenhuma senha do banco nem chave do age passa pelo GitHub.
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          sparse-checkout: deploy/ci
          # O job não faz push nem fala com a API do GitHub: o token não
          # precisa ficar gravado no .git do runner.
          persist-credentials: false

      - name: backup pela VPS
        id: backup
        env:
          VPS_HOST: ${{ secrets.VPS_HOST }}
          VPS_SSH_KEY: ${{ secrets.VPS_SSH_KEY }}
          VPS_HOST_KEY: ${{ secrets.VPS_HOST_KEY }}
        # O vps.sh guarda a saída padrão da VPS no arquivo, mostra o log, e só
        # deixa o arquivo se ele começar com o cabeçalho do age: texto claro
        # nunca vira artifact. 0: conferido. 4: conferido, mas o disco da VPS
        # passou de 85%: o arquivo é bom, segue para o artifact, e o último
        # passo derruba o job. Outro código: não há backup que sirva, o
        # arquivo foi apagado e o job para aqui.
        run: |
          codigo=0
          sh deploy/ci/vps.sh backup "$RUNNER_TEMP/vigilume-$(date -u +%Y%m%d).dump.age" || codigo=$?
          echo "codigo=$codigo" >> "$GITHUB_OUTPUT"
          if [ "$codigo" -ne 0 ] && [ "$codigo" -ne 4 ]; then exit "$codigo"; fi

      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: vigilume-backup-${{ github.run_id }}
          path: ${{ runner.temp }}/vigilume-*.dump.age
          retention-days: 30
          if-no-files-found: error

      # Depois do artifact: o backup do dia fica guardado, e o job ainda fica
      # vermelho (o GitHub manda o e-mail).
      - name: disco acima de 85%
        if: steps.backup.outputs.codigo == '4'
        run: |
          echo "::error::Disco da VPS acima de 85%: libere espaço ou aumente o disco (runbook, \"Disco\"). O backup de hoje foi conferido e guardado."
          exit 1
````

Aplique em `ci.yml`, `dependabot.yml` e `compose_test.go`:

````diff
diff --git a/.github/dependabot.yml b/.github/dependabot.yml
index 2209bd7..f03ec92 100644
--- a/.github/dependabot.yml
+++ b/.github/dependabot.yml
@@ -6,7 +6,7 @@
 # backup.yml é o de produção) têm um Dockerfile de uma linha cada, lido pelo
 # workflow: o Dependabot não lê o `env` de um workflow. Ficam à mão:
 # - as versões das ferramentas Go no env do ci.yml (sqlc, staticcheck, gosec,
-#   govulncheck, gitleaks);
+#   govulncheck, gitleaks, actionlint) e o crane do deploy.yml;
 # - os digests do Postgres 17 e 18 na matriz do job test do ci.yml;
 # - o imagemPadrao de internal/storage/testdb (o mesmo digest do 18);
 # - a versão do flyctl (0.4.108) no deploy.yml e no backup.yml.
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index 88ce9a1..959f375 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -37,6 +37,7 @@ env:
   GOSEC_VERSION: v2.29.0
   GOVULNCHECK_VERSION: v1.8.0
   GITLEAKS_VERSION: v8.30.1
+  ACTIONLINT_VERSION: v1.7.12
   # O nome com que o job image carrega a imagem e o e2e a usa: o mesmo do
   # compose.yaml, com VIGILUME_TAG=ci.
   #
@@ -149,6 +150,37 @@ jobs:
           go install "github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}"
           gitleaks git . --config .gitleaks.toml --redact --no-banner --verbose
 
+  workflows:
+    # Os workflows não rodam fora do GitHub, e o deploy e o backup só rodam
+    # à mão ou de madrugada: um erro neles aparece no pior momento. Aqui: o
+    # actionlint (sintaxe, expressões e o shellcheck de cada `run:`), as
+    # checagens de texto que ele não faz (host key fixada, actions por SHA,
+    # permissões, gatilhos) e o vps.sh contra um ssh falso.
+    runs-on: ubuntu-latest
+    timeout-minutes: 10
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          persist-credentials: false
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+          cache: false
+      - name: actionlint
+        # O shellcheck vem na imagem do runner; sem ele, o actionlint pularia
+        # os `run:` em silêncio, e por isso o passo seguinte o chama direto.
+        run: |
+          go install "github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}"
+          actionlint
+      - name: shellcheck dos scripts de deploy/ci
+        run: |
+          shellcheck --version
+          shellcheck deploy/ci/*.sh
+      - name: checagens dos workflows
+        run: sh deploy/ci/checar-workflows.sh
+      - name: vps.sh contra um ssh falso
+        run: sh deploy/ci/testar-vps.sh
+
   web:
     runs-on: ubuntu-latest
     timeout-minutes: 15
@@ -332,7 +364,7 @@ jobs:
     # repositório (nunca em PR, nem num fork). O pacote do GHCR segue a
     # visibilidade do repositório: privado, por enquanto.
     if: github.event_name == 'push' && github.repository == 'BipoXDXD/vigilume'
-    needs: [lint, test, secrets, web, image, e2e]
+    needs: [lint, test, secrets, workflows, web, image, e2e]
     runs-on: ubuntu-latest
     timeout-minutes: 30
     permissions:
diff --git a/internal/config/compose_test.go b/internal/config/compose_test.go
index 2407152..c3a918f 100644
--- a/internal/config/compose_test.go
+++ b/internal/config/compose_test.go
@@ -69,28 +69,6 @@ func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 	}
 }
 
-// O deploy e o backup usam o mesmo flyctl: um comando que funciona num e
-// não no outro por diferença de versão é o tipo de coisa que só aparece em
-// produção.
-func TestFlyctlVersionIsTheSameInDeployAndBackup(t *testing.T) {
-	re := regexp.MustCompile(`(?ms)setup-flyctl@\S+.*?^\s+version: (\S+)$`)
-	var versoes []string
-	for _, arq := range []string{"../../.github/workflows/deploy.yml", "../../.github/workflows/backup.yml"} {
-		conteudo, err := os.ReadFile(arq)
-		if err != nil {
-			t.Fatal(err)
-		}
-		m := re.FindSubmatch(conteudo)
-		if m == nil {
-			t.Fatalf("%s: versão do setup-flyctl não encontrada", arq)
-		}
-		versoes = append(versoes, string(m[1]))
-	}
-	if versoes[0] != versoes[1] {
-		t.Errorf("flyctl %s no deploy.yml e %s no backup.yml", versoes[0], versoes[1])
-	}
-}
-
 // O compose tem de continuar subindo com as chaves de demo: o PUBLIC_BASE_URL
 // dele precisa ser http e de loopback, senão o boot as recusa (hostLocal).
 func TestComposePublicBaseURLIsLocal(t *testing.T) {
````

- [ ] **Step 4: Rode e veja passar**

Run:

```bash
actionlint            # com shellcheck no PATH
shellcheck deploy/ci/*.sh
sh deploy/ci/checar-workflows.sh
sh deploy/ci/testar-vps.sh
go test -count=1 ./internal/config/
```

Expected: actionlint e shellcheck sem saída; `workflows: tudo certo`; `vps.sh: 46 casos, tudo certo`; `ok`.

- [ ] **Step 5: Sabotagens (restaure de backup depois de cada uma)**

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| C1 | `deploy.yml`: passo com `ssh -o StrictHostKeyChecking=no deploy@x true` | `StrictHostKeyChecking desligado` e `ssh direto no workflow` |
| C2 | `ci.yml`: `-o "StrictHostKeyChecking accept-new"` num `run:` | `StrictHostKeyChecking desligado` (vale para todo workflow, e com espaço) |
| C3 | `backup.yml`: `ssh-keyscan x >> ~/.ssh/known_hosts` | `workflow com ssh-keyscan` |
| C4 | `deploy.yml`: `docker/login-action@v4` | `deploy.yml:<linha>: action sem SHA completo: docker/login-action@v4` |
| C5 | `ci.yml`: `actions/setup-node@v7` | `ci.yml:195` e `ci.yml:323: action sem SHA completo` |
| C6 | `deploy.yml`: `on: push:` a mais | `gatilhos "push workflow_dispatch ", queria só workflow_dispatch` |
| C7 | `backup.yml`: `cancel-in-progress: true` | `concurrency tem de ser cancel-in-progress: false` |
| C8 | `deploy.yml`: `packages: write` | `permissão de escrita` e `permissions "contents:read packages:write "` |
| C9 | `backup.yml`: `packages: read` a mais | `permissions "contents:read packages:read ", queria só contents: read` |
| C10 | `deploy.yml`: `ssh deploy@"$VPS_HOST" "deploy $SHA $IMAGE_ID"` no lugar do vps.sh | `ssh direto no workflow` e `a entrega não passa por deploy/ci/vps.sh deploy` |
| C11 | `backup.yml`: cron `'0 6 * * *'` | `sem o agendamento diário das 06:17 UTC` |
| C12 | `backup.yml`: tire o `if: vars.BACKUP_ENABLED == '1'` | `o job não está desligado até BACKUP_ENABLED=1` |
| C13 | `backup.yml`: `retention-days: 7` | `o artifact não fica 30 dias` |
| C14 | `backup.yml`: `AGE_RECIPIENT: ${{ secrets.AGE_RECIPIENT }}` | `secret fora da lista: secrets.AGE_RECIPIENT` |
| C15 | `backup.yml`: `secrets.GITHUB_TOKEN` | `backup.yml: secrets.GITHUB_TOKEN` |
| C16 | `backup.yml`: `if-no-files-found: warn` | `o upload sem arquivo tem de falhar` |
| C17 | `backup.yml`: um segundo `- cron:` | `2 agendamentos, queria só o das 06:17 UTC` |
| C18 | `deploy.yml`: `cancel-in-progress: ${{ … }}` | `concurrency tem de ser cancel-in-progress: false` |
| A1 | `backup.yml`: `$RUNNER_TEMP/…` sem aspas no `run:` | actionlint (shellcheck SC2086) |
| A2 | `backup.yml`: `steps.bakup.outputs.codigo` | actionlint (propriedade inexistente) |

A C17 ficou verde na primeira rodada (o checador só procurava a linha certa, e não a ausência de outra); a
perna de contagem entrou por causa dela.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/deploy.yml .github/workflows/backup.yml .github/workflows/ci.yml \
  .github/dependabot.yml deploy/ci/checar-workflows.sh internal/config/compose_test.go
git commit -m "feat(deploy): deploy.yml e backup.yml falam com a VPS pelo vps.sh; job workflows no CI com actionlint e checagens"
```

---

### Task W3: o Fly sai do repositório; `deploy/vps/db/Dockerfile`; perna "nada do Fly.io"

Spec §1 (critério 3) e §7. `docs/deploy/fly.md` é do grupo runbook (que o apaga e escreve
`docs/deploy/vps.md`): esta tarefa não o toca, e por isso a perna 6 só fica verde depois dele.

**Files:**
- Delete: `fly.toml`, `deploy/fly/db/fly.toml`, `deploy/fly/db/Dockerfile`
- Create: `deploy/vps/db/Dockerfile` (grupo vps cria o mesmo: conflito esperado)
- Modify: `deploy/ci/checar-workflows.sh` (perna 6)
- Modify: `.github/dependabot.yml`, `.github/workflows/ci.yml` (comentários), `README.md`, `Dockerfile`
  (comentários), `compose.yaml` (comentário), `deploy/compose/initdb.sql` (cabeçalho),
  `deploy/backup/backup.sh` (mensagem do código 4), `cmd/vigilume/user.go` (comentário), `.gitignore`
  (comentário), `internal/config/compose_test.go`, `internal/config/repositorio_test.go`,
  `internal/storage/testdb/testdb.go` (comentário)

**Interfaces:**
- Consumes: a remoção de `docs/deploy/fly.md` (runbook).
- Produces: `deploy/vps/db/Dockerfile`; Dependabot em `/deploy/vps/db`; a perna 6.

- [ ] **Step 1: Escreva o teste**

Aplique em `deploy/ci/checar-workflows.sh`:

````diff
diff --git a/deploy/ci/checar-workflows.sh b/deploy/ci/checar-workflows.sh
index 88ad0b1..9611ec7 100644
--- a/deploy/ci/checar-workflows.sh
+++ b/deploy/ci/checar-workflows.sh
@@ -105,6 +105,24 @@ for f in "$DEPLOY" "$BACKUP"; do
 done 2>&1 | tee "$marcas" >&2
 contar_marcas
 
+# 6. Nada do Fly.io sobra no repositório (Plano 11, spec §1, critério 3): nem
+#    arquivo, nem comando, nem secret, nem link para o runbook antigo. Fica de
+#    fora a história (docs/superpowers/: specs, planos e pendências de
+#    planos já feitos) e este script. O nome do cabeçalho Fly-Client-IP
+#    continua valendo: o CLIENT_IP_HEADER o aceita, e o Caddy o apaga da
+#    requisição (Obrigação 1 do Plano 11); por isso a busca é pelo que só
+#    existe na plataforma, e não pela palavra "Fly".
+FLY_ERE='flyctl|superfly|fly\.(io|dev|toml)|deploy/fly|registry\.fly|(^|[^a-z0-9_-])fly[[:space:]]+(deploy|ssh|volumes?|tokens?|proxy|launch|secrets?|machines?|apps|auth|scale|logs|pg|postgres|status|config|ips|certs|console|wireguard|regions)([^a-z0-9_-]|$)'
+for arq in fly.toml deploy/fly docs/deploy/fly.md; do
+    [ ! -e "$arq" ] || falhar "$arq ainda existe: o Fly saiu do repositório (o runbook é docs/deploy/vps.md)"
+done
+if git grep -nIiE "$FLY_ERE" -- . ':(exclude)docs/superpowers/**' ':(exclude)deploy/ci/checar-workflows.sh' >&2; then
+    falhar "referência ao Fly.io (acima): o deploy é na VPS (docs/deploy/vps.md)"
+fi
+if git grep -nIE 'FLY_[A-Z]' -- . ':(exclude)docs/superpowers/**' ':(exclude)deploy/ci/checar-workflows.sh' >&2; then
+    falhar "variável ou secret do Fly.io (acima)"
+fi
+
 if [ "$falhas" -gt 0 ]; then
     echo "$falhas falha(s) nos workflows" >&2
     exit 1
````

- [ ] **Step 2: Rode e veja falhar**

Run: `sh deploy/ci/checar-workflows.sh`

Expected: FAIL: `fly.toml ainda existe`, `deploy/fly ainda existe`, `docs/deploy/fly.md ainda existe`,
`referência ao Fly.io` (listando `dependabot.yml`, `ci.yml`, `.gitignore`, `README.md`, `user.go`,
`compose.yaml`, `backup.sh`, `initdb.sql`, `deploy/fly/**`, `docs/deploy/fly.md`, `fly.toml`,
`compose_test.go`, `repositorio_test.go`, `testdb.go`) e `variável ou secret do Fly.io` (só
`docs/deploy/fly.md`).

- [ ] **Step 3: Implemente**

```bash
git rm -r fly.toml deploy/fly
mkdir -p deploy/vps/db
```

Crie `deploy/vps/db/Dockerfile`:

````dockerfile
# Postgres de produção do Vigilume: a imagem OFICIAL, a mesma (tag e digest)
# do compose e do compose de produção (D28, D29 do Plano 7; Plano 11). Este
# arquivo não se constrói: existe para o Dependabot enxergar o digest.
# O MESMO digest está fixado em quatro lugares: este Dockerfile, o
# compose.yaml, o .github/workflows/ci.yml e o internal/storage/testdb.
# Atualize os quatro juntos.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
````

Aplique:

````diff
diff --git a/.github/dependabot.yml b/.github/dependabot.yml
index f03ec92..29e6ac0 100644
--- a/.github/dependabot.yml
+++ b/.github/dependabot.yml
@@ -8,8 +8,7 @@
 # - as versões das ferramentas Go no env do ci.yml (sqlc, staticcheck, gosec,
 #   govulncheck, gitleaks, actionlint) e o crane do deploy.yml;
 # - os digests do Postgres 17 e 18 na matriz do job test do ci.yml;
-# - o imagemPadrao de internal/storage/testdb (o mesmo digest do 18);
-# - a versão do flyctl (0.4.108) no deploy.yml e no backup.yml.
+# - o imagemPadrao de internal/storage/testdb (o mesmo digest do 18).
 version: 2
 updates:
   - package-ecosystem: gomod
@@ -46,12 +45,12 @@ updates:
       interval: weekly
 
   # O Dockerfile do app (node, golang, distroless); o do Postgres de
-  # produção (que o fly.toml e o backup.yml usam: o Dependabot não lê
-  # nenhum dos dois) e o do Trivy do CI.
+  # produção (deploy/vps/db, só a linha FROM, com o digest que o compose de
+  # produção repete) e o do Trivy do CI.
   - package-ecosystem: docker
     directories:
       - /
-      - /deploy/fly/db
+      - /deploy/vps/db
       - /deploy/ci/trivy
     schedule:
       interval: weekly
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index 959f375..e25d479 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -41,12 +41,8 @@ env:
   # O nome com que o job image carrega a imagem e o e2e a usa: o mesmo do
   # compose.yaml, com VIGILUME_TAG=ci.
   #
-  # Nomes repetidos à mão: ghcr.io/bipoxdxd/vigilume aqui, no ORIGEM do
-  # deploy.yml e no image do compose.yaml. Os apps do Fly (vigilume e
-  # vigilume-db) estão no deploy.yml, no backup.yml, nos dois fly.toml
-  # (inclusive PUBLIC_BASE_URL e CORS_ORIGIN) e no secret DATABASE_URL
-  # (vigilume-db.internal). Renomear é mexer em todos juntos: a lista
-  # completa está no runbook, "Se o nome do app estiver tomado".
+  # Nome repetido à mão: ghcr.io/bipoxdxd/vigilume aqui, no ORIGEM do
+  # deploy.yml e no image do compose.yaml. Renomear é mexer nos três juntos.
   IMAGEM: ghcr.io/bipoxdxd/vigilume
 
 jobs:
@@ -100,7 +96,7 @@ jobs:
       fail-fast: false
       matrix:
         # Por digest, com a tag na string. O Dependabot não atualiza estas
-        # strings: a mão, junto com o digest de deploy/fly/db/Dockerfile (o
+        # strings: a mão, junto com o digest de deploy/vps/db/Dockerfile (o
         # 18 é o de produção, igual ao do Dockerfile, do compose.yaml e do
         # testdb) e com o imagemPadrao de internal/storage/testdb.
         include:
@@ -424,10 +420,10 @@ jobs:
           build-args: VERSION=${{ env.VERSION }}
           cache-from: type=gha,scope=imagem
           # Sem atestado de proveniência: ele vira uma entrada "unknown/unknown"
-          # no índice, que o Fly e o Trivy teriam de pular.
+          # no índice, que o Trivy e o deploy teriam de pular.
           provenance: false
 
-      # O tools (só o seed do compose; nunca vai para o Fly) não passa pelo
+      # O tools (só o seed do compose; nunca vai para produção) não passa pelo
       # reescaneamento daqui: o Trivy do job image já o varreu na amd64 (mesmo
       # código e bases); a arm64 dele não passa por Trivy.
       - id: tools
diff --git a/.gitignore b/.gitignore
index 00a5595..aa33899 100644
--- a/.gitignore
+++ b/.gitignore
@@ -16,7 +16,7 @@
 .env.*
 !.env.example
 !/web/.env.development
-# O backup (docs/deploy/fly.md, D3 do Plano 10): a chave privada do age
+# O backup (docs/deploy/vps.md, D3 do Plano 10): a chave privada do age
 # (vigilume-backup.key), o dump decifrado na restauração (vigilume.dump) e o
 # artifact baixado (vigilume-AAAAMMDD.dump.age). Nenhum deles vai para o git.
 *.key
diff --git a/Dockerfile b/Dockerfile
index 00fdd4d..6a39184 100644
--- a/Dockerfile
+++ b/Dockerfile
@@ -42,14 +42,14 @@ RUN --mount=type=cache,target=/go/pkg/mod \
 
 # tools: o mesmo binário sobre a variante debug, que tem o busybox. Só o seed
 # do compose a usa, para passar a senha de demo pelo stdin (D6). NUNCA vai
-# para o Fly.
+# para produção.
 FROM gcr.io/distroless/static-debian13:debug-nonroot@sha256:2a581fcbda6320d4d17fd6ff4774bb96e4825d2be9c2bca59f0777b429997f51 AS tools
 COPY --from=build /out/vigilume /vigilume
 USER nonroot:nonroot
 ENTRYPOINT ["/vigilume"]
 
-# runtime: o último estágio, o padrão do `docker build`. Sem HEALTHCHECK: o
-# Fly faz as próprias checagens, e o compose usa `vigilume healthcheck` (D5).
+# runtime: o último estágio, o padrão do `docker build`. Sem HEALTHCHECK: os
+# compose (o local e o de produção) usam `vigilume healthcheck` (D5).
 FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS runtime
 COPY --from=build /out/vigilume /vigilume
 USER nonroot:nonroot
diff --git a/README.md b/README.md
index 8f94ba0..4c65bc4 100644
--- a/README.md
+++ b/README.md
@@ -148,7 +148,7 @@ make e2e           # APAGA o banco do compose, sobe com --build, confere e roda
 | Banco | PostgreSQL 18 (compatível com o 17) |
 | Painel | React 19, TypeScript, Vite, TanStack Query, React Router, React Hook Form + Zod, Radix, Tailwind, Recharts |
 | Testes | `testing` + testcontainers, Vitest + Testing Library + MSW, Playwright + axe |
-| Entrega | Docker (distroless), GitHub Actions, GHCR, Fly.io |
+| Entrega | Docker (distroless), GitHub Actions, GHCR, VPS com Docker Compose e Caddy |
 
 ## Desenvolvimento
 
@@ -204,27 +204,29 @@ O que mudou no Plano 10 (spec do Plano 10, §4):
 
 ## Deploy e operação
 
-A hospedagem escolhida é uma VPS na Hostinger, com Caddy na frente: é o Plano 11, que traz o
-compose de produção e o runbook próprio. Até lá, o que existe é o alvo do Fly.io, descrito
-abaixo. Dois apps no Fly.io: o binário (`--role=all`) e um Postgres 18 com a imagem oficial, sem gerência
-do Fly, só na rede privada. **Nada disso foi criado ainda**: os artefatos e o runbook existem, e o
-primeiro deploy é decisão do dono do projeto.
+Produção é uma VPS (Hostinger, Ubuntu 24.04) com Docker Compose: Caddy na frente (só as portas 80
+e 443, HTTPS automático), o binário (`--role=all`) e um Postgres 18 com a imagem oficial, os dois
+só na rede interna do compose (`deploy/vps/`). **O primeiro deploy é feito com o dono, guiado
+pelo runbook.**
 
 - **Imagem:** ~7,6 MB comprimida. A de produção sai do mesmo código e das mesmas bases fixadas
   por digest que o E2E testou, mas é construída de novo no `publish` (amd64 e arm64) e
   **reescaneada** pelo Trivy antes de ganhar as tags `:<sha do commit>` e `:latest`.
 - **Deploy:** sempre manual (Actions → deploy → Run workflow), só da `main` e só de um commit
   que está nela, com o **SHA do commit** como entrada. O job resolve a tag `:<sha>`, que só
-  existe depois do reescaneamento, no digest dela, copia a imagem por esse digest para o
-  registry do Fly e faz o deploy. No GitHub Free com repositório privado não há revisor
-  obrigatório: quem pode disparar é quem tem acesso de escrita ao repositório (hoje, só o dono),
-  e os tokens do Fly são secrets de repositório.
-- **Backup:** dump lógico diário, cifrado com `age`, restaurado num banco descartável e
-  conferido (dono das tabelas, RLS com `FORCE`, contagens, partições). Uma checagem que falha
-  sai com código 3. Disco acima de 85% sai com código 4: o dump já conferido é cifrado e guardado
+  existe depois do reescaneamento, no digest dela, baixa a imagem linux/amd64 por esse digest e
+  a entrega à VPS por SSH, com a host key fixada num secret e uma chave que só alcança o script
+  de entrada (`deploy/vps/vigilume-entrada`). A VPS confere a imagem, troca o app e volta
+  sozinha para a versão anterior se a nova não ficar saudável. No GitHub Free com repositório
+  privado não há revisor obrigatório: quem pode disparar é quem tem acesso de escrita ao
+  repositório (hoje, só o dono).
+- **Backup:** dump lógico diário, feito na VPS, restaurado num banco descartável e conferido
+  (dono das tabelas, RLS com `FORCE`, contagens, partições), cifrado com a chave **pública** do
+  `age` (a privada fica só com o dono) e guardado como artifact por 30 dias. Uma checagem que
+  falha sai com código 3. Disco acima de 85% sai com código 4: o dump já conferido é guardado
   mesmo assim, e o job fica vermelho no fim.
 
-Runbook: [`docs/deploy/fly.md`](docs/deploy/fly.md).
+Runbook: [`docs/deploy/vps.md`](docs/deploy/vps.md).
 
 ## O que ficou de fora, e por quê
 
@@ -237,7 +239,7 @@ Runbook: [`docs/deploy/fly.md`](docs/deploy/fly.md).
 - **Domínio próprio na status page**: verificação de DNS é escopo próprio.
 - **Réplica, failover e PITR do Postgres**: um nó com snapshot diário e dump diário basta para
   este volume; no pior caso, perde-se um dia de checks.
-- **Métricas raspadas em produção**: exigiriam abrir o endpoint na rede privada do Fly.
+- **Métricas raspadas em produção**: exigiriam um coletor e abrir o endpoint na rede do compose.
 - **Cotas por usuário no sondador e no despacho**: só o operador cria contas (pelo CLI), então
   quem as esgotaria é uma conta que o próprio dono criou. Volta à mesa se o cadastro abrir.
 
@@ -247,4 +249,4 @@ e nos [planos](docs/superpowers/plans/).
 ## Licença
 
 Ainda não definida (repositório privado). A escolha é do dono do projeto e vem junto com a
-publicação ([runbook](docs/deploy/fly.md), "Tornar o repositório público").
+publicação ([runbook](docs/deploy/vps.md), "Tornar o repositório público").
diff --git a/cmd/vigilume/user.go b/cmd/vigilume/user.go
index 14f4d4f..c65ba37 100644
--- a/cmd/vigilume/user.go
+++ b/cmd/vigilume/user.go
@@ -53,7 +53,7 @@ type entradaDaSenha struct {
 	// pipe: uma linha só, lida daqui.
 	pipe io.Reader
 	// avisarPipe: o stdin não é terminal, mas o stderr é — uma pessoa digitando
-	// num `docker exec -i` sem -t, ou num `fly ssh console -C`. Sem o aviso, o
+	// num `docker exec -i` sem -t, ou num `docker compose exec -T`. Sem o aviso, o
 	// comando parece travado: espera a linha em silêncio, e com eco.
 	avisarPipe bool
 }
diff --git a/compose.yaml b/compose.yaml
index cc1a0f2..f2bd33f 100644
--- a/compose.yaml
+++ b/compose.yaml
@@ -33,7 +33,7 @@ name: vigilume
 # Com elas, qualquer um forja um JWT e decifra os segredos gravados. O boot
 # só as aceita com PUBLIC_BASE_URL http em localhost ou num IP de loopback
 # (D14); https, ou outro host, é recusado. Produção: o compose de produção do
-# Plano 11 (e, enquanto existir, docs/deploy/fly.md).
+# Plano 11 (deploy/vps/compose.prod.yaml; runbook docs/deploy/vps.md).
 x-ambiente: &ambiente
   DATABASE_URL: postgres://vigilume_app:vigilume_app@db:5432/vigilume?sslmode=disable
   # A mesma porta publicada abaixo: os links dos e-mails e da página de
diff --git a/deploy/backup/backup.sh b/deploy/backup/backup.sh
index c8244c0..27498ed 100644
--- a/deploy/backup/backup.sh
+++ b/deploy/backup/backup.sh
@@ -63,7 +63,7 @@ if [ -n "${VOLUME_BYTES:-}" ]; then
     if [ "$pct" -ge 85 ]; then
         # O backup acima já está conferido; 4 (e não 3) diz a quem chama que
         # ele deve ser cifrado e guardado mesmo assim.
-        echo "ERRO: acima de 85%: aumente o volume (fly volumes extend)" >&2
+        echo "ERRO: acima de 85%: libere espaço ou aumente o disco" >&2
         exit 4
     elif [ "$pct" -ge 70 ]; then
         echo "AVISO: acima de 70% do volume"
diff --git a/deploy/compose/initdb.sql b/deploy/compose/initdb.sql
index aab6891..4e60b1c 100644
--- a/deploy/compose/initdb.sql
+++ b/deploy/compose/initdb.sql
@@ -1,7 +1,7 @@
--- Papel e banco da aplicação. É o MESMO SQL em três lugares: o init do
--- Postgres do compose, o banco descartável em que o backup é restaurado todo
--- dia, e o console do Postgres de produção (docs/deploy/fly.md, passo 4, com
--- senha nova).
+-- Papel e banco da aplicação. É o MESMO SQL em dois lugares: o init do
+-- Postgres do compose e o banco descartável em que o backup é restaurado todo
+-- dia. O Postgres de produção cria os papéis com deploy/vps/initdb.sh, com as
+-- senhas geradas na VPS.
 --
 -- O papel é comum de propósito: sem SUPERUSER e sem BYPASSRLS, que
 -- atravessariam a row-level security em silêncio. O boot recusa um papel
diff --git a/internal/config/compose_test.go b/internal/config/compose_test.go
index c3a918f..a21bd35 100644
--- a/internal/config/compose_test.go
+++ b/internal/config/compose_test.go
@@ -31,7 +31,7 @@ func TestComposeUsesTheDemoKeys(t *testing.T) {
 
 // O Postgres de produção, do compose, da perna 18 do CI e dos testes tem de
 // ser o MESMO digest (D29 do Plano 7). O Dependabot só atualiza o
-// deploy/fly/db/Dockerfile e o compose.yaml; as outras duas strings são a mão,
+// deploy/vps/db/Dockerfile e o compose.yaml; as outras duas strings são a mão,
 // e este teste pega a que ficou para trás.
 func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 	digest18 := regexp.MustCompile(`postgres:18-alpine@sha256:[0-9a-f]{64}`)
@@ -40,7 +40,7 @@ func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
 		// regex com um grupo que isola a linha onde o digest 18 deve estar.
 		linha *regexp.Regexp
 	}{
-		{"../../deploy/fly/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
+		{"../../deploy/vps/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
 		{"../../compose.yaml", regexp.MustCompile(`(?m)^\s+image: (postgres:\S+)$`)},
 		{"../../.github/workflows/ci.yml", regexp.MustCompile(`(?m)^\s+image: (postgres:18\S*)$`)},
 		{"../storage/testdb/testdb.go", regexp.MustCompile(`(?m)^const imagemPadrao = "([^"]+)"$`)},
diff --git a/internal/config/repositorio_test.go b/internal/config/repositorio_test.go
index 40be18b..fc3e130 100644
--- a/internal/config/repositorio_test.go
+++ b/internal/config/repositorio_test.go
@@ -42,7 +42,7 @@ func TestDependabotWaitsSevenDaysForGoAndNpm(t *testing.T) {
 	}
 }
 
-// D3 (Plano 10, §3.4; banco S3 da revisão): o runbook (docs/deploy/fly.md)
+// D3 (Plano 10, §3.4; banco S3 da revisão): o runbook (docs/deploy/vps.md)
 // gera a chave privada do age e decifra o backup no diretório de trabalho.
 // Um `git add -A` na raiz a levaria para o histórico, e o gitleaks do CI só a
 // acha depois do push. Os nomes são os do runbook e do backup.yml; os
@@ -65,7 +65,7 @@ func TestGitignoreCoversTheBackupKeyAndDumps(t *testing.T) {
 	// exemplo de ambiente, o .env de desenvolvimento do Vite (sem segredo, por
 	// convenção dele) e chaves de fixture TLS em testdata/.
 	versionados := []string{
-		"compose.yaml", "deploy/compose/seed.sh", "docs/deploy/fly.md", "go.mod",
+		"compose.yaml", "deploy/compose/seed.sh", "docs/deploy/vps.md", "go.mod",
 		".env.example", "web/.env.example", "web/.env.development",
 		"internal/prober/safedial/testdata/server.key", "testdata/tls/ca.key",
 	}
diff --git a/internal/storage/testdb/testdb.go b/internal/storage/testdb/testdb.go
index fc1d42a..cef8773 100644
--- a/internal/storage/testdb/testdb.go
+++ b/internal/storage/testdb/testdb.go
@@ -151,7 +151,7 @@ func iniciar(t *testing.T) {
 // imagemPadrao é a imagem de desenvolvimento e testes: a mesma de produção,
 // por digest (D29 do Plano 7); a CI escolhe a versão por TEST_POSTGRES_IMAGE.
 // O Dependabot não atualiza esta string: mantenha-a igual ao digest de
-// deploy/fly/db/Dockerfile e atualize as duas juntas (e a matriz do ci.yml).
+// deploy/vps/db/Dockerfile e atualize as duas juntas (e a matriz do ci.yml).
 const imagemPadrao = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"
 
 func imagem() string {
````

- [ ] **Step 4: Rode e veja passar**

Run:

```bash
gofmt -l cmd internal && go vet ./... && go build ./...
go test -count=1 ./internal/config/ ./cmd/...
actionlint && shellcheck deploy/ci/*.sh deploy/backup/backup.sh
sh deploy/ci/testar-vps.sh
sh deploy/ci/checar-workflows.sh
```

Expected: tudo verde, e o checador com uma única falha enquanto `docs/deploy/fly.md` existir
(`docs/deploy/fly.md ainda existe` e as linhas dele). Com o arquivo removido (depois do runbook, ou
simulado com `git rm docs/deploy/fly.md` e restaurado com `git reset -q HEAD docs/deploy/fly.md && git
checkout -- docs/deploy/fly.md`): `workflows: tudo certo`.

- [ ] **Step 5: Sabotagens (com `docs/deploy/fly.md` fora, como acima; restaure de backup depois de cada uma)**

| # | Quebre | Tem que ficar vermelho |
|---|---|---|
| F1 | `README.md`: link do runbook de volta para `docs/deploy/fly.md` | `referência ao Fly.io` |
| F2 | `user.go`: `` `fly ssh console -C` `` no comentário | `referência ao Fly.io` |
| F3 | `backup.sh`: `(fly volumes extend)` de volta | `referência ao Fly.io` |
| F4 | `backup.yml`: `FLY_API_TOKEN: x` | `backup.yml:53: FLY_API_TOKEN` e `variável ou secret do Fly.io` |
| F5 | `compose.yaml`: `https://vigilume.fly.dev` num comentário | `referência ao Fly.io` |
| F6 | `dependabot.yml`: `/deploy/fly/db` | `referência ao Fly.io` |
| F7 | `Dockerfile`: `registry.fly.io` num comentário | `referência ao Fly.io` |
| F8 | `config.go`: comentário `uses: superfly/flyctl-actions` (fora de workflow e de docs) | `referência ao Fly.io` |
| F9 | `fly.toml` de volta (não versionado) | `fly.toml ainda existe` |
| F10 | `deploy/fly/db/` de volta (vazio) | `deploy/fly ainda existe` |
| G1 | `deploy/vps/db/Dockerfile`: um hex do digest trocado | `TestPostgres18DigestIsTheSameEverywhere` |
| V1 | (verde declarada) o `Fly-Client-IP` do README, do `config.go` e dos testes de `clientip`, os nomes `…BehindFly…` e `docs/superpowers/**` | continuam verdes: são o cabeçalho que o `CLIENT_IP_HEADER` aceita e o Caddy apaga, e a história |

- [ ] **Step 6: Commit**

```bash
git add -A fly.toml deploy/fly deploy/vps/db .github README.md Dockerfile compose.yaml deploy cmd .gitignore internal
git commit -m "chore(deploy): Fly.io sai do repositório; digest do Postgres em deploy/vps/db e checagem de referência ao Fly no CI"
```


---

# Parte: runbook

# Plano 11 — parte do grupo **runbook** (spec §7, o runbook para iniciante; README, pendências e roadmap)

Redigida e validada numa cópia destacada a partir de `aebc4d0` (o `main` em `a61fc0f` mais o commit
da spec do Plano 11): `.superpowers/plano-11-spike/runbook`. Cada tarefa virou um commit ali; os
diffs e os arquivos novos abaixo são exatamente os desses commits.

| Tarefa | Commit na cópia | Diff contra |
|---|---|---|
| R-1: runbook `docs/deploy/vps.md`, `fly.md` apagado, README sem o Fly, `PENDENCIAS-PARA-O-PLANO-12.md`, roadmap | `3138e89` | `aebc4d0` |
| R-2: comentários e o teste do `.gitignore` apontam para `docs/deploy/vps.md` | `45cd5cb` | `3138e89` |

Os nomes "R-1" e "R-2" são provisórios: o controlador os renumera na montagem. **A R-1 é
documentação que descreve o que os grupos vps e workflows fazem**: ela entra depois deles, com os
marcadores `⟦…⟧` resolvidos contra os nomes finais (lista em "Interfaces"). A R-2 não depende de
ninguém e pode entrar a qualquer momento depois da R-1 (antes dela, o teste do `.gitignore` citaria
um arquivo que ainda não existe, o que o `git check-ignore --no-index` aceita, mas o comentário
mentiria).

## (a) Tarefas

1. **Task R-1: o runbook da VPS e os documentos vivos sem o Fly.** Cria `docs/deploy/vps.md` (o
   runbook da spec §7, blocos 0 a 10, para quem nunca usou VPS nem SSH: cada passo com "Faça",
   "Por quê", "Deve aparecer" e "Confira"), apaga `docs/deploy/fly.md` (o genérico migrou:
   reset-2fa, operação do Postgres, restauração, upgrade maior, "tornar o repositório público",
   minutos de Actions, depuração), reescreve a seção "Deploy e operação" do README e tira dele
   todo o Fly, cria `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md` e marca a linha 11 do roadmap
   como concluída. Commit `3138e89`.
2. **Task R-2: as referências ao runbook do Fly fora dos documentos** (`.gitignore`,
   `compose.yaml`, `deploy/compose/initdb.sql`, `internal/config/repositorio_test.go`). Commit
   `45cd5cb`.

## (b) Interfaces entre grupos

**Produz:** `docs/deploy/vps.md` (nome fixo das instruções comuns). Nenhum código. Os passos que
os outros grupos podem citar pelo número: 2.7 (SSH endurecido), 3.2 (`daemon.json` com o store do
containerd), 3.3 (arquivos em `/opt/vigilume`), 3.4 (segredos), 3.5 (`age`), 4.1–4.6 (usuário
`deploy`, `authorized_keys`, secrets), 4.8 (primeiro deploy), 7.1–7.2 (backup e ensaio), 8
(vigia), 9 (verificações finais), bloco 10 "Depuração do deploy" (tabela de códigos).

**Consome** (nomes das instruções comuns usados sem marcador): `deploy/vps/{compose.prod.yaml,
Caddyfile, initdb.sh, env.exemplo, vigilume-entrada, sudoers-vigilume-entrada, vigia.sh,
vigilume-vigia.service, vigilume-vigia.timer, db/Dockerfile}`; `/opt/vigilume/{.env,
age-recipient, versao, versao-anterior}`; `/usr/local/sbin/vigilume-entrada`; o protocolo
`ssh deploy@VPS "deploy <sha40> <image-id>"` com códigos 0/2/3/4/1 e `ssh deploy@VPS backup` com
0/3/4/1; a rede 172.30.0.0/24 (caddy .2, app .3, db .4, sem IPv6); `VIGILUME_APP_PASSWORD`,
`VIGILUME_BACKUP_PASSWORD`, `HC_URL`. Do grupo **workflows** (mensagem do controlador, já
aplicada): `VPS_HOST_KEY` = a linha inteira de `/etc/ssh/ssh_host_ed25519_key.pub`; usuário SSH
`deploy`; artifact `vigilume-AAAAMMDD.dump.age` (custom, cifrado); só a variável
`BACKUP_ENABLED`; `deploy/ci/vps.sh` (códigos 0/2/3/4/1/255 com mensagem), job `workflows` e
`deploy/ci/checar-workflows.sh` (nada do Fly fora de `docs/superpowers/**`); Dependabot em
`deploy/vps/db/Dockerfile`. Do **controlador**: o *image store* do containerd (verificação e
`features.containerd-snapshotter` no passo 3.2; recusa com código 1 citada na depuração).

**Marcadores `⟦…⟧` por resolver na montagem** (30 linhas; o Step 3 da R-1 falha enquanto sobrar
um). Todos numa linha só, para o `grep` achar:

| Onde | Marcador | Quem resolve |
|---|---|---|
| `vps.md` 2.9 | `443/udp` publicado (HTTP/3)? Se sim, a regra no `ufw` e no painel; se não, apagar a frase | vps |
| `vps.md` 3.3 | arquivos ou pacotes do host que o `vigilume-entrada` exige (ex.: `age`, `backup.sh`, `verificar.sql`, `initdb.sql`), com o destino; ou apagar | vps |
| `vps.md` 3.4 (2×) | `⟦VPS-SENHA-DO-SUPERUSUARIO⟧`: o nome, no `env.exemplo`, da senha do superusuário | vps |
| `vps.md` 3.4 | o nome da variável do domínio no `.env` | vps |
| `vps.md` 3.4 | o `env.exemplo` tem exatamente as variáveis do passo; o compose deriva `PUBLIC_BASE_URL`, `CORS_ORIGIN`, `DATABASE_URL` e `TRUSTED_PROXIES`? | vps |
| `vps.md` 4.4 | a mensagem de uso do `vigilume-entrada` sem verbo | vps |
| `vps.md` 4.7 | subir `db` e `caddy` antes do primeiro deploy, ou "o verbo sobe tudo" (apagar) | vps |
| `vps.md` 4.8, bloco 10 (3×) | nome do workflow de deploy e do input do SHA (`⟦WORKFLOWS-INPUT-DO-SHA⟧` está dentro de um comando) | workflows |
| `vps.md` 8 (2×) | `⟦VPS-DESTINO-DO-VIGIA⟧`: o caminho do `ExecStart` do `vigilume-vigia.service` | vps |
| `vps.md` 9.3 | o que o Caddy responde a `Host` desconhecido nas três formas | vps |
| `vps.md` bloco 10 | a mensagem do `vigilume-entrada` para store que não é o do containerd | vps |
| `vps.md` bloco 10 | o digest do Postgres também no `compose.prod.yaml`? | vps |
| `vps.md` bloco 10 | o nome do volume do Postgres e como o `compose.prod.yaml` o declara | vps |
| `README.md` (2×) | o teste do compose de produção e os testes do `vigilume-entrada`, com link | vps |
| `PENDENCIAS-12` (7×) | a prova no repositório de cada obrigação 1–7 | vps |
| `PENDENCIAS-12` (2×) | o que o grupo vps removeu (`fly.toml`, `deploy/fly/`); os limites registrados pelos grupos vps e workflows | controlador |
| roadmap | o nome do arquivo do Plano 11 | controlador |

**Conflitos de merge prováveis:** `README.md` (se outro grupo mexer na tabela de segurança ou no
Stack), `compose.yaml` e `.gitignore` (R-2: uma linha de comentário cada), e
`internal/config/repositorio_test.go` (R-2: o comentário e um caminho na perna oposta; o grupo vps
pode acrescentar `deploy/vps/env.exemplo` à mesma lista: manter os dois).

**Pergunta para o grupo vps (sem marcador, mas precisa de resposta):** o `authorized_keys` do
runbook é `restrict,command="sudo /usr/local/sbin/vigilume-entrada"`. O `sudo` zera o ambiente, e
com ele o `SSH_ORIGINAL_COMMAND`: o `sudoers-vigilume-entrada` precisa de
`Defaults!/usr/local/sbin/vigilume-entrada env_keep += "SSH_ORIGINAL_COMMAND"` (ou equivalente),
senão o script nunca vê o verbo. O teste 4.4 do runbook (`ls /` → código 2, e o deploy do 4.8)
pega isso na VPS; o CI do grupo vps deveria pegar antes.

## (c) Desvios

| Spec / pedido | Aqui | Motivo |
|---|---|---|
| Pedido: "como obter a host key com `ssh-keyscan` e conferir a impressão digital pelo terminal web" | O `VPS_HOST_KEY` vem de `ssh vigilume cat /etc/ssh/ssh_host_ed25519_key.pub` (a conexão já conferida no 2.1), e o `ssh-keyscan` fica como comparação por fora; a impressão digital é conferida contra o terminal do painel | O grupo workflows fixou o formato: a linha inteira do `.pub`, que o `ssh-keyscan` não devolve igual (ele troca o comentário pelo host). Pegar pela conexão já conferida e comparar com o keyscan dá as duas provas |
| Pedido: segredos só no `.env` e no gerenciador | Os três secrets do GitHub vão por `gh secret set … < arquivo`, e a `MASTER_KEY` do ensaio de restauração por `read -rs` | O GitHub e o ensaio precisam dos valores; nenhum dos dois é "colar": o `gh` lê de arquivo e cifra, o `read -rs` não ecoa nem grava histórico. A chave privada do deploy é apagada depois (4.6) |
| Spec §7, bloco 1: "DNS do domínio (registro A)" | Registro A em `@` e **apagar** `A`/`AAAA`/`CNAME` de `@` e `www`; sem `www` | A documentação da Hostinger manda apagar os registros existentes; o Caddy atende um nome só (um `www` apontado teria erro de certificado) |
| Spec §5: "firewall do painel com as mesmas regras" | 22, 80, 443 TCP; a regra UDP 443 só se o compose publicar HTTP/3 (marcador) | O compose é do grupo vps |
| Spec §4: "segunda camada: backup semanal da Hostinger" | O runbook manda conferir que ele está ligado (7.3) e o usa como primeiro recurso em "a VPS morreu" | A Hostinger faz o semanal por padrão (fonte citada); não há o que configurar |
| Pedido: "`docker compose exec app /vigilume user create`" | Igual, pelo atalho `vc` (`alias vc='sudo docker compose -f /opt/vigilume/compose.prod.yaml'`, criado no 4.8) | O atalho evita repetir o `-f` em ~40 comandos; o comando completo aparece uma vez no 4.8 |
| O `fly.md` tinha "Se o nome do app estiver tomado" e o `TRUSTED_PROXIES` medido | Não migraram | São do Fly: na VPS o `TRUSTED_PROXIES` é o IP fixo do Caddy (sem medição) e não há nome global de app |
| Spec §7: "Upgrade do Postgres" | Upgrade maior com dump para `/root` durante a janela (em claro, só root) e volume novo; o menor por Dependabot + recopiar o compose | O dump temporário no mesmo disco do banco não aumenta a exposição; cifrá-lo com a pública obrigaria a decifrar no computador do dono e mandar de volta em claro |

## (d) Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | **Usuário administrador pessoal com `sudo` por senha, e `docker` só com `sudo`** | Entrar como root; pôr o admin no grupo `docker` | O grupo `docker` é root sem senha; com `sudo`, roubar só a chave SSH não dá root. Custo: digitar a senha do `sudo` |
| D2 | **Arquivo `~/.vigilume-vps` com `VPS`, `DOMINIO` e `EU`** no computador do dono, e `Host vigilume` no `~/.ssh/config` | Placeholders `<IP>` nos comandos | Placeholders com `<>` quebram o shell se colados sem trocar; o arquivo é reaproveitado em todo terminal. Custo: lembrar do `. ~/.vigilume-vps` (o runbook repete) |
| D3 | **`sshd_config.d/00-vigilume.conf`** (o `00` vence um `50-cloud-init.conf`), conferido com `sshd -T` e com três tentativas de fora | Editar o `sshd_config` | O sshd usa o primeiro valor lido; um arquivo com número maior perderia para o da imagem da Hostinger. `sshd -T` mostra o valor efetivo |
| D4 | **`fail2ban` com `backend = systemd`** e `python3-systemd` | O padrão (`auth.log`) | O diário sempre existe; o `auth.log` depende do rsyslog da imagem |
| D5 | **Reinício às 04:30 com `WithUsers "true"`** e o fuso `America/Sao_Paulo` na VPS | Fuso UTC e 07:30 UTC | Com UTC, os logs e o reinício ficariam em outra hora que a do dono; uma sessão SSH esquecida não segura o reinício |
| D6 | **`features.containerd-snapshotter: true` no `daemon.json` desde o início**, e não só se a verificação falhar | Só conferir | Explícito sobrevive a uma instalação que veio de versão antiga; numa VPS nova não custa nada |
| D7 | **O `authorized_keys` do `deploy` e o `~/.ssh` dele são do root** (`0755`/`0644`) | Do próprio `deploy` (`0700`/`0600`) | O `StrictModes` do sshd aceita dono root; o `deploy` não pode trocar a própria chave |
| D8 | **A chave privada do deploy é apagada do computador** depois do `gh secret set` | Guardar no gerenciador | Ela só serve ao GitHub; se precisar, gera-se outra (bloco 10, "Trocar um segredo") |
| D9 | **Incidente de teste por `expected_status` 500 em `https://example.com/`** | Derrubar um serviço próprio | Determinístico, sem depender de nada do dono; testa sondador, detector, fila e e-mail juntos |
| D10 | **Ensaio de restauração no computador do dono, com `--role=api`** (como no `fly.md`) | Restaurar na VPS | A chave privada do `age` não vai à VPS; `--role=all` numa cópia mandaria alertas reais |
| D11 | **A R-2 muda o teste `TestGitignoreCoversTheBackupKeyAndDumps`** (a perna oposta passa a citar `docs/deploy/vps.md`) | Deixar `docs/deploy/fly.md` na lista | O `check-ignore --no-index` aceita caminho inexistente, então o teste ficaria verde com um arquivo apagado; a perna oposta passa a proteger um arquivo real |
| D12 | **Sem `www`, sem `AAAA`** | Os dois | O Caddy tem um bloco de site só; `AAAA` exigiria IPv6 conferido de ponta a ponta |

## (e) Review Focus

1. **O terminal do painel (Web console) depois do SSH endurecido.** A documentação da Hostinger
   não diz se ele passa pelo `sshd` nem com que usuário entra quando o root não pode. O runbook o
   testa no 2.7, logo depois de endurecer, e cai para o **Emergency mode** se ele não abrir. É a
   primeira coisa a confirmar na execução guiada.
2. **`SSH_ORIGINAL_COMMAND` atravessando o `sudo`** (pergunta ao grupo vps, acima).
3. **O firewall do painel da Hostinger é *stateful*?** Se não for, as respostas das conexões de
   saída (sondas, `apt`, GHCR) morrem. O 2.10 confere com `curl` e `apt update` logo depois de
   ativar; nenhuma documentação encontrada diz explicitamente.
4. **O `docker compose config -q` do 3.4 antes do primeiro deploy**: se o `compose.prod.yaml`
   interpolar a tag da imagem de uma variável que só o `vigilume-entrada` define, o `config -q`
   pode reclamar de variável vazia. Conferir com o arquivo final do grupo vps.
5. **Os rótulos do painel da Hostinger e do Resend** vêm da documentação pública de 2026-09 e
   podem ter mudado; nenhum passo depende de um rótulo exato para funcionar, mas o iniciante
   depende deles para achar o botão.
6. **O `gh` com `read:packages`** no ensaio (7.2): o token padrão do `gh` não tem essa permissão;
   o `gh auth refresh -s read:packages` resolve, mas abre o navegador.

## Validação feita

- **Fontes** (WebFetch/WebSearch em 2026-09-25): Hostinger — [free domain](https://support.hostinger.com/en/articles/1583407-how-to-register-a-domain-for-free),
  [point a domain to your VPS](https://www.hostinger.com/support/1583227-how-to-point-a-domain-to-your-vps-at-hostinger/),
  [managed VPS firewall](https://www.hostinger.com/support/8172641-how-to-use-a-managed-vps-firewall-at-hostinger/),
  [browser terminal](https://www.hostinger.com/support/7978544-how-to-use-the-browser-terminal-in-hostinger/),
  [VPS dashboard](https://www.hostinger.com/support/5726606-how-to-use-the-vps-dashboard-in-hostinger/),
  [backups e snapshots](https://www.hostinger.com/support/1583232-how-to-back-up-or-restore-a-vps),
  [emergency mode](https://support.hostinger.com/en/articles/5726577-how-to-use-emergency-mode-on-your-vps),
  [SSH troubleshooting](https://www.hostinger.com/support/9969102-how-to-troubleshoot-ssh-connection-issues-on-vps/);
  Resend — [add a domain](https://resend.com/docs/add-a-domain),
  [domain not verifying](https://resend.com/docs/knowledge-base/what-if-my-domain-is-not-verifying);
  Docker — [install on Ubuntu](https://docs.docker.com/engine/install/ubuntu/),
  [containerd image store](https://docs.docker.com/engine/storage/containerd/);
  healthchecks.io — [pinging API](https://healthchecks.io/docs/http_api/). As fontes da Hostinger,
  do Resend e do Docker estão citadas no próprio runbook, no passo que as usa.
- **Comandos:** os 85 blocos `bash` do runbook passam no `bash -n`; o subcomando e as mensagens do
  `vigilume user` (`create`, `set-password`, `reset-2fa`, `--email`, senha de 15 a 128, códigos
  0/1/2/3/4/130) conferidos em `cmd/vigilume/user.go` e `internal/auth/password.go`; as mensagens
  de log citadas (`TRUSTED_PROXIES vazio`, `cabeçalho de IP do cliente …`, `migrations aplicadas`,
  `sondador ativo`, `servindo`) em `internal/config/config.go`, `internal/httpapi/middleware/clientip.go`
  e `cmd/vigilume/main.go`; os rótulos do painel (Configurações, Verificação em duas etapas,
  Canais de notificação, Novo canal, Novo alvo, "Enviamos o link de verificação.") em `web/src`;
  as flags do `gh` (`secret set` lendo do stdin, `run download`, `workflow run -f`) pelo `--help`
  do `gh` da máquina; `ssh-keyscan -t`, `ssh-keygen -lf` do OpenSSH 9.6 local. `age`, `nmap` e
  `shellcheck` não estão instalados aqui: as flags do `age` (`-o`, `-y`, `-r`, `-R`, `-d -i`) e do
  `nmap` (`-Pn -p- --open`) são as da documentação deles.
- **Checador** (`conferir-docs.sh`, no Step 3 da R-1): antes da R-1 (em `aebc4d0`), 5 falhas pela
  razão certa (runbook e pendências ausentes, `fly.md` presente, referência viva e Fly no README);
  depois da R-1, só a referência viva (fechada pela R-2) e os marcadores; depois da R-2, só os
  marcadores. Numa cópia com todo `⟦…⟧` trocado por texto, **0 falhas** (o checador sabe ficar
  verde).
- **Sabotagens** (feitas e restauradas de backup em `scratchpad/p11-runbook/bak`, conferidas com
  `cmp`): **8 vermelhas, nenhuma verde no estado final** (S1–S8; a S9 repete a S5). Uma sabotagem nasceu **verde** e revelou um buraco no
  checador: citar `deploy/backup/verificar2.sql` dentro de um bloco de comando passava, porque ele
  só olhava caminhos entre crases; o checador passou a olhar todo caminho `deploy/…`, `docs/…`,
  `internal/…` do runbook e do README, e a sabotagem ficou vermelha (S3).
- **Gates:** `go test -count=1 ./internal/config/` e `go vet ./internal/config/` verdes na R-2;
  `docker compose -f compose.yaml config -q` válido depois do comentário novo. Nada mais de Go
  mudou.

---

### Task R-1: o runbook da VPS e os documentos vivos sem o Fly

Spec do Plano 11, §1 (critérios 1 a 3), §5, §6 e §7. Roda **depois** das tarefas dos grupos vps e
workflows: o runbook descreve os arquivos deles. Documentação, sem teste Go; a prova é o checador
do Step 3, que falha antes (runbook ausente, `fly.md` presente) e depois só pelos marcadores.

**Os pontos marcados `⟦…⟧` dependem de nomes finais dos outros grupos** e o controlador os
preenche na montagem (a tabela está em "Interfaces entre grupos"). Nenhum `⟦` pode sobrar no
commit: o Step 3 confere.

**Files:**
- Create: `docs/deploy/vps.md`
- Delete: `docs/deploy/fly.md`
- Modify: `README.md` ("Rodar local", a tabela "Decisões de segurança" com duas linhas novas de
  produção, "Stack", "Deploy e operação" inteira, "O que ficou de fora", "Licença")
- Create: `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`
- Modify: `docs/superpowers/plans/2026-09-22-roadmap.md` (linhas 11 e 12)

**Interfaces:**
- Consumes: os nomes fixos das instruções comuns e os do grupo workflows (acima); os marcados.
- Produces: `docs/deploy/vps.md` e a numeração dos passos.

- [ ] **Step 1: Confira o que os documentos vão citar**

Run:

```bash
ls deploy/vps/ deploy/vps/db/Dockerfile deploy/ci/vps.sh deploy/ci/checar-workflows.sh
grep -n 'user create\|set-password\|reset-2fa' cmd/vigilume/user.go | head -5
grep -n 'MinPasswordLen *=\|MaxPasswordLen *=' internal/auth/password.go
grep -n 'TRUSTED_PROXIES vazio' internal/config/config.go
grep -n 'msgForaDaLista\|msgSemCabecalho' internal/httpapi/middleware/clientip.go | head -2
grep -rn 'Verificação em duas etapas\|Enviamos o link de verificação' web/src/features --include=*.tsx | head -2
grep -n 'SSH_ORIGINAL_COMMAND' deploy/vps/sudoers-vigilume-entrada deploy/vps/vigilume-entrada
```

Expected: os nove arquivos de `deploy/vps/` e os dois scripts do CI existem; o `user` tem os três
subcomandos; senha de 15 a 128; as mensagens de log e os rótulos do painel com essas grafias; e o
`SSH_ORIGINAL_COMMAND` atravessa o `sudo` (se não, ver "Pergunta para o grupo vps"). Um nome
diferente: ajuste o texto do Step 2 antes de seguir.

- [ ] **Step 2: Escreva o runbook, apague o do Fly, atualize README, roadmap e pendências**

```bash
git rm docs/deploy/fly.md
```

Crie `docs/deploy/vps.md`:

`````markdown
# Vigilume numa VPS da Hostinger: do zero ao ar, e a operação

Runbook **manual**, escrito para quem **nunca usou VPS nem SSH**. Nada aqui roda sozinho: cada
passo é feito por você, na ordem, e só se passa ao seguinte depois da verificação do passo
("**Confira**"). Se uma verificação não der o que o texto diz, pare ali: seguir com um passo
errado quase sempre complica o conserto.

O porquê das escolhas está na spec do Plano 11
(`docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`); as decisões que você ainda
pode revisar estão na §9 dela.

## Como ler este runbook

- **Dois computadores.** Todo bloco de comando diz onde roda:
  - **no seu computador** (o seu Linux, num terminal: `Ctrl+Alt+T` no Ubuntu);
  - **na VPS** (o servidor da Hostinger, num terminal conectado a ela por SSH, que você abre no
    bloco 2).
- **Um comando por vez.** Copie a linha inteira, cole no terminal (`Ctrl+Shift+V`), aperte Enter e
  leia o que apareceu antes de colar a próxima. Linhas que começam com `#` são comentários: o
  terminal as ignora.
- **`sudo` pede a sua senha** (a do seu usuário na VPS, criada no passo 2.5). Ao digitá-la, nada
  aparece na tela: é normal.
- **Segredos só vão para dois lugares:** o gerenciador de senhas e o arquivo `/opt/vigilume/.env`
  na VPS. Nunca num chat, num e-mail, num print, num arquivo do repositório ou num campo do
  GitHub. Os três segredos que o GitHub precisa (bloco 4) vão por um comando que lê de arquivo,
  sem colar nada.
- **O `compose.yaml` da raiz do repositório não vai para o servidor.** Ele cria um usuário de
  demonstração com senha pública e usa chaves públicas: num servidor, qualquer um entraria como
  dono. O servidor usa só os arquivos de `deploy/vps/`.

## Topologia

Uma VPS (Hostinger KVM 2: 2 vCPU, 8 GB, 100 GB NVMe, datacenter no Brasil) com Ubuntu 24.04 LTS e
Docker. Em `/opt/vigilume`, um `compose.prod.yaml` com três serviços numa rede interna
`vigilume` (172.30.0.0/24, sem IPv6):

| Serviço | O que é | Endereço | Porta publicada |
|---|---|---|---|
| `caddy` | Proxy com HTTPS automático (Let's Encrypt), só para o seu domínio | 172.30.0.2 | **80 e 443, e só ele** |
| `app` | O binário do Vigilume (`--role=all`): API, painel, sondador, avisos, manutenção | 172.30.0.3 | nenhuma |
| `db` | Postgres 18, a imagem oficial, fixada por digest | 172.30.0.4 | nenhuma |

Por fora: o GitHub Actions entrega cada versão nova por SSH, com uma chave que só sabe fazer
`deploy` e `backup` (bloco 4); o backup diário sai cifrado para o GitHub (bloco 7); e o
healthchecks.io avisa por e-mail se o site parar de responder (bloco 8).

## Mapa

| Bloco | O quê | Onde |
|---|---|---|
| 0 | Gerenciador de senhas, o arquivo de variáveis, as ferramentas do seu computador | seu computador |
| 1 | Chaves SSH, domínio, Resend, healthchecks.io | seu computador, painéis |
| 2 | Primeiro acesso e endurecimento da VPS | VPS |
| 3 | Docker, `/opt/vigilume`, segredos, chave do backup | VPS e seu computador |
| 4 | Usuário `deploy`, secrets do GitHub, primeiro deploy | VPS, seu computador, GitHub |
| 5 | Seu usuário no Vigilume e o 2FA | VPS, navegador |
| 6 | E-mail, canal e alvo de teste | navegador |
| 7 | Backup ligado e ensaio de restauração | GitHub, seu computador |
| 8 | Vigia externo | VPS, healthchecks.io |
| 9 | Verificações finais | seu computador, VPS |
| 10 | Operação: deploys, logs, 2FA perdido, Postgres, "a VPS morreu", repositório público | — |

Tempo estimado da primeira vez: uma tarde, sem contar a espera do DNS e do Resend (de minutos a
algumas horas).

---

## Bloco 0: antes de tudo

### 0.1 Gerenciador de senhas

**Faça:** escolha um gerenciador de senhas (Bitwarden ou KeePassXC servem) e crie nele uma pasta
"Vigilume". Tudo o que este runbook mandar "guardar no gerenciador" vai para lá, cada coisa numa
entrada com o nome indicado.

**Por quê:** a VPS pode morrer, e com ela o `.env`; o que está no gerenciador é o que permite
reconstruir (bloco 10). Perder a `MASTER_KEY`, em especial, torna ilegíveis os headers de alvo e as
configurações de canal para sempre: nenhum backup devolve a chave.

**Confira:** a pasta existe e você sabe abri-la no computador e no celular.

### 0.2 Ferramentas do seu computador

**Faça** (no seu computador):

```bash
sudo apt update
sudo apt install -y openssh-client age nmap git curl
gh auth status
```

**Por quê:** `ssh`/`scp` falam com a VPS; `age` decifra o backup; `nmap` confere de fora quais
portas estão abertas; `gh` grava os secrets do GitHub sem você colar nada.

**Deve aparecer:** a instalação termina sem `E:`; o `gh auth status` diz `Logged in to github.com`
com a sua conta. Se o `gh` não estiver instalado ou logado: `sudo apt install gh` e
`gh auth login` (escolha GitHub.com, HTTPS, e entre pelo navegador).

**Confira:**

```bash
age --version
nmap --version | head -1
git -C ~/Projetos/Go/Vigilume remote -v
```

Três respostas, a última com `BipoXDXD/vigilume`. (Se o seu clone estiver em outro lugar, use o
caminho dele em todo este runbook.)

### 0.3 O arquivo de variáveis do seu computador

Os comandos deste runbook usam três nomes, que você define uma vez num arquivo:

- `VPS`: o endereço IPv4 da VPS (você o acha no passo 1.3);
- `DOMINIO`: o domínio grátis da Hostinger (passo 1.4), sem `https://` e sem `www`;
- `EU`: o nome do seu usuário administrador na VPS (passo 2.5), em minúsculas, sem espaço (ex.:
  `lucas`).

**Faça** (no seu computador), trocando os três valores de exemplo quando os tiver (pode criar o
arquivo agora e corrigir depois, com `nano ~/.vigilume-vps`):

```bash
cat > ~/.vigilume-vps <<'FIM'
VPS=203.0.113.10
DOMINIO=exemplo.com
EU=lucas
FIM
. ~/.vigilume-vps
```

**Por quê:** nenhum dos três é segredo, e tê-los num arquivo evita digitar errado o IP no meio de
um comando.

**Toda vez que abrir um terminal novo no seu computador**, rode `. ~/.vigilume-vps` antes dos
comandos deste runbook.

**Confira:** `echo "$VPS $DOMINIO $EU"` mostra os três valores.

---

## Bloco 1: chaves, domínio e contas

### 1.1 Chave SSH do administrador

**Faça** (no seu computador):

```bash
ssh-keygen -t ed25519 -C "vigilume-admin" -f ~/.ssh/vigilume_admin
```

Ele pede uma *passphrase* duas vezes: invente uma frase longa, guarde-a no gerenciador (entrada
"Vigilume – passphrase da chave SSH") e digite-a. Nada aparece enquanto digita.

**Por quê:** a VPS vai aceitar entrada **só** por chave, nunca por senha (passo 2.7). A chave
privada (`~/.ssh/vigilume_admin`) nunca sai do seu computador; a pública (`.pub`) vai para a VPS.
A passphrase protege a chave se alguém copiar o arquivo.

**Deve aparecer:** `Your identification has been saved in /home/…/.ssh/vigilume_admin` e um
desenho de caracteres (o *randomart*).

**Confira:** `ls -l ~/.ssh/vigilume_admin*` lista dois arquivos, o sem `.pub` com permissão
`-rw-------`.

**Cópia de segurança:** guarde também o **conteúdo** da chave privada no gerenciador (entrada
"Vigilume – chave SSH do administrador", como anexo ou nota segura): `cat ~/.ssh/vigilume_admin`
mostra o texto. Sem ela e sem o computador, a volta é pelo terminal do painel (bloco 10, "Perdi a
chave SSH").

### 1.2 Conta da Hostinger e a VPS

**Faça** (no navegador): entre em <https://hpanel.hostinger.com>. No menu **VPS**, clique em
**Manage** ao lado do seu servidor. Na página **OS & Panel → Operating System**, confira o sistema.

**Deve aparecer:** **Ubuntu 24.04** puro (sem painel como CyberPanel, sem "Docker" pré-instalado).
Se for outro, reinstale ali mesmo escolhendo **Ubuntu 24.04** simples. Reinstalar apaga a VPS, o
que numa VPS nova não custa nada.

**Por quê:** o runbook foi escrito e conferido para o Ubuntu 24.04 limpo; um painel ou um Docker
de outra origem mudaria portas, firewall e atualizações.

**Faça também:** em **Settings → Main settings**, defina uma senha de root longa (gere no
gerenciador; entrada "Vigilume – senha de root da VPS"). Ela só serve para o terminal do painel
em emergência: o SSH de root vai ser desligado no passo 2.7.

### 1.3 O IP da VPS e a chave pública no painel

**Faça:** na página **Overview** da VPS, copie o endereço **IPv4** e ponha-o no `~/.vigilume-vps`
(`VPS=…`, passo 0.3). Depois, no seu computador:

```bash
cat ~/.ssh/vigilume_admin.pub
```

Copie a linha inteira (começa com `ssh-ed25519`, termina com `vigilume-admin`). No painel, em
**Settings → SSH keys**, clique em adicionar, dê o nome `vigilume-admin` e cole a linha. (A chave
**pública** não é segredo: é a única exceção à regra de "não colar", e ela é pública por desenho.)

**Por quê:** a Hostinger instala essa chave para o root, e é com ela que você entra pela primeira
vez (passo 2.1).

**Confira:** a chave aparece listada em **SSH keys**.

### 1.4 O domínio apontado para a VPS

Fonte: Hostinger, [How to get a free domain](https://support.hostinger.com/en/articles/1583407-how-to-register-a-domain-for-free)
e [How to point a domain to your VPS](https://www.hostinger.com/support/1583227-how-to-point-a-domain-to-your-vps-at-hostinger/).

**Faça:**

1. Se ainda não pegou o domínio grátis do plano: na página inicial do painel, **Claim Domain** →
   registre um nome novo (planos VPS não têm transferência grátis, só registro). Ponha o nome no
   `~/.vigilume-vps` (`DOMINIO=…`).
2. Vá em **Domain portfolio** → **Manage** ao lado do domínio → **DNS / Nameservers**.
3. **Apague** qualquer registro `A`, `AAAA` ou `CNAME` cujo nome seja `@` ou `www` (os que a
   Hostinger cria apontando para a hospedagem dela).
4. Adicione um registro **A**: **Name** `@`, **Points to** o IPv4 da VPS, **TTL** o padrão.

**Por quê:** o registro `A` diz ao mundo que `seu-domínio` é a sua VPS; o Caddy só consegue o
certificado HTTPS quando isso já vale. Sem `AAAA`: o Vigilume é configurado e conferido só em
IPv4. Sem `www`: o Caddy atende um nome só (o `www` não teria certificado).

**Deve aparecer:** na lista do DNS, uma linha `A  @  <o IP da VPS>`, e nenhum `AAAA`/`CNAME` para
`@` ou `www`.

**Confira** (no seu computador; a propagação pode levar de minutos até 24 h):

```bash
getent ahostsv4 "$DOMINIO" | head -1
```

Deve mostrar o IP da VPS. Se mostrar outro IP ou nada, espere e repita; siga para os passos 1.5 e
1.6 enquanto isso, mas **não** faça o primeiro deploy (4.8) antes disto dar certo.

### 1.5 Resend: domínio verificado e chave de API

Fonte: Resend, [Add and verify a domain](https://resend.com/docs/add-a-domain) e
[What if my domain is not verifying?](https://resend.com/docs/knowledge-base/what-if-my-domain-is-not-verifying).

**Faça:**

1. Crie a conta em <https://resend.com>.
2. **Domains → Add Domain.** Use um **subdomínio** do seu domínio, como `avisos.seu-domínio`
   (recomendação do Resend: separa a reputação de envio do domínio principal). Região: a mais
   próxima do Brasil que ele oferecer.
3. O Resend mostra os registros DNS a criar (SPF e DKIM: `TXT` e `MX`, ou `CNAME` em domínios
   novos). Para cada um, no painel da Hostinger (**DNS / Nameservers** do domínio), adicione um
   registro do **mesmo tipo**, com o **Name** e o **valor** exatamente como o Resend mostra.
   Atenção ao **Name**: a Hostinger completa o domínio sozinha. Se o Resend mostra
   `resend._domainkey.avisos.seu-domínio`, digite só `resend._domainkey.avisos`; se mostra
   `send.avisos.seu-domínio`, digite `send.avisos`. O DKIM diferencia maiúsculas de minúsculas:
   copie com o botão de copiar do Resend.
4. De volta ao Resend, clique no botão de verificar o domínio.
5. **API Keys → Create API Key**: nome `vigilume-vps`, permissão **Sending access**, restrita ao
   domínio `avisos.seu-domínio`. A chave aparece **uma vez**: guarde-a direto no gerenciador
   (entrada "Vigilume – RESEND_API_KEY"). Ela só volta a ser usada no passo 3.3.

**Por quê:** sem domínio verificado, todo envio do Vigilume volta com erro 403 do Resend; o SPF e
o DKIM são o que faz o e-mail de alerta não cair no spam. A chave só de envio e só daquele domínio
limita o estrago se vazar.

**Deve aparecer:** o domínio com status **Verified** (muitas vezes em 15 minutos; o Resend diz que
pode levar até 72 h).

**Confira:** status **Verified** na lista de domínios. Se ficar **Pending** por horas, compare
registro a registro com o painel da Hostinger (o erro mais comum é o domínio repetido no **Name**).

Anote o remetente que vai usar, no formato `Vigilume <alertas@avisos.seu-domínio>`: é o
`EMAIL_FROM` do passo 3.3 (não é segredo).

### 1.6 healthchecks.io

**Faça:**

1. Crie a conta em <https://healthchecks.io> (o plano grátis basta) com o e-mail em que quer
   receber o alarme.
2. No projeto, **Add Check**: nome `vigilume-healthz`; **Period** `1 minute`; **Grace Time**
   `5 minutes`.
3. Copie a **ping URL** do check (o formato é `https://hc-ping.com/<uuid>`) para o gerenciador
   (entrada "Vigilume – HC_URL").

**Por quê:** um timer na VPS vai pingar essa URL a cada minuto quando o site responder, e pingar
`/fail` quando não responder (bloco 8). Se os pings pararem (VPS morta) ou falharem, o
healthchecks manda e-mail: o Vigilume não consegue avisar da própria queda. A carência de 5 min
evita alarme no reinício automático das 04:30 (passo 2.11). A URL é quase-segredo: quem a tem
pode pingar por você e esconder uma queda.

**Confira:** o check aparece com status **new** (cinza) e o canal de e-mail da conta está ativo
(**Integrations**).

---

## Bloco 2: primeiro acesso e endurecimento

### 2.1 Primeiro SSH, conferindo a impressão digital

**Faça, primeiro no painel:** na página **Overview** da VPS, clique em **Web console** (ou
**Browser terminal**; fonte: Hostinger, [How to use the Browser terminal](https://www.hostinger.com/support/7978544-how-to-use-the-browser-terminal-in-hostinger/)).
Abre uma aba com um terminal já logado como root. Nele:

```bash
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

**Deve aparecer:** uma linha como `256 SHA256:AbC…xyz root@… (ED25519)`. Deixe essa aba aberta.

**Faça, depois, no seu computador:**

```bash
ssh -i ~/.ssh/vigilume_admin root@"$VPS"
```

Ele pergunta `Are you sure you want to continue connecting (yes/no/[fingerprint])?` e mostra um
`ED25519 key fingerprint is SHA256:…`.

**Por quê:** essa pergunta só aparece na primeira conexão. Se a impressão digital for a do terminal
do painel, você está falando com a sua VPS, e não com alguém no meio do caminho.

**Confira:** o `SHA256:…` da pergunta é **idêntico** ao do terminal do painel. Só então digite
`yes`, e a passphrase da chave. O prompt vira `root@…:~#`: você está **na VPS**.

Se forem diferentes, digite `no` e pare: releia o IP no `~/.vigilume-vps`.

### 2.2 Atualizar o sistema

**Faça** (na VPS, como root):

```bash
apt update && apt full-upgrade -y
```

**Por quê:** a imagem do Ubuntu da Hostinger é de semanas ou meses atrás; as correções de
segurança do intervalo entram agora.

**Deve aparecer:** a lista de pacotes atualizados e o prompt de volta, sem linhas começando com
`E:`. Se aparecer uma tela azul perguntando sobre um arquivo de configuração, aceite o padrão
(Enter).

**Confira:**

```bash
ls /var/run/reboot-required 2>/dev/null && echo "precisa reiniciar" || echo "sem reinício"
```

Se disser `precisa reiniciar`: `reboot`, espere um minuto e entre de novo com o mesmo `ssh` do
passo 2.1 (sem a pergunta, desta vez).

### 2.3 Fuso horário e relógio

**Faça** (na VPS, como root):

```bash
timedatectl set-timezone America/Sao_Paulo
timedatectl set-ntp true
timedatectl status
```

**Por quê:** o reinício automático (passo 2.11) é às 04:30 **no horário da VPS**; o fuso de
Brasília faz esse horário ser o seu. O relógio certo (NTP, pelo `systemd-timesyncd`) é exigência
do 2FA (os códigos mudam a cada 30 s) e dos rollups de uptime.

**Deve aparecer:** `Time zone: America/Sao_Paulo (-03, -0300)`, `System clock synchronized: yes`,
`NTP service: active`.

**Confira:** `systemctl is-active systemd-timesyncd` diz `active`. (Se `synchronized` ainda for
`no`, espere um minuto e rode `timedatectl status` de novo.)

### 2.4 Uma foto da VPS antes de mexer no SSH (opcional, recomendado)

**Faça:** no painel, **Backups & Monitoring → Snapshots & Backups → Create Snapshot**.

**Por quê:** os próximos passos mexem no acesso; se algo der muito errado, restaurar o snapshot
volta a VPS a este ponto. O snapshot da Hostinger expira em 24 h e só existe um por vez
([fonte](https://www.hostinger.com/support/1583232-how-to-back-up-or-restore-a-vps)). Restaurar
**apaga tudo** o que veio depois.

**Confira:** o snapshot aparece listado com a data de hoje.

### 2.5 Seu usuário administrador

**Faça** (na VPS, como root), trocando `lucas` pelo valor de `EU` do seu `~/.vigilume-vps`:

```bash
adduser lucas
```

Ele pede uma senha duas vezes (gere uma longa no gerenciador, entrada "Vigilume – senha sudo do
administrador") e depois nome, telefone etc.: deixe em branco com Enter e confirme com `Y`.

```bash
usermod -aG sudo lucas
install -d -m 700 -o lucas -g lucas /home/lucas/.ssh
install -m 600 -o lucas -g lucas /root/.ssh/authorized_keys /home/lucas/.ssh/authorized_keys
```

**Por quê:** ninguém entra mais como root (passo 2.7). Você entra como `lucas`, só com a chave, e
vira root com `sudo` quando precisa, digitando a senha: quem roubar só a chave não vira root.

**Confira**, num **terminal novo** do seu computador (deixe o do root aberto, é a sua rede de
segurança):

```bash
. ~/.vigilume-vps
ssh -i ~/.ssh/vigilume_admin "$EU@$VPS"
```

e, já na VPS, como você:

```bash
sudo whoami
```

Deve pedir a senha do `sudo` e responder `root`. Só siga se deu certo.

### 2.6 A chave no `~/.ssh/config` do seu computador

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
cat >> ~/.ssh/config <<FIM

Host vigilume
    HostName $VPS
    User $EU
    IdentityFile ~/.ssh/vigilume_admin
    IdentitiesOnly yes
FIM
chmod 600 ~/.ssh/config
```

**Por quê:** a partir daqui, `ssh vigilume` e `scp arquivo vigilume:` bastam; o `IdentitiesOnly`
evita que o SSH ofereça outras chaves suas e conte tentativas no `fail2ban` (passo 2.8).

**Confira:** `ssh vigilume true && echo ok` imprime `ok` (depois da passphrase, se o seu
chaveiro não a lembrar).

### 2.7 SSH sem senha e sem root

**Faça** (na VPS, como você):

```bash
sudo tee /etc/ssh/sshd_config.d/00-vigilume.conf >/dev/null <<'FIM'
# Endurecimento do Vigilume (spec do Plano 11, §5). O nome começa com 00 porque
# o sshd usa o PRIMEIRO valor que lê, e os arquivos deste diretório são lidos em
# ordem alfabética: um 50-cloud-init.conf com PasswordAuthentication yes não
# vence este.
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
FIM
sudo sshd -t && sudo systemctl restart ssh
sudo sshd -T | grep -Ei '^(permitrootlogin|passwordauthentication|kbdinteractiveauthentication) '
```

**Por quê:** robôs tentam senhas em toda VPS do mundo o dia inteiro; sem senha aceita, não há o
que adivinhar. A porta continua 22: mudar a porta só diminui o barulho do log, não é segurança.

**Deve aparecer:** o `sshd -t` não imprime nada (configuração válida); as três linhas finais são
`permitrootlogin no`, `passwordauthentication no` e `kbdinteractiveauthentication no`.

**Confira**, **sem fechar** o terminal em que você está (num terminal novo do seu computador):

```bash
. ~/.vigilume-vps
ssh vigilume true && echo "entrada por chave: ok"
ssh -o PubkeyAuthentication=no -o PreferredAuthentications=password "$EU@$VPS" true
ssh -i ~/.ssh/vigilume_admin root@"$VPS" true
```

A primeira imprime `entrada por chave: ok`; a segunda e a terceira terminam em
`Permission denied (publickey).` Se a primeira falhar, **não feche** o terminal aberto: desfaça
com `sudo rm /etc/ssh/sshd_config.d/00-vigilume.conf && sudo systemctl restart ssh` e releia os
passos 2.5 e 2.6.

**Confira também o terminal do painel:** feche a aba do **Web console** e abra de novo. Anote o
que acontece: se ele entrar sozinho, ótimo; se pedir usuário e senha, entre com o **seu** usuário
e a senha do `sudo`. Ele é a sua porta de emergência (bloco 10, "Perdi a chave SSH"): garanta
agora que ela abre. Se não abrir de jeito nenhum, a emergência passa a ser o **Emergency mode** do
painel (**Settings**, [fonte](https://support.hostinger.com/en/articles/5726577-how-to-use-emergency-mode-on-your-vps)).

### 2.8 fail2ban

**Faça** (na VPS):

```bash
sudo apt install -y fail2ban python3-systemd
sudo tee /etc/fail2ban/jail.d/vigilume.local >/dev/null <<'FIM'
[sshd]
enabled  = true
backend  = systemd
maxretry = 5
findtime = 10m
bantime  = 1h
FIM
sudo systemctl enable fail2ban
sudo systemctl restart fail2ban
```

**Por quê:** mesmo sem senha, cada tentativa gasta log e CPU; o `fail2ban` bloqueia por uma hora o
IP que errar 5 vezes em 10 minutos. `backend = systemd` lê o diário do sistema, que sempre existe.

**Deve aparecer:** nada de erro.

**Confira:**

```bash
sudo fail2ban-client status sshd
```

Mostra `Status for the jail: sshd` com `Currently failed` e `Currently banned` (provavelmente 0).
Se você mesmo for bloqueado um dia: pelo terminal do painel,
`sudo fail2ban-client set sshd unbanip <seu IP>`.

### 2.9 Firewall da VPS (ufw)

**Faça** (na VPS):

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

O `enable` avisa que pode derrubar conexões SSH: responda `y`. (A 22 foi liberada antes, então a
sua não cai.) ⟦VPS: se o compose.prod.yaml publicar 443/udp para HTTP/3, acrescente aqui `sudo ufw allow 443/udp` e a regra UDP 443 no passo 2.10; se não, apague esta frase⟧

**Por quê:** fecha tudo o que chega, menos SSH (22), HTTP (80, que o Caddy usa para o certificado e
para redirecionar) e HTTPS (443). **Atenção:** o Docker passa por cima do `ufw` nas portas que um
contêiner publica. Quem garante que só 80 e 443 ficam abertas é o `compose.prod.yaml` (só o Caddy
publica porta) e o firewall do painel (passo seguinte); o bloco 9 confere de fora.

**Confira:**

```bash
sudo ufw status verbose
```

`Status: active`, `Default: deny (incoming), allow (outgoing)`, e as três portas com `ALLOW IN`
(em IPv4 e `(v6)`). E, num terminal novo do seu computador, `ssh vigilume true && echo ok`
continua dando `ok`.

### 2.10 Firewall do painel da Hostinger

Fonte: Hostinger, [How to use a managed VPS firewall](https://www.hostinger.com/support/8172641-how-to-use-a-managed-vps-firewall-at-hostinger/).

**Faça** (no painel da VPS): **Security → Firewall** → **Add Firewall**, nome `vigilume`,
**Create**. No `⋯` ao lado dele, **Edit**, e em **Add firewall rule** crie três regras, cada uma
com **Action** `accept`, **Protocol** `TCP`, **Traffic Source** `anywhere`, e **Port** `22`, `80`
e `443` (uma por regra; **Add rule** a cada uma). Confira que o firewall está **ativo** para esta
VPS.

**Por quê:** é a segunda camada, fora da VPS: ela vale mesmo se o Docker, um erro de configuração
ou um pacote novo abrir uma porta lá dentro. A Hostinger descarta todo tráfego que nenhuma regra
aceita, e aplica a mudança em até 2 minutos. Com isso o `ping` para de responder: é esperado.

**Confira**, depois de 2 minutos:

```bash
# no seu computador
ssh vigilume true && echo "ssh ok"
```

```bash
# na VPS: a saída continua livre (o sondador precisa dela)
curl -sS -o /dev/null -w '%{http_code}\n' https://example.com/
sudo apt update
```

`ssh ok`; o `curl` responde `200`; o `apt update` termina sem `E:`. Se a saída falhar, o firewall
do painel está bloqueando as respostas: revise as regras (só entrada, `accept`) antes de seguir.

### 2.11 Atualizações automáticas, com reinício às 04:30

**Faça** (na VPS):

```bash
sudo apt install -y unattended-upgrades
sudo tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'FIM'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
FIM
sudo tee /etc/apt/apt.conf.d/52vigilume-reinicio >/dev/null <<'FIM'
// Spec do Plano 11, §5 e §9 (decisão 1): reinicia sozinho, de madrugada, quando
// uma atualização de segurança (o kernel, quase sempre) pedir. A hora é a do
// fuso da VPS (America/Sao_Paulo, passo 2.3).
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:30";
FIM
```

**Por quê:** correções de segurança entram todo dia sem você lembrar; uma que exige reinício é
aplicada às 04:30 (horário de Brasília), com ~1 minuto fora do ar. Os contêineres voltam sozinhos
(`restart: unless-stopped` no compose). `WithUsers` reinicia mesmo com alguém logado: uma sessão
SSH esquecida aberta não pode segurar uma correção de kernel para sempre.

**Confira:**

```bash
apt-config dump | grep -E 'Periodic::(Update-Package-Lists|Unattended-Upgrade)|Automatic-Reboot'
sudo unattended-upgrade --dry-run 2>&1 | tail -3
```

As cinco linhas com os valores acima (`"1"`, `"1"`, `"true"`, `"true"`, `"04:30"`); o *dry-run*
termina sem `Traceback` nem `error`.

---

## Bloco 3: Docker, `/opt/vigilume`, segredos e a chave do backup

### 3.1 Docker pelo repositório oficial

Fonte: Docker, [Install Docker Engine on Ubuntu](https://docs.docker.com/engine/install/ubuntu/).

**Faça** (na VPS):

```bash
sudo apt install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
gpg --show-keys /etc/apt/keyrings/docker.asc
```

**Confira** que a impressão digital mostrada é `9DC8 5822 9FC7 DD38 854A  E2D8 8D81 803C 0EBF CD88`
(a chave de assinatura da Docker). Se for outra, pare: não é a chave da Docker.

```bash
sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<FIM
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
FIM
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

**Por quê:** o Docker do repositório da própria Docker é o suportado e o que recebe correção
primeiro; o `docker.io` do Ubuntu fica para trás. (O `unattended-upgrades` só aplica as
atualizações de segurança do Ubuntu: o Docker você atualiza no passo mensal do bloco 10.)

**Confira:** `sudo docker run --rm hello-world` imprime `Hello from Docker!`.

### 3.2 Configuração do Docker: logs, `live-restore` e o image store do containerd

**Faça** (na VPS):

```bash
sudo tee /etc/docker/daemon.json >/dev/null <<'FIM'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true,
  "features": { "containerd-snapshotter": true }
}
FIM
sudo systemctl restart docker
sudo docker info -f '{{.LoggingDriver}} {{.LiveRestoreEnabled}}'
sudo docker info -f '{{.DriverStatus}}'
```

**Por quê:** sem rotação, o log de um contêiner cresce até encher o disco (aqui, no máximo 3
arquivos de 10 MB por contêiner). O `live-restore` mantém os contêineres de pé quando o próprio
Docker é atualizado ou reiniciado. E o deploy (bloco 4) confere que a imagem carregada é a
aprovada comparando o ID dela com o digest do GitHub, o que só funciona com o *image store* do
containerd: ele é o padrão das instalações novas do Docker 29
([fonte](https://docs.docker.com/engine/storage/containerd/)), e a linha `features` o garante
mesmo numa instalação que veio de uma versão anterior. O `vigilume-entrada` recusa o deploy (sai
com 1 e diz o motivo) se o store não for o do containerd.

**Deve aparecer:** `json-file true` e, na última linha, algo com
`[[driver-type io.containerd.snapshotter.v1]]`.

**Confira:** se a última linha **não** tiver `io.containerd.snapshotter.v1`, o `daemon.json` não
foi lido: `sudo cat /etc/docker/daemon.json` (tem de ser exatamente o JSON acima) e
`sudo journalctl -u docker -n 20` (um erro de JSON aparece ali). Não siga sem ela. (Trocar de store
esconde as imagens do store antigo; numa VPS nova, só o `hello-world` some, e não faz falta.)

### 3.3 Os arquivos do Vigilume em `/opt/vigilume`

**Faça, primeiro no seu computador**, com a `main` atualizada (os arquivos da VPS têm de ser os da
versão que vai para o ar):

```bash
cd ~/Projetos/Go/Vigilume
git switch main && git pull --ff-only
ssh vigilume 'mkdir -p ~/vigilume-arquivos'
scp deploy/vps/compose.prod.yaml deploy/vps/Caddyfile deploy/vps/initdb.sh deploy/vps/env.exemplo \
    deploy/vps/vigilume-entrada deploy/vps/sudoers-vigilume-entrada \
    deploy/vps/vigia.sh deploy/vps/vigilume-vigia.service deploy/vps/vigilume-vigia.timer \
    vigilume:vigilume-arquivos/
```

**Deve aparecer:** uma linha de progresso por arquivo, nove no total.

**Depois, na VPS:**

```bash
cd ~/vigilume-arquivos
sudo install -d -m 0755 -o root -g root /opt/vigilume
sudo install -m 0644 -o root -g root compose.prod.yaml Caddyfile /opt/vigilume/
sudo install -m 0755 -o root -g root initdb.sh /opt/vigilume/
sudo install -m 0600 -o root -g root env.exemplo /opt/vigilume/.env
ls -la /opt/vigilume
```

**Por quê:** `/opt/vigilume` é o lugar fixo que o script de deploy usa. Tudo é do root: o usuário
`deploy` (bloco 4) não escreve ali, só o script que ele pode chamar. O `.env` nasce do modelo, sem
valores, com permissão `600` (só o root lê).

**Deve aparecer:** `compose.prod.yaml`, `Caddyfile`, `initdb.sh` e `.env`, todos com dono `root`;
o `.env` com `-rw-------`.

⟦VPS: se o vigilume-entrada precisar de arquivos ou pacotes do host além destes (por exemplo o `age`, o `deploy/backup/backup.sh`, o `verificar.sql` ou o `deploy/compose/initdb.sql` para o backup), o comando exato de instalação vai aqui, com o destino; se não precisar, apague este parágrafo⟧

### 3.4 Os segredos, gerados na VPS

**Faça** (na VPS):

```bash
cd /opt/vigilume
for v in ⟦VPS-SENHA-DO-SUPERUSUARIO⟧ VIGILUME_APP_PASSWORD VIGILUME_BACKUP_PASSWORD; do
  sudo sed -i "s|^$v=\$|$v=$(openssl rand -hex 32)|" .env
done
for v in MASTER_KEY JWT_SECRET IP_HASH_KEY; do
  sudo sed -i "s|^$v=\$|$v=$(openssl rand -base64 32)|" .env
done
```

(`⟦VPS-SENHA-DO-SUPERUSUARIO⟧` é o nome, no `env.exemplo`, da senha do superusuário do Postgres.)

**Por quê:** segredo gerado na própria VPS nunca passou pela rede nem por outro computador. As
senhas do Postgres são hexadecimais porque entram em URLs `postgres://usuário:senha@…`, e o `/`,
o `+` e o `=` do base64 quebrariam a URL. As três chaves do app são 32 bytes aleatórios em
base64, o formato que o boot exige.

**Agora preencha o que não é gerado**, no editor:

```bash
sudo nano /opt/vigilume/.env
```

- ⟦VPS: nome da variável do domínio⟧: o seu domínio, sem `https://` (o valor de `DOMINIO`).
- `RESEND_API_KEY`: cole a chave do gerenciador (passo 1.5). Este `.env` é um dos dois lugares
  onde segredo pode ser colado.
- `EMAIL_FROM`: `Vigilume <alertas@avisos.seu-domínio>` (passo 1.5).
- `HC_URL`: a ping URL do healthchecks (passo 1.6).

Salve com `Ctrl+O` e Enter, saia com `Ctrl+X`.

⟦VPS: conferir que o env.exemplo tem exatamente estas variáveis e nenhuma outra sem valor; se o compose de produção derivar PUBLIC_BASE_URL, CORS_ORIGIN, DATABASE_URL e TRUSTED_PROXIES sozinho (do domínio, das senhas e do IP fixo do Caddy), dizer isso aqui em uma frase⟧

**Confira:**

```bash
sudo stat -c '%U %a' /opt/vigilume/.env
sudo grep -cE '^[A-Z_]+=$' /opt/vigilume/.env
sudo docker compose -f /opt/vigilume/compose.prod.yaml config -q && echo "compose ok"
```

`root 600`; `0` (nenhuma variável vazia); `compose ok`.

**Guarde no gerenciador**, agora, cada valor (uma entrada por nome, "Vigilume – MASTER_KEY" e
assim por diante):

```bash
sudo cat /opt/vigilume/.env
clear
```

Copie cada valor do terminal para o gerenciador e rode o `clear` em seguida. É a única vez que
eles aparecem na tela. O mais importante é a `MASTER_KEY`: sem ela, os headers de alvo e as
configurações de canal ficam ilegíveis para sempre, e nenhum backup a devolve. O `JWT_SECRET` e o
`IP_HASH_KEY` podem ser trocados a qualquer hora (bloco 10) com custo pequeno; as senhas do
Postgres e a `MASTER_KEY`, não.

**Confira:** o gerenciador tem uma entrada por variável do `.env`, e o valor de uma delas, tirado
ao acaso, confere com o do terminal (ex.: `sudo grep '^JWT_SECRET=' /opt/vigilume/.env` e compare
com os olhos, depois `clear`).

### 3.5 A chave do backup (`age`): a privada fica só com você

**Faça** (no seu computador, **fora** do diretório do repositório):

```bash
age-keygen -o ~/vigilume-backup.key
chmod 600 ~/vigilume-backup.key
age-keygen -y ~/vigilume-backup.key > ~/vigilume-age-recipient.txt
cat ~/vigilume-age-recipient.txt
```

**Por quê:** o backup diário sai da VPS já cifrado com a chave **pública**; só a **privada**,
que nunca vai para a VPS nem para o GitHub, abre os dumps. Quem invadir a VPS ou ler os artifacts
do GitHub vê só texto cifrado. Fora do repositório para um `git add -A` nunca a pegar (o
`.gitignore` também cobre `*.key`, por garantia).

**Deve aparecer:** o `age-keygen -o` imprime `Public key: age1…`, e o `cat` mostra a mesma linha
`age1…`.

**Guarde** o conteúdo de `~/vigilume-backup.key` no gerenciador (entrada "Vigilume – chave
privada do backup (age)"): `cat ~/vigilume-backup.key`. **Perder a chave privada = perder todos
os backups.**

**Leve a pública para a VPS:**

```bash
scp ~/vigilume-age-recipient.txt vigilume:vigilume-arquivos/
ssh -t vigilume 'sudo install -m 0644 -o root -g root ~/vigilume-arquivos/vigilume-age-recipient.txt /opt/vigilume/age-recipient'
```

**Confira** (no seu computador): a pública da VPS abre com a sua privada.

```bash
ssh vigilume cat /opt/vigilume/age-recipient | diff - ~/vigilume-age-recipient.txt && echo "iguais"
echo "ida e volta" | age -R ~/vigilume-age-recipient.txt | age -d -i ~/vigilume-backup.key
```

`iguais` e `ida e volta`.

---

## Bloco 4: o usuário `deploy`, os secrets do GitHub e o primeiro deploy

O GitHub Actions entrega cada versão nova por SSH, como o usuário `deploy`. Esse usuário não tem
shell, não tem `sudo` geral e não está no grupo `docker`: a chave dele só consegue rodar
`/usr/local/sbin/vigilume-entrada`, e o script só aceita dois pedidos, `deploy` e `backup`.

### 4.1 O script de entrada e a regra do `sudo`

**Faça** (na VPS):

```bash
cd ~/vigilume-arquivos
sudo install -m 0755 -o root -g root vigilume-entrada /usr/local/sbin/vigilume-entrada
sudo visudo -cf sudoers-vigilume-entrada
sudo install -m 0440 -o root -g root sudoers-vigilume-entrada /etc/sudoers.d/vigilume-entrada
sudo visudo -c
```

**Por quê:** o `sudoers` deixa o `deploy` rodar como root **só** esse script. O `visudo -c` confere
a sintaxe antes e depois: um `sudoers` quebrado pode travar o `sudo` de todo mundo (se acontecer,
conserte pelo terminal do painel).

**Deve aparecer:** `parsed OK` nas duas conferências (a segunda lista todos os arquivos).

**Confira:** `ls -l /usr/local/sbin/vigilume-entrada /etc/sudoers.d/vigilume-entrada` mostra
`-rwxr-xr-x root root` e `-r--r----- root root`.

### 4.2 O usuário `deploy`

**Faça** (na VPS):

```bash
sudo useradd --create-home --shell /bin/sh deploy
id deploy
sudo passwd -S deploy
```

**Por quê:** o `deploy` existe só para receber a chave do GitHub. A senha nasce travada (ninguém
entra por senha, e o SSH nem aceita senha). O shell `/bin/sh` é o que o SSH usa para executar o
comando forçado; interativo ele nunca fica, por causa do `restrict,command=` do passo 4.4.

**Deve aparecer:** `id` com `groups=…(deploy)` e **nenhum** outro grupo (nem `sudo`, nem
`docker`); `passwd -S` com `deploy L` (*locked*).

### 4.3 A chave do deploy (no seu computador)

**Faça** (no seu computador):

```bash
ssh-keygen -t ed25519 -N "" -C "vigilume-deploy-github" -f ~/.ssh/vigilume_deploy
scp ~/.ssh/vigilume_deploy.pub vigilume:vigilume-arquivos/
```

**Por quê:** uma chave só para o GitHub, sem passphrase (o Actions não tem quem a digite). Ela
não precisa ser guardada: depois do passo 4.6 ela é apagada, e se um dia precisar, gera-se outra.

### 4.4 O `authorized_keys` do `deploy`, com a chave presa ao script

**Faça** (na VPS):

```bash
sudo install -d -m 0755 -o root -g root /home/deploy/.ssh
printf 'restrict,command="sudo /usr/local/sbin/vigilume-entrada" %s\n' \
  "$(cat ~/vigilume-arquivos/vigilume_deploy.pub)" \
  | sudo tee /home/deploy/.ssh/authorized_keys >/dev/null
sudo chmod 0644 /home/deploy/.ssh/authorized_keys
sudo cat /home/deploy/.ssh/authorized_keys
```

**Por quê:** `command=` faz toda conexão com essa chave rodar o script, seja qual for o comando
pedido (o pedido chega ao script como texto, e ele recusa o que não for `deploy` ou `backup`);
`restrict` desliga terminal, túnel de porta, agente e X11. O diretório e o arquivo são do root:
nem o próprio `deploy` pode trocar a chave.

**Deve aparecer:** uma linha só, começando com
`restrict,command="sudo /usr/local/sbin/vigilume-entrada" ssh-ed25519 AAAA` e terminando em
`vigilume-deploy-github`.

**Confira** (no seu computador), que a chave **só** faz o que deve:

```bash
. ~/.vigilume-vps
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes deploy@"$VPS" ; echo "saiu com $?"
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes deploy@"$VPS" ls / ; echo "saiu com $?"
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes -W 172.30.0.4:5432 deploy@"$VPS" ; echo "saiu com $?"
```

- As duas primeiras: ⟦VPS: a mensagem de uso que o vigilume-entrada imprime⟧ e `saiu com 2` (pedido
  recusado; nada de lista de arquivos, nada de terminal). A primeira pode mostrar antes
  `PTY allocation request failed on channel 0`: é o `restrict` negando o terminal, como deve.
- A terceira: `open failed: administratively prohibited` e `saiu com 255` (o túnel para o banco
  é recusado).

Se alguma pedir senha, a chave não foi aceita: confira o passo 4.4 e `sudo journalctl -u ssh -n 20`
na VPS.

### 4.5 A chave do servidor, para o GitHub

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
ssh vigilume cat /etc/ssh/ssh_host_ed25519_key.pub > ~/vigilume-host-key.txt
cat ~/vigilume-host-key.txt
ssh-keygen -lf ~/vigilume-host-key.txt
ssh-keyscan -t ed25519 "$VPS" 2>/dev/null | awk '{print $2, $3}'
```

**Por quê:** o Actions vai conferir que fala com a **sua** VPS (e não com alguém no meio) pela
chave do servidor fixada no secret `VPS_HOST_KEY`, sem `StrictHostKeyChecking=no`. O valor é a
linha inteira de `/etc/ssh/ssh_host_ed25519_key.pub`. Ela vem pela sua conexão SSH, cuja chave
você já conferiu no passo 2.1; o `ssh-keyscan` a busca de novo, por fora, para comparar.

**Deve aparecer:** o `cat` mostra uma linha `ssh-ed25519 AAAA… root@…`; o `ssh-keygen -lf`,
`256 SHA256:… (ED25519)`; o `ssh-keyscan`, `ssh-ed25519 AAAA…` com o mesmo `AAAA…` do `cat`.

**Confira:** o `SHA256:…` é idêntico ao do `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`
do terminal do painel (passo 2.1), e o `AAAA…` do `ssh-keyscan` é igual ao do `cat`. Se algo
diferir, pare e não grave o secret.

### 4.6 Os secrets do GitHub (sem colar nada)

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
gh secret set VPS_HOST --repo BipoXDXD/vigilume --body "$VPS"
gh secret set VPS_SSH_KEY --repo BipoXDXD/vigilume < ~/.ssh/vigilume_deploy
gh secret set VPS_HOST_KEY --repo BipoXDXD/vigilume < ~/vigilume-host-key.txt
gh secret list --repo BipoXDXD/vigilume
```

**Por quê:** o `gh` lê o valor do arquivo e o cifra antes de mandar; nada passa pela área de
transferência nem pela tela. No GitHub Free com repositório privado não há *environments*: são
secrets de repositório, e quem pode disparar o deploy é quem tem acesso de escrita (hoje, só
você). Secrets de repositório não chegam a PR de fork.

**Deve aparecer:** a lista com `VPS_HOST`, `VPS_HOST_KEY` e `VPS_SSH_KEY` (com a data de hoje). São
os únicos secrets que o deploy e o backup usam; se aparecer algum outro, de uma hospedagem
anterior, apague-o com `gh secret delete <nome> --repo BipoXDXD/vigilume`. A única variável é a
`BACKUP_ENABLED` (bloco 7): `gh variable list --repo BipoXDXD/vigilume` mostra as que existem, e
outras antigas saem com `gh variable delete <nome> --repo BipoXDXD/vigilume`.

**Agora apague a chave do deploy do seu computador:**

```bash
shred -u ~/.ssh/vigilume_deploy
rm ~/.ssh/vigilume_deploy.pub
```

**Confira:** `ls ~/.ssh/vigilume_deploy*` diz que não existe.

### 4.7 Limpar a pasta de trabalho da VPS

**Faça** (na VPS):

```bash
rm -r ~/vigilume-arquivos
```

**Por quê:** os arquivos já estão instalados nos lugares certos; cópias soltas envelhecem e
confundem. Na próxima mudança de `deploy/vps/` você recopia (bloco 10, "Arquivos da VPS").

⟦VPS: se o primeiro deploy precisar do banco e do Caddy de pé antes (o verbo `deploy` só faz `up -d app`), o comando que os sobe e o que conferir (`db` healthy, `docker compose exec db psql …` com os papéis `vigilume_app f f` e `vigilume_backup f t`) vai aqui como passo 4.7b; se o verbo sobe tudo, apague este parágrafo⟧

### 4.8 O primeiro deploy, pelo Actions

**Antes:** o `getent` do passo 1.4 já devolve o IP da VPS (o Caddy precisa disso para o
certificado).

**Faça** (no navegador, ou no seu computador):

1. Ache o SHA completo (40 caracteres) do último commit da `main` com o job `publish` do `ci`
   verde. A tag `:<sha>` da imagem só existe depois que o Trivy aprovou:
   ```bash
   gh run list --repo BipoXDXD/vigilume --workflow ci.yml --branch main --limit 3
   git -C ~/Projetos/Go/Vigilume rev-parse origin/main
   ```
2. **Actions → ⟦WORKFLOWS: nome do workflow de deploy⟧ → Run workflow**, na `main`, com o SHA no campo
   ⟦WORKFLOWS: nome do input do SHA⟧. Ou, pelo terminal:
   ```bash
   SHA=$(git -C ~/Projetos/Go/Vigilume rev-parse origin/main)
   echo "$SHA"
   gh workflow run deploy.yml --repo BipoXDXD/vigilume --ref main -f ⟦WORKFLOWS-INPUT-DO-SHA⟧="$SHA"
   gh run watch --repo BipoXDXD/vigilume
   ```

**Por quê:** o deploy é sempre manual e sempre de uma imagem que passou no CI e no Trivy. O job
resolve a tag no digest, baixa a imagem do GHCR, manda por SSH (`docker save | ssh deploy@… deploy
<sha> <id>`), e o script na VPS confere o ID, sobe a versão nova, espera o healthcheck por até
90 s e volta sozinho à anterior se ela não ficar saudável.

**Deve aparecer:** o job verde. Os códigos do script, se ficar vermelho, estão no bloco 10
("Depuração do deploy").

**Confira** (na VPS):

```bash
sudo docker compose -f /opt/vigilume/compose.prod.yaml ps
cat /opt/vigilume/versao
sudo docker compose -f /opt/vigilume/compose.prod.yaml logs app | head -20
```

- `ps`: `caddy`, `app` e `db` com estado `running` (e `healthy` onde há healthcheck);
- `versao`: o SHA que você mandou;
- `logs`: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`, `servindo`, e
  **nenhuma** linha `TRUSTED_PROXIES vazio`.

**E de fora** (no seu computador):

```bash
. ~/.vigilume-vps
curl -sS "https://$DOMINIO/healthz"; echo
curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"
```

`/healthz` responde (o corpo do app) sem erro de certificado, e `/readyz` dá `200`. Se o `curl`
reclamar do certificado, espere um minuto (o Caddy pode estar terminando de pegar o certificado)
e veja `sudo docker compose -f /opt/vigilume/compose.prod.yaml logs caddy | tail -20`: um erro de
"challenge" quase sempre é DNS ainda não propagado ou porta 80 fechada no firewall do painel.

**Um atalho para os próximos comandos** (na VPS; opcional, mas o resto do runbook o usa):

```bash
echo "alias vc='sudo docker compose -f /opt/vigilume/compose.prod.yaml'" >> ~/.bash_aliases
. ~/.bash_aliases
vc ps
```

**Confira no banco** quem é dono das tabelas (as migrations rodaram como o papel comum):

```bash
vc exec db psql -U postgres -d vigilume -c "SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY 1;"
vc exec db psql -U postgres -c "SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname LIKE 'vigilume%' ORDER BY 1;"
```

Só `vigilume_app` na primeira; `vigilume_app | f | f` e `vigilume_backup | f | t` na segunda.

---

## Bloco 5: seu usuário no Vigilume e o 2FA

### 5.1 Criar o dono

**Faça** (na VPS), com o seu e-mail no lugar do exemplo:

```bash
vc exec app /vigilume user create --email voce@exemplo.com
```

Ele pede `Senha:` e `Confirme a senha:`, sem eco. Gere a senha no gerenciador (entrada "Vigilume
– login do painel"; de 15 a 128 caracteres) e cole-a nas duas perguntas.

**Por quê:** o sistema é de um dono só, e não há cadastro pela web: o primeiro usuário nasce pelo
CLI, dentro do contêiner que já tem a `DATABASE_URL`. A senha vem do terminal, nunca de argumento
(argumento aparece no `ps` e no histórico). O `docker compose exec` já abre um terminal, por isso
não ecoa.

**Deve aparecer:** `usuário criado: voce@exemplo.com (id …)`. Se disser
`já existe um usuário; nada foi alterado` (código 3), alguém já criou: use
`vc exec app /vigilume user set-password --email <e-mail>` para trocar a senha.

### 5.2 Entrar e ligar o 2FA

**Faça** (no navegador): abra `https://seu-domínio`, entre com o e-mail e a senha. Vá em
**Configurações → Verificação em duas etapas → Ativar**, leia o QR code com o seu app
autenticador (Aegis, 2FAS, Google Authenticator…), digite o código de 6 dígitos e guarde os
**códigos de recuperação** no gerenciador (entrada "Vigilume – códigos de recuperação do 2FA").

**Por quê:** com o painel na internet, a senha sozinha é pouco. Os códigos de recuperação são a
volta se o celular sumir; sem eles e sem o app, só o `reset-2fa` pelo CLI (bloco 10).

**Confira:** saia (**Sair**), entre de novo: depois da senha, ele pede o código do app. A tela
**Configurações** mostra a verificação em duas etapas ativada.

---

## Bloco 6: e-mail, canal e alvo de teste

### 6.1 Canal de e-mail

**Faça** (no painel): **Canais de notificação → Novo canal**, tipo **E-mail**, com o seu e-mail.

**Por quê:** confere ponta a ponta o Resend (domínio verificado, chave, remetente) e os links, que
usam a `PUBLIC_BASE_URL` de produção.

**Deve aparecer:** `Enviamos o link de verificação.` e, em até alguns minutos, um e-mail do
remetente `Vigilume <alertas@avisos.seu-domínio>` com um link para `https://seu-domínio/…`.
Clique nele.

**Confira:** o canal aparece como verificado. Se o e-mail não chegar: a caixa de spam; depois
`vc logs app | grep -i resend | tail` (um `403` é domínio não verificado ou `EMAIL_FROM` com
outro domínio).

(Opcional: um canal do Discord pelo mesmo caminho; ele manda uma mensagem de teste na hora.)

### 6.2 Alvo de teste e um incidente de verdade

**Faça** (no painel): **Novo alvo** com a URL `https://example.com/`, intervalo de 30 s e
**status esperado 500** (de propósito: o site responde 200, então o Vigilume o verá fora do
ar).

**Por quê:** força um incidente sem depender de derrubar nada: confere sondador, detector,
fila de avisos e e-mail, tudo de uma vez.

**Deve aparecer:** em um ou dois minutos, o alvo fica fora do ar no painel, um incidente abre e
chega o e-mail de alerta.

**Confira:** depois do e-mail, edite o alvo e ponha o status esperado **200**. Em mais um ou dois
minutos o incidente fecha e chega o aviso de que voltou. Então apague o alvo, ou deixe-o com 200
como monitor de exemplo.

---

## Bloco 7: backup ligado e ensaio de restauração

O backup diário (06:17 UTC, 03:17 de Brasília) roda no GitHub: `ssh deploy@VPS backup` devolve o
dump **já cifrado** com a sua chave pública, que vira um artifact de 30 dias. Na VPS, o script
sobe um Postgres descartável, faz o dump como `vigilume_backup`, restaura a cópia e a confere
(`deploy/backup/backup.sh` e `verificar.sql`) antes de cifrar. Um backup que não restaura não é
backup.

### 7.1 Ligar e rodar o primeiro à mão

**Faça** (no seu computador):

```bash
gh variable set BACKUP_ENABLED --repo BipoXDXD/vigilume --body 1
gh workflow run backup.yml --repo BipoXDXD/vigilume --ref main
gh run watch --repo BipoXDXD/vigilume
```

(O `gh run watch` pergunta qual execução acompanhar: escolha a do `backup` que acabou de começar.)

**Por quê:** até a variável existir, o agendamento diário termina em *skipped* sem fazer nada.
Rodar à mão agora confere tudo (chave, script, `age`, artifact) antes de confiar no automático.

**Deve aparecer:** o job verde, e no resumo uma linha `Postgres: N% …` com a ocupação do disco.

**Confira** (na VPS), que o dump em texto claro não ficou no disco:

```bash
sudo find / -xdev -name '*.dump' -newer /opt/vigilume/versao 2>/dev/null
```

Nenhuma linha (a restauração de conferência roda num banco descartável, e só o cifrado sai da
VPS).

**Depois de ligado, um `backup` agendado *skipped* (cinza) é falha**, tanto quanto um vermelho: não
houve backup naquele dia, e o GitHub não manda e-mail por isso. A causa usual é a variável
`BACKUP_ENABLED` apagada ou mudada.

### 7.2 Ensaio de restauração (antes de dar o Vigilume por pronto)

**Faça** (no seu computador):

1. Baixe o artifact da execução do passo 7.1:
   ```bash
   mkdir -p ~/vigilume-restauro && cd ~/vigilume-restauro
   RUN_ID=$(gh run list --repo BipoXDXD/vigilume --workflow backup.yml --status success --limit 1 \
     --json databaseId -q '.[0].databaseId')
   echo "$RUN_ID"
   gh run download "$RUN_ID" --repo BipoXDXD/vigilume
   ARQ=$(find ~/vigilume-restauro -name 'vigilume-*.dump.age' | sort | tail -1)
   echo "$ARQ"
   ```
   Deve aparecer o caminho de um arquivo `vigilume-AAAAMMDD.dump.age` (o dump do Postgres, em
   formato custom, cifrado).
2. Decifre, com a chave que só você tem:
   ```bash
   age -d -i ~/vigilume-backup.key -o ~/vigilume-restauro/vigilume.dump "$ARQ"
   ```
3. Restaure num Postgres descartável, da mesma imagem de produção (o digest de
   `deploy/vps/db/Dockerfile`), só no `127.0.0.1` do seu computador:
   ```bash
   cd ~/Projetos/Go/Vigilume
   docker run -d --name restauro -p 127.0.0.1:25433:5432 -e POSTGRES_PASSWORD=restauro \
     "$(sed -n 's/^FROM //p' deploy/vps/db/Dockerfile)"
   until docker exec restauro pg_isready -h 127.0.0.1 -U postgres; do sleep 1; done
   docker exec -i restauro psql -U postgres < deploy/compose/initdb.sql
   docker exec -i restauro pg_restore -U postgres --exit-on-error -d vigilume < ~/vigilume-restauro/vigilume.dump
   ```
   As mesmas checagens do backup, com os números da linha `schema N, U usuário(s), A alvo(s)` do
   log daquela execução (`gh run view "$RUN_ID" --repo BipoXDXD/vigilume --log | grep 'schema '`):
   ```bash
   docker exec -i restauro psql -U postgres -d vigilume -v versao=N -v usuarios=U -v alvos=A \
     < deploy/backup/verificar.sql
   ```
   Deve terminar em `NOTICE:  cópia conferida`.
4. Suba o Vigilume **da versão que está no ar** contra a cópia, **só com a API** (`--role=api`):
   com `--role=all`, a cópia sondaria os seus alvos e mandaria alertas aos canais **reais**. A
   imagem é privada: entre no GHCR com o token do `gh`, depois de dar a ele a permissão
   `read:packages` (o `refresh` abre o navegador uma vez):
   ```bash
   gh auth refresh -s read:packages
   gh auth token | docker login ghcr.io -u BipoXDXD --password-stdin
   SHA=$(ssh vigilume cat /opt/vigilume/versao)
   read -rs -p 'MASTER_KEY de produção (do gerenciador): ' MASTER_KEY; echo
   export MASTER_KEY
   docker run --rm --network host \
     -e DATABASE_URL='postgres://vigilume_app:vigilume_app@127.0.0.1:25433/vigilume?sslmode=disable' \
     -e MASTER_KEY \
     -e JWT_SECRET="$(openssl rand -base64 32)" -e IP_HASH_KEY="$(openssl rand -base64 32)" \
     -e PUBLIC_BASE_URL=http://localhost:8080 -e CORS_ORIGIN=http://localhost:8080 \
     -e HTTP_ADDR=127.0.0.1:8080 -e METRICS_ADDR=off \
     "ghcr.io/bipoxdxd/vigilume:$SHA" --role=api
   ```
   O `read -rs` lê a chave sem eco e sem gravá-la no histórico; o `-e MASTER_KEY` sem valor passa
   a variável ao contêiner sem ela aparecer na linha de comando. A senha `vigilume_app` vale aqui
   porque o `initdb.sql` é o do compose local (a de demonstração, num banco que só existe no seu
   `127.0.0.1`). `JWT_SECRET` e `IP_HASH_KEY` são novos: a cópia não aceita sessões de produção.
5. Num navegador, `http://localhost:8080`: entre com o seu usuário de produção (senha e 2FA) e
   abra um alvo que tenha header, ou um canal. **É isto que o ensaio prova**: os dados voltam, e a
   `MASTER_KEY` do gerenciador é a certa (com a errada, header e canal dão erro).
6. Limpe:
   ```bash
   # Ctrl+C no terminal do docker run, e então:
   unset MASTER_KEY
   docker rm -f restauro
   shred -u ~/vigilume-restauro/vigilume.dump
   ```

**Confira:** o passo 5 funcionou. Anote a data do ensaio no gerenciador: ele se repete a cada
trimestre (bloco 10).

### 7.3 A segunda camada: backup semanal da Hostinger

**Confira** no painel, **Backups & Monitoring → Snapshots & Backups**, que os backups automáticos
estão ligados (a Hostinger faz um semanal; fonte acima, passo 2.4). Eles guardam a VPS inteira e
servem para "a VPS morreu" (bloco 10) quando o de ontem for velho demais. Restaurar um deles
sobrescreve a VPS inteira.

---

## Bloco 8: o vigia externo

**Faça** (no seu computador, na `main` atualizada):

```bash
cd ~/Projetos/Go/Vigilume
ssh vigilume 'mkdir -p ~/vigilume-arquivos'
scp deploy/vps/vigia.sh deploy/vps/vigilume-vigia.service deploy/vps/vigilume-vigia.timer vigilume:vigilume-arquivos/
```

**Depois, na VPS:**

```bash
cd ~/vigilume-arquivos
sudo install -m 0755 -o root -g root vigia.sh ⟦VPS-DESTINO-DO-VIGIA⟧
sudo install -m 0644 -o root -g root vigilume-vigia.service vigilume-vigia.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vigilume-vigia.timer
rm -r ~/vigilume-arquivos
```

(`⟦VPS-DESTINO-DO-VIGIA⟧` é o caminho que o `ExecStart` do `vigilume-vigia.service` espera.)

**Por quê:** a cada minuto o timer faz um `curl` em `https://seu-domínio/healthz`, passando por
DNS, certificado, Caddy e app como um visitante; se der certo, pinga a `HC_URL` do `.env`; se não,
pinga `HC_URL/fail`. O healthchecks manda e-mail quando chega um `/fail` ou quando os pings
param (VPS morta, rede fora).

**Confira:**

```bash
systemctl list-timers vigilume-vigia.timer
sudo journalctl -u vigilume-vigia.service -n 5 --no-pager
```

O timer com a próxima execução em menos de um minuto; o diário sem erro. No healthchecks.io, o
check `vigilume-healthz` fica **up** (verde) em até dois minutos.

**Confira que o alarme funciona**, derrubando o app de propósito (na VPS):

```bash
vc stop app
```

Em um ou dois minutos o check fica **down** e chega o e-mail do healthchecks. Então:

```bash
vc start app
```

O check volta a **up**, e chega o e-mail de volta.

---

## Bloco 9: verificações finais

Cada item abaixo é uma das obrigações do Plano 10 (`docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`,
"Obrigações do Plano 11") ou um critério da spec. Só dê o Vigilume por pronto com todos verdes.

### 9.1 Só 22, 80 e 443 abertas, vistas de fora (obrigação 3)

**Faça** (no seu computador, **fora** da VPS; demora alguns minutos):

```bash
. ~/.vigilume-vps
sudo nmap -Pn -p- --open "$VPS"
```

**Deve aparecer:** `22/tcp open ssh`, `80/tcp open http`, `443/tcp open https`, e **nenhuma**
outra porta (sem 5432, sem 8080, sem 9090).

**E por dentro** (na VPS):

```bash
sudo ss -tlnp
```

Escutando em todos os endereços (`0.0.0.0` ou `[::]`): só a 22 (`sshd`) e a 80 e 443
(`docker-proxy`). O resto, se houver, só em `127.0.0.x` (como o DNS local do Ubuntu, `127.0.0.53`).

**Confira no compose:**

```bash
vc config | grep -n -A3 'ports:'
```

Só o serviço `caddy` tem `ports`.

### 9.2 O IP do cliente chega certo (obrigações 1 e 2)

**Faça** (na VPS), depois de ter usado o painel por uns minutos:

```bash
vc logs app | grep -c 'TRUSTED_PROXIES vazio'
vc logs app | grep 'cabeçalho de IP do cliente' | tail -3
```

**Deve aparecer:** `0` e nenhuma linha. O primeiro aviso diria que o app não confia em proxy
nenhum; o segundo (`… ignorado` ou `… ausente ou ilegível`), que o `TRUSTED_PROXIES` não bate com
o IP do Caddy (172.30.0.2).

**O teste de duas redes** (no seu computador; um e-mail que não existe e senha errada):

```bash
. ~/.vigilume-vps
for i in 1 2 3 4 5 6; do
  curl -s -o /dev/null -w '%{http_code}\n' -X POST "https://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
    -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}'
done
```

- Da sua rede de casa: cinco `401` e um `429`.
- Logo depois, do celular em 4G/5G (o mesmo `curl` no Termux, ou abra o painel e tente entrar
  com a senha errada): `401`, não `429`. Os baldes de cada cliente estão separados.
- De novo de casa, forjando os cabeçalhos que o Caddy remove:
  ```bash
  curl -s -o /dev/null -w '%{http_code}\n' -X POST "https://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
    -H 'X-Real-IP: 203.0.113.77' -H 'Fly-Client-IP: 203.0.113.77' -H 'CF-Connecting-IP: 203.0.113.77' \
    -H 'X-Forwarded-For: 203.0.113.77' \
    -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}'
  ```
  Continua `429`: o cabeçalho forjado não escolhe o balde.

Se o 4G levar `429`, todos os clientes estão dividindo um balde: o `TRUSTED_PROXIES` do app não é
o IP do Caddy. Pare e confira o `compose.prod.yaml` e o `.env`.

### 9.3 `Host` estranho não chega ao app (obrigação 5, a parte do Caddy)

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "http://$VPS/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "https://$DOMINIO/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' "https://$VPS/healthz"; echo "curl saiu com $?"
```

**Deve aparecer:** ⟦VPS: o que o Caddy responde a um Host desconhecido nas três formas (por exemplo, sem 200 do app, e a terceira falhando no TLS)⟧. Nunca `200` com
o corpo do Vigilume.

**Por quê:** o Caddy só tem o bloco do seu domínio; uma requisição com outro nome não chega ao app.

### 9.4 Certificado e cabeçalhos

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
echo | openssl s_client -connect "$DOMINIO:443" -servername "$DOMINIO" 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
curl -sI "https://$DOMINIO/" | grep -iE '^(HTTP|strict-transport-security|content-security-policy|x-content-type-options)'
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' "http://$DOMINIO/"
```

**Deve aparecer:** `subject` com o seu domínio, `issuer` do Let's Encrypt (ou do ZeroSSL, a
alternativa automática do Caddy) e `notAfter` uns 90 dias à frente; a resposta `200` com
`Strict-Transport-Security`, `Content-Security-Policy` e `X-Content-Type-Options`; e o `http://`
redirecionando (`308`) para `https://`.

### 9.5 Segredos e compose de produção (obrigações 4 e 5)

**Faça** (na VPS):

```bash
vc config --services
vc config | grep -nE 'mem_limit|GOMEMLIMIT|restart:'
vc exec app /vigilume version
```

**Deve aparecer:** os serviços `caddy`, `app` e `db`, e **nenhum** `seed`; um `mem_limit` e um
`restart: unless-stopped` por serviço, e o `GOMEMLIMIT` no app; a versão igual ao
`/opt/vigilume/versao`.

Nenhuma senha de produção está no repositório: o `.env` só existe na VPS e no gerenciador, e o
`gitleaks` do CI varre o histórico em todo PR.

### 9.6 Reboot de teste: tudo volta sozinho

**Faça** (na VPS):

```bash
sudo reboot
```

Espere dois minutos e entre de novo (`ssh vigilume`):

```bash
vc ps
systemctl list-timers vigilume-vigia.timer
sudo ufw status | head -1
sudo fail2ban-client status sshd | head -1
timedatectl | grep synchronized
```

**Deve aparecer:** os três serviços `running`/`healthy`; o timer do vigia agendado; `Status:
active`; `Status for the jail: sshd`; `System clock synchronized: yes`. E, de fora,
`curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"` dá `200`. O healthchecks
pode ter visto um minuto fora: é o custo do reinício, dentro da carência de 5 min.

### 9.7 Lista final

| Verificação | Passo |
|---|---|
| SSH só por chave, sem root; `fail2ban`; `ufw` e firewall do painel | 2.7–2.10 |
| Atualizações automáticas com reinício às 04:30 | 2.11 |
| Docker com rotação de log, `live-restore` e o store do containerd | 3.2 |
| `.env` do root, `600`, sem variável vazia, copiado no gerenciador | 3.4 |
| Chave privada do `age` só no seu computador e no gerenciador | 3.5 |
| `deploy` preso ao script; túnel recusado | 4.4 |
| Primeiro deploy verde, papéis e dono das tabelas certos | 4.8 |
| 2FA ligado e códigos de recuperação guardados | 5.2 |
| E-mail e incidente de teste chegaram | 6.1, 6.2 |
| Backup verde e restauração ensaiada | 7.1, 7.2 |
| Vigia externo avisou da queda de propósito | 8 |
| Portas, IP do cliente, `Host`, certificado, compose, reboot | 9.1–9.6 |

---

## Bloco 10: operação

### Os três comandos de todo dia (na VPS)

```bash
vc ps                               # quem está de pé e saudável
vc logs --since 1h app              # o log do app da última hora (troque app por caddy ou db)
vc logs -f --tail 50 app            # acompanhar ao vivo; Ctrl+C sai
```

O vigia: `sudo journalctl -u vigilume-vigia.service --since today`. O deploy e o backup: o log
do próprio job no Actions (o script manda o log dele pela saída de erro, que o job mostra).

### Deploy seguinte

Actions → `ci` na `main` → espere o `publish` verde → Actions → ⟦WORKFLOWS: nome do workflow de deploy⟧ →
**Run workflow** na `main`, com o SHA completo do commit (passo 4.8). O script guarda a versão que
estava no ar em `/opt/vigilume/versao-anterior` e mantém as 3 últimas imagens. Durante a troca há
alguns segundos de `502` (o painel mostra "Resposta inesperada do servidor (502)"; recarregue).

### Voltar uma versão

Rode o mesmo workflow com o SHA anterior (`cat /opt/vigilume/versao-anterior` na VPS). Só é
seguro se as migrations da versão nova forem **aditivas**: o `goose` não roda `Down` sozinho, e o
rollback automático do script também não desfaz migration. Atenção: uma tag `v*` publicada depois
**move** a tag `:<sha>` daquele commit para o digest do build da tag (mesmo código, outro build,
reescaneado); o workflow leva então esse digest.

### Depuração do deploy

Os códigos de saída do `vigilume-entrada deploy`, que o job repete com uma mensagem
(`deploy/ci/vps.sh`), mais o 255 do próprio SSH:

| Código | Quer dizer | O que fazer |
|---|---|---|
| 0 | deploy saudável | nada |
| 2 | pedido recusado: verbo desconhecido, SHA fora do formato (40 hex), ID fora do formato | conferir o SHA passado ao workflow |
| 3 | a imagem recebida não tem o ID esperado | rodar de novo; se repetir, a imagem mudou no caminho: não force, investigue |
| 4 | a versão nova não ficou saudável em 90 s; **a anterior já voltou** | o log do job traz o log do app: corrija e publique outra versão |
| 1 | outro erro (disco, Docker parado, **image store que não é o do containerd**, …) | a mensagem diz qual; para o store, o passo 3.2 |
| 255 | o SSH nem chegou ao script: VPS fora do ar, porta 22 fechada, chave do servidor diferente do `VPS_HOST_KEY` (VPS reinstalada?) ou chave do deploy recusada | `ssh vigilume true` do seu computador; se a VPS foi reinstalada, passos 4.2 a 4.6 de novo |

O `backup` usa os mesmos códigos do `deploy/backup/backup.sh`: 0, cópia restaurada e conferida;
3, uma checagem da cópia falhou (o dump não serve, e nada é guardado); 4, conferido e guardado,
mas o disco passou de 85% (o job fica vermelho no fim, de propósito); 1, outro erro (não houve
backup); 255, o SSH não chegou ao script.

O store: se a mensagem for ⟦VPS: a mensagem exata do vigilume-entrada para store que não é o do containerd⟧, confira `sudo docker info -f '{{.DriverStatus}}'` e o `daemon.json` do passo 3.2.

A imagem do app **não tem shell** (distroless): `vc exec app sh` não funciona. O que serve: os
logs, os subcomandos do binário (`vc exec app /vigilume version`, `… healthcheck`, `… user …`) e o
`psql` do banco (`vc exec db psql -U postgres -d vigilume`). O `vigilume healthcheck` consulta o
`/readyz` com prazo de 3 s:

| Código | Quando |
|---|---|
| 0 | o `/readyz` respondeu 200 dentro dos 3 s |
| 1 | qualquer outra resposta, prazo esgotado, conexão recusada ou `HTTP_ADDR` inválido |
| 2 | argumento a mais (`vigilume healthcheck x`); o `vigilume version` faz o mesmo |
| 130 | interrompido (Ctrl-C ou SIGTERM) |

Uma `PUBLIC_BASE_URL` com usuário e senha (`https://usuario:senha@…`) é recusada no boot: o app
não sobe, e o log diz "PUBLIC_BASE_URL não pode trazer credenciais na URL (a parte com @ antes do
host)", sem repetir o valor.

### Arquivos da VPS: quando algo em `deploy/vps/` muda na `main`

O deploy troca só a imagem do app. Se um PR mudar `compose.prod.yaml`, `Caddyfile`, `initdb.sh`,
`vigilume-entrada`, o `sudoers` ou os arquivos do vigia, recopie o que mudou, como nos passos 3.3,
4.1 e 8 (o `scp` para `~/vigilume-arquivos` e o `sudo install` para o mesmo destino), e aplique:

```bash
vc up -d            # depois de trocar compose.prod.yaml ou Caddyfile
sudo systemctl daemon-reload && sudo systemctl restart vigilume-vigia.timer   # depois do vigia
```

Para ver o que mudou desde a última cópia: no seu computador,
`git log --oneline -- deploy/vps/` e `git diff <commit da última cópia> main -- deploy/vps/`. O
`initdb.sh` só roda num banco **vazio**: trocá-lo não muda um banco que já existe.

### Atualizações do sistema e do Docker

As de segurança do Ubuntu entram sozinhas (passo 2.11). **Uma vez por mês**, na VPS:

```bash
sudo apt update && sudo apt full-upgrade
ls /var/run/reboot-required 2>/dev/null && echo "precisa reiniciar"
```

Isso atualiza também o Docker (que o automático não toca). Com o `live-restore`, os contêineres
seguem de pé durante a atualização do Docker. Se pedir reinício, reinicie numa hora calma (ou
deixe para as 04:30).

### Perdeu o autenticador e os códigos de recuperação

Sem o app **e** sem os códigos, o login para no segundo fator. Na VPS:

```bash
vc exec app /vigilume user reset-2fa --email voce@exemplo.com
```

Ele desliga o 2FA, apaga os códigos de recuperação, zera o bloqueio da conta e encerra todas as
sessões, numa transação só. Não pede senha: quem tem a `DATABASE_URL` (o contêiner) já pode tudo
no banco. A saída diz o e-mail e quantas sessões caíram ("2FA desativado para …; N sessões
encerradas. Acessos já abertos expiram em até 15 minutos."); com o 2FA já desligado, diz "nada
mudou" e sai com 0, **sem** mexer no bloqueio (para destravar uma conta sem 2FA, use o
`set-password`). Código 4 é e-mail não cadastrado; 2, uso errado; 1, banco ou papel. Depois,
entre só com a senha e ative o 2FA de novo (passo 5.2).

Quem ainda tem o app ou um código de recuperação não precisa disto: **Configurações → Trocar
autenticador** troca de celular sem desligar o 2FA.

Esqueceu a senha: `vc exec app /vigilume user set-password --email voce@exemplo.com` (pede a
senha nova duas vezes, sem eco; encerra todas as sessões).

### Perdi a chave SSH, ou fui bloqueado

1. No painel, **Overview → Web console** (passo 2.7). Se o `fail2ban` bloqueou o seu IP:
   `sudo fail2ban-client set sshd unbanip <seu IP>` (o seu IP: <https://ifconfig.me> no
   navegador).
2. Chave perdida: gere outra (passo 1.1) e, pelo terminal do painel, ponha a `.pub` nova em
   `/home/<você>/.ssh/authorized_keys` (apague a linha da velha). Uma linha longa no terminal do
   navegador é chata de colar: alternativa é adicioná-la em **Settings → SSH keys** (vai para o
   root) e, pelo terminal do painel, `sudo cp /root/.ssh/authorized_keys /home/<você>/.ssh/` e
   `sudo chown <você>: /home/<você>/.ssh/authorized_keys`.
3. Se nem o terminal do painel abrir: **Settings → Emergency mode** (fonte no passo 2.7), que dá
   acesso ao disco para consertar o `sshd_config.d` ou o `authorized_keys`.

### Operação do Postgres

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| Backup | O `backup` diário do Actions (dump conferido, cifrado, artifact de 30 dias) + o backup semanal da Hostinger (a VPS inteira) | Automático: conferir que o `backup` segue verde. Vermelho **ou** *skipped* (cinza) é falha |
| Ensaio de restauração | Automático em cada `backup`; à mão a cada trimestre (passo 7.2) | Diário e trimestral |
| Cópia local | Baixe um artifact por mês para o seu computador (ou um disco externo): os do GitHub somem em 30 dias | Mensal |
| Disco | O `backup` avisa acima de 70% e **falha** acima de 85% (o dump do dia ainda é guardado; o job fica vermelho no fim). O disco é o da VPS: `df -h /var/lib/docker` na VPS. Para liberar: logs e imagens velhas (`sudo docker image prune` só depois de conferir `sudo docker image ls`; o script já guarda só 3 versões do app); se for o banco crescendo, o plano maior da Hostinger | Quando avisar |
| Upgrade menor (18.x) | PR do Dependabot em `deploy/vps/db/Dockerfile`. O mesmo digest está em mais lugares, que sobem juntos no mesmo PR (`deploy/vps/db/Dockerfile`, `compose.yaml`, `.github/workflows/ci.yml`, `internal/storage/testdb` ⟦VPS: e o compose.prod.yaml, se o digest estiver nele⟧). Depois do merge, recopie o `compose.prod.yaml` (seção acima) e `vc up -d db` (10–30 s fora). O backup passa a usar a imagem nova sozinho | Quando o Dependabot abrir |
| Upgrade maior (18 → 19) | **Não** vem por PR: o `dependabot.yml` ignora versão maior de `postgres`. Dump e restauração num volume novo (abaixo) | ~1 vez por ano |

#### Upgrade maior do Postgres

Numa janela calma, com o backup do dia verde:

1. Rode o `backup` à mão (passo 7.1) e **ensaie a restauração** com a imagem nova (passo 7.2,
   trocando a imagem do `docker run` pela do 19): prova que o dump do 18 sobe no 19.
2. Na VPS, pare o app e faça um dump fresco (fica em `/root`, que só o root lê, e só durante a
   janela: a VPS já guarda o banco inteiro em claro, então o arquivo não aumenta a exposição):
   ```bash
   sudo -v
   vc stop app
   vc exec -T db pg_dump -U postgres --format=custom vigilume | sudo tee /root/vigilume-antes-do-19.dump >/dev/null
   sudo ls -l /root/vigilume-antes-do-19.dump
   ```
3. Por PR, a imagem nova em `deploy/vps/db/Dockerfile` e nos outros lugares do digest, e o
   `compose.prod.yaml` apontando o `db` para um **volume novo** ⟦VPS: nome do volume do Postgres e como o compose.prod.yaml o declara⟧, para o volume velho continuar
   intacto. Recopie o `compose.prod.yaml` e suba **só o banco**: `vc up -d db` (o `initdb.sh`
   cria os papéis no volume novo com as senhas do `.env`).
4. Restaure no banco novo como superusuário, preservando os donos, e apague o dump:
   ```bash
   sudo -v
   sudo cat /root/vigilume-antes-do-19.dump | vc exec -T db pg_restore -U postgres --exit-on-error -d vigilume
   sudo shred -u /root/vigilume-antes-do-19.dump
   ```
5. `vc up -d app`, confira o passo 4.8 (dono das tabelas, papéis) e rode o `backup` à mão.
6. Mantenha o volume velho por 7 dias antes de apagá-lo (`sudo docker volume rm <volume velho>`).

### A VPS morreu (ou apaguei um alvo)

**Apaguei um alvo, um canal, uma status page:** não restaure a produção inteira por isso.
Restaure o backup de ontem numa cópia local (passo 7.2), olhe a configuração perdida no painel da
cópia (`http://localhost:8080`) e recrie à mão na produção.

**A VPS morreu** (não liga, disco corrompido, conta suspensa):

1. Primeiro o mais barato: **Snapshots & Backups** da Hostinger (passo 7.3). Restaurar o backup
   semanal volta a VPS inteira à semana passada; depois disso, siga do item 4 abaixo com o dump de
   ontem para recuperar a semana.
2. Se não houver backup da Hostinger que sirva: reinstale o Ubuntu 24.04 (ou crie outra VPS) e
   refaça os blocos 2, 3 e 4. **No passo 3.4, não gere segredos novos**: preencha o `.env` com os
   valores do gerenciador (a `MASTER_KEY`, as senhas, tudo). Com outra `MASTER_KEY`, o banco
   restaurado teria headers e canais ilegíveis. O IP pode mudar: atualize o registro `A` (passo
   1.4), o `~/.vigilume-vps`, o `~/.ssh/config`, e grave de novo `VPS_HOST` e `VPS_HOST_KEY`
   (passos 4.5 e 4.6; a VPS nova tem outra chave de servidor, e o `ssh` do seu computador vai
   reclamar dela: `ssh-keygen -R <IP velho>` e confira a nova pelo terminal do painel).
3. Faça o primeiro deploy (passo 4.8) com o SHA que estava no ar (o último deploy verde do Actions).
4. Restaure o último dump do Actions no banco novo: pare o app (`vc stop app`), decifre o dump no
   seu computador (passo 7.2, itens 1 e 2), leve-o com `scp vigilume.dump vigilume:` e, na VPS:
   ```bash
   vc exec -T db dropdb -U postgres vigilume
   vc exec -T db psql -U postgres -c "CREATE DATABASE vigilume OWNER vigilume_app;"
   vc exec -T db pg_restore -U postgres --exit-on-error -d vigilume < ~/vigilume.dump
   shred -u ~/vigilume.dump
   vc start app
   ```
   (O banco é recriado vazio porque o app já migrou um schema no primeiro deploy.)
5. Confira os passos 4.8 (tabelas e papéis), 5.2 (entrar com 2FA), 6.1 (e-mail) e rode o
   `backup` à mão. Perde-se no máximo o que aconteceu desde o dump de ontem (checks e incidentes).

### Trocar um segredo

- `JWT_SECRET`: gere outro (`openssl rand -base64 32`, no `.env` pelo `nano`), guarde no
  gerenciador, `vc up -d app`. Todas as sessões caem; entra-se de novo.
- `IP_HASH_KEY`: o mesmo caminho; os hashes de IP antigos da auditoria deixam de casar com os
  novos.
- `MASTER_KEY` e as senhas do Postgres: **não** há rotação pronta. A `MASTER_KEY` cifra os
  segredos gravados; as senhas dos papéis vivem também no banco (trocar exige `ALTER ROLE` no
  mesmo momento). Se uma delas vazar, é trabalho de plano próprio.
- A chave de deploy do GitHub: passos 4.3 a 4.6 com uma chave nova (a linha antiga do
  `authorized_keys` é substituída).

### Minutos de Actions

Com o repositório privado, o CI gasta a cota gratuita (2.000 min/mês na conta pessoal): ~15 min de
runner por push somando os jobs, dos quais o `e2e` é o maior, e alguns minutos por dia do
`backup`. Um push novo no mesmo branch cancela a execução anterior. Se a cota apertar, o primeiro
corte é o `e2e` só na `main` (`if: github.event_name == 'push'` no job).

## Tornar o repositório público (passo futuro)

O repositório é **privado por decisão do dono**. No dia de abrir:

1. Varredura do histórico inteiro: `gitleaks git . --redact -v` (o job `secrets` do CI já faz
   isso em todo PR). O falso positivo conhecido está no `.gitleaksignore`, com o motivo.
2. **Se aparecer um segredo real:** considere-o vazado e **rotacione primeiro** (Resend, Discord,
   o que for). Depois, tire-o do histórico com `git filter-repo --replace-text` num clone novo,
   `git push --force` de todos os branches e tags, e rode a varredura de novo até zerar.
   Reescrever a história não desfaz um vazamento.
3. O e-mail pessoal do autor está no histórico e **fica** (decisão do dono).
4. Settings → General → Change visibility. O pacote do GHCR tem visibilidade própria: Package
   settings → Change visibility → Public. Confira de uma máquina sem login:
   `docker pull ghcr.io/bipoxdxd/vigilume:latest`.
5. No README, troque o parágrafo "enquanto o repositório for privado" pelo dos 30 segundos: o
   `docker compose up` passa a puxar a imagem em vez de construir, sem mudar o compose.
6. **Deploy com revisor:** num repositório público, o GitHub Free tem *environments* com revisor
   obrigatório. Crie o `production`, mova para ele os secrets `VPS_SSH_KEY`, `VPS_HOST` e
   `VPS_HOST_KEY`, e ponha `environment: production` nos jobs do deploy e do backup. Junto, uma
   regra de proteção da `main` (PR obrigatório, sem push direto): hoje, um `workflow_dispatch`
   disparado num branch roda o workflow **daquele** branch, e a trava "só da `main`" vale contra
   engano, não contra quem tem escrita.
7. **Backups e logs do Actions ficam públicos.** Num repositório público, qualquer pessoa baixa os
   artifacts e lê os logs. Os artifacts são 30 dias de dumps **cifrados** com o `age` (ilegíveis
   sem a chave privada, mas passam a depender só dela; um vazamento da chave, ou uma falha futura
   do algoritmo, abriria o histórico inteiro). Os logs do `backup` mostram a versão do schema, o
   número de usuários e de alvos, o tamanho do dump e a ocupação do disco; os do deploy, os
   digests em produção e o IP da VPS se ele aparecer num passo. **Antes de mudar a
   visibilidade**, mova os backups para um lugar privado (um repositório privado separado só para
   o workflow de backup, ou um bucket com o upload no lugar do `upload-artifact`) e apague os
   artifacts antigos, **ou** aceite esse risco explicitamente, por escrito.
8. **Agendamentos:** num repositório público, o GitHub **desliga** os workflows agendados depois
   de 60 dias sem atividade no repositório, e avisa por e-mail. O `backup` para junto: reative-o
   em Actions quando o aviso chegar.
`````

Aplique em `README.md` e `docs/superpowers/plans/2026-09-22-roadmap.md`:

`````diff
diff --git a/README.md b/README.md
index 8f94ba0..5ae5748 100644
--- a/README.md
+++ b/README.md
@@ -43,9 +43,9 @@ no primeiro `up`: `DEMO_TARGETS_EXTRA="https://seu.site/ https://outro.site/" do
   `seed` recusa rodar (sai com 1, e o `up` para) se a `PUBLIC_BASE_URL` não for `http` em
   `localhost` ou IP de loopback. **Isso não protege um servidor**: atrás de um proxy (um Caddy com
   `reverse_proxy app:8080`) e com a `PUBLIC_BASE_URL` ainda local, o seed e o boot aceitam, o
-  usuário de demonstração nasce, e qualquer um entra com a senha pública. Nenhuma checagem do
-  `Host` recebido existe hoje. Para hospedar, o compose de produção vem no Plano 11, sem o
-  `seed` e sem nenhuma chave ou senha do repositório.
+  usuário de demonstração nasce, e qualquer um entra com a senha pública. O app não confere o
+  `Host` recebido. Para hospedar, use o compose de produção de [`deploy/vps/`](deploy/vps/), sem
+  o `seed` e sem nenhuma chave ou senha do repositório, pelo [runbook](docs/deploy/vps.md).
 
 `docker compose down` para; `docker compose down -v` apaga o banco também.
 
@@ -96,10 +96,12 @@ Kafka, porque o volume de um monitor destes cabe com folga no banco que já exis
 | Segredos com AES-256-GCM e o id da linha como AAD (um texto cifrado não muda de linha); tipo `Secret` que não sai em log nem em JSON | [`box.go`](internal/crypto/box.go), [`secret.go`](internal/crypto/secret.go) | [`box_test.go`](internal/crypto/box_test.go) (`TestOpenRejectsCiphertextMovedToAnotherRow`), [`secret_test.go`](internal/crypto/secret_test.go) |
 | Chaves de demonstração do compose aceitas no boot só com `PUBLIC_BASE_URL` `http` em `localhost` ou IP de loopback; `https` recusado sempre, em qualquer host; URL malformada ou com usuário e senha recusada | [`config.go`](internal/config/config.go) | [`config_test.go`](internal/config/config_test.go) (`TestLoadRejectsDemoKeysOverHTTPS`, `TestLoadRejectsDemoKeysOnANonLoopbackHost`, `TestLoadAcceptsDemoKeysOnLoopback`, `TestLoadRejectsMalformedPublicBaseURL`), [`compose_test.go`](internal/config/compose_test.go) (`TestComposeUsesTheDemoKeys`) |
 | Status page com DTO próprio: nada interno chega à página pública | [`public.go`](internal/statuspage/public.go) | [`public_test.go`](internal/statuspage/public_test.go) (`TestPublicViewHasNoInternalFields`) |
-| IP real do cliente atrás do proxy, com falha **fechada**: o cabeçalho (`Fly-Client-IP` no Fly) só vale vindo de `TRUSTED_PROXIES`, e um prefixo `/0` ali é recusado no boot. `CLIENT_IP_HEADER` só serve atrás de proxy que **substitui** esse cabeçalho (o do Fly substitui); atrás do Caddy, que repassa o que o cliente mandou, deixe-o **vazio** e vale o último salto do `X-Forwarded-For`, com todas as linhas lidas juntas. Com `PUBLIC_BASE_URL` https, um `X-Forwarded-For` de fora de `TRUSTED_PROXIES` vira aviso no log (um por minuto, com o `remote_addr`) | [`clientip.go`](internal/httpapi/middleware/clientip.go) | [`clientip_test.go`](internal/httpapi/middleware/clientip_test.go) (`TestRateLimitSeparatesClientsBehindFly`, `TestClientIPJoinsEveryForwardedForLine`), `TestSessionsBehindFlyRecordDistinctOrigins`, `TestForwardedForFromOutsideTheListWarnsInProduction`, [`config_test.go`](internal/config/config_test.go) (`TestLoadRejectsAZeroBitTrustedProxy`) |
+| IP real do cliente atrás do proxy, com falha **fechada**: o `X-Forwarded-For` (ou o cabeçalho de `CLIENT_IP_HEADER`) só vale vindo de `TRUSTED_PROXIES`, e um prefixo `/0` ali é recusado no boot. `CLIENT_IP_HEADER` só serve atrás de proxy que **substitui** esse cabeçalho; atrás do Caddy da produção ele fica **vazio**, o `TRUSTED_PROXIES` é o IP fixo do Caddy na rede do compose, e vale o último salto do `X-Forwarded-For`, com todas as linhas lidas juntas. O Caddy remove `X-Real-IP`, `Fly-Client-IP` e `CF-Connecting-IP` vindos do cliente. Com `PUBLIC_BASE_URL` https, um `X-Forwarded-For` de fora de `TRUSTED_PROXIES` vira aviso no log (um por minuto, com o `remote_addr`) | [`clientip.go`](internal/httpapi/middleware/clientip.go) | [`clientip_test.go`](internal/httpapi/middleware/clientip_test.go) (`TestRateLimitSeparatesClientsBehindFly`, `TestClientIPJoinsEveryForwardedForLine`), `TestSessionsBehindFlyRecordDistinctOrigins`, `TestForwardedForFromOutsideTheListWarnsInProduction`, [`config_test.go`](internal/config/config_test.go) (`TestLoadRejectsAZeroBitTrustedProxy`) |
 | Argon2id com teto de concorrência: toda verificação e todo hash (inclusive o do e-mail inexistente) passam por um semáforo de `min(NumCPU, 4)` vagas (`ARGON2_MAX_CONCURRENT`, 1–64); sem vaga em 2 s, `429 rate_limited` com `Retry-After: 2`, em vez de o processo morrer sem memória | [`argon2.go`](internal/auth/argon2.go) | [`argon2_test.go`](internal/auth/argon2_test.go) (`TestEveryArgon2PathWaitsForASlot`), [`argon2_integration_test.go`](internal/httpapi/argon2_integration_test.go) (`TestSaturatedArgon2AnswersRateLimited`) |
 | Bloqueio de conta atômico (a tentativa é reservada **antes** do Argon2 ou do TOTP: N requisições paralelas gastam N tentativas) e sem negação de serviço ao dono: um login completo deixa o cookie de dispositivo `vigilume_dispositivo`, e quem o apresenta não é barrado pelo bloqueio que um anônimo provocou; nas rotas autenticadas, a 5ª prova errada revoga a família da sessão | [`user.go`](internal/auth/user.go), [`dispositivo.go`](internal/auth/dispositivo.go), [`provas.go`](internal/auth/provas.go), [`provas.go` (httpapi)](internal/httpapi/provas.go) | [`user_test.go`](internal/auth/user_test.go) (`TestConcurrentReservationsStopAtTheThreshold`), [`dispositivo_integration_test.go`](internal/httpapi/dispositivo_integration_test.go) (`TestParallelLoginGuessesStopAtTheLock`, `TestDeviceCookieLetsTheOwnerInPastTheAccountLock`), [`dispositivo_revogacao_integration_test.go`](internal/httpapi/dispositivo_revogacao_integration_test.go) (`TestDeviceCookieDiesWithTheCredentials`), [`provas_familia_integration_test.go`](internal/httpapi/provas_familia_integration_test.go) (`TestFifthWrongProofDropsTheFamily`) |
-| O `seed` do compose recusa `PUBLIC_BASE_URL` fora de `http` em loopback, antes de chamar o binário. Isso **não** protege um servidor: atrás de um proxy e com a URL ainda local, ele roda e o usuário de demonstração nasce. O `compose.yaml` é só para a máquina local; para hospedar, o compose de produção do Plano 11, sem o `seed` | [`seed.sh`](deploy/compose/seed.sh) | [`seed_test.go`](internal/config/seed_test.go) (`TestSeedRefusesOutsideLoopback`), [`compose_test.go`](internal/config/compose_test.go) (`TestComposeHeaderSaysItIsLocalOnly`) |
+| O `seed` do compose recusa `PUBLIC_BASE_URL` fora de `http` em loopback, antes de chamar o binário. Isso **não** protege um servidor: atrás de um proxy e com a URL ainda local, ele roda e o usuário de demonstração nasce. O `compose.yaml` é só para a máquina local; para hospedar, o compose de produção de `deploy/vps/`, sem o `seed` | [`seed.sh`](deploy/compose/seed.sh) | [`seed_test.go`](internal/config/seed_test.go) (`TestSeedRefusesOutsideLoopback`), [`compose_test.go`](internal/config/compose_test.go) (`TestComposeHeaderSaysItIsLocalOnly`) |
+| Produção: só o Caddy publica porta (80 e 443); uma requisição com outro `Host` não chega ao app; `X-Real-IP`, `Fly-Client-IP` e `CF-Connecting-IP` mandados pelo cliente são removidos; o banco não aceita conexão de fora da rede do compose; rede sem IPv6 | [`compose.prod.yaml`](deploy/vps/compose.prod.yaml), [`Caddyfile`](deploy/vps/Caddyfile) | ⟦VPS: o script ou teste do CI que sobe o compose de produção e confere isso, com link⟧ |
+| Deploy por uma chave SSH que só roda um script: verbo desconhecido, SHA malformado e imagem divergente recusados; rollback automático se a versão nova não ficar saudável; o backup só devolve texto cifrado | [`vigilume-entrada`](deploy/vps/vigilume-entrada) | ⟦VPS: os testes do vigilume-entrada, com link⟧ |
 | CSP com nonce por resposta, no painel e na status page | [`webui.go`](internal/webui/webui.go), [`publicpage.go`](internal/httpapi/publicpage.go) | [`publicpage_integration_test.go`](internal/httpapi/publicpage_integration_test.go) (`TestPublicPageRendersWithoutLeaking`); E2E: [`fluxo.spec.ts`](web/e2e/fluxo.spec.ts) (zero violações, com controle positivo) |
 | Imagem distroless **nonroot, sem shell**, Trivy sem achado HIGH/CRITICAL corrigível, varredura de segredos (gitleaks) no histórico inteiro em todo PR | [`Dockerfile`](Dockerfile), [`ci.yml`](.github/workflows/ci.yml), [`.gitleaks.toml`](.gitleaks.toml) | [`checar-imagem.sh`](deploy/ci/checar-imagem.sh) |
 | Versão nova de dependência Go ou npm espera 7 dias antes de entrar no PR do Dependabot (as atualizações de segurança não esperam); a chave privada do `age` e os dumps do backup não entram no git | [`dependabot.yml`](.github/dependabot.yml), [`.gitignore`](.gitignore) | [`repositorio_test.go`](internal/config/repositorio_test.go) (`TestDependabotWaitsSevenDaysForGoAndNpm`, `TestGitignoreCoversTheBackupKeyAndDumps`) |
@@ -148,7 +150,7 @@ make e2e           # APAGA o banco do compose, sobe com --build, confere e roda
 | Banco | PostgreSQL 18 (compatível com o 17) |
 | Painel | React 19, TypeScript, Vite, TanStack Query, React Router, React Hook Form + Zod, Radix, Tailwind, Recharts |
 | Testes | `testing` + testcontainers, Vitest + Testing Library + MSW, Playwright + axe |
-| Entrega | Docker (distroless), GitHub Actions, GHCR, Fly.io |
+| Entrega | Docker (distroless), GitHub Actions, GHCR, uma VPS (Hostinger) com Caddy |
 
 ## Desenvolvimento
 
@@ -204,27 +206,34 @@ O que mudou no Plano 10 (spec do Plano 10, §4):
 
 ## Deploy e operação
 
-A hospedagem escolhida é uma VPS na Hostinger, com Caddy na frente: é o Plano 11, que traz o
-compose de produção e o runbook próprio. Até lá, o que existe é o alvo do Fly.io, descrito
-abaixo. Dois apps no Fly.io: o binário (`--role=all`) e um Postgres 18 com a imagem oficial, sem gerência
-do Fly, só na rede privada. **Nada disso foi criado ainda**: os artefatos e o runbook existem, e o
-primeiro deploy é decisão do dono do projeto.
+Uma VPS na Hostinger (Ubuntu 24.04), com três contêineres num compose de produção
+([`deploy/vps/compose.prod.yaml`](deploy/vps/compose.prod.yaml)): o **Caddy**, com HTTPS
+automático e o único que publica porta (80 e 443); o **binário** (`--role=all`), sem porta
+publicada; e o **Postgres 18** com a imagem oficial fixada por digest, só na rede interna. Os
+segredos ficam num `.env` do root na própria VPS, gerados lá, com cópia no gerenciador de senhas do
+dono. A VPS vem endurecida pelo runbook: SSH só por chave e sem root, `fail2ban`, firewall no
+sistema e no painel da Hostinger, atualizações de segurança automáticas.
 
 - **Imagem:** ~7,6 MB comprimida. A de produção sai do mesmo código e das mesmas bases fixadas
   por digest que o E2E testou, mas é construída de novo no `publish` (amd64 e arm64) e
   **reescaneada** pelo Trivy antes de ganhar as tags `:<sha do commit>` e `:latest`.
-- **Deploy:** sempre manual (Actions → deploy → Run workflow), só da `main` e só de um commit
-  que está nela, com o **SHA do commit** como entrada. O job resolve a tag `:<sha>`, que só
-  existe depois do reescaneamento, no digest dela, copia a imagem por esse digest para o
-  registry do Fly e faz o deploy. No GitHub Free com repositório privado não há revisor
-  obrigatório: quem pode disparar é quem tem acesso de escrita ao repositório (hoje, só o dono),
-  e os tokens do Fly são secrets de repositório.
-- **Backup:** dump lógico diário, cifrado com `age`, restaurado num banco descartável e
-  conferido (dono das tabelas, RLS com `FORCE`, contagens, partições). Uma checagem que falha
-  sai com código 3. Disco acima de 85% sai com código 4: o dump já conferido é cifrado e guardado
-  mesmo assim, e o job fica vermelho no fim.
-
-Runbook: [`docs/deploy/fly.md`](docs/deploy/fly.md).
+- **Deploy:** sempre manual (Actions → deploy → Run workflow), só da `main`, com o **SHA do
+  commit** como entrada. O job resolve a tag `:<sha>` (que só existe depois do reescaneamento) no
+  digest, baixa a imagem e a entrega por SSH a um usuário `deploy` cuja chave só roda
+  [`vigilume-entrada`](deploy/vps/vigilume-entrada): ele confere o ID da imagem, troca a versão,
+  espera o healthcheck e **volta sozinho à anterior** se a nova não ficar saudável. A chave do
+  servidor é fixada num secret (nada de `StrictHostKeyChecking=no`).
+- **Backup:** dump lógico diário, feito na VPS e restaurado num banco descartável e conferido
+  (dono das tabelas, RLS com `FORCE`, contagens, partições) antes de sair **já cifrado** com
+  `age` (a chave privada fica só com o dono) para um artifact de 30 dias. Uma checagem que falha
+  sai com código 3; disco acima de 85% sai com 4 (o dump é guardado mesmo assim, e o job fica
+  vermelho). Por cima, o backup semanal da Hostinger.
+- **Vigia externo:** um timer na VPS consulta o `/healthz` público a cada minuto e pinga o
+  healthchecks.io, que avisa o dono por e-mail se o site cair ou a VPS sumir: o Vigilume não
+  consegue avisar da própria queda.
+
+Runbook, do "VPS recém-criada" ao "no ar com 2FA, e-mail, backup e vigia", e a operação:
+[`docs/deploy/vps.md`](docs/deploy/vps.md).
 
 ## O que ficou de fora, e por quê
 
@@ -237,7 +246,8 @@ Runbook: [`docs/deploy/fly.md`](docs/deploy/fly.md).
 - **Domínio próprio na status page**: verificação de DNS é escopo próprio.
 - **Réplica, failover e PITR do Postgres**: um nó com snapshot diário e dump diário basta para
   este volume; no pior caso, perde-se um dia de checks.
-- **Métricas raspadas em produção**: exigiriam abrir o endpoint na rede privada do Fly.
+- **Métricas raspadas em produção**: exigiriam um coletor na VPS; em produção,
+  `METRICS_ADDR=off`.
 - **Cotas por usuário no sondador e no despacho**: só o operador cria contas (pelo CLI), então
   quem as esgotaria é uma conta que o próprio dono criou. Volta à mesa se o cadastro abrir.
 
@@ -247,4 +257,4 @@ e nos [planos](docs/superpowers/plans/).
 ## Licença
 
 Ainda não definida (repositório privado). A escolha é do dono do projeto e vem junto com a
-publicação ([runbook](docs/deploy/fly.md), "Tornar o repositório público").
+publicação ([runbook](docs/deploy/vps.md), "Tornar o repositório público").
diff --git a/docs/superpowers/plans/2026-09-22-roadmap.md b/docs/superpowers/plans/2026-09-22-roadmap.md
index 6ee4b6d..1e8b48b 100644
--- a/docs/superpowers/plans/2026-09-22-roadmap.md
+++ b/docs/superpowers/plans/2026-09-22-roadmap.md
@@ -20,8 +20,8 @@ Cada plano entrega software que roda e é testável por si só.
 | 8 | Endurecimento | `2026-09-25-endurecimento.md` | Pendências de segurança e correção (grupo A) | 15 | concluído |
 | 9 | Operação | `2026-09-25-operacao.md` | reset-2fa, troca de autenticador, pausa de canal, M7 | 16 | concluído |
 | 10 | Correções da revisão | `2026-09-25-revisao.md` | Achados Altos e Médios da revisão do projeto inteiro, e os Baixos baratos ou de segurança | 17 | concluído |
-| 11 | Hospedagem | a escrever | VPS na Hostinger: compose de produção com Caddy, runbook, backup | — | desenho com as seções 1–3 aprovadas |
-| 12 | Pendências | a escrever | Pendências do Plano 9 e dívidas do grupo C (`PENDENCIAS-PARA-O-PLANO-11.md`, "Para o Plano 12") | — | a escrever |
+| 11 | Hospedagem | ⟦CONTROLADOR: `2026-09-25-<nome>.md`, o arquivo deste plano⟧ | VPS na Hostinger: compose de produção com Caddy, deploy e backup por SSH com chave restrita, vigia externo, runbook para iniciante; Fly removido | 18 | concluído (a execução na VPS é guiada, com o dono, depois do merge) |
+| 12 | Pendências | a escrever | Pendências dos Planos 9 a 11 e dívidas do grupo C (`PENDENCIAS-PARA-O-PLANO-12.md`) | — | a escrever |
 | 13 | R2 | a escrever | `auth.PGStore` e `auth.TOTP` para sqlc | — | a escrever |
 
 Em 2026-09-25, antes de hospedar, o usuário pediu uma revisão do projeto inteiro buscando erros e
`````

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`:

`````markdown
# Pendências depois do Plano 11

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-11.md` como estado corrente; os anteriores ficam como histórico.

O Plano 11 pôs o Vigilume numa VPS da Hostinger (spec
`docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`): compose de produção com Caddy
em `deploy/vps/`, deploy e backup do GitHub Actions por SSH com chave restrita, vigia externo, e o
runbook `docs/deploy/vps.md`, escrito para um dono que nunca usou VPS. O Fly saiu do repositório
inteiro; o que era genérico no runbook dele (reset-2fa, operação do Postgres, restauração, tornar o
repositório público) migrou para o runbook novo. **A execução na VPS de verdade é feita com o dono,
guiada, depois do merge** (spec §1, critério 5): até lá, nada disto está no ar.

A ordem dos planos segue a de 2026-09-25: **12 = pendências** (as do Plano 9, as dívidas do grupo
C e o que os Planos 10 e 11 empurraram para cá), **13 = R2** (`auth` para sqlc).

## Fechadas no Plano 11: as 7 obrigações do Plano 10

Todas eram critério de pronto (`PENDENCIAS-PARA-O-PLANO-11.md`, "Obrigações do Plano 11"). A
coluna "Verificação" é o passo do runbook que a confere na VPS de verdade; a coluna "Prova no
repositório" é o que o CI roda.

| # | Obrigação | Como foi atendida | Prova no repositório | Verificação na VPS |
|---|---|---|---|---|
| 1 | O Caddy remove `Fly-Client-IP`, `X-Real-IP` e `CF-Connecting-IP` do cliente, e **não** configura `trusted_proxies` | `deploy/vps/Caddyfile`: um bloco de site só para o domínio, com a remoção dos três cabeçalhos na entrada, sem `trusted_proxies` (o Caddy substitui o `X-Forwarded-For` pelo IP real) | ⟦VPS: teste do CI que manda os cabeçalhos forjados e confere o IP visto⟧ | runbook 9.2 (o `429` continua com os cabeçalhos forjados) |
| 2 | `TRUSTED_PROXIES` = o IP fixo do Caddy, `CLIENT_IP_HEADER` vazio | Rede `vigilume` 172.30.0.0/24 fixada por `ipam` no `compose.prod.yaml`, Caddy em 172.30.0.2; o app com `TRUSTED_PROXIES=172.30.0.2/32` e `CLIENT_IP_HEADER` vazio | ⟦VPS: teste que lê o compose.prod.yaml e confere os dois valores⟧ | runbook 9.2 (sem o aviso da B3 no log; o teste de duas redes separa os baldes) |
| 3 | Só 80 e 443, do Caddy, publicadas; sem `ports:` no `db` e no `app`; sem IPv6 global na rede do compose | `compose.prod.yaml` com `ports` só no `caddy` e a rede sem IPv6; na VPS, `ufw` e o firewall do painel com 22, 80 e 443 | ⟦VPS: teste do compose de produção (portas publicadas e rede sem IPv6)⟧ | runbook 9.1 (`nmap` de fora, `ss -tlnp` de dentro) |
| 4 | `GOMEMLIMIT` no app e `mem_limit` em cada serviço | `compose.prod.yaml` | ⟦VPS: teste que confere `mem_limit` por serviço e `GOMEMLIMIT` abaixo do do app⟧ | runbook 9.5 |
| 5 | Compose de produção sem `seed`, sem `x-ambiente`, sem chave ou senha do repositório; segredos num `env_file` fora do git, gerados com `openssl rand`; `restart: unless-stopped` em tudo; o runbook diz que o `compose.yaml` não vai para o servidor e a `PUBLIC_BASE_URL` é `https://<domínio>` | `compose.prod.yaml` + `deploy/vps/env.exemplo` (modelo sem valores) + `deploy/vps/initdb.sh` (papéis a partir das senhas do ambiente); runbook "Como ler este runbook" (a frase literal) e 3.4 (segredos gerados na VPS, cópia no gerenciador) | ⟦VPS: teste que confere a ausência do seed e de chave do repositório no compose de produção⟧ | runbook 3.4 e 9.5 (`config --services` sem `seed`) e 9.3 (`Host` estranho não chega ao app) |
| 6 | Backup e deploy da VPS; a chave do `age` gerada fora do repositório | `deploy.yml` e `backup.yml` reescritos para `ssh deploy@VPS deploy …`/`backup`, com a chave presa a `deploy/vps/vigilume-entrada` por `restrict,command=` e `sudoers`; host key fixada em `VPS_HOST_KEY`; a chave do `age` nasce em `~/vigilume-backup.key`, fora do repositório (runbook 3.5) | ⟦VPS: testes do vigilume-entrada⟧; `deploy/ci/checar-workflows.sh` (job `workflows` do CI: nenhum `StrictHostKeyChecking=no`, nada do Fly) e `deploy/ci/vps.sh` (os códigos do deploy e do backup com mensagem) | runbook 4.4 (a chave só faz o que deve), 4.8 (primeiro deploy), 7.1 e 7.2 (backup e ensaio de restauração) |
| 7 | Um vigia externo do `/healthz` público | `deploy/vps/vigia.sh` + `vigilume-vigia.service` + `vigilume-vigia.timer`: a cada minuto, `curl` no `/healthz` público e ping no healthchecks.io (`HC_URL` no `.env`), `/fail` quando falha | ⟦VPS: teste do vigia.sh (ping de sucesso e /fail)⟧ | runbook 8 (derrubar o app de propósito e receber o e-mail) |

**Também fechado no Plano 11**, dos itens que o Plano 10 mandou para cá:

- **`age-keygen -o ~/vigilume-backup.key`, fora do repositório, no runbook** (banco S3 da revisão):
  runbook 3.5.
- **O Fly**: `docs/deploy/fly.md` apagado, com o genérico migrado para `docs/deploy/vps.md`
  (reset-2fa, operação do Postgres, restauração, "tornar o repositório público"); ⟦CONTROLADOR: o que o grupo vps removeu (`fly.toml`, `deploy/fly/`)⟧; os workflows não falam mais com o Fly
  (o `deploy/ci/checar-workflows.sh` do CI recusa qualquer menção fora de `docs/superpowers/`), e
  o Dependabot lê o digest do Postgres em `deploy/vps/db/Dockerfile`. Os secrets e variáveis de
  antes (`BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`, `PG_VOLUME_GB` e os tokens) saíram: o backup
  usa só `BACKUP_ENABLED`, e a chave pública do `age` mora em `/opt/vigilume/age-recipient`.
- **A checagem de `Host`**: o Caddy só tem o bloco do domínio, e uma requisição com outro `Host`
  não chega ao app (runbook 9.3). A checagem **no app** fica para o Plano 12 só se o cadastro abrir
  ou se aparecer outra porta de entrada.
- **Os itens do Fly em "Dependem do dono"** (apps, volume, tokens, `PG_VOLUME_GB`, medir o proxy
  do Fly): extintos com o Fly. O runbook manda apagar os secrets e a variável se existirem (4.6).

## Decisões do controlador para o dono revisar (spec do Plano 11, §9)

Tomadas com a autorização do dono ("tome as decisões por mim, exceto as cruciais"), marcadas
**[D]** na spec:

1. **Reinício automático às 04:30** (horário de Brasília) quando uma atualização de segurança
   pedir. Custo: ~1 min fora do ar de madrugada, e o vigia pode mandar um alarme falso curto (a
   carência de 5 min do healthchecks o absorve na maioria das vezes).
2. **`fail2ban` + SSH só por chave, porta 22.** Custo: perder a chave do computador = acesso só
   pelo terminal do painel da Hostinger (o runbook ensina a recuperação, bloco 10).
3. **healthchecks.io com timer na própria VPS**, em vez de um serviço que consulta de fora.
   Custo: se o DNS ou a rede entre a VPS e o mundo falhar de um jeito que o `curl` da própria VPS
   não veja, o alarme atrasa ou não vem.
4. **Scripts da VPS em shell** (o `vigilume-entrada`, o `vigia.sh`), testados no CI.
5. **Fly removido por inteiro** (decisão do dono), com o conteúdo genérico migrado.

E as do runbook (desta parte do Plano 11):

6. **O dono entra como um usuário administrador próprio com `sudo` e senha**, não como root; o
   `docker` só com `sudo` (o grupo `docker` equivale a root sem senha).
7. **Os secrets do GitHub vão pelo `gh secret set` lendo de arquivo**, sem colar valor em campo
   nenhum; a chave privada do deploy é apagada do computador depois (gera-se outra se precisar).
8. **O ensaio de restauração roda no computador do dono**, com a `MASTER_KEY` lida por
   `read -rs` (sem eco e sem histórico) e passada ao contêiner por `-e MASTER_KEY` sem valor.
9. **Sem registro `AAAA` nem `www`**: o Caddy atende um nome só, em IPv4.

## Limites conhecidos do Plano 11

- **A execução real não foi feita**: nenhum passo do runbook rodou numa VPS da Hostinger (spec §1,
  critério 5). Os comandos foram conferidos com `bash -n`, com o `--help` das ferramentas
  disponíveis e com a documentação pública (Hostinger, Resend, Docker, healthchecks.io); os rótulos
  do painel da Hostinger vêm da documentação dela e podem ter mudado.
- **O terminal do painel (Web console) depois do SSH endurecido**: a documentação da Hostinger não
  diz se ele depende do `sshd` nem com que usuário entra quando o root não pode entrar por SSH. O
  runbook manda testá-lo no passo 2.7, logo depois de endurecer, e aponta o **Emergency mode** se
  ele não abrir. Confirmar na execução guiada.
- **Segundos de `502` durante a troca de versão**, e o rollback automático não desfaz migration
  (as migrations são aditivas; spec §3).
- **O vigia mora na própria VPS** ([D] 3): uma falha de rede que o `curl` de dentro não vê atrasa
  o alarme.
- **O Docker não entra nas atualizações automáticas** (o `unattended-upgrades` só aplica as de
  segurança do Ubuntu): o runbook manda um `apt full-upgrade` mensal (bloco 10).
- **Os arquivos de `deploy/vps/` na VPS são copiados à mão**: o deploy troca só a imagem. Uma
  mudança no `compose.prod.yaml`, no `Caddyfile` ou nos scripts exige recopiar (runbook, bloco 10,
  "Arquivos da VPS").
- **Não há rotação pronta da `MASTER_KEY` nem das senhas do Postgres** (runbook, "Trocar um
  segredo").
- ⟦CONTROLADOR: os limites registrados pelos grupos vps e workflows⟧

## Fora de escopo do Plano 11, com destino

| Item | Origem | Destino |
|---|---|---|
| Aviso no boot quando `CLIENT_IP_HEADER` está preenchido | http S3 (parte), Plano 10 | Plano 12. O compose de produção o fixa vazio, e o aviso da B3 cobre o `TRUSTED_PROXIES` errado |
| Re-emitir o cookie de dispositivo nas rotas em que o dono logado muda a própria credencial | revisão final do Plano 10 | Plano 12 (o Plano 11 não mexeu em código do app) |
| Lista extra de bloqueio no safedial (`PROBE_DENY_CIDRS`) para o IP público da VPS | sondador S4 | Plano 12, só se o hairpin voltar a importar: a rede do compose não tem IPv6 e só o Caddy publica porta |
| `npm ci --ignore-scripts` no Dockerfile e no CI | banco S2 (segunda metade) | Plano 12 |
| Recusar o INSERT de check com `checked_at` muito à frente do `now()` do banco | banco C2 (segunda metade) | Plano 12 |
| Refresh repetido depois de resposta perdida vira "roubo" | auth C2 | Plano 12, com desenho próprio |
| `blocked` contra `dns` enumera os nomes internos da rede Docker | sondador S5 | Aceito; Plano 12 se o cadastro abrir |
| Cotas por usuário no sondador e no despacho | sondador S2 e S3 | Limite registrado; reavaliar se o cadastro abrir |

## Limpeza do code review do PR #15 (para o Plano 12)

Confirmados no code review do PR #15 e cortados do Plano 10 por não mudarem o comportamento. Conferido
contra os diffs dos grupos A e E na integração: nenhum item saiu inteiro; um mudou de forma (abaixo).

- CHECKs no banco para o par segredo pendente / família (`totp_pending_secret_enc` e
  `totp_pending_family_id` juntos), numa migration futura.
- `Disable` repete o SQL de `ResetTOTP` e `DeleteRecoveryCodes`.
- A reserva da prova (`provas.reservar`, `defer Release`, `Succeed`) repetida nos 6 handlers de
  credencial. O bloco "registra a falha e responde 401", que era copiado 5 vezes, virou uma chamada
  a `provasDaFamilia.errou` na A3.
- Trava de sessões + `FamilyIsLive` repetidos 4 vezes.
- `useEnvioUnico` local no painel, em vez de compartilhado.
- `GetChannelView` repete a query de `ListChannels`.
- `substituirCodigos` faz 11 idas ao banco sob a trava.
- `PaginaCanais` refaz o `GET` depois do `PATCH`, que já devolve o canal.
- Comentários de sabotagem desatualizados nos testes.
- A dica de porta de métricas ocupada nunca aparece no Windows (o erro não é `EADDRINUSE`).


## Minors da revisão do Plano 10 (para o Plano 12)

Achados menores das quatro revisões por grupo (`.superpowers/sdd/2026-09-25-revisao/revisao-*.md`)
que a onda de correção não fechou. Os que ela fechou: auth M1 (a espera do Argon2id respeita o
`ctx`), banco-docs M1 (marca d'água), m2 (`.gitignore`) e m3 (README do `reset-2fa`), painel I1
(prazo do rotate) e M2 (a troca no CI), http Q2 (a lista de divergências, com o Q1).

- auth M2: `FamilyProofs.Fail` decide pelo `n` da hora da reserva; se reservas anteriores em voo
  forem devolvidas (um `429` do semáforo), a 5ª reserva que errar derruba a família com menos de 5
  erros reais. Só com requisições simultâneas da mesma família; não é explorável por terceiros.
- auth M3: a família parada é esquecida em 24 h (`familiaParada`): um ladrão com a sessão (30 dias de
  refresh) tem 4 chutes de senha por dia, além dos de cada restart. O D3 só cita o restart.
- painel M1: na ativação, repetir o confirm depois de "não se sabe" termina no toast "já está
  ativada" (o `403`), sem repetir que os códigos desta ativação se perderam; a troca trata o `403`
  depois da resposta perdida, a ativação não.
- painel M4a: o teste "a barreira da CSP pega a violação que ainda não chegou" (`a11y.spec.ts`) fica
  verde sem a espera (sabotagem S2 declarada); quem prova a espera é o do coletor quebrado. O nome
  promete mais do que verifica.
- painel M4b: o PATCH de alvo da API falsa compara a origem pela regra WHATWG do painel, e não pelo
  `SameOrigin` do servidor: um teste de IPv4 não canônico ou de host que o `ToASCII` recusa passa
  contra a falsa e diverge do servidor. A falsa não serve de prova nesse ponto.
- painel M5: em Canais, o `aria-disabled` não segura o clique: entre o `recarregar()` do Retomar e o
  `finally`, o botão já diz "Pausar", o clique abre o Confirmar e o `finally` o fecha em seguida (uma
  piscada; nada é enviado).
- banco-docs m1: o `seed.sh` aceita URLs inválidas que o boot recusa (`http://localhost:evil.com`,
  `http://[::1]:8080.evil.com`, quebra de linha na `PUBLIC_BASE_URL`): o host real nunca é outro,
  mas o usuário demo já foi criado quando o boot falha. Endurecer: o resto depois do host vazio ou
  `:[0-9]+`, e recusar espaço, tab e quebra de linha.
- revisão final, conta: a troca de senha consome o código do 2FA ou o de recuperação
  (`s.totp.Verify`) **antes** do `auth.HashPassword` da senha nova (`internal/account/account.go`,
  `ChangePassword`); sem vaga no semáforo do Argon2id, ela responde `429` com o código já gasto. O
  painel (`erroDeCredencial` em `PaginaConfiguracoes.tsx`) só avisa do código de recuperação
  possivelmente gasto em `401` ou resposta perdida, não no `429`.
- revisão final, cookie de dispositivo: o carimbo não muda com o encerramento de sessões (nem com
  "encerrar todas") nem com a troca de autenticador; só com a troca de senha, o `set-password`, o
  `reset-2fa` e a ativação ou o disable do 2FA. Quem teve acesso completo (senha e 2FA) pode cunhar
  vários cookies antes de perdê-lo, e cada um tem o seu contador (5 chutes a cada 15 min) no
  `/totp/verify`, fora do bloqueio da conta. A remediação é trocar a senha, e o painel e o README não
  dizem isso. O mesmo carimbo derruba o cookie do próprio dono nessas operações (ver "Limites
  conhecidos"): re-emitir no Plano 12 (o Plano 11, de hospedagem, não mexeu em código do app).
- revisão final, `SameOrigin`: `internal/targets/validate.go` usa o `golang.org/x/net/idna` direto
  (versão do `go.mod`), e o `net/http` disca com a cópia embutida na biblioteca padrão (versão do
  Go). As tabelas Unicode das duas podem divergir num host novo, e a comparação deixa de ser "como
  o `net/http` disca" exatamente nesse host.
- revisão final, e2e: o `docker compose exec app /vigilume user reset-2fa` do `troca.spec`
  (`E2E_COMPOSE_SERVICE: app` no job `e2e` do `ci.yml`) só foi validado localmente; conferir no
  primeiro CI que a troca roda e não é pulada.


## Para o Plano 12 (limites do Plano 9, de `PENDENCIAS-PARA-O-PLANO-10.md`, ainda abertos)

Os limites conhecidos do Plano 9 que o Plano 10 não fechou:

- **Pausar descarta os avisos.** Retomar não reenvia o que passou; o diálogo de "Pausar" diz
  isso.
- **Um canal pausado só aparece na lista de canais**: o dashboard não avisa.
- **O código de recuperação usado para iniciar uma troca fica gasto**, mesmo que a troca não seja
  confirmada. O painel avisa ao fechar no passo 2 (e, desde o Plano 10, no 401 do passo 1).
- **O segredo pendente não tem prazo**, mas é da família que o criou e morre com ela (confirmação,
  nova troca, `disable`, `reset-2fa`, troca de senha, `set-password`).
- **`PATCH /channels/{id}` com campo desconhecido** responde `400 validation_failed` sem `fields`.
- **O `reset-2fa` não zera a marca do desafio** (`totp_challenge_mark`).
- **Porta 9091 ocupada por outro programa** em desenvolvimento: o `--role=prober` não sobe.
- **Uma notificação já reivindicada pode sair logo depois de uma pausa.**
- **No painel, o `/me` relido no passo 2 da troca** fecha o diálogo pelo portão, sem o aviso de
  "nada mudou".
- **Um 403 na confirmação da troca perde o aviso de código de recuperação gasto.**
- **O E2E de acessibilidade falhou uma vez sem causa encontrada.** Se repetir, guardar o trace.
- **`ROLE` inválido fica em `cfg.Role`** (`internal/config/config.go`), inofensivo porque o `Load`
  devolve `nil, err`.
- **Os testes de trava procuram a espera pelo `pg_stat_activity` sem amarrar o backend** (a lista
  completa dos testes está no `PENDENCIAS-PARA-O-PLANO-10.md`).

Os limites dos Planos 7 e 8 seguem como estão no `PENDENCIAS-PARA-O-PLANO-8.md` e no
`PENDENCIAS-PARA-O-PLANO-9.md`.

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" do
`PENDENCIAS-PARA-O-PLANO-5.md`, inteira.


## Limites conhecidos do Plano 10 (seguem valendo)

- **O contador por dispositivo e o contador por família vivem em memória** (A3): zeram num
  restart, e com `--role=api` em mais de um processo cada um conta o seu. Quem rouba o cookie de
  dispositivo (HttpOnly) ganha 5 tentativas próprias a cada 15 min, além das do IP.
- **Sob ataque ao Argon2id, o dono também pode receber `429` no login** por alguns segundos (A1).
- **Quem troca o domínio de um alvo redigita os headers** (B1); o painel avisa antes de salvar.
- **O `compose.yaml` copiado para um servidor atrás de um proxy, com a `PUBLIC_BASE_URL` ainda
  local, sobe com o usuário de demonstração** (revisão, banco-docs I1): o `seed.sh` (D1) e o boot
  só olham a URL declarada, e nenhum dos dois vê o `Host` que chega. O demo, com a senha pública,
  vira o dono. Em produção, resolvido pelo Plano 11 (obrigação 5, abaixo): o compose de produção não
  tem o seed nem chave do repositório, e o runbook diz que o `compose.yaml` não vai para o servidor.
  O limite continua valendo para quem copiar o `compose.yaml` à mão; a checagem de `Host` no app
  segue fora (tabela "Fora de escopo" do Plano 10).
- **O `seed.sh` só aceita `localhost`, `127.0.0.0/8` em IPv4 decimal e `[::1]`**: formas que o
  boot aceita e o seed não (como `[::ffff:127.0.0.1]`) são recusadas pelo seed, que é o lado
  seguro. O teste roda o script no `sh` do host (o `dash` no CI); o `busybox sh` da imagem
  `tools`, que é o que roda de verdade, só é exercitado pelo caminho local, no job `e2e`
  (`deploy/ci/checar-compose.sh`).
- **O rollup gravado no futuro fica no banco** (D4) até a retenção o apagar pela idade; a barra da
  status page o mostra na hora dele quando o tempo real chegar lá.
- **O cookie de dispositivo morre com as credenciais** (I1 da revisão): o MAC cobre o hash da senha
  e a data de ativação do 2FA, então a troca de senha, o `set-password`, o `reset-2fa`, a ativação
  e o disable do 2FA invalidam todos os cookies da conta, **os do dono inclusive**: ele volta ao
  bloqueio da conta até o próximo login completo (o mesmo custo do D7 com a `MASTER_KEY`); o
  rehash do Argon2id no login (parâmetros novos) também. A troca de autenticador **não** muda o
  carimbo: quem tinha a senha e a semente e guardou cookies continua com eles até o dono trocar
  a senha. Um cookie cunhado com o 2FA desligado volta a valer se o 2FA for ligado e resetado com
  a mesma senha (quem o tem entrou com essa senha, que sozinha já abre a conta). Para o Plano 12
  (era do Plano 11, que ficou só na hospedagem): re-emitir o cookie na resposta das rotas em que o
  dono logado muda a própria credencial.
- **O dono num navegador novo (ou com os cookies limpos) enquanto um anônimo mantém a conta
  travada** não tem cookie de dispositivo: espera os 15 min ou usa `vigilume user set-password`
  (A3, D6 do plano).
- **Painel velho em cache depois do deploy**: o `rotate/confirm` sem `rotation` recebe `403`, e o
  painel antigo diz "concluída ou cancelada em outra aba" até o dono recarregar (A7).
- **A 5ª prova errada numa rota autenticada desloga** sem explicação própria: o painel mostra "O
  acesso desta tela venceu." e, ao renovar, "Sua sessão terminou" (A3).
- **Sonda da URL antiga em voo durante o PATCH** entra na histerese zerada como o primeiro check da
  URL nova (C4). E o incidente aberto da URL antiga fecha pelos sucessos da nova, com a duração
  somada (H10 do plano).
- **O aviso do painel e o servidor divergem em hosts exóticos** (B1): IPv4 em hexadecimal, ponto
  final no host, host que o navegador aceita e o `idna.Lookup.ToASCII` recusa. O servidor apaga os
  headers (o lado seguro) e o painel pode não avisar. Desde a revisão (Q1), o servidor compara o
  host como o `net/http` o disca (`idna.Lookup.ToASCII`, o mesmo UTS 46 do `new URL().origin`):
  `straße.de` e `STRAẞE.de` são origens diferentes (discam `xn--strae-oqa.de` e `strasse.de`), e
  Unicode × punycode do mesmo host é a mesma origem (`TestSameOriginAgreesWithTheDialedHost`).
- **Alvo com `expected_status` 1xx ou 3xx gravado antes do Plano 10** (só pela API) continua no
  banco, e todo PATCH nele responde `400 expected_status/out_of_range` até o valor ser corrigido (C2),
  inclusive o `{"enabled": false}` do Pausar do painel, que não destaca campo nenhum: justamente o
  alvo que está `down` para sempre não pode ser pausado (revisão, http-sondador Q3).
- **HTTP/2 com cabeçalho de resposta enorme** (C1): o teto vale, mas os testes só exercitam HTTP/1.
- **O aviso de `TRUSTED_PROXIES` (B3) não distingue lista errada de porta do app exposta**: um
  cliente falando direto com a porta publicada gera o mesmo aviso.
- **Resposta perdida de um confirm que o servidor comitou**: o painel diz que não sabe e ensina a
  conferir; nenhum teste passa por um servidor que comita e perde a resposta (E2).
- **Dois canais mudando juntos e os dois falhando**: a tela mostra um erro só, o último (E4).
- **Logout com o banco fora por muito tempo**: cada "Sair" mostra o erro e a aba continua dentro
  (A9); não há "sair só desta aba". O mesmo vale para o `502`/`504` sem envelope do proxy com o
  binário fora (o caso mais provável atrás do Caddy do Plano 11): o painel diz "Resposta inesperada
  do servidor (502)" e a sessão local fica, enquanto sem rede ela é largada (P8; revisão, painel M3).


## Dependem do dono do projeto (nenhum agente faz)

- **Executar o runbook `docs/deploy/vps.md` na VPS, guiado, um passo por vez** (spec §1, critério
  5): criar as contas (Resend, healthchecks.io), apontar o domínio, gerar os segredos, gravar os
  secrets `VPS_HOST`, `VPS_SSH_KEY` e `VPS_HOST_KEY` e a variável `BACKUP_ENABLED`, o primeiro
  deploy, o 2FA e o ensaio de restauração.
- Revisar as decisões **[D]** acima (spec do Plano 11, §9) e as da spec do Plano 10 (§6): cookie
  de dispositivo e contador por família (A3), `429` do semáforo do Argon2 (A1), trocar a origem da
  URL apaga os headers (B1), `rotate`/`rotate/confirm` zeram o contador (A6), cotas por usuário
  fora.
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado (não o
  revisor obrigatório).
- Escolher a licença e tornar o repositório e o pacote do GHCR públicos, quando quiser (runbook,
  "Tornar o repositório público").

## Herdadas, ainda abertas

**R2** (Plano 13): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O `ConsumeChallenge` do Plano 8,
os métodos da troca de autenticador do Plano 9 (`internal/auth/totp_rotation.go`) e o SQL novo do
Plano 10 no `auth` (`ReserveAttempt` e `RefundAttempt` em `internal/auth/user.go`, o `fatorTravado`
e o consumo do timestep com CAS em `internal/auth/totp.go` e `internal/auth/totp_rotation.go`, e
`Tx.UserTOTPSecret` em `internal/auth/store.go`) também são SQL à mão.
`````

- [ ] **Step 3: Confira comandos, links, nomes, o Fly e os marcadores**

Salve o checador fora do repositório (ex.: `/tmp/conferir-docs.sh`) e rode-o na raiz:

```bash
#!/bin/bash
# Confere os documentos da parte runbook do Plano 11. Roda na raiz do repositório.
# Sai 1 se achar problema; imprime cada problema numa linha.
set -u
falhas=0
falha() { echo "FALHA: $*"; falhas=$((falhas + 1)); }
docs="docs/deploy/vps.md README.md docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md docs/superpowers/plans/2026-09-22-roadmap.md"

# 1. Cada bloco ```bash do runbook passa no bash -n.
tmp=$(mktemp -d)
[ -e docs/deploy/vps.md ] && awk -v d="$tmp" '
  /^[[:space:]]*```bash[[:space:]]*$/ { n++; f = sprintf("%s/bloco-%03d.sh", d, n); dentro = 1; inicio = NR; print "# linha " inicio > f; next }
  /^[[:space:]]*```[[:space:]]*$/ && dentro { dentro = 0; close(f); next }
  dentro { sub(/^   /, ""); print > f }
' docs/deploy/vps.md
blocos=0
for b in "$tmp"/bloco-*.sh; do
  [ -e "$b" ] || continue
  blocos=$((blocos + 1))
  if ! erro=$(bash -n "$b" 2>&1); then falha "bash -n em $(head -1 "$b"): $erro"; fi
done
rm -rf "$tmp"
echo "blocos de shell conferidos: $blocos"

# 2. Links relativos existem (em relação ao arquivo que os cita).
for doc in $docs; do
  [ -e "$doc" ] || { falha "documento $doc não existe"; continue; }
  dir=$(dirname "$doc")
  grep -o '](\([^)#]*\))' "$doc" | sed 's/](\(.*\))/\1/' | grep -v '^https\?:' | sort -u | while read -r f; do
    case "$f" in deploy/vps/*) continue ;; esac
    [ -e "$dir/$f" ] || echo "FALHA: link quebrado em $doc: $f"
  done
done | tee "$tmp.links"
[ -s "$tmp.links" ] && falhas=$((falhas + $(wc -l < "$tmp.links")))
rm -f "$tmp.links"

# 3. Arquivos do repositório citados em crase no runbook existem (os de deploy/vps/ vêm de outro grupo).
grep -hoE '(^|[^A-Za-z0-9_./-])(deploy|docs|internal|cmd|\.github)/[A-Za-z0-9_./-]*[A-Za-z0-9_/-]' docs/deploy/vps.md README.md \
  | sed -E 's#^[^A-Za-z0-9_.]##' | sort -u | while read -r f; do
  case "$f" in deploy/vps/*|deploy/ci/vps.sh|deploy/ci/checar-workflows.sh) continue ;; esac
  [ -e "$f" ] || echo "FALHA: arquivo citado não existe: $f"
done | tee "$tmp.arqs"
[ -s "$tmp.arqs" ] && falhas=$((falhas + $(wc -l < "$tmp.arqs")))
rm -f "$tmp.arqs"

# 4. Nada do Fly sobra: o runbook velho saiu, e os documentos vivos não mandam para ele.
[ -e docs/deploy/fly.md ] && falha "docs/deploy/fly.md ainda existe"
if git grep -n 'docs/deploy/fly\.md' -- . ':!docs/superpowers/plans' ':!docs/superpowers/specs' \
     ':!docs/superpowers/PENDENCIAS-PARA-O-PLANO-[0-9]*.md' ':!docs/superpowers/reports' \
     ':!docs/superpowers/revisao-2026-09-25' ':!fly.toml' ':!deploy/fly' ':!.github' ; then
  falha "referência viva a docs/deploy/fly.md (acima)"
fi
if grep -niE 'fly\.io|fly\.dev|flyctl|fly deploy|fly\.toml' README.md $(ls docs/deploy/vps.md 2>/dev/null); then
  falha "Fly no README ou no runbook (acima)"
fi

# 5. Marcadores do controlador.
m=$(grep -c '⟦' $docs 2>/dev/null | awk -F: '{s += $2} END {print s + 0}')
if [ "$m" -gt 0 ]; then
  falha "$m linha(s) com marcador ⟦…⟧ por resolver"
  grep -n '⟦' $docs | cut -c1-160
fi

echo "falhas: $falhas"
[ "$falhas" -eq 0 ]
```

Run: `bash /tmp/conferir-docs.sh`

Expected: `blocos de shell conferidos: 85` e `falhas: 0`. Cada `FALHA:` diz o que corrigir: um
bloco que não passa no `bash -n`, um link ou caminho citado que não existe (os de `deploy/vps/` e
os dois scripts de `deploy/ci/` são dos outros grupos e ficam de fora da conferência de
existência), o `fly.md` ainda presente, uma referência viva a ele, o Fly no README ou no runbook,
ou um `⟦` que o controlador não preencheu. **Antes das tarefas dos outros grupos e da montagem,
este Step fica vermelho só pelos marcadores** (30 linhas), e pela referência viva até a R-2.

Sabotagens (cada uma feita e desfeita a partir de uma cópia do arquivo):

| # | Sabotagem | Resultado esperado |
|---|---|---|
| S1 | No README, `(docs/deploy/vps.md)` → `(docs/deploy/vpx.md)` | `FALHA: link quebrado em README.md: docs/deploy/vpx.md` |
| S2 | No runbook, `sudo ufw default deny incoming` → `sudo ufw default "deny incoming` | `FALHA: bash -n em # linha …` |
| S3 | No runbook, `< deploy/backup/verificar.sql` → `< deploy/backup/verificar2.sql` | `FALHA: arquivo citado não existe: deploy/backup/verificar2.sql` (nasceu verde; ver "Validação") |
| S4 | No README, o Stack volta a dizer `Fly.io` | `FALHA: Fly no README ou no runbook` |
| S5 | No `compose.yaml` (depois da R-2), o comentário volta a citar `docs/deploy/fly.md` | `FALHA: referência viva a docs/deploy/fly.md` |
| S6 | Apagar `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md` | `FALHA: documento … não existe` |
| S7 | Recriar `docs/deploy/fly.md` (de `aebc4d0`) | `FALHA: docs/deploy/fly.md ainda existe` |

- [ ] **Step 4: Commit**

```bash
git add docs/deploy/vps.md README.md docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md docs/superpowers/plans/2026-09-22-roadmap.md
git commit -m "docs: runbook da VPS para quem nunca usou VPS (docs/deploy/vps.md), README sem o Fly, PENDENCIAS-PARA-O-PLANO-12 e roadmap; fly.md apagado"
```

---

### Task R-2: as referências ao runbook do Fly fora dos documentos

Spec §1, critério 3 ("nada do Fly sobra"). Quatro arquivos citavam `docs/deploy/fly.md`, que a R-1
apaga: três comentários e a perna oposta do teste do `.gitignore`.

**Files:**
- Modify: `.gitignore` (comentário do backup)
- Modify: `compose.yaml` (comentário das chaves de demonstração)
- Modify: `deploy/compose/initdb.sql` (comentário do cabeçalho)
- Modify: `internal/config/repositorio_test.go` (comentário e a perna oposta de `TestGitignoreCoversTheBackupKeyAndDumps`)

**Interfaces:**
- Consumes: `docs/deploy/vps.md` (R-1), `deploy/vps/compose.prod.yaml` e `deploy/vps/initdb.sh`
  (nomes fixos, citados em comentário).
- Produces: nada.

- [ ] **Step 1: Veja as referências falharem**

Run (depois da R-1): `bash /tmp/conferir-docs.sh`

Expected: FAIL com `FALHA: referência viva a docs/deploy/fly.md`, listando `.gitignore:19`,
`compose.yaml:36`, `deploy/compose/initdb.sql:3` e `internal/config/repositorio_test.go:45` e `:68`.

- [ ] **Step 2: Aplique**

`````diff
diff --git a/.gitignore b/.gitignore
index 00a5595..aa33899 100644
--- a/.gitignore
+++ b/.gitignore
@@ -16,7 +16,7 @@
 .env.*
 !.env.example
 !/web/.env.development
-# O backup (docs/deploy/fly.md, D3 do Plano 10): a chave privada do age
+# O backup (docs/deploy/vps.md, D3 do Plano 10): a chave privada do age
 # (vigilume-backup.key), o dump decifrado na restauração (vigilume.dump) e o
 # artifact baixado (vigilume-AAAAMMDD.dump.age). Nenhum deles vai para o git.
 *.key
diff --git a/compose.yaml b/compose.yaml
index cc1a0f2..4ee80c3 100644
--- a/compose.yaml
+++ b/compose.yaml
@@ -32,8 +32,8 @@ name: vigilume
 # !!! CHAVES DE DEMONSTRAÇÃO, PÚBLICAS NO REPOSITÓRIO. NÃO USE EM PRODUÇÃO. !!!
 # Com elas, qualquer um forja um JWT e decifra os segredos gravados. O boot
 # só as aceita com PUBLIC_BASE_URL http em localhost ou num IP de loopback
-# (D14); https, ou outro host, é recusado. Produção: o compose de produção do
-# Plano 11 (e, enquanto existir, docs/deploy/fly.md).
+# (D14); https, ou outro host, é recusado. Produção: deploy/vps/compose.prod.yaml,
+# pelo runbook docs/deploy/vps.md.
 x-ambiente: &ambiente
   DATABASE_URL: postgres://vigilume_app:vigilume_app@db:5432/vigilume?sslmode=disable
   # A mesma porta publicada abaixo: os links dos e-mails e da página de
diff --git a/deploy/compose/initdb.sql b/deploy/compose/initdb.sql
index aab6891..88aed88 100644
--- a/deploy/compose/initdb.sql
+++ b/deploy/compose/initdb.sql
@@ -1,7 +1,8 @@
--- Papel e banco da aplicação. É o MESMO SQL em três lugares: o init do
--- Postgres do compose, o banco descartável em que o backup é restaurado todo
--- dia, e o console do Postgres de produção (docs/deploy/fly.md, passo 4, com
--- senha nova).
+-- Papel e banco da aplicação. É o MESMO SQL em dois lugares: o init do
+-- Postgres do compose e o banco descartável em que o backup é restaurado (o do
+-- backup diário e o do ensaio de restauração, docs/deploy/vps.md, passo 7.2).
+-- A produção cria os mesmos papéis com deploy/vps/initdb.sh, com as senhas do
+-- .env da VPS.
 --
 -- O papel é comum de propósito: sem SUPERUSER e sem BYPASSRLS, que
 -- atravessariam a row-level security em silêncio. O boot recusa um papel
diff --git a/internal/config/repositorio_test.go b/internal/config/repositorio_test.go
index 40be18b..fc3e130 100644
--- a/internal/config/repositorio_test.go
+++ b/internal/config/repositorio_test.go
@@ -42,7 +42,7 @@ func TestDependabotWaitsSevenDaysForGoAndNpm(t *testing.T) {
 	}
 }
 
-// D3 (Plano 10, §3.4; banco S3 da revisão): o runbook (docs/deploy/fly.md)
+// D3 (Plano 10, §3.4; banco S3 da revisão): o runbook (docs/deploy/vps.md)
 // gera a chave privada do age e decifra o backup no diretório de trabalho.
 // Um `git add -A` na raiz a levaria para o histórico, e o gitleaks do CI só a
 // acha depois do push. Os nomes são os do runbook e do backup.yml; os
@@ -65,7 +65,7 @@ func TestGitignoreCoversTheBackupKeyAndDumps(t *testing.T) {
 	// exemplo de ambiente, o .env de desenvolvimento do Vite (sem segredo, por
 	// convenção dele) e chaves de fixture TLS em testdata/.
 	versionados := []string{
-		"compose.yaml", "deploy/compose/seed.sh", "docs/deploy/fly.md", "go.mod",
+		"compose.yaml", "deploy/compose/seed.sh", "docs/deploy/vps.md", "go.mod",
 		".env.example", "web/.env.example", "web/.env.development",
 		"internal/prober/safedial/testdata/server.key", "testdata/tls/ca.key",
 	}
`````

- [ ] **Step 3: Rode e veja passar**

Run:

```bash
bash /tmp/conferir-docs.sh
go test -count=1 ./internal/config/ && go vet ./internal/config/
docker compose -f compose.yaml config -q
```

Expected: nenhuma `FALHA` de referência viva (sobram só os marcadores, se a R-1 ainda os tiver);
`ok`; o `config -q` sem saída.

Sabotagens:

| # | Sabotagem | Resultado esperado |
|---|---|---|
| S8 | `.gitignore` com `docs/deploy/*.md` a mais | `TestGitignoreCoversTheBackupKeyAndDumps` vermelho: `docs/deploy/vps.md, que é do repositório, passou a ser ignorado` |
| S9 | O comentário do `compose.yaml` volta a `docs/deploy/fly.md` | `FALHA: referência viva a docs/deploy/fly.md` (é a S5) |

(Com `docs/deploy/fly.md` na lista, a S8 ficaria verde: o `check-ignore --no-index` aceita caminho
que não existe, e nada ignorava esse. A troca de caminho é o que dá à perna oposta um arquivo real.)

- [ ] **Step 4: Commit**

```bash
git add .gitignore compose.yaml deploy/compose/initdb.sql internal/config/repositorio_test.go
git commit -m "chore: comentários e o teste do .gitignore apontam para docs/deploy/vps.md, não para o runbook do Fly"
```
