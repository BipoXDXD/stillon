-- +goose Up
-- A retenção apaga heartbeat_events por idade (at), em lotes. Sem índice em
-- at, cada lote varria a tabela inteira. A tabela é pequena (teto de 1000
-- eventos por heartbeat), então o CREATE INDEX comum, dentro da transação,
-- termina em instantes; o lock_timeout faz a migration desistir em vez de
-- enfileirar o ping atrás dela, e o boot tenta de novo.
SET LOCAL lock_timeout = '5s';
CREATE INDEX heartbeat_events_at_idx ON heartbeat_events (at);

-- +goose Down
DROP INDEX heartbeat_events_at_idx;
