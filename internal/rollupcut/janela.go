package rollupcut

import (
	"errors"
	"time"
)

// ErrJanelaInvalida é o erro de NovaJanela: intervalo vazio, invertido ou com
// algum instante zero.
var ErrJanelaInvalida = errors.New("rollupcut: janela inválida")

// Janela é o intervalo semiaberto [desde, ate) lido numa série de uptime, junto
// com a hora corrente em que o corte é calculado. Reúne o que antes andava em
// parâmetros soltos (início, fim, relógio, hora truncada) e nasce válida: o
// valor zero não é uma janela, use NovaJanela. É imutável.
type Janela struct {
	desde time.Time // truncado à hora, em UTC
	ate   time.Time
	hora  time.Time // hora corrente, truncada, em UTC
}

// NovaJanela devolve a janela [desde, ate) lida na hora corrente de agora.
// Falha com ErrJanelaInvalida se algum instante for zero ou se desde não vier
// estritamente antes de ate (um intervalo semiaberto vazio não é uma leitura).
func NovaJanela(desde, ate, agora time.Time) (Janela, error) {
	if desde.IsZero() || ate.IsZero() || agora.IsZero() || !desde.Before(ate) {
		return Janela{}, ErrJanelaInvalida
	}
	return Janela{
		desde: desde.UTC().Truncate(time.Hour),
		ate:   ate,
		hora:  agora.UTC().Truncate(time.Hour),
	}, nil
}

// Desde é o início da leitura: a hora cheia que contém o desde recebido, em
// UTC, para que a hora parcial do começo entre.
func (j Janela) Desde() time.Time { return j.desde }

// Ate é o fim exclusivo da janela, como recebido.
func (j Janela) Ate() time.Time { return j.ate }

// Hora é a hora corrente, truncada, em UTC.
func (j Janela) Hora() time.Time { return j.hora }

// Corte é At(marca, Desde(), Hora()): onde a leitura troca os rollups pelos
// checks brutos, dada a marca d'água dos rollups.
func (j Janela) Corte(marca time.Time) time.Time {
	return At(marca, j.desde, j.hora)
}
