package prober

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Cada campo recebe um valor distinto dos demais: trocar dois campos na
// montagem da linha (id por alvo, status por tipo de erro) falha aqui.
func TestParamsDoCheck_ResultadoCompleto(t *testing.T) {
	id := uuid.MustParse("11111111-1111-7111-8111-111111111111")
	alvoID := uuid.MustParse("22222222-2222-7222-8222-222222222222")
	em := time.Date(2026, 11, 11, 10, 20, 30, 0, time.UTC)

	p := paramsDoCheck(id, alvoID, em, Result{
		Status:     StatusDown,
		Latency:    1234 * time.Millisecond,
		StatusCode: 503,
		ErrorKind:  KindHTTPStatus,
	})

	if p.ID != id {
		t.Errorf("ID = %v, quer %v", p.ID, id)
	}
	if p.TargetID != alvoID {
		t.Errorf("TargetID = %v, quer %v", p.TargetID, alvoID)
	}
	if !p.CheckedAt.Equal(em) {
		t.Errorf("CheckedAt = %v, quer %v", p.CheckedAt, em)
	}
	if p.Status != "down" {
		t.Errorf("Status = %q, quer %q", p.Status, "down")
	}
	if p.LatencyMS != 1234 {
		t.Errorf("LatencyMS = %d, quer 1234", p.LatencyMS)
	}
	if p.StatusCode == nil {
		t.Error("StatusCode = nil, quer 503")
	} else if *p.StatusCode != 503 {
		t.Errorf("StatusCode = %d, quer 503", *p.StatusCode)
	}
	if p.ErrorKind == nil {
		t.Error(`ErrorKind = nil, quer "http_status"`)
	} else if *p.ErrorKind != "http_status" {
		t.Errorf("ErrorKind = %q, quer %q", *p.ErrorKind, "http_status")
	}
}

func TestParamsDoCheck_SemRespostaNemErro(t *testing.T) {
	p := paramsDoCheck(uuid.New(), uuid.New(), time.Now(), Result{Status: StatusUp, Latency: 80 * time.Millisecond})

	if p.StatusCode != nil {
		t.Errorf("StatusCode = %d, quer nil quando não houve resposta", *p.StatusCode)
	}
	if p.ErrorKind != nil {
		t.Errorf("ErrorKind = %q, quer nil quando não é down", *p.ErrorKind)
	}
}

func TestParamsDoCheck_LimitaALatenciaAoInt32(t *testing.T) {
	const umAno = 365 * 24 * time.Hour
	p := paramsDoCheck(uuid.New(), uuid.New(), time.Now(), Result{Status: StatusDown, Latency: umAno})

	if p.LatencyMS != math.MaxInt32 {
		t.Errorf("LatencyMS = %d, quer %d", p.LatencyMS, int32(math.MaxInt32))
	}
}

// Um relógio que andou para trás deixa a latência negativa; a coluna não
// aceita (CHECK latency_ms >= 0), então a gravação trava em 0.
func TestParamsDoCheck_LatenciaNegativaViraZero(t *testing.T) {
	p := paramsDoCheck(uuid.New(), uuid.New(), time.Now(), Result{Status: StatusUp, Latency: -5 * time.Millisecond})

	if p.LatencyMS != 0 {
		t.Errorf("LatencyMS = %d, quer 0", p.LatencyMS)
	}
}
