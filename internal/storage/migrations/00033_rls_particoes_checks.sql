-- +goose Up
-- A RLS de checks passa a valer também em cada partição. A política da mãe só
-- cobre consultas que nomeiam checks; a RLS não se herda, então uma consulta
-- que nomeasse checks_AAAA_MM direto escaparia dela. Cada partição recebe
-- ENABLE, FORCE e a mesma política checks_via_target da 00004, escrita uma vez
-- só em protect_checks_partition.
--
-- protect_checks_partition só altera o que falta: ALTER TABLE pega ACCESS
-- EXCLUSIVE na partição, e o tick diário chama ensure_checks_partition para a
-- partição do mês, que já existe e já está protegida. Sem isso, ele
-- enfileiraria o sondador atrás da trava todo dia.
--
-- O sondador continua gravando pela mãe (vale a política da mãe, não a da
-- partição), e o DROP da retenção não passa por RLS.
--
-- As duas funções são SECURITY INVOKER com search_path fixo e nomes
-- qualificados: rodam com os privilégios de quem chama, e uma tabela
-- temporária não sombreia checks nem targets.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE FUNCTION protect_checks_partition(p_partition regclass) RETURNS void
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
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path = public, pg_temp AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
    nome   text := 'checks_' || to_char(inicio, 'YYYY_MM');
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.checks FOR VALUES FROM (%L) TO (%L)',
        nome,
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
    PERFORM protect_checks_partition(format('public.%I', nome)::regclass);
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
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
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
        EXECUTE format('DROP POLICY IF EXISTS checks_via_target ON %s', parte);
        EXECUTE format('ALTER TABLE %s NO FORCE ROW LEVEL SECURITY', parte);
        EXECUTE format('ALTER TABLE %s DISABLE ROW LEVEL SECURITY', parte);
    END LOOP;
END
$$;
-- +goose StatementEnd

DROP FUNCTION protect_checks_partition(regclass);
