package crypto_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

const plaintext = "senha-do-webhook-super-secreta"

// O objetivo do tipo Secret é tornar o vazamento impossível pelo sistema de
// tipos, não improvável pela disciplina do programador. Cada teste abaixo
// cobre um caminho pelo qual um segredo costuma escapar na prática.
func TestSecretNeverPrints(t *testing.T) {
	s := crypto.Secret(plaintext)

	cases := map[string]string{
		"%v direto":  fmt.Sprintf("%v", s),
		"%s direto":  fmt.Sprintf("%s", s),
		"%#v direto": fmt.Sprintf("%#v", s),
		// Os verbos numéricos são o furo menos óbvio: String() e GoString() não
		// são consultados por %d, %c, %x nem %q, então sem um Format() o
		// conteúdo sai em claro (%q) ou trivialmente reversível (%d, %c, %x).
		"%d direto": fmt.Sprintf("%d", s),
		"%c direto": fmt.Sprintf("%c", s),
		"%x direto": fmt.Sprintf("%x", s),
		"%q direto": fmt.Sprintf("%q", s),
		"dentro de struct com %v": fmt.Sprintf("%v", struct {
			Nome  string
			Token crypto.Secret
		}{"alvo", s}),
	}

	for nome, got := range cases {
		if strings.Contains(got, plaintext) {
			t.Errorf("%s vazou o segredo: %s", nome, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s não trouxe o marcador de redação: %s", nome, got)
		}
	}
}

func TestSecretNeverSerializesToJSON(t *testing.T) {
	payload := struct {
		Nome  string        `json:"nome"`
		Token crypto.Secret `json:"token"`
	}{"alvo", crypto.Secret(plaintext)}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(plaintext)) {
		t.Errorf("json.Marshal vazou o segredo: %s", b)
	}
	if !bytes.Contains(b, []byte("[REDACTED]")) {
		t.Errorf("json.Marshal não trouxe o marcador de redação: %s", b)
	}
}

func TestSecretNeverReachesTheLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	log.Info("configuração do alvo", "token", crypto.Secret(plaintext))

	if strings.Contains(buf.String(), plaintext) {
		t.Errorf("slog vazou o segredo: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Errorf("slog não trouxe o marcador de redação: %s", buf.String())
	}
}

// Reveal tem nome explícito de propósito: uma busca por "Reveal(" no
// repositório lista todos os pontos onde um segredo é exposto em claro.
func TestRevealReturnsTheBytes(t *testing.T) {
	s := crypto.Secret(plaintext)
	if got := string(s.Reveal()); got != plaintext {
		t.Errorf("Reveal() = %q, queria %q", got, plaintext)
	}
}
