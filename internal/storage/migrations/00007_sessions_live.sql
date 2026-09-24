-- +goose Up
-- As sessões vivas de um usuário: GET /sessions e a checagem de família viva
-- das rotas de credencial (Plano 5). Sem ele, sessions_user_idx lê todas as
-- linhas do usuário, inclusive as já rotacionadas, que passam de mil por
-- família em uso (uma a cada ~15 min, por até 37 dias antes da retenção).
CREATE INDEX sessions_user_live_idx ON sessions (user_id, family_id)
    WHERE used_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP INDEX sessions_user_live_idx;
