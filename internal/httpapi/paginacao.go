package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// paginaDe lê limit e cursor das listagens paginadas. Fora da faixa ou
// malformado é 400, nunca o padrão em silêncio. Padrão e teto vêm de quem
// chama, porque cada pacote do domínio tem os seus.
func paginaDe(w http.ResponseWriter, r *http.Request, q url.Values, padrao, maximo int32) (int32, *uuid.UUID, bool) {
	limite := padrao
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", targets.ReasonInvalid)
			return 0, nil, false
		}
		if n < 1 || n > int64(maximo) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return 0, nil, false
		}
		limite = int32(n)
	}
	s := q.Get("cursor")
	if s == "" {
		return limite, nil, true
	}
	c, err := uuid.Parse(s)
	if err != nil {
		campoInvalido(w, r, "cursor", targets.ReasonInvalid)
		return 0, nil, false
	}
	return limite, &c, true
}

// proximoCursor devolve o cursor da página seguinte: o id do último item
// quando a página veio cheia, ou "" quando não há continuação. Página cheia
// pode ter continuação; página incompleta, certamente não. Com "" o campo
// sai do JSON (omitempty).
func proximoCursor[T any](items []T, limite int32, id func(T) uuid.UUID) string {
	if len(items) == 0 || len(items) != int(limite) {
		return ""
	}
	return id(items[len(items)-1]).String()
}
