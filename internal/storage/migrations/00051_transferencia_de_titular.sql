-- +goose Up
-- PR 4.3 (transferência de titularidade em dois passos, decisão D2): o
-- expand. A coluna nova é anulável e o binário anterior a ignora; o gatilho
-- afrouxado só aceita trocar o titular quando a troca casa com o pendente.
--
-- 1. pending_titular_id é o owner a quem o titular propôs o projeto. Quem pode
--    propor e cancelar é conferido no Go (decisão D3), sob a trava
--    stillon.members; o banco garante que a proposta nunca aponta o próprio
--    titular nem o Projeto padrão. A conta apagada leva a proposta (SET NULL:
--    o gatilho deixa a coluna mudar, e a ação da FK não passa pela RLS).
-- 2. guard_projects_imutavel passa a aceitar a troca de titular_id só no
--    aceite: o titular novo é o pendente, é quem age (app.user_id), e a
--    proposta é limpa na mesma UPDATE. A FK adiável projects_titular_e_owner
--    (00048) exige, no commit, que ele seja owner.
-- 3. cota_do_receptor e cota_urls_com_regra_do_receptor, no molde das funções
--    da 00045, contam os recursos do receptor com os do projeto somados, para
--    o Go comparar com os tetos (o de regras por host inclusive).
--
-- NULLIF pelo motivo da 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

-- O Projeto padrão não é transferido: quem recebe já tem o dele (índice único
-- projects_um_padrao_por_titular), e as rotas antigas usam o padrão de cada
-- um. tenancy.AuthorizeTransferProposal recusa antes; esta CHECK é a segunda
-- barreira. Isso pode ser revisto pela D4 do PR 7.
ALTER TABLE projects
    ADD COLUMN pending_titular_id uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD CONSTRAINT projects_pendente_nao_e_o_titular CHECK (pending_titular_id IS DISTINCT FROM titular_id),
    ADD CONSTRAINT projects_padrao_nao_transfere CHECK (pending_titular_id IS NULL OR NOT is_default);

-- A FK para users (a remoção da conta) e a busca das propostas de alguém.
CREATE INDEX projects_pendente ON projects (pending_titular_id) WHERE pending_titular_id IS NOT NULL;

-- Renomear, propor e cancelar mudam só name e pending_titular_id. titular_id
-- muda só no aceite, e só o titular grava uma proposta.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_projects_imutavel() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
BEGIN
    -- Só comparações que nunca dão NULL: sem proposta, um NOT (NULL AND ...)
    -- daria NULL, e o IF deixaria passar.
    IF NEW.titular_id IS DISTINCT FROM OLD.titular_id
       AND NOT (OLD.pending_titular_id IS NOT NULL
                AND NEW.titular_id IS NOT DISTINCT FROM OLD.pending_titular_id
                AND NEW.pending_titular_id IS NULL
                AND NEW.titular_id IS NOT DISTINCT FROM NULLIF(current_setting('app.user_id', true), '')::uuid) THEN
        RAISE EXCEPTION 'em projects, o titular só muda no aceite de quem recebe' USING ERRCODE = '42501';
    END IF;
    -- Gravar uma proposta é só do titular; limpar é livre (o titular cancela,
    -- o pendente recusa, e rebaixar ou remover o pendente a apaga).
    IF NEW.pending_titular_id IS NOT NULL
       AND NEW.pending_titular_id IS DISTINCT FROM OLD.pending_titular_id
       AND OLD.titular_id IS DISTINCT FROM NULLIF(current_setting('app.user_id', true), '')::uuid THEN
        RAISE EXCEPTION 'em projects, só o titular propõe a transferência' USING ERRCODE = '42501';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.titular_role IS DISTINCT FROM OLD.titular_role
       OR NEW.is_default IS DISTINCT FROM OLD.is_default
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'em projects, só o nome e a proposta de transferência mudam' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- A cota de quem recebe, já com o projeto somado: (projetos, alvos,
-- heartbeats, canais) do receptor mais os do projeto. Só o pendente chama:
-- app.user_id tem de ser o pending_titular_id do projeto, que ele enxerga
-- pela RLS normal de projects (é membro); senão, 42501. Como na 00045, guarda
-- app.project_id, zera, liga os app.cota_* e restaura na única saída normal;
-- num erro, o abort desfaz os set_config locais. As colunas de saída levam
-- n_ para não colidir com os nomes das tabelas no plpgsql.
-- +goose StatementBegin
CREATE FUNCTION cota_do_receptor(projeto uuid)
RETURNS TABLE (n_projetos bigint, n_alvos bigint, n_heartbeats bigint, n_canais bigint)
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    receptor uuid;
    pendente uuid;
    projetos uuid[];
    np       bigint;
    na       bigint;
    nh       bigint;
    nc       bigint;
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    receptor := NULLIF(current_setting('app.user_id', true), '')::uuid;
    PERFORM set_config('app.project_id', '', true);
    SELECT p.pending_titular_id INTO pendente FROM projects p WHERE p.id = projeto;
    IF receptor IS NULL OR pendente IS NULL OR pendente <> receptor THEN
        RAISE EXCEPTION 'a titularidade do projeto não foi proposta a quem conta' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', receptor::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = receptor;
    np := COALESCE(cardinality(projetos), 0) + 1;
    projetos := array_append(COALESCE(projetos, '{}'::uuid[]), projeto);
    PERFORM set_config('app.cota_projetos', projetos::text, true);
    SELECT count(*) INTO na FROM targets WHERE project_id = ANY (projetos);
    SELECT count(*) INTO nh FROM heartbeats WHERE project_id = ANY (projetos);
    SELECT count(*) INTO nc FROM notif_channels WHERE project_id = ANY (projetos);
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN QUERY SELECT np, na, nh, nc;
END
$$;
-- +goose StatementEnd

-- As URLs dos alvos com regra de conteúdo do receptor, com as do projeto
-- somadas: o teto de regras por host, como cota_urls_com_regra_do_titular
-- (00045), com a chave do host em Go. Os mesmos guardas de cota_do_receptor.
-- +goose StatementBegin
CREATE FUNCTION cota_urls_com_regra_do_receptor(projeto uuid) RETURNS TABLE (id uuid, url text)
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    receptor uuid;
    pendente uuid;
    projetos uuid[];
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    receptor := NULLIF(current_setting('app.user_id', true), '')::uuid;
    PERFORM set_config('app.project_id', '', true);
    SELECT p.pending_titular_id INTO pendente FROM projects p WHERE p.id = projeto;
    IF receptor IS NULL OR pendente IS NULL OR pendente <> receptor THEN
        RAISE EXCEPTION 'a titularidade do projeto não foi proposta a quem conta' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', receptor::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = receptor;
    projetos := array_append(COALESCE(projetos, '{}'::uuid[]), projeto);
    PERFORM set_config('app.cota_projetos', projetos::text, true);
    RETURN QUERY
        SELECT t.id, t.url FROM targets t
         WHERE t.project_id = ANY (projetos) AND t.content_mode IS NOT NULL;
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION cota_do_receptor(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cota_do_receptor(uuid) TO CURRENT_USER;
REVOKE EXECUTE ON FUNCTION cota_urls_com_regra_do_receptor(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cota_urls_com_regra_do_receptor(uuid) TO CURRENT_USER;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP FUNCTION cota_urls_com_regra_do_receptor(uuid);
DROP FUNCTION cota_do_receptor(uuid);

-- O gatilho da 00048.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_projects_imutavel() RETURNS trigger
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

DROP INDEX projects_pendente;
ALTER TABLE projects DROP COLUMN pending_titular_id;
