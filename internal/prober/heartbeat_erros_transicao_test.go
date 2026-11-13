package prober_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Os outros passos da transação do avaliador, além de marcar e enfileirar
// (TestHeartbeatFalhaAoAvisarDesfazATransacao): inserir o incidente, marcar
// last_incident_at no heartbeat e resolver o incidente. A falha em qualquer
// um volta o Tick com o erro do passo, nada fica gravado pela metade, e o
// tique seguinte, sem a causa, faz a transição uma vez só.
func TestHeartbeatFalhaNaTransicaoDesfazATransacao(t *testing.T) {
	casos := []struct {
		nome    string
		fecha   bool
		criar   string
		apagar  string
		erroCom string
	}{
		{
			"abertura: inserir incidente", false,
			gatilhoRecusarHB("BEFORE INSERT ON incidents", "true"),
			`DROP TRIGGER recusar_passo ON incidents`, "abrir incidente de heartbeat",
		},
		{
			"abertura: marcar o heartbeat", false,
			gatilhoRecusarHB("BEFORE UPDATE ON heartbeats", "OLD.last_incident_at IS DISTINCT FROM NEW.last_incident_at"),
			`DROP TRIGGER recusar_passo ON heartbeats`, "marcar abertura no heartbeat",
		},
		{
			"fechamento: resolver incidente", true,
			gatilhoRecusarHB("BEFORE UPDATE ON incidents", "OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL"),
			`DROP TRIGGER recusar_passo ON incidents`, "resolver incidente de heartbeat",
		},
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
			if n := b.tick(t); n != 0 {
				t.Errorf("tique seguinte: %d transições, queria 0 (a transição só acontece uma vez)", n)
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

// gatilhoRecusarHB recusa a operação dada (momento e tabela) quando a
// condição vale. Os dois argumentos vêm de constantes do próprio teste.
func gatilhoRecusarHB(quando, condicao string) string {
	return `
	CREATE FUNCTION recusar_passo() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'passo recusado pelo teste';
	END $$;
	CREATE TRIGGER recusar_passo ` + quando + `
		FOR EACH ROW WHEN (` + condicao + `)
		EXECUTE FUNCTION recusar_passo();`
}
