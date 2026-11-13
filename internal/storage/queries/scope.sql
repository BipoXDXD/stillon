-- Escopos da RLS ligados no MEIO de uma transação, quando ela só descobre o
-- usuário depois de começar (login, refresh, logout, set-password, reset-2fa)
-- ou quando o código de auth precisa do pgx.Tx cru. Quem já sabe o usuário
-- antes de abrir a transação usa storage.InUserTx. set_config(..., true) é o
-- SET LOCAL: o valor morre no fim da transação.

-- A transação passa a ser do usuário: enxerga e grava só as linhas dele.
-- name: ScopeToUser :exec
SELECT set_config('app.user_id', sqlc.arg(user_id)::uuid::text, true);

-- Pré-autenticação do refresh e do logout: a transação LÊ só a linha de
-- sessions cujo token_hash é este (política sessions_by_token_select), e só
-- para descobrir o dono. Em hexadecimal porque GUC é texto.
-- name: ScopeToSessionHash :exec
SELECT set_config('app.session_token_hash', encode(sqlc.arg(token_hash)::bytea, 'hex'), true);
