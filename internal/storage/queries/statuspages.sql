-- Status page (spec §10): as do dono rodam sob InUserTx e filtram por
-- user_id; as públicas, sob InPublicTx (somente leitura, escopo do slug).

-- name: GetStatusPage :one
SELECT * FROM status_pages WHERE user_id = $1;

-- A trava do PATCH: sem ela, dois PATCH simultâneos leem a mesma versão, e
-- o segundo regrava os campos que o primeiro mudou (achado da revisão).
-- name: LockStatusPage :one
SELECT id FROM status_pages WHERE user_id = $1 FOR UPDATE;

-- name: InsertStatusPage :exec
INSERT INTO status_pages (id, user_id, slug, title, published, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateStatusPage :execrows
UPDATE status_pages SET slug = $3, title = $4, published = $5
 WHERE user_id = $1 AND id = $2;

-- Os itens são trocados inteiros a cada escrita: apagar e reinserir, na
-- mesma transação.
-- name: DeleteStatusPageItems :exec
DELETE FROM status_page_items i
 USING status_pages p
 WHERE p.id = i.status_page_id AND p.user_id = sqlc.arg(user_id) AND p.id = sqlc.arg(page_id);

-- name: InsertStatusPageItem :exec
INSERT INTO status_page_items (status_page_id, target_id, display_name, position)
VALUES ($1, $2, $3, $4);

-- name: ListStatusPageItems :many
SELECT i.target_id, i.display_name, i.position
  FROM status_page_items i
  JOIN status_pages p ON p.id = i.status_page_id
 WHERE p.user_id = sqlc.arg(user_id) AND p.id = sqlc.arg(page_id)
 ORDER BY i.position;

-- Os alvos do dono entre os ids pedidos: a validação dos itens diz qual id
-- não é dele.
-- name: OwnTargetIDs :many
SELECT id FROM targets WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::uuid[]);

-- Públicas. Nenhuma devolve URL, headers, user_id nem status code: a
-- projeção só tem o que a spec §5.6 permite expor.

-- name: GetPublicPage :one
SELECT id, title FROM status_pages WHERE slug = sqlc.arg(slug) AND published;

-- O estado atual é o do check mais recente. interval_sec e enabled servem só
-- para decidir se esse check ainda é recente; não saem na página. Alvo sem
-- check nenhum volta com last_status '' e last_checked_at na época Unix.
-- name: ListPublicItems :many
SELECT i.target_id, i.display_name, t.enabled, t.interval_sec,
       coalesce(lc.status, '')::text AS last_status,
       coalesce(lc.checked_at, 'epoch'::timestamptz)::timestamptz AS last_checked_at
  FROM status_page_items i
  JOIN targets t ON t.id = i.target_id
  LEFT JOIN LATERAL (
       SELECT c.status, c.checked_at FROM checks c
        WHERE c.target_id = i.target_id
        ORDER BY c.checked_at DESC LIMIT 1) lc ON true
 WHERE i.status_page_id = sqlc.arg(page_id)
 ORDER BY i.position;

-- A marca d'água da página: a hora mais recente já consolidada entre os alvos
-- dela, ANTES de before (a hora corrente), como LatestUserRollupBucket. Sem
-- rollup, a época Unix.
-- name: LatestPageRollupBucket :one
SELECT coalesce(max(r.bucket), 'epoch'::timestamptz)::timestamptz
  FROM status_page_items i
  CROSS JOIN LATERAL (
       SELECT bucket FROM check_rollup_1h
        WHERE target_id = i.target_id AND bucket < sqlc.arg(before)::timestamptz
        ORDER BY bucket DESC LIMIT 1) r
 WHERE i.status_page_id = sqlc.arg(page_id);

-- Uptime diário (UTC) dos alvos da página: os rollups de [since, cut) mais
-- os checks brutos de [cut, until), no corte do pacote rollupcut (o mesmo do
-- dashboard). Sem os brutos, a página ficava até ~1h15 atrasada e cega à
-- queda em curso (C4 da revisão).
-- name: ListPublicDailyUptime :many
SELECT u.target_id,
       to_timestamp(floor(extract(epoch FROM u.at) / 86400) * 86400)::timestamptz AS day,
       sum(u.total)::bigint AS total, sum(u.ok)::bigint AS ok
  FROM (SELECT r.target_id, r.bucket AS at, r.total, r.ok
          FROM check_rollup_1h r
          JOIN status_page_items i ON i.target_id = r.target_id AND i.status_page_id = sqlc.arg(page_id)
         WHERE r.bucket >= sqlc.arg(since)::timestamptz AND r.bucket < sqlc.arg(cut)::timestamptz
        UNION ALL
        SELECT c.target_id, c.checked_at, 1, (c.status <> 'down')::int
          FROM checks c
          JOIN status_page_items i ON i.target_id = c.target_id AND i.status_page_id = sqlc.arg(page_id)
         WHERE c.checked_at >= sqlc.arg(cut)::timestamptz AND c.checked_at < sqlc.arg(until)::timestamptz) u
 GROUP BY 1, 2
 ORDER BY 1, 2;

-- Latência mediana aproximada: média das medianas horárias, ponderada pelos
-- sucessos de cada hora. As horas de [cut, until) saem dos checks brutos, com
-- a mediana de ConsolidateRollups; as anteriores, dos rollups.
-- name: ListPublicLatency :many
SELECT h.target_id, (sum(h.p50::bigint * h.ok) / nullif(sum(h.ok), 0))::int AS p50
  FROM (SELECT r.target_id, r.p50, r.ok
          FROM check_rollup_1h r
          JOIN status_page_items i ON i.target_id = r.target_id AND i.status_page_id = sqlc.arg(page_id)
         WHERE r.bucket >= sqlc.arg(since)::timestamptz AND r.bucket < sqlc.arg(cut)::timestamptz
        UNION ALL
        SELECT c.target_id,
               percentile_disc(0.50) WITHIN GROUP (ORDER BY c.latency_ms) FILTER (WHERE c.status <> 'down'),
               (count(*) FILTER (WHERE c.status <> 'down'))::int
          FROM checks c
          JOIN status_page_items i ON i.target_id = c.target_id AND i.status_page_id = sqlc.arg(page_id)
         WHERE c.checked_at >= greatest(sqlc.arg(cut)::timestamptz, sqlc.arg(since)::timestamptz)
           AND c.checked_at < sqlc.arg(until)::timestamptz
         GROUP BY c.target_id, floor(extract(epoch FROM c.checked_at) / 3600)) h
 WHERE h.ok > 0
 GROUP BY 1;

-- Incidentes resolvidos dos alvos da página que tocam a janela [since,
-- agora): resolvidos depois de since, mesmo que tenham começado antes. Filtrar
-- pelo início deixava sem explicação o dia vermelho da queda que atravessa o
-- começo da barra (C9). Só o nome de exibição, o início e o fim; a causa não
-- sai (spec §5.6).
-- name: ListPublicIncidents :many
SELECT i.display_name, x.started_at, x.resolved_at::timestamptz AS resolved_at
  FROM incidents x
  JOIN status_page_items i ON i.target_id = x.target_id AND i.status_page_id = sqlc.arg(page_id)
 WHERE x.resolved_at IS NOT NULL AND x.resolved_at >= sqlc.arg(since)::timestamptz
 ORDER BY x.started_at DESC
 LIMIT sqlc.arg(max_rows)::int;
