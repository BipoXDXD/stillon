package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BipoXDXD/stillon/internal/observability"
)

// registroCapturado guarda o essencial de uma linha de log para conferência.
type registroCapturado struct {
	nivel     slog.Level
	mensagem  string
	atributos map[string]string
}

// capturaLog é um slog.Handler que só acumula os registros em memória.
type capturaLog struct {
	mu        sync.Mutex
	registros []registroCapturado
}

func (c *capturaLog) Enabled(context.Context, slog.Level) bool { return true }

func (c *capturaLog) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registros = append(c.registros, registroCapturado{nivel: r.Level, mensagem: r.Message, atributos: attrs})
	return nil
}

func (c *capturaLog) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capturaLog) WithGroup(string) slog.Handler      { return c }

// trocaLogPadrao põe a captura no lugar do slog.Default e devolve o
// original no fim do teste. Mexe em estado global: nada de t.Parallel.
func trocaLogPadrao(t *testing.T) *capturaLog {
	t.Helper()
	original := slog.Default()
	captura := &capturaLog{}
	slog.SetDefault(slog.New(captura))
	t.Cleanup(func() { slog.SetDefault(original) })
	return captura
}

func TestErroInternoLogLevel(t *testing.T) {
	const op = "testa erro interno"
	const traceID = "trace-desistencia"

	casos := []struct {
		nome          string
		cancelaCtx    bool
		err           error
		nivelEsperado slog.Level
	}{
		{
			nome:          "cliente desistiu: contexto cancelado e erro com context.Canceled",
			cancelaCtx:    true,
			err:           fmt.Errorf("consulta: %w", context.Canceled),
			nivelEsperado: slog.LevelInfo,
		},
		{
			nome:          "contexto vivo e erro qualquer",
			cancelaCtx:    false,
			err:           errors.New("banco caiu"),
			nivelEsperado: slog.LevelError,
		},
		{
			nome:          "contexto vivo mas context.Canceled vindo de outra fonte",
			cancelaCtx:    false,
			err:           fmt.Errorf("tarefa interna: %w", context.Canceled),
			nivelEsperado: slog.LevelError,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			captura := trocaLogPadrao(t)

			ctx, cancela := context.WithCancel(observability.WithTraceID(context.Background(), traceID))
			defer cancela()
			if tc.cancelaCtx {
				cancela()
			}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/qualquer", nil).WithContext(ctx)
			w := httptest.NewRecorder()

			erroInterno(w, r, op, tc.err)

			if len(captura.registros) != 1 {
				t.Fatalf("esperava 1 registro de log, veio %d: %+v", len(captura.registros), captura.registros)
			}
			reg := captura.registros[0]
			if reg.nivel != tc.nivelEsperado {
				t.Errorf("nível = %v, esperava %v", reg.nivel, tc.nivelEsperado)
			}
			if !strings.Contains(reg.mensagem, op) {
				t.Errorf("mensagem %q não contém a op %q", reg.mensagem, op)
			}
			if reg.atributos["trace_id"] != traceID {
				t.Errorf("trace_id no log = %q, esperava %q", reg.atributos["trace_id"], traceID)
			}

			// O cliente recebe o mesmo envelope internal_error nos três casos.
			if w.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, esperava 500", w.Code)
			}
			var corpo struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
				TraceID string `json:"trace_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &corpo); err != nil {
				t.Fatalf("corpo não é JSON: %v (%s)", err, w.Body.String())
			}
			if corpo.Error.Code != "internal_error" {
				t.Errorf("code = %q, esperava internal_error", corpo.Error.Code)
			}
			if corpo.TraceID != traceID {
				t.Errorf("trace_id no corpo = %q, esperava %q", corpo.TraceID, traceID)
			}
		})
	}
}
