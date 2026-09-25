package auth_test

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

var totpMasterKey = bytes.Repeat([]byte{3}, crypto.KeySize)

func setupTOTP(t *testing.T) (*auth.TOTP, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)

	box, err := crypto.NewBox(totpMasterKey)
	if err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		userID, "totp@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return auth.NewTOTP(pool, box, "Vigilume"), pool, userID
}

// secretFromURI extrai o segredo base32 da URI otpauth:// para que o teste
// consiga gerar um código válido, como um app autenticador faria.
func secretFromURI(t *testing.T, uri string) string {
	t.Helper()
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	return key.Secret()
}

func TestEnrollThenConfirm(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}

	// Antes de confirmar, o TOTP não pode estar ativo — senão um cadastro
	// interrompido trancaria o usuário para fora da própria conta.
	//
	// Sozinha, esta asserção seria verdadeira num IsEnabled que devolvesse
	// false sempre; quem morde na direção oposta é a asserção logo abaixo da
	// confirmação, que exige true.
	enabled, err := svc.IsEnabled(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("TOTP ficou ativo antes da confirmação")
	}

	secret := secretFromURI(t, uri)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	recovery, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatalf("confirmar: %v", err)
	}
	if len(recovery) != 10 {
		t.Errorf("%d códigos de recuperação, queria 10", len(recovery))
	}

	enabled, err = svc.IsEnabled(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("TOTP deveria estar ativo depois da confirmação")
	}

	// O segredo nunca pode estar em claro no banco.
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT totp_secret_enc FROM users WHERE id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("nada foi gravado em totp_secret_enc: as asserções de ausência abaixo seriam verdadeiras por vacuidade")
	}
	if bytes.Contains(stored, []byte(secret)) {
		t.Error("o segredo TOTP foi gravado em claro (base32)")
	}
	// Só o base32 não basta: gravar os bytes crus do segredo também é gravar
	// em claro, e passaria pela asserção acima.
	cru, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, cru) {
		t.Error("o segredo TOTP foi gravado em claro (bytes crus)")
	}

	// A perna positiva: o que está lá é de fato o segredo selado com a AAD
	// desta linha. Sem ela, gravar lixo aleatório passaria em tudo acima.
	box, err := crypto.NewBox(totpMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	aberto, err := box.Open(stored, crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		t.Fatalf("o valor gravado não abre com a AAD desta linha: %v", err)
	}
	if string(aberto.Reveal()) != secret {
		t.Error("o valor gravado não é o segredo da URI entregue ao usuário")
	}

	// E a AAD precisa amarrar a linha: o selo de um usuário não pode abrir
	// no lugar de outro.
	if _, err := box.Open(stored, crypto.AAD("users", "totp_secret_enc", uuid.New())); err == nil {
		t.Error("o segredo abriu com a AAD de outro usuário: a AAD não amarra a linha")
	}
}

func TestVerifyRejectsWrongCode(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secretFromURI(t, uri), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("erro = %v, queria ErrTOTPInvalid", err)
	}
	// A perna oposta: o código certo passa. Sem ela, um Verify que recusasse
	// tudo passaria na asserção acima.
	//
	// É o código do período SEGUINTE, e não o mesmo do Confirm: o timestep da
	// confirmação já entra consumido (RFC 6238 §5.2), então reusá-lo aqui
	// testaria o reúso, não a aceitação.
	seguinte, err := totp.GenerateCode(secretFromURI(t, uri), now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, userID, seguinte, now.Add(30*time.Second)); err != nil {
		t.Errorf("o código correto deveria ser aceito: %v", err)
	}
}

// Skew de 1 período aceita o código anterior e o seguinte. Sem tolerância,
// relógio de celular alguns segundos fora já reprova o usuário; com tolerância
// alta demais, a janela de reúso do código cresce.
func TestVerifyToleratesOnePeriodOfClockSkew(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	// Instante fixo no meio de um período, para que "anterior" e "seguinte"
	// sejam mesmo os períodos vizinhos e não dependam de onde o relógio da
	// máquina caiu dentro da janela de 30 segundos.
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	// Segredo FIXO, gravado direto no banco. O Enroll gera o segredo
	// internamente e não aceita um de fora, e um segredo aleatório a cada
	// execução era o que obrigava as asserções abaixo a serem condicionais:
	// dois períodos podem render os mesmos seis dígitos (~2e-6), e o teste
	// então PULAVA — apagando justamente as pernas negativas, que são as
	// únicas que provam que a janela de skew não é larga demais. Medido: com
	// o skew arrebentado e uma colisão simulada, a suíte ficava VERDE.
	//
	// Trocar o offset não resolveria: o segredo continuaria sorteado e a
	// colisão voltaria a ser possível. Só o segredo determinístico fecha.
	// O caminho do Enroll continua coberto por TestEnrollThenConfirm.
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	gravarSegredoTOTP(t, pool, userID, secret)

	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	gerar := func(d time.Duration) string {
		t.Helper()
		c, err := totp.GenerateCode(secret, now.Add(d))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	anterior := gerar(-30 * time.Second)
	seguinte := gerar(30 * time.Second)
	distante := gerar(-5 * time.Minute)
	adiante := gerar(5 * time.Minute)

	// Com segredo fixo os cinco códigos são conhecidos e distintos. A
	// verificação é uma trava contra regressão do próprio teste: se um dia
	// colidirem, o teste FALHA em vez de pular calado.
	vistos := map[string]string{}
	for nome, c := range map[string]string{
		"atual": code, "anterior": anterior, "seguinte": seguinte,
		"distante": distante, "adiante": adiante,
	} {
		if outro, jaVisto := vistos[c]; jaVisto {
			t.Fatalf("os códigos %q e %q colidiram (%s): as asserções abaixo não distinguiriam nada",
				nome, outro, c)
		}
		vistos[c] = nome
	}

	if err := svc.Verify(ctx, userID, anterior, now); err != nil {
		t.Errorf("código do período anterior deveria ser aceito: %v", err)
	}
	if err := svc.Verify(ctx, userID, seguinte, now); err != nil {
		t.Errorf("código do período seguinte deveria ser aceito: %v", err)
	}

	// A outra ponta: um código distante não pode entrar, senão a janela de
	// reúso de um código capturado vira minutos. Como o Verify aceitaria o
	// código atual (provado acima), a recusa aqui só pode vir do skew.
	if err := svc.Verify(ctx, userID, distante, now); err == nil {
		t.Error("código de 5 minutos atrás não deveria ser aceito")
	}
	if err := svc.Verify(ctx, userID, adiante, now); err == nil {
		t.Error("código de 5 minutos à frente não deveria ser aceito")
	}
}

// gravarSegredoTOTP põe um segredo conhecido na linha do usuário, já
// confirmado, selando-o com a mesma AAD que o serviço usa. É o que permite um
// teste determinístico sem passar pelo Enroll, que sorteia o segredo.
func gravarSegredoTOTP(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, secret string) {
	t.Helper()
	box, err := crypto.NewBox(totpMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte(secret), crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET totp_secret_enc = $2, totp_confirmed_at = $3 WHERE id = $1`,
		userID, sealed, time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryCodeWorksExactlyOnce(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secretFromURI(t, uri), now)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatal(err)
	}

	contar := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := contar(); n != len(recovery) {
		t.Fatalf("%d códigos no banco, queria %d", n, len(recovery))
	}

	if err := svc.Verify(ctx, userID, recovery[0], now); err != nil {
		t.Fatalf("primeiro uso do código de recuperação deveria funcionar: %v", err)
	}
	// Um único código foi consumido: nem zero (SELECT no lugar de DELETE),
	// nem todos.
	if n := contar(); n != len(recovery)-1 {
		t.Errorf("%d códigos restantes, queria %d: o consumo não apagou exatamente uma linha",
			n, len(recovery)-1)
	}
	if err := svc.Verify(ctx, userID, recovery[0], now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Error("o código de recuperação foi aceito duas vezes")
	}
	if err := svc.Verify(ctx, userID, recovery[1], now); err != nil {
		t.Errorf("os outros códigos deveriam continuar válidos: %v", err)
	}
}

func TestVerifyFailsWhenNotEnrolled(t *testing.T) {
	svc, _, userID := setupTOTP(t)

	if err := svc.Verify(context.Background(), userID, "123456", time.Now()); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("erro = %v, queria ErrTOTPNotEnrolled", err)
	}
}

// Cadastro sem confirmação não ativa nada: o Verify recusa como não cadastrado
// mesmo com o código certo do segredo já gravado. É o que impede que fechar a
// tela no meio do cadastro tranque o usuário para fora.
func TestVerifyFailsWhenEnrolledButNotConfirmed(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secretFromURI(t, uri), now)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, code, now); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("erro = %v, queria ErrTOTPNotEnrolled", err)
	}
}

// R38: com um fator já confirmado, Enroll recusa — e, o que mais importa,
// recusa SEM estragar nada.
//
// A versão anterior zerava o totp_confirmed_at, e isso era um bypass completo:
// quem tivesse um access token roubado desligava o segundo fator numa
// requisição. A asserção que separa "recusou" de "recusou sem estragar" é a do
// banco, logo abaixo — devolver erro é fácil, o difícil é não ter apagado o
// fator no caminho.
func TestEnrollIsRefusedWhenTOTPAlreadyConfirmed(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := svc.Enroll(ctx, userID, "totp@exemplo.com"); err != nil {
		t.Fatal(err)
	}

	// Antes de confirmar, recadastrar é permitido: é o cadastro interrompido
	// que recomeça. Esta é a perna oposta, sem a qual a recusa mais abaixo
	// poderia vir de um Enroll que recusasse sempre. O segredo que vale é o
	// deste segundo cadastro.
	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatalf("recadastro antes da confirmação deveria ser permitido: %v", err)
	}
	secret := secretFromURI(t, uri)

	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Enroll(ctx, userID, "totp@exemplo.com"); !errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		t.Errorf("erro = %v, queria ErrTOTPAlreadyEnabled", err)
	}

	// O fator continua ativo no banco.
	var confirmadoEm *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&confirmadoEm); err != nil {
		t.Fatal(err)
	}
	if confirmadoEm == nil {
		t.Error("o enroll recusado ainda assim desligou o segundo fator: bypass de 2FA")
	}
	if enabled, err := svc.IsEnabled(ctx, userID); err != nil || !enabled {
		t.Errorf("IsEnabled = %v (erro %v) depois do enroll recusado, queria true", enabled, err)
	}
	// E o segredo original continua valendo: o enroll recusado não pode ter
	// trocado a chave do usuário por outra que ele não tem no telefone. Usa o
	// período seguinte porque o da confirmação já foi consumido.
	depois := now.Add(30 * time.Second)
	novo, err := totp.GenerateCode(secretFromURI(t, uri), depois)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, userID, novo, depois); err != nil {
		t.Errorf("o segredo confirmado deixou de valer depois do enroll recusado: %v", err)
	}
}

// eventosDeAuditoria conta as entradas de audit_log de uma ação.
func eventosDeAuditoria(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2`,
		userID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// R39: ativar o segundo fator deixa rastro.
func TestConfirmIsAudited(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	// O cadastro sozinho não é o evento: quem ativa é a confirmação.
	if n := eventosDeAuditoria(t, pool, userID, "totp.enabled"); n != 0 {
		t.Errorf("%d eventos totp.enabled antes de confirmar, queria 0", n)
	}

	code, err := totp.GenerateCode(secretFromURI(t, uri), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
		t.Fatal(err)
	}

	if n := eventosDeAuditoria(t, pool, userID, "totp.enabled"); n != 1 {
		t.Errorf("%d eventos totp.enabled depois de confirmar, queria 1", n)
	}
}

// R39: uso de código de recuperação é sinal de perda de dispositivo OU de
// comprometimento, e é a única pista que o dono da conta teria.
func TestRecoveryCodeUseIsAudited(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Now()

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	secret := secretFromURI(t, uri)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatal(err)
	}

	// Um código normal do app não é evento de recuperação. Sem esta perna,
	// um insertAudit incondicional passaria no teste. Período seguinte, pelo
	// mesmo motivo de sempre: o da confirmação já foi consumido.
	depois := now.Add(30 * time.Second)
	normal, err := totp.GenerateCode(secretFromURI(t, uri), depois)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, userID, normal, depois); err != nil {
		t.Fatal(err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.recovery_used"); n != 0 {
		t.Errorf("%d eventos totp.recovery_used depois de um código normal, queria 0", n)
	}

	// Um código errado também não.
	if err := svc.Verify(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Fatal(err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.recovery_used"); n != 0 {
		t.Errorf("%d eventos totp.recovery_used depois de um código errado, queria 0", n)
	}

	if err := svc.Verify(ctx, userID, recovery[0], now); err != nil {
		t.Fatal(err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.recovery_used"); n != 1 {
		t.Errorf("%d eventos totp.recovery_used depois do uso, queria 1", n)
	}
}

// TestConfirmRejectsAWrongCode trava a propriedade central do cadastro do
// segundo fator, que estava sem teste nenhum.
//
// Verify com código errado era testado em três lugares, o que fazia a
// propriedade PARECER coberta; Confirm com código errado, em nenhum, e a
// linha que devolve ErrTOTPInvalid nunca havia executado. Sem esta trava,
// derrubar o `!` da guarda deixava a suíte inteira verde — e a confirmação
// deixava de provar posse do autenticador, que é a única coisa que ela faz.
func TestConfirmRejectsAWrongCode(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	if _, err := svc.Enroll(ctx, userID, "totp@exemplo.com"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Confirm(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("Confirm com código errado devolveu %v, queria ErrTOTPInvalid", err)
	}

	// A asserção que distingue "recusou" de "recusou e ativou mesmo assim" —
	// a mesma distinção que escondeu o bug do Rotate na Task 6.
	var confirmado *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&confirmado); err != nil {
		t.Fatal(err)
	}
	if confirmado != nil {
		t.Error("o Confirm recusado ativou o segundo fator mesmo assim")
	}

	var codigos int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID).Scan(&codigos); err != nil {
		t.Fatal(err)
	}
	if codigos != 0 {
		t.Errorf("o Confirm recusado gerou %d códigos de recuperação", codigos)
	}
}

// TestCodeCannotBeReusedWithinTheWindow implementa a RFC 6238 §5.2: o
// verificador deve recusar um segundo OTP para o mesmo timestep.
//
// Sem isso, um código observado valia pela janela inteira — o período mais o
// skew, cerca de noventa segundos. Exige observar o código (ombro, phishing
// ao vivo, malware de tela), mas dentro dessa janela o mesmo código servia
// quantas vezes o atacante quisesse.
func TestCodeCannotBeReusedWithinTheWindow(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	gravarSegredoTOTP(t, pool, userID, secret)

	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Verify(ctx, userID, code, now); err != nil {
		t.Fatalf("o primeiro uso deveria passar: %v", err)
	}
	if err := svc.Verify(ctx, userID, code, now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o segundo uso do MESMO código devolveu %v, queria ErrTOTPInvalid", err)
	}

	// A perna oposta, sem a qual um Verify que recusasse tudo depois do
	// primeiro uso passaria: o código do período SEGUINTE continua entrando.
	seguinte, err := totp.GenerateCode(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if seguinte == code {
		t.Fatal("os dois períodos colidiram; a asserção seguinte não distinguiria nada")
	}
	if err := svc.Verify(ctx, userID, seguinte, now); err != nil {
		t.Errorf("o código do período seguinte deveria entrar: %v", err)
	}
}

// Um código ANTERIOR ao último aceito também não volta: sem isso, o atacante
// que observou o código das 12:00:15 o usaria depois de o dono ter entrado
// com o das 12:00:45.
func TestAnEarlierCodeIsRefusedAfterALaterOne(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	gravarSegredoTOTP(t, pool, userID, secret)

	anterior, err := totp.GenerateCode(secret, now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	atual, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if anterior == atual {
		t.Fatal("os dois períodos colidiram; o teste seria vácuo")
	}

	if err := svc.Verify(ctx, userID, atual, now); err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, userID, anterior, now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o código do período anterior devolveu %v depois do atual, queria ErrTOTPInvalid", err)
	}
}

// TestConfirmIsRefusedOnAnAlreadyConfirmedAccount fecha a simetria que
// faltava ao R38.
//
// Confirm não tinha guarda nenhuma: o UPDATE era `WHERE id = $1` seco,
// enquanto o do Enroll já trazia `AND totp_confirmed_at IS NULL`. Um segundo
// Confirm rotacionava os dez códigos de recuperação em silêncio, gravava
// outro totp.enabled e — o pior, que não estava no relato — REESCREVIA
// totp_confirmed_at com o instante atual, apagando a data de ativação
// original, que é justamente a evidência de que uma investigação de sequestro
// precisaria.
func TestConfirmIsRefusedOnAnAlreadyConfirmedAccount(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	secret := secretFromURI(t, uri)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	primeiros, err := svc.Confirm(ctx, userID, code, now)
	if err != nil {
		t.Fatal(err)
	}

	var ativadoEm *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&ativadoEm); err != nil {
		t.Fatal(err)
	}

	// Segundo Confirm, com um código válido de um período posterior.
	depois := now.Add(90 * time.Second)
	outro, err := totp.GenerateCode(secret, depois)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, outro, depois); !errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		t.Errorf("segundo Confirm devolveu %v, queria ErrTOTPAlreadyEnabled", err)
	}
	// Com o fator ligado, o código nem é conferido (C1 da revisão do Plano
	// 10): o errado recebe o mesmo ErrTOTPAlreadyEnabled do certo, e o
	// Confirm deixa de ser um oráculo do código do segredo ativo.
	if _, err := svc.Confirm(ctx, userID, codigoErradoDe(t, secret, depois), depois); !errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		t.Errorf("Confirm com código errado e o fator ligado devolveu %v, queria ErrTOTPAlreadyEnabled", err)
	}

	var agora *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&agora); err != nil {
		t.Fatal(err)
	}
	if agora == nil || !agora.Equal(*ativadoEm) {
		t.Errorf("a data de ativação virou %v, queria continuar %v", agora, ativadoEm)
	}

	// Os códigos originais continuam valendo: não podem ser invalidados sem o
	// usuário pedir.
	if err := svc.Verify(ctx, userID, primeiros[0], depois); err != nil {
		t.Errorf("o código de recuperação original deixou de valer: %v", err)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.enabled"); n != 1 {
		t.Errorf("%d eventos totp.enabled, queria 1", n)
	}
}

// TestDisableRejectsAWrongCode fecha um buraco que eu mesmo abri ao escrever
// o Disable, e que tem a assinatura EXATA do item 16.
//
// O teste de integração cobria "sem senha" e "senha errada", que param no
// handler, e o caminho feliz. Faltava a combinação que chega ao serviço:
// senha CERTA com código ERRADO. Medido: com a recusa removida, a suíte
// inteira ficava verde, e qualquer sequência de seis dígitos desligava o
// segundo fator de quem já tivesse a senha — que é precisamente o bypass que
// a rota existe para não ser.
func TestDisableRejectsAWrongCode(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	gravarSegredoTOTP(t, pool, userID, secret)

	if err := svc.Disable(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("Disable com código errado devolveu %v, queria ErrTOTPInvalid", err)
	}

	// A asserção que separa "recusou" de "recusou e desligou assim mesmo".
	var confirmado *time.Time
	var sealed []byte
	if err := pool.QueryRow(ctx,
		`SELECT totp_confirmed_at, totp_secret_enc FROM users WHERE id = $1`, userID).
		Scan(&confirmado, &sealed); err != nil {
		t.Fatal(err)
	}
	if confirmado == nil {
		t.Error("o Disable recusado desligou o segundo fator mesmo assim")
	}
	if len(sealed) == 0 {
		t.Error("o Disable recusado apagou o segredo mesmo assim")
	}

	// A perna oposta: o código certo desliga. Sem ela, um Disable que
	// recusasse tudo passaria nas asserções acima.
	certo, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Disable(ctx, userID, certo, now); err != nil {
		t.Fatalf("Disable com o código certo devolveu %v", err)
	}
}

// Desativar o que nunca foi ativado não pode virar caminho de escrita.
//
// HONESTIDADE SOBRE O QUE ESTE TESTE PRENDE: a propriedade tem DOIS guardas
// independentes — a checagem precoce de `confirmed` e o
// `AND totp_confirmed_at IS NOT NULL` na WHERE do UPDATE. Medido: removendo
// a checagem precoce, este teste continua VERDE, porque a WHERE segura
// sozinha. Ele prova a propriedade, não a primeira guarda. Mantive as duas
// assim mesmo: a precoce evita abrir transação à toa, e defesa em
// profundidade aqui custa três linhas.
func TestDisableOnAnAccountWithoutTOTP(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)

	if err := svc.Disable(context.Background(), userID, "000000", now); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("Disable sem segundo fator devolveu %v, queria ErrTOTPNotEnrolled", err)
	}
}

// codigoErradoDe é um código de seis dígitos que não é o do segredo no
// instante dado.
func codigoErradoDe(t *testing.T, secret string, em time.Time) string {
	t.Helper()
	certo, err := totp.GenerateCode(secret, em)
	if err != nil {
		t.Fatal(err)
	}
	if certo == "000000" {
		return "111111"
	}
	return "000000"
}
