-- +goose Up
-- Regra de conteúdo do alvo HTTP (sp1-conteudo): contains, not_contains ou
-- json_equals num ponteiro RFC 6901. As três colunas são anuláveis e sem
-- default: o binário anterior não as cita e segue inserindo alvos sem regra.
--
-- lock_timeout de 5 s (Ruling): a migration roda no boot, sob o tráfego do
-- sondador. Sem prazo, o ALTER esperaria atrás de uma leitura longa e
-- enfileiraria todo INSERT de checks atrás do seu ACCESS EXCLUSIVE. Com 5 s,
-- o pior caso é um tique do sondador atrasado; um prazo curto (abaixo de
-- 500 ms) aumentaria a chance de o boot cair com 55P03 no deploy, e uma
-- falha de release para tudo e exige intervenção manual. Se a trava não vier,
-- a transação desfaz tudo, goose_db_version não muda e o próximo boot tenta
-- de novo.
SET LOCAL lock_timeout = '5s';

ALTER TABLE targets
    ADD COLUMN content_mode text
        CHECK (content_mode IN ('contains', 'not_contains', 'json_equals')),
    -- Sem caractere de controle (C0, DEL e C1), como o cadastro: é o mesmo
    -- conjunto do unicode.IsControl do Go.
    ADD COLUMN content_value text
        CHECK (char_length(content_value) BETWEEN 1 AND 256
               AND content_value !~ '[\u0001-\u001f\u007f-\u009f]'),
    -- Forma RFC 6901: "~" só como ~0 ou ~1. Um ponteiro que o sondador não lê
    -- deixaria o alvo parado no último estado, sem check e sem aviso (L4 do
    -- STRIDE do #255). Sem controle, como o valor e o cadastro.
    ADD COLUMN content_json_pointer text
        CHECK (char_length(content_json_pointer) BETWEEN 1 AND 256 AND left(content_json_pointer, 1) = '/'
               AND content_json_pointer !~ '~([^01]|$)'
               AND content_json_pointer !~ '[\u0001-\u001f\u007f-\u009f]');

-- Tudo ou nada. Precisa ser CASE: com (content_mode IS NULL) = (... IS NULL)
-- o ponteiro de um contains passaria, e com '=' sobre NULL o CHECK daria NULL,
-- que o Postgres aceita.
ALTER TABLE targets
    ADD CONSTRAINT targets_content_rule_shape CHECK (CASE
        WHEN content_mode IS NULL THEN content_value IS NULL AND content_json_pointer IS NULL
        WHEN content_mode = 'json_equals' THEN content_value IS NOT NULL AND content_json_pointer IS NOT NULL
        ELSE content_value IS NOT NULL AND content_json_pointer IS NULL END),
    -- HEAD não tem corpo: a regra nunca casaria.
    ADD CONSTRAINT targets_content_rule_not_head CHECK (content_mode IS NULL OR method <> 'HEAD');

-- Portão da Task 1 (FORMA B), medido em PG 18 com 17.568.000 checks: trocar a
-- constraint validada (forma A) segurou ACCESS EXCLUSIVE durante a varredura,
-- com ALTER de 2,549 s e INSERT do sondador parado 2,523 s; NOT VALID (forma
-- B) levou 41 ms, com o maior INSERT em 32 ms. PG 17 e 18 propagam o NOT
-- VALID às partições existentes e às criadas depois (convalidated = false).
-- Sem VALIDATE (Ruling): as linhas antigas já obedecem à lista anterior, que é
-- mais estrita; um VALIDATE futuro levou 1,876 s sem bloquear INSERTs.
ALTER TABLE checks
    DROP CONSTRAINT checks_error_kind_check,
    ADD CONSTRAINT checks_error_kind_check
        CHECK (error_kind IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                              'blocked', 'unknown', 'content_mismatch', 'content_encoding')) NOT VALID;

-- incidents é pequena: a troca validada direta não pesa.
ALTER TABLE incidents
    DROP CONSTRAINT incidents_cause_check,
    ADD CONSTRAINT incidents_cause_check
        CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                         'blocked', 'unknown', 'heartbeat_late', 'heartbeat_failed', 'content_mismatch',
                         'content_encoding'));

-- +goose Down
-- Resíduo do rollback: content_mismatch e content_encoding viram 'unknown' (o
-- binário anterior não conhece outro motivo) e não voltam na subida; as
-- regras dos alvos se perdem com as colunas. O UPDATE de checks varre a
-- tabela inteira (não há índice em error_kind), o que só acontece em quem
-- desce de propósito. O esquema não volta byte a byte:
-- checks_error_kind_check fica NOT VALID, e a 00004 o criou validado.
--
-- Piso sem Down (o binário anterior sobre este esquema): ele não conhece a
-- regra, então um PATCH que troque o método de um alvo com regra para HEAD
-- bate em targets_content_rule_not_head (23514), e o binário anterior
-- provavelmente responde 500.
SET LOCAL lock_timeout = '5s';
-- Roda com o papel da aplicação, sujeito à RLS: sem o escopo do sondador, os
-- UPDATEs não enxergariam linha nenhuma, e o CHECK antigo de incidents falharia.
SELECT set_config('app.scope', 'prober', true);
UPDATE checks SET error_kind = 'unknown' WHERE error_kind IN ('content_mismatch', 'content_encoding');
UPDATE incidents SET cause = 'unknown' WHERE cause IN ('content_mismatch', 'content_encoding');

-- targets antes de checks, como no Up e no sondador (FOR KEY SHARE no alvo,
-- depois INSERT em checks). Na ordem inversa, um sondador que já segura o alvo
-- e espera checks formaria um deadlock com este ALTER.
ALTER TABLE targets
    DROP CONSTRAINT targets_content_rule_not_head,
    DROP CONSTRAINT targets_content_rule_shape,
    DROP COLUMN content_json_pointer,
    DROP COLUMN content_value,
    DROP COLUMN content_mode;

ALTER TABLE checks
    DROP CONSTRAINT checks_error_kind_check,
    ADD CONSTRAINT checks_error_kind_check
        CHECK (error_kind IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                              'blocked', 'unknown')) NOT VALID;

ALTER TABLE incidents
    DROP CONSTRAINT incidents_cause_check,
    ADD CONSTRAINT incidents_cause_check
        CHECK (cause IN ('timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit',
                         'blocked', 'unknown', 'heartbeat_late', 'heartbeat_failed'));
