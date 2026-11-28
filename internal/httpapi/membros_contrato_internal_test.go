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

// O acordo entre memberResponse e o painel, no molde de
// projetos_contrato_internal_test.go: o JSON real, de toMemberResponse e
// apierr.WriteJSON, tem de continuar igual a testdata/contrato-membros.json.
// Mudou o DTO de propósito? Regrave com STILLON_REGRAVAR_CONTRATO=1. O vitest
// do painel lê este arquivo (schemas-contrato.test.ts): rode-o também.
const arquivoDoContratoMembros = "contrato-membros.json"

func TestMemberResponseKeepsThePanelContract(t *testing.T) {
	var casos []casoDoContrato
	for _, c := range membrosDoContrato() {
		rec := httptest.NewRecorder()
		apierr.WriteJSON(rec, http.StatusOK, toMemberResponse(c.membro))
		if rec.Code != http.StatusOK {
			t.Fatalf("WriteJSON deu %d", rec.Code)
		}
		casos = append(casos, casoDoContrato{Caso: c.nome, Resposta: bytes.TrimSpace(rec.Body.Bytes())})
	}
	gerado, err := json.MarshalIndent(casos, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	gerado = append(gerado, '\n')

	caminho := filepath.Join("testdata", arquivoDoContratoMembros)
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
		t.Errorf("a resposta de membro mudou; se foi de propósito, regrave com %s=1 e confira o git diff de %s",
			regravarContrato, caminho)
	}
}

// membrosDoContrato é a entrada do domínio, e nunca o JSON: a forma sai só de
// toMemberResponse.
func membrosDoContrato() []struct {
	nome   string
	membro tenancy.MemberView
} {
	return []struct {
		nome   string
		membro tenancy.MemberView
	}{
		{"titular", tenancy.MemberView{
			UserID: uuid.MustParse("01900000-0000-7000-8000-000000000201"), Email: "titular@exemplo.com",
			Role: tenancy.RoleOwner, IsTitular: true, JoinedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		}},
		{"viewer", tenancy.MemberView{
			UserID: uuid.MustParse("01900000-0000-7000-8000-000000000202"), Email: "leitor@exemplo.com",
			Role: tenancy.RoleViewer, JoinedAt: time.Date(2026, 11, 27, 9, 30, 15, 123000000, time.UTC),
		}},
		{"owner com transferência proposta", tenancy.MemberView{
			UserID: uuid.MustParse("01900000-0000-7000-8000-000000000203"), Email: "socia@exemplo.com",
			Role: tenancy.RoleOwner, PendingTitular: true, JoinedAt: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC),
		}},
	}
}
