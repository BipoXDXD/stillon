package account_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

type limiteDeEntrega struct {
	tentativas int
	suprimir   bool
}

func (l *limiteDeEntrega) admitir() bool {
	l.tentativas++
	return !l.suprimir || l.tentativas == 1
}

func (l *limiteDeEntrega) Gastar(context.Context, string, []byte, time.Duration, int) (time.Duration, bool, error) {
	return 0, l.admitir(), nil
}

func (l *limiteDeEntrega) Reservar(context.Context) (bool, error) {
	return l.admitir(), nil
}

type transporteDeLinks struct {
	tentativas   int
	recebidos    chan emailEnviado
	falhaSegundo error
}

func (tr *transporteDeLinks) SendAccount(_ context.Context, to crypto.Secret, subject, body string) error {
	tr.tentativas++
	tr.recebidos <- emailEnviado{to: string(to.Reveal()), subject: subject, body: body}
	if tr.tentativas == 2 {
		return tr.falhaSegundo
	}
	return nil
}

const destinoDeLink = "conta@exemplo.test"

// iniciarFluxoDeLink emite o primeiro link e devolve ações para o mesmo dono
// e propósito. O mailer do teste decide quando concluir cada envio.
func iniciarFluxoDeLink(t *testing.T, a ambienteCadastro, finalidade string, mailer account.AccountMailer) (pedir func() error, consumir func(string) error) {
	t.Helper()
	ctx := t.Context()
	if finalidade == "confirmacao" {
		cad := account.NewCadastro(a.pool, a.clk, mailer, linksDeTeste)
		cad.SetHash(hashArgon2Barato)
		if err := cad.Cadastrar(ctx, destinoDeLink, senha); err != nil {
			t.Fatal(err)
		}
		id := idDoEmail(t, destinoDeLink)
		return func() error { return cad.ReenviarConfirmacao(ctx, id) },
			func(token string) error { return cad.ConfirmarEmail(ctx, token, senha, nil) }
	}
	inserirComoNoSignup(t, a.pool, destinoDeLink)
	r := account.NewRecuperacao(a.pool, a.clk, mailer, linksDeTeste)
	r.SetHash(hashBarato)
	pedir = func() error { return r.PedirRedefinicao(ctx, destinoDeLink) }
	if err := pedir(); err != nil {
		t.Fatal(err)
	}
	return pedir, func(token string) error { return r.Redefinir(ctx, token, senhaRedefinida) }
}

func TestEntregaDeLinkDecideSubstituicao(t *testing.T) {
	casos := []struct {
		nome            string
		baldeVazio      bool
		tetoEsgotado    bool
		falhaSegundo    error
		querTentativas  int
		querGravados    int
		consumirSegundo bool
	}{
		{nome: "balde vazio", baldeVazio: true, querTentativas: 1, querGravados: 1},
		{nome: "teto esgotado", tetoEsgotado: true, querTentativas: 1, querGravados: 1},
		{nome: "provedor recusa", falhaSegundo: &notify.SendError{Reason: notify.ReasonRejected, Permanent: true}, querTentativas: 2, querGravados: 1},
		{nome: "sem resposta preserva o primeiro", falhaSegundo: context.DeadlineExceeded, querTentativas: 2, querGravados: 2},
		{nome: "sem resposta permite usar o segundo", falhaSegundo: context.DeadlineExceeded, querTentativas: 2, querGravados: 2, consumirSegundo: true},
		{nome: "provedor aceita", querTentativas: 2, querGravados: 1, consumirSegundo: true},
	}
	for _, finalidade := range []string{"confirmacao", "redefinicao"} {
		t.Run(finalidade, func(t *testing.T) {
			for _, caso := range casos {
				t.Run(caso.nome, func(t *testing.T) {
					a := novoCadastro(t)
					ctx := t.Context()
					tr := &transporteDeLinks{recebidos: make(chan emailEnviado, 2), falhaSegundo: caso.falhaSegundo}
					e := contaemail.New(tr, &limiteDeEntrega{suprimir: caso.tetoEsgotado},
						&limiteDeEntrega{suprimir: caso.baldeVazio}, make([]byte, crypto.KeySize),
						slog.New(slog.NewTextHandler(io.Discard, nil)))
					workerCtx, parar := context.WithCancel(ctx)
					fim := make(chan struct{})
					go func() {
						defer close(fim)
						e.Rodar(workerCtx)
					}()
					defer func() { parar(); <-fim }()

					pedir, consumir := iniciarFluxoDeLink(t, a, finalidade, e)

					var primeiro emailEnviado
					select {
					case primeiro = <-tr.recebidos:
					case <-time.After(5 * time.Second):
						t.Fatal("transporte não recebeu o primeiro link")
					}
					if err := pedir(); err != nil {
						t.Fatal(err)
					}
					parar()
					<-fim

					if n := tr.tentativas; n != caso.querTentativas {
						t.Fatalf("transporte recebeu %d links, queria %d", n, caso.querTentativas)
					}
					if n := totalDeTokens(t); n != caso.querGravados {
						t.Errorf("%d links gravados, queria %d", n, caso.querGravados)
					}
					valido := tokenDoEmail(t, primeiro)
					var outro string
					if tr.tentativas == 2 {
						outro = tokenDoEmail(t, <-tr.recebidos)
					}
					if caso.consumirSegundo {
						valido, outro = outro, valido
					}
					if outro != "" && caso.querGravados == 1 {
						if err := consumir(outro); !errors.Is(err, account.ErrTokenInvalido) {
							t.Fatalf("consumo do link descartado: %v, queria ErrTokenInvalido", err)
						}
					}
					if err := consumir(valido); err != nil {
						t.Fatalf("consumo do link válido: %v", err)
					}
					for _, token := range []string{valido, outro} {
						if token != "" {
							if err := consumir(token); !errors.Is(err, account.ErrTokenInvalido) {
								t.Fatalf("consumo após um uso válido: %v, queria ErrTokenInvalido", err)
							}
						}
					}
				})
			}
		})
	}
}

func TestConsumoConferePrazoDepoisDaTrava(t *testing.T) {
	for _, finalidade := range []string{"confirmacao", "redefinicao"} {
		t.Run(finalidade, func(t *testing.T) {
			a := novoCadastro(t)
			ctx := t.Context()
			const destino = "conta@exemplo.test"
			var token string
			var consumir func(string) error
			var validade time.Duration
			switch finalidade {
			case "confirmacao":
				token = cadastrarEPegarToken(t, a, destino)
				consumir = func(token string) error { return a.cad.ConfirmarEmail(ctx, token, senha, nil) }
				validade = 24 * time.Hour
			case "redefinicao":
				inserirComoNoSignup(t, a.pool, destino)
				r, m := novaRecuperacao(a.pool, a.clk)
				token = pedirToken(t, r, m, destino)
				consumir = func(token string) error { return r.Redefinir(ctx, token, senhaRedefinida) }
				validade = time.Hour
			}
			id := idDoEmail(t, destino)
			soltar := segurarTrava(t, a.pool, id)
			fim := make(chan error, 1)
			go func() { fim <- consumir(token) }()
			if esperou, err := esperarNaTravaDoUsuario(t, a.pool, id, fim); !esperou {
				t.Fatalf("consumo terminou antes de esperar a trava do usuário: %v", err)
			}
			a.clk.Advance(validade)
			soltar()
			if err := <-fim; !errors.Is(err, account.ErrTokenInvalido) {
				t.Fatalf("consumo depois do prazo: %v, queria ErrTokenInvalido", err)
			}
			if emailConfirmado(t, id) {
				t.Fatal("o link vencido confirmou o endereço")
			}
		})
	}
}

type entregaAdiada struct {
	email    emailEnviado
	concluir account.ConclusaoDoEnvio
}

type mailerAdiado chan entregaAdiada

func (m mailerAdiado) SendAccountEmail(ctx context.Context, to, subject, body string, entrega account.EntregaDeEmail) error {
	select {
	case m <- entregaAdiada{email: emailEnviado{to: to, subject: subject, body: body}, concluir: entrega.Concluir}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func concluirEntrega(t *testing.T, e entregaAdiada, resultado account.ResultadoDoEnvio) {
	t.Helper()
	if e.concluir == nil {
		t.Fatal("mensagem com link sem conclusão")
	}
	if err := e.concluir(t.Context(), resultado); err != nil {
		t.Fatalf("concluir entrega: %v", err)
	}
}

func TestConclusaoForaDeOrdemERepetida(t *testing.T) {
	for _, finalidade := range []string{"confirmacao", "redefinicao"} {
		t.Run(finalidade, func(t *testing.T) {
			a := novoCadastro(t)
			m := make(mailerAdiado, 3)
			pedir, consumir := iniciarFluxoDeLink(t, a, finalidade, m)
			primeira := <-m
			concluirEntrega(t, primeira, account.AceitoPeloTransporte)
			if err := pedir(); err != nil {
				t.Fatal(err)
			}
			segunda := <-m
			if err := pedir(); err != nil {
				t.Fatal(err)
			}
			terceira := <-m
			if n := totalDeTokens(t); n != 3 {
				t.Fatalf("esperava os três links antes de concluir, encontrei %d", n)
			}

			concluirEntrega(t, terceira, account.AceitoPeloTransporte)
			concluirEntrega(t, segunda, account.AceitoPeloTransporte)
			concluirEntrega(t, terceira, account.AceitoPeloTransporte)
			for _, e := range []entregaAdiada{primeira, segunda} {
				if err := consumir(tokenDoEmail(t, e.email)); !errors.Is(err, account.ErrTokenInvalido) {
					t.Fatalf("consumo de emissão anterior à que foi aceita: %v", err)
				}
			}
			if err := consumir(tokenDoEmail(t, terceira.email)); err != nil {
				t.Fatalf("consumo do último link aceito: %v", err)
			}
			concluirEntrega(t, terceira, account.AceitoPeloTransporte)
			if n := totalDeTokens(t); n != 0 {
				t.Errorf("conclusão repetida após uso deixou %d tokens, queria zero", n)
			}
		})
	}
}

func TestRedefinirRevogaTambemAConfirmacao(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	m := make(mailerAdiado, 3)
	reenviar, confirmar := iniciarFluxoDeLink(t, a, "confirmacao", m)
	confirmacao := <-m
	concluirEntrega(t, confirmacao, account.AceitoPeloTransporte)
	if err := reenviar(); err != nil {
		t.Fatal(err)
	}
	confirmacaoEmVoo := <-m
	id := idDoEmail(t, destinoDeLink)
	familia := abrirSessao(t, a.pool, a.clk, id)

	r := account.NewRecuperacao(a.pool, a.clk, m, linksDeTeste)
	r.SetHash(hashBarato)
	if err := r.PedirRedefinicao(ctx, destinoDeLink); err != nil {
		t.Fatal(err)
	}
	redefinicao := <-m
	concluirEntrega(t, redefinicao, account.AceitoPeloTransporte)
	if err := r.PedirRedefinicao(ctx, destinoDeLink); err != nil {
		t.Fatal(err)
	}
	redefinicaoEmVoo := <-m

	if err := r.Redefinir(ctx, tokenDoEmail(t, redefinicao.email), senhaRedefinida); err != nil {
		t.Fatalf("redefinição com link novo em voo: %v", err)
	}
	concluirEntrega(t, redefinicaoEmVoo, account.AceitoPeloTransporte)
	concluirEntrega(t, confirmacaoEmVoo, account.AceitoPeloTransporte)
	for _, e := range []entregaAdiada{confirmacao, confirmacaoEmVoo} {
		if err := confirmar(tokenDoEmail(t, e.email)); !errors.Is(err, account.ErrTokenInvalido) {
			t.Fatalf("confirmação anterior à redefinição ainda pôde ser consumida: %v", err)
		}
	}
	if err := r.Redefinir(ctx, tokenDoEmail(t, redefinicaoEmVoo.email), senha); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("redefinição em voo foi reativada: %v", err)
	}
	if n := totalDeTokens(t); n != 0 {
		t.Errorf("restaram %d tokens depois da redefinição, queria zero", n)
	}
	if familiaViva(t, familia) || !senhaEntra(t, a.pool, id, senhaRedefinida) || senhaEntra(t, a.pool, id, senha) {
		t.Error("perfil após redefinição deve aceitar só a senha nova e encerrar a sessão existente")
	}
}

func TestNovoPedidoDeRedefinicaoEsperaConsumo(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	m := make(mailerAdiado, 3)
	_, consumir := iniciarFluxoDeLink(t, a, "redefinicao", m)
	primeira := <-m
	concluirEntrega(t, primeira, account.AceitoPeloTransporte)
	id := idDoEmail(t, destinoDeLink)
	familia := abrirSessao(t, a.pool, a.clk, id)
	admin := testdb.Admin(t)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	// O consumo adquire a trava do usuário e para na gravação da senha;
	// uma nova emissão precisa esperar a revogação dos links anteriores.
	if _, err := tx.Exec(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", id); err != nil {
		t.Fatal(err)
	}
	token := tokenDoEmail(t, primeira.email)
	fimConsumo := make(chan error, 1)
	go func() { fimConsumo <- consumir(token) }()
	if !testdb.EsperarBloqueados(t, admin, 1, fimConsumo) {
		t.Fatal("o consumo terminou enquanto a escrita da conta estava bloqueada")
	}

	r := account.NewRecuperacao(a.pool, a.clk, m, linksDeTeste)
	r.SetHash(hashBarato)
	fimPedido := make(chan error, 1)
	go func() { fimPedido <- r.PedirRedefinicao(ctx, destinoDeLink) }()
	if esperou, err := esperarNaTravaDoUsuario(t, a.pool, id, fimPedido); !esperou {
		t.Fatalf("o pedido terminou antes da revogação dos links: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-fimConsumo; err != nil {
		t.Fatalf("consumo: %v", err)
	}
	if err := <-fimPedido; err != nil {
		t.Fatalf("pedido: %v", err)
	}
	if familiaViva(t, familia) || !senhaEntra(t, a.pool, id, senhaRedefinida) {
		t.Fatal("o primeiro consumo não trocou a senha e encerrou a sessão")
	}

	segundo := <-m
	concluirEntrega(t, segundo, account.AceitoPeloTransporte)
	if err := r.Redefinir(ctx, tokenDoEmail(t, segundo.email), senha); err != nil {
		t.Fatalf("pedido emitido depois da revogação: %v", err)
	}
	if !senhaEntra(t, a.pool, id, senha) {
		t.Error("o segundo consumo não trocou a senha")
	}
}

func TestAceiteDeLinkVencidoPreservaAnterior(t *testing.T) {
	a := novoCadastro(t)
	m := make(mailerAdiado, 2)
	pedir, consumir := iniciarFluxoDeLink(t, a, "redefinicao", m)
	primeira := <-m
	concluirEntrega(t, primeira, account.AceitoPeloTransporte)

	// Em outra réplica, a segunda emissão pode ter um relógio atrasado.
	a.clk.Advance(-time.Minute)
	if err := pedir(); err != nil {
		t.Fatal(err)
	}
	segunda := <-m
	a.clk.Advance(time.Hour)
	concluirEntrega(t, segunda, account.AceitoPeloTransporte)
	if err := consumir(tokenDoEmail(t, segunda.email)); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("consumo do link recém-vencido: %v, queria ErrTokenInvalido", err)
	}
	if err := consumir(tokenDoEmail(t, primeira.email)); err != nil {
		t.Fatalf("aceite atrasado substituiu o único link válido: %v", err)
	}
}

func TestNovaEmissaoPodaCandidatoVencido(t *testing.T) {
	a := novoCadastro(t)
	m := make(mailerAdiado, 2)
	pedir, _ := iniciarFluxoDeLink(t, a, "redefinicao", m)
	primeira := <-m
	concluirEntrega(t, primeira, account.EnvioIncerto)
	if n := totalDeTokens(t); n != 1 {
		t.Fatalf("tokens antes do vencimento: %d, queria um", n)
	}
	a.clk.Advance(time.Hour)
	if err := pedir(); err != nil {
		t.Fatal(err)
	}
	if n := totalDeTokens(t); n != 1 {
		t.Errorf("tokens após emitir com o anterior vencido: %d, queria um", n)
	}
}

func TestTrabalhadorIgnoraLinkRevogadoAntesDoTransporte(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	tr := &transporteDeLinks{recebidos: make(chan emailEnviado, 1)}
	balde := &limiteDeEntrega{}
	e := contaemail.New(tr, &limiteDeEntrega{}, balde, make([]byte, crypto.KeySize),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	iniciarFluxoDeLink(t, a, "confirmacao", e)

	// A redefinição revoga a confirmação enquanto ela ainda está enfileirada.
	r, m := novaRecuperacao(a.pool, a.clk)
	token := pedirToken(t, r, m, destinoDeLink)
	if err := r.Redefinir(ctx, token, senhaRedefinida); err != nil {
		t.Fatal(err)
	}
	workerCtx, parar := context.WithCancel(ctx)
	parar()
	e.Rodar(workerCtx)
	if balde.tentativas != 0 || tr.tentativas != 0 {
		t.Errorf("link revogado chegou ao balde %d vez(es) e ao transporte %d vez(es)", balde.tentativas, tr.tentativas)
	}
}
