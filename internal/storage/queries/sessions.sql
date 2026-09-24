-- Sessões (spec §5.2). sessions não tem RLS (D7 do Plano 2): login e refresh
-- a consultam por e-mail e por hash, antes de existir usuário na transação.
-- Toda query daqui que parte de um usuário filtra por user_id.

-- O dono de um refresh, lido SEM trava: serve só para saber qual trava de
-- usuário pegar antes de travar a linha (D1 do Plano 5).
-- name: SessionUserByHash :one
SELECT user_id FROM sessions WHERE token_hash = $1;

-- Serializa as mutações de sessão de um usuário (D1 do Plano 5). É de
-- TRANSAÇÃO: sai sozinha no commit ou no rollback. Toda transação que mexe em
-- linhas de sessions a pega ANTES de qualquer trava de linha, e é isso que
-- impede o ciclo (deadlock) entre rotação e revogação.
-- name: LockUserSessions :exec
SELECT pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' || sqlc.arg(user_id)::uuid::text, 0));

-- Todas as famílias do usuário: é o set-password do CLI. Marca também as
-- linhas já rotacionadas, como o RevokeFamily do auth.
-- name: RevokeAllFamilies :execrows
UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
 WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- A família está viva: tem a linha corrente (não usada, não revogada, não
-- vencida). $now é o relógio injetado, o mesmo que Rotate usa, nunca now().
-- name: FamilyIsLive :one
SELECT EXISTS (
    SELECT 1 FROM sessions
     WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)
       AND used_at IS NULL AND revoked_at IS NULL
       AND expires_at > sqlc.arg(now)::timestamptz
);

-- A troca de senha derruba as OUTRAS famílias do usuário e mantém a atual.
-- name: RevokeOtherFamilies :execrows
UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
 WHERE user_id = sqlc.arg(user_id) AND family_id <> sqlc.arg(keep_family_id)
   AND revoked_at IS NULL;

-- GET /sessions: a linha corrente de cada família viva do usuário. O
-- created_at dela é o do último refresh (cada rotação insere uma linha).
-- name: ListLiveFamilies :many
SELECT family_id, created_at, coalesce(user_agent, '')::text AS user_agent
  FROM sessions
 WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL AND revoked_at IS NULL
   AND expires_at > sqlc.arg(now)::timestamptz;

-- DELETE /sessions/{id}: todas as linhas não revogadas da família, como o
-- RevokeFamily do auth, e só se ela for deste usuário.
-- name: RevokeFamilyOfUser :execrows
UPDATE sessions SET revoked_at = sqlc.arg(now)::timestamptz
 WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)
   AND revoked_at IS NULL;
