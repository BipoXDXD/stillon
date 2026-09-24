-- Dispatcher (spec §8). Rodam só sob InProberTx.

-- Reivindicação com lease, no mesmo molde de ClaimDueTargets: o UPDATE
-- empurra next_attempt_at para lease_until e conta a tentativa na mesma
-- instrução que trava as linhas, e o commit acontece antes do envio. A
-- CTE devolve, na mesma ida ao banco, tudo o que o envio precisa.
-- name: ClaimDueNotifications :many
WITH claimed AS (
    UPDATE notifications n
       SET next_attempt_at = sqlc.arg(lease_until)::timestamptz, attempts = n.attempts + 1
     WHERE n.id IN (
           SELECT id FROM notifications
            WHERE status = 'pending' AND next_attempt_at <= sqlc.arg(now)::timestamptz
            ORDER BY next_attempt_at
            LIMIT sqlc.arg(max_rows)::int
              FOR UPDATE SKIP LOCKED)
    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts, n.created_at
)
SELECT cl.id, cl.event, cl.attempts, cl.created_at,
       c.id AS channel_id, c.user_id, c.kind, c.config_enc, c.enabled, c.verified_at,
       i.id AS incident_id, i.started_at, i.resolved_at, i.cause,
       t.name AS target_name
  FROM claimed cl
  JOIN notif_channels c ON c.id = cl.channel_id
  JOIN incidents i ON i.id = cl.incident_id
  JOIN targets t ON t.id = i.target_id;

-- name: MarkNotificationSent :exec
UPDATE notifications SET status = 'sent', sent_at = sqlc.arg(now)::timestamptz, last_error = NULL
 WHERE id = sqlc.arg(id);

-- refund devolve a tentativa que a reivindicação contou: um 429 é o
-- provedor pedindo espera, não uma falha do envio (Plano 4, apagão em massa).
-- name: RetryNotification :exec
UPDATE notifications
   SET next_attempt_at = sqlc.arg(next_attempt_at), last_error = sqlc.narg(last_error),
       attempts = attempts - CASE WHEN sqlc.arg(refund)::bool THEN 1 ELSE 0 END
 WHERE id = sqlc.arg(id);

-- status é 'failed' ou 'cancelled'.
-- name: CloseNotification :exec
UPDATE notifications SET status = $2, last_error = $3 WHERE id = $1;

-- Falha definitiva vai para a trilha de auditoria (spec §8). audit_log não
-- tem RLS (D7 do Plano 2).
-- name: InsertAudit :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, at) VALUES ($1, $2, $3, $4, $5, $6);

-- Saúde do canal (Plano 4): o último sucesso e a última falha definitiva.
-- name: MarkChannelSuccess :exec
INSERT INTO channel_health (channel_id, last_success_at) VALUES (sqlc.arg(channel_id), sqlc.arg(now)::timestamptz)
ON CONFLICT (channel_id) DO UPDATE SET last_success_at = EXCLUDED.last_success_at;

-- name: MarkChannelFailure :exec
INSERT INTO channel_health (channel_id, last_failure_at, last_error)
VALUES (sqlc.arg(channel_id), sqlc.arg(now)::timestamptz, sqlc.arg(last_error))
ON CONFLICT (channel_id) DO UPDATE
   SET last_failure_at = EXCLUDED.last_failure_at, last_error = EXCLUDED.last_error;
