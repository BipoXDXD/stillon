package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Motivos de recusa de TetoDeEmailsDeConta.Reservar.
const (
	// RecusaConta: a conta que causou o envio já gastou o teto dela hoje.
	RecusaConta = "conta"
	// RecusaTeto: o teto global do dia já foi atingido.
	RecusaTeto = "teto"
)

// errTetoGlobal desfaz a transação da reserva quando o teto global recusa,
// para a vaga da conta voltar. Não sai de Reservar.
var errTetoGlobal = errors.New("teto global de e-mails de conta atingido")

// TetoDeEmailsDeConta é o teto diário (dia UTC) de e-mails de conta
// (confirmação, redefinição, verificação de canal), guardado no Postgres
// (account_email_daily, migration 00047) para valer entre réplicas, e o teto
// diário por conta (account_email_daily_by_user, migration 00053). Protege a
// cota e a reputação do provedor de e-mail; os avisos de incidente nunca
// passam por ele. O valor zero não serve: use NewTetoDeEmailsDeConta.
type TetoDeEmailsDeConta struct {
	banco        Beginner
	clk          clock.Clock
	teto         int
	tetoPorConta int
}

// NewTetoDeEmailsDeConta usa o relógio injetado para escolher o dia, e não o
// now() do banco: o mesmo relógio que os testes controlam. Teto menor que 1
// recusa toda reserva; tetoPorConta menor que 1, toda reserva com conta.
func NewTetoDeEmailsDeConta(b Beginner, clk clock.Clock, teto, tetoPorConta int) *TetoDeEmailsDeConta {
	return &TetoDeEmailsDeConta{banco: b, clk: clk, teto: teto, tetoPorConta: tetoPorConta}
}

// Reservar conta um e-mail de conta para hoje. Com conta != uuid.Nil, reserva
// antes uma vaga do teto da conta, na MESMA transação; se o teto global
// recusar, a vaga da conta volta (rollback). recusa: "" liberado, RecusaConta
// ou RecusaTeto. A vaga não volta se o envio falhar depois: o teto é proteção
// de cota, não contabilidade.
func (t *TetoDeEmailsDeConta) Reservar(ctx context.Context, conta uuid.UUID) (recusa string, err error) {
	// A reserva no SQL cria a linha do dia com 1 sem olhar o teto.
	if t.teto < 1 {
		return RecusaTeto, nil
	}
	if conta != uuid.Nil && t.tetoPorConta < 1 {
		return RecusaConta, nil
	}
	hoje := t.hoje()
	err = InAccountEmailTx(ctx, t.banco, func(q *db.Queries) error {
		// A conta antes do global, sempre nessa ordem: duas reservas nunca
		// esperam uma pela outra em ordem trocada.
		if conta != uuid.Nil {
			_, err := q.ReserveAccountEmailForUser(ctx, db.ReserveAccountEmailForUserParams{
				UserID: conta, Day: hoje, Teto: limiteInt32(t.tetoPorConta),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				recusa = RecusaConta
				return nil // nada gravado na conta; o global nem é tocado
			}
			if err != nil {
				return err
			}
		}
		_, err := q.ReserveAccountEmail(ctx, db.ReserveAccountEmailParams{
			Day: hoje, Teto: limiteInt32(t.teto),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errTetoGlobal
		}
		return err
	})
	if errors.Is(err, errTetoGlobal) {
		return RecusaTeto, nil
	}
	if err != nil {
		return "", fmt.Errorf("reservar e-mail de conta: %w", err)
	}
	return recusa, nil
}

// EnviadosHojePelaConta diz quantos e-mails de conta a conta reservou hoje.
func (t *TetoDeEmailsDeConta) EnviadosHojePelaConta(ctx context.Context, conta uuid.UUID) (int, error) {
	var n int32
	err := InAccountEmailTx(ctx, t.banco, func(q *db.Queries) error {
		var err error
		n, err = q.AccountEmailsSentByUserOn(ctx, db.AccountEmailsSentByUserOnParams{
			UserID: conta, Day: t.hoje(),
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("contar e-mails de conta de hoje pela conta: %w", err)
	}
	return int(n), nil
}

// limiteInt32 corta o teto em MaxInt32, o maior que a coluna aceita.
func limiteInt32(n int) int32 {
	return int32(min(n, math.MaxInt32)) // #nosec G115 -- limitado a MaxInt32.
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
