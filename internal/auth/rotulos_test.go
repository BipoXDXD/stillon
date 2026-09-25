package auth

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// TestRotulosFixados prende os rótulos de domínio da derivação de chaves no
// texto do StillOn. Trocar um deles invalida os cookies de dispositivo já
// emitidos ou os ids de rotação pendentes; por isso os valores esperados são
// literais aqui, e não as constantes.
func TestRotulosFixados(t *testing.T) {
	const (
		dispositivo = "stillon.dispositivo.v1"
		rotacao     = "stillon.rotacao.v1"
	)

	// (i) as constantes.
	if rotuloDispositivo != dispositivo {
		t.Errorf("rotuloDispositivo = %q; queria %q", rotuloDispositivo, dispositivo)
	}
	if rotuloRotacao != rotacao {
		t.Errorf("rotuloRotacao = %q; queria %q", rotuloRotacao, rotacao)
	}

	// (ii) a chave do MAC do cookie de dispositivo sai do HKDF com o rótulo.
	k := []byte("chave-mestra-de-teste-32-bytes!!")
	if len(k) != 32 {
		t.Fatalf("chave de teste com %d bytes; queria 32", len(k))
	}
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	d := NewDevices(k, clk)
	quer, err := hkdf.Key(sha256.New, k, nil, dispositivo, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.chave, quer) {
		t.Errorf("chave do dispositivo = %x; queria %x (HKDF com %q)", d.chave, quer, dispositivo)
	}

	// (iii) o id de rotação é sha256(rótulo || pendente)[:16] em hex.
	p := []byte("segredo-pendente-cifrado")
	h := sha256.Sum256(append([]byte(rotacao), p...))
	if got, want := rotacaoDe(p), hex.EncodeToString(h[:16]); got != want {
		t.Errorf("rotacaoDe = %s; queria %s", got, want)
	}
}
