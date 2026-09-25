package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// nonceSize é o tamanho do nonce do GCM, prefixado pelo Seal.
const nonceSize = 12

func testKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{7}, 32)
}

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := crypto.NewBox(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := crypto.AAD("targets", "secret_enc", uuid.New())
	msg := []byte("Authorization: Bearer abc123")

	sealed, err := box.Seal(msg, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, msg) {
		t.Error("o ciphertext contém o texto em claro")
	}

	opened, err := box.Open(sealed, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened.Reveal(), msg) {
		t.Errorf("Open devolveu %q, queria %q", opened.Reveal(), msg)
	}
}

// O AAD amarra o ciphertext à linha que o guarda. Sem ele, quem tem acesso de
// escrita ao banco pode copiar o segredo de um alvo para outro e fazer o
// sistema usar credenciais de terceiros.
func TestOpenRejectsCiphertextMovedToAnotherRow(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	original := crypto.AAD("targets", "secret_enc", uuid.New())
	outra := crypto.AAD("targets", "secret_enc", uuid.New())

	sealed, err := box.Seal([]byte("segredo"), original)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := box.Open(sealed, outra); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", "secret_enc", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)
	sealed[len(sealed)-1] ^= 0xFF // vira um bit do tag de autenticação

	if _, err := box.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTruncatedInput(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))

	// O caso de 20 bytes é o que tem força: passa do nonce de 12 bytes mas não
	// chega ao mínimo de 12+16. Uma checagem que esquecesse o Overhead() ainda
	// rejeitaria 3 bytes, mas mandaria 20 para o GCM e devolveria ErrAuthFailed.
	casos := map[string][]byte{
		"3 bytes, nem o nonce cabe":         {1, 2, 3},
		"20 bytes, nonce sim, tag não":      bytes.Repeat([]byte{1}, 20),
		"27 bytes, um a menos que o mínimo": bytes.Repeat([]byte{1}, 27),
	}

	for nome, sealed := range casos {
		if _, err := box.Open(sealed, nil); !errors.Is(err, crypto.ErrMalformed) {
			t.Errorf("%s: erro = %v, queria ErrMalformed", nome, err)
		}
	}
}

// O AAD só protege se cada componente entrar de fato no valor. Estas asserções
// falham se alguém deixar de concatenar a tabela, a coluna ou o id.
func TestAADIsDeterministicAndBindsEveryComponent(t *testing.T) {
	id := uuid.New()
	outraID := uuid.New()

	if !bytes.Equal(crypto.AAD("users", "totp_secret_enc", id), crypto.AAD("users", "totp_secret_enc", id)) {
		t.Error("AAD não é determinístico para a mesma tabela, coluna e id")
	}
	if bytes.Equal(crypto.AAD("users", "totp_secret_enc", id), crypto.AAD("users", "totp_secret_enc", outraID)) {
		t.Error("ids diferentes produziram o mesmo AAD: o id não está entrando no valor")
	}
	if bytes.Equal(crypto.AAD("users", "totp_secret_enc", id), crypto.AAD("targets", "totp_secret_enc", id)) {
		t.Error("tabelas diferentes produziram o mesmo AAD: a tabela não está entrando no valor")
	}
	if bytes.Equal(crypto.AAD("users", "totp_secret_enc", id), crypto.AAD("users", "secret_enc", id)) {
		t.Error("colunas diferentes produziram o mesmo AAD: a coluna não está entrando no valor")
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	outra, _ := crypto.NewBox(bytes.Repeat([]byte{9}, 32))
	aad := crypto.AAD("targets", "secret_enc", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)

	if _, err := outra.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

// Nonce repetido em GCM é catastrófico: dois ciphertexts com o mesmo nonce
// permitem recuperar o XOR dos textos em claro. Este teste é uma sentinela
// contra alguém "otimizar" o nonce para um valor fixo.
func TestSealUsesFreshNonceEveryTime(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", "secret_enc", uuid.New())
	msg := []byte("mesma mensagem")

	vistos := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sealed, err := box.Seal(msg, aad)
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(sealed[:nonceSize])
		if vistos[nonce] {
			t.Fatal("nonce repetido em 100 selagens")
		}
		vistos[nonce] = true
	}

	// Um contador por instância de Box passaria no laço acima e mesmo assim
	// reutilizaria nonces a cada reinício do processo — que é justamente
	// quando a mesma chave volta do zero. Dois Box com a mesma chave têm de
	// produzir nonces distintos.
	a, _ := crypto.NewBox(testKey(t))
	b, _ := crypto.NewBox(testKey(t))
	selaA, err := a.Seal(msg, aad)
	if err != nil {
		t.Fatal(err)
	}
	selaB, err := b.Seal(msg, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(selaA[:nonceSize], selaB[:nonceSize]) {
		t.Error("dois Box com a mesma chave começaram pelo mesmo nonce")
	}
}

func TestNewBoxRejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := crypto.NewBox(bytes.Repeat([]byte{1}, n)); err == nil {
			t.Errorf("NewBox aceitou chave de %d bytes", n)
		}
	}
}
