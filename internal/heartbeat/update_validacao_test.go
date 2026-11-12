package heartbeat_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// O PATCH entrega nome e tolerância ao Service.Update sem validar nada antes:
// a validação do nome e da tolerância mora só nele. Um patch recusado não
// grava coisa alguma, nem o campo válido que vinha junto.
func TestUpdateRejectsAnInvalidNameOrGraceAndWritesNothing(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx := t.Context()
	antes, _, err := svc.Create(ctx, a.dono, heartbeat.Input{Name: "backup", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	casos := []struct {
		nome   string
		patch  heartbeat.Patch
		campo  string
		motivo string
	}{
		{"nome vazio", heartbeat.Patch{Name: ptr("")}, "name", heartbeat.ReasonRequired},
		{"nome só de espaços", heartbeat.Patch{Name: ptr(" \t ")}, "name", heartbeat.ReasonRequired},
		{"nome com um caractere a mais que o teto", heartbeat.Patch{Name: ptr(strings.Repeat("é", heartbeat.MaxNameLength+1))}, "name", heartbeat.ReasonOutOfRange},
		{"nome com inversão bidi", heartbeat.Patch{Name: ptr("a\u202eb")}, "name", heartbeat.ReasonInvalid},
		{"tolerância zero", heartbeat.Patch{Grace: ptr(time.Duration(0))}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"tolerância negativa", heartbeat.Patch{Grace: ptr(-time.Minute)}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"tolerância um segundo abaixo do piso", heartbeat.Patch{Grace: ptr(heartbeat.MinGrace - time.Second)}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"tolerância um segundo acima do teto", heartbeat.Patch{Grace: ptr(heartbeat.MaxGrace + time.Second)}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"nome válido junto de tolerância inválida", heartbeat.Patch{Name: ptr("novo nome"), Grace: ptr(heartbeat.MinGrace - time.Second)}, "grace_sec", heartbeat.ReasonOutOfRange},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := svc.Update(ctx, a.dono, antes.ID, c.patch)
			var ve *heartbeat.ValidationError
			if !errors.As(err, &ve) || ve.Field != c.campo || ve.Reason != c.motivo {
				t.Fatalf("Update = %v; queria %s %s", err, c.campo, c.motivo)
			}
			depois, err := svc.Get(ctx, a.dono, antes.ID)
			if err != nil {
				t.Fatal(err)
			}
			if depois.Name != antes.Name || depois.Grace != antes.Grace || !depois.DeadlineAt.Equal(antes.DeadlineAt) {
				t.Errorf("depois do patch recusado: nome %q, tolerância %v, prazo %v; queria %q, %v, %v",
					depois.Name, depois.Grace, depois.DeadlineAt, antes.Name, antes.Grace, antes.DeadlineAt)
			}
		})
	}
}

// O outro lado da fronteira: o nome no teto (em caracteres, e com espaço em
// volta, que sai) e as tolerâncias exatas do piso e do teto passam.
func TestUpdateAcceptsTheNameAndGraceLimitsThemselves(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx := t.Context()
	criado, _, err := svc.Create(ctx, a.dono, heartbeat.Input{Name: "backup", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	noTeto := strings.Repeat("é", heartbeat.MaxNameLength)
	got, err := svc.Update(ctx, a.dono, criado.ID, heartbeat.Patch{Name: ptr(" " + noTeto + " ")})
	if err != nil || got.Name != noTeto {
		t.Fatalf("nome no teto: (%q, %v); queria %d caracteres sem os espaços", got.Name, err, heartbeat.MaxNameLength)
	}
	for _, grace := range []time.Duration{heartbeat.MinGrace, heartbeat.MaxGrace} {
		got, err := svc.Update(ctx, a.dono, criado.ID, heartbeat.Patch{Grace: ptr(grace)})
		if err != nil || got.Grace != grace {
			t.Errorf("tolerância %v: (%v, %v); queria aceita e gravada", grace, got.Grace, err)
		}
	}
}
