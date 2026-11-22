-- +goose Up
-- Segunda camada do fan-out por projeto (spec §8): a notificação de um
-- incidente só entra num canal do projeto do recurso (alvo ou heartbeat).
-- O sondador lê todos os canais (notif_channels_prober_select) e a
-- RESTRICTIVE da 00042 vale true sem app.project_id: sem esta guarda, um
-- enfileiramento errado no código (ListDeliverableChannelsForIncident é a
-- primeira camada) entregaria o aviso de A num canal de B.
--
-- Descarta (RETURN NULL) e não aborta: o enfileiramento roda na transação
-- do InsertCheck, e abortar desfaria o check e o incidente e pararia o
-- monitor. Fallback legado, o mesmo da consulta (sai no PR 7, com o NOT
-- NULL): recurso ou canal sem project_id casa por user_id.
--
-- SECURITY INVOKER: lê sob a RLS de quem insere. Todo INSERT de produção em
-- notifications roda em InProberTx (prober/avisos.go, notify/reminders.go),
-- que enxerga incidents, targets, heartbeats e notif_channels. Não achar a
-- linha do recurso ou do canal descarta (fail closed).
--
-- incident_id NULL passa: não há recurso a comparar, e o NOT NULL da coluna
-- (conferido depois dos gatilhos BEFORE) recusa a linha com erro, como hoje.
-- Uma versão que grave avisos sem incidente decide a guarda deles.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE FUNCTION guardar_notificacao_no_projeto() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path = public, pg_temp AS $$
DECLARE
    recurso_projeto uuid;
    recurso_dono    uuid;
    canal_projeto   uuid;
    canal_dono      uuid;
BEGIN
    IF NEW.incident_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT COALESCE(t.project_id, h.project_id), COALESCE(t.user_id, h.user_id)
      INTO recurso_projeto, recurso_dono
      FROM incidents i
      LEFT JOIN targets t    ON t.id = i.target_id
      LEFT JOIN heartbeats h ON h.id = i.heartbeat_id
     WHERE i.id = NEW.incident_id;
    -- user_id é NOT NULL em targets e heartbeats: NULL aqui é o recurso que
    -- a RLS escondeu (ou o incidente que não existe).
    IF recurso_dono IS NULL THEN
        RAISE WARNING 'notificação descartada: canal % fora do projeto do incidente %',
            NEW.channel_id, NEW.incident_id;
        RETURN NULL;
    END IF;

    SELECT c.project_id, c.user_id INTO canal_projeto, canal_dono
      FROM notif_channels c
     WHERE c.id = NEW.channel_id;
    IF NOT FOUND
       OR (recurso_projeto IS NOT NULL AND canal_projeto IS NOT NULL
           AND recurso_projeto IS DISTINCT FROM canal_projeto)
       OR ((recurso_projeto IS NULL OR canal_projeto IS NULL)
           AND recurso_dono IS DISTINCT FROM canal_dono) THEN
        RAISE WARNING 'notificação descartada: canal % fora do projeto do incidente %',
            NEW.channel_id, NEW.incident_id;
        RETURN NULL;
    END IF;

    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notifications_canal_do_projeto BEFORE INSERT ON notifications
    FOR EACH ROW EXECUTE FUNCTION guardar_notificacao_no_projeto();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- O gatilho cai antes: ele prende a função (sem isso, 2BP01).
DROP TRIGGER notifications_canal_do_projeto ON notifications;
DROP FUNCTION guardar_notificacao_no_projeto();
