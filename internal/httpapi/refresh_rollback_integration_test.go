package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

const canarioDaRotacao = "CANARY-falha-da-rotacao"

// storeQueInterrompeARotacao usa a transação real do Postgres, e quando armado
// a rotação falha com o token já marcado e o sucessor já inserido nela. A
// transação segue sã: só o rollback impede que essas escritas fiquem.
type storeQueInterrompeARotacao struct {
	inner  auth.Store
	armado *atomic.Bool
}

func (s storeQueInterrompeARotacao) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		if s.armado.Load() {
			tx = txQueFalhaDepoisDoSucessor{Tx: tx}
		}
		return fn(ctx, tx)
	})
}

type txQueFalhaDepoisDoSucessor struct{ auth.Tx }

func (t txQueFalhaDepoisDoSucessor) InsertSession(ctx context.Context, r auth.SessionRow) error {
	if err := t.Tx.InsertSession(ctx, r); err != nil {
		return err
	}
	return errors.New(canarioDaRotacao)
}

// recusarCommitDeSessoes faz o próprio Postgres recusar o COMMIT de quem insere
// em sessions, com um trigger adiado. O papel da aplicação é dono da tabela, e
// o snapshot do testdb apaga o trigger no fim do teste. Devolve o que desliga.
func recusarCommitDeSessoes(t *testing.T, a *api) func() {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `
		CREATE FUNCTION teste_recusa_commit() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION '`+canarioDaRotacao+`'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `
		CREATE CONSTRAINT TRIGGER teste_recusa_commit AFTER INSERT ON sessions
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION teste_recusa_commit()`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := a.pool.Exec(ctx, `DROP TRIGGER teste_recusa_commit ON sessions`); err != nil {
			t.Fatal(err)
		}
	}
}

// fotoDasSessoes é a tabela inteira em texto: "como estava" vale para toda
// coluna e toda linha, inclusive uma linha nova que não devia existir.
func fotoDasSessoes(t *testing.T, a *api) string {
	t.Helper()
	var foto string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT coalesce(json_agg(s ORDER BY s.id)::text, '') FROM sessions s`).Scan(&foto); err != nil {
		t.Fatal(err)
	}
	return foto
}

func contagemDasSessoes(t *testing.T, a *api) (total, usadas, revogadas int) {
	t.Helper()
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*), count(used_at), count(revoked_at) FROM sessions WHERE user_id = $1`, a.userID).
		Scan(&total, &usadas, &revogadas); err != nil {
		t.Fatal(err)
	}
	return total, usadas, revogadas
}

// provarRollbackDaRotacao exige que a falha dentro da transação não deixe nada
// no banco. A segunda metade é a consequência que importa ao dono: com used_at
// gravado, o mesmo cookie viraria reúso e a família inteira cairia.
func provarRollbackDaRotacao(t *testing.T, a *api, armar func() (desarmar func())) {
	t.Helper()
	c := clientWithJar(t)
	entrada := login(t, a, c, testEmail, testPass)
	entrada.Body.Close()
	cookie := refreshCookie(entrada)
	if entrada.StatusCode != http.StatusOK || cookie == nil {
		t.Fatalf("login devolveu %d, cookie %+v", entrada.StatusCode, cookie)
	}
	antes := fotoDasSessoes(t, a)
	desarmar := armar()

	corpo := conferirErroInternoGenerico(t, post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil))
	if strings.Contains(string(corpo), canarioDaRotacao) {
		t.Errorf("o corpo trouxe o erro real: %s", corpo)
	}
	if depois := fotoDasSessoes(t, a); depois != antes {
		t.Errorf("a rotação que falhou deixou rastro em sessions:\nantes:  %s\ndepois: %s", antes, depois)
	}
	if total, usadas, revogadas := contagemDasSessoes(t, a); total != 1 || usadas != 0 || revogadas != 0 {
		t.Errorf("%d sessões, %d usadas, %d revogadas; queria 1, 0 e 0", total, usadas, revogadas)
	}

	desarmar()
	renovado := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	renovado.Body.Close()
	if renovado.StatusCode != http.StatusOK {
		t.Fatalf("o mesmo cookie com o banco de volta: %d, queria 200", renovado.StatusCode)
	}
	if novo := refreshCookie(renovado); novo == nil || novo.Value == cookie.Value {
		t.Errorf("cookie depois da renovação: %+v, queria um token novo", novo)
	}
	if total, usadas, revogadas := contagemDasSessoes(t, a); total != 2 || usadas != 1 || revogadas != 0 {
		t.Errorf("depois da renovação: %d sessões, %d usadas, %d revogadas; queria 2, 1 e 0", total, usadas, revogadas)
	}
	if n := eventosDe(t, a, "session.reuse_detected"); n != 0 {
		t.Errorf("%d eventos session.reuse_detected, queria 0", n)
	}
}

func TestRefreshFailureAfterWritingRollsBackTheRotation(t *testing.T) {
	armado := &atomic.Bool{}
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Sessions = auth.NewManager(storeQueInterrompeARotacao{inner: d.Store, armado: armado}, d.Clock, testIPHashKey)
	})

	provarRollbackDaRotacao(t, a, func() func() {
		armado.Store(true)
		return func() { armado.Store(false) }
	})
}

func TestRefreshRefusedAtCommitLeavesTheSessionAsItWas(t *testing.T) {
	a := newAPI(t)

	provarRollbackDaRotacao(t, a, func() func() { return recusarCommitDeSessoes(t, a) })
}
