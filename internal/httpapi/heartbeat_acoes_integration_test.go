package httpapi_test

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Ações sobre uma tarefa que já existe (SP1, PR 2): duplicar e limpar o log.

func clonar(t *testing.T, a *api, token, id string, corpo any) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/clone", token, corpo)
}

func contarHeartbeats(t *testing.T, a *api) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		var err error
		n, err = q.CountHeartbeats(ctx, a.userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A cópia leva a configuração (nome com sufixo, descrição, tags, agendamento,
// tolerância, enabled) e nasce sem nada do que o original viveu: token,
// eventos, last_*, falha, incidentes e retomada ficam para trás.
func TestCloneCopiesTheConfigurationAndNotTheState(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, tokenOriginal := criarHB(t, a, dono, map[string]any{
		"name": "backup noturno", "description": "roda às 3h no servidor de dados", "tags": []string{"prod", "backup"},
		"schedule": map[string]any{"kind": "period", "period_sec": 31536000}, "grace_sec": 600,
	})
	for _, sufixo := range []string{"/start", "", "/fail"} {
		if status, _, corpo := pingar(t, a, "/ping/"+tokenOriginal+sufixo, nil); status != http.StatusOK {
			t.Fatalf("ping %q no original: %d %s", sufixo, status, corpo)
		}
	}
	avaliar(t, a)
	if status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+original, dono, map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatalf("pausar o original: %d %s", status, corpo)
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+original, dono, nil)
	antes := decodificar(t, corpo)
	if antes["open_incident_id"] == nil || antes["last_ping_at"] == nil || antes["enabled"] != false {
		t.Fatalf("o original precisa estar pausado, com ping e incidente aberto: %s", corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(original)); n != 3 {
		t.Fatalf("o original tem %d eventos, queria 3", n)
	}

	a.clk.Advance(time.Minute)
	agoraDaCopia := a.clk.Now()
	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicar: %d %s", status, corpo)
	}
	copia := decodificar(t, corpo)
	tokenDaCopia := tokenDaURL(t, corpo)
	id := copia["id"].(string)
	if id == original || tokenDaCopia == tokenOriginal {
		t.Fatalf("a cópia repetiu o id ou o token do original: %s", corpo)
	}

	if copia["name"] != "backup noturno (cópia)" {
		t.Errorf("nome %v", copia["name"])
	}
	if copia["description"] != "roda às 3h no servidor de dados" {
		t.Errorf("descrição %v", copia["description"])
	}
	if got := copia["tags"].([]any); !slices.Equal(got, []any{"backup", "prod"}) {
		t.Errorf("tags %v", got)
	}
	agendamento := copia["schedule"].(map[string]any)
	if agendamento["kind"] != "period" || agendamento["period_sec"] != float64(31536000) {
		t.Errorf("agendamento %v", agendamento)
	}
	if copia["grace_sec"] != float64(600) || copia["enabled"] != false || copia["state"] != "paused" {
		t.Errorf("tolerância %v, enabled %v, estado %v", copia["grace_sec"], copia["enabled"], copia["state"])
	}
	for _, campo := range []string{"last_ping_at", "last_success_at", "last_start_at", "last_runtime_ms", "open_incident_id"} {
		if v, ok := copia[campo]; !ok || v != nil {
			t.Errorf("%s = %v, queria null", campo, v)
		}
	}
	prazo, err := time.Parse(time.RFC3339Nano, copia["deadline_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if quer := agoraDaCopia.Add(31536000*time.Second + 600*time.Second); !prazo.Equal(quer) {
		t.Errorf("prazo %s, queria %s", prazo, quer)
	}

	linha := linhaDoHeartbeat(t, a, uuid.MustParse(id))
	if linha.FailingSince != nil || linha.ResumedAt != nil || linha.LastSignal != nil {
		t.Errorf("a cópia herdou estado: falha %s, retomada %s, último sinal %s",
			valorDe(linha.FailingSince), valorDe(linha.ResumedAt), valorDe(linha.LastSignal))
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 0 {
		t.Errorf("a cópia nasceu com %d eventos", n)
	}
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents", dono, nil)
	if n := len(decodificar(t, corpo)["items"].([]any)); n != 0 {
		t.Errorf("a cópia nasceu com %d incidentes: %s", n, corpo)
	}

	// O token devolvido é da cópia: pinga ela, e o original não muda.
	if status, _, corpo := pingar(t, a, "/ping/"+tokenDaCopia+"/start", nil); status != http.StatusOK {
		t.Fatalf("ping com o token da cópia: %d %s", status, corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 1 {
		t.Errorf("a cópia tem %d eventos depois do ping, queria 1", n)
	}
	if n := contarEventos(t, a, uuid.MustParse(original)); n != 3 {
		t.Errorf("o ping da cópia chegou ao original: %d eventos, queria 3", n)
	}
}

// A resposta é a da criação: a tarefa e a ping_url, que só sai aqui.
func TestCloneResponseHasExactKeys(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, _ := criarHB(t, a, dono, corpoDePeriodo)
	base := []string{"created_at", "deadline_at", "description", "enabled", "grace_sec", "id", "last_ping_at", "last_runtime_ms", "last_start_at",
		"last_success_at", "name", "open_incident_id", "schedule", "state", "tags"}
	comURL := append(slices.Clone(base), "ping_url")
	sort.Strings(comURL)

	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicar: %d %s", status, corpo)
	}
	copia := decodificar(t, corpo)
	if got := chavesDe(copia); !slices.Equal(got, comURL) {
		t.Errorf("chaves %v, queria %v", got, comURL)
	}
	token := tokenDaURL(t, corpo)
	_, leitura := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+copia["id"].(string), dono, nil)
	if got := chavesDe(decodificar(t, leitura)); !slices.Equal(got, base) {
		t.Errorf("GET da cópia: chaves %v, queria %v", got, base)
	}
	if strings.Contains(leitura, token) {
		t.Errorf("GET da cópia devolveu o token: %s", leitura)
	}
}

// Com 99 tarefas a cópia é a centésima; com 100 é recusada e nada é gravado.
func TestCloneCountsTowardsTheLimit(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, _ := criarHB(t, a, dono, corpoDePeriodo)
	encherHeartbeats(t, a, 98)

	if status, corpo := clonar(t, a, dono, original, nil); status != http.StatusCreated {
		t.Fatalf("a centésima: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 100 {
		t.Fatalf("%d tarefas, queria 100", n)
	}
	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusConflict || !strings.Contains(corpo, `"heartbeat_limit_reached"`) {
		t.Errorf("acima do teto: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 100 {
		t.Errorf("%d tarefas depois da recusa, queria 100", n)
	}
}

// A linha de uma versão posterior (piso de rollback): esta não sabe regravar o
// agendamento nem calcular o prazo, então recusa sem gravar.
func TestCloneOfUnknownScheduleConflicts(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)

	status, corpo := clonar(t, a, dono, id.String(), nil)
	if status != http.StatusConflict || !strings.Contains(corpo, `"schedule_unsupported"`) {
		t.Errorf("duplicar agendamento desconhecido: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 1 {
		t.Errorf("%d tarefas depois da recusa, queria 1", n)
	}
}

// Nenhum campo do corpo muda a cópia: nome, estado, dono e enabled vêm do
// original e de quem chamou.
func TestCloneIgnoresTheBody(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	outro := outroUsuario(t, a)
	corpo := map[string]any{
		"name": "backup noturno", "schedule": map[string]any{"kind": "period", "period_sec": 3600}, "grace_sec": 300,
		"enabled": false,
	}
	original, _ := criarHB(t, a, dono, corpo)

	status, resp := clonar(t, a, dono, original, map[string]any{
		"name": "x", "status": "up", "user_id": outro.String(), "enabled": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("duplicar com corpo: %d %s", status, resp)
	}
	copia := decodificar(t, resp)
	if copia["name"] != "backup noturno (cópia)" || copia["enabled"] != false {
		t.Errorf("o corpo mudou a cópia: nome %v, enabled %v", copia["name"], copia["enabled"])
	}
	id := copia["id"].(string)
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); status != http.StatusOK {
		t.Errorf("a cópia não é de quem chamou: GET deu %d", status)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", tokenDe(t, a, outro), nil)
	if strings.Contains(lista, id) || strings.Contains(lista, "backup noturno") {
		t.Errorf("a cópia foi para o usuário do corpo: %s", lista)
	}
}

// Limpar o log (SP1, PR 2, Task 4).

func limpar(t *testing.T, a *api, token, id string, corpo any) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/events/clear", token, corpo)
}

// pingarOK manda os sinais em ordem, como o curl da tarefa. Sinais iguais em
// seguida, dentro de heartbeat.RepeatWindow, viram um evento só.
func pingarOK(t *testing.T, a *api, token string, sufixos ...string) {
	t.Helper()
	for _, sufixo := range sufixos {
		if status, _, corpo := pingar(t, a, "/ping/"+token+sufixo, nil); status != http.StatusOK {
			t.Fatalf("ping %q: %d %s", sufixo, status, corpo)
		}
	}
}

// registroDeLimpeza é uma linha de audit_log da limpeza do log.
type registroDeLimpeza struct {
	userID, entityID *uuid.UUID
	entity           *string
	semIP            bool
	at               time.Time
}

// registrosDeLimpeza lê audit_log direto: a tabela não tem RLS nem rota.
func registrosDeLimpeza(t *testing.T, a *api) []registroDeLimpeza {
	t.Helper()
	rows, err := a.pool.Query(context.Background(),
		`SELECT user_id, entity, entity_id, ip_hash IS NULL, at FROM audit_log
		  WHERE action = 'heartbeat.events_cleared' ORDER BY at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []registroDeLimpeza
	for rows.Next() {
		var r registroDeLimpeza
		if err := rows.Scan(&r.userID, &r.entity, &r.entityID, &r.semIP, &r.at); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// conferirUmRegistro exige exatamente a linha de auditoria de quem limpou o
// log de id, no instante dado e sem hash de IP.
func conferirUmRegistro(t *testing.T, a *api, id string, quando time.Time) {
	t.Helper()
	registros := registrosDeLimpeza(t, a)
	if len(registros) != 1 {
		t.Fatalf("%d linhas de auditoria, queria 1", len(registros))
	}
	r := registros[0]
	if r.userID == nil || *r.userID != a.userID {
		t.Errorf("user_id %s, queria %s", valorDe(r.userID), a.userID)
	}
	if r.entity == nil || *r.entity != "heartbeat" {
		t.Errorf("entity %s, queria heartbeat", valorDe(r.entity))
	}
	if r.entityID == nil || r.entityID.String() != id {
		t.Errorf("entity_id %s, queria %s", valorDe(r.entityID), id)
	}
	if !r.semIP {
		t.Error("a auditoria gravou hash de IP")
	}
	if !r.at.Equal(quando) {
		t.Errorf("at %s, queria %s", r.at, quando)
	}
}

func TestClearEventsWritesAudit(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingarOK(t, a, ping, "/start", "", "/fail")
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 3 {
		t.Fatalf("%d eventos antes de limpar, queria 3", n)
	}
	a.clk.Advance(time.Minute)
	quando := a.clk.Now()

	status, corpo := limpar(t, a, dono, id, nil)
	if status != http.StatusNoContent || corpo != "" {
		t.Fatalf("limpar: %d %q, queria 204 sem corpo", status, corpo)
	}
	status, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/events", dono, nil)
	if status != http.StatusOK {
		t.Fatalf("GET events: %d %s", status, corpo)
	}
	if itens := decodificar(t, corpo)["items"].([]any); len(itens) != 0 {
		t.Errorf("o log ficou com %d eventos: %s", len(itens), corpo)
	}
	conferirUmRegistro(t, a, id, quando)
}

// Limpar mexe só no log: os sinais, o prazo, a falha e o incidente aberto
// ficam exatamente como estavam (spec §7.2).
func TestClearEventsKeepsTheSignalsAndTheDeadline(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	hb := uuid.MustParse(id)
	pingarOK(t, a, ping, "/start", "", "/fail")
	avaliar(t, a)
	ultimoPing := time.Date(2026, time.March, 4, 1, 2, 3, 0, time.UTC)
	ultimoSucesso := time.Date(2026, time.March, 4, 0, 59, 0, 0, time.UTC)
	comoDonoSQL(t, a, `UPDATE heartbeats SET last_ping_at = $2, last_success_at = $3, last_runtime_ms = 4321, deadline_at = $4
		WHERE id = $1`, hb, ultimoPing, ultimoSucesso, prazoGravado)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	incidente, ok := decodificar(t, corpo)["open_incident_id"].(string)
	if !ok {
		t.Fatalf("a tarefa precisa de um incidente aberto: %s", corpo)
	}
	linhaAntes := linhaDoHeartbeat(t, a, hb)

	if status, corpo := limpar(t, a, dono, id, nil); status != http.StatusNoContent {
		t.Fatalf("limpar: %d %s", status, corpo)
	}

	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	depois := decodificar(t, corpo)
	for campo, quer := range map[string]time.Time{"last_ping_at": ultimoPing, "last_success_at": ultimoSucesso, "deadline_at": prazoGravado} {
		texto, _ := depois[campo].(string)
		got, err := time.Parse(time.RFC3339Nano, texto)
		if err != nil || !got.Equal(quer) {
			t.Errorf("%s = %v, queria %s", campo, depois[campo], quer)
		}
	}
	if depois["last_runtime_ms"] != float64(4321) {
		t.Errorf("last_runtime_ms = %v, queria 4321", depois["last_runtime_ms"])
	}
	if depois["open_incident_id"] != incidente {
		t.Errorf("open_incident_id = %v, queria %s", depois["open_incident_id"], incidente)
	}
	if linhaDepois := linhaDoHeartbeat(t, a, hb); !reflect.DeepEqual(linhaDepois, linhaAntes) {
		t.Errorf("a linha da tarefa mudou:\nantes  %+v\ndepois %+v", linhaAntes, linhaDepois)
	}
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents?status=open", dono, nil)
	abertos := decodificar(t, corpo)["items"].([]any)
	if len(abertos) != 1 || abertos[0].(map[string]any)["id"] != incidente {
		t.Errorf("incidentes abertos depois de limpar: %s", corpo)
	}
	if n := contarEventos(t, a, hb); n != 0 {
		t.Errorf("%d eventos depois de limpar, queria 0", n)
	}
}

// O pedido de limpar aconteceu mesmo com o log vazio, e fica registrado.
func TestClearEventsOnAnEmptyLogIsStillNoContent(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 0 {
		t.Fatalf("%d eventos numa tarefa nova", n)
	}

	status, corpo := limpar(t, a, dono, id, nil)
	if status != http.StatusNoContent || corpo != "" {
		t.Fatalf("limpar o log vazio: %d %q, queria 204 sem corpo", status, corpo)
	}
	conferirUmRegistro(t, a, id, a.clk.Now())
}

// O log recomeça do zero: o primeiro ping depois de limpar é o único evento.
func TestPingAfterClearRecordsOneEvent(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingarOK(t, a, ping, "/start", "")
	if status, corpo := limpar(t, a, dono, id, nil); status != http.StatusNoContent {
		t.Fatalf("limpar: %d %s", status, corpo)
	}
	a.clk.Advance(time.Minute)
	instante := a.clk.Now()

	pingarOK(t, a, ping, "")

	eventos := eventosDoHeartbeat(t, a, uuid.MustParse(id))
	if len(eventos) != 1 {
		t.Fatalf("%d eventos depois do ping, queria 1", len(eventos))
	}
	if eventos[0].Kind != "success" || !eventos[0].At.Equal(instante) {
		t.Errorf("evento %s em %s, queria success em %s", eventos[0].Kind, eventos[0].At, instante)
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	texto, _ := decodificar(t, corpo)["last_ping_at"].(string)
	if got, err := time.Parse(time.RFC3339Nano, texto); err != nil || !got.Equal(instante) {
		t.Errorf("last_ping_at = %q, queria %s", texto, instante)
	}
}

// A tarefa alheia é 404 sem dado do dono, e o log dele fica inteiro, sem
// auditoria em nome de ninguém.
func TestClearEventsOfAnotherUserIsNotFound(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingarOK(t, a, ping, "/start", "")
	intruso := tokenDe(t, a, outroUsuario(t, a))

	status, corpo := limpar(t, a, intruso, id, nil)
	if status != http.StatusNotFound {
		t.Errorf("limpar a tarefa alheia: %d %s, queria 404", status, corpo)
	}
	conferirSemDadosDoDono(t, "limpar a tarefa alheia", corpo, []string{id, "backup noturno"})
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 2 {
		t.Errorf("o dono ficou com %d eventos, queria 2", n)
	}
	if registros := registrosDeLimpeza(t, a); len(registros) != 0 {
		t.Errorf("%d linhas de auditoria depois da recusa, queria 0", len(registros))
	}
}

// Nenhum campo do corpo escolhe o que sai: o log da tarefa da rota sai
// inteiro, e o da tarefa nomeada no corpo fica.
func TestClearEventsIgnoresTheBody(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	outra, pingDaOutra := criarHB(t, a, dono, corpoDePeriodo)
	pingarOK(t, a, ping, "/start", "", "/fail")
	pingarOK(t, a, pingDaOutra, "/start", "")

	status, corpo := limpar(t, a, dono, id, map[string]any{"heartbeat_id": outra, "keep": 2, "before": uuid.NewString()})
	if status != http.StatusNoContent {
		t.Fatalf("limpar com corpo: %d %s", status, corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 0 {
		t.Errorf("a tarefa da rota ficou com %d eventos, queria 0", n)
	}
	if n := contarEventos(t, a, uuid.MustParse(outra)); n != 2 {
		t.Errorf("a tarefa do corpo ficou com %d eventos, queria 2", n)
	}
}

// Limpar não regrava o agendamento nem recalcula o prazo, então a linha de uma
// versão posterior também se limpa, e o prazo dela fica.
func TestClearEventsWorksOnUnknownSchedule(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, ping := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)
	pingarOK(t, a, ping, "")
	if n := contarEventos(t, a, id); n != 1 {
		t.Fatalf("%d eventos antes de limpar, queria 1", n)
	}

	if status, corpo := limpar(t, a, dono, id.String(), nil); status != http.StatusNoContent {
		t.Fatalf("limpar agendamento desconhecido: %d %s", status, corpo)
	}
	if n := contarEventos(t, a, id); n != 0 {
		t.Errorf("%d eventos depois de limpar, queria 0", n)
	}
	if linha := linhaDoHeartbeat(t, a, id); !linha.DeadlineAt.Equal(prazoGravado) || linha.PeriodSec != nil || linha.CronExpr != nil {
		t.Errorf("limpar mexeu no agendamento: prazo %s, período %s, cron %s",
			linha.DeadlineAt, valorDe(linha.PeriodSec), valorDe(linha.CronExpr))
	}
}

// Log apagado sem rastro não existe: se a auditoria falha no banco, os eventos
// voltam com o rollback.
func TestClearEventsKeepsTheLogWhenTheAuditFails(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingarOK(t, a, ping, "/start", "")
	recusarAuditoria(t, a)

	if status, corpo := limpar(t, a, dono, id, nil); status != http.StatusInternalServerError {
		t.Errorf("limpar com a auditoria fora: %d %s, queria 500", status, corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 2 {
		t.Errorf("%d eventos depois da falha, queria 2", n)
	}
}
