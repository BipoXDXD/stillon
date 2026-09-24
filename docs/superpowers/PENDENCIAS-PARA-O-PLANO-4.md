# Pendências que atravessam o Plano 3

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` no Fly.io.** Herdada do Plano 1, ainda aberta. Sem ela, o rate limit por
   IP (inclusive o de `/verify/`) e o `ip_hash` da auditoria colapsam num valor único.
2. **Papel comum do Postgres no Fly.io**, dono das tabelas, sem superusuário nem `BYPASSRLS`
   (D8 do Plano 2). Os três detalhes de deploy continuam valendo (ver
   `PENDENCIAS-PARA-O-PLANO-3.md`). A migration 00005 cria quatro tabelas novas e troca uma
   função: o papel precisa de `CREATE` no schema `public`.
3. **`METRICS_ADDR` no Fly.io.** Decidir no Plano 5 entre `off` e um bind na interface privada.
4. **E-mail.** `RESEND_API_KEY` e `EMAIL_FROM` no Fly.io, as duas ou nenhuma. O domínio do
   remetente precisa estar verificado no Resend, ou todo envio volta 403 e vira falha definitiva.
   Sem elas, o cadastro de e-mail responde `400 kind/unavailable`, e o Discord segue funcionando.
5. **`PUBLIC_BASE_URL` precisa ser o host da API.** O link de verificação é
   `PUBLIC_BASE_URL/verify/{token}`, servido pela API, não pelo painel.

## Resolvidas no Plano 3

| Origem | Como fechou |
|---|---|
| Plano 2, ponto de extensão | `gravar` avalia a histerese na mesma transação do check |
| M4 | Check fora de ordem é ignorado pela histerese e gravado em `checks` |
| D9/M1 | `Scheduler.Reconcile` ao subir, a cada 30 s e a cada transição |
| `Scheduler.Run` silencioso com banco fora | `/readyz` pinga o banco (papel `api`) |
| Advisory lock de partição | `ensure_checks_partition` com `pg_advisory_xact_lock` (migration 00005) |
| `POST /metrics` sem trava | `TestMetricsServerOnlyAnswersGET` |
| `METRICS_ADDR=OFF` | `strings.EqualFold` |
| Tabela das 6 rotas; bordas de `/targets` e `/checks` via HTTP | `TestTargetRoutesRequireAuthentication`, `TestTargetQueriesRejectMalformedValues`, `TestChecksWindowIsHalfOpen` |
| `limit` não numérico reportava `out_of_range` | Agora `invalid` (D22) |

## Limites conhecidos do Plano 3

- **Entrega at-least-once (D7).** Um processo que morre entre o envio e a gravação do desfecho
  reenvia depois do lease (2 min). O Resend deduplica por `Idempotency-Key`; o Discord recebe
  a mensagem duas vezes.
- **Escopo `verify` limita linha, não coluna.** Nele, um `UPDATE` arbitrário poderia mudar o
  `user_id` da linha do token. A trava é haver uma única query nesse escopo (`ConfirmChannel`).
- **Renomear o alvo entre duas tentativas de e-mail** muda o corpo. O Resend responde 409
  `invalid_idempotent_request`, e a notificação falha de vez (D17).
- **`incidents_open` é global**: com vários sondadores, agregue com `max` (D19).
- **Alvo desativado mantém o incidente aberto; alvo apagado leva o histórico** (D18). A status
  page do Plano 4 precisa decidir se mostra incidentes de alvos que saíram da página.
- **Não há rota para desabilitar canal.** A coluna `enabled` existe e é respeitada no fan-out e
  no envio, mas só dá para mudar por SQL. O Plano 5 decide se o painel precisa disso.
- **O dispatcher só roda no papel `prober`.** Com `--role=api` sozinho, as notificações ficam
  na outbox até um sondador subir.
- **`TestIncidentFlowsFromProbeToAuditedNotification` passa por duas guardas.** A revalidação
  da allowlist e o `safedial` recusam a URL interna; o teste prova o fluxo, não qual das duas
  agiu. A revalidação sozinha é travada por `TestDiscordRevalidatesTheURLBeforeSending`.

## Para o Plano 4

- **R2**, adiada por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.
- **D6 do Plano 2:** `GET /targets/{id}/checks` com janela acima de 48h passa a ler
  `check_rollup_1h`.
- **Retenção (spec §12):** `DROP` das partições vencidas de `checks`; `audit_log` com 12 meses;
  e `notifications` já entregues, falhas e canceladas, que hoje crescem para sempre (sugestão:
  30 dias, no mesmo job de manutenção).
- **Status page:** o histórico de incidentes resolvidos (spec §10.2) lê `incidents` por alvo;
  `incidents_target_idx (target_id, id DESC)` já atende.

## Para o Plano 5

- Telas de incidentes (lista, reconhecer) e de canais (cadastro, estado de verificação,
  reenvio), consumindo os contratos das Tasks 8 e 9 do Plano 3.
- Documentar no cadastro de alvos que headers **não** seguem redirect para outro host:porta
  nem rebaixamento https→http (I3 do Plano 2).
- `Spec.Headers` decifrado com `LogValue` que redija os valores (M6 do Plano 2).
- Porta de métricas em desenvolvimento com `--role=api` e `--role=prober` no mesmo host (M7).
- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA; troca de autenticador;
  `HashPasswordWith` com parâmetros fora de faixa (herdados).

## Dívidas menores herdadas do Plano 2, ainda abertas

- Partições não herdam a RLS (a aplicação nunca consulta partição; está comentado).
- `MaintainChecksPartitions` sem teste; usa ticker real e dá panic com `every <= 0`.
- O comentário de `targets.sql` diz que toda query filtra por `user_id`; o `INSERT` não filtra.
- `initialisms` sem `ip` gera `IpHash` nos modelos de auth.
- `targets.Service.Get`/`List` decifram os headers só para devolver os nomes; uma linha
  corrompida derruba o `List` (o `ChannelService.List` do Plano 3 já não tem esse defeito).
- `apply` compara antes de normalizar; `Update` revalida a URL guardada; `Service.List` com
  `limit` fora de faixa cai para 50; `0` explícito em `POST` vira padrão e em `PATCH` dá 400.
- `Probe` com `Timeout == 0` gera contexto vencido (a validação garante ≥ 1000 ms).
- `TestConcurrentMigrationsSerialize` não confere a versão final do schema.
- `GRANT CREATE ON DATABASE` do testdb não sobrevive ao `Restore` (falta comentário).
- Logs do testcontainers poluem a saída de teste; nomes de teste em inglês ao lado de helpers
  em português.

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-incidentes-e-notificacoes.md`: decisões D1–D24 deste plano,
  com o custo de cada uma estar errada.
- `docs/superpowers/plans/2026-09-24-alvos-e-sondador.md`: decisões D1–D16 do Plano 2.
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md`: o estado anterior a este plano.
