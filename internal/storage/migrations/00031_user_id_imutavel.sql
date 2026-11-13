-- +goose Up
-- O dono (user_id) de alvo, heartbeat e canal não muda depois de criado. As
-- políticas dos escopos de serviço filtram qual linha cada um enxerga, mas o
-- WITH CHECK do ping e da verificação só exige o mesmo hash de token, e o do
-- sondador não restringe nada: sem esta trava, um UPDATE nesses escopos
-- entregaria a linha a outro usuário. Nenhuma query do código muda user_id.
--
-- Um gatilho cobre todos os escopos de uma vez, sem manter uma lista de
-- colunas por política. O erro é de privilégio (42501), como o da RLS.
-- Transferir um recurso de dono, se um dia existir, exige mudar esta regra
-- de propósito.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE FUNCTION guard_user_id_imutavel() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.user_id IS DISTINCT FROM OLD.user_id THEN
        RAISE EXCEPTION 'O dono (user_id) de % não muda.', TG_TABLE_NAME
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER targets_user_id_imutavel
BEFORE UPDATE OF user_id ON targets
FOR EACH ROW EXECUTE FUNCTION guard_user_id_imutavel();

CREATE TRIGGER heartbeats_user_id_imutavel
BEFORE UPDATE OF user_id ON heartbeats
FOR EACH ROW EXECUTE FUNCTION guard_user_id_imutavel();

CREATE TRIGGER notif_channels_user_id_imutavel
BEFORE UPDATE OF user_id ON notif_channels
FOR EACH ROW EXECUTE FUNCTION guard_user_id_imutavel();

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TRIGGER notif_channels_user_id_imutavel ON notif_channels;
DROP TRIGGER heartbeats_user_id_imutavel ON heartbeats;
DROP TRIGGER targets_user_id_imutavel ON targets;
DROP FUNCTION guard_user_id_imutavel();
