package config

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	return map[string]string{
		"DATABASE_URL":    "postgres://localhost/vigilume",
		"PUBLIC_BASE_URL": "https://vigilume.example",
		"CORS_ORIGIN":     "https://app.vigilume.example",
		"MASTER_KEY":      k,
		"JWT_SECRET":      k,
		"IP_HASH_KEY":     k,
	}
}

func getenvFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatalf("esperava sucesso, veio: %v", err)
	}
	if cfg.Role != RoleAll {
		t.Errorf("Role padrão = %q, queria %q", cfg.Role, RoleAll)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr padrão = %q, queria \":8080\"", cfg.HTTPAddr)
	}
	if len(cfg.MasterKey) != 32 {
		t.Errorf("MasterKey tem %d bytes, queria 32", len(cfg.MasterKey))
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel padrão = %v, queria Info", cfg.LogLevel)
	}
}

func TestLoadReportsEveryMissingVarAtOnce(t *testing.T) {
	env := validEnv()
	delete(env, "DATABASE_URL")
	delete(env, "CORS_ORIGIN")

	_, err := Load(getenvFrom(env))
	if err == nil {
		t.Fatal("esperava erro quando faltam variáveis")
	}
	for _, want := range []string{"DATABASE_URL", "CORS_ORIGIN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erro não menciona %s: %v", want, err)
		}
	}
}

func TestLoadRejectsShortKey(t *testing.T) {
	env := validEnv()
	env["MASTER_KEY"] = base64.StdEncoding.EncodeToString([]byte("curta"))

	_, err := Load(getenvFrom(env))
	if err == nil || !strings.Contains(err.Error(), "MASTER_KEY") {
		t.Fatalf("esperava erro citando MASTER_KEY, veio: %v", err)
	}
}

func TestLoadRejectsUnknownRole(t *testing.T) {
	env := validEnv()
	env["ROLE"] = "banana"

	_, err := Load(getenvFrom(env))
	if err == nil || !strings.Contains(err.Error(), "ROLE") {
		t.Fatalf("esperava erro citando ROLE, veio: %v", err)
	}
}

func TestRolePredicates(t *testing.T) {
	cases := []struct {
		role              Role
		wantAPI, wantProb bool
	}{
		{RoleAll, true, true},
		{RoleAPI, true, false},
		{RoleProber, false, true},
	}
	for _, c := range cases {
		if got := c.role.RunsAPI(); got != c.wantAPI {
			t.Errorf("%s.RunsAPI() = %v, queria %v", c.role, got, c.wantAPI)
		}
		if got := c.role.RunsProber(); got != c.wantProb {
			t.Errorf("%s.RunsProber() = %v, queria %v", c.role, got, c.wantProb)
		}
	}
}

func TestLogValueRedactsSecrets(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("boot", "cfg", cfg)
	out := buf.String()

	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if strings.Contains(out, secret) {
		t.Error("o log vazou material de chave")
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("esperava marcador de redação no log, veio: %s", out)
	}
}

func TestLoadParsesTrustedProxies(t *testing.T) {
	env := validEnv()
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, 2001:db8::/32"

	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatalf("esperava sucesso, veio: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("%d prefixos confiáveis, queria 2", len(cfg.TrustedProxies))
	}
	if got := cfg.TrustedProxies[0].String(); got != "10.0.0.0/8" {
		t.Errorf("primeiro prefixo = %q, queria 10.0.0.0/8", got)
	}
}

// Vazio é o padrão e significa "não confie em X-Forwarded-For de ninguém" —
// a falha fechada. Ver middleware.ClientIP.
func TestTrustedProxiesDefaultsToEmpty(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("sem TRUSTED_PROXIES vieram %d prefixos, queria nenhum", len(cfg.TrustedProxies))
	}
}

// Um CIDR errado não pode virar silêncio: uma lista que o operador achou que
// tinha preenchido, e que na verdade ficou vazia, é a pior das duas falhas.
func TestLoadRejectsAMalformedTrustedProxy(t *testing.T) {
	env := validEnv()
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, isto-nao-e-um-cidr"

	if _, err := Load(getenvFrom(env)); err == nil {
		t.Fatal("esperava erro para CIDR inválido em TRUSTED_PROXIES")
	} else if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("o erro não menciona TRUSTED_PROXIES: %v", err)
	}
}

// Spec §5.7: /metrics entrega o mapa operacional, e só pode escutar em
// loopback (D10). "off" desliga.
func TestMetricsAddrMustBeLoopback(t *testing.T) {
	cases := []struct {
		valor string
		quer  string // "" com ok=true significa desligado
		ok    bool
	}{
		{"", "127.0.0.1:9090", true}, // padrão
		{"off", "", true},
		{"OFF", "", true},
		{"Off", "", true},
		{"127.0.0.1:9100", "127.0.0.1:9100", true},
		{"[::1]:9090", "[::1]:9090", true},
		{"localhost:9090", "localhost:9090", true},
		{":9090", "", false},
		{"0.0.0.0:9090", "", false},
		{"[::]:9090", "", false},
		{"10.0.0.5:9090", "", false},
		{"sem-porta", "", false},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			if c.valor != "" {
				env["METRICS_ADDR"] = c.valor
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "METRICS_ADDR") {
					t.Fatalf("METRICS_ADDR=%q aceito (err = %v)", c.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("METRICS_ADDR=%q recusado: %v", c.valor, err)
			}
			if cfg.MetricsAddr != c.quer {
				t.Errorf("MetricsAddr = %q, queria %q", cfg.MetricsAddr, c.quer)
			}
		})
	}
}

// M7 (Plano 9, §3.4): sem METRICS_ADDR, o padrão depende do papel efetivo,
// para que --role=api e --role=prober no mesmo host não disputem a porta. Com
// METRICS_ADDR definida (inclusive off), ela vale para qualquer papel, e as
// validações de sempre continuam.
func TestMetricsAddrDefaultsByRole(t *testing.T) {
	cases := []struct {
		role, metrics string
		quer          string
		ok            bool
	}{
		{"", "", "127.0.0.1:9090", true}, // ROLE ausente é all
		{"all", "", "127.0.0.1:9090", true},
		{"api", "", "127.0.0.1:9090", true},
		{"prober", "", "127.0.0.1:9091", true},
		{"prober", "127.0.0.1:9100", "127.0.0.1:9100", true},
		{"api", "127.0.0.1:9091", "127.0.0.1:9091", true},
		{"prober", "off", "", true},
		{"prober", "0.0.0.0:9091", "", false},
		{"prober", ":9091", "", false},
	}
	for _, c := range cases {
		t.Run(c.role+"/"+c.metrics, func(t *testing.T) {
			env := validEnv()
			if c.role != "" {
				env["ROLE"] = c.role
			}
			if c.metrics != "" {
				env["METRICS_ADDR"] = c.metrics
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "METRICS_ADDR") {
					t.Fatalf("ROLE=%q METRICS_ADDR=%q aceito (err = %v)", c.role, c.metrics, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ROLE=%q METRICS_ADDR=%q recusado: %v", c.role, c.metrics, err)
			}
			if cfg.MetricsAddr != c.quer {
				t.Errorf("ROLE=%q METRICS_ADDR=%q: MetricsAddr = %q, queria %q", c.role, c.metrics, cfg.MetricsAddr, c.quer)
			}
		})
	}
}

func TestEmailSettingsGoTogether(t *testing.T) {
	cases := []struct {
		nome        string
		chave, from string
		ligado, ok  bool
	}{
		{"nenhuma", "", "", false, true},
		{"as duas", "re_123", "Vigilume <alertas@vigilume.dev>", true, true},
		{"só a chave", "re_123", "", false, false},
		{"só o remetente", "", "alertas@vigilume.dev", false, false},
		{"remetente inválido", "re_123", "não é e-mail", false, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			env := validEnv()
			env["RESEND_API_KEY"], env["EMAIL_FROM"] = c.chave, c.from
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "EMAIL_FROM") {
					t.Fatalf("aceito (err = %v)", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.EmailEnabled() != c.ligado {
				t.Errorf("EmailEnabled = %v, queria %v", cfg.EmailEnabled(), c.ligado)
			}
		})
	}
}

func TestLogValueRedactsTheResendKey(t *testing.T) {
	env := validEnv()
	env["RESEND_API_KEY"], env["EMAIL_FROM"] = "re_chave_secreta", "a@b.dev"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "cfg", cfg)
	if strings.Contains(buf.String(), "re_chave_secreta") || !strings.Contains(buf.String(), `"email_enabled":true`) {
		t.Errorf("log = %s", buf.String())
	}
}

func TestLoadParsesTheClientIPHeader(t *testing.T) {
	env := validEnv()
	env["CLIENT_IP_HEADER"] = "fly-client-ip"
	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "Fly-Client-Ip" {
		t.Errorf("ClientIPHeader = %q, queria a forma canônica Fly-Client-Ip", cfg.ClientIPHeader)
	}
}

func TestClientIPHeaderDefaultsToEmpty(t *testing.T) {
	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "" {
		t.Errorf("sem CLIENT_IP_HEADER veio %q, queria vazio (último salto do XFF)", cfg.ClientIPHeader)
	}
}

func TestLoadRejectsABadClientIPHeader(t *testing.T) {
	for _, v := range []string{"Fly Client IP", "Fly-Client-IP:", "X-Forwarded-For", "x-forwarded-for"} {
		env := validEnv()
		env["CLIENT_IP_HEADER"] = v
		_, err := Load(getenvFrom(env))
		if err == nil || !strings.Contains(err.Error(), "CLIENT_IP_HEADER") {
			t.Errorf("CLIENT_IP_HEADER=%q: erro = %v, queria recusa citando a variável", v, err)
		}
	}
}

// Plano 8: nomes que nunca carregam o IP do cliente são recusados no boot, em
// qualquer caixa. Sem isto, o erro só aparecia como um balde de rate limit
// dividido por todos os clientes.
func TestLoadRejectsHeadersThatNeverCarryTheClientIP(t *testing.T) {
	// Lista literal, e não cabecalhosProibidos: um teste que percorresse a
	// própria lista encolheria junto com ela, e ficaria verde com a lista
	// vazia.
	nomes := []string{
		"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port",
		"Host", "Cookie", "Authorization", "Proxy-Authorization",
		"Connection", "Upgrade", "TE", "Trailer", "Transfer-Encoding", "Keep-Alive",
		"Content-Length", "Content-Type",
	}
	for _, nome := range nomes {
		for _, v := range []string{nome, strings.ToLower(nome), strings.ToUpper(nome), " " + nome + " "} {
			env := validEnv()
			env["CLIENT_IP_HEADER"] = v
			_, err := Load(getenvFrom(env))
			if err == nil || !strings.Contains(err.Error(), "CLIENT_IP_HEADER") {
				t.Errorf("CLIENT_IP_HEADER=%q: erro = %v, queria recusa citando a variável", v, err)
			}
		}
	}
	// A perna positiva: os cabeçalhos de IP de cliente dos proxies comuns
	// passam, inclusive um que começa parecido com a família proibida.
	for _, v := range []string{"Fly-Client-IP", "CF-Connecting-IP", "True-Client-IP", "X-Real-IP", "X-Forwarded"} {
		env := validEnv()
		env["CLIENT_IP_HEADER"] = v
		if _, err := Load(getenvFrom(env)); err != nil {
			t.Errorf("CLIENT_IP_HEADER=%q foi recusado: %v", v, err)
		}
	}
}

// As chaves de demonstração do compose.yaml são públicas no repositório. Com
// elas em produção, qualquer um forja JWT e decifra os segredos.
func TestLoadRejectsDemoKeysOverHTTPS(t *testing.T) {
	// O esquema não diferencia maiúsculas (RFC 3986): "HTTPS://" também é
	// produção, e não pode passar pelo portão como se fosse http.
	// https em loopback também recusa (Plano 8): o portão é "http E
	// loopback", e não "loopback"; sem estes dois casos, uma regressão para
	// só !hostLocal passaria no CI.
	for _, base := range []string{"https://vigilume.example", "HTTPS://vigilume.example", "https://localhost", "https://127.0.0.1"} {
		for i, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = base
			// Cada variável com a chave de demo de OUTRA: embaralhar não escapa.
			env[nome] = base64.StdEncoding.EncodeToString(chavesDeDemo[(i+1)%len(chavesDeDemo)])
			_, err := Load(getenvFrom(env))
			if err == nil || !strings.Contains(err.Error(), nome) || !strings.Contains(err.Error(), "demonstração") {
				t.Errorf("%s com chave de demo e %s: erro = %v, queria recusa", nome, base, err)
				continue
			}
			// A mensagem vai para o log do boot: não pode repetir a chave,
			// nem em texto nem em base64.
			vazamentos := []string{"naouse"}
			for _, d := range chavesDeDemo {
				vazamentos = append(vazamentos, string(d), base64.StdEncoding.EncodeToString(d))
			}
			for _, vazado := range vazamentos {
				if strings.Contains(err.Error(), vazado) {
					t.Errorf("%s com %s: a mensagem de erro repete a chave (%q): %v", nome, base, vazado, err)
				}
			}
		}
	}
}

func TestEhHTTPS(t *testing.T) {
	casos := map[string]bool{
		"https://vigilume.example":   true,
		"HTTPS://vigilume.example":   true,
		"Https://vigilume.example":   true,
		" https://vigilume.example ": true,
		"http://localhost:8080":      false,
		"HTTP://localhost:8080":      false,
		"httpsx://vigilume.example":  false,
		"vigilume.example":           false,
		"":                           false,
	}
	for s, want := range casos {
		if got := EhHTTPS(s); got != want {
			t.Errorf("EhHTTPS(%q) = %v, queria %v", s, got, want)
		}
	}
}

func TestLoadAcceptsDemoKeysOverHTTP(t *testing.T) {
	env := validEnv()
	env["PUBLIC_BASE_URL"] = "http://localhost:8080"
	env["MASTER_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[0])
	env["JWT_SECRET"] = base64.StdEncoding.EncodeToString(chavesDeDemo[1])
	env["IP_HASH_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[2])
	if _, err := Load(getenvFrom(env)); err != nil {
		t.Errorf("chaves de demo com http (o compose) deveriam subir: %v", err)
	}
}

// As chaves de demo têm de ter o tamanho certo: senão o compose nem sobe, e
// a recusa acima nunca seria exercida por quem copiou o arquivo.
func TestDemoKeysHaveTheRequiredLength(t *testing.T) {
	for i, k := range chavesDeDemo {
		if len(k) != keyLen {
			t.Errorf("chave de demo %d tem %d bytes, queria %d", i, len(k), keyLen)
		}
	}
}

// PUBLIC_BASE_URL decide se o processo está em produção. Um valor malformado
// que começa com https:// não pode passar como http e ligar as chaves de demo,
// tirar o Secure do cookie e calar o aviso de TRUSTED_PROXIES: o boot recusa.
func TestLoadRejectsMalformedPublicBaseURL(t *testing.T) {
	for _, v := range []string{
		"https://exa mple.com",
		`"https://x"`,
		"ftp://x",
		"https://",
		"x",
		"https://x%zz",
		"https://[::1",
		"https://:443",
		"https://usuario:segredo@[::1",
	} {
		env := validEnv()
		env["PUBLIC_BASE_URL"] = v
		_, err := Load(getenvFrom(env))
		if err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
			t.Errorf("PUBLIC_BASE_URL=%q: erro = %v, queria recusa", v, err)
			continue
		}
		if strings.Contains(err.Error(), "segredo") {
			t.Errorf("PUBLIC_BASE_URL=%q: a mensagem repete a credencial: %v", v, err)
		}
	}
}

func TestLoadAcceptsValidPublicBaseURL(t *testing.T) {
	for _, v := range []string{
		"https://vigilume.example",
		"HTTPS://vigilume.example",
		"https://vigilume.example/",
		"https://vigilume.example/status",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		env := validEnv()
		env["PUBLIC_BASE_URL"] = v
		cfg, err := Load(getenvFrom(env))
		if err != nil {
			t.Errorf("PUBLIC_BASE_URL=%q: %v", v, err)
			continue
		}
		if cfg.PublicBaseURL != v {
			t.Errorf("PUBLIC_BASE_URL=%q guardado como %q", v, cfg.PublicBaseURL)
		}
	}
}

// As chaves de demo só servem numa máquina local. Um compose copiado para um
// servidor com http e um host de verdade (ou 0.0.0.0) também é produção, e o
// esquema sozinho não pega isso (I3 da revisão do Plano 7).
func TestLoadRejectsDemoKeysOnANonLoopbackHost(t *testing.T) {
	for _, base := range []string{
		"http://vigilume.example",
		"http://vigilume.example:8080",
		"http://10.0.0.5:8080",
		"http://192.168.1.10",
		"http://0.0.0.0:8080",
		"http://[::]:8080",
		"http://[2001:db8::1]:8080",
		"http://localhost.vigilume.example",
		"http://127.0.0.1.nip.io",
	} {
		env := validEnv()
		env["PUBLIC_BASE_URL"] = base
		env["JWT_SECRET"] = base64.StdEncoding.EncodeToString(chavesDeDemo[1])
		_, err := Load(getenvFrom(env))
		if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") || !strings.Contains(err.Error(), "demonstração") {
			t.Errorf("chave de demo com %s: erro = %v, queria recusa", base, err)
		}
	}
}

func TestLoadAcceptsDemoKeysOnLoopback(t *testing.T) {
	for _, base := range []string{
		"http://localhost:8080",
		"http://LocalHost:8080",
		"http://localhost",
		"http://127.0.0.1:18080",
		"http://127.8.9.1",
		"http://[::1]:8080",
	} {
		env := validEnv()
		env["PUBLIC_BASE_URL"] = base
		env["MASTER_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[0])
		env["JWT_SECRET"] = base64.StdEncoding.EncodeToString(chavesDeDemo[1])
		env["IP_HASH_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[2])
		if _, err := Load(getenvFrom(env)); err != nil {
			t.Errorf("chaves de demo com %s (máquina local) deveriam subir: %v", base, err)
		}
	}
}

// Credencial na PUBLIC_BASE_URL vai parar em link de e-mail e no log do boot:
// o boot recusa, e a mensagem não repete o valor.
func TestLoadRejectsUserinfoInPublicBaseURL(t *testing.T) {
	for _, v := range []string{
		"https://usuario:segredo@vigilume.example",
		"https://segredo@vigilume.example",
		"http://usuario:segredo@localhost:8080",
		"https://:segredo@vigilume.example",
	} {
		env := validEnv()
		env["PUBLIC_BASE_URL"] = v
		_, err := Load(getenvFrom(env))
		if err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
			t.Errorf("PUBLIC_BASE_URL=%q: erro = %v, queria recusa", v, err)
			continue
		}
		if strings.Contains(err.Error(), "segredo") || strings.Contains(err.Error(), "usuario") {
			t.Errorf("PUBLIC_BASE_URL=%q: a mensagem repete a credencial: %v", v, err)
		}
	}
}

// Um prefixo /0 em TRUSTED_PROXIES confia no X-Forwarded-For (ou no
// CLIENT_IP_HEADER) de qualquer origem: é a falha aberta que a lista existe
// para evitar. Também dentro de uma lista com outros prefixos.
func TestLoadRejectsAZeroBitTrustedProxy(t *testing.T) {
	for _, v := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8, 0.0.0.0/0", "1.2.3.4/0"} {
		env := validEnv()
		env["TRUSTED_PROXIES"] = v
		_, err := Load(getenvFrom(env))
		if err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") || !strings.Contains(err.Error(), "/0") {
			t.Errorf("TRUSTED_PROXIES=%q: erro = %v, queria recusa citando /0", v, err)
		}
	}
}
