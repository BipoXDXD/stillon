-- Convites de projeto (migration 00049). Criar, listar e revogar rodam sob
-- storage.InProjectTx (políticas project_invitations_do_projeto_*), sob a
-- trava stillon.members do projeto (LockProjectMembers). Espiar roda no
-- escopo do hash (storage.InInvitationTokenTx); aceitar, no escopo do hash
-- com a sessão de quem aceita (storage.InInvitationAcceptTx).
-- (Os nomes não dizem "token": o gosec lê as constantes geradas como
-- credencial embutida, G101.)

-- Limpeza no próximo convite do projeto: pendentes vencidos e aceitos há
-- mais de 30 dias (o vínculo já existe; o aceito só serve à política de
-- INSERT na transação do aceite).
-- name: DeleteStaleInvitations :exec
DELETE FROM project_invitations
 WHERE project_id = sqlc.arg(project_id)
   AND ((accepted_at IS NULL AND expires_at <= sqlc.arg(now)::timestamptz)
        OR accepted_at <= sqlc.arg(now)::timestamptz - interval '30 days');

-- Reenviar: o pendente anterior do mesmo e-mail morre (e o link dele).
-- name: DeletePendingInvitationForEmail :exec
DELETE FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND email = sqlc.arg(email) AND accepted_at IS NULL;

-- O teto de membros conta os membros menos o titular.
-- name: CountMembersBesidesTitular :one
SELECT count(*)
  FROM project_members m
  JOIN projects p ON p.id = m.project_id
 WHERE m.project_id = sqlc.arg(project_id) AND m.user_id <> p.titular_id;

-- E os pendentes ainda válidos: o aceite só troca um pelo outro.
-- name: CountPendingInvitations :one
SELECT count(*)
  FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND accepted_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz;

-- name: IsMemberEmail :one
SELECT EXISTS (
    SELECT 1
      FROM project_members m
      JOIN users u ON u.id = m.user_id
     WHERE m.project_id = sqlc.arg(project_id) AND lower(u.email::text) = sqlc.arg(email)::text
)::boolean AS is_member;

-- name: InsertInvitation :one
INSERT INTO project_invitations (id, project_id, email, role, token_hash, invited_by, created_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(email), sqlc.arg(role), sqlc.arg(token_hash),
        sqlc.arg(invited_by), sqlc.arg(created_at), sqlc.arg(expires_at))
RETURNING id, project_id, email, role, created_at, expires_at;

-- name: ListPendingInvitations :many
SELECT id, project_id, email, role, created_at, expires_at
  FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND accepted_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
 ORDER BY created_at, id;

-- Revogar é apagar o pendente: o link morre. Aceito não se revoga (some
-- do painel; quem tira o membro é a remoção). O papel volta para a
-- autorização: revogar pede o mesmo que convidar com esse papel, e a recusa
-- desfaz o DELETE com a transação.
-- name: DeleteInvitation :one
DELETE FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND accepted_at IS NULL
RETURNING role;

-- O texto do e-mail: o nome do projeto e o e-mail de quem convida.
-- name: InvitationContext :one
SELECT p.name AS project_name, u.email::text AS inviter_email
  FROM projects p
  JOIN users u ON u.id = sqlc.arg(inviter)::uuid
 WHERE p.id = sqlc.arg(project_id);

-- O pendente válido do hash, sem consumi-lo. Nenhuma linha é convite
-- inexistente, revogado, aceito ou vencido, sem distinção.
-- name: PeekInvitation :one
SELECT project_id, email, role
  FROM project_invitations
 WHERE token_hash = sqlc.arg(token_hash) AND accepted_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz;

-- O aceite é um comando só: o UPDATE trava a linha, e o segundo aceite
-- simultâneo reavalia o WHERE na versão nova (accepted_at preenchido) e não
-- devolve nada. A política por_token_update recusa a conta de outro e-mail.
-- name: AcceptInvitation :one
UPDATE project_invitations
   SET accepted_at = sqlc.arg(now)::timestamptz,
       accepted_by = NULLIF(current_setting('app.user_id', true), '')::uuid
 WHERE token_hash = sqlc.arg(token_hash) AND accepted_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
RETURNING project_id, role;

-- O vínculo de quem aceitou, pela política project_members_por_convite_insert.
-- Já membro (inalcançável: o convite recusa e-mail de membro) não muda nada.
-- name: InsertMemberFromInvitation :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (sqlc.arg(project_id), sqlc.arg(user_id), sqlc.arg(role), sqlc.arg(now))
ON CONFLICT (project_id, user_id) DO NOTHING;

-- Na remoção (ou saída) do membro, os convites que ele aceitou ali somem:
-- a política de INSERT por convite nunca mais o admite sem convite novo.
-- name: DeleteAcceptedInvitationsOf :exec
DELETE FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND accepted_by = sqlc.arg(user_id)::uuid;

-- Quem convidou e foi removido leva junto os pendentes que mandou: o link não
-- sobrevive a quem o assinou.
-- name: DeletePendingInvitationsBy :exec
DELETE FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND invited_by = sqlc.arg(user_id)::uuid AND accepted_at IS NULL;

-- Os pendentes de quem convidou, para a mudança de papel apagar os que o
-- papel novo não poderia mandar (a regra é a do Go, AuthorizeInvite).
-- name: ListPendingInvitationsBy :many
SELECT id, role
  FROM project_invitations
 WHERE project_id = sqlc.arg(project_id) AND invited_by = sqlc.arg(user_id)::uuid AND accepted_at IS NULL;
