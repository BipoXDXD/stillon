package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// checkPadraoNaoTransfere é a CHECK da 00051 que recusa proposta no Projeto
// padrão: a segunda barreira atrás de tenancy.AuthorizeTransferProposal.
const checkPadraoNaoTransfere = "projects_padrao_nao_transfere"

// ProposeTransfer grava a proposta de titularidade a targetID, sob a trava dos
// membros, com o papel de quem pede relido sob ela. Alvo que não é membro é
// ErrTransferTarget, como o que não é owner: a titularidade só vai para outro
// owner do projeto.
func (s *Store) ProposeTransfer(ctx context.Context, actorID, projectID, targetID uuid.UUID) error {
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		estado, err := q.ProjectTransferState(ctx, projectID)
		if err != nil {
			return fmt.Errorf("ler a titularidade do projeto: %w", err)
		}
		alvo := tenancy.MemberView{UserID: targetID}
		l, err := q.GetProjectMember(ctx, db.GetProjectMemberParams{ProjectID: projectID, UserID: targetID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Sem vínculo, o alvo fica sem papel: AuthorizeTransferProposal
			// recusa depois de conferir quem pede.
		case err != nil:
			return fmt.Errorf("ler o alvo da transferência: %w", err)
		default:
			if alvo, err = membro(l.UserID, l.Email, l.Role, l.IsTitular, l.CreatedAt); err != nil {
				return err
			}
		}
		if err := tenancy.AuthorizeTransferProposal(sc, estado.TitularID, estado.IsDefault, alvo); err != nil {
			return err
		}
		if err := q.ProposeTitularTransfer(ctx, db.ProposeTitularTransferParams{ProjectID: projectID, TargetID: targetID}); err != nil {
			return fmt.Errorf("propor a transferência: %w", err)
		}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == checkPadraoNaoTransfere {
		return tenancy.ErrTransferTarget
	}
	return err
}

// CancelTransfer desfaz a proposta, sob a trava dos membros: o titular cancela
// e o pendente recusa.
func (s *Store) CancelTransfer(ctx context.Context, userID, projectID uuid.UUID) error {
	return storage.InProjectTx(ctx, s.pool, userID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		estado, err := q.ProjectTransferState(ctx, projectID)
		if err != nil {
			return fmt.Errorf("ler a titularidade do projeto: %w", err)
		}
		if err := tenancy.AuthorizeTransferCancel(sc, estado.TitularID, pendente(estado.PendingTitularID)); err != nil {
			return err
		}
		if err := q.CancelTitularTransfer(ctx, projectID); err != nil {
			return fmt.Errorf("cancelar a transferência: %w", err)
		}
		return nil
	})
}

// AcceptTransfer faz do pendente o titular. A ordem importa:
//
//  1. escopo do receptor no projeto e a trava dos membros, com o papel relido
//     (travarEReler): um rebaixamento comitado antes já apagou a proposta;
//  2. a linha do projeto travada (ProjectTransferState, FOR UPDATE): uma
//     criação em voo no projeto, que leu o titular com FOR KEY SHARE, termina
//     antes, e a contagem abaixo já vê o que ela gravou;
//  3. as travas de cota do receptor, na ordem projetos, alvos, heartbeats e
//     canais (com a linha de users antes da de canais, como em
//     notify.ChannelService): as criações dele em outros projetos esperam;
//  4. cota_do_receptor e cota_urls_com_regra_do_receptor (00051), com o
//     projeto somado, contra os tetos de hoje (o de regras por host
//     inclusive), e o teto de membros do receptor, que pode ser menor que o
//     do titular;
//  5. AcceptTitularTransfer, o único UPDATE de titular_id que o gatilho aceita.
//
// A FK adiável projects_titular_e_owner confere no commit que o titular novo
// é owner.
func (s *Store) AcceptTransfer(ctx context.Context, userID, projectID uuid.UUID, now time.Time) (tenancy.ProjectSummary, error) {
	var out tenancy.ProjectSummary
	err := storage.InProjectTx(ctx, s.pool, userID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		estado, err := q.ProjectTransferState(ctx, projectID)
		if err != nil {
			return fmt.Errorf("ler a titularidade do projeto: %w", err)
		}
		if err := tenancy.AuthorizeTransferAcceptance(sc, pendente(estado.PendingTitularID)); err != nil {
			return err
		}
		if err := travarCotasDoReceptor(ctx, q, userID); err != nil {
			return err
		}
		operador, err := q.UserIsOperator(ctx, userID)
		if err != nil {
			return fmt.Errorf("ler se quem recebe é operador: %w", err)
		}
		if err := s.conferirCotaDoReceptor(ctx, q, projectID, operador, now); err != nil {
			return err
		}
		l, err := q.AcceptTitularTransfer(ctx, db.AcceptTitularTransferParams{ProjectID: projectID, UserID: userID})
		if err != nil {
			return fmt.Errorf("aceitar a transferência: %w", err)
		}
		out = tenancy.ProjectSummary{
			ID: l.ID, Name: l.Name, IsDefault: l.IsDefault, Role: sc.Role, CreatedAt: l.CreatedAt,
			Limites: tenancy.LimitesDe(operador),
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}
	return out, nil
}

// travarCotasDoReceptor toma as travas de cota do receptor na ordem das
// criações: quem cria alvo, heartbeat ou canal em outro projeto dele espera o
// aceite, e conta depois com o projeto já somado.
func travarCotasDoReceptor(ctx context.Context, q *db.Queries, receptor uuid.UUID) error {
	if err := q.LockTitularProjects(ctx, receptor); err != nil {
		return fmt.Errorf("travar projetos de quem recebe: %w", err)
	}
	if err := q.LockTitularTargets(ctx, receptor); err != nil {
		return fmt.Errorf("travar alvos de quem recebe: %w", err)
	}
	if err := q.LockTitularHeartbeats(ctx, receptor); err != nil {
		return fmt.Errorf("travar heartbeats de quem recebe: %w", err)
	}
	if err := q.LockUser(ctx, receptor); err != nil {
		return fmt.Errorf("travar quem recebe: %w", err)
	}
	if err := q.LockTitularChannels(ctx, receptor); err != nil {
		return fmt.Errorf("travar canais de quem recebe: %w", err)
	}
	return nil
}

// conferirCotaDoReceptor compara a cota do receptor, já com o projeto somado,
// com os tetos que valem hoje (os mesmos das criações, o de regras de
// conteúdo por host inclusive, sob a trava de alvos já tomada). As contagens
// incluem o projeto: passa no limite exato e recusa acima dele. O teto de
// membros é o do receptor: os membros além do titular continuam os mesmos (o
// antigo titular fica no lugar de quem recebe), mais os convites pendentes
// válidos em now, o relógio do serviço, como em conferirTetoDeMembros.
func (s *Store) conferirCotaDoReceptor(ctx context.Context, q *db.Queries, projectID uuid.UUID, operador bool, now time.Time) error {
	c, err := q.CotaDoReceptor(ctx, projectID)
	if err != nil {
		return fmt.Errorf("contar a cota de quem recebe: %w", err)
	}
	switch {
	case c.Projetos > int64(tenancy.LimitesDe(operador).Projetos):
		return tenancy.ErrProjectLimit
	case c.Alvos > targets.MaxTargetsPerUser:
		return targets.ErrLimitReached
	case c.Heartbeats > heartbeat.MaxPerUser:
		return heartbeat.ErrLimitReached
	case c.Canais > notify.MaxChannels:
		return notify.ErrLimitReached
	}
	regras, err := q.ListReceiverContentRuleTargetURLs(ctx, projectID)
	if err != nil {
		return fmt.Errorf("listar as regras de conteúdo de quem recebe: %w", err)
	}
	urls := make([]string, len(regras))
	for i, r := range regras {
		urls[i] = r.URL
	}
	if targets.ExcedeRegrasPorHost(urls, s.tetoPorHost) {
		return targets.ErrContentHostLimit
	}
	membros, err := q.CountMembersBesidesTitular(ctx, projectID)
	if err != nil {
		return fmt.Errorf("contar membros: %w", err)
	}
	pendentes, err := q.CountPendingInvitations(ctx, db.CountPendingInvitationsParams{ProjectID: projectID, Now: now})
	if err != nil {
		return fmt.Errorf("contar convites pendentes: %w", err)
	}
	if membros+pendentes > int64(tenancy.MemberLimit(operador)) {
		return tenancy.ErrMemberLimit
	}
	return nil
}

// pendente converte a coluna anulável no uuid.Nil de "sem proposta".
func pendente(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
