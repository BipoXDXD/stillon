-- Tokens de confirmação de e-mail e de redefinição de senha (migration 00047).
-- Os propósitos são 'confirm_email' e 'reset_password'. Gravar e apagar rodam
-- no escopo do dono (app.user_id); espiar e consumir, no escopo do próprio
-- hash (storage.InAccountTokenTx), porque quem clica no link não tem sessão.

-- name: InsertAccountToken :exec
INSERT INTO account_tokens (token_hash, user_id, purpose, created_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(purpose), sqlc.arg(created_at), sqlc.arg(expires_at));

-- Um token novo invalida os anteriores do mesmo propósito: o último e-mail
-- enviado é o único link que vale.
-- name: DeleteAccountTokens :exec
DELETE FROM account_tokens WHERE user_id = sqlc.arg(user_id) AND purpose = sqlc.arg(purpose);

-- O dono de um token válido, sem consumi-lo.
-- name: PeekAccountToken :one
SELECT user_id FROM account_tokens
 WHERE token_hash = sqlc.arg(token_hash) AND purpose = sqlc.arg(purpose)
   AND used_at IS NULL AND expires_at > sqlc.arg(now);

-- O consumo é um comando só: o UPDATE trava a linha, e o segundo consumo
-- simultâneo reavalia o WHERE na versão nova (used_at preenchido) e não
-- devolve nada. Nenhuma linha (pgx.ErrNoRows) é token inexistente, usado ou
-- vencido, sem distinção.
-- name: ConsumeAccountToken :one
UPDATE account_tokens SET used_at = sqlc.arg(now)::timestamptz
 WHERE token_hash = sqlc.arg(token_hash) AND purpose = sqlc.arg(purpose)
   AND used_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING user_id;
