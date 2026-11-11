package prober_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// Characterization tests de "marcar o incidente como avisado e enfileirar o
// aviso" no caminho dos heartbeats (R3.6): gravam o comportamento de ANTES
// da extração e têm de continuar verdes depois dela.

func (b *bancadaHB) comoSondador(t *testing.T, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaHB) marcas(t *testing.T, incidente uuid.UUID) (abertura, fechamento *time.Time) {
	t.Helper()
	b.comoSondador(t, func(tx pgx.Tx) {
		err := tx.QueryRow(context.Background(),
			`SELECT open_notified_at, close_notified_at FROM incidents WHERE id = $1`, incidente).Scan(&abertura, &fechamento)
		if err != nil {
			t.Fatal(err)
		}
	})
	return abertura, fechamento
}

// atrasar vence o prazo e abre o incidente do heartbeat.
func (b *bancadaHB) atrasar(t *testing.T) {
	t.Helper()
	b.clk.Advance(2 * time.Hour)
	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições na abertura, queria 1", n)
	}
}

// recuperar manda o sucesso e deixa o tique seguinte fechar o incidente.
func (b *bancadaHB) recuperar(t *testing.T, token string) {
	t.Helper()
	b.clk.Advance(10 * time.Minute)
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Second)
}

func TestHeartbeatMarcaEEnfileiraAAbertura(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.canal(t)

	b.atrasar(t)

	l := b.incidentes(t, id)
	if len(l) != 1 || len(l[0].Avisos) != 1 || l[0].Avisos[0] != "opened:backup" {
		t.Fatalf("incidentes: %+v; queria um, com a abertura avisada", l)
	}
	abertura, fechamento := b.marcas(t, l[0].ID)
	if abertura == nil || !abertura.Equal(b.clk.Now()) || fechamento != nil {
		t.Errorf("marcas = %v, %v; queria abertura em %v e fechamento nulo", abertura, fechamento, b.clk.Now())
	}
}

func TestHeartbeatMarcaEEnfileiraOFechamento(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.canal(t)
	b.atrasar(t)
	b.recuperar(t, token)

	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria o fechamento", n)
	}

	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Fim == nil || len(l[0].Avisos) != 2 || l[0].Avisos[1] != "resolved:backup" {
		t.Fatalf("incidentes: %+v; queria um, resolvido, com abertura e fechamento avisados", l)
	}
	if _, fechamento := b.marcas(t, l[0].ID); fechamento == nil || !fechamento.Equal(b.clk.Now()) {
		t.Errorf("close_notified_at = %v; queria %v", fechamento, b.clk.Now())
	}
}

// n == 0: o fechamento já avisado resolve o incidente sem enfileirar de novo.
func TestHeartbeatFechamentoJaAvisadoNaoEnfileira(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.canal(t)
	b.atrasar(t)
	incidente := b.incidentes(t, id)[0].ID
	avisadoAntes := inicioHB.Add(-time.Hour)
	b.comoSondador(t, func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(),
			`UPDATE incidents SET close_notified_at = $2 WHERE id = $1`, incidente, avisadoAntes); err != nil {
			t.Fatal(err)
		}
	})
	b.recuperar(t, token)

	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria o fechamento", n)
	}

	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Fim == nil {
		t.Fatalf("incidentes: %+v; queria um, resolvido", l)
	}
	if len(l[0].Avisos) != 1 || l[0].Avisos[0] != "opened:backup" {
		t.Errorf("avisos: %v; queria só a abertura", l[0].Avisos)
	}
	if _, fechamento := b.marcas(t, incidente); fechamento == nil || !fechamento.Equal(avisadoAntes) {
		t.Errorf("close_notified_at = %v; queria a marca anterior %v intacta", fechamento, avisadoAntes)
	}
}

// Falha no marcar ou no enfileirar: o Tick devolve o erro, a transação toda
// volta (inclusive a reivindicação do heartbeat) e, tirada a causa, o tique
// seguinte refaz a transição.
func TestHeartbeatFalhaAoAvisarDesfazATransacao(t *testing.T) {
	casos := []struct {
		nome    string
		fecha   bool
		criar   string
		apagar  string
		erroCom string
	}{
		{"abertura: marcar", false, gatilhoMarcarHB("open_notified_at"), `DROP TRIGGER recusar_marca ON incidents`, "marcar abertura notificada"},
		{"abertura: enfileirar", false, gatilhoEnfileirarHB, `DROP TRIGGER recusar_aviso ON notifications`, "enfileirar notificação"},
		{"fechamento: marcar", true, gatilhoMarcarHB("close_notified_at"), `DROP TRIGGER recusar_marca ON incidents`, "marcar fechamento notificado"},
		{"fechamento: enfileirar", true, gatilhoEnfileirarHB, `DROP TRIGGER recusar_aviso ON notifications`, "enfileirar notificação"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancadaHB(t)
			id, token := b.heartbeat(t, "backup")
			b.canal(t)
			if c.fecha {
				b.atrasar(t)
				b.recuperar(t, token)
			} else {
				b.clk.Advance(2 * time.Hour)
			}
			if _, err := b.pool.Exec(context.Background(), c.criar); err != nil {
				t.Fatal(err)
			}

			_, err := b.av.Tick(context.Background())
			if err == nil || !strings.Contains(err.Error(), c.erroCom) {
				t.Fatalf("Tick = %v; queria erro com %q", err, c.erroCom)
			}
			l := b.incidentes(t, id)
			if c.fecha {
				if len(l) != 1 || l[0].Fim != nil || len(l[0].Avisos) != 1 {
					t.Errorf("incidentes: %+v; queria o incidente ainda aberto, só com a abertura avisada", l)
				}
			} else if len(l) != 0 {
				t.Errorf("incidentes: %+v; queria nada gravado", l)
			}

			if _, err := b.pool.Exec(context.Background(), c.apagar); err != nil {
				t.Fatal(err)
			}
			if n := b.tick(t); n != 1 {
				t.Fatalf("depois de tirar a causa: %d transições, queria 1 (a reivindicação tem de ter voltado)", n)
			}
			l = b.incidentes(t, id)
			if len(l) != 1 || (l[0].Fim != nil) != c.fecha {
				t.Fatalf("incidentes: %+v", l)
			}
			if want := map[bool]int{false: 1, true: 2}[c.fecha]; len(l[0].Avisos) != want {
				t.Errorf("avisos: %v; queria %d", l[0].Avisos, want)
			}
		})
	}
}

const gatilhoEnfileirarHB = `
	CREATE FUNCTION recusar_aviso() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'aviso recusado pelo teste';
	END $$;
	CREATE TRIGGER recusar_aviso BEFORE INSERT ON notifications
		FOR EACH ROW EXECUTE FUNCTION recusar_aviso();`

// gatilhoMarcarHB recusa o UPDATE que preenche a coluna de marca dada. A
// coluna vem de uma constante do próprio teste.
func gatilhoMarcarHB(coluna string) string {
	return `
	CREATE FUNCTION recusar_marca() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'marca recusada pelo teste';
	END $$;
	CREATE TRIGGER recusar_marca BEFORE UPDATE ON incidents
		FOR EACH ROW WHEN (OLD.` + coluna + ` IS NULL AND NEW.` + coluna + ` IS NOT NULL)
		EXECUTE FUNCTION recusar_marca();`
}
