package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Um handler de recurso montado sem o middleware de projeto não pode cair num
// escopo zero: escopo() recusa com 500 e registra o defeito de montagem.
func TestResourceHandlerWithoutProjectScopeFailsClosed(t *testing.T) {
	var log bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(anterior) })

	rec := httptest.NewRecorder()
	sc, ok := escopo(rec, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))

	if ok {
		t.Fatalf("escopo() sem contexto devolveu ok com %+v", sc)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, queria 500: %s", rec.Code, rec.Body.String())
	}
	var corpo struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil || corpo.Error.Code != "internal_error" {
		t.Fatalf("corpo %s (erro %v), queria code internal_error", rec.Body.String(), err)
	}
	if !strings.Contains(log.String(), "rota de recurso sem escopo de projeto") {
		t.Errorf("o log não registrou o defeito: %s", log.String())
	}
}
