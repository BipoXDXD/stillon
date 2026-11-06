# Heartbeats: design

Data: 2026-11-05. Tarefas (cron jobs, backups) avisam o StillOn por ping numa URL própria, com
período e tolerância ou expressão cron, sinais de início e de falha, tempo de execução e log de
eventos. Quando o ping não chega no prazo, abre incidente e avisa pelos canais que já existem.

Fora do escopo: equipes, projetos, integrações novas e a tela (a frente visual está refazendo o
painel; este trabalho para na API, com o contrato da §7 pronto).

## 1. Decisões

| # | Decisão | Motivo |
|---|---|---|
| D1 | Migration **00016**. A 00015 fica vaga para sempre e a vinculação Discord pega o próximo número livre quando for implementada | `storage.Migrate` aceita a lacuna, mas **recusa a 00015 depois da 00016** (§2). Mudar o número planejado custa uma linha de documento; ligar `WithAllowOutofOrder` desligaria a proteção para sempre |
| D2 | **Tabela própria `heartbeats`**, com `incidents.heartbeat_id` como arco exclusivo ao lado de `target_id`. Hipótese descartada: `kind` em `targets` | Critério: risco de rollback e queries tocadas. Medido na §3: o `kind` para o sondador antigo inteiro; a tabela própria é ignorada por ele |
| D3 | `GET /incidents` e o ack existente **não mudam**: os incidentes de heartbeat saem por rotas próprias | `schemas.ts` exige `target_id` uuid em cada item: um `null` derrubaria a tela inteira de incidentes. A frente visual decide quando juntar as duas listas |
| D4 | O ping só **registra**; quem abre e fecha incidente é o avaliador, no papel `prober` | Um escritor só de incidentes. `--role=api` responde pings sem tocar em incidentes nem na outbox; `--role=prober` avalia e avisa. O escopo do ping fica com o mínimo |
| D5 | Avaliador sem estado em memória: reivindica por `FOR UPDATE SKIP LOCKED` a cada 5 s, transação curta, `open`/`close` e outbox na mesma transação | Seguro com várias instâncias, no molde de `ClaimDueTargets`. O índice único parcial é a garantia final de um incidente aberto por heartbeat |
| D6 | Só **POST** em `/ping/{token}`, `/start` e `/fail`; GET responde 405 | Prévia de link e scanner fazem GET: um falso ping esconderia uma tarefa parada, que é a falha que o produto existe para pegar. Liberar GET depois é aditivo; o contrário quebraria clientes |
| D7 | Token de 256 bits (`crypto/rand`), base64url, guardado só como SHA-256 | Mesmo padrão do token de verificação. Chave não acrescenta nada com 256 bits de entropia |
| D8 | Revogar é trocar o hash, sem tombstone | Token antigo e inexistente passam pelo mesmo caminho, e a resposta igual vem de construção, não de um `if` |
| D9 | Sinal repetido igual dentro de 10 s não tem efeito: responde igual e não grava | Idempotência sob `curl --retry`. A decisão sai da linha travada, então vale entre instâncias |
| D10 | `start` e `fail` não movem o prazo; só `success` move | Regra única: a tolerância cobre o tempo de execução. Um `start` sem `success` vira atraso no prazo normal |
| D11 | Sem histerese: uma transição por sinal, com **intervalo mínimo de 5 min entre aberturas** do mesmo heartbeat | Um ping já é um sinal deliberado. O intervalo fecha o abuso de token vazado alternando `fail`/`success` para disparar aviso: atrasa a abertura, nunca a perde |
| D12 | Heartbeat novo já tem prazo (`criação + período + tolerância`) | Tarefa que nunca rodou é o caso mais comum de agendamento errado, e deve avisar |
| D13 | Pausar interrompe os lembretes, como no alvo pausado. Retomar zera a falha, recalcula o prazo a partir de agora e grava `resumed_at`. Ping em heartbeat pausado é aceito e registrado | O incidente aberto fecha na retomada, pelo avaliador. Recusar ping pausado quebraria a tarefa do dono sem motivo |
| D14 | Corpo do ping é descartado e não é guardado (limite 10 KiB); nada de IP nos eventos | Dado que não é necessário não entra. Cada byte guardado de um POST público seria superfície |
| D15 | Teto de 1000 eventos por heartbeat, aplicado na transação do ping; 30 dias de idade, aplicados na manutenção | Teto exato sem depender do job. A manutenção cobre o heartbeat que parou de pingar |
| D16 | Limite de 100 heartbeats por usuário, sob advisory lock | Mesmo teto e mesmo mecanismo de `targets` |
| D17 | Dependência nova: **`github.com/robfig/cron/v3`** (só o parser de 5 campos) | Sem dependências próprias, MIT, e resolve o cálculo do próximo disparo com fuso e horário de verão, que é o que erra num parser caseiro. Um parser próprio custaria mais código de risco do que a dependência |
| D18 | Fuso IANA obrigatório em cron, sem padrão; `Local` e vazio recusados. `time/tzdata` embutido no binário | Cron sem fuso depende da máquina. O embutido evita depender do tzdata da imagem distroless |
| D19 | Sem ADR | O repositório não tem `docs/adr/`: decisões ficam nas specs |
| D20 | Causas novas em `incidents.cause`: `heartbeat_late` e `heartbeat_failed`; o texto do aviso fala em tarefa sem sinal, não em "fora do ar" | O CHECK muda por acréscimo. "Fora do ar" descreve um serviço, não um backup que não rodou |

## 2. Numeração: por que não a 00015

Provado em Postgres 18 real com o provider do `storage.Migrate`, goose 3.29.0:

| Cenário | Resultado |
|---|---|
| Da 00014 direto para a 00016 | aceita |
| 00015 chegando com a 00016 aplicada | `detected 1 missing (out-of-order) migration lower than database version (16): version 15` |

`Migrate` roda no boot: uma 00015 depois da 00016 impediria a imagem nova de subir. Por isso o
plano da vinculação Discord passa a usar o próximo número livre.

## 3. Rollback: `kind` em `targets` × tabela própria

Merge é deploy, e rollback é reimplantar a imagem anterior; o schema não desce. Logo o binário
anterior precisa conviver com a 00016 **e com linhas de heartbeat já gravadas**.

Medido com o código gerado da `main` (o binário anterior), em Postgres real, contra as duas
formas de esquema, com um alvo HTTP normal ao lado da linha de heartbeat:

| Operação do binário anterior | `kind` em `targets` (colunas HTTP nulas) | Tabela própria |
|---|---|---|
| `ClaimDueTargets` | **falha** (23502: `make_interval` com `NULL` viola `next_run_at`); **nenhum alvo é sondado** | 1 alvo, normal |
| `ListTargets` | **falha** (`cannot scan NULL into *string`) | 1 alvo, normal |
| `ListTargetSummaries` | **falha** (`cannot scan NULL into *int32`); painel sem dados | 1 alvo, normal |
| `ListIncidents` | não medido | ignora o incidente de heartbeat (inner join) |
| `ClaimDueNotifications` | não medido | não devolve a linha (inner join) |
| `ListReminderCandidates` | não medido | ignora |
| `CountOpenIncidents` | não medido | conta o de heartbeat (só a métrica) |
| Boot: `Migrate` sem a 00016 num banco já nela | ok | ok (0 aplicadas, sem erro) |

Evitar o `NULL` no `kind` exigiria URL e método de mentira: o sondador antigo sondaria um endereço
falso e abriria incidente falso.

**Queries existentes tocadas:**

- `kind` em `targets`: ao menos 9 precisam de filtro (`ClaimDueTargets`, `CountDueTargets`,
  `GetTarget`, `GetTargetForUpdate`, `ListTargets`, `ListTargetSummaries`, `CountTargets`,
  `OwnTargetIDs`, `DeleteTarget`), e um filtro esquecido vira 500 ou alvo fantasma. Colunas HTTP
  viram ponteiros em todo o Go de alvos e sondagem.
- Tabela própria: 4 mudam de verdade (`ClaimDueNotifications`, `ListReminderCandidates`,
  `EnqueueNotification`, `EnqueueReminder`), mais `ListIncidents` e `AckIncident`, que só
  trocam `i.target_id` por `t.id` para seguir devolvendo uuid não nulo. Em RLS, uma política
  nova e nenhuma alterada: políticas permissivas somam por OR.

**Decisão: tabela própria.** Vence nos dois critérios.

### O que acontece num rollback para antes da 00016

Nada quebra, mas heartbeats ficam **sem avaliação**, e isso é o custo aceito:

- Pings não são atendidos (`/ping/` não existe no binário antigo e cai no painel, que só serve
  GET e HEAD): `curl -f` falha na tarefa do dono, sem efeito no StillOn.
- Nenhum incidente de heartbeat abre ou fecha, e os abertos seguem abertos.
- Notificação pendente de heartbeat não sai: o `ClaimDueNotifications` antigo a reivindica e o
  join a descarta, então `attempts` sobe a cada lease. Voltando à versão nova, a linha que passou
  de `MaxAttempts` falha de vez e vai para a auditoria. **Perda possível: um aviso.**
- `incidents_open` conta os incidentes de heartbeat (só métrica).
- A mesma linha de pendência ocupa uma vaga do lote por lease: não bloqueia as demais, que seguem
  por `next_attempt_at`.

Avanço de novo: os prazos vencidos no intervalo viram incidentes no primeiro tique do avaliador,
limitados por D11. Esperado: quem ficou sem avaliação aparece atrasado.

`TestOlderBinaryIgnoresHeartbeatRows` congela isso: roda o SQL literal da `main` anterior
(6 queries) sobre linhas de heartbeat, e `TestOlderBinaryBootsOnNewerSchema` prova o boot.

## 4. Modelo de dados (00016)

```sql
heartbeats (
  id uuid PK, user_id uuid FK users ON DELETE CASCADE,
  name text 1..100,
  ping_token_hash bytea UNIQUE, octet_length 32,
  period_sec int NULL, 60..2678400,
  cron_expr text NULL, 1..100,  timezone text NULL, 1..64,
  grace_sec int, 30..604800,
  enabled bool, created_at timestamptz,
  deadline_at timestamptz NOT NULL,   -- quando o silêncio vira atraso
  last_ping_at, last_success_at, last_start_at timestamptz NULL,
  last_signal text NULL ('start','success','fail'),
  failing_since timestamptz NULL,     -- fail recebido e ainda sem success
  last_incident_at timestamptz NULL,  -- base do intervalo de D11
  CHECK exatamente um agendamento: período, ou cron + fuso
)
heartbeat_events (
  id uuid PK (v7, ordena por tempo), heartbeat_id uuid FK ON DELETE CASCADE,
  at timestamptz, kind text ('start','success','fail'), runtime_ms bigint NULL >= 0
)
incidents: target_id passa a aceitar NULL; heartbeat_id uuid NULL FK ON DELETE CASCADE;
           CHECK num_nonnulls(target_id, heartbeat_id) = 1; cause ganha 2 valores;
           UNIQUE parcial: um aberto por heartbeat; índice na FK.
```

- Índices: `heartbeats (user_id, id DESC)`, `heartbeats (deadline_at) WHERE enabled`,
  `heartbeat_events (heartbeat_id, id DESC)`, e o parcial do arco.
- Sem `kind` e sem `*_type`: as duas FKs reais mantêm integridade referencial.
- **RLS com FORCE**, nos quatro escopos: dono (`user_id`), sondador (SELECT e UPDATE), ping (a
  linha do hash, via GUC `app.ping_token_hash`, em `storage.InPingTx`), e `heartbeat_events`
  delegado por `EXISTS` a `heartbeats`, como `checks` a `targets`.
- `incidents_via_heartbeat` **nomeia** dono e sondador, como `notifications_via_channel`: sem
  isso, o escopo do ping alcançaria o incidente do próprio heartbeat, e o escopo público nada
  enxerga porque `heartbeats` não tem política pública.
- Down apaga os incidentes de heartbeat, restaura o `NOT NULL` e o CHECK e remove as tabelas.
- **Correção da 00016:** o comentário "Também serve à FK" antes de
  `incidents_one_open_per_heartbeat` está errado. Aquele índice cobre só os abertos; quem atende a
  FK é `incidents_heartbeat_idx`. A migration já foi aplicada e não muda.
- **`OR` mantido por medição:** `ClaimHeartbeatsToOpen` filtra com
  `deadline_at <= now OR failing_since IS NOT NULL`, que não usa `heartbeats_due_idx`. Separar em
  `UNION` para alcançar o índice piorou a consulta, e o que não melhora se desfaz. Melhor de 5
  `EXPLAIN ANALYZE`, com 1% de linhas vencidas e 1% em falha:

  | Linhas | `OR` | `UNION` | `UNION` + índice parcial em `failing_since` |
  |---|---|---|---|
  | 100 | 47 µs | 80 µs (seq scan: o planejador não usa o índice) | 62 µs |
  | 10 mil | 452 µs | 669 µs | 397 µs |

  O teto é de 100 heartbeats por usuário e a consulta roda a cada 5 s. O índice parcial, 55 µs
  melhor que o `OR` com 100 vezes o teto, não paga uma migration nem o custo de escrita.
- **Pendência sem urgência:** `heartbeats_due_idx` ficou sem consulta que o use. Removê-lo na próxima
  migration que passar por aqui, que tem de ser maior que a das tags (a 00018, se a 00017 entrar
  antes). Até lá, o custo é só o de manter o índice a cada ping.

A **00018** acrescenta `heartbeats.resumed_at` e `incidents.closed_by_resume` (§5). Tem de entrar
depois da 00017, das tags: com a 00018 aplicada, `storage.Migrate` recusaria a 00017. Colunas
novas não afetam o binário anterior: o sqlc expande `SELECT *` na geração.

## 5. Regra de prazo

Função pura em `internal/heartbeat`, sem I/O, recebendo `now`:

- `success`: `deadline_at = proximo(now) + grace`; zera `failing_since`; `runtime_ms = now -
  last_start_at` se houver `start` aberto, e o `start` é consumido.
- `start`: grava `last_start_at`; prazo intacto.
- `fail`: `failing_since = coalesce(failing_since, now)`; prazo intacto; `runtime_ms` e consumo do
  `start`, como no `success`.
- `success` e `fail` gravam `last_runtime_ms`: a duração medida, ou `NULL` sem `start` aberto.
- `proximo(t)`: `t + period` ou o próximo disparo do cron depois de `t + 30 s`, no fuso declarado.
  A folga (`EarlySignalMargin`, igual a `MinGrace`) existe porque um host com o relógio adiantado
  termina a tarefa de 3h às 02:59:59: sem ela, o próximo disparo seria o das 3h que acabou de rodar,
  com prazo às 03:05, e o incidente falso voltaria todo dia. Exemplo com `0 3 * * *` e 5 min de
  tolerância: sucesso às 02:59:59 ou 02:59:30 dá prazo às 03:05 do dia seguinte; às 02:59:29, às
  03:05 do mesmo dia. O período não tem folga: conta do próprio sinal, sem disparo marcado.
  Custo: um sucesso de verdade do disparo anterior, a menos de 30 s do próximo, conta para o próximo,
  e a falta dele aparece um ciclo depois.
- Saudável = `failing_since IS NULL AND deadline_at > now`. Abrir: habilitado, não saudável, sem
  incidente aberto, e `last_incident_at` fora dos 5 min. Fechar: habilitado, saudável, com
  incidente aberto e com um `success` ou uma retomada depois do início dele.
- **Prazo novo não encerra alarme.** O PATCH só recalcula o prazo se o agendamento ou a tolerância
  mudarem de valor: reenviar o que está gravado não mexe em nada. Com mudança real, o prazo vai para
  a frente, mas o incidente aberto fica até um `success` ou até a retomada, e o `state` segue `late`.
  Sem isso, editar um heartbeat atrasado fecharia o incidente e avisaria que a tarefa voltou, sem
  ping nenhum.
- `started_at`: `deadline_at` no atraso, `failing_since` na falha. `resolved_at`: o `last_success_at`
  que fechou; sem `success` depois do início (retomada), `now`, com `closed_by_resume` ligado. Esse
  aviso diz que o heartbeat foi retomado, e nunca que voltou: a coluna fica no incidente porque a
  retentativa repete o texto, e o Resend recusa a mesma chave com outro corpo.

Cron recusado: fora dos 5 campos, com `TZ=`/`CRON_TZ=` ou que nunca dispara (`0 0 30 2 *`).

Limites conhecidos do horário de verão, fixados em teste: a hora que não existe pula aquele dia, e a
hora repetida dispara duas vezes (1h30 de 1/11/2026 em Nova York: 05:30 e 06:30 UTC). Quem agenda
nela e roda uma vez só pode ter um alarme falso por ano; a saída é agendar fora dessa hora.

## 6. STRIDE do `POST /ping/{token}`

Fluxo: tarefa → (TLS) → proxy → cadeia própria sem cookie, sem CORS e sem cabeçalho anti-CSRF →
limite por IP → limite por token → hash → `InPingTx`.
Dado: o token, que é a credencial; os instantes dos sinais. Sem PII.

| Ameaça | Mitigação | Teste |
|---|---|---|
| **S** Adivinhar token | 256 bits de `crypto/rand` | `TestNewPingTokenIsRandomAnd256Bits` |
| **S** Token lido de dump do banco | só o SHA-256 é guardado; o token sai uma vez, na criação ou na rotação | `TestCreateStoresOnlyTheTokenHash` |
| **S** Token revogado reusado | rotação troca o hash | `TestPingUnknownAndRevokedTokensAnswerTheSame` |
| **T** Token de A mexendo em B | a RLS do escopo ping vê só a linha do hash | `TestPingScopeSeesOnlyTheTokenRow` |
| **T** Forjar incidente pelo ping | política nomeia dono e sondador; o ping nem tenta | `TestPingScopeCannotTouchIncidents` |
| **R** Negar um sinal | aceito: o evento tem instante e tipo, sem origem (D14). Quem tem o token pode pingar; rotacionar é a resposta | `TestPingRecordsTheEvent` |
| **I** Token em log | log usa `r.Pattern`, nunca o caminho; nenhum `slog` do pacote recebe o token | `TestPingNeverLogsTheToken` (canário, inclusive em erro e panic) |
| **I** Descobrir se a URL existe | inexistente, revogada e malformada: mesmo status e mesmo corpo | `TestPingUnknownAndRevokedTokensAnswerTheSame` |
| **I** Vazar dados na resposta | sucesso devolve corpo fixo, sem nome nem estado | `TestPingSuccessBodyHasNoHeartbeatData` |
| **D** Enxurrada por IP, sem penalizar NAT | balde por IP folgado (1200/min, rajada 300: 100 tarefas no mesmo minuto, com início e fim, passam) | `TestPingIPLimitAdmitsManyHeartbeatsBehindOneAddress` |
| **D** Token vazado em laço | balde por token (30/min, rajada 10), chaveado pelo hash, com `Retry-After` | `TestPingPerTokenLimitAnswers429WithRetryAfter` |
| **D** Corpo gigante | 10 KiB, drenado e descartado; acima disso, 413 | `TestPingRejectsOversizedBody` |
| **D** Histórico sem fim | teto na transação e idade na manutenção | `TestEventsStayUnderTheCap`, `TestRetentionDeletesOldHeartbeatEvents` |
| **D** Avisos em massa por token vazado | intervalo mínimo entre aberturas | `TestReopenCooldownLimitsIncidents` |
| **D** Repetição e corrida | D9, com a linha travada | `TestRepeatedPingHasOneEffect`, `TestConcurrentPingsHaveOneEffect` |
| **E** Rota pública alcançar o resto | allowlist explícita, cadeia separada | `TestEveryRegisteredRouteDeniesByDefault` (as três entradas novas em `rotasPublicas`), `TestPingChainServesOnlyPing` |

**Limites em memória.** Os dois baldes usam o limitador existente, por processo. Com mais de uma
instância de API, o orçamento se multiplica por instância; os tetos que valem entre réplicas são
D9, D11 e D15, todos no banco. Hoje há uma instância de API; não há requisito para Redis.

**429 é falha de ping legítima possível.** O efeito é um alarme falso, nunca um silêncio: é a
direção segura para um monitor. O `curl --retry` respeita `Retry-After`.

**Log de proxy, fora do código.** Os `Caddyfile` do repositório não têm diretiva `log`, então não
gravam acesso. O `coolify-proxy` (Traefik) da VPS não tem access log ligado: conferido na VPS em
2026-11-05, nos argumentos e no compose. Ver §9.

## 7. Contrato

Público:

| Rota | Efeito | Respostas |
|---|---|---|
| `POST /ping/{token}` | sucesso | `200 {"status":"ok"}`, `400`, `404`, `413`, `429` |
| `POST /ping/{token}/start` | início | idem |
| `POST /ping/{token}/fail` | falha | idem |
| `POST /ping/{token}/{exit_code}` | `0` é sucesso, `1..255` é falha, com o número no log; fora disso, `404` | idem |

`400` é o corpo que não pôde ser lido até o fim, como conexão interrompida no meio do envio; nada é
gravado. Método diferente de POST dá `405`. Falha do lado do servidor dá `500`, ou `503` com o prazo
vencido, no envelope de erro comum.

Autenticado (`protegida`: Authn, limite por usuário, sessão viva; mutações com o cabeçalho
anti-CSRF). Entrada estrita, com `DisallowUnknownFields`. Recurso alheio é 404.

| Rota | Uso |
|---|---|
| `GET /api/v1/heartbeats?cursor=&limit=` | lista, teto 100 |
| `POST /api/v1/heartbeats` | cria; devolve `ping_url` **uma vez** |
| `GET /api/v1/heartbeats/cron-preview?expr=&timezone=` | próximos 5 disparos, sem gravar: `{ "next_runs": ["…"] }` |
| `GET /api/v1/heartbeats/{id}` | detalhe |
| `PATCH /api/v1/heartbeats/{id}` | `name`, `tags`, agendamento, `grace_sec`, `enabled` |
| `DELETE /api/v1/heartbeats/{id}` | apaga, com eventos e incidentes |
| `POST /api/v1/heartbeats/{id}/token/rotate` | revoga e devolve `ping_url` novo, uma vez |
| `GET /api/v1/heartbeats/{id}/events?cursor=&limit=` | log, do mais novo ao mais velho |
| `GET /api/v1/heartbeats/{id}/incidents?status=&cursor=&limit=` | histórico |
| `POST /api/v1/heartbeats/{id}/incidents/{incident_id}/ack` | reconhece; idempotente |

```jsonc
// heartbeat: conjunto exato de chaves
{ "id": "…", "name": "Backup noturno", "tags": ["prod"], "enabled": true,
  "schedule": { "kind": "period", "period_sec": 86400 },
  // ou { "kind": "cron", "expr": "0 3 * * *", "timezone": "America/Sao_Paulo" }
  "grace_sec": 900, "state": "waiting|up|late|failing|paused",
  "deadline_at": "…", "last_ping_at": null, "last_success_at": null,
  "last_start_at": null, "last_runtime_ms": null, "open_incident_id": null, "created_at": "…" }
// create e rotate: o mesmo objeto + "ping_url"
// evento: { "id": "…", "at": "…", "kind": "start|success|fail", "runtime_ms": 1234, "exit_code": 3 }
// incidente: { "id", "heartbeat_id", "heartbeat_name", "started_at", "resolved_at",
//              "cause": "heartbeat_late|heartbeat_failed", "acknowledged_at" }
```

Criar e editar: `name` 1..100; `schedule` com `kind`; `period_sec` 60..2678400; `expr` até 100;
`timezone` IANA; `grace_sec` 30..604800 (padrão 300). Erros no envelope de campos existente.
`ping_url` nunca volta em leitura. Sem `ETag`/`If-Match`: nenhuma rota do projeto usa, e o PATCH
serializa com `FOR UPDATE`, como o de `targets` (a regra do projeto vence a geral).

## 8. PRs

Empilhados, nesta ordem, cada um com a sabotagem dos testes novos no corpo. Mesclar em ordem.

1. **Esta spec** e a renumeração do plano Discord (`[skip ci]`).
2. **Esquema e domínio:** 00016, `internal/heartbeat` (regra de prazo, cron, token), SQL de
   convivência e os testes de RLS e de rollback.
3. **Endpoint público de ping:** escopo, cadeia, limites, eventos e teto.
4. **Avaliador:** `internal/prober`, outbox, lembretes, texto do aviso, retenção.
5. **API autenticada:** rotas da §7.

Papéis: `--role=api` serve 3 e 5; `--role=prober` roda 4; `--role=all`, tudo.

## 9. Para o dono

- Ajustar o `CLAUDE.md`: "a 00015 é reservada à vinculação Discord" deixa de valer (D1). Nesta
  frente ele não é editado.
- Proxy do Coolify: **conferido em 2026-11-05.** O `coolify-proxy` (Traefik) não tem access log
  ligado, nem nos argumentos nem no compose, então o caminho com o token não é gravado. Se alguém
  ligar o access log, os tokens de ping e de verificação de e-mail passam a ir para o log: é preciso
  filtrar o caminho antes.
