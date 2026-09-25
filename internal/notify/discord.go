package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

var ErrDiscordURL = errors.New("URL de webhook do Discord inválida")

// discordHosts são os hosts de webhook do Discord. A lista é fechada: o
// servidor faz um POST para a URL que o usuário deu, e aceitar qualquer
// host transformaria o canal num proxy de POST arbitrário.
var discordHosts = map[string]bool{
	"discord.com":        true,
	"discordapp.com":     true,
	"canary.discord.com": true,
	"ptb.discord.com":    true,
}

// /api/webhooks/{id}/{token}, com versão opcional (/api/v10/webhooks/...).
var discordPath = regexp.MustCompile(`^/api(/v[0-9]{1,2})?/webhooks/[0-9]{1,20}/[A-Za-z0-9_-]{1,100}$`)

const maxDiscordURL = 512

// ValidateDiscordURL aceita só URLs de webhook do Discord: https, host da
// lista, sem porta, sem credencial, sem query nem fragmento. A query fica
// de fora porque o envio acrescenta a sua (?wait=true).
func ValidateDiscordURL(raw string) error {
	if len(raw) > maxDiscordURL || strings.ContainsAny(raw, "?#") {
		return ErrDiscordURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
		return ErrDiscordURL
	}
	if !discordHosts[strings.ToLower(u.Host)] || !discordPath.MatchString(u.EscapedPath()) {
		return ErrDiscordURL
	}
	return nil
}

// suppressEmbeds (1 << 2) impede o Discord de buscar prévia de um link que
// apareça no nome do alvo.
const suppressEmbeds = 1 << 2

type discordPayload struct {
	Content         string          `json:"content"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
	Flags           int             `json:"flags"`
}

// Parse vazio desliga TODA menção: um alvo chamado "@everyone" não pinga o
// servidor inteiro. O padrão do Discord para webhook, sem este campo,
// resolve menções de usuário.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

type Discord struct {
	client *http.Client
	// validate é ValidateDiscordURL em produção. Os testes do pacote trocam
	// por uma que aceita o httptest.
	validate func(string) error
}

// NewDiscord usa o cliente do safedial: a URL veio do usuário.
func NewDiscord() *Discord {
	return &Discord{client: safedial.NewClient(0), validate: ValidateDiscordURL}
}

func (*Discord) Kind() Kind { return KindDiscord }

func (d *Discord) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	_, corpo := mensagem(ev)
	emoji := "🔴 "
	if ev.Kind == incident.EventResolved {
		emoji = "🟢 "
	}
	return d.post(ctx, dest, emoji+corpo)
}

// SendTest é a verificação do canal no cadastro: só um 2xx cria o canal.
func (d *Discord) SendTest(ctx context.Context, dest crypto.Secret) error {
	return d.post(ctx, dest, "✅ StillOn conectado. Os alertas de incidente vão chegar neste canal.")
}

func (d *Discord) post(ctx context.Context, dest crypto.Secret, conteudo string) error {
	alvo := string(dest.Reveal())
	// Revalida a cada envio: o que está no banco pode ter entrado antes de
	// uma regra ficar mais rígida, ou por fora da API.
	if err := d.validate(alvo); err != nil {
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	}
	corpo, err := json.Marshal(discordPayload{
		Content:         conteudo,
		AllowedMentions: allowedMentions{Parse: []string{}},
		Flags:           suppressEmbeds,
	})
	if err != nil {
		return &SendError{Reason: ReasonUnknown, Permanent: true}
	}
	// wait=true faz o Discord confirmar a gravação da mensagem antes de
	// responder; sem ele, uma mensagem descartada não devolve erro.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, alvo+"?wait=true", bytes.NewReader(corpo))
	if err != nil {
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return classificarErro(err)
	}
	defer drenar(resp)
	return classificarResposta(resp)
}
