package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/targets"
)

const traceDoTeste = "trace-do-teste"

// capturarRegistros troca o slog.Default por um JSON em memória e devolve a
// função que lê os registros emitidos até o momento. O padrão volta no
// t.Cleanup. Não serve para teste paralelo, porque o slog.Default é global.
func capturarRegistros(t *testing.T) func() []map[string]any {
	t.Helper()
	var saida bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&saida, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	return func() []map[string]any {
		t.Helper()
		var registros []map[string]any
		for _, linha := range strings.Split(strings.TrimSpace(saida.String()), "\n") {
			if linha == "" {
				continue
			}
			var registro map[string]any
			if err := json.Unmarshal([]byte(linha), &registro); err != nil {
				t.Fatalf("registro de log ilegível %q: %v", linha, err)
			}
			registros = append(registros, registro)
		}
		return registros
	}
}

func requisicaoComTrace(ctx context.Context) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/x", nil).
		WithContext(observability.WithTraceID(ctx, traceDoTeste))
}

// conferirErroInterno fixa o contrato do erro interno: 500 com o envelope
// genérico (sem o detalhe), e um único registro ERROR com o erro real e o
// trace_id do contexto, só com as chaves de sempre.
func conferirErroInterno(t *testing.T, w *httptest.ResponseRecorder, registros []map[string]any, op, detalhe string) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quero 500; corpo %s", w.Code, w.Body)
	}
	var corpo struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo ilegível %q: %v", w.Body, err)
	}
	if corpo.Error.Code != string(apierr.CodeInternal) || corpo.Error.Message != "Erro interno." || corpo.TraceID != traceDoTeste {
		t.Errorf("envelope = %+v, quero internal_error, \"Erro interno.\" e trace_id %q", corpo, traceDoTeste)
	}
	if detalhe != "" && strings.Contains(w.Body.String(), detalhe) {
		t.Errorf("o corpo vazou o erro real: %s", w.Body)
	}
	if len(registros) != 1 {
		t.Fatalf("registros = %d (%v), quero exatamente 1", len(registros), registros)
	}
	registro := registros[0]
	if registro["level"] != "ERROR" || registro["msg"] != op || registro["trace_id"] != traceDoTeste {
		t.Errorf("registro = %v, quero ERROR, msg %q e trace_id %q", registro, op, traceDoTeste)
	}
	if erro, _ := registro["erro"].(string); erro == "" || (detalhe != "" && erro != detalhe) {
		t.Errorf("erro do registro = %q, quero o erro real %q", erro, detalhe)
	}
	for chave := range registro {
		switch chave {
		case "time", "level", "msg", "erro", "trace_id":
		default:
			t.Errorf("chave inesperada no registro: %q", chave)
		}
	}
}

func TestErroInternoDosNoveCaminhos(t *testing.T) {
	casos := []struct {
		nome, op string
		chamar   func(w http.ResponseWriter, r *http.Request, err error)
	}{
		{"alvos", "buscar alvo", func(w http.ResponseWriter, r *http.Request, err error) {
			(&TargetHandlers{}).falhou(w, r, "buscar alvo", err)
		}},
		{"incidentes", "reconhecer incidente", func(w http.ResponseWriter, r *http.Request, err error) {
			(&IncidentHandlers{}).falhou(w, r, "reconhecer incidente", err)
		}},
		{"heartbeats", "ler heartbeat", func(w http.ResponseWriter, r *http.Request, err error) {
			(&HeartbeatHandlers{}).falhou(w, r, "ler heartbeat", err)
		}},
		{"canais", "apagar canal", func(w http.ResponseWriter, r *http.Request, err error) {
			(&ChannelHandlers{}).falhou(w, r, "apagar canal", err)
		}},
		{"status pages", "atualizar status page", func(w http.ResponseWriter, r *http.Request, err error) {
			(&StatusPageHandlers{}).falhou(w, r, "atualizar status page", err)
		}},
		{"auth", "buscar usuário", func(w http.ResponseWriter, r *http.Request, err error) {
			(&AuthHandlers{}).internal(w, r, "buscar usuário", err)
		}},
		{"conta", "ler conta", func(w http.ResponseWriter, r *http.Request, err error) {
			(&AccountHandlers{}).internal(w, r, "ler conta", err)
		}},
		{"ping", "registrar ping", func(w http.ResponseWriter, r *http.Request, err error) {
			(&PingHandlers{}).responder(w, r, func(*uuid.UUID) error { return err })
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			registros := capturarRegistros(t)
			erro := errors.New("SQLSTATE 57P01 CANARY-" + uuid.NewString())
			w := httptest.NewRecorder()
			c.chamar(w, requisicaoComTrace(context.Background()), erro)
			conferirErroInterno(t, w, registros(), c.op, erro.Error())
		})
	}
}

func TestErroInternoDeProvasDerrubarFalhaDa500(t *testing.T) {
	registros := capturarRegistros(t)
	clk := clock.NewFake(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	pool := poolDePortaFechada(t)
	p := &provasDaFamilia{contas: account.NewService(pool, auth.NewTOTP(pool, nil, "StillOn"), clk, []byte("chave-de-teste-com-32-bytes-ok!!"))}

	w := httptest.NewRecorder()
	p.derrubar(w, requisicaoComTrace(context.Background()), uuid.New(), uuid.New())

	conferirErroInterno(t, w, registros(), "derrubar família por provas erradas", "")
	for _, vazou := range []string{"127.0.0.1", "connection refused"} {
		if strings.Contains(w.Body.String(), vazou) {
			t.Errorf("o corpo vazou %q: %s", vazou, w.Body)
		}
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Errorf("Set-Cookie = %q, quero nenhum", w.Header().Get("Set-Cookie"))
	}
}

func TestErroInternoComPrazoVencidoVira503(t *testing.T) {
	registros := capturarRegistros(t)
	ctx, cancelar := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelar()

	w := httptest.NewRecorder()
	(&TargetHandlers{}).falhou(w, requisicaoComTrace(ctx), "buscar alvo", errors.New("prazo estourou no banco"))

	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), string(apierr.CodeUnavailable)) {
		t.Errorf("resposta = %d %s, quero 503 unavailable", w.Code, w.Body)
	}
	if rs := registros(); len(rs) != 1 || rs[0]["level"] != "ERROR" {
		t.Errorf("registros = %v, quero o ERROR do erro real", rs)
	}
}

func TestInternalDeAuthEContaPreservaOArgon2Ocupado(t *testing.T) {
	casos := map[string]func(w http.ResponseWriter, r *http.Request, err error){
		"auth": func(w http.ResponseWriter, r *http.Request, err error) {
			(&AuthHandlers{}).internal(w, r, "entrar", err)
		},
		"conta": func(w http.ResponseWriter, r *http.Request, err error) {
			(&AccountHandlers{}).internal(w, r, "trocar senha", err)
		},
	}
	for nome, chamar := range casos {
		t.Run(nome, func(t *testing.T) {
			registros := capturarRegistros(t)
			w := httptest.NewRecorder()
			chamar(w, requisicaoComTrace(context.Background()), fmt.Errorf("x: %w", auth.ErrArgon2Busy))

			if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), string(apierr.CodeRateLimited)) {
				t.Errorf("resposta = %d %s, quero 429 rate_limited", w.Code, w.Body)
			}
			if w.Header().Get("Retry-After") == "" {
				t.Error("falta Retry-After")
			}
			rs := registros()
			if len(rs) != 1 || rs[0]["level"] != "WARN" || rs[0]["msg"] != "teto de Argon2id simultâneos atingido" {
				t.Errorf("registros = %v, quero só o WARN do teto", rs)
			}
		})
	}
}

func TestFalhouDeAlvosMantemOMapeamentoDeDominio(t *testing.T) {
	casos := []struct {
		erro   error
		status int
		code   apierr.ErrorCode
	}{
		{targets.ErrNotFound, http.StatusNotFound, apierr.CodeNotFound},
		{targets.ErrContentHostLimit, http.StatusConflict, apierr.CodeContentHostLimit},
	}
	for _, c := range casos {
		t.Run(string(c.code), func(t *testing.T) {
			registros := capturarRegistros(t)
			w := httptest.NewRecorder()
			(&TargetHandlers{}).falhou(w, requisicaoComTrace(context.Background()), "buscar alvo", fmt.Errorf("x: %w", c.erro))

			if w.Code != c.status || !strings.Contains(w.Body.String(), string(c.code)) {
				t.Errorf("resposta = %d %s, quero %d %s", w.Code, w.Body, c.status, c.code)
			}
			if rs := registros(); len(rs) != 0 {
				t.Errorf("registros = %v, quero nenhum (erro de domínio não é ERROR)", rs)
			}
		})
	}
}

func TestErroInternoDireto(t *testing.T) {
	registros := capturarRegistros(t)
	erro := errors.New("SQLSTATE 57P01 CANARY-" + uuid.NewString())

	w := httptest.NewRecorder()
	erroInterno(w, requisicaoComTrace(context.Background()), "op de teste", erro)

	conferirErroInterno(t, w, registros(), "op de teste", erro.Error())
}
