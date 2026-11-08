package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Os testes deste arquivo fecham lacunas achadas por mutação: cada um falha
// com uma regra específica dos avisos desligada.

// O Retry-After com espaços em volta ainda é um número.
func TestRetryAfterIgnoresSurroundingSpaces(t *testing.T) {
	if got := retryAfter(" 3 "); got != 3*time.Second {
		t.Errorf("retryAfter = %v, queria 3s", got)
	}
}

// Só o fechamento sai em verde.
func TestDiscordResolvedHasTheResolvedColor(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := &Discord{client: client, validate: aceitarQualquer}
	ev := evAberto
	ev.Kind = incident.EventResolved
	ev.ResolvedAt = ev.StartedAt.Add(time.Hour)

	if err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/tok"), ev); err != nil {
		t.Fatal(err)
	}
	var corpo discordPayload
	if err := json.Unmarshal(receber(t, pedidos).Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(corpo.Content, "🟢 ") {
		t.Errorf("conteúdo = %q", corpo.Content)
	}
}

// O que não tem o tamanho de um token nosso é recusado sem ir ao banco.
func TestTokenHashRefusesTheWrongLength(t *testing.T) {
	bom := base64.RawURLEncoding.EncodeToString(make([]byte, tokenBytes))
	curto := base64.RawURLEncoding.EncodeToString(make([]byte, tokenBytes-1))
	if _, ok := hashDoToken(bom); !ok {
		t.Error("token do tamanho certo foi recusado")
	}
	if _, ok := hashDoToken(curto); ok {
		t.Error("token curto foi aceito")
	}
}

func TestHintEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		nome    string
		tipo    Kind
		destino string
		quer    string
	}{
		{"id de quatro dígitos sai inteiro", KindDiscord, "https://discord.com/api/webhooks/1234/segredo", "webhook …1234"},
		{"id de cinco dígitos perde o primeiro", KindDiscord, "https://discord.com/api/webhooks/12345/segredo", "webhook …2345"},
		{"barra no fim não vira parte do caminho", KindDiscord, "https://discord.com/api/webhooks/123456/segredo/", "webhook …3456"},
		{"destino sem barra", KindDiscord, "invalido", "webhook"},
		{"e-mail sem parte local", KindEmail, "@exemplo.com", "e-mail"},
		{"e-mail sem arroba", KindEmail, "sem-arroba", "e-mail"},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			if got := hint(tc.tipo, crypto.Secret(tc.destino)); got != tc.quer {
				t.Errorf("hint = %q, queria %q", got, tc.quer)
			}
		})
	}
}

func TestDurationBoundaries(t *testing.T) {
	for d, quer := range map[time.Duration]string{
		time.Minute - time.Second:  "menos de um minuto",
		time.Minute:                "1 min",
		24*time.Hour - time.Minute: "23h 59min",
		24 * time.Hour:             "1d 0h",
	} {
		if got := duracao(d); got != quer {
			t.Errorf("duracao(%v) = %q, queria %q", d, got, quer)
		}
	}
}

func TestResponseClassificationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		status     int
		motivo     Reason
		permanente bool
	}{
		{199, ReasonRejected, true},
		{200, "", false},
		{299, "", false},
		{300, ReasonRejected, true},
		{429, ReasonRateLimited, false},
		{499, ReasonRejected, true},
		{500, ReasonServerError, false},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			err := classificarResposta(&http.Response{StatusCode: tc.status, Header: http.Header{}})
			if tc.motivo == "" {
				if err != nil {
					t.Fatalf("erro = %v, queria entregue", err)
				}
				return
			}
			var se *SendError
			if !errors.As(err, &se) || se.Reason != tc.motivo || se.Permanent != tc.permanente {
				t.Errorf("erro = %v, queria %s (permanente: %v)", err, tc.motivo, tc.permanente)
			}
		})
	}
}

func TestErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		nome       string
		err        error
		motivo     Reason
		permanente bool
	}{
		{"destino bloqueado", fmt.Errorf("x: %w", safedial.ErrBlocked), ReasonInvalidChannel, true},
		{"URL inválida", fmt.Errorf("x: %w", safedial.ErrInvalidURL), ReasonInvalidChannel, true},
		{"redirecionamento recusado", fmt.Errorf("x: %w", safedial.ErrRedirect), ReasonInvalidChannel, true},
		{"prazo", fmt.Errorf("x: %w", context.DeadlineExceeded), ReasonTimeout, false},
		{"rede", errors.New("reset"), ReasonNetwork, false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			var se *SendError
			if err := classificarErro(tc.err); !errors.As(err, &se) || se.Reason != tc.motivo || se.Permanent != tc.permanente {
				t.Errorf("erro = %v, queria %s (permanente: %v)", err, tc.motivo, tc.permanente)
			}
		})
	}
}

func TestFailingAfterSuccess(t *testing.T) {
	cedo := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tarde := cedo.Add(time.Minute)
	for _, tc := range []struct {
		nome           string
		falha, sucesso *time.Time
		quer           bool
	}{
		{"sem falha", nil, &cedo, false},
		{"falha sem nenhum sucesso", &cedo, nil, true},
		{"falha depois do sucesso", &tarde, &cedo, true},
		{"sucesso depois da falha", &cedo, &tarde, false},
		{"mesmo instante: o sucesso vale", &cedo, &cedo, false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			if got := falhandoDepoisDoSucesso(tc.falha, tc.sucesso); got != tc.quer {
				t.Errorf("falhandoDepoisDoSucesso = %v, queria %v", got, tc.quer)
			}
		})
	}
}

// Quem parou o canal falhou de vez, sem próxima tentativa: a adiada calcula o
// próprio backoff, em vez de voltar para a fila no instante zero.
func TestDeferredBehindADefinitiveFailureUsesItsOwnBackoff(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 2)
	b.comoSondador(t, `UPDATE notifications SET attempts = $1 WHERE id = $2`, int32(MaxAttempts-1), ids[0])
	b.canal.respostas = []error{&SendError{Reason: ReasonNetwork}}

	b.despachar(t, 2)

	if l := b.estado(t, ids[0]); l.Status != "failed" {
		t.Fatalf("quem parou o canal: %+v; queria failed", l)
	}
	l := b.estado(t, ids[1])
	if l.Status != "pending" || l.Attempts != 1 || l.Erro == nil || *l.Erro != "network" ||
		!l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) {
		t.Errorf("adiada: %+v; queria pendente, 1 tentativa, network, próxima em 30s", l)
	}
}

// Canal Discord sem verificação não é "e-mail pendente": o reenvio recusa e
// não manda link nenhum para a URL do webhook.
func TestResendRefusesAnUnverifiedDiscordChannel(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(b.svc.box, id, webhookValido)
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InUserTx(ctx, b.svc.pool, b.dono, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: id, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, CreatedAt: b.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.svc.ResendVerification(ctx, b.dono, id); !errors.Is(err, ErrNotPending) {
		t.Errorf("erro = %v, queria ErrNotPending", err)
	}
	if len(b.email.enderecos) != 0 {
		t.Errorf("mandou link para %d destinos, queria nenhum", len(b.email.enderecos))
	}
}
