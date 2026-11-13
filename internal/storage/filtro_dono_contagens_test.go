package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para as contagens por dono e a remoção da
// validade TLS: superusuário, sem RLS, e só o user_id do WHERE separa A de B.
// A e B têm quantidades diferentes, para que uma contagem sem filtro, que
// devolveria a soma, não acerte nenhum dos dois literais.

const (
	registrosDeA = 3
	registrosDeB = 2
)

// exigirContagem compara a contagem de um dono com o literal esperado.
func exigirContagem(t *testing.T, consulta, dono string, obtido, esperado int64, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s de %s: %v", consulta, dono, err)
	}
	if obtido != esperado {
		t.Errorf("%s de %s devolveu %d, queria %d", consulta, dono, obtido, esperado)
	}
}

// criarVarios chama criar n vezes para o mesmo dono.
func criarVarios(n int, criar func(i int)) {
	for i := range n {
		criar(i)
	}
}

func TestCountTargetsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	criarVarios(registrosDeA, func(int) { novoAlvo(t, pool, a, agora) })
	criarVarios(registrosDeB, func(int) { novoAlvo(t, pool, b, agora) })
	q := queriesSemRLS(t)

	n, err := q.CountTargets(ctx, a)
	exigirContagem(t, "CountTargets", "A", n, 3, err)
	n, err = q.CountTargets(ctx, b)
	exigirContagem(t, "CountTargets", "B", n, 2, err)
}

func TestCountChannelsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	criarVarios(registrosDeA, func(int) { novoCanal(t, pool, a, nil, agora) })
	criarVarios(registrosDeB, func(int) { novoCanal(t, pool, b, nil, agora) })
	q := queriesSemRLS(t)

	n, err := q.CountChannels(ctx, a)
	exigirContagem(t, "CountChannels", "A", n, 3, err)
	n, err = q.CountChannels(ctx, b)
	exigirContagem(t, "CountChannels", "B", n, 2, err)
}

func TestCountHeartbeatsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	heartbeatsDe(t, pool, a, "a", registrosDeA)
	heartbeatsDe(t, pool, b, "b", registrosDeB)
	q := queriesSemRLS(t)

	n, err := q.CountHeartbeats(ctx, a)
	exigirContagem(t, "CountHeartbeats", "A", n, 3, err)
	n, err = q.CountHeartbeats(ctx, b)
	exigirContagem(t, "CountHeartbeats", "B", n, 2, err)
}

// heartbeatsDe cria n heartbeats com nomes únicos: o nome também gera o hash
// do token de ping, que não pode repetir.
func heartbeatsDe(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, prefixo string, n int) {
	t.Helper()
	criarVarios(n, func(i int) { novoHeartbeat(t, pool, dono, fmt.Sprintf("hb-contagem-%s-%d", prefixo, i)) })
}

// B pede a remoção da validade TLS do alvo de A; sem RLS, só o filtro por
// dono impede o DELETE, e a releitura do banco prova que a linha ficou.
func TestDeleteTargetTLSOfOwnerFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	novaValidadeTLS(t, pool, alvoA)
	q := queriesSemRLS(t)

	if err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: b, TargetID: alvoA}); err != nil {
		t.Fatalf("B removendo a validade de A: %v", err)
	}
	v, err := q.GetTargetTLSOfOwner(ctx, db.GetTargetTLSOfOwnerParams{UserID: a, TargetID: alvoA})
	if err != nil {
		t.Fatalf("a validade de A sumiu depois do DELETE em nome de B: %v", err)
	}
	if !v.NotAfter.Equal(validadeTLS) {
		t.Errorf("a validade de A virou %v, queria %v", v.NotAfter, validadeTLS)
	}

	// Controle: em nome do dono, o mesmo DELETE remove a linha.
	if err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: a, TargetID: alvoA}); err != nil {
		t.Fatalf("A removendo a própria validade: %v", err)
	}
	_, err = q.GetTargetTLSOfOwner(ctx, db.GetTargetTLSOfOwnerParams{UserID: a, TargetID: alvoA})
	exigirNenhumaLinha(t, err, "GetTargetTLSOfOwner depois do DELETE do dono")
}
