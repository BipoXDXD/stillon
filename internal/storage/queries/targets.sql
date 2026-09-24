-- Toda query daqui filtra por user_id E roda sob a RLS de InUserTx. As duas
-- barreiras são independentes de propósito (spec §5.5): um WHERE esquecido
-- é pego pela política, e uma política desligada é pega pelo WHERE.

-- name: InsertTarget :exec
INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
                     expected_status, headers_enc, degraded_threshold_ms, enabled,
                     next_run_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetTarget :one
SELECT * FROM targets WHERE user_id = $1 AND id = $2;

-- name: GetTargetForUpdate :one
SELECT * FROM targets WHERE user_id = $1 AND id = $2 FOR UPDATE;

-- Paginação por cursor (spec §9.1): o id é UUIDv7, ordenável por tempo, e o
-- cursor é o último id da página anterior.
-- name: ListTargets :many
SELECT * FROM targets
 WHERE user_id = sqlc.arg(user_id)
   AND (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before)::uuid)
 ORDER BY id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- name: UpdateTarget :exec
UPDATE targets
   SET name = $3, url = $4, method = $5, interval_sec = $6, timeout_ms = $7,
       expected_status = $8, headers_enc = $9, degraded_threshold_ms = $10,
       enabled = $11, next_run_at = $12
 WHERE user_id = $1 AND id = $2;

-- name: DeleteTarget :execrows
DELETE FROM targets WHERE user_id = $1 AND id = $2;

-- name: ListChecks :many
SELECT c.checked_at, c.status, c.latency_ms, c.status_code, c.error_kind
  FROM checks c
  JOIN targets t ON t.id = c.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND c.target_id = sqlc.arg(target_id)
   AND c.checked_at >= sqlc.arg(from_at)
   AND c.checked_at < sqlc.arg(to_at)
 ORDER BY c.checked_at;
