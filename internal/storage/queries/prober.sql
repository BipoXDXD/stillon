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
-- nos canais do dono (Plano 3).
-- name: ClaimDueTargets :many
UPDATE targets t
   SET next_run_at = sqlc.arg(now)::timestamptz
       + make_interval(secs => t.interval_sec * (0.9 + random() * 0.2))
 WHERE t.id IN (
       SELECT id FROM targets
        WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz
        ORDER BY next_run_at
        LIMIT sqlc.arg(max_rows)::int
          FOR UPDATE SKIP LOCKED)
RETURNING t.id, t.user_id, t.url, t.method, t.timeout_ms, t.expected_status,
          t.headers_enc, t.degraded_threshold_ms;

-- name: CountDueTargets :one
SELECT count(*) FROM targets
 WHERE enabled AND next_run_at <= sqlc.arg(now)::timestamptz;

-- name: InsertCheck :exec
INSERT INTO checks (id, target_id, checked_at, status, latency_ms, status_code, error_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: EnsureChecksPartition :exec
SELECT ensure_checks_partition(sqlc.arg(month)::date);
