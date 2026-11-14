package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Retenção (spec §12).
const (
	// ChecksRetention é a idade a partir da qual uma partição mensal de
	// checks pode sair. A partição sai inteira: os checks duram entre 30 e
	// 61 dias.
	ChecksRetention        = 30 * 24 * time.Hour
	RollupRetentionMonths  = 13
	AuditRetentionMonths   = 12
	SessionGrace           = 7 * 24 * time.Hour
	NotificationsRetention = 30 * 24 * time.Hour
	// HeartbeatEventsRetention é a idade máxima de um evento de heartbeat.
	HeartbeatEventsRetention = 30 * 24 * time.Hour
	// FamilyProofsDeadGrace é quanto o contador de provas de uma família que já
	// não está viva fica antes da retenção: as provas em voo dela, no máximo um
	// punhado de Argon2id sob o prazo de 15 s da requisição, ainda precisam da
	// linha para contar.
	FamilyProofsDeadGrace = time.Minute
	// FamilyProofsSweepBatch é o teto de linhas por rodada: a tabela tem uma
	// linha por família que errou prova, e o resto sai na rodada seguinte.
	FamilyProofsSweepBatch = 1000
)

const (
	// retentionBatch é o teto de linhas de um lote da retenção: pequeno o
	// bastante para o DELETE terminar em segundos e não segurar locks.
	retentionBatch = 5000
	// retentionMaxBatches é o teto de lotes por tabela numa rodada: com a rodada
	// a cada 15 min, até 100 mil linhas por tabela, e o resto na seguinte. Com
	// lotes de segundos, a rodada fica bem abaixo de maintenanceRoundTimeout;
	// se mesmo assim o prazo vencer, o DROP e a limpeza falham com ele e a
	// rodada seguinte tenta de novo.
	retentionMaxBatches = 20
)

const (
	// rollupReconsolidate é quanto antes da marca d'água cada rodada volta:
	// um check que chegou atrasado ainda entra na hora dele.
	rollupReconsolidate = 2 * time.Hour
	// rollupBackfill é até onde a primeira rodada volta, sem rollup nenhum:
	// o que ainda pode existir de checks brutos. A partição sai inteira, então
	// um check vive até 61 dias; 31 perderia a metade mais velha no primeiro
	// deploy.
	rollupBackfill = 62 * 24 * time.Hour
	// dropLockTimeout limita a espera do DROP da partição vencida. O DROP
	// pega ACCESS EXCLUSIVE em checks, e enquanto espera, enfileira atrás
	// dele o INSERT do sondador e as leituras da página pública. Melhor
	// desistir e tentar na próxima rodada.
	dropLockTimeout = 2 * time.Second
	// maintenanceRoundTimeout é o prazo de uma rodada inteira de RunMaintenance,
	// abaixo do intervalo de produção (15 min): presa, a rodada desiste, vai
	// ao log, e a seguinte tenta de novo.
	maintenanceRoundTimeout = 10 * time.Minute
	// maintenanceStatementTimeout substitui, nas transações da manutenção, o
	// statementTimeout do pool: a primeira consolidação volta 62 dias de
	// checks, e a primeira retenção apaga meses de linhas.
	maintenanceStatementTimeout = 5 * time.Minute
	// rollupLockKey nomeia a advisory lock da consolidação, hasheada no banco
	// como a de ensure_checks_partition (migration 00006).
	rollupLockKey = "stillon.consolidate_rollups"
)

var ErrMaintenanceInterval = errors.New("intervalo de manutenção precisa ser positivo")

// MaintenanceReport diz o que uma rodada fez, para o log.
type MaintenanceReport struct {
	RollupHours            int64
	DroppedPartitions      int32
	DeletedRollups         int64
	DeletedNotifications   int64
	DeletedHeartbeatEvents int64
	DeletedAudit           int64
	DeletedSessions        int64
	DeletedFamilyProofs    int64
}

// Maintain é uma rodada da manutenção (spec §12): garante as partições,
// consolida os rollups e aplica a retenção. Cada passo é idempotente, e
// rodar de novo no mesmo instante não muda nada.
func Maintain(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	var r MaintenanceReport
	if err := EnsureChecksPartitions(ctx, pool, now); err != nil {
		return r, err
	}
	n, err := ConsolidateRollups(ctx, pool, now)
	if err != nil {
		return r, err
	}
	r.RollupHours = n
	// Com o DROP desistindo ou uma tabela falhando, o resto da retenção já
	// valeu: o relatório parcial sobe junto com o erro, para o log dizer o que
	// foi feito.
	ret, err := ApplyRetention(ctx, pool, now)
	ret.RollupHours = r.RollupHours
	return ret, err
}

// ConsolidateRollups consolida as horas cheias ainda não consolidadas, até a
// última hora completa antes de now (em UTC). Volta rollupReconsolidate antes
// da marca d'água; sem rollup nenhum, volta rollupBackfill. Devolve quantas
// linhas de rollup gravou ou reescreveu.
//
// Com outra instância consolidando (a advisory lock rollupLockKey tomada),
// pula a rodada e devolve 0 sem erro: o upsert é idempotente, e a rodada
// seguinte cobre a janela.
//
// A marca d'água é o último rollup ANTES de ate. Um rollup no futuro (o relógio
// esteve adiantado quando foi gravado) não conta: com a marca no futuro, de ficava
// depois de ate e nenhuma hora era consolidada; limitada a ate, ela escondia a
// última marca real, e uma parada de mais de 2 h perdia para sempre as horas antes
// dela. O rollup do futuro fica; a retenção o apaga com a idade.
func ConsolidateRollups(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	ate := now.UTC().Truncate(time.Hour)
	var n int64
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		livre, err := q.TryLockRollups(ctx, rollupLockKey)
		if err != nil {
			return fmt.Errorf("travar a consolidação: %w", err)
		}
		if !livre {
			slog.InfoContext(ctx, "consolidação de rollups pulada: outra instância está consolidando")
			return nil
		}
		if err := q.SetLocalStatementTimeout(ctx, milissegundos(maintenanceStatementTimeout)); err != nil {
			return fmt.Errorf("prazo da consolidação: %w", err)
		}
		ultima, err := q.LatestRollupBucketBefore(ctx, ate)
		if err != nil {
			return fmt.Errorf("ler marca d'água dos rollups: %w", err)
		}
		de := ate.Add(-rollupBackfill)
		if ultima.Unix() > 0 {
			if volta := ultima.Add(-rollupReconsolidate); volta.After(de) {
				de = volta
			}
		}
		if !de.Before(ate) {
			return nil
		}
		n, err = q.ConsolidateRollups(ctx, db.ConsolidateRollupsParams{FromAt: de, ToAt: ate})
		if err != nil {
			return fmt.Errorf("consolidar rollups: %w", err)
		}
		return nil
	})
	return n, err
}

// ApplyRetention apaga o que venceu (spec §12). Pendência nunca sai por idade:
// uma notificação pendente ainda vai ser tentada.
//
// Cada tabela sai em lotes de até retentionBatch linhas, cada lote na sua
// transação, até retentionMaxBatches lotes por rodada: um DELETE único de
// meses de acúmulo passava do statement_timeout e voltava inteiro, rodada após
// rodada. O que passa do teto sai na rodada seguinte. Uma tabela que falha não
// segura as outras: o erro sobe com o nome dela, e o relatório conta só os
// lotes que comitaram.
//
// O DROP das partições vem sozinho, com lock_timeout, porque pega ACCESS
// EXCLUSIVE em checks e não pode segurá-la durante o resto da retenção. A
// limpeza dos contadores de provas das famílias mortas vem por último, na
// própria transação. Os dois rodam mesmo com uma tabela falhando; todos os
// erros sobem juntos, e a próxima rodada tenta de novo.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	return applyRetention(ctx, pool, now, retentionLimits{batch: retentionBatch, maxBatches: retentionMaxBatches})
}

// retentionLimits é o tamanho do lote e o teto de lotes por tabela numa
// rodada; o teste encolhe os dois.
type retentionLimits struct {
	batch      int32
	maxBatches int
}

// retentionTable é uma tabela da retenção por DELETE: o nome para o erro, onde
// o relatório soma e o DELETE de um lote.
type retentionTable struct {
	what    string
	deleted *int64
	delete  func(q *db.Queries, batch int32) (int64, error)
}

func applyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time, lim retentionLimits) (MaintenanceReport, error) {
	var r MaintenanceReport
	var errs []error
	for _, tbl := range retentionTables(ctx, now, &r) {
		n, err := deleteInBatches(ctx, pool, lim, tbl.delete)
		*tbl.deleted = n
		if err != nil {
			errs = append(errs, fmt.Errorf("apagar %s: %w", tbl.what, err))
		}
	}
	errs = append(errs, dropExpiredPartitions(ctx, pool, now, &r), sweepDeadFamilyProofs(ctx, pool, now, &r))
	return r, errors.Join(errs...)
}

func retentionTables(ctx context.Context, now time.Time, r *MaintenanceReport) []retentionTable {
	return []retentionTable{
		{"rollups vencidos", &r.DeletedRollups, func(q *db.Queries, batch int32) (int64, error) {
			return q.DeleteOldRollups(ctx, db.DeleteOldRollupsParams{Cutoff: now.AddDate(0, -RollupRetentionMonths, 0), Batch: batch})
		}},
		{"notificações vencidas", &r.DeletedNotifications, func(q *db.Queries, batch int32) (int64, error) {
			return q.DeleteOldNotifications(ctx, db.DeleteOldNotificationsParams{Cutoff: now.Add(-NotificationsRetention), Batch: batch})
		}},
		{"eventos de heartbeat vencidos", &r.DeletedHeartbeatEvents, func(q *db.Queries, batch int32) (int64, error) {
			return q.DeleteOldHeartbeatEvents(ctx, db.DeleteOldHeartbeatEventsParams{Cutoff: now.Add(-HeartbeatEventsRetention), Batch: batch})
		}},
		{"auditoria vencida", &r.DeletedAudit, func(q *db.Queries, batch int32) (int64, error) {
			return q.DeleteOldAudit(ctx, db.DeleteOldAuditParams{Cutoff: now.AddDate(0, -AuditRetentionMonths, 0), Batch: batch})
		}},
		{"sessões vencidas", &r.DeletedSessions, func(q *db.Queries, batch int32) (int64, error) {
			return q.DeleteExpiredSessions(ctx, db.DeleteExpiredSessionsParams{Cutoff: now.Add(-SessionGrace), Batch: batch})
		}},
	}
}

// deleteInBatches roda o DELETE de um lote, cada um na sua transação, até um
// lote vir incompleto (acabou o que venceu) ou chegar ao teto. Devolve quantas
// linhas os lotes comitados apagaram.
func deleteInBatches(ctx context.Context, pool *pgxpool.Pool, lim retentionLimits, del func(*db.Queries, int32) (int64, error)) (int64, error) {
	var total int64
	for range lim.maxBatches {
		var n int64
		err := InProberTx(ctx, pool, func(q *db.Queries) error {
			if err := q.SetLocalStatementTimeout(ctx, milissegundos(maintenanceStatementTimeout)); err != nil {
				return fmt.Errorf("prazo do lote: %w", err)
			}
			var err error
			n, err = del(q, lim.batch)
			return err
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(lim.batch) {
			break
		}
	}
	return total, nil
}

func dropExpiredPartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time, r *MaintenanceReport) error {
	return InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.SetLocalLockTimeout(ctx, milissegundos(dropLockTimeout)); err != nil {
			return fmt.Errorf("limitar a espera do DROP: %w", err)
		}
		var err error
		if r.DroppedPartitions, err = q.DropChecksPartitionsBefore(ctx, now.Add(-ChecksRetention)); err != nil {
			return fmt.Errorf("apagar partições vencidas: %w", err)
		}
		return nil
	})
}

func sweepDeadFamilyProofs(ctx context.Context, pool *pgxpool.Pool, now time.Time, r *MaintenanceReport) error {
	return InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.SetLocalStatementTimeout(ctx, milissegundos(maintenanceStatementTimeout)); err != nil {
			return fmt.Errorf("prazo da limpeza dos contadores de provas: %w", err)
		}
		var err error
		if r.DeletedFamilyProofs, err = q.DeleteDeadFamilyProofs(ctx, db.DeleteDeadFamilyProofsParams{
			DeadBefore: now.Add(-FamilyProofsDeadGrace), Now: now, Batch: FamilyProofsSweepBatch,
		}); err != nil {
			return fmt.Errorf("apagar contadores de provas de famílias mortas: %w", err)
		}
		return nil
	})
}

// RunMaintenance roda Maintain ao subir e depois a cada every, até ctx acabar.
// Substitui o antigo MaintainChecksPartitions, que não tinha teste e dava
// panic com every <= 0. Erro de uma rodada vai ao log; a próxima tenta de novo.
// Cada rodada tem o prazo maintenanceRoundTimeout, nunca maior que every.
func RunMaintenance(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
	if every <= 0 {
		return ErrMaintenanceInterval
	}
	prazo := min(maintenanceRoundTimeout, every)
	rodar := func() {
		rodada, cancel := context.WithTimeout(ctx, prazo)
		defer cancel()
		r, err := Maintain(rodada, pool, clk.Now())
		if err != nil && ctx.Err() != nil {
			return
		}
		nivel := slog.LevelInfo
		atributos := []any{"rollups", r.RollupHours, "particoes_apagadas", r.DroppedPartitions,
			"rollups_apagados", r.DeletedRollups, "notificacoes_apagadas", r.DeletedNotifications,
			"eventos_de_heartbeat_apagados", r.DeletedHeartbeatEvents,
			"auditoria_apagada", r.DeletedAudit, "sessoes_apagadas", r.DeletedSessions,
			"contadores_de_provas_apagados", r.DeletedFamilyProofs}
		if err != nil {
			nivel = slog.LevelError
			atributos = append(atributos, "erro", err)
		}
		slog.Log(ctx, nivel, "manutenção", atributos...)
	}
	rodar()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			rodar()
		}
	}
}
