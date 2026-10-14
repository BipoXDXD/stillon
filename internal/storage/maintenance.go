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
	// maintenanceRoundTimeout é o prazo de uma rodada inteira de RunMaintenance,
	// abaixo do intervalo de produção (15 min): presa, a rodada desiste, vai
	// ao log, e a seguinte tenta de novo (C2 da revisão).
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
	// Com o DROP desistindo, os DELETEs já valeram: o relatório parcial
	// sobe junto com o erro, para o log dizer o que foi feito.
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
// A marca d'água é o último rollup ANTES de ate. Um rollup no futuro (o
// relógio esteve adiantado quando foi gravado) não conta: com a marca no
// futuro, de ficava depois de ate e nenhuma hora era consolidada (D4 do Plano
// 10); limitada a ate, ela escondia a última marca real, e uma parada de mais
// de 2 h perdia para sempre as horas antes dela (M1 da revisão). O rollup do
// futuro fica; a retenção o apaga com a idade.
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
// São duas transações. Os DELETEs vêm primeiro; o DROP das partições vem
// sozinho, com lock_timeout, porque pega ACCESS EXCLUSIVE em checks e não
// pode segurá-la durante o resto da retenção (achado da revisão). Se o DROP
// não conseguir a trava, o erro sobe, os DELETEs já valeram, e a próxima
// rodada tenta de novo.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (MaintenanceReport, error) {
	var r MaintenanceReport
	err := InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.SetLocalStatementTimeout(ctx, milissegundos(maintenanceStatementTimeout)); err != nil {
			return fmt.Errorf("prazo da retenção: %w", err)
		}
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
		if err := q.SetLocalLockTimeout(ctx, milissegundos(dropLockTimeout)); err != nil {
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
			"auditoria_apagada", r.DeletedAudit, "sessoes_apagadas", r.DeletedSessions}
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
