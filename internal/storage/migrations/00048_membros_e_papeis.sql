-- +goose Up
-- PR 4.1 (membros e papéis): o expand. Só alarga dentro de app.project_id,
-- que só storage.InProjectTx liga depois de conferir a pertença.
--
-- 1. O titular é sempre owner. A FK composta projects (id, titular_id,
--    titular_role) → project_members (project_id, user_id, role), com
--    titular_role fixo em 'owner', faz o banco recusar rebaixar o titular,
--    apagar o vínculo dele e criar projeto sem o vínculo de owner. Como o
--    titular é sempre owner, "todo projeto tem pelo menos um owner" deixa de
--    precisar de trava ou contagem. É DEFERRABLE INITIALLY DEFERRED porque
--    todo caminho (CriarProjetoPadraoNaTx, Store.Create, backfill) grava o
--    projeto antes do vínculo, na mesma transação: a conferência fica para o
--    commit. A FK valida as linhas existentes na hora: um projeto sem owner
--    faria o release falhar sem dano, e isso é inalcançável (portão do 2b).
-- 2. Os colegas ficam visíveis por app.project_id, e não por uma subconsulta
--    em project_members, que recursaria na própria política (42P17).
-- 3. UPDATE e DELETE de vínculos e UPDATE de projects, só no projeto do
--    escopo. Quem pode mudar o quê (papel) é conferido no Go (decisão D3).
-- 4. Em projects, o UPDATE só muda o nome: o gatilho recusa o resto.
--
-- NULLIF pelo motivo da 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

ALTER TABLE projects
    ADD COLUMN titular_role text NOT NULL DEFAULT 'owner' CHECK (titular_role = 'owner');

ALTER TABLE project_members
    ADD CONSTRAINT project_members_papel_unico UNIQUE (project_id, user_id, role);

ALTER TABLE projects
    ADD CONSTRAINT projects_titular_e_owner FOREIGN KEY (id, titular_id, titular_role)
        REFERENCES project_members (project_id, user_id, role)
        DEFERRABLE INITIALLY DEFERRED;

CREATE POLICY project_members_do_projeto_select ON project_members FOR SELECT
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY project_members_do_projeto_update ON project_members FOR UPDATE
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY project_members_do_projeto_delete ON project_members FOR DELETE
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY projects_do_projeto_update ON projects FOR UPDATE
    USING (id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.project_id', true), '')::uuid);

-- Renomear é o único UPDATE de projects. A 00050 (transferência de
-- titularidade) afrouxa titular_id, só para o owner pendente que aceita.
-- +goose StatementBegin
CREATE FUNCTION guard_projects_imutavel() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.titular_id IS DISTINCT FROM OLD.titular_id
       OR NEW.titular_role IS DISTINCT FROM OLD.titular_role
       OR NEW.is_default IS DISTINCT FROM OLD.is_default
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'em projects, só o nome muda' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER guard_projects_imutavel BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION guard_projects_imutavel();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_projects_imutavel ON projects;
DROP FUNCTION guard_projects_imutavel();
DROP POLICY projects_do_projeto_update ON projects;
DROP POLICY project_members_do_projeto_delete ON project_members;
DROP POLICY project_members_do_projeto_update ON project_members;
DROP POLICY project_members_do_projeto_select ON project_members;
ALTER TABLE projects DROP CONSTRAINT projects_titular_e_owner;
ALTER TABLE project_members DROP CONSTRAINT project_members_papel_unico;
ALTER TABLE projects DROP COLUMN titular_role;
