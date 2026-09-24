# Pendências que atravessam o Plano 4

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` no Fly.io.** Herdada do Plano 1, ainda aberta, e agora mais cara: a
   status page pública tem limite por IP (120/min, burst 60). Sem `TRUSTED_PROXIES`, todos os
   visitantes dividem **um** balde, e a página começa a responder 429 justamente durante um
   incidente, quando mais gente a abre.
2. **Papel comum do Postgres no Fly.io**, dono das tabelas, sem superusuário nem `BYPASSRLS`
   (D8 do Plano 2). A migration 00006 cria quatro tabelas, três funções e uma política em
   `targets`: o papel precisa de `CREATE` no schema `public`.
3. **`METRICS_ADDR` no Fly.io.** Decidir no Plano 5 entre `off` e um bind na interface privada.
4. **E-mail.** `RESEND_API_KEY` e `EMAIL_FROM`, as duas ou nenhuma (ver o Plano 3).
5. **`PUBLIC_BASE_URL` é o host da API.** Agora vale também para a status page: o
   `public_url` de `GET /status-pages` é `PUBLIC_BASE_URL/p/{slug}`, servido pela API.
6. **Primeira manutenção depois do deploy.** Sem rollup nenhum, a primeira rodada consolida
   os últimos 62 dias de checks de uma vez (uma consulta só): tudo o que as partições ainda
   guardam. Com muitos alvos, é a rodada mais pesada que o job faz; as seguintes voltam só
   2 horas.

## Resolvidas no Plano 4

| Origem | Como fechou |
|---|---|
| D6 do Plano 2 | `GET /targets/{id}/checks` acima de 48h lê `check_rollup_1h`; a resposta diz `resolution` |
| Retenção (spec §12) | `RunMaintenance` a cada 15 min: partições, rollups, `DROP` de partições vencidas, `DELETE` de rollups (13 meses), auditoria (12 meses), sessões (7 dias após vencer) e notificações encerradas (30 dias) |
| `MaintainChecksPartitions` sem teste, panic com `every <= 0` | Substituído por `RunMaintenance`, que recusa `every <= 0` |
| Canal que quebra depois de verificado | `channel_health`; `GET /channels` mostra `failing`, `last_error` e `last_failure_at` |
| Apagão em massa (429 esgotando as tentativas) | 429 devolve a tentativa por até 1h; o mesmo canal recebe em série e espera depois de um 429 |
| Abertura e fechamento no mesmo lote chegando fora de ordem | Mesmo canal em série, na ordem de enfileiramento |
| Status page (spec §10) | Cadastro do dono e `/p/{slug}` em HTML, sem JavaScript |
| Incidentes de alvos que saíram da página | Decisão: só os alvos que estão na página agora |

## Limites conhecidos do Plano 4

- **10 KB vale para cinco serviços.** O pior caso com cinco (dias espalhados pelas quatro
  cores) tem ~9,3 KB. A página aceita até 20 serviços, e aí passa dos 10 KB. É HTML sem
  compressão; o limite da spec é de transferência, e gzip resolveria se virar problema.
- **O rollup atrasa até ~1h15.** A hora em curso não entra, e a última hora fechada entra na
  rodada seguinte (a cada 15 min). A barra de hoje e a latência de 24h da página pública, e
  as janelas acima de 48h de `/checks`, ficam esse tanto atrás. O estado atual da página não
  sofre com isso: vem do último check.
- **A latência da página é aproximada:** média das medianas horárias, ponderada pelos
  sucessos de cada hora, arredondada. Não é a mediana exata das 24h.
- **Dias da barra em UTC.** Sem JavaScript, não há fuso do visitante; os horários dos
  incidentes também saem em UTC, e a página diz isso.
- **Cache de 30s em dois lugares.** O navegador (e qualquer proxy) guarda a resposta 30s, e o
  processo guarda a projeção 30s por slug. Uma mudança feita pelo painel tira o slug do cache
  da instância que a recebeu; quem já abriu a página vê a mudança em até 30s (cache HTTP).
  Checks, rollups e incidentes novos aparecem quando o cache vence. A página recém-publicada
  aparece na hora: o 404 não é guardado.
- **Cache por processo.** Com várias instâncias da API, cada uma tem o seu, e a mudança feita
  numa leva até 30s para valer nas outras; o teto é de 1.000 slugs por instância.
- **Um 429 dentro do perdão ainda conta como `retry` em `notify_total`.** Os adiados sem envio
  contam como `deferred`.
- **Um webhook que responde 429 para sempre** segura cada notificação por até 1h antes de ela
  começar a gastar tentativas. Depois disso, as 6 tentativas seguem o backoff normal.
- **Uma falha transitória para o canal no lote.** Depois de um 429, timeout, erro de rede ou
  5xx, o resto das notificações daquele canal no lote é adiado sem envio. Fora do 429 dentro
  de 1h, a tentativa conta, e quem está no teto falha de vez com o mesmo motivo. Um canal
  fora do ar esgota as 6 tentativas das notificações dele em ~15 min, como antes.
- **Lease de 3 min** (8 × (10 s + 5 s) + 1 min): um processo que morre no meio de um lote
  devolve as notificações à fila 3 min depois.
- **Canal `failing` é derivado de "última falha depois do último sucesso".** Um canal que
  alterna falha e sucesso fica `failing` só até a próxima entrega que der certo.
- **Retenção de checks por partição inteira:** os checks duram entre 30 e 61 dias.
- **429 e 413 da cadeia `/p/` saem como envelope JSON**, como os de `/verify/`.
- **O DROP da partição vencida desiste em 2 s** se `checks` estiver ocupada (`lock_timeout`),
  e tenta de novo na rodada seguinte. Os DELETEs da retenção rodam antes, noutra transação.

## Para o Plano 5

- Telas de incidentes, canais (com o estado `failing`, `last_error` e `last_failure_at`) e
  status page (cadastro, pré-visualização, link público), consumindo os contratos deste plano.
- Gráficos do detalhe do alvo: `resolution` decide entre a série bruta (`items` com
  `checked_at`) e a horária (`items` com `bucket`, `total`, `ok`, `p50_ms`…). Janela máxima:
  400 dias.
- Documentar no cadastro de alvos que headers **não** seguem redirect para outro host:porta
  nem rebaixamento https→http (I3 do Plano 2).
- Documentar no cadastro de canais que webhook de canal de **fórum** do Discord é recusado.
- `Spec.Headers` decifrado com `LogValue` que redija os valores (M6 do Plano 2).
- Porta de métricas em desenvolvimento com `--role=api` e `--role=prober` no mesmo host (M7).
- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA; troca de autenticador;
  `HashPasswordWith` com parâmetros fora de faixa (herdados).
- Rota para desabilitar canal, se o painel precisar (a coluna `enabled` já é respeitada).

## Plano próprio, fora da sequência

- **R2**, por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.

## Dívidas menores herdadas, ainda abertas

- Partições não herdam a RLS (a aplicação nunca consulta partição; está comentado).
- O comentário de `targets.sql` diz que toda query filtra por `user_id`; o `INSERT` não filtra.
- `initialisms` sem `ip` gera `IpHash` nos modelos de auth.
- `targets.Service.Get`/`List` decifram os headers só para devolver os nomes; uma linha
  corrompida derruba o `List`.
- `apply` compara antes de normalizar; `Update` revalida a URL guardada; `Service.List` com
  `limit` fora de faixa cai para 50; `0` explícito em `POST` vira padrão e em `PATCH` dá 400.
- `Probe` com `Timeout == 0` gera contexto vencido (a validação garante ≥ 1000 ms).
- `TestConcurrentMigrationsSerialize` não confere a versão final do schema.
- `GRANT CREATE ON DATABASE` do testdb não sobrevive ao `Restore` (falta comentário).
- Logs do testcontainers poluem a saída de teste; nomes de teste em inglês ao lado de helpers
  em português.

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-statuspage-e-retencao.md`: decisões deste plano, com o
  custo de cada uma estar errada.
- `docs/superpowers/plans/2026-09-24-incidentes-e-notificacoes.md`: decisões D1–D24 do Plano 3.
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`: o estado anterior a este plano.
