package notify

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Um destino que não abre (MASTER_KEY errada num deploy ou num restauro, ou
// linha corrompida) é tratado como falha definitiva e auditada do canal
// dono dele, sem envio, e não derruba nem atrasa os outros canais do mesmo
// lote. O teste prende o desfecho de hoje: se a decisão mudar (por exemplo,
// para retentar com backoff), é este teste que muda junto, de propósito.
func TestUnreadableDestinationFailsAloneAndTheOtherChannelStillSends(t *testing.T) {
	outraChave, err := crypto.NewBox([]byte("outra-chave-de-teste-de-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		nome string
		// ilegivel devolve o config_enc do canal que não abre.
		ilegivel func(canal uuid.UUID) ([]byte, error)
	}{
		{"selado com outra chave", func(canal uuid.UUID) ([]byte, error) {
			return sealConfig(outraChave, canal, webhookDeTeste)
		}},
		{"bytes que nem são um selo", func(uuid.UUID) ([]byte, error) { return []byte{0x00, 0xff}, nil }},
	} {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, func(p *db.InsertChannelParams) {
				sealed, err := c.ilegivel(p.ID)
				if err != nil {
					t.Fatal(err)
				}
				p.ConfigEnc = sealed
			})
			saudavel := b.novoCanalVerificado(t)
			quebrada := b.enfileirar(t, incident.EventOpened)
			entregue := b.enfileirarPara(t, saudavel, incident.EventOpened)

			b.despachar(t, 2)

			if l := b.estado(t, quebrada); l.Status != "failed" || l.Erro == nil || *l.Erro != "invalid_channel" || l.Enviada != nil {
				t.Errorf("notificação do canal ilegível = %+v; queria failed/invalid_channel sem envio", l)
			}
			if b.auditorias(t) != 1 {
				t.Error("falha definitiva do canal ilegível não foi auditada")
			}
			if saude := b.canalPorID(t, b.canalID); saude.Status != StatusFailing || saude.LastError != ReasonInvalidChannel {
				t.Errorf("saúde do canal ilegível = %+v; queria failing/invalid_channel", saude)
			}
			if saude := b.canalPorID(t, saudavel); saude.Status != StatusVerified || !saude.LastFailureAt.IsZero() {
				t.Errorf("saúde do canal sadio = %+v; queria verified, sem falha", saude)
			}
			if l := b.estado(t, entregue); l.Status != "sent" || l.Enviada == nil {
				t.Errorf("notificação do canal sadio = %+v; queria sent", l)
			}
			if n := b.canal.quantos(); n != 1 {
				t.Errorf("%d envios, queria só o do canal sadio", n)
			}
			if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "failed")); v != 1 {
				t.Errorf("notify_total{discord,failed} = %v, queria 1", v)
			}
			if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "sent")); v != 1 {
				t.Errorf("notify_total{discord,sent} = %v, queria 1", v)
			}
		})
	}
}

// canalPorID é o canal como o dono o vê em GET /channels, entre vários.
func (b *bancada) canalPorID(t *testing.T, id uuid.UUID) Channel {
	t.Helper()
	svc := NewChannelService(b.pool, b.box, b.clk, nil, nil, "https://stillon.example")
	cs, err := svc.List(context.Background(), b.dono)
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("canal %s fora da listagem %+v", id, cs)
	return Channel{}
}

// novoCanalVerificado cria um segundo canal Discord, verificado e legível, do
// mesmo dono da bancada.
func (b *bancada) novoCanalVerificado(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(b.box, id, webhookDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	agora := b.clk.Now()
	err = storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: id, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// enfileirarPara enfileira o evento do incidente da bancada para o canal dado.
func (b *bancada) enfileirarPara(t *testing.T, canal uuid.UUID, evento string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: b.incidente, ChannelID: canal, Event: evento, Now: b.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
