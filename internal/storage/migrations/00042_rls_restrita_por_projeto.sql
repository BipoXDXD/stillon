-- +goose Up
-- Políticas RESTRITIVAS: entram em AND com as permissivas (as de dono por
-- user_id continuam até o contract). Só estreitam: com app.project_id
-- ligado (só storage.InProjectTx o liga, depois de conferir a pertença), a
-- linha tem de ser daquele projeto; sem ele (InUserTx do binário anterior,
-- sondador, ping, verify, página pública), valem true. As tabelas de posse
-- indireta herdam pelo EXISTS que já fazem no pai. NULLIF pelo motivo da
-- 00004 (GUC vira '' na conexão reusada).
SET LOCAL lock_timeout = '5s';

CREATE POLICY targets_so_projeto_do_escopo ON targets AS RESTRICTIVE FOR ALL
    USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
           OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY heartbeats_so_projeto_do_escopo ON heartbeats AS RESTRICTIVE FOR ALL
    USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
           OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY notif_channels_so_projeto_do_escopo ON notif_channels AS RESTRICTIVE FOR ALL
    USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
           OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
CREATE POLICY status_pages_so_projeto_do_escopo ON status_pages AS RESTRICTIVE FOR ALL
    USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
           OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
    WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);

-- Segundo passe: linhas que o binário anterior gravou sem projeto entre a
-- 00041 e aqui (usuário criado por binário anterior ao PR 1).
SELECT backfill_projetos_padrao();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Só as políticas caem: o backfill não se desfaz (os project_id gravados
-- valem para o binário anterior, que os ignora).
DROP POLICY status_pages_so_projeto_do_escopo ON status_pages;
DROP POLICY notif_channels_so_projeto_do_escopo ON notif_channels;
DROP POLICY heartbeats_so_projeto_do_escopo ON heartbeats;
DROP POLICY targets_so_projeto_do_escopo ON targets;
