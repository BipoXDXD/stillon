-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE targets        ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE heartbeats     ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE notif_channels ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE status_pages   ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;

CREATE INDEX targets_project_idx        ON targets (project_id, id DESC);
CREATE INDEX heartbeats_project_idx     ON heartbeats (project_id, id DESC);
CREATE INDEX notif_channels_project_idx ON notif_channels (project_id, id DESC);
CREATE UNIQUE INDEX status_pages_project_key ON status_pages (project_id);

-- O binário anterior (e o deste PR) grava sem project_id: o gatilho põe o
-- Projeto padrão do user_id. Roda sob a RLS de quem insere; no InUserTx o
-- titular enxerga o próprio projeto (projects_member_select). Dentro do
-- escopo de projeto não preenche: código novo que esquece o project_id tem
-- de falhar alto. Sai no contract, com o NOT NULL.
-- +goose StatementBegin
CREATE FUNCTION preencher_project_id_padrao() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
    IF NEW.project_id IS NULL
       AND NULLIF(current_setting('app.project_id', true), '') IS NULL THEN
        SELECT p.id INTO NEW.project_id
          FROM projects p
         WHERE p.titular_id = NEW.user_id AND p.is_default;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Como guard_user_id_imutavel (00031): os escopos de serviço (ping, verify,
-- sondador) têm UPDATE de linha inteira. NULL → valor é o backfill.
-- +goose StatementBegin
CREATE FUNCTION guard_project_id_imutavel() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF OLD.project_id IS NOT NULL AND NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        RAISE EXCEPTION 'O projeto (project_id) de % não muda.', TG_TABLE_NAME
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER targets_project_id_padrao BEFORE INSERT ON targets
    FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
CREATE TRIGGER heartbeats_project_id_padrao BEFORE INSERT ON heartbeats
    FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
CREATE TRIGGER notif_channels_project_id_padrao BEFORE INSERT ON notif_channels
    FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
CREATE TRIGGER status_pages_project_id_padrao BEFORE INSERT ON status_pages
    FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();

CREATE TRIGGER targets_project_id_imutavel BEFORE UPDATE OF project_id ON targets
    FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
CREATE TRIGGER heartbeats_project_id_imutavel BEFORE UPDATE OF project_id ON heartbeats
    FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
CREATE TRIGGER notif_channels_project_id_imutavel BEFORE UPDATE OF project_id ON notif_channels
    FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
CREATE TRIGGER status_pages_project_id_imutavel BEFORE UPDATE OF project_id ON status_pages
    FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Os gatilhos caem antes: o UPDATE OF project_id prende a coluna, e o de
-- preenchimento prende a função (sem isso, 2BP01). DROP COLUMN leva a FK e
-- os índices; nenhuma linha de recurso é apagada (ver
-- TestProjectMigrationsDownKeepResources).
DROP TRIGGER status_pages_project_id_imutavel ON status_pages;
DROP TRIGGER notif_channels_project_id_imutavel ON notif_channels;
DROP TRIGGER heartbeats_project_id_imutavel ON heartbeats;
DROP TRIGGER targets_project_id_imutavel ON targets;
DROP TRIGGER status_pages_project_id_padrao ON status_pages;
DROP TRIGGER notif_channels_project_id_padrao ON notif_channels;
DROP TRIGGER heartbeats_project_id_padrao ON heartbeats;
DROP TRIGGER targets_project_id_padrao ON targets;
ALTER TABLE status_pages   DROP COLUMN project_id;
ALTER TABLE notif_channels DROP COLUMN project_id;
ALTER TABLE heartbeats     DROP COLUMN project_id;
ALTER TABLE targets        DROP COLUMN project_id;
DROP FUNCTION guard_project_id_imutavel();
DROP FUNCTION preencher_project_id_padrao();
