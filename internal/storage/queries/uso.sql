-- O uso do titular para o operador (00054): uma linha por projeto de que o
-- app.user_id da transação é titular, o padrão primeiro. Roda em InUserTx.
-- Os casts e os AS são para o sqlc, como em CotaDoReceptor.
-- name: TitularUsage :many
SELECT u.p_id::uuid AS id, u.p_nome::text AS name, u.p_padrao::boolean AS is_default,
       u.n_alvos::bigint AS alvos, u.n_heartbeats::bigint AS heartbeats, u.n_canais::bigint AS canais,
       u.n_membros::bigint AS membros, u.n_convites::bigint AS convites
  FROM uso_do_titular(sqlc.arg(agora)::timestamptz) AS u;
