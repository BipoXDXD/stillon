-- Conta do usuário. users não tem RLS: o login a
-- consulta por e-mail antes de existir usuário na transação. Toda query daqui
-- que parte de um usuário recebe o id dele.

-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: InsertUser :exec
INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4);

-- O cadastro público (PR 3). E-mail repetido (citext: a caixa não importa) não
-- grava nada e não devolve linha (pgx.ErrNoRows), sem erro de UNIQUE que
-- abortaria a transação do cadastro.
-- O aceite dos termos (versão e instante) entra só na conta nova.
-- name: InsertSignupUser :one
INSERT INTO users (id, email, password_hash, created_at, terms_version, terms_accepted_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(password_hash), sqlc.arg(created_at),
        sqlc.arg(terms_version)::text, sqlc.arg(created_at))
ON CONFLICT (email) DO NOTHING
RETURNING id;

-- A confirmação do e-mail (pelo link ou pela redefinição de senha). O
-- COALESCE guarda o primeiro instante: confirmar de novo não o move.
-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = COALESCE(email_verified_at, sqlc.arg(now)::timestamptz) WHERE id = sqlc.arg(id);

-- A trava de criação de recursos de conta não confirmada.
-- name: UserEmailVerified :one
SELECT (email_verified_at IS NOT NULL)::bool AS verified FROM users WHERE id = sqlc.arg(id);

-- O usuário criado pelo CLI é o operador: sem as cotas do plano grátis, e com
-- o e-mail dado por verificado (foi o operador quem o digitou no servidor).
-- name: MarkUserOperator :exec
UPDATE users SET is_operator = true, email_verified_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- A senha redefinida pelo operador também tira o bloqueio: é o caminho de
-- recuperação de quem ficou trancado para fora. (O nome evita "password": o
-- gosec lê a constante gerada como credencial embutida, G101.)
--
-- Um cadastro de 2FA pendente (segredo sem confirmação) cai junto: quem o
-- iniciou pode ser o invasor que a redefinição está expulsando, e ele o
-- confirmaria com o autenticador dele. O 2FA confirmado fica.
--
-- Uma troca de autenticador em andamento (totp_pending_secret_enc)
-- cai pelo mesmo motivo: o pendente vale só PendingRotationTTL (10 min), mas
-- quem o iniciou pode ser o invasor, e uma sessão roubada o confirmaria
-- depois, ficando com o 2FA.
-- Trocar a senha é a reação do dono à suspeita, e mata a troca. A família que
-- a iniciou (totp_pending_family_id) cai junto: os dois andam juntos.
-- name: ReplaceUserHash :execrows
UPDATE users SET password_hash = $2, failed_attempts = 0, locked_until = NULL,
       totp_secret_enc = CASE WHEN totp_confirmed_at IS NULL THEN NULL ELSE totp_secret_enc END,
       totp_pending_secret_enc = NULL, totp_pending_family_id = NULL
 WHERE id = $1;

-- O registro de auditoria com entidade. O insertAudit do auth (sem entidade)
-- continua escrito à mão em internal/auth/store.go.
-- name: InsertAuditEntry :exec
INSERT INTO audit_log (id, user_id, action, entity, entity_id, ip_hash, at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- GET /me. totp_enabled é o segundo fator CONFIRMADO: um cadastro iniciado e
-- abandonado não conta (o mesmo critério de auth.TOTP.IsEnabled).
-- email_verified_at é nulo na conta do cadastro público ainda não confirmada.
-- name: GetAccount :one
SELECT id, email, (totp_confirmed_at IS NOT NULL)::bool AS totp_enabled, created_at, email_verified_at
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

-- O hash atual, lido por auth.Manager.Issue SOB a trava do usuário: a
-- sessão só nasce se a senha verificada no login ainda é a do usuário. Sem
-- isto, um login que conferiu a senha ANTIGA abria sessão depois de um
-- set-password ou de uma troca de senha, e ela sobrevivia.
-- name: GetUserHash :one
SELECT password_hash FROM users WHERE id = $1;

-- O rehash do login (parâmetros do Argon2id subiram) é um compare-and-swap:
-- sem ele, o login que verificou a senha ANTIGA regravava o hash dela por cima
-- de um set-password concorrente, e desfazia a redefinição. Não mexe no
-- bloqueio: com segundo fator, quem zera é o /totp/verify.
-- name: RehashUser :execrows
UPDATE users SET password_hash = sqlc.arg(new_hash)
 WHERE id = sqlc.arg(id) AND password_hash = sqlc.arg(old_hash);

-- stillon user reset-2fa: desliga o 2FA inteiro, inclusive a
-- troca de autenticador pendente. A guarda na WHERE torna o comando
-- idempotente quando a conta está sem 2FA confirmado e sem trava permanente.
-- Nessa condição, quem chama deixa as sessões e a auditoria como estão.
-- A marca do desafio (totp_challenge_mark) fica:
-- ela só recusa desafios velhos, e zerá-la não ajuda ninguém.
--
-- Tira também o bloqueio, como o set-password: quem roda o
-- comando é o operador, a identidade foi provada fora da API, e o dono que
-- perdeu o autenticador costuma chegar aqui com a conta travada pelos chutes
-- dele mesmo. É também a única saída do teto absoluto do bloqueio do
-- segundo fator (totp_locked_until = 'infinity'), inclusive quando a
-- desativação do fator já apagou a confirmação.
-- name: ResetTOTP :execrows
UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL, totp_last_timestep = NULL,
       totp_pending_secret_enc = NULL, totp_pending_family_id = NULL,
       failed_attempts = 0, locked_until = NULL,
       totp_failures = 0, totp_locked_until = NULL
 WHERE id = $1 AND (totp_confirmed_at IS NOT NULL OR totp_locked_until = 'infinity'::timestamptz);

-- Os códigos de recuperação morrem com o fator (reset-2fa).
-- name: DeleteRecoveryCodes :exec
DELETE FROM totp_recovery_codes WHERE user_id = $1;

-- O operador tem teto de projetos maior (tenancy.LimitesDe).
-- name: UserIsOperator :one
SELECT is_operator FROM users WHERE id = sqlc.arg(id);

-- A suspensão (stillon user suspend). Zero linhas quando nada muda: a conta
-- já suspensa (o primeiro instante fica) ou o operador, que não se suspende.
-- name: SuspendUser :execrows
UPDATE users SET suspended_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(id) AND NOT is_operator AND suspended_at IS NULL;

-- A reativação (stillon user unsuspend). Zero linhas quando a conta não
-- estava suspensa. As sessões encerradas pela suspensão não voltam.
-- name: UnsuspendUser :execrows
UPDATE users SET suspended_at = NULL
 WHERE id = sqlc.arg(id) AND suspended_at IS NOT NULL;

-- A suspensão relida sob a trava do usuário, na emissão e na rotação de
-- sessão (auth.Manager).
-- name: UserSuspension :one
SELECT suspended_at FROM users WHERE id = sqlc.arg(id);
