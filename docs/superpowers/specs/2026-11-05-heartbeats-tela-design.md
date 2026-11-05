# Heartbeats no painel: tela

Data: 2026-11-05. Continua a [spec de heartbeats](2026-11-05-heartbeats-design.md), que parou na API
(§7), e a do [painel denso](2026-11-05-painel-denso-design.md), que deixou a tabela pronta para um
segundo tipo de monitor. Identidade do redesign mantida: Fraunces, Inter, índigo, tokens.

## Objetivo

Cadastrar, editar, pausar, retomar e apagar heartbeats, rotacionar o token, ver o log e reconhecer
incidente, sem sair do painel. O heartbeat aparece na mesma tabela dos alvos. Nada de marca, texto ou
layout vem do healthchecks.io.

## Decisões

| # | Decisão | Motivo |
|---|---|---|
| T1 | Sem lista própria: a tabela de monitores é a lista. Rotas `/heartbeats/new`, `/heartbeats/:id` e `/heartbeats/:id/edit`, na aba Painel | Pedido do dono: junto dos alvos. Uma segunda lista dividiria a pergunta "o que está quebrado?" |
| T2 | No painel, a tarefa é "tarefa"; o objeto cadastrado é "heartbeat". O selo diz **Em dia, Atrasado, Falhando, Aguardando, Pausado**, nunca "No ar" nem "Fora do ar" | D20: "fora do ar" descreve um serviço, não um backup que não rodou |
| T3 | Forma e cor seguem o estado exibido: `late` e `failing` são `down`, `waiting` é `unknown`, `paused` é `pausado`. A gravidade, o acento e o filtro de estado vêm daí; só o texto muda (`linha.textoDoEstado`) | Uma escala de gravidade só. O botão "Fora do ar" conta as tarefas atrasadas: o filtro agrupa por gravidade, e o selo da linha diz o que é |
| T4 | Heartbeat não tem tag: com filtro de tag ligado ele some da tabela. O estado e a busca filtram no cliente | A rota não filtra, e são até 100 linhas já na tela |
| T5 | Latência, 24 h e Últimas 24 h ficam vazias (`—` e o minigráfico sem barras) na linha do heartbeat | Inventar medida nessas colunas mentiria. O tempo de execução está no log |
| T6 | Coluna "Alvo" vira "Monitor"; "de N alvos" vira "de N monitores"; a busca vira "Buscar por nome ou tag" | Os textos passam a cobrir os dois tipos |
| T7 | `GET /heartbeats?limit=100` em poll de 15 s, como o resumo. Uma página basta: 100 é o teto por usuário (D16). O `next_cursor` que vem com exatamente 100 é ignorado | Custo: +4 req/min, dentro do limite de 60 |
| T8 | **`ping_url` só em estado de componente.** A resposta do `POST` e do `rotate` é lida por um schema próprio e nunca entra no cache de consultas; o `Heartbeat` não tem o campo. Sem `navigate(..., { state })`, porque o estado do router vai para `history.state`, que o navegador persiste | §Segredo |
| T9 | A URL aparece num diálogo travado, no mesmo lugar em que foi gerada, com copiar e os exemplos de `curl`. Só fecha por "Já guardei" | Mesmo padrão dos códigos de recuperação. Recarregar perde a URL, e a saída é rotacionar |
| T10 | Rotacionar pede confirmação, e pausar também, com o que muda | A URL antiga para na hora; pausar interrompe os lembretes (D13) |
| T11 | Duração em quantidade + unidade (segundos, minutos, horas, dias), com os limites da §7 validados no cliente. A expressão cron quem valida é o servidor | Validar cron no cliente exigiria um segundo parser, que é o risco que o D17 evitou |
| T12 | O `PATCH` manda só o que mudou. `schedule` vai inteiro, e só se mudou de valor | O servidor recalcula o prazo só com mudança real, mas um `PATCH` vazio não tem por que existir |
| T13 | Em `/incidents` entra um aviso com os heartbeats de incidente aberto, com link ao detalhe, e o vazio verde não aparece nesse caso. O contador da aba soma os dois | D3 mantém as listas separadas, mas "Nenhum incidente aberto." com um heartbeat atrasado seria mentira |

## A manchete

A manchete fala do conjunto inteiro: alvos e heartbeats. Um filtro não muda isso.

| Situação | Manchete |
|---|---|
| Só heartbeat com problema | "Uma tarefa pede atenção." / "Duas tarefas pedem atenção." |
| Alvo e heartbeat com problema | "Um serviço e uma tarefa pedem atenção." |
| Só alvo com problema | a de hoje: "Um serviço pede atenção." |
| Sem problema, heartbeat aguardando o primeiro sinal | "Uma tarefa ainda sem sinal." (junto de alvo: "Um serviço ainda sem dados e uma tarefa sem sinal.") |
| Consulta de heartbeats falhou, sem problema conhecido | **"Não foi possível conferir as tarefas."** Nunca "Tudo no ar." |
| Tudo pausado | "Todos os monitores estão pausados." |
| Tudo bem | "Tudo no ar." |
| Nada cadastrado | a de hoje, só se a consulta de heartbeats respondeu |

Atrasado e falhando contam como problema; pausado não, mesmo com incidente aberto, como o alvo.
Enquanto a primeira resposta de heartbeats não chega, a tela fica no esqueleto: não saber não é
"tudo no ar". A falha da consulta vira um aviso com "Tentar de novo", e os dados velhos ficam na
tabela. A linha de detalhe ganha "Tarefas: 1 atrasada, 2 em dia.", e a meta, "2 de 3 tarefas em dia".

## Estados

| Tela | Estado | O que aparece |
|---|---|---|
| Detalhe | Carregando, erro, 404 | `EstadoDaConsulta`; sumiu num poll: volta ao painel com aviso, como o alvo |
| Detalhe | Nunca recebeu sinal | "Aguardando o primeiro sinal" e o prazo |
| Log | Vazio | "Nenhum sinal recebido." |
| Incidentes | Vazio | "Nenhum incidente." |
| Formulário | Limite de 100 | erro do formulário |
| Formulário | Campo recusado pelo servidor | erro no campo (`expr`, `timezone`, `period_sec`, `grace_sec`, `name`) |
| Diálogo da URL | Copiar falhou | aviso de erro; a URL segue selecionável |
| Painel | Consulta de heartbeats falhou | ver A manchete |

## Segredo

O `ping_url` é a credencial da tarefa.

| Lugar | Regra | Teste |
|---|---|---|
| URL, histórico | nada de caminho, query ou `history.state` | `a URL de ping não vai para o endereço, o histórico nem o armazenamento` |
| Cache de consultas | a resposta com `ping_url` não entra; o schema `Heartbeat` descarta o campo | idem, lendo o cache inteiro |
| `localStorage`, `sessionStorage` | nada | idem |
| Log | nenhum `console.*` recebe a resposta; erro de contrato leva só o caminho do campo (`lerCorpo`) | idem, espionando o console |

## Mobile (400 px)

A tabela herda as regras do painel denso. No detalhe, ações e cartões quebram linha; a URL e os
exemplos de `curl` quebram dentro do bloco (`break-all`), sem rolar a página. O log é uma lista, não
uma tabela: tem três campos curtos e cabe em uma ou duas linhas.

## Entrega

Três PRs empilhados, nesta ordem, cada um com a sabotagem dos testes novos no corpo:

1. **Monitores:** esta spec, schemas, cliente, heartbeats na tabela, na manchete, nos filtros e nos
   anúncios, e a rota do detalhe só com o que a linha leva. A manchete fica certa antes de haver
   como cadastrar pela tela.
2. **Cadastro:** formulário, edição, pausar, retomar, apagar, rotacionar e a URL uma vez só.
3. **Detalhe:** log de eventos, incidentes, reconhecer, o aviso em `/incidents` e o contador.
