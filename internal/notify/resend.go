package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
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
	corpo := "Alguém cadastrou este endereço para receber alertas do StillOn.\n\n" +
		"Para confirmar, abra o link abaixo em até 24 horas:\n" + link + "\n\n" +
		"Se não foi você, ignore esta mensagem: sem a confirmação, nenhum alerta é enviado.\n"
	return r.post(ctx, dest, "[StillOn] Confirme este endereço", corpo, "")
}

// SendTOTPLocked avisa o dono que o segundo fator foi bloqueado por códigos
// errados. O texto não traz código, IP nem horário exato: só o que
// o dono precisa para agir. chave é a Idempotency-Key do bloqueio.
// O booleano indica um endereço que falhou na validação local.
func (r *Resend) SendTOTPLocked(ctx context.Context, email string, permanente bool, chave string) (destinoInvalido bool, err error) {
	destino, ok := NormalizeEmail(email)
	if !ok {
		return true, errors.New("destino do aviso inválido")
	}
	corpo := "O segundo fator da sua conta no StillOn foi bloqueado depois de vários códigos errados seguidos.\n\n" +
		"O código só é pedido depois da senha certa. Se não foi você, troque a senha agora.\n"
	if permanente {
		corpo += "\nO bloqueio não vence sozinho: só o comando stillon user reset-2fa, rodado no servidor, o libera.\n"
	} else {
		corpo += "\nO bloqueio vence sozinho, e cada novo bloqueio dura o dobro do anterior.\n"
	}
	return false, r.post(ctx, crypto.Secret(destino), "[StillOn] Segundo fator bloqueado", corpo, chave)
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

	resp, err := semSeguirRedirect(r.client).Do(req)
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
// o mesmo 409. Um 409 que não se deixa ler (corpo que não decodifica ou
// nome desconhecido) é transitório: repetir com a mesma chave é seguro, e
// se ele era o de outro corpo volta igual até o teto de tentativas.
func conflitoResend(resp *http.Response) error {
	var corpo struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&corpo); err != nil {
		return &SendError{Reason: ReasonServerError}
	}
	switch corpo.Name {
	case "concurrent_idempotent_requests":
		return &SendError{Reason: ReasonRateLimited}
	case "invalid_idempotent_request":
		return &SendError{Reason: ReasonRejected, Permanent: true}
	default:
		return &SendError{Reason: ReasonServerError}
	}
}
