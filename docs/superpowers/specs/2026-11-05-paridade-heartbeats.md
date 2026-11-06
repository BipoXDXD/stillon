# Heartbeats: lacunas em relação ao healthchecks.io

Data: 2026-11-05. Escopo aprovado pelo dono: "Visual e heartbeat", semelhante ao healthchecks.io em
recursos e densidade, com identidade própria. Fora do escopo: equipes, projetos e integrações novas.
Nada de marca, texto, ícone ou layout vem de lá.

Fonte: as páginas públicas da documentação lidas em 2026-11-05 (`docs/`, `monitoring_cron_jobs`,
`http_api`, `measuring_script_run_time`, `attaching_logs`, `configuring_checks`, `badges`, `api`,
`signaling_failures`). Em 2026-11-06 a leitura foi refeita em 19 páginas, com a API de gestão
inteira: os fatos e as URLs estão em "Fatos da documentação", abaixo. Ordenação do painel, prévia do
cron no cadastro e exportação **continuam sem constar**: seguem como exemplos do pedido, avaliados
pelo que valem para o StillOn, e não como afirmação sobre o outro produto. O que a documentação não
diz não vira fato: as páginas mostram a interface em imagens, e o texto cobre só parte dela.

## Tabela de lacunas

Situação: **tem** (já entregue), **parcial**, **falta**, **diferente** (escolha deliberada, com
decisão registrada). A ordem dos "fazer" é a do mais útil ao menos útil, com corte em quatro PRs.

| Recurso | Situação no StillOn | Decisão |
|---|---|---|
| Sinais de sucesso, início e falha | tem (`/ping/{token}`, `/start`, `/fail`) | nada a fazer |
| Código de saída no ping (`/ping/{token}/$?`) | tem | **feito, PR 1.** Uma linha só no crontab cobre sucesso e falha, e o código vai para o log |
| Última duração na lista e no detalhe | tem | **feito, PR 2.** É o que mostra uma tarefa ficando lenta sem abrir o detalhe |
| Duração da execução que falhou | tem | **feito, PR 2.** Mesmo recurso: a execução que falha é a que mais interessa medir |
| Indicador de execução em andamento | tem | **feito, PR 2.** Mesmo recurso, com o início consumido também na falha |
| Tags em heartbeats | tem | **feito, PR 3.** Fecha a principal diferença entre as duas metades da tabela |
| Prévia dos próximos disparos do cron | tem | **feito, PR 4.** Cron e fuso errados são o erro mais caro de cadastro, e só aparecem no primeiro alarme falso |
| Descrição da tarefa | tem, depois do PR 5 | **feito, PR 5.** Não estava nesta tabela. Sem corpo no log (D14), é o único lugar para dizer onde a tarefa roda e o que fazer quando falha. Ver "Descrição" abaixo |
| Ordenação da tabela | tem, depois do corte: gravidade, nome, menor tempo no ar e maior latência | **feito.** Ver "Ordenação" abaixo |
| Corpo do ping guardado com limite | diferente (D14) | **decisão do dono**, abaixo |
| GET e HEAD no ping | diferente (D6) | **decisão do dono**, abaixo |
| Badge de estado | falta | **não fazer.** Superfície pública nova com segredo próprio, e a página de status já publica o estado dos alvos. Ver abaixo |
| Sinal de log, sem mudar o estado (`/log`) | falta | **não fazer.** Sem corpo (D14), o evento só diria "algo aconteceu". Reavaliar junto de D14 |
| Filtro por palavra-chave no corpo | falta | **não fazer.** Depende de D14, e um `if` na tarefa com `/fail` resolve |
| ID de execução (`rid`) para tarefas concorrentes | falta | **não fazer.** Exige guardar várias execuções abertas por heartbeat. Sem pedido real; a alternativa é um heartbeat por tarefa |
| Atraso medido desde o início (`start` abre o prazo) | diferente (D10) | **não fazer.** A tolerância cobre o tempo de execução, e só o sucesso move o prazo: uma regra só |
| Selo "dentro da tolerância" | falta: `up` até o prazo com tolerância | **não fazer agora.** Selo novo é decisão de design; ver a lista de design abaixo |
| Pausa que ignora pings, ping que retoma | diferente (D13) | **não fazer.** Ping pausado é registrado e não retoma: retomar é um ato do dono. Lá o padrão é o ping retomar, e o nosso comportamento é a opção `manual_resume` |
| Exportação | parcial: `GET /heartbeats` e `/events` já entregam JSON | **não fazer.** Confirmado: a documentação não cita botão nem arquivo, só a API em JSON, como aqui. Sem equipe nem migração à vista, um botão a mais não paga a superfície |
| Expressões `OnCalendar` do systemd | falta | **não fazer.** Segundo parser e segundo formato para o mesmo problema; 5 campos cobrem o caso (D17) |
| Identificador por slug, criação automática no ping | falta | **não fazer.** A URL com 256 bits é a credencial (D7); um nome previsível enfraquece o modelo e não há ganho sem provisionamento em massa. Lá o slug depende de uma chave de ping do projeto, que aqui não existe |
| Domingo como 7 no cron | diferente: `0 3 * * 7`, `5-7` e `FRI-SUN` são recusados; `0`, `SUN`, `MON-FRI` e `5,6,0` passam (medido no `NewCron`) | **feito na ajuda e na mensagem**, sem reescrever a expressão. Ver a lista de design abaixo |
| Limite de 72 h entre início e fim para mostrar a duração | diferente: mede qualquer intervalo | **não fazer.** Toda execução com `/start` sobrescreve o início anterior, e cortar esconderia a tarefa longa, que é a que mais interessa medir |
| Período e tolerância até 365 dias | diferente: período até 31 dias, tolerância até 7 dias | **não fazer.** O que roda mais raro que um mês cabe no cron, que não tem esse teto |
| IP, user-agent e método por evento | falta | **não fazer.** D14 já recusou IP nos eventos: dado desnecessário de um POST público |
| Relatório periódico por e-mail | falta | **não fazer.** Os incidentes por heartbeat já dão o histórico, e um e-mail recorrente é um envio novo a operar, com descadastro. Sem pedido real |
| Métricas Prometheus por heartbeat | falta | **não fazer.** Exigiria uma chave somente leitura, a mesma autenticação nova recusada na API de gestão |
| API de gestão com chave própria | falta | **não fazer.** Autenticação nova; a API do painel já existe, com sessão |
| Clonar heartbeat | falta | **não fazer agora.** Com a descrição, são cinco campos, ainda poucos para um teto de 100 heartbeats. Se houver pedido, o desenho é o de lá: copia tudo menos a URL e o log. Ver a lista de design abaixo |
| Ping por e-mail | falta | **fora do escopo:** integração nova |
| Integrações por heartbeat | falta: os avisos usam os canais do dono | **fora do escopo:** integração nova |
| Histórico de transições (`flips`) | tem, em outra forma: incidentes e log | nada a fazer |
| Lembretes enquanto está com problema | tem | nada a fazer |
| Densidade da lista, filtros e busca | tem (spec do painel denso) | nada a fazer |
| Retenção do log | tem: 1000 eventos, 30 dias (D15) | nada a fazer |

## Fatos da documentação

Lidos em 2026-11-06, sempre em `https://healthchecks.io`. Paráfrases, e nada daqui é decisão: as
decisões estão na tabela.

| Assunto | Fato | Fonte |
|---|---|---|
| Ordenação | Nenhuma página a cita, e a listagem da API só filtra por `slug` e `tag` | `/docs/api/`, `/faq/` |
| Prévia do cron | O cadastro não cita prévia. Há um testador de expressões na página de referência, fora do check. A API devolve `next_ping` | `/docs/cron/`, `/docs/configuring_checks/`, `/docs/api/` |
| Sintaxe do cron | Cinco campos; dia da semana de 0 a 7, com 0 e 7 como domingo; nomes `JAN-DEC` e `MON-SUN`. A página não diz como dia do mês e dia da semana se combinam | `/docs/cron/` |
| Exportação | Nenhuma página a cita. A API lê checks, pings e flips em JSON, e o evento tem download do corpo | `/docs/api/`, `/docs/attaching_logs/` |
| Clonagem | Botão no detalhe. Copia nome, tags, descrição, agendamento, filtros e integrações; a cópia tem outra URL e log vazio | `/docs/cloning_checks/` |
| Descrição | Texto livre, até 10.000 caracteres pela API. A página sugere registrar o papel da tarefa e o que fazer na falha. Não diz onde o texto aparece, nem se vai nos avisos, nem se tem formatação | `/docs/configuring_checks/`, `/docs/api/` |
| API de gestão | Chave por projeto, de escrita ou só leitura. CRUD de checks, pausa, retomada, pings, corpo do ping, flips, integrações, badges e status | `/docs/api/` |
| Duração na API | Sai por ping (`duration`). O objeto do check não tem última duração | `/docs/api/` |
| Faixas | `timeout` e `grace` de 60 s a 365 dias, com padrões de 1 dia e 1 hora | `/docs/api/` |
| Semântica do atraso | No período, fica atrasado depois do prazo e cai depois da tolerância. No cron, o atraso começa no horário previsto | `/docs/configuring_checks/` |
| Estados | `new`, `up`, `grace` (na tela, "Late"), `down` e `paused`. Em execução não é estado: é o booleano `started` e uma animação sob o ícone | `/docs/`, `/docs/api/` |
| Medição | Do início ao sucesso ou à falha seguinte, só se estiverem a menos de 72 h. Início sem sucesso dentro da tolerância derruba o check | `/docs/measuring_script_run_time/` |
| `rid` | Pareia início e fim em execuções concorrentes, mas só a mais recente tem o tempo vigiado | `/docs/measuring_script_run_time/` |
| Método HTTP | Por padrão aceita HEAD, GET e POST. A opção "só POST" existe para evitar bots de prévia de link, o motivo do D6 | `/docs/configuring_checks/` |
| Pausa | Por padrão, o ping retoma. `manual_resume` mantém a pausa | `/docs/configuring_checks/` |
| Filtro de corpo | Palavras-chave nos primeiros 100 kB; sem casar, o ping é ignorado, ou vira falha se configurado | `/docs/configuring_checks/` |
| Log por evento | A API devolve tipo, data, esquema, IP, método, user-agent, `rid` e duração. Contas gratuitas leem 100 pings, e pagas, 1000 | `/docs/api/` |
| Taxa | Mais de 5 pings por minuto no mesmo check podem ser descartados | `/docs/http_api/` |
| Flips | Mudanças de estado, com filtro de tempo. Retenção do mês atual e dos dois anteriores | `/docs/api/` |
| Relatórios | Por e-mail, com quedas e tempo fora por mês. As páginas divergem na frequência: uma cita mensal e semanal, outra também diária | `/docs/configuring_notifications/`, `/docs/projects_teams/` |
| Prometheus | Rota por projeto com chave só leitura: estado por check e por tag e totais. Sem métrica de duração | `/docs/configuring_prometheus/` |
| Slug | Rota com chave de ping do projeto. Slug duplicado responde 409. `create=1` cria o check no ping | `/docs/slug_urls/`, `/docs/autoprovisioning/` |
| Badge | Por projeto, tag ou check, em SVG, JSON e Shields, com dois ou três estados | `/docs/badges/`, `/docs/api/` |

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

- **Onde a descrição aparece.** Foi para o cabeçalho do detalhe, abaixo das ações, em texto normal,
  com as quebras de linha. Alternativa: um cartão próprio. Recomendação: manter, porque no incidente
  ela é a primeira coisa a ler; o custo é empurrar a página para baixo quando o texto é longo.
- **Descrição na lista.** Ela não aparece na tabela, só no detalhe, mas a API a devolve nas 100
  linhas. Alternativa: tirá-la da lista, com um tipo de resposta a mais. Recomendação: manter, porque
  o teto é 1000 caracteres e o caso comum é uma frase ou nenhuma; o custo é um corpo maior no poll de
  15 s de quem preencher tudo.
- **Descrição nos avisos.** Fica de fora: aviso sai para terceiros, e o texto pode citar máquina e
  caminho. Alternativa: mandar junto, para ler o que fazer sem abrir o painel. Recomendação: manter
  de fora; incluir depois é aditivo, e o contrário, depois de um vazamento, não adianta.
- **Domingo como 7.** O `robfig/cron` só aceita 0 a 6 (D17), e faixa que começa depois do domingo não
  pode terminar nele, nem com o nome: `FRI-SUN` e `MON-SUN` são recusados como `5-7`, e `SUN-SUN` e
  `0-0` passam. Alternativa: reescrever o 7 antes do
  parser, o que pede tratar listas e faixas e é o segundo parser que o D17 evitou. Recomendação
  seguida: não reescrever. A ajuda do campo cita `0` ou `SUN`, e a recusa também cita a lista `5,6,0`,
  porque trocar `5-7` por `5-SUN` continuaria recusado. O custo é quem cola a linha do crontab ajustar
  a expressão.
- **Clonar heartbeat.** Recomendação: não fazer sem pedido. Se o cadastro em série virar rotina, o
  caminho barato é só no painel: abrir o formulário preenchido, sem rota nova.

- **Onde a última duração aparece.** Foi para a coluna Situação ("Sinal há 3 min · levou 12 s"), e a
  coluna Latência segue com `—` (T5). Alternativa: renomear a coluna para "Medida" e pôr a duração
  nela. Recomendação: manter, porque latência e tempo de execução são grandezas diferentes numa
  coluna só; o custo é a duração sumir no celular, com a coluna Situação.
- **Selo "dentro da tolerância".** Hoje a tarefa fica "Em dia" até vencer a tolerância. Um selo
  intermediário anteciparia o problema, mas põe um sexto estado na escala de gravidade. Recomendação:
  não ter, porque a escala única é o que faz o problema saltar aos olhos.

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

A lista e o detalhe mostram "em execução" pela mesma regra: `last_start_at` diferente de `NULL` em
`up` ou `waiting`. Atrasada, falhando ou pausada, a tarefa diz isso, e não que está rodando. O
detalhe trocou "Último início" por "Em execução desde", porque o início consumido virava "Nunca".
Sem superfície nova: nenhuma rota, e o dado sai só para o dono.

Dois acertos de regra saíram daqui. `waiting` passou a ser nunca ter terminado uma execução, com
sucesso ou falha, em vez de nunca ter pingado: a tarefa nova que manda `/start` e morre ficava "Em
dia" até o prazo. A retomada apaga o início aberto: sem isso, ele sobrevivia à pausa e virava uma
execução de dias. Com o início descartado, a tarefa nova segue em `waiting`, com o texto "Aguardando
o primeiro sucesso".

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
| **S** Rota sem sessão | `protegida`, como as demais | `TestCronPreviewWithoutSessionIs401`, `TestEveryRegisteredRouteDeniesByDefault`, `TestEveryResourceRouteRejectsRevokedFamily` |
| **T** Entrada ruim virar 500 | mesma validação de `NewCron`, com tamanho conferido antes do parser | `TestCronPreviewRefusesWhatTheFormWouldRefuse` |
| **I** Chave da consulta ecoada | campo fixo `query` | `TestCronPreviewQueryIsStrict` |
| **I** Fuso como caminho de arquivo | `time.LoadLocation` recusa `..`; caso de teste com caminho | `TestCronPreviewRefusesWhatTheFormWouldRefuse` |
| **D** Muitas consultas | limite por usuário da cadeia `protegida`, o mesmo das outras rotas, e 400 ms de espera no painel | `espera a digitação parar: não é uma consulta por tecla` |

## Descrição (PR 5)

`description` é texto puro de até 1000 caracteres, com quebra de linha, gravado em
`heartbeats.description` (00022). Sem descrição é `""`, nunca `null`, nas leituras. Segue o contrato
das tags: no `PATCH`, ausente não mexe, e `""` e `null` limpam. Mudar o texto não recalcula o prazo.
A validação é a do nome, linha a linha, e só quebra de linha e tab passam entre os controles; o
retorno de carro é recusado, porque o navegador já o normaliza. A recusa de caractere aponta a linha
em `description[i]`, contada desde 0 no texto recebido, a mesma forma de `items[i]` na página de
status: o envelope segue `{field, reason}`, e o cliente não repete a regra de caracteres. O painel
soma as linhas em branco que o envio tira do começo, e mostra a linha da tela, contada desde 1.
Aparece no detalhe e no formulário,
não na tabela e não nos avisos. O teto de 1000 cobre um pequeno roteiro, sem o peso dos 10.000 de lá
no poll da lista. Sem rota nova: quem escreve e quem lê é o dono, pela API de sessão.

| Ameaça | Mitigação | Teste |
|---|---|---|
| **I** Texto de A lido por B | a RLS de `heartbeats` e o filtro por `user_id` já valem para a coluna | `TestAnotherUsersHeartbeatDescriptionStaysOut` |
| **T** B alterar o texto de A | `PATCH` alheio é 404, e o texto fica intacto | `TestAnotherUsersHeartbeatDescriptionStaysOut` |
| **T** Lixo gravado por fora da validação | CHECK de tamanho, em caracteres; NUL o Postgres já não guarda | `TestHeartbeatDescriptionCheckRefusesWhatTheCodeWouldNot` |
| **T** Marcação virar código na tela | o React escapa, e a tela não usa HTML cru | `marcação na descrição aparece como texto` |
| **T** Entrada ruim virar 500 ou gravar parte | validada antes de abrir a transação, com o `PATCH` inteiro recusado | `TestPatchWithInvalidDescriptionChangesNothing`, `TestValidateDescription`, `TestHeartbeatValidationBoundaries` |
| **I** Texto sair para terceiros ou pelo ping | o aviso leva só o nome, e o ping responde sem dados | `TestHeartbeatDescriptionStaysOutOfThePingAndTheNotices` |
| **D** Texto sem teto | 1000 caracteres, recusados antes do banco | `TestValidateDescription` |
| **I** Recusa ecoar o texto | a resposta leva só o índice da linha | `TestInvalidDescriptionNamesTheLineWithoutEchoingIt` |

O dono pode escrever um segredo ali, como em qualquer campo livre, e ele iria para o backup do banco.
A ajuda do formulário não pede dado sensível, e o CHECK não tem como detectar isso.

## Ordenação

Um seletor na barra de filtros, com a ordem em `?ordem=`. A gravidade segue como padrão e não vai à
URL, e valor desconhecido abre nela. Ordem não é filtro: não esconde linha, não conta para "Limpar
filtros" e sobrevive a ele. A manchete não muda com a ordem, e a linha de problema mantém o acento em
qualquer posição. O empate fica com o mais grave.

Nas ordens por medida, a falta dela não diz a mesma coisa para todos: a API devolve latência nula
quando o último check foi `down`, e a tarefa não tem uptime.

| Faixa | Quem | Ordem dentro da faixa |
|---|---|---|
| 1 | Sem a medida, com problema (fora do ar, degradado, atrasada, falhando) | gravidade e nome |
| 2 | Com a medida | maior latência, ou menor tempo no ar |
| 3 | Sem a medida, sem problema | gravidade e nome |

## Migrations

Cada PR que precisa de migration leva o próximo número livre, e por isso os PRs são empilhados e
têm de entrar na ordem: `storage.Migrate` recusa número menor que o aplicado. A 00019 também remove
`heartbeats_due_idx`, a pendência registrada na §4 da spec de heartbeats.
