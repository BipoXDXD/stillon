# Healthchecks.io: inventário de funcionalidades (novembro de 2026)

Pesquisa feita em 2026-11-22, só em fontes primárias: site e documentação oficiais
(`healthchecks.io`) e o repositório oficial `github.com/healthchecks/healthchecks`
(README, CHANGELOG e código, branch `master`). Nenhuma resenha de terceiros.

Abreviações das fontes usadas nas tabelas:

| Sigla | URL |
|---|---|
| HOME | https://healthchecks.io/ |
| PRICING | https://healthchecks.io/pricing/ |
| FAQ | https://healthchecks.io/faq/ |
| CHK | https://healthchecks.io/docs/configuring_checks/ |
| PING | https://healthchecks.io/docs/http_api/ |
| API | https://healthchecks.io/docs/api/ (Management API v3) |
| SLUG | https://healthchecks.io/docs/slug_urls/ |
| AUTO | https://healthchecks.io/docs/autoprovisioning/ |
| RUN | https://healthchecks.io/docs/measuring_script_run_time/ |
| FAIL | https://healthchecks.io/docs/signaling_failures/ |
| LOGS | https://healthchecks.io/docs/attaching_logs/ |
| EMAIL | https://healthchecks.io/docs/email/ |
| NOTIF | https://healthchecks.io/docs/configuring_notifications/ |
| TEAMS | https://healthchecks.io/docs/projects_teams/ |
| BADGES | https://healthchecks.io/docs/badges/ |
| PROM | https://healthchecks.io/docs/configuring_prometheus/ |
| CLONE | https://healthchecks.io/docs/cloning_checks/ |
| VS-CRONITOR | https://healthchecks.io/docs/healthchecks_cronitor_comparison/ (atualizada em 2026-09-22) |
| SELF | https://healthchecks.io/docs/self_hosted/ |
| SELFCFG | https://healthchecks.io/docs/self_hosted_configuration/ |
| README | https://github.com/healthchecks/healthchecks/blob/master/README.md |
| CL | https://github.com/healthchecks/healthchecks/blob/master/CHANGELOG.md (última versão: v5.0.3, 2026-10-27; v5.1-dev em aberto) |
| SRC:`arquivo` | arquivo no repositório, ex.: `hc/api/models.py` em https://github.com/healthchecks/healthchecks/tree/master |

Modelo do produto: monitoramento passivo por "dead man's switch"/heartbeat: o sistema
monitorado precisa fazer check-in; a falta do check-in gera o alerta (FAQ).

## 1. Tipos de check e agendamento

| Item | O que oferece | Fonte |
|---|---|---|
| Tipos de agenda | Três: Simple, Cron e OnCalendar | CHK; SRC:`hc/api/models.py` (`CHECK_KINDS`) |
| Simple | Period (intervalo esperado) + Grace Time (tolerância antes do alerta) | CHK |
| Limites de period/grace (API) | 60 s a 31.536.000 s (365 dias); padrão period 86400 s, grace 3600 s | API (create check: `timeout`, `grace`) |
| Cron | Expressão cron + fuso do servidor + grace; garante horário certo, não só intervalo; parser `cronsim` | CHK; README |
| OnCalendar (systemd) | Uma ou mais expressões (uma por linha); espera ping quando qualquer uma casar | CHK |
| Detecção automática | Campo `schedule` da API detecta cron ou OnCalendar; `schedule` tem precedência sobre `timeout` | API |
| Fuso horário | Campo `tz` (padrão "UTC"), só vale com `schedule` | API; FAQ ("alerts a few hours before/after") |
| Grace com /start | Depois de um "start", espera o "success" dentro do grace; senão marca down | CHK; RUN |
| Estados (API) | `new`, `up`, `grace`, `down`, `paused`; `started` é um booleano à parte | API (List checks: "possible values for the status field") |
| Estados (UI) | New, Up, Late (= grace), Down; "Started" quando há start sem conclusão | HOME; RUN |
| Pausa | Pausar/retomar pela UI e API; opção `manual_resume` ("sticky"): ping não tira da pausa | CHK; API |
| Nome, slug, tags, descrição | Descrição até 10.000 caracteres | CHK; API; CL v5.0 |
| Clonar check / projeto | "Create a Copy..." copia nome, tags, descrição, agenda, filtros e integrações (UUID novo, log vazio) | CLONE |
| Transferir check entre projetos | Mantém a URL de ping | TEAMS |

## 2. Sinais de ping

| Item | O que oferece | Fonte |
|---|---|---|
| Protocolos | HTTP e HTTPS; HTTP/1.0, 1.1 e 2; IPv4 e IPv6; HEAD, GET, POST | PING |
| Sucesso | `https://hc-ping.com/<uuid>` | PING |
| Início | `/start` (mede duração; detecta execução além do grace) | PING; RUN |
| Falha | `/fail` | PING; FAIL |
| Log | `/log` registra evento sem mudar o estado | PING; LOGS |
| Código de saída | `/<exit-status>` (0–255; 0 = sucesso, resto = falha) | PING; FAIL |
| Run ID | `?rid=<uuid>` casa start e conclusão em execuções concorrentes; só a execução iniciada mais recentemente é vigiada quanto ao tempo | PING; RUN |
| Duração | Calculada entre start e success/fail se distantes menos de 72 h | RUN; SRC:`hc/api/models.py` (`MAX_DURATION`) |
| Corpo do ping | POST guarda o corpo se parecer UTF-8; até 100 kB no serviço hospedado; cabeçalho de resposta `Ping-Body-Limit` | PING; LOGS |
| Corpo no self-hosted | `PING_BODY_LIMIT`, padrão 10.000 bytes (ou sem limite) | SELFCFG |
| Retenção do log de pings | Por quantidade, não por tempo: 100 entradas por check (free) e 1000 (pagos); as mais antigas saem | PRICING |
| Retenção de flips | Mês atual + dois meses completos anteriores | API (List check's status changes) |
| Download do corpo | "Download Original" na UI; API `GET .../pings/<n>/body` | LOGS; API |
| Ping por UUID | UUID imutável, é o próprio segredo | PING; SLUG |
| Ping por slug | `https://hc-ping.com/<ping-key>/<slug>[/start|/fail|/log|/<code>]`; slug não é único: slug ambíguo responde 409 | PING; SLUG |
| Auto-provisionamento | `?create=1` em URLs de slug cria o check (HTTP 201); usa padrões (1 dia, grace 1 h, todas as integrações); não aceita parâmetros pela URL; pode passar até 2x o limite de checks | AUTO; CL v4.3 |
| Filtro de método | "Only POST" (`methods: "POST"`) contra bots de preview | CHK; API |
| Filtro por palavras-chave | Start/success/failure keywords (sensível a maiúsculas; ordem falha → sucesso → início) no corpo HTTP (primeiros 100 kB), assunto ou corpo de e-mail; sem casamento: ignorar ou tratar como falha | CHK; EMAIL; API (`start_kw`, `success_kw`, `failure_kw`, `filter_*`) |
| E-mail como ping | Endereço de e-mail próprio por check; aceita HTML; aviso de atraso de entrega | EMAIL |
| Rate limit de ping | Mais de 5 pings/min por check podem ser descartados; abuso extremo bloqueia o IP | PING |
| IPs publicados | `healthchecks.io/ipv4.txt` e `ipv6.txt` (entrada); IPs de saída (webhooks) não são publicados | FAQ |
| Domínios de ping | `hc-ping.com` (preferido) e `hchk.io` (legado) | FAQ |

## 3. Notificações e integrações

Integrações no código (`TRANSPORTS`, SRC:`hc/api/models.py`), 28 tipos:

| Categoria | Canais | Fonte |
|---|---|---|
| E-mail e webhook | Email, Webhook (URLs, método GET/POST/PUT, corpo e cabeçalhos separados para down e up) | HOME; SRC:`hc/integrations/webhook/forms.py` |
| Chat | Slack, Discord, Telegram, Microsoft Teams (Workflows), Google Chat, Mattermost, Rocket.Chat, Matrix, Zulip, Signal, WhatsApp | HOME; SRC:`hc/api/models.py` |
| Push | ntfy, Pushover, Pushbullet, Gotify | HOME |
| Incidentes | PagerDuty, Opsgenie, PagerTree, Splunk On-Call (VictorOps), Spike.sh | HOME |
| Telefonia | SMS, WhatsApp e Phone Call (só planos pagos, via Twilio no self-hosted) | VS-CRONITOR; SELFCFG (`TWILIO_*`) |
| Tarefas | GitHub Issues, Trello (só abrem item no "down") | HOME; SRC:`hc/integrations/github/transport.py`, `trello/transport.py` |
| Outros | Prometheus (endpoint de métricas), Group (agrupa integrações), Apprise e Shell Command (desligados por padrão; para self-hosted) | SRC:`hc/api/models.py`; README (Apprise, Shell Commands) |

| Item | O que oferece | Fonte |
|---|---|---|
| Aviso de "down" e "up" | Padrão é avisar nos dois; e-mail, Signal, SMS e WhatsApp têm caixas `down`/`up`; webhook e Shell têm URL/comando distintos; Pushover, ntfy e Gotify têm prioridade por estado (incluindo "desligado"); Phone Call, GitHub e Trello só no down | SRC:`hc/integrations/*/forms.py`, `*/transport.py` (`is_noop`) |
| Alerta único | Alertas são "one-shot" | FAQ ("keep getting notified") |
| Repetição | Lembretes por e-mail a cada hora ou dia enquanto houver check down (Account Settings › Email Reports); ou escalonamento no PagerDuty/Opsgenie/PagerTree/Splunk; ou Pushover "Emergency" (repete a cada 5 min) | NOTIF; SRC:`hc/accounts/models.py` (`NAG_PERIODS`) |
| Duração da queda no alerta | Templates informam a duração do downtime | CL v4.3 |
| Roteamento | Integração pertence a um projeto; cada check liga/desliga cada integração (ícones na lista ou ON/OFF no detalhe); API aceita `channels` por UUID, por nome ou `"*"` | NOTIF; API |
| Tags e roteamento | Não há roteamento por tag documentado; tags servem para organizar, filtrar e gerar badges | CHK; BADGES |
| Cota de SMS/WhatsApp/ligação | Mensal, zera no início do mês, sem acúmulo; aviso por e-mail e na página de integrações ao estourar (desde v4.3 para todo o time) | NOTIF; CL v4.3 |
| Aviso de flapping | E-mail a todos do time quando um check passa de 200 flips em 24 h | CL v4.4; SRC:`hc/accounts/management/commands/sendflappingnotices.py` |
| Mute | Por check (pausa ou desligar integrações) | VS-CRONITOR |

## 4. Organização

| Item | O que oferece | Fonte |
|---|---|---|
| Projetos | Vários por conta; checks e integrações pertencem a um projeto; limite de checks é da conta (somado entre projetos) | TEAMS |
| Equipes | Time por projeto; convite por e-mail; sem limite de membros em nenhum plano desde v4.1 | TEAMS; CL v4.1 |
| Papéis | Owner, Manager (convida/remove), Team Member (edita checks/integrações, vê chaves), Read-only (só vê; sem chaves de API) | TEAMS |
| Transferência | Check entre projetos (mantém URL); projeto entre contas (com confirmação do novo dono) | TEAMS |
| Chaves de API | Por projeto (não há chave da conta); read-write e read-only; read-only omite `uuid`, `ping_url`, `channels` etc. | API |
| Ping Key | Chave por projeto para URLs de slug | SLUG; AUTO |
| Badges | Por projeto (todos os checks), por tag ou por check; SVG, JSON e Shields.io; 2 estados (up/down) ou 3 (up/late/down); URL pública difícil de adivinhar | BADGES; API (List project's badges) |
| Tags | Lista separada por espaço; filtro por tag na UI e na API (`?tag=` repetível) | CHK; API |
| Busca e filtros na UI | Busca por nome, slug ou código; filtro por tags e por status; ordenação por nome, último ping ou criação | SRC:`hc/front/views.py` (`search`, `tag`, `status`, `VALID_SORT_VALUES`) |
| Outras telas | Painel "TV" (`/tv/`), log do projeto, downtimes por check | SRC:`hc/front/urls.py` |

## 5. API de gestão, Prometheus e exportação

Management API v3 (versões v1 e v2 ainda documentadas), autenticação `X-Api-Key` ou campo
`api_key` no JSON, limite de 100 requisições/min (HTTP 429). Fonte: API.

| Endpoint | Função |
|---|---|
| `GET /api/v3/checks/` | Lista checks (filtros `?slug=`, `?tag=`) |
| `GET /api/v3/checks/<uuid>` ou `<unique_key>` | Um check |
| `POST /api/v3/checks/` | Cria; `unique` faz upsert (201 ou 200) |
| `POST /api/v3/checks/<uuid>` | Atualiza |
| `POST /api/v3/checks/<uuid>/pause` e `/resume` | Pausa e retoma |
| `DELETE /api/v3/checks/<uuid>` | Remove |
| `GET /api/v3/checks/<uuid>/pings/` | Pings registrados |
| `GET /api/v3/checks/<uuid>/pings/<n>/body` | Corpo de um ping |
| `GET /api/v3/checks/<uuid>/flips/` | Mudanças de estado (filtros `seconds`, `start`, `end`) |
| `GET /api/v3/channels/` | Lista integrações (só leitura; não há criação de integração pela API) |
| `GET /api/v3/badges/` | Badges por tag e `*` |
| `GET /api/v3/status/` | Saúde do serviço (banco) |

| Item | O que oferece | Fonte |
|---|---|---|
| Prometheus | `/projects/<uuid>/metrics/<chave-ro>` ou com Bearer (Grafana Cloud); métricas `hc_check_up`, `hc_check_started`, `hc_check_grace`, `hc_check_paused`, `hc_tag_up`, `hc_checks_total`, `hc_checks_down_total` | PROM |
| Exportação | Não há exportação em lote (CSV/JSON) documentada; os dados saem pela API, pelo download do corpo do ping e pelas métricas | API; LOGS; PROM |

## 6. Segurança e conta

| Item | O que oferece | Fonte |
|---|---|---|
| Login | Link mágico por e-mail ou e-mail + senha | FAQ (Authentication) |
| 2FA | WebAuthn (várias chaves) e TOTP; proteção contra replay de TOTP reforçada na v5.1-dev | FAQ; README; CL v5.1-dev |
| SSO | Sem SAML SSO no serviço hospedado; no self-hosted, autenticação externa via cabeçalho de proxy (`REMOTE_USER_HEADER`) | VS-CRONITOR; README (External Authentication) |
| Hash de senha | Argon2 desde v4.3 | CL v4.3 |
| Log de auditoria | Não documentado (nada no FAQ, docs ou README) | FAQ; README |
| Dados | Servidores Hetzner na Alemanha; empresa na Letônia (GDPR); criptografia em trânsito (HTTPS, WireGuard interno); sem criptografia em repouso no banco; backups cifrados com gpg | FAQ (Security) |
| Compliance | Sem ISO 27001/SOC 2, sem pentest formal, sem bug bounty, não assina DPA com clientes nem SLA | FAQ (Security) |
| Operação | Uma pessoa; quedas de horas ou dias são possíveis; status próprio em status.healthchecks.io | FAQ ("How reliable") |
| Contas inativas | Removidas após inatividade, com aviso por e-mail | FAQ |

## 7. Status pages e relatórios

| Item | O que oferece | Fonte |
|---|---|---|
| Status page pública | Não oferece; a comparação oficial marca "Public status pages: –" | VS-CRONITOR |
| Alternativa sugerida | Badges (SVG/JSON) embutidos em README, painéis ou status page de terceiros que vigie o JSON | BADGES; HOME |
| Relatórios por e-mail | Mensal (dia 1), semanal (segunda) ou diário, ou desligado; todos os projetos acessíveis; status atual, nº de quedas e duração total por mês nos últimos dois meses; resumo de uma linha no topo | NOTIF; TEAMS; CL v4.1; SRC:`hc/accounts/models.py` (`REPORT_CHOICES`) |
| Lembretes | Hora em hora ou diário enquanto houver check down | NOTIF |

## 8. Self-hosting, planos e preços

| Item | O que oferece | Fonte |
|---|---|---|
| Licença | BSD 3-clause | README; SELF |
| Requisitos | Python 3.12+, Django 6.1, PostgreSQL, MySQL ou MariaDB (a página SELF ainda diz Django 6.0 e não cita MariaDB); desde v4.4 exige PostgreSQL ≥ 15, MySQL ≥ 8.4, MariaDB ≥ 10.11; SQLite no desenvolvimento | README; SELF; CL v4.4 |
| Docker | Imagens oficiais; desde v5.0 gunicorn em vez de uWSGI | README; CL v5.0 |
| Peças | Processos `sendalerts`, `sendreports`, `smtpd` (ping por e-mail); armazenamento S3 opcional para corpos de ping | CL v5.0; SELFCFG (`S3_*`) |
| Configuração | Cada integração liga por variável (`*_ENABLED`, chaves de Slack, Telegram, Twilio etc.); `REGISTRATION_OPEN`; `USE_PAYMENTS` (cobrança desligada por padrão); `RP_ID` para WebAuthn | SELFCFG; README |

Planos do serviço hospedado (PRICING):

| Plano | Preço | Checks | Log por check | SMS + WhatsApp/mês | Ligações/mês | Suporte |
|---|---|---|---|---|---|---|
| Hobbyist | US$ 0 | 20 | 100 | 0 | 0 | — |
| Supporter | US$ 5/mês | 20 | 100 | 0 | 0 | — |
| Business | US$ 20/mês (US$ 192/ano) | 100 | 1000 | 50 | 20 | E-mail |
| Business Plus | US$ 80/mês (US$ 768/ano) | 1000 | 1000 | 500 | 100 | E-mail prioritário |

- Membros de time: sem limite em todos os planos (CL v4.1).
- Plano gratuito sem SMS/ligação: padrão `sms_limit = 0` e `call_limit = 0` (SRC:`hc/accounts/models.py`); a comparação marca SMS, WhatsApp e ligação como "paid plans only" (VS-CRONITOR).
- Business grátis para open source e ONGs; anual com 20% de desconto; reembolso em 14 dias; inadimplência > 60 dias volta ao Hobbyist (PRICING; FAQ).
- Várias contas para fugir do limite gratuito é considerado abuso (PRICING).

## 9. O que o Healthchecks.io não faz (segundo as fontes)

| Item | Situação | Fonte |
|---|---|---|
| Monitoramento ativo de site/uptime HTTP | Não faz ("Website uptime monitoring: –") | VS-CRONITOR |
| Status pages públicas hospedadas | Não faz | VS-CRONITOR |
| SAML SSO | Não faz no hospedado | VS-CRONITOR |
| CLI e SDKs próprios, importação de crontab | Não tem (recomenda o Runitor, de terceiros) | VS-CRONITOR; LOGS |
| Métricas além do exit code | Não (só exit code e duração start→success) | VS-CRONITOR; RUN |
| Alertas repetidos por canal | Não; só lembretes por e-mail ou escalonamento em ferramenta externa | FAQ; NOTIF |
| Parâmetros no auto-provisionamento | Não aceita period/grace pela URL | AUTO |
| Logs grandes | Corta em 100 kB; recomenda serviço de logs dedicado | LOGS |
| Verificação de certificado TLS, ping de porta/TCP/ICMP, DNS, keyword em página | Não citado em nenhuma página; o modelo é só passivo (check-in do cliente). Conclusão por ausência, não afirmação explícita | FAQ ("What is Healthchecks.io?"); HOME |
| Roteamento de alertas por tag ou severidade | Não documentado; roteamento é por check × integração | NOTIF |
| Log de auditoria, SLA, certificações, DPA com cliente | Não oferece | FAQ (Security) |
