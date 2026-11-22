-- +goose Up
-- Etapa 1 (multi-inquilino), PR 1: o expand. Cria projetos e vínculos e as
-- colunas de conta em users; nada aqui muda o que o binário anterior faz.
SET LOCAL lock_timeout = '5s';

ALTER TABLE users
    ADD COLUMN is_operator       boolean NOT NULL DEFAULT false,
    ADD COLUMN email_verified_at timestamptz,
    ADD COLUMN suspended_at      timestamptz;

CREATE TABLE projects (
    id         uuid PRIMARY KEY,
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    titular_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);
CREATE INDEX projects_titular_idx ON projects (titular_id);
CREATE UNIQUE INDEX projects_um_padrao_por_titular ON projects (titular_id) WHERE is_default;

CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'manager', 'member', 'viewer')),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user_idx ON project_members (user_id);

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects FORCE ROW LEVEL SECURITY;
ALTER TABLE project_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_members FORCE ROW LEVEL SECURITY;

-- A pessoa vê os próprios vínculos. Ver os colegas de projeto é do PR 4.
CREATE POLICY project_members_own_select ON project_members FOR SELECT
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
-- Só o titular entra, e só como owner, no projeto de que é titular. Esta
-- política consulta projects, e a de projects consulta project_members só
-- pela política de SELECT acima, que não volta a projects: sem ciclo (42P17).
CREATE POLICY project_members_titular_owner_insert ON project_members FOR INSERT
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                AND role = 'owner'
                AND EXISTS (SELECT 1 FROM projects p
                             WHERE p.id = project_members.project_id
                               AND p.titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid));

-- O titular aparece antes do vínculo existir: o INSERT do vínculo de owner
-- consulta o projeto recém-criado.
CREATE POLICY projects_member_select ON projects FOR SELECT
    USING (titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid
           OR EXISTS (SELECT 1 FROM project_members m
                       WHERE m.project_id = projects.id
                         AND m.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));
CREATE POLICY projects_titular_insert ON projects FOR INSERT
    WITH CHECK (titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
-- Sem UPDATE nem DELETE: renomear, transferir e excluir são dos PRs 4 e 6.

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- As políticas que consultam a outra tabela criam dependência entre as duas:
-- sem derrubá-las antes, o DROP TABLE dá 2BP01.
DROP POLICY projects_member_select ON projects;
DROP POLICY project_members_titular_owner_insert ON project_members;
DROP TABLE project_members;
DROP TABLE projects;
ALTER TABLE users DROP COLUMN suspended_at, DROP COLUMN email_verified_at, DROP COLUMN is_operator;
