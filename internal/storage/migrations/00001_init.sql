-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

-- IDs são UUIDv7 gerados na aplicação (spec §4.2): o id existe antes do
-- INSERT e não depende da versão do servidor (uuidv7() nativo só existe a
-- partir do Postgres 18; a produção roda o 18, mas o CI ainda testa o 17).
CREATE TABLE users (
    id              uuid PRIMARY KEY,
    email           citext NOT NULL UNIQUE,
    password_hash   text NOT NULL,
    totp_secret_enc bytea,
    failed_attempts int NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    used_at    timestamptz,
    ip_hash    bytea,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Índice parcial: a revogação de família só percorre sessões ainda vivas.
CREATE INDEX sessions_family_alive_idx ON sessions (family_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE audit_log (
    id        uuid PRIMARY KEY,
    user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    action    text NOT NULL,
    entity    text,
    entity_id uuid,
    ip_hash   bytea,
    at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_user_at_idx ON audit_log (user_id, at DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
