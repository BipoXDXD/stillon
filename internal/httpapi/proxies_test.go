package httpapi

import (
	"testing"

	"github.com/BipoXDXD/vigilume/internal/config"
)

// B3 (Plano 10): o aviso de X-Forwarded-For fora de TRUSTED_PROXIES só liga
// em produção (PUBLIC_BASE_URL https). Em desenvolvimento seria ruído: o
// compose e o Vite não têm proxy na lista.
func TestProxiesWarnAboutForwardedForOnlyOverHTTPS(t *testing.T) {
	for _, c := range []struct {
		base string
		quer bool
	}{
		{"https://status.exemplo.com", true},
		{"HTTPS://status.exemplo.com", true},
		{"http://localhost:8080", false},
		{"", false},
	} {
		if got := proxiesDe(&config.Config{PublicBaseURL: c.base}).AvisarXFF; got != c.quer {
			t.Errorf("PUBLIC_BASE_URL %q: AvisarXFF = %v, queria %v", c.base, got, c.quer)
		}
	}
}
