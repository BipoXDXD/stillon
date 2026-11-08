package prober

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Os testes deste arquivo fecham lacunas achadas por mutação: cada um falha
// com uma regra específica do sondador desligada.

// A linha reivindicada vira a Spec da sonda com as unidades certas.
func TestSpecComesFromTheClaimedRow(t *testing.T) {
	limiar := int32(250)
	for _, tc := range []struct {
		nome   string
		limiar *int32
		quer   time.Duration
	}{
		{"com limiar", &limiar, 250 * time.Millisecond},
		{"sem limiar", nil, 0},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			s := &Scheduler{}
			spec, err := s.especificar(db.ClaimDueTargetsRow{
				ID: uuid.New(), URL: "https://exemplo.com", Method: http.MethodHead,
				TimeoutMS: 1500, ExpectedStatus: 204, DegradedThresholdMS: tc.limiar,
			})
			if err != nil {
				t.Fatal(err)
			}
			if spec.URL != "https://exemplo.com" || spec.Method != http.MethodHead || spec.Timeout != 1500*time.Millisecond ||
				spec.ExpectedStatus != 204 || spec.DegradedThreshold != tc.quer || spec.Headers != nil {
				t.Errorf("spec = %+v, queria limiar de %s", spec, tc.quer)
			}
		})
	}
}

// Depois de uma lacuna, os dois contadores recomeçam, e não só o de falhas.
func TestLongGapAlsoRestartsTheSuccessStreak(t *testing.T) {
	ultimo := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	antes := incident.State{ConsecutiveSuccesses: 2, StreakSince: ultimo, LastCheckedAt: ultimo, Open: true}
	depois := recomecarAposLacuna(antes, ultimo.Add(time.Hour), 60)
	if depois.ConsecutiveSuccesses != 0 || !depois.Open {
		t.Errorf("estado = %+v; queria 0 sucessos e o incidente ainda aberto", depois)
	}
}

func TestInt32Limited(t *testing.T) {
	for _, tc := range []struct {
		entrada int64
		quer    int32
	}{
		{-1, 0},
		{0, 0},
		{1500, 1500},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
	} {
		if got := int32Limitado(tc.entrada); got != tc.quer {
			t.Errorf("int32Limitado(%d) = %d, queria %d", tc.entrada, got, tc.quer)
		}
	}
}

func TestLooksLikeAVanishedTarget(t *testing.T) {
	pg := func(codigo string) error { return &pgconn.PgError{Code: codigo} }
	for _, tc := range []struct {
		nome string
		err  error
		quer bool
	}{
		{"RLS", pg("42501"), true},
		{"chave estrangeira", pg("23503"), true},
		{"RLS embrulhada", fmt.Errorf("gravar: %w", pg("42501")), true},
		{"outro código", pg("23505"), false},
		{"erro que não é do Postgres", errors.New("rede"), false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			if got := pareceAlvoSumido(tc.err); got != tc.quer {
				t.Errorf("pareceAlvoSumido = %v, queria %v", got, tc.quer)
			}
		})
	}
}

// Sem resposta, o check fica sem status_code: NULL, e não zero.
func TestCheckWithoutResponseHasNoStatusCode(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}

	b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})

	rows := b.checks(t, alvo)
	if len(rows) != 1 || rows[0].StatusCode != nil {
		t.Fatalf("checks = %+v; queria um, sem status_code", rows)
	}
}
