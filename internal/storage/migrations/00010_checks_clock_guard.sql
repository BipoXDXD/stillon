-- +goose Up
-- Um check muito futuro faria o detector ignorar as sondas seguintes até
-- o relógio alcançar last_checked_at. A margem de um minuto tolera uma
-- pequena diferença de relógios; o banco é a referência independente do
-- Clock injetado no sondador. Valores antigos continuam válidos para
-- restauração de backups e fixtures históricas.
--
-- Não usar CHECK com now(): a condição depende do tempo no momento da
-- escrita, não é uma propriedade imutável da linha. O trigger por linha é
-- clonado pelo Postgres nas partições existentes e nas criadas depois.
-- +goose StatementBegin
CREATE FUNCTION guard_checks_clock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT isfinite(NEW.checked_at)
       OR NEW.checked_at > statement_timestamp() + interval '1 minute' THEN
        RAISE EXCEPTION 'O horário do check está à frente do relógio do banco.'
            USING ERRCODE = '23514', CONSTRAINT = 'checks_checked_at_not_future';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER checks_clock_guard
BEFORE INSERT OR UPDATE OF checked_at ON checks
FOR EACH ROW EXECUTE FUNCTION guard_checks_clock();

-- +goose Down
DROP TRIGGER checks_clock_guard ON checks;
DROP FUNCTION guard_checks_clock();
