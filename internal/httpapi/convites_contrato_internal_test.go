package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// O acordo entre invitationResponse e o painel, no molde de
// membros_contrato_internal_test.go: o JSON real, de toInvitationResponse e
// apierr.WriteJSON, tem de continuar igual a testdata/contrato-convites.json.
// Mudou o DTO de propósito? Regrave com STILLON_REGRAVAR_CONTRATO=1. O vitest
// do painel lê este arquivo (schemas-contrato.test.ts): rode-o também.
const arquivoDoContratoConvites = "contrato-convites.json"

func TestInvitationResponseKeepsThePanelContract(t *testing.T) {
	var casos []casoDoContrato
	for _, c := range convitesDoContrato() {
		rec := httptest.NewRecorder()
		apierr.WriteJSON(rec, http.StatusCreated, toInvitationResponse(c.convite))
		if rec.Code != http.StatusCreated {
			t.Fatalf("WriteJSON deu %d", rec.Code)
		}
		casos = append(casos, casoDoContrato{Caso: c.nome, Resposta: bytes.TrimSpace(rec.Body.Bytes())})
	}
	gerado, err := json.MarshalIndent(casos, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	gerado = append(gerado, '\n')

	caminho := filepath.Join("testdata", arquivoDoContratoConvites)
	if os.Getenv(regravarContrato) != "" {
		if err := os.WriteFile(caminho, gerado, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	gravado, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gerado, gravado) {
		t.Errorf("a resposta de convite mudou; se foi de propósito, regrave com %s=1 e confira o git diff de %s",
			regravarContrato, caminho)
	}
}

// convitesDoContrato é a entrada do domínio, e nunca o JSON: a forma sai só
// de toInvitationResponse. O projeto do convite não vai na resposta (está no
// caminho).
func convitesDoContrato() []struct {
	nome    string
	convite tenancy.Invitation
} {
	criado := time.Date(2026, 11, 27, 9, 30, 15, 123000000, time.UTC)
	return []struct {
		nome    string
		convite tenancy.Invitation
	}{
		{"member", tenancy.Invitation{
			ID: uuid.MustParse("01900000-0000-7000-8000-000000000301"), ProjectID: uuid.MustParse("01900000-0000-7000-8000-000000000300"),
			Email: "convidada@exemplo.com", Role: tenancy.RoleMember, CreatedAt: criado, ExpiresAt: criado.Add(tenancy.InvitationTTL),
		}},
		{"owner", tenancy.Invitation{
			ID: uuid.MustParse("01900000-0000-7000-8000-000000000302"), ProjectID: uuid.MustParse("01900000-0000-7000-8000-000000000300"),
			Email: "socio@exemplo.com", Role: tenancy.RoleOwner,
			CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		}},
	}
}
