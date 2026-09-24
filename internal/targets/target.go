// Package targets é o domínio dos alvos de monitoramento: validação, segredo
// dos headers e o CRUD. Toda operação exige userID (spec §5.5) e passa pela
// RLS de storage.InUserTx.
package targets

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("alvo não encontrado")

// Target é o que sai do domínio. Os VALORES dos headers não saem nunca (D5):
// o dono os escreve, o sondador os usa, e ninguém os lê de volta.
type Target struct {
	ID                  uuid.UUID
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32 // nil: sem classificação degraded (spec §11.5)
	Enabled             bool
	HeaderNames         []string
	CreatedAt           time.Time
}

// Input é o cadastro. Zero em Method, IntervalSec, TimeoutMS e ExpectedStatus
// significa "use o padrão"; zero nunca é válido em nenhum deles, então não há
// ambiguidade. DegradedThresholdMS NÃO segue essa regra: nil já significa
// "desativado", e quem traduz "campo ausente" em DefaultDegradedThresholdMS é
// a camada HTTP, a única que distingue ausente de null.
type Input struct {
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32
	Enabled             bool
	Headers             map[string]string
}

// Patch muda só o que vier preenchido. Nos dois campos com Set*, "limpar" é
// uma mudança legítima e diferente de "não mexer", e um ponteiro nil não
// separaria as duas coisas.
type Patch struct {
	Name, URL, Method                      *string
	IntervalSec, TimeoutMS, ExpectedStatus *int32
	SetDegradedThreshold                   bool
	DegradedThresholdMS                    *int32
	Enabled                                *bool
	SetHeaders                             bool
	Headers                                map[string]string
}

type Check struct {
	CheckedAt  time.Time
	Status     string
	LatencyMS  int32
	StatusCode *int32
	ErrorKind  *string
}

// Rollup é uma hora cheia (UTC) de checks de um alvo. OK conta os que não
// foram down; as latências são só deles, e ficam nil numa hora sem sucesso.
type Rollup struct {
	Bucket             time.Time
	Total, OK          int32
	P50, P95, P99, Max *int32
}

// Resolution diz de onde veio a série. Quem escolhe é o servidor, pela
// largura da janela (spec §9.1): o cliente pede tempo, não tabela.
type Resolution string

const (
	ResolutionRaw Resolution = "raw"
	Resolution1h  Resolution = "1h"
)

// Series é a resposta de Service.Checks: Checks quando a resolução é raw,
// Rollups quando é 1h. A outra fica vazia.
type Series struct {
	Resolution Resolution
	Checks     []Check
	Rollups    []Rollup
}

const (
	DefaultMethod                    = "GET"
	DefaultIntervalSec         int32 = 60
	DefaultTimeoutMS           int32 = 10000
	DefaultExpectedStatus      int32 = 200
	DefaultDegradedThresholdMS int32 = 2000

	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100

	// MaxChecksWindow é a janela que a tabela bruta atende (spec §9.1).
	// Acima disso, a série vem de check_rollup_1h (D6 do Plano 2).
	MaxChecksWindow = 48 * time.Hour
	// MaxRollupWindow cobre a retenção dos rollups (13 meses, spec §12):
	// guardar o que não se pode consultar seria só custo. São até 9.600
	// horas por resposta.
	MaxRollupWindow = 400 * 24 * time.Hour
)
