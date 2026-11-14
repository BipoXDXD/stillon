package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestServerFlagsAcceptTheRole(t *testing.T) {
	for _, c := range []struct {
		args []string
		quer string
	}{
		{nil, ""},
		{[]string{"--role", "api"}, "api"},
		{[]string{"-role=prober"}, "prober"},
	} {
		var stderr bytes.Buffer
		role, err := lerFlagsDoServidor(c.args, &stderr)
		if err != nil || role != c.quer {
			t.Errorf("lerFlagsDoServidor(%q) = %q, %v; queria %q", c.args, role, err, c.quer)
		}
	}
}

// Um subcomando digitado errado não pode cair no modo servidor: ele migraria
// o banco de produção antes de morrer com a porta ocupada.
func TestServerFlagsRefuseLeftoverArguments(t *testing.T) {
	for _, args := range [][]string{
		{"healtcheck"},
		{"usr", "create"},
		{"--role", "api", "sobra"},
	} {
		var stderr bytes.Buffer
		if _, err := lerFlagsDoServidor(args, &stderr); err == nil {
			t.Errorf("lerFlagsDoServidor(%q) aceitou o argumento que sobra", args)
		}
		if !strings.Contains(stderr.String(), "healthcheck") {
			t.Errorf("lerFlagsDoServidor(%q) não listou os subcomandos:\n%s", args, stderr.String())
		}
	}
}

func TestServerFlagsRefuseAnUnknownFlag(t *testing.T) {
	var stderr bytes.Buffer
	_, err := lerFlagsDoServidor([]string{"--rol", "api"}, &stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		t.Errorf("err = %v; queria erro de flag desconhecida", err)
	}
}
