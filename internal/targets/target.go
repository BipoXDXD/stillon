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

const (
	DefaultMethod                    = "GET"
	DefaultIntervalSec         int32 = 60
	DefaultTimeoutMS           int32 = 10000
	DefaultExpectedStatus      int32 = 200
	DefaultDegradedThresholdMS int32 = 2000

	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100

	// MaxChecksWindow é a janela que a tabela bruta atende (spec §9.1).
	// Acima disso, rollups, que chegam no Plano 4 (D6).
	MaxChecksWindow = 48 * time.Hour
)
