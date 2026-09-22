-- +goose Up
-- A coluna totp_secret_enc já nasceu na 00001. O que falta é o marco de
-- confirmação: o segredo só vale depois de confirmado, porque cadastrar sem
-- confirmar não pode trancar ninguém para fora da própria conta.
ALTER TABLE users ADD COLUMN totp_confirmed_at timestamptz;

-- Códigos de recuperação são de uso único: a linha é apagada ao ser usada.
-- Guardar o hash, e não o código, mantém a coluna inútil em caso de vazamento.
CREATE TABLE totp_recovery_codes (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX totp_recovery_user_idx ON totp_recovery_codes (user_id);
CREATE UNIQUE INDEX totp_recovery_hash_idx ON totp_recovery_codes (code_hash);

-- +goose Down
DROP TABLE totp_recovery_codes;
ALTER TABLE users DROP COLUMN totp_confirmed_at;
