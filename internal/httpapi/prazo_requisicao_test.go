package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/turnstile"
)

// repoFalso é o repositório de projetos dos testes sem banco. Nenhum deles
// chega a uma rota de recurso; se chegar, a falha aparece como 500.
// convitesFalsos é o repositório de convites dos testes sem banco: todo
// projeto é inexistente.
type convitesFalsos struct{}

func (convitesFalsos) Create(context.Context, uuid.UUID, uuid.UUID, tenancy.NewInvitation) (tenancy.Invitation, tenancy.InvitationEmail, error) {
	return tenancy.Invitation{}, tenancy.InvitationEmail{}, tenancy.ErrProjectNotFound
}

func (convitesFalsos) ListPending(context.Context, uuid.UUID, uuid.UUID, time.Time) ([]tenancy.Invitation, error) {
	return nil, tenancy.ErrProjectNotFound
}

func (convitesFalsos) Revoke(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return tenancy.ErrProjectNotFound
}

func (convitesFalsos) Accept(context.Context, uuid.UUID, []byte, time.Time) (tenancy.ProjectSummary, error) {
	return tenancy.ProjectSummary{}, tenancy.ErrInvitationInvalid
}

// convitesSemBanco monta Deps.Invitations sem banco nem e-mail.
func convitesSemBanco(clk clock.Clock) *tenancy.Invitations {
	return tenancy.NewInvitations(convitesFalsos{}, nil, clk)
}

type repoFalso struct{}

var errRepoFalso = errors.New("repoFalso: teste sem banco")

func (repoFalso) Membership(context.Context, uuid.UUID, uuid.UUID) (tenancy.ProjectScope, error) {
	return tenancy.ProjectScope{}, errRepoFalso
}

func (repoFalso) EnsureDefault(context.Context, uuid.UUID, time.Time) (tenancy.ProjectScope, error) {
	return tenancy.ProjectScope{}, errRepoFalso
}

func (repoFalso) ListForUser(context.Context, uuid.UUID) ([]tenancy.ProjectSummary, error) {
	return nil, errRepoFalso
}

func (repoFalso) Create(context.Context, tenancy.NewProject) (tenancy.ProjectSummary, error) {
	return tenancy.ProjectSummary{}, errRepoFalso
}

func (repoFalso) ListMembers(context.Context, uuid.UUID, uuid.UUID) ([]tenancy.MemberView, error) {
	return nil, errRepoFalso
}

func (repoFalso) ChangeRole(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, tenancy.Role) (tenancy.MemberView, error) {
	return tenancy.MemberView{}, errRepoFalso
}

func (repoFalso) RemoveMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return errRepoFalso
}

func (repoFalso) ProposeTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return errRepoFalso
}

func (repoFalso) AcceptTransfer(context.Context, uuid.UUID, uuid.UUID, time.Time) (tenancy.ProjectSummary, error) {
	return tenancy.ProjectSummary{}, errRepoFalso
}

func (repoFalso) CancelTransfer(context.Context, uuid.UUID, uuid.UUID) error {
	return errRepoFalso
}

func (repoFalso) Rename(context.Context, uuid.UUID, uuid.UUID, string) (tenancy.ProjectSummary, error) {
	return tenancy.ProjectSummary{}, errRepoFalso
}

// conferidorQueConfirma é a trava de e-mail dos testes sem banco: toda conta
// conta como confirmada, para que a trava não esconda o que o teste olha.
type conferidorQueConfirma struct{}

func (conferidorQueConfirma) EmailConfirmado(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

// contaPublicaFalsa é o cadastro e a recuperação dos testes sem banco: todo
// token é inválido, e o resto falha como o repositório falso.
type contaPublicaFalsa struct{}

func (contaPublicaFalsa) Cadastrar(context.Context, string, string) error { return errRepoFalso }
func (contaPublicaFalsa) ConfirmarEmail(context.Context, string, string, []byte) error {
	return account.ErrTokenInvalido
}
func (contaPublicaFalsa) ReenviarConfirmacao(context.Context, uuid.UUID) error {
	return errRepoFalso
}
func (contaPublicaFalsa) PedirRedefinicao(context.Context, string) error { return errRepoFalso }
func (contaPublicaFalsa) Redefinir(context.Context, string, string) error {
	return account.ErrTokenInvalido
}

// turnstileQueRecusa é o Turnstile dos testes sem banco: recusa todo desafio.
type turnstileQueRecusa struct{}

func (turnstileQueRecusa) Verificar(context.Context, string, string, string) error {
	return turnstile.ErrRecusado
}

// rotasSemBanco monta Routes sem Postgres: o /readyz só chama o ping
// injetado, e o ping vê o contexto que a cadeia de middlewares lhe deu.
func rotasSemBanco(t *testing.T, cfg *config.Config, ping func(context.Context) error) http.Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	return httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:        notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages:     statuspage.NewService(nil, clk),
		Projects:        tenancy.NewService(repoFalso{}, clk),
		Invitations:     convitesSemBanco(clk),
		EmailConfirmado: conferidorQueConfirma{},
		Cadastro:        contaPublicaFalsa{}, Recuperacao: contaPublicaFalsa{}, Turnstile: turnstileQueRecusa{},
		Clock:     clk,
		Ready:     ping,
		LimitesIP: middleware.NovosOrcamentosEmMemoria(ctx, clk),
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

// Sem resolvedor de projeto, nenhuma rota de recurso teria escopo: a montagem
// recusa os Deps em vez de subir uma API que falharia a cada requisição.
func TestRoutesRecusaDepsSemProjects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	cfg := configSemBanco(time.Second)
	defer func() {
		motivo, _ := recover().(string)
		if !strings.Contains(motivo, "Projects") || !strings.Contains(motivo, "tenancy.NewService") {
			t.Fatalf("Routes sem Projects: panic %q, queria um que mande passar o tenancy.NewService", motivo)
		}
	}()
	httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:        notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages:     statuspage.NewService(nil, clk),
		Projects:        nil,
		EmailConfirmado: conferidorQueConfirma{},
		Cadastro:        contaPublicaFalsa{}, Recuperacao: contaPublicaFalsa{}, Turnstile: turnstileQueRecusa{},
		Clock:     clk,
		LimitesIP: middleware.NovosOrcamentosEmMemoria(ctx, clk),
	})
}

// Sem conferidor de e-mail, a trava de criação não teria como recusar a conta
// não confirmada: a montagem recusa os Deps, em vez de deixar todo mundo criar.
func TestRoutesRecusaDepsSemEmailConfirmado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	cfg := configSemBanco(time.Second)
	defer func() {
		motivo, _ := recover().(string)
		if !strings.Contains(motivo, "EmailConfirmado") || !strings.Contains(motivo, "account.Service") {
			t.Fatalf("Routes sem EmailConfirmado: panic %q, queria um que mande passar o account.Service", motivo)
		}
	}()
	httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:    notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(nil, clk),
		Projects:    tenancy.NewService(repoFalso{}, clk),
		Invitations: convitesSemBanco(clk),
		Clock:       clk,
		LimitesIP:   middleware.NovosOrcamentosEmMemoria(ctx, clk),
	})
}

// Sem o cadastro, a recuperação ou o Turnstile, as rotas públicas da conta
// cairiam em panic no primeiro pedido: a montagem recusa os Deps antes, e o
// panic diz o que passar.
func TestRoutesRecusaDepsSemCadastroPublico(t *testing.T) {
	casos := []struct {
		campo, dica string
		tirar       func(*httpapi.Deps)
	}{
		{"Invitations", "tenancy.NewInvitations", func(d *httpapi.Deps) { d.Invitations = nil }},
		{"Cadastro", "account.Cadastro", func(d *httpapi.Deps) { d.Cadastro = nil }},
		{"Recuperacao", "account.Recuperacao", func(d *httpapi.Deps) { d.Recuperacao = nil }},
		{"Turnstile", "turnstile.Verificador", func(d *httpapi.Deps) { d.Turnstile = nil }},
	}
	for _, c := range casos {
		t.Run(c.campo, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
			cfg := configSemBanco(time.Second)
			deps := httpapi.Deps{
				Channels:        notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
				StatusPages:     statuspage.NewService(nil, clk),
				Projects:        tenancy.NewService(repoFalso{}, clk),
				Invitations:     convitesSemBanco(clk),
				EmailConfirmado: conferidorQueConfirma{},
				Cadastro:        contaPublicaFalsa{}, Recuperacao: contaPublicaFalsa{}, Turnstile: turnstileQueRecusa{},
				Clock:     clk,
				LimitesIP: middleware.NovosOrcamentosEmMemoria(ctx, clk),
			}
			c.tirar(&deps)
			defer func() {
				motivo, _ := recover().(string)
				if !strings.Contains(motivo, c.campo) || !strings.Contains(motivo, c.dica) {
					t.Fatalf("Routes sem %s: panic %q, queria um que mande passar o %s", c.campo, motivo, c.dica)
				}
			}()
			httpapi.Routes(ctx, cfg, deps)
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
		Channels:        notify.NewChannelService(pool, box, clk, &discordFalso{}, &carteiroFalso{}, cfg.PublicBaseURL),
		StatusPages:     statuspage.NewService(nil, clk),
		Projects:        tenancy.NewService(repoFalso{}, clk),
		Invitations:     convitesSemBanco(clk),
		EmailConfirmado: conferidorQueConfirma{},
		Cadastro:        contaPublicaFalsa{}, Recuperacao: contaPublicaFalsa{}, Turnstile: turnstileQueRecusa{},
		Clock:     clk,
		LimitesIP: middleware.NovosOrcamentosEmMemoria(ctx, clk),
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
