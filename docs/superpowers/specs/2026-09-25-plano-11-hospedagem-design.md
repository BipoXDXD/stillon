# Vigilume — Plano 11: produção numa VPS (Hostinger) (design)

**Status:** seções 1–3 aprovadas pelo usuário em 2026-09-25 (topologia, deploy, backup); seções 4–6
decididas pelo controlador com autorização do usuário ausente ("tome as decisões por mim, exceto as
cruciais"), marcadas **[D]** e listadas em §9 para revisão. Nada aqui é código nem plano.

**Base de código:** `main` em `a61fc0f` (Plano 10 integrado).

**Decisões do usuário (2026-09-25):** VPS **Hostinger KVM 2** já assinada (2 vCPU, 8 GB, 100 GB NVMe,
datacenter no Brasil), **Ubuntu 24.04 LTS**, **domínio grátis da Hostinger**, e-mail pelo **Resend com o
domínio verificado**, **Fly removido do repositório**, deploy pela **opção A** (o GitHub Actions entrega a
imagem por SSH com chave restrita). O usuário **nunca usou VPS**: o runbook é um passo a passo para
iniciante, e a configuração será guiada ao vivo, um passo por vez.

**Obrigações vindas do Plano 10:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`, seção "Obrigações do
Plano 11 (hospedagem)", itens 1–7. Todas são critério de pronto deste plano.

---

## 1. Objetivo

Colocar o Vigilume no ar, em `https://<domínio>`, numa VPS que o dono consiga operar sem experiência
prévia, **sem dado vazado e sem servidor que cai toda hora** (palavras do usuário).

**Critérios de sucesso:**

1. Tudo o que é arquivo (compose de produção, Caddyfile, scripts da VPS, workflows, runbook) está no
   repositório, testado no CI quando possível, e o runbook leva do "VPS recém-criada" ao "Vigilume no ar
   com 2FA, e-mail, backup e vigia externo" sem passo implícito.
2. As 7 obrigações do Plano 10 estão atendidas e cada uma tem uma verificação no runbook.
3. Nada do Fly sobra no repositório (o que era genérico migra para o runbook novo).
4. Suíte, lint, sec, painel, E2E e os checks novos de CI verdes; PR só com todos os jobs verdes.
5. A execução na VPS de verdade é feita **com o dono**, guiada, depois do merge — não por agente.

## 2. Topologia (seção 1, aprovada)

Diretório `/opt/vigilume` na VPS, com `compose.prod.yaml` versionado em `deploy/vps/`.

| Serviço | Imagem | Rede | Portas |
|---|---|---|---|
| `caddy` | `caddy:2` por digest | `vigilume` + publicada | **só** 80 e 443 |
| `app` | `vigilume:<sha>` carregada pelo deploy, `--role=all` | só `vigilume`, IP fixo | nenhuma publicada |
| `db` | `postgres:18-alpine`, o mesmo digest dos testes | só `vigilume` | nenhuma publicada |

- Rede `vigilume` com sub-rede fixa por `ipam` (ex.: `172.30.0.0/24`; Caddy `.2`, app `.3`), **sem IPv6**.
- **Caddy:** um bloco de site só para `<domínio>` (requisições com outro `Host` não chegam ao app —
  cobre a checagem de `Host` da revisão); HTTPS automático (Let's Encrypt); remove da requisição
  `Fly-Client-IP`, `X-Real-IP` e `CF-Connecting-IP`; **sem** `trusted_proxies` (ele substitui o
  `X-Forwarded-For` pelo IP real); cabeçalhos de segurança continuam sendo do app.
- **App:** `TRUSTED_PROXIES=<IP fixo do Caddy>/32`, `CLIENT_IP_HEADER` vazio, `PUBLIC_BASE_URL` e
  `CORS_ORIGIN` = `https://<domínio>`, `METRICS_ADDR=off`, `GOMEMLIMIT` abaixo do `mem_limit`.
- **Papéis do Postgres:** superusuário só no init; `vigilume_app` (NOSUPERUSER NOBYPASSRLS, dono do
  banco); `vigilume_backup` (`pg_read_all_data`, BYPASSRLS para o dump). Criados por um script de init
  que lê as senhas do ambiente (nada de senha no repositório).
- **Segredos** em `/opt/vigilume/.env` (dono root, `600`, fora do git), gerados na VPS com
  `openssl rand`; cópia no gerenciador de senhas do dono (perder a `MASTER_KEY` = perder os segredos
  cifrados).
- `restart: unless-stopped` e `mem_limit` em todos os serviços; rotação de log do Docker.

## 3. Deploy (seção 2, aprovada)

- Usuário `deploy` na VPS: sem shell interativo, sem `sudo` geral, fora do grupo `docker`.
- `authorized_keys` do `deploy`: `restrict,command="sudo /usr/local/sbin/vigilume-entrada"`; `sudoers`
  permite só esse script. O script (`deploy/vps/vigilume-entrada`) aceita dois verbos:
  - `deploy <sha 40 hex> <image-id>`: `docker load` da entrada padrão, confere o ID, etiqueta
    `vigilume:<sha>`, anota a versão anterior, `docker compose up -d app`, espera o healthcheck até 90 s,
    **rollback automático** se não ficar saudável (e sai com erro mostrando o log), mantém as 3 últimas
    imagens.
  - `backup` (§4).
- `deploy.yml` reescrito, manual por SHA: resolve a tag `:<sha>` (só existe depois do Trivy) no digest,
  baixa do GHCR e envia com `docker save | ssh`. Secrets `VPS_HOST`, `VPS_SSH_KEY` (ed25519 só para
  isso), `VPS_HOST_KEY` (impressão digital fixada; sem `StrictHostKeyChecking=no`).
- Limites: segundos de 502 durante a troca; o rollback não desfaz migration (migrations são aditivas).

## 4. Backup (seção 3, aprovada)

- Verbo `backup`: sobe um `postgres:18-alpine` descartável na rede interna, roda o
  `deploy/backup/backup.sh` **sem mudança** (dump como `vigilume_backup`, restauração em `tmpfs`,
  `verificar.sql`), cifra com `age` usando só a **chave pública** (`/opt/vigilume/age-recipient`), manda o
  cifrado pela saída padrão e o log pela de erro; códigos 0/3/4 preservados; aviso de disco medindo o
  sistema de arquivos dos volumes do Docker.
- `backup.yml` reescrito: diário 06:17 UTC + manual, `ssh deploy@VPS backup > vigilume-<data>.sql.age`,
  artifact por 30 dias, vermelho (e-mail do GitHub) em qualquer falha; desligado até `BACKUP_ENABLED=1`.
- Segunda camada: backup semanal da Hostinger (imagem da VPS).
- Runbook: restauração ("a VPS morreu", "apaguei um alvo"), ensaio trimestral, cópia mensal local.

## 5. Endurecimento da VPS **[D]**

- **Acesso:** usuário administrador pessoal (`sudo`, com senha própria) entrando só por **chave SSH**
  ed25519; `PermitRootLogin no`, `PasswordAuthentication no`, `KbdInteractiveAuthentication no`. Porta
  22 (mudar a porta não é segurança). `fail2ban` no `sshd`.
- **Firewall em duas camadas:** `ufw` (entrada: nega tudo, libera 22, 80, 443) **e** o firewall do painel
  da Hostinger com as mesmas regras. Como o Docker ignora o `ufw` para portas publicadas, a regra que vale
  é "só o Caddy publica porta" (§2) — o runbook confere com `ss -tlnp` e um scan de fora.
- **Atualizações:** `unattended-upgrades` com atualizações de segurança automáticas e **reinício
  automático às 04:30 (horário de Brasília)** quando o kernel pedir; os contêineres voltam sozinhos
  (`restart: unless-stopped`). Docker pelo repositório oficial da Docker.
- **Docker:** `daemon.json` com rotação de log (`max-size` 10m, `max-file` 3) e `live-restore`.
- **Tempo:** `systemd-timesyncd` ativo (TOTP e rollups dependem do relógio).

## 6. Observação **[D]**

- **Vigia externo:** conta gratuita no **healthchecks.io** (criada pelo dono). Um `systemd timer` na VPS,
  a cada minuto, faz `curl` em `https://<domínio>/healthz` — passando por DNS, TLS, Caddy e app — e, se der
  certo, pinga a URL do healthchecks; se falhar, pinga `/fail`. Parar de pingar (VPS morta, rede fora) ou
  falhar dispara e-mail do healthchecks para o dono. O Vigilume não vigia a si mesmo.
- **Disco:** o aviso de >85% do backup diário (já existe) + o próprio healthchecks do backup (o
  `backup.yml` falha em vermelho).
- **Logs:** `docker compose logs`, com rotação; o runbook ensina os três comandos que o dono precisa.

## 7. Runbook `docs/deploy/vps.md` **[D]**

Passo a passo para quem nunca usou VPS, em ordem, cada passo com **o que fazer, por que, e o que deve
aparecer**, e uma verificação antes do próximo. Grandes blocos:

1. Antes: gerenciador de senhas; chave SSH no computador do dono; domínio da Hostinger apontado para o IP
   da VPS (registro A); conta no Resend (registros DNS SPF/DKIM do domínio); conta no healthchecks.io.
2. Primeiro acesso e endurecimento (§5).
3. Docker e o diretório `/opt/vigilume` (arquivos de `deploy/vps/` copiados por `scp`), segredos gerados
   na VPS, chave pública do `age` (a privada fica só com o dono).
4. Usuário `deploy`, script de entrada, `sudoers`, secrets do GitHub, primeiro deploy pelo Actions.
5. Primeiro usuário do Vigilume pelo CLI (`docker compose exec app /vigilume user create`), login, 2FA.
6. E-mail (Resend), canal de teste, alvo de teste.
7. Backup ligado, primeiro backup à mão, **ensaio de restauração** antes de considerar pronto.
8. Vigia externo.
9. Verificações finais: scan de portas de fora, `TRUSTED_PROXIES` sem o aviso da B3 no log, `Host`
   estranho recusado, certificado válido, reboot de teste (tudo volta sozinho).
10. Operação: deploy seguinte, logs, reset-2fa, upgrade do Postgres, "a VPS morreu".

O que era genérico do `docs/deploy/fly.md` (reset-2fa, operação do Postgres, restauração, tornar o repo
público) migra para cá; `docs/deploy/fly.md`, `fly.toml`, `deploy/fly/` e as partes Fly dos workflows
saem. O digest do Postgres que o Dependabot lia em `deploy/fly/db/Dockerfile` passa a um arquivo
equivalente em `deploy/vps/` (os quatro lugares do digest continuam sincronizados).

## 8. Testes e CI

- **Compose de produção no CI:** um job (ou passo do e2e) sobe `deploy/vps/compose.prod.yaml` com um
  Caddy em modo local (certificado interno, domínio de teste) e confere: só 80/443 publicados; `Host`
  estranho não chega ao app; `X-Real-IP`/`Fly-Client-IP` do cliente não mudam o IP visto (o rate limit e
  o `ip_hash` usam o IP real); `/healthz` responde via Caddy; o banco não aceita conexão de fora da rede.
- **`vigilume-entrada`:** testes em shell (ou Go chamando o script) para: verbo desconhecido recusado,
  SHA malformado recusado, image-id divergente recusado, rollback quando o healthcheck falha, backup
  devolvendo só texto cifrado na saída padrão (nunca SQL em claro).
- **Workflows:** `actionlint` (se já usado) e checagem de que nenhum passo usa
  `StrictHostKeyChecking=no`.
- Sabotagem registrada para cada teste de ausência, como nos planos anteriores.

## 9. Decisões do controlador para o usuário revisar

1. **Reinício automático às 04:30** quando uma atualização de segurança pedir (custo: ~1 min fora do ar
   de madrugada, e o vigia pode mandar um alarme falso curto).
2. **`fail2ban` + SSH só por chave, porta 22** (custo: perder a chave do computador = acesso só pelo
   terminal web do painel da Hostinger; o runbook ensina a recuperação).
3. **healthchecks.io com timer na VPS** em vez de um serviço que consulta de fora (custo: se o DNS ou a
   rede entre a VPS e o mundo falhar de um jeito que o `curl` da própria VPS não veja, o alarme atrasa).
4. **Scripts da VPS em shell** (o `vigilume-entrada`), testados no CI.
5. **Fly removido por inteiro** (decisão do usuário), com o conteúdo genérico migrado.
