package prober

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// agoraDoTLS é o instante em que o cliente de teste verifica os certificados.
// Fixo, para que as validades literais dos testes (dezembro de 2026, janeiro
// de 2027) não passem a falhar quando o relógio real as alcançar.
var agoraDoTLS = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

// cadeiaDeTeste monta raiz, intermediário e folha com o SAN host (DNS ou IP).
// O servidor entrega a folha e o intermediário; a raiz fica com o cliente.
// As chaves nascem no teste: nenhum arquivo de chave entra no repositório.
func cadeiaDeTeste(t *testing.T, host string, folhaAte, intermediarioAte time.Time) (*x509.Certificate, tls.Certificate) {
	t.Helper()
	inicio := agoraDoTLS.Add(-30 * 24 * time.Hour)
	raizAte := agoraDoTLS.Add(10 * 365 * 24 * time.Hour)

	chaveRaiz := gerarChave(t)
	raiz := assinar(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Raiz de teste"},
		NotBefore: inicio, NotAfter: raizAte, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}, nil, &chaveRaiz.PublicKey, chaveRaiz)

	chaveInter := gerarChave(t)
	inter := assinar(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Intermediário de teste"},
		NotBefore: inicio, NotAfter: intermediarioAte, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}, raiz, &chaveInter.PublicKey, chaveRaiz)

	chaveFolha := gerarChave(t)
	modeloFolha := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: host},
		NotBefore: inicio, NotAfter: folhaAte,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		modeloFolha.IPAddresses = []net.IP{ip}
	} else {
		modeloFolha.DNSNames = []string{host}
	}
	folha := assinar(t, modeloFolha, inter, &chaveFolha.PublicKey, chaveInter)

	return raiz, tls.Certificate{Certificate: [][]byte{folha.Raw, inter.Raw}, PrivateKey: chaveFolha}
}

func gerarChave(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	return chave
}

// assinar cria o certificado do modelo assinado por emissor (nil: autoassinado).
func assinar(t *testing.T, modelo, emissor *x509.Certificate, publica *ecdsa.PublicKey, chaveDoEmissor *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	if emissor == nil {
		emissor = modelo
	}
	der, err := x509.CreateCertificate(rand.Reader, modelo, emissor, publica, chaveDoEmissor)
	if err != nil {
		t.Fatalf("criar certificado %q: %v", modelo.Subject.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ler certificado %q: %v", modelo.Subject.CommonName, err)
	}
	return cert
}

// clienteComRaiz é o cliente de produção (safedial) com as portas liberadas e
// a raiz de teste como única confiável. A verificação continua ligada: só o
// conjunto de raízes e o relógio da verificação mudam, e só aqui no teste.
func clienteComRaiz(t *testing.T, raiz *x509.Certificate, liberados ...netip.AddrPort) *http.Client {
	t.Helper()
	client := safedial.NewClientAllowing(0, liberados...)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transporte de produção = %T, queria *http.Transport", client.Transport)
	}
	raizes := x509.NewCertPool()
	raizes.AddCert(raiz)
	transport.TLSClientConfig = &tls.Config{RootCAs: raizes, Time: func() time.Time { return agoraDoTLS }}
	t.Cleanup(transport.CloseIdleConnections)
	return client
}

// confiarTambem acrescenta outra raiz ao cliente de clienteComRaiz, para um
// redirect cujo destino tem cadeia própria.
func confiarTambem(t *testing.T, client *http.Client, raiz *x509.Certificate) {
	t.Helper()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("confiarTambem exige um cliente de clienteComRaiz")
	}
	transport.TLSClientConfig.RootCAs.AddCert(raiz)
}

// resolverDeTeste troca o net.DefaultResolver, que é por onde o safedial
// resolve, por um que responde ip a qualquer nome, dentro do processo (Dial
// em net.Pipe). Por trocar um global, os testes que o usam não chamam
// t.Parallel. Cópia do instalarResolver de safedial/dns_interno_test.go.
func resolverDeTeste(t *testing.T, ip string) {
	t.Helper()
	endereco := netip.MustParseAddr(ip)
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			cliente, servidor := net.Pipe()
			go responderDNS(servidor, endereco)
			return cliente, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
}

// responderDNS atende uma consulta no enquadramento de stream (RFC 1035
// §4.2.2). Um erro só fecha a conexão: o resolver falha, e o teste vê down
// dns em vez do resultado que exige.
func responderDNS(conn net.Conn, endereco netip.Addr) {
	defer conn.Close()
	var tamanho [2]byte
	if _, err := io.ReadFull(conn, tamanho[:]); err != nil {
		return
	}
	consulta := make([]byte, binary.BigEndian.Uint16(tamanho[:]))
	if _, err := io.ReadFull(conn, consulta); err != nil {
		return
	}
	resposta, err := montarRespostaComEndereco(consulta, endereco)
	if err != nil {
		return
	}
	quadro := binary.BigEndian.AppendUint16(nil, uint16(len(resposta)))
	_, _ = conn.Write(append(quadro, resposta...))
}

func montarRespostaComEndereco(consulta []byte, endereco netip.Addr) ([]byte, error) {
	const ttlSegundos = 60
	var leitor dnsmessage.Parser
	cabecalho, err := leitor.Start(consulta)
	if err != nil {
		return nil, err
	}
	pergunta, err := leitor.Question()
	if err != nil {
		return nil, err
	}
	escritor := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: cabecalho.ID, Response: true, Authoritative: true, RCode: dnsmessage.RCodeSuccess,
	})
	if err := escritor.StartQuestions(); err != nil {
		return nil, err
	}
	if err := escritor.Question(pergunta); err != nil {
		return nil, err
	}
	if err := escritor.StartAnswers(); err != nil {
		return nil, err
	}
	registro := dnsmessage.ResourceHeader{Name: pergunta.Name, Class: dnsmessage.ClassINET, TTL: ttlSegundos}
	switch {
	case pergunta.Type == dnsmessage.TypeA && endereco.Is4():
		err = escritor.AResource(registro, dnsmessage.AResource{A: endereco.As4()})
	case pergunta.Type == dnsmessage.TypeAAAA && !endereco.Is4():
		err = escritor.AAAAResource(registro, dnsmessage.AAAAResource{AAAA: endereco.As16()})
	}
	if err != nil {
		return nil, err
	}
	return escritor.Finish()
}
