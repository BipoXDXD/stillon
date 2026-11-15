package main

import (
	"bytes"
	"strings"
	"testing"
)

// Um argumento que não é subcomando nem flag (um erro de digitação como
// `stillon migarte`) é recusado com o uso, em vez de subir o servidor.
func TestServerArgsRejectUnknownSubcommand(t *testing.T) {
	casos := []struct {
		nome  string
		args  []string
		citar string
	}{
		{"subcomando digitado errado", []string{"migarte"}, `"migarte"`},
		{"nome que não é subcomando", []string{"serve"}, `"serve"`},
		{"argumento solto depois da flag", []string{"--role=api", "lixo"}, `"lixo"`},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			var errb bytes.Buffer
			role, codigo, seguir := lerRole(c.args, &errb)
			if seguir || codigo != saidaUso || role != "" {
				t.Errorf("lerRole(%q) = (%q, %d, %v), queria (\"\", %d, false)", c.args, role, codigo, seguir, saidaUso)
			}
			if !strings.Contains(errb.String(), c.citar) || !strings.Contains(errb.String(), "uso: stillon") {
				t.Errorf("stderr %q não cita %s nem traz o uso", errb.String(), c.citar)
			}
		})
	}
}

// O caminho normal continua: só flags, ou nenhuma (a role vem da env ROLE).
func TestServerArgsAcceptTheRoleFlag(t *testing.T) {
	casos := []struct {
		nome string
		args []string
		role string
	}{
		{"flag com igual", []string{"--role=all"}, "all"},
		{"flag e valor separados", []string{"-role", "prober"}, "prober"},
		{"sem argumentos", nil, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			var errb bytes.Buffer
			role, codigo, seguir := lerRole(c.args, &errb)
			if !seguir || codigo != saidaOK || role != c.role || errb.Len() != 0 {
				t.Errorf("lerRole(%q) = (%q, %d, %v), stderr %q; queria (%q, %d, true) e stderr vazio",
					c.args, role, codigo, seguir, errb.String(), c.role, saidaOK)
			}
		})
	}
}

// -h mostra a ajuda e sai com 0 sem subir o servidor, como o flag.ExitOnError
// fazia antes.
func TestServerArgsHelpExitsZeroWithoutStarting(t *testing.T) {
	var errb bytes.Buffer
	if _, codigo, seguir := lerRole([]string{"-h"}, &errb); seguir || codigo != saidaOK || !strings.Contains(errb.String(), "-role") {
		t.Errorf("-h: código %d, seguir %v, stderr %q; queria 0, false e a ajuda da flag", codigo, seguir, errb.String())
	}
}

// Flag desconhecida é uso errado, e o stderr diz qual.
func TestServerArgsRejectUnknownFlag(t *testing.T) {
	var errb bytes.Buffer
	if _, codigo, seguir := lerRole([]string{"--porta=9000"}, &errb); seguir || codigo != saidaUso || !strings.Contains(errb.String(), "porta") {
		t.Errorf("flag desconhecida: código %d, seguir %v, stderr %q; queria %d, false e a flag citada", codigo, seguir, errb.String(), saidaUso)
	}
}

// O uso impresso lista todos os subcomandos do mapa: um subcomando novo que
// esqueça a mensagem quebra aqui.
func TestServerUsageListsEverySubcommand(t *testing.T) {
	for nome := range subcomandos {
		if !strings.Contains(usoDoServidor, nome) {
			t.Errorf("usoDoServidor não cita o subcomando %q", nome)
		}
	}
}
