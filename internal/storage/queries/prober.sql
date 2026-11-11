-- Estas queries rodam só sob InProberTx (app.scope = 'prober').

-- Reivindicação com lease (spec §6.1). O UPDATE grava o próximo next_run_at,
-- com jitter de ±10%, NA MESMA instrução que trava as linhas; o commit
-- acontece antes de discar. Segurar a transação durante a sonda não caberia:
-- o pool tem 10 conexões e o semáforo aceita 64 sondas.
--
-- SKIP LOCKED faz um segundo sondador pegar OUTROS alvos em vez de esperar
-- pelos que o primeiro travou (TestSkipLockedDoesNotWaitForAnotherProber).
--
-- user_id volta porque a avaliação de incidente enfileira as notificações
-- nos canais do dono (Plano 3); interval_sec, porque a histerese recomeça
-- depois de uma lacuna longa entre checks; as três colunas da regra de
-- conteúdo, porque a sonda julga o corpo pela regra que reivindicou, e a
-- cerca da gravação compara com ela.
--
-- O lote fica numa CTE MATERIALIZED, que roda uma vez só. Numa subconsulta
-- dentro do IN, um nested loop pode reexecutá-la a cada linha de fora, e o
-- LIMIT deixaria de limitar o lote (TestClaimDueTargetsRespectsTheLimitInAnyPlan).
-- name: ClaimDueTargets :many
WITH lote AS MATERIALIZED (
    SELECT id FROM targets
     WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz
     ORDER BY next_run_at
     LIMIT sqlc.arg(max_rows)::int
       FOR UPDATE SKIP LOCKED
)
UPDATE targets t
   SET next_run_at = sqlc.arg(now)::timestamptz
       + make_interval(secs => t.interval_sec * (0.9 + random() * 0.2))
 WHERE t.id IN (SELECT id FROM lote)
RETURNING t.id, t.user_id, t.url, t.method, t.timeout_ms, t.expected_status,
          t.headers_enc, t.degraded_threshold_ms, t.interval_sec,
          t.content_mode, t.content_value, t.content_json_pointer;

-- name: CountDueTargets :one
SELECT count(*) FROM targets
 WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz;

-- name: InsertCheck :exec
INSERT INTO checks (id, target_id, checked_at, status, latency_ms, status_code, error_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: EnsureChecksPartition :exec
SELECT ensure_checks_partition(sqlc.arg(month)::date);

-- Fencing da gravação (E2 da revisão de 2026-10-14). A sonda leva a URL, o
-- método e a regra de conteúdo da reivindicação; antes de gravar, o sondador
-- relê os do alvo. Se o dono trocou um deles com a sonda em voo, o PATCH
-- zerou a histerese, e o resultado julga outra coisa: a gravação é
-- descartada.
--
-- FOR KEY SHARE espera o FOR UPDATE do PATCH (targets.GetTargetForUpdate) e,
-- depois do commit dele, devolve a linha nova. É a mesma trava que a FK do
-- INSERT em checks já pegaria, então não cria disputa nova com a
-- reivindicação. Sem linha: o alvo foi apagado. O enabled vem no SELECT, e não
-- no WHERE, porque sem linha a pausa se confundiria com o alvo apagado.
-- name: LockTargetProbeSpec :one
SELECT url, method, enabled, content_mode, content_value, content_json_pointer
  FROM targets WHERE id = $1 FOR KEY SHARE;

-- Validade guardada do pior certificado visto no alvo (spec §5.2). Sem linha:
-- nenhuma leitura ainda. Quem decide se a leitura nova substitui é
-- prober.substituirValidade; a gravação corre depois da trava da histerese,
-- que serializa duas sondas do mesmo alvo.
-- name: GetTargetTLS :one
SELECT not_after, observed_at FROM target_tls WHERE target_id = $1;

-- name: SaveTargetTLS :exec
INSERT INTO target_tls (target_id, not_after, observed_at)
VALUES ($1, $2, $3)
ON CONFLICT (target_id) DO UPDATE
   SET not_after = EXCLUDED.not_after, observed_at = EXCLUDED.observed_at;
