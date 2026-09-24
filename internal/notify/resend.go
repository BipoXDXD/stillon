package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

const resendEndpoint = "https://api.resend.com/emails"

// Resend envia e-mail pela API do Resend (spec §3.3). A chave é do operador
// (RESEND_API_KEY), não do usuário: o canal de e-mail guarda só o endereço.
type Resend struct {
	client   *http.Client
	endpoint string
	apiKey   crypto.Secret
	from     string
}

// NewResend usa o cliente do safedial. O host é fixo e confiável; o
// cliente guardado não bloqueia nada legítimo e evita um segundo cliente
// sem guarda no processo.
func NewResend(apiKey crypto.Secret, from string) *Resend {
	return &Resend{client: safedial.NewClient(0), endpoint: resendEndpoint, apiKey: apiKey, from: from}
}

func (*Resend) Kind() Kind { return KindEmail }

// Send manda o alerta com Idempotency-Key = id da notificação. O Resend
// guarda a chave por 24h: uma retentativa depois de um timeout em que o
// e-mail JÁ saiu não gera um segundo e-mail.
func (r *Resend) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	assunto, corpo := mensagem(ev)
	return r.post(ctx, dest, assunto, corpo, "notification/"+ev.NotificationID.String())
}

// SendVerification manda o link de verificação (spec §5.8). Não leva
// Idempotency-Key: cada envio tem um token novo, então o corpo muda.
func (r *Resend) SendVerification(ctx context.Context, dest crypto.Secret, link string) error {
	corpo := "Alguém cadastrou este endereço para receber alertas do Vigilume.\n\n" +
		"Para confirmar, abra o link abaixo em até 24 horas:\n" + link + "\n\n" +
		"Se não foi você, ignore esta mensagem: sem a confirmação, nenhum alerta é enviado.\n"
	return r.post(ctx, dest, "[Vigilume] Confirme este endereço", corpo, "")
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (r *Resend) post(ctx context.Context, dest crypto.Secret, assunto, corpo, chave string) error {
	payload, err := json.Marshal(resendPayload{
		From: r.from, To: []string{string(dest.Reveal())}, Subject: assunto, Text: corpo,
	})
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+string(r.apiKey.Reveal()))
	if chave != "" {
		req.Header.Set("Idempotency-Key", chave)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return classificarErro(err)
	}
	defer drenar(resp)
	if resp.StatusCode == http.StatusConflict {
		return conflitoResend(resp)
	}
	return classificarResposta(resp)
}

// conflitoResend separa os dois 409 do Resend. concurrent_idempotent_requests
// é a mesma chave ainda em processamento, e vale repetir mais tarde;
// invalid_idempotent_request é a mesma chave com outro corpo, e repetir dá
// o mesmo 409.
func conflitoResend(resp *http.Response) error {
	var corpo struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&corpo)
	if corpo.Name == "concurrent_idempotent_requests" {
		return &SendError{Reason: ReasonRateLimited}
	}
	return &SendError{Reason: ReasonRejected, Permanent: true}
}
