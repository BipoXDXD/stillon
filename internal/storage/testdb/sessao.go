package testdb

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/google/uuid"
)

// FamiliaSintetica grava direto no banco uma família de sessão do usuário,
// sem passar pelo Manager: serve aos testes que precisam de uma sessão viva
// anterior à ativação do 2FA sem consumir código TOTP ou de recuperação nem
// mover o relógio do cenário. Os testes de login e de emissão usam os
// caminhos reais.
//
// O id é um UUIDv7 com o instante em (os 48 bits de milissegundos), como o
// Manager gera; a sessão vence em expira. tokenHash e ipHash já vêm
// calculados por quem chama (crypto.HashToken e crypto.HashIP).
func FamiliaSintetica(t *testing.T, userID uuid.UUID, em, expira time.Time, tokenHash, ipHash []byte) uuid.UUID {
	t.Helper()
	familia, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(em.UnixMilli()))
	copy(familia[:6], ms[2:])
	if _, err := Admin(t).Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, ip_hash)
		 VALUES ($1, $2, $1, $3, $4, $5)`, familia, userID, tokenHash, expira, ipHash); err != nil {
		t.Fatal(err)
	}
	return familia
}
