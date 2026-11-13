package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// ErrMasterKey é a MASTER_KEY que não abre nenhum dos destinos cifrados no
// banco. A mensagem diz o que conferir e nunca traz a chave.
var ErrMasterKey = errors.New("a MASTER_KEY não abre os destinos de canal cifrados no banco; " +
	"confira se é a mesma chave que os cifrou (deploy ou restauro com outra chave?)")

// selosConferidos é quantos canais recentes a subida tenta abrir. Basta um
// abrir para a chave valer: uma linha corrompida não derruba o boot, e uma
// chave errada não abre nenhuma.
const selosConferidos = 5

// CheckMasterKey confere, na subida, que box abre os destinos de canal já
// gravados. Sem a conferência, a chave errada só aparece no envio: cada aviso
// vira falha definitiva e o reenvio de verificação responde 500. Banco sem
// canal não tem o que conferir e passa.
func CheckMasterKey(ctx context.Context, b storage.Beginner, box *crypto.Box) error {
	var selos []db.LatestChannelSealsRow
	err := storage.InProberTx(ctx, b, func(q *db.Queries) error {
		var err error
		selos, err = q.LatestChannelSeals(ctx, selosConferidos)
		return err
	})
	if err != nil {
		return fmt.Errorf("conferir a MASTER_KEY: %w", err)
	}
	if len(selos) == 0 {
		return nil
	}
	for _, s := range selos {
		if _, err := openConfig(box, s.ID, s.ConfigEnc); err == nil {
			return nil
		}
	}
	return ErrMasterKey
}
