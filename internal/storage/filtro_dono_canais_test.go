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
// e só o project_id do WHERE separa A de B. A e B são dois projetos do MESMO
// usuário: o filtro por dono os deixaria passar.

// canalDe lê o canal no projeto dado, sem RLS.
func canalDe(t *testing.T, q *db.Queries, projeto, canal uuid.UUID) db.NotifChannel {
	t.Helper()
	c, err := q.GetChannel(context.Background(), db.GetChannelParams{ProjectID: projeto, ID: canal})
	if err != nil {
		t.Fatalf("A lendo o próprio canal: %v", err)
	}
	return c
}

func TestGetChannelFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	_, err := q.GetChannel(ctx, db.GetChannelParams{ProjectID: pb, ID: canalA})
	exigirNenhumaLinha(t, err, "GetChannel")
	if c := canalDe(t, q, pa, canalA); c.ID != canalA || c.UserID != a {
		t.Errorf("A leu o canal %s do dono %s, queria %s de %s", c.ID, c.UserID, canalA, a)
	}
}

func TestGetChannelViewFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	_, err := q.GetChannelView(ctx, db.GetChannelViewParams{ProjectID: pb, ID: canalA})
	exigirNenhumaLinha(t, err, "GetChannelView")
	v, err := q.GetChannelView(ctx, db.GetChannelViewParams{ProjectID: pa, ID: canalA})
	if err != nil {
		t.Fatalf("A lendo a vista do próprio canal: %v", err)
	}
	if v.ID != canalA {
		t.Errorf("A leu o canal %s, queria %s", v.ID, canalA)
	}
}

func TestSetChannelEnabledFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	n, err := q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{Enabled: false, ProjectID: pb, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if ligado := canalDe(t, q, pa, canalA).Enabled; n != 0 || !ligado {
		t.Errorf("B pausou o canal de A sem RLS: %d linhas, enabled = %v", n, ligado)
	}
	n, err = q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{Enabled: false, ProjectID: pa, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if ligado := canalDe(t, q, pa, canalA).Enabled; n != 1 || ligado {
		t.Errorf("A pausou o próprio canal: %d linhas, enabled = %v; queria 1 e false", n, ligado)
	}
}

func TestDeleteChannelFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)

	n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{ProjectID: pb, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("B apagou %d canais de A sem RLS, queria 0", n)
	}
	canalDe(t, q, pa, canalA)
	n, err = q.DeleteChannel(ctx, db.DeleteChannelParams{ProjectID: pa, ID: canalA})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("A apagou %d linhas do próprio canal, queria 1", n)
	}
}

func TestRenewVerificationFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, hashDe("velho"), agora.Add(time.Hour))
	q := queriesSemRLS(t)
	renovar := func(projeto uuid.UUID, token string) int64 {
		t.Helper()
		n, err := q.RenewVerification(ctx, db.RenewVerificationParams{
			TokenHash: hashDe(token), ExpiresAt: agora.Add(2 * time.Hour), ProjectID: projeto, ID: canalA,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := renovar(pb, "de-b"); n != 0 {
		t.Errorf("B renovou %d verificações do canal de A sem RLS, queria 0", n)
	}
	if h := canalDe(t, q, pa, canalA).VerifyTokenHash; !bytes.Equal(h, hashDe("velho")) {
		t.Error("o token do canal de A mudou depois da renovação de B")
	}
	if n := renovar(pa, "novo"); n != 1 {
		t.Errorf("A renovou %d verificações do próprio canal, queria 1", n)
	}
	if h := canalDe(t, q, pa, canalA).VerifyTokenHash; !bytes.Equal(h, hashDe("novo")) {
		t.Error("A renovou o próprio canal e o token não mudou")
	}
}

func TestListChannelsFiltersProjectWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	canalA := novoCanal(t, pool, a, nil, agora)
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.ListChannelsRow {
		t.Helper()
		cs, err := q.ListChannels(ctx, projeto)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}

	if deB := listar(pb); len(deB) != 0 {
		t.Errorf("B listou %d canais sem RLS, queria 0 (o canal de A vazou)", len(deB))
	}
	if deA := listar(pa); len(deA) != 1 || deA[0].ID != canalA {
		t.Errorf("A listou %v, queria só o canal %s", deA, canalA)
	}
}
