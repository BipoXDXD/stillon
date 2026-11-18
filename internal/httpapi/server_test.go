package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

func TestNewServerConfiguresTimeoutsAndHeaderLimit(t *testing.T) {
	srv := httpapi.NewServer(&config.Config{}, http.NotFoundHandler())

	for _, c := range []struct {
		campo     string
		got, quer time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, 5 * time.Second},
		{"ReadTimeout", srv.ReadTimeout, 15 * time.Second},
		{"WriteTimeout", srv.WriteTimeout, 30 * time.Second},
		{"IdleTimeout", srv.IdleTimeout, 60 * time.Second},
	} {
		if c.got != c.quer {
			t.Errorf("%s = %s, queria %s", c.campo, c.got, c.quer)
		}
	}
	if srv.MaxHeaderBytes != 64<<10 {
		t.Errorf("MaxHeaderBytes = %d, queria %d", srv.MaxHeaderBytes, 64<<10)
	}
}
