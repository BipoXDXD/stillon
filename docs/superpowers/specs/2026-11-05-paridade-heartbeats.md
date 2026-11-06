# Heartbeats: lacunas em relação ao healthchecks.io

Data: 2026-11-05. Escopo aprovado pelo dono: "Visual e heartbeat", semelhante ao healthchecks.io em
recursos e densidade, com identidade própria. Fora do escopo: equipes, projetos e integrações novas.
Nada de marca, texto, ícone ou layout vem de lá.

Fonte: as páginas públicas da documentação lidas em 2026-11-05 (`docs/`, `monitoring_cron_jobs`,
`http_api`, `measuring_script_run_time`, `attaching_logs`, `configuring_checks`, `badges`, `api`,
`signaling_failures`). A página da API de gestão foi lida em parte. Ordenação do painel, prévia do
cron e exportação **não constam dessas páginas**: entram aqui como exemplos do pedido, avaliados
pelo que valem para o StillOn, e não como afirmação sobre o outro produto.

## Tabela de lacunas

Situação: **tem** (já entregue), **parcial**, **falta**, **diferente** (escolha deliberada, com
decisão registrada). A ordem dos "fazer" é a do mais útil ao menos útil, com corte em quatro PRs.

| Recurso | Situação no StillOn | Decisão |
|---|---|---|
| Sinais de sucesso, início e falha | tem (`/ping/{token}`, `/start`, `/fail`) | nada a fazer |
| Código de saída no ping (`/ping/{token}/$?`) | falta | **fazer, PR 1.** Uma linha só no crontab cobre sucesso e falha, e o código vai para o log |
| Última duração na lista e no detalhe | parcial: só no log | **fazer, PR 2.** É o que mostra uma tarefa ficando lenta sem abrir o detalhe |
| Duração da execução que falhou | falta: só o sucesso mede | **fazer, PR 2.** Mesmo recurso: a execução que falha é a que mais interessa medir |
| Indicador de execução em andamento | parcial: `last_start_at` só no detalhe | **fazer, PR 2.** Mesmo recurso, com o início consumido também na falha |
| Tags em heartbeats | falta: com filtro de tag, o heartbeat some (T4) | **fazer, PR 3.** Fecha a principal diferença entre as duas metades da tabela |
| Prévia dos próximos disparos do cron | falta | **fazer, PR 4.** Cron e fuso errados são o erro mais caro de cadastro, e só aparecem no primeiro alarme falso |
| Ordenação da tabela | falta: gravidade e nome, fixos | **fazer, depois do corte.** Útil, mas a ordem por gravidade já responde à pergunta do painel; fica para a próxima rodada |
| Corpo do ping guardado com limite | diferente (D14) | **decisão do dono**, abaixo |
| GET e HEAD no ping | diferente (D6) | **decisão do dono**, abaixo |
| Badge de estado | falta | **não fazer.** Superfície pública nova com segredo próprio, e a página de status já publica o estado. Ver abaixo |
| Sinal de log, sem mudar o estado (`/log`) | falta | **não fazer.** Sem corpo (D14), o evento só diria "algo aconteceu". Reavaliar junto de D14 |
| Filtro por palavra-chave no corpo | falta | **não fazer.** Depende de D14, e um `if` na tarefa com `/fail` resolve |
| ID de execução (`rid`) para tarefas concorrentes | falta | **não fazer.** Exige guardar várias execuções abertas por heartbeat. Sem pedido real; a alternativa é um heartbeat por tarefa |
| Atraso medido desde o início (`start` abre o prazo) | diferente (D10) | **não fazer.** A tolerância cobre o tempo de execução, e só o sucesso move o prazo: uma regra só |
| Selo "dentro da tolerância" | falta: `up` até o prazo com tolerância | **não fazer agora.** Selo novo é decisão de design; ver a lista de design abaixo |
| Pausa que ignora pings, ping que retoma | diferente (D13) | **não fazer.** Ping pausado é registrado e não retoma: retomar é um ato do dono |
| Exportação | parcial: `GET /heartbeats` e `/events` já entregam JSON | **não fazer.** Sem equipe nem migração à vista, um botão a mais não paga a superfície |
| Expressões `OnCalendar` do systemd | falta | **não fazer.** Segundo parser e segundo formato para o mesmo problema; 5 campos cobrem o caso (D17) |
| Identificador por slug, criação automática no ping | falta | **não fazer.** A URL com 256 bits é a credencial (D7); um nome previsível enfraquece o modelo e não há ganho sem provisionamento em massa |
| API de gestão com chave própria | falta | **não fazer.** Autenticação nova; a API do painel já existe, com sessão |
| Clonar heartbeat | falta | **não fazer.** Cadastrar custa quatro campos |
| Ping por e-mail | falta | **fora do escopo:** integração nova |
| Integrações por heartbeat | falta: os avisos usam os canais do dono | **fora do escopo:** integração nova |
| Histórico de transições (`flips`) | tem, em outra forma: incidentes e log | nada a fazer |
| Lembretes enquanto está com problema | tem | nada a fazer |
| Densidade da lista, filtros e busca | tem (spec do painel denso) | nada a fazer |
| Retenção do log | tem: 1000 eventos, 30 dias (D15) | nada a fazer |

## Decisões do dono, à parte

**D14, corpo do ping: manter descartado.** Guardar os últimos KiB ajudaria a ler o erro de um backup
sem entrar na máquina, mas um POST público passaria a gravar conteúdo arbitrário, às vezes com
segredo, que a tarefa nem sabe que enviou, e que iria para o backup do banco. Mudar exige refazer o
STRIDE da §6 da spec de heartbeats e testar cada ameaça. Recomendação: manter D14. O custo é o log
só dizer quando e com que código a tarefa terminou. Se o dono quiser mudar, o desenho menos
arriscado é opcional por heartbeat, desligado por padrão, com teto de 10 KiB, retenção igual à do
log e exibição só no detalhe.

**D6, só POST: manter.** Aceitar GET simplifica `wget` e clientes limitados, mas qualquer prévia de
link ou scanner que vir a URL contará como sucesso e esconderá uma tarefa parada, que é a falha que
o produto existe para pegar. Recomendação: manter D6. O custo é exigir `-X POST`; liberar depois é
aditivo.

**Badge.** Recomendação: não fazer. Exigiria uma rota pública nova, um token próprio para não expor
o de ping, um escopo de RLS e um STRIDE. Quem quer publicar estado já tem a página de status, que
hoje só mostra alvos. Se houver pedido, o caminho barato é levar heartbeats à página de status.

## Pontos de design, à parte

Nenhum deles segurou os PRs; cada um segue a recomendação até o dono dizer o contrário.

- **Onde a última duração aparece.** Foi para a coluna Situação ("Sinal há 3 min · levou 12 s"), e a
  coluna Latência segue com `—` (T5). Alternativa: renomear a coluna para "Medida" e pôr a duração
  nela. Recomendação: manter, porque latência e tempo de execução são grandezas diferentes numa
  coluna só; o custo é a duração sumir no celular, com a coluna Situação.
- **Selo "dentro da tolerância".** Hoje a tarefa fica "Em dia" até vencer a tolerância. Um selo
  intermediário anteciparia o problema, mas põe um sexto estado na escala de gravidade. Recomendação:
  não ter, porque a escala única é o que faz o problema saltar aos olhos.
- **Ordenação.** Quando entrar, clique no cabeçalho ou seletor acima da tabela? Recomendação:
  seletor, com a ordem na URL, porque a tabela esconde colunas por largura.

## Código de saída (PR 1)

`POST /ping/{token}/{exit_code}`, com 0..255: 0 é sucesso, o resto é falha, e o log guarda o número
em `heartbeat_events.exit_code`. D6 e D14 não mudam: continua só POST, e o corpo continua
descartado. Código fora da faixa, ou que não seja a forma canônica de um número, responde como token
desconhecido, sem ir ao banco.

STRIDE da rota nova, no molde da §6 da spec de heartbeats. O que não está aqui herda a mitigação e o
teste de lá, porque a rota passa pela mesma cadeia e pelo mesmo `InPingTx`:

| Ameaça | Mitigação | Teste |
|---|---|---|
| **T** Número que contradiz o sinal gravado | CHECK casa código e tipo | `TestHeartbeatEventExitCodeMatchesTheKind` |
| **I** Sondar a rota para saber se a URL existe | código inválido e token desconhecido: mesmo status e corpo | `TestPingInvalidExitCodeAnswersLikeUnknownToken`, `TestPingExitCodeWithUnknownTokenAnswersTheSame` |
| **I** Token em log pela rota nova | mesmo caminho de código, sem o caminho no log | `TestPingNeverLogsTheToken` (com a rota nova) |
| **D** Balde novo para cada número | a chave do balde é o hash do token | `TestPingExitCodeSharesTheTokenLimit` |
| **D** Retry duplicando o log | a repetição do D9 vale pelo sinal | `TestPingRepeatedExitCodeHasOneEffect` |
| **E** Rota pública além do ping | allowlist explícita | `TestEveryRegisteredRouteDeniesByDefault` (entrada nova em `rotasPublicas`), `TestPingChainServesOnlyPing` (405 também com código) |

## Última duração (PR 2)

`last_runtime_ms` fica na linha de `heartbeats` (00020), ao lado de `last_ping_at`. Derivar do log
exigiria varrer até 1000 eventos por heartbeat a cada 15 s em quem nunca manda `/start`, que é o caso
comum. A falha agora encerra a execução como o sucesso: mede e consome o início. Sem isso, a tarefa
que falhou ficaria "em execução" até o próximo sinal. Sucesso ou falha sem início deixam a duração
`NULL`: repetir a anterior falaria de uma execução que ninguém mediu.

`last_start_at` diferente de `NULL` passa a significar "em execução". O detalhe trocou "Último
início" por "Em execução desde", que só aparece nesse caso, porque o início consumido virava
"Nunca". Sem superfície nova: nenhuma rota, e o dado sai só para o dono.

## Tags em heartbeats (PR 3)

Mesmas regras e mesmo contrato das tags de alvo: até 8, 1 a 24 caracteres, allowlist, e no `PATCH`
ausente não mexe, `[]` e `null` limpam. A regra saiu de `internal/targets` para `internal/rotulo`, e
as duas validações são a mesma função. Substitui T4: heartbeat aparece com filtro de tag, e o filtro
continua no cliente, porque a rota não filtra e as 100 linhas já estão na tela. O filtro de tag vale
para os dois tipos, e a contagem de cada tag soma alvos e heartbeats.

| Ameaça | Mitigação | Teste |
|---|---|---|
| **I** Tag de A lida por B ou pelo escopo do ping | a política de `heartbeat_tags` nomeia o dono, e as queries filtram por `user_id` | `TestHeartbeatTagsAreSeenOnlyByTheOwner`, `TestAnotherUsersHeartbeatTagsStayOut` |
| **T** B gravar tag no heartbeat de A | `WITH CHECK` e filtro por dono; o `PATCH` alheio é 404 | `TestAnotherUserCannotInsertHeartbeatTags`, `TestAnotherUsersHeartbeatTagsStayOut` |
| **T** Lixo gravado por fora da validação | CHECK espelha a validação | `TestHeartbeatTagCheckRefusesWhatTheCodeWouldNot` |
| **D** Tags sem teto | até 8 por heartbeat, recusado antes do banco | `TestHeartbeatTagsAreValidatedLikeTargetTags` |

## Prévia do cron (PR 4)

`GET /api/v1/heartbeats/cron-preview?expr=&timezone=` devolve os 5 próximos disparos, com a validação
do cadastro. Sem migration e sem banco. Quem julga continua sendo o servidor (T11): um parser no
cliente é o risco que o D17 evitou, e a prévia não trava o envio. O formulário consulta depois de
400 ms sem digitar e mostra as datas no fuso declarado, que é o da máquina da tarefa.

| Ameaça | Mitigação | Teste |
|---|---|---|
| **S** Rota sem sessão | `protegida`, como as demais | `TestCronPreviewWithoutSessionIs401`, `TestEveryRegisteredRouteDeniesByDefault` |
| **T** Entrada ruim virar 500 | mesma validação de `NewCron`, com tamanho conferido antes do parser | `TestCronPreviewRefusesWhatTheFormWouldRefuse` |
| **I** Chave da consulta ecoada | campo fixo `query` | `TestCronPreviewQueryIsStrict` |
| **I** Fuso como caminho de arquivo | `time.LoadLocation` recusa `..`; caso de teste com caminho | `TestCronPreviewRefusesWhatTheFormWouldRefuse` |
| **D** Muitas consultas | limite por usuário da cadeia `protegida`, o mesmo das outras rotas, e 400 ms de espera no painel | `espera a digitação parar: não é uma consulta por tecla` |

## Migrations

Cada PR que precisa de migration leva o próximo número livre, e por isso os PRs são empilhados e
têm de entrar na ordem: `storage.Migrate` recusa número menor que o aplicado. A 00019 também remove
`heartbeats_due_idx`, a pendência registrada na §4 da spec de heartbeats.
