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

-- O ÚNICO lugar que liga app.project_id é storage.InProjectTx, e só depois de
-- ActiveMembershipRole conferir a pertença na mesma transação. As políticas
-- restritivas da 00042 passam a enxergar só as linhas deste projeto.
-- name: ScopeToProject :exec
SELECT set_config('app.project_id', sqlc.arg(project_id)::uuid::text, true);

-- O papel de user_id no projeto, se for membro, a conta dele não estiver
-- suspensa e a do titular também não. Sem linha: não membro, projeto
-- inexistente ou suspensão, que a aplicação trata igual (404). Sem FOR SHARE:
-- travar a linha exigiria política de UPDATE em project_members.
-- name: ActiveMembershipRole :one
SELECT m.role
  FROM project_members m
  JOIN users u ON u.id = m.user_id
  JOIN projects p ON p.id = m.project_id
  JOIN users t ON t.id = p.titular_id
 WHERE m.project_id = sqlc.arg(project_id) AND m.user_id = sqlc.arg(user_id)
   AND u.suspended_at IS NULL AND t.suspended_at IS NULL;
