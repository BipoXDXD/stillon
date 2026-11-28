package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// CriarProjetoPadraoNaTx cria o Projeto padrão de userID e o vínculo de owner
// na transação de q, e devolve o id do projeto. Supõe o escopo do usuário já
// ligado (InUserTx, ou ScopeToUser na transação): projects e project_members
// estão sob FORCE RLS, e sem o escopo a gravação é recusada.
//
// É idempotente: o índice único parcial faz o INSERT ON CONFLICT não criar
// outro padrão (uma chamada concorrente espera a primeira transação e, em READ
// COMMITTED, a leitura seguinte já enxerga o projeto dela), e o vínculo também
// é ON CONFLICT. Os dois nascem juntos ou nenhum nasce: a FK adiável
// projects_titular_e_owner (00048) recusa no commit o projeto sem o vínculo
// de owner do titular.
func CriarProjetoPadraoNaTx(ctx context.Context, q *db.Queries, userID uuid.UUID, now time.Time) (uuid.UUID, error) {
	novo, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("gerar id do projeto: %w", err)
	}
	if err := q.EnsureDefaultProject(ctx, db.EnsureDefaultProjectParams{
		ID: novo, Name: tenancy.DefaultProjectName, TitularID: userID, Now: now,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("criar Projeto padrão: %w", err)
	}
	projeto, err := q.GetDefaultProjectID(ctx, userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ler Projeto padrão: %w", err)
	}
	if err := q.EnsureProjectOwner(ctx, db.EnsureProjectOwnerParams{
		ProjectID: projeto, UserID: userID, Now: now,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("vincular owner ao Projeto padrão: %w", err)
	}
	return projeto, nil
}
