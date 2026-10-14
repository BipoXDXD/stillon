package storage_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O pool leva os prazos do lado do servidor (C2 da revisão). O
// statement_timeout é a rede de segurança de uma consulta cujo cliente sumiu
// (o cancelamento pelo contexto nem chega ao banco numa conexão meio-aberta);
// o idle_in_transaction_session_timeout derruba a transação aberta que
// ninguém mais vai fechar, com as travas que ela segura.
func TestPoolSetsServerSideTimeouts(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	casos := map[string]string{
		"statement_timeout":                   "30s",
		"idle_in_transaction_session_timeout": "1min",
	}
	for parametro, quer := range casos {
		var valor string
		if err := pool.QueryRow(ctx, `SELECT current_setting($1)`, parametro).Scan(&valor); err != nil {
			t.Fatal(err)
		}
		if valor != quer {
			t.Errorf("%s = %q, queria %q", parametro, valor, quer)
		}
	}
}

// registrosDoLog troca o logger padrão por um que entrega cada registro
// num canal, e devolve o canal.
func registrosDoLog(t *testing.T) <-chan slog.Record {
	t.Helper()
	c := make(chan slog.Record, 64)
	antigo := slog.Default()
	slog.SetDefault(slog.New(canalDeLog(c)))
	t.Cleanup(func() { slog.SetDefault(antigo) })
	return c
}

type canalDeLog chan<- slog.Record

func (canalDeLog) Enabled(context.Context, slog.Level) bool { return true }

// Handle não bloqueia: com o canal cheio, o registro se perde, e o teste
// que esperava por ele reprova pelo prazo.
func (c canalDeLog) Handle(_ context.Context, r slog.Record) error {
	select {
	case c <- r.Clone():
	default:
	}
	return nil
}
func (c canalDeLog) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c canalDeLog) WithGroup(string) slog.Handler      { return c }

// C2 da revisão: uma rodada de manutenção presa prendia o job para sempre,
// sem nenhuma linha no log. A rodada tem prazo; quando ele vence, o erro vai
// ao log e a próxima tenta de novo.
func TestMaintenanceRoundGivesUpAtTheDeadline(t *testing.T) {
	pool := testdb.New(t)
	registros := registrosDoLog(t)
	testdb.LockTable(t, pool, "check_rollup_1h")

	ctx, cancel := context.WithCancel(context.Background())
	feito := make(chan error, 1)
	// O intervalo curto limita o prazo da rodada, que nunca passa do
	// intervalo.
	go func() { feito <- storage.RunMaintenance(ctx, pool, clock.NewFake(agora), 300*time.Millisecond) }()
	defer func() {
		cancel()
		<-feito
	}()

	limite := time.After(10 * time.Second)
	for {
		select {
		case r := <-registros:
			if r.Message == "manutenção" && r.Level == slog.LevelError && strings.Contains(erroDo(r), "context deadline exceeded") {
				return
			}
		case <-limite:
			t.Fatal("a rodada presa não desistiu no prazo: nenhum erro de manutenção no log")
		}
	}
}

func erroDo(r slog.Record) string {
	var erro string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "erro" {
			erro = a.Value.String()
		}
		return true
	})
	return erro
}

// A consolidação e a retenção sobem o próprio statement_timeout com SET
// LOCAL, porque a primeira rodada varre semanas de linhas e passaria dos 30 s
// do pool. Uma trigger de comando em check_rollup_1h anota o prazo em vigor
// no INSERT da consolidação e no DELETE da retenção. O snapshot restaurado
// no fim do teste leva a tabela e as triggers embora.
func TestMaintenanceRaisesItsOwnStatementTimeout(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE prazo_visto (etapa text NOT NULL, valor text NOT NULL);
		CREATE FUNCTION anotar_prazo() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO prazo_visto VALUES (TG_OP, current_setting('statement_timeout'));
			RETURN NULL;
		END $$;
		CREATE TRIGGER anotar_prazo AFTER INSERT OR DELETE ON check_rollup_1h
			FOR EACH STATEMENT EXECUTE FUNCTION anotar_prazo();`); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.ConsolidateRollups(ctx, pool, agora); err != nil {
		t.Fatalf("ConsolidateRollups: %v", err)
	}
	if _, err := storage.ApplyRetention(ctx, pool, agora); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	vistos := map[string]string{}
	linhas, err := pool.Query(ctx, `SELECT etapa, valor FROM prazo_visto`)
	if err != nil {
		t.Fatal(err)
	}
	for linhas.Next() {
		var etapa, valor string
		if err := linhas.Scan(&etapa, &valor); err != nil {
			t.Fatal(err)
		}
		vistos[etapa] = valor
	}
	if err := linhas.Err(); err != nil {
		t.Fatal(err)
	}
	for _, etapa := range []string{"INSERT", "DELETE"} {
		if vistos[etapa] != "5min" {
			t.Errorf("statement_timeout no %s da manutenção = %q, queria \"5min\"", etapa, vistos[etapa])
		}
	}
}
