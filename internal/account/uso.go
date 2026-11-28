package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// UsoDoProjeto são as contagens de um projeto de que a conta é titular.
// Membros são os vínculos além do titular; Convites, os pendentes não
// vencidos.
type UsoDoProjeto struct {
	ID                                           uuid.UUID
	Nome                                         string
	Padrao                                       bool
	Alvos, Heartbeats, Canais, Membros, Convites int64
}

// Uso é o que `stillon user usage` mostra ao operador sobre uma conta.
type Uso struct {
	Email      string
	Operador   bool
	SuspensaEm *time.Time
	// Limites são os do plano da conta, os mesmos que as criações conferem.
	Limites  tenancy.Limites
	Projetos []UsoDoProjeto // o padrão primeiro
	// EmailsHoje são os e-mails de conta que a conta causou hoje (dia UTC);
	// EmailsHojeGlobal, os de todas as contas.
	EmailsHoje       int
	EmailsHojeGlobal int
}

// ContagemDeEmailsDeConta é o que Usage lê do teto de e-mails de conta
// (*storage.TetoDeEmailsDeConta satisfaz). account não importa contaemail,
// que já importa account.
type ContagemDeEmailsDeConta interface {
	EnviadosHoje(ctx context.Context) (int, error)
	EnviadosHojePelaConta(ctx context.Context, conta uuid.UUID) (int, error)
}

// Usage devolve o uso da conta de email: plano, suspensão, as contagens de
// cada projeto de que ela é titular e os e-mails de conta do dia. Devolve
// ErrUserNotFound para e-mail sem conta. Só lê: sem trava e sem auditoria.
//
// O id vem por GetUserIDByEmail (users não tem RLS); o resto, numa transação
// no escopo da própria conta, que a função do banco exige para contar os
// projetos dela.
func (s *Service) Usage(ctx context.Context, email string, emails ContagemDeEmailsDeConta) (Uso, error) {
	normalizado := auth.NormalizeEmail(email)
	userID, err := db.New(s.pool).GetUserIDByEmail(ctx, normalizado)
	if errors.Is(err, pgx.ErrNoRows) {
		return Uso{}, ErrUserNotFound
	}
	if err != nil {
		return Uso{}, fmt.Errorf("buscar usuário: %w", err)
	}
	uso := Uso{Email: normalizado}
	agora := s.clk.Now()
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		if uso.Operador, err = q.UserIsOperator(ctx, userID); err != nil {
			return fmt.Errorf("ler se é operador: %w", err)
		}
		if uso.SuspensaEm, err = q.UserSuspension(ctx, userID); err != nil {
			return fmt.Errorf("ler a suspensão: %w", err)
		}
		linhas, err := q.TitularUsage(ctx, agora)
		if err != nil {
			return fmt.Errorf("contar o uso dos projetos: %w", err)
		}
		for _, l := range linhas {
			uso.Projetos = append(uso.Projetos, UsoDoProjeto{
				ID: l.ID, Nome: l.Name, Padrao: l.IsDefault,
				Alvos: l.Alvos, Heartbeats: l.Heartbeats, Canais: l.Canais,
				Membros: l.Membros, Convites: l.Convites,
			})
		}
		return nil
	})
	if err != nil {
		return Uso{}, err
	}
	uso.Limites = tenancy.LimitesDe(uso.Operador)
	if uso.EmailsHoje, err = emails.EnviadosHojePelaConta(ctx, userID); err != nil {
		return Uso{}, err
	}
	if uso.EmailsHojeGlobal, err = emails.EnviadosHoje(ctx); err != nil {
		return Uso{}, err
	}
	return uso, nil
}
