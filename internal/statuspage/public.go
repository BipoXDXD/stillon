package statuspage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targetstate"
)

// A projeção pública (spec §10.2). Os tipos daqui são o que a página
// mostra, e nada além: nome de exibição, estado, uptime, latência agregada
// e incidentes com início e fim (spec §5.6). Nenhum carrega id, URL, status
// code ou causa, então nenhuma mudança no template os faz vazar.

// State é o estado atual de um item na página.
type State string

const (
	StateUp       State = "up"
	StateDegraded State = "degraded"
	StateDown     State = "down"
	// StateUnknown: sem check recente. Alvo desativado, sondador parado
	// ou alvo que nunca foi sondado; a página não afirma o que não sabe.
	StateUnknown State = "unknown"
)

const (
	// Days é a largura da barra de uptime (spec §10.2).
	Days = 90
	// MaxIncidents limita o histórico da página: os mais recentes.
	MaxIncidents  = 20
	latencyWindow = 24 * time.Hour
	// cacheTTL é o mesmo max-age da resposta: a página é lida quando algo
	// quebrou, e mil visitantes de IPs diferentes não podem virar mil
	// agregações de 90 dias no banco.
	cacheTTL = 30 * time.Second
	// maxCached limita a memória: slugs são publicados por usuários, e o
	// cache não pode crescer sem teto.
	maxCached = 1000
	// publicQueryTimeout limita a projeção: com o banco lento, a página
	// responde 503 em vez de segurar a conexão do visitante.
	publicQueryTimeout = 5 * time.Second
)

// Day é um dia (UTC) da barra. Uptime é nil sem nenhum check no dia.
type Day struct {
	Date   time.Time
	Uptime *float64 // 0 a 1
}

type PublicItem struct {
	Name  string
	State State
	Days  [Days]Day // do mais antigo ao de hoje
	// Uptime90 é o agregado dos 90 dias; nil sem check nenhum.
	Uptime90 *float64
	// LatencyMS é a mediana aproximada das últimas 24h; nil sem sucesso.
	LatencyMS *int32
}

type PublicIncident struct {
	Name       string
	StartedAt  time.Time
	ResolvedAt time.Time
}

func (i PublicIncident) Duration() time.Duration { return i.ResolvedAt.Sub(i.StartedAt) }

type View struct {
	Title       string
	Items       []PublicItem
	Incidents   []PublicIncident // resolvidos, do mais recente ao mais antigo
	GeneratedAt time.Time
}

// Public devolve a projeção da página publicada do slug, ou ErrNotFound. A
// página inexistente e o rascunho são o mesmo ErrNotFound: o público não
// distingue os dois. Os dados ficam em cache por cacheTTL.
func (s *Service) Public(ctx context.Context, slug string) (View, error) {
	if !slugRE.MatchString(slug) {
		return View{}, ErrNotFound // nem vai ao banco
	}
	agora := s.clk.Now()
	if v, ok := s.cache.get(slug, agora); ok {
		return v, nil
	}
	antes := s.cache.geracao(slug)
	v, err, _ := s.voo.Do(slug, func() (any, error) {
		inicio := s.cache.geracao(slug)
		v, err := s.projetarComPrazo(ctx, slug, agora)
		if s.depoisDeProjetar != nil {
			s.depoisDeProjetar(slug)
		}
		if err == nil {
			s.cache.putSe(slug, v, agora, inicio)
		}
		return v, err
	})
	// A página mudou por este processo enquanto o voo estava no ar (o dono
	// despublicou durante o acesso): o resultado pode ser o de antes, e
	// quem esperou no voo refaz a consulta em vez de recebê-lo (achado da
	// segunda revisão).
	if s.cache.geracao(slug) != antes {
		v, err = s.projetarComPrazo(ctx, slug, agora)
	}
	if err != nil {
		return View{}, err
	}
	return v.(View), nil
}

// projetarComPrazo desliga a consulta do contexto do visitante: ela é de
// todos os que esperam no voo, e o visitante que fecha a aba não derruba os
// outros.
func (s *Service) projetarComPrazo(ctx context.Context, slug string, agora time.Time) (View, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicQueryTimeout)
	defer cancel()
	return s.projetar(ctx, slug, agora)
}

func (s *Service) projetar(ctx context.Context, slug string, agora time.Time) (View, error) {
	hoje := agora.UTC().Truncate(24 * time.Hour)
	desde := hoje.AddDate(0, 0, -(Days - 1))
	v := View{GeneratedAt: agora}
	err := storage.InPublicTx(ctx, s.pool, slug, func(q *db.Queries) error {
		pagina, err := q.GetPublicPage(ctx, slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar página pública: %w", err)
		}
		v.Title = pagina.Title

		itens, err := q.ListPublicItems(ctx, pagina.ID)
		if err != nil {
			return fmt.Errorf("listar itens públicos: %w", err)
		}
		dias, err := q.ListPublicDailyUptime(ctx, db.ListPublicDailyUptimeParams{PageID: pagina.ID, Since: desde})
		if err != nil {
			return fmt.Errorf("listar uptime diário: %w", err)
		}
		latencias, err := q.ListPublicLatency(ctx, db.ListPublicLatencyParams{PageID: pagina.ID, Since: agora.Add(-latencyWindow)})
		if err != nil {
			return fmt.Errorf("listar latência: %w", err)
		}
		incidentes, err := q.ListPublicIncidents(ctx, db.ListPublicIncidentsParams{
			PageID: pagina.ID, Since: desde, MaxRows: MaxIncidents,
		})
		if err != nil {
			return fmt.Errorf("listar incidentes públicos: %w", err)
		}

		porAlvo := map[uuid.UUID][]db.ListPublicDailyUptimeRow{}
		for _, d := range dias {
			porAlvo[d.TargetID] = append(porAlvo[d.TargetID], d)
		}
		latencia := map[uuid.UUID]int32{}
		for _, l := range latencias {
			latencia[l.TargetID] = l.P50
		}
		v.Items = make([]PublicItem, 0, len(itens))
		for _, it := range itens {
			pi := PublicItem{Name: it.DisplayName, State: estado(it, agora)}
			preencherDias(&pi, desde, porAlvo[it.TargetID])
			if ms, ok := latencia[it.TargetID]; ok {
				pi.LatencyMS = &ms
			}
			v.Items = append(v.Items, pi)
		}
		v.Incidents = make([]PublicIncident, 0, len(incidentes))
		for _, x := range incidentes {
			v.Incidents = append(v.Incidents, PublicIncident{Name: x.DisplayName, StartedAt: x.StartedAt.UTC(), ResolvedAt: x.ResolvedAt.UTC()})
		}
		return nil
	})
	return v, err
}

// estado é o do último check, se ele ainda é recente. A regra mora em
// targetstate, a mesma do dashboard (D15 do Plano 5).
func estado(it db.ListPublicItemsRow, agora time.Time) State {
	return State(targetstate.Current(it.Enabled, time.Duration(it.IntervalSec)*time.Second,
		it.LastStatus, it.LastCheckedAt, agora))
}

func preencherDias(pi *PublicItem, desde time.Time, linhas []db.ListPublicDailyUptimeRow) {
	for i := range pi.Days {
		pi.Days[i].Date = desde.AddDate(0, 0, i)
	}
	var total, ok int64
	for _, l := range linhas {
		i := int(l.Day.Sub(desde) / (24 * time.Hour))
		if i < 0 || i >= Days || l.Total == 0 {
			continue
		}
		u := float64(l.Ok) / float64(l.Total)
		pi.Days[i].Uptime = &u
		total += l.Total
		ok += l.Ok
	}
	if total > 0 {
		u := float64(ok) / float64(total)
		pi.Uptime90 = &u
	}
}

// cachePublico guarda a projeção por slug. Só guarda sucesso: a página que
// acabou de ser publicada aparece na hora, e um erro de banco não fica
// preso por 30 segundos.
//
// geracoes conta as mudanças de cada slug feitas por este processo. Uma
// projeção só entra no cache se a geração não mudou desde que ela começou:
// sem isso, a projeção que leu a página antes de o dono despublicar a
// regravaria no cache depois do forget, por mais 30 s. Cresce um contador
// por slug alterado na vida do processo.
type cachePublico struct {
	mu       sync.Mutex
	entradas map[string]entradaCache
	geracoes map[string]uint64
}

type entradaCache struct {
	v    View
	vale time.Time
}

func (c *cachePublico) get(slug string, agora time.Time) (View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entradas[slug]
	if !ok || !agora.Before(e.vale) {
		return View{}, false
	}
	return e.v, true
}

// forget tira o slug do cache e avança a geração dele: a página mudou
// neste processo.
func (c *cachePublico) forget(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entradas, slug)
	if c.geracoes == nil {
		c.geracoes = map[string]uint64{}
	}
	c.geracoes[slug]++
}

func (c *cachePublico) geracao(slug string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.geracoes[slug]
}

// putSe guarda a projeção se o slug ainda está na geração em que ela
// começou.
func (c *cachePublico) putSe(slug string, v View, agora time.Time, geracao uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.geracoes[slug] != geracao {
		return
	}
	if c.entradas == nil {
		c.entradas = map[string]entradaCache{}
	}
	if len(c.entradas) >= maxCached {
		for k, e := range c.entradas {
			if !agora.Before(e.vale) {
				delete(c.entradas, k)
			}
		}
		if len(c.entradas) >= maxCached {
			return // cheio de entradas válidas: esta fica sem cache
		}
	}
	c.entradas[slug] = entradaCache{v: v, vale: agora.Add(cacheTTL)}
}

type singleflightGroup = singleflight.Group
