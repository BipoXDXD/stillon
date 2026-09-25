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

-- A família de sessão que iniciou a troca (revisão final do Plano 9): só ela
-- confirma. Sem isto, o pendente sobrevivia à revogação da sessão que o criou
-- (DELETE /sessions/{id}, logout) e outra sessão viva o confirmava. Anda
-- junto com o pendente: gravada no rotate, apagada em toda limpeza dele.
-- Anulável e sem DEFAULT, pelo mesmo motivo; sem chave estrangeira, porque
-- family_id não é chave de sessions (uma família tem várias linhas).
ALTER TABLE users ADD COLUMN totp_pending_family_id uuid;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_pending_family_id;
ALTER TABLE users DROP COLUMN totp_pending_secret_enc;
