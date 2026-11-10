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
			"handshake sem cadeia verificada não vale",
			func(t *testing.T) []tls.ConnectionState {
				return []tls.ConnectionState{{ServerName: "exemplo.com"}}
			},
			time.Time{}, false,
		},
		{
			"sem handshake",
			func(t *testing.T) []tls.ConnectionState { return nil },
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
