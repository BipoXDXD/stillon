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
	// depoisDeContar existe para os testes concorrentes: roda no máximo uma
	// vez por Create ou Update, depois de todas as contagens daquela chamada e
	// antes das decisões de teto.
	depoisDeContar func()
	// tetoPorHost é o máximo de alvos do dono com regra de conteúdo num mesmo
	// host de destino.
	tetoPorHost int
}

// Opcao ajusta o Service na criação.
type Opcao func(*Service)

// ComTetoDeRegrasPorHost troca o teto de alvos com regra de conteúdo por host
// (CONTENT_RULES_PER_HOST); sem ela, vale DefaultContentRulesPerHost.
func ComTetoDeRegrasPorHost(n int) Opcao {
	return func(s *Service) { s.tetoPorHost = n }
}

func NewService(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, opcoes ...Opcao) *Service {
	s := &Service{pool: pool, box: box, clk: clk, tetoPorHost: DefaultContentRulesPerHost}
	for _, o := range opcoes {
		o(s)
	}
	return s
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
		noHost := 0
		if f.ContentRule != nil {
			if noHost, err = contarRegrasNoHost(ctx, q, userID, hostDaRegra(f.URL), uuid.Nil); err != nil {
				return err
			}
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n >= MaxTargetsPerUser {
			return ErrLimitReached
		}
		if f.ContentRule != nil && noHost >= s.tetoPorHost {
			return ErrContentHostLimit
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
	if errors.Is(err, ErrLimitReached) || errors.Is(err, ErrContentHostLimit) {
		return Target{}, err
	}
	if err != nil {
		return Target{}, fmt.Errorf("gravar alvo: %w", err)
	}
	return f.comoTarget(id, in.Enabled, in.TLSWarn, now), nil
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
//
// Ordem: trava do usuário, linha (404), validação (400), teto de regras por
// host (409) e só então as escritas (histerese, alvo, validade do
// certificado). A recusa vem antes de toda escrita para não depender do
// rollback, e depois da linha para o alheio continuar 404.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, p Patch) (Target, error) {
	var out Target
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		out, err = s.editarAlvo(ctx, q, userID, id, p)
		return err
	})
	return out, err
}

// editarAlvo é o corpo transacional do Update, na ordem que o doc dele fixa.
func (s *Service) editarAlvo(ctx context.Context, q *db.Queries, userID, id uuid.UUID, p Patch) (Target, error) {
	row, f, err := s.lerParaEditar(ctx, q, userID, id)
	if err != nil {
		return Target{}, err
	}
	e := novaEdicao(row, f, p)
	if verr := validate(&e.f); verr != nil {
		return Target{}, verr
	}
	if err := s.recusarRegraAcimaDoTeto(ctx, q, userID, e); err != nil {
		return Target{}, err
	}
	if err := zerarHistereseSeMudouOCriterio(ctx, q, userID, e); err != nil {
		return Target{}, err
	}
	if err := s.gravarAlvo(ctx, q, userID, e); err != nil {
		return Target{}, err
	}
	validade, err := acompanharDestinoETags(ctx, q, userID, e, p)
	if err != nil {
		return Target{}, err
	}
	out := e.f.comoTarget(row.ID, e.enabled, e.tlsWarn, row.CreatedAt)
	out.TLS = validade
	return out, nil
}

// lerParaEditar toma a trava do usuário e lê a linha com FOR UPDATE, os
// headers abertos e as tags. A mesma ordem de travas do Create (consultiva,
// depois a linha): dois PATCH simultâneos que poriam regra no mesmo host
// contam em fila. Linha de outro dono é ErrNotFound.
func (s *Service) lerParaEditar(ctx context.Context, q *db.Queries, userID, id uuid.UUID) (db.Target, fields, error) {
	if err := q.LockUserTargets(ctx, userID); err != nil {
		return db.Target{}, fields{}, fmt.Errorf("travar alvos do usuário: %w", err)
	}
	row, err := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: userID, ID: id})
	if err != nil {
		return db.Target{}, fields{}, naoAchado(err)
	}
	headers, err := OpenHeaders(s.box, row.ID, row.HeadersEnc)
	if err != nil {
		return db.Target{}, fields{}, err
	}
	tags, err := lerTags(ctx, q, userID, row.ID)
	if err != nil {
		return db.Target{}, fields{}, err
	}
	return row, fieldsDaLinha(row, headers, tags), nil
}

// edicao é o PATCH já aplicado à linha gravada, ainda sem nenhuma escrita:
// os campos de depois, a regra que valia antes e as três decisões que o PATCH
// toma à parte dos campos (enabled, aviso de certificado e se a sonda mudou).
type edicao struct {
	row          db.Target
	regraGravada *ContentRule
	f            fields
	enabled      bool
	tlsWarn      bool
	mudouASonda  bool
}

func novaEdicao(row db.Target, f fields, p Patch) edicao {
	e := edicao{row: row, regraGravada: f.ContentRule, f: f, enabled: row.Enabled, tlsWarn: p.tlsWarnDepois(row.TlsWarn)}
	e.mudouASonda = p.apply(&e.f, &e.enabled)
	return e
}

// recusarRegraAcimaDoTeto devolve ErrContentHostLimit quando a edição poria
// uma regra num host que já está no teto. Só conta quando a edição aumenta o
// host; a recusa vem antes de toda escrita para não depender do rollback.
func (s *Service) recusarRegraAcimaDoTeto(ctx context.Context, q *db.Queries, userID uuid.UUID, e edicao) error {
	if !aumentaOHost(e.regraGravada, e.row.URL, e.f.ContentRule, e.f.URL) {
		return nil
	}
	outros, err := contarRegrasNoHost(ctx, q, userID, hostDaRegra(e.f.URL), e.row.ID)
	if err != nil {
		return err
	}
	if s.depoisDeContar != nil {
		s.depoisDeContar()
	}
	if outros >= s.tetoPorHost {
		return ErrContentHostLimit
	}
	return nil
}

// zerarHistereseSeMudouOCriterio: outra URL ou outro método é outro serviço,
// e outra regra é outro critério de saúde. As falhas do anterior não somam com
// as do novo; o incidente aberto fica e fecha pelos sucessos do novo.
func zerarHistereseSeMudouOCriterio(ctx context.Context, q *db.Queries, userID uuid.UUID, e edicao) error {
	if !mudouOCriterioDeSaude(e.row.URL, e.row.Method, e.regraGravada, e.f) {
		return nil
	}
	if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{UserID: userID, TargetID: e.row.ID}); err != nil {
		return fmt.Errorf("zerar histerese do alvo: %w", err)
	}
	return nil
}

// gravarAlvo cifra os headers e grava a linha. A próxima sonda sai agora só
// quando mudou o que é sondado; senão o agendamento anterior fica.
func (s *Service) gravarAlvo(ctx context.Context, q *db.Queries, userID uuid.UUID, e edicao) error {
	sealed, err := SealHeaders(s.box, e.row.ID, e.f.Headers)
	if err != nil {
		return err
	}
	proximo := e.row.NextRunAt
	if e.mudouASonda {
		proximo = s.clk.Now()
	}
	err = q.UpdateTarget(ctx, e.f.updateTargetParams(userID, e.row.ID, sealed, e.enabled, proximo, e.tlsWarn))
	if err != nil {
		return fmt.Errorf("atualizar alvo: %w", err)
	}
	return nil
}

// acompanharDestinoETags faz o que vem depois do UPDATE: outra origem
// (esquema, host ou porta) é outro certificado, e a leitura anterior sai junto
// com a troca; devolve a validade que ficou e, se o PATCH mandou tags, troca-as.
func acompanharDestinoETags(ctx context.Context, q *db.Queries, userID uuid.UUID, e edicao, p Patch) (*CertificateReading, error) {
	if mudouDeDestino(e.row.URL, e.f.URL) {
		err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: userID, TargetID: e.row.ID})
		if err != nil {
			return nil, fmt.Errorf("apagar validade do certificado: %w", err)
		}
	}
	validade, err := lerValidade(ctx, q, userID, e.row.ID)
	if err != nil {
		return nil, err
	}
	if p.SetTags {
		if err := trocarTags(ctx, q, userID, e.row.ID, e.f.Tags); err != nil {
			return nil, err
		}
	}
	return validade, nil
}

// fieldsDaLinha monta os campos editáveis do alvo a partir da linha gravada,
// dos headers já abertos e das tags lidas. urlGravada guarda a URL de agora
// para a validação reconhecer o que o PATCH não mexeu.
func fieldsDaLinha(row db.Target, headers map[string]string, tags []string) fields {
	return fields{
		Name: row.Name, URL: row.URL, Method: row.Method,
		IntervalSec: row.IntervalSec, TimeoutMS: row.TimeoutMS, ExpectedStatus: row.ExpectedStatus,
		DegradedThresholdMS: row.DegradedThresholdMS, Headers: headers, Tags: tags,
		ContentRule: RuleFromColumns(row.ContentMode, row.ContentValue, row.ContentJsonPointer),
		urlGravada:  row.URL,
	}
}

// comoTarget é a visão pública dos campos. Só nomes de header saem, nunca os
// valores; a leitura do certificado (TLS) quem chama acrescenta.
func (f fields) comoTarget(id uuid.UUID, enabled, tlsWarn bool, criadoEm time.Time) Target {
	return Target{
		ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
		IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
		DegradedThresholdMS: f.DegradedThresholdMS, Enabled: enabled, TLSWarn: tlsWarn,
		HeaderNames: headerNames(f.Headers), Tags: f.Tags, ContentRule: f.ContentRule, CreatedAt: criadoEm,
	}
}

// updateTargetParams são as colunas do UPDATE do alvo: os campos já
// validados, os headers cifrados e os três valores que o PATCH decide à parte.
func (f fields) updateTargetParams(userID, id uuid.UUID, sealed []byte, enabled bool, proximo time.Time, tlsWarn bool) db.UpdateTargetParams {
	modo, valor, ponteiro := colunasDaRegra(f.ContentRule)
	return db.UpdateTargetParams{
		UserID: userID, ID: id, Name: f.Name, URL: f.URL, Method: f.Method,
		IntervalSec: f.IntervalSec, TimeoutMS: f.TimeoutMS, ExpectedStatus: f.ExpectedStatus,
		HeadersEnc: sealed, DegradedThresholdMS: f.DegradedThresholdMS,
		Enabled: enabled, NextRunAt: proximo, TlsWarn: tlsWarn,
		ContentMode: modo, ContentValue: valor, ContentJsonPointer: ponteiro,
	}
}

// tlsWarnDepois é o aviso de certificado que vale depois do PATCH: o enviado,
// ou o atual quando o PATCH não falou dele.
func (p Patch) tlsWarnDepois(atual bool) bool {
	if p.TLSWarn != nil {
		return *p.TLSWarn
	}
	return atual
}

// mudouOCriterioDeSaude diz se o PATCH trocou o que define "saudável": outra
// URL ou outro método é outro serviço, e outra regra é outro critério. Quem
// decide isso zera a histerese, porque as falhas do anterior não somam com as
// do novo.
func mudouOCriterioDeSaude(urlAntes, metodoAntes string, regraAntes *ContentRule, depois fields) bool {
	return depois.URL != urlAntes || depois.Method != metodoAntes || !depois.ContentRule.Equal(regraAntes)
}

// contarRegrasNoHost conta os alvos do dono com regra cujo hostDaRegra é
// host, sem contar exceto (uuid.Nil no Create). Só conta: quem chama compara
// com o teto. Pede a trava LockUserTargets já tomada.
func contarRegrasNoHost(ctx context.Context, q *db.Queries, userID uuid.UUID, host string, exceto uuid.UUID) (int, error) {
	linhas, err := q.ListContentRuleTargetURLs(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("contar regras no host: %w", err)
	}
	n := 0
	for _, l := range linhas {
		if l.ID != exceto && hostDaRegra(l.URL) == host {
			n++
		}
	}
	return n, nil
}

// aumentaOHost diz se o PATCH põe uma regra num host onde o alvo não contava:
// o teto barra o aumento, não o estado. Trocar valor, modo, caminho,
// porta ou esquema no mesmo host, pausar ou tirar a regra nunca aumenta. É de
// propósito outra pergunta que mudouDeDestino, que olha a origem inteira.
func aumentaOHost(regraAntes *ContentRule, urlAntes string, regraDepois *ContentRule, urlDepois string) bool {
	if regraDepois == nil {
		return false
	}
	return regraAntes == nil || hostDaRegra(urlAntes) != hostDaRegra(urlDepois)
}

// apply devolve true quando a mudança altera O QUE é sondado.
//
// Uma URL de outra origem sem headers no mesmo PATCH apaga os headers: eles são
// credenciais daquele destino, e levá-los a outro host é o mesmo vazamento que o
// sondador já evita num redirecionamento.
func (p Patch) apply(f *fields, enabled *bool) bool {
	antes := *f
	p.aplicarCampos(f)
	p.aplicarHeaders(f, antes.URL)
	sobrescrever(enabled, p.Enabled)
	return p.mudouASonda(antes, *f)
}

// aplicarCampos grava em f os campos que o PATCH mandou: ponteiro nil é
// ausente e não altera; nos campos que aceitam limpar, a flag Set* manda e o
// valor nil limpa.
func (p Patch) aplicarCampos(f *fields) {
	sobrescrever(&f.Name, p.Name)
	sobrescrever(&f.URL, p.URL)
	sobrescrever(&f.Method, p.Method)
	sobrescrever(&f.IntervalSec, p.IntervalSec)
	sobrescrever(&f.TimeoutMS, p.TimeoutMS)
	sobrescrever(&f.ExpectedStatus, p.ExpectedStatus)
	if p.SetDegradedThreshold {
		f.DegradedThresholdMS = p.DegradedThresholdMS
	}
	if p.SetTags {
		f.Tags = p.Tags
	}
	if p.SetContentRule {
		f.ContentRule = p.ContentRule
	}
}

// aplicarHeaders troca os headers quando o PATCH os mandou; sem eles, uma URL
// de outra origem (urlAntes é a de antes do PATCH) apaga os que existiam.
func (p Patch) aplicarHeaders(f *fields, urlAntes string) {
	if p.SetHeaders {
		f.Headers = p.Headers
	} else if mudouDeDestino(urlAntes, f.URL) {
		f.Headers = nil
	}
}

// mudouASonda diz se o PATCH alterou o que a sonda faz: destino, método,
// cadência, prazo, status esperado, headers ou regra. Nome, tags, limiar,
// enabled e aviso de certificado não contam.
func (p Patch) mudouASonda(antes, depois fields) bool {
	return depois.URL != antes.URL || depois.Method != antes.Method ||
		depois.IntervalSec != antes.IntervalSec || depois.TimeoutMS != antes.TimeoutMS ||
		depois.ExpectedStatus != antes.ExpectedStatus || p.SetHeaders ||
		!depois.ContentRule.Equal(antes.ContentRule)
}

// sobrescrever copia *novo para *destino quando o PATCH mandou o campo.
func sobrescrever[T any](destino, novo *T) {
	if novo != nil {
		*destino = *novo
	}
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
			janela, err := rollupcut.NovaJanela(from, to, s.clk.Now())
			if err != nil {
				return fmt.Errorf("janela da série: %w", err)
			}
			out.Rollups, err = serieHoraria(ctx, q, userID, id, janela)
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

// serieHoraria lê as horas da janela [Desde, Ate), com o início já arredondado
// para baixo até a hora cheia, para que a hora que o contém entre. Como o
// resumo e a página pública, junta os rollups até o corte de rollupcut e os
// checks brutos dele em diante, agregados por hora com a fórmula dos rollups:
// ler só os rollups deixava a série cega à queda em curso. O corte é lido antes das duas
// consultas, para nenhuma hora contar duas vezes. Os buckets saem em UTC: são
// horas cheias em UTC por definição, e o pgx devolveria o fuso local do
// processo.
func serieHoraria(ctx context.Context, q *db.Queries, userID, id uuid.UUID, j rollupcut.Janela) ([]Rollup, error) {
	marca, err := q.LatestUserRollupBucket(ctx, db.LatestUserRollupBucketParams{Before: j.Hora(), UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("marca d'água dos rollups: %w", err)
	}
	corte := j.Corte(marca)

	rollups, err := q.ListRollups(ctx, db.ListRollupsParams{
		UserID: userID, TargetID: id, FromAt: j.Desde(), ToAt: minTime(corte, j.Ate()),
	})
	if err != nil {
		return nil, fmt.Errorf("listar rollups: %w", err)
	}
	brutos, err := q.ListRawHours(ctx, db.ListRawHoursParams{UserID: userID, TargetID: &id, FromAt: corte, ToAt: j.Ate()})
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
	return fieldsDaLinha(row, h, nil).comoTarget(row.ID, row.Enabled, row.TlsWarn, row.CreatedAt), nil
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
