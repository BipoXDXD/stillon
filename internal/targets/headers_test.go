package targets_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

var chaveDeTeste = []byte("chave-mestra-de-teste-32-bytes!!")

func caixa(t *testing.T) *crypto.Box {
	t.Helper()
	b, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHeadersRoundTripAndHidePlaintext(t *testing.T) {
	box := caixa(t)
	id := uuid.Must(uuid.NewV7())
	h := map[string]string{"Authorization": "Bearer segredo-do-cliente"}

	sealed, err := targets.SealHeaders(box, id, h)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("segredo-do-cliente")) {
		t.Fatal("o valor do header aparece em claro no ciphertext")
	}
	back, err := targets.OpenHeaders(box, id, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if back["Authorization"] != "Bearer segredo-do-cliente" {
		t.Errorf("volta = %v", back)
	}
}

// AAD amarra o ciphertext à linha (spec §5.4): copiar headers_enc de um alvo
// para outro não pode entregar os headers do primeiro ao sondador do segundo.
func TestHeadersAreBoundToTheirRow(t *testing.T) {
	box := caixa(t)
	sealed, err := targets.SealHeaders(box, uuid.Must(uuid.NewV7()), map[string]string{"X-A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := targets.OpenHeaders(box, uuid.Must(uuid.NewV7()), sealed); err == nil {
		t.Error("headers de um alvo abriram com o id de outro")
	}
}

func TestEmptyHeadersAreStoredAsNull(t *testing.T) {
	sealed, err := targets.SealHeaders(caixa(t), uuid.Must(uuid.NewV7()), nil)
	if err != nil || sealed != nil {
		t.Errorf("sealed = %v, err = %v; queria nil, nil", sealed, err)
	}
	h, err := targets.OpenHeaders(caixa(t), uuid.Must(uuid.NewV7()), nil)
	if err != nil || h != nil {
		t.Errorf("h = %v, err = %v; queria nil, nil", h, err)
	}
}
