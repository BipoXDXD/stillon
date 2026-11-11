package safedial

import (
	"net/http"
	"net/netip"
	"time"
)

// NewClientAllowingBlocking junta, só para teste, a exceção de loopback do
// NewClientAllowing e as faixas da hospedagem do NewClientBlocking. Todo
// servidor de teste escuta em loopback: liberá-lo pela porta e pô-lo na lista
// da hospedagem é a única forma de provar que a lista vence, sem tocar a rede.
func NewClientAllowingBlocking(timeout time.Duration, hospedagem []netip.Prefix, allowed ...netip.AddrPort) *http.Client {
	return newClient(timeout, normalizarLiberados(allowed), hospedagem)
}
