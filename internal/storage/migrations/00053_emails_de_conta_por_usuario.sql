-- +goose Up
-- Teto diário de e-mails de conta por usuário (spec §Cotas e abuso): quantos
-- e-mails de conta cada conta causou em cada dia UTC, entre todas as réplicas.
-- Só expande: o binário anterior ignora a tabela.
SET LOCAL lock_timeout = '5s';

CREATE TABLE account_email_daily_by_user (
    user_id uuid    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day     date    NOT NULL,
    sent    integer NOT NULL CHECK (sent >= 0),
    PRIMARY KEY (user_id, day)
);

-- A reserva apaga as linhas com mais de 7 dias de todas as contas: o índice
-- poupa a varredura da tabela a cada e-mail.
CREATE INDEX account_email_daily_by_user_day ON account_email_daily_by_user (day);

-- Como account_email_daily: a RLS só abre a tabela ao escopo
-- app.scope = 'account_email' (storage.InAccountEmailTx), na mesma transação
-- que reserva o teto global. FOR ALL pelo INSERT ... ON CONFLICT DO UPDATE com
-- RETURNING e pelo DELETE das linhas velhas.
ALTER TABLE account_email_daily_by_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_email_daily_by_user FORCE ROW LEVEL SECURITY;

CREATE POLICY account_email_daily_by_user_scope ON account_email_daily_by_user
    USING (current_setting('app.scope', true) = 'account_email')
    WITH CHECK (current_setting('app.scope', true) = 'account_email');

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE account_email_daily_by_user;
