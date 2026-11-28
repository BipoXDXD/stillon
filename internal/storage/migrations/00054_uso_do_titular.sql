-- +goose Up
-- PR 5.7 (`stillon user usage`): o uso do titular para o operador, uma linha
-- por projeto de que ele é titular, com as contagens que as cotas comparam.
-- Só expande: o binário anterior não chama a função.
--
-- 1. project_members e project_invitations ganham a política de cota da 00045:
--    abrem, só para SELECT, as linhas dos projetos em app.cota_projetos, que
--    só as funções de cota ligam e zeram antes de sair.
-- 2. uso_do_titular, no molde de cota_do_receptor (00051): o titular é o
--    app.user_id da transação (InUserTx); sem ele, 42501. A suspensão não
--    esconde nada: o operador lê justamente as contas suspensas.
--
-- NULLIF pelo motivo da 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

CREATE POLICY project_members_cota_select ON project_members FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));
CREATE POLICY project_invitations_cota_select ON project_invitations FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));

-- Como na 00045, guarda app.project_id, zera, liga os app.cota_* e restaura
-- antes do RETURN; num erro, o abort desfaz os set_config locais. As colunas
-- de saída levam prefixo: id e name sem ele colidiriam com as colunas das
-- tabelas no plpgsql (referência ambígua). Cada contagem é a do próprio
-- projeto: n_membros conta os vínculos além do titular
-- (CountMembersBesidesTitular) e n_convites, os pendentes não vencidos em
-- agora (CountPendingInvitations).
-- +goose StatementBegin
CREATE FUNCTION uso_do_titular(agora timestamptz)
RETURNS TABLE (p_id uuid, p_nome text, p_padrao boolean,
               n_alvos bigint, n_heartbeats bigint, n_canais bigint,
               n_membros bigint, n_convites bigint)
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    titular  uuid;
    projetos uuid[];
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    titular := NULLIF(current_setting('app.user_id', true), '')::uuid;
    IF titular IS NULL THEN
        RAISE EXCEPTION 'o uso do titular só é lido no escopo do próprio titular' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.project_id', '', true);
    PERFORM set_config('app.cota_titular_id', titular::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular;
    PERFORM set_config('app.cota_projetos', COALESCE(projetos, '{}'::uuid[])::text, true);
    RETURN QUERY
        SELECT p.id, p.name, p.is_default,
               (SELECT count(*) FROM targets t WHERE t.project_id = p.id),
               (SELECT count(*) FROM heartbeats h WHERE h.project_id = p.id),
               (SELECT count(*) FROM notif_channels c WHERE c.project_id = p.id),
               (SELECT count(*) FROM project_members m WHERE m.project_id = p.id AND m.user_id <> titular),
               (SELECT count(*) FROM project_invitations i
                 WHERE i.project_id = p.id AND i.accepted_at IS NULL AND i.expires_at > agora)
          FROM projects p
         WHERE p.titular_id = titular
         ORDER BY p.is_default DESC, p.created_at, p.id;
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION uso_do_titular(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION uso_do_titular(timestamptz) TO CURRENT_USER;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP FUNCTION uso_do_titular(timestamptz);
DROP POLICY project_invitations_cota_select ON project_invitations;
DROP POLICY project_members_cota_select ON project_members;
