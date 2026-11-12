package testdb

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/jackc/pgx/v5"
)

// conexoesDeAdminOciosas conta os backends de superusuário parados no banco
// "postgres", que é onde o Restore se conecta. A conexão da contagem fica de
// fora pelo pg_backend_pid.
func conexoesDeAdminOciosas(ctx context.Context, t *testing.T) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	defer conn.Close(ctx)

	var n int
	err = conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
		  WHERE datname = 'postgres' AND usename = $1
		    AND state = 'idle' AND pid <> pg_backend_pid()`, adminUser).Scan(&n)
	if err != nil {
		t.Fatalf("contar conexões de admin: %v", err)
	}
	return n
}

// Regressão da #329: o Restore do módulo postgres do testcontainers abre uma
// *sql.Conn que nunca fecha, e a conexão só some quando o GC finaliza o socket.
// Sem GC, cada restauração deixava um backend de superusuário vivo, e o
// pacote targets esgotava o max_connections (53300 para o papel da aplicação).
func TestRestaurarNaoVazaConexaoDeAdmin(t *testing.T) {
	iniciar(t)
	ctx := t.Context()

	anterior := debug.SetGCPercent(-1)
	t.Cleanup(func() { debug.SetGCPercent(anterior) })

	antes := conexoesDeAdminOciosas(ctx, t)
	const restauracoes = 3
	for range restauracoes {
		if err := restaurar(ctx); err != nil {
			t.Fatalf("restaurar snapshot: %v", err)
		}
	}
	if vazadas := conexoesDeAdminOciosas(ctx, t) - antes; vazadas != 0 {
		t.Fatalf("%d restaurações deixaram %d conexões de admin abertas; esperado 0", restauracoes, vazadas)
	}
}
