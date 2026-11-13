-- +goose Up
-- Endurecimento depois das políticas por comando (00034):
--
-- incidents: a política de UPDATE do dono limita as linhas, não as colunas.
--   Um gatilho faz o dono (e qualquer escopo que não seja o do sondador) só
--   mudar ack_at, que é o que AckIncident e AckHeartbeatIncident fazem.
-- target_health: lê quem enxerga o alvo; só o sondador grava (EnsureTargetHealth,
--   SaveTargetHealth). O dono só zera a histerese ao mudar o critério de saúde
--   do alvo (ResetTargetHealth): a política exige a linha zerada, e o gatilho
--   impede que o mesmo UPDATE mexa em outra coluna. Ninguém apaga: a linha só
--   sai no ON DELETE CASCADE do alvo, e ação referencial não passa por RLS.
-- check_rollup_1h: lê quem enxerga o alvo (a página pública inclusive); só o
--   sondador grava e apaga (ConsolidateRollups, com ON CONFLICT DO UPDATE, e
--   DeleteOldRollups).
-- protect_checks_partition: recusa o que não é partição de checks.
--
-- O gatilho vale também para quem roda sem escopo nenhum (migrations, ajustes
-- manuais): corrigir um incidente à mão exige ligar o escopo do sondador na
-- transação, de propósito.
SET LOCAL lock_timeout = '5s';

-- A trava de partição vem antes de qualquer trava de tabela, como na 00034.
SELECT pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));

-- TG_ARGV lista as colunas que podem mudar fora do sondador. A comparação é
-- da linha inteira sem elas, então uma coluna nova nasce protegida.
-- +goose StatementBegin
CREATE FUNCTION guard_colunas_fora_do_sondador() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF current_setting('app.scope', true) IS DISTINCT FROM 'prober'
       AND (to_jsonb(NEW) - TG_ARGV) IS DISTINCT FROM (to_jsonb(OLD) - TG_ARGV) THEN
        RAISE EXCEPTION 'Fora do sondador, % só muda em %.', TG_TABLE_NAME, array_to_string(TG_ARGV, ', ')
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER incidents_so_ack_fora_do_sondador
BEFORE UPDATE ON incidents
FOR EACH ROW EXECUTE FUNCTION guard_colunas_fora_do_sondador('ack_at');

CREATE TRIGGER target_health_so_zerar_fora_do_sondador
BEFORE UPDATE ON target_health
FOR EACH ROW EXECUTE FUNCTION guard_colunas_fora_do_sondador(
    'consecutive_failures', 'consecutive_successes', 'streak_since');

DROP POLICY target_health_via_target ON target_health;
CREATE POLICY target_health_select_via_target ON target_health FOR SELECT
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));
CREATE POLICY target_health_prober_insert ON target_health FOR INSERT
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));
CREATE POLICY target_health_prober_update ON target_health FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));
-- O dono nomeado, e não "quem enxerga o alvo": a página pública também
-- enxerga alvos.
CREATE POLICY target_health_owner_reset ON target_health FOR UPDATE
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                      AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id
                           AND t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
                AND consecutive_failures = 0 AND consecutive_successes = 0
                AND streak_since IS NULL);

DROP POLICY check_rollup_via_target ON check_rollup_1h;
CREATE POLICY check_rollup_select_via_target ON check_rollup_1h FOR SELECT
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));
CREATE POLICY check_rollup_prober_insert ON check_rollup_1h FOR INSERT
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));
CREATE POLICY check_rollup_prober_update ON check_rollup_1h FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober'
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));
CREATE POLICY check_rollup_prober_delete ON check_rollup_1h FOR DELETE
    USING (current_setting('app.scope', true) = 'prober');

-- Igual à da 00034, com a conferência no começo: chamada com outra tabela, ela
-- ligaria RLS e criaria as políticas de checks nela.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_checks_partition(p_partition regclass) RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path = public, pg_temp AS $$
DECLARE
    ligada  boolean;
    forcada boolean;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits
                    WHERE inhrelid = p_partition AND inhparent = 'public.checks'::regclass) THEN
        RAISE EXCEPTION '% não é partição de checks.', p_partition
            USING ERRCODE = '22023';
    END IF;
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

-- O comentário da 00004 diz que partição não tem RLS; desde a 00033 cada
-- partição tem. A 00004 não se edita, então a nota correta fica no catálogo,
-- na política de leitura da mãe: um COMMENT ON TABLE iria parar no código que
-- o sqlc gera.
COMMENT ON POLICY checks_select_via_target ON checks IS
    'Vale na mãe e em cada partição: protect_checks_partition (00033, 00034, 00035) copia para a '
    'partição as políticas da mãe, e ensure_checks_partition a chama para toda partição nova.';

-- +goose Down
SET LOCAL lock_timeout = '5s';

SELECT pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));

COMMENT ON POLICY checks_select_via_target ON checks IS NULL;

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

DROP POLICY check_rollup_prober_delete ON check_rollup_1h;
DROP POLICY check_rollup_prober_update ON check_rollup_1h;
DROP POLICY check_rollup_prober_insert ON check_rollup_1h;
DROP POLICY check_rollup_select_via_target ON check_rollup_1h;
CREATE POLICY check_rollup_via_target ON check_rollup_1h
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));

DROP POLICY target_health_owner_reset ON target_health;
DROP POLICY target_health_prober_update ON target_health;
DROP POLICY target_health_prober_insert ON target_health;
DROP POLICY target_health_select_via_target ON target_health;
CREATE POLICY target_health_via_target ON target_health
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));

DROP TRIGGER target_health_so_zerar_fora_do_sondador ON target_health;
DROP TRIGGER incidents_so_ack_fora_do_sondador ON incidents;
DROP FUNCTION guard_colunas_fora_do_sondador();
