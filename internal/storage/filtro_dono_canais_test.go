package storage_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para notif_channels: superusuário, sem RLS,
// e só o user_id do WHERE separa A de B.

// canalDe lê o canal como o dono, sem RLS.
func canalDe(t *testing.T, q *db.Queries, dono, canal uuid.UUID) db.NotifChannel {
	t.Helper()
	c, err := q.GetChannel(context.Background(), db.GetChannelParams{UserID: dono, ID: canal})
	if err != nil {
		t.Fatalf("A lendo o próprio canal: %v", err)
	}
	return c
}

func TestGetChannelFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	_, err := q.GetChannel(ctx, db.GetChannelParams{UserID: b, ID: canalA})
	exigirNenhumaLinha(t, err, "GetChannel")
	if c := canalDe(t, q, a, canalA); c.ID != canalA || c.UserID != a {
		t.Errorf("A leu o canal %s do dono %s, queria %s de %s", c.ID, c.UserID, canalA, a)
	}
}

func TestGetChannelViewFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	_, err := q.GetChannelView(ctx, db.GetChannelViewParams{UserID: b, ID: canalA})
	exigirNenhumaLinha(t, err, "GetChannelView")
	v, err := q.GetChannelView(ctx, db.GetChannelViewParams{UserID: a, ID: canalA})
	if err != nil {
		t.Fatalf("A lendo a vista do próprio canal: %v", err)
	}
	if v.ID != canalA {
		t.Errorf("A leu o canal %s, queria %s", v.ID, canalA)
	}
}

func TestSetChannelEnabledFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	n, err := q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{Enabled: false, UserID: b, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || !canalDe(t, q, a, canalA).Enabled {
		t.Errorf("B pausou o canal de A sem RLS: %d linhas, enabled = %v", n, canalDe(t, q, a, canalA).Enabled)
	}
	n, err = q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{Enabled: false, UserID: a, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || canalDe(t, q, a, canalA).Enabled {
		t.Errorf("A pausou o próprio canal: %d linhas, enabled = %v; queria 1 e false", n, canalDe(t, q, a, canalA).Enabled)
	}
}

func TestDeleteChannelFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: b, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("B apagou %d canais de A sem RLS, queria 0", n)
	}
	canalDe(t, q, a, canalA)
	n, err = q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: a, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("A apagou %d linhas do próprio canal, queria 1", n)
	}
}

func TestRenewVerificationFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, hashDe("velho"), agora.Add(time.Hour))
	q := queriesSemRLS(t)
	renovar := func(dono uuid.UUID, token string) int64 {
		t.Helper()
		n, err := q.RenewVerification(ctx, db.RenewVerificationParams{
			TokenHash: hashDe(token), ExpiresAt: agora.Add(2 * time.Hour), UserID: dono, ID: canalA,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := renovar(b, "de-b"); n != 0 {
		t.Errorf("B renovou %d verificações do canal de A sem RLS, queria 0", n)
	}
	if h := canalDe(t, q, a, canalA).VerifyTokenHash; !bytes.Equal(h, hashDe("velho")) {
		t.Error("o token do canal de A mudou depois da renovação de B")
	}
	if n := renovar(a, "novo"); n != 1 {
		t.Errorf("A renovou %d verificações do próprio canal, queria 1", n)
	}
	if h := canalDe(t, q, a, canalA).VerifyTokenHash; !bytes.Equal(h, hashDe("novo")) {
		t.Error("A renovou o próprio canal e o token não mudou")
	}
}
