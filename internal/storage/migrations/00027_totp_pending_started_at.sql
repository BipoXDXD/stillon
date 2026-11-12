-- +goose Up
-- O instante em que a troca pendente do segredo TOTP foi iniciada (#326). Com
-- ele a troca ganha prazo próprio (auth.PendingRotationTTL): antes, ela valia
-- enquanto a família da sessão vivesse, até RefreshTTL (30 dias).
--
-- Só aditiva: coluna anulável, sem DEFAULT e sem CHECK novo, então é só
-- catálogo e o binário anterior segue gravando e limpando o pendente sem
-- citá-la. O par users_totp_pending_pair (00011) não muda. A coluna só tem
-- sentido com o pendente presente; limpar o pendente a deixa como está, e o
-- próximo StartRotation a sobrescreve. NULL com pendente (linha antiga, ou
-- gravada pelo binário anterior) é troca vencida: a confirmação a recusa como
-- "sem troca pendente", e o dono inicia outra.
--
-- lock_timeout de 5 s pelo mesmo motivo da 00026: o ALTER pega ACCESS
-- EXCLUSIVE em users e não pode enfileirar o tráfego atrás de uma leitura longa.
SET LOCAL lock_timeout = '5s';

ALTER TABLE users ADD COLUMN totp_pending_started_at timestamptz;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_pending_started_at;
