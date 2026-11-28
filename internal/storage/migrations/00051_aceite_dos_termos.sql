-- +goose Up
-- PR 6.1 (textos legais e aceite): o cadastro grava a versão dos termos que a
-- pessoa aceitou e quando. Expand seguro: o binário anterior insere sem as
-- colunas, as duas ficam NULL e o CHECK aceita. As contas anteriores ficam
-- sem aceite registrado.
SET LOCAL lock_timeout = '5s';
ALTER TABLE users ADD COLUMN terms_version text, ADD COLUMN terms_accepted_at timestamptz;
ALTER TABLE users ADD CONSTRAINT users_aceite_em_par
  CHECK ((terms_version IS NULL) = (terms_accepted_at IS NULL));

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE users DROP CONSTRAINT users_aceite_em_par, DROP COLUMN terms_accepted_at, DROP COLUMN terms_version;
