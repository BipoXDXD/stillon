package apierr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// Um código desconhecido não pertence ao contrato público: o cliente recebe
// o envelope seguro de erro interno, ou indisponibilidade se o prazo venceu.
func TestCodigoDesconhecidoMantemEnvelopeSeguro(t *testing.T) {
	for _, caso := range []struct {
		nome, codigo, mensagem string
		status                 int
		prazoVencido           bool
	}{
		{"sem deadline", "internal_error", "Erro interno.", http.StatusInternalServerError, false},
		{"deadline vencido", "unavailable", "Serviço indisponível.", http.StatusServiceUnavailable, true},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			ctx := context.Background()
			if caso.prazoVencido {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			ctx = observability.WithTraceID(ctx, "trace-codigo-desconhecido")
			pedido := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			resposta := httptest.NewRecorder()
			const desconhecido = "codigo-nao-publico"
			apierr.WriteError(resposta, pedido, apierr.ErrorCode(desconhecido))
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				TraceID string `json:"trace_id"`
			}
			if err := json.Unmarshal(resposta.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("resposta não é JSON válido: %v", err)
			}
			if resposta.Code != caso.status || envelope.Error.Code != caso.codigo || envelope.Error.Message != caso.mensagem {
				t.Fatalf("status = %d, envelope = %s", resposta.Code, resposta.Body.String())
			}
			if resposta.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, queria JSON UTF-8", resposta.Header().Get("Content-Type"))
			}
			if envelope.TraceID != "trace-codigo-desconhecido" {
				t.Errorf("trace_id = %q, queria correlacionar o pedido", envelope.TraceID)
			}
			if strings.Contains(resposta.Body.String(), desconhecido) {
				t.Fatal("a resposta repetiu um código que não pertence ao contrato público")
			}
		})
	}
}

func TestValidacaoEntregaCamposETraceNoJSON(t *testing.T) {
	ctx := observability.WithTraceID(context.Background(), "trace-validacao")
	resposta := httptest.NewRecorder()
	apierr.WriteValidation(resposta, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), []apierr.FieldError{
		{Field: "url", Reason: "invalid"},
		{Field: "timeout_ms", Reason: "out_of_range"},
	})
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Fields  []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"fields"`
		} `json:"error"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(resposta.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope de validação inválido: %v", err)
	}
	if resposta.Code != http.StatusBadRequest || envelope.Error.Code != "validation_failed" || envelope.Error.Message != "Dados inválidos." {
		t.Fatalf("status = %d, envelope = %s", resposta.Code, resposta.Body.String())
	}
	if envelope.TraceID != "trace-validacao" {
		t.Errorf("trace_id = %q, queria correlacionar a validação", envelope.TraceID)
	}
	if len(envelope.Error.Fields) != 2 {
		t.Fatalf("campos = %v, queria os dois campos culpados", envelope.Error.Fields)
	}
	if envelope.Error.Fields[0].Field != "url" || envelope.Error.Fields[0].Reason != "invalid" ||
		envelope.Error.Fields[1].Field != "timeout_ms" || envelope.Error.Fields[1].Reason != "out_of_range" {
		t.Errorf("campos de validação alterados: %v", envelope.Error.Fields)
	}
}

func TestFalhaDeCodificacaoJSONFicaVisivelNoLog(t *testing.T) {
	// A troca do logger global impede t.Parallel neste teste. O logger real
	// anterior é restaurado mesmo se uma asserção falhar.
	anterior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(anterior) })
	var log bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	resposta := httptest.NewRecorder()
	apierr.WriteJSON(resposta, http.StatusOK, map[string]float64{"latencia": math.NaN()})
	if resposta.Code != http.StatusOK || resposta.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status = %d, Content-Type = %q", resposta.Code, resposta.Header().Get("Content-Type"))
	}
	if resposta.Body.Len() != 0 {
		t.Errorf("uma falha de codificação produziu corpo: %q", resposta.Body.String())
	}
	var registro struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Erro  string `json:"erro"`
	}
	if err := json.Unmarshal(log.Bytes(), &registro); err != nil {
		t.Fatalf("a falha de codificação não produziu um registro JSON: %v", err)
	}
	if registro.Level != "ERROR" || registro.Msg != "falha ao escrever resposta" || !strings.Contains(registro.Erro, "NaN") {
		t.Fatalf("registro da falha = %s", log.String())
	}
}
