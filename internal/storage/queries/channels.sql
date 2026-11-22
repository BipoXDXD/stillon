-- Canais (spec §8, §5.8). As do dono rodam sob InProjectTx e filtram por
-- project_id; as exceções, por user_id, são a cota do usuário (LockUser,
-- CountChannels). FindChannelByVerification e ConfirmChannel rodam sob
-- InVerifyTx.

-- Serializa as criações de canal do mesmo usuário, para que o limite de
-- canais valha também contra duas requisições simultâneas. users não tem
-- RLS.
-- name: LockUser :exec
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: CountChannels :one
SELECT count(*) FROM notif_channels WHERE user_id = $1;

-- user_id é quem criou o canal; project_id, o projeto em que ele vive, e
-- sempre vem: no escopo de projeto o gatilho preencher_project_id_padrao não
-- preenche, e a RLS recusaria o nulo.
-- name: InsertChannel :exec
INSERT INTO notif_channels (id, user_id, kind, config_enc, verified_at,
                            verify_token_hash, verify_expires_at, enabled, created_at,
                            project_id)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(config_enc),
        sqlc.narg(verified_at), sqlc.narg(verify_token_hash), sqlc.narg(verify_expires_at),
        true, sqlc.arg(created_at), sqlc.arg(project_id)::uuid);

-- Sem paginação: o limite por usuário é pequeno (notify.MaxChannels).
-- name: ListChannels :many
SELECT c.id, c.kind, c.config_enc, c.verified_at, c.verify_expires_at, c.enabled, c.created_at,
       h.last_success_at, h.last_failure_at, h.last_error
  FROM notif_channels c
  LEFT JOIN channel_health h ON h.channel_id = c.id
 WHERE c.project_id = sqlc.arg(project_id)::uuid
 ORDER BY c.id DESC;

-- Um canal no formato da listagem (ListChannels), para a resposta do PATCH
-- /channels/{id}.
-- name: GetChannelView :one
SELECT c.id, c.kind, c.config_enc, c.verified_at, c.verify_expires_at, c.enabled, c.created_at,
       h.last_success_at, h.last_failure_at, h.last_error
  FROM notif_channels c
  LEFT JOIN channel_health h ON h.channel_id = c.id
 WHERE c.project_id = sqlc.arg(project_id)::uuid AND c.id = sqlc.arg(id);

-- Pausar e retomar. O despacho lê enabled na hora do envio
-- (semEnvio): pausado, o aviso é cancelado, e retomar não o reenvia. Gravar o
-- valor que já está afeta a linha do mesmo jeito: a rota é idempotente.
-- name: SetChannelEnabled :execrows
UPDATE notif_channels SET enabled = sqlc.arg(enabled)
 WHERE project_id = sqlc.arg(project_id)::uuid AND id = sqlc.arg(id);

-- name: GetChannel :one
SELECT id, user_id, kind, config_enc, verified_at, verify_token_hash,
       verify_expires_at, enabled, created_at, project_id
  FROM notif_channels WHERE project_id = sqlc.arg(project_id)::uuid AND id = sqlc.arg(id);

-- name: DeleteChannel :execrows
DELETE FROM notif_channels WHERE project_id = sqlc.arg(project_id)::uuid AND id = sqlc.arg(id);

-- Reenvio: troca o token. O antigo deixa de casar com qualquer linha.
-- name: RenewVerification :execrows
UPDATE notif_channels
   SET verify_token_hash = sqlc.arg(token_hash), verify_expires_at = sqlc.arg(expires_at)::timestamptz
 WHERE project_id = sqlc.arg(project_id)::uuid AND id = sqlc.arg(id)
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

-- Conferência da MASTER_KEY na subida (notify.CheckMasterKey), sob
-- InProberTx: os selos mais recentes de qualquer dono, só para tentar abrir.
-- name: LatestChannelSeals :many
SELECT id, config_enc FROM notif_channels ORDER BY created_at DESC, id DESC LIMIT $1;
