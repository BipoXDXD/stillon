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

// O acordo entre projectResponse e o schema de projeto do painel, no molde de
// channels_contrato_internal_test.go: o JSON real, de toProjectResponse e
// apierr.WriteJSON, tem de continuar igual a testdata/contrato-projetos.json.
// Mudou o DTO de propósito? Regrave com STILLON_REGRAVAR_CONTRATO=1 e rode o
// vitest do painel.
const arquivoDoContratoProjetos = "contrato-projetos.json"

func TestProjectResponseKeepsThePanelContract(t *testing.T) {
	gerado := contratoGeradoProjetos(t)
	caminho := filepath.Join("testdata", arquivoDoContratoProjetos)
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
		t.Errorf("a resposta de projeto mudou; se foi de propósito, regrave com %s=1, confira o git diff de %s e rode o vitest do painel",
			regravarContrato, caminho)
	}
}

func contratoGeradoProjetos(t *testing.T) []byte {
	t.Helper()
	var casos []casoDoContrato
	for _, c := range projetosDoContrato() {
		casos = append(casos, casoDoContrato{Caso: c.nome, Resposta: respostaRealDeProjeto(t, c.projeto)})
	}
	b, err := json.MarshalIndent(casos, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func respostaRealDeProjeto(t *testing.T, p tenancy.ProjectSummary) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	apierr.WriteJSON(rec, http.StatusOK, toProjectResponse(p))
	if rec.Code != http.StatusOK {
		t.Fatalf("WriteJSON deu %d", rec.Code)
	}
	return bytes.TrimSpace(rec.Body.Bytes())
}

type projetoDoContrato struct {
	nome    string
	projeto tenancy.ProjectSummary
}

// projetosDoContrato é a entrada do domínio, e nunca o JSON: a forma sai só
// de toProjectResponse.
func projetosDoContrato() []projetoDoContrato {
	return []projetoDoContrato{
		{nome: "padrão, owner", projeto: tenancy.ProjectSummary{
			ID:        uuid.MustParse("01900000-0000-7000-8000-000000000101"),
			Name:      "Projeto padrão",
			IsDefault: true,
			Role:      tenancy.RoleOwner,
			CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		}},
		{nome: "criado pela API, owner", projeto: tenancy.ProjectSummary{
			ID:        uuid.MustParse("01900000-0000-7000-8000-000000000102"),
			Name:      "Clientes",
			Role:      tenancy.RoleOwner,
			CreatedAt: time.Date(2026, 11, 22, 9, 30, 15, 123000000, time.UTC),
		}},
	}
}
