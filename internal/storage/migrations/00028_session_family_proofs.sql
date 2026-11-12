-- +goose Up
-- Contador de provas erradas por família de sessão (#324). Até aqui ele vivia
-- na memória do processo e um restart ou deploy devolvia as cinco tentativas a
-- quem tinha uma sessão roubada.
--
-- Só aditiva: tabela nova com FK e dois índices. Nada existente muda, e o
-- binário anterior não cita a tabela e segue funcionando.
--
-- lock_timeout de 5 s (Ruling): a migration roda no boot, sob o tráfego da
-- API. A FK pega SHARE ROW EXCLUSIVE em users. Sem prazo, uma leitura longa
-- enfileiraria todo login atrás da migration. Se a trava não vier, a
-- transação desfaz tudo e o próximo boot tenta de novo.
SET LOCAL lock_timeout = '5s';

CREATE TABLE session_family_proofs (
    -- sessions.family_id não é chave de sessions (uma família tem uma linha por
    -- refresh), então não há FK para a família: a do usuário apagado é a FK
    -- para users, e a da família revogada ou vencida é a retenção periódica
    -- (DeleteDeadFamilyProofs). Família viva nunca perde a linha: a vida dela
    -- já é limitada pelo prazo do refresh.
    --
    -- Nada de gatilho em sessions que apague a linha ao revogar: as provas em
    -- voo da família (5 Argon2id) ainda precisam dela para contar, e quem as
    -- derruba são as reservas esgotadas, que revogam antes de elas terminarem.
    family_id  uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Identifica a "vida" do contador: o sucesso apaga a linha, e a próxima
    -- reserva nasce com outra geração. Reserva, falha e devolução de uma vida
    -- anterior não casam e não mexem na nova.
    generation uuid NOT NULL,
    -- reserved = provas em voo + provas erradas. A reserva vem antes da
    -- verificação, então N requisições simultâneas gastam N provas.
    reserved   bigint NOT NULL CHECK (reserved >= 0),
    errors     bigint NOT NULL CHECK (errors >= 0),
    -- Relógio da aplicação, o mesmo das sessões. Serve à folga da retenção
    -- das famílias mortas.
    updated_at timestamptz NOT NULL,
    CONSTRAINT session_family_proofs_errors_le_reserved CHECK (errors <= reserved)
);

-- A coluna da FK: sem índice, apagar um usuário varreria a tabela.
CREATE INDEX session_family_proofs_user_idx ON session_family_proofs (user_id);
-- A retenção das famílias mortas (revogadas ou vencidas), por updated_at.
CREATE INDEX session_family_proofs_updated_idx ON session_family_proofs (updated_at);

-- +goose Down
-- Perde os contadores em curso: as famílias voltam a ter o orçamento cheio.
SET LOCAL lock_timeout = '5s';

DROP TABLE session_family_proofs;
