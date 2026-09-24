// Package history é o histórico de incidentes do dono (GET /incidents e o
// reconhecimento). Mora fora de internal/incident para que o detector
// continue puro, sem banco (spec §3.2).
package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var ErrNotFound = errors.New("incidente não encontrado")

type Incident struct {
	ID         uuid.UUID
	TargetID   uuid.UUID
	TargetName string
	StartedAt  time.Time
	ResolvedAt *time.Time // nil enquanto aberto
	Cause      string
	AckAt      *time.Time
}

// Filter: Open nil lista tudo; true só abertos; false só resolvidos. Before
// é o cursor, o último id da página anterior. TargetID restringe a um alvo
// (Plano 5, a tela de detalhe do alvo); o de outro usuário dá lista vazia,
// não erro: o filtro não confirma a existência de alvo alheio.
type Filter struct {
	Open     *bool
	Before   *uuid.UUID
	TargetID *uuid.UUID
	Limit    int32
}

const (
	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100
)

type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, f Filter) ([]Incident, error) {
	if f.Limit < 1 || f.Limit > MaxPageSize {
		f.Limit = DefaultPageSize
	}
	var out []Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListIncidents(ctx, db.ListIncidentsParams{
			UserID: userID, Open: f.Open, Before: f.Before, TargetID: f.TargetID, MaxRows: f.Limit,
		})
		if err != nil {
			return fmt.Errorf("listar incidentes: %w", err)
		}
		out = make([]Incident, 0, len(rows))
		for _, r := range rows {
			out = append(out, Incident{
				ID: r.ID, TargetID: r.TargetID, TargetName: r.TargetName, StartedAt: r.StartedAt,
				ResolvedAt: r.ResolvedAt, Cause: r.Cause, AckAt: r.AckAt,
			})
		}
		return nil
	})
	return out, err
}

// Ack reconhece o incidente. É idempotente: o segundo ack devolve o
// instante do primeiro. Incidente de outro usuário é ErrNotFound (spec §5.5).
func (s *Service) Ack(ctx context.Context, userID, id uuid.UUID) (Incident, error) {
	var out Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		r, err := q.AckIncident(ctx, db.AckIncidentParams{UserID: userID, ID: id, Now: s.clk.Now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reconhecer incidente: %w", err)
		}
		out = Incident{
			ID: r.ID, TargetID: r.TargetID, TargetName: r.TargetName, StartedAt: r.StartedAt,
			ResolvedAt: r.ResolvedAt, Cause: r.Cause, AckAt: r.AckAt,
		}
		return nil
	})
	return out, err
}
