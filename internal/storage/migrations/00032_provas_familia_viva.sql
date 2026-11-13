-- +goose Up
-- O sondador (app.scope = 'prober') só apaga o contador de provas de família
-- morta. A 00029 deixava apagar qualquer linha de session_family_proofs, e
-- apagar o contador de uma família viva zera o limite de provas dela: quem
-- esgotou as tentativas ganharia outra rodada. A retenção
-- (DeleteDeadFamilyProofs) já filtra pela ausência de sessão viva; a política
-- passa a ser o teto do mesmo critério, então nem um DELETE direto, sem o
-- filtro da query, toca a família ativa.
--
-- O corte é o now() do banco. A query compara com o relógio da aplicação; a
-- diferença só pesa para sessão a segundos de vencer, e nesse caso a
-- política só adia a limpeza para a próxima rodada.
--
-- O used_at e o revoked_at repetem o que a sessions_prober_select_proof_family
-- já corta (o sondador não vê sessão usada ou revogada que não venceu); ficam
-- explícitos para a política não depender do desenho da outra.
--
-- Só a política de DELETE. A de SELECT não pode citar sessions: a
-- sessions_prober_select_proof_family (00030) lê esta tabela de volta, e a
-- referência mútua dá recursão de política (42P17). Aqui não recursa porque o
-- NOT EXISTS lê sessions pelas políticas de SELECT dela, e essas leem
-- session_family_proofs pela política de SELECT, que não cita sessions.
SET LOCAL lock_timeout = '5s';

DROP POLICY session_family_proofs_prober_delete ON session_family_proofs;

CREATE POLICY session_family_proofs_prober_delete ON session_family_proofs FOR DELETE
    USING (current_setting('app.scope', true) = 'prober'
           AND NOT EXISTS (SELECT 1 FROM sessions s
                            WHERE s.user_id = session_family_proofs.user_id
                              AND s.family_id = session_family_proofs.family_id
                              AND s.used_at IS NULL
                              AND s.revoked_at IS NULL
                              AND s.expires_at > now()));

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP POLICY IF EXISTS session_family_proofs_prober_delete ON session_family_proofs;

CREATE POLICY session_family_proofs_prober_delete ON session_family_proofs FOR DELETE
    USING (current_setting('app.scope', true) = 'prober');
