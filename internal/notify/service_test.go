package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

type testadorFalso struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (f *testadorFalso) SendTest(_ context.Context, dest crypto.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, string(dest.Reveal()))
	return f.err
}

func (f *testadorFalso) quantos() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

type carteiroFalso struct {
	mu        sync.Mutex
	enderecos []string
	links     []string
	err       error
}

func (f *carteiroFalso) SendVerification(_ context.Context, dest crypto.Secret, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enderecos = append(f.enderecos, string(dest.Reveal()))
	f.links = append(f.links, link)
	return f.err
}

// ultimoToken tira o token do último link enviado.
func (f *carteiroFalso) ultimoToken(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.links) == 0 {
		t.Fatal("nenhum link enviado")
	}
	link := f.links[len(f.links)-1]
	token, ok := strings.CutPrefix(link, "https://vigilume.dev/verify/")
	if !ok {
		t.Fatalf("link fora do formato: %s", link)
	}
	return token
}

type bancadaCanais struct {
	svc     *ChannelService
	clk     *clock.Fake
	discord *testadorFalso
	email   *carteiroFalso
	dono    uuid.UUID
	outro   uuid.UUID
}

const webhookValido = "https://discord.com/api/webhooks/123456789/token-do-webhook"

func novaBancadaCanais(t *testing.T, comEmail bool) *bancadaCanais {
	t.Helper()
	pool := testdb.New(t)
	box, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	b := &bancadaCanais{
		clk:     clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)),
		discord: &testadorFalso{}, email: &carteiroFalso{},
		dono: uuid.Must(uuid.NewV7()), outro: uuid.Must(uuid.NewV7()),
	}
	for _, id := range []uuid.UUID{b.dono, b.outro} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
			id, id.String()+"@exemplo.com"); err != nil {
			t.Fatal(err)
		}
	}
	var mailer VerificationMailer
	if comEmail {
		mailer = b.email
	}
	b.svc = NewChannelService(pool, box, b.clk, b.discord, mailer, "https://vigilume.dev/")
	return b
}

func (b *bancadaCanais) listar(t *testing.T, userID uuid.UUID) []Channel {
	t.Helper()
	cs, err := b.svc.List(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestCreateDiscordSendsTheTestAndSavesVerified(t *testing.T) {
	b := novaBancadaCanais(t, true)
	c, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: " " + webhookValido + " "})
	if err != nil {
		t.Fatal(err)
	}
	if b.discord.quantos() != 1 || b.discord.urls[0] != webhookValido {
		t.Errorf("mensagens de teste = %v", b.discord.urls)
	}
	if c.Status != StatusVerified || c.Hint != "webhook …6789" {
		t.Errorf("canal = %+v", c)
	}
	cs := b.listar(t, b.dono)
	if len(cs) != 1 || cs[0].ID != c.ID || cs[0].Status != StatusVerified || cs[0].Hint != "webhook …6789" {
		t.Errorf("listagem = %+v", cs)
	}
}

// Envia ANTES de gravar: falhou o envio, nada fica no banco.
func TestFailedDeliverySavesNothing(t *testing.T) {
	b := novaBancadaCanais(t, true)
	b.discord.err = &SendError{Reason: ReasonRejected, Permanent: true}
	b.email.err = &SendError{Reason: ReasonServerError}

	for _, in := range []ChannelInput{
		{Kind: KindDiscord, URL: webhookValido},
		{Kind: KindEmail, Email: "pessoa@exemplo.com"},
	} {
		if _, err := b.svc.Create(context.Background(), b.dono, in); !errors.Is(err, ErrDeliveryFailed) {
			t.Errorf("%s com envio falho deu %v, queria ErrDeliveryFailed", in.Kind, err)
		}
	}
	if cs := b.listar(t, b.dono); len(cs) != 0 {
		t.Errorf("%d canais gravados depois de envios que falharam", len(cs))
	}
}

func TestInvalidInputIsRejectedBeforeAnySend(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for _, c := range []struct {
		in            ChannelInput
		campo, motivo string
	}{
		{ChannelInput{}, "kind", ReasonRequired},
		{ChannelInput{Kind: "slack"}, "kind", ReasonInvalid},
		{ChannelInput{Kind: KindDiscord}, "url", ReasonRequired},
		{ChannelInput{Kind: KindDiscord, URL: "https://evil.io/api/webhooks/1/t"}, "url", ReasonInvalid},
		{ChannelInput{Kind: KindEmail}, "email", ReasonRequired},
		{ChannelInput{Kind: KindEmail, Email: "Fulano <p@x.com>"}, "email", ReasonInvalid},
	} {
		_, err := b.svc.Create(context.Background(), b.dono, c.in)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Field != c.campo || verr.Reason != c.motivo {
			t.Errorf("Create(%+v) = %v, queria %s/%s", c.in, err, c.campo, c.motivo)
		}
	}
	if b.discord.quantos() != 0 || len(b.email.links) != 0 {
		t.Error("entrada inválida chegou a disparar envio")
	}
}

func TestEmailIsUnavailableWithoutResend(t *testing.T) {
	b := novaBancadaCanais(t, false)
	_, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Field != "kind" || verr.Reason != ReasonUnavailable {
		t.Errorf("e-mail sem Resend deu %v, queria kind/unavailable", err)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "pessoa@Exemplo.com"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusPending || c.Hint != "p***@exemplo.com" || b.email.enderecos[0] != "pessoa@exemplo.com" {
		t.Errorf("canal = %+v, endereço = %v", c, b.email.enderecos)
	}
	token := b.email.ultimoToken(t)

	if st, err := b.svc.CheckToken(ctx, token); err != nil || st != VerifyPending {
		t.Fatalf("CheckToken = %v, %v; queria pendente", st, err)
	}
	// Olhar não verifica: é o que protege do leitor de links (D2).
	if cs := b.listar(t, b.dono); cs[0].Status != StatusPending {
		t.Fatalf("o GET verificou o canal: %+v", cs[0])
	}

	if err := b.svc.ConfirmToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if cs := b.listar(t, b.dono); cs[0].Status != StatusVerified {
		t.Errorf("depois de confirmar: %+v", cs[0])
	}
	if st, _ := b.svc.CheckToken(ctx, token); st != VerifyDone {
		t.Errorf("CheckToken depois de confirmar = %v, queria VerifyDone", st)
	}
	if err := b.svc.ConfirmToken(ctx, token); err != nil {
		t.Errorf("segunda confirmação deu %v, queria sucesso idempotente", err)
	}
}

func TestExpiredTokenNeitherChecksNorConfirms(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	if _, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"}); err != nil {
		t.Fatal(err)
	}
	token := b.email.ultimoToken(t)
	b.clk.Advance(VerifyTTL)

	if st, _ := b.svc.CheckToken(ctx, token); st != VerifyInvalid {
		t.Errorf("CheckToken vencido = %v", st)
	}
	if err := b.svc.ConfirmToken(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ConfirmToken vencido = %v", err)
	}
	if cs := b.listar(t, b.dono); cs[0].Status != StatusExpired {
		t.Errorf("status = %s, queria expired", cs[0].Status)
	}
}

func TestMalformedTokenIsInvalid(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for _, token := range []string{"", "curto", strings.Repeat("A", 43) + "=", "../../etc"} {
		if st, err := b.svc.CheckToken(context.Background(), token); err != nil || st != VerifyInvalid {
			t.Errorf("CheckToken(%q) = %v, %v", token, st, err)
		}
		if err := b.svc.ConfirmToken(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("ConfirmToken(%q) = %v", token, err)
		}
	}
}

// Reenviar troca o token: o link antigo para de valer. Vale também para
// um pendente que já venceu.
func TestResendReplacesTheToken(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	antigo := b.email.ultimoToken(t)
	b.clk.Advance(VerifyTTL + time.Hour)

	if err := b.svc.ResendVerification(ctx, b.dono, c.ID); err != nil {
		t.Fatal(err)
	}
	novo := b.email.ultimoToken(t)
	if novo == antigo || len(b.email.enderecos) != 2 || b.email.enderecos[1] != "p@x.com" {
		t.Fatalf("reenvio: token novo=%v, endereços=%v", novo != antigo, b.email.enderecos)
	}
	if st, _ := b.svc.CheckToken(ctx, antigo); st != VerifyInvalid {
		t.Errorf("token antigo depois do reenvio = %v", st)
	}
	if st, _ := b.svc.CheckToken(ctx, novo); st != VerifyPending {
		t.Errorf("token novo = %v", st)
	}
}

func TestResendRefusesWhatIsNotPending(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	discord, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	email, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindEmail, Email: "p@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmToken(ctx, b.email.ultimoToken(t)); err != nil {
		t.Fatal(err)
	}

	for nome, id := range map[string]uuid.UUID{"discord": discord.ID, "e-mail verificado": email.ID} {
		if err := b.svc.ResendVerification(ctx, b.dono, id); !errors.Is(err, ErrNotPending) {
			t.Errorf("reenviar %s deu %v, queria ErrNotPending", nome, err)
		}
	}
	if err := b.svc.ResendVerification(ctx, b.outro, email.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reenviar canal de outro usuário deu %v, queria ErrNotFound", err)
	}
}

// O limite é conferido ANTES do envio: o 11º canal não chega a mandar a
// mensagem de teste. (A segunda conferência, na gravação, é a que vale sob
// concorrência; este teste trava a primeira.)
func TestChannelLimitIsCheckedBeforeSending(t *testing.T) {
	b := novaBancadaCanais(t, true)
	for range MaxChannels {
		if _, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := b.svc.Create(context.Background(), b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("canal %d deu %v, queria ErrLimitReached", MaxChannels+1, err)
	}
	if n := b.discord.quantos(); n != MaxChannels {
		t.Errorf("%d mensagens de teste, queria %d: o canal recusado também recebeu", n, MaxChannels)
	}
}

// A segunda conferência do limite, a da gravação, é serializada pela trava
// na linha do usuário (LockUser). O R9 da spec do Plano 5 dizia que não; o
// código tinha a trava desde o Plano 3, e faltava esta prova (Plano 8). Com
// nove canais, duas criações simultâneas se encontram depois da contagem: sem
// a trava, as duas contam nove e gravam, e o usuário fica com onze.
func TestConcurrentCreatesDoNotPassTheChannelLimit(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	for range MaxChannels - 1 {
		if _, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido}); err != nil {
			t.Fatal(err)
		}
	}

	// As duas criações esperam uma pela outra depois de contar, por no
	// máximo 500 ms. Com a trava, a segunda nem chega a contar antes do
	// commit da primeira, e a espera só encurta o teste.
	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	b.svc.depoisDeContar = func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	}

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
		}()
	}
	wg.Wait()

	if n := len(b.listar(t, b.dono)); n != MaxChannels {
		t.Errorf("%d canais depois de duas criações simultâneas (erros: %v), queria %d", n, erros, MaxChannels)
	}
	recusadas := 0
	for _, err := range erros {
		if errors.Is(err, ErrLimitReached) {
			recusadas++
		} else if err != nil {
			t.Errorf("erro inesperado: %v", err)
		}
	}
	if recusadas != 1 {
		t.Errorf("%d criações recusadas pelo limite, queria 1", recusadas)
	}
}

func TestAnotherUsersChannelIsInvisible(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	if cs := b.listar(t, b.outro); len(cs) != 0 {
		t.Errorf("o outro usuário listou %d canais", len(cs))
	}
	if err := b.svc.Delete(ctx, b.outro, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("apagar canal alheio deu %v", err)
	}
	if err := b.svc.Delete(ctx, b.dono, c.ID); err != nil {
		t.Errorf("o dono apagando o próprio canal deu %v", err)
	}
	if cs := b.listar(t, b.dono); len(cs) != 0 {
		t.Errorf("depois do DELETE, %d canais", len(cs))
	}
}

func TestUnreadableChannelDoesNotBreakTheList(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	c, err := b.svc.Create(ctx, b.dono, ChannelInput{Kind: KindDiscord, URL: webhookValido})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := b.svc.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notif_channels SET config_enc = '\x00ff' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	cs := b.listar(t, b.dono)
	if len(cs) != 1 || cs[0].Hint != "" || cs[0].Status != StatusVerified {
		t.Errorf("listagem com linha ilegível = %+v", cs)
	}
}
