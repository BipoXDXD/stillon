-- Checagens da cópia restaurada. Rodam no banco restaurado, como
-- superusuário, com as variáveis do psql que backup.sh lê da ORIGEM:
-- :versao (a maior versão do goose), :usuarios e :alvos (contagens).
-- Qualquer RAISE derruba o psql (ON_ERROR_STOP) e o job fica vermelho.
\set ON_ERROR_STOP on
\o /dev/null
SELECT set_config('verificar.versao', :'versao', false),
       set_config('verificar.usuarios', :'usuarios', false),
       set_config('verificar.alvos', :'alvos', false);
\o

DO $$
DECLARE
    alheias   text;
    sem_force text;
    com_rls   int;
    versao    bigint;
    n         bigint;
BEGIN
    -- 1. Dono: a aplicação só roda como dona das tabelas (D30). Restaurar com
    --    --no-owner deixaria tudo com o superusuário, e o boot seguinte
    --    falharia no Migrate ou na manutenção.
    SELECT string_agg(tablename, ', ' ORDER BY tablename) INTO alheias
      FROM pg_tables WHERE schemaname = 'public' AND tableowner <> 'vigilume_app';
    IF alheias IS NOT NULL THEN
        RAISE EXCEPTION 'tabelas sem o dono vigilume_app: %', alheias;
    END IF;

    -- 2. RLS: toda tabela com política continua com FORCE depois da volta.
    SELECT count(*) FILTER (WHERE c.relrowsecurity),
           string_agg(c.relname, ', ') FILTER (WHERE c.relrowsecurity AND NOT c.relforcerowsecurity)
      INTO com_rls, sem_force
      FROM pg_class c JOIN pg_namespace ns ON ns.oid = c.relnamespace
     WHERE ns.nspname = 'public' AND c.relkind IN ('r', 'p');
    IF com_rls = 0 THEN
        RAISE EXCEPTION 'nenhuma tabela com row-level security: a restauração perdeu as políticas';
    END IF;
    IF sem_force IS NOT NULL THEN
        RAISE EXCEPTION 'tabelas com RLS sem FORCE: %', sem_force;
    END IF;

    -- 3. Schema: a mesma versão da origem.
    SELECT max(version_id) INTO versao FROM goose_db_version;
    IF versao IS DISTINCT FROM current_setting('verificar.versao')::bigint THEN
        RAISE EXCEPTION 'versão do schema restaurado = %, origem = %', versao, current_setting('verificar.versao');
    END IF;

    -- 4. Dados: as tabelas que mudam devagar voltam inteiras. A contagem da
    --    origem é de ANTES do dump (outro instante): um alvo cadastrado no meio
    --    entra no dump e não na contagem. Por isso ">=", e não "=". (checks
    --    muda a cada minuto e não entra.)
    SELECT count(*) INTO n FROM users;
    IF n < 1 OR n < current_setting('verificar.usuarios')::bigint THEN
        RAISE EXCEPTION 'users restaurado = %, origem = %', n, current_setting('verificar.usuarios');
    END IF;
    SELECT count(*) INTO n FROM targets;
    IF n < current_setting('verificar.alvos')::bigint THEN
        RAISE EXCEPTION 'targets restaurado = %, origem = %', n, current_setting('verificar.alvos');
    END IF;

    -- 5. Partições de checks: sem elas o sondador não grava.
    SELECT count(*) INTO n FROM pg_inherits WHERE inhparent = 'public.checks'::regclass;
    IF n < 1 THEN
        RAISE EXCEPTION 'checks restaurado sem partições';
    END IF;

    RAISE NOTICE 'cópia conferida: schema %, % usuário(s), % alvo(s), % tabela(s) com RLS',
        versao, current_setting('verificar.usuarios'), current_setting('verificar.alvos'), com_rls;
END
$$;
