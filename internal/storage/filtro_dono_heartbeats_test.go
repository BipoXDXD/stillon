package storage_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para heartbeats, seus eventos, incidentes e
// tags: superusuário, sem RLS, e só o project_id do WHERE separa A de B. A e B
// são dois projetos do MESMO usuário: o filtro por dono os deixaria passar.

// heartbeatDe lê o heartbeat no projeto dado, sem RLS.
func heartbeatDe(t *testing.T, q *db.Queries, projeto, hb uuid.UUID) db.Heartbeat {
	t.Helper()
	h, err := q.GetHeartbeat(context.Background(), db.GetHeartbeatParams{ProjectID: projeto, ID: hb})
	if err != nil {
		t.Fatalf("A lendo o próprio heartbeat: %v", err)
	}
	return h
}

func TestGetHeartbeatFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)

	_, err := q.GetHeartbeat(ctx, db.GetHeartbeatParams{ProjectID: pb, ID: hbA})
	exigirNenhumaLinha(t, err, "GetHeartbeat")
	if h := heartbeatDe(t, q, pa, hbA); h.ID != hbA || h.UserID != a {
		t.Errorf("A leu o heartbeat %s do dono %s, queria %s de %s", h.ID, h.UserID, hbA, a)
	}
}

func TestGetHeartbeatViewFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)

	_, err := q.GetHeartbeatView(ctx, db.GetHeartbeatViewParams{ProjectID: pb, ID: hbA})
	exigirNenhumaLinha(t, err, "GetHeartbeatView")
	v, err := q.GetHeartbeatView(ctx, db.GetHeartbeatViewParams{ProjectID: pa, ID: hbA})
	if err != nil {
		t.Fatalf("A lendo a vista do próprio heartbeat: %v", err)
	}
	if v.Heartbeat.ID != hbA {
		t.Errorf("A leu o heartbeat %s, queria %s", v.Heartbeat.ID, hbA)
	}
}

func TestGetHeartbeatForUpdateFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)

	_, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{ProjectID: pb, ID: hbA})
	exigirNenhumaLinha(t, err, "GetHeartbeatForUpdate")
	h, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{ProjectID: pa, ID: hbA})
	if err != nil {
		t.Fatalf("A travando o próprio heartbeat: %v", err)
	}
	if h.ID != hbA {
		t.Errorf("A leu o heartbeat %s, queria %s", h.ID, hbA)
	}
}

// atualizacaoDoHeartbeat copia o heartbeat com outro nome, para o UPDATE no
// projeto dado.
func atualizacaoDoHeartbeat(h db.Heartbeat, projeto uuid.UUID, nome string) db.UpdateHeartbeatParams {
	return db.UpdateHeartbeatParams{
		Name: nome, Description: h.Description, PeriodSec: h.PeriodSec, CronExpr: h.CronExpr,
		Timezone: h.Timezone, GraceSec: h.GraceSec, Enabled: h.Enabled, DeadlineAt: h.DeadlineAt,
		FailingSince: h.FailingSince, ResumedAt: h.ResumedAt, LastStartAt: h.LastStartAt,
		ProjectID: projeto, ID: h.ID,
	}
}

func TestUpdateHeartbeatFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)
	original := heartbeatDe(t, q, pa, hbA)

	if err := q.UpdateHeartbeat(ctx, atualizacaoDoHeartbeat(original, pb, "de B")); err != nil {
		t.Fatal(err)
	}
	if nome := heartbeatDe(t, q, pa, hbA).Name; nome != original.Name {
		t.Errorf("B renomeou o heartbeat de A sem RLS para %q, queria %q", nome, original.Name)
	}
	if err := q.UpdateHeartbeat(ctx, atualizacaoDoHeartbeat(original, pa, "novo")); err != nil {
		t.Fatal(err)
	}
	if nome := heartbeatDe(t, q, pa, hbA).Name; nome != "novo" {
		t.Errorf("A renomeou o próprio heartbeat e ficou %q, queria \"novo\"", nome)
	}
}

func TestSetHeartbeatPingHashFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)
	original := heartbeatDe(t, q, pa, hbA).PingTokenHash

	if err := q.SetHeartbeatPingHash(ctx, db.SetHeartbeatPingHashParams{ProjectID: pb, ID: hbA, PingTokenHash: hashDe("de-b")}); err != nil {
		t.Fatal(err)
	}
	if h := heartbeatDe(t, q, pa, hbA).PingTokenHash; !bytes.Equal(h, original) {
		t.Error("B trocou o token do heartbeat de A sem RLS")
	}
	if err := q.SetHeartbeatPingHash(ctx, db.SetHeartbeatPingHashParams{ProjectID: pa, ID: hbA, PingTokenHash: hashDe("novo")}); err != nil {
		t.Fatal(err)
	}
	if h := heartbeatDe(t, q, pa, hbA).PingTokenHash; !bytes.Equal(h, hashDe("novo")) {
		t.Error("A trocou o token do próprio heartbeat e ele não mudou")
	}
}

func TestDeleteHeartbeatFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)

	n, err := q.DeleteHeartbeat(ctx, db.DeleteHeartbeatParams{ProjectID: pb, ID: hbA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("B apagou %d heartbeats de A sem RLS, queria 0", n)
	}
	heartbeatDe(t, q, pa, hbA)
	n, err = q.DeleteHeartbeat(ctx, db.DeleteHeartbeatParams{ProjectID: pa, ID: hbA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("A apagou %d linhas do próprio heartbeat, queria 1", n)
	}
}

func TestListHeartbeatEventsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	novoEvento(t, pool, hbA)
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.HeartbeatEvent {
		t.Helper()
		ev, err := q.ListHeartbeatEvents(ctx, db.ListHeartbeatEventsParams{ProjectID: projeto, HeartbeatID: hbA, MaxRows: 10})
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}

	if ev := listar(pb); len(ev) != 0 {
		t.Errorf("B leu %d eventos do heartbeat de A sem RLS, queria 0", len(ev))
	}
	if ev := listar(pa); len(ev) != 1 || ev[0].HeartbeatID != hbA {
		t.Errorf("A leu %v, queria um evento do próprio heartbeat", ev)
	}
}

// incidentesDoHeartbeat lista os incidentes do heartbeat no projeto dado, sem
// RLS.
func incidentesDoHeartbeat(t *testing.T, q *db.Queries, projeto, hb uuid.UUID) []db.ListHeartbeatIncidentsRow {
	t.Helper()
	inc, err := q.ListHeartbeatIncidents(context.Background(), db.ListHeartbeatIncidentsParams{
		ProjectID: projeto, HeartbeatID: hb, MaxRows: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return inc
}

func TestListHeartbeatIncidentsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	incidente := novoIncidenteDeHeartbeat(t, pool, hbA)
	q := queriesSemRLS(t)

	if inc := incidentesDoHeartbeat(t, q, pb, hbA); len(inc) != 0 {
		t.Errorf("B leu %d incidentes do heartbeat de A sem RLS, queria 0", len(inc))
	}
	if inc := incidentesDoHeartbeat(t, q, pa, hbA); len(inc) != 1 || inc[0].ID != incidente {
		t.Errorf("A leu %v, queria só o incidente %s", inc, incidente)
	}
}

func TestAckHeartbeatIncidentFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	incidente := novoIncidenteDeHeartbeat(t, pool, hbA)
	q := queriesSemRLS(t)
	reconhecer := func(projeto uuid.UUID) (db.AckHeartbeatIncidentRow, error) {
		return q.AckHeartbeatIncident(ctx, db.AckHeartbeatIncidentParams{
			Now: agora, ProjectID: projeto, HeartbeatID: hbA, ID: incidente,
		})
	}

	_, err := reconhecer(pb)
	exigirNenhumaLinha(t, err, "AckHeartbeatIncident")
	if inc := incidentesDoHeartbeat(t, q, pa, hbA); len(inc) != 1 || inc[0].AckAt != nil {
		t.Errorf("depois do ack de B sem RLS, o incidente de A ficou %v; queria ack_at nulo", inc)
	}
	ack, err := reconhecer(pa)
	if err != nil {
		t.Fatalf("A reconhecendo o próprio incidente: %v", err)
	}
	if ack.ID != incidente || ack.AckAt == nil || !ack.AckAt.Equal(agora) {
		t.Errorf("A reconheceu e recebeu %+v, queria o incidente %s com ack em %s", ack, incidente, agora)
	}
}

// tagsDoHeartbeat lê as tags do heartbeat no projeto dado, sem RLS.
func tagsDoHeartbeat(t *testing.T, q *db.Queries, projeto, hb uuid.UUID) []string {
	t.Helper()
	tags, err := q.ListHeartbeatTags(context.Background(), db.ListHeartbeatTagsParams{ProjectID: projeto, HeartbeatID: hb})
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

func TestListHeartbeatTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	comTagDoHeartbeat(t, pool, a, hbA)
	q := queriesSemRLS(t)

	if tags := tagsDoHeartbeat(t, q, pb, hbA); len(tags) != 0 {
		t.Errorf("B leu as tags %v do heartbeat de A sem RLS", tags)
	}
	if tags := tagsDoHeartbeat(t, q, pa, hbA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("A leu %v, queria [prod]", tags)
	}
}

func TestDeleteHeartbeatTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	comTagDoHeartbeat(t, pool, a, hbA)
	q := queriesSemRLS(t)

	if err := q.DeleteHeartbeatTags(ctx, db.DeleteHeartbeatTagsParams{ProjectID: pb, HeartbeatID: hbA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoHeartbeat(t, q, pa, hbA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("depois do DELETE de B sem RLS, o heartbeat de A ficou com %v, queria [prod]", tags)
	}
	if err := q.DeleteHeartbeatTags(ctx, db.DeleteHeartbeatTagsParams{ProjectID: pa, HeartbeatID: hbA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoHeartbeat(t, q, pa, hbA); len(tags) != 0 {
		t.Errorf("A apagou as próprias tags e sobrou %v", tags)
	}
}

func TestInsertHeartbeatTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	comTagDoHeartbeat(t, pool, a, hbA)
	q := queriesSemRLS(t)

	if err := q.InsertHeartbeatTags(ctx, db.InsertHeartbeatTagsParams{Tags: []string{"intrusa"}, ProjectID: pb, HeartbeatID: hbA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoHeartbeat(t, q, pa, hbA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("B pôs tag no heartbeat de A sem RLS: %v, queria [prod]", tags)
	}
	if err := q.InsertHeartbeatTags(ctx, db.InsertHeartbeatTagsParams{Tags: []string{"api"}, ProjectID: pa, HeartbeatID: hbA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoHeartbeat(t, q, pa, hbA); !slices.Equal(tags, []string{"api", "prod"}) {
		t.Errorf("A pôs a tag api e ficou %v, queria [api prod]", tags)
	}
}

func TestListHeartbeatsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.ListHeartbeatsRow {
		t.Helper()
		hs, err := q.ListHeartbeats(ctx, db.ListHeartbeatsParams{ProjectID: projeto, MaxRows: 10})
		if err != nil {
			t.Fatal(err)
		}
		return hs
	}

	if deB := listar(pb); len(deB) != 0 {
		t.Errorf("B listou %d heartbeats sem RLS, queria 0 (o heartbeat de A vazou)", len(deB))
	}
	if deA := listar(pa); len(deA) != 1 || deA[0].Heartbeat.ID != hbA {
		t.Errorf("A listou %v, queria só o heartbeat %s", deA, hbA)
	}
}

func TestListProjectHeartbeatTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	hbA := novoHeartbeat(t, pool, a, "a")
	comTagDoHeartbeat(t, pool, a, hbA)
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.HeartbeatTag {
		t.Helper()
		tags, err := q.ListProjectHeartbeatTags(ctx, projeto)
		if err != nil {
			t.Fatal(err)
		}
		return tags
	}

	if deB := listar(pb); len(deB) != 0 {
		t.Errorf("B listou as tags %v dos heartbeats de A sem RLS", deB)
	}
	if deA := listar(pa); len(deA) != 1 || deA[0].HeartbeatID != hbA || deA[0].Tag != "prod" {
		t.Errorf("A listou %v, queria só a tag prod do heartbeat %s", deA, hbA)
	}
}
