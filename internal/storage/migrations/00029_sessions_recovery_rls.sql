-- +goose Up
-- RLS forçada em sessions, session_family_proofs e totp_recovery_codes, as
-- três tabelas de credencial que só tinham o WHERE das consultas como
-- barreira. Os escopos (ver storage/scope.go e queries/scope.sql):
--
--   app.user_id               dono: tudo nas linhas do próprio usuário.
--   app.session_token_hash    pré-autenticação (refresh e logout): só LÊ a
--                             linha de sessions daquele hash, para descobrir o
--                             dono. Toda escrita vem depois, já com app.user_id.
--   app.scope = 'prober'      retenção e métrica: lê e APAGA sessions e
--                             session_family_proofs de todos. Não insere nem
--                             altera, e não vê totp_recovery_codes.
--
-- FORCE porque a aplicação é dona das tabelas; NULLIF pelo motivo da 00004.
--
-- lock_timeout de 5 s, como na 00028: ENABLE pega ACCESS EXCLUSIVE, e sem prazo
-- uma leitura longa enfileiraria todo login atrás da migration. Sem a trava, a
-- transação desfaz tudo e o próximo boot tenta de novo.
SET LOCAL lock_timeout = '5s';

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY sessions_owner ON sessions
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

CREATE POLICY sessions_by_token_select ON sessions FOR SELECT
    USING (token_hash = decode(NULLIF(current_setting('app.session_token_hash', true), ''), 'hex'));

CREATE POLICY sessions_prober_select ON sessions FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY sessions_prober_delete ON sessions FOR DELETE
    USING (current_setting('app.scope', true) = 'prober');

ALTER TABLE session_family_proofs ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_family_proofs FORCE ROW LEVEL SECURITY;

CREATE POLICY session_family_proofs_owner ON session_family_proofs
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

CREATE POLICY session_family_proofs_prober_select ON session_family_proofs FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY session_family_proofs_prober_delete ON session_family_proofs FOR DELETE
    USING (current_setting('app.scope', true) = 'prober');

ALTER TABLE totp_recovery_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE totp_recovery_codes FORCE ROW LEVEL SECURITY;

CREATE POLICY totp_recovery_codes_owner ON totp_recovery_codes
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP POLICY totp_recovery_codes_owner ON totp_recovery_codes;
ALTER TABLE totp_recovery_codes NO FORCE ROW LEVEL SECURITY;
ALTER TABLE totp_recovery_codes DISABLE ROW LEVEL SECURITY;

DROP POLICY session_family_proofs_prober_delete ON session_family_proofs;
DROP POLICY session_family_proofs_prober_select ON session_family_proofs;
DROP POLICY session_family_proofs_owner ON session_family_proofs;
ALTER TABLE session_family_proofs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE session_family_proofs DISABLE ROW LEVEL SECURITY;

DROP POLICY sessions_prober_delete ON sessions;
DROP POLICY sessions_prober_select ON sessions;
DROP POLICY sessions_by_token_select ON sessions;
DROP POLICY sessions_owner ON sessions;
ALTER TABLE sessions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE sessions DISABLE ROW LEVEL SECURITY;
