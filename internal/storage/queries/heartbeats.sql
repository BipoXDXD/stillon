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

-- name: SaveHeartbeatPing :exec
UPDATE heartbeats
   SET deadline_at = sqlc.arg(deadline_at), last_ping_at = sqlc.narg(last_ping_at),
       last_success_at = sqlc.narg(last_success_at), last_start_at = sqlc.narg(last_start_at),
       last_signal = sqlc.narg(last_signal), failing_since = sqlc.narg(failing_since)
 WHERE id = sqlc.arg(id);

-- name: InsertHeartbeatEvent :exec
INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, runtime_ms) VALUES ($1, $2, $3, $4, $5);

-- O teto do log (D15), na transação do ping: apaga do evento de posição
-- max_events + 1, do mais novo para o mais velho, para trás. O id é UUIDv7,
-- e o índice (heartbeat_id, id DESC) resolve os dois lados.
-- name: TrimHeartbeatEvents :exec
DELETE FROM heartbeat_events e
 WHERE e.heartbeat_id = sqlc.arg(heartbeat_id)
   AND e.id <= (SELECT o.id FROM heartbeat_events o
                 WHERE o.heartbeat_id = sqlc.arg(heartbeat_id)
                 ORDER BY o.id DESC
                OFFSET sqlc.arg(max_events)::int LIMIT 1);

-- name: GetHeartbeat :one
SELECT * FROM heartbeats WHERE user_id = $1 AND id = $2;

-- O log do dono, do mais novo ao mais velho. O id é UUIDv7, e o cursor é o
-- último id da página anterior.
-- name: ListHeartbeatEvents :many
SELECT e.id, e.heartbeat_id, e.at, e.kind, e.runtime_ms
  FROM heartbeat_events e
  JOIN heartbeats h ON h.id = e.heartbeat_id
 WHERE h.user_id = sqlc.arg(user_id)
   AND e.heartbeat_id = sqlc.arg(heartbeat_id)
   AND (sqlc.narg(before)::uuid IS NULL OR e.id < sqlc.narg(before)::uuid)
 ORDER BY e.id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- Revogar é isto: o hash antigo deixa de casar com qualquer linha (D8).
-- name: SetHeartbeatPingHash :exec
UPDATE heartbeats SET ping_token_hash = $3 WHERE user_id = $1 AND id = $2;

-- Avaliador (D5): rodam sob InProberTx, com FOR UPDATE OF h SKIP LOCKED no
-- molde de ClaimDueTargets. Dois avaliadores pegam linhas diferentes, e o
-- índice único parcial é a garantia final de um incidente aberto por vez.
-- Sem incidente aberto, atrasado ou em falha, e fora do intervalo mínimo
-- entre aberturas (D11): quem está nele fica para um tique seguinte.
-- name: ClaimHeartbeatsToOpen :many
SELECT h.id, h.user_id, h.deadline_at, h.failing_since
  FROM heartbeats h
 WHERE h.enabled
   AND (h.deadline_at <= sqlc.arg(now)::timestamptz OR h.failing_since IS NOT NULL)
   AND (h.last_incident_at IS NULL OR h.last_incident_at <= sqlc.arg(cooldown_cut)::timestamptz)
   AND NOT EXISTS (SELECT 1 FROM incidents x WHERE x.heartbeat_id = h.id AND x.resolved_at IS NULL)
 ORDER BY h.deadline_at
 LIMIT sqlc.arg(max_rows)::int
   FOR UPDATE OF h SKIP LOCKED;

-- Com incidente aberto e saudável de novo. Pausado fica como está: sem
-- avaliação, como o alvo pausado sem sonda (D13).
-- name: ClaimHeartbeatsToClose :many
SELECT h.id, h.user_id, h.last_success_at, x.id AS incident_id, x.started_at
  FROM incidents x
  JOIN heartbeats h ON h.id = x.heartbeat_id
 WHERE x.resolved_at IS NULL
   AND h.enabled
   AND h.failing_since IS NULL
   AND h.deadline_at > sqlc.arg(now)::timestamptz
 ORDER BY x.id
 LIMIT sqlc.arg(max_rows)::int
   FOR UPDATE OF h SKIP LOCKED;

-- name: MarkHeartbeatIncident :exec
UPDATE heartbeats SET last_incident_at = sqlc.arg(now)::timestamptz WHERE id = sqlc.arg(id);

-- API do dono: as de cima e estas rodam sob InUserTx e filtram por user_id,
-- como as de targets.sql.

-- O teto por usuário: contar e inserir sob esta trava, como LockUserTargets.
-- name: LockUserHeartbeats :exec
SELECT pg_advisory_xact_lock(hashtextextended('stillon.heartbeats:' || sqlc.arg(user_id)::uuid::text, 0));

-- name: CountHeartbeats :one
SELECT count(*) FROM heartbeats WHERE user_id = $1;

-- O índice único parcial garante no máximo um incidente aberto.
-- name: GetHeartbeatView :one
SELECT sqlc.embed(h), x.id AS open_incident_id
  FROM heartbeats h
  LEFT JOIN incidents x ON x.heartbeat_id = h.id AND x.resolved_at IS NULL
 WHERE h.user_id = sqlc.arg(user_id) AND h.id = sqlc.arg(id);

-- Paginação por cursor: o id é UUIDv7, e o cursor é o último id da página.
-- name: ListHeartbeats :many
SELECT sqlc.embed(h), x.id AS open_incident_id
  FROM heartbeats h
  LEFT JOIN incidents x ON x.heartbeat_id = h.id AND x.resolved_at IS NULL
 WHERE h.user_id = sqlc.arg(user_id)
   AND (sqlc.narg(before)::uuid IS NULL OR h.id < sqlc.narg(before)::uuid)
 ORDER BY h.id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- FOR UPDATE: o PATCH lê a linha e recalcula o prazo, e um ping no meio se
-- perderia sem a trava.
-- name: GetHeartbeatForUpdate :one
SELECT * FROM heartbeats WHERE user_id = $1 AND id = $2 FOR UPDATE;

-- name: UpdateHeartbeat :exec
UPDATE heartbeats
   SET name = sqlc.arg(name), period_sec = sqlc.narg(period_sec), cron_expr = sqlc.narg(cron_expr),
       timezone = sqlc.narg(timezone), grace_sec = sqlc.arg(grace_sec), enabled = sqlc.arg(enabled),
       deadline_at = sqlc.arg(deadline_at), failing_since = sqlc.narg(failing_since)
 WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id);

-- name: DeleteHeartbeat :execrows
DELETE FROM heartbeats WHERE user_id = $1 AND id = $2;

-- open NULL lista tudo; true só abertos; false só resolvidos.
-- name: ListHeartbeatIncidents :many
SELECT i.id, h.id AS heartbeat_id, h.name AS heartbeat_name, i.started_at, i.resolved_at, i.cause, i.ack_at
  FROM incidents i
  JOIN heartbeats h ON h.id = i.heartbeat_id
 WHERE h.user_id = sqlc.arg(user_id)
   AND h.id = sqlc.arg(heartbeat_id)
   AND (sqlc.narg(open)::bool IS NULL OR (i.resolved_at IS NULL) = sqlc.narg(open)::bool)
   AND (sqlc.narg(before)::uuid IS NULL OR i.id < sqlc.narg(before)::uuid)
 ORDER BY i.id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- Reconhecer é idempotente: o COALESCE preserva o primeiro ack.
-- name: AckHeartbeatIncident :one
UPDATE incidents i
   SET ack_at = COALESCE(i.ack_at, sqlc.arg(now)::timestamptz)
  FROM heartbeats h
 WHERE h.id = i.heartbeat_id AND h.user_id = sqlc.arg(user_id)
   AND h.id = sqlc.arg(heartbeat_id) AND i.id = sqlc.arg(id)
RETURNING i.id, h.id AS heartbeat_id, h.name AS heartbeat_name, i.started_at, i.resolved_at, i.cause, i.ack_at;
