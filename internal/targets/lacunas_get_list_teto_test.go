package targets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// Fora de [1, MaxPageSize] o List volta ao tamanho padrão; dentro, respeita o
// pedido. Com 101 alvos gravados, cada tamanho de página se distingue.
func TestListLimiteForaDaFaixaVoltaAoPadrao(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, 101)

	casos := []struct {
		pedido int32
		quer   int
	}{
		{-1, 50},
		{0, 50},
		{1, 1},
		{100, 100},
		{101, 50},
	}
	for _, c := range casos {
		lista, err := a.svc.List(context.Background(), dono, nil, c.pedido)
		if err != nil {
			t.Fatalf("List com limite %d: %v", c.pedido, err)
		}
		if len(lista) != c.quer {
			t.Errorf("List com limite %d devolveu %d alvos, queria %d", c.pedido, len(lista), c.quer)
		}
	}
}

// O List de quem não é dono não traz nada, nem com o cursor apontando para
// depois de um alvo do dono; o dono continua vendo os dele.
func TestListDoIntrusoNaoTrazAlvosDoDono(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, intruso := a.usuario(t), a.usuario(t)
	primeiro, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	lista, err := a.svc.List(ctx, intruso, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if lista == nil || len(lista) != 0 {
		t.Errorf("List do intruso = %v, queria lista vazia", lista)
	}
	cursor := segundo.ID
	lista, err = a.svc.List(ctx, intruso, &cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 0 {
		t.Errorf("List do intruso com cursor devolveu %d alvos, queria 0", len(lista))
	}

	doDono, err := a.svc.List(ctx, dono, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(doDono) != 2 || doDono[0].ID != segundo.ID || doDono[1].ID != primeiro.ID {
		t.Errorf("List do dono = %v, queria os dois alvos dele, o mais novo primeiro", doDono)
	}
}

// O PATCH que põe regra num host respeita o teto configurado na borda: com
// teto 2 e uma regra já no host, a segunda entra; com duas, a terceira é
// recusada sem gravar.
func TestPatchPondoRegraRespeitaOTetoConfiguradoNaBorda(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	svc := targets.NewService(a.pool, caixa(t), a.clk, targets.ComTetoDeRegrasPorHost(2))
	a.encherComRegra(t, dono, "https://example.com", 1, true)
	x, err := svc.Create(ctx, dono, semRegra("https://example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := svc.Create(ctx, dono, semRegra("https://example.com/y"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Update(ctx, dono, x.ID, targets.Patch{SetContentRule: true, ContentRule: regraOK()}); err != nil {
		t.Fatalf("a 2ª regra no host com teto 2 foi recusada: %v", err)
	}
	if _, err := svc.Update(ctx, dono, y.ID, targets.Patch{SetContentRule: true, ContentRule: regraOK()}); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("a 3ª regra no host com teto 2 deu %v, queria ErrContentHostLimit", err)
	}
	if n := a.regrasDoDono(t, dono); n != 2 {
		t.Errorf("regras do dono = %d, queria 2", n)
	}
}
