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

-- Os projetos de que user_id é membro, com o papel dele, sem os de titular
-- suspenso (os membros perdem o acesso enquanto durar a suspensão). O padrão
-- vem primeiro. Roda no escopo do usuário: project_members_own_select já
-- restringe aos vínculos dele, e o filtro por user_id deixa isso explícito.
-- name: ListMyProjects :many
SELECT p.id, p.name, p.is_default, m.role, p.created_at
  FROM project_members m
  JOIN projects p ON p.id = m.project_id
  JOIN users t ON t.id = p.titular_id
 WHERE m.user_id = sqlc.arg(user_id) AND t.suspended_at IS NULL
 ORDER BY p.is_default DESC, p.id;

-- O titular do projeto, para as cotas (trava e contagem). Roda sob
-- InProjectTx: projects_member_select deixa o membro ver o projeto.
-- name: ProjectTitular :one
SELECT titular_id FROM projects WHERE id = sqlc.arg(project_id);
