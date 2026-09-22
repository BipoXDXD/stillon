package auth_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/auth"
)

const senha = "uma-senha-bem-comprida-e-boa"

func TestHashAndVerifyRoundTrip(t *testing.T) {
	h, err := auth.HashPassword(senha)
	if err != nil {
		t.Fatal(err)
	}

	ok, needsRehash, err := auth.VerifyPassword(h, senha)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a senha correta não foi aceita")
	}
	if needsRehash {
		t.Error("um hash recém-criado não deveria pedir rehash")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	h, _ := auth.HashPassword(senha)

	ok, _, err := auth.VerifyPassword(h, senha+"x")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("senha errada foi aceita")
	}
}

// Salt aleatório por hash impede que duas contas com a mesma senha tenham o
// mesmo registro — e com isso impede tabela arco-íris e comparação entre
// contas em caso de vazamento.
func TestSamePasswordProducesDifferentHashes(t *testing.T) {
	a, _ := auth.HashPassword(senha)
	b, _ := auth.HashPassword(senha)

	if a == b {
		t.Error("dois hashes da mesma senha saíram idênticos: o salt não é aleatório")
	}
}

func TestHashUsesSpecParameters(t *testing.T) {
	h, _ := auth.HashPassword(senha)

	// Formato PHC: $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$`)
	if !re.MatchString(h) {
		t.Errorf("hash fora do formato ou dos parâmetros da spec: %s", h)
	}
}

// Guardar os parâmetros dentro do hash é o que permite subir o custo depois
// sem invalidar as senhas existentes: o login recalcula com os parâmetros
// antigos, valida, e sinaliza rehash.
func TestVerifyFlagsWeakerParametersForRehash(t *testing.T) {
	fraco := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$" +
		mustHashWith(t, senha, 19456, 2, 1)

	ok, needsRehash, err := auth.VerifyPassword(fraco, senha)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("hash com parâmetros antigos deveria continuar validando")
	}
	if !needsRehash {
		t.Error("parâmetros abaixo do padrão atual deveriam pedir rehash")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := map[string]string{
		"vazio":                "",
		"sem cifrão":           "argon2id",
		"algoritmo errado":     "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"versão errada":        "$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"parâmetros ilegíveis": "$argon2id$v=19$m=abc,t=3,p=2$c2FsdA$aGFzaA",
		"salt não é base64":    "$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
		"hash não é base64":    "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$!!!",
		"salt vazio":           "$argon2id$v=19$m=65536,t=3,p=2$$aGFzaA",
		"hash vazio":           "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$",
		"campos a menos":       "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
	}

	for nome, h := range cases {
		t.Run(nome, func(t *testing.T) {
			if _, _, err := auth.VerifyPassword(h, senha); !errors.Is(err, auth.ErrInvalidHash) {
				t.Errorf("erro = %v, queria ErrInvalidHash", err)
			}
		})
	}
}

// VerifyDummy existe para o login gastar o mesmo tempo quando o e-mail não
// existe. Sem ela, o tempo de resposta vira oráculo de existência de conta
// (spec §5.2). O teste aqui só garante que a função é chamável e não entra em
// pânico; afirmar sobre tempo em teste é receita de flakiness.
func TestVerifyDummyDoesNotPanic(t *testing.T) {
	auth.VerifyDummy()
}

func mustHashWith(t *testing.T, plain string, m, tt uint32, p uint8) string {
	t.Helper()
	h := auth.HashPasswordWith(plain, []byte("saltsaltsaltsa"), m, tt, p)
	parts := strings.Split(h, "$")
	return parts[len(parts)-1]
}

// TestVerifyPasswordRejectsOutOfRangeParams cobre hashes cujos parâmetros são
// sintaticamente válidos mas fora da faixa que o argon2 aceita. A biblioteca
// entra em panic nesses casos; decodeHash tem de recusar antes.
func TestVerifyPasswordRejectsOutOfRangeParams(t *testing.T) {
	cases := map[string]string{
		"tudo zero":            "$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
		"t=0":                  "$argon2id$v=19$m=65536,t=0,p=2$c2FsdA$aGFzaA",
		"p=0":                  "$argon2id$v=19$m=65536,t=3,p=0$c2FsdA$aGFzaA",
		"memória abaixo de 8p": "$argon2id$v=19$m=8,t=3,p=2$c2FsdA$aGFzaA",
	}

	for nome, h := range cases {
		t.Run(nome, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifyPassword entrou em panic: %v", r)
				}
			}()
			if _, _, err := auth.VerifyPassword(h, senha); !errors.Is(err, auth.ErrInvalidHash) {
				t.Errorf("erro = %v, queria ErrInvalidHash", err)
			}
		})
	}
}
