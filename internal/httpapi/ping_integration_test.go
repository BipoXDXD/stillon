package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// novoHeartbeat cria direto no banco: a API de cadastro é o PR seguinte.
func novoHeartbeat(t *testing.T, a *api) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: a.userID, Name: "backup noturno", PingTokenHash: hash, PeriodSec: &periodo,
			GraceSec: 300, Enabled: true, CreatedAt: a.clk.Now(), DeadlineAt: a.clk.Now().Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

// pingar faz o que um curl faz: POST sem cabeçalho nenhum.
func pingar(t *testing.T, a *api, caminho string, corpo io.Reader) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+caminho, corpo)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

func contarEventos(t *testing.T, a *api, id uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	var n int
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		ev, err := q.ListHeartbeatEvents(ctx, db.ListHeartbeatEventsParams{UserID: a.userID, HeartbeatID: id, MaxRows: 100})
		n = len(ev)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPingSignalsReachTheHeartbeat(t *testing.T) {
	a := newAPI(t)
	id, token := novoHeartbeat(t, a)
	for i, sufixo := range []string{"/start", "", "/fail"} {
		if status, _, corpo := pingar(t, a, "/ping/"+token+sufixo, nil); status != http.StatusOK {
			t.Fatalf("POST %s: %d %s", sufixo, status, corpo)
		}
		if n := contarEventos(t, a, id); n != i+1 {
			t.Fatalf("depois de %q: %d eventos, queria %d", sufixo, n, i+1)
		}
	}
}

// A resposta de sucesso é fixa: nada do heartbeat sai por uma rota pública.
func TestPingSuccessBodyHasNoHeartbeatData(t *testing.T) {
	a := newAPI(t)
	_, token := novoHeartbeat(t, a)
	status, _, corpo := pingar(t, a, "/ping/"+token, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, corpo)
	}
	var campos map[string]any
	if err := json.Unmarshal([]byte(corpo), &campos); err != nil {
		t.Fatal(err)
	}
	if len(campos) != 1 || campos["status"] != "ok" {
		t.Errorf("corpo %s, queria só status ok", corpo)
	}
	if strings.Contains(corpo, "backup") {
		t.Errorf("o nome vazou: %s", corpo)
	}
}

// Inexistente, revogado e malformado respondem igual, e o revogado não grava.
// A revogação é a troca do hash que o token/rotate fará.
func TestPingUnknownAndRevokedTokensAnswerTheSame(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	id, revogado := novoHeartbeat(t, a)
	_, novoHash := heartbeat.NewPingToken()
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		return q.SetHeartbeatPingHash(ctx, db.SetHeartbeatPingHashParams{UserID: a.userID, ID: id, PingTokenHash: novoHash})
	})
	if err != nil {
		t.Fatal(err)
	}
	inexistente, _ := heartbeat.NewPingToken()

	stRev, _, corpoRev := pingar(t, a, "/ping/"+revogado, nil)
	if stRev != http.StatusNotFound {
		t.Fatalf("revogado deu %d: %s", stRev, corpoRev)
	}
	for nome, token := range map[string]string{"inexistente": inexistente, "malformado": "token-curto"} {
		st, _, corpo := pingar(t, a, "/ping/"+token, nil)
		if st != stRev || semTrace(t, corpo) != semTrace(t, corpoRev) {
			t.Errorf("%s deu %d %s; o revogado, %d %s", nome, st, corpo, stRev, corpoRev)
		}
	}
	if n := contarEventos(t, a, id); n != 0 {
		t.Errorf("token revogado gravou %d eventos", n)
	}
}

// O caminho é a credencial: nem o token do ping que deu certo, nem o de um
// ping que falhou por dentro, nem o de um panic aparecem no log.
func TestPingNeverLogsTheToken(t *testing.T) {
	log := capturarLog(t)
	a := newAPI(t)
	_, token := novoHeartbeat(t, a)
	desconhecido, _ := heartbeat.NewPingToken()

	pingar(t, a, "/ping/"+token, nil)
	pingar(t, a, "/ping/"+desconhecido, nil)

	// Falha interna: o banco fechado faz o Ping devolver erro.
	a.pool.Close()
	if status, _, corpo := pingar(t, a, "/ping/"+token, nil); status != http.StatusInternalServerError {
		t.Fatalf("com o banco fechado: %d %s", status, corpo)
	}
	if strings.Contains(log.String(), token) || strings.Contains(log.String(), desconhecido) {
		t.Fatalf("token no log: %s", log.String())
	}
	if !strings.Contains(log.String(), "registrar ping") {
		t.Fatalf("o controle falhou: a falha interna não chegou ao log capturado: %s", log.String())
	}
}

func TestPingPanicNeverLogsTheToken(t *testing.T) {
	log := capturarLog(t)
	// Sem serviço, o handler entra em panic, e o Recover é quem loga.
	a := newAPICom(t, nil, func(d *httpapi.Deps) { d.Pings = nil })
	token, _ := heartbeat.NewPingToken()
	if status, _, corpo := pingar(t, a, "/ping/"+token, nil); status != http.StatusInternalServerError {
		t.Fatalf("panic deu %d %s", status, corpo)
	}
	if !strings.Contains(log.String(), "panic no handler") {
		t.Fatalf("o controle falhou: o panic não chegou ao log: %s", log.String())
	}
	if strings.Contains(log.String(), token) {
		t.Fatalf("token no log: %s", log.String())
	}
}

// 100 tarefas no mesmo minuto, atrás de um endereço, com início, fim e falha:
// 300 sinais passam. O 429 aqui seria alarme falso. O que vem depois é barrado:
// o limite existe. O relógio está parado, então nenhum token volta.
func TestPingIPLimitAdmitsManyHeartbeatsBehindOneAddress(t *testing.T) {
	a := newAPI(t)
	var tokens []string
	for range 100 {
		_, token := novoHeartbeat(t, a)
		tokens = append(tokens, token)
	}
	for _, sufixo := range []string{"/start", "", "/fail"} {
		for _, token := range tokens {
			if status, _, corpo := pingar(t, a, "/ping/"+token+sufixo, nil); status != http.StatusOK {
				t.Fatalf("sinal %q barrado: %d %s", sufixo, status, corpo)
			}
		}
	}
	status, h, _ := pingar(t, a, "/ping/"+tokens[0], nil)
	if status != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		t.Errorf("o sinal além do balde por IP deu %d, Retry-After %q", status, h.Get("Retry-After"))
	}
}

// Um token em laço esgota o balde dele, e o vizinho no mesmo IP segue.
func TestPingPerTokenLimitAnswers429WithRetryAfter(t *testing.T) {
	a := newAPI(t)
	id, token := novoHeartbeat(t, a)
	_, vizinho := novoHeartbeat(t, a)

	sinais := []string{"/start", "", "/fail"}
	for i := range 10 {
		if status, _, corpo := pingar(t, a, "/ping/"+token+sinais[i%3], nil); status != http.StatusOK {
			t.Fatalf("sinal %d dentro da rajada: %d %s", i, status, corpo)
		}
	}
	antes := contarEventos(t, a, id)
	status, h, _ := pingar(t, a, "/ping/"+token+"/start", nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("sinal além da rajada deu %d, queria 429", status)
	}
	if h.Get("Retry-After") == "" {
		t.Error("429 sem Retry-After")
	}
	if n := contarEventos(t, a, id); n != antes {
		t.Errorf("o 429 gravou evento: %d, antes %d", n, antes)
	}
	if status, _, corpo := pingar(t, a, "/ping/"+vizinho, nil); status != http.StatusOK {
		t.Errorf("o vizinho foi barrado: %d %s", status, corpo)
	}
}

func TestPingRejectsOversizedBody(t *testing.T) {
	a := newAPI(t)
	id, token := novoHeartbeat(t, a)

	grande := strings.NewReader(strings.Repeat("x", 10<<10+1))
	if status, _, corpo := pingar(t, a, "/ping/"+token, grande); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("corpo acima do teto deu %d: %s", status, corpo)
	}
	// Sem Content-Length: é a drenagem que descobre.
	semTamanho := io.MultiReader(strings.NewReader(strings.Repeat("x", 10<<10+1)))
	if status, _, corpo := pingar(t, a, "/ping/"+token, semTamanho); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("corpo sem Content-Length deu %d: %s", status, corpo)
	}
	if n := contarEventos(t, a, id); n != 0 {
		t.Errorf("corpo recusado gravou %d eventos", n)
	}
	noTeto := strings.NewReader(strings.Repeat("x", 10<<10))
	if status, _, corpo := pingar(t, a, "/ping/"+token, noTeto); status != http.StatusOK {
		t.Errorf("corpo no teto deu %d: %s", status, corpo)
	}
}

// A cadeia só tem os três sinais: nada de GET, nada de subcaminho, nada de CORS.
func TestPingChainServesOnlyPing(t *testing.T) {
	a := newAPI(t)
	id, token := novoHeartbeat(t, a)

	for _, metodo := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequest(metodo, a.srv.URL+"/ping/"+token, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s deu %d, queria 405", metodo, resp.StatusCode)
		}
	}
	if status, _, corpo := pingar(t, a, "/ping/"+token+"/outra", nil); status != http.StatusNotFound {
		t.Errorf("subcaminho deu %d: %s", status, corpo)
	}
	if n := contarEventos(t, a, id); n != 0 {
		t.Errorf("rota inválida gravou %d eventos", n)
	}

	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/ping/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://app.exemplo.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("a cadeia pública tem CORS: %q", v)
	}
}
