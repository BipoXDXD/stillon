package prober

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// enfileirar grava uma notificação por canal entregável do dono (spec §8).
// Os ids são UUIDv7 gerados aqui, como em toda a aplicação (spec §4.2); com
// no máximo notify.MaxChannels canais, um INSERT por canal é barato. O
// UNIQUE (incident, channel, event, seq), com seq 0 na abertura e no
// fechamento, torna a repetição inofensiva.
func enfileirar(ctx context.Context, q *db.Queries, dono, incidente uuid.UUID, evento string, em time.Time) error {
	canais, err := q.ListDeliverableChannels(ctx, dono)
	if err != nil {
		return fmt.Errorf("listar canais do dono: %w", err)
	}
	for _, canal := range canais {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("gerar id de notificação: %w", err)
		}
		if err := q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: evento, Now: em,
		}); err != nil {
			return fmt.Errorf("enfileirar notificação: %w", err)
		}
	}
	return nil
}

// avisarAbertura marca a abertura do incidente como avisada e enfileira o
// aviso. É a mesma regra para o incidente de um alvo (avaliar) e para o de
// um heartbeat. A idempotência é do banco (spec §8): o UPDATE só afeta a
// linha de quem chega primeiro, e quem vê 0 linhas não enfileira. Roda na
// transação de quem chama, e um erro aqui desfaz tudo o que ela gravou.
func avisarAbertura(ctx context.Context, q *db.Queries, dono, incidente uuid.UUID, em time.Time) error {
	n, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: incidente, Now: em})
	if err != nil {
		return fmt.Errorf("marcar abertura notificada: %w", err)
	}
	if n == 1 {
		return enfileirar(ctx, q, dono, incidente, incident.EventOpened, em)
	}
	return nil
}

// avisarFechamento é o par de avisarAbertura para o fechamento.
func avisarFechamento(ctx context.Context, q *db.Queries, dono, incidente uuid.UUID, em time.Time) error {
	n, err := q.MarkCloseNotified(ctx, db.MarkCloseNotifiedParams{ID: incidente, Now: em})
	if err != nil {
		return fmt.Errorf("marcar fechamento notificado: %w", err)
	}
	if n == 1 {
		return enfileirar(ctx, q, dono, incidente, incident.EventResolved, em)
	}
	return nil
}
