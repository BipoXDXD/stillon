package prober

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Fixam o contrato de "marcar o incidente como avisado e enfileirar o aviso"
// no caminho do agendador: marca gravada, aviso só nos canais entregáveis,
// nenhum aviso novo quando o incidente já foi avisado (n == 0), e a transação
// do check inteira desfeita quando marcar ou enfileirar falha.

// comoSondador roda fn numa transação com o escopo do sondador (a RLS de
// incidents não deixa a conexão do teste ver nada sem escopo).
func (b *bancada) comoSondador(t *testing.T, fn func(pgx.Tx)) {
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

// marcasDoIncidente lê open_notified_at e close_notified_at sem passar pelo
// código testado.
func (b *bancada) marcasDoIncidente(t *testing.T, id uuid.UUID) (abertura, fechamento *time.Time) {
	t.Helper()
	b.comoSondador(t, func(tx pgx.Tx) {
		err := tx.QueryRow(context.Background(),
			`SELECT open_notified_at, close_notified_at FROM incidents WHERE id = $1`, id).Scan(&abertura, &fechamento)
		if err != nil {
			t.Fatal(err)
		}
	})
	return abertura, fechamento
}

// abrirPeloAgendador faz três falhas seguidas e devolve o id do incidente
// aberto e o instante da terceira falha.
func (b *bancada) abrirPeloAgendador(t *testing.T, a db.ClaimDueTargetsRow) (uuid.UUID, time.Time) {
	t.Helper()
	t0 := b.clk.Now()
	var terceira time.Time
	for i := range 3 {
		terceira = t0.Add(time.Duration(i) * time.Minute)
		b.s.gravar(a, terceira, Result{Status: StatusDown, ErrorKind: KindTimeout})
	}
	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Fatalf("três down: incidentes = %+v, queria um aberto", inc)
	}
	return inc[0].ID, terceira
}

func (b *bancada) fecharPeloAgendador(t *testing.T, a db.ClaimDueTargetsRow, de time.Time) (primeiroSucesso, ultimoSucesso time.Time) {
	t.Helper()
	primeiroSucesso = de
	for i := range 5 {
		ultimoSucesso = de.Add(time.Duration(i) * time.Minute)
		b.s.gravar(a, ultimoSucesso, Result{Status: StatusUp, StatusCode: 200})
	}
	return primeiroSucesso, ultimoSucesso
}

func (b *bancada) alvoDeAviso(t *testing.T) db.ClaimDueTargetsRow {
	t.Helper()
	alvo := b.alvo(t, "https://exemplo.com", nil)
	return db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}
}

func TestSchedulerMarcaEEnfileiraAAbertura(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	canal := b.canal(t, true, true)

	id, terceira := b.abrirPeloAgendador(t, a)

	abertura, fechamento := b.marcasDoIncidente(t, id)
	if abertura == nil || !abertura.Equal(terceira) || fechamento != nil {
		t.Errorf("marcas = %v, %v; queria abertura em %v e fechamento nulo", abertura, fechamento, terceira)
	}
	ns := b.notificacoes(t)
	if len(ns) != 1 || ns[0].Canal != canal || ns[0].Evento != incident.EventOpened {
		t.Errorf("notificações = %+v; queria só a abertura no canal %s", ns, canal)
	}
}

func TestSchedulerMarcaEEnfileiraOFechamento(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	canal := b.canal(t, true, true)
	id, terceira := b.abrirPeloAgendador(t, a)

	_, quinto := b.fecharPeloAgendador(t, a, terceira.Add(time.Minute))

	abertura, fechamento := b.marcasDoIncidente(t, id)
	if abertura == nil || !abertura.Equal(terceira) || fechamento == nil || !fechamento.Equal(quinto) {
		t.Errorf("marcas = %v, %v; queria abertura em %v e fechamento em %v", abertura, fechamento, terceira, quinto)
	}
	ns := b.notificacoes(t)
	if len(ns) != 2 || ns[1].Canal != canal || ns[1].Evento != incident.EventResolved {
		t.Errorf("notificações = %+v; queria abertura e fechamento no canal %s", ns, canal)
	}
}

// n == 0: quem vê o fechamento já avisado resolve o incidente, mas não
// enfileira de novo (spec §8, idempotência no banco).
func TestSchedulerFechamentoJaAvisadoNaoEnfileira(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	b.canal(t, true, true)
	id, terceira := b.abrirPeloAgendador(t, a)
	avisadoAntes := terceira.Add(-time.Hour)
	b.comoSondador(t, func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(),
			`UPDATE incidents SET close_notified_at = $2 WHERE id = $1`, id, avisadoAntes); err != nil {
			t.Fatal(err)
		}
	})

	b.fecharPeloAgendador(t, a, terceira.Add(time.Minute))

	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt == nil {
		t.Fatalf("incidentes = %+v; queria o mesmo, resolvido", inc)
	}
	if _, fechamento := b.marcasDoIncidente(t, id); fechamento == nil || !fechamento.Equal(avisadoAntes) {
		t.Errorf("close_notified_at = %v; queria a marca anterior %v intacta", fechamento, avisadoAntes)
	}
	if ns := b.notificacoes(t); len(ns) != 1 || ns[0].Evento != incident.EventOpened {
		t.Errorf("notificações = %+v; queria só a abertura", ns)
	}
}

// Qualquer falha no marcar ou no enfileirar desfaz a transação do check
// inteira (incidente, histerese e check), e o erro vai ao log com o motivo.
func TestSchedulerFalhaAoAvisarDesfazATransacao(t *testing.T) {
	casos := []struct {
		nome    string
		fecha   bool
		gatilho string
		texto   string
	}{
		{"abertura: marcar", false, gatilhoMarcar("open_notified_at"), "marcar abertura notificada"},
		{"abertura: enfileirar", false, gatilhoEnfileirar, "enfileirar notificação"},
		{"fechamento: marcar", true, gatilhoMarcar("close_notified_at"), "marcar fechamento notificado"},
		{"fechamento: enfileirar", true, gatilhoEnfileirar, "enfileirar notificação"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, nil, MaxInFlight)
			a := b.alvoDeAviso(t)
			b.canal(t, true, true)
			var terceira time.Time
			if c.fecha {
				_, terceira = b.abrirPeloAgendador(t, a)
			}
			if _, err := b.pool.Exec(context.Background(), c.gatilho); err != nil {
				t.Fatal(err)
			}
			log := capturarLog(t)

			if c.fecha {
				b.fecharPeloAgendador(t, a, terceira.Add(time.Minute))
			} else {
				b.abrirSemExigirIncidente(t, a)
			}

			if s := log.String(); !strings.Contains(s, "level=ERROR") || !strings.Contains(s, "gravar check") || !strings.Contains(s, c.texto) {
				t.Errorf("log = %q; queria o erro de gravar check com %q", s, c.texto)
			}
			inc := b.incidentes(t)
			ns := b.notificacoes(t)
			if c.fecha {
				if len(inc) != 1 || inc[0].ResolvedAt != nil || len(ns) != 1 || ns[0].Evento != incident.EventOpened {
					t.Errorf("incidentes = %+v, notificações = %+v; queria o incidente ainda aberto e só a abertura avisada", inc, ns)
				}
			} else if len(inc) != 0 || len(ns) != 0 {
				t.Errorf("incidentes = %+v, notificações = %+v; queria nada gravado", inc, ns)
			}
		})
	}
}

// abrirSemExigirIncidente faz as três falhas sem exigir que o incidente exista
// (o caso de erro desfaz a transação que o abriria).
func (b *bancada) abrirSemExigirIncidente(t *testing.T, a db.ClaimDueTargetsRow) {
	t.Helper()
	t0 := b.clk.Now()
	for i := range 3 {
		b.s.gravar(a, t0.Add(time.Duration(i)*time.Minute), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}
}

const gatilhoEnfileirar = `
	CREATE FUNCTION recusar_aviso() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'aviso recusado pelo teste';
	END $$;
	CREATE TRIGGER recusar_aviso BEFORE INSERT ON notifications
		FOR EACH ROW EXECUTE FUNCTION recusar_aviso();`

// gatilhoMarcar recusa o UPDATE que preenche a coluna de marca dada. A
// coluna vem de uma constante do próprio teste.
func gatilhoMarcar(coluna string) string {
	return `
	CREATE FUNCTION recusar_marca() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'marca recusada pelo teste';
	END $$;
	CREATE TRIGGER recusar_marca BEFORE UPDATE ON incidents
		FOR EACH ROW WHEN (OLD.` + coluna + ` IS NULL AND NEW.` + coluna + ` IS NOT NULL)
		EXECUTE FUNCTION recusar_marca();`
}
