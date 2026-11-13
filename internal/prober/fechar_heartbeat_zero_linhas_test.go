package prober_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// fechadoComAviso abre o incidente, manda o sucesso e fecha num tique, deixando
// o incidente resolvido com os avisos de abertura e de fechamento. Devolve a
// linha que um tique concorrente teria lido antes do commit desse fechamento.
func fechadoComAviso(t *testing.T, b *bancadaHB) (uuid.UUID, db.ClaimHeartbeatsToCloseRow) {
	t.Helper()
	id, token := b.heartbeat(t, "backup")
	b.canal(t)
	b.clk.Advance(2 * time.Hour)
	b.tick(t)
	b.clk.Advance(10 * time.Minute)
	sucesso := b.clk.Now()
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Second)
	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria o fechamento", n)
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Fim == nil || len(l[0].Avisos) != 2 {
		t.Fatalf("incidentes antes da reivindicação velha: %+v", l)
	}
	velha := db.ClaimHeartbeatsToCloseRow{
		ID: id, UserID: b.dono, LastSuccessAt: &sucesso, IncidentID: l[0].ID, StartedAt: l[0].Inicio,
	}
	return id, velha
}

// Uma reivindicação velha chega ao fechamento depois que outro tique já
// resolveu e avisou: o UPDATE resolve 0 linhas. O "voltou" não se repete e o
// fim gravado não muda, qualquer que seja a barreira que segure o aviso: o
// fechamento sair ao ver 0 linhas, ou, se ele ainda chamar o aviso, o
// MarkCloseNotified que marca uma vez só e o ON CONFLICT do
// EnqueueNotification que descarta a segunda linha do mesmo (incidente, canal,
// evento).
func TestFechamentoComReivindicacaoVelhaNaoRepeteOAviso(t *testing.T) {
	b := novaBancadaHB(t)
	id, velha := fechadoComAviso(t, b)
	antes := b.incidentes(t, id)[0]

	b.clk.Advance(time.Minute)
	if err := prober.FecharComReivindicacao(t.Context(), b.pool, velha, b.clk.Now()); err != nil {
		t.Fatalf("fechamento com 0 linhas devolveu erro: %v", err)
	}

	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Fim == nil || !l[0].Fim.Equal(*antes.Fim) {
		t.Fatalf("incidentes: %+v; queria o fim de antes, %v", l, *antes.Fim)
	}
	if len(l[0].Avisos) != 2 || l[0].Avisos[1] != "resolved:backup" {
		t.Errorf("avisos: %v; queria um só resolved", l[0].Avisos)
	}
}

// O aviso de fechamento só sai quando o UPDATE deste tique resolveu a linha.
// Hoje nenhum caminho deixa um incidente de heartbeat resolvido sem
// close_notified_at, porque resolver e avisar vão na mesma transação; se um
// caminho assim surgir (fechamento manual, limpeza, script de suporte), uma
// reivindicação velha não pode enfileirar um "voltou" que ninguém pediu.
func TestFechamentoComZeroLinhasNaoAvisaIncidenteResolvidoPorOutroCaminho(t *testing.T) {
	b := novaBancadaHB(t)
	id, velha := fechadoComAviso(t, b)
	// Simula outro caminho que resolveu sem avisar.
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE incidents SET close_notified_at = NULL WHERE id = $1`, velha.IncidentID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("limpar close_notified_at: %v, %d linhas", err, tag.RowsAffected())
	}
	tag, err = tx.Exec(ctx, `DELETE FROM notifications WHERE incident_id = $1 AND event = 'resolved'`, velha.IncidentID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("apagar o resolved: %v, %d linhas", err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	b.clk.Advance(time.Minute)
	if err := prober.FecharComReivindicacao(t.Context(), b.pool, velha, b.clk.Now()); err != nil {
		t.Fatalf("fechamento com 0 linhas devolveu erro: %v", err)
	}

	if l := b.incidentes(t, id); len(l) != 1 || len(l[0].Avisos) != 1 {
		t.Errorf("avisos: %+v; queria só o de abertura, porque este fechamento não resolveu nada", l)
	}
}
