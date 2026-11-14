package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// provedorQueRedireciona responde ao POST com um 302 único para um caminho
// que aceita qualquer método com 200. Seguir o redirect troca o POST por um
// GET sem corpo, e o 200 do GET viraria "enviado" sem a mensagem sair.
func provedorQueRedireciona(t *testing.T) (*httptest.Server, *http.Client, *atomic.Int32) {
	t.Helper()
	var seguiu atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destino" {
			seguiu.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", "/destino")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return srv, safedial.NewClientAllowing(0, ap), &seguiu
}

func TestDiscordSingleRedirectIsNotDelivered(t *testing.T) {
	srv, client, seguiu := provedorQueRedireciona(t)
	d := &Discord{client: client, validate: aceitarQualquer}
	err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("302 único deu %+v, queria invalid_channel permanente", se)
	}
	if n := seguiu.Load(); n != 0 {
		t.Errorf("o redirect foi seguido %d vez(es): o POST virou GET", n)
	}
}

func TestResendSingleRedirectIsNotDelivered(t *testing.T) {
	srv, client, seguiu := provedorQueRedireciona(t)
	r := &Resend{client: client, endpoint: srv.URL + "/emails", apiKey: crypto.Secret("re_chave"), from: "StillOn <alertas@stillon.example>"}
	err := r.Send(context.Background(), crypto.Secret("pessoa@exemplo.com"), evAberto)
	// O endpoint do Resend é nosso, não do canal: o endereço de e-mail não
	// tem culpa do redirect, então a falha é rejected, não invalid_channel.
	if se := comoSendError(t, err); se.Reason != ReasonRejected || !se.Permanent {
		t.Errorf("302 único deu %+v, queria rejected permanente", se)
	}
	if n := seguiu.Load(); n != 0 {
		t.Errorf("o redirect foi seguido %d vez(es): o POST virou GET", n)
	}
}
