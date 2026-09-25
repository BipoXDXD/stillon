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
--
-- Um cadastro de 2FA pendente (segredo sem confirmação) cai junto: quem o
-- iniciou pode ser o invasor que a redefinição está expulsando, e ele o
-- confirmaria com o autenticador dele. O 2FA confirmado fica (D10). Task 11.
--
-- Uma troca de autenticador em andamento (totp_pending_secret_enc, Plano 9)
-- cai pelo mesmo motivo: o pendente não tem prazo, quem o iniciou pode ser o
-- invasor, e uma sessão roubada o confirmaria depois, ficando com o 2FA.
-- Trocar a senha é a reação do dono à suspeita, e mata a troca. A família que
-- a iniciou (totp_pending_family_id) cai junto: os dois andam juntos.
-- name: ReplaceUserHash :execrows
UPDATE users SET password_hash = $2, failed_attempts = 0, locked_until = NULL,
       totp_secret_enc = CASE WHEN totp_confirmed_at IS NULL THEN NULL ELSE totp_secret_enc END,
       totp_pending_secret_enc = NULL, totp_pending_family_id = NULL
 WHERE id = $1;

-- Serializa dois `vigilume user create` simultâneos: sem ela, os dois contam
-- zero usuários e os dois criam (D9 do Plano 5).
-- name: LockUserCreation :exec
SELECT pg_advisory_xact_lock(hashtextextended('vigilume.user_create', 0));

-- O registro de auditoria com entidade. O insertAudit do auth (sem entidade)
-- continua à mão até o R2.
-- name: InsertAuditEntry :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, ip_hash, at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- GET /me. totp_enabled é o segundo fator CONFIRMADO: um cadastro iniciado e
-- abandonado não conta (o mesmo critério de auth.TOTP.IsEnabled).
-- name: GetAccount :one
SELECT id, email, (totp_confirmed_at IS NOT NULL)::bool AS totp_enabled, created_at
  FROM users WHERE id = $1;

-- A troca de senha pela API é um compare-and-swap: só grava se o hash ainda
-- é o que foi verificado. Duas trocas simultâneas com a mesma senha atual não
-- se atropelam, e a verificação (Argon2id) não precisa segurar trava. Zera o
-- bloqueio: a senha e o segundo fator acabaram de ser provados. Apaga o
-- cadastro de 2FA pendente e a troca de autenticador em andamento, pelos
-- motivos do ReplaceUserHash.
-- name: SwapUserHash :execrows
UPDATE users SET password_hash = sqlc.arg(new_hash), failed_attempts = 0, locked_until = NULL,
       totp_secret_enc = CASE WHEN totp_confirmed_at IS NULL THEN NULL ELSE totp_secret_enc END,
       totp_pending_secret_enc = NULL, totp_pending_family_id = NULL
 WHERE id = sqlc.arg(id) AND password_hash = sqlc.arg(old_hash);

-- O hash atual, lido por auth.Manager.Issue SOB a trava do usuário (D1): a
-- sessão só nasce se a senha verificada no login ainda é a do usuário. Sem
-- isto, um login que conferiu a senha ANTIGA abria sessão depois de um
-- set-password ou de uma troca de senha, e ela sobrevivia (Task 11).
-- name: GetUserHash :one
SELECT password_hash FROM users WHERE id = $1;

-- O rehash do login (parâmetros do Argon2id subiram) é um compare-and-swap:
-- sem ele, o login que verificou a senha ANTIGA regravava o hash dela por cima
-- de um set-password concorrente, e desfazia a redefinição. Não mexe no
-- bloqueio: com segundo fator, quem zera é o /totp/verify (R40).
-- name: RehashUser :execrows
UPDATE users SET password_hash = sqlc.arg(new_hash)
 WHERE id = sqlc.arg(id) AND password_hash = sqlc.arg(old_hash);

-- vigilume user reset-2fa (Plano 9, §3.1): desliga o 2FA inteiro, inclusive a
-- troca de autenticador pendente. A guarda na WHERE torna o comando
-- idempotente: sem 2FA confirmado, nenhuma linha muda, e quem chama não
-- encerra sessão nem audita. A marca do desafio (totp_challenge_mark) fica:
-- ela só recusa desafios velhos, e zerá-la não ajuda ninguém.
-- name: ResetTOTP :execrows
UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL, totp_last_timestep = NULL,
       totp_pending_secret_enc = NULL, totp_pending_family_id = NULL
 WHERE id = $1 AND totp_confirmed_at IS NOT NULL;

-- Os códigos de recuperação morrem com o fator (reset-2fa).
-- name: DeleteRecoveryCodes :exec
DELETE FROM totp_recovery_codes WHERE user_id = $1;
