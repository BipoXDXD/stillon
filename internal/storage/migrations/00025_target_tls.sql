-- +goose Up
-- O ADD COLUMN abaixo é só metadado, mas pede ACCESS EXCLUSIVE em targets.
-- Com uma transação longa segurando a tabela, ele esperaria e enfileiraria
-- atrás de si o painel e o sondador; com o prazo, falha em 5 s e o boot tenta
-- de novo. A migration roda numa transação (sem NO TRANSACTION), por isso o
-- SET LOCAL vale só para ela.
SET LOCAL lock_timeout = '5s';

-- Se o dono quer o aviso de certificado perto de vencer. Ligado por padrão:
-- certificado vencido derruba o site do mesmo jeito que o servidor fora do ar.
-- O binário anterior não cita a coluna e segue inserindo com o padrão.
ALTER TABLE targets ADD COLUMN tls_warn bool NOT NULL DEFAULT true;

-- A validade efetiva do pior certificado visto no host cadastrado, uma linha
-- por alvo. Em produção, só o sondador insere e atualiza; o dono apaga quando
-- a origem muda. A regra de substituição é a de prober/certificado.go.
CREATE TABLE target_tls (
    target_id   uuid PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,
    not_after   timestamptz NOT NULL,
    observed_at timestamptz NOT NULL
);

-- Nomeia o dono e o sondador, como incidents_via_heartbeat, em vez de delegar
-- só ao alvo, como target_health: targets_public_select mostra os alvos da
-- status page ao escopo público, e a validade do certificado não faz parte da
-- página.
ALTER TABLE target_tls ENABLE ROW LEVEL SECURITY;
ALTER TABLE target_tls FORCE ROW LEVEL SECURITY;
-- Ler e apagar: o dono (o PATCH que troca o host apaga a leitura antiga) ou o
-- sondador.
CREATE POLICY target_tls_owner_or_prober_select ON target_tls FOR SELECT
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
CREATE POLICY target_tls_owner_or_prober_delete ON target_tls FOR DELETE
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                      AND (t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                           OR current_setting('app.scope', true) = 'prober')));
-- Gravar: só o sondador. O dono não escolhe not_after, senão escolheria quando
-- o aviso de vencimento dispara.
CREATE POLICY target_tls_prober_insert ON target_tls FOR INSERT
    WITH CHECK (current_setting('app.scope', true) = 'prober');
CREATE POLICY target_tls_prober_update ON target_tls FOR UPDATE
    USING (current_setting('app.scope', true) = 'prober')
    WITH CHECK (current_setting('app.scope', true) = 'prober');

-- +goose Down
-- Sem dado a converter: a tabela e a coluna somem inteiras, e o escopo do
-- sondador não é preciso. O Down perde a escolha tls_warn = false do dono (um
-- Up depois volta tudo para true), por isso o rollback desta versão é só de
-- binário, sem goose down: o binário anterior funciona neste schema
-- (docs/deploy/vps.md, "Voltar uma versão").
DROP TABLE target_tls;
ALTER TABLE targets DROP COLUMN tls_warn;
