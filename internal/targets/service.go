package targets

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Service é o CRUD de alvos. Todo método recebe userID logo depois de ctx
// (spec §5.5) e roda sob storage.InUserTx, que liga a RLS para esse
// usuário: as queries filtram por user_id E a política filtra de novo.
type Service struct {
	pool *pgxpool.Pool
	box  *crypto.Box
	clk  clock.Clock
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem de alvos e a inserção.
	depoisDeContar func()
}

func NewService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock) *Service {
	return &Service{pool: pool, box: box, clk: clk}
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Target, error) {
	f := fields{
		Name:                in.Name,
		URL:                 in.URL,
		Method:              cmp.Or(in.Method, DefaultMethod),
		IntervalSec:         cmp.Or(in.IntervalSec, DefaultIntervalSec),
		TimeoutMS:           cmp.Or(in.TimeoutMS, DefaultTimeoutMS),
		ExpectedStatus:      cmp.Or(in.ExpectedStatus, DefaultExpectedStatus),
		DegradedThresholdMS: in.DegradedThresholdMS,
		Headers:             in.Headers,
	}
	if verr := validate(&f); verr != nil {
		return Target{}, verr
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Target{}, fmt.Errorf("gerar id do alvo: %w", err)
	}
	sealed, err := SealHeaders(s.box, id, f.Headers)
	if err != nil {
		return Target{}, err
	}

	// O alvo nasce vencido: a primeira sonda sai no próximo tick, e quem
	// cadastrou vê o estado em segundos, não depois de um intervalo inteiro.
	now := s.clk.Now()
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		// O teto conta e insere sob a trava do usuário: sem ela, dois POST
		// simultâneos passariam os dois pela contagem.
		if err := q.LockUserTargets(ctx, userID); err != nil {
			return fmt.Errorf("travar alvos do usuário: %w", err)
		}
		n, err := q.CountTargets(ctx, userID)
		if err != nil {
			return fmt.Errorf("contar alvos: %w", err)
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n >= MaxTargetsPerUser {
			return ErrLimitReached
		}
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: userID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: in.Enabled, NextRunAt: now, CreatedAt: now,
		})
	})
	if errors.Is(err, ErrLimitReached) {
		return Target{}, err
	}
	if err != nil {
		return Target{}, fmt.Errorf("gravar alvo: %w", err)
	}
	return Target{
		ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
		IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
		DegradedThresholdMS: f.DegradedThresholdMS, Enabled: in.Enabled,
		HeaderNames: headerNames(f.Headers), CreatedAt: now,
	}, nil
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		row, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id})
		if err != nil {
			return naoAchado(err)
		}
		out, err = s.toTarget(row)
		return err
	})
	return out, err
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, before *uuid.UUID, limit int32) ([]Target, error) {
	if limit < 1 || limit > MaxPageSize {
		limit = DefaultPageSize
	}
	var out []Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: userID, Before: before, MaxRows: limit})
		if err != nil {
			return fmt.Errorf("listar alvos: %w", err)
		}
		out = make([]Target, 0, len(rows))
		for _, r := range rows {
			t, err := s.toTarget(r)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return nil
	})
	return out, err
}

// Update lê com FOR UPDATE, aplica o patch, valida o alvo RESULTANTE e grava,
// tudo na mesma transação: dois PATCH simultâneos não se atropelam, e um
// timeout que cabia no intervalo antigo não passa despercebido no novo.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, p Patch) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		row, err := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: userID, ID: id})
		if err != nil {
			return naoAchado(err)
		}
		headers, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
		if err != nil {
			return err
		}

		f := fields{
			Name: row.Name, URL: row.URL, Method: row.Method,
			IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
			DegradedThresholdMS: row.DegradedThresholdMS, Headers: headers,
		}
		enabled := row.Enabled
		mudouASonda := p.apply(&f, &enabled)
		if verr := validate(&f); verr != nil {
			return verr
		}
		// C4 (Plano 10): outra URL ou outro método é outro serviço. As falhas
		// do anterior não somam com as do novo; o incidente aberto fica e
		// fecha pelos sucessos do novo.
		if f.URL != row.URL || f.Method != row.Method {
			if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{UserID: userID, TargetID: row.ID}); err != nil {
				return fmt.Errorf("zerar histerese do alvo: %w", err)
			}
		}

		sealed, err := SealHeaders(s.box, row.ID, f.Headers)
		if err != nil {
			return err
		}
		proximo := row.NextRunAt
		if mudouASonda {
			proximo = s.clk.Now() // D13
		}
		err = q.UpdateTarget(ctx, db.UpdateTargetParams{
			UserID: userID, ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: enabled, NextRunAt: proximo,
		})
		if err != nil {
			return fmt.Errorf("atualizar alvo: %w", err)
		}
		out = Target{
			ID: row.ID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			DegradedThresholdMS: f.DegradedThresholdMS, Enabled: enabled,
			HeaderNames: headerNames(f.Headers), CreatedAt: row.CreatedAt,
		}
		return nil
	})
	return out, err
}

// apply devolve true quando a mudança altera O QUE é sondado (D13).
//
// Uma URL de outra origem sem headers no mesmo PATCH apaga os headers (B1
// do Plano 10): eles são credenciais daquele destino, e levá-los a outro
// host é o mesmo vazamento que o sondador já evita num redirecionamento.
func (p Patch) apply(f *fields, enabled *bool) bool {
	antes := *f
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.URL != nil {
		f.URL = *p.URL
	}
	if p.Method != nil {
		f.Method = *p.Method
	}
	if p.IntervalSec != nil {
		f.IntervalSec = *p.IntervalSec
	}
	if p.TimeoutMS != nil {
		f.TimeoutMS = *p.TimeoutMS
	}
	if p.ExpectedStatus != nil {
		f.ExpectedStatus = *p.ExpectedStatus
	}
	if p.SetDegradedThreshold {
		f.DegradedThresholdMS = p.DegradedThresholdMS
	}
	if p.SetHeaders {
		f.Headers = p.Headers
	} else if !SameOrigin(antes.URL, f.URL) {
		f.Headers = nil
	}
	if p.Enabled != nil {
		*enabled = *p.Enabled
	}
	return f.URL != antes.URL || f.Method != antes.Method ||
		f.IntervalSec != antes.IntervalSec || f.TimeoutMS != antes.TimeoutMS ||
		f.ExpectedStatus != antes.ExpectedStatus || p.SetHeaders
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: userID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar alvo: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Checks devolve a série de um alvo no intervalo [from, to). Até
// MaxChecksWindow, os checks brutos; acima, as horas de check_rollup_1h, com
// from arredondado para baixo até a hora cheia, para que a hora que o contém
// entre. Os buckets saem em UTC: são horas cheias em UTC por definição, e o
// pgx devolveria o fuso local do processo. Busca o alvo antes, e não só a série, porque uma lista vazia não
// distinguiria "alvo de outro usuário" de "alvo sem sondas": o primeiro
// precisa ser 404.
func (s *Service) Checks(ctx context.Context, userID, id uuid.UUID, from, to time.Time) (Series, error) {
	if !from.Before(to) {
		return Series{}, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonInvalid}}}
	}
	if to.Sub(from) > MaxRollupWindow {
		return Series{}, &ValidationError{Fields: []FieldError{{Field: "from", Reason: ReasonOutOfRange}}}
	}
	out := Series{Resolution: ResolutionRaw}
	if to.Sub(from) > MaxChecksWindow {
		out.Resolution = Resolution1h
	}
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if _, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id}); err != nil {
			return naoAchado(err)
		}
		if out.Resolution == Resolution1h {
			rows, err := q.ListRollups(ctx, db.ListRollupsParams{
				UserID: userID, TargetID: id, FromAt: from.Truncate(time.Hour), ToAt: to,
			})
			if err != nil {
				return fmt.Errorf("listar rollups: %w", err)
			}
			out.Rollups = make([]Rollup, 0, len(rows))
			for _, r := range rows {
				out.Rollups = append(out.Rollups, Rollup{
					Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok,
					P50: r.P50, P95: r.P95, P99: r.P99, Max: r.Max,
				})
			}
			return nil
		}
		rows, err := q.ListChecks(ctx, db.ListChecksParams{UserID: userID, TargetID: id, FromAt: from, ToAt: to})
		if err != nil {
			return fmt.Errorf("listar checks: %w", err)
		}
		out.Checks = make([]Check, 0, len(rows))
		for _, r := range rows {
			out.Checks = append(out.Checks, Check{
				CheckedAt: r.CheckedAt, Status: r.Status, LatencyMS: r.LatencyMS,
				StatusCode: r.StatusCode, ErrorKind: r.ErrorKind,
			})
		}
		return nil
	})
	return out, err
}

func (s *Service) toTarget(row db.Target) (Target, error) {
	h, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
	if err != nil {
		return Target{}, err
	}
	return Target{
		ID: row.ID, Name: row.Name, URL: row.URL, Method: row.Method,
		IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
		DegradedThresholdMS: row.DegradedThresholdMS, Enabled: row.Enabled,
		HeaderNames: headerNames(h), CreatedAt: row.CreatedAt,
	}, nil
}

// naoAchado traduz a ausência de linha. Sob a RLS, "não existe" e "é de
// outro usuário" são indistinguíveis de propósito: os dois viram 404.
func naoAchado(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("buscar alvo: %w", err)
}
