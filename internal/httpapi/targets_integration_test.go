package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

func tokenDe(t *testing.T, a *api, userID uuid.UUID) string {
	t.Helper()
	// Uma credencial autorizada precisa da família que o servidor consulta;
	// os casos de família ausente assinam o token explicitamente no teste.
	_, sessao, err := gerente(a).Issue(context.Background(), userID, carimboDe(t, a, userID), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.NewAccessToken(testJWTSecret, userID, sessao.FamilyID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func outroUsuario(t *testing.T, a *api) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// chamar faz uma requisição com os headers que a cadeia exige: o anti-CSRF
// em mutação, o token quando houver.
func chamar(t *testing.T, a *api, metodo, caminho, token string, corpo any) *http.Response {
	t.Helper()
	var r io.Reader
	if corpo != nil {
		r = jsonBody(t, corpo)
	}
	req, err := http.NewRequest(metodo, a.srv.URL+caminho, r)
	if err != nil {
		t.Fatal(err)
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if metodo != http.MethodGet {
		req.Header.Set("X-Requested-With", "stillon")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type alvoJSON struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	URL                 string     `json:"url"`
	IntervalSec         int32      `json:"interval_sec"`
	DegradedThresholdMS *int32     `json:"degraded_threshold_ms"`
	HeaderNames         []string   `json:"header_names"`
	ContentRule         *regraJSON `json:"content_rule"`
	CreatedAt           time.Time  `json:"created_at"`
}

// regraJSON é content_rule na saída; JSONPointer nil é o null fora de
// json_equals.
type regraJSON struct {
	Mode        string  `json:"mode"`
	Value       string  `json:"value"`
	JSONPointer *string `json:"json_pointer"`
}

// lerCorpo devolve o corpo cru (para procurar o que NÃO pode estar nele) e
// o decodifica em dst.
func lerCorpo(t *testing.T, resp *http.Response, dst any) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if dst != nil {
		if err := json.Unmarshal(b, dst); err != nil {
			t.Fatalf("corpo não é o JSON esperado: %v\n%s", err, b)
		}
	}
	return b
}

var alvoValido = map[string]any{
	"name":    "API",
	"url":     "https://api.exemplo.com/health",
	"headers": map[string]string{"Authorization": "Bearer segredo-do-cliente"},
}

func TestTargetCRUDFlow(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, lerCorpo(t, resp, nil))
	}
	var criado alvoJSON
	cru := lerCorpo(t, resp, &criado)
	if strings.Contains(string(cru), "segredo-do-cliente") {
		t.Fatal("a resposta do POST contém o valor do header (D5)")
	}
	if len(criado.HeaderNames) != 1 || criado.HeaderNames[0] != "Authorization" {
		t.Errorf("header_names = %v", criado.HeaderNames)
	}

	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil)
	cru = lerCorpo(t, resp, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(cru), "segredo-do-cliente") {
		t.Fatalf("GET deu %d ou vazou o header: %s", resp.StatusCode, cru)
	}

	resp = chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, map[string]any{"name": "Novo"})
	var editado alvoJSON
	lerCorpo(t, resp, &editado)
	if resp.StatusCode != http.StatusOK || editado.Name != "Novo" {
		t.Fatalf("PATCH deu %d, nome %q", resp.StatusCode, editado.Name)
	}

	resp = chamar(t, a, http.MethodDelete, "/api/v1/targets/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE deu %d", resp.StatusCode)
	}
	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET depois do DELETE deu %d, queria 404", resp.StatusCode)
	}
}

// Spec §5.5: recurso de outro usuário é 404, nunca 403, em toda rota.
func TestAnotherUsersTargetIs404OnEveryRoute(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	dono := tokenDe(t, a, a.userID)
	intruso := tokenDe(t, a, outroUsuario(t, a))
	admin, err := pgxpool.New(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)

	const valorDeA = "CANARY-regra-de-a-7f3c"
	alvoDeA := maps.Clone(alvoValido)
	alvoDeA["content_rule"] = map[string]any{"mode": "contains", "value": valorDeA}
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", dono, alvoDeA)
	var criado alvoJSON
	if cru := lerCorpo(t, resp, &criado); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST do dono deu %d: %s", resp.StatusCode, cru)
	}
	id := uuid.MustParse(criado.ID)
	gravarValidadeTLS(t, a, criado.ID)

	// Histerese no meio de uma sequência de falhas, como o sondador deixaria:
	// um PATCH que troca a regra a zeraria (C4) e adiantaria o next_run_at
	// (D13). O intruso não pode provocar nenhum dos dois.
	desde := a.clk.Now().Add(-3 * time.Minute)
	if _, err := admin.Exec(ctx, `
		INSERT INTO target_health (target_id, consecutive_failures, consecutive_successes, streak_since, last_checked_at)
		VALUES ($1, 2, 0, $2, $2)`, id, desde); err != nil {
		t.Fatal(err)
	}
	proximo := a.clk.Now().Add(42 * time.Minute)
	if _, err := admin.Exec(ctx, `UPDATE targets SET next_run_at = $2 WHERE id = $1`, id, proximo); err != nil {
		t.Fatal(err)
	}
	// desde é ponteiro: a histerese zerada tem streak_since NULL, e o teste
	// precisa relatar a diferença em vez de parar no Scan.
	type efeitos struct {
		falhas, sucessos int32
		desde            *time.Time
		proximo          time.Time
	}
	lerEfeitos := func() efeitos {
		t.Helper()
		var e efeitos
		if err := admin.QueryRow(ctx, `
			SELECT h.consecutive_failures, h.consecutive_successes, h.streak_since, t.next_run_at
			  FROM targets t JOIN target_health h ON h.target_id = t.id
			 WHERE t.id = $1`, id).Scan(&e.falhas, &e.sucessos, &e.desde, &e.proximo); err != nil {
			t.Fatal(err)
		}
		return e
	}
	antes := lerEfeitos()
	if antes.desde == nil || !antes.proximo.Equal(proximo) {
		t.Fatalf("a preparação não gravou a histerese e o next_run_at: %v", antes)
	}

	for _, c := range []struct {
		metodo, caminho string
		corpo           any
	}{
		{http.MethodGet, "/api/v1/targets/" + criado.ID, nil},
		{http.MethodPatch, "/api/v1/targets/" + criado.ID, map[string]any{"name": "roubado"}},
		{http.MethodPatch, "/api/v1/targets/" + criado.ID, map[string]any{"tls_warn": false}},
		{http.MethodPatch, "/api/v1/targets/" + criado.ID, map[string]any{
			"content_rule": map[string]any{"mode": "json_equals", "value": "roubado", "json_pointer": "/status"},
		}},
		{http.MethodPatch, "/api/v1/targets/" + criado.ID, map[string]any{"content_rule": nil}},
		{http.MethodDelete, "/api/v1/targets/" + criado.ID, nil},
		{http.MethodGet, "/api/v1/targets/" + criado.ID + "/checks", nil},
	} {
		status, cru := lerCru(t, a, c.metodo, c.caminho, intruso, c.corpo)
		var env envelopeComCampos
		if err := json.Unmarshal([]byte(cru), &env); err != nil {
			t.Fatalf("%s %s: %v\n%s", c.metodo, c.caminho, err, cru)
		}
		if status != http.StatusNotFound || env.Error.Code != "not_found" {
			t.Errorf("%s %s %v pelo intruso deu %d/%s, queria 404/not_found", c.metodo, c.caminho, c.corpo, status, env.Error.Code)
		}
		conferirSemDadosDoDono(t, c.metodo+" "+c.caminho, cru,
			[]string{"API", "api.exemplo.com", "segredo-do-cliente", valorDeA, notAfterTLS, "not_after"})
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/targets", intruso, nil)
	conferirSemDadosDoDono(t, "GET /api/v1/targets", lista, []string{criado.ID, valorDeA, notAfterTLS, "not_after"})

	_, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, dono, nil)
	var intacto struct {
		Name        string     `json:"name"`
		TLSWarn     bool       `json:"tls_warn"`
		ContentRule *regraJSON `json:"content_rule"`
	}
	if err := json.Unmarshal([]byte(cru), &intacto); err != nil {
		t.Fatal(err)
	}
	if intacto.Name != "API" || !intacto.TLSWarn {
		t.Errorf("o alvo do dono mudou: %s", cru)
	}
	quer := regraJSON{Mode: "contains", Value: valorDeA}
	if intacto.ContentRule == nil || *intacto.ContentRule != quer {
		t.Errorf("a regra do dono mudou para %+v, queria %+v", intacto.ContentRule, quer)
	}
	if depois := lerEfeitos(); depois.falhas != 2 || depois.sucessos != 0 || depois.desde == nil ||
		!depois.desde.Equal(desde) || !depois.proximo.Equal(proximo) {
		t.Errorf("o PATCH do intruso mexeu na sondagem de A: falhas=%d sucessos=%d streak_since=%v next_run_at=%s; queria 2, 0, %s e %s",
			depois.falhas, depois.sucessos, depois.desde, depois.proximo.UTC().Format(time.RFC3339),
			desde.UTC().Format(time.RFC3339), proximo.UTC().Format(time.RFC3339))
	}
}

// notAfterTLS e observedAtTLS são a validade que gravarValidadeTLS grava, como
// a API as escreve.
const (
	notAfterTLS   = "2026-12-01T00:00:00Z"
	observedAtTLS = "2026-09-22T11:30:00Z"
)

// gravarValidadeTLS grava a linha de target_tls do alvo como o sondador grava:
// no escopo dele, o único que a escreve em produção.
func gravarValidadeTLS(t *testing.T, a *api, alvoID string) {
	t.Helper()
	ctx := context.Background()
	p := db.SaveTargetTLSParams{
		TargetID:   uuid.MustParse(alvoID),
		NotAfter:   time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		ObservedAt: time.Date(2026, 9, 22, 11, 30, 0, 0, time.UTC),
	}
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.SaveTargetTLS(ctx, p) }); err != nil {
		t.Fatal(err)
	}
}

// Spec §7.2: a validade gravada pelo sondador sai no alvo, no GET e na lista;
// sem leitura, "tls" é null, e não ausente.
func TestTargetShowsTheCertificateReading(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var lido, semLeitura alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &lido)
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &semLeitura)
	gravarValidadeTLS(t, a, lido.ID)

	queria := `"tls":{"not_after":"` + notAfterTLS + `","observed_at":"` + observedAtTLS + `"}`
	_, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+lido.ID, tok, nil)
	if !strings.Contains(cru, queria) {
		t.Errorf("GET: queria %s em %s", queria, cru)
	}
	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets/"+semLeitura.ID, tok, nil)
	if !strings.Contains(cru, `"tls":null`) {
		t.Errorf("GET sem leitura: queria \"tls\":null em %s", cru)
	}

	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/targets", tok, nil)
	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(lista), &envelope); err != nil {
		t.Fatal(err)
	}
	porID := map[string]string{}
	for _, item := range envelope.Items {
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &id); err != nil {
			t.Fatal(err)
		}
		porID[id.ID] = string(item)
	}
	if !strings.Contains(porID[lido.ID], queria) {
		t.Errorf("lista: queria %s em %s", queria, porID[lido.ID])
	}
	if !strings.Contains(porID[semLeitura.ID], `"tls":null`) {
		t.Errorf("lista sem leitura: queria \"tls\":null em %s", porID[semLeitura.ID])
	}
}

// tls_warn é ligado por padrão (spec §5.2), muda pelo PATCH e nunca vira null.
// tls é só de saída: o DTO estrito o recusa na entrada como campo desconhecido.
func TestTLSWarnDefaultsAndPatch(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, outroUsuario(t, a))
	tlsWarn := func(onde, cru string) bool {
		t.Helper()
		var v struct {
			TLSWarn *bool `json:"tls_warn"`
		}
		if err := json.Unmarshal([]byte(cru), &v); err != nil || v.TLSWarn == nil {
			t.Fatalf("%s: sem tls_warn (%v): %s", onde, err, cru)
		}
		return *v.TLSWarn
	}
	criar := func(corpo map[string]any) (int, string, string) {
		t.Helper()
		status, cru := lerCru(t, a, http.MethodPost, "/api/v1/targets", tok, corpo)
		var id struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(cru), &id) // o corpo de erro não tem id
		return status, cru, id.ID
	}

	status, cru, padrao := criar(map[string]any{"name": "padrao", "url": "https://exemplo.com/"})
	if status != http.StatusCreated || !tlsWarn("POST sem tls_warn", cru) {
		t.Errorf("POST sem tls_warn: %d %s; queria 201 com true", status, cru)
	}
	status, cru, desligado := criar(map[string]any{"name": "desligado", "url": "https://exemplo.com/", "tls_warn": false})
	if status != http.StatusCreated || tlsWarn("POST com false", cru) {
		t.Errorf("POST com false: %d %s; queria 201 com false", status, cru)
	}
	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets/"+desligado, tok, nil)
	if tlsWarn("GET depois do POST com false", cru) {
		t.Errorf("tls_warn false não foi gravado: %s", cru)
	}

	status, cru = lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+desligado, tok, map[string]any{"tls_warn": true})
	if status != http.StatusOK || !tlsWarn("PATCH true", cru) {
		t.Errorf("PATCH true: %d %s", status, cru)
	}
	status, cru = lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+padrao, tok, map[string]any{"name": "outro nome"})
	if status != http.StatusOK || !tlsWarn("PATCH sem tls_warn", cru) {
		t.Errorf("PATCH sem tls_warn mexeu no interruptor: %d %s", status, cru)
	}
	status, cru = lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+padrao, tok, map[string]any{"tls_warn": false})
	if status != http.StatusOK || tlsWarn("PATCH false", cru) {
		t.Errorf("PATCH false: %d %s", status, cru)
	}

	var env envelopeComCampos
	status, cru = lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+desligado, tok, map[string]any{"tls_warn": nil})
	if err := json.Unmarshal([]byte(cru), &env); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "tls_warn" {
		t.Errorf("PATCH tls_warn null: %d %s; queria 400 em tls_warn", status, cru)
	}
	for _, corpo := range []map[string]any{{"tls_warn": "sim"}, {"tls_warn": 1}} {
		if status, cru := lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+desligado, tok, corpo); status != http.StatusBadRequest {
			t.Errorf("PATCH %v: %d %s; queria 400", corpo, status, cru)
		}
	}
	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets/"+desligado, tok, nil)
	if !tlsWarn("GET depois dos PATCH recusados", cru) {
		t.Errorf("um PATCH recusado mudou tls_warn: %s", cru)
	}

	if status, cru, _ := criar(map[string]any{"name": "sim", "url": "https://exemplo.com/", "tls_warn": "sim"}); status != http.StatusBadRequest {
		t.Errorf("POST tls_warn \"sim\": %d %s; queria 400", status, cru)
	}
	tls := map[string]any{"not_after": notAfterTLS, "observed_at": observedAtTLS}
	if status, cru, _ := criar(map[string]any{"name": "forjado", "url": "https://exemplo.com/", "tls": tls}); status != http.StatusBadRequest {
		t.Errorf("POST com tls: %d %s; queria 400", status, cru)
	}
	if status, cru := lerCru(t, a, http.MethodPatch, "/api/v1/targets/"+padrao, tok, map[string]any{"tls": tls}); status != http.StatusBadRequest {
		t.Errorf("PATCH com tls: %d %s; queria 400", status, cru)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/targets", tok, nil)
	if strings.Contains(lista, "forjado") || strings.Contains(lista, `"name":"sim"`) || strings.Contains(lista, notAfterTLS) {
		t.Errorf("um POST recusado criou alvo ou gravou validade: %s", lista)
	}
}

// Todas as seis rotas, e não só a lista: uma rota nova montada com
// mux.Handle direto, sem protegida(), ficaria aberta sem teste nenhum ver.
func TestTargetRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	id := uuid.NewString()
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/targets"},
		{http.MethodPost, "/api/v1/targets"},
		{http.MethodGet, "/api/v1/targets/" + id},
		{http.MethodPatch, "/api/v1/targets/" + id},
		{http.MethodDelete, "/api/v1/targets/" + id},
		{http.MethodGet, "/api/v1/targets/" + id + "/checks"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// Dívida do Plano 2: as bordas das consultas via HTTP. Cada valor
// malformado aponta o campo e o motivo certos.
func TestTargetQueriesRejectMalformedValues(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)
	checks := "/api/v1/targets/" + criado.ID + "/checks?"
	agora := a.clk.Now().UTC().Format(time.RFC3339)

	for caminho, quer := range map[string]apierr.FieldError{
		"/api/v1/targets?limit=0":                 {Field: "limit", Reason: "out_of_range"},
		"/api/v1/targets?limit=abc":               {Field: "limit", Reason: "invalid"},
		"/api/v1/targets?cursor=nao-e-uuid":       {Field: "cursor", Reason: "invalid"},
		checks + "from=ontem":                     {Field: "from", Reason: "invalid"},
		checks + "to=2026-13-01T00:00:00Z":        {Field: "to", Reason: "invalid"},
		checks + "from=" + agora + "&to=" + agora: {Field: "from", Reason: "invalid"}, // janela vazia
	} {
		var env envelopeComCampos
		resp := chamar(t, a, http.MethodGet, caminho, tok, nil)
		lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0] != quer {
			t.Errorf("%s deu %d/%+v, queria 400 %+v", caminho, resp.StatusCode, env.Error.Fields, quer)
		}
	}
}

// A janela é [from, to): o check em from entra, o check em to não.
func TestChecksWindowIsHalfOpen(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)
	fim := a.clk.Now().UTC().Truncate(time.Minute)
	for _, em := range []time.Time{fim.Add(-2 * time.Minute), fim.Add(-time.Minute), fim} {
		p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: uuid.MustParse(criado.ID), CheckedAt: em, Status: "up", LatencyMS: 5}
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	var got struct {
		Items []struct {
			CheckedAt time.Time `json:"checked_at"`
		} `json:"items"`
	}
	caminho := fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
		fim.Add(-2*time.Minute).Format(time.RFC3339), fim.Format(time.RFC3339))
	lerCorpo(t, chamar(t, a, http.MethodGet, caminho, tok, nil), &got)
	if len(got.Items) != 2 || !got.Items[0].CheckedAt.Equal(fim.Add(-2*time.Minute)) || !got.Items[1].CheckedAt.Equal(fim.Add(-time.Minute)) {
		t.Errorf("janela [from, to) devolveu %+v", got.Items)
	}
}

func TestValidationErrorNamesTheFieldsWithoutEchoingInput(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tokenDe(t, a, a.userID), map[string]any{
		"name": "x", "url": "http://127.0.0.1:6379/", "interval_sec": 10,
	})
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"fields"`
		} `json:"error"`
	}
	cru := lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" {
		t.Fatalf("deu %d/%s", resp.StatusCode, env.Error.Code)
	}
	quer := map[string]string{"url": "forbidden", "interval_sec": "out_of_range"}
	for _, f := range env.Error.Fields {
		if quer[f.Field] == f.Reason {
			delete(quer, f.Field)
		}
	}
	if len(quer) != 0 {
		t.Errorf("faltaram %v em %s", quer, cru)
	}
	if strings.Contains(string(cru), "6379") {
		t.Errorf("a resposta ecoou a entrada: %s", cru)
	}
}

// degraded_threshold_ms: ausente → 2000, null → desativado, número → valor.
func TestDegradedThresholdAbsentNullAndValue(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	criar := func(extra map[string]any) alvoJSON {
		t.Helper()
		corpo := map[string]any{"name": "x", "url": "https://exemplo.com"}
		for k, v := range extra {
			corpo[k] = v
		}
		resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, corpo)
		var got alvoJSON
		lerCorpo(t, resp, &got)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST deu %d", resp.StatusCode)
		}
		return got
	}

	if got := criar(nil); got.DegradedThresholdMS == nil || *got.DegradedThresholdMS != 2000 {
		t.Errorf("ausente deu %v, queria 2000", got.DegradedThresholdMS)
	}
	if got := criar(map[string]any{"degraded_threshold_ms": nil}); got.DegradedThresholdMS != nil {
		t.Errorf("null deu %v, queria null", *got.DegradedThresholdMS)
	}
	comValor := criar(map[string]any{"degraded_threshold_ms": 500})
	if comValor.DegradedThresholdMS == nil || *comValor.DegradedThresholdMS != 500 {
		t.Errorf("500 deu %v", comValor.DegradedThresholdMS)
	}

	// PATCH com null desativa; PATCH sem o campo não mexe.
	//
	// O status vem ANTES de olhar o limiar: um envelope de erro decodificado
	// em alvoJSON deixa DegradedThresholdMS nil sem reclamar, e "null
	// desativa" passaria com o PATCH falhando.
	resp := chamar(t, a, http.MethodPatch, "/api/v1/targets/"+comValor.ID, tok, map[string]any{"name": "y"})
	var semMexer alvoJSON
	cru := lerCorpo(t, resp, &semMexer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH sem o campo deu %d: %s", resp.StatusCode, cru)
	}
	if semMexer.DegradedThresholdMS == nil || *semMexer.DegradedThresholdMS != 500 {
		t.Errorf("PATCH sem o campo mexeu no limiar: %v", semMexer.DegradedThresholdMS)
	}
	resp = chamar(t, a, http.MethodPatch, "/api/v1/targets/"+comValor.ID, tok, map[string]any{"degraded_threshold_ms": nil})
	var desativado alvoJSON
	cru = lerCorpo(t, resp, &desativado)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH null deu %d: %s", resp.StatusCode, cru)
	}
	if desativado.DegradedThresholdMS != nil {
		t.Errorf("PATCH null deu %v, queria null", *desativado.DegradedThresholdMS)
	}
}

// PATCH de headers: {...} substitui o conjunto inteiro, null limpa. Nas duas
// respostas só podem aparecer nomes, nunca valores (D5).
func TestPatchHeadersReplacesAndClears(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	valores := []string{"segredo-do-cliente", "outro-segredo", "valor-do-tenant"}

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	var criado alvoJSON
	lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d", resp.StatusCode)
	}

	patch := func(corpo map[string]any) (alvoJSON, string) {
		t.Helper()
		resp := chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, corpo)
		var got alvoJSON
		cru := string(lerCorpo(t, resp, &got))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %v deu %d: %s", corpo, resp.StatusCode, cru)
		}
		for _, v := range valores {
			if strings.Contains(cru, v) {
				t.Errorf("a resposta do PATCH contém o valor de header %q (D5): %s", v, cru)
			}
		}
		return got, cru
	}

	substituido, cru := patch(map[string]any{"headers": map[string]string{
		"X-Api-Key": "outro-segredo", "X-Tenant": "valor-do-tenant",
	}})
	if !slices.Equal(substituido.HeaderNames, []string{"X-Api-Key", "X-Tenant"}) {
		t.Errorf("headers {...} deu header_names %v, queria exatamente [X-Api-Key X-Tenant]: %s", substituido.HeaderNames, cru)
	}

	limpo, cru := patch(map[string]any{"headers": nil})
	if len(limpo.HeaderNames) != 0 {
		t.Errorf("headers null deu header_names %v, queria vazio: %s", limpo.HeaderNames, cru)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tokenDe(t, a, a.userID), map[string]any{
		"name": "x", "url": "https://exemplo.com", "intervalsec": 30,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("campo desconhecido deu %d, queria 400", resp.StatusCode)
	}
}

func TestMalformedIDIs404(t *testing.T) {
	a := newAPI(t)
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets/nao-e-uuid", tokenDe(t, a, a.userID), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("id malformado deu %d, queria 404 (D16)", resp.StatusCode)
	}
}

func TestListPaginatesWithNextCursor(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for range 3 {
		chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido).Body.Close()
	}

	type paginaJSON struct {
		Items      []alvoJSON `json:"items"`
		NextCursor string     `json:"next_cursor"`
	}
	var pagina paginaJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets?limit=2", tok, nil), &pagina)
	if len(pagina.Items) != 2 || pagina.NextCursor == "" {
		t.Fatalf("primeira página: %d itens, cursor %q", len(pagina.Items), pagina.NextCursor)
	}
	// Variável nova para a segunda página: next_cursor é omitempty, e
	// decodificar por cima da primeira deixaria o cursor velho no lugar.
	var segunda paginaJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets?limit=2&cursor="+pagina.NextCursor, tok, nil), &segunda)
	if len(segunda.Items) != 1 || segunda.NextCursor != "" {
		t.Errorf("segunda página: %d itens, cursor %q; queria 1 e nenhum", len(segunda.Items), segunda.NextCursor)
	}

	resp := chamar(t, a, http.MethodGet, "/api/v1/targets?limit=101", tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("limit=101 deu %d, queria 400", resp.StatusCode)
	}
}

func TestChecksEndpoint(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	var criado alvoJSON
	lerCorpo(t, resp, &criado)
	alvoID := uuid.MustParse(criado.ID)

	kind := "timeout"
	for i, st := range []string{"up", "down"} {
		p := db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvoID,
			CheckedAt: a.clk.Now().Add(time.Duration(i-2) * time.Minute), Status: st, LatencyMS: 40,
		}
		if st == "down" {
			p.ErrorKind = &kind
		}
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	var got struct {
		Resolution string `json:"resolution"`
		Items      []struct {
			Status    string  `json:"status"`
			ErrorKind *string `json:"error_kind"`
		} `json:"items"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID+"/checks", tok, nil), &got)
	if got.Resolution != "raw" || len(got.Items) != 2 || got.Items[0].Status != "up" || got.Items[1].ErrorKind == nil || *got.Items[1].ErrorKind != "timeout" {
		t.Errorf("checks = %q %+v", got.Resolution, got.Items)
	}

	// D6: acima de 48h a série é horária, lida de check_rollup_1h.
	fim := a.clk.Now().UTC().Truncate(time.Hour)
	velho := db.InsertCheckParams{
		ID: uuid.Must(uuid.NewV7()), TargetID: alvoID, CheckedAt: fim.Add(-30*time.Hour + time.Minute),
		Status: "down", LatencyMS: 10000, ErrorKind: &kind,
	}
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, velho) }); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ConsolidateRollups(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	var horaria struct {
		Resolution string `json:"resolution"`
		Items      []struct {
			Bucket time.Time `json:"bucket"`
			Total  int32     `json:"total"`
			OK     *int32    `json:"ok"`
			P50    *int32    `json:"p50_ms"`
		} `json:"items"`
	}
	caminho := fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
		fim.Add(-49*time.Hour).Format(time.RFC3339), fim.Format(time.RFC3339))
	cru := lerCorpo(t, chamar(t, a, http.MethodGet, caminho, tok, nil), &horaria)
	// Duas horas: a do check velho e a dos dois checks recentes (11:00 UTC).
	if horaria.Resolution != "1h" || len(horaria.Items) != 2 || !horaria.Items[0].Bucket.Equal(fim.Add(-30*time.Hour)) ||
		horaria.Items[0].Bucket.Location() != time.UTC ||
		horaria.Items[0].Total != 1 || horaria.Items[0].OK == nil || *horaria.Items[0].OK != 0 || horaria.Items[0].P50 != nil {
		t.Errorf("janela de 49h = %s, queria 1h com a hora sem sucesso (ok 0, p50_ms null)", cru)
	}

	caminho = fmt.Sprintf("/api/v1/targets/%s/checks?from=%s&to=%s", criado.ID,
		fim.Add(-401*24*time.Hour).Format(time.RFC3339), fim.Format(time.RFC3339))
	resp = chamar(t, a, http.MethodGet, caminho, tok, nil)
	var env struct {
		Error struct {
			Fields []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"fields"`
		} `json:"error"`
	}
	cru = lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
		env.Error.Fields[0].Field != "from" || env.Error.Fields[0].Reason != "out_of_range" {
		t.Errorf("janela de 401 dias deu %d %s, queria 400 from/out_of_range", resp.StatusCode, cru)
	}
}

// Spec §5.7: um usuário esgota o SEU balde; outro usuário, no mesmo IP,
// continua passando. O limite por usuário (burst 20) é mais apertado que o
// por IP (burst 30) justamente para que este teste distinga os dois.
func TestPerUserRateLimit(t *testing.T) {
	a := newAPI(t)
	tokA := tokenDe(t, a, a.userID)
	tokB := tokenDe(t, a, outroUsuario(t, a))

	// Dispara até o 429, em vez de contar exatamente 21: o balde reabastece
	// 1 token por segundo, e sob -race num CI lento a 21ª poderia passar. O
	// teto de 25 fica abaixo do burst do IP (30), então quem barrou A foi o
	// limite por usuário.
	barrada := 0
	for i := 1; i <= 25; i++ {
		resp := chamar(t, a, http.MethodGet, "/api/v1/targets", tokA, nil)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			barrada = i
			break
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("requisição %d de A deu %d", i, resp.StatusCode)
		}
	}
	if barrada == 0 {
		t.Fatal("A fez 25 requisições sem receber 429: não há limite por usuário")
	}
	if barrada <= 20 {
		t.Errorf("A foi barrada na requisição %d, antes do burst de 20", barrada)
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/targets", tokB, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("B, no mesmo IP, deu %d depois de A esgotar o balde: o limite não é por usuário", resp.StatusCode)
	}
}

// O 101º alvo é 409 target_limit_reached (D17 do Plano 5, reconciliado no
// contrato); o painel mapeia o código no formulário.
func TestTargetLimitIs409(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $2::int) i`, a.userID, targets.MaxTargetsPerUser); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || decodeEnvelope(t, resp).Error.Code != "target_limit_reached" {
		t.Errorf("status %d, queria 409 target_limit_reached", resp.StatusCode)
	}
	// O 409 não basta: a recusa não pode ter gravado o 101º. A contagem roda
	// no escopo do dono, como a inserção acima: targets tem RLS.
	var n int64
	err = storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		var err error
		n, err = q.CountTargets(ctx, a.userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != targets.MaxTargetsPerUser {
		t.Errorf("%d alvos depois da recusa, queria %d", n, targets.MaxTargetsPerUser)
	}
}

// B6 (Plano 10): a janela do gráfico sem relógio do navegador. Com
// ?window=, o servidor calcula from = to - janela, e to é o agora do
// servidor (ou o to pedido). Só as janelas do painel valem; window junto com
// from é ambíguo e recusado.
func TestChecksWindowIsComputedByTheServer(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)
	agora := a.clk.Now().UTC()
	for _, em := range []time.Time{agora.Add(-90 * time.Minute), agora.Add(-30 * time.Minute)} {
		p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: uuid.MustParse(criado.ID), CheckedAt: em, Status: "up", LatencyMS: 5}
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	type serie struct {
		Resolution string `json:"resolution"`
		Items      []struct {
			CheckedAt time.Time `json:"checked_at"`
		} `json:"items"`
	}
	pedir := func(consulta string) (int, serie, string) {
		t.Helper()
		resp := chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID+"/checks?"+consulta, tok, nil)
		var s serie
		cru := string(lerCorpo(t, resp, &s))
		return resp.StatusCode, s, cru
	}

	if st, s, cru := pedir("window=1h"); st != http.StatusOK || s.Resolution != "raw" || len(s.Items) != 1 ||
		!s.Items[0].CheckedAt.Equal(agora.Add(-30*time.Minute)) {
		t.Errorf("window=1h deu %d %s, queria só o check de 30 min atrás", st, cru)
	}
	if st, s, cru := pedir("window=6h"); st != http.StatusOK || len(s.Items) != 2 {
		t.Errorf("window=6h deu %d %s, queria os dois checks", st, cru)
	}
	// A base é o to: com to uma hora atrás, a janela de 1h pega o de 90 min.
	to := agora.Add(-time.Hour).Format(time.RFC3339)
	if st, s, cru := pedir("window=1h&to=" + to); st != http.StatusOK || len(s.Items) != 1 ||
		!s.Items[0].CheckedAt.Equal(agora.Add(-90*time.Minute)) {
		t.Errorf("window=1h&to=%s deu %d %s, queria só o check de 90 min atrás", to, st, cru)
	}
	// Acima de 48h a resolução é horária, como com from.
	if st, s, cru := pedir("window=7d"); st != http.StatusOK || s.Resolution != "1h" {
		t.Errorf("window=7d deu %d %s, queria resolution 1h", st, cru)
	}
	for _, j := range []string{"1h", "6h", "24h", "7d", "30d", "90d"} {
		if st, _, cru := pedir("window=" + j); st != http.StatusOK {
			t.Errorf("window=%s deu %d %s, queria 200", j, st, cru)
		}
	}

	for _, consulta := range []string{"window=2h", "window=1d", "window=", "window=1h&from=" + to} {
		resp := chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID+"/checks?"+consulta, tok, nil)
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Fields []struct {
					Field  string `json:"field"`
					Reason string `json:"reason"`
				} `json:"fields"`
			} `json:"error"`
		}
		cru := lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
			env.Error.Fields[0].Field != "window" || env.Error.Fields[0].Reason != "invalid" {
			t.Errorf("%s deu %d %s, queria 400 window/invalid", consulta, resp.StatusCode, cru)
		}
	}
}

// B1 (Plano 10), pelo contrato: PATCH com url de outra origem e sem headers
// responde header_names vazio.
func TestPatchToAnotherOriginDropsTheHeaders(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)

	resp := chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, map[string]any{"url": "https://atacante.example/"})
	var got alvoJSON
	cru := lerCorpo(t, resp, &got)
	if resp.StatusCode != http.StatusOK || got.HeaderNames == nil || len(got.HeaderNames) != 0 {
		t.Errorf("PATCH para outra origem deu %d %s, queria 200 com header_names []", resp.StatusCode, cru)
	}
}

// C2 (Plano 10): 1xx e 3xx em expected_status são 400 no POST e no PATCH.
func TestExpectedStatusOutsideTheReachableRangesIs400(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	var criado alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &criado)

	type campos struct {
		Error struct {
			Fields []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"fields"`
		} `json:"error"`
	}
	for _, status := range []int{101, 301, 302, 308} {
		corpo := map[string]any{"name": "API", "url": "https://api.exemplo.com/", "expected_status": status}
		for _, r := range []struct{ metodo, caminho string }{
			{http.MethodPost, "/api/v1/targets"},
			{http.MethodPatch, "/api/v1/targets/" + criado.ID},
		} {
			resp := chamar(t, a, r.metodo, r.caminho, tok, corpo)
			var env campos
			cru := lerCorpo(t, resp, &env)
			if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
				env.Error.Fields[0].Field != "expected_status" || env.Error.Fields[0].Reason != "out_of_range" {
				t.Errorf("%s com expected_status %d deu %d %s, queria 400 expected_status/out_of_range", r.metodo, status, resp.StatusCode, cru)
			}
		}
	}
}

// content_rule: POST com regra a devolve; PATCH com outra a troca; PATCH sem o
// campo não mexe; PATCH com null limpa.
func TestContentRuleCreatePatchAndClear(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, map[string]any{
		"name": "x", "url": "https://exemplo.com/status",
		"content_rule": map[string]any{"mode": "json_equals", "value": "ok", "json_pointer": "/status"},
	})
	var criado alvoJSON
	if cru := lerCorpo(t, resp, &criado); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	if r := criado.ContentRule; r == nil || r.Mode != "json_equals" || r.Value != "ok" ||
		r.JSONPointer == nil || *r.JSONPointer != "/status" {
		t.Fatalf("POST devolveu a regra %+v, queria json_equals ok /status", r)
	}

	patch := func(corpo map[string]any) alvoJSON {
		t.Helper()
		resp := chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, corpo)
		var got alvoJSON
		if cru := lerCorpo(t, resp, &got); resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %v deu %d: %s", corpo, resp.StatusCode, cru)
		}
		return got
	}

	trocado := patch(map[string]any{"content_rule": map[string]any{"mode": "not_contains", "value": "erro"}})
	if r := trocado.ContentRule; r == nil || r.Mode != "not_contains" || r.Value != "erro" || r.JSONPointer != nil {
		t.Errorf("PATCH com outra regra devolveu %+v, queria not_contains erro sem ponteiro", r)
	}

	mantido := patch(map[string]any{"name": "y"})
	if r := mantido.ContentRule; r == nil || r.Mode != "not_contains" || r.Value != "erro" {
		t.Errorf("PATCH sem o campo mexeu na regra: %+v", r)
	}

	limpo := patch(map[string]any{"content_rule": nil})
	if limpo.ContentRule != nil {
		t.Errorf("PATCH com null devolveu a regra %+v, queria null", *limpo.ContentRule)
	}
	resp = chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil)
	cru := string(lerCorpo(t, resp, nil))
	if !strings.Contains(cru, `"content_rule":null`) {
		t.Errorf("GET depois do null não traz content_rule null: %s", cru)
	}
}

// Cada recusa aponta o campo da regra e o motivo, e o envelope não ecoa o
// valor enviado.
func TestContentRuleValidationNamesTheField(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	const canario = "CANARY-9d2e41"

	criar := func(extra map[string]any) (int, envelopeComCampos, string) {
		t.Helper()
		corpo := map[string]any{"name": "x", "url": "https://exemplo.com/"}
		for k, v := range extra {
			corpo[k] = v
		}
		resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, corpo)
		cru := lerCorpo(t, resp, nil)
		var env envelopeComCampos
		if resp.StatusCode != http.StatusCreated {
			if err := json.Unmarshal(cru, &env); err != nil {
				t.Fatalf("envelope inválido: %v %s", err, cru)
			}
		}
		return resp.StatusCode, env, string(cru)
	}
	regra := func(valor string) map[string]any {
		return map[string]any{"content_rule": map[string]any{"mode": "contains", "value": valor}}
	}

	for _, c := range []struct {
		nome  string
		extra map[string]any
		quer  apierr.FieldError
	}{
		{"valor vazio", regra(""), apierr.FieldError{Field: "content_rule.value", Reason: "required"}},
		{"HEAD com regra", map[string]any{
			"method": "HEAD", "content_rule": map[string]any{"mode": "contains", "value": canario},
		}, apierr.FieldError{Field: "content_rule", Reason: "forbidden"}},
		{"valor com NUL", regra(canario + "\u0000"), apierr.FieldError{Field: "content_rule.value", Reason: "invalid"}},
		{"257 caracteres", regra(canario + strings.Repeat("é", 257-len(canario))), apierr.FieldError{Field: "content_rule.value", Reason: "too_long"}},
	} {
		status, env, cru := criar(c.extra)
		if status != http.StatusBadRequest || env.Error.Code != "validation_failed" ||
			len(env.Error.Fields) != 1 || env.Error.Fields[0] != c.quer {
			t.Errorf("%s deu %d/%+v, queria 400 %+v: %s", c.nome, status, env.Error.Fields, c.quer, cru)
		}
		if strings.Contains(cru, canario) {
			t.Errorf("%s: a resposta ecoou o valor enviado: %s", c.nome, cru)
		}
	}

	// 256 caracteres de dois bytes: o limite conta caracteres, não bytes.
	if status, _, cru := criar(regra(strings.Repeat("é", 256))); status != http.StatusCreated {
		t.Errorf("256 caracteres deu %d, queria 201: %s", status, cru)
	}
}

// O decoder estrito alcança dentro da regra: um campo a mais (um "regex" que
// o servidor não implementa) é 400, e não uma regra diferente da pedida.
func TestContentRuleRejectsUnknownNestedField(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	extra := map[string]any{"mode": "contains", "value": "a", "regex": true}

	resp := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, map[string]any{
		"name": "x", "url": "https://exemplo.com/", "content_rule": extra,
	})
	var env envelopeComCampos
	if cru := lerCorpo(t, resp, &env); resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" {
		t.Errorf("POST com campo desconhecido na regra deu %d/%s, queria 400: %s", resp.StatusCode, env.Error.Code, cru)
	}
	var lista struct {
		Items []alvoJSON `json:"items"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets", tok, nil), &lista)
	if len(lista.Items) != 0 {
		t.Fatalf("o POST recusado criou %d alvo(s)", len(lista.Items))
	}

	// No PATCH também, e a regra gravada fica como estava.
	resp = chamar(t, a, http.MethodPost, "/api/v1/targets", tok, map[string]any{
		"name": "x", "url": "https://exemplo.com/",
		"content_rule": map[string]any{"mode": "contains", "value": "b"},
	})
	var criado alvoJSON
	if cru := lerCorpo(t, resp, &criado); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	resp = chamar(t, a, http.MethodPatch, "/api/v1/targets/"+criado.ID, tok, map[string]any{"content_rule": extra})
	if cru := lerCorpo(t, resp, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH com campo desconhecido na regra deu %d, queria 400: %s", resp.StatusCode, cru)
	}
	var lido alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, tok, nil), &lido)
	if r := lido.ContentRule; r == nil || r.Mode != "contains" || r.Value != "b" {
		t.Errorf("o PATCH recusado mexeu na regra: %+v", r)
	}
}
