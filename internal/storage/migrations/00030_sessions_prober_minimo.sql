-- +goose Up
-- O escopo do sondador (app.scope = 'prober') em sessions fica no mínimo que
-- a retenção usa. A 00029 deixava ler e apagar qualquer sessão; o código só
-- precisa de duas coisas:
--
--   DeleteExpiredSessions    apaga sessão vencida há mais de SessionGrace.
--   DeleteDeadFamilyProofs   o NOT EXISTS lê as sessões das famílias que têm
--                            contador de provas, para nunca apagar o de uma
--                            família viva.
--
-- Então o sondador lê a sessão vencida e a de família com contador, e apaga só
-- a vencida. A sessão viva fica intocável. O corte é o now() do banco, mais
-- largo que o da aplicação (now - SessionGrace): a política é o teto, a query
-- refina. São duas políticas de leitura para que a retenção das vencidas não
-- dependa de session_family_proofs existir; as dela não referenciam sessions,
-- então o EXISTS não recursa.
SET LOCAL lock_timeout = '5s';

DROP POLICY sessions_prober_select ON sessions;
DROP POLICY sessions_prober_delete ON sessions;

CREATE POLICY sessions_prober_select_expired ON sessions FOR SELECT
    USING (current_setting('app.scope', true) = 'prober' AND expires_at < now());
CREATE POLICY sessions_prober_select_proof_family ON sessions FOR SELECT
    USING (current_setting('app.scope', true) = 'prober'
           AND EXISTS (SELECT 1 FROM session_family_proofs p
                        WHERE p.user_id = sessions.user_id
                          AND p.family_id = sessions.family_id));
CREATE POLICY sessions_prober_delete ON sessions FOR DELETE
    USING (current_setting('app.scope', true) = 'prober' AND expires_at < now());

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP POLICY sessions_prober_delete ON sessions;
DROP POLICY IF EXISTS sessions_prober_select_proof_family ON sessions;
DROP POLICY sessions_prober_select_expired ON sessions;

CREATE POLICY sessions_prober_select ON sessions FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY sessions_prober_delete ON sessions FOR DELETE
    USING (current_setting('app.scope', true) = 'prober');
