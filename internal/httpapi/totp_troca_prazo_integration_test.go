package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// O prazo da troca pendente (#326), pelo HTTP: o rotate/confirm aceita a troca
// um instante antes de auth.PendingRotationTTL e responde 403 forbidden, o mesmo
// de "sem troca em andamento", no próprio prazo. O relógio é o falso da api.
func TestRotateConfirmHonorsThePendingDeadline(t *testing.T) {
	casos := []struct {
		nome   string
		apos   time.Duration
		errado bool
		status int
	}{
		{"um instante antes do prazo", auth.PendingRotationTTL - time.Nanosecond, false, http.StatusOK},
		{"no próprio prazo", auth.PendingRotationTTL, false, http.StatusForbidden},
		{"depois do prazo, com o código errado", auth.PendingRotationTTL + time.Minute, true, http.StatusForbidden},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			velho := cadastrarTOTP(t, a, clientWithJar(t))
			token := accessTokenValido(t, a)
			_, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})

			a.clk.Advance(caso.apos)
			codigo := codigoDe(t, a, novo)
			if caso.errado {
				codigo = codigoDe(t, a, velho)
			}
			st, erro, codigos := confirmarTrocaHTTP(t, a, token, codigo)
			if st != caso.status {
				t.Fatalf("confirm %s: %d %s, queria %d", caso.nome, st, erro, caso.status)
			}
			if caso.status == http.StatusOK {
				if len(codigos) != 10 {
					t.Errorf("%d códigos de recuperação, queria 10", len(codigos))
				}
				return
			}
			if erro != "forbidden" {
				t.Errorf("confirm vencido: code %q, queria forbidden", erro)
			}
			if n := falhasDeLogin(t, a); n != 0 {
				t.Errorf("o confirm vencido contou %d falhas no bloqueio", n)
			}
			if !temPendente(t, a) {
				t.Error("a recusa apagou o pendente: ela não grava nada")
			}
			// O fator ativo segue sendo o app antigo.
			if st := entrarCom(t, a, codigoDe(t, a, novo)); st == http.StatusOK {
				t.Error("o app novo entrou com a troca vencida")
			}
		})
	}
}
