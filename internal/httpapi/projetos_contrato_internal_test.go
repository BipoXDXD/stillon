package httpapi

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// O acordo entre projectResponse e o schema de projeto do painel, no molde de
// channels_contrato_internal_test.go: o JSON real, de toProjectResponse e
// apierr.WriteJSON, tem de continuar igual a testdata/contrato-projetos.json.
// Mudou o DTO de propósito? Regrave com STILLON_REGRAVAR_CONTRATO=1. O painel
// lê este arquivo (schemas-contrato.test.ts): regravar pede rodar também o
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

// O item tem exatamente as chaves que o painel lê, e limits as seis cotas do
// titular.
func TestProjectContractListsLimits(t *testing.T) {
	for _, c := range projetosDoContrato() {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(respostaRealDeProjeto(t, c.projeto), &item); err != nil {
			t.Fatal(err)
		}
		if got, want := chavesOrdenadas(item), []string{"created_at", "id", "is_default", "limits", "name", "pending_titular", "role"}; !slices.Equal(got, want) {
			t.Errorf("%s: chaves do item = %v, queria %v", c.nome, got, want)
		}
		var limites map[string]json.RawMessage
		if err := json.Unmarshal(item["limits"], &limites); err != nil {
			t.Fatalf("%s: limits não é objeto: %v", c.nome, err)
		}
		want := []string{"channels_per_project", "heartbeats", "members_per_project", "min_interval_sec", "projects", "targets"}
		if got := chavesOrdenadas(limites); !slices.Equal(got, want) {
			t.Errorf("%s: chaves de limits = %v, queria %v", c.nome, got, want)
		}
	}
}

func chavesOrdenadas(m map[string]json.RawMessage) []string {
	chaves := slices.Collect(maps.Keys(m))
	slices.Sort(chaves)
	return chaves
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
			Limites:   tenancy.LimitesDe(false),
		}},
		{nome: "criado pela API, owner, titular operador", projeto: tenancy.ProjectSummary{
			ID:        uuid.MustParse("01900000-0000-7000-8000-000000000102"),
			Name:      "Clientes",
			Role:      tenancy.RoleOwner,
			CreatedAt: time.Date(2026, 11, 22, 9, 30, 15, 123000000, time.UTC),
			Limites:   tenancy.LimitesDe(true),
		}},
		{nome: "proposto a quem pede", projeto: tenancy.ProjectSummary{
			ID:             uuid.MustParse("01900000-0000-7000-8000-000000000103"),
			Name:           "Loja",
			Role:           tenancy.RoleOwner,
			CreatedAt:      time.Date(2026, 11, 27, 18, 5, 0, 0, time.UTC),
			Limites:        tenancy.LimitesDe(false),
			PendingTitular: true,
		}},
	}
}
