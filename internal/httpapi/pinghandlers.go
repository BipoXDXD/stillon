package httpapi

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

const (
	// pingBodyBytes: o corpo é descartado, e o teto só limita o quanto
	// a drenagem lê.
	pingBodyBytes = 10 << 10
	// pingTimeout fica abaixo do prazo da API: um ping é uma transação curta,
	// e quem o manda é uma tarefa com seu próprio timeout.
	pingTimeout = 5 * time.Second
	// O balde por IP é folgado de propósito: uma máquina com 100 tarefas no
	// mesmo minuto, ou várias atrás de um NAT, manda início e fim de cada uma
	// (200 sinais de uma vez), e o 429 ali viraria alarme falso.
	pingPorIPMinuto, pingPorIPRajada = 1200, 300
	// O balde por token é o que segura um token vazado em laço.
	pingPorTokenMinuto, pingPorTokenRajada = 30, 10
)

// pingOK é fixo: a resposta não diz nada sobre o heartbeat.
var pingOK = struct {
	Status string `json:"status"`
}{Status: "ok"}

// PingHandlers responde às tarefas. Nenhum log deste arquivo leva o caminho
// nem o token: o caminho é a credencial.
type PingHandlers struct {
	svc *heartbeat.PingService
}

func NewPingHandlers(svc *heartbeat.PingService) *PingHandlers {
	return &PingHandlers{svc: svc}
}

func (h *PingHandlers) sinal(sig heartbeat.Signal) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.responder(w, r, func(rid *uuid.UUID) error {
			return h.svc.Ping(r.Context(), r.PathValue("token"), sig, rid)
		})
	}
}

// codigoDeSaida atende /ping/{token}/{exit_code}. Número inválido é igual a
// token desconhecido: a rota não diz a quem a sonda se a URL existe.
func (h *PingHandlers) codigoDeSaida(w http.ResponseWriter, r *http.Request) {
	h.responder(w, r, func(rid *uuid.UUID) error {
		code, ok := heartbeat.ParseExitCode(r.PathValue("exit_code"))
		if !ok {
			return heartbeat.ErrUnknownToken
		}
		return h.svc.PingExit(r.Context(), r.PathValue("token"), code, rid)
	})
}

// responder lê o rid antes de tudo: o 400 sai igual para qualquer token, sem
// ler o corpo e sem ir ao banco, e por isso não diz se a URL existe.
func (h *PingHandlers) responder(w http.ResponseWriter, r *http.Request, registrar func(rid *uuid.UUID) error) {
	rid, ok := lerRunID(r.URL.RawQuery)
	if !ok {
		campoInvalido(w, r, "rid", heartbeat.ReasonInvalid)
		return
	}
	// Drena antes do banco: corpo acima do teto não tem efeito nenhum.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		if corpoAcimaDoTeto(err) {
			apierr.WriteError(w, r, apierr.CodePayloadTooLarge)
			return
		}
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}
	switch err := registrar(rid); {
	case err == nil:
		apierr.WriteJSON(w, http.StatusOK, pingOK)
	case errors.Is(err, heartbeat.ErrUnknownToken):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	default:
		erroInterno(w, r, "registrar ping", err)
	}
}

// pingPublico é a cadeia de /ping/, separada da API. Quem chama é uma tarefa
// agendada, com curl: fica fora do CORS e do RequireCustomHeader. Sem cookie e
// sem sessão, não há o que um CSRF roubaria. Só POST: uma prévia de link
// ou um scanner que fizesse GET contaria como sinal e esconderia uma tarefa
// parada.
func pingPublico(ctx context.Context, cfg *config.Config, clk clock.Clock, svc *heartbeat.PingService) http.Handler {
	ph := NewPingHandlers(svc)
	porToken := middleware.RateLimitBy(ctx, clk, pingPorTokenMinuto, pingPorTokenRajada, chaveDoToken)
	rota := func(sig heartbeat.Signal) http.Handler { return porToken(ph.sinal(sig)) }

	mux := http.NewServeMux()
	mux.Handle("POST /ping/{token}", rota(heartbeat.SignalSuccess))
	mux.Handle("POST /ping/{token}/start", rota(heartbeat.SignalStart))
	mux.Handle("POST /ping/{token}/fail", rota(heartbeat.SignalFail))
	mux.Handle("POST /ping/{token}/log", rota(heartbeat.SignalLog))
	// Os sufixos literais de cima vencem este curinga, no ServeMux.
	mux.Handle("POST /ping/{token}/{exit_code}", porToken(http.HandlerFunc(ph.codigoDeSaida)))

	h := comEnvelope(mux)
	h = middleware.BodyLimit(pingBodyBytes)(h)
	h = middleware.RateLimit(ctx, clk, pingPorIPMinuto, pingPorIPRajada, proxiesDe(cfg))(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.Deadline(pingTimeout)(h)
	h = middleware.RequestID(h)
	return h
}

// chaveDoToken é o balde por tarefa, chaveado pelo hash: o limitador guarda
// suas chaves em memória, e o token não fica nela.
func chaveDoToken(r *http.Request) string {
	return hex.EncodeToString(crypto.HashToken(r.PathValue("token")))
}

// tamanhoDoRunID é o da forma canônica 8-4-4-4-12, a única aceita.
const tamanhoDoRunID = 36

// lerRunID lê o ?rid= da query crua. Devolve nil sem rid, e false quando o rid
// é inválido: vazio, repetido, fora da forma canônica ou com escape. Varre os
// pares à mão porque url.ParseQuery descarta o par com escape inválido, e um
// rid assim passaria como ausente. Os demais parâmetros seguem ignorados.
func lerRunID(rawQuery string) (*uuid.UUID, bool) {
	var achado *uuid.UUID
	for par := range strings.SplitSeq(rawQuery, "&") {
		chave, valor, _ := strings.Cut(par, "=")
		if nome, err := url.QueryUnescape(chave); err != nil || nome != "rid" {
			continue
		}
		if achado != nil {
			return nil, false
		}
		id, ok := runIDCanonico(valor)
		if !ok {
			return nil, false
		}
		achado = &id
	}
	return achado, true
}

// runIDCanonico aceita só os 36 caracteres do uuid, sem escape, chaves nem
// prefixo; a caixa é livre. O uuid nulo fica de fora: um script que o gere por
// engano juntaria todas as execuções num rid só.
func runIDCanonico(valor string) (uuid.UUID, bool) {
	if len(valor) != tamanhoDoRunID {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(valor)
	if err != nil || id == uuid.Nil || !strings.EqualFold(valor, id.String()) {
		return uuid.Nil, false
	}
	return id, true
}
