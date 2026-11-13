package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Os números do login: uma ficha a cada 6 s, rajada de 5.
const (
	baldeDeTeste  = "login"
	fichaACada    = 6 * time.Second
	rajadaDeTeste = 5
)

func chaveDeTeste(nome string) []byte {
	soma := sha256.Sum256([]byte(nome))
	return soma[:]
}

type gasto struct {
	espera    time.Duration
	permitido bool
}

func gastar(t *testing.T, o *storage.OrcamentosPorIP, balde string, chave []byte) gasto {
	t.Helper()
	espera, permitido, err := o.Gastar(context.Background(), balde, chave, fichaACada, rajadaDeTeste)
	if err != nil {
		t.Fatalf("gastar ficha: %v", err)
	}
	return gasto{espera: espera, permitido: permitido}
}

func esgotarRajada(t *testing.T, o *storage.OrcamentosPorIP, chave []byte) {
	t.Helper()
	for i := range rajadaDeTeste {
		if g := gastar(t, o, baldeDeTeste, chave); !g.permitido {
			t.Fatalf("pedido %d da rajada recusado", i+1)
		}
	}
}

func TestIPBudgetAllowsTheBurstAndRefusesTheNext(t *testing.T) {
	o := storage.NewOrcamentosPorIP(testdb.New(t), clock.NewFake(agora))
	chave := chaveDeTeste("a")

	esgotarRajada(t, o, chave)
	g := gastar(t, o, baldeDeTeste, chave)

	if g.permitido {
		t.Fatal("o pedido além da rajada passou")
	}
	if g.espera != fichaACada {
		t.Errorf("espera = %v, queria %v", g.espera, fichaACada)
	}
}

func TestIPBudgetRefillsOnTheInjectedClock(t *testing.T) {
	clk := clock.NewFake(agora)
	o := storage.NewOrcamentosPorIP(testdb.New(t), clk)
	chave := chaveDeTeste("a")
	esgotarRajada(t, o, chave)

	clk.Advance(fichaACada - time.Millisecond)
	antes := gastar(t, o, baldeDeTeste, chave)
	if antes.permitido {
		t.Fatal("passou um milissegundo antes da ficha")
	}
	if got := antes.espera.Round(time.Microsecond); got != time.Millisecond {
		t.Errorf("espera um milissegundo antes = %v, queria 1ms", got)
	}

	clk.Advance(time.Millisecond)
	if g := gastar(t, o, baldeDeTeste, chave); !g.permitido {
		t.Error("recusado com a ficha de volta")
	}
}

// Recusar não pode gastar a ficha que o cliente receberia ao respeitar o
// Retry-After, como no limitador em memória (reservar, com CancelAt).
func TestIPBudgetRefusalSpendsNothing(t *testing.T) {
	clk := clock.NewFake(agora)
	o := storage.NewOrcamentosPorIP(testdb.New(t), clk)
	chave := chaveDeTeste("a")
	esgotarRajada(t, o, chave)
	for range 3 {
		gastar(t, o, baldeDeTeste, chave)
	}

	clk.Advance(fichaACada)

	if g := gastar(t, o, baldeDeTeste, chave); !g.permitido {
		t.Error("as recusas gastaram a ficha que voltou")
	}
}

func TestIPBudgetNeverRefillsPastTheBurst(t *testing.T) {
	clk := clock.NewFake(agora)
	o := storage.NewOrcamentosPorIP(testdb.New(t), clk)
	chave := chaveDeTeste("a")
	gastar(t, o, baldeDeTeste, chave)

	clk.Advance(time.Hour)
	esgotarRajada(t, o, chave)

	if g := gastar(t, o, baldeDeTeste, chave); g.permitido {
		t.Error("uma hora parada rendeu mais que a rajada")
	}
}

func TestIPBudgetSeparatesKeysAndBuckets(t *testing.T) {
	o := storage.NewOrcamentosPorIP(testdb.New(t), clock.NewFake(agora))
	a := chaveDeTeste("a")
	esgotarRajada(t, o, a)

	if g := gastar(t, o, baldeDeTeste, chaveDeTeste("b")); !g.permitido {
		t.Error("a chave b pagou pela a")
	}
	if g := gastar(t, o, "totp_verify", a); !g.permitido {
		t.Error("o outro balde da mesma chave pagou pelo login")
	}
}

// Duas réplicas com relógios diferentes. A atrasada gasta das fichas que
// existem (o tempo "negativo" não tira nada) e não puxa o carimbo da linha
// para trás, o que daria à certa um minuto de recarga que não houve.
func TestIPBudgetToleratesAReplicaWithAClockBehind(t *testing.T) {
	pool := testdb.New(t)
	certa := storage.NewOrcamentosPorIP(pool, clock.NewFake(agora))
	atrasada := storage.NewOrcamentosPorIP(pool, clock.NewFake(agora.Add(-time.Minute)))
	chave := chaveDeTeste("a")
	gastar(t, certa, baldeDeTeste, chave)

	if g := gastar(t, atrasada, baldeDeTeste, chave); !g.permitido {
		t.Fatal("a réplica atrasada não gastou das fichas que restavam")
	}
	for i := range rajadaDeTeste - 2 {
		if g := gastar(t, certa, baldeDeTeste, chave); !g.permitido {
			t.Fatalf("gasto %d do resto da rajada recusado", i+1)
		}
	}
	if g := gastar(t, certa, baldeDeTeste, chave); g.permitido {
		t.Error("a réplica atrasada rendeu fichas além da rajada")
	}
}

// N pedidos simultâneos da mesma origem gastam no máximo a rajada: a decisão é
// uma instrução só, sem ler e depois gravar.
func TestIPBudgetConcurrentSpendsNeverExceedTheBurst(t *testing.T) {
	o := storage.NewOrcamentosPorIP(testdb.New(t), clock.NewFake(agora))
	chave := chaveDeTeste("a")
	const pedidos = 20

	var permitidos atomic.Int32
	var wg sync.WaitGroup
	erros := make(chan error, pedidos)
	for range pedidos {
		wg.Go(func() {
			_, ok, err := o.Gastar(context.Background(), baldeDeTeste, chave, fichaACada, rajadaDeTeste)
			if err != nil {
				erros <- err
				return
			}
			if ok {
				permitidos.Add(1)
			}
		})
	}
	wg.Wait()
	close(erros)
	for err := range erros {
		t.Errorf("gasto simultâneo: %v", err)
	}
	if got := permitidos.Load(); got != rajadaDeTeste {
		t.Errorf("permitidos = %d, queria %d", got, rajadaDeTeste)
	}
}

// A linha parada além do ocioso equivale a um balde cheio: o próximo gasto do
// mesmo balde a apaga, sem job. A recente e a de outro balde ficam.
func TestIPBudgetSpendClearsIdleRowsOfTheSameBucket(t *testing.T) {
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	o := storage.NewOrcamentosPorIP(pool, clk)
	parada, outroBalde, recente := chaveDeTeste("parada"), chaveDeTeste("outro balde"), chaveDeTeste("recente")
	gastar(t, o, baldeDeTeste, parada)
	gastar(t, o, "totp_verify", outroBalde)

	clk.Advance(storage.OciosoMinimoPorIP + time.Second)
	gastar(t, o, baldeDeTeste, recente)
	gastar(t, o, baldeDeTeste, chaveDeTeste("quem limpa"))

	restantes := chavesGravadas(t, pool)
	if contem(restantes, parada) {
		t.Error("a linha parada do login sobreviveu ao gasto seguinte")
	}
	if !contem(restantes, outroBalde) {
		t.Error("o gasto do login apagou a linha de outro balde")
	}
	if !contem(restantes, recente) {
		t.Error("o gasto apagou uma linha recente")
	}
}

func chavesGravadas(t *testing.T, pool *pgxpool.Pool) [][]byte {
	t.Helper()
	var chaves [][]byte
	comEscopo(t, pool, "app.scope", "ip_rate_limit", func(tx pgx.Tx) {
		linhas, err := tx.Query(context.Background(), `SELECT key_hash FROM ip_rate_limits`)
		if err != nil {
			t.Fatal(err)
		}
		chaves, err = pgx.CollectRows(linhas, pgx.RowTo[[]byte])
		if err != nil {
			t.Fatal(err)
		}
	}, false)
	return chaves
}

func contem(chaves [][]byte, chave []byte) bool {
	for _, c := range chaves {
		if bytes.Equal(c, chave) {
			return true
		}
	}
	return false
}

// A tabela não tem dono: só o escopo do limite a lê e grava. Nenhum outro
// escopo (sem escopo, usuário, sondador) a enxerga ou escreve nela.
func TestIPRateLimitsAreOnlyReachableInTheirScope(t *testing.T) {
	pool := testdb.New(t)
	gastar(t, storage.NewOrcamentosPorIP(pool, clock.NewFake(agora)), baldeDeTeste, chaveDeTeste("a"))

	escopos := []struct{ nome, guc, valor string }{
		{"sem escopo", "app.scope", ""},
		{"usuário", "app.user_id", novoUsuario(t, pool).String()},
		{"sondador", "app.scope", "prober"},
	}
	for _, e := range escopos {
		t.Run(e.nome, func(t *testing.T) {
			comEscopo(t, pool, e.guc, e.valor, func(tx pgx.Tx) {
				ctx := context.Background()
				var vistas int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ip_rate_limits`).Scan(&vistas); err != nil {
					t.Fatal(err)
				}
				if vistas != 0 {
					t.Errorf("enxergou %d linhas", vistas)
				}
				if n := afetadasNaTx(t, tx, `UPDATE ip_rate_limits SET tokens = 5`); n != 0 {
					t.Errorf("UPDATE alterou %d linhas", n)
				}
				if n := afetadasNaTx(t, tx, `DELETE FROM ip_rate_limits`); n != 0 {
					t.Errorf("DELETE apagou %d linhas", n)
				}
				_, err := tx.Exec(ctx, `INSERT INTO ip_rate_limits (bucket, key_hash, tokens, last_allowed, updated_at)
				                        VALUES ('login', $1, 5, true, now())`, chaveDeTeste("b"))
				if err == nil {
					t.Error("INSERT fora do escopo passou")
				}
			}, false)
		})
	}
	if got := len(chavesGravadas(t, pool)); got != 1 {
		t.Errorf("linhas no escopo do limite = %d, queria 1", got)
	}
}
