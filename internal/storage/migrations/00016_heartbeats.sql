-- +goose Up
-- Heartbeats: tarefas que avisam por ping
-- (docs/superpowers/specs/2026-11-05-heartbeats-design.md). A 00015 fica vaga
-- de propósito: storage.Migrate recusa uma versão menor que a do banco.
--
-- Tabela própria, e não uma linha em targets: o binário anterior a esta
-- migration lê targets sem saber de heartbeat, e colunas HTTP nulas ali
-- paravam o sondador inteiro num rollback (spec §3). Aqui ele só não enxerga
-- as linhas (TestOlderBinaryIgnoresHeartbeatRows).
CREATE TABLE heartbeats (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    -- O token de ping só existe como hash: sai uma vez na criação ou na
    -- rotação, e trocá-lo é o que o revoga.
    ping_token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(ping_token_hash) = 32),
    period_sec       int CHECK (period_sec BETWEEN 60 AND 2678400),
    cron_expr        text CHECK (char_length(cron_expr) BETWEEN 1 AND 100),
    timezone         text CHECK (char_length(timezone) BETWEEN 1 AND 64),
    grace_sec        int NOT NULL CHECK (grace_sec BETWEEN 30 AND 604800),
    enabled          bool NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL,
    -- Quando o silêncio vira atraso. Nasce preenchido: tarefa que nunca
    -- rodou também avisa.
    deadline_at      timestamptz NOT NULL,
    last_ping_at     timestamptz,
    last_success_at  timestamptz,
    -- O início ainda sem sucesso: é dele que sai o tempo de execução.
    last_start_at    timestamptz,
    last_signal      text CHECK (last_signal IN ('start', 'success', 'fail')),
    -- Falha avisada e ainda sem sucesso depois dela.
    failing_since    timestamptz,
    -- Base do intervalo mínimo entre aberturas (D11 da spec).
    last_incident_at timestamptz,
    -- Exatamente um agendamento: período, ou cron com fuso explícito.
    CONSTRAINT heartbeats_one_schedule CHECK (
        (period_sec IS NOT NULL AND cron_expr IS NULL AND timezone IS NULL)
        OR (period_sec IS NULL AND cron_expr IS NOT NULL AND timezone IS NOT NULL))
);

CREATE INDEX heartbeats_user_idx ON heartbeats (user_id, id DESC);
-- Índice parcial: o avaliador só procura heartbeats ativos e vencidos.
CREATE INDEX heartbeats_due_idx ON heartbeats (deadline_at) WHERE enabled;

ALTER TABLE heartbeats ENABLE ROW LEVEL SECURITY;
ALTER TABLE heartbeats FORCE ROW LEVEL SECURITY;

CREATE POLICY heartbeats_owner ON heartbeats
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O avaliador lê e atualiza, e nada mais, como em targets.
CREATE POLICY heartbeats_prober_select ON heartbeats FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY heartbeats_prober_update ON heartbeats FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober');

-- Escopo do ping público (storage.InPingTx): a transação enxerga exatamente
-- a linha daquele hash. decode(NULL) é NULL, então sem o GUC, ou com ele
-- vazio numa conexão reusada, a política não casa nada.
CREATE POLICY heartbeats_ping_select ON heartbeats FOR SELECT
    USING (ping_token_hash = decode(NULLIF(current_setting('app.ping_token_hash', true), ''), 'hex'));
CREATE POLICY heartbeats_ping_update ON heartbeats FOR UPDATE
    USING (ping_token_hash = decode(NULLIF(current_setting('app.ping_token_hash', true), ''), 'hex'))
    WITH CHECK (ping_token_hash = decode(NULLIF(current_setting('app.ping_token_hash', true), ''), 'hex'));

-- O log de sinais. O id é UUIDv7: ordenar por ele é ordenar por chegada.
CREATE TABLE heartbeat_events (
    id           uuid PRIMARY KEY,
    heartbeat_id uuid NOT NULL REFERENCES heartbeats(id) ON DELETE CASCADE,
    at           timestamptz NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('start', 'success', 'fail')),
    runtime_ms   bigint CHECK (runtime_ms >= 0)
);

CREATE INDEX heartbeat_events_heartbeat_idx ON heartbeat_events (heartbeat_id, id DESC);

-- Sem user_id: a RLS delega ao heartbeat, como checks a targets. Quem não o
-- enxerga, em qualquer escopo, não enxerga nem cria eventos dele.
ALTER TABLE heartbeat_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE heartbeat_events FORCE ROW LEVEL SECURITY;
CREATE POLICY heartbeat_events_via_heartbeat ON heartbeat_events
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_events.heartbeat_id))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_events.heartbeat_id));

-- Arco exclusivo: duas FKs reais, e não um *_type. O incidente é de um alvo
-- ou de um heartbeat, nunca dos dois.
ALTER TABLE incidents ALTER COLUMN target_id DROP NOT NULL;
ALTER TABLE incidents ADD COLUMN heartbeat_id uuid REFERENCES heartbeats(id) ON DELETE CASCADE;
ALTER TABLE incidents ADD CONSTRAINT incidents_one_subject
    CHECK (num_nonnulls(target_id, heartbeat_id) = 1);

ALTER TABLE incidents DROP CONSTRAINT incidents_cause_check;
ALTER TABLE incidents ADD CONSTRAINT incidents_cause_check
    CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                     'blocked', 'unknown', 'heartbeat_late', 'heartbeat_failed'));

-- O par de incidents_one_open_per_target. Também serve à FK: o ON DELETE
-- CASCADE de apagar um heartbeat varreria incidents sem índice.
CREATE UNIQUE INDEX incidents_one_open_per_heartbeat ON incidents (heartbeat_id) WHERE resolved_at IS NULL;
CREATE INDEX incidents_heartbeat_idx ON incidents (heartbeat_id, id DESC) WHERE heartbeat_id IS NOT NULL;

-- Soma com incidents_via_target por OR, sem alterá-la. Nomeia o dono e o
-- sondador, como notifications_via_channel: "o heartbeat existe" não basta,
-- porque ele também é visível no escopo do ping, e o token de uma tarefa não
-- pode alcançar incidentes.
CREATE POLICY incidents_via_heartbeat ON incidents
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

-- +goose Down
-- Roda com o papel da aplicação, sujeito à RLS: sem o escopo do sondador, o
-- DELETE não enxergaria linha nenhuma, e o NOT NULL abaixo falharia.
SELECT set_config('app.scope', 'prober', true);
DELETE FROM incidents WHERE heartbeat_id IS NOT NULL;
DROP POLICY incidents_via_heartbeat ON incidents;
DROP INDEX incidents_heartbeat_idx;
DROP INDEX incidents_one_open_per_heartbeat;
ALTER TABLE incidents DROP CONSTRAINT incidents_cause_check;
ALTER TABLE incidents ADD CONSTRAINT incidents_cause_check
    CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                     'blocked', 'unknown'));
ALTER TABLE incidents DROP CONSTRAINT incidents_one_subject;
ALTER TABLE incidents DROP COLUMN heartbeat_id;
ALTER TABLE incidents ALTER COLUMN target_id SET NOT NULL;
DROP TABLE heartbeat_events;
DROP TABLE heartbeats;
