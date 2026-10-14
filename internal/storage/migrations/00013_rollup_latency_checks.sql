-- +goose Up
-- As latências do rollup (00006) só tinham o p50 amarrado ao ok. A
-- consolidação calcula as quatro com percentile_disc e max sobre a MESMA
-- amostra de latency_ms (NOT NULL, >= 0 desde a 00004) dos checks que não
-- foram down: elas são NULL juntas (hora sem sucesso) ou presentes juntas,
-- nunca negativas, e em ordem. ConsolidateRollups é a única escrita na tabela
-- e tem a mesma fórmula desde a 00006; o banco passa a recusar a escrita de
-- fora que gravaria uma curva impossível para o painel.
--
-- NOT VALID: esta migration roda no boot, e um CHECK validado varre a tabela
-- inteira. Uma linha de produção fora da regra (de restore, edição manual ou
-- algo que o código não explica) faria a migration falhar e o app não subir.
-- Assim o CHECK vale para todo INSERT e UPDATE daqui em diante, sem tocar nas
-- linhas antigas. Pendente: conferir em produção que esta consulta volta
-- vazia e só então, numa migration nova, rodar
-- ALTER TABLE check_rollup_1h VALIDATE CONSTRAINT <nome> para os dois.
--   SELECT target_id, bucket FROM check_rollup_1h
--    WHERE NOT ((p95 IS NULL) = (p50 IS NULL) AND (p99 IS NULL) = (p50 IS NULL)
--               AND (max IS NULL) = (p50 IS NULL))
--       OR NOT (0 <= p50 AND p50 <= p95 AND p95 <= p99 AND p99 <= max);
--
-- Só acrescenta CHECK (expand): nenhuma coluna muda, e o binário anterior
-- continua gravando linhas válidas.
ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_juntas
    CHECK ((p95 IS NULL) = (p50 IS NULL)
       AND (p99 IS NULL) = (p50 IS NULL)
       AND (max IS NULL) = (p50 IS NULL)) NOT VALID;

ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_em_ordem
    CHECK (0 <= p50 AND p50 <= p95 AND p95 <= p99 AND p99 <= max) NOT VALID;

-- +goose Down
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_em_ordem;
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_juntas;
