-- +goose Up
-- As latências do rollup (00006) só tinham o p50 amarrado ao ok. A
-- consolidação calcula as quatro com percentile_disc e max sobre a MESMA
-- amostra de latency_ms (>= 0) dos checks que não foram down: elas são NULL
-- juntas (hora sem sucesso) ou presentes juntas, nunca negativas, e em ordem.
-- Toda linha que a consolidação grava já obedece; o banco passa a recusar a
-- escrita de fora que gravaria uma curva impossível para o painel.
--
-- Só acrescenta CHECK (expand): nenhuma coluna muda, e o binário anterior
-- continua gravando linhas válidas. A tabela é pequena (uma linha por alvo e
-- hora, com 13 meses de retenção), e a validação na mesma transação é barata.
ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_juntas
    CHECK ((p95 IS NULL) = (p50 IS NULL)
       AND (p99 IS NULL) = (p50 IS NULL)
       AND (max IS NULL) = (p50 IS NULL));

ALTER TABLE check_rollup_1h ADD CONSTRAINT check_rollup_1h_latencias_em_ordem
    CHECK (0 <= p50 AND p50 <= p95 AND p95 <= p99 AND p99 <= max);

-- +goose Down
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_em_ordem;
ALTER TABLE check_rollup_1h DROP CONSTRAINT check_rollup_1h_latencias_juntas;
