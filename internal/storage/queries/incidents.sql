-- Detecção (spec §7): estas rodam só sob InProberTx, na MESMA transação do
-- INSERT do check (prober.Scheduler.avaliar).

-- name: EnsureTargetHealth :exec
INSERT INTO target_health (target_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- A trava da linha serializa duas sondas do mesmo alvo gravando ao mesmo
-- tempo (M4 do Plano 2): a segunda lê o estado que a primeira gravou.
-- name: LockTargetHealth :one
SELECT * FROM target_health WHERE target_id = $1 FOR UPDATE;

-- name: SaveTargetHealth :exec
UPDATE target_health
   SET consecutive_failures = $2, consecutive_successes = $3,
       streak_since = $4, last_checked_at = sqlc.arg(last_checked_at)::timestamptz
 WHERE target_id = $1;

-- name: GetOpenIncident :one
SELECT * FROM incidents WHERE target_id = $1 AND resolved_at IS NULL;

-- name: OpenIncident :exec
INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, $4);

-- name: ResolveIncident :execrows
UPDATE incidents SET resolved_at = sqlc.arg(resolved_at)::timestamptz
 WHERE id = sqlc.arg(id) AND resolved_at IS NULL;

-- Idempotência no banco (spec §8): a corrida é resolvida pelo Postgres.
-- Quem vê 0 linhas não enfileira.
-- name: MarkOpenNotified :execrows
UPDATE incidents SET open_notified_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id) AND open_notified_at IS NULL;

-- name: MarkCloseNotified :execrows
UPDATE incidents SET close_notified_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id) AND close_notified_at IS NULL;

-- Fan-out (spec §8, §5.8): só canais habilitados E verificados.
-- name: ListDeliverableChannels :many
SELECT id FROM notif_channels
 WHERE user_id = $1 AND enabled AND verified_at IS NOT NULL
 ORDER BY id;

-- name: EnqueueNotification :exec
INSERT INTO notifications (id, incident_id, channel_id, event, next_attempt_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(incident_id), sqlc.arg(channel_id), sqlc.arg(event),
        sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (incident_id, channel_id, event, seq) DO NOTHING;

-- Reconciliação das métricas (D9/M1 do Plano 2).
-- name: CountOpenIncidents :one
SELECT count(*) FROM incidents WHERE resolved_at IS NULL;

-- name: ExistingTargetIDs :many
SELECT id FROM targets WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- Histórico (API): rodam sob InUserTx e filtram por user_id, como as de
-- targets.sql. open NULL lista tudo; true só abertos; false só resolvidos.
-- target_id NULL lista todos os alvos; um id de alvo alheio não casa com
-- nada e devolve a lista vazia (reconciliação dos Planos 5–7).
-- O id é UUIDv7, gerado quando o incidente abre: ordenar por ele é ordenar
-- pela abertura, e o cursor é o último id da página anterior.
-- name: ListIncidents :many
SELECT i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at
  FROM incidents i
  JOIN targets t ON t.id = i.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND (sqlc.narg(open)::bool IS NULL OR (i.resolved_at IS NULL) = sqlc.narg(open)::bool)
   AND (sqlc.narg(before)::uuid IS NULL OR i.id < sqlc.narg(before)::uuid)
   AND (sqlc.narg(target_id)::uuid IS NULL OR i.target_id = sqlc.narg(target_id)::uuid)
 ORDER BY i.id DESC
 LIMIT sqlc.arg(max_rows)::int;

-- Reconhecer é idempotente: o COALESCE preserva o primeiro ack.
-- name: AckIncident :one
UPDATE incidents i
   SET ack_at = COALESCE(i.ack_at, sqlc.arg(now)::timestamptz)
  FROM targets t
 WHERE t.id = i.target_id AND t.user_id = sqlc.arg(user_id) AND i.id = sqlc.arg(id)
RETURNING i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at;
