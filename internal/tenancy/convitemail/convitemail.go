// Package convitemail entrega o e-mail do convite de projeto pelo enviador de
// e-mails de conta (fila, cotas e transporte de contaemail).
package convitemail

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Mailer implementa tenancy.InvitationMailer.
type Mailer struct {
	pool  *pgxpool.Pool
	clk   clock.Clock
	envio account.AccountMailer
	links account.Links
}

var _ tenancy.InvitationMailer = (*Mailer)(nil)

func New(pool *pgxpool.Pool, clk clock.Clock, envio account.AccountMailer, links account.Links) *Mailer {
	return &Mailer{pool: pool, clk: clk, envio: envio, links: links}
}

// SendInvitation monta o texto e enfileira. Antes das cotas e da rede, o
// trabalhador espia o convite pelo hash: revogado, trocado, aceito ou vencido
// enquanto esperava na fila, o e-mail é descartado. Não há conclusão: o
// convite não guarda o estado do envio.
func (m *Mailer) SendInvitation(ctx context.Context, e tenancy.InvitationEmail) error {
	assunto, corpo := account.EmailDeConvite(e.ProjectName, e.InviterEmail, string(e.Role), m.links.AceitarConvite(e.Token))
	hash := e.TokenHash
	return m.envio.SendAccountEmail(ctx, e.To, assunto, corpo, account.EntregaDeEmail{
		Preparar: func(ctx context.Context) (bool, error) { return m.pendente(ctx, hash) },
		Conta:    e.InviterID,
	})
}

// pendente diz se o hash ainda abre um convite pendente e não vencido.
func (m *Mailer) pendente(ctx context.Context, hash []byte) (bool, error) {
	err := storage.InInvitationTokenTx(ctx, m.pool, hash, func(q *db.Queries) error {
		_, err := q.PeekInvitation(ctx, db.PeekInvitationParams{TokenHash: hash, Now: m.clk.Now()})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
