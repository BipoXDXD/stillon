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

-- FOR UPDATE, e não FOR NO KEY UPDATE: a gravação do sondador
-- (LockTargetProbeSpec, FOR KEY SHARE) precisa esperar o PATCH inteiro para
-- ver a URL nova (E2 da revisão de 2026-10-14).
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

-- C4 (Plano 10): trocar a URL ou o método zera a histerese (a linha volta a
-- "sem dados"). last_checked_at fica: é a guarda de check fora de ordem do
-- detector. O EXISTS filtra pelo dono, como o resto deste arquivo, além da
-- RLS de target_health.
-- name: ResetTargetHealth :exec
UPDATE target_health
   SET consecutive_failures = 0, consecutive_successes = 0, streak_since = NULL
 WHERE target_id = sqlc.arg(target_id)
   AND EXISTS (SELECT 1 FROM targets t
                WHERE t.id = target_health.target_id AND t.user_id = sqlc.arg(user_id));

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

-- GET /targets/summary (Plano 5). Todas rodam sob InUserTx E filtram por
-- user_id, como as de cima.

-- Os alvos do dono na ordem de GET /targets, com o último check e o incidente
-- aberto (o índice único parcial garante no máximo um). Alvo sem check volta
-- com last_status '' e last_checked_at na época Unix, como ListPublicItems: o
-- sqlc não enxerga a nulidade de um LEFT JOIN LATERAL.
-- name: ListTargetSummaries :many
SELECT t.id, t.name, t.enabled, t.interval_sec,
       coalesce(lc.status, '')::text AS last_status,
       coalesce(lc.checked_at, 'epoch'::timestamptz)::timestamptz AS last_checked_at,
       coalesce(lc.latency_ms, 0)::int AS last_latency_ms,
       x.id AS open_incident_id
  FROM targets t
  LEFT JOIN LATERAL (
       SELECT c.status, c.checked_at, c.latency_ms FROM checks c
        WHERE c.target_id = t.id
        ORDER BY c.checked_at DESC LIMIT 1) lc ON true
  LEFT JOIN incidents x ON x.target_id = t.id AND x.resolved_at IS NULL
 WHERE t.user_id = sqlc.arg(user_id)
 ORDER BY t.id DESC;

-- A marca d'água DO DONO: a hora mais recente já consolidada entre os alvos
-- dele. max(bucket) da tabela inteira não serve: sob a RLS, um dono sem
-- rollups recentes faria o Postgres varrer o índice de bucket da instância
-- inteira, testando a política linha a linha. Só conta rollup ANTES de before
-- (a hora corrente): um gravado no futuro por relógio adiantado esconderia as
-- horas ainda não consolidadas (C8, a mesma guarda de
-- LatestRollupBucketBefore). Sem rollup, a época Unix.
-- name: LatestUserRollupBucket :one
SELECT coalesce(max(r.bucket), 'epoch'::timestamptz)::timestamptz
  FROM targets t
  CROSS JOIN LATERAL (
       SELECT bucket FROM check_rollup_1h
        WHERE target_id = t.id AND bucket < sqlc.arg(before)::timestamptz
        ORDER BY bucket DESC LIMIT 1) r
 WHERE t.user_id = sqlc.arg(user_id);

-- name: ListSummaryRollups :many
SELECT r.target_id, r.bucket, r.total, r.ok, r.p50
  FROM check_rollup_1h r
  JOIN targets t ON t.id = r.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND r.bucket >= sqlc.arg(from_at)::timestamptz
   AND r.bucket < sqlc.arg(to_at)::timestamptz;

-- As horas cheias ainda não consolidadas, com a MESMA fórmula de
-- ConsolidateRollups. Serve o resumo (todos os alvos do dono, target_id nulo)
-- e a série longa de um alvo (GET /targets/{id}/checks acima de 48 h). As
-- latências saem 0 numa hora sem sucesso (o sqlc não enxerga a nulidade do
-- percentil); quem lê usa ok = 0 para saber que não há latência, a mesma
-- regra do CHECK de check_rollup_1h.
-- name: ListRawHours :many
SELECT c.target_id,
       to_timestamp(floor(extract(epoch FROM c.checked_at) / 3600) * 3600)::timestamptz AS bucket,
       count(*)::int AS total,
       (count(*) FILTER (WHERE c.status <> 'down'))::int AS ok,
       coalesce(percentile_disc(0.50) WITHIN GROUP (ORDER BY c.latency_ms)
                FILTER (WHERE c.status <> 'down'), 0)::int AS p50,
       coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY c.latency_ms)
                FILTER (WHERE c.status <> 'down'), 0)::int AS p95,
       coalesce(percentile_disc(0.99) WITHIN GROUP (ORDER BY c.latency_ms)
                FILTER (WHERE c.status <> 'down'), 0)::int AS p99,
       coalesce(max(c.latency_ms) FILTER (WHERE c.status <> 'down'), 0)::int AS max
  FROM checks c
  JOIN targets t ON t.id = c.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND (sqlc.narg(target_id)::uuid IS NULL OR c.target_id = sqlc.narg(target_id)::uuid)
   AND c.checked_at >= sqlc.arg(from_at)::timestamptz
   AND c.checked_at < sqlc.arg(to_at)::timestamptz
 GROUP BY 1, 2
 ORDER BY 1, 2;

-- A hora corrente entra só no uptime, não no spark: só contagens, sem
-- percentil.
-- name: ListSummaryCurrentHour :many
SELECT c.target_id, count(*)::int AS total, (count(*) FILTER (WHERE c.status <> 'down'))::int AS ok
  FROM checks c
  JOIN targets t ON t.id = c.target_id
 WHERE t.user_id = sqlc.arg(user_id)
   AND c.checked_at >= sqlc.arg(from_at)::timestamptz
   AND c.checked_at < sqlc.arg(to_at)::timestamptz
 GROUP BY 1;

-- O teto de alvos por usuário (Plano 5): contar e inserir sob esta trava.
-- Sem ela, dois POST simultâneos passariam os dois pela contagem.
-- name: LockUserTargets :exec
SELECT pg_advisory_xact_lock(hashtextextended('stillon.targets:' || sqlc.arg(user_id)::uuid::text, 0));

-- name: CountTargets :one
SELECT count(*) FROM targets WHERE user_id = $1;
