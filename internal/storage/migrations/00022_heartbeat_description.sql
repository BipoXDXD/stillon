-- +goose Up
-- O que o dono quer lembrar da tarefa: o que ela faz, onde roda, o que fazer
-- quando falha. O log não guarda o corpo do ping (D14), e sem isto essa
-- informação fica fora do painel. '' é "sem descrição": o texto nunca é
-- ausente de duas formas. O binário anterior não cita a coluna e segue
-- inserindo com o padrão.
ALTER TABLE heartbeats ADD COLUMN description text NOT NULL DEFAULT ''
    CHECK (char_length(description) <= 1000);

-- +goose Down
ALTER TABLE heartbeats DROP COLUMN description;
