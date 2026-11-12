package targets_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/targetstate"
)

// gravarCheck grava um check como o sondador grava (InProberTx), criando a
// partição do mês se preciso.
func (a ambiente) gravarCheck(t *testing.T, alvo uuid.UUID, em time.Time, status string, latencia int32) {
	t.Helper()
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, a.pool, em); err != nil {
		t.Fatal(err)
	}
	p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: latencia}
	if status == "down" {
		kind := "timeout"
		p.ErrorKind = &kind
	}
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) gravarRollup(t *testing.T, alvo uuid.UUID, hora time.Time, total, ok int32, p50 *int32) {
	t.Helper()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, $3, $4, $5, $5, $5, $5)`, alvo, hora, total, ok, p50); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) alvo(t *testing.T, dono uuid.UUID) uuid.UUID {
	t.Helper()
	alvo, err := a.svc.Create(context.Background(), dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	return alvo.ID
}

func resumoDe(t *testing.T, a ambiente, dono uuid.UUID) map[uuid.UUID]targets.Summary {
	t.Helper()
	itens, err := a.svc.Summary(context.Background(), dono, targets.SummaryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]targets.Summary{}
	for _, s := range itens {
		out[s.ID] = s
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// O spark lê os rollups até a marca d'água do dono e os checks brutos depois
// dela, sem hora duplicada nem faltando; a hora corrente fica fora do spark e
// dentro do uptime.
//
// Relógio em 12:20; H = 12:00; janela do spark = [ontem 12:00, hoje 12:00).
// Rollups até 10:00 (a manutenção ainda não consolidou 11:00). Um check bruto
// às 10:30 está numa hora já consolidada e não pode contar de novo.
func TestSummarySparkMixesRollupsAndRaw(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-25*time.Hour), 60, 0, nil) // antes da janela
	for i := 24; i >= 2; i-- {                                // 23 horas: ontem 12:00 até hoje 10:00
		a.gravarRollup(t, alvo, h.Add(-time.Duration(i)*time.Hour), 60, 60, ptr(int32(100)))
	}
	a.gravarCheck(t, alvo, h.Add(-90*time.Minute), "down", 5000) // 10:30, já consolidada
	a.gravarCheck(t, alvo, h.Add(-55*time.Minute), "up", 70)     // 11:05
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "down", 5000) // 11:10
	a.gravarCheck(t, alvo, h.Add(10*time.Minute), "down", 5000)  // 12:10, hora corrente
	a.gravarCheck(t, alvo, h.Add(19*time.Minute), "down", 5000)  // 12:19, a última

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 24 {
		t.Fatalf("spark com %d horas, queria 24", len(s.Spark))
	}
	if !s.Spark[0].Bucket.Equal(h.Add(-24*time.Hour)) || !s.Spark[23].Bucket.Equal(h.Add(-time.Hour)) {
		t.Errorf("spark de %v a %v, queria de %v a %v", s.Spark[0].Bucket, s.Spark[23].Bucket, h.Add(-24*time.Hour), h.Add(-time.Hour))
	}
	for i := 1; i < len(s.Spark); i++ {
		if !s.Spark[i].Bucket.Equal(s.Spark[i-1].Bucket.Add(time.Hour)) {
			t.Fatalf("hora duplicada ou faltando entre %v e %v", s.Spark[i-1].Bucket, s.Spark[i].Bucket)
		}
	}
	dezHoras, onzeHoras := s.Spark[22], s.Spark[23]
	if dezHoras.Total != 60 || dezHoras.OK != 60 {
		t.Errorf("10:00 = %+v; queria o rollup (60/60), sem o check bruto das 10:30", dezHoras)
	}
	if onzeHoras.Total != 2 || onzeHoras.OK != 1 || onzeHoras.P50 == nil || *onzeHoras.P50 != 70 || onzeHoras.Bucket.Location() != time.UTC {
		t.Errorf("11:00 = %+v; queria 2 checks, 1 ok, p50 70, em UTC", onzeHoras)
	}
	// 23 horas × 60 + 2 brutos das 11h + 2 da hora corrente.
	if s.Uptime24h == nil || *s.Uptime24h != float64(23*60+1)/float64(23*60+2+2) {
		t.Errorf("uptime_24h = %v, queria %v", s.Uptime24h, float64(23*60+1)/float64(23*60+2+2))
	}
	if s.State != targetstate.Down || s.LastLatencyMS != nil || s.LastCheckedAt == nil || !s.LastCheckedAt.Equal(h.Add(19*time.Minute)) {
		t.Errorf("estado %q, latência %v, último check %v; queria down, nil, 12:19", s.State, s.LastLatencyMS, s.LastCheckedAt)
	}
}

// Sem rollup nenhum (instalação nova, ou manutenção parada), o corte recua ao
// início da janela e tudo vem dos brutos.
func TestSummaryWithoutRollupsReadsRaw(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarCheck(t, alvo, h.Add(-24*time.Hour-time.Second), "up", 10) // antes da janela
	a.gravarCheck(t, alvo, h.Add(-24*time.Hour), "up", 20)             // primeira hora
	a.gravarCheck(t, alvo, h.Add(-time.Second), "degraded", 30)        // última hora
	a.gravarCheck(t, alvo, h, "up", 40)                                // hora corrente

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 2 || !s.Spark[0].Bucket.Equal(h.Add(-24*time.Hour)) || !s.Spark[1].Bucket.Equal(h.Add(-time.Hour)) {
		t.Fatalf("spark = %+v; queria as horas de ontem 12:00 e de hoje 11:00", s.Spark)
	}
	if *s.Spark[1].P50 != 30 {
		t.Errorf("degraded conta como sucesso no p50: p50 = %d, queria 30", *s.Spark[1].P50)
	}
	if s.Uptime24h == nil || *s.Uptime24h != 1 {
		t.Errorf("uptime_24h = %v, queria 1 (três checks, nenhum down)", s.Uptime24h)
	}
	if s.State != targetstate.Up || s.LastLatencyMS == nil || *s.LastLatencyMS != 40 {
		t.Errorf("estado %q, latência %v; queria up e 40", s.State, s.LastLatencyMS)
	}
}

// Uma hora bruta sem nenhum sucesso (ok = 0) entra no spark sem p50, como o
// rollup de uma hora toda fora: a consulta devolve a latência dela como 0.
func TestSummarySparkLeavesP50NilForARawHourWithoutSuccess(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarCheck(t, alvo, h.Add(-30*time.Minute), "down", 5000) // 11:30
	a.gravarCheck(t, alvo, h.Add(-20*time.Minute), "down", 5000) // 11:40

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 1 {
		t.Fatalf("spark = %+v; queria só a hora das 11:00", s.Spark)
	}
	if hora := s.Spark[0]; hora.Total != 2 || hora.OK != 0 || hora.P50 != nil {
		t.Errorf("11:00 = %+v (p50 %v); queria 2 checks, nenhum ok, p50 nil", hora, hora.P50)
	}
}

// O spark guarda o p50 da hora bruta. A linha traz também p95, p99 e max, e
// três sucessos com latências diferentes separam o p50 (20) dos outros (90).
func TestSummarySparkTakesTheP50OfARawHour(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "up", 10) // 11:10
	a.gravarCheck(t, alvo, h.Add(-40*time.Minute), "up", 20) // 11:20
	a.gravarCheck(t, alvo, h.Add(-30*time.Minute), "up", 90) // 11:30

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 1 {
		t.Fatalf("spark = %+v; queria só a hora das 11:00", s.Spark)
	}
	if hora := s.Spark[0]; hora.Total != 3 || hora.OK != 3 || hora.P50 == nil || *hora.P50 != 20 {
		t.Errorf("11:00 = %+v (p50 %v); queria 3 checks, 3 ok, p50 20", hora, hora.P50)
	}
}

// Uma queda na hora corrente derruba o uptime na hora: é por isso que a hora
// corrente entra nele.
func TestSummaryUptimeSeesAnOngoingOutage(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(20 * time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	for i := 24; i >= 1; i-- {
		a.gravarRollup(t, alvo, h.Add(-time.Duration(i)*time.Hour), 60, 60, ptr(int32(100)))
	}
	for m := range 20 {
		a.gravarCheck(t, alvo, h.Add(time.Duration(m)*time.Minute), "down", 5000)
	}

	s := resumoDe(t, a, dono)[alvo]
	if s.Uptime24h == nil || *s.Uptime24h >= 1 {
		t.Errorf("uptime_24h = %v com 20 minutos fora agora; a queda em curso não apareceu", s.Uptime24h)
	}
	if len(s.Spark) != 24 {
		t.Errorf("spark com %d horas, queria 24 (a hora corrente fica fora)", len(s.Spark))
	}
}

// Um rollup gravado no futuro (relógio adiantado) não vira a marca d'água:
// com ele, o corte ia para a hora corrente e as horas fechadas ainda não
// consolidadas sumiam do spark e do uptime.
//
// Relógio em 12:20; rollups às 09:00 (60 de 60) e às 13:00 (futuro). O
// check das 11:30 foi down: o spark tem 09:00 e 11:00, e o uptime é 60/61.
func TestSummaryIgnoresRollupsFromTheFuture(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(20 * time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarRollup(t, alvo, h.Add(-3*time.Hour), 60, 60, ptr(int32(100)))
	a.gravarRollup(t, alvo, h.Add(time.Hour), 60, 60, ptr(int32(100)))
	a.gravarCheck(t, alvo, h.Add(-30*time.Minute), "down", 5000)

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 2 || !s.Spark[0].Bucket.Equal(h.Add(-3*time.Hour)) || !s.Spark[1].Bucket.Equal(h.Add(-time.Hour)) {
		t.Fatalf("spark = %+v; queria as horas 09:00 (rollup) e 11:00 (bruto)", s.Spark)
	}
	if s.Uptime24h == nil || *s.Uptime24h != 60.0/61.0 {
		t.Errorf("uptime_24h = %v, queria 60/61", s.Uptime24h)
	}
}

// Alvo sem check: tudo nulo e spark vazio, não nil. Alvo desativado com check
// recente: unknown, mas o último check continua visível.
func TestSummaryNullables(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	vazio := a.alvo(t, dono)
	desligado := a.alvo(t, dono)
	if _, err := a.svc.Update(context.Background(), dono, desligado, targets.Patch{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	a.gravarCheck(t, desligado, a.clk.Now().Add(-time.Second), "up", 55)

	r := resumoDe(t, a, dono)
	v := r[vazio]
	if v.State != targetstate.Unknown || v.LastCheckedAt != nil || v.LastLatencyMS != nil || v.Uptime24h != nil || v.OpenIncidentID != nil || len(v.Spark) != 0 {
		t.Errorf("alvo sem check = %+v", v)
	}
	d := r[desligado]
	if d.State != targetstate.Unknown || d.Enabled || d.LastLatencyMS == nil || *d.LastLatencyMS != 55 {
		t.Errorf("alvo desativado = %+v; queria unknown com a latência do último check", d)
	}
}

// O incidente aberto sai; o resolvido não.
func TestSummaryOpenIncident(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	aberto := a.alvo(t, dono)
	resolvido := a.alvo(t, dono)
	inc := uuid.Must(uuid.NewV7())
	agora := a.clk.Now()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		inc, aberto, agora.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause) VALUES ($1, $2, $3, $4, 'timeout')`,
		uuid.Must(uuid.NewV7()), resolvido, agora.Add(-time.Hour), agora.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := resumoDe(t, a, dono)
	if r[aberto].OpenIncidentID == nil || *r[aberto].OpenIncidentID != inc {
		t.Errorf("incidente aberto = %v, queria %v", r[aberto].OpenIncidentID, inc)
	}
	if r[resolvido].OpenIncidentID != nil {
		t.Errorf("o incidente resolvido saiu como aberto: %v", r[resolvido].OpenIncidentID)
	}
}

// A ordem é a de GET /targets: id decrescente (o mais novo primeiro).
func TestSummaryOrderIsTheListOrder(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	var criados []uuid.UUID
	for range 3 {
		criados = append(criados, a.alvo(t, dono))
	}
	itens, err := a.svc.Summary(context.Background(), dono, targets.SummaryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	lista, err := a.svc.List(context.Background(), dono, nil, targets.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 3 || len(lista) != 3 {
		t.Fatalf("%d itens no resumo, %d na lista", len(itens), len(lista))
	}
	for i := range itens {
		if itens[i].ID != lista[i].ID {
			t.Errorf("posição %d: resumo %v, lista %v", i, itens[i].ID, lista[i].ID)
		}
	}
	if itens[0].ID != criados[2] {
		t.Errorf("o primeiro não é o mais novo")
	}
}

// Nada de outro usuário entra: nem alvo, nem check no uptime, nem rollup no
// spark, nem incidente.
//
// Duas barreiras dão esta propriedade: o WHERE user_id das consultas e a RLS
// da InUserTx. Sabotar só uma deixa o teste verde; o plano registra a
// sabotagem das duas juntas.
//
// O que este teste prova é a barreira da consulta 1 (ListTargetSummaries):
// a montagem do resumo só percorre os alvos que ela devolve. O filtro de
// usuário das consultas 2 a 4 (rollups, brutos, hora corrente) — e o da marca
// d'água — é defesa em profundidade que a saída não observa: uma linha alheia
// que passasse por elas ficaria num mapa por alvo que ninguém lê. Tirá-lo
// deixa este teste verde, e não é vácuo do teste: não há o que observar.
func TestSummaryIsolation(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono, intruso := a.usuario(t), a.usuario(t)
	meu := a.alvo(t, dono)
	alheio := a.alvo(t, intruso)
	a.gravarRollup(t, alheio, h.Add(-3*time.Hour), 60, 0, nil)
	a.gravarCheck(t, alheio, h.Add(-30*time.Minute), "down", 1)
	a.gravarCheck(t, alheio, h.Add(5*time.Minute), "down", 1)
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		uuid.Must(uuid.NewV7()), alheio, h); err != nil {
		t.Fatal(err)
	}
	a.gravarCheck(t, meu, h.Add(5*time.Minute), "up", 10)

	r := resumoDe(t, a, dono)
	if len(r) != 1 {
		t.Fatalf("%d alvos no resumo do dono, queria 1", len(r))
	}
	m := r[meu]
	if m.Uptime24h == nil || *m.Uptime24h != 1 || len(m.Spark) != 0 || m.OpenIncidentID != nil {
		t.Errorf("o resumo do dono carrega dados alheios: %+v", m)
	}
}

// Os buckets são horas cheias em UTC mesmo com a sessão do banco num fuso de
// meia hora: date_trunc em timestamptz usaria o fuso da sessão.
func TestSummaryBucketsAreUTCHoursInAnyTimeZone(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "up", 10)

	dsn, err := url.Parse(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("timezone", "Asia/Kolkata")
	dsn.RawQuery = q.Encode()
	kolkata, err := storage.NewPool(context.Background(), dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer kolkata.Close()

	svc := targets.NewService(kolkata, caixa(t), a.clk)
	itens, err := svc.Summary(context.Background(), dono, targets.SummaryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 1 || len(itens[0].Spark) != 1 || !itens[0].Spark[0].Bucket.Equal(h.Add(-time.Hour)) {
		t.Fatalf("spark = %+v; queria uma hora, 11:00 UTC", itens)
	}
}

// Borda do corte, lado dos rollups: um rollup gravado na própria hora corrente
// (relógio adiantado ou consolidação precoce) não é a marca d'água, porque a
// marca só vê horas antes de H. O corte então é H, e o rollup das 12:00 fica
// de fora do spark: a hora corrente só entra, por contagem, pelos checks.
//
// Relógio em 12:20; rollups às 11:00 (60 de 60) e às 12:00 (10 de 10, que não
// pode entrar); check bruto das 11:59:59.999 numa hora já consolidada e o das
// 12:00:00 exato, que é da hora corrente (down).
func TestSummaryCutEdgeIgnoresARollupAtTheCurrentHour(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-time.Hour), 60, 60, ptr(int32(100)))
	a.gravarRollup(t, alvo, h, 10, 10, ptr(int32(100)))
	a.gravarCheck(t, alvo, h.Add(-time.Millisecond), "down", 5000)
	a.gravarCheck(t, alvo, h, "down", 5000)

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 1 || !s.Spark[0].Bucket.Equal(h.Add(-time.Hour)) || s.Spark[0].Total != 60 || s.Spark[0].OK != 60 {
		t.Fatalf("spark = %+v; queria só o rollup das 11:00 (60/60)", s.Spark)
	}
	if want := 60.0 / 61.0; s.Uptime24h == nil || *s.Uptime24h != want {
		t.Errorf("uptime_24h = %v, queria %v (60 do rollup + 1 down da hora corrente)", s.Uptime24h, want)
	}
}

// Borda do corte, lado dos brutos: o corte é a hora seguinte à marca d'água e
// vale como [corte, H) nos brutos e [início, corte) nos rollups. Um check às
// 10:59:59.999 é da hora consolidada e não conta de novo; um check às 11:00:00
// exato é a primeira hora bruta, e não some.
//
// Relógio em 12:20; rollup só às 10:00 (60 de 60), corte às 11:00.
func TestSummaryCutEdgeSplitsRollupsAndRawAtTheExactInstant(t *testing.T) {
	a := novoAmbiente(t)
	a.clk.Advance(time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC).Sub(a.clk.Now()))
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-2*time.Hour), 60, 60, ptr(int32(100)))
	a.gravarCheck(t, alvo, h.Add(-time.Hour-time.Millisecond), "down", 5000) // 10:59:59.999
	a.gravarCheck(t, alvo, h.Add(-time.Hour), "up", 70)                      // 11:00:00
	a.gravarCheck(t, alvo, h.Add(-time.Millisecond), "down", 5000)           // 11:59:59.999

	s := resumoDe(t, a, dono)[alvo]
	if len(s.Spark) != 2 {
		t.Fatalf("spark com %d horas (%+v), queria 2", len(s.Spark), s.Spark)
	}
	dez, onze := s.Spark[0], s.Spark[1]
	if !dez.Bucket.Equal(h.Add(-2*time.Hour)) || dez.Total != 60 || dez.OK != 60 {
		t.Errorf("10:00 = %+v; queria o rollup (60/60), sem o check das 10:59:59.999", dez)
	}
	if !onze.Bucket.Equal(h.Add(-time.Hour)) || onze.Total != 2 || onze.OK != 1 {
		t.Errorf("11:00 = %+v; queria 2 checks (11:00:00 up e 11:59:59.999 down)", onze)
	}
	if want := 61.0 / 62.0; s.Uptime24h == nil || *s.Uptime24h != want {
		t.Errorf("uptime_24h = %v, queria %v", s.Uptime24h, want)
	}
}
