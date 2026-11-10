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
		// Um /0 recusaria todo alvo.
		"0.0.0.0/0",
		"::/0",
		// Item vazio é erro, e não lista vazia.
		",,,",
		",",
		"192.0.2.10,,2001:db8::/48",
		"192.0.2.10,",
	} {
		t.Run(valor, func(t *testing.T) {
			env := validEnv()
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

// Espaço em volta de um item também é recusado pelo netip, mas a mensagem
// própria diz o conserto a quem digita a variável no Coolify.
func TestLoadRejectsSpaceAroundAProbeBlockedCIDR(t *testing.T) {
	for _, valor := range []string{
		" 192.0.2.10",
		"192.0.2.10 ",
		"192.0.2.10, 2001:db8::/48",
		" ",
	} {
		t.Run(valor, func(t *testing.T) {
			env := validEnv()
			env["PROBE_BLOCKED_CIDRS"] = valor
			_, err := Load(getenvFrom(env))
			if err == nil {
				t.Fatalf("PROBE_BLOCKED_CIDRS=%q passou, queria erro", valor)
			}
			if !strings.Contains(err.Error(), "PROBE_BLOCKED_CIDRS") || !strings.Contains(err.Error(), "tem espaço em volta") {
				t.Errorf("erro = %v, queria citar PROBE_BLOCKED_CIDRS e \"tem espaço em volta\"", err)
			}
		})
	}
}

// Em produção, nos papéis que sondam, a lista ausente ou vazia derruba o
// boot com uma mensagem só (L1 do STRIDE do #255).
func TestProducaoQueSondaExigeAListaDaHospedagem(t *testing.T) {
	cases := []struct {
		nome      string
		publicURL string
		role      string
		apagar    bool
	}{
		{nome: "https sem a variável", publicURL: "https://stillon.example", apagar: true},
		{nome: "https com a variável vazia", publicURL: "https://stillon.example"},
		{nome: "http fora do loopback sem a variável", publicURL: "http://stillon.example", apagar: true},
		{nome: "papel prober em produção sem a variável", publicURL: "https://stillon.example", role: "prober", apagar: true},
		{nome: "papel all explícito, vazia", publicURL: "https://stillon.example", role: "all"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = c.publicURL
			env["ROLE"] = c.role
			env["PROBE_BLOCKED_CIDRS"] = ""
			if c.apagar {
				delete(env, "PROBE_BLOCKED_CIDRS")
			}
			_, err := Load(getenvFrom(env))
			if err == nil {
				t.Fatal("Load passou sem a lista da hospedagem, queria erro")
			}
			if !strings.Contains(err.Error(), "variável obrigatória ausente em produção: PROBE_BLOCKED_CIDRS") {
				t.Errorf("erro = %v, queria a mensagem de lista ausente em produção", err)
			}
			if n := strings.Count(err.Error(), "PROBE_BLOCKED_CIDRS"); n != 1 {
				t.Errorf("PROBE_BLOCKED_CIDRS aparece %d vezes no erro, queria 1: %v", n, err)
			}
		})
	}
}

// Fora da produção que sonda a lista segue opcional: o papel api nunca disca
// um alvo, e em dev (loopback) não há vizinhos. Quem avisa em dev é o
// sondador, ao nascer (TestListaDaHospedagem* no pacote prober).
func TestListaDaHospedagemOpcionalForaDaProducaoQueSonda(t *testing.T) {
	cases := []struct {
		nome      string
		publicURL string
		cors      string
		role      string
		apagar    bool
	}{
		{nome: "papel api em produção sem a variável", publicURL: "https://stillon.example", role: "api", apagar: true},
		{nome: "dev em localhost sem a variável", publicURL: "http://localhost:8080", cors: "http://localhost:5173", apagar: true},
		{nome: "dev em 127.0.0.1 com a variável vazia", publicURL: "http://127.0.0.1:8080", cors: "http://localhost:5173"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = c.publicURL
			if c.cors != "" {
				env["CORS_ORIGIN"] = c.cors
			}
			env["ROLE"] = c.role
			env["PROBE_BLOCKED_CIDRS"] = ""
			if c.apagar {
				delete(env, "PROBE_BLOCKED_CIDRS")
			}
			cfg, err := Load(getenvFrom(env))
			if err != nil {
				t.Fatalf("Load com a lista vazia: %v", err)
			}
			if len(cfg.ProbeBlockedCIDRs) != 0 {
				t.Errorf("ProbeBlockedCIDRs = %v, queria vazio", cfg.ProbeBlockedCIDRs)
			}
		})
	}
}

// Um item inválido já tem o erro dele; "ausente" ao lado esconderia o erro de
// digitação.
func TestItemInvalidoDaListaNaoViraAusente(t *testing.T) {
	for _, valor := range []string{",", " "} {
		t.Run(valor, func(t *testing.T) {
			env := validEnv()
			env["PROBE_BLOCKED_CIDRS"] = valor
			_, err := Load(getenvFrom(env))
			if err == nil {
				t.Fatalf("PROBE_BLOCKED_CIDRS=%q passou, queria erro", valor)
			}
			if !strings.Contains(err.Error(), "PROBE_BLOCKED_CIDRS") {
				t.Errorf("erro = %v, queria citar PROBE_BLOCKED_CIDRS", err)
			}
			if strings.Contains(err.Error(), "variável obrigatória ausente") {
				t.Errorf("erro = %v, não queria a mensagem de lista ausente", err)
			}
		})
	}
}

func TestExigeListaDaHospedagem(t *testing.T) {
	cases := []struct {
		publicURL string
		role      Role
		quer      bool
	}{
		{"https://stillon.example", RoleAll, true},
		{"https://stillon.example", RoleProber, true},
		{"https://stillon.example", RoleAPI, false},
		{"http://stillon.example", RoleAll, true},
		{"http://localhost:8080", RoleAll, false},
		{"http://[::1]:8080", RoleProber, false},
		{"", RoleAll, false},
	}
	for _, c := range cases {
		t.Run(c.publicURL+"/"+string(c.role), func(t *testing.T) {
			if got := exigeListaDaHospedagem(c.publicURL, c.role); got != c.quer {
				t.Errorf("exigeListaDaHospedagem(%q, %q) = %v, queria %v", c.publicURL, c.role, got, c.quer)
			}
		})
	}
}
