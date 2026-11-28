// Package exportacao escreve os dados da conta em JSON versionado
// ("stillon-export", versão 1): a conta, as sessões vivas, a auditoria, os
// vínculos com projetos e, dos projetos em que a pessoa é owner, os recursos.
//
// Nunca entram: hash de senha, segredo TOTP, códigos de recuperação, hashes
// de token, ip_hash, a URL do webhook (vai só o hint), os valores dos headers
// (vão só os nomes, com o valor "[omitido]"), o token de ping e os checks
// brutos. Os DTOs são deste pacote: um campo novo numa query não entra na
// exportação sem passar por aqui.
package exportacao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

const (
	// Formato e Versao identificam o arquivo. Mudar o formato de um campo ou
	// tirar uma chave pede versão nova.
	Formato = "stillon-export"
	Versao  = 1

	omitido = "[omitido]"
	// pagina é o tamanho de cada leitura paginada (alvos, heartbeats,
	// incidentes).
	pagina int32 = 100
)

// ErrUsuarioInexistente: o usuário pedido não existe mais.
var ErrUsuarioInexistente = errors.New("usuário inexistente")

// Servico lê e escreve a exportação. box abre os headers dos alvos (só os
// nomes saem) e o destino dos canais (sai o hint e, do e-mail, o endereço).
type Servico struct {
	pool *pgxpool.Pool
	box  *crypto.Box
	clk  clock.Clock
}

func Novo(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock) *Servico {
	return &Servico{pool: pool, box: box, clk: clk}
}

type conta struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	EmailVerified   bool       `json:"email_verified"`
	TOTPEnabled     bool       `json:"totp_enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	TermsVersion    *string    `json:"terms_version"`
	TermsAcceptedAt *time.Time `json:"terms_accepted_at"`
}

type sessao struct {
	ID         uuid.UUID `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type auditoria struct {
	Action   string     `json:"action"`
	Entity   *string    `json:"entity"`
	EntityID *uuid.UUID `json:"entity_id"`
	At       time.Time  `json:"at"`
}

type pertenca struct {
	ProjectID   uuid.UUID `json:"project_id"`
	ProjectName string    `json:"project_name"`
	Role        string    `json:"role"`
	IsTitular   bool      `json:"is_titular"`
}

type projeto struct {
	ID          uuid.UUID        `json:"id"`
	Name        string           `json:"name"`
	IsDefault   bool             `json:"is_default"`
	CreatedAt   time.Time        `json:"created_at"`
	Members     []membro         `json:"members"`
	Invitations []convite        `json:"invitations"`
	Targets     []alvo           `json:"targets"`
	Heartbeats  []heartbeat      `json:"heartbeats"`
	Channels    []canal          `json:"channels"`
	StatusPages []paginaDeStatus `json:"status_pages"`
	Incidents   []incidente      `json:"incidents"`
	Rollups     []rollup         `json:"rollups"`
}

type membro struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type convite struct {
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type alvo struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	URL                 string            `json:"url"`
	Method              string            `json:"method"`
	IntervalSec         int32             `json:"interval_sec"`
	TimeoutMS           int32             `json:"timeout_ms"`
	ExpectedStatus      int32             `json:"expected_status"`
	Headers             map[string]string `json:"headers"`
	DegradedThresholdMS *int32            `json:"degraded_threshold_ms"`
	Enabled             bool              `json:"enabled"`
	CreatedAt           time.Time         `json:"created_at"`
	TLSWarn             bool              `json:"tls_warn"`
	ContentMode         *string           `json:"content_mode"`
	ContentValue        *string           `json:"content_value"`
	ContentJSONPointer  *string           `json:"content_json_pointer"`
	Tags                []string          `json:"tags"`
}

type heartbeat struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	PeriodSec   *int32     `json:"period_sec"`
	CronExpr    *string    `json:"cron_expr"`
	Timezone    *string    `json:"timezone"`
	GraceSec    int32      `json:"grace_sec"`
	Enabled     bool       `json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
	LastPingAt  *time.Time `json:"last_ping_at"`
	Tags        []string   `json:"tags"`
}

type canal struct {
	ID      uuid.UUID `json:"id"`
	Kind    string    `json:"kind"`
	Hint    string    `json:"hint"`
	Email   *string   `json:"email"`
	Enabled bool      `json:"enabled"`
}

type paginaDeStatus struct {
	ID        uuid.UUID      `json:"id"`
	Slug      string         `json:"slug"`
	Title     string         `json:"title"`
	Published bool           `json:"published"`
	CreatedAt time.Time      `json:"created_at"`
	Items     []itemDaPagina `json:"items"`
}

type itemDaPagina struct {
	TargetID    uuid.UUID `json:"target_id"`
	DisplayName string    `json:"display_name"`
	Position    int32     `json:"position"`
}

type incidente struct {
	ID          uuid.UUID  `json:"id"`
	TargetID    *uuid.UUID `json:"target_id"`
	HeartbeatID *uuid.UUID `json:"heartbeat_id"`
	StartedAt   time.Time  `json:"started_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	Cause       string     `json:"cause"`
	AckAt       *time.Time `json:"ack_at"`
}

type rollup struct {
	TargetID uuid.UUID `json:"target_id"`
	Bucket   time.Time `json:"bucket"`
	Total    int32     `json:"total"`
	OK       int32     `json:"ok"`
	P50      *int32    `json:"p50"`
	P95      *int32    `json:"p95"`
	P99      *int32    `json:"p99"`
	Max      *int32    `json:"max"`
}

// pessoal é o que vem do escopo do usuário, lido antes do primeiro byte.
type pessoal struct {
	conta     conta
	sessoes   []sessao
	auditoria []auditoria
	pertencas []pertenca
	donoDe    []uuid.UUID
	geradoEm  time.Time
}

// Escrever lê e escreve a exportação de userID em w.
//
// Os dados da pessoa são lidos numa transação de usuário antes do primeiro
// byte; cada projeto em que ela é owner é lido depois, no seu próprio
// storage.InProjectTx, e escrito só quando a leitura dele termina. Não há
// snapshot único: um projeto pode refletir um instante um pouco posterior ao
// da conta. O papel vale pelo que a transação do projeto lê: quem deixou de
// ser owner no meio não leva o projeto.
//
// Um erro depois do primeiro byte volta para quem chamou com o JSON
// incompleto em w: o handler aborta a resposta (panic(http.ErrAbortHandler))
// para o download falhar em vez de chegar truncado.
func (s *Servico) Escrever(ctx context.Context, userID uuid.UUID, w io.Writer) error {
	p, err := s.lerPessoal(ctx, userID)
	if err != nil {
		return err
	}
	e := &escritor{enc: json.NewEncoder(w), w: w}
	e.bruto(`{"format":`)
	e.valor(Formato)
	e.bruto(`,"version":`)
	e.valor(Versao)
	e.bruto(`,"generated_at":`)
	e.valor(p.geradoEm)
	e.bruto(`,"account":`)
	e.valor(p.conta)
	e.bruto(`,"sessions":`)
	e.valor(p.sessoes)
	e.bruto(`,"audit":`)
	e.valor(p.auditoria)
	e.bruto(`,"memberships":`)
	e.valor(p.pertencas)
	e.bruto(`,"projects":[`)
	primeiro := true
	for _, id := range p.donoDe {
		if e.err != nil {
			return e.err
		}
		proj, ok, err := s.lerProjeto(ctx, userID, id, p.geradoEm)
		if err != nil {
			return fmt.Errorf("exportar o projeto %s: %w", id, err)
		}
		if !ok {
			continue
		}
		if !primeiro {
			e.bruto(",")
		}
		primeiro = false
		e.valor(proj)
	}
	e.bruto("]}\n")
	return e.err
}

func (s *Servico) lerPessoal(ctx context.Context, userID uuid.UUID) (pessoal, error) {
	p := pessoal{geradoEm: s.clk.Now().UTC()}
	// users e audit_log não têm RLS (o filtro por id é a barreira); sessions
	// (00029) e project_members abrem pelo app.user_id do escopo do usuário.
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		r, err := q.GetAccount(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUsuarioInexistente
		}
		if err != nil {
			return fmt.Errorf("ler conta: %w", err)
		}
		p.conta = conta{
			ID: r.ID, Email: r.Email, EmailVerified: r.EmailVerifiedAt != nil, TOTPEnabled: r.TotpEnabled,
			CreatedAt: r.CreatedAt, TermsVersion: r.TermsVersion, TermsAcceptedAt: r.TermsAcceptedAt,
		}

		familias, err := q.ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: userID, Now: p.geradoEm})
		if err != nil {
			return fmt.Errorf("listar sessões: %w", err)
		}
		p.sessoes = make([]sessao, 0, len(familias))
		for _, f := range familias {
			// A ordem (família, linha mais nova) deixa a linha corrente de
			// cada família primeiro: as outras ficam de fora.
			if n := len(p.sessoes); n > 0 && p.sessoes[n-1].ID == f.FamilyID {
				continue
			}
			seg, nseg := f.FamilyID.Time().UnixTime()
			p.sessoes = append(p.sessoes, sessao{
				ID: f.FamilyID, CreatedAt: time.Unix(seg, nseg).UTC(), LastUsedAt: f.CreatedAt, ExpiresAt: f.ExpiresAt,
			})
		}

		registros, err := q.ListAuditOfUser(ctx, &userID)
		if err != nil {
			return fmt.Errorf("listar auditoria: %w", err)
		}
		p.auditoria = make([]auditoria, 0, len(registros))
		for _, a := range registros {
			p.auditoria = append(p.auditoria, auditoria{Action: a.Action, Entity: a.Entity, EntityID: a.EntityID, At: a.At})
		}

		projetos, err := q.ListMyProjects(ctx, userID)
		if err != nil {
			return fmt.Errorf("listar projetos: %w", err)
		}
		p.pertencas = make([]pertenca, 0, len(projetos))
		for _, m := range projetos {
			p.pertencas = append(p.pertencas, pertenca{
				ProjectID: m.ID, ProjectName: m.Name, Role: m.Role, IsTitular: m.IsTitular,
			})
			if m.Role == string(tenancy.RoleOwner) {
				p.donoDe = append(p.donoDe, m.ID)
			}
		}
		return nil
	})
	return p, err
}

// lerProjeto lê o projeto inteiro numa transação dele. ok é falso quando a
// pessoa já não é owner (ou já não é membro) quando a transação abre.
func (s *Servico) lerProjeto(ctx context.Context, userID, projectID uuid.UUID, agora time.Time) (projeto, bool, error) {
	var out projeto
	dono := false
	err := storage.InProjectTx(ctx, s.pool, userID, projectID, func(q *db.Queries, esc tenancy.ProjectScope) error {
		if esc.Role != tenancy.RoleOwner {
			return nil
		}
		dono = true
		var err error
		out, err = s.lerRecursos(ctx, q, esc, agora)
		return err
	})
	if errors.Is(err, tenancy.ErrProjectNotFound) {
		return projeto{}, false, nil
	}
	if err != nil {
		return projeto{}, false, err
	}
	return out, dono, nil
}

func (s *Servico) lerRecursos(ctx context.Context, q *db.Queries, esc tenancy.ProjectScope, agora time.Time) (projeto, error) {
	id := esc.ProjectID
	r, err := q.GetMyProject(ctx, db.GetMyProjectParams{UserID: esc.UserID, ProjectID: id})
	if err != nil {
		return projeto{}, fmt.Errorf("ler projeto: %w", err)
	}
	p := projeto{ID: r.ID, Name: r.Name, IsDefault: r.IsDefault, CreatedAt: r.CreatedAt}

	membros, err := q.ListProjectMembers(ctx, id)
	if err != nil {
		return p, fmt.Errorf("listar membros: %w", err)
	}
	p.Members = make([]membro, 0, len(membros))
	for _, m := range membros {
		p.Members = append(p.Members, membro{Email: m.Email, Role: m.Role})
	}

	convites, err := q.ListPendingInvitations(ctx, db.ListPendingInvitationsParams{ProjectID: id, Now: agora})
	if err != nil {
		return p, fmt.Errorf("listar convites: %w", err)
	}
	p.Invitations = make([]convite, 0, len(convites))
	for _, c := range convites {
		p.Invitations = append(p.Invitations, convite{Email: c.Email, Role: c.Role, ExpiresAt: c.ExpiresAt})
	}

	if p.Targets, p.Rollups, err = s.lerAlvos(ctx, q, id, agora); err != nil {
		return p, err
	}
	if p.Heartbeats, err = lerHeartbeats(ctx, q, id); err != nil {
		return p, err
	}
	if p.Channels, err = s.lerCanais(ctx, q, id); err != nil {
		return p, err
	}
	if p.StatusPages, err = lerPaginas(ctx, q, id); err != nil {
		return p, err
	}
	if p.Incidents, err = lerIncidentes(ctx, q, id, p.Heartbeats); err != nil {
		return p, err
	}
	return p, nil
}

// lerAlvos lê os alvos e os rollups horários deles. Os checks brutos não
// entram (D5-A): o rollup é o histórico que a exportação leva.
func (s *Servico) lerAlvos(ctx context.Context, q *db.Queries, projectID uuid.UUID, agora time.Time) ([]alvo, []rollup, error) {
	tags := map[uuid.UUID][]string{}
	linhasDeTag, err := q.ListProjectTargetTags(ctx, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("listar tags dos alvos: %w", err)
	}
	for _, t := range linhasDeTag {
		tags[t.TargetID] = append(tags[t.TargetID], t.Tag)
	}

	alvos := []alvo{}
	rollups := []rollup{}
	var antes *uuid.UUID
	for {
		linhas, err := q.ListTargets(ctx, db.ListTargetsParams{ProjectID: projectID, Before: antes, MaxRows: pagina})
		if err != nil {
			return nil, nil, fmt.Errorf("listar alvos: %w", err)
		}
		for _, t := range linhas {
			headers, err := targets.OpenHeaders(s.box, t.ID, t.HeadersEnc)
			if err != nil {
				return nil, nil, err
			}
			nomes := make(map[string]string, len(headers))
			for nome := range headers {
				nomes[nome] = omitido
			}
			a := alvo{
				ID: t.ID, Name: t.Name, URL: t.URL, Method: t.Method, IntervalSec: t.IntervalSec,
				TimeoutMS: t.TimeoutMS, ExpectedStatus: t.ExpectedStatus, Headers: nomes,
				DegradedThresholdMS: t.DegradedThresholdMS, Enabled: t.Enabled, CreatedAt: t.CreatedAt,
				TLSWarn: t.TlsWarn, ContentMode: t.ContentMode, ContentValue: t.ContentValue,
				ContentJSONPointer: t.ContentJsonPointer, Tags: tags[t.ID],
			}
			if a.Tags == nil {
				a.Tags = []string{}
			}
			alvos = append(alvos, a)

			hs, err := q.ListRollups(ctx, db.ListRollupsParams{
				ProjectID: projectID, TargetID: t.ID, FromAt: time.Unix(0, 0).UTC(), ToAt: agora.Add(time.Hour),
			})
			if err != nil {
				return nil, nil, fmt.Errorf("listar rollups do alvo %s: %w", t.ID, err)
			}
			for _, h := range hs {
				rollups = append(rollups, rollup{
					TargetID: t.ID, Bucket: h.Bucket, Total: h.Total, OK: h.Ok, P50: h.P50, P95: h.P95, P99: h.P99, Max: h.Max,
				})
			}
		}
		if len(linhas) < int(pagina) {
			return alvos, rollups, nil
		}
		antes = &linhas[len(linhas)-1].ID
	}
}

// lerHeartbeats leva a configuração, nunca o hash do token de ping.
func lerHeartbeats(ctx context.Context, q *db.Queries, projectID uuid.UUID) ([]heartbeat, error) {
	tags := map[uuid.UUID][]string{}
	linhasDeTag, err := q.ListProjectHeartbeatTags(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("listar tags dos heartbeats: %w", err)
	}
	for _, t := range linhasDeTag {
		tags[t.HeartbeatID] = append(tags[t.HeartbeatID], t.Tag)
	}
	out := []heartbeat{}
	var antes *uuid.UUID
	for {
		linhas, err := q.ListHeartbeats(ctx, db.ListHeartbeatsParams{ProjectID: projectID, Before: antes, MaxRows: pagina})
		if err != nil {
			return nil, fmt.Errorf("listar heartbeats: %w", err)
		}
		for _, l := range linhas {
			h := l.Heartbeat
			hb := heartbeat{
				ID: h.ID, Name: h.Name, Description: h.Description, PeriodSec: h.PeriodSec, CronExpr: h.CronExpr,
				Timezone: h.Timezone, GraceSec: h.GraceSec, Enabled: h.Enabled, CreatedAt: h.CreatedAt,
				LastPingAt: h.LastPingAt, Tags: tags[h.ID],
			}
			if hb.Tags == nil {
				hb.Tags = []string{}
			}
			out = append(out, hb)
		}
		if len(linhas) < int(pagina) {
			return out, nil
		}
		antes = &linhas[len(linhas)-1].Heartbeat.ID
	}
}

// lerCanais leva o hint do destino e, do e-mail, o endereço; do webhook,
// nunca a URL. Um destino que não decifra (chave trocada, linha adulterada)
// sai sem hint, como na listagem da API, em vez de impedir a exportação.
func (s *Servico) lerCanais(ctx context.Context, q *db.Queries, projectID uuid.UUID) ([]canal, error) {
	linhas, err := q.ListChannels(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("listar canais: %w", err)
	}
	out := make([]canal, 0, len(linhas))
	for _, c := range linhas {
		item := canal{ID: c.ID, Kind: c.Kind, Enabled: c.Enabled}
		hint, email, err := notify.DestinoExportavel(s.box, c.ID, c.Kind, c.ConfigEnc)
		if err != nil {
			slog.Error("canal com destino ilegível na exportação", "channel_id", c.ID)
		} else {
			item.Hint = hint
			if email != "" {
				item.Email = &email
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// lerPaginas: o projeto tem no máximo uma página de status.
func lerPaginas(ctx context.Context, q *db.Queries, projectID uuid.UUID) ([]paginaDeStatus, error) {
	pg, err := q.GetStatusPage(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return []paginaDeStatus{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler página de status: %w", err)
	}
	itens, err := q.ListStatusPageItems(ctx, db.ListStatusPageItemsParams{ProjectID: projectID, PageID: pg.ID})
	if err != nil {
		return nil, fmt.Errorf("listar itens da página de status: %w", err)
	}
	out := paginaDeStatus{
		ID: pg.ID, Slug: pg.Slug, Title: pg.Title, Published: pg.Published, CreatedAt: pg.CreatedAt,
		Items: make([]itemDaPagina, 0, len(itens)),
	}
	for _, i := range itens {
		out.Items = append(out.Items, itemDaPagina{TargetID: i.TargetID, DisplayName: i.DisplayName, Position: i.Position})
	}
	return []paginaDeStatus{out}, nil
}

// lerIncidentes junta os incidentes dos alvos e os de cada heartbeat.
func lerIncidentes(ctx context.Context, q *db.Queries, projectID uuid.UUID, hbs []heartbeat) ([]incidente, error) {
	out := []incidente{}
	var antes *uuid.UUID
	for {
		linhas, err := q.ListIncidents(ctx, db.ListIncidentsParams{ProjectID: projectID, Before: antes, MaxRows: pagina})
		if err != nil {
			return nil, fmt.Errorf("listar incidentes: %w", err)
		}
		for _, i := range linhas {
			alvoID := i.TargetID
			out = append(out, incidente{
				ID: i.ID, TargetID: &alvoID, StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, Cause: i.Cause, AckAt: i.AckAt,
			})
		}
		if len(linhas) < int(pagina) {
			break
		}
		antes = &linhas[len(linhas)-1].ID
	}
	for _, hb := range hbs {
		antes = nil
		for {
			linhas, err := q.ListHeartbeatIncidents(ctx, db.ListHeartbeatIncidentsParams{
				ProjectID: projectID, HeartbeatID: hb.ID, Before: antes, MaxRows: pagina,
			})
			if err != nil {
				return nil, fmt.Errorf("listar incidentes do heartbeat %s: %w", hb.ID, err)
			}
			for _, i := range linhas {
				hbID := i.HeartbeatID
				out = append(out, incidente{
					ID: i.ID, HeartbeatID: &hbID, StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, Cause: i.Cause, AckAt: i.AckAt,
				})
			}
			if len(linhas) < int(pagina) {
				break
			}
			antes = &linhas[len(linhas)-1].ID
		}
	}
	return out, nil
}

// escritor guarda o primeiro erro de escrita: depois dele, nada mais sai.
type escritor struct {
	enc *json.Encoder
	w   io.Writer
	err error
}

func (e *escritor) bruto(s string) {
	if e.err == nil {
		_, e.err = io.WriteString(e.w, s)
	}
}

// valor escreve v com um json.Encoder (um por seção, com o objeto externo
// escrito à mão). O "\n" que o Encoder acrescenta é espaço entre tokens.
func (e *escritor) valor(v any) {
	if e.err == nil {
		e.err = e.enc.Encode(v)
	}
}
