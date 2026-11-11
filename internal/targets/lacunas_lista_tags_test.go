package targets_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// A lista traz a validade do certificado só do alvo que a tem, em UTC, e não
// mistura alvos de outro dono. Antes, o corpo do laço de validadesDoDono nunca
// rodava no pacote.
func TestListTrazAValidadeDoCertificadoSoDoAlvoCerto(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	outroDono := a.usuario(t)

	comCertificado, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	semCertificado, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	alheio, err := a.svc.Create(ctx, outroDono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	observedAt := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	a.gravarValidade(t, comCertificado.ID, notAfter, observedAt)
	a.gravarValidade(t, alheio.ID, notAfter.AddDate(1, 0, 0), observedAt)

	lista, err := a.svc.List(ctx, dono, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 2 {
		t.Fatalf("List devolveu %d alvos, queria 2", len(lista))
	}
	for _, alvo := range lista {
		switch alvo.ID {
		case comCertificado.ID:
			if alvo.TLS == nil {
				t.Fatal("alvo com certificado lido veio sem TLS")
			}
			if !alvo.TLS.NotAfter.Equal(notAfter) || !alvo.TLS.ObservedAt.Equal(observedAt) {
				t.Errorf("TLS = %+v, queria not_after %v e observed_at %v", alvo.TLS, notAfter, observedAt)
			}
			if alvo.TLS.NotAfter.Location() != time.UTC || alvo.TLS.ObservedAt.Location() != time.UTC {
				t.Errorf("TLS fora de UTC: %v e %v", alvo.TLS.NotAfter.Location(), alvo.TLS.ObservedAt.Location())
			}
		case semCertificado.ID:
			if alvo.TLS != nil {
				t.Errorf("alvo sem certificado veio com TLS = %+v", alvo.TLS)
			}
		default:
			t.Errorf("a lista trouxe o alvo %s, que não é do dono", alvo.ID)
		}
	}
}

// PATCH com tags apaga as antigas e grava as novas na mesma transação; sem
// tags deixa a lista vazia, nunca nil. Antes, nenhum teste do pacote lia tags
// gravadas nem as trocava.
func TestUpdateSetTagsSubstituiAsTags(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)

	in := entrada()
	in.Tags = []string{"prod"}
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}
	lidas := func(passo string) []string {
		t.Helper()
		got, err := a.svc.Get(ctx, dono, alvo.ID)
		if err != nil {
			t.Fatalf("%s: %v", passo, err)
		}
		return got.Tags
	}
	if got := lidas("depois do Create"); !slices.Equal(got, []string{"prod"}) {
		t.Fatalf("tags depois do Create = %v, queria [prod]", got)
	}

	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{SetTags: true, Tags: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if got := lidas("depois de trocar"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("tags depois de trocar = %v, queria [a b]", got)
	}

	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{SetTags: true, Tags: nil}); err != nil {
		t.Fatal(err)
	}
	got := lidas("depois de limpar")
	if got == nil || len(got) != 0 {
		t.Errorf("tags depois de limpar = %#v, queria []string{} (não nil)", got)
	}
}
