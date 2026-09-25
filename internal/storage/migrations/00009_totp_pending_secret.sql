-- +goose Up
-- O segredo do autenticador NOVO durante a troca (Plano 9, §3.2): o rotate o
-- grava aqui, e só a confirmação com um código dele o promove a
-- totp_secret_enc. Até lá o segredo antigo continua valendo, e quem desiste
-- no meio não fica sem 2FA — que era o preço de trocar de celular
-- desativando e ativando de novo.
--
-- Cifrado com o mesmo crypto.Box, com a AAD amarrada a esta coluna
-- (crypto.AAD("users", "totp_pending_secret_enc", id)): o selo não abre em
-- totp_secret_enc nem na linha de outro usuário, e por isso a confirmação
-- decifra e cifra de novo em vez de copiar os bytes.
--
-- Anulável e sem DEFAULT: só catálogo, sem reescrever a tabela. NULL quer
-- dizer "nenhuma troca em andamento".
ALTER TABLE users ADD COLUMN totp_pending_secret_enc bytea;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_pending_secret_enc;
