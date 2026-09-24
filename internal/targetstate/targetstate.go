// Package targetstate é a regra do estado atual de um alvo, a única do
// sistema (D15 do Plano 5): a página pública (/p/{slug}) e o dashboard
// (GET /targets/summary) a chamam, e por isso não divergem.
package targetstate

import "time"

type State string

const (
	Up       State = "up"
	Degraded State = "degraded"
	Down     State = "down"
	// Unknown: sem check recente. Alvo desativado, sondador parado ou alvo
	// que nunca foi sondado; nenhuma tela afirma o que não sabe.
	Unknown State = "unknown"
)

// StaleIntervals: o último check mais velho que isto, em intervalos do alvo,
// não diz mais nada sobre o presente. É o mesmo 3 da lacuna da histerese
// (Plano 3) e o D15 do Plano 4. O limite é inclusivo.
const StaleIntervals = 3

// Current é o estado do último check, se ele ainda é recente. lastStatus
// vazio quer dizer "nunca houve check".
func Current(enabled bool, interval time.Duration, lastStatus string, lastCheckedAt, now time.Time) State {
	recente := now.Sub(lastCheckedAt) <= StaleIntervals*interval
	if !enabled || lastStatus == "" || !recente {
		return Unknown
	}
	switch s := State(lastStatus); s {
	case Up, Degraded, Down:
		return s
	}
	return Unknown
}
