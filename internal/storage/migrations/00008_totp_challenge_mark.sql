-- +goose Up
-- A marca d'água do desafio de 2FA (Plano 8): o instante de emissão do último
-- desafio consumido pelo /auth/totp/verify. Um desafio só abre sessão se for
-- mais novo que a marca, e abri-la a avança; assim o mesmo desafio não abre
-- uma segunda sessão. NULL: nenhum desafio consumido ainda.
ALTER TABLE users ADD COLUMN totp_challenge_mark timestamptz;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_challenge_mark;
