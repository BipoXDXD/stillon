-- +goose Up
-- Suspensão derivada (spec §Cotas e abuso): o projeto some dos workers e da
-- página pública enquanto o titular estiver suspenso; nada é apagado.
-- Os workers (sondador, avaliador de heartbeats, claim das notificações e
-- lembretes) rodam no escopo do sondador; a página pública, no do slug. As
-- duas políticas abaixo só abrem a leitura de projects a esses escopos, para
-- projeto_suspenso achar o titular. Sem ciclo de RLS (42P17): as políticas de
-- status_pages não consultam projects.
SET LOCAL lock_timeout = '5s';

CREATE POLICY projects_prober_select ON projects FOR SELECT
    USING (current_setting('app.scope', true) = 'prober');
CREATE POLICY projects_public_select ON projects FOR SELECT
    USING (EXISTS (SELECT 1 FROM status_pages s
                    WHERE s.project_id = projects.id AND s.published
                      AND s.slug = NULLIF(current_setting('app.public_slug', true), '')));

-- SECURITY INVOKER: enxerga só o que o escopo de quem chama enxerga.
-- Projeto nulo (linha anterior ao contract) nunca está suspenso.
-- +goose StatementBegin
CREATE FUNCTION projeto_suspenso(projeto uuid) RETURNS boolean
    LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
    SELECT EXISTS (SELECT 1 FROM projects p JOIN users u ON u.id = p.titular_id
                    WHERE p.id = projeto AND u.suspended_at IS NOT NULL) $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP FUNCTION projeto_suspenso(uuid);
DROP POLICY projects_public_select ON projects;
DROP POLICY projects_prober_select ON projects;
