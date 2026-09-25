package httpapi

import (
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// comEnvelope põe o 404 e o 405 do próprio ServeMux no envelope de erro (B5
// do Plano 10, spec §9.1: todo erro da API tem error.code e trace_id).
//
// Só as requisições sem padrão (Handler devolve pattern "") passam pelo
// interceptador: com padrão, quem responde é o handler da rota, e um 404
// dele (recurso alheio, id malformado) já sai no envelope. Sem padrão, o
// ServeMux responde 404 ou 405 (com o Allow, que fica). O redirecionamento
// para o caminho limpo ("//" vira "/") não chega aqui: a raiz de Routes, que
// também é um ServeMux, o faz antes. Qualquer outro status passa intacto.
func comEnvelope(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, padrao := mux.Handler(r); padrao == "" {
			w = &erroDoMux{ResponseWriter: w, r: r}
		}
		mux.ServeHTTP(w, r)
	})
}

// erroDoMux troca a resposta de texto do ServeMux pelo envelope. O
// http.Error do ServeMux chama WriteHeader e depois Write com o texto; o
// envelope sai no WriteHeader, e o texto é descartado.
type erroDoMux struct {
	http.ResponseWriter
	r       *http.Request
	trocado bool
}

func (e *erroDoMux) WriteHeader(status int) {
	var codigo apierr.ErrorCode
	switch status {
	case http.StatusNotFound:
		codigo = apierr.CodeNotFound
	case http.StatusMethodNotAllowed:
		codigo = apierr.CodeMethodNotAllowed
	default:
		e.ResponseWriter.WriteHeader(status)
		return
	}
	e.trocado = true
	// O http.Error já pôs o nosniff e o text/plain; o WriteJSON troca o tipo.
	apierr.WriteError(e.ResponseWriter, e.r, codigo)
}

func (e *erroDoMux) Write(b []byte) (int, error) {
	if e.trocado {
		return len(b), nil
	}
	return e.ResponseWriter.Write(b)
}
