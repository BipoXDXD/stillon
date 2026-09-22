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

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/crypto"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
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
	return auth.NewTOTP(pool, box, "Sentinela"), pool, userID
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
	if err := svc.Verify(ctx, userID, code, now); err != nil {
		t.Errorf("o código correto deveria ser aceito: %v", err)
	}
}

// Skew de 1 período aceita o código anterior e o seguinte. Sem tolerância,
// relógio de celular alguns segundos fora já reprova o usuário; com tolerância
// alta demais, a janela de reúso do código cresce.
func TestVerifyToleratesOnePeriodOfClockSkew(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	// Instante fixo no meio de um período, para que "anterior" e "seguinte"
	// sejam mesmo os períodos vizinhos e não dependam de onde o relógio da
	// máquina caiu dentro da janela de 30 segundos.
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
	if _, err := svc.Confirm(ctx, userID, code, now); err != nil {
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
	if anterior == code {
		t.Fatal("o código do período anterior saiu igual ao atual: a asserção não distinguiria nada")
	}
	if err := svc.Verify(ctx, userID, anterior, now); err != nil {
		t.Errorf("código do período anterior deveria ser aceito: %v", err)
	}

	seguinte := gerar(30 * time.Second)
	if seguinte != code && seguinte != anterior {
		if err := svc.Verify(ctx, userID, seguinte, now); err != nil {
			t.Errorf("código do período seguinte deveria ser aceito: %v", err)
		}
	}

	// A outra ponta: um código distante não pode entrar, senão a janela de
	// reúso de um código capturado vira minutos. Como o Verify aceitaria o
	// código atual (provado acima), a recusa aqui só pode vir do skew.
	distante := gerar(-5 * time.Minute)
	if distante == code || distante == anterior {
		t.Skip("colisão de seis dígitos entre períodos distantes; nada a distinguir nesta execução")
	}
	if err := svc.Verify(ctx, userID, distante, now); err == nil {
		t.Error("código de 5 minutos atrás não deveria ser aceito")
	}
	adiante := gerar(5 * time.Minute)
	if adiante != code && adiante != anterior && adiante != distante {
		if err := svc.Verify(ctx, userID, adiante, now); err == nil {
			t.Error("código de 5 minutos à frente não deveria ser aceito")
		}
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
