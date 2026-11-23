package storage_test

import (
	"context"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para a remoção da validade TLS:
// superusuário, sem RLS, e só o project_id do WHERE separa A de B.

// O projeto B pede a remoção da validade TLS do alvo de A; sem RLS, só o filtro por
// projeto impede o DELETE, e a releitura do banco prova que a linha ficou.
func TestDeleteTargetTLSOfProjectFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	// A e B são dois projetos do MESMO usuário: só o filtro por projeto os
	// separa (o por dono deixaria passar).
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	novaValidadeTLS(t, pool, alvoA)
	q := queriesSemRLS(t)

	if err := q.DeleteTargetTLSOfProject(ctx, db.DeleteTargetTLSOfProjectParams{ProjectID: pb, TargetID: alvoA}); err != nil {
		t.Fatalf("B removendo a validade de A: %v", err)
	}
	v, err := q.GetTargetTLSOfProject(ctx, db.GetTargetTLSOfProjectParams{ProjectID: pa, TargetID: alvoA})
	if err != nil {
		t.Fatalf("a validade de A sumiu depois do DELETE em nome de B: %v", err)
	}
	if !v.NotAfter.Equal(validadeTLS) {
		t.Errorf("a validade de A virou %v, queria %v", v.NotAfter, validadeTLS)
	}

	// Controle: em nome do dono, o mesmo DELETE remove a linha.
	if err := q.DeleteTargetTLSOfProject(ctx, db.DeleteTargetTLSOfProjectParams{ProjectID: pa, TargetID: alvoA}); err != nil {
		t.Fatalf("A removendo a própria validade: %v", err)
	}
	_, err = q.GetTargetTLSOfProject(ctx, db.GetTargetTLSOfProjectParams{ProjectID: pa, TargetID: alvoA})
	exigirNenhumaLinha(t, err, "GetTargetTLSOfProject depois do DELETE do dono")
}
