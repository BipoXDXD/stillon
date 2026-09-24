package targets

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

// SealHeaders cifra os headers do alvo para headers_enc (spec §5.4).
//
// Mapa vazio vira NULL: não há segredo a guardar, e um ciphertext de "{}"
// só daria a impressão de que há.
func SealHeaders(box *crypto.Box, targetID uuid.UUID, h map[string]string) ([]byte, error) {
	if len(h) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("serializar headers: %w", err)
	}
	sealed, err := box.Seal(plain, headersAAD(targetID))
	if err != nil {
		return nil, fmt.Errorf("cifrar headers: %w", err)
	}
	return sealed, nil
}

// OpenHeaders decifra headers_enc. O AAD é o id da linha: um ciphertext
// movido para outro alvo não abre.
func OpenHeaders(box *crypto.Box, targetID uuid.UUID, sealed []byte) (map[string]string, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	plain, err := box.Open(sealed, headersAAD(targetID))
	if err != nil {
		return nil, fmt.Errorf("decifrar headers do alvo: %w", err)
	}
	var h map[string]string
	if err := json.Unmarshal(plain.Reveal(), &h); err != nil {
		return nil, fmt.Errorf("ler headers do alvo: %w", err)
	}
	return h, nil
}

func headersAAD(id uuid.UUID) []byte { return crypto.AAD("targets", "headers_enc", id) }

// headerNames é o que a API pode mostrar: os nomes, ordenados, e nunca nil,
// para que o JSON saia [] e não null.
func headerNames(h map[string]string) []string {
	names := slices.Collect(maps.Keys(h))
	slices.Sort(names)
	if names == nil {
		return []string{}
	}
	return names
}
