-- Manutenção (spec §12): rodam sob InProberTx, no job do papel prober.

-- Consolida as horas cheias de [from_at, to_at) em check_rollup_1h. É
-- idempotente: rodar de novo sobre a mesma janela reescreve os mesmos números,
-- e um check que chegou atrasado entra na próxima rodada.
-- name: ConsolidateRollups :execrows
INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
SELECT c.target_id,
       to_timestamp(floor(extract(epoch FROM c.checked_at) / 3600) * 3600),
       count(*),
       count(*) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.50) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.95) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       percentile_disc(0.99) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
       max(c.latency_ms) FILTER (WHERE c.status <> 'down')
  FROM checks c
 WHERE c.checked_at >= sqlc.arg(from_at)::timestamptz
   AND c.checked_at < sqlc.arg(to_at)::timestamptz
 GROUP BY 1, 2
ON CONFLICT (target_id, bucket) DO UPDATE
   SET total = EXCLUDED.total, ok = EXCLUDED.ok, p50 = EXCLUDED.p50,
       p95 = EXCLUDED.p95, p99 = EXCLUDED.p99, max = EXCLUDED.max;

-- A marca d'água: a hora mais recente já consolidada. Sem rollup nenhum,
-- devolve a época Unix.
-- name: LatestRollupBucket :one
SELECT coalesce(max(bucket), 'epoch'::timestamptz)::timestamptz FROM check_rollup_1h;

-- name: DropChecksPartitionsBefore :one
SELECT drop_checks_partitions_before(sqlc.arg(cutoff)::timestamptz)::int;

-- name: DeleteOldRollups :execrows
DELETE FROM check_rollup_1h WHERE bucket < sqlc.arg(cutoff)::timestamptz;

-- Entregues, falhas e canceladas; pendente nunca sai por idade.
-- name: DeleteOldNotifications :execrows
DELETE FROM notifications WHERE status <> 'pending' AND created_at < sqlc.arg(cutoff)::timestamptz;

-- audit_log e sessions não têm RLS (D7 do Plano 2).
-- name: DeleteOldAudit :execrows
DELETE FROM audit_log WHERE at < sqlc.arg(cutoff)::timestamptz;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < sqlc.arg(cutoff)::timestamptz;

-- D6 do Plano 2: GET /targets/{id}/checks acima de 48h lê daqui. Roda sob
-- InUserTx e filtra por user_id, como ListChecks.
-- name: ListRollups :many
SELECT r.bucket, r.total, r.ok, r.p50, r.p95, r.p99, r.max
  FROM check_rollup_1h r
  JOIN targets t ON t.id = r.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND r.target_id = sqlc.arg(target_id)
   AND r.bucket >= sqlc.arg(from_at)
   AND r.bucket < sqlc.arg(to_at)
 ORDER BY r.bucket;
