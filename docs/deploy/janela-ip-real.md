# Janela do IP real: a parte do StillOn

Plano de operação. **Nada aqui foi executado.** Cada fase depende do "pode executar" do coordenador.

A janela é única para a VPS compartilhada, e a sessão do gateway a conduz. Os comandos do gateway
ficam no [ADR 041 do GoblinShift](https://github.com/BipoXDXD/GoblinShift/blob/main/docs/adr/041-ip-do-cliente-atras-da-cloudflare.md)
e no plano interno. Este arquivo é público: não cite endereço da origem, regra de rede nem segredo,
nem aqui nem no PR.

**O problema.** Hoje a borda manda ao StillOn o IP da Cloudflare. Login, ping, limite global e
`ip_hash` contam a borda, e não o cliente.

## Horários (UTC)

| Quando | O quê | Efeito no StillOn |
|---|---|---|
| Seg 09/11, 11:10 | Mini-janela de ensaio, cerca de 50 min (estimativa) | Nenhuma mudança e nenhuma publicação |
| Ter 10/11, 10:40 | Portão: conferências do go/no-go | Nenhum |
| Ter 10/11, 11:10 | Congelamento: sem merge na `main` dos três repositórios | Merge parado, inclusive de documentação |
| Ter 10/11, 11:20 | Fase 2: site do StillOn por `reload` | Sem queda esperada |
| Ter 10/11, 11:35 | Fase 3: o gateway é recriado | Queda de 4 a 6 s (estimativa) |
| Ter 10/11, 13:10 | Fim previsto do congelamento | Merge liberado |
| Qui 12/11 | Reserva, no mesmo horário | Repete a tabela |

Se a Fase 2 ou a Fase 3 falhar, a sessão para e as fases seguintes não acontecem.

## Condições antes de começar

- [ ] **GoblinShift#183 mesclado.** Em 2026-11-06 ele estava aberto e atrás da `main` de lá. O merge
  não publica o gateway.
- [ ] **Hash combinado com a sessão do gateway.** Veja [O que muda](#o-que-muda): o hash depende de
  o GoblinShift#187 entrar ou não.
- [ ] **Nada em andamento no StillOn.** `gh run list --workflow coolify.yml --status in_progress`
  vem vazio.
- [ ] **Último merge de código até 10:15 UTC.** Merge é deploy. Em 2026-11-06 o `ci` da `main` levou
  de 11 a 14 min, e o `Coolify release`, de 1 a 2 min, então um merge depois disso pode não terminar
  antes do portão. Vale o mesmo cuidado na mini-janela.
- [ ] **Linha de base anotada:** `/readyz` 200, `/healthz` 200 e `www` 308, com os comandos abaixo.
- [ ] **Digest do Caddy do StillOn parado.** `stillon-caddy-1` segue em `caddy:2@sha256:0c994536…`,
  a imagem antiga do gateway: é o que a protege da limpeza e garante a volta da Fase 3. Nenhum PR do
  StillOn troca esse digest antes de 24 h de gateway estável.
- [ ] **Sem redeploy do banco.** Nenhum passo recria `stillon-db-1`. Veja
  [`deploy/coolify/README.md`](../../deploy/coolify/README.md).

## O que muda

Só um arquivo, no repositório do GoblinShift: `docker/gateway/sites/stillon.tech.caddy`, publicado
por `caddy reload`. Os bytes vêm de
[`deploy/ci/vps/borda-cloudflare/sites/stillon.tech.caddy`](../../deploy/ci/vps/borda-cloudflare/sites/stillon.tech.caddy):

| Versão | sha256 | O que é |
|---|---|---|
| GoblinShift#183 | `de9c1a30362576e63769f4e49ee3f2c6f46d5ba80624863f2d06032a050d95e3` | Igual ao arquivo do StillOn e ao que o CI prova |
| GoblinShift#187, head `be304de` | `8fc99d3f3fbd5f7f0b667bd689f8059acbef5e4d763f20f0ee46cf3efdd1d143` | O anterior com duas linhas `tls`, em aberto |

Com a mudança, o `CF-Connecting-IP` só vale em conexão vinda das faixas da Cloudflare e com um único
endereço IPv4 ou IPv6, e não segue adiante. Fora disso, vale quem conectou.

**Não muda:** código, imagem e banco do StillOn, o `TRUSTED_PROXIES`, o Caddy do StillOn e o commit
fixo do recurso. Não há PR nem release do StillOn na janela. As sessões antigas guardam o `ip_hash`
que já tinham.

## Como conferir depois

Sem criar conta e sem credencial nova. Nada aqui guarda IP de cliente.

**1. Saúde, no Mac (prompt do Mac), um comando por vez:**

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://stillon.tech/readyz
```

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://stillon.tech/healthz
```

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://www.stillon.tech/
```

Esperado: `200`, `200` e `308`.

**2. Cabeçalho forjado, no Mac, 3 vezes:**

```sh
curl -sS -o /dev/null -w '%{http_code}\n' -H 'X-Forwarded-For: 198.51.100.8' -H 'CF-Connecting-IP: 198.51.100.9' https://stillon.tech/healthz
```

Esperado: `200`. Isso só mostra que o pedido passa. Quem prova o valor que chegou é a captura por
contagem da sessão do gateway: o IP do Mac aparece, e o forjado, nunca.

**3. 429 de login, no Mac.** Usa um e-mail que não existe, então nenhuma conta real é afetada. Repita
até o primeiro `429`, no máximo 8 vezes, sem paralelo:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' -d '{"email":"ninguem@stillon.example","password":"nao-e-senha-de-ninguem"}' https://stillon.tech/api/v1/auth/login
```

Esperado: `401` até o saldo acabar e `429` na sexta ou sétima tentativa. O limite é de 10 por minuto,
com 5 de uma vez. O balde enche em 30 s e é dividido com a troca de senha: espere isso antes de um
login de verdade dessa rede.

Numa segunda rede (por exemplo, o celular), logo em seguida, a primeira tentativa deve dar `401`. O
`401` sozinho não prova nada, porque duas redes também podem cair em bordas diferentes. Vale como
sinal contrário: `429` na outra rede aponta balde compartilhado.

**4. `ip_hash` nas sessões novas, na VPS (prompt da VPS).** Toda sessão nova grava o hash, inclusive a
rotação do refresh, então basta o dono usar o painel, com a conta que já existe. Troque a hora pela
da Fase 2:

```sh
docker exec stillon-db-1 psql -U postgres -d stillon -Atc "SELECT created_at, left(encode(ip_hash,'hex'),12) FROM sessions WHERE created_at >= '2026-11-10 11:20+00' ORDER BY created_at"
```

Esperado: da mesma rede e da mesma família de IP, o prefixo se repete entre as sessões, mesmo com a
Cloudflare trocando de borda. IPv4 e IPv6 do mesmo Mac dão hashes diferentes, e isso é normal. Uma
sessão só não prova nada. O hash não vai para texto público.

## O que observar

Nos 30 min depois de cada fase e numa leitura em 24 h.

| Sinal | Normal | Se não for |
|---|---|---|
| `/readyz` | `200` | Reverter, salvo se o erro vier do banco |
| Resposta pela Cloudflare | Sem 502, 525 nem 526 | Reverter |
| Queda medida na Fase 3 | Segundos | Mais de 60 s fora: reversão da Fase 3 |
| `ip_hash` nas sessões novas | Estável por rede | Parar e investigar: o IP real não chegou |
| 429 de login | Só para quem esgotou o próprio balde | Cliente sem tentativas recebendo 429: reverter |
| Ping de heartbeat | Sem 429 novo | Olhar antes de soltar o congelamento |
| `stillon-vigia` e healthchecks.io | Sem alerta novo | Conferir a linha `/healthz` antes de concluir |

## Reversão

No StillOn não há nada a reverter: nenhum arquivo, imagem ou dado mudou.

- **Quem faz:** a sessão do gateway, pela seção "Reversão" do ADR 041: restaura a cópia de `sites/`,
  repete `validate` e `reload`. Leva segundos, sem queda.
- **Quando:** 502, 525 ou 526 pela Cloudflare, `readyz` fora de 200 por causa da borda, `error` novo
  no gateway ou IP errado na captura.
- **`validate` falhou:** nada mudou. Pare a sessão.
- **Fase 3 revertida:** devolve os sites antigos. A Fase 2 tem de ser repetida, ou a sessão para.
- **Depois:** o app volta a contar pela borda. As sessões gravadas no intervalo ficam como estão, e os
  baldes são em memória. Não há limpeza de dados.

## Dependências levantadas

- **`stillon-vigia` passa pela Cloudflare.** Ele pede `https://<domínio>/healthz` com o certificado
  conferido. Um problema entre Cloudflare e origem aparece como queda, e um certificado que só valha
  atrás da Cloudflare falha para ele se o proxy for desligado. Não conferi nesta passada se o timer
  está ativo na VPS atual.
- **O release entra por SSH.** O `Coolify release` fala direto com a VPS e não passa pela Cloudflare.
  Nada da janela pode cortar esse caminho. A prova real é o primeiro release depois dela.
- **Certificado de origem.** Let's Encrypt, válido até 2027-01-12. A renovação está a definir: a
  proposta é o certificado de origem do GoblinShift#187 (ADR 043), ainda aberto, e o assunto tem de
  estar resolvido antes de 2026-12-12. Não conferi a data na origem. De fora só se vê o certificado da
  Cloudflare.
- **Cópia do site em dois repositórios.** Nada compara a fixture daqui com o arquivo do GoblinShift. Se
  o site mudar lá, a fixture acompanha, e o `checar-borda-cloudflare.sh` precisa continuar valendo.
- **Caddyfile do StillOn.** O CI prova o da `main`, e a produção usa o do commit fixo. O arquivo não
  muda desde `5018381`, então os dois devem coincidir; o commit fixo só se lê na VPS.
- **Backup diário.** Marcado para 06:17 UTC, mas o horário deriva (rodou às 14:23 em 2026-11-06) e pode
  cair na janela. Ele usa SSH, não a borda.

## Sem prova até a produção

O `checar-borda-cloudflare.sh` roda a cadeia em contêineres, com um cliente que imita o Traefik. Ficam
para a janela: a Cloudflare e o Traefik de verdade, HTTP/2 entre eles, IPv6 real no limite do app e uma
zona que remova o cabeçalho do visitante. Nesse último caso o site continua no ar e o app segue
contando a borda.
