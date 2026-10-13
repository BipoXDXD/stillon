# StillOn e GoblinShift na mesma VPS

Estado: spec aprovada pelo dono em 2026-10-13; implementação em andamento
([plano](../plans/2026-10-13-vps-compartilhada.md)).

## Contexto

O runbook ([`docs/deploy/vps.md`](../../deploy/vps.md)) parou depois do bloco 2,
em 2026-09-28. Em 2026-10-13, o `provision.sh` do GoblinShift, outro projeto do
dono, foi executado na mesma VPS (Hostinger KVM 2: 2 vCPU, 7,8 GB, 96 GB). O plano
do GoblinShift já previa dividir a máquina: um proxy de borda do host recebe as
portas 80/443 e encaminha por domínio para o ponto de entrada de cada projeto, numa
rede Docker compartilhada `edge` (ADR 014 do GoblinShift). A tecnologia do TLS da
borda ficou para "quando houver o primeiro domínio", e o primeiro é o `stillon.tech`.

Estado conferido na VPS em 2026-10-13, sem `sudo`:

- O endurecimento do bloco 2 continua de pé (SSH só por chave e sem root,
  fail2ban, ufw, atualizações automáticas). O GoblinShift acrescentou
  `10-hardening.conf` com as mesmas regras.
- Docker instalado pelo GoblinShift. O `daemon.json` tem rotação de log, pools de
  endereço (`172.17.0.0/12` e `192.168.0.0/16`, blocos /24) e o runtime `runsc`
  (gVisor). Não tem `live-restore`.
- Borda em `/srv/edge`: `nginx:1.30.5` só na porta 80, IP fixo `172.31.0.2` na rede
  `edge` (`172.31.0.0/24`, IPs dinâmicos só em `172.31.0.128/25`), sem TLS.
- O GoblinShift ocupa `172.30.0.0/24`, exatamente a sub-rede que o
  `compose.prod.yaml` do StillOn fixa. O deploy do StillOn falharia ao criar a rede.
- A 443/tcp, liberada no ufw pelo `provision.sh`, é descartada de fora (timeout).
  Suspeita: firewall do painel da Hostinger.

## Objetivo e critérios de sucesso

Pôr o StillOn no ar em `https://stillon.tech` na mesma VPS do GoblinShift:

1. Um deploy de um projeto não derruba o outro.
2. As obrigações de hospedagem do Plano 11 continuam valendo e testadas no CI:
   - o IP do cliente que chega ao app é o real, e um cliente não consegue
     escolhê-lo forjando `X-Forwarded-For`;
   - uma requisição com `Host` estranho não chega ao app;
   - de fora, só 22, 80 e 443/tcp;
   - nenhum segredo fica com quem não precisa dele (o Caddy não vê segredo).
3. O app, o banco, o backup, o deploy pelo Actions e o usuário `deploy` não mudam
   de comportamento.

## Decisões do dono

- **A borda vira Caddy** (alternativas: nginx com certbot, VPS separada, tirar o
  GoblinShift). Motivo: certificado automático sem cron nem reload externos; o
  StillOn já usa e testa Caddy; o ADR 014 previa a opção.
- **O StillOn mantém o próprio Caddy atrás da borda** (alternativa: a borda falar
  direto com o app). Motivo: o app fica fora da rede compartilhada, a checagem de
  `Host` e a limpeza de cabeçalhos continuam no repositório do StillOn, e é o mesmo
  modelo do GoblinShift (o nginx dele fica atrás da borda). Custo: um contêiner e um
  salto a mais.

## Arquitetura

```
internet ──80/443──► borda: Caddy (/srv/edge, 172.31.0.2 na rede edge)
                     ├─ stillon.tech ──► stillon-caddy :80 (redes edge e stillon)
                     │                     └─► app :8080 ─► db  (rede stillon, 10.30.0.0/24)
                     └─ :80 por IP ────► goblinshift-web (como hoje)
```

### Borda (do host; código-fonte no repositório do GoblinShift)

- Imagem `caddy:2` fixada por digest, `restart: unless-stopped`, IP fixo
  `172.31.0.2`, publica `80:80` e `443:443` (só TCP). Volumes nomeados para
  `/data` (certificados e conta ACME) e `/config`.
- Caddyfile principal só com opções globais: `servers { protocols h1 h2 }` (a 443/udp
  não é publicada) e **sem `trusted_proxies`**, para a borda substituir o
  `X-Forwarded-For` do cliente pelo IP de quem conectou. Termina em
  `import conf.d/*.caddy`.
- Cada projeto entrega um trecho em `/srv/edge/conf.d/<projeto>.caddy` e nunca
  sobrescreve o de outro. O upstream é resolvido por nome na hora da conexão: um
  projeto parado não impede a borda de subir; as requisições dele recebem 502.
- Trecho do GoblinShift: `:80` (acesso pelo IP) para `goblinshift-web`, como hoje.
  Os cabeçalhos que o app dele lê (`X-Forwarded-For`, `X-Forwarded-Proto`) mantêm o
  significado: o Caddy sem proxies confiáveis põe o IP do cliente e o esquema real.
- Contrato que o StillOn assume, e que o teste do StillOn reproduz: rede externa
  `edge`, borda em `172.31.0.2`, sem `trusted_proxies`, HTTP/1.1 e HTTP/2, `Host`
  original preservado, `import conf.d/*.caddy`.

### Trecho do StillOn na borda (`deploy/vps/edge/stillon.caddy`)

`{$STILLON_DOMINIO}` não existe no ambiente da borda: o arquivo traz o domínio
literal `stillon.tech`, e o runbook o instala. Bloco de site para o domínio com
`reverse_proxy stillon-caddy:80`. TLS automático pelo Let's Encrypt; o redirecionamento
de HTTP para HTTPS é o padrão do Caddy.

### Caddy do StillOn

- Sem `ports` e sem TLS: `http://{$STILLON_DOMINIO}`. Um `Host` diferente não casa
  com nenhum bloco e não chega ao app.
- Opções globais: `trusted_proxies static 172.31.0.2/32`, `trusted_proxies_strict`
  e `client_ip_headers X-Forwarded-For`. O `{client_ip}` passa a ser o IP real do
  cliente quando a conexão vem da borda, e o IP da conexão em qualquer outro caso.
- `reverse_proxy app:8080` com `header_up X-Forwarded-For {client_ip}`. Sem isso, o
  Caddy acrescentaria o IP da borda ao fim da cadeia, e o app, que confia só no
  último salto, veria a borda no lugar de todo cliente. Continuam os
  `header_up -Fly-Client-IP`, `-X-Real-IP` e `-CF-Connecting-IP`.
- Redes: `edge` (externa, IP dinâmico, alias `stillon-caddy`) e `stillon`
  (IP fixo `10.30.0.2`). É o único contêiner do StillOn na `edge`.
- A opção `STILLON_CADDY_TESTE` (`local_certs`) deixa de existir no Caddy do
  StillOn, que não emite certificado; ela passa para a borda do teste.

### Rede `stillon`

`10.30.0.0/24`, gateway `10.30.0.1`, fixos `.2` (caddy), `.3` (app), `.4` (db),
dinâmicos em `10.30.0.128/25`. Fica fora dos pools do `daemon.json`, então nenhuma
rede criada por outro projeto pode tomá-la. `TRUSTED_PROXIES: 10.30.0.2/32` e o
`DATABASE_URL` com `10.30.0.4`. Antes da implementação, conferir `ip route` na VPS:
nenhuma rota pode cobrir `10.30.0.0/24` (em 2026-10-13, nenhuma cobria).

### Docker do host

- `live-restore: true` acrescentado ao `daemon.json` existente, preservando as
  chaves do GoblinShift, e aplicado com `systemctl reload docker` (a opção é
  recarregável por SIGHUP): nenhum contêiner reinicia.
- Image store do containerd: é o padrão do Docker 29 instalado do zero. O runbook
  deixa de exigir a linha `features` e só confere `docker info`. O `stillon-entrada`
  continua recusando o deploy sem o store do containerd.

## Mudanças por repositório

### GoblinShift (um PR)

- `deploy/edge/compose.yaml`: nginx → caddy por digest, volumes do Caddy, porta 443.
- `deploy/edge/Caddyfile` novo; `deploy/edge/conf.d/default.conf` →
  `deploy/edge/conf.d/goblinshift.caddy`.
- `deploy/provision.sh`: instala a borda nova; a regra de nunca sobrescrever trechos
  de outros projetos continua.
- ADR 014: Caddy escolhido, contrato da borda e o porquê.
- A alteração não comitada do `provision.sh` (gVisor) é de outra frente e não entra
  neste PR.

### StillOn (um PR)

- `deploy/vps/compose.prod.yaml`, `deploy/vps/Caddyfile`,
  `deploy/vps/edge/stillon.caddy` (novo).
- `deploy/ci/vps/compose.teste.yaml` e `deploy/ci/checar-vps.sh`: ver Testes.
- `docs/deploy/vps.md`: 3.1 vira conferência do Docker existente; 3.2 vira o
  acréscimo do `live-restore`; um passo novo instala o trecho na borda e recarrega
  (`caddy reload`); o bloco 9 passa a conferir pela borda; a tabela de cópias ganha
  o `stillon.caddy`.
- Digests: o `caddy:2` do StillOn continua no `compose.prod.yaml`, atualizado pelo
  Dependabot (`docker-compose` em `/deploy/vps`). A borda de teste do
  `checar-vps.sh` usa esse mesmo digest. O digest da borda real é do repositório do
  GoblinShift e do Dependabot dele; os dois podem divergir por algumas semanas sem
  quebrar o contrato.

## Testes

O `checar-vps.sh` sobe uma borda de teste igual ao contrato (Caddy com
`local_certs`, IP `172.31.0.2`, rede `edge` própria do teste, o `stillon.caddy` com o
domínio de teste) na frente do `compose.prod.yaml`. As portas do loopback passam do
Caddy do StillOn para a borda. Todas as checagens atuais de obrigação correm pela
borda, e entram três novas:

1. **XFF forjado:** um cliente que manda `X-Forwarded-For: 203.0.113.9` aparece para
   o app com o IP real dele (o da rede do teste), nunca com `203.0.113.9`.
2. **Acesso direto sem a borda:** de um contêiner na rede `edge` que não é a borda,
   uma requisição ao `stillon-caddy` com `X-Forwarded-For` forjado não muda o IP
   visto pelo app (o Caddy do StillOn só confia em `172.31.0.2`).
3. **Borda sem o StillOn:** com a pilha do StillOn parada, a borda continua de pé
   e responde 502 ao domínio.

A rede do teste usa a mesma `10.30.0.0/24` da produção, e o aviso de não rodar numa
máquina com o StillOn de produção continua valendo. Cada teste novo é visto falhando
com a mitigação sabotada (por exemplo, `trusted_proxies` amplo ou sem
`header_up X-Forwarded-For`) antes de ser dado como bom.

## Ordem na VPS

1. Conferir `ip route` (nenhuma rota em `10.30.0.0/24`) e `docker info` (store do
   containerd).
2. `live-restore` no `daemon.json`, com `systemctl reload docker`. Ninguém cai.
3. Troca da borda em `/srv/edge` pelos arquivos do PR do GoblinShift integrado.
   Única janela de queda: segundos, só do GoblinShift. Conferir o acesso pelo IP.
4. Firewall do painel da Hostinger: 443/tcp liberada. Conferir de fora.
5. Runbook do StillOn, do 3.3 em diante, com a `main` nova, guiado um passo e seu
   **Confira** por vez. O trecho vai para a borda depois que o Caddy do StillOn
   estiver na rede `edge`.
6. Bloco 9 conferido de fora.

## Riscos

- **Limite do Let's Encrypt:** tentativas de emissão falhas repetidas (443 bloqueada,
  DNS errado) contam no limite por hora. O passo 4 vem antes do trecho do StillOn
  entrar na borda.
- **A borda vira ponto único dos dois projetos.** Um erro no trecho de um projeto
  impede o `caddy reload`, mas o Caddy mantém a configuração anterior no ar. O runbook
  roda `caddy validate` antes do reload.
- **Contrato duplicado entre repositórios.** O StillOn testa contra uma borda
  equivalente, não contra a do GoblinShift. Se a borda real mudar o contrato, o teste
  não percebe. Mitigação: o contrato está escrito aqui e no ADR 014, e qualquer
  mudança nele é decisão do dono nos dois repositórios.

## Fora do escopo

Domínio próprio para o GoblinShift; IPv6; HTTP/3; métricas da borda; mover a borda
para um repositório próprio.
