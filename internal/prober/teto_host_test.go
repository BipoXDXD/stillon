package prober

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

var instanteDoTeto = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// baldes é quantos hosts o teto guarda agora.
func (t *tetoPorHost) baldes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.porHost)
}

// A rajada sai no mesmo instante; a ficha seguinte chega no ritmo de 60 por
// minuto, uma por segundo.
func TestHostThrottleAllowsBurstThenWaits(t *testing.T) {
	teto := novoTetoPorHost(RequisicoesPorHostPorMinuto, RajadaPorHost)
	for i := range RajadaPorHost {
		if espera, ok := teto.Reservar("exemplo.com", instanteDoTeto); !ok {
			t.Fatalf("a reserva %d da rajada foi recusada (espera %v)", i+1, espera)
		}
	}
	espera, ok := teto.Reservar("exemplo.com", instanteDoTeto)
	if ok || espera != time.Second {
		t.Fatalf("a reserva além da rajada deu ok=%v, espera %v; queria recusa com 1s", ok, espera)
	}
	if _, ok := teto.Reservar("exemplo.com", instanteDoTeto.Add(time.Second)); !ok {
		t.Error("um segundo depois a ficha nova não chegou")
	}
}

// O balde é do host canônico: caixa, ponto final e grafias do mesmo IP não
// separam hosts. Os hosts chegam como url.URL.Hostname() os devolve (IPv6 sem
// colchetes).
func TestHostThrottleIsPerCanonicalHost(t *testing.T) {
	for _, caso := range []struct{ nome, gasta, depois string }{
		{"caixa", "Exemplo.COM", "exemplo.com"},
		{"ponto final", "exemplo.com.", "exemplo.com"},
		{"IPv6 com zeros", "2001:0db8::1", "2001:db8::1"},
		{"IPv4 mapeado em IPv6", "::ffff:192.0.2.1", "192.0.2.1"},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			teto := novoTetoPorHost(RequisicoesPorHostPorMinuto, RajadaPorHost)
			for range RajadaPorHost {
				teto.Reservar(caso.gasta, instanteDoTeto)
			}
			if _, ok := teto.Reservar(caso.depois, instanteDoTeto); ok {
				t.Errorf("%s ganhou balde próprio; devia dividir o de %s", caso.depois, caso.gasta)
			}
			if _, ok := teto.Reservar("outro.com", instanteDoTeto); !ok {
				t.Errorf("outro.com foi recusado pelo balde de %s", caso.gasta)
			}
		})
	}
}

// Balde parado e cheio sai do mapa: um host visto uma vez não fica para sempre
// na memória do sondador.
func TestHostThrottlePrunesIdleBuckets(t *testing.T) {
	teto := novoTetoPorHost(RequisicoesPorHostPorMinuto, RajadaPorHost)
	teto.Reservar("visto-uma-vez.com", instanteDoTeto)
	teto.Reservar("outro.com", instanteDoTeto.Add(time.Hour))
	if n := teto.baldes(); n != 1 {
		t.Errorf("%d baldes depois de uma hora; queria só o de outro.com", n)
	}
}

// alvosNoHost grava n alvos vencidos agora no projeto, todos com a mesma URL,
// por fora da RLS.
func (b *bancada) alvosNoHost(t *testing.T, projeto uuid.UUID, url string, n int) {
	t.Helper()
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		SELECT gen_random_uuid(), $1, $2, 'alvo', $3, 'GET', 60, 5000, 200, true, $4, $4
		  FROM generate_series(1, $5::int)`,
		b.dono, projeto, url, b.clk.Now(), n); err != nil {
		t.Fatalf("criar alvos no host: %v", err)
	}
}

type alvoAdiado struct {
	id     uuid.UUID
	proxim time.Time
	checks int
}

// estadoDosAlvos lê, por fora da RLS, o next_run_at e quantos checks cada alvo
// tem.
func estadoDosAlvos(t *testing.T) []alvoAdiado {
	t.Helper()
	linhas, err := testdb.Admin(t).Query(context.Background(), `
		SELECT t.id, t.next_run_at, (SELECT count(*) FROM checks c WHERE c.target_id = t.id)
		  FROM targets t`)
	if err != nil {
		t.Fatal(err)
	}
	defer linhas.Close()
	var l []alvoAdiado
	for linhas.Next() {
		var a alvoAdiado
		if err := linhas.Scan(&a.id, &a.proxim, &a.checks); err != nil {
			t.Fatal(err)
		}
		l = append(l, a)
	}
	if err := linhas.Err(); err != nil {
		t.Fatal(err)
	}
	return l
}

// O adiado não vira check nem incidente, volta em segundos (e não um
// intervalo inteiro depois) e devolve a vaga do semáforo na hora.
func TestHostThrottleDefersWithoutCheckOrIncident(t *testing.T) {
	transporte := &transporteContador{}
	b := novaBancada(t, &http.Client{Transport: transporte}, MaxInFlight)
	b.alvosNoHost(t, testdb.ProjetoPadrao(t, b.dono), "https://exemplo.com/saude", 25)

	disparadas, err := b.s.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	if disparadas != RajadaPorHost || len(transporte.requisicoes()) != RajadaPorHost {
		t.Fatalf("Tick disparou %d e a rede viu %d; queria %d", disparadas, len(transporte.requisicoes()), RajadaPorHost)
	}
	adiados := 0
	for _, a := range estadoDosAlvos(t) {
		if a.checks > 0 {
			continue
		}
		adiados++
		if de, ate := b.clk.Now().Add(time.Second), b.clk.Now().Add(5*time.Second); a.proxim.Before(de) || a.proxim.After(ate) {
			t.Errorf("alvo adiado %s volta em %v; queria entre %v e %v", a.id, a.proxim, de, ate)
		}
	}
	if adiados != 5 {
		t.Errorf("%d alvos sem check; queria 5", adiados)
	}
	var incidentes int
	if err := testdb.Admin(t).QueryRow(context.Background(), `SELECT count(*) FROM incidents`).Scan(&incidentes); err != nil {
		t.Fatal(err)
	}
	if incidentes != 0 {
		t.Errorf("%d incidentes abertos pelo adiamento", incidentes)
	}
	if !b.s.sem.TryAcquire(int64(MaxInFlight)) {
		t.Error("o semáforo não voltou a ter todas as vagas depois das sondas")
	}
	if n := testutil.ToFloat64(b.m.hostThrottled); n != 5 {
		t.Errorf("métrica de adiamentos = %v; queria 5", n)
	}
}

// Os adiados do mesmo host não voltam todos no mesmo instante: o i-ésimo
// espera mais i fichas (um segundo cada, a 60 por minuto). Iguais, eles
// seriam reivindicados e adiados de novo a cada segundo, com um UPDATE por
// alvo, tomando as vagas dos outros projetos.
func TestHostThrottleSpreadsDeferredWithinTick(t *testing.T) {
	b := novaBancada(t, &http.Client{Transport: &transporteContador{}}, MaxInFlight)
	b.alvosNoHost(t, testdb.ProjetoPadrao(t, b.dono), "https://exemplo.com/saude", RajadaPorHost+5)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	var voltas []time.Duration
	for _, a := range estadoDosAlvos(t) {
		if a.checks == 0 {
			voltas = append(voltas, a.proxim.Sub(b.clk.Now()))
		}
	}
	slices.Sort(voltas)
	want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second}
	if !slices.Equal(voltas, want) {
		t.Errorf("os adiados voltam em %v; queria %v", voltas, want)
	}
}

// O teto é do host, não do projeto: dois projetos no mesmo host dividem a
// rajada.
func TestHostThrottleSumsAcrossProjects(t *testing.T) {
	transporte := &transporteContador{}
	b := novaBancada(t, &http.Client{Transport: transporte}, MaxInFlight)
	b.alvosNoHost(t, testdb.ProjetoPadrao(t, b.dono), "https://exemplo.com/a", 15)
	b.alvosNoHost(t, testdb.NovoProjeto(t, b.dono, "B"), "https://EXEMPLO.com/b", 15)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	if n := len(transporte.requisicoes()); n != RajadaPorHost {
		t.Errorf("a rede viu %d sondas; queria %d", n, RajadaPorHost)
	}
}
