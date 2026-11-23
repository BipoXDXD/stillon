package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// TetoDeEmailsDeConta é o teto diário (dia UTC) de e-mails de conta
// (confirmação, redefinição, verificação de canal), guardado no Postgres
// (account_email_daily, migration 00047) para valer entre réplicas. Protege a
// cota do provedor de e-mail; os avisos de incidente nunca passam por ele. O
// valor zero não serve: use NewTetoDeEmailsDeConta.
type TetoDeEmailsDeConta struct {
	banco Beginner
	clk   clock.Clock
	teto  int
}

// NewTetoDeEmailsDeConta usa o relógio injetado para escolher o dia, e não o
// now() do banco: o mesmo relógio que os testes controlam. Teto menor que 1
// recusa toda reserva.
func NewTetoDeEmailsDeConta(b Beginner, clk clock.Clock, teto int) *TetoDeEmailsDeConta {
	return &TetoDeEmailsDeConta{banco: b, clk: clk, teto: teto}
}

// Reservar conta um e-mail no dia de hoje se o teto ainda não foi atingido, e
// diz se contou. A vaga não volta se o envio falhar depois: o teto é proteção
// de cota, não contabilidade.
func (t *TetoDeEmailsDeConta) Reservar(ctx context.Context) (bool, error) {
	if t.teto < 1 {
		// A reserva no SQL cria a linha do dia com 1 sem olhar o teto.
		return false, nil
	}
	err := InAccountEmailTx(ctx, t.banco, func(q *db.Queries) error {
		_, err := q.ReserveAccountEmail(ctx, db.ReserveAccountEmailParams{
			Day: t.hoje(), Teto: int32(min(t.teto, math.MaxInt32)), // #nosec G115 -- limitado a MaxInt32.
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reservar e-mail de conta: %w", err)
	}
	return true, nil
}

// EnviadosHoje diz quantos e-mails de conta foram reservados hoje.
func (t *TetoDeEmailsDeConta) EnviadosHoje(ctx context.Context) (int, error) {
	var n int32
	err := InAccountEmailTx(ctx, t.banco, func(q *db.Queries) error {
		var err error
		n, err = q.AccountEmailsSentOn(ctx, t.hoje())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("contar e-mails de conta de hoje: %w", err)
	}
	return int(n), nil
}

// hoje é a data UTC do relógio injetado. O pgx grava a data pelo ano, mês e
// dia do valor, no fuso dele: sem o UTC, um relógio em -03 só mudaria de dia
// às 03h UTC.
func (t *TetoDeEmailsDeConta) hoje() time.Time {
	ano, mes, dia := t.clk.Now().UTC().Date()
	return time.Date(ano, mes, dia, 0, 0, 0, 0, time.UTC)
}
