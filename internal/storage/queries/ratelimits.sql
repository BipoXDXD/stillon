-- Limite por IP compartilhado entre réplicas (migration 00036). Roda no
-- escopo storage.InIPRateLimitTx. A decisão é uma instrução só: nada de ler e
-- depois gravar, e N pedidos simultâneos da mesma origem gastam no máximo a
-- rajada.

-- Gasta uma ficha do balde da chave. Linha nova nasce com a rajada menos a
-- ficha gasta. Na existente, recarrega até now e gasta se houver uma ficha
-- inteira. Sem ela, a linha fica como estava: recusar não consome a ficha de
-- quem respeita o Retry-After, e não grava uma recarga parcial cujo
-- arredondamento deixaria a ficha seguinte um fio abaixo de 1. updated_at
-- nunca anda para trás: uma réplica atrasada não ganha fichas nem as tira das
-- outras. available são as fichas em now, de onde sai o Retry-After.
--
-- O CTE apaga até 20 linhas paradas do MESMO balde (paradas além de
-- idle_before equivalem a um balde cheio), sem esperar quem as trava, e nunca a
-- da própria chave. É a limpeza: cada gasto paga um pouco, sem job.
-- name: SpendIPRateLimitQuota :one
WITH idle AS (
    DELETE FROM ip_rate_limits d
     WHERE (d.bucket, d.key_hash) IN (
        SELECT o.bucket, o.key_hash FROM ip_rate_limits o
         WHERE o.bucket = sqlc.arg(bucket)::text
           AND o.updated_at < sqlc.arg(idle_before)::timestamptz
           AND o.key_hash <> sqlc.arg(key_hash)::bytea
         LIMIT 20
           FOR UPDATE SKIP LOCKED
     )
)
INSERT INTO ip_rate_limits AS l (bucket, key_hash, tokens, last_allowed, updated_at)
VALUES (sqlc.arg(bucket)::text, sqlc.arg(key_hash)::bytea, sqlc.arg(burst)::float8 - 1, true, sqlc.arg(now)::timestamptz)
ON CONFLICT (bucket, key_hash) DO UPDATE
   SET tokens = CASE WHEN ip_rate_limit_refill(l.tokens, l.updated_at, excluded.updated_at, sqlc.arg(burst)::float8, sqlc.arg(seconds_per_token)::float8) >= 1
                     THEN ip_rate_limit_refill(l.tokens, l.updated_at, excluded.updated_at, sqlc.arg(burst)::float8, sqlc.arg(seconds_per_token)::float8) - 1
                     ELSE l.tokens END,
       updated_at = CASE WHEN ip_rate_limit_refill(l.tokens, l.updated_at, excluded.updated_at, sqlc.arg(burst)::float8, sqlc.arg(seconds_per_token)::float8) >= 1
                         THEN GREATEST(l.updated_at, excluded.updated_at)
                         ELSE l.updated_at END,
       last_allowed = ip_rate_limit_refill(l.tokens, l.updated_at, excluded.updated_at, sqlc.arg(burst)::float8, sqlc.arg(seconds_per_token)::float8) >= 1
RETURNING last_allowed,
          ip_rate_limit_refill(tokens, updated_at, sqlc.arg(now)::timestamptz, sqlc.arg(burst)::float8, sqlc.arg(seconds_per_token)::float8)::float8 AS available;
