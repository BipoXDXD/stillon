package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// A troca de autenticador com segredo pendente. Antes dela,
// trocar de celular era desativar e ativar de novo, e no meio a conta ficava
// sem 2FA — quem desistisse no meio ficava desprotegido sem perceber.
//
// StartRotation grava o segredo do app novo em totp_pending_secret_enc, e o
// antigo continua valendo; ConfirmRotation, com um código do app novo, promove
// o pendente numa transação só. O pendente vale PendingRotationTTL (10 min) a
// partir de totp_pending_started_at, e é da família de sessão que o criou
// (totp_pending_family_id): só ela o confirma, e ele morre com ela —
// revogada a família (DELETE /sessions/{id}, logout, troca de senha),
// nenhuma outra sessão viva o confirma. Pendente e família caem juntos na confirmação, no Disable, no
// reset-2fa do CLI, na troca de senha e no set-password, e um novo
// StartRotation substitui os dois.

// PendingRotationTTL é o prazo da troca pendente, contado do StartRotation. Passado
// o prazo, a confirmação é recusada como "sem troca pendente" (ErrTOTPNoRotation)
// e nada é gravado; o fator ativo não muda. Sem prazo, a troca valia enquanto a
// família da sessão vivesse (RefreshTTL, 30 dias), e uma sessão roubada a
// confirmaria muito depois.
const PendingRotationTTL = 10 * time.Minute

// StartRotation inicia a troca. O chamador já provou a senha; aqui se prova a
// posse do fator ATUAL, pelo código do app ou por um código de recuperação — as
// mesmas provas do Disable. Sem a segunda prova, uma sessão roubada somada à
// senha entregaria o segundo fator ao atacante, o bypass que o Enroll já fecha.
//
// Tudo numa transação, na ordem global de travas: a trava de
// sessões do usuário, a família do token reconferida, a linha
// de users (FOR NO KEY UPDATE), os códigos de recuperação, a auditoria. A
// prova só é conferida e gasta DEPOIS da família e do 2FA ligado, sob as
// travas: antes, ela era gasta primeiro, e uma família já caída ou um 2FA
// desligado no meio devolviam 401 ou 403 com o código de recuperação já
// gasto. O timestep do código do app fica consumido (o 2FA continua ligado
// depois deste passo, e o código observado não pode entrar depois), e o código
// de recuperação fica gasto com o rastro totp.recovery_used, mesmo que a troca
// nunca seja confirmada.
//
// Pendente, família e o instante do início (totp_pending_started_at, o `now` do
// chamador) são gravados juntos, com a auditoria totp.rotation_started (a pista do dono de que uma troca foi iniciada). O
// bloqueio da conta zera junto: a senha e a posse do fator acabaram de ser
// provadas.
//
// Devolve a otpauth_uri do segredo novo e o identificador da troca (rotation),
// que o rotate/confirm exige de volta. Devolve
// ErrInvalidSession se a família caiu, ErrTOTPNotEnrolled com o 2FA desligado
// e ErrTOTPInvalid com a prova errada ou vazia — nessa ordem, e nos três casos
// nada foi gasto nem gravado.
func (s *TOTP) StartRotation(ctx context.Context, userID, familyID uuid.UUID, email, code string, now time.Time) (uri, rotation string, err error) {
	uri, sealed, err := s.novoSegredoPendente(userID, email)
	if err != nil {
		return "", "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	step, err := s.provarFatorAtual(ctx, tx, userID, familyID, code, now)
	if err != nil {
		return "", "", err
	}
	if err := gravarPendente(ctx, tx, userID, familyID, sealed, step, now); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("gravar segredo TOTP pendente: %w", err)
	}
	return uri, rotacaoDe(sealed), nil
}

// novoSegredoPendente gera o segredo do app novo e o devolve como otpauth_uri e
// cifrado com a AAD do pendente.
func (s *TOTP) novoSegredoPendente(userID uuid.UUID, email string) (uri string, sealed []byte, err error) {
	key, err := s.novaChave(email)
	if err != nil {
		return "", nil, err
	}
	sealed, err = s.box.Seal([]byte(key.Secret()), aadPendente(userID))
	if err != nil {
		return "", nil, fmt.Errorf("cifrar segredo TOTP pendente: %w", err)
	}
	return key.URL(), sealed, nil
}

// provarFatorAtual, na transação de StartRotation, trava e confere nesta ordem:
// a família viva (ErrInvalidSession), o 2FA ligado (ErrTOTPNotEnrolled) e a
// prova de posse (ErrTOTPInvalid). Só a última gasta alguma coisa, e o código de
// recuperação gasto deixa o rastro totp.recovery_used. Devolve o timestep
// consumido, ou nil quando a prova foi um código de recuperação.
func (s *TOTP) provarFatorAtual(ctx context.Context, tx pgx.Tx, userID, familyID uuid.UUID, code string, now time.Time) (*int64, error) {
	// Sob a trava, a família não cai no meio: ou a troca de senha já comitou
	// (e a família está morta aqui), ou ela espera esta transação e apaga o
	// pendente gravado.
	viva, err := travarFamiliaViva(ctx, db.New(tx), userID, familyID, now)
	if err != nil {
		return nil, err
	}
	if !viva {
		return nil, ErrInvalidSession
	}
	f, err := s.fatorTravado(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if !f.confirmado {
		return nil, ErrTOTPNotEnrolled
	}
	step, recuperacao, err := f.provar(ctx, tx, userID, code, now)
	if err != nil {
		return nil, err
	}
	if recuperacao {
		if err := insertAudit(ctx, tx, userID, "totp.recovery_used", nil, now); err != nil {
			return nil, err
		}
	}
	return step, nil
}

// gravarPendente grava o segredo pendente com a família que o iniciou, consome
// o timestep da prova (quando houve) e zera o bloqueio da conta, e deixa a
// auditoria totp.rotation_started.
func gravarPendente(ctx context.Context, tx pgx.Tx, userID, familyID uuid.UUID, sealed []byte, step *int64, now time.Time) error {
	if _, err := tx.Exec(ctx,
		`UPDATE users SET totp_pending_secret_enc = $2, totp_pending_family_id = $3,
		                  totp_pending_started_at = $5,
		                  totp_last_timestep = COALESCE($4, totp_last_timestep),
		                  failed_attempts = 0, locked_until = NULL
		   WHERE id = $1`,
		userID, sealed, familyID, step, now); err != nil {
		return fmt.Errorf("gravar segredo TOTP pendente: %w", err)
	}
	return insertAudit(ctx, tx, userID, "totp.rotation_started", nil, now)
}

// rotuloRotacao separa o hash do id de rotação de todo outro hash do selo.
const rotuloRotacao = "stillon.rotacao.v1"

// rotacaoDe é o identificador de uma troca: 16 bytes em hex
// derivados do segredo pendente CIFRADO, como está no banco. Não revela nada
// do segredo (é um hash do selo, que tem nonce aleatório), e muda a cada
// rotate: o rotate/confirm que traz o de uma troca já substituída na mesma
// família recebe 403 (a troca que ele conhece não existe mais) em vez de um
// 401 de código errado que contaria como prova errada.
func rotacaoDe(pendente []byte) string {
	h := sha256.New()
	h.Write([]byte(rotuloRotacao))
	h.Write(pendente)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// ConfirmRotation conclui a troca com um código do app NOVO e devolve os 10
// códigos de recuperação novos. Numa transação só: o pendente vira o segredo
// atual (decifrado e cifrado de novo com a AAD de totp_secret_enc), o timestep
// usado fica consumido, os códigos antigos morrem, as OUTRAS famílias de
// sessão caem e a atual (familyID) fica, e a auditoria totp.rotated.
//
// Encerrar as outras sessões pelo mesmo motivo da troca de senha: um celular
// trocado pode ter sido perdido ou roubado.
//
// Só a família que iniciou a troca a confirma (totp_pending_family_id): o
// pendente de outra família é, para esta, "nenhuma troca em andamento". O
// pendente vencido (now no instante do prazo ou depois dele) também, e sem
// gravar nada. E só
// a troca que o cliente conhece (rotation): um rotate que
// substituiu o pendente na mesma família faz o identificador não casar.
//
// Devolve ErrTOTPNoRotation sem pendente, com o pendente de outra família ou
// vencido (ou com o 2FA desligado), ou com outro rotation — sempre antes de olhar o
// código —, ErrTOTPInvalid com o código errado (o pendente fica) e
// ErrInvalidSession se a família caiu antes da trava.
func (s *TOTP) ConfirmRotation(ctx context.Context, userID, familyID uuid.UUID, rotation, code string, now time.Time) ([]string, error) {
	pendente, err := s.carregarPendenteDaTroca(ctx, userID, familyID, rotation, now)
	if err != nil {
		return nil, err
	}
	ativo, step, err := s.provarPendente(userID, pendente, code, now)
	if err != nil {
		return nil, err
	}
	codes, err := novosCodigosDeRecuperacao()
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit
	q := db.New(tx)

	// A trava de sessões do usuário ANTES de qualquer trava de linha, na mesma
	// ordem da troca de senha: trava, linha de users,
	// linhas de sessions. Sem ela, uma rotação de refresh em curso deixaria o
	// sucessor fora da revogação.
	//
	// A família pode ter caído entre a guarda da rota e a trava. Então nada
	// muda: o 200 sempre quer dizer "trocado, e a sessão atual continua viva".
	viva, err := travarFamiliaViva(ctx, q, userID, familyID, now)
	if err != nil {
		return nil, err
	}
	if !viva {
		return nil, ErrInvalidSession
	}
	if err := promoverPendente(ctx, tx, userID, familyID, promocao{pendente: pendente, ativo: ativo, step: step, codes: codes}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirmar troca de TOTP: %w", err)
	}
	return codes, nil
}

// carregarPendenteDaTroca lê o segredo pendente cifrado, como está no banco, e
// confere que ele é a troca que o cliente conhece (rotation) e da família que a
// iniciou. Tudo isso vem antes do código: o pendente de outra família, o
// vencido (PendingRotationTTL), a troca substituída e o 2FA desligado são, para quem chama, "nenhuma troca em
// andamento" (ErrTOTPNoRotation), sem contar falha no bloqueio.
func (s *TOTP) carregarPendenteDaTroca(ctx context.Context, userID, familyID uuid.UUID, rotation string, now time.Time) ([]byte, error) {
	var pendente []byte
	var dona *uuid.UUID
	var confirmado, iniciada *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT totp_pending_secret_enc, totp_pending_family_id, totp_confirmed_at, totp_pending_started_at
		   FROM users WHERE id = $1`, userID).
		Scan(&pendente, &dona, &confirmado, &iniciada)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("carregar troca de TOTP: %w", err)
	}
	if len(pendente) == 0 || confirmado == nil || dona == nil || *dona != familyID ||
		pendenteVencido(iniciada, now) ||
		!hmac.Equal([]byte(rotacaoDe(pendente)), []byte(rotation)) {
		return nil, ErrTOTPNoRotation
	}
	return pendente, nil
}

// pendenteVencido: o prazo vale até um instante ANTES de iniciada+PendingRotationTTL;
// no próprio prazo já venceu. Sem o instante (linha anterior à 00027 ou gravada
// pelo binário anterior), o pendente é tratado como vencido.
func pendenteVencido(iniciada *time.Time, now time.Time) bool {
	return iniciada == nil || !now.Before(iniciada.Add(PendingRotationTTL))
}

// provarPendente confere o código contra o segredo pendente (ErrTOTPInvalid se
// não casa) e devolve esse segredo recifrado com a AAD de totp_secret_enc, pronto
// para virar o ativo, e o timestep que o código usou.
func (s *TOTP) provarPendente(userID uuid.UUID, pendente []byte, code string, now time.Time) (ativo []byte, step int64, err error) {
	plain, err := s.box.Open(pendente, aadPendente(userID))
	if err != nil {
		return nil, 0, fmt.Errorf("decifrar segredo TOTP pendente: %w", err)
	}
	segredo := string(plain.Reveal())
	step, ok := validateTOTP(code, segredo, now)
	if !ok {
		return nil, 0, ErrTOTPInvalid
	}
	ativo, err = s.box.Seal([]byte(segredo), aadAtivo(userID))
	if err != nil {
		return nil, 0, fmt.Errorf("cifrar segredo TOTP: %w", err)
	}
	return ativo, step, nil
}

// promocao é o que ConfirmRotation promove: o segredo pendente lido (a chave do
// compare-and-swap), o mesmo segredo recifrado como ativo, o timestep do código
// e os códigos de recuperação novos.
type promocao struct {
	pendente []byte
	ativo    []byte
	step     int64
	codes    []string
}

// promoverPendente grava, na transação de ConfirmRotation e já sob a trava de
// sessões, o que a confirmação muda: o pendente vira o segredo ativo, os códigos
// de recuperação são trocados, as OUTRAS famílias de sessão caem e a auditoria
// totp.rotated registra o evento.
func promoverPendente(ctx context.Context, tx pgx.Tx, userID, familyID uuid.UUID, p promocao, now time.Time) error {
	// Compare-and-swap no pendente lido e na família: um rotate que o
	// substituiu, uma confirmação concorrente ou um Disable no meio fazem a
	// WHERE não casar, e o código validado com o segredo velho não promove
	// nada. O bloqueio da conta zera junto.
	tag, err := tx.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_pending_secret_enc = NULL, totp_pending_family_id = NULL,
		                  totp_last_timestep = $3, failed_attempts = 0, locked_until = NULL
		   WHERE id = $1 AND totp_confirmed_at IS NOT NULL AND totp_pending_secret_enc = $4
		     AND totp_pending_family_id = $5`,
		userID, p.ativo, p.step, p.pendente, familyID)
	if err != nil {
		return fmt.Errorf("promover segredo TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTOTPNoRotation
	}
	if err := substituirCodigos(ctx, tx, userID, p.codes); err != nil {
		return err
	}
	if _, err := db.New(tx).RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{
		UserID: userID, KeepFamilyID: familyID, Now: now,
	}); err != nil {
		return fmt.Errorf("revogar as outras sessões: %w", err)
	}
	// Trocar o segundo fator é o que um sequestro faria; o evento é a pista
	// do dono (a mesma razão de ativar o 2FA deixar rastro).
	return insertAudit(ctx, tx, userID, "totp.rotated", nil, now)
}

// travarFamiliaViva toma a trava de sessões do usuário e SÓ
// ENTÃO confere a família: conferir antes deixaria uma revogação concorrente
// passar entre a leitura e a escrita. Quem chama escolhe o erro de "família
// morta". É a mesma função de account, não compartilhada: exportá-la aqui
// obrigaria a API pública a receber *db.Queries.
func travarFamiliaViva(ctx context.Context, q *db.Queries, userID, familyID uuid.UUID, now time.Time) (bool, error) {
	if err := q.LockUserSessions(ctx, userID); err != nil {
		return false, fmt.Errorf("travar sessões: %w", err)
	}
	viva, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
	if err != nil {
		return false, fmt.Errorf("conferir família de sessão: %w", err)
	}
	return viva, nil
}
