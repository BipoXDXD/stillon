-- +goose Up
-- Cadastro público (PR 3): os tokens de confirmação de e-mail e de
-- redefinição de senha, e o teto diário de e-mails de conta. Só expande: o
-- binário anterior ignora as duas tabelas.
SET LOCAL lock_timeout = '5s';

-- token_hash é o SHA-256 do token de 256 bits que vai no link; o token não é
-- gravado. Uso único (used_at) e prazo próprio por propósito (24 h a
-- confirmação, 1 h a redefinição), escolhido por quem grava.
CREATE TABLE account_tokens (
    token_hash bytea       PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('confirm_email', 'reset_password')),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    used_at    timestamptz,
    emissao    bigint      GENERATED ALWAYS AS IDENTITY
);

-- O primeiro link nasce na transação que cria a conta. A trava do dono
-- ordena as emissões seguintes. O aceite de um envio só pode substituir
-- emissões anteriores do mesmo propósito.
CREATE INDEX account_tokens_user_purpose ON account_tokens (user_id, purpose);

ALTER TABLE account_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_tokens FORCE ROW LEVEL SECURITY;

-- Dois escopos. O do token (storage.InAccountTokenTx, app.account_token_hash)
-- enxerga e consome só a linha do próprio hash: quem clica no link ainda não
-- tem sessão. O do dono (app.user_id) grava e apaga os tokens dele. O SELECT
-- tem os dois ramos porque o DELETE ... WHERE user_id = ... também passa pela
-- política de SELECT para ler as linhas que apaga.
CREATE POLICY account_tokens_select ON account_tokens FOR SELECT
    USING (token_hash = decode(NULLIF(current_setting('app.account_token_hash', true), ''), 'hex')
           OR user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- Consumir é só do escopo do hash: o dono não marca o próprio token como usado.
CREATE POLICY account_tokens_update ON account_tokens FOR UPDATE
    USING (token_hash = decode(NULLIF(current_setting('app.account_token_hash', true), ''), 'hex'))
    WITH CHECK (token_hash = decode(NULLIF(current_setting('app.account_token_hash', true), ''), 'hex'));

CREATE POLICY account_tokens_insert ON account_tokens FOR INSERT
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

CREATE POLICY account_tokens_delete ON account_tokens FOR DELETE
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- Quantos e-mails de conta saíram em cada dia UTC, entre todas as réplicas: o
-- teto protege a cota da Resend. A reserva apaga as linhas com mais de 7 dias.
CREATE TABLE account_email_daily (
    day  date    PRIMARY KEY,
    sent integer NOT NULL CHECK (sent >= 0)
);

-- Sem dono: a RLS só abre a tabela ao escopo app.scope = 'account_email'
-- (storage.InAccountEmailTx). FOR ALL porque a reserva é um INSERT ... ON
-- CONFLICT DO UPDATE com RETURNING e um DELETE das linhas velhas.
ALTER TABLE account_email_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_email_daily FORCE ROW LEVEL SECURITY;

CREATE POLICY account_email_daily_scope ON account_email_daily
    USING (current_setting('app.scope', true) = 'account_email')
    WITH CHECK (current_setting('app.scope', true) = 'account_email');

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE account_email_daily;
DROP TABLE account_tokens;
