package prober

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Os retornos de erro de banco do ciclo do agendador: medir a fila no fim do
// Tick, ler e gravar target_tls e travar o alvo antes de gravar o check. Em
// todos, o ciclo segue, o alvo não se perde, o erro fica visível (no retorno
// do Tick ou no log) e nada é gravado pela metade.

// medirFila falha depois do disparo: o Tick devolve o erro e, mesmo assim,
// quantos alvos disparou, porque a sonda já está em voo e grava o check. O
// gauge da fila fica com o último valor bom, e o Tick seguinte, sem a causa,
// sonda o outro alvo vencido.
func TestTickComFalhaAoMedirFilaDevolveOsDisparados(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, 1)
	// Dois vencidos e uma vaga: um fica para a contagem da fila, que assim
	// passa por uma linha e esbarra na política seja qual for a ordem dos
	// filtros no plano.
	a1 := b.alvo(t, srv.URL, nil)
	a2 := b.alvo(t, srv.URL, nil)
	const ultimoValorBom = 42
	b.m.queueDepth.Set(ultimoValorBom)
	tirar := b.recusarConsulta(t, "targets", "CountDueTargets")

	n, err := b.s.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "medir fila") {
		t.Fatalf("Tick = %d, %v; queria erro com %q", n, err, "medir fila")
	}
	if n != 1 {
		t.Errorf("Tick devolveu %d disparos, queria 1: a sonda saiu antes da contagem", n)
	}
	b.s.emVoo.Wait()
	if c := len(b.checks(t, a1)) + len(b.checks(t, a2)); c != 1 {
		t.Errorf("%d checks depois do Tick com erro, queria 1: o alvo disparado não pode se perder", c)
	}
	if v := testutil.ToFloat64(b.m.queueDepth); v != ultimoValorBom {
		t.Errorf("scheduler_queue_depth = %v, queria %d (o último valor bom)", v, ultimoValorBom)
	}

	tirar()
	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick sem a causa = %d, %v; queria 1 disparo (o outro vencido)", n, err)
	}
	b.s.emVoo.Wait()
	if c1, c2 := len(b.checks(t, a1)), len(b.checks(t, a2)); c1 != 1 || c2 != 1 {
		t.Errorf("checks = %d e %d, queria 1 em cada", c1, c2)
	}
	if v := testutil.ToFloat64(b.m.queueDepth); v != 0 {
		t.Errorf("scheduler_queue_depth = %v depois do Tick bom, queria 0", v)
	}
}

// A falha ao ler ou ao gravar target_tls desfaz a transação inteira: nem o
// check nem a validade ficam, o erro vai ao log com o passo, e a gravação
// seguinte, sem a causa, guarda os dois.
func TestValidadeComFalhaNoBancoDesfazAGravacao(t *testing.T) {
	casos := []struct {
		nome    string
		semear  bool
		recusar func(*testing.T, *bancada) func()
		erroCom string
	}{
		{
			"ler", true,
			func(t *testing.T, b *bancada) func() { return b.recusarConsulta(t, "target_tls", "GetTargetTLS") },
			"ler validade do certificado",
		},
		{
			"gravar", false,
			func(t *testing.T, b *bancada) func() { return b.recusarGravacaoTLS(t) },
			"gravar validade do certificado",
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, nil, MaxInFlight)
			alvo := b.alvo(t, urlVelha, nil)
			a := b.linhaDoAlvo(alvo)
			em := b.clk.Now()
			checksAntes := 0
			if c.semear {
				b.s.gravar(a, em, comCertificado(validadeB))
				checksAntes = 1
			}
			antes, tinhaLinha := b.linhaTLS(t, alvo)
			tirar := c.recusar(t, b)
			log := capturarLog(t)

			// validadeA vence antes de validadeB: com a linha semeada, é a
			// leitura nova que substituiria a guardada.
			b.s.gravar(a, em.Add(time.Minute), comCertificado(validadeA))

			if !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), c.erroCom) {
				t.Errorf("log sem o erro %q:\n%s", c.erroCom, log)
			}
			if n := len(b.checks(t, alvo)); n != checksAntes {
				t.Errorf("%d checks, queria %d: o check vai junto com a validade", n, checksAntes)
			}
			if depois, tem := b.linhaTLS(t, alvo); tem != tinhaLinha || depois != antes {
				t.Errorf("target_tls = %+v (existe: %v), queria %+v (existe: %v)", depois, tem, antes, tinhaLinha)
			}

			tirar()
			b.s.gravar(a, em.Add(2*time.Minute), comCertificado(validadeA))
			if n := len(b.checks(t, alvo)); n != checksAntes+1 {
				t.Errorf("%d checks sem a causa, queria %d", n, checksAntes+1)
			}
			if l := b.exigirLinhaTLS(t, alvo); !l.NotAfter.Equal(validadeA) {
				t.Errorf("not_after = %v sem a causa, queria %v", l.NotAfter, validadeA)
			}
		})
	}
}

// Um erro de banco ao travar o alvo não é o alvo apagado: vai ao log como
// erro, a série de latência do alvo continua, nenhum check é gravado, e a
// gravação seguinte, sem a causa, passa.
func TestConferirSondaComFalhaNoBancoNaoEsqueceOAlvo(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	b.m.observe(alvo, Result{Status: StatusUp, StatusCode: 200})
	tirar := b.recusarConsulta(t, "targets", "LockTargetProbeSpec")
	log := capturarLog(t)

	b.s.gravar(a, b.clk.Now(), Result{Status: StatusUp, StatusCode: 200})

	if !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), "conferir o alvo da sonda") {
		t.Errorf("log sem o erro de conferir o alvo:\n%s", log)
	}
	if !slices.Contains(b.m.alvosVistos(), alvo) {
		t.Error("a série do alvo foi esquecida: um erro de banco não é alvo apagado")
	}
	if n := len(b.checks(t, alvo)); n != 0 {
		t.Errorf("%d checks com a trava falhando, queria 0", n)
	}

	tirar()
	b.s.gravar(a, b.clk.Now(), Result{Status: StatusUp, StatusCode: 200})
	if n := len(b.checks(t, alvo)); n != 1 {
		t.Errorf("%d checks sem a causa, queria 1", n)
	}
}

// recusarConsulta faz falhar só a consulta sqlc de nome dado, por uma política
// RESTRICTIVE de SELECT na tabela que reconhece o comentário "-- name:" no
// texto do comando. As outras consultas da mesma tabela passam. Tabela e nome
// vêm de constantes do próprio teste. Devolve a função que tira a política.
func (b *bancada) recusarConsulta(t *testing.T, tabela, consulta string) func() {
	t.Helper()
	criar := `
	CREATE FUNCTION recusar_consulta() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
	BEGIN
		IF current_query() LIKE '%-- name: ` + consulta + ` :%' THEN
			RAISE EXCEPTION 'consulta recusada pelo teste';
		END IF;
		RETURN true;
	END $$;
	CREATE POLICY recusar_consulta ON ` + tabela + ` AS RESTRICTIVE FOR SELECT
		USING (recusar_consulta());`
	if _, err := b.pool.Exec(context.Background(), criar); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if _, err := b.pool.Exec(context.Background(),
			`DROP POLICY recusar_consulta ON `+tabela+`; DROP FUNCTION recusar_consulta();`); err != nil {
			t.Fatal(err)
		}
	}
}

// recusarGravacaoTLS faz falhar o INSERT e o UPDATE de target_tls. Devolve a
// função que tira o gatilho.
func (b *bancada) recusarGravacaoTLS(t *testing.T) func() {
	t.Helper()
	const criar = `
	CREATE FUNCTION recusar_tls() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		RAISE EXCEPTION 'validade recusada pelo teste';
	END $$;
	CREATE TRIGGER recusar_tls BEFORE INSERT OR UPDATE ON target_tls
		FOR EACH ROW EXECUTE FUNCTION recusar_tls();`
	if _, err := b.pool.Exec(context.Background(), criar); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if _, err := b.pool.Exec(context.Background(),
			`DROP TRIGGER recusar_tls ON target_tls; DROP FUNCTION recusar_tls();`); err != nil {
			t.Fatal(err)
		}
	}
}
