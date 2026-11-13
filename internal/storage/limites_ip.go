package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// OciosoMinimoPorIP é quanto uma linha parada sobrevive à limpeza, no mínimo.
// É o mesmo piso do limitador em memória (middleware.ociosoMinimo).
const OciosoMinimoPorIP = 10 * time.Minute

// OrcamentosPorIP guarda os baldes do limite por IP no Postgres
// (ip_rate_limits, migration 00036), e é isso que faz réplicas da API somarem
// as tentativas de uma origem no mesmo orçamento. O valor zero não serve: use
// NewOrcamentosPorIP.
type OrcamentosPorIP struct {
	banco Beginner
	clk   clock.Clock
}

// NewOrcamentosPorIP usa o relógio injetado como o agora de cada gasto, e não
// o now() do banco: o mesmo relógio que os testes controlam.
func NewOrcamentosPorIP(banco Beginner, clk clock.Clock) *OrcamentosPorIP {
	return &OrcamentosPorIP{banco: banco, clk: clk}
}

// Gastar tira uma ficha do balde (balde, chave), que recebe uma ficha a cada
// aCada até rajada. Sem ficha inteira, devolve false e quanto falta para a
// próxima, e nada é gasto. chave é o HMAC da origem: o IP não chega aqui.
func (o *OrcamentosPorIP) Gastar(ctx context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error) {
	agora := o.clk.Now()
	// Apagar uma linha é devolver o balde cheio, o que só é inofensivo depois
	// de ele ter tido tempo de encher sozinho.
	ocioso := max(OciosoMinimoPorIP, aCada*time.Duration(rajada))
	var linha db.SpendIPRateLimitQuotaRow
	err := InIPRateLimitTx(ctx, o.banco, func(q *db.Queries) error {
		var err error
		linha, err = q.SpendIPRateLimitQuota(ctx, db.SpendIPRateLimitQuotaParams{
			Bucket:          balde,
			KeyHash:         chave,
			Burst:           float64(rajada),
			Now:             agora,
			SecondsPerToken: aCada.Seconds(),
			IdleBefore:      agora.Add(-ocioso),
		})
		return err
	})
	if err != nil {
		return 0, false, fmt.Errorf("gastar ficha do limite por IP: %w", err)
	}
	if linha.LastAllowed {
		return 0, true, nil
	}
	return time.Duration((1 - linha.Available) * float64(aCada)), false, nil
}
