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

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/rollupcut"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
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
		Tags:                in.Tags,
		ContentRule:         in.ContentRule,
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
	modo, valor, ponteiro := colunasDaRegra(f.ContentRule)
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
		err = q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: userID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: in.Enabled, NextRunAt: now, CreatedAt: now, TlsWarn: in.TLSWarn,
			ContentMode: modo, ContentValue: valor, ContentJsonPointer: ponteiro,
		})
		if err != nil {
			return err
		}
		return q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: f.Tags, UserID: userID, TargetID: id})
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
		DegradedThresholdMS: f.DegradedThresholdMS, Enabled: in.Enabled, TLSWarn: in.TLSWarn,
		HeaderNames: headerNames(f.Headers), Tags: f.Tags, ContentRule: f.ContentRule, CreatedAt: now,
	}, nil
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		row, err := q.GetTarget(ctx, db.GetTargetParams{UserID: userID, ID: id})
		if err != nil {
			return naoAchado(err)
		}
		if out, err = s.toTarget(row); err != nil {
			return err
		}
		if out.Tags, err = lerTags(ctx, q, userID, id); err != nil {
			return err
		}
		out.TLS, err = lerValidade(ctx, q, userID, id)
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
		tags, err := tagsDoDono(ctx, q, userID)
		if err != nil {
			return err
		}
		validades, err := validadesDoDono(ctx, q, userID)
		if err != nil {
			return err
		}
		out = make([]Target, 0, len(rows))
		for _, r := range rows {
			t, err := s.toTarget(r)
			if err != nil {
				return err
			}
			t.Tags = tagsDoAlvo(tags, t.ID)
			t.TLS = validades[t.ID]
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
		tags, err := lerTags(ctx, q, userID, row.ID)
		if err != nil {
			return err
		}

		f := fields{
			Name: row.Name, URL: row.URL, Method: row.Method,
			IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
			DegradedThresholdMS: row.DegradedThresholdMS, Headers: headers, Tags: tags,
			ContentRule: RuleFromColumns(row.ContentMode, row.ContentValue, row.ContentJsonPointer),
			urlGravada:  row.URL,
		}
		regraGravada := f.ContentRule
		enabled := row.Enabled
		mudouASonda := p.apply(&f, &enabled)
		tlsWarn := row.TlsWarn
		if p.TLSWarn != nil {
			tlsWarn = *p.TLSWarn
		}
		if verr := validate(&f); verr != nil {
			return verr
		}
		// C4 (Plano 10): outra URL ou outro método é outro serviço, e outra
		// regra é outro critério de saúde. As falhas do anterior não somam com
		// as do novo; o incidente aberto fica e fecha pelos sucessos do novo.
		if f.URL != row.URL || f.Method != row.Method || !f.ContentRule.Equal(regraGravada) {
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
		modo, valor, ponteiro := colunasDaRegra(f.ContentRule)
		err = q.UpdateTarget(ctx, db.UpdateTargetParams{
			UserID: userID, ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
			Enabled: enabled, NextRunAt: proximo, TlsWarn: tlsWarn,
			ContentMode: modo, ContentValue: valor, ContentJsonPointer: ponteiro,
		})
		if err != nil {
			return fmt.Errorf("atualizar alvo: %w", err)
		}
		// Outra origem (esquema, host ou porta) é outro certificado: a leitura
		// anterior sai junto com a troca.
		if mudouDeDestino(row.URL, f.URL) {
			err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: userID, TargetID: row.ID})
			if err != nil {
				return fmt.Errorf("apagar validade do certificado: %w", err)
			}
		}
		validade, err := lerValidade(ctx, q, userID, row.ID)
		if err != nil {
			return err
		}
		if p.SetTags {
			if err := trocarTags(ctx, q, userID, row.ID, f.Tags); err != nil {
				return err
			}
		}
		out = Target{
			ID: row.ID, Name: f.Name, URL: f.URL, Method: f.Method,
			IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
			DegradedThresholdMS: f.DegradedThresholdMS, Enabled: enabled, TLSWarn: tlsWarn, TLS: validade,
			HeaderNames: headerNames(f.Headers), Tags: f.Tags, ContentRule: f.ContentRule,
			CreatedAt: row.CreatedAt,
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
	} else if mudouDeDestino(antes.URL, f.URL) {
		f.Headers = nil
	}
	if p.SetTags {
		f.Tags = p.Tags
	}
	if p.SetContentRule {
		f.ContentRule = p.ContentRule
	}
	if p.Enabled != nil {
		*enabled = *p.Enabled
	}
	return f.URL != antes.URL || f.Method != antes.Method ||
		f.IntervalSec != antes.IntervalSec || f.TimeoutMS != antes.TimeoutMS ||
		f.ExpectedStatus != antes.ExpectedStatus || p.SetHeaders ||
		!f.ContentRule.Equal(antes.ContentRule)
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
// MaxChecksWindow, os checks brutos; acima, a série horária (serieHoraria).
// Busca o alvo antes, e não só a série, porque uma lista vazia não
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
			var err error
			out.Rollups, err = serieHoraria(ctx, q, userID, id, from, to, s.clk.Now())
			return err
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

// serieHoraria lê as horas de [from, to), com from arredondado para baixo até
// a hora cheia, para que a hora que o contém entre. Como o resumo e a página
// pública, junta os rollups até o corte de rollupcut e os checks brutos dele
// em diante, agregados por hora com a fórmula dos rollups: ler só os rollups
// deixava a série cega à queda em curso. O corte é lido antes das duas
// consultas, para nenhuma hora contar duas vezes. Os buckets saem em UTC: são
// horas cheias em UTC por definição, e o pgx devolveria o fuso local do
// processo.
func serieHoraria(ctx context.Context, q *db.Queries, userID, id uuid.UUID, from, to, agora time.Time) ([]Rollup, error) {
	h := agora.UTC().Truncate(time.Hour)
	inicio := from.UTC().Truncate(time.Hour)
	marca, err := q.LatestUserRollupBucket(ctx, db.LatestUserRollupBucketParams{Before: h, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("marca d'água dos rollups: %w", err)
	}
	corte := rollupcut.At(marca, inicio, h)

	rollups, err := q.ListRollups(ctx, db.ListRollupsParams{
		UserID: userID, TargetID: id, FromAt: inicio, ToAt: minTime(corte, to),
	})
	if err != nil {
		return nil, fmt.Errorf("listar rollups: %w", err)
	}
	brutos, err := q.ListRawHours(ctx, db.ListRawHoursParams{UserID: userID, TargetID: &id, FromAt: corte, ToAt: to})
	if err != nil {
		return nil, fmt.Errorf("listar horas brutas: %w", err)
	}
	out := make([]Rollup, 0, len(rollups)+len(brutos))
	for _, r := range rollups {
		out = append(out, Rollup{
			Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok,
			P50: r.P50, P95: r.P95, P99: r.P99, Max: r.Max,
		})
	}
	for _, r := range brutos {
		out = append(out, horaBruta(r))
	}
	return out, nil
}

// horaBruta converte uma hora agregada dos checks brutos. Sem sucesso na hora
// (ok = 0), a consulta devolve as latências como 0; aqui elas viram nil, como
// no rollup. A série do detalhe e o spark do resumo passam por aqui.
func horaBruta(r db.ListRawHoursRow) Rollup {
	hora := Rollup{Bucket: r.Bucket.UTC(), Total: r.Total, OK: r.Ok}
	if r.Ok > 0 {
		hora.P50, hora.P95, hora.P99, hora.Max = &r.P50, &r.P95, &r.P99, &r.Max
	}
	return hora
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Service) toTarget(row db.Target) (Target, error) {
	h, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
	if err != nil {
		return Target{}, err
	}
	return Target{
		ID: row.ID, Name: row.Name, URL: row.URL, Method: row.Method,
		IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
		DegradedThresholdMS: row.DegradedThresholdMS, Enabled: row.Enabled, TLSWarn: row.TlsWarn,
		HeaderNames: headerNames(h), CreatedAt: row.CreatedAt,
		ContentRule: RuleFromColumns(row.ContentMode, row.ContentValue, row.ContentJsonPointer),
	}, nil
}

// lerValidade devolve nil quando o sondador ainda não leu um certificado do
// alvo. Os instantes saem em UTC: o pgx devolveria o fuso local do processo.
func lerValidade(ctx context.Context, q *db.Queries, userID, targetID uuid.UUID) (*CertificateReading, error) {
	r, err := q.GetTargetTLSOfOwner(ctx, db.GetTargetTLSOfOwnerParams{UserID: userID, TargetID: targetID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler validade do certificado: %w", err)
	}
	return &CertificateReading{NotAfter: r.NotAfter.UTC(), ObservedAt: r.ObservedAt.UTC()}, nil
}

// validadesDoDono lê as validades de todos os alvos do dono numa consulta só,
// para a lista não fazer uma por alvo.
func validadesDoDono(ctx context.Context, q *db.Queries, userID uuid.UUID) (map[uuid.UUID]*CertificateReading, error) {
	linhas, err := q.ListTargetTLSOfOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ler validades do dono: %w", err)
	}
	validades := make(map[uuid.UUID]*CertificateReading, len(linhas))
	for _, l := range linhas {
		validades[l.TargetID] = &CertificateReading{NotAfter: l.NotAfter.UTC(), ObservedAt: l.ObservedAt.UTC()}
	}
	return validades, nil
}

// naoAchado traduz a ausência de linha. Sob a RLS, "não existe" e "é de
// outro usuário" são indistinguíveis de propósito: os dois viram 404.
func naoAchado(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("buscar alvo: %w", err)
}
