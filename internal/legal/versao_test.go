package legal_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/BipoXDXD/stillon/internal/legal"
)

// O painel envia a versão que leu do versao.json; o servidor só aceita a
// atual. As duas cópias precisam andar juntas.
func TestVersaoDosTermosIgualAoPainel(t *testing.T) {
	cru, err := os.ReadFile("../../web/src/features/legal/versao.json")
	if err != nil {
		t.Fatal(err)
	}
	var painel struct {
		Versao string `json:"versao"`
	}
	if err := json.Unmarshal(cru, &painel); err != nil {
		t.Fatal(err)
	}
	if painel.Versao != legal.VersaoAtual {
		t.Fatalf("versao.json do painel = %q; legal.VersaoAtual = %q", painel.Versao, legal.VersaoAtual)
	}
}
