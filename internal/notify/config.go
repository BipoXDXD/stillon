package notify

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// sealConfig cifra o destino do canal para config_enc (spec §5.4). O AAD
// amarra o ciphertext à linha: movido para outro canal, ele não abre.
func sealConfig(box *crypto.Box, id uuid.UUID, dest string) ([]byte, error) {
	sealed, err := box.Seal([]byte(dest), configAAD(id))
	if err != nil {
		return nil, fmt.Errorf("cifrar destino do canal: %w", err)
	}
	return sealed, nil
}

func openConfig(box *crypto.Box, id uuid.UUID, sealed []byte) (crypto.Secret, error) {
	dest, err := box.Open(sealed, configAAD(id))
	if err != nil {
		return nil, fmt.Errorf("decifrar destino do canal: %w", err)
	}
	return dest, nil
}

func configAAD(id uuid.UUID) []byte { return crypto.AAD("notif_channels", "config_enc", id) }

const maxEmail = 254

// NormalizeEmail aceita só um endereço nu ("a@b.com"), sem nome de exibição
// nem comentário, e devolve o domínio em minúsculas. O segundo retorno é
// false para qualquer outra coisa.
func NormalizeEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmail {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", false
	}
	i := strings.LastIndexByte(a.Address, '@')
	local, dominio := a.Address[:i], a.Address[i+1:]
	if !strings.Contains(dominio, ".") {
		return "", false
	}
	return local + "@" + strings.ToLower(dominio), true
}

// hint é o que a API mostra do destino: o bastante para o dono reconhecer
// o canal, e nada que sirva a quem lê a resposta sem ser o dono. Do
// webhook, os quatro últimos dígitos do id (nunca o token); do e-mail, a
// primeira letra e o domínio.
func hint(kind Kind, dest crypto.Secret) string {
	s := string(dest.Reveal())
	switch kind {
	case KindDiscord:
		partes := strings.Split(strings.TrimSuffix(s, "/"), "/")
		if len(partes) < 2 {
			return "webhook"
		}
		id := partes[len(partes)-2]
		if len(id) > 4 {
			id = id[len(id)-4:]
		}
		return "webhook …" + id
	case KindEmail:
		i := strings.LastIndexByte(s, '@')
		if i < 1 {
			return "e-mail"
		}
		primeira, _ := utf8.DecodeRuneInString(s)
		return string(primeira) + "***" + s[i:]
	}
	return ""
}
