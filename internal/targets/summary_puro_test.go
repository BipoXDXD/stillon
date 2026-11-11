package targets

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

func ptrI32(v int32) *int32 { return &v }

func TestSparkPorAlvoJuntaRollupsEBrutosPorAlvo(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	h0 := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	h1, h2 := h0.Add(time.Hour), h0.Add(2*time.Hour)

	got := sparkPorAlvo(
		[]db.ListSummaryRollupsRow{
			{TargetID: a, Bucket: h0, Total: 10, Ok: 9, P50: ptrI32(120)},
			{TargetID: b, Bucket: h1, Total: 7, Ok: 3, P50: nil},
		},
		[]db.ListRawHoursRow{
			{TargetID: a, Bucket: h2, Total: 5, Ok: 4, P50: 80},
		},
	)

	if len(got) != 2 || len(got[a]) != 2 || len(got[b]) != 1 {
		t.Fatalf("tamanhos inesperados: %+v", got)
	}
	if h := got[a][0]; !h.Bucket.Equal(h0) || h.Total != 10 || h.OK != 9 || h.P50 == nil || *h.P50 != 120 {
		t.Errorf("hora do rollup de a = %+v", h)
	}
	if h := got[a][1]; !h.Bucket.Equal(h2) || h.Total != 5 || h.OK != 4 || h.P50 == nil || *h.P50 != 80 {
		t.Errorf("hora bruta de a = %+v (deve vir depois do rollup)", h)
	}
	if h := got[b][0]; !h.Bucket.Equal(h1) || h.Total != 7 || h.OK != 3 || h.P50 != nil {
		t.Errorf("hora de b = %+v", h)
	}
}

func TestSparkPorAlvoDevolveOsBucketsEmUTC(t *testing.T) {
	brasilia := time.FixedZone("BRT", -3*3600)
	// 05:00 em Brasília é 08:00 UTC; Equal sozinho não enxerga o fuso.
	deBrasilia := time.Date(2026, 11, 10, 5, 0, 0, 0, brasilia)
	a := uuid.New()

	got := sparkPorAlvo(
		[]db.ListSummaryRollupsRow{{TargetID: a, Bucket: deBrasilia, Total: 10, Ok: 9, P50: ptrI32(120)}},
		[]db.ListRawHoursRow{{TargetID: a, Bucket: deBrasilia.Add(time.Hour), Total: 5, Ok: 4, P50: 80}},
	)

	if len(got[a]) != 2 {
		t.Fatalf("horas de a = %+v, quero 2", got[a])
	}
	for i, quero := range []time.Time{
		time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
	} {
		if b := got[a][i].Bucket; !b.Equal(quero) || b.Location() != time.UTC {
			t.Errorf("bucket %d = %v (%v), quero %v em UTC", i, b, b.Location(), quero)
		}
	}
}

func TestSparkPorAlvoHoraBrutaSemSucessoNaoTemP50(t *testing.T) {
	a := uuid.New()
	h := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

	got := sparkPorAlvo(nil, []db.ListRawHoursRow{
		// P50 diferente de zero de propósito: com Ok=0 a agregação não tem
		// latência de sucesso para reportar, e o valor bruto não pode vazar.
		{TargetID: a, Bucket: h, Total: 3, Ok: 0, P50: 999, P95: 999, P99: 999, Max: 999},
	})

	if len(got[a]) != 1 {
		t.Fatalf("horas de a = %+v, quero 1", got[a])
	}
	if hr := got[a][0]; !hr.Bucket.Equal(h) || hr.Total != 3 || hr.OK != 0 || hr.P50 != nil {
		t.Errorf("hora bruta sem sucesso = %+v, quero Total 3, OK 0 e P50 nil", hr)
	}
}

func TestSparkPorAlvoSemLinhasDevolveMapaVazio(t *testing.T) {
	if got := sparkPorAlvo(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("got = %#v, quero mapa vazio não nulo", got)
	}
}

func TestContagemPorAlvoConvertePorAlvo(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := contagemPorAlvo([]db.ListSummaryCurrentHourRow{
		{TargetID: a, Total: 6, Ok: 5},
		{TargetID: b, Total: 11, Ok: 2},
	})
	if got[a] != (contagem{total: 6, ok: 5}) || got[b] != (contagem{total: 11, ok: 2}) {
		t.Errorf("got = %+v", got)
	}
	if c := got[uuid.New()]; c != (contagem{}) {
		t.Errorf("alvo ausente deve dar contagem zerada, deu %+v", c)
	}
}

func TestUltimoCheck(t *testing.T) {
	brasilia := time.FixedZone("BRT", -3*3600)
	quando := time.Date(2026, 11, 10, 9, 30, 0, 0, brasilia)
	casos := []struct {
		nome    string
		linha   db.ListTargetSummariesRow
		temEm   bool
		latency *int32
	}{
		{"sem check", db.ListTargetSummariesRow{LastStatus: "", LastCheckedAt: time.Unix(0, 0), LastLatencyMS: 77}, false, nil},
		{"up", db.ListTargetSummariesRow{LastStatus: "up", LastCheckedAt: quando, LastLatencyMS: 143}, true, ptrI32(143)},
		{"degraded", db.ListTargetSummariesRow{LastStatus: "degraded", LastCheckedAt: quando, LastLatencyMS: 2210}, true, ptrI32(2210)},
		{"down sem latência", db.ListTargetSummariesRow{LastStatus: "down", LastCheckedAt: quando, LastLatencyMS: 10000}, true, nil},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			em, ms := ultimoCheck(c.linha)
			if (em != nil) != c.temEm {
				t.Fatalf("em = %v, quero presente=%v", em, c.temEm)
			}
			if c.temEm && (!em.Equal(quando) || em.Location() != time.UTC) {
				t.Errorf("em = %v, quero %v em UTC", em, quando)
			}
			if (ms == nil) != (c.latency == nil) || (ms != nil && *ms != *c.latency) {
				t.Errorf("latência = %v, quero %v", ms, c.latency)
			}
		})
	}
}

func TestUptime24h(t *testing.T) {
	spark := []SparkHour{{Total: 10, OK: 9}, {Total: 20, OK: 15}}
	t.Run("soma a hora corrente e o spark", func(t *testing.T) {
		got := uptime24h(contagem{total: 10, ok: 6}, spark)
		// (6+9+15)/(10+10+20) = 30/40
		if got == nil || *got != 0.75 {
			t.Errorf("got = %v, quero 0.75", got)
		}
	})
	t.Run("só hora corrente", func(t *testing.T) {
		got := uptime24h(contagem{total: 8, ok: 2}, nil)
		if got == nil || *got != 0.25 {
			t.Errorf("got = %v, quero 0.25", got)
		}
	})
	t.Run("só spark", func(t *testing.T) {
		got := uptime24h(contagem{}, spark)
		if got == nil || *got != 24.0/30.0 {
			t.Errorf("got = %v, quero 0.8", got)
		}
	})
	t.Run("sem check nenhum é nil", func(t *testing.T) {
		if got := uptime24h(contagem{}, nil); got != nil {
			t.Errorf("got = %v, quero nil", *got)
		}
	})
}

func TestMontarResumosFiltraOrdenaEPreservaAOrdemDosAlvos(t *testing.T) {
	agora := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	h := agora.Truncate(time.Hour)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	incidente := uuid.New()
	alvos := []db.ListTargetSummariesRow{
		{ID: a, Name: "alfa", Enabled: true, IntervalSec: 60, LastStatus: "up", LastCheckedAt: agora.Add(-time.Minute), LastLatencyMS: 55, OpenIncidentID: &incidente},
		{ID: b, Name: "beta", Enabled: true, IntervalSec: 60, LastStatus: "up", LastCheckedAt: agora.Add(-time.Minute), LastLatencyMS: 66},
		{ID: c, Name: "gama", Enabled: false, IntervalSec: 120},
	}
	base := resumoBase{
		agora:  agora,
		filtro: SummaryFilter{Tags: []string{"prod"}},
		tags:   map[uuid.UUID][]string{a: {"prod", "api"}, b: {"staging"}, c: {"prod"}},
		horas: map[uuid.UUID][]SparkHour{
			a: {{Bucket: h.Add(-time.Hour), Total: 4, OK: 4}, {Bucket: h.Add(-3 * time.Hour), Total: 6, OK: 3}},
		},
		corrente: map[uuid.UUID]contagem{a: {total: 2, ok: 1}},
	}

	got := montarResumos(alvos, base)

	if len(got) != 2 || got[0].ID != a || got[1].ID != c {
		t.Fatalf("ids = %+v, quero [a c] (b filtrado)", got)
	}
	sa := got[0]
	if sa.Name != "alfa" || sa.OpenIncidentID == nil || *sa.OpenIncidentID != incidente {
		t.Errorf("campos simples de a: %+v", sa)
	}
	if len(sa.Spark) != 2 || !sa.Spark[0].Bucket.Equal(h.Add(-3*time.Hour)) || !sa.Spark[1].Bucket.Equal(h.Add(-time.Hour)) {
		t.Errorf("spark de a deve vir ordenado: %+v", sa.Spark)
	}
	// (1+4+3)/(2+4+6) = 8/12
	if sa.Uptime24h == nil || *sa.Uptime24h != 8.0/12.0 {
		t.Errorf("uptime de a = %v", sa.Uptime24h)
	}
	if sa.LastLatencyMS == nil || *sa.LastLatencyMS != 55 || sa.LastCheckedAt == nil {
		t.Errorf("último check de a: %v %v", sa.LastCheckedAt, sa.LastLatencyMS)
	}
	sc := got[1]
	if sc.LastCheckedAt != nil || sc.LastLatencyMS != nil || sc.Uptime24h != nil || len(sc.Spark) != 0 {
		t.Errorf("c sem checks deve ter nulos: %+v", sc)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "prod" {
		t.Errorf("tags de c = %v", sc.Tags)
	}
}

func TestMontarResumosFiltraPorEstadoEPausado(t *testing.T) {
	agora := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	recente := agora.Add(-time.Minute)
	sobe, pausado, cai, semCheck := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	alvos := []db.ListTargetSummariesRow{
		{ID: sobe, Name: "sobe", Enabled: true, IntervalSec: 60, LastStatus: "up", LastCheckedAt: recente, LastLatencyMS: 40},
		// Pausado com último check "up" recente: é "paused" no filtro, e
		// nunca "up" nem "unknown".
		{ID: pausado, Name: "pausado", Enabled: false, IntervalSec: 60, LastStatus: "up", LastCheckedAt: recente, LastLatencyMS: 41},
		{ID: cai, Name: "cai", Enabled: true, IntervalSec: 60, LastStatus: "down", LastCheckedAt: recente, LastLatencyMS: 10000},
		{ID: semCheck, Name: "sem-check", Enabled: true, IntervalSec: 60},
	}
	casos := []struct {
		nome    string
		estados []string
		quero   []uuid.UUID
	}{
		{"sem estados não filtra", nil, []uuid.UUID{sobe, pausado, cai, semCheck}},
		{"paused pega só o desativado", []string{EstadoPausado}, []uuid.UUID{pausado}},
		{"up não pega o pausado", []string{"up"}, []uuid.UUID{sobe}},
		{"unknown não pega o pausado", []string{"unknown"}, []uuid.UUID{semCheck}},
		{"estados são alternativas (OU)", []string{"down", EstadoPausado}, []uuid.UUID{pausado, cai}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := montarResumos(alvos, resumoBase{agora: agora, filtro: SummaryFilter{States: c.estados}})
			ids := make([]uuid.UUID, len(got))
			for i, sm := range got {
				ids[i] = sm.ID
			}
			if !slices.Equal(ids, c.quero) {
				t.Errorf("ids = %v, quero %v", ids, c.quero)
			}
		})
	}
}
