-- +goose Up
-- Rollup horário dos checks (spec §4.1, §9.1, §12): o que sobra de um alvo
-- depois que a partição bruta vence. ok conta os checks que não foram down;
-- as latências são só desses, e ficam NULL numa hora sem nenhum sucesso.
CREATE TABLE check_rollup_1h (
    target_id uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    bucket    timestamptz NOT NULL,
    total     int NOT NULL CHECK (total > 0),
    ok        int NOT NULL CHECK (ok BETWEEN 0 AND total),
    p50       int,
    p95       int,
    p99       int,
    max       int,
    PRIMARY KEY (target_id, bucket),
    -- A hora cheia em UTC. date_trunc em timestamptz usa o TimeZone da
    -- sessão, e num fuso de meia hora (Índia) a "hora cheia" seria outra.
    CHECK (extract(epoch FROM bucket)::bigint % 3600 = 0),
    CHECK ((ok = 0) = (p50 IS NULL))
);

-- Retenção (DELETE por bucket) e marca d'água da consolidação (max(bucket)).
CREATE INDEX check_rollup_bucket_idx ON check_rollup_1h (bucket);

ALTER TABLE check_rollup_1h ENABLE ROW LEVEL SECURITY;
ALTER TABLE check_rollup_1h FORCE ROW LEVEL SECURITY;
CREATE POLICY check_rollup_via_target ON check_rollup_1h
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = check_rollup_1h.target_id));

-- Status page (spec §4.1, §10). Uma por usuário (spec §2.1): o UNIQUE em
-- user_id é a garantia, e a API responde 409 na segunda.
CREATE TABLE status_pages (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    -- 3 a 40 caracteres, minúsculas, dígitos e hífen, sem hífen nas pontas.
    -- Vira caminho de URL (/p/{slug}), e por isso a regra fica também no banco.
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    published  bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);

ALTER TABLE status_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_pages FORCE ROW LEVEL SECURITY;
CREATE POLICY status_pages_owner ON status_pages
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
-- Escopo público (storage.InPublicTx): enxerga só a página PUBLICADA daquele
-- slug. Rascunho e página de outro slug não existem para ele.
CREATE POLICY status_pages_public_select ON status_pages FOR SELECT
    USING (published AND slug = NULLIF(current_setting('app.public_slug', true), ''));

CREATE TABLE status_page_items (
    status_page_id uuid NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    target_id      uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    display_name   text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 100),
    position       int  NOT NULL CHECK (position >= 0),
    PRIMARY KEY (status_page_id, target_id),
    UNIQUE (status_page_id, position)
);

CREATE INDEX status_page_items_target_idx ON status_page_items (target_id);

-- O USING delega à página e NÃO olha targets: a política de targets abaixo
-- consulta esta tabela, e as duas se referindo uma à outra seria recursão
-- infinita. O WITH CHECK olha o alvo: a FK ignora RLS, e sem esta checagem o
-- dono poderia pôr na própria página o alvo de outro usuário.
ALTER TABLE status_page_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_page_items FORCE ROW LEVEL SECURITY;
CREATE POLICY status_page_items_via_page ON status_page_items
    USING (EXISTS (SELECT 1 FROM status_pages p WHERE p.id = status_page_items.status_page_id))
    WITH CHECK (EXISTS (SELECT 1 FROM status_pages p WHERE p.id = status_page_items.status_page_id)
                AND EXISTS (SELECT 1 FROM targets t WHERE t.id = status_page_items.target_id));

-- O escopo público enxerga os alvos da página publicada daquele slug, e por
-- delegação os checks, rollups e incidentes deles. Só SELECT, e o escopo
-- público só existe em transação READ ONLY: as políticas FOR ALL que delegam
-- a targets (incidents, checks) não viram porta de escrita.
--
-- A consulta aos itens fica numa função plpgsql, e não num EXISTS direto na
-- política. O WITH CHECK dos itens olha targets, e targets olhando os itens
-- fecharia um ciclo: o Postgres recusa com "infinite recursion detected in
-- policy" (42P17, provado em spike) ao expandir as políticas. Uma função
-- plpgsql não é expandida na reescrita; ela roda com o papel de quem chama,
-- e a RLS dos itens vale dentro dela.
--
-- A função devolve o CONJUNTO de alvos da página, e a política pergunta
-- "id IN (conjunto)": o Postgres a roda uma vez por consulta (hashed
-- SubPlan). Uma função booleana por alvo rodaria uma vez para cada alvo da
-- instância, de qualquer usuário, e o custo da página pública cresceria com
-- o total de alvos (achado da revisão: ~0,5 s com 5 mil alvos).
--
-- SECURITY INVOKER sem search_path fixo, de propósito: a função roda com os
-- privilégios de quem chama, e sombrear status_page_items com uma tabela
-- temporária exige executar SQL na própria sessão. Não há fronteira de
-- privilégio a cruzar; isso só importaria numa SECURITY DEFINER.
-- +goose StatementBegin
CREATE FUNCTION public_page_target_ids() RETURNS SETOF uuid
LANGUAGE plpgsql STABLE AS $$
BEGIN
    RETURN QUERY SELECT i.target_id FROM status_page_items i;
END
$$;
-- +goose StatementEnd

CREATE POLICY targets_public_select ON targets FOR SELECT
    USING (NULLIF(current_setting('app.public_slug', true), '') IS NOT NULL
           AND targets.id IN (SELECT public_page_target_ids()));

-- Saúde do canal (pendência do Plano 3): o dispatcher grava o último sucesso
-- e a última falha definitiva, e a API mostra "failing". Tabela à parte, como
-- target_health: o sondador não ganha UPDATE em notif_channels.
CREATE TABLE channel_health (
    channel_id      uuid PRIMARY KEY REFERENCES notif_channels(id) ON DELETE CASCADE,
    last_success_at timestamptz,
    last_failure_at timestamptz,
    last_error      text CHECK (last_error IN ('timeout', 'network', 'rate_limited', 'rejected',
                                               'server_error', 'invalid_channel', 'unknown'))
);

ALTER TABLE channel_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_health FORCE ROW LEVEL SECURITY;
-- Como notifications: o canal visível NÃO basta (o escopo verify o vê).
CREATE POLICY channel_health_owner_or_prober ON channel_health
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                   AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                        OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = channel_health.channel_id
                        AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                             OR current_setting('app.scope', true) = 'prober')));

-- Retenção das notificações entregues, falhas e canceladas (created_at < corte).
CREATE INDEX notifications_created_idx ON notifications (created_at) WHERE status <> 'pending';
-- Retenção de audit_log (12 meses) e de sessões vencidas.
CREATE INDEX audit_log_at_idx ON audit_log (at);

-- Retenção de checks (spec §12): apaga as partições mensais que terminam
-- antes de p_cutoff. A partição que contém o corte fica, então os checks
-- duram entre 30 e 61 dias. Mesma trava de ensure_checks_partition: criar e
-- apagar partição nunca correm juntos.
-- +goose StatementBegin
CREATE FUNCTION drop_checks_partitions_before(p_cutoff timestamptz) RETURNS int
LANGUAGE plpgsql AS $$
DECLARE
    parte  record;
    inicio date;
    apagadas int := 0;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('stillon.ensure_checks_partition', 0));
    FOR parte IN
        SELECT c.relname FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'checks'::regclass
           AND c.relname ~ '^checks_[0-9]{4}_[0-9]{2}$'
    LOOP
        inicio := to_date(substr(parte.relname, 8), 'YYYY_MM');
        IF ((inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC') <= p_cutoff THEN
            EXECUTE format('DROP TABLE %I', parte.relname);
            apagadas := apagadas + 1;
        END IF;
    END LOOP;
    RETURN apagadas;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION drop_checks_partitions_before(timestamptz);
DROP INDEX audit_log_at_idx;
DROP INDEX notifications_created_idx;
DROP TABLE channel_health;
DROP POLICY targets_public_select ON targets;
DROP FUNCTION public_page_target_ids();
DROP TABLE status_page_items;
DROP TABLE status_pages;
DROP TABLE check_rollup_1h;
