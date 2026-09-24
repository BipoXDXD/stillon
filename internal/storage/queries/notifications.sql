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
    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts
)
SELECT cl.id, cl.event, cl.attempts,
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

-- name: RetryNotification :exec
UPDATE notifications SET next_attempt_at = $2, last_error = $3 WHERE id = $1;

-- status é 'failed' ou 'cancelled'.
-- name: CloseNotification :exec
UPDATE notifications SET status = $2, last_error = $3 WHERE id = $1;

-- Falha definitiva vai para a trilha de auditoria (spec §8). audit_log não
-- tem RLS (D7 do Plano 2).
-- name: InsertAudit :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, at) VALUES ($1, $2, $3, $4, $5, $6);
