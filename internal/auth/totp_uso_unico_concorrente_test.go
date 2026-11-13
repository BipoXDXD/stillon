package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// verificacoesConcorrentes cabe no pool de 10 conexões, com a trava e a
// consulta de espera.
const verificacoesConcorrentes = 8

// disputarCodigo segura as linhas do usuário que travar seleciona, dispara as
// verificações ao mesmo tempo, espera todas pararem nessa trava e só então
// solta: a corrida acontece sempre, e não só quando o agendador ajuda. Devolve
// quantas foram aceitas e quantas recusadas com ErrTOTPInvalid.
func disputarCodigo(t *testing.T, pool *pgxpool.Pool, travar string, userID uuid.UUID, verificar func() error) (aceitas, recusadas int) {
	t.Helper()
	return disputarOperacao(t, pool, travar, userID, auth.ErrTOTPInvalid, verificar)
}

// disputarOperacao é disputarCodigo para qualquer operação: quem perde a
// corrida devolve o erro recusa, e qualquer outro erro falha o teste.
func disputarOperacao(t *testing.T, pool *pgxpool.Pool, travar string, userID uuid.UUID, recusa error, operar func() error) (aceitas, recusadas int) {
	t.Helper()
	// Sem conexão para a trava e a espera, as verificações ficariam na fila do
	// pool e o teste só pararia no -timeout.
	if limite := pool.Config().MaxConns; limite < verificacoesConcorrentes+2 {
		t.Fatalf("o pool tem %d conexões; a disputa precisa de %d (%d verificações, a trava e a espera)",
			limite, verificacoesConcorrentes+2, verificacoesConcorrentes)
	}
	ctx := context.Background()
	trava, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var umaVez sync.Once
	soltar := func() { umaVez.Do(func() { _ = trava.Rollback(ctx) }) }
	t.Cleanup(soltar)
	if _, err := trava.Exec(ctx, travar, userID); err != nil {
		t.Fatal(err)
	}

	resultados := make(chan error, verificacoesConcorrentes)
	for range verificacoesConcorrentes {
		go func() { resultados <- operar() }()
	}
	chegaram := testdb.EsperarBloqueados(t, pool, verificacoesConcorrentes, resultados)
	soltar()
	for range verificacoesConcorrentes {
		switch err := <-resultados; {
		case err == nil:
			aceitas++
		case errors.Is(err, recusa):
			recusadas++
		default:
			t.Errorf("operação concorrente: %v, queria nil ou %v", err, recusa)
		}
	}
	if !chegaram {
		t.Fatalf("as %d verificações não pararam juntas na trava (%d aceitas, %d recusadas)",
			verificacoesConcorrentes, aceitas, recusadas)
	}
	return aceitas, recusadas
}

// Um código capturado do app só serve se o dono não tiver usado o mesmo
// período: verificações simultâneas não podem todas enxergar o período livre.
func TestConcurrentVerificationsAcceptTheAppCodeOnce(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	codigo := codigoEm(t, segredoDeTroca, inicioDaTroca)

	aceitas, recusadas := disputarCodigo(t, pool, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID, func() error {
		return svc.Verify(context.Background(), userID, codigo, inicioDaTroca)
	})

	if aceitas != 1 || recusadas != 7 {
		t.Errorf("%d aceitas e %d recusadas, queria 1 e 7", aceitas, recusadas)
	}
	var passo int64
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_last_timestep FROM users WHERE id = $1`, userID).Scan(&passo); err != nil {
		t.Fatal(err)
	}
	if passo != 59669280 {
		t.Errorf("totp_last_timestep = %d, queria 59669280, o período do código aceito", passo)
	}
}

// O código de recuperação não se renova e cada uso é a pista que o dono teria
// de um comprometimento: um gasto, um evento.
func TestConcurrentVerificationsSpendTheRecoveryCodeOnce(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	_, codigos := ligarComCodigos(t, svc, userID, inicioDaTroca)

	aceitas, recusadas := disputarCodigo(t, pool, `SELECT 1 FROM totp_recovery_codes WHERE user_id = $1 FOR UPDATE`, userID, func() error {
		return svc.Verify(context.Background(), userID, codigos[0], inicioDaTroca)
	})

	if aceitas != 1 || recusadas != 7 {
		t.Errorf("%d aceitas e %d recusadas, queria 1 e 7", aceitas, recusadas)
	}
	if n := quantosCodigos(t, pool, userID); n != 9 {
		t.Errorf("%d códigos restantes, queria 9", n)
	}
	if n := eventosDeAuditoria(t, pool, userID, "totp.recovery_used"); n != 1 {
		t.Errorf("%d eventos totp.recovery_used, queria 1", n)
	}
}
