-- +goose Up
-- Tags dos heartbeats, na mesma forma das tags dos alvos (00017): tabela
-- dependente, com a chave primária cobrindo a FK.
CREATE TABLE heartbeat_tags (
    heartbeat_id uuid NOT NULL REFERENCES heartbeats(id) ON DELETE CASCADE,
    tag          text NOT NULL,
    PRIMARY KEY (heartbeat_id, tag),
    CONSTRAINT heartbeat_tags_tag_valida
        CHECK (char_length(tag) BETWEEN 1 AND 24 AND tag !~ '[[:space:][:cntrl:],]')
);

-- A política nomeia o dono, e não delega só à linha do heartbeat, como faz
-- heartbeat_events: o escopo do ping enxerga a linha do próprio token, e não
-- tem motivo para ler tags. Sondador e avaliador também não as usam.
ALTER TABLE heartbeat_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE heartbeat_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY heartbeat_tags_owner ON heartbeat_tags
    USING (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid))
    WITH CHECK (EXISTS (SELECT 1 FROM heartbeats h WHERE h.id = heartbeat_tags.heartbeat_id
                      AND h.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));

-- +goose Down
DROP TABLE heartbeat_tags;
