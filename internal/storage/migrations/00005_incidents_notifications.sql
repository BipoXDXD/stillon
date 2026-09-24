-- +goose Up
-- Histerese por alvo (spec §7): uma linha por alvo, criada na primeira
-- sonda gravada. Vive fora de targets para que a gravação do sondador não
-- dispute a linha do alvo com o PATCH do dono.
CREATE TABLE target_health (
    target_id             uuid PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,
    consecutive_failures  int NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    consecutive_successes int NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0),
    streak_since          timestamptz,
    last_checked_at       timestamptz
);

-- Sem user_id: a RLS delega ao alvo, como em checks (migration 00004).
ALTER TABLE target_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE target_health FORCE ROW LEVEL SECURITY;
CREATE POLICY target_health_via_target ON target_health
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_health.target_id));

-- Incidentes (spec §4.1). cause usa o mesmo enum de checks.error_kind.
CREATE TABLE incidents (
    id                uuid PRIMARY KEY,
    target_id         uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    started_at        timestamptz NOT NULL,
    resolved_at       timestamptz,
    cause             text NOT NULL CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused',
                                                     'http_status', 'body_limit', 'blocked', 'unknown')),
    ack_at            timestamptz,
    -- Instante em que o fan-out da abertura e o do fechamento foram
    -- enfileirados em notifications (spec §8, D6 do Plano 3).
    open_notified_at  timestamptz,
    close_notified_at timestamptz,
    CHECK (resolved_at IS NULL OR resolved_at >= started_at)
);

-- No máximo um incidente aberto por alvo. O detector já não abre dois, e
-- o índice é a garantia que não depende do código estar certo.
CREATE UNIQUE INDEX incidents_one_open_per_target ON incidents (target_id) WHERE resolved_at IS NULL;
CREATE INDEX incidents_target_idx ON incidents (target_id, id DESC);

ALTER TABLE incidents ENABLE ROW LEVEL SECURITY;
ALTER TABLE incidents FORCE ROW LEVEL SECURITY;
CREATE POLICY incidents_via_target ON incidents
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = incidents.target_id));

-- Canais de notificação (spec §4.1, §5.8). config_enc guarda a URL do
-- webhook ou o endereço de e-mail, cifrados com AAD da linha (spec §5.4).
CREATE TABLE notif_channels (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind              text NOT NULL CHECK (kind IN ('discord', 'email')),
    config_enc        bytea NOT NULL,
    verified_at       timestamptz,
    verify_token_hash bytea UNIQUE,
    verify_expires_at timestamptz,
    enabled           bool NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL,
    CHECK ((verify_token_hash IS NULL) = (verify_expires_at IS NULL)),
    -- Só e-mail tem token: o Discord é verificado pela mensagem de teste.
    CHECK (kind = 'email' OR verify_token_hash IS NULL)
);

CREATE INDEX notif_channels_user_idx ON notif_channels (user_id, id DESC);

ALTER TABLE notif_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE notif_channels FORCE ROW LEVEL SECURITY;

CREATE POLICY notif_channels_owner ON notif_channels
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- O sondador (e o dispatcher, que roda no mesmo papel) lê canais para o
-- fan-out e para o envio, e nada mais: sem INSERT, UPDATE nem DELETE.
CREATE POLICY notif_channels_prober_select ON notif_channels FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');

-- Escopo de verificação: GET/POST /verify/{token} não tem usuário, só o
-- token. A transação liga app.verify_token_hash (storage.InVerifyTx) e
-- enxerga exatamente a linha daquele hash. decode(NULL) é NULL, então sem o
-- GUC, ou com ele vazio numa conexão reusada, a política não casa nada.
--
-- A confirmação MANTÉM o hash na linha (query ConfirmChannel). Anulá-lo
-- aqui não funciona: num UPDATE, a linha NOVA também precisa passar pela
-- política de SELECT, e uma linha sem hash deixa de casar. O Postgres
-- responde "new row violates row-level security policy" (provado em spike).
-- O uso único fica por conta de verified_at.
--
-- Limite conhecido: a política diz QUAL linha, não QUAIS colunas. Neste
-- escopo, um UPDATE arbitrário poderia mudar o user_id da linha do token.
-- A trava é existir uma query só que roda nele.
CREATE POLICY notif_channels_verify_select ON notif_channels FOR SELECT
    USING (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'));
CREATE POLICY notif_channels_verify_update ON notif_channels FOR UPDATE
    USING (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'))
    WITH CHECK (verify_token_hash = decode(NULLIF(current_setting('app.verify_token_hash', true), ''), 'hex'));

-- Outbox das notificações (decisão do usuário, 2026-09-24): o sondador
-- grava aqui, na mesma transação que abre ou fecha o incidente, e o
-- dispatcher consome com SKIP LOCKED. Sobrevive a restart e funciona com
-- --role=api e --role=prober separados.
CREATE TABLE notifications (
    id              uuid PRIMARY KEY,
    incident_id     uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES notif_channels(id) ON DELETE CASCADE,
    event           text NOT NULL CHECK (event IN ('opened', 'resolved')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'cancelled')),
    attempts        int  NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- Próxima tentativa. Enquanto um dispatcher envia, vale como lease: a
    -- reivindicação o empurra para o futuro, e um processo que morre no
    -- meio devolve a notificação à fila quando o lease vence.
    next_attempt_at timestamptz NOT NULL,
    last_error      text CHECK (last_error IN ('timeout', 'network', 'rate_limited', 'rejected',
                                               'server_error', 'invalid_channel', 'unknown')),
    created_at      timestamptz NOT NULL,
    sent_at         timestamptz,
    -- Spec §8: no máximo uma notificação por incidente, canal e evento.
    UNIQUE (incident_id, channel_id, event)
);

CREATE INDEX notifications_due_idx ON notifications (next_attempt_at) WHERE status = 'pending';
-- O ON DELETE CASCADE de apagar um canal varreria a outbox inteira sem este índice.
CREATE INDEX notifications_channel_idx ON notifications (channel_id);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
-- A delegação não pode ser só "o canal existe": o canal também é visível no
-- escopo de verificação (notif_channels_verify_select), e um e-mail já
-- verificado mantém o hash para sempre (D3). Sem nomear o dono ou o
-- sondador aqui, o escopo verify alcançaria as notificações daquele canal.
CREATE POLICY notifications_via_channel ON notifications
    USING (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')))
    WITH CHECK (EXISTS (SELECT 1 FROM notif_channels c WHERE c.id = notifications.channel_id
                      AND (c.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));

-- Dívida do Plano 2: CREATE TABLE IF NOT EXISTS ... PARTITION OF não é
-- seguro com dois boots concorrentes (os dois veem "não existe" e o segundo
-- cai com 42P07). A advisory lock de transação serializa as chamadas; ela
-- é solta no fim da TRANSAÇÃO que chamou a função (pg_advisory_xact_lock),
-- não no fim do comando.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('vigilume.ensure_checks_partition', 0));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_checks_partition(p_month date) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    inicio date := date_trunc('month', p_month)::date;
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF checks FOR VALUES FROM (%L) TO (%L)',
        'checks_' || to_char(inicio, 'YYYY_MM'),
        inicio::timestamp AT TIME ZONE 'UTC',
        (inicio + interval '1 month')::timestamp AT TIME ZONE 'UTC');
END
$$;
-- +goose StatementEnd
DROP TABLE notifications;
DROP TABLE notif_channels;
DROP TABLE incidents;
DROP TABLE target_health;
