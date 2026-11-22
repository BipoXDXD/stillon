-- Projetos e vínculos. projects e project_members estão sob FORCE RLS: quem
-- grava aqui liga antes o escopo do usuário (InUserTx ou ScopeToUser).

-- O Projeto padrão do titular. O índice único parcial
-- projects_um_padrao_por_titular faz a segunda chamada não criar nada.
-- name: EnsureDefaultProject :exec
INSERT INTO projects (id, name, titular_id, is_default, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(titular_id), true, sqlc.arg(now))
ON CONFLICT (titular_id) WHERE is_default DO NOTHING;

-- name: GetDefaultProjectID :one
SELECT id FROM projects WHERE titular_id = sqlc.arg(titular_id) AND is_default;

-- name: EnsureProjectOwner :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (sqlc.arg(project_id), sqlc.arg(user_id), 'owner', sqlc.arg(now))
ON CONFLICT DO NOTHING;
