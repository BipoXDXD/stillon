-- Teto diário de e-mails de conta (migration 00047), no escopo
-- storage.InAccountEmailTx. O dia é o UTC.

-- A reserva é um comando só, porque o ON CONFLICT DO UPDATE trava a linha do
-- dia e avalia o WHERE na versão mais nova: N reservas simultâneas, em
-- quantas réplicas forem, param exatamente no teto. Nenhuma linha devolvida
-- (pgx.ErrNoRows) é teto atingido. O CTE apaga os dias com mais de 7 dias: a
-- limpeza sai da própria reserva, sem job.
-- name: ReserveAccountEmail :one
WITH velhos AS (DELETE FROM account_email_daily WHERE day < sqlc.arg(day)::date - 7)
INSERT INTO account_email_daily AS d (day, sent) VALUES (sqlc.arg(day)::date, 1)
ON CONFLICT (day) DO UPDATE SET sent = d.sent + 1 WHERE d.sent < sqlc.arg(teto)::int
RETURNING d.sent;

-- Quantos e-mails de conta saíram no dia; zero se nenhum.
-- name: AccountEmailsSentOn :one
SELECT COALESCE((SELECT sent FROM account_email_daily WHERE day = sqlc.arg(day)::date), 0)::int AS sent;

-- Teto diário por conta (migration 00053), no molde de ReserveAccountEmail e
-- na mesma transação dele: a linha (conta, dia) travada pelo ON CONFLICT faz
-- N reservas simultâneas da mesma conta pararem no teto. Nenhuma linha
-- devolvida (pgx.ErrNoRows) é teto da conta atingido. O CTE apaga as linhas
-- com mais de 7 dias de todas as contas.
-- name: ReserveAccountEmailForUser :one
WITH velhos AS (DELETE FROM account_email_daily_by_user WHERE day < sqlc.arg(day)::date - 7)
INSERT INTO account_email_daily_by_user AS d (user_id, day, sent)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(day)::date, 1)
ON CONFLICT (user_id, day) DO UPDATE SET sent = d.sent + 1 WHERE d.sent < sqlc.arg(teto)::int
RETURNING d.sent;

-- Quantos e-mails de conta a conta causou no dia; zero se nenhum.
-- name: AccountEmailsSentByUserOn :one
SELECT COALESCE((SELECT sent FROM account_email_daily_by_user
                  WHERE user_id = sqlc.arg(user_id)::uuid AND day = sqlc.arg(day)::date), 0)::int AS sent;
