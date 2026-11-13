package heartbeat_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// O ping grava o estado da linha, o evento e a apara do log numa transação só.
// Se qualquer passo falha, o ping devolve o erro do passo e nada fica: a linha
// do heartbeat segue igual (prazo, last_*, sinal) e o log não ganha evento. Um
// prazo adiado sem o evento que o justifica esconderia um atraso. O ping
// seguinte, sem a causa, grava normalmente.
func TestPingFalhaNoMeioDesfazATransacao(t *testing.T) {
	casos := []struct {
		nome    string
		comRid  bool
		criar   string
		apagar  string
		erroCom string
	}{
		{
			nome:    "gravar o estado",
			criar:   gatilhoRecusarPing("BEFORE UPDATE ON heartbeats FOR EACH ROW"),
			apagar:  `DROP TRIGGER recusar_ping ON heartbeats; DROP FUNCTION recusar_ping();`,
			erroCom: "gravar ping",
		},
		{
			nome:    "gravar o evento",
			criar:   gatilhoRecusarPing("BEFORE INSERT ON heartbeat_events FOR EACH ROW"),
			apagar:  `DROP TRIGGER recusar_ping ON heartbeat_events; DROP FUNCTION recusar_ping();`,
			erroCom: "gravar evento",
		},
		{
			// Por comando, e não por linha: a apara só apaga linhas acima do
			// teto, e o gatilho tem de disparar com o log curto do teste.
			nome:    "aparar o log",
			criar:   gatilhoRecusarPing("BEFORE DELETE ON heartbeat_events FOR EACH STATEMENT"),
			apagar:  `DROP TRIGGER recusar_ping ON heartbeat_events; DROP FUNCTION recusar_ping();`,
			erroCom: "aparar eventos",
		},
		{
			// A leitura do início de mesmo rid é um SELECT: falha pela política
			// RESTRICTIVE que reconhece só essa consulta.
			nome:    "ler o início da execução",
			comRid:  true,
			criar:   politicaRecusarConsulta("heartbeat_events", "GetRunStartAt"),
			apagar:  `DROP POLICY recusar_consulta ON heartbeat_events; DROP FUNCTION recusar_consulta();`,
			erroCom: "ler início da execução",
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			id, token := a.novoHeartbeat(t)
			var rid *uuid.UUID
			if c.comRid {
				r := uuid.Must(uuid.NewV7())
				rid = &r
			}
			ctx := context.Background()
			if err := a.svc.Ping(ctx, token, heartbeat.SignalStart, rid); err != nil {
				t.Fatalf("start: %v", err)
			}
			a.clk.Advance(2 * time.Minute)
			antes, eventosAntes := a.linha(t, id), len(a.eventos(t, id))

			if _, err := a.pool.Exec(ctx, c.criar); err != nil {
				t.Fatal(err)
			}
			err := a.svc.Ping(ctx, token, heartbeat.SignalSuccess, rid)
			if err == nil || errors.Is(err, heartbeat.ErrUnknownToken) || !strings.Contains(err.Error(), c.erroCom) {
				t.Fatalf("ping = %v; queria erro com %q", err, c.erroCom)
			}
			if depois := a.linha(t, id); !reflect.DeepEqual(depois, antes) {
				t.Errorf("a linha mudou com o ping recusado:\nantes  %+v\ndepois %+v", antes, depois)
			}
			if n := len(a.eventos(t, id)); n != eventosAntes {
				t.Errorf("%d eventos depois do ping recusado, queria %d", n, eventosAntes)
			}

			if _, err := a.pool.Exec(ctx, c.apagar); err != nil {
				t.Fatal(err)
			}
			a.conferirPingSeguinte(t, id, token, rid, eventosAntes)
		})
	}
}

// Uma linha cujo agendamento este binário não sabe ler (período não positivo,
// cron sem fuso) recusa o ping antes de qualquer escrita: sem agendamento não
// há prazo para calcular. Os CHECKs de hoje impedem gravar essas linhas, então
// o teste os tira (o testdb restaura o snapshot depois de cada teste).
func TestPingComAgendamentoIlegivelNaoGravaNada(t *testing.T) {
	casos := []struct {
		nome     string
		estragar string
	}{
		{"período zero", `UPDATE heartbeats SET period_sec = 0 WHERE id = $1`},
		{"período negativo", `UPDATE heartbeats SET period_sec = -60 WHERE id = $1`},
		{"cron sem fuso", `UPDATE heartbeats SET period_sec = NULL, cron_expr = '0 3 * * *', timezone = NULL WHERE id = $1`},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			semChecksDeAgendamento(t, a.pool)
			id, token := a.novoHeartbeat(t)
			a.comoDono(t, c.estragar, id)
			a.clk.Advance(2 * time.Minute)
			antes := a.linha(t, id)

			err := a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, nil)
			if err == nil || errors.Is(err, heartbeat.ErrUnknownToken) || !strings.Contains(err.Error(), "agendamento do heartbeat") {
				t.Fatalf("ping = %v; queria erro de agendamento", err)
			}
			if depois := a.linha(t, id); !reflect.DeepEqual(depois, antes) {
				t.Errorf("a linha mudou com o ping recusado:\nantes  %+v\ndepois %+v", antes, depois)
			}
			if n := len(a.eventos(t, id)); n != 0 {
				t.Errorf("%d eventos depois do ping recusado, queria 0", n)
			}

			a.comoDono(t, `UPDATE heartbeats SET period_sec = 3600, cron_expr = NULL, timezone = NULL WHERE id = $1`, id)
			a.conferirPingSeguinte(t, id, token, nil, 0)
		})
	}
}

// conferirPingSeguinte manda um sucesso sem a causa da falha e confere que ele
// grava: o prazo anda um período mais a tolerância a partir de agora, e o log
// ganha o evento de sucesso.
func (a ambiente) conferirPingSeguinte(t *testing.T, id uuid.UUID, token string, rid *uuid.UUID, eventosAntes int) {
	t.Helper()
	if err := a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, rid); err != nil {
		t.Fatalf("ping seguinte: %v", err)
	}
	h := a.linha(t, id)
	agoraFake := a.clk.Now()
	if want := agoraFake.Add(time.Hour + 5*time.Minute); !h.DeadlineAt.Equal(want) {
		t.Errorf("prazo depois do ping seguinte %v, queria %v", h.DeadlineAt, want)
	}
	if h.LastSuccessAt == nil || !h.LastSuccessAt.Equal(agoraFake) || h.LastSignal == nil || *h.LastSignal != "success" {
		t.Errorf("linha depois do ping seguinte: %+v", h)
	}
	ev := a.eventos(t, id)
	if len(ev) != eventosAntes+1 || ev[0].Kind != "success" || !ev[0].At.Equal(agoraFake) {
		t.Errorf("eventos depois do ping seguinte: %+v; queria %d com um sucesso no topo", ev, eventosAntes+1)
	}
}

// gatilhoRecusarPing cria um gatilho que recusa o comando em quando. O
// argumento vem de constantes do próprio teste.
func gatilhoRecusarPing(quando string) string {
	return `
	CREATE FUNCTION recusar_ping() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'passo recusado pelo teste';
	END $$;
	CREATE TRIGGER recusar_ping ` + quando + `
		EXECUTE FUNCTION recusar_ping();`
}

// politicaRecusarConsulta faz falhar só a consulta sqlc de nome dado, por uma
// política RESTRICTIVE de SELECT que reconhece o comentário "-- name:" no texto
// do comando. As outras consultas da tabela passam. Tabela e nome vêm de
// constantes do próprio teste.
func politicaRecusarConsulta(tabela, consulta string) string {
	return `
	CREATE FUNCTION recusar_consulta() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
	BEGIN
		IF current_query() LIKE '%-- name: ` + consulta + ` :%' THEN
			RAISE EXCEPTION 'consulta recusada pelo teste';
		END IF;
		RETURN true;
	END $$;
	CREATE POLICY recusar_consulta ON ` + tabela + ` AS RESTRICTIVE FOR SELECT
		USING (recusar_consulta());`
}
