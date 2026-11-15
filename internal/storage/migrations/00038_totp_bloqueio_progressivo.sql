-- +goose Up
-- Bloqueio progressivo do segundo fator, separado do bloqueio da conta (spec
-- §5.2). Só /totp/verify, que exige a senha certa antes, mexe nestas colunas.
--
-- totp_failures conta os códigos errados desde o último acerto: o fim de um
-- bloqueio não a zera, e é isso que faz cada bloqueio durar o dobro do
-- anterior. totp_locked_until é 'infinity' quando a contagem chega ao teto
-- absoluto; só o reset-2fa do CLI (ResetTOTP) libera.
--
-- totp_lock_alert_at é a outbox do e-mail ao dono, na própria linha: o
-- instante do último bloqueio ainda não avisado. Gravá-lo no mesmo UPDATE que
-- trava evita a escrita dupla (banco e depois e-mail), e o envio o apaga só se
-- ele não mudou (auth.SendTOTPLockAlerts). Bloqueios seguidos sem envio no
-- meio (e-mail fora do ar) viram um aviso só, o do último. A natureza
-- do aviso fica gravada com o instante, para permanecer estável na retentativa.
ALTER TABLE users
    ADD COLUMN totp_failures integer NOT NULL DEFAULT 0 CHECK (totp_failures >= 0),
    ADD COLUMN totp_locked_until timestamptz,
    ADD COLUMN totp_lock_alert_at timestamptz,
    ADD COLUMN totp_lock_alert_permanent boolean NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE users
    DROP COLUMN totp_lock_alert_permanent,
    DROP COLUMN totp_lock_alert_at,
    DROP COLUMN totp_locked_until,
    DROP COLUMN totp_failures;
