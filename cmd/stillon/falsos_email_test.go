package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
)

// emailRecebido é o corpo que o Resend recebe (o resendPayload do notify).
type emailRecebido struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// resendFalso imita a API do Resend em httptest e grava cada e-mail. Criá-lo
// troca novoResend até o fim do teste: todo Resend que o processo montar
// fala com ele. Por isso os testes deste pacote não rodam em paralelo.
type resendFalso struct {
	mu        sync.Mutex
	recebidos []emailRecebido
}

func novoResendFalso(t *testing.T) *resendFalso {
	t.Helper()
	rf := &resendFalso{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e emailRecebido
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "corpo inválido", http.StatusBadRequest)
			return
		}
		rf.mu.Lock()
		rf.recebidos = append(rf.recebidos, e)
		rf.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"falso"}`))
	}))
	t.Cleanup(srv.Close)

	anterior := novoResend
	novoResend = func(chave crypto.Secret, from string) *notify.Resend {
		return notify.NewResendComEndpoint(chave, from, srv.URL, srv.Client())
	}
	t.Cleanup(func() { novoResend = anterior })
	return rf
}

func (rf *resendFalso) lista() []emailRecebido {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return append([]emailRecebido(nil), rf.recebidos...)
}

// esperar devolve os e-mails assim que chegarem pelo menos n.
func (rf *resendFalso) esperar(t *testing.T, n int) []emailRecebido {
	t.Helper()
	prazo := time.Now().Add(10 * time.Second)
	for {
		if got := rf.lista(); len(got) >= n {
			return got
		}
		if time.Now().After(prazo) {
			t.Fatalf("o Resend falso recebeu %d e-mails em 10 s, queria %d", len(rf.lista()), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type tetoFalso struct {
	mu       sync.Mutex
	reservas int
	libera   bool
}

func (t *tetoFalso) Reservar(context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reservas++
	return t.libera, nil
}

func (t *tetoFalso) total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reservas
}

type orcamentosFalsos struct {
	mu     sync.Mutex
	libera bool
}

func (o *orcamentosFalsos) Gastar(context.Context, string, []byte, time.Duration, int) (time.Duration, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.libera {
		return time.Minute, false, nil
	}
	return 0, true, nil
}

func (o *orcamentosFalsos) mudar(libera bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.libera = libera
}
