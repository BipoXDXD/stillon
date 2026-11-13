-- +goose Up
-- As políticas FOR ALL que delegavam ao alvo (00004, 00005, 00016, 00017)
-- davam a cada escopo que enxerga a linha todos os comandos, inclusive os que
-- nenhum caminho do código usa. Aqui cada uma vira políticas por comando, só
-- para quem precisa:
--
-- checks: lê quem enxerga o alvo; só o sondador grava (InsertCheck). Ninguém
--   altera nem apaga pela RLS: a retenção derruba partição inteira
--   (drop_checks_partitions_before), e DROP TABLE não passa por RLS.
-- incidents: lê quem enxergava antes; só o sondador abre (OpenIncident,
--   OpenHeartbeatIncident). Alteram o sondador (fechar e marcar aviso) e o
--   dono (AckIncident, AckHeartbeatIncident). Ninguém apaga: incidente só sai
--   no ON DELETE CASCADE do alvo ou do heartbeat, e ação referencial não passa
--   por RLS.
-- target_tags: só o dono, no molde de heartbeat_tags (00021). Sondador e
--   página pública não leem tags.
--
-- As partições de checks recebem as mesmas políticas da mãe por
-- protect_checks_partition (00033), reescrita aqui.
SET LOCAL lock_timeout = '5s';

-- A trava de partição vem antes de qualquer trava de tabela: a retenção e a
-- criação de partição pegam a advisory e depois checks. Na ordem inversa, um
-- DROP de partição concorrente fecha um deadlock com esta migration.
SELECT pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));

DROP POLICY checks_via_target ON checks;
CREATE POLICY checks_select_via_target ON checks FOR SELECT
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id));
CREATE POLICY checks_prober_insert ON checks FOR INSERT
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id));

DROP POLICY incidents_via_target ON incidents;
DROP POLICY incidents_via_heartbeat ON incidents;
CREATE POLICY incidents_select_via_target ON incidents FOR SELECT
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id));
-- O escopo do ping enxerga o próprio heartbeat: por isso o dono e o sondador
-- vêm nomeados, como na 00016.
CREATE POLICY incidents_select_via_heartbeat ON incidents FOR SELECT
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
CREATE POLICY incidents_prober_insert ON incidents FOR INSERT
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id)
                     OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id)));
CREATE POLICY incidents_prober_update ON incidents FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id)
                     OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id)));
-- O dono nomeado, e não "quem enxerga o alvo": a página pública também
-- enxerga alvos.
CREATE POLICY incidents_owner_update ON incidents FOR UPDATE
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
           OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
                OR EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                           AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));

DROP POLICY target_tags_via_target ON target_tags;
CREATE POLICY target_tags_owner ON target_tags
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));

-- Mesmo contrato da 00033: só altera o que falta, para o tick diário não pegar
-- ACCESS EXCLUSIVE numa partição já protegida. A política antiga sai daqui
-- também, para mãe e partições nunca divergirem.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_checks_partition(p_partition regclass) RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path = public, pg_temp AS $$
DECLARE
    ligada  boolean;
    forcada boolean;
BEGIN
    SELECT relrowsecurity, relforcerowsecurity INTO ligada, forcada
      FROM pg_catalog.pg_class WHERE oid = p_partition;
    IF NOT ligada THEN
        EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', p_partition);
    END IF;
    IF NOT forcada THEN
        EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', p_partition);
    END IF;
    IF EXISTS (SELECT 1 FROM pg_catalog.pg_policy
                WHERE polrelid = p_partition AND polname = 'checks_via_target') THEN
        EXECUTE format('DROP POLICY checks_via_target ON %s', p_partition);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_policy
                    WHERE polrelid = p_partition AND polname = 'checks_select_via_target') THEN
        EXECUTE format(
            'CREATE POLICY checks_select_via_target ON %1$s FOR SELECT
                 USING (EXISTS (SELECT 1 FROM public.targets t WHERE t.id = %1$s.target_id))',
            p_partition);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_policy
                    WHERE polrelid = p_partition AND polname = 'checks_prober_insert') THEN
        EXECUTE format(
            'CREATE POLICY checks_prober_insert ON %1$s FOR INSERT
                 WITH CHECK (current_setting(''app.scope'', true) = ''prober''
                             AND EXISTS (SELECT 1 FROM public.targets t WHERE t.id = %1$s.target_id))',
            p_partition);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    parte regclass;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));
    FOR parte IN
        SELECT inhrelid::regclass FROM pg_inherits WHERE inhparent = 'public.checks'::regclass
    LOOP
        PERFORM protect_checks_partition(parte);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_checks_partition(p_partition regclass) RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path = public, pg_temp AS $$
DECLARE
    ligada  boolean;
    forcada boolean;
BEGIN
    SELECT relrowsecurity, relforcerowsecurity INTO ligada, forcada
      FROM pg_catalog.pg_class WHERE oid = p_partition;
    IF NOT ligada THEN
        EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', p_partition);
    END IF;
    IF NOT forcada THEN
        EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', p_partition);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_policy
                    WHERE polrelid = p_partition AND polname = 'checks_via_target') THEN
        EXECUTE format(
            'CREATE POLICY checks_via_target ON %1$s
                 USING (EXISTS (SELECT 1 FROM public.targets t WHERE t.id = %1$s.target_id))
                 WITH CHECK (EXISTS (SELECT 1 FROM public.targets t WHERE t.id = %1$s.target_id))',
            p_partition);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    parte regclass;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));
    FOR parte IN
        SELECT inhrelid::regclass FROM pg_inherits WHERE inhparent = 'public.checks'::regclass
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS checks_select_via_target ON %s', parte);
        EXECUTE format('DROP POLICY IF EXISTS checks_prober_insert ON %s', parte);
        PERFORM protect_checks_partition(parte);
    END LOOP;
END
$$;
-- +goose StatementEnd

DROP POLICY target_tags_owner ON target_tags;
CREATE POLICY target_tags_via_target ON target_tags
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id));

DROP POLICY incidents_owner_update ON incidents;
DROP POLICY incidents_prober_update ON incidents;
DROP POLICY incidents_prober_insert ON incidents;
DROP POLICY incidents_select_via_heartbeat ON incidents;
DROP POLICY incidents_select_via_target ON incidents;
CREATE POLICY incidents_via_target ON incidents
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id));
CREATE POLICY incidents_via_heartbeat ON incidents
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = incidents.heartbeat_id
                      AND (h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

DROP POLICY checks_prober_insert ON checks;
DROP POLICY checks_select_via_target ON checks;
CREATE POLICY checks_via_target ON checks
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks.target_id));
