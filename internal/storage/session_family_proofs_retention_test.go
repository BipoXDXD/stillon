package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A retenção apaga o contador de provas da família que deixou de existir
// (sem sessão viva), em lote, depois da folga; e nunca o da família viva, por
// mais parada que esteja. Sabotagem: tirar o NOT EXISTS da query apaga também
// a viva; tirar o LIMIT apaga mais que o lote; trocar a folga por zero apaga a
// recém-derrubada.
func TestRetentionDeletesDeadFamilyProofsInBatchesAndKeepsLiveOnes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)

	// Mortas: sem nenhuma sessão viva, paradas há mais que a folga.
	const mortas = storage.FamilyProofsSweepBatch + 5
	velha := agora.Add(-time.Hour)
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		 SELECT gen_random_uuid(), $1, gen_random_uuid(), 1, 1, $2 FROM generate_series(1, $3::int)`,
		dono, velha, mortas); err != nil {
		t.Fatal(err)
	}
	// Viva e parada há dias: fica.
	viva := uuid.New()
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), dono, viva, []byte("hash-da-viva"), agora.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		 VALUES ($1, $2, $3, 4, 4, $4)`, viva, dono, uuid.New(), agora.Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Morta há menos que a folga: as provas em voo ainda a usam.
	recente := uuid.New()
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		 VALUES ($1, $2, $3, 1, 1, $4)`, recente, dono, uuid.New(), agora.Add(-storage.FamilyProofsDeadGrace/2)); err != nil {
		t.Fatal(err)
	}

	r, err := storage.ApplyRetention(ctx, pool, agora)
	if err != nil {
		t.Fatal(err)
	}
	if r.DeletedFamilyProofs != storage.FamilyProofsSweepBatch {
		t.Errorf("a primeira rodada apagou %d contadores, queria o lote de %d", r.DeletedFamilyProofs, storage.FamilyProofsSweepBatch)
	}
	r, err = storage.ApplyRetention(ctx, pool, agora)
	if err != nil {
		t.Fatal(err)
	}
	if r.DeletedFamilyProofs != 5 {
		t.Errorf("a segunda rodada apagou %d contadores, queria os 5 que sobraram do lote", r.DeletedFamilyProofs)
	}

	var restam int
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT count(*) FROM session_family_proofs`).Scan(&restam); err != nil {
		t.Fatal(err)
	}
	if restam != 2 {
		t.Errorf("%d contadores restaram, queria 2: o da família viva e o da recém-derrubada", restam)
	}
	var daViva int
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT count(*) FROM session_family_proofs WHERE family_id = $1`, viva).Scan(&daViva); err != nil || daViva != 1 {
		t.Errorf("contador da família viva: %d linhas (erro %v), queria 1", daViva, err)
	}
}

// Com a limpeza dos contadores de provas falhando, as retenções que já existiam
// ainda rodam (sessões vencidas, auditoria, DROP das partições) e o erro sobe,
// agregado, sem ser escondido. Sabotagem: a limpeza dentro da transação das
// outras retenções desfaz os DELETEs e pula o DROP.
func TestRetentionStillRunsTheOldStepsWhenTheFamilyProofsCleanupFails(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hoje := hora(12, 0)
	dono := novoUsuario(t, pool)
	for _, mes := range []time.Time{hora(0, 0).AddDate(0, -2, 0), hoje} {
		if err := storage.EnsureChecksPartitions(ctx, pool, mes); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testdb.Admin(t).Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, novoID(), novoID().String(), hoje.AddDate(0, 0, -8)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, user_id, action, at) VALUES ($1, $2, 'x', $3)`,
		novoID(), dono, hoje.AddDate(0, -13, 0)); err != nil {
		t.Fatal(err)
	}
	// A limpeza nova falha: a tabela sumiu (CASCADE leva junto a política
	// sessions_prober_select_proof_family da 00030, que a referencia).
	if _, err := pool.Exec(ctx, `DROP TABLE session_family_proofs CASCADE`); err != nil {
		t.Fatal(err)
	}

	r, err := storage.ApplyRetention(ctx, pool, hoje)

	if err == nil || !strings.Contains(err.Error(), "contadores de provas") {
		t.Errorf("erro = %v, queria o erro da limpeza dos contadores, sem escondê-lo", err)
	}
	if r.DeletedSessions != 1 || r.DeletedAudit != 1 {
		t.Errorf("relatório = %+v; queria a sessão e a auditoria vencidas apagadas apesar da falha", r)
	}
	if r.DroppedPartitions < 1 || existeTabela(t, pool, "checks_2026_07") {
		t.Errorf("a partição vencida não foi apagada (DroppedPartitions=%d): a falha pulou o DROP", r.DroppedPartitions)
	}
}
