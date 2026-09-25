-- Canais (spec §8, §5.8). As desta seção rodam sob InUserTx e filtram por
-- user_id; as duas do fim rodam sob InVerifyTx.

-- Serializa as criações de canal do mesmo usuário, para que o limite de
-- canais valha também contra duas requisições simultâneas. users não tem
-- RLS (D7 do Plano 2).
-- name: LockUser :exec
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: CountChannels :one
SELECT count(*) FROM notif_channels WHERE user_id = $1;

-- name: InsertChannel :exec
INSERT INTO notif_channels (id, user_id, kind, config_enc, verified_at,
                            verify_token_hash, verify_expires_at, enabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8);

-- Sem paginação: o limite por usuário é pequeno (notify.MaxChannels).
-- name: ListChannels :many
SELECT c.id, c.kind, c.config_enc, c.verified_at, c.verify_expires_at, c.enabled, c.created_at,
       h.last_success_at, h.last_failure_at, h.last_error
  FROM notif_channels c
  LEFT JOIN channel_health h ON h.channel_id = c.id
 WHERE c.user_id = $1
 ORDER BY c.id DESC;

-- Um canal no formato da listagem (ListChannels), para a resposta do PATCH
-- /channels/{id} (Plano 9, §3.3).
-- name: GetChannelView :one
SELECT c.id, c.kind, c.config_enc, c.verified_at, c.verify_expires_at, c.enabled, c.created_at,
       h.last_success_at, h.last_failure_at, h.last_error
  FROM notif_channels c
  LEFT JOIN channel_health h ON h.channel_id = c.id
 WHERE c.user_id = $1 AND c.id = $2;

-- Pausar e retomar (Plano 9, §3.3). O despacho lê enabled na hora do envio
-- (semEnvio): pausado, o aviso é cancelado, e retomar não o reenvia. Gravar o
-- valor que já está afeta a linha do mesmo jeito: a rota é idempotente.
-- name: SetChannelEnabled :execrows
UPDATE notif_channels SET enabled = sqlc.arg(enabled)
 WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id);

-- name: GetChannel :one
SELECT * FROM notif_channels WHERE user_id = $1 AND id = $2;

-- name: DeleteChannel :execrows
DELETE FROM notif_channels WHERE user_id = $1 AND id = $2;

-- Reenvio: troca o token. O antigo deixa de casar com qualquer linha.
-- name: RenewVerification :execrows
UPDATE notif_channels
   SET verify_token_hash = sqlc.arg(token_hash), verify_expires_at = sqlc.arg(expires_at)::timestamptz
 WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id)
   AND kind = 'email' AND verified_at IS NULL;

-- name: FindChannelByVerification :one
SELECT id, verified_at, verify_expires_at FROM notif_channels
 WHERE verify_token_hash = sqlc.arg(token_hash);

-- Idempotente: confirmar de novo um canal já verificado não muda nada e
-- afeta a linha, então o segundo clique também vê sucesso. Token vencido e
-- ainda não usado não afeta nada.
-- name: ConfirmChannel :execrows
UPDATE notif_channels
   SET verified_at = COALESCE(verified_at, sqlc.arg(now)::timestamptz)
 WHERE verify_token_hash = sqlc.arg(token_hash)
   AND (verified_at IS NOT NULL OR verify_expires_at > sqlc.arg(now)::timestamptz);
