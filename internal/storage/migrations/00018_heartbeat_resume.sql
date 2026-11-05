-- +goose Up
-- O incidente de heartbeat só fecha com um sucesso ou com a retomada: editar o
-- agendamento move o prazo, e prazo novo não encerra alarme sozinho (§5 da
-- spec de heartbeats). A 00018 vem depois da 00017, das tags: storage.Migrate
-- recusa uma versão menor que a do banco.

-- Quando o dono retomou o heartbeat pela última vez. É o que o avaliador
-- compara com o início do incidente: retomar antes de o incidente abrir não
-- fecha nada.
ALTER TABLE heartbeats ADD COLUMN resumed_at timestamptz;

-- Como o incidente acabou. O texto do aviso sai só da linha e do incidente, e
-- a retentativa repete o texto da primeira: derivar isto do heartbeat, que muda
-- a cada ping, trocaria o corpo entre as tentativas, e o Resend recusa a mesma
-- Idempotency-Key com outro corpo.
ALTER TABLE incidents ADD COLUMN closed_by_resume bool NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE incidents DROP COLUMN closed_by_resume;
ALTER TABLE heartbeats DROP COLUMN resumed_at;
