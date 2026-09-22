-- +goose Up
-- O último timestep aceito por usuário. A RFC 6238 §5.2 manda o verificador
-- recusar um segundo OTP para o mesmo timestep, e sem esta coluna um código
-- observado valia por toda a janela (o período mais o skew, ~90s): quem
-- lesse o código por cima do ombro, por phishing ao vivo ou por malware de
-- tela podia reusá-lo enquanto ele não expirasse.
--
-- Vai no banco, e não em memória, por duas razões: sobrevive a reinício, e
-- vale entre instâncias — um cache local deixaria o réplica B aceitar o que
-- o réplica A acabou de consumir.
ALTER TABLE users ADD COLUMN totp_last_timestep bigint;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_last_timestep;
