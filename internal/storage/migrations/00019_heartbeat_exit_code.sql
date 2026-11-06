-- +goose Up
-- O código de saída que a tarefa mandou em /ping/{token}/{exit_code}. NULL é
-- sinal sem número (os sufixos /start e /fail e o ping puro). O CHECK guarda a
-- regra inteira: 0 só no sucesso, e 1..255 só na falha.
ALTER TABLE heartbeat_events ADD COLUMN exit_code smallint;
ALTER TABLE heartbeat_events ADD CONSTRAINT heartbeat_events_exit_code_check
    CHECK (exit_code IS NULL
           OR (kind = 'success' AND exit_code = 0)
           OR (kind = 'fail' AND exit_code BETWEEN 1 AND 255));

-- Pendência da §4 da spec de heartbeats: ClaimHeartbeatsToOpen filtra com OR e
-- não usa este índice, e cada ping pagava para mantê-lo.
DROP INDEX heartbeats_due_idx;

-- +goose Down
CREATE INDEX heartbeats_due_idx ON heartbeats (deadline_at) WHERE enabled;
ALTER TABLE heartbeat_events DROP CONSTRAINT heartbeat_events_exit_code_check;
ALTER TABLE heartbeat_events DROP COLUMN exit_code;
