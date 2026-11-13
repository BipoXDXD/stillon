package heartbeat_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Limpar o log e auditar a limpeza são um passo só: se a auditoria falha, o
// erro volta e os eventos ficam, porque não existe log apagado sem rastro. Sem a
// causa, a limpeza seguinte apaga e audita uma vez.
func TestClearEventsSemAuditoriaNaoApagaOsEventos(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	id, token := a.novoHeartbeat(t)
	a.pingar(t, token, heartbeat.SignalStart)
	a.clk.Advance(time.Minute)
	a.pingar(t, token, heartbeat.SignalSuccess)
	ctx := context.Background()

	a.executar(t, gatilhoRecusarEscrita("BEFORE INSERT ON audit_log FOR EACH ROW"))
	err := svc.ClearEvents(ctx, a.dono, id)
	if err == nil || !strings.Contains(err.Error(), "auditar heartbeat.events_cleared") {
		t.Fatalf("ClearEvents = %v; queria erro da auditoria", err)
	}
	if n := len(a.eventos(t, id)); n != 2 {
		t.Errorf("%d eventos depois da limpeza recusada, queria 2", n)
	}
	if n := a.auditorias(t, id); n != 0 {
		t.Errorf("%d auditorias depois da limpeza recusada, queria 0", n)
	}

	a.executar(t, `DROP TRIGGER recusar_escrita ON audit_log; DROP FUNCTION recusar_escrita();`)
	if err := svc.ClearEvents(ctx, a.dono, id); err != nil {
		t.Fatalf("ClearEvents sem o gatilho: %v", err)
	}
	if n := len(a.eventos(t, id)); n != 0 {
		t.Errorf("%d eventos depois da limpeza, queria 0", n)
	}
	if n := a.auditorias(t, id); n != 1 {
		t.Errorf("%d auditorias depois da limpeza, queria 1", n)
	}
}

// Trocar as tags é apagar e regravar na transação do PATCH: se um dos dois
// falha, o erro volta e nem as tags nem o resto do PATCH (o nome) ficam.
func TestUpdateComTagsRecusadasNaoGravaNada(t *testing.T) {
	casos := []struct {
		nome    string
		criar   string
		erroCom string
	}{
		{
			// Por comando: o DELETE das tags tem de falhar mesmo sem linha.
			nome:    "apagar as tags",
			criar:   gatilhoRecusarEscrita("BEFORE DELETE ON heartbeat_tags FOR EACH STATEMENT"),
			erroCom: "apagar tags do heartbeat",
		},
		{
			nome:    "gravar as tags",
			criar:   gatilhoRecusarEscrita("BEFORE INSERT ON heartbeat_tags FOR EACH STATEMENT"),
			erroCom: "gravar tags do heartbeat",
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			svc := heartbeat.NewService(a.pool, a.clk)
			ctx := context.Background()
			h, _, err := svc.Create(ctx, a.dono, heartbeat.Input{
				Name: "backup", Tags: []string{"noite"}, Schedule: hora, Grace: tolerancia, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			antes := a.linha(t, h.ID)

			a.executar(t, c.criar)
			novo := "backup novo"
			_, err = svc.Update(ctx, a.dono, h.ID, heartbeat.Patch{Name: &novo, SetTags: true, Tags: []string{"dia"}})
			if err == nil || !strings.Contains(err.Error(), c.erroCom) {
				t.Fatalf("Update = %v; queria erro com %q", err, c.erroCom)
			}
			if depois := a.linha(t, h.ID); !reflect.DeepEqual(depois, antes) {
				t.Errorf("a linha mudou com o PATCH recusado:\nantes  %+v\ndepois %+v", antes, depois)
			}
			if tags := a.tags(t, h.ID); !reflect.DeepEqual(tags, []string{"noite"}) {
				t.Errorf("tags depois do PATCH recusado %v, queria [noite]", tags)
			}
		})
	}
}

// Apagar que falha devolve o erro e deixa o heartbeat, os eventos e o token.
func TestDeleteRecusadoDeixaOHeartbeat(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	id, token := a.novoHeartbeat(t)
	a.pingar(t, token, heartbeat.SignalSuccess)
	antes := a.linha(t, id)

	a.executar(t, gatilhoRecusarEscrita("BEFORE DELETE ON heartbeats FOR EACH ROW"))
	err := svc.Delete(context.Background(), a.dono, id)
	if err == nil || errors.Is(err, heartbeat.ErrNotFound) || !strings.Contains(err.Error(), "apagar heartbeat") {
		t.Fatalf("Delete = %v; queria erro ao apagar", err)
	}
	if depois := a.linha(t, id); !reflect.DeepEqual(depois, antes) {
		t.Errorf("a linha mudou com o Delete recusado:\nantes  %+v\ndepois %+v", antes, depois)
	}
	if n := len(a.eventos(t, id)); n != 1 {
		t.Errorf("%d eventos depois do Delete recusado, queria 1", n)
	}
}

// As leituras do log e do histórico devolvem o erro do banco, sem lista
// parcial e sem confundi-lo com "não encontrado".
func TestEventsEIncidentsDevolvemOErroDaLeitura(t *testing.T) {
	t.Run("eventos", func(t *testing.T) {
		a := novoAmbiente(t)
		svc := heartbeat.NewService(a.pool, a.clk)
		id, token := a.novoHeartbeat(t)
		a.pingar(t, token, heartbeat.SignalSuccess)
		a.executar(t, politicaRecusarLeitura("heartbeat_events", "ListHeartbeatEvents"))

		out, err := svc.Events(context.Background(), a.dono, id, nil, 50)
		if err == nil || errors.Is(err, heartbeat.ErrNotFound) || !strings.Contains(err.Error(), "listar eventos") {
			t.Fatalf("Events = %v; queria erro ao listar", err)
		}
		if out != nil {
			t.Errorf("Events devolveu %d eventos com o erro, queria nil", len(out))
		}
	})
	t.Run("incidentes", func(t *testing.T) {
		a := novoAmbiente(t)
		svc := heartbeat.NewService(a.pool, a.clk)
		id, _ := a.novoHeartbeat(t)
		// Só o sondador abre incidente; o Admin, que ignora a RLS, monta o
		// histórico sem depender dessa política.
		if _, err := testdb.Admin(t).Exec(context.Background(),
			`INSERT INTO incidents (id, heartbeat_id, started_at, cause) VALUES ($1, $2, $3, 'heartbeat_late')`,
			uuid.Must(uuid.NewV7()), id, agora.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		a.executar(t, politicaRecusarLeitura("incidents", "ListHeartbeatIncidents"))

		out, err := svc.Incidents(context.Background(), a.dono, id, nil, nil, 50)
		if err == nil || errors.Is(err, heartbeat.ErrNotFound) || !strings.Contains(err.Error(), "listar incidentes") {
			t.Fatalf("Incidents = %v; queria erro ao listar", err)
		}
		if out != nil {
			t.Errorf("Incidents devolveu %d incidentes com o erro, queria nil", len(out))
		}
	})
}

// Cada passo de criar que falha devolve o erro do passo e não deixa heartbeat
// nem tag: o token, que só sai na resposta, não pode ficar gravado sem dono.
func TestCreateComPassoRecusadoNaoGravaNada(t *testing.T) {
	casos := []struct {
		nome    string
		criar   string
		erroCom string
	}{
		{
			nome:    "contar",
			criar:   politicaRecusarLeitura("heartbeats", "CountHeartbeats"),
			erroCom: "contar heartbeats",
		},
		{
			// InsertHeartbeat devolve o erro do banco sem embrulho.
			nome:    "gravar o heartbeat",
			criar:   gatilhoRecusarEscrita("BEFORE INSERT ON heartbeats FOR EACH ROW"),
			erroCom: "passo recusado pelo teste",
		},
		{
			nome:    "gravar as tags",
			criar:   gatilhoRecusarEscrita("BEFORE INSERT ON heartbeat_tags FOR EACH STATEMENT"),
			erroCom: "gravar tags do heartbeat",
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			svc := heartbeat.NewService(a.pool, a.clk)
			// Um heartbeat que já existe: a política só é avaliada em linha lida.
			a.novoHeartbeat(t)
			a.executar(t, c.criar)

			_, token, err := svc.Create(context.Background(), a.dono, heartbeat.Input{
				Name: "novo", Tags: []string{"noite"}, Schedule: hora, Grace: tolerancia, Enabled: true,
			})
			if err == nil || token != "" || !strings.Contains(err.Error(), c.erroCom) {
				t.Fatalf("Create = %q, %v; queria erro com %q e sem token", token, err, c.erroCom)
			}
			if n := a.contarComoDono(t, `SELECT count(*) FROM heartbeats WHERE user_id = $1`); n != 1 {
				t.Errorf("%d heartbeats depois da criação recusada, queria 1", n)
			}
			if n := a.contarComoDono(t, `SELECT count(*) FROM heartbeat_tags ht JOIN heartbeats h ON h.id = ht.heartbeat_id WHERE h.user_id = $1`); n != 0 {
				t.Errorf("%d tags depois da criação recusada, queria 0", n)
			}
		})
	}
}

// A trava por usuário é a primeira coisa de criar: se ela não sai (aqui, outro
// pedido a segura e o contexto é cancelado durante a espera), o erro volta
// embrulhado e nada é gravado. O cancelamento só sai depois que o backend está
// comprovadamente parado na trava: sem prazo de parede, que falharia à toa num
// passo anterior (conexão, BEGIN, set_config) com a máquina carregada.
func TestCreateSemATravaNaoGravaNada(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	fundo := context.Background()
	tx, err := a.pool.Begin(fundo)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(fundo)
	if _, err := tx.Exec(fundo, `SELECT pg_advisory_xact_lock(hashtextextended('stillon.heartbeats:' || $1::uuid::text, 0))`, a.dono); err != nil {
		t.Fatal(err)
	}

	type resultado struct {
		token string
		err   error
	}
	ctx, cancelar := context.WithCancel(fundo)
	defer cancelar()
	fim := make(chan resultado, 1)
	go func() {
		_, token, err := svc.Create(ctx, a.dono, heartbeat.Input{Name: "novo", Schedule: hora, Grace: tolerancia, Enabled: true})
		fim <- resultado{token, err}
	}()
	if !testdb.EsperarBloqueados(t, a.pool, 1, fim) {
		r := <-fim
		t.Fatalf("Create terminou sem esperar a trava: %q, %v", r.token, r.err)
	}
	cancelar()
	r := <-fim
	if r.err == nil || r.token != "" || !strings.Contains(r.err.Error(), "travar heartbeats do usuário") {
		t.Fatalf("Create = %q, %v; queria erro da trava e sem token", r.token, r.err)
	}
	if err := tx.Rollback(fundo); err != nil {
		t.Fatal(err)
	}
	if n := a.contarComoDono(t, `SELECT count(*) FROM heartbeats WHERE user_id = $1`); n != 0 {
		t.Errorf("%d heartbeats depois da criação sem trava, queria 0", n)
	}
}

func (a ambiente) executar(t *testing.T, sql string) {
	t.Helper()
	if _, err := a.pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// contarComoDono conta no escopo do dono uma consulta que recebe o id
// dele em $1.
func (a ambiente) contarComoDono(t *testing.T, sql string) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.dono.String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, sql, a.dono).Scan(&n)
	})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (a ambiente) auditorias(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.dono.String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2 AND entity_id = $3`,
			a.dono, heartbeat.AuditEventsCleared, id).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (a ambiente) tags(t *testing.T, id uuid.UUID) []string {
	t.Helper()
	h, err := heartbeat.NewService(a.pool, a.clk).Get(context.Background(), a.dono, id)
	if err != nil {
		t.Fatal(err)
	}
	return h.Tags
}

// gatilhoRecusarEscrita cria um gatilho que recusa o comando em quando. O
// argumento vem de constantes do próprio teste.
func gatilhoRecusarEscrita(quando string) string {
	return `
	CREATE FUNCTION recusar_escrita() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'passo recusado pelo teste';
	END $$;
	CREATE TRIGGER recusar_escrita ` + quando + `
		EXECUTE FUNCTION recusar_escrita();`
}

// politicaRecusarLeitura faz falhar só a consulta sqlc de nome dado, por uma
// política RESTRICTIVE de SELECT que reconhece o comentário "-- name:" no texto
// do comando. Tabela e nome vêm de constantes do próprio teste.
func politicaRecusarLeitura(tabela, consulta string) string {
	return `
	CREATE FUNCTION recusar_leitura() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
	BEGIN
		IF current_query() LIKE '%-- name: ` + consulta + ` :%' THEN
			RAISE EXCEPTION 'consulta recusada pelo teste';
		END IF;
		RETURN true;
	END $$;
	CREATE POLICY recusar_leitura ON ` + tabela + ` AS RESTRICTIVE FOR SELECT
		USING (recusar_leitura());`
}
