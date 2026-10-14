-- +goose Up
-- Valida os dois CHECKs que a 00013 criou NOT VALID. Conferido em produção
-- em 2026-10-14, logo depois do deploy da 00013: a consulta do comentário
-- dela voltou vazia (a tabela estava vazia). Daí em diante toda escrita já
-- passa pelos CHECKs, então nenhuma linha fora da regra pode ter surgido, e
-- o VALIDATE, que varre a tabela sem bloquear escritas
-- (SHARE UPDATE EXCLUSIVE), não falha no boot.
ALTER TABLE check_rollup_1h VALIDATE CONSTRAINT check_rollup_1h_latencias_juntas;
ALTER TABLE check_rollup_1h VALIDATE CONSTRAINT check_rollup_1h_latencias_em_ordem;

-- +goose Down
-- Não há "desvalidar": volta ao estado da 00013 recriando os dois NOT VALID.
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_em_ordem;
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_juntas;

ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_juntas
    CHECK ((p95 IS NULL) = (p50 IS NULL)
       AND (p99 IS NULL) = (p50 IS NULL)
       AND (max IS NULL) = (p50 IS NULL)) NOT VALID;

ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_em_ordem
    CHECK (0 <= p50 AND p50 <= p95 AND p95 <= p99 AND p99 <= max) NOT VALID;
