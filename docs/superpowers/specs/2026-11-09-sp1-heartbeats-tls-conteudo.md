# SP1: heartbeats completos, validade de TLS e verificação de conteúdo

**Status:** rascunho para aprovação do dono, 2026-11-09, ajustado pela revisão independente (`revisao-SP1-SP2.md`). Sem código,
sem commit, sem alteração no repositório.
**Base:** escrita sobre `origin/main` em `ae6ad3f`; os pontos das duas revisões foram conferidos em `3456d36` e `d177e72`, pelo ref `origin/main`. Levantamento
de paridade e decisões de design na pasta `design/`.

**Sem verificar** (detalhes na seção 13): nada foi executado; produção e disco da VPS não foram vistos; números de CPU e
memória são estimativa; alguns comportamentos de Go e Postgres estão marcados como portão de PR.

## 1. Objetivo e fora de escopo

**Objetivo.** Fechar a paridade de heartbeats com o serviço de referência e acrescentar duas verificações que ele não tem
por não sondar sites: validade do certificado e conteúdo da resposta.

| Grupo | Itens do levantamento |
|---|---|
| Agendamento | A1 e A4 (faixas), A3 e A7 (OnCalendar e prévia), A9 (tolerância visível), A11 (tempo máximo), A13 (retomada por ping) |
| Ping | B2 (slug e chave), B4 (`/log`), B6 (métodos), B7 (run id), B8 (autoprovisionamento), B9 e B10 (corpo), B11 (filtros), B13 (metadados) |
| Painel e operação | B16 (log unificado), B17 (limpar), B18 (sinalizar), B19 (exemplos), C4 (descrição), C7 (duplicar) |
| Ir além | Validade de TLS; conteúdo na sonda |

B6, A11, B9, B10 e B13 dependem de D6, D10 e D14 (seção 3): se o dono mantiver a decisão atual, o item sai do escopo.

**Fora de escopo.** Ping por e-mail (B12, exige recepção de e-mail e terá spec própria); validade de domínio; canais novos
(SP2); API por chave (SP3); badges (SP4); projetos e equipes (SP5); regex em regra de conteúdo; botão "testar regra" no
painel (seria um segundo caminho de saída para URL de usuário); aviso de certificado renovado; cadência de aviso por alvo.

## 2. Fatos do código que limitam o desenho

| Fato | Consequência |
|---|---|
| Maior migration: 00022; a 00015 é vaga para sempre (`TestMigration00015StaysVacant`) | Numeração começa acima da 00022 |
| `storage.Migrate` recusa número menor que o do banco | **Não dá para deixar a 00023 reservada ao Discord**: uma 00024 aplicada antes impediria o boot. Cada PR pega o próximo número livre na hora do merge. O plano do Discord já diz "se nada entrar antes" e muda uma linha |
| #236 trouxe `TestEveryTableForcesRLSWithAPolicy`, com `totp_recovery_codes` na allowlist e sem migration | Toda tabela nova já é coberta pelo teste; nenhuma migration de TOTP disputa número |
| O rollback real é binário anterior sobre schema novo: o goose só roda `Up` no boot | Toda linha nova precisa ser inofensiva ao binário anterior, e o `Down` é o segundo plano |
| `toHeartbeat`, `registrar` e `updateParams` chamam `ScheduleFromRow`, e ela aplica as faixas de cadastro na leitura | Período acima de 31 dias, ou linha só com OnCalendar, derruba a página inteira em `Service.List` e dá 500 no ping e no PATCH do binário atual. O PR 1 define o piso de rollback (5.1 e seção 12) |
| Tolerância e descrição acima da faixa antiga só são barradas na escrita | Inofensivas na leitura do binário atual; reenviá-las no PATCH dá 400, sem gravar |
| `Schedule{}` tem `Next(t) = t` | Tolerar linha desconhecida com o valor zero faria o prazo virar `agora + tolerância` e abriria alarme falso. Precisa de forma explícita (5.1) |
| `POST /ping/{token}/{exit_code}` casa qualquer segundo segmento | Slug não cabe em `/ping/{chave}/{slug}`; precisa de prefixo próprio |
| `notifications.incident_id` é NOT NULL | Aviso de certificado precisa de arco ou outbox própria |
| `incidents_one_open_per_target`, e a status page lê `incidents` | Aviso de certificado não pode ser incidente |
| A sonda abre conexão nova a cada vez e lê no máximo 64 KiB; até 64 em voo | Dá para observar certificado e corpo sem custo de rede extra |
| `resp.TLS` descreve o último salto do redirect | O certificado precisa vir do handshake do host cadastrado |
| Certificado vencido já falha o handshake e abre incidente `tls` | SP1 só cobre o aviso **antes** do vencimento |
| `conferirSonda` cerca URL e método | A regra de conteúdo precisa entrar na cerca |
| #236 já está em `origin/main`: resolução única com recusa de nome misto em `safedial`, sonda de alvo pausado descartada, `userAgentGravavel` e `consultaPermitida` | Nenhuma dependência de branch pendente. Rotas novas com query usam a allowlist (seção 7) |
| `targets_public_select` abre alvos da status page ao escopo público | Política que só delega ao alvo, como `target_health`, abriria a tabela nova junto (seção 6) |
| Merges congelados até 11/11, 14:00 UTC, e merge é deploy | Nenhum PR deste plano antes disso; respeitar os bloqueios de publicação vigentes |

## 3. Escolhas do dono (não decididas aqui)

### D6: métodos aceitos no ping

Hoje: só POST; GET responde 405.

| Opção | Prós | Contras |
|---|---|---|
| A. Manter só POST | Prévia de link e scanner nunca escondem uma tarefa parada; nenhum código | Quem só consegue GET (dispositivo simples, URL em campo de monitor externo) fica de fora |
| B. Opção por tarefa, com só POST como padrão | Compatibilidade onde o dono pedir; POST segue protegido | Naquela tarefa, GET acidental conta como sinal; exige 405 idêntico para token inexistente e tarefa só POST, senão vira oráculo |
| C. GET liberado em todas | Máxima compatibilidade | Reabre o risco que D6 fechou, em todas as tarefas |

**Recomendação: B.** Motivo: atende o caso real sem mudar o padrão. Custo: uma coluna e o aviso no formulário.

### D10: efeito do sinal de início no prazo

Hoje: só o sucesso move o prazo; um início sem fim vira atraso no prazo normal.

| Opção | Prós | Contras |
|---|---|---|
| A. Manter | Regra única, nenhum código | Tarefa travada só aparece no prazo normal, que pode ser um dia depois |
| B. `início + tolerância`, opcional | Paridade direta, um booleano | Tarefa de 2 h com tolerância de 5 min dá alarme falso; subir a tolerância atrasa a detecção de silêncio |
| C. `max_run_sec` próprio, opcional | Separa duração de tolerância; cobre B quando os valores coincidem; NULL preserva o comportamento atual | Um campo a mais no formulário |

**Recomendação: C.** Difere do "`start + grace`" registrado em `decisoes-design.md`, pelo contra de B. Custo: um campo e os
testes de interação com o prazo. Se D10 for B ou C, a regra está em 5.1, "Limite de execução"; se for A, ela sai.

### D14: corpo e metadados do ping

Hoje: corpo descartado, teto de 10 KiB, nenhum IP nos eventos. Conta bruta, sem teto próprio, com 1000 eventos de 100 kB:
até 10 GB por usuário.

| Opção | Prós | Contras |
|---|---|---|
| A. Manter descartado | Nenhum dado de terceiro em disco | Sem diagnóstico; B9, B10 e B13 saem. O filtro B11 funciona igual, em memória |
| B. Opt-in por tarefa: 10 KiB, 100 corpos por tarefa, cifrado; guarda método e User-Agent truncado, sem IP | Teto por construção: 100 tarefas x 100 corpos x 10 KiB = 100 MiB por conta, sem contador concorrente | Log de tarefa pode conter segredo; backup cresce; disco da VPS sem verificar |
| C. Como B, mais IP de origem | Identifica de onde veio o sinal | IP é dado pessoal; contraria "dado que não é necessário não entra" |

**Recomendação: B.** Custo: até 100 MiB por conta, mais o excedente até a manutenção (seção 11), e dado de terceiro em
disco, por escolha de cada tarefa.

## 4. Abordagens

| | A. Estender o caminho existente | B. Verificadores separados | C. Asserções genéricas |
|---|---|---|---|
| Como | TLS e conteúdo saem da requisição que a sonda já faz; colunas em tabelas atuais; tabela lateral só para 1:N, 1:1 opcional ou dado sensível | Job diário com conexão própria para TLS; requisição própria para conteúdo | Tabela `assertions` (tipo + `jsonb`) e motor que as avalia |
| Prós | Nenhuma conexão a mais; um único caminho pelo `safedial`; check, regra e aviso na mesma transação de `gravar`; cada função é um PR pequeno | Lê certificado mesmo se o HTTP falhar; não depende do intervalo do alvo | Tipo novo sem migration |
| Contras | Sem sonda não há aviso (alvo pausado fica sem); `Probe` ganha responsabilidade | Dobra saídas com 0,5 CPU numa VPS compartilhada; segundo caminho de SSRF; mais um laço para encerrar no shutdown | Perde CHECK e FK por tipo; `jsonb` para estrutura fixa; flexibilidade sem segundo caso pedido |

**Recomendação: A.** Motivo: não cria superfície nova de SSRF nem gasta conexão, e o alvo pausado sem aviso é coerente com
pausar. Custo: `Probe` cresce; a decisão fica em funções puras (`avaliarConteudo`, `estagioDoCertificado`).

## 5. Comportamento

### 5.1 Heartbeats

- **Faixas.** Período de 60 s a 365 dias; tolerância de 30 s a 365 dias (o mínimo atual fica, porque `EarlySignalMargin`
  depende dele); descrição até 10.000 caracteres.
- **Leitura compatível (PR 1).** A remontagem da linha deixa de aplicar a faixa de cadastro: período positivo vale até o que o
  CHECK permitir. O cadastro segue em 31 dias nesse binário, e reenviar um período maior no PATCH dá 400, sem gravar.
- **Agendamento desconhecido (PR 1).** Linha sem período e sem cron vira uma forma explícita de `Schedule`, nunca o valor zero.
  Vale para o binário que não entende a forma nova, durante um rollback:
  - O ping responde o 200 fixo, grava evento e `last_*`, e **não altera `deadline_at`**, em nenhum sinal. A forma desconhecida
    nunca chega a `FirstDeadline`.
  - O avaliador ignora o prazo dessa linha: não abre incidente por atraso e não exige prazo futuro para fechar. Falha avisada
    continua abrindo `heartbeat_failed`, e o sucesso seguinte fecha.
  - A saída traz `schedule.kind = 'unknown'`, o estado ignora o prazo, e pausa e falha seguem valendo.
  - PATCH e duplicar respondem 409 com código novo, sem gravar; DELETE e rotação de token seguem.
  - Custo aceito: enquanto durar o rollback, atraso dessa tarefa não avisa. Ao voltar a um binário que entende a linha, o prazo
    parado pode abrir um incidente, que fecha no próximo sucesso.
- **Em tolerância (A9).** Estado derivado na leitura, sem coluna: `deadline_at - grace` <= agora < `deadline_at`, sem
  execução aberta. Não avisa: o aviso continua no prazo.
- **Retomada por ping (A13).** `ping_resumes`, padrão falso, equivalente ao D13 atual. Ligado, só o sucesso retoma, grava
  `resumed_at` e recalcula o prazo.
- **`/log` (B4).** Grava evento e não mexe em `last_signal`, prazo nem execução. Divide o teto de 1000 eventos da tarefa com os
  demais sinais, e o excesso apara os mais antigos: aceito, com o balde por token limitando o ritmo.
- **Limite de execução (A11, só se D10 for B ou C).** Tudo na transação do ping:
  - O limite é a tolerância, com a opção ligada, se D10 for B; é `max_run_sec`, quando não nulo, se D10 for C. Desligado, o
    início não mexe no prazo, como hoje.
  - No início, `deadline_at = min(deadline_at, início + limite)`. O início só antecipa, nunca adia.
  - Com `rid` sobreposto, vale o início mais antigo sem sucesso depois dele, por consequência do `min`. Início repetido em 10 s,
    com o mesmo `rid`, não tem efeito.
  - O sucesso de qualquer `rid` recoloca o prazo pelo agendamento, como hoje; outra execução ainda aberta fica sem limite até o
    próximo início. Falha e `/log` não alteram o prazo.
  - Vencido o limite, abre incidente `heartbeat_late`, com `started_at` igual ao prazo. Não há valor novo em `incidents.cause`:
    o avaliador só enxerga `deadline_at`, e o binário anterior lê o incidente sem mudança.
  - PATCH que muda agendamento ou tolerância recalcula o prazo como hoje e descarta o limite em curso; mudar só o limite vale
    a partir do próximo início.
- **Run id (B7).** `?rid=<uuid>`. Duração vem do início de mesmo `rid`. `isRepeat` passa a comparar o `rid`: dois sucessos
  de execuções distintas em 10 s não são repetição. Sem `rid`, tudo fica como hoje. `rid` malformado sempre responde 400,
  antes de olhar o token, e não vira oráculo.
- **Filtros (B11).** Só na rota sem sufixo: sufixo explícito vence. Comparação de bytes, sensível a maiúsculas. Prioridade:
  falha, sucesso, início. Sem casar: `success` (padrão, o de hoje), `fail` ou `ignore` (grava `log`).
- **Slug e chave (B2).** Uma chave por conta, 256 bits, só o hash fica no banco, mostrada uma vez. A tela "ver chave" do
  levantamento não existe: só gerar e rotacionar. Slug único por dono: sem ambiguidade e sem 409 no ping.
- **Autoprovisionamento (B8).** Desligado por padrão, por chave. Com ele, `?create=1` em slug inexistente cria tarefa de
  1 dia com 1 h de tolerância e nome igual ao slug, respeitando o limite de 100, e aplica à tarefa nova o sinal recebido.
  - Slug fora do formato da seção 7 responde o mesmo 404 de slug inexistente, antes do banco, e não cria.
  - `Service.Create` traduz a violação de `UNIQUE (user_id, slug)` em erro próprio. No `?create=1`, quem perde a corrida aplica o
    sinal à tarefa que o vencedor criou: dois pedidos simultâneos dão uma tarefa e dois 200, nunca 500. No painel, 409.
  - Com 99 tarefas, o perdedor conta 100 e recebe `ErrLimitReached` antes do `UNIQUE`. Por isso, depois desse erro, relê o slug:
    se a tarefa existe, aplica o sinal; se não, responde o erro de limite.
  - `GET` e `HEAD` nunca criam, em qualquer opção de D6 (seção 7.1).
- **Sinalizar agora (B18).** Aplica um sucesso no escopo do dono, com `source = 'panel'`.
- **OnCalendar (A3).** Subconjunto estrito, sem dependência nova:
  - atalhos `minutely`, `hourly`, `daily`, `weekly`, `monthly`, `yearly`;
  - forma `[DiaDaSemana] [Ano-]Mês-Dia Hora:Minuto[:00]`, com `*`, listas, faixas `a..b` e passo `/n`;
  - segundos só `00`; `~` (último dia) e fuso dentro da expressão ficam de fora; o fuso é o campo obrigatório de hoje;
  - até 16 linhas e 1000 caracteres; o próximo disparo é o menor entre as linhas;
  - expressão sem disparo em 5 anos é recusada, como no cron;
  - horário de verão: instante inexistente vai para o seguinte válido, ambíguo usa a primeira ocorrência, fixados por testes
    com valores literais gerados uma vez com `systemd-analyze calendar`.

### 5.2 Validade do certificado

1. A sonda anexa `httptrace.TLSHandshakeDone` ao contexto, atrás de mutex.
2. Vale o handshake cujo `ServerName`, canonicalizado com `DialedHost`, é o host cadastrado. Redirect para terceiro não gera
   aviso sobre certificado alheio. URL `http` sem salto para o mesmo host: "não se aplica". URL com IP literal também: o cliente
   TLS não envia SNI para IP, e o `ServerName` vem vazio. A comparação remove o ponto final dos dois lados: em
   `https://exemplo.com./`, `DialedHost` mantém o ponto e o cliente TLS o tira do `ServerName`. `DialedHost` não muda, porque
   serve às barreiras de SSRF.
3. Validade efetiva: o maior, entre as `VerifiedChains`, do menor `NotAfter` de cada cadeia. Assim um intermediário que vence
   antes da folha conta, e uma cadeia alternativa válida não dá falso aviso.
4. `gravar` atualiza `target_tls` na mesma transação, por uma função pura. A linha guarda o pior certificado visto:
   - leitura menor que a guardada substitui na hora;
   - leitura igual só renova `observed_at`, se ele tiver mais de 1 h;
   - leitura maior só substitui depois que a guardada ficou sem ser vista por `max(6 h, 3 x intervalo do alvo)`.
5. Estágios: **14, 7 e 3 dias**, em constante. O levantamento citava 30/14/7, mas certificado de 90 dias costuma renovar
   perto de 30 dias restantes, e o primeiro aviso seria ruído. **Item para o dono revisar.**
6. Dias restantes: `ceil((not_after - instante da leitura) / 24 h)`, sem fuso. 14 dias exatos dão 14, e 14 dias + 1 s dão 15.
   Vale o menor estágio maior ou igual aos dias restantes; acima de 14, nenhum. `UNIQUE (target_id, not_after, stage_days)`
   garante um aviso por certificado e estágio. A primeira leitura emite só o estágio atual, sem os maiores: 5 dias emitem só o
   7, e 2 dias + 1 h (3 dias restantes) só o 3. Painel e aviso usam o mesmo cálculo; no aviso, com o instante da fila.
7. Só a substituição por validade maior é renovação. Aviso só nasce de leitura igual à guardada depois da atualização, então
   certificado já renovado não avisa durante a janela. O aviso pendente do certificado antigo é cancelado no envio (abaixo).
8. Balanceador com certificados diferentes: a alternância não grava a cada sonda nem cancela aviso válido, porque o pior
   certificado fica na linha até sumir pela janela. Limites aceitos: o nó com certificado melhor só é avisado depois que o pior
   sai, e um nó que escape de três sondas seguidas é tratado como renovação, de novo limitado pelo número de certificados.
9. Retenção: `ApplyRetention` apaga `tls_alerts` com `not_after` mais antigo que `NotificationsRetention` (30 dias) e sem
   notificação pendente.

**Entrega do aviso (PR 7).** Tornar `incident_id` anulável não basta para a linha sair:

- **Reivindicação.** `claimed` devolve `tls_alert_id`, e `ClaimDueNotifications` passa a usar `LEFT JOIN incidents i`,
  `LEFT JOIN tls_alerts a ON a.id = cl.tls_alert_id`, `LEFT JOIN targets t ON t.id = COALESCE(i.target_id, a.target_id)`,
  `LEFT JOIN heartbeats h ON h.id = i.heartbeat_id` e `LEFT JOIN target_tls tt ON tt.target_id = a.target_id`. Sem o `COALESCE`
  no alvo, `COALESCE(t.enabled, h.enabled)::bool` vem NULL, o Scan em `TargetEnabled bool` falha, e o erro derruba a reivindicação
  inteira a cada tique: a fila trava.
- **Colunas.** `incident_id` fica anulável na linha; `started_at`, `cause` e `closed_by_resume` usam `COALESCE` para manter os
  tipos. Entram `a.not_after`, `a.stage_days` e `tt.not_after`.
- **Cancelamento.** `semEnvio` cancela `tls_expiring` com alvo pausado ou com `target_tls.not_after` maior que
  `tls_alerts.not_after`. Só maior: validade menor é outro nó do balanceador, e o aviso continua válido.
- **Texto.** `mensagem()` ganha um ramo `tls_expiring` antes dos demais. Sem ele, o aviso diria "fora do ar desde" uma data zero.
  `Event` ganha `TLS` (`NotAfter`, `StageDays`), nome do contrato da SP2. O Discord não usa o marcador de queda nesse evento.
- **Enfileiramento.** Consulta própria, na transação de `gravar`, para cada canal de `ListDeliverableChannels`: `target_name` vem
  de `targets` por `tls_alerts.target_id`, porque `EnqueueNotification` tira o nome de `incidents`, e o conflito usa
  `ON CONFLICT (tls_alert_id, channel_id) WHERE tls_alert_id IS NOT NULL DO NOTHING`.
- **Ordem com a SP2.** D-10 está sem decisão. Se a SP1 vier primeiro, este PR cria o `LEFT JOIN` e a SP2 adapta `Event` e o
  envelope uma vez, com os tipos novos filtrados por D-11. Se a SP2 vier primeiro, o envelope nasce com `tls` e `incident_id`
  anulável, nada emite `tls_expiring` até aqui, e este PR mexe de novo na reivindicação (SP2, §6 e §15).
- **Canais.** Discord e e-mail recebem em todas as opções de D-11, então este PR não depende dela. D-11 só decide quais tipos
  novos da SP2 recebem o evento.

### 5.3 Conteúdo

| Modo | Passa quando | Observação |
|---|---|---|
| `contains` | O valor aparece nos primeiros 64 KiB | Bytes exatos |
| `not_contains` | O valor não aparece e o corpo coube em 64 KiB | |
| `json_equals` | O escalar no ponteiro RFC 6901 tem a forma de texto informada | Objeto ou array: não passa |

- A leitura vai até 64 KiB + 1 byte, e só quando há regra. Sem regra, o resultado não muda: o corpo é drenado até 64 KiB e
  descartado, e corpo grande segue não sendo falha.
- O transporte não pede gzip (`DisableCompression` no safedial): quem pede é a sonda, com `Accept-Encoding: gzip`, e quem
  descomprime é ela. Com regra, sempre. Sem regra, só quando o dono não mandou `Accept-Encoding` nem `Range`, a mesma guarda
  do transporte do Go; com um deles, o corpo é drenado cru, como antes.
- Dois tetos quando a sonda descomprime: 64 KiB sobre o corpo descomprimido (contra bomba de gzip) e 128 KiB sobre os bytes
  comprimidos lidos (um gzip que quase não gera saída seria lido até o prazo). Com regra, bater em qualquer um sem conclusão
  é `body_limit`; sem regra, a drenagem só para ali.
- Status esperado é avaliado antes; status errado continua `http_status`.
- Regra reprovada: down com a causa nova `content_mismatch`.
- Corpo comprimido num formato que a sonda não lê (tudo que não for exatamente um `gzip`, somando as linhas de
  `Content-Encoding`): down com a causa nova `content_encoding`. Inconclusivo, nunca `up`.
- Corpo cortado sem conclusão, ou JSON cortado: down com `body_limit`, que já está no enum e nunca foi emitido. Inconclusivo
  nunca vira `up`.
- Só o resultado sai de `Probe`. O corpo não vai a banco, log, métrica, aviso ou resposta de API.
- Regra recusada no cadastro (400 `validation_failed`, `content_rule` `forbidden`) com método HEAD, com header
  `Accept-Encoding` do dono (a sonda só lê o gzip que ela mesma pede) ou com header `Range` do dono (a regra julgaria só uma
  fatia, e a fatia de um gzip nem abre).
- `conferirSonda` passa a comparar também a regra: sonda em voo com regra antiga é descartada.

## 6. Modelo de dados e migrations

Números relativos, na ordem dos PRs da seção 12: `+0` é o próximo livre no merge. Toda descida com dado usa
`SELECT set_config('app.scope', 'prober', true)`, como 00012 e 00016. O escopo do sondador não tem política de DELETE em
`heartbeats`, então a descida converte em vez de apagar.

| Migration | Mudança | Descida |
|---|---|---|
| +0 `heartbeat_limits` | CHECKs de `period_sec`, `grace_sec` (até 31536000) e `description` (até 10000) | `LEAST` e `left` para as faixas antigas, depois CHECKs antigos |
| +1 `heartbeat_events_log_run` | `kind` aceita `log`; `run_id uuid`, `source text NOT NULL DEFAULT 'ping' CHECK (source IN ('ping','panel'))`; índice `(heartbeat_id, run_id, id DESC) WHERE run_id IS NOT NULL`; `heartbeats.last_run_id`, `ping_resumes` | `DELETE` de eventos `log`; remove índice e colunas; CHECK antigo |
| +2 `targets_content_rule` | `targets.content_mode`, `content_value`, `content_json_pointer`, com CHECK de tudo ou nada e de método diferente de HEAD; `content_mismatch` e `content_encoding` em `checks.error_kind` e `incidents.cause` | `content_mismatch` e `content_encoding` viram `unknown`; CHECKs antigos; remove colunas |
| +3 `target_tls` | `targets.tls_warn bool NOT NULL DEFAULT true`; `target_tls(target_id PK FK targets ON DELETE CASCADE, not_after, observed_at)`; `tls_alerts(id, target_id FK targets ON DELETE CASCADE, not_after, stage_days, created_at, UNIQUE)` | Roda depois da descida da +4, que já soltou a FK: `DROP TABLE tls_alerts` e `target_tls`; remove `targets.tls_warn` |
| +4 `notifications_tls_arc` | `incident_id` anulável; `tls_alert_id` FK `tls_alerts ON DELETE CASCADE`; `CHECK (num_nonnulls(incident_id, tls_alert_id) = 1)`; `UNIQUE (tls_alert_id, channel_id) WHERE tls_alert_id IS NOT NULL`; `event` aceita `tls_expiring`. Consultas em 5.2, "Entrega do aviso" | `DELETE` das linhas TLS; remove o `UNIQUE`, o CHECK `num_nonnulls` e a coluna `tls_alert_id`; `SET NOT NULL` em `incident_id`; CHECK antigo de `event` |
| +5 `ping_keys` | `ping_keys(user_id PK FK users ON DELETE CASCADE, key_hash bytea UNIQUE CHECK 32 bytes, allow_create bool, created_at, rotated_at)`; `heartbeats.slug` com `UNIQUE (user_id, slug) WHERE slug IS NOT NULL` | Remove políticas, coluna e `DROP TABLE` |
| +6 `heartbeat_filters` | 1:1, `heartbeat_id PK FK heartbeats ON DELETE CASCADE`: `start_keywords`, `success_keywords`, `fail_keywords` em `text[]` com CHECK `cardinality(...) <= 8`, `no_match`. CHECK não alcança o tamanho de cada elemento: 1 a 64 caracteres fica na validação do serviço, único caminho de escrita | `DROP TABLE` |
| +7 `heartbeat_options` (uma migration por decisão aprovada) | Se D6 for B: `ping_allow_get bool`. Se D10 for B: `start_uses_grace bool NOT NULL DEFAULT false`; se C: `max_run_sec int CHECK (30..31536000)`. Se D14 for B ou C: `keep_ping_body bool`, `heartbeat_events.method`, `.user_agent` (até 200) e tabela `heartbeat_event_bodies`; se C, também o IP de origem em `heartbeat_events` | `DROP` do que entrou |
| +8 `heartbeat_oncalendar` | `oncalendar_expr text`; `heartbeats_one_schedule` com `num_nonnulls(period_sec, cron_expr, oncalendar_expr) = 1` e fuso só em calendário ou cron | Linhas OnCalendar viram pausadas com período de 1 dia e sem expressão (o dono as vê pausadas, sem alarme falso); CHECK antigo |

**RLS em toda tabela nova** (ENABLE + FORCE, com `NULLIF` no GUC):

| Tabela | Políticas |
|---|---|
| `ping_keys` | Dono em ALL; SELECT no escopo de ping quando `key_hash = decode(app.ping_key_hash)` |
| `heartbeat_filters` | Dono em ALL (nomeando `user_id` do heartbeat); SELECT via heartbeat visível, para o ping |
| `heartbeat_event_bodies` (se D14 for B ou C) | SELECT e DELETE do dono; INSERT via heartbeat visível; SELECT e DELETE do sondador para a manutenção. **Sem SELECT no escopo de ping:** o token de uma tarefa grava corpos e não os lê |
| `target_tls`, `tls_alerts` | Dono ou sondador nomeados na política (`t.user_id = app.user_id OR app.scope = 'prober'`), como `incidents_via_heartbeat`. Nunca só "o alvo é visível", como `target_health`: o escopo público enxerga alvos da status page |

**Políticas novas em `heartbeats`.** SELECT e UPDATE no ping por slug exigem os dois GUCs juntos, `app.ping_key_hash` e
`app.ping_slug`: a transação enxerga exatamente uma linha, como `InPingTx`. Criação por `?create=1` valida a chave, descobre
o dono e chama `Service.Create` em `InUserTx`, sem política de INSERT no escopo de ping.

**Tabela de corpos (se D14 for B ou C).** `heartbeat_event_bodies(event_id PK FK CASCADE, heartbeat_id FK CASCADE, body_enc bytea, size_bytes int
CHECK (0..10240))`, índice `(heartbeat_id, event_id DESC)`. Cifrada com `crypto.Box`, AAD no id do evento, como os headers.

**Portão de PR em +2.** Trocar CHECK em `checks` particionada pega lock em todas as partições e varre as linhas. A 00013 usou
`NOT VALID`, mas numa tabela não particionada: testar no Postgres 18 se a forma vale aqui e medir o tempo com volume de 61
dias, com `lock_timeout`. A migration roda no boot, e o sondador grava nessa tabela.

## 7. API

Erros no envelope atual: 400 com `field` e `reason` (`required`, `invalid`, `out_of_range`), 404 igual para inexistente e
alheio, 413, 429 e 500 sem detalhe. Entrada por DTO estrito (`DisallowUnknownFields`); PATCH mantém `optional[T]`; saída campo a campo.

### 7.1 Ping público

Todas fora de CORS e de sessão, com os limites atuais, e nenhum log do app leva o caminho. O Caddy do repositório não tem
diretiva `log`; o log da borda do Coolify não foi localizado e precisa ser conferido antes do PR 8, porque a chave da conta viaja
no caminho.

| Rota | Efeito | Respostas |
|---|---|---|
| `POST /ping/{token}/log` | Registra | 200 fixo; 404 |
| `POST /ping/k/{chave}/{slug}` | Sucesso (ou resultado do filtro) | 200 fixo; 404 igual para chave ou slug errados |
| `POST /ping/k/{chave}/{slug}/start`, `/fail`, `/log`, `/{exit_code}` | Como as rotas de token | Idem |
| `?rid=<uuid>` em todas | Run id | 400 se malformado |
| `POST` com `?create=1` na rota por slug | Cria, se a chave permitir (5.1); slug existente recebe só o sinal | 400 se o valor não for `1`; 404 se não permitir ou o slug for malformado; erro de limite existente com 100 tarefas |
| `GET` e `HEAD` nas mesmas rotas (se D6 for B ou C) | Se B, sinal quando a tarefa permitir; se C, sinal sempre. `?create=1` não cria | Se B, 405 idêntico em todo caso que não registra sinal, inclusive slug inexistente com `?create=1`; se C, como o POST, com 404 em slug inexistente |

- Rotas por slug aceitam só `rid` e `create` na query (allowlist, como `consultaPermitida`); desconhecido é 400. Rotas por token
  leem só `rid` e seguem ignorando o resto, para não quebrar quem já chama.
- Balde por tarefa chaveado pelo hash de chave + slug, e balde por chave, também pelo hash, de 600 por minuto e rajada de 120
  (provisório). Token e slug têm baldes separados: uma tarefa chamada pelos dois caminhos soma 60 por minuto. Aceito: unificar
  exigiria ir ao banco antes do limitador.
- Se D14 for B ou C, toda resposta leva `Ping-Body-Limit: 10240`. Corpo acima do teto segue 413 sem efeito, como hoje.

### 7.2 API autenticada

| Rota | Entrada | Saída |
|---|---|---|
| `GET /api/v1/heartbeats/oncalendar-preview` | `expr`, `timezone` | Próximos disparos, como `cron-preview` |
| `POST`/`PATCH /api/v1/heartbeats` | Novos: `schedule.kind = 'oncalendar'`, `slug`, `ping_resumes`, e os de D6, D10, D14 | Novos: `slug`, `ping_resumes`, `state` com `grace` |
| `GET /api/v1/heartbeats/{id}/timeline` | `limit`, `cursor` | `items[]`: `{kind, at, ...}` por tipo, sem endereço de canal; `next_cursor` |
| `GET`/`PUT /api/v1/heartbeats/{id}/filters` | Listas e `no_match`; listas vazias com `success` removem a regra | O estado salvo |
| `POST /api/v1/heartbeats/{id}/events/clear` | Vazio | 204; grava em `audit_log`; não mexe em `last_*` |
| `POST /api/v1/heartbeats/{id}/ping-now` | Vazio | O heartbeat |
| `POST /api/v1/heartbeats/{id}/clone` | Vazio | 201 com `ping_url`, uma vez; sem slug, eventos nem token copiados; conta no limite |
| `GET /api/v1/heartbeats/{id}/events/{event_id}/body` (se D14 for B ou C) | `download=1` opcional | Texto com `nosniff`, em `attachment` com `download=1`; 404 sem corpo |
| `GET`/`PATCH /api/v1/ping-key` | `allow_create` | `{exists, allow_create, created_at, rotated_at}`, nunca o valor |
| `POST /api/v1/ping-key/rotate` | Vazio | `{ping_key}`, uma vez |
| `POST`/`PATCH /api/v1/targets` | Novos: `content_rule` (null limpa), `tls_warn` | Novos: `content_rule`, `tls_warn`, `tls: {not_after, observed_at} ou null` |

Eventos ganham `run_id`, `source` e, se D14 for B ou C, `method`, `user_agent`, `has_body`.

Query de `oncalendar-preview`, `timeline` e `body` passa pela allowlist da rota, com a leitura de `consultaPermitida` (#236):
parâmetro desconhecido é 400. Na prévia, repetido também é 400, como em `cron-preview`.

**Limites.** Slug `^[a-z0-9][a-z0-9_-]{0,99}$`, recusando colisão com 409 (código novo), inclusive em corrida. Palavras: até 8 por lista, 1 a 64
caracteres. Regra de conteúdo: valor de 1 a 256 caracteres, ponteiro até 256 começando em `/`. OnCalendar conforme 5.1.
Toda string rejeita NUL e caracteres de controle. User-Agent gravado passa por `userAgentGravavel` (#236).

**Desvio registrado.** A regra geral pede `If-Match` em PUT e PATCH, e a API de heartbeats não usa. O SP1 mantém o padrão atual
por haver um dono só; revisar em SP3 ou SP5.

## 8. Telas do painel

Seguem o Editorial claro e as decisões de `decisoes-design.md`: erro neutro com "Tentar de novo", erro de envio fica até
dispensar, botão habilitado com erro ao enviar, alvo de toque de 44 px, cor nunca como único sinal. Carregando e erro usam
`EstadoDaConsulta` com esqueleto.

| Tela | Conteúdo | Vazio | Carregando | Erro |
|---|---|---|---|---|
| Formulário de tarefa | Agendamento com OnCalendar e prévia, slug, retomada por ping e opções de D6, D10, D14 em "Avançado" | Prévia sem expressão: frase de exemplo | Prévia: "Calculando…" em texto | Campo culpado, sem apagar o digitado |
| Regras de filtragem (diálogo) | Três listas e "se nada casar" | "Sem regras: todo sinal é sucesso" | Esqueleto | Aviso neutro com "Tentar de novo" |
| Log unificado | Sinais, transições e avisos, com selo, ícone e texto; substitui as duas listas atuais | "Nenhum sinal recebido" e link para a URL de ping | Esqueleto com linhas | Aviso neutro; dados velhos ficam com o aviso |
| Detalhe de evento | Horário, tipo, duração, código de saída, `rid`; se D14 for B ou C, método, User-Agent, corpo, baixar | "Esta tarefa não guarda o corpo" | Esqueleto no corpo | Erro só no bloco do corpo |
| Ações no detalhe | Sinalizar agora, Duplicar, Limpar eventos (com confirmação) | Limpar some sem eventos | Botão ocupado, sem duplo clique | Aviso persistente |
| Chave de ping (Configurações) | Estado, rotacionar com confirmação, permitir criação | "Nenhuma chave" e botão primário "Gerar chave" | Esqueleto | Aviso neutro; valor novo só no diálogo |
| Diálogo da URL | Abas token e slug, exemplos de `curl`, `rid`, `/log` | Slug sem chave: link para Configurações | Não se aplica | Não se aplica |
| Alvo: formulário | Regra de conteúdo em "Avançado", aviso de TLS | Regra desligada por padrão | Não se aplica | Campo culpado; HEAD explica por que a regra não cabe |
| Alvo: detalhe | "Certificado válido até …, em N dias"; textos das causas `content_mismatch` e `content_encoding` | `http`: "não se aplica"; sem leitura: "Aguardando a primeira sonda" | Esqueleto do indicador | "Indisponível", sem acusar o alvo |

`schemas.ts` tem enums estritos: a tolerância a `kind` desconhecido e a `schedule.kind = 'unknown'` entra no primeiro PR. A
tarefa nesse estado aparece com "Agendamento não suportado nesta versão", sem edição.

## 9. Testes

**Regra.** Teste novo só vale depois de visto falhar com o código sabotado, registrando a sabotagem no corpo do PR. Valor
esperado é literal. Antes de dizer que funciona: `go build ./... && go vet ./... && make test`.

| Área | Teste | Sabotagem que precisa quebrá-lo |
|---|---|---|
| Faixas | 365 dias passa, 365 dias + 1 s não, nos dois lados (validação e CHECK) | Tirar um dos limites |
| Em tolerância | `grace` em `deadline - grace`, não um instante antes, não no `deadline`; não com execução aberta | Trocar `<` por `<=` |
| `/log` | Prazo e `last_signal` intactos; conta no teto de 1000 | Fazer `Apply` tratar como sucesso |
| Limite de execução (se D10 for B ou C) | Início antecipa o prazo com valor literal; início nunca adia; dois `rid` sobrepostos ficam com o mais antigo; sucesso recoloca o prazo; opção desligada não muda nada; vencido, abre `heartbeat_late` | Trocar `min` por atribuição; somar o limite ao prazo atual |
| `rid` | Duas execuções sobrepostas têm durações corretas; sucessos de `rid` distintos em 10 s gravam dois eventos | Ignorar `rid` em `isRepeat` |
| Filtros | Tabela: prioridade, sem casar, sufixo vence, corpo vazio | Inverter a prioridade |
| OnCalendar | Tabela literal; passagens de verão; 16 e 17 linhas; `~` recusado | `Next` devolvendo a primeira linha |
| Rollback | Binário do PR 1 lista e pinga com período de 365 dias, tolerância de 365 dias, descrição de 10.000 caracteres, linha OnCalendar, `log`, slug e aviso TLS presentes. No PR 1, a linha sem agendamento nasce em banco de teste sem o CHECK; PR 2 e PR 12 ensaiam o binário publicado sobre o schema novo | Remover a tolerância; reaplicar `MaxPeriod` na leitura |
| Outbox no piso (PR 1) | Linha pendente com `incident_id` NULL, em banco de teste sem o NOT NULL, fica fora do lote: `attempts` e `next_attempt_at` literais intactos; a linha de incidente no mesmo tique sai | Tirar `incident_id IS NOT NULL` do `lote` |
| Agendamento desconhecido | Sucesso grava evento e `last_*`, responde 200 e mantém o `deadline_at` literal; avaliador não abre por atraso; falha abre `heartbeat_failed` e o sucesso fecha; PATCH e duplicar dão 409 | Devolver `Schedule{}` em `ScheduleFromRow`; tirar o filtro do avaliador |
| `?create=1` | Dois simultâneos: uma tarefa, dois 200, nenhum 500; com 99 tarefas, os dois também; com 100 e slug novo, erro de limite; slug malformado dá 404 sem criar; `create=2` dá 400; GET não cria (se D6 for B ou C) | Propagar a violação do `UNIQUE`; não reler o slug depois de `ErrLimitReached`; validar o slug depois do banco |
| Query | Parâmetro desconhecido em cada rota nova com allowlist: 400 | Trocar a allowlist por `r.URL.Query()` |
| Descida | `Up`, dados, `Down`, `Up` em cada migration | Tirar o `set_config` do `Down` |
| Sonda: conteúdo | Tabela de modos; corte em 64 KiB exatos e + 1; JSON cortado; bomba gzip | Devolver `up` no inconclusivo; ler sem limite |
| Sonda: cerca | Regra trocada em voo é descartada | Tirar a regra de `conferirSonda` |
| TLS | Intermediário vence antes da folha; duas cadeias; redirect para outro host; `http`; IP literal; `https://exemplo.com./` | Usar `resp.TLS`; usar só a folha; comparar com o ponto final |
| TLS em balanceador | Alternância A/B mantém o menor `not_after`, sem gravar a cada sonda e sem cancelar aviso; validade maior só substitui depois da janela, nas bordas de `max(6 h, 3 x intervalo)` | Gravar sempre a última leitura |
| Aviso TLS | Um por estágio com 2 sondas simultâneas; 14 dias exatos emitem o 14, e 14 dias + 1 s nada; primeira leitura emite só o estágio certo; renovação cancela pendente; leitura maior na janela não avisa; retentativa repete o texto; retenção apaga o vencido e mantém o pendente | Tirar o `ON CONFLICT`; calcular dias com o relógio atual; trocar `ceil` por `floor` |
| Dispatcher | Lote com linha TLS e linha de incidente não falha no Scan; linha TLS sai com nome e validade, sem "fora do ar", e não entra em lembretes; alvo pausado e `target_tls.not_after` maior cancelam; igual ou menor envia | Reintroduzir `JOIN` obrigatório; juntar `targets` só por `i.target_id`; trocar `>` por `<>`; tirar o ramo de `mensagem()` |
| RLS | Cobertura em toda tabela nova; escopo de ping não lê corpo; escopo público não lê `target_tls` nem `tls_alerts`; par de GUCs incompleto ou vazio não casa | Remover `FORCE`; afrouxar a política; delegar só ao alvo |
| `ON DELETE` | Apagar alvo, usuário, heartbeat e aviso leva as linhas dependentes | Tirar um `CASCADE` |
| Acesso | Usuário B em cada rota nova de A: 404, corpo sem dado de A, recurso intacto | Tirar o filtro de dono |
| Entrada | Campo extra, NUL, borda e acima da borda: 4xx, nunca 500 | Tirar `DisallowUnknownFields` |
| Saída | Conjunto de chaves exato; `key_hash` nunca aparece | Acrescentar campo à resposta |
| Painel | Vazio, carregando e erro de cada tela; contraste nos dois temas | Remover o estado |

O teste com TLS real precisa de CA de teste no cliente: `NewClientAllowing` não aceita `RootCAs` hoje. Ampliar por
`export_test`, sem nenhum caminho de produção para desligar verificação.

## 10. STRIDE

Fluxos: URL de usuário (sonda), TLS, ping público, corpo e metadados.

| # | Ameaça | Mitigação | Teste |
|---|---|---|---|
| S1 | Chave de ping vazada sinaliza tarefas | Hash, rotação, 404 uniforme, balde por chave | `TestPingKeyRotationRevokesTheOldKey` |
| S2 | Adivinhar slug | Exige chave de 256 bits; resposta igual | `TestSlugPingWithWrongKeyMatchesUnknownSlug` |
| S3 | Prévia de link faz GET (D6) | Padrão só POST; 405 uniforme | `TestGetOnPostOnlyMatchesUnknownToken` |
| T1 | Alterar corpo guardado | AAD no id do evento | `TestBodyFromAnotherEventDoesNotOpen` |
| T2 | Sonda antiga grava sob regra nova | Cerca em `conferirSonda` | `TestProbeDiscardedWhenRuleChanged` |
| T3 | Certificado não verificado influencia aviso | Só `VerifiedChains` | `TestUnverifiedCertificateRecordsNothing` |
| R1 | Limpar log sem rastro | `audit_log` | `TestClearEventsWritesAudit` |
| R2 | Sinal manual confundido com a tarefa | `source = 'panel'` | `TestPingNowIsMarkedAsPanel` |
| I1 | Regra de conteúdo vaza corpo | Só o resultado sai; canário | `TestProbeBodyCanaryNeverLeaves` |
| I2 | Sonda alcança rede interna com a regra | Mesmo cliente `safedial`; salto interno dá `blocked` | `TestNewUsesTheSSRFGuardedClient`, `TestContentRuleRedirectToInternalIsBlocked` |
| I3 | Segredo em log de tarefa no disco e no backup | Opt-in, cifrado, aviso no formulário, sem log | `TestPingBodyIsEncryptedAtRest`, `TestBodyCanaryAbsentFromLogs` |
| I4 | Token de uma tarefa lê corpos | Sem SELECT no escopo de ping | `TestPingScopeCannotReadBodies` |
| I5 | Corpo renderizado no painel executa | Texto escapado, `nosniff`, `attachment` | `TestBodyDownloadHeaders` |
| I6 | Timeline revela endereço de canal | DTO só com tipo e estado | `TestTimelineKeysAreExact` |
| I7 | IP como dado pessoal | Não coletado se D14 for A ou B | `TestEventsHaveNoIPColumn` |
| I8 | Status page expõe validade de certificado | Política nomeia dono ou sondador | `TestPublicScopeCannotReadTargetTLS` |
| D1 | Resposta enorme ou comprimida | Teto sobre o descomprimido; prazo da sonda | `TestGzipBombStopsAtLimit` |
| D2 | Ping encher disco | Teto de 100 corpos por tarefa, balde por token | `TestMaintenanceTrimsBodiesToTheCap` |
| D3 | Chave criar tarefas sem fim | Desligado por padrão; limite de 100 | `TestCreateRespectsLimitAndSwitch` |
| D4 | OnCalendar caro | 16 linhas, 1000 caracteres, busca de 5 anos | `BenchmarkOnCalendarNext`, `TestOnCalendarLimits` |
| E1 | Escopo de ping alcança outra linha | Par de GUCs, uma linha só | `TestSlugScopeSeesExactlyOneRow` |
| E2 | Criação por chave ganha sessão | `InUserTx` chama só `Service.Create` | `TestCreateByKeyDoesNotAuthenticateApi` |

S3 vale se D6 for B ou C; T1, I3, I4, I5 e D2 valem se D14 for B ou C.

Resíduos aceitos: o resultado de uma regra diz ao dono algo sobre uma URL pública que ele cadastrou; quem tem a chave
sinaliza qualquer slug da conta, como desenhado.

## 11. Custo na VPS compartilhada

Referência: app com 0,5 CPU, `mem_limit` 512m, `GOMEMLIMIT` 384 MiB, dos quais 256 MiB são o pior caso do Argon2; banco com
512m. Pico medido citado no compose: 13 MiB sem login. **Tudo abaixo é estimativa.**

| Item | Memória, pior caso | CPU | Disco e banco |
|---|---|---|---|
| Regra `contains` | 64 x 64 KiB = 4 MiB | Dezenas de microssegundos por sonda | Colunas em `targets` |
| Regra JSON | ~25 MiB, com 64 sondas simultâneas em JSON de 64 KiB | Cerca de 1 ms por sonda | Nenhum |
| TLS | Bytes por sonda | Sem handshake extra | Até 1 gravação por hora e alvo, mais uma por troca de certificado |
| Dois limitadores novos | 300 a 400 bytes por balde x 100.000 (`maxBuckets`) x 2 = 60 a 80 MiB | Desprezível | Nenhum |
| Corpo no ping (se D14 for B ou C) | 300 de rajada x 10 KiB = 3 MiB | Cifra desprezível | Até 100 MiB por conta; excedente até a manutenção de 15 min: até 450 corpos (4,5 MiB) por tarefa em abuso com token vazado |
| Eventos | Nenhuma | Nenhuma | ~16 a 250 bytes por linha, 100 mil linhas por conta |
| OnCalendar | Desprezível | Parse a cada ping, como o cron | Coluna |
| Timeline | Página de até `MaxPageSize` | União de 3 consultas limitadas | Índices existentes, a confirmar com `EXPLAIN` |

**Limitadores, também estimativa.** O balde soma chave hexadecimal de 64 caracteres, entrada no mapa, nó da lista e
`rate.Limiter`; o número não foi medido. Tirados os 256 MiB do Argon2, sobram 128 MiB do `GOMEMLIMIT`. Regra JSON, corpo e
limitadores novos somam 88 a 108 MiB, antes do heap atual e dos limitadores que já existem. Encher os baldes pede cerca de 9
origens, com o balde por IP de 1200 por minuto e 10 min de ociosidade.

**Portões antes do merge:** benchmark da regra JSON com `-benchmem`; pico real em ensaio com 64 sondas, os 4 Argon2 e os dois
limitadores cheios (se a soma passar da folga, o PR reduz o teto dos limitadores novos; `rateLimit` já recebe o teto como
parâmetro); disco livre na VPS conferido antes de ativar D14; tamanho do backup observado. Sem laço novo nem conexão nova: o
pool segue em 10.

## 12. Plano em PRs

Cada PR com código roda o CI completo (cerca de 30 min de Actions), e merge é deploy. Se o orçamento apertar, juntar 8 com 9.
Conferir o run do release por SHA antes de anunciar.

**Piso de rollback.** O PR 1 vai sozinho para produção, conferido por SHA, antes do PR 2 e do PR 12. Depois que existir linha
com período acima de 31 dias, com OnCalendar ou aviso TLS na outbox, o rollback só vai até o binário do PR 1. O PR 1 também
exclui `incident_id IS NULL` do `lote` de `ClaimDueNotifications`: sem isso, um binário sem o `LEFT JOIN` reivindicaria a linha
TLS, contaria a tentativa, perderia a linha no `JOIN` e a fecharia como `failed` acima de `MaxAttempts`. Com o filtro, ela fica
pendente e intacta até voltar um binário com o PR 7, que o remove.

**Custo de descer abaixo do piso.** `storage.Migrate` só chama `provider.Up`; `Down` é operação manual com a CLI do goose, uma
versão por vez, da mais nova para a mais antiga. Desfazer a +0 obriga a passar por todas as posteriores aplicadas, e cada uma
perde dado: OnCalendar vira pausa (+8), opções e corpos (+7), filtros (+6), chave e slugs (+5), avisos e validades (+4 e +3),
regras de conteúdo (+2), eventos `log` e `run_id` (+1). Se for inevitável, o caminho barato é um `UPDATE` pontual no escopo do
sondador, com a conversão das descidas: período com `LEAST`, OnCalendar pausado, eventos `log` apagados e TLS pendente cancelada.

| PR | Conteúdo | Migration | Depende de |
|---|---|---|---|
| 1 | Leitura compatível, agendamento desconhecido no ping, no avaliador e na API, `lote` sem `incident_id` NULL, tolerância a `kind`; sem usar nada novo | Nenhuma | Fim do congelamento |
| 2 | Faixas, descrição, duplicar, limpar, sinalizar, exemplos | +0 | 1 publicado antes |
| 3 | `/log`, `rid`, `source`, estado em tolerância, retomada por ping | +1 | 1 |
| 4 | Linha do tempo unificada | Nenhuma | 3 |
| 5 | Conteúdo na sonda e no alvo | +2 | Portão de lock |
| 6 | Leitura e exibição do certificado, sem aviso | +3 | 5 |
| 7 | Aviso de certificado pelo arco: reivindicação com `LEFT JOIN`, cancelamento, `mensagem()`, `Event.TLS` e enfileiramento próprio (5.2) | +4 | 6; D-10 da SP2 define quem adapta |
| 8 | Chave de ping e slug | +5 | 3, log da borda conferido |
| 9 | Autoprovisionamento | Nenhuma | 8 |
| 10 | Filtros | +6 | 3 |
| 11 | D6, D10 e D14, um PR por decisão aprovada | +7 | 3, escolhas do dono, disco |
| 12 | OnCalendar e prévia | +8 | 1 publicado antes |

Com a escolha "manter" em D6, D10 ou D14, a parte correspondente some. Os números de migration valem na ordem em que os PRs
entrarem, e o Discord ocupa o que estiver livre quando chegar.

## 13. Verificado e em aberto

**Verificado por leitura de `origin/main`:** migrations até a 00022; `heartbeat.go`, `schedule.go`, `ping.go`,
`pinghandlers.go`, `heartbeathandlers.go`, `probe.go`, `classify.go`, `scheduler.go`, `safedial`, avaliador, dispatcher
(parcial), queries, retenção, limites do compose e telas do detalhe. Em `3456d36`: `Service.List` devolve erro ao primeiro
`toHeartbeat` que falha, conteúdo do #236, políticas de `target_health` e `targets_public_select`, `ratelimit.go` e `Caddyfile`.
Em `d177e72`: `ClaimDueNotifications`, `ClaimDueNotificationsRow`, `semEnvio`, `mensagem()`, `Event`, `EnqueueNotification`,
`DialedHost` e `storage.Migrate`. No código-fonte do Go 1.26.10 instalado: `hostnameInSNI` devolve vazio para IP e tira o ponto
final.

**Em aberto:**
- D6, D10, D14, os estágios de aviso e, na SP2, D-10 e D-11: decisões do dono.
- Disco da VPS e o que está de fato em produção.
- Log da borda do Coolify, que recebe a chave de ping no caminho: não localizado.
- Todos os números da seção 11.
- `TLSHandshakeDone` com HTTP/2 e transporte próprio: esperado que dispare em cada salto; provar em teste.
- `NOT VALID` em CHECK de tabela particionada e tempo da troca em `checks` (portão no PR 5).
- Texto de `content_mismatch` e `content_encoding` no binário anterior (`causa()` com causa desconhecida).
- Diferenças de horário de verão entre OnCalendar e o `robfig/cron`.
- Regra de estágios 14/7/3: proposta minha, diverge do levantamento.
