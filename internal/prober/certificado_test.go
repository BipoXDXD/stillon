package prober

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func dias(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

// certificadoQueVenceEm gera um certificado autoassinado de verdade, com a
// chave criada no próprio teste: nenhum arquivo de chave entra no repositório.
func certificadoQueVenceEm(t *testing.T, notAfter time.Time) *x509.Certificate {
	t.Helper()
	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	modelo := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "exemplo.com"},
		NotBefore:    t0.Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, modelo, modelo, &chave.PublicKey, chave)
	if err != nil {
		t.Fatalf("criar certificado: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ler certificado: %v", err)
	}
	return cert
}

// cadeia monta uma cadeia (folha primeiro) com um certificado por validade.
func cadeia(t *testing.T, validades ...time.Time) []*x509.Certificate {
	t.Helper()
	certs := make([]*x509.Certificate, 0, len(validades))
	for _, v := range validades {
		certs = append(certs, certificadoQueVenceEm(t, v))
	}
	return certs
}

func TestValidadeEfetiva(t *testing.T) {
	cases := []struct {
		nome    string
		cadeias func(t *testing.T) [][]*x509.Certificate
		quer    time.Time
	}{
		{
			"intermediário que vence antes da folha conta",
			func(t *testing.T) [][]*x509.Certificate {
				return [][]*x509.Certificate{cadeia(t, dias(90), dias(30), dias(3650))}
			},
			dias(30),
		},
		{
			"a cadeia alternativa válida vale",
			func(t *testing.T) [][]*x509.Certificate {
				return [][]*x509.Certificate{
					cadeia(t, dias(90), dias(30)),
					cadeia(t, dias(90), dias(60)),
				}
			},
			dias(60),
		},
		{
			"cadeia só com a folha",
			func(t *testing.T) [][]*x509.Certificate {
				return [][]*x509.Certificate{cadeia(t, dias(90))}
			},
			dias(90),
		},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got := validadeEfetiva(c.cadeias(t))
			if !got.Equal(c.quer) {
				t.Errorf("validadeEfetiva = %v, queria %v", got, c.quer)
			}
		})
	}
}

func TestHostDoCertificado(t *testing.T) {
	cases := []struct {
		nome, url, quer string
		ok              bool
	}{
		{"caixa vira minúscula", "https://Exemplo.COM/x", "exemplo.com", true},
		{"ponto final sai", "https://exemplo.com./", "exemplo.com", true},
		{"Unicode vira punycode", "https://ação.com.br/", "xn--ao-siap.com.br", true},
		{"IPv4 literal não tem SNI", "https://203.0.113.5/", "", false},
		{"IPv6 literal não tem SNI", "https://[2001:db8::1]/", "", false},
		{"host que o IDNA recusa", "https://\u200d.example/", "", false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got, ok := hostDoCertificado(c.url)
			if got != c.quer || ok != c.ok {
				t.Errorf("hostDoCertificado(%q) = (%q, %v), queria (%q, %v)", c.url, got, ok, c.quer, c.ok)
			}
		})
	}
}

func estadoTLS(t *testing.T, serverName string, validades ...time.Time) tls.ConnectionState {
	t.Helper()
	return tls.ConnectionState{
		ServerName:     serverName,
		VerifiedChains: [][]*x509.Certificate{cadeia(t, validades...)},
	}
}

func TestValidadeDoHost(t *testing.T) {
	cases := []struct {
		nome    string
		estados func(t *testing.T) []tls.ConnectionState
		quer    time.Time
		ok      bool
	}{
		{
			"redirect para terceiro não conta",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{
					estadoTLS(t, "exemplo.com", dias(30)),
					estadoTLS(t, "outro.com", dias(5)),
				}
			},
			dias(30), true,
		},
		{
			"ServerName com ponto final casa",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{estadoTLS(t, "exemplo.com.", dias(30))}
			},
			dias(30), true,
		},
		{
			"ServerName em maiúsculas casa",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{estadoTLS(t, "EXEMPLO.com", dias(30))}
			},
			dias(30), true,
		},
		{
			"ServerName vazio (IP) não casa",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{estadoTLS(t, "", dias(30))}
			},
			time.Time{}, false,
		},
		{
			"dois handshakes do host: vale o menor",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{
					estadoTLS(t, "exemplo.com", dias(30)),
					estadoTLS(t, "exemplo.com", dias(20)),
				}
			},
			dias(20), true,
		},
		{
			// O certificado apresentado não vale sem cadeia verificada.
			// Sabotagem: cair em PeerCertificates quando VerifiedChains vem vazio.
			"handshake sem cadeia verificada não vale, mesmo com certificado apresentado",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{{ServerName: "exemplo.com", PeerCertificates: cadeia(t, dias(30))}}
			},
			time.Time{}, false,
		},
		{
			"sem handshake",
			func(t *testing.T) []tls.ConnectionState { return nil },
			time.Time{}, false,
		},
		{
			"cadeias verificadas todas vazias não valem",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{{ServerName: "exemplo.com", VerifiedChains: [][]*x509.Certificate{{}}}}
			},
			time.Time{}, false,
		},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got, ok := validadeDoHost("exemplo.com", c.estados(t))
			if !got.Equal(c.quer) || ok != c.ok {
				t.Errorf("validadeDoHost = (%v, %v), queria (%v, %v)", got, ok, c.quer, c.ok)
			}
		})
	}
}

// O host cadastrado é canonicalizado como o ServerName, sem depender de quem
// chama já tê-lo canonicalizado.
func TestValidadeDoHostCanonicalizesTheRegisteredHost(t *testing.T) {
	got, ok := validadeDoHost("Exemplo.COM.", []tls.ConnectionState{estadoTLS(t, "exemplo.com", dias(30))})
	if !got.Equal(dias(30)) || !ok {
		t.Errorf("validadeDoHost = (%v, %v), queria (%v, true)", got, ok, dias(30))
	}
}

func TestSubstituirValidade(t *testing.T) {
	a := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	agora := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	const umMinuto = 60 * time.Second
	antes := func(d time.Duration) time.Time { return agora.Add(-d) }

	cases := []struct {
		nome      string
		guardada  *validadeGuardada
		lida      time.Time
		intervalo time.Duration
		quer      validadeGuardada
		gravar    bool
	}{
		{"sem guardada grava a lida", nil, a, umMinuto, validadeGuardada{a, agora}, true},
		{"menor substitui na hora", &validadeGuardada{b, antes(time.Minute)}, a, umMinuto, validadeGuardada{a, agora}, true},
		{"igual vista há 1 h não renova", &validadeGuardada{a, antes(time.Hour)}, a, umMinuto, validadeGuardada{a, antes(time.Hour)}, false},
		{"igual vista há 1 h + 1 s renova", &validadeGuardada{a, antes(time.Hour + time.Second)}, a, umMinuto, validadeGuardada{a, agora}, true},
		{"maior dentro da janela de 6 h", &validadeGuardada{a, antes(6 * time.Hour)}, b, umMinuto, validadeGuardada{a, antes(6 * time.Hour)}, false},
		{"maior depois da janela de 6 h", &validadeGuardada{a, antes(6*time.Hour + time.Second)}, b, umMinuto, validadeGuardada{b, agora}, true},
		{"intervalo de 3 h: janela de 9 h ainda aberta", &validadeGuardada{a, antes(6*time.Hour + time.Second)}, b, 10800 * time.Second, validadeGuardada{a, antes(6*time.Hour + time.Second)}, false},
		{"intervalo de 3 h: janela de 9 h vencida", &validadeGuardada{a, antes(9*time.Hour + time.Second)}, b, 10800 * time.Second, validadeGuardada{b, agora}, true},
		{"intervalo de 24 h: janela de 72 h no limite", &validadeGuardada{a, antes(72 * time.Hour)}, b, 86400 * time.Second, validadeGuardada{a, antes(72 * time.Hour)}, false},
		{"intervalo de 24 h: janela de 72 h vencida", &validadeGuardada{a, antes(72*time.Hour + time.Second)}, b, 86400 * time.Second, validadeGuardada{b, agora}, true},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got, gravar := substituirValidade(c.guardada, c.lida, agora, c.intervalo)
			if !got.NotAfter.Equal(c.quer.NotAfter) || !got.ObservedAt.Equal(c.quer.ObservedAt) || gravar != c.gravar {
				t.Errorf("substituirValidade = (%+v, %v), queria (%+v, %v)", got, gravar, c.quer, c.gravar)
			}
		})
	}
}

// Um balanceador com certificados diferentes por réplica alterna as leituras.
// A guardada fica no pior (A) e nenhuma leitura grava.
func TestSubstituirValidadeComBalanceadorAlternando(t *testing.T) {
	a := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	agora := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	guardada := validadeGuardada{a, agora.Add(-10 * time.Minute)}

	for i, lida := range []time.Time{b, a, b, a} {
		em := agora.Add(time.Duration(i) * time.Minute)
		nova, gravar := substituirValidade(&guardada, lida, em, 60*time.Second)
		if gravar {
			t.Fatalf("leitura %d (%v em %v) gravou %+v", i, lida, em, nova)
		}
		guardada = nova
	}
	if !guardada.NotAfter.Equal(a) || !guardada.ObservedAt.Equal(agora.Add(-10*time.Minute)) {
		t.Errorf("guardada = %+v, queria {%v %v}", guardada, a, agora.Add(-10*time.Minute))
	}
}
