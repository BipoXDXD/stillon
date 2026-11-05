-- Heartbeats (docs/superpowers/specs/2026-11-05-heartbeats-design.md). As do
-- dono rodam sob InUserTx e filtram por user_id; as do ping, sob InPingTx; as
-- do avaliador, sob InProberTx.

-- name: InsertHeartbeat :exec
INSERT INTO heartbeats (id, user_id, name, ping_token_hash, period_sec, cron_expr, timezone,
                        grace_sec, enabled, created_at, deadline_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- A trava serializa pings simultâneos do mesmo token: o segundo lê o estado
-- que o primeiro gravou, e é isso que faz a repetição ter um efeito só (D9).
-- (O nome evita "token": o gosec lê a constante gerada como credencial
-- embutida, G101.)
-- name: LockHeartbeatForPing :one
SELECT * FROM heartbeats WHERE ping_token_hash = $1 FOR UPDATE;

-- name: OpenHeartbeatIncident :exec
INSERT INTO incidents (id, heartbeat_id, started_at, cause)
VALUES (sqlc.arg(id), sqlc.arg(heartbeat_id)::uuid, sqlc.arg(started_at), sqlc.arg(cause));
