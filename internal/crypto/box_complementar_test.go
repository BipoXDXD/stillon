package crypto_test

import (
	"bytes"
	"context"
	"crypto/fips140"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

func TestNewBoxRejectsAES192KeyComplementar(t *testing.T) {
	// AES aceita 24 bytes, mas o formato persistido de Box exige AES-256.
	key := mustHex(t, "000102030405060708090a0b0c0d0e0f1011121314151617")
	box, err := crypto.NewBox(key)
	if err == nil || box != nil {
		t.Fatalf("NewBox aceitou chave AES-192: box presente=%t, erro=%v", box != nil, err)
	}
}

func TestOpenReadsPublishedAES256GCMEnvelopeComplementar(t *testing.T) {
	// Vetores literais da tabela aesGCMTests do corpus oficial do Go:
	// https://github.com/golang/go/blob/go1.26.8/src/crypto/cipher/gcm_test.go
	// Os envelopes são nonce || ciphertext || tag, com AAD vazio.
	casos := []struct {
		nome, key, nonce, ciphertextTag, plaintext string
	}{
		{
			nome:          "plaintext vazio no limite de 28 bytes",
			key:           "5394e890d37ba55ec9d5f327f15680f6a63ef5279c79331643ad0af6d2623525",
			nonce:         "3c819d9a9bed087615030b65",
			ciphertextTag: "d9b260d4bc4630733ffb642f5ce45726",
			plaintext:     "",
		},
		{
			nome:          "plaintext de 16 bytes",
			key:           "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308",
			nonce:         "54cc7dc2c37ec006bcc6d1da",
			ciphertextTag: "d50b9e252b70945d4240d351677eb10f937cdaef6f2822b6a3191654ba41b197",
			plaintext:     "007c5e5b3e59df24a7c355584fc1518d",
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			box, err := crypto.NewBox(mustHex(t, caso.key))
			if err != nil {
				t.Fatal(err)
			}
			secret, err := box.Open(mustHex(t, caso.nonce+caso.ciphertextTag), nil)
			if err != nil {
				t.Fatalf("Open rejeitou envelope publicado: %v", err)
			}
			if !bytes.Equal(secret.Reveal(), mustHex(t, caso.plaintext)) {
				t.Fatalf("Open devolveu %x, queria %s", secret.Reveal(), caso.plaintext)
			}
		})
	}
}

func TestNewBoxReportsFIPSOnlyRejectionComplementar(t *testing.T) {
	const childMarker = "STILLON_CRYPTO_FIPS_FILHO"
	if os.Getenv(childMarker) == "1" {
		if !fips140.Enabled() {
			t.Fatal("subprocesso não iniciou com FIPS 140 habilitado")
		}
		// Chave pública de teste; o erro deve vir da construção GCM.
		box, err := crypto.NewBox(make([]byte, crypto.KeySize))
		if box != nil || err == nil {
			t.Fatalf("NewBox não propagou rejeição FIPS estrita: box presente=%t, erro=%v", box != nil, err)
		}
		if !strings.HasPrefix(err.Error(), "construir GCM: ") {
			t.Fatalf("erro perdeu contexto da construção GCM: %v", err)
		}
		return
	}

	// GODEBUG=fips140 é fixo na partida do executável. Isolar o subprocesso
	// preserva o runtime do pai e não declara conformidade FIPS do produto.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestNewBoxReportsFIPSOnlyRejectionComplementar$", "-test.v")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GODEBUG=") && !strings.HasPrefix(entry, childMarker+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	debug := os.Getenv("GODEBUG")
	if debug != "" {
		debug += ","
	}
	cmd.Env = append(cmd.Env, childMarker+"=1", "GODEBUG="+debug+"fips140=only")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subprocesso FIPS estrito falhou: %v\n%s", err, output)
	}
}
