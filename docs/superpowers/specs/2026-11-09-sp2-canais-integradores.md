# SP2: modelo de canal e 5 primeiros integradores

**Estado:** rascunho para aprovação do dono, 2026-11-09, ajustado pela revisão independente (`revisao-SP1-SP2.md`). Nada foi
implementado, nenhum teste rodou e o repositório não foi alterado. Base: `origin/main` em `d177e72`, só leitura.

**Decisões em aberto:** o texto segue as recomendações da §14 só para ter forma concreta. Trecho que depende de uma resposta leva
**[se D-n=x]**, e a §3 diz o que muda em cada alternativa. Nenhuma decisão foi presumida.

**O que não foi verificado** (detalhe na §16): nenhum custo foi medido; o `Down` proposto não foi executado; os limites do
ntfy.sh e o endpoint europeu do PagerDuty não foram confirmados; os quatro arquivos de `~/.claude/knowledge` foram lidos nas
seções pertinentes, não inteiros. Itens de terceiros sem confirmação levam **[não verificado]**.

## 1. Objetivo e fora de escopo

**Objetivo:** tornar o canal de aviso extensível (D3 da matriz) e entregar webhook genérico, Slack, Telegram, PagerDuty e ntfy,
com troca de configuração, teste sob demanda e histórico de envios.

**Da matriz, neste pacote:** D3, D4 (alcance em D-4), D5 (webhook), D10 (forma em D-3), D13 (só ntfy), D15, D26, D27.

**Fora de escopo:** outros integradores; OAuth do Slack e do Discord; SMS e telefone; Shell Command e Apprise; grupo de canais
(D23); relatórios e fuso (D30, A6); aviso de flapping (D31); bounces (D32); reconhecer pelo aviso; propagar o ack do StillOn ao
PagerDuty. Canal por monitor (D25) depende de D-5; pausa automática do canal, de D-9.

## 2. O que existe hoje

| Tema | Comportamento lido em `origin/main` |
|---|---|
| Contrato | `Notifier.Send(ctx, dest crypto.Secret, ev Event)`; `dest` é um valor só, URL ou e-mail |
| Cifragem | `config_enc`, AES-256-GCM, AAD `notif_channels:config_enc:<id>`. `Box` tem uma chave só e formato `nonce‖ct‖tag`, sem identificador de chave |
| Tipos | CHECK `kind IN ('discord','email')`. O tipo se repete em `validar`, `hint`, `Create`, `Policy.cadences` e no `z.enum` do painel (Repeated Switches) |
| Despacho | Outbox, `SKIP LOCKED`, lote de 8, tick de 2 s, `sendTimeout` de 10 s, lease de 3 min. `Dispatch` espera o lote inteiro |
| Retry | 6 tentativas, backoff de 30 s dobrando, **sem jitter**. 429 não gasta tentativa por 1 h. `Retry-After` só do cabeçalho, teto de 1 h. 4xx é permanente |
| Por canal | Em série dentro do lote. Falha transitória adia o resto; envio **lento e bem-sucedido** não adia |
| `failing` | Derivado de `channel_health`: última falha definitiva mais nova que o último sucesso. O canal continua recebendo |
| Limites | `MaxChannels = 10`. Balde de envio por usuário: 1 a cada 6 min, burst 5, em criar e reenviar |
| RLS | Dono por `user_id`; sondador só com SELECT em `notif_channels`; retenção de `notifications`: 30 dias |
| `safedial` | IP validado no dial, sem proxy, sem keep-alive, 64 KiB de cabeçalho, **segue até 3 redirects** |

### Pontos que quebram com um `kind` desconhecido

| Ponto | Efeito |
|---|---|
| `Reminders.enqueueDue` | `NextReminder` devolve erro e aborta a página inteira. Lembretes de outros canais param enquanto o par estiver na página |
| `z.enum(['discord','email'])` no painel | Provável erro de parse em toda a lista (inferido da leitura) |
| Dispatcher | `failed/invalid_channel` definitivo, canal marcado `failing` e auditoria. **O aviso se perde:** quando a imagem nova volta, a linha já fechou (§6, "Rollback de tipo novo") |

Rollback é reimplantar a imagem anterior com o schema novo. Por isso o PR 1 (§15) torna esses pontos tolerantes e precisa estar
publicado antes de existir qualquer linha de tipo novo. A proteção só alcança rollback até uma imagem que já contenha o PR 1.

## 3. Modelo de canal

| | A. Colunas ou tabela por integrador | B. JSON selado em `config_enc`, tipo Go por integrador | C. Híbrido: público em `jsonb`, segredo selado |
|---|---|---|---|
| Prós | O banco valida a forma; consulta direta | Sem tabela nem política RLS nova; `discord` e `email` seguem com o valor cru, sem recifrar; o binário antigo continua lendo os dois | Exibe e filtra sem decifrar |
| Contras | Uma migration e uma política por integrador, até 27 tipos; mais linhas fora do AAD | O banco não valida o conteúdo; o parser tolera versões antigas | Dois lugares por canal; risco de segredo cair no lado público (o tópico do ntfy funciona como senha) |
| Rollback | Tabelas novas ignoradas | Sem mudança de formato nos tipos antigos | Coluna nova ignorada |

**Recomendação: B.** Nada precisa consultar a configuração: a listagem já decifra cada linha para o `hint`, com teto de
`MaxChannels`. **Custo principal:** a validação fica só no código, então o parse é estrito na API e repetido no envio, como o
Discord já faz.

### Premissas que dependem do dono

| Decisão | Trechos escritos com a recomendação | Se a resposta for outra |
|---|---|---|
| D-1=B | "Forma proposta" abaixo; coluna "Configuração selada" da §4; §5; `config_size` da §8; `Parse` e `PUT .../config` da §9 | **A:** colunas ou tabela por integrador, política RLS por tabela nova, e reescrita de §§5, 8 e 9. **C:** parte pública em `jsonb`, e a §5 passa a dizer o que é público. Nos dois casos, reabrir essas seções antes do PR 2 |
| D-3=a | Telegram nas §§4, 5, 9 e 10 | **b:** sai `bot_token` por canal; entram segredo do operador, rota pública de vínculo e ameaças novas na §12. O PR 8 volta à revisão |
| D-4=a | Linha Webhook dos contratos da §4; envelope e assinatura (§6); cabeçalhos reservados (§7); rotação (§§5 e 9); testes de assinatura (§§11 e 12) | **b:** o corpo vira modelo do dono, com escape por tipo de conteúdo. **c:** mudam nome e formato dos cabeçalhos. O PR 6 acompanha |
| D-7 | Cadência por tipo; PagerDuty sem lembrete (§4 e `TestPagerDutySemLembrete`) | Muda só o valor em `Policy` e os testes de cadência |

Forma proposta **[se D-1=B]**:

- `Notifier` mantém a assinatura; `dest` passa a significar "configuração decifrada". É interface legítima, com vários implementadores reais.
- `map[Kind]Integration` com **funções**, sem classes novas: `Parse` (entrada da API para JSON canônico), `Hint`, modo de verificação (`link` ou `mensagem`), cadência (`nil` = sem lembrete), raia e se o tipo recebe `tls_expiring` (D-11).
- `discord` e `email`: valor cru para sempre. Tipos novos: JSON com `{"v":1,...}`. Nenhuma migração de dados.
- `Event` ganha `IncidentID` (anulável: a reivindicação já o traz, e aviso sem incidente não tem), `SubjectKind` (`target` ou `heartbeat`) e, só em aviso de certificado, `TLS` (`NotAfter`, `StageDays`; mais o id do alerta se D-11=a). Contrato na §6.
- `DiscordTester` vira `ChannelTester`, indexado por tipo; `*Resend` ganha mensagem de teste sem link (§9).
- Coluna nova `name` (rótulo do dono), porque o `hint` não distingue dez canais.

## 4. Os 5 primeiros, na ordem da matriz

A ordem vem da `paridade-healthchecks.md`, §5: modelo de canal, webhook, Slack, Telegram, PagerDuty, ntfy.

| # | Tipo | Por quê | Configuração selada | Destino e raia |
|---|---|---|---|---|
| 1 | Webhook | Cobre o que não tem integrador e força SSRF e assinatura primeiro | `url`, `headers`, segredo de assinatura **[se D-4=a]** | URL do usuário; raia lenta |
| 2 | Slack | Base de equipes, esforço P | `url` | Somente `hooks.slack.com`; raia confiável |
| 3 | Telegram | Aviso no celular sem conta extra | `bot_token`, `chat_id` **[se D-3=a]** | Somente `api.telegram.org`; raia confiável |
| 4 | PagerDuty | Plantão com escalonamento | `routing_key` | Somente `events.pagerduty.com`; raia confiável |
| 5 | ntfy | Push sem conta; aceita servidor próprio | `base_url`, `topic`, `token` opcional | `ntfy.sh` ou URL do usuário; raia lenta (a raia sai do tipo, que é o que a reivindicação enxerga) |

### Contratos por integrador (documentação oficial lida em 2026-11-09)

| Tipo | Envio | Sucesso | Permanente | Transitório | Duplicata no retry |
|---|---|---|---|---|---|
| Webhook **[se D-4=a]** | POST de envelope JSON v1 (§6) | 2xx | Outro 4xx, 3xx | 429, 5xx, rede, timeout | Receptor deduplica por `webhook-id` |
| Slack | POST `{"text":…,"mrkdwn":false}` | 200 | 4xx, como canal arquivado ou webhook removido | 429 **[não verificado na página lida]**, 5xx | Possível, como no Discord |
| Telegram | POST `/bot<token>/sendMessage`, sem `parse_mode`, sem prévia de link | 200 | 400, 401, 403 | 429 com `parameters.retry_after` **no corpo**, 5xx | Possível: a API não tem chave |
| PagerDuty | POST `/v2/enqueue`: `trigger` e `resolve`, `severity=critical` | 202 | 400 | 429, 5xx; a documentação sugere espera de minutos | `dedup_key = stillon:<incident_id>`, só em evento de incidente (`tls_expiring`: D-11) |
| ntfy | POST JSON na raiz com `topic`, `title`, `message`, `priority` | 2xx | Outro 4xx, 3xx | 429, 5xx | Possível: não há chave |

Regras específicas:

- **Texto é entrada do usuário.** Slack: escapar `&`, `<`, `>` **e** usar `mrkdwn:false`, para `<!channel>` no nome do alvo sair literal. Telegram: texto puro, teto de 4096 caracteres. ntfy: JSON em vez de cabeçalho, teto de 4096 bytes. PagerDuty: `summary` cortado em 1024 caracteres.
- **Telegram:** o `retryAfter` atual só lê cabeçalho; este tipo lê o corpo, limitado a 4 KiB e com o mesmo teto de 1 h. O token vai no caminho da URL, e `SendError` já não carrega a causa.
- **PagerDuty:** sem lembretes **[se D-7 aceitar]**, pois o serviço escala sozinho e um `trigger` repetido só acrescenta registro. Um `resolve` de chave desconhecida é descartado sem erro. Depois de resolvido, nova abertura usa outro `incident_id`, então a chave nunca colide.
- **PagerDuty e `send_up=false`:** nenhum `resolve` sai, e o incidente fica aberto lá até alguém fechá-lo. A regra depende de D-12.
- **ntfy.sh:** o limite por IP seria compartilhado por toda a VPS **[não verificado]**.

## 5. Segredos, cifragem e rotação

- Todo segredo fica em `config_enc`, selado com o `Box` e o AAD atual. A API devolve só `hint`.

| Tipo | `hint` |
|---|---|
| Webhook | Host |
| Slack | "webhook …" + 4 últimos caracteres do penúltimo segmento |
| Telegram | "chat …" + 4 últimos dígitos |
| PagerDuty | "chave …" + 4 últimos caracteres |
| ntfy | Host, nunca o tópico |

**Segredo de assinatura do webhook:** gerado pelo StillOn, 32 bytes, formato `whsec_` + base64, mostrado uma única vez.

**Rotação pelo dono:**

| O quê | Como | Falha |
|---|---|---|
| Segredo que o dono cola (URL, token, chave) | `PUT .../config` com `If-Match`, testado antes de gravar | Configuração anterior continua valendo |
| Assinatura do webhook **[se D-4=a]** | `POST .../signing-secret/rotate` com `If-Match`. Assina com as duas por 24 h; o antigo e o prazo ficam no JSON selado e expiram na leitura, sem o sondador escrever | Segredo anterior continua valendo |

**Regras do `PUT .../config` e da rotação:**

- **O que o dono envia é substituído por inteiro.** Campo ausente some (cabeçalhos do webhook, `token` do ntfy). Não existe "manter o valor atual": a tela não recebe valores (§10), então o dono cola de novo todo segredo que quiser manter.
- **O que o StillOn gerou nunca vem do cliente e não é perdido.** No webhook, o `PUT` relê do JSON selado o segredo de assinatura, o anterior e o prazo, e os regrava. Trocar a URL não troca a assinatura nem encerra uma rotação em andamento.
- **Ordem:** ler a linha e conferir o `If-Match` (412 sem enviar nada); `Parse`; testar o destino fora de transação; gravar com `UPDATE ... SET config_enc = $novo, config_rev = config_rev + 1 WHERE id = $id AND user_id = $dono AND config_rev = $rev`, com a auditoria na mesma transação. Zero linhas é 412: nada é gravado, e o segredo gerado por uma rotação é descartado sem aparecer em resposta.
- **A rotação também exige `If-Match`**, porque ela e o `PUT` regravam o mesmo JSON selado. Não envia nada ao destino. Nova rotação dentro das 24 h descarta o segredo mais antigo e recomeça o prazo.
- **`config_rev` só muda em `PUT` e rotação.** O `PATCH` (`name`, `enabled`, `send_down`, `send_up`) não toca a configuração selada, não incrementa a versão e não exige `If-Match`. A expiração do segredo anterior acontece na leitura, sem incrementar; ele some na gravação seguinte.
- `config_rev` fica fora do AAD: o binário anterior continua abrindo a linha, e o AAD atual não muda.
- `PUT` em `email` é recusado (400, `kind`, `invalid`): endereço novo exigiria nova verificação, e trocar é apagar e recriar. Em `discord`, grava o valor cru, como o cadastro.
- Canal pausado aceita `PUT` e continua pausado. Sucesso no teste do `PUT` grava `last_success_at`, como o `/test` (§9).
- Aviso já reivindicado sai com a configuração que leu. A janela é o prazo de um envio, e fica aceita.

**Rotação da `MASTER_KEY`:** não existe hoje; ver D-2.

## 6. Contrato de `Notifier`, retry, idempotência e limite

**Envelope do webhook (v1) [se D-4=a].** Corpo montado só com dados do banco, igual em toda retentativa (o teste é síncrono e não tem
retentativa). Todas as chaves estão sempre presentes, e o receptor discrimina por `event`:

```json
{"schema":"stillon.aviso.v1","id":"<uuid>","event":"opened|reminder|resolved|tls_expiring|test",
 "incident_id":"<uuid>|null","subject":{"kind":"target|heartbeat","name":"…"},
 "started_at":"…","resolved_at":null,"queued_at":"…","cause":"http_status","closed_by_resume":false,
 "tls":null}
```

| Campo | `opened`, `reminder`, `resolved` | `tls_expiring` | `test` |
|---|---|---|---|
| `id` | Id da notificação | Id da notificação | UUIDv7 gerado no teste; não há linha na outbox |
| `incident_id` | Id do incidente | `null` | `null` |
| `subject` | Alvo ou heartbeat | `kind: "target"` e nome do alvo | `null` |
| `started_at`, `cause` | Do incidente | `null` | `null` |
| `resolved_at` | `null` enquanto aberto | `null` | `null` |
| `closed_by_resume` | Do incidente | `false` | `false` |
| `queued_at` | `created_at` da notificação | `created_at` da notificação | Instante do teste |
| `tls` | `null` | `{"not_after":"…","stage_days":<n>}` | `null` |

**Aviso de certificado (SP1).** A SP1 (§5.2 e migration `notifications_tls_arc`) torna `notifications.incident_id` anulável, acrescenta `tls_alert_id` e
o evento `tls_expiring`; aviso de certificado não é incidente. Por isso:

- Nenhum tipo depende de `incident_id` fora dos eventos de incidente. A chave do PagerDuty existe só neles; quem recebe `tls_expiring` é D-11.
- Os valores de `stage_days` são os que a SP1 fixar; o contrato não os enumera.
- Slack, Telegram e ntfy reusam o texto do evento em `message.go`, sem citar incidente.
- A reivindicação segue a regra da SP1 para o arco: `LEFT JOIN incidents i`, `LEFT JOIN tls_alerts a ON a.id = cl.tls_alert_id` e `LEFT JOIN targets t ON t.id = COALESCE(i.target_id, a.target_id)`. Trocar só `incidents` por `LEFT JOIN` não basta: `t` sai nulo, `COALESCE(t.enabled, h.enabled)` vem NULL, o Scan em `TargetEnabled bool` falha, a transação da reivindicação cai e a fila trava a cada tick. Pelo mesmo motivo, `IncidentID`, `StartedAt`, `Cause` e `ClosedByResume` deixam de ser tipos não anuláveis na linha reivindicada.
- Com o `JOIN` atual, a linha TLS é reivindicada, a tentativa é contada e ela some do resultado. Ela nunca volta do lote, então `semEnvio` não a alcança: neste binário fica `pending`, com a tentativa subindo a cada lease. Só uma imagem com a junção acima a lê, e, se a linha já passou de `MaxAttempts`, aí sim `semEnvio` a fecha como `failed`, sem envio.
- Quem entrar depois, entre a SP1 e a SP2 (D-10), faz a adaptação. Se a SP2 vier antes, o envelope já nasce com `tls` e `incident_id` anulável e nada emite `tls_expiring` até o PR 7 da SP1. Se a SP1 vier antes, os tipos novos entram já filtrados pela resposta de D-11.

**Assinatura (formato Standard Webhooks) [se D-4=a]:** cabeçalhos `webhook-id` (id da notificação, fixo), `webhook-timestamp`
(instante da tentativa, relógio injetado) e `webhook-signature` (`v1,<base64>`, HMAC-SHA256 de `id.timestamp.corpo`). Durante a
rotação, duas assinaturas separadas por espaço.

**Retry.** Mantém outbox, 6 tentativas e o perdão de 429. Acrescenta jitter de até 20% ao backoff (Dogpile). Entrega é
at-least-once: deduplica onde o provedor permite (e-mail, PagerDuty, receptor do webhook) e aceita duplicata rara nos demais.

**Bulkhead: duas raias.** Hoje os destinos são confiáveis; com URL do usuário, um receptor que responde em 9,9 s prende o lote.

| Raia | Tipos | Prazo por envio | Lote |
|---|---|---|---|
| Confiável | `discord`, `email`, `slack`, `telegram`, `pagerduty` | 10 s | 8 |
| URL do usuário | `webhook`, `ntfy` | 5 s | 8 |

Um único `Dispatcher` com dois laços internos, cada um reivindicando com `kind = ANY($)`. Uma instância por raia não serve:
`NewDispatcher` faz `MustRegister` de `notify_total` no construtor, e a segunda entraria em pânico. O `lote` passa a unir
`notif_channels`, com `FOR UPDATE OF n SKIP LOCKED`. O lease atual cobre os dois.

**Disputa pelo pool.** O pool tem 10 conexões (`storage.NewPool`), divididas com sondas e API. Com duas raias, até 16 desfechos
gravam ao mesmo tempo, cada um na sua transação, e a espera por conexão conta nos 5 s de `persistTimeout`. Desfecho não gravado
volta pelo lease e sai duplicado. Por isso `persistir` passa por um semáforo único do `Dispatcher`, com 4 vagas (constante, 40% do
pool), cuja espera conta nos mesmos 5 s. Cada raia continua usando uma conexão na reivindicação, com `claimTimeout` próprio.

**Fan-out por evento.** `enfileirar` (`scheduler.go`, também chamada por `heartbeat.go`) lista os canais com
`ListDeliverableChannels` e chama `EnqueueNotification` para cada um. O filtro entra na listagem, que passa a receber o evento;
`EnqueueNotification` não muda.

| Evento | Quem entra |
|---|---|
| `opened` | Canais com `send_down` |
| `resolved` | Canais com `send_up` |
| `reminder` | Sem filtro novo: `ListReminderCandidates` parte das linhas `opened` e `reminder`, então canal sem `send_down` não tem lembrete |
| `tls_expiring` | Conforme D-11 |
| `test` | Sem fan-out (§9) |

Vale no enfileiramento: mudar `send_down` ou `send_up` não altera aviso já na fila. A imagem anterior ignora as duas colunas, então
um rollback envia a mais, nunca a menos.

**Rollback de tipo novo (proposta, confirmar em D-13).** A partir do PR 1, a reivindicação só pega os tipos que o binário
**conhece**: lista constante do código, e não os notifiers registrados, para `email` com o Resend desligado continuar `failed`, como
hoje. Linha de tipo desconhecido fica `pending`: não gasta tentativa, não marca `failing`, não ocupa vaga do lote e sai quando a
imagem que a conhece voltar. Nessa hora, `semEnvio` cancela abertura e lembrete de incidente já resolvido. A retenção só apaga o que
não está `pending`, então um rollback definitivo deixa linhas paradas até o `Down`, que as apaga em cascata com os canais. As raias
do PR 4 listam só os tipos que a imagem já implementa. Se D-13 aceitar a perda, o PR 1 não muda a reivindicação, e o comportamento é
o da §2 apenas até o PR 4: depois dele, `kind = ANY($)` já não reivindica tipo desconhecido.

A mesma decisão cobre `tls_expiring` num binário abaixo do PR 7 da SP1, que tem o `JOIN` atual. Com D-13=a, o `lote` também só pega
eventos conhecidos: a linha fica `pending`, sem gastar tentativa, e sai quando a imagem com o arco voltar. O filtro precisa estar
publicado antes do PR 7 da SP1, no primeiro PR de tolerância da spec que vier primeiro (D-10). Com D-13=b, a linha segue o caminho
descrito em "Aviso de certificado" e pode terminar `failed`, e o filtro de `kind` das raias não a protege.

**Limite de envio por canal.** Sem estado em memória: depois do primeiro envio bem-sucedido de um canal no lote, os seguintes
voltam à fila **sem gastar tentativa**, com `próxima = agora + 2 s × posição`. A regra vale dentro de um lote e não coordena lotes
de processos diferentes: com dois processos no ar, como num deploy, o teto pode dobrar, e o 429 existente cobre o excedente. Vale
para Slack, Telegram, webhook e ntfy. Dá cerca de 30 por minuto por canal: atende o Telegram em chat privado (1 por segundo), e em grupo
(20 por minuto) o 429 existente cobre o excedente.

O volume total é limitado: fan-out só na transição e `MaxChannels` por usuário.

**Lembretes:** ver D-7.

**`failing`.** Regra atual mantida, sem coluna nova. Falha do próprio StillOn não marca o canal. Um teste manual com sucesso grava
`last_success_at` e limpa o estado. O padrão Standard Webhooks pede desativar em 410; **não** adotado neste pacote: exigiria UPDATE
do sondador em `notif_channels`, e o 410 já vira `failing` visível.

## 7. SSRF

| Regra | Onde | Motivo |
|---|---|---|
| Somente `https`, sem credencial na URL, sem fragmento | `Parse` e no envio | Cabeçalhos do webhook são segredo |
| Portas 443 e 8443 | `Parse` e no envio | Reduz varredura e confusão de protocolo; ampliar é uma linha |
| Cliente `safedial` sem seguir redirect, em todos os tipos novos, inclusive ntfy (`ntfy.sh` ou servidor próprio) | Novo construtor em `safedial` | 307/308 reenviaria corpo, assinatura e o Bearer do ntfy. 3xx vira `rejected` permanente |
| Recusar o host de `PUBLIC_BASE_URL`, comparando depois de `DialedHost` e de remover pontos finais | `Parse` | Evita laço com `/ping` do próprio StillOn; o IP público da VPS não está na lista de bloqueio. `DialedHost` só trata caixa e IDNA, e `exemplo.com.` chega ao mesmo destino |
| Reusar `ValidateURL`, `DialedHost`, `IsSingleLabel`, `HostInstavelNoIDNA` | `Parse` | Mesma barreira do alvo |
| Cabeçalhos do usuário: reusar `forbiddenHeaders`; recusar `webhook-*` e `Content-Type` | `Parse` | Impede forjar assinatura |
| Erro genérico (`delivery_failed`) | API | Sem oráculo de porta ou host |
| Hosts fechados | Slack, Telegram, PagerDuty | Como `discordHosts`: não vira proxy de POST |

O sondador já acessa URL arbitrária pelo `safedial`. A novidade é POST com corpo e cabeçalhos.

## 8. Migration

**Numeração.** A maior migration da `origin/main` é a `00022`; a `00015` fica vaga para sempre, e o goose recusa número menor que o
aplicado. O plano Discord cita a `00023` "se nada entrar antes". A disputa com o TOTP acabou: ele não virou migration (a SP1
registra a solução por allowlist em teste). Número definitivo: o maior da `origin/main` no merge, mais um; `NNNNN` abaixo.

Só expansão:

```sql
-- +goose Up
ALTER TABLE notif_channels DROP CONSTRAINT notif_channels_kind_check;
ALTER TABLE notif_channels ADD CONSTRAINT notif_channels_kind_check
    CHECK (kind IN ('discord','email','webhook','slack','telegram','pagerduty','ntfy'));
ALTER TABLE notif_channels ADD COLUMN name text
    CHECK (name IS NULL OR char_length(name) BETWEEN 1 AND 80);
ALTER TABLE notif_channels ADD COLUMN config_rev int NOT NULL DEFAULT 1 CHECK (config_rev >= 1);
ALTER TABLE notif_channels ADD COLUMN send_down bool NOT NULL DEFAULT true;
ALTER TABLE notif_channels ADD COLUMN send_up   bool NOT NULL DEFAULT true;
ALTER TABLE notif_channels ADD CONSTRAINT notif_channels_some_event CHECK (send_down OR send_up);
ALTER TABLE notif_channels ADD CONSTRAINT notif_channels_config_size CHECK (octet_length(config_enc) <= 16384);
CREATE INDEX notifications_channel_id_idx ON notifications (channel_id, id DESC);
DROP INDEX notifications_channel_idx;

-- +goose Down
-- A descida apaga os canais de tipo novo. Sob FORCE, o papel da aplicação sem
-- usuário não apaga canal de ninguém, e o sondador só tem SELECT.
ALTER TABLE notif_channels NO FORCE ROW LEVEL SECURITY;
DELETE FROM notif_channels WHERE kind NOT IN ('discord','email');
ALTER TABLE notif_channels FORCE ROW LEVEL SECURITY;
ALTER TABLE notif_channels DROP CONSTRAINT notif_channels_config_size;
ALTER TABLE notif_channels DROP CONSTRAINT notif_channels_some_event;
ALTER TABLE notif_channels DROP COLUMN send_up, DROP COLUMN send_down, DROP COLUMN config_rev, DROP COLUMN name;
ALTER TABLE notif_channels DROP CONSTRAINT notif_channels_kind_check;
ALTER TABLE notif_channels ADD CONSTRAINT notif_channels_kind_check CHECK (kind IN ('discord','email'));
CREATE INDEX notifications_channel_idx ON notifications (channel_id);
DROP INDEX notifications_channel_id_idx;
```

- **`CHECK` e não tabela de tipos:** o conjunto muda por release de código, não por operação de negócio. O alargamento é de uma vez por lote de integradores.
- **RLS:** nenhuma tabela nova. As políticas atuais cobrem as colunas novas. `InsertChannel` e os SELECT têm colunas explícitas no código gerado, e as colunas novas têm padrão, então o binário antigo convive.
- **Índice:** `deliveries` pagina por `(channel_id, id)`, e `notifications_channel_idx` só tem `channel_id`. O índice novo continua servindo ao `ON DELETE CASCADE` de apagar canal.
- **[se D-1=B]** `config_rev` e `config_size` assumem o JSON selado; com A ou C, a §3 diz o que refazer.
- **Dois caminhos de descida.** Operacional: imagem anterior com schema novo, desde que contenha o PR 1, testada no molde de `heartbeat_rollback_test.go`, inclusive com linhas de tipo novo. Destrutivo: `Down`, que perde esses canais e, em cascata, seus avisos.
- O `Down` assume que o papel da aplicação é dono da tabela (comentário do patch do TOTP). Provar em Postgres 17 e 18.

## 9. API, DTOs e limites

| Rota | Balde | Observação |
|---|---|---|
| `GET /api/v1/channels` | comum | Inclui `name`, `rev` (o `config_rev`), `send_down`, `send_up`; nunca configuração |
| `POST /api/v1/channels` | envio | `{"kind","name","config":{…},"send_down","send_up"}`. Mantém `url` e `email` antigos por uma versão |
| `PATCH /api/v1/channels/{id}` | comum | Passa a aceitar `enabled`, `name`, `send_down`, `send_up`; ausente não altera |
| `PUT /api/v1/channels/{id}/config` | envio | Corpo como o `config`; exige `If-Match: "<rev>"`. Sem ele, 428; versão velha, 412, nada gravado. Primeiro uso de `If-Match` na API. Regras na §5 |
| `POST /api/v1/channels/{id}/test` | envio | Comportamento por estado abaixo |
| `POST /api/v1/channels/{id}/signing-secret/rotate` | envio | Só `webhook` **[se D-4=a]**. Exige `If-Match`, como o `PUT`. Devolve o segredo uma vez |
| `GET /api/v1/channels/{id}/deliveries?cursor&limit` | comum | Keyset por id, `limit` até 100 |

- Entrada: DTO estrito, campo desconhecido é 400. `id`, dono, `verified_at`, segredo de assinatura e `rev` nunca vêm do cliente.
- Saída: `channelResponse` por allowlist. `deliveries` só `id`, `event`, `status`, `attempts`, `last_error`, `created_at`, `sent_at`, `subject_name`.
- O campo `signing_secret` só existe no 201 do webhook e na resposta da rotação.
- Canal alheio é 404 em toda rota, sem revelar existência.
- `send_up=false` em `pagerduty`, no `POST` e no `PATCH`: conforme D-12.

**`POST .../test` por estado do canal:**

| Canal | Resposta |
|---|---|
| Verificado ou `failing`, habilitado | Envia pelo tipo; 204 ou `delivery_failed` |
| Pausado (`enabled=false`) | Envia e continua pausado: a pausa silencia a fila (`semEnvio`), e o teste é ação explícita do dono, útil antes de retomar |
| E-mail verificado | Mensagem de teste pelo Resend, sem link; não toca em `verified_at` |
| E-mail `pending` ou `expired` | 409 (`channel_not_verified`, código novo), nada enviado: o endereço ainda não provou ser do dono. O caminho é `verify/resend` |
| E-mail com Resend desligado | 400 (`kind`, `unavailable`), como no cadastro |
| PagerDuty | Conforme D-8; o PR 9 depende dela |

O teste é síncrono, com o prazo da raia do tipo, e não passa pela outbox: `notifications` não aceita o evento `test`, e toda linha precisa
de um pai (incidente hoje; incidente ou `tls_alerts` com o arco da SP1), então ele não aparece em `deliveries`. Sucesso grava `last_success_at` (a política de `channel_health` aceita o dono) e limpa
`failing`. Falha não grava `last_failure_at`: `failing` continua refletindo só falha definitiva da fila.

| Campo | Limite |
|---|---|
| Corpo | 16 KiB |
| `name` | 1 a 80 caracteres, sem controle |
| URL (webhook, ntfy) | até 2048 bytes |
| URL Slack | até 512 bytes, `/services/<id>/<id>/<segredo>`; padrão exato **[não verificado]** |
| Cabeçalhos do webhook | até 8; nome até 64; valor até 1024; total 4 KiB |
| `bot_token` **[se D-3=a]** | `^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$` **[não verificado]** |
| `chat_id` **[se D-3=a]** | inteiro com sinal, ou `@nome` até 33 caracteres |
| `routing_key` | exatamente 32 caracteres alfanuméricos |
| `topic` | `^[-_A-Za-z0-9]{1,64}$` **[não verificado]** |
| `token` ntfy | até 256 caracteres |
| Canais | `MaxChannels` (ver D-6) |

Sequência de criação inalterada: valida, confere o teto, testa o destino, grava com o usuário travado. O teste do e-mail continua
sendo o link.

## 10. Telas e estados

Aplicam-se as decisões de design 7 (erro persistente, sucesso some em 6 s) e 9 (vazio com ícone, frase e botão primário) e o botão
habilitado com erro ao enviar.

| Tela | Conteúdo | Estados |
|---|---|---|
| Canais | Lista com nome, tipo, `hint`, saúde, ações | Carregando; erro neutro com "Tentar de novo"; vazio; tipo desconhecido (linha genérica, sem quebrar a lista) |
| Catálogo | Escolha do tipo, um cartão por integrador | Tipo indisponível (e-mail sem Resend) com motivo |
| Formulário por tipo | União discriminada; ajuda de onde obter cada valor (Telegram conforme D-3); `Avançado` recolhido com cabeçalhos; "avisar retornos" no PagerDuty conforme D-12 | Validando; recusado por campo; `delivery_failed`, com o digitado mantido; 429 com o tempo de espera |
| Segredo do webhook **[se D-4=a]** | Diálogo de cópia, mostrado uma vez | Copiado; aviso de que não reaparece |
| Trocar configuração | Mesmo formulário, sem valores atuais; aviso de que o que ficar em branco será removido | 412 com "Alterado em outra aba, recarregue", também na rotação |
| Detalhe do canal | Avisar quedas e retornos, Testar, Rotacionar, Pausar, Apagar | `verified`, `pending`, `expired`, `failing` com motivo, pausado. Testar fica indisponível, com motivo, em e-mail `pending` ou `expired` |
| Histórico de envios | Lista paginada | Vazio; carregando; erro; retenção de 30 dias informada |

Cor nunca é o único sinal: selo com ícone e texto, como hoje.

## 11. Testes

**Harness.** Servidor falso em loopback, atingido com `safedial.NewClientAllowing`, com cenários:

| Cenário | Resultado esperado |
|---|---|
| Lentidão acima do prazo | `timeout`, transitório, sem ultrapassar o prazo da raia |
| Cabeçalho lento, corpo pendurado | Aborta no prazo, sem goroutine presa |
| 500, 502, 503 | `server_error`, retry com backoff |
| Resposta parcial: conexão cai antes do status | `network`, retry |
| Resposta parcial: 2xx e corpo truncado | Entregue, sem retry |
| Conexão recusada | `network`, retry |
| 429 com `Retry-After` válido, absurdo, `NaN` e negativo | Espera respeitada, teto ou backoff; não gasta tentativa |
| 429 do Telegram com `retry_after` no corpo, e corpo acima de 4 KiB | Lê limitado, respeita teto |
| 301, 307, 308 | `rejected` permanente, nenhum segundo pedido |
| Corpo gigante | Lê até o limite, sem estourar memória |

**Unitários, em `[table]` por tipo:** `Parse` com vazio, mínimo, máximo, acima do máximo, controle, NUL e campo desconhecido;
`Hint` sem segredo; montagem do corpo com valores literais calculados à mão; assinatura contra vetor fixo.

**Integração com Postgres real:** criação com teto concorrente, fan-out por `send_down` e `send_up`, reivindicação por raia, adiamento
sem gastar tentativa, RLS e IDOR em toda rota, rollback com SQL antigo, `Up`, `Down` e `Up` de novo.

**Regra de aceitação:** cada teste só vale depois de visto falhar com o código sabotado.

| Teste | Sabotagem que precisa derrubá-lo |
|---|---|
| `TestKindDesconhecidoNaoParaOsLembretes` | Devolver o erro em `enqueueDue` |
| `TestWebhookNaoSegueRedirect` | Usar `safedial.NewClient` |
| `TestWebhookParaEnderecoInternoERecusado` | Liberar loopback no cliente de produção |
| `TestAssinaturaCobreIdTempoECorpo` | Omitir o timestamp do HMAC |
| `TestRotacaoAssinaComAsDuasDuranteOPrazo` | Enviar só a nova |
| `TestWebhookLentoNaoAtrasaARaiaConfiavel` | Reivindicar sem filtro de tipo |
| `TestAdiamentoPorLimiteNaoGastaTentativa` | `devolver: false` |
| `TestPagerDutySemLembrete` | Dar cadência ao tipo |
| `TestDedupKeyEDoIncidente` | Usar o id da notificação |
| `TestNomeDoAlvoNaoViraMencaoNoSlack` | Remover o escape |
| `TestPutConfigComVersaoVelhaDa412` | Tirar `config_rev` do `WHERE` |
| `TestDownApagaTiposNovosERestauraCheck` | Remover o `NO FORCE` |
| `TestFanOutRespeitaSendDownESendUp` | Listar canais sem filtrar pelo evento |
| `TestEnvelopeSemIncidenteTemIncidentIdNulo` | Emitir o UUID zero em `tls_expiring` e `test` |
| `TestTipoDesconhecidoFicaPendenteSemGastarTentativa` **[se D-13=a]** | Reivindicar sem filtro de tipo conhecido |
| `TestTlsExpiringFicaPendenteSemGastarTentativa` **[se D-13=a]** | Reivindicar sem filtro de evento conhecido |
| `TestAvisoTLSReivindicadoNaoTravaAFila` | Unir `targets` só por `i.target_id` |
| `TestDuasRaiasUsamUmaMetricaSo` | Construir um `Dispatcher` por raia |
| `TestGravacaoDeDesfechoRespeitaOSemaforo` | Remover o semáforo de `persistir` |
| `TestPutConfigPreservaSegredoDeAssinatura` | Selar só o que veio do cliente |
| `TestRotacaoComVersaoVelhaDa412` | Ignorar `If-Match` na rotação |
| `TestPatchNaoMudaConfigRev` | Incrementar `config_rev` no `PATCH` |
| `TestEmailNaoVerificadoNaoRecebeTeste` | Remover a checagem de `verified_at` no `/test` |
| `TestTesteEmCanalPausadoEnviaEMantemPausa` | Recusar quando `enabled=false` |
| `TestWebhookParaOProprioHostERecusado`, com ponto final no host | Comparar sem remover o ponto |
| `TestNtfyNaoSegueRedirect` | Usar `safedial.NewClient` |

Sem credencial real em gate. Canário `CANARY-<uuid>` em todo campo secreto, conferido em corpo, cabeçalho e log capturado.

## 12. STRIDE

| Ameaça | Caso | Mitigação | Teste |
|---|---|---|---|
| Spoofing | Receptor aceita evento forjado | HMAC com segredo por canal | `TestAssinaturaCobreIdTempoECorpo` |
| Spoofing | Replay no receptor | Timestamp assinado, id fixo; a tolerância é do receptor | `TestTimestampEDaTentativaEIdEFixo` |
| Spoofing | Cabeçalho do usuário imita `webhook-signature` | Recusado no `Parse` | `TestCabecalhosReservadosRecusados` |
| Tampering | Ciphertext movido entre canais | AAD com id | `TestConfigDeOutroCanalNaoAbre` |
| Tampering | `@channel` ou texto hostil no nome do alvo | Escape e texto puro | `TestNomeDoAlvoNaoViraMencaoNoSlack` |
| Tampering | Duas trocas de configuração simultâneas | `If-Match` + `config_rev` | `TestPutConfigComVersaoVelhaDa412` |
| Tampering | Troca de configuração e rotação simultâneas perdem o segredo | `If-Match` nas duas; segredo gerado preservado no `PUT` | `TestRotacaoComVersaoVelhaDa412`, `TestPutConfigPreservaSegredoDeAssinatura` |
| Repudiation | Falha sem rastro | Auditoria `notification.failed` existente | `TestFalhaDefinitivaGravaAuditoria` |
| Repudiation | Troca ou rotação sem rastro | Auditoria na mesma transação | `TestRotacaoGravaAuditoria` |
| Info disclosure | Token em log ou erro | `SendError` sem causa, canário | `TestSegredoDoCanalNuncaApareceEmLogNemResposta` |
| Info disclosure | Segredo na API | DTO sem campo; chaves exatas | `TestRespostaDeCanalTemChavesExatas` |
| Info disclosure | Oráculo de rede pelo cadastro | Erro único | `TestDestinoRecusadoEInalcancavelRespondemIgual` |
| Info disclosure | Dado de outro usuário | RLS e `user_id` na query, 404 | `TestCanalAlheioDa404EmTodaRota` |
| DoS | Webhook lento segura todos | Raias e prazo | `TestWebhookLentoNaoAtrasaARaiaConfiavel` |
| DoS | Teste usado para gerar tráfego | Balde de envio | `TestTesteDeCanalDivideOBaldeDeEnvio` |
| DoS | `/test` manda mensagem a e-mail de terceiro não verificado | 409 sem envio | `TestEmailNaoVerificadoNaoRecebeTeste` |
| DoS | Amplificação contra terceiro | Teto de canais, fan-out por transição, limite por canal | `TestUmEnvioPorLoteParaTiposLimitados` |
| DoS | Resposta hostil | Cabeçalho e corpo limitados, `Retry-After` com teto | `TestRetryAfterAbsurdoFicaNoTeto` |
| Elevation | SSRF, DNS rebinding, redirect | `safedial`, sem redirect | `TestWebhookParaEnderecoInternoERecusado`, `TestWebhookNaoSegueRedirect` |
| Elevation | Laço com o próprio StillOn | Recusa do host público | `TestWebhookParaOProprioHostERecusado` |
| Elevation | Sondador escrevendo em canal | Só SELECT mantido | `TestSondadorNaoAlteraCanal` |

## 13. Custo na VPS compartilhada

**Tudo é estimativa, nada foi medido.**

- **Limites atuais:** processo com 512 MB, 0,5 CPU e `GOMEMLIMIT` de 384 MiB (`deploy/coolify/app.json`).
- **Concorrência:** a segunda raia dobra o teto de 8 para 16 conexões simultâneas. Cada uma faz handshake TLS completo, porque o `safedial` desliga keep-alive. Medir CPU num lote de 16 antes de publicar a raia.
- **Memória (estimativa, não medida):** por conexão, até 64 KiB de cabeçalho e cerca de 100 KiB de buffers HTTP e TLS (suposição), perto de 0,2 MiB; o corpo da resposta é descartado em `drenar`, e o do Telegram guarda até 4 KiB. Com 16 conexões, cerca de 3 MiB; o lote traz até 16 × 16 KiB de `config_enc`, cerca de 0,25 MiB. Pico total de uns 4 MiB, metade disso novo, cerca de 1% dos 384 MiB. Não há limitador novo em memória. A revisão da SP1 estima 128 MiB de folga para os limitadores dela, sobre o mesmo processo. O PR 4 confere o pico com as métricas `go_memstats_*` no mesmo lote de 16.
- **Pool:** 10 conexões. O semáforo da §6 limita o dispatcher a 4 gravações simultâneas. O PR 4 mede as métricas `pgxpool_*` no lote de 16, junto com as sondas, antes de publicar a raia. Estimativa, como o resto.
- **Banco:** nenhuma tabela nova. Um incidente de 24 h gera 17 linhas por canal com cadência do Discord (2 + 12 + 3); com 10 canais, 170. Retenção de 30 dias.
- **Tráfego:** só saída, em rajadas curtas. Não abre porta; firewall não muda.
- **Actions:** cerca de 30 min por PR com código (dado do `CLAUDE.md`). Os 11 PRs da §15 dão cerca de 5,5 h. Agrupar reduz o custo e aumenta o tamanho de revisão e o risco de deploy.
- **Serviços pagos:** nenhum.

## 14. Decisões do dono

Nenhuma foi tomada aqui.

| # | Decisão | Opções, prós e contras | Recomendação e custo |
|---|---|---|---|
| D-1 | Modelo de canal | Tabela da §3 | **B.** Custo: validação só no código |
| D-2 | `MASTER_KEY` | **(a) Manter:** nada a fazer; vazamento ou troca obriga a recadastrar canais, cabeçalhos e TOTP. **(b) `MASTER_KEY_PREVIOUS` e comando de recifrar:** `Open` tenta as duas, sem mudar o formato; AEAD torna a tentativa segura; toca três áreas | **(b)**, em PR separado e não bloqueante. Custo: procedimento novo no runbook |
| D-3 | Telegram | **(a) Bot do usuário:** sem rota de entrada nem segredo do operador; cadastro difícil (criar bot, descobrir `chat_id`). **(b) Bot do StillOn com vínculo:** cadastro de um clique; exige segredo do operador, entrada pública e verificação | **(a)** agora. Custo: atrito no cadastro |
| D-4 | Webhook | **(a) JSON fixo + Standard Webhooks:** pequeno, sem injeção de template, bibliotecas prontas no receptor; não adapta a qualquer API, paridade parcial do D4. **(b) Placeholders:** paridade; escape por tipo de conteúdo. **(c) Assinatura própria:** sem ganho | **(a)**; (b) em spec futura. Custo: quem usa webhook hoje para adaptar a outra API precisa de um intermediário |
| D-5 | Canal por monitor (D25) | **Incluir:** resolve "tudo vai para todos", importante com PagerDuty; exige N:N, RLS e fan-out novo. **Adiar:** pacote menor | **Adiar para a spec seguinte.** Custo: todo aviso vai a todos os canais até lá |
| D-6 | Teto de canais | **Manter 10:** nada muda, pode apertar com 7 tipos. **Subir para 25:** folga; 2,5 vezes o pior caso de envio | **Manter 10** até haver pedido real |
| D-7 | Lembretes dos tipos novos (a tabela atual foi aprovada por você) | **Como Discord (30 min, depois 6 h):** consistente; push no celular pode incomodar. **Como e-mail (2 h, depois 24 h):** menos ruído, menos insistência. **Sem lembrete** | Slack e webhook como Discord; Telegram e ntfy como e-mail; PagerDuty sem |
| D-8 | Teste do PagerDuty | **Change event:** não aciona ninguém; não prova o caminho de alerta e não funciona com chaves de ruleset (começam com `R`). **Trigger + resolve:** prova tudo; pode acordar quem está de plantão | **Change event**, com aviso na tela, e recusa de chave `R` apenas no teste. Custo: 202 não prova que o alerta chegaria |
| D-9 | Pausa automática após falhas | **Não pausar:** como hoje, sem escrita do sondador. **Pausar após N:** poupa envios inúteis; UPDATE do sondador e canal some sem aviso | **Não pausar** |
| D-10 | Ordem entre SP1 e SP2 | **SP1 primeiro:** o arco e `tls_expiring` já existem quando o envelope sai, e a SP2 adapta uma vez; os canais novos esperam. **SP2 primeiro:** canais novos antes; o envelope nasce sem dado real de `tls_expiring`, e o PR 7 da SP1 mexe de novo na reivindicação. Só o PR 7 da SP1 cruza com esta spec | **SP1 primeiro.** Custo: canais novos chegam depois |
| D-11 | O aviso de certificado vai a todos os canais? | **(a) Todos, com PagerDuty:** `trigger` com `dedup_key = stillon:tls:<tls_alert_id>`, o que exige levar esse id a `Event` (hoje `TLS` só traz `NotAfter` e `StageDays`; o envelope não muda); cobertura total, mas aciona o plantão sem queda e abre incidente sem `resolve`, pois aviso de renovação está fora da SP1. **(b) Todos menos PagerDuty:** sem plantão falso; quem só usa PagerDuty fica sem aviso. **(c) Só Discord e e-mail:** nada a adaptar nos tipos novos; Slack, Telegram, webhook e ntfy ficam sem aviso. Em todas, certificado não é queda nem retorno, e `send_down` e `send_up` não filtram, salvo se você preferir o contrário | **(b).** Custo: quem só tem PagerDuty não recebe aviso de certificado |
| D-12 | PagerDuty com `send_up=false` | **(a) Recusar** (400, `send_up`, `invalid`, no `POST` e no `PATCH`): o incidente sempre fecha lá; quem prefere fechar à mão não consegue. **(b) Permitir, com aviso na tela:** liberdade; incidente fica aberto até ação manual. `send_down=false` não tem esse problema: o `resolve` de chave desconhecida é descartado. Pausar ou apagar o canal com incidente aberto tem o mesmo efeito hoje e não muda aqui | **(a).** Custo: uma regra específica do tipo em `Parse` e no `PATCH` |
| D-13 | No rollback, avisos que o binário não conhece: tipo novo, e `tls_expiring` abaixo do PR 7 da SP1 | **(a) Ficam pendentes** (§6): nada se perde; o `lote` filtra tipo e evento conhecidos, e linhas ficam paradas se o rollback for definitivo. **(b) Podem se perder:** o dispatcher fica como está. Para tipo novo, só até o PR 4: cada aviso vira `failed` e o canal fica `failing`; depois dele, `kind = ANY($)` já deixa o tipo desconhecido pendente. Para `tls_expiring`, o filtro de `kind` não ajuda, e a linha pode terminar `failed` sem envio | **(a).** Custo: filtro e junção na reivindicação antes do PR 4 e do PR 7 da SP1 |

| Classe | Decisões | Efeito |
|---|---|---|
| Bloqueia o início | D-1 (PR 2), D-10 (ordem das specs), D-13 (parte do dispatcher no PR 1) | Mudam estrutura ou o primeiro PR |
| Bloqueia só o PR do tipo | D-4 (PR 6), D-7 (PRs 7 a 10), D-3 (PR 8), D-8, D-11 e D-12 (PR 9) | O resto avança; D-11 também define a entrega de `tls_expiring` aos demais tipos |
| Não bloqueia | D-2, D-5, D-6, D-9 | Sem resposta, fica o comportamento atual: sem rotação da chave mestra, sem canal por monitor, teto de 10, sem pausa automática. D-6 é uma constante e muda o pior caso de envio da §13 |

**Bottom line:** aprove D-1, D-10 e D-13 para o trabalho começar, e D-3, D-4, D-7, D-8, D-11 e D-12 antes do PR de cada tipo; D-2, D-5,
D-6 e D-9 não bloqueiam. Maior risco: a raia e os canais com URL
do usuário numa VPS compartilhada, por isso o custo da §13 é medido antes de publicar. Custo principal: cerca de 5,5 h de Actions.

## 15. Plano em PRs

Merge é deploy; cada PR confere o run do `Coolify release` do SHA. Merges estão congelados até 11/11, 14:00 UTC.

| PR | Conteúdo | Requisito |
|---|---|---|
| 1 | Tolerância a `kind` desconhecido em `Reminders` e no painel; `Policy` com "sem cadência"; reivindicação só de tipos e eventos conhecidos (D-13) | Publicado antes do PR 3 |
| 2 | Tabela `Integration`, `ChannelTester` e `Event` ampliado, sem mudança de comportamento | D-1; refatoração pura, testes verdes antes e depois |
| 3 | Migration, com índice de `deliveries`, teste de rollback e `Down` | Número livre conferido no merge |
| 4 | Jitter, duas raias num `Dispatcher`, semáforo de `persistir`, limite por canal | Medição da §13 (CPU, memória, pool) |
| 5 | Fan-out por evento em `ListDeliverableChannels`; API: `config`, `name`, `PATCH`, `PUT`, `test`, `deliveries`; `safedial` sem redirect | O filtro entra antes de a API aceitar `send_*`; sem tipo novo ligado |
| 6 | Webhook com SSRF, envelope, assinatura e rotação | PRs 3 a 5; D-4 |
| 7 | Slack | 6; D-7 |
| 8 | Telegram | D-3; D-7 |
| 9 | PagerDuty, com a regra de `send_up` | D-7, D-8, D-11, D-12 |
| 10 | ntfy | 6; D-7 |
| 11 | Painel: catálogo, formulários, detalhe e histórico | Por tipo já publicado; pode ser fatiado |

**Cruzamento com a SP1 (D-10).** O PR 7 da SP1 e os PRs 2, 4 e 6 desta spec tocam a reivindicação, `Event` e o envelope. Quem entrar
depois aplica a junção completa e o contrato da §6 (`LEFT JOIN tls_alerts` e `targets` por `COALESCE(i.target_id, a.target_id)`, conforme a SP1), com os casos de `tls_expiring` e `test` nos testes.

Antes de cada commit: `go build ./... && go vet ./... && make test` e `sh deploy/ci/checar-nome.sh`.

## 16. Verificado e não verificado

**Verificado na fonte (`d177e72`; da §2, relido só o que mudou desde `ae6ad3f`):** a §2, nomes de funções e rotas citados, numeração das migrations, ausência de
`If-Match` em `internal/httpapi`, `enfileirar`, `ListDeliverableChannels`, `ClaimDueNotifications`, `notifications_channel_idx`,
retenção só do que não está `pending`, política de `channel_health`, pool de 10, `MustRegister` em `NewDispatcher` e `DialedHost`. Na documentação oficial: formato e erros do Slack, escape de texto, `mrkdwn:false`; URL, `sendMessage`,
`retry_after` e limites de frequência do Telegram; campos, deduplicação, códigos e change events do PagerDuty; publicação em JSON,
Bearer e 4096 bytes do ntfy; cabeçalhos, assinatura, segredo, retentativas, rotação, 410 e 3xx do Standard Webhooks.

**Não verificado:**

- Execução de qualquer parte; quebra do painel com `kind` desconhecido (inferida); `Down` com `NO FORCE`; nome do constraint de `kind` (convenção do Postgres).
- Toda estimativa da §13, inclusive memória e disputa pelo pool, e o tamanho de 4 vagas do semáforo.
- A documentação de terceiros não foi relida na revisão de 09/11; vale o registro original acima.
- Limites do ntfy.sh por IP e códigos de erro dele; padrão exato do `topic`.
- Endpoint europeu do PagerDuty (fora do escopo até confirmar); se uma chave inválida responde 400 ou é aceita com 202.
- 429 do webhook do Slack; formato exato da URL e do token do bot.
- Se `mrkdwn:false` sozinho impede menção: por isso o escape é obrigatório.
- Cooldown de flapping: citado da matriz, não relido no código.
- Os quatro arquivos de `knowledge` foram lidos em parte: distributed-data (§§ 1 a 8, 11, estabilidade), api-security (modelagem, rate limit, SSRF, logging, segredos, idempotência, webhooks), design-patterns (quando não usar), testing (integração).
