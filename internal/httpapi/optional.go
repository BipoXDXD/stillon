package httpapi

import "encoding/json"

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
	return json.Unmarshal(b, &o.Value)
}
