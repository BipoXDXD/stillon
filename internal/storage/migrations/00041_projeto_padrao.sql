-- +goose Up
SET LOCAL lock_timeout = '5s';

-- UUIDv7 em SQL (o uuidv7() nativo só existe no 18; a CI testa o 17).
-- +goose StatementBegin
CREATE FUNCTION gerar_uuid_v7() RETURNS uuid
LANGUAGE sql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
    SELECT encode(
        set_bit(set_bit(
            overlay(uuid_send(gen_random_uuid())
                    PLACING substring(int8send((extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
                    FROM 1 FOR 6),
            52, 1), 53, 1),
        'hex')::uuid
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION gerar_uuid_v7() FROM PUBLIC;

-- Roda com o papel da aplicação, sob FORCE RLS: sem escopo, os UPDATEs não
-- enxergariam linha nenhuma e a migration "passaria" sem preencher nada.
-- Por isso liga app.user_id de cada usuário, e as políticas de dono (e as
-- de projects/project_members da 00039) deixam passar só o que é dele.
-- Idempotente: a 00042 e o contract a chamam de novo para as linhas que o
-- binário anterior gravar sem projeto durante o release. Só preenche
-- project_id: tokens de ping, links de verificação e slugs não mudam.
-- +goose StatementBegin
CREATE FUNCTION backfill_projetos_padrao() RETURNS void
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
    u       uuid;
    projeto uuid;
    -- A 00042 chama a função dentro de um escopo: ele volta como estava.
    escopo  text := coalesce(current_setting('app.user_id', true), '');
BEGIN
    FOR u IN SELECT id FROM users ORDER BY id LOOP
        PERFORM set_config('app.user_id', u::text, true);
        INSERT INTO projects (id, name, titular_id, is_default, created_at)
        VALUES (gerar_uuid_v7(), 'Projeto padrão', u, true, now())
        ON CONFLICT (titular_id) WHERE is_default DO NOTHING;
        SELECT p.id INTO STRICT projeto FROM projects p WHERE p.titular_id = u AND p.is_default;
        INSERT INTO project_members (project_id, user_id, role, created_at)
        VALUES (projeto, u, 'owner', now())
        ON CONFLICT DO NOTHING;
        UPDATE targets        SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE heartbeats     SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE notif_channels SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
        UPDATE status_pages   SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
    END LOOP;
    PERFORM set_config('app.user_id', escopo, true);
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION backfill_projetos_padrao() FROM PUBLIC;

SELECT backfill_projetos_padrao();

-- Uma vez só, e fora da função: até aqui só o CLI criava usuário, e ele
-- recusa o segundo (account.ErrUserExists). O dono atual é o usuário mais
-- antigo (desempate por id): ele vira o operador, e o e-mail dele é
-- confiável. Rerodar a função depois do cadastro público não promove ninguém.
UPDATE users SET is_operator = true, email_verified_at = COALESCE(email_verified_at, now())
 WHERE id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1);

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Os projetos e o project_id preenchidos ficam: apagar projeto levaria os
-- recursos junto (ON DELETE CASCADE da 00040). O down da 00040 derruba as
-- colunas e o da 00039 as tabelas, sem apagar recurso.
DROP FUNCTION backfill_projetos_padrao();
DROP FUNCTION gerar_uuid_v7();
