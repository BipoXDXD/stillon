-- +goose Up
-- app.project_id passa a conceder acesso: só storage.InProjectTx o liga,
-- depois de conferir a pertença (membro com a conta ativa, titular com a
-- conta ativa). As políticas de dono por user_id saem: nas quatro tabelas com
-- project_id, <t>_do_projeto concede a linha do projeto do escopo; as de
-- posse indireta que nomeavam o dono passam a nomear o projeto, mantendo o
-- sondador e as condições de linha. Sem app.project_id (escopo só do
-- usuário, ping, verify, página pública) nenhuma delas concede; o sondador,
-- o ping, o verify e a página pública seguem pelas políticas próprias.
-- user_id fica como quem criou a linha, sem conceder nada. NULLIF pelo motivo
-- da 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

-- Último passe do backfill, ainda com as políticas de dono: a linha que o
-- binário anterior gravou sem projeto (usuário sem Projeto padrão) entra no
-- padrão. Depois daqui o escopo de usuário não grava recurso, e o guard
-- abaixo não deixa mais NULL → valor.
SELECT backfill_projetos_padrao();

DROP POLICY targets_owner ON targets;
CREATE POLICY targets_do_projeto ON targets FOR ALL
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
DROP POLICY heartbeats_owner ON heartbeats;
CREATE POLICY heartbeats_do_projeto ON heartbeats FOR ALL
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
DROP POLICY notif_channels_owner ON notif_channels;
CREATE POLICY notif_channels_do_projeto ON notif_channels FOR ALL
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
DROP POLICY status_pages_owner ON status_pages;
CREATE POLICY status_pages_do_projeto ON status_pages FOR ALL
    USING (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);

-- As de posse indireta: o mesmo EXISTS no pai, com o projeto no lugar do
-- dono. O sondador continua nomeado onde estava (o escopo verify enxerga o
-- canal, o do ping enxerga o heartbeat, a página pública enxerga o alvo:
-- "a linha do pai existe" não basta).
ALTER POLICY notifications_via_channel ON notifications
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

ALTER POLICY channel_health_owner_or_prober ON channel_health RENAME TO channel_health_projeto_ou_sondador;
ALTER POLICY channel_health_projeto_ou_sondador ON channel_health
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                   AND (c.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                        OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                        AND (c.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                             OR current_setting('app.scope', true) = 'prober')));

ALTER POLICY heartbeat_tags_owner ON heartbeat_tags RENAME TO heartbeat_tags_do_projeto;
ALTER POLICY heartbeat_tags_do_projeto ON heartbeat_tags
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid));

ALTER POLICY target_tls_owner_or_prober_select ON target_tls RENAME TO target_tls_projeto_ou_sondador_select;
ALTER POLICY target_tls_projeto_ou_sondador_select ON target_tls
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
ALTER POLICY target_tls_owner_or_prober_delete ON target_tls RENAME TO target_tls_projeto_ou_sondador_delete;
ALTER POLICY target_tls_projeto_ou_sondador_delete ON target_tls
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

ALTER POLICY incidents_select_via_heartbeat ON incidents
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

ALTER POLICY incidents_owner_update ON incidents RENAME TO incidents_projeto_update;
ALTER POLICY incidents_projeto_update ON incidents
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                      AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
           OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND h.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                           AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
                OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                           AND h.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid));

ALTER POLICY target_tags_owner ON target_tags RENAME TO target_tags_do_projeto;
ALTER POLICY target_tags_do_projeto ON target_tags
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                      AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                           AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid));

ALTER POLICY target_health_owner_reset ON target_health RENAME TO target_health_projeto_reset;
ALTER POLICY target_health_projeto_reset ON target_health
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                      AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                           AND t.project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
                AND consecutive_failures = 0 AND consecutive_successes = 0
                AND streak_since IS NULL);

-- O guard fica estrito: o único NULL → valor era o backfill, que acabou de
-- rodar pela última vez. Nenhum escopo, nem o do superusuário, põe ou troca o
-- projeto de uma linha.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_id_imutavel() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        RAISE EXCEPTION 'O projeto (project_id) de % não muda.', TG_TABLE_NAME
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Sem as políticas de dono, a função não enxergaria nada para preencher.
DROP FUNCTION backfill_projetos_padrao();

-- +goose Down
-- O inverso exato, sem DML: o catálogo volta ao da 00043 (as expressões
-- vigentes ali, o guard da 00040 e o backfill da 00041 com o REVOKE). Os
-- project_id gravados ficam, e o binário anterior volta a ver as linhas pelos
-- donos.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE FUNCTION backfill_projetos_padrao() RETURNS void
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
    u       uuid;
    projeto uuid;
    -- A 00042 chama a função dentro de um escopo: ele volta como estava.
    escopo  text := coalesce(current_setting('app.user_id', true), '');
BEGIN
    FOR u IN SELECT id FROM users ORDER BY id LOOP
        PERFORM set_config('app.user_id', u::text, true);
        INSERT INTO projects (id, name, titular_id, is_default, created_at)
        VALUES (gerar_uuid_v7(), 'Projeto padrão', u, true, now())
        ON CONFLICT (titular_id) WHERE is_default DO NOTHING;
        SELECT p.id INTO STRICT projeto FROM projects p WHERE p.titular_id = u AND p.is_default;
        INSERT INTO project_members (project_id, user_id, role, created_at)
        VALUES (projeto, u, 'owner', now())
        ON CONFLICT DO NOTHING;
        UPDATE targets        SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE heartbeats     SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE notif_channels SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE status_pages   SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
    END LOOP;
    PERFORM set_config('app.user_id', escopo, true);
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION backfill_projetos_padrao() FROM PUBLIC;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_id_imutavel() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF OLD.project_id IS NOT NULL AND NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        RAISE EXCEPTION 'O projeto (project_id) de % não muda.', TG_TABLE_NAME
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

ALTER POLICY target_health_projeto_reset ON target_health
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
                AND consecutive_failures = 0 AND consecutive_successes = 0
                AND streak_since IS NULL);
ALTER POLICY target_health_projeto_reset ON target_health RENAME TO target_health_owner_reset;

ALTER POLICY target_tags_do_projeto ON target_tags
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));
ALTER POLICY target_tags_do_projeto ON target_tags RENAME TO target_tags_owner;

ALTER POLICY incidents_projeto_update ON incidents
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
           OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
                OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                           AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));
ALTER POLICY incidents_projeto_update ON incidents RENAME TO incidents_owner_update;

ALTER POLICY incidents_select_via_heartbeat ON incidents
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

ALTER POLICY target_tls_projeto_ou_sondador_delete ON target_tls
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
ALTER POLICY target_tls_projeto_ou_sondador_delete ON target_tls RENAME TO target_tls_owner_or_prober_delete;
ALTER POLICY target_tls_projeto_ou_sondador_select ON target_tls
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
ALTER POLICY target_tls_projeto_ou_sondador_select ON target_tls RENAME TO target_tls_owner_or_prober_select;

ALTER POLICY heartbeat_tags_do_projeto ON heartbeat_tags
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));
ALTER POLICY heartbeat_tags_do_projeto ON heartbeat_tags RENAME TO heartbeat_tags_owner;

ALTER POLICY channel_health_projeto_ou_sondador ON channel_health
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                   AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                        OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                        AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                             OR current_setting('app.scope', true) = 'prober')));
ALTER POLICY channel_health_projeto_ou_sondador ON channel_health RENAME TO channel_health_owner_or_prober;

ALTER POLICY notifications_via_channel ON notifications
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

DROP POLICY status_pages_do_projeto ON status_pages;
CREATE POLICY status_pages_owner ON status_pages
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
DROP POLICY notif_channels_do_projeto ON notif_channels;
CREATE POLICY notif_channels_owner ON notif_channels
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
DROP POLICY heartbeats_do_projeto ON heartbeats;
CREATE POLICY heartbeats_owner ON heartbeats
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
DROP POLICY targets_do_projeto ON targets;
CREATE POLICY targets_owner ON targets
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
