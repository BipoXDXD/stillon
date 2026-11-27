package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// tentativasDoRefund guarda o erro do contexto que o Refund recebe.
type tentativasDoRefund struct{ erroNoRefund error }

func (a *tentativasDoRefund) Reserve(context.Context) (bool, error) { return true, nil }
func (a *tentativasDoRefund) Refund(ctx context.Context) error {
	a.erroNoRefund = ctx.Err()
	return a.erroNoRefund
}
func (a *tentativasDoRefund) Reset(context.Context) error { return nil }

// A reserva de uma prova certa volta mesmo com o cliente que já desistiu: com
// o contexto da requisição, a devolução falharia e a prova certa ficaria
// contada como errada.
func TestDevolverTentativaIgnoraOCancelamentoDaRequisicao(t *testing.T) {
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	tentativas := &tentativasDoRefund{}

	devolverTentativa(r, tentativas)

	if tentativas.erroNoRefund != nil {
		t.Errorf("o Refund recebeu o contexto em %v; queria fora do cancelamento", tentativas.erroNoRefund)
	}
}
