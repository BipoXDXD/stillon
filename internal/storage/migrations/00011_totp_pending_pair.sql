-- +goose Up
-- Uma troca pendente só existe com o segredo novo E a família que a criou.
-- A aplicação grava/limpa o par em uma query, mas queries futuras e SQL de
-- operação também devem respeitar a relação (dívida do PR #15, Plano 12).
-- IS NULL devolve booleanos definidos: nenhum estado incompleto passa pelo
-- UNKNOWN que um CHECK permitiria. Valida também as linhas existentes;
-- inconsistência recusa a migration, sem apagar ou reparar credenciais.
ALTER TABLE users ADD CONSTRAINT users_totp_pending_pair
    CHECK ((totp_pending_secret_enc IS NULL) = (totp_pending_family_id IS NULL));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_totp_pending_pair;
