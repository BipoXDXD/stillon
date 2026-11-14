package notify

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// O dono apaga o canal enquanto o aviso está saindo: o ON DELETE CASCADE leva
// a notificação junto, e não sobra desfecho para gravar. Isso não é erro do
// StillOn, e não pode aparecer no log como erro.
func TestChannelDeletedDuringSendIsNotLoggedAsError(t *testing.T) {
	for _, tc := range []struct {
		nome     string
		resposta error
	}{
		{"enviada", nil},
		{"falha definitiva", &SendError{Reason: ReasonRejected, Permanent: true}},
		{"retentativa", &SendError{Reason: ReasonServerError}},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			log := &logSeguro{}
			antigo := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
			t.Cleanup(func() { slog.SetDefault(antigo) })

			b := novaBancada(t, nil)
			svc := NewChannelService(b.pool, b.box, b.clk, nil, nil, "https://stillon.example")
			b.canal.respostas = []error{tc.resposta}
			b.canal.aoEnviar = func() {
				if err := svc.Delete(context.Background(), b.dono, b.canalID); err != nil {
					t.Errorf("apagar o canal no meio do envio: %v", err)
				}
			}
			b.enfileirar(t, incident.EventOpened)

			b.despachar(t, 1)
			if strings.Contains(log.String(), "level=ERROR") {
				t.Errorf("o canal apagado virou erro no log:\n%s", log)
			}
			if n := b.linhasDeSaude(t); n != 0 {
				t.Errorf("channel_health tem %d linhas de um canal apagado", n)
			}
		})
	}
}
