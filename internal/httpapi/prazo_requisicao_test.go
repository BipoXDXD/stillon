package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// rotasSemBanco monta Routes sem Postgres: o /readyz só chama o ping
// injetado, e o ping vê o contexto que a cadeia de middlewares lhe deu.
func rotasSemBanco(t *testing.T, cfg *config.Config, ping func(context.Context) error) http.Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	return httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:    notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(nil, clk),
		Clock:       clk,
		Ready:       ping,
	})
}

func configSemBanco(prazo time.Duration) *config.Config {
	return &config.Config{
		CORSOrigin:     "https://app.exemplo.test",
		JWTSecret:      []byte("segredo-jwt-sintetico-so-para-teste"),
		IPHashKey:      []byte("chave-de-ip-sintetica-so-para-teste"),
		MasterKey:      []byte("chave-mestra-de-teste-32-bytes!!"),
		RequestTimeout: prazo,
	}
}

// O /readyz limita o ping a 2 s, mas um prazo de requisição menor vence:
// com 50 ms na configuração, o ping não pode ganhar mais que isso. Com o
// prazo fixo no código, ele ganharia os 2 s do readyz.
func TestRoutesAplicaOPrazoDaConfiguracao(t *testing.T) {
	var restante time.Duration
	rotas := rotasSemBanco(t, configSemBanco(50*time.Millisecond), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("ping sem prazo")
			return nil
		}
		restante = time.Until(deadline)
		return nil
	})
	w := httptest.NewRecorder()
	rotas.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || restante <= 0 || restante > 50*time.Millisecond {
		t.Fatalf("status=%d restante=%v", w.Code, restante)
	}
}

// Um banco que não responde segura o ping até o contexto acabar. Com o prazo
// de requisição folgado (10 s), quem tem de cortar é o teto próprio do
// /readyz: o orquestrador recebe 503 unavailable em cerca de 2 s, e o handler
// não fica pendurado até o prazo da requisição.
func TestReadyzComPingTravadoRespondeNaoProntoNoTeto(t *testing.T) {
	const tetoDoReadyz = 2 * time.Second
	const folga = 2 * time.Second
	var restante time.Duration
	rotas := rotasSemBanco(t, configSemBanco(10*time.Second), func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); ok {
			restante = time.Until(deadline)
		}
		<-ctx.Done()
		return ctx.Err()
	})

	w := httptest.NewRecorder()
	respondeu := make(chan struct{})
	go func() {
		defer close(respondeu)
		rotas.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	}()
	select {
	case <-respondeu:
	case <-time.After(tetoDoReadyz + folga):
		t.Fatalf("/readyz com ping travado não respondeu em %v: o teto do ping sumiu", tetoDoReadyz+folga)
	}

	if restante <= 0 || restante > tetoDoReadyz {
		t.Errorf("ping recebeu %v de prazo, queria no máximo %v", restante, tetoDoReadyz)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("corpo não é o envelope de erro (%v): %s", err, w.Body)
	}
	if w.Code != http.StatusServiceUnavailable || env.Error.Code != "unavailable" {
		t.Errorf("/readyz com ping travado deu %d/%q, queria 503/unavailable", w.Code, env.Error.Code)
	}
}

// Um Config montado sem prazo (ou com prazo negativo) deixaria toda
// requisição vencida na largada: é defeito do código, e Routes recusa na
// montagem, dizendo como corrigir, em vez de servir 503.
func TestRoutesRecusaConfigSemPrazo(t *testing.T) {
	for _, prazo := range []time.Duration{0, -time.Nanosecond} {
		t.Run(prazo.String(), func(t *testing.T) {
			defer func() {
				motivo, _ := recover().(string)
				if !strings.Contains(motivo, "RequestTimeout") || !strings.Contains(motivo, "config.Load") {
					t.Fatalf("Routes com prazo %v: panic %q, queria um que mande montar o Config pelo config.Load", prazo, motivo)
				}
			}()
			rotasSemBanco(t, configSemBanco(prazo), nil)
		})
	}
}

// bancoMudo aceita a conexão TCP e nunca responde: o pgx fica esperando a
// resposta do startup até o contexto vencer. É o banco preso, sem Docker.
func bancoMudo(t *testing.T) *pgxpool.Pool {
	t.Helper()
	escuta, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conexoes := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = escuta.Close()
		for {
			select {
			case c := <-conexoes:
				_ = c.Close()
			default:
				return
			}
		}
	})
	go func() {
		for {
			conexao, err := escuta.Accept()
			if err != nil {
				return
			}
			select {
			case conexoes <- conexao:
			default:
				_ = conexao.Close()
			}
		}
	}()
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf(
		"postgres://stillon:x@%s/stillon?sslmode=disable", escuta.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A cadeia de /verify/ tem o próprio Deadline, fora da cadeia da API. Com o
// banco preso, ela só responde quando o prazo vence: com 100 ms na
// configuração, a resposta chega bem antes dos 15 s de produção. A de /p/
// tem teste próprio (publicpage_prazo_test.go): a consulta dela não vê o
// prazo da requisição.
func TestVerifyAplicaOPrazoDaConfiguracao(t *testing.T) {
	const prazo = 100 * time.Millisecond
	cfg := configSemBanco(prazo)
	pool := bancoMudo(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	rotas := httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:    notify.NewChannelService(pool, box, clk, &discordFalso{}, &carteiroFalso{}, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(nil, clk),
		Clock:       clk,
	})
	for _, rota := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/verify/" + strings.Repeat("A", 43)},
		{http.MethodPost, "/verify/" + strings.Repeat("A", 43)},
	} {
		t.Run(rota.metodo+" "+rota.caminho, func(t *testing.T) {
			w := httptest.NewRecorder()
			inicio := time.Now()
			rotas.ServeHTTP(w, httptest.NewRequest(rota.metodo, rota.caminho, nil))
			gasto := time.Since(inicio)
			if w.Code != http.StatusServiceUnavailable || gasto > 3*time.Second {
				t.Fatalf("status=%d em %v, queria 503 logo depois de %v", w.Code, gasto, prazo)
			}
		})
	}
}
