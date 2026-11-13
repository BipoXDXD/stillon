package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// Sem cadastro não há código a errar: o /totp/confirm de uma conta sem enroll
// é 400 validation, e a reserva da prova da família volta. Repetido além do
// limite de provas, nunca vira 401 nem derruba a sessão. Sabotagem: contar o
// ErrTOTPNotEnrolled como prova errada (provas.errou).
func TestTOTPConfirmSemCadastroNaoContaComoErro(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	for i := range auth.FamilyProofLimit + 1 {
		a.clk.Advance(umaFichaPorConta) // o balde por conta não entra na conta
		resp := chamar(t, a, http.MethodPost, "/api/v1/auth/totp/confirm", tok, map[string]string{"code": "123456"})
		status, corpo := resp.StatusCode, string(lerCorpo(t, resp, nil))
		if cookies := resp.Header.Values("Set-Cookie"); len(cookies) != 0 {
			t.Errorf("pedido %d: a recusa mexeu nos cookies: %v", i+1, cookies)
		}
		if status != http.StatusBadRequest {
			t.Fatalf("pedido %d: status %d, queria 400: %s", i+1, status, corpo)
		}
		if codigo := codigoDoEnvelope(t, corpo); codigo != string(apierr.CodeValidation) {
			t.Fatalf("pedido %d: code %q, queria %s", i+1, codigo, apierr.CodeValidation)
		}
		if strings.Contains(corpo, "recovery_codes") || strings.Contains(corpo, "TOTP") {
			t.Errorf("pedido %d: o corpo trouxe mais que o envelope: %s", i+1, corpo)
		}
	}
	if status := statusEmMe(t, a, tok); status != http.StatusOK {
		t.Errorf("/me deu %d depois dos confirms sem cadastro; a família caiu", status)
	}
}

// limiarDe lê degraded_threshold_ms do alvo; nil é null.
func limiarDe(t *testing.T, a *api, tok, id string) *int32 {
	t.Helper()
	var got alvoJSON
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets/"+id, tok, nil)
	cru := lerCorpo(t, resp, &got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET do alvo deu %d: %s", resp.StatusCode, cru)
	}
	return got.DegradedThresholdMS
}

func conferirLimiar(t *testing.T, got *int32, quer int32) {
	t.Helper()
	if got == nil || *got != quer {
		t.Errorf("limiar = %v, queria %d", got, quer)
	}
}

// PATCH com degraded_threshold_ms com valor grava o valor; ausente não mexe;
// null limpa. Nas bordas, 1 e 60000 entram, e 0, negativo e 60001 são 400
// out_of_range sem alterar o banco. O PATCH de outro usuário é 404 sem dado do
// alvo. Sabotagem: o Update não copiar o valor para o Patch.
func TestPatchAlvoDegradedThresholdComValor(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var criado alvoJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok,
		map[string]any{"name": "limiar", "url": "https://limiar.exemplo.com"})
	if cru := lerCorpo(t, resp, &criado); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	caminho := "/api/v1/targets/" + criado.ID

	patch := func(t *testing.T, corpo map[string]any) (int, string) {
		t.Helper()
		return lerCru(t, a, http.MethodPatch, caminho, tok, corpo)
	}

	if status, corpo := patch(t, map[string]any{"degraded_threshold_ms": 800}); status != http.StatusOK {
		t.Fatalf("PATCH 800 deu %d: %s", status, corpo)
	}
	conferirLimiar(t, limiarDe(t, a, tok, criado.ID), 800)

	if status, corpo := patch(t, map[string]any{"name": "outro nome"}); status != http.StatusOK {
		t.Fatalf("PATCH sem o campo deu %d: %s", status, corpo)
	}
	conferirLimiar(t, limiarDe(t, a, tok, criado.ID), 800)

	for _, borda := range []int32{1, 60000} {
		if status, corpo := patch(t, map[string]any{"degraded_threshold_ms": borda}); status != http.StatusOK {
			t.Fatalf("PATCH %d deu %d: %s", borda, status, corpo)
		}
		conferirLimiar(t, limiarDe(t, a, tok, criado.ID), borda)
	}

	for _, fora := range []int64{0, -1, 60001} {
		t.Run(strconv.FormatInt(fora, 10), func(t *testing.T) {
			status, corpo := patch(t, map[string]any{"degraded_threshold_ms": fora})
			conferirRecusa(t, status, corpo,
				[]apierr.FieldError{{Field: "degraded_threshold_ms", Reason: targets.ReasonOutOfRange}})
			conferirLimiar(t, limiarDe(t, a, tok, criado.ID), 60000)
		})
	}

	intruso := tokenDe(t, a, outroUsuario(t, a))
	status, corpo := lerCru(t, a, http.MethodPatch, caminho, intruso, map[string]any{"degraded_threshold_ms": 900})
	if status != http.StatusNotFound || codigoDoEnvelope(t, corpo) != string(apierr.CodeNotFound) {
		t.Errorf("PATCH alheio deu %d: %s, queria 404", status, corpo)
	}
	for _, alheio := range []string{"limiar.exemplo.com", "outro nome", "60000"} {
		if strings.Contains(corpo, alheio) {
			t.Errorf("a recusa do PATCH alheio trouxe %q: %s", alheio, corpo)
		}
	}
	conferirLimiar(t, limiarDe(t, a, tok, criado.ID), 60000)

	if status, corpo := patch(t, map[string]any{"degraded_threshold_ms": nil}); status != http.StatusOK {
		t.Fatalf("PATCH null deu %d: %s", status, corpo)
	}
	if got := limiarDe(t, a, tok, criado.ID); got != nil {
		t.Errorf("PATCH null deixou o limiar em %d", *got)
	}
}

type paginaDoHeartbeat struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func lerPaginaDoHeartbeat(t *testing.T, a *api, tok, caminho string) paginaDoHeartbeat {
	t.Helper()
	status, corpo := lerCru(t, a, http.MethodGet, caminho, tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s deu %d: %s", caminho, status, corpo)
	}
	var p paginaDoHeartbeat
	if err := json.Unmarshal([]byte(corpo), &p); err != nil {
		t.Fatalf("%v: %s", err, corpo)
	}
	return p
}

// conferirDuasPaginas pede limit=2 sobre 3 itens (ids em ordem crescente): a
// primeira página traz os dois mais novos e o cursor no último deles; a
// segunda, o restante e nenhum cursor. Nada se repete nem falta.
func conferirDuasPaginas(t *testing.T, a *api, tok, rota string, ids []string) {
	t.Helper()
	p1 := lerPaginaDoHeartbeat(t, a, tok, rota+"?limit=2")
	if len(p1.Items) != 2 || p1.Items[0].ID != ids[2] || p1.Items[1].ID != ids[1] {
		t.Fatalf("primeira página: %+v, queria %s e %s", p1.Items, ids[2], ids[1])
	}
	if p1.NextCursor == nil {
		t.Fatalf("a primeira página veio sem cursor, queria %s", ids[1])
	}
	if *p1.NextCursor != ids[1] {
		t.Fatalf("cursor da primeira página = %s, queria %s", *p1.NextCursor, ids[1])
	}
	p2 := lerPaginaDoHeartbeat(t, a, tok, rota+"?limit=2&cursor="+*p1.NextCursor)
	if len(p2.Items) != 1 || p2.Items[0].ID != ids[0] {
		t.Errorf("segunda página: %+v, queria só %s", p2.Items, ids[0])
	}
	if p2.NextCursor != nil {
		t.Errorf("a última página trouxe cursor %q", *p2.NextCursor)
	}
}

// Eventos e incidentes de heartbeat paginam por cursor: limit+1 itens dão uma
// página cheia com next_cursor no último id e uma segunda com o resto, sem
// cursor. O outro usuário recebe 404 nas duas rotas, sem nenhum id do dono.
// Sabotagens: as closures do proximoCursor devolverem outro id.
func TestEventosEIncidentesDeHeartbeatPaginam(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	hb, _ := criarHB(t, a, tok, corpoDePeriodo)
	hbID := uuid.MustParse(hb)

	var eventos, incidentes []string
	for i := range 3 {
		ev, inc := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		em := a.clk.Now().Add(time.Duration(i-3) * time.Minute)
		err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
			if err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: ev, HeartbeatID: hbID, At: em, Kind: "success", Source: "ping",
			}); err != nil {
				return err
			}
			if err := q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
				ID: inc, HeartbeatID: hbID, StartedAt: em, Cause: "heartbeat_late",
			}); err != nil {
				return err
			}
			if i < 2 { // só pode haver um aberto por heartbeat
				_, err := q.ResolveHeartbeatIncident(ctx, db.ResolveHeartbeatIncidentParams{ID: inc, ResolvedAt: em.Add(time.Second)})
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		eventos = append(eventos, ev.String())
		incidentes = append(incidentes, inc.String())
	}

	t.Run("eventos", func(t *testing.T) {
		conferirDuasPaginas(t, a, tok, "/api/v1/heartbeats/"+hb+"/events", eventos)
	})
	t.Run("incidentes", func(t *testing.T) {
		conferirDuasPaginas(t, a, tok, "/api/v1/heartbeats/"+hb+"/incidents", incidentes)
	})

	intruso := tokenDe(t, a, outroUsuario(t, a))
	for _, rota := range []string{"/events", "/incidents"} {
		status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+hb+rota+"?limit=2", intruso, nil)
		if status != http.StatusNotFound || codigoDoEnvelope(t, corpo) != string(apierr.CodeNotFound) {
			t.Errorf("%s alheio deu %d: %s, queria 404", rota, status, corpo)
		}
		for _, id := range slices.Concat(eventos, incidentes) {
			if strings.Contains(corpo, id) {
				t.Errorf("%s alheio trouxe o id %s: %s", rota, id, corpo)
			}
		}
	}
}

// O item do resumo com incidente aberto e checks na última hora fechada sai
// com open_incident_id e spark preenchidos, no conjunto exato de chaves. O
// resumo do outro usuário não traz o alvo. Sabotagens: não copiar o
// OpenIncidentID; não montar o spark.
func TestResumoComIncidenteAbertoESpark(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	agora := a.clk.Now()
	for _, mes := range []time.Time{agora.Add(-time.Hour), agora} {
		if err := storage.EnsureChecksPartitions(ctx, a.pool, mes); err != nil {
			t.Fatal(err)
		}
	}
	var criado alvoJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	if cru := lerCorpo(t, resp, &criado); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	alvo := uuid.MustParse(criado.ID)

	horaFechada := agora.UTC().Truncate(time.Hour).Add(-time.Hour)
	tempo := "timeout"
	incidente := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		for i, st := range []string{"up", "up", "down"} {
			p := db.InsertCheckParams{
				ID: uuid.Must(uuid.NewV7()), TargetID: alvo,
				CheckedAt: horaFechada.Add(time.Duration(10*(i+1)) * time.Minute), Status: st, LatencyMS: 40,
			}
			if st == "down" {
				p.ErrorKind = &tempo
			}
			if err := q.InsertCheck(ctx, p); err != nil {
				return err
			}
		}
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: incidente, TargetID: alvo, StartedAt: agora, Cause: "timeout"})
	})
	if err != nil {
		t.Fatal(err)
	}

	var corpo struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/summary", tok, nil)
	cru := lerCorpo(t, resp, &corpo)
	if resp.StatusCode != http.StatusOK || len(corpo.Items) != 1 {
		t.Fatalf("resumo deu %d com %d itens: %s", resp.StatusCode, len(corpo.Items), cru)
	}
	item := corpo.Items[0]
	chaves := []string{"enabled", "id", "last_checked_at", "last_latency_ms", "name", "open_incident_id",
		"spark", "state", "tags", "uptime_24h"}
	if got := slices.Sorted(func(yield func(string) bool) {
		for k := range item {
			if !yield(k) {
				return
			}
		}
	}); !slices.Equal(got, chaves) {
		t.Errorf("chaves do item = %v, queria %v", got, chaves)
	}
	if string(item["open_incident_id"]) != `"`+incidente.String()+`"` {
		t.Errorf("open_incident_id = %s, queria %s", item["open_incident_id"], incidente)
	}

	var spark []map[string]json.RawMessage
	if err := json.Unmarshal(item["spark"], &spark); err != nil {
		t.Fatalf("spark: %v: %s", err, item["spark"])
	}
	if len(spark) != 1 {
		t.Fatalf("spark com %d horas, queria 1: %s", len(spark), item["spark"])
	}
	hora := spark[0]
	if got := slices.Sorted(func(yield func(string) bool) {
		for k := range hora {
			if !yield(k) {
				return
			}
		}
	}); !slices.Equal(got, []string{"bucket", "ok", "p50_ms", "total"}) {
		t.Errorf("chaves da hora do spark = %v", got)
	}
	var bucket time.Time
	if err := json.Unmarshal(hora["bucket"], &bucket); err != nil || !bucket.Equal(horaFechada) {
		t.Errorf("bucket = %s, queria %s", hora["bucket"], horaFechada.Format(time.RFC3339))
	}
	if string(hora["total"]) != "3" || string(hora["ok"]) != "2" || string(hora["p50_ms"]) != "40" {
		t.Errorf("hora do spark = total %s, ok %s, p50 %s; queria 3, 2 e 40", hora["total"], hora["ok"], hora["p50_ms"])
	}
	if strings.Contains(string(cru), "exemplo.com") {
		t.Errorf("o resumo vazou a URL: %s", cru)
	}

	intruso := tokenDe(t, a, outroUsuario(t, a))
	status, alheio := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary", intruso, nil)
	if status != http.StatusOK || strings.Contains(alheio, criado.ID) || strings.Contains(alheio, incidente.String()) {
		t.Errorf("resumo do intruso deu %d com dado do dono: %s", status, alheio)
	}
}
