-- +goose Up
-- Faixas de heartbeat alargadas (spec SP1, §2): período e tolerância até
-- 365 dias, descrição até 10000 caracteres. Os nomes dos CHECKs ficam os
-- mesmos, e o binário do piso de rollback (PR 1) já lê linhas acima das
-- faixas antigas.
--
-- A descida converte em vez de apagar (spec SP1, §6): apagar perderia a
-- tarefa, o token e o histórico de quem só reverteu o deploy. O binário do
-- piso lê as linhas convertidas como lê qualquer outra.
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_period_sec_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_period_sec_check
    CHECK (period_sec BETWEEN 60 AND 31536000);
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_grace_sec_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_grace_sec_check
    CHECK (grace_sec BETWEEN 30 AND 31536000);
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_description_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_description_check
    CHECK (char_length(description) <= 10000);

-- +goose Down
-- Roda com o papel da aplicação, sujeito à RLS: sem o escopo do sondador, o
-- UPDATE não enxergaria linha nenhuma, e os CHECKs antigos abaixo falhariam.
-- O sondador não tem DELETE em heartbeats; converter é a saída.
SELECT set_config('app.scope', 'prober', true);
-- Período rebaixado também recoloca o prazo: sem isso, o binário antigo
-- ficaria até um ano sem avisar sobre uma tarefa de 365 dias rebaixada a 31.
-- O LEAST nunca adia um prazo; o cron com tolerância reduzida mantém o seu
-- (só atrasa o aviso, nunca o antecipa).
--
-- O período usa CASE, e não LEAST: o LEAST ignora NULL, e daria período a
-- uma linha cron, que heartbeats_one_schedule recusa.
UPDATE heartbeats
   SET period_sec = CASE WHEN period_sec > 2678400 THEN 2678400 ELSE period_sec END,
       grace_sec = LEAST(grace_sec, 604800),
       description = left(description, 1000),
       deadline_at = CASE
           WHEN period_sec > 2678400
           THEN LEAST(deadline_at, now() + make_interval(secs => 2678400 + LEAST(grace_sec, 604800)))
           ELSE deadline_at
       END
 WHERE period_sec > 2678400 OR grace_sec > 604800 OR char_length(description) > 1000;
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_period_sec_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_period_sec_check
    CHECK (period_sec BETWEEN 60 AND 2678400);
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_grace_sec_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_grace_sec_check
    CHECK (grace_sec BETWEEN 30 AND 604800);
ALTER TABLE heartbeats DROP CONSTRAINT heartbeats_description_check;
ALTER TABLE heartbeats ADD CONSTRAINT heartbeats_description_check
    CHECK (char_length(description) <= 1000);
