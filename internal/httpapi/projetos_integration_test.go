package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// projetoJSON é um item de GET /api/v1/projects.
type projetoJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// U é owner do Projeto padrão e viewer de B (de outro titular): a lista traz
// os dois, o padrão primeiro, cada um com o papel de U. O projeto de V, de que
// U não é membro, não aparece.
func TestListProjectsReturnsOwnProjectsWithRole(t *testing.T) {
	a := newAPI(t)
	u := tokenDe(t, a, a.userID)
	padrao := testdb.ProjetoPadrao(t, a.userID)
	b := testdb.NovoProjeto(t, outroUsuario(t, a), "B")
	testdb.Membro(t, b, a.userID, "viewer")
	v := outroUsuario(t, a)
	deV := testdb.NovoProjeto(t, v, "Projeto-de-V")
	padraoDeV := testdb.ProjetoPadrao(t, v)

	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/projects", u, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/projects deu %d, queria 200: %s", status, corpo)
	}
	envelope := decodificar(t, corpo)
	if chaves := chavesDe(envelope); !slices.Equal(chaves, []string{"items"}) {
		t.Errorf("o envelope tem as chaves %v, queria só items (o de GET /api/v1/channels)", chaves)
	}
	crus, _ := envelope["items"].([]any)
	for i, item := range crus {
		m, _ := item.(map[string]any)
		if chaves := chavesDe(m); !slices.Equal(chaves, []string{"created_at", "id", "is_default", "name", "role"}) {
			t.Errorf("o item %d tem as chaves %v", i, chaves)
		}
	}

	var lista struct {
		Items []projetoJSON `json:"items"`
	}
	if err := json.Unmarshal([]byte(corpo), &lista); err != nil {
		t.Fatal(err)
	}
	queria := []projetoJSON{
		{ID: padrao.String(), Name: "Projeto padrão", IsDefault: true, Role: "owner"},
		{ID: b.String(), Name: "B", IsDefault: false, Role: "viewer"},
	}
	if len(lista.Items) != len(queria) {
		t.Fatalf("a lista trouxe %d projetos, queria %d: %s", len(lista.Items), len(queria), corpo)
	}
	for i, q := range queria {
		got := lista.Items[i]
		if got.CreatedAt.IsZero() {
			t.Errorf("o item %d veio sem created_at: %s", i, corpo)
		}
		got.CreatedAt = time.Time{}
		if got != q {
			t.Errorf("item %d = %+v, queria %+v", i, got, q)
		}
	}
	conferirSemDadosDoDono(t, "GET /api/v1/projects", corpo, []string{deV.String(), padraoDeV.String(), "Projeto-de-V"})
}

// recursosDoDono são os ids que preenchem os curingas das gêmeas, todos de
// recursos de verdade no Projeto padrão do dono: o caminho chega até o
// serviço, e não para num id que não existe.
type recursosDoDono struct {
	projeto                                           string
	alvo, incidente, hb, incidenteDoHB, canal, pagina string
}

// dados são o que a resposta a outro usuário não pode trazer.
func (r recursosDoDono) dados() []string {
	return []string{r.alvo, r.incidente, r.hb, r.incidenteDoHB, r.canal, r.pagina,
		"webhook …6789", "pagina-do-dono", "Titulo-do-dono", "Vitrine-do-dono"}
}

// caminho preenche o molde de uma gêmea: {projectID} com projeto, {id} com o
// recurso do primeiro segmento depois dele, e a consulta que a rota exige.
// Curinga desconhecido falha alto: rota nova precisa ensinar este teste.
func (r recursosDoDono) caminho(t *testing.T, rota, projeto string) string {
	t.Helper()
	_, molde, _ := strings.Cut(rota, " ")
	resto := strings.TrimPrefix(molde, prefixoDaGemea)
	recurso, _, _ := strings.Cut(resto, "/")
	ids := map[string]string{
		"targets": r.alvo, "incidents": r.incidente, "heartbeats": r.hb,
		"channels": r.canal, "status-pages": r.pagina,
	}
	trocas := map[string]string{"{projectID}": projeto, "{id}": ids[recurso], "{incident_id}": r.incidenteDoHB}
	caminho := curinga.ReplaceAllStringFunc(molde, func(c string) string {
		valor := trocas[c]
		if valor == "" {
			t.Fatalf("%s: não sei preencher %s; ensine recursosDoDono.caminho", rota, c)
		}
		return valor
	})
	return caminho + consultaDaGemea[rota]
}

// consultaDaGemea é a query string das gêmeas que recusam a chamada sem ela.
var consultaDaGemea = map[string]string{
	"GET /api/v1/projects/{projectID}/heartbeats/cron-preview": "?expr=0+3+*+*+*&timezone=UTC",
}

// corpoDaGemea é o corpo válido mínimo de cada gêmea de escrita (nil: a rota
// não lê corpo). Com o corpo válido, a recusa vem do papel, e não da
// validação. conferirTabelaCobreAsRotas exige uma linha por gêmea de escrita.
var corpoDaGemea = map[string]any{
	"POST /api/v1/projects/{projectID}/targets":                                     alvoValido,
	"PATCH /api/v1/projects/{projectID}/targets/{id}":                               map[string]any{"name": "alterado"},
	"DELETE /api/v1/projects/{projectID}/targets/{id}":                              nil,
	"POST /api/v1/projects/{projectID}/incidents/{id}/ack":                          nil,
	"POST /api/v1/projects/{projectID}/heartbeats":                                  corpoDePeriodo,
	"PATCH /api/v1/projects/{projectID}/heartbeats/{id}":                            map[string]any{"name": "alterado"},
	"DELETE /api/v1/projects/{projectID}/heartbeats/{id}":                           nil,
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/token/rotate":                nil,
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/clone":                       nil,
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/ping-now":                    nil,
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/events/clear":                nil,
	"POST /api/v1/projects/{projectID}/heartbeats/{id}/incidents/{incident_id}/ack": nil,
	"POST /api/v1/projects/{projectID}/channels":                                    map[string]any{"kind": "discord", "url": webhookDeTeste},
	"PATCH /api/v1/projects/{projectID}/channels/{id}":                              map[string]any{"enabled": false},
	"DELETE /api/v1/projects/{projectID}/channels/{id}":                             nil,
	"POST /api/v1/projects/{projectID}/channels/{id}/verify/resend":                 nil,
	"POST /api/v1/projects/{projectID}/status-pages":                                map[string]any{"slug": "pagina-nova", "title": "Nova"},
	"PATCH /api/v1/projects/{projectID}/status-pages/{id}":                          map[string]any{"title": "alterado"},
}

// rotasGemeas separa, das rotas registradas, as gêmeas de leitura e as de
// escrita.
func rotasGemeas(t *testing.T) (leituras, escritas []string) {
	t.Helper()
	for _, rota := range rotasRegistradas(t) {
		metodo, caminho, _ := strings.Cut(rota, " ")
		if !strings.HasPrefix(caminho, prefixoDaGemea) {
			continue
		}
		if metodo == http.MethodGet {
			leituras = append(leituras, rota)
		} else {
			escritas = append(escritas, rota)
		}
	}
	if len(leituras) == 0 || len(escritas) == 0 {
		t.Fatalf("achei %d gêmeas de leitura e %d de escrita; a enumeração deixou de enxergá-las", len(leituras), len(escritas))
	}
	return leituras, escritas
}

// prepararDono cria, pelas rotas antigas, um recurso de cada tipo no Projeto
// padrão do dono: alvo com incidente aberto, heartbeat com incidente, canal
// e status page.
func prepararDono(t *testing.T, a *api, dono string) recursosDoDono {
	t.Helper()
	r := recursosDoDono{projeto: testdb.ProjetoPadrao(t, a.userID).String()}
	alvo, incidentes := incidentesDe(t, a, dono, 1)
	r.alvo, r.incidente = alvo, incidentes[0]
	r.hb, _ = criarHB(t, a, dono, corpoDePeriodo)
	incidenteDoHB := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		return q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
			ID: incidenteDoHB, HeartbeatID: uuid.MustParse(r.hb), StartedAt: a.clk.Now(), Cause: "heartbeat_late",
		})
	}); err != nil {
		t.Fatal(err)
	}
	r.incidenteDoHB = incidenteDoHB.String()
	r.canal = criarCanalDiscord(t, a, dono)
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/status-pages", dono, map[string]any{
		"slug": "pagina-do-dono", "title": "Titulo-do-dono", "published": true,
		"items": []map[string]string{{"target_id": alvo, "display_name": "Vitrine-do-dono"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("criar a status page do dono deu %d: %s", status, corpo)
	}
	r.pagina, _ = decodificar(t, corpo)["id"].(string)
	return r
}

// lerTudoDoDono devolve o corpo de cada gêmea de leitura, lida pelo dono no
// projeto dele. Todas dão 200: é o que o outro usuário não pode mudar.
func lerTudoDoDono(t *testing.T, a *api, dono string, r recursosDoDono, leituras []string) map[string]string {
	t.Helper()
	corpos := map[string]string{}
	for _, rota := range leituras {
		esperarVagaNoLimite(a)
		caminho := r.caminho(t, rota, r.projeto)
		status, corpo := lerCru(t, a, http.MethodGet, caminho, dono, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s do dono deu %d, queria 200: %s", caminho, status, corpo)
		}
		corpos[caminho] = corpo
	}
	return corpos
}

func conferirLeiturasDoDonoIguais(t *testing.T, antes, depois map[string]string) {
	t.Helper()
	for caminho, corpo := range antes {
		if depois[caminho] != corpo {
			t.Errorf("GET %s do dono mudou:\nantes  %s\ndepois %s", caminho, corpo, depois[caminho])
		}
	}
}

// Spec §5.5 levada ao projeto (Review Focus 5): em TODA gêmea registrada, o
// projeto de outro usuário dá o mesmo 404 de um projeto que não existe, sem
// dado nenhum do dono, e nada do dono muda. Um 403, ou um 404 com outro corpo,
// diria ao intruso que o projeto existe. A varredura vem de rotasRegistradas:
// uma gêmea nova entra sozinha (e falha em corpoDaGemea se for de escrita).
func TestAnotherUsersProjectIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	r := prepararDono(t, a, dono)
	intruso := tokenDe(t, a, outroUsuario(t, a))
	leituras, escritas := rotasGemeas(t)
	conferirTabelaCobreAsRotas(t, escritas, corpoDaGemea)

	antes := lerTudoDoDono(t, a, dono, r, leituras)
	for _, rota := range ordemComDeleteNoFim(append(slices.Clone(leituras), escritas...)) {
		metodo, _, _ := strings.Cut(rota, " ")
		corpo := corpoDaGemea[rota]
		esperarVagaNoLimite(a)
		stAlheio, crAlheio := lerCru(t, a, metodo, r.caminho(t, rota, r.projeto), intruso, corpo)
		esperarVagaNoLimite(a)
		stInexistente, crInexistente := lerCru(t, a, metodo, r.caminho(t, rota, uuid.NewString()), intruso, corpo)
		if stAlheio != http.StatusNotFound || stInexistente != http.StatusNotFound {
			t.Errorf("%s: projeto alheio deu %d, inexistente %d; queria 404 nos dois:\n%s", rota, stAlheio, stInexistente, crAlheio)
			continue
		}
		if semTrace(t, crAlheio) != semTrace(t, crInexistente) {
			t.Errorf("%s: o 404 do projeto alheio difere do inexistente:\n%s\n%s", rota, crAlheio, crInexistente)
		}
		conferirSemDadosDoDono(t, rota, crAlheio, r.dados())
	}
	conferirLeiturasDoDonoIguais(t, antes, lerTudoDoDono(t, a, dono, r, leituras))
}

// O UUID nulo no {projectID} é um projeto que não existe: o mesmo 404 de um
// UUID qualquer, e não o 500 do storage.ErrNoProject que InProjectTx devolve
// quando o recebe.
func TestNilProjectIDIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	stNulo, crNulo := lerCru(t, a, http.MethodGet, "/api/v1/projects/"+uuid.Nil.String()+"/targets", dono, nil)
	stInexistente, crInexistente := lerCru(t, a, http.MethodGet, "/api/v1/projects/"+uuid.NewString()+"/targets", dono, nil)
	if stNulo != http.StatusNotFound || stInexistente != http.StatusNotFound {
		t.Fatalf("UUID nulo deu %d, inexistente %d; queria 404 nos dois:\n%s", stNulo, stInexistente, crNulo)
	}
	if semTrace(t, crNulo) != semTrace(t, crInexistente) {
		t.Errorf("o 404 do UUID nulo difere do inexistente:\n%s\n%s", crNulo, crInexistente)
	}
}

// O viewer do projeto do dono lê, mas não escreve: toda gêmea de escrita dá
// 403 forbidden, e nada do dono muda. As gêmeas de leitura de coleção dão
// 200. As de leitura por {id} dão 404 hoje: enquanto a política permissiva
// for user_id = app.user_id (portão do PR 2b), o viewer não enxerga as linhas
// que o dono criou, e o 404 é o mesmo de um id que não existe. Nunca 403: o
// viewer tem PermRead.
func TestViewerGetsForbiddenOnEveryMutatingProjectRoute(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	r := prepararDono(t, a, dono)
	idDoViewer := outroUsuario(t, a)
	testdb.Membro(t, uuid.MustParse(r.projeto), idDoViewer, "viewer")
	viewer := tokenDe(t, a, idDoViewer)
	leituras, escritas := rotasGemeas(t)
	conferirTabelaCobreAsRotas(t, escritas, corpoDaGemea)

	antes := lerTudoDoDono(t, a, dono, r, leituras)
	for _, rota := range leituras {
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, http.MethodGet, r.caminho(t, rota, r.projeto), viewer, nil)
		quer := http.StatusOK
		if strings.Contains(rota, "{id}") {
			// TODO(PR 2b): 200 quando as permissivas forem por projeto
			quer = http.StatusNotFound
		}
		if status != quer {
			t.Errorf("%s do viewer deu %d, queria %d: %s", rota, status, quer, corpo)
		}
	}
	for _, rota := range ordemComDeleteNoFim(escritas) {
		metodo, _, _ := strings.Cut(rota, " ")
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, metodo, r.caminho(t, rota, r.projeto), viewer, corpoDaGemea[rota])
		if status != http.StatusForbidden {
			t.Errorf("%s do viewer deu %d, queria 403 forbidden: %s", rota, status, corpo)
			continue
		}
		if erro, _ := decodificar(t, corpo)["error"].(map[string]any); erro["code"] != "forbidden" {
			t.Errorf("%s do viewer deu 403 com o código %v, queria forbidden: %s", rota, erro["code"], corpo)
		}
	}
	conferirLeiturasDoDonoIguais(t, antes, lerTudoDoDono(t, a, dono, r, leituras))
}

// O reenvio do viewer não chega ao envio: dá 403 forbidden, o token pendente e
// o prazo dele ficam como estavam, e nenhum e-mail novo sai. Um reenvio que
// mandasse o link antes de conferir a permissão gastaria a cota de e-mail do
// dono e invalidaria o link que ele já tem.
func TestViewerResendLeavesPendingTokenUntouched(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	noProjeto := "/api/v1/projects/" + projeto.String() + "/channels"
	status, corpo := lerCru(t, a, http.MethodPost, noProjeto, dono,
		map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	if status != http.StatusCreated {
		t.Fatalf("POST %s do dono deu %d, queria 201: %s", noProjeto, status, corpo)
	}
	canal, _ := decodificar(t, corpo)["id"].(string)
	if n := linksEnviados(a); n != 1 {
		t.Fatalf("a criação mandou %d links, queria 1", n)
	}
	pendente := func() (hash []byte, vence time.Time) {
		t.Helper()
		if err := testdb.Admin(t).QueryRow(context.Background(),
			`SELECT verify_token_hash, verify_expires_at FROM notif_channels WHERE id = $1`, canal).Scan(&hash, &vence); err != nil {
			t.Fatal(err)
		}
		return hash, vence
	}
	hashAntes, venceAntes := pendente()

	idDoViewer := outroUsuario(t, a)
	testdb.Membro(t, projeto, idDoViewer, "viewer")
	status, corpo = lerCru(t, a, http.MethodPost, noProjeto+"/"+canal+"/verify/resend", tokenDe(t, a, idDoViewer), nil)
	if erro, _ := decodificar(t, corpo)["error"].(map[string]any); status != http.StatusForbidden || erro["code"] != "forbidden" {
		t.Fatalf("reenvio do viewer deu %d, queria 403 forbidden: %s", status, corpo)
	}
	hashDepois, venceDepois := pendente()
	if !slices.Equal(hashAntes, hashDepois) || !venceAntes.Equal(venceDepois) {
		t.Errorf("o reenvio do viewer mexeu no token pendente: hash %x → %x, prazo %v → %v", hashAntes, hashDepois, venceAntes, venceDepois)
	}
	if n := linksEnviados(a); n != 1 {
		t.Errorf("depois do reenvio do viewer, %d links enviados; queria 1", n)
	}
}

func linksEnviados(a *api) int {
	a.email.mu.Lock()
	defer a.email.mu.Unlock()
	return len(a.email.links)
}
