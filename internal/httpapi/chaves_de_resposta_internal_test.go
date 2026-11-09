package httpapi

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// Allowlist de saída (Secure APIs 4.6.2): cada DTO abaixo tem exatamente estas
// chaves. Um campo novo tem de passar por aqui, de propósito. Os que têm esse
// teste nas integrações (alvo, heartbeat, sessão, resumo) ficam de fora.
func TestDTOsDeSaidaTemExatamenteAsChavesDoContrato(t *testing.T) {
	agora := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	casos := map[string]struct {
		dto    any
		chaves []string
	}{
		"canal": {
			toChannelResponse(notify.Channel{
				ID: uuid.New(), Kind: notify.KindDiscord, Hint: "webhook …6789", Status: notify.StatusFailing,
				Enabled: true, CreatedAt: agora, LastFailureAt: agora, LastError: notify.Reason("x"),
			}),
			[]string{"created_at", "enabled", "hint", "id", "kind", "last_error", "last_failure_at", "status"},
		},
		"incidente": {
			toIncidentResponse(history.Incident{ID: uuid.New(), TargetID: uuid.New(), TargetName: "api", StartedAt: agora, Cause: "timeout"}),
			[]string{"acknowledged_at", "cause", "id", "resolved_at", "started_at", "target_id", "target_name"},
		},
		"página de status": {
			(&StatusPageHandlers{baseURL: "https://exemplo.com"}).toResponse(statuspage.Page{
				ID: uuid.New(), Slug: "empresa", Title: "T", Published: true, CreatedAt: agora,
				Items: []statuspage.Item{{TargetID: uuid.New(), DisplayName: "API"}},
			}),
			[]string{"created_at", "id", "items", "public_url", "published", "slug", "title"},
		},
		"item da página": {statusPageItemJSON{}, []string{"display_name", "target_id"}},
		"conta":          {meResponse{}, []string{"created_at", "email", "id", "totp_enabled"}},
		"check":          {checkResponse{}, []string{"checked_at", "error_kind", "latency_ms", "status", "status_code"}},
		"hora da série":  {rollupResponse{}, []string{"bucket", "max_ms", "ok", "p50_ms", "p95_ms", "p99_ms", "total"}},
		"token":          {tokenResponse{}, []string{"access_token", "expires_in"}},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			cru, err := json.Marshal(c.dto)
			if err != nil {
				t.Fatal(err)
			}
			var campos map[string]json.RawMessage
			if err := json.Unmarshal(cru, &campos); err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(maps.Keys(campos)); !slices.Equal(got, c.chaves) {
				t.Errorf("chaves %v, queria %v", got, c.chaves)
			}
		})
	}
}

// Canal que nunca falhou sai com os dois campos em null, e não sem eles: o
// conjunto de chaves é o mesmo nos dois estados.
func TestCanalSemFalhaMantemAsChaves(t *testing.T) {
	cru, err := json.Marshal(toChannelResponse(notify.Channel{ID: uuid.New()}))
	if err != nil {
		t.Fatal(err)
	}
	var campos map[string]json.RawMessage
	if err := json.Unmarshal(cru, &campos); err != nil {
		t.Fatal(err)
	}
	for _, chave := range []string{"last_error", "last_failure_at"} {
		if string(campos[chave]) != "null" {
			t.Errorf("%s = %s, queria null", chave, campos[chave])
		}
	}
}
