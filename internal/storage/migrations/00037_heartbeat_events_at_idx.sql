-- +goose NO TRANSACTION

-- +goose Up
-- O índice atende aos lotes por idade sem bloquear as escritas dos pings
-- durante a construção. Se a construção anterior foi interrompida, o
-- índice pode ter ficado inválido; remova antes de tentar outra vez.
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS heartbeat_events_at_idx;
CREATE INDEX CONCURRENTLY heartbeat_events_at_idx ON heartbeat_events (at);
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS heartbeat_events_at_idx;
RESET lock_timeout;
