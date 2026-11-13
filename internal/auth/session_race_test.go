package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// storeQuePausa segura a transação logo depois de LockSessionByHash, com a
// linha viva travada: é o instante em que uma revogação concorrente precisa
// esperar a rotação terminar.
type storeQuePausa struct {
	inner  auth.Store
	travou chan struct{}
	solta  chan struct{}
}

func (s storeQuePausa) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		return fn(ctx, txQuePausa{Tx: tx, travou: s.travou, solta: s.solta})
	})
}

type txQuePausa struct {
	auth.Tx
	travou chan struct{}
	solta  chan struct{}
}

func (t txQuePausa) LockSessionByHash(ctx context.Context, hash []byte) (auth.SessionRow, error) {
	r, err := t.Tx.LockSessionByHash(ctx, hash)
	close(t.travou)
	<-t.solta
	return r, err
}

// Revogar uma família enquanto ela é rotacionada não pode deixar o sucessor
// vivo.
//
// Em READ COMMITTED, o UPDATE que revoga espera a linha viva travada pela
// rotação; a rotação insere o sucessor e comita; o UPDATE revoga a linha que
// esperou, mas não enxerga o sucessor, que entrou depois do snapshot dele. A
// família "revogada" continuava viva. Os dois caminhos que revogam família
// hoje são a detecção de reúso e o logout com um refresh antigo — o caso em
// que o ladrão está rotacionando a cadeia viva.
func TestRevokingDuringRotationLeavesNothingAlive(t *testing.T) {
	casos := []struct {
		nome    string
		revogar func(ctx context.Context, mgr *auth.Manager, antigo string) error
	}{
		{"detecção de reúso", func(ctx context.Context, mgr *auth.Manager, antigo string) error {
			_, _, err := mgr.Rotate(ctx, antigo, meta)
			if errors.Is(err, auth.ErrTokenReuse) {
				return nil
			}
			return err
		}},
		{"logout com refresh antigo", func(ctx context.Context, mgr *auth.Manager, antigo string) error {
			return mgr.Revoke(ctx, antigo)
		}},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			mgr, pool, clk, userID := setup(t)
			ctx := context.Background()

			antigo, _, err := mgr.Issue(ctx, userID, carimbo, meta)
			if err != nil {
				t.Fatal(err)
			}
			vivo, s, err := mgr.Rotate(ctx, antigo, meta)
			if err != nil {
				t.Fatal(err)
			}

			travou, solta := make(chan struct{}), make(chan struct{})
			// soltar fecha solta uma vez só, e o t.Cleanup a chama se o teste
			// morrer antes (o t.Fatal logo abaixo): sem isso, a
			// rotação ficaria parada com as travas na mão, e o fechamento do
			// pool esperaria por ela até o timeout do go test.
			var umaVez sync.Once
			soltar := func() { umaVez.Do(func() { close(solta) }) }
			t.Cleanup(soltar)
			lento := auth.NewManager(
				storeQuePausa{inner: auth.NewPGStore(pool), travou: travou, solta: solta}, clk, ipKey)

			rotacao := make(chan error, 1)
			go func() {
				_, _, err := lento.Rotate(ctx, vivo, meta)
				rotacao <- err
			}()
			<-travou

			revogacao := make(chan error, 1)
			go func() { revogacao <- caso.revogar(ctx, mgr, antigo) }()
			// Só com a revogação no ponto de conflito soltar a rotação exercita
			// a corrida.
			if !testdb.EsperarBloqueados(t, pool, 1, revogacao) {
				t.Fatal("a revogação terminou sem esperar a rotação: o teste não exercitou a corrida")
			}
			soltar()

			if err := <-rotacao; err != nil {
				t.Fatalf("a rotação concorrente falhou: %v", err)
			}
			if err := <-revogacao; err != nil {
				t.Fatalf("a revogação falhou: %v", err)
			}

			var vivas int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM sessions
				  WHERE family_id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
				s.FamilyID).Scan(&vivas); err != nil {
				t.Fatal(err)
			}
			if vivas != 0 {
				t.Errorf("%d linha(s) viva(s) na família revogada: o sucessor da rotação concorrente escapou", vivas)
			}
		})
	}
}
