package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Prazos do lado do servidor, em toda conexão do pool (C2 da revisão; SQL 13).
// O cancelamento pelo contexto depende de o cliente alcançar o banco; estes
// valem mesmo quando ele não alcança.
const (
	// statementTimeout é a rede de segurança de uma consulta cujo cliente
	// sumiu ou que perdeu o prazo do contexto. Fica acima de todo prazo
	// legítimo do código: o da requisição da API (15 s) e os das rodadas do
	// sondador e do dispatcher. A manutenção, que varre semanas de linhas,
	// sobe o seu com SET LOCAL (maintenanceStatementTimeout). O pg_dump do
	// backup e as migrations não passam por este pool.
	statementTimeout = 30 * time.Second
	// idleInTransactionTimeout derruba a transação aberta que ninguém mais
	// vai fechar (processo travado, conexão meio-aberta), e com ela as
	// travas que ela segura. Nenhuma transação do código espera rede nem
	// usuário: os envios do dispatcher e as sondas ficam fora delas.
	idleInTransactionTimeout = time.Minute
)

// NewPool constrói o pool de conexões da aplicação.
//
// MaxConnLifetime existe para que conexões sejam recicladas periodicamente:
// sem isso, uma conexão pode ficar pendurada em um backend de Postgres que o
// provedor vai derrubar, e o erro aparece como falha intermitente.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN inválido: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = milissegundos(statementTimeout)
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = milissegundos(idleInTransactionTimeout)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("criar pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping no banco: %w", err)
	}
	return pool, nil
}

// milissegundos escreve d no formato que os parâmetros de tempo do Postgres
// aceitam.
func milissegundos(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}
