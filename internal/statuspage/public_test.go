package statuspage_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/statuspage"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// admin enxerga o banco como superusuário: grava rollups e incidentes em
// qualquer data, sem depender do job de manutenção.
func admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := storage.NewPool(context.Background(), testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func (a ambiente) check(t *testing.T, alvo uuid.UUID, em time.Time, status string) {
	t.Helper()
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, a.pool, em); err != nil {
		t.Fatal(err)
	}
	p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: 50}
	if status == "down" {
		kind := "timeout"
		p.ErrorKind = &kind
	}
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) })
	if err != nil {
		t.Fatal(err)
	}
}

func rollup(t *testing.T, adm *pgxpool.Pool, alvo uuid.UUID, hora time.Time, total, ok int, p50 *int) {
	t.Helper()
	if _, err := adm.Exec(context.Background(), `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, $3, $4, $5, $5, $5, $5)`, alvo, hora, total, ok, p50); err != nil {
		t.Fatal(err)
	}
}

func incidenteResolvido(t *testing.T, adm *pgxpool.Pool, alvo uuid.UUID, inicio, fim time.Time) {
	t.Helper()
	if _, err := adm.Exec(context.Background(), `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause)
		VALUES ($1, $2, $3, $4, 'timeout')`, uuid.Must(uuid.NewV7()), alvo, inicio, fim); err != nil {
		t.Fatal(err)
	}
}

func (a ambiente) publicar(t *testing.T, dono uuid.UUID, slug string, publicada bool, itens ...statuspage.Item) statuspage.Page {
	t.Helper()
	p, err := a.svc.Create(context.Background(), dono, statuspage.Input{Slug: slug, Title: "Status", Published: publicada, Items: itens})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Spec §10: rascunho, slug inexistente e slug malformado são o mesmo 404.
func TestPublicFindsOnlyPublishedPages(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	a.publicar(t, a.usuario(t), "rascunho", false)
	for _, slug := range []string{"rascunho", "nao-existe", "Maiuscula", "../etc", ""} {
		if _, err := a.svc.Public(ctx, slug); !errors.Is(err, statuspage.ErrNotFound) {
			t.Errorf("Public(%q) = %v, queria ErrNotFound", slug, err)
		}
	}
}

func TestPublicProjection(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site, velho, desligado, fora := a.alvo(t, dono, "api"), a.alvo(t, dono, "site"),
		a.alvo(t, dono, "velho"), a.alvo(t, dono, "desligado"), a.alvo(t, dono, "fora da página")
	nunca := a.alvo(t, dono, "nunca sondado")
	if _, err := adm.Exec(ctx, `UPDATE targets SET enabled = false WHERE id = $1`, desligado); err != nil {
		t.Fatal(err)
	}
	a.publicar(t, dono, "empresa", true,
		statuspage.Item{TargetID: site, DisplayName: "Site"}, statuspage.Item{TargetID: api, DisplayName: "API"},
		statuspage.Item{TargetID: velho, DisplayName: "Velho"}, statuspage.Item{TargetID: desligado, DisplayName: "Desligado"},
		statuspage.Item{TargetID: nunca, DisplayName: "Nunca"})

	agora := a.clk.Now() // 2026-09-24 12:00 UTC; intervalo dos alvos: 60s
	a.check(t, api, agora.Add(-time.Minute), "down")
	a.check(t, site, agora.Add(-3*time.Minute), "degraded") // exatamente 3 intervalos: ainda recente
	a.check(t, velho, agora.Add(-3*time.Minute-time.Second), "up")
	a.check(t, desligado, agora.Add(-time.Minute), "up")
	a.check(t, fora, agora.Add(-time.Minute), "up")

	hoje := agora.Truncate(24 * time.Hour)
	primeiro := hoje.AddDate(0, 0, -(statuspage.Days - 1))
	p50a, p50b := 100, 300
	rollup(t, adm, api, hoje.Add(8*time.Hour), 60, 60, &p50a)    // hoje: 60 de 60
	rollup(t, adm, api, hoje.Add(9*time.Hour), 60, 30, &p50b)    // hoje: +30 de 60
	rollup(t, adm, api, primeiro.Add(time.Hour), 10, 0, nil)     // 1º dia: 0 de 10
	rollup(t, adm, api, primeiro.Add(-time.Hour), 10, 10, &p50a) // antes da barra: fora
	rollup(t, adm, fora, hoje.Add(8*time.Hour), 60, 0, nil)      // fora da página

	incidenteResolvido(t, adm, api, agora.Add(-48*time.Hour), agora.Add(-47*time.Hour))
	incidenteResolvido(t, adm, api, agora.Add(-2*time.Hour), agora.Add(-time.Hour-50*time.Minute))
	incidenteResolvido(t, adm, fora, agora.Add(-time.Hour), agora.Add(-30*time.Minute))
	incidenteResolvido(t, adm, api, primeiro.Add(-time.Hour), primeiro.Add(-30*time.Minute)) // antes dos 90 dias
	if _, err := adm.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		uuid.Must(uuid.NewV7()), site, agora.Add(-time.Minute)); err != nil { // aberto: não entra
		t.Fatal(err)
	}

	v, err := a.svc.Public(ctx, "empresa")
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "Status" || len(v.Items) != 5 {
		t.Fatalf("página = %q com %d itens, queria os 5 da página", v.Title, len(v.Items))
	}
	nomes := []string{}
	estados := map[string]statuspage.State{}
	for _, it := range v.Items {
		nomes = append(nomes, it.Name)
		estados[it.Name] = it.State
	}
	if strings.Join(nomes, ",") != "Site,API,Velho,Desligado,Nunca" {
		t.Errorf("ordem = %v, queria a da página", nomes)
	}
	for nome, quer := range map[string]statuspage.State{
		"Site": statuspage.StateDegraded, "API": statuspage.StateDown, "Velho": statuspage.StateUnknown,
		"Desligado": statuspage.StateUnknown, "Nunca": statuspage.StateUnknown,
	} {
		if estados[nome] != quer {
			t.Errorf("estado de %s = %q, queria %q", nome, estados[nome], quer)
		}
	}

	apiItem := v.Items[1]
	if !apiItem.Days[0].Date.Equal(primeiro) || !apiItem.Days[statuspage.Days-1].Date.Equal(hoje) {
		t.Errorf("barra de %v a %v, queria de %v a %v", apiItem.Days[0].Date, apiItem.Days[statuspage.Days-1].Date, primeiro, hoje)
	}
	if u := apiItem.Days[statuspage.Days-1].Uptime; u == nil || *u != 0.75 {
		t.Errorf("uptime de hoje = %v, queria 0.75 (90 de 120)", u)
	}
	if u := apiItem.Days[0].Uptime; u == nil || *u != 0 {
		t.Errorf("uptime do 1º dia = %v, queria 0", u)
	}
	if apiItem.Days[1].Uptime != nil {
		t.Errorf("dia sem check tem uptime %v, queria nil", *apiItem.Days[1].Uptime)
	}
	if u := apiItem.Uptime90; u == nil || *u != 90.0/130.0 {
		t.Errorf("uptime de 90 dias = %v, queria 90/130", u)
	}
	// Mediana ponderada pelos sucessos: (100×60 + 300×30) / 90 = 166,67,
	// arredondada (sum de bigint é numeric, e o ::int arredonda).
	if l := apiItem.LatencyMS; l == nil || *l != 167 {
		t.Errorf("latência = %v, queria 167", l)
	}
	if v.Items[0].Uptime90 != nil || v.Items[0].LatencyMS != nil {
		t.Errorf("item sem rollup tem uptime/latência: %+v", v.Items[0])
	}

	if len(v.Incidents) != 2 || v.Incidents[0].Name != "API" ||
		!v.Incidents[0].StartedAt.Equal(agora.Add(-2*time.Hour)) || v.Incidents[0].Duration() != 10*time.Minute ||
		!v.Incidents[1].StartedAt.Equal(agora.Add(-48*time.Hour)) {
		t.Errorf("incidentes = %+v; queria os 2 resolvidos da API nos 90 dias, o mais recente primeiro", v.Incidents)
	}
}

func TestPublicIncidentsAreCapped(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono, "api")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: alvo, DisplayName: "API"})
	for i := range statuspage.MaxIncidents + 5 {
		inicio := a.clk.Now().Add(-time.Duration(i+1) * time.Hour)
		incidenteResolvido(t, adm, alvo, inicio, inicio.Add(time.Minute))
	}
	v, err := a.svc.Public(context.Background(), "empresa")
	if err != nil || len(v.Incidents) != statuspage.MaxIncidents {
		t.Errorf("%d incidentes (%v), queria %d", len(v.Incidents), err, statuspage.MaxIncidents)
	}
}

// O alvo apagado some também da página pública, com o histórico de
// incidentes dele (D18 do Plano 3: apagar o alvo leva o histórico).
func TestDeletedTargetLeavesThePublicPage(t *testing.T) {
	a := novoAmbiente(t)
	adm := admin(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: api, DisplayName: "API"}, statuspage.Item{TargetID: site, DisplayName: "Site"})
	incidenteResolvido(t, adm, api, a.clk.Now().Add(-2*time.Hour), a.clk.Now().Add(-time.Hour))
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: dono, ID: api})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.svc.Public(ctx, "empresa")
	if err != nil || len(v.Items) != 1 || v.Items[0].Name != "Site" || len(v.Incidents) != 0 {
		t.Errorf("depois de apagar o alvo: %+v, %v; queria só o Site, sem incidentes", v, err)
	}
}

// A projeção fica 30s em cache: mil visitantes não viram mil agregações. A
// página que acabou de ser publicada aparece na hora (o 404 não é guardado).
func TestPublicViewIsCachedForThirtySeconds(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	p := a.publicar(t, dono, "empresa", false)
	if _, err := a.svc.Public(ctx, "empresa"); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("rascunho = %v", err)
	}
	sim := true
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Published: &sim}); err != nil {
		t.Fatal(err)
	}
	v, err := a.svc.Public(ctx, "empresa")
	if err != nil || v.Title != "Status" {
		t.Fatalf("recém-publicada = %+v, %v; queria visível na hora", v, err)
	}

	novo := "Título novo"
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Title: &novo}); err != nil {
		t.Fatal(err)
	}
	a.clk.Advance(29 * time.Second)
	if v, _ := a.svc.Public(ctx, "empresa"); v.Title != "Status" {
		t.Errorf("antes de 30s veio %q, queria a versão em cache", v.Title)
	}
	a.clk.Advance(time.Second)
	if v, _ := a.svc.Public(ctx, "empresa"); v.Title != "Título novo" {
		t.Errorf("depois de 30s veio %q, queria a versão nova", v.Title)
	}
}

// Spec §5.6: a projeção pública não tem onde guardar id, URL, código de
// status ou causa. O teste percorre os tipos; um campo novo com um desses
// tipos ou nomes quebra aqui antes de chegar ao template.
func TestPublicViewHasNoInternalFields(t *testing.T) {
	// Pedaços de nome em CamelCase: "ID" pega TargetID, e não Incidents.
	proibidos := []string{"ID", "URL", "Cause", "Code", "Header", "User", "Error", "Target", "Slug"}
	vistos := map[reflect.Type]bool{}
	var andar func(tp reflect.Type, caminho string)
	andar = func(tp reflect.Type, caminho string) {
		for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice || tp.Kind() == reflect.Array {
			tp = tp.Elem()
		}
		if tp.Kind() != reflect.Struct || vistos[tp] || tp == reflect.TypeOf(time.Time{}) {
			return
		}
		vistos[tp] = true
		if tp == reflect.TypeOf(uuid.UUID{}) {
			t.Errorf("%s é um uuid", caminho)
		}
		for i := range tp.NumField() {
			f := tp.Field(i)
			for _, p := range proibidos {
				if strings.Contains(f.Name, p) {
					t.Errorf("%s.%s: nome proibido na projeção pública", caminho, f.Name)
				}
			}
			if f.Type == reflect.TypeOf(uuid.UUID{}) {
				t.Errorf("%s.%s é um uuid", caminho, f.Name)
			}
			andar(f.Type, caminho+"."+f.Name)
		}
	}
	andar(reflect.TypeOf(statuspage.View{}), "View")
}
