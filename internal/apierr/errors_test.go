package apierr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
)

func TestDeadlineMantemEnvelopeEErrosDeDominio(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ctx = observability.WithTraceID(ctx, "trace-deadline")
	for _, tc := range []struct {
		code   apierr.ErrorCode
		status int
		want   apierr.ErrorCode
	}{
		{apierr.CodeInternal, 503, apierr.CodeUnavailable},
		{apierr.CodeValidation, 400, apierr.CodeValidation},
		{apierr.CodeUnauthorized, 401, apierr.CodeUnauthorized},
		{apierr.CodeTargetLimit, 409, apierr.CodeTargetLimit},
		{apierr.CodeRateLimited, 429, apierr.CodeRateLimited},
		{apierr.CodeDeliveryFailed, 502, apierr.CodeDeliveryFailed},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			w := httptest.NewRecorder()
			apierr.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), tc.code)
			var got struct {
				Error struct{ Code apierr.ErrorCode }
				Trace string `json:"trace_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || got.Error.Code != tc.want || got.Trace != "trace-deadline" {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestInternalSemDeadlineContinua500(t *testing.T) {
	w := httptest.NewRecorder()
	apierr.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), apierr.CodeInternal)
	if w.Code != 500 {
		t.Fatalf("status=%d", w.Code)
	}
}
