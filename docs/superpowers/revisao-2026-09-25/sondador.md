# Auditoria: sondador, safedial, notificações, alvos e incidentes

Repositório: `/home/bipo/Projetos/Go/Vigilume` @ `640cd9e` (main). Auditoria somente leitura. Rodei
`go test ./internal/prober/safedial/` (verde) e um experimento isolado no scratchpad
(`scratchpad/hdr/main.go`, fora do repositório) para medir a memória do achado S1.

Contexto de produção considerado: VPS pública, Docker compose com Caddy + app (`--role=all`) + Postgres
numa rede interna; metadados em 169.254.169.254; IPs privados e loopback na VPS.

## Resumo

| # | Sev. | Área | Título |
|---|---|---|---|
| S1 | **Alto** (Médio se o dono for o único usuário) | Segurança/DoS | Sem teto para o tamanho dos cabeçalhos da resposta: 64 sondas × 10 MiB derrubam o processo inteiro |
| S2 | **Médio** | Segurança/DoS | Sondador sem cota por usuário: uma conta ocupa as 64 vagas globais; algumas contas desligam a detecção de incidentes para todo mundo |
| S3 | **Médio** | Segurança/DoS | Despacho sem cota por usuário: teto global de 4 avisos/s em FIFO; uma conta atrasa os alertas de todos e gasta a cota do Resend do operador |
| S4 | Baixo | Segurança/SSRF | O IP público da própria VPS (e IPv6 global da rede Docker, se ligado) não está bloqueado: acesso por hairpin, que passa por fora do firewall externo da Hostinger |
| S5 | Baixo | Segurança/info | `blocked` contra `dns` revela os nomes internos da rede Docker (`db`, `caddy`, `app`) |
| C1 | Baixo | Correção | `expected_status` 1xx/3xx é aceito, mas não tem como bater: o alvo fica `down` para sempre |
| C2 | Baixo | Correção | Mais de 3 redirecionamentos, ou um redirecionamento para um esquema que não é http(s), vira `blocked` ("destino bloqueado pela proteção de rede") |
| C3 | Baixo | Correção | Trocar a URL/método de um alvo (PATCH) não zera a histerese nem o incidente: o alvo novo herda falhas e `started_at` do antigo |

Total: **0 Crítico, 1 Alto, 2 Médio, 5 Baixo**. Segurança: 1 Alto, 2 Médio e 2 Baixo (S4, S5).
Correção: 3 Baixo (C1, C2, C3).

---

# Segurança

## S1. Alto. Resposta com 10 MiB de cabeçalhos × 64 sondas: memória e OOM (a API cai junto)

**Onde:** `internal/prober/safedial/transport.go:203-231` (o `http.Transport` não define
`MaxResponseHeaderBytes`); `internal/prober/scheduler.go:30` (`MaxInFlight = 64`);
`internal/prober/probe.go:89-96` (a resposta fica viva enquanto o corpo é lido, até o timeout).

**Cenário:** um usuário autenticado cadastra 64 alvos (o limite é 100 por usuário) apontando para um
servidor dele que responde `HTTP/1.1 200` seguido de cerca de 10 MiB de cabeçalhos (`X-H1: aaaa…`,
`X-H2: …`) e depois manda o corpo a conta-gotas até perto do `timeout_ms` (26 999 ms com intervalo de
30 s). Sem `MaxResponseHeaderBytes`, o `net/http` usa o padrão de **10 MiB** por resposta
(`$GOROOT/src/net/http/transport.go:329-333`: `return 10 << 20`, que vale também para o HTTP/2). Medi num
programa isolado (Go 1.26.8, o mesmo `DisableKeepAlives`, 64 requisições simultâneas, cada resposta
com cerca de 10 MiB de cabeçalho): **HeapInuse = 660 MiB e Sys = 843 MiB** depois do GC, com as
respostas ainda vivas. Isso se repete a cada ~30 s. Com `--role=all`, API, sondador e despacho estão no
mesmo processo. Numa VPS pequena, com o Postgres e o Caddy dividindo a RAM, o OOM killer mata o app
(ou o Postgres). Mesmo sem OOM, a pressão de GC pesa na latência da API e distorce a latência medida
de todos os alvos (vira `degraded`).

**Por que as defesas não bastam:** `MaxBodyBytes` (64 KiB) limita só o corpo. O semáforo limita o
número de sondas, mas não os bytes de cada uma. O timeout só encerra a sonda depois que o dano já
aconteceu. Não há `GOMEMLIMIT` nem limite de memória no compose.

**Refutação tentada:** o HTTP/2 tem limite próprio? Não: o `http2` do `net/http` herda
`t1.MaxResponseHeaderBytes`, com o mesmo padrão de 10 MiB. A compressão HPACK torna esse caminho
mais barato para o atacante, não mais caro.

**Correção:** em `newTransport`, `MaxResponseHeaderBytes: 64 << 10` (ou 256 KiB; nenhum alvo legítimo
de monitoramento precisa de mais). Isso vale para o Discord e o Resend, que usam o mesmo transporte.
Um cabeçalho acima do limite vira erro, que cai no `KindUnknown` (ou crie um tipo próprio). Como
defesa em profundidade, `GOMEMLIMIT` e `mem_limit` no compose de produção. Teste: um servidor que
manda 1 MiB de cabeçalho e a sonda sai `down/unknown` sem alocar o total.

## S2. Médio. O sondador não tem cota por usuário: uma conta ocupa as 64 vagas globais e algumas contas desligam a detecção para todos

**Onde:** `internal/prober/scheduler.go:28-30,158-203` (um semáforo global de 64, sem noção de
dono); `internal/storage/queries/prober.sql:14-25` (a reivindicação é FIFO por `next_run_at`, sem
justiça por usuário); `internal/targets/validate.go:112-120` (permite `timeout_ms` até 0,9 × o intervalo);
`internal/prober/scheduler.go:418-444` (a regra de lacuna).

**Cenário:** um usuário cadastra 100 alvos com intervalo de 30 s e `timeout_ms = 26999`, apontando
para um IP público que descarta SYN (ou para um servidor que manda o corpo a conta-gotas). Cada sonda
ocupa uma vaga por cerca de 27 s. A demanda dele é 100 × 27/30 = **90 vagas**, e o teto global é 64.
Como a fila é FIFO global, cada rodada precisa de pelo menos 100 × 27 / 64 ≈ 42 s: **todos** os alvos
de todos os usuários passam a ser sondados a cada ~42 s, e não a cada 30 s. Com 3 contas assim, a
rodada vai a ~127 s, acima de `fatorLacuna × intervalo` = 90 s. Aí `recomecarAposLacuna` zera os
contadores **antes de cada avaliação** para todo alvo de 30 s, e nenhum incidente abre para ninguém.
Os alvos de 60 s caem com ~5 contas. É uma falha silenciosa: o painel mostra checks, mas o alerta
nunca vem. `queue_depth` sobe, mas nada age sobre ela.

**Por que as defesas não bastam:** o `MaxTargetsPerUser = 100` limita a contagem, e não o tempo de
vaga consumido. A regra D3 (timeout < 0,9 × intervalo) impede duas sondas do mesmo alvo, mas deixa
cada alvo ocupar 90% de uma vaga. O `RateLimitUser` vale só para a API. A regra de lacuna foi
desenhada para o sondador parado, e aqui ela transforma lentidão em cegueira.

**Refutação tentada:** num deploy só do dono, o impacto é autoinfligido (por isso Médio, e não Alto).
As contas são criadas pelo CLI, não há cadastro aberto. Mesmo assim, uma única conta já atrasa em ~40%
as sondas das outras.

**Correção:** (a) uma cota de vaga-segundo por usuário: por exemplo, a soma de `timeout_ms/interval_sec`
por usuário ≤ 20, conferida no cadastro sob a mesma trava do teto de alvos; ou (b) um teto de sondas em
voo por usuário no `Tick`, com a reivindicação ordenada por `next_run_at` e um corte por `user_id`,
usando `row_number() OVER (PARTITION BY user_id)`; e (c) a regra de lacuna usar o atraso relativo à
agenda (`checkedAt - next_run_at` previsto) e não a distância bruta entre checks, para que a lentidão
do sondador não zere a histerese. Alarme em `queue_depth > 0` sustentado.

## S3. Médio. O despacho não tem cota por usuário: teto global de ~4 avisos/s em FIFO, e a cota do Resend é do operador

**Onde:** `internal/notify/dispatcher.go:24-28,89-130` (lote de 8 a cada tick de 2 s, e `Dispatch`
espera o lote terminar: **no máximo 4 notificações/s no processo**, contando canceladas);
`internal/storage/queries/notifications.sql:7-17` (`ORDER BY next_attempt_at`, FIFO global);
`internal/prober/scheduler.go:453-474` (fan-out: uma notificação por canal, com até 10 canais);
`internal/notify/resend.go:141-156` (a chave e a cota são do operador).

**Cenário:** um usuário com 100 alvos apontando para um servidor dele que alterna entre 3 respostas 500
e 5 respostas 200 (o ciclo mínimo abre-fecha: 8 checks × ~27 s ≈ 216 s) e 10 canais verificados (10
webhooks num servidor Discord dele, ou 10 caixas de e-mail dele) gera 100 × 2 eventos × 10 canais por
216 s ≈ **9,3 notificações/s**, mais que o dobro da vazão global. O atraso cresce ~5 notificações a cada
segundo. Depois de 1 h de ataque há cerca de 19 000 na frente, e o alerta de queda de **outro** usuário,
enfileirado com `next_attempt_at = now`, espera a fila inteira (mais de 1 h). As aberturas ficam velhas e
são canceladas (`semEnvio`), mas o cancelamento também ocupa vaga no lote de 8. Com canais de e-mail,
cada envio é um e-mail real pela conta Resend do operador. O atacante esgota a cota diária/mensal e o
limite por segundo da conta (que o próprio código trata como compartilhado, `dispatcher.go:27`). Os 429
e as recusas por cota passam a atingir os e-mails de todos os usuários, e depois de `rateLimitGrace`
(1 h) viram falha definitiva. Há ainda o custo financeiro e a reputação do domínio remetente. Mesmo sem
malícia, um apagão legítimo de 100 alvos × 10 canais (1000 avisos) leva mais de 4 min para escoar e
atrasa os outros donos.

Acessório: os e-mails de verificação (`POST /channels` e `/verify/resend`) vão a **qualquer**
endereço de terceiro, mas com texto fixo e no ritmo de 5 de uma vez e depois 1 a cada 6 min por usuário
(`internal/httpapi/routes.go:31-32,150-158`). São ~240 por dia por conta, o que basta sozinho para
estourar um plano gratuito do Resend.

**Por que as defesas não bastam:** `MaxChannels = 10` e `MaxTargetsPerUser = 100` limitam cadastros, e
não a taxa de eventos. A serialização por canal protege o Discord de 429, mas não protege a fila global.
O `UNIQUE (incident, channel, event)` só deduplica o mesmo incidente.

**Refutação tentada:** o PENDENCIAS-PARA-O-PLANO-4 registra o "apagão em massa" do ponto de vista dos
429 do Discord do mesmo dono (resolvido pelo perdão de 429). Não registra a vazão global nem a
starvation entre donos, nem a cota do Resend. É pior do que o documentado.

**Correção:** (a) reivindicar com justiça por dono (por exemplo, no máximo 2 por `user_id` por lote,
com `row_number() OVER (PARTITION BY c.user_id ORDER BY next_attempt_at)`), ou ordenar por "último envio
do dono"; (b) cancelar ou agregar em massa antes de enviar: um resumo "N alvos caíram" por dono e por
janela, ou no máximo X avisos por dono por hora, com os demais em `cancelled/throttled`; (c) um cooldown
de flapping no detector (não reabrir um incidente do mesmo alvo em menos de N min, ou marcar
`flapping`); (d) tirar o `wg.Wait()` do caminho ou despachar em ticks sem esperar o lote, para a vazão
não ficar presa em 4/s; (e) um teto diário de e-mails por usuário, contado na tabela `notifications`.

## S4. Baixo. O IP público da VPS (e o IPv6 global da rede Docker) não está na lista; o hairpin passa por fora do firewall externo

**Onde:** `internal/prober/safedial/safedial.go:28-78` (a lista é estática, só com faixas especiais);
não há como o operador acrescentar faixas.

**Cenário:** a VPS tem IP público `203.x.y.z`. O operador protege serviços do host com o firewall do
painel da Hostinger (externo à VM): por exemplo, uma porta do Docker publicada como `"5432:5432"` ou
`"9090:9090"` (sem `127.0.0.1:`), um exporter, ou a API do Docker em `tcp://0.0.0.0:2375`. Um usuário
cadastra `http://203.x.y.z:2375/…`. A conexão sai do contêiner pela bridge e volta ao próprio host (via
hairpin, DNAT do Docker ou docker-proxy) **sem passar pelo firewall externo**, e o safedial deixa passar,
porque o endereço é público. O retorno é um oráculo de varredura de portas (`conn_refused`, `timeout`,
`unknown`, `http_status`, com o código e a latência), e o atacante tem GET/POST sem corpo às cegas. Contra
a API do Docker, `POST /containers/<id>/kill` não precisa de corpo. O mesmo vale se o dono ligar IPv6 na
rede do compose com um prefixo **global** roteado (a forma recomendada de dar IPv6 aos contêineres): `db`
e `caddy` ganham endereços fora de `fc00::/7`, e o safedial não os bloqueia.

**O que não é alcançável (verificado):** `db:5432`, `caddy`, `app` pelo nome (resolvem para
172.16/12 ou 10/8, bloqueados no `ControlContext`), `169.254.169.254`, `::1`, `fc00::/7`, `fe80::/10`,
`0.0.0.0`, formas mapeadas, compatíveis, NAT64, 6to4 e Teredo, zonas IPv6, e o admin do Caddy em
`localhost:2019` (fica no netns do contêiner do Caddy, e o loopback é bloqueado). O Caddy público (80/443)
é alcançável, mas é o mesmo que qualquer pessoa na internet alcança.

**Por que é só Baixo:** depende de uma configuração do operador (uma porta publicada no 0.0.0.0
contando com o firewall externo). Não há exfiltração de corpo, que é descartado (`probe.go:96`); a
tabela `checks` só guarda status, latência, código e `error_kind`.

**Correção:** uma variável `PROBE_DENY_CIDRS` (lista extra, validada no boot, somada a
`blockedPrefixes`) documentada no runbook da VPS com os IPs públicos v4 e v6 da máquina e o prefixo IPv6
da rede Docker; no compose de produção, nenhum `ports:` além do Caddy; o firewall do host na chain
`DOCKER-USER`/`INPUT` (e não só o do painel).

## S5. Baixo. `blocked` contra `dns` enumera os nomes internos

**Onde:** `internal/prober/classify.go:152-165`; o `error_kind` aparece na API de checks e no alerta.

**Cenário:** `http://db/` → `blocked` (resolveu, e o IP é interno); `http://dbx/` → `dns`. O usuário
descobre quais nomes existem na rede Docker e no resolvedor do host (`db`, `caddy`, `app`,
`host.docker.internal`, nomes do search domain da Hostinger). É só informação, sem acesso: por isso Baixo.

**Correção:** aceitável como está. Se quiser fechar, recuse no cadastro hostnames sem ponto (nomes de
rótulo único não são alvos públicos legítimos) e trate o `blocked` pós-DNS igual a `dns` na mensagem ao
usuário.

---

# Correção

## C1. Baixo. `expected_status` 1xx/3xx é aceito, mas nunca bate: `down` permanente e incidente falso

**Onde:** `internal/targets/validate.go:122-124` (aceita de 100 a 599); `safedial/transport.go:277-299`
(o cliente segue até 3 redirecionamentos, então o status final nunca é 3xx, a menos que a cadeia passe
do limite, e aí é erro); o `net/http` consome 1xx internamente. `probe.go:102`.

**Cenário:** o dono monitora `http://site.com/` com `expected_status=301`, para garantir o redirect para
https. A sonda segue o 301 e recebe 200 ≠ 301, então `down/http_status`. Três checks depois abre um
incidente, e ele nunca fecha. O mesmo acontece com 302, 307 e 308, e com qualquer 1xx.

**Correção:** recusar 1xx e 3xx no `validate` (`out_of_range`), ou, se o monitoramento de redirect for
desejado, um campo `follow_redirects=false` que faz o `CheckRedirect` devolver
`http.ErrUseLastResponse`.

## C2. Baixo. Excesso de redirecionamentos, ou um esquema inválido no Location, vira "bloqueado pela proteção de rede"

**Onde:** `safedial/transport.go:285-293` (embrulha em `ErrBlocked`/`ErrInvalidURL`);
`prober/classify.go:153-154` (qualquer um dos dois vira `KindBlocked`); `notify/message.go:246`.

**Cenário:** um site legítimo com 4 saltos (`http→https→www→/pt/→/pt/home`), ou que redireciona para
`ftp://` ou `mailto:`, gera o alerta "Causa: destino bloqueado pela proteção de rede". O dono vai procurar
um problema de SSRF que não existe. A classificação também impede diferenciar a tentativa de SSRF real
de um erro de configuração do site.

**Correção:** um erro próprio (`ErrTooManyRedirects`, `ErrRedirectScheme`) classificado como `unknown`
ou como um tipo novo (`redirect`, que exige migration no CHECK), e manter `blocked` só para o IP
recusado no dial.

## C3. Baixo. PATCH de URL/método não zera a histerese nem o incidente aberto

**Onde:** `internal/targets/service.go:139-188` (`mudouASonda` só reagenda `next_run_at`);
`internal/prober/scheduler.go:348-416` (lê `target_health` e o incidente aberto sem saber da troca).

**Cenário:** o alvo aponta para `https://a.exemplo` e tem 2 falhas seguidas (StreakSince = 10:00). O
dono corrige a URL para `https://b.exemplo`, que dá um erro passageiro na primeira sonda. A terceira
falha "consecutiva" abre um incidente com `started_at = 10:00` (da URL antiga), e o alerta sai para um
alvo que falhou uma vez só. No caminho inverso, um incidente aberto pela URL antiga é "resolvido" por 5
sucessos da URL nova, com o texto "voltou depois de X fora do ar", misturando dois serviços.

**Correção:** quando `URL` ou `Method` mudar no `Update`, na mesma transação: zerar `target_health`
(contadores e `StreakSince`) e, havendo incidente aberto, resolvê-lo com `resolved_at = now`. Decidir
também se o fechamento notifica ou é silencioso (registrar a decisão).

---

# O que verifiquei e não virou achado

- **DNS rebinding e TOCTOU:** a validação é no `ControlContext`, sobre o `IP:porta` que vai ser
  conectado. `DisableKeepAlives` impede reusar uma conexão validada antes. O `DialContext` é usado também
  para o TLS e o HTTP/2. A resolução do resolvedor (127.0.0.11) não passa pelo dialer, e não precisa.
- **IPv6, mapeados e embutidos:** `::ffff:`, `::/96` (compatível), `::ffff:0:0:0/96` (SIIT), NAT64
  (`64:ff9b::/96`, `/48`), 6to4, Teredo, zona recusada antes do `Unmap` (a ordem está certa). Notações
  octal e decimal não importam, porque a checagem é sobre o IP resolvido. Host com `%31%32%37…` é
  desescapado pelo `url.Parse` e recusado.
- **Redirecionamentos:** até 3, o esquema é revalidado a cada salto e o IP no dial. Os headers do alvo
  (e o Referer) são limpos na troca de host:porta e no rebaixamento https→http, e a limpeza olha a cadeia
  inteira (o `net/http` recopia os headers da requisição original). Não achei furo.
- **Proxy de ambiente:** `Proxy: nil` (`TestTransportIgnoresEnvironmentProxy`). O `ALL_PROXY` não é lido
  pelo `net/http`.
- **Esquemas e credenciais:** só `http`/`https`, e `user:pass@` é recusado. Não há restrição de porta,
  e isso é aceitável para um monitor. Um ataque cross-protocol (por exemplo, SMTP) não é viável: o nome
  de header só aceita token, sem espaço, e o valor não aceita CR/LF.
- **Headers customizados:** nome token (RFC 9110), valor sem CR/LF/controles, `Host`, framing e
  hop-by-hop proibidos. O `net/http` ignora `Header["Host"]` de qualquer jeito. Os valores são cifrados com
  AAD = id do alvo, e o `LogValue` não mostra os valores.
- **Exfiltração:** o corpo é lido até 64 KiB e descartado. `checks` guarda status, latência, código e
  `error_kind`, e nenhuma mensagem crua vai para o banco, o log ou o alerta (`causas` é um mapa fechado,
  e o `SendError` não carrega a URL do webhook).
- **Webhook do Discord:** allowlist de host exata (sem porta, sem ponto final, sem userinfo), regex de
  path sobre `EscapedPath` sem `%`, revalidação a cada envio, o mesmo cliente safedial.
  `allowed_mentions.parse=[]` e `SUPPRESS_EMBEDS`. O texto é do próprio dono do canal (não há injeção
  entre usuários).
- **Resend:** endpoint fixo; o destinatário passa por `mail.ParseAddress` com `Address == entrada` (sem
  display name, sem vírgula ou lista); corpo `text` (não HTML); o nome do alvo não aceita controles; o
  JSON é montado pelo `encoding/json`. Não vi injeção de header ou HTML.
- **Reivindicação duplicada de alvos:** `FOR UPDATE SKIP LOCKED` mais o corte `len(alvos) > vagas`.
  Uma falha no claim devolve todas as vagas. Um panic na sonda devolve a vaga.
- **Histerese:** o check fora de ordem é ignorado, a lacuna é tratada, `ActionClose` só com
  `temAberto`, `target_health` travada antes de ler o incidente, e check e transição na mesma transação.
- **Outbox:** o lease (180 s) cobre o pior lote serial (8 × 15 s). Abertura velha cancelada, canal
  pausado ou não verificado cancelado no envio, perdão de 429 por 1 h, e as tentativas contadas no claim
  com devolução.
- **Partições de checks:** as bordas são calculadas em UTC dos dois lados (Go e SQL).

Limites já documentados e não repetidos: M4 (duas sondas em voo), headers que não seguem redirect,
renomear alvo entre tentativas (409 do Resend), `incidents_open` global, reenvio do Discord depois do
lease, pausa que descarta avisos, notificação reivindicada logo antes de uma pausa.
