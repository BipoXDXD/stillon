package targets_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/rollupcut"
	"github.com/BipoXDXD/stillon/internal/targets"
)

const seteDias = 7 * 24 * time.Hour

// serieLonga pede a série de 7 dias terminando agora, como o painel pede.
func serieLonga(t *testing.T, a ambiente, dono, alvo uuid.UUID) map[time.Time]targets.Rollup {
	t.Helper()
	agora := a.clk.Now()
	s, err := a.svc.Checks(context.Background(), dono, alvo, agora.Add(-seteDias), agora)
	if err != nil {
		t.Fatal(err)
	}
	if s.Resolution != targets.Resolution1h {
		t.Fatalf("resolução %q, queria 1h", s.Resolution)
	}
	horas := map[time.Time]targets.Rollup{}
	for i, r := range s.Rollups {
		if r.Bucket.Location() != time.UTC {
			t.Errorf("bucket %v fora de UTC", r.Bucket)
		}
		if i > 0 && !r.Bucket.After(s.Rollups[i-1].Bucket) {
			t.Fatalf("série fora de ordem ou com hora repetida: %v depois de %v", r.Bucket, s.Rollups[i-1].Bucket)
		}
		horas[r.Bucket] = r
	}
	return horas
}

// A janela de 7 dias lê os rollups até a marca d'água e os checks brutos
// depois dela, inclusive a hora corrente: o alvo que cai agora aparece caído.
//
// Relógio em 12:20; H = 12:00. Rollups até 10:00 (a manutenção ainda não
// consolidou 11:00). O check bruto das 10:30 está numa hora já consolidada e
// não pode contar de novo.
func TestChecksLongWindowSeesTheOngoingOutage(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(20 * time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-3*time.Hour), 60, 60, ptr(int32(100)))
	a.gravarRollup(t, alvo, h.Add(-2*time.Hour), 60, 60, ptr(int32(100)))
	a.gravarCheck(t, alvo, h.Add(-90*time.Minute), "down", 5000) // 10:30, já consolidada
	a.gravarCheck(t, alvo, h.Add(-55*time.Minute), "up", 70)     // 11:05
	a.gravarCheck(t, alvo, h.Add(-50*time.Minute), "down", 5000) // 11:10
	a.gravarCheck(t, alvo, h.Add(10*time.Minute), "down", 5000)  // 12:10, hora corrente

	horas := serieLonga(t, a, dono, alvo)
	if len(horas) != 4 {
		t.Errorf("série com %d horas, queria 4 (9:00, 10:00, 11:00 e 12:00): %v", len(horas), horas)
	}
	if dez := horas[h.Add(-2*time.Hour)]; dez.Total != 60 || dez.OK != 60 {
		t.Errorf("10:00 = %+v; queria o rollup (60/60), sem o check bruto das 10:30", dez)
	}
	onze, ok := horas[h.Add(-time.Hour)]
	if !ok || onze.Total != 2 || onze.OK != 1 {
		t.Errorf("11:00 = %+v (presente: %v); queria 2 checks brutos, 1 ok", onze, ok)
	}
	for nome, v := range map[string]*int32{"p50": onze.P50, "p95": onze.P95, "p99": onze.P99, "max": onze.Max} {
		if v == nil || *v != 70 {
			t.Errorf("11:00 %s = %v, queria 70 (só o sucesso conta)", nome, v)
		}
	}
	corrente, ok := horas[h]
	if !ok || corrente.Total != 1 || corrente.OK != 0 {
		t.Fatalf("hora corrente = %+v (presente: %v); queria a queda em curso, 1 check e 0 ok", corrente, ok)
	}
	if corrente.P50 != nil || corrente.P95 != nil || corrente.P99 != nil || corrente.Max != nil {
		t.Errorf("hora corrente sem sucesso tem latência %+v; queria todas nil", corrente)
	}
}

// Fronteira do corte: o check exatamente no corte vem dos brutos; o de um
// instante antes está numa hora já consolidada e só o rollup conta.
func TestChecksLongWindowCutBoundary(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	marca := h.Add(-3 * time.Hour) // 09:00
	corte := marca.Add(time.Hour)  // 10:00
	a.gravarRollup(t, alvo, marca, 10, 10, ptr(int32(100)))
	a.gravarCheck(t, alvo, corte.Add(-time.Microsecond), "down", 5000) // 09:59:59.999999
	a.gravarCheck(t, alvo, corte, "down", 5000)                        // 10:00 em ponto

	horas := serieLonga(t, a, dono, alvo)
	if nove := horas[marca]; nove.Total != 10 || nove.OK != 10 {
		t.Errorf("09:00 = %+v; queria só o rollup (10/10)", nove)
	}
	if dez, ok := horas[corte]; !ok || dez.Total != 1 || dez.OK != 0 {
		t.Errorf("10:00 = %+v (presente: %v); queria o check do corte, 1 down", dez, ok)
	}
}

// Com a manutenção parada, a série não lê mais que rollupcut.MaxRawWindow de
// checks brutos: as horas não consolidadas antes do teto ficam de fora.
func TestChecksLongWindowCapsRawAt24h(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	teto := h.Add(-rollupcut.MaxRawWindow)
	a.gravarCheck(t, alvo, teto.Add(-time.Second), "down", 5000) // antes do teto
	a.gravarCheck(t, alvo, teto, "up", 20)                       // no teto
	a.gravarCheck(t, alvo, h, "down", 5000)                      // hora corrente

	horas := serieLonga(t, a, dono, alvo)
	if len(horas) != 2 {
		t.Errorf("série com %d horas, queria 2 (o teto e a hora corrente): %v", len(horas), horas)
	}
	if _, ok := horas[teto.Add(-time.Hour)]; ok {
		t.Errorf("a hora antes do teto entrou na série; queria fora")
	}
	if r, ok := horas[teto]; !ok || r.Total != 1 || r.OK != 1 {
		t.Errorf("hora do teto = %+v (presente: %v); queria 1 check ok", r, ok)
	}
	if r, ok := horas[h]; !ok || r.Total != 1 || r.OK != 0 {
		t.Errorf("hora corrente = %+v (presente: %v); queria 1 down", r, ok)
	}
}

// Janela que termina no passado: os brutos depois de to não entram, nem os
// rollups depois dele.
func TestChecksLongWindowEndingInThePastStopsAtTo(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	a.gravarRollup(t, alvo, h.Add(-5*time.Hour), 10, 10, ptr(int32(100))) // 07:00, a marca
	a.gravarCheck(t, alvo, h.Add(-4*time.Hour), "up", 20)                 // 08:00, bruto
	a.gravarCheck(t, alvo, h.Add(-90*time.Minute), "down", 5000)          // 10:30, depois de to

	ate := h.Add(-2 * time.Hour) // 10:00
	s, err := a.svc.Checks(context.Background(), dono, alvo, ate.Add(-seteDias), ate)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rollups) != 2 || !s.Rollups[0].Bucket.Equal(h.Add(-5*time.Hour)) || !s.Rollups[1].Bucket.Equal(h.Add(-4*time.Hour)) {
		t.Errorf("série = %+v; queria 07:00 (rollup) e 08:00 (bruto), sem 10:00", s.Rollups)
	}
}

// Caracterização da janela [from, to) da série horária: from no meio da hora
// puxa a hora que o contém, to no meio da hora deixa entrar o rollup dessa
// hora e corta os brutos depois dele, e to em hora cheia é exclusivo. Rede de
// segurança da troca dos parâmetros soltos de serieHoraria por rollupcut.Janela.
//
// Relógio em 12:20 de 22/09. Rollups em 19/09 às 08:00, 09:00 e 10:00 e em
// 22/09 às 08:00 e 09:00 (a marca d'água: o corte vira 10:00). Checks brutos
// às 10:15 e 10:45 de 22/09.
func TestChecksLongWindowCharacterization(t *testing.T) {
	a := novoAmbiente(t)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a.clk.Advance(h.Add(20 * time.Minute).Sub(a.clk.Now()))
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)

	dia19 := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for hora, total := range map[int]int32{8: 11, 9: 12, 10: 13} {
		a.gravarRollup(t, alvo, dia19.Add(time.Duration(hora)*time.Hour), total, total, ptr(int32(100)))
	}
	a.gravarRollup(t, alvo, h.Add(-4*time.Hour), 21, 21, ptr(int32(100))) // 08:00
	a.gravarRollup(t, alvo, h.Add(-3*time.Hour), 22, 22, ptr(int32(100))) // 09:00
	a.gravarCheck(t, alvo, h.Add(-105*time.Minute), "down", 5000)         // 10:15
	a.gravarCheck(t, alvo, h.Add(-75*time.Minute), "down", 5000)          // 10:45

	type hora struct {
		bucket    time.Time
		total, ok int32
	}
	dia22 := func(h, m int) time.Time { return time.Date(2026, 9, 22, h, m, 0, 0, time.UTC) }
	casos := []struct {
		nome   string
		to     time.Time
		queria []hora
	}{
		{"to no meio da hora (10:30): entra o bruto das 10:15 e não o das 10:45", dia22(10, 30), []hora{
			{dia19.Add(10 * time.Hour), 13, 13}, {dia22(8, 0), 21, 21}, {dia22(9, 0), 22, 22}, {dia22(10, 0), 1, 0},
		}},
		{"to antes do corte (08:30): sem brutos, o rollup das 08:00 entra", dia22(8, 30), []hora{
			{dia19.Add(8 * time.Hour), 11, 11}, {dia19.Add(9 * time.Hour), 12, 12}, {dia19.Add(10 * time.Hour), 13, 13}, {dia22(8, 0), 21, 21},
		}},
		{"to em hora cheia (09:00): exclusivo", dia22(9, 0), []hora{
			{dia19.Add(9 * time.Hour), 12, 12}, {dia19.Add(10 * time.Hour), 13, 13}, {dia22(8, 0), 21, 21},
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, err := a.svc.Checks(context.Background(), dono, alvo, c.to.Add(-3*24*time.Hour), c.to)
			if err != nil {
				t.Fatal(err)
			}
			var got []hora
			for _, r := range s.Rollups {
				got = append(got, hora{r.Bucket, r.Total, r.OK})
			}
			if len(got) != len(c.queria) {
				t.Fatalf("série = %+v, queria %+v", got, c.queria)
			}
			for i := range got {
				if !got[i].bucket.Equal(c.queria[i].bucket) || got[i].total != c.queria[i].total || got[i].ok != c.queria[i].ok {
					t.Errorf("hora %d = %+v, queria %+v", i, got[i], c.queria[i])
				}
			}
		})
	}
}
