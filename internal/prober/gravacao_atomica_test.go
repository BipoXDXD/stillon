package prober

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// estadoDoCiclo é tudo o que um ciclo de gravar escreve, lido sem passar pelo
// código testado.
type estadoDoCiclo struct {
	Checks       int
	Falhas       int32
	Sucessos     int32
	StreakSince  time.Time
	UltimoCheck  time.Time
	Incidentes   int
	Notificacoes int
	ValidadeTLS  bool
}

func (e estadoDoCiclo) igual(o estadoDoCiclo) bool {
	return e.Checks == o.Checks && e.Falhas == o.Falhas && e.Sucessos == o.Sucessos &&
		e.StreakSince.Equal(o.StreakSince) && e.UltimoCheck.Equal(o.UltimoCheck) &&
		e.Incidentes == o.Incidentes && e.Notificacoes == o.Notificacoes && e.ValidadeTLS == o.ValidadeTLS
}

func (b *bancada) estadoDoCiclo(t *testing.T, alvo uuid.UUID) estadoDoCiclo {
	t.Helper()
	var e estadoDoCiclo
	b.comoDono(t, func(tx pgx.Tx) {
		ctx := context.Background()
		err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM checks WHERE target_id = $1),
			       h.consecutive_failures, h.consecutive_successes, h.streak_since, h.last_checked_at,
			       (SELECT count(*) FROM notifications),
			       EXISTS (SELECT 1 FROM target_tls WHERE target_id = $1)
			FROM target_health h WHERE h.target_id = $1`, alvo).
			Scan(&e.Checks, &e.Falhas, &e.Sucessos, &e.StreakSince, &e.UltimoCheck, &e.Notificacoes, &e.ValidadeTLS)
		if err != nil {
			t.Fatal(err)
		}
	})
	e.Incidentes = len(b.incidentes(t))
	return e
}

// O ciclo de gravar escreve check, histerese, incidente, marca de aviso,
// notificação e, por último, a validade do certificado. Se a última escrita
// falha, nenhuma das anteriores pode ficar: o painel mostraria um incidente
// sem aviso, ou um check que a histerese não contou. O gatilho recusa só a
// escrita em target_tls; o snapshot restaurado no fim do teste o leva embora.
func TestFalhaNaUltimaEscritaDoCicloNaoDeixaNadaGravado(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	b.canal(t, true, true)
	t0 := b.clk.Now()
	b.s.gravar(a, t0, Result{Status: StatusDown, ErrorKind: KindTimeout})
	b.s.gravar(a, t0.Add(time.Minute), Result{Status: StatusDown, ErrorKind: KindTimeout})
	antes := b.estadoDoCiclo(t, a.ID)
	if antes.Checks != 2 || antes.Falhas != 2 || antes.Incidentes != 0 || antes.Notificacoes != 0 || antes.ValidadeTLS {
		t.Fatalf("estado depois de duas falhas = %+v; queria 2 checks, 2 falhas e nada mais", antes)
	}

	if _, err := b.pool.Exec(context.Background(), `
		CREATE FUNCTION recusar_validade() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'validade recusada pelo teste';
		END $$;
		CREATE TRIGGER recusar_validade BEFORE INSERT OR UPDATE ON target_tls
			FOR EACH ROW EXECUTE FUNCTION recusar_validade();`); err != nil {
		t.Fatal(err)
	}
	log := capturarLog(t)
	// A terceira falha abre o incidente, marca e enfileira o aviso e, por
	// trazer certificado, termina gravando a validade.
	terceira := Result{Status: StatusDown, ErrorKind: KindHTTPStatus, StatusCode: 500, CertNotAfter: validadeA}
	b.s.gravar(a, t0.Add(2*time.Minute), terceira)

	if s := log.String(); !strings.Contains(s, "level=ERROR") || !strings.Contains(s, "gravar validade do certificado") ||
		!strings.Contains(s, "validade recusada pelo teste") {
		t.Errorf("log = %q; queria o erro de gravar a validade", s)
	}
	if depois := b.estadoDoCiclo(t, a.ID); !depois.igual(antes) {
		t.Errorf("estado depois da falha na última escrita = %+v; queria o de antes, %+v", depois, antes)
	}

	// Controle: sem o gatilho, o mesmo ciclo grava tudo o que o teste confere,
	// então a igualdade acima não é vazia.
	if _, err := b.pool.Exec(context.Background(), `DROP TRIGGER recusar_validade ON target_tls`); err != nil {
		t.Fatal(err)
	}
	b.s.gravar(a, t0.Add(2*time.Minute), terceira)
	depois := b.estadoDoCiclo(t, a.ID)
	if depois.Checks != 3 || depois.Falhas != 3 || depois.Incidentes != 1 || depois.Notificacoes != 1 || !depois.ValidadeTLS {
		t.Errorf("estado sem o gatilho = %+v; queria 3 checks, 3 falhas, 1 incidente, 1 aviso e a validade", depois)
	}
	if ns := b.notificacoes(t); len(ns) != 1 || ns[0].Evento != incident.EventOpened {
		t.Errorf("notificações = %+v; queria só a abertura", ns)
	}
}
