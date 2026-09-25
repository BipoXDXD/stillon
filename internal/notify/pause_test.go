package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/incident"
)

// Pausar e retomar canal (Plano 9, §3.3): SetEnabled muda só o enabled, e o
// canal volta no formato da listagem.
func TestSetEnabledPausesAndResumes(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}

	pausado, err := b.svc.SetEnabled(ctx, b.dono, c.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if pausado.ID != c.ID || pausado.Enabled || pausado.Status != StatusVerified || pausado.Hint != "webhook …6789" {
		t.Errorf("canal pausado = %+v", pausado)
	}
	if cs := b.listar(t, b.dono); len(cs) != 1 || cs[0].Enabled {
		t.Errorf("a listagem não reflete a pausa: %+v", cs)
	}

	// Pausar o que já está pausado não é erro nem muda nada (idempotente).
	deNovo, err := b.svc.SetEnabled(ctx, b.dono, c.ID, false)
	if err != nil || deNovo.Enabled {
		t.Errorf("pausar de novo = %+v, %v", deNovo, err)
	}

	retomado, err := b.svc.SetEnabled(ctx, b.dono, c.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !retomado.Enabled {
		t.Errorf("canal retomado = %+v", retomado)
	}
	if cs := b.listar(t, b.dono); len(cs) != 1 || !cs[0].Enabled {
		t.Errorf("a listagem não reflete a retomada: %+v", cs)
	}
}

// Canal alheio ou inexistente é ErrNotFound, e o canal alheio não muda. A
// perna positiva é o dono pausando o mesmo canal.
func TestSetEnabledOnAnotherUsersChannelIsNotFound(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.SetEnabled(ctx, b.outro, c.ID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("pausar canal alheio deu %v, queria ErrNotFound", err)
	}
	if _, err := b.svc.SetEnabled(ctx, b.dono, uuid.Must(uuid.NewV7()), false); !errors.Is(err, ErrNotFound) {
		t.Errorf("pausar canal inexistente deu %v, queria ErrNotFound", err)
	}
	if cs := b.listar(t, b.dono); len(cs) != 1 || !cs[0].Enabled {
		t.Fatalf("a tentativa do outro usuário mexeu no canal: %+v", cs)
	}
	if _, err := b.svc.SetEnabled(ctx, b.dono, c.ID, false); err != nil {
		t.Errorf("o dono pausando o próprio canal deu %v", err)
	}
}

// Pausado pelo serviço, o aviso pendente é cancelado no envio; retomado, o
// seguinte sai. Retomar não reenvia o que foi cancelado (spec §3.3).
func TestPausedChannelDropsAndResumedChannelSends(t *testing.T) {
	b := novaBancada(t, nil)
	svc := NewChannelService(b.pool, b.box, b.clk, nil, nil, "")
	ctx := context.Background()

	abertura := b.enfileirar(t, incident.EventOpened)
	if _, err := svc.SetEnabled(ctx, b.dono, b.canalID, false); err != nil {
		t.Fatal(err)
	}
	b.despachar(t, 1)
	if b.canal.quantos() != 0 || b.estado(t, abertura).Status != "cancelled" {
		t.Fatalf("canal pausado: %d envios, status %s", b.canal.quantos(), b.estado(t, abertura).Status)
	}

	if _, err := svc.SetEnabled(ctx, b.dono, b.canalID, true); err != nil {
		t.Fatal(err)
	}
	fechamento := b.enfileirar(t, incident.EventResolved)
	b.despachar(t, 1)
	if b.canal.quantos() != 1 || b.estado(t, fechamento).Status != "sent" {
		t.Errorf("canal retomado: %d envios, status %s", b.canal.quantos(), b.estado(t, fechamento).Status)
	}
	if b.estado(t, abertura).Status != "cancelled" {
		t.Error("retomar reenviou o aviso cancelado durante a pausa")
	}
}
