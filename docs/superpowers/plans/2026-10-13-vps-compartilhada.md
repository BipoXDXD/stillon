# VPS compartilhada com o GoblinShift — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** pôr o StillOn em `https://stillon.tech` atrás de uma borda Caddy do host, dividida com o GoblinShift, sem perder nenhuma obrigação de hospedagem do Plano 11.

**Architecture:** a borda (`/srv/edge`, Caddy, `172.31.0.2` na rede Docker `edge`) faz o TLS e encaminha por domínio. O Caddy do StillOn deixa de publicar portas, atende só HTTP para o domínio, confia só no IP da borda e entrega ao app `X-Forwarded-For: {client_ip}`. A rede `stillon` sai de `172.30.0.0/24` (ocupada na VPS) para `10.30.0.0/24`. O app não muda.

**Tech Stack:** Docker Compose, Caddy 2.11 (`caddy:2` por digest), Bash (`deploy/ci/checar-vps.sh`), pytest (GoblinShift).

**Spec:** [`docs/superpowers/specs/2026-10-13-vps-compartilhada-design.md`](../specs/2026-10-13-vps-compartilhada-design.md)

## Global Constraints

- Contrato da borda: rede externa `edge`, sub-rede `172.31.0.0/24`, dinâmicos em `172.31.0.128/25`, borda em `172.31.0.2`, **sem `trusted_proxies`**, `protocols h1 h2`, `Host` preservado, `import /etc/caddy/conf.d/*.caddy`.
- Rede `stillon`: `10.30.0.0/24`, gateway `10.30.0.1`, `ip_range 10.30.0.128/25`, caddy `10.30.0.2`, app `10.30.0.3`, db `10.30.0.4`, IPv6 desligado.
- Caddy do StillOn: `trusted_proxies static 172.31.0.2/32`, `trusted_proxies_strict`, `client_ip_headers X-Forwarded-For`, site `http://{$STILLON_DOMINIO}`, `header_up X-Forwarded-For {client_ip}`, mais os três `header_up -…` de hoje. Alias `stillon-caddy` na rede `edge`. Nenhum `ports`. Única variável: `STILLON_DOMINIO`.
- Trecho da borda: `deploy/vps/edge/stillon.caddy`, domínio literal `stillon.tech`, `reverse_proxy stillon-caddy:80`.
- Imagem do Caddy: `caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52` (a do `compose.prod.yaml`; o Dependabot a move).
- Tudo em português do Brasil. Commits do StillOn: `tipo(escopo): descrição`, assinados (GPG), com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Mudança na `main` só por PR. Commit só de documentação leva `[skip ci]`.
- Teste novo só vale depois de visto falhando com a mitigação sabotada.
- Nunca pedir segredo no chat. Passos com `sudo` ou passphrase: o dono roda, num terminal separado.

## Review Focus

1. **Emissão do certificado com a 443 fechada ou o DNS errado.** O esperado é que o trecho do StillOn só entre na borda depois que a 443 responder de fora (Task 6, passo 6.4 antes do 6.6), para não gastar o limite do Let's Encrypt.
2. **Reboot da VPS.** O esperado é que a borda, o GoblinShift e o StillOn voltem sozinhos. Coberto pelo 9.6 do runbook, que o passo 6.7 roda.
3. **O GoblinShift pelo IP depois da troca da borda.** O esperado é que ele responda como antes, com `X-Forwarded-For` = IP do cliente e `X-Forwarded-Proto` = esquema real. Coberto pelo teste de fumaça da Task 4 e pela conferência do passo 6.3.
4. **Um vizinho na rede `edge` forjando `X-Forwarded-For` direto no Caddy do StillOn.** O esperado é que o app veja o IP do vizinho. Coberto pela checagem 6b da Task 2.
5. **O StillOn parado.** O esperado é que a borda siga de pé, responda 502 ao domínio e volte a encaminhar quando ele subir. Coberto pela checagem 7b da Task 2.

---

### Task 0: Preparação e linha de base do `checar-vps.sh` no Mac

**Files:** nenhum versionado.

- [ ] **Step 1: Branch a partir da `main` atualizada**

```bash
cd /Users/01bipo/Documents/Projetos/Go/StillOn
git switch main && git pull --ff-only
git switch -c feat/vps-compartilhada
```

- [ ] **Step 2: Ferramentas GNU que o script pede**

O `checar-vps.sh` usa `flock`, `timeout` e `sha1sum`, além de `sed`, `awk` e `tar` com sintaxe GNU.

```bash
brew install flock coreutils gnu-sed gawk gnu-tar jq
export PATH="/opt/homebrew/opt/coreutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/gnu-tar/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:$PATH"
```

O `PATH` vale só para a sessão que roda o teste. Não vai para o `~/.zshrc`.

- [ ] **Step 3: Imagem do app e linha de base**

```bash
make image
bash deploy/ci/checar-vps.sh stillon:dev 2>&1 | tee /tmp/checar-vps-base.log | grep -E '^(ok|FALHOU)'
```

Esperado: todas as linhas `ok`, nenhuma `FALHOU`. Se houver `FALHOU` causado pelo macOS (Docker Desktop, BSD), registre quais. A verificação final de cada task passa a ser o job `vps` do CI no PR. Não corrija o script para o Mac nesta frente.

---

### Task 1: O StillOn atrás de uma borda de teste

O `compose.prod.yaml` perde as portas e só sobe com a rede `edge` de pé. Por isso a configuração, a borda de teste e o ajuste das checagens existentes vão juntos numa só task.

**Files:**
- Modify: `deploy/vps/compose.prod.yaml` (serviço `caddy`, `TRUSTED_PROXIES`, `DATABASE_URL`, IPs, `networks`, `volumes`)
- Modify: `deploy/vps/Caddyfile` (inteiro)
- Create: `deploy/vps/edge/stillon.caddy`
- Create: `deploy/ci/vps/borda/compose.yaml`, `deploy/ci/vps/borda/Caddyfile`
- Modify: `deploy/ci/vps/compose.teste.yaml`
- Modify: `deploy/ci/checar-vps.sh` (cabeçalho, `limpar`, cópias, `.env`, bloco 1, bloco 2, checagens 3 a 7)

**Interfaces:**
- Produces: a função `bd` no `checar-vps.sh` (compose da borda de teste), a variável `caddy_img` (a imagem do Caddy da config resolvida) e `cliente_ip` (o gateway da rede `edge`, que é o IP com que o curl do host chega à borda). A Task 2 usa as três.

- [ ] **Step 1: Escrever as checagens novas do bloco 1 (vão falhar)**

No `checar-vps.sh`, substitua as linhas de 188 a 193 (portas e serviços) por:

```bash
[ "$(q '[.services | to_entries[] | select(.value.ports != null and (.value.ports | length) > 0) | .key] | join(",")')" = "" ] ||
    falhar "algum serviço publica porta: quem ouve a 80 e a 443 é a borda do host"
[ "$(q '.services | keys | sort | join(",")')" = app,caddy,db ] ||
    falhar "serviços do compose.prod: $(q '.services | keys | join(",")'), queria app, caddy e db"
# Só o Caddy está na rede da borda, com o alias que o trecho dela usa.
[ "$(q '[.services | to_entries[] | select(.value.networks.edge != null) | .key] | join(",")')" = caddy ] ||
    falhar "outro serviço além do caddy está na rede edge"
[ "$(q '.services.caddy.networks.edge.aliases | join(",")')" = stillon-caddy ] ||
    falhar "o caddy não tem o alias stillon-caddy na rede edge"
[ "$(q '.networks.edge.external')/$(q '.networks.edge.name')" = true/edge ] ||
    falhar "a rede edge não é a externa 'edge' da borda"
```

Troque os valores de `172.30.0.x` nas linhas de 210 a 220 e na 236:

```bash
caddy_ip=$(q '.services.caddy.networks.stillon.ipv4_address')
[ "$caddy_ip" = 10.30.0.2 ] || falhar "IP do caddy é '$caddy_ip', queria 10.30.0.2"
```

```bash
[ "$(q '.services.app.networks.stillon.ipv4_address')/$(q '.services.db.networks.stillon.ipv4_address')" = 10.30.0.3/10.30.0.4 ] ||
    falhar "IPs fixos do app e do db não são 10.30.0.3 e 10.30.0.4"
```

```bash
[ "$(q '.networks.stillon.ipam.config[0].subnet')" = 10.30.0.0/24 ] || falhar "sub-rede da rede stillon não é 10.30.0.0/24"
```

```bash
[ "$(q '.services.caddy.environment | keys | sort | join(",")')" = STILLON_DOMINIO ] ||
    falhar "o caddy recebe variáveis além do domínio: $(q '.services.caddy.environment | keys | join(",")')"
```

```bash
[ "$(q '.services.app.environment.DATABASE_URL')" = "postgres://stillon_app:$(env_do_teste STILLON_APP_PASSWORD)@10.30.0.4:5432/stillon?sslmode=disable" ] ||
```

Substitua a checagem "Sem trusted_proxies no Caddyfile" (linhas 238 a 240) por:

```bash
# O Caddyfile confia só na borda (obrigação 1), fora dos comentários: uma
# lista mais larga deixaria um vizinho da rede edge escolher o IP do cliente.
sem_comentario() { sed 's/#.*//' "$1"; }
[ "$(sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -c 'trusted_proxies' || true)" = 2 ] &&
    sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*trusted_proxies static 172\.31\.0\.2/32' &&
    sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*trusted_proxies_strict' ||
    falhar "o Caddyfile não confia exatamente em 172.31.0.2/32, em modo estrito"
sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*header_up X-Forwarded-For \{client_ip\}' ||
    falhar "o Caddyfile não reescreve o X-Forwarded-For com o {client_ip}: o app veria a borda"
# O trecho da borda não confia em ninguém e aponta para o alias.
[ "$(sem_comentario "$RAIZ/deploy/vps/edge/stillon.caddy" | grep -c trusted_proxies || true)" = 0 ] ||
    falhar "o trecho da borda tem trusted_proxies: um cliente escolheria o próprio IP"
sem_comentario "$RAIZ/deploy/vps/edge/stillon.caddy" | grep -qxE '[[:space:]]*reverse_proxy stillon-caddy:80' ||
    falhar "o trecho da borda não encaminha para stillon-caddy:80"
```

No `case` do `env.exemplo` (linha 244), tire o `STILLON_CADDY_TESTE`:

```bash
    case "$v" in STILLON_SHA) continue ;; esac
```

Mensagem do `ok` do bloco 1:

```bash
ok "compose.prod.yaml: sem portas, só o caddy na rede edge, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis, segredos do .env; Caddyfile confia só na borda"
```

- [ ] **Step 2: Rodar e ver o bloco 1 falhar**

Run: `bash deploy/ci/checar-vps.sh stillon:dev 2>&1 | grep -E '^(ok|FALHOU)' | head -20`

Esperado: `FALHOU: algum serviço publica porta`, `IP do caddy é '172.30.0.2'`, `não confia exatamente em 172.31.0.2/32` e outros do bloco 1. O resto pode passar ou falhar. Interrompa com Ctrl-C se ele seguir para a pilha.

- [ ] **Step 3: O Caddyfile novo**

Substitua o conteúdo de `deploy/vps/Caddyfile` por:

```caddyfile
# Caddy de produção do StillOn (Plano 11; spec 2026-10-13-vps-compartilhada).
# Montado só-leitura em /etc/caddy/Caddyfile pelo deploy/vps/compose.prod.yaml;
# na VPS, fica em /opt/stillon/Caddyfile.
#
# Fica atrás da borda do host (/srv/edge), que faz o TLS e encaminha o domínio
# para cá pela rede edge. {$STILLON_DOMINIO} vem do /opt/stillon/.env (pelo
# environment do serviço caddy no compose).
{
	servers {
		protocols h1 h2

		# Só a borda, pelo IP fixo dela na rede edge, informa o IP do cliente
		# (obrigação 1). Estrito: vale o salto mais à direita que não é da
		# borda. Qualquer outra origem (um contêiner de outro projeto na rede
		# edge) vale pelo IP da própria conexão, e o X-Forwarded-For dela é
		# ignorado.
		trusted_proxies static 172.31.0.2/32
		trusted_proxies_strict
		client_ip_headers X-Forwarded-For
	}
}

# Só HTTP: o TLS termina na borda. Uma requisição com outro Host não casa com
# nada e não chega ao app: é a checagem de Host da revisão.
http://{$STILLON_DOMINIO} {
	reverse_proxy app:8080 {
		# O app confia no último salto do X-Forwarded-For quando a conexão vem
		# deste Caddy (TRUSTED_PROXIES). Sem esta linha, o Caddy acrescentaria
		# o IP da borda ao fim da cadeia e todo cliente viraria a borda; com
		# ela, o único salto é o cliente que o Caddy calculou acima.
		header_up X-Forwarded-For {client_ip}

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
```

- [ ] **Step 4: O trecho da borda**

Crie `deploy/vps/edge/stillon.caddy`:

```caddyfile
# O StillOn na borda do host (spec 2026-10-13-vps-compartilhada). Vai para
# /srv/edge/conf.d/stillon.caddy (runbook, passo "O StillOn na borda"); a
# borda importa todo conf.d/*.caddy. O domínio é literal: a borda não lê o
# .env do StillOn. TLS e o redirecionamento de http para https são da borda;
# sem trusted_proxies na borda, o X-Forwarded-For que chega aqui é o IP de
# quem conectou, nunca o que o cliente mandou.
stillon.tech {
	reverse_proxy stillon-caddy:80
}
```

- [ ] **Step 5: O `compose.prod.yaml`**

Serviço `caddy` inteiro (troca o bloco das linhas 30 a 60):

```yaml
  caddy:
    # caddy:2 por digest (a tag fica no nome). O Dependabot atualiza este
    # digest e o do Postgres (ecossistema docker-compose, /deploy/vps).
    image: caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52
    # Nenhuma porta publicada (obrigação 3). Quem ouve a 80 e a 443 da VPS é
    # a borda do host (/srv/edge), dividida com o GoblinShift: ela faz o TLS
    # e encaminha o domínio para cá pela rede edge. Este Caddy é o único
    # contêiner do StillOn naquela rede: o app e o banco ficam fora do
    # alcance dos outros projetos.
    environment:
      STILLON_DOMINIO: ${STILLON_DOMINIO:?defina STILLON_DOMINIO no /opt/stillon/.env}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
    networks:
      stillon:
        ipv4_address: 10.30.0.2
      edge:
        # O nome que o trecho da borda (deploy/vps/edge/stillon.caddy) usa.
        aliases: [stillon-caddy]
    mem_limit: 256m
    security_opt: ["no-new-privileges:true"]
    logging: *log
    restart: unless-stopped
```

No `app`: `DATABASE_URL` com `@10.30.0.4:5432`, `TRUSTED_PROXIES: 10.30.0.2/32`, `ipv4_address: 10.30.0.3`. No `db`: `ipv4_address: 10.30.0.4`. O comentário do `TRUSTED_PROXIES` passa a ser:

```yaml
      # O IP fixo do Caddy, e o CLIENT_IP_HEADER vazio: o app lê o último
      # salto do X-Forwarded-For, que o Caddy reescreve com o IP do cliente
      # que a borda informou (obrigação 2).
```

Seções `networks` e `volumes` do fim do arquivo:

```yaml
networks:
  stillon:
    # Sem IPv6 (obrigação 3): o sondador não ganha um caminho por um prefixo
    # que o safedial não bloqueia.
    enable_ipv6: false
    ipam:
      config:
        # Fora dos default-address-pools do daemon.json da VPS (172.16/12 e
        # 192.168/16): nenhuma rede criada por outro projeto toma esta faixa.
        # Era 172.30.0.0/24, ocupada pelo GoblinShift em 2026-10-13.
        - subnet: 10.30.0.0/24
          gateway: 10.30.0.1
          # Os IPs dinâmicos (o Postgres descartável do backup) saem da
          # metade de cima; os fixos (.2, .3, .4) ficam fora do sorteio.
          ip_range: 10.30.0.128/25
  edge:
    # A rede da borda do host, criada pelo compose dela (/srv/edge).
    # Externa: o StillOn nunca a cria nem a apaga.
    external: true
    name: edge

volumes:
  pgdata:
```

Atualize o comentário do topo do arquivo (linhas 1 a 3) para citar a borda:

```yaml
# StillOn em produção numa VPS (Plano 11, spec §2; spec 2026-10-13-vps-
# compartilhada). Fica em /opt/stillon/compose.prod.yaml, ao lado do
# Caddyfile, do initdb.sh e do .env (segredos, root 600, fora do git; modelo
# em deploy/vps/env.exemplo). Depende da rede edge da borda do host.
```

- [ ] **Step 6: A borda de teste**

Crie `deploy/ci/vps/borda/compose.yaml`:

```yaml
# SÓ PARA O TESTE (deploy/ci/checar-vps.sh). NUNCA vai para a VPS.
#
# A borda do host como o contrato da spec 2026-10-13-vps-compartilhada a
# descreve (a de verdade vem do repositório do GoblinShift): rede edge
# 172.31.0.0/24 com os dinâmicos em .128/25, a borda em 172.31.0.2, sem
# trusted_proxies, h1 e h2, um trecho por projeto em conf.d. As portas vão
# para o loopback, em portas altas; o certificado sai da CA interna.
name: stillon-teste-borda

services:
  proxy:
    # O mesmo digest do Caddy do compose.prod.yaml (o checar-vps.sh passa).
    image: ${CADDY_IMAGEM:?o checar-vps.sh define CADDY_IMAGEM}
    ports:
      - "127.0.0.1:${TESTE_VPS_HTTP:-18580}:80"
      - "127.0.0.1:${TESTE_VPS_HTTPS:-18543}:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./conf.d:/etc/caddy/conf.d:ro
    networks:
      edge:
        ipv4_address: 172.31.0.2

networks:
  edge:
    name: edge
    ipam:
      config:
        - subnet: 172.31.0.0/24
          ip_range: 172.31.0.128/25
```

Crie `deploy/ci/vps/borda/Caddyfile`:

```caddyfile
# SÓ PARA O TESTE: a borda do contrato, com a CA interna no lugar do Let's
# Encrypt. Sem trusted_proxies, de propósito: é o que faz a borda trocar o
# X-Forwarded-For do cliente pelo IP de quem conectou.
{
	local_certs
	servers {
		protocols h1 h2
	}
}

import /etc/caddy/conf.d/*.caddy
```

Substitua `deploy/ci/vps/compose.teste.yaml` por:

```yaml
# SÓ PARA O TESTE (deploy/ci/checar-vps.sh). NUNCA vai para a VPS.
#
# Por cima do deploy/vps/compose.prod.yaml: outro nome de projeto e outro
# nome de imagem para o app, para o teste nunca mexer numa imagem stillon:*
# de verdade. As portas são da borda de teste (deploy/ci/vps/borda). O resto
# (IPs fixos, ambiente do app, healthchecks, limites) é o de produção.
name: stillon-teste-vps

services:
  app:
    image: stillon-teste-vps:${STILLON_SHA:-atual}
```

- [ ] **Step 7: O `checar-vps.sh` sobe a borda e fala com ela**

Cabeçalho (linhas 2 a 14): troque o trecho sobre portas e rede por:

```bash
# Checagens da produção na VPS (Plano 11, spec §8; spec 2026-10-13-vps-
# compartilhada), sem VPS: sobe uma borda de teste igual ao contrato
# (deploy/ci/vps/borda, CA interna, domínio stillon.example) e, atrás dela,
# o deploy/vps/compose.prod.yaml, e confere o que a revisão exigiu da
# hospedagem. O job "vps" do CI roda; localmente, da raiz do repositório:
```

…e, depois da linha das ferramentas:

```bash
# Os projetos do compose são "stillon-teste-vps" e "stillon-teste-borda", e
# as portas da borda vão para o loopback (TESTE_VPS_HTTP, padrão 18580;
# TESTE_VPS_HTTPS, padrão 18543). As redes são as de produção (edge,
# 172.31.0.0/24, e 10.30.0.0/24): não rode numa máquina que já tenha a borda
# ou o StillOn de produção de pé.
```

Em `limpar()`, depois do `dc down -v --remove-orphans …`:

```bash
    bd down -v --remove-orphans >/dev/null 2>&1 || true
```

Depois do `cp "$RAIZ/deploy/ci/vps/compose.teste.yaml" "$DIR/"`:

```bash
# A borda de teste com o trecho de produção, trocando só o domínio (uma
# linha, exatamente, ou o teste rodaria outra coisa).
mkdir -p "$DIR/borda/conf.d"
cp "$RAIZ/deploy/ci/vps/borda/compose.yaml" "$RAIZ/deploy/ci/vps/borda/Caddyfile" "$DIR/borda/"
[ "$(grep -cx 'stillon.tech {' "$RAIZ/deploy/vps/edge/stillon.caddy")" = 1 ] ||
    { echo "FALHOU: o stillon.caddy não tem a linha 'stillon.tech {' (uma vez)" >&2; exit 1; }
sed "s/^stillon\.tech {\$/$DOMINIO {/" "$RAIZ/deploy/vps/edge/stillon.caddy" > "$DIR/borda/conf.d/stillon.caddy"
```

No `.env` do teste, tire a linha `STILLON_CADDY_TESTE=local_certs`.

Depois da definição de `dc()`:

```bash
bd() {
    CADDY_IMAGEM=$caddy_img TESTE_VPS_HTTP=$HTTP TESTE_VPS_HTTPS=$HTTPS \
        docker compose --project-directory "$DIR/borda" -f "$DIR/borda/compose.yaml" "$@"
}
```

No bloco 1, logo depois de `q()`:

```bash
caddy_img=$(q '.services.caddy.image')
```

No bloco 2, antes do `2a`:

```bash
# A borda antes da pilha: o compose.prod.yaml só sobe com a rede edge.
bd up -d --wait --wait-timeout 60 >&2 || { echo "FALHOU: a borda de teste não subiu" >&2; exit 1; }
# O curl do host chega à borda pelo gateway da rede edge: é esse o IP do
# cliente que o app tem de ver.
cliente_ip=$(docker network inspect -f '{{(index .IPAM.Config 0).Gateway}}' edge)
[ -n "$cliente_ip" ] || { echo "FALHOU: a rede edge não informou o gateway" >&2; exit 1; }
```

Troque a checagem "Só o caddy publica" (linhas 292 a 294) por:

```bash
# Ninguém da pilha publica porta: só a borda, e só 80 e 443.
publicadas=$(dc ps --format json | jq -rs '[.[] | (.Publishers // [])[] | select(.PublishedPort > 0)] | length')
[ "$publicadas" = 0 ] || falhar "a pilha do StillOn publica porta"
borda_portas=$(bd ps --format json | jq -rs '[.[] | .Service as $s | (.Publishers // [])[] | select(.PublishedPort > 0) | "\($s):\(.TargetPort)"] | sort | unique | join(" ")')
[ "$borda_portas" = "proxy:443 proxy:80" ] || falhar "portas publicadas na borda: '$borda_portas'"
for s in caddy app db; do
```

(o laço passa a incluir `caddy`, e o corpo dele fica igual).

A CA interna passa a vir da borda:

```bash
bd exec -T proxy cat /data/caddy/pki/authorities/local/root.crt > "$DIR/ca.crt"
```

Nas mensagens das checagens 3 e 4, "pelo Caddy" vira "pela borda". Checagem 5: troque as linhas do `gateway` (352 a 354) por:

```bash
[ "$real" = "$(hmac 172.31.0.2)" ] && falhar "o app viu o IP da borda: o Caddy do StillOn não reescreve o X-Forwarded-For"
[ "$real" = "$(hmac "$cliente_ip")" ] || falhar "o app não viu o IP do cliente ($cliente_ip, o gateway da rede edge)"
```

Checagem 7: `172.30.0.4` → `10.30.0.4` nas duas linhas do `pg_isready`.

- [ ] **Step 8: Rodar e ver tudo passar**

Run: `bash deploy/ci/checar-vps.sh stillon:dev 2>&1 | tee /tmp/checar-vps.log | grep -E '^(ok|FALHOU)'`

Esperado: todos os `ok` da linha de base, nenhum `FALHOU`. Se a linha de base da Task 0 tinha falhas do macOS, as mesmas e só elas.

- [ ] **Step 9: Sabotagens (cada uma tem de derrubar uma checagem; desfaça depois de cada uma)**

1. No `deploy/vps/Caddyfile`, apague a linha `header_up X-Forwarded-For {client_ip}`. Esperado: o bloco 1 falha com "não reescreve o X-Forwarded-For", e a checagem 5 com "o app viu o IP da borda".
2. No `deploy/ci/vps/borda/Caddyfile`, ponha `trusted_proxies static private_ranges` dentro do `servers`. Esperado: a checagem 5 falha (o app acreditou em `1.2.3.7` ou não viu o `cliente_ip`).
3. No `compose.prod.yaml`, ponha de volta `ports: ["0.0.0.0:80:80"]` no `caddy`. Esperado: o bloco 1 falha com "algum serviço publica porta".

Run após desfazer: `git diff --stat` mostra só as mudanças da task.

- [ ] **Step 10: `shellcheck` e commit**

```bash
shellcheck deploy/ci/checar-vps.sh
sh deploy/ci/checar-nome.sh
git add deploy/vps/compose.prod.yaml deploy/vps/Caddyfile deploy/vps/edge/stillon.caddy \
  deploy/ci/vps/borda deploy/ci/vps/compose.teste.yaml deploy/ci/checar-vps.sh
git commit -m "feat(vps): StillOn atrás da borda do host compartilhada com o GoblinShift

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Vizinho na rede `edge` e a borda sem o StillOn

**Files:**
- Modify: `deploy/ci/checar-vps.sh` (checagens novas 6b, depois da 6, e 7b, depois da 7)

**Interfaces:**
- Consumes: `bd`, `caddy_img`, `cu`, `BASE`, `entrar`, `ultimo_hash`, `hmac`, `sql`, `dc`, `DOMINIO`, `SENHA` (Task 1 e script existente).

- [ ] **Step 1: Escrever a 6b**

Depois do `ok "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP"`:

```bash
# 6b. Um contêiner de outro projeto na rede edge, falando direto com o Caddy
#     do StillOn (sem a borda), não escolhe o IP que o app vê: o Caddy só
#     confia no X-Forwarded-For que vem de 172.31.0.2. Outra conta, para o
#     limitador de login da conta do dono não entrar na conta.
VIZINHO=vizinho@stillon.example
printf '%s\n' "$SENHA" | dc exec -T app /stillon user create --email "$VIZINHO" >&2 ||
    falhar "stillon user create do vizinho falhou"
caddy_edge=$(docker inspect -f '{{(index .NetworkSettings.Networks "edge").IPAddress}}' "$(dc ps -q caddy)")
vizinho_ip=172.31.0.77
c=$(docker run --rm --network edge --ip "$vizinho_ip" --add-host "$DOMINIO:$caddy_edge" \
    --entrypoint curl "$caddy_img" -sS --max-time 10 -o /dev/null -w '%{http_code}' \
    -X POST "http://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' -H 'X-Forwarded-For: 1.2.3.8' \
    -d "{\"email\":\"$VIZINHO\",\"password\":\"$SENHA\"}" || true)
[ "$c" = 200 ] || falhar "login do vizinho direto no Caddy do StillOn não deu 200 ($c)"
direto=$(ultimo_hash)
[ "$direto" = "$(hmac 1.2.3.8)" ] && falhar "o Caddy do StillOn acreditou no X-Forwarded-For de um vizinho da rede edge"
[ "$direto" = "$(hmac "$vizinho_ip")" ] || falhar "o app não viu o IP do vizinho ($vizinho_ip)"
ok "um vizinho da rede edge não escolhe o IP visto pelo app"
```

- [ ] **Step 2: Ver a 6b falhar com a mitigação sabotada**

No `deploy/vps/Caddyfile`, troque `trusted_proxies static 172.31.0.2/32` por `trusted_proxies static private_ranges`.

Run: `bash deploy/ci/checar-vps.sh stillon:dev 2>&1 | grep -E '^(ok|FALHOU)'`

Esperado: `FALHOU: o Caddy do StillOn acreditou no X-Forwarded-For de um vizinho da rede edge` (o bloco 1 também falha). Desfaça a sabotagem.

- [ ] **Step 3: Escrever a 7b**

Depois do `ok "o banco só aceita conexão de dentro da rede"`:

```bash
# 7b. A borda não depende do StillOn: com o Caddy dele parado, ela continua
#     de pé e responde 502 ao domínio; com ele de volta, encaminha de novo.
dc stop caddy >&2
c=$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)
[ "$c" = 502 ] || falhar "com o StillOn parado, a borda respondeu $c, queria 502"
[ "$(docker inspect -f '{{.State.Running}}' "$(bd ps -q proxy)")" = true ] || falhar "a borda caiu com o StillOn parado"
dc start caddy >&2
de_volta=
for _ in $(seq 30); do
    if [ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)" = 200 ]; then de_volta=1; break; fi
    sleep 1
done
[ -n "$de_volta" ] || falhar "com o Caddy do StillOn de volta, a borda não voltou a encaminhar em 30 s"
ok "a borda segue de pé com o StillOn parado (502) e volta a encaminhar"
```

- [ ] **Step 4: Ver a 7b falhar com a borda sabotada**

Sabotagem: o trecho copiado para o teste aponta para um IP onde não há nada, em vez do nome `stillon-caddy`. No `checar-vps.sh`, troque temporariamente o `sed` da cópia do trecho por:

```bash
sed -e "s/^stillon\.tech {\$/$DOMINIO {/" -e 's/stillon-caddy:80/172.31.0.200:80/' "$RAIZ/deploy/vps/edge/stillon.caddy" > "$DIR/borda/conf.d/stillon.caddy"
```

Esperado: a checagem 3 e a 7b falham (sem o StillOn alcançável, `/healthz` nunca dá 200). Desfaça.

- [ ] **Step 5: Rodar tudo**

Run: `bash deploy/ci/checar-vps.sh stillon:dev 2>&1 | grep -E '^(ok|FALHOU)'`

Esperado: todos `ok`, inclusive as duas linhas novas, e os blocos 8 e 9 (`stillon-entrada` e backup) intactos.

- [ ] **Step 6: Commit**

```bash
shellcheck deploy/ci/checar-vps.sh
git add deploy/ci/checar-vps.sh
git commit -m "test(vps): vizinho na rede edge e a borda sem o StillOn

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Runbook e orientação do projeto

**Files:**
- Modify: `docs/deploy/vps.md` (Topologia, 3.1, 3.2, 3.3, um passo novo no bloco 4, bloco 9, tabela de cópias)
- Modify: `CLAUDE.md` (Orientação atual)
- Modify: `docs/superpowers/specs/2026-10-13-vps-compartilhada-design.md` (Estado)

Regras de escrita do runbook (do `CLAUDE.md`): linhas curtas, um comando por bloco, nada de heredoc (`echo '…' | sudo tee` no lugar), dizer onde roda e o prompt esperado, **Faça / Por quê / Deve aparecer / Confira** em cada passo.

- [ ] **Step 1: Topologia**

Troque o desenho da seção "Topologia" pelo da spec (borda → `stillon-caddy` → app → db; borda → `goblinshift-web`) e uma frase: a borda é do host, vem do repositório do GoblinShift, e o StillOn só entrega o trecho `stillon.caddy`.

- [ ] **Step 2: 3.1 vira "O Docker que já está na VPS"**

**Faça** (na VPS, prompt `<seu usuário>@<host da VPS>:~$`):

```bash
docker --version
```

```bash
sudo docker info -f '{{.DriverStatus}}'
```

```bash
ip -4 route
```

**Deve aparecer:** Docker 29 ou maior; `io.containerd.snapshotter.v1` no `DriverStatus`; nenhuma rota começando com `10.30.`. **Por quê:** o Docker foi instalado pelo `provision.sh` do GoblinShift; o deploy do StillOn exige o store do containerd, e a rede `stillon` usa `10.30.0.0/24`. **Confira:** sem o `snapshotter`, pare e chame o Claude (trocar de store esconde as imagens do GoblinShift). Com rota em `10.30.`, pare: a faixa precisa mudar no `compose.prod.yaml`.

- [ ] **Step 3: 3.2 vira "`live-restore` sem derrubar ninguém"**

**Faça:**

```bash
sudo apt install -y jq
```

```bash
sudo cp /etc/docker/daemon.json /etc/docker/daemon.json.antes-do-stillon
```

```bash
sudo jq '. + {"live-restore": true}' /etc/docker/daemon.json.antes-do-stillon | sudo tee /etc/docker/daemon.json
```

```bash
sudo systemctl reload docker
```

```bash
sudo docker info -f '{{.LiveRestoreEnabled}}'
```

**Deve aparecer:** o JSON com as chaves do GoblinShift (`default-address-pools`, `runtimes`, `log-opts`) **e** `"live-restore": true`; depois, `true`. **Por quê:** o `live-restore` mantém os contêineres de pé quando o Docker é atualizado, e o `reload` aplica sem reiniciar nada (a opção é recarregável). **Confira:** `docker ps` não mostra nenhum contêiner do GoblinShift com `Up` de poucos segundos (`sudo docker ps --format '{{.Names}} {{.Status}}'`).

- [ ] **Step 4: 3.3 e a tabela de cópias**

No `scp` do 3.3, acrescente `deploy/vps/edge/stillon.caddy` à lista. A instalação dele **não** acontece no 3.3 (vai no passo novo do Step 5). Na tabela do fim do runbook, acrescente a linha:

| `deploy/vps/edge/stillon.caddy` | `/srv/edge/conf.d/stillon.caddy` | `0644` | `caddy validate` e `caddy reload` na borda (passo "O StillOn na borda") |

- [ ] **Step 5: Passo novo depois do primeiro deploy (4.8): "O StillOn na borda"**

Pré-condição escrita no passo: a borda já é Caddy e a 443 responde de fora (Task 6, passos 6.3 e 6.4).

**Faça:**

```bash
sudo install -m 0644 -o root -g root stillon.caddy /srv/edge/conf.d/
```

```bash
cd /srv/edge
```

```bash
sudo docker compose exec proxy caddy validate --config /etc/caddy/Caddyfile
```

```bash
sudo docker compose exec proxy caddy reload --config /etc/caddy/Caddyfile
```

No Mac (prompt `%`):

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://stillon.tech/healthz
```

**Deve aparecer:** `Valid configuration`; o reload sem erro; `200`. **Por quê:** o trecho diz à borda para encaminhar `stillon.tech` ao Caddy do StillOn e pedir o certificado. **Confira:** se o `curl` falhar no TLS, `sudo docker compose logs --tail 30 proxy` em `/srv/edge` (procure `obtaining certificate` e o erro do ACME). Não repita o reload em sequência: cada falha conta no limite do Let's Encrypt.

- [ ] **Step 6: Bloco 9**

- 9.2 (IP do cliente): a conta é "o `ip_hash` da sua sessão é o HMAC do IP público do seu Mac", conferido como hoje, agora pela borda.
- 9.3 (`Host` estranho): `curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: estranho.example' http://<IP da VPS>/healthz` mostra a resposta do GoblinShift ou 404, nunca um `X-Trace-Id` do StillOn (`curl -sS -D - -o /dev/null …`).
- 9.4 (certificado): o emissor é Let's Encrypt, servido pela borda.
- 9.1 (portas): continua só 22, 80 e 443, agora vindo da borda.

- [ ] **Step 7: `CLAUDE.md` e a spec**

Em `CLAUDE.md`, seção "Orientação atual", troque pela situação de 2026-10-13: o dono migrou para o Mac; a VPS é compartilhada com o GoblinShift (spec e plano desta frente); o próximo passo é a Task 6 deste plano, guiada; a hospedagem parte do Mac. Na spec, `Estado:` vira "spec aprovada; implementação em andamento (plano 2026-10-13-vps-compartilhada)".

- [ ] **Step 8: Commit**

```bash
sh deploy/ci/checar-nome.sh
git add docs/deploy/vps.md CLAUDE.md docs/superpowers/specs/2026-10-13-vps-compartilhada-design.md
git commit -m "docs(vps): runbook com a borda compartilhada e o Docker do GoblinShift

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

(Sem `[skip ci]`: o mesmo PR leva as Tasks 1 e 2.)

---

### Task 4: A borda do GoblinShift vira Caddy

**Pré-condição:** o dono confirma que ninguém mais está commitando no GoblinShift durante esta task. Em 2026-10-13 havia outra frente ativa (ADR 015, gVisor).

**Files** (em `/Users/01bipo/Documents/Projetos/Python/GoblinShift`):
- Modify: `deploy/edge/compose.yaml`
- Create: `deploy/edge/Caddyfile`, `deploy/edge/conf.d/goblinshift.caddy`
- Delete: `deploy/edge/conf.d/default.conf`
- Modify: `deploy/provision.sh` (comentário do firewall)
- Create: `docs/adr/014-proxy-de-borda-caddy.md`
- Modify: `docs/GoblinShiftPlan.md` (o parágrafo "Proxy de borda" e a linha do 014 na tabela de ADRs)
- Test: `tests/test_edge.py`

- [ ] **Step 1: Branch**

```bash
cd /Users/01bipo/Documents/Projetos/Python/GoblinShift
git switch main && git pull --ff-only
git switch -c borda-caddy
```

- [ ] **Step 2: Teste do contrato da borda (vai falhar)**

Crie `tests/test_edge.py`:

```python
import re
from pathlib import Path

import yaml

EDGE = Path(__file__).resolve().parent.parent / "deploy" / "edge"


def _sem_comentarios(path: Path) -> str:
    return "\n".join(line.split("#", 1)[0] for line in path.read_text().splitlines())


def _compose() -> dict:
    return yaml.safe_load((EDGE / "compose.yaml").read_text())


def test_borda_e_caddy_por_digest() -> None:
    image = _compose()["services"]["proxy"]["image"]
    assert re.fullmatch(r"caddy:2@sha256:[0-9a-f]{64}", image)


def test_borda_publica_so_80_e_443_tcp() -> None:
    assert sorted(_compose()["services"]["proxy"]["ports"]) == ["443:443", "80:80"]


def test_borda_tem_ip_fixo_fora_da_faixa_dinamica() -> None:
    compose = _compose()
    assert compose["services"]["proxy"]["networks"]["edge"]["ipv4_address"] == "172.31.0.2"
    ipam = compose["networks"]["edge"]["ipam"]["config"][0]
    assert ipam == {"subnet": "172.31.0.0/24", "ip_range": "172.31.0.128/25"}


def test_borda_nao_confia_em_proxy_nenhum() -> None:
    # Sem trusted_proxies, a borda troca o X-Forwarded-For do cliente pelo IP
    # de quem conectou; os projetos confiam nesse valor só vindo de 172.31.0.2.
    for arquivo in [EDGE / "Caddyfile", *sorted((EDGE / "conf.d").glob("*.caddy"))]:
        assert "trusted_proxies" not in _sem_comentarios(arquivo), arquivo


def test_borda_importa_um_trecho_por_projeto() -> None:
    texto = _sem_comentarios(EDGE / "Caddyfile")
    assert "import /etc/caddy/conf.d/*.caddy" in texto
    assert "protocols h1 h2" in texto


def test_acesso_pelo_ip_vai_para_o_goblinshift() -> None:
    texto = _sem_comentarios(EDGE / "conf.d" / "goblinshift.caddy")
    assert re.search(r"^:80 \{", texto, re.MULTILINE)
    assert "reverse_proxy goblinshift-web:80" in texto
```

Run: `uv run pytest tests/test_edge.py -q`

Esperado: FAIL (imagem `nginx:1.30.5`, sem `Caddyfile`, sem `goblinshift.caddy`). Se o `yaml` não estiver nas dependências de teste, confira com `uv run python -c "import yaml"` e acrescente `pyyaml` ao grupo de desenvolvimento do `pyproject.toml`.

- [ ] **Step 3: A borda em Caddy**

`deploy/edge/compose.yaml`:

```yaml
# Proxy de borda do host (docs/GoblinShiftPlan.md, ADR 014). Não pertence a nenhum projeto:
# ouve nas portas 80/443, faz o TLS (Let's Encrypt) e encaminha por domínio para o ponto de
# entrada de cada projeto, na rede Docker compartilhada `edge`. Cada projeto entrega um trecho
# em conf.d/<projeto>.caddy. Instalado em /srv/edge por deploy/provision.sh.
name: edge

services:
  proxy:
    image: caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./conf.d:/etc/caddy/conf.d:ro
      # Certificados e conta ACME: perder o volume é pedir tudo de novo ao Let's Encrypt,
      # que tem limite semanal por domínio.
      - caddy_data:/data
      - caddy_config:/config
    ports:
      # Só TCP: HTTP/3 (443/udp) fica de fora, como no Caddyfile.
      - "80:80"
      - "443:443"
    networks:
      edge:
        # IP fixo: os projetos confiam nos cabeçalhos X-Forwarded-* só vindos daqui,
        # não de qualquer container da rede compartilhada.
        ipv4_address: 172.31.0.2
    restart: unless-stopped

networks:
  edge:
    name: edge
    ipam:
      config:
        - subnet: 172.31.0.0/24
          # Alocação dinâmica só na metade de cima: nenhum container recebe 172.31.0.2
          # por acaso (por exemplo, com o proxy parado) e herda a confiança dele.
          ip_range: 172.31.0.128/25

volumes:
  caddy_data:
  caddy_config:
```

`deploy/edge/Caddyfile`:

```caddyfile
# Proxy de borda do host (ADR 014). Só opções globais; cada projeto entrega o seu trecho em
# conf.d/<projeto>.caddy e nunca sobrescreve o de outro.
{
	servers {
		# HTTP/3 fora: a 443/udp não é publicada.
		protocols h1 h2
		# Sem trusted_proxies, de propósito: a borda é o primeiro salto voltado para a
		# internet e troca o X-Forwarded-For do cliente pelo IP de quem conectou.
	}
}

import /etc/caddy/conf.d/*.caddy
```

`deploy/edge/conf.d/goblinshift.caddy`:

```caddyfile
# Enquanto o GoblinShift não tiver domínio, o acesso pelo IP (http) vai para ele. O nome é
# resolvido na hora de cada conexão: com o projeto parado, a borda sobe e responde 502.
:80 {
	reverse_proxy goblinshift-web:80
}
```

Apague `deploy/edge/conf.d/default.conf`. Os `proxy_set_header` dele não fazem falta: o Caddy preserva o `Host`, põe `X-Forwarded-For` com o IP de quem conectou e `X-Forwarded-Proto` com o esquema real. O `client_max_body_size 256m` e o `proxy_request_buffering off` também não: o Caddy não limita o corpo por padrão e repassa em streaming. O `proxy_read_timeout 600s` também não: o Caddy não tem timeout de leitura de resposta por padrão.

No `deploy/provision.sh`, troque o comentário do firewall:

```bash
# Portas publicadas pelo Docker contornam o ufw. Só o proxy de borda publica a 80 e a 443;
# o OpenObserve fica preso a 127.0.0.1.
```

- [ ] **Step 4: Rodar os testes**

Run: `uv run pytest tests/test_edge.py tests/test_compose_docker_socket.py -q`

Esperado: PASS.

- [ ] **Step 5: Fumaça da borda com um GoblinShift falso (nada vai para o repositório)**

```bash
S=$(mktemp -d)
printf ':80 {\n\trespond "xff={header.X-Forwarded-For} proto={header.X-Forwarded-Proto} host={host}"\n}\n' > "$S/eco"
docker compose -p borda-fumaca --project-directory deploy/edge -f deploy/edge/compose.yaml up -d
docker run -d --name eco-gs --network edge --network-alias goblinshift-web \
  -v "$S/eco:/etc/caddy/Caddyfile:ro" caddy:2@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52
curl -sS -H 'X-Forwarded-For: 1.2.3.4' http://127.0.0.1/
docker network inspect -f '{{(index .IPAM.Config 0).Gateway}}' edge
docker rm -f eco-gs
docker compose -p borda-fumaca --project-directory deploy/edge -f deploy/edge/compose.yaml down -v
rm -rf "$S"
```

Esperado: `xff=<o gateway da rede edge> proto=http host=127.0.0.1`. Nunca `1.2.3.4`. Requer as portas 80 e 443 livres no Mac.

- [ ] **Step 6: ADR 014 e o plano**

Crie `docs/adr/014-proxy-de-borda-caddy.md` no formato do ADR 011 (título, **Estado**, **Plano**, Contexto, Decisão, Alternativas descartadas em tabela, Consequências):

- **Estado:** aceito (2026-10-13).
- **Contexto:** só um processo ouve na 80/443 da VPS compartilhada (ADR 011); o primeiro domínio (`stillon.tech`, do outro projeto) pede TLS na borda.
- **Decisão:** borda Caddy (`caddy:2` por digest) em `/srv/edge`, IP `172.31.0.2` na rede `edge` (`172.31.0.0/24`, dinâmicos em `.128/25`), sem `trusted_proxies`, `protocols h1 h2`, TLS automático; cada projeto entrega `conf.d/<projeto>.caddy`; o acesso pelo IP vai para o GoblinShift. Esse é o contrato de que os projetos dependem; mudá-lo é decisão do dono nos dois repositórios.
- **Alternativas descartadas:** Nginx com certbot (renovação por cron e reload externos, falha silenciosa de renovação); cada projeto publicar a própria porta (só um processo ouve na 80/443); o Nginx do GoblinShift atender o outro projeto (um deploy derrubaria o outro).
- **Consequências:** certificados no volume `caddy_data` (perder o volume = reemitir, com limite semanal); um trecho inválido impede o `caddy reload`, mas a configuração anterior fica no ar; a borda roda `caddy validate` antes de todo reload.

Em `docs/GoblinShiftPlan.md`, no parágrafo **Proxy de borda** (seção 3.6), troque "O TLS dos domínios fica no proxy de borda (ADR 014)." por "O TLS dos domínios fica no proxy de borda, um Caddy com certificados automáticos (ADR 014)." Na tabela de ADRs, a linha 014 passa a dizer "A tecnologia é Caddy, com TLS automático e um trecho por projeto em `conf.d`".

- [ ] **Step 7: Lint e commit**

```bash
uv run ruff check . && uv run ruff format --check . && uv run mypy . && uv run pytest -q
git add deploy/edge deploy/provision.sh docs/adr/014-proxy-de-borda-caddy.md docs/GoblinShiftPlan.md tests/test_edge.py
git commit -m "Borda: proxy de borda em Caddy com TLS automático e um trecho por projeto (ADR 014)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

(Siga a forma de integração que o GoblinShift usa: o histórico tem commits diretos na `main`. Pergunte ao dono antes de dar `push`.)

---

### Task 5: PRs, CI e integração

- [ ] **Step 1: PR do StillOn**

Escreva `/tmp/pr-vps-compartilhada.md` com três seções: **O que muda** (um item por arquivo da Task 1 a 3, com o porquê, citando a spec); **Verificação** (as saídas `ok` do `checar-vps.sh` e cada sabotagem das Tasks 1 e 2 com a checagem que ela derrubou); **Fora deste PR** (a borda do GoblinShift e a execução na VPS). Última linha: `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

```bash
cd /Users/01bipo/Documents/Projetos/Go/StillOn
git push -u origin feat/vps-compartilhada
gh pr create --base main --title "feat(vps): StillOn atrás da borda compartilhada com o GoblinShift" --body-file /tmp/pr-vps-compartilhada.md
```

- [ ] **Step 2: CI**

Run: `gh pr checks --watch`

Esperado: todos verdes, inclusive o job `vps` (é ele que roda o `checar-vps.sh` em Linux). Uma falha vira investigação com o log (`gh run view --log-failed`), nunca um novo push às cegas: o orçamento de Actions é curto.

- [ ] **Step 3: Integração**

O merge do StillOn e o `push` do GoblinShift são do dono. Depois do merge, conferir que o `publish` da `main` terminou: `gh run list --branch main --limit 3`.

---

### Task 6: Execução guiada na VPS

Guiada como o `CLAUDE.md` manda: um passo numerado por vez, dizendo onde roda e o prompt esperado, o que deve aparecer e o porquê em uma frase. O dono roda o que pede `sudo` num terminal separado e confirma; o Claude confere com o **Confira** antes do próximo. A chave entra no agente com `ssh-add -t 4h ~/.ssh/stillon_admin`, para o Claude poder conferir sem `sudo`.

- [ ] **6.1** Runbook 3.1 novo: Docker, store do containerd, nenhuma rota em `10.30.`.
- [ ] **6.2** Runbook 3.2 novo: `live-restore` com `reload`. Confira: os contêineres do GoblinShift não reiniciaram.
- [ ] **6.3** Troca da borda: no Mac, `scp` de `deploy/edge/compose.yaml`, `deploy/edge/Caddyfile` e `deploy/edge/conf.d/goblinshift.caddy` (do GoblinShift já integrado) para `~/borda/` na VPS. Na VPS: `sudo cp /srv/edge/compose.yaml /srv/edge/compose.yaml.nginx`; `sudo install -m 0644 -o root -g root ~/borda/compose.yaml ~/borda/Caddyfile /srv/edge/`; `sudo install -m 0644 -o root -g root ~/borda/goblinshift.caddy /srv/edge/conf.d/`; `sudo rm /srv/edge/conf.d/default.conf`; `cd /srv/edge`; `sudo docker compose up -d`; `sudo docker compose exec proxy caddy validate --config /etc/caddy/Caddyfile`. Confira, do Mac: `curl -sS http://$VPS/` responde como o GoblinShift respondia (hoje, `{"detail":"Not Found"}`), e `curl -sSI http://$VPS/` não mostra `Server: nginx`. Para voltar atrás: `sudo cp /srv/edge/compose.yaml.nginx /srv/edge/compose.yaml`, devolver o `default.conf` e `sudo docker compose up -d`.
- [ ] **6.4** Firewall do painel da Hostinger: conferir que a regra da 443/tcp existe e está antes de qualquer "drop". Confira, do Mac: `nc -z -G 5 $VPS 443 && echo aberta` (com a borda ouvindo, tem de abrir).
- [ ] **6.5** Runbook do StillOn do 3.3 ao 4.8, com a `main` nova, um passo e seu **Confira** por vez (o 3.4 gera segredos: antes dele, um passo próprio "crie a entrada no Bitwarden").
- [ ] **6.6** Passo novo "O StillOn na borda" (Task 3, Step 5). Confira: `https://stillon.tech/healthz` responde `200` com certificado Let's Encrypt.
- [ ] **6.7** Blocos 5 a 9 do runbook, inclusive o reboot de teste do 9.6: a borda, o GoblinShift e o StillOn voltam sozinhos.
- [ ] **6.8** Registro de continuidade novo em `docs/superpowers/reports/` e `CLAUDE.md` atualizados com o estado real (o que foi feito, o que foi conferido, o que falta), num PR só de documentação com `[skip ci]`.
