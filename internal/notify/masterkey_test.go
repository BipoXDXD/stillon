package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

var outraChave = []byte("outra-chave-mestra-32-bytes!!!!!")

func caixa(t *testing.T, chave []byte) *crypto.Box {
	t.Helper()
	box, err := crypto.NewBox(chave)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// gravarCanais cria um dono e um canal por selo; selar recebe o id do canal
// e devolve o config_enc gravado.
func gravarCanais(t *testing.T, pool *pgxpool.Pool, selos ...func(uuid.UUID) []byte) {
	t.Helper()
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	agora := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		for i, selar := range selos {
			id := uuid.Must(uuid.NewV7())
			if err := q.InsertChannel(ctx, db.InsertChannelParams{
				ID: id, UserID: dono, Kind: string(KindDiscord), ConfigEnc: selar(id),
				VerifiedAt: &agora, CreatedAt: agora.Add(time.Duration(i) * time.Minute),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seladoCom(t *testing.T, box *crypto.Box) func(uuid.UUID) []byte {
	return func(id uuid.UUID) []byte {
		sealed, err := sealConfig(box, id, webhookDeTeste)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
}

func TestCheckMasterKeyRefusesAKeyThatOpensNoChannel(t *testing.T) {
	pool := testdb.New(t)
	gravarCanais(t, pool, seladoCom(t, caixa(t, outraChave)), seladoCom(t, caixa(t, outraChave)))

	err := CheckMasterKey(context.Background(), pool, caixa(t, chaveDeTeste))
	if !errors.Is(err, ErrMasterKey) {
		t.Fatalf("CheckMasterKey com a chave errada = %v, queria ErrMasterKey", err)
	}
}

func TestCheckMasterKeyAcceptsTheRightKey(t *testing.T) {
	pool := testdb.New(t)
	box := caixa(t, chaveDeTeste)
	gravarCanais(t, pool, seladoCom(t, box))

	if err := CheckMasterKey(context.Background(), pool, box); err != nil {
		t.Fatalf("CheckMasterKey com a chave certa = %v", err)
	}
}

// Banco novo não tem o que conferir: o primeiro boot sobe.
func TestCheckMasterKeyAcceptsAnEmptyDatabase(t *testing.T) {
	pool := testdb.New(t)
	if err := CheckMasterKey(context.Background(), pool, caixa(t, chaveDeTeste)); err != nil {
		t.Fatalf("CheckMasterKey sem canais = %v", err)
	}
}

// Uma linha corrompida não é chave errada: basta um selo recente abrir para
// a chave valer, e o canal ruim continua falhando sozinho no envio.
func TestCheckMasterKeyToleratesOneCorruptedChannel(t *testing.T) {
	pool := testdb.New(t)
	box := caixa(t, chaveDeTeste)
	lixo := func(uuid.UUID) []byte { return []byte("nem-e-um-selo") }
	// O corrompido é o mais recente: a conferência não pode parar nele.
	gravarCanais(t, pool, seladoCom(t, box), lixo)

	if err := CheckMasterKey(context.Background(), pool, box); err != nil {
		t.Fatalf("CheckMasterKey com um canal corrompido e um sadio = %v", err)
	}
}
