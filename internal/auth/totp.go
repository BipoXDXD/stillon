package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/crypto"
)

var (
	ErrTOTPInvalid     = errors.New("código de segundo fator inválido")
	ErrTOTPNotEnrolled = errors.New("segundo fator não cadastrado")
	// ErrTOTPAlreadyEnabled fecha o bypass do R38: ver Enroll.
	ErrTOTPAlreadyEnabled = errors.New("segundo fator já está ativo")
)

const (
	totpPeriod = 30
	// Skew de 1 aceita o código do período anterior e do seguinte, cobrindo
	// relógio de celular levemente fora. Valores maiores alargam demais a
	// janela em que um código capturado continua servindo.
	totpSkew          = 1
	recoveryCodeCount = 10
	recoveryCodeBytes = 10
)

// TOTP implementa o segundo fator da spec §5.2.
//
// O segredo fica cifrado em repouso com a AAD amarrada à tabela, à coluna e ao
// id do usuário: o selo de uma linha não abre no lugar de outra.
type TOTP struct {
	pool   *pgxpool.Pool
	box    *crypto.Box
	issuer string
}

func NewTOTP(pool *pgxpool.Pool, box *crypto.Box, issuer string) *TOTP {
	return &TOTP{pool: pool, box: box, issuer: issuer}
}

// Enroll gera um segredo novo e o grava cifrado, ainda não confirmado.
//
// Gravar sem ativar é deliberado: se o usuário fechar a tela no meio do
// cadastro, a conta continua acessível com senha apenas.
//
// Com um fator JÁ confirmado, Enroll recusa (R38). A versão anterior zerava o
// totp_confirmed_at, e isso era um bypass completo do segundo fator: quem
// tivesse um access token — o caso que importa é o token ROUBADO — desligava o
// 2FA numa requisição, sem provar posse de nada, e o login seguinte voltava a
// passar só com a senha. Comprometer a sessão passava a bastar, que é
// exatamente o que o segundo fator existe para impedir.
//
// O preço aceito e registrado: não há caminho para trocar de autenticador
// depois de ativado. É lacuna de funcionalidade, não de segurança, e sai do
// Plano 5 com prova de posse adequada. Não "conserte" isso zerando a coluna.
func (s *TOTP) Enroll(ctx context.Context, userID uuid.UUID, email string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: email,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // exigido por Google Authenticator e afins
	})
	if err != nil {
		return "", fmt.Errorf("gerar chave TOTP: %w", err)
	}

	sealed, err := s.box.Seal([]byte(key.Secret()), crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", fmt.Errorf("cifrar segredo TOTP: %w", err)
	}

	// A guarda vai na cláusula WHERE, e não numa consulta anterior: entre um
	// SELECT e o UPDATE cabe uma confirmação concorrente, e o segredo novo
	// sobrescreveria um fator recém-ativado.
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2 WHERE id = $1 AND totp_confirmed_at IS NULL`,
		userID, sealed)
	if err != nil {
		return "", fmt.Errorf("gravar segredo TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Ou a conta sumiu, ou já tem fator ativo. A consulta extra só roda
		// neste caminho de falha.
		enabled, err := s.IsEnabled(ctx, userID)
		if err != nil {
			return "", err
		}
		if enabled {
			return "", ErrTOTPAlreadyEnabled
		}
		return "", ErrNotFound
	}

	return key.URL(), nil
}

// Confirm ativa o segundo fator e devolve os códigos de recuperação. Eles são
// mostrados uma única vez: o banco guarda apenas o hash.
func (s *TOTP) Confirm(ctx context.Context, userID uuid.UUID, code string, now time.Time) ([]string, error) {
	secret, _, sealed, err := s.loadSealed(ctx, userID)
	if err != nil {
		return nil, err
	}
	step, ok := validateTOTP(code, secret, now)
	if !ok {
		return nil, ErrTOTPInvalid
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		raw := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("gerar código de recuperação: %w", err)
		}
		codes[i] = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	// A guarda `totp_confirmed_at IS NULL` é a simetria que faltava ao R38,
	// que a pôs no Enroll e esqueceu do Confirm. Sem ela, um segundo Confirm
	// rotacionava os dez códigos de recuperação em silêncio, gravava outro
	// totp.enabled e REESCREVIA a data de ativação — apagando a evidência que
	// uma investigação de sequestro precisaria. Na WHERE, e não num SELECT
	// anterior, pelo motivo de sempre: entre ler e escrever cabe outra
	// confirmação.
	//
	// O timestep da confirmação já entra consumido: o código usado para
	// ativar não pode servir de novo para entrar.
	//
	// E o segredo tem de ser o mesmo que validou o código (Task 11 do Plano
	// 5): a troca de senha e o set-password apagam o segredo pendente, e uma
	// confirmação que o leu antes ligaria o 2FA sem segredo nenhum — o dono
	// nunca mais entraria.
	tag, err := tx.Exec(ctx,
		`UPDATE users SET totp_confirmed_at = $2, totp_last_timestep = $3
		   WHERE id = $1 AND totp_confirmed_at IS NULL AND totp_secret_enc = $4`,
		userID, now, step, sealed)
	if err != nil {
		return nil, fmt.Errorf("ativar TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		enabled, err := s.IsEnabled(ctx, userID)
		if err != nil {
			return nil, err
		}
		if enabled {
			return nil, ErrTOTPAlreadyEnabled
		}
		return nil, ErrTOTPNotEnrolled
	}
	// Recadastro invalida os códigos anteriores.
	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("limpar códigos antigos: %w", err)
	}
	for _, c := range codes {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("gerar id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
			id, userID, crypto.HashToken(c)); err != nil {
			return nil, fmt.Errorf("gravar código de recuperação: %w", err)
		}
	}

	// Ativar um segundo fator é evento de segurança, e vai na mesma transação
	// para que não exista ativação sem rastro (R39).
	if err := insertAudit(ctx, tx, userID, "totp.enabled", nil, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirmar TOTP: %w", err)
	}
	return codes, nil
}

// Verify aceita tanto um código do app quanto um código de recuperação. O de
// recuperação é consumido no momento do uso.
func (s *TOTP) Verify(ctx context.Context, userID uuid.UUID, code string, now time.Time) error {
	secret, confirmed, err := s.load(ctx, userID)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrTOTPNotEnrolled
	}

	if step, ok := validateTOTP(code, secret, now); ok {
		return s.consumirTimestep(ctx, userID, step)
	}

	// O consumo e o registro de auditoria vão na mesma transação: uso de
	// código de recuperação é sinal forte de perda de dispositivo OU de
	// comprometimento, e é a única pista que o dono da conta teria (R39). A
	// transação impede as duas metades soltas — código consumido sem rastro,
	// ou rastro de um consumo que não aconteceu.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	// DELETE ... RETURNING resolve o consumo do código em uma única
	// instrução: dois usos simultâneos do mesmo código não podem ambos
	// encontrar a linha.
	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1 AND code_hash = $2 RETURNING id`,
		userID, crypto.HashToken(code)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTOTPInvalid
	}
	if err != nil {
		return fmt.Errorf("consumir código de recuperação: %w", err)
	}

	if err := insertAudit(ctx, tx, userID, "totp.recovery_used", nil, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("consumir código de recuperação: %w", err)
	}
	return nil
}

// Disable desliga o segundo fator e apaga tudo que o sustentava.
//
// Existe porque o R38 fechou o único caminho de volta: recusar o recadastro
// impediu o bypass, mas deixou sem saída quem perdeu o telefone — e, pior,
// quem teve o fator LIGADO por um atacante com sessão roubada. A diferença
// entre esta rota e o bypass que o R38 fechou é a prova exigida: aqui é
// preciso posse do fator, e o chamador ainda exige a senha antes.
//
// Um código de recuperação serve no lugar do código do app, que é o caso que
// motiva a rota existir.
func (s *TOTP) Disable(ctx context.Context, userID uuid.UUID, code string, now time.Time) error {
	secret, confirmed, err := s.load(ctx, userID)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrTOTPNotEnrolled
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, ok := validateTOTP(code, secret, now); !ok {
		// O DELETE ... RETURNING é a própria verificação do código de
		// recuperação: não há janela entre conferir e consumir.
		var id uuid.UUID
		err := tx.QueryRow(ctx,
			`DELETE FROM totp_recovery_codes WHERE user_id = $1 AND code_hash = $2 RETURNING id`,
			userID, crypto.HashToken(code)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTOTPInvalid
		}
		if err != nil {
			return fmt.Errorf("consumir código de recuperação: %w", err)
		}
	}

	// A guarda vai na cláusula WHERE, e não num SELECT anterior, pelo mesmo
	// motivo do R38: entre ler e escrever cabe uma desativação concorrente.
	tag, err := tx.Exec(ctx,
		`UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL
		   WHERE id = $1 AND totp_confirmed_at IS NOT NULL`, userID)
	if err != nil {
		return fmt.Errorf("desativar TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTOTPNotEnrolled
	}

	// Os códigos restantes morrem com o fator: deixá-los vivos seria manter
	// credencial de segundo fator para um fator que não existe mais.
	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("limpar códigos de recuperação: %w", err)
	}

	// Desligar o segundo fator é exatamente o que um sequestro tentaria: o
	// evento é a pista que o dono da conta teria (mesma razão do R39).
	if err := insertAudit(ctx, tx, userID, "totp.disabled", nil, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("desativar TOTP: %w", err)
	}
	return nil
}

// consumirTimestep marca o período como usado e recusa se ele já foi.
//
// A condição vai na cláusula WHERE, e não num SELECT anterior, pelo mesmo
// motivo do R38: entre ler e escrever cabe outra verificação com o mesmo
// código, e é exatamente essa corrida que o reúso explora. Recusar `<=` e não
// só `=` fecha também o código do período ANTERIOR depois que um posterior
// entrou.
func (s *TOTP) consumirTimestep(ctx context.Context, userID uuid.UUID, step int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET totp_last_timestep = $2
		   WHERE id = $1 AND (totp_last_timestep IS NULL OR totp_last_timestep < $2)`,
		userID, step)
	if err != nil {
		return fmt.Errorf("consumir timestep TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTOTPInvalid
	}
	return nil
}

func (s *TOTP) IsEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, userID).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("consultar TOTP: %w", err)
	}
	return confirmed != nil, nil
}

func (s *TOTP) load(ctx context.Context, userID uuid.UUID) (secret string, confirmed bool, err error) {
	secret, confirmed, _, err = s.loadSealed(ctx, userID)
	return secret, confirmed, err
}

// loadSealed é load devolvendo também o segredo cifrado como está no banco,
// para quem precisa gravar só se ele não mudou (Confirm).
func (s *TOTP) loadSealed(ctx context.Context, userID uuid.UUID) (secret string, confirmed bool, sealed []byte, err error) {
	var at *time.Time

	err = s.pool.QueryRow(ctx,
		`SELECT totp_secret_enc, totp_confirmed_at FROM users WHERE id = $1`, userID).
		Scan(&sealed, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil, ErrNotFound
	}
	if err != nil {
		return "", false, nil, fmt.Errorf("carregar TOTP: %w", err)
	}
	if len(sealed) == 0 {
		return "", false, nil, ErrTOTPNotEnrolled
	}

	plain, err := s.box.Open(sealed, crypto.AAD("users", "totp_secret_enc", userID))
	if err != nil {
		return "", false, nil, fmt.Errorf("decifrar segredo TOTP: %w", err)
	}
	return string(plain.Reveal()), at != nil, sealed, nil
}

// validateTOTP devolve o timestep que casou, e não só um booleano. Saber QUAL
// período foi aceito é o que permite consumi-lo: com skew, o código pode ser
// do período anterior, do atual ou do seguinte, e marcar o período "de agora"
// deixaria o do anterior livre para outra rodada.
//
// Cada candidato é testado com skew ZERO, justamente para que o casamento
// identifique um período só.
func validateTOTP(code, secret string, now time.Time) (int64, bool) {
	for _, delta := range []int{-totpSkew, 0, totpSkew} {
		instante := now.Add(time.Duration(delta) * totpPeriod * time.Second)
		ok, err := totp.ValidateCustom(code, secret, instante, totp.ValidateOpts{
			Period:    totpPeriod,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return instante.Unix() / totpPeriod, true
		}
	}
	return 0, false
}
