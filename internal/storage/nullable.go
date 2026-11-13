package storage

import "time"

// TimeOrZero converte uma coluna de tempo anulável (o sqlc a devolve como
// *time.Time) no campo sem ponteiro do domínio: NULL vira o instante zero
// (time.Time{}), e quem lê testa IsZero().
func TimeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
