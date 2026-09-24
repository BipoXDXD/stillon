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
