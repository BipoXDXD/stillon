-- +goose Up
-- Tags dos alvos (spec do painel denso). Tabela dependente, e não coluna com
-- lista: a tag é chave natural do par, e ON DELETE CASCADE leva as tags junto
-- com o alvo. A chave primária começa em target_id, então já cobre a FK.
--
-- O CHECK é a segunda barreira, independente do código: tamanho e nada de
-- espaço, controle ou vírgula. A allowlist fina (minúsculas, letras, dígitos,
-- hífen e sublinhado) mora em targets.ParseTag, porque [[:alpha:]] e lower()
-- do Postgres dependem do locale do banco. O teto de tags por alvo também: uma
-- regra sobre várias linhas não cabe num CHECK.
CREATE TABLE target_tags (
    target_id uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    tag       text NOT NULL,
    PRIMARY KEY (target_id, tag),
    CONSTRAINT target_tags_tag_valida
        CHECK (char_length(tag) BETWEEN 1 AND 24 AND tag !~ '[[:space:][:cntrl:],]')
);

-- Sem user_id: a RLS delega ao alvo, como em checks e check_rollup_1h.
ALTER TABLE target_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE target_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY target_tags_via_target ON target_tags
    USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id))
    WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tags.target_id));

-- +goose Down
DROP TABLE target_tags;
