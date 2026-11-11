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

-- A marca d'água: a hora mais recente já consolidada ANTES de before (a hora
-- corrente). Um rollup gravado no futuro (relógio adiantado) não conta: com
-- ele, a marca real ficava escondida, e uma parada de mais de 2 h perdia as
-- horas antes dela. Sem rollup nenhum antes de
-- before, devolve a época Unix.
-- name: LatestRollupBucketBefore :one
SELECT coalesce(max(bucket), 'epoch'::timestamptz)::timestamptz FROM check_rollup_1h
 WHERE bucket < sqlc.arg(before)::timestamptz;

-- Uma consolidação por vez entre todas as instâncias: o upsert é idempotente, mas dois em paralelo travam as mesmas
-- linhas em ordens diferentes (deadlock 40P01). A trava é de transação e
-- não espera: quem não a pega pula a rodada, e a próxima tenta de novo.
-- name: TryLockRollups :one
SELECT pg_try_advisory_xact_lock(hashtextextended(sqlc.arg(key)::text, 0)) AS acquired;

-- Vale só até o fim da transação (o terceiro argumento de set_config).
-- name: SetLocalLockTimeout :exec
SELECT set_config('lock_timeout', sqlc.arg(timeout)::text, true);

-- A manutenção passa do statement_timeout do pool (storage.NewPool): a
-- primeira consolidação e a primeira retenção varrem semanas de linhas. Vale
-- só até o fim da transação.
-- name: SetLocalStatementTimeout :exec
SELECT set_config('statement_timeout', sqlc.arg(timeout)::text, true);

-- name: DropChecksPartitionsBefore :one
SELECT drop_checks_partitions_before(sqlc.arg(cutoff)::timestamptz)::int;

-- name: DeleteOldRollups :execrows
DELETE FROM check_rollup_1h WHERE bucket < sqlc.arg(cutoff)::timestamptz;

-- Entregues, falhas e canceladas; pendente nunca sai por idade. Fica também a
-- linha mais nova de cada par (incidente, canal) que ainda pode receber lembrete:
-- ela é a base e o último seq de ListReminderCandidates, e um canal ou alvo
-- pausado não gera linha nova por mais de 30 dias. O filtro não olha a pausa de
-- propósito: a retenção pode rodar entre a retomada e o tique seguinte. Uma
-- linha por par de incidente aberto é o teto do que sobra; ao fechar, ela sai.
-- name: DeleteOldNotifications :execrows
DELETE FROM notifications n
 WHERE n.status <> 'pending' AND n.created_at < sqlc.arg(cutoff)::timestamptz
   AND NOT EXISTS (
        SELECT 1 FROM incidents i
         WHERE i.id = n.incident_id
           AND i.resolved_at IS NULL AND i.ack_at IS NULL AND i.open_notified_at IS NOT NULL
           AND NOT EXISTS (
                SELECT 1 FROM notifications mais_nova
                 WHERE mais_nova.incident_id = n.incident_id
                   AND mais_nova.channel_id = n.channel_id
                   AND mais_nova.created_at > n.created_at));

-- A idade do log de sinais (spec de heartbeats). O teto por heartbeat
-- fica na transação do ping; esta cobre quem parou de pingar.
-- name: DeleteOldHeartbeatEvents :execrows
DELETE FROM heartbeat_events WHERE at < sqlc.arg(cutoff)::timestamptz;

-- audit_log e sessions não têm RLS.
-- name: DeleteOldAudit :execrows
DELETE FROM audit_log WHERE at < sqlc.arg(cutoff)::timestamptz;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < sqlc.arg(cutoff)::timestamptz;

-- GET /targets/{id}/checks acima de 48h lê daqui. Roda sob
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
