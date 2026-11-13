package prober

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Os retornos de erro das duas leituras de avaliar: travar a histerese do alvo
// (LockTargetHealth) e buscar o incidente aberto (GetOpenIncident). Nos dois,
// o erro vai ao log com o passo, a transação inteira é desfeita (nem check,
// nem histerese, nem incidente) e a gravação seguinte, sem a causa, avalia
// normalmente.

// Com duas falhas na histerese, a terceira abriria o incidente. Com a trava
// falhando, nada muda; sem a causa, a mesma terceira falha abre o incidente.
func TestAvaliarComFalhaAoTravarAHistereseNaoGravaNada(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	b.semearFalhasDa(t, a)
	checksAntes := len(b.checks(t, a.ID))
	saudeAntes := b.saude(t, a.ID)
	tirar := b.recusarLeitura(t, "target_health", "LockTargetHealth")
	log := capturarLog(t)

	em := b.clk.Now()
	b.s.gravar(a, em, Result{Status: StatusDown, ErrorKind: KindTimeout})

	if !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), "travar histerese do alvo") {
		t.Errorf("log sem o erro de travar a histerese:\n%s", log)
	}
	if n := len(b.checks(t, a.ID)); n != checksAntes {
		t.Errorf("%d checks com a trava falhando, queria %d", n, checksAntes)
	}
	if depois := b.saude(t, a.ID); depois != saudeAntes {
		t.Errorf("target_health = %+v, queria %+v (intacta)", depois, saudeAntes)
	}
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Errorf("incidentes = %+v com a trava falhando, queria nenhum", inc)
	}

	tirar()
	b.s.gravar(a, em.Add(time.Second), Result{Status: StatusDown, ErrorKind: KindTimeout})
	if n := len(b.checks(t, a.ID)); n != checksAntes+1 {
		t.Errorf("%d checks sem a causa, queria %d", n, checksAntes+1)
	}
	if n := b.falhasSeguidas(t, a.ID); n != 3 {
		t.Errorf("%d falhas seguidas sem a causa, queria 3", n)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Errorf("incidentes = %+v sem a causa, queria um aberto", inc)
	}
}

// Com um incidente aberto, a falha ao lê-lo não pode virar "não há incidente
// aberto": nada é gravado, o incidente segue aberto, e sem a causa a mesma
// sonda up conta o primeiro sucesso do fechamento.
func TestAvaliarComFalhaAoBuscarIncidenteAbertoNaoGravaNada(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	a := b.alvoDeAviso(t)
	id, terceira := b.abrirPeloAgendador(t, a)
	checksAntes := len(b.checks(t, a.ID))
	saudeAntes := b.saude(t, a.ID)
	tirar := b.recusarLeitura(t, "incidents", "GetOpenIncident")
	log := capturarLog(t)

	em := terceira.Add(time.Minute)
	b.s.gravar(a, em, Result{Status: StatusUp, StatusCode: 200})

	if !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), "buscar incidente aberto") {
		t.Errorf("log sem o erro de buscar o incidente aberto:\n%s", log)
	}
	if n := len(b.checks(t, a.ID)); n != checksAntes {
		t.Errorf("%d checks com a leitura falhando, queria %d", n, checksAntes)
	}
	if depois := b.saude(t, a.ID); depois != saudeAntes {
		t.Errorf("target_health = %+v, queria %+v (intacta)", depois, saudeAntes)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ID != id || inc[0].ResolvedAt != nil {
		t.Errorf("incidentes = %+v, queria só o %s, aberto", inc, id)
	}

	tirar()
	b.s.gravar(a, em.Add(time.Second), Result{Status: StatusUp, StatusCode: 200})
	if n := len(b.checks(t, a.ID)); n != checksAntes+1 {
		t.Errorf("%d checks sem a causa, queria %d", n, checksAntes+1)
	}
	if s := b.saude(t, a.ID); s.Sucessos != 1 || s.Falhas != 0 {
		t.Errorf("target_health = %+v sem a causa, queria 1 sucesso e 0 falhas", s)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Errorf("incidentes = %+v sem a causa, queria o mesmo ainda aberto (1 de 5 sucessos)", inc)
	}
}

// saudeDoAlvo é a linha de target_health inteira, para conferir que uma
// gravação desfeita não deixou rastro em nenhuma coluna. Instante nulo vira
// o zero de time.Time, para a struct se comparar por valor.
type saudeDoAlvo struct {
	Falhas, Sucessos int32
	Desde, Checado   time.Time
}

func (b *bancada) saude(t *testing.T, alvo uuid.UUID) saudeDoAlvo {
	t.Helper()
	var s saudeDoAlvo
	var desde, checado *time.Time
	b.comoDono(t, func(tx pgx.Tx) {
		err := tx.QueryRow(context.Background(), `
			SELECT consecutive_failures, consecutive_successes, streak_since, last_checked_at
			  FROM target_health WHERE target_id = $1`, alvo).Scan(&s.Falhas, &s.Sucessos, &desde, &checado)
		if err != nil {
			t.Fatal(err)
		}
	})
	if desde != nil {
		s.Desde = desde.UTC()
	}
	if checado != nil {
		s.Checado = checado.UTC()
	}
	return s
}

// recusarLeitura faz falhar só a consulta sqlc de nome dado, por uma política
// RESTRICTIVE de SELECT na tabela que reconhece o comentário "-- name:" no
// texto do comando; as outras consultas da mesma tabela passam. A política só
// é avaliada em linhas lidas, então a tabela precisa ter a linha que a
// consulta busca. Tabela e nome vêm de constantes do próprio teste. Devolve a
// função que tira a política.
//
// Duplica recusarConsulta do PR #361 (scheduler_erros_ciclo_test.go), com
// outro nome para os dois conviverem; unificar depois do merge do #361.
func (b *bancada) recusarLeitura(t *testing.T, tabela, consulta string) func() {
	t.Helper()
	criar := `
	CREATE FUNCTION recusar_leitura() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
	BEGIN
		IF current_query() LIKE '%-- name: ` + consulta + ` :%' THEN
			RAISE EXCEPTION 'leitura recusada pelo teste';
		END IF;
		RETURN true;
	END $$;
	CREATE POLICY recusar_leitura ON ` + tabela + ` AS RESTRICTIVE FOR SELECT
		USING (recusar_leitura());`
	if _, err := b.pool.Exec(context.Background(), criar); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if _, err := b.pool.Exec(context.Background(),
			`DROP POLICY recusar_leitura ON `+tabela+`; DROP FUNCTION recusar_leitura();`); err != nil {
			t.Fatal(err)
		}
	}
}
