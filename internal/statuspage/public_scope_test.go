package statuspage_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// comando é um SQL que o pool mandou ao banco, com a conexão (PID) que o levou.
type comando struct {
	pid  uint32
	sql  string
	args []any
}

// rastro grava tudo o que o pool manda ao banco, inclusive begin e commit.
type rastro struct {
	mu       sync.Mutex
	comandos []comando
}

func (r *rastro) TraceQueryStart(ctx context.Context, conn *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comandos = append(r.comandos, comando{pid: conn.PgConn().PID(), sql: d.SQL, args: d.Args})
	return ctx
}

func (r *rastro) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// transacaoDe devolve os comandos da transação, na mesma conexão, em que
// correu a primeira consulta cujo SQL contém marca: do begin até ela.
func (r *rastro) transacaoDe(t *testing.T, marca string) []comando {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, c := range r.comandos {
		if !strings.Contains(c.sql, marca) {
			continue
		}
		var tx []comando
		for j := i; j >= 0; j-- {
			if r.comandos[j].pid != c.pid {
				continue
			}
			tx = append([]comando{r.comandos[j]}, tx...)
			if strings.EqualFold(strings.TrimSpace(r.comandos[j].sql), "begin") {
				return tx
			}
		}
		t.Fatalf("%s rodou fora de uma transação aberta pelo pool", marca)
	}
	t.Fatalf("o pool do serviço não mandou %s ao banco", marca)
	return nil
}

func poolRastreado(t *testing.T, r *rastro) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = r
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// A consulta de latência não confere se a página está publicada nem se é a
// do slug: isso só vale porque ela roda no escopo de storage.InPublicTx
// (somente leitura, app.public_slug = slug pedido). O teste prende o serviço
// a esse escopo pelo que o pool mandou ao banco: uma transação comum, uma de
// outro escopo ou uma conexão fora do pool do serviço o quebram.
func TestPublicLatencyRunsInsideThePublicScopeOfTheSlug(t *testing.T) {
	a := novoAmbiente(t)
	r := &rastro{}
	svc := statuspage.NewService(poolRastreado(t, r), a.clk)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono, "api")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: alvo, DisplayName: "API"})
	a.check(t, alvo, a.clk.Now().Add(-time.Minute), "up")

	v, err := svc.Public(context.Background(), "empresa")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Items) != 1 || v.Items[0].LatencyMS == nil || *v.Items[0].LatencyMS != 50 {
		t.Fatalf("página = %+v, queria o alvo com 50 ms", v.Items)
	}

	tx := r.transacaoDe(t, "-- name: ListPublicLatency")
	if len(tx) < 2 || !strings.EqualFold(strings.TrimSpace(tx[1].sql), "SET TRANSACTION READ ONLY") {
		t.Errorf("a transação da latência não começa somente leitura: %v", tx)
	}
	escopo := false
	for _, c := range tx[1 : len(tx)-1] {
		s := strings.ToLower(c.sql)
		if strings.Contains(s, "commit") || strings.Contains(s, "rollback") {
			t.Errorf("a transação fechou antes da latência: %q", c.sql)
		}
		if strings.Contains(s, "set_config(") && !strings.Contains(s, "set_config('app.public_slug'") {
			t.Errorf("a transação da latência ligou outro escopo: %q", c.sql)
		}
		if strings.Contains(c.sql, "set_config('app.public_slug'") && len(c.args) == 1 && c.args[0] == "empresa" {
			escopo = true
		}
	}
	if !escopo {
		t.Errorf("a latência rodou sem app.public_slug = \"empresa\" na transação: %v", tx)
	}
}

// insereCheck grava um check bem-sucedido com a latência dada.
func (a ambiente) insereCheck(t *testing.T, alvo uuid.UUID, em time.Time, latencia int32) {
	t.Helper()
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, a.pool, em); err != nil {
		t.Fatal(err)
	}
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em, Status: "up", LatencyMS: latencia,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A página publicada mostra a latência dos próprios alvos, e nenhuma
// latência sai de rascunho ou de slug que não existe, mesmo com checks.
func TestPublicLatencyComesOnlyFromThePublishedPage(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro, terceiro := a.usuario(t), a.usuario(t), a.usuario(t)
	naPagina, fora := a.alvo(t, dono, "api"), a.alvo(t, dono, "fora")
	doOutro, noRascunho := a.alvo(t, outro, "do outro"), a.alvo(t, terceiro, "no rascunho")
	a.publicar(t, dono, "empresa", true, statuspage.Item{TargetID: naPagina, DisplayName: "API"})
	a.publicar(t, outro, "outra", true, statuspage.Item{TargetID: doOutro, DisplayName: "Do outro"})
	a.publicar(t, terceiro, "rascunho", false, statuspage.Item{TargetID: noRascunho, DisplayName: "Rascunho"})
	em := a.clk.Now().Add(-time.Minute)
	a.insereCheck(t, naPagina, em, 111)
	a.insereCheck(t, fora, em, 222)
	a.insereCheck(t, doOutro, em, 333)
	a.insereCheck(t, noRascunho, em, 444)

	v, err := a.svc.Public(ctx, "empresa")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Items) != 1 || v.Items[0].Name != "API" || v.Items[0].LatencyMS == nil || *v.Items[0].LatencyMS != 111 {
		t.Errorf("empresa = %+v, queria só API com 111 ms", v.Items)
	}
	if v, err := a.svc.Public(ctx, "outra"); err != nil || len(v.Items) != 1 || v.Items[0].LatencyMS == nil || *v.Items[0].LatencyMS != 333 {
		t.Errorf("outra = %+v (%v), queria só o alvo do outro com 333 ms (controle)", v.Items, err)
	}
	for _, slug := range []string{"rascunho", "nao-existe"} {
		if v, err := a.svc.Public(ctx, slug); !errors.Is(err, statuspage.ErrNotFound) {
			t.Errorf("Public(%q) = %+v, %v; queria ErrNotFound", slug, v, err)
		}
	}
}
