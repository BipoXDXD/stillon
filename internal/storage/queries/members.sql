-- Membros de um projeto (PR 4.1). Todas rodam sob storage.InProjectTx: as
-- políticas project_members_do_projeto_* (00048) liberam os vínculos do
-- projeto do escopo, e só ele. users não tem RLS.

-- Serializa toda mutação de membros e convites do projeto (mudar papel,
-- remover, convidar, aceitar, transferir): a contagem do teto e a mudança
-- seguinte veem o que a anterior gravou.
-- name: LockProjectMembers :exec
SELECT pg_advisory_xact_lock(hashtextextended('stillon.members:' || sqlc.arg(project_id)::uuid::text, 0));

-- O titular primeiro, depois por data de entrada.
-- name: ListProjectMembers :many
SELECT m.user_id, u.email, m.role, (m.user_id = p.titular_id)::boolean AS is_titular, m.created_at
  FROM project_members m
  JOIN users u ON u.id = m.user_id
  JOIN projects p ON p.id = m.project_id
 WHERE m.project_id = sqlc.arg(project_id)
 ORDER BY (m.user_id = p.titular_id) DESC, m.created_at, m.user_id;

-- name: GetProjectMember :one
SELECT m.user_id, u.email, m.role, (m.user_id = p.titular_id)::boolean AS is_titular, m.created_at
  FROM project_members m
  JOIN users u ON u.id = m.user_id
  JOIN projects p ON p.id = m.project_id
 WHERE m.project_id = sqlc.arg(project_id) AND m.user_id = sqlc.arg(user_id);

-- A FK adiável projects_titular_e_owner recusa, no commit, rebaixar o
-- titular: segunda barreira atrás do domínio.
-- name: UpdateMemberRole :exec
UPDATE project_members SET role = sqlc.arg(role)
 WHERE project_id = sqlc.arg(project_id) AND user_id = sqlc.arg(user_id);

-- name: DeleteProjectMember :execrows
DELETE FROM project_members
 WHERE project_id = sqlc.arg(project_id) AND user_id = sqlc.arg(user_id);

-- O teto de membros é do titular: o operador tem folga.
-- name: TitularIsOperator :one
SELECT u.is_operator
  FROM projects p
  JOIN users u ON u.id = p.titular_id
 WHERE p.id = sqlc.arg(project_id);
