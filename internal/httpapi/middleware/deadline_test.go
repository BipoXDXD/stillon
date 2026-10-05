package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

func TestDeadlineInterrompeTrabalhoCooperativo(t *testing.T) {
	h := middleware.Deadline(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
				t.Errorf("cancelamento=%v", r.Context().Err())
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-time.After(time.Second):
			t.Error("trabalho não recebeu prazo")
		}
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 503 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestDeadlineRecoverMantemEnvelope(t *testing.T) {
	h := middleware.RequestID(middleware.Deadline(20 * time.Millisecond)(middleware.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		panic("falha privada após prazo")
	}))))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var body struct {
		Error struct{ Code string }
		Trace string `json:"trace_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || body.Error.Code != "unavailable" || body.Trace == "" || body.Trace != w.Header().Get(middleware.TraceHeader) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeadlineHerdaPrazoMenorECancelamento(t *testing.T) {
	for _, tc := range []string{"prazo menor", "cancelamento do cliente"} {
		t.Run(tc, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			want, _ := parent.Deadline()
			var child context.Context
			h := middleware.Deadline(time.Hour)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				child = r.Context()
				got, ok := child.Deadline()
				if !ok || !got.Equal(want) {
					t.Errorf("deadline=%v, queria %v", got, want)
				}
				if observability.TraceID(child) != "trace" {
					t.Error("perdeu trace do contexto")
				}
				if tc == "cancelamento do cliente" {
					cancel()
				}
				select {
				case <-child.Done():
				case <-time.After(time.Second):
					t.Error("contexto filho não cancelado")
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(observability.WithTraceID(parent, "trace")))
			if child.Err() != parent.Err() {
				t.Errorf("filho=%v pai=%v", child.Err(), parent.Err())
			}
		})
	}
}

func TestDeadlineLiberaContextoAoRetornar(t *testing.T) {
	var ctx context.Context
	h := middleware.Deadline(time.Hour)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("contexto retido após handler: %v", ctx.Err())
	}
}
