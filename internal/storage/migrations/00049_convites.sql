-- +goose Up
-- PR 4.2 (convites): o expand. A tabela é nova e o binário anterior a ignora;
-- a política nova de INSERT em project_members só vale com
-- app.invitation_token_hash, que ele nunca liga.
--
-- Dois escopos, no molde de account_tokens (00047):
--   * o do projeto (app.project_id, que só storage.InProjectTx liga depois de
--     conferir a pertença) lista, cria e revoga os convites do projeto; quem
--     pode o quê (papel) é conferido no Go (decisão D3);
--   * o do token (app.invitation_token_hash, que só storage.InInvitationTokenTx
--     e storage.InInvitationAcceptTx ligam) enxerga a linha do próprio hash e,
--     com a sessão de quem aceita (app.user_id), a consome e cria o vínculo.
--
-- NULLIF pelo motivo da 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

-- token_hash é o SHA-256 do token de 256 bits que vai no link; o token não é
-- gravado. Uso único (accepted_at), 7 dias de prazo escolhidos por quem grava.
-- O e-mail é o normalizado (minúsculas); a comparação com a conta de quem
-- aceita usa lower() do citext de users.
CREATE TABLE project_invitations (
    id          uuid        PRIMARY KEY,
    project_id  uuid        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    email       text        NOT NULL CHECK (email = lower(email) AND char_length(email) BETWEEN 3 AND 254),
    role        text        NOT NULL CHECK (role IN ('owner', 'manager', 'member', 'viewer')),
    token_hash  bytea       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    invited_by  uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL CHECK (expires_at > created_at),
    accepted_at timestamptz,
    accepted_by uuid        REFERENCES users(id) ON DELETE CASCADE,
    CHECK ((accepted_at IS NULL) = (accepted_by IS NULL))
);

-- Um pendente por e-mail e projeto: reenviar apaga o anterior e grava outro.
CREATE UNIQUE INDEX project_invitations_pendente_unico ON project_invitations (project_id, email)
    WHERE accepted_at IS NULL;
CREATE INDEX project_invitations_projeto ON project_invitations (project_id);
-- As FKs para users (a remoção da conta) e a limpeza dos aceitos do membro
-- que sai.
CREATE INDEX project_invitations_aceito_por ON project_invitations (accepted_by)
    WHERE accepted_by IS NOT NULL;
CREATE INDEX project_invitations_convidado_por ON project_invitations (invited_by)
    WHERE invited_by IS NOT NULL;

ALTER TABLE project_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_invitations FORCE ROW LEVEL SECURITY;

-- Escopo do projeto. O INSERT exige que quem convida seja quem age: o
-- invited_by não é forjável dentro do escopo.
CREATE POLICY project_invitations_do_projeto_select ON project_invitations FOR SELECT
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY project_invitations_do_projeto_insert ON project_invitations FOR INSERT
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                AND invited_by = NULLIF(current_setting('app.user_id', true), '')::uuid);
CREATE POLICY project_invitations_do_projeto_delete ON project_invitations FOR DELETE
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);

-- Escopo do token: ler a linha do próprio hash (espiar antes do envio e antes
-- do aceite).
CREATE POLICY project_invitations_por_token_select ON project_invitations FOR SELECT
    USING (token_hash = decode(NULLIF(current_setting('app.invitation_token_hash', true), ''), 'hex'));

-- Aceitar: só o pendente e não vencido do hash, e só a conta com o e-mail do
-- convite. O banco recusa outra conta logada com o link, mesmo que o Go fosse
-- pulado; o prazo é conferido com o relógio do banco, além do da consulta. O
-- WITH CHECK prende accepted_by a quem aceita; o gatilho abaixo, o resto da
-- linha.
CREATE POLICY project_invitations_por_token_update ON project_invitations FOR UPDATE
    USING (token_hash = decode(NULLIF(current_setting('app.invitation_token_hash', true), ''), 'hex')
           AND accepted_at IS NULL
           AND expires_at > now()
           AND email = (SELECT lower(u.email::text) FROM users u
                         WHERE u.id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (token_hash = decode(NULLIF(current_setting('app.invitation_token_hash', true), ''), 'hex')
                AND accepted_by = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O aceite é o único UPDATE de project_invitations e só preenche accepted_at
-- e accepted_by: trocar o papel ou o projeto na mesma UPDATE faria a política
-- de INSERT abaixo admitir um owner forjado ou outro projeto.
-- +goose StatementBegin
CREATE FUNCTION guard_project_invitations_imutavel() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.project_id IS DISTINCT FROM OLD.project_id
       OR NEW.email IS DISTINCT FROM OLD.email
       OR NEW.role IS DISTINCT FROM OLD.role
       OR NEW.token_hash IS DISTINCT FROM OLD.token_hash
       OR NEW.invited_by IS DISTINCT FROM OLD.invited_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'em project_invitations, só o aceite muda' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER guard_project_invitations_imutavel BEFORE UPDATE ON project_invitations
    FOR EACH ROW EXECUTE FUNCTION guard_project_invitations_imutavel();

-- Entrar no projeto pelo convite: quem aceita entra só no projeto e com o
-- papel do convite do hash, e só depois de o ter aceitado (accepted_by). Sem
-- ciclo (42P17): nenhuma política de project_invitations consulta
-- project_members. O membro removido perde os convites que aceitou ali (o Go
-- os apaga na remoção), e esta política nunca mais o admite sem convite novo.
CREATE POLICY project_members_por_convite_insert ON project_members FOR INSERT
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                AND EXISTS (SELECT 1 FROM project_invitations i
                             WHERE i.token_hash = decode(NULLIF(current_setting('app.invitation_token_hash', true), ''), 'hex')
                               AND i.project_id = project_members.project_id
                               AND i.role = project_members.role
                               AND i.accepted_by = project_members.user_id));

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- A política de project_members consulta project_invitations: sem derrubá-la
-- antes, o DROP TABLE dá 2BP01.
DROP POLICY project_members_por_convite_insert ON project_members;
DROP TABLE project_invitations;
DROP FUNCTION guard_project_invitations_imutavel();
