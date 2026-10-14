package targets

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/rollupcut"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targetstate"
)

// SparkHours é a janela do spark: as 24 horas cheias anteriores à hora
// corrente (contrato dos Planos 5–7).
const SparkHours = 24

// Summary é um alvo como o dashboard o mostra (GET /targets/summary).
type Summary struct {
	ID      uuid.UUID
	Name    string
	Enabled bool
	State   targetstate.State
	// LastCheckedAt e LastLatencyMS são nil sem check nenhum; LastLatencyMS é
	// nil também quando o último check foi down (D16 do Plano 5): a latência
	// de um down é o tempo até o timeout, não a do serviço.
	LastCheckedAt *time.Time
	LastLatencyMS *int32
	// Uptime24h é ok/total sobre as horas do spark MAIS a hora corrente (D16):
	// uma queda em curso aparece nele. nil sem check nenhum na janela.
	Uptime24h      *float64
	OpenIncidentID *uuid.UUID
	Spark          []SparkHour
}

// SparkHour é uma hora cheia (UTC) do spark. Hora sem check não aparece. P50
// é nil numa hora sem sucesso.
type SparkHour struct {
	Bucket    time.Time
	Total, OK int32
	P50       *int32
}

type contagem struct{ total, ok int64 }

// Summary monta o dashboard numa chamada só (D16 do Plano 5), em quatro
// consultas na mesma transação do dono:
//
//  1. os alvos, com o último check e o incidente aberto;
//  2. a marca d'água dos rollups do dono, que dá o corte;
//  3. os rollups horários de [H−24h, corte);
//  4. os checks brutos de [corte, H), agregados por hora com a fórmula dos
//     rollups, e os da hora corrente [H, H+1h), só contados.
//
// Ler só os rollups deixaria a última hora fechada de fora por até 15 min e o
// uptime cego à queda em curso; ler só os brutos custa ~390 ms com 100 alvos
// (medido na spec). O corte é um valor fixo lido antes das consultas 3 e 4:
// uma consolidação que comite entre elas não conta hora nenhuma duas vezes.
// A regra do corte mora em rollupcut, a mesma da página pública.
func (s *Service) Summary(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	agora := s.clk.Now()
	h := agora.UTC().Truncate(time.Hour)
	inicio := h.Add(-SparkHours * time.Hour)

	var out []Summary
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		alvos, err := q.ListTargetSummaries(ctx, userID)
		if err != nil {
			return fmt.Errorf("listar alvos do resumo: %w", err)
		}
		marca, err := q.LatestUserRollupBucket(ctx, db.LatestUserRollupBucketParams{Before: h, UserID: userID})
		if err != nil {
			return fmt.Errorf("marca d'água dos rollups: %w", err)
		}
		corte := rollupcut.At(marca, inicio, h)

		horas := map[uuid.UUID][]SparkHour{}
		rollups, err := q.ListSummaryRollups(ctx, db.ListSummaryRollupsParams{UserID: userID, FromAt: inicio, ToAt: corte})
		if err != nil {
			return fmt.Errorf("rollups do resumo: %w", err)
		}
		for _, r := range rollups {
			horas[r.TargetID] = append(horas[r.TargetID], SparkHour{Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok, P50: r.P50})
		}
		brutos, err := q.ListRawHours(ctx, db.ListRawHoursParams{UserID: userID, FromAt: corte, ToAt: h})
		if err != nil {
			return fmt.Errorf("checks do resumo: %w", err)
		}
		for _, r := range brutos {
			hora := SparkHour{Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok}
			if r.Ok > 0 {
				p50 := r.P50
				hora.P50 = &p50
			}
			horas[r.TargetID] = append(horas[r.TargetID], hora)
		}
		correntes, err := q.ListSummaryCurrentHour(ctx, db.ListSummaryCurrentHourParams{UserID: userID, FromAt: h, ToAt: h.Add(time.Hour)})
		if err != nil {
			return fmt.Errorf("hora corrente do resumo: %w", err)
		}
		corrente := map[uuid.UUID]contagem{}
		for _, r := range correntes {
			corrente[r.TargetID] = contagem{total: int64(r.Total), ok: int64(r.Ok)}
		}

		out = make([]Summary, 0, len(alvos))
		for _, a := range alvos {
			sm := Summary{
				ID: a.ID, Name: a.Name, Enabled: a.Enabled, OpenIncidentID: a.OpenIncidentID,
				State: targetstate.Current(a.Enabled, time.Duration(a.IntervalSec)*time.Second,
					a.LastStatus, a.LastCheckedAt, agora),
				Spark: horas[a.ID],
			}
			if a.LastStatus != "" {
				em := a.LastCheckedAt.UTC()
				sm.LastCheckedAt = &em
				if a.LastStatus != "down" {
					ms := a.LastLatencyMS
					sm.LastLatencyMS = &ms
				}
			}
			slices.SortFunc(sm.Spark, func(x, y SparkHour) int { return x.Bucket.Compare(y.Bucket) })
			c := corrente[a.ID]
			for _, hr := range sm.Spark {
				c.total += int64(hr.Total)
				c.ok += int64(hr.OK)
			}
			if c.total > 0 {
				u := float64(c.ok) / float64(c.total)
				sm.Uptime24h = &u
			}
			out = append(out, sm)
		}
		return nil
	})
	return out, err
}
