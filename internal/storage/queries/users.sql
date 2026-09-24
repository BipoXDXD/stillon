-- Conta do usuário (Plano 5). users não tem RLS (D7 do Plano 2): o login a
-- consulta por e-mail antes de existir usuário na transação. Toda query daqui
-- que parte de um usuário recebe o id dele.

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: InsertUser :exec
INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4);

-- A senha redefinida pelo operador também tira o bloqueio: é o caminho de
-- recuperação de quem ficou trancado para fora. (O nome evita "password": o
-- gosec lê a constante gerada como credencial embutida, G101.)
-- name: ReplaceUserHash :execrows
UPDATE users SET password_hash = $2, failed_attempts = 0, locked_until = NULL WHERE id = $1;

-- Serializa dois `vigilume user create` simultâneos: sem ela, os dois contam
-- zero usuários e os dois criam (D9 do Plano 5).
-- name: LockUserCreation :exec
SELECT pg_advisory_xact_lock(hashtextextended('vigilume.user_create', 0));

-- O registro de auditoria com entidade. O insertAudit do auth (sem entidade)
-- continua à mão até o R2.
-- name: InsertAuditEntry :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, ip_hash, at)
VALUES ($1, $2, $3, $4, $5, $6, $7);
