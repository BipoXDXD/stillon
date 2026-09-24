package observability_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/observability"
)

func TestMetricsServerExposesRuntimeMetrics(t *testing.T) {
	srv := httptest.NewServer(observability.NewMetricsServer("", observability.NewRegistry()).Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	corpo, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(corpo), "go_goroutines") {
		t.Errorf("status %d, corpo sem go_goroutines", resp.StatusCode)
	}

	outra, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	outra.Body.Close()
	if outra.StatusCode != http.StatusNotFound {
		t.Errorf("/ deu %d: o servidor de métricas só serve /metrics", outra.StatusCode)
	}
}
