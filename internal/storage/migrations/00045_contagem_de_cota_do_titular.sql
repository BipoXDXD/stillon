-- +goose Up
-- Cota do titular (spec §Cotas): enxergar os outros projetos do titular sem
-- reabrir nada por user_id. Os GUCs app.cota_* só existem aqui, ligados
-- dentro das funções abaixo. Políticas só de SELECT.
--
-- Cada função conta no escopo de quem chama: o titular sai do projeto pela
-- RLS normal de projects (app.user_id), e um projeto que o escopo não
-- enxerga é recusado (42501). Dentro dela, app.project_id fica vazio (a
-- restritiva <t>_so_projeto_do_escopo da 00042 vale true) e as políticas
-- abaixo concedem só a leitura dos projetos daquele titular. SECURITY
-- INVOKER (o padrão): a RLS vale dentro da função como fora. NULLIF pelo
-- motivo da 00004 (GUC vira '' na conexão reusada).
--
-- Por que set_config com restauração e não a cláusula SET da função: o
-- Postgres (15 em diante) só aceita GUC de placeholder (app.*) numa cláusula
-- SET de função de superusuário ou de quem tem GRANT SET ON PARAMETER, e o
-- stillon_app, que roda as migrations, é NOSUPERUSER. Então cada função
-- guarda app.project_id em escopo, zera, liga os app.cota_* e, na única saída
-- normal, devolve app.project_id ao valor guardado e zera os dois da cota.
-- Num erro, o abort da transação ou do savepoint (o bloco EXCEPTION do
-- plpgsql também é um) desfaz os set_config locais.
SET LOCAL lock_timeout = '5s';

CREATE POLICY projects_cota_select ON projects FOR SELECT
    USING (titular_id = NULLIF(current_setting('app.cota_titular_id', true), '')::uuid);
CREATE POLICY targets_cota_select ON targets FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));
CREATE POLICY heartbeats_cota_select ON heartbeats FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));
CREATE POLICY notif_channels_cota_select ON notif_channels FOR SELECT
    USING (project_id = ANY (NULLIF(current_setting('app.cota_projetos', true), '')::uuid[]));

-- Os alvos do titular do projeto, somando todos os projetos dele.
-- +goose StatementBegin
CREATE FUNCTION cota_alvos_do_titular(projeto uuid) RETURNS bigint
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    titular  uuid;
    projetos uuid[];
    n        bigint;
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    PERFORM set_config('app.project_id', '', true);
    SELECT p.titular_id INTO titular FROM projects p WHERE p.id = projeto;
    IF titular IS NULL THEN
        RAISE EXCEPTION 'projeto fora do escopo da contagem de cota' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', titular::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular;
    PERFORM set_config('app.cota_projetos', projetos::text, true);
    SELECT count(*) INTO n FROM targets WHERE project_id = ANY (projetos);
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN n;
END
$$;
-- +goose StatementEnd

-- As URLs dos alvos com regra de conteúdo do titular, em todos os projetos
-- dele: a chave do host sai em Go (IDNA). RETURN QUERY só acumula as linhas;
-- a restauração vem antes do RETURN que encerra.
-- +goose StatementBegin
CREATE FUNCTION cota_urls_com_regra_do_titular(projeto uuid) RETURNS TABLE (id uuid, url text)
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    titular  uuid;
    projetos uuid[];
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    PERFORM set_config('app.project_id', '', true);
    SELECT p.titular_id INTO titular FROM projects p WHERE p.id = projeto;
    IF titular IS NULL THEN
        RAISE EXCEPTION 'projeto fora do escopo da contagem de cota' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', titular::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular;
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

-- Os heartbeats do titular do projeto, somando todos os projetos dele.
-- +goose StatementBegin
CREATE FUNCTION cota_heartbeats_do_titular(projeto uuid) RETURNS bigint
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    titular  uuid;
    projetos uuid[];
    n        bigint;
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    PERFORM set_config('app.project_id', '', true);
    SELECT p.titular_id INTO titular FROM projects p WHERE p.id = projeto;
    IF titular IS NULL THEN
        RAISE EXCEPTION 'projeto fora do escopo da contagem de cota' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', titular::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular;
    PERFORM set_config('app.cota_projetos', projetos::text, true);
    SELECT count(*) INTO n FROM heartbeats WHERE project_id = ANY (projetos);
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN n;
END
$$;
-- +goose StatementEnd

-- Os canais do titular do projeto, somando todos os projetos dele.
-- +goose StatementBegin
CREATE FUNCTION cota_canais_do_titular(projeto uuid) RETURNS bigint
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
DECLARE
    escopo   text;
    titular  uuid;
    projetos uuid[];
    n        bigint;
BEGIN
    escopo := COALESCE(current_setting('app.project_id', true), '');
    PERFORM set_config('app.project_id', '', true);
    SELECT p.titular_id INTO titular FROM projects p WHERE p.id = projeto;
    IF titular IS NULL THEN
        RAISE EXCEPTION 'projeto fora do escopo da contagem de cota' USING ERRCODE = '42501';
    END IF;
    PERFORM set_config('app.cota_titular_id', titular::text, true);
    SELECT array_agg(p.id) INTO projetos FROM projects p WHERE p.titular_id = titular;
    PERFORM set_config('app.cota_projetos', projetos::text, true);
    SELECT count(*) INTO n FROM notif_channels WHERE project_id = ANY (projetos);
    PERFORM set_config('app.cota_projetos', '', true);
    PERFORM set_config('app.cota_titular_id', '', true);
    PERFORM set_config('app.project_id', escopo, true);
    RETURN n;
END
$$;
-- +goose StatementEnd

-- Só o papel da aplicação conta: quem roda as migrations é ele (dono das
-- tabelas, como em produção), e PUBLIC perde o EXECUTE padrão.
REVOKE EXECUTE ON FUNCTION cota_alvos_do_titular(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION cota_urls_com_regra_do_titular(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION cota_heartbeats_do_titular(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION cota_canais_do_titular(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cota_alvos_do_titular(uuid) TO CURRENT_USER;
GRANT EXECUTE ON FUNCTION cota_urls_com_regra_do_titular(uuid) TO CURRENT_USER;
GRANT EXECUTE ON FUNCTION cota_heartbeats_do_titular(uuid) TO CURRENT_USER;
GRANT EXECUTE ON FUNCTION cota_canais_do_titular(uuid) TO CURRENT_USER;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP FUNCTION cota_canais_do_titular(uuid);
DROP FUNCTION cota_heartbeats_do_titular(uuid);
DROP FUNCTION cota_urls_com_regra_do_titular(uuid);
DROP FUNCTION cota_alvos_do_titular(uuid);

DROP POLICY notif_channels_cota_select ON notif_channels;
DROP POLICY heartbeats_cota_select ON heartbeats;
DROP POLICY targets_cota_select ON targets;
DROP POLICY projects_cota_select ON projects;
