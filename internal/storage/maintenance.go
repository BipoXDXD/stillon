package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
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
)

const (
	// rollupReconsolidate é quanto antes da marca d'água cada rodada volta:
	// um check que chegou atrasado ainda entra na hora dele.
	rollupReconsolidate = 2 * time.Hour
	// rollupBackfill é até onde a primeira rodada volta, sem rollup nenhum:
	// o que ainda pode existir de checks brutos. A partição sai inteira, então
	// um check vive até 61 dias; 31 perderia a metade mais velha no primeiro
	// deploy (achado da revisão).
	rollupBackfill = 62 * 24 * time.Hour
	// dropLockTimeout limita a espera do DROP da partição vencida. O DROP
	// pega ACCESS EXCLUSIVE em checks, e enquanto espera, enfileira atrás
	// dele o INSERT do sondador e as leituras da página pública. Melhor
	// desistir e tentar na próxima rodada.
	dropLockTimeout = 2 * time.Second
)

var ErrMaintenanceInterval = errors.New("intervalo de manutenção precisa ser positivo")

// MaintenanceReport diz o que uma rodada fez, para o log.
type MaintenanceReport struct {
	RollupHours          int64
	DroppedPartitions    int32
	DeletedRollups       int64
	DeletedNotifications int64
	DeletedAudit         int64
	DeletedSessions      int64
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
	ret, err := ApplyRetention(ctx, pool, now)
	if err != nil {
		return r, err
	}
	ret.RollupHours = r.RollupHours
	return ret, nil
}

// ConsolidateRollups consolida as horas cheias ainda não consolidadas, até a
// última hora completa antes de now (em UTC). Volta rollupReconsolidate antes
// da marca d'água; sem rollup nenhum, volta rollupBackfill. Devolve quantas
// linhas de rollup gravou ou reescreveu.
func ConsolidateRollups(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	ate := now.UTC().Truncate(time.Hour)
	var n int64
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		ultima, err := q.LatestRollupBucket(ctx)
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
// São duas transações. Os DELETEs vêm primeiro; o DROP das partições vem
// sozinho, com lock_timeout, porque pega ACCESS EXCLUSIVE em checks e não
// pode segurá-la durante o resto da retenção (achado da revisão). Se o DROP
// não conseguir a trava, o erro sobe, os DELETEs já valeram, e a próxima
// rodada tenta de novo.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	var r MaintenanceReport
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		if r.DeletedRollups, err = q.DeleteOldRollups(ctx, now.AddDate(0, -RollupRetentionMonths, 0)); err != nil {
			return fmt.Errorf("apagar rollups vencidos: %w", err)
		}
		if r.DeletedNotifications, err = q.DeleteOldNotifications(ctx, now.Add(-NotificationsRetention)); err != nil {
			return fmt.Errorf("apagar notificações vencidas: %w", err)
		}
		if r.DeletedAudit, err = q.DeleteOldAudit(ctx, now.AddDate(0, -AuditRetentionMonths, 0)); err != nil {
			return fmt.Errorf("apagar auditoria vencida: %w", err)
		}
		if r.DeletedSessions, err = q.DeleteExpiredSessions(ctx, now.Add(-SessionGrace)); err != nil {
			return fmt.Errorf("apagar sessões vencidas: %w", err)
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	err = InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.SetLocalLockTimeout(ctx, fmt.Sprintf("%dms", dropLockTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("limitar a espera do DROP: %w", err)
		}
		var err error
		if r.DroppedPartitions, err = q.DropChecksPartitionsBefore(ctx, now.Add(-ChecksRetention)); err != nil {
			return fmt.Errorf("apagar partições vencidas: %w", err)
		}
		return nil
	})
	return r, err
}

// RunMaintenance roda Maintain ao subir e depois a cada every, até ctx acabar.
// Substitui o MaintainChecksPartitions do Plano 2, que não tinha teste e dava
// panic com every <= 0. Erro de uma rodada vai ao log; a próxima tenta de novo.
func RunMaintenance(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
	if every <= 0 {
		return ErrMaintenanceInterval
	}
	rodar := func() {
		r, err := Maintain(ctx, pool, clk.Now())
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("manutenção", "erro", err)
			}
			return
		}
		slog.Info("manutenção", "rollups", r.RollupHours, "particoes_apagadas", r.DroppedPartitions,
			"rollups_apagados", r.DeletedRollups, "notificacoes_apagadas", r.DeletedNotifications,
			"auditoria_apagada", r.DeletedAudit, "sessoes_apagadas", r.DeletedSessions)
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
