package config

import (
	"strings"
	"testing"
)

// PROBE_BLOCKED_CIDRS aceita IP solto ou CIDR, v4 e v6, separados por
// vírgula. O IP solto vira /32 ou /128, o CIDR sai normalizado, e o v4
// mapeado vira v4, para casar com o IP que o safedial confere já com Unmap.
func TestLoadParsesProbeBlockedCIDRs(t *testing.T) {
	cases := []struct {
		valor string
		quer  []string
	}{
		{"192.0.2.10", []string{"192.0.2.10/32"}},
		{"2001:db8::5", []string{"2001:db8::5/128"}},
		{"192.0.2.0/24,2001:db8::/32", []string{"192.0.2.0/24", "2001:db8::/32"}},
		{" 192.0.2.10 , , 2001:db8::/48 ", []string{"192.0.2.10/32", "2001:db8::/48"}},
		{"192.0.2.77/24", []string{"192.0.2.0/24"}},
		{"::ffff:192.0.2.10", []string{"192.0.2.10/32"}},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			env["PROBE_BLOCKED_CIDRS"] = c.valor
			cfg, err := Load(getenvFrom(env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := make([]string, 0, len(cfg.ProbeBlockedCIDRs))
			for _, p := range cfg.ProbeBlockedCIDRs {
				got = append(got, p.String())
			}
			if strings.Join(got, ",") != strings.Join(c.quer, ",") {
				t.Errorf("ProbeBlockedCIDRs = %v, queria %v", got, c.quer)
			}
		})
	}
}

func TestLoadRejectsABadProbeBlockedCIDR(t *testing.T) {
	for _, valor := range []string{
		"isto-nao-e-ip",
		"192.0.2.0/33",
		"2001:db8::/129",
		"192.0.2.256",
		"3221226219",
		"0300.0000.0002.0353",
		"fe80::1%eth0",
		// O safedial compara o IP já com Unmap: um CIDR escrito como v6
		// mapeado nunca casaria, e a hospedagem ficaria aberta em silêncio.
		"::ffff:192.0.2.0/120",
		"192.0.2.10,lixo",
	} {
		t.Run(valor, func(t *testing.T) {
			// Em dev, para que o erro seja o do item, e não o da lista
			// vazia em produção.
			env := validEnv()
			env["PUBLIC_BASE_URL"] = "http://localhost:8080"
			env["PROBE_BLOCKED_CIDRS"] = valor
			_, err := Load(getenvFrom(env))
			if err == nil {
				t.Fatalf("PROBE_BLOCKED_CIDRS=%q passou, queria erro", valor)
			}
			if !strings.Contains(err.Error(), "PROBE_BLOCKED_CIDRS") {
				t.Errorf("erro = %v, queria citar PROBE_BLOCKED_CIDRS", err)
			}
		})
	}
}

// Em produção (a mesma regra das chaves de demo: PUBLIC_BASE_URL https, ou
// com host fora do loopback) o processo que sonda não sobe sem a lista. Em
// dev, a lista vazia continua permitida. O papel api não sonda e não precisa
// dela.
func TestProbeBlockedCIDRsIsRequiredInProduction(t *testing.T) {
	cases := []struct {
		nome      string
		publicURL string
		role      string
		valor     string
		erro      bool
	}{
		{nome: "https sem a variável", publicURL: "https://stillon.example", erro: true},
		{nome: "https com a variável só de vírgulas", publicURL: "https://stillon.example", valor: " , ", erro: true},
		{nome: "http fora do loopback sem a variável", publicURL: "http://stillon.example", erro: true},
		{nome: "papel prober em produção sem a variável", publicURL: "https://stillon.example", role: "prober", erro: true},
		{nome: "papel api em produção sem a variável", publicURL: "https://stillon.example", role: "api"},
		{nome: "https com a variável", publicURL: "https://stillon.example", valor: "192.0.2.10"},
		{nome: "dev em localhost sem a variável", publicURL: "http://localhost:8080"},
		{nome: "dev em 127.0.0.1 sem a variável", publicURL: "http://127.0.0.1:8080"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = c.publicURL
			env["ROLE"] = c.role
			env["PROBE_BLOCKED_CIDRS"] = c.valor
			cfg, err := Load(getenvFrom(env))
			if !c.erro {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if c.valor == "" && len(cfg.ProbeBlockedCIDRs) != 0 {
					t.Errorf("ProbeBlockedCIDRs = %v, queria vazio", cfg.ProbeBlockedCIDRs)
				}
				return
			}
			if err == nil {
				t.Fatal("o boot passou sem PROBE_BLOCKED_CIDRS em produção")
			}
			if !strings.Contains(err.Error(), "PROBE_BLOCKED_CIDRS") {
				t.Errorf("erro = %v, queria citar PROBE_BLOCKED_CIDRS", err)
			}
		})
	}
}
