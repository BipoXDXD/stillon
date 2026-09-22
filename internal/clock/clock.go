// Package clock isola a leitura do tempo para que a lógica dependente de
// relógio seja testável sem time.Sleep.
//
// A spec (§6.2) exige isso para o agendador do Plano 2; as sessões são o
// primeiro consumidor.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake é seguro para uso concorrente porque os testes do agendador vão
// avançar o tempo de uma goroutine enquanto outras o leem.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
