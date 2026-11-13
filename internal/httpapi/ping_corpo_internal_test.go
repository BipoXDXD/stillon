package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/google/uuid"
)

// respostaDoPing passa o corpo pelo responder com o mesmo teto da cadeia
// /ping/ e conta quantas vezes o registro do ping foi chamado. A costura é o
// registrar: o teste não precisa de banco para saber se o ping contou.
func respostaDoPing(t *testing.T, corpo io.Reader) (*httptest.ResponseRecorder, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ping/token-do-teste", corpo)
	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, pingBodyBytes)
	registros := 0
	registrar := func(*uuid.UUID) error {
		registros++
		return nil
	}

	(&PingHandlers{}).responder(rec, req, registrar)

	return rec, registros
}

type envelopeDeErro struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func lerEnvelopeDeErro(t *testing.T, rec *httptest.ResponseRecorder) envelopeDeErro {
	t.Helper()
	var env envelopeDeErro
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("corpo da resposta não é o envelope de erro: %v (%q)", err, rec.Body.String())
	}
	return env
}

// Corpo que falha no meio da leitura (cliente que aborta, chunked truncado,
// conexão resetada) não é corpo grande: 400 de validação, nenhum ping
// registrado e nada do erro de leitura no corpo da resposta.
func TestPingBodyReadFailureIs400AndRecordsNothing(t *testing.T) {
	casos := []struct {
		nome string
		erro error
	}{
		{"EOF inesperado", io.ErrUnexpectedEOF},
		{"conexão resetada", syscall.ECONNRESET},
		{"erro opaco", errors.New("detalhe-interno-da-leitura")},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec, registros := respostaDoPing(t, iotest.ErrReader(c.erro))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, queria 400", rec.Code)
			}
			env := lerEnvelopeDeErro(t, rec)
			if env.Error.Code != "validation_failed" || env.Error.Message != "Dados inválidos." {
				t.Errorf("envelope = %+v, queria validation_failed / Dados inválidos.", env.Error)
			}
			if registros != 0 {
				t.Errorf("ping registrado %d vezes com corpo ilegível", registros)
			}
			if corpo := rec.Body.String(); strings.Contains(corpo, c.erro.Error()) {
				t.Errorf("resposta vaza o erro de leitura: %s", corpo)
			}
		})
	}
}

// Leitura que falha depois de parte do corpo já lida segue a mesma regra.
func TestPingBodyCutMidwayIs400AndRecordsNothing(t *testing.T) {
	cortado := io.MultiReader(strings.NewReader(strings.Repeat("x", 100)), iotest.ErrReader(io.ErrUnexpectedEOF))

	rec, registros := respostaDoPing(t, cortado)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, queria 400", rec.Code)
	}
	if registros != 0 {
		t.Errorf("ping registrado %d vezes com corpo cortado", registros)
	}
}

// O teto é de 10 KiB (10240 bytes): no teto o ping conta, um byte acima não,
// e o acima é 413, não 400. O integração TestPingRejectsOversizedBody prova o
// mesmo pela cadeia inteira, com banco.
func TestPingBodyCeilingOnAndOffPoint(t *testing.T) {
	casos := []struct {
		nome          string
		tamanho       int
		status        int
		code          string
		registrosQuer int
	}{
		{"no teto", 10240, http.StatusOK, "", 1},
		{"um byte acima", 10241, http.StatusRequestEntityTooLarge, "payload_too_large", 0},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			// MultiReader esconde o tamanho: é a drenagem que descobre o excesso.
			corpo := io.MultiReader(strings.NewReader(strings.Repeat("x", c.tamanho)))

			rec, registros := respostaDoPing(t, corpo)

			if rec.Code != c.status {
				t.Errorf("status = %d, queria %d (%s)", rec.Code, c.status, rec.Body.String())
			}
			if registros != c.registrosQuer {
				t.Errorf("registros = %d, queria %d", registros, c.registrosQuer)
			}
			if c.code != "" {
				if env := lerEnvelopeDeErro(t, rec); env.Error.Code != c.code {
					t.Errorf("code = %q, queria %q", env.Error.Code, c.code)
				}
			}
		})
	}
}
