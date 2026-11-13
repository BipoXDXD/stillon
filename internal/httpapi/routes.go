package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/webui"
)

// maxBodyBytes cobre com folga qualquer payload legítimo desta API.
const maxBodyBytes = 1 << 20 // 1 MiB

// As rotas que mandam mensagem para fora (criar canal, reenviar
// verificação) ganham um balde próprio por usuário: um envio a cada 6
// minutos, 5 de uma vez. Dá 10 por hora depois do burst (spec §5.8).
const (
	envioACada = 6 * time.Minute
	envioBurst = 5
)

// verifyBodyBytes: o POST de /verify é um formulário sem campos.
const verifyBodyBytes = 1 << 10

// Deps reúne o que as rotas consomem. Virou struct quando o sétimo
// parâmetro chegou: a lista posicional já não se lia.
type Deps struct {
	Store    *auth.PGStore
	Sessions *auth.Manager
	TOTP     *auth.TOTP
	// Accounts é a conta do dono: GET /me e POST /auth/password.
	Accounts *account.Service
	// Proofs é o contador de provas erradas por família de sessão, no banco:
	// uma instância para a troca de senha e as rotas do 2FA. Nil só onde
	// nenhuma dessas rotas é chamada, como Accounts.
	Proofs    *auth.FamilyProofs
	Targets   *targets.Service
	Incidents *history.Service
	Channels  *notify.ChannelService
	// StatusPages serve o cadastro do dono e a página pública /p/{slug}.
	StatusPages *statuspage.Service
	// Pings recebe os sinais das tarefas em /ping/{token}.
	Pings *heartbeat.PingService
	// Heartbeats é o cadastro do dono.
	Heartbeats *heartbeat.Service
	Clock      clock.Clock
	// Ready é o teste de prontidão de /readyz: o ping no banco em produção.
	Ready func(context.Context) error
	// Painel são os arquivos do painel React. Nil é o embutido no binário
	// (webui.Dist); os testes injetam um fstest.MapFS.
	Painel fs.FS
}

// Routes monta a árvore de rotas e a cadeia de middlewares.
//
// ctx é o contexto de vida do servidor: ele encerra as goroutines de
// varredura dos rate limiters no shutdown, em vez de deixá-las vazar.
func Routes(ctx context.Context, cfg *config.Config, d Deps) http.Handler {
	// O prazo vem do config.Load (15 s). Zero ou negativo deixaria toda
	// requisição vencida na largada: um Config montado sem ele é defeito do
	// código.
	if cfg.RequestTimeout <= 0 {
		panic("httpapi: Config sem RequestTimeout positivo; monte o Config pelo config.Load ou defina RequestTimeout")
	}
	// Sem https não há como marcar o cookie como Secure: o navegador
	// simplesmente não o guardaria, e o desenvolvimento local pararia. Em
	// produção PUBLIC_BASE_URL é https, e o refresh nunca trafega em claro.
	secureCookie := config.EhHTTPS(cfg.PublicBaseURL)
	// O teto de Argon2id simultâneos é do processo, e quem o
	// paga é a API: o config.Load sempre o preenche; um Config montado à mão
	// (testes) sem ele fica com o padrão.
	if cfg.Argon2MaxConcurrent > 0 {
		auth.SetArgon2MaxConcurrent(cfg.Argon2MaxConcurrent)
	} else {
		auth.SetArgon2MaxConcurrent(auth.DefaultArgon2MaxConcurrent())
	}
	// Um contador de provas por família para a troca de senha e as rotas do
	// 2FA: o orçamento é da família, não da rota.
	provas := &provasDaFamilia{contador: d.Proofs, contas: d.Accounts, proxies: proxiesDe(cfg)}
	// O cookie de dispositivo: a chave do MAC vem da MASTER_KEY por HKDF, com
	// rótulo próprio.
	dispositivos := auth.NewDevices(cfg.MasterKey, d.Clock)
	h := NewAuthHandlers(d.Store, d.Sessions, d.TOTP, d.Clock, cfg.JWTSecret, secureCookie, proxiesDe(cfg), provas, dispositivos)
	th := NewTargetHandlers(d.Targets, d.Clock)
	ih := NewIncidentHandlers(d.Incidents)
	hb := NewHeartbeatHandlers(d.Heartbeats, cfg.PublicBaseURL)
	ch := NewChannelHandlers(d.Channels)
	sp := NewStatusPageHandlers(d.StatusPages, cfg.PublicBaseURL)
	ah := NewAccountHandlers(d.Accounts, proxiesDe(cfg), secureCookie, provas)
	authn := middleware.Authn(cfg.JWTSecret, d.Clock)

	mux := http.NewServeMux()

	// Rotas de autenticação: limite mais apertado, porque é onde a força
	// bruta bate.
	//
	// O login tem um limitador por IP e o bloqueio progressivo de
	// auth.LockThreshold, que operam em camadas diferentes e de propósito: o
	// limite é por IP, o bloqueio é por conta. Depois de esgotado o burst ainda
	// passam 10 tentativas por minuto, então cinco falhas na mesma conta chegam
	// em poucos segundos — o bloqueio não fica inalcançável.
	//
	// O limitador NÃO protege o dono contra o bloqueio da própria conta: o burst
	// (5) é igual ao limiar (5), e um único IP tranca a conta com cinco senhas
	// erradas a cada 15 min. Quem deixa o dono entrar mesmo assim é o cookie de
	// dispositivo: o navegador que já completou um login tem um contador próprio,
	// e o bloqueio disparado por outras origens não o alcança.
	//
	// O balde por IP é só do login (e, mais abaixo, do /totp/verify, que tem o
	// seu). A troca de senha e as cinco rotas autenticadas do 2FA têm um balde por
	// CONTA, de que só quem tem token válido gasta ficha: com o IP, quem divide
	// NAT ou um terceiro sem credencial esvaziaria o balde do dono.
	//
	// O estado dos baldes é só da memória desta instância: com mais de uma réplica,
	// cada uma conta por si. É a mesma limitação do login, e não é resolvida aqui.
	limiteLogin := middleware.RateLimit(ctx, d.Clock, 10, 5, proxiesDe(cfg))
	mux.Handle("POST /api/v1/auth/login", limiteLogin(http.HandlerFunc(h.Login)))
	// Uma instância só, e não uma por rota: a troca de senha e as cinco rotas do
	// 2FA dividem o balde. Com instâncias separadas, quem adivinha senha ou código
	// dobraria o orçamento alternando as rotas. A chave é o usuário que authn põe
	// no contexto, e não a sessão: outra família da mesma conta, de qualquer IP,
	// cai no mesmo balde, e logar de novo não rende ficha. A cadeia é authn, viva,
	// limite, handler: sem token válido, 401 e nenhuma ficha gasta; e um token de
	// sessão revogada (ainda no prazo de 15 min) é recusado pelo viva SEM gastar o
	// balde de que o dono precisa para trocar a senha ou o 2FA. O preço é uma
	// consulta de sessão por pedido de quem já tem token assinado, sob o piso
	// global por IP. Mesma taxa do login: 10/min, rajada 5.
	limiteConta := middleware.RateLimitUser(ctx, d.Clock, 10, 5)
	viva := ah.exigirSessaoViva
	autenticadaComLimite := func(fn http.HandlerFunc) http.Handler { return authn(viva(limiteConta(fn))) }
	mux.Handle("POST /api/v1/auth/password", autenticadaComLimite(ah.ChangePassword))
	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(h.Logout))

	// Todas exigem a família do token viva: um token de sessão encerrada não mexe
	// no 2FA.
	mux.Handle("POST /api/v1/auth/totp/enroll", autenticadaComLimite(h.TOTPEnroll))
	mux.Handle("POST /api/v1/auth/totp/confirm", autenticadaComLimite(h.TOTPConfirm))
	// Desativar é a saída que a recusa do recadastro tirou. Exige senha E posse do
	// fator, no handler — a sessão sozinha aqui seria o bypass de volta.
	mux.Handle("POST /api/v1/auth/totp/disable", autenticadaComLimite(h.TOTPDisable))
	// A troca de autenticador: a mesma cadeia. Duas camadas independentes limitam
	// as provas erradas destas rotas, e das três acima e da troca de senha: o
	// limite por conta (limiteConta, depois de authn e da sessão viva) e o contador
	// por família de sessão (provasDaFamilia), em que a quinta prova errada derruba
	// a família. O 429 sai antes do handler e não conta prova. O contador de tentativas do login (da conta ou, com cookie de
	// dispositivo válido, do dispositivo) é só do login e do /totp/verify.
	mux.Handle("POST /api/v1/auth/totp/rotate", autenticadaComLimite(h.TOTPRotate))
	mux.Handle("POST /api/v1/auth/totp/rotate/confirm", autenticadaComLimite(h.TOTPRotateConfirm))
	// Verify não passa por authn: o usuário ainda não tem sessão, só o
	// desafio. O limitador por IP vem de fora; o contador de tentativas vale aqui,
	// no handler: o código errado entra no mesmo contador do login (h.tentativas:
	// o da conta ou o do dispositivo), porque só o limite por IP não segura quem
	// chuta os seis dígitos de várias origens.
	mux.Handle("POST /api/v1/auth/totp/verify",
		middleware.RateLimit(ctx, d.Clock, 20, 5, proxiesDe(cfg))(http.HandlerFunc(h.TOTPVerify)))

	// Rotas de recurso: Authn primeiro, depois o limite por USUÁRIO (spec
	// §5.7), que precisa do usuário que Authn põe no contexto. Uma instância
	// só, compartilhada pelas rotas: o orçamento é do usuário, não da rota.
	//
	// 60/min com burst 20 é mais apertado que o por IP (120/min, burst 30)
	// de propósito: um usuário sozinho esgota o seu antes de esgotar o do
	// IP, e é isso que torna a camada observável (TestPerUserRateLimit).
	porUsuario := middleware.RateLimitUser(ctx, d.Clock, 60, 20)
	// Revogar bloqueia novas requisições, mesmo com JWT ainda válido. Uma
	// mutação que já passou pela guarda pode concluir; não há cancelamento
	// retroativo em voo. O limite continua antes da consulta de sessão.
	protegida := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(viva(fn))) }

	mux.Handle("GET /api/v1/me", protegida(ah.Me))
	mux.Handle("GET /api/v1/sessions", protegida(ah.ListSessions))
	mux.Handle("DELETE /api/v1/sessions/{id}", protegida(ah.RevokeSession))

	mux.Handle("GET /api/v1/targets", protegida(th.List))
	mux.Handle("POST /api/v1/targets", protegida(th.Create))
	// O literal é mais específico que {id} e ganha no ServeMux, sem conflito.
	mux.Handle("GET /api/v1/targets/summary", protegida(th.Summary))
	mux.Handle("GET /api/v1/targets/{id}", protegida(th.Get))
	mux.Handle("PATCH /api/v1/targets/{id}", protegida(th.Update))
	mux.Handle("DELETE /api/v1/targets/{id}", protegida(th.Delete))
	mux.Handle("GET /api/v1/targets/{id}/checks", protegida(th.Checks))

	mux.Handle("GET /api/v1/incidents", protegida(ih.List))
	mux.Handle("POST /api/v1/incidents/{id}/ack", protegida(ih.Ack))

	mux.Handle("GET /api/v1/heartbeats", protegida(hb.List))
	mux.Handle("POST /api/v1/heartbeats", protegida(hb.Create))
	mux.Handle("GET /api/v1/heartbeats/cron-preview", protegida(hb.CronPreview))
	mux.Handle("GET /api/v1/heartbeats/{id}", protegida(hb.Get))
	mux.Handle("PATCH /api/v1/heartbeats/{id}", protegida(hb.Update))
	mux.Handle("DELETE /api/v1/heartbeats/{id}", protegida(hb.Delete))
	mux.Handle("POST /api/v1/heartbeats/{id}/token/rotate", protegida(hb.RotateToken))
	mux.Handle("POST /api/v1/heartbeats/{id}/clone", protegida(hb.Clone))
	mux.Handle("POST /api/v1/heartbeats/{id}/ping-now", protegida(hb.PingNow))
	mux.Handle("GET /api/v1/heartbeats/{id}/events", protegida(hb.Events))
	mux.Handle("POST /api/v1/heartbeats/{id}/events/clear", protegida(hb.ClearEvents))
	mux.Handle("GET /api/v1/heartbeats/{id}/incidents", protegida(hb.Incidents))
	mux.Handle("POST /api/v1/heartbeats/{id}/incidents/{incident_id}/ack", protegida(hb.AckIncident))

	porEnvio := middleware.RateLimitUserEvery(ctx, d.Clock, envioACada, envioBurst)
	comEnvio := func(fn http.HandlerFunc) http.Handler { return authn(porUsuario(porEnvio(viva(fn)))) }
	mux.Handle("GET /api/v1/channels", protegida(ch.List))
	mux.Handle("POST /api/v1/channels", comEnvio(ch.Create))
	mux.Handle("DELETE /api/v1/channels/{id}", protegida(ch.Delete))
	// Pausar e retomar não manda mensagem para fora: fica no
	// limitador comum, e não no balde de envio.
	mux.Handle("PATCH /api/v1/channels/{id}", protegida(ch.SetEnabled))
	mux.Handle("POST /api/v1/channels/{id}/verify/resend", comEnvio(ch.Resend))

	mux.Handle("GET /api/v1/status-pages", protegida(sp.List))
	mux.Handle("POST /api/v1/status-pages", protegida(sp.Create))
	mux.Handle("PATCH /api/v1/status-pages/{id}", protegida(sp.Update))

	// Spec §13: /healthz é raso (o processo responde); /readyz verifica o
	// banco.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", readyz(d.Ready))

	// A ordem importa, e se escreve de dentro para fora: a ÚLTIMA linha é o
	// middleware mais externo.
	//
	// RequestID tem de ser o mais externo de todos. apierr.WriteError lê o
	// trace do contexto, e o contexto só o tem depois que RequestID rodou —
	// um erro produzido por qualquer middleware acima dele sairia com
	// trace_id vazio, impossível de correlacionar com o log. Deadline fica
	// antes de Recover para que até um erro recuperado observe o prazo;
	// Recover captura panic de todo o restante da cadeia.
	var handler http.Handler = comEnvelope(mux)
	handler = middleware.RequireCustomHeader(handler)
	handler = middleware.BodyLimit(maxBodyBytes)(handler)
	// Preflight fica DENTRO do rate limit e FORA do RequireCustomHeader: o
	// OPTIONS passa a ser contado pelo limitador sem precisar do
	// cabeçalho customizado, que nenhum preflight carrega. Os cabeçalhos de
	// CORS continuam por fora do limitador, em middleware.CORS, para que o
	// 429 também os leve.
	handler = middleware.Preflight()(handler)
	handler = middleware.RateLimit(ctx, d.Clock, 120, 30, proxiesDe(cfg))(handler)
	handler = middleware.CORS(cfg.CORSOrigin)(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(handler)
	handler = middleware.Deadline(cfg.RequestTimeout)(handler)
	handler = middleware.RequestID(handler)

	// A raiz separa as cadeias pelo prefixo, e o ServeMux escolhe sempre o
	// padrão mais específico: o painel, montado em "/", nunca vê /api/, /p/
	// nem /verify/. Antes do painel, "/" era a API; agora a API tem de ser
	// nomeada, inclusive os dois health checks, que ficam fora de /api/.
	raiz := http.NewServeMux()
	raiz.Handle("/api/", handler)
	raiz.Handle("/healthz", handler)
	raiz.Handle("/readyz", handler)
	raiz.Handle("/verify/", verificacao(ctx, cfg, d.Clock, d.Channels))
	raiz.Handle("/p/", paginaPublica(ctx, cfg, d.Clock, (&PublicPageHandlers{svc: d.StatusPages}).Page))
	raiz.Handle("/ping/", pingPublico(ctx, cfg, d.Clock, d.Pings))
	raiz.Handle("/", painel(d.Painel))
	return raiz
}

// painel é a cadeia do painel React. Só serve GET e HEAD de arquivos que já
// estão em memória: sem CORS, sem RequireCustomHeader, sem BodyLimit e sem
// rate limit — dividir o balde da API com o carregamento do painel geraria
// 429 falso. SecurityHeaders põe HSTS, nosniff e companhia; o handler troca a
// CSP e o Cache-Control pelos dele, como a página pública.
func painel(arquivos fs.FS) http.Handler {
	var h http.Handler
	if arquivos == nil {
		h = painelEmbutido()
	} else {
		h = webui.New(arquivos)
	}
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.RequestID(h)
	return h
}

// painelEmbutido lê e comprime o dist embutido uma vez por processo. O New
// lê tudo e comprime no boot; refazer isso a cada Routes()
// custava ~70 s na suíte de integração com -race e um dist real, que monta
// Routes() em quase todo teste.
var painelEmbutido = sync.OnceValue(func() *webui.Painel { return webui.New(webui.Dist()) })

// proxiesDe junta as duas variáveis que decidem de onde vem o IP do cliente.
// Todo limitador por IP e o ip_hash da auditoria passam por aqui: um lugar
// só para errar ou acertar. Com PUBLIC_BASE_URL https, um X-Forwarded-For de
// fora de TRUSTED_PROXIES vira aviso no log.
func proxiesDe(cfg *config.Config) middleware.Proxies {
	return middleware.Proxies{
		Trusted: cfg.TrustedProxies, Header: cfg.ClientIPHeader,
		AvisarXFF: config.EhHTTPS(cfg.PublicBaseURL),
	}
}

// readyzTimeout limita o ping: um banco que não responde tem de virar 503
// rápido, e não segurar o health check do orquestrador.
const readyzTimeout = 2 * time.Second

// readyz responde 200 só com o banco alcançável. Sem ping no Deps também é
// 503: um wiring esquecido não pode parecer pronto.
func readyz(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
		defer cancel()
		if ping == nil {
			apierr.WriteError(w, r, apierr.CodeUnavailable)
			return
		}
		if err := ping(ctx); err != nil {
			slog.Warn("readyz: banco indisponível", "erro", err)
			apierr.WriteError(w, r, apierr.CodeUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// verificacao é a cadeia das páginas de /verify/{token}, separada da API.
//
// Ela é aberta ao navegador, e não ao SPA: o POST vem de um <form> HTML,
// que não manda X-Requested-With. Por isso fica fora do RequireCustomHeader.
// Sem cookie e sem sessão, não há o que um CSRF roubaria: a credencial é o
// token no caminho, que o atacante não tem. Também fica fora do CORS, que é
// da API. O limite por IP é mais apertado que o da API.
func verificacao(ctx context.Context, cfg *config.Config, clk clock.Clock, svc *notify.ChannelService) http.Handler {
	vh := NewVerifyHandlers(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /verify/{token}", vh.Page)
	mux.HandleFunc("POST /verify/{token}", vh.Confirm)

	var h http.Handler = mux
	h = middleware.BodyLimit(verifyBodyBytes)(h)
	h = middleware.RateLimit(ctx, clk, 20, 10, proxiesDe(cfg))(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.Deadline(cfg.RequestTimeout)(h)
	h = middleware.RequestID(h)
	return h
}
