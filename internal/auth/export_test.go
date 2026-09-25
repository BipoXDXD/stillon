package auth

import (
	"testing"
	"time"
)

// EncurtarEsperaDoArgon2 troca o prazo de aquisição do semáforo do Argon2id
// (Argon2Wait) só durante o teste: os testes do pacote provam a recusa sem
// esperar os 2 s de produção a cada caminho.
func EncurtarEsperaDoArgon2(t *testing.T, d time.Duration) {
	t.Helper()
	antes := esperaArgon2
	esperaArgon2 = d
	t.Cleanup(func() { esperaArgon2 = antes })
}

// Len diz quantas famílias o contador acompanha: é a única forma de observar a
// varredura das paradas sem exportar o mapa.
func (f *FamilyProofs) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.contas)
}
