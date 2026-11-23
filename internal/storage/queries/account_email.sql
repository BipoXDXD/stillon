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
