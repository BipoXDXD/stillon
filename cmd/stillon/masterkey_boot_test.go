package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Uma MASTER_KEY que não abre os destinos cifrados no banco (troca de
// chave num deploy ou restauro) recusa o boot com erro claro, em vez de
// subir e queimar cada aviso como falha definitiva. Vale para os dois
// papéis: a API também decifra (reenvio de verificação).
func TestRunRefusesAMasterKeyThatOpensNoChannel(t *testing.T) {
	for _, papel := range []config.Role{config.RoleProber, config.RoleAPI} {
		t.Run(string(papel), func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()

			// O canal foi cifrado com a chave antiga; o processo sobe com
			// a de configDeTeste.
			antiga, err := crypto.NewBox([]byte("chave-antiga-de-teste-32-bytes!!"))
			if err != nil {
				t.Fatal(err)
			}
			dono, canal := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'mk@exemplo.com', 'x')`, dono); err != nil {
				t.Fatal(err)
			}
			sealed, err := antiga.Seal([]byte("https://discord.com/api/webhooks/1/tok"),
				crypto.AAD("notif_channels", "config_enc", canal))
			if err != nil {
				t.Fatal(err)
			}
			agora := time.Now()
			err = storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
				return q.InsertChannel(ctx, db.InsertChannelParams{
					ID: canal, UserID: dono, Kind: string(notify.KindDiscord), ConfigEnc: sealed,
					VerifiedAt: &agora, CreatedAt: agora,
				})
			})
			if err != nil {
				t.Fatal(err)
			}

			cfg := configDeTeste(t, papel, testdb.DSN(t))
			cfg.HTTPAddr, cfg.ShutdownGrace = "127.0.0.1:0", time.Second
			// Com prazo: sem a checagem, run subiria e só voltaria no
			// cancelamento. O teste ficaria pendurado em vez de vermelho.
			runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			err = run(runCtx, cfg)
			if !errors.Is(err, notify.ErrMasterKey) {
				t.Fatalf("run com a MASTER_KEY errada = %v, queria ErrMasterKey", err)
			}
			// A mensagem orienta a operação sem expor a chave, nem em base64
			// nem crua, nem um trecho dela.
			msg := err.Error()
			if !strings.Contains(msg, "MASTER_KEY") {
				t.Errorf("o erro não diz qual variável conferir: %q", msg)
			}
			b64 := base64.StdEncoding.EncodeToString(chave)
			for _, segredo := range []string{b64, string(chave), b64[:8], string(chave[:8])} {
				if strings.Contains(msg, segredo) {
					t.Errorf("o erro expõe a chave: %q", msg)
				}
			}
		})
	}
}
