// Package history é o histórico de incidentes do projeto (GET /incidents e o
// reconhecimento). Mora fora de internal/incident para que o detector
// continue puro, sem banco (spec §3.2).
//
// Cada operação recebe o escopo de projeto e roda sob storage.InProjectTx. A
// permissão é conferida pelo papel lido NA transação (o escopo que
// InProjectTx entrega), nunca pelo que quem chamou trouxe.
package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
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
// (a tela de detalhe do alvo); o de outro projeto dá lista vazia, não erro:
// o filtro não confirma a existência de alvo alheio.
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

// List exige PermRead.
func (s *Service) List(ctx context.Context, scope tenancy.ProjectScope, f Filter) ([]Incident, error) {
	if f.Limit < 1 || f.Limit > MaxPageSize {
		f.Limit = DefaultPageSize
	}
	var out []Incident
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermRead); err != nil {
			return err
		}
		rows, err := q.ListIncidents(ctx, db.ListIncidentsParams{
			ProjectID: atual.ProjectID, Open: f.Open, Before: f.Before, TargetID: f.TargetID, MaxRows: f.Limit,
		})
		if err != nil {
			return fmt.Errorf("listar incidentes: %w", err)
		}
		out = make([]Incident, 0, len(rows))
		for _, r := range rows {
			out = append(out, toIncident(r))
		}
		return nil
	})
	return out, err
}

// Ack reconhece o incidente e exige PermEditMonitors. É idempotente: o
// segundo ack devolve o instante do primeiro. Incidente de outro projeto é
// ErrNotFound (spec §5.5).
func (s *Service) Ack(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) (Incident, error) {
	var out Incident
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermEditMonitors); err != nil {
			return err
		}
		r, err := q.AckIncident(ctx, db.AckIncidentParams{ProjectID: atual.ProjectID, ID: id, Now: s.clk.Now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reconhecer incidente: %w", err)
		}
		out = toIncident(db.ListIncidentsRow(r))
		return nil
	})
	return out, err
}

// toIncident converte uma linha do histórico. O sqlc gera um tipo por
// consulta, e ListIncidents e AckIncident devolvem as mesmas colunas: o Ack
// converte a sua linha para esta, e o compilador recusa a conversão no dia em
// que as colunas divergirem.
func toIncident(r db.ListIncidentsRow) Incident {
	return Incident{
		ID: r.ID, TargetID: r.TargetID, TargetName: r.TargetName, StartedAt: r.StartedAt,
		ResolvedAt: r.ResolvedAt, Cause: r.Cause, AckAt: r.AckAt,
	}
}
