package targets

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// HostDaRegra expõe a chave do host da regra de conteúdo.
var HostDaRegra = hostDaRegra

// escopoDe é o escopo de userID no Projeto padrão dele, criado na primeira
// chamada (como faz a rota antiga). O escopo sai de storage.InProjectTx no
// pool do teste, com o papel lido do banco, e não montado à mão.
func escopoDe(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	projeto := testdb.ProjetoPadrao(t, userID)
	var sc tenancy.ProjectScope
	err := storage.InProjectTx(context.Background(), pool, userID, projeto, func(_ *db.Queries, lido tenancy.ProjectScope) error {
		sc = lido
		return nil
	})
	if err != nil {
		t.Fatalf("escopo de %s no Projeto padrão: %v", userID, err)
	}
	return sc
}

// EscopoDe expõe escopoDe aos testes do pacote targets_test.
var EscopoDe = escopoDe
