-- +goose Up
-- A duração da última execução medida, para a lista de heartbeats. Fica na
-- linha, como last_ping_at e last_success_at: a lista roda a cada 15 s, e achar
-- o último evento com duração varreria o log inteiro de quem nunca manda
-- /start. NULL é "a última execução não foi medida".
ALTER TABLE heartbeats ADD COLUMN last_runtime_ms bigint CHECK (last_runtime_ms >= 0);

-- +goose Down
ALTER TABLE heartbeats DROP COLUMN last_runtime_ms;
