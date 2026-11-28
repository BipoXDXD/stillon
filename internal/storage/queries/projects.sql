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
-- titular_is_operator dá os limites do titular de cada projeto
-- (tenancy.LimitesDe): valem os do dono do projeto, não os de quem pede.
-- name: ListMyProjects :many
SELECT p.id, p.name, p.is_default, m.role, p.created_at, t.is_operator AS titular_is_operator
  FROM project_members m
  JOIN projects p ON p.id = m.project_id
  JOIN users t ON t.id = p.titular_id
 WHERE m.user_id = sqlc.arg(user_id) AND t.suspended_at IS NULL
 ORDER BY p.is_default DESC, p.id;

-- Criação de projeto pela API (tenancy.Service.Create). A trava, a contagem e
-- os dois INSERTs rodam numa só transação no escopo do titular: a trava
-- serializa as criações do mesmo titular, e a contagem já vê o que a anterior
-- gravou (READ COMMITTED lê de novo depois de esperar a trava).
-- name: LockTitularProjects :exec
SELECT pg_advisory_xact_lock(hashtextextended('stillon.projects:' || sqlc.arg(titular_id)::uuid::text, 0));

-- projects_member_select mostra ao titular todos os projetos dele por
-- titular_id, inclusive os que não têm vínculo dele.
-- name: CountTitularProjects :one
SELECT count(*) FROM projects WHERE titular_id = sqlc.arg(titular_id);

-- Sem ON CONFLICT: projeto novo que colide falha alto (ao contrário do padrão).
-- name: InsertProject :one
INSERT INTO projects (id, name, titular_id, is_default, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(titular_id), false, sqlc.arg(now))
RETURNING id, name, is_default, created_at;

-- name: InsertProjectOwner :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (sqlc.arg(project_id), sqlc.arg(user_id), 'owner', sqlc.arg(now));

-- O titular do projeto, para as cotas (trava e contagem). Roda sob
-- InProjectTx: projects_member_select deixa o membro ver o projeto.
-- name: ProjectTitular :one
SELECT titular_id FROM projects WHERE id = sqlc.arg(project_id);

-- Renomear (PR 4.1): sob InProjectTx, projects_do_projeto_update (00048) só
-- alcança o projeto do escopo, e o gatilho guard_projects_imutavel recusa
-- mudar qualquer coluna além do nome.
-- name: RenameProject :one
UPDATE projects SET name = sqlc.arg(name)
 WHERE id = sqlc.arg(project_id)
RETURNING id, name, is_default, created_at;
