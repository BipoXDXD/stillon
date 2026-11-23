-- +goose Up
-- Uma status page por projeto, não mais por usuário: o titular de dois
-- projetos tem uma página em cada. O limite que fica é o índice único
-- status_pages_project_key da 00040. O serviço continua traduzindo
-- status_pages_user_id_key em ErrExists até o PR 7, para o rollback.
SET LOCAL lock_timeout = '5s';

ALTER TABLE status_pages DROP CONSTRAINT status_pages_user_id_key;

-- +goose Down
-- Devolve o UNIQUE por usuário. Se algum usuário já tiver duas páginas, o
-- ADD CONSTRAINT falha com 23505 e o down para sem apagar nada: escolher qual
-- página sai é decisão do dono, não da migration.
SET LOCAL lock_timeout = '5s';

ALTER TABLE status_pages ADD CONSTRAINT status_pages_user_id_key UNIQUE (user_id);
