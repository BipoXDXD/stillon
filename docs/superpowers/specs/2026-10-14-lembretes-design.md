# Lembretes de incidente aberto: design

Data: 2026-10-14. Implementa a política de avisos aprovada pelo dono em 2026-10-05
([continuidade](../reports/2026-10-05-continuacao.md), linhas 18–23). Revisa a §8 da
[spec principal](2026-09-22-stillon-design.md), que dizia "no máximo uma vez na abertura
e uma no fechamento".

## Política (aprovada; não muda aqui)

| Fase do incidente | Discord | E-mail |
|---|---|---|
| Abertura (3 falhas seguidas) | imediato | imediato |
| Lembrete enquanto a idade do incidente < 6 h | a cada 30 min | a cada 2 h |
| Lembrete com idade ≥ 6 h | a cada 6 h | a cada 24 h |
| Recuperação (5 sucessos seguidos) | um aviso | um aviso |

- A histerese 3/5 fica como está.
- Reconhecer (ack) o incidente interrompe **só** os lembretes. O aviso de recuperação
  sai do mesmo jeito.
- Idade do incidente = `agora − incidents.started_at` (a primeira falha da sequência).

## Regra do próximo lembrete

Para cada par (incidente aberto, canal), a base é o aviso mais recente desse par entre
`opened` e `reminder`, pelo `created_at`, que é o instante em que ele foi enfileirado:

```
idade_na_base = base.created_at − incidente.started_at
intervalo     = tabela[canal.kind][idade_na_base < 6 h ? "curto" : "longo"]
vence_em      = base.created_at + intervalo
```

O lembrete é enfileirado quando `agora ≥ vence_em`. A troca de faixa acontece no
lembrete que cai depois das 6 h.

Exemplo no e-mail: a abertura sai em 0 h e os lembretes em 2 h, 4 h, 6 h e 30 h. No
lembrete das 4 h a idade ainda é menor que 6 h, então o próximo vem 2 h depois. No das
6 h a idade já chegou a 6 h, então o próximo vem 24 h depois.

A regra é uma função pura, `NextReminder(kind, startedAt, baseAt) time.Time`, com a
tabela de intervalos num valor `Policy` em Go. Ela não lê relógio nem banco, e a
política de negócio fica num lugar só, com nome.

## Quem recebe lembrete

Só os pares que já têm a linha `opened` na `notifications` recebem lembrete. O fan-out
continua acontecendo só na transição.

- Um canal criado no meio do incidente não recebe lembrete. Isso mantém a regra
  "canais lidos na transição".
- O canal precisa estar habilitado e verificado **no momento do lembrete**.
- Um canal pausado não gera lembrete. Quando ele volta, o próximo tique enfileira um
  único lembrete (o vencido), não um por intervalo perdido.
- Um alvo pausado (`targets.enabled = false`) não gera lembrete, e o incidente continua
  aberto. Ao reativar, vale a regra do canal pausado: um único lembrete, o vencido. Um
  lembrete já na fila de alvo pausado é cancelado no envio.
- O incidente precisa estar aberto (`resolved_at IS NULL`), sem ack (`ack_at IS NULL`)
  e com `open_notified_at` preenchido.

## Esquema (migration 00012)

- O CHECK de `notifications.event` passa a aceitar `'opened'`, `'resolved'` e
  `'reminder'`.
- Nova coluna `seq int NOT NULL DEFAULT 0`. Ela vale 0 para `opened` e `resolved`, e
  1, 2, 3… para os lembretes do par (incidente, canal).
- O UNIQUE vira `(incident_id, channel_id, event, seq)`.
  - Assim, dois processos prober que calculam o mesmo lembrete colidem e só um insere
    (`ON CONFLICT DO NOTHING`). A idempotência vem do banco, não de lock em memória.
- A migration é expand-only e compatível com o binário anterior durante o deploy. As
  linhas antigas ficam com `seq = 0`.
  - O `EnqueueNotification` atual usa `ON CONFLICT (incident_id, channel_id, event)`,
    que precisa de um índice único exatamente com essas colunas. Por isso a query
    passa a usar a constraint nova.
  - Como o Postgres exige o alvo de conflito exato, o deploy é de passo único: o
    binário novo sobe junto com a migration. É o modelo atual do projeto, com goose
    rodando no boot.

## Job de lembretes

`notify.Reminders` roda no papel `prober`, ao lado do `Dispatcher`. Ele recebe
`Clock`, `Policy` e o pool, e tem um `Tick(ctx)` público para os testes.

A cada 30 s (`time.NewTicker`, como os outros jobs), sob `InProberTx`:

1. `ListReminderCandidates(now)` devolve, para cada par elegível:
   - incidente: `started_at`;
   - canal: `kind`;
   - o maior `seq` de lembrete;
   - o `created_at` da base.
2. Em Go: `vence := policy.NextReminder(kind, startedAt, baseAt)`. Se `now ≥ vence`,
   roda `EnqueueReminder(id, incident, channel, seq+1, now)` com `ON CONFLICT DO
   NOTHING`.
3. As consultas têm teto de linhas (`LIMIT`). O próximo tique continua de onde parou.

## Envio

- O dispatcher não muda de forma. O `semEnvio` ganha uma regra: um `reminder` vira
  `cancelled` se o incidente já resolveu ou recebeu ack. Para isso, o
  `ClaimDueNotifications` passa a trazer `i.ack_at` e `n.seq`.
- Mensagem (`message.go`), montada só com dados do banco:
  - assunto: `[StillOn] <alvo> continua fora do ar`;
  - corpo: `<alvo> continua fora do ar desde <started_at> (há <duracao(created_at −
    started_at)>).\nCausa: <causa>.\nReconheça o incidente no painel para parar os
    lembretes.\n`;
  - no Discord, o mesmo prefixo de cor da abertura.
- A idempotência no Resend continua `notification/<id>`. Cada lembrete é uma linha
  própria.
- Revisão do PR #73: o nome do alvo vai para `notifications.target_name` no
  enfileiramento (abertura, lembrete e fechamento). Antes ele era relido a cada
  tentativa, e renomear o alvo entre duas tentativas mudava o corpo, o que o Resend
  recusa com 409 para a mesma Idempotency-Key. Linhas de antes da 00012 ficam com
  `NULL` e usam o nome atual.

## Painel

- O texto de ajuda do ack em `PaginaIncidentes.tsx` passa a ser: "Reconhecer interrompe
  os lembretes; não resolve o incidente."
- A API não muda.

## Fora do escopo

- Lembrete para canal criado depois da abertura.
- Intervalos configuráveis pelo usuário.
- Proibir ack em incidente resolvido.
- Mudança na vinculação Discord.

## Testes (cada um precisa falhar com a regra sabotada)

- `NextReminder`, table-driven:
  - para cada canal, idade 0, logo abaixo de 6 h (5h59m59s), exatamente 6 h e acima;
  - sequência do exemplo do e-mail;
  - kind desconhecido falha alto.
- Job com Postgres real (testdb) e `clock.Fake`:
  - antes de vencer, nada;
  - vencido, um lembrete com `seq = 1`;
  - dois `Tick` no mesmo instante, um lembrete só;
  - com ack, nada;
  - resolvido, nada;
  - canal pausado ou não verificado, nada;
  - canal sem `opened`, nada;
  - depois das 6 h, o intervalo longo;
  - Discord e e-mail no mesmo incidente, cadências independentes.
- Dispatcher:
  - `reminder` pendente com incidente reconhecido ou resolvido vira `cancelled`;
  - `reminder` normal é enviado com o texto do lembrete.
- Migration: um `opened` duplicado continua colidindo, e lembretes com `seq`
  diferentes coexistem.
- E2E, se couber no `incident_e2e_test.go`: abertura, avanço do relógio, lembrete
  auditado como enviado, ack, sem novo lembrete.
