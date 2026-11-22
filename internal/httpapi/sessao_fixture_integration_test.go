package httpapi_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// sessaoPreexistente monta uma família sintética para as rotas que exigem
// sessão viva. Representa a sessão anterior à ativação do fator sem consumir
// OTP/recovery nem mover o relógio usado nas provas de repetição e rotação.
// Os testes do login e da emissão usam os caminhos reais com senha e fator.
func sessaoPreexistente(t *testing.T, a *api) (string, auth.Session) {
	t.Helper()
	familia, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(a.clk.Now().UnixMilli()))
	copy(familia[:6], ms[2:])
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	s := auth.Session{ID: familia, UserID: a.userID, FamilyID: familia, ExpiresAt: a.clk.Now().Add(auth.RefreshTTL)}
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, ip_hash)
		 VALUES ($1, $2, $1, $3, $4, $5)`, s.ID, s.UserID, crypto.HashToken(refresh), s.ExpiresAt,
		crypto.HashIP(testIPHashKey, "203.0.113.9")); err != nil {
		t.Fatal(err)
	}
	return refresh, s
}
