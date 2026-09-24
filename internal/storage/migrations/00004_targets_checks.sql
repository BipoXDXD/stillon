-- +goose Up
-- Alvos de monitoramento (spec §4.1). Cada CHECK tem par na validação de
-- internal/targets: a validação existe para devolver o campo culpado, e o
-- CHECK para que nenhum caminho que esqueça de validar grave lixo.
CREATE TABLE targets (
    id                    uuid PRIMARY KEY,
    user_id               uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                  text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    url                   text NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2048),
    method                text NOT NULL CHECK (method IN ('GET', 'HEAD', 'POST')),
    interval_sec          int  NOT NULL CHECK (interval_sec BETWEEN 30 AND 86400),
    timeout_ms            int  NOT NULL CHECK (timeout_ms BETWEEN 1000 AND 30000),
    expected_status       int  NOT NULL CHECK (expected_status BETWEEN 100 AND 599),
    headers_enc           bytea,
    degraded_threshold_ms int  CHECK (degraded_threshold_ms BETWEEN 1 AND 60000),
    enabled               bool NOT NULL DEFAULT true,
    next_run_at           timestamptz NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    -- O jitter de -10% pode antecipar a próxima execução para 90% do
    -- intervalo; um timeout acima disso deixaria duas sondas do mesmo alvo
    -- em voo ao mesmo tempo (D3).
    CHECK (timeout_ms * 10 < interval_sec * 9000)
);

CREATE INDEX targets_user_idx ON targets (user_id, id DESC);
-- Índice parcial: a reivindicação só procura alvos ativos e vencidos.
CREATE INDEX targets_due_idx ON targets (next_run_at) WHERE enabled;

-- RLS (spec §5.5). FORCE porque a aplicação conecta como DONA da tabela, e o
-- dono ignora RLS sem ele. Superusuário ignora mesmo com FORCE — por isso o
-- boot recusa esse papel (storage.CheckRole).
--
-- NULLIF é obrigatório. Depois do primeiro SET LOCAL de uma sessão,
-- current_setting('app.user_id', true) passa a devolver '' — e não NULL —
-- naquela sessão. ''::uuid explode com "invalid input syntax for type uuid",
-- e num pool, que reusa conexões, isso derrubaria a primeira consulta sem
-- escopo depois de qualquer transação de usuário. Um teste com conexão nova
-- nunca vê isso; TestScopeSurvivesConnectionReuse vê.
ALTER TABLE targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE targets FORCE ROW LEVEL SECURITY;

CREATE POLICY targets_owner ON targets
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O sondador lê e reagenda alvos de todos os usuários, e nada mais: sem
-- política de INSERT nem de DELETE para ele. São duas políticas, e não uma
-- FOR ALL, porque o WITH CHECK de dono barraria o UPDATE do sondador.
CREATE POLICY targets_prober_select ON targets FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY targets_prober_update ON targets FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober');

-- Resultados de sonda, particionados por mês (spec §4.2): retenção por
-- DROP da partição, em vez de DELETE com bloat.
CREATE TABLE checks (
    id          uuid NOT NULL,
    target_id   uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    checked_at  timestamptz NOT NULL,
    status      text NOT NULL CHECK (status IN ('up', 'degraded', 'down')),
    latency_ms  int  NOT NULL CHECK (latency_ms >= 0),
    status_code int,
    -- 'unknown' é desvio registrado da spec §4.2 (D1).
    error_kind  text CHECK (error_kind IN ('timeout', 'dns', 'tls', 'conn_refused',
                                           'http_status', 'body_limit', 'blocked', 'unknown')),
    -- A chave de partição precisa entrar na PK.
    PRIMARY KEY (id, checked_at),
    -- Toda falha tem motivo, e só falha tem motivo.
    CHECK ((status = 'down') = (error_kind IS NOT NULL))
) PARTITION BY RANGE (checked_at);

CREATE INDEX checks_target_at_idx ON checks (target_id, checked_at DESC);

-- checks não tem user_id: a RLS delega ao alvo. O EXISTS roda sob as
-- políticas de targets, então quem não enxerga o alvo não enxerga os
-- checks dele. Vale para leitura E para escrita: um check de alvo
-- inexistente falha aqui (42501), antes da FK (23503).
--
-- A política vale para consultas pela tabela-mãe. Partições não herdam RLS;
-- a aplicação nunca consulta uma partição diretamente.
ALTER TABLE checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE checks FORCE ROW LEVEL SECURITY;

CREATE POLICY checks_via_target ON checks
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id));

-- Cria a partição do mês que contém p_month. As bordas são instantes em UTC,
-- independentes do TimeZone da sessão: se dois boots discordassem sobre onde
-- o mês começa, o segundo tentaria criar uma faixa sobreposta e cairia.
-- +goose StatementBegin
CREATE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION ensure_checks_partition(date);
DROP TABLE checks;
DROP TABLE targets;
