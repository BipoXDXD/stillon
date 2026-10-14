-- +goose Up
-- Lembretes de incidente aberto (docs/superpowers/specs/2026-10-14-lembretes-design.md).
-- Cada lembrete é uma linha própria da outbox, numerada por seq dentro do par
-- (incidente, canal). Abertura e fechamento ficam com seq 0, e o UNIQUE
-- continua garantindo um de cada por par; dois sondadores que calculam o
-- mesmo lembrete colidem no seq, e só um insere (ON CONFLICT DO NOTHING).
-- O ON CONFLICT do binário anterior nomeia o UNIQUE antigo: o deploy é de
-- passo único, com o goose rodando no boot do binário novo.
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
    CHECK (event IN ('opened', 'resolved', 'reminder'));

ALTER TABLE notifications ADD COLUMN seq int NOT NULL DEFAULT 0;
-- seq 0 é de abertura e fechamento; um lembrete começa em 1.
ALTER TABLE notifications ADD CONSTRAINT notifications_seq_check
    CHECK ((event = 'reminder') = (seq >= 1) AND seq >= 0);

ALTER TABLE notifications DROP CONSTRAINT notifications_incident_id_channel_id_event_key;
ALTER TABLE notifications ADD CONSTRAINT notifications_incident_channel_event_seq_key
    UNIQUE (incident_id, channel_id, event, seq);

-- O nome do alvo no enfileiramento. O corpo do aviso sai só da linha e do
-- incidente, e a retentativa repete o texto da primeira tentativa: o Resend
-- recusa a mesma Idempotency-Key com outro corpo, e renomear o alvo entre
-- as tentativas faria o aviso falhar de vez. NULL só nas linhas de antes
-- desta migration; o dispatcher cai no nome atual do alvo.
ALTER TABLE notifications ADD COLUMN target_name text;

-- +goose Down
DELETE FROM notifications WHERE event = 'reminder';
ALTER TABLE notifications DROP CONSTRAINT notifications_incident_channel_event_seq_key;
ALTER TABLE notifications ADD CONSTRAINT notifications_incident_id_channel_id_event_key
    UNIQUE (incident_id, channel_id, event);
ALTER TABLE notifications DROP COLUMN target_name;
ALTER TABLE notifications DROP CONSTRAINT notifications_seq_check;
ALTER TABLE notifications DROP COLUMN seq;
ALTER TABLE notifications DROP CONSTRAINT notifications_event_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_event_check
    CHECK (event IN ('opened', 'resolved'));
