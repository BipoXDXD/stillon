// Package rollupcut é a regra de onde a leitura de uptime troca os rollups
// horários pelos checks brutos, a única do sistema: o dashboard
// (GET /targets/summary) e a página pública (/p/{slug}) a chamam, e por isso
// não divergem (C4 da revisão).
//
// A manutenção consolida as horas cheias a cada 15 min. Ler só os rollups
// deixa a tela até ~1h15 atrasada e cega à queda em curso; ler só os brutos
// é caro. Então: rollups em [início, corte) e checks brutos de corte em
// diante, com o corte lido uma vez, antes das duas consultas, para nenhuma
// hora contar duas vezes.
//
// A marca d'água que entra aqui é o último rollup ANTES da hora corrente: as
// consultas de marca (LatestUserRollupBucket, LatestPageRollupBucket e a
// LatestRollupBucketBefore da manutenção) ignoram o rollup gravado no futuro
// por um relógio adiantado (C8 da revisão).
package rollupcut

import "time"

// At devolve o corte: a hora seguinte à marca d'água, limitada a
// [from, hour], em UTC. from é o início da janela lida, e hour é a hora
// corrente (truncada). Sem rollup, a marca é a época Unix e o corte vira from.
func At(watermark, from, hour time.Time) time.Time {
	corte := watermark.UTC().Add(time.Hour)
	if corte.Before(from) {
		return from.UTC()
	}
	if corte.After(hour) {
		return hour.UTC()
	}
	return corte
}
