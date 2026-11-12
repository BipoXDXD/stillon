package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Os caminhos de falha da camada que todo acesso ao banco atravessa: o erro de
// fn desfaz a transação e chega a quem chamou, o contexto cancelado não abre
// transação, e o que falha ao checar o papel do banco ou ao criar a partição
// não vira sucesso. Os testes de RLS cobrem o escopo; estes cobrem o que
// acontece quando algo dá errado dentro ou antes dele.

// escopo é uma das cinco transações com escopo, com o que o teste precisa
// saber sobre ela. escreve é false na pública, que é somente leitura.
type escopo struct {
	nome    string
	escreve bool
	roda    func(ctx context.Context, pool *pgxpool.Pool, fn func(*db.Queries) error) error
}

func todosOsEscopos() []escopo {
	dono, hash := uuid.Must(uuid.NewV7()), hashDe("escopo")
	return []escopo{
		{"InUserTx", true, func(ctx context.Context, p *pgxpool.Pool, fn func(*db.Queries) error) error {
			return storage.InUserTx(ctx, p, dono, fn)
		}},
		{"InProberTx", true, func(ctx context.Context, p *pgxpool.Pool, fn func(*db.Queries) error) error {
			return storage.InProberTx(ctx, p, fn)
		}},
		{"InVerifyTx", true, func(ctx context.Context, p *pgxpool.Pool, fn func(*db.Queries) error) error {
			return storage.InVerifyTx(ctx, p, hash, fn)
		}},
		{"InPingTx", true, func(ctx context.Context, p *pgxpool.Pool, fn func(*db.Queries) error) error {
			return storage.InPingTx(ctx, p, hash, fn)
		}},
		{"InPublicTx", false, func(ctx context.Context, p *pgxpool.Pool, fn func(*db.Queries) error) error {
			return storage.InPublicTx(ctx, p, "pagina", fn)
		}},
	}
}

// gravarAuditoria escreve em audit_log, que não tem RLS: qualquer escopo
// consegue, e a linha prova se a transação foi confirmada.
func gravarAuditoria(ctx context.Context, q *db.Queries, acao string) error {
	return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{ID: novoID(), Action: acao, At: agora})
}

func auditoriasDe(t *testing.T, pool *pgxpool.Pool, acao string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1`, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// O erro de fn volta como está, o que fn gravou some e a conexão volta ao
// pool: uma transação deixada aberta seguraria as travas e esgotaria o pool.
func TestScopedTxRollsBackAndPropagatesTheCallbackError(t *testing.T) {
	falhou := errors.New("falha da fn")
	for _, e := range todosOsEscopos() {
		t.Run(e.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()
			acao := "desfeita-" + e.nome

			err := e.roda(ctx, pool, func(q *db.Queries) error {
				if e.escreve {
					if err := gravarAuditoria(ctx, q, acao); err != nil {
						return err
					}
				}
				return falhou
			})
			if !errors.Is(err, falhou) {
				t.Errorf("erro = %v, queria o erro de fn", err)
			}
			if n := auditoriasDe(t, pool, acao); n != 0 {
				t.Errorf("a gravação de fn sobreviveu ao erro: %d linhas", n)
			}
			if n := pool.Stat().AcquiredConns(); n != 0 {
				t.Errorf("%d conexões ainda presas depois do erro de fn", n)
			}
		})
	}
}

// O contraponto do teste acima: sem erro, a gravação é confirmada. Sem ele, a
// gravação sumir poderia ser defeito do próprio teste.
func TestScopedTxCommitsWhenTheCallbackSucceeds(t *testing.T) {
	for _, e := range todosOsEscopos() {
		if !e.escreve {
			continue
		}
		t.Run(e.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()
			acao := "confirmada-" + e.nome

			err := e.roda(ctx, pool, func(q *db.Queries) error { return gravarAuditoria(ctx, q, acao) })
			if err != nil {
				t.Fatal(err)
			}
			if n := auditoriasDe(t, pool, acao); n != 1 {
				t.Errorf("%d linhas confirmadas, queria 1", n)
			}
		})
	}
}

// Contexto já cancelado: nem abre a transação nem chama fn, e o erro deixa ver
// a causa.
func TestScopedTxWithACancelledContextNeverRunsTheCallback(t *testing.T) {
	for _, e := range todosOsEscopos() {
		t.Run(e.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			chamou := false

			err := e.roda(ctx, pool, func(*db.Queries) error {
				chamou = true
				return nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Errorf("erro = %v, queria context.Canceled", err)
			}
			if chamou {
				t.Error("fn rodou com o contexto cancelado")
			}
			if n := pool.Stat().AcquiredConns(); n != 0 {
				t.Errorf("%d conexões presas", n)
			}
		})
	}
}

// O hash do token tem de ter exatamente 32 bytes (SHA-256): tanto o que sobra
// quanto o que falta é defeito de quem chama, e erra alto em vez de abrir um
// escopo que não casa com nada.
func TestTokenScopesAcceptOnlyAHashOfExactlyThirtyTwoBytes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	escopos := map[string]func(hash []byte, fn func(*db.Queries) error) error{
		"InVerifyTx": func(h []byte, fn func(*db.Queries) error) error { return storage.InVerifyTx(ctx, pool, h, fn) },
		"InPingTx":   func(h []byte, fn func(*db.Queries) error) error { return storage.InPingTx(ctx, pool, h, fn) },
	}
	for nome, roda := range escopos {
		for _, c := range []struct {
			tamanho int
			aceito  bool
		}{{0, false}, {31, false}, {32, true}, {33, false}} {
			chamou := false
			err := roda(make([]byte, c.tamanho), func(*db.Queries) error {
				chamou = true
				return nil
			})
			if c.aceito && (err != nil || !chamou) {
				t.Errorf("%s com %d bytes: erro %v, fn chamada %v; queria aceito", nome, c.tamanho, err, chamou)
			}
			if !c.aceito && (!errors.Is(err, storage.ErrNoToken) || chamou) {
				t.Errorf("%s com %d bytes: erro %v, fn chamada %v; queria ErrNoToken sem chamar fn", nome, c.tamanho, err, chamou)
			}
		}
	}
}

// O papel do banco que a aplicação usa é conferido no boot, e a conferência
// que não consegue ler o papel não pode passar: é erro, e não é o erro de
// "papel ignora a RLS" (esse é outro defeito, de outra causa).
func TestCheckRoleFailsClosedWhenItCannotReadTheRole(t *testing.T) {
	pool := testdb.New(t)
	pool.Close()

	err := storage.CheckRole(context.Background(), pool)
	if err == nil {
		t.Fatal("CheckRole passou sem conseguir ler o papel")
	}
	if errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("falha de leitura apareceu como %v", err)
	}
	if !strings.Contains(err.Error(), "consultar papel do banco") {
		t.Errorf("erro %q não diz que a consulta do papel falhou", err)
	}
}

func TestEnsureChecksPartitionsReportsTheFailingMonth(t *testing.T) {
	pool := testdb.New(t)
	pool.Close()

	err := storage.EnsureChecksPartitions(context.Background(), pool, time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("EnsureChecksPartitions passou com o pool fechado")
	}
	if !strings.Contains(err.Error(), "criar partição de checks 2026-12") {
		t.Errorf("erro %q não nomeia o mês que falhou", err)
	}
}

// Sem Docker: DSN malformado e banco inalcançável erram no boot. Um pool
// devolvido sem ping só mostraria o defeito na primeira requisição.
func TestNewPoolFailsAtBootWhenTheDatabaseIsUnusable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if pool, err := storage.NewPool(ctx, "postgres://u:p@127.0.0.1:porta-invalida/db"); err == nil {
		pool.Close()
		t.Error("NewPool aceitou um DSN malformado")
	} else if !strings.Contains(err.Error(), "DSN inválido") {
		t.Errorf("DSN malformado deu %q, queria a mensagem DSN inválido", err)
	}

	// A porta 1 recusa a conexão na hora, em qualquer máquina de teste.
	if pool, err := storage.NewPool(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=2"); err == nil {
		pool.Close()
		t.Error("NewPool devolveu um pool para um banco inalcançável")
	} else if !strings.Contains(err.Error(), "ping no banco") {
		t.Errorf("banco inalcançável deu %q, queria a mensagem ping no banco", err)
	}
}
