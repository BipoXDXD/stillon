# Pendências depois do Plano 11

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui as pendências do
Plano 11 como estado corrente; os anteriores foram apagados em 2026-09-25 (README, Histórico).

O Plano 11 pôs o StillOn numa VPS da Hostinger (a spec do Plano 11, apagada em 2026-09-25; ver
README, Histórico): compose de produção com Caddy
em `deploy/vps/`, deploy e backup do GitHub Actions por SSH com chave restrita, vigia externo, e o
runbook `docs/deploy/vps.md`, escrito para um dono que nunca usou VPS. O Fly saiu do repositório
inteiro; o que era genérico no runbook dele (reset-2fa, operação do Postgres, restauração, tornar o
repositório público) migrou para o runbook novo. **A execução na VPS de verdade é feita com o dono,
guiada, depois do merge** (spec §1, critério 5): até lá, nada disto está no ar.

A ordem dos planos segue a de 2026-09-25: **12 = pendências** (as do Plano 9, as dívidas do grupo
C e o que os Planos 10 e 11 empurraram para cá), **13 = R2** (`auth` para sqlc).

## Atualização local de 2026-10-04: primeira onda do Plano 12

Implementação e provas na branch `melhoria/plano-12-seguranca-painel`, sem deploy nem
CI remoto nesta execução. O [relatório da primeira onda](reports/2026-10-04-primeira-onda.md)
concentra resultados, cobertura de leitura, referências e limites. O Plano 12 continua parcial.

Fechadas no código local: headers secretos em redirects (inclusive User-Agent e comparação
IDNA compartilhada), recovery preservado quando o segundo hash de senha satura, contagem de
erros reais e isolamento de gerações das provas de família, senhas fora do argv de initdb/backup,
URL inválida antes do seed, instalação npm sem hooks, checks futuros recusados no banco,
CHECK do par segredo/família TOTP pendentes, aviso de canal pausado no dashboard e guarda do
clique Pausar durante Retomar. Corrigida também a espera de SQL/pool além do WriteTimeout:
contexto de trabalho com 15 s e `503 unavailable` na API; a projeção pública preserva seu worker
independente de até 5 s. O painel recebeu proposta de redesign com prova de acessibilidade,
CSP e responsividade; a aceitação estética é do dono.

O transporte SSH real agora tem ensaio local/CI com OpenSSH, sudoers de produção e entrada
simulada: bytes, códigos, host key, comando forçado e recusa de PTY. Isso não substitui o
primeiro deploy guiado nem a conferência das restrições na VPS.

Os parágrafos históricos abaixo descrevem o estado ao fim dos planos anteriores. Para a
hospedagem, o [PR #26 ainda aberto](https://github.com/BipoXDXD/stillon/pull/26) registra os
blocos 0 a 2 concluídos em 28/09 e o bloco 3 como próximo; não houve nova conferência na VPS.
A decisão posterior do dono é operar por esta máquina Linux, dispensando o fluxo Mac.
Ele esclareceu que pretende apagar os dados que colocou na VPS Hostinger; o servidor
considerado continua na Hostinger, administrado por este Linux. Nenhuma ação destrutiva
foi executada. Depois da limpeza, reconferir os passos afetados; se houver reinstalação,
os registros anteriores ficam históricos e todos os passos aplicáveis exigem novos Confira.
Preservar chaves/segredos e, se houver dados a recuperar, backup e MASTER_KEY.
Os PRs de documentação #25/#26 continuam separados.

**Segunda onda, fechada no código local:** contagens e `pg_dump` compartilham snapshot
exportado. Supervisor Go com clientes libpq, limites e coleta dos filhos; binário distribuído
automaticamente para o PG18 descartável e executado como UID/GID 65532, sem capabilities.
DELETE/INSERT, interrupções, URI/ambiente, owners/RLS/partições e códigos 0/3/4 receberam
provas reais, sabotagens compiláveis e revisão independente. A trava comum de criação/retenção
também tem teste comportamental nos PG17/18. Build/vet/make test com race, lint/sec, imagens
e ensaio VPS local completos passaram. [Resultados e limites](reports/2026-10-04-segunda-onda.md).
Recursos 3 GiB/2 GiB e predicados de integridade foram preservados. Não houve deploy;
proveniência assinada da imagem e assinatura GPG do commit continuam pendentes.

**Terceira onda, fechada no código local:** bootstrap do tema normaliza preferência
inválida como sistema, de acordo com o provider. Testes executam o script público
real, montagem, persistência e recarga; 18 novos casos, 269 Vitest no total, com
sabotagens isoladas restauradas. Mutações HTTP exigem o literal `stillon` já publicado;
35 casos unitários e cinco HTTP reais conferem recusas e controle positivo exato.
Isso alinha o contrato, sem alegar bypass CSRF anterior. Gates e limites da rodada
estão no [relatório da terceira onda](reports/2026-10-04-terceira-onda.md).

## Fechadas no Plano 11: as 7 obrigações do Plano 10

Todas eram critério de pronto (as pendências do Plano 11, "Obrigações do Plano 11"; o arquivo foi
apagado em 2026-09-25: ver README, Histórico). A
coluna "Verificação" é o passo do runbook que a confere na VPS de verdade; a coluna "Prova no
repositório" é o que o CI roda.

| # | Obrigação | Como foi atendida | Prova no repositório | Verificação na VPS |
|---|---|---|---|---|
| 1 | O Caddy remove `Fly-Client-IP`, `X-Real-IP` e `CF-Connecting-IP` do cliente, e **não** configura `trusted_proxies` | `deploy/vps/Caddyfile`: um bloco de site só para o domínio, com a remoção dos três cabeçalhos na entrada, sem `trusted_proxies` (o Caddy substitui o `X-Forwarded-For` pelo IP real) | `deploy/ci/checar-vps.sh` (job `vps` do CI), bloco 5 (login com `X-Real-IP`, `Fly-Client-IP`, `CF-Connecting-IP` e `X-Forwarded-For` forjados: o `ip_hash` da sessão é o da conexão, não o forjado) e bloco 6 (com um `CLIENT_IP_HEADER` posto por engano, o app cai no IP do Caddy: o Caddy tirou o cabeçalho) | runbook 9.2 (o `429` continua com os cabeçalhos forjados) |
| 2 | `TRUSTED_PROXIES` = o IP fixo do Caddy, `CLIENT_IP_HEADER` vazio | Rede `stillon` 172.30.0.0/24 fixada por `ipam` no `compose.prod.yaml`, Caddy em 172.30.0.2; o app com `TRUSTED_PROXIES=172.30.0.2/32` e `CLIENT_IP_HEADER` vazio | `deploy/ci/checar-vps.sh`, bloco 1 (a config resolvida do `compose.prod.yaml`: Caddy em 172.30.0.2, `TRUSTED_PROXIES` = `172.30.0.2/32`, `CLIENT_IP_HEADER` vazio) e bloco 5 (o `ip_hash` do login não é o do Caddy, nem aparece o aviso da B3) | runbook 9.2 (sem o aviso da B3 no log; o teste de duas redes separa os baldes) |
| 3 | Só 80 e 443, do Caddy, publicadas; sem `ports:` no `db` e no `app`; sem IPv6 global na rede do compose | `compose.prod.yaml` com `ports` só no `caddy` e a rede sem IPv6; na VPS, `ufw` e o firewall do painel com 22, 80 e 443 | `deploy/ci/checar-vps.sh`, bloco 1 (só o `caddy` publica, exatamente `0.0.0.0:80` e `0.0.0.0:443` TCP; `enable_ipv6: false`; sub-rede 172.30.0.0/24), bloco 2b (na pilha de pé, só `caddy:80` e `caddy:443` publicadas, `app` e `db` sem `PortBindings`, rede sem IPv6) e bloco 7 (de outra rede do Docker o banco não responde) | runbook 9.1 (`nmap` de fora, `ss -tlnp` de dentro) |
| 4 | `GOMEMLIMIT` no app e `mem_limit` em cada serviço | `compose.prod.yaml` | `deploy/ci/checar-vps.sh`, bloco 1 (`mem_limit` e `restart: unless-stopped` em `caddy`, `app` e `db`; `GOMEMLIMIT` abaixo do `mem_limit` do app) | runbook 9.5 |
| 5 | Compose de produção sem `seed`, sem `x-ambiente`, sem chave ou senha do repositório; segredos num `env_file` fora do git, gerados com `openssl rand`; `restart: unless-stopped` em tudo; o runbook diz que o `compose.yaml` não vai para o servidor e a `PUBLIC_BASE_URL` é `https://<domínio>` | `compose.prod.yaml` + `deploy/vps/env.exemplo` (modelo sem valores do `/opt/stillon/.env`, que o compose lê pela interpolação, e não por `env_file`: cada serviço recebe só o que usa) + `deploy/vps/initdb.sh` (papéis a partir das senhas do ambiente); runbook "Como ler este runbook" (a frase literal) e 3.4 (segredos gerados na VPS, cópia no gerenciador) | `deploy/ci/checar-vps.sh`, bloco 1 (serviços exatamente `app`, `caddy` e `db`, sem `seed`; nenhuma chave de demonstração no arquivo; `PUBLIC_BASE_URL` = `https://<domínio>`; o `env.exemplo` sem valor e com toda variável que o compose lê; o Caddy sem segredo e o app sem a senha do superusuário nem a do backup) e bloco 4 (`Host` estranho não chega ao app) | runbook 3.4 e 9.5 (`config --services` sem `seed`) e 9.3 (`Host` estranho não chega ao app) |
| 6 | Backup e deploy da VPS; a chave do `age` gerada fora do repositório | `deploy.yml` e `backup.yml` reescritos para `ssh deploy@VPS deploy …`/`backup`, com a chave presa a `deploy/vps/stillon-entrada` por `restrict,command=` e `sudoers`; host key fixada em `VPS_HOST_KEY`; a chave do `age` nasce em `~/stillon-backup.key`, fora do repositório (runbook 3.5) | `deploy/ci/checar-vps.sh`, blocos 2 (primeiro deploy doente e deploy saudável), 8a–8f (pedidos recusados com 2, imagem divergente, tar com nome e o SHA no ar ou o anterior já etiquetado com outra imagem com 3, rollback com 4, as 3 últimas imagens, store que não é o do containerd com 1, `sudoers` num Ubuntu 24.04) e 9 (backup: só o cifrado na saída padrão, 1 sem chave e 3 com a cópia reprovada); `deploy/ci/checar-workflows.sh` (job `workflows` do CI: nenhum `StrictHostKeyChecking=no`, nada do Fly) e `deploy/ci/vps.sh` (os códigos do deploy e do backup com mensagem, testados com ssh falso por `deploy/ci/testar-vps.sh`) | runbook 4.4 (a chave só faz o que deve), 4.8 (primeiro deploy), 7.1 e 7.2 (backup e ensaio de restauração) |
| 7 | Um vigia externo do `/healthz` público | `deploy/vps/vigia.sh` + `stillon-vigia.service` + `stillon-vigia.timer`: a cada minuto, `curl` no `/healthz` público e ping no healthchecks.io (`HC_URL` no `/opt/stillon/vigia.env`, o arquivo só do vigia; o `.env` não chega a ele), `/fail` quando falha | `deploy/ci/checar-vigia.sh` (job `vps` do CI), blocos 1 e 2 (saudável pinga a `HC_URL`; fora do ar pinga `HC_URL/fail` e sai com erro), 3 a 6 (barra no fim, sem `HC_URL`, sem domínio, certificado conferido e prazos) e 7 (as unidades: timer a cada minuto, `DynamicUser`, `LoadCredential`, `ExecStart=/usr/local/sbin/stillon-vigia`) | runbook 8 (derrubar o app de propósito e receber o e-mail) |

**Também fechado no Plano 11**, dos itens que o Plano 10 mandou para cá:

- **`age-keygen -o ~/stillon-backup.key`, fora do repositório, no runbook** (banco S3 da revisão):
  runbook 3.5.
- **O Fly**: `docs/deploy/fly.md` apagado, com o genérico migrado para `docs/deploy/vps.md`
  (reset-2fa, operação do Postgres, restauração, "tornar o repositório público"); o `fly.toml` e o `deploy/fly/` (o `Dockerfile` e o `fly.toml` do Postgres) apagados, com o digest do Postgres de produção agora em `deploy/vps/db/Dockerfile`; os workflows não falam mais com o Fly
  (o `deploy/ci/checar-workflows.sh` do CI recusa qualquer menção fora de `docs/superpowers/`), e
  o Dependabot lê o digest do Postgres em `deploy/vps/db/Dockerfile`. Os secrets e variáveis de
  antes (`BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`, `PG_VOLUME_GB` e os tokens) saíram: o backup
  usa só `BACKUP_ENABLED`, e a chave pública do `age` mora em `/opt/stillon/age-recipient`.
- **A checagem de `Host`**: o Caddy só tem o bloco do domínio, e uma requisição com outro `Host`
  não chega ao app (runbook 9.3). A checagem **no app** fica para o Plano 12 só se o cadastro abrir
  ou se aparecer outra porta de entrada.
- **Os itens do Fly em "Dependem do dono"** (apps, volume, tokens, `PG_VOLUME_GB`, medir o proxy
  do Fly): extintos com o Fly. O runbook manda apagar os secrets e a variável se existirem (4.6).

## Decisões do controlador para o dono revisar (spec do Plano 11, §9)

Tomadas com a autorização do dono ("tome as decisões por mim, exceto as cruciais"), marcadas
**[D]** na spec:

1. **Reinício automático às 04:30** (horário de Brasília) quando uma atualização de segurança
   pedir. Custo: ~1 min fora do ar de madrugada, e o vigia pode mandar um alarme falso curto: a
   carência de 5 min do healthchecks só absorve o ping que **não chega** (a VPS desligada); se o
   vigia rodar com o app ainda subindo, o `/fail` chega e vem um par de e-mails **down**/**up**
   (runbook 1.6). Eliminar o par (o vigia só mandar `/fail` depois de duas falhas seguidas) é
   código, para o Plano 12 se o par incomodar.
2. **`fail2ban` + SSH só por chave, porta 22.** Custo: perder a chave do computador = acesso só
   pelo terminal do painel da Hostinger (o runbook ensina a recuperação, bloco 10).
3. **healthchecks.io com timer na própria VPS**, em vez de um serviço que consulta de fora.
   Custo: se o DNS ou a rede entre a VPS e o mundo falhar de um jeito que o `curl` da própria VPS
   não veja, o alarme atrasa ou não vem.
4. **Scripts da VPS em shell** (o `stillon-entrada`, o `vigia.sh`), testados no CI.
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
- **O passo do job `vps` que liga o image store do containerd no runner** (`daemon.json` +
  `systemctl restart docker`) nunca rodou fora do GitHub: a máquina da validação já usava o
  containerd. Um `daemon.json` inesperado no runner deixa o job vermelho no primeiro passo.
- **O ID da imagem entre o runner e a VPS** (`<image-id>` = digest do manifesto linux/amd64; tar em
  layout OCI do `crane pull --format oci`): a entrada foi testada com um layout OCI montado no
  próprio teste, não com a saída do `crane`, e a VPS exige o image store do containerd (com o
  clássico, todo deploy sai 1 com a mensagem). Se o `crane` puser
  `org.opencontainers.image.ref.name` no `index.json`, todo deploy sai 3 ("o index.json do tar não é uma
  imagem só, sem nome, com o manifesto …"). Conferir no primeiro deploy guiado: o log mostra `Loaded image ID: sha256:<IMAGE_ID do
  job>`.
- **SSH na VPS ainda depende de conferência guiada**. Ao fim do Plano 11, nem o `restrict,command=` com o `sudo` guardando o
  `SSH_ORIGINAL_COMMAND` (provado num contêiner com `su deploy -c …`), nem o tar binário passando
  por `ssh | sudo | script`, nem o `known_hosts` escrito a partir do `VPS_HOST_KEY` (as opções do
  `ssh` foram conferidas com `ssh -G` e o resto com um ssh falso). O primeiro deploy guiado é a
  prova naquele estado. A primeira onda do Plano 12 acrescentou o ensaio real em contêineres
  (abaixo); o hardening completo e o deploy da VPS continuam com o dono.
- **`docker compose up --wait` com o app em loop de reinício** sai em segundos no Compose medido
  (v5.5.1); um Compose mais velho na VPS pode esperar os 90 s inteiros.
- **O IP que o Caddy vê** só foi testado com o cliente no próprio host (o `docker-proxy` entrega o
  IP do gateway); para clientes de fora, em IPv4, o Docker preserva a origem, e o runbook confere
  no 9.2.
- **O backup** roda o `backup.sh` como o usuário de quem chama (root na VPS), num Postgres
  descartável com `--memory 3g` e os dados em `tmpfs` de 2 GB: um banco restaurado acima de ~2 GB
  faz o backup sair 1 (hoje o banco é de MB; revisar quando o `backup.sh` mostrar mais de 1 GB).
  Deploy e backup dividem uma trava (`flock -w 600`): um espera até 10 min atrás do outro.
- **O `shellcheck` do job `workflows`** é o da imagem do runner, chamado direto; se ela deixar de
  trazê-lo, o job fica vermelho (instalar por `apt-get` no passo). A perna 6 do
  `checar-workflows.sh` procura o Fly por uma lista de termos: um termo novo da plataforma, fora
  da lista, passaria.
- **Host key só `ssh-ed25519`** (`HostKeyAlgorithms=ssh-ed25519`): uma VPS sem essa chave é
  recusada com mensagem; trocar é uma linha. Um `255` que viesse da própria entrada (não vem: ela
  só usa 0–4) cairia na mensagem de "falha de SSH".

## Da revisão final do Plano 11 (para o Plano 12)

- **O caminho SSH de verdade no CI — fechado localmente em 2026-10-04** (M7 da revisão final do código).
  `deploy/ci/testar-ssh-real.sh` e `deploy/ci/ssh/` implementam o ensaio no job VPS existente;
  provas e limites no relatório da primeira onda. O desenho que originou a tarefa era: o `restrict,command=`
  + `/bin/sh` + `sudo` + a entrada padrão binária + o código de saída só se provam no primeiro
  deploy guiado (acima, "SSH de verdade nunca foi exercitado"). Proposta: no `checar-vps.sh`, um
  contêiner com `openssh-server`, o usuário `deploy` com o `authorized_keys` e o `sudoers` de
  `deploy/vps/`, e uma entrada falsa que ecoa o pedido e o `sha256` da entrada padrão e sai com
  um código escolhido; o teste confere, pelo `ssh` de verdade com o `known_hosts` montado como o
  `deploy/ci/vps.sh` monta, que o pedido, os bytes e o código chegam intactos. Sabotagem: tirar o
  `SSH_ORIGINAL_COMMAND` do `env_keep` do `sudoers` tem de derrubar o teste.
- **Risco residual da opção A: a VPS não confere assinatura da imagem** (revisão final do
  código, "Risco residual"; a opção A foi aprovada pelo dono). A chave de deploy não dá root no
  host (o pedido é filtrado, o tar só é extraído com os nomes da lista, o app roda com
  `cap_drop: ALL` e `no-new-privileges`, sem socket do Docker e sem porta publicada), mas dá o
  app **inteiro**: quem a tem sobe qualquer imagem como o app, com a `MASTER_KEY`, o
  `JWT_SECRET`, o `IP_HASH_KEY`, a senha do `stillon_app` e a `RESEND_API_KEY`. O `image-id` do
  pedido não protege contra isso, porque quem ataca o escolhe. A defesa de verdade: o `publish`
  assinar a imagem (cosign, ou o atestado de proveniência do GitHub), o deploy mandar o bundle
  pelo mesmo SSH, e a entrada recusar (3) a imagem cuja assinatura não confere com a identidade
  do workflow `publish` da `main`. Até lá, o runbook ("Trocar um segredo", "A chave de deploy
  vazou") trata a chave vazada como vazamento de todos os segredos do app, com o passo a passo.

## Fora de escopo do Plano 11, com destino

| Item | Origem | Destino |
|---|---|---|
| Aviso no boot quando `CLIENT_IP_HEADER` está preenchido | http S3 (parte), Plano 10 | Plano 12. O compose de produção o fixa vazio, e o aviso da B3 cobre o `TRUSTED_PROXIES` errado |
| Re-emitir o cookie de dispositivo nas rotas em que o dono logado muda a própria credencial | revisão final do Plano 10 | Plano 12 (o Plano 11 não mexeu em código do app) |
| Lista extra de bloqueio no safedial (`PROBE_DENY_CIDRS`) para o IP público da VPS | sondador S4 | Plano 12, só se o hairpin voltar a importar: a rede do compose não tem IPv6 e só o Caddy publica porta |
| `npm ci --ignore-scripts` no Dockerfile e no CI | banco S2 (segunda metade) | Fechado localmente na primeira onda do Plano 12; build Linux/Alpine e imagem reais |
| Recusar o INSERT de check com `checked_at` muito à frente do `now()` do banco | banco C2 (segunda metade) | Fechado localmente: migration 00010, trigger de INSERT/UPDATE com `statement_timestamp()`; não repara dados antigos nem o lease do agendador |
| Refresh repetido depois de resposta perdida vira "roubo" | auth C2 | Plano 12, com desenho próprio |
| `blocked` contra `dns` enumera os nomes internos da rede Docker | sondador S5 | Aceito; Plano 12 se o cadastro abrir |
| Cotas por usuário no sondador e no despacho | sondador S2 e S3 | Limite registrado; reavaliar se o cadastro abrir |

## Limpeza do code review do PR #15 (para o Plano 12)

Confirmados no code review do PR #15 e cortados do Plano 10 por não mudarem o comportamento. Conferido
contra os diffs dos grupos A e E na integração: nenhum item saiu inteiro; um mudou de forma (abaixo).

- **Fechado localmente na primeira onda:** CHECK no banco para o par segredo pendente / família
  (`totp_pending_secret_enc` e `totp_pending_family_id` juntos), migration 00011. Dados antigos
  inconsistentes recusam a migration e precisam de investigação; nenhuma credencial é reparada
  automaticamente.
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
  erros reais. **Fechado localmente na primeira onda:** Fail conta erros concluídos e reservas de
  geração antiga não devolvem vagas à nova. `Exhausted` na sexta reserva em voo continua com
  a política anterior; não foi redesenhado nesta onda.
- auth M3: a família parada é esquecida em 24 h (`familiaParada`): um ladrão com a sessão (30 dias de
  refresh) tem 4 chutes de senha por dia, além dos de cada restart. O D3 só cita o restart.
- painel M1: na ativação, repetir o confirm depois de "não se sabe" termina no toast "já está
  ativada" (o `403`), sem repetir que os códigos desta ativação se perderam; a troca trata o `403`
  depois da resposta perdida, a ativação não. **Fechado localmente em 2026-10-05:** o
  toast após o `403` mantém o aviso de ativação incerta e dos códigos perdidos. Um `403` sem
  resposta perdida continua dizendo "já está ativada"; um retry bem-sucedido mostra os códigos
  e limpa a incerteza. Sem mudança no protocolo de autenticação.
- painel M4a: o teste "a barreira da CSP pega a violação que ainda não chegou" (`a11y.spec.ts`) fica
  verde sem a espera (sabotagem S2 declarada); quem prova a espera é o do coletor quebrado. O nome
  promete mais do que verifica.
- painel M4b: o PATCH de alvo da API falsa compara a origem pela regra WHATWG do painel, e não pelo
  `SameOrigin` do servidor: um teste de IPv4 não canônico ou de host que o `ToASCII` recusa passa
  contra a falsa e diverge do servidor. A falsa não serve de prova nesse ponto.
- painel M5: em Canais, o `aria-disabled` não segura o clique: entre o `recarregar()` do Retomar e o
  `finally`, o botão já diz "Pausar", o clique abre o Confirmar e o `finally` o fecha em seguida (uma
  piscada; nada é enviado). **Fechado localmente:** guarda `emVoo` antes de abrir confirmação.
- banco-docs m1: o `seed.sh` aceita URLs inválidas que o boot recusa (`http://localhost:evil.com`,
  `http://[::1]:8080.evil.com`, quebra de linha na `PUBLIC_BASE_URL`): o host real nunca é outro,
  mas o usuário demo já foi criado quando o boot falha. Endurecer: o resto depois do host vazio ou
  `:[0-9]+`, e recusar espaço, tab e quebra de linha. **Fechado localmente na primeira onda:**
  parser POSIX valida URL inteira, loopback canônico e porta 1–65535 antes do binário.
- revisão final, conta: a troca de senha consome o código do 2FA ou o de recuperação
  (`s.totp.Verify`) **antes** do `auth.HashPassword` da senha nova (`internal/account/account.go`,
  `ChangePassword`); sem vaga no semáforo do Argon2id, ela responde `429` com o código já gasto. O
  painel (`erroDeCredencial` em `PaginaConfiguracoes.tsx`) só avisa do código de recuperação
  possivelmente gasto em `401` ou resposta perdida, não no `429`. **Fechado localmente:** o hash
  novo termina antes de verificar/consumir o fator; teste prova recovery utilizável após `429`.
  Isso não garante devolução do fator em todo erro posterior da transação.
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
- revisão final, e2e: o `docker compose exec app /stillon user reset-2fa` do `troca.spec`
  (`E2E_COMPOSE_SERVICE: app` no job `e2e` do `ci.yml`) só foi validado localmente; conferir no
  primeiro CI que a troca roda e não é pulada.
- revisão da renomeação, testes — **fechado localmente na segunda onda:** a trava advisory
  de `drop_checks_partitions_before` e `ensure_checks_partition` foi exercitada nos dois
  sentidos, com transação dona aberta e observação por PID. O teste não compara texto SQL;
  uma chave divergente na retenção recebeu RED compilável, restauração e GREEN no PG18,
  além de GREEN focado no PG17.
- revisão da renomeação, testes — **fechado localmente na terceira onda:** preferência
  persistida é exercitada no bootstrap e provider reais com sistema oposto e nova carga.
  Chave divergente em qualquer lado recebe RED comportamental; normalização inválida
  também. Não foi criada comparação por regex da constante.
- revisão da renomeação, testes — **fechado localmente na terceira onda:**
  `RequireCustomHeader` exige o valor literal documentado `stillon`. Valores ausente,
  vazio, outro e caixa diferente são recusados no middleware e login HTTP reais;
  controle correto chega à autenticação e recebe `401 invalid_credentials`. GET/HEAD/
  OPTIONS preservados. Literal é contrato público, não token secreto; callers externos
  que antes enviavam outro valor passam a receber 403.


## Para o Plano 12 (limites do Plano 9, das pendências do Plano 10, ainda abertos)

Os limites conhecidos do Plano 9 que o Plano 10 não fechou (as pendências do Plano 10, apagadas
em 2026-09-25; ver README, Histórico):

- **Pausar descarta os avisos.** Retomar não reenvia o que passou; o diálogo de "Pausar" diz
  isso.
- **Fechado localmente:** o dashboard avisa sobre canais pausados, ausência de canal ativo
  verificado e erros da consulta, sem inferir ausência quando a consulta falha.
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
  completa dos testes está nas pendências do Plano 10 (apagadas em 2026-09-25; ver README,
  Histórico)).

Os limites dos Planos 7 e 8 seguem como estão nas pendências dos Planos 8 e 9 (apagadas em
2026-09-25; ver README, Histórico).

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" das pendências
do Plano 5 (apagadas em 2026-09-25; ver README, Histórico), inteira.


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
  travada** não tem cookie de dispositivo: espera os 15 min ou usa `stillon user set-password`
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


## Renomeação para StillOn (2026-09-25)

Em 2026-09-25, depois do Plano 11, o projeto trocou de nome (README, Histórico). Fica para o dono:

- **Apagar o pacote antigo do GHCR**, órfão depois da renomeação (as imagens novas saem em
  `ghcr.io/bipoxdxd/stillon`).
- **Dar o nome `StillOn` ao check do healthchecks.io** ao criá-lo (runbook 1.6).
- **O remetente do Resend, `StillOn <alertas@stillon.tech>`**: a variável é a `EMAIL_FROM` do
  `.env` da VPS (runbook 1.5 e 3.4), com o domínio que estiver verificado no Resend (se for o
  subdomínio `avisos.` que o runbook sugere, `alertas@avisos.stillon.tech`).

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
