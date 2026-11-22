package httpapi_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

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
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	expira := a.clk.Now().Add(auth.RefreshTTL)
	familia := testdb.FamiliaSintetica(t, a.userID, a.clk.Now(), expira,
		crypto.HashToken(refresh), crypto.HashIP(testIPHashKey, "203.0.113.9"))
	return refresh, auth.Session{ID: familia, UserID: a.userID, FamilyID: familia, ExpiresAt: expira}
}
