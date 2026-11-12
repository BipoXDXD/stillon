-- Contador de provas erradas por família de sessão (#324). session_family_proofs
-- não tem RLS: as rotas autenticadas contam por família sem app.user_id na
-- transação, e a tabela guarda só ids e contagens. Toda query daqui é uma
-- instrução única, e é isso que a torna atômica: nada de ler e depois gravar.

-- Reserva uma prova antes de verificar. O INSERT só nasce para uma família
-- viva (a mesma definição de FamilyIsLive), então quem chega depois da
-- revogação não recria a linha que a varredura apagou; sem linha devolvida, a
-- família morreu. Se a linha existe, a reserva a soma, e
-- RETURNING diz qual geração e qual posição ela ganhou.
-- name: ReserveFamilyProof :one
INSERT INTO session_family_proofs AS p (family_id, user_id, generation, reserved, errors, updated_at)
SELECT sqlc.arg(family_id)::uuid, sqlc.arg(user_id)::uuid, sqlc.arg(generation)::uuid, 1, 0, sqlc.arg(now)::timestamptz
 WHERE EXISTS (
    SELECT 1 FROM sessions s
     WHERE s.user_id = sqlc.arg(user_id)::uuid AND s.family_id = sqlc.arg(family_id)::uuid
       AND s.used_at IS NULL AND s.revoked_at IS NULL
       AND s.expires_at > sqlc.arg(now)::timestamptz
 )
ON CONFLICT (family_id) DO UPDATE
   SET reserved = p.reserved + 1, updated_at = excluded.updated_at
RETURNING generation, reserved;

-- A prova errada: conta o erro da geração da reserva. Sem linha, a geração
-- acabou (sucesso ou revogação) e o erro não conta.
-- name: FailFamilyProof :one
UPDATE session_family_proofs SET errors = errors + 1, updated_at = sqlc.arg(now)::timestamptz
 WHERE family_id = sqlc.arg(family_id) AND generation = sqlc.arg(generation)
RETURNING errors;

-- A prova conferiu: zera a família. As reservas em voo da mesma geração
-- deixam de casar.
-- name: ClearFamilyProofs :exec
DELETE FROM session_family_proofs
 WHERE family_id = sqlc.arg(family_id) AND generation = sqlc.arg(generation);

-- Devolve a reserva de quem não chegou a errar. Nunca desce do número de erros.
-- name: ReleaseFamilyProof :exec
UPDATE session_family_proofs SET reserved = reserved - 1
 WHERE family_id = sqlc.arg(family_id) AND generation = sqlc.arg(generation)
   AND reserved > errors;

-- A varredura, a cada reserva: apaga a conta da família parada (ninguém a usa
-- há um dia; o access token vive 15 min e o refresh o renova) e a da família
-- que já não está viva (revogada ou vencida) depois de uma folga, em que as
-- provas em voo ainda a usam para contar. É assim que a revogação limpa o
-- contador: nada de gatilho em sessions, que apagaria a linha antes das provas
-- em voo.
-- name: SweepFamilyProofs :exec
DELETE FROM session_family_proofs p
 WHERE p.updated_at < sqlc.arg(idle_before)::timestamptz
    OR (p.updated_at < sqlc.arg(dead_before)::timestamptz
        AND NOT EXISTS (
            SELECT 1 FROM sessions s
             WHERE s.user_id = p.user_id AND s.family_id = p.family_id
               AND s.used_at IS NULL AND s.revoked_at IS NULL
               AND s.expires_at > sqlc.arg(now)::timestamptz
        ));

-- O tamanho da tabela, para a métrica.
-- name: CountFamilyProofs :one
SELECT count(*) FROM session_family_proofs;
