-- +goose Up
-- Baldes do limite por IP (login e /totp/verify) no banco, para que réplicas
-- da API dividam o mesmo orçamento por origem (#328). Antes, cada processo
-- tinha os seus na memória.
--
-- key_hash é o HMAC da origem com IP_HASH_KEY, como o ip_hash de sessions: o
-- IP não é gravado. tokens são as fichas que sobraram em updated_at, o último
-- gasto permitido; a recarga é calculada na hora (ip_rate_limit_refill), sem
-- job. last_allowed é o resultado do último pedido, que o RETURNING precisa:
-- as fichas sozinhas não distinguem "permitido e sobrou 0,5" de "recusado com
-- 0,5".
--
-- Sem dono e lida antes do login: a RLS só abre a tabela ao escopo
-- app.scope = 'ip_rate_limit' (storage.InIPRateLimitTx), que não enxerga mais
-- nada. FOR ALL de propósito: o gasto é um INSERT ... ON CONFLICT DO UPDATE com
-- RETURNING (INSERT, UPDATE e SELECT) e apaga as linhas paradas (DELETE).
CREATE TABLE ip_rate_limits (
    bucket       text             NOT NULL CHECK (bucket <> '' AND length(bucket) <= 64),
    key_hash     bytea            NOT NULL CHECK (length(key_hash) = 32),
    tokens       double precision NOT NULL CHECK (tokens >= 0),
    last_allowed boolean          NOT NULL,
    updated_at   timestamptz      NOT NULL,
    PRIMARY KEY (bucket, key_hash)
);

-- A limpeza oportunista procura as linhas paradas do mesmo balde.
CREATE INDEX ip_rate_limits_bucket_updated_at ON ip_rate_limits (bucket, updated_at);

-- Fichas em p_now: as de p_since mais uma por p_seconds_per_token passado,
-- até p_burst. Tempo negativo (réplica com relógio atrasado) não devolve nada.
-- +goose StatementBegin
CREATE FUNCTION ip_rate_limit_refill(p_tokens double precision, p_since timestamptz, p_now timestamptz,
                                     p_burst double precision, p_seconds_per_token double precision)
RETURNS double precision
LANGUAGE sql IMMUTABLE SET search_path = pg_catalog, pg_temp AS $$
    SELECT LEAST(p_burst, p_tokens + GREATEST(0, EXTRACT(EPOCH FROM (p_now - p_since))::double precision)
                                     / p_seconds_per_token)
$$;
-- +goose StatementEnd

ALTER TABLE ip_rate_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE ip_rate_limits FORCE ROW LEVEL SECURITY;

CREATE POLICY ip_rate_limits_scope ON ip_rate_limits
    USING (current_setting('app.scope', true) = 'ip_rate_limit')
    WITH CHECK (current_setting('app.scope', true) = 'ip_rate_limit');

-- +goose Down
DROP TABLE ip_rate_limits;
DROP FUNCTION ip_rate_limit_refill(double precision, timestamptz, timestamptz, double precision, double precision);
