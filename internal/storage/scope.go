package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Beginner é o mínimo que uma transação com escopo precisa. *pgxpool.Pool e
// *pgxpool.Conn satisfazem; aceitar a conexão avulsa é o que permite provar
// o comportamento numa conexão REUSADA (TestScopeSurvivesConnectionReuse).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var (
	ErrNoUser    = errors.New("transação de usuário sem usuário")
	ErrNoProject = errors.New("transação de projeto sem projeto")
	ErrNoToken   = errors.New("transação sem hash de token")
	ErrNoSlug    = errors.New("transação pública sem slug")
)

// InUserTx roda fn numa transação em que a RLS só enxerga as linhas de userID.
//
// set_config(..., true) é o SET LOCAL: o valor morre no fim da transação e
// não vaza para o próximo uso da conexão no pool. Ver a migration 00004 para
// o motivo do NULLIF nas políticas.
func InUserTx(ctx context.Context, b Beginner, userID uuid.UUID, fn func(*db.Queries) error) error {
	if userID == uuid.Nil {
		// uuid.Nil nunca é usuário. Aceitá-lo produziria uma transação que
		// não enxerga nada, e o erro apareceria longe daqui, como "não
		// encontrado".
		return ErrNoUser
	}
	return inTx(ctx, b, `SELECT set_config('app.user_id', $1, true)`, userID.String(), fn)
}

// InProjectTx roda fn no escopo do projeto projectID, agindo como userID. Na
// MESMA transação, liga app.user_id, confere a pertença (membro com a conta
// ativa, titular com a conta ativa) e só então liga app.project_id: com
// InProjectPgxTx, que ela embrulha, é o ÚNICO caminho do código que liga
// app.project_id, e nunca antes da conferência.
//
// Sem pertença, o erro é tenancy.ErrProjectNotFound, o mesmo de um projeto
// inexistente, para a API não revelar que o projeto existe; fn não roda. fn
// recebe o escopo lido aqui: a permissão vale pelo papel desta transação,
// nunca pelo de quem chamou.
func InProjectTx(ctx context.Context, b Beginner, userID, projectID uuid.UUID,
	fn func(*db.Queries, tenancy.ProjectScope) error) error {
	return InProjectPgxTx(ctx, b, userID, projectID, func(tx pgx.Tx, escopo tenancy.ProjectScope) error {
		return fn(db.New(tx), escopo)
	})
}

// InProjectPgxTx é InProjectTx com o pgx.Tx cru, para o SQL sem query gerada
// (as sementes dos testes): a mesma conferência de pertença, o mesmo erro e o
// mesmo escopo. É aqui que app.project_id é ligado.
func InProjectPgxTx(ctx context.Context, b Beginner, userID, projectID uuid.UUID,
	fn func(pgx.Tx, tenancy.ProjectScope) error) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	if projectID == uuid.Nil {
		return ErrNoProject
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	q := db.New(tx)
	if err := q.ScopeToUser(ctx, userID); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	texto, err := q.ActiveMembershipRole(ctx, db.ActiveMembershipRoleParams{ProjectID: projectID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.ErrProjectNotFound
	}
	if err != nil {
		return fmt.Errorf("conferir a pertença ao projeto: %w", err)
	}
	papel, err := tenancy.ParseRole(texto)
	if err != nil {
		// O CHECK da tabela só aceita papéis conhecidos: chegar aqui é
		// defeito, não pedido inválido.
		return fmt.Errorf("papel gravado em project_members: %w", err)
	}
	if err := q.ScopeToProject(ctx, projectID); err != nil {
		return fmt.Errorf("definir escopo do projeto: %w", err)
	}
	if err := fn(tx, tenancy.ProjectScope{ProjectID: projectID, UserID: userID, Role: papel}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InProberTx roda fn com o escopo do sondador: lê e reagenda alvos de todos
// os usuários e grava checks, mas não cria nem apaga alvos (políticas
// targets_prober_*). É o ÚNICO lugar do código que liga esse escopo.
func InProberTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "prober", fn)
}

// InIPRateLimitTx roda fn no escopo do limite por IP (login e /totp/verify):
// a RLS abre só ip_rate_limits, a tabela sem dono que o limite lê e grava
// antes de existir usuário, e nada de outra tabela. É o único lugar do código
// que liga esse escopo.
func InIPRateLimitTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "ip_rate_limit", fn)
}

// InVerifyTx roda fn no escopo de verificação de canal (GET/POST
// /verify/{token}): a RLS enxerga só a linha de notif_channels cujo
// verify_token_hash é tokenHash, e nada de outra tabela. É o único lugar do
// código que liga esse escopo.
//
// O hash vai em hexadecimal porque GUC é texto; a política o decodifica.
func InVerifyTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error {
	if len(tokenHash) != sha256.Size {
		// Um hash vazio casaria com nada, e o erro apareceria longe daqui
		// como "link inválido". Um tamanho errado é defeito de quem chama.
		return ErrNoToken
	}
	return inTx(ctx, b, `SELECT set_config('app.verify_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
}

// InAccountTokenTx roda fn no escopo do link de conta (confirmação de e-mail
// e redefinição de senha). Em account_tokens, o escopo do hash permite ler e
// atualizar a linha correspondente. Inserções e remoções exigem o escopo do
// dono (app.user_id). Quem clica no link não tem sessão. É o único lugar do
// código que liga app.account_token_hash.
func InAccountTokenTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error {
	if len(tokenHash) != sha256.Size {
		return ErrNoToken
	}
	return inTx(ctx, b, `SELECT set_config('app.account_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
}

// InAccountEmailTx roda fn no escopo do teto diário de e-mails de conta: a
// RLS abre só account_email_daily, a contagem sem dono que todas as réplicas
// dividem. É o único lugar do código que liga esse escopo.
func InAccountEmailTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "account_email", fn)
}

// InPingTx roda fn no escopo do ping público (POST /ping/{token}): a RLS
// enxerga só a linha de heartbeats cujo ping_token_hash é tokenHash e os
// eventos dela. Incidentes ficam de fora: quem os abre é o avaliador. É o
// único lugar do código que liga esse escopo.
func InPingTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error {
	if len(tokenHash) != sha256.Size {
		return ErrNoToken
	}
	return inTx(ctx, b, `SELECT set_config('app.ping_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
}

// InPublicTx roda fn no escopo público da status page (GET /p/{slug}): a
// RLS enxerga a página publicada daquele slug, os itens dela e, por
// delegação, os alvos, checks, rollups e incidentes desses itens. Nada mais.
//
// A transação é READ ONLY. Desde as políticas por comando (migrations 00034
// e 00035), nenhuma política dá escrita ao escopo público: as de checks,
// incidents, target_health e check_rollup_1h só deixam ler quem enxerga o
// alvo e gravar o sondador ou o dono nomeado. O READ ONLY fica como segunda
// barreira, para uma política futura que delegue a targets sem nomear o
// escopo: o Postgres recusa qualquer escrita com 25006, seja qual for a
// política.
func InPublicTx(ctx context.Context, b Beginner, slug string, fn func(*db.Queries) error) error {
	if slug == "" {
		return ErrNoSlug
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	// Tem de ser o primeiro comando da transação.
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		return fmt.Errorf("transação somente leitura: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', $1, true)`, slug); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func inTx(ctx context.Context, b Beginner, setSQL, valor string, fn func(*db.Queries) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, err := tx.Exec(ctx, setSQL, valor); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
