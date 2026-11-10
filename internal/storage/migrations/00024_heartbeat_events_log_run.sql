-- +goose Up
-- Linhas de log da tarefa (POST /ping/{token}/log), o id da execução (?rid=)
-- e a origem do sinal (o ping ou o "sinalizar agora" do painel). Tudo é
-- inofensivo ao binário do PR 1, o piso de rollback: ele lista eventos log (o
-- painel dele os mostra como "Outro sinal"), e o INSERT e o UPDATE dele não
-- citam as colunas novas, que caem nos DEFAULTs ou ficam como estavam. O
-- SELECT * das queries dele foi expandido em colunas explícitas pelo sqlc na
-- geração, então coluna nova não muda o que ele escaneia.
ALTER TABLE heartbeat_events DROP CONSTRAINT heartbeat_events_kind_check;
ALTER TABLE heartbeat_events ADD CONSTRAINT heartbeat_events_kind_check
    CHECK (kind IN ('start', 'success', 'fail', 'log'));

-- NULL é sinal sem ?rid=.
ALTER TABLE heartbeat_events ADD COLUMN run_id uuid;
-- O DEFAULT é o que mantém o INSERT do piso, que não cita a coluna, gravando.
ALTER TABLE heartbeat_events ADD COLUMN source text NOT NULL DEFAULT 'ping'
    CHECK (source IN ('ping', 'panel'));

-- O início de uma execução com rid é o start mais recente de mesmo rid.
CREATE INDEX heartbeat_events_run_idx ON heartbeat_events (heartbeat_id, run_id, id DESC)
    WHERE run_id IS NOT NULL;

-- O rid do último sinal que mudou o estado. last_signal não ganha log: /log
-- não muda o estado.
ALTER TABLE heartbeats ADD COLUMN last_run_id uuid;
-- Um sucesso por ping retoma a tarefa pausada, se o dono ligou isto.
ALTER TABLE heartbeats ADD COLUMN ping_resumes bool NOT NULL DEFAULT false;

-- +goose Down
-- Roda com o papel da aplicação, sujeito à RLS: sem o escopo do sondador, o
-- DELETE não enxergaria linha nenhuma, e o CHECK antigo falharia nos eventos
-- log que sobrassem.
SELECT set_config('app.scope', 'prober', true);
DELETE FROM heartbeat_events WHERE kind = 'log';
ALTER TABLE heartbeats DROP COLUMN ping_resumes;
ALTER TABLE heartbeats DROP COLUMN last_run_id;
DROP INDEX heartbeat_events_run_idx;
ALTER TABLE heartbeat_events DROP COLUMN source;
ALTER TABLE heartbeat_events DROP COLUMN run_id;
ALTER TABLE heartbeat_events DROP CONSTRAINT heartbeat_events_kind_check;
ALTER TABLE heartbeat_events ADD CONSTRAINT heartbeat_events_kind_check
    CHECK (kind IN ('start', 'success', 'fail'));
