package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// optional distingue os três estados de um campo JSON: ausente, null e
// valor. *T colapsa os dois primeiros, e em degraded_threshold_ms eles
// significam coisas opostas: ausente é "use o padrão de 2000 ms", null é
// "desative a classificação degraded" (spec §11.5).
//
// Funciona porque encoding/json chama UnmarshalJSON também para null,
// quando o campo não é ponteiro; se a chave não aparece, não chama nada, e
// Set fica false.
type optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	// O decoder do corpo não alcança aqui: sem isto, um campo desconhecido
	// dentro de schedule ou items sumiria em silêncio.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(&o.Value)
}

// ponteiro é nil só com o campo ausente. O null de campo obrigatório não chega
// aqui: recusouNulo responde antes.
func (o optional[T]) ponteiro() *T {
	if !o.Set {
		return nil
	}
	return &o.Value
}

// campoDoPatch é um campo obrigatório de PATCH e se ele veio null.
type campoDoPatch struct {
	nome string
	nulo bool
}

// recusouNulo responde 400 se algum campo obrigatório veio null: no PATCH,
// ausente é "não mexa", e null seria pedir o recurso sem o campo. A recusa vem
// antes de buscar o recurso, e por isso é a mesma para o alheio e o inexistente.
func recusouNulo(w http.ResponseWriter, r *http.Request, campos []campoDoPatch) bool {
	var nulos []apierr.FieldError
	for _, c := range campos {
		if c.nulo {
			nulos = append(nulos, apierr.FieldError{Field: c.nome, Reason: "required"})
		}
	}
	if len(nulos) == 0 {
		return false
	}
	apierr.WriteValidation(w, r, nulos)
	return true
}
